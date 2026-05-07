# BREAKING_v0.6 — Public catalog of v0.5 → v0.6 breaking changes

> **Status**: 권위적 카탈로그. v0.5 코드가 v0.6 컴파일러에서 *어떻게 깨지는지*
> 의 단일 진실 소스. 소프트 (compat 모드 있음) / 하드 (즉시 break) 분류.
>
> **Migration how-to**: [`MIGRATING_v0.5_to_v0.6.md`](./MIGRATING_v0.5_to_v0.6.md).
>
> **Spec authority**: [`LANG_SPEC_v0.6/`](./LANG_SPEC_v0.6/), [`SPEC_GAPS.md`](./SPEC_GAPS.md) §"Resolved in v0.6", [`LANG_SPEC_v0.6/18-change-history.md`](./LANG_SPEC_v0.6/18-change-history.md) §18.0.

## Severity Legend

| Marker | Meaning |
|---|---|
| 🔴 **Hard** | v0.5 코드 즉시 컴파일 에러. 호환 모드 없음. v0.6.0 부터 수정 필수. |
| 🟡 **Soft (v0.6.x compat)** | `--legacy-*` flag 또는 manifest 옵션으로 v0.6.x 동안 계속 작동. v0.7 에서 hard break 예정. |
| 🟠 **Phase-deferred** | v0.6.0 에선 영향 없음. Implementation phase 도달 시 (Phase 4/5) hard break 발효. |
| 🔵 **Surface-only** | 식별자 / annotation 이름 충돌. 단순 rename 으로 해소. |

---

## 1. 🔴 Reserved keyword: `while` (G49)

식별자로 사용된 `while` 은 v0.6.0 부터 즉시 컴파일 에러.

**Affected**:
```osty
let while = 1                  // ERROR: reserved keyword
fn while() { ... }             // ERROR
struct While {                  // OK — 'While' (대문자) 는 영향 없음
    while: Int,                 // ERROR: field name
}
```

**Fix**: rename 한 곳만. `while_` / `whileVar` / `_while` 등.

**Compat**: 없음. 사용자 0 단계의 acceptable break.

**Spec**: §1.2, §4.4, OSTY_GRAMMAR_v0.6 R27.

---

## 2. 🔴 Reserved contextual keywords: `spec` / `example` / `law` / `invariant` (G43)

이 4 식별자는 *함수 본문 첫 statement 위치* 또는 *spec block 안*에서만 keyword. 다른 위치는 식별자 그대로. 하지만 다음 케이스가 충돌:

```osty
fn foo() {
    spec { ... }         // v0.6 → spec block 으로 파싱 (parse error 가능)
}

fn bar() {
    let spec = 1         // OK — 첫 statement 가 'let' 이므로 식별자
    spec.method()        // OK — 식별자
}
```

**Fix**: 함수 본문 첫 위치에 `spec` 식별자 사용 (드물지만 가능) — `let` / `;` 등으로 위치 옮기기. 첫 위치가 진짜 spec block 일 경우 v0.6 syntax 인지 확인.

**Compat**: 없음. 통계상 충돌 케이스 거의 없음.

**Spec**: §1.3, §3.13, OSTY_GRAMMAR_v0.6 R28.

---

## 3. 🟡 Stdlib effect globals → capability methods (G36)

v0.5 의 *전역 effect 함수* 호출은 v0.6.x 에선 deprecated (W0750), v0.7 에서 hard break.

**Affected (전수)**:
```osty
time.now()              time.monotonic()           time.sleep(d)
random.next()           random.nextBytes(n)         random.default()
env.get(k)              env.set(k, v)               env.args()           env.vars()
fs.readToString(p)      fs.write(p, c)              fs.exists(p)         fs.create(p)         fs.remove(p)         fs.mkdir(p)
os.exec(c, a)           os.exit(code)               os.hostname()        os.pid()
net.dial(h, p)          net.listen(p)
```

**Fix v0.6 권장**: capability 파라미터 추가.
```osty
// before
pub fn loadConfig() -> Result<Config, Error> {
    fs.readToString("/etc/app.toml")
}

// after
pub fn loadConfig(fs: Fs) -> Result<Config, Error> {
    fs.readToString("/etc/app.toml")
}
```

**Compat (v0.6.x 만)**: `osty build --legacy-globals` 또는 `osty.toml` `[legacy] globals = true`. 위 호출이 자동 desugar 됨 (`time.now()` → `std.time.host.now()`). 사용 시 manifest stability 가 자동 `experimental` 로 강제 — `stable` API 약속 불가.

**v0.7 에서**: `--legacy-globals` flag 자체가 unknown flag 로 fail.

**Spec**: §20.1–20.3, [`MIGRATING_v0.5_to_v0.6.md`](./MIGRATING_v0.5_to_v0.6.md) §3.

---

## 4. 🟡 Stdlib sealed types — 외부 struct literal 차단 (G40)

다음 stdlib types 가 v0.6 에서 `#[sealed_construct]` 화. 외부 코드의 직접 struct literal 은 컴파일 에러.

**Affected types**: `std.string.Email`, `std.url.Url`, `std.fs.Path`, `std.sql.SqlIdent`, `std.time.Duration`, `std.uuid.Uuid`.

```osty
// ❌ E0420
let email = Email { local: "alice", domain: "example.com" }
let path  = Path  { components: ["etc", "passwd"], absolute: true }

// ✅ Must route through parse
let email = Email.parse("alice@example.com")?
let path  = Path.parse("/etc/passwd")?
```

**Spread update / 직접 mutation 도 차단**:
```osty
let email2 = Email { ..email, domain: "other.com" }   // E0420
email.domain = "other.com"                             // E0420
```

**Compat (v0.6.x 만)**: `osty build --legacy-construct`. v0.7 제거.

**Spec**: §3.4.5, [`MIGRATING_v0.5_to_v0.6.md`](./MIGRATING_v0.5_to_v0.6.md) §4.

---

## 5. 🟠 Information flow sink rollout (Phase 5, G37)

**v0.6.0 에선 영향 없음.** Implementation Phase 5 (예정 v0.6.4 또는 v0.7 alpha) 도달 시 다음 stdlib sink 들이 `#[requires("...")]` 어노테이션을 받아 *unsanitized 사용자 입력 도달 시 컴파일 에러*.

**Affected sinks (Phase 5 baseline)**:
| Sink | Required tag | Sanitizer 권장 |
|---|---|---|
| `db.query`, `db.exec` (raw SQL) | `sql_safe` | `std.sql.escape`, parameterized form |
| `process.exec`, `process.spawn` | `shell_safe` | `std.shell.quote` |
| `fs.read*`, `fs.write*`, `fs.open` (path arg) | `path_safe` | `std.path.normalize` |
| `http.redirect`, `http.fetch` | `url_safe` | `std.url.encode` |
| `template.render`, `http.respondHtml` | `html_safe` | `std.html.escape` |

**예상 fail 패턴**:
```osty
// Phase 5 부터 컴파일 에러
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    db.query("SELECT * FROM users WHERE id = {id}")    // E0900
}
```

**Fix 옵션**:
1. **Parameterized form** (권장): `db.exec("... WHERE id = ?", [id])`
2. **Sanitize**: `let safe = std.sql.escape(id); db.query("... = {safe}")`
3. **Audit-marked escape**: `#[trusted_declassify(reason = "validated upstream")]` — `osty audit --trusted-declassify` 로 enumerate

**Compat**: 없음. 의도된 *보안 회귀 노출* — 기존 코드의 SQLi/XSS/명령 주입을 컴파일 타임에 발견.

**Spec**: §21.8, [`MIGRATING_v0.5_to_v0.6.md`](./MIGRATING_v0.5_to_v0.6.md) §5.

---

## 6. 🔵 Annotation namespace conflicts

신규 v0.6 annotation 20 개. 기존 사용자 코드가 같은 이름 식별자 / 사용자 정의 annotation (불허지만 형식상) 사용 시 충돌.

**v0.6 신규 annotation 이름**:
```
#[ambient]            #[reproducible_capability]
#[taint]              #[sanitizes]              #[requires]              #[trusted_declassify]              #[taint_field]
#[spec]               #[purpose]                #[example]               #[fixture]
#[sealed_construct]   #[trusted_construct]      #[test_construct]
#[error_contract]
#[reproducible]
#[since]              #[stability]              #[match_compat]
#[golden]
#[budget]
```

**Affected**: v0.5 까지는 fixed annotation set 이라 사용자 정의 annotation 자체가 불허였으므로 *직접 충돌 거의 없음*. 단, 식별자 이름이 위와 같은 경우 (예: `let example = ...` → annotation 자리에 충돌) 는 별개 문제.

**Fix**: 거의 없음. 사용자 정의 annotation 시도가 v0.5 에서 이미 `E0400` (CodeUnknownAnnotation) 였으므로.

**Spec**: §1.9, §3.8, §14.

---

## 7. 🟠 `#[reproducible]` checker enforcement (Phase 3, G39)

**v0.6.0 에선 영향 없음.** Phase 3 (예정 v0.6.2) 부터 `#[reproducible(scope=...)]` 함수가:
- non-deterministic capability 수신 시 `E0784`
- unordered iter (`Map.iter`, `Set.iter`) 사용 시 `E0786`
- transitive callee 도 같거나 강한 scope 미만 시 `E0787`

**Affected**: 기존 `#[pure]` 만 사용하는 코드는 영향 없음 (`#[pure]` 는 v0.5 부터 동일 검사). `#[reproducible]` 은 신규.

**Fix**: capability 파라미터 추가 또는 deterministic capability 사용. `Map.entriesSorted()` / `Set.toListSorted()` 로 unordered 회피.

**Spec**: §3.11.

---

## 8. 🟠 Match exhaustiveness with `#[error_contract]` (Phase 4, G41)

**v0.6.0 에선 영향 없음.** Phase 4 부터 `#[error_contract]` 함수의 결과를 match 할 때 *contract variant 만* exhaustive 판정. contract 외 variant arm 은 `W0413` (dead per contract).

**Affected**: stdlib 함수가 v0.6 에서 contract 어노테이션을 받으면 호출자 측 match 가 영향. v0.6.0 시점엔 stdlib annotation rollout 미완 — 점진 영향.

**Fix**: contract 외 arm 제거 또는 `_ -> ...` 로 catch-all.

**Spec**: §7.5.7.

---

## 9. 🔵 `osty publish` SemVer enforcement (Phase 4, G44)

**v0.6.0 에선 영향 없음.** Phase 4 부터 `osty publish` 가 manifest API surface diff 검증.

**Affected**: registry 에 publish 하는 라이브러리. `stable` 시그니처 변경 + minor/patch bump 시 publish 거부 (`E2100`).

**Fix**: 적절한 major version bump 또는 `experimental` stability 로 표기. `osty publish --allow-major-bump` 명시 옵션.

**Spec**: §3.14.3, §13.5.

---

## 10. 🟠 `--legacy-globals` 의 stability 강제 (G36 + G44)

`--legacy-globals` 활성화된 패키지는 자동으로 `[stability] default = "experimental"` 강제. *legacy 의존 코드는 stable API 약속 불가*.

**Affected**: legacy globals 사용 중인 라이브러리가 v0.6.x 에서 stable API 약속하려 할 때.

**Fix**: capability 파라미터로 마이그레이션 후 legacy 모드 해제.

**Spec**: §20.3.1, §3.14.

---

## 호환 전략

### v0.6.0 (initial release)

- 1, 2 (`while` / spec keywords) 만 hard break
- 3, 4 는 deprecation warning (W0750), `--legacy-*` flag 작동
- 5 (taint), 7 (reproducible), 8, 9, 10 은 phase 진행 후 발효

### v0.6.x (마이그레이션 기간)

- legacy modes 작동
- 점진적으로 phase 도달 (Phase 1 → 5)
- 사용자 코드는 capability / sealed / taint 적용 가능 (선택적)

### v0.7.0 (cleanup)

- `--legacy-globals` / `--legacy-construct` 제거 → 3, 4 hard break
- Phase 5 완료된 sink 들 모두 enforcement 활성

## 마이그레이션 순서 권장

1. `osty check --legacy-globals --warnings-as-errors` 로 deprecated 호출 enumerate
2. capability parameter 추가 (§3, MIGRATING.md)
3. stdlib sealed types literal → `Type.parse(...)` 전환 (§4, MIGRATING.md)
4. (Phase 5 도달 시) sink 호출 audit 후 sanitizer / parameterized form 으로
5. legacy flags 제거

각 단계 별 detail 은 [`MIGRATING_v0.5_to_v0.6.md`](./MIGRATING_v0.5_to_v0.6.md).
