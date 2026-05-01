# LIR Proto plan

Status: isolated prototype in progress. No production wiring yet.
Authoring direction: new LIR Proto shape is Osty-first in
`toolchain/lir_proto.osty`; the earlier Go prototype has been ported out and
removed so new shape lands in Osty before production wiring.

LIR Proto is the working name for a low-level prototype between
`internal/mir` and backend text emission. It starts LLVM-aware because the
current backend pressure is LLVM text generation, but it lives independently so
it can grow into a real low-level compiler layer instead of remaining a helper
package hidden under `internal/llvmgen`.

The immediate goal is to finish the prototype as an isolated path, verify it
against MIR fixtures and existing LLVM output, and wire it into production in
one deliberate switch behind an explicit gate.

## Why this exists

MIR already owns source-level lowering: pattern removal, CFG construction,
monomorphized symbols, explicit locals, projections, terminators, and
layout-aware aggregate shape. LIR Proto should not repeat that work.

The pressure point is lower than MIR. `internal/llvmgen/mir_generator.go`
currently mixes several responsibilities in one state machine:

- MIR support whitelisting and fallback decisions.
- LLVM type spelling and aggregate layout spelling.
- Runtime ABI calls for lists, maps, sets, strings, bytes, closures, GC,
  concurrency, and stdlib helpers.
- Temporary slot allocation, out-pointer ABI handling, string/type/runtime
  declaration pools, and final text rendering.

LIR Proto exists to split those low-level emission decisions into a typed,
deterministic plan before rendering LLVM text.

## Non-goals

- Do not introduce a new public compiler IR yet.
- Do not move HIR or MIR semantic lowering into LIR Proto.
- Do not add a new optimizer pipeline as part of the prototype.
- Do not require SSA, register allocation, or backend-independent codegen.
- Do not route `osty build`, `osty gen`, tests, or normal backend dispatch
  through LIR Proto until the final wiring phase.

## Naming and location

Public name in docs: `LIR Proto`.

Osty source of truth:

```text
toolchain/lir_proto.osty
```

Osty smoke/shape tests:

```text
toolchain/lir_proto_test.osty
toolchain/lir_proto_parity.osty
```

The name intentionally says `proto`. The surface is independent because the
intended future is larger than a local LLVM refactor, but `proto` keeps
production assumptions out until parity proves the shape.

Design decision: after the Phase-3 projected-result slice, LIR Proto authoring
switched to Osty-first. Existing Go prototype behavior has been ported into
`toolchain/lir_proto.osty` and the ported Go package has been removed, because
the eventual self-hosted backend should not require a second translation pass.

## Core contract

Input:

- A validated, monomorphic `*mir.Module`.
- LLVM emission options that affect text shape: package, source path, target,
  GC instrumentation, and feature gates.

Output:

- A deterministic `LirModule`.
- A renderer that can emit LLVM text from that module.
- A structured unsupported diagnostic when the selected surface is not covered.

The production emitter remains authoritative until the final wiring phase.

## One-shot wiring rule

Phases 1 through 6 may add packages, tests, fixtures, renderers, and dual
emission helpers, but they must not change normal backend dispatch.

Production wiring starts only in Phase 7, behind an explicit gate. If Phase 7
finds that a required surface is missing, the answer is to go back to the
earlier phase and complete the isolated prototype rather than partially wiring
the production path.

## Prototype shape

The first version should be boring data, not a smart graph.

```text
Module
  Target
  SourcePath
  TypeDefs
  Globals
  Functions
  RuntimeDecls
  StringPool
  Metadata

Function
  Name
  Return
  Params
  Blocks
  LocalSlots
  Attrs

Block
  Label
  Instrs
  Term

Instr
  Alloca
  Load
  Store
  Binary
  Unary
  Call
  Cast
  InsertValue
  ExtractValue
  Gep
  Comment

Term
  Br
  CondBr
  Switch
  Ret
  Unreachable
```

LLVM-specific names are acceptable in the first slices. The boundary is
independent, but the prototype should earn portability by surviving real
backend pressure rather than by abstracting too early.

Design decision: instruction and terminator nodes are typed variants from
Phase 1. There is no raw instruction text escape hatch inside function bodies;
if a new LLVM operation is needed, add a node for it and a renderer case.

Design decision: the LIR node vocabulary is sealed. External code should
construct the node types defined by `toolchain/lir_proto.osty`, not implement
its own instruction or terminator variants. New operations must be added to the
Osty surface so validation, rendering, and future lowering stay in one place.

Design decision: value names stay string-backed in Phase 1. `Operand` carries
`{Type, Value string}` rather than splitting value identity into `Reg`,
`Const`, `Global`, `Param`, and label wrapper types immediately. LIR Proto is
typed at instruction boundaries from the start, but value identity remains a
compact LLVM spelling until MIR lowering shows which value categories need
extra invariants.

Design decision: types use a hybrid representation from Phase 1:
`Type{LLVM string, Class TypeClass}`. The exact LLVM spelling remains the
source of truth for rendering and parity checks, while `Class` carries a small
semantic bucket (`void`, integer, float, pointer, aggregate, function, unknown)
for early validation and future ABI planning. Do not grow this into a full
second type system until runtime ABI lowering proves the missing invariants.

Design decision: add a Phase-1 structural validator. `lirValidateModule(module)`
checks the plan shape before rendering: missing function names, duplicate
block labels, nil instructions, missing terminators, empty operands, invalid
void positions, negative alignment, and obvious `Type{LLVM, Class}`
mismatches. It does not check MIR semantic parity, dominance, SSA use-def
rules, runtime ABI compatibility, or target data layout yet.

Design decision: MIR lowering exposes both the direct entry point
`lirLowerMirModule(cfg, mir) -> LirLowerResult` and the ported lowerer shell
`lirNewLowerer(cfg) -> LirLowerer` / `lirLowererLowerMIR(lowerer, mir)`. The
Osty lowerer keeps a defensive config snapshot while each run owns fresh
diagnostics and per-function state. `LirLowerResult` keeps the structured
module plus diagnostics so unsupported gaps, invalid inputs, and lowering bugs
can be tracked without parsing error strings.

Design decision: Phase 2 starts with the Single-Block Scalar Core. This covers
functions with exactly one MIR basic block, scalar locals/params/return slots,
`AssignInstr` without projections, `UseRV`, primitive constants, unary ops,
binary ops, and `ReturnTerm`. It deliberately does not cover branches, calls,
intrinsics, projections, aggregates, globals, imports, or runtime shims yet.
Those wider shapes must produce structured unsupported diagnostics rather than
falling through silently.

Design decision: the second Phase-2 slice extends scalar lowering to
multi-block control flow. It keeps the same scalar-only instruction and value
surface, but allows multiple MIR basic blocks and lowers `GotoTerm`,
`BranchTerm`, `SwitchIntTerm`, `ReturnTerm`, and `UnreachableTerm` into LIR
terminators. Calls, intrinsics, projections, aggregates, globals, imports, and
runtime shims remain outside the slice.

Design decision: the third Phase-2 slice adds scalar direct calls. It accepts
`CallInstr` only when the callee is a `FnRef` whose symbol is defined in the
same MIR module and whose parameters/return are scalar LIR-supported types.
Call results may be discarded or stored into a non-projected local. Indirect
calls, external/runtime/stdlib-only symbols, call-destination projections, and
argument/result casts remain unsupported until later slices.

Design decision: the fourth Phase-2 slice adds the one-argument print family:
`print`, `println`, `eprint`, and `eprintln`. Instead of mixing direct
`printf` emission with stderr-specific paths, LIR Proto lowers every supported
argument to a runtime string pointer and emits
`osty_rt_io_write(ptr text, i1 newline, i1 stderr)`. Strings pass through
directly; ints, floats, bools, chars, and bytes use the existing runtime
conversion helpers. This keeps stdout/stderr/newline behavior in one shape and
leaves aggregate formatting, bytes formatting, and full runtime intrinsic
coverage for later ABI phases.

Design decision: the sixth Phase-2 slice adds scalar cast/coercion support.
Explicit MIR `CastRV` can lower integer resize, integer-to-float,
float-to-integer, float resize, and bitcast for scalar LLVM-supported types.
Assignment and direct-call argument coercion are narrower: they only resize
within the same numeric family, such as `i32 -> i64` or `float -> double`.
Implicit cross-family coercion like `Int -> Float` remains unsupported unless
MIR carries an explicit `CastRV`, so call lowering does not invent type
semantics that the checker/MIR did not request.

Design decision: the seventh Phase-2 slice adds scalar primitive conversion
intrinsics that MIR represents as `IntrinsicInstr`: `Byte.toInt`,
`Char.toInt`, `Int.toByte`, `Int.toChar`, `Byte.toChar`, and `Char.toByte`.
These lower directly to LLVM integer casts (`zext` or `trunc`) and store into
the intrinsic destination when present. They do not introduce runtime calls,
and they stay separate from broader stdlib/runtime intrinsic coverage so this
slice remains a scalar codegen parity step rather than a runtime ABI expansion.

Design decision: the eighth Phase-2 slice adds a test-only source fixture
harness above the manual MIR fixtures. It runs small `.osty` snippets through
parse, resolve, check, IR lowering, monomorphization, and MIR lowering, then
renders the resulting MIR through both LIR Proto and the current MIR generator.
The first source fixtures cover scalar arithmetic, branch returns, same-module
direct calls, and primitive byte-to-char conversion. Source-level print parity
stays out of this slice because the current source path still emits a legacy
`printf` shape there while the manual MIR print intrinsic fixture pins the newer
`osty_rt_io_write` ABI shape.

Design decision: the ninth Phase-2 slice pins source-level print on the LIR
Proto side without changing production emission. The source fixture first
asserts that `println(7)` lowers into a MIR `IntrinsicPrintln`, then renders
that MIR through LIR Proto and checks the runtime string conversion plus
`osty_rt_io_write(ptr, i1, i1)` shape. It deliberately does not require the
current MIR generator to match this source-level print ABI yet; that mismatch
is now an explicit future wiring/ABI cleanup item rather than a hidden parity
failure.

Design decision: the first Phase-3 slice starts with tuple values only. MIR
`AggTuple` lowers to an `undef` aggregate plus a deterministic `insertvalue`
chain, and `TupleProj` reads lower through `extractvalue`. This intentionally
does not open struct fields, enum payloads, projected writes, or list/map/string
indexing yet. Tuple support is small enough to pin in all three harness layers:
direct LIR tests, current-generator parity, and source-level parity.

Design decision: the second Phase-3 slice adds plain struct values. LIR Proto
uses `mir.LayoutTable.Structs` as the source of truth for named struct type
definitions, `AggStruct` construction, and `FieldProj` reads. This keeps field
order and type spelling aligned with the current MIR generator while leaving
projected writes, enum payloads, builtin collection layouts, and runtime-backed
aggregate operations for later slices.

Design decision: the third Phase-3 slice adds projected assignment for the
static aggregate projections LIR Proto already knows how to read:
`FieldProj` and `TupleProj`. The write path mirrors the current MIR generator:
load the root aggregate, extract intermediate aggregate values along the
projection chain, rebuild from the leaf outward with `insertvalue`, and store
the rebuilt root. Runtime-backed `IndexProj`, enum payload writes, call-result
projected destinations, and intrinsic projected destinations remain outside
this slice.

The projected assignment fixtures include a three-level struct path
(`root.middle.leaf.value`) so the rebuild loop must retain the aggregate value
for each projection frame, not just the final intermediate aggregate. This
guards the exact failure mode where two-level writes appear correct while
deeper writes rebuild the wrong aggregate. `LirProjectionFrame` is part of the
Osty surface so this rebuild rule is described and exercised in
`toolchain/lir_proto.osty` / `toolchain/lir_proto_test.osty`.

Parity ownership is now also Osty-side. `toolchain/lir_proto_parity.osty`
contains the manual-MIR and source-level shape-critical fixture catalog:
scalar returns, control flow, direct calls, print runtime ABI, casts, primitive
conversions, tuple/struct aggregate reads, nested projected assignment,
projected call results, and projected intrinsic results. It also owns the
manual-fixture check surface (`lirParityCheckAllManual` and related result
helpers), so adding a new manual parity case no longer requires rebuilding a Go
test harness first.

Design decision: the fourth Phase-3 slice reuses that projected value-store
path for direct-call and scalar primitive-conversion intrinsic results. A
supported result can now target `place.field` / `place.tupleIndex` directly,
so LIR Proto matches the current generator's call-result load-modify-store
shape instead of forcing callers to materialize a temporary local first. The
first fixtures cover both same-module direct calls and `Byte.toInt()` projected
intrinsic results. Void-call destinations, runtime-backed index projections,
and broader runtime intrinsics remain deferred.

The call-result and intrinsic-result fixtures also cover the same
`root.middle.leaf.value` path as projected assignment, so every entry point
that reuses projected value-store lowering is pinned against deeper aggregate
rebuilds.

Design decision: the first Osty-first port slice mirrors the accumulated Go
prototype surface in one file: LIR model structs, renderer, validator,
runtime declaration/string-pool helpers, scalar cast/coercion planning,
aggregate projection records, result diagnostic helpers, and a self-hosted
`MirModule -> LirModule` lowerer shell. This still does not alter production
dispatch.

Design decision: the first Phase-4 slice covers the String and Bytes runtime
ABI. The intrinsic dispatch in `lirLowerMirIntrinsic` now routes
`MirIntrinsicString*` and `MirIntrinsicBytes*` (excluding the few that wrap
into Option/Result) through a generic `lirLowerMirStringRuntimeCall(symbol,
returnType, paramTypes, label)` helper. That helper coerces each argument
to its parameter LIR type, declares the runtime symbol exactly once via
`lirRuntimeDeclsDeclare`, emits the call, and stores the result through the
existing projected-assignment path so the Phase-3 destination machinery is
reused. Two operations carry custom shapes:

- `MirIntrinsicStringIsEmpty` reuses `osty_rt_strings_ByteLen` plus an
  `icmp eq i64 ..., 0` instead of a non-existent runtime helper, matching
  production. The optimiser sees a plain integer compare so chained
  `s.isEmpty()` checks fold the same way LLVM already folds them in the
  current path.
- `MirIntrinsicStringConcat` distinguishes its 2-arg case (single
  `osty_rt_strings_Concat(ptr, ptr)` call) from the N>=3 case
  (`alloca [N x ptr]` + per-slot `getelementptr inbounds` + `store ptr` +
  `osty_rt_strings_ConcatN(i64 count, ptr parts)`). The chain shape stays
  in one place so it cannot drift from the production allocator.

`lirLowerMirBinary` also intercepts `BinAdd` on two String-typed operands
and lowers it through the same Concat helper. The MIR layer's
`flattenStringConcatChain` already collapses 3+-leaf chains into
`MirIntrinsicStringConcat`, so this binary intercept only has to handle
the residual two-operand case.

Fixture coverage: `lirParityManualMIRFixtures` grows by twelve Phase-4a
fixtures pinning the runtime declare line and the call site for ByteLen,
IsEmpty, Concat (binary + ConcatN), Contains, TrimSpace, Repeat, Slice,
ReplaceAll, plus three Bytes shapes (len, concat, slice). A new Go-side
`TestLIRProtoPhase4aManualFixtureCatalog` re-parses
`toolchain/lir_proto_parity.osty`, finds each Phase-4a fixture, and asserts
the runtime symbol needles are present, so the production binary fails its
own tests if the Osty catalog is truncated or a runtime symbol is renamed
on one side without the other.

Design decision: builtin reference-shaped generics (`List<T>`, `Map<K,V>`,
`Set<T>`, `Channel<T>`, `Handle<T>`, `Box<T>`, plus `TaskGroup` and
`Select`) lower to plain `ptr` in `lirLowerMirType` via the new
`lirIsRefBuiltinTypeName` predicate. Without this, Phase-4 intrinsics like
`StringChars -> List<Char>` and `StringSplit -> List<String>` could not
store their results because the destination local type would lower to
`LirTypeInvalid`. The check is intentionally a string-prefix sniff so the
LIR Proto surface does not need its own generic type AST yet; the canonical
type names already arrive from the monomorphizer with stable spellings.

Design decision: the second Phase-4 slice covers the typed-lane List, Map,
and Set runtime ABI plus three argument-free concurrency primitives
(`ChanClose`, `Yield`, `IsCancelled`). Element-type lane resolution is the
new piece — production splits the runtime symbols by LLVM lane
(`osty_rt_list_push_<i64|i1|f64|ptr|string>`,
`osty_rt_map_insert_<i64|i1|f64|ptr|string>`,
`osty_rt_set_insert_<lane>`), so LIR Proto adds:

- `lirContainerInnerArg(typeName, prefix)` — extracts the inner argument
  string from a generic type name (`"List<Int>"` → `"Int"`).
- `lirMapKeyTypeName` / `lirMapValueTypeName` — split `Map<K, V>` on the
  outer `,`, using `lirFirstTopLevelComma` to track nested generic depth
  so `Map<String, List<Int>>` cuts cleanly at the outer comma.
- `lirContainerElemLaneLLVM(typeName)` — translates an MIR primitive type
  name to the LLVM lane suffix production already uses.
- `LirContainerReceiver` + `lirLowerMirContainerReceiver` — pulls the
  receiver register, element-type name, lane suffix, and the
  string-special-case flag out of the intrinsic in one place so each
  intrinsic lowering reads as a four-line scaffold (resolve receiver,
  resolve symbol, declare runtime, emit call + optional store).
- `lirLirTypeForLane(lane)` — reverse-translates the lane back into the
  `LirType` used to spell the runtime parameter type for the
  element-typed argument.

The runtime symbol resolvers themselves (`llvmListRuntimePushSymbolFor`,
`llvmMapRuntimeInsertSymbol`, `llvmSetRuntimeContainsSymbol`, ...) live in
`toolchain/llvmgen.osty`; the new lowerings call them directly so LIR
Proto and the production MIR generator stay on one source of truth for
runtime symbol names.

Coverage in this slice:

- List: Push, Get, Insert, Sorted (typed lane); Len, IsEmpty (Len + icmp),
  Reverse, Reversed, PopDiscard, Clear (lane-agnostic).
- Map: New, Insert, Contains, Remove (typed key lane); Len, Keys, Values,
  Clear (lane-agnostic).
- Set: Insert, Contains, Remove (typed elem lane); New, Len, ToList,
  Clear (lane-agnostic).
- Concurrency: ChanClose, Yield, IsCancelled (zero-/one-arg primitives).

Deliberately deferred to a later slice:

- Composite element types (the production bytes-v1 fallback path that
  needs `alloca` + `sizeof` per element).
- `MapGet` / `ListGet` Option-returning safe forms (need Option<T>
  construction, which is a separate Phase-4 slice).
- `MapKeysSorted` / `MapToString` / `ListToString` (per-element-kind
  formatter dispatch lives in the runtime; the LIR side just emits the
  call but currently picks the wrong helper for non-default element
  kinds).
- All channel send/receive (per-element ABI; Channel<T> sends need the
  value lane resolved off the channel's element type).
- TaskGroup / Spawn / HandleJoin / Parallel / Race / CollectAll / Select*
  (closure-env ABI; Phase 5 introduces the GC root/bind machinery these
  need to bracket the spawned closure).
- `CheckCancelled` (returns `Result<(), Error>` — Result lowering slice).
- `Sleep` (Duration argument lowering is unspecified at MIR level today).

The Phase-4 fixture catalog grows to 47 manual entries
(`lirParityManualMIRFixtures`) with 19 new Phase-4b fixtures; the existing
Go-side `TestLIRProtoManualFixtureCatalog` is the single pin so a missed
runtime symbol on either the Osty or the production side surfaces as a
test failure.

Design decision: a Phase-4 deferred slice adds Option<Int> wrapping for
the IndexOf-family intrinsics (`StringIndexOf`, `StringLastIndexOf`,
`BytesIndexOf`, `BytesLastIndexOf`). Two pieces of new infrastructure:

- **Basic-block splitting in the lowerer.** `LirMirFunctionLowerer`
  grows `currentBlockLabel`, `extraBlocks`, and `auxLabel`, plus
  `lirCutBlockBr` / `lirCutBlockCondBr` helpers that close the active
  sub-block with the requested terminator and start a fresh one. The
  MIR-block lowering loop splices `extraBlocks` ahead of the final block
  before pushing — the MIR terminator always lands on whichever sub-
  block is current at the end of the MIR block. Production uses a phi
  node here; the alloca shape that LIR Proto picks (slot in the entry
  block, store in each arm, load on the join sub-block) is equivalent
  for correctness and avoids extending the LIR instruction vocabulary
  for one site.
- **Algebraic 2-i64 type lowering.** `lirLowerMirType` now emits a
  named `%Option.<T> = type { i64, i64 }` (and the matching `%Result.*`
  / `%Maybe.*`) typedef when it sees the corresponding type-name prefix.
  `lirMangleAlgebraicTypeName` strips angle brackets / commas / spaces
  so nested generics like `Result<Option<Int>, Error>` produce a valid
  identifier without colliding. Production uses the same `{ i64, i64 }`
  shape (`mir_generator_snapshot.go::mirOkAggregateLines /
  mirNoneAggregateLines`).

`lirEmitOptionNone` / `lirEmitOptionSome` / `lirEmitResultOk` /
`lirEmitResultErr` produce the canonical insertvalue chain;
`lirWrapOptionFromI64Sentinel` glues them together with the sentinel
test for the IndexOf shape (sge 0 → present). `lirLowerMirIndexOf`
checks the destination local's type and routes Option-typed dests
through the wrapper, raw-Int dests through the bare i64 store.

Three Phase-4 fixtures pin the new shape — raw-i64 destination (no
wrap), Option<Int> destination on `String.indexOf`, Option<Int> on
`Bytes.indexOf` — through `TestLIRProtoManualFixtureCatalog`.

The remaining Option-returning safe forms (`MapGet`, `ListFirst`,
`ListLast`, `BytesGet`, `ListGet` safe form, `ListPop`,
`StringToInt`/`ToFloat` Result wrapping) need different sentinel
shapes (out-pointer + i1 present, len-bounds check, runtime parser
status, etc.) — each is its own follow-up slice that reuses the
basic-block-splitting + Option/Result aggregate helpers added here.

Design decision: a follow-up Phase-4 slice replaces the Turn-2
hardcoded `osty_rt_chan_close` symbol with the canonical
`osty_rt_thread_chan_close` from `toolchain/llvmgen.osty`'s
`llvmChanRuntime*` resolver family, then adds `MirIntrinsicChanMake`,
`ChanIsClosed`, `ChanSend` (per-element-lane resolution mirroring
List/Map/Set), and `ChanRecv` (whose runtime helper already returns
the `{i64, i64}` Option<T> aggregate, so the LIR side just declares
the lane-specific symbol and stores the call result into the dest).
Composite element types route through a structured unsupported
diagnostic — the bytes-v1 channel fallback is a separate slice
because it shares its alloca + sizeof shape with the same fallback
already deferred for List push/get.

Five Phase-4 fixtures pin the new shape: corrected `chan_close`,
`chan_make`, `chan_is_closed`, `chan_send_int` (i64 lane),
`chan_recv_int` (returns `%Option.Int`).

Design decision: a follow-up Phase-4 slice wires `String.toInt` /
`String.toFloat` through the Result<T, Error> aggregate helpers added
in the IndexOf-Option slice. `lirLowerMirStringParse` runs the
validate-call (`osty_rt_strings_IsValidInt` / `IsValidFloat`,
returning i1), branches on validity, and on the Ok arm calls
`osty_rt_strings_ToInt` / `ToFloat` (returning i64 / double),
widens / bitcasts the parsed value into the i64 payload slot, and
constructs the Ok aggregate. The Err arm constructs an Err(0)
placeholder. Production uses a phi node at the merge point; LIR Proto
keeps the alloca shape from the IndexOf slice for the same
"no-new-instruction-vocabulary" reason.

`lirParseResultPayloadAsI64` widens parsed scalars into the i64
payload slot exactly the way production's `toI64Slot` does:
- i64 passes through.
- double bitcasts to i64 (preserves the bit pattern).
- float zero-extends to double then bitcasts.
- ptr ptrtoints to i64.
- narrower ints zext to i64.

Two Phase-4 fixtures pin the shape: `string_to_int` (returns
`%Result.Int_Error`) and `string_to_float` (returns
`%Result.Float_Error` with the double-to-i64 bitcast on the Ok arm).
Together with the IndexOf-Option fixtures, this exercises every
sentinel pattern the basic-block-splitting infrastructure was added
to support: i64 sge sentinel for the IndexOf family, i1 boolean
present-flag for the parse family.

Design decision: a follow-up Phase-4 slice wires `Map.get(key) ->
Option<V>` through the same Option-aggregate machinery. Production
calls `osty_rt_map_get_<keysuf>(map, key, out_ptr) -> i1` — the
runtime memcpys the value into `out_ptr` on hit and returns true; on
miss it returns false and leaves `out_ptr` untouched. The Some arm
loads from the out-slot, widens to i64 via `lirParseResultPayloadAsI64`
(reused from the parse slice), and builds Option.Some; the None arm
builds Option.None. Both arms store into an alloca-backed merge slot.

Composite value types (struct / tuple values) need the bytes-v1
runtime fallback that List/Map/Set/Channel composite elements all
share — that's a separate slice.

Two Phase-4 fixtures pin the shape: `map_get_string_int`
(`Map<String, Int>` → `%Option.Int`, exercises the most common
string-key dispatch) and `map_get_i64_string`
(`Map<Int, String>` → `%Option.String`, exercises both i64-key and
ptr-value lane resolutions plus the ptrtoint payload coercion).

Design decision: a follow-up Phase-4 slice wires `List.first()` /
`List.last() -> Option<T>` through the same Option-aggregate
machinery. Production has no bespoke `_first` / `_last` runtime —
both compose `osty_rt_list_len` + `osty_rt_list_get_<lane>` and
gate on `len == 0` (mir_generator.go::IntrinsicListFirst|Last). LIR
Proto mirrors that composition: call len, icmp eq with 0, on
non-empty branch call get with `idx=0` (first) or `idx=len-1` (last),
widen the loaded value via `lirParseResultPayloadAsI64`, build
Option.Some; on empty branch build Option.None.

Two Phase-4 fixtures pin the shape: `list_first_int`
(`List<Int>` → `%Option.Int`, idx=0, exercises the i64 element lane)
and `list_last_float` (`List<Float>` → `%Option.Float`, idx=len-1,
exercises the f64 element lane and the sub-from-len idx
computation plus the bitcast double-to-i64 payload coercion).

Design decision: a batched Phase-4 slice covers the remaining
straightforward Option/Result-returning intrinsics in one PR:
`ListPop` (Option<T> with `len-1 → get → discard` capture order),
`BytesGet` (Option<Byte> with `idx >= 0 && idx < len` AND'd
in-bounds check), `ListToString` (per-elem-kind dispatch into
`osty_rt_list_to_string_<i64|f64|i1|char|byte|string>`),
`MapToString` and `SetToString` (both single-call entries — the
runtime carries `key_kind` / `elem_kind` set at allocation time so
the per-kind formatter is picked inside C, lowering just emits the
call), and `CheckCancelled` (`Result<(), Error>` returned as the raw
`{ i64, i64 }` aggregate from the runtime — declares and stores at
the raw type since the dest slot's named `%Result.Unit_Error` type
is structurally identical and LLVM accepts the direct store).

Six Phase-4 fixtures pin the shape: `list_pop_int`,
`bytes_get`, `list_to_string_int`, `map_to_string`,
`set_to_string`, `check_cancelled`.

This closes the bulk of the Option/Result-returning intrinsic gap.
The remaining Option-related work is the bytes-v1 fallback path for
composite element types (List/Map/Set/Channel push/get/insert all
need the same alloca + sizeof shape), plus the closure-env
intrinsics (TaskGroup/Spawn/HandleJoin/Parallel/Race/CollectAll/
Select*) that depend on the GC root-binding pass.

Design decision: a five-slice batch covers the bytes-v1 fallback
infrastructure plus several straightforward intrinsic
groups in one PR:

- **bytes-v1 fallback for List push (composite element)**:
  `lirEmitSizeOf` materialises the runtime byte size via the canonical
  `getelementptr inbounds %T, ptr null, i32 1` + `ptrtoint` idiom,
  matching production's `llvmSizeOf`. `lirLowerMirListPush` now
  detects composite element types up front (no LIR runtime lane) and
  routes through `lirLowerMirListPushBytesV1` which spills the value
  into a stack slot and calls
  `osty_rt_list_push_bytes_v1(list, slot, size)`. The other composite
  paths (List get/insert/set, Map insert/get composite value, Set
  insert/contains/remove composite elem, Channel send composite) all
  share the same shape and reuse `lirEmitSizeOf` in follow-up slices.

- **Concurrency primitives** without closure-env: `Sleep`,
  `GroupCancel`, `GroupIsCancelled` lower as plain runtime calls
  through the existing `lirLowerMirStringRuntimeCall` helper.
  `HandleJoin` reads its return type off the destination local
  (production picks the LLVM type from `i.Dest.Local.Type`).

- **`MapKeysSorted`**: per-key-lane symbol
  `osty_rt_map_keys_sorted_<i64|string>` selected via the existing
  `LirContainerReceiver` key-lane info.

- **Bytes ↔ String/List conversions**: `BytesFromString`,
  `BytesFromList` are simple ptr→ptr calls through the existing
  helper. `BytesToString` and `BytesFromHex` use the new
  `lirLowerMirBytesValidatedResult` which mirrors the
  `lirLowerMirStringParse` shape but with ptr success values
  (production:
  mir_generator.go::emitBytesValidatedResult).

Eleven Phase-4 fixtures pin every shape: `list_push_bytes_v1`,
`handle_join_int`, `group_cancel`, `group_is_cancelled`, `sleep`,
`map_keys_sorted_i64`, `bytes_from_string`, `bytes_from_list`,
`bytes_to_string`, `bytes_from_hex` (10 new + the existing
`check_cancelled` reuse).

Design decision: a follow-up batch extends the bytes-v1 fallback to
List get / List insert / Channel send composite paths, fixes the
production-divergent `Set.insert` / `Set.remove` LLVM signature
(was declared `void`, runtime is `i1` newly-added/removed flag), and
fixes `Map.set` to spill the value into a stack slot regardless of
value type — production declares `osty_rt_map_insert_<keysuf>(ptr,
<keyLLVM>, ptr) -> void` and memcpys `value_size` bytes from the
pointer, so the typed-i64 form LIR Proto used previously was both a
linker mismatch and a wrong-bitpattern bug for non-i64 values.

Four new fixtures pin the new shapes: `list_get_bytes_v1`,
`list_insert_bytes_v1`, `chan_send_bytes_v1`, `set_remove_i1`. Two
existing fixtures (`map_insert_string_int`, `set_insert_string`)
update to pin the corrected ABI. The bytes-v1 helper
`lirEmitSizeOf` is reused unchanged from the prior batch.

Set composite element types remain unsupported — production has no
bytes-v1 set runtime entry today and the runtime helper would need
a comparator callback to compare composite elements. Map composite
value types now route through the spill path correctly; Map composite
key types are still unsupported on both sides.

Design decision: a follow-up batch covers the concurrency closure-env
intrinsics — the runtime callbacks pass through as opaque ptr at the
LLVM boundary (closures lower to ptr at the function-value boundary
already), so LIR Proto can emit the per-intrinsic runtime call shape
without needing a new closure-env ABI surface. The eleven new
lowerings:

- `TaskGroup(body)` — return type read off the destination local
  (defaults to void). Matches production's
  `osty_rt_task_group_root(ptr) -> retType`.
- `Spawn(body)` (1 arg, detached) and `Spawn(group, body)` (2 args,
  group-scoped) → `osty_rt_task_spawn(ptr) -> ptr` /
  `osty_rt_task_group_spawn(ptr, ptr) -> ptr`.
- `Select(body)`, `SelectRecv(builder, ch, callback)`,
  `SelectTimeout(builder, duration, callback)`,
  `SelectDefault(builder, callback)` — all-ptr arg shapes through the
  existing simple-runtime-call helper.
- `SelectSend(builder, ch, value, arm)` — per-channel-element-lane
  dispatch like ChanSend (typed `osty_rt_select_send_<lane>` for
  scalar, `osty_rt_select_send_bytes_v1` with spill+sizeof for
  composite values).
- `Parallel(items, concurrency, f)` →
  `osty_rt_parallel(ptr, i64, ptr) -> ptr`.
- `Race(body)` returns `Result<T, Error>` as the raw
  `{ i64, i64 }` aggregate — same direct-store shape as
  `CheckCancelled`.
- `CollectAll(body)` → `osty_rt_task_collect_all(ptr) -> ptr`.

Eleven new fixtures pin every shape: `task_group_unit`,
`spawn_detached`, `spawn_grouped`, `select`, `select_recv`,
`select_send_int`, `select_timeout`, `select_default`, `parallel`,
`race`, `collect_all`.

What stays deferred: the actual GC root binding pass (the closure
envs are passed by ptr but no per-call root_bind/release pair is
emitted today), per-loop `!llvm.loop.*` metadata (needs MIR loop
classification), per-instruction `!llvm.access.group` metadata, the
Phase-7 actual runner (Osty↔Go bridge), and the Phase-8 default-on
flip.

Design decision: GC root binding lands as a frame-lifetime pass in
the MIR-function lowerer. `lirLowerMirLocals` now inspects each
local's MIR type via the new `lirTypeNameIsGCManaged(name)`
predicate (true for `String`, `Bytes`, and every reference-shaped
builtin: `List<T>` / `Map<K,V>` / `Set<T>` / `Channel<T>` /
`Handle<T>` / `Box<T>` / `TaskGroup` / `Select`); when the slot
holds a managed reference, the prologue emits
`call void @osty.gc.root_bind_v1(ptr %slot)` immediately after the
alloca and (when the local is a parameter) the param-store, then
records the slot in `LirMirFunctionLowerer.gcRootSlots`.

Each `MirTermReturn` arm in `lirLowerMirTerm` now invokes
`lirEmitGcRootReleases(l)` immediately before the `ret`, walking
`gcRootSlots` in REVERSE insertion order. That mirrors production's
LIFO discipline (`generator.go::releaseGCRoots`) so the runtime
sees the same bind/release nesting as the existing MIR emitter.

`RawPtr` is intentionally excluded from the managed predicate — it's
the user's escape hatch for foreign pointers and binding it would
attribute non-GC memory to the collector. Composite struct/tuple
locals also do not currently bind: their slot type is the aggregate
value, not a managed pointer, so they are scanned through the
`%struct.<T>` field walk inside the safepoint runtime instead of a
dedicated bind/release pair (matching production today).

Three Phase-5 fixtures pin the new shape: `gc_root_bind_release`
(single managed param + bind/release pair), `gc_root_multiple_slots`
(two managed params with explicit `%l1`/`%l2` slot names and LIFO
release order), and `gc_root_scalar_only` (scalar-only function
that must NOT emit root binding — implicitly enforced by the
catalog-shape pin since the function envelope and `ret i64` needles
match without any GC declares).

Design decision: per-loop `!llvm.loop.*` metadata lands as a paired
MIR + LIR change.

**MIR side**: `MirTerm` grows a `loopBackEdge: Bool` field
(default `false`). The HIR→MIR loop lowerers in
`toolchain/mir_lower.osty` (`mirLowerForInfinite` / `forWhile` /
`forRange` / `forInList` / `forInChannel`) replace the body-end
`mirGotoTerm(header, span)` with the new
`mirGotoBackEdgeTerm(header, span)` constructor at the five back-
edge sites — the only Goto/Branch terminators in those lowerers
that close one iteration of an enclosing loop. Other Gotos in those
loop shapes (entry → header, body → step) keep the false default
because they are loop-internal forward edges, not back-edges.

The Go-side `internal/mir/mir.go` is intentionally NOT touched —
production's MIR generator emits loop metadata via a different
mechanism (text-rewriting the most-recent `br` line in
`generator.go::attachVectorizeMD`) and does not need a back-edge
flag in MIR. Adding the flag only on the Osty side keeps the
existing Go path quiet while LIR Proto gets a structured
back-edge signal.

**LIR side**: `LirTerm` grows `loopMDRef: String` (empty by default
= no metadata). `lirRenderTerm` appends `, !llvm.loop !N` after the
base terminator text when the ref is set. `lirNextLoopMD(l, fn_)`
allocates a fresh `!N` distinct loop node plus the property nodes
the function's annotations request:

- `#[vectorize]` → `!{!"llvm.loop.vectorize.enable", i1 true}` plus
  optional `vectorize.width`, `vectorize.scalable.enable`,
  `vectorize.predicate.enable` per the LANG_SPEC v0.6 A5/A5.1
  surface.
- `#[unroll]` / `#[unroll(count = N)]` → `!{!"llvm.loop.unroll.count",
  i32 N}` (when count > 0) or `!{!"llvm.loop.unroll.enable",
  i1 true}` (bare).

`lirLowerMirTerm` for `MirTermGoto` / `MirTermBranch` checks
`term.loopBackEdge && lirFnHasLoopHints(l.fn_)` and calls
`lirNextLoopMD` to allocate the metadata, attaching the resulting
`!N` to `LirTerm.loopMDRef`. Nodes accumulate in
`LirModule.metadata` and the renderer emits them at the end of the
module text — same shape as production
(`generator.go::nextLoopMD`).

Three Phase-5 fixtures pin the new shape: `loop_md_vectorize`
(infinite loop with `vectorize=true` → back-edge carries metadata
ref), `loop_md_unroll_count` (infinite loop with `unroll=true,
unrollCount=4` → property node renders as `unroll.count`,
i32 4), and `loop_md_plain_no_md` (infinite loop without any
annotations — back-edge MUST NOT carry metadata, enforced by the
absence of `!llvm.loop` in the rendered text).

Design decision: per-instruction `!llvm.access.group` metadata for
`#[parallel]` functions builds on the loop-metadata infrastructure
above (LANG_SPEC v0.6 A6). The pair lifts the alias-analysis
restriction that would otherwise block vectorisation of memory ops
inside parallel loops.

**LIR side**: `LirInstr` grows `metadata: List<String>` (empty by
default). `lirRenderInstr` appends each entry as a comma-separated
trailer (`, !llvm.access.group !N`) after the rendered instruction
body. `LirMirFunctionLowerer` grows
`parallelAccessGroupRef: String` (empty until first use).

When lowering a function whose `MirFunction.parallel` is true,
`lirLowerMirFunction` runs a final pass —
`lirAttachParallelAccessGroup(l, blocks)` — that walks every
load/store in every block and pushes the function-wide access-group
ref onto `instr.metadata`. The ref is allocated lazily by
`lirParallelAccessGroupRef(l)`, which records a `distinct !{}` node
in `LirModule.metadata` on first call. This keeps non-parallel
functions free of any metadata churn and ensures every load/store
inside a parallel function picks up the SAME group ref (alias-set
membership is per-function in the v0.6 design).

The cross-link with loop metadata: when `MirFunction.parallel` is
true AND a back-edge is being given a `!llvm.loop` node,
`lirNextLoopMD` appends an extra property — `!{!"llvm.loop.parallel_accesses",
!N}` — pointing at the same access-group ref. LLVM's loop alias
analyser uses this property to recognise that the marked memory
ops can be reordered across loop iterations even when standard
alias analysis cannot prove independence.

The Go-side `internal/llvmgen/generator.go` is intentionally NOT
touched — production today does not emit access-group metadata
(LANG_SPEC v0.6 A6 ships with the LIR Proto migration), and the
shadow runner is gated behind Phase 7 so neither the LLVM text
diff nor the bench backstop sees the new metadata until the flip
lands.

Two Phase-5 fixtures pin the new shape:
`parallel_access_group` (one-block parallel function with a param-
to-return identity → store/load both carry `, !llvm.access.group !N`,
the function-wide `= distinct !{}` def is emitted) and
`parallel_loop_with_access_group` (parallel function with a
back-edge → access-group def + loop md `parallel_accesses`
property + load/store carry the group trailer + back-edge carries
`!llvm.loop`). A non-parallel function exercising loads/stores has
no fixture entry — the absence is enforced by the existing
`gc_root_scalar_only` and other non-parallel fixtures, none of
which contain `!llvm.access.group` in their needles.

Design decision: the loop-metadata tuning properties already wired
inside `lirNextLoopMD` (`vectorize.width`, `vectorize.scalable.enable`,
`vectorize.predicate.enable`, `unroll.enable` bare, combined
vectorize+unroll, parallel-only) get five focused fixtures so the
LANG_SPEC v0.6 A5/A5.1/A7 surface is pinned individually rather than
implicitly through the original `loop_md_vectorize` /
`loop_md_unroll_count` pair.

- `loop_md_vectorize_width` — `vectorize=true` + `vectorizeWidth=4`.
  Pins both the base `vectorize.enable` property and the
  `vectorize.width, i32 4` companion.
- `loop_md_vectorize_full` — all three vectorize tuning args at
  once (`vectorizeWidth=8`, `vectorizeScalable=true`,
  `vectorizePredicate=true`). Pins all four properties so a future
  refactor that drops one would surface here.
- `loop_md_unroll_enable_bare` — `unroll=true` + `unrollCount=0`.
  Pins the `unroll.enable, i1 true` form (the else branch of the
  unroll-count conditional) which the existing `unroll_count`
  fixture cannot cover by construction.
- `loop_md_combined_vectorize_unroll` — `vectorize=true` +
  `unroll=true, unrollCount=2`. Pins that BOTH annotations land
  in the SAME `!N = distinct !{}` loop md node when applied to
  the same loop.
- `loop_md_parallel_only` — `parallel=true` only (no vectorize,
  no unroll). Pins that `parallel_accesses` works as a standalone
  loop md property — the access-group def is still emitted, but
  no `vectorize.enable` / `unroll.*` companion properties appear.

Each fixture uses the same one-block-then-back-edge CFG as the
existing loop_md fixtures so the back-edge terminator is the only
`br` that picks up `, !llvm.loop !N`. The CFG shape stays
deliberately identical — variation lives entirely in
`MirFunction` annotation flags — so a regression in
`lirNextLoopMD` property selection surfaces in exactly one
fixture each.

Design decision: the disc-tag inspection intrinsics
(`OptionIsSome`, `OptionIsNone`, `ResultIsOk`, `ResultIsErr`)
plus `RawNull` (LANG_SPEC §19) land as a paired LIR Proto +
fixture batch. The four disc checks all reduce to the same
shape — `extractvalue %Algebraic.<...>, 0` on the i64 disc
field then `icmp ne` (present-tag) or `icmp eq` (absent-tag)
against `0` — so a single helper `lirLowerMirAlgebraicTagCheck(l,
instr, cmp, label)` covers all four; the dispatcher passes
`"ne"` for IsSome/IsOk and `"eq"` for IsNone/IsErr. `RawNull`
is the trivial constant case: no operand, no extractvalue, no
compare — just `store ptr null` into the dest local. Mirrors
production `emitOptionIntrinsic` / `emitResultIntrinsic` /
`emitRuntimeRawNull`.

Five Phase-4 fixtures pin the new shape:
`option_is_some` / `option_is_none` (each takes `Option<Int>`
param → returns Bool, pin Option.Int aggregate def + extractvalue
+ correct icmp predicate), `result_is_ok` / `result_is_err`
(each takes `Result<Int, Error>` param → returns Bool, same
shape with Result.Int_Error aggregate), and `raw_null` (no
params → returns RawPtr, pins `define ptr @rawNull()` +
`store ptr null` + `ret ptr`).

Design decision: the next batch lands four more MIR intrinsics
that previously fell through the unsupported wildcard, all of
which reduce to runtime ABI calls with no extra logic:

- `MirIntrinsicListSlice` → `osty_rt_list_slice(ptr, i64, i64)`.
  Element-agnostic (memcpy by elem_size) so a single dispatch
  case via `lirLowerMirStringRuntimeCall` (the existing
  fixed-signature runtime call helper) covers every `List<T>`.
- `MirIntrinsicListToSet` → per-element-lane runtime
  (`osty_rt_list_to_set_<i64|i1|f64|ptr|string>`). Picked by
  `llvmListRuntimeToSetSymbol(elemLLVM, isString)` from
  `toolchain/llvmgen.osty` — same selector production uses.
  A small `lirLowerMirListToSet` helper extracts the element
  type via `lirLowerMirContainerReceiver` then emits one ptr
  call.
- `MirIntrinsicStringFields` → `osty_rt_strings_Fields(ptr) → ptr`.
  Whitespace-split-into-words: trivial 1-arg ptr call.
- `MirIntrinsicStringSplitN` → `osty_rt_strings_SplitN(ptr, ptr, i64) → ptr`.
  Limited-N split: 3-arg call returning `List<String>`.

Five Phase-4 fixtures pin each shape:
`list_slice` (List<Int> sliced [0..3) → pins runtime decl + call
+ `ret ptr`), `list_to_set_i64` and `list_to_set_string` (the
two most common `llvmListRuntimeToSetSymbol` branches —
enforces both the scalar and string-lane symbols are emitted
correctly), `string_fields` (no-arg `Fields` call returning a
List<String>), and `string_split_n` (3-arg `SplitN` call with
String/String/Int param signature).

Design decision: the next batch lands the four Unwrap variants
(`OptionUnwrap` / `OptionUnwrapOr` / `ResultUnwrap` /
`ResultUnwrapOr`) plus two new lowering helpers they share:

- `lirNarrowI64ToType(l, i64Reg, target)` — inverse of
  `lirParseResultPayloadAsI64`. Casts the i64-encoded payload
  back to the declared payload type via `bitcast` (double),
  `inttoptr` (ptr), `trunc` (sub-word int), or pass-through
  for i64. Mirrors production `fromI64Slot`.
- `lirCutBlockUnreachable(l, nextLabel)` — counterpart of
  `lirCutBlockBr` / `lirCutBlockCondBr` for diverging arms. The
  unwrap-on-absent panic helpers (`osty_rt_option_unwrap_none` /
  `osty_rt_result_unwrap_err`) are noreturn so the absent arm's
  block must terminate with `unreachable` rather than fall
  through to a branch.

`lirLowerMirAlgebraicUnwrap(l, instr, abortSym, label)` covers
both Option.unwrap and Result.unwrap — the sole variation is
the panic-helper symbol (`osty_rt_option_unwrap_none` vs
`osty_rt_result_unwrap_err`). `lirLowerMirAlgebraicUnwrapOr(l,
instr, label)` covers both fallback variants — the absent arm
evaluates `instr.args[1]` (the fallback operand) instead of
calling a panic helper, and merges through the same alloca-backed
slot pattern already used by `lirWrapOptionFromI64Sentinel` and
`lirLowerMirListFirstOrLast`. Production uses a phi node here;
the alloca shape is equivalent and matches existing LIR Proto
sites.

Four Phase-4 fixtures pin the new shapes, each scoped to scalar
Int payload (the simplest narrowing path — pass-through):
`option_unwrap` / `result_unwrap` pin disc extract + icmp eq +
panic helper decl/call + unreachable + payload extract + ret;
`option_unwrap_or` / `result_unwrap_or` pin alloca-merge slot +
both arm stores + ret. The narrowing helper's other branches
(double / ptr / sub-word) stay covered by the existing IndexOf
/ FirstOrLast / parse fixtures that already exercise the
i64-payload boxing direction.

## Phase 0: lock the boundary

Deliverables:

- This plan document.
- A short design note in the first LIR Proto PR stating that LIR Proto starts
  below MIR and above LLVM text.
- No code path changes.

Exit criteria:

- The scope is documented.
- The final production switch is explicitly deferred.

## Phase 1: data model and renderer shell

Deliverables:

- Add `toolchain/lir_proto.osty` as the Osty-owned source for the data model,
  validator, renderer surface, and MIR-lowering shape.
- Add a renderer that can print an empty module, a simple function shell, type
  declarations, runtime declarations, and a string pool.
- Model function-body instructions and terminators as typed variants from the
  first slice.
- Add a shallow structural validator for the Phase-1 model.
- Add focused unit tests for deterministic ordering.

Coverage target:

- No MIR lowering yet.
- Construct `LirModule` by hand in tests.

Exit criteria:

- The renderer is deterministic.
- The validator catches malformed hand-built plans without becoming a full
  backend verifier.
- No import of `internal/llvmgen`.
- No dependency on `internal/ast`, `internal/resolve`, or `internal/check`.

## Phase 2: scalar MIR lowering

Deliverables:

- Expand `lirLowerMirModule(cfg, mir)` beyond the Phase-1 envelope shell.
- Keep `toolchain/lir_proto.osty` as the implementation source for each slice
  so the self-hosted backend surface does not lag behind the design.
- First slice: Single-Block Scalar Core for scalar locals, params, returns,
  primitive constants, unary/binary ops, and `ReturnTerm`.
- Second slice: multi-block scalar control flow through `GotoTerm`,
  `BranchTerm`, `SwitchIntTerm`, `ReturnTerm`, and `UnreachableTerm`.
- Third slice: scalar `CallInstr` lowering for same-module direct `FnRef`
  targets.
- Fourth slice: one-argument print-family intrinsics via runtime string
  conversion plus `osty_rt_io_write`.
- Fifth slice: a small test-only parity harness that renders the same manual
  MIR fixture through both LIR Proto and the current MIR generator, then checks
  the shape-critical LLVM snippets both must contain. This is intentionally not
  a full textual diff yet because header order, temp names, and declaration
  placement still differ.
- Sixth slice: scalar `CastRV` plus conservative same-family assignment and
  direct-call argument coercion for integer and float resize operations.
- Seventh slice: scalar primitive conversion intrinsics for byte/char/int
  widening and narrowing through LLVM integer casts.
- Eighth slice: source-level scalar parity fixtures that drive real `.osty`
  snippets through the front-end and MIR lowering before comparing LIR Proto
  against the current MIR generator.
- Ninth slice: a source-level print fixture that proves `println` reaches MIR
  as `IntrinsicPrintln` and that LIR Proto renders the newer runtime I/O ABI
  without changing the production generator yet.
- Later scalar slices: broader source-level fixture comparison once the scalar
  surface stops moving.

Coverage target:

- Primitive numeric/bool/string/unit functions.
- No aggregates, lists, maps, closures, GC, or stdlib runtime shims.

Exit criteria:

- Scalar functions round-trip through `MIR -> LIR Proto -> LLVM text`.
- Unsupported shapes return structured diagnostics.
- Existing production `GenerateFromMIR` remains untouched.

## Phase 3: layouts, projections, and aggregate values

Deliverables:

- First slice: tuple aggregate construction and tuple element reads through
  `insertvalue` / `extractvalue`, with direct MIR, parity, and source fixtures.
- Second slice: plain struct aggregate construction and field reads through
  `mir.LayoutTable.Structs`, with direct MIR, parity, and source fixtures.
- Third slice: nested `FieldProj` / `TupleProj` projected assignments through
  load-modify-store aggregate rebuilding, with direct MIR, parity, and source
  fixtures, including a three-level nested struct write.
- Fourth slice: projected direct-call and scalar primitive-conversion intrinsic
  result writes through the same aggregate rebuilding helper, with direct MIR,
  parity, and source fixtures, including a three-level nested destination.
- Add struct, tuple, enum, option/result, and interface-shaped value support.
- Add projection lowering for field, tuple, variant payload, and map/list/string
  index reads that are already supported by MIR direct emission.
- Add type definition interning and stable type-name rendering.

Coverage target:

- The aggregate surfaces currently green in MIR direct tests.
- No new semantic behavior beyond the current MIR emitter.

Exit criteria:

- Snapshot diffs are limited to harmless register/name ordering or are
  explicitly normalized.
- Every aggregate feature has at least one direct MIR fixture and one source
  level smoke fixture where practical.

## Phase 4: runtime ABI surfaces

Deliverables:

- Move low-level runtime call planning into LIR Proto:
  list, map, set, string, bytes, option/result helpers, stdlib shims,
  concurrency intrinsics, closure env calls, and raw runtime helpers.
- Represent out-pointer and temporary-slot requirements in the plan instead of
  ad-hoc string emission.
- Add runtime declaration and ABI argument tests.

Coverage target:

- All runtime ABI surfaces needed by the current MIR-first path.
- Explicit tests for composite list/map value paths and closure env ABI.

Exit criteria:

- The plan can explain every required runtime declaration before rendering.
- Composite value paths do not rely on string-pattern side effects.
- Existing MIR-first tests can run against the prototype renderer in a
  non-production test helper.

## Phase 5: GC, metadata, and optimization hints

Deliverables:

- Add GC root binding, safepoint planning, release ordering, and loop back-edge
  safepoints.
- Add loop metadata, vectorization, unroll, parallel access groups, function
  attributes, target features, noalias, hot/cold, and pure hints.
- Add metadata ordering tests.

Coverage target:

- The `Options.EmitGC` surface currently implemented by MIR direct emission.
- v0.6 function and loop attribute surfaces already represented in MIR.

Exit criteria:

- GC and metadata output is deterministic.
- Return-value load and root release ordering matches current behavior.
- The plan remains render-only after construction.

Design decision: the first Phase-5 slice covers function-entry GC
safepoints plus the always-on function/parameter attribute surface
(`#[inline(always|never)]`, `#[hot]`, `#[cold]`, `#[pure]`,
`#[target_feature]`, `#[noalias]`). MIR already carries every flag on
`MirFunction` (toolchain/mir.osty:1090–1104), so this slice just
translates them into LLVM strings:

- `lirEmitGcSafepoint(l, kind)` records `osty.gc.safepoint_v1` exactly
  once per module and pushes a `call void @osty.gc.safepoint_v1(i64 id,
  ptr null, i64 0)` whose `id` is `llvmEncodeSafepointId(kind, serial)`.
  Per-function `LirMirFunctionLowerer.safepointSerial` bumps so each
  emit gets a fresh low-56-bit slot while sharing the high-8 kind
  byte, matching production's encoding (mir_generator_test.go:3469
  pins the entry kind id `72057594037927936 = 1 << 56`).
- `lirLowerMirFunction` emits one entry safepoint at the very front of
  the prologue — before any local `alloca` / projection store — so
  every entered function can be observed by the runtime regardless of
  which MIR block is `fn_.entry`.
- `lirFnAttrsFromMir(fn_)` derives the function-header attribute list:
  `inlinehint` / `alwaysinline` / `noinline` from `MirInlineMode`,
  `hot` / `cold` from the matching flags, `readnone` from `pure`, and
  `"target-features"="+f1,+f2"` from `targetFeatures`.
- `lirParamAttrsFromMir(fn_, paramType)` adds `noalias` to every `ptr`
  parameter when `noaliasAll` is set; the per-param `#[noalias(p1, p2)]`
  selector form is deferred because MIR drops the per-name metadata
  today.

The matching parity fixtures (`entry_safepoint`, four `fn_attr_*`)
pin the rendered LLVM line shape from Go via
`TestLIRProtoManualFixtureCatalog`, so a regression in the runtime
declare line, the entry id encoding, or any attribute spelling
surfaces without needing a production wiring switch.

Deliberately deferred to a later Phase-5 slice:

- Per-call follow-up safepoints around runtime calls (need root
  visibility tracking before the empty-roots form is replaced).
- GC root binding / release pairs (`osty.gc.root_bind_v1` /
  `osty.gc.root_release_v1`) — depend on the same root-visibility pass.
- Per-instruction `!llvm.access.group` and per-loop `!llvm.loop.*`
  metadata — need MIR loop-back-edge classification before LIR can
  attach the metadata to the right `br` terminator.
- `#[noalias(p1, p2)]` selector form — MIR needs to preserve the
  per-parameter name list first.
- Per-loop `vectorize.enable` / `unroll.enable` / `unroll.count`
  metadata — the `MirFunction.vectorize` / `unroll` flags arrive but
  there is no MIR loop tag yet to attach them to.

## Phase 6: full parity harness

Deliverables:

- Add test-only dual emission:

```text
MIR -> current GenerateFromMIR
MIR -> LIR Proto -> LLVM text
```

- Normalize harmless SSA/temp names where necessary.
- Run the current MIR direct LLVM fixture set through both paths.
- Track unsupported gaps in one table inside the test output or a helper file.

The first shadow slice is deliberately narrower than full dual emission.
`toolchain/lir_proto_parity.osty` marks source fixtures that the current
generator should already satisfy with the `current-generator` tag.
`internal/llvmgen` parses that Osty-owned catalog, lowers each embedded source
through the normal front-end, IR, monomorphization, and MIR pipeline, runs
`GenerateFromMIR`, and checks the fixture needles. Fixtures such as
`source_println_int_runtime_abi` stay tagged `lir-only` until the current
generator and LIR Proto runtime ABI intentionally converge.

Coverage target:

- The current MIR-first support whitelist.
- The Stage 5 parity gaps called out in `docs/mir_design.md`.

Exit criteria:

- The prototype is either clean for the selected surface or has a small,
  explicit, documented gap list.
- No production code calls the prototype yet.

Design decision: the catalog-shape Phase-6 slice is a single Go-side test
(`TestLIRProtoFixtureCatalogShape` in
`internal/llvmgen/lir_proto_shadow_parity_test.go`). It walks every parity
fixture in `toolchain/lir_proto_parity.osty` and asserts catalog-wide
invariants the per-fixture self-tests only check indirectly: every fixture
has a non-empty `name` / `sourcePath` / `needles` triple, every source
fixture is tagged with either `current-generator` or `lir-only` so the
shadow parity loader knows where to route it, no fixture name appears
twice, and the cumulative count never regresses below the Phase-3
baseline (16 manual / 9 source). Catalog drift now surfaces as a failing
Go test before either the manual-MIR or source-fixture runners would catch
it at slice-add time.

## Phase 7: one-shot wiring behind a gate

Deliverables:

- Add an explicit gate, for example:

```text
OSTY_LLVM_LIR_PROTO=1
```

- Route only the selected backend entry point through LIR Proto when the gate
  is enabled.
- Keep automatic fallback to the current MIR emitter on structured unsupported
  diagnostics.
- Add focused backend dispatch tests.

Exit criteria:

- Gate off: output is unchanged.
- Gate on: selected fixtures use LIR Proto and pass.
- Unsupported prototype shapes fall back cleanly.

Design decision: the first Phase-7 slice lands the gate scaffold without
the runner. `internal/llvmgen/lir_proto_gate.go` exposes `LIRProtoEnvVar`
(`OSTY_LLVM_LIR_PROTO`), `LIRProtoSelected()` (env-var read with the
project's standard truthy/falsy rules), and `ErrLIRProtoNotWired` (a
sentinel that says "you asked for LIR Proto; the Go-side runner that
would call into `toolchain/lir_proto.osty` does not exist yet; falling
back to the current path").

`internal/backend/llvm.go::generateLLVMIR` reads the gate at the very top
of the dispatcher: when set, `ErrLIRProtoNotWired` is appended to the
warnings slice and the dispatcher continues through whichever fallback
path it would have chosen (native-owned fast path or MIR-direct), so
flipping the gate on early stays safe — production output is unchanged
but the gate selection is visible in build logs and test output. The
native-owned fast path was simultaneously fixed to forward the outer
warnings instead of overwriting them, so the Phase-7 warning is never
silently dropped on the shorter dispatch route.

Pinned by two Go tests: `TestLLVMDispatchAppendsLIRProtoFallbackWarning`
asserts the gate-on path emits the sentinel; the negative pin
`TestLLVMDispatchSkipsLIRProtoWarningWhenGateOff` asserts the default
behavior is unchanged so a regression that always-on'd the warning would
fail the test instead of silently noisifying every build. Five env-var
unit tests cover the truthy/falsy parsing rules.

The next Phase-7 slice lands the actual MIR -> LIR Proto -> LLVM text
runner — at that point the `ErrLIRProtoNotWired` return is replaced with
a real call into the Osty-owned lowerer plus a structured unsupported-
diagnostic fallback when the prototype declines.

## Phase 8: default-on decision

Deliverables:

- Flip a narrow default-on subset only after Phase 7 has been stable.
- Remove duplicate code only when coverage and diagnostics are better in the
  prototype path.
- Decide whether `proto` should be renamed or remain an internal detail.

Exit criteria:

- The old MIR text emitter no longer owns behavior that the prototype cannot
  reproduce.
- Removing old code reduces complexity rather than hiding it elsewhere.

## Verification loop

Fast checks during early phases:

```text
go run ./cmd/osty tokens toolchain/lir_proto.osty
go run ./cmd/osty tokens toolchain/lir_proto_parity.osty
go run ./cmd/osty tokens toolchain/lir_proto_test.osty
go run ./cmd/osty parse toolchain/lir_proto.osty >/tmp/lir_proto.parse.json
go run ./cmd/osty parse toolchain/lir_proto_parity.osty >/tmp/lir_proto_parity.parse.json
go run ./cmd/osty parse toolchain/lir_proto_test.osty >/tmp/lir_proto_test.parse.json
go run ./cmd/osty fmt --check toolchain/lir_proto.osty
go run ./cmd/osty fmt --check toolchain/lir_proto_parity.osty
go run ./cmd/osty fmt --check toolchain/lir_proto_test.osty
go test -count=1 -vet=off ./internal/llvmgen -run 'GenerateFromMIR'
```

Until the native test runner supports `toolchain`'s existing `main` package
shape, `toolchain/lir_proto_test.osty` remains a parser/formatter smoke source
rather than a directly executable `osty test` target. Keep shape checks in this
Osty source so new slices do not reintroduce a parallel Go prototype.

Broader checks near wiring:

```text
just front
just short
just osty
```

Use source-level smoke tests only after the lower-level MIR fixtures pin the
shape. The prototype should fail small before it fails across the whole
toolchain.

## First implementation slice

The current implementation slice is Osty-first:

- `toolchain/lir_proto.osty` owns the LIR Proto model, validator, renderer, and
  MIR-lowering surface.
- `toolchain/lir_proto_parity.osty` owns the shape-critical manual/source
  parity catalog and manual check result surface.
- `toolchain/lir_proto_test.osty` owns parser/formatter smoke coverage for the
  ported model, renderer, lowerer, and parity catalog shapes.
- `internal/llvmgen/lir_proto_shadow_parity_test.go` wires the
  `current-generator` source fixture subset into the current `GenerateFromMIR`
  path as the first shadow parity harness.

This gives the project a self-hosted place to land future pieces without
touching backend dispatch. Production wiring still waits for the explicit
Phase-7 gate.
