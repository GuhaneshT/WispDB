package wal

import (
	"path/filepath"
	"testing"
)

func benchRecord() WALRecord {
	return WALRecord{SeriesID: 42, Timestamp: 1234567890, Value: []byte("the quick brown fox jumps over the lazy dog")}
}

func BenchmarkSerializePayload(b *testing.B) {
	record := benchRecord()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := serializePayload(record); err != nil {
			b.Fatalf("serializePayload() error = %v", err)
		}
	}
}

func BenchmarkDeserializePayload(b *testing.B) {
	record := benchRecord()
	payload, err := serializePayload(record)
	if err != nil {
		b.Fatalf("serializePayload() error = %v", err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := deserializePayload(payload); err != nil {
			b.Fatalf("deserializePayload() error = %v", err)
		}
	}
}

func BenchmarkAppendRecord(b *testing.B) {
	path := filepath.Join(b.TempDir(), "wal.log")
	w, err := CreateWAL(path)
	if err != nil {
		b.Fatalf("CreateWAL() error = %v", err)
	}
	defer w.Close()

	record := benchRecord()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := w.AppendRecord(record); err != nil {
			b.Fatalf("AppendRecord() error = %v", err)
		}
	}
}
