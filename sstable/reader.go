package sstable

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sort"
)

type Reader struct {
	file        *os.File
	index       []IndexEntry
	entryCount  uint64
	bloomFilter *BloomFilter
}

func OpenReader(path string) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader := &Reader{file: file}
	if err := reader.readFooter(); err != nil {
		file.Close()
		return nil, err
	}
	return reader, nil
}

func (r *Reader) readFooter() error {
	info, err := r.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < FooterSize {
		return fmt.Errorf("sstable too small")
	}
	footer := make([]byte, FooterSize)
	if _, err := r.file.ReadAt(footer, info.Size()-int64(FooterSize)); err != nil {
		return err
	}
	magic := binary.LittleEndian.Uint32(footer[0:4])
	if magic != Magic {
		return fmt.Errorf("invalid sstable magic")
	}
	version := footer[4]
	if version != Version {
		return fmt.Errorf("unsupported sstable version: %d", version)
	}
	indexOffset := binary.LittleEndian.Uint64(footer[8:16])
	indexSize := binary.LittleEndian.Uint64(footer[16:24])
	bloomOffset := binary.LittleEndian.Uint64(footer[24:32])
	bloomSize := binary.LittleEndian.Uint64(footer[32:40])
	r.entryCount = binary.LittleEndian.Uint64(footer[40:48])
	checksum := binary.LittleEndian.Uint32(footer[48:52])
	if err := r.readIndex(indexOffset, indexSize); err != nil {
		return err
	}
	if err := r.readBloomFilter(bloomOffset, bloomSize); err != nil {
		return err
	}
	checksumSize := indexOffset + indexSize
	if bloomSize > 0 {
		checksumSize = bloomOffset + bloomSize
	}
	return r.verifyChecksum(checksumSize, checksum)
}

func (r *Reader) readBloomFilter(offset uint64, size uint64) error {
	if size == 0 {
		return nil
	}
	data := make([]byte, size)
	if _, err := r.file.ReadAt(data, int64(offset)); err != nil {
		return err
	}
	filter, err := DecodeBloomFilter(data)
	if err != nil {
		return err
	}
	r.bloomFilter = filter
	return nil
}

// MayContain reports whether seriesID could be present in this SSTable.
// A false result is definitive; a true result requires checking the data.
func (r *Reader) MayContain(seriesID uint64) bool {
	if r.bloomFilter == nil {
		return true
	}
	return r.bloomFilter.MayContain(seriesID)
}

func (r *Reader) verifyChecksum(dataSize uint64, want uint32) error {
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, io.NewSectionReader(r.file, 0, int64(dataSize))); err != nil {
		return err
	}
	if got := h.Sum32(); got != want {
		return fmt.Errorf("sstable checksum mismatch: got %d, want %d", got, want)
	}
	return nil
}

func (r *Reader) readIndex(offset uint64, size uint64) error {
	if size%28 != 0 {
		return fmt.Errorf("corrupt index size")
	}
	data := make([]byte, size)
	if _, err := r.file.ReadAt(data, int64(offset)); err != nil {
		return err
	}
	count := size / 28
	r.index = make([]IndexEntry, count)
	for i := uint64(0); i < count; i++ {
		start := i * 28
		r.index[i] = IndexEntry{SeriesID: binary.LittleEndian.Uint64(data[start : start+8]), Timestamp: int64(binary.LittleEndian.Uint64(data[start+8 : start+16])), Offset: binary.LittleEndian.Uint64(data[start+16 : start+24]), Size: binary.LittleEndian.Uint32(data[start+24 : start+28])}
	}
	return nil
}

func (r *Reader) Get(seriesID uint64, timestamp int64) ([]byte, bool,bool, 	 error) {
	if !r.MayContain(seriesID) {
		return nil, false, false, nil
	}
	block, found := r.findBlock(seriesID, timestamp)
	if !found {
		return nil, false,false, nil
	}
	entries, err := r.readBlock(block)
	if err != nil {
		return nil, false,false, err
	}
	for _, entry := range entries {
		if entry.SeriesID == seriesID && entry.Timestamp == timestamp {
			if entry.Deleted {
				return nil, false, true, nil
			}
			return entry.Value, true, false, nil
		}
	}
	return nil, false, false, nil
}

// func (r *Reader) Get(seriesID uint64, timestamp int64) ([]byte, bool, bool, error) {
// 	block, found := r.findBlock(seriesID, timestamp)

// 	if !found {
// 		fmt.Printf("GET: block not found\n")
// 		return nil, false, false, nil
// 	}

// 	entries, err := r.readBlock(block)
// 	if err != nil {
// 		return nil, false, false, err
// 	}

// 	for _, entry := range entries {
// 		if entry.SeriesID == seriesID && entry.Timestamp == timestamp {

// 			fmt.Printf(
// 				"GET MATCH: series=%d timestamp=%d deleted=%v\n",
// 				entry.SeriesID,
// 				entry.Timestamp,
// 				entry.Deleted,
// 			)

// 			if entry.Deleted {
// 				fmt.Println("GET RETURN: found=false deleted=true")
// 				return nil, false, true, nil
// 			}

// 			fmt.Println("GET RETURN: found=true deleted=false")
// 			return entry.Value, true, false, nil
// 		}
// 	}

// 	fmt.Println("GET: matching entry not found")
// 	return nil, false, false, nil
// }

func (r *Reader) Close() error {
	return r.file.Close()
}

// findBlock returns the last block whose first key is <= (seriesID,
// timestamp) — the only block that could contain the target entry, since
// each index entry is keyed on its block's first entry and the index is
// sorted ascending by construction (blocks are written in key order).
func (r *Reader) findBlock(seriesID uint64, timestamp int64) (IndexEntry, bool) {
	if len(r.index) == 0 {
		return IndexEntry{}, false
	}
	target := Entry{SeriesID: seriesID, Timestamp: timestamp}
	// i is the first index entry strictly greater than target; the
	// candidate block, if any, is the one just before it.
	i := sort.Search(len(r.index), func(i int) bool {
		entry := r.index[i]
		return compareEntries(Entry{SeriesID: entry.SeriesID, Timestamp: entry.Timestamp}, target) > 0
	})
	if i == 0 {
		return IndexEntry{}, false
	}
	return r.index[i-1], true
}

func (r *Reader) readBlock(indexEntry IndexEntry) ([]Entry, error) {
	data := make([]byte, indexEntry.Size)
	if _, err := r.file.ReadAt(data, int64(indexEntry.Offset)); err != nil {
		return nil, err
	}
	var entries []Entry
	for pos := 0; pos < len(data); {
		if pos+4 > len(data) {
			return nil, fmt.Errorf("corrupt block entry length")
		}
		entryLength := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if pos+entryLength > len(data) || entryLength < 21 {
			return nil, fmt.Errorf("corrupt block entry")
		}
		seriesID := binary.LittleEndian.Uint64(data[pos : pos+8])
		timestamp := int64(binary.LittleEndian.Uint64(data[pos+8 : pos+16]))
		deleted := data[pos+16] != 0
		// fmt.Printf(
		// 	"DEBUG: series=%d timestamp=%d deleted=%v\n",
		// 	seriesID,
		// 	timestamp,
		// 	deleted,
		// )
		valueLength := int(binary.LittleEndian.Uint32(data[pos+17 : pos+21]))
		valueStart := pos + 21
		valueEnd := valueStart + valueLength
		if valueEnd > pos+entryLength {
			return nil, fmt.Errorf("corrupt block value")
		}
		value := append([]byte(nil), data[valueStart:valueEnd]...)
		entries = append(entries, Entry{SeriesID: seriesID, Timestamp: timestamp, Deleted: deleted, Value: value})
		pos += entryLength
	}
	return entries, nil
}

func compareEntries(a Entry, b Entry) int {
	if a.SeriesID < b.SeriesID {
		return -1
	}
	if a.SeriesID > b.SeriesID {
		return 1
	}
	if a.Timestamp < b.Timestamp {
		return -1
	}
	if a.Timestamp > b.Timestamp {
		return 1
	}
	return 0
}
