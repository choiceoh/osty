### 10.24 HTTP (`std.http`)

HTTP client/server ergonomics built on top of three runtime-owned
transport primitives.

> **v0.6 migration**: HTTP client construction routes through `Net`
> capability (`net.httpClient()`, §20.9.5). Two Phase 5 information-flow
> sinks are added: `http.redirect(target)` requires `url_safe`
> (sanitize via `std.url.encode` or `Url.parse(...)?`), and
> `http.respondHtml(body)` requires `html_safe` (sanitize via
> `std.html.escape` or use auto-escape templates). See §21.8 for the
> full sink catalogue and §21.12.4 / §21.12.5 for vulnerable→fixed
> patterns.

HTTP client/server ergonomics built on top of three runtime-owned
transport primitives:

- `http.request(req)` — full client request
- `http.get(url, headers)` — convenience GET primitive
- `http.serve(addr, handler)` — server entry point

Everything else in `std.http` is pure Osty convenience code layered on
those primitives: request and response builders, query/form codecs,
cookie and auth helpers, media-type parsing, problem responses, and a
router with path parameters.

```osty
use std.http

// Library code receives a `Net` capability and constructs the HTTP
// client off it (§20.9.5). The client itself is then passed wherever
// requests need to flow, so reachability of network effects is visible
// at every call boundary.
fn createUser(net: Net, token: String) -> Result<User, Error> {
    let client = net.httpClient()

    let req = http.newRequest(http.Post, "https://api.example.com/users")
        .withBearerToken(token)
        .withQueryValue("expand", "profile")
        .withJson(UserCreate { name: "Ada" })

    let created = client.request(req)?
        .requireStatus(201)?
    created.json::<User>()
}

fn loginAndCookie(net: Net, user: String, pass: String) -> Result<Cookie, Error> {
    let client = net.httpClient()
    let login = client.postForm("https://example.com/login", {
        "username": [user],
        "password": [pass],
    })?
    login.setCookie()?.orError("missing Set-Cookie")
}

// Server side: routes are pure data; the transport binding requires
// `Net`. `http.respondHtml` requires the `html_safe` flow tag (§21.8).
fn buildRouter() -> Router {
    let mut router = http.newRouter()
    router.get("/health", |req, params| {
        let _ = req
        let _ = params
        Ok(http.okText("ok"))
    })
    router.get("/users/:id", |req, params| {
        let _ = req
        Ok(http.okJson(UserView {
            id: params.get("id").unwrap(),
        }))
    })
    router
}

fn run(net: Net) -> Result<(), Error> {
    let router = buildRouter()
    net.httpServer().serve(":8080", |req| http.dispatch(router, req))
}

#[ambient(net)]
fn main() {
    let _ = run(net)?
}
```

API:

```osty
pub type Headers = Map<String, String>
pub type QueryValues = Map<String, List<String>>
pub type FormValues = QueryValues
pub type CookieJar = Map<String, String>
pub type Params = Map<String, String>
pub type Handler = fn(Request) -> Result<Response, Error>
pub type RouteHandler = fn(Request, Params) -> Result<Response, Error>

// Network-effecting primitives — methods on the client / server objects
// returned by `Net.httpClient()` / `Net.httpServer()` (§20.9.5).
HttpClient.request(self, req: Request) -> Result<Response, Error>
HttpClient.get(self, url: String, headers: Headers = {:}) -> Result<Response, Error>
HttpClient.send(self, method: Method, url: String, body: Bytes, headers: Headers = {:}) -> Result<Response, Error>
HttpClient.head(self, ...) / post(self, ...) / put(self, ...) / patch(self, ...) / delete(self, ...) / options(self, ...) / trace(self, ...) / connect(self, ...)
HttpClient.sendText(self, ...) / sendJson(self, ...) / sendForm(self, ...)
HttpClient.postText(self, ...) / putText(self, ...) / patchText(self, ...)
HttpClient.postJson(self, ...) / putJson(self, ...) / patchJson(self, ...)
HttpClient.postForm(self, ...) / putForm(self, ...) / patchForm(self, ...)
HttpServer.serve(self, addr: String, handler: Handler) -> Result<(), Error>

// Pure helpers — operate on values, no capability needed.
http.newRequest(method: Method, url: String) -> Request
http.dispatch(router: Router, req: Request) -> Result<Response, Error>

// Query/form codecs
http.parseQuery(text: String) -> Result<QueryValues, Error>
http.formatQuery(values: QueryValues) -> String
http.parseForm(text: String) -> Result<FormValues, Error>
http.formatForm(values: FormValues) -> String

// Media types / auth / cookies
http.parseMediaType(text: String) -> MediaType?
http.parseSameSite(text: String) -> Result<SameSite, Error>
http.parseSetCookie(text: String) -> Result<Cookie, Error>
http.cookie(name: String, value: String) -> Cookie

pub struct MediaType {
    pub value: String,
    pub params: Map<String, String>,

    fn toString(self) -> String
    fn parameter(self, name: String) -> String?
    fn is(self, value: String) -> Bool
}

pub struct BasicAuth {
    pub username: String,
    pub password: String,

    fn toHeaderValue(self) -> String
}

pub enum SameSite {
    Strict,
    Lax,
    NoneSite,

    fn toString(self) -> String
}

pub struct Cookie {
    pub name: String,
    pub value: String,
    pub path: String,
    pub domain: String,
    pub maxAge: Int,
    pub secure: Bool,
    pub httpOnly: Bool,
    pub sameSite: SameSite?,

    fn toString(self) -> String
}

// Request / response
pub struct Request {
    pub method: Method,
    pub url: String,
    pub headers: Headers,
    pub body: Bytes,

    fn header(self, name: String) -> String?
    fn contentType(self) -> String?
    fn mediaType(self) -> MediaType?
    fn accepts(self, value: String) -> Bool
    fn path(self) -> String
    fn query(self) -> Result<QueryValues, Error>
    fn queryValue(self, key: String) -> Result<String?, Error>
    fn queryValues(self, key: String) -> Result<List<String>, Error>
    fn text(self) -> Result<String, Error>
    fn json<T>(self) -> Result<T, Error>
    fn form(self) -> Result<FormValues, Error>
    fn cookies(self) -> CookieJar
    fn cookie(self, name: String) -> String?
    fn bearerToken(self) -> String?
    fn basicAuth(self) -> Result<BasicAuth?, Error>

    fn withHeader(self, name: String, value: String) -> Request
    fn withBody(self, body: Bytes) -> Request
    fn withText(self, body: String) -> Request
    fn withJson<T>(self, value: T) -> Request
    fn withForm(self, values: FormValues) -> Request
    fn withQueryValue(self, name: String, value: String) -> Request
    fn withQuery(self, values: QueryValues) -> Request
    fn withCookie(self, name: String, value: String) -> Request
    fn withBearerToken(self, token: String) -> Request
    fn withBasicAuth(self, username: String, password: String) -> Request
}

pub struct Response {
    pub status: Int,
    pub headers: Headers,
    pub body: Bytes,

    fn statusText(self) -> String
    fn contentType(self) -> String?
    fn mediaType(self) -> MediaType?
    fn isSuccess(self) -> Bool
    fn isRedirect(self) -> Bool
    fn isClientError(self) -> Bool
    fn isServerError(self) -> Bool
    fn location(self) -> String?
    fn etag(self) -> String?
    fn setCookie(self) -> Result<Cookie?, Error>
    fn requireSuccess(self) -> Result<Response, Error>
    fn requireStatus(self, status: Int) -> Result<Response, Error>
    fn text(self) -> Result<String, Error>
    fn json<T>(self) -> Result<T, Error>

    fn withStatus(self, status: Int) -> Response
    fn withHeader(self, name: String, value: String) -> Response
    fn withText(self, body: String) -> Response
    fn withJson<T>(self, value: T) -> Response
    fn withCookie(self, cookie: Cookie) -> Response
    fn withLocation(self, location: String) -> Response
}

// Response constructors
http.response(status: Int, body: Bytes, headers: Headers = {:}) -> Response
http.textResponse(status: Int, body: String, headers: Headers = {:}) -> Response
http.jsonResponse<T>(status: Int, value: T, headers: Headers = {:}) -> Response

http.ok(...) / okText(...) / okJson(...)
http.created(...) / createdAt(...)
http.accepted(...)
http.noContent(...)
http.badRequest(...) / unauthorized(...) / forbidden(...) / notFound(...) / methodNotAllowed(...)
http.conflict(...) / gone(...) / unprocessableContent(...) / tooManyRequests(...)
http.internalServerError(...) / serviceUnavailable(...)
http.redirect(...) / movedPermanently(...) / found(...) / seeOther(...)
http.temporaryRedirect(...) / permanentRedirect(...)

pub struct Problem {
    pub type: String,
    pub title: String,
    pub status: Int,
    pub detail: String,
    pub instance: String,

    fn toResponse(self, headers: Headers = {:}) -> Response
}

http.problem(status: Int, title: String, detail: String = "", headers: Headers = {:}) -> Response

// Router
pub struct Route {
    pub methods: List<Method>,
    pub pattern: String,
    pub handler: RouteHandler,
}

pub struct RouteMatch {
    pub route: Route,
    pub params: Params,
}

pub struct Router {
    pub routes: List<Route>,

    fn route(self, methods: List<Method>, pattern: String, handler: RouteHandler)
    fn handle(self, method: Method, pattern: String, handler: RouteHandler)
    fn handleAny(self, pattern: String, handler: RouteHandler)
    fn get(self, pattern: String, handler: RouteHandler)
    fn post(self, pattern: String, handler: RouteHandler)
    fn put(self, pattern: String, handler: RouteHandler)
    fn patch(self, pattern: String, handler: RouteHandler)
    fn delete(self, pattern: String, handler: RouteHandler)
    fn head(self, pattern: String, handler: RouteHandler)
    fn options(self, pattern: String, handler: RouteHandler)
    fn trace(self, pattern: String, handler: RouteHandler)
    fn connect(self, pattern: String, handler: RouteHandler)
    fn find(self, req: Request) -> RouteMatch?
}

http.newRouter() -> Router
```

**Behavior.**

- `Headers` remains `Map<String, String>` for compatibility with the
  current runtime bridge. Header names are canonicalized (`content-type`
  and `Content-Type` collapse to the same logical key), but only one
  logical value is stored per name.
- `parseQuery` and `parseForm` preserve repeated keys by using
  `Map<String, List<String>>`. Query formatting is percent-encoded; form
  formatting uses the usual `+`-for-space convention.
- `Request.path()` accepts both absolute URLs and origin-form paths
  (`/users/1?expand=true`). `Router` patterns support literal segments,
  `:param` captures, and final `*rest` catch-alls.
- `Router.dispatch` returns `404 Not Found` when no route pattern matches
  and `405 Method Not Allowed` with an `Allow` header when the path
  matches but the method does not.
- `problem(...)` and `Problem.toResponse()` emit
  `application/problem+json` bodies.
- Server code dispatches routers through `http.dispatch(router, req)`. The
  transport primitive is `HttpServer.serve(addr, handler)` on the
  capability-supplied server object (legacy `http.serve(addr, handler)`
  desugars there under `--legacy-globals`).

**Current runtime limits.**

- `std.http` does **not** invent transport capabilities beyond the
  existing runtime primitives. There are no standard-library-level retry,
  timeout, redirect-following, streaming-body, or connection-pool knobs
  yet because the current runtime bridge cannot honor them.
- `Response.withCookie` and `Response.setCookie` model a single
  `Set-Cookie` header line because `Headers` is single-valued. Higher
  multiplicity will require a future runtime/header representation change.

#### 10.24.1 HTTP and information flow — sink registry

`std.http` registers four sinks that participate in the v0.6 flow
type system (§21.18.3):

| Sink | Required tag | Trigger |
|---|---|---|
| `http.redirect(target)` | `url_safe` | `target` is a redirect Location |
| `http.movedPermanently(target)` | `url_safe` | same |
| `http.found(target)` / `http.seeOther(target)` | `url_safe` | same |
| `http.respondHtml(body)` | `html_safe` | body is rendered as HTML |
| `template.render(...)` | `html_safe` | template body is HTML |

Sources that produce tainted strings (`req.queryParam`,
`req.body`, `req.cookie`, `req.path`) must pass through the
appropriate sanitizer before reaching these sinks.

#### 10.24.2 HTTP and capability matrix

Production HTTP code routes through three capability touchpoints:

1. **`Net` for connection setup** — `net.httpClient()` and
   `net.httpServer()` both consume a `Net` capability for the
   underlying transport.
2. **`Console` for logging** — `log.info` etc. are dispatched via
   ambient `Console` (§10.10).
3. **`Clock` for timeouts** — request timeout values are
   `Duration` constants; the actual deadline check uses the task's
   ambient cancellation, which `Clock.sleep` respects.

A test of a handler injects `FakeNet`, `FakeConsole`, and
`FakeClock` — a fully hermetic test environment.

#### 10.24.3 Body parsing and flow tag inheritance

`req.json::<T>()` returns `T` whose type carries `req.body`'s flow
tag set. This means a JSON-decoded user-input form inherits the
tag set element-wise:

```osty
struct UserCreate {
    pub email: String,
    pub name: String,
}

fn handler(req: Request) -> Result<Response, Error> {
    let body: #[taint("user_input")] UserCreate = req.json::<UserCreate>()?
    // body.email and body.name both carry user_input
    let email = Email.parse(body.email)?      // sanitizes → email_safe
    ...
}
```

The struct's fields all carry `user_input` because the parser
preserves the input's tag set on each output field. Authors who
need *per-field* tag narrowing use `#[taint_field]` (§21.5.8).

#### 10.24.4 Router pattern matching

`Router.find(req)` returns a `RouteMatch?` with extracted path
parameters. The pattern grammar:

| Pattern | Matches | Captures |
|---|---|---|
| `/users` | exact | none |
| `/users/:id` | one segment after `/users/` | `id` → segment |
| `/files/*rest` | one or more segments | `rest` → joined path |
| `/users/:id/posts/:pid` | two-level params | `id`, `pid` |
| `/static/*path` | catch-all suffix | `path` |

Pattern matching is deterministic — the first matching route in
registration order wins. Authors building dynamic routers should
register more-specific patterns before catch-alls.

Path parameters extracted from the URL carry
`#[taint("user_input")]`. Authors must sanitize before passing
into sinks. `params.get("id")` returns the raw string.

#### 10.24.5 Cookie and header attacks

The HTTP module does not implement automatic cookie sanitization.
Setting a cookie value containing CRLF (`\r\n`) is a header-
injection vulnerability — the request would split into two
responses. The recommended pattern:

```osty
fn safeCookie(name: String, value: #[taint("user_input")] String) -> Result<Cookie, Error> {
    if value.contains("\r") || value.contains("\n") {
        return Err(Error.new("invalid cookie value"))
    }
    Ok(http.cookie(name, value))
}
```

A future stdlib `std.http.cookieSafe` sanitizer is tracked for
Phase 5; v0.6 baseline requires manual validation.

#### 10.24.6 Server-side cancellation

A handler invoked via `http.dispatch` runs in the request task.
The task is cancelled when the client disconnects mid-request.
Long-running handlers should check cancellation periodically or
use cancellation-aware blocking calls:

```osty
fn slowHandler(req: HttpRequest) -> Result<HttpResponse, Error> {
    for chunk in processChunks(req.body) {
        thread.checkCancelled()?       // honor client disconnect
        publishChunk(chunk)?
    }
    Ok(http.okText(""))
}
```

`Net.read` and `Net.write` already honor cancellation; explicit
`thread.checkCancelled()` is needed only for CPU-bound work.
