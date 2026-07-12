# Changelog

## v0.0.1-beta — 2026-04-27

첫 번째 베타 릴리즈입니다.

### 컴파일러 프론트엔드

- **렉서** — UTF-8, ASI, triple-quoted 문자열, 보간, raw/bytes 리터럴
- **파서** — 셀프호스팅 recursive-descent + Pratt, 에러 복구(`syncStmt`, `syncDecl`)
- **AST** — 모든 노드가 `ast.Node` 인터페이스 구현 (`Pos()`, `End()`)
- **진단** — `error[E0xxx]:` 캐럿 렌더, hint, note, 안정적인 에러 코드 카탈로그
- **네임 리졸루션** — 단일/다중 파일, 워크스페이스 전역 확산, typo 제안
- **타입 체커** — 양방향 추론 + 로컬 단일화, 제네릭 인스턴스화, 구조적 인터페이스, exhaustiveness, builder 프로토콜, 함수값 arity, 클로저 패턴 파라미터
- **포매터** — reparse → print → reparse (idempotent)
- **린터** — 28개 코드 (`L0001`–`L0070`), unused / dead-code / naming / simplify / complexity / docs, `--fix` / `--fix-dry-run`

### 백엔드 및 IR

- **독립 IR** — 패턴, 매치, 클로저, struct/필드/메서드, 제네릭 free-fn + struct/enum monomorphization
- **LLVM 백엔드** (`--backend llvm`) — scalar / control-flow / Bool / String, `Result<T,E>` 4가지 shape, `?` 전파, match expression/statement, 인터페이스 vtable 디스패치, list/map/set 리터럴 및 intrinsic, `osty gen` / `osty build` / `osty run`

### 툴링

- **LSP** (`osty lsp`) — hover, definition, formatting, documentSymbol, lint 진단, 에디터 정책
- **프로젝트 스캐폴딩** (`osty new` / `osty init`) — `--bin`, `--lib`, `--workspace`, `--cli`, `--service`
- **매니페스트 + 락파일 + SemVer** — parse / validate / resolve
- **빌드 오케스트레이터** (`osty build`) — manifest → 프론트엔드 → 네이티브 백엔드, profile/target/feature 배선
- **테스트 하네스** (`osty test`) — `test*` 함수 자동 발견, LLVM 백엔드 통과, 병렬 실행, assertion 위치 출력
- **API 문서 생성기** (`osty doc`) — HTML + markdown, 필드 문서, 크로스 레퍼런스
- **CI 품질 툴링** (`osty ci`) — 셀프호스팅 생성 CI 코어, signature-aware 스냅샷
- **파이프라인 시각화** (`osty pipeline`) — 단계별 타이밍, 베이스라인 diff
- **패키지 매니저** (`osty add` / `osty update` / `osty publish` / `osty registry`) — 파일 기반 HTTP 레지스트리, SemVer 리졸버, 결정론적 락파일

### 알려진 한계

- Map의 `update` / `mergeWith` / `groupBy` 등 bodied helper는 LLVM 백엔드 lowering 미지원 (LLVM015)
- `err.downcast::<T>()` 는 프론트엔드 체커에서 미지원 (백엔드 전용)
- `osty new` / `osty init` 생성 파일은 아직 edition `0.4` 고정
- `testing.benchmark` / `testing.snapshot` / ToString 구조적 diff 미구현
