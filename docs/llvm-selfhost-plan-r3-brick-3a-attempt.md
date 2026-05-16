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

## 6. R3 trajectory 의 다음 brick 후보

- **3a-debug**: 위 (1)~(3) measurement spike 후 root cause 명확화 → 진짜 fix
- **3b** (wrapping/saturating/checked C wrapper): osty-tests 가 미사용이라 unblock effect 0. 향후 사용 대비 cover. PR1c 패턴 그대로
- **`mirJsonObjectGetNamed` cover** (handoff §10 production path next wall): R3 의 별도 brick

## 7. 본 PR 의 산출물

- `docs/llvm-selfhost-plan-r3-brick-3a-attempt.md` (이 문서) — 시도 detail + root cause 후보 + 다음 권장
- `SPEC_GAPS.md::cross-pkg-module-resolution` 갱신 — brick 3a 시도 결과 기록

코드 변경 없음 (시도 코드는 revert 됨).
