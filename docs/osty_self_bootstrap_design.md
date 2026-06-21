# `osty-self` 부트스트랩 설계 — Post-#1405 follow-up

> **상태**: **옵션 C (stage0 fallback) 구현 완료** (2026-05). source self-host ratchet landed in PR [#2022](https://github.com/choiceoh/osty/pull/2022) — `verify-self-rebuild` stages 2+ require the Osty source compiler (`toolchain/selfhost_driver.osty`). Remaining gaps: production LIR Proto / cross-pkg link walls tracked in [`SPEC_GAPS.md`](../SPEC_GAPS.md).
> **연관 PR**: #1405 (Go MIR emitter 제거), #1406 (MIR-direct 디스패처 복구), #2022 (source selfhost ratchet), #2023 (stage0 audit 100%).
> **소유**: backend / toolchain.

## 1. 문제 정의

PR #1405가 `internal/llvmgen` (Go 측 MIR emitter mirror, ~112K 줄)를 제거하면서 LLVM 백엔드의 in-process emit 경로가 사라졌다. 모든 MIR → LLVM IR 변환은 이제 다음 체인을 통과한다:

```
host osty (Go 부트스트랩)
   └─ LLVMBackend.Emit
       └─ tryNativeOwnedMIRPayloadLLVMIRText
           └─ exec(osty-native-llvmgen)               # internal/nativellvmgen
               └─ tryMIRRequestViaLIRProto
                   └─ exec(osty-native-lirproto)       # internal/nativelirproto
                       └─ exec(osty-self lir-proto-lower)
                           └─ toolchain/lir_proto.osty (Osty 자체)
```

체인 끝의 **`osty-self`는 `osty build --backend=llvm toolchain/`의 산출물**이고 — 그걸 만들려면 다시 `osty build` 가 필요하다. 즉 **닭-달걀 부트스트랩 의존이 만들어졌다**.

#1405 머지 이전에는 같은 호출 시점에 `llvmgen.GenerateFromMIR(entry.MIR, opts)` (in-process Go 코드)이 fallback이었기 때문에 `osty-self`가 없어도 MIR→LLVM이 가능했다. #1405는 그 in-process 경로를 삭제하면서 fallback도 같이 제거했다.

### 1.1 현재 관찰 가능한 결과

- `osty build --backend=llvm toolchain/` 는 resolvable `osty-self` 없을 때 **stage0 Go emitter** (`internal/backend/stage0/`, `OSTY_STAGE0_FALLBACK=1`) 로 bootstrap 가능. Production 경로는 여전히 `osty-self lir-proto-lower-mir-json` 서브프로세스.
- `scripts/verify-self-rebuild` 는 stage1 을 host osty + stage0 fallback 으로 빌드한 뒤, stage2/3 을 **source compiler** (`HIR → Mono → MIR → LIR Proto`) 로 재빌드하고 byte parity 를 강제. `just verify-self-rebuild` / `just backend-loop` 가 래핑.
- **audit-pass ≠ build-pass**: `TestStage0ToolchainAudit` 가 100% (PR #2023) 여도 `install-self` / monomorph / LIR Proto 단계에서 별도 decline 가능 — [`SPEC_GAPS.md`](../SPEC_GAPS.md) `cross-pkg-module-resolution`.
- `osty-self` 가 미리 빌드돼 있으면 (CI 캐시, registry fetch, `just bootstrap`) production LIR Proto 경로가 정상 동작.

### 1.2 설계 목표

- 부트스트랩 경로 1개 — fresh clone에서 git checkout → 단일 명령으로 osty 컴파일러 + toolchain 산출물 생성 가능.
- 산출물은 Reproducible (동일 입력 → 동일 byte). 이미 `verify-self-rebuild`가 stage2/3 byte parity를 강제 중.
- 호스트 의존성 최소: Go toolchain + clang 외 추가 사전 산출물 요구하지 않음.
- v0.5 baseline 규칙 준수: 새 surface 추가 없음, Osty로 작성 가능한 로직은 Go로 새로 작성하지 않음.

## 2. 후보 옵션

### 옵션 A: Go fallback emitter 부분 복원 (포기 권장)

#1405 이전의 `llvmgen.GenerateFromMIR`을 부분적으로 되돌린다. MIR-direct 경로가 `osty-self` 없이도 동작.

- 장점: 즉시 효과. 부트스트랩 닭-달걀 해소.
- 단점: **CLAUDE.md "Osty 우선" 규칙 정면 위반**. #1405 의 정신을 부정. 112K 줄을 다시 들이는 셈은 절대 비례하지 않으므로 현실적으론 minimal subset만 복원해도 그 minimal이 다음 토큰부터 drift surface가 됨.
- **권장하지 않음**.

### 옵션 B: 사전 빌드된 `osty-self`를 git에 커밋 (포기 권장)

`toolchain/.osty/out/...` 아래 stage1-equivalent 바이너리를 LFS 또는 GitHub release artifact로 제공.

- 장점: fresh clone → 빠른 빌드.
- 단점: 플랫폼별 바이너리 (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64, windows-arm64) 6개 모두 관리 필요. 보안/감사 부담. 현재 단일 시드 (`internal/selfhost/generated.go`, 68k 줄) 정책과 모순.
- **권장하지 않음**.

### 옵션 C: Go 호스트 측에 minimal MIR→LLVM "stage0" fallback 추가 (권장)

`osty-self`가 없을 때만 켜지는, 의도적으로 좁은 stage0 emitter를 Go 측에 둔다. 범위:

- **포함**: `toolchain/*.osty` 자기 자신을 한번 컴파일하기에 충분한 MIR 패턴 — 함수 정의, 분기, 산술, struct/enum 기본 lowering, runtime ABI 호출 (`osty.gc.*`, `osty_rt_*`).
- **제외**: vectorize 힌트, parallel access groups, target_feature, hot/cold 섹션, advanced unroll. 즉  spec의 **컴파일러를 컴파일할 수 있는 핵심만**.

stage0 emitter는 명시적으로 **deprecated-on-arrival** — fast path가 아니라 부트스트랩용. Production 빌드 (`osty-self` 사용 가능 시)는 LIR Proto 경로 그대로.

설계 패턴:

```go
// internal/backend/llvm.go
func emitLLVMFallback(route llvmDispatchRoute, entry Entry, opts llvmabi.Options) ([]byte, []error, error) {
    if entry.MIR == nil { ... }
    // 1차 시도: native subprocess (osty-self 사용 가능)
    if out, ok, ws, err := tryNativeOwnedMIRPayloadLLVMIRText(entry, opts.Target); err == nil && ok {
        return out, ws, nil
    } else if err != nil && !isOstySelfMissing(err) {
        return nil, ws, err   // 진짜 에러는 그대로 surface
    }
    // 2차: stage0 fallback (osty-self 없을 때만)
    if !stage0Enabled() {
        return nil, nil, llvmabi.Unsupported("mir-emit", "...")
    }
    return stage0EmitMIR(entry.MIR, opts)
}
```

`stage0EmitMIR` 본체는:
- **새 패키지 `internal/backend/stage0/`** 에 격리. ~5K 줄을 넘지 않도록 spec 핵심 구문만 다룸.
- 매 회 `toolchain/*.osty`가 stage0 surface를 벗어나지 않는지 CI에서 검증 (`TestStage0CoversToolchainMIR` 같은 게이트).
- 출력은 LIR Proto 경로와 byte-equivalent일 필요 **없음**. `verify-self-rebuild`의 stage2/3 parity는 stage1의 결과물 (`osty-self-1`)이 stage1을 사용해서 다시 빌드되는 것이므로, stage0 IR이 stage1 IR과 다른 건 정상.

장점:
- spec ON, 부트스트랩 가능.
- stage0이 모든 emit을 처리하지 않으므로 surface 폭주 위험 제어됨.
- LIR Proto 경로가 default — stage0 retire는 osty-self 광범위 가용 시점에 가능.

단점:
- **소소한 코드 중복** — toolchain/lir_proto.osty 의 일부 패턴과 stage0 가 같은 일을 다른 언어로 구현. CI 게이트가 drift 막아주지만 0%는 아님.
- 새 Go 코드가 들어가는 점은 CLAUDE.md "Osty 우선" 정신과 마찰. 단, **부트스트랩 경계는 기존 예외 카테고리 (호스트 / 부트스트랩 시드)에 해당**한다고 본다. 같은 카테고리에서 `internal/selfhost/generated.go` (68K 줄, 동결 시드) 가 이미 허용됨.

### 옵션 D: `osty-self` 없을 때 부트스트랩-only Osty interpreter (포기 권장)

`toolchain/lir_proto.osty` 를 Go 측 tree-walking interpreter로 실행.

- 장점: 코드 중복 0. surface 변경 없음.
- 단점: 성능 cliff (toolchain 빌드가 분 단위 소요로 늘어날 가능성). interpreter 자체가 새 surface — 옵션 C보다 도리어 invasive.

## 3. 권장 설계 — 옵션 C (Stage0 fallback)

### 3.1 패키지 레이아웃

```
internal/backend/stage0/
    doc.go            // 부트스트랩 전용 명시 + retirement 조건 명문화
    emitter.go        // MIR → LLVM IR 핵심 (function/block/instr 분기)
    rvalue.go         // 산술 / 비교 / 캐스트 / 호출
    place.go          // local/projection 주소 계산
    types.go          // mir.Type → LLVM 타입
    runtime.go        // osty.gc.* + osty_rt_* 선언
    coverage_test.go  // toolchain/*.osty 가 stage0 안에 머무르는지 검사
```

### 3.2 활성화 조건

- 환경변수 `OSTY_STAGE0_FALLBACK=1` (기본 OFF) 로 명시적 opt-in 시 stage0 사용.
- **자동 fallback은 `tryNativeOwnedMIRPayloadLLVMIRText`가 "osty-self not found"으로 declined한 경우에만**. 다른 declined 사유 (예: `osty-self`는 있지만 lir-proto가 명시적으로 "이 shape 못 한다"고 선언한 경우)는 fall through 시키지 않고 그대로 unsupported로 surface.
- CI 빌드는 `OSTY_STAGE0_FALLBACK=1` 명시 + `osty-self`도 빌드해서 양쪽 모두 검증 (stage0이 stale 안 나도록).

### 3.3 surface 가드

```go
// internal/backend/stage0/coverage_test.go
func TestStage0CoversToolchainMIR(t *testing.T) {
    // toolchain/*.osty 전체를 MIR로 lowering 후 stage0가 받아들이는지 검증.
    // 새 MIR 패턴이 toolchain에 들어갈 때 stage0 갱신 누락이 즉시 빨간불.
}
```

이 게이트가 stage0 retirement까지 drift 잡아준다.

### 3.4 retirement 경로

다음 조건이 모두 충족되면 stage0 삭제:

1. `osty-self` 가 모든 지원 호스트 트리플에서 reproducible 빌드 가능.
2. `osty install` (또는 동등 부트스트랩 명령) 이 단일 명령으로 first-build 완료.
3. CI/dev 환경 모두 osty-self 자동 캐시 메커니즘 보유 — fresh clone에서도 LIR Proto 경로가 곧바로 동작.

retirement는 별도 PR에서 진행하고, 그 PR이 stage0 디렉토리를 통째로 삭제 + 본 design doc도 archive로 이동.

### 3.5 spec 영향

없음. stage0는 emitter level에서만 작동하며 surface 추가 0건. `LANG_SPEC_v0.5/`, `OSTY_GRAMMAR_v0.5.md`는 변경 안 함.

## 4. 구현 단계 (제안)

| Phase | 범위 | 측정 |
|---|---|---|
| P0 | `osty-self not found` 에러 분류 / `isOstySelfMissing(err)` 도입 | unit test — **구현 완료**: `internal/backend/bootstrap.go::IsOstySelfMissing` |
| P1 | `internal/backend/stage0/` skeleton + 단순 `fn main()` 케이스 한 개 | TestStage0HelloWorld — **구현 완료**: `internal/backend/stage0/emit.go::EmitMIR` |
| P2a | non-main fn → Int { N } (literal return) | TestStage0EmitsIntLiteralFunctionAlongsideMain — **구현 완료** |
| P2b | non-main fn(x: Int) -> Int { x } (param passthrough) | TestStage0EmitsIntParamPassthrough — **구현 완료** |
| P2c | non-main fn(a, b: Int) -> Int { a + b } (binary add) | TestStage0EmitsIntBinaryAdd — **구현 완료** |
| P2d | non-main fn(a, b: Int) -> Int { a OP b } — Sub/Mul/Div/Mod 확장 | TestStage0EmitsIntBinaryArithOps — **구현 완료** |
| P2e | single-instruction batch — 비교 / 비트 / 시프트 / 논리 / Bool / mixed const+var / 0~2 파라미터 / 자유로운 피연산자 순서 | 47 tests — **구현 완료** |
| P3a | multi-instruction sequential (let / 임시 변수) — 단일 블록 안에서 N개 AssignInstr; UseRV 는 inline, BinaryRV 는 SSA register | 59 tests — **구현 완료** |
| P3b | 함수 호출 (CallInstr → LLVM `call`) — direct FnRef, scalar args/return, in-module symbol | 70 tests — **구현 완료** |
| P3c | if-else (4-block: entry/then/else/merge + BranchTerm + GotoTerm + phi for ret) | 80 tests — **구현 완료** |
| P4 | 디스패처 wiring (`OSTY_STAGE0_FALLBACK=1` + `IsOstySelfMissing` 조건부 라우팅) | TestEmitLLVMFallbackUsesStage0WhenOstySelfMissing — **구현 완료** |
| P5 | real front-end → MIR 통합 probe + UnitConst / StorageLive / StorageDead 갭 봉합 | TestStage0RealMIRBaseline 12 cases — **구현 완료** |
| P6 | while loop (4-block entry/header/body/exit + back-edge) + alloca/store/load 가변 local | TestStage0EmitsCountToWhileLoop / while_loop_count real-MIR — **구현 완료** |
| P7 | for-in-range loop (5-block entry/header/body/post/exit) — while alloca 인프라 재사용 | for_in_range_sum real-MIR — **구현 완료** |
| P8 | println(Int) intrinsic — printf declare + format string global + IntrinsicInstr 처리 | println_int_param / println_const_in_loop real-MIR — **구현 완료** |
| P9 | String literal return + moduleCtx refactor (string-pool / extraDecls 인프라) | string_literal_return real-MIR — **구현 완료** |
| P10 | struct field accessor — `fn name(p: Struct) -> T { p.field }` + module-level `%Struct = type {...}` 정의 | struct_field_read_x / struct_field_read_y real-MIR — **구현 완료** |
| P11 | println(String) — `%s\n` format + main 본문 안 intrinsic 호출 받기 | println_string_literal real-MIR — **구현 완료** |
| P12 | List<Int> 리터럴 + len() — runtime ABI 호출 (osty_rt_list_new / push_i64 / len) | list_literal_len real-MIR — **구현 완료** |
| P13 | String + (concat) → osty_rt_strings_Concat 호출 + 재귀 함수 호출 검증 | string_concat_*, recursive_factorial real-MIR — **구현 완료** |
| P14 | aggregate constructor (struct + tuple) — `Point { x, y }` / `(a, b)` → insertvalue 체인 + 합성 tuple 타입 풀 | struct_constructor_* / tuple_int_int_return real-MIR — **구현 완료** |
| P15 | list literal + indexed read `xs[N]` → list_get_i64 runtime ABI | list_literal_index_first / _second real-MIR — **구현 완료** |
| P16 | String == String runtime call | (#1455) — **구현 완료** |
| P17 | if-else with phi-merged struct return | (#1459) — **구현 완료** |
| P18 | `\|\|` short-circuit + if-else struct return | (#1461) — **구현 완료** |
| P19 | N-arm else-if chain with struct return + ? early-return desugar | (#1463 / #1465) — **구현 완료** |
| P20 | `\|\|` head + N-arm else-if chain | (#1467) — **구현 완료** |
| **P21–P23** | **unfrozen 2026-05 — 머지됨**. P21 (blocks=1 multi-param direct call → aggregate ret), P22 (for-in-list loop), P23 (for-in-list early-exit). 누적 audit cover 62.6% → 64.9% → 94.2% (`OSTY_STAGE0_AUDIT=1 ./internal/backend/`). 참조: #1571, e94ca9ac, 4878c62c, a391dd45, 8214e32b. | TestStage0ToolchainAudit |
| **P24+** | **active — master plan v2 진행 중**. 현 `install-self` 시 `OSTY_STAGE0_LIST_ALL_DECLINES=1` → 1340 function declines (audit %에도 불구하고 부트스트랩은 unique shape × 함수 instance 단위로 cumulative). 다음 P-phase 후보는 `b2_1_audit.md §4.3` 의 master plan v2 표 참조. | 측정 중 |

각 Phase는 independent PR. P0은 수십 줄. P1~P23 합산 emit.go 4253 + P21–P23 추가분 = ~5K+ 줄.

## 5. 결정 — RESOLVED (P21+ 정책 갱신 2026-05)

| 항목 | 결정 |
|---|---|
| 1. stage0 surface 의 spec 범위 | **v0.5 핵심 — P21+ unfrozen**. P0–P20 + P21/P22/P23 머지됨. install-self 부트스트랩 가능까지 master plan v2 (b2_1_audit §4.3) 의 sequence 진행. surface 추가는 부트스트랩 차단 함수에 한정 (CLAUDE.md "v0.5 baseline" 규칙은 spec surface — emitter coverage는 무관). |
| 2. stage0 위치 | `internal/backend/stage0/` — 결정. emit.go (15304 줄, P21–P23 포함) + emit_test.go (6783 줄) + doc.go. |
| 3. `OSTY_STAGE0_FALLBACK=1` 기본값 | **OFF** — registry path 가 default. 하지만 registry 가 0 자산 (`github.com/choiceoh/osty/releases/.../osty-self-snapshots` 404 confirmed 2026-05-11) 인 상황에서는 stage0 가 유일한 fresh-clone 부트스트랩 경로. CI bootstrap-smoke 도 양쪽 모두 검증. |
| 4. stage0 retirement 시점 | **영구 보존** — Q8. 진단 가치 + registry-down-시 DR2 부트스트랩 경로로 정당화. retirement PR 미예정. |

---

## Appendix A. 현재 verify-self-rebuild 흐름

```
host_osty (Go-built .bin/osty)
    └─ gates (optional): check toolchain/, snapshot parity, stage0 audit, LLVM route probes
    └─ stage1 build: host_osty build toolchain/  →  osty-self-1  (OSTY_STAGE0_FALLBACK=1)
    └─ stage2-seed / stage2 / stage3: previous osty-self rebuilds toolchain/
         via source compiler (selfhost_driver.osty: HIR → Mono → MIR → LIR Proto)
         host compiler forwarding forbidden after stage1 (host_guard script)
    └─ assert byte_eq(osty-self-2, osty-self-3)  [Mach-O metadata normalized on darwin]
```

Stage1 은 host osty 의 stage0 emitter 로 `osty-self` 를 처음 만든다. Stage2+ 는 그 바이너리의 **source compiler** 가 동일 toolchain 을 다시 컴파일해야 한다 — MIR-JSON-only shortcut 은 ratchet 에서 거부 (`TestVerifySelfRebuildRequiresSourceCompilerStages`). Production `osty build` 는 별도로 host-prepared MIR JSON → `lir-proto-lower-mir-json` 경로를 쓴다.
