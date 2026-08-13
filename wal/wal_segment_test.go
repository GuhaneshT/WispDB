package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recordsEqual(a, b WALRecord) bool {
	return a.SeriesID == b.SeriesID &&
		a.Timestamp == b.Timestamp &&
		a.Deleted == b.Deleted &&
		bytes.Equal(a.Value, b.Value)
}

func assertReplay(t *testing.T, w *WAL, want []WALRecord) {
	t.Helper()
	got, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Replay() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !recordsEqual(got[i], want[i]) {
			t.Fatalf("Replay()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func makeRecords(n int) []WALRecord {
	records := make([]WALRecord, 0, n)
	for i := 0; i < n; i++ {
		records = append(records, WALRecord{
			SeriesID:  uint64(i),
			Timestamp: int64(i * 10),
			Value:     []byte(strings.Repeat("v", 8)),
		})
	}
	return records
}

func TestWALRotatesOnSegmentSizeAndReplaysInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")

	// Each record is 8 bytes of framing + 21 bytes of payload header + 8 bytes
	// of value = 37 bytes, so a 100 byte cap holds two records per segment.
	w, err := CreateWALWithSegmentSize(path, 100)
	if err != nil {
		t.Fatalf("CreateWALWithSegmentSize() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	want := makeRecords(5)
	for _, record := range want {
		if err := w.AppendRecord(record); err != nil {
			t.Fatalf("AppendRecord() error = %v", err)
		}
	}

	segments, err := w.Segments()
	if err != nil {
		t.Fatalf("Segments() error = %v", err)
	}
	if len(segments) < 2 {
		t.Fatalf("Segments() = %v, want the log to have rotated at least once", segments)
	}
	if w.CurrentSegment() != segments[len(segments)-1] {
		t.Fatalf("CurrentSegment() = %d, want the highest segment %d", w.CurrentSegment(), segments[len(segments)-1])
	}

	// Rotation must never split or reorder records.
	assertReplay(t, w, want)
}

func TestWALOversizedRecordGetsItsOwnSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")

	w, err := CreateWALWithSegmentSize(path, 32)
	if err != nil {
		t.Fatalf("CreateWALWithSegmentSize() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	want := []WALRecord{
		{SeriesID: 1, Timestamp: 1, Value: bytes.Repeat([]byte("x"), 512)},
		{SeriesID: 2, Timestamp: 2, Value: []byte("small")},
	}
	for _, record := range want {
		if err := w.AppendRecord(record); err != nil {
			t.Fatalf("AppendRecord() error = %v", err)
		}
	}

	assertReplay(t, w, want)
}

func TestWALRotateSealsCurrentSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	first := WALRecord{SeriesID: 1, Timestamp: 10, Value: []byte("alpha")}
	if err := w.AppendRecord(first); err != nil {
		t.Fatalf("AppendRecord() error = %v", err)
	}

	sealed, err := w.Rotate()
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if sealed != 1 {
		t.Fatalf("Rotate() sealed = %d, want 1", sealed)
	}
	if w.CurrentSegment() != 2 {
		t.Fatalf("CurrentSegment() = %d, want 2", w.CurrentSegment())
	}

	second := WALRecord{SeriesID: 2, Timestamp: 20, Value: []byte("beta")}
	if err := w.AppendRecord(second); err != nil {
		t.Fatalf("AppendRecord() error = %v", err)
	}

	assertReplay(t, w, []WALRecord{first, second})
}

func TestWALRemoveSegmentsUpToKeepsActiveAndLaterSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if err := w.AppendRecord(WALRecord{SeriesID: 1, Timestamp: 1, Value: []byte("one")}); err != nil {
		t.Fatalf("AppendRecord() error = %v", err)
	}
	if _, err := w.Rotate(); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if err := w.AppendRecord(WALRecord{SeriesID: 2, Timestamp: 2, Value: []byte("two")}); err != nil {
		t.Fatalf("AppendRecord() error = %v", err)
	}
	sealed, err := w.Rotate()
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	survivor := WALRecord{SeriesID: 3, Timestamp: 3, Value: []byte("three")}
	if err := w.AppendRecord(survivor); err != nil {
		t.Fatalf("AppendRecord() error = %v", err)
	}

	if err := w.RemoveSegmentsUpTo(sealed); err != nil {
		t.Fatalf("RemoveSegmentsUpTo() error = %v", err)
	}

	segments, err := w.Segments()
	if err != nil {
		t.Fatalf("Segments() error = %v", err)
	}
	if len(segments) != 1 || segments[0] != w.CurrentSegment() {
		t.Fatalf("Segments() = %v, want only the active segment %d", segments, w.CurrentSegment())
	}

	assertReplay(t, w, []WALRecord{survivor})
}

func TestWALRemoveSegmentsUpToNeverDropsActiveSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	unflushed := WALRecord{SeriesID: 9, Timestamp: 90, Value: []byte("still needed")}
	if err := w.AppendRecord(unflushed); err != nil {
		t.Fatalf("AppendRecord() error = %v", err)
	}

	// An over-eager caller must not be able to discard records that have not
	// reached an SSTable yet.
	if err := w.RemoveSegmentsUpTo(w.CurrentSegment() + 100); err != nil {
		t.Fatalf("RemoveSegmentsUpTo() error = %v", err)
	}

	assertReplay(t, w, []WALRecord{unflushed})
}

func TestWALReopenAppendsToLatestSegment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")

	w, err := CreateWALWithSegmentSize(path, 100)
	if err != nil {
		t.Fatalf("CreateWALWithSegmentSize() error = %v", err)
	}
	want := makeRecords(5)
	for _, record := range want {
		if err := w.AppendRecord(record); err != nil {
			t.Fatalf("AppendRecord() error = %v", err)
		}
	}
	segmentsBefore, err := w.Segments()
	if err != nil {
		t.Fatalf("Segments() error = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := CreateWALWithSegmentSize(path, 100)
	if err != nil {
		t.Fatalf("CreateWALWithSegmentSize() reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	if reopened.CurrentSegment() != segmentsBefore[len(segmentsBefore)-1] {
		t.Fatalf("CurrentSegment() after reopen = %d, want %d", reopened.CurrentSegment(), segmentsBefore[len(segmentsBefore)-1])
	}

	extra := WALRecord{SeriesID: 99, Timestamp: 990, Value: []byte("after reopen")}
	if err := reopened.AppendRecord(extra); err != nil {
		t.Fatalf("AppendRecord() after reopen error = %v", err)
	}

	assertReplay(t, reopened, append(append([]WALRecord{}, want...), extra))
}

func TestWALReplayToleratesTornTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() error = %v", err)
	}
	want := makeRecords(3)
	for _, record := range want {
		if err := w.AppendRecord(record); err != nil {
			t.Fatalf("AppendRecord() error = %v", err)
		}
	}
	segmentPath := w.segmentPath(w.CurrentSegment())
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Simulate a crash partway through appending the third record.
	info, err := os.Stat(segmentPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if err := os.Truncate(segmentPath, info.Size()-5); err != nil {
		t.Fatalf("Truncate() error = %v", err)
	}

	reopened, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	assertReplay(t, reopened, want[:2])
}

func TestWALReplayRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() error = %v", err)
	}
	for _, record := range makeRecords(3) {
		if err := w.AppendRecord(record); err != nil {
			t.Fatalf("AppendRecord() error = %v", err)
		}
	}
	segmentPath := w.segmentPath(w.CurrentSegment())
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Flip a byte inside the first record's payload; a torn tail is tolerated,
	// silent corruption must not be.
	data, err := os.ReadFile(segmentPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	data[recordHeaderSize] ^= 0xFF
	if err := os.WriteFile(segmentPath, data, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reopened, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	if _, err := reopened.Replay(); err == nil {
		t.Fatal("Replay() error = nil, want a checksum mismatch error")
	}
}

func TestWALAdoptsLegacySingleFileLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")

	// Write a pre-segmentation log: one flat file at the base path.
	legacy, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	want := makeRecords(2)
	for _, record := range want {
		payload, err := serializePayload(record)
		if err != nil {
			t.Fatalf("serializePayload() error = %v", err)
		}
		frame := make([]byte, recordHeaderSize+len(payload))
		putRecordHeader(frame, payload)
		copy(frame[recordHeaderSize:], payload)
		if _, err := legacy.Write(frame); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy wal still present at base path, err = %v", err)
	}
	assertReplay(t, w, want)
}
