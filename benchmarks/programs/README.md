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
lru-sim             8.15        66.11       8x
dep-resolver        3.40       436.14     128x
markdown-stats      3.52      1103.92     314x
expr-calc           3.81      2087.49     548x
log-aggregator      9.25     21730.98    2349x
```

(macOS/arm64, Apple Silicon, `--runs 5 --warmup 1`. Reproduce with
`./run-all.sh`; raw JSON per-program in `<prog>/.last-run.json`,
suite summary in `.last-results.md`.)

**What the spread is telling us.** Every program does roughly the
same kind of work (parse-ish → loop → group → format) but the gap
ranges from 8× to 2349×. The signal that pops out:

- **lru-sim — 8×**: keys come from a 45-entry namespace and are
  reused throughout. Map ops touch already-interned strings; almost
  no String allocation in the hot loop.
- **dep-resolver — 128×**: 10,000 module names *are* reused (built
  once, then read), so the bulk of the run is List<List<Int>>
  traversal and Int math. The `split("-")` for prefix histogram is
  the only hot allocator.
- **markdown-stats — 314×**: 20,000 lines × `"## " + w1 + " " + w2 + " " + w3`
  → ~5 String allocs per line. Hot path is `String + String` and
  `Map<String,Int>` insert.
- **expr-calc — 548×**: 5,000 expressions, each goes through
  String.split → shunting-yard with String stack → postfix String list →
  evaluator. String-heavy at every stage; even tiny per-token allocs
  multiply by ~9 tokens × 5,000 expressions.
- **log-aggregator — 2349×**: 50,000 log lines, each rebuilt via 6
  concats during generation, then split twice during processing.
  Maximum String pressure of the suite.

The takeaway is the **String allocator and Map<String,_> hash path**
are the dominant Osty-side cost. Programs that reuse a small fixed
set of Strings (lru-sim) close the gap to single-digit ratios;
programs that allocate a new String per item (log-aggregator) pay
~2000× per item. Native `String + String` and `Map<String,_>`
inlining are the two perf-backlog items that would compress this
spread the most (MEMORY: `bench_perf_backlog`,
`stdlib_injection_hang`).

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
