### 10.28 SQL (`std.sql`)

`std.sql` provides safe SQL fragment and statement builders. It does not
open database connections or execute queries; drivers such as SQLite or
Postgres should accept the `Query` value produced by this module.

`SqlIdent` (the type produced by `sql.quoteIdent` / `sql.quotePath`) is
a v0.6 sealed-construct type (§3.4.5, G40) — external literal
construction is rejected. Identifiers reach a `Db.query` sink only
through this validated path:

| Path | Yields | Sealed? |
|---|---|---|
| `sql.quoteIdent("users")?` | `SqlIdent` | yes — sole construction route |
| `SqlIdent { value: "..." }` | — | rejected (`E0420`) |
| Raw `String` concatenation into SQL text | — | flagged at the sink (`E0901` if input is tainted) |

The `Query` struct itself is *not* sealed — it is constructed by every
builder in this module — but its `sql: String` field is registered as
`#[trust("sql_safe")]` because the builder guarantees parameterized
form. Direct mutation of `Query.sql` via field write would invalidate
that promise, so the field is `pub` *getter only* in v0.6 (consistent
with §3.4.4).

```osty
use std.sql

let active = sql.eq("users.active", sql.boolean(true))?
let base = sql.selectWhere("users", ["id", "email"], active)?
let ordered = sql.orderBy(base, [sql.asc("email")?])?
let q = sql.limit(ordered, 100)?

let postgresText = sql.render(q, sql.postgresDialect())
let debugText = sql.debugSql(q)
```

Core types:

```osty
pub enum Dialect { Generic, Postgres, MySql, Sqlite }

pub enum Value {
    SqlNull,
    SqlString(String),
    SqlInt(Int),
    SqlFloat(Float),
    SqlBool(Bool),
    SqlRaw(String),
}

pub struct Query { pub sql: String, pub params: List<Value> }
pub struct Condition { pub sql: String, pub params: List<Value> }
pub struct Assignment { pub column: String, pub value: Value }
pub struct Order { pub column: String, pub descending: Bool }
```

Builders:

```osty
sql.select(table, columns) -> Result<Query, Error>
sql.selectWhere(table, columns, condition) -> Result<Query, Error>
sql.insert(table, assignments) -> Result<Query, Error>
sql.update(table, assignments, condition) -> Result<Query, Error>
sql.deleteFrom(table, condition) -> Result<Query, Error>
sql.orderBy(query, orders) -> Result<Query, Error>
sql.limit(query, n) -> Result<Query, Error>
sql.offset(query, n) -> Result<Query, Error>
```

Conditions:

```osty
sql.eq(column, value) -> Result<Condition, Error>
sql.ne(column, value) -> Result<Condition, Error>
sql.gt(column, value) -> Result<Condition, Error>
sql.gte(column, value) -> Result<Condition, Error>
sql.lt(column, value) -> Result<Condition, Error>
sql.lte(column, value) -> Result<Condition, Error>
sql.like(column, value) -> Result<Condition, Error>
sql.isNull(column) -> Result<Condition, Error>
sql.isNotNull(column) -> Result<Condition, Error>
sql.inList(column, values) -> Result<Condition, Error>
sql.allOf(conditions) -> Condition
sql.anyOf(conditions) -> Condition
```

Identifier and value helpers:

```osty
sql.quoteIdent(name) -> Result<String, Error>
sql.quotePath(path) -> Result<String, Error>
sql.literal(value) -> String
sql.placeholder(dialect, index) -> String
sql.genericDialect() -> Dialect
sql.postgresDialect() -> Dialect
sql.mysqlDialect() -> Dialect
sql.sqliteDialect() -> Dialect
sql.render(query, dialect) -> String
sql.debugSql(query) -> String
```

Behavior:

- Identifiers must be ASCII SQL identifiers: `[A-Za-z_][A-Za-z0-9_]*`.
  `quotePath` accepts dot-separated paths and final `*`, such as
  `users.email` or `users.*`.
- Builders quote identifiers and use `?` placeholders internally. `render`
  rewrites placeholders for dialects; Postgres uses `$1`, `$2`, and the
  generic/MySQL/SQLite modes keep `?`.
- `literal` and `debugSql` are for diagnostics, migrations, or generated SQL
  text. Runtime query execution should prefer `Query.params` over string
  interpolation.

#### 10.28.1 SQL builder determinism

Every builder in `std.sql` produces deterministic output —
identical inputs yield byte-identical `Query` values. This is
critical for:

- **`#[pure]` cache keys**: a function that derives a SQL
  fingerprint hashes the rendered query text and expects bit-
  identical output across runs.
- **Migration verification**: the same schema definition must
  produce the same SQL across runs to validate migrations.
- **`#[golden]` tests**: snapshot files of generated SQL can be
  compared byte-exact.

The determinism includes:

- Column ordering (preserves `select(table, columns)` argument
  order).
- Conditions in `allOf`/`anyOf` join in argument order.
- `orderBy` clauses join in argument order with the dialect's
  default `NULLS FIRST` / `NULLS LAST` (Postgres convention is
  preserved as-is; MySQL/SQLite emit no `NULLS` clause).
- Placeholder numbering follows `Query.params` order.

#### 10.28.2 SqlIdent and parameterization

`SqlIdent` (returned from `sql.quoteIdent`) is sealed (§3.4.5) —
external `SqlIdent { value: "..." }` literals are rejected
(`E0420`). The seal ensures every identifier reaching a SQL sink
has been validated against the `[A-Za-z_][A-Za-z0-9_]*` pattern.

```osty
fn buildOrderBy(col: String) -> Result<Order, Error> {
    let ident = sql.quoteIdent(col)?      // validates the column name
    Ok(sql.asc(ident)?)
}
```

If `col` is tainted (`#[taint("user_input")]`), `sql.quoteIdent`
acts as a sanitizer (`#[sanitizes("user_input", into = "sql_safe")]`)
— the resulting `SqlIdent` carries `sql_safe`, acceptable for
`Query` construction.

#### 10.28.3 Dialect-specific render output

`sql.render(query, dialect)` converts the internal placeholder
form (`?`) to the dialect's expected form:

| Dialect | Placeholder | Identifier quote | NULL ordering |
|---|---|---|---|
| `Generic` | `?` | `"name"` | dialect default |
| `Postgres` | `$1, $2, ...` | `"name"` | explicit `NULLS FIRST`/`LAST` |
| `MySql` | `?` | `` `name` `` | implicit |
| `Sqlite` | `?` | `"name"` | implicit |

The dialect choice is a runtime decision (typically inferred from
the `db.Config`). Generated SQL text is byte-identical for the
same `(query, dialect)` pair.
