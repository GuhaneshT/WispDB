# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

WispDB is a from-scratch LSM-tree storage engine for time-series data, written in Go with **no external dependencies** (`go.mod` is a single `module wisp` line — no `go` directive, no `require` block). Every key is the pair `(SeriesID uint64, Timestamp int64)`, ordered by SeriesID then Timestamp; values are opaque `[]byte`.

## Commands

Go is not on `PATH` in this environment — `go` must be located or installed before any of these will run.

```powershell
go test ./...                       # all packages
go test ./sstable/ -run TestWriterReaderRoundTrip -v   # single test
go test . -race -v                  # concurrency test needs -race to be meaningful
go vet ./...                        # main build check
go build ./...                      # builds cleanly now — see package layout below
.\scripts\bench-compare.ps1         # run tests + benchmarks, compare to prior baseline
```

**Benchmarking:** `bench-compare.ps1` runs the full test suite, collects benchmarks from all packages, and compares ns/op (CPU), B/op (memory), and allocs/op against the previous run. Baseline stored locally at `.claude/benchmarks/baseline.json` (not committed). First run establishes the baseline; subsequent runs show % deltas.

**Package layout:** the engine itself is `package wisp` at the module root (moved out of `main/`, which was `package main` with no `func main()` — undocumented and, worse, unimportable by design). Importing it elsewhere is now just `import "wisp"`. `examples/basic/main.go` is the one real `package main` in the repo, a runnable demo of `Insert`/`Get`/`Delete`/`Scan`; nothing else should ever declare `func main()` unless a CLI is genuinely being introduced.

## Architecture

Layering, bottom-up: `skiplist` → `memtable` → `wal` + `sstable` → `compactor` → `wisp` (root package, the `Wisp` engine that wires them together).

**Write path** (`Wisp.Insert` / `Wisp.Delete` in `wisp.go`): append to WAL (fsync per record) → write into the mutable memtable. A `Delete` is a tombstone insert, not a removal — it flows through the skiplist, memtable, WAL, and SSTable formats as a `Deleted bool` alongside the value.

**Memtable rotation**: when the mutable memtable's estimated size crosses `MemTableFlushThreshold`, it is `Freeze()`d (rejects further writes) and becomes the single immutable memtable; a fresh mutable one replaces it. `flushImmutable` writes the immutable memtable to a brand-new SSTable generation. Note `prepareMutableMemTableLocked` deliberately unlocks and re-locks `w.mu` around a flush — it is called with the lock held and returns with it held.

**Read path** (`Wisp.Get`): mutable memtable → immutable memtable → SSTables newest generation first. **A tombstone is authoritative and stops the search** — finding a deleted entry must not fall through to older SSTables. `Get` returns `(value []byte, found bool, deleted bool, err error)`; a tombstone yields `found=false, deleted=true`.

**Locking**: `w.mu` (RWMutex) guards engine state; `w.flushMu` serializes flushes. `flushImmutable` re-checks that the immutable memtable it was handed is still the current one before doing work, so redundant concurrent flush calls are no-ops. The skiplist itself has no internal locking and relies entirely on `w.mu`.

### SSTable format (`sstable/`)

Layout: 12-byte header (magic `0x57495350` = "WISP", version, blockSize, 3 reserved bytes) → data blocks → index → bloom filter → 52-byte footer (`FooterSize` in `sstable.go`). The footer's `Checksum` field is a CRC32 (IEEE, `hash/crc32`) over everything before the footer (header + blocks + index + bloom filter, when present), computed incrementally in `Writer` via an `io.MultiWriter` and verified in `Reader.readFooter` after the index and bloom filter load; a mismatch fails `OpenReader` the same way an invalid magic/version does.

- Block entry encoding (`block.go` / `reader.go`): `entryLength(4) | seriesID(8) | timestamp(8) | deleted(1) | valueLength(4) | value`. Encoder and decoder are hand-rolled in separate files — changing one requires changing the other, plus `entryEncodedSize`.
- Index entry: fixed 28 bytes (`seriesID, timestamp, offset, size`), one per block, keyed on the block's first entry. `readIndex` rejects any index whose size isn't a multiple of 28.
- `Reader.findBlock` binary-searches the index (`sort.Search`) for the last entry whose key is `<=` the target, relying on the index being sorted ascending by construction.
- Bloom filter (`bloom.go`): one filter per SSTable, keyed on `SeriesID`, built by `Writer` during flush/compaction and encoded after the index. `Reader.MayContain` is checked first in `Reader.Get` — a negative result skips the block search and all disk I/O for that table entirely; `bloomOffset`/`bloomSize` of `0` (pre-bloom-filter tables) makes `MayContain` always return `true` so `Get` degrades to the old point-lookup behavior.
- `Reader.findEntryInBlock` walks a single block's raw bytes and returns as soon as it matches the target `(seriesID, timestamp)`, allocating exactly one value copy (or zero, on a miss) instead of decoding every entry in the block. `Reader.Get` calls this directly; `Reader.readBlock`, which decodes and copies every entry into a `[]Entry`, is kept only for callers that need the whole block (the compaction iterator in `iterator.go`) — the two decode loops are hand-kept in sync, same as the block encoder/decoder pair above.

### SSTable lifecycle (`sstable/sstable_list.go`)

Files are generation-numbered: `data_000001.sst`, from `NewPath(basePath, gen)`. `SSTableList` keeps them **sorted by generation descending** (newest first) — the read path depends on this ordering.

Each `SSTableFile` is reference-counted with two distinct terminal states, and conflating them has caused bugs before:
- `closed` — the DB shut down; the file stays on disk and is reopened by `Load`.
- `unlinked` — the file was compacted away; `os.Remove` runs once the refcount hits zero.

Any caller of `GetTables()` **must** pair it with `defer sstable.ReleaseTables(tables)`, or files are never closed/deleted.

### Compaction (`compactor/`)

`CompactSSTables` does a k-way merge via a min-heap over per-SSTable iterators. The heap's `Less` tie-breaks equal `(seriesID, timestamp)` by **higher generation first**, so the newest version of a key pops first and later duplicates are skipped. `IsMajor` controls tombstone handling: major compaction drops tombstones entirely, minor compaction preserves them (an older SSTable may still hold the shadowed value). If the merge produces zero output entries the output file is removed and `nil` is returned — never leave a headers-and-footer-only SSTable on disk. `ReplaceTables` swaps the merged file in and marks the inputs `unlinked`.

`Compactor.CompactRange` is currently dead code — defined but never called (`CompactAll` is the only live entry point) and has no test coverage. `heapItem.sstableIdx` is likewise stored but never read.

## Recent optimizations

**Allocation elimination (WAL + SSTable block encoding):** `binary.Write`/`binary.Read` (which box each field into `interface{}`, causing heap allocations) have been replaced with manual `binary.LittleEndian.PutUintXX`/`UintXX` calls. `wal/wal.go`'s `AppendRecord` now encodes into a `sync.Pool`-backed buffer (safe for concurrent callers); `sstable/block.go`'s `Block.Encode` writes into a Writer-held scratch buffer (single-owner path). Wire format unchanged. Benchmarks in `wal/wal_bench_test.go` and `sstable/block_bench_test.go` can be tracked with `scripts/bench-compare.ps1`.

**SSTable checksum validation:** The footer's `Checksum` field now holds a real CRC32 (IEEE) over the data+index, computed incrementally during write and verified on open. Mismatches fail `OpenReader` cleanly (like invalid magic/version), preventing silent corruption from going undetected.

**`Reader.findBlock` binary search:** Replaced the O(n) linear scan over the index with `sort.Search` (O(log n)), relying on the index being sorted ascending by construction. Same "last block whose first key is `<=` target" semantics, just faster as SSTables accumulate more blocks.

**Per-SSTable bloom filters:** `Reader.Get` checks `MayContain` before any disk I/O; a negative result skips the block search, block read, and CRC-adjacent work for that table entirely. Built by `Writer` during flush/compaction, encoded between the index and footer.

**Targeted block decode (`Reader.findEntryInBlock`):** `Get` no longer decodes an entire block into a `[]Entry` (with a value-copy per entry) just to find one match. It scans the raw block bytes and stops at the first matching `(seriesID, timestamp)`, allocating one value copy on a hit and none on a miss. `readBlock` is unchanged and still used by the compaction iterator, which genuinely needs every entry.

## Known in-progress state

- **WAL segmentation is implemented.** The WAL is split into generation-numbered segments (`wal/wal.go`); `freezeMutableLocked` calls `w.wal.Rotate()` to seal the segment carrying the about-to-be-frozen memtable and records the sealed id in `w.immutableWALSegment`. Once `flushImmutable` has durably written that memtable to an SSTable, it calls `walInstance.RemoveSegmentsUpTo(sealedSegment)` to reclaim the now-redundant segments. Ordering matters: truncation only happens *after* the SSTable write succeeds, so a crash between the two steps never loses data. `Recover()` still replays every remaining segment, but that's now bounded by segments not yet flushed, not the whole history.
- `sstable.SSTable` (in `sstable.go`) is a leftover type; the live path uses `SSTableFile` + `Reader`.
- The codebase intentionally carries no explanatory comments in most files; commented-out debug `fmt.Printf` blocks in `reader.go` and `compact.go` are historical debugging aids.

## Testing conventions

Tests use `t.TempDir()` with tiny tuning values (`SSTableBlockSize: 128`, `MemTableFlushThreshold: 1024`) to force multi-block, multi-generation behavior on small datasets — keep that pattern when adding tests, otherwise flush and compaction paths never execute. `concurrency_test.go` runs writers, readers, and a background compactor concurrently and is the primary guard on the refcounting and locking rules above.
