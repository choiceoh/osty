### 10.16 URL (`std.url`)

URL parsing and building.

`Url` is a v0.6 sealed-construct type (§3.4.5, G40) — external
struct literals (`Url { scheme: ..., host: ..., ... }`) are rejected.
Values are constructed only via `url.parse(text)?` or
`Url.builder()...build()`, both of which validate the result. This
is what makes `Url` acceptable to `#[requires("url_safe")]` sinks
like `http.redirect` (§21.8) — the type system can guarantee that a
`Url` value passed to a redirect target was vetted by the parser, not
spliced together from user input.

```osty
use std.url

// Pure — capability-free. Parsing is the only way to drop an
// untrusted `String` into `Url` shape.
let u: Url = url.parse("https://example.com:8080/path?q=1&r=2#top")?

u.scheme       // "https"
u.host         // "example.com"
u.port         // Some(8080)
u.path         // "/path"
u.query        // Map<String, String>
u.fragment     // Some("top")

// Builder produces a validated `Url` — invalid configurations
// surface from `.build()`.
let built: Url = Url.builder()
    .scheme("https")
    .host("api.example.com")
    .path("/v1/users")
    .queryParam("limit", "10")
    .build()?

// `toString` emits the canonical reverse of `parse` — the round-trip
// `parse(u.toString())? == u` holds for every valid `Url`.
let canonical: String = built.toString()
```

API:

```
// Pure — capability-free.
url.parse(text: String) -> Result<Url, Error>
url.join(base: Url, relative: String) -> Result<Url, Error>

pub struct Url {
    pub scheme: String,
    pub host: String,
    pub port: Int?,
    pub path: String,
    pub query: Map<String, String>,
    pub fragment: String?,

    fn toString(self) -> String
    fn queryValues(self, key: String) -> List<String>
}
```

#### Information flow interaction

`url.parse(s)` is registered as a sanitizer with
`#[sanitizes("user_input", into = "url_safe")]` (§21.6). Any `String`
flowing through `url.parse` (and surviving as `Ok(u)`) re-emerges
with the `url_safe` flow tag, so the resulting `Url` is acceptable
to `#[requires("url_safe")]` sinks. A `Url` that originated from
the builder (`Url.builder()...build()?`) is also `url_safe` because
the builder rejects malformed inputs at `.build()` time.

External literal construction is `E0420`. Authors who need to
*assemble* a URL from validated parts must go through the builder,
which is the only sealed-aware path.
