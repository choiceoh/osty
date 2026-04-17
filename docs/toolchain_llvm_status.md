# Toolchain × LLVM compilability — status report

Snapshot date: 2026-04-18, against branch tip `5ace7c3` (Stage 3.11 landed).

The question this report answers: **can we run `toolchain/*.osty` through the
LLVM backend today, and if not, what specifically blocks it?** The runbook is
short because the answer is "no" — but the blockers are concrete and fall
into three separate layers, each independently fixable.

## TL;DR

Today zero `toolchain/*.osty` files reach `internal/llvmgen`. The pipeline
stops on one of:

| Layer | Where | What blocks |
|---|---|---|
| CLI wiring | `internal/check/host_boundary.go:578` | `osty gen --backend=llvm <any>.osty` segfaults (nil pointer in `selfhostSpanIndex.addNode`) before the backend is reached |
| Front-end (lexer) | `internal/lexer` string-interpolation path | `"0_{label}"` / `"[a-z0-9_-]*"` inside string literals are flagged E0008 |
| Front-end (resolver) | `internal/resolve` / `internal/stdlib` | `std.strings` module not in scope — 39 × E0500 in `toolchain/ast_lower.osty` alone |
| Front-end (parser) | `internal/parser` | `fn …(def: Expr)` inside a `use "…" { … }` FFI block fails with E0001 |
| Type checker | `internal/check` | 1700 aggregated errors across the package (but 97.6% of assignment/return/call checks still accept — tail is small per-file) |

The MIR-direct emitter itself (Stages 3.1–3.11) covers most of the language
shapes toolchain uses. What is blocking is *front-end bookkeeping and CLI
plumbing*, not backend lowering.

## How the probe was run

```
go build -o /tmp/osty ./cmd/osty
/tmp/osty check --airepair=false toolchain > /tmp/tc.log 2>&1
/tmp/osty gen   --backend=llvm   toolchain/core.osty  2>&1
/tmp/osty gen   --backend=llvm   /tmp/hello.osty      2>&1
/tmp/osty build --backend=llvm   examples/calc        2>&1
```

`/tmp/hello.osty` was a 3-line `fn main() { println(42) }` baseline so the
panic could be isolated from toolchain-specific issues.

## Layer 1 — CLI panic blocks any `osty gen --backend=llvm` call

Input: a single-line `fn main() { println(42) }`.

```
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: ...]
  check.(*selfhostSpanIndex).addNode
    internal/check/host_boundary.go:578
  check.(*selfhostSpanIndex).addNode
    internal/check/host_boundary.go:501
  check.(*selfhostSpanIndex).addNode
    internal/check/host_boundary.go:530
  check.buildSelfhostSpanIndex
    internal/check/host_boundary.go:433
  check.overlaySelfhostResult
    internal/check/host_boundary.go:360
  check.applySelfhostFileResult
    internal/check/host_boundary.go:248
  check.File
    internal/check/check.go:108
```

Same root cause as the four pre-existing `internal/llvmgen` test failures:

- `TestGenerateModuleInterfaceVtableEmitted`
- `TestGenerateSafepointKeepsImmutableManagedLocalsAndAggregateFields`
- `TestGenerateManagedAggregateListsTraceNestedRoots`
- `TestGenerateModulePtrBackedListToSetAndBoolPrint`

These all route through `runMonoLowerPipeline → check.File → …addNode` and
trip the same nil deref. The Go-side `TestLLVMBackendBinaryRunsBundledRuntime`
path uses a different entry and passes cleanly, which is why most LLVM
backend coverage still looks green — the pipeline that the CLI uses is
broken, the pipeline the Go test harness uses is fine.

This is the single highest-ROI fix on the path to `toolchain/*.osty` through
LLVM, because every other issue below only matters once this one is gone.

## Layer 2 — front-end error budget in `toolchain/`

Aggregated from `osty check --airepair=false toolchain`:

| Code | Count | Meaning | Dominant site |
|---|---|---|---|
| E0500 | 39 | undefined name — `strings` module not in scope | `toolchain/ast_lower.osty` (all 39) |
| E0008 | 6 | numeric separator `_` must appear between two digits — false-positive inside string literals | `lsp.osty:415-423`, `ci.osty:546`, `manifest_validation.osty:283` |
| E0001 | 4 | expected IDENT — parser loses sync at `fn ParamNode(…, def: Expr, …)` | `ast_lower.osty:91`, `:93` (× 2 each) |
| E0010 | 2 | cascading from E0001 | `ast_lower.osty:1051`, `:1054` |
| E0700 | 1 | native checker summary — 1700 errors aggregated | `airepair_flags.osty:1` (summary anchor) |

Error-bearing files: `ast_lower.osty` (46), `lsp.osty` (4),
`manifest_validation.osty` (1), `ci.osty` (1), `airepair_flags.osty` (1).
Every other `toolchain/*.osty` is free of parser / lexer / resolver errors
at the per-file level; the remaining pressure comes from the checker's
aggregate 1700-error summary, which is pulled forward into every `osty
check` / `osty gen` run.

### E0008 — string-interpolation lexer leak

```osty
return "0_{label}"                                 //  lsp.osty:415
return "1_{label}"                                 //  lsp.osty:418
"…expected [a-z][a-z0-9_-]*"                       //  ci.osty:546
"…[A-Za-z_][A-Za-z0-9_-]*)"                        //  manifest_validation.osty:283
```

All six sites share the same shape: a literal digit followed by `_`
followed by something non-digit, *inside* a string literal (with or without
interpolation). The numeric-literal validator is being invoked on string
content.

Minimal reproducer candidate: add a positive spec-corpus fixture containing
`let s = "0_{x}"; let t = "0_-foo"` and expect the checker to emit zero
diagnostics. The fix lives in the lexer's string-literal scanner; the
symptom is that the number-separator rule is reached from inside the string
state instead of staying in the number state.

### E0500 — `std.strings` not in scope

39 call sites in `toolchain/ast_lower.osty` reference `strings.HasPrefix`,
`strings.TrimPrefix`, `strings.TrimSuffix`, `strings.Split`, `strings.Count`
(and similar). The checker's hint (`did you mean stringAt?`) shows that
something named `stringAt` is reachable, but no module bound to `strings`
is registered in the prelude / stdlib for this package.

Two equally-valid fixes:

1. Port `std.strings` to Osty and register it in the prelude — matches how
   Stage 3.9's iter/cmp/csv work landed. This is the expected long-term
   resolution.
2. Rewrite `ast_lower.osty`'s call sites to use the Osty-native string
   primitives (`stringAt`, char-index loops, etc). Faster, but doesn't
   help any of the other files that will eventually want `std.strings`.

### E0001 — parser trips on `def: Expr` parameter

```
fn FieldNode(pos: Pos, end: Pos, isPub: Bool, name: String, typ: Type, def: Expr, …)
fn ParamNode(pos: Pos, end: Pos, name: String, pat: Pattern, typ: Type, def: Expr)
```

Both signatures live inside a `use runtime.golegacy.astbridge as astbridge {
... }` FFI block (lines 1 – 254 of `ast_lower.osty`). The parser reports
"expected IDENT, got fn" at the column of `def`, which suggests `def` is
being handled as a reserved keyword in this position even though the
grammar spec (`OSTY_GRAMMAR_v0.4.md`) doesn't list it. Worth double-checking
the FFI-block param parser specifically — the regular fn-decl param parser
accepts `def` fine (see `toolchain/ir.osty` and `ast_lower.osty:1043` which
use `let mut def = …`).

### E0700 — 1700 checker errors, 97.6% accept rate

```
native checker reported type errors: 1700 error(s)
note: native checker accepted 26191 of 26850 assignment/return/call checks
```

Most likely a cascade from the E0500 / E0001 failures above: once a type
can't be resolved (e.g. a `strings.Split` call site), every transitively
dependent `Arg`/`Return` site also fails its type constraint. Clearing
Layer 1 + 2 should knock this number down by an order of magnitude before
any individual-site chasing is worthwhile.

## Layer 3 — MIR-direct backend coverage (for context)

When front-end errors are out of the way, the backend is close to ready.
`docs/mir_design.md` Stage 3.1–3.11 already landed coverage for:

- primitives, struct aggregates, tuples, named enums
- `Option` / `Result` / `Maybe` + payload match
- `List<T>` / `Map<K,V>` / `Set<T>` — scalar elements via typed runtime
  symbols, composite list elements via `bytes-v1` ABI (`IndexProj` included)
- non-capturing **and** capturing closures via uniform env ABI + indirect
  call
- top-level globals via module-ctor init (`Options.EmitGC`-independent)
- concurrency intrinsics mapped to declared-but-pending runtime symbols
- GC roots + entry/back-edge safepoints for managed locals when
  `Options.EmitGC` is set (String / Bytes / Channel / Handle / Group /
  TaskGroup / Select / Duration / ClosureEnv included)

Still outstanding (Stage 5 parity gate):

- composite **map** element types (parallels the Stage 3.6 `bytes-v1` list path)
- heap-escaping closure envs (Stage 3.8 ships stack-only)
- `DerefProj` on non-env places
- concurrency *runtime symbols* themselves (emitter emits declare lines;
  the shared object is not shipped yet, so links will fail at final link
  time even if IR generates)

None of these are tripped during `osty check` or `osty gen` IR emission —
they only matter at link time or for features `toolchain/*.osty` doesn't
heavily use. The work budget to reach "`toolchain/*.osty` links into a
runnable binary" is dominated by Layer 1 + Layer 2 above, not Layer 3.

## Recommended fix order (smallest → largest unlock)

1. **Fix the `selfhostSpanIndex.addNode` nil panic** in
   `internal/check/host_boundary.go`. Unblocks `osty gen --backend=llvm`
   *and* four pre-existing test failures. Likely a missing nil guard on a
   span field that is populated for AST-native files but empty for
   selfhost-overlay files.

2. **Fix the string-interpolation lexer bug** (E0008). Six sites today,
   pattern is well-defined, fixture is small. Unblocks `lsp.osty`,
   `ci.osty`, `manifest_validation.osty`.

3. **Decide `std.strings` policy.** Either port it (39 call sites in
   one file say the demand is real) or rewrite `ast_lower.osty` against
   existing primitives. Unblocks `ast_lower.osty`.

4. **Investigate parser sensitivity to `def`** inside FFI `use` blocks.
   One call site, low effort, cleans up E0001/E0010.

5. **Re-run `osty check toolchain`** — expect the 1700 native-checker
   summary to collapse with most cascades gone. Remaining tail becomes a
   focused second pass.

6. **Try `osty gen --backend=llvm toolchain/<file>.osty`** per-file.
   Stage 3.1–3.11 should cover the language shapes toolchain uses;
   anything that trips `mir-mvp` unsupported is a new, narrower wedge for
   the MIR roadmap.

Each step is independently reviewable. Step 1 alone probably collapses
the pre-existing test-failure list and opens the CLI path for toolchain
experiments.

## Reproducing this report

```bash
go build -o /tmp/osty ./cmd/osty
/tmp/osty check --airepair=false toolchain > /tmp/tc.log 2>&1

grep -oE "E[0-9]{4}"          /tmp/tc.log | sort | uniq -c | sort -rn
grep -oE "toolchain/[a-z_]+\.osty" /tmp/tc.log | sort | uniq -c | sort -rn

# CLI panic repro
printf 'fn main() { println(42) }\n' > /tmp/hello.osty
/tmp/osty gen --backend=llvm /tmp/hello.osty   # panics today
```
