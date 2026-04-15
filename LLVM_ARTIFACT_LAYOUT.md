# LLVM Artifact Layout

이 문서는 LLVM 이주 25-step Phase 4의 산출물이다. 목적은 Go backend와 LLVM
backend가 같은 project/profile/target에서 동시에 동작해도 생성물이 서로
덮어쓰지 않도록 artifact layout을 확정하는 것이다.

## Legacy Layout

Phase 6 이전 구현은 Go backend 단일 경로를 전제로 했다.

| Surface | Current path shape | Source |
|---|---|---|
| `osty build` generated Go | `<root>/.osty/out/<profile>[-<target>]/main.go` | `profile.OutputDir`, `cmd/osty/build.go` |
| `osty build` binary | `<root>/.osty/out/<profile>[-<target>]/<bin>[-<target>]` | `cmd/osty/build.go` |
| `osty run` generated Go | `<root>/.osty/out/<profile>[-<target>]/main.go` | `cmd/osty/run.go` |
| build fingerprint | `<root>/.osty/cache/<profile>[-<target>].json` | `profile.CachePath` |
| `osty test` generated harness | `$TMPDIR/osty-test-<pkg-hash>/main.go`, `harness.go` | `cmd/osty/test.go` |
| `osty test` cached binary | `$TMPDIR/osty-test-<pkg-hash>/osty-test-bin-<hash>` | `cmd/osty/test.go` |
| publish tarball | `<root>/.osty/publish/<name>-<version>.tgz` | `cmd/osty/publish.go` |

This layout works for one active backend, but LLVM would collide with Go for
`main.go`, binary names, and cache fingerprints if it reused the same directory.

Phase 6 moved `osty build` and `osty run` Go source/binary artifacts to the
backend-aware `go/` output directory. The legacy cache path remains in use until
the cache migration step lands.

## Target Layout

Backend-aware artifacts should use this shape:

```text
<root>/.osty/out/<profile>[-<target>]/
  go/
    main.go
    <binary>
  llvm/
    main.ll
    main.o
    <binary>
    runtime/
      libosty_runtime.a
      include/
```

The profile/target key remains unchanged:

```text
<profile>
<profile>-<target-triple>
```

Examples:

```text
.osty/out/debug/go/main.go
.osty/out/debug/go/myapp
.osty/out/debug/llvm/main.ll
.osty/out/debug/llvm/main.o
.osty/out/debug/llvm/myapp
.osty/out/release-amd64-linux/go/main.go
.osty/out/release-amd64-linux/llvm/main.ll
```

## Backend Names

Backend directory names are stable API for tooling.

| Backend | Directory | Notes |
|---|---|---|
| Go transpiler | `go` | Existing backend, still default during migration |
| LLVM native | `llvm` | New backend; textual `.ll` first, then object/binary |

Do not encode implementation detail in the backend directory name. For example,
use `llvm`, not `clang`, `llc`, `capi`, or `llvm-ir`.

## Emit Modes

`--emit` controls which artifacts are requested, not where they live.

| Backend | Emit mode | Required artifacts |
|---|---|---|
| `go` | `go` | `go/main.go` |
| `go` | `binary` | `go/main.go`, `go/<binary>` |
| `llvm` | `llvm-ir` | `llvm/main.ll` |
| `llvm` | `object` | `llvm/main.ll`, `llvm/main.o` |
| `llvm` | `binary` | `llvm/main.ll`, `llvm/main.o`, `llvm/<binary>` |

For `osty run --backend=llvm`, the effective emit mode is `binary` because the
command must execute a host binary.

## Cache Layout

Build fingerprints must include backend identity.

Recommended cache shape:

```text
<root>/.osty/cache/<profile>[-<target>]/
  go.json
  llvm.json
```

During the migration, the existing cache path:

```text
<root>/.osty/cache/<profile>[-<target>].json
```

may remain as the legacy Go backend fingerprint. Once `internal/backend` owns
cache keys, move Go to the backend-aware shape and treat the legacy JSON as
stale if both exist.

Fingerprint fields should include:

- `backend`: `go` or `llvm`
- `emit`: requested emit mode
- `tool_version`: Osty compiler version
- backend toolchain identity
  - Go: `go version`, `go` executable path/modtime
  - LLVM: `clang --version` or `llc --version`, linker path, runtime ABI version
- source hashes
- manifest/profile/target/features
- produced artifact paths relative to project root

## Test Artifact Layout

Current `osty test` uses `$TMPDIR/osty-test-<pkg-hash>/` with generated Go files
and a cached Go test binary. Backend-aware tests should add a backend level:

```text
$TMPDIR/osty-test-<pkg-hash>/
  go/
    main.go
    harness.go
    go.mod
    osty-test-bin-<hash>
  llvm/
    main.ll
    harness.ll
    runtime/
    osty-test-bin-<hash>
```

The test binary hash should include backend, emit mode, toolchain identity, and
runtime ABI version. Go and LLVM test binaries must never share a cache key.

## Source Maps and Debug Files

Generated-source artifacts must stay inspectable after failures.

Go:

- `go/main.go` keeps existing `// Osty: path:line:column` comments.

LLVM:

- `llvm/main.ll` should initially keep readable source comments or metadata
  anchors near emitted functions/basic blocks.
- Later DWARF metadata may be added, but textual anchors are required first so
  failure reporting works before full debug-info support.

Failure reports should always include:

- backend name
- generated artifact path
- work directory
- rerunnable command
- nearest Osty source location when available

## Cleanup Semantics

`osty cache clean` currently removes `.osty/cache` and `.osty/out`. That remains
valid. Backend-aware subdirectories should not require a new cleanup command.

Rules:

- `osty cache clean` removes all backend artifacts.
- Future selective cleanup may accept backend filters, but that is not required
  for the LLVM migration's first implementation.
- Publish artifacts under `.osty/publish` are unrelated to backend build
  outputs and should remain unchanged.

## Migration Plan

The initial helper package for this plan is `internal/backend`.

1. Add backend-aware helpers without changing the default Go output path. Done
   in Phase 5.
2. Switch Go backend writes from `.osty/out/<key>/main.go` to
   `.osty/out/<key>/go/main.go`. Done for `osty build` and `osty run` in
   Phase 6.
3. Add `--backend=go|llvm` CLI plumbing. Done for `gen`, `build`, `run`, and
   `test` in Phase 7; `llvm` currently reports a not-implemented error.
4. Add `--emit=go|llvm-ir|object|binary` CLI plumbing. Done for `gen`,
   `build`, `run`, and `test` in Phase 8. `build --emit=go` writes Go source
   without linking; `run` and `test` require `binary`.
5. Update failure reporting tests to accept the new generated path.
6. Move cache fingerprints from `.osty/cache/<key>.json` to
   `.osty/cache/<key>/go.json`.
7. Add LLVM artifacts under `.osty/out/<key>/llvm/`.
8. Add LLVM cache fingerprints under `.osty/cache/<key>/llvm.json`.
9. Keep a compatibility note for existing ignored `.osty/out/<key>/main.go`
   files; they can be deleted by `osty cache clean`.

The backend subdirectory change should land before the LLVM backend writes any
files, so LLVM never shares the old Go-only output location.

## Phase 4 Done Criteria

- The current artifact layout is documented.
- The target backend-aware layout is documented.
- Cache and test artifact separation rules are documented.
- The migration order avoids Go/LLVM clobbering.
