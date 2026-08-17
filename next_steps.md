# WispDB — Next Steps

## Priority 1: Bloom filters per SSTable [DONE]
**The problem:** Right now, Wisp.Get (main/wisp.go) walks every SSTable newest-to-oldest until it finds the key or exhausts the list. For a key that doesn't exist (or only exists in an old generation), that's a full disk read + binary-search-and-block-decode per SSTable — and this gets worse the longer the engine runs and the more generations accumulate between compactions.

**The fix:** Add a Bloom filter per SSTable, built during Writer flush/compaction and stored in the file (or alongside it), checked before doing any disk I/O for that table. A negative Bloom check lets Get skip the table entirely — no ReadAt, no block decode, no CRC verification.

## Priority 2: Targeted block decode (stop allocating the whole block on every Get) [DONE]
**The problem:** Reader.readBlock (sstable/reader.go) decodes every entry in a block into a freshly allocated []Entry, and each entry's Value gets its own append([]byte(nil), data[valueStart:valueEnd]...) copy — even though Get only wants the one entry matching (seriesID, timestamp). For a block holding 32 entries, a single point lookup allocates 32 Entry structs and up to 32 value-copy slices just to find one.

**The fix:** Add a Reader.findEntryInBlock(indexEntry, seriesID, timestamp) ([]byte, bool, bool, error) that walks the raw block bytes the same way readBlock does now, but returns as soon as it hits a matching key — allocating exactly one value copy (or zero, on a miss) instead of decoding the whole block. Keep readBlock itself for callers that genuinely need every entry (e.g. compaction's Iterator, if it goes through the same path — worth checking), and have Get call the new targeted version instead.

## Priority 3: Range/iterator query API
**The problem:** Wisp only exposes point lookups (Get(seriesID, timestamp)). Time-series workloads are almost never single-point — "give me everything for series X between t1 and t2" is the basic access pattern, and there's currently no way to do that without calling Get in a loop over guessed timestamps, which doesn't work when you don't already know which timestamps exist.

**The fix:** Add Wisp.Scan(seriesID, startTs, endTs) (Entry, error) returning a merged iterator over the mutable memtable, immutable memtable, and all SSTables (newest generation wins on duplicate keys, tombstones suppress older values — same precedence rules Get already follows). The compactor's existing k-way merge heap over per-SSTable iterators is the direct template for this; the new piece is merging live memtables into the same heap and stopping at endTs instead of exhausting every source.

**Why this ranks** above compression/TTL/metrics below: without it, Wisp is a KV store with a two-column key, not a usable time-series engine — every other feature on this list is an optimization or operational concern layered on top of a working query surface. This one changes what the engine can actually be used for.

### Open decision: mutable memtable scan concurrency. SSTable iterators are already safe for a long-lived scan (GetTables/ReleaseTables refcounting pins the file, same pattern Get uses), and the immutable memtable is safe too (Freeze() makes Put/Delete refuse further writes, so its skiplist is content-frozen the instant it's snapshotted). The mutable memtable is the open problem: CLAUDE.md notes "the skiplist itself has no internal locking and relies entirely on w\.mu," and skiplist.insert does an in-place mutation on a duplicate key (next.Value = value; next.Deleted = deleted) rather than only ever appending — so a scan that walks forward pointers after releasing w\.mu races with concurrent Insert/Delete. Three options, not yet decided:

1\. Lock-free append-only skiplist (RocksDB/LevelDB method — fastest, most work). Both engines make the memtable single-writer/multi-reader with ***zero*** read-side locking, and it's not just "reads happen to be quick" — it's structural:
- The skiplist is append-only: insert only ever prepends brand-new nodes ahead of existing ones. An existing node's key/value is never touched again once linked in — no in-place update on a duplicate key the way WispDB's skiplist.insert does today. (RocksDB's actual behavior on a duplicate key is to insert a second node for the same key rather than mutate the first; newest-wins is resolved at read time by sequence number, the same "newest generation wins" idea WispDB's SSTable generations already use.)
- Node publication uses an atomic release-store on the node's forward pointer when linking it into the list, paired with an atomic acquire-load on the reader side when traversing forward pointers. That ordering guarantees a concurrent reader either observes the list state fully before the insert or fully after it — never a torn/partial pointer — which is what makes lock-free traversal memory-safe, not merely "usually fine in practice."
- Memory comes from an arena (bump allocator): nodes are allocated once and never freed or resized while the memtable is reachable, so there's no use-after-free risk from a reader that's lagging behind a concurrent writer (no ABA problem, no dangling pointer if a node were freed mid-read).
- Concurrent *\*writers\** are still serialized by one write lock/mutex — only inserting requires mutual exclusion; iteration and point lookups need no lock at all, at any point, even during heavy concurrent writes. This is why it's "fastest": a scan never blocks writers and is never blocked by them.
- Cost for WispDB: this means reworking skiplist.go's node struct to use atomic.Pointer for forward links, removing the in-place mutation branch in insert(), and resolving duplicate keys at merge/read time instead of at insert time — a real change to the core data structure, not an additive scan feature.
2\. Freeze-on-scan (pragmatic middle ground). Reuse the existing freezeMutableLocked() rotation (same path flush already triggers): briefly hold w\.mu to seal the current mutable memtable into the immutable slot and install a fresh mutable memtable, then scan the now-frozen copy lock-free, same as the immutable memtable is already handled. One bounded rotation cost per scan instead of per-entry, no changes to the skiplist itself. Needs to fold into the existing single-slot immutable memtable design (only one pending-flush immutable memtable at a time) rather than bypass it.
3\. Hold w\.mu.RLock() for the whole scan (simplest, slowest). Correct with zero new code, but stalls all writers for as long as the scan runs — a long range scan under concurrent write load could meaningfully hurt write latency.

## Sources

**Scan concurrency & skiplist design (Priority 3)**
- [LevelDB Explained - The Implementation Details of MemTable](https://selfboot.cn/en/2025/06/11/leveldb_source_memtable/)
- [LevelDB Source Reading (4): Concurrent Access](http://tonyz93.blogspot.com/2016/11/leveldb-source-reading-4-concurrent.html)
- [LevelDB Explained - How to implement SkipList](https://selfboot.cn/en/2024/09/09/leveldb_source_skiplist/)
- [Prometheus TSDB (Part 1): The Head Block | Ganesh Vernekar](https://ganeshvernekar.com/blog/prometheus-tsdb-the-head-block/)
- [prometheus/tsdb/head.go at main · prometheus/prometheus](https://github.com/prometheus/prometheus/blob/main/tsdb/head.go)

**Read & write latency optimizations (Priority 4.7)**

*RocksDB — compaction strategy & amplification tradeoffs*
- [RocksDB Tuning Guide — read/write amplification, compaction strategies](https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide)
- [5 RocksDB Tweaks That Tame Write Amplification](https://medium.com/@connect.hashblock/5-rocksdb-tweaks-that-tame-write-amplification-f31685910d6e)
- [Architecting Log-Structured Merge Trees: Optimizing Write-Intensive Performance](https://martinuke0.github.io/posts/2026-05-26-architecting-log-structured-merge-trees-optimizing-write-intensive-performance-for-distributed-database-systems-at-scale/)

*Pebble (CockroachDB) — block cache, compression profiles, L0 sublevels*
- [Storage Layer — block cache and compression strategies](https://www.cockroachlabs.com/docs/v26.2/architecture/storage-layer)
- [Value Separation in Pebble: Storage Engine Optimization](https://www.cockroachlabs.com/blog/value-separation-pebble-optimization/)
- [The LSM Design Space and its Read Optimizations](https://subhadeep.net/assets/fulltext/The_LSM_Design_Space_and_its_Read_Optimizations.pdf)

*VictoriaMetrics — concurrency limiting, cardinality management for time-series*
- [Performance optimization: string interning for cardinality](https://victoriametrics.com/blog/tsdb-performance-techniques-strings-interning/)
- [Performance optimization: limiting concurrency reduces thread overhead](https://victoriametrics.com/blog/tsdb-performance-techniques-limiting-concurrency/)
- [Performance optimization: function result caching](https://victoriametrics.com/blog/tsdb-performance-techniques-functions-caching/)

## Priority 4: Per-block compression (snappy/zstd) [DONE]
**The problem:** SSTable blocks are stored raw. For time-series data — which is often highly repetitive (similar deltas, sparse values, repeated series IDs) — that wastes disk space and I/O bandwidth on every read and write.

**The fix:** Compress each block's encoded bytes before writing. Snappy is the conventional LSM choice (fast decompress, modest ratio); zstd trades more CPU for a better ratio. Done: blocks are always snappy-compressed, no metadata needed.

## Priority 4.5: Delta compression (follow-up optimization)
**The problem:** Snappy achieves ~40% reduction on raw block bytes, but time-series data has more structure snappy doesn't exploit: timestamps are sequential (100, 200, 300 → deltas 100, 100, 100), seriesIDs repeat within a block (all 1s, then all 2s → delta 0, 0, 0). Deltas are much smaller and compress better than absolutes.

**The fix:** Encode timestamps and values as deltas before snappy compression. Store the first (absolute) value, then encode the delta from the prior value for each subsequent entry. Snappy on deltas gives 60–70% reduction vs 40% on raw — essentially free extra savings since decompression is already happening. Adds complexity to block encoding/decoding but is a pure extension (old tables still work).

**Why later:** Snappy alone ships today and is a 2–3x improvement in disk usage. Delta compression is the follow-up win once you have numbers showing the current ratio and can measure the actual improvement.

**Why this ranks** below the range API: it's a real format change (needs a version bump and back-compat handling for existing uncommitted-format files) and a pure resource-efficiency win, not a capability the engine currently lacks entirely.

## Priority 4.7: Read & write latency optimizations (measure-first)

**The problem:** Once scan is working, profiling will show where latency is spent. Common bottlenecks in LSM engines:
- **Read latency:** decompressing the same blocks repeatedly (not cached between calls)
- **GC pressure:** allocations during decompression/parsing per read
- **Read amplification:** number of SSTable files checked per logical read (bloom filters help, but compaction strategy matters)
- **Write latency:** WAL fsync serializes concurrent writers; group commit unbatches this

**The fix (rank by impact, do only after profiling):**

1. **Block cache** — keep decompressed blocks in memory so cache hits skip decompression. Benefit: 10–50x faster for hot blocks. Effort: 100–200 lines. Measurement: how often are the same blocks read repeatedly in real workload?

2. **WAL group commit** — batch fsyncs across concurrent writers instead of per-record (see Priority 8 for details). Benefit: 10–100x throughput increase under load, but ~1–2ms latency increase per write. Effort: 200+ lines, architectural change. Tradeoff: throughput vs write latency.

3. **Buffer pool for decompression** — reuse byte slices via sync.Pool to reduce GC. Benefit: ~5–10% latency improvement. Effort: 10–20 lines. Measurement: does profiling show snappy.Decode allocation in the hot path?

4. **Tunable bloom filter FPR** — higher false positive rate = faster writes, more disk reads on misses. Benefit: context-dependent. Effort: 5 lines to parameterize. Measurement: what's the actual false positive rate on real data?

5. **Prefetch next block during scan** — queue background reads for adjacent blocks while processing current one. Benefit: 10–20% scan latency if I/O-bound. Effort: 50–100 lines. Measurement: is I/O latency actually the bottleneck for scans?

6. **Keep SSTables open** — reuse file handles across reads instead of open/close per-read. Benefit: ~1–3% latency (saves syscalls). Effort: 50–100 lines. Measurement: how much time is spend in Open syscalls?

**Why measure first:** RocksDB tuning shows the bottleneck is workload-specific. What looks like a 50% win on paper (fewer syscalls) can be invisible in practice if mutex contention is actually the issue. Pebble's compression profiles show that "best" compression varies by level and block type — one-size-fits-all doesn't work.

**Learn from:**
- [RocksDB Tuning Guide](https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide) — compaction strategy, read/write amplification tradeoffs
- [Pebble compression profiles](https://www.cockroachlabs.com/docs/v26.2/architecture/storage-layer) — different strategies for different layers (fastest for L0, good for lower levels)
- [VictoriaMetrics concurrency limiting](https://victoriametrics.com/blog/tsdb-performance-techniques-limiting-concurrency/) — reducing thread overhead saves more than optimizing individual syscalls

## Priority 5: TTL / retention policies
**The problem:** Time-series data is usually retained for a bounded window (e.g. 30 days) and then discarded. Right now the only way to remove data is an explicit Delete per key — there's no way to say "drop everything older than X" without an application-level sweep.

**The fix:** Add a retention window (either global or per-series) that compaction enforces — during CompactSSTables, entries older than the cutoff are dropped the same way major compaction already drops tombstones. This reuses the existing compaction machinery rather than adding a new deletion path.

**Why this ranks** below compression: it depends on the range/iterator work conceptually (both reason about "all entries for a series across a time window") and is lower urgency — nothing is broken without it, disk just grows unbounded.

## Priority 6: Observability (metrics/stats surface)
**The problem:** There's no visibility into what the engine is doing internally — compaction frequency and duration, flush latency, bloom filter false-positive rate, WAL fsync latency, SSTable count per generation depth. Debugging a slow production instance currently means reading logs or adding printf debugging.

**The fix:** A Wisp.Stats() snapshot (or a pluggable metrics callback) exposing counters/histograms for the above, updated at the natural points they already occur (flushImmutable, CompactSSTables, WAL.AppendRecord, Reader.MayContain). No new subsystem — just instrumentation of paths that already exist.

**Why this ranks** below TTL: it's purely additive and low-risk, but doesn't change correctness or capability — it's an operations nicety that matters more once the engine is actually deployed somewhere.

## Priority 7: Crash-recovery test harness
**The problem:** main/concurrency_test.go exercises concurrent writers/readers/compaction, but nothing simulates a hard kill (process death mid-write, mid-flush, or mid-compaction) and asserts Recover() reconstructs correct state afterward. The WAL-truncation-after-flush ordering (CLAUDE.md's "Known in-progress state" note) is exactly the kind of invariant that only a crash-injection test actually proves.

**The fix:** A test that forks a subprocess (or fakes an os.Exit) at controlled points — after WAL append but before memtable insert, after SSTable write but before WAL truncation, mid-compaction before ReplaceTables — then reopens the engine and verifies no data loss and no double-application.

**Why this ranks** last among the new items: it's a test-only investment with no product-facing feature, valuable but not blocking any of the above from shipping.

## Priority 8 (deferred): WAL group commit (batch fsyncs across concurrent writers)
**The problem:** WAL.AppendRecord (wal/wal.go) calls w\.file.Sync() synchronously on every single record, while holding w\.mu. Under concurrent writers, each one serializes behind the previous writer's fsync — and fsync is typically the slowest operation in the whole write path (real disk flush, not just an OS buffer write). This is a correctness-safe design (CLAUDE.md documents it as intentional: "append to WAL (fsync per record)"), but it caps write throughput at roughly 1 / fsync_latency regardless of CPU or how many goroutines are writing.

**The fix:** Classic group-commit pattern — instead of each AppendRecord call fsyncing for itself, batch: writers append their record to the buffer and enqueue a "waiting for durability" signal; one goroutine (or the last writer to arrive in a short window) performs a single Sync() covering everyone's appended bytes, then wakes all waiters. This trades a small, bounded latency window (batch collection time, typically sub-millisecond to a few ms) for dramatically higher throughput under concurrent load — the same technique RocksDB/LevelDB/Postgres use.

Deferred at the user's request (2026-08-15) — revisit after the range/iterator API and other items above. It's a bigger structural change (touches locking/signaling design in WAL, not just a self-contained function) with real durability-window tradeoffs the user should consciously choose, and only pays off under concurrent write load.
