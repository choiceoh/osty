# LLVM Phase 1 Baseline

기준일: 2026-04-16 Asia/Seoul
기준 commit: `0eb6ea1`
목적: LLVM backend 이주 초기에 기록한 historical baseline 이다. 당시 Go
backend와 관련 CLI 경로의 실제 동작을 고정해 두었고, 이후 LLVM parity 작업에서
"그 시점 구현이 이미 하던 것"과 "그 시점 구현도 아직 못 하던 것"을 구분하기 위한
기준선으로 사용했다.

## Scope

이번 기준선은 25단계 LLVM 이주 로드맵의 Phase 1, "현재 Go 백엔드 기준선
고정"에 해당한다.

확인한 표면은 다음과 같다.

- `osty gen`: Go source emission
- `osty build`: manifest/profile-aware Go build path
- `osty run`: generated Go execution path
- `osty test`: test harness path
- `osty check`: front-end package/workspace validation
- `go test -short ./...`: repository test-suite health signal

## Command Results

| Area | Command | Result | Notes |
|---|---|---|---|
| CLI build | `go build -o .bin/osty ./cmd/osty` | pass | Baseline CLI built successfully. Temporary `.bin/` was removed after checks. |
| calc front-end | `osty check examples/calc` | pass | No diagnostics. |
| calc tests | `osty test examples/calc` | fail | Fails in type-checking before execution. See failure details below. |
| concurrency gen | `osty gen -o /tmp/osty-phase1-baseline/concurrency.go examples/concurrency/main.osty` | pass | Generated 209 lines of Go. |
| concurrency run | `osty run` from `examples/concurrency` | pass | stdout matched expected sample output. |
| concurrency build | `osty build examples/concurrency` | pass | Built `examples/concurrency/.osty/out/debug/concurrency-demo`. |
| FFI run | `osty run` from `examples/ffi` | pass | Go FFI example executed successfully. |
| FFI build | `osty build examples/ffi` | pass | Built `examples/ffi/.osty/out/debug/ffi-demo`. |
| workspace build | `osty build examples/workspace` | pass | Virtual workspace root completed successfully; no root binary emitted. |
| workspace check | `osty check examples/workspace` | pass | No diagnostics. |
| repository short tests | `go test -short ./...` | fail/interrupted | Multiple failing packages were observed; command was stopped after long-running packages continued. See below. |

## Observed Runtime Output

`examples/concurrency`:

```text
sum=39
grouped=61
```

`examples/ffi`:

```text
OSTYOSTY
ffi ok
```

## Known Failing Baseline Items

### `osty test examples/calc`

Current result:

```text
error[E0700]: type mismatch: expected `Result<Int, ()>`, got `Result<Int, CalcError>`
 --> examples/calc/lib_test.osty:54:25
```

Impact:

- The calc package itself front-end checks cleanly.
- The test harness path is not a clean Go-backend parity gate today.
- Do not use `examples/calc` as an LLVM `osty test` parity gate until this
  checker/testgen expectation is fixed or the fixture is reclassified.

### `go test -short ./...`

The command was run to sample repository health. It was not a clean baseline.
Observed failures before interruption:

- `internal/check`
  - `TestCheck_DefaultArgMustBeLiteral`: expected `E0762`, got `E0106`
  - `TestCheck_KeywordArgOK`: keyword/default argument path emitted `E0732` and
    `E0701` diagnostics where the test expected none
- `internal/docgen`
  - `TestFnSignature`: default argument value was missing from rendered
    signature
- `internal/lint`
  - `TestLint_UnusedMutHasFix`
  - `TestLint_UnusedMutTopLevelHasFix`
  - `TestLint_Fixes_RoundTripThroughParser/unused_mut_stmt`

Observed passing packages before interruption included:

- `cmd/codesdoc`
- `cmd/osty`
- `internal/ci`
- `internal/diag`
- `internal/examples`
- `internal/format`
- `internal/gen` (historical — later `internal/bootstrap/gen`; removed)
- `internal/ir`
- `internal/lexer`
- `internal/lockfile`

Impact:

- LLVM migration work should not assume the full short suite is green.
- Phase 2+ changes should avoid hiding these existing failures.
- If CI requires a clean short suite, fix or explicitly quarantine these
  failures before using it as an LLVM gate.

## Fixture Classification

| Fixture | Current Go backend status | LLVM migration classification |
|---|---|---|
| `examples/concurrency` | `run` and `build` pass | Go backend executable baseline; LLVM parity later, after runtime/concurrency work |
| `examples/ffi` | `run` and `build` pass | Go backend executable baseline; LLVM should treat `use go` as Go-only until native FFI exists |
| `examples/calc` | `check` passes, `test` fails | Front-end baseline only for now; not a test-runner parity gate |
| `examples/workspace` | `check` and root `build` pass | Workspace front-end/orchestration baseline; no root binary emitted |
| `word_freq.osty` | not exercised in this pass | Candidate for later real-world corpus once stdlib/runtime parity is clearer |

## Phase 1 Conclusions

- The current Go backend can successfully execute and build the concurrency and
  Go FFI examples.
- The front-end path is clean for `examples/calc` and `examples/workspace`.
- The current test-runner fixture `examples/calc` is not usable as a clean
  backend parity gate without follow-up work.
- The repository short test suite is currently not green, independent of LLVM
  work.
- LLVM's first executable smoke tests should be new or carefully selected
  scalar/control-flow fixtures rather than examples that require Go FFI,
  testgen, or concurrency runtime parity.

## Next Phase

Phase 2 is now tracked in [`LLVM_BACKEND_CORPUS.md`](./LLVM_BACKEND_CORPUS.md).
It covers:

1. Create the backend parity corpus file/list.
2. Mark each fixture as `exec`, `front-end-only`, `go-only`, or
   `future-runtime`.
3. Add the first minimal LLVM smoke candidates that avoid stdlib, Go FFI, and
   concurrency.
