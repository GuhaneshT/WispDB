package main

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"wisp/sstable"
	"wisp/wal"
)

// ============================================================
// BENCHMARK HELPERS
// ============================================================

func newBenchmarkWisp(
	b *testing.B,
	config WispConfig,
) *Wisp {
	b.Helper()

	dir := b.TempDir()

	config.WALPath = filepath.Join(dir, "wal.log")
	config.SSTablePath = filepath.Join(dir, "data.sst")

	w, err := CreateWispWithConfig(config)
	if err != nil {
		b.Fatalf("failed to create Wisp: %v", err)
	}

	b.Cleanup(func() {
		if err := w.Close(); err != nil {
			b.Errorf("failed to close Wisp: %v", err)
		}
	})

	return w
}

func defaultBenchmarkConfig() WispConfig {
	config := DefaultWispConfig()

	// Large enough that normal benchmarks don't constantly flush.
	config.MemTableFlushThreshold = 64 * 1024 * 1024

	return config
}

// ============================================================
// INSERT
// ============================================================

// Measures:
// WAL append
// + MemTable insertion
// + Skiplist insertion
// + Wisp write locking
func BenchmarkInsert(b *testing.B) {
	config := defaultBenchmarkConfig()
	w := newBenchmarkWisp(b, config)

	value := []byte("benchmark-value")

	b.SetBytes(int64(len(value)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := w.Insert(
			uint64(i),
			int64(i),
			value,
		); err != nil {
			b.Fatalf("insert failed: %v", err)
		}
	}

	b.ReportMetric(float64(b.N), "records")
}

// ============================================================
// INSERT - SAME SERIES
// ============================================================

// Measures insertion when all points belong to the same series.
//
// Workload:
//
//	Series 1 -> timestamp 0
//	Series 1 -> timestamp 1
//	Series 1 -> timestamp 2
//	...
func BenchmarkInsertSameSeries(b *testing.B) {
	config := defaultBenchmarkConfig()
	w := newBenchmarkWisp(b, config)

	value := []byte("benchmark-value")

	b.SetBytes(int64(len(value)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := w.Insert(
			1,
			int64(i),
			value,
		); err != nil {
			b.Fatalf("insert failed: %v", err)
		}
	}

	b.ReportMetric(float64(b.N), "records")
}

// ============================================================
// GET - MEMTABLE HIT
// ============================================================

// Measures reads directly from the MemTable.
//
// No SSTable lookup is involved.
func BenchmarkGetMemtable(b *testing.B) {
	config := defaultBenchmarkConfig()
	w := newBenchmarkWisp(b, config)

	const recordCount = 10000

	value := []byte("benchmark-value")

	// Setup.
	for i := 0; i < recordCount; i++ {
		if err := w.Insert(
			uint64(i),
			int64(i),
			value,
		); err != nil {
			b.Fatalf("setup insert failed: %v", err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		id := uint64(i % recordCount)

		_, found, deleted, err := w.Get(
			id,
			int64(id),
		)

		if err != nil {
			b.Fatalf("get failed: %v", err)
		}

		if !found || deleted {
			b.Fatalf(
				"expected record %d, found=%v deleted=%v",
				id,
				found,
				deleted,
			)
		}
	}
}

// ============================================================
// GET - SSTABLE HIT
// ============================================================

// Measures:
//
//	Get
//	  -> SSTableList
//	  -> index lookup
//	  -> ReadAt
//	  -> block decoding
//	  -> entry lookup
func BenchmarkGetSSTable(b *testing.B) {
	config := defaultBenchmarkConfig()
	w := newBenchmarkWisp(b, config)

	const recordCount = 10000

	value := []byte("benchmark-value")

	// Setup.
	for i := 0; i < recordCount; i++ {
		if err := w.Insert(
			uint64(i),
			int64(i),
			value,
		); err != nil {
			b.Fatalf("setup insert failed: %v", err)
		}
	}

	if err := w.Flush(); err != nil {
		b.Fatalf("flush failed: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		id := uint64(i % recordCount)

		_, found, deleted, err := w.Get(
			id,
			int64(id),
		)

		if err != nil {
			b.Fatalf("get failed: %v", err)
		}

		if !found || deleted {
			b.Fatalf(
				"expected record %d, found=%v deleted=%v",
				id,
				found,
				deleted,
			)
		}
	}
}

// ============================================================
// GET - MISS
// ============================================================

// Measures lookup of a key which does not exist.
func BenchmarkGetMiss(b *testing.B) {
	config := defaultBenchmarkConfig()
	w := newBenchmarkWisp(b, config)

	const recordCount = 10000

	value := []byte("benchmark-value")

	// Setup.
	for i := 0; i < recordCount; i++ {
		if err := w.Insert(
			uint64(i),
			int64(i),
			value,
		); err != nil {
			b.Fatalf("setup insert failed: %v", err)
		}
	}

	if err := w.Flush(); err != nil {
		b.Fatalf("flush failed: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		id := uint64(recordCount + i)

		_, found, deleted, err := w.Get(
			id,
			int64(id),
		)

		if err != nil {
			b.Fatalf("get failed: %v", err)
		}

		// A normal miss is:
		//
		// found   = false
		// deleted = false
		if found || deleted {
			b.Fatalf(
				"expected miss for %d, found=%v deleted=%v",
				id,
				found,
				deleted,
			)
		}
	}
}

// ============================================================
// INSERT + AUTOMATIC FLUSH
// ============================================================

// Measures the complete write path including:
//
//	Insert
//	  -> WAL
//	  -> MemTable
//	  -> MemTable rotation
//	  -> SSTable creation
func BenchmarkInsertWithFlush(b *testing.B) {
	config := defaultBenchmarkConfig()

	// IMPORTANT:
	// Set the threshold BEFORE CreateWispWithConfig().
	config.MemTableFlushThreshold = 64 * 1024

	w := newBenchmarkWisp(b, config)

	value := []byte("benchmark-value")

	b.SetBytes(int64(len(value)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := w.Insert(
			uint64(i),
			int64(i),
			value,
		); err != nil {
			b.Fatalf("insert failed: %v", err)
		}
	}

	b.ReportMetric(float64(b.N), "records")
}

// ============================================================
// FLUSH
// ============================================================

// Measures:
//
//	MemTable
//	  -> SSTable Writer
//	  -> block encoding
//	  -> file writes
//	  -> footer
//	  -> OpenReader
// func BenchmarkFlush(b *testing.B) {
// 	const recordCount = 10000

// 	for i := 0; i < b.N; i++ {
// 		b.StopTimer()

// 		config := defaultBenchmarkConfig()

// 		w := newBenchmarkWisp(b, config)

// 		value := []byte("benchmark-value")

// 		for j := 0; j < recordCount; j++ {
// 			if err := w.Insert(
// 				uint64(j),
// 				int64(j),
// 				value,
// 			); err != nil {
// 				b.Fatalf("insert failed: %v", err)
// 			}
// 		}

// 		b.StartTimer()

// 		if err := w.Flush(); err != nil {
// 			b.Fatalf("flush failed: %v", err)
// 		}

// 		b.StopTimer()

// 		if err := w.Close(); err != nil {
// 			b.Fatalf("close failed: %v", err)
// 		}
// 	}

// 	b.ReportMetric(float64(recordCount), "records/flush")
// }

func BenchmarkFlush(b *testing.B) {
	const recordCount = 10000

	value := []byte("benchmark-value")

	for i := 0; i < b.N; i++ {
		b.StopTimer()

		config := DefaultWispConfig()
		config.WALPath = filepath.Join(b.TempDir(), fmt.Sprintf("wal-%d.log", i))
		config.SSTablePath = filepath.Join(b.TempDir(), fmt.Sprintf("data-%d.sst", i))

		w := newBenchmarkWisp(b, config)

		for j := 0; j < recordCount; j++ {
			if err := w.Insert(
				uint64(j),
				int64(j),
				value,
			); err != nil {
				b.Fatalf("insert failed: %v", err)
			}
		}

		b.StartTimer()

		if err := w.Flush(); err != nil {
			b.Fatalf("flush failed: %v", err)
		}

		b.StopTimer()

		if err := w.Close(); err != nil {
			b.Fatalf("close failed: %v", err)
		}
	}

	b.ReportMetric(float64(recordCount), "records/flush")
}

// ============================================================
// COMPACTION
// ============================================================

// Measures SSTable merge performance.
func BenchmarkCompaction(b *testing.B) {
	const recordCount = 10000

	for i := 0; i < b.N; i++ {
		b.StopTimer()

		config := defaultBenchmarkConfig()

		// Small MemTable so we create multiple SSTables.
		config.MemTableFlushThreshold = 32 * 1024

		w := newBenchmarkWisp(b, config)

		value := []byte("benchmark-value")

		for j := 0; j < recordCount; j++ {
			if err := w.Insert(
				uint64(j),
				int64(j),
				value,
			); err != nil {
				b.Fatalf("insert failed: %v", err)
			}
		}

		if err := w.Flush(); err != nil {
			b.Fatalf("flush failed: %v", err)
		}

		tables := w.config.SSTableList.GetTables()

		if len(tables) < 2 {
			sstable.ReleaseTables(tables)
			_ = w.Close()
			b.Skip("not enough SSTables for compaction benchmark")
		}

		sstable.ReleaseTables(tables)

		b.StartTimer()

		if err := w.Compact(); err != nil {
			b.Fatalf("compaction failed: %v", err)
		}

		b.StopTimer()

		if err := w.Close(); err != nil {
			b.Fatalf("close failed: %v", err)
		}
	}

	b.ReportMetric(float64(recordCount), "records/compaction")
}

// ============================================================
// PARALLEL GET
// ============================================================

// Measures concurrent readers.
//
// This is particularly important for your:
//
//	sync.RWMutex
//	ReadAt()
//	SSTable reference counting
func BenchmarkGetParallel(b *testing.B) {
	config := defaultBenchmarkConfig()
	w := newBenchmarkWisp(b, config)

	const recordCount = 10000

	value := []byte("benchmark-value")

	// Setup.
	for i := 0; i < recordCount; i++ {
		if err := w.Insert(
			uint64(i),
			int64(i),
			value,
		); err != nil {
			b.Fatalf("setup insert failed: %v", err)
		}
	}

	if err := w.Flush(); err != nil {
		b.Fatalf("flush failed: %v", err)
	}

	var counter atomic.Uint64

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := counter.Add(1) % recordCount

			_, found, deleted, err := w.Get(
				id,
				int64(id),
			)

			if err != nil {
				b.Errorf("get failed: %v", err)
				return
			}

			if !found || deleted {
				b.Errorf(
					"expected record %d, found=%v deleted=%v",
					id,
					found,
					deleted,
				)
				return
			}
		}
	})
}

// ============================================================
// PARALLEL INSERT
// ============================================================

// Measures concurrent writers.
//
// Uses an atomic counter so different goroutines don't repeatedly
// insert the exact same key.
func BenchmarkInsertParallel(b *testing.B) {
	config := defaultBenchmarkConfig()
	w := newBenchmarkWisp(b, config)

	value := []byte("benchmark-value")

	var counter atomic.Uint64

	b.SetBytes(int64(len(value)))
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := counter.Add(1)

			if err := w.Insert(
				id,
				int64(id),
				value,
			); err != nil {
				b.Errorf("insert failed: %v", err)
				return
			}
		}
	})

	b.ReportMetric(float64(counter.Load()), "records")
}

// ============================================================
// WAL APPEND
// ============================================================

// Isolates WAL performance from MemTable/Skiplist performance.
func BenchmarkWALAppend(b *testing.B) {
	dir := b.TempDir()

	w, err := wal.CreateWAL(
		filepath.Join(dir, "wal.log"),
	)
	if err != nil {
		b.Fatalf("CreateWAL failed: %v", err)
	}

	defer func() {
		if err := w.Close(); err != nil {
			b.Errorf("WAL close failed: %v", err)
		}
	}()

	value := []byte("benchmark-value")

	b.SetBytes(int64(len(value)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := w.AppendRecord(wal.WALRecord{
			SeriesID:  uint64(i),
			Timestamp: int64(i),
			Value:     value,
		}); err != nil {
			b.Fatalf("WAL append failed: %v", err)
		}
	}
}

// ============================================================
// WAL REPLAY
// ============================================================

// Measures recovery/replay performance.
//
// The WAL is created during setup and Replay() is measured separately.
func BenchmarkWALReplay(b *testing.B) {
	const recordCount = 10000

	for i := 0; i < b.N; i++ {
		b.StopTimer()

		dir := b.TempDir()

		path := filepath.Join(dir, "wal.log")

		w, err := wal.CreateWAL(path)
		if err != nil {
			b.Fatalf("CreateWAL failed: %v", err)
		}

		value := []byte("benchmark-value")

		for j := 0; j < recordCount; j++ {
			if err := w.AppendRecord(wal.WALRecord{
				SeriesID:  uint64(j),
				Timestamp: int64(j),
				Value:     value,
			}); err != nil {
				_ = w.Close()
				b.Fatalf("append failed: %v", err)
			}
		}

		if err := w.Close(); err != nil {
			b.Fatalf("WAL close failed: %v", err)
		}

		// Open again so Replay() behaves like recovery.
		reopened, err := wal.CreateWAL(path)
		if err != nil {
			b.Fatalf("reopen WAL failed: %v", err)
		}

		b.StartTimer()

		records, err := reopened.Replay()
		if err != nil {
			b.Fatalf("Replay failed: %v", err)
		}

		b.StopTimer()

		if len(records) != recordCount {
			b.Fatalf(
				"Replay returned %d records, expected %d",
				len(records),
				recordCount,
			)
		}

		if err := reopened.Close(); err != nil {
			b.Fatalf("reopened WAL close failed: %v", err)
		}
	}

	b.ReportMetric(float64(recordCount), "records/replay")
}

// ============================================================
// BENCHMARK INFO
// ============================================================

// func ExampleBenchmarkCommands() {
// 	fmt.Println("Run all benchmarks:")
// 	fmt.Println("go test ./main -bench=. -benchmem")

// 	fmt.Println("Run benchmarks for 3 seconds:")
// 	fmt.Println("go test ./main -bench=. -benchmem -benchtime=3s")

// 	fmt.Println("Run benchmarks 5 times:")
// 	fmt.Println("go test ./main -bench=. -benchmem -count=5")

// 	fmt.Println("Run only insert benchmarks:")
// 	fmt.Println("go test ./main -bench=BenchmarkInsert -benchmem")

// 	fmt.Println("Run only Get benchmarks:")
// 	fmt.Println("go test ./main -bench=BenchmarkGet -benchmem")

// 	fmt.Println("Run only parallel benchmarks:")
// 	fmt.Println("go test ./main -bench=Parallel -benchmem")

// 	fmt.Println("Run with race detector:")
// 	fmt.Println("go test ./main -race -bench=BenchmarkGetParallel -benchtime=3s")

	// Output:
	// Run all benchmarks:
	// go test ./main -bench=. -benchmem
	// Run benchmarks for 3 seconds:
	// go test ./main -bench=. -benchmem -benchtime=3s
	// Run benchmarks 5 times:
	// go test ./main -bench=. -benchmem -count=5
	// Run only insert benchmarks:
	// go test ./main -bench=BenchmarkInsert -benchmem
	// Run only Get benchmarks:
	// go test ./main -bench=BenchmarkGet -benchmem
	// Run only parallel benchmarks:
	// go test ./main -bench=Parallel -benchmem
	// Run with race detector:
	// go test ./main -race -bench=BenchmarkGetParallel -benchtime=3s
// }