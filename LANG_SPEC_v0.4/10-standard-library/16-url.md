### 10.16 URL (`std.url`)

URL parsing and building.

```osty
use std.url

let u = url.parse("https://example.com:8080/path?q=1&r=2#top")?

u.scheme       // "https"
u.host         // "example.com"
u.port         // Some(8080)
u.path         // "/path"
u.query        // Map<String, String>
u.fragment     // Some("top")

let built = Url.builder()
    .scheme("https")
    .host("api.example.com")
    .path("/v1/users")
    .queryParam("limit", "10")
    .build()
    .toString()
```

API:

```
url.parse(text: String) -> Result<Url, Error>
url.join(base: String, relative: String) -> Result<String, Error>

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
