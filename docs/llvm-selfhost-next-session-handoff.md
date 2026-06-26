# LLVM self-host — 다음 세션 handoff

> **목적**: fresh context 진입 시 5분에 읽고 다음 PR 진입 가능한 단일 명세.
> **상태**: 2026-05-17 후속 세션 종료 시점. 50+ PR 머지 + R3 trajectory brick 1-12 + PR3-D~F activation 완성.

## 1. 30초 요약

- **목표**: `cmd/osty-native-checker` 의 LLVM 자체 빌드 + Go-built 와 byte-identical `CheckResult` JSON. plan 본문은 [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md).
- **현재**: M1 ✓, M2 partial ✓, 🎉 **source bootstrap UNLOCK** ([PR #1863](https://github.com/choiceoh/osty/pull/1863)). **R3 trajectory 12 brick + PR3 activation 완성**:
  - Brick 1-5 ([#1873-83](https://github.com/choiceoh/osty/pull/1873)) — type cover + C wrappers + lir_proto inline + 10 graceful fallback sites + broader local fallback
  - Brick 6+7 ([#1885](https://github.com/choiceoh/osty/pull/1885)) — **recoverOperandType MatchExpr/BlockExpr cover + mirjson ErrType→Int downgrade**. MIR JSON `<error>` literal 완전 0건
  - PR3-C step 3 ([#1886](https://github.com/choiceoh/osty/pull/1886)) — cross-pkg method-call dispatch arm
  - PR3-D scaffold ([#1889](https://github.com/choiceoh/osty/pull/1889)) — toolchain [lib] manifest
  - PR3-E/F activation ([#1890](https://github.com/choiceoh/osty/pull/1890)) — cross-pkg method-call E0703 해소
  - measurement v1-v4 ([#1892-3/1896/1899](https://github.com/choiceoh/osty/pull/1892)) — wall trigger 정확화
  - Brick 10 ([#1898](https://github.com/choiceoh/osty/pull/1898)) — term.branch i64 cond → i1 coerce
  - Brick 11 ([#1900](https://github.com/choiceoh/osty/pull/1900)) — term.branch aggregate cond → disc extract + icmp eq 0
  - **Brick 12** ([#1901](https://github.com/choiceoh/osty/pull/1901)) — **assign.dest `<error>` → i64 fallback**
  - PR-G1 scaffold ([#1902](https://github.com/choiceoh/osty/pull/1902)) — backend.Request.ExtraObjects link plumbing
- **남은 unlock 후보**:
  1. **declined messages 그대로** — `term.branch i1, got %Option.Int` + `assign.dest <error>`. brick 11/12 머지 후 PR #1898 같은 외부 fix wave 가 추가로 cover 확장해야 osty-self runtime 에 반영. Chicken-and-egg: 우리 변경이 binary 에 들어가지만 stage0 cover gap 으로 wrong code emit
  2. **doctor direct smoke** — 2026-05-19 Windows/clang path now dispatches `osty-self --selfhost-doctor` directly and exits 0 after runtime argv capture + self-rebuild file/link portability fixes.
  3. **PR-G2~G3 (link plumbing 활성화)** — PR #1902 scaffold 후 multi-file dep 의 .o link 활성. cross-pkg fn call 의 진짜 link wall 해소
- **합의 없이 자율 진행 가능**: §6.

## 2. 빌드 + 검증 (cheat sheet)

```sh
# Walking skeleton + naive parser 빌드 (PR1c + PR2)
go build -o .bin/osty ./cmd/osty
OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/

# 실행 + 비교 (M2 partial byte parity 검증)
go build -o /tmp/go-checker ./cmd/osty-native-checker
LLVM_OUT=$(echo '{"source":""}' | ./cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm)
GO_OUT=$(echo '{"source":""}' | /tmp/go-checker)
diff <(echo "$LLVM_OUT") <(echo "$GO_OUT") && echo IDENTICAL

# 🎉 source bootstrap UNLOCK 재현 (osty-self install, PR #1863)
OSTY_STAGE0_FALLBACK=1 \
  .bin/osty install-self

# stage0 audit (현재 100.0% — 8240/8241, PR #1858 머지 후)
OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/

# production path 시도 (osty-self cached 후) — next wall: mirJsonObjectGetNamed
.bin/osty build --backend llvm cmd/osty-native-checker/

# Front-end 회귀 (단일 PR 후 검증)
just prepush  # fmt-check + vet + repair-check + ci
just front    # 전체 front-end 회귀
```

## 3. 핵심 파일 (변경 흔한 곳)

| 파일 | 역할 |
|---|---|
| `cmd/osty-native-checker/main.osty` | LLVM-built entry, naive parser + stub CheckResult |
| `cmd/osty-native-checker/osty.toml` | `[bin]` + (현재) 빈 `[dependencies]` |
| `cmd/osty-native-checker/README.md` | dual-target 빌드 안내 + 한계 |
| `internal/mir/lower.go::qualifiedSymbol` | `rewriteStdlibSymbolToRuntime` helper (PR1c 옵션 1) — `std.io.readLine → osty_rt_io_read_line` |
| `internal/selfhost/api/types.go::ResolvedSymbol.ImportPath` | use-decl import path 필드 ([PR #1842](https://github.com/choiceoh/osty/pull/1842)) |
| `internal/selfhost/resolve_adapter.go::selfhostUseAliasImportPaths` | post-processing walker |
| `internal/selfhost/generated.go:41950` | **frozen seed의 method-call dispatch** (step 3 wall) |
| `internal/selfhost/generated.go:28553` | E0703 emit (`diagUnknownMethod`) |
| `toolchain/elab.osty:19712` | transpile source 의 method-call dispatch (generated.go 와 drift) |
| `internal/backend/stage0/emit.go` | stage0 fallback emitter (17679 LOC, 99.8% cover) |
| `internal/backend/runtime/osty_runtime.c::osty_rt_io_read_line` | C runtime (line 25384) |
| `SPEC_GAPS.md::cross-pkg-module-resolution` | open gap (path #5 = step 3 wall) |

## 4. 알아야 할 wall + 우회 패턴

### 4.1 stage0 fallback 의 stdlib body injection wall

> **Default flipped (2026-05, PR #1998)**: `OSTY_STDLIB_BODY_LOWER` is **ON**
> unless set to `0` / `false` / `off`. `just bootstrap` and CI no longer mask
> it. Historical notes below describe the pre-flip bisect axes.

- **`OSTY_STDLIB_BODY_LOWER=0` (escape hatch)**: `std.json.parseValue` 등 undefined symbol (link 실패) — use only to bisect injection regressions
- **`OSTY_STDLIB_BODY_LOWER=1` (production default)**: stdlib body inject 되나 stage0 패턴 매칭 부족 시 `osty_std_json__asString does not match any stage0 pattern` 같은 decline 가능
- **우회**: PR1c 옵션 1 패턴 — `internal/mir/lower.go::qualifiedSymbol` 에 stdlib → C runtime symbol rewrite (`std.io.readLine → osty_rt_io_read_line` 사례). C runtime 함수가 존재해야 작동.
- **`rewriteStdlibSymbolToRuntime` 에 향후 추가**:
  ```go
  case "std.io.readAll":
      return "osty_rt_io_read_all"  // 단 osty_rt_io_read_all 가 C runtime 에 부재
  ```

### 4.2 std.json wall

- 본 wall 의 우회 = manual naive parser (PR2 옵션 3', `strings.indexOf` + `strings.slice` 만). 정확성 한계 (escape 미처리).
- 진짜 fix = std.json.* 의 C runtime mapping 또는 stdlib body 의 stage0 cover.

### 4.3 cross-package method-call wall (step 3, 단일 unlock 차단)

세 use form 모두 같은 wall ([#1847](https://github.com/choiceoh/osty/pull/1847)):
- `use toolchain.check as tc; tc.fn()` → E0703 (method-on-type)
- `use toolchain.check::{fn}` → E0704 (not callable)
- `use toolchain.check.fn` → E0704 (not callable)

E0703 emit site = `generated.go:41950` (transpiled from `toolchain/elab.osty:19712`). **frozen seed** 라 수정 = spec 위반.

**유일한 path = 옵션 c** (spec narrow exception). [#1848](https://github.com/choiceoh/osty/pull/1848) spec draft 가 합의 대기.

## 5. 합의 후 sub-PR sequencing (옵션 c)

`docs/llvm-selfhost-plan-pr3-c-step3-c-spec-draft.md` 의 §3 narrow exception 표현이 CLAUDE.md "하지 말 것" 에 합의 머지된 후:

```
PR3-C-step3-c-spec-merge (~50 LOC)
  ↓
PR3-C-step3-c-impl       (~50 LOC, generated.go:41950 + elab.osty:19712 동등)
  ↓
PR3-C-step3-c-test       (use toolchain.check + tc.fn() 통과 검증)
  ↓
PR3-D ~ PR3-F            (cmd/osty lib build path + 진짜 checker 호출 + corpus parity)
```

### PR3-C-step3-c-impl 의 구체 변경

`generated.go:41928~41958` 부근 (transpiled from elab.osty 의 method-call dispatch):

```go
// 현재:
ownerName := elabOwnerNameForReceiver(cx.env.tys, lookupRecvTy)
lookedUpSig := checkLookupMethodForReceiver(cx.env, lookupRecvTy, ownerName, methodName)
if sig.name == "" {
    cx.env.local.diagnostics = append(..., diagUnknownMethod(...))
    return elabPoisonResult(...)  // ← wall
}

// 추가할 분기 (sig.name == "" 직전):
if pkgSym := findPackageSymbol(cx.env, ownerName); pkgSym != nil && pkgSym.ImportPath != "" {
    if moduleSig := checkLookupMethodInModule(cx.env, pkgSym.ImportPath, methodName); moduleSig.name != "" {
        sig = moduleSig
        // module-scoped 로 emit 분기 (기존 path 와 같이)
    }
}
```

같은 변경을 `toolchain/elab.osty:19712` 부근에 동등 적용 (drift 방지 의무).

## 6. 합의 없이 자율 진행 가능한 후보

1. **stage0 16 decline 함수 cover 측정** — 현재 99.8% (8221/8237). 16 decline 의 shape 별 분류 → `stage0_p24_scope.md` 패턴 따라 다음 unlock 후보 식별. 측정 doc.
2. **`osty.lock` 의 `examples/` 패턴 정리** — examples 의 `main.osty.lock` 일부 stale 가능성 (toolchain 새 머지 후). audit + 갱신.
3. **`cmd/osty-native-checker/main_test.go` 갱신** — Go-built shell 의 test 가 LLVM-built 와의 byte parity 도 검증하도록 ` 추가.
4. **`benchmarks/` 추가 fixture** — `cmd/osty-native-checker` 의 dual-target build time + 빌드 산출물 크기 측정.
5. **LSP / lint 의 `ResolvedSymbol.ImportPath` consumer** — step 1+2 ([#1842](https://github.com/choiceoh/osty/pull/1842)) 데이터를 LSP 의 cross-package hover / goto-def 등에서 활용 가능 여부 측정.

## 7. 이 세션 (2026-05-16~17) 27 PR 카탈로그

| # | PR | 핵심 산출 |
|---|---|---|
| 1 | [#1812](https://github.com/choiceoh/osty/pull/1812) | plan 본 doc + main fmt 회귀 |
| 2 | [#1814](https://github.com/choiceoh/osty/pull/1814) | Spike 1 (Q1–Q5, stage0 99.8%) |
| 3 | [#1816](https://github.com/choiceoh/osty/pull/1816) | **M1** stub binary builds + runs |
| 4 | [#1819](https://github.com/choiceoh/osty/pull/1819) | Spike 2 (Q6–Q11) |
| 5 | [#1821](https://github.com/choiceoh/osty/pull/1821) | M2 stub byte parity |
| 6 | [#1823](https://github.com/choiceoh/osty/pull/1823) | PR1c blockers (옵션 a/b/c) |
| 7 | [#1825](https://github.com/choiceoh/osty/pull/1825) | PR1c-1 negative (stage0 emit 분산) |
| 8 | [#1826](https://github.com/choiceoh/osty/pull/1826) | **🎉 PR1c — 진짜 stdin via qualifiedSymbol rewrite** |
| 9 | [#1827](https://github.com/choiceoh/osty/pull/1827) | PR2 attempt (std.json wall) |
| 10 | [#1829](https://github.com/choiceoh/osty/pull/1829) | **PR2 manual naive parser (옵션 3')** |
| 11 | [#1830](https://github.com/choiceoh/osty/pull/1830) | PR3 attempt (cross-package wall) |
| 12 | [#1831](https://github.com/choiceoh/osty/pull/1831) | PR3 design ([lib] crate model) |
| 13 | [#1832](https://github.com/choiceoh/osty/pull/1832) | PR3-A spec 명확화 |
| 14 | [#1834](https://github.com/choiceoh/osty/pull/1834) | PR3-B [lib]-only manifest decline |
| 15 | [#1835](https://github.com/choiceoh/osty/pull/1835) | PR3-C 측정 (use-decl path 추적 부재) |
| 16 | [#1837](https://github.com/choiceoh/osty/pull/1837) | plan §12 갱신 (M1 ✓ / M2 partial) |
| 17 | [#1840](https://github.com/choiceoh/osty/pull/1840) | PR3-C-impl 시도 (dead code, toolchain/resolve.osty 변경이 frozen generated.go 와 drift) |
| 18 | [#1841](https://github.com/choiceoh/osty/pull/1841) | PR3-C-impl-go 분석 (post-processing layer 정통 path) |
| 19 | [#1842](https://github.com/choiceoh/osty/pull/1842) | **🎯 PR3-C-impl-go step 1+2 — ResolvedSymbol.ImportPath data flow** |
| 20 | [#1844](https://github.com/choiceoh/osty/pull/1844) | step 3 wall (generated.go:41950 + elab.osty:19712) |
| 21 | [#1845](https://github.com/choiceoh/osty/pull/1845) | step 3 design (옵션 c 권장) |
| 22 | [#1847](https://github.com/choiceoh/osty/pull/1847) | 옵션 d + 우회 use form 3 종 → 모두 같은 wall |
| 23 | [#1848](https://github.com/choiceoh/osty/pull/1848) | **PR3-C-step3-c-spec-draft (검토 대기)** |
| 24 | [#1849](https://github.com/choiceoh/osty/pull/1849) | cmd/osty-native-checker/README.md |
| 25 | [#1851](https://github.com/choiceoh/osty/pull/1851) | README.md status entry |
| 26 | [#1852](https://github.com/choiceoh/osty/pull/1852) | CHANGELOG_v0.5 entry |
| 27 | [#1854](https://github.com/choiceoh/osty/pull/1854) | cleanup (.gitignore cmd/*/osty.lock + graphify update) |

## 8. doc 시리즈 (cross-link map)

| doc | 내용 |
|---|---|
| [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md) | 본 plan (목표 + 비-목표 + walls + PR chain + corpus + milestone) |
| [docs/llvm-selfhost-plan-spike-findings.md](llvm-selfhost-plan-spike-findings.md) | Q1–Q5 측정 (stage0 99.8% 확인) |
| [docs/llvm-selfhost-plan-pr1b-readiness.md](llvm-selfhost-plan-pr1b-readiness.md) | Q6–Q11 (backend 매핑 inventory) |
| [docs/llvm-selfhost-plan-pr1c-blockers.md](llvm-selfhost-plan-pr1c-blockers.md) | PR1c 의 옵션 a/b/c attempt |
| [docs/llvm-selfhost-plan-pr1c-1-attempt.md](llvm-selfhost-plan-pr1c-1-attempt.md) | PR1c-1 negative result |
| [docs/llvm-selfhost-plan-pr2-attempt.md](llvm-selfhost-plan-pr2-attempt.md) | PR2 의 std.json wall + 4 옵션 |
| [docs/llvm-selfhost-plan-pr3-attempt.md](llvm-selfhost-plan-pr3-attempt.md) | PR3 의 cross-package wall (4 옵션) |
| [docs/llvm-selfhost-plan-pr3-design.md](llvm-selfhost-plan-pr3-design.md) | [lib] crate model spec + 경로 A/B 선택 |
| [docs/llvm-selfhost-plan-pr3-c-step3-design.md](llvm-selfhost-plan-pr3-c-step3-design.md) | step 3 의 옵션 a/b/c/d 비교 |
| [docs/llvm-selfhost-plan-pr3-c-step3-c-spec-draft.md](llvm-selfhost-plan-pr3-c-step3-c-spec-draft.md) | **CLAUDE.md narrow exception 검토안 (합의 대기)** |
| [cmd/osty-native-checker/README.md](../cmd/osty-native-checker/README.md) | dual-target 빌드 안내 |
| [SPEC_GAPS.md::cross-pkg-module-resolution](../SPEC_GAPS.md) | open gap (path #1~#5) |

## 9. 첫 결정 (다음 세션 시작 시) — 2026-05-17 갱신

```
🎉 큰 진척 (2026-05-17):
   - stage0 audit 100% (외부 PR #1858)
   - source bootstrap UNLOCK (PR #1863) — osty-self install
   - CLAUDE.md narrow exception 합의 머지 (PR #1857) — 단 impl 한도 초과

이제 다음 4 path:

A') osty-self monomorph cover wave — PR #1858 classifier mechanism 으로
    mirJsonObjectGetNamed 등 production path 의 stage0 decline 해소.
    PR #1858 author trajectory 의 후속. 단일 fresh session 가능.

B)  옵션 c' 재합의 — narrow exception 한도를 "dispatch arm" 에서 "단일
    mechanism" (~150-200 LOC) 으로 확대. cross-pkg symbol registration
    추가 (registerToolchainAliasFns).

C)  옵션 a 수용 — generated.go 의 method-call dispatch 직접 + struct
    field 추가. spec 위반 인지 + 단일 PR.

D)  PR3 자체 보류 — M2 partial 로 만족. source bootstrap UNLOCK 이 핵심
    milestone 이라 plan §12 의 M3/M4 trajectory 갱신 가능.
```

**권장 (옵션 A')**: production path next wall (mirJsonObjectGetNamed) 의 stage0 cover wave 가 가장 직접적 진척. PR #1858 의 3-commit cascade 패턴 재사용 가능. unblock 시 production path 전체 활성 → cmd/osty-native-checker 의 M3 (L2 corpus) 자동 가능성.

## 10a. 2026-05-17 옵션 γ Phase 1 인벤토리 측정

`toolchain/mir_generator.osty` 의 Phase 1 단계 (grep `Phase 1[a-z]`):

| Phase | 내용 | 상태 |
|---|---|---|
| **1a** | block label + operand-free terminator (return / goto / unreachable) | ✓ 착륙 |
| **1b** | terminator operands wired (PR #1271) | ✓ 착륙 |
| **1c** | per-instruction emission (MirInstrAssign + rvalue arms) | **진행 중** (line 17715+) |
| **1d** | MirRVNullary 진짜 값 + MirInstrCall + Intrinsic | TODO |
| **1p** | MirConstString string pool | ✓ 착륙 |

`mirJsonObjectGetNamed` decline = Phase 1c/d 의 미구현 reach (for loop 의 list indexing + if + early Some return + None fallback = MirInstrAssign + multiple rvalue arms + MirInstrCall).

**옵션 γ 의 진짜 sub-trajectory** = mir_generator.osty 의 **Phase 1c + Phase 1d 진척**. 추정 작업:
- Phase 1c rvalue arms 확장 (~수십 LOC per arm × 10+ arms)
- Phase 1d MirInstrCall / Intrinsic emit (수백 LOC, intrinsic family 별)
- 검증: cmd/osty-native-checker build production path 통과

본 plan scope 외 — mir_generator owner trajectory. PR #1858 의 stage0 cascade 패턴 (PR1/PR2/PR3 wave) 으로 cover 추가도 같은 layer 의 별개 작업.

**2026-05-17 옵션 γ 추가 측정 — mirEmitModule entry 확인**:
- `toolchain/main.osty:524` 의 `osty-self lir-proto-lower` 명령이 `mirEmitModule(mir, opts) -> irText` 호출. 즉 LIR Proto pipeline 의 진짜 entry.
- `mirEmitIntrinsicInstr` (line 18906+) 의 cover 가 매우 깊음 — 수십 intrinsic family (Print/Println/Eprint, List*, Map*, Set*, String*, Channel/Spawn/...) match arm.
- `mirEmitCallInstr` (line 18746+) 도 calleeKind 분기 + projection prelude + indirect call 처리.

즉 Phase 1c 가 실제로 매우 진척된 상태. **`mirJsonObjectGetNamed` build 시 "stage0 declined" 의 진짜 원인 = mirEmitModule 거치고도 mirEmitFunctionStub 내부의 일부 rvalue/instruction 이 `; TODO Phase 1d` comment 또는 `unreachable` stub 으로 emit → 후속 LLVM verifier 또는 clang link 단계에서 silent fail → osty-self 가 fallback path 로 throw**. 즉 LIR Proto pipeline 활성이지만 cover gap.

진짜 fix path 의 구체:
1. `mirEmitInstruction` 의 dispatch 에 missing rvalue/intrinsic kind 추가 — case-by-case (수십 LOC per case)
2. `; TODO Phase 1d` comment 마다 진짜 emit code 추가
3. unreachable stub 의 진짜 LLVM IR 대체

이 작업이 mir_generator owner 의 본격 trajectory. 외부 작업자 (PR #1858 author 등) 의 후속 wave 가능성.

## 10. 2026-05-17 최종 측정 (PR #1865/#1868)

**audit scope 의 한계 발견** + **backend audit-out 함수 수**:

| file | 함수 수 | scope |
|---|---|---|
| toolchain/mir_json.osty | 126 | audit 외부 |
| toolchain/mir_lower.osty | 306 | audit 외부 |
| toolchain/mir_optimize.osty | 39 | audit 외부 |
| toolchain/mir_generator.osty | **2659** | audit 외부 |
| toolchain/llvmgen.osty | 500 | audit 외부 |
| toolchain/lir_proto.osty | 418 | audit 외부 |
| **backend 합 (audit 외부)** | **4048** | — |
| (참고) toolchainCheckerFiles audit cover | 8240 | audit 내부 |

audit 100% 는 checker bundle 만. backend 4048 함수의 stage0 cover 는 별도 trajectory. PR #1858 의 16 decline → 0 cascade 도 checker bundle 만 적용.

**진짜 next-phase trajectory**: backend 4048 함수의 stage0 cover wave. PR #1858 의 classifier mechanism + 3-commit cascade 패턴 재사용 가능. 단일 fresh session 으로 가능한지 미지수 (4048 vs 16 비율 100배 이상).

대안:
- **bundle scope 확장** (옵션 α'): toolchainCheckerFiles 에 backend 파일 추가 → 새 audit 측정 → cascade unblock
- **osty-self LIR Proto pipeline (Phase 1+) 직접 활성** (옵션 γ): stage0 우회, osty-self 의 별도 구조 변경
- **PR #1858 author 후속 wave wait** (옵션 β): 외부 trajectory 의존

위 선택지 중 하나가 명확해지면 본 doc 의 §2 cheat sheet 만으로 즉시 PR 시작 가능.

### 10.1 옵션 α' framing 결함 (2026-05-17 측정)

`toolchainCheckerFiles` 에 backend 6 파일 (mir_json/mir_lower/mir_optimize/mir_generator/llvmgen/lir_proto) 추가 시도. 측정 결과 **framing 결함 발견**:

- `bundle.ToolchainCheckerFiles()` / `bundle.MergeToolchainChecker(root)` 의 **production consumer 0건**. `grep` 결과 `bundle_test.go` + `toolchain_checker_probe_test.go` (test only) 만 호출
- `cmd/osty-native-checker/main.go` 가 `selfhost.CheckSourceStructured / CheckPackageStructured` 호출 → `internal/selfhost/generated.go` (frozen seed) 가 진짜 production checker. **bundle merge 와 무관**
- `internal/toolchain/native_checker.go::buildNativeChecker` 가 `go build ./cmd/osty-native-checker` 로 native checker 빌드 — bundle scope 미참조

**즉 옵션 α' 은 probe-only 작업**. bundle 확장은 production checker capability 에 영향 zero. handoff §10 의 "α' = production capability 확장" framing 이 실제 mechanism 과 mismatch.

진짜 production capability 확장 path = `internal/selfhost/generated.go` 의 backend logic 추가:
1. **옵션 c' (재합의)**: narrow exception 한도 확대 + `registerToolchainAliasFns` 같은 단일 mechanism. spec 재합의 필요
2. **옵션 γ (deep)**: type-propagation gap fix (`<error>` leak 의 IR→MIR seam, `internal/mir/lower.go:3012` 코멘트 명시). multi-PR, 측정 spike 부터 시작 필요
3. **frozen seed 정책 재고**: generated.go regen 모델 재도입 — `internal/selfhost/generated.go` 자체를 다시 자동 생성 가능하게. CLAUDE.md 의 frozen seed 결정 (PR #854) 자체 revisit

α' 자체는 dropped path (probe-only). 다음 trajectory 는 위 3 path 중 선택.

### 10.2 인터-instr 블록 분할 렌더링 한계 (2026-05-19 측정 / 해소)

`xs.get(i)` 의 unsafe-get → safe Option-wrap 시도 중 발견했던 **아키텍처적 한계**. 후속 PR 두 개로 해소.

**원래 한계 (PR #1925 문서화)**: `lirCutBlockCondBr` / `lirCutBlockBr` 가 현재 블록을 `l.extraBlocks` (LirBlock 객체) 에 push 하지만, function 렌더링이 사용하는 `renderedBlocks: List<String>` 에는 안 추가됨. 결과적으로 인터-instr cut 으로 만든 entry/none/some 블록 setup 이 IR 출력에서 사라지고 end 블록만 살아남아 broken IR.

**해소**:

1. **Architecture fix** (PR #1926): `lirAppendRenderedLirBlocks(renderedBlocks, blockLowerer.extraBlocks, 0)` 를 `lirLowerMirFunctionBlocksInto` 에 추가 (`toolchain/lir_proto.osty:2887`). extra LirBlock 들이 final 앞에 `lirRenderBlockText` 로 변환되어 `renderedBlocks` 에 들어감. `lirBlockIsPopSentinel` 스킵 + `outFn.blocks` LirBlock 리스트와의 순서 일치 유지. Guard test = `TestLirProtoStage2SeedCoercionAndIndexStoreGuards`.
2. **Safe `list.get` 본 구현** (이 PR): `lirLowerMirListGet` 시작에 dest-type sugar-aware gate 추가 — `lirIsOptionTypeName(destLoc.typ) || lirStringHasQuestionSuffix(destLoc.typ)` 일 때 `lirLowerMirListGetSafe` 로 분기. `lirLowerMirBytesGet` + `lirLowerMirListFirstOrLast` 패턴 (`len(list) → idx >= 0 && idx < len → some/none cut → merge`) 그대로. 일반 `list[i]` 경로 (dest = T, abort-on-OOB) 는 기존 path 유지.

**ABI 정합성**: 체커는 `List.get(index) -> Option<T>` 로 등록 (`toolchain/check_env.osty:2062`), MIR `IntrinsicListGet` 의 `Dest is T` 코멘트 (`internal/mir/mir.go:480`) 는 `list[i]` subscript 케이스 기준. 두 표면 (`list[i]` vs `list.get(i)`) 이 같은 intrinsic 으로 lowering 되므로 dest 타입으로 disambiguate. production go-side stage0 도 동일 dispatch (`internal/backend/llvm_list_get_safe_test.go` 의 `TestLLVMBackendBinaryRunsListSafeGet*` 가 Osty self-host 경로로 검증).

## 10. 본 doc 의 갱신 트리거

- 새 PR 머지 시 §7 카탈로그 갱신
- step 3 옵션 변경 시 §5 갱신
- 자율 작업 추가 시 §6 갱신
- 본 doc 머지 = 27 PR + handoff 완료 선언
