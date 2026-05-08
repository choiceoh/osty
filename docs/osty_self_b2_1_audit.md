# B2.1 — Stage0 coverage audit (source + MIR ground truth)

> **상태**: 측정 (이 PR).
> **연관**: `docs/osty_self_b1_findings.md` (부트스트랩 갭),
> `docs/osty_self_b2_audit.md` (B2 시범 재작성),
> `docs/osty_self_bootstrap_design.md` (P0–P20).
> **소유**: backend / toolchain.

## 1. 목표

B2 의 후속 작업 (mechanical match rewrite vs stage0 unlock vs `?` 재작성
중 어디부터 칠지) 의사결정을 위한 데이터 갱신. 두 축에서 측정:

1. **Source-level audit** — `scripts/audit-stage0-coverage.sh` 가 잡는
   non-stage0 패턴 카운트. legacy regex 가 false-positive 가 있어
   세분화 + 분류 추가.
2. **MIR-level audit** — `TestStage0ToolchainAudit` (이미 존재) 가
   toolchain 의 모든 함수를 stage0 에 직접 흘려보고 decline 사유를
   집계. **부트스트랩 가능 여부의 ground truth.**

## 2. Source-level 현황 (B2.1 확장 audit)

### 2.1 Legacy totals (back-compat)

| 패턴 | 카운트 | 비고 |
|---|---|---|
| match exprs | 297 | `^[[:space:]]*match[[:space:]]+` |
| closures | 6 | `\|x\| \{` / `\|x\| ->` |
| generic fns | 0 | `fn name<T:` |
| `?` propagations (legacy) | 109 | `\?[[:space:].]` — type optional false positives 다수 |
| **total non-stage0** | **412** | |

### 2.2 Match-arm 분류 (B2.1 추가)

97 production .osty 파일을 across 한 arm count:

| arm 종류 | 카운트 |
|---|---|
| payload-less (`Variant -> ...`) | 2,212 |
| payload-bearing (`Variant(x) -> ...`) | **53** |
| wildcard (`_ -> ...`) | 213 |
| literal-int | 233 |
| literal-str | 0 |
| range | 0 |

**핵심 관찰**: payload-bearing arm 이 **53 개뿐**. b2_audit 의 추정 "100+"
보다 훨씬 적음.

### 2.3 Per-match 분류 (B2.1 추가, awk 기반 brace-depth tracking)

| match 분류 | 카운트 | 의미 |
|---|---|---|
| **bareonly** (Variant + wildcard만) | **244** | 순수 mechanical if-else 체인 |
| payload-only (Variant(x)만) | 5 | helper-fn extract 또는 unlock |
| mixed (payload + bare/literal) | 33 | case-by-case |
| literal (숫자/문자열/범위) | 2 | if-else cascade |
| **total** | **284** | (legacy 297 과 13 차이는 awk brace-tracking 의 string interpolation 노이즈) |

**결정적 사실**: 86% (244/284) 의 match 가 **순수 mechanical**.
unlock 없이 if-else 체인만으로 변환 가능.

### 2.4 `?` 분류 (B2.1 추가)

legacy `\?[[:space:].]` 가 over-count 했음 (109). 정밀 분류:

| `?` 종류 | 카운트 |
|---|---|
| `?-early-return` (`expr?` 단독, Result/Option 전파) | **30** |
| `?-optional-chain` (`?.field`) | 24 |
| `?-coalesce` (`??`) | 35 |
| **total real** | **89** |

legacy 의 109 - 정밀 89 = **20 개의 type-optional false positive** (예:
`String?`, `T?` 가 매개변수/반환 위치에서 carriage return 직전 공백
패턴으로 잡힘).

## 3. MIR-level 현황 (ground truth)

`OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v
./internal/backend/`. 150 초 동안 toolchain 의 7,932 함수를 stage0
emitter 에 단독 lowering 시도.

```
toolchain stage0 audit: 899 / 7932 functions covered (11.3%)
```

**88.7% 의 toolchain 함수가 현재 stage0 로 emit 안 됨.** decline 사유
top 30 (실측):

| 카운트 | shape | 해석 |
|---|---|---|
| 224 | `blocks=1 params=0 ret=String instrs=1 feats=call` | 0-인자 String-반환 + 단일 call. P3b call 지원의 빈틈 |
| 158 | `blocks=1 params=2 ret=String instrs=2 feats=intr` | 2-인자 String + intrinsic. P8/P11 confines `println` 만 |
| 132 | `blocks=1 params=1 ret=String instrs=2 feats=intr` | 1-인자 String + intrinsic |
| 127 | `blocks=1 params=3 ret=String instrs=2 feats=intr` | 3-인자 String + intrinsic |
| 109 | `blocks=1 params=1 ret=String instrs=1 feats=call` | 1-인자 String + call |
| 105 | `blocks=1 params=2 ret=String instrs=1 feats=call` | 2-인자 String + call |
| 98 | `blocks=1 params=4 ret=String instrs=2 feats=intr` | 4-인자 |
| 73 | `blocks=1 params=1 ret=Int instrs=1 feats=intr` | Int + intrinsic |
| 72 | `blocks=1 params=3 ret=String instrs=1 feats=call` | 3-인자 + call |
| 66 | `blocks=1 params=2 ret=String instrs=2 feats=call` | 2-인자, 2-instr + call |
| 54 | `blocks=1 params=3 ret=String instrs=3 feats=call,intr` | call + intr 혼합 |
| 52 | `blocks=1 params=0 ret=named:MirModule instrs=19 feats=call,agg` | aggregate |
| 50 | `blocks=1 params=0 ret=named:LirParityFixture instrs=6 feats=call,agg` | 테스트 픽스처 |
| 45 | `blocks=1 params=4 ret=String instrs=3 feats=call,intr` | 4-인자 |
| 42 | `blocks=1 params=2 ret=String instrs=3 feats=call,intr` | |
| 42 | `blocks=1 params=3 ret=named:LlvmValue instrs=2 feats=call,agg` | |

**의미심장한 패턴**:

1. **상위 decline 들이 match 와 무관**. `feats=` 에 `switch` 없음. 이는
   소스에 `match` 가 있더라도 MIR lower 후엔 if-else 체인으로 이미
   변환됨 (또는 stage0 가 다른 이유로 거부 중).
2. **String 반환 함수가 압도적**. top 16 중 12개가 `ret=String`. P9
   String-literal-return 은 cover 하지만, **call/intrinsic 결과의
   String 전달이 거부되고 있음**.
3. **단일 블록, 1–4 인자, 1–3 instructions** 의 trivial 함수가 대량
   decline. P2/P3 가 cover 한다고 본 것보다 빈틈 큼.
4. **Aggregate 반환** 도 큰 클러스터 (`feats=...,agg` 기반 100+ 함수).
   P14 가 stage0 cover 하지만 toolchain 의 실제 aggregate 함수와
   shape-match 안 됨.

## 4. 전략 재정렬

### 4.1 원래 가정 vs 현실

원래 마스터 플랜의 가정: "match→if-else 가 본진 (412 사이트), 거기다
`?` (109) 추가하면 끝".

**현실**:
- Source 수준의 mechanical 작업은 **~10–15 시간**으로 작음 (244
  bareonly + 38 case-by-case + 30 `?` early + 6 closures).
- MIR 수준 coverage 가 11.3% → 이게 끝나도 88.7% 의 toolchain 함수가
  stage0 거부. **install-self 부트스트랩은 source rewrite 만으로 안
  풀림.**

### 4.2 진짜 고비

`feats=call` / `feats=intr` / `feats=agg` 단일 블록 함수 1500+ 개가
현재 거부. 이는 stage0 의 **single-block multi-instruction call/intr/agg
shape coverage** 의 구멍. P21+ unlock 들이 여기를 메워야 함.

대표 후보 unlock:
- **P21**: `blocks=1 params=N ret=String instrs=1 feats=call`
  — 0~4 파라미터 + 단일 직접 call → ret. 224+109+105+72+22 = **532+
  함수** 즉시 cover.
- **P22**: `blocks=1 params=N ret=String instrs=1-2 feats=intr` —
  intrinsic call (string concat 외). 158+132+127+98 = **515+ 함수**.
- **P23**: `blocks=1 params=N ret=String instrs=2 feats=call` — 2-instr
  call (let + ret). 66+41+27+ = **134+ 함수**.
- **P24**: `blocks=1 params=N feats=call,intr` 혼합. 54+45+42+39 =
  **180+ 함수**.

각 P21–P24 가 stage0 emit.go 에 **~300–500 줄씩** 추가될 것 (기존 P1–P20
케이던스 기준). 합계 ~1500 줄. 한 PR 1000 diff 에 맞추려면 PR 두세
개로 분할.

### 4.3 마스터 플랜 v2

| # | PR | 범위 | diff | cumulative MIR coverage |
|---|---|---|---|---|
| **B2.1** | (이 PR) audit script 확장 + 측정 doc | 0 사이트, 측정만 | ~400 | 11.3% |
| **B2.2** | `toolchain: bareonly match → if-else (batch 1)` — lir_proto + monomorph_pass + frontend + hir_lower (~80 매치) | 80 mech rewrites | ~800–1200 | 11.3% (소스만 변하고 MIR shape 동일) |
| **B2.3** | `toolchain: bareonly match → if-else (batch 2)` — 잔여 13개 파일 (~164 매치) + closures lift (6) | 170 mech rewrites | ~1000–1400 | 11.3% |
| **B2.4** | `stage0 P21: blocks=1 multi-param direct call → ret` — 0–4 인자 String/Int 직접 call+ret | 0 source, +stage0 | ~800 (Go) | ~22% (~530 함수 추가) |
| **B2.5** | `stage0 P22: blocks=1 multi-param intrinsic → ret` — String-builder intrinsic family | 0 source, +stage0 | ~800 (Go) | ~28% |
| **B2.6** | `stage0 P23: blocks=1 instrs=2 let+ret patterns` | 0 source, +stage0 | ~600 (Go) | ~30% |
| **B2.7** | `stage0 P24: call+intr 혼합 + aggregate-return + field-read` | 0 source, +stage0 | ~1200 (Go) | ~40% |
| **B2.8** | `toolchain: case-by-case matches (mixed/payload) + `?`-family + remaining closures` | 38+89+0 | ~1000 | ~45% |
| **B2.9** | `re-audit + design B2.10+` — TestStage0ToolchainAudit 재실행하고 다음 unlock 후보 도출 | measurement | ~200 | ~45% |
| **B2.10+** | iterate stage0 unlock until coverage ≥ 90% | TBD | TBD | 90% target |
| **B2.final** | E2E verify (`OSTY_STAGE0_FALLBACK=1 install-self`) + CI 게이트 + retire stage0 docs | ~200 | n/a | (gate) |

**추정 총 PR**: 10–15 개. 원래 6개 가정에서 늘어남. 기간 추정: 2–4 주.

### 4.4 즉시 실행 가능한 변경

다음 PR (**B2.2**) 은 mechanical rewrite 중 가장 큰 파일들 — 데이터
의존성 없이 바로 시작 가능. audit 카운트가 줄어드는 직접적 가시
효과를 줌으로써 B2.4–B2.7 의 stage0 작업이 시간 걸리더라도 진행률 신호
유지.

`?` 패밀리 (총 89) 와 closures (6) 는 case-by-case 라 B2.8 에 묶음.
이쪽은 stage0 unlock 비용이 ROI 음수 (89 + 6 = 95 사이트 × 3 라인
mechanical = 285 라인 추가; stage0 P21+ unlock 보다 훨씬 작음).

## 5. 결론

1. **Source-level audit 만으로 install-self 부트스트랩 도달 못 함.**
   88.7% 의 toolchain 함수가 stage0 거부 중이며, 거부 사유 top 30 중
   match 와 직접 연관된 것 거의 없음.
2. **진짜 작업의 본진은 stage0 의 basic-shape coverage 확장** (P21+).
   기존 P1–P20 가 잡지 못하는 single-block multi-param call/intr/agg
   shapes 가 1500+ 함수 차지.
3. **mechanical match rewrite 는 여전히 가치 있음** — 244+38 = 282 매치
   는 손으로 풀어야 함. 다만 source rewrite 만으로 부트스트랩 풀린다는
   가정은 폐기.
4. **마스터 플랜 v2** 가 6 PR → 10–15 PR 로 늘어남. 1000 diff 케이던스
   유지하면 2–4 주 분량.

## 6. 다음 작업

이 PR (B2.1) 머지 후:

- **B2.2** — bareonly match→if-else batch 1 (가장 큰 파일들) 시작.
- 병행 가능: **B2.4** — `stage0 P21: blocks=1 multi-param call+ret`
  unlock 시작 (다른 worktree 권장 — toolchain 변경과 독립).

이 PR 이 다음 PR 들에 남기는 인프라:
- `scripts/audit-stage0-coverage.sh` — match-arm 분류 + per-match
  분류 + `?` 세분화. 매 PR 후 재실행해 진행률 측정.
- 본 문서 — 의사결정 baseline. 다음 audit 결과는 이 위에 increment.
