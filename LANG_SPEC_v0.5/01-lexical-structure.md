## 1. Lexical Structure

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

### 1.2 Keywords (17)

```
fn  struct  enum  interface  type
let  mut  pub
if  else  match
for  break  continue  return
use  defer
```

These are reserved and may not be used as identifiers.

### 1.3 Contextual Identifiers

The following have special meaning in context but are not reserved words:

- `self` — bound only inside method bodies
- `Self` — refers to the enclosing type inside a `struct`, `enum`, or
  `interface` body
- `true`, `false` — `Bool` constants from prelude
- `Some`, `None` — `Option` variants from prelude
- `Ok`, `Err` — `Result` variants from prelude

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
User-defined operator overloading is not provided.

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
source is a lex error. (This was open issue O7 in the v0.1 grammar; see
§18.)

Assignment operators (`=`, `+=`, …) are statement-only — assignment is
not an expression, so `let x = (y = 1)` is a compile error. `<-`
(channel send) is likewise statement-only.

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

This was open issue O3 in the v0.1 grammar.

**Suppression — following token.** The newline is also discarded when
the next non-whitespace token is any of:

- A closing `)`, `]`, or `}`
- `.`, `?.`
- `,`
- `..`, `..=`
- A binary operator that requires a left operand

Notably, **`else` is not in this set.** A `}` followed by a newline and
then `else` is a syntax error — `} else {` (and `} else if`) must appear
on a single physical line. This was open issue O2.

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
