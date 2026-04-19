# Osty 스펙·Grammar 갭 트래킹

`LANG_SPEC_v0.5/` + `OSTY_GRAMMAR_v0.5.md` 기준.

**v0.5 시점 open gap: 없음.** v0.4 사용 코퍼스에서 관찰된 15 개
개선점(G20-G34) 을 v0.5 에서 일괄 결정했다.

스펙은 v0.2 부터 폴더 구조이다. §X (X = 1..18) 는 `LANG_SPEC_v0.5/NN-*.md`
파일. §10 의 서브섹션은 `LANG_SPEC_v0.5/10-standard-library/NN-*.md`.
§19 는 v0.4 additive minor 로 도입된 toolchain-only 서브언어.

---

## Open Gaps

(없음)

**운영 정책.** 새 gap 은 기존 처리 절차대로 G 번호를 부여해 `Open Gaps`
섹션에서 추적. 버그 수정 / 명확화 / 성능 최적화 / 의미 중립 변경은
G 번호 없이 일반 이슈 트래커에서 처리. 언어 surface 변경은 정식 버전
업 (minor / major) 과 함께 문서화.

---

## Resolved in v0.5

v0.5 는 v0.4 이후 일정 기간 사용하며 누적된 15 개 개선점을 한 번에
결정해서 접는 릴리스다. 모두 additive — v0.4 프로그램은 바이트 단위
변경 없이 컴파일된다.

| ID | 영역 | 결정 | 상태 |
|---|---|---|---|
| **G20** | Function value parameter-name preservation | `fn(...) -> ...` 타입에 파라미터 이름을 **type-equality-neutral metadata** 로 보존. 이름이 일치하는 키워드 호출을 함수값에서도 허용. 기본값 metadata 는 G15 대로 여전히 erase. 타입 동등성은 이름을 무시하므로 `fn(String, Int)` 타입으로의 대입은 그대로 가능. | decided |
| **G21** | Default argument literal definition extension | `DefaultLiteral` 집합이 (a) 필드가 모두 literal 인 struct literal 과 (b) `const fn` 호출 반환값을 포함. 평가 시점은 기존 규칙 (each call site) 유지. `const fn` 본문 허용 범위는 §3.1.1 capability matrix 로 고정 — literal / 산술 / 비교 / 논리 / 직접 `const fn` 호출 / 구성자만 허용, 제어 흐름·재귀·문자열 연결·제네릭은 금지. Call graph 는 acyclic (`E0767`), 제네릭 `const fn` 은 `E0768`, 매트릭스 밖 구문은 `E0766`. | decided |
| **G22** | `loop` expression | `loop { ... break value }` — 값 반환 가능한 무한 루프. `for cond { }` (Unit 반환 while-style) 와 역할 분리. `break value` 의 타입이 루프 전체 타입. | decided |
| **G23** | Trailing closure call | 마지막 인자가 함수 타입일 때 `(...)` 바깥으로 closure 를 뺄 수 있다. 괄호 + closure 공존 가능 (`f(a, b) \|x\| { ... }`). Closure 시작 `\|` 다음에 ident 가 오지 않으면 block expression 으로 파싱 (fallthrough). | decided |
| **G24** | Labeled break/continue | `'label: for / loop` prefix. `break 'label [value]?` / `continue 'label`. 없는 라벨은 `E0763`. 라벨 없으면 가장 가까운 enclosing 루프 대상. | decided |
| **G25** | Range step `by` | `0..100 by 2`, `10..=0 by -1`. `by` 는 range expression 문맥의 contextual keyword — 그 외 위치에선 식별자. 0 step 은 런타임 abort. | decided |
| **G26** | Struct update shorthand | receiver 로컬 식별자가 struct 타입이면 `receiver { field: value, ... }` 가 `Type { ..receiver, field: value, ... }` 와 등가. Expression path (비-ident) 수신은 기존 `Type { ..expr, ... }` 형식 유지. | decided |
| **G27** | `as?` downcast syntax | `err as? T` 는 `err.downcast::<T>()` 와 정확 등가. 좌측 피연산자가 `Error` 구현체여야 함; 그 외는 `E0727`. 새 키워드 없음 — `as?` 는 single 토큰으로 lex. | decided |
| **G28** | Scoped / grouped imports | `use path::{a, b as c, d}`. 기존 `use Path (as Ident)?` 와 공존. 중복 이름은 `E0554`. | decided |
| **G29** | Conditional compilation `#[cfg(...)]` | pre-resolve filter — cfg 평가가 false 인 선언은 resolve 전에 제거되어 type check 도 안 됨. Key 네 종류만 허용 (`os`, `target`, `arch`, `feature`). 조합은 `all(a, b)` / `any(a, b)` / `not(x)`. 미인식 key 는 `E0405`. | decided |
| **G30** | `pub use` re-export | 해당 심볼의 가시성을 re-export 위치에 복사. 순환은 `E0552`. target 이 private 인데 `pub use` 로 노출 시도는 `E0553`. | decided |
| **G31** | Enum integer discriminants | `pub enum X: IntN { A = 1, B = 2 }` — discriminant 타입은 `Int` / `Int32` / `Int16` / `Int8` 네 개. Payload variant 에는 할당 금지 (`E0721`). 중복 값은 `E0722`. `.discriminant()` / `X.fromDiscriminant(n)` 자동 파생. | decided |
| **G32** | Inline `#[test]` + doctest | `#[test]` 어노테이션이 붙은 `fn` 은 프로덕션 바이너리에서 제외되고 `osty test` 에서만 수집. 기존 `_test.osty` 경로는 backward compat 으로 유지. `///` 블록 안의 ` ```osty ``` ` fence 에서 doctest 추출 — `osty test --doc`. | decided |
| **G33** | Property-based testing | `std.testing.gen` 서브모듈 신설 — `Gen<T>` + 조합자 (`oneOf` / `map` / `filter` / `pair` / `list` / `record`), 기본 타입에 대한 shrinker. `testing.property(name, gen, pred)` 러너. | decided |
| **G34** | Lossless numeric widening | 암묵 승격: `Int8 → Int16 → Int32 → Int → Float64`, `Int32 → Float64`, `Int → Float64`, `Float32 → Float64`. Narrowing 은 접미사 강제 — `.toInt32() / .toInt16() / .toInt8() / .toIntTrunc() / .toIntRound() / .toIntFloor() / .toIntCeil() / .toFloat32()`. 암묵 narrowing 은 `E0765`. | decided |
| **G35** | Bounded operator overloading | `#[op(+)] / #[op(-)] / #[op(*)] / #[op(/)] / #[op(%)]` (binary) + `#[op(-)]` (unary) 여섯 개만 허용. 어노테이션 없으면 디스패치 안 됨 (우연 오버로딩 차단). 메서드 시그니처 불일치는 `E0723`, 중복 구현은 `E0724`, 허용되지 않은 연산자는 `E0725`. `== / != / < / <= / > / >= / [] / () / << / >> / & / \| / ^` 는 primitive-only. | decided |

### Final v0.5 Decisions

이 표는 v0.5 에 올린 결정안이다. 원칙 네 가지:
문법 확장은 모두 additive 일 것, 새 키워드는 contextual 일 것,
체커 변경은 기존 타입 equality 를 깨지 않을 것, 기본 인자 / 오버로드
같은 확장은 opt-in annotation 을 요구할 것.

| ID | 추천 결정안 | 이유 | 구현 체크 |
|---|---|---|---|
| **G20** | 함수값 타입이 파라미터 이름을 metadata 로 보존한다. 이름 일치 시 키워드 호출을 허용하지만 타입 equality 에서는 무시한다. 기본값은 G15 대로 erase. | HOF 체이닝에서 API 이름을 유지하되, 함수값을 다른 같은-shape 타입으로 alias 하면 이름은 사라지도록 한다. 최소 변경으로 실전 체감을 올림. | `FnType` 구조에 `paramNames: List<String>?` 추가. equality 는 이름 skip. 키워드 호출 경로에서 이름 일치 체크 후 positional 로 강등. |
| **G21** | `DefaultLiteral` 을 재귀적으로 정의해서 fields 가 literal 인 struct literal 과 `const fn` 호출을 포함한다. `const fn` 본문 범위는 §3.1.1 capability matrix 로 확정. | 팩토리 함수로 우회해야 했던 default 인자 케이스를 직접 작성하게 한다. "각 call site 에서 재평가" 규칙은 그대로 유지. 경계를 matrix 로 박아 확장은 정식 버전 업에서만 — 패치 내 조용한 완화로 인한 de facto dialect drift 차단. | default expr 파서에 struct literal / `const fn` 호출 허용. checker 가 `isDefaultLiteral` 을 재귀 정의. `const fn` 본문은 §3.1.1 matrix 검사 (`E0766`), call graph 는 resolver 에서 acyclic 검증 (`E0767`), 제네릭 `const fn` 은 parser/checker 에서 거부 (`E0768`). |
| **G22** | `loop { ... }` 은 값 반환. `for cond { }` 는 Unit. | `for cond` 에 break-value 를 얹으면 `for` / `loop` 역할이 섞여 사용자가 혼동한다. 두 형태를 분리. | parser: `LoopExpr`. checker: `break value` 의 타입 수집 → 루프 타입. LLVM: SSA result via phi. |
| **G23** | 마지막 인자가 함수 타입일 때 trailing closure 허용. 괄호와 공존. | Kotlin/Swift 패턴으로 검증됨. `taskGroup` / `testing.context` / `List.map` 같은 DSL 체이닝이 시각적으로 깔끔해진다. | parser disambiguation: callee 직후 같은 줄에 `\|ident` 가 오면 closure 인자로 해석. `\|` 다음 ident 없으면 block expression fall-through. 포매터는 `foo(args) { body }` 류 선호 레이아웃 제공. |
| **G24** | `'label:` prefix + `break/continue 'label`. 라벨 없으면 innermost. | sentinel flag 패턴 제거. 라벨 범위는 lexical 하므로 resolver 부담 낮음. | lexer: `'` prefix → label token. parser: loop prefix label. resolver: label stack. `break 'undefined` → `E0763`. |
| **G25** | `by` contextual keyword — range expression 문맥에서만. step = 0 은 abort. | 역방향 + 스텝 표현을 자연스럽게. 기존 식별자 `by` 쓰는 프로그램 호환 위해 contextual. | parser: `..` / `..=` 다음에 `by expr` optional. checker: step 타입 = element 타입. 런타임: step == 0 abort. |
| **G26** | receiver 식별자 + struct literal = spread shorthand. 비-ident 는 기존 `..expr` 만. | 가장 흔한 패턴 (같은 변수 갱신) 에서 `..self` 잉크 제거. Ident 한정이라 파서 구현 단순. | parser: `Ident '{' FieldInit ... '}'` 에서 Ident 가 해당 scope 의 struct 변수이면 shorthand. resolver 가 타입 look up 후 desugar. |
| **G27** | `as?` single 토큰. `Error` downcast 한정. | `.downcast::<T>()` 가 자주 쓰이는 곳에서 타이핑 절약. 임의 타입 변환용으로 확장하지 않음 → `as` 키워드 금지 정책 유지. | lexer: `as?` 단일 토큰. parser: postfix. checker: 피연산자 타입이 `Error` 구현체인지 확인; 아니면 `E0727`. MIR: `downcast` intrinsic 과 동일 lowering. |
| **G28** | `use path::{a, b as c}`. 기존 `use path as alias` 와 공존. | 명시 import 로 scope 오염을 줄이고, rename 조합도 같은 구문에 포함. | parser: UseItem = `Path ('as' Ident)?`. resolver: UseItem 각각이 바인딩 생성. 중복은 `E0554`. |
| **G29** | `#[cfg(...)]` pre-resolve 필터. Key 4 종, 조합 `all/any/not`. | 플랫폼 분기를 언어 단에서 지원해야 stdlib 자체 (std.os 등) 가 작성 가능. Key 화이트리스트로 scope 확산 방지. | resolver: cfg 평가는 file 단위 scan 으로 resolve 전 수행. 제외된 선언은 AST 에서 제거. 미인식 key `E0405`. cfg 조합은 literal DSL. |
| **G30** | `pub use` 는 원본 가시성을 re-export 위치로 복사. 순환은 `E0552`. | facade 패턴 기본 요구사항. private 노출 시도는 명시 거부. | resolver: re-export 체인 topological sort. `pub use` target 이 private → `E0553`. self-cycle / mutual-cycle → `E0552`. |
| **G31** | discriminant 할당은 payload-free variant 만. 타입은 네 정수 타입만. | FFI / 프로토콜 상수 매핑에서 자주 쓰이는 패턴을 선언 안으로 가져옴. payload variant 에도 허용하면 C-enum 과 semantics 가 꼬임. | parser: `EnumDecl` 확장. checker: 각 variant payload 여부 확인 후 할당 허용/금지. auto-derive `.discriminant()` / `.fromDiscriminant(n)`. LLVM: explicit tag 저장. |
| **G32** | `#[test]` inline + ` ```osty ``` ` doctest. `_test.osty` 파일은 계속 유효. | 작은 유틸 테스트의 진입 장벽 제거. Doctest 는 문서가 곧 테스트 보장. | checker: `#[test] fn` 표식 → 테스트 컬렉션 등록, 프로덕션 exclude. doctest extractor: `///` fence 파싱 → implicit test fn 생성. `osty test --doc` 플래그 추가. |
| **G33** | `std.testing.gen` 서브모듈 + `testing.property` 러너. 기본 타입 shrinker 포함. | Fuzz 전 단계에서 invariant 검증을 싸게 돌릴 수 있는 경로. stdlib 에 표준 reducer / generator 를 박아 사용자 프로젝트 간 일관성 확보. | `std.testing.gen` 신설. `Gen<T>` 인터페이스 + 기본 조합자. shrinker 는 `T: Shrinkable` interface 로 opt-in. `property` 러너는 100 회 기본, seed CLI 노출. |
| **G34** | 암묵 widening lattice + narrowing 접미사 강제. 암묵 narrowing 은 `E0765` + `.toXxx()` 힌트. | 수식 가독성 확보 vs 정밀도 안전을 양쪽 다 잡음. 방향성 규칙이 lattice 이므로 사용자 예측 가능. | checker: 이항 연산 이전에 `commonWiden(lhs, rhs)` 삽입. `applyLiteralAdapt` 는 여전히 먼저 시도 후 fallback. MIR: widening 을 `sext` / `zext` / `sitofp` / `fpext` 로 lowering. |
| **G35** | `#[op(...)]` 6 연산자 opt-in. 허용되지 않은 연산자 시도는 `E0725`. | 수학 코드 가독성 회복 + iostream-style 오남용 차단. 6개는 finite 하고 우선순위 / 결합성이 primitive 와 동일. | parser: `#[op(+)]` 등 annotation value 화이트리스트. checker: 이항 / 단항 연산 디스패치 훅. 우선순위 테이블은 primitive 와 공유. 중복 구현 `E0724`, 시그니처 오류 `E0723`, 비허용 연산자 `E0725`. |

### Closed during v0.5 prep

| 항목 | 해결 위치 | 비고 |
|---|---|---|
| E07xx 진단 네임스페이스 정리 | `internal/diag/codes.go`, `toolchain/check_diag.osty` | Go / osty 양쪽 drift 된 E07xx 를 codes.go 기준으로 canonical 화. 9 개 Osty-native 개념에 신규 코드 (E0744-E0749 / E0751-E0753) 배정. |
| G14 generic callable reference 구현 | `toolchain/elab.osty` | 제네릭 최상위 fn / 제네릭 메서드를 값으로 꺼내는 걸 `E0742` 로 거부. turbofish 값 참조 경로 (`let g = f::<Int>`) 도 동일하게 막음. |
| Struct spread 구현 | `toolchain/elab.osty` | `P { ..p, x: 5 }` 가 `..p` 가 제공하는 필드를 `missing-field` 로 잘못 잡던 버그 수정. spread operand 를 owner type hint 로 elaborate 후 필드 이름 수집. |
| G15 behavior lock | `toolchain/elab.osty`, tests | 함수값 호출이 default / keyword metadata 없이 positional + exact arity 만 받음을 3 개 전용 테스트로 pin. |
| List.take / drop 추가 | `internal/stdlib/modules/collections.osty` | toolchain 코드 슬라이싱 관용구를 stdlib 에 노출. |
| testing.assertTrue / assertFalse | `internal/stdlib/modules/testing.osty`, `internal/llvmgen/stmt.go` | Boolean assertion API 완성. LLVM 하네스가 assertTrue / assertFalse 를 intercept. |
| std.strings chapter 구현 | `internal/stdlib/modules/strings.osty` | Unicode grapheme 테이블 + trim / split / replace / toUpper / toLower / hasPrefix / hasSuffix 전 함수 구현. |
| Option / Result full combinator surface | `internal/stdlib/modules/option.osty`, `result.osty` | `isSome` / `andThen` / `mapErr` / `orElse` 등 26 개 + 24 개 메서드 서명 체크. |

### Not Language-Decision Gaps

아래는 검색에서 나온 `TODO` / `future` 항목이지만, 언어 의미론의
미정사항으로 취급하지 않는다. 필요하면 별도 implementation backlog 로
관리하고, `SPEC_GAPS.md` 의 G 번호를 부여하지 않는다.

| 항목 | 분류 | 처리 |
|---|---|---|
| generic type parameter defaults (`struct Pair<T, U = T>`) | excluded feature | v0.5 에서 제외. 재오픈하려면 정식 버전에서 설계 제안 필요. |
| Historical Go transpiler TODO audit / unsupported lowering forms | backend archive | 언어 스펙 미정이 아니라 retired backend parity 문맥이다. `LLVM_GEN_TODO_AUDIT.md` 는 역사 기록으로만 유지한다. |
| IR 가 아직 gen 의 입력이 아님 | backend architecture | `internal/ir/doc.go` 의 follow-on work. 언어 결정 아님. |
| package-manager SAT solver, multi-binary manifests, build artifact convention | tooling / product backlog | manifest / pkgmgr / build 문서나 이슈로 관리. 언어 의미론 아님. |
| LSP incremental sync, richer doc / comment transport | tooling backlog | LSP / editor 품질 항목. 언어 의미론 아님. |
| `BytesSlice` / `StringView` zero-copy 뷰 | performance backlog | v0.5 excluded 로 명시. 컴파일러 최적화 (escape analysis) 로 대체. |
| Phantom type params (user-facing) | excluded feature | `Builder<T>` 내부 phantom 외 user-visible phantom 은 v0.5 excluded. |

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
| **G19** | runtime sublanguage capability surface | C 로 작성된 GC/allocator (`internal/backend/runtime/osty_runtime.c`) 를 Osty 로 셀프호스트할 수 있도록, **package-gated** 한 작은 surface 를 v0.4 에 additive minor 로 더한다. 사용자 prelude/문법은 변경 없음. opaque type `RawPtr` + marker trait `Pod` + 6 개 어노테이션 (`#[intrinsic]`, `#[pod]`, `#[repr(c)]`, `#[export("...")]`, `#[c_abi]`, `#[no_alloc]`) + `std.runtime.raw.*` 13 개 intrinsic (null/fromBits/bits/alloc/free/zero/copy/offset/read/write/cas/sizeOf/alignOf). 진단 `E0770` / `E0771` / `E0772` 추가. safepoint ABI 는 §19.10 에 명시 (compiler-emitted root array). 구현 진행 상황은 LANG_SPEC §19.11 (Implementation Status) 표가 권위적이며, 새 piece 가 land 할 때마다 같은 PR 에서 표를 갱신한다. | decided |

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
결정. 주요 항목은 v0.3 change-history §18.1 참조.

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
