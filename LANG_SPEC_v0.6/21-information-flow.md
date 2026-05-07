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

