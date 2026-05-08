### 10.41 Keychain / Secrets (`std.keychain`, `std.secrets`)

`std.keychain` is the portable credential-store facade for API keys, tokens,
and other small application secrets. It stores secrets by `(service, account)`
in the OS credential store instead of in project files.

> **v0.6 capability note**: keychain access is a host effect — the
> implementation invokes Keychain Services on macOS, Credential Manager
> on Windows, etc. Reads/writes funnel through the ambient `Process`
> capability (§20.9.6) at the runtime layer. Library code that wants
> hermetic tests should accept a `KeychainStore` parameter instead of
> calling the module-level functions directly; a deterministic fake is
> available as `std.capability.testing.FakeKeychain`. A capability-shaped
> wrapper type is tracked as Phase 5 follow-up.

```osty
use std.keychain

// Script / entry-point convenience surface (ambient `process` binding):
keychain.setApiKey("openrouter", "sk-...")?
let apiKey = keychain.getApiKey("openrouter")?
```

The runtime backend is platform-specific:

- macOS: Keychain Services (`Security.framework`)
- Windows: Credential Manager
- Other targets: backend is reported as unavailable and read/write operations
  return `Err`

Core surface:

```osty
pub fn backend() -> String
pub fn isAvailable() -> Bool
pub fn get(service: String, account: String) -> Result<String, Error>
pub fn set(service: String, account: String, secret: String) -> Result<(), Error>
pub fn delete(service: String, account: String) -> Result<(), Error>
pub fn getApiKey(provider: String) -> Result<String, Error>
pub fn setApiKey(provider: String, secret: String) -> Result<(), Error>
pub fn deleteApiKey(provider: String) -> Result<(), Error>
```

`std.secrets` is a convenience facade over `std.keychain` for the common
API-key/token case. It uses the default service name `osty.api` and treats the
caller-provided `name` or `provider` as the credential account.

#### 10.41.1 Keychain values and information flow

A secret returned by `keychain.get(service, account)` carries
`#[taint("keychain_input")]` — it is data from outside the
program's trust boundary, even though the OS keychain is generally
considered trusted. The taint marker forces explicit sanitization
or sink-routing decisions:

```osty
fn callApi(net: Net, key: #[taint("keychain_input")] String) -> ... {
    // The raw key flows into the HTTP Authorization header.
    // Headers are not a registered sink, so no sanitization is required.
    net.httpClient().request(http.newRequest(http.Get, "...")
        .withBearerToken(key))
}
```

If the key were to flow into a SQL `WHERE` clause (rare but
possible — querying audit logs by key), the standard `sql_safe`
sanitization would apply.

#### 10.41.2 Keychain testing

`std.capability.testing.FakeKeychain` provides an in-memory
keychain for tests:

```osty
fn test_loadApiKey() {
    let kc = std.capability.testing.FakeKeychain()
    kc.setApiKey("openrouter", "test-key-123")?
    let key = loadApiKey(kc, "openrouter")?
    testing.assertEq(key, "test-key-123")
}
```

The fake is process-local and fresh per test. There is no global
state to worry about; tests do not interfere with each other.

For production builds, the host backend dispatches to the OS
credential store. Test builds default to `FakeKeychain` unless the
test explicitly opts into the real backend (rare).
