### 10.22 Formatting (`std.fmt`)

String formatting utilities beyond `{}` interpolation: number base
conversion, padding and alignment, fixed-precision printing, escaped
string rendering, compact human-readable numbers, and composite value
rendering.

```osty
use std.fmt

// Number bases
fmt.hex(255)             // "ff"
fmt.hex(255, upper: true)  // "FF"
fmt.hexPrefixed(-255)    // "-0xff"
fmt.octal(8)             // "10"
fmt.binary(10)           // "1010"

// Padding and alignment
fmt.padLeft("hi", 6)         // "    hi"
fmt.padLeft("hi", 6, '0')    // "0000hi"
fmt.padRight("hi", 6)        // "hi    "
fmt.center("hi", 6)          // "  hi  "
fmt.align("hi", 6, "right")  // "    hi"

// Fixed precision (mirrors Float.toFixed)
fmt.toFixed(3.14159, 2)      // "3.14"
fmt.toFixed(3.1,    4)       // "3.1000"

// Scientific notation
fmt.scientific(0.000123)     // "1.23e-4"
fmt.scientific(0.000123, precision: 1)  // "1.2e-4"

// Template formatting
fmt.format(r"{} scored {1:>4}", ["Ada", "42"])  // "Ada scored   42"
fmt.format(r"{0:.5}", ["abcdef"])               // "abcde"

// Escaped strings and compact numbers
fmt.quote("a\nb")         // "\"a\\nb\""
fmt.compact(1234000)      // "1.2M"
fmt.bytesFixed(1536, 1)   // "1.5 KiB"
fmt.metricBytes(1500, 1)  // "1.5 KB"

// Composite rendering
fmt.join(["a", "b", "c"], sep: ", ")          // "a, b, c"
fmt.join(["a", "b", "c"], sep: ", ", last: " and ")  // "a, b and c"
fmt.repeat("ab", 3)          // "ababab"
fmt.truncate("hello world", 8)          // "hello..."
fmt.truncate("hello world", 8, suffix: "…")  // "hello w…"
fmt.truncateMiddle("abcdef", 5)         // "a...f"
```

API:

```
// Integer base formatting
fmt.intToBase(n: Int, base: Int) -> String
fmt.hex(n: Int, upper: Bool = false) -> String
fmt.hexUpper(n: Int) -> String
fmt.octal(n: Int) -> String
fmt.binary(n: Int) -> String
fmt.bin(n: Int) -> String
fmt.oct(n: Int) -> String
fmt.inBase(n: Int, base: Int) -> String   // base in 2..36
fmt.prefixedBase(n: Int, base: Int, prefix: String, upper: Bool = false) -> String
fmt.hexPrefixed(n: Int, upper: Bool = false) -> String
fmt.binaryPrefixed(n: Int) -> String
fmt.octalPrefixed(n: Int) -> String
fmt.zeroPad(n: Int, width: Int) -> String

// Floating-point formatting
fmt.fixed(n: Float, precision: Int) -> String
fmt.toFixed(n: Float, precision: Int) -> String
fmt.scientific(n: Float, precision: Int = 6) -> String
fmt.percentage(n: Float, precision: Int = 0) -> String
fmt.signFloat(n: Float, precision: Int = -1) -> String

// Padding and alignment (pad character defaults to ' ')
fmt.padLeft(s: String, width: Int, pad: Char = ' ') -> String
fmt.padRight(s: String, width: Int, pad: Char = ' ') -> String
fmt.center(s: String, width: Int, pad: Char = ' ') -> String
fmt.align(s: String, width: Int, mode: String = "left", pad: Char = ' ') -> String
fmt.visibleWidth(s: String) -> Int

// String utilities
fmt.repeat(s: String, n: Int) -> String
fmt.truncate(s: String, maxLen: Int, suffix: String = "...") -> String
fmt.truncateStart(s: String, maxLen: Int, suffix: String = "...") -> String
fmt.truncateMiddle(s: String, maxLen: Int, suffix: String = "...") -> String
fmt.escape(s: String) -> String
fmt.quote(s: String) -> String
fmt.indent(text: String, prefix: String) -> String
fmt.trimTrailingZeros(s: String) -> String

// Template formatting
fmt.format(template: String, args: List<String>) -> String

// Human-readable number formatting
fmt.thousands(n: Int, sep: String = ",") -> String
fmt.ordinal(n: Int) -> String
fmt.sign(n: Int) -> String
fmt.compact(n: Int, precision: Int = 1) -> String
fmt.bytes(n: Int) -> String
fmt.bytesFixed(n: Int, precision: Int = 2) -> String
fmt.metricBytes(n: Int, precision: Int = 2) -> String

// List formatting
fmt.joinWith<T>(items: List<T>, sep: String, mapper: fn(T) -> String) -> String
fmt.join(parts: List<String>, sep: String, last: String = "") -> String
fmt.bullet(items: List<String>, marker: String = "- ") -> String
fmt.numbered(items: List<String>) -> String
fmt.table(headers: List<String>, rows: List<List<String>>, sep: String = " | ") -> String
fmt.tableAligned(headers: List<String>, rows: List<List<String>>, alignments: List<String>, sep: String = " | ") -> String
```

**Notes.**

- `hex`, `octal`, `binary`, and `inBase` operate on non-negative
  integers. For negative values the sign is preserved:
  `fmt.hex(-1)` → `"-1"`. Use wrapping casts to format the raw bit
  pattern of negative signed integers.
- `inBase` accepts `base` in the range 2–36. Values outside this range
  abort.
- Padding, alignment, truncation, and template width/precision count
  Unicode grapheme clusters so combining marks are not split.
- `center` distributes extra padding with one more fill character on the
  right when `width - visibleWidth(s)` is odd.
- `truncate` returns `s` unchanged when `visibleWidth(s) <= maxLen`.
  When the suffix itself is longer than `maxLen`, the suffix is returned
  as-is without truncating it further.
- `format` supports automatic placeholders (`{}`), positional
  placeholders (`{0}`), alignment (`<`, `>`, `^`), custom fill
  (`{0:0>4}`), width, and string precision (`{0:.5}`). Invalid or
  out-of-range placeholders are preserved literally. Use raw strings or
  escaped braces so Osty's normal interpolation does not consume the
  template first.
- `join` with `last` produces Oxford-comma-style output. When
  `parts.len() <= 1` the `last` separator is not used.
- `tableAligned` accepts `"left"`, `"right"`, and `"center"`; unknown or
  missing alignment entries default to `"left"`.
