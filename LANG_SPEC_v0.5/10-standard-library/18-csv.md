### 10.18 CSV (`std.csv`)

RFC 4180-compliant CSV.

```osty
use std.csv

let text = csv.encode([
    ["name", "age", "city"],
    ["alice", "30", "seoul"],
    ["bob", "25", "busan"],
])

let rows: List<List<String>> = csv.decode(text)?

// With headers
let records: List<Map<String, String>> = csv.decodeHeaders(text)?
for r in records {
    println("{r.get(\"name\")}: {r.get(\"age\")}")
}
```

API:

```
csv.encode(rows: List<List<String>>) -> String
csv.encodeWith(rows: List<List<String>>, options: CsvOptions) -> String

csv.decode(text: String) -> Result<List<List<String>>, Error>
csv.decodeHeaders(text: String) -> Result<List<Map<String, String>>, Error>
csv.decodeWith(text: String, options: CsvOptions) -> Result<List<List<String>>, Error>

pub struct CsvOptions {
    pub delimiter: Char = ',',
    pub quote: Char = '"',
    pub trimSpace: Bool = false,
}
```

Behavior:

- `encode` emits CRLF row separators. Fields containing the delimiter,
  quote, CR, or LF are quoted, and embedded quotes are doubled.
- `encodeWith` aborts on invalid options. `decodeWith` returns an error
  for the same invalid options. `delimiter` and `quote` must differ, and
  neither may be CR or LF.
- `decode` accepts CRLF and bare LF row terminators, accepts a final row
  without a trailing terminator, and returns `[]` for empty input.
- `trimSpace` trims ASCII space and tab from unquoted fields. It also
  permits quoted fields to be surrounded by ASCII space or tab, and
  `encodeWith(..., trimSpace = true)` quotes fields whose leading or
  trailing space/tab would otherwise be lost on decode.
- `decodeHeaders` treats the first row as column names. Header names must
  be non-empty and unique, and every data row must have exactly the same
  field count as the header row.
