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
