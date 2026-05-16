# LLVM self-host plan — PR3-C step 3 design (`generated.go` frozen seed 협상)

> **상태**: 제안 (draft). 합의 후 별도 PR chain.
> **선행**: PR [#1842](https://github.com/choiceoh/osty/pull/1842) (step 1+2 ImportPath data flow), PR [#1844](https://github.com/choiceoh/osty/pull/1844) (step 3 wall 측정).
> **소유**: backend / toolchain / spec.

## 1. 문제 정의

`ResolvedSymbol.ImportPath` 가 채워지지만 **checker 의 method-call dispatch 가 활용 안 함**. dispatch site:

```go
// internal/selfhost/generated.go:41950 (transpiled from toolchain/elab.osty:19712)
ownerName := elabOwnerNameForReceiver(cx.env.tys, lookupRecvTy)
methodName := fieldNode.text
lookedUpSig := checkLookupMethodForReceiver(cx.env, lookupRecvTy, ownerName, methodName)
rawSig := ... // sig.name == "" 면 diagUnknownMethod (E0703)
```

`lookupRecvTy` 가 type repr — package symbol 의 ImportPath 정보 미보유. `elabOwnerNameForReceiver` 도 type → name 만 추출, ResolvedSymbol 의 ImportPath 도달 못 함.

## 2. 옵션 b (post-processing layer) 의 한계

원래 design ([SPEC_GAPS::cross-pkg-module-resolution path #5](../SPEC_GAPS.md)) 의 옵션 b 가 "diagnostic suppress + module method wrapper" — 그러나:

```go
// generated.go:41955-41958 — E0703 emit 직후
if sig.name == "" {
    hint := diagDidYouMean(...)
    cx.env.local.diagnostics = append(..., diagUnknownMethod(...))
    return elabPoisonResult(cx, callNode.start, callNode.end)  // ← poison!
}
```

diagnostic suppress 만으로는 `elabPoisonResult` (poison return type) 가 caller 로 전파 → 그 자체로 invalid program. method-call 의 진짜 lowering (module-scoped sig lookup → real method call emit) 이 필요.

**즉 옵션 b 는 hacky 가 아니라 본질적으로 불가능**. checker logic 자체 변경이 필수.

## 3. 옵션 a — `generated.go` 직접 수정

```go
// generated.go:41928 부근 — checkLookupMethodForReceiver 호출 전후
// Owner 가 package symbol (kind == "package") 이고 ImportPath 있으면 module lookup 분기
if pkgSym := findPackageSymbol(cx.env, ownerName); pkgSym != nil && pkgSym.ImportPath != "" {
    moduleSig := checkLookupMethodInModule(cx.env, pkgSym.ImportPath, methodName)
    if moduleSig.name != "" {
        // module-scoped method call emit
        ...
    }
}
// 기존 dispatch fallthrough
```

**위험**:
- PR #854 frozen seed 결정과 충돌. CLAUDE.md "하지 말 것": "제거된 부트스트랩 Osty→Go 트랜스파일러 재도입 금지"
- `generated.go` 는 frozen seed — 수동 패치 시 다음 regen (만약 부활) 과 drift
- partial regen 옵션 c 가 더 정통

## 4. 옵션 c — spec PR: `PR #854 frozen seed narrow`

CLAUDE.md 의 frozen seed 결정을 narrow:
- `generated.go` 전체 regen 은 여전히 retire
- **그러나 cross-package method-call dispatch site (~5-10 줄) 만 hand-edit 허용**
- toolchain/elab.osty 의 동등 변경을 같은 PR 에 같이 (drift 방지 — 다음 manual sync 시점에 reconcile)

**위험**:
- spec/team 합의 필요 (CLAUDE.md 수정)
- partial regen pattern 이 다른 hand-edit 의 선례 — slippery slope
- 단 cross-package method-call 은 PR3-F 의 진정한 self-host unlock 의 단일 wall — narrow exception 정당성 강함

## 5. 옵션 d (신규) — checker 가 frontend HIR/MIR 단계에서 method lookup 다시

method-call 의 진짜 dispatch 가 elab.osty 가 아닌 hir_lower.osty 또는 mir_generator.osty 에서. 그 단계가 ResolvedSymbol.ImportPath 인지 후 module-scoped lookup.

**검토 필요**:
- HIR / MIR 단계에서 method dispatch 가 일어나는가? (대부분 elab 에서 끝남)
- 만약 그렇다면 transpiled generated.go 의 같은 함수 — 같은 wall

## 6. 가성비 비교

| 옵션 | LOC | spec 변경 | 위험 | 정통성 |
|---|---|---|---|---|
| a | ~50 | no | spec 위반 | low |
| b | n/a | no | **불가능** | n/a |
| c | spec 50 + code 100 | yes (CLAUDE.md) | spec/team 합의 | high |
| d | unknown (검토 필요) | no | unknown | medium |

**권장**: 옵션 c (spec PR + narrow exception). a 는 spec 위반, b 불가능, d 미지.

## 7. 옵션 c 의 sub-PR 분할

```
PR3-C-step3-c-spec: CLAUDE.md / SPEC_GAPS.md 갱신 — partial regen exception
                    (cross-pkg method-call dispatch 만)
PR3-C-step3-c-impl: generated.go method-call dispatch site (~50 LOC) hand-edit
                    + toolchain/elab.osty 동등 변경 (drift 방지)
PR3-C-step3-c-test: cmd/osty-native-checker/main.osty 가 use toolchain.check
                    + tc.frontInvalidTypeRepr() 통과 검증
```

추정 3 sub-PR, 각 단일 fresh session.

## 8. 본 plan §10 갱신

- R9 (신규): cross-pkg method-call dispatch 의 frozen seed wall — spec PR (옵션 c) 또는 partial regen narrow exception 필요
- N12 (신규): generated.go:41950 (transpiled from toolchain/elab.osty:19712) 의 method-call dispatch 분기 추가 — 약 50 LOC

## 9. 다음 단계 (다음 세션 시작점)

옵션 c 의 PR3-C-step3-c-spec 부터:

```sh
git checkout -b pr3-c-step3-c-spec origin/main
# CLAUDE.md 의 "하지 말 것" 섹션에 narrow exception 추가:
# - generated.go 전체 regen 은 여전히 금지
# - 그러나 cross-pkg method-call dispatch site 같은 surgical hand-edit 은 허용
# - 동시에 toolchain/elab.osty 의 동등 변경 의무
# SPEC_GAPS.md::cross-pkg-module-resolution 의 step 3 옵션 c 선택 명시
```

합의 후 PR3-C-step3-c-impl 진입.

## 본 plan completion 의 trajectory

| milestone | 상태 |
|---|---|
| M1 (PR1a builds + runs) | ✓ |
| M2 (L1 byte parity, 진짜 stdin + naive parser) | ✓ |
| M3 (PR3-F 후, L2 spec/positive corpus) | step 3 unlock 후 |
| M4 (L3 toolchain self-input) | M3 후 |

옵션 c 의 3 sub-PR 머지 후 PR3-D/E/F 가 trivial sequencing 으로 풀림. plan 의 6–10 sessions estimate 와 일치.
