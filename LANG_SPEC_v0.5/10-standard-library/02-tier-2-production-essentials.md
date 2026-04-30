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
- `std.compress` — gzip compression
- `std.term` — terminal mode, ANSI control, size, and key input
- `std.tui` — retained terminal frame buffers and diff rendering
- `std.grid` — `Point`, `Size`, `Rect`, `Direction`, and `Grid<T>` for
  board, map, and turn-based layouts
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
- `std.jsonl` — JSON Lines parsing and append-only record helpers
- `std.tokenest` — model-family-aware multilingual token estimation with
  explicit calibration state
- `std.shortid` — deterministic short id formatting
- `std.metrics` — lightweight labeled counters
