# LLVM self-host plan — R3 trajectory coercion wall measurement

> **상태**: measurement (multi-session debug 의 진척 doc). cmd/osty-native-checker production build 의 `string.indexOf result` coercion declined wall 의 정확한 trigger 확인 (PR #1894 off-by-one fix 후 정확한 label 노출).
> **선행**: R3 brick 1-7 ([PR #1873-85](https://github.com/choiceoh/osty/pull/1873)), PR3-D/E ([PR #1889-90](https://github.com/choiceoh/osty/pull/1889)), MIR JSON wire-format alignment ([PR #1894](https://github.com/choiceoh/osty/pull/1894)).
> **소유**: backend / lir_proto.

## 0. PR #1894 후 wall label 정확화

| | 이전 | 이후 |
|---|---|---|
| declined label | `set.toString` / `string.endsWith` | `string.indexOf result` |

PR #1894 (wire-format off-by-one fix) 가 MIR JSON 의 intrinsic kind 매핑을 정확화 → declined message 의 label 가 정확한 site 노출. 진짜 trigger = `string.indexOf` 의 result 처리 path.

## 1. 측정한 wall

`.bin/osty build cmd/osty-native-checker/` 시 declined message:

```
osty-self lir-proto-lower-mir-json: unsupported set.toString: set.toString coercion is not supported
osty-self lir-proto-lower-mir-json: unsupported string.endsWith: string.endsWith coercion is not supported
```

결과: `main.ll` 가 unsupported-source skeleton (`; code generation is not implemented yet`). LLVM IR 정상 emit 안 됨.

## 2. Bisection: 정확한 trigger

main.osty 의 일부분만 남기고 build 시도:

| main.osty 내용 | declined? |
|---|---|
| `fn main() { let _x: Int = 1 }` | **OK (Built)** |
| `use std.io + fn main() { let _x: Int = 1 }` | OK |
| `use std.strings + fn main() { let _x: Int = 1 }` | OK |
| `use std.io + use std.strings + fn main() { let _x: Int = 1 }` | OK |
| `use std.io + use std.strings + fn main() { let text = "hello"; if let Some(i) = strings.indexOf(text, "world") { let _ = i } }` | **FAIL** (`set.toString` / `string.endsWith` coercion declined) |

**Trigger = `if let Some(i) = strings.indexOf(...)` 패턴 — `Option<Int>` 의 match destructure** 가 lir_proto path 에서 declined.

## 3. 측정된 MIR 상태 (brick 6+7 머지 후)

`OSTY_LIRPROTO_KEEP_STAGED=1 .bin/osty build cmd/osty-native-checker/` 로 staged MIR 검사:

- staged MIR JSON 의 `<error>` literal **0건** (brick 6+7 의 cover 완료)
- intrinsics 분포: `{Println, StringLen, StringIndexOf, StringSubstring}` (4개)
- **set.toString / string.endsWith call site 0건** (모든 staged MIR JSON)
- main fn 의 call instr 의 callee.type.display 가 brick 7 의 ErrType→Int downgrade 로 모두 concrete

즉 **input MIR 깨끗**. declined message 는 osty-self runtime 의 lir_proto path 가 자체 내부 lower 시 emit (input MIR 의 직접 site 가 아니라 처리 chain 의 다른 단계).

## 4. 가설

declined message format `label + " coercion is not supported"` (lir_proto.osty:5040). label = "set.toString" / "string.endsWith" — `lirLowerMirStringRuntimeCall` 의 4006 / 3862 line 의 호출. 그 함수의 내부에서 `lirStoreMirResult` 또는 `lirLowerCoercedOperand` 호출이 declined trigger.

가능 root cause:
1. `Option<Int>` 의 match destructure 가 lir_proto 의 어떤 algebraic-handling path 에서 set.toString/endsWith 호출 (cleanup 또는 debug path)
2. osty-self 의 stage0 lower 시 monomorphization cover gap 으로 wrong code emit (lir_proto path 가 의도 안 한 곳 트리거)

## 5. 시도한 fallback (모두 osty-self runtime 에 반영 안 됨)

R3 brick 4 [PR #1881](https://github.com/choiceoh/osty/pull/1881):
- `lirLowerMirLocalsFrom`: `<error>` literal → i64 default
- `lirStoreMirResult`: dest type unknown → producer type fallback
- `lirLowerMirStringRuntimeCall` arg coercion: value type unknown → param type
- 그 외 10 coercion sites 의 `className == LirTypeUnknown` fallback

R3 brick 5 [PR #1883](https://github.com/choiceoh/osty/pull/1883):
- `lirLowerMirLocalsFrom`: 모든 `lirTypeIsZero(typ)` → i64 default (broader)

Brick 8 시도 (revert 됨):
- `lirLowerCoercedOperand`: castOp 실패 시 hint type 으로 broadest fallback
- `lirStoreMirResult`: castOp 실패 시 destType 으로 broadest fallback

**모두 osty-self runtime 의 declined message 에 반영 안 됨**. 즉 우리 변경이 osty-self build 시 LLVM 으로 lower 되지만 runtime path 가 우리 fallback 거치지 않음. 다른 declined emit site.

## 6. 다음 시도 권장 (multi-session)

PR #1894 후 정확한 site = `lirLowerMirIndexOf` (`toolchain/lir_proto.osty:6315`). result 처리:

```osty
// dest.typ = "Int?" (loc.typ from MIR JSON)
if (lirIsOptionTypeName(loc.typ) || lirStringHasQuestionSuffix(loc.typ)) && !lirTypeIsZero(destType) {
    let merged = lirWrapOptionFromI64Sentinel(l, indexReg, destType, label)
    lirStoreMirResult(l, instr.dest, lirOperand(destType, merged), loc.typ, label + " result")
    return
}
// Fallback: i64 dest path
lirStoreMirResult(l, instr.dest, lirOperand(lirIntType(64), indexReg), "Int", label + " result")
```

가설:
1. `lirStringHasQuestionSuffix("Int?")` 매칭 실패 (stage0 cover gap) → fallback path → i64 store to Option<Int> aggregate → `lirStoreMirResult` 의 coercion 실패
2. 또는 `lirLowerMirType("Int?")` 가 stage0 cover gap 으로 zero (Unknown) 반환 → 두 path 모두 fail

**brick 9 시도 (revert 됨)**: `destType.className == LirTypeAggregate` 도 sentinel-wrap path 활성화. 그러나 osty-self runtime 에 반영 안 됨 (stage0 cover gap 동일). osty-tests 회귀 zero.

진짜 fix multi-session 영역:
1. osty-self 의 LLVM IR 디스어셈블 — `lirStringHasQuestionSuffix` 또는 `lirLowerMirType` 의 stage0 lower wrong-code 확인
2. lir_proto.osty 의 sentinel-wrap path 의 stage0 cover 확장 (PR #1858 cascade 패턴 추가 wave)

## 7. 본 doc 의 산출물

- `docs/llvm-selfhost-plan-r3-coercion-wall-measurement.md` (이 문서) — bisection 측정 + 가설 + 다음 권장
- 코드 변경 없음

R3 trajectory 의 multi-session brick 의 시작점. 다음 fresh session 의 spike 가 더 빠르게 root cause 식별.

## 8. PR #1897 후 wall 변화 (term.branch)

[PR #1897](https://github.com/choiceoh/osty/pull/1897) (List/Map/Set toString receiver-return-type recovery) 머지 후 cmd/osty-native-checker build 의 declined 변화:

| | 이전 (PR #1894 후) | 이후 (PR #1897 후) |
|---|---|---|
| subcommand | `lir-proto-lower-mir-json` | `lir-proto-lower` (자체 path) |
| 핵심 message | `string.indexOf result coercion` | **`term.branch: branch condition requires i1, got %Option.Int`** |

진척: install-self 자체는 OK (`Built ... osty-self`). cmd build 의 새 wall = `term.branch` cond 가 `Option<Int>` aggregate 그대로 (i1 expect). `if let Some(i) = strings.indexOf(...)` 의 desugar 시 cond 가 wrap 안 됨 (checker / MIR generator 의 gap).

### 정확한 site

`toolchain/lir_proto.osty:2906::MirTermBranch`:

```osty
if termKind == MirTermBranch {
    let cond = lirLowerMirOperand(l, l.instrs, bb.termCond, "Bool", lirIntType(1))
    if cond.typ.llvm != "i1" {
        lirLowerError(l, LirDiagUnsupported, "branch condition requires i1, got `" + cond.typ.llvm + "`", "term.branch")
        return lirUnreachable()
    }
    ...
}
```

cond 가 `%Option.Int` aggregate. 정상 form = `presentReg = extractvalue %Option.Int %scrut, 0; cmp = icmp eq i64 %presentReg, 0` 로 wrap.

## 9. brick 10 시도 (revert 됨)

`term.branch` 에서 cond 가 aggregate 일 때 자동 disc extract + icmp eq 0 으로 i1 narrow:

```osty
if cond.typ.className == LirTypeAggregate {
    let discReg = lirFresh(l)
    l.instrs.push(lirExtractValue(discReg, cond.typ, cond.value, [0]))
    let presentReg = lirFresh(l)
    l.instrs.push(lirBinary(presentReg, "icmp eq", lirIntType(64), discReg, "0"))
    cond = lirOperand(lirIntType(1), presentReg)
}
```

검증:
- ✓ `just osty-tests` 통과 (회귀 zero)
- ✗ osty-self runtime 의 declined 동일 (`function "main" does not match any stage0 pattern` 으로 message format 변화 단 본질 동일)

즉 brick 10 도 stage0 cover gap. 우리 새 코드 (`cond.typ.className == LirTypeAggregate` if-branch) 자체가 stage0 fallback path 에서 unsupported pattern → wrong code emit → runtime 에 같은 declined.

## 10. 진짜 unlock path (multi-session architecture)

Chicken-and-egg architecture wall 의 root cause = osty-self 가 자기 자신의 stage0 cover gap 으로 빌드. **어떤 lir_proto.osty 변경도 osty-self runtime 에 반영 안 됨** (brick 8/9/10 모두 동일 결과).

multi-session brick wave 의 진짜 path:
1. **이중 부트스트랩** — stage0 빌드된 osty-self 가 LIR Proto direct 로 새 osty-self 재빌드. 두 번째 osty-self 가 우리 변경 반영
2. **또는** stage0/emit.go 의 cover 확장 — 특정 lir_proto.osty 함수의 wrong-code emit pattern 식별 + 새 stage0 case 추가. 단 audit 100% 의 monomorphization 후 subset 와는 다른 pre-monomorph pattern.

## 11. MIR JSON term 분석 — input branch 0건 확정 (2026-05-17 추가)

main.osty 원본 + minimal repro (`if let Some(i) = strings.indexOf(...)` 만) 둘 다의 staged MIR JSON 의 term 측정:

```
원본 (3 fn):
  fn[0].naiveExtractSource: switch_int×2, goto×3, return×2
  fn[1].emptyCheckResultJson: return
  fn[2].main: return

Minimal (1 fn):
  fn[0].main: switch_int, goto×2, return
```

**branch term 0건**. 즉 declined `term.branch: branch condition requires i1` 는 input MIR 와 **무관** — osty-self 자체 binary 의 `lirLowerMirTerm` (또는 그 caller) 의 stage0 fallback 으로 lower 된 wrong-code 가 runtime 에 BranchTerm 처리 path 트리거 + cond.typ.llvm 가 `%Option.Int` aggregate.

즉 osty-self 의 `term.branch` 처리 path 자체가 stage0 cover gap. 우리 input MIR 에 branch term 없음에도 osty-self runtime 에서 cond aggregate 의 branch 호출 chain 트리거.

multi-session deep debug 의 핵심 site:
- `internal/backend/stage0/emit.go::5772` 의 BranchTerm handling — cond LLVM type 가 i1 만 expect
- `toolchain/lir_proto.osty::2906::MirTermBranch` — runtime path
- 두 곳의 cover gap 식별 + 정확한 wrong-code pattern 분석 multi-session

→ Chicken-and-egg wall: lir_proto.osty 변경 (brick 10) 도 stage0 cover gap 으로 미반영, stage0/emit.go 변경도 별도 wave 필요.
