# LLVM self-host plan — `osty-native-checker` behavior parity

> **상태**: 제안 (draft). 합의 후 별도 PR chain.
> **연관**:
> - [docs/osty_self_bootstrap_design.md](osty_self_bootstrap_design.md) — 옵션 C (stage0 fallback emitter) 결정
> - [docs/osty_self_b2_1_audit.md](osty_self_b2_1_audit.md) — stage0 coverage 11.3% 시점 audit (2026-04-29 측정)
> - [docs/stage0_p24_scope.md](stage0_p24_scope.md) — 2026-05-11 시점 94.2% audit + P24 첫 target
> - [docs/post_1405_coverage_audit_design.md](post_1405_coverage_audit_design.md) — PR #1405가 삭제한 ~36k줄 회복 audit
> - [docs/osty_self_artifact_design.md](osty_self_artifact_design.md) — `osty-self` artifact 캐시
> - [SELFHOST_PORT_MATRIX.md](../SELFHOST_PORT_MATRIX.md) — frontend resolver/checker 포팅 매트릭스 (orthogonal track)
> - [LLVM_BACKEND_GAP_PLAN.md](../LLVM_BACKEND_GAP_PLAN.md) — PR #1405 이후 LLVM 백엔드 Phase A–F architecture
> **소유**: backend / toolchain.

## 1. 목표 한 줄

`cmd/osty-native-checker` 바이너리를 **LLVM 백엔드 자체 빌드**로 만들어, 같은 `CheckRequest` JSON 입력에 대해 Go-built 바이너리와 **바이트-동일한 `CheckResult` JSON 출력**을 내는 것.

"진짜 셀프호스팅" 의 가장 작은 의미 있는 단위. 이 한 바이너리만 닫히면, 그 다음 단계 (`cmd/osty` 전체, 탈Go 등) 는 같은 패턴의 반복이다.

## 2. 비-목표 (이 plan 의 scope 바깥)

| 항목 | 이유 |
|---|---|
| `OSTY_STDLIB_BODY_LOWER=1` (stdlib 본문 LLVM IR 인젝트) | 진짜 셀프호스팅 정의에 포함시키지 않음. stdlib는 `osty_runtime.c` 의 runtime symbol (`osty_rt_*`) 로 lowering. Go-built 도 같은 runtime 에 link 되므로 behavior parity 와 무관 |
| `cmd/osty/` 전체 탈Go (`main.osty` 경유 CLI) | scope 너무 큼. 별도 plan |
| `internal/selfhost/generated.go` (~70k LOC, 정확히 70615) 재생성 부활 | PR #854 에서 retire. CLAUDE.md "하지 말 것" 에 명시. frozen seed 유지 |
| `internal/resolve/{resolve,cfg,prelude,scope}.go` 4파일 삭제 | `SELFHOST_PORT_MATRIX.md` Phase 1c.5 의 frontend track. orthogonal |
| Fixed-point byte equality (`B2 ≡ B3`) | 검증을 더 어렵게 하지만 이 plan 의 본질은 아님. follow-up |
| ABI 고정 / 결정성 보장 — symbol ordering, hash seed 등 | byte-동일 JSON 출력만 강제. binary layout / debug info 동일성은 unclaimed |
| 기존 LLVM 백엔드 Phase A–F 의 잔여 wall 정상화 (`LLVM_BACKEND_GAP_PLAN.md`) | 우리 plan 은 그 wall 들 위에 올라탄다. 새 wall 만 다룬다 |

## 3. 현황 인벤토리 (2026-05-16 시점)

### 3.1 기존 셀프호스팅 trajectory 위치

- **PR #1405** (2026-05-05) — `internal/llvmgen` Go MIR emitter 112K LOC 삭제. LLVM 백엔드의 MIR→LLVM IR 변환은 이제 `osty-self lir-proto-lower` 서브프로세스 = `toolchain/{llvmgen,lir_proto,mir_generator}.osty` 컴파일 산출물에 의존.
- **stage0 fallback** — `osty-self` 가 부재할 때만 작동하는 Go 측 의도적으로 좁은 emitter (`internal/backend/stage0/`). bootstrap 닭-달걀 해소. **production 빌드 경로 아님**.
- **`scripts/verify-self-rebuild`** — stage2/stage3 byte parity 강제. fresh checkout 에서 self-host 부트스트랩이 가능한지 확인.
- **`scripts/audit-stage0-coverage.sh`** + `TestStage0ToolchainAudit` (`OSTY_STAGE0_AUDIT=1`) — toolchain/*.osty 함수가 stage0 surface 안에 머무는 비율 측정.
- **스파이크 측정 (2026-05-16 — [llvm-selfhost-plan-spike-findings.md §Q1](llvm-selfhost-plan-spike-findings.md))** — 역사적 스냅샷:

  | 메트릭 | 값 (스파이크 시점) |
  |---|---|
  | Stage0 audit cover (toolchain 전체) | **99.8% (8221 / 8237)** |
  | decline 함수 수 | **16** (모두 large complex 함수, blocks 11–103) |
  | `install-self` 실제 decline | PR1 시점에 재측정 (audit-install 격차 monomorph 효과) |

- **갱신 (2026-05-17, PR [#1858](https://github.com/choiceoh/osty/pull/1858))**: `TestStage0ToolchainAudit` 기준 stage0 audit cover **100.0% (8240 / 8241)**. 위 16개 decline 웨이브는 종결. 후속 노트·정정은 [`SPEC_GAPS.md`](../SPEC_GAPS.md) `cross-pkg-module-resolution` 타임라인(같은 날짜) — 요지: **audit-pass ≠ build-pass** (`install-self` / monomorph / LIR Proto 단계에서 별도 decline 가능), `bundle.ToolchainCheckerFiles()` probe vs `resolve.PackageSourcePaths` 전체 walk 구분, production `osty-self` 경로에서 남은 wall(예: `mirJsonObjectGetNamed`) 은 이 표의 퍼센트와 독립적으로 추적.

### 3.2 우리 plan 과 stage0 trajectory 의 관계

본 plan 은 stage0 trajectory 의 **sibling angle** 이다. 같은 산을 다른 면에서 오른다.

- stage0 trajectory: **"toolchain/*.osty 의 모든 함수가 stage0 fallback 으로 emit 되는가?"** — 부트스트랩 가능성. 측정 단위 = 함수.
- 본 plan: **"osty-native-checker 가 LLVM 으로 자체 빌드되고 Go-built 와 같은 동작인가?"** — 셀프호스팅의 의미 단위. 측정 단위 = behavior parity.

두 trajectory 가 만나는 지점:
- stage0 **audit** 100% (PR #1858 이후, §3.1) ⇒ 남은 블로커는 주로 **LIR Proto / monomorph / cross-pkg** 축 (`SPEC_GAPS.md` 동일 날짜 타임라인). LLVM-built `main.osty` 는 PR [#1938](https://github.com/choiceoh/osty/pull/1938) 이후 `tc.frontCheckSourceToWireJson` 로 **진짜 checker wire** 를 태우고, M4 (PR [#1940](https://github.com/choiceoh/osty/pull/1940), [#1942](https://github.com/choiceoh/osty/pull/1942)) 에서 Go adapter 와 맞춘 byte/telemetry/stable-id 층을 맞췄다 — 남은 큰 덩어리는 **stdin/JSON entry 품질**, **production 링크 (stage0 fallback 없이)**, **corpus (L2/L3)**.
- production 경로 (LIR Proto 서브프로세스) 는 **빌드된 `osty-self` 캐시** 에 의존한다는 점은 변하지 않는다. audit 퍼센트와 무관하게 subprocess 가 decline 하면 `OSTY_STAGE0_FALLBACK=1` 로만 bootstrap emitter 가 개입한다 (`internal/backend/llvm.go`).

### 3.3 `osty-native-checker` 의 현재 구조

**갱신 (2026-05-20)**: LLVM-built `main.osty` 는 Go shell 과 동일한 `CheckRequest` JSON 을 받아 `tc.frontCheckSourceToWireJson` (`toolchain/check_json.osty`) 로 checker 결과를 직렬화한다 — frozen seed 의 multi-step adapter 를 한 entry 에서 흉내 내는 형태가 아니라, Osty 측 front-check 가 wire 레이어까지 한 번에 처리한다 (세부 milestone 은 [`cmd/osty-native-checker/README.md`](../cmd/osty-native-checker/README.md)).

`cmd/osty-native-checker/main.go` (38 LOC, Go shell):

```
stdin → json.Decode(&CheckRequest)
     → selfhost.CheckPackageStructured / CheckSourceStructured  (frozen seed)
       → toolchain/check.osty + elab.osty + resolve.osty + ty.osty + ...  (generated.go embedded)
     → json.Encode(CheckResult) → stdout
```

자체 빌드한 LLVM 산출물이 대체해야 하는 것:
- Go `main()` 진입 — 우리 측 `cmd/osty-native-checker/main.osty` 신규
- `json.NewDecoder.Decode(&req)` — Osty 의 `std.json.parseValue` + manual walk
- `selfhost.CheckPackageStructured(*req.Package)` — **단일 entry 없음**. Go side adapter 가 다음 5단계 Osty 시퀀스를 호출 ([spike findings §Q3](llvm-selfhost-plan-spike-findings.md)):
  1. `selfhostBuildPackageAst(files) -> (file, layout)`
  2. `newElabCx(file, None)`
  3. `selfhostInstallImportSurfaces(cx.env, imports)`
  4. `elabFile(cx)` ← 핵심 elaboration
  5. `serializeCheckResult(cx) -> raw`
  6. `adaptCheckResultWithTokenLayout(raw, layout)` ← **현재 Go side 함수**, Osty 이식 필요 (N8)
- `selfhost.CheckSourceStructured([]byte(req.Source))` — 비슷한 5단계 (선두 두 단계만 `ostyLexSource` + `astParseLexedSource` 로 갈음)
- `json.NewEncoder.Encode(checked)` — `std.json.stringifyValue` + manual build

### 3.4 Wire shape

`internal/selfhost/api/types.go` (658 LOC, 19 struct) + `api/package.go` (`PackageCheckInput` / `PackageCheckFile` / `PackageCheckImport`):

```
// types.go 19 struct
CheckRequest, CheckResult, CheckSummary, CheckedNode, CheckedBinding,
CheckedSymbol, CheckInstantiation, CheckDiagnosticRecord, CheckResultIndex,
TypeRepr, ResolveSummary, ResolvedSymbol, ResolvedRef, ResolvedTypeRef,
ResolveDiagnosticRecord, SpanProvenanceRecord, ResolveResult, ResolveRequest,
InspectRecord

// package.go (CheckRequest.Package 의 element type 거주지)
PackageCheckInput, PackageCheckFile, PackageCheckImport
```

`CheckRequest.Package` 는 `*PackageCheckInput` — `api/package.go` 에 정의 (위 19 카운트에 포함되지 않음).

이 중 `cmd/osty-native-checker` 가 직접 다루는 것: **`CheckRequest` 입력 + `CheckResult` 출력 + 그 transitive 구성요소** (`CheckedNode` / `TypeRepr` / `CheckDiagnosticRecord` / `CheckSummary` / `PackageCheckInput` / `PackageCheckFile`).

`ResolveRequest` / `InspectRecord` 등은 별도 binary (`cmd/osty-native-resolver`, inspect adapter) 가 다루므로 본 plan scope 바깥. transitive 의존성으로 **10 struct 정도** 가 Osty 측 정의 필요.

## 4. 완성 조건

### 4.1 Primary gate — behavior parity

```
$ go build -o /tmp/go-checker ./cmd/osty-native-checker              # Go-built (production shell)
$ go build -o .bin/osty ./cmd/osty                                   # host driver (once)
$ .bin/osty build --bootstrap-stage0 --backend llvm cmd/osty-native-checker/   # LLVM-built target
# 산출물 경로는 profile 에 따라 다름 — 예: cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm
$ for fixture in testdata/selfhost_parity/*.request.json; do
    diff <(/tmp/go-checker < $fixture) <(./cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm < $fixture) || fail
  done
```

`cmd/osty-native-checker/osty.toml` 이 있으므로 디렉토리 인자 빌드는 지원된다. 다만 **bootstrap LLVM** 경로가 아직 실무 기본값인 이유는 `osty-self` LIR Proto 서브프로세스가 거절할 때 in-process stage0 emitter 가 필요하기 때문이다 (`--bootstrap-stage0`; [`cmd/osty-native-checker/README.md`](../cmd/osty-native-checker/README.md) §Build 참조). production 경로만으로 링크하는 측정은 [`docs/llvm-selfhost-plan-cross-pkg-link-measurement.md`](llvm-selfhost-plan-cross-pkg-link-measurement.md) 등에서 별도 추적.

- 두 binary 의 stdout 바이트가 정확히 같아야 한다 (byte-equal, not semantically-equal).
- stderr 는 비교 안 함 (Go panic backtrace 등 host 차이 허용).
- exit code 동일.

### 4.2 Corpus levels (점진)

| Level | 입력 종류 | 추정 fixture 수 | 통과 시점 |
|---|---|---|---|
| L1: seed | `testdata/selfhost_parity/01_*.osty` ~ `10_*.osty` (manually-curated minimal) | 10 | PR1–PR3 |
| L2: spec coverage | `testdata/spec/positive/**/*.osty` 자동 흡수 | ~100+ | PR-mid |
| L3: self-input | `toolchain/*.osty` 자체를 input 으로 (osty checks osty) | ~150 file 합쳐서 ~140k LOC | PR-final |

각 fixture 는:
1. `.osty` 소스를 `CheckRequest { source: <text> }` 또는 `CheckRequest { package: { files: [...] } }` 로 wrap 한 JSON.
2. 골든 `CheckResult` JSON (Go-built 으로 한 번 만들어 커밋).
3. CI 가 `llvm-checker < input.json | diff golden.json -` 실행.

L1 → L2 → L3 진행 중 stage0 coverage 가 같이 올라간다. L3 통과 = stage0 100% + JSON serde + entry point 모두 완성.

### 4.3 CI gate 명세

- `TestLLVMCheckerBehaviorParity` (or skill: `parity` test 카테고리) — 위 corpus 를 OSTY_LLVM_PARITY=1 env 로 실행. 기본 skip (heavy).
- `just parity` justfile target — 일상 개발 시 수동 실행.
- L1 통과는 PR-CI mandatory, L2/L3 는 nightly.

## 5. Walls inventory + 우선순위

기존 알려진 wall + 본 plan 에서 새로 발견될 가능성 있는 wall.

### 5.1 차단성 walls (이미 알려짐)

| # | Wall | 출처 | 처리 |
|---|---|---|---|
| W1 | stage0 coverage < 100% | stage0_p24_scope.md §0 | stage0 trajectory 가 다룸. 본 plan 은 그 진척에 의존 |
| W2 | `tyToRepr` / `useDeclTailAfter` / `frontTypeReprToString` 3 함수 | stage0_p24_scope.md §1 | P24 phase 가 다룸 |
| W3 | LIR Proto receiver `List<Pair>` / `Map<String, Pair>` composite lane | LLVM_BACKEND_GAP_PLAN.md Phase B | 이미 진행 중 |
| W4 | `Option<Struct>` aggregate / `Some(struct)` payload slot | LLVM_BACKEND_GAP_PLAN.md Phase C | 이미 진행 중 |
| W5 | `println(struct).toString()` 자동 wrapping | LLVM_BACKEND_GAP_PLAN.md Phase F | 이미 닫힘 (`source_println_struct_to_string` parity fixture 가 회귀 가드) |

본 plan 이 직접 다루지 않음. 다만 PR chain 의 각 PR 이 이 wall 들 중 어디에 의존하는지 명시.

### 5.2 새 walls (본 plan 이 도입)

| # | Wall | 형태 | 추정 LOC | 추정 난이도 |
|---|---|---|---|---|
| N1 | `cmd/osty-native-checker/main.osty` 신규 — Osty entry point | `fn main()` + stdin 한번에 읽기 + stdout 쓰기 | ~80 | 작음. 단 stdin all-read helper 필요 |
| N2 | `toolchain/api.osty` (또는 `cmd/.../api.osty`) — wire shape Osty struct | ~10 struct, 평탄 필드, no method | ~250 | 작음 |
| N3 | `cmd/.../json_serde.osty` — `parseCheckRequest` / `stringifyCheckResult` | std.json.parseValue 위에 manual field walk | ~500 | 중간. 보일러플레이트 양 |
| N4 | `toolchain/check.osty` 의 entry point export 정리 — `checkPackage(input: PackageInput) -> CheckResult` Osty 직접 호출 가능 | 현재는 Go 측 `selfhost.CheckPackageStructured` 가 한 번 wrap. Osty 측에 같은 함수가 있는지 확인 후 export | 0 (있다면) ~100 (없다면 추가) | 모름 — 확인 필요 |
| N5 | stdin / stdout helper — `std.io.stdin().readAll()` 또는 동등물 | osty_rt_io_read_line 만 있음. read-all helper 추가 (`osty_rt_io_read_all`) | C ~20 + Osty wrapper ~20 | 작음 |
| N6 | LLVM build 가 `toolchain/` 모듈 + `cmd/.../main.osty` 동시 compile + link | 현재 `osty build` 가 multi-package workspace 처리하는지 확인. 단일 binary 생성 가능 여부 | 0 ~ ??? | 모름 — 확인 필요 |
| N7 | `CheckResult` JSON field ordering 결정성 | Go encoding/json 은 struct field 선언 순서 = JSON 출력 순서. Osty 측 stringifyObject 도 같은 순서로 emit 해야 byte 동일 | ~50 (정렬 코드) | 작음 |
| N8 | `adaptCheckResultWithTokenLayout` Osty 이식 — token.Pos → byte offset 변환 | Go side `package_adapter.go` 의 helper. Osty 측 entry 가 같은 layout 변환 수행해야 wire shape parity | ~100 | 작음~중간 (spike Q3 발견) |

### 5.3 잠재 walls (탐색 필요)

수집한 데이터로는 알 수 없는 항목. 첫 PR 진행 중 발견될 가능성:

- (P1) `std.json.parseValue` 자체가 toolchain self-host 시 stage0 surface 안에 머무는가? (parseValue 내부에 generic / closure / match payload-bearing 이 있다면 P24 같은 unlock 필요)
- (P2) `osty build` CLI 가 단일 binary multi-file 빌드 모드를 지원하는가? 또는 패키지 단위만? 패키지 단위만이라면 `cmd/osty-native-checker/` 디렉토리 안에 모든 `.osty` 를 두는 모델.
- (P3) `osty_rt_*` symbol naming + ABI 의 결정성 — 같은 Osty 소스가 같은 LLVM IR / 같은 `.o` 를 만드는가? 이는 byte-equal 출력의 직접 조건은 아니지만 (출력 비교는 stdout JSON), 디버깅 노출 시 영향.
- (P4) `CheckResult.TypedNodes` 의 정렬 — `internal/selfhost/api/types.go::CheckResult.EnsureStableIDs` 가 stable ID 부여. Osty 측 entry 도 같은 sorting 호출 필요.
- (P5) Diagnostic 의 `Spans` 안의 `token.Pos` (Go `int`) vs Osty 측 표현 — wire 에 그대로 노출되면 두 binary 모두 같은 lexer 인덱스 산출 필요. 둘 다 같은 toolchain/lexer.osty 를 doors 한다면 자동 일치.

각 잠재 wall 은 발견 시점에 본 plan §11 의 "Open questions" 에 기록.

## 6. PR chain 제안

### PR 0 — Plan 의 합의 (이 PR)

- `docs/llvm-selfhost-plan.md` 추가 (이 문서).
- CLAUDE.md / SELFHOST_PORT_MATRIX.md / stage0_p24_scope.md 의 reference 1줄씩 추가.
- 사용자/팀 review 후 합의.

### PR 1 — Walking skeleton: entry point + dummy JSON echo

"Walking skeleton" = end-to-end 경로가 동작하지만 각 단계는 stub. 즉 build/run/I-O 파이프라인은 완성, 의미 있는 응답은 추후 PR 이 채움.

목표: LLVM 빌드된 dummy checker binary 가 stdin JSON 받아 stdout 으로 stub response 를 돌려준다. behavior parity 는 아직 없음 (stub 응답이라 Go-built 와 다름).

산출물:
- `cmd/osty-native-checker/main.osty` — `fn main()` + stdin readAll + parseCheckRequest stub + dummy CheckResult build + stdout write.
- `toolchain/api_check.osty` — `CheckRequest` / `CheckResult` / `CheckedNode` 등 wire-shape Osty struct (10개 정도, 평탄).
- `internal/stdlib/modules/io.osty` 에 `stdin().readAll() -> Result<String, Error>` 추가 + C runtime 에 `osty_rt_io_read_all` 추가 (W: N5).
- `osty build cmd/osty-native-checker/ --backend llvm -o /tmp/llvm-checker-stub` 가 exit 0.
- 자체 smoke test: `echo '{"source":"fn main(){}"}' | /tmp/llvm-checker-stub` 가 `{"diagnostics":[],...}` 같은 stub JSON 출력 + exit 0.

검증: PR 안의 단일 smoke test. behavior parity 는 PR2 이후.

차단되면: N5 (stdin readAll helper), N6 (multi-file binary build), 또는 stage0 의 미지 wall (`std.json.parseValue` self-host 시도 시 새로 발견). 차단 발견 시 본 PR 에서 stub helper / Go 측 patch 로 우회 + open question 로 기록.

### PR 2 — JSON manual serde: parseCheckRequest + stringifyCheckResult

목표: stub 대신 진짜 `parseValue` 기반 wire decode/encode. 단 `checkPackage` 호출은 아직 stub (always empty CheckResult).

산출물:
- `cmd/osty-native-checker/json_serde.osty` — `parseCheckRequest(text: String) -> Result<CheckRequest, Error>` + `stringifyCheckResult(r: CheckResult) -> String`.
- 모든 N7 field ordering 결정성 확보 (Go side `CheckResult` 의 JSON tag 순서를 Osty stringify 가 그대로 따른다).
- L1 fixture 중 가장 작은 1개 (예: `01_empty_source.json`) 에 대해 byte parity 확인.

검증:
- `testdata/selfhost_parity/01_empty_source.{osty,request.json,golden.json}` 추가.
- `just parity` 가 L1 단일 케이스 통과.

차단되면: N2/N3 의 보일러플레이트 양에서 발견되는 typo / Json type method 부족 등.

### PR 3 — 진짜 checker 호출: `checkPackage` Osty 직접 호출

목표: stub 대신 `toolchain/check.osty` 의 `checkPackage` 직접 호출. L1 fixture 5–10 개에 대해 byte parity 통과.

산출물:
- `cmd/osty-native-checker/main.osty` 에서 `checkPackage(input)` 호출 후 결과를 `CheckResult` wire shape 로 lift.
- N4 의 export 정리 — `toolchain/check.osty` 에 public entry function 이 있는지 확인 후 부족하면 추가.
- L1 fixture 10개 전체 통과.
- `TestLLVMCheckerBehaviorParity` 가 PR-CI mandatory 로 등록 (L1 만).

검증:
- L1 byte parity 10/10.
- `just parity` 가 PR CI 에서 자동 실행.

차단되면: W1 (stage0 coverage gap) 의 함수가 `checkPackage` transitive closure 에 포함된 경우. 본 PR 은 stage0 trajectory 의 다음 phase (P25, P26, ...) 가 그 함수를 cover 할 때까지 대기.

### PR 4–N — Stage0 coverage gap 깎기 (필요 시)

PR3 에서 발견된 stage0 wall 들이 하나씩 깎인다. 각 PR 은 [stage0_p24_scope.md](stage0_p24_scope.md) 패턴을 따른다:

- 타겟 함수 1–3개 선정.
- 새 unlock 명시 (예: "multi for-in sequential composition", "string-equality chain in if-cond").
- `internal/backend/stage0/emit.go` 에 unlock 추가.
- `OSTY_STAGE0_AUDIT=1` 실행 시 cover 비율 상승 확인.
- 누적 install-self decline 수 감소 측정.

본 plan 은 stage0 진척에 의존하지만 stage0 trajectory 자체를 driving 하지 않음 — 별도 owner 가 P24 → P25 → ... 진행.

### PR M — L2 corpus 흡수

목표: L2 (testdata/spec/positive/) 전체에 대해 byte parity. 한 번에 100+ fixture 흡수.

산출물:
- `scripts/build_parity_goldens.sh` — Go-built checker 로 모든 spec positive 파일에 대해 golden JSON 생성.
- `testdata/selfhost_parity/spec/` 디렉토리 — golden corpus 자동 생성.
- `just parity` (또는 `just parity-l2`) 가 nightly 로 등록.

PR4–N 의 진척 + L2 corpus 추가로 발견되는 잔여 wall 깎기.

### PR M+1 — L3 self-input

목표: `toolchain/*.osty` 자체를 input 으로 byte parity. 가장 의미 있는 self-host 증명.

산출물:
- `scripts/build_toolchain_self_golden.sh` — `toolchain/{check,elab,resolve,...}.osty` 패키지를 input 으로 Go-built checker 의 출력을 golden 저장.
- `just parity-l3` nightly.
- L3 통과 시 본 plan completion 선언.

### PR Final — Documentation 동기

- README 의 status 표 갱신 — "osty-native-checker self-build" 행 ✅ 으로.
- SELFHOST_PORT_MATRIX.md 에 본 plan 의 결과 cross-link.
- CHANGELOG_v0.5.md 의 "Shipped" 표에 entry.
- 본 doc 의 상태 "draft" → "shipped".

## 7. Fixture format

```
testdata/selfhost_parity/
├── 01_empty_source.request.json     # CheckRequest payload
├── 01_empty_source.golden.json      # Go-built CheckResult (golden)
├── 01_empty_source.source.osty      # 원본 .osty (참조용, 직접 사용 안 함)
├── 02_struct_decl.request.json
├── 02_struct_decl.golden.json
├── 02_struct_decl.source.osty
├── ...
├── spec/                            # L2 자동 흡수
│   ├── 01_declarations/
│   │   ├── 01_let.request.json
│   │   ├── 01_let.golden.json
│   │   └── 01_let.source.osty
│   └── ...
└── toolchain/                       # L3 self-input
    ├── check.request.json           # toolchain/check.osty 가 input
    ├── check.golden.json
    └── (.source.osty 는 symlink 또는 reference)
```

### 7.1 Golden 재생성

```sh
just rebuild-parity-goldens        # 모든 *.request.json 에 대해 Go-built 으로 골든 갱신
```

Go-built 동작이 바뀌면 golden 도 같이 바뀐다. 의도된 동작 변경일 경우 같은 PR 에서 golden 갱신 + LLVM-built 가 새 golden 에 다시 맞춰진다.

### 7.2 byte-equal 비교

JSON 출력은 다음 결정성 조건을 만족해야 byte-equal 비교 가능:

- struct field 순서 = Go side struct field 선언 순서.
- map / dict 출력 시 key 정렬 (Go encoding/json 은 map 만 정렬, struct field 는 선언 순서).
- 숫자 출력 형식 = Go encoding/json 의 기본 (Float 의 경우 `%g` semantics 일치).
- whitespace 없음 (compact JSON).

본 plan PR2 의 stringifyCheckResult 가 위 조건을 일치시킨다. 일치 못 하는 케이스가 발견되면 wire shape 의 deterministic encoding 표준을 SPEC_GAPS 에 등록 후 처리.

## 8. SELFHOST_PORT_MATRIX 와의 관계

`SELFHOST_PORT_MATRIX.md` 는 **frontend porting matrix** — `internal/resolve` / `internal/check` 의 Go 로직이 Osty 로 옮겨졌는지 추적. 다음 axis 로 측정:

- `ported` / `partial` / `broken` / `go-only` / `n/a`

본 plan 의 axis 는 다른 것:

- **stage0 coverage** (toolchain 함수 중 stage0 emit 가능한 비율).
- **behavior parity** (LLVM-built binary 가 Go-built 와 같은 동작).

두 매트릭스가 충돌하지 않는다. SELFHOST_PORT_MATRIX 의 `ported` 행이 모두 통과해야 toolchain/*.osty 가 자기 자신을 체크할 수 있고, 그게 본 plan 의 입력. 한편 본 plan 의 L3 통과는 SELFHOST_PORT_MATRIX 의 Phase 1c.5 (Go resolver 4파일 삭제) 의 마지막 정당화 증거 — "Osty 가 Go 없이 자기 자신을 체크한다" 가 됨.

따라서 두 doc 은 평행하게 진행되며 본 plan 의 L3 PR 에서 SELFHOST_PORT_MATRIX 에 cross-link 추가.

## 9. Stage0 trajectory 와의 관계

§3.2 에서 sibling angle 로 다뤘다. 핵심만 한 줄로 재차: 본 plan 은 stage0 trajectory 의 **consumer**, stage0 가 본 plan 의 **선행 의존**. stage0 측의 P24/P25/... PR 들은 본 plan 이 부르지 않으며 별도 owner.

## 10. 위험 + 미지수

### 10.1 큰 위험

| 위험 | 발현 시점 | 완화 / 현재 상태 |
|---|---|---|
| ~~**R1: `osty build` 가 single-binary multi-package 빌드 불가**~~ | ~~PR1 시도 시~~ | **종결 (spike Q2)** — `osty.toml` + `[bin]` 모델 (`toolchain/osty.toml` 그대로). PR1 에서 `cmd/osty-native-checker/osty.toml` 추가 |
| **R2: stage0 coverage 가 본 plan PR3 시점에 충분히 안 올라옴** | PR3+ | **위험 크게 축소** — spike 측정 결과 99.8% (5일만에 +5.6%p). PR3 시점에 100% 가능성 매우 높음. PR 4–N 의 stage0 gap PR 들이 사실상 불필요해질 가능성 |
| **R3: byte-equal 출력이 불가능한 비결정성 (예: map iteration order, hash seed)** | PR2/PR3 | wire shape 의 결정적 encoding 을 SPEC_GAPS 에 등록 후 Osty/Go 양쪽 stringify 정렬 |
| ~~**R4: `std.json.parseValue` 가 self-host 시 stage0 surface 밖**~~ | ~~PR1/PR2~~ | **종결 (spike Q1)** — stage0 99.8%, std.json 12 함수 전부 cover |
| **R5: `toolchain/check.osty` 의 public entry function 부재 / 시그니처 불일치** | PR3 | **부분 종결 (spike Q3)** — 단일 entry 없지만 5단계 시퀀스 명확. N8 (token-layout adapter Osty 이식, ~100 LOC) 작업 항목 추가 |

### 10.2 측정 결과 (2026-05-16 spike — [llvm-selfhost-plan-spike-findings.md](llvm-selfhost-plan-spike-findings.md))

| # | 질문 | 답 |
|---|---|---|
| Q1 | `std.json.parseValue` stage0 coverage? | **YES, stage0 99.8%, std.json 12함수 전부 cover** |
| Q2 | `osty build` single-binary multi-package? | **YES, `osty.toml` + `[bin] name=... path=main.osty` 모델 (`toolchain/osty.toml` 그대로)** |
| Q3 | `checkPackage` Osty signature mapping? | **단일 entry 없음. 5단계 Osty 시퀀스 + Go token-layout adapter (N8 작업으로 Osty 이식)** |
| Q4 | stdin EOF — single request vs persistent loop? | **single request, readAll 패턴** |
| Q5 | stderr 사용 정책? | **error → stderr 한 줄 + exit 1; behavior parity 는 stdout + exit code 만 비교** |

후속 미지수 (PR1 시점에 발견):
- (Q6) `cmd/osty-native-checker/` 디렉토리에 `main.go` 와 `main.osty` 공존 시 `osty build` 가 `.go` 무시하는가?
- (Q7) install-self 측정 시 99.8% audit cover 가 install-self 의 몇 %로 환산되는가? (monomorph 격차)

## 11. 다음 단계

이 plan 이 합의되면:

1. **PR 0 — 본 plan 합의**: 본 doc + CLAUDE.md / SELFHOST_PORT_MATRIX.md 의 cross-link 추가. (작은 PR)
2. **Spike — Q1~Q5 측정 PR (선택)**: 본격 PR 시작 전 alone-day 측정. 결과를 본 doc §3.3 / §10.2 에 반영.
3. **PR 1 — Walking skeleton**: §6 의 PR1 스펙대로 시작.

이후 PR chain 은 §6 표를 따른다.

## 12. 성공 / 실패 선언 기준

### 성공 — 본 plan completion

다음 모두 통과:
- `osty build --backend llvm cmd/osty-native-checker/ -o $LLVM_CHECKER` exit 0 + binary 동작.
- L1 (10 fixture), L2 (testdata/spec/positive 전체), L3 (toolchain self-input) 의 byte parity 모두 100%.
- `TestLLVMCheckerBehaviorParity` CI gate green.
- `verify-self-rebuild` 가 LLVM-built checker 로도 통과 (stage2/stage3 byte parity 유지).
- README + SELFHOST_PORT_MATRIX + CHANGELOG_v0.5 의 상태 표 갱신.

### 부분 성공 — interim landing 가능 milestone

L1/L2/L3 = corpus level (§4.2). M1–M4 = 본 plan 의 PR 머지 milestone:

- **M1** ✓ (PR1a [#1816](https://github.com/choiceoh/osty/pull/1816) 머지, 2026-05-16): walking skeleton — stub but builds + runs (`OSTY_STAGE0_FALLBACK=1` 경유)
- **M2 부분 진척** (PR1c [#1826](https://github.com/choiceoh/osty/pull/1826) + PR2 [#1829](https://github.com/choiceoh/osty/pull/1829) 이후 확장, 2026-05-16~): empty source fixture byte parity; stdin 은 full-buffer (`readAllStdin`) + manual JSON `source` / `package` 추출 및 표준 escape 디코드 ([#1961](https://github.com/choiceoh/osty/pull/1961) 등). `package` 모드 multi-file + `imports` surface 는 Go `package_adapter` 와 동등하게 wired ([#1966](https://github.com/choiceoh/osty/pull/1966), [#1973](https://github.com/choiceoh/osty/pull/1973)). 대규모 multi-fixture **L3 parity** 같은 다음 gate 는 여전히 PR3 wall + `SPEC_GAPS::cross-pkg-module-resolution` + §4.2 코퍼스 추적
- **M3** (PR M 머지): **L2** byte parity 100%. PR3-F 후
- **M4** (PR M+1 머지): **L3** byte parity 100%. ← 본 plan completion

**2026-05-16 세션 진척**: 15 PR 머지 ([#1812](https://github.com/choiceoh/osty/pull/1812) ~ [#1835](https://github.com/choiceoh/osty/pull/1835)) — plan + 2 spike + walking skeleton + 진짜 stdin/JSON 처리 (옵션 1: `qualifiedSymbol` rewrite + 옵션 3': naive parser) + PR3 의 cross-package wall 분석 + spec 명확화 + decline 진단. PR3-C-impl (SelfSymbol.importPath + module-scoped lookup, ~150 LOC) 가 M2 완전 진입 + PR3-F 까지의 선행. 자세한 분기 결정: `docs/llvm-selfhost-plan-pr*.md` 시리즈.

### 실패 — 본 plan 폐기 조건

- R1 (single-binary build 불가) 가 우회 불가능한 architectural 차단 → plan retire + 새 plan.
- stage0 trajectory 가 P24 부근에서 영원히 정체 (수개월) → 본 plan 도 같이 hold + alternative architecture 검토.
- byte-equal 출력이 본질적으로 비결정적이라 판명 → 검증 기준을 semantic-parity (구조 비교) 로 약화하는 새 plan PR 합의 필요.

---

## 부록 A. 사용자 결정 사항 (이 plan 의 근거)

본 plan 은 다음 결정 위에 구성됨 (2026-05-16 인터뷰):

| 분기 | 결정 | 거절된 대안 |
|---|---|---|
| 셀프호스팅의 종착지 | **LLVM 백엔드로 toolchain/*.osty 자체 컴파일 (진짜 셀프호스팅)** | Go resolver 4파일 삭제 (Phase 1c.5) / 현재 first-walls 만 닫기 (Phase 3) |
| 자체 컴파일의 산출물 범위 | **osty-native-checker 만 LLVM 으로 자체 빌드 (최소)** | toolchain 전체 / Go shell 포함 완전 탈Go |
| 검증 기준 | **Behavior parity — 같은 input 에 같은 JSON output (바이트 동일)** | self-build smoke 만 / fixed-point byte equality (`B2 ≡ B3`) — 후자는 결정성 작업 추가 필요 |
| stdlib body injection | **OFF (현재 default) — stdlib 은 runtime symbol 호출** | ON — monomorph hang 선행 |
| 진행 방식 | **Spec/design 먼저, 실제 PR 은 다음 세션** | walking skeleton 으로 바로 시작 / walls-first batch |
| Fixture corpus | **점진: 시드 → spec/positive 흡수 → toolchain/ self-input** | testdata/spec/positive 만 / toolchain self 만 |
| JSON ser/de 방식 | **Manual parseValue + stringifyValue 조합** | `#[json(key)]` derive — LLVM 백엔드의 generic encode/parse 완성도 미지 |
| Doc 위치 | **`docs/llvm-selfhost-plan.md` (별도 angle)** | stage0 trajectory follow-up (`b2_2_plan.md`) / bootstrap_design 갱신 |

## 부록 B. 단일 명령 cheat sheet

`just parity*` / `just rebuild-parity-goldens` 는 **현재 justfile 에 없음** — 본 plan 의 해당 PR 에서 도입.

```sh
# 현재 stage0 coverage 측정 (이미 존재)
OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/

# self-host 부트스트랩 byte parity 검증 (이미 존재)
scripts/verify-self-rebuild

# 본 plan 이 도입하는 target (작성 시점에 부재):
just parity                  # PR3 도입 — L1 fixture byte parity
just parity-l2               # PR M 도입 — testdata/spec/positive 전체
just parity-l3               # PR M+1 도입 — toolchain/*.osty self-input
just rebuild-parity-goldens  # PR2 도입 — Go-built 으로 golden 갱신
```

## 부록 C. 본 doc 의 갱신 트리거

다음이 발생하면 본 doc 갱신:

- PR 1/2/3 의 산출물이 §6 의 명세와 다르게 머지된 경우 → 해당 PR 표 갱신.
- §10.2 의 open question 의 답이 나온 경우 → §3.3/§3.4/§5 갱신.
- stage0 trajectory 가 100% 달성 → §3.1 측정 갱신 + §9 의 의존 관계 갱신.
- L1/L2/L3 의 fixture 추가/삭제 → §4.2 카운트 갱신.
- 본 plan completion → §12 의 상태 표 + 본 doc 의 상태 "draft" → "shipped".
