# Toolchain × LLVM compilability — status report

## 2026-04-22 policy update — AST merged-probe gate retired

`TestNativeToolchainMergedIsClean` (formerly at
`internal/llvmgen/native_toolchain_clean_test.go`) has been **deleted**.
It measured the legacy HIR→AST bridge surface, which the MIR-first real
backend is actively replacing. Gating on it was locking in a surface we
are deprecating, and any file migrating off `use go "..."` toward
`use std.*` routinely tripped it for reasons unrelated to the
self-host critical path (static type propagation gaps in the AST
emitter's local `staticExprInfo`, not backend capability).

The **real** self-host measurement is
`TestProbeNativeToolchainMergedMIR` — parse → resolve → check →
`ir.Lower` → `Monomorphize` → `mir.Lower` → `GenerateFromMIR`.
Promoting that probe to authoritative + closing its first wall
(`LLVM000 unsupported-source: unsupported local type <error> in
checkHashKey`) is the next A-path deliverable. Historical language
below still says "AST-merged CLEAN" because that was accurate at
2026-04-21 snapshot time; treat it as archive, not current gate state.

---

Snapshot date: 2026-04-21 (late). Two major milestones since the morning
refresh: (1) the native-only merged LLVM probe is now **CLEAN** — `#486`
closed the last walls by landing `String.bytes()` / `String.chars()`
intrinsic lowering and `List<T>.clear()`, and an authoritative gate
(`TestNativeToolchainMergedIsClean` in `internal/llvmgen/`) fails fast if
a regression re-introduces a wall; (2) `#503` ported the MIR core to
Osty (`toolchain/mir.osty`), and (3) `#496` finished the runtime
scheduler — `osty_rt_select_send` joined the public surface, so the
public LLVM runtime has zero `osty_sched_unimplemented` call sites left
(concurrency spec §8 fully covered). This refresh revalidated the
universal CLI / LLVM smoke path, re-ran the whole/native merged
toolchain probes, and cross-checked the current code paths
(`internal/check`, `internal/selfhost`, `internal/llvmgen`) so this report
distinguishes historical samples from current-tree observations. Stage numbers shift
week-over-week; for the live MIR-direct coverage see
[docs/mir_design.md](./mir_design.md) Stage 3.x + Stage 5 sections.

## TL;DR

The old "CLI panic blocks any `osty gen --backend=llvm` call" statement is no
longer true.

As of 2026-04-21:

- a fresh `fn main() { println(42) }` file goes through
  `osty gen --backend=llvm` successfully and emits LLVM IR
- the four targeted `internal/llvmgen` tests called out in the previous
  snapshot now pass again
- the parser-side `def: Expr` stable-alias issue remains fixed
- the whole-toolchain merged LLVM probe still first-walls on the
  bootstrap-only `runtime.golegacy.astbridge` bridge
- the native-only merged LLVM probe (with bootstrap-only files skipped)
  now lowers **CLEAN** (`#486`, guarded by
  `TestNativeToolchainMergedIsClean` in `internal/llvmgen/`) — the
  last wall (`LLVM015 [method_call_field]` on `buf.clear()` in
  `toolchain/ty.osty`'s generic-arg splitter, where rebinding
  `buf = []` would've lost bootstrap-gen's element inference on
  Windows regen) was closed by adding `osty_rt_list_clear` to the C
  runtime and routing `List<T>.clear()` through `emitListMethodCallStmt`.
  The preceding `String.bytes`/`String.chars` intrinsic gap is also
  closed: `internal/llvmgen/expr.go` dispatches both methods to
  `osty_rt_strings_Chars` / `osty_rt_strings_Bytes` with `listElemTyp`
  tagged `i32` / `i8` so downstream iteration takes the `_bytes_v1`
  ABI. The earlier `LLVM013 match arm must
  be a payload-free enum variant` wall was a **source inconsistency**:
  `lexer.osty` (3 sites) and `lossless_lex.osty` (3 sites) matched on
  `FrontDiagBadNumericSeparator`, but the `FrontLexDiagnosticCode`
  enum in `frontend.osty` never declared that variant. Adding it to
  the enum closed all six sites. Unblocking that wall uncovered a
  second latent gap: the return-stmt path in `emitReturningBlock` and
  `emitReturn` hardcoded `false` for every `listElemString` /
  `mapKeyString` / `setElemString` hint, so `pub fn foo() ->
  List<String> { [literals...] }` tripped `list_mixed_ptr` even
  though the list was fully typed. Fixed by widening
  `emitReturningBlock`'s signature and storing the flags on the
  generator so `emitReturn` can read them back. The earlier `LLVM011 [string_non_ascii]` wall is closed: plain
  String literals with multi-byte UTF-8 content (BOM `\u{FEFF}` at
  `frontend.osty:600`, Unit Separator `\u{1F}` at the monomorph key
  builder, Korean / emoji in user text) now lower through
  `llvmCStringEscape` as one `\HH` escape per UTF-8 byte instead of
  being rejected by the ASCII gate. `llvmCString` also counts bytes
  (not runes) so `[N x i8]` globals stay the right size. The legacy
  `llvmIsAsciiStringText` check is now a no-op stub — kept for
  call-site stability; a future cleanup may inline it into callers. The earlier `list_mixed_ptr` wall was a **source-type
  propagation gap**, not the genuine "heterogeneous literal" case it
  claimed: (1) alias-qualified stdlib strings calls
  (`strings.join(...)`) never fed through `staticExprSourceType` so
  the list-literal isString check couldn't tell they returned String;
  (2) bare `""` literals carried no `sourceType`, so a `let mut line =
  ""` binding dropped the String tag; (3) if-expression phi values
  merged container metadata but not `sourceType`, so `let op = if ...
  { "a" } else { "b" }` also dropped it. All three plumbed through
  the merged toolchain probe at
  `formatter_ast.osty:195` / `275` / `532`. Fixed by adding
  `staticStdStringsCallSourceType`, tagging literal emission with
  `sourceType = String`, and merging agreed-sourceType in
  `mergeContainerMetadata`.
  The earlier LLVM011 `logical not on %PmCheckOutcome` wall is also
  closed: it was a **parser precedence bug**, not a backend gap. The v0.5 grammar has `UnaryExpr ::= prefix UnaryExpr |
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
  The LLVM012 statement-form category is cleared for the toolchain's
  actual shape. The historical wall chain, each entry closed in order:
  `LLVM011 [fn_param_struct_type]` Char at `lspUtf16UnitsForChar`
  (Char→i32 / Byte→i8 lowering plus `Char.toInt()` / `Byte.toInt()` /
  `Int.toChar()` width conversions + unsigned compare predicates);
  `LLVM012 statement: *ast.MatchExpr is not a call` (match-as-statement
  lowering for tag-enum scrutinees with bare-variant / wildcard arms);
  `LLVM012 statement: field assignment base *ast.FieldExpr` (nested
  field chain like `cx.env.returnTy = sig.retTy` — inside-out extractvalue
  descent + innermost-first `llvmInsertValue` rebuild in
  `stmt.go:emitFieldAssign`)
- the current `osty check --airepair=false toolchain` surface is an
  aggregate native-checker summary of `949 error(s)` with
  `26811 / 27501` assignment/return/call checks accepted
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
| Bootstrap bridge | merged whole-toolchain probe | first wall is `LLVM001 foreign-ffi` on `ci.osty`'s `use go "github.com/osty/osty/internal/cihost"`. `toolchain/ast_lower.osty` (dead duplicate of `internal/selfhost/ast_lower.osty`, 1672 LOC) was deleted 2026-04-22, and `toolchain/docgen.osty` + `toolchain/manifest_validation.osty` were ported from `use go "strings"` to `use std.strings as strings` on the same day (including a new `strings.fields` backend shim — runtime `osty_rt_strings_Fields` + `stdStringsCallStaticResult`/`staticStdStringsCallSourceType` entries — to close the AST-emitter static-type gap that was blocking `for part in strings.fields(text)` iteration). Only `ci.osty` remains with `use go "..."`; it must be ported to `runtime.cabi.*` / `runtime.<surface>` bindings (LANG_SPEC_v0.5 §12.8) before the whole probe reports CLEAN |
| Native backend surface | merged native-only probe | **CLEAN** after skipping 1 bootstrap-only file (`#486`, verified 2026-04-21 via `TestNativeToolchainMergedIsClean` + `TestProbeNativeToolchainMerged`). Most recently closed: `LLVM015 [method_call_field]` on `buf.clear()` in `ty.osty` — `List<T>.clear()` now dispatches through `osty_rt_list_clear` in `emitListMethodCallStmt`, with the symbol added to `listMethodInfo`'s whitelist. Preceding `String.bytes()`/`String.chars()` intrinsic gap also closed (lowers to `osty_rt_strings_Chars` / `osty_rt_strings_Bytes` with `listElemTyp` tagged `i32` / `i8`). Earlier closed walls (in order): `LLVM011 [fn_param_struct_type]` Char on `lspUtf16UnitsForChar`; `LLVM012 *ast.MatchExpr is not a call` (match-as-statement lowering); `LLVM012 field assignment base *ast.FieldExpr` (nested `a.b.c = x` via `llvmInsertValue` rebuild chain); parser precedence `(!x).y` / `!(x.y)` hoisted at stable-AST lowering; `LLVM011 [list_mixed_ptr]` source-type propagation (stdlib strings alias calls, bare `""` literals, if-expr phi branches — `staticStdStringsCallSourceType` + literal sourceType tagging + mergeContainerMetadata sameSourceType); `LLVM011 [string_non_ascii]` multi-byte UTF-8 literals (BOM / Unit Separator / Korean / emoji) now byte-escaped via `\HH` in `llvmCStringEscape`, with `llvmCString` counting UTF-8 bytes instead of runes; `LLVM013 match arm must be a payload-free enum variant` was a source inconsistency (missing `FrontDiagBadNumericSeparator` enum variant) + a latent return-path gap (hardcoded `listElemString=false` in `emitReturningBlock` / `emitReturn`). List / Map / Set `isEmpty`, nested `IndexExpr`, and `list.pop()` discard sites also closed |
| Public runtime scheduler | `osty_rt_*` task/thread/select | `#496` complete. Select-send arm landed as typed entry points `osty_rt_select_send_{i64,i1,f64,ptr,bytes_v1}` (scalar packing into channel ring slot, bytes via GC-managed copy). The public LLVM runtime now has zero `osty_sched_unimplemented` call sites — concurrency spec §8 (taskGroup / spawn / join / cancel / chan / select / parallel / race / collectAll) is fully covered. See RUNTIME_SCHEDULER.md |
| MIR Osty port | `toolchain/mir.osty` | `#503` — MIR core (intrinsic kinds, printer, operand/instr shapes) now has an Osty-native mirror in `toolchain/mir.osty`; Go remains authoritative while the Osty side participates in the spec corpus |
| Checker boundary | `internal/check` / `internal/toolchain` | host still manages an external `osty-native-checker` artifact and falls back to the embedded selfhost checker when it cannot be prepared |
| Toolchain package health | `osty check --airepair=false toolchain` | current CLI surface is still an aggregate `E0700` summary (`949 error(s)`, `26811 / 27501` accepted) rather than a clean self-compile pass |
| Stdlib / string surface | `internal/llvmgen/stdlib_shim.go`, `expr.go` | a subset of `std.strings` is shimmed through runtime helpers. `Char` and `Byte` parameters/returns, literals, comparisons, and width conversions lower; `String.chars` / `String.bytes` lower to `osty_rt_strings_Chars` / `osty_rt_strings_Bytes` producing GC-managed lists with `listElemTyp` tagged `i32` / `i8`, and downstream iteration takes the `_bytes_v1` ABI. What remains is retiring the runtime-backed `std.strings` shim in favor of pure-Osty bodies built on those primitives |

The MIR-direct emitter itself (Stages 3.1–3.11) covers a growing subset of the
language shapes toolchain uses. The late 2026-04-21 refresh narrows the
story further: backend entry is no longer the first blocker, the
native-only merged probe is CLEAN (`#486`, guarded by
`TestNativeToolchainMergedIsClean`), and the public runtime has no
unimplemented scheduler paths left (`#496`, select-send arm landed —
zero `osty_sched_unimplemented` call sites). What remains between here
and "fully self-hosted native binary" is (a) the bootstrap-only bridge
`runtime.golegacy.astbridge` on the whole-toolchain merged probe, and
(b) the aggregate native-checker summary (`949 error(s)`,
`26811 / 27501 accepted`) on `osty check --airepair=false toolchain` —
neither of which is a backend wall anymore.

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

Observed in the 2026-04-21 refresh:

- `/tmp/osty gen --backend=llvm /tmp/hello.osty` exited 0 and emitted a valid
  `.ll` module containing `define i32 @main()`
- the targeted `internal/llvmgen` regression set returned
  `ok github.com/osty/osty/internal/llvmgen`
- `TestProbeWholeToolchainMerged` reported
  `LLVM002 runtime-ffi: ... runtime.golegacy.astbridge ...` (at the 2026-04-21
  snapshot; after the 2026-04-22 deletion of `toolchain/ast_lower.osty` the
  first wall shifted to `LLVM001 foreign-ffi` on `ci.osty`'s `internal/cihost`
  import)
- `TestProbeNativeToolchainMerged` skipped
  `ci.osty` only (the 2026-04-22 port of `docgen.osty` + `manifest_validation.osty`
  from `use go "strings"` to `use std.strings as strings`, plus the
  `ast_lower.osty` deletion, trimmed the skip list from 4 to 1) and
  reported `NATIVE TOOLCHAIN first wall: CLEAN`. The authoritative
  regression gate `TestNativeToolchainMergedIsClean`
  (`internal/llvmgen/native_toolchain_clean_test.go`) passed,
  fail-fast on any re-introduced wall. The earlier
  `LLVM011 [list_mixed_ptr]`, `LLVM011 [fn_param_struct_type]` Char,
  `LLVM012 MatchExpr is not a call`, `LLVM012 field assignment base
  *ast.FieldExpr`, `LLVM011 [string_non_ascii]`, `LLVM011 [other]
  String.bytes`, and `LLVM015 [method_call_field] buf.clear()`
  (`List<T>.clear`) walls are all closed
- `/tmp/osty check --airepair=false toolchain` exited with the aggregate
  summary
  `native checker reported type errors: 949 error(s)` plus
  `native checker accepted 26811 of 27501 assignment/return/call checks`

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
error[E0700]: native checker reported type errors: 949 error(s)
= note: native checker accepted 26811 of 27501 assignment/return/call checks
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

Fix: in `collectStableAliasProvenance`, suppress the alias provenance
step when the identifier is immediately followed by `:` (parameter /
struct field / keyword-argument slot), and more generally when
`isAliasInExpressionPosition` detects LHS-of-assign / `.`-access / `,` /
`)` / `]` / `let`/`mut` / return / arrow / binary-op surroundings that
make keyword interpretation impossible. Regression test in
`internal/parser/parser_features_test.go::TestParseStableAliasesPreservedAsIdentifiers`.

### E0700 — aggregate checker summary

```
native checker reported type errors: 1700 error(s)
note: native checker accepted 26191 of 26850 assignment/return/call checks
```

On the current tree the visible summary is `949` errors with
`26811 / 27501` accepted, so the old `1700` figure should now be treated as a
historical snapshot only. The error count dropped substantially as the native
checker coverage grew; large summary counts still hide a much smaller set of
root-cause wedges, and the whole/native probe results suggest
`List<String>` / ptr-backed collection lowering and bootstrap-only host
boundaries are now higher-priority roots than the old parser alias bug.

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

1. **Keep the native-merged clean gate authoritative.**
   `TestNativeToolchainMergedIsClean` now locks the "no wall" state as a
   fail-fast regression gate (native-only merged probe CLEAN per `#486`).
   Future changes that re-introduce a wall surface here immediately
   (e.g. adding a new method or literal shape the statement-form
   dispatcher doesn't recognize yet). When a new wall appears, treat it
   as Tier A: resolve in a follow-up PR and reference this gate.

2. **Shrink the aggregate checker summary on the current tree.**
   Re-profile the `949`-error native-checker summary into a current
   histogram before making more claims from the 2026-04-18 sample —
   the drop from the earlier `1700` / `3846` figures already suggests
   the root-cause set has shifted. With the native probe CLEAN and the
   scheduler (`#496`) complete, this is now the primary unlock for
   "self-compile clean."

3. **Retire the runtime-backed `std.strings` shim.**
   Now that `String.chars()` / `String.bytes()` lower to real
   `List<Char>` / `List<Byte>` values, rewrite the pure-Osty
   `std.strings` bodies to use those primitives and drop the
   `internal/llvmgen/stdlib_shim.go` routes that remain only as a
   bridge.

4. **Port more of the toolchain/compiler core into `toolchain/*.osty`.**
   `#503` landed MIR in Osty. The natural next movers are the pieces
   still living in Go but already expressible in Osty (lint / format /
   check policy). See [AGENTS.md](../AGENTS.md) for the "Osty로 짤 수
   있는 건 Go로 짜지 마" rule.

5. **Retire the bootstrap-only bridge files from the critical path.**
   Three of the original four bootstrap-only files are gone as of
   2026-04-22: `toolchain/ast_lower.osty` was deleted (dead duplicate
   of `internal/selfhost/ast_lower.osty`), and `toolchain/docgen.osty`
   + `toolchain/manifest_validation.osty` were ported from
   `use go "strings"` to `use std.strings as strings` after closing
   the `strings.fields` AST-emitter gap (new `osty_rt_strings_Fields`
   runtime helper + `stdStringsCallStaticResult` /
   `staticStdStringsCallSourceType` entries in
   `internal/llvmgen/{stdlib_shim.go,type.go}`). The remaining
   bootstrap-only file is `toolchain/ci.osty`, whose
   `use go "github.com/osty/osty/internal/cihost"` must be ported to
   `runtime.cabi.<lib>` / `runtime.<surface>` with matching `osty_rt_*`
   runtime ABI helpers (real host I/O — FS / process — can't fold
   into `std.strings`). With the native-only probe CLEAN, this is
   the last backend-adjacent wedge on the merged surface. Also
   outstanding: the Go CLI is still wired through
   `internal/selfhost/ast_lower.osty` + `internal/selfhost/astbridge/`
   — both become dead code once `cmd/osty` consumes
   `parser.osty`'s `AstArena` directly.

6. **Then re-run `osty check toolchain` and per-file `osty gen
   --backend=llvm` probes.** Once the checker summary, stdlib-body,
   and bootstrap-bridge wedges move, the remaining tail should become
   a much narrower backend/runtime parity queue.
