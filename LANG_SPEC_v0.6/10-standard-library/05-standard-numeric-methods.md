### 10.5 Standard Numeric Methods

**Common to every integer type `T` (`Int`, `Int8..Int64`, `UInt8..UInt64`, `Byte`):**

```
abs() -> T                           // aborts on T.MIN for signed types
checkedAbs() -> T?
wrappingAbs() -> T
min(other: T) -> T
max(other: T) -> T
clamp(lo: T, hi: T) -> T
pow(exp: Int) -> T                   // aborts when exp < 0 or on overflow

wrappingAdd(other: T) -> T
wrappingSub(other: T) -> T
wrappingMul(other: T) -> T
wrappingDiv(other: T) -> T           // aborts only on other = 0
wrappingMod(other: T) -> T           // aborts only on other = 0
wrappingShl(other: Int) -> T
wrappingShr(other: Int) -> T

checkedAdd(other: T) -> T?
checkedSub(other: T) -> T?
checkedMul(other: T) -> T?
checkedDiv(other: T) -> T?           // None on other = 0 or overflow
checkedMod(other: T) -> T?
checkedShl(other: Int) -> T?
checkedShr(other: Int) -> T?

saturatingAdd(other: T) -> T
saturatingSub(other: T) -> T
saturatingMul(other: T) -> T
saturatingDiv(other: T) -> T         // aborts on other = 0

toFloat() -> Float                   // lossy beyond 2^53
toIntN() -> IntN?                    // lossless conversion to wider Int types
toChar() -> Char                     // aborts on out-of-range or surrogate
```

**Common to every float type `T` (`Float`, `Float32`, `Float64`):**

```
abs() -> T
min(other: T) -> T
max(other: T) -> T
clamp(lo: T, hi: T) -> T
pow(exp: T) -> T                     // fractional / negative OK

sqrt() -> T
floor() -> T
ceil() -> T
round() -> T                         // half-to-even (banker's rounding)
trunc() -> T
toFixed(n: Int) -> String            // "{n}"-digit decimal form
isNaN() -> Bool
isInfinite() -> Bool
toBits() -> UInt64                   // IEEE-754 bit pattern (for hashing)
totalCompare(other: T) -> Ordering    // IEEE-754 totalOrder; NaN sorted last
totalKey() -> UInt64                  // sortable key matching totalCompare

toIntTrunc() -> Result<Int, Error>   // truncates toward zero; Err on NaN/±Inf/overflow
toIntRound() -> Result<Int, Error>   // banker's rounding
toIntFloor() -> Result<Int, Error>
toIntCeil()  -> Result<Int, Error>
```

Osty does not provide a plain `Float.toInt()` — the four explicit
variants above eliminate the ambiguity about rounding mode. NaN and
±Inf are `Err(Error.new(...))` rather than abort, because they are
commonly produced by external inputs.

**`Char` conversions** (§2.1):

```
Char.toInt() -> Int
Char.fromInt(n: Int) -> Char?        // None on invalid scalar or surrogate
```

`Int.toChar()` aborts on invalid input (out of range or surrogate);
`Char.fromInt` is the safe form that returns `None`.

#### v0.6 reproducibility note

Every method in this chapter is *pure* — no capability is consulted,
no allocation occurs (numeric methods return scalar values), and
the output is fully determined by the input. This makes the entire
numeric surface acceptable inside `#[reproducible(scope =
"portable")]` and `#[pure]` contexts.

Float methods (`Float.sqrt`, `Float.pow`, `Float.sin`, etc.) inherit
the platform's IEEE-754 implementation. The v0.6 contract guarantees:

1. **Determinism within a target triple** — `Float.sqrt(2.0)`
   produces the same bit pattern on repeated runs on the same
   `<arch>-<os>` triple.
2. **NaN bit-pattern stability** — operations preserve NaN bit
   patterns across compatible operations, so a `#[reproducible(scope
   = "portable")]` function that does `f.sqrt().sqrt()` produces a
   bit-equal NaN regardless of platform when the input was NaN.
3. **No subnormal-flushing surprises** — Osty's float ops do not
   silently flush subnormals. A program that depends on subnormals
   produces the same answer on every supported target.

Floats do **not** implement `Ordered` because `Ordered` inherits
`Equal`, and `NaN.eq(NaN)` is false (§2.6.5). Sorting code that needs a
stable float order uses `totalCompare` or `totalKey`, both of which
follow IEEE-754 totalOrder: `-0.0 < +0.0`, all finite values sort
before infinities, and NaN payloads sort after non-NaN values in a
stable bit-pattern order. Floats are also not `Hashable`; use
`toBits()` when bit-pattern hashing is intended.
