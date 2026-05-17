# LLVM self-host plan — R3 trajectory coercion wall measurement

> **상태**: measurement (multi-session debug 의 진척 doc). cmd/osty-native-checker production build 의 `set.toString` / `string.endsWith` coercion declined wall 의 정확한 trigger 확인.
> **선행**: R3 brick 1-7 ([PR #1873-85](https://github.com/choiceoh/osty/pull/1873)), PR3-D/E ([PR #1889-90](https://github.com/choiceoh/osty/pull/1889)).
> **소유**: backend / lir_proto.

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

1. **osty-self 의 LLVM IR 디스어셈블** — `set.toString coercion is not supported` string literal 의 reference site 추적. `lirLowerCoercedOperand:5040` 의 declined emit site 가 어떤 다른 caller path 로 트리거.
2. **`OSTY_LIRPROTO_DEBUG=1`** — osty-self subprocess 의 verbose mode 시도 (existence 검증 필요).
3. **debug eprint in lirLowerCoercedOperand** — `eprint("DBG: coercion site label=" + label)` 추가, osty-self 재빌드, build 시 어떤 label 가 진짜 declined emit (set.toString / string.endsWith / 다른 label?). 단 osty-self 의 stage0 cover gap 으로 우리 변경 반영 안 될 수도.

## 7. 본 doc 의 산출물

- `docs/llvm-selfhost-plan-r3-coercion-wall-measurement.md` (이 문서) — bisection 측정 + 가설 + 다음 권장
- 코드 변경 없음

R3 trajectory 의 multi-session brick 의 시작점. 다음 fresh session 의 spike 가 더 빠르게 root cause 식별.
