package pkgmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// HashPrefix is the algorithm tag used in lockfile `checksum` fields.
// Kept constant so future migrations (sha512, blake3, …) can be
// additive — we match against the prefix in Verify.
const HashPrefix = "sha256:"

// HashBytes returns the sha256-prefixed checksum of b. Used for
// tarball / archive verification where the entire artifact is in
// memory.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return HashPrefix + hex.EncodeToString(sum[:])
}

// HashReader consumes r and returns the sha256-prefixed checksum.
// Convenience wrapper for streaming inputs (HTTP responses, open
// files).
func HashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return HashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

// HashDir computes a stable hash over the contents of dir. Files are
// visited in sorted path order; each file contributes its relative
// path (NUL-terminated) followed by its byte content. Directories
// and symlinks contribute only their path entry.
//
// Used for path-source dependencies where the "version" is implicit
// in the directory state. Two vendored copies with identical
// contents get identical hashes regardless of filesystem timestamps.
//
// Files whose name begins with `.osty` (e.g. our own cache) are
// excluded so we don't self-hash.
func HashDir(dir string) (string, error) {
	type entry struct {
		rel string
		abs string
	}
	var files []entry
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		// Skip our own vendor / cache directories and hidden system
		// files that aren't semantically part of the package.
		if skipDirEntry(rel, info) {
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info == nil || info.IsDir() {
			return nil
		}
		files = append(files, entry{rel: rel, abs: path})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	h := sha256.New()
	for _, e := range files {
		// Normalize path separators so hashes are cross-platform.
		fmt.Fprintf(h, "%s\x00", filepath.ToSlash(e.rel))
		f, err := os.Open(e.abs)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			return "", err
		}
		_ = f.Close()
		fmt.Fprintf(h, "\x00")
	}
	return HashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyChecksum returns nil when want == got; otherwise a
// descriptive error naming the mismatch so the CLI can surface it.
// Ignores prefix differences as long as the algorithm family
// matches HashPrefix.
func VerifyChecksum(want, got string) error {
	return GolegacyVerifyChecksum(want, got)
}

// skipDirEntry returns true for filesystem entries the packager
// should ignore: our own cache, version-control metadata, OS cruft.
func skipDirEntry(rel string, info os.FileInfo) bool {
	base := filepath.Base(rel)
	if rel == "." {
		return false
	}
	switch base {
	case ".osty", ".git", ".hg", ".svn", ".DS_Store":
		return true
	}
	return false
}
