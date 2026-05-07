### 10.41 Keychain / Secrets (`std.keychain`, `std.secrets`)

`std.keychain` is the portable credential-store facade for API keys, tokens,
and other small application secrets. It stores secrets by `(service, account)`
in the OS credential store instead of in project files.

```osty
use std.keychain

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
