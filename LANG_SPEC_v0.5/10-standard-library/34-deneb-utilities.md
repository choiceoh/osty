### 10.34 Deneb-Derived Utilities

- **Scope**: Osty stdlib spec — 10.34 Deneb-Derived Utilities
- **Type**: Standard library specification
These modules port Deneb subsystems that were intentionally implemented
without external dependencies. The goal is not a thin wrapper around Deneb
names; each module preserves the behavior that made the original useful.

#### `std.redact`

Best-effort secret redaction for data leaving an application boundary: logs,
chat transcripts, tool output, crash text, and HTTP metadata. It recognizes
vendor token prefixes, JWT-like tokens, sensitive key/value names, JSON fields,
bearer headers, URL userinfo, database connection strings, Telegram bot tokens,
Discord mentions, E.164-style phone numbers, and private-key blocks.
`RedactPolicy` controls masking shape while keeping redaction enabled by
default.

Parity audit: ported at Deneb-equivalent or stronger stdlib scope. The Osty
version keeps the original vendor/JWT/DB/header/query/form/JSON/env/private
key/Telegram/Discord/phone coverage, but implements it as explicit scanners
instead of regex tables. It also preserves Deneb's important distinction
between URL/form key-value secrets and ordinary prose such as
`password=is_not_form`.

#### `std.security`

Small security helpers that do not require a web framework:

- `checkUrl` / `isSafeUrl` block dangerous schemes, localhost, private
  networks, cloud metadata hosts, IPv4-mapped IPv6 private ranges, and numeric
  IPv4 bypasses such as decimal, hex, and octal forms
- `extractSafeLinks` strips Markdown links, finds bare URLs, deduplicates them,
  and keeps only SSRF-safe targets
- `sanitizeHtml` escapes HTML-significant characters
- `validateSessionKey` rejects empty, overlong, or control-character-bearing
  session keys

Parity audit: ported at Deneb-equivalent or stronger stdlib scope. It covers
the original session-key, HTML escaping, dangerous-scheme, localhost/private
network, cloud metadata, IPv4-mapped IPv6, numeric IPv4, IPv6 zone, file URL,
UNC path, Markdown-link stripping, dedupe, and balanced URL-tail cases.

#### `std.search`

Pure in-memory text search for small-to-medium local document sets. It performs
Unicode-aware tokenization, Hangul/CJK token handling, AND-first search with OR
fallback, BM25-style corpus scoring, title boosts, and snippet extraction.
The module exposes both a stateless `search` API and a Deneb-style explicit
`Index` with `upsert`, `remove`, `clear`, `searchIndex`, and `searchIndexOR`.

Parity audit: ported at Deneb-equivalent or stronger stdlib scope. The original
thread-safe Go index becomes an explicit immutable-ish value in Osty, which is
more testable and avoids hidden global state while preserving search behavior.

#### `std.markdown`

Lightweight Markdown and HTML text extraction: block classification, fenced
code extraction, inline-markup stripping, entity decoding, tag stripping, and a
Deneb-style HTML-to-Markdown converter for chat/document ingestion paths.
`HtmlResult` carries both extracted text and `<title>`, while `HtmlOptions`
enables Deneb's noise-stripping mode for `nav`, `aside`, `svg`, `iframe`, and
`form` elements. The converter strips script/style/noscript/head bodies,
preserves multibyte text, converts links/images/emphasis/inline-code/pre-code
blocks/blockquote/table/ordered-list/list/heading shapes, escapes Markdown
table cells, and decodes the typography and symbol entities Deneb commonly
normalized.

Parity audit: now near-full Deneb `htmlmd` surface parity for dependency-free
stdlib use. It does not copy Deneb's exact tokenizer/emitter implementation,
but it covers the practical behavior set from the full converter tests:
title extraction, script/style/noise suppression, malformed/truncated-tag
resilience, links, images with filename fallback, headings, unordered and
ordered lists, blockquotes, inline/pre code with language extraction, simple
tables with header separators and escaping, extended entities, and multibyte
alignment safety.

#### `std.media`

Magic-byte media detection and file-safety helpers. The detector covers common
image/audio/video/archive/document formats, ISO BMFF `ftyp` brands, OOXML ZIP
markers for DOCX/XLSX/PPTX, JSON/XML/HTML starts, binary sampling, archive-path
cleaning, human-size formatting, YouTube URL/id helpers, and `MEDIA:` token
parsing with fenced-code awareness, `file://` path normalization,
`[[audio_as_voice]]` detection, and generic `[[key=value]]` directive
stripping.

Parity audit: ported at Deneb-equivalent or stronger stdlib scope. It includes
the original PNG/JPEG/GIF/WEBP/WAVE/BMP/MP3/TIFF/Ogg/FLAC/WebM/PDF/ZIP/GZIP,
icon, ftyp MP4/M4A/AVIF/HEIC, OOXML, JSON/XML/HTML, fenced MEDIA, local path,
quoted path, backtick, dedupe, and directive cases.

#### `std.httpretry`

HTTP/LLM failure classification and retry decisions. It maps status codes and
provider-style codes/messages to `FailureReason`, then to retry actions such as
`RetryLater`, `RefreshCredentials`, `CompactContext`, `ReducePayload`,
`SwitchModel`, or `UpgradePlan`. The classifier includes the Deneb-style edge
cases for thinking-signature retries, long-context tiers, 402 quota/billing
disambiguation, generic 400 large-session context overflow, provider error
codes, large-session disconnect overflow, transient transport/TLS failures,
and action flags such as `rotate`, `refresh`, `compress`, `stripThinking`,
`retryOnce`, and `abort`.

Parity audit: ported at Deneb-equivalent or stronger stdlib scope, minus Go
error wrapping and JSON-body extraction which belong to host/runtime layers.
Provider codes are accepted directly through `classifyWithCode`.

#### `std.jsonl`

JSON Lines helpers for append-only local records: line parsing, multi-line
parsing, value/record encoding, append helpers, quick validation, and
head/tail compaction for long logs.

#### `std.tokenest`

Model-family-aware token estimation. It classifies Unicode scalar values into
Latin, Hangul, CJK, digit, space, punctuation, and other buckets, then applies
conservative per-family ratios for Claude, OpenAI, Gemini, and unknown models.
`Calibration`, `recordFeedback`, `correctionFactor`, and
`estimateWithCalibration` port Deneb's self-calibration idea without global
state or filesystem persistence.

Parity audit: ported at Deneb-equivalent or stronger stdlib scope. The
estimator keeps Deneb's script ratios, model-family resolution, byte divisor,
and EMA/clamped correction-factor behavior; persistence remains an application
or host-runtime concern.

#### `std.shortid`

Deterministic short id formatting using the Deneb-style `prefix_0000` shape.
The stdlib version keeps generator state explicit so callers can test and
thread it without hidden globals.

Parity audit: behavior-equivalent for formatting and wraparound, stronger for
stdlib use because state is explicit instead of package-global.

#### `std.metrics`

Lightweight labeled counters backed by a map. This mirrors Deneb's small
counter/snapshot utility without assuming Prometheus, histograms, or a scrape
pipeline.

Parity audit: behavior-equivalent for increment/add/get/snapshot/key joining,
with explicit value threading instead of lock-backed package globals.
