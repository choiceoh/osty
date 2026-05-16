# LLVM self-host plan — PR1c-1 attempt (negative result)

> **상태**: 측정 + revert (이 PR).
> **선행**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md), [docs/llvm-selfhost-plan-pr1c-blockers.md](llvm-selfhost-plan-pr1c-blockers.md) §"권장 다음 단계 경로 A".
> **소유**: backend / toolchain.

## 목적

PR1c-blockers report 의 권장 경로 A (`mir.IntrinsicReadLine` 신설 — 대안: stage0 의 `classifyCallStep` 에 `std.io.readLine → osty_rt_io_read_line` symbol rewrite 추가) 시도. **단일 세션 무리** 확인 + 분산 emit path 의 정확 위치 측정.

## Attempt 경로 — symbol rewrite 만으로 가능한가?

가장 작은 변경 가설:
1. `internal/backend/stage0/emit.go::classifyCallStep` 에 `runtimeSymbolRewriteForCall` helper 추가
2. `std.io.readLine → osty_rt_io_read_line` rewrite (정의는 `osty_runtime.c:25384` 에 이미 존재)
3. `cmd/osty-native-checker/main.osty` 가 `io.readLine()` 호출
4. stage0 가 `declare ptr @osty_rt_io_read_line()` + `call ptr @osty_rt_io_read_line()` emit

## 실측 결과

### 시도 1: `classifyCallStep` 의 두 declare/return site 에 rewrite

```go
func runtimeSymbolRewriteForCall(symbol string) string {
    switch symbol {
    case "std.io.readLine":
        return "osty_rt_io_read_line"
    }
    return symbol
}

// classifyCallStep 안:
callSym := runtimeSymbolRewriteForCall(ref.Symbol)
declareFunctionPrototype(mctx, callSym, destType, resolved.args)
return pendingInstr{kind: instrCall, callSymbol: callSym, ...}
```

**결과**: link 실패. emit 된 IR 분석:

```llvm
declare ptr @std.io.readLine()
...
define void @main() {
  %0 = call ptr @std.io.readLine()
  ...
}
```

rewrite 가 **적용 안 됨** — `classifyCallStep` 가 trigger 안 되고 다른 emit path 가 main 의 readLine call 처리.

### main.osty 의 IR shape 가 multi-block (if-else) 인 경우

main:
```osty
fn main() {
    let input = io.readLine()
    if input.len() >= 0 {
        println("...")
    }
}
```

→ MIR multi-block (entry → bb.1 (println) → bb.3 (ret), bb.2 (else, empty)). `classifyCallStep` 는 single-block sequential pattern 또는 그 변형용. main 의 multi-block IR 는 다음 중 하나 거침:

- `emitTrivialMain` (line ~1045) — 단순 main, decline 후 fall-through
- `matchStructFieldRead/Write` 류 — n/a
- `matchSequentialVoid` / `matchSequentialReturn` — n/a
- `matchIfElseReturn` — 가능성 큼 (실제 emit 위치)
- `matchShortCircuitGuardReturn` — 가능성 있음

각 `emitXxx` 함수가 자체적으로 call line 을 emit. `ref.Symbol` 을 그대로 사용. rewrite 한 곳만 추가해서는 부족 — **모든 emit 함수 (`emitIfElseReturn`, `emitShortCircuitGuardReturn`, `emitSequentialVoid/Return`, 등 10+) 의 `ref.Symbol` 사용 site 마다 rewrite 적용 필요**.

### grep 기준 분산된 callSymbol 사이트

`internal/backend/stage0/emit.go` (17679 LOC) 안의 `callSymbol` 출력 site (16+):

```
1442  type pendingInstr struct { ..., callSymbol string, ... }
2399  pendingInstr{ callSymbol: callSym, ... }  (classifyCallStep, 본 시도 사이트)
2429  pendingInstr{ callSymbol: callSym, ... }  (classifyCallStep, 두 번째)
3064  pendingInstr{ callSymbol: spec.symbol, ... }
3105  pendingInstr{ callSymbol: "osty_rt_strings_Slice", ... }
3190  pendingInstr{ callSymbol: symbol, ... }
3237  pendingInstr{ callSymbol: symbol, ... }
3393  pendingInstr{ callSymbol: "llvm.expect.i1", ... }
3442  pendingInstr{ callSymbol: "osty_rt_strings_Concat", ... }
3451  pendingInstr{ callSymbol: "osty_rt_strings_Concat", ... }
3679  pendingInstr{ callSymbol: symbol, ... }
3711  pendingInstr{ callSymbol: "osty_rt_strings_Equal", ... }
4031  pendingInstr{ callSymbol: "osty_rt_bytes_len", ... }
4041  pendingInstr{ callSymbol: "osty_rt_list_len", ... }
4049  pendingInstr{ callSymbol: "osty_rt_strings_ByteLen", ... }
5139  fmt.Fprintf(out, "  %s = call %s @%s(", pi.binDestReg, ..., pi.callSymbol)
7791  type directAggregateCallPattern struct { callSymbol string, ... }
7899  pat.callSymbol = ref.Symbol  (matchDirectAggregateCall)
```

대부분은 이미 `osty_rt_*` runtime symbol 직접 사용. `ref.Symbol` (user-space dotted symbol) 을 직접 emit 하는 site 는 line 2399 / 2429 (`classifyCallStep`) + 7899 (`matchDirectAggregateCall`) + 그 외 if-else / sequential emit 함수들 내부.

## 판정

| 가설 | 결과 |
|---|---|
| `classifyCallStep` 만 패치하면 충분 | **No** — main 의 multi-block IR 는 다른 emit path 거침 |
| 분산된 모든 emit 사이트 패치 | 가능하나 ~10+ site 동시 + 회귀 위험 |
| 더 깊은 위치 (MIR construction 단계 rewrite) | 가능 — `internal/mir/lower.go` 또는 `internal/backend/entry.go` 의 FnRef.Symbol 생성 시점에 rewrite. production path (lir_proto) 도 자동 일관 |
| `mir.IntrinsicReadLine` enum 신설 (원래 plan) | 가능 — MIR ABI 변경, 모든 MIR consumer 회귀 검증 필요 |

**가장 작은 진짜 fix 후보**: MIR construction 단계 (`internal/mir/lower.go` 또는 `internal/ir/lower.go::lowerCall`) 에서 `ref.Symbol = "std.io.readLine"` 시 `osty_rt_io_read_line` 으로 rewrite. 한 곳 + 자동 stage0/production 둘 다 적용.

단 이 fix 도 다음 위험:
- `osty_rt_io_read_line` 의 C signature 가 `void *osty_rt_io_read_line(void)` — Osty 측 `readLine() -> String` 와 매핑 (String == ptr). Type 일치 가정.
- Production path (lir_proto) 가 같은 rewrite 후 어떻게 동작하는지 unverified.
- GC 협력 — rewrite 한 함수 호출 시 safepoint 처리.

## 시도된 patch (revert 됨, 참조용)

```go
// Before classifyCallStep:
func runtimeSymbolRewriteForCall(symbol string) string {
    switch symbol {
    case "std.io.readLine":
        return "osty_rt_io_read_line"
    }
    return symbol
}

// Inside classifyCallStep, two declare+return sites:
callSym := runtimeSymbolRewriteForCall(ref.Symbol)
declareFunctionPrototype(mctx, callSym, destType, resolved.args)
return pendingInstr{kind: instrCall, callSymbol: callSym, ...}
```

main.osty 시도 형태 (revert 됨):
```osty
use std.io

fn main() {
    let input = io.readLine()
    if input.len() >= 0 {
        println("...")  // stub JSON
    }
}
```

→ emit 된 IR 가 여전히 `@std.io.readLine` symbol 사용 (위 분석 참조). link 실패.

## 다음 세션 시작점 (PR1c-2 후보)

**옵션 1 — MIR construction rewrite (가성비 최상)**

`internal/ir/lower.go` 또는 `internal/mir/lower.go` 의 cross-module function call 생성 site 에서 `Symbol` 결정 시 rewrite. 1 곳 + stage0/production 양쪽 통과.

```go
// internal/ir/lower.go::lowerCall (approximate):
sym := callee.QualifiedName()
sym = runtimeSymbolRewriteForCall(sym)  // <- 한 줄 추가
return &mir.FnRef{Symbol: sym, ...}
```

추정 작업: 5–10 LOC, 1 파일, 단순. 진짜 위험 = production path 의 동작 변경 검증 + `osty_rt_io_read_line` type signature 일치.

**옵션 2 — `mir.IntrinsicReadLine` enum 신설 (원래 plan, 안전성 ↑)**

원래 PR1c-1 blockers doc 의 권장 path. MIR ABI 확장이라 모든 consumer 회귀 검증 필요. 단 명시적 intrinsic 분류 → stage0 의 `classifyIntrinsicLine` family 에서 명확히 cover.

추정 작업: 100–200 LOC, 5 파일 (mir.go / mir_generator.osty / lir_proto.osty / stage0/emit.go / io.osty), MIR consumer 회귀.

**옵션 3 — toolchain 측 lowering rewrite**

`toolchain/mir_generator.osty` 의 dispatch 가 `std.io.readLine` call 을 인지해서 직접 `osty_rt_io_read_line` 호출 emit. 한 곳 + Osty 측 변경.

추정 작업: 10–20 LOC, `toolchain/mir_generator.osty` 1 파일. dead `mirRtIOReadLineSymbol` helper 가 활용됨.

**권장 다음 세션 진행**: 옵션 3 (toolchain 측) → 옵션 1 (Go MIR) → 옵션 2 (enum) 순서로 시도. 옵션 3 가 가장 작고 self-host 원칙 (Osty 측에 lowering) 와 일치.

## plan §10 갱신 항목

- N9 (PR1c-blockers doc 에서 도입): 작업 항목 확장 — "mir.IntrinsicReadLine 신설 (옵션 2)" 외에 옵션 1 (MIR construction rewrite, 5-10 LOC) / 옵션 3 (toolchain dispatch, 10-20 LOC) 추가
- R6 (신규): `osty_rt_io_read_line` type signature 와 Osty `readLine() -> String` 매핑 검증 — String == ptr 가정. production path 의 동작 변경 검증
- Q14 (신규): MIR construction 단계 어디서 `FnRef.Symbol` 가 결정되는가? `internal/ir/lower.go` 또는 `internal/mir/lower.go` 의 정확 site
- Q15 (신규): `toolchain/mir_generator.osty` 의 dispatch 가 `std.io.readLine` call 을 인지하는 첫 site (현재 dead helper `mirStdIoMethodIsReadLine`/`mirRtIOReadLineSymbol` 외 정의된 진짜 caller)

## 이 PR 의 산출물

- `docs/llvm-selfhost-plan-pr1c-1-attempt.md` (이 문서) — negative result 기록 + 분산 emit site 인벤토리 + 다음 옵션 비교

revert 됨: `internal/backend/stage0/emit.go` (rewrite helper 추가 시도) + `cmd/osty-native-checker/main.osty` (readLine 호출 형태 시도).
