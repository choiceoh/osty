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
