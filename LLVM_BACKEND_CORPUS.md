# LLVM Backend Corpus

이 문서는 LLVM 이주 Phase 2의 산출물이다. 목적은 backend parity에 사용할
fixture를 명시적으로 분류해서, Go backend의 현재 동작과 LLVM backend의 목표
범위를 섞지 않도록 하는 것이다.

## Classification

| Class | Meaning | LLVM gate usage |
|---|---|---|
| `exec` | 현재 Go backend로 실행 가능한 기준 프로그램 | LLVM이 같은 stdout/exit code를 내야 하는 parity gate |
| `front-end-only` | parse/resolve/check 기준선으로만 쓰는 fixture | LLVM codegen gate로 쓰지 않음 |
| `go-only` | Go backend 전용 기능에 의존 | LLVM은 명확한 unsupported diagnostic을 내야 함 |
| `future-runtime` | runtime/stdlib/concurrency port 이후에야 의미 있는 fixture | 초기 LLVM gate에서 제외 |
| `llvm-smoke` | LLVM 첫 세로 경로를 위해 새로 만든 작은 fixture | `.osty -> IR -> .ll -> binary` 초기 gate |

## Current Repository Fixtures

| Path | Class | Current status | Rationale |
|---|---|---|---|
| `examples/concurrency` | `future-runtime` | Go `run`/`build` pass | Channel, spawn, `parallel`, `taskGroup` runtime parity가 필요하다. |
| `examples/ffi` | `go-only` | Go `run`/`build` pass | `use go "strings"`는 native LLVM backend에서 그대로 지원할 수 없다. |
| `examples/calc` | `front-end-only` | `check` pass, `test` fail | Package front-end baseline으로만 사용한다. 현재 `osty test`는 `expectError` error type mismatch로 실패한다. |
| `examples/workspace` | `front-end-only` | `check` pass, root `build` pass | Workspace resolution/orchestration baseline이다. Root binary는 없다. |
| `examples/stdlib-tour` | `future-runtime` | Not sampled in Phase 1 | stdlib runtime coverage가 늘어난 뒤 module별로 승격한다. |
| `examples/dogfood` | `front-end-only` | Not sampled in Phase 1 | Self-host/dogfood scale fixture. LLVM 초기 executable gate로는 너무 크다. |
| `examples/selfhost-core` | `front-end-only` | Not sampled in Phase 1 | Front-end/dogfood baseline. LLVM 초기 executable gate에서 제외한다. |
| `word_freq.osty` | `future-runtime` | Not sampled in Phase 1 | Real-world candidate. Regex/fs/json/parallel 등 runtime dependency가 크다. |
| `testdata/spec/positive/*.osty` | `front-end-only` | Existing spec corpus | Spec coverage용이다. 일부 파일은 library-shaped라 실행 gate로 쓰지 않는다. |
| `testdata/spec/negative/reject.osty` | `front-end-only` | Existing reject corpus | Diagnostic reject 기준이다. Backend gate로 쓰지 않는다. |

## Initial LLVM Smoke Corpus

The initial LLVM smoke corpus lives under `testdata/backend/llvm_smoke/`.
Each fixture should be independently checkable as a single file and avoid:

- package/workspace dependencies
- `use go`
- heap-heavy stdlib modules
- channels/concurrency
- test harness APIs
- generic/interface lowering requirements

| Path | Expected stdout | Coverage |
|---|---|---|
| `testdata/backend/llvm_smoke/minimal_print.osty` | `42\n` | Phase 12 minimal LLVM IR: no-arg `main` plus integer `println` expression |
| `testdata/backend/llvm_smoke/scalar_arithmetic.osty` | `42\n` | Phase 13 LLVM IR: primitive integers, helper function call, immutable let, comparison, if/else |
| `testdata/backend/llvm_smoke/control_flow.osty` | `15\n` | Phase 14 LLVM IR: mutable local, inclusive range loop, accumulator update |
| `testdata/backend/llvm_smoke/booleans.osty` | `7\n` | Phase 15 LLVM IR: Bool comparison, logical not/and, value-position if/else, phi |
| `testdata/backend/llvm_smoke/string_print.osty` | `hello, osty\n` | Phase 23 LLVM IR: plain printable-ASCII `String` literal through `println` and module string constants |
| `testdata/backend/llvm_smoke/string_escape_print.osty` | `line one\nquote " slash \\\n` | Phase 24 LLVM IR: escaped ASCII `String` literal through Osty-owned LLVM C-string encoding |

The Phase 10 skeleton behavior and the Phase 12-15 plus Phase 23-24 lowering
behavior are mirrored in
`examples/selfhost-core/llvmgen.osty` so the LLVM backend logic is authored in
Osty first. The Go `internal/llvmgen` package includes generated bridge code
from that Osty source for module/function/skeleton rendering. A backend test
transpiles the Osty emitter again and compares its
minimal/scalar/control-flow/booleans/string and skeleton output byte-for-byte
against the production bridge.

These fixtures are deliberately smaller than the current examples. They are
the first target for proving the thin vertical path:

```text
Osty source -> front-end -> internal/ir -> LLVM IR -> native binary
```

Phase 18 adds the first host toolchain driver for this path: supported smoke
fixtures can now go through `clang` from textual `.ll` to object/binary on
hosts with that toolchain installed. Phase 19 adds the first executable parity
gate for the table above. The case list and expected stdout are owned by
`examples/selfhost-core/llvmgen.osty` via `llvmSmokeExecutableCorpus`; the Go
test is only the host shim that runs the generated native binaries. The corpus
still stays in `llvm-smoke` until CI policy promotes these gates to required
jobs.

Phase 20 moves unsupported/backend-capability diagnostics into the same
self-hosted backend core. `use go` remains a `go-only` fixture class for native
LLVM and receives the Osty-authored `LLVM001 go-only` diagnostic policy instead
of Go-owned wording.

Phase 21 extends that policy into a taxonomy for unsupported LLVM source
shapes. The category codes and hints for source layout, type-system, statement,
expression, control-flow, call, name, and function-signature failures are owned
by `examples/selfhost-core/llvmgen.osty`; the Go bridge only detects the
current AST situation and asks the generated policy for the diagnostic summary.

Phase 22 routes the successful scalar instruction builders through that same
self-hosted core. The Go bootstrap bridge still walks the existing AST while the
LLVM strings for return, println, arithmetic, comparison, calls, if branches,
loads, stores, mutable locals, and range loops are emitted by generated
functions from `examples/selfhost-core/llvmgen.osty`.

Phase 23 adds the first `String` vertical path without moving string semantics
back into Go. `examples/selfhost-core/llvmgen.osty` owns the module-level string
constant shape, newline terminator, `printf(ptr @.strN)` call, and the string
smoke fixture entry. The Go bridge only classifies the AST literal as the
current conservative subset: non-raw, non-triple, non-interpolated printable
ASCII without quotes, backslashes, or control characters.

Phase 24 expands that path to escaped ASCII strings while keeping the LLVM
constant encoding self-hosted. `examples/selfhost-core/llvmgen.osty` now maps
newline, tab, carriage return, quote, and backslash into LLVM C-string escapes
before appending the `println` newline and NUL terminator. The Go bridge still
only rejects source outside the currently supported ASCII subset.

## Promotion Rules

A fixture may move to a stronger class only when the required runtime and
backend features are available.

- `llvm-smoke -> exec`: once LLVM can compile and run it in CI.
- `front-end-only -> exec`: once package emission and runtime dependencies are
  implemented.
- `future-runtime -> exec`: once the relevant stdlib/runtime module is ported.
- `go-only -> exec`: only if a native equivalent feature exists. Otherwise keep
  it Go-only and require an unsupported diagnostic in LLVM mode.

## Phase 2 Done Criteria

- The corpus classes above are documented.
- The first LLVM smoke fixtures exist in the repository.
- The smoke fixtures pass current front-end checks individually.
- Later backend work can refer to this document instead of guessing which
  examples are parity gates.

The follow-up Go transpiler TODO audit is tracked in
[`LLVM_GEN_TODO_AUDIT.md`](./LLVM_GEN_TODO_AUDIT.md).
