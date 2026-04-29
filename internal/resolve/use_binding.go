package resolve

import (
	"strings"

	"github.com/osty/osty/internal/ast"
)

// scopedUseBaseKey returns the base path of a scoped `use a.b.{c, d}`
// declaration. import_surface and workspace_native use this to look up
// the parent package whose exports the scoped clause filters.
func scopedUseBaseKey(u *ast.UseDecl) string {
	if u == nil {
		return ""
	}
	if len(u.ScopedBase) > 0 {
		return strings.Join(u.ScopedBase, ".")
	}
	base, _, ok := splitUseMemberPath(UseKey(u))
	if !ok {
		return ""
	}
	return base
}

func splitUseMemberPath(path string) (base string, member string, ok bool) {
	if path == "" || strings.ContainsAny(path, "/") {
		return "", "", false
	}
	i := strings.LastIndex(path, ".")
	if i <= 0 || i == len(path)-1 {
		return "", "", false
	}
	return path[:i], path[i+1:], true
}
