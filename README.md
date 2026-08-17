# WispDB

[![Go](https://img.shields.io/badge/Go-1.23-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Dependencies](https://img.shields.io/badge/dependencies-none-brightgreen)](go.mod)

A from-scratch LSM-tree storage engine for time-series data, written in Go with **zero external dependencies**.

Every key is the pair `(SeriesID uint64, Timestamp int64)`, ordered by series then time; values are opaque `[]byte`. Writes go through a write-ahead log and an in-memory skiplist first, and get organized into sorted, immutable files on disk in the background — the same design LevelDB, RocksDB, and Cassandra's storage layer are built on.

This project exists to build and understand that design firsthand — every layer (skiplist, WAL, SSTable format, bloom filter, compaction) is implemented from the ground up rather than imported. See [`JOURNEY.md`](JOURNEY.md) for the build story: what broke, how it got fixed, and what it took to get here.

---

## Architecture

```
        Insert / Delete                          Get / Scan
              │                                       │
              ▼                                       ▼
     ┌────────────────┐                     mutable memtable
     │   Write-Ahead   │◄── fsync per record       │
     │      Log        │                            ▼
     └────────────────┘                  immutable memtable (frozen)
              │                                      │
              ▼                                      ▼
     ┌────────────────┐                    SSTables, newest generation
     │ mutable memtable│──rotate on full──►        first
     │   (skiplist)    │                            │
     └────────────────┘                    bloom filter → index →
              │                              block → CRC32 check
        flush on full
              ▼
     ┌────────────────┐
     │    SSTable      │◄── background compaction (k-way merge,
     │  (sorted, immut)│    drops tombstones, dedups generations)
     └────────────────┘
```

**Write path:** append to the WAL (fsync per record) → insert into the mutable memtable. A `Delete` is a tombstone write, not a removal — it flows through every layer as a `Deleted` marker and is authoritative on read: once found, the search stops, even if an older SSTable still holds a value underneath it.

**Read path:** mutable memtable → immutable memtable → SSTables, newest generation first. Each SSTable is checked with a bloom filter before any disk I/O — a negative result skips the file entirely.

**Background:** once a memtable fills up it's frozen and flushed to a new SSTable generation; a compactor periodically k-way merges SSTables together, dropping shadowed keys and (on major compaction) tombstones, so old generations don't accumulate forever.

## Features

- **Write-ahead log** with per-record fsync and generation-based segmentation, so recovery only replays what hasn't been flushed yet, not the entire write history.
- **Skiplist memtable** for the in-memory write buffer, mutable → immutable → flushed lifecycle.
- **SSTable format**: sorted data blocks, a binary-searchable index, a per-table bloom filter, and a CRC32 checksum over the whole file — corruption fails loudly on open instead of returning bad data silently.
- **Compaction**: k-way merge across SSTable generations via a min-heap, newest-generation-wins, with major/minor modes for whether tombstones get dropped.
- **Reference-counted SSTable files**, so compaction can retire a file without racing a concurrent reader still using it.
- **No external dependencies** — `go.mod` is a single `module wisp` line.

## Status

**Implemented:**
- Point lookups (`Get`) and writes (`Insert`/`Delete`) with tombstone support
- Concurrent access with reference-counted SSTable management
- Snappy compression on blocks
- Bloom filters per SSTable
- WAL segmentation and recovery
- CRC32 validation on SSTables

**In progress:**
- Range/scan queries (`Scan`) — freeze-on-scan approach, ready for testing
- See [`next_steps.md`](next_steps.md) for the roadmap and open decisions

## Getting started

Go isn't required to read the code, but you'll need it to build or test.

```powershell
go test ./...                                          # run all tests
go test ./sstable/ -run TestWriterReaderRoundTrip -v    # a single test
go test ./main/ -race -v                                # concurrency tests need -race to mean anything
go vet ./...                                             # primary build/lint check
.\scripts\bench-compare.ps1                              # tests + benchmarks, diffed against the last run
```

> `go build ./...` fails by design: `main/` is a library package with no `func main()` — it's the engine implementation, not a CLI, and only compiles under `go test`/`go vet`. There's no importable public API yet either, since a package named `main` can't be imported from outside its own module; that's expected at this stage, not a bug.

## Project layout

```
skiplist/    in-memory ordered structure backing the memtable
memtable/    mutable/immutable write buffer wrapping the skiplist
wal/         write-ahead log, segmented, fsync per record
sstable/     on-disk sorted table format: writer, reader, index, bloom filter
compactor/   k-way merge across SSTable generations
main/        the Wisp engine — wires the above together, Insert/Delete/Get
scripts/     benchmark tooling
```

Layering is bottom-up: `skiplist` → `memtable` → `wal` + `sstable` → `compactor` → `main`.

## Documentation

- [`JOURNEY.md`](JOURNEY.md) — the build story: bugs hit, fixes made, what each one taught, with sources.
- [`next_steps.md`](next_steps.md) — the live, prioritized roadmap.
- [`BenchmarkAndMemory1.md`](BenchmarkAndMemory1.md) — first full benchmark and allocation profile.

## License

MIT — see [`LICENSE`](LICENSE).
