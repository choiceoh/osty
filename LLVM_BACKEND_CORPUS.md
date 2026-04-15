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
| `testdata/backend/llvm_smoke/scalar_arithmetic.osty` | `42\n` | primitive integers, function call, arithmetic, if/else |
| `testdata/backend/llvm_smoke/control_flow.osty` | `15\n` | mutable locals, inclusive range loop, accumulator update |
| `testdata/backend/llvm_smoke/booleans.osty` | `7\n` | bool expressions, comparison, logical operators, conditional return |

These fixtures are deliberately smaller than the current examples. They are
the first target for proving the thin vertical path:

```text
Osty source -> front-end -> internal/ir -> LLVM IR -> native binary
```

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
