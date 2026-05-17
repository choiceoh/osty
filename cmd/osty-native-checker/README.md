# `osty-native-checker` — dual-target build

This directory hosts the bootstrap Go shell (`main.go`) **and** an LLVM-built
Osty entry point (`main.osty`, manifested by `osty.toml`). Two binaries with
matching responsibilities, built from disjoint sources.

## Targets

| target | command | source | status |
|---|---|---|---|
| Go-built | `go build -o /tmp/go-checker ./cmd/osty-native-checker` | `main.go` (~38 LOC) + `internal/selfhost/generated.go` (frozen seed) | production |
| LLVM-built | `OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/` | `main.osty` + manual naive parser | **walking skeleton** (stub CheckResult) |

## Why two targets

The LLVM-built target is the **smallest meaningful unit of true self-hosting**
under the plan tracked in [docs/llvm-selfhost-plan.md](../../docs/llvm-selfhost-plan.md).
The Go-built target remains the production checker until the LLVM-built one
reaches behavior parity (plan §12 M3/M4).

## Current state (2026-05-17)

- ✓ **M1** — LLVM-built binary builds + runs (`OSTY_STAGE0_FALLBACK=1`)
- ✓ **M2 (partial)** — empty-source fixture byte-parity:
  ```
  $ echo '{"source":""}' | /tmp/go-checker
  $ echo '{"source":""}' | ./.osty/out/debug/llvm/osty-native-checker-llvm
  diff: IDENTICAL
  ```
- ⏳ **M3/M4** — multi-fixture / spec corpus / toolchain self-input parity
  blocked by `SPEC_GAPS.md::cross-pkg-module-resolution` (see PR3-C step 3
  design [docs/llvm-selfhost-plan-pr3-c-step3-design.md](../../docs/llvm-selfhost-plan-pr3-c-step3-design.md))

## Build (LLVM-built)

```sh
# from repo root
go build -o .bin/osty ./cmd/osty
OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/

# run
echo '{"source":""}' | ./cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm
```

`OSTY_STAGE0_FALLBACK=1` is still the practical default for this LLVM build
because **normal MIR→LLVM emission goes through `osty-self lir-proto-lower`**
(`internal/backend/llvm.go` `emitLLVMFallback`). On a checkout without a
working `osty-self` cache (or when that subprocess declines the MIR payload),
the dispatcher only proceeds if you opt into the in-process **stage0**
bootstrap emitter (`internal/backend/stage0/`, gated by this env var;
see `internal/backend/bootstrap.go`).

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
  - escape (`\"`, `\n`) 미처리
  - 다른 key 가 `"source"` substring 포함 시 오인지
- 진짜 checker 호출 (`elabFile` etc.) **부재** — `use toolchain.check` 가
  cross-package wall (E0703/E0704) — PR3-C step 3 unlock 대기

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
| (TBD) | PR3-C step 3 (옵션 c) 합의 후 cross-package method-call unlock |

자세한 분기 결정 + 시도 결과 → `docs/llvm-selfhost-plan-pr*.md` 시리즈.
