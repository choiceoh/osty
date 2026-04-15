package registry

import "errors"

// Sentinel errors the backend returns so callers (both the Go
// server glue and external tools) can respond with the right HTTP
// status and a human-friendly message.
var (
	// ErrVersionExists signals an immutable-publish violation: a
	// client tried to re-upload (name, version) that is already
	// recorded. The server maps this to HTTP 409 Conflict.
	ErrVersionExists = errors.New("registry: version already published")

	// ErrChecksumMismatch signals that the body the client uploaded
	// hashed to something other than the advertised `Osty-Checksum`
	// header. Maps to HTTP 400 Bad Request.
	ErrChecksumMismatch = errors.New("registry: checksum mismatch")

	// ErrUnauthorized signals the request did not carry a valid
	// token (for publish/yank). Maps to HTTP 401.
	ErrUnauthorized = errors.New("registry: unauthorized")

	// ErrForbidden signals a valid token that is not allowed to act
	// on the requested package (e.g. publishing to somebody else's
	// name). Maps to HTTP 403.
	ErrForbidden = errors.New("registry: forbidden")
)
