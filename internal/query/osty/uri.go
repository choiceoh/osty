// Package osty wires the Osty compiler pipeline into the generic
// query engine in [github.com/osty/osty/internal/query]. Callers
// (LSP, CLI) construct an [Engine], seed inputs (SourceText,
// PackageFiles, WorkspaceMembers), and pull results via typed query
// handles (Parse, ResolvePackage, CheckFile, FileDiagnostics, etc.).
//
// # Input seams
//
// The engine treats source bytes, the set of files in a package, and
// the set of packages in a workspace as the three primitive inputs.
// Everything downstream is a derived query whose identity is computed
// from those inputs and the Prelude/Stdlib singletons baked into the
// Database at construction.
//
// # Early cutoff
//
// Every derived query registers a content-based hash function. When a
// re-run produces a value whose hash matches the previous cache, the
// slot's computedAt is preserved and downstream dependents skip their
// own re-runs. See hash.go for the serialisation rules.
package osty

import (
	"path/filepath"
	"strings"
)

// NormalizePath returns the canonical form used as the key for
// [SourceText] and related path-keyed queries. Paths are made absolute
// (when possible), slashes forward-normalised, and cleaned of "." /
// ".." segments. Callers must pass NormalizePath output for every
// Input.Set / Query.Get, so the Database sees one key per file.
func NormalizePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// FromURI converts an LSP `file://` URI into the normalized path the
// engine uses as its key. For non-file URIs (untitled/scratch buffers,
// in-memory schemes) the URI is returned verbatim and ok reports
// false; callers can still use the returned key but should be aware
// that the key is not a filesystem path.
func FromURI(uri string) (key string, ok bool) {
	const prefix = "file://"
	if !strings.HasPrefix(uri, prefix) {
		return uri, false
	}
	p := strings.TrimPrefix(uri, prefix)
	// On Windows the URI looks like file:///C:/path. Strip the leading
	// slash so filepath.Abs does the right thing.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	// Percent-decode the few characters LSP clients encode. We only
	// handle the common cases (space, colon); callers sending exotic
	// URIs should normalize on their side.
	p = strings.ReplaceAll(p, "%20", " ")
	p = strings.ReplaceAll(p, "%3A", ":")
	p = strings.ReplaceAll(p, "%3a", ":")
	return NormalizePath(p), true
}

// PackageDirOf returns the normalized directory containing path. Used
// as the key for package-level queries.
func PackageDirOf(path string) string {
	return NormalizePath(filepath.Dir(path))
}
