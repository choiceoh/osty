# SELFHOST_PORT_MATRIX.md

Go → Osty 셀프호스팅 포팅 현황 매트릭스. 리졸버 / 체커 잔여 작업을 항목 단위로 분류.

> **Scope**: `internal/resolve` ↔ `toolchain/resolve.osty`, `internal/check` ↔
> `toolchain/check*.osty` + `toolchain/elab.osty` + `toolchain/hir_lower.osty`.
> 기준일: 2026-04-23. 최근 관련 커밋: `#727` (NodeID-keyed resolve/check maps),
> `#728` (HIR/MIR bundles + transpile smoke), `#741` (self-host HIR lowering
> complete).

## 2026-04-23 — Phase 1c.1 flip landed

`cmd/osty` 의 `check` / `typecheck` 기본 경로가 **self-host arena 파이프라인**
(`ParseRun → CheckStructuredFromRun → CheckDiagnosticsAsDiag`) 로 전환되었다.
Go-native `runCheckFileLegacy` / `runCheckPackageLegacy` 경로는 `--legacy`
플래그로만 진입 가능. 역사적인 `--native` 플래그는 기본값이 된 지금도 backwards
compat 로 수락된다 (no-op).

영향 범위:
- `osty check FILE` / `osty check DIR` → self-host default
- `osty typecheck FILE` / `osty typecheck DIR` → self-host default (DIR 는
  native 전용, `typecheck --legacy DIR` 는 exit 2 로 거부)
- `osty resolve` 는 2026-04 시점부터 이미 self-host 우선이라 이번 flip 의
  직접 영향 없음
- `osty check --legacy --no-airepair FILE` 로 남은 parser-owned 헬퍼
  (`enumerate`, `len`, `append` 의 함수형 surface) 는 아직 `parser.ParseRun`
  이 `stableLowerer` fixup 을 적용하지 않아 self-host 가 거부 —
  `airepair_test.go` 의 관련 케이스 2 건은 임시로 `--legacy` 로 강제
  (parser-lowering 포팅이 해제 조건)

회귀 테스트:
- `TestCheckCLIDefaultPathExitsZero` — `osty check FILE` 기본 경로 exit 0
- `TestCheckCLILegacyOptOutStillWorks` — `--legacy` escape hatch 검증
- `TestRunCheckFileDefaultPathIsAstbridgeFree` — astbridge 카운터 0

## 1c 로드맵 — Go resolver 제거까지

매트릭스 `ported` 가 "Osty 가 구동" 이라면, 1c 목표 상태는 "Go 소스 자체가
제거됨". 진행은 5 단계로 쪼개 세션 단위로 착륙한다.

- **1c.1** ✅ `cmd/osty` `check`/`typecheck` default flip + `--legacy` escape
  hatch (2026-04-23 착륙, #755).
- **1c.2** ✅ **arena-first loader 수용 (2026-04-23 착륙)**.
  `resolve.LoadPackageArenaFirst` / `(*Workspace).LoadPackageArenaFirst` +
  `(*Package).MaterializeCanonicalSources` 도입. `internal/pipeline` ·
  `cmd/osty/{build,run,doc,main}` · `internal/cihost` · `internal/lsp` ·
  `cmd/osty-native-llvmgen` 가 전부 arena-first 경유로 이식. 파싱은
  `selfhost.Run` 단일 진입점, `pf.File` / `pf.CanonicalSource` 는
  EnsureFiles + MaterializeCanonicalSources 로 lazy 생성. Go-hosted
  `ResolveAll` / `check.Workspace` 는 변경 없음 — 선행 adapter 로 계획된
  `selfhost.ResolveResult → RefsByID` translation 은 1c.4 에서 담당.
- **1c.3** ⏳ (현 matrix 기준 상당 부분이 이미 `ported` — #754 import-surface
  이식, #757 annotation validators 이식). 잔여: `osty-only-unwired`
  refNames/refTargets 가 native_adapter 에 직결되어 자체 형식 translation
  이 사라지는 것, partial struct/enum merge 를 cross-file 로 확장하는
  workspace-level pass.
- **1c.4** ⏳ `check.File(file, res, opts)` 시그니처 제거. 모든 call site 가
  `selfhost.CheckStructuredFromRun` 으로 통일. `check.Workspace` 는 얇은
  orchestrator 로 축소. check 내부 helper 들 (`privilege`, `podshape`,
  `noalloc`, `builder_desugar`) 은 `check_gates.osty` 로 이식이 완료된
  시점에서 삭제.
- **1c.5** ⏳ `internal/resolve/resolve.go` (body-walk resolver),
  `internal/resolve/cfg.go` (legacy cfg pre-filter),
  `internal/resolve/prelude.go` (Go-side prelude builder),
  `internal/resolve/scope.go` (depth-based Go scope), `LoadPackage` /
  `ResolveAll` (Go-host) 전부 삭제. `--legacy` 플래그 제거. 매트릭스의
  `ported` 행 전부 "완료".

각 phase 의 끝에 `just full` + 매뉴얼 `osty check toolchain` smoke 로
regression 없음을 확인하고 이 문서의 ⏳ → ✅ 전환.

## Bridge 모델 (둘 다 dual-track)

양쪽 파이프라인 모두 "native 바이너리 / embedded selfhost / Go-native fallback"
세 갈래가 공존한다:

- **Resolver**
  - `internal/resolve/workspace.go::LoadPackage` → Go-native resolver (레거시)
  - `internal/resolve/workspace_native.go::LoadPackageNative` → `selfhost.LowerPublicFileFromRun`
  - `internal/resolve/native_adapter.go` → `selfhost.ResolvePackageStructured` (Osty `resolve.osty` 구동)
- **Checker** (`toolchain/README.md`에 문서화됨)
  - `internal/check/host_boundary.go::nativeCheckerExec` → 외부 `osty-native-checker`
  - `embeddedNativeChecker` → `selfhost.CheckPackageStructured` (Osty `check.osty` + `elab.osty` 구동)
  - Go-native fallback은 주로 gate/analysis (`privilege.go`, `podshape.go`, `noalloc.go`, `intrinsic_body.go`, `inspect.go`)

따라서 "포팅 완료" 의 실질은 **Go 레거시 제거** 이고, 매트릭스의 `state` 는 그
기준으로 읽을 것.

## Legend

- `ported` — Osty 가 해당 로직을 구동하며 Go 쪽은 facade/adapter
- `partial` — 양쪽에 overlapping/diverged 구현 존재
- `go-only` — 아직 Go 에만 있음
- `osty-only-unwired` — Osty 에 있으나 consumer 파이프라인이 사용 안 함
- `n/a` — 호스트 경계 infra (포팅 대상 아님)

---

## 1. Resolver 매트릭스

| Feature | Go locations | Osty locations | Diag codes | State | Notes |
|---|---|---|---|---|---|
| Top-level name declaration | `internal/resolve/resolve.go:130` | `toolchain/resolve.osty:411` | E0400 | ported | Osty `srAstCollectTopLevel` 가 구동, native_adapter 가 소비 |
| Scope construction / 렉시컬 중첩 | `internal/resolve/scope.go:1` | `toolchain/resolve.osty:327` | — | ported | 양쪽 depth-based 동일 모델 |
| Symbol lookup / shadowing | `internal/resolve/scope.go:120` | `toolchain/resolve.osty:397` | — | ported | flat list + depth check |
| 식별자 참조 해석 | `internal/resolve/resolve.go:700` | `toolchain/resolve.osty:1141` | E0500 | ported | `RefsByID` 로 bridge |
| 타입 참조 head symbol | `internal/resolve/resolve.go:500` | `toolchain/resolve.osty:1227` | E0502–E0504 | ported | `TypeRefsByID` bridge |
| `use` / import alias | `internal/resolve/resolve.go:326` | `toolchain/resolve.osty:419` | E0501 | ported | Osty kind="package" + Go `UseKey` loader |
| Did-you-mean (E0400) | `internal/resolve/resolve.go:1900` | `toolchain/resolve.osty:2260` | E0400 | ported | commit `ccfb7808` (Osty 가 Go 초월) |
| 함수 바디 해석 | `internal/resolve/resolve.go:800` | `toolchain/resolve.osty:659` | — | ported | — |
| 블록/문 해석 | `internal/resolve/resolve.go:1100` | `toolchain/resolve.osty:684` | — | ported | inFn/inLoop context |
| 컨트롤 플로우 검증 (return/defer) | `internal/resolve/resolve.go:1400` | `toolchain/resolve.osty:713` | E0600, E0601 | ported | commit `ca3605a1` |
| Loop-label 해석 | `internal/resolve/loop_label_test.go:1` | `toolchain/resolve.osty:834` | E0763, E0764 | ported | shadow check 포함 |
| Wildcard-in-expr 거부 | `internal/resolve/resolve.go:1500` | `toolchain/resolve.osty:1310` | E0604 | ported | commit `c8c8c551` |
| Annotation target validation | `internal/resolve/resolve.go:551` | `toolchain/resolve.osty:484` | E0607, E0609 | ported | commit `a1108c1e` |
| Annotation arg validation | `internal/resolve/runtime_annot_test.go:1` | `toolchain/resolve.osty:1780` | E0739 | ported | commit `c38fa9d4` |
| Interface default-field ctx | `internal/resolve/resolve.go:?` | `toolchain/resolve.osty:1324` | E0602, E0603 | ported | `ifaceDefault` flag |
| Closure scope isolation | `internal/resolve/resolve.go:900` | `toolchain/resolve.osty:1124` | E0605, E0606 | ported | inLoop 리셋 |
| **Partial struct/enum merge** | `internal/resolve/merge.go:47` | `toolchain/resolve.osty::srHandlePartialType` | E0501 (R19 variants) | **partial** | 2026-04-23 Osty 측 `SelfPartialDecl` + R19 검증기 (pub/generics/fields/method dup) 착륙. 현재 single-file 범위만; cross-file stitching 은 workspace pass 모델 생기면 확장 |
| **Workspace cycle detection** | `internal/resolve/workspace.go:468` | `toolchain/resolve.osty::selfDetectImportCycles` | E0506 | **ported** | 2026-04-23 DFS (tri-state colour) Osty 이식, Go 쪽은 그래프 prep + token.Pos 복원 + diag 렌더링 glue 만 남음 |
| **Workspace 2-pass (declare/body)** | `internal/resolve/workspace.go:356` | — | — | n/a | 호스트 경계 — `internal/selfhost/workspace_driver.go` 쪽에서 담당 |
| **#[cfg] 사전 필터** | `internal/resolve/cfg.go:69` | `toolchain/resolve.osty::srCfgDeclPasses` | E0405, E0739 | **partial** | 2026-04-22 native 경로 Osty 구동 (srCfgDeclPasses + selfResolveAstFileWithCfg). Go `cfg.go` 는 레거시 `ResolveAll` 에 남아있음 — 레거시 retire 시 제거 |
| **Package visibility 강제** | `internal/resolve/resolve.go:410` | — | E0553, E0770 | **go-only** | `ResolveUseTarget` 가 pub 체크 |
| Partial-decl method name 유일성 | `internal/resolve/merge.go:100` | — | — | go-only | partial struct 메서드명 중복 금지 |
| Self-host ref tracking (refNames/Targets) | — | `toolchain/resolve.osty:1270` | — | osty-only-unwired | Osty 가 emit 하지만 adapter 가 다른 형식으로만 쓴다 |
| Generic arity 기록 | — | `toolchain/resolve.osty:510` | — | ported | 양쪽 동일 |

### Resolver 포팅 우선순위

1. ~~**`#[cfg]` 사전 필터** (E0405)~~ — **착륙 2026-04-22**. `toolchain/resolve.osty` 의 `srCfgDeclPasses` + `selfResolveAstFileWithCfg` + `srCheckCfgArgs` 로 이식, `PackageResolveInput.Cfg` / `ResolveSourceStructuredWithCfg` 로 bridge. native 경로는 `workspace.cfgEnv` / `DefaultCfgEnv()` 자동 상속. 레거시 `internal/resolve/cfg.go` 는 `ResolveAll` 경로가 살아있는 동안 유지.
2. ~~**Partial struct/enum merge** (E0509/E0501)~~ — **착륙 2026-04-23**. `SelfPartialDecl` 트래커 + `srHandlePartialType`, R19 4 invariants (pub / generics / fields-in-one / method name uniqueness) 검증. 현재는 single-file (native_adapter 가 `ResolvePackageStructured` 로 다중 파일을 synthetic 단일 네임스페이스로 합쳐 통과). True cross-file stitching 은 workspace 모델 등장 이후.
3. ~~**Workspace cycle detection** (E0506)~~ — **착륙 2026-04-23**. `selfDetectImportCycles` + `SelfWorkspaceUses` / `SelfPackageUses` / `SelfUseEdge` / `SelfCycleDiag` (toolchain/resolve.osty). `selfhost.DetectImportCycles` Go bridge. `internal/resolve/workspace.go::detectCycles` 는 그래프 준비 + offset 기반 roundtrip 후 `token.Pos` 복원 + 진단 렌더링 glue 만 남김. 알고리즘 (DFS 3-상태 색칠) 은 완전히 Osty 쪽.
4. **Package visibility 강제** (E0553/E0770) — `pub use` re-export 검증 포함. cross-package 가시성 그래프 필요 — cycle detection 과 같은 workspace 레이어 위에 올린다.
5. **`osty-only-unwired` refNames/refTargets** — adapter 가 이미 자체 형식으로 번역 중이라, Osty 측 emit 을 소비하도록 adapter 를 슬림하게 바꾸면 bridge 면적이 줄어든다. 버그픽스성 작업.

---

## 2. Checker 매트릭스

| Feature | Go locations | Osty locations | Diag codes | State | Notes |
|---|---|---|---|---|---|
| Bidirectional type inference | `(hostboundary)` | `toolchain/elab.osty:§2a.3` | E0700–E0757 | ported | Osty 구동, Go 는 JSON bridge |
| Generic monomorphization | `(hostboundary)` | `toolchain/elab.osty:53` | — | ported | call site 별 fresh Solver |
| 메서드 dispatch (struct/enum/iface) | `(hostboundary)` | `toolchain/elab.osty:1550` | E0703 | ported | `checkSpecializeMethodSelf` |
| Interface default-body | `(hostboundary)` | `toolchain/check_env.osty` | — | **partial** | 상속은 기록, default body 코드패스 빈약 (`269eebf8` 가 일반화 진행중) |
| `?` 전파 (Option/Result) | `(hostboundary)` | `toolchain/elab.osty:161,1360` | E0717 | ported | `TkOptional` intern |
| `?.` optional chain | `(hostboundary)` | `toolchain/elab.osty:432` | — | partial | 타이핑 존재, 테스트 커버리지 얕음 |
| `??` nil-coalesce | `(hostboundary)` | `toolchain/elab.osty:432` | — | ported | Option unwrap 특수 |
| **Defer lifecycle** | `internal/check/*.go` (분산) | — | — | **go-only** | Osty 쪽 검증 없음; 백엔드 의존성 설계 필요 |
| 패턴 exhaustiveness | `(hostboundary)` | `toolchain/elab.osty:2669` | E0712 | ported | witness 생성 phase 1, opaque range phase 2 |
| 패턴 reachability | `(hostboundary)` | `toolchain/elab.osty:3171` | E0740, E0741 | ported | guarded pattern 추적 |
| 숫자 리터럴 다형성 | `(hostboundary)` | `toolchain/elab.osty:273` | — | partial | 기대 타입 기반 narrowing, 범위 좁음 |
| Closure capture / escape (E0743) | `(hostboundary)` | `toolchain/elab.osty:1849` | E0743 | **partial** | param seeding 은 되지만 escape analysis 는 phase 2 남음 |
| `Self` specialization | `(hostboundary)` | `toolchain/elab.osty:1700` | — | ported | `6cd17fa2` 최근 착륙 |
| Annotation semantic validation | `(hostboundary)` | `toolchain/resolve.osty` (v0.6 arg validators) + `toolchain/hir_lower.osty:923-950` (extract) + `toolchain/mir.osty::MirFunction` + `toolchain/mir_lower.osty::mirLowerCopyFnAnnotations` (HIR→MIR 전달) | E0739 | **partial (LLVM-emission 남음)** | 2026-04-23 resolve 단계 v0.6 arg validator 10 종 전부 이식. HIR extract 계층 + HirFnDecl 필드 10 종 landing 완료. **2026-04-23 후속**: MirFunction 이 9 annotation 그룹 필드 (`vectorize[Width/Scalable/Predicate]` / `noVectorize` / `parallel` / `unroll[Count]` / `inlineMode` (MirInlineMode enum) / `hot` / `cold` / `targetFeatures` / `noaliasAll` / `noaliasParams` / `pure`) 을 HirFnDecl 로부터 `mirLowerCopyFnAnnotations` 가 lossless 복사 — Go `ir.FnDecl` shim 이 소비할 수 있는 단일 MIR-level 소스 완성. **잔여**: `toolchain/llvmgen.osty` 가 이 MIR 필드를 LLVM fn-attr / loop-metadata 로 emit 하는 경로 (A5 loop md / A8 fn attr / A9 section 배치 / A10 target-features / A11 noalias param attr / A13 readnone). 현재는 Go `internal/llvmgen` 쪽이 emit 을 전담 — Osty native LLVMgen 은 여전히 scalar-only |
| **Builder auto-derive** | `internal/check/builder_desugar.go` | `toolchain/hir_lower.osty:684` | E0774 | **partial** | Go 는 AST 레벨 desugar, Osty 는 HIR 레벨 qualify — **중복 포팅 필요** |
| **ToString auto-derive** | — | — | — | — | 양쪽 미구현. 스펙 요구 여부 확인 필요 |
| **Privilege system** | `internal/check/privilege.go` (33 줄, `isPrivilegedPackage[Path]` 헬퍼만 잔존) | `toolchain/check_gates.osty:376` | E0770 | **ported** | 2026-04-23 PR #770 로 호출부 제거, 후속 PR 로 Go duplicate walker + parity harness + 전용 test 전부 제거. Osty `runCheckGates` 가 authoritative. 동시에 `check_gates.osty::privilegeWalkTypeAlias` 의 `children2` → `children` 버그 수정 (TypeAlias 는 generics 를 `children` 에 둠 — §19.4 Pod-bound 이 type alias 에서 누락되던 실버그 해소) |
| **POD shape analysis** | (Go duplicate 제거됨) | `toolchain/check_gates.osty:146` | E0771 | **ported** | 2026-04-23 same consolidation. Go `podshape.go` / `podshape_test.go` 전부 제거 |
| **Noalloc analysis** | (Go duplicate 제거됨) | `toolchain/check_gates.osty:275` | E0772 | **ported** | 2026-04-23 same consolidation. Go `noalloc.go` / `noalloc_test.go` 전부 제거 |
| Raw-ptr handling | privilege + podshape gates | `toolchain/check_gates.osty:runtimeGated*` | E0770, E0771 | partial | privilege + POD 결합 |
| **Intrinsic body support** | `selfhost.IntrinsicBodyDiagsForSource` wrapper (PR #769 에서 `check.go` 의 Go-only 호출 제거; PR #770 에서 non-intrinsic gate 호출들도 제거) | `toolchain/check_gates.osty:63` + `selfhostIntrinsicBodyDiagnosticsFromArena` | E0773 | **ported** | 2026-04-23 Go 레거시 `intrinsic_body.go` (93 LOC) + 전용 test 제거. Osty arena gate 가 single source of truth. native `CheckPackageStructured` 내부 호출 + 비-native 경로는 public `selfhost.IntrinsicBodyDiagsForSource(src, path)` 래퍼 경유. silent diverge 불가능 |
| Package input / workspace parallel | `internal/check/package_input.go` | — | — | **go-only** | 호스트 경계 worker pool, 캐시 fingerprinting |
| HIR lowering orchestration | `host_boundary.go` (JSON 읽기) | `toolchain/hir_lower.osty` 전체 | — | ported | Osty 가 decl walk + annotation 추출, Go 는 결과 소비 |
| File/Package/Workspace 진입점 | `internal/check/check.go` | `toolchain/check.osty` | — | partial | Go 가 native exec + embed 래퍼, Osty 가 phase 1–7 |
| **Type query / LookupType API** | `internal/check/query.go` (check.Result maps) + `internal/selfhost/check_query.go` (api.CheckResult offsets) | `toolchain/check.osty::selfCheck{Type,LetType,Symbol}NameAtOffset` + `selfCheckHoverAtOffset` | — | **partial** | 2026-04-23 Osty canonical 알고리즘 착륙 + Go-side `selfhost.{TypeAtOffset,LetTypeAtOffset,SymbolNameAtOffset,HoverAtOffset}` 브리지. `check.Result` 경유 레거시 쿼리는 그대로 존속 — 후속 작업: lint/LSP/inspect consumer 를 `api.CheckResult` 경유로 이관 → `query.go` 삭제 가능 |
| Diagnostic file path 결합 | `host_boundary.go::stampPackageDiags` | `toolchain/check_diag.osty` | — | partial | Go 가 경로 스탬프, Osty 가 메시지 렌더 |
| **Native checker exec 경계** | `host_boundary.go::nativeCheckerExec` | — | — | **n/a** | 외부 바이너리 인터페이스 — 포팅 대상 아님 |
| Embedded selfhost bridge | `host_boundary.go::embeddedNativeChecker` | `toolchain/check_bridge.osty` (9줄) | — | n/a | **9줄짜리 얇은 stub — AST 변경에 취약** |
| **Inspect / per-node 기록** | `internal/check/inspect.go` (262줄) | — | — | **go-only** | bidirectional 규칙 재실행해 hint 재구성 |
| Generic bound checking | `(hostboundary)` | `toolchain/elab.osty` | E0749 | ported | monomorph 시 bound 강제 |
| Pattern shape mismatch (E0753) | `(hostboundary)` | `toolchain/elab.osty:elabPattern` | E0753 | ported | — |
| Closure annotation requirement | `(hostboundary)` | `toolchain/elab.osty:1849` | E0752 | partial | param seeding 만 |

### Checker 포팅 우선순위

1. ~~**Intrinsic body (E0773)**~~ — **착륙 2026-04-23**. Osty side (`toolchain/check_gates.osty` + `selfhostIntrinsicBodyDiagnosticsFromArena`) 가 이미 존재했고, Go 레거시 `intrinsic_body.go` (93 LOC) + test 제거 + `check.go:208/285` 콜사이트를 public `selfhost.IntrinsicBodyDiagsForSource(src, path)` 래퍼로 대체. Native path (CheckPackageStructured 내부) 와 Go fallback 모두 동일 arena gate 공유 — silent diverge 불가능.
2. **Privilege / POD / Noalloc 3-gate 통합** — 양쪽 `partial` 인데 **독립 실행 중** — silent divergence 리스크 제일 큼. 방법: 해당 gate 를 `check_gates.osty` 단일 소스로 몰고 Go 쪽은 adapter 만 남긴다. 3개가 구조적으로 유사하니 한 번에 처리.
3. **Builder auto-derive 단일화** — Go AST 레벨 desugar 를 `hir_lower.osty` 쪽으로 흡수. `toBuilder()` receiver type recovery 를 옮기려면 scope 정보 필요 → resolver `#[cfg]` 포팅 이후 진행.
4. **Inspect / Query API 포팅** — `inspect.go` 262줄 + `query.go` 127줄 을 `toolchain/inspect.osty` 로. IDE/LSP feature 기반, golden snapshot 영향 적어 차단 없이 병행 가능.
5. **Annotation semantic validation 완성** — `#[vectorize]`/`#[parallel]`/`#[hot]`/`#[pure]` 를 `hir_lower.osty` 에 추가. v0.6 A5–A13 stanza 기반 스펙이 권위.
6. **Defer lifecycle 설계** — 스펙 정리 먼저. 백엔드와 cross-cutting 이라 `SPEC_GAPS.md` entry 선행.
7. **Package input / worker pool** — 호스트 경계 바깥이지만 Osty 로 가져오려면 파일 IO + 프로세스 스케줄링 이 Osty 측에 필요. 가장 뒤.

---

## 3. Hidden hazards

이식 작업 시 터질 가능성이 높은 지점:

- **`check_bridge.osty` 9 줄** — AST 모양 변경 1회에 전체 bridge 가 깨진다. resolver merge/cycle 포팅 시 AST 확장이 같이 가므로 bridge 확장을 **같은 PR 에** 묶을 것. golden snapshot 최소 1 개는 bridge 경로 커버 필요.
- ~~**Privilege/POD/Noalloc/Intrinsic-body 가 양쪽 독립 실행 중**~~ — **해제 2026-04-23**: (a) `gates_diff_test.go::TestGatesCrossSideParity` 로 parity harness 장착 (9 fixture). (b) `check.go` 의 4 Go-side gate 호출부 제거로 Osty 가 단일 source. Go 측 함수 (`runPrivilegeGate`/`runPodShapeChecks`/`runNoAllocChecks`/`runIntrinsicBodyChecks`) 는 reference impl + 단위 테스트용으로 유지 — 실 production 경로에서는 호출 안 됨. parity harness 가 향후 regress 감지.
- **Builder desugar 중복** — Go 가 AST 재작성, Osty 가 HIR qualify. 둘 다 돌고 있어 지금도 이론적으로 double-apply 가능. 포팅 = Go 제거 = 현재 동작 보존 어려움 (scope 정보 복구 로직 재구현).
- **`inspect.go` 가 elab 규칙 복제** — bidirectional 추론 규칙이 `elab.osty` 와 `inspect.go` 두 곳에 있다. `elab.osty` 가 바뀌면 `inspect.go` 업데이트 안 하면 IDE hint 가 틀린다. 포팅 시 elab 에 introspection hook 을 박는 쪽이 정답.
- **Workspace-level 로직 (cycle / visibility / partial merge)** — resolver 매트릭스의 3 대 `go-only` 는 모두 단일 파일 범위를 넘는다. Osty `resolve.osty` 는 single-file 시점이라 **workspace state 모델 설계** 가 선행. Go `workspace.go` 600줄 + `merge.go` 를 그대로 옮기면 `resolve.osty` 구조 깨짐 — 모델 먼저 합의.
- **Native checker exec 바이너리** — `osty-native-checker` 는 `toolchain/check.osty` 가 컴파일된 산출물. Osty 측 checker 변경 → 바이너리 리빌드 → `just build-all` 없이는 테스트가 stale 버전으로 돈다. 포팅 PR 마다 `just build-all` 명시.

---

## 다음 단계

이 문서를 기준으로:

- **Phase 1 (resolver 잔여)**: `#[cfg]` → partial merge → cycle detection → visibility → unwired refs. 각 항목 PR 단위.
- **Phase 2 (checker 활성화)**: intrinsic body → 3-gate 통합 → builder desugar → inspect → annotation 완성 → defer → worker pool.

각 항목 착수 시 `SPEC_GAPS.md` / `SELFHOST_PORT_MATRIX.md` 업데이트 및 `just build-all` 후 진행.
