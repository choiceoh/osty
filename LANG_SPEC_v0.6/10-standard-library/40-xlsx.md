### 10.40 XLSX (`std.xlsx`)

`std.xlsx` builds simple Excel-compatible `.xlsx` workbooks on top of
stored ZIP packaging and `std.table` row adapters.

`std.xlsx` is *pure* — every function operates on `Bytes` /
`String` / `Table` values; the chapter never touches the
filesystem. Reading a `.xlsx` from disk and writing one back are
ordinary `Fs.read`/`Fs.write` calls in the caller's code:

```osty
fn loadAndAnnotate(fs: Fs, path: String) -> Result<Bytes, Error> {
    let raw = fs.read(path)?               // Fs effect at the boundary
    let rows = xlsx.decodeRows(raw, "Sheet1")?
    let annotated = rows.map(|r| r.appendCell("checked"))
    xlsx.encodeRows("Sheet1", annotated)   // pure, returns new Bytes
}
```

The encode/decode pair is acceptable inside
`#[reproducible(scope = "target")]` since it does not consult any
capability.

The first supported format is intentionally conservative: workbooks are ZIP
packages whose entries use the ZIP "store" method, worksheet cells use inline
strings or scalar numeric/bool values, and decode supports those same stored
packages plus shared-string tables when present. Deflated third-party XLSX
archives become readable once `std.compress` grows deflate support for
`std.zip`.

```osty
use std.xlsx

let bytes = xlsx.encodeRows("Report", [
    ["name", "score"],
    ["Ada", "42"],
    ["Grace", "99"],
])?

let rows = xlsx.decodeRows(bytes, "Report")?
```

Primary surface:

- `CellKind`, `Cell`, `Sheet`, and `Workbook`
- `text`, `number`, `int`, `bool`, `blank`, `cell`
- `sheet`, `workbook`, `fromRows`, `fromTable`, `toTable`, `rows`
- `encode`, `decode`, `encodeRows`, `decodeRows`, `encodeTable`
- `sheetNames`, `isWorkbook`, `kindName`
