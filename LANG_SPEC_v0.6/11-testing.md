## 11. Testing

Osty v0.6 ships a built-in testing surface. Tests live alongside code
in `_test.osty` files or as `#[test]`-annotated functions in
production sources; the runner is `osty test`, with assertions
provided by `std.testing`. The v0.6 surface adds: declarative golden
tests via `#[golden]` (§11.5.2, G45) which reuse the v0.5 snapshot
on-disk format, executable specification clauses through `spec { }`
blocks (§3.13, G43) which run as tests under `osty test --spec`, and
the `#[example]` annotation (§3.12, G42) which auto-checks
`input → output` declarations under `osty test --example`.

Test files use the `_test.osty` suffix and live alongside code in the
same package. Functions whose names begin with lowercase `test` and
take no arguments are discovered and run by `osty test`. A top-level
zero-arity function annotated with `#[test]` is also discovered, even in
a production source file; annotated test functions are excluded from
production builds.

```osty
// auth/login_test.osty
use std.testing
use std.capability.testing as ct

fn testLoginSuccess() {
    let db = ct.FakeDb.seeded(["alice"])
    let result = login("alice", "valid_pass", db)
    testing.assert(result.isOk())
}

fn testLoginRejectsBlankUser() {
    let db = ct.FakeDb.empty()
    let result = login("", "anything", db)
    testing.assertEq(result, Err(InvalidInput))
}
```

The production-side `login` here takes a `Db` capability parameter and
the tests inject a deterministic fake — there is no global database
client, no fixed clock, and no leaked filesystem state. v0.6's
information-flow rule (§21) is satisfied by construction: tainted
input never reaches a sink because the `Db` interface only accepts
sanitized values.

Test files are excluded from production builds.

### 11.1 Assertion API

```
testing.assert(cond: Bool)
testing.assertEq<T: Equal>(actual: T, expected: T)
testing.assertNe<T: Equal>(actual: T, expected: T)
testing.expectOk<T, E>(result: Result<T, E>) -> T
testing.expectError<T, E>(result: Result<T, E>) -> E
testing.fail(msg: String) -> Never
testing.context(msg: String, body: fn())
```

### 11.2 Detailed Failure Output

The compiler recognizes the above `testing` functions specifically and
generates detailed failure output including:

- Source location (file and line)
- Textual form of the argument expressions (captured at compile time)
- Runtime values (formatted structurally)
- A structural diff for composite values

**Structural diff (shipped).** When both sides of `assertEq` share
a diffable shape, the failure message appends a trim-prefix /
trim-suffix line diff with up to 3 lines of shared context. Lines
that differ are prefixed with `- left` or `+ right`; shared context
lines are prefixed with two spaces. The diff is only computed on the
failure path — passing asserts pay zero. Today's diff coverage:

- Two `String` values — compared directly, line by line.
- Two `List<T>` values with the same primitive `T` (`Int`, `Float`,
  `Bool`, `String`) — each list is rendered as a multi-line literal
  (`[\n  elem,\n  ...\n]`) via `osty_rt_list_primitive_to_string`
  before the diff runs, so an element-level divergence surfaces as
  a single-line `-`/`+` pair.

**Still deferred.** Structs, enums, Maps, Sets, Lists of composite
elements (`List<Struct>`, `List<Map<K, V>>`), and Lists parameterised
by `Char` or `Byte` fall back to source-text-only rendering. A full
solution needs `ToString` protocol dispatch and per-shape format
rules in the backend.

For example:

```osty
let user = getUser("alice")
testing.assertEq(user, User { name: "alice", age: 30, email: "a@x.com" })
```

On failure produces:

```
assertion failed at user_test.osty:42
  testing.assertEq(user, User { name: "alice", age: 30, email: "a@x.com" })

  actual:
    User {
      name: "alice",
      age: 25,          // differs
      email: "a@x.com",
    }

  expected:
    User {
      name: "alice",
      age: 30,
      email: "a@x.com",
    }
```

This is not a general-purpose macro facility. The compiler has built-in
knowledge of the `std.testing` assertion functions only. User-defined
functions cannot access argument source text.

### 11.3 Context

`testing.context(msg, body)` attaches a prefix to any assertion failures
in the callback. Useful for table-driven tests:

```osty
fn testAdd() {
    let cases = [
        (1, 2, 3),
        (0, 0, 0),
        (-1, -1, -2),
    ]
    for (i, (a, b, expected)) in cases.enumerate() {
        testing.context("case {i}: add({a}, {b})", || {
            testing.assertEq(add(a, b), expected)
        })
    }
}
```

### 11.4 Benchmarks

Functions whose names begin with `bench` and take no arguments are
benchmark functions, run by `osty test --bench`:

```osty
fn benchParseJson() {
    testing.benchmark(1000, || {
        let _: Config = json.decode(sampleText)?
        Ok(())
    })
}
```

`testing.benchmark(iterations, body)` runs the closure and prints two
lines per call site:

```
bench <abs-path>:<line> iter=<N> total=<T>ns avg=<A>ns
  min=<Mn>ns p50=<M50>ns p99=<M99>ns max=<Mx>ns
```

`T` is the monotonic-clock elapsed time around the loop, `A` is
`T / N` (or `0` when `N <= 0`). Values are nanoseconds throughout.
The distribution line is computed from per-iteration samples.
`<abs-path>` is the source file containing the `testing.benchmark(...)`
call (`<bench>` when the compiler has no source path), and `<line>` is
that call's 1-based line number.

Before the timed loop the compiler inserts a warmup pass of
`clamp(N/10, 1, 1000)` iterations that is **not** counted in `iter=`;
this reduces cold-cache / branch-predictor noise on short bodies.

`?` inside the closure body is allowed. If it sees `Err(e)` or `None`
on any iteration the benchmark prints `bench \`?\` propagated failure
at <abs-path>:<line>` to stdout and exits the bench with failure
status; the summary line is not emitted for that benchmark.

In bench mode:

- Only `bench*`-named, zero-arity functions are discovered. `#[test]`
  annotations have no effect on bench discovery, and `test*` functions
  are skipped.
- `--bench` and `--doc` are mutually exclusive.
- Assertion failures inside the closure abort the bench with the
  normal `testing.*` diagnostic; no summary line is printed for a
  failed bench.
- `osty test --bench --benchtime <duration>` activates auto-tuning:
  the declared `N` is replaced by the output of a 10-iteration probe
  scaled to hit `<duration>` (clamped to `[10, 100_000_000]` with 20%
  headroom). Without the flag the declared `N` is authoritative. The
  flag uses Go-style durations (`500ms`, `2s`, `1m`) and requires
  `--bench`.

### 11.5 Snapshots / Golden Tests

Two surface forms are provided: a *runtime form* via
`std.testing.snapshot()` (§11.5.1, baseline) and a *declarative form*
via the `#[golden]` annotation (§11.5.2, v0.6 G45). Both share the
same on-disk format — a snapshot file written by one form can be
consumed by the other.

#### 11.5.1 Runtime form — `testing.snapshot()`

`std.testing.snapshot` provides golden-file testing within a regular
test function:

```osty
fn testRenderOutput() {
    let output = render(input)
    testing.snapshot("render_basic", output)
}
```

**Location.** The golden file lives at
`<source_dir>/__snapshots__/<sanitize(name)>.snap`, where
`source_dir` is the directory of the test source file and `sanitize`
follows the rule from [§11.7](#117-test-order) (letters, digits,
underscore pass through; everything else collapses to `_`; empty or
all-sanitized names fall back to the stem `snapshot`). The source
path is pinned at compile time so snapshot resolution is independent
of the process working directory.

**Lifecycle.**

| Golden state | Result |
|---|---|
| Missing | Write `output` to the golden; pass with `snapshot: created <path>` on stdout. |
| Matches `output` byte-for-byte | Pass silently. |
| Differs from `output` | Print `testing.snapshot(<name>) mismatch: <path>` + a line-level diff (same shape as §11.2) to stdout and exit 1 — same observable outcome as any failing assertion. |

**Accepting new output.** `osty test --update-snapshots` (which sets
`OSTY_UPDATE_SNAPSHOTS=1` for the emitted test binaries) overwrites
every golden encountered during the run and prints
`snapshot: updated <path>` for each — tests still pass.

**Test-harness override.** The runtime honors
`OSTY_SNAPSHOT_DIR=<path>` as a drop-in replacement for the source
directory when resolving the golden's location. This is only intended
for test harnesses that exercise the snapshot machinery itself and
want to isolate writes into a tempdir; production `osty test` runs
leave it unset.

#### 11.5.2 Declarative form — `#[golden]` (G45)

A function may carry `#[golden(path, mode)]` to declare its output is
compared against an explicit snapshot file. The annotation form is
designed for *compiler / formatter / docgen / diagnostic* output
testing — exactly the workloads where the language's own toolchain is
exercised.

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

**Modes.**

| Mode | Comparison |
|---|---|
| `"text"` *(default)* | Byte-exact. |
| `"ast"` | Reparse both sides as Osty source, compare normalised AST. Whitespace / comments / formatter idiosyncrasies are ignored. |
| `"json"` | Parse as JSON, compare structurally (key order ignored). |
| `"diag"` | Osty diagnostic format — same code/message comparable across `Span` deltas. |

**Reproducibility.** A `#[golden]` function is implicitly
`#[reproducible(scope = "target")]` (§3.11). Calling non-deterministic
capabilities or unordered iteration is `E0444`. AST mode applied to
output that is not valid Osty source is `E0446`. Missing snapshot on
first run is `E0445` (run `osty test --update-golden`). A snapshot's
embedded `source-hash` header that disagrees with the function's
current definition is `W0444`.

**Snapshot file format.**

```
# osty-golden-v1
# function: TestFormatBinaryOp
# mode: ast
# fixture: sampleBinaryExpr   (optional; references #[fixture(name)])
# generated: 2026-05-07T12:34:56Z
# source-hash: abc123...

fn add(x: Int, y: Int) -> Int { x + y }
```

Header lines start with `#`; the body begins after one blank line.

**Tooling.** `osty test --golden` runs the comparison; `osty test
--update-golden[=<path>]` rewrites snapshots. See §13.8.

#### 11.5.3 Mode selection guide

The four golden modes target different output shapes. Choose the
strictest mode that the output actually demands:

| Output shape | Recommended mode | Why |
|---|---|---|
| Free-form text (logs, error messages without span info) | `"text"` | Byte-exact catches every regression |
| Generated Osty source (formatter, codegen output) | `"ast"` | Whitespace / comment-only diffs ignored, semantic regressions caught |
| Structured data exports (`osty context`, `osty doc --format=json`) | `"json"` | Key ordering / pretty-print noise ignored |
| Diagnostic output (`osty check` with positions) | `"diag"` | Span column shifts ignored, code + message + suggested-fix preserved |

Fallback rule: if uncertain, start with `"text"`. Loosen to `"ast"`
or `"diag"` only when the strict mode produces noisy diffs that
don't reflect real regressions.

#### 11.5.4 Workflow for accepted golden updates

A golden update is a deliberate change to a `.snap` file checked in
alongside the test. The recommended workflow:

```sh
# 1. Run tests; observe golden mismatches.
$ osty test --golden
testing.assertGolden(...) mismatch at fixtures/format_expr.snap

# 2. Inspect the diff; verify the new output is the intended one.
$ osty test --golden --report=diff
- fixtures/format_expr.snap
+ test output
@@ -1,3 +1,3 @@
- 1 + 2 * 3
+ 1 + (2 * 3)

# 3. Accept the new output explicitly.
$ osty test --update-golden=fixtures/format_expr.snap
snapshot: updated fixtures/format_expr.snap

# 4. Commit the snapshot change with the source change.
$ git add fixtures/format_expr.snap toolchain/format.osty
$ git commit -m "format: parenthesize precedence-ambiguous binary ops"
```

`osty test --update-golden` (no path) updates **every** mismatched
snapshot in one pass. Use the per-path form for targeted updates so
unrelated regressions stay visible as failures.

**CI policy.** A pull request that touches snapshot files **must**
also touch the source file producing the snapshot — the relationship
is reviewed manually. `osty audit --golden-orphans` enumerates
snapshots whose source-hash header does not match any current
function in the workspace; orphans are removed at PR review time.

#### 11.5.5 `#[fixture]` integration

A `#[golden]` function may reference a `#[fixture(name)]` to
parameterize the input. The fixture body runs once per test
invocation and is *recorded* in the snapshot header so reviewers can
see which input produced which output:

```osty
#[fixture(name = "sampleBinaryExpr")]
fn sampleBinaryExpr() -> Expr {
    parseExpr("1 + 2 * 3")
}

#[golden("fixtures/format_sampleBinary.snap")]
fn testFormatSampleBinary() {
    let expr = sampleBinaryExpr()
    testing.assertGolden(formatExpr(expr))
}
```

The snapshot's `# fixture: sampleBinaryExpr` header provides the
audit trail. Updating `sampleBinaryExpr`'s body without rerunning
`--update-golden` produces `W0444` (source-hash mismatch) on the
next test run, prompting the author to either accept or revert the
fixture change.

#### 11.5.6 In-tree vs out-of-tree snapshot organization

Two layouts are supported:

```
project/
├── toolchain/
│   ├── format.osty
│   └── format_test.osty                 ← #[golden] functions live here
└── fixtures/
    └── format/
        ├── format_binary_op.snap        ← snapshot files
        └── format_sampleBinary.snap
```

The `path` argument of `#[golden(path, ...)]` is resolved relative
to the project root (the directory containing `osty.toml`). Layout
within `fixtures/` is a project convention; `osty audit
--golden-orphans` walks the whole tree.

For *runtime-form* snapshots (`testing.snapshot(name, value)`), the
file lives at `<source_dir>/__snapshots__/<sanitize(name)>.snap` —
a per-test-file directory. The two layouts coexist; pick one
consistently per test file.

### 11.6 Parallel Execution

Tests run in parallel by default. Use `--serial` to force sequential
execution. Tests depending on shared mutable state should use
`std.sync` primitives or opt into serial execution.

### 11.7 Test Order

Within each execution mode (parallel or serial), `osty test` chooses a
**randomized** start order. The random seed is printed at the start of
every run (and at the head of any failure output) so failures are
reproducible:

```
$ osty test
running 42 tests (seed 0x8F3A2B71)
...

$ osty test --seed 0x8F3A2B71   # reproduce the exact same order
```

Declaration-order or alphabetical execution is not provided. Tests
that accidentally share state are surfaced by the randomization; fix
the dependency rather than pinning the order.

### 11.8 Setup / Teardown

Osty does not expose `beforeEach`/`afterEach` hooks. Shared setup
belongs in helper functions called from each test, or in a
`testing.context` block. Test-local cleanup uses `defer`. Because tests
may run in parallel (§11.6), any shared fixture must be constructed
per-test or guarded with `std.sync` primitives.

---

### 11.9 Capability fakes for deterministic tests

v0.6 의 `std.capability.testing` 모듈은 7 canonical capability 별
deterministic fake 을 제공한다. 테스트는 production 코드에 fake
capability 를 주입해 *시간 / 난수 / 환경 / 파일시스템 / 네트워크 /
프로세스 / 콘솔* 모든 사이드 이펙트를 hermetic 하게 만들 수 있다.

#### 11.9.1 Fake capability registry

| Capability | Fake | 결정성 보장 |
|---|---|---|
| `Clock` | `FakeClock(epoch_ms = N)` | 모든 `now()` 가 `epoch_ms` 반환; `monotonic()` 매 호출 +1ms; `sleep()` 즉시 반환 |
| `Rng` | `FakeRng(seed = N)` | seed 로 결정된 xorshift64 시퀀스 |
| `Env` | `FakeEnv(vars = {...}, args = [...])` | in-memory map / list |
| `Fs` | `FakeFs(layout = {...})` | in-memory 파일 트리 |
| `Net` | `FakeNet(routes = {...})` | host:port → canned response |
| `Process` | `FakeProcess(stubs = {...})` | (cmd, args) → canned `Output` |
| `Console` | `FakeConsole()` | stdout/stderr capture |

#### 11.9.2 단일 capability 주입

```osty
use std.capability.testing as ct

fn buildId(clock: Clock, rng: Rng) -> String {
    "{clock.now().toEpochMillis()}-{rng.next()}"
}

#[test]
fn test_buildId_format() {
    let clock = ct.FakeClock(epoch_ms = 1_000_000)
    let rng = ct.FakeRng(seed = 42)
    let id = buildId(clock, rng)
    testing.assertEq(id, "1000000-1608637542")
}
```

테스트가 깨지지 않는 것은 fake 가 *결정적* 이기 때문이다 — 같은
`epoch_ms` + `seed` 는 항상 같은 출력을 만든다.

#### 11.9.3 다중 capability — convenience factory

```osty
fn runPipeline(
    clock: Clock,
    rng: Rng,
    fs: Fs,
    console: Console,
) -> Result<(), Error> {
    let id = "{clock.now().toEpochMillis()}-{rng.next()}"
    fs.write("/tmp/{id}.log", "started")?
    console.println("pipeline {id} started")
    Ok(())
}

#[test]
fn test_pipeline_writes_and_logs() {
    let f = std.testing.capabilityFakes()
    runPipeline(f.fakeClock, f.fakeRng, f.fakeFs, f.fakeConsole)?
    testing.assert(f.fakeFs.exists("/tmp/0-1608637542.log"))
    testing.assertEq(f.fakeConsole.stdoutCaptured(), "pipeline 0-1608637542 started\n")
}
```

`std.testing.capabilityFakes()` 는 7 canonical fake 모두 preset 한
`CapabilityFakes` struct 를 반환. 단일 호출로 모든 capability 가
hermetic 으로 준비된다.

#### 11.9.4 Fake assertions

각 fake 는 검증을 위한 helper 메서드 노출:

| Fake | Assertion helper | 의미 |
|---|---|---|
| `FakeConsole` | `stdoutCaptured() -> String` | print 누적 |
| `FakeConsole` | `stderrCaptured() -> String` | eprint 누적 |
| `FakeFs` | `exists(p) -> Bool`, `readToString(p)` | 작성 후 검증 |
| `FakeNet` | `dialedHosts() -> List<String>` | dial 호출 host 추적 |
| `FakeProcess` | `executedCommands() -> List<(String, List<String>)>` | exec 호출 카탈로그 |
| `FakeEnv` | `set` 후 `get` 정합 | mutation 추적 |
| `FakeRng` | (deterministic 이므로 별도 helper 없음) | seed 기준 sequence |

#### 11.9.5 Cancel 전파 테스트

```osty
fn longRunning(clock: Clock) -> Result<(), Error> {
    clock.sleep(Duration.seconds(60))?
    Ok(())
}

#[test]
fn test_cancel_propagates_through_sleep() {
    let clock = ct.FakeClock(epoch_ms = 0)
    taskGroup(|g| {
        let h = g.spawn(|| longRunning(clock))
        g.cancel(Cancelled.New("test"))
        match h.join() {
            Err(e) -> testing.assert(e is Cancelled),
            Ok(_) -> testing.fail("expected Cancelled"),
        }
        Ok(())
    })
}
```

`FakeClock.sleep()` 은 즉시 반환하지만 `taskGroup` cancel 이 도달
하면 `Err(Cancelled)` 로 응답.

### 11.10 `spec { }` block as tests

`spec { example: ... }` 절은 `osty test --spec` 모드에서 *자동 등록
테스트* 가 된다 (G43, §3.13). spec block 의 example 은 `assertEq`
호출 없이 boolean expression 으로 작성되며, runner 가 결과를 평가.

#### 11.10.1 Example clauses

```osty
fn normalizeEmail(s: String) -> String {
    spec {
        example: normalizeEmail(" Alice@EXAMPLE.COM ") == "alice@example.com"
        example: normalizeEmail("") == ""
    }
    s.trim().toLowerCase()
}
```

`osty test --spec` 호출 시 두 example 이 자동 테스트로 등록 — 출력은
`spec[normalizeEmail#example:1] PASS` 형식.

#### 11.10.2 Law / invariant clauses

```osty
fn normalizeEmail(s: String) -> String {
    spec {
        example: normalizeEmail(" Hi ") == "hi"
        law: result == result.trim()
        law: result == result.toLowerCase()
        invariant: result.indexOf(" ") == -1
    }
    s.trim().toLowerCase()
}
```

`law:` / `invariant:` 는 v0 (Phase 3) 에선 *문서화 + LSP hover* 만 —
실행되지 않는다. v1 (Phase 5) 에서 `forall` property test 자동
생성과 함께 enforcement.

`result` 는 함수 반환값을 가리키는 *virtual binding* — `law:` /
`invariant:` 안에서만 의미를 가진다.

#### 11.10.3 Spec block 와 일반 test 의 공존

```osty
fn normalize(s: String) -> String {
    spec {
        example: normalize("HI") == "hi"
        law: result.length() <= s.length()
    }
    s.toLowerCase().trim()
}

// 같은 함수에 일반 test
#[test]
fn test_normalize_preserves_alpha() {
    testing.assertEq(normalize("hello"), "hello")
}
```

두 형식 모두 등록 — `spec { example: }` 는 `osty test --spec` 시,
`#[test]` 는 `osty test` 시 (`--spec` 도 spec example 포함).

### 11.11 `#[example]` annotation as tests

`#[example(input = ..., output = ...)]` (G42, §3.12) 는 함수
선언에 *machine-readable* example 부착 — `osty test --example` 모드
에서 자동 검증.

```osty
#[example(input = "alice@example.com", output = "Some(...)")]
#[example(input = "invalid", output = "None")]
pub fn parseEmail(s: String) -> Email? { ... }
```

`osty test --example` 호출 시 두 example 모두 평가 — `parseEmail("alice@example.com")
== Some(Email{...})` / `parseEmail("invalid") == None` 로 비교.
실패 시 `example[parseEmail#1] FAIL: expected Some(...), got None`.

#### 11.11.1 Capability + example

`#[example(uses = "name")]` 는 fixture 참조 — fixture 함수가
capability 인스턴스를 반환하면 example 호출 시 fixture 가 먼저
평가되어 인자로 주입.

```osty
#[fixture(name = "fakeDb")]
fn fakeDb() -> Db {
    let f = std.capability.testing.FakeDb()
    f.seed(User.parse("alice@example.com")?)
    f
}

#[example(input = "alice@example.com", uses = "fakeDb", output = "Ok(42)")]
#[example(input = "missing@x.com", uses = "fakeDb", output = "Err(NotFound)")]
pub fn lookupUser(email: String, db: Db) -> Result<UserId, LookupError> { ... }
```

`uses = "fakeDb"` 는 같은 파일 / 같은 패키지의 `#[fixture(name =
"fakeDb")]` 를 참조. fixture 는 zero-arity 이므로 매 example 호출마다
새로 생성 — hermetic 보장.

### 11.12 `#[golden]` annotation tests

`#[golden(path, mode)]` (G45, §11.5.2) 는 함수 출력을 디스크 snapshot
파일과 비교. `osty test --golden` / `osty test --update-golden` 으로
실행/갱신.

#### 11.12.1 Compiler / formatter / docgen 자가 테스트

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

#### 11.12.2 Mode 별 비교 의미

| Mode | 비교 |
|---|---|
| `"text"` (default) | byte-exact |
| `"ast"` | reparse 후 AST 비교 (whitespace / 주석 무시) |
| `"json"` | structural JSON 비교 (key 순서 무시) |
| `"diag"` | Osty diagnostic format — Span 차이 무시, code/message 비교 |

#### 11.12.3 Reproducibility 요구

`#[golden]` 함수는 *암묵적으로* `#[reproducible(scope = "target")]` —
non-deterministic capability 수신 시 `E0444`. 시간/난수/환경에
의존하는 출력을 snapshot 화하면 `osty test --golden` 이 매 실행마다
실패하므로 의도적 거부.

```osty
#[golden("fixtures/timestamp.snap")]
fn testTimestamp(clock: Clock) {  // ERROR E0444
    testing.assertGolden(clock.now().toString())
}
```

#### 11.12.4 Snapshot 파일 형식

```
# osty-golden-v1
# function: TestFormatBinaryOp
# mode: ast
# fixture: sampleBinaryExpr
# generated: 2026-05-07T12:34:56Z
# source-hash: abc123...

fn add(x: Int, y: Int) -> Int { x + y }
```

헤더 (`#` 시작) 는 메타데이터; 본문은 빈 줄 다음. `source-hash` 는
함수 정의 의 해시 — 함수 변경 시 stale snapshot 알림 (`W0444`).

#### 11.12.5 Update workflow

```sh
# 신규 snapshot 만 생성 (기존 변경 안 함)
osty test --update-golden=missing

# 모든 snapshot 갱신
osty test --update-golden

# 특정 함수만
osty test --update-golden --filter=TestFormatBinaryOp

# Diff 만 보고 적용 안 함 (CI dry-run)
osty test --golden --report=diff
```

### 11.13 `#[fixture]` 공유 canonical instance

`#[fixture(name = "...")]` (G42, §3.12) 는 zero-arity 함수가 *재사용
가능한* canonical instance 를 반환함을 표시. 사용처:

1. `#[example(uses = "name")]` 의 입력
2. `osty doc` 의 코드 예시
3. `osty context <symbol>` JSON 의 fixtures_referenced 항목
4. property test seed (v1 spec block)
5. `#[golden]` 함수의 입력 (수동 호출 패턴)

#### 11.13.1 Zero-arity 제약

`#[fixture]` 함수는 인자를 받지 않는다 (`E0432`). 매 호출마다 *새
instance* 가 생성 — 테스트 간 hermetic 보장.

```osty
#[fixture(name = "sampleUser")]
fn sampleUser() -> User {
    User.builder()
        .email("alice@example.com")
        .age(30)
        .build()
}

#[fixture(name = "sampleDb")]
fn sampleDb() -> Db {
    let db = std.capability.testing.FakeDb()
    db.seed(sampleUser())  // 다른 fixture 호출 가능
    db
}
```

#### 11.13.2 Cross-fixture 호출

fixture 함수는 *서로 호출 가능* — 위 `sampleDb` 가 `sampleUser` 를
호출. fixture 간 cycle 은 `E0433` (cycle detected — fixture 로직
재구성 필요).

#### 11.13.3 Fixture 와 capability fake 결합

```osty
#[fixture(name = "appCaps")]
fn appCaps() -> AppCaps {
    let f = std.testing.capabilityFakes()
    AppCaps {
        clock: f.fakeClock,
        rng: f.fakeRng,
        env: f.fakeEnv,
        fs: f.fakeFs,
    }
}

#[example(input = "/etc/app.toml", uses = "appCaps", output = "Ok(...)")]
#[example(input = "/missing", uses = "appCaps", output = "Err(NotFound)")]
pub fn loadConfig(path: String, caps: AppCaps) -> Result<Config, ConfigError> { ... }
```

### 11.14 Property-based testing (v1 outlook)

v0.6 baseline 은 `spec { example: }` 만 자동 실행. **v1 (Phase 5)** 에서
`forall x in gen: ...` 형식의 property test 자동 등록 추가:

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

`forall x in gen.list(gen.int(), 128)` 는 `gen.list(elemGen,
maxLen)` 으로 `List<Int>` (길이 0..=128) 시퀀스 생성. v1 의 spec
block runner 가 default 100 iterations 수행 + shrinking on failure.

자세한 generator API 는 §10.5 (std.testing.gen) 참조.

### 11.15 Test 모드 통합

```sh
# 기본 — #[test] / test_* / bench_* / spec block example / #[example]
osty test

# spec block example 만
osty test --spec

# #[example] 만 (faster than --spec since no spec block discovery)
osty test --example

# golden snapshot 비교
osty test --golden

# golden snapshot 갱신
osty test --update-golden

# doc 블록 (`///`) 안의 doctest
osty test --doc

# 모든 모드 동시
osty test --spec --example --golden --doc
```

`--filter=name` 으로 패턴 매칭, `--serial` 로 병렬 비활성화,
`--seed=N` 으로 test order 의 randomization seed 고정 (§11.7).

### 11.16 CI / test runner integration

```yaml
# .github/workflows/test.yml
- name: Test
  run: osty test --report=junit > test-results.xml

- name: Spec block tests
  run: osty test --spec --strict --report=json

- name: Golden snapshot
  run: osty test --golden --report=diff
  # PR 가 의도적 snapshot 갱신을 포함하면 osty test --update-golden 후 commit
```

`osty bench --budget` 은 §3.15.2 의 runtime budget 검증을 추가
(`time_ms` / `p99_ms` 회귀 시 fail).

### 11.17 Forward compatibility

테스트 surface 의 SemVer 영향:

| 변경 | 영향 |
|---|---|
| `#[example]` / `#[golden]` / `#[fixture]` 추가 | additive (테스트만 영향) |
| `std.testing` API 추가 | additive |
| `std.capability.testing.Fake*` 메서드 추가 | additive |
| Existing fake 의 메서드 시그니처 변경 | breaking |
| `osty test --<mode>` flag 제거 | breaking |
| Spec block clause syntax 변경 (`example:` / `law:`) | breaking |

`#[stability("stable")]` API 가 테스트 surface (e.g., custom Fake
impl) 를 노출하면 `osty publish` 의 SemVer 룰 적용.

### 11.18 Capability test recipes

This section catalogues canonical recipes for testing capability-typed
code. Each recipe states the production shape, the deterministic test
fake, and what the recipe specifically guards against.

#### 11.18.1 Pure recipe — no capability needed

If a function takes no capability parameter, no fake is required.
This is the cheapest test path; the recipe is to *keep functions
this way* whenever possible.

```osty
#[reproducible(scope = "target")]
pub fn classify(text: String) -> Category { ... }

#[test]
fn test_classify_short_text() {
    testing.assertEq(classify("hello"), Category.Greeting)
}
```

Production code that performs an effect should accept a capability
parameter rather than calling a global; the recipes below assume that
discipline.

#### 11.18.2 Single capability — direct fake

```osty
fn buildId(clock: Clock, rng: Rng) -> String {
    "{clock.now().toEpochMillis()}-{rng.next()}"
}

#[test]
fn test_buildId_is_deterministic() {
    let clock = std.capability.testing.FakeClock(epoch_ms = 1_000_000)
    let rng = std.capability.testing.FakeRng(seed = 42)
    let id = buildId(clock, rng)
    testing.assertEq(id, "1000000-1608637542")
}
```

Guards against: hidden time / randomness dependencies. The result is
byte-equal across runs because both fakes are deterministic.

#### 11.18.3 Filesystem recipe — `FakeFs` layout

```osty
fn loadConfig(fs: Fs, path: String) -> Result<Config, Error> {
    let text = fs.readToString(path)?
    json.parse(text)
}

#[test]
fn test_loadConfig_reads_layout() {
    let fs = std.capability.testing.FakeFs.fromLayout({
        "/etc/app.toml": "{ \"port\": 8080 }",
    })
    let cfg = loadConfig(fs, "/etc/app.toml")?
    testing.assertEq(cfg.port, 8080)
}

#[test]
fn test_loadConfig_missing_file_errors() {
    let fs = std.capability.testing.FakeFs.empty()
    let result = loadConfig(fs, "/nope")
    testing.expectError(result)
}
```

Guards against: tests reaching the real filesystem (and thus
race-conditioning with other tests, leaking artifacts, or depending
on the test runner's working directory).

#### 11.18.4 Network recipe — `FakeNet` route table

```osty
fn fetchHealth(net: Net, host: String) -> Result<HealthStatus, Error> {
    let conn = net.connect("{host}:80")?
    defer conn.close()
    io.writeString(conn, "GET /health HTTP/1.0\r\n\r\n")?
    let body = io.readAll(conn)?
    HealthStatus.parse(body.toString()?)
}

#[test]
fn test_fetchHealth_parses_response() {
    let net = std.capability.testing.FakeNet.routes({
        "example.com:80": std.capability.testing.cannedResponse(
            "HTTP/1.0 200 OK\r\n\r\n{\"status\":\"ok\"}",
        ),
    })
    let result = fetchHealth(net, "example.com")?
    testing.assertEq(result.status, "ok")
}
```

Guards against: tests requiring live network endpoints, port
collisions, network policy issues in CI.

#### 11.18.5 Cancellation recipe — `taskGroup` + `g.cancel`

```osty
fn longRunning(clock: Clock) -> Result<(), Error> {
    clock.sleep(60.s)?
    Ok(())
}

#[test]
fn test_longRunning_honors_cancel() {
    let clock = std.capability.testing.FakeClock(epoch_ms = 0)
    taskGroup(|g| {
        let h = g.spawn(|| longRunning(clock))
        g.cancel(Cancelled.New("test"))
        match h.join() {
            Err(e) -> testing.assert(e.downcast::<Cancelled>().isSome()),
            Ok(_) -> testing.fail("expected Cancelled"),
        }
        Ok(())
    })
}
```

Guards against: blocking calls that fail to honor `taskGroup`
cancellation. `FakeClock.sleep` returns immediately by default, but
honors the cancel signal of the surrounding `taskGroup` exactly as
the production `Clock.sleep` does.

#### 11.18.6 Information-flow recipe — taint preserved through fakes

```osty
fn safeRender(net: Net, console: Console, conn: TcpConn) -> Result<(), Error> {
    let raw: Bytes = io.readAll(conn)?
    let text: String = raw.toString()?
    let html = std.html.escape(text)         // sanitizes user_input → html_safe
    console.println(html)
    Ok(())
}

#[test]
fn test_safeRender_writes_escaped_output() {
    let f = std.testing.capabilityFakes()
    let conn = std.capability.testing.fakeTcp("<script>alert(1)</script>")
    safeRender(f.fakeNet, f.fakeConsole, conn)?
    let out = f.fakeConsole.stdoutCaptured()
    testing.assertEq(out, "&lt;script&gt;alert(1)&lt;/script&gt;\n")
}
```

Guards against: regression in sanitization. The fake `Console`'s
captured output is byte-equal to what the production `Console` would
emit, so flow-tag bugs surface as observable failures.

#### 11.18.7 Spec block + capability recipe

`spec { example: ... }` clauses (§3.13) inside a capability-typed
function run under the `--spec` mode. Capability instances that the
function takes must be provided either by `#[example(uses = "name")]`
referencing a fixture, or by explicit closure construction inside the
example expression:

```osty
#[fixture(name = "frozenClock")]
fn frozenClock() -> Clock {
    std.capability.testing.FakeClock(epoch_ms = 0)
}

#[fixture(name = "seededRng")]
fn seededRng() -> Rng {
    std.capability.testing.FakeRng(seed = 42)
}

#[example(input = "()", uses = "frozenClock", uses = "seededRng",
          output = "\"0-1608637542\"")]
fn buildId(clock: Clock, rng: Rng) -> String {
    spec {
        example: buildId(frozenClock(), seededRng()) == "0-1608637542"
    }
    "{clock.now().toEpochMillis()}-{rng.next()}"
}
```

The `uses = "name"` form composes; multiple `uses =` repeats inject
each named fixture in order. v0.6 baseline runs the inline
`example:` clause; the `#[example]` annotation form runs under
`osty test --example`.
