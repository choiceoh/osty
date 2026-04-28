# SELFHOST_PORT_MATRIX.md

Go → Osty 셀프호스팅 포팅 현황 매트릭스. 리졸버 / 체커 잔여 작업을 항목 단위로 분류.

> **Scope**: `internal/resolve` ↔ `toolchain/resolve.osty`, `internal/check` ↔
> `toolchain/check*.osty` + `toolchain/elab.osty` + `toolchain/hir_lower.osty`.
> 기준일: 2026-04-26 (post-rebase audit, HEAD = #921 + Phase 0 fixes).
> 이전 2026-04-24 스냅샷 이후 80 커밋이 들어왔고, 그 위에 Phase 0 (MirSwitchCase
> rename + typeRepr migration + scopeFor O(N²) → sorted-binsearch+memoize) 가
> 추가됐다. 코드 실측 기준: `--legacy` check/typecheck escape hatch 는
> 제거됐고, `--native` 는 backwards-compat no-op 이다. `just front`,
> `just verify-selfhost`, CLI astbridge-free 회귀 테스트는 통과. **`osty check
> toolchain` smoke 는 EXIT=0 으로 복구됐다** (Phase 0 a/b 커밋 후). MIR-merged
> probe 4 종 중 `TestProbeWholeToolchainMerged` / `TestProbeNativeToolchainMerged`
> / `TestProbeNativeToolchainMergedMIR` (info-only) 는 통과; 잔여 walls 는
> 아래 "2026-04-26 — 현재 first-walls (post-Phase-0)" 섹션 참조.

## 2026-04-26 — 현재 first-walls (post-Phase-0 실측)

**Phase 0 (a/b/c) 착륙 후 상태.** `.bin/osty check toolchain` smoke 는 EXIT=0
으로 복구됐고, MIR-merged probe 의 dup-struct / typeRepr 벽이 모두 해소됐다.
Pipeline-Clean timeout 도 풀렸다 (5분+ → 246s 정상 종료, 다음 wall 로 진입).

| 테스트 | 시간 | 상태 / 첫 벽 |
|---|---|---|
| `osty check toolchain` smoke | < 5s | ✅ EXIT=0 |
| `TestProbeWholeToolchainMerged` (AST-only) | 2.4s | ✅ 통과 |
| `TestProbeNativeToolchainMerged` (AST, ci.osty 제외) | 2.3s | ✅ 통과 |
| `TestProbeNativeToolchainMergedMIR` (full MIR, info-only) | ~280s | ✅ 통과 (info log only) |
| `TestNativeToolchainMergedMIRPipelineIsClean` | ~210s | ⚠️ FAIL — `LLVM016 name: unknown identifier "None"` (legacy fallback wall, LLVM011 default-value walls 모두 해소 후 다음 wall 진입) |
| `TestNativeToolchainMergedMIRErrTypeFloor` | 242s | ⚠️ FAIL — 374 ErrType locals (이전 474 → -100; floor=40 까지는 334 잔여) |

### 해소된 Phase 0 항목 (2026-04-26 착륙)

- ✅ `MirSwitchCase` 중복 선언 — main 측 PR 에서 [toolchain/mir_generator.osty](toolchain/mir_generator.osty)
  의 line-builder 측을 `MirGenSwitchCase` 로 rename 했다. [toolchain/mir.osty:888](toolchain/mir.osty:888)
  의 MIR 추상 타입 (`value/target/label`) 은 그대로 유지.
- ✅ `typeName` → `typeRepr` 마이그레이션 (PR #892 follow-up) — PR #927 가
  [toolchain/inspect.osty](toolchain/inspect.osty) 8 사이트 + [toolchain/lint.osty:2884](toolchain/lint.osty:2884)
  1 사이트 모두 `frontTypeReprToString(X.typeRepr)` 로 갈았다.
  Osty-내부 `InspectRecord.typeName` / `InspectBindingEntry.typeName` 필드는
  String 그대로 유지 (consumer 가 보는 stable shape).
- ✅ `selfhostSpanIndex.scopeFor` O(N²) — [internal/check/host_boundary.go:924](internal/check/host_boundary.go:924)
  를 sorted slice + binary-search + memoize 로 교체. `scopeSpans []scopeSpanEntry`
  (sorted by `span.start` asc, lazily built on first cache miss) +
  `scopeQueries map[selfhostSpanKey]*resolve.Scope` (resolved cache, ok-semantic
  으로 nil 도 memoize). Pipeline-Clean 더 이상 timeout 안 남.

### 잔여 walls

- ~~**LLVM011 CheckFnSig.hasReceiver default value**~~ — **Phase 4a 착륙
  (2026-04-26)**. `pub hasReceiver: Bool = false` default 를 제거하고 251 개
  CheckFnSig literal 사이트 (`toolchain/check.osty` 57 + `toolchain/check_env.osty`
  189 + `toolchain/elab.osty` 5) 모두 명시적 `hasReceiver: false/true`
  지정 — 값은 `receiverTy: -1` 이면 `false`, 아니면 `true`.
  `checkSpecializeMethodSelf` 는 입력 sig 의 hasReceiver 를 그대로 전달
  (`hasReceiver: sig.hasReceiver`). `CheckFieldSig.exported` 도 같은 패턴
  으로 default 제거 (`emptyCheckFieldSig` 1 사이트만 영향). 다음 legacy
  fallback wall 은 **LLVM016 unknown identifier "None"** (Option/Result
  builtin variant name resolution — 별 카테고리, 별 PR).
- **374 ErrType locals (floor=40)** — Phase 0 가 PR #921 의 12개 type-error
  중 11개를 제거하고 (474 → 426), Phase 3a 가 추가로 -52 (426 → 374,
  `lowerMatch` scrutinee recovery). 잔여 ~334 leaks 는 **checker 가 통과한
  소스인데 ir.Lower / ir.Monomorphize 에서 타입을 못 잡는 사이트** 다 (`osty
  check toolchain` 자체는 EXIT=0).

  Phase 3a 분포 (probe 결과 상위 10개, 167 functions affected):

  | count | function |
  |---:|---|
  | 29 | `hirOptimizeVisitExpr` |
  | 22 | `monoScanExpr` |
  | 12 | `llvmNativeEmitStmt` |
  | 10 | `hirOptimizeVisitModule` |
  | 9  | `hirReachExpr` |
  | 7  | `llvmNativeEmitFieldAssign` |
  | 7  | `hirReachExprAsMethods` |
  | 6  | `mirDeadAssignElimBlock` |
  | 6  | `hirWalkVisitExpr` |
  | 6  | `monoScanRoots` |

  공통 패턴: 큰 `match expr.kind` 디스패치에서 `let folded = call(...)` 또는
  `let kind = e.field` 이 ErrType 으로 떨어진다. Phase 3a (`lowerMatch`
  scrutinee recovery) 가 cascade 의 seed (`_scrut` local) 를 차단했지만,
  arm 안의 사용자 정의 enum variant ident 와 chained field/index access 의
  recovery 는 아직 미흡. 단일 파일 머지가 만드는 generic instantiation
  context 에서 monomorph 이 inference 를 못 하는 케이스로 추정. 실제
  multi-file 컴파일에서는 발생 안 함.

  Phase 3b 후보 (추가 진입 비용 낮음):
  - `identTypeFromDecl` (`internal/ir/lower.go`) 에 `*ast.Variant` case 추가
    — 현재 nil 반환 → 부모 enum 이름으로 `&ast.NamedType` 합성. 변수
    `let mut kind = HirSwitchUnknown` 같은 bare variant ident 가 ErrType
    으로 안 떨어지게 된다. (resolver 가 Variant Symbol 에 부모 enum 백포인터
    를 안 보존하므로 lookup 헬퍼 신설 필요.)
  - `bindingTypeFromAST` (`internal/ir/lower.go`) 가 `StructLit/ParenExpr`
    만 처리 — `CallExpr` (recoverFnDeclReturnType 재사용), `IfExpr` (양쪽
    branch 결과 type), `FieldExpr` (recoverFieldType 재사용), `IndexExpr`
    (List<T>[i]→T) 추가.

이 두 wall 은 현재 Phase 1 (E0553) 또는 1c.5 (Go resolver 삭제) 와는 독립이라
별도 트랙으로 추적.

## 2026-04-24 — Phase 1c.5 code state (historical snapshot)

> 이 섹션은 2026-04-24 시점의 스냅샷이다. 그 이후 LOC 변화와 새 walls 는
> 위 "2026-04-26" 섹션이 권위.

`cmd/osty` 의 `check` / `typecheck` 기본 경로가 **self-host arena 파이프라인**
(`ParseRun → CheckStructuredFromRun → CheckDiagnosticsAsDiag`) 로 전환되었다.
Go-native `runCheckFileLegacy` / `runCheckPackageLegacy` /
`runTypecheckFileLegacy` 경로와 `cliFlags.legacy` 는 삭제됐다. 역사적인
`--native` 플래그는 현재도 backwards compat 로 수락되지만 선택지는 바꾸지 않는다
(no-op).

영향 범위:
- `osty check FILE` / `osty check DIR` → self-host default
- `osty typecheck FILE` / `osty typecheck DIR` → self-host default
- `osty resolve FILE` / `osty resolve DIR` → self-host default
- `osty lint FILE` 은 `check.SelfhostFile` 로 타입 정보를 만들지만, lint
  engine 자체는 아직 `*ast.File` / Go resolver identity 를 소비한다.
- `osty lint DIR` / workspace lint 도 arena-first loader 뒤에 Go resolver +
  linter 를 태운다. lint identity 모델 포팅 전까지 astbridge 제거 대상이다.

회귀 테스트:
- `TestCheckCLIDefaultPathExitsZero` — `osty check FILE` 기본 경로 exit 0
- `TestRunCheckFileDefaultPathIsAstbridgeFree` — 기본 check 경로 astbridge 0
- `TestRunResolve{File,Package}HappyPathIsAstbridgeFree` +
  `TestRun{Check,Typecheck}{File,Package,Workspace}NativeIsAstbridgeFree`
  — resolve/check/typecheck native/default 경로 astbridge 0
- ~~`TestGoGenerateSelfhostLeavesGeneratedArtifactsClean`~~ — **삭제됨
  (2026-04-23, PR #854)**. Osty→Go bootstrap transpiler 가 retire 되면서
  `go generate ./internal/selfhost` regen 파이프라인 자체가 사라졌고,
  CI "verify selfhost regen is clean" step 도 같이 제거됐다.
  `internal/selfhost/generated.go` 는 frozen seed 가 됐다.

## 1c 로드맵 — Go resolver 제거까지

매트릭스 `ported` 가 "Osty 가 구동" 이라면, 1c 목표 상태는 "Go 소스 자체가
제거됨". 진행은 5 단계로 쪼개 세션 단위로 착륙한다.

- **1c.1** ✅ `cmd/osty` `check`/`typecheck` default flip (2026-04-23 착륙,
  #755). 당시 존재하던 `--legacy` escape hatch 는 1c.5 에서 제거됨.
- **1c.2** ✅ **arena-first loader 수용 (2026-04-23 착륙)**.
  `resolve.LoadPackageArenaFirst` / `(*Workspace).LoadPackageArenaFirst` +
  `(*Package).MaterializeCanonicalSources` 도입. `internal/pipeline` ·
  `cmd/osty/{build,run,doc,main}` · `internal/cihost` · `internal/lsp` ·
  `cmd/osty-native-llvmgen` 가 전부 arena-first 경유로 이식. 파싱은
  `selfhost.Run` 단일 진입점이다. `LoadPackageArenaFirst` 는 아직
  downstream shape 유지를 위해 `EnsureFiles` + `MaterializeCanonicalSources`
  를 호출하며, bump-free native paths 는 `LoadPackageForNative` / package
  structured adapters 를 직접 사용한다.
- **1c.3** ✅ import-surface 이식, annotation validators 이식, refNames /
  refTargets adapter 직결 재평가 완료. partial struct/enum merge 의 true
  cross-file stitching 은 resolver deletion chain 이후 workspace-level pass 로
  별도 추적한다.
- **1c.4** ✅ `check.File(file, res, opts)` 시그니처 제거. production call
  site 는 `check.SelfhostFile` / `check.Package` / `check.Workspace` 로 통일됐고,
  이 entry 들은 embedded/external native checker boundary 를 통과한다. check
  내부 helper 들 (`privilege`, `podshape`, `noalloc`) 은 `check_gates.osty`
  로 이식 후 삭제됐다. builder auto-derive prepass 도 selfhost checker +
  IR on-demand lowering 착륙 후 삭제됐다.

  **2026-04-24 스코프 검증** (grep 기반 실측):
  - `func File` 은 `internal/check` 에 더 이상 없다.
  - production 호출부는 `check.SelfhostFile` / `check.Package` /
    `check.Workspace` 로 통일됐다. `internal/pipeline/pipeline.go:483` 도
    `check.SelfhostFile` 을 호출한다.
  - `run{Lint,Check,Typecheck}FileLegacy` 계열 runner 와 legacy baseline
    tests 는 삭제됐다.

  잔여 Go check/ 파일 실측 LOC (2026-04-24 `wc -l`):
  - `host_boundary.go` 1990 (브릿지: `nativeCheckerExec` +
    `embeddedNativeChecker` + `cachedNativeChecker` + `apply*Result` 군)
  - `inspect.go` 647 (과거 매트릭스 262 표기는 stale — 현 규모 2.5배)
  - `check.go` 417 (SelfhostFile/Package/Workspace entry 3종)
  - `package_input.go` 199 (workspace 병렬 캐시)
  - `privilege.go` 47 (host 식별 predicate 2 개 — `isPrivilegedPackage` /
    `isPrivilegedPackagePath`. #770 이후 "최소 잔류" 로 주석 명시)
  - 합계 3300 (테스트 제외)

  **2026-04-26 재측정** (`wc -l`, post-rebase):
  - `host_boundary.go` **1878** (-112)
  - `inspect.go` 647 (변동 없음)
  - `check.go` **416** (-1)
  - `package_input.go` 199 (변동 없음)
  - `privilege.go` 47 (변동 없음)
  - 합계 **3187** (-113). 추가로 `inspect_format.go` 116 LOC,
    `builder_policy.go` 70 LOC, `package_input_export.go` 14 LOC 가
    non-test 파일로 들어와 있다 (이 셋은 매트릭스 본문에서 별도 추적
    안 됨).

  **이미 삭제된 파일 (확인됨)**: `podshape.go`, `noalloc.go`, `intrinsic_body.go`,
  `query.go`, `builder_desugar.go` — 이전 매트릭스/CHANGELOG 대로 모두 부재.
- **1c.5** ⏳ `internal/resolve/resolve.go` (body-walk resolver),
  `internal/resolve/cfg.go` (legacy cfg pre-filter),
  `internal/resolve/prelude.go` (Go-side prelude builder),
  `internal/resolve/scope.go` (depth-based Go scope), `LoadPackage` /
  `ResolveAll` (Go-host) 전부 삭제. `--legacy` 플래그 제거는 완료됐지만,
  lint / formatter / LSP 등의 `*ast.File` identity 소비자가
  남아 있어 매트릭스의 `ported` 행을 아직 전부 "완료" 로 볼 수는 없다.

  **2026-04-24 진행** (incremental deletion chain):
  - #829: `resolve.File` helper (dead 6 LOC) 삭제.
  - #830: `resolve.LoadPackage` 13 call site → `LoadPackageArenaFirst` 이전
    + top-level `LoadPackage` wrapper 삭제.
  - **완료**: `--legacy` 플래그 제거 + `cliFlags.legacy` 필드 삭제 +
    `runCheckFileLegacy` / `runTypecheckFileLegacy` / `runCheckPackageLegacy` /
    `runCheckWorkspace` 4 legacy runner 삭제 + `runLintFileLegacy` →
    `runLintFile` / `runLintPackageLegacy` → `runLintPackage` 리네임 +
    `cmd/osty/check_legacy_baseline_test.go` / `lint_legacy_baseline_test.go`
    test file 전체 삭제 + 관련 legacy 가드 test 2 종 (`TestCheckCLILegacyOptOutStillWorks` /
    `TestTypecheckCLILegacyPackageRejected`) 삭제. `--native` 는 backwards-compat
    no-op 으로 수락. `check.File` 잔여 호출자는 0.

각 phase 의 끝에 `just full` + 매뉴얼 `osty check toolchain` smoke 로
regression 없음을 확인하고 이 문서의 ⏳ → ✅ 전환. **2026-04-26 시점에는
이 smoke 자체가 11 errors 로 실패** — 위 "현재 first-walls" 섹션 참조.
1c.5 의 Go resolver 파일 삭제 (`resolve.go` / `cfg.go` / `prelude.go` /
`scope.go`) 도 아직 미완 — 모두 트리에 잔존 (실측 2026-04-26).

## 2026-04-24 이후 주요 착륙 (post-snapshot, 80 commits)

매트릭스 본문 행이 아직 반영하지 못한 변경:

- **PR #854 (2026-04-23)** — Osty→Go bootstrap transpiler 폐기 (17,182 LOC
  제거). `internal/bootstrap/{gen,seedgen}` + `cmd/osty-bootstrap-gen` +
  bundle facade + regen smoke test + CI step 모두 삭제.
  `internal/selfhost/generated.go` 는 frozen seed.
- **PR #849** — E0743 (§8.1 G13) phase 2a+2b decl/call site escape 검증
  착륙. Checker 매트릭스 "Closure capture / escape (E0743)" 행 이미 반영.
- **PR #850** — v0.6 A8 `#[inline]` annotation native LLVM emit 경로 wire.
- **PR #851** — cross-package member lookup (E0507/E0508) wording self-host.
- **PR #863 + #c1f34e4b 외 List intrinsics 시리즈** — `toolchain/llvmgen.osty`
  로 List intrinsic 18종 (~500 LOC) 포팅.
- **PR #861/#864/#887/#888/#890/#897–#915 시리즈** — `toolchain/llvmgen.osty`
  + `toolchain/mir_generator.osty` 에 §3 (runtime decls / vtable refs),
  §4 (function-header / thunk cache), §6/§7/§8/§9 (line builders / inline
  chain drains), §12 (string-literal interning), §14 (layout cache),
  §15 (stateful Phase B fnBuf mirror) 모두 native-owned 으로 포팅. 30+
  commit 의 누적 결과로 `llvmgen.osty` 가 6,211 LOC 까지 확장.
- **PR #894** — LSP 패키지/워크스페이스 분석을 Salsa incremental engine 으로
  라우팅 (`analyzePackageViaEngine` / `analyzeWorkspaceViaEngine`).
- **PR #877** — workspace 의 hierarchical `[lint]` config merge.
- **PR #886** — `osty fmt` 가 conservative repair 대신 airepair 경유.
- **PR #921** — 8 backend / cmd-test failures 수정 (poisoned-type recovery
  + Variant/IndirectCall hints in `internal/ir/lower.go` + `internal/mir/lower.go`).
  현재 ErrType floor (40) 가 474 까지 튄 이유는 이 recovery 가
  upstream `osty check toolchain` 의 11 type-error 케이스에서는 작동하지
  않기 때문 — recovery 가 의존하는 syntactic AST fallback 이 typeName/typeRepr
  drift 를 만나면 깔끔히 type 을 재구성 못 함.
- **GC / runtime perf** (#867–#917 시리즈, 약 20 commit) — `osty_gc_header`
  112→96 byte (-14%), single-mutator skip path, mmap arena, small-string
  pointer-tagging, small-list inline storage, Map/String/list_reserve
  inlining via `OSTY_HOT_INLINE`. 매트릭스 본문은 runtime 영역을 추적하지
  않으나 backend 안정성에 직접 영향.
- **MIR 합병 fusions** (#873/#914/#916) — `String + ` chain → `ConcatN`,
  `expr.split(sep)[K]` → `NthSegment`, `m.insert(k, m.getOr(k, 0)+δ)` →
  `MapIncr`. `mir.osty` / `mir_lower.osty` 단일 호출 fusion 패턴 추가.
- **Doc**: PR #840 (`Update self-hosting status docs`) 가 2026-04-24
  스냅샷의 마지막 docs 갱신. 그 이후 본 매트릭스/README 의 status 본문은
  drift 상태 — 본 문서가 그 drift 를 잡는 첫 번째 갱신.

## Bridge 모델

핵심 경로는 selfhost 가 authoritative 이고, Go 코드는 외부 실행 경계 /
embedded adapter / 아직 포팅되지 않은 consumer shape 를 담당한다:

- **Resolver**
  - `internal/resolve/workspace.go::LoadPackage` → arena-first alias over
    `LoadPackageNative` (Go parser/resolver-specific load path retired)
  - `internal/resolve/workspace_native.go::LoadPackageNative` → `selfhost.LowerPublicFileFromRun`
  - `internal/resolve/native_adapter.go` → `selfhost.ResolvePackageStructured` (Osty `resolve.osty` 구동)
  - `internal/resolve/workspace.go::ResolveAll` → per-package native resolve bridge
    + cycle-diag stitching (Go 2-pass resolve walk retired)
- **Checker** (`toolchain/README.md`에 문서화됨)
  - `internal/check/host_boundary.go::nativeCheckerExec` → 외부 `osty-native-checker`
  - `embeddedNativeChecker` → `selfhost.CheckPackageStructured` (Osty `check.osty` + `elab.osty` 구동)
  - `check.SelfhostFile` / `check.Package` / `check.Workspace` → downstream 이
    소비하는 Go `check.Result` shape 로 native checker output 을 적재
  - Go-only 잔여는 `inspect.go`, workspace package input/cache, file stamping,
    host-boundary glue 중심이다. `podshape.go`, `noalloc.go`,
    `intrinsic_body.go`, `query.go`, `builder_desugar.go` 는 삭제 완료.

따라서 "포팅 완료" 의 실질은 **Go 레거시 제거** 이고, 매트릭스의 `state` 는 그
기준으로 읽을 것.

## Legend

- `ported` — Osty 가 해당 로직을 구동하며 Go 쪽은 facade/adapter
- `partial` — 양쪽에 overlapping/diverged 구현 존재
- `broken` — 스펙이 요구하는데 양쪽 모두 구현 누락 / 오동작
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
| **Workspace package dispatch** | `internal/resolve/workspace.go::ResolveAll` | `toolchain/resolve.osty` (per-package via `ResolvePackageStructured`) | — | **ported** | 2026-04-28 workspace가 Go declare/body pass 대신 package별 native resolve bridge + cycle stitching 으로 전환 |
| **#[cfg] 사전 필터** | `internal/resolve/native_adapter.go:nativeCfgEnvFor` | `toolchain/resolve.osty::srCfgDeclPasses` | E0405, E0739 | **ported** | 2026-04-28 workspace/package 모두 selfhost cfg 필터 경로만 사용. `cfg.go` 는 AST-only compatibility fallback 전용 잔여 |
| **Cross-package member lookup (E0507/E0508)** | `internal/resolve/resolve.go:420::lookupPackageMember` | `toolchain/resolve.osty::selfLookupPackageMember` + `SelfMemberLookupResult` | E0507, E0508 | **ported** | 2026-04-24 Osty 가 wording (message/primary/note/hint) 단일 소스. Go `lookupPackageMember` 는 Scope lookup 만 수행 후 `selfhost.LookupPackageMember(pkgName, member, typePos, found, public)` 로 분기. status 0/1/2 → OK/Private(E0507)/Missing(E0508). `pub use` 체인 traversal (E0553) 은 별도 |
| **`pub use` re-export visibility (E0553)** | — | — | E0553 | **go-only (silent)** | `resolve.go:368-390` pub 재노출만 기록; 소스가 private 인데 re-export 하는 케이스 진단 없음. Phase 2.2+ follow-up — workspace pub-symbol graph 필요 |
| **Runtime privilege gate (E0770)** | `internal/check/privilege.go` (47 LOC 잔존, `isPrivilegedPackage[Path]` helper) | `toolchain/check_gates.osty::runCheckGates` | E0770 | **ported** | 2026-04-23 consolidation. 상세는 Checker 매트릭스 "Privilege system" row 참조 |
| Partial-decl method name 유일성 | `internal/resolve/merge.go:100::checkPartialMethodNames` (Go fallback helper; no CLI `--legacy` entry remains) | `toolchain/resolve.osty::srHandlePartialType` (line 2247+ cross-file methodNames dup 검출 → E0501 + R19 note 발행) | E0501 (R19) | **ported** | Osty `SelfPartialDecl.methodNames` 트래커가 partial struct/enum 여러 파일에 걸친 method 중복을 잡고 동일 메시지 / 동일 note 발행. Go helper 는 `declareTopLevelPackage` 계열 잔여 resolver 경로가 살아있는 동안만 비교 기준으로 남아 있으며, 1c.5 resolver deletion chain 에서 제거 대상 |
| Self-host ref tracking (refNames/Targets) | `internal/selfhost/resolve_adapter.go::adaptResolveResult` (refNodes/refNames/refTargets/refTargetStarts/refTargetEnds 전부 `ResolvedRef` 로 lift) | `toolchain/resolve.osty:1483+ out.refNames.push` / `out.refTargets.push` | — | **ported** | PR #754 리베이스 시점 이후 adapter 가 Osty emit 된 병렬 리스트 5종을 `ResolvedRef` 로 공식 변환. 추가 필드 이관 없음 — translation 은 단순 indexed zip 이므로 "unwired" 라벨은 과거 표현 |
| Generic arity 기록 | — | `toolchain/resolve.osty:510` | — | ported | 양쪽 동일 |

### Resolver 포팅 우선순위

1. ~~**`#[cfg]` 사전 필터** (E0405)~~ — **착륙 2026-04-22**, **workspace 전환 2026-04-28**. `toolchain/resolve.osty` 의 `srCfgDeclPasses` + `selfResolveAstFileWithCfg` + `srCheckCfgArgs` 로 이식, `PackageResolveInput.Cfg` / `ResolveSourceStructuredWithCfg` 로 bridge. native 경로는 `workspace.cfgEnv` / `DefaultCfgEnv()` 자동 상속. `internal/resolve/cfg.go` 는 AST-only compatibility fallback 에만 남음.
2. ~~**Partial struct/enum merge** (E0509/E0501)~~ — **착륙 2026-04-23**. `SelfPartialDecl` 트래커 + `srHandlePartialType`, R19 4 invariants (pub / generics / fields-in-one / method name uniqueness) 검증. 현재는 single-file (native_adapter 가 `ResolvePackageStructured` 로 다중 파일을 synthetic 단일 네임스페이스로 합쳐 통과). True cross-file stitching 은 workspace 모델 등장 이후.
3. ~~**Workspace cycle detection** (E0506)~~ — **착륙 2026-04-23**. `selfDetectImportCycles` + `SelfWorkspaceUses` / `SelfPackageUses` / `SelfUseEdge` / `SelfCycleDiag` (toolchain/resolve.osty). `selfhost.DetectImportCycles` Go bridge. `internal/resolve/workspace.go::detectCycles` 는 그래프 준비 + offset 기반 roundtrip 후 `token.Pos` 복원 + 진단 렌더링 glue 만 남김. 알고리즘 (DFS 3-상태 색칠) 은 완전히 Osty 쪽.
4. ~~**Cross-package member lookup (E0507/E0508)**~~ — **착륙 2026-04-24**. `selfLookupPackageMember` + `SelfMemberLookupResult` 가 wording 단일 소스. Go `lookupPackageMember` 는 Scope lookup 후 bridge 로 분기. `pub use` 재노출 체인 (E0553) 은 workspace pub-symbol graph 가 별도로 필요해 다음 우선순위로 분리.
5. **`pub use` re-export visibility (E0553)** — `resolve.go:368-390` 은 재노출 사실만 기록하고 소스 가시성 검증 없음. workspace-level pub-symbol graph + re-export chain traversal 이 선행. Osty 측 스키마는 기존 `SelfUseEdge` / `SelfPackageUses` 위에 얹을 수 있음 — cycle detection 과 같은 layer.
6. ~~**`osty-only-unwired` refNames/refTargets**~~ — **재평가 결과 기 이식**. `internal/selfhost/resolve_adapter.go::adaptResolveResult` 가 이미 Osty emit 된 5 개 병렬 리스트 (refNodes / refNames / refTargets / refTargetStarts / refTargetEnds) 를 `ResolvedRef` 로 indexed-zip 해 공식 변환 중. "unwired" 라벨은 과거 표현이었고 현재 adapter 가 유일 소비자 — bridge 면적 추가 축소 여지 없음.

---

## 2. Checker 매트릭스

| Feature | Go locations | Osty locations | Diag codes | State | Notes |
|---|---|---|---|---|---|
| Bidirectional type inference | `(hostboundary)` | `toolchain/elab.osty:§2a.3` | E0700–E0757 | ported | Osty 구동, Go 는 JSON bridge |
| Generic monomorphization | `(hostboundary)` | `toolchain/elab.osty:53` | — | ported | call site 별 fresh Solver |
| 메서드 dispatch (struct/enum/iface) | `(hostboundary)` | `toolchain/elab.osty:1550` | E0703 | ported | `checkSpecializeMethodSelf` |
| Interface default-body | `(hostboundary)` | `toolchain/check_env.osty` | — | **partial** | 상속은 기록, default body 코드패스 빈약 (`269eebf8` 가 일반화 진행중) |
| `?` 전파 (Option/Result) | `(hostboundary)` | `toolchain/elab.osty:161,1360` | E0717 | ported | `TkOptional` intern |
| `?.` optional chain | `(hostboundary)` | `toolchain/elab.osty::elabInferField` + `elabInferMethodCall` + `elabWrapOptionalChain` + `toolchain/check_diag.osty::diagOptionalChainOnNon` | E0719 | **ported** | 2026-04-23 field-access + method-call 양쪽 LANG_SPEC §4 / 부록 A.6 준수로 복구. Field: `elabInferField` 가 `AstNField.flags == 1` 분기 — Option receiver `tyInnerAt` unwrap → lookup → 결과를 `elabWrapOptionalChain` 로 `Option<_>` 재래핑 (이미 Option 이면 flatten). Non-Option → E0719. Method: `elabInferMethodCall` 이 `fieldNode.flags == 1` 으로 동일 처리 — mono / generic instantiation 양쪽 return path 에서 retTy 를 wrap. `generated.go` lockstep sync. 회귀 가드: [internal/check/optional_chain_test.go](internal/check/optional_chain_test.go) 8 테스트 (field + method call 쌍 × {Option receiver 정상, `??` coalesce unwrap, non-Option E0719, 메시지 shape, nested flatten}) |
| `??` nil-coalesce | `(hostboundary)` | `toolchain/elab.osty:432` | — | ported | Option unwrap 특수 |
| **Defer lifecycle** | `internal/parser/parser.go`·`internal/resolve/file.go`·`internal/lint/typebased.go` | `toolchain/parser.osty:2050`·`toolchain/resolve.osty:947`·`toolchain/lint.osty:3043` | E0603 / E0608 / L0007 | static-ported | 정적 검증 (top-level defer 거부, 비-호출 defer 거부, Result/Option defer → L0007) 은 Go·Osty 양쪽에 있음. 런타임 lifecycle (LIFO / `?` propagation / cancellation / abort-skip §4.12 rules 3/7/8) 은 백엔드 영역 |
| 패턴 exhaustiveness | `(hostboundary)` | `toolchain/elab.osty:2669` | E0712 | ported | witness 생성 phase 1, opaque range phase 2 |
| 패턴 reachability | `(hostboundary)` | `toolchain/elab.osty:3171` | E0740, E0741 | ported | guarded pattern 추적 |
| 숫자 리터럴 다형성 | `(hostboundary)` | `toolchain/elab.osty:273` | — | partial | 기대 타입 기반 narrowing, 범위 좁음 |
| Closure capture / escape (E0743) | `(hostboundary)` | `toolchain/elab.osty:1849` · `toolchain/check.osty` (collectFnDecl/Struct/Enum/TypeAlias/LetDecl) · `toolchain/ty.osty::tyEscapingCapabilityHead` | E0743 | **ported** | 2026-04-24 phase 2a+2b 착륙. decl-site 5 곳 (fn ret / struct field / enum payload / type alias / top-level let) + call-site 2 곳 (`elabInferCall`, `elabInferMethodCall`) 가 monomorph 후 retTy 를 구조적으로 검사. `spawn` 이름 exemption 으로 prelude/`Group.spawn`/`thread.spawn` 을 한 번에 허용. negative 코퍼스 5 케이스로 잠금 |
| `Self` specialization | `(hostboundary)` | `toolchain/elab.osty:1700` | — | ported | `6cd17fa2` 최근 착륙 |
| Annotation semantic validation | `(hostboundary)` | `toolchain/resolve.osty` (v0.6 arg validators) + `toolchain/hir_lower.osty:923-950` (extract) + `toolchain/mir.osty::MirFunction` + `toolchain/mir_lower.osty::mirLowerCopyFnAnnotations` (HIR→MIR 전달) | E0739 | **partial (LLVM-emission 남음)** | 2026-04-23 resolve 단계 v0.6 arg validator 10 종 전부 이식. HIR extract 계층 + HirFnDecl 필드 10 종 landing 완료. **2026-04-23 후속**: MirFunction 이 9 annotation 그룹 필드 (`vectorize[Width/Scalable/Predicate]` / `noVectorize` / `parallel` / `unroll[Count]` / `inlineMode` (MirInlineMode enum) / `hot` / `cold` / `targetFeatures` / `noaliasAll` / `noaliasParams` / `pure`) 을 HirFnDecl 로부터 `mirLowerCopyFnAnnotations` 가 lossless 복사 — Go `ir.FnDecl` shim 이 소비할 수 있는 단일 MIR-level 소스 완성. **잔여**: `toolchain/llvmgen.osty` 가 이 MIR 필드를 LLVM fn-attr / loop-metadata 로 emit 하는 경로 (A5 loop md / A8 fn attr / A9 section 배치 / A10 target-features / A11 noalias param attr / A13 readnone). 현재는 Go `internal/llvmgen` 쪽이 emit 을 전담 — Osty native LLVMgen 은 여전히 scalar-only |
| **Builder auto-derive** | `internal/check/builder_policy.go` + `internal/ir/lower.go` | `toolchain/builder_policy.osty` + `toolchain/elab.osty` + `toolchain/hir_lower.osty` | E0774 | **ported** | 2026-04-24: derive-policy shared source (`builder_policy.osty`) 분리 후 후속으로 selfhost checker (`elab.osty`) 가 `.builder()/toBuilder().build()` typing + E0774 를 직접 처리하도록 착륙. `check.File`/`Package`/`Workspace` 의 Go AST prepass 제거, legacy `builder_desugar.go` 삭제. Go 쪽은 `check.ClassifyBuilderDerive` adapter 를 IR metadata/lowering 에만 재사용 |
| **ToString auto-derive** | — | — | — | — | 양쪽 미구현. 스펙 요구 여부 확인 필요 |
| **Privilege system** | `internal/check/privilege.go` (47 LOC — 2026-04-24 실측; 주석 포함. `isPrivilegedPackage[Path]` 헬퍼 2 개만 잔존) | `toolchain/check_gates.osty:376` | E0770 | **ported** | 2026-04-23 PR #770 로 호출부 제거, 후속 PR 로 Go duplicate walker + parity harness + 전용 test 전부 제거. Osty `runCheckGates` 가 authoritative. 동시에 `check_gates.osty::privilegeWalkTypeAlias` 의 `children2` → `children` 버그 수정 (TypeAlias 는 generics 를 `children` 에 둠 — §19.4 Pod-bound 이 type alias 에서 누락되던 실버그 해소) |
| **POD shape analysis** | (Go duplicate 제거됨) | `toolchain/check_gates.osty:146` | E0771 | **ported** | 2026-04-23 same consolidation. Go `podshape.go` / `podshape_test.go` 전부 제거 |
| **Noalloc analysis** | (Go duplicate 제거됨) | `toolchain/check_gates.osty:275` | E0772 | **ported** | 2026-04-23 same consolidation. Go `noalloc.go` / `noalloc_test.go` 전부 제거 |
| Raw-ptr handling | privilege + podshape gates | `toolchain/check_gates.osty:runtimeGated*` | E0770, E0771 | partial | privilege + POD 결합 |
| **Intrinsic body support** | `selfhost.IntrinsicBodyDiagsForSource` wrapper (PR #769 에서 `check.go` 의 Go-only 호출 제거; PR #770 에서 non-intrinsic gate 호출들도 제거) | `toolchain/check_gates.osty:63` + `selfhostIntrinsicBodyDiagnosticsFromArena` | E0773 | **ported** | 2026-04-23 Go 레거시 `intrinsic_body.go` (93 LOC) + 전용 test 제거. Osty arena gate 가 single source of truth. native `CheckPackageStructured` 내부 호출 + 비-native 경로는 public `selfhost.IntrinsicBodyDiagsForSource(src, path)` 래퍼 경유. silent diverge 불가능 |
| Package input / workspace parallel | `internal/check/package_input.go` | — | — | **go-only** | 호스트 경계 worker pool, 캐시 fingerprinting |
| HIR lowering orchestration | `host_boundary.go` (JSON 읽기) | `toolchain/hir_lower.osty` 전체 | — | ported | Osty 가 decl walk + annotation 추출, Go 는 결과 소비 |
| File/Package/Workspace 진입점 | `internal/check/check.go` | `toolchain/check.osty` | — | partial | Go 가 native exec + embed 래퍼, Osty 가 phase 1–7 |
| **Type query / LookupType API** | `internal/selfhost/check_query.go` (api.CheckResult offsets) | `toolchain/check.osty::selfCheck{Type,LetType,Symbol}NameAtOffset` + `selfCheckHoverAtOffset` | — | **ported** | 2026-04-23 Osty canonical 알고리즘 착륙 + Go-side `selfhost.{TypeAtOffset,LetTypeAtOffset,SymbolNameAtOffset,HoverAtOffset}` 브리지 (PR #787). **후속 정리 (이 PR)**: `internal/check/query.go` (127 LOC, `Result.TypeAt/SymbolAt/LetTypeAt/Hover` + 로컬 `HoverInfo`) 가 내부 호출만 있고 external consumer 가 0 이었음을 grep 으로 확인 후 전체 삭제. `check.Result` 경유 레거시 쿼리 경로 소멸, `selfhost.HoverInfo` + offset API 가 유일 — LSP/lint/inspect 는 이미 `findNamedTypeAt` / `targetSymbolAt` 등 자체 LSP 헬퍼를 쓰고 있어 이관 불필요했음 |
| Diagnostic file path 결합 | `host_boundary.go::stampPackageDiags` | `toolchain/check_diag.osty` | — | partial | Go 가 경로 스탬프, Osty 가 메시지 렌더 |
| **Native checker exec 경계** | `host_boundary.go::nativeCheckerExec` | — | — | **n/a** | 외부 바이너리 인터페이스 — 포팅 대상 아님 |
| Embedded selfhost bridge | `host_boundary.go::embeddedNativeChecker` | `toolchain/check_bridge.osty` (9줄) | — | n/a | **9줄짜리 얇은 stub — AST 변경에 취약** |
| **Inspect / per-node 기록** | `internal/check/inspect.go` (647 LOC — 2026-04-24 실측) | `toolchain/inspect.osty` + `toolchain/inspect_hint.osty` + tests + `internal/selfhost/inspect_adapter.go` (Go 브리지) | — | **partial** | 2026-04-24 점진 착륙: v1~v1.11 까지 포팅. **2026-04-24 후속**: bundle 등록 + `generated.go` regen + Go 브리지 착륙. `toolchain/inspect.osty` / `inspect_hint.osty` 를 `bundle.toolchainCheckerFiles` 에 추가 후 `go generate ./internal/selfhost/...` 이 exit 0 + `go build ./...` 통과. PR #824 의 `for _ in` → `for _k in` 리네임이 transpiler 버그를 우회했고, `elabPoisonResult` regression 은 재현되지 않아 별도 수정 없이 해소. `internal/selfhost/inspect_adapter.go::InspectFromSource(src []byte) []api.InspectRecord` 공개. `api.InspectRecord` (byte offsets + 렌더된 타입 문자열) 는 `check.InspectRecord` (`types.Type` + `token.Pos`) 와 달리 self-host 의 pre-lift 표현 그대로 노출 — CLI 와 IDE 소비자는 구조적 타입이 필요하면 당분간 Go-side `check.Inspect` 유지. **남은 갭**: (1) For-loop body / 필드 접근 chain / method call receiver 같은 기타 container shape 미커버. (2) Expression `notes` (generic instantiation / method-call marker) 비어있음. (3) CLI `osty check --inspect` 는 여전히 Go `check.Inspect` 호출 — `--arena` 토글 추가 + Go parity 검증 후 전환. |
| Generic bound checking | `(hostboundary)` | `toolchain/elab.osty` | E0749 | ported | monomorph 시 bound 강제 |
| Pattern shape mismatch (E0753) | `(hostboundary)` | `toolchain/elab.osty:elabPattern` | E0753 | ported | — |
| Closure annotation requirement | `(hostboundary)` | `toolchain/elab.osty:1849` | E0752 | partial | param seeding 만 |

### Checker 포팅 우선순위

1. ~~**Intrinsic body (E0773)**~~ — **착륙 2026-04-23**. Osty side (`toolchain/check_gates.osty` + `selfhostIntrinsicBodyDiagnosticsFromArena`) 가 이미 존재했고, Go 레거시 `intrinsic_body.go` (93 LOC) + test 제거 + `check.go:208/285` 콜사이트를 public `selfhost.IntrinsicBodyDiagsForSource(src, path)` 래퍼로 대체. Native path (CheckPackageStructured 내부) 와 Go fallback 모두 동일 arena gate 공유 — silent diverge 불가능.
2. **Privilege / POD / Noalloc 3-gate 통합** — 양쪽 `partial` 인데 **독립 실행 중** — silent divergence 리스크 제일 큼. 방법: 해당 gate 를 `check_gates.osty` 단일 소스로 몰고 Go 쪽은 adapter 만 남긴다. 3개가 구조적으로 유사하니 한 번에 처리.
3. ~~**Builder auto-derive 단일화**~~ — **착륙 2026-04-24**. derive-policy shared source (`toolchain/builder_policy.osty`) 분리 후 selfhost checker 가 builder chain typing / E0774 를 직접 수행하고, Go AST prepass `builder_desugar.go` 는 제거. Go `internal/ir/lower.go` 는 `check.ClassifyBuilderDerive` adapter 만 재사용.
4. **Inspect 포팅** — **v1~v1.11 + Go 브리지 착륙 2026-04-24**. bundle 등록 / regen / `selfhost.InspectFromSource` 공개 완료. **Follow-up**: (a) For-loop body / 필드 chain / method receiver 등 미커버 container shape 추가. (b) Expression `notes` 채우기 (generic instantiation / method-call marker). (c) CLI `cmd/osty inspect` 의 `--arena` 토글 추가 + Go `check.Inspect` parity 검증 → Go 복제본 제거.
5. **Annotation semantic validation 완성** — `#[vectorize]`/`#[parallel]`/`#[hot]`/`#[pure]` 를 `hir_lower.osty` 에 추가. v0.6 A5–A13 stanza 기반 스펙이 권위.
6. **Defer lifecycle 설계** — 정적 검증 (E0603·E0608·L0007) 은 Go·Osty 양쪽 이식 완료 (2026-04-24). 런타임 lifecycle (LIFO / `?` propagation / cancellation / abort-skip) 은 백엔드 cross-cutting 이라 `SPEC_GAPS.md` entry 선행.
7. **Package input / worker pool** — 호스트 경계 바깥이지만 Osty 로 가져오려면 파일 IO + 프로세스 스케줄링 이 Osty 측에 필요. 가장 뒤.

### LSP 포팅 현황 (2026-04-24 실측)

`toolchain/lsp.osty` ~1200 LOC 가 이미 순수 에디터 정책을 커버 — 시맨틱 토큰 legend (`lspSemanticTypeForTokenKind/SymbolKind`), UTF-16 위치 변환
(`lspOstyPositionToLSP`, `lspPositionToOsty`), completion sort key
(`lspCompletionSortTextForSymbolKind`), hover signature
(`lspHoverSignatureLine`), 심볼/import sort (`lspSortSymbolIndexes` /
`lspSortImportIndexes`), code-action kind filter (`lspWantsCodeActionKind`),
diagnostic payload (`lspDiagnosticPayload`) 모두 Osty side 에 있음.

`internal/lsp` 의 Go 쪽 (`handlers.go` / `refactor.go` / `completion.go` / `codeaction.go`)
은 대부분 이 pure fn 들을 thin wrapper 로 호출하는 orchestration —
`*ast.UseDecl` / `*resolve.Symbol` / `*check.Result` 등 **Go identity
type** 을 소비하기 때문에 단순 포팅이 아니라 identity-model migration 이
선행되어야 함 (#727 이 NodeID-keyed maps 로 해제했으나 LSP caller 는 아직
전환 전). 현재 상태에서는 추가 port 가 오히려 orchestration 을
복잡하게 만들 위험이 있어 **LSP 는 active 포팅 항목에서 제외**.

다음 사이클의 작은 착륙 후보 (진입 비용 낮음):
- completion sort 를 `labels: List<String>` → `List<Int>` 순수 index 재정렬로 완결 (`SortLSPCompletionIndexes` 를 selfhost 단일 호출로 단순화)
- `symbolDoc` 의 AST decl → doc comment switch 를 `ast_lower.osty` 에 쌍둥이로 이관하고 Go 는 1-line delegator 로 축소
- orchestration-layer identity 전환 (arena NodeID) 가 선행되면 `organizeImportsAction` / `completionItemFromSym` 전체 이관 검토

**2026-04-26 Phase 0 착륙 — value-typed view boundary**:
`generated.go` frozen seed 제약 때문에 정책을 lsp.osty 로 더 옮겨도 Go LSP
런타임에 닿지 않는다 (PR #854). 대신 **leaf consumer 함수의 pointer-arg 표면을
value-typed view 로 깎아 내** orchestration 자체를 LLVM self-host LSP 가
catch up 할 때 trivial-portable 하게 만드는 경로로 전환.

`selfhost/lsp_policy.go` 에 두 개의 view 타입 신설:
- `LSPSymbolView{Name, Kind, TypeText, DocText, HasSym}` — hover/completion 공용.
- `LSPUseDeclView{PosOffset, EndOffset, Path, RawPath, Alias, IsFFI, FFIPath}` —
  organize-imports / refactor 공용.

착륙한 phase:

| phase | 파일 | 변경 |
|---|---|---|
| 0a | `internal/lsp/handlers.go` | `hoverForSymbol` → `hoverSymbolView` extractor + `selfhost.LSPHoverMarkdown(view)`. `writeSymSignature` 삭제. |
| 0b | `internal/lsp/completion.go` | `completionItemFromSym` → `completionSymbolView` + `completionItemFromView`. 모든 정책 (Kind/Detail/SortText) 가 view 통과. |
| 0d | `internal/lsp/refactor.go` | `keyedUse.u *ast.UseDecl` → `keyedUse.view LSPUseDeclView`. `unusedUseSet map[*ast.UseDecl]bool` → `unusedUseOffsets map[int]bool`. `useGroup`/`useKey`/`useSourceText`/`endOfLineOffset`/`hasTriviaBetweenUses`/`keyWithAlias` 1-line shim 6 종 삭제 — 모두 `LSP*` 직접 호출. `useDeclViews([]*ast.UseDecl) []LSPUseDeclView` 가 유일 pointer-touch site. |

평가 결과 보류 (별도 design 필요):

- **0c (signature.go)**: 단일 `symbolDoc` call 외에는 `*types.FnType` /
  `*ast.FnDecl` 포인터에 직접 의존. extraction layer 를 추가해도 가치 낮음.
- **0e (references.go)**: `if refs[id.ID] != target` 의 **포인터 동등 비교가
  알고리즘 그 자체**. value-typed view 로 깎을 수 없고 `SymbolID`
  (`internal/resolve/symbolid.go`) 또는 NodeID 기반 identity 모델을 도입해야
  한다. `containsNode` 의 `ast.Node(d) == decl` 도 같은 종류. 별도 PR.

후속 항목:
- `references.go` SymbolID identity 도입 (별도 design 문서 선행 권장)
- `signature.go::buildSignatureInfo` 는 `LSPBuildSignatureText` 가 이미
  selfhost 라 추가 작업 가치 낮음 — close as won't-fix 후보

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

- ~~**Phase 0 (immediate, blocks every other gate)**: 위 "현재 first-walls"
  의 11 type errors 해소.~~ **착륙 2026-04-26**. (a) `MirSwitchCase` rename →
  `MirGenSwitchCase` (별도 PR 에서). (b) `typeName` → `frontTypeReprToString(typeRepr)`
  9 consumer 사이트 (PR #927). (c) `scopeFor` O(N²) → sorted+binsearch+memoize
  (이 PR). smoke + AST probe + Pipeline-Clean (LLVM011 까지 진입) 모두 진행.
  ErrType floor 474→426. 위 "현재 first-walls (post-Phase-0)" 표가 새 baseline.
- **Phase 1 (resolver 잔여)**: `pub use` 가시성 (E0553) workspace pub-symbol
  graph. **착수 보류** — scoped import (G28) 가 현재 `pub use std.fs::{open}`
  를 `pub use std.fs.open` 으로 expand 만 하고 resolution 단계에서는 "package
  std.fs.open not found" 로 떨어진다. E0553 emit 전에 (i) symbol-aware use
  resolution (cross-package symbol lookup) (ii) workspace pub-symbol graph
  (iii) re-export chain traversal 의 3 종 인프라 선행. 매트릭스 1c.5 의
  `internal/resolve/{resolve,cfg,prelude,scope}.go` 실제 삭제는 lint /
  formatter / LSP 의 identity 소비자 포팅과 맞물려 진행.
- **Phase 2 (checker 활성화)**: inspect 미커버 container shape (for-body /
  field chain / method receiver) → annotation A5/A8/A9/A10/A11/A13 LLVM
  emit 경로 완성 → defer runtime lifecycle (SPEC_GAPS 선행) → workspace
  worker pool.
- **Phase 3 (post-Phase-0 quality gates)**: 426 ErrType floor 잔여 분포
  분석 + `LLVM011 CheckFnSig.hasReceiver default` legacy fallback wall.
  Phase 0 fix 들과 독립이지만 merged MIR pipeline gate 정상화 위해 필요.

각 항목 착수 시 `SPEC_GAPS.md` / `SELFHOST_PORT_MATRIX.md` 업데이트 및 `just build-all` 후 진행.
