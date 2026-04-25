# benchmarks/programs — end-to-end runtime program suite

Five small but real CLI programs, each implemented twice — once in Go,
once in Osty — with **byte-for-byte identical output** so we can
honestly compare wall-clock cost via hyperfine. The osty-vs-go
microbench suite (`benchmarks/osty-vs-go/`) measures per-primitive
ratios; this directory measures whole-program cost on workloads where
GC, allocator, hashing, and dispatch all run together — the shape of
real production CLIs and request handlers.

## Programs

| program | shape | dominant work |
|---|---|---|
| **log-aggregator** | log analyzer CLI | String parse + Map<String,Int> count + sort |
| **markdown-stats** | text classifier | startsWith + Map count + sort |
| **lru-sim** | bounded-cache simulator | List<String> mutation + Map<String,Int> recency |
| **dep-resolver** | build dep graph + depth pass | List<List<Int>> + linear topological scan + Map prefix histogram |
| **expr-calc** | arithmetic interpreter | tokenize + shunting-yard + stack-machine eval |

Together they cover string parsing, classification, state mutation,
graph traversal, and an interpreter loop — the four-five axes of
typical CLI/server workloads.

Each program:

- generates its corpus from a deterministic LCG so there's no fixture
  file to drift between languages
- compiles to a self-contained release binary on each side
- prints a small fixed-shape summary that `build-all.sh` `diff`s on
  every build before timing

## Layout

```
benchmarks/programs/
├── README.md
├── build-all.sh        # build + assert byte-for-byte parity
├── run-all.sh          # hyperfine each program, summary table
├── log-aggregator/
│   ├── go/{go.mod,main.go}
│   ├── osty/{osty.toml,main.osty}
│   └── README.md       # per-program detail
├── markdown-stats/...
├── lru-sim/...
├── dep-resolver/...
└── expr-calc/...
```

## Running

```sh
# from repo root
just build                              # ensures .bin/osty is fresh
benchmarks/programs/build-all.sh        # builds + diff-verifies all 5
benchmarks/programs/run-all.sh          # hyperfine sweep, prints + saves table
```

Defaults to `--runs 5 --warmup 1`; bump via `RUNS=20 WARMUP=3 ./run-all.sh`
once you can afford the wall-time. The Osty side currently takes
multiple seconds per invocation on most programs, so the small default
`--runs` is mostly about keeping the suite under ~5 minutes total.

## Latest results

```
program          go (ms)    osty (ms)   osty/go
---------------  -------    ---------   -------
lru-sim             9.07        65.89      7.3x
expr-calc           5.53        48.56      8.8x
markdown-stats      3.87        41.42     10.7x
dep-resolver        2.95        31.86     10.8x
log-aggregator     12.06       248.84     20.6x
```

(macOS/arm64, Apple Silicon, `--runs 10 --warmup 2`. Reproduce with
`./run-all.sh`; raw JSON per-program in `<prog>/.last-run.json`,
suite summary in `.last-results.md`.)

### Where this is from

Initial measurements ranged from 8× (lru-sim) to **2349×**
(log-aggregator). Profiling the worst case showed ~99% of
log-aggregator's CPU sat inside `osty_gc_post_write_v1` — the GC
write barrier — almost entirely in the recursive-mutex acquire/release
that wrapped every barrier call, plus a remembered-set append that
the current generational marker doesn't actually consume for YOUNG
owners. Two surgical changes to `internal/backend/runtime/osty_runtime.c`:

- **Single-mutator skip** for the recursive lock (the barrier runs
  in mutator code, the only writer to GC state under the STW
  marker).
- **YOUNG-owner skip** for the remembered-set append: the minor
  collector reaches YOUNG values transitively through their owner's
  fields, so the entry is pure overhead unless the owner is OLD or
  pinned.

Both are correctness-preserving — the remembered set still records
exactly the OLD→YOUNG edges the minor collector relies on. The
spread collapsed from **8×–2349× → 7×–21×**, a ~114× speedup on
log-aggregator alone (21.7 s → 0.25 s wall-clock for the same 50,000
lines).

### What's left

The next visible costs are still String-shaped — `Map<String,_>`
insert/get hashing and `String + String` concat each allocate fresh
managed buffers per call. Both are perf-backlog items
(MEMORY: `bench_perf_backlog`, `stdlib_injection_hang`) and would
close the remaining gap further; lru-sim's 7× floor is roughly
where the suite would settle once those land.

## What this is measuring vs. osty-vs-go

`benchmarks/osty-vs-go/` reports **per-primitive ratios** — `arith.Add`
0.75x, `fnv_hash.FnvHash` 0.97x, `fib.Fib15` 0.67x — across 14
microbenches geomean ~3.7×. Each pair is one `#[inline(never)]`
function in a tight harness loop with GC mostly idle.

`benchmarks/programs/` measures **whole-program wall clock** on
workloads where every subsystem is live at once: GC running between
stages, allocator hot, string hashing on every Map insert, branch
predictor churning through different code shapes. Together the two
suites bracket the performance picture: microbenches set the ceiling
(0.7×–4× on isolated primitives), end-to-end programs set the floor
(8×–2349× depending on String-allocator pressure).

## Constraints respected on the Osty side

For every program the Osty implementation:

- **uses `Map<String, Int>` somewhere** so the build dispatches through
  the MIR LLVM lowering path (the alternate path mishandles
  String concat chains in some module shapes — seen during
  development on `markdown-stats`)
- **avoids `Int.toString()`** (LLVM backend limit; uses a local
  `intToStr` digit helper)
- **avoids `Option<scalar>` boxing in hot loops** — uses preallocated
  `List<Int>` + Int stack pointer rather than `pop() -> T?`
- **avoids `xs.split(...)` of strings declared as `let body = a + b
  + ...`** — composes concat directly inside push() / push-arg form
  to dodge a context-dependent String-vs-Int type confusion in the
  IR generator
- only uses **intrinsic** Map operations (`insert`, `get`, `getOr`,
  `keys`, `containsKey`, `remove`); no body-injection mode

These are workarounds for current backend limitations, not language
limitations — the Osty source style is otherwise unconstrained and
mirrors what a deployed app would naturally write.

## Adding a sixth program

Use any existing pair as a template. The constraints to respect:

- **Same output**, byte-for-byte. `build-all.sh` re-asserts on every
  build.
- **No I/O.** Generate input via LCG so there's no fixture drift.
- **Touch a `Map<String, Int>`** somewhere in `main()` so the build
  picks the MIR LLVM path (a stub `histogram.insert("seed", 0)` is
  fine if the workload doesn't naturally need one).
- **One `osty.toml`** at edition 0.4, single `main.osty` next to it.
  CLI scaffold from `osty new --cli` is the canonical layout.
- The directory name = the binary name = the path
  `osty/.osty/out/release/llvm/<name>` that `run-all.sh` looks for.
