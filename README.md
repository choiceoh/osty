# Osty

A work-in-progress implementation of the **Osty** programming language — a
general-purpose, statically-typed, GC'd language specified in
[`LANG_SPEC_v0.3/`](./LANG_SPEC_v0.3/README.md) with grammar fixed in
[`OSTY_GRAMMAR_v0.3.md`](./OSTY_GRAMMAR_v0.3.md).

The target is transpilation to Go. Current scope: front-end
(lex → parse → resolve → type-check), multi-file packages and
workspaces, formatter, linter, a JSON-RPC LSP server, a Go
transpiler with **Phases 1–6 wired end-to-end** (primitives,
control flow, structs/enums/match, generics/closures,
Option/Result/`?`, `use`/FFI, channels/concurrency), project
scaffolding (`osty new` / `osty init`), a manifest-driven build
orchestrator (`osty build`) that reads `osty.toml` / `osty.lock`
and threads the front-end + gen across the declared packages, a
working test runner (`osty test`), API documentation generation
(`osty doc`), and CI quality tooling (`osty ci`). The remaining
pieces are four deferred type-checker hooks (generic
monomorphization, interface-satisfaction, match exhaustiveness,
`#[derive(Builder)]`), Tier 2+ of the standard library, and the
real package-registry backend behind `osty add` / `osty update` /
`osty publish`.
transpiler whose Phase 1 (primitives, fns, control flow) is
working, project scaffolding (`osty new` / `osty init`), a
manifest-driven build orchestrator (`osty build`) that reads
`osty.toml` / `osty.lock` and threads the front-end + gen across
the declared packages, a package manager (`osty add` / `osty
update` / `osty publish`) that drives registry + git + path
sources through a SemVer resolver into a deterministic lockfile,
and a build-and-execute shortcut (`osty run`). Phase 2+ of the
transpiler (structs, enums, match, generics, Option / Result,
FFI, concurrency) is the main remaining piece.

## Status

| Phase | Status |
|---|---|
| Lexer (UTF-8, ASI, triple-quoted strings, interpolation) | done |
| Parser (v0.3 grammar, error recovery, fuzz-clean) | done |
| AST (all node kinds implement `ast.Node`) | done |
| Diagnostics (`error[E0002]:` with caret, hints, notes) | done |
| Name resolution (single + multi-file, workspace, typo suggestions) | done |
| Formatter (`internal/format`) | done |
| Type checker (`internal/check`) | partial (see gaps below) |
| Linter (`internal/lint`, L0001–L0042, `--fix` / `--fix-dry-run`) | done |
| Multi-file packages (`resolve` loader/package/workspace) | done |
| LSP (`internal/lsp`, wired as `osty lsp`) | done — hover, definition, formatting, documentSymbol, lint diagnostics |
| Go transpiler (`internal/gen`, wired as `osty gen`) | Phases 1–6 wired end-to-end (primitives, structs/enums/match, generics/closures, Option/Result/`?`, `use`/FFI, channels/concurrency) |
| Independent IR (`internal/ir`) | done — patterns, match, closures, struct/field/method |
| Project scaffolding (`internal/scaffold`, `osty new` / `osty init`) | done — `--bin`, `--lib`, `--workspace`, `--cli`, `--service` |
| Manifest + lockfile + SemVer (`internal/manifest`, `lockfile`, `pkgmgr/semver`) | done (parse + validate + resolve) |
| Build orchestrator (`osty build`) | done — manifest → front-end → gen, profile/target/feature wiring |
| Test runner harness (`internal/testgen`) | done — merges per-file gen output, injects a real std.testing runtime + main(), runs via `go run` |
| `osty test` (discovery + front-end + execution) | done — validates and **runs** discovered `test*` / `bench*` fns; failures and pass/fail totals report inline |
| API doc generator (`internal/docgen`, `osty doc`) | done — HTML + markdown, field docs, cross-refs, workspace mode |
| CI quality tooling (`internal/ci`, `osty ci`) | done — signature-aware snapshots, workspace coverage, JSON reports |
| Pipeline visualizer (`osty pipeline`) | done — per-stage timing, workspace mode, package-mode gen, baseline diff, LSP trace, `--explain` |
| Package registry / `osty add` / `osty update` / `osty run` / `osty publish` | scaffolded — manifest wiring ready; registry backend not yet implemented |
| Build orchestrator (`osty build`) | wired — drives manifest → front-end → gen Phase 1 |
| Test runner harness (`internal/testgen`) | wired — merges per-file gen output, injects a real std.testing runtime + main(), runs via `go run` |
| `osty test` (discovery + front-end + execution) | wired — validates and **runs** discovered `test*` / `bench*` fns; failures and pass/fail totals report inline |
| Package registry / `osty add` / `osty update` / `osty run` | done (resolve + vendor + lockfile-honoring re-resolves, ETag-cached registry index, copy fallback for symlink-less filesystems; CLI: `add`, `remove`/`rm`, `update`, `run`, `fetch`, `publish`, `search`, `info`, `yank`/`unyank`, `login`/`logout`; `--locked` / `--frozen` CI guards) |
| Package manager (`osty add` / `osty update`, path + git + registry sources, SemVer resolver, deterministic lockfile) | wired — `add` mutates `osty.toml` and re-vendors; `update` re-resolves selectively or in full |
| `osty run` (build + exec through gen Phase 1) | wired — resolves manifest, vendors deps, transpiles entry, `go run`s the output with profile/target-aware flags |
| `osty publish` (pack + upload tarball to a registry) | wired — deterministic gzipped tar, sha256 checksum, bearer-auth POST; `--dry-run` stops before upload |

The front-end (lex → parse → resolve) is **spec-complete for v0.3**:
every syntactic construct in `LANG_SPEC_v0.3/` has a positive-corpus
fixture that parses cleanly, and every reject-rule has a negative
fixture that triggers the expected `Exxxx` diagnostic. See
[`ERROR_CODES.md`](./ERROR_CODES.md) for the full code index.

### Type-checker gaps

The checker covers the common path (literals, operators, collections,
functions, patterns, control flow, field access, method dispatch,
Option/Result, closures, type aliases) but has four consciously-deferred
hooks documented at `internal/check/env.go`:

- **Generic monomorphization** — generic fns aren't specialized per call site.
- **Interface-satisfaction validation** — structs aren't checked to actually
  implement their declared interfaces.
- **Match exhaustiveness (`E0731`)** — code reserved in `diag/codes.go`
  but not yet emitted.
- **Builder auto-derivation** (§3.4) — `#[derive(Builder)]` not supported.

Minor deferred items: method references as values (`obj.method` without
a call) at `internal/check/expr.go:1106`, and cross-file partial-method
name-collision detection (test skipped at
`internal/resolve/package_test.go:243`).

### Transpiler phases

`internal/gen` is wired up as the `osty gen FILE` subcommand.
Phases 1–6 are all implemented end-to-end (see commit
`4829685` "gen/check: finish phases 4–6 for end-to-end CLI
usability"). Remaining unimplemented corners of each phase still
emit a `/* TODO(phaseN): ... */` marker so the produced Go file
type-checks where possible. Phase scope, per `internal/gen/doc.go`:

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
├── LANG_SPEC_v0.3/          # Language spec (prose + examples, per-section)
├── OSTY_GRAMMAR_v0.3.md     # EBNF grammar + decision log
├── SPEC_GAPS.md             # Resolved-gap archive (no open items in v0.3)
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
│   ├── gen/                 # Go transpiler (Phases 1–6; `osty gen FILE`)
│   ├── testgen/             # Test runner harness (drives `osty test`)
│   ├── docgen/              # API doc generator (HTML + markdown; `osty doc`)
│   ├── ci/                  # CI quality tooling (`osty ci`)
│   ├── profile/             # Build profiles / targets / features
│   ├── lsp/                 # Language server (stdio JSON-RPC)
│   ├── scaffold/            # `osty new` / `osty init` project templates
│   ├── tomlparse/           # Generic TOML parser (subset)
│   ├── manifest/            # osty.toml parse + validate + lookup
│   ├── lockfile/            # osty.lock read/write
│   ├── registry/            # Package registry client (stub)
│   └── pkgmgr/semver/       # SemVer parse, compare, constraint match
└── testdata/                # .osty fixtures used by tests
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
osty build [DIR]       # manifest-driven: manifest → deps → front-end
osty add PKG           # append a dependency to osty.toml and re-resolve
osty remove NAME...    # drop dependencies from osty.toml and re-resolve (alias: rm)
osty update [NAMES...] # refresh the lockfile (selective or full)
osty run [-- ARGS...]  # build and exec the binary (gen Phase 1)
osty test [PATH|FILTERS...] # discover & validate *_test.osty; list test + bench fns
osty publish           # pack the project and upload to a registry
osty search QUERY      # full-text search the registry (--registry, --limit)
osty info PKG          # show registry metadata for a package (--all-versions)
osty fetch             # resolve + vendor without building (--locked, --frozen)
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
osty fmt FILE          # format to canonical style (see --check, --write)
osty gen FILE          # transpile to Go source (see -o, --package)
osty doc PATH          # generate API documentation (HTML + markdown)
osty ci                # run CI quality checks (signatures, coverage, snapshots)
osty scaffold          # generators (fixture / schema / ffi)
osty lsp               # run the language server on stdio
osty explain [CODE]    # describe a diagnostic (Exxxx/Wxxxx/Lxxxx); no arg lists every code
osty pipeline FILE|DIR # run every front-end phase; per-stage timing
                       # (--json, --trace, --per-decl, --gen, --cpuprofile,
                       #  --memprofile, --baseline)
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

- `--check` — exit 1 if the file is not already formatted; show diff
- `--write` — rewrite the file in place instead of printing

`gen`-specific flags (after the subcommand):

- `-o PATH` / `--out PATH` — write Go source to `PATH` instead of stdout
- `--package NAME` — Go package clause for the emitted file (default: `main`)

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

`osty build` loads `osty.toml` starting at the given path (or the cwd),
resolves dependencies against `osty.lock` (regenerated if stale),
vendors deps into `<project>/.osty/deps/`, and runs the front-end
(parse → resolve → check → lint) plus gen Phase 1 across every package
the manifest names. Future iterations will invoke `go build` on the
emitted source; today step emits the Go into `<project>/.osty/out/`
and reports diagnostics.

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

The current baseline is **v0.3** (`LANG_SPEC_v0.3/`, `OSTY_GRAMMAR_v0.3.md`).
v0.3 closed every previously-open gap; new findings are tracked in
[`SPEC_GAPS.md`](./SPEC_GAPS.md). The compiler follows spec decisions
literally — if a construct "should" work but doesn't, verify it against
the grammar first.

Conventions:
- Every new error site gets a stable `Exxxx` code in
  `internal/diag/codes.go` and a focused test that asserts the code.
- Fuzz regressions must be added to the corresponding corpus.
- No `panic` outside of programmer-error paths (nil map, impossible
  enum case) — lexer and parser recover gracefully.
