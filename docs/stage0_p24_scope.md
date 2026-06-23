# Stage0 P24 — bootstrap unblock 첫 타겟 스코프

> **상태**: **historical scope doc** (2026-05-11 측정 기준). P24–P26 unlock PR chain + PR [#1858](https://github.com/choiceoh/osty/pull/1858) 이후 stage0 audit 는 **100%** — 아래 §0 은 당시 baseline 보존용.
> **현재 authority**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md) §3.1, [`SPEC_GAPS.md`](../SPEC_GAPS.md) `cross-pkg-module-resolution` 타임라인.
> **선행**: [docs/osty_self_b2_1_audit.md](osty_self_b2_1_audit.md) §4.3 master plan v2, [docs/osty_self_bootstrap_design.md](osty_self_bootstrap_design.md) §4 P21–P23 row.
> **차단 대상 (historical)**: [LLVM_BACKEND_GAP_PLAN.md](../LLVM_BACKEND_GAP_PLAN.md) Phase 0-A.

## 0. 현 측정 (2026-05-11 — historical)

| 메트릭 | 값 | 출처 |
|---|---|---|
| Stage0 audit cover (toolchain checker 모듈) | 94.2% (6112 / 6489) | `OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/` |
| `install-self` 실제 decline | **1340 functions** | `OSTY_STAGE0_FALLBACK=1 OSTY_STAGE0_LIST_ALL_DECLINES=1 .bin/osty install-self` |
| 누락 클러스터 (audit top 30) | `blocks=7 params=2 ret=String feats=call,intr,fr` 류 dominate | 위 audit 명령 출력 |

audit % 와 install-self 실제 decline 의 격차 = audit이 toolchain checker bundle 만 측정, install-self는 binary 컴파일 → monomorphization 등으로 함수 수 1340 까지 늘어남.

### 0.1 Revalidated baseline (2026-06)

| 메트릭 | 값 | 출처 |
|---|---|---|
| Stage0 audit cover (full `toolchain/` walk) | **100.0% (8240 / 8241)** | PR #1858; `OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/` |
| Fresh-clone source bootstrap | **`OSTY_STAGE0_FALLBACK=1 osty install-self` succeeds** | PR #1861+ C-wrapper wave + `just bootstrap` recipe |
| Remaining self-host walls | LIR Proto / cross-pkg link + interface boxing (steps 1–4 landed; vtable reach step 3.5 open) | [`docs/llvm-selfhost-plan-cross-pkg-link-measurement.md`](llvm-selfhost-plan-cross-pkg-link-measurement.md) §10, `SPEC_GAPS.md` |

**audit-pass ≠ build-pass** still applies: production `osty-self` LIR Proto can decline shapes that audit classifies as covered under different monomorph specializations.

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

## 6. 추가 audit 발견 (2026-05-12) — 단순 후보 진입 못 함

`useDeclTailAfter` 대신 더 단순해 보이는 single-block aggregate-return 후보 (`Runner__checkLockfile`, `selfPkgPathDependency`) 를 점검했더니, 두 함수 모두 **upstream MIR type-info 손실** 로 인해 stage0 매처가 깨끗하게 거부할 수 없는 상태.

### 6.1 `Runner__checkLockfile` (audit `blocks=1 params=1 ret=named:Check instrs=2 feats=call`)

```
bb 0 ReturnTerm instrs=2:
  CallInstr dest=local#2 callee=FnRef{runtime.cihost.CheckLockfile type=fn}
                              args=[Copy(local#1+proj type=String), Copy(local#1+proj type=opt)]
  CallInstr dest=local#0 callee=FnRef{checkFromHostResult type=*ir.ErrType}
                              args=[Const(FnConst type=*ir.ErrType), Copy(local#2 type=named:CheckResult)]
```

매처 거부 사유:
- `matchDirectAggregateCall` — 단일 CallInstr만 허용 (여기는 2개).
- `matchAggregateConstructor` — 마지막 instr이 `AggregateRV{Struct|Tuple}` 이어야 함 (여기는 CallInstr).
- `matchGenericScalarCFG` — `emitWhileStep` 가 두 번째 CallInstr 인자 `Copy(local#2 type=named:CheckResult)` 처리 못함 → fail at line 16.

진짜 P24 필요 unlock:
- (a) **named-type intermediate를 call arg로**: `Copy(local#2 type=named:T)` 를 opaque ptr ABI로 통과. `resolveOperandWithPrelude` 확장.
- (b) **FnConst arg**: `Const(FnConst type=*ir.ErrType)` — 함수 포인터 상수를 i64/ptr로 인코딩.
- (c) **`+proj type=opt` arg**: Optional 필드 projection. P10 (struct field read)이 scalar만 cover.
- (d) **errType callee signature**: 콜리 시그니처가 손실됐을 때 인자 / 반환 타입 추론을 호출 사이트로부터 역추론.

### 6.2 `selfPkgPathDependency` (audit `blocks=1 params=2 ret=named:SelfPkgDependency instrs=3 feats=call,agg`)

```
bb 0 ReturnTerm instrs=3:
  AssignInstr dest=local#3 src=Aggregate(variant, fields=[], type=named:SelfPkgSourceKind)
  CallInstr dest=local#4 callee=FnRef{anySemReq type=*ir.ErrType} args=[]
  AssignInstr dest=local#0 src=Aggregate(struct, fields=[
    Copy(local#1 type=String),
    Const(StringConst type=String),
    Copy(local#3 type=named:SelfPkgSourceKind),
    Copy(local#4 type=Bool),         # ← 타입 불일치
    Const(StringConst type=String),
    Copy(local#2 type=String),
  ], type=named:SelfPkgDependency)
```

`SelfPkgDependency.versionReq: SemReq` 인데 fields[3] 이 `Copy(local#4 type=Bool)`. 즉 `anySemReq()` 반환 타입이 errType 으로 손실 → fallback Bool 로 lower. **타입 불일치 IR 을 emit 하면 verifier가 reject.**

매처 거부 사유: `matchAggregateConstructor` 의 `if ty != fieldTypes[i]` 검사가 정확히 이 미스매치 잡음 → 거부 정상.

진짜 fix 위치: stage0 가 아니라 **upstream `anySemReq` 콜 시그니처 보존** (front-end / IR / MIR lowering 어느 단계인지 추적 필요).

### 6.3 결론 갱신

- "단순한 single-block aggregate-return" 결정 클러스터는 사실 upstream MIR type-degradation 의 표출. stage0 매처 추가만으로 풀리지 않음.
- 진짜 P24 의 best ROI 는: **`Copy(local type=named:T)` opaque-ptr arg 통과** + **`+proj type=opt` Optional 필드 read** 두 unlock 의 조합. 이 둘이 풀리면 `Runner__checkLockfile` 류 함수가 `matchGenericScalarCFG` 안에서 통과.
- 그 두 unlock은 stage0 emit.go 의 `resolveOperandWithPrelude` 와 `classifyProjectedCallLine` / `resolveProjectedFieldSlot` 확장. 추정 ~300 LOC + Option<T> 필드 read 시 i64 tag + payload 분리 emit 필요.
- 별도 P-phase로 분할:
  - **P24-named**: `Copy(named:T)` call arg 통과 (단독)
  - **P24-optproj**: `+proj type=opt` 필드 read (별개)
  - **P25-aggcallchain**: 2+ CallInstr 시퀀스 ending in aggregate-return CallInstr (위 둘 위에 누적)
- `useDeclTailAfter` (P24-foritermut) 와 `tyToRepr` (P26-enumagg) 는 다른 unlock 으로 별도 PR.

### 6.4 install-self 차단 해소까지의 거리

위 분석으로 P-phase 수가 더 늘어남. 최소 6–8 개 P-phase + 각 ~300–500 LOC + 회귀 테스트. install-self 완주까지 4–6 주 단일 사람 estimate, 병렬 2–3 주.

이 경로의 alternative 는 LLVM_BACKEND_GAP_PLAN.md Phase 0-B (registry publish) 또는 다른 머신/체크아웃의 working osty-self 를 `OSTY_SELF_BIN` 으로 고정. Phase C–F closeout 자체 진행을 빨리 보고 싶다면 0-B 권장.

---

다음 액션: 이 문서 기준으로 P24 PR을 별도 세션에서 진행. 한 PR 당 단일 P-phase. PR 단위 review/regression 가시화 유지.
