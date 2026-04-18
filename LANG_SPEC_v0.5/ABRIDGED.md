# Osty v0.5 Agent Quick Spec

AI 에이전트가 짧게 읽고 바로 Osty 코드를 생성·수정하기 위한 규칙 카드.
예제는 싣지 않는다. 충돌 시 `LANG_SPEC_v0.5/`와 `OSTY_GRAMMAR_v0.5.md`가
우선한다.

## 1. File and Lexing

- 파일은 `.osty`, UTF-8, newline은 `\n`; `\r\n`은 정규화된다.
- shebang은 byte offset 0에서만 한 번 허용된다.
- 예약어: `fn struct enum interface type let mut pub if else match for break
  continue return use defer`.
- 문맥 식별자: `self Self true false Some None Ok Err`.
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
- overloading은 없다.
- default parameter는 trailing parameter에만 허용된다.
- default value는 literal 계열, `None`, literal payload의 `Ok`/`Err`, empty
  collection, unit만 가능하다.
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
- compiler annotation은 고정 집합이다. v0.4는 `#[json]`, `#[deprecated]`.
  annotation은 named declaration 앞에만 둔다.

## 3. Types

- primitive: signed/unsigned integer family, `Byte`, float family, `Bool`,
  `Char`, `String`, `Bytes`, `Never`.
- `Int`는 항상 64-bit signed. machine-word `UInt`는 없다.
- `String`은 immutable UTF-8 bytes. `Bytes`는 immutable byte sequence.
- composite: `struct`, `enum`, `interface`, tuple, function type, `List<T>`,
  `Map<K, V>`, `Set<T>`, `Option<T>`, `Result<T, E>`.
- `T?`는 `Option<T>` sugar. formatter는 `Option<T>`를 `T?`로 정규화한다.
- `null`/`nil`은 없다. 부재는 `Option<T>`/`T?`.
- alias는 transparent하다. 새 nominal type이 아니다.
- 변수 사이 numeric conversion은 암묵적으로 일어나지 않는다.
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
- `for expr`은 while-style loop.
- bare `for`는 infinite loop.
- `for let pattern = expr`은 match 성공 동안 반복한다.
- `break`/`continue`는 innermost loop에만 적용된다. label은 없다.
- `Type { ... }` struct literal은 `if`/`match`/`for`/`if let`/`for let` head에서
  괄호로 감싼다.
- assignment는 statement이다. expression으로 쓰지 않는다.
- channel send `<-`도 statement이다.
- postfix `?`는 `Result<T, E>`와 `Option<T>`에만 적용된다.
- `?`는 enclosing return type과 같은 family로만 전파한다. `Option`과 `Result`
  family를 직접 섞지 않는다.
- `?.`는 `Option<T>` field/method access를 short-circuit한다.
- `??`는 left가 `None`일 때만 right를 평가한다.
- closure는 capture by reference이다. capture mutability는 binding 선언을 따른다.
- closure parameter pattern은 irrefutable `LetPattern`만 허용된다. refutable
  literal/range/variant/or pattern은 `E0741`.
- member access와 method call은 `.`만 사용한다.
- string indexing/slicing은 Unicode scalar가 아니라 byte 단위이다.
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
- dotted path와 URL-like path를 혼합하지 않는다.
- script file은 top-level statement가 있는 파일이다.
- script top-level statement는 implicit `main() -> Result<(), Error>` 안에 있는
  것처럼 컴파일된다.
- script는 import할 수 없다.
- script top-level `?`와 `return`은 implicit `main`에 적용된다.
- script top-level `defer`는 금지된다. block 안에 둔다.
- `use go "path" [as alias] { ... }`는 Go FFI.
- FFI block에는 monomorphic function declaration과 field-only struct declaration만.
- Go `(T, error)`는 `Result<T, Error>`로 매핑된다.
- Go `panic`은 process abort. Go concrete error downcast는 없다.
- Osty closure, generic declaration, empty interface, Go channel type은 FFI에
  직접 노출하지 않는다.

## 8. Errors

- `Error`는 structural interface이지만 runtime downcast를 위해 nominal type tag를
  보존하는 특별한 interface이다.
- `?`는 enclosing return type이 `Result<_, Error>`일 때 concrete error를
  `Error`로 upcast할 수 있다.
- 서로 다른 concrete error를 한 함수에서 전파하려면 `Result<_, Error>`로 넓히거나
  wrapper enum을 명시적으로 구성한다.
- `?`는 wrapper enum을 자동 합성하지 않는다.

## 9. Concurrency

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

## 10. Protocols and Runtime Model

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

## 11. Tests and Tooling

- test file suffix는 `_test.osty`.
- lowercase `test`로 시작하고 argument가 없는 function은 test.
- lowercase `bench`로 시작하고 argument가 없는 function은 benchmark.
- test는 production build에서 제외된다.
- `std.testing` assertion은 compiler-known이다. 일반 macro 기능은 아니다.
- test order는 기본 randomized/parallel이다. order에 의존하지 않는다.
- `beforeEach`/`afterEach`는 없다. helper, `testing.context`, `defer`를 쓴다.
- formatter는 설정이 없다. 생성 후 `osty fmt`, `osty check`, 필요하면 `osty lint`.
- manifest는 `osty.toml`, lockfile은 `osty.lock`.

## 12. Do Not Generate

- `null`, `nil`, exceptions, `try`, `catch`, panic recovery.
- inheritance, class, `impl`, macro, user-defined annotation.
- operator/function overloading.
- `while`, `loop`, C-style `for`.
- labelled `break`/`continue`.
- detached spawn, `async`, `await`, `WaitGroup`.
- lifetime annotation, variance annotation, generic parameter default.
- implicit numeric conversion, `as` conversion keyword.
- set literal, anonymous record/struct.
- `const`; use top-level `let`.
- `unsafe`; use FFI declarations.
- `where` clause; put constraints directly on type parameters.
- expression annotation or `use` annotation.

## 13. Agent Checklist

- No semicolons, no trailing-dot chains, no newline before `else`.
- Use `T?`, not `Option<T>`, in formatted output.
- Use `for`, not `while` or C-style loops.
- Use `match`/`if let` for enum destructuring, not plain `let`.
- Parenthesize struct literals in control-flow heads.
- Use `::<T>` only on calls and never with an empty type-argument list.
- Do not store generic functions or generic methods as values.
- Do not rely on default/keyword args after a callable becomes `fn(...) -> ...`.
- Keep `?` propagation within `Result` or within `Option`; convert explicitly across them.
- Keep task handles inside their `taskGroup`.
- Prefer safe `get` methods when missing/out-of-range should be recoverable.
- Run formatter/checker after edits.
