# osty-vs-go

Side-by-side microbenchmarks for the **Osty language** (LLVM backend,
the one that ships user programs) vs **plain Go** (the `go test -bench`
harness). The Go portions of the Osty *toolchain* are out of scope —
this directory compares the two languages' emitted code quality, not
the host plumbing.

## Layout

```
benchmarks/osty-vs-go/
├── arith/
│   ├── go/      # package arithbench — BenchmarkAdd
│   └── osty/    # benchAdd
├── loop_sum/
│   ├── go/      # package loopsumbench — BenchmarkSumTo100
│   └── osty/    # benchSumTo100
└── fib/
    ├── go/      # package fibbench — BenchmarkFib15
    └── osty/    # benchFib15
```

One bench per workload, named the same on both sides: Go's
`BenchmarkFoo` pairs with Osty's `benchFoo`. Adding a new pair is just
a new top-level directory that follows the same layout — the runner
discovers it automatically.

## Running

```sh
just build                       # ensures .bin/osty is fresh
go run ./cmd/osty-vs-go --benchtime 500ms --label "baseline"
```

Useful flags:

- `--benchtime <dur>` — forwarded to both sides (Go's `-benchtime`
  and Osty's `--benchtime`). Default `500ms`.
- `--filter <regex>` — only run pair names matching the regex.
- `--label <tag>` — short tag stored with the run (appears in the
  history listing; good for marking `main`, `branch-x`, etc.).
- `--osty <path>` / `--go <path>` — override the binaries. The default
  prefers `$PWD/.bin/osty` (from `just build`) and the `go` on `PATH`.
- `--pairs-dir <path>` — defaults to `benchmarks/osty-vs-go`.
- `--history <N>` — skip running and render the last N recorded runs
  as an ASCII trend graph (see below).
- `--research` — skip running and print the research journal: current
  champion, latest run, verdict, and per-bench deltas (see
  [Autoresearch](#autoresearch-one-metric-one-loop-keep-the-best)).
- `--loop <dur>` — keep re-running on a fixed cadence until Ctrl-C;
  every tick prints a vs-best verdict so you can iterate on the
  compiler in a tight edit → measure → decide loop.
- `--noise <frac>` — band under which a vs-best delta is classified
  as "within noise" rather than a regression. Default `0.02` (±2%).

Sample output of one run:

```
# osty-vs-go run #3 (baseline) — benchtime=500ms, pairs=3

pair      bench     go ns/op  osty ns/op  ns osty/go  go B/op  osty B/op  go allocs/op
--------  --------  --------  ----------  ----------  -------  ---------  ------------
arith     Add       1.20      161.0       134.17x     0        0          0
fib       Fib15     13002.00  38195.0     2.94x       0        0          0
loop_sum  SumTo100  205.50    2001.0      9.74x       0        0          0

geomean ns osty/go: 10.90x  (over 3 benches)
```

## Metrics

Per bench the tool collects:

- **ns/op** (both sides). Go's is `testing.B` time-per-op after
  auto-tuned `b.N`. Osty's is the `avg=…ns` field from
  `testing.benchmark(N, …)` with per-iteration clock sampling.
- **B/op** (both sides). Go's comes from `-benchmem`. Osty's comes
  from a runtime-side delta on `osty_gc_allocated_bytes_total`
  sampled before and after the timed region, divided by the final
  iteration count. Zero means the body didn't drive the GC.
- **allocs/op** (Go only for now — Osty's GC doesn't expose a cheap
  allocation-count accessor yet, so the Osty column is left blank.)
- **ns osty/go ratio** per bench and a **geometric mean** across all
  benches that have both sides. Geomean is less sensitive to single
  microbenches blowing up than arithmetic mean.

## Run history + trend graph

Every run appends one JSON object to `.osty-vs-go-history.jsonl` at
the repo root. Each object carries the timestamp, the `--label`, the
benchtime duration, and every measurement. `--history N` re-reads the
last N runs and draws an ASCII sparkline per bench:

```
$ go run ./cmd/osty-vs-go --history 5
# osty-vs-go history — last 5 run(s)

  run 1: #9  2026-04-20 14:12:03 baseline   (benchtime=500ms)
  run 2: #10 2026-04-21 09:01:47 try-inline (benchtime=500ms)
  run 3: #11 2026-04-21 09:18:12 try-inline (benchtime=500ms)
  run 4: #12 2026-04-21 10:47:55 post-merge (benchtime=500ms)
  run 5: #13 2026-04-21 11:12:01 post-merge (benchtime=500ms)

pair.bench                    trend (osty ns/op)  latest (go ns/op, osty ns/op, osty B/op)
------------------------------------------------------------------------------------------
arith.Add                     ▄▇█▂▁  go 1.20, osty 148.0, osty B/op 0
fib.Fib15                     ▅▆█▃▁  go 13002.00, osty 29201.0, osty B/op 0
loop_sum.SumTo100             ▅█▄▃▁  go 205.50, osty 1912.0, osty B/op 0
```

The `#<num>` on the `run N` line is the absolute 1-based run index
across every entry ever written to the history file, so you can still
correlate with old runs after trimming the display window. Each
sparkline cell is one run; the cell height is scaled per-bench between
that bench's min and max across the window, so a fast bench and a
slow bench both fill the column independently. Missing measurements
(e.g. a bench that was added later) render as blank cells instead of
shifting the column alignment.

Delete `.osty-vs-go-history.jsonl` to reset history.

## Autoresearch: one metric, one loop, keep the best

Inspired by [karpathy/autoresearch][karpathy] (2026). The original is
an AI agent that edits training code, runs a 5-minute experiment,
keeps changes that beat the previous best score, and repeats overnight.
Here we borrow the **keep-the-best / one-metric** discipline without
the LLM agent — the human (or an outer-loop agent) is the one editing
the compiler between runs; the tool's job is to make every edit land
with a clear one-line verdict.

The one metric: `geomean(ns_osty / ns_go)` across every bench pair
with both sides present. **Lower is better.** It's persisted on the
run record (`"score"` in the JSON) and the current git HEAD is
captured too, so you can correlate scores with commits even if the
working tree has moved on.

### On every run

Each `osty-vs-go` invocation compares its score against the best
score seen in prior history and prints one of:

```
-> NEW BEST: score 17.260  (prev champion 18.754 at run #2; -7.97%)
   top per-bench deltas (osty ns/op, vs champion):
     ↓ fib.Fib15          16462.0 -> 9643.0     (-41.42%)
     ↓ loop_sum.SumTo100    829.0 -> 496.0      (-40.17%)
     ↓ arith.Add             65.0 -> 56.0       (-13.85%)
```

```
-> regression: score 18.614 vs best 17.260 at run #3 (+7.84%)
   top per-bench deltas …
```

```
-> within noise: score 17.339 vs best 17.260 at run #3 (+0.46%)
```

```
-> first run: score 19.360 (no prior data)
```

The `--noise` flag controls the band. Default ±2% — tight enough to
catch real regressions, loose enough that normal clock jitter on
short benches doesn't cry wolf.

### `--research` — just the journal

Skip the bench run and print the current state of the research:

```
$ go run ./cmd/osty-vs-go --research
# osty-vs-go research journal — 6 run(s)

  champion: run #3 third @ b0c6cd06-dirty  (score 17.260, 2026-04-22 00:55:07)
  latest: run #6 variance-3 @ b0c6cd06-dirty  (score 21.177, 2026-04-22 00:55:39)

  verdict: regression  (+22.69% vs champion)
   top per-bench deltas (osty ns/op, vs champion):
     ↑ arith.Add             56.0 -> 75.0   (+33.93%)
     ↑ loop_sum.SumTo100    496.0 -> 632.0  (+27.42%)
     ↓ fib.Fib15          9643.0 -> 9130.0  (-5.32%)

  3 run(s) since the champion was set (run #3 -> run #6)
```

### `--loop` — continuous feedback

Runs forever until Ctrl-C, sleeping `--loop <dur>` between sweeps.
Edit the Osty compiler / runtime in another terminal; every tick
re-measures, prints the vs-best verdict, and appends to history.
Pairs naturally with an AI coding agent as the outer loop: the agent
proposes a compiler tweak, you or CI run the sweep, the verdict
decides whether to keep the edit.

```sh
go run ./cmd/osty-vs-go --loop 5m --benchtime 500ms --label "inline-probe"
# run #7 — score 17.260 … -> NEW BEST (-4.1%)
# (waiting 5m before next sweep; Ctrl-C to stop)
# run #8 — score 17.183 … -> NEW BEST (-0.4%)
# …
```

### Caveats

- Git HEAD capture appends `-dirty` when the tree has uncommitted
  edits, so runs against an in-progress branch stay honest in the
  journal.
- The composite is geomean over **every pair that has both sides**.
  A pair added mid-history contributes to later scores but not
  earlier ones, which shifts the comparison basis. When you add a
  new pair, expect a step change on the next run's score.
- Noise bands are set low by default. For microbenches (arith-style)
  prefer `--benchtime 2s` or longer to quiet jitter before trusting
  verdicts.

[karpathy]: https://github.com/karpathy/autoresearch

## How the numbers compose (and when to distrust them)

- **Go ns/op** comes straight from `go test -run=^$ -bench=. -benchmem
  -benchtime=<dur>`. `b.N` auto-tunes to fit the duration budget and
  the reported `ns/op` is total-elapsed divided by `b.N` after an
  implicit warmup.
- **Osty ns/op** is the `avg=…ns` field from `osty test --bench
  --benchtime <dur>`. The Osty harness does its own warmup of
  `clamp(N/10, 1, 1000)` iterations before the first clock sample and
  samples each iteration individually so it can report
  `min/p50/p99/max`. That per-iter clock pair costs ~30–60ns on a
  modern x86/arm core, so for **nanosecond-scale bodies the Osty
  number is inflated by sampling overhead**, not by the language
  itself. Use `fib` and `loop_sum` when you want fair comparisons;
  treat `arith` as a worst-case ceiling.
- **Ratios** are `osty_ns / go_ns` as a geometric mean across pairs.
  Arithmetic mean would let one microbench (usually `arith`) dominate
  the average.
- **bytes/op** for Osty is GC-tracked (heap allocations only — values
  that stay on the stack / in registers don't contribute). Go's
  `-benchmem` measures the same scope, so the two columns are
  comparable in practice.

Other things the tool does *not* control for:

- Go's benchmark runs in the same process as the test harness; Osty
  emits a fresh binary per bench and clang links it. Only the inner
  timing is reported, but if you see a 3s wall-clock on a 500ms bench,
  most of that is compile + link.
- DCE defenses: Go's `//go:noinline` marks keep the measured function
  honest. Osty has no equivalent attribute yet, so `let _ = add(1, 2)`
  could in principle be elided by a future optimiser pass; it isn't
  today.
- Thermal state, turbo boost, background apps, and laptop power
  profiles affect both sides. Run with `--benchtime 2s` or longer to
  reduce noise when comparing branches.

## Adding a new pair

1. `mkdir -p benchmarks/osty-vs-go/<name>/{go,osty}`
2. In `go/`, write a Go file + `_test.go` with one or more
   `BenchmarkFoo(b *testing.B)` functions. Pick a package name unique
   across the tree (convention: `<name>bench`).
3. In `osty/`, write the matching `.osty` file + `_test.osty` with
   `fn benchFoo()` bodies that call `testing.benchmark(N, || {...})`.
   The Osty function name's `bench` prefix is stripped for the match,
   so `benchFoo` ↔ `BenchmarkFoo`.
4. Re-run `go run ./cmd/osty-vs-go` — the new pair shows up without
   any registration.
