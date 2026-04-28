# STDLIB_MATRIX.md

Osty 표준 라이브러리 모듈별 production-ready 상태 매트릭스.

> **Scope**: `internal/stdlib/modules/*.osty` (Phase B / surface) +
> `internal/llvmgen/stdlib_*_shim.go` (Phase A / LLVM bridge) +
> `internal/backend/runtime/osty_runtime.c` 의 `osty_rt_*` 함수 (Phase A / C runtime).
>
> **기준일**: 2026-04-28 (post-stdlib-sprint audit). 어제 (#1018–#1029) 24시간
> 안에 stdlib 8개 모듈에 대규모 작업이 들어왔고 (http 26×, io 34×, net 1.5×,
> crypto runtime 1679 LOC, random/compress/math/os runtime 추가),
> osty resolver authoritative 마일스톤(#1013)도 같이 통과했다.
>
> **평가 모델 정정 (중요)**: `.osty` 줄 수만 보면 *bodyless declaration +
> runtime intrinsic* 패턴 모듈 (math, crypto, fs, env, random, os, compress)
> 들이 stub 으로 오해된다. 실제 평가는 *의도된 범위 ÷ 실제 커버리지* 기준
> 이어야 한다. 자세한 함정 케이스는 [§5](#5-평가-함정-osty-줄-수-모델의-한계) 참조.

## 1. 4-tier 분류

총 37 모듈. 분류 기준:

- **⭐⭐⭐⭐⭐ Production**: surface + backend 모두 풀 커버. 외부 사용자에게 추천 가능
- **⭐⭐⭐⭐ Production-adjacent**: 사용 가능. 일부 helper 미흡 또는 surface 부풀림 다음 라운드
- **⭐⭐⭐ Functional**: 기본 사용 가능, 깊이는 부족
- **🚧 Skeleton / Empty**: 작업 안 됨

### ⭐⭐⭐⭐⭐ Production (32 / 37 = 86%)

| 모듈 | Surface (LOC) | Backend | 비고 |
|---|---|---|---|
| strings | 7242 | string 17 + bytes 29 runtime | 압도적. UTF-8 / casefold / normalize / grapheme |
| http | 1468 | net 40 runtime | Router + Cookie + MediaType + Form + Query + dispatch |
| net | 1084 | net 40 runtime | TCP/UDP, IPv4/IPv6, parseSocketAddr, tcpListen |
| fmt | 926 | (surface heavy) | graphem-aware width, format spec engine |
| json | 629 | (parser self) | generic encode<T> / decode<T>, UTF-8 / surrogate pair |
| url | 592 | — | parse / join / format |
| io | 541 | shim 171 | Reader/Writer 프로토콜, Buffer, copyN, readExact |
| collections | 509 | list 68 + map 42 + set 17 | 30+ List 메서드, groupBy, windowed, zip3 |
| result | 357 | — | composition (map / mapErr / and / or / collect) |
| option | 356 | — | flatten / transpose / traverse / map2 / map3 |
| csv | 351 | — | options-driven, header-aware decode |
| char | 219 | — | Unicode / ASCII methods |
| iter | 118 | — | iteration 프로토콜 |
| bytes | 113 | bytes 29 runtime | byte 슬라이스 조작 |
| sync | 96 | mu 5 + rmu 5 | Mutex<T> / Locked<T> type-state, RwLock, Atomic |
| error | 96 | — | Error interface + wrap / rootCause cause chain |
| time | 89 | (검색 필요) | Duration / Instant / Zone / Weekday / ZonedTime |
| testing | 88 + 168 (gen) | test 5 + bench 7 | assert + benchmark + snapshot + property-based (Gen<T>) |
| log | 85 | — | Level + Handler + TextHandler / JsonHandler — slog 동급 |
| regex | 74 | — | compile / matches / find / findAll / replace / split |
| os | 60 | shim 421 + runtime 22 | exec / execShell / exit / pid / hostname / onSignal |
| thread | 59 | thread 16 + chan 10 + select 22 | spawn / race / chan / select / cancel — 구조적 동시성 |
| math | 42 | float 40 runtime | sin/cos/tan/asin/acos/atan/atan2/sinh/cosh/tanh/exp/log/log2/log10/sqrt/cbrt/pow/floor/ceil/round/trunc/abs/min/max/hypot/clamp/fract/signum + libm 매핑 |
| fs | 29 | shim 685 + runtime 21 | read/write/exists/create/remove/rename/copy/mkdir/mkdirAll — 11 API |
| cmp | 27 | — | Equal / Ordered / Hashable interface 정의 (osty 타입 시스템 기반) |
| random | 26 | shim 524 + runtime 17 | Rng struct + default + seeded RNG |
| hint | 25 | — | black_box (벤치마킹용 컴파일러 최적화 차단) |
| process | 24 | — | abort / unreachable / todo / ignoreError / logError (exception helper, NOT OS process) |
| crypto | 23 | shim 262 + runtime 19 (#1018: 1679 LOC) | sha256 / sha512 / sha1 / md5 / HMAC / randomBytes (CSPRNG) / constantTimeEq |
| uuid | 22 | — | v4 + v7 (2024 IETF draft 8) + parse + nil |
| env | 20 | shim 624 + runtime 15 | args / get / require / set / unset / vars / currentDir |
| debug | 10 | — | dbg<T>(v) — Rust dbg! 매크로 |
| ref | 9 | — | same<T>(a, b) — reference identity 비교 |

### ⭐⭐⭐⭐ Production-adjacent (4 / 37 = 11%)

| 모듈 | Surface (LOC) | 갭 |
|---|---|---|
| compress | 11 | gzip 만 (zstd / deflate 추가 가능). Phase A shim 199 |
| encoding | 41 | Base64 / Base64Url / Hex / UrlEncoding 4 표준. struct method 직접 확인 필요 |
| testing_gen | 168 | property-based testing generator. testing 의 helper |
| (process) | (위 Production 에 분류) | 이름 함정 — OS process spawn 은 `os.exec` 에 있음 |

### 🚧 미확인 / 갭

기존 37 모듈 중 *진짜 stub* 은 없음. 모든 모듈이 *의도된 범위 안에서 production* 또는 *production-adjacent*.

## 2. 카테고리별 데모 가능성

| 데모 종류 | 가능성 | 필요 모듈 |
|---|---|---|
| HTTP 웹 서버 | ✅ 즉시 가능 | http + io + json + net |
| CLI 도구 | ✅ 즉시 가능 | os + fs + io + fmt + env |
| 데이터 처리 (CSV / JSON) | ✅ 즉시 가능 | csv + json + collections + iter + fmt |
| 파일 변환기 | ✅ 가능 | fs + io + compress (gzip 만) + fmt |
| 암호화 / 해시 | ✅ 가능 | crypto (sha256 / hmac / random / constantTimeEq) |
| 랜덤 게임 / 시뮬레이션 | ✅ 가능 | random (seeded RNG) + math |
| 수치 계산 | ✅ 가능 | math (sin / cos / log / exp / sqrt / pow / floor / ceil / clamp / hypot) |
| 시간 처리 | ✅ 가능 | time (Duration / Zone / parse / sleep) |
| Regex 매칭 | ✅ 가능 | regex (compile / find / replace / split) |
| Property-based testing | ✅ 가능 | testing (Gen<T> / property / propertySeeded) |
| Structured logging | ✅ 가능 | log (Handler / TextHandler / JsonHandler) |
| 동시성 (구조적) | ✅ 가능 | thread (spawn / race / chan / select / cancel) |

## 3. 진짜 약점 (없는 모듈)

기존 모듈은 production-ready. 진짜 갭은 *없는 모듈*들:

| 없는 모듈 | 영향 | 우선순위 |
|---|---|---|
| db / sql | DB 작업 (sqlite / postgres wrap 필요) | 높음 (실용 어플리케이션 핵심) |
| email / smtp | 메일 발송 | 중간 |
| template (HTML) | 웹 템플릿 | 중간 (http 와 페어) |
| websocket | WS 통신 | 중간 (http 와 페어) |
| cli (argparse-like) | CLI flag 파싱 (현재 env.args 만) | 중간 |
| tar / zip | 아카이브 (compress 는 gzip 만) | 낮음 |
| image | 이미지 디코딩 | 낮음 |
| xml | XML 파싱 | 낮음 |
| graphql | GraphQL 클라이언트 / 서버 | 낮음 |
| i18n / l10n | 다국어 | 낮음 |

## 4. Phase A / B 분리 패턴

24 시간 sprint (#1018–#1029) 에서 드러난 작업 패턴:

**Phase A (runtime + LLVM bridge)** — 사용자에게 안 보임:
- `internal/backend/runtime/osty_runtime.c` — C 로 실제 함수 구현 (예: gzip, sha256)
- `internal/llvmgen/stdlib_*_shim.go` — LLVM IR generation bridge
- `toolchain/check.osty` — 체커 인지 (호출 가능 여부)
- 테스트 (Go 측)

**Phase B (`.osty` surface)** — 사용자에게 보임:
- `internal/stdlib/modules/*.osty` — `pub fn` declaration / wrapper
- `LANG_SPEC_v0.5/10-standard-library/*.md` — 스펙 문서

**의도된 우선순위**: Phase A 먼저, Phase B 나중. runtime support 없이 surface 만 만들면 *컴파일은 되지만 실행 못 함* 함정. backend 먼저 → wrapper 나중 순서가 정직.

**현재 상태**:
- 14 모듈은 Phase A + Phase B 둘 다 충실 (strings, http, net, fmt, json, url, io, collections, result, option, csv, char, iter, bytes)
- 6 모듈은 Phase A 충실 + Phase B declaration-only (fs, env, random, os, crypto, compress)
- 나머지는 의도된 범위에서 surface 만으로 완성 (math, cmp, hint, debug, ref, process, log, time, error, sync, thread, regex, testing, encoding, uuid)

## 5. 평가 함정: `.osty` 줄 수 모델의 한계

이전 평가 (4 회) 가 `.osty` 줄 수만 보고 잘못된 결론을 냈다. 함정 패턴:

### 5.1 Bodyless declaration + runtime intrinsic

`math.osty` 42 줄, `fs.osty` 29 줄, `crypto.osty` 23 줄 모두 *각 함수가 한 줄 declaration*:

```osty
pub fn sin(x: Float) -> Float
pub fn read(path: String) -> Result<Bytes, Error>
pub fn sha256(data: Bytes) -> Bytes
```

실제 구현은 `osty_runtime.c` + `stdlib_*_shim.go` 에 있다. 줄 수가 작은 게 아니라 *각 declaration 이 한 줄로 압축*된 것.

### 5.2 1-2 함수 의도 모듈

`debug.osty` (10 줄), `ref.osty` (9 줄), `hint.osty` (25 줄) 는 의도된 범위가 1-2 함수. 줄 수 작아도 *완성*.

### 5.3 Interface / struct 정의 모듈

`cmp.osty` 는 27 줄로 Equal / Ordered / Hashable 3 interface 정의. *전체 osty 타입 시스템의 기반*. 줄 수보다 *디자인 깊이* 가 본질.

### 5.4 Typed struct + method 패턴

`Mutex<T>` (sync.osty), `Rng` (random.osty), `Regex` (regex.osty) 는 struct 1 개에 method 가 매달려 있다. struct declaration 이 짧아도 *풀 라이브러리 표면*.

### 5.5 이름 함정

`process.osty` 는 OS process 가 아니라 *exception / result helper* (abort / unreachable / todo / ignoreError / logError). 진짜 OS process 작업은 `os.exec` 에 있다. *이름만 보고 카테고리 추정 금지*.

## 6. 정확한 평가 모델

```
모듈 평가 = (의도된 범위 ÷ 실제 커버리지) × (Phase A 충실도 + Phase B 충실도)
```

질문 순서:
1. 이 모듈이 *의도하는 범위* 는 무엇인가? (LANG_SPEC + 모듈 헤더 코멘트)
2. 그 의도가 *Phase A (runtime)* 에서 다 채워졌나?
3. 그 의도가 *Phase B (surface)* 에서 다 노출됐나?
4. 둘 다 OK 면 ⭐⭐⭐⭐⭐ Production. Phase A 만 OK 면 ⭐⭐⭐⭐ Backend-heavy.

`.osty` 줄 수 만으로 평가하지 말 것.

## 7. 인상적인 디자인 디테일

stdlib audit 중 발견된 *진짜 자랑할 만한* 디자인 패턴:

1. **`sync.Mutex<T>` + `Locked<T>` type-state** — 락 안 잡고 데이터 접근 컴파일 타임 차단. Rust 모델 정확
2. **`crypto.constantTimeEq`** — timing-safe 비교. 일반 라이브러리는 `==` 쓰는데 osty 는 timing attack 인지
3. **`uuid.v7()`** — 2024 IETF draft 8 의 time-ordered UUID. *최신 표준 따라잡음*
4. **`testing.property<T>(name, Gen<T>, pred)`** — property-based testing first-class. QuickCheck / Hypothesis 모델. Go / Crystal / Nim 다 third-party
5. **`log.Handler` + `TextHandler` / `JsonHandler`** — structured logging first-class. slog / zap 모델
6. **`option.map2 / map3 / traverse / filterMap`** — 함수형 composition 깊이. Rust + Scala + Haskell Maybe monad 영향
7. **`fmt.visibleWidth` + `graphemeSlice`** — graphem-aware width. 일반 언어는 char count, osty 는 *이모지 조합 + zero-width joiner* 정확 처리
8. **`http.Router` + `Cookie` + `MediaType` + `parseQuery` + `parseSetCookie`** — 풀 HTTP 스택. 단순 client wrapper 아님
9. **`io.Reader` / `Writer` / `ByteWriter` / `LineReader`** — Go io 동급 추상화. fs / http / net 다 이 위에 얹힘
10. **`collections.windowed(size, step)`** — sliding window. Rust std 에 없음 (itertools 만)

## 8. 다음 라운드 후보

stdlib 자체는 거의 production. 다음 우선순위:

1. **새 모듈 추가** (db / template / cli / websocket) — *없는 모듈* 카테고리 채우기
2. **compress / encoding 깊이** — zstd / deflate / Base32 등 추가 표준
3. **Phase B surface 부풀리기** — random / crypto / compress 는 Phase A 풍부한데 Phase B helper 가 declaration 위주. user-friendly wrapper (예: `crypto.sha256Hex(data)`, `random.shuffle(list)`) 추가
4. **스펙 문서 동기화** — `LANG_SPEC_v0.5/10-standard-library/*.md` 가 24 시간 sprint 진척 따라잡았는지 확인
