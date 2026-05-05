# B2 — Stage0 coverage audit + rewrite demonstration

> **상태**: 정찰 + 한 함수 시범 (이 PR).
> **연관**: `docs/osty_self_b1_findings.md` (B1 — 부트스트랩 갭),
> `docs/osty_self_bootstrap_design.md` (P0–P15 stage0 동결).
> **소유**: backend / toolchain.

## 1. 목표

B1 이 발견한 사실: stage0 (P0–P15) 가 toolchain 의 MIR 패턴을
cover 하지 못 함. B2 는 그 갭의 **양적 크기**와 **수정 패턴**을
실측한다.

## 2. 측정 결과

`scripts/audit-stage0-coverage.sh` 가 toolchain/*.osty (테스트 제외, 97
파일, 7963 함수) 를 스캔한 결과:

| 패턴 | 건수 | 메모 |
|---|---|---|
| `match` expressions | 418 | 가장 큰 갭. if-else 체인으로 재작성 |
| `?` propagations | 109 | 명시적 변수 + early return 으로 재작성 |
| closures (`\|x\|`) | 6 | helper 함수로 lift 가능 |
| generic fns | 0 | **monomorphization 차원 갭 없음** |
| **합계 non-stage0** | **533** | |

생성형 코드 한 줄에 약 5–10 분의 신중한 재작성 + 회귀 테스트가
든다고 가정하면 **50–100 시간** 의 기계적 작업.

generic 함수가 0 인 것은 의외의 호재 — toolchain 은 이미
monomorphized 형태로 작성되어 있어 P16+ 의 가장 어려운 항목 하나가
저절로 해결.

## 3. 시범 재작성 (이 PR)

가장 단순한 케이스 — payload 없는 enum 위에서의 dispatch — 를 한
함수에 적용:

```osty
// Before
pub fn lirTypeClassName(c: LirTypeClass) -> String {
    match c {
        LirTypeVoid -> "void",
        LirTypeInt -> "int",
        LirTypeFloat -> "float",
        LirTypePtr -> "ptr",
        LirTypeAggregate -> "aggregate",
        LirTypeFunction -> "function",
        _ -> "unknown",
    }
}

// After
pub fn lirTypeClassName(c: LirTypeClass) -> String {
    if c == LirTypeVoid { return "void" }
    if c == LirTypeInt { return "int" }
    if c == LirTypeFloat { return "float" }
    if c == LirTypePtr { return "ptr" }
    if c == LirTypeAggregate { return "aggregate" }
    if c == LirTypeFunction { return "function" }
    "unknown"
}
```

핵심 관찰:

- **enum equality (`==`) 가 작동** — 자동 파생 `Equal` (CLAUDE.md
  부록 B.2 #16) 으로 enum variant 비교가 stage0 가 cover 하는
  "if-else with phi" 패턴 안에 들어옴.
- **multi-arm match → 다중 if-return 체인** 이 가장 단순한 매핑.
  체인의 마지막 식 (fallthrough) 이 `_ ->` arm 을 대체.
- **시각적 비용**: 11 라인 → 14 라인. 약 +25% 길이.

## 4. 패턴 카탈로그

audit 스크립트 출력에서 발췌한 카테고리별 재작성 전략:

### 4.1 Match on payload-less enum (이 PR 시범)

```osty
match foo { A -> x, B -> y, _ -> z }
// → if foo == A { return x } if foo == B { return y } z
```

대다수의 418 match 중 가장 많은 부분 (정확한 비율 미측정).

### 4.2 Match on payload enum (Some/None, Ok/Err)

```osty
match opt {
    Some(x) -> use(x),
    None -> fallback,
}
// → ??
```

stage0 가 enum payload 추출을 지원하지 않으면 명시적 helper 함수
호출 + 분기 필요. **payload 추출 helper** 가 stdlib 에 없으면 stage0
의 P9 (struct field accessor) 가 해당 추출을 cover 할 수 있는지
미확인.

### 4.3 `?` propagation

```osty
let x = foo()?
// → 
let r = foo()
if r.isErr() { return r.toErr() }
let x = r.unwrap()
```

명시적 wrapping 으로 라인 수가 3배 늘어남. 109 사이트 × 3 = ~330 추가
라인.

### 4.4 Closure literals

```osty
xs.filter(|x| x > 0)
// → helper fn isPositive(x: Int) -> Bool { x > 0 } and reference it
```

`xs.filter(isPositive)` 형태로 lift. 6 사이트만 있으니 쉽게 처리.

## 5. 결론

B2 의 전체 작업은 **기계적이지만 부피가 큼** (533 사이트, 50–100 시간).
이 PR 은:

1. `scripts/audit-stage0-coverage.sh` 로 진행률 추적용 인프라를
   남긴다 — 이후 PR 마다 `match exprs` 카운트가 줄어들면 진행 상황을
   객관적으로 볼 수 있음.
2. `lirTypeClassName` 한 함수를 시범 재작성 — 가장 단순한 패턴이
   실제로 통과하는지 확인. `osty check` 로 syntax / type 검증.
3. 패턴 카탈로그 (§4) — 다음 작업자가 동일한 변환을 일관되게 적용
   가능.

전체 toolchain 을 stage0 안에 들이는 것은 한 PR 의 범위가 아니다.
A4–A6 의 registry-publish + signing 인프라가 fresh-clone 사용자에게
더 빠른 패스를 줄 가능성이 높으나, 그것도 §8 (CI 호스트 / release
정책 / 스토리지) 의 정책 결정이 풀리기 전엔 활성화 못 함.

## 6. 다음 작업

- **B2 후속 PR**: 한 파일 (예: `lir_proto.osty` 의 enum dispatch
  helpers) 을 통째로 stage0 패턴으로 변환. 진행률을 audit 스크립트로
  측정.
- **stage0 enum payload 지원 측정**: §4.2 의 미확인 사항을 작은 합성
  테스트로 검증. payload 추출이 cover 되면 `?` propagation 도 같은
  접근으로 풀 수 있을 가능성.
- **A6 §8 정책 결정**: 여기 풀리면 B2 가 emergency-only fallback 의
  꼬리만 다듬는 작업으로 축소됨.
