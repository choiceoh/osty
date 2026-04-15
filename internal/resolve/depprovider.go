package resolve

import (
	"strings"

	"github.com/osty/osty/internal/ast"
)

// DepProvider resolves external `use` targets — anything that is
// neither a sibling package in the workspace tree nor a stdlib
// module — into an on-disk directory the loader can read.
//
// Two shapes of use-path are routed here:
//
//  1. URL-style: `use github.com/user/lib`. The RawPath (preserved
//     by the parser) is passed unchanged to LookupDep.
//
//  2. Bare-alias: `use fastjson`. When no sibling package named
//     `fastjson` exists under the workspace root, the alias is
//     passed to LookupDep so vendored deps fetched via
//     [dependencies] in osty.toml become resolvable.
//
// Implementations live in internal/pkgmgr: the package manager knows
// what's been vendored, and injects a concrete DepProvider into the
// workspace before LoadPackage starts.
//
// Returning ("", false) means "I don't recognize this key" — the
// workspace then falls through to its default missing-package
// diagnostic.
type DepProvider interface {
	LookupDep(rawPath string) (dir string, ok bool)
}

// useKey returns the stable key this workspace uses to index a
// `use` target. URL-style paths keep their raw slash form so
// `github.com/user/lib` and `lib` never collide; dotted paths are
// rejoined with dots.
//
// Exported so pkgmgr can keep its graph keys in sync with the
// workspace.
func UseKey(u *ast.UseDecl) string {
	if u == nil {
		return ""
	}
	if u.RawPath != "" && strings.ContainsAny(u.RawPath, "/") {
		return u.RawPath
	}
	if len(u.Path) > 0 {
		return strings.Join(u.Path, ".")
	}
	return u.RawPath
}

// isURLStyle reports whether a use target is a URL-like path
// (has a `/`). Used to decide between dirFor (workspace layout)
// and DepProvider (external deps).
func isURLStyle(key string) bool {
	return strings.ContainsAny(key, "/")
}
