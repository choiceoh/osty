# MIR design (Osty compiler)

Status: Stage 1 — scaffolding, lowering for the currently green LLVM subset,
no backend rewiring yet. The existing `internal/llvmgen` IR→AST bridge stays
in place; MIR is introduced underneath `internal/ir` as a new, backend-
friendly layer that a future llvmgen rewrite can consume directly.

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
    `Select`.

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
    The backend dispatcher (`internal/backend/llvm.go`) now prefers
    `GenerateFromMIR(entry.MIR, opts)` by default on raw `llvm-ir`
    emission, lets requests opt back into the legacy bridge with the
    `legacy-llvmgen` feature, and still allows explicit opt-in on
    object/binary emission via `mir-backend`. On `ErrUnsupported` the
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
    that a program using structs (outside the MVP) is rejected by
    `GenerateFromMIR` with `ErrUnsupported` and accepted by the
    legacy path.

- **Stage 4 (partially landed — MIR-first IR emission).** The LLVM
  backend now prefers the MIR-direct emitter by default for raw
  `llvm-ir` output. The legacy HIR→AST bridge remains callable under
  the `legacy-llvmgen` feature, object/binary emission can still opt in
  explicitly with `mir-backend`, and the dispatcher falls back
  automatically on `ErrUnsupported`.

- **Stage 5.** Remove `legacyFileFromModule` and the AST-driven
  emitter. `internal/llvmgen` now speaks only MIR. Future backends
  (e.g. a bytecode VM, WASM) start from MIR with no AST knowledge.

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
