### 10.28 SQL (`std.sql`)

- **Scope**: Osty stdlib spec — 10.28 SQL (`std.sql`)
- **Type**: Standard library specification
`std.sql` provides safe SQL fragment and statement builders. It does not
open database connections or execute queries; drivers such as SQLite or
Postgres should accept the `Query` value produced by this module.

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
