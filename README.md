# Osty

A work-in-progress implementation of the **Osty** programming language — a
general-purpose, statically-typed, GC'd language specified in
[`LANG_SPEC_v0.3/`](./LANG_SPEC_v0.3/README.md) with grammar fixed in
[`OSTY_GRAMMAR_v0.3.md`](./OSTY_GRAMMAR_v0.3.md).

The target is transpilation to Go. Current scope: front-end
(lex → parse → resolve → type-check), multi-file packages and
workspaces, formatter, linter, a JSON-RPC LSP server, a Go
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
| Name resolution (single-file, typo suggestions) | done |
| Formatter (`internal/format`) | done |
| Type checker (`internal/check`) | partial (see gaps below) |
| Linter (`internal/lint`, L0001–L0042) | done |
| Multi-file packages (`resolve` loader/package/workspace) | done |
| LSP (`internal/lsp`, wired as `osty lsp`) | done |
| Go transpiler (`internal/gen`, wired as `osty gen`) | Phase 1 done (primitives, fns, if/for) — Phase 2+ pending |
| Project scaffolding (`internal/scaffold`, `osty new` / `osty init`) | done |
| Manifest + lockfile + SemVer (`internal/manifest`, `lockfile`, `pkgmgr/semver`) | done (parse + validate + resolve) |
| Build orchestrator (`osty build`) | wired — drives manifest → front-end → gen Phase 1 |
| Test runner harness (`internal/testgen`) | wired — merges per-file gen output, injects a real std.testing runtime + main(), runs via `go run` |
| `osty test` (discovery + front-end + execution) | wired — validates and **runs** discovered `test*` / `bench*` fns; failures and pass/fail totals report inline |
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

`internal/gen` is wired up as the `osty gen FILE` subcommand. Phase 1
handles primitive literals/operators, user fn declarations, let bindings,
if / for / return, list literals over primitives, and the
println/print/eprintln/eprint intrinsics. Unimplemented forms emit a
`/* TODO(phaseN): ... */` marker so the produced Go file still
type-checks where possible. The remaining phases are scoped in
`internal/gen/doc.go`:

- **Phase 2**: structs, enums, interfaces, type aliases, match, patterns
- **Phase 3**: generics, closures, collection methods
- **Phase 4**: Option / Result, `?` operator, defer
- **Phase 5**: `use` declarations, Go FFI
- **Phase 6**: channels, concurrency primitives

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
│   ├── gen/                 # Go transpiler (Phase 1; `osty gen FILE`)
│   ├── testgen/             # Test runner harness (drives `osty test`)
│   ├── lsp/                 # Language server (stdio JSON-RPC)
│   ├── scaffold/            # `osty new` / `osty init` project templates
│   ├── tomlparse/           # Generic TOML parser (subset)
│   ├── manifest/            # osty.toml parse + validate + lookup
│   ├── lockfile/            # osty.lock read/write
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
osty update [NAMES...] # refresh the lockfile (selective or full)
osty run [-- ARGS...]  # build and exec the binary (gen Phase 1)
osty test [PATH|FILTERS...] # discover & validate *_test.osty; list test + bench fns
osty publish           # pack the project and upload to a registry
osty tokens FILE       # print the token stream (debugging)
osty parse FILE        # parse to AST, emit JSON
osty resolve FILE|DIR  # name resolution; directory = package mode (--scopes for tree)
osty check FILE|DIR    # lex + parse + resolve + type-check (diagnostics only)
osty typecheck FILE    # same as check, plus a per-expression type dump
osty lint FILE|DIR     # style + correctness warnings (L0xxx codes)
osty fmt FILE          # format to canonical style (see --check, --write)
osty gen FILE          # transpile to Go source (see -o, --package)
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

`build` / `run` / `test` / `add` / `update`-specific flags (after the
subcommand):

- `--offline` — do not fetch dependencies; fail if caches are missing

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
- `--token T` — API token; also read from `$OSTY_PUBLISH_TOKEN` or
  the token recorded under `[registries.<name>]`.
- `--dry-run` — build the tarball into `<project>/.osty/publish/`
  but do not upload.

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
