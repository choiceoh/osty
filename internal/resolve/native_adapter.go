package resolve

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/token"
)

type nativeResolveCache struct {
	once       sync.Once
	checkOnce  sync.Once
	result     api.ResolveResult
	check      api.CheckResult
	checkInput api.PackageCheckInput
	files      []nativeResolveFileInfo
	err        error
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

// NativeResolveFactSet is the stable, value-typed view of the selfhost
// resolver result for package/file-aware tooling. Offsets are source-relative
// within each File, not merged-package offsets.
type NativeResolveFactSet struct {
	Symbols []NativeResolvedSymbol
	Refs    []NativeResolvedRef
	Imports []NativeImportSurface
}

type NativeResolvedSymbol struct {
	ID      string
	Name    string
	Kind    string
	Type    string
	File    string
	Start   int
	End     int
	Depth   int
	Public  bool
	Builtin bool
}

type NativeResolvedRef struct {
	ID             string
	Name           string
	File           string
	Start          int
	End            int
	TargetSymbolID string
	TargetName     string
	TargetKind     string
	TargetType     string
	TargetFile     string
	TargetStart    int
	TargetEnd      int
	Type           bool
	Builtin        bool
}

type NativeImportSurface struct {
	Alias   string
	Symbols []NativeImportSymbol
}

type NativeImportSymbol struct {
	Name string
	Kind string
	Type string
}

// NativeStructuredResult returns the cached selfhost structured resolve result
// for pkg. The returned slices should be treated as read-only.
func NativeStructuredResult(pkg *Package) (api.ResolveResult, error) {
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

// NativeIdentKindIndex returns source-relative identifier offsets for one file,
// mapped to LSP/policy-facing symbol kind strings. It is intentionally built
// from the selfhost ResolveResult rather than from PackageFile.RefsByID or
// Scope, giving downstream tools a structured-result path that does not need
// the Go resolver compatibility projection.
func NativeIdentKindIndex(pkg *Package, path string) (map[int]string, error) {
	result, files, err := nativeResolveArtifacts(pkg)
	if err != nil {
		return nil, err
	}
	return nativeIdentKindIndexFromArtifacts(path, result, files), nil
}

// NativeResolveFacts returns source-relative symbol/reference facts derived
// directly from the cached selfhost structured resolve result.
func NativeResolveFacts(pkg *Package) (NativeResolveFactSet, error) {
	result, files, checked, err := nativeResolveFactArtifacts(pkg)
	if err != nil {
		return NativeResolveFactSet{}, err
	}
	facts := nativeResolveFactsFromArtifacts(result, files, checked)
	facts.Imports = nativeImportSurfaces(pkg)
	return facts, nil
}

func nativeResolveArtifacts(pkg *Package) (api.ResolveResult, []nativeResolveFileInfo, error) {
	if pkg == nil {
		return api.ResolveResult{}, nil, fmt.Errorf("native resolve: nil package")
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
		pkg.nativeResolve.checkInput = api.PackageCheckInput{
			Files:   input.Files,
			Imports: input.Imports,
		}
		pkg.nativeResolve.result = resolved
		pkg.nativeResolve.files = files
	})
	return pkg.nativeResolve.result, pkg.nativeResolve.files, pkg.nativeResolve.err
}

func nativeResolveFactArtifacts(pkg *Package) (api.ResolveResult, []nativeResolveFileInfo, api.CheckResult, error) {
	result, files, err := nativeResolveArtifacts(pkg)
	if err != nil || pkg == nil {
		return result, files, api.CheckResult{}, err
	}
	pkg.nativeResolve.checkOnce.Do(func() {
		checked, err := selfhost.CheckPackageStructured(pkg.nativeResolve.checkInput)
		if err == nil {
			pkg.nativeResolve.check = checked
		}
	})
	return pkg.nativeResolve.result, pkg.nativeResolve.files, pkg.nativeResolve.check, pkg.nativeResolve.err
}

// nativeCfgEnvFor picks the `#[cfg(...)]` evaluation env the native resolver
// should use for pkg. Matches the legacy ResolveAll behaviour: when the
// package is attached to a workspace it inherits the workspace env (or
// DefaultCfgEnv when the workspace hasn't set one), so cross-compilation
// flags populate both paths identically. Standalone packages (no workspace)
// stay unfiltered.
func nativeCfgEnvFor(pkg *Package) *api.CfgEnv {
	if pkg == nil || pkg.workspace == nil {
		return nil
	}
	env := pkg.workspace.cfgEnv
	if env == nil {
		env = DefaultCfgEnv()
	}
	return env.toSelfhost()
}

func nativeResolveInput(pkg *Package) (api.PackageResolveInput, []nativeResolveFileInfo, error) {
	input := api.PackageResolveInput{
		Files:       make([]api.PackageResolveFile, 0, len(pkg.Files)),
		Imports:     PackageImportSurfaces(pkg, pkg.workspace, nil),
		PackagePath: nativeResolvePackagePath(pkg),
		Cfg:         nativeCfgEnvFor(pkg),
	}
	files := make([]nativeResolveFileInfo, 0, len(pkg.Files))
	base := 0
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		src, err := nativeResolveSourceForFile(pf)
		if err != nil {
			return api.PackageResolveInput{}, nil, err
		}
		if len(src) == 0 {
			continue
		}
		// ResolvePackageStructured re-parses Source via the self-host
		// lexer + parser — no *ast.File round-trip, so we only pass
		// source bytes + routing metadata here.
		input.Files = append(input.Files, api.PackageResolveFile{
			Source: append([]byte(nil), src...),
			Base:   base,
			Name:   filepath.Base(pf.Path),
			Path:   pf.Path,
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

func nativeResolvePackagePath(pkg *Package) string {
	if pkg == nil {
		return ""
	}
	if pkg.workspace != nil {
		for path, candidate := range pkg.workspace.Packages {
			if candidate == pkg {
				return path
			}
		}
	}
	if pkg.Dir != "" {
		return pkg.Dir
	}
	return pkg.Name
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

func nativeResolutionRowsFromArtifacts(path string, resolved api.ResolveResult, files []nativeResolveFileInfo) []NativeResolutionRow {
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

func nativeResolveFactsFromArtifacts(resolved api.ResolveResult, files []nativeResolveFileInfo, checked api.CheckResult) NativeResolveFactSet {
	facts := NativeResolveFactSet{
		Symbols: make([]NativeResolvedSymbol, 0, len(resolved.Symbols)),
		Refs:    make([]NativeResolvedRef, 0, len(resolved.Refs)+len(resolved.TypeRefs)),
	}
	typeByNode := nativeCheckSymbolTypes(checked)
	symbolsByID := make(map[string]NativeResolvedSymbol, len(resolved.Symbols))
	symbolsByTarget := make(map[string]NativeResolvedSymbol, len(resolved.Symbols))
	for _, sym := range resolved.Symbols {
		start, end, ok := nativeResolveOriginalSpan(files, sym.File, sym.Start, sym.End)
		if !ok {
			start, end = sym.Start, sym.End
		}
		typeText := ""
		if sym.Type != nil {
			typeText = sym.Type.String()
		}
		if typeText == "" {
			typeText = typeByNode[sym.Node]
		}
		fact := NativeResolvedSymbol{
			ID:      sym.ID,
			Name:    sym.Name,
			Kind:    nativeResolveSemanticKindLabel(sym.Kind),
			Type:    typeText,
			File:    sym.File,
			Start:   start,
			End:     end,
			Depth:   sym.Depth,
			Public:  sym.Public,
			Builtin: sym.Node < 0,
		}
		facts.Symbols = append(facts.Symbols, fact)
		if fact.ID != "" {
			symbolsByID[fact.ID] = fact
		}
		symbolsByTarget[nativeResolveTargetKey(sym.File, sym.Node, sym.Start, sym.End)] = fact
	}
	for _, ref := range resolved.Refs {
		fact, ok := nativeResolvedRefFact(files, symbolsByID, symbolsByTarget, ref.ID, ref.Name, ref.File, ref.Start, ref.End, ref.TargetSymbolID, ref.TargetFile, ref.TargetNode, ref.TargetStart, ref.TargetEnd, false)
		if ok {
			facts.Refs = append(facts.Refs, fact)
		}
	}
	for _, ref := range resolved.TypeRefs {
		fact, ok := nativeResolvedRefFact(files, symbolsByID, symbolsByTarget, ref.ID, ref.Name, ref.File, ref.Start, ref.End, ref.TargetSymbolID, ref.TargetFile, ref.TargetNode, ref.TargetStart, ref.TargetEnd, true)
		if ok {
			facts.Refs = append(facts.Refs, fact)
		}
	}
	sort.Slice(facts.Symbols, func(i, j int) bool {
		if facts.Symbols[i].File != facts.Symbols[j].File {
			return facts.Symbols[i].File < facts.Symbols[j].File
		}
		if facts.Symbols[i].Start != facts.Symbols[j].Start {
			return facts.Symbols[i].Start < facts.Symbols[j].Start
		}
		return facts.Symbols[i].Name < facts.Symbols[j].Name
	})
	sort.Slice(facts.Refs, func(i, j int) bool {
		if facts.Refs[i].File != facts.Refs[j].File {
			return facts.Refs[i].File < facts.Refs[j].File
		}
		if facts.Refs[i].Start != facts.Refs[j].Start {
			return facts.Refs[i].Start < facts.Refs[j].Start
		}
		return facts.Refs[i].Name < facts.Refs[j].Name
	})
	return facts
}

func nativeCheckSymbolTypes(checked api.CheckResult) map[int]string {
	if len(checked.Symbols) == 0 {
		return nil
	}
	out := make(map[int]string, len(checked.Symbols))
	for _, sym := range checked.Symbols {
		if sym.Type == nil {
			continue
		}
		node := sym.NodeID
		if node == 0 {
			node = sym.Node
		}
		if node < 0 {
			continue
		}
		out[node] = sym.Type.String()
	}
	return out
}

func nativeImportSurfaces(pkg *Package) []NativeImportSurface {
	imports := PackageImportSurfaces(pkg, pkg.workspace, nil)
	if len(imports) == 0 {
		return nil
	}
	out := make([]NativeImportSurface, 0, len(imports))
	for _, imp := range imports {
		if imp.Alias == "" {
			continue
		}
		surface := NativeImportSurface{Alias: imp.Alias}
		seen := map[string]bool{}
		add := func(name, kind, typeText string) {
			name = nativeImportLocalName(name)
			if name == "" || seen[name] {
				return
			}
			seen[name] = true
			surface.Symbols = append(surface.Symbols, NativeImportSymbol{
				Name: name,
				Kind: kind,
				Type: typeText,
			})
		}
		for _, fn := range imp.Functions {
			if fn.Owner != "" {
				continue
			}
			add(fn.Name, "function", nativeImportFnType(fn))
		}
		for _, typ := range imp.TypeDecls {
			add(typ.Name, nativeResolveSemanticKindLabel(typ.Kind), "")
		}
		for _, alias := range imp.Aliases {
			add(alias.Name, "type alias", nativeTypeReprText(alias.TargetRepr, alias.Target))
		}
		for _, variant := range imp.Variants {
			add(variant.Name, "variant", nativeImportVariantType(variant))
		}
		for _, field := range imp.Fields {
			if field.Owner != imp.Alias {
				continue
			}
			add(field.Name, "binding", nativeTypeReprText(field.Type, field.TypeName))
		}
		sort.Slice(surface.Symbols, func(i, j int) bool {
			return surface.Symbols[i].Name < surface.Symbols[j].Name
		})
		out = append(out, surface)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Alias < out[j].Alias
	})
	return out
}

func nativeImportLocalName(name string) string {
	if idx := strings.LastIndexByte(name, '.'); idx >= 0 && idx+1 < len(name) {
		return name[idx+1:]
	}
	return name
}

func nativeTypeReprText(repr *api.TypeRepr, fallback string) string {
	if repr != nil {
		return repr.String()
	}
	return fallback
}

func nativeImportFnType(fn api.PackageCheckFn) string {
	params := make([]string, 0, len(fn.ParamTypes)+len(fn.ParamTypeReprs))
	if len(fn.ParamTypeReprs) > 0 {
		for i := range fn.ParamTypeReprs {
			params = append(params, fn.ParamTypeReprs[i].String())
		}
	} else {
		params = append(params, fn.ParamTypes...)
	}
	ret := nativeTypeReprText(fn.ReturnTypeRepr, fn.ReturnType)
	if ret == "" {
		ret = "()"
	}
	return "fn(" + strings.Join(params, ", ") + ") -> " + ret
}

func nativeImportVariantType(variant api.PackageCheckVariant) string {
	fields := make([]string, 0, len(variant.FieldTypes)+len(variant.FieldTypeReprs))
	if len(variant.FieldTypeReprs) > 0 {
		for i := range variant.FieldTypeReprs {
			fields = append(fields, variant.FieldTypeReprs[i].String())
		}
	} else {
		fields = append(fields, variant.FieldTypes...)
	}
	if len(fields) == 0 {
		return ""
	}
	return "fn(" + strings.Join(fields, ", ") + ") -> " + variant.Owner
}

func nativeResolvedRefFact(
	files []nativeResolveFileInfo,
	symbolsByID map[string]NativeResolvedSymbol,
	symbolsByTarget map[string]NativeResolvedSymbol,
	id string,
	name string,
	file string,
	start int,
	end int,
	targetSymbolID string,
	targetFile string,
	targetNode int,
	targetStart int,
	targetEnd int,
	typeRef bool,
) (NativeResolvedRef, bool) {
	refStart, refEnd, ok := nativeResolveOriginalSpan(files, file, start, end)
	if !ok {
		return NativeResolvedRef{}, false
	}
	if typeRef && refEnd > refStart+len(name) {
		refEnd = refStart + len(name)
	}
	target := symbolsByID[targetSymbolID]
	if target.ID == "" {
		target = symbolsByTarget[nativeResolveTargetKey(targetFile, targetNode, targetStart, targetEnd)]
	}
	targetOrigStart, targetOrigEnd, targetOK := nativeResolveOriginalSpan(files, targetFile, targetStart, targetEnd)
	if targetOK && target.End == 0 && target.Start == 0 {
		target.Start = targetOrigStart
		target.End = targetOrigEnd
	}
	if target.Name == "" {
		target.Name = name
	}
	if target.Kind == "" {
		if targetNode < 0 {
			target.Kind = "builtin"
		} else if typeRef {
			target.Kind = "struct"
		} else {
			target.Kind = "binding"
		}
	}
	if target.File == "" {
		target.File = targetFile
	}
	return NativeResolvedRef{
		ID:             id,
		Name:           name,
		File:           file,
		Start:          refStart,
		End:            refEnd,
		TargetSymbolID: targetSymbolID,
		TargetName:     target.Name,
		TargetKind:     target.Kind,
		TargetType:     target.Type,
		TargetFile:     target.File,
		TargetStart:    target.Start,
		TargetEnd:      target.End,
		Type:           typeRef,
		Builtin:        targetNode < 0 || target.Builtin,
	}, true
}

func nativeIdentKindIndexFromArtifacts(path string, resolved api.ResolveResult, files []nativeResolveFileInfo) map[int]string {
	kindByTarget := make(map[string]string, len(resolved.Symbols))
	kindByID := make(map[string]string, len(resolved.Symbols))
	for _, sym := range resolved.Symbols {
		kind := nativeResolveSemanticKindLabel(sym.Kind)
		kindByTarget[nativeResolveTargetKey(sym.File, sym.Node, sym.Start, sym.End)] = kind
		if sym.ID != "" {
			kindByID[sym.ID] = kind
		}
	}
	out := make(map[int]string, len(resolved.Refs)+len(resolved.TypeRefs))
	for _, ref := range resolved.Refs {
		if ref.File != path {
			continue
		}
		off, ok := nativeResolveOriginalStart(files, path, ref.Start, ref.End)
		if !ok {
			continue
		}
		kind := kindByID[ref.TargetSymbolID]
		if kind == "" {
			kind = kindByTarget[nativeResolveTargetKey(ref.TargetFile, ref.TargetNode, ref.TargetStart, ref.TargetEnd)]
		}
		if kind == "" {
			if ref.TargetNode < 0 {
				kind = "builtin"
			} else {
				kind = "binding"
			}
		}
		out[off] = kind
	}
	for _, ref := range resolved.TypeRefs {
		if ref.File != path {
			continue
		}
		off, ok := nativeResolveOriginalStart(files, path, ref.Start, ref.End)
		if !ok {
			continue
		}
		kind := kindByID[ref.TargetSymbolID]
		if kind == "" {
			kind = kindByTarget[nativeResolveTargetKey(ref.TargetFile, ref.TargetNode, ref.TargetStart, ref.TargetEnd)]
		}
		if kind == "" {
			if ref.TargetNode < 0 {
				kind = "builtin"
			} else {
				kind = "struct"
			}
		}
		out[off] = kind
	}
	return out
}

func nativeResolveTargetKey(file string, node, start, end int) string {
	return file + "\x00" + fmt.Sprint(node) + "\x00" + fmt.Sprint(start) + "\x00" + fmt.Sprint(end)
}

func nativeResolveDiagnosticsFromArtifacts(resolved api.ResolveResult, files []nativeResolveFileInfo) []*diag.Diagnostic {
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

func nativeResolveOriginalStart(files []nativeResolveFileInfo, path string, start, end int) (int, bool) {
	span, ok := nativeResolveSpan(files, path, start, end)
	if !ok {
		return 0, false
	}
	return span.Start.Offset, true
}

func nativeResolveOriginalSpan(files []nativeResolveFileInfo, path string, start, end int) (int, int, bool) {
	span, ok := nativeResolveSpan(files, path, start, end)
	if !ok {
		return 0, 0, false
	}
	return span.Start.Offset, span.End.Offset, true
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

func nativeResolveSemanticKindLabel(kind string) string {
	switch kind {
	case "fn":
		return "function"
	case "value":
		return "binding"
	case "type":
		// The selfhost resolver currently emits one broad "type" kind for
		// structs/enums/interfaces/aliases. The LSP policy only needs the
		// semantic-token family here, so use any concrete type label it already
		// treats as "type".
		return "struct"
	case "alias":
		return "type alias"
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
