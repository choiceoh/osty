# Osty

A work-in-progress implementation of the **Osty** programming language — a
general-purpose, statically-typed, GC'd language specified in
[`LANG_SPEC_v0.5/`](./LANG_SPEC_v0.5/README.md) with grammar fixed in
[`OSTY_GRAMMAR_v0.5.md`](./OSTY_GRAMMAR_v0.5.md).

The target is a self-hosted native runtime and LLVM backend. Current scope:
front-end (lex → parse → resolve → type-check), multi-file packages and
workspaces, formatter, linter, a JSON-RPC LSP server, an Osty-authored LLVM
emitter core for the native backend, project scaffolding (`osty new` /
`osty init`), a manifest-driven build orchestrator (`osty build`) that reads
`osty.toml` / `osty.lock` and threads the front-end + native backend across
declared packages, API documentation generation (`osty doc`), CI quality
tooling (`osty ci`), profile/target/feature/cache inspection commands, and a
package manager (`osty add` / `osty update` / `osty publish`) backed by a
file-backed HTTP registry server for local/private registries. The repository
tracks spec/grammar work in v0.5, but the shipped manifest/scaffolder path is
still pinned to edition v0.4 today (`osty new` / `osty init` write
`edition = "0.4"`, and checked-in fixtures still exercise legacy v0.3/v0.4
inputs). The next work is native implementation/runtime coverage. The public
compiler path is now native-only through the LLVM backend.

## Status

| Phase | Status |
|---|---|
| Lexer (UTF-8, ASI, triple-quoted strings, interpolation) | done |
| Parser (selfhosted front end, error recovery, fuzz-clean) | done |
| AST (all node kinds implement `ast.Node`) | done |
| Diagnostics (`error[E0002]:` with caret, hints, notes) | done |
| Name resolution (single + multi-file, workspace, typo suggestions) | done |
| Formatter (`internal/format`) | done |
| Type checker (`internal/check`) | done for the shipped v0.4 front-end core — generic instantiation, structural interface checks, exhaustiveness, builder protocol, function-value arity, closure pattern params. Algorithm: bidirectional + local unification, spec in [`LANG_SPEC_v0.5/02a-type-inference.md`](./LANG_SPEC_v0.5/02a-type-inference.md); `osty check --inspect` observes it at runtime |
| Linter (`internal/lint`, L0001–L0042, `--fix` / `--fix-dry-run`) | done |
| Multi-file packages (`resolve` loader/package/workspace) | done |
| LSP (`internal/lsp`, wired as `osty lsp`) | done — hover, definition, formatting, documentSymbol, lint diagnostics, editor policy backed by toolchain sources |
| Native LLVM backend (`internal/backend`, `internal/llvmgen`) | public backend path; scalar/control-flow/string smoke subset emits LLVM IR/object/binary, later phase 64-73 value/control-flow smoke expansion is documented, unsupported shapes report Osty-authored LLVM diagnostics |
| Bootstrap Go transpiler (`internal/bootstrap/gen`, `cmd/osty-bootstrap-gen`) | developer-only tool that regenerates `internal/selfhost/generated.go` from the toolchain sources; not part of the public `osty` CLI |
| Independent IR (`internal/ir`) | done — patterns, match, closures, struct/field/method, generic free-fn + generic struct/enum monomorphization with Itanium-mangled specializations (`ir.Monomorphize`, invoked from `backend.PrepareEntry`; fn symbols use `_Z…`, nominal types use `_ZTS…`) |
| Project scaffolding (`internal/scaffold`, `osty new` / `osty init`) | done — `--bin`, `--lib`, `--workspace`, `--cli`, `--service` |
| Manifest + lockfile + SemVer (`internal/manifest`, `lockfile`, `pkgmgr/semver`) | done (parse + validate + resolve) |
| Build orchestrator (`osty build`) | done — manifest → front-end → native backend, profile/target/feature wiring, backend-aware artifact/cache paths |
| `osty test` | native backend harness — discovers `test*` functions, compiles each through the LLVM backend, runs in parallel by default with a seeded shuffled order (`--seed`, `--serial`, `--jobs`), reports per-test wall time and an `ok/FAIL` summary; assertions are intercepted by the LLVM generator and failures exit non-zero with the source location. `benchmark`/`snapshot` and per-argument structural diff are not implemented yet |
| API doc generator (`internal/docgen`, `osty doc`) | done — checked-in generated Go package, HTML + markdown, field docs, cross-refs, `--check`, `--verify-examples`, workspace mode |
| CI quality tooling (`internal/ci`, `osty ci`) | done — Osty-authored generated CI core, signature-aware snapshots, workspace coverage, JSON reports |
| Pipeline visualizer (`osty pipeline`) | done — per-stage timing, workspace mode, backend-aware gen, baseline diff, LSP trace, `--explain` |
| Profiles / targets / features / cache (`internal/profile`, `osty profiles` / `targets` / `features` / `cache`) | done — built-in and manifest profiles, cross-target env, feature closure + file pragmas, backend-aware fingerprints |
| LLVM backend (`internal/backend`, `internal/llvmgen`, `--backend llvm`) | early executable slice — textual IR/object/binary for scalar/control-flow/Bool/String and `Float`/String payload enum smoke programs, plus simple struct aggregates and enum matches (payload-free + single-`Int` and phase-54-63 payload generalization), then phase 64-73 value/control-flow smoke expansion, host `clang` driver, inspectable skeleton + categorized diagnostics for unsupported source shapes |
| Package registry backend / `osty registry serve` | done — file-backed HTTP server for index/search/download/publish/yank, with ETag index responses and bearer-token write auth |
| Package registry / `osty add` / `osty update` / `osty run` | done (resolve + vendor + lockfile-honoring re-resolves, ETag-cached registry index, copy fallback for symlink-less filesystems; CLI: `add`, `remove`/`rm`, `update`, `run`, `fetch`, `publish`, `search`, `info`, `yank`/`unyank`, `login`/`logout`; `--locked` / `--frozen` CI guards) |
| Package manager (`osty add` / `osty update`, path + git + registry sources, SemVer resolver, deterministic lockfile) | wired — `add` mutates `osty.toml` and re-vendors; `update` re-resolves selectively or in full |
| `osty run` (build + exec through backend) | wired — resolves manifest, vendors deps, emits the native entry artifact, runs the backend binary with profile/feature flags, and rejects cross-target execution |
| `osty publish` (pack + upload tarball to a registry) | wired — deterministic gzipped tar, sha256 checksum, bearer-auth POST; `--dry-run` stops before upload |

Status note (revalidated 2026-04-19): the universal LLVM CLI wedge called out
in older status docs is closed again. A fresh hello-world
`osty gen --backend=llvm` run exits 0 and emits `.ll`, and the four targeted
`internal/llvmgen` regressions previously cited in
[`docs/toolchain_llvm_status.md`](./docs/toolchain_llvm_status.md) now pass.
Current selfhosting/toolchain tracking is therefore about remaining front-end
and toolchain-surface gaps, not a blanket "all LLVM CLI calls panic" state.

The front-end (lex → parse → resolve → type-check) is **coverage-complete
for the v0.4 core**: spec blocks parse, package/workspace resolution is
covered, reject-rules have stable `Exxxx` diagnostics, and the checker
now covers generic call-site instantiation, interface satisfaction,
match exhaustiveness/unreachable arms, builder/default/toBuilder,
method references as values, cross-file partial-method collisions,
builtin type-position arity, function-call turbofish arity, and
cross-package call arity. v0.4 additionally locks positional-only exact
arity for erased function values and irrefutable-only closure parameter
patterns.

### v0.4 Edge-Case Sweep

v0.4 closes the still-soft language corners without adding a large new
surface area. The resolved decisions are archived in
[`SPEC_GAPS.md`](./SPEC_GAPS.md):

- structured-concurrency escape rules for `Handle<T>` / `TaskGroup`
- exact semantics for generic method turbofish and method references
- callable arity after function/default metadata is erased
- closure parameter pattern implementation parity
- finite witness diagnostics for the remaining nested pattern shapes
- any stdlib protocol edge cases discovered while Tier 2 modules are
  moved from prose to checked stubs

### Backend Status

`osty gen FILE` uses the native LLVM backend and writes LLVM IR unless another
native artifact mode is requested by a build/run command.

LLVM binary emission links a local backend runtime ABI object from the backend
runtime directory at native link time for the `osty.gc.*` surface. That is the
native runtime bridge for the executable binary, not a claim of full Go GC
parity.

## Layout

```
osty/
├── LANG_SPEC_v0.5/          # Current spec prose / design target
├── OSTY_GRAMMAR_v0.5.md     # Current EBNF grammar + decision log
├── SPEC_GAPS.md             # Resolved-gap archive by language version
├── LLVM_MIGRATION_PLAN.md   # Native backend migration history/plan
├── LLVM_PHASE1_BASELINE.md  # Legacy Go-backend baseline for LLVM migration
├── LLVM_BACKEND_CORPUS.md   # Backend parity fixture classes and smoke set
├── LLVM_ARTIFACT_LAYOUT.md  # Backend-aware output/cache layout policy
├── cmd/
│   ├── osty/                # Main CLI (`osty` binary)
│   ├── osty-bootstrap-gen/  # Dev-only seed transpiler (regenerates internal/selfhost/generated.go)
│   ├── osty-native-checker/ # Host subprocess that runs the native Osty checker
│   └── codesdoc/            # Regenerates ERROR_CODES.md from codes.go
├── internal/
│   ├── token/               # Token kinds + positions
│   ├── lexer/               # Thin Go facade over internal/selfhost tokenization
│   ├── ast/                 # AST node types
│   ├── parser/              # Thin Go facade + compatibility lowerings over internal/selfhost
│   ├── selfhost/            # Committed bootstrap-generated front end + adapters
│   ├── diag/                # Diagnostics + Rust-style renderer
│   ├── resolve/             # Name resolution (single + multi-file)
│   ├── stdlib/              # Built-in prelude symbols + `modules/*.osty`
│   ├── types/               # Semantic types (shared by checker + LSP)
│   ├── check/               # Type checker
│   ├── lint/                # Style/correctness lint rules (L0xxx codes)
│   ├── format/              # Canonical-style formatter
│   ├── ir/                  # Independent intermediate representation
│   ├── backend/             # Backend names, emit modes, native artifact layout
│   ├── bootstrap/gen/       # Dev-only Osty→Go transpiler used by osty-bootstrap-gen (NOT a public backend)
│   ├── llvmgen/             # LLVM bridge generated from Osty toolchain backend logic
│   ├── docgen/              # Osty-authored API doc generator (HTML + markdown; `osty doc`)
│   ├── ci/                  # CI quality tooling (`osty ci`, generated core)
│   ├── cihost/              # Go host bridge for generated CI core
│   ├── profile/             # Build profiles / targets / features
│   ├── lsp/                 # Language server (stdio JSON-RPC)
│   ├── pipeline/            # Shared phase runner / timing helpers
│   ├── scaffold/            # `osty new` / `osty init` project templates
│   ├── tomlparse/           # Generic TOML parser (subset)
│   ├── manifest/            # osty.toml parse + validate + lookup
│   ├── lockfile/            # osty.lock read/write
│   ├── registry/            # Package registry client + file-backed HTTP server
│   └── pkgmgr/              # Dependency resolution, fetch/vendoring, SemVer
├── examples/                # Executable/sample packages kept under compiler coverage
├── toolchain/               # Osty-authored compiler/tooling cores and LLVM emitter prototype
└── testdata/                # .osty fixtures used by tests and backend corpus
```

## Building

Requires Go 1.26.2 or newer (matching `go.mod`).

```sh
go build -o osty ./cmd/osty
```

## Native Checker

`internal/check` prefers an external checker executable boundary when one is
available. By default the CLI manages a versioned checker artifact under
`.osty/toolchain/<tool-version>/osty-native-checker` and builds it on demand,
but falls back to the embedded selfhost checker when that binary is unavailable.
`OSTY_NATIVE_CHECKER_BIN` is still supported as a strict override/debug escape
hatch.

This repository also ships a repo-local wrapper at
[`scripts/osty-native-checker`](./scripts/osty-native-checker), backed by
[`cmd/osty-native-checker/`](./cmd/osty-native-checker/).

Normal use no longer needs any env var or prebuilt checker binary:

```sh
go run ./cmd/osty check examples/calc
```

To force a specific checker binary:

```sh
export OSTY_NATIVE_CHECKER_BIN="$PWD/scripts/osty-native-checker"
go run ./cmd/osty check examples/calc
```

Or wrap a single command:

```sh
./scripts/with-native-checker go run ./cmd/osty build --backend llvm --emit llvm-ir examples/calc
```

If you prefer a prebuilt binary for speed:

```sh
go build -o .osty/bin/osty-native-checker ./cmd/osty-native-checker
export OSTY_NATIVE_CHECKER_BIN="$PWD/.osty/bin/osty-native-checker"
```

## Runtime GC

The active GC implementation path is the LLVM/native runtime path, not the
large executable model under `examples/gc`.

The current source of truth is:

- [`RUNTIME_GC.md`](./RUNTIME_GC.md)
- [`internal/llvmgen/llvmgen.go`](./internal/llvmgen/llvmgen.go)
- [`internal/backend/runtime/osty_runtime.c`](./internal/backend/runtime/osty_runtime.c)

`examples/gc` remains useful as a prototype/invariant lab, but new GC
implementation work should land in the runtime path first.

## CLI

```sh
osty new NAME          # scaffold a new project directory (--bin, --lib, --workspace, --cli, --service)
osty init              # scaffold into the current directory (same kind flags; --name, --member)
osty build [DIR]       # manifest-driven: manifest → deps → front-end → backend
osty add PKG           # append a dependency to osty.toml and re-resolve
osty remove NAME...    # drop dependencies from osty.toml and re-resolve (alias: rm)
osty update [NAMES...] # refresh the lockfile (selective or full)
osty run [-- ARGS...]  # build and exec the binary through the native backend
osty test [PATH|FILTERS...] # discover test* functions and run them through the native backend (--seed, --serial, --jobs)
osty publish           # pack the project and upload to a registry
osty search QUERY      # full-text search the registry (--registry, --limit)
osty info PKG          # show registry metadata for a package (--all-versions)
osty fetch             # resolve + vendor without building (--locked, --frozen)
osty registry serve    # run a local/private package registry
osty yank --version V [PKG]   # mark a published version as yanked
osty unyank --version V [PKG] # un-yank a previously yanked version
osty login [--registry N]     # store an API token in ~/.osty/credentials.toml
osty logout [--registry N|--all] # forget a stored token
osty tokens FILE       # print the token stream (debugging)
osty parse FILE        # parse to AST, emit JSON
osty resolve FILE|DIR  # name resolution; directory = package mode (--scopes for tree)
osty check FILE|DIR    # lex + parse + resolve + type-check (diagnostics only)
                       # --inspect prints one record per expression with the
                       # inference rule applied (see LANG_SPEC_v0.5/02a-type-inference.md)
osty typecheck FILE    # same as check, plus a per-expression type dump
osty lint FILE|DIR     # style + correctness warnings (L0xxx codes)
osty fmt FILE          # airepair + format to canonical style (see --check, --write, --engine)
osty airepair FILE     # auto-fix common AI-authored syntax/idiom slips (legacy alias: repair)
osty airepair triage DIR
osty airepair learn DIR
osty airepair promote CASE
osty gen FILE          # emit LLVM IR (see -o, --package)
osty doc PATH          # generate API documentation (HTML + markdown; --check, --verify-examples)
osty ci                # run CI quality checks (signatures, coverage, snapshots)
osty ci snapshot       # capture the exported API baseline
osty profiles          # list build profiles (debug, release, profile, test, ...)
osty targets           # list declared cross-compilation targets
osty features          # list declared opt-in features
osty cache [ls|clean|info] # inspect or prune backend build caches
osty scaffold <kind>   # one-off generators (fixture / schema / ffi)
osty lsp               # run the language server on stdio
osty explain [CODE]    # describe a diagnostic (Exxxx/Wxxxx/Lxxxx); no arg lists every code
osty pipeline FILE|DIR # run every front-end phase; per-stage timing
                       # (--json, --trace, --per-decl, --gen, --backend,
                       #  --emit, --cpuprofile, --memprofile, --baseline)
                       # DIR may be a single package or a workspace root
```

Global flags (precede the subcommand):

- `--no-color` / `--color` — force disable/enable ANSI output
- `--max-errors N` — stop printing after the first N diagnostics
- `--json` — emit diagnostics as NDJSON on stderr (for tooling)
- `--strict` — `lint`-only: exit 1 on any warning (CI mode)
- `--scopes` — `resolve`-only: also dump the nested scope tree
- `--trace` — stream per-phase timing (lex/parse/resolve/check/lint) to stderr;
  applies to `tokens`, `parse`, `resolve`, `check`, `typecheck`, `lint`
- `--explain` — after diagnostics, append the `osty explain CODE` text for each
  unique code; applies to `check`, `typecheck`, `resolve`, `lint`, `parse`, `tokens`
- `--inspect` — `check`-only: emit one record per expression naming the
  inference rule and the type/hint the checker used. Pairs with `--json` for
  NDJSON output. See [`LANG_SPEC_v0.5/02a-type-inference.md`](./LANG_SPEC_v0.5/02a-type-inference.md).

`fmt`-specific flags (after the subcommand):

`osty fmt` runs the same automatic AI repair pass before formatting by
default, so AI-authored syntax slips are normalized in one command.

- `--check` — exit 1 if the file is not already formatted; show diff
- `--write` — rewrite the file in place instead of printing
- `--airepair` — enable the default automatic AI repair pass
- `--no-airepair` — disable the default automatic AI repair pass
  Legacy aliases: `--repair`, `--no-repair`
- `--engine go|osty` — choose the formatter engine. `go` is the default
  AST formatter; `osty` is a compatibility entry point that now shares the
  same AST-backed formatting contract instead of maintaining a separate
  token-heuristic printer.

`airepair`-specific flags (after the subcommand):

Legacy alias: `osty repair`

- `--check` — exit 1 if the file contains repairable syntax slips
- `--write` — rewrite the file in place instead of printing
- `--json` — emit a structured report including before/after front-end diagnostics
  plus `status`/`summary`, `accepted_reason` / `rejected_reason`, residual
  hints (`residual_primary_code`, `residual_primary_habit`), and `change_details`
  metadata (`phase`, `source_habit`, `confidence`) for tooling
- `--capture-dir DIR` — write corpus-ready `.input.osty`, `.expected.osty`, and `.report.json` artifacts to `DIR`
- `--capture-name NAME` — basename to use for captured airepair artifacts
- `--capture-if residual|changed|always` — capture only residual cases by default, or widen to changed/all cases
- `triage DIR` — summarize captured `.report.json` files by status, source habit, and residual diagnostic code
- `learn DIR` — rank captured residual patterns into next-work priorities, with corpus coverage awareness
- `promote CASE` — copy a captured case into `internal/airepair/testdata/corpus/` (override with `--dest DIR` or `--name NAME`)
- `--stdin-name NAME` — filename to use in reports when reading from stdin via `-`
- `--mode auto|rewrite|parse|frontend` — debug acceptance mode; `auto` is the default best-effort mode and should usually be left alone

`osty airepair -` reads from stdin and writes the repaired source (or JSON report
with `--json`) to stdout.

For failure collection, `osty airepair --json --capture-dir tmp/airepair-cases --capture-if residual FILE`
writes corpus-style artifacts only when airepair still leaves residual diagnostics.

For quick triage, `osty airepair triage tmp/airepair-cases`

For an agent-friendly ranked backlog, `osty airepair learn --json tmp/airepair-cases`

To promote one captured case into the checked-in corpus, `osty airepair promote tmp/airepair-cases/foreign_fn_tuple_index_case`

Single-file `osty check`, `osty resolve`, `osty typecheck`, and `osty lint`
run airepair in memory by default before parsing. You can still tune or disable it
after the subcommand:

- `--airepair` — keep the default automatic in-memory airepair enabled
- `--no-airepair` — disable automatic in-memory airepair for debugging/raw parser behavior
- `--airepair-mode auto|rewrite|parse|frontend` — debug acceptance mode; `auto` is the default

The manifest-driven `osty build`, `osty run`, and `osty test` commands also
run airepair in memory by default before any parser / resolver / checker work.
Use `--airepair=false` to disable it or `--airepair-mode auto|rewrite|parse|frontend`
to debug the acceptance heuristic. `osty gen` now uses the same automatic
best-effort in-memory airepair path before loading package sources.

Repairs include common foreign-language carryovers such as `func`/`def`,
`var`/`const`, `while`, `switch`/`case`, `nil`/`null`, Python word
operators, JS `console.log`, semicolons, `=>`, trailing chain dots, and
newline-separated `else`.

`gen`-specific flags (after the subcommand):

- `-o PATH` / `--out PATH` — write the generated artifact to `PATH` instead of stdout
- `--package NAME` — backend package/module name for the emitted file (default: `main`)
- `--backend NAME` — code generation backend (`llvm`; default: `llvm`;
  `llvm` emits textual `.ll` for the early scalar/control-flow/plain/escaped
  string subset, including immutable/mutable string locals and simple String
  function boundaries plus simple struct aggregate values and enum
  tags/match expressions (payload-free + single-`Int` with `{ i64, i64 }`
  payload), plus Phase 54-63 payload enum generalization (`Float` return/param/mut/reversed/wildcard,
  String payload return/param/mut/reversed/wildcard). Unsupported shapes still
  prepare skeleton artifacts and report structured diagnostics from the
  toolchain backend policy)
- `--emit MODE` — requested text artifact. `llvm-ir` emits LLVM IR.

`pipeline --gen` accepts the same source-artifact backend selection:
`--backend llvm --emit llvm-ir` for LLVM IR. Without `--gen`, `--backend` and
`--emit` are rejected because the
pipeline is otherwise front-end only.

### Debugging build / run / test failures

`osty gen`, `osty build`, `osty run`, and `osty test` keep generated
artifacts inspectable. Native backend artifacts include source mappings where
the current emitter can provide them.

```text
// Osty: /path/to/main.osty:12:5
```

When the native backend or test harness fails after the Osty front-end has
succeeded, the CLI prints the generated artifact path and the backend/toolchain
diagnostic. Legacy `use go "..."` imports should move to the runtime ABI
surface (`use runtime.* as name { ... }`) before using the native backend.

`new` / `init`-specific flags (after the subcommand):

- `--bin`, `--lib`, `--workspace`, `--cli`, `--service` — mutually
  exclusive starter layouts; `--bin` is the default
- `--member NAME` — workspace-only: default member directory name
  (default `core`)
- `--name NAME` — `init`-only: override the project name (defaults to the
  current directory basename)

Binary and library starters create `osty.toml`, one source file, one
companion `*_test.osty`, and `.gitignore`. `--cli` and `--service`
create multi-file binary starters with tests. `--workspace` creates a
root manifest plus one default member package. `osty new` never
overwrites an existing directory; `osty init` writes into the current
directory after checking for conflicting files. The scaffolder currently
pins `edition = "0.4"`.

`build` / `run` / `test` / `add` / `update` / `fetch`-specific flags
(after the subcommand):

- `--offline` — do not fetch dependencies; fail if caches are missing.
- `--locked` — refuse to overwrite `osty.lock`; the resolve must match
  the existing pins exactly. Intended for CI ("did the contributor
  forget to commit the lockfile?").
- `--frozen` — implies `--locked` and `--offline`, and additionally
  requires `osty.lock` to already exist. Catches "fresh checkout, no
  lockfile" mistakes before any download starts.

`build` / `run` / `test` backend flags (after the subcommand):

- `--backend NAME` — code generation backend (`llvm`; default: `llvm`;
  `llvm` can write textual IR for the early scalar/control-flow/plain/escaped
  string subset, including immutable/mutable string locals and simple String
  function boundaries plus simple struct aggregate values and enum tags/match
  expressions (payload-free + single-`Int` with `{ i64, i64 }` payload), plus
  the Phase 54-63 payload enum generalization (`Float` return/param/mut/reversed/wildcard,
  String payload return/param/mut/reversed/wildcard) path. `clang`-driven
  object/binary emission now bundles a root-tracked LLVM native runtime for
  String/List/GC ABI symbols, including explicit-collect managed-heap smoke
  coverage, and generated-source diagnostics are available for supported programs;
  unsupported shapes still prepare skeleton artifacts and report missing lowering
  through structured diagnostics from the toolchain backend policy.
- `--emit MODE` — requested artifact mode (`llvm-ir`, `object`, or
  `binary`). `build --backend llvm --emit object|binary` uses `clang`; `run`
  requires `binary` because it executes the result. `osty test` uses the same
  native path under the hood and then runs discovered test functions through the
  LLVM-backed harness.

`profiles` / `targets` / `features` / `cache` commands:

- `osty profiles [--verbose]` — list built-in and manifest-defined profiles,
  including `debug`, `release`, `profile`, and `test`.
- `osty targets` — list manifest `[target.<arch-os>]` cross-compilation
  presets.
- `osty features` — list manifest `[features]` entries and the default set.
- `osty cache ls` — show backend-aware build fingerprints under `.osty/cache/`.
- `osty cache info [--profile NAME] [--target TRIPLE] [--backend NAME]` —
  inspect one cached fingerprint.
- `osty cache clean` — remove `.osty/cache/` and `.osty/out/` build artifacts.

`osty build` loads `osty.toml` starting at the given path (or the cwd),
resolves dependencies against `osty.lock` (regenerated if stale),
vendors deps into `<project>/.osty/deps/`, and runs the front-end
(parse → resolve → check → lint) plus native backend emission across every
package the manifest names. For binary packages it emits backend artifacts into
`<project>/.osty/out/<profile>[-<target>]/llvm/`, invokes the selected native
toolchain, and reports diagnostics with generated-source mapping.

`add`-specific flags (after the subcommand):

- `--path DIR` — local-path dependency (no network). Not combined with
  a positional name; the dir basename becomes the local alias unless
  `--rename` overrides it.
- `--git URL` — git dependency; combine with `--tag REF`, `--branch
  REF`, or `--rev SHA` to pin. Defaults to the repository's HEAD.
- `--version REQ` — registry version requirement (e.g. `^1.0`); also
  accepted as `NAME@REQ` in the positional form.
- `--dev` — add to `[dev-dependencies]` instead of `[dependencies]`.
- `--rename NAME` — override the local alias (what `use <alias>`
  refers to).

`publish`-specific flags (after the subcommand):

- `--registry NAME` — target registry (defaults to `[registries.""]`
  or the built-in default URL).
- `--token T` — API token; also read from `$OSTY_PUBLISH_TOKEN`,
  the token recorded under `[registries.<name>]`, or
  `~/.osty/credentials.toml` (set via `osty login`).
- `--dry-run` — build the tarball into `<project>/.osty/publish/`
  but do not upload.

`yank` / `unyank`-specific flags (after the subcommand):

- `--version V` — the version to flag (required).
- `--registry NAME` — target registry (defaults to the package's
  default registry).
- `--token T` — API token (same fallback chain as `publish`).

The package name is taken from `[package].name` in the local
`osty.toml`; pass an explicit `PACKAGE_NAME` positional to operate on
a different package without leaving its directory.

`search`-specific flags (after the subcommand):

- `--registry NAME` — registry to query (defaults to the project's
  default, or the built-in one when run outside a project).
- `--limit N` — maximum hits to display (default 20; pass 0 for the
  registry's own default page size).

`registry serve`-specific flags (after the subcommand):

- `--addr HOST:PORT` — address to listen on (default `127.0.0.1:7878`).
- `--root DIR` — package index/tarball storage root (default
  `.osty/registry`).
- `--token T` — bearer token required for `publish`, `yank`, and
  `unyank`; defaults to `$OSTY_REGISTRY_TOKEN`.
- `--allow-anonymous-writes` — local-test escape hatch when no token
  should be required.
- `--max-upload-mb N` — maximum package tarball size.

`login` / `logout`-specific flags (after the subcommand):

- `--registry NAME` — which registry the token belongs to. The
  empty/default registry is stored under `default` in the on-disk
  file but addressed as `--registry ""` from the CLI.
- `--token T` — login only; supply the token directly. With no
  `--token`, login reads `$OSTY_PUBLISH_TOKEN`, then falls back to
  reading a single line from stdin (works with `echo $TOKEN | osty
  login`).
- `--all` — logout only; remove every stored token.

`remove` / `rm`-specific flags (after the subcommand):

- `--dev` — only remove from `[dev-dependencies]`.
- `--offline` — re-resolve after removal without contacting any
  registry; fail if caches are missing.

### Package-manager flow

```sh
$ osty new myapp
Created binary project "myapp" at $PWD/myapp
$ cd myapp
$ osty add --path ../../shared
Added dependency shared 0.1.0 (path+../../shared)
$ osty build
Resolved 1 dependencies for myapp v0.1.0
  shared 0.1.0    (path+../../shared)
$ osty run
Hello, Osty!
$ osty publish --dry-run
Packed myapp-0.1.0  (612 bytes, 3a7f12c4b6f0)
Dry run: tarball at .osty/publish/myapp-0.1.0.tgz (not uploaded)
```

The manifest (`osty.toml`) declares deps; the lockfile (`osty.lock`)
pins the exact resolved versions and content hashes. Path deps are
symlinked; git deps are cloned into `~/.osty/cache/git/…` and
snapshotted per commit; registry deps are downloaded + verified
against the advertised sha256 before extraction.

### Example

```sh
$ cat demo.osty
fn main() {
    let msg = "hello, {name}"
    println(msg)
}

$ osty check demo.osty
error[E0500]: undefined name `name`
 --> demo.osty:2:24
    |
  2 |     let msg = "hello, {name}"
    |                        ^^^^ not in scope

  1 error(s), 0 warning(s)
```

## Testing

```sh
go test ./...                                           # unit tests
go test ./... -run TestSpecCodeBlocks -v                # spec markdown coverage
go test ./internal/resolve/ -run TestSpec -v            # positive + negative corpus
go test ./internal/airepair -run TestAnalyzeCorpus -v   # AI syntax-adaptation corpus
go test -fuzz=FuzzLex   -fuzztime=30s ./internal/lexer/
go test -fuzz=FuzzParse -fuzztime=30s ./internal/parser/
```

Fast local loops are captured in the `justfile`:

```sh
just front                 # uncached front-end packages, usually a few seconds
just short                 # skips generated-Go/runtime-heavy integration paths
just gen TestQuestionOp    # one gen test or regex
just lsp TestCompletion    # one LSP test or regex
just pipe examples/calc    # front-end timing for an Osty package
just pipe-gen path/file.osty
```

The **spec corpus** lives under `testdata/spec/`:

- `positive/NN-<chapter>.osty` — one fixture per spec chapter;
  `TestSpecPositiveCorpus` asserts the full pipeline emits **zero**
  diagnostics for each.
- `negative/reject.osty` — a bundled file of `// === CASE: Exxxx ===`
  blocks; `TestSpecNegativeCorpus` extracts each block and asserts the
  declared diagnostic code fires.

The **airepair corpus** lives under `internal/airepair/testdata/corpus/`:

- `*.input.osty` / `*.expected.osty` fixture pairs capture common
  foreign-language habits from AI-authored Osty.
- `TestAnalyzeCorpus` checks exact repaired output plus before/after
  diagnostic counts so new repair phases can grow without silently
  regressing older adaptation paths.
- `osty airepair promote ...` copies captured `.input.osty` /
  `.expected.osty` pairs into this directory so residual cases can move
  from ad-hoc capture to checked-in regression coverage quickly.

### Golden snapshot updates

Diagnostic-rendering tests compare output to files under
`internal/diag/testdata/golden/`. When you intentionally change the
format:

```sh
go test ./internal/diag/ -run TestGolden -update
git diff internal/diag/testdata/golden/
```

## Architecture notes

See [`ARCHITECTURE.md`](./ARCHITECTURE.md) for the pass-level design,
error-recovery strategy, and how diagnostics flow through the pipeline.
The diagnostic catalogue is in [`ERROR_CODES.md`](./ERROR_CODES.md) —
generated from the doc comments on the `CodeXxx` constants in
`internal/diag/codes.go`. Regenerate with `go generate
./internal/diag/...`. CI runs `go run ./cmd/codesdoc -in
internal/diag/codes.go -check ERROR_CODES.md` to catch missed
regenerations.

## Contributing

Spec work in this repo is tracked under **v0.5**
(`LANG_SPEC_v0.5/`, `OSTY_GRAMMAR_v0.5.md`), but the shipped project
edition is still **0.4** today: `osty new` / `osty init` write
`edition = "0.4"`, and manifest validation still accepts the
historical `0.3` / `0.4` range used by checked-in examples. Future
surface changes follow the normal versioning process and land in a new
spec directory. See [`SPEC_GAPS.md`](./SPEC_GAPS.md) for the full
decision log and [`CHANGELOG_v0.5.md`](./CHANGELOG_v0.5.md) for which
parts of the v0.5 surface are wired in the CLI today versus still
waiting on regen. When docs discuss newer surface area, be explicit
about whether it is a design target or behavior already wired in the
CLI and tests.

Conventions:
- Every new error site gets a stable `Exxxx` code in
  `internal/diag/codes.go` and a focused test that asserts the code.
- Fuzz regressions must be added to the corresponding corpus.
- No `panic` outside of programmer-error paths (nil map, impossible
  enum case) — lexer and parser recover gracefully.
