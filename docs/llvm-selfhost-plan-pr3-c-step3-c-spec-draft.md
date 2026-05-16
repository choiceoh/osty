# LLVM self-host plan — PR3-C step 3 옵션 c spec draft

> **상태**: spec draft (사용자/팀 검토 대기). **본 PR 은 CLAUDE.md 미수정** — 실제 spec 변경은 합의 후 별도 PR.
> **선행**: [docs/llvm-selfhost-plan-pr3-c-step3-design.md](llvm-selfhost-plan-pr3-c-step3-design.md), PR [#1847](https://github.com/choiceoh/osty/pull/1847) (옵션 d + 우회 use form 측정 — 우회 path 없음 확인).
> **소유**: spec / backend / toolchain.

## 1. 합의 필요 사항

LLVM self-host trajectory 의 cross-package method-call dispatch (PR3-C step 3) 가 단일 unlock path = 옵션 c (spec narrow exception). CLAUDE.md 의 "하지 말 것" 섹션의 한 줄을 narrow.

## 2. CLAUDE.md 의 현재 표현

```
## 하지 말 것

- Osty로 작성 가능한 로직을 Go로 새로 작성
- 제거된 부트스트랩 Osty→Go 트랜스파일러 재도입 (`internal/selfhost/generated.go` 는 동결된 시드; 재생성 경로 없음)
- ...
```

핵심 제약: **`internal/selfhost/generated.go` 는 동결된 시드; 재생성 경로 없음**

## 3. Narrow exception 의 spec 표현 (draft)

```
- 제거된 부트스트랩 Osty→Go 트랜스파일러 재도입 (`internal/selfhost/generated.go`
  는 동결된 시드; 재생성 경로 없음).
  - **예외 (LLVM self-host critical path)**: cross-package method-call
    dispatch site 같은 **surgical hand-edit** 은 허용. 단:
    1. 단일 PR 에서 `toolchain/elab.osty` 의 동등 변경 의무 (drift 방지)
    2. 영향 범위 한 함수 내 또는 한 dispatch arm (~50 LOC) 만
    3. PR description 에 진단 코드 + LLVM self-host plan 의 단계 명시
       (예: "PR3-C step 3, E0703 dispatch, cross-pkg method lookup")
    4. 새 dispatch arm 추가만 — 기존 path 변경 금지 (regression 회피)
    5. cross-pkg-module-resolution gap (SPEC_GAPS.md) 의 trajectory
       완성 시 narrow exception 자체 retire — full regen 모델 재고
```

## 4. Narrow 의 정당성

- 옵션 a (generated.go 전체 수정): spec 위반, slippery slope
- 옵션 b (post-processing diagnostic suppress): **불가능** (elabPoisonResult 가 caller 로 전파)
- 옵션 c (narrow exception): **유일한 정통 path** — frozen seed 정책의 본질 (Osty→Go 트랜스파일러 재도입 방지) 은 유지하면서 단일 dispatch arm 만 surgical edit
- 옵션 d (HIR/MIR method lookup): 같은 wall — elab dispatch 가 단일 root
- 우회 use form 3 종: 모두 같은 wall (PR [#1847](https://github.com/choiceoh/osty/pull/1847))

## 5. Slippery slope 방지

narrow exception 의 자기-제한 메커니즘:

1. PR description 명시 의무 — 진단 코드 + 단계 cross-link 으로 매 hand-edit 의 정당성 검증
2. `toolchain/elab.osty` 동등 변경 의무 — Osty source 와 Go seed 의 drift 방지 + 다음 manual sync 시점 reconcile
3. 영향 범위 한 함수 / 한 dispatch arm — 큰 변경 시 합의 재요청
4. trajectory 완성 시 narrow exception 자체 retire — 영구적 예외 아님

## 6. 합의 요청 — 사용자 / 팀

위 §3 의 narrow exception 표현이 다음 두 axis 에서 합리적인가?

| axis | 평가 |
|---|---|
| frozen seed 정책의 본질 보존 | yes — 전체 regen 금지 유지 |
| LLVM self-host trajectory unlock | yes — step 3 wall 해소 후 PR3-D/E/F sequencing |
| Slippery slope 위험 | low (4 자기-제한) |
| 정책 복잡성 증가 | low (단일 예외 + 자기-제한 + retire 조건 명시) |

## 7. 합의 후 다음 단계

1. **PR3-C-step3-c-spec-merge**: 본 doc 의 §3 을 CLAUDE.md 의 "하지 말 것" 섹션에 마이그레이션. SPEC_GAPS::cross-pkg-module-resolution 의 옵션 c 선택 명시.
2. **PR3-C-step3-c-impl**: `generated.go:41950` 의 method-call dispatch 에 module-scoped lookup 분기 추가 (~50 LOC) + `toolchain/elab.osty:19712` 동등 변경.
3. **PR3-C-step3-c-test**: `cmd/osty-native-checker/main.osty` 가 `use toolchain.check as tc; let _ = tc.frontInvalidTypeRepr()` 통과 검증.

각 sub-PR 단일 fresh session. step 3 unlock 후 PR3-D/E/F (cmd/osty 의 lib build path + 진짜 checker 호출 + corpus parity) trivial sequencing.

## 8. 본 PR 의 산출물

- `docs/llvm-selfhost-plan-pr3-c-step3-c-spec-draft.md` (이 문서) — spec 변경 합의 draft
- CLAUDE.md 미수정 (사용자/팀 검토 대기)

## 9. 본 PR 머지 후 trajectory

머지 = "spec draft 가 검토 대기" 의 의미. 실제 CLAUDE.md 변경은:
- 사용자/팀이 본 doc §3 검토
- 합의 시 PR3-C-step3-c-spec-merge 진행
- 비-합의 시 본 doc 의 §3 표현 수정 또는 옵션 c 자체 거부 → 다른 path 검토 (옵션 a 수용 또는 PR3 자체 보류)
