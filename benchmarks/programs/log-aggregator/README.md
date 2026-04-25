# log-aggregator — runtime program benchmark (1 of 5)

One of five end-to-end CLI programs in `benchmarks/programs/`. See
the [suite README](../README.md) for the overall shape and how to
run the whole set; this file documents the workload only.

A single 50,000-line synthetic log pipeline that exercises parse /
filter / group / sort / format together, the way a deployed CLI tool
actually does.

## Workload

Both binaries do exactly the same thing:

1. Generate 50,000 synthetic log lines from a deterministic LCG seeded
   identically on both sides
   (`"HH:MM:SS LEVEL user=NAME action=VERB"`).
2. For each line: split on `" "`, drop `DEBUG` rows, increment two
   counters — one keyed by level, one keyed by hour bucket
   (`Map<String, Int>`).
3. Sort levels alphabetically; sort hour buckets by count desc, ties
   broken by hour asc; take top 10.
4. Print the summary to stdout.

Output is byte-for-byte identical between the two implementations —
`build.sh` re-asserts that on every build before timing, so any
silent divergence fails the bench instead of producing meaningless
numbers.

```
total: 50000
kept: 37501
levels:
  ERROR: 12503
  INFO: 12499
  WARN: 12499
top hours:
  hour=07 count=1587
  hour=14 count=1587
  ...
```

## Results

See the [suite README](../README.md#latest-results) for the latest
numbers — log-aggregator is the slowest of the five with the
highest String-alloc pressure (~2000–2400× depending on thermal
state).

## What this is measuring vs. osty-vs-go

osty-vs-go reports **per-primitive ratios** — `arith.Add` 0.75x,
`fnv_hash.FnvHash` 0.97x, `fib.Fib15` 0.67x — across 14 microbenches
geomean 3.7×. Each pair is one `#[inline(never)]` function called in a
tight harness loop, GC mostly idle, allocator mostly idle.

This directory measures **whole-program wall clock** on a workload
where every subsystem is live at once: GC running between stages,
allocator hot, string hashing on every Map insert, branch predictor
churning through different code shapes. ~2000× tells you the gap that
shows up when those costs compound, not in any single primitive.

The headline gap (50,000 lines × parse + 2 Map inserts each) is
dominated by:

- `String.split(" ")` returning a fresh `List<String>` per line —
  100,000 list allocs in the hot path. The runtime helper is opaque
  to LLVM, so no fusion / hoisting.
- `Map<String, Int>` insert/get — String hashing is more expensive
  than Go's `map[string]int` and the perf-backlog item
  `Map<K,V> inlining` (MEMORY) hasn't landed yet.
- `String + String` concat in the LCG loop — 50,000 lines × 6 concats
  per line = 300k allocs just to build the corpus.

This matches the per-primitive results: `csv_parse` (1564×) and
`record_pipeline` (74×) are the closest osty-vs-go pairs and they
share these exact bottlenecks.

## Layout

```
benchmarks/programs/log-aggregator/
├── README.md
├── build.sh         # builds both, asserts byte-for-byte output parity
├── run.sh           # hyperfine wrapper (RUNS=N, WARMUP=N env overrides)
├── go/
│   ├── go.mod
│   └── main.go
└── osty/
    ├── osty.toml
    └── main.osty
```

Both binaries are self-contained — no fixture files, no env vars, no
argv. The deterministic LCG makes output reproducible across hosts.

## Reproducing

```sh
just build                                  # ensures .bin/osty is fresh
benchmarks/programs/build-all.sh            # builds all 5 + diffs each
benchmarks/programs/run-all.sh              # hyperfine sweep, prints table
```

To run only this program:

```sh
hyperfine --warmup 1 --runs 5 -N \
    benchmarks/programs/log-aggregator/bin-go-release \
    benchmarks/programs/log-aggregator/osty/.osty/out/release/llvm/log-aggregator
```
