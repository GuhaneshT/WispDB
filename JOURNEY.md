# The WispDB Journey

WispDB is an LSM-tree storage engine for time-series data, written from scratch in Go with no external dependencies. Every key is `(SeriesID, Timestamp)`. Below is what actually went wrong while building it, and how I fixed it — not a changelog, just the bugs and design mistakes worth remembering.

Foundation went in first: WAL for durability, a skiplist memtable, an SSTable format to flush into once the memtable filled up. None of that was interesting on its own. Things got interesting once it started getting used the way a real engine gets used — concurrently, with deletes, with many files piling up.

---

## Deletes that didn't actually delete

You can't remove a key from an LSM tree — SSTables are immutable once written. So `Delete` writes a tombstone instead, and that tombstone flows through the memtable, WAL, and SSTable exactly like a normal write. Fine so far.

The bug was on the read side. `Get` would find the tombstone in one SSTable, then keep going and check older SSTables anyway — and if an older one still had the value, it came back as if it were never deleted. A tombstone has to stop the search the moment it's found. It doesn't matter what's sitting underneath it in an older generation; that data is dead and `Get` isn't allowed to look.

This is the kind of bug that doesn't crash anything. It just quietly gives you back data you deleted.

## Two "gone" states that weren't the same thing

An SSTable file can stop being live in two different ways: the DB shuts down cleanly and the file just sits on disk to be reopened later, or the file gets compacted away and should eventually be deleted. Early on these were treated as the same state, and it caused exactly the problems you'd expect — files that should've been removed stuck around, and in one case a file got removed while something still had it open for reading.

Fixed by splitting them into two real states, `closed` and `unlinked`, and adding a reference count to every SSTable file so a file only gets physically deleted once nothing is reading it anymore — not the instant compaction decides it's done with it.

## A flush that wrote an empty file

If everything in a memtable got shadowed by a newer write before it ever got flushed, the flush still ran and wrote out a file — just a header and a footer with zero actual entries in between. Nothing crashed, but it left an empty shell sitting on disk that anything downstream would eventually have to deal with. Fix was simple: count how many entries actually got written, and if it's zero, delete the file instead of leaving it.

## Recovery replaying the whole history every time

Before WAL segmentation, restarting the engine meant replaying the *entire* write-ahead log from the very beginning, every time — even though most of that history was already safely sitting in SSTables. Segmentation split the WAL into numbered chunks that get truncated once their data is durably flushed.

The part I had to be careful about: truncation can only happen *after* the SSTable write succeeds, never before. If you flip that order, a crash between "SSTable written" and "WAL truncated" loses data for good. Same discipline Postgres and every serious WAL implementation follows — you don't throw away the log entry until you're sure the thing it was protecting survived.

## Corruption that looked like a normal read

A corrupted SSTable — bad sector, truncated write, whatever — would just return something wrong or fail in a confusing way, with nothing actually telling you the file was bad. Added a CRC32 over the header, blocks, index, and bloom filter, verified whenever a file is opened, so corruption fails loudly the same way a bad magic number or version mismatch does. An engine that can't tell you it just read garbage is worse than one that crashes outright.

## The allocation hotspot I only found by profiling

`encoding/binary.Write` and `.Read` are known to be slow in Go — they box every field into an `interface{}` and fall back to reflection under the hood. I suspected it but didn't know how bad until profiling the insert path (results in [`BenchmarkAndMemory1.md`](BenchmarkAndMemory1.md)) — 45% of all allocations on a single insert traced straight back to `binary.Write`.

Replaced it with manual `binary.LittleEndian.PutUintXX` calls into a reused buffer instead — pooled for the WAL's concurrent callers, a plain scratch buffer for the SSTable writer's single-owner path. Same wire format, allocation gone. This is the one fix in the whole project that came from a number, not a hunch — the benchmark script named the cost, and I went and fixed exactly that.

## Every lookup scanning every file, even for keys that don't exist

Before bloom filters, a miss meant checking every SSTable — binary search plus a block decode against each one — before giving up. Classic read amplification, and it only gets worse as more SSTables pile up between compactions. Added a bloom filter per SSTable, keyed on `SeriesID`, checked before touching disk at all. A negative result skips the block search, the read, and the CRC work entirely for that file.

## Decoding a whole block to find one key

`readBlock` was decoding *every* entry in a block — with a fresh value copy for each one — just to answer a lookup for a single key. A block with 32 entries meant 32 allocations to find one match. Added `findEntryInBlock`, which walks the raw bytes and stops as soon as it matches, so a hit costs one copy and a miss costs none. Kept `readBlock` around too, since compaction genuinely needs every entry in a block — no reason to make the common case (one lookup) pay for the rare case (full scan).

## Three iterators that quietly disagreed with each other

The skiplist, memtable, and SSTable each had their own iterator, and they'd grown independently without ever needing to agree on anything — until I started building a range/scan query that needs to merge across all three. That's when the cracks showed: the skiplist iterator bundled key and value but handled `Deleted` separately, and the SSTable iterator had a straight-up copy-paste bug where `Key()` returned an entire `Entry` instead of just the key.

None of this mattered while each iterator was only ever used on its own. Fixed by making `skiplist.Key` the one key type everyone agrees on, and giving every iterator's `Entry()` call the same `(Key, Value, Deleted)` shape.

## Is it even safe to scan the memtable mid-write?

This one isn't fixed yet, just scoped. Before writing scan/merge logic, the real question is whether an iterator can stay open across the mutable memtable, the immutable memtable, and every SSTable for the whole duration of a scan. Two of the three already handle this fine: SSTables, because of the refcounting from the closed/unlinked fix above, and the immutable memtable, because `Freeze()` makes it reject further writes the second it's snapshotted.

The mutable memtable is the actual problem — it has no locking of its own and depends entirely on the engine holding a lock, which a long scan can't do without stalling every writer for however long the scan takes. Looked at how LevelDB and RocksDB handle this: their memtable skiplists are lock-free for reads by design — inserts only ever append new nodes and never touch an existing one, node visibility uses atomic pointer publication so a reader can never see a half-written pointer, and memory comes from an arena that's never freed while the structure is live. That's a real rewrite of the skiplist, not a quick patch, so it's written up as an open decision in [`next_steps.md`](next_steps.md) with two cheaper alternatives ranked against it.

---

## Patterns that kept showing up

- Deletes are writes. The write half is easy; forgetting to make the read half respect it is where it actually breaks.
- Never throw away the old copy until the new one is proven safe — same idea behind WAL truncation ordering and refcounted file deletion, two unrelated bugs with the same root cause.
- Profile before optimizing. The one clear allocation win in this project came from a number the profiler produced, not a guess.
- It's fine for two callers to use two different code paths when their needs are actually different — don't force the common case to pay for the rare one.
- Small inconsistencies between components don't matter until something needs to cross the boundary between them. That's exactly when they stop being harmless.

---

## Sources

**Tombstones & deletion in LSM trees**
- [LSM Trees — The Complete Guide to WAL, MemTables, SSTables, Compaction & Bloom Filters](https://medium.com/@harshithgowdakt/lsm-trees-the-complete-guide-to-wal-memtables-sstables-compaction-bloom-filters-7ddde77935f4)
- [LSM Tree, Tombstones and YugabyteDB (No Vacuum)](https://dev.to/yugabyte/lsm-tree-tombstones-and-yugabytedb-31cc)
- [About Deletes and Tombstones in Cassandra](https://thelastpickle.com/blog/2016/07/27/about-deletes-and-tombstones.html)

**Write-ahead log durability & truncation ordering**
- [How Write-Ahead Logging Makes Databases Crash-Safe](https://medium.com/@vinodbokare0588/how-write-ahead-logging-makes-databases-crash-safe-7d420a03fca5)
- [Mastering the Postgres Write-Ahead Log](https://martinuke0.github.io/posts/2026-05-25-mastering-the-postgres-write-ahead-log-architecture-durability-guarantees-and-implementation-for-data-integrity/)
- [Write-Ahead Logging: Why Your Database Never Loses Data Even When It Crashes](https://medium.com/@moksh.9/write-ahead-logging-why-your-database-never-loses-data-even-when-it-crashes-7823faf4471d)

**`encoding/binary` reflection/allocation cost**
- [golang/go#2634 — encoding/binary: Write is too slow](https://github.com/golang/go/issues/2634)
- [golang/go#27403 — fast-paths in Read and Write allocate despite attempted optimization](https://github.com/golang/go/issues/27403)
- [Golang High Performance Programming Manual](https://www.sobyte.net/post/2022-07/golang-performance/)

**Bloom filters & read amplification**
- [Bloom Filter: How It Works & Use in Cassandra — ScyllaDB](https://www.scylladb.com/glossary/bloom-filter/)
- [What Is a Log-Structured Merge Tree (LSM Tree)? — Aerospike](https://aerospike.com/blog/log-structured-merge-tree-explained/)
- [LSM Trees, Memtables & Sorted String Tables: An Introduction](https://www.darchuletajr.com/blog/lsm-trees-memtables-sorted-string-tables-introduction)

**Lock-free memtable scanning (LevelDB/RocksDB skiplist design)** — full writeup in [`next_steps.md`](next_steps.md)
- [LevelDB Explained — The Implementation Details of MemTable](https://selfboot.cn/en/2025/06/11/leveldb_source_memtable/)
- [LevelDB Source Reading (4): Concurrent Access](http://tonyz93.blogspot.com/2016/11/leveldb-source-reading-4-concurrent.html)
- [LevelDB Explained — How to implement SkipList](https://selfboot.cn/en/2024/09/09/leveldb_source_skiplist/)
- [Prometheus TSDB (Part 1): The Head Block](https://ganeshvernekar.com/blog/prometheus-tsdb-the-head-block/)

---

## What's next

[`next_steps.md`](next_steps.md) has the live list — decide the memtable scan concurrency approach, build the actual range/scan API, then compression, TTL, observability, and a crash-recovery test harness.
