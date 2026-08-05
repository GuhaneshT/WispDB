package sstable

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type Reader struct {
	file *os.File
	index []IndexEntry
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
		r.index[i] = IndexEntry{SeriesID: binary.LittleEndian.Uint64(data[start : start+8]), Timestamp: int64(binary.LittleEndian.Uint64(data[start+8:start+16])), Offset: binary.LittleEndian.Uint64(data[start+16:start+24]), Size: binary.LittleEndian.Uint32(data[start+24 : start+28])}
	}
	return nil
}
