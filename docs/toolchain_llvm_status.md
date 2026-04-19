# Toolchain × LLVM compilability — status report

Snapshot date: 2026-04-19. This refresh revalidated the universal CLI / LLVM
smoke path and the previously named `internal/llvmgen` regressions. The deeper
`toolchain/*.osty` error-budget counts below are still the 2026-04-18 sample
until they are re-run end-to-end. Stage numbers shift week-over-week; for the
live MIR-direct coverage see
[docs/mir_design.md](./mir_design.md) Stage 3.x + Stage 5 sections.

## TL;DR

The old "CLI panic blocks any `osty gen --backend=llvm` call" statement is no
longer true.

As of 2026-04-19:

- a fresh `fn main() { println(42) }` file goes through
  `osty gen --backend=llvm` successfully and emits LLVM IR
- the four targeted `internal/llvmgen` tests called out in the previous
  snapshot now pass again
- the parser-side `def: Expr` stable-alias issue remains fixed

That means the universal LLVM entry wedge is closed. The remaining
toolchain/selfhosting work is now about front-end/toolchain coverage, not "any
CLI use of LLVM crashes before backend emission."

The 2026-04-18 full-toolchain sample still showed the following blockers:

| Layer | Where | What blocks |
|---|---|---|
| CLI wiring | historical 2026-04-18 blocker | **resolved in the 2026-04-19 refresh** — hello-world `osty gen --backend=llvm` now exits 0 and writes `.ll` output |
| Front-end (lexer) | `internal/lexer` string-interpolation path | `"0_{label}"` / `"[a-z0-9_-]*"` inside string literals are flagged E0008 |
| Front-end (resolver) | `internal/resolve` / `internal/stdlib` | `std.strings` module not in scope — 39 × E0500 in `toolchain/ast_lower.osty` alone |
| Front-end (parser) | historical 2026-04-18 blocker | **resolved before this refresh** — `def: Expr` is no longer rewritten in `name: Type` positions |
| Type checker | `internal/check` | 1700 aggregated errors across the package (but 97.6% of assignment/return/call checks still accept — tail is small per-file) |

The MIR-direct emitter itself (Stages 3.1–3.11) covers most of the language
shapes toolchain uses. The 2026-04-19 refresh narrows the story further:
backend entry is no longer the first blocker; the remaining wedge is front-end
bookkeeping plus still-unported/selfhost-only toolchain usage.

## How the probe was run

```
go build -o /tmp/osty ./cmd/osty
/tmp/osty gen   --backend=llvm   /tmp/hello.osty      >/tmp/hello.ll
go test ./internal/llvmgen -run 'TestGenerateModuleInterfaceVtableEmitted|TestGenerateSafepointKeepsImmutableManagedLocalsAndAggregateFields|TestGenerateManagedAggregateListsTraceNestedRoots|TestGenerateModulePtrBackedListToSetAndBoolPrint' -count=1
/tmp/osty check --airepair=false toolchain > /tmp/tc.log 2>&1
/tmp/osty gen   --backend=llvm   toolchain/core.osty  2>&1   # historical 2026-04-18 sample
/tmp/osty build --backend=llvm   examples/calc        2>&1
```

`/tmp/hello.osty` was a 3-line `fn main() { println(42) }` baseline so the
backend entry path could be isolated from toolchain-specific issues.

Observed in the 2026-04-19 refresh:

- `/tmp/osty gen --backend=llvm /tmp/hello.osty` exited 0 and emitted a valid
  `.ll` module containing `define i32 @main()`
- the targeted `internal/llvmgen` regression set returned
  `ok github.com/osty/osty/internal/llvmgen`

## Layer 1 — universal CLI panic wedge (resolved)

The previous snapshot's highest-priority blocker was:

```
panic: runtime error: invalid memory address or nil pointer dereference
...
check.(*selfhostSpanIndex).addNode
  internal/check/host_boundary.go:578
```

That specific universal wedge is now closed.

Current revalidation result:

```text
$ /tmp/osty gen --backend=llvm /tmp/hello.osty
$ echo $?
0
```

The same refresh also re-ran the four `internal/llvmgen` tests that were named
as evidence for the panic and they now pass:

- `TestGenerateModuleInterfaceVtableEmitted`
- `TestGenerateSafepointKeepsImmutableManagedLocalsAndAggregateFields`
- `TestGenerateManagedAggregateListsTraceNestedRoots`
- `TestGenerateModulePtrBackedListToSetAndBoolPrint`

So the current remaining work should no longer be framed as "LLVM CLI path is
broken before backend reachability." The backend entry path is alive again; the
next re-profile should focus on actual `toolchain/*.osty` diagnostics and
selfhosting surface gaps.

## Layer 2 — front-end error budget in `toolchain/` (historical 2026-04-18 sample)

Aggregated from `osty check --airepair=false toolchain`:

| Code | Count | Meaning | Dominant site |
|---|---|---|---|
| E0500 | 39 | undefined name — `strings` module not in scope | `toolchain/ast_lower.osty` (all 39) |
| E0008 | 6 | numeric separator `_` must appear between two digits — false-positive inside string literals | `lsp.osty:415-423`, `ci.osty:546`, `manifest_validation.osty:283` |
| E0001 | 4 (historical) | expected IDENT — parser lost sync at `fn ParamNode(…, def: Expr, …)` before the stable-alias fix | `ast_lower.osty:91`, `:93` (× 2 each) |
| E0010 | 2 | cascading from E0001 | `ast_lower.osty:1051`, `:1054` |
| E0700 | 1 | native checker summary — 1700 errors aggregated | `airepair_flags.osty:1` (summary anchor) |

Error-bearing files: `ast_lower.osty` (46), `lsp.osty` (4),
`manifest_validation.osty` (1), `ci.osty` (1), `airepair_flags.osty` (1).
Every other `toolchain/*.osty` is free of parser / lexer / resolver errors
at the per-file level; the remaining pressure comes from the checker's
aggregate 1700-error summary, which is pulled forward into every `osty
check` / `osty gen` run.

### E0008 — string-interpolation lexer leak

```osty
return "0_{label}"                                 //  lsp.osty:415
return "1_{label}"                                 //  lsp.osty:418
"…expected [a-z][a-z0-9_-]*"                       //  ci.osty:546
"…[A-Za-z_][A-Za-z0-9_-]*)"                        //  manifest_validation.osty:283
```

All six sites share the same shape: a literal digit followed by `_`
followed by something non-digit, *inside* a string literal (with or without
interpolation). The numeric-literal validator is being invoked on string
content.

Minimal reproducer candidate: add a positive spec-corpus fixture containing
`let s = "0_{x}"; let t = "0_-foo"` and expect the checker to emit zero
diagnostics. The fix lives in the lexer's string-literal scanner; the
symptom is that the number-separator rule is reached from inside the string
state instead of staying in the number state.

### E0500 — `std.strings` not in scope

29 call sites in `toolchain/ast_lower.osty` (39 diagnostics — some lines
hold two calls) reference `strings.HasPrefix`, `strings.TrimPrefix`,
`strings.TrimSuffix`, `strings.Split`, `strings.Count` (and similar). The checker's hint (`did you mean stringAt?`) shows that
something named `stringAt` is reachable, but no module bound to `strings`
is registered in the prelude / stdlib for this package.

Two equally-valid fixes:

1. Port `std.strings` to Osty and register it in the prelude — matches how
   Stage 3.9's iter/cmp/csv work landed. This is the expected long-term
   resolution.
2. Rewrite `ast_lower.osty`'s call sites to use the Osty-native string
   primitives (`stringAt`, char-index loops, etc). Faster, but doesn't
   help any of the other files that will eventually want `std.strings`.

### E0001 — parser trips on `def: Expr` parameter — **fixed**

Root cause was the stable-alias pre-pass in `internal/parser/normalize.go`:
it rewrote every `IDENT` token matching `def` / `func` / `function` /
`import` / `while` to its canonical keyword form, regardless of position.
`fn FieldNode(…, def: Expr, …)` and `fn ParamNode(…, def: Expr)` therefore
had `def` silently rewritten to `fn`, producing four E0001 diagnostics at
`ast_lower.osty:91` and `:93`.

Fix: in `normalizeStableAliases`, skip the rewrite when the identifier is
immediately followed by `:` (parameter / struct field / keyword-argument
slot). Regression test in
`internal/parser/parser_features_test.go::TestParseStableAliasesPreservedAsIdentifiers`.

### E0700 — 1700 checker errors, 97.6% accept rate

```
native checker reported type errors: 1700 error(s)
note: native checker accepted 26191 of 26850 assignment/return/call checks
```

Most likely a cascade from the E0500 / E0001 failures above: once a type
can't be resolved (e.g. a `strings.Split` call site), every transitively
dependent `Arg`/`Return` site also fails its type constraint. Clearing
Layer 1 + 2 should knock this number down by an order of magnitude before
any individual-site chasing is worthwhile.

## Layer 3 — MIR-direct backend coverage (for context)

Live coverage and remaining gaps are tracked in
[docs/mir_design.md](./mir_design.md) (Stage 3.x for landed shapes,
Stage 5 for deferred parity items). The outstanding items at the time
of writing are not tripped during `osty check` or `osty gen` IR
emission, so the work budget to reach "`toolchain/*.osty` links into a
runnable binary" is dominated by Layer 1 + Layer 2, not Layer 3.

## Recommended fix order (smallest → largest unlock)

1. **Re-run the full `toolchain` sample on the current tree** and replace the
   historical 2026-04-18 counts. The universal CLI panic wedge is already
   closed, so fresh numbers matter more than old ones now.

2. **Fix the string-interpolation lexer bug** (E0008). Six sites in the last
   full sample,
   pattern is well-defined, fixture is small. Unblocks `lsp.osty`,
   `ci.osty`, `manifest_validation.osty`.

3. **Decide `std.strings` policy.** Either port it (29 call sites in
   one file say the demand is real) or rewrite `ast_lower.osty` against
   existing primitives. Unblocks `ast_lower.osty`.

4. ~~**Investigate parser sensitivity to `def`** inside FFI `use` blocks.~~
   Done — stable-alias pre-pass now skips rewrites when the identifier
   sits in `name : type` position.

5. **Re-run `osty check toolchain`** — expect the 1700 native-checker
   summary to collapse with most cascades gone. Remaining tail becomes a
   focused second pass.

6. **Try `osty gen --backend=llvm toolchain/<file>.osty`** per-file.
   The MIR-direct emitter covers most of the language shapes toolchain
   uses (see [mir_design.md](./mir_design.md)); anything that trips
   `mir-mvp` unsupported is a new, narrower wedge for the MIR roadmap.
