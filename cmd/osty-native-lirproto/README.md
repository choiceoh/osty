# `osty-native-lirproto` — MIR / source → LLVM IR bridge

Go shim (~540 LOC in `main.go`) that implements the Phase-7 **LIR Proto
subprocess** contract. The host (`internal/nativelirproto`) JSON-encodes a
request on stdin; this binary stages input, forks **`osty-self`**, and returns
LLVM IR text (or a structured **decline** so the caller can fall back to the
legacy MIR-direct path).

Production callers include `internal/backend/llvm.go` (MIR-owned emission),
`cmd/osty-native-llvmgen`, and workspace library compiles triggered by
`OSTY_CROSS_PKG_LINK=1` (`cmd/osty/cross_pkg_deps.go`).

## Wire contract

| Direction | Shape |
|---|---|
| stdin | `nativelirproto.Request` JSON (`internal/nativelirproto/exec.go`) |
| stdout | `nativelirproto.Response` JSON — `llvmIr` on success, or `declined: true` + `error` |

Request fields (all optional except that **either** `source` **or** `mir` must
be present after staging):

| Field | Role |
|---|---|
| `packageName` | Passed to `osty-self` as `--package-name=` (default `main`) |
| `sourcePath` | Filename hint for staged temp files and empty-source disk read |
| `source` | Osty source text for `lir-proto-lower` |
| `target` | Cross-compilation triple forwarded to `osty-self` |
| `mir` | Already-lowered MIR JSON for `lir-proto-lower-mir-json` (**wins** over `source` when set) |

**MIR wins:** when `mir` is non-null, the bridge stages `*.mir.json` and never
re-enters source lowering, even if `source` is also populated for diagnostics.

## `osty-self` dispatch

After staging, the binary execs `osty-self` with one of:

| Command | When |
|---|---|
| `lir-proto-lower` | Source-only request |
| `lir-proto-lower-mir-json` | `mir` field present |

Argv forwarding uses `OSTY_SELF_REBUILD_FORWARD_ARGS` (newline-joined args) so
`toolchain/main.osty` can recover the subcommand when `std.env.args()` is empty
inside the child.

Lookup order for `osty-self` is delegated to
`selfhostcache.ResolveBinaryWithFetch` — same as other native bridges (`$OSTY_SELF_BIN`
→ in-tree `.osty/out/.../osty-self` → content-addressed cache → optional registry
fetch). A missing binary returns **`declined: true`** with
`osty-self not found; run osty build toolchain/` so upstream code can fall back
without a hard exit.

## Legacy compatibility fallbacks

When `lir-proto-lower-mir-json` fails with specific errors, the bridge may retry
through older paths (all opt-in or bounded — production defaults stay on MIR JSON):

1. **Stage0 MIR compat** — re-lowers the staged MIR through the Go stage0 emitter
   when `osty-self` rejects the MIR JSON entry point.
2. **Source re-lowering** — only when `OSTY_LIRPROTO_SOURCE_COMPAT_MAX_BYTES` is a
   **positive** byte limit and the source payload fits; unset/`0` skips this path
   (PR that made source compat opt-in rather than silent).
3. **Timeout → stage0** — when `lir-proto-lower-mir-json` times out and the
   staged file is ≤ `OSTY_LIRPROTO_TIMEOUT_COMPAT_MAX_BYTES` (default 1 MiB),
   stage0 compat may run before giving up.

Declines are intentional: `internal/backend` treats them as signals to use the
MIR-direct emitter instead of failing the whole build.

## Managed binary resolution

| Mechanism | Path / behavior |
|---|---|
| **Managed slot** | `toolchain.EnsureNativeLIRProto` → `.osty/toolchain/<ver>/osty-native-lirproto` (built via `go build` during `just bootstrap`) |
| **Override** | `OSTY_NATIVE_LIRPROTO_BIN` — wins outright (`internal/nativelirproto.Env`) |

## Environment variables

Authoritative definitions are the `const` block at the top of `main.go`. The
README bootstrap table in [`README.md`](../../README.md) lists the operator-facing
subset next to checker and backend gates.

| Var | Default | Purpose |
|---|---|---|
| `OSTY_NATIVE_LIRPROTO_BIN` | _(unset)_ | Absolute path override; skips managed lookup |
| `OSTY_SELF_BIN` | _(unset)_ | Same as other bridges — pin a specific `osty-self` |
| `OSTY_LIRPROTO_SELF_TIMEOUT` | size-aware budget | Per-invocation timeout for `lir-proto-lower*` (`0` disables). Base 20s + up to 10m extra for large MIR JSON payloads |
| `OSTY_LIRPROTO_TIMEOUT_COMPAT_MAX_BYTES` | `1048576` | Max staged bytes for stage0 retry after MIR JSON **timeout** (`0` disables) |
| `OSTY_LIRPROTO_SOURCE_COMPAT_MAX_BYTES` | `0` (off) | Max source bytes for legacy **source** re-lowering when MIR JSON is unsupported (`0` = skip) |
| `OSTY_LIRPROTO_KEEP_STAGED` | _(unset)_ | Keep temp staging dir after the call (debug) |
| `OSTY_LIRPROTO_DEBUG` | _(unset)_ | Log staged path + command to stderr |
| `OSTY_KEEP_TMP` | _(unset)_ | Honor `stageInput` cleanup skip (shared with other tools) |

## Operational notes

- **Large packages:** compiling full `toolchain/` through this bridge can take
  10+ minutes; `ARCHITECTURE.md` documents stall risk for `OSTY_CROSS_PKG_LINK`
  experiments.
- **Declined ≠ crash:** stderr stays clean for recoverable declines; only JSON
  decode / env failures exit non-zero from `main`.
- **Tests:** wire-shape tests live in `main_test.go` and
  `internal/nativelirproto/exec_test.go` (fake binaries via
  `FAKE_NATIVE_LIRPROTO_*` env vars).

## Related docs

- Host adapter: `internal/nativelirproto/exec.go`
- Backend dispatch: `internal/backend/llvm.go` (`llvmDispatchMIRDirect`)
- Self-host artifact lookup: [`docs/osty_self_artifact_design.md`](../../docs/osty_self_artifact_design.md)
- Checker subprocess (parallel pattern): [`cmd/osty-native-checker/README.md`](../osty-native-checker/README.md)
