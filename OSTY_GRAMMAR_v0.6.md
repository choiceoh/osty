# Osty Grammar — Rules & EBNF (v0.6)

v0.5 의 R1–R26 결정과 EBNF 를 baseline 으로, v0.6 에서 추가/변경된 rule
만 본 문서에 명시한다. v0.5 grammar 의 본문은
[`OSTY_GRAMMAR_v0.5.md`](./OSTY_GRAMMAR_v0.5.md) 가 계속 권위.

> **Status**: v0.6 spec revision 동기 (G36–G50). spec 본문은
> [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md).

---

## 결정 이력 (v0.5 → v0.6)

### 새 reserved keyword 1 개 (R27)

- **`while`** (G49) — `while cond { }` 형식의 conditional loop. v0.5 의
  `for cond { }` 와 동의어 — 둘 다 같은 lowering. 식별자로 사용했던 코드는
  rename 필요 (사용자 0 단계에서의 acceptable break).

### 새 contextual keyword 4 개 (R28)

- **`spec`** — 함수 본문 첫 statement 위치에서만 keyword. 그 외 위치
  식별자.
- **`example`** — `spec { }` block 안에서만 keyword.
- **`law`** — `spec { }` block 안에서만 keyword.
- **`invariant`** — `spec { }` block 안에서만 keyword.
- **`forall`** *(v1 — Phase 5)* — `spec { }` block 안에서만 keyword. v0
  단계는 식별자.

### 새 lexer 토큰 0 개

신규 syntax 는 모두 기존 token 조합으로 표현. `{`, `}`, `:`, `,` 재사용.

### Annotation set 확장 +20

§5 of [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md)
참조 — 31 개 fixed annotation 으로 확장. `#[name(args)]` 형식 그대로.

### Annotation parameter position (R30)

`ParamDecl` 의 두 위치에 `Annotation*` 신규 허용:
- Pattern 앞 (caller-facing intent: source 표시)
- Type 앞 (type-modifier intent: trust requirement)

---

## EBNF — 전체 신규 production

### G43 — spec block

```ebnf
SpecBlock      ::= 'spec' '{' SpecClause+ '}'

SpecClause     ::= ExampleClause
                 | LawClause
                 | InvariantClause
                 | ForallClause            (* v1 — Phase 5 *)

ExampleClause  ::= 'example' ':' Expr LineEnd

LawClause      ::= 'law' ':' Expr LineEnd

InvariantClause::= 'invariant' ':' Expr LineEnd

ForallClause   ::= 'forall' Ident (',' Ident)* 'in' Expr ':' Expr LineEnd

LineEnd        ::= NEWLINE
                 | (다음 SpecClause 시작 token 직전 — ASI 적용)
```

위치 제약 (R28):
- `SpecBlock` 은 함수 본문의 *첫 statement* 위치에만 출현 가능.
- `'spec'` 토큰은 그 위치에서만 keyword. 그 외 위치 (예: `let spec = 1`)
  에서는 일반 식별자.
- `'example'` / `'law'` / `'invariant'` 는 `SpecBlock` 본문 안에서만
  keyword.

### G49 — while loop

```ebnf
WhileStmt      ::= 'while' Expr Block

(* 의미: 'while' Expr Block ≡ 'for' Expr Block — same lowering, same type *)
```

`'while'` 은 fully reserved (R27). 식별자 자리에 사용 불가.

### G50 — anonymous structural record

```ebnf
AnonymousRecordType  ::= '{' RecordTypeField (',' RecordTypeField)* ','? '}'
RecordTypeField      ::= IDENT ':' Type

AnonymousRecordValue ::= '{' RecordValueField (',' RecordValueField)* ','? '}'
RecordValueField     ::= IDENT (':' Expr)?       (* shorthand, struct literal 과 같음 *)
```

R29 의 disambiguation 규칙:

```
1. Type 위치 (let `: T`, fn return type, fn param type) — 항상 record type
2. Value 위치, type ascription 있음 — 항상 record value (struct literal 우선
   적용 안 됨, AnonymousRecordValue 로 파싱)
3. Value 위치, type ascription 없음 — lookahead:
   `'{' IDENT ':'` 패턴이면 record value
   그 외 (e.g., `'{' Stmt`) 는 block expression
4. 모호 시 `: T` ascription 추가 권장 (E0342 hint)
```

`AnonymousRecordType` / `AnonymousRecordValue` 의 *필드 안에는 annotation
허용 안 함* (E0340) — annotation 이 필요하면 nominal struct 사용.

### G37 — annotation on parameter

```ebnf
(* 변경 전 v0.5 *)
ParamDecl_v05  ::= Pattern ':' Type ('=' DefaultExpr)?

(* v0.6 *)
ParamDecl      ::= Annotation* Pattern ':' Annotation* Type ('=' DefaultExpr)?
                   (* Annotation* 두 위치 신규 — Pattern 앞, Type 앞 *)
```

의미는 *두 위치 동일* — 컴파일러가 annotation 을 해당 parameter 에 적용.
스타일: source 의도 (`#[taint]`) 는 Pattern 앞, sink 의도
(`#[requires]`) 는 Type 앞 권장.

```
fn handler(
    #[taint("user_input")] form: String,    // Pattern 앞 — source
    table: #[requires("sql_safe")] SqlIdent,  // Type 앞 — sink
) -> ... { ... }
```

### G36 — capability annotation

```ebnf
(* 기존 Annotation rule 재사용. 새 annotation name 만 추가. *)

CapabilityAnnotation ::= '#[ambient' '(' AmbientArg (',' AmbientArg)* ')' ']'

AmbientArg          ::= IDENT
                        (* IDENT 가 미리 정의된 capability name 중 하나여야 *)
                        (* 'clock', 'rng', 'env', 'fs', 'net', 'process', 'console' *)

ReproducibleCapAnno ::= '#[reproducible_capability' ']'
                        (* interface 선언에만 적용 *)
```

### G37 — taint annotation

```ebnf
TaintAnno     ::= '#[taint' '(' StringLit ')' ']'
SanitizeAnno  ::= '#[sanitizes' '(' StringLit ',' 'into' '=' StringLit ')' ']'
RequiresAnno  ::= '#[requires' '(' StringLit ')' ']'
DeclassifyAnno::= '#[trusted_declassify' '(' 'reason' '=' StringLit ')' ']'
TaintFieldAnno::= '#[taint_field' '(' StringLit ')' ']'
```

### G38 — spec link

```ebnf
SpecAnno      ::= '#[spec' '(' StringLit ')' ']'
                  (* StringLit 는 markdown anchor (예: "§2.2", "§10.30.user") *)
```

### G39 — reproducibility

```ebnf
ReproAnno     ::= '#[reproducible' ('(' 'scope' '=' ReproScope ')')? ']'
ReproScope    ::= '"run"' | '"target"' | '"portable"'
                  (* 미지정 시 default = "target" *)
```

### G40 — sealed construct

```ebnf
SealedAnno    ::= '#[sealed_construct' '(' IDENT ')' ']'
                  (* IDENT 는 같은 type 의 method 이름 *)
TrustedConstAnno ::= '#[trusted_construct' '(' 'reason' '=' StringLit ')' ']'
TestConstAnno    ::= '#[test_construct' ']'
```

### G41 — error contract

```ebnf
ErrContractAnno  ::= '#[error_contract' '(' ErrContractEntry (',' ErrContractEntry)* ','? ')' ']'
                   | '#[error_contract' '(' 'any' ')' ']'

ErrContractEntry ::= QualifiedVariantPath 'when' StringLit

QualifiedVariantPath ::= IDENT ('.' IDENT)*    (* 예: "EmailError.Format" *)
```

### G42 — structured intent

```ebnf
PurposeAnno  ::= '#[purpose' '(' StringLit ')' ']'

ExampleAnno  ::= '#[example' '(' ExampleArg (',' ExampleArg)* ','? ')' ']'
ExampleArg   ::= 'input' '=' StringLit_or_Expr
               | 'output' '=' StringLit_or_Expr
               | 'uses' '=' StringLit             (* fixture name *)

FixtureAnno  ::= '#[fixture' '(' 'name' '=' StringLit ')' ']'
                 (* 함수에만 적용 가능, 인자 0 개, 반환 type 명시 필수 *)
```

### G44 — API evolution

```ebnf
SinceAnno    ::= '#[since' '(' StringLit ')' ']'
                 (* StringLit 는 SemVer "X.Y" 또는 "X.Y.Z" *)

StabilityAnno::= '#[stability' '(' StabilityLevel (',' StabilityArg)* ','? ')' ']'

StabilityLevel    ::= '"stable"' | '"experimental"' | '"deprecated"' | '"internal"'

StabilityArg ::= 'until' '=' StringLit
               | 'since' '=' StringLit
               | 'remove' '=' StringLit
               | 'reason' '=' StringLit

MatchCompatAnno ::= '#[match_compat' '(' StringLit (',' MatchCompatArg)* ','? ')' ']'

MatchCompatArg ::= 'fallback' '=' IDENT          (* fallback 함수 이름 *)
                 | 'unsafe_silent' '=' BoolLit   (* true 시 silent fallthrough 명시 *)
                 | 'reason' '=' StringLit
```

### G45 — golden tests

```ebnf
GoldenAnno   ::= '#[golden' '(' StringLit (',' GoldenArg)* ','? ')' ']'

GoldenArg    ::= 'mode' '=' GoldenMode
GoldenMode   ::= '"text"' | '"ast"' | '"json"' | '"diag"'
                 (* 미지정 시 default = "text" *)
```

### G46 — performance contract

```ebnf
BudgetAnno   ::= '#[budget' '(' BudgetField (',' BudgetField)* ','? ')' ']'

BudgetField  ::= 'allocs' '=' IntLit
               | 'io_calls' '=' IntLit
               | 'stack_depth' '=' IntLit
               | 'instructions' '=' IntLit
               | 'time_ms' '=' (IntLit | FloatLit)
               | 'p99_ms' '=' (IntLit | FloatLit)

(* 같은 어노테이션에 static (allocs/io_calls/stack_depth/instructions) 와
   runtime (time_ms/p99_ms) 키가 혼재 가능 — 컴파일러가 분리 처리 *)
```

### Annotation 위치 enforcement

각 annotation 의 합법 위치는 `LANG_SPEC_v0.6/00-revision.md §7.6` 의 표가
권위. 그 표에 위반하면 `E0405` (annotation site invalid).

---

## R-rule 추가

### R27. `while` 키워드 (G49)

`while` 은 fully reserved keyword. parser 에서 `'while' Expr Block` 를
`'for' Expr Block` 와 동등하게 lower. expression / type / control flow
의미 완전 동일. v0.5 의 `for cond {}` 도 deprecate 안 함 — 양쪽 모두
컴파일러가 같은 IR 생성.

### R28. spec block 위치 (G43)

`spec { ... }` block 은 함수 본문의 *첫 statement* 위치에만 허용. 그
외 위치는 `E0440`. block 은 expression 이 아니다 — 결과 type 없음, 마지막
expression 평가 안 됨. spec block 본문은 *순수 메타데이터*: `example:`
는 test runner 가 따로 실행, `law:` / `invariant:` 는 doc generator 가
추출.

`'spec'` keyword 는 그 위치에서만 keyword 로 lex. 그 외 위치 (예: 변수
이름, struct field 이름) 에서는 식별자.

### R29. anonymous record vs block 모호성 (G50)

`{ x: 1, y: 2 }` 가 block expression 인지 anonymous record literal
인지의 모호성 해소 규칙:

1. **Type 위치**: 항상 `AnonymousRecordType`. e.g. `let r: { x: Int, y:
   Int } = ...`.
2. **함수 반환 type 위치**: 항상 `AnonymousRecordType`.
3. **Value 위치, type ascription 있음**: 항상 `AnonymousRecordValue`.
   e.g. `let r: { x: Int, y: Int } = { x: 1, y: 2 }`.
4. **Value 위치, type ascription 없음**: parser 가 *lookahead* — `{`
   다음 `IDENT ':'` 패턴이면 record value, 그 외는 block.
5. **모호한 경우** (예: lambda `|| { x: 1 }` 가 block 인지 record 반환
   인지): block 으로 파싱 — `: Int` 가 statement-level annotation 아님.
   Record 의도를 강제하려면 type ascription 추가.
6. **mismatched 위치는 명확한 진단**: type ascription 없이 모호한 자리
   에서 record value 의도였으면 `E0342` (suggestion: `: T` 추가).

### R30. annotation 위치 확장 (G37)

`Annotation*` 가 새로 허용되는 위치:

- Function parameter `Pattern` 앞 (v0.5 까지는 declaration-only)
- Function parameter `Type` 앞 (동일)
- Anonymous record field 에는 annotation **불허** (`E0340`)
- Tuple 요소에는 annotation **불허** (모호성)

기존 declaration 위치 (top-level fn/struct/enum/interface, struct
field, enum variant) 는 변경 없음.

---

## R7 보강 — 키워드 vs 문맥 식별자 (v0.6 갱신)

v0.5 의 R7 는 contextual keyword 7+ 개 (`self`, `Self`, `true`, `false`,
`Some`, `None`, `Ok`, `Err`, `loop`, `const`, `by`) 를 정의. v0.6 은:

- **추가 contextual**: `spec`, `example`, `law`, `invariant` — spec
  block 컨텍스트에서만 keyword.
- **추가 reserved**: `while` — 모든 위치에서 keyword.
- **`forall`**: v1 (Phase 5) 단계에서 contextual keyword 추가. v0
  단계에서는 식별자 그대로.

R7 에 다음 추가:

> **spec block scope**: `spec { ... }` block 안에서 `example`, `law`,
> `invariant` 는 keyword. 그 외 위치 (block 외부) 에서는 식별자.

> **block 시작 위치**: 함수 본문 첫 statement 위치에서 `spec` 가 keyword.
> 그 외 위치에서는 식별자. R28 참조.

---

## Grammar 규모 변화

| 항목 | v0.4 | v0.5 | v0.6 | Δ (v0.5→v0.6) |
|---|---:|---:|---:|---:|
| Reserved keywords | 17 | 18 | **19** | +1 (`while`) |
| Contextual keywords | 7 | 10 | **14** | +4 (`forall` 은 v1 단계 추가 시 15) |
| Fixed annotation set | 8 | 11 | **31** | +20 |
| EBNF productions | 180 | 191 | **203** | +12 |
| Lexer token classes | 34 | 36 | **36** | 0 |
| Diagnostic bands used | E0001-E0779 + E0405 | 동일 | E0001-E0949 + E2149 + W0949 | +5 bands |

---

## 새 R-rule 인덱스 표

| 번호 | 이름 | 영역 |
|---|---|---|
| R1–R26 | (v0.5 와 동일) | — |
| R27 | `while` 키워드 | G49 |
| R28 | spec block 위치 + `spec`/`example`/`law`/`invariant` 컨텍스트 | G43 |
| R29 | anonymous record vs block 모호성 | G50 |
| R30 | annotation 위치 확장 (parameter 두 위치) | G37 |

---

## 진단 코드 — Grammar 영역 신규 할당

| 코드 | 의미 |
|---|---|
| `E0340` | Anonymous record field 에 annotation/method 시도 |
| `E0341` | Anonymous record self-recursive type |
| `E0342` | Block expression 과 anonymous record literal 모호 |
| `E0405` | Annotation 이 잘못된 위치 (annotation matrix 기반) |
| `E0440` | `spec` block 이 함수 본문 첫 위치가 아님 |
| `E0441` | `spec { example: }` 가 boolean 으로 평가되지 않음 |
| `E0442` | `spec { law:/invariant: }` 가 boolean 으로 평가되지 않음 |
| `E0443` | `spec { forall }` 의 generator 가 `Gen<T>` 가 아님 (v1) |

기타 G36–G48 의 의미론적 진단 코드는
[00-revision.md §6](./LANG_SPEC_v0.6/00-revision.md) 참조.

---

## EBNF 통합 (v0.5 baseline + v0.6 delta)

본 문서의 v0.6 production 들을 v0.5 production 의 *위에* 병합한 *통합
grammar* 는 별도 파일 `OSTY_GRAMMAR_v0.6_FULL.md` (Phase 1 종료 시 생성)
참조 — 본 문서는 *delta only*.
