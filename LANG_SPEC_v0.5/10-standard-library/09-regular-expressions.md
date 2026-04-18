### 10.9 Regular Expressions (`std.regex`)

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
```

`Match` provides `.text: String`, `.start: Int`, `.end: Int`.
`Captures` provides `.get(i: Int) -> String?` and
`.named(name: String) -> String?` for named groups `(?P<name>...)`.
