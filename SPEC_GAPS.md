# Osty 스펙·Grammar 갭 트래킹

`LANG_SPEC_v0.4/` + `OSTY_GRAMMAR_v0.4.md` 기준.

**v0.4 시점 open gap: 없음.** v0.3 의 구현·진단 경계가 부드럽던
항목(G13-G18)을 v0.4 에서 모두 결정했다. v0.4 baseline 위에 G19
(runtime sublanguage) 가 additive minor 로 결정되어 §19 에
명시되었다. 다음 새 gap 은 G20 부터 부여한다.

스펙은 v0.2 부터 폴더 구조이다. §X (X = 1..18) 는 `LANG_SPEC_v0.4/NN-*.md`
파일. §10 의 서브섹션은 `LANG_SPEC_v0.4/10-standard-library/NN-*.md`.

---

## Open Gaps

(없음)

---

## Resolved in v0.4

v0.4 의 초점은 새 문법을 크게 늘리는 것이 아니라, v0.3 에서 의미는
정했지만 구현·진단·테스트 경계가 아직 부드럽던 엣지케이스를
명시적으로 닫는 것이다. 아래 항목은 v0.4 baseline 에 포함된 최종
결정이다.

| ID | 영역 | 필요한 결정 | 상태 |
|---|---|---|---|
| **G13** | `Handle<T>` / `TaskGroup` escape | `taskGroup` 밖으로 handle/group capability 가 빠져나가는 경우를 finite front-end rule 로 금지한다. 반환, 필드 저장, channel send, escaping closure capture, collection 저장 후 group 밖 사용은 `E0743` non-escaping capability violation 으로 본다. | decided |
| **G14** | generic method turbofish + method refs | `obj.method::<T>(args)` 의 explicit type args 는 method-local generics 만 가리킨다. owner generics 는 receiver 타입에서 결정된다. partial explicit 은 금지. generic method 를 `obj.method` 함수값으로 빼는 것은 금지하고 wrapper closure 를 사용한다. | decided |
| **G15** | callable arity after erasure | 선언 함수의 default/keyword metadata 는 `fn(...) -> ...` 값으로 흐르지 않는다. function value 호출은 positional-only, exact arity 이며 too-few/too-many 모두 `E0701`. | decided |
| **G16** | closure parameter patterns | closure parameter 는 `LetPattern (':' Type)?` 이되 irrefutable pattern 만 허용한다. ident, wildcard, tuple, struct, nested irrefutable 조합은 허용하고 literal/range/variant/or pattern 은 `E0741`. | decided |
| **G17** | nested pattern witness diagnostics | exhaustiveness witness 는 한 개의 최소 missing pattern 만 출력한다. tuple/struct 는 좌측부터 첫 missing component 를 구체화하고 나머지는 `_`; closed enum/Option/Result payload 는 재귀 witness, 열린 타입은 `_`; guard arm 은 coverage 에 기여하지 않는다. | decided |
| **G18** | stdlib protocol executable stubs | §10/§15/§16/§17 protocol 은 checked signature stubs 우선으로 관리한다. bodies 는 dummy 여도 parse/resolve/check 되는 `.osty` stub 이어야 하며, runtime/gen parity 는 language gap 이 아니라 implementation backlog 로 분리한다. | decided |
| **G19** | runtime sublanguage capability surface | C 로 작성된 GC/allocator (`internal/backend/runtime/osty_runtime.c`) 를 Osty 로 셀프호스트할 수 있도록, **package-gated** 한 작은 surface 를 v0.4 에 additive minor 로 더한다. 사용자 prelude/문법은 변경 없음. opaque type `RawPtr` + marker trait `Pod` + 6 개 어노테이션 (`#[intrinsic]`, `#[pod]`, `#[repr(c)]`, `#[export("...")]`, `#[c_abi]`, `#[no_alloc]`) + `std.runtime.raw.*` 13 개 intrinsic (null/fromBits/bits/alloc/free/zero/copy/offset/read/write/cas/sizeOf/alignOf). 진단 `E0770` / `E0771` / `E0772` 추가. safepoint ABI 는 §19.10 에 명시 (compiler-emitted root array). | decided |

### Final v0.4 Decisions

이 표는 v0.4 로 올린 최종 결정안이다. 원칙은 세 가지:
front-end 에서 finite 하게 잡을 수 있을 것, 에러 메시지가 안정적일 것,
나중에 기능을 넓혀도 기존 코드를 깨지 않을 것.

| ID | 추천 결정안 | 이유 | 구현 체크 |
|---|---|---|---|
| **G13** | `Handle<T>` 와 `TaskGroup` 은 **non-escaping capability** 로 취급한다. 같은 `taskGroup` closure 안에서 `join`, `cancel`, `isCancelled`, helper 함수 인자로 전달하는 것은 허용한다. 반환, top-level/local-static 저장, struct field 저장, collection 저장 후 group 밖 사용, channel send, group 밖으로 탈출하는 closure capture 는 `E0743` 로 금지한다. | 구조적 동시성 invariant 를 가장 단순하게 보존한다. lifetime 시스템 없이도 AST + type flow 의 finite rule 로 구현 가능하다. | `Handle`/`TaskGroup` 타입을 poison tag 로 추적. return/top-level/assign/struct literal/list/map/channel send/closure capture 검사 및 tests 추가. |
| **G14** | `obj.method::<T>(args)` 는 **method-local generics 만** 명시한다. receiver 의 owner generics 는 receiver 타입에서 이미 결정된다. explicit method type args 는 exact arity 만 허용하고 partial explicit 은 금지한다. `obj.method` 함수값은 method-local generics 가 없는 경우만 허용한다. generic method 를 값으로 빼려면 wrapper closure 를 쓰게 한다. | owner generic 과 method generic 을 분리하면 사용자가 예측하기 쉽다. generic function value 를 도입하지 않아도 된다. | method call path 에 turbofish receiver 처리. method ref path 에 generic-method rejection diagnostic. |
| **G15** | `fn(...) -> ...` function value 로 지워지는 순간 default/keyword metadata 는 **보존하지 않는다**. function value 호출은 positional-only, exact arity 이다. 선언 함수 직접 호출과 package member 직접 호출만 default/keyword-aware 이다. | 함수 타입을 작게 유지하고, default 값 capture/재평가 semantics 를 피한다. 직접 호출의 편의성과 값 호출의 단순성을 분리한다. | `applyFnTo` under-arity 허용 제거 완료. direct declaration/member path 는 `applyDeclaredCall` 유지. |
| **G16** | closure parameter 는 `LetPattern (':' Type)?` 를 구현하되 **irrefutable pattern 만** 허용한다. 허용: ident, wildcard, tuple, struct pattern against struct, nested irrefutable 조합. 금지: enum variant, range, literal-only match, refutable or-pattern. 실패 시 `E0741`. | v0.3 스펙과 일치하고 `let` destructuring 규칙을 재사용할 수 있다. refutable closure params 를 허용하지 않아 호출 실패 경로가 생기지 않는다. | parser/gen parity 확인, checker irrefutable predicate + tests 추가. |
| **G17** | witness 진단은 **한 개의 최소 missing pattern** 만 출력한다. tuple/struct 는 좌측부터 첫 missing component 를 구체화하고 나머지는 `_` 로 둔다. enum/Option/Result payload 는 닫힌 타입이면 재귀 witness, 열린 타입이면 `_`. guard arm 은 coverage 에 기여하지 않는다. | 완벽한 pattern set 출력보다 안정적이고 읽기 쉽다. 현재 exhaustiveness 알고리즘과 잘 맞는다. | `findWitness` stringification 규칙 고정. nested tuple/struct/variant tests 추가. |
| **G18** | stdlib protocol 은 v0.4 에서 **checked signature stubs 우선**으로 간다. bodies 는 dummy 여도 parse/resolve/check 되는 `.osty` stub 이어야 한다. 구현 없는 런타임 동작은 gen/runtime backlog 로 분리하고, protocol signature 모호성만 G 번호로 승격한다. | 언어/프론트엔드 결정과 런타임 구현을 분리한다. 스펙 drift 를 테스트가 바로 잡게 한다. | `TestAllSignatureStubsCheck` 가 모든 embedded stub 의 parse/resolve/check 를 검증. `std.thread` Handle 생성 body 의 G13 `E0743` 만 예외. |
| **G19** | **Runtime sublanguage** 을 v0.4 baseline 에 additive minor 로 더한다. 새 surface 는 (a) opaque pointer-shaped type `RawPtr` (Pod, GC traceless), (b) compiler-decided marker interface `Pod`, (c) 어노테이션 6 개 — `#[intrinsic]`/`#[pod]`/`#[repr(c)]`/`#[export("name")]`/`#[c_abi]`/`#[no_alloc]`, (d) `std.runtime.raw` 의 13 개 intrinsic (`null`/`fromBits`/`bits`/`alloc`/`free`/`zero`/`copy`/`offset`/`read`/`write`/`cas`/`sizeOf`/`alignOf`), (e) §19.10 의 compiler-emitted root array safepoint ABI. **Gate 는 package path** 로만 — `std.runtime.*` 또는 toolchain workspace 안의 `[capabilities] runtime = true` 패키지만 사용 가능. 일반 사용자 코드에서 사용 시 `E0770`. `#[pod]` shape 위반은 `E0771`. `#[no_alloc]` 본문에서 managed allocation 발견 (또는 `cas` 의 invalid type size) 은 `E0772`. | (1) GC 를 Osty 로 셀프호스트하려면 raw memory + 주소 산술 + Pod load/store + C ABI export + "본문에서 절대 alloc 안함" 증명 + caller-emitted root array 가 필수다. (2) 사용자 prelude 에 `unsafe` 를 노출하지 않는 cleanest path 는 Rust `core::intrinsics` 와 같은 package-gated surface. (3) 새 grammar token 0 개, 새 키워드 0 개 → v0.4 grammar freeze 유지. (4) Pod 는 generic struct 의 경우 `T: Pod` bound 강제 (per-instantiation 은 v0.5 후보). (5) `cas` 는 `sizeOf<T> ∈ {1,2,4,8,16}` + 자연 정렬 제약. (6) `Option<T: Pod>` 은 Pod 이므로 runtime code 에서 `RawPtr?` 를 자유롭게 쓰지만, `#[c_abi]` 함수 반환 타입으로는 사용 불가 (allocator-failure 는 `null()` sentinel). | §19 새 챕터 작성 (10 개 sub-section). §2.1 / §2.6 / §3.8 / §14 / §18 cross-ref 추가. checker 에 (a) `E0770` privilege gate (annotation/type/import 모두), (b) `E0771` Pod shape 검사 + generic bound 검사, (c) `E0772` no_alloc 본문 walker + `cas` size 검사, (d) `Pod` 자동 derivation (primitives + `#[pod] #[repr(c)]` struct + tuple + `Option<T:Pod>`). lowering 에 (a) `raw.*` intrinsic 13 개 의 LLVM mapping 표 §19.7, (b) `#[c_abi]` → LLVM `ccc` calling conv, (c) `#[export]` → external linkage + `dso_local` preemption + exact symbol, (d) `read` 에 `freeze`. `std.runtime.raw` stdlib package + `osty_rt_alloc_aligned` shim 을 `internal/backend/runtime/raw_alloc_shim.c` 에 unconditionally 링크. 기존 `osty_runtime.c` ABI (alloc_v1/free... safepoint_v1/post_write_v1/...) 보존 테스트는 `internal/backend/llvm_runtime_gc_test.go` 를 그대로 통과시킴. manifest schema 의 `[capabilities] runtime = true` 는 §10/§11/§13 manifest reference 의 다음 minor 에 동기화. |

### Closed during v0.4 prep

| 항목 | 해결 위치 | 비고 |
|---|---|---|
| builtin type-position partition | `internal/check/typeref.go` | `Some<Int>` 같은 builtin value-in-type-position 을 `E0502` 로 거부. |
| builtin type arity | `internal/check/typeref.go` | `Equal<Int>`, `TaskGroup<T>` 같은 잘못된 type argument 수를 `E0727` 로 거부. |
| explicit function-call turbofish arity | `internal/check/expr.go` | `f::<A, B>()` 가 선언 generic 수와 다르면 `E0727`; method/value-position semantics 는 G14 로 추적. |
| cross-package call arity | `internal/check/expr.go` | package member calls 도 keyword/default-aware call path 를 사용해 too-few/too-many 를 모두 진단. |
| erased function-value arity | `internal/check/expr.go` | `fn(...) -> ...` 값 호출은 default/keyword metadata 없이 exact positional arity 로 검사. |
| closure parameter irrefutability | `internal/check/expr.go` | tuple/struct closure params 의 binding type 을 설치하고 literal/range/variant/or pattern 을 `E0741` 로 거부. |
| non-escaping capability escape | `internal/check/capability.go`, `internal/check/stmt.go`, `internal/check/expr.go` | `Handle<T>` / `TaskGroup` 반환, top-level 저장, field/list/map/channel 저장, escaping closure capture 를 `E0743` 으로 거부. helper 함수 인자와 `TaskGroup.spawn` 의 group capture 는 허용. |
| empty turbofish syntax | `internal/parser/parser.go` | `foo::<>` 를 `E0300` 으로 거부해 type-argument list 를 non-empty 로 고정. |
| generic callable reference | `internal/check/expr.go` | generic top-level function / method 를 값으로 꺼내는 것을 `E0742` 로 거부. wrapper closure 로 명시적 instantiation 을 요구. |
| method turbofish implementation | `internal/check/expr.go` | `obj.method::<T>(args)` 를 method-local generics 로만 적용하고 owner generics 는 receiver 에서 대체. |
| empty generic/type parameter lists | `internal/parser/parser.go` | `fn f<>()`, `List<>`, `foo::<>` 모두 non-empty rule 위반으로 거부. |
| keyword args after callable erasure | `internal/check/expr.go` | function value 호출에서 keyword arg 를 `E0732` 로 거부해 positional-only rule 을 구현. |
| singleton tuple type parsing | `internal/parser/parser.go` | `(T,)` 는 grouping 이 아니라 1-요소 tuple type 으로 파싱. `fn()` function type 은 Unit-return shorthand 로 문법에 명시. |
| checked stdlib signature stubs | `internal/stdlib/stdlib_test.go`, `internal/stdlib/**/*.osty` | 모든 embedded stdlib stub 이 parse/resolve/check 를 통과하도록 G18 기준을 실행 테스트로 고정. `std.thread` 의 Handle 생성 dummy body 만 G13 `E0743` 예외로 두고 signature 검증은 유지. |

### Not Language-Decision Gaps

아래는 검색에서 나온 `TODO`/`future` 항목이지만, 언어 의미론의
미정사항으로 취급하지 않는다. 필요하면 별도 implementation backlog 로
관리하고, `SPEC_GAPS.md` 의 G 번호를 부여하지 않는다.

| 항목 | 분류 | 처리 |
|---|---|---|
| generic type parameter defaults (`struct Pair<T, U = T>`) | excluded feature | v0.4 grammar freeze 에서 제외. 나중에 넣으려면 새 edition 제안으로 재오픈. |
| Historical Go transpiler TODO audit / unsupported lowering forms | backend archive | 언어 스펙 미정이 아니라 retired backend parity 문맥이다. `LLVM_GEN_TODO_AUDIT.md` 는 역사 기록으로만 유지한다. |
| IR가 아직 gen 의 입력이 아님 | backend architecture | `internal/ir/doc.go` 의 follow-on work. 언어 결정 아님. |
| package-manager SAT solver, multi-binary manifests, build artifact convention | tooling/product backlog | manifest/pkgmgr/build 문서나 이슈로 관리. 언어 의미론 아님. |
| LSP incremental sync, richer doc/comment transport | tooling backlog | LSP/editor 품질 항목. 언어 의미론 아님. |

---

## Resolved in v0.3

| 갭 | 해결 위치 | 비고 |
|---|---|---|
| **G4** 클로저 파라미터 패턴 | `04-expressions.md` §4.7 | `LetPattern` 확장, irrefutable 제한. v0.4 에서 parser/checker 구현 parity 완료. |
| **G8** 채널 close semantics | `08-concurrency.md` §8.5 | 누구든 close, 두 번째 abort. drain 후 `None`. |
| **G9** `Builder<T>` phantom | `03-declarations.md` §3.4 | 완전 추상화. 에러 메시지에 누락 필드 명시. |
| **G10** Char / surrogate | `02-type-system.md` §2.1, `10-standard-library/05-standard-numeric-methods.md` §10.5 | surrogate 불허, `Char.fromInt` 안전 변환. |
| **G11** Generic 컴파일 모델 | `02-type-system.md` §2.7.3 | Monomorphization. Interface 값은 fat pointer vtable. |
| **G12** Cancellation 전파 | `08-concurrency.md` §8.4 | Task-group 자동 전파. `Cancelled(cause: Error)`. stdlib 전체 cancel-aware. |

v0.3 는 추가로 v0.2 전수 감사에서 나온 91건의 세부 모호성을 모두
결정. 주요 항목은 `LANG_SPEC_v0.3/18-change-history.md` §18.1 참조.

---

## Resolved in v0.2

| 갭 | 해결 위치 | 비고 |
|---|---|---|
| **G1** 컬렉션 `Equal`/`Hashable` 자동 구현 조건 | `02-type-system.md` §2.6.5 | Built-in instances 표 + 컬렉션 derivation 규칙 |
| **G2** Duration toString, log 표현 | `10-standard-library/20-time-extensions.md`, `10-standard-library/10-logging.md` | `Duration.toString()` 추가, `LogValue::Duration` 명시 |
| **G3** `LogValue` 합 타입 정의 | `10-standard-library/10-logging.md` §10.10 | Concrete enum + `ToLogValue` trait + 자동 promotion |
| **G5** 스크립트 `return` 의미 | `06-scripts.md` §6 | 암묵 main 의 return 으로 정의 |
| **G6** Partial struct 어노테이션 | `03-declarations.md` §3.4 | 각 선언 독립 적용, 합성 없음 |
| **G7** 진단 메시지 템플릿 | `03-declarations.md` §3.1 | positional-after-keyword 에러 박스 추가 |
