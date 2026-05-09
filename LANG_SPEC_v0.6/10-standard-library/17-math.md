### 10.17 Math (`std.math`)

Mathematical functions and constants.

```osty
use std.math

let area = math.PI * r * r
let angle = math.sin(math.PI / 4.0)
let n = math.log(100.0, 10.0)
```

Constants:

```
math.PI, math.E, math.TAU
math.INFINITY, math.NAN
```

Functions (all operate on `Float`):

```
math.sin(x), math.cos(x), math.tan(x)
math.asin(x), math.acos(x), math.atan(x)
math.atan2(y, x)
math.sinh(x), math.cosh(x), math.tanh(x)

math.exp(x)
math.log(x)                           // natural log
math.log(x, base)
math.log2(x), math.log10(x)

math.sqrt(x), math.cbrt(x)
math.pow(x, y)

math.floor(x), math.ceil(x), math.round(x), math.trunc(x)
math.abs(x)
math.min(a, b), math.max(a, b)
math.hypot(a, b)
```

Integer math lives on the integer methods (§10.5).

#### v0.6 reproducibility

Every function in `std.math` is *pure* — no capability is consulted,
no allocation occurs, and the output is fully determined by the
input. The whole module is acceptable inside
`#[pure]` contexts.

The float-determinism guarantees from §10.5 apply: `math.sin(0.5)`
produces the same bit pattern on every run on a given target
triple, NaN bit patterns are preserved across compatible
operations, and subnormals are not silently flushed.

`math.NAN` is a single canonical NaN (the quiet NaN with the
implementation-defined payload). Operations that would produce a
NaN (`math.sqrt(-1.0)`, `0.0 / 0.0`, etc.) return a NaN with the
same bit pattern as `math.NAN`; this is necessary for `portable`
scope reproducibility.

#### Constants

The math constants (`math.PI`, `math.E`, `math.TAU`,
`math.INFINITY`, `math.NAN`) are compile-time `const fn` values
(§3.1.1) — referencing them counts as zero allocations and zero
operations beyond the constant load. Acceptable inside `#[pure]`
functions and any allocation-sensitive context.
