### 10.45 Webhooks (`std.webhook`)

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

// Webhook intake: `Env` resolves the shared secret; `Clock` provides
// the timestamp used to bound the replay window. Both are explicit
// capability parameters (§20.9). The handler closure remains pure
// data — no ambient effect leaks into business logic.
fn handle(env: Env, clock: Clock, request: HttpRequest) -> Result<HttpResponse, Error> {
    let policy = webhook.stripe(env.require("STRIPE_WEBHOOK_SECRET")?)
    let store = webhook.emptyStore()

    let outcome = webhook.dispatch(request, policy, clock, store, fn(event) {
        // Business logic runs only after signature and replay checks pass.
        webhook.ack()
    })

    Ok(outcome.response)
}
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

#### 10.45.1 Webhook input flow tag

The bytes received from a webhook callback carry
`#[taint("user_input")]` because the sender is not authenticated
beyond signature verification. Even after signature passes, the
*payload* is still controlled by an external party.

```osty
fn handle(env: Env, clock: Clock, req: HttpRequest) -> Result<HttpResponse, Error> {
    let policy = webhook.stripe(env.require("STRIPE_WEBHOOK_SECRET")?)
    let store = webhook.emptyStore()

    let outcome = webhook.dispatch(req, policy, clock, store, fn(event) {
        // event.payload: #[taint("user_input")] Bytes
        // — process under the same trust assumption as any HTTP body
        webhook.ack()
    })
    Ok(outcome.response)
}
```

The signature check is a *replay-prevention* guarantee, not a
declassification of the payload. Code processing the event payload
must apply the same flow-tag rules as ordinary user input.

#### 10.45.2 Replay window and Clock determinism

The `webhook.policy` configures an acceptable replay window (e.g.
"timestamps older than 5 minutes are rejected"). The check uses
`clock.now()` against the event's signed timestamp; deterministic
testing requires `FakeClock`:

```osty
#[test]
fn test_replay_rejected() {
    let env = std.capability.testing.FakeEnv(vars = {"STRIPE_WEBHOOK_SECRET": "test"})
    let clock = std.capability.testing.FakeClock(epoch_ms = 1_700_000_000_000)
    let req = std.capability.testing.fakeStripeWebhook(timestamp = 1_699_999_900_000)  // 100s old

    let outcome = handle(env, clock, req)?
    testing.assert(outcome.response.status == 400)  // replay rejected
}
```

The replay window is part of the package's policy, not a v0.6
language feature; the `Clock` capability is the v0.6 mechanism
that makes the policy testable.

#### 10.45.3 Webhook and `#[error_contract]`

A handler returning `Result<HttpResponse, WebhookError>` carries
contract entries for each verification failure mode:

```osty
pub enum WebhookError {
    SignatureMismatch,
    Replay,
    PayloadParseFailed,
    HandlerFailed(Error),
}

#[error_contract(
    WebhookError.SignatureMismatch when "signature did not verify",
    WebhookError.Replay when "timestamp outside replay window",
    WebhookError.PayloadParseFailed when "payload was not valid JSON",
    WebhookError.HandlerFailed when "business handler returned Err",
)]
pub fn handle(env: Env, clock: Clock, req: HttpRequest) -> Result<HttpResponse, WebhookError> { ... }
```

Each variant maps to a distinct HTTP response shape; the contract
makes the mapping explicit at the type level, and the handler
function's match exhaustiveness benefits from the contract pruning.
