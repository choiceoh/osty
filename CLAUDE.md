# CLAUDE.md — Osty 프로젝트 규칙

## 프로젝트

**Osty**: 정적 타입, GC 기반, 범용 프로그래밍 언어의 **셀프호스팅 컴파일러/툴체인**.
언어 스펙은 v0.4 (grammar frozen), 네이티브 백엔드는 LLVM.

- **셀프호스팅**: 컴파일러 본체(렉서/파서/리졸버/체커/린트/포매터/LLVM 코드젠/LSP 정책)는 전부 **Osty로 작성** (`toolchain/*.osty`)
- Go는 **호스트 경계와 부트스트랩 역할만**: I/O, JSON-RPC, CLI 진입점, Osty→Go 셀프호스트 시드(`internal/selfhost/generated.go`), 얇은 어댑터(`internal/lexer`·`internal/parser` 등은 수십 줄짜리 파사드)
- 외부 Go 의존성: `golang.org/x/sys`, `golang.org/x/term`만 허용 (추가 금지)
- 공개 백엔드: LLVM만 (레거시 Go 트랜스파일러 `internal/gen`은 제거 중)
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
- `LANG_SPEC_v0.4/` — 언어 시맨틱 (프로즈 + 예제)
- `OSTY_GRAMMAR_v0.4.md` — EBNF 문법 + decision log. 스펙과 구현이 충돌하면 **스펙이 기준**
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
- `internal/resolve` — 3-pass 네임 리졸루션 (`File` / `Package` / `Workspace`)
- `internal/types` — 시맨틱 타입. 순수 데이터, 로직은 `compat.go`만
- `internal/check` — 2-pass 타입체커 (collect → expr/stmt)
- `internal/stdlib` — prelude + `modules/*.osty` + `primitives/`
- `internal/format` — reparse → print → reparse (idempotent)
- `internal/lint` — `Lxxxx` 경고. 컴파일 차단 안 함
- `internal/ir` — 백엔드-독립 IR
- `internal/backend`, `internal/llvmgen` — LLVM 백엔드 (공개 경로)
- `internal/gen` — 레거시 Go 트랜스파일러. **새 작업 금지**, 제거 중
- `internal/lsp` — stdio JSON-RPC LSP
- `internal/manifest`, `internal/lockfile`, `internal/pkgmgr`, `internal/registry` — 패키지 매니저
- `cmd/osty` — 메인 CLI. 서브커맨드는 `README.md` 참조
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
- 스펙 코퍼스:
  - `testdata/spec/positive/NN-<chapter>.osty` — 0 diagnostic 보장
  - `testdata/spec/negative/reject.osty` — `// === CASE: Exxxx ===` 블록별로 해당 코드 발화
- 골든 스냅샷: `go test ./internal/diag/ -run TestGolden -update` 후 diff 확인
- 일상 루프는 `justfile`:
  - `just front` — 프론트엔드 패키지만 (수 초)
  - `just short` — `-short` 플래그로 러닝-헤비 제외
  - `just gen <TestName>` / `just lsp <TestName>`
  - `just pipe <path>` — 파이프라인 타이밍

## v0.4 불변 사항

- 그래마 frozen. 새 구문/키워드 추가는 스펙 개정 없이는 **금지**
- `LANG_SPEC_v0.4/`와 `OSTY_GRAMMAR_v0.4.md`가 최종 권위
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
- `internal/gen`(레거시 Go 트랜스파일러)에 새 기능 추가
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
