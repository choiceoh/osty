## 21. Information Flow Tracking

> v0.6 G37. *Hidden dependency is forbidden* design north star
> 의 *security* 축. mainstream 산업언어 첫 정적 IFC.

### 21.1 동기

OWASP Top 10 의 SQLi / XSS / 명령 주입 / 경로 traversal / SSRF 가 모두 **"sanitize
하지 않은 사용자 입력이 sink 에 도달했나"** 한 질문으로 환원된다. 산업언어 중
mainstream 에 정적 IFC (Information Flow Control) 를 가진 사례 없음. v0.6 은
**1-bit + N-tag flow tracking** 을 type system 에 추가한다.

### 21.2 Surface

세 어노테이션:

```osty
// (1) Source — 데이터 진입점 표시
#[taint("user_input")]
pub fn readForm() -> String { ... }

// (2) Sanitizer — taint 제거, 새 trust 부여
#[sanitizes("user_input", into = "sql_safe")]
pub fn sqlIdent(s: String) -> SqlIdent? { ... }

// (3) Sink — 특정 trust 요구
pub fn query(table: #[requires("sql_safe")] SqlIdent) -> Rows { ... }
```

#### 21.2.1 Annotation argument grammar

세 annotation 모두 *string literal* 만 인자로 받는다. 표현식이나
식별자 인자는 거부 (`E0904`). 이 제약은 두 가지를 보장한다:

1. **Type-checker가 컴파일 시점에 tag 식별** — runtime evaluation
   이나 reflection 없이 결정.
2. **Audit 도구가 grep 가능** — `osty audit` 가 어떤 source / sink
   인지 알기 위해 type checker 호출 필요 없음.

```osty
// ✅ String literal — 합법.
#[taint("user_input")]

// ❌ Identifier — 거부 (E0904).
const tag = "user_input"
#[taint(tag)]

// ❌ String concatenation — 거부.
#[taint("user_" + "input")]
```

#### 21.2.2 Annotation 위치 규칙

- `#[taint(t)]` — 함수 반환 위치 (선언 앞). 함수 결과 전체에 tag 부여.
- `#[sanitizes(t, into = u)]` — 함수 반환 위치 (선언 앞). 입력의
  tag `t` 가 출력의 tag `u` 로 변환.
- `#[requires(u)]` — *parameter* 위치 (Pattern 앞 또는 Type 앞).
  parameter 별로 다른 trust 요구 가능.
- `#[taint(t)]` — parameter 위치 (Pattern 앞 또는 Type 앞)도 가능.
  *입력*에 tag 부여 — 데이터가 함수 안에 들어올 때 이미 tagged
  임을 callee 측에서 attest.

함수 *body* 내부에는 어떠한 flow annotation 도 attach 불가 — 이는
declaration-level annotation 의 일관된 규칙 (§3.8 positioning rules).

#### 21.2.3 Multiple tags

한 declaration 에 같은 종류의 annotation 을 여러 번 적용 가능 —
tag set 의 합집합으로 해석:

```osty
// 두 source tag 모두 부여 — 결과에 {user_input, web_input} 둘 다
#[taint("user_input")]
#[taint("web_input")]
pub fn readBody(req: HttpRequest) -> String { ... }

// 두 trust tag 모두 요구 — 입력에 {sql_safe, web_safe} 모두 있어야
pub fn unsafeStore(value: #[requires("sql_safe")] #[requires("web_safe")] String) { ... }
```

`#[sanitizes]` 는 다른 의미: 첫 인자의 tag 를 제거, 두 번째의
tag 를 추가. 여러 sanitizer annotation 은 *순차* 적용:

```osty
#[sanitizes("user_input", into = "url_safe")]
#[sanitizes("user_input", into = "html_safe")]   // 두 번째 호출 — 같은 source 다른 trust
pub fn doubleSanitize(s: String) -> String { ... }
```

이 형태는 흔하지 않으며, 일반적으로는 하나의 `#[sanitizes]` 가
충분하다.

### 21.3 의미론

타입에 *flow tag set* 이 첨부된다 (concrete syntax 노출 없음 — annotation 으로만
표현). 규칙:

1. `#[taint(t)]` 함수의 반환값은 tag `t` 가 추가된다.
2. Tagged 값을 받은 함수의 반환값은 tag 가 **자동 propagate** 된다 (transitive).
3. `#[sanitizes(t, into = u)]` 함수는 tag `t` 를 제거하고 tag `u` 를 추가한다.
4. `#[requires(u)]` 위치에 tag `u` 가 없는 값을 전달하면 `E0900` (sink violation).
5. 서로 다른 source 의 taint 는 *합집합* — `#[taint("a")]` 와 `#[taint("b")]` 결과
   를 concatenate 하면 결과는 `{a, b}` tag.

### 21.4 예제

```osty
// 사용자 입력 → sql sink
fn handleQuery(form: #[taint("user_input")] String) -> Rows {
    db.query("SELECT * FROM users WHERE id = {form}")
    //         ^^^^^^^^^^^^^^ E0900: tainted value reaches sql sink
}

// Sanitize 후 sink
fn handleQuerySafe(form: #[taint("user_input")] String) -> Rows {
    let safe = sqlIdent(form)?
    db.query("SELECT * FROM users WHERE id = {safe}")    // OK
}

// Sanitize 가 잘못된 trust 부여
#[sanitizes("user_input", into = "html_safe")]
fn htmlEscape(s: String) -> String { ... }

fn handleHtmlInSql(form: #[taint("user_input")] String) -> Rows {
    let html = htmlEscape(form)
    db.query("SELECT * FROM users WHERE id = {html}")
    //         ^^^^^^^^^^^^^^ E0900: html_safe ≠ sql_safe
}
```

### 21.5 Generic / closure / struct 처리

**Generic**: Osty 는 monomorphize 하므로 generic 함수의 instance 별로 tag 흐름이
결정된다.

```osty
fn id<T>(x: T) -> T { x }

fn handler(form: #[taint("user_input")] String) {
    let copy = id(form)              // copy: { user_input } 유지
    db.query("... {copy}")            // E0900
}
```

**Closure**: capture 된 tagged 값은 closure 결과 type 에 propagate.

```osty
fn process(form: #[taint("user_input")] String) -> fn() -> String {
    || form.toUpperCase()            // 결과 closure 호출 시 반환값에 user_input tag
}
```

**Struct**: tagged 값을 struct 필드로 저장하면 *struct 인스턴스 자체* 에 tag 가
첨부된다. 필드 접근으로 *나오는* 값에도 같은 tag.

```osty
struct Form {
    email: String,
    name: String,
}

fn build(emailIn: #[taint("user_input")] String) -> Form {
    Form { email: emailIn, name: "anon" }
    // 결과 Form 은 { user_input } tag — 어떤 필드든 sink 도달 시 차단
}

fn use(f: Form) {
    db.query("... {f.email}")    // E0900 — Form 에서 tag 전파
    db.query("... {f.name}")      // E0900 — 같은 tag, 모든 필드 affected
}
```

**필드 별 tag 분리 (선택적)**: struct 단위 fold 가 false-positive 가 잦다면
필드별 어노테이션으로 narrow 가능 (Phase 5 옵션):

```osty
struct Form {
    #[taint_field("user_input")]
    email: String,
    name: String,                    // tag 없음
}

// 이 모드에선 f.email 만 tagged, f.name 은 untagged
```

**기본은 struct 단위 fold (sound default)**, `#[taint_field]` 는 명시적 narrow.

### 21.5.1 Container / collection

`List<T>` / `Map<K, V>` / `Set<T>` 가 tagged 값을 담으면 collection 자체에 tag.
요소 추출 시 같은 tag 유지.

```osty
let inputs: List<#[taint("user_input")] String> = ...
for s in inputs {
    db.query(s)   // E0900 — element 가 tagged
}
```

### 21.5.2 Result / Option

`Result<T, E>` / `Option<T>` 의 `Ok(...)` / `Some(...)` 안에 tagged 값이 들어가면
unwrap (`?`) 후에도 tag 유지.

```osty
fn fetch() -> Result<#[taint("network")] String, NetError> { ... }

fn handler() -> Result<(), Error> {
    let body = fetch()?              // body: { network } tag
    db.query(body)                    // E0900
    Ok(())
}
```

### 21.5.3 Type tag propagation — formal inference rules

타입 `T` 는 *tag set* `A ⊆ Tags` 를 운반한다 — 표기 `T@A`. 어노테이션 없는 위치는
`A = ∅` (clean). 다음 inference rule 들이 propagation 의 권위:

```
─────────────────────────────────                            (T-Const)
   Γ ⊢ literal : T@∅


   Γ ⊢ e : T@A       (T-Forward, 어노테이션 없는 통과 함수)
   f : T -> U
─────────────────────────
   Γ ⊢ f(e) : U@A


   Γ ⊢ e1 : T1@A1     Γ ⊢ e2 : T2@A2     ...     (T-Combine, n-ary)
   f : T1 × T2 × ... × Tn -> U
─────────────────────────────────────────────────
   Γ ⊢ f(e1, e2, ..., en) : U@(A1 ∪ A2 ∪ ... ∪ An)


   f 의 선언이 #[taint("σ")] 부착                            (T-Source)
   f : () -> T
─────────────────────────────────
   f : () -> T@{σ}        (실제 시그니처)


   f 의 선언이 #[sanitizes("σ", into = "τ")] 부착             (T-Sanitize)
   f : T -> U      σ ∈ A
─────────────────────────────────────────
   Γ ⊢ f(e:T@A) : U@((A \ {σ}) ∪ {τ})


   f 의 인자 위치에 #[requires("τ")] 부착                     (T-Sink)
   Γ ⊢ e : T@A         τ ∈ A
─────────────────────────────────
   Γ ⊢ f(e) : U                  (호출 OK)

   Γ ⊢ e : T@A         τ ∉ A
─────────────────────────────────
   E0900: tainted value reaches sink (호출 거부)


   Γ ⊢ s : Struct { f1: T1@A1, ..., fn: Tn@An }              (T-Struct)
   #[taint_field] 어노테이션 없음
─────────────────────────────────────────────────────
   tag(s) = A1 ∪ A2 ∪ ... ∪ An       (struct 단위 fold)
   ∀i.  Γ ⊢ s.fi : Ti@(A1 ∪ ... ∪ An)


   Γ ⊢ s : Struct { f1: T1@A1, ..., fn: Tn@An }              (T-Struct-Field)
   필드 fi 만 #[taint_field("σ")] 어노테이션
─────────────────────────────────────────────────────
   Γ ⊢ s.fi : Ti@({σ} ∪ Ai)          (해당 필드만 narrow)
   ∀j ≠ i. Γ ⊢ s.fj : Tj@Aj


   Γ ⊢ e : List<T@A>                                         (T-Container)
   xs : List<T@A>
─────────────────────────────────
   Γ ⊢ xs[i] : T@A
   ∀x ∈ xs.  Γ ⊢ x : T@A


   Γ ⊢ e : Result<T@A, E@B>                                  (T-Result)
─────────────────────────────────────────
   Γ ⊢ e? : T@A         (Ok 분기 — A 보존)
                        (Err 분기 — B 운반하며 enclosing 의 Err 로 propagate)


   λ |x: T@A| -> body  closure                              (T-Closure)
   capture set: { y_i : U_i@B_i }
─────────────────────────────────────────
   closure type : (T@A) -> R@(A ∪ B_1 ∪ ... ∪ B_k)
                  (capture 된 모든 tag 결과에 합집합)


   Γ ⊢ e : T@A      f<X> : X -> X      (T-Generic-Mono)
   monomorphization: X = T@A
─────────────────────────────────
   Γ ⊢ f::<T@A>(e) : T@A         (tag 가 generic instance 와 함께 propagate)
```

### 21.5.4 Subtyping 와 lattice

Tag set 은 powerset lattice (⊆ 순서) 이지만 *subtyping 방향은 sink 측 요구의
역방향*:

```
   Γ ⊢ e : T@A      A ⊇ τ-required-set       (T-Subsume-Sink)
─────────────────────────────────────────
   Γ ⊢ e : T (sink-acceptable)
```

**중요**: tag 추가는 *제약 강화* 가 아니라 *제공 정보 추가*. 더 많은 tag 가 붙은
값은 더 많은 sink 요구 만족 가능. *제거*는 sanitize 를 통해서만 가능.

### 21.5.5 Soundness 약속

이 규칙 set 은 다음 invariant 를 보장 — *증명 의무는 v0.7+ 의 Open Item* (formal
proof in mechanized assistant 검토):

> **No-Bypass Invariant**: 만약 `db.query(s)` call site 가 컴파일 통과했다면,
> `s` 의 source-to-sink path 위 어딘가에 `#[sanitizes(σ_user_input, into =
> sql_safe)]` 또는 `#[trusted_declassify]` 가 존재한다.

### 21.6 Implicit flow 미지원 — 정책

다음은 **explicit flow only** 정책을 채택한다 (Jif [Myers 2002] 와 동일 결정):

```osty
fn leak(token: #[taint("secret")] String) -> Bool {
    if token.startsWith("admin") {
        true       // 결과는 tainted 가 아니다 (false-positive 폭발 회피)
    } else {
        false
    }
}
```

이는 *covert channel* 을 막지 못하지만, 실용 영역에서는 explicit flow 만으로 OWASP
대부분을 cover. Implicit flow 는 v0.7+ 에서 옵션으로 검토.

### 21.7 FFI 경계

`use go "..." { ... }` 또는 §19 runtime 측을 통한 데이터는 *untagged* 로 시작.
명시적 declassify 가 필요하면:

```osty
#[trusted_declassify("user_input", reason = "validated by Go-side parser")]
fn fromGoParser() -> String { ... }
```

`#[trusted_declassify]` 는 *human review 표식* — 컴파일러는 이를 신뢰하고 tag 제거.
모든 사용처는 `osty audit --trusted-declassify` 로 enumerate 가능.

### 21.8 Stdlib sink 카탈로그 (v0.6 baseline)

| Sink | Required tag |
|---|---|
| `db.query`, `db.exec` | `sql_safe` |
| `process.exec`, `process.spawn` | `shell_safe` |
| `fs.read*`, `fs.write*`, `fs.open` | `path_safe` |
| `http.redirect`, `http.fetch` | `url_safe` |
| `http.respondHtml` | `html_safe` |
| `http.respondJson` | (없음 — JSON encoder 가 escape) |
| `template.render` | `html_safe` (default) |

stdlib 내 sanitizer 도 함께 제공: `sql.escape` / `shell.quote` /
`path.normalize` / `url.encode` / `html.escape`.

### 21.9 진단 코드

| 코드 | 의미 |
|---|---|
| `E0900` | Sink violation — tainted value reaches `#[requires]` |
| `E0901` | `#[taint]` tag 가 알려지지 않음 |
| `E0902` | `#[sanitizes(t, into = u)]` 의 t 가 가능 source 에 없음 |
| `E0903` | `#[requires]` 가 stdlib sink 카탈로그 외부 tag 사용 |
| `W0901` | `#[trusted_declassify]` 사용 (audit hint) |

---


### 21.10 Tag taxonomy

v0.6 의 정보 흐름 태그는 두 부류로 나뉜다 — *source tags* (데이터의
출처를 표시) 와 *trust tags* (sanitization 통과 사실을 표시). 두
부류 모두 일반 `String` literal 이며 — 컴파일러가 이름을 hardcode
하지 않으므로 사용자 정의 영역도 같은 메커니즘으로 작동한다.

#### 21.10.1 Stdlib source tags (v0.6 baseline)

| Tag | 발생 위치 | 의도 |
|---|---|---|
| `"user_input"` | `HttpRequest.queryParam`, `formField`, `cookie`, `header` | 신뢰 불가능한 외부 입력 |
| `"network_payload"` | `Net` capability 에서 읽은 raw bytes | TLS/MTLS 검증 후에도 application-level 로 untrusted |
| `"filesystem_content"` | `Fs.readToString`, `readToBytes` 의 반환 (사용자 controlled path 일 때) | 디스크 내용은 누가 작성했는지 모를 수 있음 |
| `"env_var"` | `Env.get`, `Env.vars` 의 반환 | 사용자 환경에서 주입 가능 |
| `"command_output"` | `Process.exec`, `Process.spawn` 의 stdout/stderr | 외부 프로세스 출력 |
| `"deserialized"` | `json.parse`, `toml.parse` 등 외부 직렬화 입력 | schema 검증 전 raw 데이터 |

stdlib 의 source 어노테이션은 *Phase 5* 에 일괄 적용. 이전 phase 에선
의도된 source 만 사용자가 명시한다.

#### 21.10.2 Stdlib trust tags (v0.6 baseline)

| Tag | 의미 | 발화 위치 (sanitizer) |
|---|---|---|
| `"sql_safe"` | SQL identifier / string literal escape 완료 | `std.sql.escape`, `SqlIdent.parse` |
| `"shell_safe"` | shell metacharacter 이스케이프 완료 | `std.shell.quote`, `Path.parse` |
| `"path_safe"` | path traversal sanitize 완료 (no `..` segments) | `std.path.normalize`, `Path.parse` |
| `"url_safe"` | URL 인코딩 완료 (RFC 3986) | `std.url.encode`, `Url.parse` |
| `"html_safe"` | HTML entity escape 완료 | `std.html.escape`, `template.htmlEscape` |
| `"json_safe"` | JSON serialize-ready (UTF-8 + valid escape) | `json.encode` (자동 발화) |

trust tag 는 *sanitizer 의 출력에만* 부착된다. 사용자 코드가 임의로
trust tag 를 부여할 수는 없다 — `#[sanitizes(... into = "X")]`
선언만 가능.

#### 21.10.3 Tag set 의 부분 순서

`#[requires("τ")]` 위치는 *value 의 tag set 이 τ 를 포함*해야 통과.
정확한 규칙은 §21.5.4 의 lattice rule. 한 값이 여러 trust tag 를
운반할 수 있다 — `sqlIdent(s)` 후 `htmlEscape(...)` 거치면 `{sql_safe,
html_safe}` 동시 운반.

source tag 는 *sanitize 통과 시 제거*된다 (`sanitizes("user_input",
into = "sql_safe")` → `user_input` 제거 + `sql_safe` 추가). 그러므로
일반 흐름은 `{user_input}` → `{sql_safe}` 순.

#### 21.10.4 사용자 정의 tag

stdlib 외부에서도 새 tag 를 도입할 수 있다 — `#[taint("my_app_secret")]`
같은 형식. 새 tag 를 sink 에서 검증하려면 `#[requires("my_app_secret")]`
도 사용자 정의 함수 시그니처에서 사용. 단 컴파일러는 *해당 tag 를
생산하는 source 가 어디든 등록*되어 있어야 한다 — 미등록 시 `E0901`.

```osty
// 사용자 정의 source
#[taint("session_token")]
fn extractSessionToken(req: HttpRequest) -> String? {
    req.cookie("session")
}

// 사용자 정의 sanitizer (signing 검증)
#[sanitizes("session_token", into = "verified_session")]
fn verifySession(token: String) -> Session? { ... }

// 사용자 정의 sink
fn loadUserData(
    db: Db,
    sessionId: #[requires("verified_session")] String,
) -> User? {
    db.query("SELECT * FROM users WHERE session_id = ?", [sessionId])
}
```

### 21.11 Sanitizer registry — `std.sanitize`

v0.6 stdlib 의 canonical sanitizer 목록. 모든 함수는
`#[sanitizes(..., into = ...)]` 어노테이션 부착이 보장되며, 그
출력은 sink 가 즉시 받을 수 있다.

#### 21.11.1 `std.sql.escape`

```osty
#[sanitizes("user_input", into = "sql_safe")]
pub fn escape(s: String) -> String { ... }
```

PostgreSQL / MySQL / SQLite 공통 string-literal escape 규칙 — `'` →
`''`, NUL byte 거부, UTF-8 검증. 식별자 escape 는 `SqlIdent.parse`
(아래 §21.11.7) 사용 — sealed type 이라 외부 literal 차단됨.

#### 21.11.2 `std.shell.quote`

```osty
#[sanitizes("user_input", into = "shell_safe")]
pub fn quote(s: String) -> String { ... }
```

POSIX `sh` / `bash` / `dash` 공통 — single-quote wrap + embedded
single-quote 처리 (`it's` → `'it'\''s'`). Windows `cmd.exe` 는 별도
sanitizer (현재 stdlib 미제공, follow-up).

#### 21.11.3 `std.path.normalize`

```osty
#[sanitizes("user_input", into = "path_safe")]
pub fn normalize(s: String) -> Result<Path, PathError> { ... }
```

`..` segment 거부 (path traversal 차단), absolute path 정규화, NUL
byte 거부, OS-specific 분리자 통일. 결과는 sealed `Path` 타입.

#### 21.11.4 `std.url.encode`

```osty
#[sanitizes("user_input", into = "url_safe")]
pub fn encode(s: String) -> String { ... }
```

RFC 3986 percent-encoding — reserved character + non-ASCII 모두
`%NN` 로 변환. URL component 별 (path / query / fragment) variant 는
`encodePath` / `encodeQuery` / `encodeFragment`.

#### 21.11.5 `std.html.escape`

```osty
#[sanitizes("user_input", into = "html_safe")]
pub fn escape(s: String) -> String { ... }
```

`<` `>` `&` `"` `'` HTML entity escape. Attribute context 와 element
content 의 escape 규칙은 동일하다 (모든 5 문자 escape).

#### 21.11.6 `std.template.htmlEscape`

`std.html.escape` 의 alias — template engine 내부에서 안전한 자동
escape 을 호출하는 위치이다.

#### 21.11.7 Sealed type parse 도 sanitizer 역할

`#[sealed_construct(parse)]` types — `Email`, `Url`, `Path`,
`SqlIdent`, `Duration`, `Uuid` — 의 `parse` 는 *암묵적으로* 다음
어노테이션이 부착되어 있다:

```osty
#[sanitizes("user_input", into = "<type>_safe")]
pub fn parse(s: String) -> Self?
```

따라서 `Email.parse(form_email)?` 의 결과는 `email_safe` tag 를
운반하며, `Email` 받는 sink 는 `#[requires("email_safe")]` 또는
default (untagged Email) 둘 다 허용.

### 21.12 Worked attack scenarios

각 시나리오: **vulnerable** → **컴파일 에러** → **fixed** (3 옵션).

#### 21.12.1 SQL injection

```osty
// vulnerable — Phase 5 부터 컴파일 에러
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    let rows = db.query("SELECT * FROM users WHERE id = {id}")
    //                                                    ^^^ E0900
    Response.ok(rows.toJson())
}
```

```osty
// option A — parameterized (권장)
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    let rows = db.exec("SELECT * FROM users WHERE id = ?", [id])
    Response.ok(rows.toJson())
}

// option B — sanitizer
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    let safe = std.sql.escape(id)
    let rows = db.query("SELECT * FROM users WHERE id = {safe}")
    Response.ok(rows.toJson())
}

// option C — sealed type
fn handler(req: HttpRequest, db: Db) -> Result<Response, Error> {
    let raw = req.queryParam("id") ?? ""
    let id = SqlIdent.parse(raw).orError(BadRequestError)?
    let rows = db.query("SELECT * FROM users WHERE id = {id}")
    Ok(Response.ok(rows.toJson()))
}
```

#### 21.12.2 Command injection

```osty
// vulnerable
fn runCommand(req: HttpRequest, process: Process) -> Result<String, Error> {
    let cmd = req.queryParam("cmd") ?? "ls"
    process.exec(cmd, []).map(|out| out.stdout)
    //           ^^^ E0900 — `cmd` parameter requires shell_safe
}
```

```osty
// option A — fixed command, dynamic args
fn runCommand(req: HttpRequest, process: Process) -> Result<String, Error> {
    let arg = req.queryParam("dir") ?? "."
    process.exec("ls", [arg]).map(|out| out.stdout)
    //                  ^^^ first arg list element is shell_safe via Process.exec
    //                  ^^^ contract — see §20.9.6.
}

// option B — quote sanitizer
fn runCommand(req: HttpRequest, process: Process) -> Result<String, Error> {
    let raw = req.queryParam("cmd") ?? "ls"
    let cmd = std.shell.quote(raw)
    process.exec("/bin/sh", ["-c", cmd]).map(|out| out.stdout)
}
```

#### 21.12.3 Path traversal

```osty
// vulnerable
fn loadFile(req: HttpRequest, fs: Fs) -> Result<Bytes, Error> {
    let path = req.queryParam("file") ?? "default.txt"
    fs.readToBytes(path)   // E0900 — `path` requires path_safe
}
```

```osty
// option A — Path.parse (path_safe trust + traversal 차단)
fn loadFile(req: HttpRequest, fs: Fs) -> Result<Bytes, Error> {
    let raw = req.queryParam("file") ?? "default.txt"
    let safe = Path.parse(raw).orError(BadPathError)?
    fs.readToBytes(safe)   // OK — Path inputs to Fs are path_safe
}

// option B — fixed base + segment join
fn loadFile(req: HttpRequest, fs: Fs) -> Result<Bytes, Error> {
    let segment = req.queryParam("file") ?? "default.txt"
    let base = Path.parse("/var/data")?
    let safe = base.join(segment)?  // join validates segments
    fs.readToBytes(safe)
}
```

#### 21.12.4 Open redirect

```osty
// vulnerable
fn redirect(req: HttpRequest, http: HttpClient) -> Response {
    let target = req.queryParam("next") ?? "/"
    http.redirect(target)  // E0900 — `target` requires url_safe
}
```

```osty
// option A — allowlist
fn redirect(req: HttpRequest, http: HttpClient) -> Response {
    let target = req.queryParam("next") ?? "/"
    if !target.startsWith("/") { return Response.badRequest() }
    let safe = std.url.encode(target)
    http.redirect(safe)
}

// option B — Url.parse + host allowlist
fn redirect(req: HttpRequest, http: HttpClient) -> Response {
    let raw = req.queryParam("next") ?? "/"
    match Url.parse(raw) {
        Some(url) if isAllowedHost(url.host()) -> http.redirect(url),
        _ -> Response.badRequest(),
    }
}
```

#### 21.12.5 XSS in template rendering

```osty
// vulnerable
fn renderProfile(req: HttpRequest, template: Template) -> Response {
    let name = req.queryParam("name") ?? "anon"
    template.render("profile.html", { "userName": name })
    //                                              ^^^ E0900 — template values require html_safe
}
```

```osty
// option A — html.escape
fn renderProfile(req: HttpRequest, template: Template) -> Response {
    let name = req.queryParam("name") ?? "anon"
    let safe = std.html.escape(name)
    template.render("profile.html", { "userName": safe })
}

// option B — auto-escape template (template engine 자동 escape 속성)
fn renderProfile(req: HttpRequest, template: AutoEscapeTemplate) -> Response {
    let name = req.queryParam("name") ?? "anon"
    template.render("profile.html", { "userName": name })
    //                                              ^^^ AutoEscapeTemplate.render
    //                                                   wrap 내부 sanitizes
}
```

### 21.13 Tag propagation through generics, closures, collections

#### 21.13.1 Generic identity

```osty
fn id<T>(x: T) -> T { x }

fn handler(form: #[taint("user_input")] String, db: Db) {
    let copy = id(form)               // copy: { user_input } 유지
    db.query("... {copy}")            // E0900
}
```

`T` 의 monomorphized instance 가 tagged type 이면 `id` 의 인자/반환
모두 같은 tag set 운반. 컴파일러는 generic instance 별로 type tag
flow 를 결정.

#### 21.13.2 Closure capture

```osty
fn process(form: #[taint("user_input")] String) -> fn() -> String {
    || form.toUpperCase()              // closure 의 반환 type: { user_input }
}

fn handler(form: #[taint("user_input")] String, db: Db) {
    let f = process(form)
    let out = f()                       // out: { user_input }
    db.query("... {out}")               // E0900
}
```

closure 가 capture 한 모든 tagged value 의 tag 가 closure 의 반환
type 에 합쳐진다.

#### 21.13.3 Collection propagation

```osty
fn handler(req: HttpRequest, db: Db) {
    let names: List<#[taint("user_input")] String> = [
        req.queryParam("a") ?? "",
        req.queryParam("b") ?? "",
    ]
    for name in names {
        db.query("... {name}")          // E0900 (each iteration)
    }
}
```

`List<T@A>` 의 element access (`xs[i]`, `for x in xs`) 는 `T@A` 를
반환. `Map<K, V@A>` 의 value access 도 동일.

#### 21.13.4 Result/Option

```osty
fn fetch(net: Net, url: String) -> Result<#[taint("network_payload")] String, NetError> { ... }

fn handler(net: Net, db: Db) -> Result<(), Error> {
    let body = fetch(net, "https://x.example")?    // body: { network_payload }
    db.query("... {body}")                          // E0900
    Ok(())
}
```

`?` operator 의 Ok-arm payload 가 같은 tag 운반. Err-arm 의 tag 는
caller 의 Err 로 propagate (caller's Err type 이 tagged 면).

#### 21.13.5 Struct field

§21.5 의 default rule: struct 단위 fold. `#[taint_field]` 로 필드별
narrow 가능.

```osty
struct Form {
    email: String,
    name: String,
}

fn build(emailIn: #[taint("user_input")] String) -> Form {
    Form { email: emailIn, name: "anon" }
    // 결과 Form: { user_input } 전체 fold
}

fn use(f: Form, db: Db) {
    db.query("... {f.name}")       // E0900 — name 도 fold 영향
}
```

```osty
// narrow with #[taint_field]
struct Form {
    #[taint_field("user_input")]
    email: String,
    name: String,
}

fn use(f: Form, db: Db) {
    db.query("... {f.email}")      // E0900
    db.query("... {f.name}")       // OK — narrow fold
}
```

### 21.14 FFI declassify policy

`#[trusted_declassify(reason = "...")]` 는 *audit-marked drop* 이며,
다음 두 의미를 가진다:
1. 컴파일러가 해당 함수의 반환 type 에서 모든 source tag 를 제거
2. `osty audit --trusted-declassify` 가 reason 과 함께 site 를
   enumerate

`reason` 문자열은 *human-readable audit trail* — vocabulary 는
표준화 되지 않았으나 다음 prefix 를 권장:

| Reason prefix | 사용 케이스 |
|---|---|
| `"validated by ..."` | 외부 검증기 통과 (e.g., upstream WAF, schema validator) |
| `"hard-coded ..."` | 컴파일타임 상수에서 옴 |
| `"signed by ..."` | 디지털 서명 검증 통과 |
| `"safe by construction ..."` | type system 외부 invariant |
| `"FFI from trusted system call ..."` | Go syscall 류 |

```osty
#[trusted_declassify(reason = "validated by upstream WAF")]
fn fromWaf(raw: String) -> String { raw }

#[trusted_declassify(reason = "hard-coded UUID for system user")]
fn systemUserId() -> String { "00000000-0000-0000-0000-000000000001" }

#[trusted_declassify(reason = "signed by ed25519 root key")]
fn parseSignedToken(token: String) -> String? { ... }
```

각 site 는 개발자 + 보안 reviewer 의 의도적 결정. CI 에서
`osty audit --trusted-declassify` 출력의 *증가* 는 review trigger.

### 21.15 Audit workflow

```sh
$ osty audit --trusted-declassify --report=pretty
src/auth/session.osty:42:10  fromSession      "validated by signed JWT (HS256)"
src/api/legacy.osty:15:5     fromLegacyClient "hard-coded UUID for system user"
src/ffi/syscall.osty:108:8   syscallReturn    "FFI from trusted system call (getpid)"
3 trusted-declassify sites
```

CI 통합 권장:

```yaml
# .github/workflows/security-audit.yml
- name: Audit declassify sites
  run: |
    osty audit --trusted-declassify --format=json > declassify.json
    # Compare to baseline; fail if new entries appear without review
    diff <(jq -r '.[] | .symbol' declassify.json | sort) \
         <(cat .ci/declassify-baseline.txt | sort) \
      || (echo "::error::new trusted-declassify site requires security review" && exit 1)
```

`osty audit --all` 은 `--trusted-declassify` + `--trusted-construct` +
`--match-compat` + `--legacy-globals` 를 동시 출력.

### 21.16 Implicit flow rationale

v0.6 은 *explicit-only* 정보 흐름 추적을 채택 — control-flow 의존
covert channel 은 추적하지 않는다. 사례:

```osty
fn leak(token: #[taint("secret")] String) -> Bool {
    if token.startsWith("admin") {
        true            // 결과는 untagged
    } else {
        false           // 결과는 untagged
    }
}
```

`Bool` 결과 의 값 은 token 정보를 *간접 전송* — 엄밀히 말하면 정보가
누출됐지만, v0.6 은 이것을 추적하지 않는다.

**근거 (Jif [Myers 2002] 의 결정과 동일)**:
1. **False positive 폭발** — 모든 conditional 의 결과가 condition 의
   tag 를 운반하면 거의 모든 값이 tainted 가 된다 — false alarm 율
   이 사용 가능 임계치 초과
2. **종합적 추적 비용** — implicit flow 추적은 type system 에 *security
   level lattice* 추가가 필요. 학술적 IFC 시스템에서도 도입
   비용으로 정착 안 된 이유
3. **Mainstream 채택 가능성** — explicit flow 만으로도 OWASP Top 10
   의 *직접 직접 데이터 흐름* (SQLi / XSS / 명령 주입) 차단 가능 —
   가장 흔한 pattern

**v0.7+ 옵션 (Open Item, SPEC_GAPS)**:
- `#[strict_flow]` — function-scoped opt-in implicit flow tracking
- 적용 함수 내에선 covert channel 도 거부 (false-positive 율 ↑)
- 보안 감사 코드 / cryptographic primitive 에서 사용

### 21.17 Forward compatibility

v0.6 의 information flow surface 는 *baseline* — 다음 추가가
미래 minor release 에 가능:

| 변경 | SemVer 영향 |
|---|---|
| 새 source tag (`#[taint("X")]` 등록) | additive |
| 새 trust tag (sanitizer 출력 tag) | additive |
| 새 sink (`#[requires]` 도입) | breaking — caller 측 sanitize 의무 추가 |
| 기존 sink 의 required tag 변경 | breaking |
| `#[trusted_declassify]` 추가 | additive |
| Implicit flow 추적 (`#[strict_flow]`) | additive (opt-in 이므로) |

stdlib 의 sink 카탈로그 에 추가는 breaking — `#[stability("stable")]`
public API 가 sink 를 추가하면 major version bump (`E2100`).
v0.6.0 의 baseline sink 5 종 (db / process / fs.path / http.redirect
/ template) 외 sink 추가는 v0.7 이후로 일정 표시.

### 21.18 Tag taxonomy reference

This section catalogues every flow tag defined in the v0.6 baseline
stdlib, the source / sanitizer / sink relationships, and where each
tag enters and exits the type system. Authors of new sinks or
sanitizers should pick from this taxonomy before introducing a new
tag string — an unrecognized tag is a compile warning (`W0902`).

#### 21.18.1 Source tags

| Tag | Sources (annotated `#[taint("σ")]`) | Origin |
|---|---|---|
| `user_input` | `http.Request.queryParam`, `http.Request.body`, `http.Request.path`, `http.Request.cookie`, form decoders | HTTP request surface |
| `env_input` | `Env.get`, `Env.require`, `Env.args` | Process environment |
| `fs_input` | `Fs.read`, `Fs.readToString`, `Fs.lines`, the `Reader` returned by `Fs.open` | Filesystem reads |
| `net_input` | `Net.connect.read`, `Net.recv`, `HttpClient.request.body` | Inbound network |
| `cli_input` | `Process.exec.stdout`, `Process.execShell.stdout` | Subprocess output |
| `db_input` | `Db.query.row.cell`, `Db.queryOne.field` | Database read result |

A value can carry *multiple* source tags — a `String` returned by
`http.respondHtml.body` after being concatenated from a query
parameter and a database row carries `{user_input, db_input}`.

#### 21.18.2 Trust tags (sanitizer output)

| Tag | Sanitizer (annotated `#[sanitizes(σ, into = τ)]`) | Required by |
|---|---|---|
| `sql_safe` | `std.sql.escape`, `std.sql.quoteIdent`, `std.sql.quotePath`, every `sql.*` builder, `Email.toString` (composition) | `Db.query`, `Db.exec` |
| `shell_safe` | `std.shell.quote` | `Process.execShell` |
| `path_safe` | `std.path.normalize`, `std.path.join`, `Path.parse` | `Fs.open`, `Fs.read`, `Fs.write`, `Fs.remove`, `scan.Options.outputDir`, `print.Document.path` |
| `url_safe` | `std.url.encode`, `std.url.parse`, `Url.builder().build()`, `std.security.checkUrl` | `http.redirect`, `http.movedPermanently`, `http.found`, `http.seeOther` |
| `html_safe` | `std.html.escape`, `std.markdown.htmlToMarkdown`, auto-escape templates | `http.respondHtml`, `template.render` |
| `json_safe` | `std.json.encode`, `std.json.stringify` | (planned for `http.respondJson` strict mode — Phase 5) |

A value may carry both source and trust tags simultaneously — the
relationship is set-intersection. A `db.exec(sql.eq("col",
sql.string(parsed.toString())))` call where `parsed: Email` produces
`#[taint({user_input, sql_safe})]` on the rendered SQL; the sink
checks `sql_safe ⊆ requires` and accepts.

#### 21.18.3 Sink registry

A *sink* is a stdlib function whose parameter carries
`#[requires("τ")]`. The v0.6 baseline registry:

| Sink | Required tag | Module |
|---|---|---|
| `Db.query(self, q)` | `sql_safe` on `q.sql` | §10.29 |
| `Db.exec(self, q)` | `sql_safe` on `q.sql` | §10.29 |
| `Process.execShell(self, cmdline)` | `shell_safe` | §10.15 |
| `Fs.open(self, path)` | `path_safe` | §10.15 |
| `Fs.read(self, path)` | `path_safe` | §10.15 |
| `Fs.write(self, path, _)` | `path_safe` | §10.15 |
| `Fs.remove(self, path)` | `path_safe` | §10.15 |
| `http.redirect(self, target)` | `url_safe` | §10.24 |
| `http.respondHtml(self, body)` | `html_safe` | §10.24 |
| `template.render(t, body)` | `html_safe` | (template stdlib) |

Adding a sink to a `#[stability("stable")]` API is a major-version
breaking change (§3.14.3) — callers gain a new sanitization
obligation, which is by definition a breaking surface change.

### 21.19 Worked sanitizer composition

Real applications often compose multiple sanitizers in a single data
path. This section catalogues the common chains.

#### 21.19.1 Form input → SQL

```osty
fn searchUsers(req: HttpRequest, db: Db) -> Result<List<User>, Error> {
    // Source: query string → user_input
    let raw: #[taint("user_input")] String = req.queryParam("q") ?? ""

    // Sanitizer: parse into Email if relevant; otherwise sql.string
    // safely binds raw text as a parameter.
    let q = sql.selectWhere("users", ["id", "email"],
        sql.like("email", sql.string(raw))?)?

    // Sink: db.query requires sql_safe — the sql.string + parameter
    // path produces sql_safe automatically.
    let rs = db.query(q)?
    Ok(rs.rows.map(|r| User.fromRow(r)))
}
```

Note `sql.string` does *not* concatenate — it produces a parameter
binding. The `sql_safe` tag on the resulting `Query.sql` reflects
the safe-by-construction property of parameterization.

#### 21.19.2 URL input → HTML output

```osty
fn renderProfile(req: HttpRequest, net: Net) -> Result<HttpResponse, Error> {
    // Source: query parameter → user_input
    let raw: #[taint("user_input")] String = req.queryParam("homepage") ?? ""

    // First sanitizer: url.parse → url_safe
    let homepage = url.parse(raw)?

    // Second sanitizer: html.escape on the canonical URL string → html_safe
    let safeText = std.html.escape(homepage.toString())

    // Sink: respondHtml requires html_safe
    Ok(http.respondHtml("<a href=\"{safeText}\">homepage</a>"))
}
```

Two sanitizers compose because each registered with a different
output tag. The final value carries `#[trust({url_safe, html_safe})]`
which is acceptable to both sink shapes.

#### 21.19.3 Filesystem path input → process exec

```osty
fn runScanner(env: Env, proc: Process) -> Result<Output, Error> {
    // Source: env variable → env_input
    let raw: #[taint("env_input")] String = env.require("TOOL_PATH")?

    // Sanitizer: path.normalize → path_safe (rejects ".." traversal,
    // null bytes, etc.)
    let safePath = std.path.normalize(raw)?

    // Sink: process.exec receives the path as args[0]; argv-style
    // exec does NOT require shell_safe (the kernel does not interpret
    // args as shell metacharacters). path_safe is acceptable.
    proc.exec(safePath, ["--version"])
}
```

`Process.exec(cmd, args)` (argv-style) does not require
`shell_safe` because the OS does not interpret args as a shell
command — the binary is invoked directly with the literal argument
list. `Process.execShell(cmdline)` *does* invoke a shell and
therefore requires `shell_safe`.

#### 21.19.4 Multi-source aggregation

When a value is built from multiple sources, the tag set unions:

```osty
fn buildLog(req: HttpRequest, env: Env) -> String {
    let user: #[taint({user_input})] String = req.queryParam("u") ?? ""
    let host: #[taint({env_input})] String = env.get("HOST") ?? ""
    // Concatenation unions the tag sets:
    //   #[taint({user_input, env_input})] String
    "{user} from {host}"
}
```

A sink that requires *either* `user_input` *or* `env_input` to be
sanitized must clear both. Conservative defaults: sanitize at the
narrowest boundary (per source) before composing.
