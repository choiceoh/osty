package registry

import (
	"encoding/json"
	"os"
	"sync"
)

// Authorizer decides whether a bearer token is permitted to publish
// or yank the package named `pkg`. The server calls Authorize for
// every mutating request; read-only endpoints skip it.
//
// Implementations should treat an empty token as anonymous (and
// typically return ErrUnauthorized).
type Authorizer interface {
	Authorize(token, pkg string) error
}

// AllowAll is the trivial Authorizer — any non-empty token is
// accepted for any package. Intended for development / local
// registries where you are the only publisher.
type AllowAll struct{}

// Authorize returns ErrUnauthorized on empty input, nil otherwise.
func (AllowAll) Authorize(token, _ string) error {
	if token == "" {
		return ErrUnauthorized
	}
	return nil
}

// TokenAuth is a simple file-backed Authorizer. Tokens and their
// per-token scopes are read from a JSON file on disk. The file
// format is intentionally small:
//
//	{
//	  "tokens": [
//	    {"token": "secret1", "subject": "alice", "allow_all": true},
//	    {"token": "secret2", "subject": "bob",   "packages": ["bobs-lib"]}
//	  ]
//	}
//
// A token without `allow_all` may only publish packages whose name
// appears in its `packages` list.
type TokenAuth struct {
	mu      sync.RWMutex
	tokens  map[string]TokenRecord
}

// TokenRecord is one entry in the token database.
type TokenRecord struct {
	Token    string   `json:"token"`
	Subject  string   `json:"subject,omitempty"`
	AllowAll bool     `json:"allow_all,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

type tokenFile struct {
	Tokens []TokenRecord `json:"tokens"`
}

// LoadTokenAuth reads path and returns an authorizer populated from
// it. A non-existent file yields an authorizer with zero tokens
// (every publish will be rejected with ErrUnauthorized) — callers
// decide whether to treat that as fatal.
func LoadTokenAuth(path string) (*TokenAuth, error) {
	ta := &TokenAuth{tokens: map[string]TokenRecord{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ta, nil
		}
		return nil, err
	}
	defer f.Close()
	var tf tokenFile
	if err := json.NewDecoder(f).Decode(&tf); err != nil {
		return nil, err
	}
	for _, t := range tf.Tokens {
		if t.Token == "" {
			continue
		}
		ta.tokens[t.Token] = t
	}
	return ta, nil
}

// Add inserts or replaces a token record. Useful for tests and for
// CLI-driven token provisioning.
func (t *TokenAuth) Add(rec TokenRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tokens == nil {
		t.tokens = map[string]TokenRecord{}
	}
	t.tokens[rec.Token] = rec
}

// Records returns a snapshot of the current token database in
// insertion-independent order. Callers should treat the returned
// slice as read-only.
func (t *TokenAuth) Records() []TokenRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]TokenRecord, 0, len(t.tokens))
	for _, r := range t.tokens {
		out = append(out, r)
	}
	return out
}

// WriteTokenAuth serializes the token database to path using the
// same JSON schema LoadTokenAuth expects. File mode is 0600 because
// the contents are credentials.
func WriteTokenAuth(ta *TokenAuth, path string) error {
	tf := tokenFile{Tokens: ta.Records()}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(tf)
}

// Authorize returns nil if the token can act on pkg, or an
// ErrUnauthorized / ErrForbidden sentinel otherwise.
func (t *TokenAuth) Authorize(token, pkg string) error {
	if token == "" {
		return ErrUnauthorized
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	rec, ok := t.tokens[token]
	if !ok {
		return ErrUnauthorized
	}
	if rec.AllowAll {
		return nil
	}
	for _, p := range rec.Packages {
		if p == pkg {
			return nil
		}
	}
	return ErrForbidden
}
