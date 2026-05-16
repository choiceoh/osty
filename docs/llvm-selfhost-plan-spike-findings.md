# LLVM self-host plan — Q1–Q5 spike findings

> **상태**: 측정 (이 PR).
> **선행**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md) §10.2 open questions.
> **소유**: backend / toolchain.

## 목적

본 plan §11 step 2 (Spike) 의 5개 open question 을 코드/audit 으로 답한다. 결과는 plan 본문 § 3.1 / §3.3 / §10.2 에 반영.

## 측정 환경

- 기준일: 2026-05-16
- 브랜치: `origin/main` (PR0 머지 후 fmt fix 포함)
- audit 명령: `OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/`

## Q1 — `std.json.parseValue` 의 stage0 surface 안에 머무는가?

**답: YES.**

stage0 audit 전체 결과:

| 메트릭 | 값 | 출처 |
|---|---|---|
| toolchain 함수 cover | **99.8% (8221 / 8237)** | audit `:198` |
| decline 함수 수 | **16** | audit `:259` |

5일 전 측정 (`stage0_p24_scope.md`, 2026-05-11) 의 94.2% (6112/6489) 대비 **+5.6%p / +2109 함수 cover**. 그 사이 P25/P26 등 stage0 unlock PR 다수가 머지된 것으로 추정 (stage0 trajectory 진척이 매우 빠름).

`std.json.{parseValue, stringifyValue, parse, encode, stringify, parseStr, parseNum, parseArr, parseObj, parseLiteral, parseHex4, encodeUtf8}` 12개 함수 중 audit decline list 에 등장한 것: **0개**. 모두 stage0 cover.

**plan 영향**: §10.2 Q1 → "YES, std.json 은 stage0 cover". R4 (parseValue stage0 surface 밖 위험) → **위험 종결**.

## Q2 — `osty build cmd/<dir>` 가 single-binary multi-package 빌드 가능한가?

**답: YES, 단 `osty.toml` + `[bin]` 섹션 추가 필요.**

`osty build [DIR]` 은 manifest-driven (`cmd/osty/main.go:183` "build is the manifest-driven project pipeline"). DIR 안의 `osty.toml` 을 walks up.

`toolchain/osty.toml` 이 정확한 모델:

```toml
[package]
name = "toolchain"
version = "0.1.0"
edition = "0.3"

[bin]
name = "osty-self"
path = "main.osty"

[capabilities]
runtime = true
```

`[bin]` 섹션이 단일 binary 진입점을 선언하면 그 디렉토리의 모든 `.osty` 파일이 transitively 컴파일되어 binary 산출. PR1 에서 `cmd/osty-native-checker/osty.toml` 을 같은 모양으로 추가하면 됨.

**잠재 이슈**: `cmd/osty-native-checker/` 디렉토리에 `main.go` 와 `main.osty` 가 공존하게 된다. `osty build` 가 `.go` 파일을 무시하는지 확인 필요 (스파이크 안에서 확인 안 함; PR1 에서 첫 빌드 시도 시 검증).

**plan 영향**: §10.2 Q2 → "YES, osty.toml + [bin] 추가". R1 (single-binary build 차단) → **위험 종결**.

## Q3 — `toolchain/check.osty` 의 외부 호출 가능한 entry function?

**답: 단일 entry 없음. Go side adapter 가 5단계 Osty 함수 시퀀스를 호출.**

`internal/selfhost/package_adapter.go::CheckPackageStructured` body 의 호출 순서:

```go
file, layout, err := selfhostBuildPackageAst(input.Files)   // Osty
if file == nil { return CheckResult{}, nil }
cx := newElabCx(file, nil)                                   // Osty
selfhostInstallImportSurfaces(cx.env, input.Imports)         // Osty
elabFile(cx)                                                 // Osty (핵심 elaboration)
result = adaptCheckResultWithTokenLayout(serializeCheckResult(cx), layout)
                                          // └ Osty          └ Go (token→byte offset)
```

`CheckSourceStructured` 도 비슷한 5단계 (선두 두 단계만 `ostyLexSource` + `astParseLexedSource` 로 갈음).

**LLVM-built entry point Osty 코드 예시**:

```osty
fn checkRequest(req: CheckRequest) -> CheckResult {
    let (file, layout) = selfhostBuildPackageAst(req.package.files)?
    if file == None { return CheckResult { /* empty */ } }
    let cx = newElabCx(file, None)
    selfhostInstallImportSurfaces(cx.env, req.package.imports)
    elabFile(cx)
    let raw = serializeCheckResult(cx)
    adaptCheckResultWithTokenLayout(raw, layout)
}
```

**새 wall (N8)**: `adaptCheckResultWithTokenLayout` 는 Go side 함수 — token.Pos → byte offset 변환. Osty 측에도 같은 함수 필요 (`toolchain/check.osty` 또는 새 `toolchain/check_token_layout.osty`). 약 ~100 LOC 추정.

**plan 영향**: §3.3 의 "checkPackage 진입점 직접 호출" 표현을 "5단계 Osty 시퀀스 + Go token-layout adapter" 로 정정. §5.2 에 N8 추가. R5 (entry function 부재 위험) → **위험 종결 + N8 작업 항목 추가**.

## Q4 — stdin EOF 처리 — single request vs persistent loop?

**답: single request. 한 번 decode → 종료.**

`cmd/osty-native-checker/main.go:21-38` body:

```go
var req api.CheckRequest
if err := json.NewDecoder(stdin).Decode(&req); err != nil { ... }
checked, err = selfhost.CheckPackageStructured(*req.Package)
return json.NewEncoder(stdout).Encode(checked)
```

`Decode` 가 첫 JSON value 를 읽고 즉시 반환. 두 번째 request 대기 없음. EOF 가 와도 정상 (이미 한 값 읽은 후 종료).

**LLVM-built 동등 패턴**:

```osty
fn main() {
    let text = stdin.readAll()?         // 또는 NewDecoder 같은 streaming
    let req = parseCheckRequest(text)?
    let result = checkRequest(req)
    stdout.write(stringifyCheckResult(result))
}
```

`stdin.readAll()` vs streaming decoder — Go side 는 streaming (`NewDecoder`) 이지만 `Decode` 한 번이므로 사실상 `ReadAll` 후 parse 와 동등. Osty 측에서 더 단순한 readAll 채택.

**plan 영향**: §10.2 Q4 → "single request, readAll 패턴 OK". 변경 없음 (plan 의 기존 가정과 일치).

## Q5 — stderr / exit code 표준?

**답: error → stderr 한 줄 + exit 1. success → stdout JSON + exit 0.**

`main.go:14-18`:

```go
if err := run(os.Stdin, os.Stdout); err != nil {
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
}
```

Behavior parity 에 stderr 는 포함 안 시킴 (§4.1 "stderr 는 비교 안 함"). 단 exit code 는 비교 — LLVM-built 도 같은 error condition 에서 exit 1.

**LLVM-built 동등 패턴**:

```osty
fn main() {
    let result = runChecker()
    match result {
        Ok(()) -> {}
        Err(e) -> {
            stderr.writeLine(e.toString())
            process.exit(1)
        }
    }
}
```

**plan 영향**: §10.2 Q5 → "stderr 한 줄 + exit 1; behavior parity 는 stdout + exit code 만 비교". 변경 없음.

## 요약 — plan §10 위험 + open question 갱신

| 항목 | 이전 상태 | spike 후 |
|---|---|---|
| R1: `osty build` multi-package | 위험 | **종결** — osty.toml + [bin] 모델 (toolchain/osty.toml 그대로) |
| R4: `std.json.parseValue` stage0 surface 밖 | 위험 | **종결** — stage0 99.8%, std.json 전부 cover |
| R5: checkPackage entry function 부재 | 위험 | **부분 종결** — 단일 entry 없지만 5단계 시퀀스 명확. N8 (token-layout adapter Osty 이식, ~100 LOC) 추가 |
| Q1 | unknown | **YES, std.json stage0 cover** |
| Q2 | unknown | **YES, manifest + [bin] 모델** |
| Q3 | unknown | **5단계 Osty 시퀀스 + Go token-layout adapter** |
| Q4 | unknown | **single request, readAll 패턴** |
| Q5 | unknown | **stderr 한 줄 + exit 1, stdout/exit-code 만 parity 비교** |

## stage0 trajectory 진척 메모

본 plan §3.1 의 측정값 (94.2% / 1340) 이 **5일만에 99.8% / 16 decline** 으로 점프. stage0 PR chain (P24, P25, ...) 진척이 plan 작성 시 예상보다 빠름. 본 plan 의 PR3 (L1 byte parity) 시점에 stage0 100% 가 거의 확실 — 본 plan 의 PR 4–N (stage0 gap 깎기) 가 사실상 불필요해질 가능성.

다만 audit cover 와 install-self decline 사이의 monomorph 격차 (`stage0_p24_scope.md §0`) 는 여전히 적용 — 99.8% audit 가 install-self 의 ~99.7% 와 같다는 보장 없음. install-self 측정은 PR1 시점에 한 번 더.

## 다음

1. plan §3.1 측정값 갱신 (94.2% → 99.8%, 1340 → install-self 미측정).
2. plan §3.3 의 "checkPackage 직접 호출" 표현 정정.
3. plan §5.2 에 N8 (token-layout adapter Osty 이식) 추가.
4. plan §10.1 R1/R4/R5 상태 갱신, §10.2 Q1–Q5 답 채움.
5. PR1 진행 (walking skeleton).

plan 갱신은 이 PR 안에서 같이.
