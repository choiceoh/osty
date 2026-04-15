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
