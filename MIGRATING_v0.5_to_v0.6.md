# MIGRATING_v0.5_to_v0.6 — v0.5 → v0.6 사용자 마이그레이션 가이드

> **TL;DR**: v0.6 는 14 결정 (G36–G49) 을 *Hidden dependency is forbidden*
> 원칙으로 추가. 대부분 *additive* 이지만 **3 영역**에서 사용자 코드 수정 필요:
> (1) `while` 식별자 rename, (2) stdlib effect 전역 함수 → capability,
> (3) stdlib sealed types literal → `Type.parse()`. (1) 만 v0.6.0 hard break,
> (2)/(3) 은 v0.6.x compat 모드 → v0.7 hard break.
>
> **Companion**: [`BREAKING_v0.6.md`](./BREAKING_v0.6.md) (catalog), [`LANG_SPEC_v0.6/`](./LANG_SPEC_v0.6/) (권위), [`CLAUDE.md`](./CLAUDE.md) 부록 C (v0.6 패턴).

## 0. 마이그레이션 단계 한눈에

```
┌──────────────────────────────────────────────────────────────────┐
│ Step 1: v0.6.0 install                                            │
│   • 식별자 'while' rename (hard break)                            │
│   • spec keyword 위치 확인 (드물게)                               │
│   • 빌드 통과까지                                                  │
├──────────────────────────────────────────────────────────────────┤
│ Step 2: --legacy-globals warning 끄기                              │
│   • osty check --warnings-as-errors                                │
│   • capability 파라미터 추가                                        │
│   • #[ambient] entry point 만                                      │
├──────────────────────────────────────────────────────────────────┤
│ Step 3: stdlib sealed types 마이그레이션                            │
│   • Email/Url/Path/SqlIdent/Duration/Uuid literal 검색             │
│   • Type.parse(...) 로 전환                                        │
│   • #[json(constructor=parse)] 등록                                │
├──────────────────────────────────────────────────────────────────┤
│ Step 4 (Phase 5 도달 시): taint / sink 검증                         │
│   • 사용자 입력 → SQL/shell/path/url/html 경로 audit               │
│   • 미-sanitize 경로에 sanitizer 또는 parameterized form           │
│   • #[trusted_declassify] 는 audit log 에 등재                     │
├──────────────────────────────────────────────────────────────────┤
│ Step 5: legacy flag 제거 (v0.7 진입 전)                             │
│   • osty.toml 에서 [legacy] 섹션 삭제                              │
│   • CI 에서 --legacy-* flag 제거                                    │
└──────────────────────────────────────────────────────────────────┘
```

각 step 의 detail 은 아래 §1–§5.

---

## 1. `while` 식별자 rename (G49) 🔴

**문제**: `while` 이 v0.6 에서 reserved keyword.

```osty
// before (v0.5 OK)
let while = computeBound()
fn while() -> Int { 0 }
struct Loop {
    while: Bool,            // field name
}
```

**fix**: 한 곳만 rename.

```osty
// after
let bound = computeBound()
fn whileBlock() -> Int { 0 }
struct Loop {
    whileFlag: Bool,
}
```

**자동화**:
```sh
# 워크스페이스 전체에서 식별자 'while' 찾기
rg -nw 'while' --type osty | grep -v 'while .*{' | grep -v '//'
```
(주의: `while cond { }` loop 호출 패턴은 v0.6 에서 정식 syntax 이므로 유지)

**spec**: [BREAKING §1](./BREAKING_v0.6.md#1--reserved-keyword-while-g49), §1.2.

---

## 2. spec block keyword 충돌 확인 (G43) 🔴

**문제**: `spec` 가 top-level `fn` / method body 의 *첫 statement* 위치에서 contextual keyword. closure body, `if` arm, `match` arm, nested block 및 그 외 위치에선 식별자.

**드물지만 충돌**:
```osty
fn foo() {
    spec.method()         // 첫 statement 가 `spec` 식별자 시작 — parse 모호
}
```

**fix**: 첫 statement 를 `let` / `let _` 등으로:
```osty
fn foo() {
    let _ = ()           // 첫 statement
    spec.method()         // 이제 식별자
}
```

또는 식별자 rename.

**탐지**:
```sh
# top-level fn / method body 첫 statement 가 'spec' / 'example' / 'law' / 'invariant' 인 경우
osty check 2>&1 | grep -E "E0440|E0441"
```

**spec**: [BREAKING §2](./BREAKING_v0.6.md#2--reserved-contextual-keywords-spec--example--law--invariant-g43), §1.3, §3.13.

---

## 3. Stdlib effect 전역 → capability 파라미터 (G36) 🟡

**문제**: v0.5 의 `time.now()` / `random.next()` / `env.get()` / `fs.read()` / `os.exec()` / `net.dial()` 가 deprecated. v0.6.x 에선 `--legacy-globals` 호환 모드, v0.7 제거.

### 3.1 라이브러리 함수 전환

**before**:
```osty
pub fn buildId() -> String {
    "{time.now().toEpochMillis()}-{random.next()}"
}

pub fn loadConfig() -> Result<Config, Error> {
    let path = env.get("CONFIG_PATH") ?? "/etc/app.toml"
    let text = fs.readToString(path)?
    json.parse(text)
}

pub fn fetchData(url: String) -> Result<Bytes, Error> {
    let conn = net.dial(parseHost(url), 443)?
    conn.read()
}
```

**after**:
```osty
pub fn buildId(clock: Clock, rng: Rng) -> String {
    "{clock.now().toEpochMillis()}-{rng.next()}"
}

pub fn loadConfig(env: Env, fs: Fs) -> Result<Config, Error> {
    let path = env.get("CONFIG_PATH") ?? "/etc/app.toml"
    let text = fs.readToString(path)?
    json.parse(text)
}

pub fn fetchData(net: Net, url: String) -> Result<Bytes, Error> {
    let conn = net.dial(parseHost(url), 443)?
    conn.read()
}
```

**규칙**:
- 모든 라이브러리 함수는 사용하는 capability 를 *명시 파라미터*로 받음
- capability 이름은 prelude 표준: `clock`, `rng`, `env`, `fs`, `net`, `process`, `console`
- `#[ambient]` 는 라이브러리 코드에선 **금지** (`E0780`)

### 3.2 Entry point — `#[ambient]`

**before**:
```osty
fn main() {
    let id = buildId()
    let cfg = loadConfig()?
    println("id={id}, cfg={cfg}")
}
```

**after**:
```osty
#[ambient(clock, rng, env, fs, console)]
fn main() {
    let id = buildId(clock, rng)              // ambient forward
    let cfg = loadConfig(env, fs)?            // ambient forward
    console.println("id={id}, cfg={cfg}")
}
```

**Script (`#!/usr/bin/env osty`) 의 경우**: 자동 ambient `(clock, rng, env, fs)`.

**테스트**:
```osty
fn test_buildId_format() {
    let fakeClock = std.time.fakeClock(epoch_ms = 1_000_000)
    let fakeRng = std.random.seededRng(seed = 42)
    let id = buildId(fakeClock, fakeRng)
    testing.assertEq(id, "1000000-1608637542")    // deterministic
}
```

### 3.3 호환 모드로 점진 마이그레이션

`osty.toml`:
```toml
[package]
name = "myapp"
version = "0.6.0"

[legacy]
globals = true       # v0.5 전역 함수 호출 자동 desugar (deprecated W0750)
```

또는 build 시:
```sh
osty build --legacy-globals
osty test --legacy-globals
```

**효과**:
- v0.5 의 `time.now()` 류 호출이 `std.time.host.now()` (capability method) 로 자동 desugar
- W0750 deprecation warning 매 호출
- 패키지의 `[stability]` 기본값이 자동 `experimental` 로 강제

**v0.7 에서**: `--legacy-globals` flag 자체가 unknown flag. `[legacy]` 섹션 무시.

### 3.4 마이그레이션 순서 권장

1. `osty check --legacy-globals` 로 deprecation 위치 enumerate
2. *leaf 함수* 부터 마이그레이션 (capability 사용처가 직접 있는 함수)
3. *호출자* 함수에 capability 파라미터 chain
4. `fn main` / 테스트 / script 에 `#[ambient]`
5. legacy flag 제거

**자동 도구 (예정 v0.6.1)**: `osty fix --to-v0.6 --capability` (작성 예정).

**spec**: [BREAKING §3](./BREAKING_v0.6.md#3--stdlib-effect-globals--capability-methods-g36), §20, [`CLAUDE.md` 부록 C.1](./CLAUDE.md).

---

## 4. Stdlib sealed types — `Type.parse()` 로 전환 (G40) 🟡

**문제**: `Email` / `Url` / `Path` / `SqlIdent` / `Duration` / `Uuid` 가 v0.6 에서 `#[sealed_construct]` 화. 외부 struct literal 차단.

### 4.1 Literal → parse

**before**:
```osty
let email = Email { local: "alice", domain: "example.com" }
let url   = Url { scheme: "https", host: "example.com", path: "/", ... }
let path  = Path { components: ["etc", "passwd"], absolute: true }
```

**after**:
```osty
let email = Email.parse("alice@example.com")?
let url   = Url.parse("https://example.com/")?
let path  = Path.parse("/etc/passwd")?
```

### 4.2 Spread update — sub-method 또는 builder

**before**:
```osty
let email2 = Email { ..email, domain: "other.com" }   // E0420 in v0.6
```

**after** (option A — explicit construction):
```osty
let email2 = Email.parse("{email.local()}@other.com")?
```

**after** (option B — builder, type 가 builder 제공 시):
```osty
let email2 = email.toBuilder().domain("other.com").build()
```

### 4.3 JSON deserialize

`#[json(constructor)]` 자동 등록:
```osty
#[sealed_construct(parse)]
#[json(constructor = parse, field = "email")]
pub struct Email { ... }

// json.parse::<Email>(text) 가 자동으로 Email.parse 경유
```

stdlib types 는 v0.6 에서 자동으로 이 등록 됨 — 사용자 측 변경 0.

### 4.4 호환 모드

```sh
osty build --legacy-construct       # sealed 검사 비활성, v0.6.x 한정
```

**v0.7 에서**: 제거.

### 4.5 자동 탐지

```sh
# Email/Url/Path 직접 literal 패턴 검색
rg -n 'Email\s*\{|Url\s*\{|Path\s*\{|SqlIdent\s*\{|Duration\s*\{|Uuid\s*\{' --type osty
```

**spec**: [BREAKING §4](./BREAKING_v0.6.md#4--stdlib-sealed-types--외부-struct-literal-차단-g40), §3.4.5, [`CLAUDE.md` 부록 C.3](./CLAUDE.md).

---

## 5. Information flow rollout (G37 — Phase 5) 🟠

**v0.6.0 시점엔 영향 없음.** Phase 5 (예정 v0.6.4 또는 v0.7-alpha) 도달 시 stdlib sink 가 `#[requires("...")]` 어노테이션을 받아 미-sanitize 코드가 컴파일 에러.

### 5.1 영향 코드 패턴

**Phase 5 부터 컴파일 에러**:
```osty
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    db.query("SELECT * FROM users WHERE id = {id}")    // E0900
}
```

```osty
fn runShell(req: HttpRequest, process: Process) -> Result<String, Error> {
    let cmd = req.queryParam("cmd") ?? "ls"
    process.exec(cmd, [])                                // E0900
}
```

```osty
fn loadFile(req: HttpRequest, fs: Fs) -> Result<Bytes, Error> {
    let path = req.queryParam("file") ?? "default.txt"
    fs.readToBytes(path)                                  // E0900
}
```

### 5.2 Fix 패턴 — 옵션 우선순위

#### A. Parameterized form (가장 권장)

```osty
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    db.exec(
        "SELECT * FROM users WHERE id = ?",
        [id],                                             // tag 와 무관 — sink 가 tag 검사 안 함
    )
}
```

#### B. Sanitizer 경유

```osty
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    let safe = std.sql.escape(id)                         // tag user_input → sql_safe
    db.query("SELECT * FROM users WHERE id = {safe}")     // OK
}
```

| Sink | Sanitizer |
|---|---|
| SQL | `std.sql.escape` |
| Shell | `std.shell.quote` |
| Path | `std.path.normalize` |
| URL | `std.url.encode` |
| HTML | `std.html.escape` |

#### C. Audit-marked declassify (최후)

```osty
#[trusted_declassify(reason = "validated by upstream gateway with WAF")]
fn fromGateway(raw: String) -> String { raw }

fn handler(req: HttpRequest, db: Db) -> Response {
    let id = fromGateway(req.queryParam("id") ?? "")
    db.query("SELECT * FROM users WHERE id = {id}")     // OK — declassified
}
```

`osty audit --trusted-declassify` 로 모든 site enumerate. 보안 review 필수.

### 5.3 사전 audit (Phase 5 전)

v0.6.0 부터 가능:
```sh
# 모든 사용자-입력 → 잠재 sink 경로 lint
osty check --enable=L0090   # 가상 (Phase 5 lint), 실제 명령은 v0.6.4 도입
```

또는 수동:
```sh
# 사용자 입력 source 식별
rg -n 'queryParam|formField|cookie|header' --type osty

# sink 호출 식별
rg -n 'db\.query|process\.exec|fs\.read|http\.redirect|template\.render' --type osty

# 두 결과 사이의 데이터 흐름 review
```

**spec**: [BREAKING §5](./BREAKING_v0.6.md#5--information-flow-sink-rollout-phase-5-g37), §21, [`CLAUDE.md` 부록 C.2](./CLAUDE.md).

---

## 6. Optional adoption — v0.6 신규 기능 (안 써도 무방)

다음은 *strict 추가* — v0.5 코드 영향 0. 점진 적용 권장.

### 6.1 `#[reproducible]` for cache / hash / migration

```osty
#[reproducible(scope = "target")]
fn computeKey(data: Bytes) -> Bytes32 {
    sha256(data)
}
```

§3.11, [`CLAUDE.md` 부록 C.7](./CLAUDE.md).

### 6.2 `#[error_contract]` for public API

```osty
#[error_contract(
    EmailError.Format         when "missing @ or wrong format",
    EmailError.DomainBlocked  when "domain blocked",
)]
pub fn parseEmail(s: String) -> Result<Email, EmailError> { ... }
```

§7.5, [`CLAUDE.md` 부록 C.4](./CLAUDE.md).

### 6.3 `#[purpose]` / `#[example]` / `#[fixture]` for documented APIs

```osty
#[purpose("Parses ISO 8601 duration string")]
#[example(input = "PT1H30M", output = "Some(...)")]
#[example(input = "invalid", output = "None")]
pub fn parseDuration(s: String) -> Duration? { ... }
```

§3.12, [`CLAUDE.md` 부록 C.5](./CLAUDE.md).

### 6.4 `spec { example: }` for spec'd functions

```osty
fn normalize(s: String) -> String {
    spec {
        example: normalize(" Hi ") == "hi"
        law: result == result.trim().toLowerCase()
    }
    s.trim().toLowerCase()
}
```

§3.13, [`CLAUDE.md` 부록 C.6](./CLAUDE.md).

### 6.5 `#[stability]` / `#[since]` for public API

```osty
#[stability("stable")]
#[since("0.6")]
pub fn parseEmail(s: String) -> Email? { ... }
```

§3.14, [`CLAUDE.md` 부록 C.9](./CLAUDE.md).

### 6.6 `#[golden]` for compiler / formatter / docgen tests

```osty
#[golden("fixtures/format_expr.snap", mode = "ast")]
fn testFormatBinaryOp() {
    let result = formatExpr(parseExpr("1 + 2 * 3"))
    testing.assertGolden(result)
}
```

§11.5.2, [`CLAUDE.md` 부록 C.10](./CLAUDE.md).

### 6.7 `while` keyword

```osty
while !queue.isEmpty() {
    process(queue.pop()?)
}
// 'for cond { ... }' 도 그대로 작동 — 동의어
```

§4.4, [`CLAUDE.md` 부록 C.11](./CLAUDE.md).

---

## 7. Rollback 전략

마이그레이션 중 문제 발생 시:

1. **CI 실패 / 빌드 깨짐** → `osty.toml` 의 `[legacy]` 섹션 활성화로 임시 우회
2. **테스트 회귀** → `--legacy-globals --legacy-construct` 로 buy 시간 (v0.6.x 안에서만)
3. **stable API breaking 의심** → `osty publish --dry-run` 으로 surface diff 확인
4. **v0.7 앞두고 legacy flag 사용 중** → migration 데드라인 결정 후 phase별 작업

## 8. CI 통합 권장

`.osty/ci.toml` (예시):
```toml
[checks]
# v0.6.0
disallow_legacy_while = true              # G49 — hard
disallow_legacy_globals = false           # 임시 허용 (마이그레이션 중)

# v0.6.x 점진
require_capability_args = true            # 라이브러리 함수의 capability 명시 강제
```

`just prepush` 게이트:
```sh
osty check
osty audit --legacy-globals --legacy-construct --report > legacy-usage.txt
osty publish --dry-run                     # surface diff sanity
```

---

## 9. Checklist (마이그레이션 pre-flight)

v0.6 머지 전:
- [ ] `osty check --warnings-as-errors` 통과
- [ ] `osty audit --legacy-globals` 0 entries (또는 명시적 deferred)
- [ ] `osty audit --legacy-construct` 0 entries
- [ ] `osty audit --trusted-declassify` 모든 site 가 reason 적정
- [ ] `osty publish --dry-run` 의 surface diff 가 의도된 변경
- [ ] CI 가 `--legacy-*` flag 없이 통과
- [ ] 테스트 coverage 가 이전 수준 유지

## 10. 도움 / 트러블슈팅

- **Spec 본문**: [`LANG_SPEC_v0.6/`](./LANG_SPEC_v0.6/), [`OSTY_GRAMMAR_v0.6.md`](./OSTY_GRAMMAR_v0.6.md)
- **Decision log**: [`SPEC_GAPS.md`](./SPEC_GAPS.md) §"Resolved in v0.6", [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md), [`LANG_SPEC_v0.6/18-change-history.md`](./LANG_SPEC_v0.6/18-change-history.md) §18.0
- **진단 코드**: [`ERROR_CODES.md`](./ERROR_CODES.md) (E0780+, E0900+, E2100+)
- **Implementation 진행도**: [`CHANGELOG_v0.6.md`](./CHANGELOG_v0.6.md)
- **Agent 패턴**: [`CLAUDE.md`](./CLAUDE.md) 부록 C
