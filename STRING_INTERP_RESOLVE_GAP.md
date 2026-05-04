# String Interpolation Resolver Gap — Multi-Day Plan

**Status (2026-05-05):** User-facing path closed via #1361 (MIR-layer
recovery). Canonical-side parser + resolver + applyTwice regression
shipped via #1360 (Day 1 lexer ranges), #1364 (Day 2 parser
sub-parse), #1376 (Day 3 resolver verification). Architectural fix
(Go-side `astbridge` consumes arena children, drops
`interp_adapter.go` re-parse) blocked on
`internal/selfhost/generated.go` regeneration — the seed has the
pre-#1364 parser; the canonical Osty parser is ahead. Until LLVM
self-host LLVMgen catches up, the runtime MIR fallback from #1361
handles the visible cases.

**Original status (2026-05-04):** Open. Identified during the fmt_e2e
wall-progression sweep after [#1326](https://github.com/choiceoh/osty/pull/1326),
[#1329](https://github.com/choiceoh/osty/pull/1329),
[#1339](https://github.com/choiceoh/osty/pull/1339),
[#1341](https://github.com/choiceoh/osty/pull/1341),
[#1342](https://github.com/choiceoh/osty/pull/1342),
[#1344](https://github.com/choiceoh/osty/pull/1344) closed six successive
walls.

## Symptom

```osty
fn applyTwice(mapper: fn(Int) -> String) -> String {
    "{mapper(1)}-{mapper(2)}"
}
```

Compiles to `call to unresolved symbol mapper` or
`unsupported local type <error> ... written by call mapper`.

The same call **outside** an interpolation (`let r = mapper(1)`) compiles
fine — the wall fires only when `mapper(...)` lives inside `"{...}"`.

The fmt_e2e harness's `testJoinWith` is the canonical user-facing
trigger: `fmt.joinWith(nums, "-", |n| n.toString())` lowers
`std.fmt.joinWith`'s body which contains `"{result}{sep}{mapper(items[i])}"`
— `mapper` is a fn-typed parameter and the inner call falls into this
gap.

## Root cause (verified 2026-05-04 via debug println)

`Ident{Name: "mapper"}` inside an interpolation reaches
`ir.lowerIdent` with `Kind=IdentUnknown(0)` and `T=ErrType`. The
resolver never recorded a `RefsByID` entry for that NodeID.

The gap spans four layers:

### 1. Self-host parser (`toolchain/parser.osty:680-685`)

```osty
if tok.kind == FrontString || tok.kind == FrontRawString {
    opAdvance(p)
    let mut n = emptyAstNode(AstNStringLit)
    n.text = tok.text
    n.start = start
    n.end = p.pos
    return opAddNode(p, n)
}
```

`AstNStringLit` is created with `text` only. The interpolation parts
(`{expr}`) are NOT stored as AST children. The string is opaque to
every downstream pass that walks the arena AST.

### 2. Self-host resolver (`toolchain/resolve.osty:1242+`)

`srAstResolveExpr` has no case for `AstNStringLit`. The generic
fall-through at lines 1291-1297 walks `node.left`, `node.right`,
`node.children`, `node.children2` — all empty for a StringLit — so the
resolver visits zero nodes inside an interpolation. No
`RefsByID[ident.ID]` entries are created for interpolation idents.

### 3. astbridge (`internal/selfhost/astbridge/astbridge.go:41-60`)

When the public `*ast.File` is needed, `stringPartsToAST` lazily calls
`ParseInterpolatedExpr(p.Expr)` on each interpolation token slice
(`internal/selfhost/interp_adapter.go`):

```go
func parseInterpolatedExpr(toks []token.Token) ast.Expr {
    src := interpolatedTokensSource(toks)
    file, _ := Parse([]byte("let __interp = " + src))
    if ls, ok := file.Stmts[0].(*ast.LetStmt); ok {
        return ls.Value
    }
    ...
}
```

The fresh re-parse produces an `ast.Expr` with brand-new NodeIDs that
never existed during the original resolve pass.

### 4. ir.Lower (`internal/ir/lower.go:1595+`)

```go
if l.res != nil {
    if s := l.res.RefsByID[id.ID]; s != nil {
        sym = s
        out.Kind = identKind(s)
    }
}
```

`l.res.RefsByID[id.ID]` returns nil for the interpolation idents.
`out.Kind` stays `IdentUnknown(0)`, `out.T` stays `ErrType`. MIR
backend's `resolveCall` (`internal/mir/lower.go:4848-4875`) sees the
non-`IdentParam`/`IdentLocal` Ident and routes through
`FnRef{Symbol: name}` — direct symbol resolution against the user's
free-fn table — which fails because no `mapper` free fn exists.

## Why earlier band-aids don't help

- The existing `recoverOperandType` chain (`internal/mir/lower.go:2662+`)
  has a fallback for fn-typed callee Idents that checks
  `Callee.Type()`. But `Callee.Type()` is also `ErrType` because step
  4 above never gave the Ident a real type.
- `recoveredIdentStorageType` walks `bs.fn.Local(name)` — also empty
  because the param's local entry was never indexed under
  `mapper` from the interp-side ident.

## Three solution paths

### Option A — Fix the parser (canonical)

Make the self-host parser break interpolations into AST children at
parse time so the resolver naturally walks them.

**Changes:**

1. `toolchain/parser.osty` — when parsing `FrontString`/`FrontRawString`,
   look up the token's stringParts via the lexer-facts side-table.
   For each `kindCode==1` (interp) part, recursively parse the interp
   tokens as an expression and attach the resulting node ID as a
   child of `AstNStringLit`.
2. `toolchain/resolve.osty` — add an explicit case for
   `AstNStringLit` that walks its children with the current
   scope. (May not be strictly required if the generic walker
   already handles populated children — needs verification.)
3. `internal/selfhost/astbridge` — when lowering arena → public
   AST, pull the interp parts from the stored arena children
   instead of re-parsing via `ParseInterpolatedExpr`. Preserve
   NodeIDs so the resolver's RefsByID matches.
4. Retire `parseInterpolatedExpr` and `interp_adapter.go` once the
   bridge consumes from the arena.

**Pros:**
- Architecturally correct — interpolation Idents go through the same
  resolve pipeline as every other expression.
- One source of truth for the AST. No split between arena and
  bridge-time re-parses.
- NodeIDs preserved end-to-end.

**Cons:**
- Largest blast radius. Touches lexer→parser→resolver→bridge.
- Subtle: parser needs access to interp tokens. Today they live in
  Go-side `FrontLexStream.interpolationTokens` (not osty-visible).
  Either expose them to osty, or have the parser re-lex interp
  source slices using offsets.

**Multi-day breakdown:**

- **Day 1** — Lexer side: add interp source byte ranges to
  `OstyLexStringPart` (a `srcStart, srcEnd` pair on top of the
  existing `exprTokenStart, exprTokenCount`) so the parser can
  re-lex interp slices without depending on Go-side
  `interpolationTokens`. No behavior change yet; parser still
  ignores parts. Verifiable by a unit test that asserts the new
  fields are populated.

- **Day 2** — Parser side: parser receives `OstyLexFacts` (or the
  stringParts subset) alongside tokens. When parsing
  `AstNStringLit`, walk its parts; for each interp part, sub-lex
  the source slice (offset-biased so spans point into the original
  source) and recursively call expression-parsing functions on the
  resulting tokens. Append child node IDs to `node.children`.
  Verifiable by an arena-snapshot test that asserts a StringLit's
  children include the expected expression shapes.

- **Day 3** — Resolver side: confirm the generic
  `srAstResolveExpr` walker recurses into the new children
  correctly. If it does, no resolver change is needed. If not,
  add an explicit `AstNStringLit` case. Verifiable by a
  resolver-trace test on a `"{ident}"` snippet that checks
  `RefsByID` has an entry for the interp ident.

- **Day 4** — Bridge side: `stringPartsToAST` consumes the arena
  children directly. Drop the `ParseInterpolatedExpr` re-parse
  path and `interp_adapter.go`. Wire arena NodeIDs through to
  `*ast.Ident.ID` so `ir.Lower` resolves correctly. Verifiable by
  the original `applyTwice` repro now lowering cleanly +
  fmt_e2e's `testJoinWith` passing under
  `OSTY_STDLIB_BODY_LOWER=1`.

### Option B — Post-bridge re-resolve (workaround)

After the bridge expands `*ast.StringLit.Parts`, run a mini-resolver
over each interp `Expr` against the parent function's scope.
Populate `RefsByID` with the new entries.

**Pros:** Smallest blast radius. Self-contained in selfhost / astbridge layer.
**Cons:** Duplicates resolution logic. Needs scope context propagated to
the bridge call site (today the bridge is context-less). Two sources
of truth for resolution.

### Option C — IR-layer name-based fallback

When `lowerIdent`'s `RefsByID` lookup fails, do a name-based
lookup against the surrounding function's params + lets.

**Pros:** Smallest code change.
**Cons:** Inverts the resolve→lower contract. Doesn't help any
non-IR consumer (lint, format, LSP). Easy to regress.

## Recommended path

**Option A.** The fmt_e2e wall-progression only exposed the symptom;
the underlying gap (StringLit opaque to resolver) is going to bite
every other tool that walks the AST eventually — lint can't see
interpolation-internal idents either, format can't reformat
interpolation expressions, LSP can't go-to-def for interpolated
symbols. Only Option A fixes all of them at once.

**Day 1 starts with the lexer change** because it's the foundation —
without exposing interp source ranges to the osty side, the parser
can't proceed on Day 2.

## Out of scope for this doc

- Specific FrontToken/AstArena schema choices for storing children.
  Decide in the Day 2 PR.
- Migration plan for `interpolationTokens` and `interpolationTokenTexts`
  fields on `FrontLexStream` once the bridge no longer needs them.
  Decide in the Day 4 cleanup PR.
- LSP / lint behavior changes once interp idents are walkable. Track
  separately as those tools start consuming the new structure.
