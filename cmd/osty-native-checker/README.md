# `osty-native-checker` — dual-target build

This directory hosts the bootstrap Go shell (`main.go`) **and** an LLVM-built
Osty entry point (`main.osty`, manifested by `osty.toml`). Two binaries with
matching responsibilities, built from disjoint sources.

## Targets

| target | command | source | status |
|---|---|---|---|
| Go-built | `go build -o /tmp/go-checker ./cmd/osty-native-checker` | `main.go` (~38 LOC) + `internal/selfhost/generated.go` (frozen seed) | production |
| LLVM-built | `.bin/osty build --bootstrap-stage0 --backend llvm cmd/osty-native-checker/` | `main.osty` + `tc.frontCheckSourceToWireJson` | **semantic correctness** (real checker, structural JSON parity; non-empty input still misses byte-offset / stable-ID adapter work) |

## Why two targets

The LLVM-built target is the **smallest meaningful unit of true self-hosting**
under the plan tracked in [docs/llvm-selfhost-plan.md](../../docs/llvm-selfhost-plan.md).
The Go-built target remains the production checker until the LLVM-built one
reaches behavior parity (plan §12 M3/M4).

## Current state (2026-05-19)

- ✓ **M1** — LLVM-built binary builds + runs (`--bootstrap-stage0`)
- ✓ **M2 (partial)** — empty-source fixture byte-parity:
  ```
  $ echo '{"source":""}' | /tmp/go-checker
  $ echo '{"source":""}' | ./.osty/out/debug/llvm/osty-native-checker-llvm
  diff: IDENTICAL
  ```
- ✓ **M3 (semantic correctness)** — `main.osty` now routes through
  `tc.frontCheckSourceToWireJson` (`toolchain/check_json.osty`) instead of
  the prior `emptyCheckResultJson` stub, so non-empty inputs emit the real
  checker summary, typed-node / binding / symbol / instantiation lists and
  structured diagnostics in the same JSON shape as
  `internal/selfhost/api/types.go::CheckResult`.
- ⏳ **M4** — full byte-for-byte parity on non-empty inputs still pending:
  the LLVM-built path keeps token indices in `start` / `end`, skips the
  Go adapter's display line/column + span-ID + sha256 stable-ID stamping
  (`internal/selfhost/check_adapter.go::EnsureStableIDs`), and does not
  populate `errorsByContext` / `errorDetails` telemetry. Tracked under
  `SPEC_GAPS.md::cross-pkg-module-resolution` (see PR3-C step 3 design
  [docs/llvm-selfhost-plan-pr3-c-step3-design.md](../../docs/llvm-selfhost-plan-pr3-c-step3-design.md)).

## Build (LLVM-built)

```sh
# from repo root
go build -o .bin/osty ./cmd/osty
.bin/osty build --bootstrap-stage0 --backend llvm cmd/osty-native-checker/

# run
echo '{"source":""}' | ./cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm
```

`--bootstrap-stage0` is still the practical default for this LLVM build
because **normal MIR→LLVM emission goes through `osty-self lir-proto-lower`**
(`internal/backend/llvm.go` `emitLLVMFallback`). On a checkout without a
working `osty-self` cache (or when that subprocess declines the MIR payload),
the dispatcher only proceeds if you opt into the in-process **stage0**
bootstrap emitter (`internal/backend/stage0/`; see
`internal/backend/bootstrap.go`).

Stage0 **audit coverage** of `toolchain/*.osty` reached **100%** after PR
[#1858](https://github.com/choiceoh/osty/pull/1858) (2026-05-17,
`TestStage0ToolchainAudit`). That milestone does **not** automatically mean
every monomorphized build of `osty-self` or every package is LIR-Proto clean;
`SPEC_GAPS.md` still tracks **audit-pass vs build-pass** gaps and the
`osty-native-checker` production-path wall when stage0 fallback is off.

## main.osty 한계

본 entry 의 의도와 현재 갭:

- `io.readLine()` 으로 stdin 한 줄만 (PR1c, [#1826](https://github.com/choiceoh/osty/pull/1826))
- `strings.indexOf` + `strings.slice` 만 사용한 manual naive JSON source 추출 (PR2, [#1829](https://github.com/choiceoh/osty/pull/1829)):
  - 입력 측 escape (`\"`, `\n`) 미처리
  - 다른 key 가 `"source"` substring 포함 시 오인지
- 출력 측은 진짜 checker 호출 — `tc.frontCheckSourceToWireJson`
  (`toolchain/check_json.osty`) 가 lex/parse/check 한 후 wire JSON 으로
  직렬화. 단:
  - `start` / `end` 가 token index (Go adapter 의 byte-offset 변환 미복제)
  - `EnsureStableIDs` 의 sha256 stable ID 미스탬프 — `omitempty` 로 인해 누락만 발생, 잘못된 값은 아님
  - `errorsByContext` / `errorDetails` summary telemetry 미산출
  - 모두 `SPEC_GAPS.md::cross-pkg-module-resolution` trajectory 의 M4 byte parity 항목

## main.go vs main.osty co-existence

`osty build` 가 `osty.toml` + `.osty` 만 보고 `.go` 무시 — 같은 디렉토리에
공존 안전. PR1 readiness spike (Q6, [#1819](https://github.com/choiceoh/osty/pull/1819)) 에서 검증.

## Trajectory

| PR | milestone |
|---|---|
| [#1816](https://github.com/choiceoh/osty/pull/1816) | **M1** stub builds + runs |
| [#1826](https://github.com/choiceoh/osty/pull/1826) | **🎉 PR1c** 진짜 stdin (`std.io.readLine → osty_rt_io_read_line` MIR symbol rewrite) |
| [#1829](https://github.com/choiceoh/osty/pull/1829) | **PR2** manual naive parser |
| [#1842](https://github.com/choiceoh/osty/pull/1842) | **PR3-C-impl-go step 1+2** ResolvedSymbol.ImportPath post-processing |
| [#1890](https://github.com/choiceoh/osty/pull/1890) | **PR3-E/F activation** — cross-pkg method-call dispatch arm |
| (this branch) | **PR3-G semantic correctness** — `tc.frontCheckSourceToWireJson` (`toolchain/check_json.osty`) replaces stub `emptyCheckResultJson` |
| (TBD) | M4 byte parity — port `internal/selfhost/check_adapter.go` (byte offsets + stable IDs + telemetry) to Osty |

자세한 분기 결정 + 시도 결과 → `docs/llvm-selfhost-plan-pr*.md` 시리즈.
