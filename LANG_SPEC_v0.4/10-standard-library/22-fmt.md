### 10.22 Formatting (`std.fmt`)

String formatting utilities beyond `{}` interpolation: number base
conversion, padding and alignment, fixed-precision printing, and
composite value rendering.

```osty
use std.fmt

// Number bases
fmt.hex(255)             // "ff"
fmt.hex(255, upper: true)  // "FF"
fmt.octal(8)             // "10"
fmt.binary(10)           // "1010"

// Padding and alignment
fmt.padLeft("hi", 6)         // "    hi"
fmt.padLeft("hi", 6, '0')    // "0000hi"
fmt.padRight("hi", 6)        // "hi    "
fmt.center("hi", 6)          // "  hi  "

// Fixed precision (mirrors Float.toFixed)
fmt.toFixed(3.14159, 2)      // "3.14"
fmt.toFixed(3.1,    4)       // "3.1000"

// Scientific notation
fmt.scientific(0.000123)     // "1.23e-4"
fmt.scientific(0.000123, precision: 1)  // "1.2e-4"

// Composite rendering
fmt.join(["a", "b", "c"], sep: ", ")          // "a, b, c"
fmt.join(["a", "b", "c"], sep: ", ", last: " and ")  // "a, b and c"
fmt.repeat("ab", 3)          // "ababab"
fmt.truncate("hello world", 8)          // "hello..."
fmt.truncate("hello world", 8, suffix: "…")  // "hello w…"
```

API:

```
// Integer base formatting
fmt.hex(n: Int, upper: Bool = false) -> String
fmt.octal(n: Int) -> String
fmt.binary(n: Int) -> String
fmt.inBase(n: Int, base: Int) -> String   // base in 2..36

// Floating-point formatting
fmt.toFixed(n: Float, precision: Int) -> String
fmt.scientific(n: Float, precision: Int = 6) -> String

// Padding and alignment (pad character defaults to ' ')
fmt.padLeft(s: String, width: Int, pad: Char = ' ') -> String
fmt.padRight(s: String, width: Int, pad: Char = ' ') -> String
fmt.center(s: String, width: Int, pad: Char = ' ') -> String

// String utilities
fmt.repeat(s: String, n: Int) -> String
fmt.truncate(s: String, maxLen: Int, suffix: String = "...") -> String

// List formatting
fmt.join(parts: List<String>, sep: String, last: String = "") -> String
```

**Notes.**

- `hex`, `octal`, `binary`, and `inBase` operate on non-negative
  integers. For negative values the sign is preserved:
  `fmt.hex(-1)` → `"-1"`. Use wrapping casts to format the raw bit
  pattern of negative signed integers.
- `inBase` accepts `base` in the range 2–36. Values outside this range
  abort.
- `center` distributes extra padding with one more space on the right
  when `width - len(s)` is odd.
- `truncate` returns `s` unchanged when `len(s) <= maxLen`. When the
  suffix itself is longer than `maxLen`, the suffix is returned as-is
  without truncating it further.
- `join` with `last` produces Oxford-comma-style output. When
  `parts.len() <= 1` the `last` separator is not used.
