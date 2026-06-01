# LLVM self-hosting flip — blocker audit + roadmap

- **Status**: design draft (2026-05-16). No code change yet.
- **Scope**: 현재 dormant 한 `toolchain/*.osty` 변경들을 production 에 반영시키는 self-hosting flip 의 경로 + blocker 정리.
- **Authority**: `LLVM_MIGRATION_PLAN.md` + `LLVM_BACKEND_GAP_PLAN.md` + `MIR_EMITTER_PORT.md` + `LLVM_PHASE1_BASELINE.md`.

## TL;DR

"Self-hosting flip" = `cmd/osty-native-checker` (Go binary, `internal/selfhost/generated.go` frozen seed 의존) → **LLVM 으로 컴파일된 native binary** 로 production 경로 전환. 결과: `toolchain/*.osty` 모든 수정이 즉시 production 에 반영됨 (현재 dormant 작업 한 번에 활성).

**현재 차단 상태 (2026-06-01 갱신)**: **chicken-egg + production link wall**. `toolchain/*.osty` → LLVM 은 `osty-self` 가 필요하고, fresh clone 은 `OSTY_STAGE0_FALLBACK=1` stage0 source-bootstrap (`just bootstrap`) 로 `osty-self` 를 먼저 만든다. Stage0 **audit** 은 `TestStage0ToolchainAudit` 기준 100% (PR #1858, real-emit hardening #2023) — **audit-pass ≠ build-pass** (`install-self` monomorph / LIR Proto / cross-pkg link). Managed `osty-native-checker` 는 PR #1954 이후 LLVM-built 슬롯을 우선하나 `toolchain.front*` link 가 아직 미완 ([`cmd/osty-native-checker/README.md`](../cmd/osty-native-checker/README.md)).

**단일 PR 로 도전 가능한 첫 단계**: Phase A1 — `lirLowerMirType_module` 폴스루 → 명시적 `lirLowerError` 진단 (~30 LOC, `toolchain/lir_proto.osty`). 직접 효과는 없지만 이후 모든 phase 의 fail site 가시화 → blocker 정확히 겨냥 가능.

**Weeks-scale epic**: Stage0 bootstrap unblock (10–15 PR) + Phase A/B/C/D/E 코딩 (lir_proto.osty / mir_generator.osty 다수 PR). 단일 세션에서 해결 불가능.

## 1. 현재 LLVM 백엔드 capability (verified 2026-05-16)

### 1.1 토대

- 공개 백엔드: LLVM only. 부트스트랩 Osty→Go 트랜스파일러는 PR #854 (2026-04-23) 로 폐기.
- `internal/selfhost/generated.go` (70.6K LOC): frozen seed. 재생성 경로 없음.
- `cmd/osty-native-checker`, `cmd/osty-native-llvmgen`, `cmd/osty-native-lirproto`: Go shim/launcher (합계 ~250 LOC). 실제 emit 은 `osty-self` subprocess.
- `internal/llvmgen` Go 패키지는 **PR #1405 이후 제거**. 모든 MIR→LLVM IR 변환은 `toolchain/lir_proto.osty` (~11k LOC) + `toolchain/mir_generator.osty` 경유.

### 1.2 emit 경로

```
osty build foo.osty
  → Go CLI
    → backend.TryEmitNativeOwnedLLVMIRText
      → subprocess osty-native-llvmgen (Go shim)
        → subprocess osty-self lir-proto-lower
          → toolchain/lir_proto.osty + toolchain/mir_generator.osty
            → LLVM IR text
              → clang → binary
```

핵심: `osty-self` 가 활성 binary 인가 / stage0 fallback emitter 인가 / Go-built `osty-native-checker` override 인가 의 차이. Managed CLI 슬롯은 PR #1954 이후 **LLVM-built** `main.osty` 를 promote 하려 한다 (link 미완 시 `OSTY_STAGE0_FALLBACK=1` detour 또는 `OSTY_NATIVE_CHECKER_BIN`). Steady-state checker wire 는 live `toolchain/*.osty` (`tc.frontCheckSourceToWireJson`).

### 1.3 MIR→LLVM IR coverage (`lir_proto.osty`)

✅ **지원**:
- Primitive type lowering (Int*/UInt*/Bool/Char/Byte/Float*/String/Bytes/RawPtr)
- List/Map/Set intrinsic 기본 경로 (스칼라/String key+value)
- Option/Result aggregate (`{i64 tag, payload}`)
- Struct 레이아웃 (MIR layout → named LLVM type)
- 모든 MIR intrinsic dispatch (141 variant case 분기)

❌ **미지원** (flip 차단):
- Interface 전체 (boxing site, vtable discovery, dispatch — `%osty.iface { ptr data, ptr vtable }`)
- Nested struct binding pattern (`let addr @ Address { city } = ...`)
- Composite container element (`List<Struct>`, `Map<K, Struct>`)
- Generic method turbofish symbol resolution (부분 — IR phase monomorphize 있음)

## 2. Flip mechanism

### 2.1 Direct path (chicken-egg)

```
1. Go binary `osty` (bootstrap, frozen seed) 실행
2. osty build toolchain/check.osty → Go→LLVM 경로로 컴파일 시도
3. 결과 binary = $OSTY_SELF_BIN (또는 registry cache)
4. 그 다음 osty invocation 부터 이 binary 사용 (production flip)
```

**Blocker**: production 경로(2단계, `OSTY_STAGE0_FALLBACK` 없음) 는 `osty-self` LIR Proto subprocess + cross-pkg link 가 필요. Bootstrap 경로(`just bootstrap`) 는 stage0 emitter 로 `install-self` 를 완료할 수 있으나, LLVM-built `osty-native-checker` / registry-only prebuilt 경로는 여전히 `SPEC_GAPS.md` 의 LIR Proto·cross-pkg wall 에 막힌다.

### 2.2 Hybrid partial-flip 가능성

일부 모듈만 LLVM 빌드, 나머지는 generated.go 사용:
- 가능: `osty-native-resolver` 만 LLVM 빌드 (resolver 는 interface 사용 최소)
- 어려움: `osty-native-checker` (check.osty 는 interface, generic, complex pattern 다 씀)

Partial flip 도 **Phase A1 (진단 강화) + B1 (receiver) 가 선행**.

## 3. Top 5 LLVM feature gaps (flip 차단)

| # | Feature | MIR Shape | 영향 toolchain 파일 | 현재 상태 |
|---|---|---|---|---|
| 1 | Interface 전체 | `%osty.iface { ptr data, ptr vtable }`; vtable indirect call | resolve.osty, check.osty, lsp.osty | 0% (silent invalid) |
| 2 | Nested struct binding pattern | Recursive `extractvalue` + `name @ pattern` alias | check.osty, lint.osty | ~10% (MIR shape 존재, LIR Proto emit 미지원) |
| 3 | Map.update canonical closure | `map_incr(map, key, delta)` 직렬화 | pkgmgr.osty, semver_parse.osty | ~30% (Go MIR direct lower, LIR Proto fallback) |
| 4 | Optional aggregate struct | `{i64 tag, %Foo payload}` projection chain (`x?.field`) | check.osty, manifest_validation.osty | ~60% (Phase C1/C2 partial merge) |
| 5 | Generic method turbofish | monomorphized call target selection | resolve.osty, check.osty | ~80% (IR phase monomorphize 있음) |

**공통 근인**: Phase A (type lowering invalid path 진단) 미완료 → 실제 fail site 가시화 불충분 → Phase B/C/D/E 검증 불가능.

## 4. PR 후보들

### Path A — partial flip (작은 모듈부터)

- **A1 — resolver.osty LLVM-native 빌드**: `osty-native-resolver` 독립 binary 증명. Phase A1 + B1 선행. ~80 LOC lir_proto.osty + 30 LOC Go bridge. 위험: Interface 막히면 즉시 fail.

### Path B — feature gap 메우기

- **B1 — Phase A1 type lowering 진단** ⭐ **추천**: `lirLowerMirType_module` 의 silent invalid → `lirLowerError`. ~30 LOC. 이후 모든 phase 의 진입문.
- **B2 — Phase C1/C2 closeout (Optional aggregate)**: lir_proto.osty:2645 에 이미 code 머지됨. osty-self 바이너리만 있으면 verify-only PR.
- **B3 — Phase E (Interface dispatch)**: 4 sub-PR 분할 (E1 type lowering / E2 vtable / E3 boxing / E4 dispatch). 합계 ~600 LOC. 가장 큰 작업.

### Path C — bootstrap unblock

- **C1 — Stage0 P24+ declination 해소**: 10-15 sub-PR. 진행 중 (master plan v2). 모든 phase 검증의 선행 조건. 예상 2-4 주.

## 5. 추천 첫 PR — Phase A1 (~30-50 LOC)

**파일**: `toolchain/lir_proto.osty`, `lirLowerMirType_module` 함수 (~1728 LOC 근처)

**변경**:
```osty
// 현재 (silent invalid):
fn lirLowerMirType_module(ctx, module, typeName) {
    // 폴스루 — 미지원 타입을 그대로 통과시킴
}

// 신규 (명시적 진단):
fn lirLowerMirType_module(ctx, module, typeName) {
    lirLowerError(ctx, module,
        "unsupported module type `{typeName}` in LIR proto lowering — "
        + "Phase A1 진단 (LLVM_BACKEND_GAP_PLAN.md 참조)")
}
```

**효과**:
- 직접 production 효과 없음 (osty-self 못 빌드면 변경 자체가 dormant)
- BUT: `osty install-self` 진행 시 fail site 가 정확히 가시화 → 다음 PR 들이 "실제 첫 wall" 을 정조준
- 위험 극히 낮음 — 기존 로직 불변, 진단 message 만 강화

**LOC**: 30-50 (helper 호출 1 site + 1 regression test fixture)

**위험**: 0 (CI gate 영향 없음, 진단만 변경)

## 6. Phase 마일스톤 현재 상태 (요약)

| Phase | 상태 | 다음 액션 |
|---|---|---|
| **0** Bootstrap chain | 🟡 stage0 audit 100%; production link / LIR Proto wall | `just bootstrap` + [`docs/llvm-selfhost-plan.md`](llvm-selfhost-plan.md) |
| **A** Infra | 🟡 in-progress | A1 첫 PR 준비 |
| **B** Map/List | 🟡 in-progress | LIR Proto 검증 대기 (osty-self 필요) |
| **C** Optional/Result | 🟡 partial-merge | C1/C2 verify (osty-self), C3/C4 coding |
| **D** Generic turbofish | ✅ complete | 차단 해제 시 verify |
| **E** Interface | 🟡 partial-merge | E3/E4 coding (4 sub-PR) |
| **F** Cleanup | 🟡 in-progress | F2 multi-file gate (osty-self) |
| **G** Closure escape | 📋 design | 부트스트랩 후 |

## 7. 결론 — 이번 세션이 의미하는 것

**이번 세션의 dormant 작업들** (287eea05, 2ec78df1, 255552d6, 5dee3b0c, A13 toolchain scaffold) **모두 LLVM self-hosting flip 까지 production 영향 없음**. 이건 잘못된 작업이 아니라 **올바른 작업이지만 활성화 시점이 미래**. flip 자체는:

1. **단일 PR 로 도전 불가능** — chicken-egg + LIR Proto / cross-pkg link + 잔여 phase 미완 (stage0 audit % 는 별도 trajectory).
2. **첫 진전**: Phase A1 진단 강화 (이 세션 외 별도 PR).
3. **전체 일정**: 10–15 stage0 PR + 4 phase code-only PR + bootstrap unblock 검증 → 수 주 단위 epic.

이 문서는 **현재 dormancy 가 architecturally 정상이고 의도된 상태**임을 못박는다. dormant 작업을 더 쌓을 가치는 단일 모듈 진전 (Phase A1) 이 들어와 fail site 가 가시화된 다음에 평가하는 게 효율적.

## 8. 참조

- `LLVM_MIGRATION_PLAN.md` — phase 정의 + 마일스톤
- `LLVM_BACKEND_GAP_PLAN.md` — GAP-IFACE / GAP-INSTR / GAP-RECV / GAP-TYP 카탈로그
- `MIR_EMITTER_PORT.md` — Go MIR → toolchain port 트래킹
- `LLVM_PHASE1_BASELINE.md` — Phase 1 측정 baseline
- `toolchain/lir_proto.osty` — MIR→LLVM IR 변환 본체 (~11k LOC)
- `toolchain/mir_generator.osty` — MIR generation 보조
- `cmd/osty-native-*` — Go shim/launcher 들
- `SELFHOST_PORT_MATRIX.md` 2026-05-16 섹션 — frontend self-host status (이 flip 의 관심사와 구분)
