# LLVM self-host plan — R3 trajectory brick 3a attempt (lir_proto inline expansion)

> **상태**: 측정 (시도 실패, 변경 revert 됨). 다음 fresh session 재시도 시 단축 경로 제공.
> **선행**: [PR #1873](https://github.com/choiceoh/osty/pull/1873) (PrimInt type cover), [PR #1874](https://github.com/choiceoh/osty/pull/1874) (Int__* C runtime wrappers).
> **소유**: backend / toolchain.

## 1. 목표

R3 trajectory (osty-self capability 확장 → generated.go 점진적 retire) 의 **brick 3a**: `toolchain/lir_proto.osty` 에 `Int__abs/min/max/clamp/signum` 의 inline LLVM IR expansion 추가 — stage0 의 `renderKnownIntMethodCall` (`internal/backend/stage0/emit.go`) 와 동등한 self-contained mechanism. PR #1874 의 C-runtime wrapper 가 fallback safety net 으로 남으되, lir_proto 자체가 inline expansion 으로 link external call 제거.

## 2. 시도한 변경 (revert 됨)

### 2.1 `internal/mir/lower.go`

`mangleMethodSymbol` filter revert:

```go
// 변경 후 (이 시도)
func mangleMethodSymbol(owner, method string) string {
    return owner + "__" + method
}

// PR #1874 (현재 main)
func mangleMethodSymbol(owner, method string) string {
    return rewriteStdlibSymbolToRuntime(owner + "__" + method)
}
```

목적: raw mangled symbol (`Int__abs`) 이 stage0 + lir_proto 양쪽에 도달하도록.

### 2.2 `toolchain/lir_proto.osty`

**(a) `LirInstrKind` enum 에 `LirInstrSelect` 추가** — LLVM `select i1 cond, T a, T b` 표현.

**(b) `lirSelect` constructor**:

```osty
pub fn lirSelect(dest: String, typ: LirType, cond: String, trueVal: String, falseVal: String) -> LirInstr {
    let mut i = lirInstrInvalid()
    i.kind = LirInstrSelect
    i.dest = dest
    i.typ = typ
    i.args = [
        lirOperand(lirIntType(1), cond),
        lirOperand(typ, trueVal),
        lirOperand(typ, falseVal),
    ]
    i
}
```

**(c) `lirRenderInstr` match arm 에 `LirInstrSelect` case 추가**:

```osty
LirInstrSelect -> i.dest + " = select i1 " + i.args[0].value + ", " + lirTypeString(i.typ) + " " + i.args[1].value + ", " + lirTypeString(i.typ) + " " + i.args[2].value,
```

**(d) `lirTryLowerMirIntMethodIntrinsic` 함수 추가** — `lirLowerMirCrossModuleCall` 직전 위치. 5 method (`Int__abs/min/max/clamp/signum`) 매칭 + inline LLVM expansion. stage0 의 `renderKnownIntMethodCall` 패턴 그대로 (sub/icmp/select 시퀀스).

**(e) `lirLowerMirCall` 의 hook**:

```osty
if instr.calleeSymbol == "std.fs.readToString" {
    lirLowerMirStdFsReadToStringCall(l, instr)
    return
}
if lirTryLowerMirIntMethodIntrinsic(l, instr) {
    return
}
let callee = lirLookupMirFunction(l.mirModule, instr.calleeSymbol)
```

## 3. 검증 결과

- ✓ `go build -o .bin/osty ./cmd/osty` — Go 측 빌드 성공
- ✓ `OSTY_STAGE0_FALLBACK=1 OSTY_INSTALL_SELF_ALLOW_SOURCE_BOOTSTRAP=1 .bin/osty install-self` — osty-self 재빌드 성공
- ✗ `osty-self --selfhost-doctor` — **exit 139 (SEGFAULT)**
- ✗ `just osty-tests` — 51/51 fail (다시 `_Int__abs` undefined symbols)

osty-self 의 simple invocation (`osty-self`, `osty-self --help`) 은 정상 동작 (exit 0, expected error message). doctor probe path 에서 SEGFAULT.

## 4. 가능한 root cause (검증 필요)

### 4.1 `LirInstrKind` enum 의 ordinal 변경

`LirInstrSelect` 를 `LirInstrComment` 다음에 추가 — 기존 enum 의 ordinal 안 변경. 단 `match` arm 의 순서 변경 가능성. 만약 어떤 path 가 enum 의 정수 discriminant 비교를 가정한다면 (예: `kind == LirInstrComment` 같은) 우리 추가는 그 path 영향 zero. **가능성 낮음**.

### 4.2 `lirRenderInstr` 의 `args[0].value` OOB

새 case 의 render 가 `i.args[0]`, `i.args[1]`, `i.args[2]` 접근. 만약 다른 path 가 `LirInstrInvalid` kind 의 instr 를 `LirInstrSelect` 로 잘못 set 후 args 비워두면 OOB.

`lirInstrInvalid()` 가 `args: []` 로 초기화. 만약 다른 곳에서 `kind = LirInstrSelect` set 후 args 안 채우면 render 가 OOB. 또는 monomorphization 이후 `args.get(0)` 호출이 panic.

**검증 path**: lir_proto.osty 의 모든 `LirInstrSelect` set 사이트 grep. `lirSelect()` 외 path 가 있는지 확인.

### 4.3 `lirTryLowerMirIntMethodIntrinsic` monomorphization

새 함수가 monomorph 후 stage0 cover 안 되는 instruction 을 emit. osty-self 가 stage0 fallback 으로 빌드되므로 그 함수의 일부가 declined → wrong code emit → SEGFAULT.

stage0 audit 100% 인 것을 고려하면 가능성 낮지만 — audit 의 scope 가 monomorphization 후 인지 검증 필요 (handoff §10 의 audit-pass ≠ build-pass 논점).

**검증 path**: `OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/` 재실행 — 새 함수 추가 후의 audit cover 측정.

### 4.4 lir_proto 의 다른 hook path 의 의도되지 않은 영향

`lirLowerMirCall` 의 hook 가 너무 일찍 — 그 결과 정상 cross-module call 도 우리 hook 거침. `lirTryLowerMirIntMethodIntrinsic` 의 sym 비교가 정확하지 않거나 (예: `Int__abs ` trailing space) false 가 아닌 잘못된 return path.

**검증 path**: hook 함수에 debug print 추가 후 osty-self 재빌드 — 어떤 symbol 이 우리 hook 을 거치는지 측정.

## 5. 다음 시도 권장 단계

1. **enum ordinal isolated test**: `LirInstrSelect` 만 추가 + render case 만 추가 (lirSelect constructor + lirTryLowerMirIntMethodIntrinsic 없이). osty-self 재빌드 + `--selfhost-doctor`. SEGFAULT 발생하면 (4.1) 또는 (4.2) — 더 정확히는 새 enum case 자체가 문제.

2. **render case dummy test**: render 의 새 case 가 `""` 반환하도록 변경 (args 접근 안 함). osty-self 재빌드. SEGFAULT 사라지면 (4.2) — args OOB.

3. **hook dummy test**: `lirTryLowerMirIntMethodIntrinsic` 가 항상 `false` 반환하도록 변경. osty-self 재빌드. SEGFAULT 사라지면 (4.3) 또는 (4.4).

각 step 단일 fresh session 으로 가능. 측정 후 root cause 결정 → 진짜 fix.

## 5.1 step 1A-1E 측정 결과 (2026-05-17 후속 spike)

§5 의 plan 그대로 진행 + 단계 더 세분화. 5 step 의 isolated measurement:

| step | 변경 | doctor SEGFAULT? | osty-tests |
|---|---|---|---|
| 1A | enum 만 추가 | **YES (exit 139)** | **OK (129 passed)** |
| 1B | + render case + lirSelect constructor | YES | OK (129 passed) |
| 1C | + hook stub (always false) | YES | OK (129 passed) |
| 1D | + `mangleMethodSymbol` filter revert | YES | **FAIL (51/51)** |
| 1E | + 실제 inline impl (5 method) | YES | **FAIL (51/51)** |

### 핵심 발견 (root cause 분리)

**(A) doctor SEGFAULT 의 root cause = enum-only 변경**:
- step 1A 부터 1E 까지 SEGFAULT 일관 — enum 추가 자체가 트리거
- osty-tests 와 무관 (1A-1C 에서 osty-tests OK)
- §4.1 (enum ordinal change) 가 확실한 root cause. 다른 코드의 어떤 path 가 LirInstrKind ordinal 또는 size 의 invariant 가정

**(B) osty-tests link miss 의 root cause = `mangleMethodSymbol` filter revert**:
- step 1D 부터 fail — filter revert 가 트리거
- step 1C 까지는 filter keep (PR #1874 의 rewrite 적용) → osty-tests OK
- step 1E (실제 inline impl) 도 fail — inline 이 실제로 적용 안 되고 있음을 의미

### step 1E 의 inline impl 적용 실패 가설

filter revert 후 raw `Int__abs` symbol 이 lir_proto 에 들어가야 함. 그러나 osty-tests 가 그것을 inline 안 받음. 가능한 이유:

1. **osty-self 가 stage0 fallback 으로 빌드** — `lirTryLowerMirIntMethodIntrinsic` 새 함수가 monomorph 후 stage0 cover gap → osty-self 의 lir_proto path 에서 그 함수가 dead code 또는 declined
2. **osty-tests 가 osty-self subprocess 호출 안 함** — 다른 path (Go-side stage0 fallback emit) 가 `Int__abs` external call 로 emit
3. **lir_proto 의 다른 path 가 우선 분기** — `lirLookupMirFunction` 가 `Int__abs` lookup 성공 (어떤 fallback) → 우리 hook 안 거침

### 다음 시도 권장 (refined)

**(A) doctor SEGFAULT 해소**:
- enum ordinal 의 hidden invariant 추적 — LirInstrKind 의 정수 비교 site 또는 array/lookup table size 가정 grep
- 또는 enum 추가 우회 — 기존 case 의 재해석 (예: LirInstrCall 의 op field 로 "select" 지정)

**(B) osty-tests inline 적용 검증**:
- `lirTryLowerMirIntMethodIntrinsic` 에 debug eprint 추가 → osty-tests 실행 시 어떤 symbol 이 hook 거치는지 측정
- 또는 osty-self 가 osty-tests 의 build path 에 실제 활용되는지 확인 (osty-self subprocess 호출 trace)
- 또는 PR #1874 의 filter keep + lir_proto 가 rewritten symbol (`osty_rt_int_abs`) 인식 + inline expand (filter revert 불필요)

가장 surgical = (B) 의 마지막 option — filter keep + hook 의 sym 매칭을 `osty_rt_int_abs/...` 로 변경. 이 경우:
- mangleMethodSymbol 의 rewrite 그대로 (`Int__abs → osty_rt_int_abs`)
- stage0: rewritten symbol 받음 → inline 안 함 → C wrapper external call (PR #1874 의 path)
- lir_proto: rewritten symbol 받음 → 우리 hook 가 매칭 + inline → external call 회피
- 단 stage0 와 lir_proto 양쪽 inline 다 가능하려면 stage0 의 매칭도 같이 갱신 필요

## 6. R3 trajectory 의 다음 brick 후보

- **3a-debug**: 위 (1)~(3) measurement spike 후 root cause 명확화 → 진짜 fix
- **3b** (wrapping/saturating/checked C wrapper): osty-tests 가 미사용이라 unblock effect 0. 향후 사용 대비 cover. PR1c 패턴 그대로
- **`mirJsonObjectGetNamed` cover** (handoff §10 production path next wall): R3 의 별도 brick

## 7. 본 PR 의 산출물

- `docs/llvm-selfhost-plan-r3-brick-3a-attempt.md` (이 문서) — 시도 detail + root cause 후보 + 다음 권장
- `SPEC_GAPS.md::cross-pkg-module-resolution` 갱신 — brick 3a 시도 결과 기록

코드 변경 없음 (시도 코드는 revert 됨).
