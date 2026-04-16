# LLVM Migration Plan

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
- 현재 Phase 37까지의 LLVM backend 의미는 `examples/selfhost-core/llvmgen.osty`
  쪽으로 옮겨지고 있다. 여기에는 smoke IR builder, skeleton renderer,
  toolchain command plan, executable parity corpus, go-only diagnostic, 그리고
  unsupported source-shape taxonomy가 포함된다. 성공 경로의 scalar instruction
  strings, temp/label naming, plain/escaped ASCII `String` `println` module
  constant/formatting shape, immutable/mutable local String value paths, and
  simple String function return/parameter paths, simple named struct type
  definitions, struct literals, field reads, struct function boundaries, and
  mutable struct locals, plus bare enum tag values across simple function and
  mutable-local paths도 같은 selfhost-core에서 생성한다.

## 이주 원칙

1. Go 백엔드는 migration 기간 동안 안정 백엔드로 유지한다.
2. LLVM 백엔드는 AST가 아니라 `internal/ir`를 입력으로 삼는다.
3. LLVM backend 의미의 소유권은 Osty 소스에 둔다. 여기에는 IR lowering,
   artifact 단계 결정, toolchain command plan, backend diagnostics가 포함된다.
   Go 구현은 migration 기간의 bootstrap bridge/reference와 host file I/O/process
   실행 shim으로만 허용하고, 새 backend 의미는
   `examples/selfhost-core/llvmgen.osty` 같은 Osty-authored backend core로 먼저
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
  - 1차 위치: `examples/selfhost-core/llvmgen.osty`
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
    `examples/selfhost-core/llvmgen.osty`가 소유하고, Go test는 host execution
    shim으로만 동작한다.
  - Phase 20 기준으로 unsupported/backend-capability diagnostic policy도
    `examples/selfhost-core/llvmgen.osty`가 소유하고, Go는 source feature 감지와
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
| `Float/Float32/Float64` | `double/float/double` | `Float` width 정책 결정 |
| `String` | `%osty.string` | pointer+len 또는 fat value |
| `Bytes` | `%osty.bytes` | pointer+len+cap 또는 runtime-owned buffer |
| `List<T>` | `%osty.list` + element descriptor | monomorphized typed list와 erased list 중 선택 |
| `Option<T>` | tagged payload | nullable pointer optimization은 후순위 |
| `Result<T,E>` | tagged payload | Go backend의 `Value/Error/IsOk`와 semantic parity |
| `struct` | named LLVM struct | Phase 30-33 smoke subset uses value aggregates; full ABI/layout stability remains runtime work |
| `enum` | `i64` tag for bare variants, tagged payload storage later | Phase 34-37 smoke subset covers payload-free variants; niche optimization is later |
| closure | fn pointer + env pointer | capture lifetime/ownership 필요 |
| interface | data pointer + vtable pointer | vtable generation은 Phase 5+ |

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

## 테스트 전략

- Unit:
  - `internal/ir` lowering/validation snapshot
  - `examples/selfhost-core/llvmgen.osty` type lowering, block builder,
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

- `Int`와 `Float`의 정확한 native width 정책
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
4. `examples/selfhost-core/llvmgen.osty` skeleton/IR builder와 `.ll` golden smoke
   test 추가. Go `internal/llvmgen`은 bootstrap/reference bridge로만 둔다.
5. LLVM toolchain discovery와 missing-tool diagnostic 추가
6. scalar/control-flow LLVM executable smoke test 추가

이 순서의 장점은 첫 PR부터 사용자 동작을 바꾸지 않으면서도, 이후 LLVM 작업이
작고 독립적인 리뷰 단위로 쌓인다는 점이다.
