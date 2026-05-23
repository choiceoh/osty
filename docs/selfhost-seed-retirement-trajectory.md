# `internal/selfhost/generated.go` retirement trajectory

**Status**: audit + plan (no code change).
**Scope**: identify every dependency on the 70k-LOC frozen seed at
`internal/selfhost/generated.go` and propose a staged retirement
sequence that maintainers can execute as small focused PRs.

**Constraints carried over from `CLAUDE.md` + `docs/llvm-selfhost-plan.md`**:

- The seed is a **checked-in build artifact**. Regenerating it from
  `toolchain/*.osty` is explicitly out of scope — the bootstrap
  Osty→Go transpiler was retired in PR #854.
- "Retirement" here means **making the seed unnecessary**, not
  hand-editing it. New `toolchain/*.osty` changes flow exclusively
  through the LLVM-built path. The seed survives as a frozen
  snapshot until its sole consumers can swap to LLVM-built or
  subprocess alternatives.
- The "narrow exception" carved out in `CLAUDE.md` (cross-package
  method-call dispatch surgical hand-edits) is a separate trajectory
  tied to `SPEC_GAPS.md::cross-pkg-module-resolution` and is NOT
  what this doc addresses.

## Inventory

44 Go packages import `github.com/osty/osty/internal/selfhost` today
(`grep -rln 'internal/selfhost"' --include='*.go'`). The seed
provides four pipeline phases in-process plus a wide surface of
exported types. Use counts come from
`grep -oE 'selfhost\.[A-Z][A-Za-z0-9_]*' | sort | uniq -c`.

### Surface by phase

| Phase | Entry points (selected) | Consumers | Notes |
|---|---|---|---|
| **A. Lex** | `Lex`, `Run.Tokens`, `Run.Comments` | `internal/parser`, `internal/format`, `internal/lint`, `internal/airepair` | Returns `[]token.Token`. Seed owns string interp + ASI + triple-quoted. |
| **B. Parse** | `Run`, `ParseCST`, `LowerPublicFileFromRun`, `FrontendRun` | `internal/parser`, `internal/pipeline`, `internal/resolve`, `internal/query/osty`, `cmd/osty/*`, every LSP read path | The widest seed dependency. `*selfhost.FrontendRun` is a structural field on `parser.Result`, `pipeline.Result`, `Package.Files[i].Run`. |
| **C. Resolve** | `ResolveSourceStructured`, `ResolvePackageStructured`, `ResolveFromSource`, `ResolveStructuredFromRun`, `ResolveSourceStructuredWithCfg` | `internal/resolve/*.go`, `cmd/osty-native-resolver` | Provides workspace-level name resolution. |
| **D. Check** | `CheckSourceStructured`, `CheckPackageStructured`, `CheckStructuredFromRun`, `CheckFromSource` | `internal/check`, `cmd/osty-native-checker/main.go` (the subprocess shell itself) | Production CLI already routes through the subprocess (`check.UseManagedSubprocessChecker`), but the subprocess **executes** the seed via this surface — so D is "behind" the subprocess boundary, not in front of it. |

### Surface by purpose (orthogonal slice)

| Category | Symbols | Retirement difficulty |
|---|---|---|
| **API types** | `CheckResult`, `TypeRepr`, `PackageCheckInput`, `PackageCheckFile`, `PackageCheckImport`, `CheckDiagnosticRecord`, `LSPSymbolView`, `LSPUseDeclView` | **Keep**. These are wire-shape contracts shared with the LLVM-built checker (`toolchain/check_json.osty` emits the same JSON shape). Move them to a leaf package (`internal/selfhost/api` partially exists) so callers can depend on shapes without dragging the seed. |
| **LSP policy** | `LSPSemanticTypeForTokenKind`, `LSPCompletionSortTextForSymbolKind`, `LSPHoverSignatureLine`, `LSPPathToURI`, `LSPServerName`, `LSPPositionEncodingUTF16`, ~30 more | **Stateless**. Each function is a pure value lookup. Easy to port to `toolchain/lsp.osty` (the spec already mandates this — `CLAUDE.md` "LSP / 툴체인 셀프호스트" section). Until ported, can be re-implemented as a small Go const table without the seed. |
| **In-process pipeline** | `Run`, `ParseCST`, `Lex`, `LowerPublicFileFromRun` (Phase B), `Resolve*` (Phase C) | **Hardest**. Every front-end caller (parser, resolver, LSP, checker fallback) holds a `*FrontendRun` and walks the seed's internal AST representation. Retiring this means either (a) every caller goes through a subprocess like the checker already does, or (b) the LLVM-built variant of `Run`/`Resolve` becomes available in-process via cgo / static linking. |
| **Frozen seed core** | Everything else in `generated.go` (Result/Option runtime, generic monomorphization scaffolding, ~30 closure type families) | **Indirect**. No package imports these directly; they're called by the in-process pipeline entry points. Once Phase B/C/D callers are gone, the unused seed-internal symbols can be deleted with `go build` as the witness. |

## Trajectory (smallest → largest)

The order is engineered so each step is mergeable independently and
the seed becomes progressively less reachable. After each step,
running `go build ./...` + the full test suite + the new per-PR
fresh-clone gate (PR #1992) is the success criterion.

### Step 1 — Move API types to a leaf package (`internal/selfhost/api`)

**Scope**: ~20 type declarations (`CheckResult`, `TypeRepr`,
`PackageCheckInput`, …) currently in `generated.go` move to
`internal/selfhost/api/*.go`. Re-exports stay in the `selfhost`
package until step 2 to avoid a 44-package churn diff. The
`internal/selfhost/api` package already exists for a subset; this
step finishes the migration so the wire shapes have no `generated.go`
ancestry. After: the LLVM-built checker subprocess and the seed-
backed checker depend on the SAME `internal/selfhost/api` types,
making byte-parity diffs structural rather than re-derived.

- **PR size**: small — types-only, no logic.
- **Risk**: low — re-exports keep callers compiling.
- **Dependencies**: none.

### Step 2 — Port LSP policy helpers to `toolchain/lsp.osty`

**Scope**: every `selfhost.LSP*` helper moves to `toolchain/lsp.osty`
(CLAUDE.md already requires this: "LSP / 툴체인 셀프호스트 …
순수 에디터 정책 … `toolchain/lsp.osty`"). Go-side
`internal/lsp/policy.go` becomes a thin bridge that calls the
LLVM-built or subprocess equivalent. Until LLVM parity is reached,
inline a Go const-table fallback so the LSP keeps working.

- **PR size**: medium — ~30 entry points, but each is stateless.
- **Risk**: low — LSP behavior is observable via existing
  `internal/lsp/*_test.go` golden snapshots.
- **Dependencies**: none.
- **By-product**: kills ~1300 LOC of `lsp_policy.go` even before any
  seed touch; that file isn't part of `generated.go` but the helpers
  it re-exports often delegate into the seed.

### Step 3 — Replace `cmd/osty-native-checker/main.go` with the LLVM-built variant

**Scope**: today the Go-built subprocess (the very binary
`UseManagedSubprocessChecker` runs in production for in-process
callers, AND the binary the new `OSTY_STAGE0_FALLBACK=1` fallback
falls back to) calls `selfhost.CheckPackageStructured` inside its
`main()`. Once the LLVM-built variant reaches M5 (package mode,
multi-line stdin, JSON escape — currently mostly done per
`cmd/osty-native-checker/README.md`), production switches default to
the LLVM-built path AND the Go-built shell either retires or shrinks
to a stage0-fallback-only artifact.

- **PR size**: large but well-tracked under
  `docs/llvm-selfhost-plan.md` M5+.
- **Risk**: high — production checker swap. Gate behind the per-PR
  fresh-clone CI from PR #1992 + the byte-parity corpus from
  `docs/llvm-selfhost-plan.md` §4.
- **Dependencies**: Step 1.
- **By-product**: Phase D's seed entry (`CheckPackageStructured` etc)
  becomes reachable only from stage0 fallback. The "frozen seed
  silently lags toolchain/*.osty" risk drops from "every check" to
  "fresh-clone bootstrap only".

### Step 4 — Subprocess-or-LLVM the in-process Resolve callers (Phase C)

**Scope**: `internal/resolve/*.go` is the second-largest seed
consumer (~10 entry points). Each invocation today does
`selfhost.ResolvePackageStructured(input)` and consumes
`*selfhost.FrontendRun`. The path forward mirrors Step 3: route via
the `cmd/osty-native-resolver` subprocess (which already exists per
the inventory) and drop the in-process seed call. After this step,
`internal/resolve` no longer transitively imports the seed.

- **PR size**: large — touches every resolver entry point.
- **Risk**: high — resolver is on every front-end pipeline.
- **Dependencies**: Steps 1 + 3 (so Resolve and Check share the same
  subprocess boundary discipline). Probably wants the same per-PR CI
  gate that Step 3 introduces.

### Step 5 — Subprocess-or-LLVM the Phase B (Run / ParseCST) callers

**Scope**: this is the seed's widest reach: 15+ packages hold
`*selfhost.FrontendRun`. Each call to `selfhost.Run(src)` parses via
the seed and returns the run object. The retirement path is
*structurally* the same — fork to a subprocess for parse, get the
public ast.File back, drop the in-process call — but the call-site
churn is the largest of any single step.

- **PR size**: very large — likely multiple sequential PRs grouped by
  caller cluster (parser cluster, pipeline cluster, query cluster,
  LSP cluster).
- **Risk**: high — every front-end path.
- **Dependencies**: Steps 1 + 3 + 4.
- **Open question**: Step 5 might want a different approach than
  "subprocess every Run()" — the LSP responsiveness budget is
  millisecond-tight. Alternatives: (a) statically link the LLVM-built
  parser into the host binary via cgo, (b) keep an in-process parser
  written natively in Go (a slimmed-down re-implementation), (c)
  long-lived parser daemon process the LSP talks to. Decision belongs
  with the maintainer at Step 4 completion.

### Step 6 — Delete unreached `generated.go` symbols

**Scope**: with Phases B/C/D all routed through subprocesses or
LLVM-built code, the seed becomes "imported but unused". `go build`
will keep optimizing away unreachable code, but humans see 70k LOC
of dead text. This step is a pure cleanup: identify the symbols
nothing imports (modern tools: `unused`, `deadcode`) and strip them
from `generated.go`. Result: the file shrinks, and at some terminal
point the file deletes entirely.

- **PR size**: shrinking; final PR is "delete `generated.go`".
- **Risk**: low once the prerequisite steps land — no behavior change.
- **Dependencies**: Steps 1 + 2 + 3 + 4 + 5.

## What this trajectory does NOT do

- **Does not regenerate `generated.go`**. The frozen seed stays as-is
  until each step's prerequisites land.
- **Does not retire the stage0 source-bootstrap path**. The Go-built
  checker shell that PR #1988 detours to on fresh-clone bootstrap
  still depends on the seed; retiring that depends on the LLVM-built
  variant being available without needing `osty-self` first (which is
  itself a chicken-and-egg, separately tracked).
- **Does not touch the cross-package method-call dispatch narrow
  exception** carved out in `CLAUDE.md` "하지 말 것" — that's a
  separate trajectory tied to
  `SPEC_GAPS.md::cross-pkg-module-resolution`.
- **Does not promise a timeline**. Each step is mergeable when its
  prerequisites land, not on a calendar.

## Verification gates (apply at every step)

Each step's PR should pass:

1. `go test -short ./...` — every existing test still passes.
2. The new per-PR fresh-clone bootstrap workflow
   (`.github/workflows/fresh-clone-source-bootstrap.yml`, PR #1992).
3. `bootstrap-smoke-test.yml` (weekly) is not broken — covers the
   registry path the trajectory doesn't touch.
4. For Steps 3-5: a byte-parity diff between the seed-backed and the
   replacement implementation, across the corpus described in
   `docs/llvm-selfhost-plan.md` §4. Mismatches block the merge.
5. For Step 6: a dead-code scan against the staged tree, with the
   removed-symbol list attached to the PR description.

## Pointers for the next-session maintainer

- This document is the audit + plan. Implementation lives in
  per-step PRs.
- Step 1 is the lowest-risk concrete next move and a good warm-up
  before Steps 3-5.
- `cmd/osty-native-checker/README.md` tracks LLVM-built checker
  M-milestones; Step 3 prerequisites are exactly those Ms.
- `docs/llvm-selfhost-plan.md` §11 ("Out of scope") still says "no
  generated.go regeneration" — this trajectory does not violate that
  constraint. Retirement is by replacement, not regeneration.
- File `SPEC_GAPS.md` should reference this trajectory when seed-
  related items appear, so the audit doesn't go stale.
