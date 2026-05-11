# Native checker daemon protocol

## Status

This document defines the long-lived stdio JSON-RPC protocol that will replace
the current fork-per-request `cmd/osty-native-checker` boundary used from
`internal/check/host_boundary.go`.

It is a protocol agreement only. The current shipped binary still speaks the
one-shot `api.CheckRequest` → `api.CheckResult` stdin/stdout contract. The
daemon transport and host switchover can land later without reopening the wire
shape debate.

## Goals

- Stop paying one process spawn per LSP-triggered check.
- Keep the checker wire payload aligned with `internal/selfhost/api`.
- Reuse the existing stdio JSON-RPC framing and lifecycle rules that the LSP
  server already follows.
- Make shutdown and request correlation explicit before the host boundary grows
  daemon management code.

## Transport

- Wire format: JSON-RPC 2.0 over stdio.
- Framing: `Content-Length: <bytes>\r\n\r\n<body>`.
- Encoding: UTF-8 JSON payloads.
- Request IDs: JSON-RPC `id` is required on every request and is echoed back
  unchanged in the response. Clients may use strings or integers.
- Server responses may arrive out of order, so callers must match by `id` even
  if the first implementation processes requests serially.

## Lifecycle

The daemon is single-session and follows the same high-level lifecycle as the
LSP server:

1. Client starts the process.
2. Client sends `initialize`.
3. Client sends one or more check requests.
4. Client sends `shutdown`.
5. Client sends `exit`.

Rules:

- No check request is valid before `initialize` succeeds.
- After `shutdown`, the daemon must reject every request except `exit`.
- `exit` is a notification. It closes the session whether or not `shutdown`
  arrived first.
- Expected process exit code mirrors LSP:
  - `0` when `shutdown` was received before `exit`
  - `1` when the client exits without a prior `shutdown`

## Methods

### `initialize`

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "client": {
      "name": "osty-lsp"
    }
  }
}
```

Response:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": 1,
    "server": {
      "name": "osty-native-checker",
      "mode": "daemon"
    },
    "capabilities": {
      "checkSource": true,
      "checkPackage": true
    }
  }
}
```

Contract:

- `protocolVersion` is required and currently must be `1`.
- The response advertises only capabilities that the process will honor for the
  entire session.
- Repeated `initialize` requests are invalid.

### `checker/checkSource`

Request params wrap the existing `api.CheckRequest` source mode:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "checker/checkSource",
  "params": {
    "source": "fn main() { let answer = 1 }"
  }
}
```

Response result is the existing `api.CheckResult` JSON object.

### `checker/checkPackage`

Request params wrap the existing structured package mode:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "checker/checkPackage",
  "params": {
    "package": {
      "files": [
        { "path": "main.osty", "source": "fn main() {}" }
      ]
    }
  }
}
```

Response result is again `api.CheckResult`.

Contract for both check methods:

- The payload fields stay source-compatible with `api.CheckRequest`.
- Exactly one mode is requested per RPC call.
- The daemon must call `EnsureStableIDs()` before returning so daemon and
  one-shot callers observe identical stable IDs.

### `shutdown`

`shutdown` is a request and returns `null` on success.

After the response is sent, the daemon enters the shutdown state and accepts no
further work requests.

### `exit`

`exit` is a notification:

```json
{
  "jsonrpc": "2.0",
  "method": "exit"
}
```

The process should terminate immediately after handling it.

## Errors

The daemon uses normal JSON-RPC error responses.

- Parse/framing failure: `-32700`
- Invalid request/method/params: `-32600`, `-32601`, `-32602`
- Request before `initialize`: `-32002` ("server not initialized"), matching
  the LSP-side policy already used in `internal/lsp`
- Request after `shutdown`: `-32600`
- Checker execution failure: server error in the `-32000..-32099` range with a
  message suitable for surfacing in host diagnostics/logs

`shutdown` remains best-effort: individual failed checks must not force the
session closed.

## Compatibility and rollout

- The current one-shot stdin JSON mode remains supported until every host path
  that shells out can speak daemon JSON-RPC.
- Host callers should feature-detect daemon mode by sending `initialize` only to
  binaries explicitly launched in daemon mode; legacy one-shot binaries should
  keep their current no-handshake behavior.
- The daemon protocol intentionally reuses `api.CheckRequest` / `api.CheckResult`
  payloads so `internal/check/host_boundary.go` can switch transports without a
  second data-shape migration.
- Cancellation, progress notifications, and cross-request cache invalidation are
  intentionally out of scope for protocol v1.
