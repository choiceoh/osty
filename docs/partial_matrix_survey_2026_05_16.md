# `partial` matrix items — actionability survey (2026-05-16)

- **Status**: planning aid, not authoritative.
- **Scope**: `SELFHOST_PORT_MATRIX.md` 의 "partial" 표기 행 + 사용자가 명시한 1c.5 외부 후보들. 각 항목을 "단독 작은 PR 가능 vs 큰 design 선행" 으로 분류.
- **Goal**: 다음 1-3 PR 의 후보를 빠르게 고르는 카탈로그.

## 분류 표

| # | 항목 | Scope (LOC) | Side | 선행 의존 | 단독 PR? | 비고 |
|---|---|---|---|---|---|---|
| 1 | Interface default-body | 150–300 | Osty | 없음 | ✅ | `check_env.osty` 의 inheritance 구조 활용, default body lookup fallback 추가 |
| 2 | Annotation LLVM emit (A5/A8/A9/A10/A11/A13) | 100–250 (split) | Osty | 없음 | ✅ × 6 PR | 아래 §3 상세 |
| 3 | Defer lifecycle (runtime) | 500+ | Osty + Go + spec | SPEC_GAPS entry | ✗ | Cross-cutting backend; spec decision 선행 |
| 4 | 숫자 리터럴 다형성 | 50–100 | Osty | 없음 | ✅ 1차 착륙 2026-05-16 (arith 전파) | `elabCheckBinary` 추가로 `let x: T = a op b` 의 양 피연산자에 T 전파. 비-arith 는 infer→subtype 유지 |
| 5 | Closure annotation requirement (E0752) | 80–150 | Osty | 없음 | ✅ | param seeding 만 구현 — closure elab 범위 확장 |
| 6 | Raw-ptr handling (privilege+POD) | 200–400 | Osty | 없음 | ⚠️ | 3-gate 통합 권장; 분할 시 conflict 위험 |
| 7 | File/Package/Workspace 진입점 정리 | 100–200 | Osty | 없음 | ✅ | Go exec wrapper 완료, Osty phase 정리만 |
| 8 | Diagnostic file-path stamping | 50–100 | Osty + Go | 없음 | ✅ | Go `stampPackageDiags` 의 Osty 측 이관 |
| 9 | Inspect missing shapes (for-loop / field-chain / receiver) | 200–400 (split) | Osty + Go | v1.11 done | ⚠️ × 3 PR | 각 shape 50-150 LOC |
| 10 | Workspace package input / worker pool | 300–600 | Go-only | Osty IO/scheduling design | ✗ | 호스트 경계 인프라; 최후 |
| 11 | E0553 `pub use` visibility | 30–50 (Path A) / 150-200 (Path B) | Go (A) / Osty+Go (B) | scoped G28 (B) | ✅ (A) | [별도 design 문서](e0553_pub_use_design.md) |
| 12 | Partial decl cross-file stitching | 80–130 | Osty | 없음 | ✅ | [별도 design 문서](partial_decl_cross_file_design.md) |

**Side 표기**:
- `Osty` = `toolchain/*.osty` 수정 중심, Go bridge 최소
- `Go` = `internal/` Go 코드 중심
- `Osty + Go` = 양쪽

## TOP 3 단독 작은 PR 후보 (가장 빨리 끝낼 수 있는 것)

### 1. **숫자 리터럴 다형성** (50–100 LOC, Osty) — **1차 착륙 2026-05-16**
`toolchain/elab.osty` 에 `elabCheckBinary` + `binOpIsArithmetic` 추가. `elabCheckImpl` dispatch 에 `AstNBinary -> elabCheckBinary` 등록. 효과: `let x: Float64 = 1 + 2` 의 `1` / `2` 가 UntypedInt 거치지 않고 직접 Float64 채택. 비-arith op (`==` / `&` / `<<` / `??` 등) 과 비-numeric expected 는 기존 infer→subtype fallback 유지. 후속 가능한 확장: 함수 인자 context narrowing (현재 `elabInferCall` 가 expected 받지만 numeric 리터럴 별도 처리 없음), unary `-1` 의 expected 전파.

### 2. **Closure annotation requirement (E0752)** (80–150 LOC, Osty)
`toolchain/elab.osty:1849` 가 param seeding 만 한다. annotation 검증 부족. closure elab 격리 범위, E0752 code 가 이미 정의. 선행 의존 없음. **session 1-2 회**.

### 3. **E0553 Path A** (30–50 LOC, Go) — [docs/e0553_pub_use_design.md](e0553_pub_use_design.md)
wording / 진단 helper / unit test 가 다 만들어진 채 emit 사이트가 0. `internal/resolve/workspace.go::detectCycles` 직후 단일-홉 `pub use pkg.X` 만 검증해도 negative 코퍼스 회귀 잠금 가능. **session 1 회**.

## TOP 2 손대지 말 것 (Spec / Design 선행)

### A. **Defer runtime lifecycle**
정적 검증 (E0603 / E0608 / L0007) 은 Go + Osty 양쪽 완료. 런타임 LIFO / `?` propagation / cancellation / abort-skip 은 LANG_SPEC §4.12 rules 3/7/8 의 backend cross-cutting. MIR / LLVM / GC runtime 동시 수정 필요. **SPEC_GAPS entry 가 선행** — `defer-runtime` 항목으로 spec decision 명시 후 phase 분할.

### B. **Workspace package input / worker pool**
Osty 측에 파일 IO + 스케줄링 인프라가 아직 없다. `internal/check/package_input.go` 의 fingerprint 캐시 + worker pool 모델을 Osty 로 옮기려면 별도 design (Osty CLI async/parallel 모델) 필요. 1c.5 의 가장 뒤로 미루는 게 정답.

## §3. Annotation LLVM emit — 6 PR 시리즈 후보

매트릭스의 "Annotation semantic validation" 항목은 resolve / HIR / MIR 까지 완료고 **LLVM emit 만 남음**. `toolchain/llvmgen.osty` 의 `llvmNativeFnAttrString` (line ~963) 이 fn-attr 의 중앙 emit 지점인데 현재는 `inlineMode` (A8) 만 처리. 주석에 "(e.g. features/noalias) land in this helper as they gain Osty-emitter" 라고 명시 — 후속 hook 위치가 이미 표시됨.

| Annotation | Spec | Emit 메커니즘 | 추정 LOC | PR 후보 |
|---|---|---|---|---|
| A5 vectorize/parallel/unroll | loop metadata `!llvm.loop.vectorize.enable` 등 | per-loop metadata, MIR loop emission site | 60–100 | PR#4 |
| A8 `#[inline]` modes | fn-attr `inlinehint` / `alwaysinline` / `noinline` | `llvmNativeFnAttrString` (이미 부분 구현) | 0 (done) | — |
| A9 `#[hot]` / `#[cold]` + section | fn-attr + `.section .text.hot` directive | function 정의 emit site | 30–50 | PR#5 |
| A10 `#[target_feature]` | `"target-features"="+avx2,..."` fn-attr | `llvmNativeFnAttrString` | 40–60 | PR#2 |
| A11 `#[noalias]` / `#[noalias(p1, p2)]` | param-level `noalias` attr | param emit site | 30–50 | PR#3 |
| A13 `#[pure]` | `readnone` fn-attr | `llvmNativeFnAttrString` + stage0 `fnDefineHeaderClose` | 20–30 + 30 (stage0) | ✅ **production 까지 착륙 2026-05-16** (scaffold + stage0 retrofit) |

**A13 production 까지 착륙 2026-05-16**:
- Scaffold (`toolchain/llvmgen.osty`): `LlvmNativeFunction.pure: Bool` 필드 + `llvmNativeFnAttrString` 가 inlineAttr 와 `readnone` 을 space-join. future Osty-native llvmgen path 용.
- Production (`internal/backend/stage0/emit.go`): `fnDefineHeaderClose(fn *mir.Function) string` helper 추가. 19 sites 의 `out.WriteString(") {\n")` 를 helper 호출로 일괄 교체. `fn.Pure` 일 때 `") readnone {\n"` 발화. 테스트: `TestStage0EmitsReadnoneForPureFn` + `TestStage0SkipsReadnoneForNonPureFn` (baseline byte-identical 보장).
- 후속 가능: 같은 `fnDefineHeaderClose` helper 안에 A8 inline / A9 hot/cold / A10 target-features / A11 noalias / A5 vectorize-related metadata 추가. 모두 단일 진입점.

**중앙 helper 일반화 PR**: `llvmNativeFnAttrString` 을 `[]string` 누적 모델로 refactor (A8 + A10 + A11 + A13 을 한 곳에서 조립). A13 PR 직후 1 회. 50 LOC.

**Per-loop metadata PR (A5)**: MIR loop emission site 별도 위치 — 다른 helper. A5 / unroll / parallel 3 종 한 PR (60-100 LOC).

**Section directive PR (A9)**: function 정의 헤더 직전 `.section` directive 출력. 분리된 emit site. 30-50 LOC.

권장 순서: PR#1 (A13) → PR#2 (A10) → PR#3 (A11) → PR#4 (A5 loop metadata) → PR#5 (A9 section) → 통합 negative 회귀.

## §4. 권장 단기 sequencing

이 survey 의 결과로 다음 1-3 sessions 에서 액션 가능한 후보 (small-first 순):

1. **PR α (1 session)**: E0553 Path A — 단일-홉 `pub use` visibility 검증. 30-50 LOC Go. emit 사이트 0 → 1 로 만드는 게 핵심.
2. **PR β (1-2 sessions)**: A13 (pure → readnone) + `llvmNativeFnAttrString` 일반화 refactor. 50-80 LOC Osty.
3. **PR γ (1-2 sessions)**: 숫자 리터럴 다형성 확장. 50-100 LOC Osty.

이후로는 partial cross-file stitching (Phase B.1+B.2, 80-130 LOC Osty — [별도 design](partial_decl_cross_file_design.md)) 가 자연스러운 다음 후보.

## §5. 참조

- `SELFHOST_PORT_MATRIX.md` (특히 Checker 매트릭스의 partial rows + 2026-05-16 잔여 작업 재프레이밍)
- `docs/e0553_pub_use_design.md` — E0553 Path A/B 설계
- `docs/partial_decl_cross_file_design.md` — R19 cross-file 설계
- `LLVM_MIGRATION_PLAN.md` — Phase 번호 권위
- `toolchain/llvmgen.osty:955-964` — fn-attr emit 중앙 지점
- `toolchain/check_env.osty:144-435` — interface inheritance 인프라
- `toolchain/elab.osty:273` — 숫자 리터럴 narrowing
- `toolchain/elab.osty:1849` — closure annotation 부분 구현
