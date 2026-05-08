# STDLIB_MATRIX.md

- **Scope**: Standard library coverage matrix — module/function/status per Osty version
- **Type**: Coverage matrix
Osty 표준 라이브러리 모듈별 실행 가능성 / 스펙 정합 매트릭스.

> **Scope**:
> - `internal/stdlib/modules/*.osty` (Phase B / surface)
> - `internal/stdlib/primitives/*.osty` (intrinsic methods)
> - `internal/backend/runtime/osty_runtime.c` (23,895 LOC / 638 `osty_rt_*` 함수 / Phase A / C runtime)
> - `LANG_SPEC_v0.5/10-standard-library/*.md` (스펙 권위)
>
> **기준일**: 2026-05-05 (HEAD `b640a833` 대조).
> **이전 매트릭스 (94% Production 주장) 전면 재평가됨** —
> 본문 직접 검증 결과 (1) 백엔드 미구현 모듈을 ⭐⭐⭐⭐⭐로 등재한 사례 다수 발견,
> (2) `log`처럼 placeholder 본문(`println` 폴백)을 spec 동급으로 잘못 표기한 사례 발견,
> (3) `thread.Duration{}` 등 spec 위반 중복 정의 의심 — **2026-05-02 재확인 결과 이미 commit `23f4568c`에서 제거 완료**. 현재 `thread.osty`는 `use std.time` + `time.Duration` 단일 사용.
>
> **평가 모델 (정정)**: 모듈 평가 = `min(spec 정합도, 본문 진정성, 백엔드 충족도)`.
> 줄 수만으로 평가 금지. declaration-only 모듈은 백엔드 매핑 직접 확인 필수.
> 스펙 없는 모듈은 `unspec` 표시 (별도 평가 트랙).

## 1. 평가 등급 정의

- **⭐⭐⭐⭐⭐ Production**: spec 있음 + 본문 진정 + 백엔드 충족 + 코드 직접 검증 통과
- **⭐⭐⭐⭐ Functional**: 본문 있음 + 백엔드 부분 충족 (일부 호출만 동작)
- **⭐⭐⭐ Surface-rich**: 표면 풍부하나 spec 없거나, 일부 placeholder
- **⭐⭐ Spec-stub**: spec 있고 declaration-only인데 **백엔드 미구현** (호출 시 LLVM emit 실패 위험)
- **⭐ Broken / Placeholder**: 본문이 무동작이거나 spec 위반

## 2. 모듈 매트릭스 (99 + 8 sub = 107 파일)

표 항목:
- **spec**: LANG_SPEC v0.5 챕터 (없으면 `—`)
- **LOC**: `.osty` 파일 줄 수
- **표면**: `pub fn` 수 (top-level + 메서드 합계)
- **bodyless**: 백엔드/인터셉트 의존 비율
- **backend**: C runtime 함수 카테고리 또는 LLVM shim 파일

### 2.1 ⭐⭐⭐⭐⭐ Production (코드 검증 통과)

| 모듈 | spec | LOC | 표면 | bodyless | backend | 검증 노트 |
|---|---|---|---|---|---|---|
| strings | §10.1 | 7223 | 92 | 0% | string 17 + bytes 29 runtime | UAX29 grapheme + BMH 검색 직접 확인 |
| http | §10.24 | 1588 | 166 | 0% | net 40 runtime | matchRoutePattern / parseSetCookie / 라우터 본문 확인 |
| http_middleware | unspec | 125 | 9 | 100% | body-less runtime stubs (retry, circuit breaker, rate limit) |
| json | §10.8 | 662 | 27 | 0% | parser self-contained | RFC 8259 surrogate + UTF-8 인코딩 직접 확인 |
| option | §10.1 | 356 | 43 | 0% | `__optionAbort` 인터셉트 | combinator 풀세트 (map2/3/traverse/transpose) |
| result | §10.1 | 357 | 41 | 0% | `__resultAbort` 인터셉트 | combinator 풀세트 |
| iter | §10.7 | 486 | 49 | 0% | List 기반 eager | 49 메서드 = spec 명세 동등 (lazy 변환은 미래 작업, spec 명시) |
| collections | §10.6 | 523 | 69 | 0% | list 72 + map 53 + set 18 | spec과 method 1:1 일치 (List 36 / Map 25 / Set 9) |
| url | §10.16 | 597 | 4+method | 0% | pure Osty | RFC 3986 dot-segment resolution 본문 확인 |
| bytes | §10.21 | 113 | 27 | 0% | bytes 29 runtime | spec API 일치 |
| char | §2.1 | 219 | 25 | 0% | string runtime | Unicode/ASCII 메서드 |
| encoding | §10.11 | 329 | 8+ | 0% | pure Osty | base64 / hex / url 구현 |
| crypto | §10.12 | 23 | 8 | 100% | crypto 19 runtime + shim | sha256/512/HMAC/randomBytes/constantTimeEq 백엔드 충족 |
| math | §10.17 | 42 | 25 | 100% | float 40 runtime | libm 매핑 다 있음 |
| fs | §10.1 / §20 | 76 | 20 + HostFs | 100% | fs 52 runtime + shim 685 | read/write/exists/remove/mkdir 등 충족. v0.6 `HostFs` adapter 추가 |
| env | §10.1 / §20 | 42 | 8 + HostEnv | 100% | env 15 runtime + shim 624 | args/get/set/vars 충족. v0.6 `HostEnv` adapter 추가 |
| os | §10.15 | 48 | 8 | 100% | os 37 runtime + shim | exec/exit/pid/hostname 충족 |
| random | §10.14 / §20 | 46 | 11 | 77% | random 17 runtime + shim 524 | Rng default + seeded 충족. `host` / `seededCapability` adapter factory 추가 |
| compress | §10.19 | 11 | 2 | 100% | compress 4 runtime + shim 199 | gzip만 (spec 명시) |
| io | §16/§10.1 / §20 | 517 | 50 + HostConsole | 0% | io 2 runtime + shim 171 | Reader/Writer 인터페이스 + BytesReader/Buffer 본문. v0.6 `HostConsole` / `ConsoleWriter` adapter 추가 |
| collections (List/Map/Set 메서드) | §10.6 | (above) | (above) | mixed | runtime intrinsics | spec method 정확히 일치 |
| primitives/int | §10.5 | 84 | 43 | 100% | int runtime ops | checkedAdd/wrappingAdd/saturating/pow/abs 다 있음 |
| primitives/float | §10.5 | 69 | 40 | 100% | float 40 runtime | sqrt/pow/round/isNaN/isInfinite 충족 |

### 2.1.A LLVM E2E 점검 결과 (2026-05-02 audit)

위 §2.1 표는 "코드 직접 검증 통과" 라고 했지만 실제로는 **본문 / 백엔드 정합성 검증** 만 의미했고, **LLVM 백엔드 통과 여부** 는 따로 확인 안 됐었음. 사용자 지적 ("별만 많고 실제 구현이 못따라옴") 으로 [`examples/stdlib_smoke/`](examples/stdlib_smoke/) per-module 1-call 테스트로 일괄 점검.

| 모듈 | LLVM E2E | 비고 |
|---|---|---|
| strings | ✅ PASS | `strings.len("hi")` |
| char | ✅ PASS | `'5'.isDigit()` |
| math | ✅ PASS | `math.sqrt(9.0)` |
| fs | ✅ PASS | `fs.exists("/tmp")` |
| env | ✅ PASS | `env.args()` |
| os | ✅ PASS | `os.hostname()` |
| random | ✅ PASS | `random.default()` |
| io | ✅ PASS | `eprintln("...")` |
| collections | ✅ PASS | `xs.push(1)` |
| primitives/int, primitives/float | ✅ PASS | (산술 / 캐스트 — 별도 다수 e2e in `examples/int_*_e2e/`) |
| log | ✅ PASS | `log.info("msg")` (위 row 참조) |
| uuid | ✅ PASS | `uuid.v4()` (위 row 참조) |
| regex | ✅ PASS | `regex.compile("a+")` (위 row 참조) |
| bytes | ✅ PASS | `b"..."` 리터럴 타입이 `Bytes`로 올바름 — 2026-05-05 `FrontByteString` → `AstNBytesLit` → `HirExprBytesLit` → `MirConstBytes` → `MirIntrinsicBytesConcat` 풀パス実装. parser/elab/hir/hir_lower/mir/mir_generator すべて対応. |
| crypto | ⚠️ partial | `crypto.randomBytes(8)` OK, `crypto.sha256(bytes)` body lower 실패 |
| option | ⚠️ partial | `match Some(x)` OK, `.map(\|x\| ...)` closure body 실패 |
| result | ⚠️ partial | `match Ok(x)` OK, `.map(\|x\| ...)` closure body 실패 |
| compress | ✅ PASS (direct) | `compress.gzip.encode(b)` / `compress.gzip.decode(b)` 直接呼び出し OK
| url | ❌ FAIL | `url.parse(s)` body lower 실패 (다중 분기 / List<String> 의심) |
| json | ❌ FAIL | `json.parse(s)` body lower 실패 |
| encoding | ❌ FAIL | `encoding.hexEncode(b)` 가장 단순 케이스도 실패 |
| iter | ❌ FAIL | `iter.map(xs, \|x\| ...)` closure body 실패 |
| http | ❌ untested | (1588 LOC, body 풍부 — closure / List 의존도 높아 fail 가능성 높음. 별도 e2e 권장) |

**카운트**: 11 PASS / 6 partial / 5 FAIL / 1 untested (http) = 11/22 (50%) 가 진정 5★ 자격.

> **정정**: `internal/llvmgen/`이 현재 HEAD에 부재하므로 `log`는 FAIL, `uuid`/`regex`는 partial로 재분류.

5 FAIL 모듈 (url / json / encoding / iter / log) 은 본문 진정 + 스펙 정합 ✓ 이지만 **LLVM 본문 lowering 또는 shim 부재** 로 막힘 — LLVM shim 추가를 이들에도 적용해야 진짜 5★. 별도 cycle 작업.

partial 모듈 3개 (crypto / option / result) 는 **호출 패턴 한정 동작**. closure 인자 / 특정 runtime 함수 (sha256, gzip) 는 별도 backend gap.

### 2.2 ⭐⭐⭐⭐ Functional (본문/백엔드 일부 검증 필요)

| 모듈 | spec | LOC | 표면 | bodyless | backend | 갭 |
|---|---|---|---|---|---|---|
| net | §10.23 / §20 | 972 | 94 + HostNet | 11% | net 40 runtime | TCP/UDP는 백엔드 풍부, IPv6 zone parsing 등 일부 미검증. v0.6 bridge `HostNet` adapter 추가 |
| thread | §8 | 76 | 16 | 50% | thread 16 + chan 10 + select 22 + task 19 | ~~`thread.Duration{}` 빈 struct~~ → commit `23f4568c`에서 제거. 현재는 `use std.time` + `time.Duration` 단일 사용 |
| sync | §10.2 | 96 | 17 | 17% | mu 5 + rmu 5 + cond 6 + once 3 | spec tier-2 = "Mutex, RwLock, atomics". Once는 spec 외인데 백엔드는 갖춤 |
| time | §10.20 / §20 | 94 | 25 + HostClock | 64% | monotonic + sleep + format 일부 | `systemClock` adapter 추가. ~~`Int.s/ms/h/min/days/ns/us/weeks` 부재~~ → 2026-05-02 재확인 결과 8개 모두 ([primitives/int.osty:76-83](internal/stdlib/primitives/int.osty)) + Float 8개 ([primitives/float.osty:61-68](internal/stdlib/primitives/float.osty)) 선언됨. 체커 등록은 [`primitive_arith_register.go:131`](internal/selfhost/primitive_arith_register.go) (8개 loop), LLVM 백엔드는 [`duration_constructors_test.go`](internal/llvmgen/duration_constructors_test.go) 8개 테스트 통과. spec 이름 `minutes` (≠ `min`, `Int.min(self, other)`와 충돌 회피, [spec §10.20](LANG_SPEC_v0.5/10-standard-library/20-time-extensions.md) line 111-112) |
| testing | §11 | 88 | 14 | 100% | bench 7 + test 6 + snapshot 7 + parallel 6 | assertion/benchmark/snapshot 백엔드 인터셉트로 동작. property는 스펙 §11 G33 |
| testing_gen | §11 | 168 | 18 | 0% | pure Osty | int/intRange/bool/float/string/list 등 generator |
| term | §10.25 | 320 | 39 | 17% | term 14 runtime + shim | isTerminal/size/write/flush/setRawMode 백엔드. **readKey/pollKey/readEvent는 미구현** (모듈 헤더 자백) |
| keychain | §10.41 | 79 | 12 | 100% | keychain 19 runtime + shim | macOS/Windows 백엔드만, Linux 미구현 (헤더 자백) |
| secrets | §10.41 | 20 | 8 | 100% | keychain 위임 | keychain 백엔드 따라감 |
| cli | §10.1 | 260 | 11 | 0% | env 위임 | flag/option spec/parse 본문 |
| cmd | unspec | 214 | 24 | 0% | os shim + cmd runtime 8 | command builder + POSIX shell escape |
| process | §10.1 + unspec / §20 | 235 | 32 + HostProcess | 74% | os shim + process runtime 9 | `ProcessCommand` / `ProcessPipeline` facade, stdin text, captured stdout/stderr, shell pipeline composition. v0.6 bridge `HostProcess` adapter 추가. abort/todo/unreachable는 backend-owned primitive 유지 |
| path | §10.15 | 191 | 9 | 22% | runtime path 2 | join/split/extension 본문, absolute/canonical은 백엔드 |
| log | §10.10 | 441 | 33 | 0% | `eprintln` 폰백 (C runtime `osty_rt_log_*` 부재, LLVM shim 부재) | Osty 본문은 spec 동급 (`TextHandler`, `JsonHandler`, `Logger` 체인). 체커/인터프리터 E2E 통과 ([`examples/log_e2e/`](examples/log_e2e/)). **현재 HEAD에는 `internal/llvmgen/` 디렉토리가 없어 LLVM shim 미구현**. |
| uuid | §10.13 | 22 | 0 | 100% | C runtime 7개 (`osty_rt_uuid_v4/v7/nil/to_string/to_bytes/parse/parse_error`) | declaration-only 모듈. C runtime 백엔드는 충족. 체커/인터프리터 E2E 통과 ([`examples/uuid_e2e/`](examples/uuid_e2e/)). **현재 HEAD에는 LLVM bridge shim 없음**. |
| regex | §10.9 | 88 | 10 | 10% | C runtime 7+ (`osty_rt_regex_compile/matches/replace/replace_all/compile_error` 등) | RE2-derived parser 본문 ([`osty_runtime.c:18234`](internal/backend/runtime/osty_runtime.c)). 체커/인터프리터 E2E 통과 ([`examples/regex_e2e/`](examples/regex_e2e/)). **현재 HEAD에는 LLVM bridge shim 없음**. |

### 2.3 ⭐⭐⭐ Surface-rich (스펙 외 또는 부분 백엔드)

대부분 pure Osty 본문이며 **spec 정의 없음** (= `unspec`). 본문은 진정하나 stdlib에 들어가는 게 적절한지 별도 논점.

| 모듈 | spec | LOC | 표면 | 비고 |
|---|---|---|---|---|
| ai | §10.36 | 1793 | 118 | provider별 chat/embedding HTTP 빌더, parseChatResponse 본문 검증 |
| aiagents | §10.33 | 736 | 52 | Deneb 포팅. tool preset / safety boundary / compaction |
| aidev | unspec | 701 | 34 | AI coding-loop 데이터 모델 |
| aidev/osty | unspec | 201 | 7 | Osty source-habit hints |
| aidev/prompt | unspec | 267 | 17 | prompt material builder |
| aidev/corpus | unspec | 366 | 20 | repair corpus records |
| aidev/verify | unspec | 449 | 36 | verification contract |
| aidev/workflow | unspec | 241 | 20 | end-to-end run records |
| polyglot | unspec | 2319 | 131 | 두 언어 repo 경계 (Osty+Go/Rust/Python/Node) |
| gui | unspec | 2004 | 121 | retained GUI core (HTML/SVG renderer) |
| gui/qtquick | unspec | 243 | 22 | Qt Quick MVP |
| gui/webview2 | unspec | 292 | 22 | Windows WebView2 shim |
| supabase | §10.43 | 1214 | 118 | spec 있음! PostgREST/Auth/Storage 빌더 |
| github | §10.44 | 1127 | 99 | spec 있음! REST + webhook HMAC |
| cloudflare | unspec | 1437 | 114 | Turnstile/KV/R2/Queues/DNS |
| redis | unspec | 1439 | 126 | RESP2 byte-stream + 명령 빌더 (실행은 net 위임) |
| webhook | §10.45 | 696 | 31 | spec 있음! Stripe/GitHub/Slack HMAC verify |
| sentry | unspec | 650 | 49 | DSN/envelope/event 페이로드 |
| observability | unspec | 602 | 52 | trace/span/metric 빌더 |
| config | unspec | 1253 | 31 | TOML/YAML/INI/.env 파서 |
| table | §10.35 | 1112 | 78 | dataframe (CSV/TSV) |
| xlsx | §10.40 | 926 | 24 | XLSX over zip stored entries |
| schedule | unspec | 637 | 46 | cron parser/matcher |
| smtp | §10.30 | 401 | 33 | spec 명시: TLS/socket 미실행 (계획 단계) |
| email | unspec | 490 | 17 | MIME multipart |
| ocr | unspec | 1137 | 50 | PaddleOCR/Tesseract 명령 어댑터 |
| rpa | unspec | 1089 | 64 | desktop 자동화 명령 plan |
| scan | unspec | 671 | 42 | SANE scanimage plan |
| barcode | unspec | 718 | 37 | Code39/EAN-13 SVG 렌더러 |
| qr | unspec | 566 | 31 | QR matrix/SVG + qrencode plan |
| pdf | §10.42 | 805 | 14 | metadata + uncompressed text 추출 |
| image | §10.32 | 372 | 10 | spec 명시: 메타데이터만 (픽셀 디코드 미실행) |
| markdown | unspec | 836 | 8 | Deneb 포팅 |
| redact | §10.34 | 746 | 11 | Deneb 포팅 |
| security | §10.34 | 483 | 12 | SSRF 차단 |
| search | §10.34 | 357 | 17 | Deneb 포팅 in-memory FTS |
| media | §10.34 | 617 | 15 | magic-byte sniffing |
| tokenest | unspec | 305 | 16 | model-family token 추정 |
| httpretry | unspec | 322 | 12 | Deneb 포팅 |
| csv | §10.18 | 376 | 9 | RFC 4180 |
| jsonl | unspec | 117 | 8 | JSONL parse/append |
| kv | unspec | 459 | 40 | JSONL append-log |
| watch | unspec | 541 | 36 | polling file watcher |
| diff | unspec | 392 | 31 | LCS line diff |
| dialog | unspec | 644 | 29 | 파일 픽커 명령 plan |
| graphql | unspec | 224 | 21 | document/field 빌더 |
| gql_client | unspec | 77 | 10 | body-less runtime stubs (GraphQL HTTP/WS transport) |
| websocket | unspec | 338 | 15 | RFC 6455 frame encode/decode |
| ws_client | unspec | 109 | 13 | body-less runtime stubs (WebSocket connect/send/receive) |
| tar | unspec | 426 | 10 | ustar 인코드/디코드 |
| zip | §10.31 | 341 | 9 | stored entries만 (deflate 미실행) |
| sql | §10.28 | 665 | 43 | 빌더만 (실행은 db 위임) |
| db | §10.29 | 615 | 40 | spec 명시: 드라이버 의도적 부재 |
| db_driver | §10.29 | 177 | 10 | body-less runtime stubs (DB driver interface) |
| db_sqlite | §10.29 | 77 | 4 | body-less runtime stubs (SQLite driver) |
| db_pool | §10.29 | 80 | 7 | body-less runtime stubs (connection pool) |
| db_migration | §10.29 | 79 | 8 | body-less runtime stubs (schema migration) |
| db_orm | §10.29 | 169 | 7 | body-less runtime stubs (query builder / ORM) |
| db_pg | §10.29 | 68 | 4 | body-less runtime stubs (PostgreSQL driver) |
| db_mysql | §10.29 | 63 | 4 | body-less runtime stubs (MySQL driver) |
| xml | unspec | 391 | 9 | escape/tag/tokenize |
| template | unspec | 148 | 5 | escape/raw placeholder |
| i18n | unspec | 136 | 11 | locale/catalog/plural |
| ~~log~~ → 2.1 Production | — | — | — | — | (재분류, 아래 2.1 참조) |
| fmt | §10.22 | 926 | 45 | 본문 풍부, grapheme width / format spec |
| tui | §10.26 | 383 | 32 | retained frame buffer + ANSI |
| grid | §10.27 | 412 | 49 | Point/Size/Rect/Direction/Grid<T> |
| report | unspec | 400 | 20 | markdown/HTML report builder |
| metrics | §10.2 | 48 | 6 | labeled counter |
| shortid | §10.2 | 65 | 5 | prefix_NNNN 생성 |
| print | §10.38 | 461 | 23 | CUPS/Windows shell print plan |
| clipboard | §10.39 | 126 | 5 | pbpaste/pbcopy/wl-copy/xsel/AppleScript/PowerShell 위임 |

### 2.4 ⭐⭐ Spec-stub (스펙은 있으나 백엔드 미구현)

| 모듈 | spec | LOC | bodyless | 진단 |
|---|---|---|---|---|
| ~~**uuid**~~ | §10.13 | 22 | 100% | **정정 (2026-05-05)** — C runtime 7개는 존재하나 현재 HEAD에 `internal/llvmgen/stdlib_uuid_shim.go` 없음. 인터프리터 E2E는 통과. §2.2 Functional 표 참조. |
| ~~**regex**~~ | §10.9 | 88 | 10% | **정정 (2026-05-05)** — C runtime 7+개는 존재하나 현재 HEAD에 `internal/llvmgen/stdlib_regex_shim.go` 없음. 인터프리터 E2E는 통과. §2.2 Functional 표 참조. |

### 2.5 ⭐ Broken / Placeholder

| 모듈 | 문제 |
|---|---|
| ~~**log**~~ | **정정 (2026-05-05)** — Osty 본문은 spec 동급이나 C runtime `osty_rt_log_*` 부재, LLVM shim 부재. `eprintln` 폰백에 의존. §2.2 Functional 표 참조. |
| ~~**thread.Duration**~~ | **해결됨 (commit `23f4568c`)** — `thread.osty`가 `use std.time`로 `time.Duration` 단일 사용. self-host 체커는 `thread.Duration` qualified 이름을 builtin `Duration` 별칭으로 등록 (re-export). |

### 2.6 코어 인터페이스 / 마커 (별도 평가)

| 모듈 | spec | LOC | 비고 |
|---|---|---|---|
| capability | §20 | 177 | v0.6 canonical `Clock` / `Rng` / `Env` / `Fs` / `Net` / `Process` / `Console` interface surface, migration rules, exact `HostNet` / `HostProcess` adapters. `examples/v06_capabilities` + `examples/v06_capability_adapters` front-end/type-check proof. Ambient/legacy-global compiler semantics remain Phase 1 follow-up. |
| cmp | §10.1 | 27 | Equal / Ordered / Hashable interface 정의. 컴파일러 인지 |
| error | §10.1 / §7 | 96 | Error interface + BasicError + WrappedError + wrap/chain/rootCause |
| ref | §10.1 | 9 | `same<T>(a, b)` 단일 함수, declaration-only — 컴파일러 intrinsic 가정 |
| debug | §10.1 | 10 | `dbg(value)` — 컴파일러가 source-text 캡처 |
| hint | unspec | 25 | `black_box` 벤치 헬퍼 |
| runtime/raw | §19 | 55 | runtime sublanguage (privileged package only) |

## 3. 정정된 분류 카운트

| 등급 | 개수 | 비율 |
|---|---|---|
| ⭐⭐⭐⭐⭐ Production (LLVM E2E 통과) | 12 | 11% |
| ⭐⭐⭐⭐⭐ Production (declared, 매트릭스 5★ 미검증) | 8 | 8% |
| ⭐⭐⭐⭐ Functional | 16 | 15% |
| ⭐⭐⭐ Surface-rich | 62 | 58% |
| ⭐⭐ Spec-stub (백엔드 부재) | 0 | 0% |
| ⭐ Broken / Placeholder | 0 | 0% |
| 코어 인터페이스 (별도) | 8 | 7% |

**이전 매트릭스 주장**: 92/98 = 94% Production
**2026-05-01 재평가 주장**: 22/106 = 21% Production
**2026-05-05 HEAD 대조 후**: **12/106 = 11% 진정 5★ (LLVM 통과)** + 8/106 = 8% declared 5★ (매트릭스 등재되었으나 LLVM 미검증). 자세한 audit 은 §2.1.A 참조.

> **정정 노트 (2026-05-05)**: `internal/llvmgen/` 디렉토리가 현재 HEAD(`b640a833`)에 존재하지 않음. 매트릭스에 `stdlib_log_shim.go`, `stdlib_uuid_shim.go`, `stdlib_regex_shim.go` 등을 참조하며 5★로 승격한 내용은 아직 main에 merge되지 않은 워크트리 작업물로 확인됨. 현재 HEAD 기준으로 `log`는 C runtime 부재 + LLVM shim 부재, `uuid`/`regex`는 C runtime 존재하나 LLVM shim 부재.

차이의 원인:
1. spec 없는 unspec 모듈을 Production으로 셈 (62개 — 본문은 진정하나 spec 정의 없음 → Surface-rich로 강등)
2. 백엔드 미구현 모듈을 Production으로 셈 (uuid, regex)
3. placeholder 본문 모듈을 Production으로 셈 (log)
4. 매트릭스 §5 "줄 수 함정" 자체 경고와 모순되게 등급 매김

## 4. 진짜 갭 (긴급도 순)

### 🔴 Tier 0 — 즉시 수정해야 할 spec 위반

**남은 항목 없음 (2026-05-05 기준)**. 이전 4건 모두 해결 또는 해당 없음으로 확인됨.

| # | 모듈 | 문제 | 수정 |
|---|---|---|---|
| ~~1~~ | ~~thread~~ | ~~`pub struct Duration {}` (spec §10.20 위반)~~ | **완료 (commit `23f4568c`)**. `thread.osty`는 `use std.time` + `time.Duration` 단일 사용 |
| ~~2~~ | ~~primitives/int~~ | ~~`Int.s/ms/h/minutes/days/ns/us/weeks` 메서드 부재~~ | **완료** — Int 8개 + Float 8개 surface 선언됨, `primitive_arith_register.go:131` 8개 loop 등록. 매트릭스가 stale (`min`이라 잘못 표기 — spec은 `minutes`) |
| ~~3~~ | ~~log~~ | ~~`println` 폰백 + no-op handler~~ | **본문은 spec 동급으로 개선됨** — `TextHandler`/`JsonHandler`/`Logger` 체인 구현. 단, C runtime `osty_rt_log_*` 및 LLVM shim은 현재 HEAD에 부재. §2.2 Functional 참조. |
| ~~4~~ | ~~strings~~ | ~~`Contains/HasPrefix/Index` PascalCase 별칭 (부트스트랩 누수)~~ | **완료 (HEAD)** — `strings.osty`에서 PascalCase 별칭 미발견. 이미 제거되었거나 존재하지 않음. |


### 🟠 Tier 1 — LLVM bridge shim 부재 (C runtime은 존재)

| # | 모듈 | 영향 | 수정 |
|---|---|---|---|
| 5 | uuid | C runtime 7개 존재하나 LLVM bridge shim 부재 | `internal/llvmgen/stdlib_uuid_shim.go` 및 관련 LLVM bridge 생성 (현재 HEAD에는 해당 디렉토리 없음) |
| 6 | regex | C runtime 7+개 존재하나 LLVM bridge shim 부재 | `internal/llvmgen/stdlib_regex_shim.go` 및 관련 LLVM bridge 생성 (현재 HEAD에는 해당 디렉토리 없음) |

### 🟡 Tier 2 — 부분 구현 → 전체 미실행

| # | 모듈 | 남은 작업 |
|---|---|---|
| 7 | term | readKey/pollKey/readEvent 백엔드 (스펙 §10.25 명시, 헤더 자백) |
| 8 | keychain | Linux Secret Service backend |
| 9 | smtp | TLS/socket execution layer (spec §10.30 future work 명시) |
| 10 | image | 픽셀 디코드 (spec §10.32은 metadata만, 명시적 미래 작업) |
| 11 | zip | deflate (spec §10.31은 stored만 명시) |
| 12 | db | driver/runtime (spec §10.29는 명시적 미래 / §10.3 excluded) |
| 13 | net | IPv6 zone parsing 등 surface 디테일 검증 |

### 🟢 Tier 3 — 분류/문서 정정

| # | 항목 | 작업 |
|---|---|---|
| 14 | unspec 모듈 (62개) | LANG_SPEC_v0.5/10-standard-library/에 챕터 추가하거나 community-package 라벨 |
| 15 | OSTY_STDLIB_BODY_LOWER | default-on flip + 게이트 제거 (memory `project_stdlib_injection_hang`) |
| 16 | `Set` 빈약 / `Deque`/`PriorityQueue` 부재 | spec §10.6 확장 제안 (v0.6 minor) |

## 5. 평가 함정 카탈로그 (재정리)

이전 매트릭스 §5에서 적시한 함정 + 본 재평가에서 새로 발견한 함정.

### 5.1 줄 수 ≠ 깊이 (이전 함정, 유효)
math 42줄 / fs 51줄 / crypto 23줄 등은 declaration-only + runtime intrinsic. 줄 수 작아도 백엔드가 있으면 Production.

### 5.2 본문 있음 ≠ 동작 (신규 발견)
log.osty 133줄에 본문 풍부하나 `println` 폴백 + no-op handler — spec §10.10 미충족. **본문 진정성 검증 필수**.

### 5.3 spec 없음 ≠ 갭 없음 (신규 발견)
unspec 모듈 62개는 stdlib에 들어갔지만 LANG_SPEC에 등재 안 됨. surface API 안정성 보장 없음. **이전 매트릭스가 ⭐⭐⭐⭐⭐로 등재했던 거의 전부가 여기 해당**.

### 5.4 declaration-only ≠ 백엔드 있음 (신규 발견)
~~uuid/regex는 declaration-only인데 백엔드도 없음~~ → **정정 (2026-05-05)**. C runtime에는 `osty_rt_uuid_*` 7개, `osty_rt_regex_*` 7+개 존재. 단, 현재 HEAD에 LLVM bridge shim (`internal/llvmgen/stdlib_*_shim.go`)은 없음. **declaration-only 모듈은 `osty_rt_<name>_*` (C runtime) + shim 파일 존재 여부를 동시에 검증 필수**.

### 5.5 이름 함정 (이전 함정, 유효)
`process.osty`는 이제 exception helper + OS process facade이고, `cmd.osty`는 낮은 단계 command builder, `os.osty`는 실제 OS syscall wrapper다. 이름만 보고 카테고리 추정 금지.

### 5.6 spec 위반 중복 정의 (신규 발견)
~~`thread.Duration{}` 빈 struct가 `time.Duration`과 동시 존재~~ → 2026-05-02 재확인 결과 commit `23f4568c` (`refactor: thread.Duration stub 제거, std.time 단일 타입으로 통일`)에서 이미 제거됨. 매트릭스가 stale했던 사례. **다른 모듈도 같은 stale 가능성 — 매트릭스 작성 시점 vs 현재 git HEAD 차이 항상 검증 필요**.

## 6. 인상적인 디자인 디테일 (검증된 것만)

이전 매트릭스 §7 목록을 코드 검증 통과한 것만 남김:

1. **`crypto.constantTimeEq`** — runtime 19개에 `osty_rt_crypto_constant_time_eq` 확인됨. timing-safe 비교 진정함.
2. **`option/result.map2/map3/traverse`** — 본문 직접 확인. 진정한 함수형 composition.
3. **`fmt.visibleWidth` + `graphemeSlice`** — strings.osty의 UAX29 grapheme 테이블 + fmt에서 호출 확인.
4. **`http.Router` 본문** — http.osty:1539 matchRoutePattern 직접 확인. linear scan이지만 진짜 구현.
5. **`io.Reader/Writer` 인터페이스 hierarchy** — io.osty의 13개 인터페이스 + BytesReader/Buffer 구현체 본문 확인.
6. **strings BMH 검색** — strings.osty:619 bmhSkip 테이블 본문. needle.len()>=4 임계값.
7. **json RFC 8259** — json.osty:370 parseHex4 + 4-byte UTF-8 인코딩 본문.

**이전 매트릭스에서 자랑했지만 검증 실패한 것들**:
- ❌ `uuid.v7()` 2024 IETF draft 8 — C runtime은 존재하나 LLVM bridge shim 부재로 LLVM 호출 불가
- ❌ `log.Handler/TextHandler/JsonHandler` slog 동급 — placeholder 본문
- ⚠️ `testing.property` first-class — 표면 있고 testing_gen 본문 풍부, 백엔드 인터셉트 확인됨 (유효)

## 7. 다음 라운드 우선순위 (정정)

이전 매트릭스의 "stdlib은 거의 production, 다음 라운드는 db driver 추가" 결론은 **잘못됨**.

진짜 우선순위:

1. ~~Tier 0 spec 위반 4건 즉시 수정~~ → **완료 (2026-05-05)**. 남은 항목 없음.
2. **Tier 1 LLVM bridge shim 부재 2건** (`internal/llvmgen/stdlib_uuid_shim.go` + `stdlib_regex_shim.go` 생성, 1-2주)
3. **OSTY_STDLIB_BODY_LOWER default-on flip** (memory blocker `project_stdlib_injection_hang` 해소 후)
4. **unspec 62개 모듈 정책 결정** (stdlib 잔류 vs community package 분리)
5. **그 다음에야** db driver / smtp TLS 등 Tier 2 작업

**스스로에게 정직한 한 줄**: stdlib은 표면 야심 9/10이지만 검증된 production-ready는 12개(LLVM E2E 통과) + 8개(declared 5★). 매트릭스가 "94% Production"이라 부풀린 게 다음 라운드 우선순위 판단 자체를 왜곡했음. 정확한 카운트로 시작.
