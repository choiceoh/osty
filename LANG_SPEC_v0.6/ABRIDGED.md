# Osty v0.6 Agent Quick Spec

AI 에이전트가 짧게 읽고 바로 Osty 코드를 생성·수정하기 위한 규칙 카드.
예제는 싣지 않는다. 충돌 시 `LANG_SPEC_v0.6/` 의 정식 챕터와
`OSTY_GRAMMAR_v0.6.md` 가 우선한다.

> **v0.6 Design north star**: *Hidden dependency is forbidden* — 시간,
> 난수, 환경, 보안 흐름, 진화 규칙, 성능 계약, 의도, 명세 어느 것도
> 암묵으로 두지 않는다.

## 1. File and Lexing

- 파일은 `.osty`, UTF-8, newline은 `\n`; `\r\n`은 정규화된다.
- shebang은 byte offset 0에서만 한 번 허용된다.
- 예약어: `fn struct enum interface type let mut pub if else match for break
  continue return use defer while`. (`while` v0.6 추가, G49)
- 문맥 식별자: `self Self true false Some None Ok Err loop const by spec
  example law invariant`. (`spec` / `example` / `law` / `invariant` v0.6 추가, G43)
- v1 단계 추가 예정: `forall` (Phase 5).
- identifier는 ASCII letter 또는 `_`로 시작한다. 단독 `_`는 wildcard이다.
- 세미콜론은 없다. newline이 statement separator이다.
- `} else`와 `} else if`는 같은 물리적 줄에 둔다.
- method chain은 다음 줄의 선행 `.` 또는 `?.`로 이어라. trailing dot은 쓰지 마라.
- 1요소 tuple에는 comma가 필요하다.
- `=>`, `++`, `--`는 없다.

## 2. Declarations

- top-level: `use`, `let`, `fn`, `struct`, `enum`, `interface`, `type`.
- visibility 기본값은 package-private. export에는 `pub`을 붙인다.
- `pub` 가능 위치: top-level declaration, struct field, struct/enum method.
- function parameter type은 필수. unit return은 return type 생략 가능.
- function body는 block expression이고 마지막 expression이 반환값이다.
- early return은 `return`.
- function overloading은 없다. operator overloading은 `#[op(+)]`, `#[op(-)]`,
  `#[op(*)]`, `#[op(/)]`, `#[op(%)]` binary와 unary `#[op(-)]`만 허용된다.
- `const fn`은 default argument용 compile-time evaluable function이다. 본문은
  §3.1.1 capability matrix 안의 literal/산술/구성/acyclic const-fn call만 쓴다.
- default parameter는 trailing parameter에만 허용된다.
- default value는 `DefaultLiteral`: literal 계열, `None`, `Ok`/`Err`, empty
  collection, unit, fields가 literal인 struct literal, 허용된 `const fn` call만 가능하다.
- required parameter는 positional-only.
- defaulted parameter는 positional 또는 keyword argument로 전달 가능.
- positional argument는 keyword argument 뒤에 올 수 없다.
- function value로 저장된 callable은 keyword/default metadata가 없다. exact
  positional arity로만 호출한다.
- `self` 또는 `mut self`는 method 첫 parameter에서만 쓴다. type annotation을
  붙이지 않는다.
- `struct`/`enum` partial declaration은 같은 package 안에서만 가능하다.
  visibility와 type parameter는 모두 일치해야 한다.
- 같은 partial type의 field/variant는 한 declaration에만 있어야 한다. method
  이름은 중복될 수 없다.
- compiler annotation은 고정 집합이다 — v0.6에서 31개로 확장 (§14 참조).
- annotation은 named declaration 앞에 둔다. v0.6 부터 parameter 위치에도
  허용 (Pattern 앞 또는 Type 앞).

## 3. Types

- primitive: signed/unsigned integer family, `Byte`, float family, `Bool`,
  `Char`, `String`, `Bytes`, `Never`.
- `Int`는 항상 64-bit signed. machine-word `UInt`는 없다.
- `String`은 immutable UTF-8 bytes. `Bytes`는 immutable byte sequence.
- composite: `struct`, `enum`, `interface`, tuple, function type, `List<T>`,
  `Map<K, V>`, `Set<T>`, `Option<T>`, `Result<T, E>`.
- `Set<T>`는 있지만 set literal은 없다. `Set.from([...])`를 사용한다.
- `T?`는 `Option<T>` sugar. formatter는 `Option<T>`를 `T?`로 정규화한다.
- `null`/`nil`은 없다. 부재는 `Option<T>`/`T?`.
- alias는 transparent하다. 새 nominal type이 아니다.
- 변수 사이 numeric conversion은 lossless widening만 암묵적으로 허용된다. narrowing과
  lossy conversion은 명시 method를 쓴다.
- numeric literal만 문맥 타입으로 추론된다. 문맥이 없으면 integer는 `Int`,
  float는 `Float`.
- arithmetic overflow, invalid shift, integer div/mod by zero는 abort한다.
  복구 동작은 checked/wrapping/saturating method를 사용한다.
- value semantics: primitives, `String`, `Bytes`, tuple.
- reference semantics: `struct`, `enum`, collections, function/closure values.
- binding은 기본 immutable. 재할당과 field mutation에는 `mut` binding이 필요하다.

## 4. Interfaces and Generics

- interface satisfaction은 structural typing이다.
- interface body에는 method signature, default method, composed interface가 온다.
- `Self`는 interface에서는 implementing type, struct/enum에서는 enclosing type.
- built-in protocol: `Equal`, `Ordered`, `Hashable`, `ToString`, `Error`,
  `Iterator<T>`, `Iterable<T>`, `Reader`, `Writer`, `Closer`.
- v0.6 stdlib capability protocol: `Clock`, `Rng`, `Env`, `Fs`, `Net`,
  `Process`, `Console` (§9).
- `==`/`!=`는 primitive에서는 built-in, 그 외에는 `Equal`.
- `struct`, `enum`, tuple은 조건 충족 시 `Equal`/`Hashable` auto-derive.
- collection, primitive, `Option`, `Result`의 built-in instance는 override 불가.
- `Ordered`는 user composite에 auto-derive되지 않는다.
- expression position generic call은 `expr::<T, U>(args)`만 허용된다.
- `::` 뒤에는 반드시 non-empty `<...>`가 온다.
- type position은 `Name<T, U>`를 쓴다. nested `>>`는 type parser가 분할한다.
- enum variant construction에는 turbofish를 쓰지 않는다.
- generic method call의 explicit type args는 method-local generics에만 적용된다.
- owner generics는 receiver type에서 이미 결정된다.
- first-class polymorphic function value는 없다. generic callable을 값으로
  꺼내지 말고 concrete wrapper closure를 만든다.
- generics는 monomorphization된다. interface-typed parameter는 fat pointer와
  vtable dispatch이다.

## 5. Expressions

- block은 lexical scope이며 expression position에서 마지막 expression 값을 가진다.
- `if`가 expression이면 모든 branch type이 같고 `else`가 필요하다.
- `match`는 exhaustive여야 한다.
- match guard는 arm 선택에는 참여하지만 exhaustiveness coverage에는 기여하지 않는다.
- `for pattern in expr`은 iterable loop.
- `for expr`은 while-style loop. **v0.6: `while expr` 도 같은 의미 (G49)**.
- bare `for`는 infinite loop.
- `loop { ... break value }`는 value-returning unbounded loop이다.
- range step은 `a..b by step` / `a..=b by step`이다.
- `for let pattern = expr`은 match 성공 동안 반복한다.
- `break`/`continue`는 기본적으로 innermost loop에 적용된다. `'label: for/loop`와
  `break 'label` / `continue 'label`이 허용된다.
- `Type { ... }` struct literal은 `if`/`match`/`for`/`while`/`if let`/`for let` head에서
  괄호로 감싼다.
- assignment는 statement이다. expression으로 쓰지 않는다.
- channel send `<-`도 statement이다.
- postfix `?`는 `Result<T, E>`와 `Option<T>`에만 적용된다.
- `?`는 enclosing return type과 같은 family로만 전파한다. `Option`과 `Result`
  family를 직접 섞지 않는다.
- `?.`는 `Option<T>` field/method access를 short-circuit한다.
- `??`는 left가 `None`일 때만 right를 평가한다.
- `err as? T`는 `Error.downcast::<T>()` shortcut이다. 일반 type test가 아니다.
- closure는 capture by reference이다. capture mutability는 binding 선언을 따른다.
- trailing closure는 마지막 function-typed argument에만 쓴다: `f(x) |y| { ... }`.
- closure parameter pattern은 irrefutable `LetPattern`만 허용된다. refutable
  literal/range/variant/or pattern은 `E0741`.
- member access와 method call은 `.`만 사용한다.
- string indexing/slicing은 Unicode scalar가 아니라 byte 단위이다.
- Unicode scalar iteration은 built-in `String.chars()`를 쓰고, 복잡한 문자열 처리는
  `std.strings` helper를 쓴다.
- unsafe lookup 대신 가능하면 `get` 계열로 `Option`을 받는다.
- `defer`는 enclosing block exit에서 LIFO 실행된다.
- `defer`는 normal exit, `return`, loop exit, `?`, cancellation에서 실행된다.
  process abort/exit에서는 실행되지 않는다.

## 6. Patterns

- pattern 위치: `match`, `let`, `if let`, `for let`, closure parameter 일부.
- pattern 종류: wildcard, literal, identifier binding, tuple, struct, variant,
  range, or, `name @ pattern`.
- `let` pattern은 irrefutable destructuring만 사용한다. enum variant는 `let`
  대신 `match` 또는 `if let`.
- pattern precedence: atomic, range, binding, or.
- or-pattern alternatives는 같은 이름을 같은 타입으로 bind해야 한다.
- literal pattern은 type-strict하다. numeric coercion은 없다.
- pattern context의 `|`는 pattern-or이다. bit-or expression이 아니다.

## 7. Packages, Scripts, FFI

- directory 하나가 package 하나이다. 같은 directory의 `.osty` 파일은 namespace를
  공유한다.
- subpackage는 subdirectory이다.
- import cycle은 금지된다. diamond import는 허용된다.
- `use path`는 Osty package import.
- `use path::{A, B as C}`와 `pub use path.Symbol`은 허용된다.
- dotted path와 URL-like path를 혼합하지 않는다.
- script file은 top-level statement가 있는 파일이다.
- script top-level statement는 implicit `main() -> Result<(), Error>` 안에 있는
  것처럼 컴파일된다. v0.6: implicit `#[ambient(clock, rng, env, fs)]` 적용.
- script는 import할 수 없다.
- script top-level `?`와 `return`은 implicit `main`에 적용된다.
- script top-level `defer`는 금지된다. block 안에 둔다.
- `use go "path" [as alias] { ... }`는 Go FFI.
- FFI block에는 monomorphic function declaration과 field-only struct declaration만.
- Go `(T, error)`는 `Result<T, Error>`로 매핑된다.
- Go `panic`은 process abort. Go concrete error downcast는 없다.
- Osty closure, generic declaration, empty interface, Go channel type은 FFI에
  직접 노출하지 않는다.
- FFI 통한 데이터는 untagged 시작 — taint flow 가 필요하면 `#[trusted_declassify(reason)]`
  명시 (§10).

## 8. Errors

- `Error`는 structural interface이지만 runtime downcast를 위해 nominal type tag를
  보존하는 특별한 interface이다.
- `?`는 enclosing return type이 `Result<_, Error>`일 때 concrete error를
  `Error`로 upcast할 수 있다.
- 서로 다른 concrete error를 한 함수에서 전파하려면 `Result<_, Error>`로 넓히거나
  wrapper enum을 명시적으로 구성한다.
- `?`는 wrapper enum을 자동 합성하지 않는다.
- v0.6: concrete enum error type 에 `#[error_contract(Variant when "...")]`
  적용 가능 (§13). caller match exhaustiveness 가 contract variants 기반.
  erased `Error` 에는 `#[error_contract(any)]` (검증 없음, 문서용).

## 9. Concurrency and Capabilities

- detached spawn은 없다. 모든 task는 `taskGroup` scope에 속한다.
- `Handle<T>`와 `TaskGroup`은 non-escaping capability이다.
- handle/group을 return, field/collection 저장, channel send, escaping closure
  capture하지 않는다. 위반은 `E0743`.
- child failure는 sibling/descendant cancellation을 유발하고 첫 관측 error를
  caller에 전파한다.
- blocking stdlib call은 cancellation-aware여야 한다.
- CPU-bound code는 explicit cancellation check helper를 호출한다.
- channel capacity 0은 synchronous rendezvous, 양수는 FIFO buffer.
- channel close는 두 번 하면 abort. closed channel send도 abort.
- `recv`는 buffered value 후 closed+drained 상태에서 `None`.
- `select`는 ready branch가 있으면 `default`보다 ready branch를 우선한다.
  여러 ready branch 사이 선택은 비결정적이다.

### v0.6 Capability parameters (G36)

- 환경 effect 는 capability parameter 로만 받는다. 7 canonical interface:
  `Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process`, `Console`.
- v0.5 의 전역 함수 (`time.now()` / `random.next()` 등) 는 v0.6.x 에서
  `--legacy-globals` 호환. v0.7 제거.
- 라이브러리 함수는 capability 명시 — `#[ambient]` 금지.
- script / `fn main` / `#[test]` / `#[bench]` / `test*` / `bench*` 함수에서만 `#[ambient(name1, ...)]`
  허용. 본문 첫 위치에 default instance 자동 bind.
- ambient binding 은 *exact 이름 매칭* 으로 callee 의 capability parameter 에
  자동 forward. 불일치 시 명시 호출 필수.
- ambient 는 함수 boundary 에서 멈춤 — callee 가 capability 를 받지 않으면
  자동 주입 없음.
- closure 는 ambient 를 *값으로 capture* — 다른 함수로 넘겨도 동작.
- 사용자 정의 capability 도 가능. `#[reproducible_capability]` 로 deterministic
  capability 등록 — interface 의 모든 메서드가 `#[reproducible]` 보장.

## 10. Information Flow Tracking (G37)

- 보안 flow 를 type system 에 1-bit + N-tag tracking 으로 명시.
- `#[taint("source")]` — 함수 반환값에 source tag 부여.
- `#[sanitizes("source", into = "trust")]` — source tag 제거 + trust tag 부여.
- `#[requires("trust")]` — sink parameter 가 trust tag 요구.
- annotation 위치: function 반환 (선언 앞), parameter (Pattern 앞 또는 Type 앞).
- tag 는 generic 함수 / closure / struct field / collection element / Result/Option
  unwrap 통해 자동 propagate.
- struct 단위 fold 가 default. 필드별 narrow 는 `#[taint_field]`.
- implicit flow (control-flow 의존) 는 *추적 안 함* — explicit flow only (Jif 와 동일).
- FFI 경계 데이터는 untagged 시작. tag 제거가 필요하면 `#[trusted_declassify(reason)]` —
  audit log (`osty audit --trusted-declassify`) 로 enumerate.
- v0.6 baseline sink: `db.query` (`sql_safe`), `process.exec` (`shell_safe`),
  `fs.path*` (`path_safe`), `http.redirect` (`url_safe`), `template.render` /
  `http.respondHtml` (`html_safe`).
- v0.6 baseline sanitizer: `std.sql.escape`, `std.shell.quote`,
  `std.path.normalize`, `std.url.encode`, `std.html.escape`.

## 11. Reproducibility (G39) and Performance (G46)

- `#[reproducible(scope=...)]` — 환경독립 강제. scope 는 `"run"` / `"target"`
  (default) / `"portable"`.
- 검사: non-deterministic capability 수신 금지, unordered iter 금지, pointer-id
  비교 금지, transitive callee 도 같거나 강한 scope 필수.
- `#[pure]` 는 더 강함 — capability 수신 자체 금지.
- `#[budget(...)]` static keys — `allocs`, `io_calls`, `stack_depth`,
  `instructions`. 컴파일러가 *증명*. 위반 `E0795`.
- `#[budget(...)]` runtime keys — `time_ms`, `p99_ms`. `osty bench --budget`
  회귀 게이트.
- `#[golden(path, mode=...)]` — snapshot 비교. mode `"text"` / `"ast"` / `"json"` /
  `"diag"`. `#[golden]` 은 암묵 `#[reproducible(scope="target")]` — 위반 `E0444`.

## 12. Spec Block (G43) and Intent (G42)

- `spec { ... }` 함수 본문 첫 statement 위치. clauses: `example:`, `law:`,
  `invariant:`, `forall x in gen:` (v1).
- v0 (Phase 3): `example:` 만 `osty test --spec` 실행, `law:` / `invariant:` 는
  doc + LSP hover. `result` 는 `law:` / `invariant:` 안에서 함수 반환값 가리키는
  virtual binding.
- `#[purpose("...")]` — 자유 텍스트 의도 (doc / LSP / context).
- `#[example(input=, output=, uses=)]` — 자동 검증. `uses` 는 fixture 이름.
- `#[fixture(name=)]` — zero-arity 함수, 같은 type 의 canonical instance 제공.
- `#[spec("§X.Y")]` — markdown anchor 검증 (`E0790` if missing). doc / LSP /
  `osty explain` 에 spec 본문 인용.
- `osty context <symbol> [--format=json]` — purpose / examples / spec_refs /
  error_contract / fixtures / stability 단일 JSON 추출 (LSP / AI agent 첫
  시민).

## 13. API Evolution (G44) and Sealed Construction (G40)

- `#[since("X.Y")]` — 도입 버전 (메타데이터, 검증 없음).
- `#[stability(level, since=, until=, remove=, reason=)]` — level 은 `"stable"`
  / `"experimental"` / `"deprecated"` / `"internal"`.
- `osty publish` 가 manifest API surface diff 계산. `stable` API breaking
  change + minor/patch bump 시 `E2100`. compat-add + patch only `E2102`.
  `experimental` 변경은 warning `W2100`.
- `#[match_compat("X.Y", fallback=name)]` — match 식이 과거 enum shape 에 pin.
  새 variant 추가 시 자동 fallback. silent fallthrough 금지 (`E0450`) — `fallback`
  또는 `unsafe_silent = true` 명시 필수 (후자는 `W0902`).
- `#[sealed_construct(name)]` — struct 의 외부 literal 생성 차단. `name` 이 가리키는
  constructor (또는 `#[trusted_construct]` stdlib helper) 만 생성 가능.
  `#[test_construct]` 는 test profile 에서만 우회.
- v0.6 stdlib sealed types: `Email`, `Url`, `Path`, `SqlIdent`, `Duration`,
  `Uuid`. `--legacy-construct` 호환 모드 v0.6.x 한정.

## 14. Annotations Catalog (v0.6, +20 from v0.5)

| 카테고리 | Annotation | 영역 |
|---|---|---|
| **Existing (v0.5)** | `#[json]`, `#[deprecated]`, `#[op]`, `#[cfg]`, `#[test]`, `#[intrinsic]`, `#[pod]`, `#[repr]`, `#[export]`, `#[c_abi]`, `#[no_alloc]` | 기존 |
| **Capability (G36)** | `#[ambient]`, `#[reproducible_capability]` | §9 |
| **Information flow (G37)** | `#[taint]`, `#[sanitizes]`, `#[requires]`, `#[trusted_declassify]`, `#[taint_field]` | §10 |
| **Spec / intent (G38, G42)** | `#[spec]`, `#[purpose]`, `#[example]`, `#[fixture]` | §12 |
| **Construction (G40)** | `#[sealed_construct]`, `#[trusted_construct]`, `#[test_construct]` | §13 |
| **Error (G41)** | `#[error_contract]` | §8 |
| **Determinism (G39)** | `#[reproducible]` | §11 |
| **Evolution (G44)** | `#[since]`, `#[stability]`, `#[match_compat]` | §13 |
| **Testing / perf (G45, G46)** | `#[golden]`, `#[budget]` | §11 |

각 annotation 의 합법 위치는 `LANG_SPEC_v0.6/00-revision.md §7.6` 의 표가
권위. 잘못된 위치는 `E0405`. annotation 인자는 항상 literal — 표현식 불가.

## 15. Protocols and Runtime Model

- iteration은 `Iterable<T>.iter() -> Iterator<T>`와 `Iterator<T>.next() -> T?`.
- `for ... in` 대상은 `Iterable<T>`를 만족해야 한다.
- I/O EOF는 `Ok(0)`이다. distinguished EOF error는 없다.
- `ToString`이 string interpolation과 print family의 기반이다.
- interpolation format specifier 문법은 없다. 필요한 formatting은 method로 한다.
- Osty는 garbage-collected이다. GC 세부 알고리즘과 tuning은 spec에 고정되지 않는다.
- `new`, `delete`, destructor, finalizer, weak reference는 없다.
- resource cleanup은 `defer` 또는 closure-scoped stdlib helper로 한다.
- allocation failure는 recoverable error가 아니라 abort이다.
- reference cycle은 GC가 회수해야 한다.

## 16. Tests and Tooling

- test file suffix는 `_test.osty`.
- lowercase `test`로 시작하고 argument가 없는 function은 test.
- inline `#[test] fn`도 test로 수집되고 production build에서는 제외된다.
- lowercase `bench`로 시작하고 argument가 없는 function은 benchmark.
- test는 production build에서 제외된다.
- `std.testing` assertion은 compiler-known이다. 일반 macro 기능은 아니다.
- test order는 기본 randomized/parallel이다. order에 의존하지 않는다.
- `beforeEach`/`afterEach`는 없다. helper, `testing.context`, `defer`를 쓴다.
- formatter는 설정이 없다. 생성 후 `osty fmt`, `osty check`, 필요하면 `osty lint`.
- manifest는 `osty.toml`, lockfile은 `osty.lock`.
- v0.6 신규 명령: `osty context <symbol>` (G47), `osty validate-spec` (G38),
  `osty publish` (G44), `osty test --golden` / `--update-golden` (G45),
  `osty test --spec` (G43), `osty test --example` (G42), `osty bench --budget`
  (G46), `osty audit --trusted-declassify | --trusted-construct | --match-compat`.

## 17. Do Not Generate

- `null`, `nil`, exceptions, `try`, `catch`, panic recovery.
- inheritance, class, `impl`, macro, user-defined annotation.
- function overloading, `[]`/`()`/bitwise/comparison operator overloading.
- C-style `for`. (v0.6 부터 `while` 은 허용.)
- detached spawn, `async`, `await`, `WaitGroup`.
- lifetime annotation, variance annotation, generic parameter default.
- implicit narrowing/lossy numeric conversion, `as` conversion keyword.
- set literal.
- run-time `const` binding; use top-level `let`. `const fn` is allowed.
- `unsafe`; use FFI declarations.
- `where` clause; put constraints directly on type parameters.
- expression annotation or `use` annotation.
- v0.6: 라이브러리 함수에 `#[ambient]` 금지. capability parameter 명시.
- v0.6: 사용자 코드에 `#[trusted_construct]` / `#[trusted_declassify]` 금지
  (stdlib 또는 audit-marked 코드만).
- v0.6: stdlib sealed types (`Email` / `Url` / `Path` / `SqlIdent` / `Duration` /
  `Uuid`) 의 외부 struct literal 금지 — `Type.parse(...)` 사용.
- v0.6: 전역 effect 함수 (`time.now()` / `random.next()` / `env.get(k)` /
  `fs.read(p)` / `os.exec(...)` / `net.dial(...)`) 는 baseline 거부 (`E0780`).
  capability parameter 명시 또는 entry-point `#[ambient]` 만 허용.
  `--legacy-globals` flag 는 v0.6.x transition 한정 — v0.7 제거.

## 18. Agent Checklist

- No semicolons, no trailing-dot chains, no newline before `else`.
- Use `T?`, not `Option<T>`, in formatted output.
- Use `for cond` 또는 `while cond` (v0.6 동의어, §4.4). C-style for 는 금지.
- Use `match`/`if let` for enum destructuring, not plain `let`.
- Parenthesize struct literals in control-flow heads.
- Use `::<T>` only on calls and never with an empty type-argument list.
- Do not store generic functions or generic methods as values.
- Do not rely on default/keyword args after a callable becomes `fn(...) -> ...`.
- Keep `?` propagation within `Result` or within `Option`; convert explicitly across them.
- Keep task handles inside their `taskGroup`.
- Prefer safe `get` methods when missing/out-of-range should be recoverable.
- v0.6: 라이브러리 코드에선 capability parameter 명시 — testability 강화.
- v0.6: SQL/shell/path/url/html 데이터는 sanitizer 경유 후 sink 도달.
- v0.6: stdlib sealed types 직접 literal 금지 — `Type.parse(...)`.
- v0.6: 메타데이터가 풍부한 함수에 `#[purpose]` / `#[example]` / `#[spec]` 부착.
- Run formatter/checker after edits.
