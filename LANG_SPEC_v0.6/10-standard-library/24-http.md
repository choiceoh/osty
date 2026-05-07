### 10.24 HTTP (`std.http`)

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

// Client
let req = http.newRequest(http.Post, "https://api.example.com/users")
    .withBearerToken(token)
    .withQueryValue("expand", "profile")
    .withJson(UserCreate { name: "Ada" })

let created = http.request(req)?
    .requireStatus(201)?
let user: User = created.json()?

// Forms and cookies
let login = http.postForm("https://example.com/login", {
    "username": ["ada"],
    "password": ["secret"],
})?

let csrf = login.setCookie()?.unwrap()

// Server
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

http.serve(":8080", |req| http.dispatch(router, req))
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

// Runtime-owned primitives
http.request(req: Request) -> Result<Response, Error>
http.get(url: String, headers: Headers = {:}) -> Result<Response, Error>
http.serve(addr: String, handler: Handler) -> Result<(), Error>

// Client helpers
http.newRequest(method: Method, url: String) -> Request
http.dispatch(router: Router, req: Request) -> Result<Response, Error>
http.send(method: Method, url: String, body: Bytes, headers: Headers = {:}) -> Result<Response, Error>
http.head(...) / post(...) / put(...) / patch(...) / delete(...) / options(...) / trace(...) / connect(...)
http.sendText(...) / sendJson(...) / sendForm(...)
http.postText(...) / putText(...) / patchText(...)
http.postJson(...) / putJson(...) / patchJson(...)
http.postForm(...) / putForm(...) / patchForm(...)

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
  transport primitive remains `http.serve(addr, handler)`.

**Current runtime limits.**

- `std.http` does **not** invent transport capabilities beyond the
  existing runtime primitives. There are no standard-library-level retry,
  timeout, redirect-following, streaming-body, or connection-pool knobs
  yet because the current runtime bridge cannot honor them.
- `Response.withCookie` and `Response.setCookie` model a single
  `Set-Cookie` header line because `Headers` is single-valued. Higher
  multiplicity will require a future runtime/header representation change.
