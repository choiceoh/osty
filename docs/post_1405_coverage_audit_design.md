# Post-#1405 Coverage Audit 설계

> **상태**: 제안 (draft). 합의 후 audit 단계별 개별 PR.
> **연관 PR**: #1405 (Go MIR emitter 제거; 124개 테스트 파일, ~36k줄 삭제), #1406 (디스패처 회귀 봉합 + 추가 12개 obsolete 테스트 삭제).
> **소유**: backend / toolchain.

## 1. 문제 정의

PR #1405가 `internal/llvmgen` (~112K 줄) 제거 시 **124개 테스트 파일, ~36,020줄**의 코드 변환 테스트를 함께 삭제했다. PR 본문은 "MIR emission must be covered by the native LIR Proto subprocess"라고만 적었지만, 그 LIR Proto 측 (`toolchain/lir_proto*.osty`) 에 동등 커버리지가 옮겨졌는지에 대한 증거는 PR 안에 없다.

PR #1406이 그 위에 추가로 12개 backend 테스트를 obsolete 처리 (delete 7건 + skip 5건). 이로써 누적 손실은:

| 카테고리 | 삭제된 줄 수 | 비고 |
|---|---|---|
| `internal/llvmgen/*_test.go` 113 파일 | 약 33,200 | Go MIR emitter 산출물 단언 |
| `internal/backend/stdlib_type_*_test.go` 2 파일 | 1,100 | stdlib body lowering 통합 테스트 |
| `cmd/osty-native-llvmgen/main_test.go` 일부 | 264 | native subprocess 테스트 |
| `cmd/osty/main_gen_test.go` 일부 | 170 | CLI gen 테스트 |
| #1406 추가 삭제 | ~1,200 | 단언 obsolete 7개 + #1406이 추가로 추출 |

**합계 약 36k줄.** 옮겨진 곳을 추정해 보면:

| 후보 새 위치 | 현재 분량 |
|---|---|
| `toolchain/lir_proto.osty` | 6,052 |
| `toolchain/lir_proto_test.osty` | 580 (test fn 28개 미만) |
| `toolchain/lir_proto_parity.osty` | 2,990 |
| `toolchain/mir_generator.osty` | 21,921 |
| `internal/backend/*_test.go` 잔존 | 약 36개 파일 (Go side) |

**관찰**: LIR Proto Osty 측 테스트 본문은 580줄. 삭제 분량의 1/60. 옵션은 두 가지:

(a) 옮겨졌다 — `verify-self-rebuild` 에 의한 indirect end-to-end coverage가 sufficient.
(b) 옮겨지지 않았다 — 단순 부재. 회귀 잡힐 가능성 매우 낮음.

본 audit의 목표는 **(a)/(b)를 카테고리별로 판정**하고, (b)인 항목은 새 테스트로 복구하거나 — 의도된 손실이라면 명문화 — 한다.

## 2. Audit 체크리스트

각 카테고리에 대해:

- [ ] **Owner**: 어디로 이전됐는가? (toolchain/*.osty / internal/backend / cmd/osty-native-* / cmd/osty / 기타)
- [ ] **Coverage shape**: 같은 입력에 대한 같은 단언인가, 다른 입력 / 다른 단언인가?
- [ ] **Activation**: 어떤 명령으로 실행되는가? (`go test`, `osty test`, `verify-self-rebuild`?)
- [ ] **Regression detection**: 옛 테스트가 잡았던 회귀가 새 테스트에서도 잡히는가?
- [ ] **Action**: keep (already covered) / port (move equivalent to new home) / archive (intentional loss + spec note)

## 3. 카테고리별 사전 분석 (초안)

### 3.1 GC instrumentation — `gc_integration_test.go` (440줄)

테스트: safepoint poll, root_bind, pre-write barrier, shadow stack frame.

- 이전 추정: `toolchain/lir_proto.osty` (직접) + `internal/runner` smoke (간접).
- 현황: `toolchain/lir_proto_test.osty` 580줄 안에 GC 관련 fn 몇 개? — 추정 부족.
- **Action 후보**: `toolchain/gc_lower_test.osty` 신설 또는 `internal/backend/llvm_gc_*_test.go` 부활 (skip-on-no-osty-self).

### 3.2 Vectorize — `vectorize_test.go` + `_extended` + `_real_simd` (~1k줄 합)

테스트: `#[vectorize]` / `#[vectorize(scalable, ...)]` / `#[no_vectorize]` 어노테이션이 LLVM `loop.vectorize.enable` metadata + safepoint skip + parallel access groups로 lowering.

- v0.6 A5/A5.1/A5.2/A6/A7 — 핵심 spec. 표지석 테스트가 사라졌다는 건 큰 회귀 위험.
- 현황: `toolchain/lir_proto.osty:` 안에 vectorize lowering 코드는 있지만 테스트는 별도 파일 없음.
- **Action**: 우선순위 최고. 실제 LLVM IR 출력 단언 테스트를 `toolchain/vectorize_test.osty` 또는 backend Go-side 통합 테스트로 부활.

### 3.3 Closure lift — `closure_lift_test.go` (296)

테스트: 자유변수 캡처 → 환경 struct 생성 → indirect call.

- v0.6 spec 핵심 (B.5).
- 현황: 자기-호스트 컴파일 자체가 closure를 사용하므로 `verify-self-rebuild` 가 indirect로 잡음. 단 specific shape (재귀, mutable capture 등)는 미보장.
- **Action**: backend 측 통합 테스트 5-10개 정도로 회복. skip-on-no-osty-self.

### 3.4 Interface downcast — `iface_downcast_test.go` (230)

테스트: `Error.downcast::<T>()` lowering, vtable lookup.

- v0.6 spec 핵심 (A.6).
- 현황: 자기-호스트 컴파일러가 downcast 안 쓰면 `verify-self-rebuild` 로 잡히지 않음.
- **Action**: 회복 필요. 우선순위 중.

### 3.5 Field call dispatch — `field_call_dispatch_test.go` (2,034)

테스트: 메서드 호출 lowering (struct method, generic method, interface dispatch).

- 가장 큰 단일 테스트 파일.
- 자기-호스트가 method를 광범위하게 사용하므로 `verify-self-rebuild` indirect 잡음 비율 높음.
- **Action**: 자기-호스트가 사용하지 않는 패턴 (예: `mut self` 변형 + Option payload 메서드) 중심으로 회복. 우선순위 중.

### 3.6 Match patterns — `match_*_test.go`, `enum_*_test.go`, `option_match_*` (~1.5k줄 합)

테스트: 모든 패턴 종류 (literal / range / or / binding / guard / payload destructure) lowering.

- v0.6 spec A.5.
- 자기-호스트가 일부 패턴만 사용 → 광범위 indirect coverage 불가.
- **Action**: spec corpus 와 결합. 우선순위 중-높음.

### 3.7 Defer — `defer_test.go` (165)

테스트: defer LIFO + `?` propagation 시 실행 + cancel 시 실행 + `panic`/`unreachable`/`todo`/`abort` skip.

- v0.6 spec A.6 (특히 panic 시 skip vs cancel 시 실행 — 미묘함).
- 자기-호스트가 defer를 사용하지만 defer × cancel × panic 조합은 안 씀.
- **Action**: 회복 필요. 우선순위 중.

### 3.8 GC tail sweep — `large_tail_sweep_test.go` (490)

테스트: 큰 모듈에서 GC root scan / tail safepoint.

- 성능 / 정합성 핵심.
- 자기-호스트 컴파일이 큰 모듈이긴 하지만 specific GC 동작 (예: tail-safepoint trigger 시점)을 단언하지 않음.
- **Action**: 회복 필요. RUNTIME_GC.md / RUNTIME_SCHEDULER.md 참조 후 재설계.

### 3.9 LIR Proto shadow parity — `lir_proto_shadow_parity_test.go` (619)

테스트: Gate ON / OFF 일 때 IR 결과가 byte-identical.

- **이미 의미 상실**: gate OFF 측이 (Go MIR emitter) 가 사라졌으므로 비교 대상이 없음.
- **Action**: archive (의도된 손실).

### 3.10 stdlib body lowering integration — `stdlib_type_e2e_test.go` (534) + `stdlib_type_inject_test.go` (566)

테스트: stdlib `*.osty` body가 사용자 코드에 inject 되어 monomorphize 되는 흐름.

- 매우 중요 — `internal/backend/stdlib_type_inject.go`는 살아있지만 그 통합 테스트가 사라짐.
- 자기-호스트가 `List<T>.map` 류 helper를 사용하므로 부분 indirect coverage 있음.
- **Action**: 우선순위 최고. backend 통합 테스트로 복원.

### 3.11 nativellvmgen / nativelirproto wire shape — `cmd/osty-native-llvmgen/main_test.go` 264줄 일부

테스트: subprocess JSON wire format, declined response handling.

- `internal/nativellvmgen` / `internal/nativelirproto` 패키지에 일부 잔존 (현재 통과).
- **Action**: 잔존분 점검만 필요. 대부분 보존된 것으로 추정.

### 3.12 mir_generator self-tests — `mir_generator_test.go` (7,240)

테스트: 가장 큰 단일 파일. 모든 MIR 패턴이 → LLVM IR 로 변환되는지 단언.

- **이전됐을 후보**: `toolchain/lir_proto_parity.osty` (2,990) + `toolchain/lir_proto_test.osty` (580) — 합쳐도 1/2 미만.
- 자기-호스트 컴파일이 광범위 indirect coverage 제공하지만, **단일 패턴 회귀의 신호**는 약화됨.
- **Action**: spec corpus 측 강화. 자기-호스트가 사용하지 않는 패턴 (예: empty enum, large tuple, complex generic constraints) 식별 후 부분 복원. **본 audit의 가장 큰 work item**.

## 4. 권장 audit 순서

| 우선순위 | 카테고리 | 예상 work | 결과물 |
|---|---|---|---|
| P0 | 3.10 stdlib body integration | 1주 | backend 측 통합 테스트 ~10개 |
| P0 | 3.2 vectorize (v0.6 A5/A6/A7 spec) | 1주 | vectorize 어노테이션별 테스트 ~15개 |
| P1 | 3.12 mir_generator self-tests audit | 2-3주 | spec corpus 강화 + 빈 패턴 식별 |
| P1 | 3.7 defer + 3.6 match patterns | 1주 | 패턴별 회복 ~20개 |
| P2 | 3.1 GC, 3.3 closure, 3.4 iface, 3.5 field call | 1-2주 | 카테고리별 5-10개씩 |
| P2 | 3.8 GC tail sweep | 1주 | RUNTIME_GC delta 분석 |
| P3 | 3.9 shadow parity archive | 즉시 | doc note + 카탈로그 update |
| P3 | 3.11 native wire shape 잔존 점검 | 1일 | 점검 보고서 |

## 5. Audit 산출물 형태

각 카테고리에 대해 별도 PR:
1. **검증** — 옛 테스트 의도와 새 위치 매핑 표
2. **gap 식별** — 옛 단언 중 새 위치에서 cover되지 않는 항목 목록
3. **복구** — 새 테스트 추가 (backend Go-side 또는 toolchain Osty-side, 위치는 카테고리별 결정)
4. **명문화** — 의도된 loss 인 경우 doc 추가 (`docs/post_1405_archive_*.md` 또는 `SPEC_GAPS.md`)

진행 트래커는 본 design doc 의 "카테고리별 사전 분석" 섹션을 그대로 사용 (체크박스 갱신).

## 6. 결정 필요 항목

1. **새 테스트 위치** — backend 측 (`internal/backend/llvm_*_test.go` Go side) 인가, toolchain 측 (`toolchain/*_test.osty` Osty side) 인가?
   - Go side 장점: 기존 `newBackendRequest`, `fakeLLVMToolchain` 인프라 재사용. 빠른 turnaround.
   - Osty side 장점: CLAUDE.md "Osty 우선" 정신. self-host parity.
   - **권장**: integration 테스트는 Go side (backend) 에, lowering shape 단언은 Osty side (`toolchain/*_test.osty`) 에 분산.

2. **`requireRealLLVMEmission` skip 정책**: 새 테스트도 osty-self 부재 시 skip할지, 아니면 stub 가능한 형태로 설계할지.
   - osty-self bootstrap 설계 (별도 doc) 와 연동.

3. **archive vs 복원 경계** — `lir_proto_shadow_parity_test.go` 같이 의미 상실된 테스트는 archive. 그 외 어디까지 복원할지 (모든 카테고리? P0/P1만?).

4. **CI 추가 부담** — 카테고리별 P0+P1 복구가 진행되면 backend test 시간 증가. 어떻게 수용할 지.

---

## Appendix A. 삭제된 테스트 카테고리 raw size

```
mir_generator_test.go             7240
osty_generated_test.go            1630   (generated 시드 테스트 — likely archive)
ir_native_entry_test.go           1414
field_call_dispatch_test.go       2034
ir_module_test.go                  862
lir_proto_shadow_parity_test.go    619
stdlib_type_inject_test.go         566
stdlib_type_e2e_test.go            534
large_tail_sweep_test.go           490
gc_integration_test.go             440
char_byte_lowering_test.go         334
closure_lift_test.go               296
multifile_probe_test.go            317
match_stmt_test.go                 368
optional_test.go                   387
... (총 124 파일)
=================================
                                  36020  줄 합계
```

## Appendix B. 잔존 / 신규 새 위치 raw size

```
toolchain/lir_proto.osty          6052
toolchain/lir_proto_parity.osty   2990
toolchain/lir_proto_test.osty      580
toolchain/mir_generator.osty     21921   (구현; 테스트 아님)
internal/backend/llvm_*_test.go  ~36 파일 (Go side; 일부는 #1406이 stub/skip 처리)
=================================
잔존 단언 라인 수 추정             < 5000 (실제 테스트 fn 단언 코드만)
```

라인 수 비율 (~5000 / 36020 ≈ 14%) 만으로 단정하긴 어렵지만, **자릿수가 일치하지 않는다는 점은 audit의 출발점이 될 만하다**.
