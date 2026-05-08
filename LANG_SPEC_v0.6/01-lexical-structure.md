## 1. Lexical Structure

This chapter defines the lexical surface of Osty v0.6 — file encoding,
keywords (18 reserved + 14 v0 contextual), identifier conventions,
literals, and the rules the lexer applies to produce a token stream
the parser can consume. The grammar (`OSTY_GRAMMAR_v0.6.md`) and v0.6
design north star (*hidden dependency is forbidden*) inform the rule
set, but the *token-level* rules below are mostly stable across
versions: the v0.6 additions are limited to the `while` reserved
keyword (G49, §1.2) and four spec-block contextual keywords (`spec`,
`example`, `law`, `invariant`; G43, §1.3).

### 1.1 Source Files

- File extension: `.osty`
- Encoding: UTF-8
- Line terminator: `\n`. `\r\n` is accepted and normalized to `\n`.

A file may begin with a shebang line (`#!`) which is consumed and ignored by
the lexer:

```osty
#!/usr/bin/env osty
println("hello")
```

This allows Osty scripts to be executed directly on Unix systems.

A shebang is recognized **only at byte offset 0** of the source file and
**only once**. Any `#` appearing elsewhere is the start of an annotation
(§1.9) or, in any other context, a lex error.

### 1.2 Keywords (18)

```
fn  struct  enum  interface  type
let  mut  pub
if  else  match
for  while  break  continue  return
use  defer
```

These are reserved and may not be used as identifiers.

`while` (G49) is reserved. `while cond { body }` is equivalent in
lowering and meaning to `for cond { body }` — provided as ergonomics
for callers who expect a distinct conditional-loop keyword.

### 1.3 Contextual Identifiers

The following have special meaning in context but are not reserved words:

- `self` — bound only inside method bodies
- `Self` — refers to the enclosing type inside a `struct`, `enum`, or
  `interface` body
- `true`, `false` — `Bool` constants from prelude
- `Some`, `None` — `Option` variants from prelude
- `Ok`, `Err` — `Result` variants from prelude
- `loop` — `loop { ... }` expression head (§4.4.1)
- `const` — `const fn` declaration prefix (§3.1.1)
- `by` — range step marker in range expressions (§4.4)
- `spec` — spec block opener at function-body first-statement position
  (§3.13). G43.
- `example`, `law`, `invariant` — clause keywords inside `spec { ... }`
  (§3.13). G43.
- `forall` — *(reserved for v1, Phase 5)* property-test clause keyword
  inside `spec { ... }` (§3.13.3).

### 1.4 Identifiers

```
identifier := letter (letter | digit | '_')*
letter     := [a-zA-Z_]
digit      := [0-9]
```

Identifier case has no syntactic significance. Visibility is controlled
exclusively by the `pub` keyword (§5.3).

**Naming conventions.** The formatter (`osty fmt`) enforces a consistent
casing convention across the codebase:

| Category | Convention | Examples |
|---|---|---|
| Types (`struct`, `enum`, `interface`, type alias, generic parameters) | `PascalCase` | `User`, `ReadWriter`, `T`, `UserMap` |
| Enum variants | `PascalCase` | `Some`, `None`, `Circle`, `RGB` |
| Functions, methods, parameters, fields, local bindings | `camelCase` | `loadConfig`, `userName`, `maxSize` |
| Top-level immutable scalar constants (literal-initialized) | `SCREAMING_SNAKE_CASE` | `MAX_USERS`, `DEFAULT_PORT` |
| Packages / modules | `lowercase` (single word preferred) | `fs`, `http`, `json`, `taskgroup` |

The formatter reports violations as warnings by default and as errors
under `osty fmt --check`.

Underscore-prefixed identifiers (e.g. `_unused`) are permitted for
intentionally unused bindings and are not reported by linters.

**`_` as a wildcard token.** A bare `_` (single underscore not followed
by any identifier-continuation character) is a distinct token used as a
pattern wildcard, a destructuring placeholder, and the type-arguments
inference marker. Because the lexer applies maximal munch, `_foo` and
`_1` are identifiers; only the lone `_` is the wildcard token.

**Label vs char literal.** A single quote followed by one scalar or
escape sequence and a closing single quote is a `CHAR_LIT` (`'x'`,
`'\n'`, `'\u{1F600}'`). A single quote followed by an identifier and
not immediately closed is a `LABEL` (`'outer`, `'search`). This
lookahead rule makes `'X'` a char literal and `'X:` a loop label prefix.

#### 1.4.1 v0.6 capability identifier convention

The 7 canonical capability ambient names are reserved as
*conventional identifiers* — `clock`, `rng`, `env`, `fs`, `net`,
`process`, `console`. They are not keywords (using them as
parameter names in non-capability contexts is permitted), but the
formatter and `osty audit --capabilities` recognize them as the
canonical capability bindings under `#[ambient]` (§20.3).

```osty
fn helper(clock: Clock) -> Time { clock.now() }   // canonical name

fn helper(c: Clock) -> Time { c.now() }           // permitted but not idiomatic;
                                                  // formatter does not rewrite
```

Authors using a non-canonical name (`c` instead of `clock`) lose
ambient compatibility — `#[ambient(clock)]` cannot inject into a
parameter named `c`. The compiler does not auto-rename; the
discipline is to follow the canonical names so that the binding
matches the ambient injection point.

### 1.5 Comments

```osty
// Line comment

/* Block
   comment */

/// Documentation comment.
/// Attached to the following declaration.
fn example() { }
```

`/* */` does not nest. `///` precedes a declaration and is extracted by
tooling.

### 1.6 Literals

#### 1.6.1 Integer literals
```
42
1_000_000        // underscore separators
0xFF             // hexadecimal
0b1010           // binary
0o777            // octal
```

Base prefixes must be lowercase (`0x`, `0b`, `0o`). The uppercase forms
`0X`, `0B`, `0O` are rejected as **E0002**.

Underscores are permitted only **between two digits of the same base**.
A numeric literal may not start with `_`, end with `_`, contain `__`, or
place `_` immediately after a base prefix or adjacent to `.`, `e`/`E`,
or the exponent sign. Violations are reported as **E0008**. Valid
examples: `1_000`, `0xDEAD_BEEF`, `0b1010_1010`. Invalid: `1_`, `_1_000`
(a leading `_` lexes as an identifier, not a number), `1__000`, `0x_FF`,
`1_.5`, `1.5_e2`.

#### 1.6.2 Float literals
```
3.14
1.0e10
2.5e-3
```

Both sides of the decimal point must be digits — neither `.5` nor `1.`
is a float literal. The exponent marker (`e` or `E`) must be followed by
at least one digit, optionally preceded by `+` or `-`. The same
underscore-placement rule applies as for integers (§1.6.1).

#### 1.6.3 String literals

Standard string:
```osty
"hello"
"escapes: \n \t \" \\"
"unicode: \u{1F600}"
```

Interpolation: an unescaped `{` opens an interpolation expression,
terminated by `}`. Escape with `\{`, `\}`.

```osty
"hi, {name}"
"{user.name} is {user.age}"
"items: {xs.join(", ")}"
"literal \{ brace }"
```

Raw string (no escape processing, no interpolation):
```osty
r"\d+\.\d+"
r"C:\Users\name"
```

Triple-quoted (multi-line) string:
```osty
let sql = """
    SELECT *
    FROM users
    WHERE id = {id}
    """
```

Indentation handling for triple-quoted strings:

1. The opening `"""` must be followed by a newline.
2. The closing `"""` must appear on its own line. The whitespace before it
   defines the common indent prefix.
3. Each content line must begin with at least the common indent prefix,
   which is stripped from every content line.
4. If any content line does not begin with the common indent prefix (and
   is not blank), it is a compile error.
5. The trailing newline before the closing `"""` is removed.
6. Interpolation and escape sequences are processed as in standard
   strings.
7. `r"""..."""` disables escape and interpolation but still applies
   indentation handling.

##### Interpolation and v0.6 surfaces

`"{expr}"` is sugar for `expr.toString()` (§17). The interaction
with v0.6 surfaces:

**Flow tags.** An interpolated value's flow tags ride into the
resulting `String`. A literal piece of the string is untagged; the
unioned tag set across all interpolation points becomes the
`String`'s tag set:

```osty
let user: #[taint("user_input")] User = ...
let line = "user is {user.name}"
//        ^^^^^^^^^^^^^^^^^^^^^^^
//        line: #[taint("user_input")] String
```

**Capability calls.** An interpolation expression may be any expression,
including `clock.now()` or `rng.next()`. The capability is consulted
once per interpolation:

```osty
let id = "req-{rng.next()}"          // rng called once
let log = "{clock.now()}: ready"     // clock.now() called once
```

`#[reproducible]` functions cannot have interpolation expressions
that consult non-deterministic capabilities — the resulting string
would not be reproducible. The checker walks each interpolation
sub-expression like any other expression.

**Raw strings disable interpolation.** `r"hello {name}"` is a raw
literal — `{name}` appears as `{name}` in the output. There is no
flow-tag or capability concern because no expression is evaluated.

**Triple-quoted preserves interpolation.** `"""{expr}"""` is
interpolated identically to `"..."`; `r"""..."""` is raw.

##### Format specifier policy

Osty does **not** support inline format specifiers — `"{x:.2f}"`
or `"{x:>10}"` are syntax errors. The rationale: format options
hide intent at the call site. The recommended pattern is explicit
method calls before interpolation:

```osty
"price: {n.toFixed(2)}"
"hex:   {n.toString(base: 16)}"
"padded:{name.padLeft(10)}"
```

This keeps interpolation grammar trivial (no parser ambiguity
between `:` for format and `:` for type ascription, etc.) and
makes formatting inspectable in the AST.

#### 1.6.4 Char and Byte literals
```osty
'A'              // Char (Unicode scalar value)
'\n'
'\u{1F600}'
b'A'             // Byte (UInt8); ASCII only
```

A char literal holds **exactly one** Unicode scalar value. A byte
literal `b'X'` holds exactly one ASCII scalar (U+0000–U+007F). Empty
literals (`''`, `b''`) are rejected by the lexer as **E0009**. Multiple
scalars inside a char literal and non-ASCII scalars inside a byte
literal are rejected during type checking (not at lex time).

#### 1.6.5 Bool literals

`true` and `false`.

#### 1.6.6 Collection literals
```osty
[1, 2, 3]                  // List<Int>
[]                         // empty List; type from context
{"a": 1, "b": 2}           // Map<String, Int>
{:}                        // empty Map; type from context
(1, "two", 3.0)            // tuple (Int, String, Float)
```

There is no Set literal. Use `Set.from([...])`.

### 1.7 Operators

```
Arithmetic:  +  -  *  /  %
Comparison:  ==  !=  <  >  <=  >=
Logical:     &&  ||  !
Bitwise:     &  |  ^  ~  <<  >>
Assignment:  =  +=  -=  *=  /=  %=  &=  |=  ^=  <<=  >>=
Other:       ?    // Result/Option propagation; also Option<T> sugar in type position
             ?.   // optional chaining
             ??   // nil-coalescing (default for None)
             ..   // exclusive range (and rest in patterns/struct update)
             ..=  // inclusive range
```

Increment (`++`) and decrement (`--`) are not provided.
User-defined operator overloading is limited to the explicit `#[op(+)]`,
`#[op(-)]`, `#[op(*)]`, `#[op(/)]`, and `#[op(%)]` binary forms plus
unary `#[op(-)]` (§3.8, §14.2). All other operators remain primitive-
only or interface-defined and cannot be overloaded.

**Punctuation and contextual tokens.** The following are not operators
but serve as syntactic punctuation or context-specific markers:

| Token | Role |
|---|---|
| `.`   | Member access (also in chained method calls) |
| `::`  | Turbofish prefix; **must** be followed by `<` (§2.7.2) |
| `->`  | Function return type, `match` arm separator |
| `<-`  | Channel send (statement only — §8.5) |
| `_`   | Wildcard (patterns, destructuring) |
| `@`   | Binding in patterns |
| `\|`  | Pattern alternation (only in pattern position) |
| `#`   | Annotation prefix (`#[...]`) or shebang at byte 0 |

**`=>` is not a token.** Match arms use `->`. Any occurrence of `=>` in
source is a lex error.

Assignment operators (`=`, `+=`, …) are statement-only — assignment is
not an expression, so `let x = (y = 1)` is a compile error. `<-`
(channel send) is likewise statement-only.

#### 1.7.1 Operator categorization

The operator set is intentionally small. Categorized by precedence
(highest to lowest):

| Group | Operators | Associativity |
|---|---|---|
| **Postfix** | `.`, `?.`, `()`, `[]`, `?` | left |
| **Unary** | `-`, `!` | right (prefix) |
| **Multiplicative** | `*`, `/`, `%` | left |
| **Additive** | `+`, `-` | left |
| **Shift** | `<<`, `>>` | left |
| **Bitwise AND** | `&` | left |
| **Bitwise XOR** | `^` | left |
| **Bitwise OR** | `\|` | left |
| **Range** | `..`, `..=` | non-associative |
| **Comparison** | `<`, `<=`, `>`, `>=` | non-associative |
| **Equality** | `==`, `!=` | non-associative |
| **Logical AND** | `&&` | left, short-circuit |
| **Logical OR** | `\|\|` | left, short-circuit |
| **Nil-coalesce** | `??` | right |

`?` (postfix propagation) is parsed at postfix precedence; `?.`
binds at the same precedence as `.`. The full Pratt table is in
`OSTY_GRAMMAR_v0.6.md §R-prec`.

Operators not in the table are reserved for future use or
explicitly excluded:
- `++`, `--` — excluded (§14)
- `=>` — excluded (use `->`)
- `~` — reserved
- `**` — reserved (use `Int.pow`)
- `<>` — not a token
- `===`, `!==` — not provided (`==` uses `Equal`)

#### 1.7.2 v0.6 operator surface

The v0.6 baseline does not change the operator set. Three
interactions worth noting:

- `?` propagates `Cancelled` (§7.6) identically to any other
  `Error` — there is no special operator for cancellation.
- `==` on capability values is not defined — capabilities do not
  implement `Equal`. Use `std.ref.same(a, b)` for reference
  identity.
- `+=` on a `String` field of a sealed-construct struct is allowed
  *only* when the field is `pub mut` (rare for sealed types).
  Sealed struct fields are typically read-only after parse.

### 1.8 Statement Separators

Newlines separate statements. There are no semicolons. The lexer promotes
each physical newline to a statement terminator (`TERMINATOR`) **unless**
one of the suppression rules below applies. There is no `\`-EOL line
continuation; newlines inside triple-quoted strings or block comments are
not tokens at all.

**Suppression — preceding token.** The newline is discarded when the
last non-whitespace token before it is any of:

- A binary operator awaiting a right operand (`+`, `-`, `*`, `/`, `%`,
  `==`, `!=`, `<`, `>`, `<=`, `>=`, `&&`, `||`, `&`, `|`, `^`, `<<`,
  `>>`, `=`, `+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`,
  `>>=`, `??`)
- `,`
- `->`, `<-`
- `::`, `@`
- `|` in pattern context (pattern-or)
- An opening `(`, `[`, or `{`

The preceding tokens `.` and `?.` **do not** suppress newlines. In
consequence, method-chain continuation must place the `.` (or `?.`) at
the **start** of the continuation line, not at the end of the previous
line:

```osty
let x = items
    .filter(|x| x > 0)        // OK — leading dot
    .map(|x| x * 2)
    .sum()

let y = items.                // ERROR — trailing dot terminates the
    filter(|x| x > 0)         // statement; the next line is a new stmt
```

**Suppression — following token.** The newline is also discarded when
the next non-whitespace token is any of:

- A closing `)`, `]`, or `}`
- `.`, `?.`
- `,`
- `..`, `..=`
- A binary operator that requires a left operand

Notably, **`else` is not in this set.** A `}` followed by a newline and
then `else` is a syntax error — `} else {` (and `} else if`) must appear
on a single physical line.

```osty
if cond {
    foo()
} else {                      // OK — same line
    bar()
}

if cond {
    foo()
}                             // ERROR — newline before else
else {
    bar()
}
```

**Trailing commas.** Permitted in lists, tuples, match arms, function
parameters, struct/enum/interface bodies, annotation arg lists, and
similar comma-separated constructs. The single-element tuple `(x,)`
**requires** a trailing comma to distinguish it from a parenthesized
expression `(x)`.

### 1.9 Annotations

Annotations attach compile-time metadata to declarations. The syntax is
Rust-style:

```
annotation := '#' '[' name ('(' argList? ')')? ']'
argList    := arg (',' arg)* ','?
arg        := argName ('=' literal)?      // flag form omits '=' literal
argName    := identifier | keyword
literal    := stringLit | intLit | floatLit | boolLit
            | '-' (intLit | floatLit)
name       := identifier
```

Two argument forms are accepted:

- **Key/value:** `name = literal` (e.g. `key = "user_id"`).
- **Flag:** a bare identifier (e.g. `skip`). The flag form means
  "argument present, value `true`."

Both forms may be mixed in the same argument list:

```osty
#[json(key = "user_id", skip)]
pub legacyId: String?
```

An annotation precedes the declaration it applies to and occupies its
own logical line. Multiple annotations may be stacked:

```osty
#[json(key = "user_id")]
pub userId: String

#[deprecated(since = "0.5", use = "loginV2")]
pub fn login(user: String, pass: String) -> Result<Session, Error> { ... }
```

Annotation argument names are matched by name; reserved keywords
(e.g. `use`, `type`) are permitted as argument names to keep the
annotation vocabulary independent of language keywords. Argument
values must be literals — there are no expressions, identifiers, or
concatenations. A unary `-` prefix is accepted on numeric literals
(consistent with default-argument syntax in §3.1).

Annotations are not expressions, cannot be constructed dynamically,
and cannot be declared by user code. The set of recognized annotations
is fixed by the compiler (§3.8). Applying any unrecognized annotation,
an annotation with unknown arguments, an annotation in an unsupported
position, or with arguments of the wrong type, is a compile error.

---

### 1.10 v0.6 lexical surface summary

이 섹션은 v0.6 의 *lexical-level* 변경을 한눈에 볼 수 있게 정리. 정식
규칙은 위 §1.2 (keywords) / §1.3 (contextual identifiers) / §1.9
(annotations).

#### 1.10.1 Reserved keyword 변경

v0.5 → v0.6 에서 reserved keyword 가 17 → 18 로 +1 — `while` (G49).
`while cond { body }` 와 `for cond { body }` 는 *동의어* — 두 form
모두 같은 IR 로 lower (§4.4).

| v0.5 | v0.6 | 변경 |
|---|---|---|
| 17 reserved | 18 reserved | +`while` |

#### 1.10.2 Contextual keyword 변경

v0.5 의 contextual identifier set (`self`, `Self`, `true`, `false`,
`Some`, `None`, `Ok`, `Err`, `loop`, `const`, `by`) 위에 v0.6 은
4 추가 — `spec`, `example`, `law`, `invariant` (G43, §3.13).

`spec` 은 top-level `fn` / method body 의 첫 statement 위치에서만
keyword. `example`, `law`, `invariant` 는 *spec block 내부* 에서만
keyword. 그 외 위치 (변수 이름, struct field 이름) 에서는 식별자.

`forall` 은 v1 (Phase 5) 단계에서 추가 예정 — v0.6.0 baseline 에는
없음.

#### 1.10.3 Annotation set 변경

v0.5 의 11 fixed annotation 위에 v0.6 은 20 신규 annotation 추가.
모든 v0.6 annotation 의 합법 위치 표는 `00-revision.md §7.6` 가
권위.

| 카테고리 | v0.6 신규 annotation |
|---|---|
| Capabilities (G36) | `#[ambient]`, `#[reproducible_capability]` |
| Information flow (G37) | `#[taint]`, `#[sanitizes]`, `#[trusted_declassify]`, `#[taint_field]` (parameter `#[requires]` 는 v0.5 reuse) |
| Spec link (G38) | `#[spec]` |
| Reproducibility (G39) | `#[reproducible]` |
| Sealed construct (G40) | `#[sealed_construct]`, `#[trusted_construct]`, `#[test_construct]` |
| Error contract (G41) | `#[error_contract]` |
| Structured intent (G42) | `#[purpose]`, `#[example]`, `#[fixture]` |
| API evolution (G44) | `#[since]`, `#[stability]`, `#[match_compat]` |
| Golden tests (G45) | `#[golden]` |
| Performance (G46) | `#[budget]` |

annotation 인자는 v0.5 와 동일 — *literal 만* (string / int / bool /
ident / `key = literal`). expression 인자 불가.

#### 1.10.4 Lexer token 변경

v0.5 에서 v0.6 으로 *새 token kind 0 추가*. 모든 v0.6 신규 syntax
(spec block / parameter-position annotation / capability parameter)
는 기존 token 으로 표현 가능.

#### 1.10.5 ASI 영향

`while` keyword 추가는 `for` / `loop` 와 동일한 ASI suppression
패턴. `while cond { body }` 는 `if cond { body }` 와 같은 token
flow 로 처리.

`spec { }` block 은 top-level `fn` / method body 의 *첫 statement
위치* 에서 lex — `fn foo() {` 다음 `spec` 식별자 발견 시 spec block
시작 token 으로 승격. closure body, `if` arm, `match` arm, nested block
안에서는 일반 식별자.

#### 1.10.6 Diagnostic 영향

v0.6 lexical 영역에 *신규 진단 코드 0* — 모든 신규 surface 가 기존
band 의 코드 재사용 또는 §3.8 / §20 / §21 의 의미론 진단으로 처리.

`E0400` (CodeUnknownAnnotation) 는 v0.6 의 20 추가 annotation 모두
recognize — `internal/ast/ast.go::annotationRules` 와
`toolchain/resolve.osty::srAnnotAllowedTargets`, checked-in selfhost
generated mirror 가 sync.
