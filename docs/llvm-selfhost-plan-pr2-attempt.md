# LLVM self-host plan — PR2 attempt (std.json backend wall)

> **상태**: 측정 + revert (이 PR).
> **선행**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md), [docs/llvm-selfhost-plan-pr1c-1-attempt.md](llvm-selfhost-plan-pr1c-1-attempt.md), PR [#1826](https://github.com/choiceoh/osty/pull/1826) (옵션 1 — `std.io.readLine` MIR symbol rewrite).
> **소유**: backend / toolchain.

## 목적

plan §6 PR2 (manual `parseCheckRequest` + `stringifyCheckResult`, ~500 LOC) 의 첫 시도 — `std.json.parseValue` / `getField` / `asString` 호출 가능 여부 확인. **단일 세션 무리** 확인 + 다음 단계 분석.

## 첫 시도 형태

```osty
use std.io
use std.json

fn parseSourceField(text: String) -> Result<String, Error> {
    let v = json.parseValue(text)?
    let sourceField = json.getField(v, "source")?
    json.asString(sourceField)
}

fn main() {
    let input = io.readLine()
    let _source = parseSourceField(input)  // 진짜 checker 호출은 PR3
    println(emptyCheckResultJson())
}
```

## 빌드 시도 결과

### 시도 1: `OSTY_STAGE0_FALLBACK=1` (`OSTY_STDLIB_BODY_LOWER=0` — 당시 기본은 off, 2026-05 이후 기본은 on)

```
Undefined symbols for architecture arm64:
  "_std.json.asString", referenced from: _main
  "_std.json.getField", referenced from: _main
  "_std.json.parseValue", referenced from: _main
ld: symbol(s) not found
```

**원인**: stdlib body injection off → `std.json.*` 함수가 declaration 만 emit, 정의 없음. PR [#1826](https://github.com/choiceoh/osty/pull/1826) 의 `std.io.readLine` 은 `osty_rt_io_read_line` C 함수에 직접 매핑 (옵션 1) 으로 해결했으나 `std.json.*` 은 같은 path 미적용.

### 시도 2: `OSTY_STAGE0_FALLBACK=1 OSTY_STDLIB_BODY_LOWER=1`

```
stage0 fallback declined: stage0: MIR shape outside bootstrap subset:
  function "osty_std_json__asString" does not match any stage0 pattern
```

**원인**: stdlib body 가 MIR 로 lowering 되나 `osty_std_json__asString` 의 MIR shape 가 stage0 16 decline 패턴 안에 있음 (audit 측정은 toolchain 만 cover, stdlib 미포함). 본 plan §3.1 의 99.8% 수치는 toolchain 모듈 기준 — stdlib 측은 별도 측정 필요.

## 진짜 옵션 분석

| # | 접근 | 추정 LOC | 위험 |
|---|---|---|---|
| 옵션 1' | `rewriteStdlibSymbolToRuntime` 에 `std.json.{parseValue,asObject,asString,getField,...}` 매핑 추가 | ~20 LOC | C type signature 일치 검증 + 각 함수마다 `osty_rt_json_*` 존재 확인 |
| 옵션 2' | `std.json.*` 의 Osty body 가 stage0 cover 가능하게 단순화 (현재 `parseStr` / `parseObj` 등이 큰 blocks) | stdlib refactor 수 일~수 주 | 큰 |
| 옵션 3' | manual byte-level JSON 파싱 (`std.json` 없이) — `String.bytes()` 등 primitive operation 위주 | ~150 LOC C-style Osty | low complexity, high LOC. 자체 parser 작성 |
| 옵션 4 | stdlib body injection 의 stage0 cover 확장 (`osty_std_json__*` 함수들의 MIR shape pattern 추가) | backend ~100 LOC + stdlib 측 verify | 중간 |

### 옵션 1' 의 잠재 문제

`osty_rt_json_*` C 함수 존재 ([osty_runtime.c](../internal/backend/runtime/osty_runtime.c)):

```c
osty_rt_json *osty_rt_json_parse(const char *text);
int64_t osty_rt_json_kind(void *raw_json);
bool osty_rt_json_get_bool(void *raw_json);
int64_t osty_rt_json_get_int(void *raw_json);
double osty_rt_json_get_float(void *raw_json);
const char *osty_rt_json_get_string(void *raw_json);
```

Osty `parseValue(text: String) -> Result<Json, Error>` 와 C `osty_rt_json *osty_rt_json_parse(const char *text)` 의 매핑:
- 반환 타입: `Result<Json, Error>` (Osty aggregate) ↔ `osty_rt_json *` (C pointer). **매핑 mismatch** — Result wrap 별도 필요.
- `Json` enum vs `osty_rt_json` opaque struct — 변환 wrap 필요.

옵션 1' 단순 symbol rewrite 만으로는 부족. ABI wrapper 가 필요 — stage0 측이나 lir_proto 측에서 별도 emit.

### 옵션 3' 의 미세 path (가장 self-contained)

소스가 `{"source":"..."}` 형태로 들어오면 manual 추출:

```osty
fn extractSourceFieldNaive(text: String) -> String {
    // Find '"source":' substring
    let key = "\"source\":"
    let kIdx = strings.indexOf(text, key)
    if kIdx < 0 { return "" }
    let afterKey = strings.substring(text, kIdx + key.len())
    // Skip whitespace, find opening quote
    ...
}
```

복잡하지만 stdlib body 의존 없음. `strings.indexOf` / `strings.substring` 만 사용 — 이들이 stage0 cover 안에 있을 확률 높음 (옵션 1 의 rewrite 와 비슷한 path 가 이미 작동 추정).

근데 escape 문자 처리 (`\"`, `\\`, `\n`) 가 정확해야 byte-identical parity 가능. 가짜 parser 의 한계.

## 판정

PR2 진행 가능 path 모두 단일 세션 무리:
- 옵션 1': backend ABI wrapper 필요 (Result/aggregate 변환)
- 옵션 2': stdlib refactor (수 일~수 주)
- 옵션 3': manual parser 작성 (~150 LOC, escape 정확성 위험)
- 옵션 4: stage0 의 stdlib MIR pattern cover 확장 (~100 LOC backend)

## 다음 세션 권장: 옵션 3' (manual naive parser)

이유:
- self-contained, backend 의존 0 (primitive String API 만)
- PR1c 옵션 1 가 `std.io.readLine` 만 cover → `std.json` 의존 회피
- escape 정확성은 L1 corpus 의 fixture 가 단순 source 위주 (`{"source":"fn main(){}"}` 식) 라 manageable

옵션 3' 의 가장 작은 형태 (다음 세션 시작):

```osty
fn naiveExtractSource(text: String) -> String {
    // assumes text is `{"source":"..."}` with no escape chars
    // good enough for L1 minimal fixtures
    let openMarker = "\"source\":\""
    let i = strings.indexOf(text, openMarker)
    if i < 0 { return "" }
    let start = i + openMarker.len()
    let end = strings.indexOf(strings.substring(text, start), "\"")
    if end < 0 { return "" }
    strings.substring(text, start, start + end)
}
```

L1 corpus 의 fixture 가 escape-free source 로 시작하면 충분.

## plan §10 갱신

- R7 (신규): std.json body injection 이 stage0 cover 밖 — backend wave (옵션 4) 또는 우회 (옵션 3') 필요
- Q16 (신규): stdlib body 의 stage0 cover 비율은? (audit 측정 확장 필요 — 본 plan §3.1 의 99.8% 는 toolchain 만)

## PR3 도 같은 wall

PR3 (진짜 checker 호출 — `selfhostBuildPackageAst` / `elabFile` / `serializeCheckResult` 등) 도 같은 std.json 의존 + toolchain 함수 import 의 multi-package build 문제. PR2 가 풀리지 않으면 PR3 는 추가 wall (toolchain crate dep)까지.

즉 **PR2 의 std.json wall 이 PR3 의 선행 조건**. 옵션 3' (manual parser) 가 PR2/PR3 둘 다 우회 가능.

## 이 PR 의 산출물

`docs/llvm-selfhost-plan-pr2-attempt.md` — std.json backend wall 측정 + 4 옵션 비교 + 다음 세션 권장 옵션 3' (manual naive parser).

revert: `cmd/osty-native-checker/main.osty` (PR1c 형태 유지).
