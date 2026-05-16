# LLVM self-host plan — PR3 design (`[lib]` crate model)

> **상태**: 제안 (draft). 합의 후 별도 PR chain.
> **선행**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md) §6 PR3, [docs/llvm-selfhost-plan-pr3-attempt.md](llvm-selfhost-plan-pr3-attempt.md) (cross-package wall 확인), [LANG_SPEC_v0.5/](../LANG_SPEC_v0.5/) §package/module.
> **소유**: backend / toolchain / spec.

## 문제 정의

plan §6 PR3 의 진짜 작업 — `cmd/osty-native-checker/main.osty` 가 toolchain 의 `elabFile` / `serializeCheckResult` / `selfhostBuildPackageAst` 등 함수를 호출해 진짜 checker 결과 산출 — 는 단일 PR 작업 아님. **Osty 의 multi-package compilation model 자체가 미성숙**.

[PR3 attempt](llvm-selfhost-plan-pr3-attempt.md) 의 측정:
- `osty.toml [dependencies] toolchain = { path = "../../toolchain" }` resolve 통과
- `use toolchain.check` / `use toolchain.check as tc` 모두 module → type 오해석 (E0703)
- `use toolchain` alone build 통과 단 함수 호출 안 함
- `[bin]` 만 존재, `[lib]` model 미정의

## 본 design doc 의 scope

옵션 1 (`[lib]` crate model 도입) 의 spec + 구현 분할안. **합의 후 별도 PR chain 으로 구현**.

옵션 3 (C runtime wrapper stopgap) 은 별도 — Go runtime 의존이라 self-host 의도 부정. 본 doc 은 정통 path 만.

## 1. spec 변경 — `[lib]` crate model

### 1.1 manifest spec

`osty.toml` 의 `[lib]` 섹션:

```toml
[lib]
path = "lib.osty"  # 또는 dir/main.osty
# name 은 [package].name 그대로 사용
# crate-type 은 항상 "lib" (현재는 [bin] 만 지원)
```

규칙:
- `[lib]` 와 `[bin]` 는 mutually exclusive (currently `[bin]` only).
- `[lib]` 이 정의된 package 는 binary 산출 없음 + dep 로 import 가능.
- entry 파일 = `[lib].path` 또는 default `lib.osty`.

### 1.2 `use <dep>.<symbol>` syntax

```osty
use toolchain.elab.elabFile
use toolchain.check as check
```

규칙:
- `use <dep>` — `[dependencies]` 의 dep name. type 이 아님.
- `<dep>.<module>` — 그 dep 의 file (예: `toolchain/elab.osty` → `toolchain.elab`).
- `<dep>.<module>.<symbol>` — 그 file 의 pub symbol.
- alias `as <ident>` — module 또는 dep 단위.
- 현재 `use std.io` 도 같은 model 아래 작동 (단 `std` 가 prelude-special-cased — 이걸 일반화).

native checker 의 변경:
- module name resolution 단계에서 `<dep>.<module>` 인식 → `Module` type 으로 등록 (현재 type 으로 오해석되는 part 가 여기).
- `<module>.<symbol>` 호출 시 method lookup 이 아니라 module-scoped symbol lookup.

### 1.3 cmd/osty 변경

- `osty build` 가 `[lib]` package 빌드 시 `*.o` + module export 메타데이터만 산출 (binary 없음).
- dep 로 import 한 lib package 의 object file 을 main binary linker 에 wire.
- `--emit lib` flag 가능 (또는 manifest 의 `[lib]` 자체로 자동 결정).

### 1.4 spec 위치

- `LANG_SPEC_v0.5/02-modules-packages.md` (또는 동등) 에 `[lib]` model 추가
- `LANG_SPEC_v0.5/15-toolchain.md` 의 manifest validation 갱신
- `OSTY_GRAMMAR_v0.5.md` 의 use-decl rule 갱신 (`<dep>.<...>` path 명시)

## 2. 구현 분할 (PR chain)

### PR3-A — spec PR

- `LANG_SPEC_v0.5/` + `OSTY_GRAMMAR_v0.5.md` + `SPEC_GAPS.md` 갱신
- 새 진단 코드 예약 (E0?? `[lib]` and `[bin]` mutually exclusive, E0?? unknown module in dep)
- 실제 코드 변경 0

추정: ~200 doc LOC, 1 PR.

### PR3-B — manifest [lib] parsing

- `internal/manifest/manifest.go` 에 `[lib]` section parse + validation
- `osty.toml [lib] path = "..."` + `[bin]` mutually-exclusive 가드
- focused test

추정: ~80 LOC + 5 tests, 1 PR.

### PR3-C — native checker module resolution

- `toolchain/resolve.osty` + `toolchain/elab.osty` 의 cross-package use 처리
- `<dep>.<module>` 가 module 로 등록되고 `<module>.<symbol>` 호출 path
- focused negative test (`use toolchain.check; check.frontInvalidTypeRepr()` 가 통과)

추정: ~150 LOC, 1 PR (Osty self-host 변경).

### PR3-D — toolchain/osty.toml 에 [lib] 추가

- `toolchain/osty.toml` 에 `[lib] path = "lib.osty"` (또는 main.osty 분기)
- `toolchain/lib.osty` 신규 — public API re-export
- `[bin]` 는 별도 `tools/osty-self/` 같은 sub-package 로 분리
- 또는 same package + `[lib]` + `[bin]` 공존 spec 추가

추정: ~100 LOC + refactor, 1 PR.

### PR3-E — cmd/osty 의 lib build path

- `osty build` 의 lib 모드 — `.o` + export 메타 산출
- main binary linker 가 dep lib 의 object file link
- focused test (`cmd/osty-native-checker/` 가 toolchain dep 로 build + 함수 호출 가능)

추정: ~200 LOC, 1 PR.

### PR3-F — cmd/osty-native-checker 의 진짜 checker 호출

PR3-A ~ PR3-E 머지 후:

```osty
use toolchain.elab as elab
use toolchain.check as check
use toolchain.check_adapter as adapter  // selfhost adapters

fn checkSource(source: String) -> String {
    let lexed = ostyLexSource(source)
    let file = adapter.selfhostSemanticAstFile(astParseLexedSource(lexed))
    let cx = elab.newElabCx(file, emptyTyArena())
    check.elabFile(cx)
    let checked = adapter.serializeCheckResult(cx)
    adapter.adaptCheckResult(checked, lexed)
}

fn main() {
    let input = io.readLine()
    let source = naiveExtractSource(input)
    println(checkSource(source))
}
```

검증:
- multiple fixture (empty source, simple fn, struct decl, etc.) byte parity
- plan §12 M2 → M3 진입 (L2 spec/positive 흡수)

추정: ~50 LOC main.osty + N8 (adaptCheckResultWithTokenLayout Osty 이식 ~100 LOC), 1 PR.

## 3. 추정 timeline

| PR | 추정 |
|---|---|
| PR3-A spec | 1–2 sessions |
| PR3-B manifest | 1 session |
| PR3-C native checker | 1–2 sessions |
| PR3-D toolchain refactor | 1 session |
| PR3-E cmd/osty lib | 2 sessions |
| PR3-F real checker call | 1 session |
| **total** | **6–8 sessions** |

각 PR 이 mergeable 단위. 도중 중단 시 partial value (예: PR3-A 만 머지되어도 spec 명확화). plan §12 M2 → M4 (L3) 의 6–10 sessions estimate 와 일치.

## 4. 대안 — 옵션 5 (PR3 자체를 보류)

본 design doc 의 작업이 큰 데 비해 PR3 의 직접 가치 (toolchain 함수 호출) 가 `osty_rt_selfhost_check_source` C wrapper 보다 명확히 우월하지 않다면, PR3 를 stage0 100% cover 이후로 미루고:

- stage0 trajectory 가 16 decline 함수 cover 후 (수 일~수 주)
- `osty install-self --source-bootstrap` 가 작동 → `osty-self` binary 자동 생성
- production path (lir_proto) 가 활성 → `cmd/osty-native-checker/` 가 `osty-self` 의 LIR Proto subprocess 통과
- toolchain 함수 호출이 production path 의 indirect call 로 자동 작동

이 path 의 작업: stage0 100% cover 만. `[lib]` crate spec 없이 진행 가능.

**선택**: 본 design (옵션 1) vs stage0 cover (옵션 5)?

| 측면 | 옵션 1 ([lib] crate) | 옵션 5 (stage0 cover) |
|---|---|---|
| 작업량 | 6–8 sessions | 외부 trajectory (수 일~수 주) |
| self-host 의도 | strong (정통 multi-package) | medium (production path 의존) |
| spec 변경 | yes ([lib] crate model) | no |
| 본 plan 의존 | direct | indirect (외부 trajectory 머지 대기) |
| 진척 가시성 | high (단계 명확) | low (외부 trajectory 진척 측정 어려움) |

## 5. 다음 단계 (다음 세션)

**경로 A (옵션 1)**: PR3-A 부터 시작. spec 작성 단일 doc PR.

**경로 B (옵션 5)**: 본 doc 을 머지하고 plan §10.1 R2 의 stage0 trajectory 진척 측정만 주기적 — 별도 작업 0.

본 design doc 자체는 합의 단계 — 사용자/팀이 경로 A vs B 선택 후 진행.

## 본 plan §10 갱신

- N11: `[lib]` crate model spec — PR3-A ~ PR3-E 의 sub-PR chain (6–8 sessions estimate)
- Q19: 사용자/팀 합의 — 경로 A ([lib] crate 옵션 1) vs 경로 B (stage0 cover 옵션 5)?

## 본 plan completion criteria 의 영향

plan §12 의 M4 (L3 toolchain self-input) 가 진짜 self-host 의미를 가지려면 옵션 1 의 진행이 필요. 옵션 5 는 production path 가 generated.go (Go-built `osty-self`) 에 의존하므로 "true LLVM self-host" 와 거리 있음.

**plan completion 의 strict 의미**: 옵션 1 의 PR3-F 머지 + L1/L2/L3 모든 corpus byte parity. 추정 timeline = 본 plan §6 의 "다음 세션" 으로 시작해 6–10 sessions.

본 plan 의 §"성공 / 실패 선언 기준" 갱신 권장:
- M1 (PR1a) ✓
- M2 (L1 byte parity) ✓ (stub)
- M2.5 (진짜 stdin + naive parser) ✓ (이 세션, PR1c + PR2 옵션 3')
- M3 (옵션 1 PR3-F 또는 옵션 5 외부 trajectory + 진짜 checker 호출)
- M4 (L2/L3 corpus 전체)
