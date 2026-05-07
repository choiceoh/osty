## 2a. Type Inference Algorithm

This section specifies the algorithm that assigns a type to every
expression, binding, and declaration. It complements §2 (type system)
and §3 (declarations): §2 tells you *what* types exist, §3 tells you
*where* annotations appear, and this section tells you *how* types are
deduced when no annotation is present.

The rules here are mechanically executed by the self-hosted checker
in [`toolchain/check.osty`](../toolchain/check.osty). Each rule in the
tables below names a specific function or line range in that file;
readers are expected to cross-reference the two. Runtime observation
is provided by `osty check --inspect FILE.osty` (§13 Tooling and
[`internal/check/inspect.go`](../internal/check/inspect.go)), which
emits one record per expression annotated with the rule applied.

### 2a.1 Algorithm class

**Bidirectional typing with local unification and monomorphization on
generic instantiation.**

- **Bidirectional.** Judgments come in two modes:
  - `Γ ⊢ e ⇒ T` — *synth*: with no expected type, compute `T` from
    `e` alone.
  - `Γ ⊢ e ⇐ T` — *check*: with expected type `T` (a "hint" flowing
    from the enclosing context), verify or refine `e`'s type.
  A context `Γ` is the set of in-scope bindings plus the current
  return-type obligation.
- **Local unification.** Unification runs eagerly at each binary /
  list / match / call site where two types must agree. There is **no**
  global constraint graph; each `frontCheckUnify(Γ, t₁, t₂)` call
  resolves on the spot or produces `Invalid` (the poisoned sentinel).
- **Monomorphization.** Generic type parameters are resolved at each
  call site by unifying actual argument types against parameter types.
  Each resolved substitution list is recorded for the backend to emit
  one specialized copy per distinct instantiation.

The choice is deliberate: unification-only (Hindley-Milner) would
require a global solver and forbid mid-file annotations as hints;
constraint-based typing (as in Rust/Scala) would require an inference
variable domain and solver. Osty keeps neither because:

- Every public fn/struct/enum signature is **fully annotated**
  (§3.2, §3.3). Hints are plentiful and always available for
  top-level synth.
- Literal polymorphism (§2.2) is handled by a sentinel type
  (`UntypedInt` / `UntypedFloat`) that collapses on the way up.
- Generic inference is confined to call sites (§2.7), where one
  pass over parameter/argument pairs suffices.

### 2a.2 Judgment forms

```
                         (synth)         (check)
                         ───────         ───────
    Γ ⊢ e ⇒ T       Γ ⊢ e ⇐ T
```

`T` is a member of the semantic type language described in §2:
primitive, `Untyped {Int|Float}`, `Tuple`, `Optional`, `FnType`,
`Named(Sym, Args)`, `TypeVar(Sym, Bounds)`, `Builder`, or the
poisoned sentinel `Invalid`.

Direction switches happen only at the boundaries listed in §2a.3.
Inside a mode, the algorithm traverses the AST structurally.

`Invalid` is the **unit of absorption** for diagnostics: once a
sub-expression yields `Invalid`, every enclosing rule that inspects
it short-circuits to `Invalid` without emitting a second error. This
is how the checker avoids cascade noise on malformed input.

### 2a.3 Mode switching

The algorithm is in *check* mode whenever one of these contexts
supplies a hint:

| Site                         | Hint provided                  | Reference |
|------------------------------|--------------------------------|-----------|
| `let x: T = e`               | `T`                            | `frontCheckLetDecl` @ [check.osty:809](../toolchain/check.osty) |
| `fn f(...) -> T { e }`       | `T` at the body's tail         | `frontCheckFnDecl` @ [check.osty:854](../toolchain/check.osty) |
| `f(..., e_i, ...)` (typed fn)| `params[i]` after substitution | `frontCheckCallSig` @ [check.osty:2420](../toolchain/check.osty) |
| `if c { e₁ } else { e₂ }`    | parent hint flows to both      | `frontCheckIfHint` @ [check.osty:2131](../toolchain/check.osty) |
| `match s { p => e, … }`      | parent hint flows to each arm  | `frontCheckMatchHint` @ [check.osty:2734](../toolchain/check.osty) |
| `[e₁, e₂, …]` with `List<T>` | element hint `T`               | `frontCheckList` @ [check.osty:2052](../toolchain/check.osty) |
| `(e₁, e₂, …)` with tuple hint| element `i`'s hint             | `frontCheckTuple` @ [check.osty:2094](../toolchain/check.osty) |
| `S { f: e, … }` (struct lit) | field's declared type          | `frontCheckStructLitHint` @ [check.osty:2661](../toolchain/check.osty) |
| `|params| body` with fn hint | params + return from fn type   | `frontCheckClosure` @ [check.osty:3868](../toolchain/check.osty) |
| block tail expression        | block's hint                   | `frontCheckBlockHint` @ [check.osty:1664](../toolchain/check.osty) |
| parenthesized expression     | parent hint passthrough        | `frontCheckExprHint` @ [check.osty:1872](../toolchain/check.osty) |

Everywhere else the algorithm is in *synth* mode. The top-level
dispatcher is [`frontCheckExprHint`](../toolchain/check.osty) at
`check.osty:1840` — it accepts an optional hint and routes to the
rule for the AST node kind.

### 2a.4 Rules for expressions

Each rule is keyed by AST node kind. Column **Rule** is the label
emitted by `--inspect`. Column **Ref** points at `check.osty`.

| Rule            | Node         | Synth / Check behavior                                                                                                                                                         | Ref |
|-----------------|--------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----|
| `LIT-INT`       | `IntLit`     | Synth `UntypedInt`. If hint is `Float`, coerce to `Float`.                                                                                                                     | 1845 |
| `LIT-FLOAT`     | `FloatLit`   | Synth `UntypedFloat`. If hint is `Float`, coerce to `Float`.                                                                                                                   | 1851 |
| `LIT-STRING`    | `StringLit`  | Synth `String`. Interpolation segments are each checked `⇐ Display` (§17).                                                                                                      | 1857 |
| `LIT-BOOL`      | `BoolLit`    | Synth `Bool`.                                                                                                                                                                  | 1860 |
| `LIT-CHAR`      | `CharLit`    | Synth `Char`.                                                                                                                                                                  | 1863 |
| `LIT-BYTE`      | `ByteLit`    | Synth `Byte`.                                                                                                                                                                  | 1866 |
| `VAR`           | `Ident`      | Synth = lookup of `name` in `Γ`. For enum variants, the hint disambiguates the owner when shadowed.                                                                            | 1869, 1930 |
| `PAREN`         | `ParenExpr`  | Pass hint through, result = inner.                                                                                                                                             | 1872 |
| `BLOCK`         | `Block`      | Recurse into statements; tail expr gets block's hint; block type = tail type or `()`.                                                                                           | 1875, 1664 |
| `UNARY`         | `UnaryExpr`  | Synth operand; result from op (`!`→Bool, `-`→numeric, `~`→integer).                                                                                                            | 1878, 1961 |
| `BINOP`         | `BinaryExpr` | Synth both operands; `frontCheckUnify` on them; result from op table.                                                                                                          | 1881, 1990 |
| `IF`            | `IfExpr`     | Check `cond ⇐ Bool`. Check each branch under parent hint. Unify branch types for the whole-expr type.                                                                           | 1884, 2131 |
| `IF-LET`        | `IfExpr`     | Same as `IF`, but additionally: synth RHS, bind pattern per §4.8, then check `then` under parent hint.                                                                          | 2131 |
| `CALL`          | `CallExpr`   | Synth callee → obtain `FnSig`. Resolve generic substitution against argument types. Check each arg under its substituted parameter type. Record instantiation. Result = return type. | 1887, 2164, 2420 |
| `METHOD-CALL`   | `CallExpr` on `FieldExpr` | Synth receiver → method lookup → treat as `CALL` with receiver as first arg.                                                                                        | 2164 |
| `LIST`          | `ListExpr`   | Hint shape `List<T>`: check each elem `⇐ T`. Else synth first elem, then `frontCheckUnify` rest against it; element type = join.                                                | 1890, 2052 |
| `TUPLE`         | `TupleExpr`  | Hint shape `(T₁,…)`: check elem `i` `⇐ Tᵢ`. Else synth each; result = tuple of synth'd elements.                                                                               | 1893, 2094 |
| `MAP`           | `MapExpr`    | Hint `Map<K,V>`: check keys `⇐ K`, values `⇐ V`. Else synth first entry and unify.                                                                                               | 1896 |
| `RANGE`         | `RangeExpr`  | Synth endpoints, unify to a numeric type, result = `Range<T>`.                                                                                                                 | 1899, 2108 |
| `FIELD`         | `FieldExpr`  | Synth receiver → `frontCheckFieldLookup` on owner — result = field type with receiver substitutions applied. `?.` on `T?` unwraps Optional and re-wraps result.                 | 1902, 2603 |
| `INDEX`         | `IndexExpr`  | Synth collection; branch by kind: `List<T>` → `T`, `Map<K,V>` → `V?`, `String` → `Char`.                                                                                         | 1905, 2631 |
| `MATCH`         | `MatchExpr`  | Synth scrutinee. For each arm, bind pattern against scrutinee type, then check `body ⇐ hint`. Unify arm types. Diag on non-exhaustive patterns (E0731).                          | 1908, 2734 |
| `STRUCT-LIT`    | `StructLit`  | Hint or spelled type → resolve Named. Check each field value `⇐ declared field type`. Diag on missing/duplicate fields.                                                         | 1911, 2661 |
| `CLOSURE`       | `ClosureExpr`| Parameter types come from (1) annotation if present, else (2) `fn`-hint param types, else (3) left as `Invalid`. Body synth'd or checked against hint return.                    | 1914, 3868 |
| `QUESTION`      | `QuestionExpr` | `e?` on `T?` → `T` after propagating `None` to caller. On `Result<T,E>` → `T` after propagating `Err` to caller.                                                                | 1917 |
| `TURBOFISH`     | `TurbofishExpr`| Pass through base synth; explicit type args seeded into `RECORD-INSTANTIATION`.                                                                                               | 1920 |
| `CAST`          | `as`-expr    | Check source is convertible per §2.2; result = target.                                                                                                                         | (in exprHint fallthrough) |
| `BUILDER`       | `Type.builder()` chain | Auto-derived — see §3.4. `.field(v)` returns `Builder<T>` with field added to set. `.build()` returns `T` and diag on missing required.                               | field lookup @ 1020 |

### 2a.5 Rules for statements and declarations

Statement-level rules feed context into the expression rules above.
Their entry is `frontCheckStmt` @ `check.osty:1707` and
`frontCheckLetDecl` @ `check.osty:809`.

| Rule        | Node           | Behavior                                                                                                                                | Ref |
|-------------|----------------|-----------------------------------------------------------------------------------------------------------------------------------------|-----|
| `LET`       | `LetStmt` / `LetDecl` | With annotation `T`: check `rhs ⇐ T`, bind `pattern: T`. Without: synth `rhs ⇒ T'`, default untyped literals, bind `pattern: T'`.  | 809  |
| `ASSIGN`    | `AssignStmt`   | Synth LHS and RHS; require LHS is lvalue and mutable; `frontCheckIsAssignable` on (LHS, RHS).                                            | 1707 |
| `RETURN`    | `ReturnStmt`   | Check value `⇐ env.returnType`. Empty return requires `env.returnType == ()`.                                                          | 1707 |
| `FOR`       | `ForStmt`      | Synth iterable; bind loop variable; body is statement (no result type).                                                                  | 1707 |
| `BREAK`, `CONTINUE` | —    | Produce `Never`; require enclosing loop.                                                                                                 | 1707 |
| `FN-DECL`   | `FnDecl`       | Build `Γ` with params typed from signature; check body `⇐ returnType`.                                                                  | 854  |
| `STRUCT-DECL`, `ENUM-DECL`, `INTERFACE-DECL`, `TYPE-ALIAS-DECL` | — | Pass 1 (`frontCheckCollectDecls` @ 369) registers shape; Pass 2 ignores (only signatures, no bodies). | 369  |

### 2a.6 Untyped-literal defaulting

Untyped literals are sentinel types that collapse on the way up:

1. If a check-mode hint names a compatible concrete type, the literal
   takes that type.
2. If a binary op unifies `UntypedInt` with a concrete integer type
   `T`, both sides become `T`.
3. If nothing fixes the type before the enclosing statement completes,
   default `UntypedInt` → `Int` and `UntypedFloat` → `Float` (§2.2
   last paragraph).

This is the only place the checker inserts a default. Everywhere else,
an unresolved type at a statement boundary is a diagnostic.

### 2a.7 Generic instantiation

Synth of a `CALL` expression with a polymorphic callee:

1. Extract the callee's `FnSig` including generic parameter list
   `[X₁, …, Xₙ]` and bounds.
2. For each positional argument `aᵢ`, synth its type `Aᵢ`, then unify
   `Aᵢ` against the declared parameter type `Pᵢ` treating each `Xⱼ`
   as a free variable. Extend the substitution `σ` with each `Xⱼ ↦ Tⱼ`
   discovered.
3. If the enclosing context provides a return-type hint, unify it
   against the declared return type under `σ`, possibly fixing any
   `Xⱼ` not pinned by arguments.
4. If any `Xⱼ` is still unresolved, it is either underdetermined
   (diag E0724) or intentionally left free when the call site does
   not require concretization.
5. Record `(callSite → [T₁, …, Tₙ])` in `Result.Instantiations`.
   Backends read this map to emit one monomorphized copy per distinct
   tuple of type arguments.

Bounds (`T: Interface`) are checked structurally via
`frontCheckSatisfiesInterface` @ `check.osty:1518`. No method-level
dispatch: interfaces are satisfied by shape.

### 2a.8 Unification

`frontCheckUnify(Γ, a, b)` is a shallow, local routine (definition at
`check.osty:1634`). It accepts two type strings and returns their
meet:

- Identical types: return the type.
- One side `Invalid`: return `Invalid` (absorption).
- One side `Untyped*`, the other compatible concrete: return concrete.
- Both `Untyped*` of the same flavor: return the wider untyped.
- `Never` on one side: return the other.
- Otherwise: `Invalid` (the caller is expected to have emitted a
  diagnostic).

No substitution accumulation; no occurs check; no recursive
constraint deferral. The algorithm is correct precisely because
generic resolution (§2a.7) always happens at a single, well-scoped
call site, and every other use of `frontCheckUnify` operates on
already-substituted types.

### 2a.9 Diagnostics interaction

The checker never throws. Every type error produces a `Diagnostic`
(§ERROR_CODES.md). `Invalid` is used to mark the erroneous node, and
the absorption rule (§2a.2) ensures exactly one diagnostic per root
cause — cascades are silenced by the sentinel.

### 2a.10 Runtime observation (`osty check --inspect`)

`osty check --inspect FILE.osty` runs the normal checker and then
walks the AST a second time, emitting one record per expression:

```
LINE:COL-LINE:COL  RULE          Node            Type         [← hint=HINT]  [notes]
```

Flags:

- `--json` (global) produces NDJSON — one record per line — suitable
  for machine consumption.
- `--explain` (global) is unrelated; that flag prints diagnostic code
  documentation.

The inspector does not re-run inference. It reads
`Result.Types` / `LetTypes` / `SymTypes` / `Instantiations` already
computed by the checker and classifies each AST node by the rule that
produced its recorded type. Implementation:
[`internal/check/inspect.go`](../internal/check/inspect.go).

Example:

```
$ osty check --inspect examples/basic.osty
1:5-1:10   LET           let             Int         (from annotation)
1:13-1:14  LIT-INT       1               Int         ← hint=Int
2:5-2:10   LET           let             Float       (synth)
2:13-2:22  BINOP         2.0 + x         Float
2:13-2:16  LIT-FLOAT     2.0             UntypedFloat → Float
2:19-2:20  VAR           x               Int
2:19-2:20  CALL          f(1, "hi")      List<String>
                         instantiated T=String
```

Use this when:

- You need to debug why a literal resolved to `Int` instead of
  `Float`.
- You want to see what type argument was chosen at a generic call.
- You are verifying that the algorithm in §2a.3–§2a.7 is actually
  what the checker does on your source.

### 2a.11 Known limitations

- **No row polymorphism.** Struct types are nominal; there is no
  inference of anonymous record types.
- **No higher-rank polymorphism.** Closures stored in fields must be
  monomorphic at their declaration site.
- **Untyped defaulting is local.** If a value flows out of a block
  without a context, it defaults immediately — it cannot be pinned
  retroactively by a later use. This is by design; it matches the
  order in which the checker walks the AST and matches user intuition.

These are deliberate trade-offs. Lifting any of them would require
changing the algorithm class (for example, to constraint-based), and
would propagate to every backend and to the LSP.

### 2a.12 Pointers

- Spec: this file.
- Reference implementation: [`toolchain/check.osty`](../toolchain/check.osty).
- Go-side adapter (no inference logic): [`internal/check/host_boundary.go`](../internal/check/host_boundary.go).
- Observation tool: [`internal/check/inspect.go`](../internal/check/inspect.go), invoked via `osty check --inspect`.
- Diagnostic codes produced: `E0300`–`E0399` (types/patterns), `E0700`–`E0799` (type-check).
- Change history: see §18.
