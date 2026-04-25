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
| 1 | generator state | 100–472 | ~10 | LOW | Partial — pure templates (`mirFormatFnAttrs`, `mirLoopHintsActive`, `mirLoopMDVectorizeEnable`/`Scalable`/`Predicate`/`Width`, `mirLoopMDUnrollEnable`/`Count`, `mirLoopMDParallelAccesses`, `mirAliasScopeDomainLine`/`ScopeLine`/`ListLine`, `mirAccessGroupLine`) + state-bearing ports via `MirSeq` (`nextLoopMD`, `listAliasScopeRef`, `nextAccessGroupMD` — `g.loopMDDefs` and `g.listMetaScopeList` migrated to the mirror) |
| 2 | support check | 473–1364 | ~20 | MEDIUM | Go-only |
| 3 | header + runtime declares | 1365–1641 | ~8 | LOW | Partial — global-var, ctor, iface/struct/enum/tuple/vtable type-def lines + three runtime-declare shapes ported (`mirLlvmGlobalVarLine`, `mirLlvmIfaceTypeDefLine`, `mirLlvmStructTypeDefLine`, `mirLlvmEnumLayoutTypeDefLine`, `mirLlvmVtableDeclLine`, `mirGlobalCtorsRegistration`, `mirInitGlobalsCtorHeader`/`Footer`/`StoreSequence`, `mirRuntimeDeclareLine`, `mirRuntimeDeclareMemoryRead`, `mirRuntimeDeclareNoReturn`); orchestration (`emitGlobalVars`/`emitTypeDefs`/`emitRuntimeDeclarations`) + state (`g.declares`/`g.out`) stays on Go |
| 4 | function emission | 1642–2384 | ~18 | MEDIUM | Go-only |
| 5 | GC instrumentation | 2385–2614 | ~8 | LOW | Go-only |
| 6 | instructions | 2615–4873 | ~35 | HIGH | Go-only |
| 7 | list/map/set intrinsics | 4874–6761 | ~50 | MEDIUM | Go-only |
| 8 | concurrency intrinsics | 6762–7472 | ~20 | MEDIUM | Go-only |
| 9 | terminators | 7473–7595 | ~5 | LOW | Go-only |
| 10 | rvalue / operand | 7596–8937 | ~25 | HIGH | Go-only |
| 11 | operators | 8938–9196 | ~12 | LOW | Partial — predicates + `emitBinary` opcode table + `emitUnary` instruction body + `emitInlineStringEqLiteral` byte-by-byte streq lowering ported (`isHeapEqualityType`, `isStringPrimType`, `isStringOrderingBinOp`, `stringOrderingPredicate`, `mirBinaryOpcode`, `mirBinaryForcesI1Type`, `mirUnaryIsIdentity`, `mirUnaryInstruction`, `MirSeq.emitInlineStringEqLiteral` → `MirInlineStringEqResult`). Go side pre-converts `lit` to `[]int` (Char/Byte primitive blocked — see CLAUDE.md backend caps) and splices `result.Lines` into `g.fnBuf`. |
| 12 | strings | 9197–9284 | ~7 | LOW | Partial — `encodeLLVMString`, `earliestAfter` (single + multi-needle: `mirEarliestAfter` + `mirEarliestAfterAny`), `mirInjectBeforeFirstFn` inject orchestration, and the string-pool line template ported (`mirEncodeLLVMString`, `mirStringPoolLine`); `stringLiteral` interning stays on Go (touches `g.strings`). `emitStringPool` / `emitGlobalVars` inject step now delegates through `mirInjectBeforeFirstFn`. |
| 13 | type mapping | 9285–9375 | ~8 | LOW | **Ported** (primitive + opaque-named + head-name + optional-surface) |
| 14 | enum layout helpers | 9376–9575 | ~10 | LOW | Partial — `llvmTypeForTupleTag` Prim / Named branches + Optional / Option / Result / Tuple name-mangling ported (`mirTupleTagForPrim`, `mirTupleTagForNamed`, `mirOptionalTypeName`, `mirOptionTypeName`, `mirResultTypeName`, `mirTupleTypeNameFromTags`); `registerEnumLayout` + `g.tupleDefs` caches deferred to Phase B |
| 15 | helpers | 9576–9616 | ~5 | LOW | Partial — pure (`firstNonEmpty`, `isUnitType`, `isFloatType`, `isScalarLLVMType`, `llvmStdIoI1Text`) + state-bearing (`MirSeq.fresh` / `MirSeq.freshLabel` / `MirSeq.reset`) ported. Phase B start: `tempSeq` field migrated from `mirGen` into Osty `MirSeq` struct mirror. `ostyEmitter` / `flushOstyEmitter` / `storeIntrinsicResult` / `emitRuntimeRawNull` still touch other state; landing as the mirror grows |

## Phased plan

**Phase A — pure leaves** (~400 LOC combined). Current phase. Port
functions that have no `g.*` state dependency. Ship one section per PR
with the compile-gate generator enforcing correctness.

- ✅ §13 type mapping — first PR.
- ✅ §11 operators — pure predicates + opcode table + unary
  instruction body + `emitInlineStringEqLiteral` (the byte-by-byte
  streq switch) all ported; the streq port lives on `MirSeq` so
  `self.fresh` / `self.freshLabel` keep SSA / label numbering byte-
  stable with the legacy stream.
- ⏳ §12 strings — `encodeLLVMString`, multi-needle `earliestAfter`,
  inject-before-first-fn orchestration in (`mirEarliestAfterAny`,
  `mirInjectBeforeFirstFn`); `stringLiteral` interning still touches
  `g.strings` and stays on Go.
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
| `mirUnaryIsIdentity` | `(symbol: String) -> Bool` | §11 | True for unary `+`, the identity op — caller short-circuits to reuse the operand register |
| `mirUnaryInstruction` | `(symbol: String, argReg: String, llvmTy: String, isFloat: Bool) -> String` | §11 | LLVM instruction body for `-` / `!` / `~` — spliced between `%tmp = ` and `\n`; returns `""` on miss so caller stays on the unsupported diagnostic |
| `mirStringPoolLine` | `(sym: String, sizeDigits: String, encoded: String) -> String` | §12 | One constant line of the string pool — `@.str.N = private unnamed_addr constant [<size> x i8] c"<encoded>"\n`; `sizeDigits` is pre-formatted by the Go caller |
| `mirAliasScopeDomainLine` | `(ref: String) -> String` | §1 | `!N = distinct !{!"osty.list.metadata.domain"}` — root of the list-alias-scope chain |
| `mirAliasScopeScopeLine` | `(ref: String, domainRef: String) -> String` | §1 | `!N = distinct !{!"osty.list.metadata.scope", !Domain}` — middle node of the chain |
| `mirAliasScopeListLine` | `(ref: String, scopeRef: String) -> String` | §1 | `!N = !{!Scope}` — the one-element scope list attached via `!alias.scope`/`!noalias` |
| `mirAccessGroupLine` | `(ref: String) -> String` | §1 | `!N = distinct !{}` — A6 parallel-access group root |
| `mirLlvmGlobalVarLine` | `(name: String, llvmType: String) -> String` | §3 | `@<name> = global <T> zeroinitializer\n` — module-scope global backing; value filled by `@__osty_init_globals` ctor |
| `mirLlvmIfaceTypeDefLine` | `() -> String` | §3 | `%osty.iface = type { ptr, ptr }\n` — fat-pointer type-def, emitted once when any interface reference lands in the module |
| `mirLlvmStructTypeDefLine` | `(name: String, fieldsJoined: String) -> String` | §3 | `%<name> = type { <fields> }\n` — used for both user structs and tuple type-defs (shape is identical; caller joins field types first) |
| `mirLlvmEnumLayoutTypeDefLine` | `(name: String) -> String` | §3 | `%<name> = type { i64, i64 }\n` — fixed 2-word enum layout for Option / Result / user enums |
| `mirLlvmVtableDeclLine` | `(symbol: String) -> String` | §3 | `<sym> = external constant [0 x ptr]\n` — downcast-site vtable reference (body deferred) |
| `mirGlobalCtorsRegistration` | `() -> String` | §3 | Constant `@llvm.global_ctors` appending-array that wires `@__osty_init_globals` at priority 65535 |
| `mirInitGlobalsCtorHeader` | `() -> String` | §3 | `define private void @__osty_init_globals() {\nentry:\n` — ctor prelude |
| `mirInitGlobalsCtorFooter` | `() -> String` | §3 | `  ret void\n}\n\n` — ctor epilogue with the two-newline separator before the ctors registration line |
| `mirInitGlobalsCtorStoreSequence` | `(globName: String, retLLVM: String, initName: String) -> String` | §3 | One `%vName = call <T> @<init>() ; store <T> %vName, ptr @<glob>` pair inside the ctor body |
| `mirRuntimeDeclareLine` | `(retTy: String, sym: String, argList: String) -> String` | §3 | Plain `declare <ret> @<sym>(<args>)` — no LLVM-attribute tuning |
| `mirRuntimeDeclareMemoryRead` | `(retTy: String, sym: String, argList: String) -> String` | §3 | `declare ... ) nounwind willreturn memory(read)` — the attribute combo that unlocks LICM / CSE of snapshot calls (list-data / list-len / slow-path getters) |
| `mirRuntimeDeclareNoReturn` | `(retTy: String, sym: String, argList: String, cold: Bool) -> String` | §3 | `declare ... ) noreturn` + optional ` cold nounwind` for bounds-check traps |
| `mirGenIntToString` | `(n: Int) -> String` | §15 | Local Int→String for the MIR-emitter surface (self-host stdlib still lacks `Int.toString`); manual digit walk mirroring `mirLowerIntToString` |
| `mirGenDigitChar` | `(d: Int) -> String` | §15 | ASCII decimal digit lookup for `mirGenIntToString`; out-of-range inputs return `"?"` |
| `MirSeq` (struct) | `{ tempSeq: Int }` | §15 | First piece of `mirGen` state to land in Osty as a real mutable model. Methods below replace the Go `g.tempSeq` field + `g.fresh()` / `g.freshLabel()` direct accesses |
| `MirSeq.fresh` | `(mut self) -> String` | §15 | Issue `%tN` SSA register name + bump counter — `mut self` mirrors the pointer-receiver Go method |
| `MirSeq.freshLabel` | `(mut self, prefix: String) -> String` | §15 | Issue `<prefix>.N` basic-block label + bump counter (shares SSA namespace with `fresh`) |
| `MirSeq.reset` | `(mut self) -> ()` | §15 | Zero the counter at function-emission boundaries — replaces `g.tempSeq = 0` at the top of `emitFunction` |
| `MirSeq.reserveMDRef` | `(self) -> String` | §1 | Read-only `!N` reservation — caller emits the line via a template that embeds the ref then calls `commitMDLine` |
| `MirSeq.commitMDLine` | `(mut self, line: String) -> ()` | §1 | Append a fully-formed `!N = …` line to the module accumulator |
| `MirSeq.allocMDNode` | `(mut self, body: String) -> String` | §1 | One-shot reserve+commit when the caller only has the LLVM payload (no `!N = ` prefix) |
| `MirSeq.nextLoopMD` | `(mut self, hints: MirLoopHints) -> String` | §1 | Self-referential `!llvm.loop` node + per-property children driven by `MirLoopHints` snapshot; returns `""` when no hints active |
| `MirSeq.listAliasScopeRef` | `(mut self) -> String` | §1 | Cached lazy 3-node domain/scope/list chain (`!alias.scope` family); singleton per module |
| `MirSeq.nextAccessGroupMD` | `(mut self) -> String` | §1 | One `distinct !{}` per `#[parallel]` function — load/store attachments + per-loop `parallel_accesses` reference it |
| `MirLoopHints` (struct) | `{ vectorize, vectorizeWidth, vectorizeScalable, vectorizePredicate, parallel, parallelAccessGroupRef, unroll, unrollCount }` | §1 | Plain-data snapshot of per-function loop annotation flags fed into `MirSeq.nextLoopMD` |
| `mirChanRecvSuffix` | `(elemLLVM: String) -> String` | §7 | Channel `recv_<suffix>` runtime symbol picker — thin wrapper over `llvmChanElementSuffix` so the scalar/composite split stays in lockstep with `llvmChanRecv` |
| `mirMapValueSizeBytes` | `(llvmTyp: String) -> Int` | §7 | LLVM type → byte width for memcpy of map values (`i64`/`double`/`ptr` → 8, `i32` → 4, `i8`/`i1` → 1, else 0) |
| `mirIntLLVMBits` | `(t: String) -> Int` | §7 | `iN` width extractor (`i1`→1 … `i64`→64, else 0); used by operand-coercion to gate sext / trunc |
| `mirThunkName` | `(symbol: String) -> String` | §6 | Closure-thunk LLVM symbol-name builder — `"__osty_closure_thunk_" + symbol` |
| `mirIsMemoryAccessLine` | `(line: String) -> Bool` | §1 | Recognises the two textual shapes the MIR emitter produces for loads / stores (leading-space-strip + `store ` prefix probe + ` = load ` substring probe) |
| `mirTagParallelAccesses` | `(body: String, groupRef: String) -> String` | §1 | Walks `body` line-by-line and appends `, !llvm.access.group <groupRef>` to every load / store line that doesn't already carry the metadata. Pure (manual byte-walk; no `strings.SplitAfter` dependency) |
| `mirEmitHeaderBlock` | `(source: String, target: String) -> String` | §3 | Four-line module preamble (`; Code generated...` + `; Osty: ...` + `source_filename = ...` + optional `target triple = ...` + blank). Replaces `emitHeader`'s inline `WriteString` chain |
| `mirEarliestAfterAny` | `(input: String, needles: List<String>) -> Int` | §12 | Multi-needle `earliestAfter` — smallest non-negative offset of any needle, `-1` when none present. The Go `earliestAfter([]string{...})` now delegates here |
| `mirInjectBeforeFirstFn` | `(body: String, block: String) -> String` | §3 | Splices `block` into `body` before the first `define ` / `declare ` line; appends at the end when neither marker is present. Replaces the inline rewrite-buffer pattern in `emitGlobalVars` / `emitStringPool` |
| `mirJoinDeclareLines` | `(orderedDecls: List<String>) -> String` | §3 | Concatenates ordered `declare ...` strings with trailing newlines into one block ready for `mirInjectBeforeFirstFn`. Caller still owns the dedupe / ordering map |
| `MirInlineStringEqResult` (struct) | `{ finalReg: String, lines: List<String> }` | §11 | Value/code split returned by `MirSeq.emitInlineStringEqLiteral`. Caller iterates `lines` (each already including leading 2-space indent + trailing newline) into `g.fnBuf`, then uses `finalReg` as the i1 result |
| `MirSeq.emitInlineStringEqLiteral` | `(mut self, opIsEq: Bool, dynReg: String, litSym: String, litBytes: List<Int>) -> MirInlineStringEqResult` | §11 | Byte-by-byte string-equality switch with pointer-equality fast path + per-byte compare + terminating NUL check. `litBytes` is the per-byte int view of the literal (Go converts via `int(lit[i])`). Every `freshLabel` / `fresh` call mirrors the legacy emitter so `tempSeq` advances byte-stably across the port |

Keep this table updated as each section lands. New entries go in
insertion order so the provenance columns (`Origin §`) stay useful as
the file grows.
