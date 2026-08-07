package sstable

import (
	"bytes"
	"path/filepath"
	"testing"
)

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
		got, found, err := reader.Get(want.SeriesID, want.Timestamp)
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

	if _, found, err := reader.Get(999, 999); err != nil {
		t.Fatalf("Get() unexpected error = %v", err)
	} else if found {
		t.Fatalf("Get() found non-existent record")
	}
}
