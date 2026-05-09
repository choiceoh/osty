### 10.45 Webhooks (`std.webhook`)

- **Scope**: Osty stdlib spec — 10.45 Webhooks (`std.webhook`)
- **Type**: Standard library specification
`std.webhook` is the safe intake layer for HTTP callbacks from external
systems. It composes with `std.http`, `std.crypto`, `std.encoding`,
`std.json`, and `std.time` instead of owning a server runtime. The module
standardizes four things that should happen before business logic runs:

- verify the raw request body against the provider signature
- reject stale signed timestamps where the provider supplies one
- derive a stable delivery id for idempotency
- dispatch through a retry-safe outcome that separates duplicates, in-flight
  deliveries, handler failures, and intentional retry responses

```osty
use std.env
use std.http
use std.webhook

let policy = webhook.stripe(env.require("STRIPE_WEBHOOK_SECRET")?)
let store = webhook.emptyStore()

let outcome = webhook.dispatch(request, policy, store, fn(event) {
    // Business logic runs only after signature and replay checks pass.
    webhook.ack()
})

return outcome.response
```

Core types:

```osty
pub enum Provider { Stripe, GitHub, Supabase, Slack, Discord, Custom }

pub enum SignatureScheme {
    StripeV1,
    GitHubSha256,
    SlackV0,
    SupabaseSha256,
    HmacSha256Hex,
    HmacSha256Base64,
    DiscordEd25519,
    ExternallyVerified,
}

pub enum VerificationCode {
    Verified,
    MissingSecret,
    MissingSignature,
    InvalidSignature,
    MissingTimestamp,
    InvalidTimestamp,
    ReplayWindowExceeded,
    UnsupportedSignature,
}

pub enum DispatchStatus { Handled, Duplicate, InFlight, Rejected, RetryLater, Failed }

pub struct Policy
pub struct Verification
pub struct Event
pub struct Store
pub struct DispatchDecision
pub struct DispatchOutcome
```

Provider policies:

```osty
webhook.stripe(secret)
webhook.github(secret)
webhook.supabase(secret)
webhook.slack(secret)
webhook.discord(publicKey)
webhook.hmacSha256Hex(secret, header)
webhook.hmacSha256Base64(secret, header)
webhook.unsigned(provider)
```

The built-in provider policies use the provider's raw-body signature shape:

- Stripe: `Stripe-Signature`, signed payload `t.raw_body`, HMAC-SHA256 hex
  `v1`, default 5-minute tolerance.
- GitHub: `X-Hub-Signature-256`, `sha256=` + HMAC-SHA256 hex of the raw body,
  with `X-GitHub-Delivery` and `X-GitHub-Event` as id/type headers.
- Slack: `X-Slack-Signature`, signed payload `v0:timestamp:raw_body`,
  `X-Slack-Request-Timestamp`, default 5-minute tolerance.
- Supabase: configurable HMAC-SHA256 hex convention using
  `x-supabase-signature`; callers can override headers with `Policy` methods.
- Discord: models the required `X-Signature-Ed25519` and
  `X-Signature-Timestamp` headers but returns `UnsupportedSignature` until the
  runtime exposes Ed25519 verification. Use `policy.externallyVerified()` only
  when a host layer has already verified the request.

Verification and extraction:

```osty
webhook.verify(policy, headers, body) -> Verification
webhook.verifyRequest(request, policy) -> Verification
webhook.eventFromRequest(request, policy, verification) -> Event
webhook.header(headers, name) -> String?
webhook.bodyFingerprint(body) -> String
```

Dispatch:

```osty
webhook.dispatch(request, policy, store, handler) -> DispatchOutcome
webhook.ack() -> DispatchDecision
webhook.retryLater(message) -> DispatchDecision
webhook.reject(status, message) -> DispatchDecision
```

`dispatch` returns `200` for already processed deliveries so provider retries do
not re-run business logic. In-flight duplicates return `202`, handler errors
return `500` and clear the in-flight marker, and retry decisions preserve the
delivery as unprocessed so a later provider retry can run again.

Idempotency helpers:

```osty
webhook.emptyStore() -> Store
webhook.idempotencyKey(event) -> String
webhook.wasProcessed(store, key) -> Bool
webhook.isInFlight(store, key) -> Bool
webhook.markInFlight(store, key) -> Store
webhook.markProcessed(store, key) -> Store
webhook.clearInFlight(store, key) -> Store
```

`Store` is intentionally value-shaped. Small applications can keep it in memory;
larger services should persist the same `idempotencyKey(event)` in a database or
queue table before performing side effects.
