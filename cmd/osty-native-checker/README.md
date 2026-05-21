# `osty-native-checker` — dual-target build

This directory hosts the bootstrap Go shell (`main.go`) **and** an LLVM-built
Osty entry point (`main.osty`, manifested by `osty.toml`). Two binaries with
matching responsibilities, built from disjoint sources.

## Targets

| target | command | source | status |
|---|---|---|---|
| Go-built | `go build -o /tmp/go-checker ./cmd/osty-native-checker` | `main.go` (~38 LOC) + `internal/selfhost/generated.go` (frozen seed) | production |
| LLVM-built | `.bin/osty build --bootstrap-stage0 --backend llvm cmd/osty-native-checker/` | `main.osty` + `tc.frontCheckSourceToWireJson` | **M4 byte parity — bricks A/B/C all wired (byte offsets, telemetry, sha256 stable IDs)** |

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
- ✓ **M4 brick A (byte offsets + diagnostic line/column)** —
  `tc.frontCheckSourceToWireJson` lexes the input, builds a rune→byte
  table, and threads a `FrontWireMapper` so every `start` / `end` carries
  the same UTF-8 byte offset the Go adapter
  (`internal/selfhost/check_adapter.go::checkNodeOffsets`) emits.
  Diagnostics additionally get `startLine` / `startColumn` / `endLine` /
  `endColumn` from `FrontLexToken.start.line`+`column`.
- ✓ **M4 brick B (`errorsByContext` / `errorDetails` summary telemetry)** —
  `frontWireBuildTelemetry` aggregates every severity-error diagnostic
  into the same shape `selfhostDiagnosticTelemetry`
  (`internal/selfhost/check_telemetry.go`) attaches to `CheckResult.Summary`.
  Bucket key = stable code (or `<error>` fallback); detail key = trimmed
  message + `@L<line>:C<col>` suffix on the lex-aware path. Map keys
  emit in lexicographic order to match Go's `encoding/json`.
- ✓ **M4 brick C (sha256 stable IDs)** — `toolchain/check_stable_id.osty`
  mirrors `EnsureStableIDs` (`internal/selfhost/api/types.go`): every
  record body stamps `id` / `nodeKey` / `typeKey`, instantiations
  additionally stamp `typeArgKeys` / `resultTypeKey`. Hashes are
  SHA256(`prefix\0part1\0part2\0...\0`)[:12] hex with the same content
  tuples the Go adapter uses, so identical wire records always hash to
  identical keys.

With bricks A + B + C wired the LLVM-built and Go-built native checker
emit byte-identical JSON for non-empty inputs (modulo any
non-determinism in token ordering, which is upstream of the wire layer).

## Build (LLVM-built)

```sh
# from repo root
go build -o .bin/osty ./cmd/osty
.bin/osty build --bootstrap-stage0 --backend llvm cmd/osty-native-checker/

# run
echo '{"source":""}' | ./cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm
```

### Bootstrap escape — prebuilt binary slot

The LLVM build path is recursive: producing the LLVM-target checker
requires `osty-self` to be present (so the production `lir-proto-lower`
subprocess can lower MIR), and producing `osty-self` is itself an
`osty build toolchain/` invocation. CI / install-self / cross-worktree
developers can break the recursion by staging a prebuilt LLVM-target
binary outside the worktree and exporting:

```sh
export OSTY_NATIVE_CHECKER_LLVM_BIN="$PWD/.bin/osty-native-checker-llvm"
```

`internal/toolchain.ResolveNativeCheckerLLVM` then returns the env path
without consulting the in-tree build output, mirroring the
`OSTY_SELF_BIN` precedent. Fallback order:

1. `$OSTY_NATIVE_CHECKER_LLVM_BIN` env override (must point at an
   existing file; missing paths are silently ignored to guard against
   stale shell-profile pins per CLAUDE.md).
2. In-tree build at
   `<project>/cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm`.
3. Returns `""` so callers can fall back to the Go-built variant or
   trigger a build explicitly.

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

- ✓ 멀티라인 stdin (`io.readAllStdin()` → `osty_rt_io_read_all_stdin`
  runtime symbol). 줄바꿈된 JSON request 가 통째로 읽힌다.
- ✓ JSON value escape (`\"` / `\\` / `\n` / `\t` / `\r` / `\b` /
  `\f` / `\/` / `\uXXXX` + surrogate pair) 디코딩
  (`extractJsonStringValueAt`).
- ✓ `CheckRequest.package` 모드 — multi-file dispatch 가 `frontExtractPackageFiles`
  로 `{source, name, path, base, sourceFileId}` 메타데이터 전체를 추출하고
  `frontCheckPackageToWireJson` 으로 라우팅. 각 파일의 combined-buffer
  시작 byte / line 을 기록한 `FrontPackageFileTable` 이 와이어 mapper 에
  threading 되어 모든 record 의 `start` / `end` / `startLine` / `endLine`
  이 file-local 좌표로 환산되고, diagnostic 은 추가로 `file` /
  `sourceFileId` 필드 + telemetry suffix `<file>:@L<line>:C<col>` 를 받는다.
- ✓ `PackageCheckInput.imports` cross-package surface — `frontExtractPackageImports`
  가 `imports` 배열을 `FrontPackageImport` 로 디코드 (Functions + Fields +
  Variants + Aliases + TypeDecls + InterfaceExts + RegisterAsIface 전부;
  nested `TypeRepr` 재귀 디코드 포함). `frontInstallImportSurfaces` 가
  `newElabCx` 와 `elabFile` 사이에서 각 alias 를 모듈-like named type
  binding + import alias 마킹 + 모든 exported surface 등록. Go-side
  `selfhostInstallImportSurfaces` (`internal/selfhost/package_adapter.go`)
  와 동등. `use std.io` / `use toolchain as tc` 같은 cross-pkg 참조가
  E0501 / E0703 / E0702 없이 정상 resolve.
- 잔여: 다른 key 가 `"source"` 를 substring 으로 포함하면 오인지
  (key-boundary 엄격 매칭 후속 batch).
- 출력 측은 진짜 checker 호출 — `tc.frontCheckSourceToWireJson`
  (`toolchain/check_json.osty` + `toolchain/check_stable_id.osty`) 가
  lex/parse/check 한 후 wire JSON 으로 직렬화. M4 byte parity 의 세
  brick 모두 wired:
  - byte offsets + diagnostic `startLine` / `startColumn` / `endLine` /
    `endColumn` (brick A)
  - `errorsByContext` / `errorDetails` summary telemetry, sorted key 순
    (brick B)
  - sha256 기반 `id` / `nodeKey` / `typeKey` (+ instantiation 의
    `typeArgKeys` / `resultTypeKey`) stamp (brick C)

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
| [#1938](https://github.com/choiceoh/osty/pull/1938) | **PR3-G semantic correctness** — `tc.frontCheckSourceToWireJson` (`toolchain/check_json.osty`) replaces stub `emptyCheckResultJson` |
| [#1940](https://github.com/choiceoh/osty/pull/1940) | **M4 brick A** — byte offsets + diagnostic line/column (`FrontWireMapper`) |
| [#1942](https://github.com/choiceoh/osty/pull/1942) | **M4 bricks B+C** — `errorsByContext` / `errorDetails` telemetry + sha256 stable IDs (`toolchain/check_stable_id.osty`) |

자세한 분기 결정 + 시도 결과 → `docs/llvm-selfhost-plan-pr*.md` 시리즈.
