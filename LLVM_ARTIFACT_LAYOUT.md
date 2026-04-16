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
| build fingerprint | `<root>/.osty/cache/<profile>[-<target>].json` | `profile.LegacyCachePath` |
| `osty test` generated harness | `$TMPDIR/osty-test-<pkg-hash>/main.go`, `harness.go` | `cmd/osty/test.go` |
| `osty test` cached binary | `$TMPDIR/osty-test-<pkg-hash>/osty-test-bin-<hash>` | `cmd/osty/test.go` |
| publish tarball | `<root>/.osty/publish/<name>-<version>.tgz` | `cmd/osty/publish.go` |

This layout works for one active backend, but LLVM would collide with Go for
`main.go`, binary names, and cache fingerprints if it reused the same directory.

Phase 6 moved `osty build` and `osty run` Go source/binary artifacts to the
backend-aware `go/` output directory. Phase 9 moved the Go build fingerprint to
`.osty/cache/<key>/go.json`; legacy `<key>.json` records are ignored for
freshness checks and can be removed by `osty cache clean`.

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

may remain on disk as a stale legacy Go backend fingerprint. Migrated builds
write and read the backend-aware path only; `ReadLegacyFingerprint` exists for
diagnostics, not for freshness decisions.

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
   `.osty/cache/<key>/go.json`. Done in Phase 9; `osty cache ls` now shows
   backend and emit columns.
7. Add LLVM artifacts under `.osty/out/<key>/llvm/`. Done in Phase 10 as
   `internal/backend.LLVMBackend` skeleton: it prepares `main.ll` and
   `runtime/`, then returns a not-implemented error until lowering lands.
8. Route CLI backend selection through the concrete backend factory. Done in
   Phase 11 for `gen`, `build`, and `run`; `test` still keeps a harness-specific
   guard until backend-aware test generation lands.
9. Add minimal LLVM textual IR lowering. Done in Phase 12 as
   `internal/llvmgen`: it emits `@main`, a `printf` declaration, and integer
   `println` expressions for a first scalar subset. Unsupported shapes still
   fall back to skeleton IR.
10. Extend the scalar LLVM slice to functions, immutable lets, identifiers,
    comparisons, and statement-position if/else. Done in Phase 13; the
    `scalar_arithmetic` LLVM smoke fixture now emits real textual IR, while
    object and binary emission still wait for the native toolchain driver.
11. Add mutable scalar locals, simple assignment, and Int range for-loops. Done
    in Phase 14; the `control_flow` LLVM smoke fixture now emits real textual
    IR with `alloca`/`load`/`store` and loop labels.
12. Port the Phase 12-14 lowering core to Osty source. Done as a correction
    pass after Phase 14: `examples/selfhost-core/llvmgen.osty` now owns the
    minimal/scalar/control-flow textual IR builder. Go `internal/llvmgen`
    includes generated bridge code from that Osty source and calls it for
    module/function rendering.
13. Port the Phase 10 skeleton renderer to Osty source. Done after the Phase
    12-14 correction pass: `examples/selfhost-core/llvmgen.osty` now owns the
    unsupported/skeleton IR shape as well as the successful smoke IR builders.
14. Add a drift guard between the Go bootstrap emitter and the Osty-owned
    emitter. Done after the Phase 10 port: `internal/backend` transpiles
    `examples/selfhost-core/llvmgen.osty` during test and compares
    minimal/scalar/control-flow/skeleton output byte-for-byte against the Go
    bootstrap/reference implementation.
15. Route the production LLVM bridge through generated Osty-owned renderers.
    Done after the drift guard: `internal/llvmgen/osty_generated.go` is
    generated from `examples/selfhost-core/llvmgen.osty`, and `Generate` plus
    unsupported skeleton fallback call those generated functions.
16. Add Bool expression and value-position if/else lowering. Done in Phase 15;
    the `booleans` LLVM smoke fixture now emits comparison, logical not/and,
    conditional branches, and `phi` in real textual IR owned by the Osty
    emitter core.
17. Add LLVM cache fingerprints under `.osty/cache/<key>/llvm.json`. Done in
    Phase 16 for successful `build --backend=llvm --emit=llvm-ir`; cache hits
    require both a matching fingerprint and the recorded LLVM artifacts to
    still exist on disk.
18. Keep a compatibility note for existing ignored `.osty/out/<key>/main.go`
    files. Done in Phase 17: migrated builds ignore legacy root-level
    `main.go` and legacy `<key>.json` cache records, write backend-aware
    artifacts/cache entries, and still let `osty cache clean` delete the old
    tree.
19. Add the LLVM host toolchain driver. Done in Phase 18 with self-hosting
    ownership preserved: `examples/selfhost-core/llvmgen.osty` owns the
    object/binary stage decisions, `clang` argv shape, and toolchain diagnostic
    text; the Go bridge only performs file I/O and process execution. Supported
    LLVM lowering can now drive `.ll -> .o` and `.o -> binary`,
    `build --backend=llvm --emit=object|binary` writes the expected artifacts,
    and `run --backend=llvm` executes the produced host binary.
20. Add the executable parity gate for the LLVM smoke corpus. Done in Phase 19:
    `examples/selfhost-core/llvmgen.osty` owns the fixture list and expected
    stdout via `llvmSmokeExecutableCorpus`, while the Go test only acts as the
    host shim that emits binaries and executes them when `clang` is available.
21. Add self-hosted unsupported/backend-capability diagnostics. Done in Phase
    20: `examples/selfhost-core/llvmgen.osty` owns go-only diagnostics such as
    `use go` under LLVM, generic unsupported fallback text, and the
    not-implemented backend message. The Go bridge detects source features and
    writes skeleton artifacts, but diagnostic wording and policy stay in Osty.
22. Add the self-hosted unsupported taxonomy. Done in Phase 21:
    `examples/selfhost-core/llvmgen.osty` owns the unsupported category codes
    and hints (`source-layout`, `type-system`, `statement`, `expression`,
    `control-flow`, `call`, `name`, and `function-signature`). The Go bridge
    only maps detected AST situations to those categories before writing the
    same skeleton artifact path.
23. Route scalar LLVM instruction building through the self-hosted core. Done
    in Phase 22: successful scalar lowering now calls generated builders for
    return, println, arithmetic, comparison, function calls, if branches,
    mutable local slots, loads, stores, and Int range loops. Go still walks the
    bootstrap AST, but the LLVM instruction strings and temp/label naming live
    in `examples/selfhost-core/llvmgen.osty`.
24. Add the first String LLVM vertical path through the self-hosted core. Done
    in Phase 23: `println("plain ascii")` emits an Osty-owned module string
    constant and print call, and the executable smoke corpus now includes
    `string_print.osty`. The Go bridge only filters literals to the current
    conservative subset before calling generated Osty helpers.
25. Add escaped ASCII String constants through the self-hosted core. Done in
    Phase 24: `examples/selfhost-core/llvmgen.osty` owns newline, tab,
    carriage-return, quote, and backslash encoding for LLVM C strings, and the
    smoke corpus now includes `string_escape_print.osty`.
26. Add the first local String value path. Done in Phase 25: String literals
    lower to self-hosted `ptr` values that can be bound with immutable `let`
    and later printed through identifiers, with `string_let_print.osty`
    covering the executable smoke case.
27. Split String value constants from `println` formatting. Done in Phase 26:
    string literals now emit NUL-terminated value constants, while
    `llvmPrintlnString` owns the `@.fmt_str` newline formatting call.
28. Add String return values. Done in Phase 27: `String` lowers to `ptr` in
    function signatures, so String literals can be returned and printed from a
    function call.
29. Add String parameters. Done in Phase 28: `String` arguments and parameters
    use the same `ptr` value path through generated self-hosted call builders.
30. Add mutable String locals. Done in Phase 29: mutable `String` slots use
    `alloca ptr`, `store ptr`, and `load ptr` through the existing Osty-owned
    mutable local helpers.
31. Add named struct type definitions and field reads. Done in Phase 30:
    non-generic struct declarations emit `%Name = type { ... }`, struct
    literals use the Osty-owned `insertvalue` builder, and field reads use the
    Osty-owned `extractvalue` builder.
32. Add struct return values. Done in Phase 31: simple aggregate struct values
    can be returned from helper functions and read in `main`.
33. Add struct parameters. Done in Phase 32: simple aggregate struct values can
    be passed to helper functions and field-read inside the callee.
34. Add mutable struct locals. Done in Phase 33: mutable struct slots reuse the
    generic Osty-owned alloca/store/load helpers for whole-value assignment and
    later field extraction.
35. Add payload-free enum variant values. Done in Phase 34: bare variants lower
    to Osty-owned `i64` tag values and can participate in equality/if smoke
    paths.
36. Add enum return values. Done in Phase 35: payload-free enum tag values can
    cross helper return boundaries.
37. Add enum parameters. Done in Phase 36: payload-free enum tag values can
    cross helper parameter boundaries.
38. Add mutable enum locals. Done in Phase 37: enum tag values reuse the generic
    Osty-owned alloca/store/load helpers for whole-value assignment and later
    comparison.
39. Add payload-free enum match expressions in `main`. Done in Phase 38: bare
    enum tags drive two-arm `match` expressions before the later tagged-payload
    ABI work.
40. Add enum match return-boundary smoke. Done in Phase 39: payload-free enum
    match expressions can consume helper return values.
41. Add enum match parameter-boundary smoke. Done in Phase 40: payload-free
    enum match expressions can consume helper parameters.
42. Add mutable enum match locals. Done in Phase 41: enum match values reuse the
    generic Osty-owned alloca/store/load helpers for whole-value assignment and
    later comparison.
43. Add payload-enum declarations and constructors for a single-Int payload
    subset. Done in Phase 42: `%Enum = type { i64, i64 }` represents tag and
    payload for conservative enum smoke fixtures.
44. Add payload enum return-boundary smoke. Done in Phase 43: single-`Int`
    payload enum tags and payload extraction can cross helper return values.
45. Add payload enum parameter/mutable-local smoke. Done in Phase 44 for
    parameter-boundary paths and Phase 45 for mutable-local paths, with
    payload extraction in `match`.
46. Add Float literal/printing smoke. Done in Phase 46: `Float` is treated as
   `double` in this subset.
47. Add Float arithmetic smoke. Done in Phase 47: `+`, `-`, `*`, `/` on `Float`
   values are emitted through selfhosted LLVM builders.
48. Add Float return-boundary smoke. Done in Phase 48: return and call paths for
   `Float` values.
49. Add Float parameter-boundary smoke. Done in Phase 49: `Float` value
   passing across helper parameters.
50. Add Float mutable-local smoke. Done in Phase 50: mutable `Float` local slot
   assignment/load paths.
51. Add Float comparison smoke. Done in Phase 51: value-position `if` on
   `Float` comparisons.
52. Add Float struct aggregate smoke. Done in Phase 52: simple struct values
   carrying `Float` fields.
53. Add Float payload enum smoke. Done in Phase 53: `Full(Float)` payload enum
   construction, `match` binding, and print path.
54. Add Float payload enum return-boundary smoke. Done in Phase 54: `Full(Float)` can
   cross helper-return boundaries, and match can extract payload in caller return
   context.
55. Add Float payload enum parameter-boundary smoke. Done in Phase 55: payload enum
   values with `Float` payload can pass through helper parameters and be pattern-matched.
56. Add Float payload enum mutable locals. Done in Phase 56: mutable local slot write/read
   for `FloatMaybe` values plus `match` payload binding in place.
57. Add Float payload enum reversed two-arm matches. Done in Phase 57:
   `match` payload dispatch works with `Empty` first and `Full(x)` second.
58. Add Float payload enum wildcard matches. Done in Phase 58: `Full(_)` wildcard
   match arm works for `Float` payload enum fixtures.
59. Add String payload enum return-boundary smoke. Done in Phase 59: `Text(String)` enum
   constructors can cross helper-return boundaries and lower to pointer-backed `String`
   matching.
60. Add String payload enum parameter-boundary smoke. Done in Phase 60: `Text(String)`
   payload enums pass through helper parameters and can be returned from `match`.
61. Add String payload enum mutable locals. Done in Phase 61: mutable `Label` locals with
   `Text("payload string")` update and `match` payload extraction.
62. Add String payload enum reversed two-arm matches. Done in Phase 62: enum payload
   match order with `Empty` first and `Text(s)` second.
63. Add String payload enum wildcard matches. Done in Phase 63: `Text(_)` wildcard
   pattern matches for `String` payload enum fixtures.

Float/Float32/Float64 policy is intentionally deferred.

The backend subdirectory change should land before the LLVM backend writes any
files, so LLVM never shares the old Go-only output location.

## Phase 4 Done Criteria

- The current artifact layout is documented.
- The target backend-aware layout is documented.
- Cache and test artifact separation rules are documented.
- The migration order avoids Go/LLVM clobbering.
