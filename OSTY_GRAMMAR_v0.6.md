# Osty Grammar — Rules & EBNF (v0.6)

v0.5 의 R1–R26 결정과 EBNF 를 baseline 으로, v0.6 에서 추가/변경된 rule
만 본 문서에 명시한다. v0.5 grammar 의 본문은
[`OSTY_GRAMMAR_v0.5.md`](./OSTY_GRAMMAR_v0.5.md) 가 계속 권위.

> **Status**: v0.6 spec revision 동기 (G36, G37, G39-G42, G44, G45,
> G47, G48). spec 본문은
> [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md).
>
> **Withdrawn from v0.6 baseline**: G38 (spec link), G43 (spec block),
> G46 (`#[budget]`), G49 (while keyword) — pre-release low-utility
> withdrawal. SPEC_GAPS.md 의 Withdrawn 섹션 참조.

---

## 결정 이력 (v0.5 → v0.6)

### 새 reserved keyword 0 개

(G49 `while` keyword 는 withdrawn — reserved keyword 17 → 17 변경 없음.)

### 새 v0 contextual keyword 0 개

(G43 spec block contextual keywords `spec` / `example` / `law` /
`invariant` 는 withdrawn.)

### 새 lexer 토큰 0 개

신규 syntax 는 모두 기존 token 조합으로 표현. `{`, `}`, `:`, `,` 재사용.

### Annotation set 확장 +16

§5 of [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md)
참조 — 27 개 fixed annotation 으로 확장. `#[name(args)]` 형식 그대로.

### Annotation parameter position (R29)

`ParamDecl` 의 두 위치에 `Annotation*` 신규 허용:
- Pattern 앞 (caller-facing intent: source 표시)
- Type 앞 (type-modifier intent: trust requirement)

---

## EBNF — 전체 신규 production

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

### Annotation 위치 enforcement

각 annotation 의 합법 위치는 `LANG_SPEC_v0.6/00-revision.md §7.6` 의 표가
권위. 그 표에 위반하면 `E0405` (annotation site invalid).

---

## R-rule 추가

### R27. annotation 위치 확장 (G37)

`Annotation*` 가 새로 허용되는 위치:

- Function parameter `Pattern` 앞 (v0.5 까지는 declaration-only)
- Function parameter `Type` 앞 (동일)
- Tuple 요소에는 annotation **불허** (모호성)

기존 declaration 위치 (top-level fn/struct/enum/interface, struct
field, enum variant) 는 변경 없음.

---

## R7 보강 — 키워드 vs 문맥 식별자 (v0.6 갱신)

v0.5 의 R7 는 contextual keyword 11 개 (`self`, `Self`, `true`, `false`,
`Some`, `None`, `Ok`, `Err`, `loop`, `const`, `by`) 를 정의. v0.6 은
변경 없음 — withdrawn G43 의 spec block contextual keywords 와 G49 의
`while` reserved keyword 는 baseline 에 포함되지 않는다.

R7 에 추가되는 항목:

> **label vs char literal**: lexer 는 single quote 다음이 one scalar or
> escape + closing quote 이면 `CHAR_LIT`, single quote 다음이 identifier 이고
> 즉시 closing quote 가 아니면 `LABEL` 로 토큰화한다. 따라서 `'X'` 는 char,
> `'X:` 는 label prefix 이다.

---

## Grammar 규모 변화

| 항목 | v0.4 | v0.5 | v0.6 | Δ (v0.5→v0.6) |
|---|---:|---:|---:|---:|
| Reserved keywords | 17 | 17 | **17** | 0 |
| Contextual keywords | 7 | 11 | **11** | 0 |
| Fixed annotation set | 8 | 11 | **27** | +16 |
| EBNF productions | 180 | 191 | **192** | +1 (parameter annotation) |
| Lexer token classes | 34 | 36 | **36** | 0 |
| Diagnostic bands used | E0001-E0779 + E0405 | 동일 | E0001-E0949 + E2149 + W0949 | +5 bands |

---

## 새 R-rule 인덱스 표

| 번호 | 이름 | 영역 |
|---|---|---|
| R1–R26 | (v0.5 와 동일) | — |
| R27 | annotation 위치 확장 (parameter 두 위치) | G37 |

---

## 진단 코드 — Grammar 영역 신규 할당

| 코드 | 의미 |
|---|---|
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
