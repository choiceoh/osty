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
