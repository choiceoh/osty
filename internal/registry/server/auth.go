package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ErrUnauthorized is returned when an incoming token is unknown or
// has no scope for the requested operation. Handlers translate this
// to HTTP 401 (missing / unknown token) or 403 (known but insufficient
// scope) depending on the cause.
var ErrUnauthorized = errors.New("unauthorized")

// ErrForbidden signals a known token whose scopes do not permit the
// operation. Kept distinct from ErrUnauthorized so handlers can pick
// the right status code.
var ErrForbidden = errors.New("forbidden")

// Token is one entry in the tokens.json file. Scopes is a list of
// scope strings:
//
//	publish:*          publish any package
//	publish:<name>     publish this package only
//	admin:yank         mark versions yanked / unyanked
//
// Owner is informational — surfaced in logs so operators can trace
// a publish back to a person or CI job.
type Token struct {
	Token  string   `json:"token"`
	Owner  string   `json:"owner,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
}

// TokenDB is the in-memory token table. It is loaded from
// tokens.json at server startup and can be swapped out at runtime
// via Replace; handlers call Authorize under a read lock.
type TokenDB struct {
	mu     sync.RWMutex
	tokens []Token
}

// LoadTokenDB reads tokens.json from path. A missing file yields an
// empty DB (useful in dev — the operator creates tokens.json out of
// band and the server picks it up on restart).
func LoadTokenDB(path string) (*TokenDB, error) {
	db := &TokenDB{}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return db, nil
		}
		return nil, err
	}
	var wire struct {
		Tokens []Token `json:"tokens"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	db.tokens = wire.Tokens
	return db, nil
}

// NewTokenDB builds a DB from an in-memory slice. Useful for tests
// and programmatic embedding.
func NewTokenDB(tokens []Token) *TokenDB {
	return &TokenDB{tokens: append([]Token(nil), tokens...)}
}

// Replace atomically swaps the token list. cmd/osty-registry calls
// this on SIGHUP to pick up edits to tokens.json.
func (db *TokenDB) Replace(tokens []Token) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.tokens = append([]Token(nil), tokens...)
}

// Snapshot returns a copy of the current token list. Used by the
// SIGHUP reload path to move tokens between two *TokenDB values
// without exposing the internal slice.
func (db *TokenDB) Snapshot() []Token {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return append([]Token(nil), db.tokens...)
}

// Authorize checks whether `bearer` is known and has a scope that
// satisfies `need`. Need is a scope string — "publish:<name>" or a
// broader scope like "publish:*". ConstantTimeCompare is used on
// token comparison so a rogue client can't time-attack the table.
func (db *TokenDB) Authorize(bearer, need string) error {
	if bearer == "" {
		return ErrUnauthorized
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	for _, t := range db.tokens {
		if subtle.ConstantTimeCompare([]byte(t.Token), []byte(bearer)) != 1 {
			continue
		}
		// Token matched. Now check scopes.
		for _, scope := range t.Scopes {
			if scopeMatches(scope, need) {
				return nil
			}
		}
		return ErrForbidden
	}
	return ErrUnauthorized
}

// scopeMatches returns true if `have` grants `need`. The matcher
// supports a single trailing wildcard:
//
//	have="publish:*"   need="publish:foo"  → true
//	have="publish:foo" need="publish:foo"  → true
//	have="publish:foo" need="publish:bar"  → false
func scopeMatches(have, need string) bool {
	if have == need {
		return true
	}
	if strings.HasSuffix(have, ":*") {
		prefix := strings.TrimSuffix(have, "*")
		return strings.HasPrefix(need, prefix)
	}
	return false
}

// parseBearer extracts the token from an `Authorization: Bearer xyz`
// header value. Returns "" when the header is missing or malformed,
// in which case callers should respond 401.
func parseBearer(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
