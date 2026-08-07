package sstable

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type Reader struct {
	file       *os.File
	index      []IndexEntry
	entryCount uint64
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
	_, err = r.file.Seek(-FooterSize, io.SeekEnd)
	if err != nil {
		return err
	}
	footer := make([]byte, FooterSize)
	if _, err := io.ReadFull(r.file, footer); err != nil {
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
	r.entryCount = binary.LittleEndian.Uint64(footer[24:32])
	return r.readIndex(indexOffset, indexSize)
}

func (r *Reader) readIndex(offset uint64, size uint64) error {
	if size%28 != 0 {
		return fmt.Errorf("corrupt index size")
	}
	if _, err := r.file.Seek(int64(offset), io.SeekStart); err != nil {
		return err
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r.file, data); err != nil {
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

func (r *Reader) Get(seriesID uint64, timestamp int64) ([]byte, bool, error) {
	block, found := r.findBlock(seriesID, timestamp)
	if !found {
		return nil, false, nil
	}
	entries, err := r.readBlock(block)
	if err != nil {
		return nil, false, err
	}
	for _, entry := range entries {
		if entry.SeriesID == seriesID && entry.Timestamp == timestamp {
			return entry.Value, true, nil
		}
	}
	return nil, false, nil
}

func (r *Reader) Close() error {
	return r.file.Close()
}

func (r *Reader) findBlock(seriesID uint64, timestamp int64) (IndexEntry, bool) {
	if len(r.index) == 0 {
		return IndexEntry{}, false
	}
	target := Entry{SeriesID: seriesID, Timestamp: timestamp}
	candidate := -1
	for i, entry := range r.index {
		if compareEntries(Entry{SeriesID: entry.SeriesID, Timestamp: entry.Timestamp}, target) > 0 {
			break
		}
		candidate = i
	}
	if candidate == -1 {
		return IndexEntry{}, false
	}
	return r.index[candidate], true
}

func (r *Reader) readBlock(indexEntry IndexEntry) ([]Entry, error) {
	data := make([]byte, indexEntry.Size)
	if _, err := r.file.Seek(int64(indexEntry.Offset), io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(r.file, data); err != nil {
		return nil, err
	}
	var entries []Entry
	for pos := 0; pos < len(data); {
		if pos+4 > len(data) {
			return nil, fmt.Errorf("corrupt block entry length")
		}
		entryLength := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if pos+entryLength > len(data) || entryLength < 20 {
			return nil, fmt.Errorf("corrupt block entry")
		}
		seriesID := binary.LittleEndian.Uint64(data[pos : pos+8])
		timestamp := int64(binary.LittleEndian.Uint64(data[pos+8 : pos+16]))
		valueLength := int(binary.LittleEndian.Uint32(data[pos+16 : pos+20]))
		valueStart := pos + 20
		valueEnd := valueStart + valueLength
		if valueEnd > pos+entryLength {
			return nil, fmt.Errorf("corrupt block value")
		}
		value := append([]byte(nil), data[valueStart:valueEnd]...)
		entries = append(entries, Entry{SeriesID: seriesID, Timestamp: timestamp, Value: value})
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
