Priority 1: Bloom filters per SSTable [DONE]
The problem: Right now, Wisp.Get (main/wisp.go) walks every SSTable newest-to-oldest until it finds the key or exhausts the list. For a key that doesn't exist (or only exists in an old generation), that's a full disk read + binary-search-and-block-decode per SSTable — and this gets worse the longer the engine runs and the more generations accumulate between compactions.

The fix: Add a Bloom filter per SSTable, built during Writer flush/compaction and stored in the file (or alongside it), checked before doing any disk I/O for that table. A negative Bloom check lets Get skip the table entirely — no ReadAt, no block decode, no CRC verification.

Priority 2: Targeted block decode (stop allocating the whole block on every Get) [DONE]
The problem: Reader.readBlock (sstable/reader.go) decodes every entry in a block into a freshly allocated []Entry, and each entry's Value gets its own append([]byte(nil), data[valueStart:valueEnd]...) copy — even though Get only wants the one entry matching (seriesID, timestamp). For a block holding 32 entries, a single point lookup allocates 32 Entry structs and up to 32 value-copy slices just to find one.

The fix: Add a Reader.findEntryInBlock(indexEntry, seriesID, timestamp) ([]byte, bool, bool, error) that walks the raw block bytes the same way readBlock does now, but returns as soon as it hits a matching key — allocating exactly one value copy (or zero, on a miss) instead of decoding the whole block. Keep readBlock itself for callers that genuinely need every entry (e.g. compaction's Iterator, if it goes through the same path — worth checking), and have Get call the new targeted version instead.

Priority 3: Range/iterator query API
The problem: Wisp only exposes point lookups (Get(seriesID, timestamp)). Time-series workloads are almost never single-point — "give me everything for series X between t1 and t2" is the basic access pattern, and there's currently no way to do that without calling Get in a loop over guessed timestamps, which doesn't work when you don't already know which timestamps exist.

The fix: Add Wisp.Scan(seriesID, startTs, endTs) (Entry, error) returning a merged iterator over the mutable memtable, immutable memtable, and all SSTables (newest generation wins on duplicate keys, tombstones suppress older values — same precedence rules Get already follows). The compactor's existing k-way merge heap over per-SSTable iterators is the direct template for this; the new piece is merging live memtables into the same heap and stopping at endTs instead of exhausting every source.

Why this ranks above compression/TTL/metrics below: without it, Wisp is a KV store with a two-column key, not a usable time-series engine — every other feature on this list is an optimization or operational concern layered on top of a working query surface. This one changes what the engine can actually be used for.

Priority 4: Per-block compression (snappy/zstd)
The problem: SSTable blocks are stored raw. For time-series data — which is often highly repetitive (similar deltas, sparse values, repeated series IDs) — that wastes disk space and I/O bandwidth on every read and write.

The fix: Compress each block's encoded bytes before writing (block header already carries a blockSize field in the SSTable header, and the format is versioned, so this is an additive change: add a compression-type byte per block or per file, decompress in Reader.readBlock / findEntryInBlock before parsing entries). Snappy is the conventional LSM choice (fast decompress, modest ratio); zstd trades more CPU for a better ratio.

Why this ranks below the range API: it's a real format change (needs a version bump and back-compat handling for existing uncommitted-format files) and a pure resource-efficiency win, not a capability the engine currently lacks entirely.

Priority 5: TTL / retention policies
The problem: Time-series data is usually retained for a bounded window (e.g. 30 days) and then discarded. Right now the only way to remove data is an explicit Delete per key — there's no way to say "drop everything older than X" without an application-level sweep.

The fix: Add a retention window (either global or per-series) that compaction enforces — during CompactSSTables, entries older than the cutoff are dropped the same way major compaction already drops tombstones. This reuses the existing compaction machinery rather than adding a new deletion path.

Why this ranks below compression: it depends on the range/iterator work conceptually (both reason about "all entries for a series across a time window") and is lower urgency — nothing is broken without it, disk just grows unbounded.

Priority 6: Observability (metrics/stats surface)
The problem: There's no visibility into what the engine is doing internally — compaction frequency and duration, flush latency, bloom filter false-positive rate, WAL fsync latency, SSTable count per generation depth. Debugging a slow production instance currently means reading logs or adding printf debugging.

The fix: A Wisp.Stats() snapshot (or a pluggable metrics callback) exposing counters/histograms for the above, updated at the natural points they already occur (flushImmutable, CompactSSTables, WAL.AppendRecord, Reader.MayContain). No new subsystem — just instrumentation of paths that already exist.

Why this ranks below TTL: it's purely additive and low-risk, but doesn't change correctness or capability — it's an operations nicety that matters more once the engine is actually deployed somewhere.

Priority 7: Crash-recovery test harness
The problem: main/concurrency_test.go exercises concurrent writers/readers/compaction, but nothing simulates a hard kill (process death mid-write, mid-flush, or mid-compaction) and asserts Recover() reconstructs correct state afterward. The WAL-truncation-after-flush ordering (CLAUDE.md's "Known in-progress state" note) is exactly the kind of invariant that only a crash-injection test actually proves.

The fix: A test that forks a subprocess (or fakes an os.Exit) at controlled points — after WAL append but before memtable insert, after SSTable write but before WAL truncation, mid-compaction before ReplaceTables — then reopens the engine and verifies no data loss and no double-application.

Why this ranks last among the new items: it's a test-only investment with no product-facing feature, valuable but not blocking any of the above from shipping.

Priority 8 (deferred): WAL group commit (batch fsyncs across concurrent writers)
The problem: WAL.AppendRecord (wal/wal.go) calls w.file.Sync() synchronously on every single record, while holding w.mu. Under concurrent writers, each one serializes behind the previous writer's fsync — and fsync is typically the slowest operation in the whole write path (real disk flush, not just an OS buffer write). This is a correctness-safe design (CLAUDE.md documents it as intentional: "append to WAL (fsync per record)"), but it caps write throughput at roughly 1 / fsync_latency regardless of CPU or how many goroutines are writing.

The fix: Classic group-commit pattern — instead of each AppendRecord call fsyncing for itself, batch: writers append their record to the buffer and enqueue a "waiting for durability" signal; one goroutine (or the last writer to arrive in a short window) performs a single Sync() covering everyone's appended bytes, then wakes all waiters. This trades a small, bounded latency window (batch collection time, typically sub-millisecond to a few ms) for dramatically higher throughput under concurrent load — the same technique RocksDB/LevelDB/Postgres use.

Deferred at the user's request (2026-08-15) — revisit after the range/iterator API and other items above. It's a bigger structural change (touches locking/signaling design in WAL, not just a self-contained function) with real durability-window tradeoffs the user should consciously choose, and only pays off under concurrent write load.
