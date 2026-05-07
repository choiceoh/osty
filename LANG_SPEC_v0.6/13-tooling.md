## 13. Tooling

### 13.1 CLI

```
osty new <name>           Scaffold a new project directory
                          (--lib | --bin | --workspace)
osty init                 Scaffold into the current directory in place
                          (--lib | --bin | --workspace)
osty build [PATH]         Manifest + deps + front-end + backend emit/build
osty run [-- ARGS...]     Build and execute the root binary on the host
osty test [PATH|FILTER...] Discover, build, and run *_test.osty tests
osty fmt FILE             Format source files (--check, --write)
osty airepair FILE        Repair common syntax/idiom slips before parsing
                          (legacy alias: osty repair)
osty check FILE|DIR       Type-check without building
osty lint FILE|DIR        Style + correctness lint (--strict for CI)
osty gen FILE             Emit a single file through the native backend
osty doc PATH             Generate API docs (markdown or html)
osty ci [PATH]            Run format/lint/policy/lockfile/API checks
osty ci snapshot [PATH]   Capture the exported API baseline
osty add <pkg>            Add a dependency to osty.toml + osty.lock
osty update [NAMES...]    Refresh dependencies (lockfile re-resolve)
osty remove <name>...     Remove dependencies from osty.toml (alias: rm)
osty fetch                Resolve and vendor without building
osty publish              Pack and upload the package to a registry
osty search QUERY         Search a registry
osty info PKG             Show registry metadata
osty yank / unyank        Mark or clear a yanked package version
osty login / logout       Manage registry credentials
osty registry serve       Run a file-backed package registry
osty profiles             List build profiles
osty targets              List cross-compilation targets
osty features             List declared feature flags
osty cache [ls|clean|info] Inspect or prune backend build caches
osty pipeline FILE|DIR    Run each front-end phase; --gen emits artifacts
osty lsp                  Run the language server on stdio
osty explain [CODE]       Explain compiler or lint diagnostics
```

`build`, `run`, and `test` accept profile/target/feature flags:
`--profile`, `--release`, `--target`, `--features`, and
`--no-default-features`. `build`, `run`, `test`, `add`, `update`, and
`fetch` accept dependency-resolution guards: `--offline`, `--locked`,
and `--frozen`. Public backend-producing commands accept `--backend llvm`
and `--emit llvm-ir|object|binary`. Historical Go-backend/bootstrap
switches are not part of the public CLI contract. `run` requires a host
binary and rejects cross-target execution. Native `osty test`
execution is still pending.

Front-end-bearing commands (`check`, `resolve`, `typecheck`, `lint`,
`build`, `run`, `test`, and `gen`) apply the in-memory `airepair`
adaptation pass by default before parsing so AI-authored foreign syntax
is normalized without requiring an explicit opt-in flag. Debugging flags
may narrow or disable that behavior, but the default user contract is
best-effort automatic adaptation.

### 13.2 Manifest

A project is described by `osty.toml` at its root. `osty.lock` records
exact resolved versions and content hashes.

**Schema (v0.4).** The following TOML shape is accepted by the v0.4
toolchain. Stable configuration tables such as dependencies, lint,
profiles, and targets reject unknown keys (`E2012`) so typos surface
early; unknown top-level tables and extra `[package]` metadata are
tolerated for forward-compatibility with future additions.

```toml
[package]                        # required unless [workspace] is present
name        = "myapp"            # required; [A-Za-z_][A-Za-z0-9_-]*
version     = "0.1.0"            # required; strict semver X.Y.Z[-pre][+build]
edition     = "0.5"              # required; must be a known spec version
description = "A short blurb."   # optional
authors     = ["Alice <a@x>"]    # optional
license     = "MIT"              # optional (SPDX identifier recommended)
repository  = "https://..."      # optional
homepage    = "https://..."      # optional
keywords    = ["cli", "demo"]    # optional

[dependencies]                   # optional
simple    = "1.0"                                # registry, version req
local-dep = { path = "../local-dep" }            # filesystem
git-dep   = { git = "https://github.com/x/y", tag = "v1" }
# { git = "...", branch = "main" } and { git = "...", rev = "sha" }
# are also accepted; at most one of tag/branch/rev may be set.
renamed   = { version = "1", package = "upstream-name" }
full      = { version = "^1.2", registry = "custom",
              optional = true, features = ["json"],
              default-features = false }

[dev-dependencies]               # optional; only visible to `osty test`
test-helper = "0.4"

[bin]                            # optional; override the default entry
name = "mytool"
path = "src/tool.osty"

[lib]                            # optional; override the default lib root
path = "src/lib.osty"

[registries.custom]              # optional; non-default registries
url = "https://custom.example"
token = "..."                    # optional; credentials may also live in ~/.osty

[workspace]                      # optional; defines a virtual workspace
members = ["packages/core", "packages/util"]

[lint]                           # optional; project-wide lint policy
allow = ["L0004"]                # suppress these codes for the whole project
deny  = ["L0003"]                # elevate these codes to error (CI-failing)
exclude = ["vendor/**"]          # skip matching files during `osty lint`

[profile.release]                # optional; override a built-in profile
opt-level = 3                    # valid range: 0..3
debug = false
strip = true
overflow-checks = false
inlining = true
lto = true
go-flags = ["-trimpath"]
env = { CGO_ENABLED = "0" }

[profile.bench]                  # optional; define a custom profile
inherits = "release"             # built-ins: debug, release, profile, test
debug = true

[target.amd64-linux]             # optional; <arch>-<os> target triple
cgo = false
env = { GOAMD64 = "v3" }

[features]                       # optional; local feature closure
default = ["cli"]
cli = []
tls = ["crypto"]
full = ["cli", "tls", "dep/feature"]
```

**Coexistence of `[package]` and `[workspace]`.** A manifest may
define both: the root directory is itself a package *and* the
workspace root. A manifest that defines only `[workspace]` is a
**virtual workspace** — the root has no package identity; its only
purpose is to group members.

**Version requirements.** Registry dependencies accept the usual
operator forms: `"1.0.0"` (exact), `"^1.0"` (compatible-update),
`">=1.0, <2"` (range). `"*"` means "latest available." Path and git
dependencies do not take a version requirement — their source pins
the version directly.

**Dependency identity.** A dependency's left-hand-side name is the
local alias used in `use <name>` statements. When the remote package
has a different canonical name, set `package = "<remote>"` to
record it; the resolver uses the canonical name for version
matching and stores the remote-to-local mapping in `osty.lock`.

**Diagnostic codes.** Manifest errors emit `E2000–E2099` codes
(listed in `ERROR_CODES.md`), rendered through the shared formatter
with caret underlines and the same source-snippet treatment as
compile-time errors.

**`[lint]` entries.** `allow` and `deny` accept the lint codes
listed under `L0xxx` in `ERROR_CODES.md`, rule-family aliases (e.g.
`unused`, `naming`), and the wildcards `lint` / `all`. Each project
applies its policy after running the default lint pass — `allow`
drops matching diagnostics entirely, `deny` converts matching
warnings to errors. `osty lint --strict` still elevates everything
to error on top of these per-project settings.

**Profiles and targets.** Four profiles are built in: `debug`,
`release`, `profile`, and `test`. A manifest may override any of
them or define a new profile that inherits from a built-in or another
manifest profile. `--profile NAME` selects one explicitly;
`--release` is shorthand for `--profile release`. Targets are named
by `<arch>-<os>` triples such as `amd64-linux` or `arm64-darwin`.
When selected with `--target`, target values populate the backend
environment (`GOARCH`, `GOOS`, optional `CGO_ENABLED`, and any
target-specific `env` entries). `osty run` refuses non-host targets
because it must execute the produced binary locally.

**Features.** `[features]` declares local feature names and their
transitive closure. `default` names features enabled unless
`--no-default-features` is present; `--features a,b` unions additional
feature names. Cross-dependency references use `dep/feature` and are
preserved for the dependency resolver. A source file may require
features with a top-of-file pragma:

```osty
// @feature: tls
```

Files whose required features are inactive are skipped by the build
driver before backend emission.

**Artifacts and cache.** Backend outputs live under
`.osty/out/<profile>[-<target>]/{go,llvm}/`; fingerprints live under
`.osty/cache/<profile>[-<target>]/{go,llvm}.json`. `osty cache ls`
lists those records, `osty cache info` prints one fingerprint, and
`osty cache clean` removes `.osty/cache/` plus `.osty/out/`.

### 13.3 Formatter

`osty fmt` is the canonical formatter. It accepts no configuration.
Among other normalizations:
- Rewrites `Option<T>` to `T?`
- Enforces naming conventions (§1.4)
- Normalizes trailing commas

---

### 13.4 `osty context` (G47)

`osty context <symbol>` extracts a *machine-readable intent dossier*
for a function, method, struct, enum, interface, or module. The
output combines `#[purpose]` (§3.12), `#[example]` (§3.12),
`#[spec]` (§3.10), `#[error_contract]` (§7.5), `#[stability]` /
`#[since]` (§3.14), capability requirements (§20), and `#[budget]`
(§3.15) into a single document. It is the canonical surface for AI
agents, IDE hover, code-search tools, and external static analysis.

```sh
osty context <symbol>                          # default text
osty context <symbol> --format=json            # machine-readable JSON
osty context <symbol> --recursive              # include depth-1 callees
osty context <symbol> --recursive=N            # depth=N
osty context current://path:line:col           # LSP cursor form
osty context --search=<query>                  # symbol search
osty context --all-stdlib --format=jsonl       # bulk export
```

The JSON schema is pinned at `https://osty.dev/schemas/context/v1.json`
and includes:

| Field | Source |
|---|---|
| `signature` | declared parameters / return type / generics |
| `purpose` | `#[purpose]` |
| `examples` | `#[example]` (auto-checked) |
| `spec_refs` | `#[spec]` (anchor + section title) |
| `error_contract` | `#[error_contract]` |
| `effects.capabilities_required` | capability parameters in signature |
| `effects.reproducible` | `#[reproducible(scope=...)]` |
| `effects.taint_sources / sanitizes / sinks` | `#[taint]` / `#[sanitizes]` / `#[requires]` |
| `budget.static / runtime` | `#[budget(...)]` |
| `stability` / `since` | `#[stability]` / `#[since]` |
| `fixtures_referenced` | `#[example(uses = "name")]` |
| `callees` | `--recursive` only |

The full schema is `LANG_SPEC_v0.6/00-revision.md §13.6.2`.

**LSP integration.** `textDocument/hover` returns the same data
rendered as markdown. AI agents query the JSON directly via `osty
context --format=json` or the LSP custom request `osty/context`.

### 13.5 `osty publish` (G44)

`osty publish` packages a workspace into a registry-publishable
artifact and validates SemVer compatibility against the previous
published manifest. It is the gate that enforces `#[stability]`
(§3.14) — *stable API breaking changes require a major version bump*.

**Workflow.**

1. Read the previous published manifest from `OSTY_SELF_REGISTRY_URL`
   (or git tag if local).
2. Compute the *API surface diff* between previous and current
   workspace per the algorithm in `00-revision.md §3.14.3`.
3. Classify each change as `Breaking` / `CompatAdd` / `Patch`.
4. Compare to the version bump:
   - Breaking + non-major bump → `E2100`.
   - CompatAdd + patch-only → `E2102`.
   - Version downgrade → `E2101`.
   - Experimental change → `W2100`.
5. On success, sign the manifest (ed25519, §A6 of selfhost) and
   upload.

**Surface definition.** API surface includes: function signatures,
struct fields and methods, enum variants and their payloads, interface
methods, type alias RHS, public constants, and the following
annotations: `#[stability]`, `#[error_contract]`, `#[since]`,
`#[reproducible]`, `#[pure]`, `#[budget]`, parameter taint annotations.
*Excluded* from surface: function bodies, `#[purpose]` / `#[example]` /
`#[fixture]` / `#[spec]`, `#[golden]`, line numbers, comments.

**Manifest format.** `target/manifest-{version}.json` (schema:
`https://osty.dev/schemas/manifest/v1.json`) — `00-revision.md
§3.14.3.4`.

### 13.6 `osty validate-spec` (G38)

`osty validate-spec` walks the workspace looking for `#[spec("§X.Y")]`
annotations and verifies each anchor exists in
`LANG_SPEC_v0.6/`. Missing anchors become `E0790`; anchors that have
moved between sections produce `W0790` with a suggested replacement.

```sh
osty validate-spec                          # workspace
osty validate-spec ./toolchain              # specific dir
osty validate-spec --strict                 # treat W0790 as error
osty validate-spec --update                 # apply suggested rewrites
```

The command is run automatically by `osty check --strict` and by
the `just prepush` recipe.

### 13.7 Audit subcommands

Several v0.6 annotations grant *audit-marked escapes* — places where
the language allows a normally-restricted operation under explicit
audit. Each escape is enumerable:

| Subcommand | Sites | Annotation / spec |
|---|---|---|
| `osty audit --trusted-declassify` | FFI / Go-side data drop | `#[trusted_declassify(reason)]` (§21.7) |
| `osty audit --trusted-construct` | sealed-struct bypass | `#[trusted_construct(reason)]` (§3.4.5.6) |
| `osty audit --match-compat` | enum-shape pin | `#[match_compat(... unsafe_silent = true)]` (§3.14.4) |
| `osty audit --legacy-globals` | v0.5 global effect calls | `--legacy-globals` flag (§7.1.1) |
| `osty audit --all` | all of the above |  |

Each subcommand prints `<file>:<line>:<col> <reason>` for every site,
suitable for security review and migration tracking.

### 13.8 Test subcommand extensions

`osty test` gains v0.6-aware modes:

| Mode | Reads | Discovers |
|---|---|---|
| `osty test --spec` | `spec { example: }` clauses (§3.13) | per-clause boolean test |
| `osty test --example` | `#[example(input=, output=, uses=)]` (§3.12) | input → output match |
| `osty test --golden` | `#[golden(path, mode)]` (§11.5.2) | snapshot compare |
| `osty test --update-golden` | (same) | overwrites snapshot |
| `osty test --doc` | `///` doc-test blocks (v0.5) | runs as test |

`osty bench --budget` (§3.15.2) gates runtime budget regressions.
