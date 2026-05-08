# Osty v0.6 — Revision Document

> **Status**: 제안 (draft). 14 개 결정 (G36–G49) 을 v0.5 baseline 위에 추가하는 spec 개정.
> v0.5 의 모든 결정은 v0.6 에서도 유효. 본 문서는 *delta* 만 기술하며, 변경 없는 챕터는
> [`../LANG_SPEC_v0.5/`](../LANG_SPEC_v0.5/) 가 계속 권위.
>
> **Companion**: [`SPEC_GAPS.md`](../SPEC_GAPS.md) §"Resolved in v0.6", [`OSTY_GRAMMAR_v0.6.md`](../OSTY_GRAMMAR_v0.6.md), [`CHANGELOG_v0.6.md`](../CHANGELOG_v0.6.md).

## 0. Overview

v0.5 의 외부 사용 corpus 와 셀프호스트 운영 (100 PR / 4 일 sprint) 에서 도출된 13 개
*hidden-dependency-surface* 결정 + 1 개 *ergonomics* 정정 (`while` 키워드) 을 v0.6 에
batch 로 수용한다. 사용자 0 인 단계의 마지막 큰 surface revision — 이후 v0.7 부터는
stable API rule (G44) 이 적용된다.

v0.5 의 §14 *anonymous structural record* 금지 정책은 그대로 유지된다 — ad-hoc
labeled data 는 nominal `struct` 또는 tuple 로 표현한다.

| G | 영역 | 한 줄 |
|---|---|---|
| G36 | Capabilities (§20) | 환경 effect 를 capability 값으로 명시 |
| G37 | Information flow (§21) | `#[taint]` / `#[sanitizes]` 정적 IFC |
| G38 | Spec link (§3.10) | `#[spec("§X.Y")]` checked spec ↔ impl 링크 |
| G39 | Reproducibility (§3.11) | `#[reproducible(scope=...)]` 환경독립 강제 |
| G40 | Construction discipline (§3.4.5) | `#[sealed_construct]` parse-don't-validate |
| G41 | Error contract (§7.5) | `#[error_contract(... when ...)]` failure mode 명세 |
| G42 | Structured intent (§3.12) | `#[purpose]` / `#[example]` / `#[fixture]` |
| G43 | Executable spec (§3.13) | `spec { example: / law: / invariant: }` 블록 |
| G44 | API evolution (§3.14) | `#[since]` / `#[stability]` / `#[match_compat]` |
| G45 | Golden tests (§11.5) | `#[golden]` AST-aware 스냅샷 |
| G46 | Performance contract (§3.15) | `#[budget(allocs/io/time)]` static + runtime 분리 |
| G47 | Machine-readable context (§13.4) | `osty context <symbol>` 구조화 추출 |
| G48 | Annotation surface | 위 신규 어노테이션의 grammar 통합 |
| **G49** | `while` keyword (§4.4) | `for cond {}` 와 동의어. mental-model 일치 |

> **G49 design review note**: G36–G48 (13 개) 가 직전 사용 corpus 분석에서
> 합의된 결정. **G49 는 본 spec 작성 과정에서 추가된 ergonomics 정정**으로
> 작은 surface (1 keyword, 새 의미 0) 라 포함. 한때 같이 검토됐던 *G50 anonymous
> structural record* 는 v0.5 §14 의 "named types are nominal" discipline 유지를
> 위해 *제외*. ad-hoc labeled data 는 nominal `struct` 또는 tuple 로.

## 1. Design North Star — *Hidden Dependency Is Forbidden*

v0.5 까지의 결정이 (a) safety-by-construction, (b) type system simplicity, (c) grammar
discipline 세 축이었다면, v0.6 은 네 번째 축을 추가한다:

> **모든 hidden dependency 는 surface 로 끌어올린다 — 시간, 난수, 환경, 보안 흐름,
> 진화 규칙, 성능 계약, 의도, 명세 — 어느 것도 "암묵"으로 두지 않는다.**

이 원칙으로 v0.6 의 13 결정이 4 카테고리로 묶인다:

| 카테고리 | 명시 대상 | 결정 |
|---|---|---|
| **Effectful** | 런타임 환경 의존 (시간/난수/IO) | G36 (Capability), G39 (Reproducible) |
| **Security** | 정보 흐름 (sources → sinks) | G37 (Taint/Sanitize) |
| **Temporal** | API 진화 / 호환성 | G44 (Since/Stability/MatchCompat) |
| **Intent + Determinism** | 의도, 명세, 성능, 실패 양태 | G38 (Spec), G40 (SealedConstruct), G41 (ErrorContract), G42 (Intent), G43 (SpecBlock), G45 (Golden), G46 (Budget), G47 (Context) |

**Capability (G36) 은 base layer.** G39 / G37 / G46 의 검사가 capability 시그니처 위에서
*allow-list 기반*으로 sound 해진다. 따라서 구현 순서는 G36 → G38 → G39 → G37 순.

## 2. Implementation Phases

스펙 결정은 v0.6 baseline 으로 한 번에 동결하지만, 구현은 5 단계로 분리한다.
각 phase 종료 시 `spec corpus`, `STDLIB_MATRIX`, `CHANGELOG_v0.6` 갱신.

```
Phase 1   G36 Capability + #[ambient] + stdlib capability migration
          (가장 큰 리팩터, base layer 이므로 선행 필수)
Phase 2   G38 #[spec("§X.Y")] + G42 #[fixture] / #[purpose] / #[example]
          + G47 osty context (작고 dogfood 가치 ↑)
Phase 3   G39 #[reproducible(scope=...)] (capability 위에 sound)
          + G43 spec { } v0 (example: 만)
          + G45 #[golden] AST-aware
Phase 4   G40 #[sealed_construct] + G41 #[error_contract]
          + G44 #[since] / #[stability] / #[match_compat]
          + G46 #[budget(static)]
Phase 5   G37 #[taint] / #[sanitizes] (가장 무거움, 시그니처급 임팩트)
          + G46 #[budget(runtime)]
          + G43 spec { } v1 (forall property test 자동 생성)
```

## 3. New Chapters

### §20 Capabilities (G36)

정식 챕터 본문은 [`20-capabilities.md`](./20-capabilities.md) 가 권위.
본 문서의 다른 섹션 (§3.x extensions, §7.5, §11.5, §13.4, interaction matrix)
에서 §20 참조는 그 챕터 파일을 가리킨다.

### §21 Information Flow Tracking (G37)

정식 챕터 본문은 [`21-information-flow.md`](./21-information-flow.md) 가 권위.
본 문서의 다른 섹션에서 §21 참조는 그 챕터 파일을 가리킨다.

## 4. Extended Chapters

### §3.10 `#[spec("§X.Y")]` (G38)

#### §3.10.1 의미

선언 (fn / struct / enum / interface) 위에 `#[spec("§X.Y")]` 를 붙이면 컴파일러가
spec 챕터의 해당 section 존재를 검증하고, doc generation / LSP / `osty explain`
에 cross-reference 를 첨부한다.

```osty
#[spec("§2.2")]
fn checkNumericWidening(from: Type, to: Type) -> CheckResult { ... }

#[spec("§4.5")]
fn lowerTryOperator(expr: AstNode) -> MirNode { ... }

#[spec("§10.1.tier-1-core")]
pub fn strings_len(s: String) -> Int { ... }
```

#### §3.10.2 검증

`osty validate-spec` (또는 빌드 시 자동) 이 모든 `#[spec(...)]` 인자를 markdown
heading anchor 로 해석하고:

- 해당 section 이 존재하지 않으면 `E0790`
- Section 이 다른 chapter 로 이동했으면 `W0790` (suggested replacement 표시)

#### §3.10.3 사용 위치

- 컴파일러 internal: `internal/check`, `internal/resolve`, `internal/llvmgen`,
  `toolchain/*.osty` — *권장*
- Stdlib: `internal/stdlib/modules/*.osty` — *권장*
- 사용자 코드: 가능 (자기 문서화 용도)

#### §3.10.4 Doc cross-reference

`osty doc` 출력 시 `#[spec(...)]` 가 있는 함수의 hover/문서에 **spec 본문 첫 단락
inline** 표시. 예:

```
fn checkNumericWidening(from: Type, to: Type) -> CheckResult
  Spec: §2.2 Numeric Conversions
  > Osty allows only lossless implicit numeric widening. The widening
  > lattice is Int8 -> Int16 -> Int32 -> Int -> Float64, ...
```

#### §3.10.5 진단 코드

| 코드 | 의미 |
|---|---|
| `E0790` | `#[spec(...)]` 의 section 이 존재하지 않음 |
| `W0790` | Section 이 이동됨 (rename suggestion 포함) |

---

### §3.11 `#[reproducible(scope=...)]` (G39)

#### §3.11.1 의미

함수가 *환경독립*임을 컴파일러에 약속. `#[pure]` 보다 강하며, 캐시 키 / 빌드 해시 /
migration ID / content addressing 용도에 적합.

```osty
#[reproducible(scope = "target")]
fn computeKey(data: Bytes) -> Bytes32 { sha256(data) }
```

#### §3.11.2 Scope

| Scope | 의미 |
|---|---|
| `"run"` | 같은 프로세스 실행 안에서 동일. `Console` capability 허용 |
| `"target"` | 같은 Osty 버전 + target triple 에서 동일. *기본값*. |
| `"portable"` | 플랫폼 간 동일 (cross-compilation 결과 byte-equal) |

#### §3.11.3 검사

- §20.4 capability deny rule 적용 (deterministic capability 만 허용)
- `time.now()` / `random.*` / `env.*` 등 전역 호출 금지 (deny-list, capability
  migration 전 transition 기간 동안 적용)
- Unordered iteration 금지 (`Map.iter`, `Set.iter` — `Map.entriesSorted` /
  `Set.toListSorted` 사용)
- Pointer identity 비교 금지
- Transitive callee 도 같은 scope 이상 만족해야 함

`scope = "portable"` 추가 제약:
- `Int` 가 아닌 platform-dependent 크기 사용 금지 (Osty 는 `Int` 가 64-bit 고정이므로
  자동)
- `endianness` 의존 직렬화 금지 (별도 표시 — `bytes.toBigEndian` 명시)
- Float NaN bit pattern 비교 금지

#### §3.11.4 진단 코드

| 코드 | 의미 |
|---|---|
| `E0784` | `#[reproducible]` 함수가 non-deterministic capability 수신 |
| `E0786` | `#[reproducible]` 함수가 unordered iter 사용 |
| `E0787` | `#[reproducible(scope=A)]` 가 더 약한 scope 함수 호출 |
| `E0788` | `#[reproducible(scope="portable")]` 의 추가 제약 위반 |

---

### §3.4.5 `#[sealed_construct]` (G40)

#### §3.4.5.1 의미

Struct 의 생성 경로를 *명시한 constructor 만* 허용. parse-don't-validate 패턴을
언어 primitive 로.

```osty
#[sealed_construct(parse)]
pub struct Email {
    local: String,
    domain: String,
}

impl Email {
    pub fn parse(s: String) -> Email? {
        let parts = s.split("@")
        if parts.len() != 2 { return None }
        Some(Email { local: parts[0], domain: parts[1] })   // OK — authorized
    }
}
```

#### §3.4.5.2 금지되는 생성 경로

`#[sealed_construct(...)]` 가 붙은 struct 에 대해:

1. **외부 struct literal** — 다른 함수에서 `Email { local: ..., domain: ... }` 생성
2. **Spread update** — `Email { ..existing, domain: "x" }`
3. **필드 직접 mutation** — `e.domain = "x"` (단, struct 가 mutable 필드를
   가지면 `mut self` 메서드 안에서만)
4. **Generic deserialize / FFI / default 생성** — `json.parse::<Email>(...)` 는
   `parse` constructor 를 *경유*해야 함 (§3.4.5.4 참조)
5. **Test helper 우회** — `#[test]` 함수 안에서도 동일 규칙 (예외는
   `#[test_construct]`, §3.4.5.5)

#### §3.4.5.3 허용되는 경로

- `#[sealed_construct(name)]` 의 `name` 이 가리키는 method/function 본문 내부
  `Self { ... }` literal
- 같은 struct 의 `mut self` method 안에서의 필드 assignment (이미 instance 가
  존재하므로 새 생성 아님)
- `..self` 가 아닌 *내부 spread* (같은 method 안에서 `Self { ..s, x: 1 }` 형태) —
  단 `s` 가 같은 sealed struct 의 instance여야 하고, *해당 method 가 sealed
  constructor 로 등록*돼야 함
- `#[trusted_construct]` 어노테이션이 붙은 stdlib/internal 함수 (§3.4.5.6)

#### §3.4.5.4 Generic 경로 (json / FFI 등)

`json.parse::<Email>(text)` 는:
- (a) `Email` 이 `JsonParseable` interface 구현 — 이 interface 의 메서드가 sealed
  constructor 로 등록돼야 함, 또는
- (b) `Email` 에 `#[json(constructor = parse)]` 가 함께 붙어 있어야 함 — json
  parser 가 `parse(s)` 경로로 라우팅

```osty
#[sealed_construct(parse)]
#[json(constructor = parse, field = "email")]
pub struct Email { local: String, domain: String }
```

#### §3.4.5.5 Test 환경

```osty
#[test_construct]    // test profile 에서만 sealed 우회 가능
fn buildTestEmail(local: String, domain: String) -> Email {
    Email { local, domain }
}
```

`#[test_construct]` 함수는 production 빌드에서 컴파일 거부 (`E0421`).
Test 환경 (`osty test`, `#[cfg(test)]` 활성) 에서만 sealed 제약 우회.

#### §3.4.5.6 Stdlib trusted constructor

```osty
#[trusted_construct(reason = "byte-level json deserializer")]
fn jsonDecodeEmail(bytes: Bytes) -> Email? { ... }
```

`#[trusted_construct]` 는 stdlib/internal 패키지에서만 허용 (사용자 코드 사용 시
`E0422`). `osty audit --trusted-construct` 로 enumerate.

#### §3.4.5.7 진단 코드

| 코드 | 의미 |
|---|---|
| `E0420` | `#[sealed_construct]` struct 의 외부 literal 생성 |
| `E0421` | `#[test_construct]` 가 production 빌드에서 사용 |
| `E0422` | `#[trusted_construct]` 가 사용자 패키지에서 사용 |
| `E0423` | `#[sealed_construct(name)]` 의 name 이 method 가 아님 |

---

### §7.5 `#[error_contract]` (G41)

#### §7.5.1 의미

Result 의 Err 타입이 *어떤 variant 가 어떤 조건에서 발생*하는지를 시그니처에
catalog 한다.

```osty
pub enum EmailError {
    Format,
    DomainBlocked(String),
    TooLong(Int),
}

#[error_contract(
    EmailError.Format         when "missing @ or wrong format",
    EmailError.DomainBlocked  when "domain is in blocklist",
    EmailError.TooLong        when "input exceeds 320 chars",
)]
pub fn parseEmail(s: String) -> Result<Email, EmailError> { ... }
```

#### §7.5.2 검증

- `Err(...)` return path 가 contract 에 명시된 variant 만 사용하는지 컴파일러가
  *static 검사* (`E0410`)
- Contract 에 없는 variant 를 명시 호출 시 `E0411`
- Contract 의 variant 가 실제 코드에서 한 번도 발화되지 않으면 `W0411`
  (dead branch)

#### §7.5.3 호출자 측 활용

```osty
let r = parseEmail(input)
match r {
    Ok(e) -> use(e),
    Err(EmailError.Format) -> reportFormatError(),
    Err(EmailError.DomainBlocked(d)) -> reportBlocked(d),
    Err(EmailError.TooLong(n)) -> reportTooLong(n),
}
```

`#[error_contract]` 가 있는 함수의 호출 결과 match 는 contract variant 만으로
exhaustiveness 판단. (없으면 generic enum exhaustiveness 규칙.)

#### §7.5.4 Doc / Context 통합

- `osty doc` 에 *Failure modes* 표 자동 생성
- `osty context <symbol>` 에 `error_contract` 항목 포함 (G47)

#### §7.5.5 Result<T, Error> dyn 의 경우

`Error` interface 인 erased error 타입엔 `#[error_contract]` 적용 불가
(`E0412`). `#[error_contract]` 는 *concrete enum* 에만 의미가 있다. Erased
경우는 `#[error_contract(any)]` 로 선언적으로 "다양함" 표시 가능 (검증 없음, 문서용).

#### §7.5.6 `?` 연산자와의 상호작용

`?` 가 `#[error_contract]` 함수의 결과에 적용되면, **caller 의 contract 가 callee
contract 를 *포함*해야 한다** (subset relation):

```osty
#[error_contract(
    EmailError.Format         when "missing @",
    EmailError.DomainBlocked  when "domain blocked",
)]
fn parseEmail(s: String) -> Result<Email, EmailError> { ... }

#[error_contract(
    EmailError.Format         when "from parseEmail",
    EmailError.DomainBlocked  when "from parseEmail",
    DbError.Conflict          when "duplicate user",
)]
fn createUser(s: String) -> Result<UserId, EmailError | DbError> {
    let e = parseEmail(s)?       // OK — caller contract ⊇ parseEmail contract
    db.insert(e)?                 // OK — DbError.Conflict 가 caller contract 에 포함
}
```

`Result<T, EmailError | DbError>` 형은 *row-polymorphic error union*. v0.6 에서는
**closed union 만 허용** — `EmailError | DbError | ...` 은 enum 합집합이며 각
variant 가 정확히 어느 enum 소속인지 정해진다. row polymorphism (open union) 은
Open Items (§8) 에 deferred.

#### §7.5.7 `match` exhaustiveness 규칙

`#[error_contract]` 가 있는 함수의 결과에 대한 `match` 는:
- (a) **모든 contract variant** 처리 시 exhaustive — 그 외 variant 는 dead code
- (b) **일부 contract variant + `_` arm** 도 exhaustive
- (c) **contract 외 variant 명시** 시 `W0413` (해당 variant 가 contract 에 없음)

```osty
let r = parseEmail(input)
match r {
    Ok(e) -> ...,
    Err(EmailError.Format) -> ...,
    Err(EmailError.DomainBlocked(d)) -> ...,
    // contract 가 위 둘만 명시하므로 exhaustive
}

match r {
    Ok(e) -> ...,
    Err(EmailError.Format) -> ...,
    Err(EmailError.TooLong(n)) -> ...,    // W0413 — contract 에 없는 variant
    _ -> ...,
}
```

#### §7.5.6 진단 코드

| 코드 | 의미 |
|---|---|
| `E0410` | `Err(...)` 가 contract 에 없는 variant |
| `E0411` | Contract 가 type 에 없는 variant 명시 |
| `W0411` | Contract variant 가 실제 코드에서 미발화 |
| `E0412` | `#[error_contract]` 가 erased Error 타입에 적용 |

---

### §3.12 Structured Intent (G42)

#### §3.12.1 의도

자유 텍스트 doc comment (`///`) 와 별도로, *machine-readable intent* 를 구조화
어노테이션으로 노출. 목적 — doc generation, LLM context, IDE hover, test
generation, fixture sharing 의 단일 source.

#### §3.12.2 어노테이션

```osty
#[purpose("이메일 검증 후 DB에 사용자 저장")]
#[example(input = "alice@example.com", output = "Ok(42)")]
#[example(input = "invalid", output = "Err(EmailError.Format)")]
pub fn createUser(email: String) -> Result<UserId, Error> { ... }
```

| 어노테이션 | 검증 | 용도 |
|---|---|---|
| `#[purpose("free text")]` | 없음 — 자유 텍스트 | doc, LSP, `osty context` |
| `#[example(input = "...", output = "...")]` | 함수 호출로 비교 — 자동 테스트 | doc, test, context |
| `#[fixture(name = "X")]` | 함수 시그니처 검증 (반환 type 일치) | property test seed, doc, test, context |

#### §3.12.3 `#[fixture]` 상세

`#[fixture]` 는 함수 데코레이터 — 같은 type 의 *canonical 인스턴스*를 제공.

```osty
#[fixture(name = "typical")]
fn sampleUser() -> User {
    User.builder()
        .email("alice@example.com")
        .age(30)
        .build()
}

#[fixture(name = "edge_min")]
fn sampleMinUser() -> User { ... }
```

사용처:
- `#[example]` 가 호출하는 fixture: `#[example(uses = "sampleUser")]`
- `osty test --spec` 의 generator seed
- `osty doc` 의 코드 예시
- `osty context <symbol>` 의 sample 항목
- `#[golden]` 테스트의 입력 (G45)

#### §3.12.4 `osty context <symbol>` (G47)

```sh
$ osty context std.user::createUser
{
  "symbol": "std.user.createUser",
  "kind": "function",
  "signature": "fn createUser(email: String) -> Result<UserId, Error>",
  "purpose": "이메일 검증 후 DB에 사용자 저장",
  "examples": [
    {"input": ["alice@example.com"], "output": "Ok(42)"},
    {"input": ["invalid"], "output": "Err(EmailError.Format)"}
  ],
  "spec_refs": ["§10.30"],
  "error_contract": [
    {"variant": "EmailError.Format", "when": "missing @ or wrong format"},
    {"variant": "EmailError.DomainBlocked", "when": "domain is in blocklist"}
  ],
  "fixtures_used": [],
  "stability": "experimental",
  "since": "0.6"
}
```

JSON 스키마는 `LANG_SPEC_v0.6/13-tooling.md §13.9` 에서 정의.

#### §3.12.5 진단 코드

| 코드 | 의미 |
|---|---|
| `E0430` | `#[example]` 호출 결과가 expected output 과 다름 |
| `E0431` | `#[example]` 의 input arity 가 함수 arity 와 불일치 |
| `E0432` | `#[fixture]` 함수가 인자를 받음 (must be zero-arity) |
| `E0433` | `#[example(uses = "X")]` 의 X 가 fixture 가 아님 |

---

### §3.13 `spec { ... }` Block (G43)

#### §3.13.1 의도

함수 본문 옆에 *실행 가능한 명세*를 배치. v0 (Phase 3) 에선 `example:` 만 실행,
`law:` / `invariant:` 는 doc + LSP hover. v1 (Phase 5) 에서 forall property test
자동 생성.

#### §3.13.2 v0 surface

```osty
fn normalizeEmail(s: String) -> String {
    spec {
        example: normalizeEmail(" Alice@EXAMPLE.COM ") == "alice@example.com"
        example: normalizeEmail("") == ""
        law: result == result.trim()
        law: result == result.toLowerCase()
        invariant: result.indexOf(" ") == -1
    }

    s.trim().toLowerCase()
}
```

처리:
- `example:` — `osty test --spec` 가 boolean expression 으로 평가, 실패 시 test
  fail
- `law:` / `invariant:` — `osty doc` 출력에 포함, LSP hover 에 표시. *실행 안
  됨*. `result` 식별자 만 hover scope 에서 의미 있음 (실제 실행 시 임의 입력 평가
  불가).

#### §3.13.3 v1 surface (Phase 5)

```osty
fn quicksort<T: Ordered>(xs: List<T>) -> List<T> {
    spec {
        forall xs in gen.list(gen.int(), 128):
            result.toMultiset() == xs.toMultiset()

        forall xs in gen.list(gen.int(), 128):
            result.windowed(2).all(|w| w[0].le(w[1]))

        example: quicksort([]) == []
        example: quicksort([3, 1, 2]) == [1, 2, 3]
    }
    // ...impl
}
```

`forall x in gen:` 형태가 `std.testing.gen.*` generator 와 통합. `osty test
--spec` 가 property-based test runner 로 변환.

#### §3.13.4 Grammar (EBNF 추가)

```
SpecBlock      ::= 'spec' '{' SpecClause+ '}'
SpecClause     ::= ExampleClause | LawClause | InvariantClause | ForallClause
ExampleClause  ::= 'example' ':' Expr (LineEnd)
LawClause      ::= 'law' ':' Expr (LineEnd)
InvariantClause::= 'invariant' ':' Expr (LineEnd)
ForallClause   ::= 'forall' Ident (',' Ident)* 'in' Expr ':' Expr (LineEnd)
                   (* v1 only, deferred to Phase 5 *)
```

`spec` 은 contextual keyword (R7) — top-level `fn` / method body 첫
statement 위치에서만 keyword.

#### §3.13.5 의미 — `result` 식별자

`law:` / `invariant:` 식 내부에서 식별자 `result` 는 *함수 반환값*을 가리킨다
(virtual binding). 함수 외부에선 의미 없음.

`example:` 식 내부에서는 `result` 미사용. 표현은 boolean expression 으로 직접 평가.

#### §3.13.6 진단 코드

| 코드 | 의미 |
|---|---|
| `E0440` | `spec` 블록이 함수 본문 첫 위치가 아님 |
| `E0441` | `example:` 가 boolean 으로 평가되지 않음 |
| `E0442` | `law:` / `invariant:` 가 boolean 으로 평가되지 않음 |
| `E0443` | `forall` 의 generator 가 `Gen<T>` 가 아님 |

---

### §3.14 API Evolution — `#[since]`, `#[stability]`, `#[match_compat]` (G44)

#### §3.14.1 `#[since("X.Y")]`

선언 (fn / struct / enum variant / interface method / field) 이 도입된 버전 표시.

```osty
pub enum Event {
    Click,
    Key(String),

    #[since("0.6")]
    Drag(Int, Int),
}
```

`#[since]` 는 검증 없음 — 단순 메타데이터. `osty doc` / `osty context` 가 사용.

#### §3.14.2 `#[stability(level, until?)]`

```osty
#[stability("stable")]
pub fn parseEmail(s: String) -> Email? { ... }

#[stability("experimental", until = "0.7")]
pub fn parseEmailLoose(s: String) -> Email? { ... }

#[stability("deprecated", since = "0.6", remove = "0.8")]
pub fn oldApi() -> Int { ... }
```

Levels:
- `"stable"` — public API 약속. breaking change 시 major version bump 필요.
- `"experimental"` — minor version 안에서도 변경 가능. `until` 까지 안정화 안 되면
  removal 검토.
- `"deprecated"` — `since` 부터 deprecated. `remove` 버전에 제거 예정. 사용 시 `W0750`.
- `"internal"` — 같은 패키지 외부 사용 시 `W0902`.

#### §3.14.3 `osty publish` API surface diff algorithm

`osty publish` 가 *이전 published 버전* 의 manifest 와 *현재 working tree* 의
manifest 를 비교해 SemVer 호환성 검증.

##### §3.14.3.1 API surface 의 정의

다음만 surface 에 포함:

| 영역 | 포함 |
|---|---|
| 함수 | `pub fn` 또는 `pub interface` 의 method. `pub` 없으면 surface 아님 |
| 함수 시그니처 | name, generic param list, param 의 (name, type), return type, where bounds |
| 함수 attributes | `#[stability]`, `#[error_contract]`, `#[since]`, `#[reproducible]`, `#[pure]`, `#[budget]`, parameter taint annotations |
| 함수 body | **포함 안 함** (구현 변경은 surface 아님) |
| Struct | name, generic params, `pub` 필드 (name, type, default), method 시그니처 |
| Struct attributes | `#[sealed_construct]`, `#[json]`, `#[stability]`, `#[since]` |
| Enum | name, generic params, variant list (name, payload types), method 시그니처 |
| Enum attributes | `#[stability]`, `#[since]` per variant |
| Interface | name, generic params, method 시그니처, default body 존재 여부 (body 내용 아님) |
| Type alias | name, generic params, RHS type |
| Constants | `pub const` 의 (name, type) — value 는 surface 아님 |

다음은 surface 에 **포함 안 함** — 변경해도 SemVer 영향 없음:
- 함수 / 메서드 본문
- 비-`pub` 항목 모두
- `#[purpose]`, `#[example]`, `#[fixture]`, `#[spec]` (메타데이터, 문서 영향)
- `#[golden]` (테스트만 영향)
- 라인 번호, 파일 위치
- 주석

##### §3.14.3.2 Diff 분류

각 surface 항목 변경을 다음 카테고리로 분류:

| 카테고리 | SemVer 효과 | 예시 |
|---|---|---|
| **Breaking** | major bump 필수 | 항목 제거, 시그니처 변경, 필드 type 변경 |
| **Compat-add** | minor bump | 신규 항목 추가, 새 enum variant (with `#[since]`), 새 default arg 추가 |
| **Patch** | patch bump | 메타데이터 갱신, body 변경, 주석 |
| **Mixed** | breaking 우선 | 한 항목이라도 breaking 이면 전체 breaking |

세부 규칙:

```
함수 시그니처:
  Δ(name)                                   → BREAKING (rename = remove + add)
  Δ(param-name) at position i               → BREAKING (named-call 영향, G20)
  Δ(param-type) at position i               → BREAKING
  Δ(return-type)                            → BREAKING
  Δ(generic-params) — count change          → BREAKING
  Δ(generic-bound) — strengthen             → BREAKING
  Δ(generic-bound) — weaken                 → COMPAT-ADD
  Add required param                        → BREAKING
  Add defaulted param at end                → COMPAT-ADD (positional caller OK)
  Add defaulted param NOT at end            → BREAKING (G20 named-call shift)
  Remove defaulted param                    → BREAKING
  Default value change                      → COMPAT-ADD (호출자 다음 빌드 시 영향)
  Add #[reproducible]                       → COMPAT-ADD (callee 추가 보장)
  Remove #[reproducible]                    → BREAKING (callee 보장 약화)
  Add #[error_contract] variant             → BREAKING (캐치 의무 추가)
  Remove #[error_contract] variant          → COMPAT-ADD (caller 가 처리하던 분기 dead)
  Add parameter #[taint] / #[requires]      → BREAKING (caller 측 sanitize 의무)
  Add #[budget] strengthening               → BREAKING (이전 호출이 새 budget 위반 가능)
  Relax #[budget]                           → COMPAT-ADD

Struct:
  Add pub field with default                → COMPAT-ADD
  Add pub field without default             → BREAKING (constructor 측 영향)
  Remove pub field                          → BREAKING
  Δ(field-type)                             → BREAKING
  Field pub → priv                          → BREAKING
  Field priv → pub                          → COMPAT-ADD
  Add #[sealed_construct]                   → BREAKING (외부 literal 차단)
  Remove #[sealed_construct]                → COMPAT-ADD

Enum:
  Add variant (with #[since])               → COMPAT-ADD * (note: G44 match_compat 권장)
  Add variant (without #[since])            → BREAKING (#[since] 누락 = exhaustiveness 강제)
  Remove variant                            → BREAKING
  Δ(variant-payload)                        → BREAKING
  Reorder variants                          → BREAKING (discriminant 영향, G31)

Interface:
  Add method with default body              → COMPAT-ADD
  Add method without default body           → BREAKING (구현체 강제)
  Remove method                             → BREAKING
  Δ(method-signature)                       → BREAKING

Type alias:
  Δ(RHS) — same shape                       → COMPAT-ADD or PATCH (case-by-case)
  Δ(RHS) — different shape                  → BREAKING

Stability transition:
  experimental → stable                     → COMPAT-ADD (강화)
  stable → experimental                     → BREAKING (regression)
  stable → deprecated                       → COMPAT-ADD (still callable)
  deprecated → removed                      → BREAKING (publish 시 #[stability] level=removed-by 표시 필수)
  internal → pub                            → COMPAT-ADD
  pub → internal                            → BREAKING
```

##### §3.14.3.3 알고리즘 의사 코드

```
fn computeDiff(prev: Manifest, curr: Manifest) -> DiffReport {
    let mut report = DiffReport::new()

    for prevItem in prev.surface {
        if let currItem = curr.surface.findByQualifiedName(prevItem.name) {
            classifyChange(prevItem, currItem) into report
        } else {
            // 1. #[stability(level="deprecated", remove="X.Y")] 와 일치하는 X.Y 도달 시
            if prevItem.deprecatedAt(curr.version) {
                report.add(REMOVED_AS_PROMISED, prevItem)   // 예고된 제거
            } else {
                report.add(BREAKING_REMOVE, prevItem)
            }
        }
    }

    for currItem in curr.surface {
        if !prev.surface.containsByQualifiedName(currItem.name) {
            report.add(COMPAT_ADD, currItem)
        }
    }

    report
}

fn classifyChange(prev: SurfaceItem, curr: SurfaceItem) -> ChangeKind {
    // 위 §3.14.3.2 표의 모든 규칙을 if-else chain 으로 매칭
    // 첫 BREAKING 매칭이 결과
}

fn validatePublish(prev: Version, curr: Version, report: DiffReport) -> Result<(), PublishError> {
    let bump = compareVersion(prev, curr)
    let maxSeverity = report.maxChangeKind()

    match (maxSeverity, bump) {
        (BREAKING, MajorBump) => Ok(()),
        (BREAKING, MinorBump | PatchBump) => Err(E2100 { needsMajorBump: report.breakingItems() }),
        (COMPAT_ADD, MinorBump | MajorBump) => Ok(()),
        (COMPAT_ADD, PatchBump) => Err(E2102 { needsMinorBump: report.addedItems() }),
        (PATCH, _) => Ok(()),     // 어떤 bump 든 patch-only 는 OK
        (_, NoChange | Downgrade) => Err(E2101),
    }
}
```

##### §3.14.3.4 Manifest 형식

`osty publish` 는 `target/manifest-{version}.json` 을 생성/갱신:

```json
{
  "$schema": "https://osty.dev/schemas/manifest/v1.json",
  "package": "github.com/x/y",
  "version": "0.6.0",
  "stability_default": "experimental",
  "surface": [
    {
      "kind": "function",
      "name": "std.user.createUser",
      "stability": "stable",
      "since": "0.6",
      "signature": { /* §13.9 와 동일 schema */ }
    },
    /* ... */
  ]
}
```

`osty publish` 는 (a) 이전 manifest fetch (registry 또는 git tag), (b)
`computeDiff` 실행, (c) bump 검증, (d) 통과 시 manifest sign + upload.

##### §3.14.3.5 Edge cases

- **Generic instance 변경**: monomorphization 결과는 surface 아님. *type-level
  signature* 만 surface. Generic body 는 patch.
- **Structural interface 변경**: nominal interface 와 동일 규칙 — `pub interface
  X { fn m(...) }` 의 `m` 변경은 BREAKING 이라도, *어떤 type 이 X 를 구현하는지*
  는 nominal 등록 아님 (구조적). 따라서 X 의 변경이 영향을 주는 *모든 구현체*가
  자동 BREAKING 으로 전파. publish 시점에 워크스페이스 내 영향 분석 추가.
- **Trait alias / type alias 의 transitive expand**: `type T = Foo<Int>` 후
  `Foo` 가 변하면 `T` 도 변함. transitive 분석 필요.
- **Re-export 변경 (`pub use`)**: re-exported symbol 의 원본이 변하면 re-export
  지점도 변경된 것으로 분류.

##### §3.14.3.6 진단 코드 (publish)

§3.14.5 통합 — `E2100`/`E2101`/`E2102`, `W2100`.

#### §3.14.4 `#[match_compat("X.Y", ...)]`

Match 식이 *과거 enum shape* 에 pin. 미래 variant 추가 시 자동으로 깨지지 않음.

```osty
#[match_compat("0.5", fallback = unknownEvent, reason = "Drag is UI-only")]
fn handle(e: Event) -> String {
    match e {
        Event.Click -> "click",
        Event.Key(k) -> "key:{k}",
    }
}
```

`#[match_compat(...)]` 의 효과:
- v0.5 의 enum shape (Drag 없음) 에 대해 exhaustive 인지 검사
- 새 variant (예: Drag) 가 존재하면 `fallback` 식별자 함수 호출
- `reason` 필드는 audit 용 free-text — `osty audit --match-compat` 로 enumerate

**Silent OK 금지**: `fallback` 또는 명시 `unsafe_silent = true` 둘 중 하나 *반드시*
지정. 없으면 `E0450`. `unsafe_silent` 사용 시 항상 `W0902` warning + audit log.

#### §3.14.5 진단 코드

| 코드 | 의미 |
|---|---|
| `E0450` | `#[match_compat]` 가 fallback 도 unsafe_silent 도 명시 안 함 |
| `E0451` | `#[match_compat]` version 이 알려지지 않음 |
| `W0902` | `unsafe_silent` 사용 또는 `internal` API 외부 사용 |
| `E2100` | `osty publish` — stable API breaking change without major bump |
| `E2101` | `osty publish` — version downgrade |
| `E2102` | `osty publish` — compat-add change with patch-only bump |
| `W2100` | `osty publish` — experimental API change |

---

### §11.5 `#[golden]` (G45)

#### §11.5.1 의미

함수의 출력을 snapshot 파일과 비교. AST-aware diff (코드 출력의 경우) 또는 plain
text diff.

```osty
#[golden("fixtures/format_expr.snap")]
fn testFormatBinaryOp() {
    let result = formatExpr(parseExpr("1 + 2 * 3"))
    testing.assertGolden(result)
}

#[golden("fixtures/diag_E0765.snap", mode = "ast")]
fn testNumericNarrowingDiag() {
    let diag = checkSnippet("let x: Int8 = bigInt")
    testing.assertGolden(diag.toString())
}
```

#### §11.5.2 명령

- `osty test --golden` — 비교, 실패 시 diff 출력
- `osty test --update-golden` — 모든 `#[golden]` 함수 재실행 후 snapshot 갱신
- `osty test --update-golden=path/to/...` — 특정 파일만 갱신

#### §11.5.3 AST-aware diff

`mode` 옵션:
- `"text"` (default) — byte-exact 비교. 어떤 String 출력이든 사용 가능
- `"ast"` — reparse 후 AST 비교. **출력이 valid Osty source 일 때만** 사용 가능
- `"json"` — JSON 으로 parse 후 structural 비교. key 순서 무시
- `"diag"` — Osty diagnostic 출력 형식. `Span` 이 다른 두 진단이 같은 코드/메시지면 same

**AST mode 의 정확한 의미**:
1. snapshot 의 텍스트와 함수 출력 텍스트를 각각 reparse
2. 두 AST 의 *normalized form* 비교 — token position, comment, whitespace 무시
3. AST 가 같으면 OK, 다르면 fail 후 *AST diff tree* 출력

```osty
// 함수 본문이 다음 출력 생성:
//   "fn add(x: Int, y: Int) -> Int { x + y }"
//
// snapshot 파일이 다음을 담음:
//   "fn add(x: Int, y: Int) -> Int {\n    x + y\n}"
//
// mode="text" → fail (whitespace 다름)
// mode="ast"  → OK (AST 동일)
```

**AST mode 가 fail 하는 경우**: AST 가 진짜 다를 때 — 즉 *프로그램 의미가 변함*.
이 경우 diff 는 AST node 수준에서:
```
- BinaryOp(Add, Var("x"), Var("y"))
+ BinaryOp(Sub, Var("x"), Var("y"))
```

**`#[golden]` 함수의 출력이 valid Osty 가 아닐 때** `mode="ast"` 사용 시
`E0446` (parse failure 시점에 fail).

#### §11.5.4 Snapshot 파일 형식

```
# osty-golden-v1
# function: TestFormatBinaryOp
# mode: ast
# fixture: sampleBinaryExpr   (optional)
# generated: 2026-05-07T12:34:56Z
# source-hash: abc123...

fn add(x: Int, y: Int) -> Int { x + y }
```

헤더 (`#` 으로 시작) 는 메타데이터. 본문은 빈 줄 이후. `source-hash` 는 함수
정의의 hash — 함수가 변하면 stale snapshot 알림.

#### §11.5.4 `#[reproducible]` 와의 결합

`#[golden]` 함수는 *암묵적으로 `#[reproducible(scope = "target")]`*. non-deterministic
함수의 golden 은 의미 없으므로 컴파일 거부 (`E0444`).

#### §11.5.5 진단 코드

| 코드 | 의미 |
|---|---|
| `E0444` | `#[golden]` 함수가 reproducible 검증 실패 |
| `E0445` | `#[golden]` 의 snapshot 파일이 없음 (첫 실행 시 안내) |
| `W0444` | `#[golden]` 의 snapshot 이 stale (수동 update 필요 안내) |

---

### §3.15 `#[budget]` — Static + Runtime 분리 (G46)

#### §3.15.1 Static budget (Phase 4)

```osty
#[budget(allocs = 0)]
fn hotLoop(xs: List<Int>) -> Int {
    let mut sum = 0
    for x in xs { sum = sum + x }
    sum
}

#[budget(io_calls = 0, stack_depth = 100)]
fn pure_compute(data: Bytes) -> Bytes32 { ... }
```

컴파일러가 *증명*. 위반 시 `E0795`.

| Budget key | 검증 |
|---|---|
| `allocs = N` | GC alloc site 수 정적 카운트 |
| `io_calls = N` | I/O capability 호출 transitive 카운트 |
| `stack_depth = N` | 재귀 깊이 분석 (재귀 cycle 감지 시 추가 검사) |
| `instructions = N` | 컴파일된 함수의 estimated cycle count (LLVM 측정) |

#### §3.15.2 Runtime budget (Phase 5)

```osty
#[budget(time_ms = 5, p99_ms = 20)]
fn routeRequest(req: Request) -> Response { ... }
```

`osty bench --budget` 가 회귀 게이트:
- `time_ms` — 평균 ≤ 5ms
- `p99_ms` — p99 ≤ 20ms
- 위반 시 bench 실패, CI 차단

#### §3.15.3 진단 코드

| 코드 | 의미 |
|---|---|
| `E0795` | Static budget 위반 (알loc / io_calls / stack_depth / instructions) |
| `E0796` | `#[budget]` key 가 알려지지 않음 |
| `W0795` | Runtime budget 위반 (bench-time, fail 아니라 warn 옵션) |

---

### §13.4 `osty context <symbol>` (G47)

#### §13.4.1 명령 surface

```sh
osty context <symbol>                          # default: human-readable text
osty context <symbol> --format=json            # machine-readable JSON
osty context <symbol> --recursive              # include callees (depth=1)
osty context <symbol> --recursive=N            # depth=N
osty context current://path:line:col           # LSP-style cursor
osty context --search=<query>                  # symbol search + first match
osty context --all-stdlib --format=jsonl       # bulk export (one JSON per line)
```

#### §13.9 JSON 스키마 (정식)

```json
{
  "$schema": "https://osty.dev/schemas/context/v1.json",
  "symbol": "std.user.createUser",
  "kind": "function",
  "package": "std.user",
  "file": "internal/stdlib/modules/user.osty",
  "line": 42,
  "signature": {
    "params": [
      {"name": "email", "type": "String", "annotations": [
        {"name": "taint", "args": ["user_input"]}
      ]},
      {"name": "db", "type": "Db", "annotations": []}
    ],
    "returns": "Result<UserId, Error>",
    "generic_params": [],
    "where_bounds": []
  },
  "stability": {
    "level": "stable",
    "since": "0.6"
  },
  "purpose": "이메일 검증 후 DB에 사용자 저장",
  "examples": [
    {
      "input": ["alice@example.com", "<Db>"],
      "output": "Ok(42)",
      "uses_fixture": null
    }
  ],
  "fixtures_referenced": [],
  "spec_refs": [
    {"section": "§10.30.user", "title": "User module"}
  ],
  "error_contract": [
    {"variant": "EmailError.Format", "when": "missing @ or wrong format"},
    {"variant": "EmailError.DomainBlocked", "when": "domain is in blocklist"},
    {"variant": "DbError.Conflict", "when": "duplicate user"}
  ],
  "effects": {
    "capabilities_required": ["Db"],
    "reproducible": null,
    "pure": false,
    "taint_sources": [],
    "taint_sanitizes": [],
    "taint_sinks": []
  },
  "budget": {
    "static": null,
    "runtime": null
  },
  "doc_comment": "사용자를 생성한다. ...",
  "diagnostics_emitted": ["E0410", "W0411"],
  "callees": [
    {"symbol": "std.user.parseEmail", "kind": "function"},
    {"symbol": "Db.insert", "kind": "method"}
  ]
}
```

스키마 필드 의미:
- `kind`: `"function" | "method" | "struct" | "enum" | "interface" | "type-alias" | "constant"`
- `signature.params[].annotations`: §3 의 모든 parameter annotation 수집
- `stability.level`: G44 의 4 레벨 (`stable | experimental | deprecated | internal`)
- `examples[].input`: position 별 인자. capability 는 `"<Capability>"` 형 placeholder
- `examples[].uses_fixture`: G42 fixture 사용 시 fixture 이름, 아니면 null
- `spec_refs[]`: G38 `#[spec]` 의 모든 등록 — markdown anchor 와 section title
- `error_contract[]`: G41 의 contract entries
- `effects.capabilities_required`: 함수 시그니처의 capability parameter 합집합
- `effects.taint_sources / sanitizes / sinks`: G37 어노테이션 수집
- `budget.static`: G46 `{"allocs": 0, "io_calls": 0, ...}` 또는 null
- `budget.runtime`: G46 `{"time_ms": 5, "p99_ms": 20}` 또는 null
- `callees[]`: `--recursive` 시에만 채워짐, depth 0 에서는 빈 배열

#### §13.4.2 사용 시나리오

**LLM agent**:
```python
import subprocess, json
ctx = json.loads(subprocess.check_output([
    "osty", "context", "std.user.createUser", "--format=json"
]))
prompt = build_prompt_with_context(ctx)
```

**LSP integration**: `textDocument/hover` 가 같은 JSON 을 받아 markdown 으로 렌더:

```
fn createUser(email: String, db: Db) -> Result<UserId, Error>

  Purpose: 이메일 검증 후 DB에 사용자 저장
  Stability: stable (since 0.6)
  Spec: §10.30.user — User module

  Examples:
    createUser("alice@example.com", Db) → Ok(42)

  Failure modes:
    EmailError.Format       — missing @ or wrong format
    EmailError.DomainBlocked — domain is in blocklist
    DbError.Conflict        — duplicate user

  Capabilities: Db
  Effects: not reproducible (calls db sink)
```

**Bulk export** (`--all-stdlib --format=jsonl`): stdlib 전체를 jsonl 로 export.
training 데이터 / static analysis / 외부 검사 도구가 사용.

#### §13.4.3 LSP integration

LSP `textDocument/hover` 응답이 같은 데이터를 markdown 으로 렌더. AI agent 측은
JSON 직접 query (`osty context --format=json` 또는 LSP custom request
`osty/context`).

---

### §4.4 `while` keyword (G49)

#### §4.4.1 동기

v0.5 까지 Osty 는 `while cond { }` 자리에 `for cond { }` 를 사용 (G22 의 `loop`
와 구별 위해). 의도는 keyword economy — `for` 하나로 두 모드 cover. 그러나 사용
corpus 에서 *읽기* 단계 mental-model 충돌 보고 다수 (`for` = iteration 이라는
기대). v0.6 은 `while cond { }` 를 *동의어* 로 도입.

#### §4.4.2 의미

```osty
while cond { body }      ≡   for cond { body }
```

- 둘 다 같은 lowering, 같은 type (Unit), 같은 control flow.
- `for` 모드는 *유지* — deprecate 하지 않는다. 어느 form 도 정답.
- `for cond { }` 는 G22 도입 시점 (v0.5) 의 형식. `while` 은 v0.6 추가.

#### §4.4.3 Grammar

`while` 은 **fully reserved keyword** (contextual 아님). v0.5 까지 `while` 을
식별자로 쓴 코드 — 만약 있다면 — 이름 변경 필요. *fresh tree* 이므로 호환성
영향 0 으로 간주.

```ebnf
WhileStmt ::= 'while' Expr Block
```

#### §4.4.4 진단 코드

신규 코드 없음. 기존 control flow 진단 (`E0600` 류) 재사용.

---

## 5. Grammar Changes (v0.5 → v0.6)

```ebnf
(* §3.13 G43 — spec block *)
SpecBlock      ::= 'spec' '{' SpecClause+ '}'
SpecClause     ::= ExampleClause | LawClause | InvariantClause | ForallClause
ExampleClause  ::= 'example' ':' Expr (LineEnd)
LawClause      ::= 'law' ':' Expr (LineEnd)
InvariantClause::= 'invariant' ':' Expr (LineEnd)
ForallClause   ::= 'forall' Ident (',' Ident)* 'in' Expr ':' Expr (LineEnd)

(* §4.4 G49 — while as alias *)
WhileStmt      ::= 'while' Expr Block

(* §21 G37 — annotation on parameter (new position) *)
ParamDecl      ::= Annotation* Pattern ':' Annotation* Type ('=' DefaultExpr)?
                   (* Annotation* before Pattern is new in v0.6 *)
                   (* Annotation* before Type allows #[taint("...")] String form *)

(* §20 / §3.4.5 / §3.10-3.15 / §7.5 — fixed annotation set extension *)
(* Existing Annotation rule unchanged: #[name(args)] form *)
```

| 항목 | v0.5 | v0.6 | Δ |
|---|---:|---:|---:|
| Reserved keywords | 17 | 18 | +1 (`while` — G49) |
| Contextual keywords | 10 | 14 | +4 (`spec`, `example`, `law`, `invariant`; `forall` 은 v1 단계) |
| Fixed annotation set | 11 | 31 | +20 |
| EBNF productions | 191 | 199 | +8 |
| Lexer token classes | 36 | 36 | 0 |

신규 어노테이션 20:

| Capability (§20) | `#[ambient]`, `#[reproducible_capability]` |
| Information flow (§21) | `#[taint]`, `#[sanitizes]`, `#[requires]`, `#[trusted_declassify]`, `#[taint_field]` |
| Spec / intent | `#[spec]`, `#[purpose]`, `#[example]`, `#[fixture]` |
| Construction | `#[sealed_construct]`, `#[trusted_construct]`, `#[test_construct]` |
| Error | `#[error_contract]` |
| Determinism | `#[reproducible]` |
| Evolution | `#[since]`, `#[stability]`, `#[match_compat]` |
| Testing | `#[golden]`, `#[budget]` |

## 5.1 Annotation namespace (proposal — Phase 2)

20 개 신규 + 기존 11 개 = 31 개 어노테이션은 namespace 없는 flat catalog.
Phase 2 에서 *category-prefix 옵션* 도입 검토:

```osty
#[lang.taint("...")]
#[lang.spec("§2.2")]
#[lang.golden("...")]
```

`lang.` prefix 는 *옵션 long form* — `#[taint(...)]` 와 `#[lang.taint(...)]` 둘
다 허용. `osty doc --strict-namespace` 모드에서 long form 강제. v1.0 cut 시
강제 전환 검토 (또는 단축형 유지).

## 6. Error Code Allocations

| Range | 영역 | 신규 |
|---|---|---|
| `E0410–E0429` | Annotation/intent (G41, G42) | E0410, E0411, E0412, E0420, E0421, E0422, E0423, E0430, E0431, E0432, E0433 |
| `E0440–E0449` | Spec block (G43, G44) | E0440, E0441, E0442, E0443, E0444, E0445, E0450, E0451 |
| `E0780–E0799` | Capability / Reproducible / Budget (G36, G39, G46) | E0780-E0788, E0790, E0795, E0796 |
| `E0900–E0949` | Information flow (G37) | E0900, E0901, E0902, E0903 |
| `E2100–E2149` | Publishing (G44) | E2100, E2101 |
| `W0750–W0799` | Stability / spec ref warnings | W0790, W0795 |
| `W0900–W0949` | Flow / declassify warnings | W0901, W0902 |
| `W2100–W2149` | Publish warnings | W2100 |

## 7. Migration Notes

### 7.1 Stdlib capability migration (Phase 1)

7 개 capability interface 도입과 함께 stdlib 의 다음 모듈이 *전역 함수 → capability
메서드* 로 전환:

| 모듈 | v0.5 | v0.6 |
|---|---|---|
| `std.time` | `time.now()` | `clock.now()` (Clock capability) |
| `std.random` | `random.next()` | `rng.next()` (Rng capability) |
| `std.env` | `env.get(k)` | `env.get(k)` (Env capability — same name) |
| `std.fs` | `fs.read(p)` | `fs.read(p)` (Fs capability — same name) |
| `std.os` | `os.exec(...)` | `process.exec(...)` (Process capability) |
| `std.net` | `net.dial(...)` | `net.dial(...)` (Net capability) |

**호환성 brige**: v0.5 호환 모드 (`--legacy-globals`) 가 v0.6.0 에서 제공, v0.7
에서 제거.

전환 전략:
- v0.6.0: capability 도입 + 전역 함수 deprecated (W0750)
- v0.6.x: 마이그레이션 기간
- v0.7.0: 전역 함수 제거

##### `--legacy-globals` desugar 의미론

`osty build --legacy-globals` 또는 manifest `[legacy] globals = true` 활성 시:

1. **stdlib 의 v0.5 전역 함수 stub 재활성화**: `std.time.now()` /
   `std.random.next()` / `std.env.get(k)` / `std.fs.read(p)` / `std.os.exec(...)`
   / `std.net.dial(...)` 가 **`std.<module>.<host>.<method>` 호출로 자동 desugar**:
   ```
   time.now()           ⟶  std.time.host.now()
   random.next()        ⟶  std.random.host.next()
   env.get(k)           ⟶  std.env.host.get(k)
   fs.read(p)           ⟶  std.fs.host.read(p)
   os.exec(c, a)        ⟶  std.process.host.exec(c, a)
   net.dial(h, p)       ⟶  std.net.host.dial(h, p)
   ```

2. **`std.<module>.host` 는 process-global capability instance**:
   - 프로그램 lifetime 동안 단 하나
   - `std.time.host: Clock` 는 system clock
   - `std.random.host: Rng` 는 process-default seed (cryptographically secure)
   - `std.fs.host: Fs` 는 host filesystem
   - 등

3. **Legacy 호출은 `W0750` deprecation warning**:
   ```
   warning: time.now() 는 v0.7 에서 제거됩니다.
            #[ambient(clock)] 또는 capability parameter 로 마이그레이션 권장.
            --legacy-globals 활성 시에만 동작.
            See: MIGRATING_v0.5_to_v0.6.md
   ```

4. **`#[reproducible]` / `#[pure]` 검사는 legacy 호출도 차단**:
   `--legacy-globals` 가 활성이어도, `#[reproducible]` 함수 본문에서
   `time.now()` 호출 시 desugar 결과가 `std.time.host.now()` (capability method)
   이고, `std.time.host: Clock` 의 deterministic 등급이 non-deterministic
   이므로 `E0784` 발화. *legacy 모드도 effect 검사를 우회하지 못함*.

5. **`--legacy-globals` 자체가 manifest `stability` 영향**:
   manifest 에 `legacy.globals = true` 표시된 패키지는 자동으로
   `[stability] default = "experimental"` 로 강제. *legacy 의존 코드는 stable
   API 못 약속*.

6. **v0.7 제거 시 동작**: `--legacy-globals` flag 자체가 unknown flag 로 fail.
   manifest 의 `[legacy]` 섹션은 warning 만 (호환성 의도이므로 무시).

이 desugar 는 **capability 시그니처 위에서 sound** — host 인스턴스가 capability
type 을 만족하므로 type system 의 모든 보장이 유지된다. 단지 *명시성* 만 잃은 것.

#### 7.1.1 Migration code samples

**Before (v0.5 — 라이브러리 코드)**:
```osty
pub fn buildId() -> String {
    "{time.now().toEpochMillis()}-{random.next()}"
}

pub fn loadConfig() -> Result<Config, Error> {
    let path = env.get("CONFIG_PATH") ?? "/etc/app.toml"
    let text = fs.readToString(path)?
    json.parse(text)
}
```

**After (v0.6 — 라이브러리 코드)**:
```osty
pub fn buildId(clock: Clock, rng: Rng) -> String {
    "{clock.now().toEpochMillis()}-{rng.next()}"
}

pub fn loadConfig(env: Env, fs: Fs) -> Result<Config, Error> {
    let path = env.get("CONFIG_PATH") ?? "/etc/app.toml"
    let text = fs.readToString(path)?
    json.parse(text)
}
```

**Before (v0.5 — entry script)**:
```osty
fn main() {
    let id = buildId()
    let cfg = loadConfig()?
    println("id={id} cfg={cfg.toString()}")
}
```

**After (v0.6 — entry script)**:
```osty
#[ambient(clock, rng, env, fs, console)]
fn main() {
    let id = buildId(clock, rng)              // ambient forward
    let cfg = loadConfig(env, fs)?            // ambient forward
    console.println("id={id} cfg={cfg.toString()}")
}
```

#### 7.1.2 Test 측 migration

**Before (v0.5)**:
```osty
fn test_buildId_format() {
    let id = buildId()
    testing.assertMatches(id, r"\d+-\d+")
}
```

**After (v0.6 — fake capability 주입)**:
```osty
fn test_buildId_format() {
    let fakeClock = std.time.fakeClock(epoch_ms = 1_000_000)
    let fakeRng = std.random.seededRng(seed = 42)
    let id = buildId(fakeClock, fakeRng)
    testing.assertEq(id, "1000000-1608637542")    // deterministic
}
```

이게 *capability migration 의 진짜 가치* — 테스트가 *deterministic* 해진다.

### 7.2 Sealed construct retrofit

기존 사용자 코드의 직접 struct literal 은 *기본 허용* — `#[sealed_construct]` 는
opt-in 이므로 마이그레이션 없음. Stdlib 의 일부 struct (`Email`, `Url`, `Path`,
`SqlIdent`, `Duration`, `Uuid`) 가 v0.6 에서 sealed 화 — 사용자 코드는 이미
stdlib parser 를 경유하므로 영향 적음.

#### 7.2.1 Sealed retrofit 코드 샘플

**Before (v0.5 stdlib)**:
```osty
pub struct Email {
    pub local: String,
    pub domain: String,
}

pub fn email_parse(s: String) -> Email? { ... }
```

**After (v0.6 stdlib)**:
```osty
#[sealed_construct(parse)]
#[json(constructor = parse, field = "email")]
pub struct Email {
    local: String,           // pub 제거 — 외부 직접 접근 차단
    domain: String,
}

impl Email {
    pub fn parse(s: String) -> Email? { ... }
    pub fn local(self) -> String { self.local }
    pub fn domain(self) -> String { self.domain }
}
```

**사용자 영향**:
```osty
// v0.5
let e = Email { local: "alice", domain: "example.com" }   // OK
let local = e.local                                        // 직접 필드 접근

// v0.6
let e = Email { local: "alice", domain: "example.com" }   // E0420
let e = Email.parse("alice@example.com")?                  // OK
let local = e.local                                        // E0500 (private field)
let local = e.local()                                      // OK (accessor method)
```

호환성: `--legacy-construct` v0.6.x 한정 — `osty build --legacy-construct`
시 sealed 검사 비활성. v0.7 제거.

### 7.3 Taint annotation rollout

Phase 5 에서 stdlib sink 5 개 (`db.query`, `process.exec`, `fs.path*`,
`http.redirect`, `template.render`) 에 `#[requires]` 추가. 기존 사용자 코드 중
sanitizer 미사용 코드는 *컴파일 에러 발생*. 이는 *의도된 회귀* — supply chain 의
보안 holes 노출.

완화: `#[trusted_declassify]` 임시 escape hatch 제공, audit 으로 enumerate.

#### 7.3.1 Taint rollout 코드 샘플

**Before (v0.5 — vulnerable)**:
```osty
fn handler(req: HttpRequest) -> HttpResponse {
    let userId = req.queryParam("id") ?? ""
    let rows = db.query("SELECT * FROM users WHERE id = {userId}")    // SQL injection
    HttpResponse.ok(rows.toJson())
}
```

**v0.6 Phase 5 — 컴파일 에러**:
```
E0900: tainted value reaches sql sink
  --> src/handler.osty:3:30
   |
 3 |     let rows = db.query("SELECT * FROM users WHERE id = {userId}")
   |                ^^^^^^^^ requires sql_safe
   |
 hint: sanitize via std.sql.escape or use parameterized query
       std.sql.exec("SELECT * FROM users WHERE id = ?", [userId])
```

**v0.6 fixed**:
```osty
fn handler(req: HttpRequest, db: Db) -> HttpResponse {
    let userId = req.queryParam("id") ?? ""        // userId: { user_input } tag
    let rows = db.exec(
        "SELECT * FROM users WHERE id = ?",
        [userId],                                    // parameterized — sink 가 tag 무시
    )
    HttpResponse.ok(rows.toJson())
}
```

또는 sanitize:
```osty
fn handler(req: HttpRequest, db: Db) -> HttpResponse {
    let userId = req.queryParam("id") ?? ""
    let safe = std.sql.escape(userId)               // tag → sql_safe 변환
    let rows = db.query("SELECT * FROM users WHERE id = {safe}")    // OK
    HttpResponse.ok(rows.toJson())
}
```

### 7.2 Sealed construct retrofit

기존 사용자 코드의 직접 struct literal 은 *기본 허용* — `#[sealed_construct]` 는
opt-in 이므로 마이그레이션 없음. Stdlib 의 일부 struct (`Email`, `Url`, `Path`,
`SqlIdent`, `Duration`) 가 v0.6 에서 sealed 화 — 사용자 코드는 이미 stdlib parser 를
경유하므로 영향 적음.

### 7.3 Taint annotation rollout

Phase 5 에서 stdlib sink 4 개 (`db.query`, `process.exec`, `fs.path*`,
`http.redirect`) 에 `#[requires]` 추가. 기존 사용자 코드 중 sanitizer 미사용
코드는 *컴파일 에러 발생*. 이는 *의도된 회귀* — supply chain 의 보안 holes 노출.
완화: `#[trusted_declassify]` 임시 escape hatch 제공, audit 으로 enumerate.

## 7.5 Cross-feature interaction matrix

14 개 결정 (G36–G49) 의 *상호작용*. 같은 함수에 다중 어노테이션 적용 시 의미는
다음 표가 권위. 11 개 *의미론적 상호작용* feature 만 행/열에 등장 — G47 (osty
context, 운영 도구), G48 (annotation surface, meta), G49 (`while`, syntactic) 는
다른 결정과 의미 충돌이 없으므로 별도 행 없음.

표는 *upper-triangular* 형식 — `Row × Column` 위치에 두 feature 의 상호작용을
명시. 대칭 위치 (Column × Row) 는 `—` 로 표기 (위쪽 entry 가 권위).

| | Capability (G36) | Taint (G37) | Spec link (G38) | Reproducible (G39) | Sealed (G40) | ErrContract (G41) | Intent (G42) | SpecBlock (G43) | Evolution (G44) | Golden (G45) | Budget (G46) |
|---|---|---|---|---|---|---|---|---|---|---|---|
| **Capability (G36)** | self | tag propagates through capability method 시그니처 | 무관 | non-det cap 수신은 `E0784` | 무관 | 무관 | 무관 | spec block 안에서 capability 호출 가능 | capability 시그니처 변경 = breaking | golden 함수는 deterministic capability 만 (`E0444`) | capability 호출이 `io_calls` budget 에 카운트 |
| **Taint (G37)** | — | self | 무관 | 직교 — taint 는 provenance, reproducibility 는 determinism. 둘 다 적용 가능 | sealed type 도 tag 운반 (struct 단위 fold per §21.5) | Err variant 도 tag 운반 (Result/Option per §21.5.2) | example 의 input 에 source tag 표시 가능 | spec block 안에서 taint 검사 — 정식 검증은 v0.7+ Open Item | taint annotation 변경 = breaking (caller 측 sanitize 의무) | golden 입력은 *untainted* 권장 (snapshot 의 reproducibility 위해) | 무관 |
| **Spec link (G38)** | — | — | self | 무관 | 무관 | 무관 | example / purpose 와 함께 사용 가능 | spec block 과 보완 (annotation 은 ref, block 은 본문) | spec section 이동 = `W0790` | 무관 | 무관 |
| **Reproducible (G39)** | — | — | — | self (scope 강도: portable > target > run) | sealed constructor 도 reproducible 가능 | 무관 | 무관 | spec block 도 reproducible 함수 안에서 OK | scope 강화 = breaking, 약화 = compat-add | golden ⇒ implied `#[reproducible(scope="target")]` | 무관 |
| **Sealed (G40)** | — | — | — | — | self | sealed type 도 ErrContract 가능 | example 이 sealed constructor 호출 가능 | spec block example 도 sealed constructor 경유 | sealed → non-sealed = breaking | 무관 | 무관 |
| **ErrContract (G41)** | — | — | — | — | — | self | example 이 contract variant 검증 가능 | spec block example 에 Err 케이스 포함 가능 | contract 변형 = breaking (variant 추가/제거) | 무관 | 무관 |
| **Intent (G42)** | — | — | — | — | — | — | self | spec block example 과 `#[example]` 동시 사용 시 두 source 합집합 — 중복은 OK | example 변경 = compat-add (no SemVer effect) | fixture 가 golden 입력으로 사용 가능 | 무관 |
| **SpecBlock (G43)** | — | — | — | — | — | — | — | self | spec block 변경 = compat-add (body 변경과 동급) | spec block example 결과가 golden 화 가능 | 무관 |
| **Evolution (G44)** | — | — | — | — | — | — | — | — | self | golden snapshot 변경 = audit (W0444) | budget 약화 = compat-add, 강화 = breaking |
| **Golden (G45)** | — | — | — | — | — | — | — | — | — | self | 무관 |
| **Budget (G46)** | — | — | — | — | — | — | — | — | — | — | self |

**핵심 invariants** (위 표가 함의):
1. `#[reproducible]` ∩ `#[ambient]` 또는 `Clock`/`Rng`/`Env`/`Fs`/`Net`/`Process`
   capability 수신 = `E0784` (compile error)
2. `#[golden]` ⇒ implied `#[reproducible(scope="target")]`
3. `#[taint]` 가 `#[requires]` sink 에 도달 + sanitize 없음 = `E0900`
4. `#[sealed_construct]` struct 의 외부 literal = `E0420` (test 환경 escape 별도)
5. `#[error_contract]` 의 contract 외 variant 발화 = `E0410`
6. `#[stability("stable")]` API 의 시그니처 변경 + minor bump = `E2100` (publish 시)
7. `#[match_compat]` 가 fallback 도 unsafe_silent 도 명시 안 함 = `E0450`

## 7.6 Annotation 합법 위치 매트릭스

Osty 에는 *impl block* 이 없다 (§14: "methods live inside `struct` / `enum`
bodies"). interface 는 별도 declaration. 아래 표는 그 기준.

| Annotation | fn | struct | enum | enum variant | field | parameter | method | interface |
|---|---|---|---|---|---|---|---|---|
| `#[ambient]` | ✓ (entry-point 만) | | | | | | | |
| `#[taint]` | ✓ (반환) | | | | ✓ | ✓ | ✓ | |
| `#[sanitizes]` | ✓ | | | | | | ✓ | |
| `#[requires]` | | | | | | ✓ | | |
| `#[trusted_declassify]` | ✓ | | | | | | ✓ | |
| `#[taint_field]` | | | | | ✓ | | | |
| `#[reproducible]` | ✓ | | | | | | ✓ | |
| `#[reproducible_capability]` | | | | | | | | ✓ |
| `#[spec]` | ✓ | ✓ | ✓ | | | | ✓ | ✓ |
| `#[sealed_construct]` | | ✓ | | | | | | |
| `#[trusted_construct]` | ✓ | | | | | | ✓ | |
| `#[test_construct]` | ✓ | | | | | | ✓ | |
| `#[error_contract]` | ✓ | | | | | | ✓ | |
| `#[purpose]` | ✓ | ✓ | ✓ | | | | ✓ | ✓ |
| `#[example]` | ✓ | | | | | | ✓ | |
| `#[fixture]` | ✓ | | | | | | | |
| `#[since]` | ✓ | ✓ | ✓ | ✓ | ✓ | | ✓ | ✓ |
| `#[stability]` | ✓ | ✓ | ✓ | | | | ✓ | ✓ |
| `#[match_compat]` | ✓ | | | | | | ✓ | |
| `#[golden]` | ✓ (test) | | | | | | | |
| `#[budget]` | ✓ | | | | | | ✓ | |

`#[ambient]` 의 *entry-point 만* 의미: `fn main` / script (`#!/usr/bin/env osty`) /
`#[test]` / `#[bench]` / `bench*` / `test*` 함수. 그 외 `fn` 위치는 `E0780`.

`✓` 표시 위치 외 사용은 `E0405` (annotation site invalid).

## 8. Open Items / Future v0.7+

v0.6 에서 결정 안 된 항목 — 사용 corpus 후 재검토:

- **Implicit information flow** (§21.6) — covert channel 까지 막는 strict mode
- **Capability composition** — `Clock + Rng` 합성 capability 의 derived interface
- **`#[budget(time_ms)]` static 증명** — LLVM cost model 기반 컴파일타임 예측
- **`spec { forall }` 의 SMT solver 통합** — Z3/CVC5 backend 로 invariant 가 *증명*
  대상 승급
- **`#[error_contract]` 의 row polymorphism** — `Result<T, EmailError | DbError>`
  형 surface
- **Cross-capability flow analysis** — `Clock` 결과가 `Rng` seed 가 되는 패턴의
  추적

각 항목은 `SPEC_GAPS.md` 의 *Open Gaps* 로 등재 (G49+).

---

_본 문서는 v0.6 의 *결정 동결 baseline*. 구현 진행도는 `CHANGELOG_v0.6.md` 가
권위. 실제 챕터 (§20, §21, §3.x 확장) 는 본 PR 또는 후속 PR 에서
[`LANG_SPEC_v0.6/`](.) 디렉토리의 개별 파일로 전개._

---

## 9. Worked Examples — combined v0.6 features

각 예시는 5–8 개의 v0.6 어노테이션을 함께 사용하며, 사용자 멘탈 모델 형성용.

### 9.1 사용자 생성 함수 (capability + sealed + error_contract + intent + spec + reproducible)

```osty
// std.user.osty

#[sealed_construct(parse)]
#[json(constructor = parse, field = "email")]
#[since("0.6")]
#[stability("stable")]
pub struct Email {
    local: String,
    domain: String,
}

impl Email {
    #[purpose("이메일 문자열을 파싱하여 검증된 Email 인스턴스 반환")]
    #[example(input = "alice@example.com", output = "Some(...)")]
    #[example(input = "invalid", output = "None")]
    #[spec("§10.30.user.email")]
    #[reproducible(scope = "portable")]
    pub fn parse(s: String) -> Email? {
        spec {
            example: Email.parse("a@b").isSome()
            example: Email.parse("noatsign").isNone()
            law: result.isSome() implies result.unwrap().toString() == s
        }

        let parts = s.split("@")
        if parts.len() != 2 { return None }
        if parts[0].isEmpty() || parts[1].isEmpty() { return None }
        Some(Email { local: parts[0], domain: parts[1] })
    }

    pub fn local(self) -> String { self.local }
    pub fn domain(self) -> String { self.domain }
}

pub enum UserCreateError {
    EmailFormat,
    DomainBlocked(String),
    DbConflict(Int),
}

#[purpose("이메일 검증 + DB 저장으로 새 사용자 생성")]
#[example(input = "alice@example.com", uses = "sampleDb", output = "Ok(42)")]
#[example(input = "invalid", uses = "sampleDb", output = "Err(UserCreateError.EmailFormat)")]
#[spec("§10.30.user.create")]
#[since("0.6")]
#[stability("stable")]
#[error_contract(
    UserCreateError.EmailFormat    when "Email.parse 실패",
    UserCreateError.DomainBlocked  when "도메인이 deny-list 에 등재",
    UserCreateError.DbConflict     when "이메일 unique 제약 위반",
)]
pub fn createUser(email: String, db: Db) -> Result<UserId, UserCreateError> {
    let e = Email.parse(email).orError(UserCreateError.EmailFormat)?

    if isDomainBlocked(e.domain()) {
        return Err(UserCreateError.DomainBlocked(e.domain()))
    }

    db.insert(e).mapErr(|dbErr| match dbErr {
        DbError.UniqueViolation(id) -> UserCreateError.DbConflict(id),
        _ -> UserCreateError.DbConflict(0),
    })
}

#[fixture(name = "sampleDb")]
fn fakeDb() -> Db { std.testing.db.inMemory() }

#[fixture(name = "alice")]
fn aliceUser() -> Email { Email.parse("alice@example.com")? }
```

**무엇이 보장되는가**:
- `Email` 은 *반드시* `parse` 통과 — 외부 literal `Email { ... }` 불가 (G40)
- `createUser` 호출자는 *세 실패 모드만* 처리하면 exhaustive (G41)
- `Email.parse` 는 plat 무관 동일 동작 (G39 `portable`)
- `osty doc` / `osty context` 가 purpose / examples / error_contract / spec
  inline 표시 (G38, G42)
- `db: Db` capability — 테스트 시 `fakeDb` 주입, production 시 real Db (G36)

### 9.2 Web 라우트 핸들러 (capability + taint + sanitize + budget + match_compat)

```osty
// app/handlers.osty

pub enum HttpEvent {
    Get,
    Post,
    Put,
    Delete,

    #[since("0.6")]
    Patch,                           // v0.6 신규 — 기존 #[match_compat] 가 처리
}

#[purpose("사용자 ID 검색 — SQL injection 방어")]
#[example(input = "alice@example.com", output = "Ok(...)")]
#[spec("§10.24.http.handlers")]
#[since("0.6")]
#[stability("stable")]
#[error_contract(
    HandlerError.NotFound when "users 테이블에 없는 ID",
    HandlerError.DbDown   when "DB 연결 실패",
)]
#[budget(allocs = 4, io_calls = 1, time_ms = 50)]
pub fn lookupUser(
    #[taint("user_input")] userId: String,
    db: Db,
    clock: Clock,
) -> Result<UserSummary, HandlerError> {
    let safe = std.sql.escape(userId)             // G37 sanitize: user_input → sql_safe
    let started = clock.monotonic()

    let rows = db.exec(
        "SELECT id, email FROM users WHERE id = ?",
        [safe],                                    // sink #[requires("sql_safe")] 충족
    ).mapErr(|_| HandlerError.DbDown)?

    if rows.isEmpty() { return Err(HandlerError.NotFound) }

    let user = UserSummary {
        id: rows[0].getInt("id"),
        email: rows[0].getString("email"),
        lookedUpAt: clock.now(),
    }

    Ok(user)
}

#[match_compat("0.6", fallback = handlePatchAsPut, reason = "Patch 는 v0.7 에 정식 핸들러")]
pub fn dispatch(event: HttpEvent) -> Response {
    match event {
        HttpEvent.Get -> handleGet(),
        HttpEvent.Post -> handlePost(),
        HttpEvent.Put -> handlePut(),
        HttpEvent.Delete -> handleDelete(),
    }
    // HttpEvent.Patch (v0.6 신규) 도달 시 fallback = handlePatchAsPut 호출
}

fn handlePatchAsPut() -> Response { handlePut() }
```

**무엇이 보장되는가**:
- `userId` 가 *반드시* sanitizer 경유 후 sink 도달 — 미경유 시 컴파일 에러 (G37)
- `lookupUser` 는 50ms 이내 / 4 alloc / 1 IO call 이내 (G46) — bench 회귀 차단
- v0.6 에 `Patch` variant 가 추가되어도 기존 dispatch 함수가 *조용히 깨지지 않음*
  — `fallback` 으로 명시 routing (G44)
- `db` / `clock` 이 capability — 테스트 시 fake injection 으로 deterministic
  (G36)

---

## 10. Prior art / Bibliography

v0.6 의 결정들이 참조한 학술/산업 선행 사례:

| 결정 | 영향받은 prior art | 비고 |
|---|---|---|
| **G36 Capabilities** | Roc platform model (Feldman 2018+) | Roc 은 platform-driven, Osty 는 ambient-with-explicit. 같은 정신, 다른 ergonomics 균형 |
| | Pony reference capabilities (Clebsch et al., 2015) | Pony 는 *memory aliasing* 용도. Osty G36 은 *effect tracking* 용도 — 카테고리 다름 |
| | Haskell ReaderT / mtl / ZIO | 함수형 effect tracking 의 origin. Osty 는 industrial 문법으로 채택 |
| **G37 Information Flow** | **Jif** (Myers, Liskov 1998–2002) | 산업 IFC 의 origin. Jif 는 Java 확장; Osty 는 mainstream 문법으로 첫 정착 |
| | Flow Caml (Pottier, Simonet 2003) | OCaml IFC. type system 통합 사례 |
| | Perl taint mode (Wall 1990s) | Dynamic / runtime IFC. Osty 는 static. 정신은 동일 |
| | Haskell `Tagged<T, Trust>` newtype 패턴 | 라이브러리-수준 IFC. Osty 는 언어-수준 |
| **G38 Spec link** | Doxygen / JSDoc cross-ref | 도구 측 cross-ref 만. *checked* 는 Osty 첫 시도 |
| **G39 Reproducibility** | Bazel hermetic build | 빌드 시스템 측 reproducibility. Osty 는 *함수* 수준 |
| | Nix purity model | 환경독립 강제 정신 동일 |
| | Rust `#[no_std]` | 환경 의존 제한 패턴 (다른 차원) |
| **G40 Sealed construct** | Haskell smart constructor + module export 관례 | 관례를 *언어 primitive* 로 |
| | F# `private` constructor + smart factory | 동일 |
| | Java sealed class (JEP 409, Java 17) | 다른 의미 — Osty 의 sealed_construct 는 *생성 경로* 제한 |
| **G41 Error contract** | Java `throws` clause | checked exception 의 응용 — but Osty 는 Result-based |
| | Eiffel postcondition | Design by Contract 영향 |
| **G42 Structured intent** | Doxygen `@brief` / `@param` | 자유 텍스트 doc. Osty 는 *machine-readable* |
| | Rust doc tests | `#[example]` 의 자동 검증 패턴 |
| **G43 spec block** | **Eiffel** Design by Contract (Meyer 1986+) | invariant / require / ensure 의 기원 |
| | Dafny (Leino, Microsoft) | spec-as-language-feature 의 학술 가장 가까운 사례 |
| | F* / Liquid Haskell | refinement type 영향 (Osty v1 에서 검토) |
| | QuickCheck (Claessen, Hughes 2000) | property-based testing |
| | Hypothesis (Python) | property test API 영향 |
| **G44 stability + publish** | **Elm package SemVer enforcement** (Czaplicki) | 가장 가까운 선행 — Osty 는 typed compiled 영역에 도입 |
| | Rust `#[stable]` / `#[unstable]` | nightly-gating, crates.io 강제 없음. Osty 는 publish-gating |
| | Java `@Deprecated` / `@Stable` | 메타데이터만, enforce 없음 |
| **G45 Golden** | Insta (Rust) | text-based snapshot 라이브러리 |
| | Jest snapshot (JS) | 동일 카테고리 |
| | AST diff: ts-morph 등 도구 | 산업 사례 부족 — Osty 는 언어 통합 |
| **G46 Budget** | C++ `[[gnu::pure]]` 등 attribute | static 측 영향 |
| | go-perf benchstat regression gate | runtime 측 영향 |
| | LLVM `cost model` | budget(time_ms) static 증명 검토 |
| **G47 Machine context** | LSP `textDocument/hover` | 동일 응용을 LLM 까지 확장 |
| | `cargo metadata` JSON output | 메타데이터 export 정신 |
| **G49 while** | C / Java / Rust / Swift | 가장 흔한 conditional loop. Osty 가 v0.5 에서 부재했던 부분 |

### 10.1 References

```
[Myers 2002]    A. C. Myers, B. Liskov. "Protecting Privacy Using the
                Decentralized Label Model." ACM TOSEM, 2000.
[Pottier 2003]  F. Pottier, V. Simonet. "Information Flow Inference for
                ML." ACM TOPLAS, 2003.
[Clebsch 2015]  S. Clebsch et al. "Deny capabilities for safe, fast
                actors." AGERE 2015.
[Meyer 1986]    B. Meyer. "Design by Contract." Eiffel manuals, 1986+.
[Leino]         K. R. M. Leino. "Dafny: An Automatic Program Verifier."
                LPAR 2010.
[Claessen 2000] K. Claessen, J. Hughes. "QuickCheck: A Lightweight Tool
                for Random Testing of Haskell Programs." ICFP 2000.
```

---

## 11. v0.6 cut readiness checklist

v0.6 baseline 동결 → public 1.0 alpha 출시 까지의 게이트:

### 9.1 Spec readiness (이 문서가 cover)

- [x] G36–G49 (14 결정) 본 문서에 명시
- [x] SPEC_GAPS.md §"Resolved in v0.6" 에 entries 등재
- [x] OSTY_GRAMMAR_v0.6.md grammar delta + R27–R29
- [x] CHANGELOG_v0.6.md skeleton
- [x] Cross-feature interaction matrix (§7.5)
- [x] Annotation site matrix (§7.6)
- [x] Migration code samples (§7.1.1, §7.2.1, §7.3.1)
- [x] `osty context` JSON schema (§13.9)
- [ ] §20 / §21 정식 챕터 파일 (`LANG_SPEC_v0.6/20-capabilities.md` 등)
- [ ] 기존 v0.5 챕터 (§3, §4, §7, §11, §13) 의 v0.6 amend 파일
- [ ] `LANG_SPEC_v0.6/ABRIDGED.md` (agent 용 단축본) v0.6 갱신
- [ ] `LANG_SPEC_v0.6/README.md` reading order 갱신

### 9.2 Implementation readiness (CHANGELOG 가 cover)

- [ ] Phase 0 (Tier A 5 개 LLVM 갭) — *진행 중*
- [ ] Phase 1 capability migration — stdlib 100 PR 분량 순회
- [ ] Phase 2 spec / intent / context — 작은 분량
- [ ] Phase 3 reproducible / spec block / golden
- [ ] Phase 4 sealed / errcontract / evolution / budget(static)
- [ ] Phase 5 taint / budget(runtime) / spec block v1

### 9.3 Tooling readiness

- [ ] `osty validate-spec` (G38)
- [ ] `osty context` (G47, JSON schema 권위 따름)
- [ ] `osty publish` API surface diff (G44)
- [ ] `osty test --golden` / `--update-golden` (G45)
- [ ] `osty test --spec` (G43)
- [ ] `osty test --example` (G42)
- [ ] `osty bench --budget` (G46)
- [ ] `osty audit --trusted-declassify` (G37)
- [ ] `osty audit --trusted-construct` (G40)
- [ ] `osty audit --match-compat` (G44)

### 9.4 Spec corpus readiness

- [ ] `testdata/spec/positive/` 에 G36-G49 별 통과 케이스 추가
- [ ] `testdata/spec/negative/reject.osty` 에 신규 진단 코드 (E0405-E0451,
  E0780-E0796, E0900-E0903, E2100-E2101) 케이스
- [ ] `STDLIB_MATRIX.md` 가 capability migration 후 모듈별 capability requirement
  컬럼 추가
- [ ] `ERROR_CODES.md` regenerate (`go generate ./internal/diag/...`)

### 9.5 Documentation readiness

- [ ] `README.md` v0.6 surface 표 갱신
- [ ] `ARCHITECTURE.md` capability layer 추가
- [ ] `CLAUDE.md` 부록 A (canonical 예시) 에 capability + sealed + taint 패턴 추가
- [ ] `CLAUDE.md` 부록 B (생산성 기법 카탈로그) 에 G36-G49 행 추가
- [ ] *마이그레이션 가이드* 문서 — `MIGRATING_v0.5_to_v0.6.md`

### 9.6 Compatibility / breaking change audit

- [ ] `--legacy-globals` 호환 모드 동작 확인 (v0.6.x 한정)
- [ ] `--legacy-construct` 호환 모드 동작 확인 (v0.6.x 한정)
- [ ] `while` reserved 충돌 audit — 기존 toolchain/* 식별자 사용 검사
- [ ] Stdlib breaking changes 목록 정리 (`BREAKING_v0.6.md`)
- [ ] v0.5 → v0.6 자동 마이그레이션 도구 검토 (`osty fix --to-v0.6`)

### 9.7 Public release prerequisites

- [ ] 위 checklist 의 *모든 항목* checked
- [ ] CI 게이트: 100 PR sprint 결과 + v0.6 spec corpus + STDLIB_MATRIX LLVM E2E
  통과율 ≥ 60%
- [ ] 외부 dogfood: `osty install-self` 가 fresh clone 에서 자력 사이클 닫음
  (Tier A 5 갭 해소)
- [ ] Reproducible build 검증: `verify-self-rebuild` stage1/2/3 byte parity
- [ ] Documentation site 또는 README 의 *왜 Osty?* section 작성 — design north
  star (§1) 기반

이 checklist 를 모두 채우기 전에는 *internal preview* 상태로 유지.
공개 1.0 alpha 는 이 checklist 의 last entry 가 checked 된 후.
