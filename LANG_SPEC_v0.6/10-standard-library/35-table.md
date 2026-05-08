### 10.35 Tables (`std.table`)

`std.table` is a small dataframe layer for command-line tools and data
cleanup jobs. It keeps cells as strings for lossless CSV/TSV round-trips,
then layers typed access and column inference on top.

`std.table` is *pure* — every function operates on already-captured
`String` / `Bytes` values. Reading a CSV from disk happens at the
caller's `Fs` boundary; the resulting `String` is then passed to
`table.fromCsv`. The pure surface is acceptable inside
`#[reproducible(scope = "target")]`. Cell values inherit the flow
tag set of the source `String` — a CSV constructed from user-uploaded
bytes carries `#[taint("user_input")]` into every cell extracted via
`table.row(i).cell("name")`, so authors handling untrusted CSV must
sanitize before reaching SQL / shell / HTML sinks.

```osty
use std.table

let people = table.fromCsv("name,age,city\nalice,30,seoul\nbob,25,busan")?
let adults = people
    .select(["name", "age"])?
    .sortBy("age", false)?

let counts = people.countBy("city", "count")?
let text = counts.toTsv()
```

Typed sort, validation, and aggregation are available when the table is
being used as a practical dataframe rather than only a CSV wrapper:

```osty
let sales = table.fromCsv("city,amount\nseoul,10\nbusan,7\nseoul,5")?
let schema = table.schemaFrom(["city", "amount"], [table.TextColumn, table.FloatColumn])?
let clean = sales.coerce(schema)?
let ranked = clean.sortByFloat("amount", true)?
let byCity = clean.summarizeBy("city", "amount")?
```

Core API:

```osty
pub enum ColumnType {
    EmptyColumn,
    BoolColumn,
    IntColumn,
    FloatColumn,
    TextColumn,
    MixedColumn,
}

pub struct ColumnSchema {
    pub name: String,
    pub kind: ColumnType,
}

pub struct Row {
    pub values: Map<String, String>,

    pub fn get(self, column: String) -> String?
    pub fn getOr(self, column: String, fallback: String) -> String
    pub fn int(self, column: String) -> Int?
    pub fn float(self, column: String) -> Float?
    pub fn bool(self, column: String) -> Bool?
}

pub struct TableGroup {
    pub key: String,
    pub rows: List<Row>,
}

pub struct NumericSummary {
    pub count: Int,
    pub sum: Float,
    pub avg: Float,
    pub min: Float,
    pub max: Float,
}

pub struct JoinIndex {
    pub columns: List<String>,
    pub column: String,
    pub keys: List<String>,
    pub buckets: Map<String, List<Row>>,
}

pub struct Table {
    pub columns: List<String>,
    pub rows: List<Row>,

    pub fn len(self) -> Int
    pub fn isEmpty(self) -> Bool
    pub fn width(self) -> Int
    pub fn hasColumn(self, column: String) -> Bool
    pub fn get(self, row: Int, column: String) -> String?

    pub fn select(self, columns: List<String>) -> Result<Table, Error>
    pub fn drop(self, columns: List<String>) -> Result<Table, Error>
    pub fn filter(self, pred: fn(Row) -> Bool) -> Table
    pub fn sortBy(self, column: String, descending: Bool) -> Result<Table, Error>
    pub fn sortByType(self, column: String, kind: ColumnType, descending: Bool) -> Result<Table, Error>
    pub fn sortByInt(self, column: String, descending: Bool) -> Result<Table, Error>
    pub fn sortByFloat(self, column: String, descending: Bool) -> Result<Table, Error>
    pub fn sortByBool(self, column: String, descending: Bool) -> Result<Table, Error>
    pub fn groupBy(self, column: String) -> Result<List<TableGroup>, Error>
    pub fn countBy(self, column: String, countColumn: String) -> Result<Table, Error>
    pub fn innerJoin(self, right: Table, leftColumn: String, rightColumn: String) -> Result<Table, Error>
    pub fn leftJoin(self, right: Table, leftColumn: String, rightColumn: String) -> Result<Table, Error>
    pub fn innerJoinIndex(self, right: JoinIndex, leftColumn: String) -> Result<Table, Error>
    pub fn leftJoinIndex(self, right: JoinIndex, leftColumn: String) -> Result<Table, Error>
    pub fn inferTypes(self) -> List<ColumnSchema>
    pub fn validateSchema(self, schema: List<ColumnSchema>) -> Result<(), Error>
    pub fn coerce(self, schema: List<ColumnSchema>) -> Result<Table, Error>
    pub fn summarize(self, column: String) -> Result<NumericSummary, Error>
    pub fn summarizeBy(self, groupColumn: String, valueColumn: String) -> Result<Table, Error>
    pub fn sum(self, column: String) -> Result<Float, Error>
    pub fn avg(self, column: String) -> Result<Float, Error>
    pub fn min(self, column: String) -> Result<Float, Error>
    pub fn max(self, column: String) -> Result<Float, Error>
    pub fn indexBy(self, column: String) -> Result<JoinIndex, Error>

    pub fn dataRows(self) -> List<List<String>>
    pub fn toRows(self) -> List<List<String>>
    pub fn records(self) -> List<Map<String, String>>
    pub fn toCsv(self) -> String
    pub fn toTsv(self) -> String
}

pub fn empty() -> Table
pub fn row(values: Map<String, String>) -> Row
pub fn schema(name: String, kind: ColumnType) -> ColumnSchema
pub fn schemaFrom(columns: List<String>, kinds: List<ColumnType>) -> Result<List<ColumnSchema>, Error>

pub fn fromRows(columns: List<String>, rows: List<List<String>>) -> Result<Table, Error>
pub fn fromRecords(columns: List<String>, rows: List<Map<String, String>>) -> Result<Table, Error>
pub fn fromCsv(text: String) -> Result<Table, Error>
pub fn fromTsv(text: String) -> Result<Table, Error>
pub fn fromDelimited(text: String, options: csv.CsvOptions) -> Result<Table, Error>

pub fn toCsv(table: Table) -> String
pub fn toTsv(table: Table) -> String
pub fn toDelimited(table: Table, options: csv.CsvOptions) -> String

pub fn select(table: Table, columns: List<String>) -> Result<Table, Error>
pub fn drop(table: Table, columns: List<String>) -> Result<Table, Error>
pub fn sortBy(table: Table, column: String, descending: Bool) -> Result<Table, Error>
pub fn sortByType(table: Table, column: String, kind: ColumnType, descending: Bool) -> Result<Table, Error>
pub fn sortByInt(table: Table, column: String, descending: Bool) -> Result<Table, Error>
pub fn sortByFloat(table: Table, column: String, descending: Bool) -> Result<Table, Error>
pub fn sortByBool(table: Table, column: String, descending: Bool) -> Result<Table, Error>
pub fn groupBy(table: Table, column: String) -> Result<List<TableGroup>, Error>
pub fn countBy(table: Table, column: String, countColumn: String) -> Result<Table, Error>
pub fn innerJoin(left: Table, right: Table, leftColumn: String, rightColumn: String) -> Result<Table, Error>
pub fn leftJoin(left: Table, right: Table, leftColumn: String, rightColumn: String) -> Result<Table, Error>
pub fn innerJoinIndex(left: Table, right: JoinIndex, leftColumn: String) -> Result<Table, Error>
pub fn leftJoinIndex(left: Table, right: JoinIndex, leftColumn: String) -> Result<Table, Error>
pub fn inferTypes(table: Table) -> List<ColumnSchema>
pub fn validateSchema(table: Table, schema: List<ColumnSchema>) -> Result<(), Error>
pub fn coerce(table: Table, schema: List<ColumnSchema>) -> Result<Table, Error>
pub fn summarize(table: Table, column: String) -> Result<NumericSummary, Error>
pub fn summarizeBy(table: Table, groupColumn: String, valueColumn: String) -> Result<Table, Error>
pub fn sum(table: Table, column: String) -> Result<Float, Error>
pub fn avg(table: Table, column: String) -> Result<Float, Error>
pub fn min(table: Table, column: String) -> Result<Float, Error>
pub fn max(table: Table, column: String) -> Result<Float, Error>
pub fn indexBy(table: Table, column: String) -> Result<JoinIndex, Error>
pub fn columnTypeName(kind: ColumnType) -> String
```

Behavior:

- `fromCsv` and `fromTsv` treat the first row as headers. Header names
  must be non-empty and unique; data rows must have the same width.
- `toCsv` and `toTsv` include the header row and delegate escaping to
  `std.csv`.
- `select`, `drop`, `sortBy`, `groupBy`, and joins return `Error` for
  unknown columns instead of silently producing sparse output.
- `sortBy` is stable enough for small tool data and compares cell text
  lexicographically.
- `sortByType` and its `Int`/`Float`/`Bool` wrappers parse cells before
  comparison and return `Error` for non-conforming values.
- `validateSchema` accepts empty cells as missing values but checks every
  non-empty cell against the requested type. `coerce` normalizes typed
  columns to canonical strings while preserving unspecified columns.
- `groupBy` preserves first-seen group order. `countBy` is the common
  aggregation shortcut.
- `summarize` computes numeric count/sum/avg/min/max for one column.
  `summarizeBy` returns those statistics as a table grouped by a key
  column.
- `innerJoin` and `leftJoin` keep all left columns and include right-side
  non-key columns. They build a right-side `JoinIndex` internally, so
  repeated key lookup is linear in the number of rows instead of a nested
  scan. Reused joins can build the index explicitly with `indexBy`.
  Conflicting right column names are prefixed with `right.` and suffixed
  when needed.
- `inferTypes` ignores empty cells, promotes `IntColumn` plus
  `FloatColumn` to `FloatColumn`, treats any text cell as `TextColumn`,
  and returns `MixedColumn` for incompatible non-text mixes.

#### 10.35.1 Table operations and v0.6 surfaces

All `std.table` operations are pure transformations over `Table`
values. They:

- Return new `Table` values; mutations are local to the call.
- Preserve flow tags element-wise — a tainted CSV input produces
  tainted cells; transformations (`select`, `sortBy`, `coerce`)
  preserve the tags.
- Are acceptable inside `#[reproducible(scope = "portable")]`,
  provided sort orders are deterministic (text comparison is
  byte-wise; numeric comparison follows the IEEE-754 totalOrder
  per §10.5).

#### 10.35.2 Table aggregation determinism

`countBy(column, name)` and `summarizeBy(groupBy, sumColumn)` use
deterministic ordering — output rows appear in *first-occurrence*
order from the input, not in hash order. This makes
table-aggregation pipelines acceptable inside `#[reproducible]`
without explicit `sortBy` afterward.

```osty
let counts = sales.countBy("city", "count")?
// counts.rows order: ["seoul", "busan"] (first-occurrence in sales)
```

Authors who want a different output order apply explicit `sortBy`
on the aggregated result. The first-occurrence default is the
choice that maximizes reproducibility under typical usage.

#### 10.35.3 Schema coercion and information flow

`Table.coerce(schema)` parses each cell from text into the
column's typed form. The parsed values inherit the source string's
flow tag set:

```osty
let sales: Table = table.fromCsv(rawCsv)?    // cells tainted user_input
let schema = table.schemaFrom(["amount"], [table.FloatColumn])?
let typed = sales.coerce(schema)?
// typed.row(0).floatAt("amount"): #[taint("user_input")] Float
```

Coercion is *not* a sanitizer — it does not validate semantic
safety beyond type parsing. Sink-routing of typed cells still
requires explicit sanitization.
