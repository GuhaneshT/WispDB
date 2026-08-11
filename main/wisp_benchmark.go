package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"wisp/sstable"
)

// newBenchmarkWisp creates an isolated Wisp instance for each benchmark.
func newBenchmarkWisp(b *testing.B) *Wisp {
	b.Helper()

	dir := b.TempDir()

	config := DefaultWispConfig()
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

// ------------------------------------------------------------
// INSERT
// ------------------------------------------------------------

func BenchmarkInsert(b *testing.B) {
	w := newBenchmarkWisp(b)

	value := []byte("benchmark-value")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := w.Insert(
			uint64(i),
			int64(i),
			value,
		)
		if err != nil {
			b.Fatalf("insert failed: %v", err)
		}
	}

	b.ReportMetric(float64(b.N), "records")
}

// ------------------------------------------------------------
// INSERT - SAME SERIES
// ------------------------------------------------------------

func BenchmarkInsertSameSeries(b *testing.B) {
	w := newBenchmarkWisp(b)

	value := []byte("benchmark-value")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := w.Insert(
			1,
			int64(i),
			value,
		)
		if err != nil {
			b.Fatalf("insert failed: %v", err)
		}
	}
}

// ------------------------------------------------------------
// GET - HIT
// ------------------------------------------------------------

func BenchmarkGetHit(b *testing.B) {
	w := newBenchmarkWisp(b)

	const recordCount = 10000

	value := []byte("benchmark-value")

	// Setup is not included in the benchmark.
	for i := 0; i < recordCount; i++ {
		err := w.Insert(
			uint64(i),
			int64(i),
			value,
		)
		if err != nil {
			b.Fatalf("setup insert failed: %v", err)
		}
	}

	if err := w.Flush(); err != nil {
		b.Fatalf("flush failed: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		id := uint64(i % recordCount)

		_, found,_, err := w.Get(
			id,
			int64(id),
		)

		if err != nil {
			b.Fatalf("get failed: %v", err)
		}

		if !found {
			b.Fatalf("expected record %d to exist", id)
		}
	}
}

// ------------------------------------------------------------
// GET - MISS
// ------------------------------------------------------------

func BenchmarkGetMiss(b *testing.B) {
	w := newBenchmarkWisp(b)

	const recordCount = 10000

	value := []byte("benchmark-value")

	for i := 0; i < recordCount; i++ {
		err := w.Insert(
			uint64(i),
			int64(i),
			value,
		)
		if err != nil {
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

		if !deleted || found{
			b.Fatalf("record %d should not exist", id)

		}
	}
}

// ------------------------------------------------------------
// INSERT + AUTOMATIC FLUSH
// ------------------------------------------------------------

func BenchmarkInsertWithFlush(b *testing.B) {
	w := newBenchmarkWisp(b)

	// Use a relatively small memtable so the benchmark
	// exercises memtable rotation and SSTable creation.
	w.config.MemTableFlushThreshold = 64 * 1024

	value := []byte("benchmark-value")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := w.Insert(
			uint64(i),
			int64(i),
			value,
		)
		if err != nil {
			b.Fatalf("insert failed: %v", err)
		}
	}
}

// ------------------------------------------------------------
// FLUSH
// ------------------------------------------------------------

func BenchmarkFlush(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		w := newBenchmarkWisp(b)

		const recordCount = 10000

		value := []byte("benchmark-value")

		for j := 0; j < recordCount; j++ {
			err := w.Insert(
				uint64(j),
				int64(j),
				value,
			)
			if err != nil {
				b.Fatalf("insert failed: %v", err)
			}
		}

		b.StartTimer()

		err := w.Flush()
		if err != nil {
			b.Fatalf("flush failed: %v", err)
		}

		b.StopTimer()

		if err := w.Close(); err != nil {
			b.Fatalf("close failed: %v", err)
		}
	}
}

// ------------------------------------------------------------
// COMPACTION
// ------------------------------------------------------------

func BenchmarkCompaction(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		w := newBenchmarkWisp(b)

		// Create several SSTables.
		w.config.MemTableFlushThreshold = 32 * 1024

		const recordCount = 10000

		value := []byte("benchmark-value")

		for j := 0; j < recordCount; j++ {
			err := w.Insert(
				uint64(j),
				int64(j),
				value,
			)
			if err != nil {
				b.Fatalf("insert failed: %v", err)
			}
		}

		if err := w.Flush(); err != nil {
			b.Fatalf("flush failed: %v", err)
		}

		// Make sure there are multiple SSTables before
		// measuring compaction.
		tables := w.config.SSTableList.GetTables()

		if len(tables) < 2 {
			sstable.ReleaseTables(tables)
			w.Close()
			b.Skip("not enough SSTables for compaction benchmark")
		}

		sstable.ReleaseTables(tables)

		b.StartTimer()

		err := w.Compact()
		if err != nil {
			b.Fatalf("compaction failed: %v", err)
		}

		b.StopTimer()

		if err := w.Close(); err != nil {
			b.Fatalf("close failed: %v", err)
		}
	}
}

// ------------------------------------------------------------
// PARALLEL GET
// ------------------------------------------------------------

func BenchmarkGetParallel(b *testing.B) {
	w := newBenchmarkWisp(b)

	const recordCount = 10000

	value := []byte("benchmark-value")

	for i := 0; i < recordCount; i++ {
		err := w.Insert(
			uint64(i),
			int64(i),
			value,
		)
		if err != nil {
			b.Fatalf("setup insert failed: %v", err)
		}
	}

	if err := w.Flush(); err != nil {
		b.Fatalf("flush failed: %v", err)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		var i uint64

		for pb.Next() {
			id := i % recordCount
			i++

			_, found, _, err := w.Get(
				id,
				int64(id),
			)

			if err != nil {
				b.Errorf("get failed: %v", err)
				return
			}

			if !found {
				b.Errorf("expected record %d", id)
				return
			}
		}
	})
}

// ------------------------------------------------------------
// PARALLEL INSERT
// ------------------------------------------------------------

func BenchmarkInsertParallel(b *testing.B) {
	w := newBenchmarkWisp(b)

	value := []byte("benchmark-value")

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := uint64(b.N)

			err := w.Insert(
				id,
				int64(id),
				value,
			)

			if err != nil {
				b.Errorf("insert failed: %v", err)
				return
			}
		}
	})
}

// ------------------------------------------------------------
// BENCHMARK INFO
// ------------------------------------------------------------

func ExampleBenchmarkCommands() {
	fmt.Println("Run all benchmarks:")
	fmt.Println("go test -bench=. -benchmem")

	fmt.Println("Run only inserts:")
	fmt.Println("go test -bench=BenchmarkInsert -benchmem")

	fmt.Println("Run only reads:")
	fmt.Println("go test -bench=BenchmarkGet -benchmem")

	fmt.Println("Run parallel reads:")
	fmt.Println("go test -bench=BenchmarkGetParallel -benchmem")

	fmt.Println("Run with a fixed duration:")
	fmt.Println("go test -bench=. -benchmem -benchtime=5s")

	fmt.Println("Run multiple times:")
	fmt.Println("go test -bench=. -benchmem -count=5")
}
