package sstable

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeValidSSTable writes a small multi-block SSTable and returns its path.
func writeValidSSTable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.sst")

	writer, err := NewWriter(path, 64)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	entries := []Entry{
		{SeriesID: 1, Timestamp: 10, Value: []byte("alpha")},
		{SeriesID: 1, Timestamp: 20, Value: []byte("beta")},
		{SeriesID: 2, Timestamp: 30, Value: []byte("gamma")},
	}
	for _, entry := range entries {
		if err := writer.Add(entry); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return path
}

// flipByte XORs the byte at offset with 0xFF.
func flipByte(t *testing.T, path string, offset int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer file.Close()
	var b [1]byte
	if _, err := file.ReadAt(b[:], offset); err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	b[0] ^= 0xFF
	if _, err := file.WriteAt(b[:], offset); err != nil {
		t.Fatalf("WriteAt() error = %v", err)
	}
}

func TestOpenReaderDetectsCorruptBlock(t *testing.T) {
	path := writeValidSSTable(t)
	// Byte 12 is just past the 12-byte header, inside the first block's
	// entry-length/seriesID bytes.
	flipByte(t, path, 12)

	_, err := OpenReader(path)
	if err == nil {
		t.Fatalf("OpenReader() error = nil, want checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("OpenReader() error = %v, want checksum mismatch", err)
	}
}

func TestOpenReaderDetectsCorruptIndex(t *testing.T) {
	path := writeValidSSTable(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	// The last byte before the footer belongs to the index.
	flipByte(t, path, info.Size()-int64(FooterSize)-1)

	_, err = OpenReader(path)
	if err == nil {
		t.Fatalf("OpenReader() error = nil, want checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("OpenReader() error = %v, want checksum mismatch", err)
	}
}

func TestOpenReaderDetectsStaleZeroChecksum(t *testing.T) {
	path := writeValidSSTable(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	// Zero out just the footer's checksum field (its last 4 bytes), leaving
	// the data and index untouched, to confirm verification isn't a no-op.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer file.Close()
	if _, err := file.WriteAt(make([]byte, 4), info.Size()-4); err != nil {
		t.Fatalf("WriteAt() error = %v", err)
	}

	_, err = OpenReader(path)
	if err == nil {
		t.Fatalf("OpenReader() error = nil, want checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("OpenReader() error = %v, want checksum mismatch", err)
	}
}

func TestWriterReaderRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sst")

	writer, err := NewWriter(path, 64)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	entries := []Entry{
		{SeriesID: 1, Timestamp: 10, Value: []byte("alpha")},
		{SeriesID: 1, Timestamp: 20, Value: []byte("beta")},
		{SeriesID: 2, Timestamp: 30, Value: []byte("gamma")},
	}
	for _, entry := range entries {
		if err := writer.Add(entry); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	for _, want := range entries {
		got, found, _, err := reader.Get(want.SeriesID, want.Timestamp)
		if err != nil {
			t.Fatalf("Get(%d, %d) error = %v", want.SeriesID, want.Timestamp, err)
		}
		if !found {
			t.Fatalf("Get(%d, %d) did not find entry", want.SeriesID, want.Timestamp)
		}
		if !bytes.Equal(got, want.Value) {
			t.Fatalf("Get(%d, %d) = %q, want %q", want.SeriesID, want.Timestamp, got, want.Value)
		}
	}

	if _, found,_, err := reader.Get(999, 999); err != nil {
		t.Fatalf("Get() unexpected error = %v", err)
	} else if found {
		t.Fatalf("Get() found non-existent record")
	}
}

func TestWriterReaderBloomFilter(t *testing.T) {
	path := writeValidSSTable(t)

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if reader.bloomFilter == nil {
		t.Fatal("reader did not load a bloom filter")
	}
	for _, seriesID := range []uint64{1, 2} {
		if !reader.MayContain(seriesID) {
			t.Errorf("MayContain(%d) = false, want true for inserted series", seriesID)
		}
	}
	if reader.MayContain(999) {
		// Not a hard guarantee (false positives are allowed), but with
		// this few keys and this filter size it should not occur.
		t.Error("MayContain(999) = true for a series never inserted")
	}
}

func TestFindBlockBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sst")

	writer, err := NewWriter(path, 32)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	// Small block size forces one entry per block, giving a multi-entry index
	// to binary-search over.
	entries := []Entry{
		{SeriesID: 1, Timestamp: 10, Value: []byte("a")},
		{SeriesID: 2, Timestamp: 20, Value: []byte("b")},
		{SeriesID: 3, Timestamp: 30, Value: []byte("c")},
		{SeriesID: 4, Timestamp: 40, Value: []byte("d")},
	}
	for _, entry := range entries {
		if err := writer.Add(entry); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if len(reader.index) < 2 {
		t.Fatalf("expected multiple index blocks, got %d", len(reader.index))
	}

	// Before the first block's key entirely.
	if _, found := reader.findBlock(0, 0); found {
		t.Fatal("findBlock() found a block before the first key, want false")
	}
	// Exact match on the first block's key.
	if block, found := reader.findBlock(1, 10); !found || block.SeriesID != 1 {
		t.Fatalf("findBlock(1, 10) = %+v, %v, want first block", block, found)
	}
	// Exact match on the last block's key.
	if block, found := reader.findBlock(4, 40); !found || block.SeriesID != 4 {
		t.Fatalf("findBlock(4, 40) = %+v, %v, want last block", block, found)
	}
	// Past the last block's key entirely — still lands on the last block,
	// which Get()'s subsequent entry scan then correctly reports as absent.
	if block, found := reader.findBlock(99, 99); !found || block.SeriesID != 4 {
		t.Fatalf("findBlock(99, 99) = %+v, %v, want last block", block, found)
	}

	for _, want := range entries {
		got, found, _, err := reader.Get(want.SeriesID, want.Timestamp)
		if err != nil || !found || !bytes.Equal(got, want.Value) {
			t.Fatalf("Get(%d, %d) = %q, %v, %v, want %q, true, nil", want.SeriesID, want.Timestamp, got, found, err, want.Value)
		}
	}
}

func TestWriterReaderTombstoneRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sst")

	writer, err := NewWriter(path, 64)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	entries := []Entry{
		{SeriesID: 1, Timestamp: 10, Value: []byte("alpha")},
		{SeriesID: 1, Timestamp: 20, Deleted: true},
	}
	for _, entry := range entries {
		if err := writer.Add(entry); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, found,deleted, err := reader.Get(1, 20); err != nil {
		t.Fatalf("Get() unexpected error = %v", err)
	} else if found || !deleted {
		t.Fatalf("Get() found tombstoned record")
	}
}
