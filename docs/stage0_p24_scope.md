# Stage0 P24 — bootstrap unblock 첫 타겟 스코프

> **상태**: 작업 시작 시 참조용 스코프 문서. 실제 구현은 별도 PR.
> **선행**: [docs/osty_self_b2_1_audit.md](osty_self_b2_1_audit.md) §4.3 master plan v2, [docs/osty_self_bootstrap_design.md](osty_self_bootstrap_design.md) §4 P21–P23 row.
> **차단 대상**: [LLVM_BACKEND_GAP_PLAN.md](../LLVM_BACKEND_GAP_PLAN.md) Phase 0-A (모든 Phase C–F closeout 의 선행).

## 0. 현 측정 (2026-05-11)

| 메트릭 | 값 | 출처 |
|---|---|---|
| Stage0 audit cover (toolchain checker 모듈) | 94.2% (6112 / 6489) | `OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/` |
| `install-self` 실제 decline | **1340 functions** | `OSTY_STAGE0_FALLBACK=1 OSTY_STAGE0_LIST_ALL_DECLINES=1 .bin/osty install-self` |
| 누락 클러스터 (audit top 30) | `blocks=7 params=2 ret=String feats=call,intr,fr` 류 dominate | 위 audit 명령 출력 |

audit % 와 install-self 실제 decline 의 격차 = audit이 toolchain checker bundle 만 측정, install-self는 binary 컴파일 → monomorphization 등으로 함수 수 1340 까지 늘어남.

## 1. P24 후보 함수 — shape 별 분리

원래 "tyToRepr / frontTypeReprToString / useDeclTailAfter 형 (match-on-enum returning struct)" 으로 묶었으나, 실제 MIR shape 측정 결과 **세 함수가 완전히 다른 패턴**:

### 1.1 `useDeclTailAfter` (toolchain/check.osty:598)

```
blocks=18 params=2 ret=String instrs=47 feats=intr,fr
```

shape 특징:
- 가변 `out`, `idx`, `j` (Int / String / ErrType)
- 두 개의 sequential `for unit in strings.split(s, "")` 루프
- 첫 루프: `idx = j` 갱신 (조건부)
- 둘째 루프: `out = "{out}{unit}"` interpolation (조건부)
- `_iter` / `_len` / `_idx` triple으로 lower된 list 순회 패턴 (P22 cover)

P24 필요 unlock:
- multi-`for-in-list` **sequential composition** (현재 matcher는 single for-loop 한정)
- mutable local with `ErrType` initial (`-1`이 IntConst 타입 추론 실패로 ErrType)
- conditional assign within for-loop body
- nested string interpolation 결과를 mutable local에 store

### 1.2 `frontTypeReprToString` (toolchain/check.osty:96)

```
blocks=? params=1 ret=String feats=call,intr,fr (재귀 + if-else chain)
```

shape 특징:
- `repr.kind == "primitive" || ... || ...` 5+ OR-chain
- else-if 5+ 분기 (named / optional / tuple / fn)
- 분기 내 재귀 호출 (`for a in repr.args { parts.push(frontTypeReprToString(a)) }`)
- `match repr.ret { Some(inner) -> ..., None -> ... }` (Option<Struct>)

P24 필요 unlock:
- string-equality chain in if-cond
- 재귀 호출 within for-loop body (P3b call extends to self-recursion)
- `match Option<Struct> { Some(x) -> use(x), None -> default }` enum-with-payload dispatch

### 1.3 `tyToRepr` (toolchain/check.osty:48) — 가장 큼

```
blocks=? params=2 ret=named:FrontTypeRepr feats=call,intr,agg,fr (재귀 + 8-variant match)
```

shape 특징:
- `match node.kind { TkErr, TkPoison, TkPrim, TkNamed, TkOptional, TkTuple, TkFn, TkVar, TkSelf -> ... }` 9-variant payload-less enum
- 각 분기마다 `FrontTypeRepr { kind, name, path, args, ret }` struct 생성자 (5필드)
- `TkNamed` / `TkTuple` / `TkFn` 분기는 `for x in node.args { ... .push(tyToRepr(...)) }` 재귀
- `TkOptional` / `TkFn` 분기는 `Some(tyToRepr(...))` Option wrapping

P24 필요 unlock:
- N-variant payload-less enum `match` returning aggregate (현재 P17/P18은 if-else struct, P19은 if-else with `?` — match는 별도)
- struct literal construction 결과를 함수 return으로 직접 lowering
- `Some(struct)` Option wrapping 으로 struct return
- 재귀 호출 결과를 List<Struct>에 push (composite list element)

## 2. 권장 P-phase 분할

세 함수가 단일 phase 가 안 되므로 다음 시퀀스 권장:

| Phase | 타겟 함수 | 새 unlock | 추정 LOC | 누적 install-self decline 감소 (estimate) |
|---|---|---|---|---|
| **P24** | `useDeclTailAfter` 류 | sequential multi for-in-list + conditional mutation + ErrType local | ~400 | ~30 functions (string-processing helpers) |
| **P25** | `frontTypeReprToString` 류 | string-equality if-else chain + self-recursion in for-body + Option<Struct> match | ~500 | ~80 functions |
| **P26** | `tyToRepr` 류 | N-variant payload-less enum returning aggregate + Some(struct) wrap | ~600 | ~120 functions |
| **P27+** | audit top buckets 재측정 | TBD | TBD | (반복) |

각 PR 별 회귀 게이트:
- `TestStage0ToolchainAudit` cover % 증가 확인
- 새 fixture 1건/phase (특정 shape lowering 잠금)
- `OSTY_STAGE0_FALLBACK=1 install-self` decline 카운트 감소

## 3. P24 (useDeclTailAfter 류) — 구현 가이드

### 3.1 새 matcher 이름

`matchSequentialForInListMutableScan` — single block sequence of `for-in-list` loops with mutable accumulator updates.

### 3.2 위치

`internal/backend/stage0/emit.go` — `emitFunction` 의 matcher chain 에 `matchForInListEarlyExit` 다음 (line ~775) 삽입.

### 3.3 패턴 시그니처

```go
type sequentialForInListMutableScanPattern struct {
    paramTypes   []scalarType
    paramNames   []string
    retType      scalarType
    stackDecls   []stackDecl
    forLoops     []forLoopBlock  // 2 개 이상
    interpolations []interpolationStep
    finalReturnLocal mir.LocalID
}
```

### 3.4 매처 사전 조건

- 1개 함수, return type=String
- N parameters scalar
- ≥2 for-in-list MIR sub-graphs (head/iter/test/body/post/exit 6-block 패턴 × N)
- forLoop 간 sequential composition (첫 exit → 둘째 entry edge)
- ErrType local 허용 (literal `-1` 등이 ErrType 으로 lower될 때 i64 로 폴백)

### 3.5 emit 전략

- 각 for-loop 를 기존 `emitForInListReturn` 인프라 재사용해 emit (단, return 대신 fall-through)
- mutable accumulator 는 `alloca String` + `store` per assign
- 마지막 return은 마지막 mutable local read

### 3.6 회귀 테스트

`internal/backend/stage0/emit_test.go` 에 추가:

```go
func TestStage0EmitsSequentialForInListMutableScan(t *testing.T) {
    // useDeclTailAfter shape: 두 for-in-list + 가변 String + 조건부 assign
    // Build MIR manually (현재 P-tests 컨벤션)
    ...
    out, err := stage0.EmitMIR(mod, llvmabi.Options{PackageName: "test"})
    ...
    // assertions: 2 phi groups, 2 load+store pairs, final ret = last store value
}
```

### 3.7 expected diff

| 파일 | 추정 LOC |
|---|---|
| `internal/backend/stage0/emit.go` (matcher + emitter) | +400 |
| `internal/backend/stage0/emit_test.go` (fixture) | +120 |
| `internal/backend/stage0_toolchain_audit_test.go` (해당 클러스터 cover assertion) | +20 |
| **합계** | **~540 LOC** |

## 4. 차단 / 의존성

- **선행 없음** — stage0 P21–P23 이미 머지. P24는 그 위 누적.
- **차단 대상**: install-self bootstrap, 그 다음 Phase C–F closeout 전체.

## 5. 미해결 결정

- P24 vs P25 vs P26 의 PR 순서 — 위 권장은 source 복잡도 오름차순. install-self decline 카운트 감소 영향력 순서로 재배치하려면 audit 단위 측정 필요.
- 매처 이름 컨벤션 — `matchSequentialForInListMutableScan` 길지만 명확. 짧은 대안 (`matchTailScan` 등) 도 OK.
- `ErrType` literal `-1` 처리 정책 — silently i64 fallback vs explicit `mir.IntConst{Type: ErrType}` 변환. 기존 P-phase 패턴 따르면 silent fallback이 일반적.

---

다음 액션: 이 문서 기준으로 P24 PR을 별도 세션에서 진행. 한 PR 당 단일 P-phase. PR 단위 review/regression 가시화 유지.
