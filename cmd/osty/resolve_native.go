package main

import (
	"fmt"
	"sort"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

func nativeResolvePackageRows(pkg *resolve.Package, path string) ([]resolve.NativeResolutionRow, error) {
	return resolve.NativeResolutionRows(pkg, path)
}

func nativeResolvePackageDiagnostics(pkg *resolve.Package) ([]*diag.Diagnostic, error) {
	return resolve.NativeDiagnostics(pkg)
}

func printNativeResolutionRows(rows []resolve.NativeResolutionRow) {
	for _, row := range rows {
		fmt.Printf("%d:%d\t%-20s\t%-12s\t->%s\n", row.Line, row.Column, row.Name, row.Kind, row.Def)
	}
}

// nativeResolveFromRunRows is the astbridge-free single-file resolve
// row builder. Given a parsed FrontendRun and the raw source, it runs
// the native resolver directly on the parser arena (no *ast.File
// round-trip) and formats the result into the same
// resolve.NativeResolutionRow shape printNativeResolutionRows expects.
func nativeResolveFromRunRows(run *selfhost.FrontendRun, src []byte, path string) []resolve.NativeResolutionRow {
	if run == nil {
		return nil
	}
	resolved := selfhost.ResolveStructuredFromRunForPath(run, path)
	lineStarts := computeLineStartsBytes(src)
	kindByNode := map[int]string{}
	for _, sym := range resolved.Symbols {
		kindByNode[sym.Node] = resolveSymbolKindLabel(sym.Kind)
	}
	rows := make([]resolve.NativeResolutionRow, 0, len(resolved.Refs))
	for _, ref := range resolved.Refs {
		if ref.File != path {
			continue
		}
		line, col, ok := offsetToLineCol(lineStarts, src, ref.Start)
		if !ok {
			continue
		}
		def := "<builtin>"
		if ref.TargetFile != "" {
			if dl, dc, dok := offsetToLineCol(lineStarts, src, ref.TargetStart); dok {
				def = fmt.Sprintf("%d:%d", dl, dc)
			}
		}
		kind := kindByNode[ref.TargetNode]
		if kind == "" {
			kind = "builtin"
		}
		rows = append(rows, resolve.NativeResolutionRow{
			Line:   line,
			Column: col,
			Name:   ref.Name,
			Kind:   kind,
			Def:    def,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Line != rows[j].Line {
			return rows[i].Line < rows[j].Line
		}
		if rows[i].Column != rows[j].Column {
			return rows[i].Column < rows[j].Column
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// nativeResolveFromRunDiagnostics converts the native resolver's
// structured diagnostics into *diag.Diagnostic, preserving the code +
// primary span + hint shape callers expect. Pair with ParseRun to keep
// the resolve pass free of the astbridge *ast.File lowering.
func nativeResolveFromRunDiagnostics(run *selfhost.FrontendRun, src []byte, path string) []*diag.Diagnostic {
	if run == nil {
		return nil
	}
	resolved := selfhost.ResolveStructuredFromRunForPath(run, path)
	lineStarts := computeLineStartsBytes(src)
	out := make([]*diag.Diagnostic, 0, len(resolved.Diagnostics))
	for _, record := range resolved.Diagnostics {
		builder := diag.New(diag.Error, record.Message).Code(record.Code).File(record.File)
		if span, ok := offsetsToSpan(lineStarts, src, record.Start, record.End); ok {
			builder.Primary(span, "")
		}
		if record.Hint != "" {
			builder.Hint(record.Hint)
		}
		out = append(out, builder.Build())
	}
	return out
}

func computeLineStartsBytes(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func offsetToLineCol(lineStarts []int, src []byte, offset int) (int, int, bool) {
	if offset < 0 || offset > len(src) {
		return 0, 0, false
	}
	idx := sort.SearchInts(lineStarts, offset+1) - 1
	if idx < 0 {
		idx = 0
	}
	line := idx + 1
	col := offset - lineStarts[idx] + 1
	return line, col, true
}

func offsetsToSpan(lineStarts []int, src []byte, start, end int) (diag.Span, bool) {
	if end < start {
		end = start
	}
	sl, sc, ok := offsetToLineCol(lineStarts, src, start)
	if !ok {
		return diag.Span{}, false
	}
	el, ec, ok := offsetToLineCol(lineStarts, src, end)
	if !ok {
		el, ec = sl, sc
	}
	return diag.Span{
		Start: token.Pos{Offset: start, Line: sl, Column: sc},
		End:   token.Pos{Offset: end, Line: el, Column: ec},
	}, true
}

// resolveSymbolKindLabel mirrors the package-internal
// resolve.nativeResolveKindLabel so cmd/osty can render the same row
// Kind strings as the existing Package-path without needing a new
// exported helper. Keep in sync with
// internal/resolve/native_adapter.go:nativeResolveKindLabel.
func resolveSymbolKindLabel(kind string) string {
	switch kind {
	case "fn":
		return "function"
	case "value":
		return "binding"
	case "type":
		return "type"
	case "variant":
		return "variant"
	case "package":
		return "package"
	case "generic":
		return "type parameter"
	case "":
		return "builtin"
	}
	return kind
}
