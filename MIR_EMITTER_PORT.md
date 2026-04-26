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
| 4 | function emission | 1642–2384 | ~18 | MEDIUM | Partial — pure post-process leaves + Phase C entry templates ported (`mirIsMemoryAccessLine`, `mirTagParallelAccesses`, `mirCConvKeyword`, `mirParamIsNoalias`, `mirFunctionParamPart`, `mirBlockLabelName`, `mirExternalDeclareLine`, `mirFunctionDefineHeader`). `emitFunction` external-declare path + define-header signature line both delegate; per-fn loop hint capture + alloca preamble + block emission still on Go (touch `g.fn` / `g.fnBuf` / `g.localSlots`). Removed dead `noaliasNameSet` + `paramIsNoalias` Go helpers (now `mirParamIsNoalias`). |
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
| 15 | helpers | 9576–9616 | ~5 | LOW | Partial — pure (`firstNonEmpty`, `isUnitType`, `isFloatType`, `isScalarLLVMType`, `llvmStdIoI1Text`) + state-bearing leaves (`MirSeq.fresh` / `MirSeq.freshLabel` / `MirSeq.reset`) + Phase B fnBuf mirror (`MirSeq.fnBuf`, `MirSeq.appendFnLine`, `MirSeq.flushFnBuf`, `MirSeq.absorbOstyEmitter`) ported. `flushOstyEmitter` Go bridge now routes through `MirSeq.absorbOstyEmitter` (Osty drains `em.body` into `seq.fnBuf`; Go drains back to `g.fnBuf`). `storeIntrinsicResult` Go body now uses the Osty `mirStoreLine` builder. `ostyEmitter` constructor stays on Go (Go `LlvmEmitter` has fields the Osty struct doesn't model — `nativeListData`/`nativeListLens`). `emitRuntimeRawNull` is mir-internal routing, no Osty change. |

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
- ✅ §15 helpers — pure-side + state-bearing leaves (`fresh`,
  `freshLabel`, `reset`) + Phase B fnBuf mirror (`fnBuf`,
  `appendFnLine`, `flushFnBuf`, `absorbOstyEmitter`) done.
  `flushOstyEmitter` routes through `MirSeq.absorbOstyEmitter`;
  `storeIntrinsicResult` uses the Osty `mirStoreLine` builder.
  `ostyEmitter` constructor stays on Go (Go-only LlvmEmitter fields).
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
| `MirSeq.emitInlineStringEqLiteral` | `(mut self, opIsEq: Bool, dynReg: String, litSym: String, litBytes: List<Int>) -> MirInlineStringEqResult` | §11 | Byte-by-byte string-equality switch with pointer-equality fast path + per-byte compare + terminating NUL check. Now expressed in terms of the small `mir*Line` builders (`mirICmpEqLine`, `mirBrCondLine`, `mirGEPInboundsI8Line`, `mirLoadLine`, `mirLabelLine`, `mirLabelHeadWithBranch`, `mirPhiI1FromTwoLine`, `mirXorI1NegLine`); SSA / label numbering still byte-stable with the legacy stream |
| `MirSeq.fnBuf` (field) | `List<String>` | §15 | Per-function body accumulator. Phase B foundation — Osty-side mirror of `g.fnBuf strings.Builder`. Populated by `appendFnLine` / `absorbOstyEmitter`; drained by `flushFnBuf` |
| `MirSeq.appendFnLine` | `(mut self, line: String) -> ()` | §15 | Push one fully-formed line (including indent + trailing newline) onto `fnBuf`. Mirrors `g.fnBuf.WriteString(line)` |
| `MirSeq.flushFnBuf` | `(mut self) -> List<String>` | §15 | Return the accumulated lines and clear the buffer in one move; caller drains into `g.fnBuf` so the existing flush-to-`g.out` path stays unchanged |
| `MirSeq.absorbOstyEmitter` | `(mut self, em: LlvmEmitter) -> ()` | §15 | Sync a Go-driven LlvmEmitter scope back into MirSeq — bumps `tempSeq` to `em.temp` and drains `em.body` into `fnBuf`. The `flushOstyEmitter` Go bridge routes through here |
| `mirStoreLine` | `(ty: String, val: String, slot: String) -> String` | §6 | `  store <ty> <val>, ptr <slot>\n` — the most common emit shape across `mir_generator.go`. `storeIntrinsicResult` now delegates here |
| `mirCallVoidLine` | `(sym: String, argList: String) -> String` | §6 | `  call void @<sym>(<argList>)\n` — runtime-action call (push/pop/clear/etc.); caller pre-joins `argList` |
| `mirCallValueLine` | `(reg: String, retTy: String, sym: String, argList: String) -> String` | §6 | `  <reg> = call <retTy> @<sym>(<argList>)\n` — runtime helper that returns a scalar |
| `mirGEPInboundsI8Line` | `(reg: String, basePtr: String, offDigits: String) -> String` | §6 | Byte-stride GEP form `  <reg> = getelementptr inbounds i8, ptr <basePtr>, i64 <offDigits>\n`; `offDigits` pre-formatted decimal |
| `mirLoadLine` | `(reg: String, ty: String, ptr: String) -> String` | §6 | Plain load `  <reg> = load <ty>, ptr <ptr>\n` (no `align` / `!nontemporal` hints) |
| `mirICmpEqLine` | `(reg: String, ty: String, lhs: String, rhs: String) -> String` | §6 | `  <reg> = icmp eq <ty> <lhs>, <rhs>\n` — eq-only specialisation; other predicates use `mirBinaryOpcode` |
| `mirBrCondLine` | `(cond: String, trueLabel: String, falseLabel: String) -> String` | §9 | Conditional branch `  br i1 <cond>, label %<trueLabel>, label %<falseLabel>\n`; labels are bare names |
| `mirBrUncondLine` | `(label: String) -> String` | §9 | Unconditional branch `  br label %<label>\n` |
| `mirLabelLine` | `(name: String) -> String` | §9 | Basic-block header `<name>:\n`; bare label name input |
| `mirLabelHeadWithBranch` | `(name: String, target: String) -> String` | §9 | Combined head-of-block + tail-branch `<name>:\n  br label %<target>\n` — match / nomatch joinpoint shape |
| `mirPhiI1FromTwoLine` | `(reg: String, trueLabel: String, falseLabel: String) -> String` | §6 | Two-incoming-edge i1 phi `  <reg> = phi i1 [true, %<trueLabel>], [false, %<falseLabel>]\n` — boolean joinpoint |
| `mirXorI1NegLine` | `(reg: String, src: String) -> String` | §6 | i1 negation `  <reg> = xor i1 <src>, true\n` — used by streq for the `!=` form |
| `mirStoreZeroinitLine` | `(ty: String, slot: String) -> String` | §6 | `  store <ty> zeroinitializer, ptr <slot>\n` — None-branch slot zeroing for Option<T> in `IntrinsicListFirst`/`IntrinsicListLast`/`IntrinsicMapGet` |
| `mirInsertValueAggLine` | `(reg: String, aggTy: String, baseVal: String, fieldTy: String, val: String, idxDigits: String) -> String` | §6 | `  <reg> = insertvalue <aggTy> <baseVal>, <fieldTy> <val>, <idxDigits>\n` — field-by-field aggregate construction (Some payload, tuple, Result) |
| `mirSubI64Line` | `(reg: String, lhs: String, rhs: String) -> String` | §6 | i64 subtraction `  <reg> = sub i64 <lhs>, <rhs>\n` — len-1 / byte-offset hot uses; other widths route through `mirBinaryOpcode` |
| `mirAddI64Line` | `(reg: String, lhs: String, rhs: String) -> String` | §6 | i64 addition `  <reg> = add i64 <lhs>, <rhs>\n` — sibling of sub; used by linear-scan loops for the `i = i + 1` step |
| `mirMulI64Line` | `(reg: String, lhs: String, rhs: String) -> String` | §6 | i64 multiplication `  <reg> = mul i64 <lhs>, <rhs>\n` — bench harness `target * probeIters` and similar i64 products |
| `mirSDivI64Line` | `(reg: String, lhs: String, rhs: String) -> String` | §6 | i64 signed division `  <reg> = sdiv i64 <lhs>, <rhs>\n` — bench harness `est = numer / probeSafe` clamp computation |
| `mirCallValueNoArgsLine` | `(reg: String, retTy: String, sym: String) -> String` | §6 | Argumentless typed call `  <reg> = call <retTy> @<sym>()\n` — bench-harness clock reads (`osty_rt_bench_target_ns`, `osty_rt_bench_now_nanos`) and other zero-arg runtime probes |
| `MirRuntimeDecls` (struct) | `{ names: List<String>, signatures: Map<String, String> }` | §3 | Runtime forward-declaration cache. Owns the dedup + insertion-order side of the emitter's `declare <ret> @<sym>(<args>)` pool. Replaces the `mirGen.declares map[string]string + declareOrder []string` Go fields. Sibling of `MirLayoutCache` (#888) for aggregate type-def pools. |
| `MirRuntimeDecls.declare` | `(mut self, name: String, signature: String) -> Bool` | §3 | Records `name → signature` and bumps the insertion list. Returns true when newly added, false when already declared — caller can gate per-declare side effects |
| `MirRuntimeDecls.signature` | `(self, name: String) -> String` | §3 | Looks up the declare line for `name`; returns `""` when not declared |
| `MirRuntimeDecls.orderedSignatures` | `(self) -> List<String>` | §3 | Insertion-ordered declare lines, ready for `mirJoinDeclareLines` to glue with `\n` separators. Used by `emitRuntimeDeclarations` to build the runtime-declare block injected before the first `define ` |
| `MirRuntimeDecls.isEmpty` | `(self) -> Bool` | §3 | Early-exit gate for `emitRuntimeDeclarations` — pointer-free / runtime-free modules emit no declare block |
| `MirStringPool` (struct) | `{ byContent: Map<String, String>, order: List<String> }` | §12 | String-literal interning pool. Owns the dedup + insertion-order side of the emitter's `@.str.N = private unnamed_addr constant ...` lines. Replaces `mirGen.strings + stringOrder` Go fields. Sibling of `MirRuntimeDecls` and `MirLayoutCache` — third member of the dedup-with-order family |
| `MirStringPool.intern` | `(mut self, content: String) -> String` | §12 | Returns the `@.str.N` symbol for `content`, allocating a fresh one (N = `order.len()` at allocation time) when first seen, returning the cached symbol on repeats |
| `MirStringPool.symbol` | `(self, content: String) -> String` | §12 | Looks up the symbol for an already-interned content; returns `""` when not present |
| `MirStringPool.orderedKeys` | `(self) -> List<String>` | §12 | Insertion-ordered list of literal contents — drives `emitStringPool`'s deterministic walk over the pool |
| `MirStringPool.isEmpty` | `(self) -> Bool` | §12 | Early-exit gate for `emitStringPool` — modules without string constants emit no pool block |
| `MirThunkDefs` (struct) | `{ bodies: Map<String, String>, order: List<String> }` | §4 | Closure-thunk definition cache. Owns the dedup + insertion-order side of the emitter's `define private <ret> @__osty_closure_thunk_<sym>(ptr env, ...)` shim functions. Replaces `mirGen.thunkDefs + thunkOrder` Go fields. Fourth member of the dedup-with-order family alongside `MirLayoutCache`, `MirRuntimeDecls`, `MirStringPool` |
| `MirThunkDefs.contains` | `(self, symbol: String) -> Bool` | §4 | Reports whether a thunk for `symbol` has already been generated. Used by `ensureThunk` as an early-exit gate before doing the (non-trivial) work of formatting the thunk's IR body |
| `MirThunkDefs.register` | `(mut self, symbol: String, body: String) -> Bool` | §4 | Records a freshly-generated thunk body. Returns true when newly added, false when already present |
| `MirThunkDefs.body` | `(self, symbol: String) -> String` | §4 | Looks up the IR for an already-registered thunk; returns `""` when not present |
| `MirThunkDefs.orderedBodies` | `(self) -> List<String>` | §4 | Insertion-ordered thunk IR strings — `emitThunks` concatenates these directly (each body already ends with `}\n\n`) |
| `MirThunkDefs.isEmpty` | `(self) -> Bool` | §4 | Early-exit gate for `emitThunks` — modules without closure-converted top-level fns emit no thunk block |
| `MirVtableRefs` (struct) | `{ seen: Map<String, Bool>, order: List<String> }` | §3 | Vtable reference set (downcast support). Owns the dedup + insertion-order side of the emitter's `@osty.vtable.<impl>__<iface>` external constant declarations. Replaces `mirGen.vtableRefs + vtableRefOrder` Go fields. Fifth (and capping) member of the dedup-with-order family alongside `MirLayoutCache`, `MirRuntimeDecls`, `MirStringPool`, `MirThunkDefs` |
| `MirVtableRefs.register` | `(mut self, symbol: String) -> Bool` | §3 | Adds `symbol` to the set; returns true when newly added, false when already seen |
| `MirVtableRefs.contains` | `(self, symbol: String) -> Bool` | §3 | Read-only check; sibling of `MirThunkDefs.contains` so cross-cache code reads uniformly |
| `MirVtableRefs.orderedSymbols` | `(self) -> List<String>` | §3 | Insertion-ordered list of registered symbols — drives `emitTypeDefs`'s walk over the external-constant block |
| `MirVtableRefs.isEmpty` | `(self) -> Bool` | §3 | Early-exit signal for callers that gate the `@osty.vtable.* = external constant [0 x ptr]` block on presence of any downcast site |
| `mirCConvKeyword` | `(cabi: Bool) -> String` | §4 | LLVM calling-convention keyword (`"ccc "` for `#[c_abi]` / `""` for default). Trailing space is part of the return |
| `mirParamIsNoalias` | `(llvmT: String, locName: String, noaliasAll: Bool, noaliasNames: List<String>) -> Bool` | §4 | Per-param `noalias` decision. Replaces the Go `noaliasNameSet` + `paramIsNoalias` helper pair — Osty side does a linear scan over the names slice (O(small N) since `#[noalias(...)]` lists rarely exceed a handful) |
| `mirFunctionParamPart` | `(llvmT: String, isNoalias: Bool, idxDigits: String) -> String` | §4 | One parameter entry of a function signature: `<llvmT>[ noalias] %arg<idxDigits>` |
| `mirBlockLabelName` | `(isEntry: Bool, blockIDDigits: String) -> String` | §4 | `"entry"` for the function's entry block, `"bb<N>"` otherwise. Used by `emitFunction`'s block-label allocation loop |
| `mirExternalDeclareLine` | `(cconv, retLLVM, name, paramListJoined, attrs) -> String` | §4 | `declare` line for an external (non-defined) function. Trailing `\n\n` matches legacy spacing — one blank line before the next `define`/`declare` |
| `mirFunctionDefineHeader` | `(cconv, retLLVM, name, paramListJoined, attrs) -> String` | §4 | Opening line of a function definition — `define [cconv]<retLLVM> @<name>(<params>) [attrs] {\n`. The `{` is included; body / closing `}` come from caller |
| `mirFunctionDefineFooter` | `() -> String` | §4 | Closing `}\n\n` of a function definition. Pair with `mirFunctionDefineHeader` to bookend every emitted function body |
| `mirInlineAsmIdentityCallLine` | `(reg: String, ty: String, val: String) -> String` | §6 | LLVM inline-asm identity-barrier shape used by `std.hint.black_box(x)`: empty asm body + `"=r,0"` register-tied constraint blocks DCE + const-fold |
| `mirCallVarargPrintfLine` | `(fmtSym: String, restArgs: String) -> String` | §6 | LLVM-IR printf call shape `  call i32 (ptr, ...) @printf(ptr <fmt>[, <args>])\n` — testing-abort, bench-error, bench-summary emit paths |
| `mirCallExitLine` | `(codeDigits: String) -> String` | §6 | Noreturn exit-call shape `  call void @exit(i32 <code>)\n` — used as the second-to-last line in testing-abort / bench `?` failure paths |
| `mirBrCondReversedLine` | `(cond: String, falseLabel: String, trueLabel: String) -> String` | §6 | Conditional branch with true/false target order swapped — `assertFalse` shape where failure is on cond=true |
| `mirCallIndirectVoidLine` | `(callType: String, fnPtrReg: String, argList: String) -> String` | §6 | Void-return indirect call `  call <callType> <fnPtrReg>(<args>)\n` — closure / fn-pointer call sites where callee is an SSA value |
| `mirCallIndirectValueLine` | `(reg: String, callType: String, fnPtrReg: String, argList: String) -> String` | §6 | Typed indirect call sibling of `mirCallIndirectVoidLine` |
| `MirAggregatePair` (struct) | `{ step1Reg: String, finalReg: String, lines: List<String> }` | §10 | Output of an Option / Result 2-step aggregate construction. Caller iterates `lines` into `g.fnBuf` and uses `finalReg` as the result; `step1Reg` exposed for cases that need to reference the partial |
| `mirSomeI64Aggregate` | `(step1Reg, finalReg, optLLVM, payloadI64) -> MirAggregatePair` | §10 | Builds the Some(payload) shape: 2 insertvalue lines (disc=1, payload=runtime value) into `%Option.<T>` |
| `mirNoneAggregate` | `(step1Reg, finalReg, optLLVM) -> MirAggregatePair` | §10 | Builds the None shape: 2 insertvalue lines (disc=0, payload=0) into `%Option.<T>` |
| `mirResultOkI64Aggregate` | `(step1Reg, finalReg, resultLLVM, payloadI64) -> MirAggregatePair` | §10 | Builds the Ok(payload) shape — same insertvalue pair but for `%Result.<Ok>.<Err>` |
| `mirResultErrI64Aggregate` | `(step1Reg, finalReg, resultLLVM, payloadI64) -> MirAggregatePair` | §10 | Builds the Err(payload) shape — used by string-parse + checked-cancellation paths |
| `mirGCAllocCallLine` | `(reg: String, traceKindDigits: String, size: String, site: String) -> String` | §10 | GC heap allocator call `<reg> = call ptr @osty.gc.alloc_v1(i64 <kind>, i64 <size>, ptr <site>)` — used by `toI64Slot`'s aggregate-payload heap-box path |
| `mirFPTruncDoubleToFloatLine` | `(reg: String, val: String) -> String` | §6 | FP-truncate from `double` to `float` |
| `mirFPExtFloatToDoubleLine` | `(reg: String, val: String) -> String` | §6 | FP-extend from `float` to `double` |
| `mirAndI1Line` | `(reg: String, lhs: String, rhs: String) -> String` | §6 | i1 logical-and `  <reg> = and i1 <lhs>, <rhs>\n` — used by bounds-check `nonNeg && inUpper` pattern in `emitListSafeGet` |
| `mirCallValueWithAliasScopeLine` | `(reg, retTy, sym, argList, scopeRef) -> String` | §1 | Snapshot-call form with `!alias.scope` metadata: `  <reg> = call <retTy> @<sym>(<args>), !alias.scope <ref>\n`. Unlocks LICM of `osty_rt_list_data_*` / `osty_rt_list_len` snapshot reads |
| `mirLoadWithNoAliasLine` | `(reg: String, ty: String, ptr: String, scopeRef: String) -> String` | §1 | Load tagged with `!noalias` metadata — vector-list fast-path read |
| `mirStoreWithNoAliasLine` | `(ty: String, val: String, ptr: String, scopeRef: String) -> String` | §1 | Store tagged with `!noalias` — vector-list fast-path write (symmetric to `mirLoadWithNoAliasLine`) |
| `mirCallVoidNoReturnNoArgsLine` | `(sym: String) -> String` | §6 | `  call void @<sym>() noreturn\n` — OOB-abort traps and other dead-end runtime hooks |
| `mirAllocaArrayLine` | `(reg: String, ty: String, countDigits: String) -> String` | §3 | Array-allocation form `  <reg> = alloca <ty>, i64 <count>\n` — GC root chunking allocates `alloca ptr, i64 N` for the slots vector |
| `mirGEPLine` | `(reg, baseTy, basePtr, idxTy, idx) -> String` | §6 | Non-inbounds GEP form (sibling of `mirGEPInboundsLine`) — used by GC root chunking where the slots-array stride is already known to be in range |
| `mirStorePtrLine` | `(val: String, slot: String) -> String` | §6 | Pointer-payload store specialisation `  store ptr <val>, ptr <slot>\n` — reads cleaner than the general `mirStoreLine("ptr", ...)` form |
| `mirFCmpLine` | `(reg: String, pred: String, ty: String, lhs: String, rhs: String) -> String` | §6 | General floating-point compare `  <reg> = fcmp <pred> <ty> <lhs>, <rhs>\n` — used by typed list scans on Float/Float64 elements |
| `mirGEPInboundsLine` | `(reg: String, baseTy: String, basePtr: String, idxTy: String, idx: String) -> String` | §6 | General single-index GEP — supersedes the over-specialised `mirGEPInboundsI8Line` (which stays as a thin wrapper for hot byte-stride sites) |
| `mirGEPStructFieldLine` | `(reg: String, structTy: String, basePtr: String, fieldDigits: String) -> String` | §6 | Two-index struct-field GEP `  <reg> = getelementptr inbounds <T>, ptr <p>, i32 0, i32 <N>\n` — canonical "field N of aggregate at p" |
| `mirICmpLine` | `(reg: String, pred: String, ty: String, lhs: String, rhs: String) -> String` | §6 | General icmp with arbitrary predicate (`eq`/`ne`/`slt`/`sle`/...). The eq-only `mirICmpEqLine` stays for streq/length probes |
| `mirAllocaLine` | `(reg: String, ty: String) -> String` | §3 | Function-preamble slot allocation `  <reg> = alloca <ty>\n` |
| `mirRetLine` | `(ty: String, val: String) -> String` | §9 | Value return `  ret <ty> <val>\n` |
| `mirRetVoidLine` | `() -> String` | §9 | Void return `  ret void\n` |
| `mirSelectLine` | `(reg: String, ty: String, cond: String, lhs: String, rhs: String) -> String` | §6 | i1 select `  <reg> = select i1 <c>, <ty> <l>, <ty> <r>\n` |
| `mirSExtLine` / `mirZExtLine` / `mirTruncLine` | `(reg, fromTy, val, toTy) -> String` | §6 | Width conversions: sign-extend / zero-extend / truncate |
| `mirPtrToIntLine` / `mirIntToPtrLine` | various | §6 | Pointer↔int conversions used by Option<T*> payload narrows / widens |
| `mirCommentLine` | `(text: String) -> String` | §6 | LLVM IR comment line `  ; <text>\n` |
| `mirExtractValueLine` | `(reg: String, aggTy: String, aggVal: String, idxDigits: String) -> String` | §6 | Aggregate field projection `  <reg> = extractvalue <aggTy> <aggVal>, <idx>\n` — Option/Result disc + payload reads |
| `mirBitcastLine` | `(reg: String, fromTy: String, val: String, toTy: String) -> String` | §6 | Same-width type reinterpretation — `i64`↔`double` Option payload narrows |
| `mirPhiTwoLine` | `(reg: String, ty: String, val1: String, label1: String, val2: String, label2: String) -> String` | §6 | General two-arm phi `  <reg> = phi <ty> [ <v1>, %<l1> ], [ <v2>, %<l2> ]\n` — Option unwrap-or merges, conditional value joins |
| `mirCallVoidNoArgsLine` | `(sym: String) -> String` | §6 | `  call void @<sym>()\n` — argumentless action call (unwrap abort, runtime hooks) |
| `mirUnreachableLine` | `() -> String` | §9 | LLVM `  unreachable\n` terminator after `noreturn` calls |
| `MirSeq.ostyEmitter` | `(self) -> LlvmEmitter` | §15 | Constructs a fresh `LlvmEmitter` seeded from `tempSeq`. Replaces the inline `&LlvmEmitter{temp:...,body:nil}` Go shim — drift fix added two missing fields (`nativeListData`, `nativeListLens`) to the Osty `LlvmEmitter` struct so the snapshot mirror compiles |
| `mirStoreNullPtrLine` | `(slot: String) -> String` | §6 | Specialised `  store ptr null, ptr <slot>\n` — GC-managed slot zeroing pattern. Used by `emitGCPolledLocals`'s preamble (zeroing non-param roots before the first poll) and the StorageDead-driven slot retire helper. Reads cleaner than `mirStorePtrLine("null", slot)` because grepping for the literal "store ptr null" finds every zero-init managed slot in the codebase |
| `mirAllocaWithStoreLine` | `(reg: String, ty: String, init: String) -> String` | §6 | Two-line preamble pattern `alloca <ty>` + `store <ty> <init>, ptr <reg>` — the canonical "freshly-zeroed scalar slot" idiom. Used by the loop-safepoint poll counter (`alloca i64` / `store i64 0`) and other fresh-typed-slot sites. Centralises the order so refactors don't reshuffle the alloca/store pair |
| `mirAllocaWithStorePtrLine` | `(reg: String, val: String) -> String` | §6 | Pointer-typed sibling — `alloca ptr` + `store ptr <val>, ptr <reg>`. Closure-thunk materialisation and indirect-call argument prep both spell this out inline; the named builder captures the intent |
| `mirAllocaWithStoreNullPtrLine` | `(reg: String) -> String` | §6 | `alloca ptr` + `store ptr null` at once — canonical zero-init managed-pointer slot used by `emitNullaryRV` for `Some(None)` payload synthesis and by the GC root preamble for non-param roots |
| `mirRawAssignLine` | `(reg: String, rhs: String) -> String` | §6 | Catch-all SSA assignment `  <reg> = <rhs>\n` for sites where the right-hand side is computed by a separate formatter (e.g. `mirUnaryInstruction`, predicate-string concatenation). Cuts five-call inline chains down to one. Use this when no specialised builder fits |
| `mirCallStmtLine` | `(retTy: String, sym: String, argList: String) -> String` | §6 | Statement-position call where the return value is discarded — `  call <retTy> @<sym>(<args>)\n`. Distinct from `mirCallVoidLine` because that hard-codes `void`; this one lets the caller pass `{ i64, i64 }` or other shapes for the unused panic-helper / cancellation-checker returns |
| `mirCallStmtNoArgsLine` | `(retTy: String, sym: String) -> String` | §6 | No-argument variant of `mirCallStmtLine` — `  call <retTy> @<sym>()\n`. Used by bounded-thread poll helpers whose result is conditionally retained (dest=nil → through this builder, dest≠nil → through `mirCallValueNoArgsLine`) |
| `mirBinaryOpLine` | `(reg: String, opcode: String, ty: String, lhs: String, rhs: String) -> String` | §6 | Two-operand instruction shape `  <reg> = <opcode> <ty> <lhs>, <rhs>\n` — the canonical generic shape that `emitBinary` lowers to after picking the opcode via `mirBinaryOpcode`. Scalar-typed `mirAddI64Line`/`mirShlI64Line` family stays for hot paths while the generic dispatcher reaches for one builder |
| `mirICmpLineFromPred` | `(reg, pred, ty, lhs, rhs) -> String` | §6 | Thin alias of `mirICmpLine` used at sites that thread the predicate string from `mirBinaryOpcode` — keeps the call site self-documenting |
| `mirCallI64MapKeyDeltaLine` | `(reg, sym, mapReg, keyLLVM, key, delta) -> String` | §6 | Fused `map.incr` ABI call `  <reg> = call i64 @<sym>(ptr <map>, <keyLLVM> <key>, i64 <delta>)\n`. Drains the 13-line inline chain at the `IntrinsicMapIncr` site (the `m.insert(k, m.getOr(k,0)+delta)` peephole). Mirror of `mirCallValueLine` specialised for this 3-arg shape |
| `mirCallVoidMapKeyValuePtrLine` | `(sym, mapReg, keyLLVM, key, valSlot) -> String` | §6 | Runtime map-set ABI `  call void @<sym>(ptr <map>, <keyLLVM> <key>, ptr <valSlot>)\n` that `IntrinsicMapSet` lowers to. Memcpy's `value_size` bytes from `valSlot` into the map's storage |
| `mirCallI1MapKeyOutPtrLine` | `(reg, sym, mapReg, keyLLVM, key, outSlot) -> String` | §6 | Runtime map-probe ABI `  <reg> = call i1 @<sym>(ptr <map>, <keyLLVM> <key>, ptr <outSlot>)\n`. Used by `IntrinsicMapGet` / `IntrinsicMapGetOr` — the runtime returns true-on-hit and writes the value into `outSlot` only on the hit path |
| `mirCallVoidListPtrI64ValueLine` | `(sym, listReg, idxReg, elemLLVM, valReg) -> String` | §6 | Typed-element list-set runtime ABI `  call void @<sym>(ptr <list>, i64 <idx>, <elemLLVM> <val>)\n`. Used by the typed-element fast path of `osty_rt_list_set_<suffix>` |
| `mirCallVoidListBytesV1SetLine` | `(sym, listReg, idxReg, slot, size) -> String` | §6 | Bytes-v1 list-set ABI `  call void @<sym>(ptr <list>, i64 <idx>, ptr <slot>, i64 <size>)\n`. Composite element types (struct / tuple) go through this shape |
| `mirCallVoidListBytesV1GetLine` | `(sym, listReg, idxReg, outSlot, size) -> String` | §6 | Bytes-v1 list-get ABI — symmetric with `mirCallVoidListBytesV1SetLine`. Memcpy's `size` bytes from the list's storage into the caller's `out` slot |
| `mirCallVoidListPushBytesV1Line` | `(sym, listReg, slot, size) -> String` | §6 | Bytes-v1 list-push ABI `  call void @<sym>(ptr <list>, ptr <slot>, i64 <size>)\n`. Used by `emitListPushOperand`'s composite-element path |
| `mirGEPNullSizeLine` | `(reg, ty) -> String` | §6 | `getelementptr <ty>, ptr null, i32 1` — the size-of stride GEP. Half of the standard sizeof idiom |
| `mirPtrToIntSizeLine` | `(reg, gepReg) -> String` | §6 | `<reg> = ptrtoint ptr <gepReg> to i64` — second half of the GEP-null sizeof idiom |
| `mirSizeOfLines` | `(gepReg, sizeReg, ty) -> String` | §6 | Renders both halves of the GEP-null sizeof idiom in one helper. Centralises the two-step sequence so order stays stable |
| `mirThunkHeaderLine` | `(retLLVM, thunkName, headerParams) -> String` | §4 | Opening line of a closure-thunk definition: `define private <retLLVM> @<thunkName>(<headerParams>) {\n` |
| `mirThunkEntryLine` | `() -> String` | §4 | Literal `entry:\n` block label header used by every thunk body |
| `mirThunkVoidCallLine` | `(symbol, argList) -> String` | §4 | Void-return thunk body: `call void @<sym>(<args>)` + `ret void` |
| `mirThunkValueCallLine` | `(retLLVM, symbol, argList) -> String` | §4 | Value-return thunk body: `%ret = call <retTy> @<sym>(<args>)` + `ret <retTy> %ret` |
| `mirThunkFooterLine` | `() -> String` | §4 | Literal `}\n\n` that closes a thunk definition. Trailing two newlines match legacy emitter spacing |
| `mirThunkBody` | `(retLLVM, thunkName, symbol, headerParams, argList) -> String` | §4 | Assembles the entire closure-thunk text in one builder. Replaces the 30-line inline `strings.Builder` block in `ensureThunk` |
| `mirThunkParamPart` | `(llvmT, idxDigits) -> String` | §4 | One parameter entry of a thunk's user-param list: `<llvmT> %arg<idxDigits>`. Mirror of `mirFunctionParamPart` for the thunk surface (no `noalias` attribute) |
| `mirCallVarargPrintfPathLine` | `(fmtSym, pathSym) -> String` | §6 | Path-prefixed printf shape `  call i32 (ptr, ...) @printf(ptr <fmt>, ptr <path>)\n`. Used by `emitBenchErrorCheck` and `emitTestingAbortString` |
| `mirICmpEqI64Line` | `(reg, lhs, rhs) -> String` | §6 | eq-on-i64 specialisation of `mirICmpEqLine`. Hot sites (Result tag check, discriminant-zero probe) use this directly |
| `mirICmpEqI1Line` | `(reg, lhs, rhs) -> String` | §6 | eq-on-i1 specialisation of `mirICmpEqLine`. Used by the bool-equality fast path |
| `mirICmpEqPtrLine` | `(reg, lhs, rhs) -> String` | §6 | eq-on-ptr specialisation of `mirICmpEqLine`. Used by the inline string-equality literal pointer-equality fast path and managed-handle null checks |
| `mirCallVarargPrintfTwoArgLine` | `(fmtSym, arg1, arg2) -> String` | §6 | printf with two variadic args. Used by testing-summary `(seed 0x%x)` lines |
| `mirCallVarargPrintfThreeArgLine` | `(fmtSym, arg1, arg2, arg3) -> String` | §6 | printf with three variadic args. Used by bench summary preludes |
| `mirAllocaSpillStoreLine` | `(slot, ty, val) -> String` | §6 | `alloca <ty>` + `store <ty> <val>, ptr <slot>` — canonical "spill an SSA value into a stack slot" preamble before bytes-v1 runtime calls |
| `mirRuntimeDeclarePrintf` | `() -> String` | §3 | `declare i32 @printf(ptr, ...)` — drains the literal printf decl at testing-abort, bench-summary, and println-like emit sites |
| `mirRuntimeDeclareExit` | `() -> String` | §3 | `declare void @exit(i32)` — drains the literal exit decl at testing-abort, bench-error, panic-helper sites |
| `mirRuntimeDeclarePtrFromPtrLine` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(ptr)` — the most common runtime ABI shape (~22 sites: list_keys/data, set_to_list, etc.) |
| `mirRuntimeDeclareI1FromPtrLine` | `(sym: String) -> String` | §3 | `declare i1 @<sym>(ptr)` — predicate runtime shape (is_empty/is_closed/pop_check) |
| `mirRuntimeDeclareVoidFromPtrLine` | `(sym: String) -> String` | §3 | `declare void @<sym>(ptr)` — side-effect runtime shape (clear/reverse/close/pop_discard) |
| `mirRuntimeDeclareI64FromPtrLine` | `(sym: String) -> String` | §3 | `declare i64 @<sym>(ptr)` — scalar-returning ptr-handle probe shape (plain, no LICM attrs). Distinct from `mirRuntimeDeclareMemoryRead` |
| `mirRuntimeDeclarePtrFromScalarLine` | `(sym, scalarLLVM) -> String` | §3 | `declare ptr @<sym>(<scalar>)` — scalar-to-string family (int_to_string/float_to_string/bool/char/byte) |
| `mirRuntimeDeclareI64NoArgsLine` | `(sym: String) -> String` | §3 | `declare i64 @<sym>()` — zero-arg scalar probe (bench clock reads, GC-debug allocators) |
| `mirRuntimeDeclareVoidI32Line` | `(sym: String) -> String` | §3 | `declare void @<sym>(i32)` — abstract-symbol form for noreturn exit-like helpers |
| `mirRuntimeDeclareSafepointV1` | `() -> String` | §3 | `declare void @osty.gc.safepoint_v1(i64, ptr, i64)` — GC safepoint runtime ABI canonical decl |
| `mirRuntimeDeclareGcAllocV1` | `() -> String` | §3 | `declare ptr @osty.gc.alloc_v1(i64, i64, ptr)` — GC allocator runtime ABI canonical decl |
| `mirRuntimeDeclareStringConcat` | `() -> String` | §3 | `declare ptr @osty_rt_strings_Concat(ptr, ptr)` — String concat runtime ABI canonical decl |
| `mirSomeAggregateLines` | `(taggedReg, filledReg, optLLVM, payloadI64) -> String` | §6 | Canonical Some(payload) two-insertvalue construction. Drains the 7-site repetition at IntrinsicListFirst/Last/Pop/IndexOf/Contains/MapGet/ListSafeGet |
| `mirSomeStoreLines` | `(taggedReg, filledReg, optLLVM, payloadI64, destSlot) -> String` | §6 | Some-aggregate construction + store into dest slot — 3-line block |
| `mirSomeStoreThenJumpLines` | `(taggedReg, filledReg, optLLVM, payloadI64, destSlot, endLabel) -> String` | §6 | Full Some-arm body: aggregate + store + br to join — 4-line block |
| `mirNoneStoreThenJumpLines` | `(optLLVM, destSlot, endLabel) -> String` | §6 | None-arm body: zeroinitializer store + br to join — 2-line block |
| `mirNoneAggregateLines` | `(stepReg, valueReg, optLLVM) -> String` | §6 | Canonical None two-insertvalue (disc=0, payload=0) for phi-merge sites |
| `mirOkAggregateLines` | `(stepReg, valueReg, resultLLVM, payloadI64) -> String` | §6 | Canonical Ok(payload) two-insertvalue for `Result<Ok, Err>` |
| `mirErrAggregateLines` | `(stepReg, valueReg, resultLLVM, payloadI64) -> String` | §6 | Canonical Err(payload) two-insertvalue for `Result<Ok, Err>` (disc=0) |
| `mirICmpSltI64Line` | `(reg, lhs, rhs) -> String` | §6 | Loop-bound check `icmp slt i64` — hot-path predicate for linear scans |
| `mirICmpSgeI64Line` | `(reg, lhs, rhs) -> String` | §6 | Lower-bound check `icmp sge i64` for negative-index detection |
| `mirLinearScanLoopHeadLines` | `(headLabel, iReg, iSlot, cont, lenReg, bodyLabel, endLabel) -> String` | §6 | Loop-head block of the linear-scan idiom (label + load + slt + cond-br) — 4-line block |
| `mirLinearScanLoopTailLines` | `(contLabel, nextReg, iReg, iSlot, headLabel) -> String` | §6 | Loop-tail block of the linear-scan idiom (cont label + add + store + jump) — 4-line block |
| `mirCallVoidI64TagAndPtrLine` | `(sym, tag, slot, count) -> String` | §6 | `call void @<sym>(i64, ptr, i64)` — safepoint chunk-call shape |
| `mirAllocaI64ZeroSlot` | `(slot: String) -> String` | §6 | Canonical "fresh i64 counter slot initialised to zero" preamble for linear-scan loops |
| `mirAllocaI1FalseSlot` | `(slot: String) -> String` | §6 | Canonical "fresh i1 result slot initialised to false" preamble for IntrinsicListContains |
| `mirStoreI1TrueLine` | `(slot: String) -> String` | §6 | `store i1 true, ptr <slot>` — IntrinsicListContains match arm flip |
| `mirRuntimeDeclarePtrFromTwoPtrLine` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(ptr, ptr)` — two-ptr ABI shape (string-pair runtimes: Concat / Replace / Split, etc.) |
| `mirRuntimeDeclareI64FromTwoPtrLine` | `(sym: String) -> String` | §3 | `declare i64 @<sym>(ptr, ptr)` — i64-from-two-ptr ABI (strings.Compare, bytes.IndexOf) |
| `mirRuntimeDeclareI1FromTwoPtrLine` | `(sym: String) -> String` | §3 | `declare i1 @<sym>(ptr, ptr)` — i1-from-two-ptr predicate (strings.Equal, bytes.starts_with/ends_with) |
| `mirRuntimeDeclarePtrFromPtrI64I64Line` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(ptr, i64, i64)` — slice-like ABI shape (strings.Substring, list.slice) |
| `mirRuntimeDeclarePtrFromThreePtrLine` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(ptr, ptr, ptr)` — three-ptr ABI (strings.Replace) |
| `mirRuntimeDeclarePtrFromPtrPtrI64Line` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(ptr, ptr, i64)` — (ptr, ptr, i64) ABI (strings.ReplaceN) |
| `mirRuntimeDeclarePtrFromPtrI64PtrLine` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(ptr, i64, ptr)` — (ptr, i64, ptr) ABI |
| `mirRuntimeDeclarePtrFromPtrI64Line` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(ptr, i64)` — (ptr, i64) ABI (list_primitive_to_string) |
| `mirRuntimeDeclarePtrFromI64Line` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(i64)` — int-derived constructor shape |
| `mirRuntimeDeclarePtrFromI64PtrLine` | `(sym: String) -> String` | §3 | `declare ptr @<sym>(i64, ptr)` — handle-with-context constructor |
| `mirRuntimeDeclareEnumLayoutFromPtrLine` | `(sym: String) -> String` | §3 | `declare { i64, i64 } @<sym>(ptr)` — Option/Result-returning runtime (chan-recv) |
| `mirRuntimeDeclareEnumLayoutNoArgsLine` | `(sym: String) -> String` | §3 | `declare { i64, i64 } @<sym>()` — zero-arg Option/Result-returning runtime (cancel-check) |
| `mirRuntimeDeclareBytesV1GetLine` | `(sym: String) -> String` | §3 | `declare void @<sym>(ptr, i64, ptr, i64)` — bytes-v1 list-get/insert |
| `mirRuntimeDeclareBytesV1PushLine` | `(sym: String) -> String` | §3 | `declare void @<sym>(ptr, ptr, i64)` — bytes-v1 list-push |
| `mirRuntimeDeclareBytesV1SetWithBarrierLine` | `(sym: String) -> String` | §3 | `declare void @<sym>(ptr, i64, ptr, i64, ptr)` — bytes-v1 list-set + GC barrier |
| `mirRuntimeDeclareThreePtrVoidLine` | `(sym: String) -> String` | §3 | `declare void @<sym>(ptr, ptr, ptr)` — used by `osty_rt_test_snapshot` |
| `mirRuntimeDeclareTaskGroupSplitLine` | `(sym: String) -> String` | §3 | `declare void @<sym>(ptr, ptr, ptr, i64, ptr)` — task-group spawn 5-arg ABI |
| `mirSubI64MinusOneLine` | `(reg: String, lenReg: String) -> String` | §6 | `<reg> = sub i64 <len>, 1` — len-1 step for IntrinsicListLast / IntrinsicListPop |
| `mirAddI64PlusOneLine` | `(reg: String, iReg: String) -> String` | §6 | `<reg> = add i64 <i>, 1` — increment for linear-scan loop tails |
| `mirLenGuardLines` | `(lenReg, isEmpty, lenSym, listReg) -> String` | §6 | "Is the list non-empty?" preamble: len call + eq-zero check (2-line block) |
| `mirCallVoidPtrLine` | `(sym: String, ptr: String) -> String` | §6 | `call void @<sym>(ptr <ptr>)` — single-ptr-arg side-effect call |
| `mirCallValueI64FromPtrLine` | `(reg, sym, ptr) -> String` | §6 | `<reg> = call i64 @<sym>(ptr <ptr>)` — single-handle scalar probe |
| `mirCallValuePtrFromPtrLine` | `(reg, sym, ptr) -> String` | §6 | `<reg> = call ptr @<sym>(ptr <ptr>)` — single-handle ptr-returning call |
| `mirCallValueI1FromPtrLine` | `(reg, sym, ptr) -> String` | §6 | `<reg> = call i1 @<sym>(ptr <ptr>)` — single-handle predicate call |
| `mirRuntimeDeclareI8FromPtrI64Line` | `(sym: String) -> String` | §3 | `declare i8 @<sym>(ptr, i64)` — byte-typed list/bytes element-get shape |
| `mirCallValueI8FromPtrI64Line` | `(reg, sym, ptr, idx) -> String` | §6 | `<reg> = call i8 @<sym>(ptr <ptr>, i64 <idx>)` — paired with i8 declare |
| `mirCallValueElemFromPtrI64Line` | `(reg, elemLLVM, sym, ptr, idx) -> String` | §6 | Generalises i8 form for any element type — typed-element list-get runtime call |
| `mirAbortPrintfExitLines` | `(fmtSym, messagePtr, nextLabel) -> String` | §6 | Canonical "printf+exit+unreachable+next-label" 4-line block — drains every testing-abort / bench-error error-trap body |
| `mirBranchToErrorTrapLines` | `(isErr, errLabel, okLabel) -> String` | §6 | Result-Err gate: cond-br + err-label header — pairs with `mirAbortPrintfExitLines` |
| `mirNoneBranchLines` | `(noneLabel, optLLVM, destSlot, endLabel) -> String` | §6 | Option-miss branch (label + zeroinit + br) — 3-line block |
| `mirSomeBranchLines` | `(someLabel, taggedReg, filledReg, optLLVM, payloadI64, destSlot, endLabel) -> String` | §6 | Option-hit branch (label + Some + store + br) — 5-line block |
| `mirSomeNoneJoinLines` | `(endLabel) -> String` | §6 | Convergence-label pass-through (named for grep-ability) |
| `mirCallExitOneLine` / `mirCallExitZeroLine` | `() -> String` | §6 | Canonical exit(1) / exit(0) line specialisations |
| `mirAbortBlockLines` | `(errLabel, fmtSym, messagePtr, nextLabel) -> String` | §6 | Full abort block (label + printf + exit + unreachable + next-label) — 5-line block |
| `mirCallI64FromTwoPtrLine` / `mirCallI1FromTwoPtrLine` / `mirCallPtrFromTwoPtrLine` / `mirCallVoidFromTwoPtrLine` | `(reg?, sym, left, right) -> String` | §6 | Two-ptr-arg runtime call shapes (Compare / Equal / Concat / void-side-effect) |
| `mirCallVoidFromThreePtrLine` | `(sym, a, b, c) -> String` | §6 | Three-ptr-arg side-effect call (test_snapshot) |
| `mirInsertValueI64IndexLine` / `mirExtractValueI64IndexLine` | `(reg, aggTy, base, val, idx) -> String` | §6 | i64-typed insertvalue/extractvalue specialisations for Option/Result disc + payload |
| `mirCallVoidI64Line` / `mirCallVoidI32Line` | `(sym, arg) -> String` | §6 | Single-scalar-arg side-effect calls |
| `mirGEPInboundsI64IdxLine` | `(reg, elemTy, basePtr, idx) -> String` | §6 | i64-indexed inbounds GEP — vector-list per-element load/store |
| `mirZExtToI64Line` / `mirSExtToI64Line` / `mirBitcastToI64Line` / `mirPtrToInt64Line` | `(reg, fromTy?, val) -> String` | §6 | i64-payload widen specialisations for Option/Result aggregate construction |
| `mirCallStringConcatLine` / `mirCallStringEqualLine` / `mirCallStringCompareLine` / `mirCallStringSubstringLine` | `(reg, left, right, ...) -> String` | §6 | Literal-symbol specialisations for the String runtime ABI most-called sites |
| `mirCallListNewLine` / `mirCallMapNewLine` / `mirCallSetNewLine` | `(reg, ...) -> String` | §6 | Constructor calls for `[]` / `{:}` / `{}` literals |
| `mirSizeOfDoubleLine` / `mirSizeOfI64Line` / `mirSizeOfI32Line` / `mirSizeOfI8Line` / `mirSizeOfPtrLine` / `mirSizeOfI1Line` | `() -> String` | §6 | Canonical sizeof literal returns — centralise the byte-width constants |

| `mirCallVoidPtrI64Line` | `(sym, ptr, idx) -> String` | §6 | `call void @<sym>(ptr <ptr>, i64 <i>)` — (ptr, i64) side-effect call shape (list_pop_n, list_splice_at) |
| `mirCallValuePtrI64Line` | `(reg, retTy, sym, ptr, idx) -> String` | §6 | Typed-return sibling of `mirCallVoidPtrI64Line` |
| `mirCallVoidSelectSendLine` | `(sym, builder, ch, elemLLVM, val, arm) -> String` | §6 | Typed-element select-send call (5-arg shape) |
| `mirCallVoidSelectSendBytesLine` | `(sym, builder, ch, slot, size, arm) -> String` | §6 | Bytes-v1 select-send call (5-arg shape with size) |
| `mirRuntimeDeclareSelectSendLine` | `(sym, elemLLVM) -> String` | §3 | `declare void @<sym>(ptr, ptr, <elem>, ptr)` — typed select-send decl |
| `mirRuntimeDeclareSelectSendBytesLine` | `(sym) -> String` | §3 | `declare void @<sym>(ptr, ptr, ptr, i64, ptr)` — bytes-v1 select-send decl |
| `mirCallVoidArgValueLine` | `(sym, argLLVM, val) -> String` | §6 | `call void @<sym>(<argLLVM> <val>)` — single-typed-arg side-effect call |
| `mirRuntimeDeclareVoidSingleArgLine` | `(sym, argLLVM) -> String` | §3 | Matching decl for single-typed-arg side-effect call |
| `mirCallValueListLenLine` / `mirCallValueMapLenLine` / `mirCallValueSetLenLine` | `(reg, handle) -> String` | §6 | Canonical container-len call specialisations (literal symbol per container kind) |
| `mirCallValueChanRecvLine` | `(reg, sym, ch) -> String` | §6 | Typed `{ i64, i64 }` chan-recv call shape |
| `mirCallValueCancelCheckLine` | `(reg) -> String` | §6 | `osty_rt_cancel_check_cancelled()` — `?`-style cancellation propagation |
| `mirCallValueCancelIsCancelledLine` | `(reg) -> String` | §6 | `osty_rt_cancel_is_cancelled()` — boolean predicate |
| `mirSpillThenSizeOfLines` | `(slot, ty, val, gepReg, sizeReg) -> String` | §6 | spill + sizeof preamble (4-line block) |
| `mirCallValueListSortedLine` / `mirCallValueMapKeysSortedLine` | `(reg, sym, handle) -> String` | §6 | Sorted-allocator call specialisations |
| `mirCallVoidListPushTypedLine` | `(sym, list, elemLLVM, val) -> String` | §6 | Typed-element list-push call |
| `mirRuntimeDeclareListPushTypedLine` | `(sym, elemLLVM) -> String` | §3 | Typed-element list-push decl |
| `mirCallVoidChanCloseLine` | `(ch) -> String` | §6 | `osty_rt_chan_close` literal-symbol specialisation |
| `mirCallVoidCancelCancelLine` / `mirCallVoidYieldLine` | `() -> String` | §6 | No-arg concurrency runtime calls |
| `mirCallValueListReversedLine` / `mirCallVoidListReverseLine` | `(reg, list)/(list) -> String` | §6 | List reverse: allocator vs in-place |
| `mirCallVoidListClearLine` / `mirCallVoidMapClearLine` / `mirCallVoidSetClearLine` | `(handle) -> String` | §6 | Container clear specialisations |
| `mirCallVoidPopDiscardLine` | `(list) -> String` | §6 | `osty_rt_list_pop_discard` literal-symbol specialisation |
| `mirCallValueIsEmptyLine` | `(reg, sym, handle) -> String` | §6 | Generic is-empty predicate probe |
| `mirCallValueListGetTypedLine` / `mirCallValueListSlowGetLine` | `(reg, elemLLVM, sym, list, idx) -> String` | §6 | Synonyms of `mirCallValueElemFromPtrI64Line` named for the linear-scan body / vector-list slow-path read sites |
| `mirCallValueBenchTargetNsLine` / `mirCallValueBenchNowNanosLine` / `mirCallValueGcDebugAllocatedBytesLine` | `(reg) -> String` | §6 | Bench harness probe / clock / GC-counter call shape specialisations |
| `mirCallVoidOptionUnwrapNoneLine` / `mirCallVoidResultUnwrapErrLine` / `mirCallVoidExpectFailedLine` | `() -> String` | §6 | Panic-helper specialisations for Option / Result unwrap and testing.expect* failure |
| `mirCallValueRuntimeProbe` / `mirCallStmtRuntimeProbe` | `(reg?, sym, argList) -> String` | §6 | Generic `{ i64, i64 }` Option/Result-returning runtime hook shapes |
| `mirCallValueOpaqueLine` / `mirCallVoidOpaqueLine` | `(reg?, sym, argList) -> String` | §6 | ptr-returning generic call shapes for FnConst-thunk trampolines |
| `mirCommentBlockHeader` / `mirCommentSourceLine` / `mirSectionSeparator` | `(text?) -> String` | §6 | LLVM-text comment / annotation builders for emit-section markers |
| `mirIntegerWidenZExt{I8,I16,I32,I1}Line` | `(reg, val) -> String` | §6 | i64-payload widen specialisations for Some/Ok aggregate construction |
| `mirIntegerNarrowTruncI64{,ToI1,ToI8,ToI16,ToI32}Line` | `(reg, val) -> String` | §6 | i64-payload narrow specialisations for non-i64 element widths |
| `mirBitcastI64ToDoubleLine` / `mirIntToPtrI64Line` | `(reg, val) -> String` | §6 | Float / RawPtr payload narrows |
| `mirStoreI64Line` / `mirStoreI1Line` / `mirStorePtrTypedLine` | `(val, slot) -> String` | §6 | Common-width store specialisations |
| `mirLoadI64Line` / `mirLoadI1Line` / `mirLoadPtrLine` / `mirLoadDoubleLine` | `(reg, ptr) -> String` | §6 | Common-width load specialisations |
| `mirGEPI64StrideLine` / `mirGEPDoubleStrideLine` / `mirGEPPtrStrideLine` | `(reg, basePtr, idx) -> String` | §6 | Hot-loop GEP specialisations for List<Int> / List<Float> / List<Ptr> |
| `mirBrUncondToHeadLine` / `mirBrUncondToEndLine` | `(label) -> String` | §6 | Linear-scan loop tail / exit branch synonyms |
| `mirPhiI64FromTwoLine` / `mirPhiPtrFromTwoLine` / `mirPhiI1FromTwoValuesLine` / `mirPhiDoubleFromTwoLine` | `(reg, v1, l1, v2, l2) -> String` | §6 | Type-specialised two-arm phi shapes |
| `mirSelectI64Line` / `mirSelectPtrLine` / `mirSelectI1Line` / `mirSelectDoubleLine` | `(reg, cond, l, r) -> String` | §6 | Type-specialised select shapes for branchless minmax / clamp / Option payload merge |
| `mirInBoundsLines` | `(nonNeg, inUpper, inBounds, idx, lenReg) -> String` | §6 | "non-neg AND in-upper" 3-line bounds check |
| `mirOutOfBoundsTrapLines` | `(oobSym) -> String` | §6 | OOB-abort body (call+unreachable) — 2-line block |
| `mirIncrementI64Line` / `mirDecrementI64Line` | `(reg, iReg, delta) -> String` | §6 | Loop-counter increment / decrement synonyms |
| `mirReturnI64Line` / `mirReturnPtrLine` / `mirReturnI1Line` / `mirReturnDoubleLine` | `(val) -> String` | §9 | Type-specialised value-return terminators |
| `mirRuntimeDeclareNoReturnVoidNoArgsLine` / `mirRuntimeDeclareNoReturnColdVoidNoArgsLine` | `(sym) -> String` | §3 | Panic-helper / abort-trap canonical declares (with optional cold attribute) |
| `mirIndirectCallVoidLine` / `mirIndirectCallValueLine` | `(reg?, callType, fnPtrReg, argList) -> String` | §6 | Closure / fn-pointer indirect call synonyms |
| `mirStoreFromOperandLine` / `mirLoadIntoOperandLine` | `(ty, val, slot)/(reg, ty, slot) -> String` | §6 | Synonyms for MIR's `lowerExprInto` / `evalOperand` shapes |
| `mirCallValueStringLenLine` / `HashLine` / `IsEmptyLine` | `(reg, sReg) -> String` | §6 | String runtime ABI canonical specialisations |
| `mirCallValueStringTrimLine` / `ToUpperLine` / `ToLowerLine` | `(reg, [sym], sReg) -> String` | §6 | String case / trim runtime calls |
| `mirCallValueStringStartsWith/EndsWith/ContainsLine` | `(reg, sReg, needleReg) -> String` | §6 | String-pair predicate runtime shapes |
| `mirCallValueStringIndexOf/LastIndexOfLine` | `(reg, sReg, needleReg) -> String` | §6 | String-search i64-returning shapes |
| `mirCallValueStringSplit/Join/Replace/Repeat/DiffLinesLine` | `(reg, args...) -> String` | §6 | String runtime ABI builders for collection-returning / multi-arg cases |
| `mirCallValueBytesLen/Get/IndexOf/LastIndexOf/Contains/StartsWith/EndsWith/SubstringLine` | `(reg, args...) -> String` | §6 | Bytes runtime ABI canonical specialisations |
| `mirCallValueListSliceLine` / `Map/Filter/FoldLine` | `(reg, args...) -> String` | §6 | List combinator runtime entrypoints |
| `mirCallValueMapValuesLine` / `EntriesLine` / `mirCallVoidMapMergeWithLine` | `(reg, args...) -> String` | §6 | Map iteration / merge runtime shapes |
| `mirCallValueSetContainsLine` / `mirCallVoidSetAdd/RemoveLine` / `mirCallValueSetToListLine` | `(reg?, args...) -> String` | §6 | Set runtime ABI canonical specialisations |
| `mirCallValueListDataNoAliasLine` / `mirCallValueListLenWithScopeLine` | `(reg, [sym], list, scopeRef) -> String` | §6 | Vector-list snapshot data / len call shapes (alias-scope tagged for LICM hoist) |
| `mirCallValueStringConcatChainLine` / `mirCallValueListGetSliceLine` | `(reg, args...) -> String` | §6 | Synonyms for the interpolation-chain concat / IndexExpr-with-Range slice paths |
| `mirIntLiteralI64` / `I32` / `I8` / `I1` | `(digits) -> String` | §6 | Canonical typed-integer literal tokens for runtime call arg lists |
| `mirPtrLiteralLine` / `mirPtrNullLiteral` | `(symbol)/() -> String` | §6 | Ptr-typed literal token + canonical `ptr null` constant |
| `mirDoubleLiteralLine` | `(digits) -> String` | §6 | Double-typed literal token |
| `mirCallVarargPrintfFourArgLine` / `FiveArgLine` / `SixArgLine` | `(fmtSym, a1..aN) -> String` | §6 | 4/5/6-arg printf shapes — drains inline arg-list concat |
| `mirCallVoidTestingAbortLine` / `ContextEnterLine` / `ContextExitLine` | `([msgPtr]/[name]/) -> String` | §6 | Testing context-stack / abort runtime helper shapes |
| `mirCallValueTestingExpectOkLine` / `ExpectErrorLine` | `(reg, resultReg) -> String` | §6 | Result<T,E> assertion predicates (i1-returning) |
| `mirGCRootSlotsAllocaLine` / `mirGCRootSlotStoreLine` | `(slotsPtr, count)/(slotPtr, slotsPtr, idx, addr) -> String` | §6 | Safepoint chunk slots-array allocation + per-slot store builders |
| `mirCommentNoteLine` / `mirCommentTodoLine` | `(text) -> String` | §6 | NOTE / TODO comment helpers for emit-pass leave-behinds |
| `mirZeroOfType` / `mirOneOfType` | `(llvmTy) -> String` | §6 | Canonical zero / one literal text for any LLVM scalar type |
| `mirIndentedLine` / `mirRawLine` | `(body)/(line) -> String` | §6 | 2-space indent wrapper / raw passthrough for pre-formatted lines |
| `mirFnAttrInlineHint` / `AlwaysInline` / `NoInline` / `Hot` / `Cold` / `Pure` / `NoUnwind` / `WillReturn` / `MemoryRead` / `MemoryWrite` / `NoReturn` | `() -> String` | §6 | LLVM fn-attribute single-token returns (centralised so style changes touch one place) |
| `mirLinkageInternal` / `Private` / `External` | `() -> String` | §6 | LLVM linkage tag tokens |
| `mirUnnamedAddrTag` / `mirConstantTag` / `mirGlobalTag` | `() -> String` | §6 | LLVM-text constant / global / unnamed-addr tokens |
| `mirNullMDRef` / `mirZeroinitializerLiteral` / `mirUndefLiteral` / `mirPoisonLiteral` | `() -> String` | §6 | LLVM literal-constant text returns (null/zeroinitializer/undef/poison) |
| `mirCalleeFnRefText` / `mirCalleeIndirectText` | `(symbol)/(reg) -> String` | §6 | Direct fn-pointer (`@<sym>`) vs indirect (SSA-reg) callee shape rendering |
| `mirEqualsAssign` / `mirAttachComma` / `mirAttachSpace` / `mirNewline` | `() -> String` | §6 | Canonical text separators (`= `, `, `, ` `, `\n`) — centralise for future style flips |
| `mirPrivateUnnamedAddrConstantTag` / `mirInternalUnnamedAddrConstantTag` / `mirInternalGlobalTag` | `() -> String` | §6 | Composite linkage+attr tokens for global / constant declarations |
| `mirAggregateUndef` / `mirAggregatePoison` / `mirAggregateZero` | `() -> String` | §6 | Aggregate-init starter tokens (semantic aliases over `undef` / `poison` / `zeroinitializer`) |
| `mirLocalReg` / `mirGlobalSym` / `mirMetadataRef` | `(name) -> String` | §6 | LLVM-text sigil prefix helpers (`%name` / `@name` / `!name`) |
| `mirArgSlotPtr` / `I64` / `I32` / `I1` / `I8` / `Double` | `(reg) -> String` | §6 | Single-arg type-prefixed slot tokens for runtime call arg lists |
| `mirArgListTwoPtr` / `mirArgListPtrI64` / `mirArgListPtrI64I64` / `mirArgListThreePtr` | `(args...) -> String` | §6 | Common 2/3-arg list shapes for runtime call arg lists |
| `mirTypeI64` / `I32` / `I16` / `I8` / `I1` / `mirTypePtr` / `mirTypeDouble` / `mirTypeFloat` / `mirTypeVoid` | `() -> String` | §6 | LLVM type-token returns — centralise for hypothetical typed-load model port |
| `mirCallAttrTail` / `MustTail` / `NoTail` | `() -> String` | §6 | LLVM call-attribute tokens for tail-call control |
| `mirParamAttrNoAlias` / `NoCapture` / `ReadOnly` / `WriteOnly` | `() -> String` | §6 | LLVM ParamAttr tokens for `#[noalias]` / pointer-flow annotations |
| `mirICmpEq` / `Ne` / `Slt` / `Sle` / `Sgt` / `Sge` / `Ult` / `Ule` / `Ugt` / `Uge` | `() -> String` | §6 | LLVM icmp predicate tokens (signed + unsigned variants) |
| `mirFCmpOEq` / `One` / `Olt` / `Ole` / `Ogt` / `Oge` | `() -> String` | §6 | LLVM fcmp ordered-predicate tokens for `Float64` / `Float32` ops |
| `mirOpAdd` / `Sub` / `Mul` / `SDiv` / `SRem` / `UDiv` / `URem` | `() -> String` | §6 | LLVM signed / unsigned integer-arithmetic op-name tokens |
| `mirOpFAdd` / `FSub` / `FMul` / `FDiv` / `FRem` | `() -> String` | §6 | LLVM floating-point arithmetic op-name tokens |
| `mirOpAnd` / `Or` / `Xor` / `Shl` / `LShr` / `AShr` | `() -> String` | §6 | LLVM bitwise + shift op-name tokens |
| `mirCastSExt` / `ZExt` / `Trunc` / `SIToFP` / `UIToFP` / `FPToSI` / `FPToUI` / `FPExt` / `FPTrunc` / `Bitcast` / `PtrToInt` / `IntToPtr` / `AddrSpace` | `() -> String` | §6 | LLVM cast-instruction op-name tokens |
| `mirTermBr` / `Switch` / `Ret` / `Unreachable` / `Invoke` / `Resume` | `() -> String` | §6 | LLVM terminator-name tokens |
| `mirInstrAlloca` / `Load` / `Store` / `GEP` / `GEPInBounds` / `Call` / `CallVoid` / `Phi` / `Select` / `InsertValue` / `ExtractValue` / `ICmp` / `FCmp` / `AtomicRMW` / `CmpXchg` / `Fence` | `() -> String` | §6 | LLVM instruction-name tokens (centralise for future flip) |
| `mirAtomicUnordered` / `Monotonic` / `Acquire` / `Release` / `AcqRel` / `SeqCst` | `() -> String` | §6 | LLVM atomic-ordering tokens (reserved for first-class atomics) |
| `mirRetTypedLine` / `mirBrLabelLine` | `(args...) -> String` | §6 | Generic typed-value-return / unconditional-branch shapes |
| `mirSwitchHeaderLine` / `mirSwitchCaseLine` / `mirSwitchFooterLine` | `(args...) -> String` | §6 | Switch terminator emit shapes (header + case + footer) |
| `mirIntrinsicLLVM{Sqrt,FAbs,FMA}{F64,F32}` / `Sin/Cos/Tan/Log/Log2/Log10/Exp/Exp2/Pow/PowI/MinNum/MaxNum`F64 | `() -> String` | §6 | LLVM math-intrinsic name tokens |
| `mirIntrinsicLLVM{Ctlz,Cttz,Ctpop,BitReverse}I64` / `BSwap{I64,I32,I16}` | `() -> String` | §6 | LLVM bit-manipulation intrinsic names |
| `mirIntrinsicLLVM{S,U}{Add,Sub,Mul}OverflowI64` | `() -> String` | §6 | LLVM checked-arithmetic intrinsic names |
| `mirIntrinsicLLVM{S,U}{Add,Sub,Shl}SatI64` | `() -> String` | §6 | LLVM saturating-arithmetic intrinsic names |
| `mirIntrinsicLLVMMemcpy` / `Memmove` / `Memset` / `LifetimeStart` / `LifetimeEnd` / `InvariantStart` / `InvariantEnd` / `Assume` / `ExpectI1` / `StackSave` / `StackRestore` / `DbgDeclare` / `DbgValue` | `() -> String` | §6 | LLVM memory / lifecycle / debug intrinsic names |
| `mirAddIntLine` / `Sub/Mul/SDiv/SRem/And/Or/Xor/Shl/LShr/AShrIntLine` | `(reg, ty, a, b) -> String` | §6 | Generic typed integer arithmetic / bitwise / shift shapes |
| `mirFRemLine` | `(reg, ty, a, b) -> String` | §6 | Generic typed fp-remainder shape (sibling of existing FAdd/FSub/FMul/FDiv) |
| `mirCastLine` | `(reg, op, fromTy, val, toTy) -> String` | §6 | Generic cast-instruction line composer |
| `mirSIToFPI64ToDoubleLine` / `mirFPToSIDoubleToI64Line` | `(reg, val) -> String` | §6 | Common width-known cast specialisations |
| `mirSExtI{32,16,8,1}ToI64Line` / `mirZExtI{32,16,8,1}ToI64Line` / `mirTruncI64ToI{32,16,8,1}Line` | `(reg, val) -> String` | §6 | Width-known integer cast specialisations |
| `mirICmpI64{Eq,Ne,Slt,Sle,Sgt,Sge}Line` / `mirICmpPtr{Eq,Ne}Line` / `mirICmpI1{Eq,Ne}Line` | `(reg, a, b) -> String` | §6 | Width-known icmp specialisations |
| `mirFCmpDouble{OEq,One,Olt,Ole,Ogt,Oge}Line` | `(reg, a, b) -> String` | §6 | Width-known fcmp specialisations |
| `mirSubI64ImmediateLine` / `mirAddI64ImmediateLine` / `mirSRemI64Line` / `mirXorI1Line` | `(reg, args...) -> String` | §6 | Common arith / bitwise specialisations |
| `mirFAddDoubleLine` / `FSubDoubleLine` / `FMulDoubleLine` / `FDivDoubleLine` / `FRemDoubleLine` | `(reg, a, b) -> String` | §6 | Width-known double-typed fp arithmetic |
| `mirCallValueDoubleFromDoubleLine` | `(reg, sym, x) -> String` | §6 | Generic unary fp-intrinsic call shape composer |
| `mirCallValueLLVM{Sqrt,FAbs,Sin,Cos,Tan,Log,Log2,Log10,Exp,Exp2,Pow,MinNum,MaxNum}F64Line` | `(reg, args...) -> String` | §6 | LLVM math-intrinsic typed-call shapes |
| `mirCallValueLLVM{Ctlz,Cttz,Ctpop,BitReverse}I64Line` / `BSwap{I64,I32,I16}Line` | `(reg, x) -> String` | §6 | LLVM bit-manipulation typed-call shapes |
| `mirAllocaSingleLine` / `mirAllocaSingleAlignedLine` | `(reg, ty[, align]) -> String` | §6 | Generic single-slot alloca shapes |
| `mirAllocaPtrLine` / `I64Line` / `I32Line` / `I8Line` / `I1Line` / `DoubleLine` | `(reg) -> String` | §6 | Width-known typed-alloca specialisations |
| `mirCallVoidLLVMLifetime{Start,End}Line` / `mirCallVoidLLVMAssumeLine` / `mirCallValueLLVMExpectI1Line` | `(args...) -> String` | §6 | Lifetime / assume / branch-hint intrinsic call shapes |
| `mirCallVoidLLVM{Memcpy,Memmove,Memset}Line` | `(args...) -> String` | §6 | Memory-intrinsic typed-call shapes |
| `mirInternalConstantTag` / `mirPrivateConstantTag` / `mirExternalGlobalTag` / `mirExternalFnTag` | `() -> String` | §6 | Additional linkage tag combos |
| `mirGlobalStringPoolDeclLine` / `mirGlobalConstantI64DeclLine` / `mirGlobalConstantPtrDeclLine` / `mirGlobalMutableI64DeclLine` / `mirGlobalMutablePtrDeclLine` | `(sym, args...) -> String` | §6 | Global declaration shape composers |
| `mirStoreI8Line` / `I32Line` / `DoubleLine` / `FloatLine` / `mirLoadI8Line` / `I32Line` / `FloatLine` | `(val, slot)/(reg, slot) -> String` | §6 | Store / load specialisations for additional widths |
| `mirGEPI8StrideLine` / `I32StrideLine` / `I16StrideLine` / `FloatStrideLine` | `(reg, basePtr, idx) -> String` | §6 | Hot-loop GEP specialisations for additional widths |
| `mirAllocaArrayPtrLine` / `I64Line` / `I8Line` | `(reg, count) -> String` | §6 | Common typed-array alloca specialisations |
| `mirPhiI8FromTwoLine` / `I32FromTwoLine` / `FloatFromTwoLine` | `(reg, args...) -> String` | §6 | Two-arm phi specialisations for additional widths |
| `mirSelectI8Line` / `I32Line` / `FloatLine` | `(reg, cond, l, r) -> String` | §6 | Select specialisations for additional widths |
| `mirExtractValueI64Line` / `I1Line` / `PtrLine` / `DoubleLine` | `(reg, args...) -> String` | §6 | Width-tagged extractvalue specialisations (semantic aliases) |
| `mirAlignAttr` / `mirZeroAttr` / `mirRangeAttrI64` | `(args...) -> String` | §6 | LLVM attribute-text helpers |
| `mirFastMathNNan` / `NInf` / `NSz` / `Arcp` / `Contract` / `Afn` / `Reassoc` / `Fast` | `() -> String` | §6 | LLVM fastmath flag tokens |
| `mirArithNUW` / `NSW` / `Exact` | `() -> String` | §6 | LLVM integer-arith poison-flag tokens |
| `mirIntrinsicLLVMGCStatepoint` / `GCResult` / `GCRelocate` / `mirGCStatepointIDPlaceholder` | `() -> String` | §6 | Reserved gc.statepoint intrinsic names |
| `mirVisibilityDefault` / `Hidden` / `Protected` / `mirDLLImport` / `DLLExport` | `() -> String` | §6 | LLVM symbol visibility / DLL-storage tokens |

Keep this table updated as each section lands. New entries go in
insertion order so the provenance columns (`Origin §`) stay useful as
the file grows.
