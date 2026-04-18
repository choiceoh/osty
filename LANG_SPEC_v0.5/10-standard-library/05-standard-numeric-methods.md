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
