# D4 — Closure capture lift for self-hosted CoreModule

Status: spec for a parallel PR. Depends on `toolchain/core_clone.osty` (D1) only for its walk helpers — can start as soon as D1 lands. Does **not** depend on D2 (TyArena substitution) or D3 (monomorphize).

## Goal

Port `internal/ir/capture.go` (358 LOC) to self-hosted Osty. After this pass a `CoreModule` contains no `CkClosure` nodes that reference free outer variables — every closure becomes:

1. A fresh top-level `CdFn` (the "lifted body") whose parameter list is `[captures..., original_params...]`.
2. A rewritten use site that constructs an aggregate closure value: `(FnConst(lifted_symbol), capture1, capture2, ...)`.

This is the pass that lets MIR's existing `AggregateKind.AggClosure` (`internal/mir/mir.go:AggClosure`) receive a backend-consumable shape without the Go bridge.

## File layout

- `toolchain/core_capture.osty` — the pass.
- `toolchain/core_capture_test.osty` — tests.

## Public API (required)

```osty
/// CaptureResult is the outcome of the lift pass. On success, `module`
/// is the rewritten CoreModule with closures replaced by aggregate
/// constructions + newly synthesized top-level fns. On failure, `err`
/// names the offending CoreKind / invariant; `module` is left in
/// whatever state the pass reached before the abort.
pub struct CaptureResult {
    pub module: CoreModule,
    pub err: String,
    pub liftedCount: Int,     // how many new top-level fns were produced
    pub closureCount: Int,    // total closure sites processed
}

pub fn coreLiftClosures(m: CoreModule, tys: TyArena) -> CaptureResult
```

## Algorithm

### 1. Walk every function body

For each `CdFn` in `m.decls` (and recursively, for bodies of nested fn decls if Osty allows — check `elab.osty` to confirm), collect every `CkClosure` index reachable from the body.

Use `coreCollectReachable` from D1 as the walker. Filter by `node.kind == CkClosure`.

### 2. Compute free variables per closure

For each `CkClosure` index:

1. Gather the **closure's param names** — read from `children` per `coreClosure` constructor convention (each child is a `CdParam`; its `.name` is the binding).
2. Walk the closure's body (`node.left` per the constructor at `toolchain/core.osty:595`) using D1's `coreCollectReachable`.
3. For each `CkIdent` encountered, consult `node.flags` (`IdentKind`):
   - `IkLocal` / `IkParam` → candidate for capture.
   - `IkFn` / `IkGlobal` / `IkVariant` / `IkTypeName` / `IkBuiltin` → **do not** capture.
4. A local is captured iff it was defined **outside** the closure body — i.e. not in the closure's own params and not in a `CsLet` / `CdLet` whose decl is inside the closure subtree.

Implementation detail: walk the closure subtree once building two sets —
`declaredInside: Set<String>` (param names + local let names) and
`referenced: Set<String>` (every `CkIdent` with `IkLocal`/`IkParam`). Then `captures := referenced \ declaredInside`. Preserve first-encounter order in `captures` so the lifted-fn parameter list is deterministic.

Osty doesn't have `Set<T>` in the prelude subset the self-host uses — fall back to a pair of parallel `List<String>` + `coreCapContainsString` helper (matches the pattern in `mir_validator.osty`).

### 3. Resolve each capture to its outer definition

For each captured name, find the corresponding outer `CdLet` / `CdParam` / `CsLet` in the enclosing function scope. Record:
- The capture's **source name** (string)
- The capture's **type index** (TyArena) — read from the outer decl's `ty` field.

If a capture cannot be resolved to an outer local, abort with `err = "coreLiftClosures: unresolved capture <name> in <fn>"`.

### 4. Synthesize a lifted fn decl

For closure at index `c` inside parent fn `F`:

1. Choose a mangled symbol: `"{F.name}$closure${i}"` where `i` is a per-parent counter. Make sure this symbol does not already exist in `m.decls`.
2. Clone the closure's body via **D1's `coreCloneSubtree`** into a fresh `CoreArena` dedicated to the lifted fn. Actually — simpler: clone into `m.arena` (the same arena) since all decls share it.
3. Build the lifted fn's parameter list: for each `captures[k]` produce a `coreParam(arena, name, tyIdx, -1, 0, 0)`; then append clones of the closure's original params.
4. Construct `coreFnDecl(arena, mangledName, "", generics=[], params=captures++originalParams, retTy=closure.retTy, body=clonedBody, fnTy=updatedFnTy, isPub=false, 0, 0)`.
5. Append the lifted fn index to `m.decls`.

For the `fnTy` slot: build a new `TyArena` entry via `tyFn(tys, [captureTypes...] ++ [originalParamTypes...], retTy)`. Do **not** try to reuse the closure's original `fnTy` — it does not include the capture prefix.

### 5. Rewrite the closure use site

Replace the `CkClosure` node's kind + payload in place with an aggregate construction:

- New kind: `CkStruct` (MIR will see this as `AggregateKind = AggClosure` once MIR lowering lands in D3 — the kind → agg-kind mapping is the lowerer's job, not this pass's).
- `node.text / name`: the mangled lifted symbol.
- `node.owner`: reserved — use the parent fn's name for debug.
- `node.children`: `[fnConst_idx, capture_operand_idxs...]` where:
  - `fnConst_idx` is a freshly added `CkIdent` with `flags = IkFn` and `text = mangledName`.
  - Each capture operand is a freshly added `CkIdent` referring to the outer local by name.
- `node.typeArgs`: empty (closures carry no explicit type args post-lift).
- `node.ty`: unchanged — the closure's `fn(...)` type is what the use site sees.

Rationale: rather than introduce a new `CkClosureConstruct` kind, we reuse `CkStruct` with a convention that D3 will recognise. This keeps the CoreKind enum stable.

**Alternative (cleaner, requires a CoreKind addition):** add `CkClosureRef` to `core.osty` and dispatch on it in D3. Prefer this if touching `core.osty` is acceptable in the same PR; otherwise use the `CkStruct` convention and document it.

### 6. Handle nested closures

A closure inside a closure must be lifted too. Process closures in **post-order** (innermost first) so that by the time you lift an outer closure, the inner one has already been replaced by its aggregate form and no longer introduces free variables that reference the inner-closure's scope.

## Edge cases

- **Closure with zero captures**: still lift it to a top-level fn (keeps the codegen uniform — no conditional path in D3). The aggregate in this case is `(FnConst, )` with an empty capture list.
- **Closure references itself** (recursive closure): not currently supported in Osty v0.5 — if you encounter a `CkIdent` whose name equals the closure's let-binding, abort with `err = "coreLiftClosures: recursive closure not supported"`. Add a waiver if needed.
- **Closure captures `self`** (inside a method): `self` is a normal param — same path as any other capture. The lifted fn takes the receiver as its first capture parameter.
- **Closures in top-level `script` (non-fn) context**: process them the same way, parenting to a synthetic `__script__` name.

## Verification

Three mechanical checks:

1. After the pass, `coreCollectReachable(m.arena, root)` for any root finds **zero** `CkClosure` nodes.
2. For every newly-added lifted fn, the fn's body contains no `CkIdent` with `IkLocal`/`IkParam` referencing a name not bound in the fn's own params.
3. `liftedCount == closureCount` on success.

## Tests

Required cases (mirrors `internal/ir/capture_test.go`):

1. **Zero captures**: `|x| x + 1` inside `fn f() { let c = |x| x + 1; c(2) }` — lifts with no capture prefix.
2. **Single capture**: `|x| x + n` where `n` is an outer let.
3. **Multiple captures in reference order**: `|x| a + b + c` where `a`, `b`, `c` are outer lets — capture list must preserve first-encounter order.
4. **Shadowed binding**: `let x = 1; let c = |x| x + x` — no captures (inner `x` shadows).
5. **Captures crossing a block boundary**: `let n = 1; if cond { let c = |x| x + n }` — `n` captured.
6. **Nested closures** (post-order correctness): `|x| (|y| x + y)` — inner lifts first, captures `x`; outer lifts, captures nothing except the inner's lifted aggregate.
7. **Recursive closure** aborts with the documented error.
8. **Method body with closure capturing `self`**: `fn m(self) { let c = |x| x + self.n }`.

## Out of scope

- **Type substitution on captures**: captures always have their original (possibly generic) type. Substitution is D2's job.
- **Storage optimisation**: D4 doesn't try to stack-allocate capture structures. The lowerer (D3) and MIR backend decide allocation.
- **Closure conversion for FFI boundary**: Osty closures passed to Go-space are not supported (not a v0.5 surface).

## Interaction with D1/D2/D3

- D1 (`core_clone.osty`) provides `coreCloneSubtree` — D4 uses it to clone closure bodies into lifted fns.
- D2 is orthogonal — D4 preserves TyArena indices as-is.
- D3 (monomorphize) runs **after** D4: the lifted fns are normal top-level fns that the monomorphizer specializes exactly like any other fn.

## Rough effort

- Pass itself: ~400 LOC.
- Free-variable computation: ~200 LOC (needs the scoping walker).
- Tests: ~300 LOC.
- Total: ~900 LOC, ~1 day of focused work.

## PR title suggestion

`feat(selfhost): D4 — closure capture lift for CoreModule`
