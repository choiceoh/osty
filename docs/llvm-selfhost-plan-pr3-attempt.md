# LLVM self-host plan — PR3 attempt (cross-package wall)

> **상태**: 측정 + revert (이 PR).
> **선행**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md) §6 PR3, PR [#1829](https://github.com/choiceoh/osty/pull/1829) (PR2 manual naive parser).
> **소유**: backend / toolchain.

## 목적

plan §6 PR3 (진짜 checker 호출 — `selfhostBuildPackageAst` / `newElabCx` / `selfhostInstallImportSurfaces` / `elabFile` / `serializeCheckResult` / `adaptCheckResultWithTokenLayout` 5–6 단계 Osty 시퀀스 wire) 의 첫 시도. **Osty 의 cross-package crate dep import 모델 wall** 확인 + 다음 단계 권장.

## Attempt

### 1. `osty.toml` 에 path dep 추가 — 통과

```toml
[dependencies]
toolchain = { path = "../../toolchain" }
```

`OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/`:
```
Resolved 1 dependencies for osty-native-checker v0.1.0
  toolchain 0.1.0	(path+../../toolchain)
Built ...osty-native-checker-llvm (debug)
```

✓ path dep resolve + 빌드 통과 (단 함수 호출 없이).

### 2. `use toolchain.check; check.frontInvalidTypeRepr()` — 실패

```osty
use toolchain.check

fn main() {
    let _t = check.frontInvalidTypeRepr()
    ...
}
```

→ `error[E0703]: no method 'frontInvalidTypeRepr' on type 'check'`. native checker 가 `check` 를 type 으로 잘못 해석.

### 3. `use toolchain.check as tc; tc.frontInvalidTypeRepr()` — 실패

→ 같은 에러 형태: `no method 'frontInvalidTypeRepr' on type 'tc'`. alias 가 type 으로 해석.

### 4. `use toolchain` alone — 통과

```osty
use toolchain
fn main() { println("test") }
```

→ build 통과. 단 toolchain 안 함수 호출 안 함 — symbol 가 실제로 link 됐는지 검증 불가.

## 분석

- **cross-package `use` syntax 의 정확한 형태가 미명세**. `examples/` 와 `toolchain/` 의 모든 `.osty` 가 `use std.*` 만 사용. **사용자 cross-package import 의 정통 예시 0건**.
- `[dependencies]` resolve 는 작동하지만, **`use <dep>.<module>` 가 module 접근이 아니라 type 접근으로 해석**됨. CLAUDE.md A.8 의 `use std.fs` 는 std 가 native prelude 로 등록돼 special-cased 일 가능성.
- `toolchain/osty.toml` 의 `[bin]` 만 정의됨 (`[lib]` 없음). **binary crate 를 dep 로 import 하는 model 미정의**.

## 진짜 wall

PR3 의 의도 (toolchain 의 `elabFile` / `serializeCheckResult` 등 Osty 함수를 LLVM-built `cmd/osty-native-checker` 안에 link) 는 Osty 의 multi-package compilation 모델 자체의 미성숙에 부딪힘:

1. `[lib]` crate vs `[bin]` crate 의 구분 부재 — toolchain 은 `osty-self` binary 만 생산
2. cross-package module access syntax (`<dep>.<module>.<fn>`) 미명세 / 미작동
3. multi-package linker resolution 모델 — 다른 package 의 fn 호출이 같은 binary 에 link 되려면 linker 가 module symbol 을 resolve 해야 하나 osty build 의 처리 path 미확인

## 다음 옵션 (다음 세션 권장)

| # | 접근 | 추정 작업 | 위험 |
|---|---|---|---|
| 옵션 1 | `[lib]` crate model 도입 — `toolchain/osty.toml` 에 `[lib]` 추가 + cross-package access syntax spec | spec 변경 + pkgmgr + manifest validation + cmd/osty multi-output | 수 주 (큰 작업) |
| 옵션 2 | `osty workspace` 멤버 모델 시도 — workspace 안에서 cross-package access 가 다르게 동작할 가능성 | workspace + member spec 측정 + 시도 | 중간 |
| 옵션 3 | **C runtime wrapper**: `osty_rt_selfhost_check_source(const char *src) -> const char *` C 함수가 Go side `selfhost.CheckSourceStructured` 호출 + JSON 반환. Osty 측은 PR1c 옵션 1 패턴으로 그 symbol 호출 | C wrapper (~50 LOC) + Osty stdlib intrinsic decl (~5 LOC) + symbol rewrite (~3 LOC) | **작음 (권장)** |
| 옵션 4 | toolchain 함수 전부를 cmd/osty-native-checker/ 같은 디렉토리로 source copy (workspace inline) | 수십 K LOC source copy + maintenance hell | 거부 |

### 옵션 3 (권장) 의 양상

```c
// internal/backend/runtime/osty_runtime.c 에 추가:
extern void *osty_rt_selfhost_check_source(const char *src);
// 구현: cgo/FFI 로 Go side selfhost.CheckSourceStructured 호출 후 JSON 직렬화 반환
```

```osty
// cmd/osty-native-checker/main.osty 의 다음 단계:
fn main() {
    let input = io.readLine()
    let source = naiveExtractSource(input)
    let result_json = osty_rt_selfhost_check_source(source)  // 직접 호출, symbol rewrite 거침
    println(result_json)
}
```

```go
// internal/mir/lower.go rewriteStdlibSymbolToRuntime 에 추가:
case "std.selfhost.checkSource":
    return "osty_rt_selfhost_check_source"
```

**큰 한계**: Go 측 함수를 C 에서 호출 — CGo 또는 별도 build mode 필요. LLVM-built binary 가 Go runtime 의존 → "true self-host" 아님. **임시 옵션 — toolchain 의 [lib] 모델 정립 전까지의 stopgap**.

### 또는 옵션 5 (가장 솔직)

지금까지 진척만으로 **plan §12 M2 의 의미 있는 단계 달성 (진짜 stdin + naive parser + stub byte parity)**. PR3 의 진짜 작업 (`elabFile` 호출) 은 **별도 design doc + spec 변경 동반하는 큰 작업** — 단일 PR 단위 아님. PR3 의 spec-level 의제를 새 design doc 으로 분리.

## plan §10 갱신

- R8 (신규): Osty 의 cross-package import 모델 미성숙 — `[lib]` crate / workspace member / cross-package access syntax 모두 미명세 또는 미작동
- Q17 (신규): `[lib]` crate model 도입 spec 변경 범위? (pkgmgr / manifest / cmd/osty multi-output 영향)
- Q18 (신규): osty workspace 모델 + member dep — 작동하는 cross-package access 예시 있나?
- N10 (신규): C runtime wrapper (`osty_rt_selfhost_check_source`) — 옵션 3 의 작업 (stopgap, true self-host 아님)

## 이 PR 의 산출물

- `docs/llvm-selfhost-plan-pr3-attempt.md` — PR3 시도 결과 + cross-package wall 분석 + 4 옵션 비교 + 다음 세션 권장
- revert: `cmd/osty-native-checker/osty.toml` (path dep 제거) + `cmd/osty-native-checker/main.osty` (PR2 형태 유지)

## 다음 세션 시작 명령

```sh
git pull origin main
git checkout -b pr3-c-wrapper-stopgap origin/main

# 옵션 3 시도:
# 1. internal/backend/runtime/osty_runtime.c 에 osty_rt_selfhost_check_source 추가
#    (CGo 사용 또는 generated.go 의 C-exportable wrapper)
# 2. internal/mir/lower.go rewriteStdlibSymbolToRuntime 에 case 추가
# 3. cmd/osty-native-checker/main.osty 에서 호출
# 4. build + parity (multiple fixtures)
#
# 또는 더 큰 작업 (옵션 1):
# osty [lib] crate model spec PR — 별도 design doc 작성 후
```
