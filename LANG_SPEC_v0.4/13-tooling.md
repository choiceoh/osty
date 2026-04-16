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
edition     = "0.4"              # required; must be a known spec version
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
