package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentReadsWritesAndCompaction(t *testing.T) {
	dir := t.TempDir()
	config := WispConfig{
		WALPath:                filepath.Join(dir, "wal.log"),
		SSTablePath:            filepath.Join(dir, "data.sst"),
		SSTableBlockSize:       128,
		MemTableFlushThreshold: 1024,
	}

	db, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig error: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	var wg sync.WaitGroup
	numWriters := 5
	numReaders := 10
	recordsPerWriter := 100

	stopCompactor := make(chan struct{})

	// Background Compactor Goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCompactor:
				return
			case <-ticker.C:
				_ = db.Compact()
			}
		}
	}()

	// Concurrent Writer Goroutines
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < recordsPerWriter; i++ {
				seriesID := uint64(writerID*1000 + i)
				val := []byte(fmt.Sprintf("val-%d-%d", writerID, i))
				if err := db.Insert(seriesID, int64(i), val); err != nil {
					t.Errorf("Insert error writer=%d i=%d: %v", writerID, i, err)
					return
				}
			}
		}(w)
	}

	// Concurrent Reader Goroutines
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for i := 0; i < recordsPerWriter*2; i++ {
				seriesID := uint64((i % numWriters) * 1000 + (i / numWriters))
				_, _, _ = db.Get(seriesID, int64(i/numWriters))
				time.Sleep(100 * time.Microsecond)
			}
		}(r)
	}

	time.Sleep(300 * time.Millisecond)
	close(stopCompactor)
	wg.Wait()
	_ = db.Flush()

	// Final verification of written keys
	for w := 0; w < numWriters; w++ {
		for i := 0; i < recordsPerWriter; i++ {
			seriesID := uint64(w*1000 + i)
			expected := fmt.Sprintf("val-%d-%d", w, i)
			val, found, err := db.Get(seriesID, int64(i))
			if err != nil {
				t.Fatalf("Get error after test seriesID=%d: %v", seriesID, err)
			}
			if !found || string(val) != expected {
				t.Fatalf("Get seriesID=%d got (%q, %v), want (%q, true)", seriesID, val, found, expected)
			}
		}
	}
}
