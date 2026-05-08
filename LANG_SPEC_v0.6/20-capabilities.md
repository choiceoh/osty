## 20. Capabilities

> v0.6 G36. *Hidden dependency is forbidden* design north star
> 의 *effectful* 축. 환경 effect 를 capability 값으로 명시한다.

### 20.1 동기

v0.5 까지 stdlib 의 환경 접근은 **전역 함수**로 노출되어 있다:

```osty
let t = time.now()
let n = random.next()
let home = env.get("HOME")
let buf = fs.readToString("/etc/passwd")?
```

이 형태는 다음 분석을 *deny-list 기반*으로 만든다:

- `#[pure]` — 본문 walk 후 `time.*`, `random.*`, `env.*`, `fs.*`, … 호출 검출
- `#[reproducible]` — 동일 + unordered iter / pointer-id 등 추가
- `#[taint]` (G37) — 마찬가지

Deny-list 는 *exhaustive 보장 불가능*. 새 effectful API 가 stdlib 에 추가될 때마다
검사기 갱신 필요하고, 사용자 정의 effect 는 검출 불가.

### 20.2 Capability 타입

v0.6 은 stdlib 의 환경 접근을 **capability 값**으로 receive 하도록 전환한다.

```osty
// std.time
pub interface Clock {
    fn now(self) -> Time
    fn monotonic(self) -> Duration
}

// std.random
pub interface Rng {
    fn next(self) -> Int
    fn nextBytes(self, n: Int) -> Bytes
}

// std.env
pub interface Env {
    fn get(self, key: String) -> String?
    fn set(self, key: String, value: String)
    fn args(self) -> List<String>
    fn vars(self) -> Map<String, String>
}

// std.fs
pub interface Fs {
    fn readToString(self, path: String) -> Result<String, FsError>
    fn write(self, path: String, content: String) -> Result<(), FsError>
    // ...
}

pub interface Net {
    fn dial(self, host: String, port: Int) -> Result<Conn, NetError>
}

pub interface Process {
    fn exec(self, cmd: String, args: List<String>) -> Result<Output, ProcError>
    fn pid(self) -> Int
}
```

이 7 개 interface (`Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process`, `Console`) 가 v0.6
의 **canonical capability set**. 사용자 정의 capability 는 §20.5 참조.

**Implementation note.** 현재 stdlib 는 이 canonical protocol set 을
`std.capability` 에 compile-checked interface surface 로 노출한다. 기존
`std.time`, `std.random`, `std.env`, `std.fs`, `std.net`, `std.process`, `std.io`
전역 함수는 v0.6.x compatibility surface 로 유지되며, ambient desugar /
`--legacy-globals` warning 은 별도 compiler phase 에서 닫는다.

함수는 capability 를 **명시적 파라미터**로 받는다:

```osty
fn buildId(clock: Clock, rng: Rng) -> String {
    "{clock.now().toEpochMillis()}-{rng.next()}"
}
```

### 20.3 `#[ambient]` — Script ergonomics

라이브러리 코드에 capability 명시는 적절하지만, 스크립트와 `main` 진입점에서 매번
`Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process` 6 개를 받기는 verbose 하다. v0.6 은
ambient capability 를 도입한다.

```osty
#[ambient(clock, rng, env, fs)]
fn main() {
    let id = buildId(clock, rng)         // capability 자동 주입
    let home = env.get("HOME") ?? "/"
    fs.write("{home}/log.txt", "id={id}")?
}
```

### 20.3.1 정밀 의미

`#[ambient(name1, name2, ...)]` 의 desugar 규칙:

1. **Bind**: 본문 첫 statement 위치에 `let name<i> = std.<capability>.default()` 가
   삽입된다. `name<i>` 와 capability 의 매핑은 prelude 의 *고정 표* (§20.6) 에서
   도출 — `clock` → `std.time.systemClock`, `rng` → `std.random.default()`, `env`
   → `std.env.host`, `fs` → `std.fs.host`, `net` → `std.net.host`, `process` →
   `std.process.host`, `console` → `std.io.stdout`.
2. **Forward**: 본문 안에서 함수 호출 시 호출 대상 함수의 capability parameter 와
   **이름이 일치**하는 ambient binding 이 있으면 자동으로 인자 자리에 채워진다.
   이름 불일치 시 (예: callee 가 `fn f(c: Clock)` 인데 ambient 는 `myClock`) 자동
   forward 안 함 — 명시 호출 필요.
3. **Shadow**: 사용자가 같은 이름으로 `let` 선언하면 ambient binding 이 가려진다 —
   shadowing 은 일반 scope rule 따름.

**Conflict resolution**: 같은 capability 를 ambient binding 과 명시 인자로
*동시에 전달*하는 경우 — 명시 인자가 우선. ambient forward 는 *명시 인자가 빠진
자리만* 채운다.

```osty
#[ambient(clock, rng)]
fn main() {
    // (a) 자동 forward — buildId(clock: Clock, rng: Rng)
    let id1 = buildId()                  // OK — 둘 다 ambient 에서 forward

    // (b) 부분 명시
    let id2 = buildId(rng = customRng)   // clock 만 ambient, rng 는 명시

    // (c) 전부 명시
    let id3 = buildId(clock, customRng)  // OK

    // (d) ambient 와 같은 이름으로 명시 — 명시 우선
    let id4 = buildId(clock = systemClock, rng)  // OK
}
```

**Ambient 가 줄 수 있는 capability 는 prelude default 만 — 사용자 정의 capability 는
ambient 불가 (`E0789`).**

**`#[ambient]` 사용 가능 위치**:
- Script (`#!/usr/bin/env osty` 파일) — **자동 ambient = `(clock, rng, env, fs)`**
- `fn main` (top-level main 이 entry 일 때)
- `#[test]` / `#[bench]` / `bench*` / `test_*` 함수 — 테스트 환경의 ambient
- 그 외 함수: **금지** (`E0780`)

라이브러리 코드는 항상 명시. ambient 는 **boundary 위에서만 허용**. 이는
*테스트 가능성*을 강제 — 라이브러리 함수는 mockable Clock / Rng 를 받아 테스트
시 fake 주입 가능.

### 20.3.2 Ambient binding scope 규칙

`#[ambient(name1, ...)]` 의 binding 은 다음 scope 규칙을 따른다:

1. **Visibility 시작점**: 함수 본문의 첫 statement 위치에 desugar 된 `let
   name<i>: Capability<i> = std.<...>.default()` 가 삽입된 *이후 모든
   statement* 에서 가시.

2. **Block 통과**: 일반 `let` 처럼 — `if` / `match` / `for` / `while` / `loop` /
   block expression 내부에서도 가시.
   ```osty
   #[ambient(clock)]
   fn main() {
       if shouldLog() {
           let t = clock.now()        // OK — ambient 가 nested block 안에 가시
       }
   }
   ```

3. **Closure capture (deterministic)**: closure 가 ambient binding 을 capture
   하면 *값 capture* (capability 값을 closure 가 보유). closure 가 다른 함수로
   넘어가도 ambient 가 함께 흐름.
   ```osty
   #[ambient(clock)]
   fn main() {
       let measure = || clock.monotonic()    // measure: () -> Duration, captures clock
       runRepeatedly(measure)                 // measure 가 다른 함수에서 호출돼도 OK
   }
   ```
   다만 *ambient 자체는 함수 boundary 를 넘지 않음* — `runRepeatedly` 의 본문이
   ambient `clock` 을 보지 못함. `measure` closure 가 capability 을 들고 들어감.

4. **Shadowing**: 같은 이름 `let` 선언이 ambient 가린다 — 일반 scoping 규칙.
   ```osty
   #[ambient(clock)]
   fn main() {
       let clock = std.time.fakeClock(epoch_ms = 0)    // ambient 를 fake 로 shadow
       buildId(clock)                                    // shadow 된 값 forward
   }
   ```

5. **Auto-forward 의 정확한 매칭**: callee 의 capability parameter 이름과 ambient
   binding 이름이 *exact 일치* 시에만 자동 forward. 케이스 차이 / underscore 차이
   불일치 — 명시 인자 필요.
   ```osty
   fn buildId(systemClock: Clock, rng: Rng) -> String { ... }

   #[ambient(clock, rng)]
   fn main() {
       buildId()                  // ERROR: ambient `clock` 이 callee `systemClock` 과
                                   //        이름 불일치 — auto-forward 안 함
       buildId(clock, rng)         // OK — 명시
   }
   ```

6. **Ambient 누수 금지**: ambient binding 은 *함수 boundary* 에서 멈춘다. 즉
   `#[ambient(clock)]` 함수가 다른 함수 `g()` 를 호출할 때, `g` 가 capability
   parameter 를 받지 않으면 capability 가 자동 주입되지 않음. *명시 routing*만
   허용.

7. **Recursion**: `#[ambient]` 가 붙은 함수가 자기 자신을 호출 시 ambient 는
   함수 boundary 마다 재-desugar — 즉 매 호출 instance 가 같은 default instance
   를 받는다 (deterministic).

### 20.3.3 Ambient 와 capability 위 effect annotation

```osty
#[ambient(clock, rng)]
#[reproducible]                  // ERROR: E0784 — ambient 가 non-det capability 주입
fn main() { ... }
```

`#[reproducible]` 와 ambient (Clock / Rng / Env / Fs / Net / Process 중 하나
포함) 동시 적용 = 컴파일 에러. ambient 는 *boundary 의 ergonomics 도구*이므로,
*그 함수가 reproducible 하다* 와는 양립 불가.

`#[ambient(console)]` 단독은 — `Console` 이 deterministic 출력 capability 이므로
`#[reproducible(scope = "run")]` 와 양립 (그 외 scope 는 `Console` 도 거부).

### 20.4 Capability 와 effect annotation 의 상호작용

Capability parameter 의 *진짜 가치*는 G39 / G37 / G46 의 sound 한 검사:

```osty
// G39 — Reproducibility
#[reproducible(scope = "target")]
fn computeKey(data: Bytes) -> Bytes32 {
    sha256(data)             // OK — capability 미수신
}

#[reproducible]
fn buildId(clock: Clock, rng: Rng) -> String {  // E0784
    // 컴파일러: clock / rng 받으면 reproducible 불가
}
```

검사 규칙:
- `#[reproducible]` 함수는 `Clock`, `Rng`, `Env`, `Net`, `Process`, `Fs` capability
  파라미터 를 받을 수 없다 (`E0784`).
- 호출하는 함수 도 같은 제약을 만족해야 한다 (transitive).
- `Console`, `Hash`, `Os` 같은 *side-effect-free 또는 deterministic* capability 는
  허용 — capability set 마다 *deterministic 등급* 을 §20.6 표에서 정의.

`#[pure]` 도 동일한 모델로 단순화:
- `#[pure]` 함수는 *어떤* capability 도 받을 수 없다.

### 20.5 사용자 정의 capability

```osty
pub interface MyDb {
    fn query(self, sql: SqlIdent) -> Result<Rows, DbError>
}

#[reproducible_capability]
pub interface Hash {
    fn hash(self, data: Bytes) -> Bytes32
}
```

`#[reproducible_capability]` 어노테이션은 해당 capability 가
"deterministic 함수만 노출함"을 컴파일러에 약속. `#[reproducible]` 검사는 이런
capability 수신을 허용한다.

**약속 검증**: `#[reproducible_capability]` interface 는 `#[reproducible]` 함수만
포함할 수 있다 (`E0785`). 즉 capability 자체가 sealed.

### 20.6 Capability deterministic 등급

| Capability | 등급 | `#[reproducible]` 가능 |
|---|---|---|
| `Clock` | non-deterministic | ❌ |
| `Rng` | non-deterministic | ❌ |
| `Env` | non-deterministic | ❌ |
| `Fs` | non-deterministic | ❌ |
| `Net` | non-deterministic | ❌ |
| `Process` | non-deterministic | ❌ |
| `Console` | side-effect (deterministic 출력) | scope = `"run"` 만 가능 |
| `Hash` | deterministic (사용자 정의) | ✅ |

### 20.7 Capability 와 G15 arity erasure

함수값 으로 저장 시 capability 파라미터는 **그대로 시그니처에 유지**된다 (G15 의
default/keyword erasure 와 다름):

```osty
let f: fn(Clock, Rng) -> String = buildId
f(systemClock, defaultRng)         // OK
```

이는 capability 가 *positional 필수 파라미터*이기 때문이며 G15 와 충돌하지 않는다.

### 20.8 진단 코드

| 코드 | 의미 |
|---|---|
| `E0780` | `#[ambient]` 가 허용되지 않는 위치에 사용 |
| `E0781` | `#[ambient]` 인자가 알려지지 않은 capability |
| `E0782` | Capability 자동 forward 실패 (이름 일치 안 함) |
| `E0783` | `#[reproducible_capability]` interface 에 비-reproducible 메서드 |
| `E0784` | `#[reproducible]` 함수가 non-deterministic capability 수신 |
| `E0785` | `#[pure]` 함수가 capability 수신 |

---
