# LIR Proto plan

Status: isolated prototype in progress. No production wiring yet.
Authoring direction: new LIR Proto shape is Osty-first in
`toolchain/lir_proto.osty`; Go code remains a harness/bridge until the final
production switch.

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

Go bridge/prototype package:

```text
internal/lirproto
```

The package name intentionally says `proto`. The package is independent because
the intended future is larger than a local LLVM refactor, but `proto` keeps
production assumptions out until parity proves the shape.

Design decision: after the Phase-3 projected-result slice, LIR Proto authoring
switches to Osty-first. Existing Go prototype behavior should be ported into
`toolchain/lir_proto.osty` rather than expanded only in Go, because the eventual
self-hosted backend should not require a second translation pass.

## Core contract

Input:

- A validated, monomorphic `*mir.Module`.
- LLVM emission options that affect text shape: package, source path, target,
  GC instrumentation, and feature gates.

Output:

- A deterministic `lirproto.Module`.
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

Design decision: the LIR node vocabulary is sealed. External packages should
construct the node types defined by `internal/lirproto`, not implement their
own instruction or terminator variants. New operations must be added to
`internal/lirproto` so validation, rendering, and future lowering stay in one
place.

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

Design decision: add a Phase-1 structural validator. `Validate(Module)`
checks the plan shape before rendering: missing function names, duplicate
block labels, nil instructions, missing terminators, empty operands, invalid
void positions, negative alignment, and obvious `Type{LLVM, Class}`
mismatches. It does not check MIR semantic parity, dominance, SSA use-def
rules, runtime ABI compatibility, or target data layout yet.

Design decision: MIR lowering uses a stateful `Lowerer` object:
`NewLowerer(cfg).LowerMIR(mod) LowerResult`. The lowerer owns per-run state
that will grow in later phases: type caches, string interning, runtime
declaration planning, block/local maps, and diagnostics. `LowerResult` keeps
the structured module plus diagnostics so unsupported gaps, invalid inputs, and
lowering bugs can be tracked without parsing error strings.

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

Design decision: the fourth Phase-3 slice reuses that projected value-store
path for direct-call and scalar primitive-conversion intrinsic results. A
supported result can now target `place.field` / `place.tupleIndex` directly,
so LIR Proto matches the current generator's call-result load-modify-store
shape instead of forcing callers to materialize a temporary local first. The
first fixtures cover both same-module direct calls and `Byte.toInt()` projected
intrinsic results. Void-call destinations, runtime-backed index projections,
and broader runtime intrinsics remain deferred.

Design decision: the first Osty-first port slice mirrors the accumulated Go
prototype surface in one file: LIR model structs, renderer, validator,
runtime declaration/string-pool helpers, scalar cast/coercion planning,
aggregate projection records, and a self-hosted `MirModule -> LirModule`
lowerer shell. This still does not alter production dispatch.

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

- Add `internal/lirproto` with pure data structs.
- Add `toolchain/lir_proto.osty` as the Osty-owned mirror/source for the same
  data model and renderer surface.
- Add a renderer that can print an empty module, a simple function shell, type
  declarations, runtime declarations, and a string pool.
- Model function-body instructions and terminators as typed variants from the
  first slice.
- Add a shallow structural validator for the Phase-1 model.
- Add focused unit tests for deterministic ordering.

Coverage target:

- No MIR lowering yet.
- Construct `lirproto.Module` by hand in tests.

Exit criteria:

- The renderer is deterministic.
- The validator catches malformed hand-built plans without becoming a full
  backend verifier.
- No import of `internal/llvmgen`.
- No dependency on `internal/ast`, `internal/resolve`, or `internal/check`.

## Phase 2: scalar MIR lowering

Deliverables:

- Expand `NewLowerer(cfg).LowerMIR(mod)` beyond the Phase-1 envelope shell.
- Keep `toolchain/lir_proto.osty` moving with each Go prototype slice so the
  self-hosted backend surface does not lag behind the harness.
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
  fixtures.
- Fourth slice: projected direct-call and scalar primitive-conversion intrinsic
  result writes through the same aggregate rebuilding helper, with direct MIR,
  parity, and source fixtures.
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

Coverage target:

- The current MIR-first support whitelist.
- The Stage 5 parity gaps called out in `docs/mir_design.md`.

Exit criteria:

- The prototype is either clean for the selected surface or has a small,
  explicit, documented gap list.
- No production code calls the prototype yet.

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
go run ./cmd/osty tokens toolchain/lir_proto_test.osty
go run ./cmd/osty parse toolchain/lir_proto.osty >/tmp/lir_proto.parse.json
go run ./cmd/osty parse toolchain/lir_proto_test.osty >/tmp/lir_proto_test.parse.json
go run ./cmd/osty fmt --check toolchain/lir_proto.osty
go run ./cmd/osty fmt --check toolchain/lir_proto_test.osty
go test -count=1 -vet=off ./internal/lirproto
go test -count=1 -vet=off ./internal/llvmgen -run 'LIRProto|GenerateFromMIR'
```

`internal/lirproto` now includes a mirror guard that reads
`toolchain/lir_proto.osty` and `toolchain/lir_proto_test.osty`, parses both,
checks canonical formatting, and pins the exported LIR Proto surface. Until the
native test runner supports `toolchain`'s existing `main` package shape,
`toolchain/lir_proto_test.osty` remains a parser/formatter smoke source rather
than a directly executable `osty test` target.

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

The first Go-side slices already landed as an isolated harness:

- `internal/lirproto/model.go`
- `internal/lirproto/render.go`
- `internal/lirproto/render_test.go`
- scalar/control/direct-call/print/cast/primitive-conversion/aggregate parity
  fixtures under `internal/lirproto`

The current next slice is the Osty-first port:

- `toolchain/lir_proto.osty`
- `toolchain/lir_proto_test.osty`
- `internal/lirproto/osty_mirror_test.go`

This gives the project a self-hosted place to land future pieces without
touching backend dispatch. Production wiring still waits for the explicit
Phase-7 gate.
