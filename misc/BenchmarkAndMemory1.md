# WispDB Benchmark & Memory Profile Report

**Environment:** Windows, `12th Gen Intel(R) Core(TM) i7-1250U`, Go 1.23.0
**Date:** August 13, 2026

---

## Benchmark Results

| Benchmark | Iterations | Time/op | Throughput | B/op | allocs/op |
|---|---|---|---|---|---|
| `BenchmarkInsert` | 16,681 | 323,369 ns | 0.05 MB/s | 408 B | 10 |
| `BenchmarkInsertSameSeries` | 17,811 | 326,279 ns | 0.05 MB/s | 408 B | 10 |
| `BenchmarkGetMemtable` | 41,389,214 | 150.7 ns | — | 0 B | 0 |
| `BenchmarkGetSSTable` | 518,091 | 12,515 ns | — | 21,423 B | 111 |
| `BenchmarkGetMiss` | 1,620,828 | 3,817 ns | — | 568 B | 9 |
| `BenchmarkInsertWithFlush` | 16,114 | 369,964 ns | 0.04 MB/s | 670 B | 14 |
| `BenchmarkFlush` (run 1) | 1 | 21,759,800 ns | — | 2,763,080 B | 51,766 |
| `BenchmarkFlush` (run 2) | 1 | 18,315,100 ns | — | 2,767,944 B | 51,769 |
| `BenchmarkCompaction` | 1 | 37,922,200 ns | — | 6,565,992 B | 92,665 |

**Notes:**
- `BenchmarkFlush` and `BenchmarkCompaction` process 10,000 records per operation (`-benchtime=1x`).
- `BenchmarkGetMemtable` is effectively zero-allocation — the fastest and cheapest path, as expected for an in-memory lookup.
- `BenchmarkGetSSTable` is ~80x slower than the memtable lookup and carries 111 allocs/op, suggesting disk-backed reads or block decoding are allocation-heavy.

---

## Memory Profile — `BenchmarkInsert`

Captured via:

```powershell
go test ./main -run "^$" -bench "^BenchmarkInsert$" -benchmem -benchtime=5s -memprofile=insert-mem.prof -o main.test
go tool pprof -alloc_objects .\main.test .\insert-mem.prof
go tool pprof -alloc_space .\main.test .\insert-mem.prof
```

### By Allocation Count (`-alloc_objects`)

Total: 361,406 objects

| Function | Flat | Flat % | Cum | Cum % |
|---|---|---|---|---|
| `encoding/binary.Write` | 163,841 | 45.33% | 197,638 | 54.69% |
| `wisp/skiplist.(*Skiplist).insert` | 76,192 | 21.08% | 76,192 | 21.08% |
| `wisp/wal.(*WAL).AppendRecord` | 54,615 | 15.11% | 185,690 | 51.38% |
| `bytes.(*Buffer).grow` | 32,770 | 9.07% | 33,797 | 9.35% |
| `wisp/wal.serializePayload` | 32,769 | 9.07% | 131,075 | 36.27% |

### By Allocation Size (`-alloc_space`)

Total: 20.02 MB

| Function | Flat | Flat % | Cum | Cum % |
|---|---|---|---|---|
| `wisp/skiplist.(*Skiplist).insert` | 7.50 MB | 37.47% | 7.50 MB | 37.47% |
| `bytes.growSlice` | 3.01 MB | 15.04% | 3.01 MB | 15.04% |
| `wisp/wal.(*WAL).AppendRecord` | 2.50 MB | 12.49% | 7.00 MB | 34.97% |
| `encoding/binary.Write` | 2.50 MB | 12.49% | 7.51 MB | 37.52% |
| `bytes.(*Buffer).grow` | 2.00 MB | 9.99% | 5.01 MB | 25.03% |
| `wisp/wal.serializePayload` | 1.50 MB | 7.49% | 4.50 MB | 22.48% |
| `wisp/sstable.(*Block).Add` | 1.01 MB | 5.03% | 1.01 MB | 5.03% |

### Key Observations

1. **`encoding/binary.Write` is the top allocation-count hotspot** (45.33% of all allocations, 54.69% cumulative). It's called within the `serializePayload` → `AppendRecord` WAL path, and is a well-known allocation source in Go because it falls back to reflection unless given a fixed-size value directly.
2. **The WAL write path** (`AppendRecord` → `serializePayload` → `binary.Write` → `bytes.Buffer` growth) accounts for the majority of both allocation count (~51–55%) and bytes allocated (~35–38%).
3. **`Skiplist.insert` is the single largest byte consumer** (37.47% of allocated space) despite a smaller share of allocation count (21.08%) — implying each skiplist insert allocates larger objects (e.g., node structs with multiple forward pointers) rather than many small ones.
4. **`bytes.Buffer` growth** (`grow` / `growSlice`) appears twice in the profile and together accounts for a meaningful share of bytes — suggesting the buffer is being resized repeatedly rather than pre-sized. This is a common, low-effort optimization target.

### Suggested Next Steps

- Replace `encoding/binary.Write` with direct `binary.LittleEndian.PutUint*` calls on a pre-sized byte slice to avoid reflection-based allocations.
- Pre-size or pool the `bytes.Buffer` used in `serializePayload` to avoid repeated `grow` calls.
- Investigate whether skiplist node allocation can be reduced (e.g., pooling nodes or reducing per-node level array size).