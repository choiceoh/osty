### 10.40 XLSX (`std.xlsx`)

`std.xlsx` builds simple Excel-compatible `.xlsx` workbooks on top of
stored ZIP packaging and `std.table` row adapters.

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
