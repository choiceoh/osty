# LLVM self-host plan — PR1b readiness spike (Q6–Q10)

> **상태**: 측정 (이 PR).
> **선행**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md), [docs/llvm-selfhost-plan-spike-findings.md](llvm-selfhost-plan-spike-findings.md).
> **소유**: backend / toolchain.

## 목적

PR1a 가 머지된 직후 (LLVM-built `osty-native-checker-llvm` stub binary 동작 확인 완료), 다음 PR1b — **`stdin().readAll()` helper + smoke test** — 진입 전 추가 spike. 첫 spike (`Q1–Q5`) 가 답한 큰 risk 제거 위에, PR1b 의 정확한 backend 매핑 site 와 작업 분할을 측정.

## Q6 — `cmd/osty-native-checker/` 에서 `main.go` + `main.osty` 공존?

**답: YES** (PR [#1816](https://github.com/choiceoh/osty/pull/1816) 머지 시 확인).

```sh
$ OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/
Built .osty/out/debug/llvm/osty-native-checker-llvm

$ ./cmd/osty-native-checker/.osty/out/debug/llvm/osty-native-checker-llvm
osty-native-checker-llvm: stub
```

`osty build` 가 `osty.toml` + `.osty` 파일만 보고 `.go` 무시. Go shell 과 Osty entry 공존 안전.

## Q7 — `install-self` decline 수 (audit cover 99.8% 와의 monomorph 격차)?

**측정 시도**: `OSTY_STAGE0_FALLBACK=1 OSTY_STAGE0_LIST_ALL_DECLINES=1 .bin/osty install-self`.

**결과**:
```
osty install-self: no osty-self bootstrap source available:
selfhostcache: no usable osty-self binary found

stage0 fallback is enabled, but a full source bootstrap currently
requires opt-in because it can exceed memory/time limits before the
emergency emitter is reached.
to attempt the heavy source bootstrap anyway, set
OSTY_INSTALL_SELF_ALLOW_SOURCE_BOOTSTRAP=1.
```

`install-self` 는 source bootstrap 진입에 추가 opt-in 필요. heavy run (수 분 + 메모리 폭주 위험). 본 PR1b 진입 조건은 아님 — **PR1b 는 단일 `osty-native-checker-llvm` binary 의 stdin echo 만 필요하며 toolchain 전체 monomorph 와 무관**.

**Q7 결정**: 본 spike scope 바깥. 별도 measurement PR (`OSTY_INSTALL_SELF_ALLOW_SOURCE_BOOTSTRAP=1` heavy run) 또는 stage0 100% 도달 시 자동 종결.

## Q8 — `readAll` 추가 시 수정해야 하는 backend 매핑 site (inventory)

PR1a 머지 후 head (`32d63039`) 기준 grep:

### 8.1 C runtime — `internal/backend/runtime/osty_runtime.c`

현재 io 함수 (2개):

| 줄 | 시그니처 | 역할 |
|---|---|---|
| 25369 | `void osty_rt_io_write(const char *text, bool newline, bool to_stderr)` | print/println/eprint/eprintln |
| 25384 | `void *osty_rt_io_read_line(void)` | readLine — EOF 시 빈 String |

**필요 추가**: `void *osty_rt_io_read_all(void)` — `fread` loop, EOF 까지 누적, GC-managed String 반환. ~30 LOC.

### 8.2 stdlib intrinsic — `internal/stdlib/modules/io.osty`

현재 stdin 관련 (513–517):
```
pub fn print(s: String)
pub fn println(s: String)
pub fn eprint(s: String)
pub fn eprintln(s: String)
pub fn readLine() -> String
```

추가로 generic helper (402):
```
pub fn readAll(r: Reader) -> Result<Bytes, Error>
```
— **이건 generic Reader interface 위 helper, stdin console 직접 hook 아님**.

**필요 추가**: `pub fn readAll() -> String` intrinsic decl (517 옆) + `HostConsole.readAll(self) -> String` method (41 옆 readLine 패턴). ~8 LOC.

### 8.3 MIR generator — `toolchain/mir_generator.osty`

현재 io dispatch helper:

| 줄 | helper | 역할 |
|---|---|---|
| 849 | `llvmStdIoI1Text` (mirror) | bool → "true"/"false" |
| 10976 | `mirIsStdIoOutputMethod(name: String) -> Bool` | print/println/eprint/eprintln |
| 15534 | `mirStdIoMethodIsReadLine(method: String) -> Bool` | readLine 0-arg method |

**필요 추가**: `mirStdIoMethodIsReadAll(method: String) -> Bool` (15534 옆 readLine 패턴). 3 LOC.

`mirIsStdIoOutputMethod` 같은 broad dispatch 도 readAll 케이스 추가하든지, 별도 `mirStdIoMethodIsRead*` family 로 모음. **dispatch site 확인 필요** — `mirStdIoMethodIsReadLine` 의 caller 가 어디인지 grep 결과 0개로 나타났으나 (`internal/llvmgen/mir_generator.go` 가 PR #1405 에서 삭제됐기 때문, comment 만 stale). 실제 dispatch 는 `toolchain/lir_proto.osty` 의 어딘가 또는 다른 MIR lowering 경로.

### 8.4 LIR proto — `toolchain/lir_proto.osty`

현재 io runtime decl + emit:

| 줄 | 내용 |
|---|---|
| 1936 | `lirRuntimeDecl("osty_rt_io_write", lirVoidType(), [ptr, i1, i1], false)` |
| 4023 | `l.instrs.push(lirCall("", lirVoidType(), "@osty_rt_io_write", [text, ...]))` — print/println emit |

**필요 추가**:
- `lirRuntimeDecl("osty_rt_io_read_all", lirPtrType(), [], false)` (1936 옆).
- io_read_all call emit site — read 결과 String ptr 을 destination 으로. ~10 LOC.
- read_line 도 같은 자리에 없는 것으로 보아 readLine 자체는 별도 lowering 경로일 가능성 (e.g. intrinsic call 자체가 MIR 의 다른 패턴) — 진입 시 readLine emit site grep 으로 확인.

### 8.5 Stage0 fallback — `internal/backend/stage0/emit.go` (17679 LOC)

현재 io 관련 (grep 결과):

```
145–147: @.fmt.stage0.print.int, @.fmt.stage0.println.int 포맷 글로벌
149–151: @.fmt.stage0.print.str, @.fmt.stage0.println.str
153–154: declare i32 @printf(ptr, ...)
156–157: @stderr global
```

**stage0 의 io 처리 모델**: `osty_rt_io_*` 함수를 호출하지 않고 **직접 printf 사용**. 즉 stage0 는 io intrinsic 을 분리해서 cover 하지 않으며, MIR 패턴 매칭으로 println 을 인지하면 printf 호출 emit.

→ **readAll 은 stage0 가 currently cover 안 함**. 새 cover 추가:
- `osty_rt_io_read_all` extern declaration emit (or printf scanf 직접?).
- MIR `mirIsStdIoMethod*` 패턴이 매칭하는 함수 호출을 `call ptr @osty_rt_io_read_all()` 로 emit.
- 추정 ~40 LOC (현재 println dispatch 가 어디서 lowering 되는지 확인 후 옆에 추가).

### 8.6 Consumer — `cmd/osty-native-checker/main.osty`

```osty
fn main() {
    let text = stdin().readAll()  // 또는 readAll() bare
    // stub JSON response 출력
    println("{\"diagnostics\":[]}")
}
```

PR1b 의 smoke test:
```sh
$ echo '{"source":"fn main(){}"}' | ./.../osty-native-checker-llvm
{"diagnostics":[]}
```

## Q9 — PR1b 작업 분할 제안

5–6 파일 동시 수정. 두 가지 분할 전략:

### 옵션 A — 단일 PR (권장)

```
PR1b: stdin readAll + osty-native-checker stub JSON echo
├── internal/backend/runtime/osty_runtime.c     +30 LOC C (osty_rt_io_read_all)
├── internal/stdlib/modules/io.osty             +8 LOC   (intrinsic decl + HostConsole.readAll)
├── toolchain/mir_generator.osty                +5 LOC   (mirStdIoMethodIsReadAll helper)
├── toolchain/lir_proto.osty                    +12 LOC  (runtime decl + emit site)
├── internal/backend/stage0/emit.go             +40 LOC  (io read_all cover, println dispatch 옆)
├── cmd/osty-native-checker/main.osty           +5 LOC   (readAll() 호출 + stub JSON)
└── cmd/osty-native-checker/main_smoke_test.sh  +10 LOC  (echo '{}' | binary)
                                                ~110 LOC + 7 files
```

각 site 가 명확한 패턴 (println / readLine 옆) → 작업 위험 낮음.

### 옵션 B — 4 sub-PR 로 분할

```
PR1b-1: C runtime osty_rt_io_read_all 추가 (단독 머지 가능, dead symbol 으로 둠)
PR1b-2: stdlib intrinsic decl + mir_generator helper (consumer 없이 머지 가능)
PR1b-3: lir_proto + stage0 emit + LLVM lowering 통합
PR1b-4: main.osty consumer + smoke test
```

각 PR 이 ~20–40 LOC. mergeable 단위 작아 review 부담 ↓. 단 PR 4개 chain → 시간 비용 ↑.

**권장**: 옵션 A. 110 LOC 는 단일 PR 수용 가능 크기. 각 파일 변경이 명확 패턴 따라가므로 review 어렵지 않음.

## Q10 — PR1b 위험 재산정 (Q8 inventory 기반)

| 작업 | 위험 | 근거 |
|---|---|---|
| C runtime fread loop | **낮음** | `osty_rt_io_read_line` 옆 단순 패턴, fread + realloc loop |
| io.osty intrinsic decl | **낮음** | 한 줄 prototype + HostConsole method 한 줄 |
| mir_generator helper | **낮음** | `mirStdIoMethodIsReadLine` 옆 3 LOC 추가 |
| lir_proto emit | **낮음~중간** | runtime decl 패턴 명확하나 readLine 의 lowering 경로 grep 미발견 → 진입 시 추가 측정 가능성 |
| stage0 emit io cover | **중간** | stage0/emit.go 17679 LOC, io intrinsic dispatch 가 currently 없음. println 처리 패턴 따라 새 함수 추가. 진입 시 println cover 위치 정확히 찾는 spike (Q11) 필요할 수도 |
| main.osty + smoke | **낮음** | echo pipe + diff |

**전체 PR1b 위험**: 중간. fresh context 1 세션에서 가능. 단 stage0 의 io intrinsic dispatch 위치 (현재 README 안 보임) 가 진입 시 발견될 가능성 — 보조 spike (Q11) 가 필요할 수도.

## Q11 (잠재) — stage0 의 println / readLine 정확한 dispatch site?

본 spike 의 grep 으로는:
- `internal/backend/stage0/emit.go` 의 io 처리는 `printf` declaration + format string 글로벌 (line 145–157) 만 보임.
- 실제 "MIR `IntrinsicCall("println", ...)` → `call printf(@.fmt.stage0.println.str, ...)`" 의 dispatch arm 은 grep 결과에서 발견 안 됨.

**필요 측정** (PR1b 진입 시점): `stage0/emit.go` 안에서 `IntrinsicCall` 또는 `mirCall` dispatch 가 println string 을 어떻게 lowering 하는지 정확한 함수 위치. 이게 readAll cover 시 자리할 자리.

본 spike 에서 측정 안 한 이유: 17679 LOC 파일을 시간 내에 완전 inventory 하기 어렵고, PR1b 진입 시 grep 한 번 더 (`grep -n "println\|stage0.io\|printf.*fmt" stage0/emit.go | sort | uniq`) 면 충분히 위치 추적 가능. 본 plan 의 §10.2 Q11 로 등록.

## 다음 — PR1b 진입 체크리스트

다음 세션 시작 시 (fresh context):

1. **선행 확인** — `git pull` 후 `OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/` 가 PR1a 시점과 동일하게 통과하는지 (regression 없음).
2. **Q11 spike** — `stage0/emit.go` 의 println dispatch arm 정확한 위치 + 함수 명. ~5 분.
3. **PR1b 옵션 A 진행** — 위 §Q9 의 7 파일 변경. ~110 LOC.
4. **smoke** — `echo '{"source":""}' | binary` 가 `{"diagnostics":[]}` 같은 stub JSON 출력 + exit 0.
5. **prepush + commit + push + PR**.

## 본 spike 가 plan 에 가져온 갱신

이 PR 머지 후 `docs/llvm-selfhost-plan.md` 갱신 항목:

- §5.2 N5 (stdin readAll helper) 의 LOC 추정 — ~40 LOC → ~110 LOC (실측 후).
- §10.2 Q6 답 (이미 채워짐), Q7 결과 (별도 measurement), Q11 신규.

## 본 spike 의 한계

- Q7 (install-self decline) 미측정 — heavy run + opt-in 필요. 본 PR1b 차단 조건 아님.
- Q11 (stage0 println dispatch 정확 site) 미측정 — 17679 LOC 파일 inventory 시간 비용. PR1b 진입 시 grep 한 번이면 충분.
- io intrinsic 의 실제 lowering call chain 끝점 (MIR → LIR Proto vs MIR → stage0 두 갈래) 가 어디서 갈리는지 본 spike 에서 완전 추적 안 함.

이런 잔여 unknown 은 모두 PR1b 의 5분 spike (선두 작업) 로 메울 수 있는 크기. 본 spike 가 도달한 답이 충분히 actionable.
