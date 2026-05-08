## 20. Capabilities

> v0.6 G36. *Hidden dependency is forbidden* design north star
> 의 *effectful* 축. 환경 effect 를 capability 값으로 명시한다.

### 20.1 동기

v0.5 baseline 까지 stdlib 의 환경 접근은 **전역 함수**로 노출되어 있었다 —
v0.6 에서 이 surface 는 capability 로 전환된다 (`--legacy-globals` 호환
모드는 v0.6.x 한정, v0.7 제거):

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
전역 함수는 v0.6.x compatibility surface 로 유지된다. Host-boundary adapter
factory 는 현재 `time.systemClock()`, `random.host()`, `env.host()`, `fs.host()`,
`capability.hostNet()`, `capability.hostProcess()`, `io.console()` 로 노출하며,
`std.net.host()` / `std.process.host()` 는 cross-module bridge helper 로 제공한다.
테스트용 deterministic fake set 은 `std.capability.testing` 의 `FakeClock`,
`FakeRng`, `FakeEnv`, `FakeFs`, `FakeNet`, `FakeProcess`, `FakeConsole` 로 제공한다.
Ambient desugar / `--legacy-globals` warning 은 별도 compiler phase 에서 닫는다.
현재 구현은 `#[ambient]` 의 entry-point 위치 제한, canonical name 검증, user-defined
capability ambient 금지를 active checker gate 로 고정한다 (`E0780` / `E0781` / `E0789`). `#[reproducible]` /
`#[pure]` 의 direct capability parameter 제한도 signature gate 로 고정한다
(`E0784` / `E0785`). 실제 ambient auto-forward/desugar 와 transitive
reproducibility analysis 는 후속 단계이다.

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
ambient 불가 (`E0781`).**

**`#[ambient]` 사용 가능 위치**:
- Script (`#!/usr/bin/env osty` 파일) — **자동 ambient = `(clock, rng, env, fs)`**
- `fn main` (top-level main 이 entry 일 때)
- `#[test]` / `#[bench]` / `bench*` / `test*` 함수 — 테스트 환경의 ambient
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
  파라미터 를 받을 수 없다 (`E0784`). 현재 구현은 direct signature parameter 를
  active checker gate 로 검증한다.
- 호출하는 함수 도 같은 제약을 만족해야 한다 (transitive).
- `Console`, `Hash`, `Os` 같은 *side-effect-free 또는 deterministic* capability 는
  허용 — capability set 마다 *deterministic 등급* 을 §20.6 표에서 정의.

`#[pure]` 도 동일한 모델로 단순화:
- `#[pure]` 함수는 *어떤* capability 도 받을 수 없다 (`E0785`). 현재 구현은
  canonical capability 와 `#[reproducible_capability]` 로 선언된 local
  deterministic capability 를 direct signature 에서 검증한다.

### 20.5 사용자 정의 capability

```osty
pub interface MyDb {
    fn query(self, sql: SqlIdent) -> Result<Rows, DbError>
}

#[reproducible_capability]
pub interface Hash {
    #[reproducible]
    fn hash(self, data: Bytes) -> Bytes32
}
```

`#[reproducible_capability]` 어노테이션은 해당 capability 가
"deterministic 함수만 노출함"을 컴파일러에 약속. `#[reproducible]` 검사는 이런
capability 수신을 허용한다.

**약속 검증**: 현재 구현은 `#[reproducible_capability]` interface 가
`#[reproducible]` 함수만 포함하도록 active checker gate 로 검증한다 (`E0783`).
즉 capability 자체가 sealed.

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

### 20.9 Canonical capability interface specifications

이하 §20.9.1–§20.9.7 은 v0.6 baseline 의 7 canonical capability —
`Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process`, `Console` — 의 정식
interface 정의이다. 각 capability 는 `std.capability` 모듈에 선언되며,
host-boundary adapter 는 §20.10 이 기록한다.

#### 20.9.1 `Clock` — wall + monotonic time

```osty
pub interface Clock {
    /// Wall clock in UTC. Monotonically non-decreasing within one
    /// process; subject to platform clock adjustments across reboot.
    fn now(self) -> Time

    /// Monotonic clock since process start. Strictly non-decreasing,
    /// immune to wall-clock adjustments. Suitable for measuring
    /// elapsed time.
    fn monotonic(self) -> Duration

    /// Cancellation-aware sleep. Returns `Err(Cancelled)` if the
    /// enclosing taskGroup is cancelled before the duration elapses.
    fn sleep(self, d: Duration) -> Result<(), Error>
}
```

**Host adapter**: `time.systemClock` — `Clock` instance backed by the
platform's wall clock + `CLOCK_MONOTONIC`.

**Fake adapter**: `std.capability.testing.FakeClock(epoch_ms = N)` —
returns the configured epoch on every `now()`; `monotonic()` advances
by 1ms per call; `sleep()` is instantaneous and never blocks.

**Determinism grade**: non-deterministic. `#[reproducible]` functions
cannot receive `Clock` (`E0784`); `#[pure]` rejects all capabilities
including `Clock` (`E0785`).

#### 20.9.2 `Rng` — random number generation

```osty
pub interface Rng {
    /// Pseudo-random Int. Distribution over the full Int range.
    fn next(self) -> Int

    /// Pseudo-random byte sequence of length n.
    fn nextBytes(self, n: Int) -> Bytes

    /// Pseudo-random Int in [lo, hi]. Returns `lo` when lo == hi;
    /// aborts when lo > hi.
    fn nextRange(self, lo: Int, hi: Int) -> Int

    /// Pseudo-random Float64 in [0.0, 1.0).
    fn nextFloat(self) -> Float64
}
```

**Host adapter**: `random.host` — cryptographically secure when
available (Linux: `getrandom`, macOS: `arc4random_buf`, Windows:
`BCryptGenRandom`); falls back to a seeded PRNG only on platforms
without secure entropy sources.

**Fake adapter**: `std.capability.testing.FakeRng(seed = N)` —
deterministic xorshift64 sequence from `seed`. Returning the same
sequence given the same seed is part of the contract.

**Determinism grade**: non-deterministic for `random.host`;
deterministic for `FakeRng`. The Rng *type* is non-deterministic
because the production adapter is.

#### 20.9.3 `Env` — environment + arguments

```osty
pub interface Env {
    /// Returns the value of environment variable `key`, or None if
    /// not set. Empty string is a present-but-empty value.
    fn get(self, key: String) -> String?

    /// Sets `key` to `value` in the current process environment.
    /// Effect on already-spawned children is platform-defined.
    fn set(self, key: String, value: String)

    /// Removes `key` from the current process environment.
    fn unset(self, key: String)

    /// Snapshot of all environment variables at call time.
    fn vars(self) -> Map<String, String>

    /// Process command-line arguments, including the program name at
    /// index 0.
    fn args(self) -> List<String>
}
```

**Host adapter**: `env.host` — wraps `std.os.getenv` / `setenv` /
`environ` / process arguments.

**Fake adapter**: `std.capability.testing.FakeEnv(vars = {...},
args = [...])` — in-memory variable map and argument list, isolated
per test.

**Determinism grade**: non-deterministic. Environment varies across
runs and platforms; `#[reproducible]` excludes.

#### 20.9.4 `Fs` — filesystem read + write

```osty
pub interface Fs {
    fn readToString(self, path: String) -> Result<String, FsError>
    fn readToBytes(self, path: String) -> Result<Bytes, FsError>
    fn write(self, path: String, content: String) -> Result<(), FsError>
    fn writeBytes(self, path: String, bytes: Bytes) -> Result<(), FsError>
    fn exists(self, path: String) -> Bool
    fn isFile(self, path: String) -> Bool
    fn isDir(self, path: String) -> Bool
    fn create(self, path: String) -> Result<(), FsError>
    fn remove(self, path: String) -> Result<(), FsError>
    fn mkdir(self, path: String) -> Result<(), FsError>
    fn mkdirAll(self, path: String) -> Result<(), FsError>
    fn list(self, path: String) -> Result<List<String>, FsError>

    /// Open a file for streaming read. Returns a Reader that must be
    /// closed via `defer` or a closure-scoped helper (`fs.withReader`).
    fn open(self, path: String) -> Result<Reader, FsError>

    /// Create or truncate for streaming write.
    fn createWriter(self, path: String) -> Result<Writer, FsError>
}
```

**Host adapter**: `fs.host` — wraps `os.Open` / `os.Create` / etc.

**Fake adapter**: `std.capability.testing.FakeFs(layout = {...})` —
in-memory tree. The `layout` argument is a `Map<String, FakeEntry>`
where `FakeEntry` covers files and directories.

**Determinism grade**: non-deterministic for `fs.host`; deterministic
for `FakeFs` given a fixed `layout`.

**Path safety**: in v0.6 Phase 5, path-accepting methods (`readTo*`,
`write*`, `open`, `mkdir`, etc.) carry `#[requires("path_safe")]` on
the path parameter. User input must pass through `std.path.normalize`
or `std.path.join` before reaching these sinks. See §21.8.

#### 20.9.5 `Net` — TCP / TLS / HTTP

```osty
pub interface Net {
    fn dial(self, host: String, port: Int) -> Result<Conn, NetError>

    /// TLS-wrapped dial. Verifies certificate chain against the
    /// system trust store unless `verify = false` is supplied
    /// (audit-marked, follow-up).
    fn dialTLS(self, host: String, port: Int) -> Result<Conn, NetError>

    fn listen(self, host: String, port: Int) -> Result<Listener, NetError>

    /// HTTP client construction. The returned client respects
    /// taskGroup cancellation on every operation.
    fn httpClient(self) -> HttpClient
}
```

**Host adapter**: `capability.hostNet` (canonical) and `net.host`
(transitional bridge — deprecated for v0.7 removal per §10.46.4).

**Fake adapter**: `std.capability.testing.FakeNet(routes = {...})` —
pre-canned responses keyed by `host:port`. Calls to `dial` return a
`Conn` backed by the fake's response stream; calls to unrouted
addresses return `NetError.RouteNotFound`.

**Determinism grade**: non-deterministic.

**URL safety (Phase 5)**: redirect-accepting methods carry
`#[requires("url_safe")]`. User input passes through `std.url.encode`
or `Url.parse(...)?` first.

#### 20.9.6 `Process` — subprocess + signals

```osty
pub interface Process {
    /// Run an external command synchronously. Returns the captured
    /// output and exit code; cancellation propagates through
    /// `Output.cancel()`.
    fn exec(self, cmd: String, args: List<String>) -> Result<Output, ProcError>

    /// Asynchronous variant — returns a Handle<Output> that joins
    /// when the child exits. Subject to G13 non-escape.
    fn spawn(self, cmd: String, args: List<String>) -> Handle<Output>

    fn pid(self) -> Int
    fn hostname(self) -> String

    /// Replace the current process image. Does not return on success;
    /// only the error path produces a value.
    fn replace(self, cmd: String, args: List<String>) -> Result<Never, ProcError>

    /// Terminate the current process with `code`. Does not return.
    fn exit(self, code: Int) -> Never
}
```

**Host adapter**: `capability.hostProcess` (canonical) and
`process.host` (transitional bridge).

**Fake adapter**: `std.capability.testing.FakeProcess(stubs = {...})`
— programmable command stubs. Each stub maps `(cmd, args)` to a
canned `Output` or `ProcError`.

**Determinism grade**: non-deterministic.

**Shell safety (Phase 5)**: `exec` and `spawn`'s args carry
`#[requires("shell_safe")]` for all but the first element (the
program name). User input through `std.shell.quote` first.

#### 20.9.7 `Console` — stdout / stderr

```osty
pub interface Console {
    fn print(self, text: String)
    fn println(self, text: String)
    fn eprint(self, text: String)
    fn eprintln(self, text: String)

    /// Read a single line from stdin (UTF-8). Returns `None` on EOF.
    fn readLine(self) -> Result<String?, IoError>

    /// Returns true when stdout is bound to an interactive TTY.
    /// Suitable for gating colored output and progress bars.
    fn isTTY(self) -> Bool
}
```

**Host adapter**: `io.console` — backed by process stdin/stdout/stderr.

**Fake adapter**: `std.capability.testing.FakeConsole()` — captures
stdout and stderr into in-memory buffers accessible via
`fakeConsole.stdoutCaptured()` / `stderrCaptured()`.

**Determinism grade**: deterministic output (writes don't depend on
external state). `#[reproducible(scope = "run")]` allows `Console`;
`scope = "target"` and `scope = "portable"` reject it because byte
ordering of interleaved stdout/stderr is run-dependent.

### 20.10 Host adapter factories — full registry

| Capability | Canonical factory | Returns | Deprecation status |
|---|---|---|---|
| `Clock` | `time.systemClock` | `Clock` | stable |
| `Rng` | `random.host` | `Rng` | stable |
| `Env` | `env.host` | `Env` | stable |
| `Fs` | `fs.host` | `Fs` | stable |
| `Net` | `capability.hostNet` | `Net` | stable |
| `Net` | `net.host` | `Net` | transitional bridge — v0.7 removal |
| `Process` | `capability.hostProcess` | `Process` | stable |
| `Process` | `process.host` | `Process` | transitional bridge — v0.7 removal |
| `Console` | `io.console` | `Console` | stable |

The transitional bridges (`net.host`, `process.host`) exist so v0.5 →
v0.6 migrations can stay within a module's existing namespace
(`net.*` / `process.*`) during the upgrade. They forward to the
canonical factory in `std.capability` and emit `W0750` deprecation on
use. v0.7 removes them; see `BREAKING_v0.6.md §3` for the timeline.

### 20.11 Fake registry for tests

`std.capability.testing` exposes deterministic fakes for every
canonical capability. Use them in `#[test]`-discovered functions to
replace the implicit determinism of `--legacy-globals`-style tests.

```osty
use std.capability.testing as ct

fn test_buildId_format() {
    let clock = ct.FakeClock(epoch_ms = 1_000_000)
    let rng = ct.FakeRng(seed = 42)
    let id = buildId(clock, rng)
    testing.assertEq(id, "1000000-1608637542")
}
```

The convenience factory `std.testing.capabilityFakes()` returns a
`CapabilityFakes` struct with all seven fakes preset — useful for
test functions that exercise capability-heavy code paths:

```osty
fn test_pipeline() {
    let f = std.testing.capabilityFakes()
    f.fakeFs.write("/tmp/in.txt", "hello")?
    runPipeline(f.fakeClock, f.fakeRng, f.fakeEnv, f.fakeFs, f.fakeNet,
                f.fakeProcess, f.fakeConsole)?
    testing.assertEq(f.fakeConsole.stdoutCaptured(), "OK\n")
}
```

### 20.12 Capability injection patterns

Three patterns for routing capabilities through a code base:

#### Pattern A — explicit parameter

The recommended baseline. Library functions receive what they need;
no implicit state, no thread-locals, no globals.

```osty
pub fn renderTimestamp(clock: Clock) -> String {
    "{clock.now().toIso8601()}"
}

pub fn loadConfig(env: Env, fs: Fs) -> Result<Config, ConfigError> {
    let path = env.get("CONFIG_PATH") ?? "/etc/app.toml"
    fs.readToString(path).mapErr(|_| ConfigError.NotFound(path))
        .andThen(|text| toml.parse(text))
}
```

#### Pattern B — entry-point ambient

At program / script / test entry points, `#[ambient(name1, ...)]`
binds default instances:

```osty
#[ambient(clock, env, fs, console)]
fn main() {
    let cfg = loadConfig(env, fs)?
    let ts = renderTimestamp(clock)
    console.println("[{ts}] config loaded: {cfg}")
}
```

The ambient bindings only exist in the entry function's body. Callees
still receive the capabilities as explicit parameters.

#### Pattern C — capability struct

When a function or struct needs many capabilities, package them
together:

```osty
pub struct AppCaps {
    pub clock: Clock,
    pub rng: Rng,
    pub env: Env,
    pub fs: Fs,
}

pub fn runApp(c: AppCaps) -> Result<(), Error> {
    let id = buildId(c.clock, c.rng)
    let cfg = loadConfig(c.env, c.fs)?
    process(id, cfg)
}
```

`AppCaps` is a regular struct; capability values are `interface`
references and follow Osty's reference semantics. Storing them in a
struct field doesn't violate any non-escape rule (only `Handle<T>` /
`TaskGroup` carry the G13 escape ban).

### 20.13 Forwarding between functions

Capability forwarding is *explicit* — callers pass them, callees
declare them. There is no implicit forwarding through dynamic
dispatch or thread-local state.

```osty
pub fn run(clock: Clock, rng: Rng) -> String {
    // Forward both capabilities to buildId.
    buildId(clock, rng)
}
```

Inside an `#[ambient]` function body, `buildId(clock, rng)` works
because `clock` and `rng` are bound as locals (per §20.3.1
desugaring).

Across `taskGroup` boundaries, capabilities flow through closure
captures:

```osty
#[ambient(clock)]
fn main() {
    taskGroup(|g| {
        let h = g.spawn(|| {
            // Closure captures `clock` from main's body.
            clock.now()
        })
        h.join()?
        Ok(())
    })
}
```

### 20.14 Anti-patterns and rejected forms

#### Library function with `#[ambient]` — `E0780`

```osty
#[ambient(clock)]                       // ERROR E0780
pub fn renderTimestamp() -> String {
    clock.now().toIso8601()
}
```

`#[ambient]` is restricted to entry-point functions. A library that
wants `Clock` declares it as a parameter; the test fake replaces it
deterministically.

#### Unknown ambient name — `E0781`

```osty
#[ambient(database)]                    // ERROR E0781
fn main() { ... }
```

The canonical set is `clock`, `rng`, `env`, `fs`, `net`, `process`,
`console`. User-defined capabilities cannot be ambient (`E0789`); pass
them explicitly.

#### `#[reproducible]` with non-deterministic capability — `E0784`

```osty
#[reproducible]                         // ERROR E0784
fn buildId(clock: Clock) -> String {
    clock.now().toIso8601()
}
```

Reproducibility requires the function's output to be fully determined
by its input. A `Clock` parameter (non-deterministic adapter)
violates this. The `#[pure]` annotation rejects all capability
parameters (`E0785`); `#[reproducible]` rejects only non-deterministic
ones.

#### Implicit forwarding does not exist

```osty
pub fn outer(clock: Clock) {
    inner()                              // ERROR — `inner` does not see `clock`
}

pub fn inner() {
    // No way to find `clock` from here.
}
```

The capability flow is exactly what the function signatures declare.

### 20.15 Compatibility — `--legacy-globals`

The compatibility mode `osty build --legacy-globals` (or
`[legacy] globals = true` in `osty.toml`) reactivates the v0.5
top-level effect functions. They desugar to capability host calls:

| v0.5 form | Desugared to |
|---|---|
| `time.now()` | `time.systemClock.now()` |
| `random.next()` | `random.host.next()` |
| `env.get("HOME")` | `env.host.get("HOME")` |
| `fs.readToString("/etc/passwd")` | `fs.host.readToString("/etc/passwd")` |
| `os.exec("ls", [])` | `capability.hostProcess.exec("ls", [])` |
| `net.dial("example.com", 443)` | `capability.hostNet.dial("example.com", 443)` |

Each call site emits `W0750` (deprecation warning). The package's
`[stability]` is forced to `experimental` whenever any legacy global
is reachable. v0.7 removes the desugar and the flag — all uses
become `E0701` (`unknown name in this scope`).

`§10.46` is the authoritative migration catalog with per-function
mapping.

### 20.16 Forward compatibility

The capability surface in v0.6 is a *baseline* — adding new
capabilities to the canonical set in a future minor release is an
additive change (existing code keeps working with the seven). Adding
new methods to an existing capability interface is also additive; any
production code that implements `Clock` (uncommon — usually the host
adapter is the only implementation) need only carry forward the
interface implementation.

Removing a method or changing a method signature is a breaking
change requiring a major version bump (per §3.14.3). The non-canonical
adapters (`net.host`, `process.host`) carry their own removal schedule
documented in `BREAKING_v0.6.md`.
