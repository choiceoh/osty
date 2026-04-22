# MIR design (Osty compiler)

Status: Stage 3.12 landed (composite map values + ABI fix); Stage 4 partially
landed — MIR-first IR emission is the default for an expanding shape set while
`internal/llvmgen`'s IR→AST bridge (`legacyFileFromModule`) stays live as the
fallback for shapes the MIR emitter does not yet cover. Stage 5 (Go fallback
removal) is still deferred; the gaps are tracked in the MIR emitter's
`checkSupported` / `ProbeModule` plus the Stage 5 notes lower in this
document.

## Why a second IR

The current `internal/ir` was conceived as a "typed, normalised tree above
the checker" — what most literature calls an HIR. It has served well for
optimisation passes (constant folding, algebraic simplification, decision-
tree pre-compilation) and for the toolchain's own self-hosting needs
(formatters, linters, docs extraction, LSP). But it carries structural
debt that grows as the backend matures:

- It still carries source-level sugar: `IfLetExpr`, `MatchExpr` with
  patterns, `QuestionExpr`, `MethodCall`, `VariantLit`, `FieldExpr` vs.
  `TupleAccess`, `ForStmt` with destructuring `Pattern`, struct literals
  with `..spread`, keyword `Arg`s, defaults on `Param`, etc.
- Every backend must re-implement the same lowering of those constructs:
  discriminant loads, payload extraction, short-circuit optional chains,
  early-return semantics for `?`, guard-and-branch for match arms.
- Control flow is still expression-tree-shaped. Blocks contain statements,
  statements sometimes contain expressions that themselves encode control
  flow. Anything doing flow-sensitive analysis has to re-derive the CFG.
- The LLVM emitter ends up fighting both problems: `internal/llvmgen`
  currently reifies `*ir.Module` back into a legacy AST
  (`internal/llvmgen/ir_module.go`) and then lowers the AST — which means
  source-level pattern semantics leak into the backend by design.

The MIR exists to absorb all of that complexity in one lowering pass and
hand backends a shape that is:

1. **Sugar-free.** No pattern nodes. No `MethodCall`, no `VariantLit`,
   no `IfLetExpr`, no `QuestionExpr`, no `for` heads with destructuring.
2. **CFG-shaped.** Functions are `locals + basic blocks + terminators`.
   Control flow is always explicit: `Goto`, `Branch`, `SwitchInt`,
   `Return`, `Unreachable`.
3. **Monomorphic and layout-aware.** Every local, place, and operand
   carries its `Type`, and the module carries a `LayoutTable` for
   aggregate layout information so a backend can emit struct, tuple and
   enum operations without re-running the type checker.

Non-goals (Stage 1):

- SSA form. MIR locals can be reassigned. We are Rust-MIR-like, not
  LLVM-IR-like; an SSA pass can sit on top later.
- A full optimisation pipeline. Stage-1 MIR only needs to be *correct*
  enough to round-trip the currently green LLVM/backend subset.
- Concurrency primitives (`taskGroup`, channels, `spawn`). These live in
  HIR today and will lower to MIR once their runtime ABI is settled.
- Rewriting `internal/llvmgen` to consume MIR directly.

## Pipeline position

```
source
  → lexer
  → parser
  → resolve
  → check
  → ir.Lower         (HIR — this stays)
  → ir.Monomorphize  (HIR, monomorphic)
  → ir.Optimize      (optional; HIR)
  → ir.Validate      (HIR invariants)
  → mir.Lower        (HIR → MIR)              ⟵ Stage 1 landed
  → mir.Validate     (MIR invariants)         ⟵ Stage 1 landed
  → backend dispatch (Stage 4 partial default):
      if Options.UseMIR && Entry.MIR != nil:
        llvmgen.GenerateFromMIR(Entry.MIR, opts)
        └── on ErrUnsupported, falls back to HIR path
      else:
        llvmgen.GenerateModule(Entry.IR, opts)
        └── legacy HIR→AST bridge (explicit opt-out / fallback path)
```

MIR lowering runs *after* monomorphisation: every `TypeVar` is gone,
every generic call has a concrete mangled target, and every generic
nominal type has been rewritten to its specialisation. MIR never has to
reason about generics.

## Responsibilities: HIR vs. MIR

| Concern                          | HIR (`internal/ir`) | MIR (`internal/mir`) |
|----------------------------------|---------------------|----------------------|
| Name resolution                  | yes (strings + kinds) | no — symbols only |
| Pattern nodes                    | yes                 | no                   |
| `match` / `if let` / `for let`   | yes                 | lowered to CFG       |
| `?` and `?.`                     | yes                 | lowered to branches  |
| Method syntax                    | yes (`MethodCall`)  | direct `Call` only   |
| Enum constructor sugar           | yes (`VariantLit`)  | `AggregateRV` only   |
| Keyword / default arguments      | yes (`Arg.Name`)    | positional only      |
| Turbofish / generics             | yes                 | eliminated earlier   |
| Control flow as expressions      | yes (`IfExpr`, `BlockExpr`) | no — only statements |
| Typed every node                 | yes                 | yes (with layout ids)|
| Decision-tree pre-compilation    | yes                 | consumed by lowerer  |
| Closure captures                 | yes (`Closure.Captures`) | lifted to top-level fn + `AggClosure` (Stage 2a) |
| `defer` bodies                   | yes                 | per-block frames, replayed at every exit edge — block fall-through, return, `?`, `break`, `continue` (Stage 2c) |
| Concurrency primitives           | yes                 | `IntrinsicInstr` set (Stage 2b)                      |
| Compound & multi-target assign   | yes                 | expanded to `BinaryRV` / tuple destructure (Stage 2a) |
| Top-level global read            | yes (`IdentGlobal`) | `GlobalRefRV` (Stage 2a)                              |

Anything in the "not yet" rows causes `mir.Lower` to return an
"unsupported" diagnostic today. That signals the caller (e.g. the
backend dispatcher) to fall back to the existing HIR-based path. As
each feature gains a settled lowering, it moves from the "not yet" row
into full MIR coverage.

## MIR node model

### Values

```
Module
  Package    string
  Functions  []*Function
  Structs    []*StructLayout    // from monomorphic HIR structs
  Enums      []*EnumLayout      // discriminant + per-variant field tables
  Globals    []*Global          // top-level `let`s
  Uses       []*Use             // retained for FFI bridging
  Layouts    *LayoutTable

Function
  Name        string            // mangled symbol
  Params      []LocalID         // subset of locals that hold incoming args
  ReturnType  Type
  ReturnLocal LocalID           // _0 by convention; unused when ReturnType == Unit
  Locals      []*Local
  Blocks      []*BasicBlock
  Entry       BlockID           // always 0 in the canonical printer
  IsExternal  bool              // FFI declarations (no body)
  IsIntrinsic bool
  Span        Span
```

Locals are the single source of storage: parameters, the return slot,
named bindings, and compiler-introduced temporaries all live here.
Each local has a type and optional debug name.

### Places, projections, operands

```
Place {
  Local        LocalID
  Projections  []Projection
}

Projection
  | FieldProj       { Index int; Field string; Type Type }
  | TupleProj       { Index int; Type Type }
  | VariantProj     { Variant int; Name string; Type Type; FieldIdx int }
  | IndexProj       { Index Operand; ElemType Type }
  | DerefProj       {}

Operand
  | CopyOp  { Place; Type }        // non-destructive read
  | MoveOp  { Place; Type }        // reserved — semantics == Copy today
  | ConstOp { Const; Type }

Const
  | IntConst    { Value int64 ; Type }
  | BoolConst   { Value bool }
  | FloatConst  { Value float64; Type }
  | StringConst { Value string }
  | CharConst   { Value rune }
  | ByteConst   { Value byte }
  | UnitConst   {}
  | NullConst   { Type }           // canonical `None` payload marker
  | FnConst     { Symbol string; Type } // function-pointer literal
```

`MoveOp` is kept in the vocabulary even though the current runtime is
GC-based and copy-on-use is uniformly safe; future owning-pointer or
linear-type work will distinguish them. Today the validator accepts
both.

### Instructions

```
Instr
  | AssignInstr    { Dest Place; Src RValue }
  | CallInstr      { Dest Place?; Callee; Args []Operand; Conv }
  | IntrinsicInstr { Dest Place?; Kind; Args []Operand }
  | StorageLive    { Local }
  | StorageDead    { Local }
```

`CallInstr` with `Dest == nil` is a statement-position call that
produces no value (unit return, or value discarded). The `Callee` is
either a direct `FnRef{Symbol}` (already mangled, as chosen by the
monomorphiser) or an `IndirectCall{Callee Operand}` for first-class
function values. Keyword arguments are gone by the time we enter MIR:
`mir.Lower` reorders and fills defaults against the callee's declared
signature.

`IntrinsicInstr` covers backend-neutral built-ins. The `Kind` value
is the stable ABI contract — backends map each kind to whatever
runtime symbol their target exposes. Kinds group into families:

- **Print family**: `IntrinsicPrint`, `IntrinsicPrintln`,
  `IntrinsicEprint`, `IntrinsicEprintln`.
- **Runtime builders**: `IntrinsicStringConcat` (interpolated string
  assembly), `IntrinsicAbort` (trap).
- **Concurrency — channels**: `IntrinsicChanMake`,
  `IntrinsicChanSend`, `IntrinsicChanRecv` (returns `Option<T>`),
  `IntrinsicChanClose`, `IntrinsicChanIsClosed`.
- **Concurrency — structured tasks**: `IntrinsicTaskGroup`,
  `IntrinsicSpawn` (detached: `[closure]`; scoped: `[group, closure]`),
  `IntrinsicHandleJoin`, `IntrinsicGroupCancel`,
  `IntrinsicGroupIsCancelled`.
- **Concurrency — helpers**: `IntrinsicParallel`, `IntrinsicRace`,
  `IntrinsicCollectAll`, `IntrinsicSelect` (the outer `thread.select`
  call), plus the per-arm intrinsics `IntrinsicSelectRecv`,
  `IntrinsicSelectSend`, `IntrinsicSelectTimeout`,
  `IntrinsicSelectDefault` for `s.recv`/`send`/`timeout`/`default`
  inside the select closure.
- **Concurrency — cancellation**: `IntrinsicIsCancelled`,
  `IntrinsicCheckCancelled`, `IntrinsicYield`, `IntrinsicSleep`.
- **Stdlib collections / primitives** (Stage 2d): the `list_*`,
  `map_*`, `set_*`, `string_*`, `bytes_*`, `option_*`, `result_*`
  intrinsic families cover method calls on the built-in primitive
  types. See the Stage 2d migration note below for the full list.

Intrinsics whose runtime ABI is "fire and forget" (`chan_send`,
`chan_close`, `group_cancel`, `yield`, the four
`select_recv`/`send`/`timeout`/`default` arm registrations, and the
mutating stdlib pair — `list_push`, `map_set`, `map_remove`,
`set_insert`) always carry a nil `Dest` — the lowerer drops the
destination even when the call site was written in expression
position.

`StorageLive` / `StorageDead` mark the live range of a local. Backends
that do not care may ignore them; they exist so that future lifetime
and GC-root analyses can run over MIR.

### Terminators

```
Terminator
  | GotoTerm        { Target BlockID }
  | BranchTerm      { Cond Operand; Then, Else BlockID }
  | SwitchIntTerm   { Scrutinee Operand; Cases []SwitchCase; Default BlockID }
  | ReturnTerm      {}                // reads _0 / ReturnLocal
  | UnreachableTerm {}
```

`SwitchCase` is `{Value int64; Target BlockID}`. Enum discriminants and
integer-literal matches collapse into the same terminator; string /
float / char matches lower to a chain of `BranchTerm`s ahead of time.

`ReturnTerm` always returns the contents of the function's
`ReturnLocal`. `Unreachable` sits after calls whose return type is
`!` / whose match coverage was proven exhaustive by the decision-tree
compiler.

### RValues

```
RValue
  | UseRV          { Op Operand }
  | UnaryRV        { Op; Arg; Type }
  | BinaryRV       { Op; Lhs, Rhs; Type }
  | AggregateRV    { Kind; Fields []Operand; Type; VariantIdx int }
  | DiscriminantRV { Place; Type }              // enum tag load
  | LenRV          { Place; Type }              // collection length
  | CastRV         { Kind; Arg; From, To Type }
  | AddressOfRV    { Place; Type }              // borrow (future use)
  | RefRV          { Place; Type }              // wrap into optional Some
  | NullaryRV      { Kind; Type }               // e.g. NoneOf<T>
  | GlobalRefRV    { Name string; Type }        // read a top-level `let`
```

`AggregateRV.Kind` enumerates `Tuple`, `Struct`, `EnumVariant`, `List`,
`Map`, and `Closure`. For `EnumVariant`, `VariantIdx` names the active
arm and `Fields` is the payload tuple. For `Closure`, `Fields[0]` is a
`FnConst` operand naming the lifted closure body and `Fields[1..]` are
the captured operands in the same order the lifted function declares
them as its prefix parameters. The emitter uses the `LayoutTable` to
pick the right in-memory representation (`%T = type { i64, ... }` for
struct; `%Enum = type { i64, i64 }` with variant-specific payload in
the current LLVM backend).

### Layouts

```
LayoutTable {
  Structs map[string]*StructLayout
  Enums   map[string]*EnumLayout
  Tuples  map[string]*TupleLayout        // keyed by structural signature
}

StructLayout {
  Name         string              // mangled post-monomorphisation
  Fields       []FieldLayout
  ByteSize     int                 // 0 when backend computes it
  Mangled      string              // runtime-visible name
}

FieldLayout {
  Index int
  Name  string
  Type  Type
}

EnumLayout {
  Name         string
  Mangled      string
  Discriminant Type                // typically Int / UInt32
  Variants     []VariantLayout
}

VariantLayout {
  Index   int
  Name    string
  Payload []FieldLayout            // empty for bare variants
}
```

`LayoutTable` starts life as a pure index: the MIR lowerer walks the
monomorphic HIR's `StructDecl` / `EnumDecl` list and records a single
entry per type. Backends that need ABI-level byte offsets fill in
`ByteSize` themselves (LLVM gets it from `DataLayout`; a future
bytecode VM would compute and pin them here). The table is keyed by
the post-monomorphisation *structural* name — `List<Int>` and
`List<String>` are two distinct entries with distinct mangled names.

## Lowering rules

The lowerer walks a HIR `*ir.Module` top-to-bottom and emits a MIR
`*mir.Module`. It never recurses into the HIR a second time; every
lowering step is driven off the node under inspection.

### Functions

One HIR function ⇒ one MIR function. Parameters become MIR locals with
the `IsParam` flag set. The first local is `_0`, the return slot,
matching the `ReturnLocal` convention. Simple name-bound params become
their own locals; destructured params (`|(a, b)|`) get one synthetic
local for the whole value plus a set of projection-based assigns at
the start of the entry block.

### Blocks and statements

The lowerer threads a `cursor` through basic blocks. Each stmt either
appends instructions to the current block or terminates it and starts
a new one.

- `LetStmt` with a simple name: allocate local, emit `StorageLive`,
  lower RHS into it.
- `LetStmt` with a pattern: lower RHS into a scratch local, then for
  each leaf binding emit `Assign Dest = Use(CopyOp(scratch + proj))`.
  Nested patterns compose by extending the projection list.
- `AssignStmt` (`=` and compound): lower RHS; for each target on the
  LHS, lower to a `Place` and emit `Assign`.
- `ReturnStmt`: lower RHS into `_0`; terminate with `Return`.
- `Break` / `Continue`: terminate with `Goto` to the loop's
  `break_target` / `continue_target` recorded on a loop stack.
- `IfStmt`: lower cond into operand, `Branch → then_bb / else_bb`,
  lower each arm body into its block, join back into `merge_bb`.
- `ForStmt`:
  - `ForInfinite`: one header block that loops back onto itself.
  - `ForWhile`: header block branches on cond to body or exit.
  - `ForRange`: allocate index local, initialise, header block does
    bounds check, body increments.
  - `ForIn`: treated as unsupported for Stage-1 when the iterable is
    not a built-in `List<T>`. For `List<T>` it lowers to an index-based
    loop using `LenRV` and an `IndexProj` read.
- `MatchStmt`: use `ir.CompileDecisionTree` (when available) to drive
  the CFG. Each `DecisionSwitch` becomes a `SwitchInt` or a `Branch`
  cascade; `DecisionBind` becomes an assign with projection;
  `DecisionGuard.Cond == nil` chains to the next arm; `DecisionFail`
  becomes `Unreachable` when the original match was proven exhaustive
  and an explicit abort intrinsic otherwise.
- `DeferStmt` registers its body with the innermost defer frame.
  Frames are pushed/popped around every block-shaped scope. At each
  control-flow exit the frames are replayed in LIFO:
  - normal block fall-through replays only the top frame;
  - `return` / `?` propagation / implicit unit fall-through replays
    every frame;
  - `break` and `continue` replay frames down to (inclusive of) the
    enclosing loop body's frame, since each iteration enters the
    body scope afresh.

### Expressions

Expressions in statement position are lowered recursively through a
helper that returns an `Operand`. Sub-expressions that produce side
effects (calls, assignments in nested form) emit instructions into the
current block and yield a `CopyOp` referencing the temporary they
assigned into.

- `IntLit`, `BoolLit`, `FloatLit`, `CharLit`, `ByteLit`, `UnitLit`,
  `StringLit` (non-interpolated): `ConstOp`.
- `StringLit` with interpolation: lowered to a `StringConcat`
  intrinsic call that backends route to their runtime's formatted
  string builder.
- `Ident`: `CopyOp` on the resolved local / param / global, or
  `ConstOp(FnConst)` for fn references.
- `UnaryExpr`, `BinaryExpr`: recursive lowering, result in a
  `UnaryRV` / `BinaryRV` assigned into a fresh temp.
- `CallExpr`: map keyword args to positional per the callee's
  signature; fill defaults; emit `CallInstr`. Two pre-dispatch
  fast-paths run first: (a) bare `Ident` callees matching a prelude-
  visible concurrency name (`taskGroup`, `parallel`, `race`,
  `collectAll`) emit an `IntrinsicInstr`; (b) `FieldExpr` callees
  whose `X` names a `use` alias matching a `thread`-namespaced
  concurrency name (`thread.spawn`, `thread.chan`, `thread.select`,
  …) do the same.
- `IntrinsicCall`: `IntrinsicInstr`.
- `MethodCall`: recogniser cascade, in order — (a) aliased
  qualifier (Stage 2a: `thread.spawn`, `runtime.strings.Split`,
  …), (b) runtime-type concurrency method (Stage 2b:
  `Channel.recv`, `Channel.close`, `Channel.isClosed`,
  `Handle.join`, `Group.spawn`/`cancel`/`isCancelled`, `Select.recv`
  /`send`/`timeout`/`default`), (c) stdlib primitive method
  (Stage 2d: `List<T>`, `Map<K,V>`, `Set<T>`, `String`, `Bytes`,
  `Option<T>` / `T?`, `Result<T,E>`). A match emits an
  `IntrinsicInstr` with the receiver as the first operand. If no
  recogniser fires the lowerer resolves to a direct function whose
  first parameter is the receiver and emits a `CallInstr`.
- `ChanSendStmt`: `IntrinsicChanSend{[channel, value]}` with `Dest`
  always nil.
- `for x in ch` (channel iterable): receive-loop header → body →
  step cycle. The header calls `IntrinsicChanRecv` into an
  `Option<T>` local, reads its discriminant, and dispatches via
  `SwitchInt` — `Some` unwraps the payload into the loop binding and
  runs the body; `None` exits the loop.
- `VariantLit`: `AggregateRV{Kind: EnumVariant}`.
- `StructLit`: `AggregateRV{Kind: Struct}`. Spread (`..rest`) lowers to
  per-field copies from the spread expression plus per-explicit
  overrides. Shorthand fields (`{name}` with no `:`) lower to
  `Use(CopyOp(local))`.
- `TupleLit`: `AggregateRV{Kind: Tuple}`.
- `ListLit`: for Stage 1 we emit `AggregateRV{Kind: List}` with the
  element operands inline; the runtime-ABI backend translates that
  into a sequence of `osty_rt_list_new` + `osty_rt_list_push_*` calls.
- `FieldExpr` (non-optional): `Place` with a `FieldProj`.
- `TupleAccess`: `Place` with a `TupleProj`.
- `IndexExpr`: `Place` with an `IndexProj`.
- `QuestionExpr`: lower the sub-expression into a scratch local, emit
  a `DiscriminantRV` + `SwitchInt`; the "happy" successor extracts the
  payload and continues, the "unhappy" successor **rebuilds** the
  error value in the enclosing function's return type — `NullaryRV{
  NullaryNone}` for Option-shaped returns, `AggregateRV{EnumVariant}`
  carrying the extracted `Err` payload for Result — and terminates
  with `Return` after replaying defers.
- `FieldExpr` (optional, `x?.field`): lower the receiver, check its
  discriminant, branch on None → fallthrough to the merge block with
  a `None` result, or extract the payload, read the field, wrap back
  in `Some`.
- `CoalesceExpr`: lower left, check discriminant, select between the
  unwrapped payload and the lowered right.
- `IfExpr` / `IfLetExpr` / `MatchExpr` / `BlockExpr` (expression
  form): same CFG construction as their statement siblings, with the
  extra step of writing each arm's tail into a single merge local.

### Guarantees

- No pattern nodes appear in MIR output. `StructPat`, `VariantPat`,
  `TuplePat`, `OrPat`, `BindingPat`, `RangePat`, `LitPat`, `WildPat`,
  `IdentPat`, `ErrorPat` are HIR-only.
- `MatchExpr`, `MatchStmt`, `IfLetExpr`, `QuestionExpr`, `MethodCall`,
  `VariantLit`, `IntrinsicCall` *as HIR nodes* are forbidden in MIR
  output. Calls are always `CallInstr` or `IntrinsicInstr`; variant
  construction is always `AggregateRV`; pattern-driven control flow is
  always CFG.
- Every `Place` rooted at a local carries only projections whose
  types are known. The validator rejects projections with a nil
  `Type`.
- Every `Operand` carries a type. The validator rejects `nil` types.
- Every basic block ends in exactly one terminator. The validator
  rejects instructions *after* a terminator and blocks without one.

## Tests

MIR ships with three test surfaces in `internal/mir/mir_test.go`:

1. **Construction tests** build MIR modules by hand, run the printer,
   and assert on the rendered output. These pin the textual format
   and catch accidental API drift.
2. **Lowering tests** lower a tiny HIR fixture and assert on the MIR
   printer output. They cover:
   - constant return / simple arithmetic
   - if / else with merge
   - while loop
   - struct literal + field read
   - enum variant construction + match via decision tree
   - `if let` over `Option<T>`
   - optional chaining `x?.y`
   - `?` on `Result<T, E>` inside a fallible fn
   - direct method call on a struct
3. **Validator tests** intentionally construct malformed MIR modules
   and assert the validator catches them. These double as executable
   documentation for the invariants above.

The tests live under `internal/mir/` and depend only on
`internal/ir`. They do *not* depend on the parser, resolver, or
checker — each fixture is a hand-written HIR module. That keeps the
MIR tests isolated from front-end churn.

## Migration plan

- **Stage 1 (landed).** Add `internal/mir` with types, printer,
  validator, lowering, and tests. `internal/llvmgen` is untouched.
  `docs/mir_design.md` is this document.

- **Stage 1 — Osty port (landed, `#503`).** `toolchain/mir.osty`
  mirrors the MIR core (intrinsic-kind enum with the full
  `MirIntrinsic*` set, printer label table, operand / instr shape
  notes) so the compiler's IR vocabulary participates in the spec
  corpus alongside the rest of the front-end. Go (`internal/mir`)
  stays authoritative — the Osty port is a read path and will become
  a source path as more HIR/MIR lowering moves into `toolchain/*.osty`
  per the "Osty로 짤 수 있는 건 Go로 짜지 마" rule in AGENTS.md /
  CLAUDE.md. If the intrinsic-kind set changes in Go, `toolchain/mir.osty`
  updates in the same commit; a printer-label mismatch surfaces as a
  spec corpus diff.

- **Stage 2a (landed).** Expand MIR coverage for the most common
  HIR-only shapes and tighten two correctness bugs spotted in code
  review:
  - **Closures.** A `Closure` expression lifts its body into a fresh
    top-level MIR function named `<parent>__closure<N>`. Captures
    become the function's leading positional parameters, and the
    closure value is an `AggregateRV{Kind: AggClosure}` whose first
    field is a `FnConst` naming the lifted function and whose
    remaining fields are the captured operands in parameter order.
    Backends wanting a flat fn-pointer calling convention unpack the
    aggregate; backends wanting a closure-struct ABI read the fields
    by index.
  - **Defer.** Defer bodies accumulate on a per-function stack and
    replay in LIFO at every return edge — explicit `return`, the
    function's trailing expression, `?` early-return, and implicit
    unit fall-through. Break/continue do not replay (they jump to
    the loop exit, where the defers will replay on the eventual
    return). Stage 2 keeps defers anchored to the function scope;
    inner-block scoping is documented as Stage 2b.
  - **Top-level global reads.** `Ident{Kind: IdentGlobal}` now lowers
    to a `GlobalRefRV{Name}` rvalue assigned into a fresh temp. The
    rvalue is a named read; backends pick their materialisation
    strategy (static slot, init-on-first-use).
  - **Compound and multi-target assignment.** `x op= rhs` expands to
    `x = x op rhs` via `BinaryRV`. `(a, b, …) = rhs` binds `rhs` into
    a scratch tuple-typed local and fans out via `TupleProj` assigns
    into each LHS place. Both paths reuse `lowerExprToPlace` so
    field-indexed and tuple-indexed targets work.
  - **`?` error-path fix.** `e?` inside a function whose return type
    differs from `e`'s type used to copy the whole operand into the
    return slot — correct only when the types happened to match. The
    lowerer now classifies the operand as `Option`- or `Result`-
    shaped and rebuilds the error value in the enclosing return
    type: `NullaryRV{NullaryNone}` for Option, an `AggregateRV{Err}`
    that re-wraps the extracted `Err` payload for Result.
  - **Package / FFI qualified call fix.** `strings.Split(s, ",")`
    used to lower as `Split(strings, s, ",")` because the FieldExpr
    callee path treated the qualifier as a receiver. The lowerer now
    indexes `use` aliases and, when a `MethodCall`'s receiver or a
    `CallExpr`'s FieldExpr callee names a use alias, emits a direct
    `FnRef` to the qualified symbol (`runtime.strings.Split`, etc.)
    with no synthetic `self` argument. Non-alias FieldExpr callees
    (first-class fn-typed fields) fall back to an `IndirectCall`
    through the projected field value.

- **Stage 2b (landed).** Concurrency primitives — `spawn`, channel
  send / recv / make / close / isClosed, `taskGroup`, `g.spawn`,
  `h.join`, `thread.select`, `parallel`, `race`, `collectAll`,
  `thread.isCancelled` / `checkCancelled` / `yield` / `sleep`, and
  `for x in ch` loops — lower to MIR `IntrinsicInstr`s. Design notes:

  - The MIR intrinsic name is the stable ABI contract. Backends map
    each kind to whatever runtime symbol their target exposes; the
    runtime itself is not contract-frozen yet, so we deliberately
    keep the vocabulary at the semantic layer rather than baking in a
    specific C ABI.
  - Channel / Handle / Group / Select method calls (`ch.recv()`,
    `h.join()`, `g.spawn(f)`, …) are recognised by **receiver type
    name** (`Channel`, `Handle`, `Group`, `TaskGroup`) and route to
    the matching intrinsic with the receiver as the first operand,
    followed by user args in source order. This runs after the
    Stage 2a use-alias / package-qualifier check, so an aliased
    `thread.spawn(f)` is spotted as a concurrency call *before* it
    falls through to the ordinary qualified-call emitter.
  - Prelude-visible calls (`taskGroup`, `parallel`, `race`,
    `collectAll`) are recognised from the bare `Ident` callee. No
    `use` is required for them; they match only when the qualifier
    is empty.
  - `ChanSendStmt` (the only dedicated HIR concurrency node) lowers
    directly to `IntrinsicChanSend` with `[channel, value]` operands.
  - `for x in channel` compiles to a receive loop: a header block
    that calls `IntrinsicChanRecv`, loads the result's discriminant,
    and dispatches `Some` → body / `None` → exit via a `SwitchInt`.
    The body unwraps the payload into a named local (or destructures
    via the loop pattern) before running the user statements.
  - `IntrinsicInstr.Dest` is nil for the no-value kinds (`chan_send`,
    `chan_close`, `group_cancel`, `yield`). The lowerer drops the
    destination for those even when the call site was written in
    expression position.

- **Stage 2c (landed).** Inner-block defer scoping and the
  `thread.select` arm expansion. Specifically:

  - `bodyState.deferStack` is replaced by a stack of per-scope defer
    *frames*. The function-level frame is seeded in `newBodyState`
    and every block-containing lowerer (`lowerBlockStmt`,
    `lowerIfStmt`/`lowerIfExprInto`, `lowerForInfinite` / `While` /
    `Range` / `In` / `InChannel`, match-arm bodies, `if let` arms,
    `lowerIfLetExprInto`, `lowerBlockExprInto`) pushes/pops its own
    frame around the statements it emits. The replay rules are:
    - Normal block fall-through replays the **top** frame before
      popping.
    - `return`, `?` propagation, the function's trailing expression,
      and implicit unit fall-through all replay **every** frame in
      LIFO.
    - `break` and `continue` replay frames from the top down to and
      *including* the enclosing loop body's frame, since each
      iteration enters the body scope fresh. `loopFrame` records the
      `deferDepth` captured right after the body frame is pushed.
  - An inner-block defer no longer leaks into the enclosing
    function's return — a regression test (`TestLowerDeferInner-
    BlockDoesNotLeakToOuterReturn`) pins that invariant.
  - `thread.select(|s| body)` arms are now distinguished by
    receiver-type recognition. Method calls on a `Select` value —
    `s.recv(ch, f)`, `s.send(ch, v, f)`, `s.timeout(d, f)`,
    `s.default(f)` — lower to dedicated intrinsics
    (`IntrinsicSelectRecv` / `Send` / `Timeout` / `Default`). All
    four always carry `Dest: nil`, matching the Osty spec where
    those builder methods are treated as fire-and-forget arm
    registrations even though the surface signature returns
    `Select`. Backend note: `IntrinsicSelectSend` lowers in
    `internal/llvmgen/mir_generator.go` via `emitSelectSend`, which
    reads the channel's `Channel<T>` element type off `Args[1]` and
    dispatches to the typed runtime entry
    `osty_rt_select_send_{i64,i1,f64,ptr}(ptr s, ptr ch, <elemLLVM>
    value, ptr arm)` for scalar elements or
    `osty_rt_select_send_bytes_v1(ptr s, ptr ch, ptr src, i64 size,
    ptr arm)` for composite elements. The `arm` pointer is the Osty
    `() -> ()` closure env the runtime invokes after a successful
    enqueue (`#496`, RUNTIME_SCHEDULER.md 2026-04-21 follow-up).

  Closure-to-SSA captures are still deferred until the borrow rules
  land in the language spec — `AggClosure` stays as-is for now and
  will be upgraded once the spec stabilises.

- **Stage 2d (landed).** Stdlib method surface. Primitive-type
  method calls on `List<T>`, `Map<K, V>`, `Set<T>`, `String`,
  `Bytes`, `Option<T>` (both `T?` and `NamedType{"Option"}`) and
  `Result<T, E>` lower to dedicated intrinsics instead of generic
  `CallInstr{FnRef{"Type__method"}}` calls. Concrete intrinsic set:

  - List: `list_push`, `list_len`, `list_get`, `list_is_empty`,
    `list_first`, `list_last`, `list_sorted`, `list_contains`,
    `list_index_of`, `list_to_set`.
  - Map: `map_new`, `map_get`, `map_set`, `map_contains`, `map_len`,
    `map_keys`, `map_values`, `map_remove`.
  - Set: `set_new`, `set_insert`, `set_contains`, `set_len`,
    `set_to_list`.
  - String: `string_len`, `string_is_empty`, `string_contains`,
    `string_starts_with`, `string_ends_with`, `string_index_of`,
    `string_split`, `string_trim`, `string_to_upper`,
    `string_to_lower`, `string_replace`, `string_chars`,
    `string_bytes`.
  - Bytes: `bytes_len`, `bytes_is_empty`, `bytes_get`.
  - Option: `option_is_some`, `option_is_none`, `option_unwrap`,
    `option_unwrap_or`.
  - Result: `result_is_ok`, `result_is_err`, `result_unwrap`,
    `result_unwrap_or`.

  All stdlib intrinsics keep the receiver as their first operand;
  element / key / value types flow through the receiver's type, so
  backends read those directly when selecting the specialised
  runtime symbol (`osty_rt_list_sorted_i64` vs `_string`, etc.).
  The void list (`isVoidStdlibIntrinsic`) drops the destination for
  `list_push`, `map_set`, `map_remove`, `set_insert` — those
  mutate in place.

  The recogniser (`stdlibIntrinsicForMethod`) runs in
  `lowerMethodCallInto` **after** the use-alias / concurrency
  checks, so neither a package-qualified call (`strings.Split`) nor
  a concurrency receiver (`Channel`, `Handle`, `Group`, `Select`)
  shadows a stdlib primitive name. Two regression tests pin that
  ordering: `TestLowerStdlibRecognizerDoesNotShadowUserMethods`
  confirms a user-defined `Point.len()` still reaches the regular
  method-call path, and `…DoesNotShadowConcurrencyMethods` confirms
  `Channel.recv()` still emits `chan_recv` rather than falling
  through. String / Bytes receivers are dispatched from their
  `PrimType` kind so the NamedType lookup path isn't needed for
  them.

  Closure-taking methods (`list.map`, `list.filter`,
  `list.reduce`, `option.map`, `result.andThen`, etc.) remain
  unrecognised in Stage 2d — they need closure-value ABI work that
  overlaps with the deferred closure-to-SSA story. They fall
  through to the ordinary `CallInstr` path for now.

- **Stage 3 (landed — MVP).** Introduces a MIR-direct path through
  `internal/llvmgen`.

  - **`backend.PrepareEntry` produces MIR.** `Entry` grows `MIR
    *mir.Module` and `MIRIssues []error` fields alongside the existing
    `IR` / `IRIssues`. After HIR monomorphization + validation, the
    pipeline calls `mir.Lower` on the monomorphic HIR and runs
    `mir.Validate` on the result. MIR issues are collected as
    warnings rather than blocking the dispatch — the HIR path remains
    authoritative while the MIR emitter grows to parity.
  - **`llvmgen.GenerateFromMIR(m, opts)`** is the new public entry
    point. It consumes a `*mir.Module` directly (no HIR→AST bridge)
    and emits textual LLVM IR via an alloca-per-local SSA scheme
    (LLVM's mem2reg pass promotes to register form during opt).
  - **Dispatcher gate.** `llvmgen.Options.UseMIR` selects the path.
    The backend dispatcher (`internal/backend/llvm.go`) prefers
    `GenerateFromMIR(entry.MIR, opts)` by default on every emit
    mode — raw `llvm-ir`, object, binary. On `ErrUnsupported` the
    dispatcher catches the sentinel and falls back to
    `GenerateModule(entry.IR, opts)` automatically, so coverage
    regressions are impossible while parity lands.
  - **MVP coverage.** Primitive types (Int / UInt / Byte / Bool /
    Char / Float{32,64} / String / Unit), functions with primitive
    params + return types, `Assign` with Use/Unary/Binary/Const
    rvalues, direct `Call` to `FnRef` callees, `IntrinsicPrint` /
    `Println` (printf-backed), `Goto` / `Branch` / `SwitchInt` /
    `Return` / `Unreachable` terminators. Anything outside the MVP
    returns `ErrUnsupported` with a `mir-mvp` kind so the fallback
    triggers rather than producing malformed IR. Structs, enums,
    tuples, lists, maps, optional/result values, closures, and the
    concurrency family are all follow-up scope.
  - **Parity tests.** `internal/llvmgen/mir_generator_test.go`
    builds HIR modules by hand (independent of the parser), runs
    them through both emitters, and asserts both outputs contain the
    expected core instructions. A dedicated fallback test confirms
    that a program outside the MVP is rejected by `GenerateFromMIR`
    with `ErrUnsupported` and accepted by the legacy path.

- **Stage 3.1 (landed).** Aggregate types — structs and tuples.

  - **Struct layouts** from `mir.Module.Layouts.Structs` are emitted
    once per module as `%Name = type { T1, T2, ... }` (sorted
    alphabetically for stable output) and injected before the first
    `define`/`declare` line.
  - **Tuple layouts** are discovered on demand while emitting
    function bodies. The emitter interns each distinct tuple type
    with a mangled name matching the legacy convention
    (`Tuple.<elem1>.<elem2>...`) so cross-emitter comparisons stay
    easy. Elements render in a compact tag form (`i64`, `string`,
    `f64`, nested `Tuple.*`) rather than full LLVM type syntax so
    the type name round-trips through LLVM's parser.
  - **`AggregateRV{Struct|Tuple}`** compiles to a chain of
    `insertvalue` instructions starting from `undef`. LLVM's SROA
    pass folds the chain away during optimisation, so the cost is
    purely a Stage-3 MVP one.
  - **`FieldProj` / `TupleProj` reads** load the whole aggregate
    once from its alloca slot then chain `extractvalue` per
    projection. Indices come from the projection's `Index` field.
  - **`FieldProj` / `TupleProj` writes** are read-modify-write:
    load → extractvalue per intermediate projection → insertvalue
    the new leaf → insertvalue back up the chain → store. Nested
    projections fan out correctly (e.g. `outer.inner.v = x`
    produces two-level insertvalue).
  - `checkSupported` accepts `NamedType` receivers only when the
    module's `LayoutTable` has a matching struct entry — enums
    still fall through to fallback. `FieldProj` and `TupleProj` are
    the only projection kinds accepted; `VariantProj`, `IndexProj`,
    `DerefProj` remain in the unsupported set awaiting later
    expansion stages.

- **Stage 3.2 (landed).** Enum / Option / Maybe / Result + variant
  payload projections.

  - All enum-shaped values share a fixed 2-word layout
    `{ i64 disc, i64 payload }` matching the legacy emitter's
    `%Maybe = type { i64, i64 }` convention. Scalar payloads up to
    64 bits ride the payload slot via `zext` / `sext` / `bitcast` /
    `ptrtoint` at construction and narrow back with
    `trunc` / `bitcast` / `inttoptr` at read.
  - User enums emit as `%EnumName = type { i64, i64 }` (registered
    from `LayoutTable.Enums`). Prelude `Option<T>` / `Maybe<T>` /
    `Result<T, E>` and the surface `T?` optional mint type-parametric
    names like `%Option.i64`, `%Result.i64.string` so the IR still
    encodes the element type in its name.
  - `AggregateRV{EnumVariant}` compiles to two `insertvalue`
    instructions — discriminant first, then payload. Bare variants
    (no payload) fill the slot with zero. Single-scalar payloads
    widen through the `toI64Slot` helper.
  - `DiscriminantRV` loads the scrutinee aggregate and
    `extractvalue` at index 0.
  - `VariantProj` extracts the i64 payload slot and narrows it back
    to the declared payload type through `fromI64Slot`, so that MIR
    patterns like `if let Some(x) = opt` produce correct reads.
  - `NullaryRV{NullaryNone}` emits a `{ 0, 0 }` aggregate of the
    target enum type, and `CastRV` handles int resize, int↔float,
    bitcast, optional-wrap/unwrap passthrough.

- **Stage 3.3 (landed).** `List<T>` / `Map<K, V>` / `Set<T>` via
  the runtime ABI.

  - Builtin collection types resolve to `ptr` at the LLVM boundary.
  - `AggregateRV{List}` compiles to `osty_rt_list_new()` + a chain
    of `osty_rt_list_push_<suffix>` calls, using
    `listRuntimeSymbolSuffix` to pick `_i64` / `_i1` / `_f64` /
    `_ptr` / …
  - Stdlib method intrinsics route through the runtime:
    `list.len` / `is_empty` / `push` / `get` / `sorted` / `to_set`
    → `osty_rt_list_*`; `map.get` / `set` / `contains` / `len` /
    `keys` / `remove` → `osty_rt_map_*`; `set.insert` / `contains`
    / `len` / `to_list` → `osty_rt_set_*`. Each symbol is declared
    once (and only once) in the module's `declare` block via
    `declareRuntime`.
  - `map.get` lowers to `osty_rt_map_get_or_abort_<keysuffix>` to
    match the legacy emitter's semantics; map values are widened
    into the ptr-sized slot through `toI64Slot` + `inttoptr` when
    the map value type is narrower than ptr.
  - Composite list element types (structs, tuples, nested lists)
    still fall back to legacy — the bytes runtime path needs a
    struct-size computation that the MVP doesn't ship yet.

- **Stage 3.4 (landed — non-capturing closures + indirect call).**

  - **`FnConst`** constants now render as `@<symbol>` LLVM value
    references, so MIR operands holding fn pointers flow through
    the emitter without an extra alloca.
  - **`AggregateRV{AggClosure}`** with a *single* field (a bare
    `FnConst`, i.e. a non-capturing closure) collapses to the fn
    pointer value. No allocation, no struct — the emitted MIR just
    becomes `store ptr @<lifted-closure-symbol>, ptr <slot>`.
    Capturing closures (multi-field `AggClosure`) still return
    `ErrUnsupported` because they need a heap-backed environment
    the MVP hasn't designed yet.
  - **`IndirectCall`** is accepted when the callee operand has an
    `FnType`. The emitter reads the LLVM signature off the FnType,
    lowers each argument against the declared param types, and
    emits `call <ret> (<params>) <ptr-operand>(<args>)`.
  - **MIR lowerer fix**: `resolveCall` now routes
    `Ident{Kind: IdentLocal|IdentParam}` callees through
    `IndirectCall` (carrying a `CopyOp` of the local) instead of
    minting a stray `FnRef` to a symbol that doesn't exist. That
    unblocks first-class fn values passed as parameters.

- **Stage 3.5 (landed — `IndexProj` on `List<T>`).**

  - `checkProjectionsSupported` accepts `IndexProj` when the
    projection base is a builtin `List<T>` (the immediate preceding
    type in the place chain, or the local's type for a leading
    index projection).
  - In `emitLoad`, when the walker hits an `IndexProj` on a ptr-
    typed list base, it emits
    `call <elem> @osty_rt_list_get_<suffix>(ptr %list, i64 %idx)`
    instead of the usual `extractvalue`. The typed suffix (`_i64`,
    `_i1`, `_f64`, `_ptr`) matches the Stage 3.3 runtime ABI.
  - `projectionIndex` still returns `false` for `IndexProj` (there
    is no static field index); the walker handles it via the
    runtime-call branch. `projectionType` returns `IndexProj.ElemType`
    so downstream projections (e.g. `xs[i].name`) compose without
    extra plumbing.
  - Composite list element types (structs, tuples, nested lists)
    now route through the bytes-v1 fallback (Stage 3.6) rather than
    falling back to the legacy path.

- **Stage 3.6 (landed — composite list element types).**

  - **`emitListPushOperand`** routes composite elements (anything
    outside the `listUsesTypedRuntime` scalar set) through the
    bytes-v1 runtime helper: alloca a slot sized to the element
    type, store the value, compute the size via the
    `getelementptr null, 1` + `ptrtoint` idiom, and call
    `osty_rt_list_push_bytes_v1(list, ptr slot, i64 size)`.
  - **`emitListGetBytes`** (new) handles the symmetric
    `osty_rt_list_get_bytes_v1(list, idx, ptr out, i64 size)` ABI:
    alloca a stack slot, let the runtime write into it, load back.
    Reachable from both `IntrinsicListGet` (stdlib method) and the
    `IndexProj` walker in `emitLoad`, so `xs[i]` on a
    `List<Point>` or `List<(Int, String)>` compiles without
    falling back.
  - **`emitSizeOf`** (new) centralises the sizeof idiom —
    `getelementptr %T, ptr null, i32 1` + `ptrtoint` — so every
    composite-element call site uses the same LLVM-friendly
    pattern.

- **Stage 3.8 (landed — capturing closures + uniform env ABI).**
  Supersedes Stage 3.4 with a single closure calling convention
  used everywhere.

  - **Closure value layout.** Every closure is a `ptr env`, where
    env is a stack-allocated struct
    `%ClosureEnv.<elemtags...> = type { ptr, cap0, cap1, ... }`.
    Slot 0 is the fn pointer; slots 1..N are captures. Non-capturing
    closures use a 1-field env `{ ptr fn }`. Envs share type defs
    across closures with the same LLVM element layout so the module
    doesn't pile up duplicates.
  - **Lifted fn ABI.** The MIR lowerer's `lowerClosure` now emits
    the lifted function with `fn(ptr env, user_args...) -> ret`.
    Inside the body, capture locals are populated at entry by
    loading from the env via `DerefProj{envTupleType} + TupleProj{
    i+1}`. The rest of the body lowers unchanged — reads of the
    capture name resolve through the regular locals lookup.
  - **Indirect call.** `IndirectCall` on a FnType operand treats
    the operand as the env ptr: load fn from env[0], emit
    `call ret (ptr, user_args...) %fnptr(%env, %user_args)`. The
    user-visible FnType doesn't include env; the emitter widens the
    signature at the call site.
  - **Top-level fn-as-value (thunks).** A bare `FnConst` reaching
    value position (not a direct-call callee) gets wrapped into a
    generated `__osty_closure_thunk_<sym>` that takes env as its
    first arg and delegates to the real symbol. The thunk is
    declared `define private`, is generated once per symbol, and
    goes into the closure value's 1-field env. This keeps `let f =
    topLevelFn; f(x)` working with the uniform ABI.
  - **Escape note.** The env alloca lives on the caller's stack,
    so a closure that escapes beyond the caller's lifetime is UB
    for now. Heap-backed envs (via `osty.gc.alloc_v1`) are planned
    but out of scope here — the common cases (sorting / filtering
    / map-style callbacks consumed in-frame) work without heap.

- **Stage 4 (partially landed — MIR-first IR emission).** The LLVM
  backend prefers the MIR-direct emitter by default on every emit
  mode — raw `llvm-ir`, object, binary. The dispatcher falls back
  automatically on `ErrUnsupported`, so coverage never regresses.

- **Stage 3.9 (landed — concurrency intrinsic runtime mapping).**
  All MIR concurrency intrinsics emitted by the MIR lowerer now
  route to a stable-but-pending Osty runtime ABI in `internal/
  llvmgen`:

  - **Channels**: `chan_make` → `osty_rt_thread_chan_make(i64) → ptr`;
    `chan_send` → `osty_rt_thread_chan_send_<suffix>(ptr, <elem>)`
    (scalar) or `…_send_bytes_v1(ptr, ptr, i64)` (composite); `chan_recv`
    → `…_chan_recv_<suffix>(ptr) → {i64, i64}` (the enum layout
    matches the Option<T> MIR convention); `chan_close` → `…_close(ptr)`;
    `chan_is_closed` → `…_is_closed(ptr) → i1`.
  - **Structured tasks**: `task_group(body)` → `osty_rt_task_group(ptr)
    → <ret>`; detached `spawn(body)` → `osty_rt_task_spawn(ptr) → ptr`;
    scoped `g.spawn(body)` → `osty_rt_task_group_spawn(ptr, ptr) → ptr`;
    `h.join()` → `osty_rt_task_handle_join(ptr)`; `g.cancel()` /
    `g.isCancelled()` → `osty_rt_task_group_cancel` /
    `…_is_cancelled`.
  - **Select**: `thread.select(body)` + the arm-registration intrinsics
    (`s.recv` / `s.send` / `s.timeout` / `s.default`) map to
    `osty_rt_select` and `osty_rt_select_<arm>`.
  - **Cancellation / timing**: `thread.isCancelled()` /
    `checkCancelled()` / `yield()` / `sleep()` →
    `osty_rt_cancel_is_cancelled` / `…_check_cancelled` /
    `osty_rt_thread_yield` / `…_sleep`.
  - **Helpers**: `parallel` / `race` / `collectAll` →
    `osty_rt_parallel` / `osty_rt_task_race` / `osty_rt_task_collect_all`.
  - Concurrency runtime-owned types (`Channel<T>`, `Handle<T>`,
    `Group` / `TaskGroup`, `Select`, `Duration`) all lower to `ptr`
    at the LLVM boundary; `typeSupported` and `llvmType` accept them
    without requiring a declared layout.
  - The runtime itself isn't in-tree yet — generated LLVM text is
    correct and the declare lines are emitted, but the symbols
    won't link until the runtime ships. This matches how
    `osty.gc.alloc_v1` and the list / map / set helpers already
    operate. What changed: concurrency code now reaches the MIR
    emitter instead of falling back to the legacy HIR→AST bridge.

- **Stage 3.10 (landed — top-level globals).** Module-level `let`
  declarations now round-trip through the MIR emitter:

  - Each `mir.Global` emits as `@<name> = global <T> zeroinitializer`
    at the module top.
  - Each `Global.Init` function (a zero-arg fn returning the global's
    declared type — produced by `mir.Lower` for any `pub let x = e`)
    is emitted as a regular LLVM fn definition before user functions.
  - A generated `define private void @__osty_init_globals()`
    constructor calls every init fn in MIR module order and stores
    the result into the matching `@<name>` slot. It's registered
    via `@llvm.global_ctors` at priority 65535 so it runs before
    `main` but after any runtime setup ctor the runtime provides.
  - `GlobalRefRV` on the rvalue side emits a `load <T>, ptr @<name>`
    — the ctor has already initialised the slot by the time user
    code executes.
  - `checkSupported` removes the old hard block; global init fns
    walk through the same `checkFunctionSupported` whitelist as
    user fns so unsupported init shapes still trigger fallback.

- **Stage 3.11 (landed — GC roots / safepoints, opt-in).** Adds
  `Options.EmitGC` to `internal/llvmgen`. When set, the MIR emitter
  instruments every function with the Osty GC runtime contract:

  - **Managed locals** — any local whose MIR type lowers to an LLVM
    `ptr` and is not a function pointer. Concretely: `String`,
    `Bytes`, `List<T>`, `Map<K, V>`, `Set<T>`, `Channel`, `Handle`,
    `Group`, `TaskGroup`, `Select`, `Duration`, `ClosureEnv`.
  - **Entry prologue.** After the alloca/store preamble runs, each
    non-param managed slot is zero-initialised
    (`store ptr null, ptr %lN`) so the GC never observes undef
    memory; then every managed slot (params included) is bound via
    `call void @osty.gc.root_bind_v1(ptr %slot)`. Finally the
    function takes an entry poll via
    `call void @osty.gc.safepoint_v1(i64 <id>, ptr null, i64 0)`.
    Pointer-free functions still take the entry safepoint but don't
    emit any root_bind — this keeps cancellation responsive
    everywhere.
  - **Terminator epilogue.** `ReturnTerm` and `UnreachableTerm`
    release every bound root in reverse bind order before the `ret`
    / `unreachable`. For value-returning functions the return load
    runs *before* the releases so the live value is already in an
    SSA register when its backing slot drops off the root list.
  - **Loop back-edges.** `GotoTerm` and `BranchTerm` emit a
    safepoint whenever they jump to a block with ID ≤ the current
    block's ID. That heuristic catches the standard
    `cond → body → cond` while-loop shape `mir.Lower` produces, plus
    any future loop construct that preserves block-id-monotone CFG
    order.
  - **Runtime declarations.** The emitter pulls
    `@osty.gc.safepoint_v1(i64, ptr, i64)` in on first use; the
    bind/release pair is pulled in together on the first bind or
    release site so the declaration order is stable.

  Passing `null/0` for the safepoint's explicit-root vector relies
  on the runtime's bound-root tracking to locate live references.
  That's the MVP shape — a follow-up can pack per-safepoint precise
  roots if the runtime needs them.

- **Stage 3.12 (landed — composite map value types + ABI fix).**
  `Map<K, V>` intrinsics (`.set` / `.get`) and `IndexProj` on a map
  base now use the runtime's `(ptr map, K key, ptr value_slot)`
  contract uniformly:

  - **`map.set(k, v)`** spills `v` into a stack slot
    (`alloca <VType>` + `store`) and passes the slot pointer as the
    third arg of `osty_rt_map_insert_<keySuffix>`. Works for
    primitive, ptr, and composite `V` — the runtime memcpy's
    `value_size` bytes from the slot into its internal storage.
  - **`m.get(k)` / `m[k]`** allocates an out-slot sized to
    `<VType>`, calls
    `void osty_rt_map_get_or_abort_<keySuffix>(ptr, K, ptr out)`,
    then loads the result back. The old MIR-emitter signature was
    `ptr(ptr, K)` — mismatched with the runtime, which returns
    void and writes into `*out_value`. The rewrite matches the
    runtime contract exactly and enables composite `V` as a
    side-effect.
  - **`IndexProj` on a map base** is now accepted in
    `checkProjectionsSupported`; the read-projection emit loop
    dispatches between List and Map via `isListPtrType` /
    `isMapPtrType` on the running MIR type.
  - The emit loop now tracks `curT` (MIR type of the running
    value) alongside `curLLVM` (LLVM type name) so each
    projection step knows how to interpret its base.

  The old `inttoptr` widening shim in `map.set` is gone — it
  happened to produce syntactically valid IR for primitive `V` but
  passed the value bit-pattern as a pointer, which the runtime
  would have dereferenced into garbage if linked end-to-end.

- **Stage 5 (deferred — still needs parity).** Remove
  `legacyFileFromModule` and the AST-driven emitter. Outstanding
  parity gaps after Stage 3.12: heap-escaping closure envs and
  `DerefProj` on anything other than a closure env. Top-level
  globals crossed off in Stage 3.10; GC roots / safepoints
  crossed off in Stage 3.11; composite map values crossed off in
  Stage 3.12. See the MIR emitter's `checkSupported` and
  `checkRValueSupported` for the current whitelist.

Each stage is an opportunity to tighten the MIR shape: Stage 3 is
where we learn which invariants the backend actually needs, Stage 4 is
where we can remove any MIR node that turned out to be redundant, and
Stage 5 is where we can finally forbid any `internal/llvmgen` code
from importing `internal/ast` at the package-boundary level.

## Out of scope for Stage 1

- SSA. MIR locals can be reassigned. An SSA pass (`mir.ToSSA`) will
  sit on top once downstream passes want it.
- Borrow / ownership analysis. `AddressOfRV` and `DerefProj` are
  in the vocabulary but currently unused.
- Region-based diagnostics. MIR carries `Span`s, but the current
  validator reports only structural errors.
- A production-quality optimizer. The existing HIR optimiser stays
  where it is; MIR may gain its own passes in Stage 2 (copy
  propagation, dead-store elimination, simple peepholes) but this
  patch ships none.
- Concurrency primitives. `spawn`, `taskGroup`, channel sends / recvs,
  and `thread.select` remain HIR-only until their runtime ABI is
  settled; the lowerer emits an unsupported diagnostic when it meets
  one.
