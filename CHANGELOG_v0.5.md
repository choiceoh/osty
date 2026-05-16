# CHANGELOG — v0.5

- **Scope**: v0.5 edition changes — new surface, wired features, regen status
- **Type**: Changelog
  Tracks the user-visible slice of the v0.5 spec (`LANG_SPEC_v0.5/`) that
  has landed in the compiler today, separate from the spec itself. The
  spec is the authority on what v0.5 _will_ be; this file is the
  authority on what v0.5 _is_ right now.

See [`LANG_SPEC_v0.5/18-change-history.md`](./LANG_SPEC_v0.5/18-change-history.md)
for the full v0.4 → v0.5 decision log (15 resolved gaps, G20 – G35) and
[`SPEC_GAPS.md`](./SPEC_GAPS.md) for per-gap rationale.

## Shipped in the compiler

### Syntax

| Form                                                    | Status            | Notes                                                                                                                                                                                                                                                                                                                                                                             |
| ------------------------------------------------------- | ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `use path::{a, b as c}` — scoped / grouped imports      | **shipped** (G28) | Parsed natively by the self-hosted parser ([`toolchain/parser.osty`](./toolchain/parser.osty)) and lowered through [`internal/selfhost/ast_lower.osty`](./internal/selfhost/ast_lower.osty). One flat `use` per item, rename preserved. The earlier Go-side pre-parse rewrite (`internal/parser/scoped_imports.go`) was retired in af371d1 once the self-hosted parser caught up. |
| `pub use path.Sym` — cross-module re-export             | **shipped** (G30) | Parsed natively in [`toolchain/parser.osty`](./toolchain/parser.osty) with the `pub` visibility carried through `ast_lower.osty`; resolver honors the flag and cycles diagnose as `E0552`. The earlier Go-side post-parse `IsPub` flip (`internal/parser/pub_use.go`) was retired in 15cd35a.                                                                                     |
| `#[cfg(key = "value")]` — conditional compilation       | **shipped** (G29) | Pre-resolve filter in [`internal/resolve/cfg.go`](./internal/resolve/cfg.go). Keys: `os`, `target`, `arch`, `feature`. Unknown key → `E0405`. Composition forms `all` / `any` / `not` with nested support shipped in PR #1716 (2026-05-12). 8 focused tests.                                                                                                                      |
| `#[test]` inline test annotation                        | **shipped** (G32) | Discovery in [`cmd/osty/test_native.go`](./cmd/osty/test_native.go) accepts any zero-arity function carrying `#[test]`, including those outside `_test.osty`. Legacy `test*` prefix still works.                                                                                                                                                                                  |
| Doctest blocks in `///` comments                        | **shipped** (G32) | Extraction in [`internal/doctest`](./internal/doctest). `osty test --doc` synthesises a runner per package and routes blocks through the normal test pipeline.                                                                                                                                                                                                                    |
| `loop { break value }` — value-returning unbounded loop | **shipped** (G22) | Parser lowers to `AstNFor` with `text="loopexpr"`; `elabInferLoop` in `elab.osty` collects `break value` types and types the loop expression. Resolver validates `break`/`continue` scope.                                                                                                                                                                                        |
| Trailing closure `f(x) \|y\| { body }`                  | **shipped** (G23) | `opAttachTrailingClosure` appends closure to CallExpr args; checker treats it as an ordinary positional arg.                                                                                                                                                                                                                                                                      |
| Labeled `break 'label` / `continue 'label`              | **shipped** (G24) | `FrontLabel` token, `opParseLabelNode`, resolver validates labels (`E0763`/`E0764`). Elab threads labels through Core IR.                                                                                                                                                                                                                                                         |
| Range step `0..100 by 2`                                | **shipped** (G25) | `rangeFlags` bit 1 marks `by` presence; step in `AstNRange.children`; `elabInferRange` checks step type.                                                                                                                                                                                                                                                                          |
| Struct update shorthand `x { field: v }`                | **shipped** (G26) | `opParseStructUpdateShorthand` desugars to spread; `elabInferStructLit` infers owner from receiver type.                                                                                                                                                                                                                                                                          |
| `err as? T` downcast                                    | **shipped** (G27) | `FrontAsQuestion` token → desugars to `.downcast::<T>()` call with `flags=1`. Checker enforces Error-bound rules (`E0757`). LLVM lowering in [`internal/llvmgen/iface_downcast.go`](./internal/llvmgen/iface_downcast.go).                                                                                                                                                        |

### Stdlib — signatures

These modules publish v0.5 signatures so user code that imports and
references them type-checks under the front-end checker. Runtime
behavior for the new surface is still the G18 stub convention —
bodies exist so the stub checker accepts imports.

- **`std.option`** — combinators: `isSome / isNone / isSomeAnd / isNoneOr / contains / take / replace / unwrap / expect / unwrapOr / unwrapOrElse / and / andThen / or / orElse / xor / filter / inspect / map / mapOr / mapOrElse / zip / okOr / okOrElse`.
- **`std.result`** — combinators: `isOk / isErr / isOkAnd / isErrAnd / contains / containsErr / unwrap / expect / unwrapErr / expectErr / unwrapOr / unwrapOrElse / ok / err / and / andThen / or / orElse / inspect / inspectErr / map / mapErr / mapOr / mapOrElse`.
- **`std.collections`** — `List.{chunked, windowed, partition, reduce, scan, flatMap, zip3}` in addition to the existing v0.4 surface. `Map.{getOrInsert, getOrInsertWith, merge, mapValues, filter}`.
- **`std.strings`** — full Unicode-aware chapter. Everything referenced by name in `LANG_SPEC_v0.5/10-standard-library` is now a real `.osty` signature.
- **`std.error`** — `Error.wrap(context)` / `Error.chain()` default methods, `WrappedError` struct, free functions `wrap()` / `rootCause()`.
- **`std.testing`** — `Gen<T>` type + `property / propertyN / propertySeeded` runners.
- **`std.testing.gen`** (new submodule) — 17 generator constructors: primitives (`int`, `intRange`, `bool`, `float`, `char`, `byte`, `asciiString`) and combinators (`oneOf`, `oneOfGens`, `map`, `filter`, `pair`, `triple`, `list`, `listOfSize`, `option`, `result`, `constant`).

### Tooling

- **`osty test --doc`** — extract and run doctest blocks as additional
  test cases. One synthesised `__osty_doctest_runner__.osty` per
  package; `fn test_doc_<owner>_<n>()` per block. Failures report
  which owner + ordinal broke.
- **Inline `#[test]`** discovery rejects `#[test]` on parameterised
  functions (silently skipped) and on a function literally named
  `testing`.
- **`osty test --bench`** (spec §11.4) — discovers `bench*`-prefixed,
  zero-arity functions and runs each through the LLVM backend. Bodies
  call `testing.benchmark(N, || { ...; Ok(()) })`, which the emitter
  lowers to a range loop bracketed by `osty_rt_bench_now_nanos()`
  samples. Each call prints two lines —
  `bench <abs-path>:<line> iter=N total=Tns avg=Ans` followed by
  `  min=...ns p50=...ns p99=...ns max=...ns` — and the CLI always surfaces
  them. Distribution stats come from per-iteration sampling; a
  compiler-inserted warmup of `clamp(N/10, 1, 1000)` iterations runs
  before the first clock sample. `?` inside the closure is supported
  and, on `Err` / `None`, prints
  `bench ?` propagated failure at <abs-path>:<line>`and exits the
bench with failure status.`--benchtime <dur>`(Go-style duration,
requires`--bench`) activates auto-tuning: a 10-iteration probe
estimates the iteration count that fills the duration, clamped to
`[10, 100_000_000]`. `--bench`and`--doc`are mutually exclusive
(exit 2). In default test mode`bench\*` functions are skipped so an
  errant benchmark never runs as a regular test.
- **`testing.snapshot(name, output)`** — golden-file testing. LLVM
  lowers the call to `osty_rt_test_snapshot` which resolves
  `<source_dir>/__snapshots__/<sanitize(name)>.snap` and either
  creates the file (first run), passes silently (match), or prints a
  line-level diff and exits 1 (mismatch). Run
  `osty test --update-snapshots` to accept current output and
  overwrite every golden in the run; set `OSTY_SNAPSHOT_DIR=<path>`
  to redirect writes out of the repo (used by the runtime's own
  test harness). Implementation:
  [`internal/backend/runtime/osty_runtime.c`](./internal/backend/runtime/osty_runtime.c)
  (runtime helper + diff),
  [`internal/llvmgen/stmt.go`](./internal/llvmgen/stmt.go) (call
  intercept), and [`cmd/osty/test_native.go`](./cmd/osty/test_native.go)
  (CLI flag).
- **`env.args()` end-to-end through the LLVM backend** — `use std.env`
  - `let args = env.args()` now compiles to a real argv lookup instead
    of the LLVM015 "call target \*ast.FieldExpr (env.args)" wall.
    [`internal/llvmgen/stdlib_env_shim.go`](./internal/llvmgen/stdlib_env_shim.go)
    routes the call to `osty_rt_env_args`, and
    [`internal/llvmgen/decl.go`](./internal/llvmgen/decl.go) widens `main`
    to `(i32 argc, ptr argv)` with an `osty_rt_env_args_init` prologue
    whenever the package imports `std.env`. Each `env.args()` invocation
    returns a fresh GC-managed `List<String>` (copies of process argv, so
    the result is safe to mutate). Packages that don't import `std.env`
    keep the bare `define i32 @main()` signature.
- **Structural diff on `testing.assertEq`** — on failure, the
  emitted message now includes a line-level diff (`- left` / `+ right`
  with up to 3 context lines) whenever both operands share a
  diffable shape. Shipped coverage: two `String` values (direct
  diff) and two `List<T>` values with the same primitive `T` in
  `{Int, Float, Bool, String}` (rendered to a multi-line literal
  first, then diffed). Runtime helpers: `osty_rt_strings_DiffLines`,
  `osty_rt_list_primitive_to_string`. Structs, enums, Maps, and
  `List<composite>` still fall back to source-text rendering — they
  need `ToString` protocol dispatch and are tracked separately.

### Vectorize default-on (A5.2 carryover)

The full optimization annotation set — `#[vectorize]`, `#[no_vectorize]`,
`#[parallel]`, `#[unroll]`, `#[inline]`, `#[hot]`, `#[cold]`,
`#[target_feature(...)]`, `#[noalias]`, `#[pure]` — is shipped on the
LLVM backend. **A5.2 flip**: `vectorize` is the default for every
function (`!llvm.loop.vectorize.enable, i1 true` metadata + per-iteration
GC safepoint poll skip) without the user typing anything.
`#[no_vectorize]` opts out, `#[vectorize(scalable, predicate, width = N)]`
refines strategy. Backend implementation:
[`internal/llvmgen/`](./internal/llvmgen/) (vectorize*\*, parallel*\_,
unroll\__, inline*\*, hot_cold*_, target*feature*\_, noalias*\*, pure*\*
files). Spec: [`LANG_SPEC_v0.5/03-declarations.md`](./LANG_SPEC_v0.5/03-declarations.md)
§3.8.3–§3.8.12. Soundness gate: `runPureGate` (E0775) at
[`toolchain/check_gates.osty`](./toolchain/check_gates.osty).

### Diagnostic codes

| Code                      | Meaning                                                                     | Where                        |
| ------------------------- | --------------------------------------------------------------------------- | ---------------------------- |
| `E0405`                   | Unknown `#[cfg]` key                                                        | `internal/resolve/cfg.go`    |
| `E0552`                   | `pub use` cycle                                                             | resolver re-export walk      |
| `E0553`                   | `pub use` of a private symbol                                               | resolver                     |
| `E0554`                   | Duplicate item in scoped `use path:{...}`                                   | resolver                     |
| `E0754`–`E0756`           | `#[op(...)]` signature / duplicate / not-allowed (G35)                      | `toolchain/check_gates.osty` |
| `E0757`                   | `as?` on a non-`Error` expression (G27)                                     | `toolchain/elab.osty`        |
| `E0758` / `E0759`         | Enum integer discriminant on payload variant / duplicate discriminant (G31) | `toolchain/check_gates.osty` |
| `E0762` / `E0766`–`E0768` | `const fn` default-literal / disallowed construct / cycle / generic (G21)   | `toolchain/check_gates.osty` |
| `E0763` / `E0764`         | Unknown loop label / label shadow (G24)                                     | `toolchain/resolve.osty`     |
| `E0765`                   | Implicit narrowing conversion (numeric) (G34)                               | `toolchain/check_env.osty`   |

All codes appear in [`ERROR_CODES.md`](./ERROR_CODES.md) (generated) and
have focused tests under `internal/diag/testdata`.

### Numeric widening (G34, `check_env.osty`)

Lossless numeric widening checker surfaced in the same commit that
introduced `checkIsAssignable`. `checkIsWideningConversion` defines the
widening lattice (`Int8 → Int16 → Int32 → Int → Float64`, `Float32 → Float64`).
`checkIsNumericNarrowing` detects narrowing at every `checkExpectAssignable`
site and emits `E0765`. The `diagImplicitNarrowing` helper and test code are
registered; no focused test fixture exercises it yet.

## Native checker status

The stub stdlib and the v0.5 syntax rewrites above run end-to-end
through parse + resolve. The native checker (`OSTY_NATIVE_CHECKER_BIN`)
does **not** yet know about the newly published method signatures on
`List` / `Map` / `Option` / `Result` / `Error`, so front-end checking
in isolation may flag `E0703` (no method) on the new surface even
though the stdlib signatures are present. This is a checker-side gap
tracked alongside the bootstrap regen work.

## Migration from v0.4

v0.5 is additive by design — every v0.4 program compiles unchanged
under v0.5 grammar. All v0.5 surface forms (G20–G35) are now
**shipped** in the compiler front-end; the remaining work is native
LLVM coverage and self-hosting gate closure.

The project edition is now `edition = "0.5"` in newly scaffolded
`osty.toml` files. Manifest validation keeps accepting historical
`0.3` and `0.4` projects so existing workspaces can migrate
incrementally.

## Implementation history

Entries touching the shipped rows above, newest first. Hashes are
short; `git log <hash>` for the full message.

- **LLVM self-host walking skeleton (2026-05-16 ~ 2026-05-17)** — 25 PR
  chain ([#1812](https://github.com/choiceoh/osty/pull/1812) ~
  [#1851](https://github.com/choiceoh/osty/pull/1851)) 가
  `cmd/osty-native-checker/` 를 dual-target (Go shell + LLVM-built Osty
  entry) 로 만들고 plan §12 의 M1/M2 partial 달성. 자세한 trajectory
  는 [docs/llvm-selfhost-plan.md](./docs/llvm-selfhost-plan.md) +
  `docs/llvm-selfhost-plan-pr*.md` 시리즈.
  - **M1 walking skeleton** ([#1816](https://github.com/choiceoh/osty/pull/1816)):
    `cmd/osty-native-checker/osty.toml` + `main.osty` 신규.
    `OSTY_STAGE0_FALLBACK=1 osty build --backend llvm cmd/osty-native-checker/`
    가 LLVM binary 산출 + 실행.
  - **PR1c — 진짜 stdin** ([#1826](https://github.com/choiceoh/osty/pull/1826)):
    `internal/mir/lower.go::qualifiedSymbol` 에 `rewriteStdlibSymbolToRuntime`
    helper 추가, `std.io.readLine` → `osty_rt_io_read_line` MIR symbol
    rewrite. stage0 fallback + production path (lir_proto) 양쪽 일관.
    backend wall 의 가장 큰 부분 (per-symbol stage0 cover) 우회.
  - **PR2 — manual naive parser** ([#1829](https://github.com/choiceoh/osty/pull/1829)):
    `std.json.*` 호출의 stdlib body injection wall 우회. primitive
    `strings.indexOf` + `strings.slice` 만 사용한 source field 추출.
    backend 의존 0.
  - **M2 partial byte parity**: empty-source fixture (`{"source":""}`)
    에 대해 Go-built `osty-native-checker` 와 LLVM-built 가 byte-identical
    `CheckResult` JSON 출력. multi-fixture parity 는 PR3 의 cross-package
    method-call wall 후.
  - **PR3-C-impl-go step 1+2** ([#1842](https://github.com/choiceoh/osty/pull/1842)):
    `api/types.go::ResolvedSymbol.ImportPath` 필드 + `resolve_adapter.go::
    selfhostUseAliasImportPaths` walker + post-processing pass. `kind ==
    "package"` symbol 이 use-decl 의 import path 보유.
  - **Spec gap + design docs**: `SPEC_GAPS.md::cross-pkg-module-resolution`
    open gap 신규 등록 ([#1832](https://github.com/choiceoh/osty/pull/1832))
    + cross-package method-call dispatch 의 4 옵션 분석
    ([#1845](https://github.com/choiceoh/osty/pull/1845)) + 우회 use form
    3 종 측정 ([#1847](https://github.com/choiceoh/osty/pull/1847)) + 옵션
    c (spec narrow exception) 의 CLAUDE.md 변경 draft
    ([#1848](https://github.com/choiceoh/osty/pull/1848)). 다음 unlock 은
    사용자/팀 합의 동반.

- **Phase 2 scheduler + compiler-speed wave (2026-04-22)** — three paths merged under a "real-world speedup" umbrella:
  - **Runtime scheduler (Phase 2)**: thread-per-task pthread model replaced by worker pool + per-worker Chase-Lev work-stealing deque + linked-list FIFO inject queue + on-demand detached elastic worker for blocking saturation. `OSTY_SCHED_WORKERS` env override. ThreadSanitizer-clean (slot publication is release/acquire). Observed 3.97x speedup (16 CPU-bound tasks, workers=1→4). Full details in [RUNTIME_SCHEDULER.md](./RUNTIME_SCHEDULER.md). ABI unchanged so MIR/backend don't recompile. As a side effect, `TestBundledRuntimeSchedulerRace` / `TestBundledRuntimeSchedulerCollectAll` (main's "list trace-kind mismatch" issue) are fixed.
  - **Parallel `check.Workspace()`**: per-package native-checker calls run on a GOMAXPROCS worker pool with a mutex only around the shared type-map overlay. 3.36x speedup on 8 synthesized packages, 2.55x on 32 ([`internal/check/workspace_parallel_test.go`](./internal/check/workspace_parallel_test.go) bench + equivalence test). `OSTY_CHECK_PARALLEL=0` disables for debugging ordering bugs.
  - **On-disk checker cache**: the existing `cachedEmbeddedChecker` is generalized to `cachedNativeChecker` and wrapped around whichever backend `defaultNativeChecker` returns (managed subprocess or embedded fallback). `cmd/osty` activates the cache in `build` / `run` / `check` commands; entries land at `.osty/cache/checker/<validity>/pkg-<hash>.json` with validity keyed by tool version + managed checker stamp. Incremental re-runs with unchanged package inputs short-circuit the multi-second checker round-trip to a JSON read. `OSTY_CHECKER_CACHE=0` disables.

- **014a6fe** — `err.downcast::<T>()` LLVM lowering (backend half of G27 / §7.4)
- **607eb19** — `use path.X as Y` stable-alias rewrite migrates from Go side to the self-hosted parser
- **af371d1** — `use path:{ ... }` scoped-use handling migrates to the self-hosted parser; `internal/parser/scoped_imports.go` retired
- **15cd35a** — `pub use` handling migrates to the self-hosted parser; `internal/parser/pub_use.go` retired
- **f38be21** — Bootstrap-gen regen pipeline grows a build gate + checker-miss fallback (narrowed #362)
- **6fd09bb / 23b3f26 / f69461d** — AST-native package checker bridge + prelude-function registration + package-native external checker requests (enabled the Osty-native checker gates for G21/G31/G35 and ELAB for G22–G27)
