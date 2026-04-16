package canonical

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/format"
	"github.com/osty/osty/internal/sourcemap"
)

// Source returns the canonical checker-facing source for a parsed file.
// Callers fall back to the original bytes when no parsed AST is available.
func Source(original []byte, file *ast.File) []byte {
	out, _ := SourceWithMap(original, file)
	return out
}

// SourceWithMap returns canonical checker-facing source plus a coarse source
// map from canonical output spans back to the original spans stored on the AST.
func SourceWithMap(original []byte, file *ast.File) ([]byte, *sourcemap.Map) {
	if file == nil {
		return append([]byte(nil), original...), nil
	}
	if out, m := format.FileWithMap(file); len(out) > 0 {
		return out, m
	}
	return append([]byte(nil), original...), nil
}
