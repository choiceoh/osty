# MIR emitter selfhost port

> **Status (2026-04): Historical.** 이 포팅 계획은 제거된 Osty→Go 부트스트랩
> 트랜스파일러(`internal/bootstrap/gen`)를 전제로 작성되었다. 현재 `toolchain/
> mir_generator.osty` 를 Go bridge 로 옮기는 공식 경로는 LLVM 셀프호스팅뿐이다.
> 이 문서는 당시 시점의 기록이며, 아래 "Osty authoring rules" 섹션의 transpile-
> safety 조언은 더 이상 구속력이 없다.

Running plan + section-by-section status for moving
`internal/llvmgen/mir_generator.go` (9,600 LOC hand-written Go) into the
selfhost surface at `toolchain/mir_generator.osty`.

Every hand-written Go edit under `internal/llvmgen/mir_generator.go` is
throwaway effort once the MIR emitter flips to selfhost. This document
is the shared landing target: any perf / correctness change to the MIR
emitter should ship alongside (or instead of) the Osty counterpart
here.

## Pipeline

```
toolchain/mir_generator.osty
      │
      │ internal/selfhost/bundle/mir_generator_bundle.go
      │   (prepends the Go-hosted `llvmStrings` prelude)
      ▼
/tmp/merged.osty
      │
      │ internal/bootstrap/seedgen (the shared transpile entry)
      ▼
internal/llvmgen/mir_generator_snapshot.go
      │
      │ postprocess (rewrite ostyStrings* shims → llvmStrings.*,
      │   rewrite `merged.osty:L:C` comments → `toolchain/mir_generator.osty:L:C`)
      ▼
compile-gated install (`go test -run ^$ ./internal/llvmgen`)
```

Regenerate with `go generate ./internal/llvmgen` (runs both the
existing `support_snapshot` directive and the new
`gen_mir_generator_snapshot.go`). The compile gate rolls the snapshot
back on build failure, so bad Osty never lands. Do **not** hand-edit
`mir_generator_snapshot.go`.

## Section map

Tracks the 15 `// ==== X ====` sections in `mir_generator.go`. Each row
shows the line range, rough function count, approximate size, and the
current port status. "Ported" means the Osty source owns the logic and
the Go call site delegates; "Stub" means the Osty source exposes
helpers but the Go site still has its own body; "Go-only" means
untouched.

| § | Section | Lines | Funcs | Risk | Status |
| - | ------- | ----- | ----- | ---- | ------ |
| 1 | generator state | 100–472 | ~10 | LOW | Partial — `firstNonEmpty`, `formatFnAttrs`, `loopHintsActive`, + loop-metadata constant templates ported (`mirFormatFnAttrs`, `mirLoopHintsActive`, `mirLoopMDVectorizeEnable`/`Scalable`/`Predicate`/`Width`, `mirLoopMDUnrollEnable`/`Count`, `mirLoopMDParallelAccesses`); state-touching (`nextLoopMD` body, `listAliasScopeRef`, `nextAccessGroupMD`) deferred to Phase B |
| 2 | support check | 473–1364 | ~20 | MEDIUM | Go-only |
| 3 | header + runtime declares | 1365–1641 | ~8 | LOW | Go-only |
| 4 | function emission | 1642–2384 | ~18 | MEDIUM | Go-only |
| 5 | GC instrumentation | 2385–2614 | ~8 | LOW | Go-only |
| 6 | instructions | 2615–4873 | ~35 | HIGH | Go-only |
| 7 | list/map/set intrinsics | 4874–6761 | ~50 | MEDIUM | Go-only |
| 8 | concurrency intrinsics | 6762–7472 | ~20 | MEDIUM | Go-only |
| 9 | terminators | 7473–7595 | ~5 | LOW | Go-only |
| 10 | rvalue / operand | 7596–8937 | ~25 | HIGH | Go-only |
| 11 | operators | 8938–9196 | ~12 | LOW | Partial — predicates + `emitBinary` opcode table ported (`isHeapEqualityType`, `isStringPrimType`, `isStringOrderingBinOp`, `stringOrderingPredicate`, `mirBinaryOpcode`, `mirBinaryForcesI1Type`); `emitUnary` / `emitInlineStringEqLiteral` deferred (touch `g.fresh` / `g.fnBuf`) |
| 12 | strings | 9197–9284 | ~7 | LOW | Partial — `encodeLLVMString` / `earliestAfter` ported |
| 13 | type mapping | 9285–9375 | ~8 | LOW | **Ported** (primitive + opaque-named + head-name + optional-surface) |
| 14 | enum layout helpers | 9376–9575 | ~10 | LOW | Partial — `llvmTypeForTupleTag` Prim / Named branches + Optional / Option / Result / Tuple name-mangling ported (`mirTupleTagForPrim`, `mirTupleTagForNamed`, `mirOptionalTypeName`, `mirOptionTypeName`, `mirResultTypeName`, `mirTupleTypeNameFromTags`); `registerEnumLayout` + `g.tupleDefs` caches deferred to Phase B |
| 15 | helpers | 9576–9616 | ~5 | LOW | Partial — `firstNonEmpty`, `isUnitType`, `isFloatType`, `isScalarLLVMType`, `llvmStdIoI1Text` ported |

## Phased plan

**Phase A — pure leaves** (~400 LOC combined). Current phase. Port
functions that have no `g.*` state dependency. Ship one section per PR
with the compile-gate generator enforcing correctness.

- ✅ §13 type mapping — first PR.
- ⏳ §11 operators — pure predicates in (`isStringOrderingBinOp`,
  `stringOrderingPredicate` delegate through `op.String()`); `emit*`
  bodies deferred (need `g.fresh` / `g.fnBuf`).
- ⏳ §12 strings — `encodeLLVMString` / `earliestAfter` in, `stringLiteral`
  / `emitStringPool` deferred (touch `g.strings`).
- ⏳ §15 helpers — pure-side done (`firstNonEmpty`, unit/float/scalar
  predicates, `llvmStdIoI1Text`); state-touching (`fresh`, `freshLabel`,
  `ostyEmitter`, `flushOstyEmitter`, `storeIntrinsicResult`,
  `emitRuntimeRawNull`) deferred to Phase B.
- ⏳ §9 terminators — small (~120 LOC), single `emitTerm` switch. Cross-
  calls into §5 safepoint + §1 metadata — port after Phase B state.

**Phase B — state-bearing helpers**. Once the `MirGen` struct is
mirrored in Osty, port the state-mutating leaves: §15 stateful
helpers (`fresh` / `freshLabel` / `ostyEmitter` / `flushOstyEmitter` /
`storeIntrinsicResult`), §1 metadata allocators (`nextLoopMD`,
`nextAccessGroupMD`, `listAliasScopeRef`), §12 string pool, §14 enum /
tuple / result layout registration, §5 GC instrumentation, §3 header +
runtime declares.

**Phase C — intrinsic dispatchers**. §7 list/map/set, §8 concurrency,
§4 function emission (including the vector-list fast-path pieces from
PRs #809/#812/#814), §2 support-check walkers. Each subsection ports as
an independent PR; fallout is mechanical text emission with well-
understood runtime ABIs.

**Phase D — irreducible core**. §6 instructions + §10 rvalue/operand
— ~3,600 LOC together, dense cross-calls. Ported last, as one or two
PRs so call-site updates stay atomic. Once they land,
`mir_generator.go` is effectively empty and can be deleted; callers of
`GenerateFromMIR` route through the Osty-sourced path.

## Osty authoring rules (transpile-safe)

Collected while porting the first leaves. The bootstrap transpiler
(`internal/bootstrap/gen` via `seedgen`) still has rough edges — this
list keeps new Osty clean of known landmines.

- **String methods** — `.indexOf`, `.hasSuffix`, `.hasPrefix`,
  `.contains` don't lower. Use the imported `llvmStrings.*` functions
  (`Index`, `HasSuffix`, `HasPrefix`, `Contains`) from the bundle
  prelude.
- **`.len()`** on String — fine.
- **String slicing** `s[a..b]` — fine.
- **Range literals in value position** — don't use `let r = 0..n`.
  They lower to `UnitConst` with a warning. Use explicit bounds.
- **Char iteration** — no `rune` type; iterate via
  `for i < s.len() { let ch = s[i..(i+1)]; ... }`.
- **Hex / arbitrary byte formatting** — the transpiler chokes on the
  `fmt.Fprintf(&b, "\\%02X", c)` shape. Split hot paths into
  printable-only Osty with a Go fallback for non-ASCII (see
  `llvmCStringEscape` in `gen_support_snapshot.go:79-97` for the
  canonical hand-patch).
- **Variadic args** — Osty has none. Port `f(xs ...T)` as
  `f(xs: List<T>)` and slicify at the Go boundary.
- **Match on strings** — don't. Use `if / else if` chains.
- **Compound assignment** — `x += 1` lowers to an overflow-checked
  IIFE. Tolerable, but prefer explicit `x = x + 1` for hot code.
- **`panic(...)`** — the transpiler doesn't recognise it as a
  builtin. Trip a panic via an intentionally out-of-range slice
  (`s[0..s.len() + 1]`) if you need a hard-fail path.
- **String interpolation braces** — `{` and `}` inside a `"..."`
  literal need `\{` / `\}` escapes.
- **`=>` in doc comments** — the selfhost lexer's fat-arrow diag
  sweep scans raw bytes and flags `=>` everywhere, including
  comments and string literals. Split or rephrase to avoid it.

## Function inventory (ported so far)

`toolchain/mir_generator.osty`:

| Function | Signature | Origin §  | Notes |
| -------- | --------- | --------- | ----- |
| `mirLlvmTypeForPrim` | `(name: String) -> String` | §13 | `Int`/`Bool`/`Float`/… → LLVM scalar string |
| `mirLlvmTypeForOpaqueNamed` | `(name: String) -> String` | §13 | `List`/`Map`/`Set`/`Channel`/… → `ptr` |
| `mirLlvmTypeHeadName` | `(typeText: String) -> String` | §13 | strip `pkg.` + `<...>` |
| `mirLlvmTypeIsOptionalSurface` | `(typeText: String) -> Bool` | §13 | trailing-`?` + bracket-depth guard |
| `mirIsHeapEqualityType` | `(typeText: String) -> Bool` | §11 | String/Bytes route to runtime equal |
| `mirIsStringPrimTypeText` | `(typeText: String) -> Bool` | §11 | String-only ordering routing |
| `mirIsStringOrderingSymbol` | `(symbol: String) -> Bool` | §11 | `<` / `<=` / `>` / `>=` symbol gate |
| `mirStringOrderingPredicate` | `(symbol: String) -> String` | §11 | symbol → `slt` / `sle` / `sgt` / `sge` |
| `mirIsUnitTypeText` | `(typeText: String) -> Bool` | §15 | Unit / `()` / Never |
| `mirIsFloatTypeText` | `(typeText: String) -> Bool` | §15 | spec surface + LLVM text forms |
| `mirIsScalarLLVMType` | `(t: String) -> Bool` | §15 | single-register LLVM scalar gate |
| `mirLlvmI1Text` | `(v: Bool) -> String` | §15 | `true`/`false` i1 literal shim |
| `mirFirstNonEmpty` | `(vals: List<String>) -> String` | §1 | variadic-erased first-non-empty |
| `mirEarliestAfter` | `(input: String, needle: String) -> Int` | §12 | wrapper around `llvmStrings.Index` |
| `mirEncodeLLVMString` | `(s: String) -> String` | §12 | printable-ASCII LLVM literal escaper |
| `mirTupleTagForPrim` | `(name: String) -> String` | §14 | Prim branch of `llvmTypeForTupleTag` — `"Float64"` → `"f64"`, `"String"` → `"string"`, `"()"` → `"unit"`; returns `""` for unknown kinds so caller falls through to `"opaque"` |
| `mirTupleTagForNamed` | `(name: String, builtin: Bool) -> String` | §14 | NamedType branch — collapses builtin collection handles + concurrency runtime types to `"ptr"`, otherwise preserves the declared name |
| `mirOptionalTypeName` | `(innerTag: String) -> String` | §14 | `"Option." + innerTag` — surface-`T?` mangled name |
| `mirOptionTypeName` | `(innerTag: String) -> String` | §14 | Named `Option<T>` — `""` tag means bare `"Option"`, otherwise mirrors `mirOptionalTypeName` |
| `mirResultTypeName` | `(okTag: String, errTag: String) -> String` | §14 | `Result` / `Result.<Ok>` / `Result.<Ok>.<Err>` — empty tags drop trailing components |
| `mirTupleTypeNameFromTags` | `(tags: List<String>) -> String` | §14 | `"Tuple." + tags.join(".")` — matches the Go source byte-for-byte including the `"Tuple."` trailing-dot on an empty tuple |
| `mirBinaryOpcode` | `(symbol: String, isFloat: Bool) -> String` | §11 | 19-branch opcode + icmp/fcmp predicate table keyed on `BinaryOp.String()`; returns `""` on miss so caller stays on the unsupported diagnostic |
| `mirBinaryForcesI1Type` | `(symbol: String) -> Bool` | §11 | `&&` / `\|\|` force operand type to `"i1"` instead of argLLVM — complement to `mirBinaryOpcode` |
| `mirLoopHintsActive` | `(vectorizeHint: Bool, parallelHint: Bool, unrollHint: Bool) -> Bool` | §1 | OR of the v0.6 loop annotation flags |
| `mirLoopMDVectorizeEnable` | `() -> String` | §1 | `!{!"llvm.loop.vectorize.enable", i1 true}` body — A5 opt-in literal |
| `mirLoopMDVectorizeScalable` | `() -> String` | §1 | `!{!"llvm.loop.vectorize.scalable.enable", i1 true}` body — A5.1 SVE toggle |
| `mirLoopMDVectorizePredicate` | `() -> String` | §1 | `!{!"llvm.loop.vectorize.predicate.enable", i1 true}` body — A5.1 tail-folding toggle |
| `mirLoopMDUnrollEnable` | `() -> String` | §1 | `!{!"llvm.loop.unroll.enable", i1 true}` body — bare `#[unroll]` |
| `mirLoopMDVectorizeWidth` | `(widthDigits: String) -> String` | §1 | `!{!"llvm.loop.vectorize.width", i32 <digits>}` body — caller pre-formats the Int |
| `mirLoopMDUnrollCount` | `(countDigits: String) -> String` | §1 | `!{!"llvm.loop.unroll.count", i32 <digits>}` body — caller pre-formats the Int |
| `mirLoopMDParallelAccesses` | `(accessGroupRef: String) -> String` | §1 | `!{!"llvm.loop.parallel_accesses", <ref>}` body — A6 group reference |
| `mirFormatFnAttrs` | `(inlineMode: Int, hot: Bool, cold: Bool, pureFn: Bool, targetFeatures: List<String>) -> String` | §1 | Space-joined v0.6 A8/A9/A10/A13 fn-attr string — `inlinehint`/`alwaysinline`/`noinline` + `hot`/`cold`/`readnone` + `"target-features"="+f1,+f2"` |

Keep this table updated as each section lands. New entries go in
insertion order so the provenance columns (`Origin §`) stay useful as
the file grows.
