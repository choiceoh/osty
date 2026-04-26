# Architecture

High-level map of the Osty front-end. For spec decisions see
`OSTY_GRAMMAR_v0.5.md`; this document covers **implementation** layout.

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
                                                                                ┌──────────┐   LLVM IR / object / binary
                                                                                │ ir / mir │   warnings (L0xxx)
                                                                                │ llvmgen  │   Osty-hosted diag policy
                                                                                │   lint   │
                                                                                └──────────┘
```

Each stage produces diagnostics as it goes; they accumulate in a single
`[]*diag.Diagnostic` that the CLI renders at the end.

### Self-hosted toolchain (Osty-authored compiler core)

The repo ships ~110 `.osty` files under `toolchain/` that reimplement
the compiler pipeline in Osty itself. These are merged into a single
bootstrap core by `internal/selfhost/bundle/bundle.go`, compiled to
Go via the frozen seed in `internal/selfhost/generated.go`, and wired
through adapter layers (`check_adapter.go`, `parse.go`, `resolve_adapter.go`,
`format_adapter.go`) so the Go CLI can consume native results. Key modules:

| Module | Lines | Purpose |
|---|---|---|
| `frontend.osty` | ~5,700 | Token types (`FrontTokenKind`), lexer stream, source normalization |
| `lexer.osty` | ~640 | `OstyLexer` — self-hosted lexer with rich tokens, trivia, error formatting |
| `lossless_lex.osty` | ~1,200 | Lossless layer — attaches whitespace/comments/shebang/BOM to tokens for byte-for-byte reconstruction |
| `parser.osty` | ~2,620 | `AstNodeKind` enum (~40+ variants), arena-based `AstFile` production |
| `resolve.osty` | ~3,700 | Self-hosted name resolver — symbol table, scope resolution, `SelfSymbol` records |
| `elab.osty` | ~5,400 | Bidirectional elaborator — type inference engine (`elabInfer` / `elabCheck`) |
| `solve.osty` | ~390 | Local constraint solver for generic type inference |
| `check.osty` | ~1,530 | Type checker entry point — orchestrates lexer → parser → elaboration → serialization |
| `check_env.osty` | ~3,010 | Elaboration environment — lexical binding stack, generic bounds, `Ty` arena indices |
| `check_diag.osty` | ~700 | Structured diagnostics with stable `Exxxx` codes (E0700–E0799) |
| `check_gates.osty` | ~1,110 | Post-elaboration policy gates — LANG_SPEC privilege/shape rules |
| `ty.osty` | ~915 | Type arena — `TyArena` with interned primitives, structural key interning |
| `core.osty` | ~1,800 | Core IR — typed intermediate representation, arena-based kind discriminators |
| `ir.osty` | ~1,400 | Legacy untyped IR — `IrNodeKind` enum, AST-based bridge |
| `hir.osty` | ~3,500 | High-level IR — typed monomorphic HIR, flat structs with `kind` tags |
| `hir_lower.osty` | ~4,300 | AST → HIR lowerer — hybrid approach (AST decls + Core typed exprs) |
| `hir_optimize.osty` | ~790 | Conservative cleanup — const-fold, dead-code elim, branch opt |
| `hir_validate.osty` | ~780 | Structural validation — catches internal lowering/rewriting bugs |
| `hir_print.osty` | ~980 | Debug rendering — stable text output per shape |
| `hir_walk.osty` | ~430 | Preorder traversal — collector recording nodes by category |
| `hir_reach.osty` | ~680 | Reachability scans — qualified reference tracking |
| `mir.osty` | ~1,950 | Middle-end IR — data model + printer + CFG helpers |
| `mir_lower.osty` | ~5,500 | HIR → MIR lowerer — Stage 4a–4e incremental port |
| `mir_optimize.osty` | — | MIR optimization passes |
| `mir_validator.osty` | — | MIR structural validation |
| `pmcompile.osty` | ~530 | Pattern-match decision tree compiler |
| `monomorph.osty` | ~360 | Generic monomorphization — Itanium ABI mangling |
| `monomorph_pass.osty` | ~2,240 | Stage A — generic type environment, deep clone + substitution |
| `monomorph_rewrite.osty` | ~470 | Stage B — HIR node type-field rewriting |
| `hir_clone.osty` | ~640 | Deep-clone helpers for HIR |
| `diagnostic.osty` | ~250 | Diagnostic types — severity, family classification |
| `diag_manifest.osty` | ~220 | AUTO-GENERATED — code → family mapping |
| `diag_policy.osty` | ~65 | CLI diagnostic classification policy |
| `lint.osty` | ~3,970 | Self-hosted lint rules — unused bindings, naming, complexity |
| `lsp.osty` | ~1,220 | LSP semantic token shapes, fix-all overlap resolution |
| `formatter_ast.osty` | ~745 | AST-based code formatter |
| `docgen.osty` | ~1,680 | Documentation generator — extracts public API declarations |
| `inspect.osty` | ~690 | Per-node type-inference observations |
| `runner.osty` | — | `osty run` pure policy |
| `ci.osty` | ~1,090 | CI commands — host handles, build/test result shapes |
| `airepair_flags.osty` | ~90 | `osty airepair` flag-parsing policy |
| `profile_flags.osty` | ~65 | Profile/feature flag resolution |
| `pkgmgr.osty` | ~1,240 | Package manager core — sources, manifest, lockfile |
| `pkgmgr_sat.osty` | — | PubGrub-style dependency solver |
| `registry.osty` | ~35 | Registry index — version records, yanked filtering |
| `package_entry.osty` | ~55 | Package file classification |
| `manifest_features.osty` | ~50 | Feature spec parsing and validation |
| `manifest_validation.osty` | ~820 | `[package]`/`[workspace]` table parsing |
| `toml.osty` | ~880 | TOML lexer + parser for `osty.toml`/`osty.lock` |
| `semver.osty` | ~235 | SemVer type — comparison, caret/tilde requirements |
| `semver_parse.osty` | ~240 | SemVer string parsing |
| `semver_req.osty` | ~145 | SemVer requirement operators |
| `pipeline.osty` | ~110 | Pipeline report — aggregate counters per stage |

Builder/derive policy: `builder_policy.osty` (~60 lines), `check_bridge.osty` (~9 lines), `check_env.osty`, `inspect_hint.osty` (~200 lines).

Test files: ~59 `*_test.osty` covering all production modules.

### Default path: self-host arena (since Phase 1c.1, updated 2026-04-27)

`osty check` and `osty typecheck` now dispatch to the **self-host arena
pipeline** by default: `parser.ParseRun` produces a selfhost `FrontendRun`
(no astbridge lowering), then `selfhost.CheckStructuredFromRun` runs the
Osty-native resolver + checker in one pass, emitting structured records
that `selfhost.CheckDiagnosticsAsDiag` lifts into the CLI's `diag.Diagnostic`
shape. The old Go-hosted `--legacy` check/typecheck escape hatch and
`check.File` entrypoint have been removed; `--native` remains accepted only as
a backwards-compatible no-op. `osty resolve` is also self-host-first. Package
and workspace loaders for `pipeline`, `build`, `run`, `doc`, `lsp`, and
`cihost` use arena-first `FrontendRun` parsing. Some compatibility wrappers
still call `EnsureFiles` / `MaterializeCanonicalSources` to hand downstream
passes the old `*ast.File` shape, while bump-free native paths use the
structured adapters directly. The remaining astbridge cleanup is therefore
lint/formatter/LSP/backend consumer work, not the
check/typecheck happy path.

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
edges, driven by `NewWorkspace` + `ResolveAll`). These are scope
boundaries, not temporal passes — the resolver runs **two passes** over
whichever scope the caller chose, and `Workspace.ResolveAll` fans the
same two passes across every package so cross-package lookups always
see populated scopes:

1. **`declarePass`** (`resolve.go:133`) — walks every file, registers
   `use` aliases into the file scope, and inserts every top-level
   `fn`/`struct`/`enum`/`interface`/`type`/`pub let` symbol into the
   package scope. Forward references and cross-package lookups both
   work after this pass.
2. **`bodyPass`** (`resolve.go:149`) — walks declaration bodies and
   top-level statements, opening child scopes for generics,
   parameters, let bindings, closure params, and match arms. Circular
   imports are detected before this pass runs and reported as E0506.

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

The main front-end static guarantee hooks (v0.4 baseline + v0.5
additions) are wired here:

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
- match exhaustiveness runs a Maranget usefulness pass
  (`pmCheckMatch` / `pmUseful` / `pmExhaustivenessWitness` in
  `toolchain/elab.osty`) that emits `E0731` with up to three concrete
  missing witnesses (including nested tuple / struct / enum payload
  shapes, depth-capped at 6 for recursive ADTs) and flags unreachable
  arms as `E0740`; G14 generic-value references are rejected as
  `E0742` with focus tests, and G15 function-value calls are pinned
  positional + exact-arity (`E0701`) so defaults / keywords never
  survive erasure,
- auto-derived `default()`, `builder()`, `toBuilder()`, setter chains,
  and `build()` obligations are checked in `builder.go`,
- method references (`obj.method` as a value) lower to receiver-free
  function types,
- keyword/default-aware local and cross-package function calls use the
  declared parameter metadata when available, while erased function
  values are positional-only with exact arity,
- closure parameter destructuring binds irrefutable tuple/struct
  patterns and rejects refutable patterns with a stable diagnostic.

The v0.4 and v0.5 language-decision sweeps are closed in
`SPEC_GAPS.md` (zero open G-numbers); remaining work is implementation
backlog — the host-side legacy checker boundary
(`internal/check/host_boundary.go`) still acts as a fallback under the
native checker, and retiring it is the main architectural cleanup
tracked outside spec gaps.

#### Type inference algorithm

Bidirectional typing with local unification and monomorphization on
generic instantiation. The full specification, including the rule
table and a line-level mapping to the self-hosted implementation,
lives at `LANG_SPEC_v0.5/02a-type-inference.md`. The reference
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
source files under `modules/` (37 modules: bytes, cmp, char,
collections, compress, crypto, csv, debug, encoding, env, error, fmt,
fs, hint, http, io, iter, json, log, math, net, option, os, process,
random, ref, regex, result, runtime/raw, sync, testing, testing_gen,
thread, time, strings, url, uuid) with primitive method stubs under
`primitives/` (bool, bytes, char, float, int, string). Tier 1 is
loadable by the front-end; Tier 2 stubs are checked `.osty` bodies
that parse / resolve / check cleanly. The Map helper family (`update`,
`getOr`, `mergeWith`, `groupBy`, `mapValues`, `retainIf`) ships as
bodied Osty per the §B.9.1 contract — runtime execution of those bodied
helpers currently blocks on the `LLVM015` Map-method lowering gap
documented in `docs/toolchain_llvm_status.md`.

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
compilation. Rules implemented today (28 codes, grouped by category):

| Code | Rule |
|---|---|
| L0001 | unused `let` binding |
| L0002 | unused fn / closure parameter |
| L0003 | unused `use` alias (package-aware) |
| L0004 | unused `mut` annotation |
| L0005 | unused struct field |
| L0006 | unused method |
| L0007 | ignored `Result` / `Option` return |
| L0008 | dead store (write never read) |
| L0010 | inner binding shadows outer |
| L0020 | statement after terminating return/break/continue |
| L0021 | redundant `else` after early return |
| L0022 | constant condition |
| L0023 | empty `if` / `else` branch |
| L0024 | needless `return` on last expression |
| L0025 | identical branches |
| L0026 | empty loop body |
| L0030 | type name not UpperCamelCase |
| L0031 | fn / let / param name not lowerCamelCase |
| L0032 | enum variant name not UpperCamelCase |
| L0040 | redundant boolean expression |
| L0041 | self-comparison (`x == x`) |
| L0042 | self-assignment (`x = x`) |
| L0043 | double negation (`!!x`) |
| L0044 | comparison against boolean literal |
| L0045 | negated boolean literal |
| L0050 | function takes too many parameters |
| L0052 | function body too long |
| L0053 | nesting too deep |
| L0070 | missing doc comment on `pub` declaration |

Policy (allow / deny / exclude per code, rule alias, category alias,
or wildcard) is sourced from `[lint]` in `osty.toml`; `deny` always
wins over `allow`. Machine-applicable fixes are emitted for the unused
/ simplify families and consumed by `osty lint --fix` /
`--fix-dry-run`. `File`, `Package`, and workspace-driven usage are all
supported so cross-file references to a `use` alias don't look unused
locally. Wired as `osty lint` with `--strict` (CI mode: exit 1 on any
warning). Rule-level policy logic lives in `toolchain/diag_policy.osty`.

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
`toolchain/lsp.osty` and exposed through the checked-in host boundary.
File-mode analysis flows through a Salsa-style incremental query engine
(`internal/query/osty`, exposed as `ostyquery.Engine`): the server's
default path for a single dirty buffer is `analyzeSingleFileViaEngine`,
so repeated edits to the same file benefit from query-level reuse of
parse / resolve / check results. Package- and workspace-mode analysis
still re-runs the eager per-file path while that migration catches up,
so cross-file refactors read fresh state on each request. Wired as
`osty lsp`.

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

### `internal/cst`
Concrete syntax tree machinery using the Red/Green tree pattern.
`GreenKind` mirrors `ast.*` plus token/trivia/error kinds.
`GreenArena` owns all nodes with structural sharing via content-based
interning. `Red` is a lazy view adding absolute source positions and
parent pointers. Byte-coverage invariant: every source byte is covered
by exactly one token or trivia — foundation for round-trip verification.
Wired through `selfhost.ParseCST()` and `lossless_lex.osty` in the
toolchain.

### `internal/query`
Salsa-style incremental query engine with revision tracking, dependency
graphs, cycle detection, and early cutoff. `Database` holds global
revision counter + slot storage. `Frame` tracks the execution stack.
Cached records per `(QueryID, key)` implement early cutoff: if a
re-run produces the same hash, downstream dependents skip. The `osty`
subpackage binds it to the compiler pipeline: `Engine` bundles
`Database` + `Inputs` (`SourceText`, `PackageFiles`, `WorkspaceMembers`)
+ registered queries (`Parse`, `ResolvePackage`, `CheckFile`,
`FileDiagnostics`). The LSP server uses `analyzeSingleFileViaEngine`
as its default path for a single dirty buffer.

### `internal/airepair`
Chains conservative lexical, structural, semantic, and diagnostic-driven
rewrite phases to automatically fix common code patterns from other
languages (Go, Python, JavaScript) into idiomatic Osty. Measures
diagnostic improvement before/after each accepted rewrite. Phases:
lexical (foreign lexemes) → structural (token normalization) → semantic
(AST-level type fixes) → diagnostic-driven (front-end analysis). Also
handles foreign loop patterns (Python `for i in range(n)`, `enumerate`)
and tuple loops. Produces `ReportSummary` with error delta counts and
`ReportMetadata` with phase/source_habit/confidence for tooling.
Corpus tests live under `testdata/corpus/` with `.input.osty` →
`.expected.osty` pairs.

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
- **User-visible snapshot tests** (`testing.snapshot(name, output)`
  per LANG_SPEC §11.5) intercepted by
  [`internal/llvmgen/stmt.go`](internal/llvmgen/stmt.go) and lowered
  to a runtime helper in
  [`internal/backend/runtime/osty_runtime.c`](internal/backend/runtime/osty_runtime.c).
  Goldens live at `<source_dir>/__snapshots__/<name>.snap`;
  `osty test --update-snapshots` accepts the current output. Golden
  mismatches surface as a trim-prefix / trim-suffix line diff that
  reuses the same runtime helper as `assertEq` on `String` values.

## Open questions

The resolver deliberately does not:

- Walk member-access (`x.field`) — the type checker does this now.
- Check variant arity or field set — handled by the checker.

Cross-file resolution used to live here as a gap; it's now done via
`LoadPackage` / `ResolvePackage` / `Workspace` in the same package, and
cross-file partial-method name collisions are covered by package tests.

Spec-level open items are tracked in `SPEC_GAPS.md` (currently: zero
open gaps for v0.5). Structured-concurrency escape rules, generic
method turbofish semantics, callable arity after function/default
metadata erasure, closure parameter patterns, and nested witness
policy were closed as v0.4 decisions; v0.5 layered on 16 additive
surface extensions (G20–G35) plus implementation closes for G14
generic callable reference (`E0742`), G15 function-value arity lock,
struct spread correctness, and the `std.strings` chapter. G17
exhaustiveness is now implemented as a full Maranget usefulness pass
(`pmCheckMatch` / `pmUseful` / `pmExhaustivenessWitness` in
`toolchain/elab.osty`) with up to three witnesses per match and a
depth cap of six for recursive ADTs.
