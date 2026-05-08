## 13. Tooling

Osty v0.6 tooling exposes the spec surface through commands. The CLI
(§13.1), package manifest format (§13.2), and zero-config formatter
(§13.3) carry forward from v0.5 unchanged. v0.6 adds: `osty context`
(§13.4, G47) for machine-readable intent extraction (purpose,
example, spec_refs, error_contract, capabilities, taint, budget) as
a single JSON document; `osty publish` (§13.5, G44) for SemVer
enforcement against `#[stability]` declarations through API surface
diff; `osty validate-spec` (§13.6, G38) for `#[spec(...)]` anchor
validation; audit subcommands (§13.7) for trusted-declassify /
trusted-construct / match-compat / legacy-globals enumeration; and
test-subcommand extensions (§13.8) for `--spec`, `--example`,
`--golden`, `--update-golden`, and `--doc` modes alongside `osty
bench --budget` runtime gates.

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

#### 13.1.1 v0.6 CLI subcommands grouped

The CLI surface is large; the v0.6 release groups subcommands into
five conceptual buckets:

| Bucket | Commands | Primary purpose |
|---|---|---|
| **Build** | `build`, `run`, `gen`, `pipeline` | Source → executable / artifact |
| **Test** | `test`, `bench`, `doc-test` (alias of `test --doc`) | Verification |
| **Quality** | `check`, `lint`, `fmt`, `airepair` | Source-level analysis |
| **Audit** | `audit`, `validate-spec`, `context` | Machine-readable surface inspection |
| **Release** | `publish`, `add`, `update`, `remove`, `fetch`, `info`, `search` | Package lifecycle |
| **Service** | `lsp`, `registry serve`, `cache`, `profiles`, `targets`, `features`, `explain` | Long-running tools / introspection |

The grouping is documentation-only — the CLI does not enforce a
namespace prefix. An author may compose any sequence (e.g. `osty
check && osty test --spec && osty publish --check`) without
restriction.

#### 13.1.2 Capability-related subcommands

Three CLI surfaces consume v0.6 capability information:

- **`osty audit --capabilities`** (§13.16) — enumerates each
  `pub fn`'s capability requirements per package.
- **`osty context <symbol>`** (§13.4) — emits per-symbol
  capability set as part of the context JSON.
- **`osty audit --legacy-globals`** (§10.46.6) — enumerates
  remaining v0.5 global effect calls during migration.

These three are the canonical inspection paths for "what
capabilities does this code use?" — adopted by `osty publish`'s
gate set and by IDE integrations.

#### 13.1.3 Information-flow-related subcommands

Two CLI surfaces consume v0.6 flow information:

- **`osty audit --trusted-declassify`** (§21.7.3) — enumerates
  every site that drops a flow tag explicitly.
- **`osty audit --taint-paths`** (Phase 5 — planned) — visualizes
  source-to-sink data paths in a workspace, useful for security
  review.

Phase 5 will add `osty audit --info-flow` as a unified flow
report; v0.6 baseline keeps the surfaces narrow.

### 13.2 Manifest

A project is described by `osty.toml` at its root. `osty.lock` records
exact resolved versions and content hashes.

**Schema.** The following TOML shape is accepted by the v0.6
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

#### 13.2.1 v0.6-specific manifest tables

Three v0.6 surfaces add manifest tables on top of the v0.5 baseline:

##### `[stability]`

```toml
[stability]
default = "stable"               # stable | experimental | deprecated | internal
```

The `default` value applies to every `pub` declaration that does not
carry an explicit `#[stability(...)]` annotation. `osty publish`
reads this default during the API-surface diff (§3.14.3) — a
package whose default is `experimental` is permitted to make
breaking changes within minor versions. A package whose default is
`stable` requires explicit `#[stability("experimental")]` on each
unstable symbol.

##### `[legacy]`

```toml
[legacy]
globals = false                  # default: false in v0.6
construct = false                # default: false in v0.6
```

`globals = true` activates the v0.5 → v0.6 desugar for global effect
calls (`time.now()` etc.) — see §10.46. `construct = true` permits
external literal construction of stdlib sealed types (§3.4.5). Both
flags are scheduled for removal in v0.7.

Activating either flag forces `[stability] default = "experimental"`
automatically — a package depending on legacy compatibility cannot
promise stable APIs.

##### `[budget]`

```toml
[budget]
strict = true                    # default: false
regression-threshold = 0.20      # 20% regression promotes W0795 to E0795
```

`strict = true` makes runtime budget violations (`W0795`) into
errors, blocking `osty publish` until fixed or the budget is
intentionally loosened. `regression-threshold` is the relative
performance loss that *automatically* triggers the strict path;
default 20% is the v0.6 baseline.

#### 13.2.2 Capability injection in manifest

A package that wants to *override* the default capability adapter
set (e.g. for embedded targets that lack a real `Net`) declares
the override via `[capability]`:

```toml
[capability.net]
adapter = "myproject.embedded.NetAdapter"  # custom Net implementation

[capability.fs]
adapter = "myproject.virtual.VfsAdapter"   # virtual file system
```

The named adapter must implement the corresponding capability
interface (§20.9). When the package is built, the adapter
substitutes for the canonical host adapter at the entry-point
`#[ambient]` binding site. This is how Osty supports targets where
a particular capability has no usable host implementation —
embedded systems, sandboxed runtimes, deterministic replay
environments.

`osty audit --capability-overrides` enumerates active overrides for
review.

#### 13.2.3 Workspace-level annotations defaults

A workspace may declare default annotation policies that propagate
to member packages unless overridden:

```toml
[workspace]
members = ["pkg-a", "pkg-b", "pkg-c"]

[workspace.defaults.stability]
default = "stable"

[workspace.defaults.budget]
strict = true
```

A workspace member's own `[stability]` / `[budget]` tables override
the workspace defaults. The workspace-level defaults exist so a
multi-package workspace can establish a uniform release discipline
without each package re-declaring it.

### 13.3 Formatter

`osty fmt` is the canonical formatter. It accepts no configuration.
Among other normalizations:
- Rewrites `Option<T>` to `T?`
- Enforces naming conventions (§1.4)
- Normalizes trailing commas

#### 13.3.1 v0.6 formatter additions

The v0.6 formatter adds three normalizations:

1. **Annotation ordering** — annotations on a declaration are
   re-sequenced into the conventional order (§3.8.14). The
   formatter writes them top-to-bottom: visibility / intent /
   examples / spec link / error contract / reproducibility / budget
   / stability / performance hints / compatibility / flow /
   capability marker.
2. **Capability parameter placement** — when a function takes both
   capability parameters and ordinary parameters, the formatter
   places capabilities first (left-aligned). Existing function
   signatures are not auto-rearranged unless `osty fmt
   --reorder-capabilities` is passed.
3. **Sealed type literal rewrite** — an external attempt to write
   `Email { local: ..., domain: ... }` (forbidden by §3.4.5) is
   rewritten by `osty fix --sealed-rewrite` into `Email.parse(...)?`
   if the field-level information is recoverable from the literal.
   The rewrite is opt-in because it changes call-site error
   handling (introduces a `?` propagation).

#### 13.3.2 Idempotence

`osty fmt` is idempotent — `fmt(fmt(x)) == fmt(x)` for any source
`x`. The implementation reaches idempotence by reparsing the
formatter's output and asserting the AST matches the input AST. Any
non-idempotent formatter behavior is a compiler bug (`osty fmt
--check` exits non-zero in CI to catch regressions).

#### 13.3.3 Annotation hint normalization

Performance hints (`#[vectorize]`, `#[parallel]`, `#[unroll]`,
`#[inline]`, `#[hot]`/`#[cold]`, `#[target_feature]`,
`#[noalias]`, `#[pure]`) are *advisory* — the compiler is free to
ignore them when the cost model disagrees. The formatter does not
strip ignored hints; it preserves the author's intent for future
readers. To enumerate which hints actually affected codegen, use
`osty build --report=hints`.

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

The full schema is specified in §13.9 below.

**LSP integration.** `textDocument/hover` returns the same data
rendered as markdown. AI agents query the JSON directly via `osty
context --format=json` or the LSP custom request `osty/context`.

#### 13.4.1 Symbol resolution

The `<symbol>` argument may be:

- **Fully-qualified** — `myapp.users.createUser`. Resolves
  exactly; ambiguity is impossible.
- **Method-qualified** — `User.greet`. Resolves against all types
  named `User` in the workspace; ambiguity is `E2105`.
- **Bare** — `createUser`. Searches the current package by default;
  `--all-packages` widens the search.
- **LSP cursor form** — `current://path:line:col`. The CLI reads
  the symbol at the named cursor position, identical to the LSP's
  `textDocument/documentSymbol` resolution.

#### 13.4.2 `--recursive` semantics

`--recursive=N` walks the call graph to depth `N`, including each
callee's full context object as a nested entry. The walk respects
package boundaries: callees in the same workspace are inlined;
callees in external dependencies are summarized (signature only,
no body details) unless the dependency was published with
`#[stability("internal")]` symbols exposed.

The recursion stops at:

- `Never`-returning functions (no further callees to visit).
- FFI imports (`use go "..."` / `use c "..."`) — the foreign
  symbol has no Osty-side context.
- Cycles (a function reachable through itself stops the walk).

#### 13.4.3 Bulk export workflow

`osty context --all-stdlib --format=jsonl` emits one JSON object
per line for every `pub` symbol in the standard library. The
typical consumer is an LLM training pipeline or an offline doc
generator:

```sh
$ osty context --all-stdlib --format=jsonl > stdlib-context.jsonl
$ wc -l stdlib-context.jsonl
3142 stdlib-context.jsonl
```

Each line is a complete context object — there is no shared
schema header. The recipient may stream-parse and filter without
buffering the entire output.

`--all-workspace` similarly emits the workspace's own `pub`
symbols. `--filter-stability=stable` restricts to stable APIs.

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

#### 13.5.1 Publish-time gates

Beyond the SemVer surface diff (Step 4), `osty publish` runs a
fixed gate set before signing:

| Gate | Behavior on failure |
|---|---|
| `osty check` (full type-check) | `E2100`-class abort |
| `osty audit --legacy-globals` | warns; promotes to abort if `[stability] default = "stable"` |
| `osty audit --trusted-declassify` | warns; lists every declassify site for review |
| `osty audit --trusted-construct` | same |
| `osty validate-spec` (`#[spec]` resolution) | `E0790` aborts |
| `osty test --example --spec` | example mismatch aborts |
| `osty bench --budget` | `time_ms`/`p99_ms` regression beyond `regression-threshold` aborts |
| `osty test --golden` | snapshot mismatch aborts |

The gate sequence is intentional: cheap checks first, expensive
benchmarks last. A `--no-bench` flag skips the budget gate for
local previews; the registry-side acceptance still requires the
full gate to have passed.

#### 13.5.2 Workspace publish

A workspace publishes its packages in topological order (root
dependency first). Each package is published as a separate
manifest entry; the workspace's `[workspace] version` is the
compound version visible to `osty add` consumers.

Cross-package references inside a workspace use *exact version
pin* during publish (the just-released version of the dependency).
External dependencies use the version range in `[dependencies]`.

#### 13.5.3 Publish dry-run

`osty publish --check` runs every gate but does not sign or upload
the manifest. It prints the surface diff and the proposed version
bump:

```sh
$ osty publish --check
api-surface diff:
  + pub fn isAcceptableEmail (private — internal helper)
proposed: v0.6.1 (patch — additive only)
0 errors, 0 warnings.
```

`--check` is the recommended pre-commit gate for branches that
modify `pub` declarations.

#### 13.5.4 Surface fingerprint

The API surface diff is computed against a *fingerprint* — a
content hash of the surface elements listed under "Surface
definition". Two workspaces with identical surface produce
identical fingerprints; this enables registry-side caching and
fast "is anything published?" checks.

The fingerprint excludes line numbers, formatting, and comments —
reformatting source code does not change the fingerprint. This is
why `osty fmt` is safe to run before `osty publish`.

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

#### 13.6.1 Anchor resolution algorithm

`osty validate-spec` walks the spec corpus once at startup, building
an index of `(file, heading) → anchor`. The walk follows these
rules:

1. **Section heading** (`## §X.Y Title`, `### §X.Y.Z Title`) — the
   anchor is `§X.Y` or `§X.Y.Z`. The title becomes the lead text.
2. **Sub-anchor** (`<a id="X.Y.user.create"></a>` immediately
   before a heading) — the anchor is the explicit `id`.
3. **Inline anchor** (text immediately following a heading) — the
   anchor is the heading's slugified title, with the parent
   section as prefix.

The index is rebuilt only when the spec corpus changes, so repeated
`osty validate-spec` invocations are fast.

For `#[spec("§X.Y")]` annotations whose argument matches a known
anchor, the resolver records the `(file, heading, lead)` triple and
caches it in `target/spec-links.json` for use by `osty doc` and
`osty context`.

#### 13.6.2 `--update` rewrite policy

When invoked with `--update`, `osty validate-spec` applies
suggested rewrites in three categories:

1. **Anchor renamed** — `§X.Y` is replaced by the target section's
   new anchor. The replacement is exact-string in the source file.
2. **Anchor moved** — `§X.Y.foo` becomes `§A.B.foo` if the named
   sub-anchor moved across sections. The resolver maintains a
   *moved-anchor history* across spec versions to enable this.
3. **Anchor removed** — emits `E0790` (no rewrite). The author
   must manually choose a replacement.

The rewrite mode is opt-in because changing spec links can have
publish-surface implications (§3.14.3 includes `#[spec]` arguments
in the API hash for `stable` symbols). CI typically runs
`osty validate-spec --strict` (no `--update`) and asks the author
to apply the rewrite locally with review.

#### 13.6.3 Cross-package spec validation

A workspace with multiple packages may reference spec anchors from
each. `osty validate-spec` resolves *all* anchors against a single
spec corpus snapshot — the workspace declares its target spec
version in `osty.toml` (`[package].edition = "0.6"`).

Mixed-edition workspaces (rare; transitional) resolve each package
against its own edition's corpus. The compiler does not allow
`edition = "0.5"` and `edition = "0.6"` to share a workspace
directly — use a `[workspace.member]` boundary with explicit
edition pin.

### 13.7 Audit subcommands

Several v0.6 annotations grant *audit-marked escapes* — places where
the language allows a normally-restricted operation under explicit
audit. Each escape is enumerable:

| Subcommand | Sites | Annotation / spec |
|---|---|---|
| `osty audit --trusted-declassify` | FFI / Go-side data drop | `#[trusted_declassify(reason)]` (§21.7) |
| `osty audit --trusted-construct` | sealed-struct bypass | `#[trusted_construct(reason)]` (§3.4.5.6) |
| `osty audit --match-compat` | enum-shape pin | `#[match_compat(... unsafe_silent = true)]` (§3.14.4) |
| `osty audit --legacy-globals` | pre-v0.6 global effect calls (e.g., `time.now()`, `random.next()`) | `--legacy-globals` flag (§7.1.1, v0.6.x only) |
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
| `osty test --doc` | `///` doc-test blocks (baseline since v0.5) | runs as test |

`osty bench --budget` (§3.15.2) gates runtime budget regressions.

### 13.9 `osty context` JSON schema

`osty context <symbol> --format=json` 출력은 다음 schema 를 따른다
(`https://osty.dev/schemas/context/v1.json`).

```json
{
  "$schema": "https://osty.dev/schemas/context/v1.json",
  "symbol": "std.user.createUser",
  "kind": "function",
  "package": "std.user",
  "file": "internal/stdlib/modules/user.osty",
  "line": 42,
  "signature": {
    "params": [
      {
        "name": "email",
        "type": "String",
        "annotations": [
          {"name": "taint", "args": ["user_input"]}
        ]
      },
      {"name": "db", "type": "Db", "annotations": []}
    ],
    "returns": "Result<UserId, UserCreateError>",
    "generic_params": [],
    "where_bounds": []
  },
  "stability": {"level": "stable", "since": "0.6"},
  "purpose": "이메일 검증 후 DB 에 사용자 저장",
  "examples": [
    {"input": ["alice@example.com", "<Db>"], "output": "Ok(42)", "uses_fixture": "fakeDb"},
    {"input": ["invalid", "<Db>"], "output": "Err(UserCreateError.Format)", "uses_fixture": "fakeDb"}
  ],
  "spec_refs": [
    {"section": "§10.30.user.create", "title": "User creation"}
  ],
  "error_contract": [
    {"variant": "UserCreateError.Format", "when": "missing @ or wrong format"},
    {"variant": "UserCreateError.DomainBlocked", "when": "domain in blocklist"},
    {"variant": "UserCreateError.DbConflict", "when": "duplicate email"}
  ],
  "effects": {
    "capabilities_required": ["Db"],
    "reproducible": null,
    "pure": false,
    "taint_sources": ["user_input"],
    "taint_sanitizes": [],
    "taint_sinks": ["sql_safe"]
  },
  "budget": {
    "static": null,
    "runtime": {"time_ms": 50, "p99_ms": 200}
  },
  "fixtures_referenced": ["fakeDb"],
  "doc_comment": "사용자를 생성한다. ...",
  "diagnostics_emitted": ["E0410"],
  "callees": []
}
```

#### 13.9.1 Schema field reference

| Field | Type | Source |
|---|---|---|
| `symbol` | `String` | qualified name |
| `kind` | `"function" \| "method" \| "struct" \| "enum" \| "interface" \| "type-alias" \| "constant"` | declaration kind |
| `package` | `String` | enclosing package path |
| `file`, `line` | `String`, `Int` | declaration site |
| `signature` | object | full type signature (params / return / generics) |
| `signature.params[].annotations` | array | parameter-position annotations (`#[taint]`, `#[requires]`) |
| `stability` | object \| null | `#[stability]` 데이터 |
| `purpose` | `String` \| null | `#[purpose("...")]` |
| `examples` | array | `#[example(input=, output=, uses=)]` 모두 |
| `spec_refs` | array | `#[spec("§X.Y")]` + 동일 함수의 모든 spec ref |
| `error_contract` | array | `#[error_contract]` entries |
| `effects.capabilities_required` | array of capability type names | 함수 시그니처에서 capability 파라미터 추출 |
| `effects.reproducible` | object \| null | `#[reproducible(scope=...)]` 데이터 |
| `effects.pure` | `Bool` | `#[pure]` 부착 여부 |
| `effects.taint_sources` | array | `#[taint("...")]` source tag list |
| `effects.taint_sanitizes` | array | `#[sanitizes]` source/trust 매핑 |
| `effects.taint_sinks` | array | parameter-position `#[requires]` trust tag list |
| `budget` | object | static / runtime 분리 |
| `fixtures_referenced` | array of names | `#[example(uses="X")]` 의 X |
| `doc_comment` | `String` \| null | `///` doc 첫 단락 |
| `diagnostics_emitted` | array of code strings | 함수가 발화 가능한 진단 코드 (compiler analysis) |
| `callees` | array | `--recursive` 시에만 채워짐 |

#### 13.9.2 Output formats

```sh
osty context <symbol>                          # default text (markdown)
osty context <symbol> --format=json            # 위 schema
osty context <symbol> --format=jsonl           # bulk export 시 1 symbol = 1 line
osty context <symbol> --recursive              # depth=1 callees 포함
osty context <symbol> --recursive=N            # depth=N
osty context current://path:line:col           # LSP cursor
osty context --search=<query>                  # symbol 검색 후 첫 매치
osty context --all-stdlib --format=jsonl       # 전체 stdlib export
```

#### 13.9.3 LSP integration

LSP server 는 `textDocument/hover` 응답을 같은 schema 의 markdown
rendering 으로 반환. AI agent 는 JSON 직접 query 또는 LSP custom
request `osty/context`.

```python
# AI agent pseudo-code
import subprocess, json
ctx = json.loads(subprocess.check_output(
    ["osty", "context", "std.user.createUser", "--format=json"]
))
prompt = f"""You are working on {ctx['symbol']}.
Purpose: {ctx['purpose']}
Failure modes:
{chr(10).join(f'  {e["variant"]} — {e["when"]}' for e in ctx['error_contract'])}
"""
```

### 13.10 `osty publish` algorithm

`osty publish` 는 packaging + SemVer 검증 + manifest signing 을 수행
(G44, §3.14.3 의 정식 명세).

#### 13.10.1 Workflow

```
1. Read previous manifest from registry (or git tag)
2. Compute API surface diff against current workspace
3. Classify each change as Breaking / CompatAdd / Patch
4. Compare diff severity vs version bump
   - Breaking + non-major bump      → E2100
   - CompatAdd + patch-only          → E2102
   - Version downgrade               → E2101
   - Experimental change             → W2100
5. On success, sign manifest (ed25519, A6) + upload
```

#### 13.10.2 Surface diff algorithm

```osty
fn computeDiff(prev: Manifest, curr: Manifest) -> DiffReport {
    let mut report = DiffReport::new()

    for prevItem in prev.surface {
        if let currItem = curr.surface.findByQualifiedName(prevItem.name) {
            classifyChange(prevItem, currItem) into report
        } else {
            // 1. #[stability(remove="X.Y")] 의 X.Y 도달 시
            if prevItem.deprecatedAt(curr.version) {
                report.add(REMOVED_AS_PROMISED, prevItem)
            } else {
                report.add(BREAKING_REMOVE, prevItem)
            }
        }
    }

    for currItem in curr.surface {
        if !prev.surface.containsByQualifiedName(currItem.name) {
            report.add(COMPAT_ADD, currItem)
        }
    }

    report
}
```

#### 13.10.3 Surface 분류 (요약)

§3.14.3.2 의 정식 표가 권위. 가장 흔한 변경:

| 변경 | Severity |
|---|---|
| 함수 제거 | BREAKING |
| 함수 시그니처 변경 (param/return) | BREAKING |
| 신규 enum variant + `#[since]` | COMPAT-ADD |
| 신규 enum variant 없음 (`#[since]`) | BREAKING |
| 신규 default arg trailing | COMPAT-ADD |
| 신규 default arg non-trailing | BREAKING (G20 named call shift) |
| stable 함수의 public parameter rename | BREAKING (keyword call surface changes) |
| default value expression 변경 | BREAKING for `stable`; `experimental` emits `W2100` |
| `#[reproducible]` 추가 | COMPAT-ADD (callee 보장 강화) |
| `#[reproducible]` 제거 | BREAKING (callee 보장 약화) |
| `#[error_contract]` variant 추가 | BREAKING (caller match exhaustiveness) |
| `#[budget]` 강화 (allocs ↓, time_ms ↓) | BREAKING |
| `#[budget]` 완화 | COMPAT-ADD |
| body / `#[purpose]` / `#[spec]` 등 metadata | PATCH |

#### 13.10.4 Manifest 형식

```json
{
  "$schema": "https://osty.dev/schemas/manifest/v1.json",
  "package": "github.com/x/y",
  "version": "0.6.0",
  "stability_default": "experimental",
  "surface": [
    {
      "kind": "function",
      "name": "std.user.createUser",
      "stability": "stable",
      "since": "0.6",
      "signature": {/* §13.9.1 와 동일 */}
    }
  ],
  "signature": {
    "ed25519_pub_key_id": "<key-fingerprint>",
    "signed_at": "2026-05-08T00:00:00Z"
  }
}
```

#### 13.10.5 Edge cases

- **Generic instance variation**: monomorphization 결과는 surface 아님. *type-level* 시그니처만.
- **Structural interface 변경**: `pub interface X { fn m(...) }` 의 `m` 변경은 BREAKING — 워크스페이스 내 *모든 구현체* 에 영향. publish 시 transitive 분석.
- **Trait alias / type alias transitive**: `type T = Foo<Int>` 후 `Foo` 변경 시 `T` 도 변경. 추적 필요.
- **Re-export (`pub use`) 변경**: re-exported 원본의 변경이 re-export site 도 영향.

#### 13.10.6 Dry-run

```sh
osty publish --dry-run
```

manifest diff + classification 만 출력 (서명 / upload 안 함). CI 의
*publish readiness* 검증에 사용.

```sh
osty publish --dry-run --report=summary
# Output:
#   2 BREAKING (require major bump)
#   3 COMPAT-ADD (require minor bump)
#   1 PATCH
#   Current bump: 0.6.0 → 0.6.1 (patch)
#   ❌ Insufficient bump — major required
```

### 13.11 `osty validate-spec` algorithm

`#[spec("§X.Y")]` 어노테이션의 anchor 가 `LANG_SPEC_v0.6/` 안에
존재하는지 검증.

#### 13.11.1 Workflow

```
1. 워크스페이스 walk → 모든 #[spec(...)] anchor 수집
2. LANG_SPEC_v0.6/ 의 모든 markdown 파일 walk → heading anchor 카탈로그
3. 각 #[spec] anchor 가 카탈로그에 있는지 확인
4. 누락 → E0790
5. 카탈로그에서 다른 chapter 로 이동했으면 (heading text 일치) → W0790 +
   suggested replacement
6. 모든 anchor 통과 → 0 errors
```

#### 13.11.2 Anchor 형식

`#[spec("§X.Y")]` 의 `§X.Y` 는 다음 패턴 매치:

- `§N` → `## N. <title>` heading
- `§N.M` → `### N.M <title>` heading
- `§N.M.K` → `#### N.M.K <title>` heading
- `§10.X.<id>` → `LANG_SPEC_v0.6/10-standard-library/<NN>-<id>.md` 의 `## 10.X` heading

#### 13.11.3 CLI

```sh
osty validate-spec                          # 전체 워크스페이스
osty validate-spec ./toolchain              # 특정 디렉토리
osty validate-spec --strict                 # W0790 도 error 처리
osty validate-spec --update                 # suggested replacement 자동 적용
```

`osty check --strict` 와 `just prepush` 가 자동 호출.

#### 13.11.4 CI 통합

```yaml
- name: Validate spec links
  run: osty validate-spec --strict
  # PR 가 spec section 을 옮기면 #[spec] 어노테이션도 함께 갱신해야 통과
```

### 13.12 Audit workflow

§13.7 이 enumerate 한 4 audit subcommand 의 *통합 사용* 패턴.

#### 13.12.1 Per-PR baseline diff

```yaml
# .github/workflows/security-audit.yml
- name: Run audits
  run: |
    osty audit --all --format=json > audit.json

- name: Compare to baseline
  run: |
    diff <(jq -r '.[] | "\(.subcommand) \(.symbol)"' audit.json | sort) \
         <(cat .ci/audit-baseline.txt | sort) \
      || (echo "::error::audit drift — review and update baseline" && exit 1)
```

baseline 파일은 `.ci/audit-baseline.txt` — 매 정상 머지 후 갱신:

```
trusted-declassify auth.session.fromSession
trusted-declassify api.legacy.fromLegacyClient
trusted-construct std.cache.internalCache
match-compat handlers.dispatch
legacy-globals util.timestamp
```

#### 13.12.2 신규 site 검토 워크플로

```
1. PR 가 새 trusted-declassify 추가
   → CI 가 audit drift 감지 → 머지 차단
2. 보안 reviewer 가 reason 검토 + approve
3. .ci/audit-baseline.txt 갱신 (해당 entry 추가)
4. 머지 가능
```

이는 *audit 드리프트가 보안 review 의 trigger* 가 되는 정책. v0.6
의 secure-by-default 정신 (legacy escape 의 visibility 강제).

### 13.13 Test subcommand 통합 reference

§13.8 의 detailed CLI:

| Mode | Discovers | Output |
|---|---|---|
| `osty test` | `#[test]` / `test_*` / `bench_*` (with `--bench`) | pass/fail summary |
| `osty test --spec` | `spec { example: }` clause | per-clause pass/fail |
| `osty test --example` | `#[example(input=, output=, uses=)]` | per-example pass/fail |
| `osty test --golden` | `#[golden(path, mode)]` | snapshot 비교 결과 |
| `osty test --update-golden` | (same) | snapshot 갱신 + report |
| `osty test --doc` | `///` doctest blocks | doc 안 example 실행 |
| `osty test --bench` | `bench*` + `#[bench]` | benchmark 결과 |
| `osty test --bench --benchtime <dur>` | (same) | auto-tuned iterations |
| `osty test --bench --budget` | (same) | runtime budget regression check |

#### 13.13.1 Filter / 병렬 / Random seed

```sh
osty test --filter=name           # 이름 패턴 매칭
osty test --serial                # 병렬 비활성
osty test --seed=N                # test order randomization seed 고정
osty test --report=junit          # JUnit XML 출력 (CI 통합)
osty test --report=json           # JSON
```

`--seed` 는 *재현 가능한* test order 를 보장. CI 가 fail 시 같은
seed 로 reproducer 생성 가능.

### 13.14 Tool integration matrix

| Annotation / Surface | `osty test` | `osty check` | `osty doc` | `osty context` | `osty publish` | `osty audit` |
|---|---|---|---|---|---|---|
| `#[test]` | ✓ discover | | | | | |
| `#[bench]` | ✓ discover | | | | | |
| `#[golden]` | ✓ snapshot | | | | | |
| `#[example]` | ✓ auto-test | | ✓ render | ✓ JSON | | |
| `#[fixture]` | ✓ seed | | ✓ render | ✓ JSON | | |
| `#[purpose]` | | | ✓ render | ✓ JSON | | |
| `#[spec("§X.Y")]` | | ✓ validate | ✓ inline ref | ✓ JSON | | |
| `spec { example: }` | ✓ auto-test | | ✓ render | ✓ JSON | | |
| `#[error_contract]` | | ✓ enforce | ✓ table | ✓ JSON | ✓ surface diff | |
| `#[reproducible]` | | ✓ enforce | | ✓ JSON | ✓ surface diff | |
| `#[stability]` | | | ✓ banner | ✓ JSON | ✓ enforce | |
| `#[since]` | | | ✓ render | ✓ JSON | ✓ surface diff | |
| `#[match_compat]` | | ✓ enforce | | | | ✓ enumerate |
| `#[budget]` | ✓ runtime check | ✓ static check | | ✓ JSON | ✓ surface diff | |
| `#[ambient]` | | ✓ enforce | | | | |
| `#[taint]` / `#[sanitizes]` | | ✓ enforce (Phase 5) | | ✓ JSON | ✓ surface diff | |
| `#[trusted_declassify]` | | | | | | ✓ enumerate |
| `#[trusted_construct]` | | | | | | ✓ enumerate |
| `#[sealed_construct]` | | ✓ enforce | | | ✓ surface diff | |

### 13.15 Forward compatibility

tooling surface 의 SemVer 영향:

| 변경 | 영향 |
|---|---|
| 새 subcommand 추가 | additive |
| 새 `--flag` 추가 | additive (flag default 가 기존 동작 보존 시) |
| Existing flag 의 default 변경 | breaking |
| Subcommand 제거 | breaking |
| JSON schema 의 새 field 추가 | additive (consumer 가 unknown field 무시) |
| JSON schema 의 field 제거 | breaking |
| Output format 변경 (text rendering) | non-breaking 가정 (스크립트 의존 시 `--report=json` 사용 권장) |

`osty publish` 자체의 `osty publish` 가 publish surface 인 것은
recursive 하지만 — `osty` CLI 자체는 의 publish 통한 SemVer 검증
대상은 아니다 (CLI 는 별도 release flow).

### 13.16 Capability inventory and review aids

A reviewer / author who needs to see *which capabilities a package
uses* without reading every file has three CLI surfaces:

```sh
# Per-package capability summary — one line per pub fn.
osty audit --capabilities

# Per-symbol detail — capabilities, flow tags, error_contract,
# stability, since, budget, fixtures, examples, spec link.
osty context <symbol> --format=json

# Diff of capability usage between two refs (e.g. main vs PR).
osty audit --capabilities --diff main..HEAD
```

Sample output of `osty audit --capabilities` (text mode):

```
pkg myapp.users
  pub fn createUser(email: String, db: Db)             [Db]
  pub fn parseEmail(s: String)                          (pure)
  pub fn fetchAvatar(net: Net, url: Url)                [Net]

pkg myapp.audit
  pub fn record(clock: Clock, fs: Fs, event: Event)     [Clock, Fs]
```

The same data drives review-bot comments and the package home-page
renderer.

### 13.17 Tooling under `--legacy-globals`

`--legacy-globals` is a *transitional* compatibility mode that lets
v0.5 source compile under v0.6.x. Tooling behavior under the flag:

| Surface | Default (`v0.6`) | `--legacy-globals` |
|---|---|---|
| `osty check` | rejects bare `time.now()` etc. (`E0780`) | accepts (`W0750` deprecation per call site) |
| `osty publish` | manifest's stability default may be `stable` | forced to `experimental` (global desugar contaminates surface) |
| `osty audit --legacy-globals` | enumerates remaining sites (none expected) | enumerates remaining sites |
| `osty bench --budget` | unchanged | unchanged (capability methods desugar to the same calls at runtime) |

`v0.7` removes the flag; programs that still depend on it must
migrate by then. The migration path is documented in
`MIGRATING_v0.5_to_v0.6.md` and §10.46 (Capability Migration
Catalog).

### 13.18 End-to-end v0.6 publishing workflow

This section walks through the canonical release flow for a v0.6
package — from local edit to published version — showing how each
v0.6 surface (capability, stability, error contract, spec link,
budget, golden) participates in the `osty` toolchain.

The example package is `myapp.users` exposing one `pub fn`:

```osty
#[purpose("Create a user, validating email and respecting DB unique constraints")]
#[example(input = "(\"alice@example.com\", fakeDb())", output = "Ok(42)")]
#[example(input = "(\"alice@example.com\", seededFakeDb())", output = "Err(UserCreateError.DbConflict(1))")]
#[spec("§10.30.user.create")]
#[stability("stable")]
#[since("0.6")]
#[error_contract(
    UserCreateError.EmailFormat   when "Email.parse failed",
    UserCreateError.DomainBlocked when "domain is in deny-list",
    UserCreateError.DbConflict    when "email unique constraint violated",
)]
#[budget(allocs = 4, io_calls = 1, time_ms = 5)]
pub fn createUser(email: String, db: Db) -> Result<UserId, UserCreateError> {
    spec {
        example: createUser("alice@example.com", fakeDb()) == Ok(UserId(42))
    }

    let parsed = Email.parse(email).orError(UserCreateError.EmailFormat)?
    if isBlocked(parsed.domain()) {
        return Err(UserCreateError.DomainBlocked(parsed.domain()))
    }
    db.exec(sql.insertReturningId("users", [
        ("email", sql.string(parsed.toString())),
    ]))
        .mapErr(|e| UserCreateError.DbConflict(e.code()))
        .map(|id| UserId(id.toInt()))
}
```

Step 1 — local check (`osty check`):

```sh
$ osty check
0 errors, 0 warnings.
```

Step 2 — examples + spec block (`osty test --example --spec`):

```sh
$ osty test --example --spec --report=summary
spec[createUser#example:1] PASS
example[createUser#1] PASS
example[createUser#2] PASS
3/3 spec/example checks passed.
```

Step 3 — golden / structural tests (`osty test`):

```sh
$ osty test --report=summary
12/12 tests passed.
```

Step 4 — performance budget (`osty bench --budget`):

```sh
$ osty bench --budget --report=summary
bench createUser: avg=2.1ms p99=4.7ms allocs=3 io_calls=1
budget OK (within 5ms / 5 allocs / 1 io_call).
```

Step 5 — capability + flow audit (`osty audit`):

```sh
$ osty audit --capabilities
pkg myapp.users
  pub fn createUser(email: String, db: Db)             [Db]
  fn isBlocked(domain: String)                          (pure)

$ osty audit --legacy-globals
0 sites — package is fully migrated to v0.6 capability surface.
```

Step 6 — surface diff (`osty publish --check`):

```sh
$ osty publish --check
api-surface unchanged from v0.6.0.
proposed: v0.6.1 (patch — internal helper added).
```

Step 7 — actual publish:

```sh
$ osty publish
publishing myapp.users v0.6.1
  + new symbol: fn isAcceptableEmail (private)
  api-surface diff: stable, additive only.
released.
```

The `osty context` JSON for `createUser` (Step 4 / §13.9 schema)
now contains the full intent payload — purpose, examples,
fixtures referenced, spec link, error contract, stability, budget,
capability set, sanitizer interactions — and is what an LLM agent
or downstream tooling consumes to reason about the function.

### 13.19 Release engineering with intent annotations

`#[purpose]`, `#[example]`, `#[fixture]`, `#[spec]` (§3.12, G42) are
the *machine-readable* layer underneath `osty doc` and
`osty context`. Their interactions during release engineering:

#### 13.19.1 `osty doc` rendering

Annotations land in the rendered doc page in this order:

1. Function signature + `#[since]` / `#[stability]` chip.
2. `#[purpose]` text — primary one-liner.
3. `#[spec]` anchor link — e.g. "§10.30.user.create" → resolved
   markdown anchor in the rendered chapter.
4. `#[error_contract]` — rendered as a "Failure modes" table.
5. `#[example]` — rendered as runnable example panels, with the
   referenced `#[fixture]` body inlined for context.
6. `spec { example: }` clauses — rendered as `// example` comments
   inside the function body in the page.
7. `#[budget]` — rendered as a "Performance budget" callout.

Authors who want a docstring on top of these annotations can keep
using `///`; doc comments and intent annotations compose
additively.

#### 13.19.2 `osty context <symbol>`

The CLI emits a single JSON object containing every intent
annotation plus the resolved spec link, the contract variants, the
fixture bodies, the example expressions, and the dependency
capability set. The schema is §13.9.

LLM-facing usage: a code-generation agent that wants to call
`createUser` retrieves the JSON, reads the contract / examples /
budget / spec, and constructs a semantically valid call site
without needing to read the implementation.

#### 13.19.3 `osty changelog` autogeneration

`osty publish` writes a changelog entry per release based on the
API-surface diff (§3.14.3) and the per-symbol intent payload:

```
v0.6.1 — 2026-05-08

Added
- `pub fn isAcceptableEmail` (private — no surface impact)

No public API changes.
```

For a release with public-surface changes, the entry includes the
`#[purpose]` text of each new / changed `pub` symbol so the
changelog reads like a feature list rather than a raw diff.

#### 13.19.4 `osty doc-test`

A specialized mode of `osty test` that runs every `#[example]` and
`spec { example: }` clause under a deterministic capability fake
set. Failure here means *the published example claims do not match
the implementation*, which would mislead documentation readers.
This is the strictest gate before `osty publish`.

```sh
$ osty doc-test
2 #[example] checks passed.
1 spec { example: } check passed.
```

The doc-test mode shares the same fake registry as `osty test
--example --spec` but omits regular `#[test]` functions. Useful as
a fast pre-commit or pre-publish gate.
