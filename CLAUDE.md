# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

WispDB is a from-scratch LSM-tree storage engine for time-series data, written in Go with **no external dependencies** (`go.mod` is a single `module wisp` line — no `go` directive, no `require` block). Every key is the pair `(SeriesID uint64, Timestamp int64)`, ordered by SeriesID then Timestamp; values are opaque `[]byte`.

## Commands

Go is not on `PATH` in this environment — `go` must be located or installed before any of these will run.

```powershell
go test ./...                       # all packages
go test ./sstable/ -run TestWriterReaderRoundTrip -v   # single test
go test ./main/ -race -v            # concurrency test needs -race to be meaningful
go vet ./...                        # main build check (see below)
```

`go build ./...` **fails by design**: `main/` is `package main` but declares no `func main()`. It is a library package that only compiles under `go test` / `go vet`. Don't "fix" this by adding a `main()` unless a CLI is actually being introduced.

## Architecture

Layering, bottom-up: `skiplist` → `memtable` → `wal` + `sstable` → `compactor` → `main` (the `Wisp` engine that wires them together).

**Write path** (`Wisp.Insert` / `Wisp.Delete` in `main/wisp.go`): append to WAL (fsync per record) → write into the mutable memtable. A `Delete` is a tombstone insert, not a removal — it flows through the skiplist, memtable, WAL, and SSTable formats as a `Deleted bool` alongside the value.

**Memtable rotation**: when the mutable memtable's estimated size crosses `MemTableFlushThreshold`, it is `Freeze()`d (rejects further writes) and becomes the single immutable memtable; a fresh mutable one replaces it. `flushImmutable` writes the immutable memtable to a brand-new SSTable generation. Note `prepareMutableMemTableLocked` deliberately unlocks and re-locks `w.mu` around a flush — it is called with the lock held and returns with it held.

**Read path** (`Wisp.Get`): mutable memtable → immutable memtable → SSTables newest generation first. **A tombstone is authoritative and stops the search** — finding a deleted entry must not fall through to older SSTables. `Get` returns `(value []byte, found bool, deleted bool, err error)`; a tombstone yields `found=false, deleted=true`.

**Locking**: `w.mu` (RWMutex) guards engine state; `w.flushMu` serializes flushes. `flushImmutable` re-checks that the immutable memtable it was handed is still the current one before doing work, so redundant concurrent flush calls are no-ops. The skiplist itself has no internal locking and relies entirely on `w.mu`.

### SSTable format (`sstable/`)

Layout: 12-byte header (magic `0x57495350` = "WISP", version, blockSize, 3 reserved bytes) → data blocks → index → 36-byte footer. The footer's `Checksum` field is written as zero — CRC validation is not implemented. Only the WAL currently checksums (crc32 per record, verified on replay).

- Block entry encoding (`block.go` / `reader.readBlock`): `entryLength(4) | seriesID(8) | timestamp(8) | deleted(1) | valueLength(4) | value`. Encoder and decoder are hand-rolled in separate files — changing one requires changing the other, plus `entryEncodedSize`.
- Index entry: fixed 28 bytes (`seriesID, timestamp, offset, size`), one per block, keyed on the block's first entry. `readIndex` rejects any index whose size isn't a multiple of 28.
- `Reader.findBlock` is a linear scan over the index, not a binary search.

### SSTable lifecycle (`sstable/sstable_list.go`)

Files are generation-numbered: `data_000001.sst`, from `NewPath(basePath, gen)`. `SSTableList` keeps them **sorted by generation descending** (newest first) — the read path depends on this ordering.

Each `SSTableFile` is reference-counted with two distinct terminal states, and conflating them has caused bugs before:
- `closed` — the DB shut down; the file stays on disk and is reopened by `Load`.
- `unlinked` — the file was compacted away; `os.Remove` runs once the refcount hits zero.

Any caller of `GetTables()` **must** pair it with `defer sstable.ReleaseTables(tables)`, or files are never closed/deleted.

### Compaction (`compactor/`)

`CompactSSTables` does a k-way merge via a min-heap over per-SSTable iterators. The heap's `Less` tie-breaks equal `(seriesID, timestamp)` by **higher generation first**, so the newest version of a key pops first and later duplicates are skipped. `IsMajor` controls tombstone handling: major compaction drops tombstones entirely, minor compaction preserves them (an older SSTable may still hold the shadowed value). If the merge produces zero output entries the output file is removed and `nil` is returned — never leave a headers-and-footer-only SSTable on disk. `ReplaceTables` swaps the merged file in and marks the inputs `unlinked`.

## Known in-progress state

- **WAL is never truncated.** The `w.wal.Reset()` call in `flushImmutable` is commented out pending WAL segmentation. Consequence: the log grows without bound and `Recover()` replays every record ever written, re-populating the memtable with data already in SSTables. Don't re-enable it without segmentation — the commit history records this as a deliberate revert.
- `sstable.SSTable` (in `sstable.go`) is a leftover type; the live path uses `SSTableFile` + `Reader`.
- The codebase intentionally carries no explanatory comments in most files; commented-out debug `fmt.Printf` blocks in `reader.go` and `compact.go` are historical debugging aids.

## Testing conventions

Tests use `t.TempDir()` with tiny tuning values (`SSTableBlockSize: 128`, `MemTableFlushThreshold: 1024`) to force multi-block, multi-generation behavior on small datasets — keep that pattern when adding tests, otherwise flush and compaction paths never execute. `main/concurrency_test.go` runs writers, readers, and a background compactor concurrently and is the primary guard on the refcounting and locking rules above.
