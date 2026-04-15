# Osty Grammar — Rules & EBNF (v0.3)

v0.1 에서 열린 이슈 O1~O7 (v0.2 에서 해결) + v0.3 에서 추가된 grammar
확장을 반영한 최종본. R1~R26 에 O1~O7 결정 및 v0.3 변경을 통합하여
단일 규범 문서로 재구성.

---

## 결정 이력 (v0.1 → v0.2 → v0.3)

### v0.2 → v0.3

- **R25 확장**: `ClosureParam ::= LetPattern (':' Type)?` — closure
  파라미터가 `LetPattern` (튜플/struct destructure) 을 수용. 단
  refutable 패턴은 컴파일 에러. 현행 참조 파서는 `IDENT (':' Type)?`
  만 지원하며, 스펙이 선행한다.
- 다른 grammar rule 변경 없음. LANG_SPEC v0.3 에서 모든 6개 open gap
  (G4, G8–G12) 과 91건의 의미론 모호성이 결정됨 — `LANG_SPEC_v0.3/
  18-change-history.md` §18.1 참조.

### v0.1 → v0.2

| 이슈 | 결정 |
|---|---|
| O2: `} else` 줄바꿈 | **불허** (Go/Rust 스타일). `} else {` 한 줄 강제. |
| O3: 메서드 체인 `.` 위치 | **선행 `.` 강제**. 후행 `.` syntax error. |
| O4: `>>` 토큰 분할 | **splittable `>`** (Rust 방식). 타입 파서가 `>>`를 `>` + `>`로 쪼갬. |
| O5: `_` 토큰 | **`UNDERSCORE` 별도 토큰**. maximal munch로 `_foo`는 `IDENT`. |
| O6: `::` 엄격성 | **turbofish 전용**. `::` 다음 `<` 없으면 에러. |
| O7: `=>` 토큰 | **완전 제거**. |
| O1: 어노테이션 | **제한적 집합**, Rust 스타일 `#[...]`, v0.9 세트 = `#[json]` + `#[deprecated]`. |

---

## Part 1. 규칙 (Rules)

### R1. 연산자 우선순위 & 결합성

높은 → 낮은.

| 레벨 | 연산자 | 결합성 | 비고 |
|---|---|---|---|
| 14 | `.` `?.` `?`(postfix) `()` `[]` `::<T>` | 좌 | postfix |
| 13 | `-` `!` `~` (단항) | — | prefix |
| 12 | `*` `/` `%` | 좌 | |
| 11 | `+` `-` | 좌 | |
| 10 | `<<` `>>` | 좌 | |
| 9  | `&` | 좌 | bit-and |
| 8  | `^` | 좌 | bit-xor |
| 7  | `\|` | 좌 | bit-or |
| 6  | `..` `..=` | **non-assoc** | |
| 5  | `<` `>` `<=` `>=` `==` `!=` | **non-assoc** | |
| 4  | `&&` | 좌 | |
| 3  | `\|\|` | 좌 | |
| 2  | `??` | 우 | |
| 1  | `=` `+=` `-=` `*=` `/=` `%=` `&=` `\|=` `^=` `<<=` `>>=` | 우 | **statement-only** |

- 비교/범위 non-associative: `a < b < c` 금지.
- 할당은 표현식이 아닌 문: `let x = (y = 1)` 금지.
- `<-` 채널 송신은 문 전용.
- `->`는 구문 구두점 (함수 반환 타입, match arm), 연산자 아님.

### R2. 자동 세미콜론 삽입 (ASI)

렉서는 물리적 newline에서 다음 경우 **제외하고** `NEWLINE`을
`TERMINATOR`로 승격.

**억제 조건 — 다음 중 하나라도 참이면 newline 소멸**:

1. **직전 토큰**이: 이항 연산자(좌측 피연산자 필요), `,`, `->`, `<-`,
   `::`, `@`, `|`(pattern-or), `(`, `[`, `{`.
   - (O3 결정) 직전 토큰이 `.` 또는 `?.`인 경우는 **억제하지 않음** →
     후행 `.` 체인 스타일 문법 에러.
2. **다음 비공백 토큰**이: `)`, `]`, `}`, `.`, `?.`, `,`, `..`, `..=`,
   이항 연산자(좌측 피연산자 필요).
   - (O2 결정) `else`는 **포함하지 않음** → `}\n else` 문법 에러.

줄 연속(`\` at EOL) 없음. 삼중 따옴표·블록 주석 내부 newline은 토큰 아님.

### R3. 구조체 리터럴 제한 문맥

`Type { ... }` 형태 금지 위치 (괄호로 감싸야 함):

- `if` 조건식
- `for x in <scrutinee>` 의 scrutinee
- `for <condition>` (while-style) 의 condition
- `match <scrutinee>` 의 scrutinee
- `if let P = <rhs>` 의 rhs
- `for let P = <rhs>` 의 rhs

괄호 `(Type { ... })`로 감싸면 허용.

### R4. 제네릭 인자 — 터보피시 전용 (O6 통합)

- **표현식 위치**: `expr::<T, U>(args)` 한 형태만. `expr<T>(args)`는
  비교-비교로 파싱됨.
- **타입 위치**: `Name<T, U>` 허용 (모호성 없음).
- `::` 다음 토큰은 **반드시 `<`**. 아니면 파서 에러:
  ```
  expected '<' after '::', got 'X'. Did you mean '.'?
  ```

### R5. `..` 역할 분리

| 위치 | `..` 의미 | `..=` 의미 |
|---|---|---|
| 표현식 | 배타 범위 | 포함 범위 |
| 패턴 (단독) | rest 마커 | — |
| 패턴 (이항) | 범위 패턴 (배타) | 범위 패턴 (포함) |
| struct 리터럴 | spread-update (최대 1회) | — |
| struct 패턴 | rest (최대 1회) | — |

렉서 토큰은 각각 `DOTDOT`, `DOTDOTEQ` 단일. 의미는 파서 문맥.

### R6. 패턴의 `|`

패턴 문맥(match arm, `let`, `if let`, `for let` LHS, 함수 파라미터 패턴은
없음)에서 `|`는 항상 **패턴-OR**. 비트-OR은 패턴 내부 표현 불가 — 필요
시 match guard의 `if` 절에서 사용.

### R7. 키워드 vs 문맥 식별자

**예약어 17개** (§1.2): 식별자로 사용 불가.
```
fn struct enum interface type
let mut pub
if else match
for break continue return
use defer
```

**문맥 식별자**: 렉서 관점에서 일반 `IDENT`, 파서가 문맥에서 해석.
- `self`, `mut self`: 메서드 첫 파라미터 위치 전용.
- `Self`: 타입 위치에서 둘러싼 타입.
- `true`, `false`, `Some`, `None`, `Ok`, `Err`: prelude 값 참조.

### R8. 문자열 보간 렉싱

렉서가 문자열을 토큰 스트림으로 분해:

```
STRING_START    "                          (또는 """ )
STR_PART        hi,
INTERP_START    {
  ... 표현식 토큰 스트림 ...
INTERP_END      }
STR_PART
STRING_END      "
```

중첩 중괄호는 스택으로 추적. `r"..."`, `r"""..."""`은 `INTERP_START`
발생시키지 않음 (보간 없음).

### R9. Triple-quoted 인덴트 처리

렉서에서 처리. 문자열 수집 완료 후 공통 인덴트 제거·검증. 위반은
lex-time 에러.

규칙 (§1.6.3):
1. 여는 `"""` 뒤 즉시 newline 필수.
2. 닫는 `"""`는 자체 줄. 앞 공백이 공통 인덴트.
3. 모든 content line은 최소 공통 인덴트로 시작. 위반 시 에러.
4. 닫는 `"""` 직전 newline 제거.

### R10. Shebang

파일 byte offset 0에서 `#!`로 시작하는 경우만. 해당 줄(개행 포함) 폐기.
그 외 위치의 `#`는 어노테이션 시작 (O1), 또는 에러.

### R11. 숫자 리터럴

- 접미사 없음. 타입은 문맥 추론.
- 언더스코어는 숫자 사이만: `1_000`, `0xFF_FF`. 선행/후행/연속/진수
  접두사 직후 금지.
- `0b`, `0o`, `0x` 소문자만.
- Float: `.` 양쪽에 숫자 필수 (`1.0` OK, `1.` `.5` 금지). 지수 `1e10` OK.
- 16진 숫자 `A-F`는 대소문자 모두 허용.

### R12. 트레일링 콤마

모든 콤마 구분 구성에서 허용. **예외**: 1-요소 튜플 `(x,)`의 콤마는
**필수** (그룹화 괄호와 구별).

### R13. `self` 파라미터

- `self` (immutable reference via 참조 시맨틱).
- `mut self` (필드 변형 허용).
- 메서드의 **첫 파라미터 위치 전용**. 타입 주석 없음.
- `&self`, `&mut self` 형태 없음.

### R14. 패턴 우선순위

높은 → 낮은:
1. Atomic: literal, identifier, `_`, `(...)`, struct, variant.
2. Range: `a..=b`, `a..b`.
3. Binding: `ident @ Pattern`.
4. Or: `P1 | P2 | P3` (좌결합, lowest).

중첩: `(A | B) | C` ≡ `A | B | C`.

### R15. `use` 경로 문법

```
UsePath := DottedPath | UrlishPath
```

- `DottedPath`: `IDENT ('.' IDENT)*` — std/로컬.
- `UrlishPath`: 최소 1개 `/`, 첫 세그먼트에 `.` 포함 가능 (도메인).
- 배타적. 혼합 금지.

### R16. `use go "..."` 블록

허용 선언:
- `fn Name(params) -> ReturnType` (본문 없음).
- `struct Name { field: Type, ... }` (필드만, 메서드 불가).

금지: 제네릭, 기본값, enum, interface, type, 본문 있는 fn, 중첩 use.

### R17. 함수 본문 필수성

| 위치 | 본문 |
|---|---|
| 일반 `fn` | 필수 |
| `interface` 내 `fn` | 선택 (기본 구현) |
| `use go` 내 `fn` | 금지 |

### R18. 기본 인자 제약

```ebnf
DefaultExpr ::= Literal
              | '-' (INT_LIT | FLOAT_LIT)
              | 'None'
              | 'Ok' '(' Literal ')'
              | 'Err' '(' Literal ')'
              | '[' ']'
              | '{' ':' '}'
              | '(' ')'
```

임의 표현식 거부. 파서 수준에서 즉시.

### R19. 파셜 선언

`struct`/`enum`: 같은 패키지 여러 파일에서 같은 이름 선언 허용.
- 필드/variant: 정확히 한 선언.
- 메서드: 여러 선언에 분산, 이름 중복 금지.
- 가시성·타입 파라미터 완전 일치.

Grammar 영향 없음. 각 선언 독립 파싱 → semantic 단계에서 병합.

### R20. 키워드 인자

호출 시 `name: expr`. 파서는 `:` 유무로 positional/keyword 구분.
순서 (positional 선행) 등은 semantic 검증.

### R21. 채널 송신

`ch <- value` 는 문 전용. 표현식 아님.

### R22. `defer` 피연산자

`defer Expr | defer Block`. Grammar 수준에서 임의 `Expr` 허용, semantic에서
"call-like" 검증.

### R23. `?` postfix

Postfix 전용. prefix/infix 없음. 타입 위치의 `?`(`User?`)는 별개 규칙.

### R24. 튜플 vs 괄호식

| 형태 | 의미 |
|---|---|
| `()` | unit |
| `(expr)` | 그룹핑 |
| `(expr,)` | 1-요소 튜플 (콤마 필수) |
| `(e1, e2)` | 2-요소 튜플 |
| `(e1, e2,)` | 2-요소 튜플 (트레일링 콤마) |

### R25. 클로저 파라미터

v0.3 부터 closure 파라미터는 일반 `LetPattern` 수용.

```ebnf
ClosureParam ::= LetPattern (':' Type)?
```

- `|x|`, `|x: Int|`, `|x, y|`, `|x: Int, y: Int| -> Int` 모두 유효 —
  `IDENT` 는 `LetPattern` 의 부분집합.
- 튜플 destructure: `|(k, v)|`.
- struct destructure: `|User { name, age }|`, `|User { name, .. }|`.
- 와일드카드: `|(_, second)|`.
- **Irrefutable 제한**: `|Some(x)|`, `|0..=9|` 등 refutable 패턴은
  컴파일 에러 (파서는 허용하되 semantic 단계에서 거절).
- 반환 타입 명시 시 본문 블록 필수. 없으면 단일 표현식 또는 블록 모두
  허용.

> **Parser status (v0.3).** 현행 참조 파서는 R25 의 v0.2 형식
> (`ClosureParam ::= IDENT (':' Type)?`) 만 구현. `LetPattern` 확장은
> 후속 PR. 사용자는 수동 destructure 형식을 사용:
>
> ```osty
> |pair| { let (k, v) = pair; ... }
> ```

### R26. 어노테이션 (O1 통합)

문법: `#[Name AnnotArgs?]`. 선언 앞에 0개 이상.

**v0.9 허용 집합** (컴파일러 고정 세트, 사용자 정의 불가):

**`#[json(...)]`** — 구조체 필드 전용.
- `key = "<name>"` — JSON 키명 재매핑.
- `skip` — 직렬화/역직렬화 양쪽 제외.
- `optional` — `None`일 때 필드 생략 (`T?` 필드 전용).
- 여러 인자 조합 가능.

**`#[deprecated(...)]`** — top-level `fn`/`struct`/`enum`/`interface`/
`type`/`let`, 그리고 struct/enum 내부 메서드.
- `since = "<version>"` (선택).
- `use = "<replacement>"` (선택, 대체 API 힌트).
- `message = "<text>"` (선택).

**에러 처리**: 허용 목록 외 어노테이션은 컴파일 에러:
```
error: unknown annotation '#[inline]'.
       permitted annotations: json, deprecated.
```

**위치 검증**: `#[json]`을 함수 앞에 붙이는 등 잘못된 대상은 컴파일 에러:
```
error: '#[json]' is only allowed on struct fields.
```

---

## Part 2. EBNF 문법

W3C-EBNF 유사 표기. 터미널은 `'...'`, 렉서 정규식은 `/regex/`.

### 2.1 렉서 규칙

```ebnf
(* 무시되거나 소비되는 요소 *)
Whitespace    ::= /[ \t]+/
LineComment   ::= '//' /[^\n]*/
BlockComment  ::= '/*' /([^*]|\*+[^*/])*/ '*/'    (* non-nesting *)
DocComment    ::= '///' /[^\n]*/                   (* 파서 소비 *)
Shebang       ::= '#!' /[^\n]*/ '\n'               (* byte offset 0 only *)

(* 문장 종결 *)
NEWLINE       ::= '\n'
TERMINATOR    ::= NEWLINE                          (* R2 ASI 후 *)

(* 식별자 & 와일드카드 *)
IDENT         ::= /[A-Za-z_][A-Za-z0-9_]*/         (* maximal munch *)
UNDERSCORE    ::= '_'                              (* 단독만, IDENT와 배타 *)

KEYWORD       ::= 'fn' | 'struct' | 'enum' | 'interface' | 'type'
                | 'let' | 'mut' | 'pub'
                | 'if' | 'else' | 'match'
                | 'for' | 'break' | 'continue' | 'return'
                | 'use' | 'defer'

(* 리터럴 *)
INT_LIT       ::= DEC_INT | HEX_INT | BIN_INT | OCT_INT
DEC_INT       ::= /[0-9]([0-9_]*[0-9])?/
HEX_INT       ::= /0x[0-9A-Fa-f]([0-9A-Fa-f_]*[0-9A-Fa-f])?/
BIN_INT       ::= /0b[01]([01_]*[01])?/
OCT_INT       ::= /0o[0-7]([0-7_]*[0-7])?/
FLOAT_LIT     ::= /[0-9]([0-9_]*[0-9])?\.[0-9]([0-9_]*[0-9])?([eE][+-]?[0-9]+)?/
                | /[0-9]([0-9_]*[0-9])?[eE][+-]?[0-9]+/
CHAR_LIT      ::= "'" CharBody "'"
BYTE_LIT      ::= "b'" AsciiCharBody "'"
STRING_LIT    ::= InterpStringStream               (* §R8 *)
                | RawString                         (* r"..." *)
                | TripleString                      (* """...""" *)
                | RawTripleString                   (* r"""...""" *)

(* 연산자 & 구두점 *)
PUNCT         ::= '(' | ')' | '[' | ']' | '{' | '}'
                | ',' | ':' | '::' | '.' | '?.' | '?' | '@'
                | '->' | '<-'
                | '..' | '..='
                | '+' | '-' | '*' | '/' | '%'
                | '==' | '!=' | '<' | '>' | '<=' | '>='
                | '&&' | '||' | '!'
                | '&' | '|' | '^' | '~' | '<<' | '>>'
                | '=' | '+=' | '-=' | '*=' | '/=' | '%='
                | '&=' | '|=' | '^=' | '<<=' | '>>='
                | '??' | '#'
```

**렉서 주의사항**:

- `_` 단독 vs `_foo`: maximal munch — 더 긴 `IDENT` 매치 우선. 결과적으로
  `_` 뒤에 `[A-Za-z0-9_]`가 있으면 `IDENT`, 없으면 `UNDERSCORE`.
- `>`, `>=`, `>>`, `>>=`는 렉서에서 최대 match로 뽑고, **타입 파서가
  `>` 기대 위치에서 splittable** (O4).
- `::` 다음 `<` 없으면 파서 에러 (R4/O6).
- `#`는 어노테이션 시작. shebang은 byte 0 + `#!`만, 중복 없음.
- `=>`는 렉서에서 인식 안 함 (O7 제거).

### 2.2 컴파일 단위

```ebnf
File          ::= Shebang? TERM* (TopLevel (TERM+ TopLevel)*)? TERM*
TERM          ::= TERMINATOR                       (* ASI 후 *)

TopLevel      ::= Annotation* TopLevelDecl
                | TopLevelStmt                     (* 스크립트 전용 *)
TopLevelDecl  ::= UseDecl
                | FnDecl
                | StructDecl
                | EnumDecl
                | InterfaceDecl
                | TypeAliasDecl
                | LetDecl
TopLevelStmt  ::= Stmt

(* 어노테이션 *)
Annotation    ::= '#' '[' IDENT AnnotationArgs? ']'
AnnotationArgs ::= '(' AnnotationArg (',' AnnotationArg)* ','? ')'
AnnotationArg ::= IDENT                            (* flag *)
                | IDENT '=' Literal                (* 키=값 *)
```

### 2.3 Use 선언

```ebnf
UseDecl       ::= 'use' (OstyUse | GoUse)

OstyUse       ::= UsePath ('as' IDENT)?
UsePath       ::= DottedPath | UrlishPath
DottedPath    ::= IDENT ('.' IDENT)*
UrlishPath    ::= DomainSeg '/' PathSeg ('/' PathSeg)*
DomainSeg     ::= IDENT ('.' IDENT)+
PathSeg       ::= IDENT

GoUse         ::= 'go' STRING_LIT ('as' IDENT)? '{' TERM*
                    (GoDecl (TERM+ GoDecl)*)? TERM*
                  '}'
GoDecl        ::= GoFnDecl | GoStructDecl
GoFnDecl      ::= 'fn' IDENT '(' GoParamList? ')' ('->' Type)?
GoParamList   ::= GoParam (',' GoParam)* ','?
GoParam       ::= IDENT ':' Type
GoStructDecl  ::= 'struct' IDENT '{' TERM*
                    (GoField (',' TERM* GoField)*)? ','? TERM*
                  '}'
GoField       ::= IDENT ':' Type
```

### 2.4 타입

```ebnf
Type          ::= TypePostfix
TypePostfix   ::= TypeAtom ('?')*
TypeAtom      ::= TypePath TypeArgs?
                | TupleType
                | FnType
                | '(' Type ')'

TypePath      ::= IDENT ('.' IDENT)*               (* 'Self' 포함 가능 *)
TypeArgs      ::= '<' Type (',' Type)* ','? '>'   (* splittable '>' — O4 *)

TupleType     ::= '(' ')'
                | '(' Type ',' ')'
                | '(' Type (',' Type)+ ','? ')'

FnType        ::= 'fn' '(' TypeList? ')' '->' Type
TypeList      ::= Type (',' Type)* ','?
```

### 2.5 함수 선언

```ebnf
FnDecl        ::= 'pub'? 'fn' IDENT GenericParams?
                  '(' ParamList? ')' ('->' Type)? Block

GenericParams ::= '<' GenericParam (',' GenericParam)* ','? '>'
GenericParam  ::= IDENT (':' TypeBound)?
TypeBound     ::= TypePath TypeArgs? ('+' TypePath TypeArgs?)*

ParamList     ::= SelfParam (',' Param)* ','?
                | Param (',' Param)* ','?
SelfParam     ::= 'mut'? 'self'
Param         ::= IDENT ':' Type ('=' DefaultExpr)?

DefaultExpr   ::= Literal
                | '-' (INT_LIT | FLOAT_LIT)
                | 'None'
                | 'Ok' '(' Literal ')'
                | 'Err' '(' Literal ')'
                | '[' ']'
                | '{' ':' '}'
                | '(' ')'
```

### 2.6 Struct / Enum / Interface

```ebnf
StructDecl    ::= 'pub'? 'struct' IDENT GenericParams? '{' TERM*
                    StructMembers? TERM*
                  '}'
StructMembers ::= StructMember (MemberSep StructMember)*
StructMember  ::= Annotation* FieldDecl
                | Annotation* MethodDecl
MemberSep     ::= ',' TERM* | TERM+

FieldDecl     ::= 'pub'? IDENT ':' Type ('=' DefaultExpr)?
MethodDecl    ::= 'pub'? 'fn' IDENT GenericParams?
                  '(' ParamList? ')' ('->' Type)? Block

EnumDecl      ::= 'pub'? 'enum' IDENT GenericParams? '{' TERM*
                    EnumMembers? TERM*
                  '}'
EnumMembers   ::= EnumMember (MemberSep EnumMember)*
EnumMember    ::= Annotation* VariantDecl
                | Annotation* MethodDecl
VariantDecl   ::= IDENT ('(' TypeList ')')?

InterfaceDecl ::= 'pub'? 'interface' IDENT GenericParams? '{' TERM*
                    IfaceMembers? TERM*
                  '}'
IfaceMembers  ::= IfaceMember (TERM+ IfaceMember)*
IfaceMember   ::= SuperIface
                | IfaceMethod
SuperIface    ::= TypePath TypeArgs?
IfaceMethod   ::= 'fn' IDENT GenericParams? '(' ParamList? ')'
                  ('->' Type)? Block?              (* Block = default impl *)
```

### 2.7 Type Alias & Let

```ebnf
TypeAliasDecl ::= 'pub'? 'type' IDENT GenericParams? '=' Type

LetDecl       ::= 'pub'? 'let' 'mut'? LetPattern (':' Type)? '=' Expr
LetPattern    ::= IDENT
                | UNDERSCORE
                | '(' LetPattern (',' LetPattern)* ','? ')'
                | TypePath '{' StructPatFields '}'
StructPatFields ::= FieldPat (',' FieldPat)* (',' '..')? ','?
                  | '..'
FieldPat      ::= IDENT                            (* shorthand *)
                | IDENT ':' LetPattern
```

### 2.8 문 (Statement)

```ebnf
Stmt          ::= LetDecl
                | AssignStmt
                | SendStmt
                | DeferStmt
                | ReturnStmt
                | BreakStmt
                | ContinueStmt
                | ExprStmt

AssignStmt    ::= AssignTarget AssignOp Expr
AssignTarget  ::= PostfixExpr                      (* 파서가 lvalue 검증 *)
                | '(' AssignTarget (',' AssignTarget)* ','? ')'
                                                   (* multi-assignment *)
AssignOp      ::= '=' | '+=' | '-=' | '*=' | '/=' | '%='
                | '&=' | '|=' | '^=' | '<<=' | '>>='

SendStmt      ::= Expr '<-' Expr
DeferStmt     ::= 'defer' (Expr | Block)
ReturnStmt    ::= 'return' Expr?
BreakStmt     ::= 'break'
ContinueStmt  ::= 'continue'
ExprStmt      ::= Expr
```

### 2.9 블록 & 표현식

```ebnf
Block         ::= '{' TERM* (Stmt (TERM+ Stmt)*)? TERM* '}'

(* 표현식은 할당 제외 — 할당은 Stmt 레벨 *)
Expr          ::= NilCoalesceExpr

NilCoalesceExpr  ::= LogicalOrExpr ('??' NilCoalesceExpr)?
LogicalOrExpr    ::= LogicalAndExpr ('||' LogicalAndExpr)*
LogicalAndExpr   ::= CompareExpr ('&&' CompareExpr)*
CompareExpr      ::= RangeExpr (CompareOp RangeExpr)?       (* non-assoc *)
CompareOp        ::= '==' | '!=' | '<' | '>' | '<=' | '>='
RangeExpr        ::= BitOrExpr (('..' | '..=') BitOrExpr)?  (* non-assoc *)
                   | ('..' | '..=') BitOrExpr               (* prefix open *)
                   | BitOrExpr ('..' | '..=')               (* postfix open *)
BitOrExpr        ::= BitXorExpr ('|' BitXorExpr)*
BitXorExpr       ::= BitAndExpr ('^' BitAndExpr)*
BitAndExpr       ::= ShiftExpr ('&' ShiftExpr)*
ShiftExpr        ::= AddExpr (('<<' | '>>') AddExpr)*
AddExpr          ::= MulExpr (('+' | '-') MulExpr)*
MulExpr          ::= UnaryExpr (('*' | '/' | '%') UnaryExpr)*
UnaryExpr        ::= ('-' | '!' | '~') UnaryExpr
                   | PostfixExpr
PostfixExpr      ::= PrimaryExpr PostfixOp*
PostfixOp        ::= '.' IDENT                    (* field/method name *)
                   | '?.' IDENT
                   | '?'                           (* propagate *)
                   | '(' ArgList? ')'              (* call *)
                   | '[' Expr ']'                  (* index *)
                   | '[' RangeExpr ']'             (* slice *)
                   | '::' TypeArgs                 (* turbofish — '<' 필수 *)

PrimaryExpr      ::= Literal
                   | IDENT
                   | 'self'
                   | StructLit                     (* 제한 문맥 외 *)
                   | TupleOrParenExpr
                   | ListLit
                   | MapLit
                   | IfExpr
                   | MatchExpr
                   | ForExpr
                   | Block
                   | ClosureExpr

StructLit        ::= TypePath '{' StructLitBody? '}'
StructLitBody    ::= '..' Expr (',' FieldInit)* ','?
                   | FieldInit (',' FieldInit)* (',' '..' Expr)? ','?
FieldInit        ::= IDENT ':' Expr
                   | IDENT                         (* shorthand *)

TupleOrParenExpr ::= '(' ')'                       (* unit *)
                   | '(' Expr ',' ')'              (* 1-tuple *)
                   | '(' Expr ')'                  (* grouping *)
                   | '(' Expr (',' Expr)+ ','? ')' (* n-tuple *)

ListLit          ::= '[' ']'
                   | '[' Expr (',' Expr)* ','? ']'
MapLit           ::= '{' ':' '}'
                   | '{' MapEntry (',' MapEntry)* ','? '}'
MapEntry         ::= Expr ':' Expr

ArgList          ::= Arg (',' Arg)* ','?
Arg              ::= IDENT ':' Expr                (* keyword *)
                   | Expr                          (* positional *)
```

### 2.10 제어 흐름 표현식

```ebnf
IfExpr        ::= 'if' IfHead Block ('else' (IfExpr | Block))?
                | 'if' 'let' LetPattern '=' RestrictedExpr Block
                  ('else' (IfExpr | Block))?
IfHead        ::= RestrictedExpr

MatchExpr     ::= 'match' RestrictedExpr '{' TERM*
                    (MatchArm (ArmSep MatchArm)*)? ArmSep? TERM*
                  '}'
ArmSep        ::= ',' TERM*
MatchArm      ::= Pattern ('if' Expr)? '->' (Expr | Block)

ForExpr       ::= 'for' ForHead Block
                | 'for' 'let' LetPattern '=' RestrictedExpr Block
                | 'for' Block                      (* infinite *)
ForHead       ::= LetPattern 'in' RestrictedExpr
                | RestrictedExpr                   (* while-style *)

(* R3: StructLit이 금지된 식 위치. 파서가 flag로 관리 *)
RestrictedExpr ::= Expr                            (* StructLit 거부 *)
```

### 2.11 클로저

```ebnf
ClosureExpr   ::= '|' ClosureParams? '|' ClosureBody
ClosureParams ::= ClosureParam (',' ClosureParam)* ','?
ClosureParam  ::= LetPattern (':' Type)?         (* v0.3; parser still IDENT-only *)
ClosureBody   ::= '->' Type Block                  (* 반환 타입 시 블록 필수 *)
                | Block
                | Expr
```

### 2.12 패턴

```ebnf
Pattern       ::= OrPattern
OrPattern     ::= BindPattern ('|' BindPattern)*
BindPattern   ::= IDENT '@' RangePattern
                | RangePattern
RangePattern  ::= AtomPattern (('..=' | '..') AtomPattern)?
                | ('..=' | '..') AtomPattern
AtomPattern   ::= UNDERSCORE
                | Literal
                | IDENT
                | TypePath
                | TypePath '(' PatList? ')'
                | TypePath '{' FieldPatList? '}'
                | '(' PatList? ')'
PatList       ::= Pattern (',' Pattern)* ','?
FieldPatList  ::= FieldPat (',' FieldPat)* (',' '..')? ','?
                | '..'
```

### 2.13 리터럴

```ebnf
Literal       ::= INT_LIT
                | FLOAT_LIT
                | STRING_LIT
                | CHAR_LIT
                | BYTE_LIT
                | 'true'
                | 'false'
```

---

## Part 3. 다음 단계

1. **본 문서 리뷰** — R1~R26 결정 사항 교차 검증.
2. 스펙 `LANG_SPEC_v0.3/` 와의 정합 — **완료** (v0.3 통합):
   - v0.2 에서 O1–O7 정합 완료 ✓
   - v0.3 에서 G4/G8/G9/G10/G11/G12 + 91건 의미론 모호성 전부 결정 ✓
   - 자세한 변경 내역은 `LANG_SPEC_v0.3/18-change-history.md` 참조.

   **스펙 디렉토리 구조** (v0.2 부터 단일 파일 → 폴더):
   - `LANG_SPEC_v0.3/README.md` — 인덱스 + 읽기 순서.
   - `LANG_SPEC_v0.3/NN-<name>.md` — chapter §N (N = 1..9, 11..18).
   - `LANG_SPEC_v0.3/10-standard-library/` — §10 의 stdlib 서브패키지별
     파일 (NN-<name>.md, N = 1..20) + chapter README.
3. **테스트 코퍼스 작성** — 각 grammar 규칙당 positive/negative 최소 1쌍.
   - 특히 R2 ASI, R3 제한 문맥, R4 turbofish, R5 `..` 다의성, O4 `>>` 분할.
4. **파서 구현 선택**:
   - 수제 recursive-descent (Rust 구현) — O4 splittable `>` 구현 간단.
   - 또는 `lalrpop`/`chumsky` 등 콤비네이터 — O4 특수 처리 필요.
5. **LSP 초안** — 토큰 정의는 본 문서의 렉서 규칙을 그대로 사용 가능.

> 본 문서는 `LANG_SPEC_v0.3/` 의 §15 / §16 / §17 (Iteration / I/O /
> Display 프로토콜) 와 함께 단일 정본을 구성한다. 사양 충돌이 발견되면
> `LANG_SPEC_v0.3/` 가 우선한다 (구현·예제 의미론 측면). 토큰·문법
> 규칙은 본 문서가 정본.
