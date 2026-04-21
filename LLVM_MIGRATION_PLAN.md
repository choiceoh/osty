# LLVM Migration Plan

> **Status (2026-04): Historical.** 공개 백엔드는 이미 LLVM 하나뿐이며,
> 기존 Go transpiler는 `internal/bootstrap/gen`으로 격리되어 `osty-bootstrap-gen`
> 개발자 바이너리를 통해 `internal/selfhost/generated.go` 재생성용으로만 남아 있다.
> 이 문서는 이주 과정의 기록이며, 아래 "현재 상태" 및 phase 설명은 당시 시점의
> 기준이다.

이 문서는 Osty의 실행 백엔드를 현재 Go transpiler 중심 구조에서 LLVM 기반
네이티브 백엔드로 옮기기 위한 이주 계획이다. 목표는 기존 Go 백엔드를 즉시
제거하는 것이 아니라, front-end 안정성을 유지한 채 LLVM 백엔드를 병렬로
도입하고 충분한 parity가 확보된 뒤 기본 경로를 전환하는 것이다.

## 현재 상태

- Front-end는 `lexer -> parser -> resolve -> check`까지 안정적으로 분리되어
  있고, diagnostics/source position 모델도 CLI와 테스트에 이미 공유된다.
- 실행 경로는 `internal/gen` Go transpiler가 담당한다. `osty gen`, `osty build`,
  `osty run`, `osty test`는 Go source를 만들고 `go build` 또는 `go run`으로
  이어진다.
- `internal/ir`는 backend-agnostic IR로 이미 존재하지만, 현재 Go transpiler는
  AST와 `resolve/check` 결과를 직접 소비한다.
- Runtime helper는 Go 출력물 안에 조건부로 주입되는 구조다. LLVM으로 가려면
  ABI가 고정된 별도 runtime surface가 필요하다.
- Manifest profile/target은 Go toolchain 플래그와 `GOOS/GOARCH/CGO_ENABLED`
  중심이다. LLVM target triple, linker, sysroot, runtime artifact 위치를
  표현할 수 있도록 확장해야 한다.
- Multi-file package, vendored dependency, workspace build의 실제 code emission은
  아직 제한이 있다. LLVM 전환 전에 package 단위 emit/link 모델을 분명히 해야
  한다.
- 현재 Phase 45까지의 LLVM backend 의미는 `toolchain/llvmgen.osty`
  쪽으로 옮겨지고 있다. 여기에는 smoke IR builder, skeleton renderer,
  toolchain command plan, executable parity corpus, go-only diagnostic, 그리고
  unsupported source-shape taxonomy가 포함된다. 성공 경로의 scalar instruction
  strings, temp/label naming, plain/escaped ASCII `String` `println` module
  constant/formatting shape, immutable/mutable local String value paths, and
  simple String function return/parameter paths, simple named struct type
  definitions, struct literals, field reads, struct function boundaries, and
  mutable struct locals, plus bare enum tag values across simple function and
  mutable-local paths와 그 다음 payload-free enum match-expression paths도 같은
  toolchain에서 생성한다. `Float`는 현재 double-subset로만 `Float`-smoke
  경로를 처리하며, `Float32`/`Float64` width/ABI 정책은 후속 단계로 미룬다.

## 셀프호스팅 critical path (2026-04 실사용 기반 재감사)

기존 phase 번호(0~73)는 feature-slice 중심으로 쌓아왔다. 실제
`toolchain/*.osty` 非-test 파일의 usage grep으로 재감사한 결과,
셀프호스팅까지의 blocker 순서는 다음과 같다.

### Tier A — 核 toolchain (`lexer → parser → check → ir → llvmgen`) 자가 컴파일 blocker

| 기능 | 非-test 사용 | 현재 상태 | 관련 phase |
|---|---|---|---|
| `List<T>` literal + 메서드 lowering | 974회 | 🟡 부분 구현 — legacy AST emitter가 list literal + `push` 및 일부 컬렉션 브리지를 낮춘다. 다만 whole-toolchain coverage는 아직 incomplete | TBD (Tier A-1) |
| `Map<K,V>` literal + 메서드 lowering | 심볼 테이블 등 다수 | 🟡 부분 구현 — legacy AST emitter가 map literal + `insert` / `remove`를 낮춘다. 더 넓은 toolchain surface는 아직 incomplete | TBD (Tier A-2) |
| `String` concat/slice/interpolation | 대부분 파일 | 🟡 부분 구현 — ASCII / runtime-backed concat / interpolation은 존재하지만 `Char`/`Byte` iteration (`String.chars` / `bytes`)과 더 넓은 string ABI가 blocker | TBD (Tier A-3) |
| Closure + env capture | AST visitor 전반 | 🟡 부분 구현 — MIR path에는 capturing-closure env emission + tests가 있다. legacy AST emitter의 first-class fn-value/capture surface는 아직 incomplete | TBD (Tier A-4) |
| Multi-file package emit + link | 37개 non-test 파일 | ❌ merged-probe가 주 검증 경로이며 package-level native self-compile/link는 아직 blocker | TBD (Tier A-5) |
| `std.strings` 함수 구현 | 20+ 파일에서 import | 🟡 부분 구현 — legacy llvmgen이 일부 함수를 runtime shim으로 우회하지만 pure-Osty body는 여전히 `Char` / `List<Char>` lowering에 막힘 | TBD (Tier A-6) |

### Tier B — pkgmgr (`semver`, `manifest`, `registry`, `solve`, `pkgmgr`) 자가 컴파일 blocker

| 기능 | 非-test 사용 | 현재 상태 | 관련 phase |
|---|---|---|---|
| `String` payload enum (`PreText(String)`) | `semver.osty:5` | 🚧 Phase 54-63 — fixture 추가됨, 드라이브 확인 중 | [§Phase 54-63](#phase-54-63-payload-enum-generalization-floatstring) |
| `Result<T, String>` ABI | semver_parse, manifest_validation | 🟡 부분 구현 — legacy AST emitter가 aggregate `Result<T, E> = {tag, ok, err}`를 갖고 있으나 pkgmgr 전체 shape는 아직 다 못 덮음 | 신규 phase 필요 |
| `?` 전파 (Result) | `semver_parse.osty` 8곳 | 🟡 부분 구현 — legacy AST path가 matching `Result<_, E>` 반환 함수에서 `?`를 낮춘다. broader selfhost coverage는 아직 미완 | 신규 phase 필요 |
| struct/enum 복합 payload (Ok(SemVersion)) | semver_parse, manifest_validation | ❌ single-field scalar/ptr만 | 신규 phase 필요 |

### 非-blocker (당초 Tier B 오판 항목)

| 기능 | 非-test 사용 | 이유 |
|---|---|---|
| `defer` | 0건 | 核/pkgmgr 모두 쓰지 않음. AST formatter/lexer test에만 문자열로 등장. 구현 우선순위 최저. |
| multi-field payload enum (`Node.BinOp(lhs, op, rhs)` 패턴) | 0건 | [core.osty](toolchain/core.osty:38), [ir.osty](toolchain/ir.osty:1)가 **arena + kind discriminator** 설계를 채택해 페이로드 enum 회피. `CoreNode` 단일 struct + flat `CoreKind` enum으로 모든 AST/IR 노드 표현. |
| 재귀 페이로드 enum | 0건 | 위와 같은 이유로 회피됨. arena index(`CoreIdx = Int`)로 recursion 구현. |

당초 CLAUDE.md 부록 A의 canonical 예시(Rust-like `Node.IntLit(val)` 페이로드 enum)를
실제 구현으로 착각한 plan은 이 섹션으로 교정한다. arena+kind 설계는
명시적 주석([core.osty 의 shape decisions](toolchain/core.osty))으로 보존된
의도적 선택이며, LLVM 백엔드가 지원해야 할 최소 enum 표현은
single-scalar-payload + bare tag이다.

### 관심 순서

1. Tier A-1 (List) + A-3 (String concat/slice) — 모든 frontend 경로가 의존.
2. Tier A-6 (`std.strings` 순수 구현) — 현재 native probe first-wall이 `Char`이므로 A-3와 사실상 한 묶음.
3. Tier A-2 (Map) + A-4 (Closure) — resolve/check/visitor가 의존.
4. Tier A-5 (Multi-file linking) — Tier A가 한 파일에서 되면 즉시 다음 관문.
5. Tier B — pkgmgr 경로. 核 셀프호스트와 독립적으로 진행 가능.
6. 非-blocker는 유저 코드 예시 호환성 위주로 여유 시 진행.

---

## 이주 원칙

1. Go 백엔드는 migration 기간 동안 안정 백엔드로 유지한다.
2. LLVM 백엔드는 AST가 아니라 `internal/ir`를 입력으로 삼는다.
3. LLVM backend 의미의 소유권은 Osty 소스에 둔다. 여기에는 IR lowering,
   artifact 단계 결정, toolchain command plan, backend diagnostics가 포함된다.
   Go 구현은 migration 기간의 bootstrap bridge/reference와 host file I/O/process
   실행 shim으로만 허용하고, 새 backend 의미는
   `toolchain/llvmgen.osty` 같은 Osty-authored backend core로 먼저
   포팅한다.
4. Runtime ABI를 먼저 문서화하고, helper를 backend별로 흩뿌리지 않는다.
5. CLI는 `--backend=go|llvm` 또는 동등한 선택지를 제공하고, 기본값 전환은
   parity gate를 통과한 뒤 별도 릴리스에서 한다.
6. 각 단계는 기존 테스트를 깨지 않는 additive change로 들어간다.

## 목표 범위

LLVM 백엔드는 다음 결과물을 지원해야 한다.

- `osty build --backend=llvm`로 실행 파일 생성
- `osty run --backend=llvm`로 host target 실행
- `osty test --backend=llvm`로 테스트 harness 실행
- `osty gen --backend=llvm --emit=llvm-ir` 또는 별도 명령으로 inspectable `.ll`
  출력
- profile/target/feature에 따른 artifact 디렉터리 분리
- Go 백엔드와 같은 source mapping 품질을 갖춘 build failure 리포트

초기 non-goal은 다음과 같다.

- JIT 실행
- LLVM optimizer pass 튜닝 자동화
- 모든 stdlib/concurrency parity를 1차 milestone에서 달성
- Go runtime과 ABI 호환
- LLVM C API 또는 MLIR 기반 재작성

## 단계별 계획

### Phase 0. 기준선 고정

목표: 마이그레이션 중 회귀를 판별할 수 있는 현재 동작 기준을 만든다.
현재 측정된 25-step Phase 1 기준선은 [`LLVM_PHASE1_BASELINE.md`](./LLVM_PHASE1_BASELINE.md)에
기록한다. Backend parity fixture 분류와 초기 LLVM smoke set은
[`LLVM_BACKEND_CORPUS.md`](./LLVM_BACKEND_CORPUS.md)에 기록한다. Go transpiler
TODO/unsupported marker 감사와 LLVM 1차 제외 목록은
[`LLVM_GEN_TODO_AUDIT.md`](./LLVM_GEN_TODO_AUDIT.md)에 기록한다. Backend-aware
artifact/cache layout 정책은 [`LLVM_ARTIFACT_LAYOUT.md`](./LLVM_ARTIFACT_LAYOUT.md)에
기록한다.

작업:

- Go 백엔드 parity corpus를 정리한다.
  - `examples/calc`, `examples/concurrency`, `examples/ffi`, `examples/workspace`,
    `examples/stdlib-tour`, `word_freq.osty` 중 backend가 실제 실행해야 하는
    범위를 표시한다.
  - 실행이 아직 불가능한 fixture는 front-end-only, Go-only, future-runtime으로
    라벨링한다.
- `internal/gen` TODO marker 목록을 audit하고 LLVM의 1차 parity 범위에서
  제외할 항목을 명시한다.
- `osty pipeline --gen` 결과와 현재 `go build/go run` 실패 리포트 형식을
  snapshot으로 묶는다.
- release/debug/profile/test profile별 기대 artifact layout을 문서화한다.

완료 조건:

- 현재 short-suite 상태가 통과 또는 기존 실패 목록으로 명시되어 있다.
- backend parity fixture 목록이 문서화되어 있다.
- LLVM 1차 milestone에서 반드시 통과해야 할 smoke programs가 정해져 있다.

### Phase 1. Backend 인터페이스와 CLI 선택지

목표: Go backend와 LLVM backend를 CLI에서 같은 추상화로 호출할 수 있게 한다.

작업:

- `internal/backend` 패키지를 추가한다.
  - 입력: package/workspace front-end 결과, selected profile, target, features.
  - 출력: generated artifacts, executable path, diagnostics/warnings, source map.
- 기존 Go 경로를 backend interface 뒤로 얇게 감싼다.
- CLI에 backend 선택지를 추가한다.
  - 예: `--backend=go|llvm`
  - 예: `--emit=go|llvm-ir|object|binary`
- build/run/test의 artifact layout을 backend별로 분리한다.
  - 예: `.osty/out/<profile>[-<target>]/go/main.go`
  - 예: `.osty/out/<profile>[-<target>]/llvm/main.ll`
- Go 실패 리포트에 묶여 있던 debug mapping 로직을 backend-neutral 형태로
  분리한다.

완료 조건:

- `--backend=go`가 기존 동작과 동일하다.
- `--backend=llvm`는 아직 unsupported여도 CLI, profile, artifact path plumbing이
  테스트된다.
- build/run/test integration test가 backend 선택지 파싱을 검증한다.

### Phase 2. IR를 실제 backend 계약으로 승격

목표: LLVM backend가 `internal/ir`만 보고 codegen할 수 있도록 gap을 닫는다.

작업:

- `internal/ir`에 backend가 필요한 metadata를 보강한다.
  - package/member identity
  - qualified symbol name
  - generic instantiation records
  - callable default/keyword lowering 결과
  - source map anchor
  - capture layout
  - interface/vtable 필요 정보
- `check.Result.Instantiations`를 IR lowering에 반영한다.
- pattern destructuring, for destructuring, qualified type/call, stdlib intrinsic을
  backend-friendly node로 정규화한다.
- `ir.Validate`를 backend preflight gate로 강화한다.
- IR golden tests를 추가해 AST 변화와 backend 계약 변화를 분리한다.

완료 조건:

- smoke corpus가 `lexer -> parser -> resolve -> check -> ir.Validate`를 통과한다.
- LLVM backend prototype이 AST/resolve/check 패키지에 직접 의존하지 않는다.
- IR printer snapshot으로 주요 언어 construct가 고정된다.

### Phase 3. Minimal LLVM IR backend

목표: 작은 Osty 프로그램을 `.ll`로 emit하고 host에서 실행 가능한 바이너리로
링크한다.

초기 지원 범위:

- scalar primitive: `Int`, fixed-width int, `Float`, `Bool`, `Char`, `Byte`
- local `let`, assignment, return
- function declaration/call
- block, if/else, while-style/infinite/range for
- arithmetic/comparison/logical operators
- simple string literal 출력은 runtime call 또는 임시 host libc bridge로 제한
- script-mode synthetic `main`

작업:

- Osty-authored LLVM emitter core를 추가한다.
  - 1차 위치: `toolchain/llvmgen.osty`
  - Go `internal/llvmgen`은 CLI를 움직이는 bootstrap bridge/reference로 격하한다.
- textual `.ll` emitter를 Osty로 작성한다.
  - deterministic name mangling
  - explicit basic block builder
  - SSA temp allocator
  - type lowering table
  - source comment 또는 metadata anchor
- Go bridge가 Osty-authored emitter를 호출하거나, self-hosting bridge가 닫히기
  전까지 동일 golden을 비교해 drift를 막는다.
- host compile driver를 추가한다.
  - `.ll -> .o -> binary`
  - 초기는 external `clang`/`llc`를 사용하고, tool discovery/error를 친절하게
    보고한다.
  - Phase 18 기준으로 `clang` driver가 연결되어 supported smoke program은
    object/binary emit과 host `run --backend=llvm`까지 가능하다.
- `osty gen --backend=llvm --emit=llvm-ir`로 `.ll` 출력이 가능하게 한다.
- Osty-authored IR golden tests와 small executable tests를 만든다.
  - Phase 19 기준으로 executable smoke case list와 expected stdout도
    `toolchain/llvmgen.osty`가 소유하고, Go test는 host execution
    shim으로만 동작한다.
  - Phase 20 기준으로 unsupported/backend-capability diagnostic policy도
    `toolchain/llvmgen.osty`가 소유하고, Go는 source feature 감지와
    host artifact I/O만 담당한다.

완료 조건:

- `fn main() { println(1 + 2) }` 수준의 프로그램이 LLVM backend로 실행된다.
- scalar/control-flow/boolean/plain-string fixture가 Go backend와 같은
  stdout/exit code를 낸다.
- 새 LLVM lowering 로직은 Go가 아니라 Osty 소스에 먼저 존재한다.
- Go bridge가 Osty-authored emitter에서 생성된 renderers를 호출한다.
- production bridge와 Osty-authored emitter가 byte-for-byte drift guard로 묶여
  있다.
- LLVM toolchain이 없을 때 test는 명확히 skip되거나 actionable error를 낸다.

### Phase 4. Runtime ABI와 heap values

목표: Osty 값 모델을 LLVM에서 안정적으로 표현한다.

작업:

- Runtime ABI 문서를 추가한다.
  - value ownership
  - allocation/free 정책
  - string/bytes layout
  - list/map/set layout
  - option/result representation
  - struct/enum layout
  - closure environment layout
  - interface/vtable representation
  - panic/error/reporting ABI
- `runtime/` 또는 `internal/runtime` 하위에 C/Rust/Zig 중 하나로 구현할
  portable runtime을 둔다. 1차 권장안은 C ABI를 노출하는 small C runtime이다.
- Go backend에 흩어진 helper 의미를 runtime ABI로 옮긴다.
  - string formatting/interpolation
  - structural equality
  - range/list/map helpers
  - Option/Result helpers
  - error/toString bridge
- LLVM backend가 runtime symbols를 declare/call하도록 한다.
- Runtime object/library를 build artifact에 포함하고 linker step에 연결한다.

완료 조건:

- struct/enum/match/list/string/Option/Result smoke tests가 LLVM backend에서 돈다.
- runtime ABI가 문서와 테스트로 고정된다.
- Go backend helper와 LLVM runtime 사이의 semantic drift를 잡는 shared tests가
  있다.

### Phase 5. Package, workspace, generics, FFI

목표: 단일 entry file을 넘어 package 단위 native build를 지원한다.

작업:

- package-per-package emission 모델을 확정한다.
  - one module per package 또는 one linked module per binary 중 선택
  - exported symbol mangling
  - duplicate runtime/helper emission 제거
- generic monomorphization 결과를 LLVM symbol로 materialize한다.
  - Phase 1(generic free function)은 이미 구현되어 있다:
    `toolchain/monomorph.osty`의 Itanium-호환 mangling 정책 +
    `internal/ir.Monomorphize`가 `backend.PrepareEntry`에서 호출되어
    LLVM 진입 시점에는 generic free fn이 concrete specialization으로
    이미 lowered 되어 있다 (smoke: `TestGenerateModuleGenericIdentityMonomorphized`).
  - Phase 2(generic struct/enum 선언 + struct-level method)도 IR
    단계에서 구현되어 있다: `_ZTS…` 나미널-타입 mangling 트랙,
    `rewriteType`이 StructLit/VariantLit/LetStmt/Field/Variant Payload
    등 모든 NamedType slot을 재작성, dedupe 키는 fn 네임스페이스와
    분리 (`MonomorphTypeDedupeKey`). Struct LLVM smoke 통과:
    `TestGenerateModuleGenericStructPairMonomorphized`
    (mangled nominal 타입이 aggregate로 emit).
  - Phase 3(generic enum variant-call 경로)는 main의 독립 구현
    (`isUnresolvedType` + `inferVariantLiteralType` + `enumVariantFieldExpr`)이
    checker의 `*ErrType` 누락을 보완해 `Maybe.Some(42)` 같은 variant
    constructor가 `Maybe<Int>`로 풀리도록 한다. Payload-free
    (`Maybe.None`)은 let/scrutinee context에서 type을 전파한다.
  - Phase 4(struct/enum method-local generic parameter)도 IR 단계에서
    구현되어 있다: `_Z<templateArgs>E` 접미사 mangling 트랙, fn/type/method
    worklist 세 개를 인터리브 drain, emit 시점에 receiverEnv ⊕ methodEnv를
    머지. 원본 generic method는 Pass 6이 출력에서 제거한다. smoke:
    `TestGenerateModuleMethodLocalGenericGetMonomorphized`.
  - Phase 5(generic interface 선언)도 IR 단계에서 구현되어 있다:
    struct/enum과 동일한 `_ZTS` 나미널 트랙을 interface에도 확장,
    `requestInterfaceType` + `emitInterfaceSpecialization`로 type queue에
    편입. Interface-as-value vtable dispatch 경로는 별도 스코프.
  - Phase 6a(interface vtable scaffold)는 llvmgen 렌더러에서 구조만
    갖춘다: `%osty.iface = type { ptr, ptr }` fat-pointer 타입과
    structural match 기반으로 발견된 (impl, interface) 쌍마다
    `@osty.vtable.<impl>__<iface>` 상수 global을 emit
    (smoke: `TestGenerateModuleInterfaceVtableEmitted`).
  - Phase 6b(interface boxing + vtable dispatch) 추가: `llvmType`이
    interface를 `%osty.iface`로 반환, `emitLet`의 concrete→interface
    경로에서 boxing (alloca + insertvalue 2회), `emitInterfaceMethodCall`
    이 수신자가 `%osty.iface`인 call을 vtable extractvalue + getelementptr
    + indirect call로 낮춘다.
    smoke: `TestGenerateModuleInterfaceBoxingDispatch`.
  - Phase 6c(interface method의 non-self 인자): `methodDecl.Params`의
    user-facing 파라미터를 `llvmType`으로 개별 lower하고 `emitExpr`로
    argument value를 만들어 indirect call에 `ptr %data, <argTyp> %arg…`
    형태로 threading. smoke: `TestGenerateModuleInterfaceDispatchWithArgs`.
  - Phase 6d(let 외 context 자동 boxing): return statement / 블록의
    trailing-expression implicit return / 함수 호출 인자에서 concrete
    값이 interface-typed slot으로 들어가면 `boxInterfaceValue`가 자동
    호출되어 fat pointer를 생성한다. `generator`에 `returnSourceType`
    필드 추가로 AST 레벨 return 타입을 추적. smokes:
    `TestGenerateModuleInterfaceBoxingFromReturn`,
    `TestGenerateModuleInterfaceBoxingFromCallArg`.
  - Phase 6e(assign-statement 자동 boxing): `let mut s: Iface = concrete`
    로 bound된 slot에 concrete값을 `s = w`로 재할당하면 자동 boxing.
    `boxInterfaceValue`가 반환 value에 `sourceType: ifaceType`을 태그
    해 slot이 자신의 선언 interface identity를 기억한다. smoke:
    `TestGenerateModuleInterfaceBoxingFromAssign`.
  - Phase 6g(vtable calling-convention shim + binary E2E): interface
    dispatch ABI(`ptr %data + non-self args`)가 실제 method의 struct-
    by-value receiver convention과 달라 IR-level smoke는 통과해도
    binary 실행 시 self가 스택 쓰레기로 읽혔다. `renderInterfaceShim`
    이 (impl, iface, method) 쌍마다 `@osty.shim.<impl>__<iface>__<method>`
    를 emit해 ptr→struct load 후 실제 method를 호출하는 adapter를
    담당. vtable slot은 method 대신 이 shim을 가리킨다. mut receiver는
    포인터를 그대로 forward. `internal/backend/llvm_test.go`에 두 개의
    binary E2E smoke 추가: `TestLLVMBackendBinaryRunsGenericIdentity`
    (Phase 1 generic fn `id::<Int>(42)`가 `42\n` 출력) +
    `TestLLVMBackendBinaryRunsInterfaceBoxingDispatch` (Phase 6b boxing +
    vtable dispatch가 `3\n` 출력). clang을 미발견 시 skip.
  - Phase 6f(Phase 5 mangled interface 이름 ↔ Phase 6 vtable 통합):
    코드 변경 없이 이미 맞물려 있다. Phase 5가 generic interface를
    specialize할 때 결과 이름은 `_ZTSN…E` Itanium RTTI 포맷이고,
    Phase 6a의 `discoverInterfaceImplementations`는 이 이름을 그대로
    소비해 `@osty.vtable.<impl>__<mangled>` vtable global을 emit한다.
    boxing / dispatch 경로도 `interfaceInfo.name` 기준이라 추가 처리
    불필요. smoke: `TestGenerateModuleMangledInterfaceNameVtableDispatch`
    가 mangled 이름을 source identifier로 직접 써서 경로 전체를
    regression-lock (parser가 generic interface `interface Foo<T>` 선언
    문법을 아직 수용하지 않아 실제 Container<Int> → Container<Int>
    specialization E2E는 별도 parser/checker 확장 후).
  - 남은 범위: bare function-pointer turbofish (`let f = id::<Int>`),
    generic interface 선언 파서 지원 (`interface Foo<T>` 문법),
    암묵 타입 변환, payload-free / 비-리터럴 인자를 가진 variant call의
    타입 복원(궁극적으로는 checker 쪽 generic variant-constructor
    inference 보강).
- workspace/dependency graph를 codegen/link order로 변환한다.
- `use go` FFI는 LLVM backend에서 그대로 지원하기 어렵기 때문에 새 FFI 정책을
  정한다.
  - native backend 전용 `use c` 또는 external symbol declaration
  - Go FFI는 Go backend-only diagnostic으로 제한
  - manifest target별 linker flags/sysroot 추가
- CLI failure report가 object/link 단계의 에러를 Osty source로 최대한 연결하게
  한다.

완료 조건:

- multi-file package와 workspace example이 LLVM backend로 build된다.
- generic functions, generic structs, closures가 LLVM smoke corpus를 통과한다.
- FFI 정책이 diagnostic과 문서에 반영된다.

### Phase 6. Stdlib와 concurrency parity

목표: 사용자 관점에서 Go backend와 LLVM backend의 표준 라이브러리 의미를 맞춘다.

작업:

- stdlib module별 backend support matrix를 만든다.
  - pure Osty로 실행 가능한 모듈
  - runtime intrinsic이 필요한 모듈
  - OS/platform binding이 필요한 모듈
  - Go backend-only로 남길 모듈
- `std.io`, `std.fs`, `std.env`, `std.time`, `std.random`, `std.json`,
  `std.regex`, `std.net`, `std.thread` 순서로 porting한다.
- concurrency runtime을 설계한다.
  - task handle layout
  - task group cancellation
  - channel close/drain semantics
  - scheduler/threading primitive
- `osty test --backend=llvm` harness를 구현한다.

완료 조건:

- stdlib-tour의 지정 subset이 LLVM backend로 통과한다.
- concurrency example이 LLVM backend로 통과한다.
- backend support matrix가 문서와 CI에 반영된다.

### Phase 7. 기본 백엔드 전환

목표: LLVM을 기본 build backend로 전환할 수 있는 품질 기준을 충족한다.

작업:

- release note와 migration guide를 작성한다.
- backend selection default를 edition/profile/feature flag로 점진 전환한다.
  - 예: 현 edition은 Go default 유지, 다음 edition에서 LLVM default
  - 또는 `OSTY_BACKEND=llvm`/manifest setting으로 opt-in 기간 운영
- CI matrix에 host LLVM build를 추가한다.
- Go backend는 최소 한 릴리스 이상 fallback으로 유지한다.
- performance, binary size, compile time baseline을 기록한다.

완료 조건:

- 정해진 parity corpus가 LLVM backend로 모두 통과한다.
- 알려진 Go-only 기능이 명확한 diagnostic을 낸다.
- 새 프로젝트 scaffold가 LLVM default에서 정상 build/run/test된다.

## 타입/값 lowering 초안

| Osty | LLVM 표현 초안 | 비고 |
|---|---|---|
| `Int` | target word 또는 고정 `i64` 중 결정 필요 | 현재 Go `int` 의미와 cross-target 안정성 중 선택 |
| `Int8..UInt64` | `i8..i64` | signedness는 operation에서 표현 |
| `Bool` | `i1` in SSA, ABI는 `i8` 가능 | runtime ABI에서 고정 |
| `Char` | `i32` | Unicode scalar value |
| `Byte` | `i8` | `UInt8`과 alias 여부 명시 필요 |
| `Float/Float32/Float64` | `double/float/double` | `Float`는 현재 단계에서 `double` 하위집합으로 다루고, `Float32`/`Float64` 정책은 후속 단계 |
| `String` | `%osty.string` | pointer+len 또는 fat value |
| `Bytes` | `%osty.bytes` | pointer+len+cap 또는 runtime-owned buffer |
| `List<T>` | `%osty.list` + element descriptor | monomorphized typed list와 erased list 중 선택 |
| `Option<T>` | tagged payload | nullable pointer optimization은 후순위 |
| `Result<T,E>` | tagged payload | Go backend의 `Value/Error/IsOk`와 semantic parity |
| `struct` | named LLVM struct | Phase 30-33 smoke subset uses value aggregates; full ABI/layout stability remains runtime work |
| `enum` | `i64` tag for bare variants, `%Enum = { i64, i64 }` for single-`Int` payload subset, tagged payload storage later | Phase 34-37 smoke subset covers payload-free variants; Phase 42-45 adds conservative single-`Int` tagged payload; broader payload layouts are later |
| `match` | enum-tag switch/branch lowering | Phase 38-41 covers payload-free match expressions; Phase 42-45 adds `match` binding for single-`Int` payload enums |
| closure | fn pointer + env pointer | capture lifetime/ownership 필요 |
| interface | data pointer + vtable pointer | vtable generation은 Phase 5+ |

### Phase 38-41. Payload-free enum match expressions

목표: payload-free enum tag values가 `match` expression 경로를 타고 helper
return/parameter boundaries와 mutable local slots을 지나도 동일하게 동작하게
한다.

작업:

- `enum_match_print.osty`를 `main` 안의 payload-free enum match expression
  smoke fixture로 추가한다.
- `enum_match_return_print.osty`를 helper return 경계 smoke fixture로 추가한다.
- `enum_match_param_print.osty`를 helper parameter 경계 smoke fixture로 추가한다.
- `enum_match_mut_print.osty`를 mutable local slot smoke fixture로 추가한다.
- 네 fixture 모두 `42\n`를 출력하도록 유지하고, toolchain-authored backend core의
  match lowering을 기준으로 문서화한다.

완료 조건:

- payload-free enum match expression lowering이 current LLVM smoke corpus에
  반영되어야 한다.
- 각 smoke fixture의 expected stdout가 `42\n`로 문서화되어야 한다.
- match lowering은 여전히 `toolchain/llvmgen.osty`에서 소유되어야
  한다.

### Phase 42-45. Single-Int payload enum smoke subset

목표: 단일 `Int` payload를 가진 enum을 보수적인 ABI로 낮춰 `match`가
`Some(x)` 바인딩을 수행할 수 있게 한다.

제약: 현재 단계는 오직 `Some(Int)`와 `None` 형태의 태그+단일 `i64` payload
경로만 다룬다. `Some`은 `%Maybe = type { i64, i64 }`로 인코딩되고 인덱스 `0`이
태그, 인덱스 `1`이 payload이다.

작업:

- `enum_payload_print.osty`를 `main` 안에서 `Some(42)` 생성 후 `match`로 `Some(x)`
  바인딩하는 payload enum fixture로 추가한다.
- `enum_payload_return_print.osty`를 helper return 경계 payload enum fixture로
  추가한다.
- `enum_payload_param_print.osty`를 helper parameter 경계 payload enum fixture로
  추가한다.
- `enum_payload_mut_print.osty`를 mutable local 할당 후 `match` payload 바인딩을
  수행하는 payload enum fixture로 추가한다.
- 문서(`LLVM_BACKEND_CORPUS.md`)의 4개 fixture와 기대 출력 `42\n`를 연결한다.

완료 조건:

- `%Enum = type { i64, i64 }` 기반 단일 `Int` payload enum이 LLVM smoke corpus에
  반영되어야 한다.
- payload enum `match`에서 `Some(x)` 바인딩이 동작해야 한다.
- 반환/파라미터/mutable local 경계 fixture가 문서와 동일한 기대 동작을 가져야
  한다.

### Phase 46-53. Float smoke subset

목표: `Float`를 `double`로 다루는 8개 LLVM smoke를 추가해, printf 기본 `%f`
출력 형태로 `42.000000`을 내는 경로를 먼저 확인한다. `Float32`/`Float64`
정책은 후속 단계로 미룬다.

작업:

- `float_print.osty`, `float_arithmetic_print.osty`, `float_return_print.osty`,
  `float_param_print.osty`, `float_mut_print.osty`, `float_compare_print.osty`,
  `float_struct_print.osty`, `float_enum_payload_print.osty`를 추가한다.
- `LLVM_BACKEND_CORPUS.md`에 Phase 46~53 항목을 추가하고 기대 stdout을
  `42.000000\n`으로 문서화한다.

완료 조건:

- `Float` smoke fixture의 8개가 같은 산출물 체인으로 toolchain-authored LLVM 경로에
  포함되어야 한다.
- `Float32`/`Float64` width 정책은 별도 단계에서 결정한다고 migration 문서에
  명시되어야 한다.

### Phase 54-63. Payload enum generalization (`Float`/`String`)

목표: `Float` payload enum과 `String` payload enum을 같은 형태로 일반화해
반환/파라미터/mutable/local `match` 경계를 통일하고, 매칭 순서/와일드카드
동작까지 검증한다.

제약: 현재 단계는 `Some/None`처럼 예약어로 보존된 형태는 사용하지 않고,
`Full(Float)`/`Text(String)`으로 진행한다. qualified constructor/pattern은
자체점검 이슈로 이번 단계는 제외한다.

작업:

- `float_payload_return_print.osty`를 추가해 `Full(42.0)` 생성
  후 helper-return 경계와 `match` 추출을 검증한다.
- `float_payload_param_print.osty`를 추가해 float payload enum을 helper parameter로
  전달해 반환 경계와 `match` 추출을 검증한다.
- `float_payload_mut_print.osty`를 추가해 mutable local payload enum 슬롯을 쓰고
  읽은 뒤 `match` 바인딩을 검증한다.
- `float_payload_reversed_match_print.osty`를 추가해 `Empty -> ...`, `Full(x) -> ...`
  순서의 two-arm match를 검증한다.
- `float_payload_wildcard_print.osty`를 추가해 `Full(_)` 와일드카드 match arm을
  검증한다.
- `string_payload_return_print.osty`를 추가해 `Text("payload string")` 반환 후
  `Text(s)`/`Empty` 분기와 문자열 반환을 검증한다.
- `string_payload_param_print.osty`를 추가해 `String` payload enum parameter
  경계를 검증한다.
- `string_payload_mut_print.osty`를 추가해 mutable local `Text("payload string")`
  업데이트와 `Text(s)`/`Empty` 분기를 검증한다.
- `string_payload_reversed_match_print.osty`를 추가해 `Empty -> ...`, `Text(s) ->
  ...` 순서의 two-arm match를 검증한다.
- `string_payload_wildcard_print.osty`를 추가해 `Text(_)` 와일드카드 match arm을
  검증한다.
- `LLVM_BACKEND_CORPUS.md`에 Phase 54~63 항목을 추가하고 expected stdout과
  각 산출물 경계를 정리한다.

완료 조건:

- `Float`와 `String` payload enum가 반환/파라미터/mutable locals/매치 패턴 순서/와일드카드로
  공통되는 lowering 경로를 가져야 한다.
- qualified 생성자 형태는 migration 계획에 맞춰 `later phase`로 분리해 둔 채
  문서화되어야 한다.
- 기존 `Float`/`String` smoke fixture와 같은 방식으로 `Osty` 소유의 helper corpus
  및 drift-guard 경로에서 일관되게 컴파일되어야 한다.

### Phase 64-73. Value/control-flow smoke expansion

목표: 값 위치 `if`, `Bool` 경계, exclusive range loop, unary/modulo, 그리고
`String`/`Bool` struct field 읽기를 한 묶음으로 추가해, LLVM executable smoke의
저수준 control-flow와 value-position coverage를 넓힌다.

작업:

- `int_if_expr_print.osty`를 추가해 `Int` value-position `if` expression을
  검증한다.
- `string_if_expr_print.osty`를 추가해 `String` value-position `if` expression을
  검증한다.
- `float_if_expr_print.osty`를 추가해 `Float` value-position `if` expression을
  검증한다.
- `bool_param_return_print.osty`를 추가해 `Bool` parameter/return boundary를
  검증한다.
- `int_range_exclusive_print.osty`를 추가해 exclusive `Int` range loop를
  검증한다.
- `int_unary_print.osty`를 추가해 unary `Int` lowering을 검증한다.
- `int_modulo_print.osty`를 추가해 `Int` modulo lowering을 검증한다.
- `struct_string_field_print.osty`를 추가해 `String` field read from simple struct
  aggregate를 검증한다.
- `struct_bool_field_print.osty`를 추가해 `Bool` field read from simple struct
  aggregate를 검증한다.
- `bool_mut_print.osty`를 추가해 mutable `Bool` local slot과 assignment를
  검증한다.
- `LLVM_BACKEND_CORPUS.md`에 Phase 64~73 항목을 추가하고 expected stdout을
  정리한다.

완료 조건:

- value-position `if`, `Bool` boundary, exclusive range loop, unary/modulo,
  and struct field smoke가 같은 native backend 경로로 통과해야 한다.
- 문서에 적힌 expected stdout과 fixture 목록이 서로 일치해야 한다.
- 이 묶음은 qualified constructor/pattern 확장과는 별개로 유지된다.

### Phase 74. Result<T, E> — `?` 전파 + match 분해 (pkgmgr 스코프)

목표: pkgmgr 경로(semver/manifest 파서)가 쓰는 `Result<T, E>` 타입에 대해
`?` 조기-리턴과 `match { Ok(v) -> ..., Err(x) -> ... }` 두-armc 분해를
native backend에서 실행 가능하게 만든다. Tier B(pkgmgr 셀프호스트) critical
path의 첫 번째 블로커.

Result ABI는 이미 builtin으로 존재한다 — `%Result.<ok>.<err> = { i64 tag,
<ok> ok, <err> err }` 3필드 struct, `Ok(x)`/`Err(x)` 생성자는
`emitBuiltinResultConstructor`로 커버됨. 이 단계는 **소비 경로** (`?` 전파 +
match 분해)를 추가한다.

작업:

- `emitQuestionExpr`에 Result 분기 추가. `staticExprSourceType` →
  `builtinResultTypeFromAST` 매치 시 `emitQuestionExprResult`로 디스패치.
- `emitQuestionExprResult`는 enclosing fn이 `Result<_, E>`(동일 err 타입)를
  반환하는지 검증 → tag 추출 → 1(Err)이면 err 필드를 반환 Result struct로
  재포장하고 `releaseGCRoots` 후 `ret` → 0(Ok)이면 ok 필드 추출해 calling
  블록에 값을 넘긴다.
- `emitMatchExprValue`에 `g.resultTypes[scrutinee.typ]` 조회 분기 추가,
  `emitResultMatchExprValue`로 디스패치.
- `matchResultPattern` 헬퍼: `Ok(name)`/`Ok(_)`/`Err(name)`/`Err(_)`/`_`
  패턴을 tag + field index + 바인딩 정보로 환원.
- `emitResultMatchExprValue`는 정확히 2-arm 매치(Ok·Err 어느 순서든, 선택적
  wildcard 대체 arm)를 `llvmIfExprStart`/`llvmIfExprElse` 패턴으로 내려보낸다.
  바인딩이 있을 때 해당 field를 `extractvalue`로 꺼내 local로 묶는다.
- smoke fixture `result_question_int_print.osty` 추가 (Result<Int, String>
  체인 `?`와 match 분해). 예상 stdout `42\n`으로 corpus에 등록.

완료 조건:

- `result_question_int_print.osty`가 `osty build`를 통해 실행 바이너리로
  떨어지고, stdout이 `42`로 검증된다 (수동 검증 완료).
- `?`와 match의 두 경로(Ok·Err)에서 `releaseGCRoots` + `ret` 순서가
  기존 Optional ptr 경로와 동일하게 유지된다.
- `TestGenerateResultQuestionExprInt`, `TestGenerateResultMatchExprInt`가
  `internal/llvmgen/optional_test.go`에 추가되어 IR 형태 회귀를 봉쇄한다.
- `Result<T, String>`의 T가 **struct / enum with Int payload**인 경우도
  기존 `insertvalue`/`extractvalue`와 `llvmZeroValue`(`zeroinitializer`)
  fallback으로 자동 동작함을 수동 테스트 2건으로 확인했다
  (`Version`-구조체, `Tag`-enum with `Num(Int)`). 별도 코드 변경 불필요.

파생 작업 (이 phase의 사전조건 해결):

- `internal/mir/lower.go`의 method lowering panic (`paramLocals[0]` index
  out of range) 수정. IR `FnDecl`은 receiver를 `Params`에 포함하지 않고
  `ReceiverMut` 불리언과 소유자 타입만 보존하므로 MIR 쪽에서 자체적으로
  synthetic `self` 파라미터를 `owner` nominal 타입으로 선두에 주입해야
  한다 — `lowerFunction`에서 처리. 이 수정 후 `toolchain/core.osty` 단일
  파일 기준 pipeline이 MIR 단계를 통과하고 lint/gen 단계까지 진행된다
  (이전에는 MIR panic으로 즉시 중단).

후속:

- `toolchain/llvmgen.osty` 측 Osty-authored AST 에미터 포팅. 현재
  `emitQuestionExpr`/`emitMatchExprValue`의 orchestration 계층은 Go에만
  있고, Osty 측에는 lower-level `llvmExtractValue`/`llvmStructLiteral` 등
  primitive만 존재. 전체 AST walker 포팅은 migration plan의 큰 단계로,
  이 Phase 74의 단위는 아니다. 현재 Go 코드가 쓰는 primitive는 모두
  `support_snapshot.go`에 이미 스냅샷되어 있어 primitive drift 위험은
  낮다.
- `Result<T, String>` T가 재귀 또는 heap-owned 복합체일 때 GC root /
  write-barrier 요구사항은 추가 검토 필요. 현재 smoke 커버리지는 Int
  / Int-payload enum / 단순 struct에 한정.

## Toolchain 전략

초기 전략:

- LLVM IR은 텍스트로 emit한다.
- Build driver는 외부 `clang`/`llc`를 찾고, 없으면 actionable error를 낸다.
- CI에서는 LLVM toolchain 설치가 가능한 job만 LLVM executable tests를 켠다.
- 로컬 개발자는 `osty gen --backend=llvm --emit=llvm-ir`만으로도 backend를
  디버그할 수 있다.

후속 전략:

- 필요하면 `.bc` bitcode 출력과 optimizer pass pipeline을 추가한다.
- 안정성이 충분해지면 target별 linker/sysroot discovery를 manifest target에
  통합한다.
- C API/cgo binding은 incremental compile, object cache, debug metadata 품질이
  textual IR 방식의 한계에 닿았을 때만 재검토한다.

## Bootstrap-only 호스트 어댑터 (astbridge 포함)

**요약**: 현재 `toolchain/*.osty`의 4개 파일은 Go 호스트 바인딩을 통과하는 **bootstrap-only 어댑터**다. 이들은 LLVM 셀프-컴파일 경로 밖에 있고, whole-toolchain LLVM probe에서 자동으로 스킵된다. 이 구조 덕분에 sub-wall 히스토그램과 probe가 "셀프호스팅 시 진짜 남는 벽"만 보여준다.

### 4개 bootstrap-only 파일

| 파일 | FFI | 역할 | native 대체 경로 |
|---|---|---|---|
| `toolchain/ast_lower.osty` | `use runtime.golegacy.astbridge` | parser.osty AstArena → Go `*ast.File` 변환 | CLI가 Osty-native check/llvmgen을 호출하도록 재배선되면 dead code |
| `toolchain/ci.osty` | `use go "github.com/osty/osty/internal/cihost"` | CI 드라이버 (format/lint/snapshot/manifest 호스트 호출) | native runtime ABI의 fs/process primitive 정의 후 포팅 |
| `toolchain/docgen.osty` | `use go` | 문서 생성기 (파일 I/O + HTML emit) | 동상 |
| `toolchain/manifest_validation.osty` | `use go` | manifest/lockfile 검증 (파일 해시, 경로 검사) | 동상 |

### 분류 규칙

`internal/llvmgen/multifile_probe_test.go:isBootstrapOnlyOstyFile`가 다음 두 패턴 중 하나라도 포함한 파일을 자동 bootstrap-only로 분류한다:

- `use runtime.golegacy.*` — 명시적 legacy bootstrap 브릿지
- `use go "..."` — 임의 Go 패키지 호스트 바인딩 (CLAUDE.md "호스트 경계" 허용 카테고리)

두 패턴 모두 LLVM native lowering 없이는 LLVM binary로 self-compile 불가. 명시적 `// bootstrap-only` 주석 마커 대신 FFI 스탠자 존재를 시맨틱 시그널로 사용한다. 새 파일 추가 시 별도 작업 불필요 — `use go` / `use runtime.golegacy`를 쓰면 자동 분류됨.

### Probe 쌍

- `TestProbeWholeToolchainMerged` — 모든 파일 포함. 현재 첫 wall **LLVM002 `runtime.golegacy.astbridge`** (bootstrap artifact).
- `TestProbeNativeToolchainMerged` — bootstrap-only 파일 제외. 현재 첫 wall **LLVM011 `[list_mixed_ptr]`** (`[...]` 리터럴 안에 `String`과 non-`String` ptr-backed 원소가 섞이는 케이스). LLVM012 statement-form 카테고리는 toolchain 실제 shape에 대해 전부 해소: (1) LLVM011 Char 벽(`lspUtf16UnitsForChar`) → `Char`→i32 / `Byte`→i8 lowering + `Char.toInt()` / `Byte.toInt()` / `Int.toChar()` 폭 변환 + 부호 없는 비교. (2) LLVM012 `*ast.MatchExpr is not a call` 벽 → tag-enum scrutinee + bare-variant / wildcard arm 용 match-as-statement lowering (#413). (3) LLVM012 `field assignment base *ast.FieldExpr` 벽 → 중첩 필드 체인(`cx.env.returnTy = ...`, `a.b.c = x`)을 inside-out extractvalue + outside-in insertvalue 재조립으로 lowering (`internal/llvmgen/stmt.go:emitFieldAssign`, #418).

두 probe의 첫-wall 차이 = bootstrap 브릿지가 히스토그램에 주입하는 노이즈 양. migration 진전은 native probe의 first-wall 이동으로 측정한다.

### astbridge 제거 경로

astbridge는 한 방에 제거하지 않고 **CLI 재배선이 완료되면 자동 dead code**가 되는 구조다:

1. **현재 상태**: Go CLI (`cmd/osty`) → `internal/selfhost/parse.go:Parse` → `ast_lower.osty`가 `*ast.File` 생성 → Go `internal/check` / `internal/resolve` / `internal/llvmgen` 소비.
2. **목표 상태**: Go CLI → Osty-native `check.osty`/`llvmgen.osty`/`resolve.osty` 직접 호출 → 이들은 이미 parser.osty `AstArena` + `AstNodeKind`를 네이티브 소비. `ast_lower.osty`와 `internal/selfhost/astbridge/` 둘 다 unused → 삭제.
3. **전제 조건**: Osty-native downstream을 Go CLI에서 호출할 수 있는 ABI. 이는 Phase 4 runtime ABI 작업과 연결된다.

중요: **`toolchain/ast.osty` 같은 신규 네이티브 AST 타입을 도입하지 않는다.** parser.osty의 `AstArena`가 이미 네이티브 AST 역할을 하고 있고, native downstream 모듈 전부 이를 이미 소비 중. 신규 AST 타입 도입은 순수 중복 작업이며 불필요하다 (이 결정은 PR #372에서 확정).

### 비-bootstrap `use go` 파일이 앞으로 추가되면

CLAUDE.md 호스트 경계 규칙상 `use go`는 특별 허가 영역(`cmd/*`, I/O, CLI glue)에서만 허용된다. 새로운 `use go` 파일이 들어오면 자동으로 bootstrap-only probe에서 스킵되므로 native path에는 영향 없음. 단, 그 파일의 LLVM native 대체 계획은 이 문서 § 4개 테이블에 추가 필수.

## 테스트 전략

- Unit:
  - `internal/ir` lowering/validation snapshot
  - `toolchain/llvmgen.osty` type lowering, block builder,
    name mangling, with `internal/llvmgen` only as bootstrap/reference
  - runtime ABI header/layout tests
- Golden:
  - `.osty -> .ll` deterministic output
  - source map metadata/comment anchors
- Execution:
  - scalar/control-flow smoke tests
  - structs/enums/match tests
  - generics/closures tests
  - stdlib/runtime tests
  - concurrency tests
- CLI:
  - backend flag parsing
  - artifact path layout
  - missing LLVM toolchain diagnostics
  - compile/link/runtime failure reporting
- Parity:
  - same program run through Go and LLVM backends, compare stdout/stderr/exit code
  - known divergence list checked into tests, not hidden in comments

## 리스크와 대응

| 리스크 | 영향 | 대응 |
|---|---|---|
| IR가 backend 계약으로 부족함 | LLVM backend가 AST/checker에 다시 결합 | Phase 2에서 IR gap을 먼저 닫고 `llvmgen` dependency를 제한 |
| Runtime ABI가 늦게 정해짐 | helper가 backend별로 갈라짐 | Phase 4 전에 string/list/result 최소 ABI부터 문서화 |
| Go FFI와 native FFI 의미 차이 | 사용자 코드가 backend별로 다르게 동작 | Go FFI는 backend capability로 진단하고 native FFI를 별도 설계 |
| target triple/profile 모델 충돌 | cross compile UX 혼란 | Go target과 LLVM target을 manifest에서 분리하거나 명시 mapping |
| source mapping 품질 저하 | LLVM 에러 디버깅 어려움 | 초기부터 source anchor를 IR/LLVM에 싣고 CLI report를 backend-neutral화 |
| stdlib porting 범위 과대 | migration이 끝나지 않음 | module별 support matrix와 staged parity gate 운영 |
| CI 시간이 증가 | PR feedback 저하 | LLVM job은 smoke부터 시작하고 full parity는 nightly/manual로 분리 |

## 의사결정이 필요한 항목

- `Int`와 `Float`의 정확한 native width 정책 (`Float32`/`Float64`는 후속 단계)
- Memory model: manual/free, arena, refcount, GC 중 1차 선택
- Runtime implementation language
- LLVM target triple을 manifest `[target.*]`에 통합할지 별도 키로 둘지
- `use go`를 LLVM backend에서 어떻게 진단할지
- Debug info를 주석/source map으로 시작할지, DWARF metadata까지 1차에 넣을지
- Default backend 전환을 edition과 묶을지, manifest setting과 묶을지

## 추천 첫 PR 순서

1. `internal/backend` facade 추가, Go backend를 기존 동작 그대로 감싸기
2. `--backend`/`--emit` CLI plumbing과 artifact layout 테스트 추가
3. `internal/ir` metadata gap 목록을 테스트 기반으로 보강
4. `toolchain/llvmgen.osty` skeleton/IR builder와 `.ll` golden smoke
   test 추가. Go `internal/llvmgen`은 bootstrap/reference bridge로만 둔다.
5. LLVM toolchain discovery와 missing-tool diagnostic 추가
6. scalar/control-flow LLVM executable smoke test 추가

이 순서의 장점은 첫 PR부터 사용자 동작을 바꾸지 않으면서도, 이후 LLVM 작업이
작고 독립적인 리뷰 단위로 쌓인다는 점이다.
