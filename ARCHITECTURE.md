# Architecture

High-level map of the Osty front-end. For spec decisions see
`OSTY_GRAMMAR_v0.4.md`; this document covers **implementation** layout.

## Pipeline

```
   source bytes
        │
        ▼
   ┌─────────┐    tokens (with LeadingDoc, string parts)
   │  lexer  │ ─────────────────────────────────────┐
   └─────────┘                                       │
                                                     ▼
                                              ┌──────────┐    *ast.File
                                              │  parser  │ ──────────┐
                                              └──────────┘           │
                                                                     ▼
                                                              ┌──────────┐  Result{Refs, TypeRefs, Diags}
                                                              │  resolve │ ───┐
                                                              └──────────┘    │
                                                                              ▼
                                                                       ┌──────────┐  Result{Types, SymTypes, Diags}
                                                                       │   check  │ ───┐
                                                                       └──────────┘    │
                                                                                       ▼
                                                                                ┌──────────┐   Go source
                                                                                │   gen    │   warnings (L0xxx)
                                                                                │   lint   │
                                                                                └──────────┘
```

Each stage produces diagnostics as it goes; they accumulate in a single
`[]*diag.Diagnostic` that the CLI renders at the end.

## Package cheat sheet

### `internal/token`
Token kinds and byte-offset/line/column positions. Keywords and operator
lookup tables live here. No logic — pure data types.

### `internal/lexer`
Scans UTF-8 source into a token stream. Notable concerns handled here:

- **ASI (automatic semicolon insertion)** per v0.2 R2. Tracks two cases:
  - Case 1: the previous token permits a trailing newline (`,`, `->`,
    `::`, binary ops, etc.) → suppress. Per O3 `.` and `?.` are **not**
    in this list so a trailing `.` is a hard error.
  - Case 2: the **next** token is one of `)` `]` `}` `.` `?.` `,` `..`
    `..=` `??` or a binary op → suppress. Implemented by
    `nextTokenSuppressesTerm` peeking the next non-whitespace char.
- **Triple-quoted strings** are lexed in a single streaming pass: the
  lexer collects `tripleLine{indent, parts}` values, then applies the
  closing-line indent as a common prefix. Interpolations are captured
  in-place so their source positions are accurate (the old re-lexing
  approach produced `1:1` positions inside triple strings — fixed).
- **Doc comments (`///`)** are accumulated in `pendingDoc` and attached
  to the next significant token's `LeadingDoc` field, but only when
  the following token sits on the line immediately after the last
  `///` (no blank-line separation).
- **Lex errors** become `diag.Diagnostic` directly via `l.errorf` or
  the code-aware `l.errorCode` helper.

### `internal/ast`
Node types for every syntactic construct. Every helper sub-type
(`Param`, `GenericParam`, `Variant`, `Receiver`, `Field`, `Arg`,
`MapEntry`, `MatchArm`, `StructLitField`, `StructPatField`) implements
the `ast.Node` interface (`Pos()`, `End()`), which means `Symbol.Decl`
in the resolver can be typed as `ast.Node` — no `any` type erasure.

Notable design choices:

- **`Block` is both `Stmt` and `Expr`.** Blocks can appear in both
  positions depending on context; the parser picks the right one.
- **`Param` carries either `Name` or `Pattern`.** Top-level / method
  params always use `Name`; closure params (per SPEC_GAPS G4) may use
  `Pattern` for destructuring.
- **`StringLit.Parts`** is a mixed slice of literal text and embedded
  AST expressions (from `{expr}` interpolations).

### `internal/parser`
Hand-written recursive-descent with a Pratt-style operator-precedence
loop for expressions. Precedence follows v0.2 R1 verbatim.

- **Non-associative operators** (comparison `==` `<` `>` …, range
  `..` `..=`) use `rbp = lbp + 1` so the inner parse bails before
  consuming a same-level operator. The outer loop then detects and
  errors on `a < b < c`.
- **`>>` splitting** for nested generics (`Map<String, List<T>>`) is
  implemented in `expectTypeGT`, which rewrites the current token
  from `SHR` → `GT` in-place and leaves one `>` to be consumed again.
- **Struct-literal ambiguity in `if`/`for`/`match` heads** (v0.2 R3)
  is handled via a `noStructLit` flag flipped around head parsing.
  Parens reset it so `if (Point {x: 1}) == p {...}` parses.
- **Error recovery**: on any parse error inside a block the parser
  `syncStmt`-s to the next newline / `}` / known statement-starter
  keyword. At the top level `syncDecl` jumps to the next `pub`/`fn`/
  `struct`/etc. This keeps a single malformed declaration from
  cascading into ten.
- **Cascade suppression**: `suppressedAt map[int]bool` (keyed by
  byte offset) makes repeated errors at the same position collapse
  to one.

### `internal/diag`
Structured diagnostics plus the renderer.

- `Diagnostic{Severity, Code, Message, Spans, Notes, Hint}` —
  consumable by the CLI, by tests, and (eventually) by the LSP as
  `textDocument/publishDiagnostics` payload.
- `Formatter.FormatAll` sorts by primary position before rendering so
  diagnostics appear in source order regardless of emission order.
- The renderer understands **multi-span diagnostics**: a primary span
  (`^` in severity color) and zero or more secondary spans
  (`-` in blue, with labels like "previous declaration here"). Lines
  are merged when multiple spans share one, otherwise rendered
  separately with a `...` ellipsis.
- **Unicode-correct caret placement**: column counts are in runes,
  so multi-byte source (CJK, emoji) aligns cleanly under the
  appropriate character.
- Golden snapshot tests in `internal/diag/golden_test.go` lock the
  rendering format; update with `go test … -update`.

### `internal/resolve`
Name resolution. Three entry points: `File` (single-file), `Package`
(directory of files sharing a namespace, loaded via `LoadPackage` +
`ResolvePackage`), and `Workspace` (tree of packages connected by `use`
edges, driven by `NewWorkspace` + `ResolveAll`). Each file goes through
a 3-pass design:

1. **`declareUse`** registers `use` aliases.
2. **`declareTopLevel`** inserts every top-level `fn`/`struct`/
   `enum`/`interface`/`type`/`pub let` symbol, enabling forward
   references between them.
3. **`resolveDecl`** walks each body, opening child scopes for
   generics, parameters, let bindings, closure params, and match
   arms.

`methodCtx{selfType, inMethod, selfSym}` bundles the three related
bits of state that control `self` / `Self`. Callers push a new context
via `enterMethod` / `enterTypeBody`, which return a restore closure —
the old three-separate-flags pattern that required manual save/restore
is gone.

Notable rules:

- Enum variants are visible at file scope (v0.2 §3.5 "bare name
  within the same package").
- Bare pattern identifiers starting with uppercase are treated as
  variant references when a matching variant exists; otherwise they
  bind a new name.
- Typo suggestions use Levenshtein edit distance ≤ 2 against every
  symbol in the active scope chain.

### `internal/types`
Pure data types for Osty's semantic world — named types, type
variables, function signatures, tuples, optionals, the error sentinel.
Shared by `check`, `gen`, and `lsp` so none of them needs to redefine
type shapes. No logic here; `compat.go` holds the small structural
comparison helpers that every consumer needs.

### `internal/check`
Type checker. Two passes: `collect.go` gathers struct / enum /
interface / type-alias shapes (fields, methods, variant payloads,
generics) so pass 2 can forward-reference; `expr.go` + `stmt.go` walk
bodies producing per-expression types in `Result.Types` plus
per-symbol types in `Result.SymTypes`. Entry points mirror
`resolve`: `File`, `Package`, `Workspace`.

The main v0.4 front-end static guarantee hooks are wired here:

- generic call sites are recorded in `Result.Instantiations`; the IR
  lowerer forwards these onto `ir.CallExpr.TypeArgs` and leaves struct
  literal / variant / type-annotation arguments on the corresponding
  `NamedType.Args` slots. `ir.Monomorphize` (invoked from
  `backend.PrepareEntry` before `ir.Validate`) materializes one
  specialization per reachable (generic fn, concrete type-args) tuple
  *and* per (generic struct/enum, concrete type-args) tuple, so LLVM
  only ever sees concrete symbols. Function symbols use Itanium
  `_Z…` mangling; nominal type symbols use the `_ZTS…` track for easy
  distinction. Generic variant constructor calls (e.g.
  `Maybe.Some(42)`) whose surface type the checker leaves as `*ErrType`
  are recovered inside the monomorphizer via main's existing
  `isUnresolvedType` + `inferVariantLiteralType` path. Method-local
  generic parameters (e.g. `fn map<U>(self, f)`) are specialized on
  demand via `rewriteGenericMethodCall` + `emitMethodSpecialization`,
  appended to the owner's Methods list so the existing llvmgen dispatch
  keeps working; original generic method templates are stripped by a
  final cleanup pass. Generic interface declarations
  (`interface Iterator<T>`) are specialized through the same typeQueue
  path as struct/enum via `requestInterfaceType` +
  `emitInterfaceSpecialization`. Bare function-pointer turbofish stays
  out of scope for this phase,
- structural interface satisfaction checks composed interfaces,
  `Self`-typed signatures, generic receiver substitution, params, and
  generic bounds,
- match exhaustiveness emits `E0731` and synthesizes witnesses for
  closed product/sum shapes, with `E0740` for unreachable arms,
- auto-derived `default()`, `builder()`, `toBuilder()`, setter chains,
  and `build()` obligations are checked in `builder.go`,
- method references (`obj.method` as a value) lower to receiver-free
  function types,
- keyword/default-aware local and cross-package function calls use the
  declared parameter metadata when available, while erased function
  values are positional-only with exact arity,
- closure parameter destructuring binds irrefutable tuple/struct
  patterns and rejects refutable patterns with a stable diagnostic.

The v0.4 language-decision sweep is closed in `SPEC_GAPS.md`; remaining
work is implementation backlog rather than broad checker architecture
gaps.

#### Type inference algorithm

Bidirectional typing with local unification and monomorphization on
generic instantiation. The full specification, including the rule
table and a line-level mapping to the self-hosted implementation,
lives at `LANG_SPEC_v0.4/02a-type-inference.md`. The reference
implementation is `toolchain/check.osty`; `host_boundary.go` is a
pure adapter that materializes the checker's output as
`Result.Types`, `Result.LetTypes`, `Result.SymTypes`, and
`Result.Instantiations` — it contains no inference logic of its own.

For runtime inspection use `osty check --inspect FILE.osty`. The
inspector (`inspect.go` + `inspect_format.go`) walks the AST after
the checker finishes and emits one record per expression naming the
rule that produced its type, the contextual hint the checker had,
and any generic instantiation recorded for the site. It does not
re-run inference — it classifies the already-typed nodes so the
algorithm is directly observable against real code. `--json`
switches the output to NDJSON.

### `internal/stdlib`
Built-in prelude symbols injected into every file before resolution.
`NewPrelude` returns the root scope; individual modules are Osty
source files under `modules/` with primitive method stubs under
`primitives/`. Tier 1 is loadable by the front-end; the v0.4 sweep is
about moving more §10 protocol prose into checked `.osty` stubs and
pinning any edge cases that fall out of that exercise.

### `internal/format`
Canonical-style formatter. `format.Source` reparses → pretty-prints →
reparses for idempotency. Comments and blank-line structure are
preserved through a `scan.go` pass that pairs trivia to nodes, then
`trivia.go` drives the blank-line policy (≥2 collapse to 1). The
printer covers every AST node kind; no config — one canonical style
per spec §13.3. Wired as `osty fmt` with `--check` / `--write`.

### `internal/lint`
Style and correctness warnings over a resolved tree. Every diagnostic
is `diag.Warning` severity with an `Lxxxx` code; nothing blocks
compilation. Rules implemented today:

| Code | Rule |
|---|---|
| L0001 | unused `let` binding |
| L0002 | unused fn / closure parameter |
| L0003 | unused `use` alias (package-aware) |
| L0010 | inner binding shadows outer |
| L0020 | statement after terminating return/break/continue |
| L0030 | type name not UpperCamelCase |
| L0031 | fn / let / param name not lowerCamelCase |
| L0032 | enum variant name not UpperCamelCase |

`File`, `Package`, and workspace-driven usage are all supported so
cross-file references to a `use` alias don't look unused locally.
Wired as `osty lint` with `--strict` (CI mode: exit 1 on any
warning).

### `internal/bootstrap/gen`
Developer-only Osty→Go transpiler used exclusively to regenerate
`internal/selfhost/generated.go` from the `toolchain/*.osty` sources.
Not reachable from the public `osty` CLI; the sole caller is
`cmd/osty-bootstrap-gen` (invoked by `internal/selfhost/gen_selfhost.go`
via `go run`). Consumes `*ast.File` + `*resolve.Result` +
`*check.Result` and emits source-mapped Go. Will be retired once the
LLVM backend can self-host the toolchain directly; no new feature work
belongs here.

### `internal/lsp`
JSON-RPC language server on stdio. Lifecycle (`initialize` / `shutdown`
/ `exit`) and the document store live in `server.go`; per-method
handlers live in `handlers.go`. Implemented: `textDocument/hover`,
`textDocument/definition`, `textDocument/formatting`,
`textDocument/documentSymbol`, completion, references, rename, signature
help, inlay hints, code actions, workspace symbols, and semantic tokens.
The server keeps JSON-RPC, workspace indexing, AST traversal, and byte/rune
adapter glue in Go, while pure editor policy such as UTF-16 position/range
conversion, semantic-token legend classification/delta encoding, completion
kind/sort buckets, completion prefix/dot context, declaration-name lookup,
outline/workspace symbol kind selection and sorting, cursor/range checks,
signature-label rendering, diagnostic payload projection, code-action
filtering, URI/reference-location ordering, organize-import helper policy, and
fix-all overlap resolution is authored in
`toolchain/lsp.osty` and exposed through the checked-in host boundary. Each
document change re-runs the full front-end (`parser.ParseDiagnostics` →
`resolve.File` → `check.File`); caching is by source identity, not incremental
— the front-end is fast enough that re-analysis is cheaper than
change-tracking at this stage. Wired as `osty lsp`.

### `internal/manifest`
Project manifest (`osty.toml`) reader, validator, and writer (spec
§13.2). Owns a small hand-rolled TOML subset parser so the toolchain
has no external TOML dependency. Public API:

- `Parse(src)` / `Read(path)` — simple error-returning form used by
  pkgmgr and CLI glue that wants a one-liner failure.
- `ParseDiagnostics(src, path)` / `Load(path)` / `LoadDir(dir)` —
  diagnostic-aware variants used by `osty build`. They emit
  `diag.Diagnostic` with stable `E2000–E2099` codes so manifest errors
  render through the same formatter as compiler errors.
- `Validate(m)` — semantic checks layered on top of parse: strict
  semver for `version`, identifier regex for `name`, membership in
  `KnownEditions` for `edition`, non-empty `[workspace].members`.
- `Marshal(m)` / `Write(path, m)` — round-trip serialization. Short
  dep form (`dep = "1.0"`) is preferred when the entry has only a
  version requirement; the long inline-table form is emitted otherwise.
- `FindRoot(dir)` — walk up to find the first `osty.toml` ancestor.

Schema coverage: `[package]` (name, version, edition, description,
authors, license, repository, homepage, keywords), `[dependencies]` /
`[dev-dependencies]` (registry / path / git with tag-branch-rev),
`[bin]` / `[lib]`, `[registries.<name>]`, `[workspace]` (members).
Virtual workspaces (no `[package]`) are supported; a manifest missing
both `[package]` and `[workspace]` is rejected.

### `internal/scaffold`
Project scaffolder — implements `osty new` and `osty init` (spec
§13.1). Writes the canonical starter files for three project kinds:
`KindBin` (main.osty + main_test.osty), `KindLib` (lib.osty +
lib_test.osty), and `KindWorkspace` (virtual workspace root + one
default binary member). Every template is asserted to pass the full
front-end in `scaffold_test.go`, so regressions in the compiler that
would break a newly-scaffolded project fail their tests before they
land. Diagnostics use `E2050–E2069`; callers render them through the
shared `diag.Formatter`.

### `internal/pkgmgr` & `internal/lockfile` & `internal/registry`
Dependency resolver, lockfile reader/writer, registry HTTP client,
and file-backed registry server. Driven by `osty build` / `osty add`
/ `osty update` / `osty publish` / `osty registry serve`. Out of
scope for this document's front-end focus — see the package-level
doc comments.

## Error handling philosophy

Two rules:

1. **Every user-facing error gets a stable code** (`Exxxx`) so docs
   and tests can reference it. Codes live in `internal/diag/codes.go`.
   The parser and resolver emit diagnostics via
   `diag.New(Severity, msg).Code(...).Primary(...).Hint(...).Build()`.

2. **Never panic on user input.** The fuzzers (`FuzzLex`, `FuzzParse`)
   run every release candidate against arbitrary UTF-8 to catch any
   regression into panic territory.

Recovery is **best-effort**: after an error the parser syncs to a
plausible boundary and keeps going so later declarations still parse.
Cascade suppression keeps the diagnostic list tight.

Post-front-end failures are handled separately from stable compiler
diagnostics: Go toolchain errors and runtime panics keep their original
output, then the CLI appends generated-file paths, nearest Osty source
markers, common package/import explanations, and a rerunnable Go command.

## Testing strategy

- **Unit** tests per package, including AST-shape assertions for
  happy-path constructs and code assertions for reject paths
  (`expectCode(t, src, diag.CodeXxx)`).
- **Integration** tests parse and resolve complete fixtures under
  `testdata/` — `full.osty` exercises the parser, `resolve_ok.osty`
  exercises the resolver.
- **Spec coverage** (`TestSpecCodeBlocks`) extracts every fenced
  `osty` block from the spec markdown and parses it. Some blocks are
  pseudo-output; the test enforces a minimum-pass floor rather than
  requiring 100%.
- **Fuzz** (`FuzzLex`, `FuzzParse`) both seeded with real Osty
  snippets and malformed inputs.
- **Golden snapshots** for diagnostic rendering.

## Open questions

The resolver deliberately does not:

- Walk member-access (`x.field`) — the type checker does this now.
- Check variant arity or field set — handled by the checker.

Cross-file resolution used to live here as a gap; it's now done via
`LoadPackage` / `ResolvePackage` / `Workspace` in the same package, and
cross-file partial-method name collisions are covered by package tests.

Spec-level open items are tracked in `SPEC_GAPS.md` (currently: zero
open gaps for v0.4). Structured-concurrency escape rules, generic
method turbofish semantics, callable arity after function/default
metadata erasure, closure parameter patterns, nested witness policy, and
stdlib protocol stubs were closed as v0.4 decisions.
