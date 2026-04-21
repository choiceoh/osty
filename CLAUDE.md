# CLAUDE.md — Osty 프로젝트 규칙

## 프로젝트

**Osty**: 정적 타입, GC 기반, 범용 프로그래밍 언어의 **셀프호스팅 컴파일러/툴체인**.
언어 스펙은 v0.5 (현행 baseline), 네이티브 백엔드는 LLVM.

- **셀프호스팅**: 컴파일러 본체(렉서/파서/리졸버/체커/린트/포매터/LLVM 코드젠/LSP 정책)는 전부 **Osty로 작성** (`toolchain/*.osty`)
- Go는 **호스트 경계와 부트스트랩 역할만**: I/O, JSON-RPC, CLI 진입점, Osty→Go 셀프호스트 시드(`internal/selfhost/generated.go`), 얇은 어댑터(`internal/lexer`·`internal/parser` 등은 수십 줄짜리 파사드)
- 외부 Go 의존성: `golang.org/x/sys`, `golang.org/x/term`만 허용 (추가 금지)
- 공개 백엔드: LLVM만. Go 트랜스파일러는 `internal/bootstrap/gen`으로 격리되어 `cmd/osty-bootstrap-gen` 개발자 바이너리를 통해 `internal/selfhost/generated.go` 재생성 용도로만 쓰인다. LLVM 셀프호스팅이 완성되면 최종 제거 예정
- Go 1.26+ (부트스트랩 빌드용)

## 언어 선택 규칙 (절대 준수)

**Osty로 작성 가능한 것은 Go로 작성 금지. Osty 우선.**

- 새 로직/정책/알고리즘은 기본적으로 `toolchain/*.osty`에 작성하고 Go bridge로 노출
- Go는 호스트 경계에서만 허용:
  - JSON-RPC / stdio / 파일시스템 / 프로세스 / 네트워크 I/O
  - `cmd/*` 진입점과 CLI 플래그 파싱
  - Osty가 아직 못 하는 것을 임시로 채우는 shim (제거 대상으로 표시)
  - Osty 자체를 부트스트랩하는 self-host seed 경로
- "Go로 짜면 금방 되는데" 는 선택 근거가 되지 않음. Osty에 기능이 빠져 있다면 Osty에 그 기능을 추가하는 것이 우선 경로
- 기존 Go 로직을 수정할 때: 그 파일이 `toolchain/*.osty`로 옮길 수 있는지 먼저 점검. 이동이 합당하면 이동하고, 당장 못 옮기면 TODO로 기록
- 예외를 쓰려면 CLAUDE.md가 허용하는 호스트 경계 범주임을 근거로 명시할 것

## 필독 문서

코드 작성 전 반드시 읽을 것:
- `README.md` — 현재 구현 상태 표, CLI 레퍼런스
- `ARCHITECTURE.md` — 파이프라인, 패키지별 책임, 에러 복구 전략
- `LANG_SPEC_v0.5/` — 언어 시맨틱 (프로즈 + 예제)
- `OSTY_GRAMMAR_v0.5.md` — EBNF 문법 + decision log. 스펙과 구현이 충돌하면 **스펙이 기준**
- `SPEC_GAPS.md` — 해결된 갭 아카이브 (v0.4는 open item 0개)
- `ERROR_CODES.md` — 진단 카탈로그 (생성물, `internal/diag/codes.go`에서 자동 생성)
- `RUNTIME_GC.md` — 런타임/GC 구현 경로

## 파이프라인 (절대 순서)

```
source → lexer → parser → resolve → check → (format / lint / ir / backend)
```

각 단계는 `[]*diag.Diagnostic`에 누적. CLI가 마지막에 렌더. 단계 건너뛰기 금지.

## 디렉토리 규칙

- `internal/token` — 토큰 종류 + 위치. 로직 없음, 순수 데이터
- `internal/lexer` — UTF-8 → 토큰. ASI, triple-quoted, interpolation 처리
- `internal/ast` — 모든 노드가 `ast.Node` 인터페이스 구현 (`Pos()`, `End()`)
- `internal/parser` — recursive-descent + Pratt. 에러 복구 (`syncStmt`, `syncDecl`)
- `internal/diag` — `Diagnostic{Severity, Code, Message, Spans, Notes, Hint}` + 렌더러
- `internal/resolve` — 2-pass 네임 리졸루션 (`declarePass` → `bodyPass`, 워크스페이스 전역으로 확산). `File` / `Package` / `Workspace` 는 scope 단위이지 pass 가 아님
- `internal/types` — 시맨틱 타입. 순수 데이터, 로직은 `compat.go`만
- `internal/check` — 2-pass 타입체커 (collect → expr/stmt)
- `internal/stdlib` — prelude + `modules/*.osty` + `primitives/`
- `internal/format` — reparse → print → reparse (idempotent)
- `internal/lint` — `Lxxxx` 경고. 컴파일 차단 안 함
- `internal/ir` — 백엔드-독립 IR
- `internal/backend`, `internal/llvmgen` — LLVM 백엔드 (공개 경로)
- `internal/bootstrap/gen` — 부트스트랩 전용 Osty→Go 트랜스파일러. 공개 CLI에서 접근 불가. **새 작업 금지**. `cmd/osty-bootstrap-gen`만이 호출하며, `internal/selfhost/generated.go` 재생성 전용
- `internal/lsp` — stdio JSON-RPC LSP
- `internal/manifest`, `internal/lockfile`, `internal/pkgmgr`, `internal/registry` — 패키지 매니저
- `cmd/osty` — 메인 CLI. 서브커맨드는 `README.md` 참조
- `cmd/osty-bootstrap-gen` — 개발자 전용 시드 트랜스파일러. 공개 도구 아님
- `toolchain/*.osty` — Osty로 작성된 컴파일러/툴체인 코어. Go 측에서 bridge로 호출

## 진단 규칙 (Exxxx/Wxxxx/Lxxxx — 절대 준수)

- **모든 유저 대면 에러는 stable code**를 가져야 함. `internal/diag/codes.go`의 `CodeXxx` 상수
- 새 에러 사이트 추가 시: 코드 추가 + doc 코멘트 작성 + `go generate ./internal/diag/...` + 그 코드를 assert하는 테스트
- 코드 네임스페이스:
  - `E0001–E0099` 렉시컬
  - `E0100–E0199` 선언/문
  - `E0200–E0299` 식
  - `E0300–E0399` 타입/패턴
  - `E0400–E0499` 어노테이션
  - `E0500–E0599` 네임 리졸루션
  - `E0600–E0699` 컨트롤 플로우
  - `E0700–E0799` 타입체크
  - `E2000–E2099` 매니페스트/스캐폴딩
  - `L0001–L0099` 린트 경고
- 생성 방식: `diag.New(Severity, msg).Code(...).Primary(...).Hint(...).Build()`
- `ERROR_CODES.md`는 생성물. 수동 편집 금지
- 진단 포맷 변경 시 golden snapshot 업데이트: `go test ./internal/diag/ -run TestGolden -update`

## 코드 스타일 (Go)

- `gofmt` 표준. 들여쓰기 탭, 탭 크기 8
- 패키지명은 단수형, 짧고 소문자
- 익스포트된 식별자에 doc 코멘트
- `panic` 금지 — **프로그래머 에러 경로만 예외** (nil map, impossible enum case). 유저 입력으로 panic 나면 즉시 버그
- 에러는 diagnostic으로. `fmt.Errorf`로 유저 메시지 만들어서 삼키지 말 것
- `any`/`interface{}` 최소화 — `ast.Node` 덕분에 resolver의 `Symbol.Decl`도 타입 박혀 있음. 새 코드에서 `any`로 퇴행 금지

## 네이밍

- 에러 코드 상수: `CodeXxx` (예: `CodeUnterminatedString`)
- AST 노드: `PascalCase` 타입, 서브타입도 `ast.Node` 구현
- 테스트: `TestName`, 스펙 코퍼스는 `TestSpec*`, 퍼즈는 `FuzzLex`/`FuzzParse`
- Osty 소스 파일: `snake_case.osty` (예: `semver_parse_test.osty`)
- CLI 서브커맨드는 한 단어 kebab 허용 (`osty registry serve`)

## 테스트 규칙

- 모든 새 에러 코드는 포커스 테스트 1개 이상. `expectCode(t, src, diag.CodeXxx)` 헬퍼 사용
- 퍼즈 크래시는 해당 코퍼스에 시드 추가 (`testdata/fuzz/...`)
- 스펙 코퍼스 (드라이버: `internal/speccorpus`, `just spec` / `just front`에 포함):
  - `testdata/spec/positive/NN-<chapter>.osty` — 파서 0 error 보장. 파일 추가는 디렉토리에 드롭하면 자동 발견. 현재 스펙과 어긋나는 파일은 `positiveWaivers` 맵에 코드 목록 + 이유 주석 등록
  - `testdata/spec/negative/reject.osty` — `// === CASE: Exxxx ===` 블록별로 풀 파이프라인 실행 후 해당 코드 발화 검증. 새 CASE 블록은 추가만 하면 자동 참여. 현재 어긋나는 케이스는 `negativeWaivers`에 `"Exxxx/<hint>"` 키로 등록
  - waiver는 갭 트래킹 용도. 컴파일러가 올바른 코드를 발화하기 시작하면 해당 waiver 엔트리는 테스트 실패와 함께 제거 요청
- 골든 스냅샷: `go test ./internal/diag/ -run TestGolden -update` 후 diff 확인
- 일상 루프는 `justfile` **우선**. 직접 `go test`/`go build` 호출은 특수 상황만.
  - `just front` — 프론트엔드 패키지만 (수 초, 스펙 코퍼스 포함)
  - `just spec` — 스펙 코퍼스만 verbose 출력
  - `just short` — `-short` 플래그로 러닝-헤비 제외
  - `just full` — 전체 `./...` (push 전 선택 검증)
  - `just gen <TestName>` / `just lsp <TestName>` / `just diag <TestName>` / `just cmd <TestName>`
  - `just pipe <path>` / `just pipe-gen <path>` — 파이프라인 타이밍 / 코드젠
  - `just profile <target>` — `.profiles/{cpu,mem}.pprof` 생성
  - `just watch-front` / `just watch-short` / `just watch-pipe <target>` — 저장 시 자동 재실행 (watchexec 필요)
  - `just sum` — gotestsum 설치 시 색상/요약 테스트 출력
  - `just prepush` — `fmt-check` + `vet` + `repair-check` + `ci` 게이트

## 로컬 개발 도구

신규 dev 환경 부트스트랩 (한 번만):

```sh
just build-all                                              # .bin/osty + .osty/bin/osty-native-checker
go install gotest.tools/gotestsum@latest                    # 테스트 출력
go install github.com/go-delve/delve/cmd/dlv@latest         # 디버거
winget install --id Casey.Just --scope user                 # just (없으면)
winget install --id LLVM.LLVM                              # clang/lld/llc (머신 스코프; 최종 링크 단계)
# watchexec: winget에 없음. https://github.com/watchexec/watchexec/releases 에서
#           x86_64-pc-windows-msvc.zip 받아 $GOPATH/bin 또는 PATH에 배치
```

- `osty-native-checker`는 CLI가 `.osty/toolchain/<ver>/` 밑에서 자동 관리함
- `OSTY_NATIVE_CHECKER_BIN`은 **디버그/override 전용**. 글로벌 셸 프로파일에 고정하지 말 것 (워크트리 간 stale 참조 위험)
- LLVM은 `osty build --backend llvm` / `osty run`의 최종 링크 단계에서만 필요. 프론트엔드/체커만 만지면 없어도 됨

## v0.5 baseline 규칙

- 새 구문/키워드 추가는 `LANG_SPEC_v0.5/` 개정 없이는 **금지**.
  정식 버전 업(minor/major)과 함께만 surface 변경.
- `LANG_SPEC_v0.5/`와 `OSTY_GRAMMAR_v0.5.md`가 권위. 스펙과 구현
  충돌 시 스펙이 기준.
- 문법 모호성 발견 → 컴파일러가 아니라 `SPEC_GAPS.md`에 먼저 기록
- 공개 백엔드는 `--backend llvm`만. 새 백엔드 플래그 추가 금지

## 백엔드 작업 규칙

- `internal/backend` / `internal/llvmgen`에만 추가
- 아직 지원 안 되는 소스 shape는 skeleton artifact + 구조화된 diagnostic 경로 유지
- 런타임 ABI는 `internal/backend/runtime/osty_runtime.c`. 심볼 이름은 `osty.gc.*` 네임스페이스
- Phase 번호가 붙은 작업(54-63, 64-73 등)은 `LLVM_MIGRATION_PLAN.md` 참조

## LSP / 툴체인 셀프호스트

- 순수 에디터 정책(UTF-16 변환, 시맨틱 토큰 분류, completion 정렬 등)은 `toolchain/lsp.osty`에 작성. Go 측은 JSON-RPC + AST 트래버설만
- 같은 원칙이 checker/formatter/lint/ci에도 적용: 정책은 `toolchain/*.osty`, 호스트 glue는 Go

## 하지 말 것

- Osty로 작성 가능한 로직을 Go로 새로 작성
- `internal/bootstrap/gen`(부트스트랩 전용 Go 트랜스파일러)에 새 기능 추가 또는 공개 CLI에 재노출
- `panic`으로 유저 입력 에러 처리
- `ERROR_CODES.md`, `payload-types.ts` 같은 생성 파일 수동 편집
- 외부 Go 의존성 추가 (`go.mod` 최소 상태 유지)
- 네이티브 백엔드 우회하는 새 Go 코드 생성 경로
- 스펙에 없는 구문/타입 추가
- `console`/`fmt.Println` 디버그 출력 커밋 (diagnostic 또는 `--trace` 사용)
- 문서 경로만 바뀌고 내용이 무효화된 상태로 커밋 (README 상태 표와 CLI 레퍼런스 동기화)

## 커밋 메시지

```
feat: closure parameter pattern 지원
fix: E0500 리졸버 typo suggestion 거리 계산 오류
chore: golang.org/x/sys v0.43 업데이트
docs: ERROR_CODES 재생성
refactor: legacy gen 경로 제거
```

prefix는 `feat` / `fix` / `chore` / `docs` / `refactor` / `test` / `perf` 중 하나.

---

# 부록 A. 언어 핵심 문법·의미론 + canonical 예시 (v0.4)

> **이 부록은 에이전트가 실제로 Osty 코드를 작성할 때 참조하는 작업 레퍼런스다.** 모든 권위는 `LANG_SPEC_v0.5/`와 `OSTY_GRAMMAR_v0.5.md`에 있으나, 아래 예시들은 Osty다운 스타일의 baseline — 그대로 따라 쓸 수 있는 기준이다. 예시 코드는 스펙 내에서 확실한 구문만 사용한다.

## A.1 프로그램 구조 / 렉시컬

- 확장자 `.osty`, UTF-8. **디렉토리 = 패키지**
- Shebang `#!/usr/bin/env osty`는 렉서가 무시
- 주석: `//` 라인, `/* */` 블록, `///` 문서(다음 선언에 귀속)
- **ASI** (newline = 문 종결자) 억제 규칙:
  - 직전 토큰이 이항 연산자, `,`, `->`, `<-`, `::`, `@`, `|`, 여는 괄호
  - 다음 토큰이 `)`, `]`, `}`, `.`, `?.`, `,`, `..`, `..=`, 이항 연산자
  - 메서드 체인의 `.`은 **선행 위치**만 (후행 `.`는 문법 에러)
  - `} else`는 반드시 같은 줄
- 숫자: `1_000_000`, `0xFF_FF`, `0b1010`, `0o777` — 접미사 없음
- 문자열: `"..."`, `"""..."""` (공통 인덴트 제거), `r"..."` raw, `b"..."` bytes, `b'A'` byte
- 보간 `"{expr}"`, 이스케이프 `\{` / `\}`

```osty
#!/usr/bin/env osty
use std.fs

/// 사용자 리포트를 문자열로 렌더한다.
pub fn renderReport(user: User) -> String {
    let lines = [
        "user: {user.name}",
        "age:  {user.age}",
    ]
    let body = """
        {lines.join("\n")}
        generated: {now()}
        """
    let hint = r"C:\Users\{user.name}\report.txt"
    body + "\npath: " + hint
}
```

## A.2 선언

- `pub`로 export. enum variant 가시성은 enum 가시성 상속(초과 불가)
- `let x` 불변, `let mut x` 가변
- struct/enum은 **부분 선언** 가능 (필드는 한 곳, 메서드는 분산)
- 기본 인자는 리터럴만, 후행 연속. 키워드 호출 `f(x, timeout: 60)`
- receiver: `self` 공유, `mut self` 가변 접근

```osty
pub struct User {
    pub name: String,
    pub age: Int,
    email: String?,

    pub fn greet(self) -> String {
        "hi, {self.name}"
    }

    pub fn olderBy(self, years: Int = 1) -> User {
        User { ..self, age: self.age + years }
    }

    fn setEmail(mut self, e: String) {
        self.email = Some(e)
    }
}

pub enum Event {
    Click(Int, Int),
    Key(String),
    Close,

    pub fn label(self) -> String {
        match self {
            Event.Click(_, _) -> "click",
            Event.Key(k)      -> "key:{k}",
            Event.Close       -> "close",
        }
    }
}

pub interface Reader {
    fn read(self, buf: Bytes) -> Result<Int, Error>
    fn close(self) -> Result<(), Error> { Ok(()) }   // 기본 구현
}
```

## A.3 타입 시스템

- 구조적 인터페이스. 수치 암묵 변환 없음
- 양방향 타입 추론(synth↔check, 국소 단일화)
- 숫자 리터럴 다형성 — 문맥이 타입 결정
- 제네릭 monomorphization, 제약 `<T: Ordered + Hashable>`
- Turbofish `::<T>`로 타입 인자 명시
- 제네릭 메서드/함수는 값으로 참조 금지 — wrapper closure 필요 (G14)
- 함수값으로 저장하면 default/keyword 메타데이터 **소거** (G15, positional/exact arity)

```osty
fn first<T>(xs: List<T>) -> T? {
    if xs.isEmpty() { None } else { Some(xs[0]) }
}

fn maxOf<T: Ordered>(a: T, b: T) -> T {
    if a.gt(b) { a } else { b }
}

// turbofish — 추론 실패/명시 필요 시
let cfg = json.parse::<Config>(text)?

// 리터럴 다형성
let pi: Float64 = 3
let buf: List<Int> = [1, 2, 3]

// 함수값 arity erasure — positional/exact
fn connect(host: String, port: Int = 80) -> Result<Conn, Error> { ... }
let f: fn(String, Int) -> Result<Conn, Error> = connect
f("api.com", 443)     // OK
// f("api.com")        // ERROR: 인자 수 부족 (함수값은 키워드/기본 불가)
```

## A.4 식 / 제어 흐름

- if / match / 블록이 **식** (값 반환)
- `if let ...`, `for let ...` 단축
- `for x in iterable { ... }` — `Iterable<T>` 프로토콜
- `defer` LIFO, `?` 전파·취소에도 실행

```osty
let label = if user.age >= 18 { "adult" } else { "minor" }

for user in users.filter(|u| u.active) {
    notify(user)
}

for let Some(job) = queue.pop() {
    process(job)
}

fn copyFile(src: String, dst: String) -> Result<(), Error> {
    let from = fs.open(src)?
    defer from.close()

    let to = fs.create(dst)?
    defer to.close()

    // 어느 ?에서 실패해도 두 파일 모두 close
    let buf = from.readAll()?
    to.writeAll(buf)?
    Ok(())
}
```

## A.5 패턴 매칭

- 와일드카드 `_`, 리터럴, 식별자(바인딩), 튜플 `(a, b)`
- struct `User { name, age }` / `User { name, .. }` / `User { name: n }`
- variant `Some(x)`, `Rect(w, h)`
- 범위 `0..=9`, `10..20`, `..=0`, `100..`
- or `A | B | C` (모든 대안이 같은 바인딩)
- binding `name @ pattern`
- guard `pat if cond ->` (coverage 기여 안 함)
- `let` LHS, 클로저 파라미터, `for` 변수는 **irrefutable** 패턴만

```osty
fn describe(event: Event) -> String {
    match event {
        Event.Click(x, y) if x >= 0 && y >= 0 -> "hit at ({x}, {y})",
        Event.Click(_, _)                     -> "out of bounds",
        Event.Key("q") | Event.Key("esc")     -> "quit",
        Event.Key(k)                          -> "key:{k}",
        Event.Close                           -> "closed",
    }
}

fn bucket(n: Int) -> String {
    match n {
        0            -> "zero",
        x @ 1..=9    -> "digit {x}",
        x @ 10..=99  -> "two digits {x}",
        x if x < 0   -> "negative {x}",
        _            -> "large",
    }
}

// destructuring (irrefutable)
let User { name, age, .. } = loadCurrentUser()?
let (lo, hi) = range.bounds()
```

## A.6 에러 처리

- `expr?` — Result/Option 전파 (혼용 금지)
- `expr?.field` — Option 단락 연쇄
- `expr ?? default` — Option fallback (우결합, 우측 lazy)
- `Error` 인터페이스 + `err.downcast::<T>()` 복구
- `panic`/`unreachable`/`todo`/`abort`는 defer **skip**

```osty
fn loadConfig(path: String) -> Result<Config, Error> {
    let text = fs.readToString(path)?
    let cfg: Config = json.parse(text)?
    Ok(cfg)
}

fn cityOf(user: User?) -> String {
    user?.address?.city ?? "unknown"
}

fn handle(err: Error) -> Result<(), Error> {
    match err.downcast::<FsError>() {
        Some(FsError.NotFound(p)) -> {
            log.warn("missing file: {p}")
            Ok(())
        },
        Some(other) -> Err(other),
        None        -> Err(err),
    }
}

fn withLock<T>(m: Mutex, body: fn() -> Result<T, Error>) -> Result<T, Error> {
    m.lock()?
    defer m.unlock()
    body()
}
```

## A.7 동시성 (구조적)

- `taskGroup(|g| { ... })` 스코프 필수
- `Handle<T>` / `TaskGroup`은 **스코프 탈출 금지** (E0743, G13)
- 채널: `thread.chan::<T>(buf)`, `ch <- v`(문), `ch.recv()`, `for x in ch`, `ch.close()`
- select: `recv` / `send` / `timeout` / `default`
- 취소는 cause 포함, defer는 취소 시에도 실행 (uninterruptible)

```osty
fn fetchAll(urls: List<String>) -> Result<List<Bytes>, Error> {
    taskGroup(|g| {
        let handles = urls.map(|u| g.spawn(|| http.get(u)))
        let mut results: List<Bytes> = []
        for h in handles {
            results.push(h.join()?)          // 반드시 스코프 내에서 join
        }
        Ok(results)
    })
}

fn pipeline(jobs: List<Job>) -> Result<(), Error> {
    let ch = thread.chan::<Job>(64)

    taskGroup(|g| {
        // 생산자
        g.spawn(|| {
            for j in jobs { ch <- j }
            ch.close()
        })

        // 소비자 (메인)
        for j in ch {
            process(j)?
        }
        Ok(())
    })
}

// select
let ch = thread.chan::<Int>(4)
let out = thread.select(|s| {
    s.recv(ch,     |x| "value: {x}")
    s.timeout(5.s, || "timeout")
    s.default(     || "empty")
})
```

## A.8 모듈 / 가시성 / FFI

- `use std.fs`, `use pkg as alias`, 순환 import 금지
- `pub` top-level/field/method/variant
- Prelude 자동: `Option/Some/None`, `Result/Ok/Err`, `println`, 기본 타입
- `use go "..." { ... }` — Osty↔Go 타입 매핑: `T?↔*T`, `Result<T,Error>↔(T, error)`, `List<T>↔[]T`

```osty
use std.fs
use std.json
use github.com/x/retry as retry
use go "net/http" {
    fn Get(url: String) -> Result<Response, Error>
    struct Response {
        StatusCode: Int,
        Body: Reader,
    }
}

// 외부 노출 최소화
pub fn fetchJson<T>(url: String) -> Result<T, Error> {
    let resp = Get(url)?
    let body = resp.Body.readAll()?
    json.parse::<T>(body.toString()?)
}

// 모듈 내부용 — pub 없음
fn normalizeUrl(u: String) -> String { ... }
```

## A.9 메타 / 어노테이션

- `#[json(...)]` — struct field / enum variant
- `#[deprecated(...)]` — 모든 선언, W0750 경고
- 값은 **리터럴만** (key=literal 또는 bare flag), 표현식 불가

```osty
pub struct ApiUser {
    #[json(key = "user_id")]
    pub id: Int,
    #[json(key = "full_name")]
    pub name: String,
    #[json(skip)]
    cache: LocalCache,
}

#[deprecated(since = "0.5", use = "loginV2")]
pub fn login(u: String, p: String) -> Result<Session, Error> { ... }
```

## A.10 특수 컴파일러 기능

- Builder 자동 파생 — 필수 `pub` 필드 미설정 시 `.build()` **컴파일 타임 거부** (G9)
- `ToString` 자동 구현 (override 가능)
- `_test.osty` + `fn test_*()` 자동 발견, 기본 병렬 + 난수 순서

```osty
// builder — url 빠지면 컴파일 에러
let req = HttpRequest.builder()
    .url("https://api.example.com/x")
    .method("POST")
    .timeout(60)
    .build()

// _test.osty 파일
use std.testing

fn testParseHandlesBlank() {
    let r = parse("")
    testing.assertEq(r, Err(ParseError.Empty))
}

fn benchJsonDecode() {
    testing.benchmark(1000, || {
        let _: Config = json.parse(sample)?
        Ok(())
    })
}
```

## A.11 명시적 배제 — 요청받아도 거부

`null`/`nil` (→ `Option`), 암묵 수치 변환, 사용자 정의 연산자 오버로딩, 제네릭 타입 매개변수 기본값, variadic generics, 상속, 암묵 인터페이스 구현.

이들을 요구하는 설계는 Osty다운 대안(`Option` 명시, 명시적 변환 함수, 새 함수명, 팩토리 함수, 헬퍼 trait, composition + 구조적 interface)으로 재구성한다.

---

# 부록 B. 생산성 기법 카탈로그 + 전형 패턴

> 각 카테고리는 **색인 테이블 + 선호 사용 패턴**으로 구성. 테이블은 "기법이 있다"의 체크리스트, 스니펫은 "이렇게 쓰는 게 Osty다움"의 기준.

## B.1 문법 설탕

| # | 기법 | 스펙 |
|---|---|---|
| 1 | `T?` = `Option<T>` | §2.5 |
| 2 | 문자열 보간 `"{expr}"` | §1.6.3 |
| 3 | Triple-quoted + 공통 인덴트 제거 | §1.6.3 |
| 4 | Raw `r"..."` | §1.6.3 |
| 5 | 숫자 분리자 `_` | §1.6.1 |
| 6 | 필드 쇼트핸드 `{ name }` | §3.4 |
| 7 | struct 스프레드 `{ ..x, ... }` | §3.4 |
| 8 | ASI | §1.8 |
| 9 | Shebang | §1.1 |
| 10 | 리터럴 다형성 | §2.2 |
| 11 | `///` doc → API 문서 | §1.5 |
| 74 | 컴파운드 대입 `x += y` 등 10종 | §4.13.1 |

**전형 패턴** — 보간 + 쇼트핸드 + 스프레드 묶음:

```osty
let User { name, age, .. } = current
let summary = "user {name} ({age})"
let nextYear = User { ..current, age: age + 1 }
```

**전형 패턴** — 커서 전진/필드 뮤테이션에는 compound assignment:

```osty
struct Cursor {
    pos: Int,

    fn advance(mut self) {
        self.pos += 1          // self.pos = self.pos + 1 장황함 제거
    }
}

fn tally(tokens: List<String>) -> Int {
    let mut n = 0
    for _ in tokens { n += 1 }
    n
}

// 10종: += -= *= /= %= &= |= ^= <<= >>=
// 인덱스 타깃(xs[i] += v)은 아직 코드젠에서 미지원 — 평문 형태로 작성
```

## B.2 타입 시스템

| # | 기법 | 비고 |
|---|---|---|
| 12 | 제네릭 + monomorphization | `fn<T: Ordered>` |
| 13 | Turbofish `::<T>` | 추론 실패 시 명시 |
| 14 | 구조적 인터페이스 | impl 블록 불필요 |
| 15 | 인터페이스 기본 메서드 | default body |
| 16 | 내장 Equal/Ordered/Hashable/ToString | 자동 파생 조건부 |
| 17 | 컬렉션 자동 derive | `List<T: Hashable>: Hashable` |
| 18 | 양방향 타입 추론 | 국소 단일화 |
| 19 | Union/Sum enum + 메서드 | payload 지원 |
| 20 | Prelude Option/Result | 임포트 불필요 |
| 21 | struct/enum 부분 선언 | 같은 패키지 다중 파일 |
| 22 | `type` 별칭 | |

**전형 패턴** — 제네릭 + 구조적 인터페이스:

```osty
pub interface Cache<K, V> {
    fn get(self, key: K) -> V?
    fn set(mut self, key: K, value: V)
}

fn memoize<K: Hashable, V>(cache: Cache<K, V>, k: K, compute: fn() -> V) -> V {
    match cache.get(k) {
        Some(v) -> v,
        None    -> {
            let v = compute()
            cache.set(k, v)
            v
        },
    }
}
```

## B.3 에러 처리

| # | 기법 | 비고 |
|---|---|---|
| 23 | `?` 전파 | Result / Option 각각 |
| 24 | `?.` optional chain | 첫 None 단락 |
| 25 | `??` nil-coalesce | 우결합, lazy 기본값 |
| 26 | `Error` + downcast | nominal tag 복구 |
| 27 | `defer` LIFO | `?`·취소에도 실행 |
| 28 | `panic`/`unreachable`/`todo`/`abort` | defer skip |

**전형 패턴** — `?` 체인 + defer 페어링:

```osty
fn compressFile(src: String, dst: String) -> Result<(), Error> {
    let r = fs.open(src)?
    defer r.close()
    let w = fs.create(dst)?
    defer w.close()
    let enc = gzip.writer(w)?
    defer enc.close()

    io.copy(r, enc)?
    Ok(())
}

let title: String = user?.profile?.title ?? "Untitled"
```

## B.4 제어 흐름 / 패턴

| # | 기법 | 비고 |
|---|---|---|
| 29 | `if let Some(x) = e` | 단일 분기 |
| 30 | `for let Some(x) = q.pop()` | Some/Ok 동안 반복 |
| 31 | `match` 식 | 값 반환 |
| 32 | Exhaustiveness + witness | 누락 지점 명시 (G17) |
| 33 | Guard `if cond ->` | coverage 기여 안 함 |
| 34 | 범위 패턴 `0..=9` | 생략형 `..=N`, `N..` |
| 35 | struct destructuring | rename `name: n` |
| 36 | or-pattern `A \| B` | 같은 바인딩 |
| 37 | binding `name @ pat` | 부분+전체 동시 |
| 38 | 블록 = 식 | 마지막 식이 값 |

**전형 패턴** — match 식으로 분기 + guard:

```osty
fn tierOf(user: User) -> Tier {
    match user.plan {
        Plan.Free                              -> Tier.Basic,
        Plan.Paid(n) if n >= 10                -> Tier.Enterprise,
        Plan.Paid(_)                           -> Tier.Standard,
        Plan.Trial(expires) if expires > now() -> Tier.Standard,
        Plan.Trial(_)                          -> Tier.Basic,
    }
}
```

## B.5 함수 / 클로저

| # | 기법 | 비고 |
|---|---|---|
| 39 | 기본 인자 (리터럴) | 후행 연속 |
| 40 | 키워드 인자 | `f(x, timeout: 60)` |
| 41 | 클로저 `\|x\| ...` | 참조 캡처 |
| 42 | 클로저 패턴 파라미터 | `\|(k, v)\| ...` (G16) |
| 43 | 메서드 문법 self/mut self | 점 호출 |
| 44 | 메서드 참조 (비제네릭) | `let f = obj.m` |
| 45 | 고차 함수 타입 | `fn(T) -> R` |
| 46 | 함수값 arity erasure (G15) | positional/exact |
| 47 | Unit 반환 shorthand | `fn()` |

**전형 패턴** — 기본 인자 + 클로저 패턴 파라미터:

```osty
pub fn fetch(url: String, timeout: Int = 30, retries: Int = 3) -> Result<Bytes, Error> {
    ...
}

// 호출자
fetch("https://api/x")
fetch("https://api/x", timeout: 60)

// 클로저 패턴 파라미터 — 튜플/struct 분해
counts.entries()
    .filter(|(_, v)| v > 0)
    .map(|(k, v)| "{k} = {v}")
```

## B.6 동시성

| # | 기법 | 비고 |
|---|---|---|
| 48 | `taskGroup` 스코프 | 구조적 |
| 49 | Non-escaping capability | E0743 (G13) |
| 50 | `parallel(items, n, f)` | bounded 병렬 |
| 51 | 채널 `thread.chan::<T>(n)` | `<-`, `recv()` |
| 52 | `for x in channel` | close 시 종료 |
| 53 | `thread.select` | recv/send/timeout/default |
| 54 | 취소 전파 (cause) | `isCancelled`/`checkCancelled` |
| 55 | Defer × 취소 | uninterruptible |

**전형 패턴** — 구조적 동시성 (자식 실패 → 형제 자동 취소):

```osty
fn mirrorDirs(srcs: List<String>, dst: String) -> Result<(), Error> {
    taskGroup(|g| {
        for src in srcs {
            g.spawn(|| syncDir(src, dst))
        }
        // 종료 시점에 모든 자식 join. 하나라도 실패하면 나머지 자동 취소.
        Ok(())
    })
}

// 함정: Handle을 스코프 밖으로 반환하면 E0743
// fn bad() -> Handle<Int> {
//     taskGroup(|g| { g.spawn(|| 1) })   // 타입 에러
// }
```

## B.7 모듈 / 가시성

| # | 기법 | 비고 |
|---|---|---|
| 56 | 디렉토리 = 패키지 | 암묵 |
| 57 | `use … as alias` | rename |
| 58 | `pub` 가시성 | field/method/variant |
| 59 | 순환 import 금지 | DAG |
| 60 | Prelude | 자동 심볼 |

**전형 패턴** — export 최소화, 내부 헬퍼는 비-pub:

```osty
pub fn parse(text: String) -> Result<Config, Error> {
    let toks = tokenize(text)?
    parseTokens(toks)
}

// 내부 전용 — pub 없음
fn tokenize(text: String) -> Result<List<Token>, Error> { ... }
fn parseTokens(tokens: List<Token>) -> Result<Config, Error> { ... }
```

## B.8 메타 / 어노테이션

| # | 기법 | 비고 |
|---|---|---|
| 61 | `#[json(...)]` | struct field / variant |
| 62 | `#[deprecated(...)]` | W0750 |
| 63 | 값 = 리터럴 전용 | 검증 용이 |

**전형 패턴** — 직렬화 키 매핑 + 내부 필드 숨김:

```osty
pub struct ApiUser {
    #[json(key = "user_id")]
    pub id: Int,
    #[json(key = "full_name")]
    pub name: String,
    #[json(skip)]
    internal: CacheHandle,
}
```

## B.9 FFI

| # | 기법 | 비고 |
|---|---|---|
| 64 | `use go "..." { ... }` | Go 패키지 |
| 65 | 타입 매핑 | `T?↔*T`, `Result↔(T,error)` |

**전형 패턴** — Go 타입을 Osty 경계로 격리:

```osty
use go "net/http" {
    fn Get(url: String) -> Result<Response, Error>
    struct Response { StatusCode: Int, Body: Reader }
}

// Go 타입이 외부로 새지 않도록 pub API는 Osty 타입만
pub fn fetchBytes(url: String) -> Result<Bytes, Error> {
    let resp = Get(url)?
    if resp.StatusCode != 200 {
        return Err(HttpError.Status(resp.StatusCode))
    }
    resp.Body.readAll()
}
```

## B.9.1 컬렉션 helper 전형 패턴

`Map<K, V>`는 intrinsic (`insert/get/remove/keys/len`) + pure helper (spec §10.6)로 구성. **`get → ?? → insert` 3-call 패턴은 금지** — `update`로 한 번에.

| # | helper | 대체되는 안티패턴 |
|---|---|---|
| 64.1 | `map.update(k, \|n\| (n ?? 0) + 1)` | `let n = map.get(k) ?? 0; map.insert(k, n + 1)` |
| 64.2 | `map.getOr(k, d)` | `map.get(k) ?? d` (원한다면 유지 가능; `getOr`는 의도 명시) |
| 64.3 | `a.mergeWith(b, \|x, y\| x + y)` | `for (k, v) in b { a.update(k, \|n\| (n ?? 0) + v) }` (단, 원본 보존 필요 시 mergeWith) |
| 64.4 | `xs.groupBy(\|x\| key(x))` | `for x in xs { let k = key(x); let bucket = m.get(k) ?? []; bucket.push(x); m.insert(k, bucket) }` |
| 64.5 | `map.mapValues(\|v\| f(v))` | `for (k, v) in m { out.insert(k, f(v)) }` |
| 64.6 | `map.retainIf(\|_k, v\| pred(v))` | `for (k, v) in m.entries() { if !pred(v) { m.remove(k) } }` (iter-during-mutation 위험) |

**전형 패턴** — 단어 빈도 집계 (canonical 레퍼런스: [word_freq.osty](word_freq.osty)):

```osty
fn tally(tokens: List<String>) -> Map<String, Int> {
    let mut counts: Map<String, Int> = {:}
    for t in tokens {
        counts.update(t, |n| (n ?? 0) + 1)
    }
    counts
}

fn mergeReports(reports: List<Map<String, Int>>) -> Map<String, Int> {
    let mut merged: Map<String, Int> = {:}
    for r in reports {
        merged = merged.mergeWith(r, |a, b| a + b)
    }
    merged
}
```

**2-arg 클로저 경고:** `counts.any(|k, v| k == "a")` 는 L0002 경고 (`v` unused). 사용 안 하는 위치는 `_k` / `_v` 명시 — spec positive 코퍼스는 이 규칙 지킴.

**백엔드 한계:** Map의 pure helper들은 stdlib에 Osty 본문으로 정의되지만, 현재 LLVM 백엔드는 intrinsic (`insert/get/remove/keys/len`) 외 bodied 메서드 lowering을 지원하지 않음 (LLVM015). 파싱/리졸브/체크까진 통과하지만 `osty run`은 실패. 백엔드가 catch up할 때까지 호출부는 intrinsic만 쓰는 경로로 fallback 가능하다는 점 명심.

## B.10 특수 / 테스트

| # | 기법 | 비고 |
|---|---|---|
| 66 | Builder 자동 파생 | 필수 필드 컴파일 타임 검증 (G9) |
| 67 | `value.toBuilder()` | 기존 값 변형 |
| 68 | `ToString` 자동 구현 | override 가능 |
| 69 | `_test.osty` + `test_*` 자동 발견 | 병렬 + 난수 |
| 70 | `testing.assertEq` | 구조적 diff |
| 71 | `testing.benchmark` | 반복 측정 |
| 72 | `testing.snapshot` | golden file |
| 73 | `dbg(expr)` | 위치 + 식 텍스트 + 값 |

**전형 패턴** — builder 기반 객체 생성 + 포커스 테스트:

```osty
fn testUserBuilderDefaults() {
    let u = User.builder()
        .name("alice")
        .age(30)
        .build()
    testing.assertEq(u.name, "alice")
    testing.assertEq(u.age, 30)
}

fn benchParseConfig() {
    testing.benchmark(1000, || {
        let _: Config = json.parse(sampleText)?
        Ok(())
    })
}
```

---

## 이 부록들의 사용 규칙

- 새 Osty 코드·예시·진단 메시지 작성 **전에** 부록 A의 해당 소섹션을 확인하고 예시 스타일을 따른다.
- 새 기능·린트 추가 전에 부록 B에 이미 있는 기법과 중복되는지 검사.
- `LANG_SPEC_v0.5/`가 확장되면 같은 커밋에서 부록 A/B도 갱신.
- 부록 예시는 반드시 스펙 내 확실한 문법만. 불확실하면 서술로 대체하거나 생략.
- 부록 A에 없는 구문을 예시·코드에 쓰려 한다면 먼저 스펙을 확인하고, 있다면 부록 A에 추가.
