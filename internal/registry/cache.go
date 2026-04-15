package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirIndexCache stores index responses on disk as one JSON file per
// crate. The file holds both the parsed entry and the registry's
// ETag so the next Versions() call can issue a conditional request.
//
// Layout, rooted at Root:
//
//	<Root>/<sanitized-name>.json
//
// Cache entries are tiny (a few KB at most); we don't bother with
// sharding by prefix until we see real-world numbers warranting it.
type DirIndexCache struct {
	Root string
}

// NewDirIndexCache returns a cache rooted at dir, creating the
// directory on demand the first time Store runs. A cache against a
// directory the user can't write to silently degrades to network-
// only behavior — the registry client treats Store failures as
// non-fatal.
func NewDirIndexCache(dir string) *DirIndexCache {
	return &DirIndexCache{Root: dir}
}

// cacheFile is the on-disk envelope. ETag is preserved verbatim so
// the conditional GET sends back exactly what the registry issued.
type cacheFile struct {
	ETag  string      `json:"etag"`
	Entry *IndexEntry `json:"entry"`
}

// Load returns the cached entry + ETag for `name`. A missing file is
// not an error — the caller treats (nil, "", nil) as "no cache hit"
// and proceeds with the unconditional request.
func (c *DirIndexCache) Load(name string) (*IndexEntry, string, error) {
	if c == nil || c.Root == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(c.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		// Corrupt cache file — treat as a miss rather than failing.
		// The next successful Store will overwrite it.
		return nil, "", nil
	}
	return cf.Entry, cf.ETag, nil
}

// Store persists entry + etag for `name`. Writes go through a temp
// file + rename so a half-written cache entry never appears.
func (c *DirIndexCache) Store(name string, entry *IndexEntry, etag string) error {
	if c == nil || c.Root == "" {
		return nil
	}
	if err := os.MkdirAll(c.Root, 0o755); err != nil {
		return err
	}
	cf := cacheFile{ETag: etag, Entry: entry}
	data, err := json.Marshal(cf)
	if err != nil {
		return err
	}
	target := c.path(name)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// path computes the on-disk filename for an index entry. Names are
// sanitized so registry-supplied identifiers can't traverse out of
// the cache directory.
func (c *DirIndexCache) path(name string) string {
	return filepath.Join(c.Root, sanitizeIndexName(name)+".json")
}

// sanitizeIndexName produces a filesystem-safe stem from a crate
// name. Crate names per the manifest validator are restricted to
// `[A-Za-z0-9_-]`, but defensive sanitization keeps a malicious or
// future-permissive registry from writing outside Root.
func sanitizeIndexName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "_%x", r)
		}
	}
	if b.Len() == 0 {
		return "_empty"
	}
	return b.String()
}
