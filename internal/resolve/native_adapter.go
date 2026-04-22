package resolve

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/token"
)

type nativeResolveCache struct {
	once   sync.Once
	result selfhost.ResolveResult
	files  []nativeResolveFileInfo
	err    error
}

type nativeResolveFileInfo struct {
	path       string
	base       int
	source     []byte
	sourceMap  *sourcemap.Map
	lineStarts []int
}

// NativeResolutionRow is one human-readable row derived from the selfhost
// resolver's structured ref data.
type NativeResolutionRow struct {
	Line   int
	Column int
	Name   string
	Kind   string
	Def    string
}

// NativeStructuredResult returns the cached selfhost structured resolve result
// for pkg. The returned slices should be treated as read-only.
func NativeStructuredResult(pkg *Package) (selfhost.ResolveResult, error) {
	result, _, err := nativeResolveArtifacts(pkg)
	return result, err
}

// NativeResolutionRows returns the cached selfhost resolve rows for one file in
// pkg, sorted by source position.
func NativeResolutionRows(pkg *Package, path string) ([]NativeResolutionRow, error) {
	result, files, err := nativeResolveArtifacts(pkg)
	if err != nil {
		return nil, err
	}
	return nativeResolutionRowsFromArtifacts(path, result, files), nil
}

// NativeDiagnostics returns the cached selfhost resolve diagnostics for pkg.
// The returned slice does not include parser diagnostics already stored on the
// package files.
func NativeDiagnostics(pkg *Package) ([]*diag.Diagnostic, error) {
	result, files, err := nativeResolveArtifacts(pkg)
	if err != nil {
		return nil, err
	}
	return nativeResolveDiagnosticsFromArtifacts(result, files), nil
}

func nativeResolveArtifacts(pkg *Package) (selfhost.ResolveResult, []nativeResolveFileInfo, error) {
	if pkg == nil {
		return selfhost.ResolveResult{}, nil, fmt.Errorf("native resolve: nil package")
	}
	pkg.nativeResolve.once.Do(func() {
		input, files, err := nativeResolveInput(pkg)
		if err != nil {
			pkg.nativeResolve.err = err
			return
		}
		resolved, err := selfhost.ResolvePackageStructured(input)
		if err != nil {
			pkg.nativeResolve.err = err
			return
		}
		pkg.nativeResolve.result = resolved
		pkg.nativeResolve.files = files
	})
	return pkg.nativeResolve.result, pkg.nativeResolve.files, pkg.nativeResolve.err
}

// nativeCfgEnvFor picks the `#[cfg(...)]` evaluation env the native resolver
// should use for pkg. Matches the legacy ResolveAll behaviour: when the
// package is attached to a workspace it inherits the workspace env (or
// DefaultCfgEnv when the workspace hasn't set one), so cross-compilation
// flags populate both paths identically. Standalone packages (no workspace)
// stay unfiltered.
func nativeCfgEnvFor(pkg *Package) *selfhost.CfgEnv {
	if pkg == nil || pkg.workspace == nil {
		return nil
	}
	env := pkg.workspace.cfgEnv
	if env == nil {
		env = DefaultCfgEnv()
	}
	return env.toSelfhost()
}

func nativeResolveInput(pkg *Package) (selfhost.PackageResolveInput, []nativeResolveFileInfo, error) {
	input := selfhost.PackageResolveInput{
		Files: make([]selfhost.PackageResolveFile, 0, len(pkg.Files)),
		Cfg:   nativeCfgEnvFor(pkg),
	}
	files := make([]nativeResolveFileInfo, 0, len(pkg.Files))
	base := 0
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		src, err := nativeResolveSourceForFile(pf)
		if err != nil {
			return selfhost.PackageResolveInput{}, nil, err
		}
		if len(src) == 0 {
			continue
		}
		// File is required by the Direct lowering path in
		// selfhostBuildPackageAstDirect. When pf was loaded via
		// LoadPackageForNative it is nil; passing nil here makes
		// ResolvePackageStructured take the non-Direct path, which
		// re-parses Source via the self-host lexer + parser (no
		// astbridge).
		input.Files = append(input.Files, selfhost.PackageResolveFile{
			Source:    append([]byte(nil), src...),
			File:      pf.File,
			SourceMap: pf.CanonicalMap,
			Base:      base,
			Name:      filepath.Base(pf.Path),
			Path:      pf.Path,
		})
		files = append(files, nativeResolveFileInfo{
			path:       pf.Path,
			base:       base,
			source:     append([]byte(nil), src...),
			sourceMap:  pf.CanonicalMap,
			lineStarts: nativeResolveLineStarts(src),
		})
		base += len(src) + 1
	}
	return input, files, nil
}

func nativeResolveSourceForFile(pf *PackageFile) ([]byte, error) {
	if pf == nil {
		return nil, nil
	}
	if len(pf.CanonicalSource) > 0 {
		return pf.CanonicalSource, nil
	}
	if len(pf.Source) == 0 {
		return nil, nil
	}
	if pf.File == nil {
		// LoadPackageForNative-loaded files carry raw Source only
		// (no canonicalization). parser.osty accepts the same
		// grammar as the canonical form, so the native resolver's
		// re-parse in selfhostBuildPackageAst handles the input
		// identically. Return a defensive copy so downstream
		// mutation of input.Files does not corrupt pf.Source.
		return append([]byte(nil), pf.Source...), nil
	}
	src, _ := canonical.SourceWithMap(pf.Source, pf.File)
	return src, nil
}

func nativeResolveLineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func nativeResolutionRowsFromArtifacts(path string, resolved selfhost.ResolveResult, files []nativeResolveFileInfo) []NativeResolutionRow {
	kindByNode := map[int]string{}
	for _, sym := range resolved.Symbols {
		kindByNode[sym.Node] = nativeResolveKindLabel(sym.Kind)
	}
	rows := make([]NativeResolutionRow, 0, len(resolved.Refs))
	for _, ref := range resolved.Refs {
		if ref.File != path {
			continue
		}
		line, col, ok := nativeResolveLineCol(files, path, ref.Start, ref.End)
		if !ok {
			continue
		}
		def := "<builtin>"
		if ref.TargetFile != "" {
			if dl, dc, ok := nativeResolveLineCol(files, ref.TargetFile, ref.TargetStart, ref.TargetEnd); ok {
				def = fmt.Sprintf("%d:%d", dl, dc)
			}
		}
		kind := kindByNode[ref.TargetNode]
		if kind == "" {
			kind = "builtin"
		}
		rows = append(rows, NativeResolutionRow{
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

func nativeResolveDiagnosticsFromArtifacts(resolved selfhost.ResolveResult, files []nativeResolveFileInfo) []*diag.Diagnostic {
	out := make([]*diag.Diagnostic, 0, len(resolved.Diagnostics))
	for _, record := range resolved.Diagnostics {
		builder := diag.New(diag.Error, record.Message).Code(record.Code).File(record.File)
		if span, ok := nativeResolveSpan(files, record.File, record.Start, record.End); ok {
			builder.Primary(span, "")
		}
		if record.Hint != "" {
			builder.Hint(record.Hint)
		}
		out = append(out, builder.Build())
	}
	return out
}

func nativeResolveSpan(files []nativeResolveFileInfo, path string, start, end int) (diag.Span, bool) {
	file, relStart, ok := nativeResolveFileOffset(files, path, start)
	if !ok {
		return diag.Span{}, false
	}
	_, relEnd, ok := nativeResolveFileOffset(files, path, end)
	if !ok {
		relEnd = relStart
	}
	if relEnd < relStart {
		relEnd = relStart
	}
	if file.sourceMap != nil {
		if remapped, ok := file.sourceMap.RemapSpan(diag.Span{
			Start: token.Pos{Offset: relStart},
			End:   token.Pos{Offset: relEnd},
		}); ok {
			return remapped, true
		}
	}
	startPos, ok := nativeResolvePositionInFile(file, relStart)
	if !ok {
		return diag.Span{}, false
	}
	endPos, ok := nativeResolvePositionInFile(file, relEnd)
	if !ok {
		endPos = startPos
	}
	if endPos.Offset < startPos.Offset {
		endPos = startPos
	}
	return diag.Span{Start: startPos, End: endPos}, true
}

func nativeResolveLineCol(files []nativeResolveFileInfo, path string, start, end int) (int, int, bool) {
	span, ok := nativeResolveSpan(files, path, start, end)
	if !ok {
		return 0, 0, false
	}
	return span.Start.Line, span.Start.Column, true
}

func nativeResolvePosition(files []nativeResolveFileInfo, path string, offset int) (token.Pos, bool) {
	file, rel, ok := nativeResolveFileOffset(files, path, offset)
	if !ok {
		return token.Pos{}, false
	}
	return nativeResolvePositionInFile(file, rel)
}

func nativeResolveFileOffset(files []nativeResolveFileInfo, path string, offset int) (nativeResolveFileInfo, int, bool) {
	for _, file := range files {
		if path != "" && file.path != path {
			continue
		}
		rel := offset - file.base
		if rel < 0 || rel > len(file.source) {
			continue
		}
		return file, rel, true
	}
	return nativeResolveFileInfo{}, 0, false
}

func nativeResolvePositionInFile(file nativeResolveFileInfo, rel int) (token.Pos, bool) {
	if rel < 0 || rel > len(file.source) {
		return token.Pos{}, false
	}
	lineIdx := sort.Search(len(file.lineStarts), func(i int) bool {
		return file.lineStarts[i] > rel
	}) - 1
	if lineIdx < 0 {
		lineIdx = 0
	}
	lineStart := file.lineStarts[lineIdx]
	col := 1 + utf8.RuneCount(file.source[lineStart:rel])
	return token.Pos{
		Offset: rel,
		Line:   lineIdx + 1,
		Column: col,
	}, true
}

func nativeResolveKindLabel(kind string) string {
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
	default:
		return kind
	}
}
