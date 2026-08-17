package sstable

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"

	"github.com/golang/snappy"
)

type Writer struct {
	file      *os.File
	out       io.Writer
	hash      hash.Hash32
	blockSize uint32
	currentBlock *Block
	blockBuf     []byte
	index        []IndexEntry
	entryCount uint64
	offset      uint64
	bloomFilter *BloomFilter
	bloomKeys []uint64
}

func NewWriter(path string, blockSize uint32) (*Writer, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	} 
	checksum := crc32.NewIEEE()
	writer := &Writer{file: file, out: io.MultiWriter(file, checksum), hash: checksum, blockSize: blockSize, currentBlock: &Block{}, offset: 0}
	if err := writer.writeHeader(); err != nil {
		file.Close()
		return nil, err
	}
	return writer, nil
}

func (w *Writer) writeHeader() error {
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], Magic)
	header[4] = Version
	binary.LittleEndian.PutUint32(header[5:9], w.blockSize)
	// bytes 9-11 reserved
	n, err := w.out.Write(header)
	if err != nil {
		return err
	}
	w.offset += uint64(n)
	return nil
}

func (w *Writer) Add(entry Entry) error {
	entrySize := entryEncodedSize(entry)
	if len(w.currentBlock.Entries) > 0 &&
		w.currentBlock.Size+entrySize > w.blockSize {
		if err := w.flushBlock(); err != nil {
			return err
		}
	}
	w.currentBlock.Add(entry)
	w.entryCount++
	w.bloomKeys = append(w.bloomKeys, entry.SeriesID)
	return nil
}

func (w *Writer) flushBlock() error {
	if len(w.currentBlock.Entries) == 0 {
		return nil
	}
	data := w.currentBlock.EncodeWithCompression(&w.blockBuf)
	offset := w.offset
	n, err := w.out.Write(data)
	if err != nil {
		return err
	}
	first := w.currentBlock.Entries[0]
	w.index = append(w.index, IndexEntry{SeriesID: first.SeriesID, Timestamp: first.Timestamp, Offset: offset, Size: uint32(n)})
	w.offset += uint64(n)
	w.currentBlock = &Block{}
	return nil
}

func (w *Writer) buildBloomFilter() {
	w.bloomFilter = NewBloomFilter(w.entryCount, 0.01)

	for _, key := range w.bloomKeys {
		w.bloomFilter.Add(key)
	}
}

func (w *Writer) writeBloomFilter() (uint64,uint64,error) {
	bloomOffset := w.offset
	if w.bloomFilter == nil || len(w.bloomFilter.Bits) == 0 {
		return 0,0,nil
	}

	n, err := w.out.Write(w.bloomFilter.Encode())
	if err != nil {
		return 0,0,err
	}
	w.offset += uint64(n)
	return bloomOffset,uint64(n),nil
}
func (w *Writer) writeIndex() (uint64, uint64, error) {
	indexOffset := w.offset
	var buf []byte
	for _, entry := range w.index {
		record := make([]byte, 28)
		binary.LittleEndian.PutUint64(record[0:8], entry.SeriesID)
		binary.LittleEndian.PutUint64(record[8:16], uint64(entry.Timestamp))
		binary.LittleEndian.PutUint64(record[16:24], entry.Offset)
		binary.LittleEndian.PutUint32(record[24:28], entry.Size)
		buf = append(buf, record...)
	}
	n, err := w.out.Write(buf)
	if err != nil {
		return 0, 0, err
	}
	w.offset += uint64(n)
	return indexOffset, uint64(n), nil
}

func (w *Writer) writeFooter(indexOffset uint64, indexSize uint64, bloomOffset uint64, bloomSize uint64) error {
	footer := make([]byte, FooterSize)
	binary.LittleEndian.PutUint32(footer[0:4], Magic)
	footer[4] = Version
	binary.LittleEndian.PutUint64(footer[8:16], indexOffset)
	binary.LittleEndian.PutUint64(footer[16:24], indexSize)
	binary.LittleEndian.PutUint64(footer[24:32],bloomOffset)
	binary.LittleEndian.PutUint64(footer[32:40],bloomSize)
	binary.LittleEndian.PutUint64(footer[40:48], w.entryCount)
	binary.LittleEndian.PutUint32(footer[48:52], w.hash.Sum32())
	n, err := w.file.Write(footer)
	if err != nil {
		return err
	}
	w.offset += uint64(n)
	return nil
}

func (w *Writer) Close() error {
	if err := w.flushBlock(); err != nil {
		return err
	}
	w.buildBloomFilter()
	indexOffset, indexSize, err := w.writeIndex()
	if err != nil {
		return err
	}
	bloomOffset, bloomSize, err := w.writeBloomFilter()
	if err != nil {
		return err
	}
	if err := w.writeFooter(indexOffset, indexSize,bloomOffset,bloomSize); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	return w.file.Close()
}
