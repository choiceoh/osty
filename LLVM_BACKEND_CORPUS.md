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
| `examples/selfhost-core` | `front-end-only` | Not sampled in Phase 1 | Self-hosting front-end baseline. LLVM 초기 executable gate에서 제외한다. |
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
| `testdata/backend/llvm_smoke/string_let_print.osty` | `stored string\n` | Phase 25 LLVM IR: immutable local `String` value bound from a literal and printed through an identifier |
| `testdata/backend/llvm_smoke/string_return_print.osty` | `from function\n` | Phase 27 LLVM IR: `String` return type lowered to `ptr` and printed from a function call |
| `testdata/backend/llvm_smoke/string_param_print.osty` | `param string\n` | Phase 28 LLVM IR: `String` parameter and argument passing through helper calls |
| `testdata/backend/llvm_smoke/string_mut_print.osty` | `after\n` | Phase 29 LLVM IR: mutable local `String` slot, assignment, load, and print |
| `testdata/backend/llvm_smoke/struct_field_print.osty` | `42\n` | Phase 30 LLVM IR: named struct type definition, aggregate literal via `insertvalue`, and field reads via `extractvalue` |
| `testdata/backend/llvm_smoke/struct_return_print.osty` | `42\n` | Phase 31 LLVM IR: simple struct value returned from a helper function and read in `main` |
| `testdata/backend/llvm_smoke/struct_param_print.osty` | `42\n` | Phase 32 LLVM IR: simple struct value passed as a function parameter |
| `testdata/backend/llvm_smoke/struct_mut_print.osty` | `42\n` | Phase 33 LLVM IR: mutable struct local slot, whole-value assignment, load, and field read |
| `testdata/backend/llvm_smoke/enum_variant_print.osty` | `42\n` | Phase 34 LLVM IR: payload-free enum variants lower to Osty-owned `i64` tag values and compare in an `if` |
| `testdata/backend/llvm_smoke/enum_return_print.osty` | `42\n` | Phase 35 LLVM IR: payload-free enum tag values cross function return boundaries |
| `testdata/backend/llvm_smoke/enum_param_print.osty` | `42\n` | Phase 36 LLVM IR: payload-free enum tag values cross function parameter boundaries |
| `testdata/backend/llvm_smoke/enum_mut_print.osty` | `42\n` | Phase 37 LLVM IR: mutable enum local slot, whole-value assignment, load, and comparison |
| `testdata/backend/llvm_smoke/enum_payload_print.osty` | `42\n` | Phase 42 LLVM IR: single-Int payload enum variant constructor path (`%Maybe = type { i64, i64 }`) |
| `testdata/backend/llvm_smoke/enum_payload_return_print.osty` | `42\n` | Phase 43 LLVM IR: payload enum return boundary with match-based payload extraction |
| `testdata/backend/llvm_smoke/enum_payload_param_print.osty` | `42\n` | Phase 44 LLVM IR: payload enum parameter boundary with `Some(x)` match binding |
| `testdata/backend/llvm_smoke/enum_payload_mut_print.osty` | `42\n` | Phase 45 LLVM IR: payload enum mutable local assignment and payload binding in `match` |
| `testdata/backend/llvm_smoke/float_print.osty` | `42.000000\n` | Phase 46 LLVM IR: Float literal + `println` path (`double` subset) |
| `testdata/backend/llvm_smoke/float_arithmetic_print.osty` | `42.000000\n` | Phase 47 LLVM IR: Float arithmetic (`+`, `-`, `*`, `/`) smoke |
| `testdata/backend/llvm_smoke/float_return_print.osty` | `42.000000\n` | Phase 48 LLVM IR: Float return boundary |
| `testdata/backend/llvm_smoke/float_param_print.osty` | `42.000000\n` | Phase 49 LLVM IR: Float parameter boundary |
| `testdata/backend/llvm_smoke/float_mut_print.osty` | `42.000000\n` | Phase 50 LLVM IR: Float mutable local assignment |
| `testdata/backend/llvm_smoke/float_compare_print.osty` | `42.000000\n` | Phase 51 LLVM IR: Float comparison in value-position `if` |
| `testdata/backend/llvm_smoke/float_struct_print.osty` | `42.000000\n` | Phase 52 LLVM IR: Float field read from simple struct aggregate |
| `testdata/backend/llvm_smoke/float_enum_payload_print.osty` | `42.000000\n` | Phase 53 LLVM IR: Float payload enum smoke (`Full(Float)`) |
| `testdata/backend/llvm_smoke/float_payload_return_print.osty` | `42.000000\n` | Phase 54 LLVM IR: Float payload enum return boundary |
| `testdata/backend/llvm_smoke/float_payload_param_print.osty` | `42.000000\n` | Phase 55 LLVM IR: Float payload enum parameter boundary |
| `testdata/backend/llvm_smoke/float_payload_mut_print.osty` | `42.000000\n` | Phase 56 LLVM IR: Float payload enum mutable local and `match` |
| `testdata/backend/llvm_smoke/float_payload_reversed_match_print.osty` | `42.000000\n` | Phase 57 LLVM IR: Float payload enum reversed two-arm `match` |
| `testdata/backend/llvm_smoke/float_payload_wildcard_print.osty` | `42.000000\n` | Phase 58 LLVM IR: Float payload enum wildcard arm (`_`) in `match` |
| `testdata/backend/llvm_smoke/string_payload_return_print.osty` | `payload string\n` | Phase 59 LLVM IR: String payload enum return boundary |
| `testdata/backend/llvm_smoke/string_payload_param_print.osty` | `payload string\n` | Phase 60 LLVM IR: String payload enum parameter boundary |
| `testdata/backend/llvm_smoke/string_payload_mut_print.osty` | `payload string\n` | Phase 61 LLVM IR: String payload enum mutable local and `match` |
| `testdata/backend/llvm_smoke/string_payload_reversed_match_print.osty` | `payload string\n` | Phase 62 LLVM IR: String payload enum reversed two-arm `match` |
| `testdata/backend/llvm_smoke/string_payload_wildcard_print.osty` | `payload string\n` | Phase 63 LLVM IR: String payload enum wildcard arm (`_`) in `match` |
| `testdata/backend/llvm_smoke/int_if_expr_print.osty` | `42\n` | Phase 64 LLVM IR: value-position `if` expression over `Int` |
| `testdata/backend/llvm_smoke/string_if_expr_print.osty` | `chosen string\n` | Phase 65 LLVM IR: value-position `if` expression over `String` |
| `testdata/backend/llvm_smoke/float_if_expr_print.osty` | `42.000000\n` | Phase 66 LLVM IR: value-position `if` expression over `Float` |
| `testdata/backend/llvm_smoke/bool_param_return_print.osty` | `42\n` | Phase 67 LLVM IR: `Bool` parameter and return boundary |
| `testdata/backend/llvm_smoke/int_range_exclusive_print.osty` | `21\n` | Phase 68 LLVM IR: exclusive `Int` range loop |
| `testdata/backend/llvm_smoke/int_unary_print.osty` | `42\n` | Phase 69 LLVM IR: unary `Int` expression lowering |
| `testdata/backend/llvm_smoke/int_modulo_print.osty` | `42\n` | Phase 70 LLVM IR: `Int` modulo lowering |
| `testdata/backend/llvm_smoke/struct_string_field_print.osty` | `struct string\n` | Phase 71 LLVM IR: `String` field read from simple struct aggregate |
| `testdata/backend/llvm_smoke/struct_bool_field_print.osty` | `42\n` | Phase 72 LLVM IR: `Bool` field read from simple struct aggregate |
| `testdata/backend/llvm_smoke/bool_mut_print.osty` | `42\n` | Phase 73 LLVM IR: mutable `Bool` local slot and assignment |

The Phase 10 skeleton behavior and the Phase 12-15 plus Phase 23-63 lowering
behavior are mirrored in
`examples/selfhost-core/llvmgen.osty` so the LLVM backend logic is authored in
Osty first. The Go `internal/llvmgen` package includes generated bridge code
from that Osty source for module/function/skeleton rendering. A backend test
transpiles the Osty emitter again and compares its
minimal/scalar/control-flow/booleans/string/struct/enum/Float and skeleton
output byte-for-byte against the production bridge.

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
constant shape, `printf` call, and the string smoke fixture entry. The Go
bridge only classifies the AST literal as the current conservative subset:
non-raw, non-triple, non-interpolated ASCII.

Phase 24 expands that path to escaped ASCII strings while keeping the LLVM
constant encoding self-hosted. `examples/selfhost-core/llvmgen.osty` now maps
newline, tab, carriage return, quote, and backslash into LLVM C-string escapes
before appending the NUL terminator. The Go bridge still only rejects source
outside the currently supported ASCII subset.

Phase 25 promotes those string constants into the first local String value path.
String literals lower to `ptr` values through generated Osty helpers, so an
immutable `let msg = "..."` can bind the value and `println(msg)` can print the
identifier without adding Go-owned LLVM instruction strings.

Phase 26 separates String values from `println` formatting. String literals now
produce NUL-terminated constants without a baked-in newline, and
`llvmPrintlnString` uses the Osty-owned `@.fmt_str` format constant to add the
line ending at print time. This keeps local String values usable for later
non-print lowering phases.

Phases 27-29 expand that corrected String value shape across simple function
boundaries and mutable locals. The bridge now lowers the `String` type to
`ptr`, so String-returning functions, String parameters, and mutable String
slots can reuse the existing self-hosted call, return, load, store, and print
builders.

Phases 30-33 add the first value-aggregate struct path. Top-level non-generic
struct declarations lower to named LLVM types, struct literals are assembled
with the Osty-owned `insertvalue` builder, field reads use the Osty-owned
`extractvalue` builder, and simple struct values can cross return/parameter
boundaries or live in mutable local slots. The Go bridge still only collects
declaration field order and source expression shape before calling generated
self-hosted helpers.

Phases 34-37 add the first payload-free enum path. Bare variants lower to
Osty-owned `i64` tag values, so equality, if branches, function return/parameter
boundaries, and mutable local slots can execute before the later tagged-payload
ABI and match lowering work. The initial smoke fixtures use unqualified variant
names because the self-hosted checker already supports that form.

Phases 38-41 extend that same path to payload-free enum match expressions. The
next four smoke fixtures stay intentionally tiny and all print `42\n`:

| Path | Expected stdout | Coverage |
|---|---|---|
| `testdata/backend/llvm_smoke/enum_match_print.osty` | `42\n` | Phase 38 LLVM IR: payload-free enum match expression in `main` |
| `testdata/backend/llvm_smoke/enum_match_return_print.osty` | `42\n` | Phase 39 LLVM IR: payload-free enum match expression crossing a function return boundary |
| `testdata/backend/llvm_smoke/enum_match_param_print.osty` | `42\n` | Phase 40 LLVM IR: payload-free enum match expression crossing a function parameter boundary |
| `testdata/backend/llvm_smoke/enum_match_mut_print.osty` | `42\n` | Phase 41 LLVM IR: payload-free enum match expression over a mutable local slot |

Phases 42-45 add a single-`Int` payload enum smoke subset with a fixed ABI shape:
`%Maybe = type { i64, i64 }`, where element `0` is tag and element `1` is
`Int` payload.

| Path | Expected stdout | Coverage |
|---|---|---|
| `testdata/backend/llvm_smoke/enum_payload_print.osty` | `42\n` | Phase 42 LLVM IR: single-Int payload enum constructor in `main` |
| `testdata/backend/llvm_smoke/enum_payload_return_print.osty` | `42\n` | Phase 43 LLVM IR: payload enum crossing function return and extracting payload in `match` |
| `testdata/backend/llvm_smoke/enum_payload_param_print.osty` | `42\n` | Phase 44 LLVM IR: payload enum crossing function parameter and `Some(x)` binding |
| `testdata/backend/llvm_smoke/enum_payload_mut_print.osty` | `42\n` | Phase 45 LLVM IR: payload enum mutable local slot and match payload binding after assignment |

Phase 46-63 expands the `Float` and `String` payload smoke path. `Float32`/`Float64`
policy is explicitly deferred to a later phase.

Qualified constructor/pattern forms (for example `FloatMaybe.Full` / `FloatMaybe.Full(x)`)
remain for a later phase because the current self-host checker does not pass them
in this bundle.

| Path | Expected stdout | Coverage |
|---|---|---|
| `testdata/backend/llvm_smoke/float_print.osty` | `42.000000\n` | Phase 46 LLVM IR: Float literal + `println` path (`double` subset) |
| `testdata/backend/llvm_smoke/float_arithmetic_print.osty` | `42.000000\n` | Phase 47 LLVM IR: Float arithmetic (`+`, `-`, `*`, `/`) smoke |
| `testdata/backend/llvm_smoke/float_return_print.osty` | `42.000000\n` | Phase 48 LLVM IR: Float return boundary |
| `testdata/backend/llvm_smoke/float_param_print.osty` | `42.000000\n` | Phase 49 LLVM IR: Float parameter boundary |
| `testdata/backend/llvm_smoke/float_mut_print.osty` | `42.000000\n` | Phase 50 LLVM IR: Float mutable local assignment |
| `testdata/backend/llvm_smoke/float_compare_print.osty` | `42.000000\n` | Phase 51 LLVM IR: Float comparison in value-position `if` |
| `testdata/backend/llvm_smoke/float_struct_print.osty` | `42.000000\n` | Phase 52 LLVM IR: Float field read from simple struct aggregate |
| `testdata/backend/llvm_smoke/float_enum_payload_print.osty` | `42.000000\n` | Phase 53 LLVM IR: Float payload enum smoke (`Full(Float)`) |
| `testdata/backend/llvm_smoke/float_payload_return_print.osty` | `42.000000\n` | Phase 54 LLVM IR: Float payload enum return boundary |
| `testdata/backend/llvm_smoke/float_payload_param_print.osty` | `42.000000\n` | Phase 55 LLVM IR: Float payload enum parameter boundary |
| `testdata/backend/llvm_smoke/float_payload_mut_print.osty` | `42.000000\n` | Phase 56 LLVM IR: Float payload enum mutable local and `match` |
| `testdata/backend/llvm_smoke/float_payload_reversed_match_print.osty` | `42.000000\n` | Phase 57 LLVM IR: Float payload enum reversed two-arm `match` |
| `testdata/backend/llvm_smoke/float_payload_wildcard_print.osty` | `42.000000\n` | Phase 58 LLVM IR: Float payload enum wildcard arm (`_`) in `match` |
| `testdata/backend/llvm_smoke/string_payload_return_print.osty` | `payload string\n` | Phase 59 LLVM IR: String payload enum return boundary |
| `testdata/backend/llvm_smoke/string_payload_param_print.osty` | `payload string\n` | Phase 60 LLVM IR: String payload enum parameter boundary |
| `testdata/backend/llvm_smoke/string_payload_mut_print.osty` | `payload string\n` | Phase 61 LLVM IR: String payload enum mutable local and `match` |
| `testdata/backend/llvm_smoke/string_payload_reversed_match_print.osty` | `payload string\n` | Phase 62 LLVM IR: String payload enum reversed two-arm `match` |
| `testdata/backend/llvm_smoke/string_payload_wildcard_print.osty` | `payload string\n` | Phase 63 LLVM IR: String payload enum wildcard arm (`_`) in `match` |

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

The historical Go transpiler TODO audit is tracked in
[`LLVM_GEN_TODO_AUDIT.md`](./LLVM_GEN_TODO_AUDIT.md).
