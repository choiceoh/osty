package canonical

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/format"
)

// Source returns the canonical checker-facing source for a parsed file.
// Callers fall back to the original bytes when no parsed AST is available.
func Source(original []byte, file *ast.File) []byte {
	if file == nil {
		return append([]byte(nil), original...)
	}
	if out := format.File(file); len(out) > 0 {
		return out
	}
	return append([]byte(nil), original...)
}
