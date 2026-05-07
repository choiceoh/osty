# Osty Grammar — Rules & EBNF (v0.6)

v0.5 의 R1–R26 결정과 EBNF 를 baseline 으로, v0.6 에서 추가/변경된 rule
만 본 문서에 명시한다. v0.5 grammar 의 본문은
[`OSTY_GRAMMAR_v0.5.md`](./OSTY_GRAMMAR_v0.5.md) 가 계속 권위.

> **Status**: v0.6 spec revision 동기 (G36–G50). spec 본문은
> [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md).

---

## 결정 이력 (v0.5 → v0.6)

### 새 reserved keyword 1 개

- **`while`** (G49) — `while cond { }` 형식의 conditional loop. v0.5 의
  `for cond { }` 와 동의어 — 둘 다 같은 lowering. 식별자로 사용했던
  코드는 rename 필요 (사용자 0 단계에서의 breaking change).

### 새 contextual keyword 4 개 (v1 단계는 5 개)

- **`spec`** — 함수 본문 첫 statement 위치에서 spec block 도입.
- **`example`** — `spec { example: ... }` 절 도입.
- **`law`** — `spec { law: ... }` 절 도입.
- **`invariant`** — `spec { invariant: ... }` 절 도입.
- **`forall`** *(v1 — Phase 5)* — `spec { forall x in gen: ... }` 형식.

이 키워드들은 spec block 안 또는 spec block 시작 위치에서만 keyword 로
파싱된다. 그 외 위치에서는 식별자.

### 새 lexer 토큰 0 개

신규 syntax 는 모두 기존 token 조합으로 표현. anonymous record literal
`{ x: 1, y: 2 }` 는 기존 `{`, `}`, `:` 재사용.

### Annotation set 확장 +20

§5 of [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md)
참조 — 31 개 fixed annotation. `#[name(args)]` 형식 그대로.

신규 annotation parameter position 1 곳 — `ParamDecl` 의 `Pattern` 앞과
`Type` 앞 둘 다에 `Annotation*` 허용 (v0.5 까지는 둘 다 불허).

```ebnf
ParamDecl ::= Annotation* Pattern ':' Annotation* Type ('=' DefaultExpr)?
```

용례:
```osty
fn handler(
    #[taint("user_input")]      // before Pattern — 함수 측이 source 표시
    form: String,

    table: #[requires("sql_safe")]    // before Type — caller 가 sink 보장
           SqlIdent,
) -> ... { ... }
```

두 위치는 *의미 동일* — 컴파일러가 어느 쪽이든 해당 파라미터에 적용.
스타일은 sender / receiver 의도 표현 — `#[taint]` 는 *반환* 의도이므로
Pattern 앞이 자연, `#[requires]` 는 *제약* 이므로 Type 앞이 자연.

### EBNF 변경 요약 (자세한 production 은 [00-revision.md §5](./LANG_SPEC_v0.6/00-revision.md))

| 영역 | v0.5 | v0.6 | 변경 |
|---|---|---|---|
| `WhileStmt` | (없음) | `'while' Expr Block` | G49 신규 |
| `SpecBlock` | (없음) | spec block + 4 clause | G43 신규 |
| `AnonymousRecordType` | (없음) | `'{' RecordTypeField+ '}'` | G50 신규 |
| `AnonymousRecordValue` | (없음) | `'{' RecordValueField+ '}'` | G50 신규 |
| `ParamDecl` | `Pattern ':' Type ('=' DefaultExpr)?` | `Annotation* Pattern ':' Annotation* Type ('=' DefaultExpr)?` | G37 — annotation positions |
| `Annotation` rule | 11 fixed names | 31 fixed names | G37/G38/G39/G40/G41/G42/G43/G44/G45/G46 |

### Grammar 규모 변화

| 항목 | v0.4 | v0.5 | v0.6 | Δ (v0.5→v0.6) |
|---|---:|---:|---:|---:|
| Reserved keywords | 17 | 18 | **19** | +1 |
| Contextual keywords | 7 | 10 | **14** | +4 (`forall` 은 v1 추가 시 15) |
| Fixed annotation set | 8 | 11 | **31** | +20 |
| EBNF productions | 180 | 191 | **203** | +12 |
| Lexer token classes | 34 | 36 | **36** | 0 |
| Diagnostic bands used | E0001-E0779 + E0405 | 동일 | E0001-E0949 + E2149 + W0949 | +5 bands |

---

## R-rule 변경 / 추가

기존 R1–R26 은 v0.6 에서도 유효. v0.6 은 R27–R30 4 개 추가.

### R27. `while` 키워드 (G49)

`while` 은 fully reserved. parser 에서 `'while' Expr Block` 를
`for cond { Block }` 와 동등하게 lower. expression / type / control flow
의미 완전 동일. 두 form 은 *style choice*.

### R28. spec block 위치 (G43)

`spec { ... }` block 은 함수 본문의 *첫 statement* 위치에만 허용. 그
외 위치는 `E0440`. block 은 expression 이 아니다 — 결과 type 없음, 마지막
expression 평가 안 됨. spec block 본문은 *순수 메타데이터*: `example:`
는 test runner 가 따로 실행, `law:` / `invariant:` 는 doc generator 가
추출.

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

v0.5 의 R7 는 contextual keyword 7 개 (`self`, `Self`, `true`, `false`,
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

## 새 R-rule 인덱스 표

| 번호 | 이름 | 영역 |
|---|---|---|
| R1–R26 | (v0.5 와 동일) | — |
| R27 | `while` 키워드 | G49 |
| R28 | spec block 위치 | G43 |
| R29 | anonymous record vs block 모호성 | G50 |
| R30 | annotation 위치 확장 (parameter) | G37 |

---

## 진단 코드 신규 할당 — Grammar 영역

| 코드 | 의미 |
|---|---|
| `E0340` | Anonymous record field 에 annotation/method 시도 |
| `E0341` | Anonymous record self-recursive type |
| `E0342` | Block expression 과 anonymous record literal 모호 |
| `E0440` | `spec` block 이 함수 본문 첫 위치가 아님 |
| `E0441` | `spec { example: }` 가 boolean 으로 평가되지 않음 |
| `E0442` | `spec { law:/invariant: }` 가 boolean 으로 평가되지 않음 |
| `E0443` | `spec { forall }` 의 generator 가 `Gen<T>` 가 아님 (v1) |
| `E0405` | Annotation 이 잘못된 위치 (annotation matrix 기반) |

기타 G36–G48 의 의미론적 진단 코드는 [00-revision.md §6](./LANG_SPEC_v0.6/00-revision.md) 참조.
