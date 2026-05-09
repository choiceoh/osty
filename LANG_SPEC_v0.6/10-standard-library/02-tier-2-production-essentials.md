### 10.2 Tier 2 (Production essentials)

- `std.json` — JSON encode/decode (reflection-based)
- `std.http` — HTTP client and server
- `std.time` — instants, durations, formatting, parsing, timezones
- `std.thread` — `taskGroup`, `collectAll`, `parallel`, `race`, `chan`,
  `select`, `isCancelled`
- `std.sync` — `Mutex`, `RwLock`, atomics
- `std.testing` — test framework (see §11)
- `std.env` — environment variables, command-line arguments
- `std.iter` — lazy iterator chains
- `std.regex` — regular expressions (RE2-based)
- `std.log` — structured logging
- `std.encoding` — base64, hex, URL encoding
- `std.crypto` — hashes, HMAC, secure random
- `std.uuid` — UUID generation and parsing
- `std.random` — pseudorandom number generation
- `std.os` — paths, process, signals
- `std.url` — URL parsing and building
- `std.math` — mathematical functions and constants
- `std.csv` — CSV read/write
- `std.table` — small dataframe helpers for CSV/TSV-scale table work:
  type inference, schema validation/coercion, typed sort, aggregation, join
- `std.xlsx` — stored-entry XLSX workbook encode/decode helpers with
  row/table adapters
- `std.compress` — gzip compression
- `std.term` — terminal mode, ANSI control, size, and key input
- `std.tui` — retained terminal frame buffers and diff rendering
- `std.grid` — `Point`, `Size`, `Rect`, `Direction`, and `Grid<T>` for
  board, map, and turn-based layouts
- `std.clipboard` — host clipboard text read/write for small workflow tools,
  implemented through the common platform clipboard commands
- `std.ai` — provider-neutral AI API request builders, headers, response
  parsers, structured tool call/result loops, model listing, embeddings, and SSE
  data-line helpers for OpenAI-compatible, OpenRouter, Anthropic, Gemini, and
  local runtimes
- `std.aiagents` — dependency-light agent/session/message, tool preset,
  safety-boundary, and compaction-policy primitives
- `std.redact` — dependency-free secret redaction for logs, transcripts, URLs,
  headers, JSON/env assignments, DB connection strings, JWTs, Telegram/Discord
  text, phone numbers, and private-key blocks
- `std.security` — SSRF-safe URL classification, safe link extraction, HTML
  escaping, and session-key validation
- `std.search` — explicit in-memory index plus stateless Unicode text search
  with AND→OR fallback, BM25-style scoring, and snippets
- `std.markdown` — Markdown block/fence extraction and Deneb-style safe
  HTML-to-Markdown conversion with title/noise/table/list/code handling
- `std.media` — magic-byte media sniffing, ftyp/OOXML ZIP detection,
  binary/text sampling, archive path cleaning, YouTube URL helpers, and
  `MEDIA:` token parsing
- `std.httpretry` — retry/backoff decisions, provider-code/message
  classification, quota/context disambiguation, and LLM-specific retry flags
- `std.webhook` — verified external callback intake for Stripe, GitHub, Slack,
  Supabase-style HMAC hooks, and Discord externally verified/Ed25519-modeled
  hooks, with replay-window checks, idempotency keys, and retry-safe dispatch
- `std.jsonl` — JSON Lines parsing and append-only record helpers
- `std.tokenest` — model-family-aware multilingual token estimation with
  explicit calibration state
- `std.shortid` — deterministic short id formatting
- `std.metrics` — lightweight labeled counters

#### 10.2.1 Tier 2 capability summary

The Tier 2 chapter listing is dense; the *capability shape* of each
module is summarized below. Use this when deciding which Tier 2
import is acceptable in a `#[pure]` / `#[pure]` /
capability-typed context.

| Module | Effectful methods (capability) | Pure helpers |
|---|---|---|
| `std.json` | (none) | encode / decode / parseValue |
| `std.http` | `HttpClient.*` (Net), `HttpServer.serve` (Net) | builders, codecs, router |
| `std.time` | `Clock.now/monotonic/sleep` | format/parse, duration math |
| `std.thread` | `taskGroup`, `parallel`, `chan`, `select` | (the primitives are themselves effects) |
| `std.sync` | `Mutex.lock`, `RwLock.*`, atomics | (none) |
| `std.testing` | (test-only effects) | assertions, golden, capability fakes |
| `std.env` | `Env.get/require/args/vars` | (none) |
| `std.iter` | (none) | All combinators |
| `std.regex` | (none) | All |
| `std.log` | (writes via ambient `Console`) | `Fields` builder |
| `std.encoding` | (none) | base64 / hex / URL encode-decode |
| `std.crypto` | `CryptoRng.randomBytes` | hashes / hmac / constantTimeEq |
| `std.uuid` | `uuid.v4(rng)`, `uuid.v7(clock, rng)` | parse / nil / toString |
| `std.random` | `Rng.*` | `random.seeded` |
| `std.os`/`std.process` | `Process.*` | `os.path.*` |
| `std.url` | (none) | parse / build / canonical |
| `std.math` | (none) | All |
| `std.csv` | (none) | encode / decode |
| `std.table` | (none) | All |
| `std.xlsx` | (none) | encode / decode |
| `std.compress` | (none) | gzip encode / decode |
| `std.term` | `Terminal.*` (via `Console.terminal()`) | ANSI sequence builders |
| `std.tui` | `Screen.present` | `Frame.*` builders, ANSI rendering |
| `std.grid` | (none) | All |
| `std.clipboard` | dispatches via ambient `Process` | (none) |
| `std.ai` | (caller drives via `Net`) | request builders, response parsers |
| `std.aiagents` | (none) | All |
| `std.redact` | (none) | All |
| `std.security` | (none) | URL classification, HTML escape |
| `std.search` | (none) | indexing, search, scoring |
| `std.markdown` | (none) | extraction, conversion |
| `std.media` | (none) | sniffing, parsing |
| `std.httpretry` | (none) | retry policy, classification |
| `std.webhook` | `Clock` (replay window), caller's `Env` (secret) | verification, dispatch |
| `std.jsonl` | (none) | parse / write |
| `std.tokenest` | (none) | estimation |
| `std.shortid` | (none) | formatting |
| `std.metrics` | (none) | counters |

This matrix is the inverse view of §20.18: §20.18 lists each
capability's stdlib clients; §10.2.1 lists each stdlib module's
capability dependencies. Both are maintained alongside chapter
edits; a divergence is a soundness issue that `osty audit
--capability-matrix` enumerates.
