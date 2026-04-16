# Osty

A work-in-progress implementation of the **Osty** programming language — a
general-purpose, statically-typed, GC'd language specified in
[`LANG_SPEC_v0.4/`](./LANG_SPEC_v0.4/README.md) with grammar fixed in
[`OSTY_GRAMMAR_v0.4.md`](./OSTY_GRAMMAR_v0.4.md).

The default production backend transpiles to Go, and an early LLVM
backend now shares the same artifact/cache layout for native IR,
object, and binary experiments. Current scope: front-end (lex → parse
→ resolve → type-check), multi-file packages and workspaces, formatter,
linter, a JSON-RPC LSP server, a Go transpiler with **Phases 1–6 wired
end-to-end** (primitives, control flow, structs/enums/match,
generics/closures, Option/Result/`?`, `use`/FFI, channels/concurrency),
an LLVM backend slice for scalar/control-flow/Bool/String smoke
programs, project scaffolding (`osty new` / `osty init`), a
manifest-driven build orchestrator (`osty build`) that reads
`osty.toml` / `osty.lock` and threads the front-end + selected backend
across the declared packages, a working test runner (`osty test`), API
documentation generation (`osty doc`), CI quality tooling (`osty ci`),
profile/target/feature/cache inspection commands, and a package manager
(`osty add` / `osty update` / `osty publish`) backed by a file-backed
HTTP registry server for local/private registries. The current language
baseline is v0.4: the grammar is frozen, the remaining semantic corners
are closed, and the next work is implementation/runtime coverage rather
than language-decision churn.

## Status

| Phase | Status |
|---|---|
| Lexer (UTF-8, ASI, triple-quoted strings, interpolation) | done |
| Parser (v0.4 grammar, error recovery, fuzz-clean) | done |
| AST (all node kinds implement `ast.Node`) | done |
| Diagnostics (`error[E0002]:` with caret, hints, notes) | done |
| Name resolution (single + multi-file, workspace, typo suggestions) | done |
| Formatter (`internal/format`) | done |
| Type checker (`internal/check`) | done for v0.4 front-end core — generic instantiation, structural interface checks, exhaustiveness, builder protocol, function-value arity, closure pattern params |
| Linter (`internal/lint`, L0001–L0042, `--fix` / `--fix-dry-run`) | done |
| Multi-file packages (`resolve` loader/package/workspace) | done |
| LSP (`internal/lsp`, wired as `osty lsp`) | done — hover, definition, formatting, documentSymbol, lint diagnostics |
| Go transpiler (`internal/gen`, wired as `osty gen`) | Phases 1–6 wired end-to-end (primitives, structs/enums/match, generics/closures, Option/Result/`?`, `use`/FFI, channels/concurrency) |
| Independent IR (`internal/ir`) | done — patterns, match, closures, struct/field/method |
| Project scaffolding (`internal/scaffold`, `osty new` / `osty init`) | done — `--bin`, `--lib`, `--workspace`, `--cli`, `--service` |
| Manifest + lockfile + SemVer (`internal/manifest`, `lockfile`, `pkgmgr/semver`) | done (parse + validate + resolve) |
| Build orchestrator (`osty build`) | done — manifest → front-end → backend emit/build, profile/target/feature wiring, backend-aware artifact/cache paths |
| Test runner harness (`internal/testgen`) | done — merges per-file gen output, injects a real std.testing runtime + main(), runs via `go run` |
| `osty test` (discovery + front-end + execution) | done — validates and **runs** discovered `test*` / `bench*` fns; failures and pass/fail totals report inline |
| API doc generator (`internal/docgen`, `osty doc`) | done — self-hosted Osty source generates the Go package; HTML + markdown, field docs, cross-refs, workspace mode |
| CI quality tooling (`internal/ci`, `osty ci`) | done — Osty-authored generated CI core, signature-aware snapshots, workspace coverage, JSON reports |
| Pipeline visualizer (`osty pipeline`) | done — per-stage timing, workspace mode, backend-aware gen, baseline diff, LSP trace, `--explain` |
| Profiles / targets / features / cache (`internal/profile`, `osty profiles` / `targets` / `features` / `cache`) | done — built-in and manifest profiles, cross-target env, feature closure + file pragmas, backend-aware fingerprints |
| LLVM backend (`internal/backend`, `internal/llvmgen`, `--backend llvm`) | early executable slice — textual IR/object/binary for scalar/control-flow/Bool/String and `Float` (double subset) smoke programs, plus simple struct aggregates and enum matches (payload-free + single-`Int` payload), host `clang` driver, inspectable skeleton + categorized diagnostics for unsupported source shapes |
| Package registry backend / `osty registry serve` | done — file-backed HTTP server for index/search/download/publish/yank, with ETag index responses and bearer-token write auth |
| Package registry / `osty add` / `osty update` / `osty run` | done (resolve + vendor + lockfile-honoring re-resolves, ETag-cached registry index, copy fallback for symlink-less filesystems; CLI: `add`, `remove`/`rm`, `update`, `run`, `fetch`, `publish`, `search`, `info`, `yank`/`unyank`, `login`/`logout`; `--locked` / `--frozen` CI guards) |
| Package manager (`osty add` / `osty update`, path + git + registry sources, SemVer resolver, deterministic lockfile) | wired — `add` mutates `osty.toml` and re-vendors; `update` re-resolves selectively or in full |
| `osty run` (build + exec through selected backend) | wired — resolves manifest, vendors deps, emits the entry artifact, executes host Go/LLVM binaries with profile/feature flags, and rejects cross-target execution |
| `osty publish` (pack + upload tarball to a registry) | wired — deterministic gzipped tar, sha256 checksum, bearer-auth POST; `--dry-run` stops before upload |

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

### Transpiler phases

`internal/gen` is wired up as the `osty gen FILE` subcommand.
Phases 1–6 are all implemented end-to-end (see commit
`4829685` "gen/check: finish phases 4–6 for end-to-end CLI
usability"). Phase scope, per `internal/gen/doc.go`:

- **Phase 1** ✓ primitive literals/operators, user fn declarations,
  let bindings, if / for / return, list literals, print intrinsics
- **Phase 2** ✓ structs, enums, interfaces, type aliases, match, patterns
- **Phase 3** ✓ generics, closures, collection methods
- **Phase 4** ✓ Option / Result, `?` operator, defer
- **Phase 5** ✓ `use` declarations, Go FFI
- **Phase 6** ✓ channels, concurrency primitives

## Layout

```
osty/
├── LANG_SPEC_v0.4/          # Current language spec (prose + examples)
├── OSTY_GRAMMAR_v0.4.md     # Current EBNF grammar + decision log
├── SPEC_GAPS.md             # Resolved-gap archive (no open items in v0.4)
├── LLVM_MIGRATION_PLAN.md   # Planned migration from Go gen to LLVM backend
├── LLVM_PHASE1_BASELINE.md  # Current Go-backend baseline for LLVM migration
├── LLVM_BACKEND_CORPUS.md   # Backend parity fixture classes and smoke set
├── LLVM_GEN_TODO_AUDIT.md   # Go-gen TODO audit and LLVM initial exclusions
├── LLVM_ARTIFACT_LAYOUT.md  # Backend-aware output/cache layout policy
├── cmd/
│   ├── osty/                # Main CLI (`osty` binary)
│   └── codesdoc/            # Regenerates ERROR_CODES.md from codes.go
├── internal/
│   ├── token/               # Token kinds + positions
│   ├── lexer/               # Source bytes → []Token
│   ├── ast/                 # AST node types
│   ├── parser/              # []Token → *ast.File
│   ├── diag/                # Diagnostics + Rust-style renderer
│   ├── resolve/             # Name resolution (single + multi-file)
│   ├── stdlib/              # Built-in prelude symbols + `modules/*.osty`
│   ├── types/               # Semantic types (shared by checker + LSP)
│   ├── check/               # Type checker
│   ├── lint/                # Style/correctness lint rules (L0xxx codes)
│   ├── format/              # Canonical-style formatter
│   ├── ir/                  # Independent intermediate representation
│   ├── backend/             # Backend names, emit modes, artifact layout, host shims
│   ├── llvmgen/             # LLVM bridge generated from Osty selfhost-core backend logic
│   ├── gen/                 # Go transpiler (Phases 1–6; `osty gen FILE`)
│   ├── testgen/             # Test runner harness (drives `osty test`)
│   ├── docgen/              # self-hosted API doc generator (HTML + markdown; `osty doc`)
│   ├── ci/                  # CI quality tooling (`osty ci`, generated core)
│   ├── cihost/              # Go host bridge for generated CI core
│   ├── profile/             # Build profiles / targets / features
│   ├── lsp/                 # Language server (stdio JSON-RPC)
│   ├── scaffold/            # `osty new` / `osty init` project templates
│   ├── tomlparse/           # Generic TOML parser (subset)
│   ├── manifest/            # osty.toml parse + validate + lookup
│   ├── lockfile/            # osty.lock read/write
│   ├── registry/            # Package registry client + file-backed HTTP server
│   └── pkgmgr/semver/       # SemVer parse, compare, constraint match
├── examples/
│   └── selfhost-core/       # Osty-authored compiler/self-hosting cores, CI policy core, LLVM emitter prototype
└── testdata/                # .osty fixtures used by tests and backend corpus
```

## Building

Requires Go 1.22+.

```sh
go build -o osty ./cmd/osty
```

## CLI

```sh
osty new NAME          # scaffold a new project directory (--lib, --workspace)
osty init              # scaffold into the current directory (same flags as new)
osty build [DIR]       # manifest-driven: manifest → deps → front-end → backend
osty add PKG           # append a dependency to osty.toml and re-resolve
osty remove NAME...    # drop dependencies from osty.toml and re-resolve (alias: rm)
osty update [NAMES...] # refresh the lockfile (selective or full)
osty run [-- ARGS...]  # build and exec the binary through the selected backend
osty test [PATH|FILTERS...] # discover, build, and run *_test.osty tests/benches
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
osty typecheck FILE    # same as check, plus a per-expression type dump
osty lint FILE|DIR     # style + correctness warnings (L0xxx codes)
osty fmt FILE          # repair + format to canonical style (see --check, --write, --engine)
osty repair FILE       # auto-fix common AI-authored syntax/idiom slips
osty gen FILE          # emit one file through a backend (see -o, --package)
osty doc PATH          # generate API documentation (HTML + markdown)
osty ci                # run CI quality checks (signatures, coverage, snapshots)
osty ci snapshot       # capture the exported API baseline
osty profiles          # list build profiles (debug, release, profile, test, ...)
osty targets           # list declared cross-compilation targets
osty features          # list declared opt-in features
osty cache [ls|clean|info] # inspect or prune backend build caches
osty scaffold          # generators (fixture / schema / ffi)
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

`fmt`-specific flags (after the subcommand):

`osty fmt` runs the same automatic source repair pass before formatting by
default, so AI-authored syntax slips are normalized in one command.

- `--check` — exit 1 if the file is not already formatted; show diff
- `--write` — rewrite the file in place instead of printing
- `--no-repair` — disable the default automatic source repair pass
- `--engine go|osty` — choose the formatter engine. `go` is the default
  AST formatter; `osty` is a compatibility entry point that now shares the
  same AST-backed formatting contract instead of maintaining a separate
  token-heuristic printer.

`repair`-specific flags (after the subcommand):

- `--check` — exit 1 if the file contains repairable syntax slips
- `--write` — rewrite the file in place instead of printing

Repairs include common foreign-language carryovers such as `func`/`def`,
`var`/`const`, `while`, `switch`/`case`, `nil`/`null`, Python word
operators, JS `console.log`, semicolons, `=>`, trailing chain dots, and
newline-separated `else`.

`gen`-specific flags (after the subcommand):

- `-o PATH` / `--out PATH` — write the emitted artifact to `PATH` instead of stdout
- `--package NAME` — Go package clause for the emitted file (default: `main`)
- `--backend NAME` — code generation backend (`go` or `llvm`; default: `go`;
  `llvm` emits textual `.ll` for the early scalar/control-flow/plain/escaped
  string subset, including immutable/mutable string locals and simple String
  function boundaries plus simple struct aggregate values and enum
  tags/match expressions (payload-free + single-`Int` with `{ i64, i64 }`
  payload), plus Phase 46-53 `Float` double-subset smoke path. Unsupported shapes
  still prepare skeleton artifacts and report structured diagnostics from the
  selfhost-core backend policy)
- `--emit MODE` — requested text artifact. `go` emits Go source for the Go
  backend; `llvm-ir` is reserved for the LLVM backend.

`pipeline --gen` accepts the same source-artifact backend selection:
`--backend go --emit go` for Go source, or `--backend llvm --emit llvm-ir` for
LLVM IR. Without `--gen`, `--backend` and `--emit` are rejected because the
pipeline is otherwise front-end only.

### Debugging build / run / test failures

`osty gen`, `osty build`, `osty run`, and `osty test` keep generated
artifacts inspectable. Emitted Go includes comments like:

```go
// Osty: /path/to/main.osty:12:5
```

When `go build`, `go run`, the LLVM toolchain, or the test harness fails
after the Osty front-end has succeeded, the CLI prints a short post-mortem:

- the generated Go/LLVM artifact or scratch directory to inspect;
- the nearest Osty source marker for Go compile errors and panic stack traces;
- a category for common Go-side failures such as `package/import`,
  `transpile output`, `generated Go type/check`, or `runtime panic`;
- the exact Go or `clang` command to rerun from the generated output directory.

For package/import errors, start with the reported `use go "..."` path
or project dependency configuration. For runtime panics, read the Go
stack trace above the summary and use the mapped Osty line as the first
source location to inspect.

`new` / `init`-specific flags (after the subcommand):

- `--bin` — scaffold a binary project (default): `main.osty` with `fn main`
- `--lib` — scaffold a library project: `lib.osty` with a `pub fn` starter,
  no binary entry point
- `--workspace` — scaffold a virtual workspace with one default member
  (`init` only — combined with `--member NAME` to pick the member directory)
- `--name NAME` — `init`-only: override the project name (defaults to the
  current directory basename)

The scaffolded layout is three files (`osty.toml`, the source file, and
`.gitignore`) in a new directory named after `NAME`. The manifest pins
the current spec edition; `osty new` never overwrites an existing
directory. `osty init` writes into the current directory instead of
creating a new one.

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

- `--backend NAME` — code generation backend (`go` or `llvm`; default: `go`;
  `llvm` can write textual IR for the early scalar/control-flow/plain/escaped
  string subset, including immutable/mutable string locals and simple String
  function boundaries plus simple struct aggregate values and enum tags/match
  expressions (payload-free + single-`Int` with `{ i64, i64 }` payload), plus
  the Phase 46-53 `Float` double-subset path. `clang`-driven object/binary
  emission and generated-source diagnostics are available for supported programs;
  unsupported shapes still prepare skeleton artifacts and report missing lowering
  through structured diagnostics from the selfhost-core backend policy.
- `--emit MODE` — requested artifact mode (`go`, `llvm-ir`, `object`, or
  `binary`). `build --emit go` writes inspectable Go without linking a binary;
  `build --backend llvm --emit object|binary` uses `clang`; `run` requires
  `binary` because it executes the result. `test` still uses the Go harness;
  `--backend llvm` is reserved for backend-aware test generation.

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
(parse → resolve → check → lint) plus selected backend emission across every
package the manifest names. For binary packages it emits backend artifacts into
`<project>/.osty/out/<profile>[-<target>]/{go,llvm}/`, invokes the selected
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
$ osty add ../my-shared-lib --path ../../shared
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

The current baseline is **v0.4** (`LANG_SPEC_v0.4/`, `OSTY_GRAMMAR_v0.4.md`).
v0.4 closed every known language-decision gap; new findings are tracked in
[`SPEC_GAPS.md`](./SPEC_GAPS.md). The compiler follows spec decisions
literally — if a construct "should" work but doesn't, verify it against
the grammar first.

Conventions:
- Every new error site gets a stable `Exxxx` code in
  `internal/diag/codes.go` and a focused test that asserts the code.
- Fuzz regressions must be added to the corresponding corpus.
- No `panic` outside of programmer-error paths (nil map, impossible
  enum case) — lexer and parser recover gracefully.
