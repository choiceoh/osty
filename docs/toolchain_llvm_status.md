# Toolchain × LLVM compilability — status report

Snapshot date: 2026-04-19. This refresh revalidated the universal CLI / LLVM
smoke path, re-ran the whole/native merged toolchain probes, and cross-checked
the current code paths (`internal/check`, `internal/selfhost`,
`internal/llvmgen`, `internal/bootstrap/gen`) so this report distinguishes
historical samples from current-tree observations. Stage numbers shift
week-over-week; for the live MIR-direct coverage see
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
- the whole-toolchain merged LLVM probe still first-walls on the
  bootstrap-only `runtime.golegacy.astbridge` bridge
- the native-only merged LLVM probe (with bootstrap-only files skipped)
  now first-walls on `LLVM011 [list_mixed_ptr] type-system: list literal
  mixes String and non-String ptr-backed values` — a collection-literal
  typing gap (heterogeneous ptr-backed element types in the same
  `[...]` literal). The earlier LLVM011 `logical not on %PmCheckOutcome`
  wall is closed: it was a **parser precedence bug**, not a backend
  gap. The v0.5 grammar has `UnaryExpr ::= prefix UnaryExpr |
  PostfixExpr`, so `!x.y` must parse as `!(x.y)`; the self-hosted
  front-end was emitting `(!x).y`. Fixed at stable-AST lowering time
  in `internal/parser/lower.go` via `hoistUnaryOverPostfix`, which
  swaps prefix / postfix when a postfix node's receiver is a prefix
  UnaryExpr (-, !, ~). Unblocks every `!a.b`, `!a[i]`, `!a()`,
  `!a.m()` site in the toolchain. The whole LLVM015 [method_call_field]
  chain that preceded it is also still closed: `List.isEmpty` /
  `Map.isEmpty` / `Set.isEmpty` are inlined as `icmp eq i64 <len>, 0`
  (the stdlib default body form), with `osty_rt_map_len` landing
  in the C runtime alongside the dispatch; `List.pop()` discard sites
  go through the `osty_rt_list_pop_discard` helper; nested `IndexExpr`
  propagates List / Map / Set element shapes via
  `decorateStaticValueFromSourceType`.
  The LLVM012 statement-form category has been cleared for the
  toolchain's actual shape: `LLVM011 [fn_param_struct_type]` Char wall
  at `lspUtf16UnitsForChar` fell first (Char→i32 / Byte→i8 lowering),
  then `LLVM012 *ast.MatchExpr is not a call` (match-as-statement for
  tag enums), and most recently `LLVM012 field assignment base
  *ast.FieldExpr` (nested field chain like `cx.env.returnTy = ...`)
  now first-walls on `LLVM012 statement: field assignment base *ast.FieldExpr`
  (nested field assignment like `a.b.c = x`). The previous
  `LLVM011 [fn_param_struct_type]` Char wall at `lspUtf16UnitsForChar`
  was resolved by lowering `Char` to `i32` and `Byte` to `i8` together
  with `Char.toInt()` / `Byte.toInt()` / `Int.toChar()` width
  conversions and unsigned compare predicates. The subsequent
  `LLVM012 statement: *ast.MatchExpr is not a call` wall was resolved
  by adding a `MatchExpr` statement-position lowering for tag-enum
  scrutinees with bare-variant / wildcard arms
  now first-walls on `LLVM012` (statement-form restriction); the previous
  `LLVM011 [fn_param_struct_type]` Char wall at `lspUtf16UnitsForChar`
  has been resolved by lowering `Char` to `i32` and `Byte` to `i8`
  together with `Char.toInt()` / `Byte.toInt()` / `Int.toChar()` width
  conversions and unsigned compare predicates
- the current `osty check --airepair=false toolchain` surface is an
  aggregate native-checker summary of `3846 error(s)` with
  `20657 / 20749` assignment/return/call checks accepted
- code-path inspection shows the remaining gap is no longer best described as
  "no collections / no Result / no closures": list/map literals and some
  methods, `Result<T, E>` `?`, runtime-backed `std.strings` shims, and MIR
  capturing-closure env emission all exist in partial form

That means the universal LLVM entry wedge is closed. The remaining
toolchain/selfhosting work is now about front-end/toolchain coverage, not "any
CLI use of LLVM crashes before backend emission."

Current-tree observations from the code re-audit:

| Layer | Where | What blocks |
|---|---|---|
| CLI wiring | universal LLVM entry wedge | **resolved** — hello-world `osty gen --backend=llvm` exits 0 and writes `.ll` output |
| Bootstrap bridge | merged whole-toolchain probe | first wall is still `LLVM002 runtime-ffi` on `runtime.golegacy.astbridge`; this is a bootstrap artifact, not yet a native backend parity claim |
| Native backend surface | merged native-only probe | first wall is now `LLVM011 [list_mixed_ptr] list literal mixes String and non-String ptr-backed values` (heterogeneous ptr element types in a single `[...]` literal) after skipping 4 bootstrap-only files; the earlier `logical not on %PmCheckOutcome` wall was a parser precedence bug — `!x.y` was emitted as `(!x).y` instead of `!(x.y)` by the self-hosted front-end, now hoisted at stable-AST lowering time; List / Map / Set `isEmpty`, nested `IndexExpr`, and `list.pop()` discard sites all remain closed |
| Native backend surface | merged native-only probe | first wall is now `LLVM012 statement: field assignment base *ast.FieldExpr` (nested field assignment) after skipping 4 bootstrap-only files; the earlier `LLVM011 [fn_param_struct_type]` Char wall and the subsequent `LLVM012 *ast.MatchExpr is not a call` wall are both closed |
| Native backend surface | merged native-only probe | first wall is now `LLVM012` (statement form) after skipping 4 bootstrap-only files; the earlier `LLVM011 [fn_param_struct_type]` Char wall is closed |
| Checker boundary | `internal/check` / `internal/toolchain` | host still manages an external `osty-native-checker` artifact and falls back to the embedded selfhost checker when it cannot be prepared |
| Toolchain package health | `osty check --airepair=false toolchain` | current CLI surface is still an aggregate `E0700` summary (`3846 error(s)`) rather than a clean self-compile pass |
| Stdlib / string surface | `internal/llvmgen/stdlib_shim.go`, `expr.go` | a subset of `std.strings` is shimmed through runtime helpers. `Char` and `Byte` parameters/returns, literals, comparisons, and width conversions now lower; `String.chars` / `String.bytes` still block the pure native path because `List<Char>` / `List<Byte>` collection lowering is separate work |

The MIR-direct emitter itself (Stages 3.1–3.11) covers a growing subset of the
language shapes toolchain uses. The 2026-04-19 refresh narrows the story
further: backend entry is no longer the first blocker, but the current tree is
still not "fully self-hosted" because the bootstrap bridge, checker boundary,
and `Char`/runtime surface all remain live.

## How the probe was run

```
go build -o /tmp/osty ./cmd/osty
/tmp/osty gen   --backend=llvm   /tmp/hello.osty      >/tmp/hello.ll
go test ./internal/llvmgen -run 'TestGenerateModuleInterfaceVtableEmitted|TestGenerateSafepointKeepsImmutableManagedLocalsAndAggregateFields|TestGenerateManagedAggregateListsTraceNestedRoots|TestGenerateModulePtrBackedListToSetAndBoolPrint' -count=1
go test ./internal/llvmgen -run 'TestProbeWholeToolchainMerged|TestProbeNativeToolchainMerged' -count=1 -v
/tmp/osty check --airepair=false toolchain > /tmp/tc.log 2>&1
/tmp/osty build --backend=llvm   examples/calc        2>&1
```

`/tmp/hello.osty` was a 3-line `fn main() { println(42) }` baseline so the
backend entry path could be isolated from toolchain-specific issues.

Observed in the 2026-04-19 refresh:

- `/tmp/osty gen --backend=llvm /tmp/hello.osty` exited 0 and emitted a valid
  `.ll` module containing `define i32 @main()`
- the targeted `internal/llvmgen` regression set returned
  `ok github.com/osty/osty/internal/llvmgen`
- `TestProbeWholeToolchainMerged` reported
  `LLVM002 runtime-ffi: ... runtime.golegacy.astbridge ...`
- `TestProbeNativeToolchainMerged` skipped
  `ast_lower.osty, ci.osty, docgen.osty, manifest_validation.osty`. After the
  `Char`/`Byte` lowering landed the probe first-walls on `LLVM012`
  (statement form), not the previous `LLVM011 [fn_param_struct_type]` on
  `Char` at `lspUtf16UnitsForChar`
- `/tmp/osty check --airepair=false toolchain` exited with the aggregate
  summary
  `native checker reported type errors: 3846 error(s)` plus
  `native checker accepted 20657 of 20749 assignment/return/call checks`

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

## Layer 2 — checker error budget (`toolchain/`)

Current-tree command surface:

```text
$ /tmp/osty check --airepair=false toolchain
error[E0700]: native checker reported type errors: 3846 error(s)
= note: native checker accepted 20657 of 20749 assignment/return/call checks
```

That means the older per-file breakdown below is no longer current-tree ground
truth; it remains useful only as a historical clue for where the first wave of
front-end issues was observed before the code re-audit.

### Historical 2026-04-18 sample

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

### E0500 — `std.strings` not in scope (historical sample)

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

Code-path note for the current tree: the backend path is no longer best
described as "`std.strings` is entirely absent". `internal/llvmgen` now ships a
targeted `std.strings` runtime shim for `compare`, `concat`, `contains`,
`hasPrefix`, `hasSuffix`, `join`, `split`, `splitN`, `trim*`, and related
helpers. The remaining blocker is that the pure-Osty stdlib body still depends
on `Char` iteration / `List<Char>` lowering, so the shim is a bridge rather
than the final self-hosted endpoint.

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

### E0700 — aggregate checker summary

```
native checker reported type errors: 1700 error(s)
note: native checker accepted 26191 of 26850 assignment/return/call checks
```

On the current tree the visible summary is larger (`3846` errors with
`20657 / 20749` accepted), so the old `1700` figure should now be treated as a
historical snapshot only. The same general caveat still applies: large summary
counts are likely to hide a much smaller set of root-cause wedges, and the
whole/native probe results suggest `Char` + bootstrap-only host boundaries are
now higher-priority roots than the old parser alias bug.

## Layer 3 — MIR-direct backend coverage (for context)

Live coverage and remaining gaps are tracked in
[docs/mir_design.md](./mir_design.md) (Stage 3.x for landed shapes,
Stage 5 for deferred parity items). The code re-audit also found that the tree
already has more backend surface than the earlier prose implied:

- legacy AST llvmgen lowers list/map literals plus a subset of collection
  methods
- legacy AST llvmgen lowers `Result<T, E>` `?` when the enclosing function
  returns a compatible `Result<_, E>`
- MIR lowering already has capturing-closure env emission and tests

So the remaining budget is not "add collections/Result/closures from zero"; it
is closer to "finish the missing runtime/type surface, retire the shims, and
rewire the remaining Go-hosted boundaries."

## Recommended fix order (smallest → largest unlock)

1. **Treat the merged native probe as the current primary signal.**
   The first real wall is now `Char` parameter lowering, not the old CLI panic
   and not the already-fixed `def: Expr` alias issue.

2. **Shrink the aggregate checker summary on the current tree.**
   Re-profile the `3846`-error native-checker summary into a current histogram
   before making more claims from the 2026-04-18 sample.

3. **Finish the `Char` / `Byte` / string iteration surface.**
   The native probe's first wall and the current `std.strings` shim both point
   at the same missing runtime/type area (`String.chars`, `String.bytes`,
   `List<Char>`, and related ABI details).

4. **Retire the bootstrap-only bridge files from the critical path.**
   Whole-toolchain merged lowering still first-walls on
   `runtime.golegacy.astbridge`, so the self-hosting story stays incomplete
   until the CLI is rewired away from those files or they remain explicitly
   outside the native path.

5. **Then re-run `osty check toolchain` and per-file `osty gen --backend=llvm`
   probes.**
   Once the `Char`/bootstrap wedges move, the remaining tail should become a
   much narrower backend/runtime parity queue.
