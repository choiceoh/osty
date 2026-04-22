package selfhost

import "errors"

// PackageResolveFile is the per-file input shape accepted by the structured
// self-host resolve adapter.
type PackageResolveFile = PackageCheckFile

// PackageResolveInput batches one or more source files into a synthetic package
// so the self-host resolver can see one shared top-level namespace.
type PackageResolveInput struct {
	Files []PackageResolveFile `json:"files,omitempty"`
}

// ResolveSummary is the exported Go summary for the bootstrapped Osty
// resolver.
type ResolveSummary struct {
	Symbols           int
	Refs              int
	TypeRefs          int
	Diagnostics       int
	Unresolved        int
	Duplicates        int
	SymbolsByKind     map[string]int
	DiagnosticsByCode map[string]int
}

// ResolvedSymbol records one symbol declared by the self-host resolver.
type ResolvedSymbol struct {
	Node     int
	Name     string
	Kind     string
	TypeName string
	Arity    int
	Depth    int
	Start    int
	End      int
	Public   bool
	File     string
}

// ResolvedRef records one value/name reference plus its resolved target span
// when available.
type ResolvedRef struct {
	Name        string
	Node        int
	Start       int
	End         int
	File        string
	TargetNode  int
	TargetStart int
	TargetEnd   int
	TargetFile  string
}

// ResolvedTypeRef records one resolved type-name reference.
type ResolvedTypeRef struct {
	Name  string
	Node  int
	Start int
	End   int
	File  string
}

// ResolveDiagnosticRecord is one structured diagnostic produced by the
// self-host resolver.
type ResolveDiagnosticRecord struct {
	Code    string
	Message string
	Name    string
	Hint    string
	Node    int
	Start   int
	End     int
	File    string
}

// ResolveResult is the structured Go-facing surface for the bootstrapped
// resolver.
type ResolveResult struct {
	Summary     ResolveSummary
	Symbols     []ResolvedSymbol
	Refs        []ResolvedRef
	TypeRefs    []ResolvedTypeRef
	Diagnostics []ResolveDiagnosticRecord
}

// ResolveSource runs the bootstrapped Osty resolver over one source string and
// returns only the summary counters.
func ResolveSource(src []byte) ResolveSummary {
	return ResolveSourceStructured(src).Summary
}

// ResolveSourceStructured runs the bootstrapped Osty resolver over one source
// string and returns the structured result.
func ResolveSourceStructured(src []byte) ResolveResult {
	lexed := ostyLexSource(string(src))
	if lexed == nil {
		return ResolveResult{}
	}
	file := astParseLexedSource(lexed)
	if file == nil {
		return ResolveResult{}
	}
	rt := newRuneTable(lexed.source)
	return adaptResolveResult(
		selfResolveAstFile(file),
		file,
		func(start, end int) (int, int) {
			return checkNodeOffsets(rt, lexed.stream, start, end)
		},
	)
}

// ResolveStructuredFromRun runs the self-host resolver directly on the
// arena produced by an existing FrontendRun, skipping both the secondary
// lex/parse pass done by ResolveSourceStructured and the *ast.File →
// AstArena round-trip done by ResolvePackageStructured. Output matches
// ResolveSourceStructured(src) for the same source. Callers that already
// hold a FrontendRun should prefer this entry point so the astbridge
// detour becomes dead code for the resolve pass.
func ResolveStructuredFromRun(run *FrontendRun) ResolveResult {
	if run == nil {
		return ResolveResult{}
	}
	file := run.astFile()
	if file == nil {
		return ResolveResult{}
	}
	rt := run.rt
	stream := run.stream
	return adaptResolveResult(
		selfResolveAstFile(file),
		file,
		func(start, end int) (int, int) {
			return checkNodeOffsets(rt, stream, start, end)
		},
	)
}

// ResolveStructuredFromRunForPath is ResolveStructuredFromRun plus
// single-file annotation: Symbols / Refs / TypeRefs / Diagnostics all
// carry path, and each Ref's TargetFile is set to path when the resolver
// found a declared (non-builtin) target. Use this when the caller
// already knows the source file path and wants the same File /
// TargetFile fields that ResolvePackageStructured produces for the
// single-file case, without going through the *ast.File round-trip.
func ResolveStructuredFromRunForPath(run *FrontendRun, path string) ResolveResult {
	result := ResolveStructuredFromRun(run)
	if path == "" {
		return result
	}
	for i := range result.Symbols {
		result.Symbols[i].File = path
	}
	for i := range result.Refs {
		result.Refs[i].File = path
		if result.Refs[i].TargetNode >= 0 {
			result.Refs[i].TargetFile = path
		}
	}
	for i := range result.TypeRefs {
		result.TypeRefs[i].File = path
	}
	for i := range result.Diagnostics {
		result.Diagnostics[i].File = path
	}
	return result
}

// ResolvePackageStructured lowers one structured package input into a
// synthetic selfhost AST and runs the self-host resolver over the merged
// package namespace.
func ResolvePackageStructured(input PackageResolveInput) (ResolveResult, error) {
	if selfhostCanBuildPackageAstDirect(input.Files) {
		file, _, err := selfhostBuildPackageAstDirect(input.Files)
		if err == nil {
			if file == nil {
				return ResolveResult{}, nil
			}
			result := adaptResolveResult(
				selfResolveAstFile(file),
				file,
				func(start, end int) (int, int) {
					if end < start {
						end = start
					}
					return start, end
				},
			)
			selfhostAnnotateResolveFiles(&result, input.Files)
			return result, nil
		}
		var unsupported *selfhostLoweringUnsupported
		if !errors.As(err, &unsupported) {
			return ResolveResult{}, err
		}
	}
	file, layout, err := selfhostBuildPackageAst(input.Files)
	if err != nil {
		return ResolveResult{}, err
	}
	if file == nil {
		return ResolveResult{}, nil
	}
	result := adaptResolveResult(
		selfResolveAstFile(file),
		file,
		func(start, end int) (int, int) {
			return checkNodeOffsetsWithTokenLayout(layout, start, end)
		},
	)
	selfhostAnnotateResolveFiles(&result, input.Files)
	return result, nil
}

func adaptResolveSummary(resolved *SelfResolveResult) ResolveSummary {
	if resolved == nil {
		return ResolveSummary{}
	}
	summary := ResolveSummary{
		Symbols:           len(resolved.symbols),
		Refs:              resolved.refs,
		TypeRefs:          resolved.typeRefs,
		Diagnostics:       len(resolved.diagnostics),
		Unresolved:        resolved.unresolved,
		Duplicates:        resolved.duplicates,
		SymbolsByKind:     map[string]int{},
		DiagnosticsByCode: map[string]int{},
	}
	for _, sym := range resolved.symbols {
		if sym == nil || sym.kind == "" {
			continue
		}
		summary.SymbolsByKind[sym.kind]++
	}
	for _, d := range resolved.diagnostics {
		if d == nil || d.code == "" {
			continue
		}
		summary.DiagnosticsByCode[d.code]++
	}
	if len(summary.SymbolsByKind) == 0 {
		summary.SymbolsByKind = nil
	}
	if len(summary.DiagnosticsByCode) == 0 {
		summary.DiagnosticsByCode = nil
	}
	return summary
}

func adaptResolveResult(resolved *SelfResolveResult, file *AstFile, offsets func(start, end int) (int, int)) ResolveResult {
	if resolved == nil {
		return ResolveResult{}
	}
	result := ResolveResult{
		Summary:     adaptResolveSummary(resolved),
		Symbols:     make([]ResolvedSymbol, 0, len(resolved.symbols)),
		Refs:        make([]ResolvedRef, 0, minResolveRefCount(resolved)),
		TypeRefs:    make([]ResolvedTypeRef, 0, minResolveTypeRefCount(resolved)),
		Diagnostics: make([]ResolveDiagnosticRecord, 0, len(resolved.diagnostics)),
	}
	for _, sym := range resolved.symbols {
		if sym == nil {
			continue
		}
		start, end := offsets(sym.start, sym.end)
		result.Symbols = append(result.Symbols, ResolvedSymbol{
			Node:     sym.node,
			Name:     sym.name,
			Kind:     sym.kind,
			TypeName: sym.typeName,
			Arity:    sym.arity,
			Depth:    sym.depth,
			Start:    start,
			End:      end,
			Public:   sym.public,
		})
	}
	for i := 0; i < minResolveRefCount(resolved); i++ {
		start, end := selfhostResolveNodeOffsets(file, resolved.refNodes[i], offsets)
		targetStart, targetEnd := offsets(resolved.refTargetStarts[i], resolved.refTargetEnds[i])
		result.Refs = append(result.Refs, ResolvedRef{
			Name:        resolved.refNames[i],
			Node:        resolved.refNodes[i],
			Start:       start,
			End:         end,
			TargetNode:  resolved.refTargets[i],
			TargetStart: targetStart,
			TargetEnd:   targetEnd,
		})
	}
	for i := 0; i < minResolveTypeRefCount(resolved); i++ {
		start, end := selfhostResolveNodeOffsets(file, resolved.typeRefNodes[i], offsets)
		result.TypeRefs = append(result.TypeRefs, ResolvedTypeRef{
			Name:  resolved.typeRefNames[i],
			Node:  resolved.typeRefNodes[i],
			Start: start,
			End:   end,
		})
	}
	for _, d := range resolved.diagnostics {
		if d == nil {
			continue
		}
		start, end := offsets(d.start, d.end)
		result.Diagnostics = append(result.Diagnostics, ResolveDiagnosticRecord{
			Code:    d.code,
			Message: d.message,
			Name:    d.name,
			Hint:    d.hint,
			Node:    d.node,
			Start:   start,
			End:     end,
		})
	}
	return result
}

func minResolveRefCount(resolved *SelfResolveResult) int {
	if resolved == nil {
		return 0
	}
	n := len(resolved.refNames)
	if len(resolved.refNodes) < n {
		n = len(resolved.refNodes)
	}
	if len(resolved.refTargets) < n {
		n = len(resolved.refTargets)
	}
	if len(resolved.refTargetStarts) < n {
		n = len(resolved.refTargetStarts)
	}
	if len(resolved.refTargetEnds) < n {
		n = len(resolved.refTargetEnds)
	}
	return n
}

func minResolveTypeRefCount(resolved *SelfResolveResult) int {
	if resolved == nil {
		return 0
	}
	n := len(resolved.typeRefNames)
	if len(resolved.typeRefNodes) < n {
		n = len(resolved.typeRefNodes)
	}
	return n
}

func selfhostResolveNodeOffsets(file *AstFile, node int, offsets func(start, end int) (int, int)) (int, int) {
	if file == nil || file.arena == nil || node < 0 || node >= len(file.arena.nodes) {
		return 0, 0
	}
	n := file.arena.nodes[node]
	if n == nil {
		return 0, 0
	}
	return offsets(n.start, n.end)
}

func selfhostAnnotateResolveFiles(result *ResolveResult, files []PackageResolveFile) {
	if result == nil || len(files) == 0 {
		return
	}
	for i := range result.Symbols {
		result.Symbols[i].File = selfhostResolveFilePath(files, result.Symbols[i].Start)
	}
	for i := range result.Refs {
		result.Refs[i].File = selfhostResolveFilePath(files, result.Refs[i].Start)
		result.Refs[i].TargetFile = selfhostResolveFilePath(files, result.Refs[i].TargetStart)
	}
	for i := range result.TypeRefs {
		result.TypeRefs[i].File = selfhostResolveFilePath(files, result.TypeRefs[i].Start)
	}
	for i := range result.Diagnostics {
		result.Diagnostics[i].File = selfhostResolveFilePath(files, result.Diagnostics[i].Start)
	}
}

func selfhostResolveFilePath(files []PackageResolveFile, offset int) string {
	for _, file := range files {
		if file.Path == "" || len(file.Source) == 0 {
			continue
		}
		start := file.Base
		end := file.Base + len(file.Source)
		if offset >= start && offset <= end {
			return file.Path
		}
	}
	return ""
}
