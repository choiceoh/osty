## 10. Standard Library

This chapter is split into one file per subsection for easier navigation. The introduction below states the structuring principles; each `std.*` package then lives in its own file.

## 10. Standard Library

**v0.4 stub policy.** Standard-library protocol signatures are tracked
as checked `.osty` stubs before runtime parity. A stub may use a dummy
body, but it must parse, resolve, and type-check. If moving prose from
§10, §15, §16, or §17 into stubs exposes a signature ambiguity, that
ambiguity becomes a new language-decision gap; missing backend/runtime
lowering remains implementation backlog.

## Sections

- [§10.1 Tier 1 (Core)](./01-tier-1-core.md)
- [§10.2 Tier 2 (Production essentials)](./02-tier-2-production-essentials.md)
- [§10.3 Excluded from stdlib](./03-excluded-from-stdlib.md)
- [§10.4 Prelude](./04-prelude.md)
- [§10.5 Standard Numeric Methods](./05-standard-numeric-methods.md)
- [§10.6 Collection Methods](./06-collection-methods.md)
- [§10.7 Lazy Iterators (`std.iter`)](./07-lazy-iterators.md)
- [§10.8 JSON (`std.json`)](./08-json.md)
- [§10.9 Regular Expressions (`std.regex`)](./09-regular-expressions.md)
- [§10.10 Logging (`std.log`)](./10-logging.md)
- [§10.11 Encoding (`std.encoding`)](./11-encoding.md)
- [§10.12 Cryptography (`std.crypto`)](./12-cryptography.md)
- [§10.13 UUID (`std.uuid`)](./13-uuid.md)
- [§10.14 Random (`std.random`)](./14-random.md)
- [§10.15 Operating System (`std.os`)](./15-operating-system.md)
- [§10.16 URL (`std.url`)](./16-url.md)
- [§10.17 Math (`std.math`)](./17-math.md)
- [§10.18 CSV (`std.csv`)](./18-csv.md)
- [§10.19 Compression (`std.compress`)](./19-compression.md)
- [§10.20 Time Extensions (`std.time`)](./20-time-extensions.md)
- [§10.21 Bytes (`std.bytes`)](./21-bytes.md)
- [§10.22 Formatting (`std.fmt`)](./22-fmt.md)
- [§10.23 Network (`std.net`)](./23-net.md)
- [§10.24 HTTP (`std.http`)](./24-http.md)
- [§10.25 Terminal (`std.term`)](./25-term.md)
- [§10.26 Text UI (`std.tui`)](./26-tui.md)
- [§10.27 Grid (`std.grid`)](./27-grid.md)
- [§10.28 SQL (`std.sql`)](./28-sql.md)
- [§10.29 DB (`std.db`)](./29-db.md)
- [§10.30 SMTP (`std.smtp`)](./30-smtp.md)
- [§10.31 ZIP (`std.zip`)](./31-zip.md)
- [§10.32 Image (`std.image`)](./32-image.md)
- [§10.33 AI Agents (`std.aiagents`)](./33-aiagents.md)
- [§10.34 Deneb-Derived Utilities (`std.redact`, `std.security`,
  `std.search`, `std.markdown`, `std.media`, `std.httpretry`, `std.jsonl`,
  `std.tokenest`, `std.shortid`, `std.metrics`)](./34-deneb-utilities.md)
- [§10.35 Tables (`std.table`)](./35-table.md)
- [§10.36 AI API (`std.ai`)](./36-ai.md)
- [§10.37 Scanner Automation (`std.scan`)](./37-scan.md)
- [§10.38 Print (`std.print`)](./38-print.md)
- [§10.39 Clipboard (`std.clipboard`)](./39-clipboard.md)
- [§10.40 XLSX (`std.xlsx`)](./40-xlsx.md)
