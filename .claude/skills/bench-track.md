---
name: bench-track
description: Run tests and benchmarks, compare to previous run, report CPU/memory/alloc deltas
---

# Benchmark Track Skill

Run all tests, then benchmarks, and compare results against the previous run. Reports ns/op (CPU), B/op (memory), and allocs/op deltas for every benchmark across the codebase.

## What it does

1. Runs `go test ./...` — validates correctness; if this fails, skips benchmarks
2. Runs `go test ./... -bench=. -benchmem -run=^$` — collects benchmarks from all packages
3. Compares against `.claude/benchmarks/baseline.json` and prints a delta table
4. Updates the baseline with the new run

On the first invocation, a baseline file is created. Subsequent runs show % deltas (faster is negative, slower is positive).

## Baseline location

`.claude/benchmarks/baseline.json` — local only, not committed, persists across invocations.

## Example output

```
Benchmark Results Comparison
============================

WAL:
  BenchmarkAppendRecord         ns/op: 1200 → 1050 (-12.5%)  B/op: 256 → 128 (-50%)  allocs: 4 → 0 (-100%)
  BenchmarkSerializePayload     ns/op: 450 → 380 (-15.6%)    B/op: 96 → 48 (-50%)   allocs: 2 → 0 (-100%)

SSTable:
  BenchmarkBlockEncode          ns/op: 2100 → 1800 (-14.3%)  B/op: 512 → 256 (-50%) allocs: 8 → 2 (-75%)
```

## Implementation

Delegates to `scripts/bench-compare.ps1` for all test execution, parsing, diffing, and baseline management.
