### 10.9 Regular Expressions (`std.regex`)

> **v0.6 capability note**: `std.regex` is *pure* — every function
> transforms strings without consulting any capability. The module
> is acceptable inside `#[reproducible(scope = "portable")]` and
> `#[pure]` contexts. Match results inherit the input string's flow
> tag set — `regex.findAll(taintedHaystack, "...")` produces a
> `List<String>` where each match carries `taintedHaystack`'s tags.
> The regex itself (compiled `Pattern`) is metadata and does not
> contribute tags.

RE2-based regular expressions. Linear-time matching; no catastrophic
backtracking, no ReDoS. Does not support backreferences or lookaround
(RE2 limitations).

```osty
use std.regex

let re = regex.compile(r"^\d{3}-\d{4}$")?

if re.matches("123-4567") { ... }

match re.captures("phone: 123-4567 ext 8") {
    Some(caps) -> println("full: {caps.get(0)}"),
    None -> {},
}

let cleaned = regex.compile(r"\s+")?.replace(text, " ")

for m in regex.compile(r"\b\w+\b")?.findAll(text) {
    process(m.text)
}
```

API:

```
regex.compile(pattern: String) -> Result<Regex, RegexError>

Regex.matches(text: String) -> Bool
Regex.find(text: String) -> Match?
Regex.findAll(text: String) -> List<Match>
Regex.captures(text: String) -> Captures?
Regex.capturesAll(text: String) -> List<Captures>
Regex.replace(text: String, replacement: String) -> String
Regex.replaceAll(text: String, replacement: String) -> String
Regex.split(text: String) -> List<String>

regex.matches(text: String, pattern: String) -> Result<Bool, RegexError>
regex.find(text: String, pattern: String) -> Result<Match?, RegexError>
regex.findAll(text: String, pattern: String) -> Result<List<Match>, RegexError>
regex.captures(text: String, pattern: String) -> Result<Captures?, RegexError>
regex.capturesAll(text: String, pattern: String) -> Result<List<Captures>, RegexError>
regex.replace(text: String, pattern: String, replacement: String) -> Result<String, RegexError>
regex.replaceAll(text: String, pattern: String, replacement: String) -> Result<String, RegexError>
regex.split(text: String, pattern: String) -> Result<List<String>, RegexError>
```

`Match` provides `.text: String`, `.start: Int`, `.end: Int`.
`Captures` provides `.get(i: Int) -> String?` and
`.named(name: String) -> String?` for named groups `(?P<name>...)`.
`RegexError` provides `.message: String` and `.message() -> String`.
