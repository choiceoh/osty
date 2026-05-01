# STDLIB_MATRIX.md

Osty 표준 라이브러리 모듈별 production-ready 상태 매트릭스.

> **Scope**: `internal/stdlib/modules/*.osty` (Phase B / surface) +
> `internal/llvmgen/stdlib_*_shim.go` (Phase A / LLVM bridge) +
> `internal/backend/runtime/osty_runtime.c` 의 `osty_rt_*` 함수 (Phase A / C runtime).
>
> **기준일**: 2026-05-01 (post-keychain runtime audit). 어제 (#1018–#1029) 24시간
> 안에 stdlib 8개 모듈에 대규모 작업이 들어왔고 (http 26×, io 34×, net 1.5×,
> crypto runtime 1679 LOC, random/compress/math/os runtime 추가),
> osty resolver authoritative 마일스톤(#1013)도 같이 통과했다.
>
> **평가 모델 정정 (중요)**: `.osty` 줄 수만 보면 *bodyless declaration +
> runtime intrinsic* 패턴 모듈 (math, crypto, fs, env, random, os, compress, keychain)
> 들이 stub 으로 오해된다. 실제 평가는 *의도된 범위 ÷ 실제 커버리지* 기준
> 이어야 한다. 자세한 함정 케이스는 [§5](#5-평가-함정-osty-줄-수-모델의-한계) 참조.

## 1. 4-tier 분류

총 98 공개 모듈. 분류 기준:

- **⭐⭐⭐⭐⭐ Production**: surface + backend 모두 풀 커버. 외부 사용자에게 추천 가능
- **⭐⭐⭐⭐ Production-adjacent**: 사용 가능. 일부 helper 미흡 또는 surface 부풀림 다음 라운드
- **⭐⭐⭐ Functional**: 기본 사용 가능, 깊이는 부족
- **🚧 Skeleton / Empty**: 작업 안 됨

### ⭐⭐⭐⭐⭐ Production (92 / 98 = 94%)

| 모듈 | Surface (LOC) | Backend | 비고 |
|---|---|---|---|
| strings | 7242 | string 17 + bytes 29 runtime | 압도적. UTF-8 / casefold / normalize / grapheme |
| http | 1588 | net 40 runtime | Router + Cookie + MediaType + Form + Query + dispatch |
| ai | 1793 | pure Osty + http/json | OpenAI-compatible / OpenRouter / Anthropic / Gemini / local-runtime chat requests, headers, structured tool calls/results, response parsing, models, embeddings, SSE data helpers |
| aiagents | 736 | pure Osty | Deneb-derived agent/session/message types, tool presets, safety boundary, RankLines / TruncateHeadTail compaction |
| aidev | 701 | pure Osty | AI coding-loop data model: SourceFile / SourceSpan / Diagnostic / Patch / FixContext, source excerpts, non-overlap validation, safe text patch application |
| aidev.osty | 201 | pure Osty + aidev | Osty-specific AI repair hints: source-habit classification, diagnostic hints, Osty fix-context summaries |
| aidev.prompt | 267 | pure Osty + aidev/redact/tokenest | Prompt material builders: compact/redacted fix prompts, review prompts, trust-labeled sections, token estimates, patch contract rules |
| aidev.corpus | 366 | pure Osty + aidev/jsonl | JSONL-friendly repair corpus records: failing cases, repair attempts, before/after summaries, residual diagnostics, and corpus stats |
| aidev.verify | 449 | pure Osty + aidev | Verification contract: command plans, host result records, required-check accounting, accept/retry/reject decisions, and report summaries |
| aidev.workflow | 241 | pure Osty + aidev/prompt/corpus/verify | End-to-end AI coding workflow records: diagnostics to prompt, patch proposal, verification report, corpus append, and run summaries |
| redact | 746 | pure Osty | Deneb-derived secret redaction: vendor tokens, JWTs, URL query/form distinction, JSON/env/key-value, DB URLs, URL userinfo, Telegram/Discord/phone/private-key handling |
| media | 617 | pure Osty + bytes | Deneb-derived MIME sniffing: magic bytes, icon/ftyp/OOXML ZIP, binary sampling, archive path safety, YouTube + MEDIA token helpers with file:// normalization/directive stripping |
| security | 483 | pure Osty + url/net | Deneb-derived SSRF-safe URL checks, numeric IPv4 bypass detection, balanced URL-tail cleanup, safe link extraction, HTML escape, session-key validation |
| search | 357 | pure Osty | Deneb-derived stateful in-memory index + stateless search: upsert/remove/OR, Unicode tokenization, AND→OR fallback, BM25-style scoring, snippets |
| markdown | 836 | pure Osty | Deneb-derived markdown + full-ish htmlmd conversion: Result/Options, title, noise stripping, pre/code, tables, ordered lists, links/images, emphasis, entities, multibyte-safe scanners |
| report | 400 | pure Osty | Markdown/HTML report builder: summary cards, sections, aligned tables, chart CSV/JSON export |
| tokenest | 305 | pure Osty + bytes | Deneb-derived model-family-aware token estimator with explicit calibration state for Claude/OpenAI/Gemini/default |
| httpretry | 322 | pure Osty | Deneb-derived retry/backoff decisions and LLM/provider error classification with provider codes, large-session disconnect handling, context/billing/rate-limit disambiguation, and action flags |
| schedule | 637 | pure Osty | cron parser/matcher, interval/daily next-run planning, task state, due/tick helpers, and retry backoff |
| jsonl | 117 | pure Osty + json | Deneb-style JSON Lines parse/append/compact helpers |
| kv | 459 | pure Osty + fs/json | JSONL append-log local KV: file-backed cache/history/settings store, string-first typed getters/setters, tombstone delete, compact rewrite, JSON convenience helpers |
| redis | 1439 | pure Osty + net/bytes | Redis integration helpers: Config/auth/DB selection, RESP2 byte-stream reader, command builders for strings/hash/list/set/pubsub/scan/streams, pipelines/transactions, typed reply converters, and convenience client helpers |
| shortid | 65 | pure Osty | Deneb-style `prefix_0000` deterministic short id generator |
| metrics | 48 | pure Osty | Deneb-style labeled counter snapshots without a metrics backend |
| observability | 602 | pure Osty + log/metrics/json/redact | Backend-neutral telemetry layer: resource/trace/span/breadcrumb/metric/event builders, traceparent+baggage headers, redacted JSON/log records, counter snapshots |
| sentry | 650 | pure Osty + observability/http | Sentry DSN parsing, envelope/auth/header generation, event/exception/transaction payloads, HTTP request/send helpers, trace propagation |
| net | 1084 | net 40 runtime | TCP/UDP, IPv4/IPv6, parseSocketAddr, tcpListen |
| fmt | 926 | (surface heavy) | graphem-aware width, format spec engine |
| json | 662 | (parser self) | generic encode<T> / decode<T>, UTF-8 / surrogate pair, value constructors |
| config | 1253 | pure Osty + json/fs/env | TOML / YAML / INI / .env parse, read/load/readAuto/loadAuto, repeated objects, default merge, schema coercion + validation |
| table | 1112 | pure Osty + csv | small dataframe: CSV/TSV read-write, type inference, schema validation/coercion, typed sort, group aggregation, indexed join |
| xlsx | 926 | pure Osty + zip/table | XLSX workbook encode/decode over stored ZIP entries, inline/shared strings, row/table adapters |
| url | 592 | — | parse / join / format |
| io | 541 | shim 171 | Reader/Writer 프로토콜, Buffer, copyN, readExact |
| collections | 509 | list 68 + map 42 + set 17 | 30+ List 메서드, groupBy, windowed, zip3 |
| email | 461 | pure Osty + encoding | Address / Message / MIME multipart / attachment base64 / SMTP DATA helpers |
| db | 554 | pure Osty + sql | Config / DSN / pool / tx options / result rows / migration planning helpers |
| polyglot | 2319 | pure Osty + os/env/fs | 두 언어 repo 표준 레일: Language / Component / Boundary / Workspace, BoundaryContract, ExecutionPolicy, doctorRun/check runner, Go+Rust/Osty+Go/Rust/Python/Node preset, build/test plan, Process/C ABI/shared-file boundary 검증, cwd/env 복원 실행, env/artifact/schema 계약 audit |
| grid | 412 | pure Osty | Point / Size / Rect / Direction / row-major Grid<T> |
| gui | 2004 | pure Osty + fs/os/json | retained GUI core: geometry / theme / flex+stack layout / render commands / HTML document renderer / browser event bridge / SVG renderer / browser document write/open launch helpers / browser event state application / pointer-key routing / focus / accessibility audit / snapshot diff |
| dialog | 644 | pure Osty + os | file picker / multi-file picker / folder picker / save dialog command plans and runners for macOS AppleScript, Windows PowerShell, Zenity, KDialog, custom commands |
| tar | 408 | pure Osty + bytes | ustar encode/decode/list/extract + checksum validation |
| sql | 417 | pure Osty | identifier quoting / literals / placeholders / SELECT-INSERT-UPDATE-DELETE builders |
| xml | 391 | pure Osty | escape / unescape / tag builder / tokenizer |
| tui | 383 | pure Osty + term | retained frame buffers, ANSI render/diff helpers |
| image | 372 | pure Osty + bytes | PNG / JPEG / GIF / BMP / WebP format + dimension metadata parser |
| ocr | 1136 | pure Osty + os/fs/json/path | PaddleOCR v5 / Tesseract command adapters, fast/accurate presets, batch JSON ingestion, confidence review queues, search, key-value extraction, RAG-friendly chunks |
| rpa | 1089 | pure Osty + os/strings | Desktop RPA command plans: mouse move/click/drag/scroll, keyboard typing/hotkeys/paste, window query/focus/wait, script repeat/delay, macOS cliclick+osascript / Linux xdotool / Windows PowerShell adapters |
| scan | 670 | pure Osty + os/fs/image/ocr/path | SANE `scanimage` / custom scanner command planning, device-list parsing, batch page paths, image metadata probe, manifest generation, scan→OCR indexed document handoff |
| barcode | 709 | pure Osty + os | Code39 / EAN-13 / UPC-A native renderers, SVG/ASCII output, scanner command adapters, ZBar/ZXing output parsing, inventory/shipment/access label helpers |
| qr | 564 | pure Osty + os | QR payload builders, matrix/SVG render helpers, qrencode generation plans, ZBar/ZXing recognition plans and result parsing |
| smtp | 358 | pure Osty + email/encoding | SMTP commands / AUTH payloads / reply parsing / transaction scripts |
| result | 357 | — | composition (map / mapErr / and / or / collect) |
| option | 356 | — | flatten / transpose / traverse / map2 / map3 |
| csv | 376 | — | options-driven, header-aware decode plus TSV convenience wrappers |
| diff | 392 | pure Osty | line-oriented LCS diff, structured changes/stats, hunk grouping, unified diff rendering, apply helpers |
| encoding | 329 | pure Osty + bytes | Base64 / Base64Url / Hex / URL percent-encoding 구현 |
| zip | 327 | pure Osty + bytes | ZIP stored-entry encode/decode/list/extract + CRC32 |
| term | 320 | host-backed + pure ANSI | terminal mode/size/input declarations + ANSI sequence builders |
| websocket | 322 | pure Osty + crypto/encoding | RFC 6455 accept key + handshake headers + frame encode/decode |
| pdf | 805 | pure Osty + bytes | PDF header/version/Info metadata, page count, object inventory, uncompressed text extraction |
| cli | 260 | pure Osty + env | flag / option spec, parse / parseEnv / usage |
| cmd | 214 | os shim + runtime | command builder, cwd/env/timeout, captured output, POSIX shell escaping, pipeline rendering |
| watch | 535 | pure Osty + fs/time/cmd | polling file watcher: fs.watch snapshot parsing, typed create/modify/remove diff, debounce, hidden/build-dir filters, transform/sync command tasks |
| clipboard | 125 | pure Osty + os | host clipboard text read/write via pbpaste/pbcopy, Wayland, X11, AppleScript, PowerShell |
| graphql | 224 | pure Osty | document / field / argument builders + request JSON body encoding |
| supabase | 1214 | pure Osty + http/json/env | Supabase PostgREST/Auth/Storage/Functions/GraphQL request builders, env-backed config, API-key/session headers, filters, Prefer/count headers, typed select pages, Content-Range metadata, signed Storage URLs, storage listing/search, object URLs, and response error helpers |
| github | 1127 | pure Osty + http/json/crypto | GitHub REST request builders for issues, PRs, Actions, releases, release asset upload, typed error parsing, and webhook HMAC/constant-time verification |
| webhook | 696 | pure Osty + http/crypto/json/time | Stripe/GitHub/Slack/Supabase-style HMAC verification, Discord Ed25519-modeled externally-verified path, replay-window checks, idempotency store, and retry-safe dispatch |
| cloudflare | 1437 | pure Osty + http/json/crypto | Cloudflare edge request builders: Turnstile siteverify, KV namespace/value/bulk helpers, R2 bucket/temp-credential management plus S3 SigV4 object requests, Queues push/pull/ack/batch/purge, DNS records, cache purge, Worker invoke/script helpers |
| template | 148 | pure Osty | escaped/raw `{{name}}` 렌더링 + HTML escape / stripTags |
| i18n | 136 | pure Osty | Locale / MessageCatalog / fallbackTags / pluralCategory / placeholder format |
| char | 219 | — | Unicode / ASCII methods |
| iter | 118 | — | iteration 프로토콜 |
| bytes | 113 | bytes 29 runtime | byte 슬라이스 조작 |
| sync | 96 | mu 5 + rmu 5 | Mutex<T> / Locked<T> type-state, RwLock, Atomic |
| error | 96 | — | Error interface + wrap / rootCause cause chain |
| time | 89 | (검색 필요) | Duration / Instant / Zone / Weekday / ZonedTime |
| testing | 88 | test 5 + bench 7 | assert + benchmark + snapshot + property runner entrypoints |
| testing_gen | 168 | test property lowering + runtime | property generators: int/intRange/bool/float/char/byte/asciiString/list/listOfSize/option/result/pair/triple/oneOf/oneOfGens/map/filter/constant |
| log | 133 | — | Level + Handler + TextHandler / JsonHandler + value/level constructors — slog 동급 |
| regex | 74 | — | compile / matches / find / findAll / replace / split |
| path | 172 | pure Osty + runtime boundary | lexical join/split/extension/dirname/basename/isAbsolute/separator; absolute/canonical stay host-runtime backed |
| os | 43 | shim + runtime | exec / execShell / execWith / execShellWith / exit / pid / hostname / onSignal |
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

### ⭐⭐⭐⭐ Production-adjacent (6 / 98 = 6%)

| 모듈 | Surface (LOC) | 갭 |
|---|---|---|
| gui.qtquick | 243 | Qt Quick/QML native app backend MVP. `libosty_qt` bridge, runtime diagnostics, reload/import-path helpers, `osty gui doctor qtquick`; bundle/deploy 연계는 후속 |
| gui.webview2 | 292 | Windows WebView2 C ABI shim. Safe wrapper / scaffold / runtime diagnostics landed; local virtual-origin assets, console-log event bridge, Osty→Web command channel, and handle-lifetime hardening added; Windows smoke and packaged linker flow still pending |
| print | 461 | 기본 프린터 / PDF / 이미지 인쇄 표면. CUPS `lp`/`lpr`, Windows shell print, custom command plan/exec, 프린터 조회와 옵션 검증은 추가됨; host spooler별 세부 기능 편차는 후속 |
| keychain | 79 | OS credential store facade. macOS Keychain + Windows Credential Manager runtime/LLVM bridge landed; Linux Secret Service backend pending |
| secrets | 38 | API-key/token convenience facade over `std.keychain`; availability follows keychain backend |
| compress | 11 | gzip 만 (zstd / deflate 추가 가능). Phase A shim 199 |

### 🚧 실제 미구현 / 갭

기존 모듈 안에도 declaration-only surface 가 많다. 이 중 `fs/env/math/crypto/io/thread/time/term/os/random/uuid` 처럼
runtime 또는 LLVM bridge 로 실제 동작하는 표면은 stub 로 세지 않는다. 하지만 아래는 실제 갭이다:

| 구분 | 갭 |
|---|---|
| 없는 모듈 | 없음 (`db`, `smtp`, `zip`, `image`, `xlsx`, `pdf`, `schedule`, `dialog`, `watch`, `scan`, `rpa`, `barcode`, `qr`, `print`, `clipboard`, `keychain`, `secrets`, `supabase`, `github`, `observability`, `sentry`, `webhook`, `cloudflare` surface 는 존재) |
| 남은 runtime/deep 기능 | `db driver/runtime`, `smtp TLS/socket execution`, `zip deflate`, `image pixel decode`, `keychain Linux Secret Service` |
| 부분 구현 | `compress` 는 gzip 만 있음. deflate/zstd 계열 없음 |
| 문서/코드 드리프트 | 일부 README/매트릭스 문구가 과거 G18 stub 정책을 아직 과장해서 남김 |

## 2. 카테고리별 데모 가능성

| 데모 종류 | 가능성 | 필요 모듈 |
|---|---|---|
| HTTP 웹 서버 | ✅ 즉시 가능 | http + io + json + net |
| CLI 도구 | ✅ 즉시 가능 | os + fs + io + fmt + env |
| 설정 파일 로딩 | ✅ 즉시 가능 | config + json + fs + env |
| 데이터 처리 (CSV / TSV / JSON / XLSX / tables) | ✅ 즉시 가능 | table + csv + xlsx + json + collections + iter + fmt |
| 파일 변환기 / 아카이브 | ✅ 가능 | fs + io + tar + compress (gzip 만) + fmt |
| 파일 변경 감시 / 자동 변환 | ✅ 가능 | watch + fs + time + cmd |
| 클립보드 업무 도구 | ✅ 가능 | clipboard + os (host clipboard command adapters) |
| 암호화 / 해시 | ✅ 가능 | crypto (sha256 / hmac / random / constantTimeEq) |
| 랜덤 게임 / 시뮬레이션 | ✅ 가능 | random (seeded RNG) + math |
| 수치 계산 | ✅ 가능 | math (sin / cos / log / exp / sqrt / pow / floor / ceil / clamp / hypot) |
| 시간 처리 | ✅ 가능 | time (Duration / Zone / parse / sleep) |
| Regex 매칭 | ✅ 가능 | regex (compile / find / replace / split) |
| Property-based testing | ✅ 가능 | testing (Gen<T> / property / propertySeeded) |
| Structured logging | ✅ 가능 | log (Handler / TextHandler / JsonHandler) |
| Error reporting / tracing / performance monitoring | ✅ 가능 | observability + sentry + http/log/metrics/redact (trace headers, breadcrumbs, transactions, Sentry envelopes) |
| 동시성 (구조적) | ✅ 가능 | thread (spawn / race / chan / select / cancel) |
| 예약 작업 / 재시도 | ✅ 가능 | schedule (cron / interval / daily / due tick / retry backoff) |
| HTML 템플릿 | ✅ 가능 | template (escaped/raw placeholder render) |
| XML 처리 | ✅ 가능 | xml (escape / tag build / tokenize) |
| 다국어 메시지 | ✅ 가능 | i18n (locale / catalog / placeholder / plural category) |
| WebSocket handshake/frame | ✅ 가능 | websocket (accept key / headers / frame encode/decode) |
| GUI 앱 코어 | ✅ 가능 | gui (retained node tree + layout + event routing + render-command backend contract + 기본 HTML/SVG 렌더러 + 브라우저 이벤트 브리지 + input/change 상태 반영 + HTML 파일 저장/기본 브라우저 실행 헬퍼) |
| GraphQL 요청 생성 | ✅ 가능 | graphql (document builder / variables JSON body) |
| AI API 연동 | ✅ 가능 | ai + http + json (OpenAI-compatible / OpenRouter / Anthropic / Gemini chat, structured tool calls/results, models, embeddings) |
| API 키 / 토큰 저장 | ✅ macOS/Windows 가능 | keychain + secrets (macOS Keychain / Windows Credential Manager; Linux Secret Service pending) |
| 이메일/MIME 생성 | ✅ 가능 | email (address / MIME multipart / SMTP DATA helpers) |
| SMTP 트랜잭션 조립 | ✅ 가능 | smtp (EHLO / STARTTLS plan / AUTH / MAIL-RCPT-DATA / reply parsing) |
| TAR 아카이브 | ✅ 가능 | tar (ustar encode/decode/list/extract) |
| ZIP 아카이브 | ✅ 가능 | zip (stored-entry encode/decode/list/extract, deflate 는 runtime 후보) |
| 이미지 메타데이터 | ✅ 가능 | image (PNG / JPEG / GIF / BMP / WebP dimensions) |
| PDF 검사 / 텍스트 추출 | ✅ 가능 | pdf (metadata / pages / objects / uncompressed text streams) |
| OCR 엔진 연결 / 결과 분석 | ✅ 가능 | ocr (PaddleOCR v5 / Tesseract 실행 계획, JSON/TSV normalize, confidence/search/key-value/chunk helpers) |
| 스캔→OCR 문서 자동화 | ✅ 가능 | scan + ocr + image + fs/os (SANE/custom scanner command plan, scanned page metadata, OCR batch/index/manifest) |
| Desktop RPA / 반복 업무 자동화 | ✅ 가능 | rpa (마우스 이동/클릭/드래그/스크롤, 키 입력/hotkey/paste, 창 검색/focus/wait, 반복 스크립트, macOS/Linux/Windows command plan) |
| QR / 바코드 문서 | ✅ 가능 | qr + barcode (QR payload/qrencode 계획, ZBar/ZXing 인식 파싱, Code39/EAN-13/UPC-A SVG 렌더링) |
| SQL 쿼리 조립 | ✅ 가능 | sql (identifier quoting / value literals / dialect placeholders / CRUD builders) |
| DB 설정/마이그레이션 계획 | ✅ 가능 | db + sql (DSN / pool / tx options / result rows / migration helpers) |
| Supabase 앱 백엔드 연결 | ✅ 가능 | supabase + http/json/env/secrets (env-backed clients, PostgREST filters/mutations/RPC/count metadata, Auth password-token requests, Storage object URLs/uploads/signed URLs/listing, Edge Function/GraphQL requests) |
| GitHub 개발 자동화 | ✅ 가능 | github + http/json/crypto (issue/PR request builders, Actions dispatch/runs/jobs/artifacts, release/create/upload, webhook HMAC verification) |
| 외부 서비스 webhook intake | ✅ 가능 | webhook + http/crypto/json/time (Stripe/GitHub/Slack/Supabase-style HMAC verify, replay-window checks, idempotency, retry-safe dispatch) |
| Cloudflare edge 보완 | ✅ 가능 | cloudflare + http/json/crypto/secrets (Turnstile bot 방어, KV, R2 파일 저장/SigV4 object requests, Queues durable queue, DNS, cache purge, Worker 호출) |
| 두 언어 앱 / repo 경계 | ✅ 가능 | polyglot + os + env + fs + `osty scaffold polyglot` (Osty+Go/Rust/Python/Node 등 역할 분리, 빌드/테스트/경계 계약, doctorRun/check runner) |
| AI agent shell / chat mode | ✅ 가능 | ai + aiagents + http/json/log/thread |
| Local KV / 설정 DB | ✅ 즉시 가능 | kv + fs + json (JSONL append-log, compact, typed getters/setters) |
| AI code assist loop | ✅ 가능 | aidev + aidev.osty + aidev.prompt + aidev.corpus + aidev.verify + aidev.workflow (diagnostic model / source spans / fix context / patch validation / Osty source-habit hints / compact prompt material / repair corpus records / verification plans and decisions / end-to-end run records) |
| Agent-safe logs/transcripts | ✅ 가능 | redact + security + tokenest + aiagents |
| Local document search | ✅ 가능 | search + markdown + media + jsonl |
| 소스/텍스트 변경 리뷰 | ✅ 가능 | diff + fs + fmt/report (structured line changes, hunks, unified diff output) |
| 사람이 읽는 리포트 산출물 | ✅ 가능 | report + markdown/jsonl/csv |
| PDF/이미지 인쇄 | ✅ 가능 | print + fs/media/os (기본 CUPS `lp`, `lpr`, Windows shell print, custom command plan) |
| LLM retry/compaction loop | ✅ 가능 | ai + httpretry + schedule + tokenest + aiagents |

## 3. 진짜 약점 (남은 런타임 / 딥 기능)

기존 모듈 대부분은 production-ready 또는 runtime-backed. 큰 갭은 이제 *없는 모듈*보다 runtime-backed 실행/딥 codec 쪽이다:

| 남은 갭 | 영향 | 우선순위 |
|---|---|---|
| db driver/runtime | 실제 DB 실행 (sqlite / postgres wrap 필요) | 높음 (실용 어플리케이션 핵심) |
| smtp TLS/socket execution | 실제 SMTP 전송 / TLS | 중간 |
| keychain Linux Secret Service | Linux desktop credential store support | 중간 |
| zip deflate | 압축 ZIP 호환성 | 낮음 |
| image pixel decode | 실제 픽셀 디코딩 / 변환 | 낮음 |

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
- 72 모듈은 Phase A + Phase B 둘 다 충실 (strings, http, ai, aiagents, aidev, aidev.osty, aidev.prompt, aidev.corpus, aidev.verify, aidev.workflow, redact, media, security, search, markdown, report, tokenest, httpretry, webhook, schedule, jsonl, kv, shortid, metrics, observability, sentry, net, fmt, json, config, table, xlsx, url, io, collections, email, db, polyglot, grid, gui, dialog, tar, sql, xml, tui, image, pdf, ocr, rpa, scan, barcode, qr, smtp, result, option, csv, diff, encoding, zip, term, websocket, watch, clipboard, graphql, github, supabase, cloudflare, template, i18n, char, iter, bytes)
- 5 모듈은 Phase A 충실 + Phase B declaration-only (env, random, os, crypto, compress)
- keychain/secrets 는 macOS/Windows Phase A+B 연결 완료, Linux Secret Service backend 대기
- fs 는 Phase A 충실 + 확장된 tool-facing declaration surface (walk/glob/watch/atomicWrite/lockFile/hashFile/copyDir/diffFiles)
- print 는 기존 `std.os` host process bridge 위에서 CUPS/Windows/custom spooler 실행 계획을 제공한다. 실제 출력은 가능하지만 프린터별 capability discovery 는 production-adjacent 로 남긴다.
- 나머지는 의도된 범위에서 surface 만으로 완성 (cli, math, cmp, hint, debug, ref, process, log, time, error, sync, thread, regex, testing, uuid)

## 5. 평가 함정: `.osty` 줄 수 모델의 한계

이전 평가 (4 회) 가 `.osty` 줄 수만 보고 잘못된 결론을 냈다. 함정 패턴:

### 5.1 Bodyless declaration + runtime intrinsic

`math.osty` 42 줄, `fs.osty`, `crypto.osty` 23 줄 모두 *각 함수가 한 줄 declaration*:

```osty
pub fn sin(x: Float) -> Float
pub fn read(path: String) -> Result<Bytes, Error>
pub fn walk(root: String) -> Result<List<String>, Error>
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
11. **`ai.chatHttpRequest` + `sendChat` + `ToolCall` / `ToolResult` + provider별 parser** — OpenAI-compatible / OpenRouter / Anthropic / Gemini / local runtime 연동과 tool loop 를 앱마다 JSON 문자열로 재발명하지 않아도 됨
12. **`aiagents.ToolPreset` + `SafetyPolicy` + `rankLines`** — Deneb 의존성 없는 agent runtime 규칙을 stdlib 표면으로 포팅. chat-only/web-only 모드와 로그/도구출력 compaction trust boundary 를 앱마다 재발명하지 않아도 됨
13. **`aidev.SourceSpan` + `Patch` + `FixContext` + `prompt.PromptBundle` + `corpus.RepairAttempt` + `verify.VerifyReport` + `workflow.WorkflowRun`** — AI 코드 작성 루프를 문자열 로그가 아니라 진단/소스 범위/수정 계획/패치 검증/프롬프트 재료/수리 코퍼스/검증 판정/전체 실행 기록이라는 구조화된 stdlib 계약으로 다룸
14. **`redact` + `security` + `search` + `media` + `tokenest`** — Deneb 에서 "의존성 없이 어렵다" 쪽을 자체 구현한 부분을 stdlib 로 승격. secret 누출 방지, SSRF 우회 차단, local FTS 대체, magic-byte media sniffing, multilingual token budget 계산을 앱마다 재발명하지 않아도 됨

## 8. 다음 라운드 후보

stdlib 자체는 거의 production. 다음 우선순위:

1. **runtime-backed 실행 추가** (db driver/runtime / smtp TLS+socket execution) — pure surface 다음 단계
2. **codec 깊이 추가** — compress deflate/zstd, zip deflate, image pixel decode
3. **Phase B surface 부풀리기** — random / crypto / compress 는 Phase A 풍부한데 Phase B helper 가 declaration 위주. user-friendly wrapper (예: `crypto.sha256Hex(data)`, `random.shuffle(list)`) 추가
4. **스펙 문서 동기화** — `LANG_SPEC_v0.5/10-standard-library/*.md` 가 24 시간 sprint 진척 따라잡았는지 확인
