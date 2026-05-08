### 10.43 Supabase (`std.supabase`)

`std.supabase` is a lightweight Supabase API layer on top of `std.http`. It
does not embed a database driver or storage client runtime. Instead, it builds
ready-to-send HTTP requests for Supabase's stable gateway paths:

- PostgREST data API under `/rest/v1`
- Auth under `/auth/v1`
- Storage under `/storage/v1`
- Edge Functions under `/functions/v1`
- GraphQL under `/graphql/v1`

```osty
use std.supabase

// Library code receives `Env` (for credential lookup) and `Net` (for
// HTTP transport) capabilities (§20.9). The `TableQuery` value is
// pure data — only `supabase.send*` actually performs the request.
fn loadActiveTodos(env: Env, net: Net) -> Result<List<Todo>, Error> {
    let sb = supabase.fromEnv(env)?
    let q = supabase.from("todos")?
        .select("id,title,completed")
        .eq("completed", "false")?
        .orderDesc("created_at")?
        .withCount(supabase.CountExact)
        .range(0, 19)?

    let page = supabase.sendSelectPage::<List<Todo>>(net, sb, q)?
    Ok(page.value)
}

#[ambient(env, net)]
fn main() {
    let todos = loadActiveTodos(env, net)?
}
```

Core types:

```osty
pub struct Client
pub struct TableQuery
pub struct Prefer
pub struct StorageSort
pub struct PageInfo
pub struct Page<T>
pub struct ApiError

pub enum CountMode { CountNone, CountExact, CountPlanned, CountEstimated }
pub enum ReturningMode { ReturnDefault, ReturnMinimal, ReturnRepresentation }
pub enum ResolutionMode { ResolveDefault, MergeDuplicates, IgnoreDuplicates }
pub enum OrderDirection { Asc, Desc }
pub enum NullOrdering { NullsDefault, NullsFirst, NullsLast }
```

Configuration:

```osty
supabase.client(baseUrl, apiKey) -> Result<Client, Error>
supabase.project(projectRef, apiKey) -> Result<Client, Error>
supabase.local(apiKey) -> Result<Client, Error>
supabase.fromEnv() -> Result<Client, Error>
supabase.serviceRoleFromEnv() -> Result<Client, Error>
supabase.fromEnvNames(urlName, keyName) -> Result<Client, Error>

client.withAccessToken(jwt)
client.withServiceRoleKey(key)
client.withSchema(schema)
client.withClientInfo(value)
client.withHeader(name, value)
```

`headers(client)` emits the `apikey` header and `Authorization: Bearer ...`.
When no session token is configured, the bearer value is the API key itself so
the value exactly matches the `apikey` header. Non-`public` schemas add
`Accept-Profile` and `Content-Profile`.

`fromEnv()` reads `SUPABASE_URL` and `SUPABASE_ANON_KEY`.
`serviceRoleFromEnv()` reads `SUPABASE_URL` and
`SUPABASE_SERVICE_ROLE_KEY`, and configures the service role key as both the
`apikey` and bearer token. `fromEnvNames()` is the same pattern with custom
environment variable names.

PostgREST query helpers:

```osty
supabase.from(table) -> Result<TableQuery, Error>

query.select(columns)
query.eq(column, value)
query.neq(column, value)
query.gt(column, value)
query.gte(column, value)
query.lt(column, value)
query.lte(column, value)
query.like(column, pattern)
query.ilike(column, pattern)
query.isNull(column)
query.inList(column, values)
query.contains(column, value)
query.containedBy(column, value)
query.overlaps(column, value)
query.textSearch(column, query)
query.order(column, direction, nulls)
query.orderAsc(column)
query.orderDesc(column)
query.limit(n)
query.offset(n)
query.range(from, to)
query.withCount(mode)
query.returning(mode)
query.resolveDuplicates(mode)
query.single()
query.csv()
```

Pagination metadata:

```osty
supabase.parseContentRange(value) -> Result<PageInfo, Error>
supabase.pageInfo(response) -> Result<PageInfo?, Error>
supabase.responseCount(response) -> Result<Int?, Error>

page.returned()
page.isEmpty()
page.hasTotal()
page.hasNext()
```

`PageInfo` parses PostgREST `Content-Range` headers such as `0-19/123`
or `0-19/*`. It is intended to pair with `query.withCount(CountExact)`,
`CountPlanned`, or `CountEstimated`.

Requests:

```osty
supabase.selectHttpRequest(client, query)
supabase.insertHttpRequest(client, table, value)
supabase.insertWithPreferHttpRequest(client, table, value, prefer)
supabase.upsertHttpRequest(client, table, value)
supabase.upsertWithPreferHttpRequest(client, table, value, prefer)
supabase.updateHttpRequest(client, query, value)
supabase.deleteHttpRequest(client, query)
supabase.rpcHttpRequest(client, functionName, args)
supabase.rpcWithPreferHttpRequest(client, functionName, args, prefer)
supabase.rpcValueHttpRequest(client, functionName, jsonValue)
supabase.rpcValueWithPreferHttpRequest(client, functionName, jsonValue, prefer)
```

Convenience senders execute those requests through `std.http.request`, require
2xx responses, and decode JSON:

```osty
supabase.sendSelect<T>(client, query)
supabase.sendSelectPage<T>(client, query)
supabase.sendInsert<T, U>(client, table, value)
supabase.sendInsertWithPrefer<T, U>(client, table, value, prefer)
supabase.sendUpdate<T, U>(client, query, value)
supabase.sendDelete<T>(client, query)
supabase.sendRpc<T, U>(client, functionName, args)
supabase.sendRpcWithPrefer<T, U>(client, functionName, args, prefer)
```

Auth:

```osty
supabase.signUpHttpRequest(client, email, password)
supabase.passwordTokenHttpRequest(client, email, password)
supabase.refreshTokenHttpRequest(client, refreshToken)
supabase.logoutHttpRequest(client)
```

Storage and functions:

```osty
supabase.publicObjectUrl(client, bucket, path)
supabase.authenticatedObjectUrl(client, bucket, path)
supabase.objectUrl(client, bucket, path)
supabase.authenticatedObjectHttpRequest(client, bucket, path)
supabase.downloadObjectHttpRequest(client, bucket, path)
supabase.uploadObjectHttpRequest(client, bucket, path, body, contentType, upsert)
supabase.updateObjectHttpRequest(client, bucket, path, body, contentType)
supabase.removeObjectHttpRequest(client, bucket, paths)
supabase.signedObjectUrlHttpRequest(client, bucket, path, expiresInSeconds)
supabase.signedObjectUrlWithTransformHttpRequest(client, bucket, path, expiresInSeconds, transformJson)
supabase.signedObjectsHttpRequest(client, bucket, paths, expiresInSeconds)
supabase.storageSort(column, direction)
supabase.listObjectsHttpRequest(client, bucket, prefix, limit, offset)
supabase.listObjectsSortedHttpRequest(client, bucket, prefix, limit, offset, sort)
supabase.searchObjectsHttpRequest(client, bucket, prefix, search, limit, offset)

supabase.invokeFunctionHttpRequest(client, functionName, payload)
supabase.graphqlHttpRequest(client, bodyJson)
```

Signed download requests follow Supabase Storage's `/object/sign/...`
endpoints. List/search requests target `/object/list/{bucket}` and emit the
documented `prefix`, `limit`, `offset`, optional `sortBy`, and optional
`search` JSON fields.

Errors:

```osty
supabase.parseApiError(response) -> ApiError
supabase.requireSuccess(response) -> Result<http.Response, Error>
```

`ApiError` extracts the common Supabase/PostgREST JSON fields `code`,
`message`, `details`, and `hint`, while preserving the raw body for logs.

#### 10.43.1 Supabase capability surface

Production Supabase code touches three v0.6 capability points:

1. **`Env` for credential lookup** — `supabase.fromEnv(env)` reads
   `SUPABASE_URL` and `SUPABASE_ANON_KEY` (or
   `SUPABASE_SERVICE_ROLE_KEY` for service role).
2. **`Net` for HTTP transport** — `supabase.send*` consumes a
   `Net` capability to issue the actual request.
3. **No `Clock`/`Rng` directly** — Supabase request building is
   pure; ID generation (e.g. UUIDs for new rows) is delegated to
   `std.uuid` and routed through whichever `Rng`/`CryptoRng` the
   caller chooses.

Tests inject `FakeEnv` (with the API URL/key set) and `FakeNet`
(with canned PostgREST responses). The pure builder layer
(`supabase.from`, `q.select`, `q.eq`, etc.) is acceptable inside
`#[reproducible(scope = "target")]`; only the `send*` calls are
non-reproducible.

#### 10.43.2 Supabase and information flow

PostgREST query results carry `#[taint("net_input")]` on each row
field, like any other network response. When echoing a row back
into a sink (HTML, SQL via different DB, shell), apply the
appropriate sanitizer:

```osty
fn renderTodo(net: Net, sb: Client, id: Int) -> Result<String, Error> {
    let q = supabase.from("todos")?.select("title").eq("id", "{id}")?
    let row = supabase.sendSelectOne::<Todo>(net, sb, q)??
    Ok(std.html.escape(row.title))             // sanitize for html_safe sink
}
```

#### 10.43.3 Service role caveat

A `Client` configured with `serviceRoleFromEnv()` bypasses Row
Level Security (RLS). Service-role requests must be carefully
audited; `osty audit --supabase-service-role` enumerates every
client construction site that uses service role.

For tests, prefer `FakeNet` over real service-role calls — service
role compromises are common attack vectors when leaked into CI
logs.
