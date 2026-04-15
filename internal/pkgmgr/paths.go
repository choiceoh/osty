package pkgmgr

import (
	"os"
	"path/filepath"
)

// userHomeDir is a thin wrapper around os.UserHomeDir that returns
// an absolute path. Isolated so tests can stub it.
var userHomeDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home, nil
}

// joinPath cleans the joined path — factored so the cross-platform
// separators stay in one place. Cosmetic but keeps call sites
// shorter.
func joinPath(parts ...string) string {
	return filepath.Join(parts...)
}

// ensureDir creates dir (and parents) with 0o755 permissions. No-op
// if already present; returns the error as-is on failure so callers
// can wrap it.
func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
