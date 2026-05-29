# Osty

- **Scope**: Project overview — build status, CLI reference, layout, testing, contributing
- **Type**: README
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
tracks spec/grammar work in v0.5, and the shipped manifest/scaffolder path now
defaults new projects to `edition = "0.5"` while manifest validation continues
to accept legacy `0.3` / `0.4` inputs for existing projects. The next work is
native implementation/runtime coverage. The public compiler path is now
native-only through the LLVM backend.

## Status

| Phase | Status |
|---|---|
| Lexer (UTF-8, ASI, triple-quoted strings, interpolation) | done |
| Parser (selfhosted front end, error recovery, fuzz-clean) | done |
| AST (all node kinds implement `ast.Node`) | done |
| Diagnostics (`error[E0002]:` with caret, hints, notes) | done |
| Name resolution (single + multi-file, workspace, typo suggestions) | done |
| Formatter (`internal/format`) | done |
| Type checker (`internal/check`) | done for the shipped v0.5 front-end core — generic instantiation, structural interface checks, exhaustiveness, builder protocol, function-value arity, closure pattern params. Algorithm: bidirectional + local unification, spec in [`LANG_SPEC_v0.5/02a-type-inference.md`](./LANG_SPEC_v0.5/02a-type-inference.md); `osty check --inspect` observes it at runtime |
| Linter (`internal/lint`, 34 registered rules with stable `Lxxxx` codes from L0001 through L0080 — unused / shadowing / dead-code / naming / simplify / complexity / docs / tests, `--fix` / `--fix-dry-run`, policy via `[lint]` in `osty.toml`; source of truth is `internal/lint/registry.go`) | done |
| Multi-file packages (`resolve` loader/package/workspace) | done |
| LSP (`internal/lsp`, wired as `osty lsp`) | done — hover, definition, formatting, documentSymbol, lint diagnostics, editor policy backed by toolchain sources |
| Native LLVM backend (`internal/backend`, `internal/llvmgen`) | public backend path; scalar/control-flow/string smoke subset emits LLVM IR/object/binary, later phase 64-73 value/control-flow smoke expansion is documented, unsupported shapes report Osty-authored LLVM diagnostics |
| Independent IR (`internal/ir`) | done — patterns, match, closures, struct/field/method, generic free-fn + generic struct/enum monomorphization with Itanium-mangled specializations (`ir.Monomorphize`, invoked from `backend.PrepareEntry`; fn symbols use `_Z…`, nominal types use `_ZTS…`) |
| Project scaffolding (`internal/scaffold`, `osty new` / `osty init`) | done — `--bin`, `--lib`, `--workspace`, `--cli`, `--service` |
| Manifest + lockfile + SemVer (`internal/manifest`, `lockfile`, `pkgmgr/semver`) | done (parse + validate + resolve) |
| Build orchestrator (`osty build`) | done — manifest → front-end → native backend, profile/target/feature wiring, backend-aware artifact/cache paths; LLVM binary emit can opt into sibling-package `.o` linking via `OSTY_CROSS_PKG_LINK` (experimental — see **LLVM workspace link** below) |
| `osty test` | native backend harness — discovers `test*` functions, compiles each through the LLVM backend, runs in parallel by default with a seeded shuffled order (`--seed`, `--serial`, `--jobs`), reports per-test wall time and an `ok/FAIL` summary; assertions are intercepted by the LLVM generator and on failure quote the original source text of every argument alongside the source location. `assertEq`/`assertNe` additionally render the runtime value of each side when it is an `Int`, `Float`, `Bool`, or `String`; `assertTrue`/`assertFalse`/`expectOk`/`expectError` quote the condition expression. `benchmark`/`snapshot` and ToString-protocol structural diff for `List`/`Map`/struct/enum values are not implemented yet |
| API doc generator (`internal/docgen`, `osty doc`) | done — checked-in generated Go package, HTML + markdown, field docs, cross-refs, `--check`, `--verify-examples`, workspace mode |
| CI quality tooling (`internal/ci`, `osty ci`) | done — Osty-authored generated CI core, signature-aware snapshots, workspace coverage, JSON reports |
| Pipeline visualizer (`osty pipeline`) | done — per-stage timing, workspace mode, backend-aware gen, baseline diff, LSP trace, `--explain` |
| Profiles / targets / features / cache (`internal/profile`, `osty profiles` / `targets` / `features` / `cache`) | done — built-in and manifest profiles, cross-target env, feature closure + file pragmas, backend-aware fingerprints |
| LLVM backend (`internal/backend`, `internal/llvmgen`, `--backend llvm`) | executable — textual IR / object / binary through `clang` for scalar / control-flow / Bool / String (ASCII + multi-byte UTF-8), all four payload-free/single-scalar/struct/enum-payload `Result<T, E>` shapes with `?` propagation (phase 54-63 + phase 74 closed), match-as-expression and match-as-statement over bare-variant and wildcard arms, nested field assignment, `Char`/`Byte` parameter + return lowering with width/sign conversions, interface vtable dispatch with generic monomorphization, list / map / set literals and intrinsics (`isEmpty`, `pop` discard, nested `IndexExpr`, source-type tracked list literals), payload-free enum match and `Float` / String payload enums, simple struct aggregates and method calls, `String.bytes` / `String.chars` lowering, host `clang` driver, and categorized `LLVM00x` / `LLVM01x` Osty-authored diagnostics for source shapes that still skeletonize. The merged toolchain native gate is not clean yet; current short-suite blockers are tracked below. |
| Package registry backend / `osty registry serve` | done — file-backed HTTP server for index/search/download/publish/yank, with ETag index responses and bearer-token write auth |
| Package registry / `osty add` / `osty update` / `osty run` | done (resolve + vendor + lockfile-honoring re-resolves, ETag-cached registry index, copy fallback for symlink-less filesystems; CLI: `add`, `remove`/`rm`, `update`, `run`, `fetch`, `publish`, `search`, `info`, `yank`/`unyank`, `login`/`logout`; `--locked` / `--frozen` CI guards) |
| Package manager (`osty add` / `osty update`, path + git + registry sources, SemVer resolver, deterministic lockfile) | wired — `add` mutates `osty.toml` and re-vendors; `update` re-resolves selectively or in full |
| `osty run` (build + exec through backend) | wired — resolves manifest, vendors deps, emits the native entry artifact, runs the backend binary with profile/feature flags, and rejects cross-target execution |
| `osty publish` (pack + upload tarball to a registry) | wired — deterministic gzipped tar, sha256 checksum, bearer-auth POST; `--dry-run` stops before upload |
| `osty-native-checker` LLVM self-build (`cmd/osty-native-checker/main.osty`) | **In progress** — the Osty entry serializes real check results through `tc.frontCheckSourceToWireJson` (M3, PR [#1938](https://github.com/choiceoh/osty/pull/1938)) and M4 wires UTF-8 byte spans, diagnostic line/column fields, summary telemetry (`errorsByContext` / `errorDetails`), and sha256 stable IDs to match the Go selfhost adapter (PRs [#1940](https://github.com/choiceoh/osty/pull/1940), [#1942](https://github.com/choiceoh/osty/pull/1942)). The input side now uses `readAllStdin`, escape-aware JSON string decoding, package-request routing, and top-level key-boundary matching so user source text cannot be mistaken for request metadata. Current verification reaches the LLVM clang link step, but the binary is not link-clean yet: `toolchain.front*` cross-package symbols remain unresolved unless the dependency object path can compile `toolchain`, and that path currently hits the LIR Proto `<error>` layout wall. Remaining work is tracked under [`SPEC_GAPS.md`](./SPEC_GAPS.md) (`cross-pkg-module-resolution`) and [`docs/llvm-selfhost-plan.md`](./docs/llvm-selfhost-plan.md). Milestone table: [`cmd/osty-native-checker/README.md`](./cmd/osty-native-checker/README.md). |

Status note (revalidated 2026-04-28): the universal
LLVM CLI wedge called out in older status docs is closed. A hello-world
`osty gen --backend=llvm` run is not the blocker anymore. The bootstrap
Osty→Go transpiler was retired in PR #854 (2026-04-23), so the older
"checked-in bootstrap output drift" framing no longer applies —
`internal/selfhost/generated.go` is now a frozen seed with no `go generate`
regen pipeline, and the corresponding "verify selfhost regen is clean" CI
step was removed. Current self-hosting tracking is about the native LLVM
coverage gate rather than front-end type-check drift.

Code-level re-audit on 2026-04-28: the tree is not yet "fully self-hosted"
end-to-end, but the front-end CLI path is ahead of several older documents.
`osty check`, `osty typecheck`, and `osty resolve` now run the self-host arena
path by default. The Go-hosted `--legacy` check/typecheck escape hatch and the
no-op `--native` flag have both been removed; the self-host arena pipeline is
the only path.
`internal/check` routes package checks through the native checker subprocess:
the CLI installs `check.UseManagedSubprocessChecker` at startup, which lazily
builds or reuses `.osty/toolchain/<ver>/osty-native-checker` via
`toolchain.EnsureNativeChecker`. After PR #1954 the managed artifact is the
**LLVM-built** `cmd/osty-native-checker/` binary (live `toolchain/*.osty`), not
the Go shell at `main.go`. Fresh clones use `OSTY_STAGE0_FALLBACK=1`
(`just bootstrap`) so `buildNativeChecker` temporarily installs the Go-built
shell until `osty-self` exists; a short recursion detour during the LLVM build
can do the same, then the outer build overwrites the slot with the LLVM
artifact. Setting `OSTY_NATIVE_CHECKER_BIN` overrides the managed path with a
prebuilt Go-built binary (debug / CI). `OSTY_NATIVE_CHECKER_LLVM_BIN` pins a
prebuilt LLVM-built binary without rebuilding. If no managed build and no
override resolve, callers get a clear checker-unavailable diagnostic — production
no longer silently falls back to the frozen in-process seed
(`SUBPROCESS_SWITCHOVER.md`, gates b-hard and b-llvm). Details:
[`cmd/osty-native-checker/README.md`](./cmd/osty-native-checker/README.md).

The front-end astbridge-free guards currently pass for the CLI and the
selfhost adapters (`TestRun{Resolve,Check,Typecheck}*AstbridgeFree`,
`TestCheckCLIDefaultPathExitsZero`, and the selfhost
`Check*Structured*AstbridgeFree` tests). `just front`, `just spec`,
`just verify-selfhost`, and `go run ./cmd/osty check toolchain` pass in the
same audit (note: `verify-selfhost` is narrow — it runs
`SnapshotParity|CoreSnapshotParity` under `internal/ci` and `internal/runner`,
not the merged toolchain MIR pipeline).

As of the 2026-04-29 test cleanup, part of the broad Go front-end/mid-end test
surface has moved toward Osty-authored fixtures. `just front` and `just short`
now start with the Osty-authored policy/source loop (`just osty`) and then run
the remaining host-side smoke tests. New coverage for lex/parse/resolve/check
policy should prefer `toolchain/*_test.osty` or focused host-boundary tests
rather than another broad Go-only front-end matrix. The live Osty executable
gate currently runs scalar control-flow, `Int` method, and `Int` aggregate
fixtures under `examples/int_control_e2e`, `examples/int_methods_e2e`, and
`examples/int_struct_e2e`.

The `TestGoGenerateSelfhostLeavesGeneratedArtifactsClean` red light cited
in earlier revisions of this section is gone — that test was removed when
the bootstrap transpiler was retired in PR #854.

Earlier walls for `Char` / `Byte` lowering, `list_mixed_ptr`, non-ASCII string
literals, match-as-statement, nested-field assignment, `List<T>.clear()`, and
`String.bytes()` / `String.chars()` are closed.

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
│   ├── osty-native-checker/ # Host subprocess that runs the native Osty checker
│   └── codesdoc/            # Regenerates ERROR_CODES.md from codes.go
├── internal/
│   ├── token/               # Token kinds + positions
│   ├── lexer/               # Thin Go facade over internal/selfhost tokenization
│   ├── ast/                 # AST node types
│   ├── parser/              # Thin Go facade + compatibility lowerings over internal/selfhost
│   ├── selfhost/            # Committed frozen Osty→Go seed (front end + adapters)
│   ├── cst/                 # Concrete syntax tree (Red/Green tree for lossless round-trip)
│   ├── query/               # Salsa-style incremental query graph (LSP + CLI/backend boundaries)
│   ├── airepair/            # AI-powered source adaptation / auto-repair
│   ├── diag/                # Diagnostics + Rust-style renderer
│   ├── resolve/             # Name resolution (single + multi-file)
│   ├── stdlib/              # Built-in prelude symbols + 63 top-level `modules/*.osty` + 6 primitives
│   ├── types/               # Semantic types (shared by checker + LSP)
│   ├── check/               # Type checker
│   ├── lint/                # Style/correctness lint rules (L0xxx codes)
│   ├── format/              # Canonical-style formatter
│   ├── ir/                  # Independent intermediate representation
│   ├── mir/                 # Middle-end IR (lowering, escape analysis, optimize)
│   ├── backend/             # Backend names, emit modes, native artifact layout
│   ├── llvmgen/             # LLVM bridge generated from Osty toolchain backend logic
│   ├── nativellvmgen/       # Native LLVM codegen execution
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

## AI agent wiki bundle

For a wiki-ready, AI-first navigation layer, see
[`docs/ai-agent-wiki/`](./docs/ai-agent-wiki/Home.md). It summarizes task
routing, non-negotiable repo rules, validation loops, and the canonical
source-of-truth documents without replacing them.

## Supported platforms

The toolchain (Osty CLI, `osty-native-checker`, and the LLVM-backend driver)
is built and verified for the following six host triples:

| OS      | amd64 (`x86_64`) | arm64 (`aarch64`) |
|---------|------------------|-------------------|
| Linux   | ✅ `linux/amd64`  | ✅ `linux/arm64`   |
| macOS   | ✅ `darwin/amd64` | ✅ `darwin/arm64`  |
| Windows | ✅ `windows/amd64`| ✅ `windows/arm64` |

CI cross-compiles all six triples on every push; see the `cross-compile` job in
[`.github/workflows/ci.yml`](.github/workflows/ci.yml). To reproduce locally:

```sh
just cross                        # build all six triples into .bin/cross/
just cross-one darwin arm64       # build a single triple
```

Cross-compilation targeting Osty programs (i.e. `osty build --target
<arch>-<os>`) is independent of the host matrix above and only requires an LLVM
toolchain capable of emitting for the requested triple.

## Building

Requires Go 1.26.2 or newer (matching `go.mod`).

```sh
go build -o osty ./cmd/osty
```

## Bootstrapping `osty-self`

The native LLVM backend forks `osty-self lir-proto-lower` for every
MIR → LLVM IR pass. `osty-self` is itself written in Osty
(`toolchain/*.osty`) so a fresh clone has a chicken-and-egg moment:
the host `osty` needs `osty-self` to compile, and `osty-self` needs
`osty` to compile. The artifact cache resolves this with a
content-addressed lookup chain so most workflows never have to
think about it.

### One-shot bootstrap

After `git clone` + `go build -o .bin/osty ./cmd/osty`:

```sh
just bootstrap   # builds osty + native-checker + native-lirproto, then
                 # `OSTY_STAGE0_FALLBACK=1 osty install-self` builds
                 # osty-self and promotes it into .osty/cache/self-host/
                 # <sha>-<triple>/.
```

`just bootstrap` bakes in `OSTY_STAGE0_FALLBACK=1` because the
production native-checker build path (PR #1954) gates on a resolvable
`osty-self`, which fresh clones do not have. The env var (PR #1980)
tells `install-self` to take the stage0 source-bootstrap path AND
tells `buildNativeChecker` to detour to `go build
./cmd/osty-native-checker` (PR #1988), sidestepping the chicken-and-
egg. After `install-self` succeeds, the Go-built native checker slot
is invalidated so the next `osty build` produces the LLVM variant
from live `toolchain/*.osty`. Override with `OSTY_STAGE0_FALLBACK=
just bootstrap` to verify the prebuilt-only path (registry / cache
hit) end-to-end.

Subsequent `osty build` / `osty run` / `osty test` invocations resolve
the binary through the cache and skip the slow toolchain rebuild.
The cache is keyed on the SHA-256 of every `toolchain/*.osty` file
plus the host triple, so editing toolchain sources invalidates the
entry automatically.

### Lookup order

`internal/toolchain/selfhostcache.ResolveBinaryWithFetch` consults,
in order:

1. `$OSTY_SELF_BIN` — explicit override for debugging or pinning a
   specific binary.
2. `toolchain/.osty/out/{debug,release}/llvm/osty-self` — the
   in-tree build path. Always wins over the cache so an active
   development build is never shadowed by a stale artifact.
3. `.osty/cache/self-host/<sha>-<triple>/osty-self` — the
   content-addressed cache. Populated by `osty install-self` and
   `verify-self-rebuild --reuse-stage1`.
4. **Network fetch** from `$OSTY_SELF_REGISTRY_URL`, falling back to
   `selfhostcache.DefaultRegistryURL` (the upstream `choiceoh/osty`
   rolling release `osty-self-snapshots`) when the env var is unset.
   Disabled by `$OSTY_SELF_REGISTRY_OFFLINE`. Successful fetches
   promote the binary into the local cache so step 3 hits next time.

When all four miss, the resolver returns the canonical `osty-self
not found` decline so the upstream backend dispatcher can fall back
to its decline handler. **Fresh clones therefore work out of the
box** — `just bootstrap` first consults the upstream registry, and
when offline / registry-unreachable falls through to the stage0
source-bootstrap path (the env var is baked into the recipe — see
the "One-shot bootstrap" section above).

### Network fetch + signing

CI dispatches the [`build-osty-self`](.github/workflows/build-osty-self.yml)
workflow per host triple. Each runner produces a manifest +
binary pair via `osty manifest-self`, optionally signed via
`osty sign-self` when the `OSTY_SELF_SIGNING_KEY` repo secret is
available. The published layout is:

```
<base>/<sha>-<triple>.json       # manifest
<base>/<sha>-<triple>.json.sig   # ed25519 signature (when signed)
<base>/<binary-url>              # osty-self binary
```

Consumer-side env vars (registry-specific subset; see the full
matrix in the next section):

| Var | Purpose |
|---|---|
| `OSTY_SELF_REGISTRY_URL` | Base URL the resolver GETs manifests / binaries from. Unset ⇒ falls back to `selfhostcache.DefaultRegistryURL` (the upstream rolling release). |
| `OSTY_SELF_REGISTRY_OFFLINE` | When truthy, hard-disables the fetcher even with a registry URL set. CI / air-gapped environments. |
| `OSTY_SELF_TRUSTED_KEY` | 64-char hex ed25519 public key. When set, manifests must be signed under the matching private key or the fetcher rejects them. Unset ⇒ falls back to `selfhostcache.DefaultTrustedKeyHex`; if both are empty, unsigned manifests are accepted (warn-only mode). |
| `OSTY_SELF_BIN` | Bypass everything and use this binary path. |

The fetcher verifies the binary's SHA-256 against the manifest
unconditionally. The signature check is opt-in via
`OSTY_SELF_TRUSTED_KEY` — once set the fetcher fails closed on
missing signatures (`ErrSignatureMissing`) so a registry compromise
cannot silently downgrade clients.

### Bootstrap env-var reference

All env vars touching `osty install-self` / `osty build` /
`internal/toolchain` bootstrap paths in one place. Truthy spellings
for boolean gates are `1` / `true` / `yes` / `on` (case-insensitive)
unless noted; empty / unset means disabled. Authoritative sources are
linked.

**osty-self resolution** ([`internal/toolchain/selfhostcache`](./internal/toolchain/selfhostcache)):

| Var | Purpose |
|---|---|
| `OSTY_SELF_BIN` | Absolute path to a prebuilt `osty-self` binary. Skips every other lookup. |
| `OSTY_SELF_REGISTRY_URL` | Registry base URL. Unset ⇒ `selfhostcache.DefaultRegistryURL`. |
| `OSTY_SELF_REGISTRY_OFFLINE` | Disables the registry fetcher (CI / air-gap). |
| `OSTY_SELF_TRUSTED_KEY` | Ed25519 public key (64-char hex) for manifest signature verification. |

**Stage0 source bootstrap** ([`cmd/osty/install_self.go`](./cmd/osty/install_self.go) / [`internal/toolchain/native_checker.go`](./internal/toolchain/native_checker.go)):

| Var | Purpose |
|---|---|
| `OSTY_STAGE0_FALLBACK` | **Single source of truth** for the chicken-and-egg bootstrap. When `=1`: `install-self` runs the stage0 source-bootstrap path (Go-side emitter) when no prebuilt `osty-self` is resolvable, AND `buildNativeChecker` detours to `go build ./cmd/osty-native-checker` instead of the LLVM build path (which would need the very `osty-self` we are trying to produce). `just bootstrap` bakes this in. The previous `--bootstrap-stage0` CLI flag was retired in favour of this gate. |
| `OSTY_STAGE0_LIST_ALL_DECLINES` | Stage0 emitter prints every stdlib-generic-method decline as a warning instead of just summary counts. Useful when diagnosing decline cascades. Auto-set by `install-self` when stage0 fallback runs. |

**LLVM backend / stdlib lowering** ([`internal/backend/entry.go`](./internal/backend/entry.go)):

| Var | Purpose |
|---|---|
| `OSTY_STDLIB_BODY_LOWER` | **Default ON** (unset or any value other than `0` / `false` / `off`). When enabled, `PrepareEntry` injects Osty-bodied stdlib methods (for example `collections.osty` `flatMap` / `zip`) into the user module so link resolves bodied helpers instead of stopping at missing `osty_rt_*` symbols. Set `=0` to bisect regressions or work around a fresh-clone path that trips an unrelated backend gap — `just bootstrap` no longer sets this by default (PR #2013), so CI exercises the production default. |

**Native checker selection** ([`internal/check/host_boundary.go`](./internal/check/host_boundary.go) / [`internal/toolchain/native_checker.go`](./internal/toolchain/native_checker.go)):

| Var | Purpose |
|---|---|
| `OSTY_NATIVE_CHECKER_BIN` | Absolute path to a prebuilt Go-built `osty-native-checker`. Wins over the managed slot and the LLVM-built variant when set. Used by `OSTY_NATIVE_CHECKER_BIN=$PWD/.osty/bin/osty-native-checker` after `just build-checker`. |
| `OSTY_NATIVE_CHECKER_LLVM_BIN` | Absolute path to a prebuilt LLVM-built `osty-native-checker-llvm`. Consulted by `ResolveNativeCheckerLLVM` before falling back to the in-tree build output at `cmd/osty-native-checker/.osty/out/debug/llvm/`. |
| `OSTY_BUILDING_NATIVE_CHECKER` | **Internal**, set by `buildNativeChecker` on its subprocess fork to abort nested re-entries (recursion guard). Do NOT set this manually. |

**Diagnostic & debug**:

| Var | Purpose |
|---|---|
| `OSTY_BUILD_PHASE_TIMING` | Print wall-clock phase markers (`install-self.locate-and-key`, `install-self.build-via-stage0`, etc) to stderr. Useful when profiling `install-self` or slow toolchain builds. |
| `OSTY_NATIVE_CHECKER_SOURCE_DUMP` | When set to a path, dumps the bytes handed to the native checker subprocess. Strictly a debug aid. |

**Backend test strictness** ([`internal/backend/native_mir_payload_stub_test.go`](./internal/backend/native_mir_payload_stub_test.go)):

| Var | Purpose |
|---|---|
| `OSTY_REQUIRE_REAL_LLVM_EMISSION` | When truthy, `requireRealLLVMEmission` **fails** tests if `osty-self` is not cached instead of skipping them. Use after `just bootstrap` (or any path that populated `.osty/cache/self-host/`) to surface MIR-direct regressions that would otherwise look like a clean skip on a fresh clone. CI sets this in [`fresh-clone-source-bootstrap.yml`](.github/workflows/fresh-clone-source-bootstrap.yml) after the offline bootstrap step. Without it, local `go test ./internal/backend/` stays fresh-clone-friendly. Known failing baseline when strict: [`docs/backend-test-failures-audit-2026-05-26.md`](./docs/backend-test-failures-audit-2026-05-26.md). |

**Common recipes** (pick one row per scenario):

| Scenario | Env |
|---|---|
| Fresh clone, online | _(none — `just bootstrap` handles it; registry is the default)_ |
| Fresh clone, offline / air-gapped | `OSTY_SELF_REGISTRY_OFFLINE=1` (stage0 fallback baked in via `just bootstrap`) |
| Manually verify the prebuilt-only path | `OSTY_STAGE0_FALLBACK= just bootstrap` (registry must be reachable) |
| Pin a specific `osty-self` | `OSTY_SELF_BIN=/path/to/osty-self` |
| CI staging a prebuilt LLVM-built checker across worktrees | `OSTY_NATIVE_CHECKER_LLVM_BIN=/path/to/osty-native-checker-llvm` |
| Reproduce CI strict backend gate locally | `just bootstrap` then `OSTY_REQUIRE_REAL_LLVM_EMISSION=1 go test -count=1 -short ./internal/backend/` |
| Bisect stdlib body injection | `OSTY_STDLIB_BODY_LOWER=0` on `osty build` / `install-self` |

### CI bootstrap gates

Two workflows cover the complementary fresh-clone paths (see
[`docs/osty_self_bootstrap_design.md`](./docs/osty_self_bootstrap_design.md)):

| Workflow | Schedule | Path exercised |
|---|---|---|
| [`bootstrap-smoke-test.yml`](.github/workflows/bootstrap-smoke-test.yml) | Weekly | Online registry fetch → cached `osty-self` |
| [`fresh-clone-source-bootstrap.yml`](.github/workflows/fresh-clone-source-bootstrap.yml) | Every PR + push to `main` | `OSTY_SELF_REGISTRY_OFFLINE=1 just bootstrap` (stage0 source bootstrap, same recipe contributors run offline) |

The per-PR workflow also runs `OSTY_REQUIRE_REAL_LLVM_EMISSION=1 go test -short ./internal/backend/` against the artifact it just built, so MIR-direct backend tests cannot silently skip when `osty-self` was missing.

### Cache maintenance

```sh
osty cache-self            # print canonical cache path for current toolchain
osty cache-self --check    # exit 1 if cache miss; useful in shell scripts
osty cache-self --key      # print the <sha>-<triple> stem
osty cache-self --triple   # print just the host triple

osty gc-self               # prune stale entries (default: keep current SHA + 5 LRU)
osty gc-self --dry-run     # print the plan without deleting
osty gc-self --keep 10     # custom LRU cap
osty gc-self --older-than 720h  # remove entries older than 30 days
```

`gc-self` always preserves entries whose SHA matches the current
toolchain — a `--keep=0` immediately after `install-self` cannot
delete the just-built artifact. Documented in
`docs/osty_self_artifact_design.md` (A1–A8 roadmap).

## Native Checker

`internal/check` speaks the JSON `api.CheckRequest` / `api.CheckResult`
boundary to a native `osty-native-checker` binary only (no in-process
embedded checker on production paths). On startup the CLI calls
`check.UseManagedSubprocessChecker`, which prepares the managed artifact under
`.osty/toolchain/<tool-version>/osty-native-checker` on first use. A managed
build failure surfaces as an explicit error instead of falling back to the
frozen seed. `OSTY_NATIVE_CHECKER_BIN` still wins when set (override, wrapper
script, or prebuilt binary from `just build-checker`).

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

## Build phase timing (`OSTY_BUILD_PHASE_TIMING`, optional)

When set to a truthy value (`1`, `true`, `yes`, `on`, case variants), `osty
build` and `osty install-self` flush **wall-clock phase markers** to stderr on
exit (lines like `phase-timing: <name> <duration>`). The gate is off by
default so normal builds pay only a cached env check. Instrumentation spans
the native backend and related pipeline phases (see `internal/backend/phase_timing.go`);
`internal/resolve`, `internal/ir`, and `internal/selfhost` duplicate the same env
gate (same stderr line shape; see `phase_timing.go` in each) so nested work is
visible without import cycles. Use this when profiling `install-self` or large
workspace builds to see whether the next bottleneck is front-end, MIR/IR, or
link — not as a stable machine interface.

## LLVM workspace link (experimental)

For manifest-driven **`osty build`** with `--backend llvm` emitting a **binary**, cross-package callees are normally lowered as unresolved `declare`s and fail at link time unless definitions are visible to the linker. Setting **`OSTY_CROSS_PKG_LINK=1`** (or `true`, `yes`, or `on`, case-insensitive) asks the CLI to compile each **other** workspace member package as a **library** object (skips emitting `main`, avoiding `_main` collisions), collect the `.o` paths, append them on `backend.Request.ExtraObjects`, and pass them to the final `clang` link.

The path is **default off** so production matches the pre–PR-G2 baseline: compiling every sibling package can stall the LIR Proto subprocess on very large trees, and cross-package dispatch is still tracked under [`SPEC_GAPS.md`](./SPEC_GAPS.md) (`cross-pkg-module-resolution`) and the LLVM self-host plan.

Behavior (workspace resolve + native-owned LLVM IR + binary emit only), stderr warnings on failed dep compiles without aborting the consumer build, and operational caveats are documented in [`ARCHITECTURE.md`](./ARCHITECTURE.md) under **LLVM binary link: cross-package dependency objects**; the implementation is [`cmd/osty/cross_pkg_deps.go`](./cmd/osty/cross_pkg_deps.go).

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
osty fmt FILE          # airepair + format to canonical style (see --check, --write)
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
osty scaffold <kind>   # one-off generators (fixture / schema / ffi / polyglot)
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

Single-file `osty check`, `osty resolve`, `osty typecheck`, `osty lint`, and
`osty pipeline` run the same **front-end** airepair pass in memory by default
before parsing (policy: `runner.UsesFrontEndAIRepair` in
`internal/runner/airepair_policy.go`, mirrored from `toolchain/airepair_flags.osty`).
You can still tune or disable it after the subcommand:

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
  `llvm` emits textual `.ll` covering scalar / control-flow / String
  (ASCII + multi-byte UTF-8 via per-byte `\HH` escapes) / immutable and
  mutable String locals / simple String function boundaries / simple
  struct aggregate values and fields / enum tags and match expressions
  (payload-free + single-scalar + struct payload + tag-and-ptr payload)
  with `?` propagation over any shape (phase 54-63 + phase 74 closed),
  match-as-statement with bare-variant/wildcard arms, nested field
  assignment, `Char`/`Byte` parameter and return lowering with width
  and sign conversions, interface vtable dispatch under generic
  monomorphization, list / map / set literals plus intrinsics
  (`isEmpty`, `pop` discard, nested `IndexExpr`), source-type tagged
  list literals, and a root-tracked native runtime that provides
  String / List / Map / Set / GC / scheduler / channel ABIs.
  Unsupported shapes still prepare skeleton artifacts and report
  `LLVM00x` / `LLVM01x` diagnostics authored by the toolchain backend
  policy. Current short-suite native gaps are optional aggregate lowering,
  nested struct binding patterns, generic method turbofish calls, interface
  boxing/dispatch, and `Map.update` locked lowering)
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
pins `edition = "0.5"`.

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
  `llvm` writes textual IR / object / binary through `clang` covering
  the scalar / control-flow / String (ASCII + multi-byte UTF-8) /
  struct / enum / `Result<T, E>` with `?` propagation (phase 54-63 +
  phase 74 closed) / match-as-expression and match-as-statement /
  nested field assignment / `Char`/`Byte` width+sign conversions /
  interface vtable dispatch under generic monomorphization / list /
  map / set literal and intrinsic surface described above, bundling a
  root-tracked LLVM native runtime that supplies String / List / Map /
  Set / GC / scheduler / channel ABIs; unsupported shapes still
  prepare skeleton artifacts and report missing lowering through
  `LLVM00x` / `LLVM01x` diagnostics from the Osty-authored backend
  policy.
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
just quick                 # fastest support-matrix smoke: stdlib + selfhost + front packages
just medium                # matrix smoke plus broader short Go verification
just verify-full           # full Go/Osty loop plus six-host cross-compile matrix
just support-matrix-fast   # STDLIB_MATRIX + SELFHOST_PORT_MATRIX fast smoke
just support-matrix-medium # deeper matrix bundles without the broad short loop
just support-stdlib-fast   # STDLIB_MATRIX module-set and resolve smoke
just support-selfhost-fast # SELFHOST_PORT_MATRIX default-path smoke
just osty                  # build, selfhost parity, `osty ci .`, and live Osty e2e tests
just osty-tests            # only the live Osty e2e test dirs
just front                 # Osty-first loop plus front-end package smoke
just short                 # Osty-first loop plus remaining short Go smoke
just gen TestQuestionOp    # one gen test or regex
just lsp TestCompletion    # one LSP test or regex
just pipe examples/calc    # front-end timing for an Osty package
just pipe-gen path/file.osty
```

The **spec corpus** lives under `testdata/spec/`:

- `positive/NN-<chapter>.osty` — one fixture per spec chapter; the
  `internal/speccorpus` driver auto-discovers every file in the
  directory and asserts the parser emits zero error-severity
  diagnostics. Known gaps between spec and implementation live in a
  `positiveWaivers` map inside `speccorpus_test.go` with a comment
  per entry — any new fixture either parses clean or is waived.
- `negative/reject.osty` — a bundled file of `// === CASE: Exxxx ===`
  blocks; the same driver splits each block and asserts the full
  front-end (lex → parse → resolve → check → lint) emits a diagnostic
  with the declared code. Cases the pipeline currently diverges on
  live in `negativeWaivers`, keyed by `"Exxxx/<hint>"`.

Run just the corpus driver with `go test ./internal/speccorpus/`. New
`.osty` fixtures or new `// === CASE: ===` blocks are picked up with
no Go-side registration; adding coverage is a one-file change.

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
(`LANG_SPEC_v0.5/`, `OSTY_GRAMMAR_v0.5.md`), and the shipped project
edition is **0.5** today: `osty new` / `osty init` write
`edition = "0.5"`, while manifest validation still accepts the
historical `0.3` / `0.4` range for existing projects. Future surface
changes follow the normal versioning process and land in a new spec
directory. See [`SPEC_GAPS.md`](./SPEC_GAPS.md) for the full decision
log and [`CHANGELOG_v0.5.md`](./CHANGELOG_v0.5.md) for which parts of
the v0.5 surface are wired in the CLI today versus still waiting on
regen. When docs discuss newer surface area, be explicit about whether
it is a design target or behavior already wired in the CLI and tests.

Conventions:
- Every new error site gets a stable `Exxxx` code in
  `internal/diag/codes.go` and a focused test that asserts the code.
- Fuzz regressions must be added to the corresponding corpus.
- No `panic` outside of programmer-error paths (nil map, impossible
  enum case) — lexer and parser recover gracefully.
