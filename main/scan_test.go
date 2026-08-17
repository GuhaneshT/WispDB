package main

import (
	"path/filepath"
	"testing"
)

func TestScanBasic(t *testing.T) {
	dir := t.TempDir()
	config := DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = filepath.Join(dir, "data.sst")

	w, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWisp: %v", err)
	}
	defer w.Close()

	// Insert test data: series 1 has values at [100, 200, 300, 400]
	if err := w.Insert(1, 100, []byte("v100")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := w.Insert(1, 200, []byte("v200")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := w.Insert(1, 300, []byte("v300")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := w.Insert(1, 400, []byte("v400")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Scan series 1 from 150 to 350 — should return [200, 300]
	it, err := w.Scan(1, 150, 350)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	defer it.Close()

	var results []int64
	for it.Next() {
		key, _, _ := it.Entry()
		results = append(results, key.Timestamp)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0] != 200 || results[1] != 300 {
		t.Fatalf("expected [200, 300], got %v", results)
	}
}

func TestScanWithDelete(t *testing.T) {
	dir := t.TempDir()
	config := DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = filepath.Join(dir, "data.sst")

	w, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWisp: %v", err)
	}
	defer w.Close()

	// Insert and then delete
	w.Insert(1, 100, []byte("v100"))
	w.Insert(1, 200, []byte("v200"))
	w.Delete(1, 200) // Delete 200
	w.Insert(1, 300, []byte("v300"))

	it, err := w.Scan(1, 100, 300)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	defer it.Close()

	var results []int64
	for it.Next() {
		key, _, _ := it.Entry()
		results = append(results, key.Timestamp)
	}

	// Should return [100, 300] — 200 is deleted so doesn't appear
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0] != 100 || results[1] != 300 {
		t.Fatalf("expected [100, 300], got %v", results)
	}
}

func TestScanMultipleSeries(t *testing.T) {
	dir := t.TempDir()
	config := DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = filepath.Join(dir, "data.sst")

	w, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWisp: %v", err)
	}
	defer w.Close()

	// Insert data for series 1 and 2
	w.Insert(1, 100, []byte("s1-100"))
	w.Insert(1, 200, []byte("s1-200"))
	w.Insert(2, 100, []byte("s2-100"))
	w.Insert(2, 200, []byte("s2-200"))

	// Scan only series 1
	it, err := w.Scan(1, 0, 300)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	defer it.Close()

	var results []int64
	for it.Next() {
		key, _, _ := it.Entry()
		results = append(results, key.Timestamp)
	}

	// Should only return series 1 entries
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, ts := range results {
		if ts != 100 && ts != 200 {
			t.Fatalf("unexpected timestamp %d", ts)
		}
	}
}

func TestScanEmpty(t *testing.T) {
	dir := t.TempDir()
	config := DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = filepath.Join(dir, "data.sst")

	w, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWisp: %v", err)
	}
	defer w.Close()

	// Scan empty database
	it, err := w.Scan(1, 0, 1000)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	defer it.Close()

	if it.Next() {
		t.Fatalf("expected no results from empty database")
	}
}

func TestScanTimeRangeFilter(t *testing.T) {
	dir := t.TempDir()
	config := DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = filepath.Join(dir, "data.sst")

	w, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWisp: %v", err)
	}
	defer w.Close()

	// Insert values at [10, 20, 30, 40, 50]
	for i := 1; i <= 5; i++ {
		ts := int64(i * 10)
		w.Insert(1, ts, []byte("value"))
	}

	// Scan with various ranges
	testCases := []struct {
		startTs int64
		endTs   int64
		want    int
	}{
		{0, 100, 5},    // All 5
		{15, 35, 2},    // 20, 30
		{30, 30, 1},    // Just 30
		{100, 200, 0},  // None
		{1, 9, 0},      // Before all
	}

	for _, tc := range testCases {
		it, _ := w.Scan(1, tc.startTs, tc.endTs)
		count := 0
		for it.Next() {
			count++
		}
		it.Close()

		if count != tc.want {
			t.Errorf("Scan(%d, %d): got %d results, want %d", tc.startTs, tc.endTs, count, tc.want)
		}
	}
}

func TestScanAcrossFlush(t *testing.T) {
	dir := t.TempDir()
	config := DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = filepath.Join(dir, "data.sst")
	config.MemTableFlushThreshold = 256 // Small threshold to force flush

	w, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWisp: %v", err)
	}
	defer w.Close()

	// Insert enough to trigger flush
	for i := 0; i < 10; i++ {
		w.Insert(1, int64(i*100), []byte("value_value_value"))
	}

	// Force another flush by inserting more
	for i := 10; i < 20; i++ {
		w.Insert(1, int64(i*100), []byte("value_value_value"))
	}

	// Scan should return results from both memtable (before flush) and SSTable (after flush)
	it, err := w.Scan(1, 0, 10000)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	defer it.Close()

	count := 0
	lastTs := int64(-1)
	for it.Next() {
		key, _, _ := it.Entry()
		// Verify ordering
		if key.Timestamp <= lastTs {
			t.Fatalf("results not in order: got %d after %d", key.Timestamp, lastTs)
		}
		lastTs = key.Timestamp
		count++
	}

	if count != 20 {
		t.Fatalf("expected 20 results, got %d", count)
	}
}

func BenchmarkScan(b *testing.B) {
	dir := b.TempDir()
	config := DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = filepath.Join(dir, "data.sst")

	w, err := CreateWispWithConfig(config)
	if err != nil {
		b.Fatalf("CreateWisp: %v", err)
	}
	defer w.Close()

	// Pre-populate with 1000 entries
	for i := 0; i < 1000; i++ {
		w.Insert(1, int64(i), []byte("value"))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it, err := w.Scan(1, 0, 1000)
		if err != nil {
			b.Fatalf("Scan: %v", err)
		}
		count := 0
		for it.Next() {
			count++
		}
		it.Close()
		if count != 1000 {
			b.Fatalf("expected 1000 results, got %d", count)
		}
	}
}
