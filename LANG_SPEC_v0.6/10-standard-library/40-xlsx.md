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

#### 10.40.1 XLSX and information flow

XLSX files originating from external sources (uploads, email
attachments) carry untrusted cell values. Decoded cells inherit
the source's flow tag set:

```osty
fn ingest(fs: Fs, path: String) -> Result<List<Row>, Error> {
    let raw: #[taint("fs_input")] Bytes = fs.read(path)?
    let rows: List<Row> = xlsx.decodeRows(raw, "Sheet1")?
    // each row's cells: #[taint("fs_input")] String
    Ok(rows)
}
```

Cells flowing into SQL, shell, or HTML sinks must pass the
appropriate sanitizer first.

#### 10.40.2 XLSX deterministic encoding

`xlsx.encodeRows(name, rows)` produces deterministic output —
identical inputs yield byte-identical XLSX archives. This is
necessary for `#[golden]` tests of XLSX-generating code:

```osty
#[golden("fixtures/report.xlsx", mode = "binary")]
#[reproducible(scope = "portable")]
fn testReport() {
    let bytes = xlsx.encodeRows("Report", [
        ["name", "score"],
        ["Ada", "42"],
    ])?
    testing.assertGolden(bytes)
}
```

The deterministic property covers cell ordering, sheet metadata,
and the underlying ZIP container's internal byte layout. ZIP
timestamps are pinned to a fixed epoch (2000-01-01) to avoid
non-determinism from build-time clocks.
