## 13. Tooling

### 13.1 CLI

```
osty new <name>           Scaffold a new project directory
                          (--lib | --bin | --workspace)
osty init                 Scaffold into the current directory in place
                          (--lib | --bin | --workspace)
osty build [PATH]         Manifest-driven front-end + dependency resolution
osty run                  Build and run (or execute a script)
osty test                 Run tests (--bench, --serial, --update-snapshots)
osty fmt FILE             Format source files (--check, --write)
osty check FILE|DIR       Type-check without building
osty lint FILE|DIR        Style + correctness lint (--strict for CI)
osty gen FILE             Transpile a single file to Go (debug aid)
osty add <pkg>            Add a dependency to osty.toml + osty.lock
osty update [NAMES...]    Refresh dependencies (lockfile re-resolve)
osty lsp                  Run the language server on stdio
```

### 13.2 Manifest

A project is described by `osty.toml` at its root. `osty.lock` records
exact resolved versions and content hashes.

**Schema (v0.4).** The following TOML shape is accepted by the v0.4
toolchain. Unknown keys inside known tables are rejected
(`E2012`) so typos surface early; unknown top-level tables are
ignored for forward-compatibility with future additions.

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

[dev-dependencies]               # optional; only visible to `osty test`
test-helper = "0.4"

[bin]                            # optional; override the default entry
name = "mytool"
path = "src/tool.osty"

[lib]                            # optional; override the default lib root
path = "src/lib.osty"

[registries.custom]              # optional; non-default registries
url = "https://custom.example"

[workspace]                      # optional; defines a virtual workspace
members = ["packages/core", "packages/util"]

[lint]                           # optional; project-wide lint policy
allow = ["L0004"]                # suppress these codes for the whole project
deny  = ["L0003"]                # elevate these codes to error (CI-failing)
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

### 13.3 Formatter

`osty fmt` is the canonical formatter. It accepts no configuration.
Among other normalizations:
- Rewrites `Option<T>` to `T?`
- Enforces naming conventions (§1.4)
- Normalizes trailing commas

---
