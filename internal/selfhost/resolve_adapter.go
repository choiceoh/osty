package selfhost

import (
	"github.com/osty/osty/internal/diag"
)

// PackageResolveFile is the per-file input shape accepted by the structured
// self-host resolve adapter.
type PackageResolveFile = PackageCheckFile

// PackageResolveInput batches one or more source files into a synthetic package
// so the self-host resolver can see one shared top-level namespace.
type PackageResolveInput struct {
	Files []PackageResolveFile `json:"files,omitempty"`
	// Cfg, when non-nil, activates the `#[cfg(key = "value")]` pre-resolve
	// filter per LANG_SPEC v0.5 §5 / G29. A nil Cfg leaves every decl
	// alive (cfg shape validation still emits E0405/E0739 either way).
	Cfg *CfgEnv `json:"cfg,omitempty"`
}

// UseEdge is one `use <target>` edge in the workspace import graph.
// Pos / EndPos are source offsets pointing at the use site; callers
// render them into line/column via their own source-map when emitting
// diagnostics.
type UseEdge struct {
	Target string
	Pos    int
	EndPos int
	File   string
}

// PackageUses groups every non-FFI use edge emitted by one package.
// Path is the dotted package key (same format as
// internal/resolve::UseKey).
type PackageUses struct {
	Path string
	Uses []UseEdge
}

// WorkspaceUses is the cross-package input accepted by
// DetectImportCycles. Callers should sort Packages lexicographically
// by Path so the diagnostic emission order stays deterministic — the
// detector respects the given order verbatim.
type WorkspaceUses struct {
	Packages []PackageUses
}

// CycleDiag is one cyclic-import diagnostic record carrying the edge
// that closed the cycle. Callers convert this to a rich
// diag.Diagnostic by rendering Pos / EndPos through their source map.
type CycleDiag struct {
	Importer string
	Target   string
	Pos      int
	EndPos   int
	File     string
	Message  string
}

// DetectImportCycles walks the given workspace graph and returns one
// CycleDiag per edge that completes a cycle. Targets absent from
// Packages are ignored (matches the Go resolver's existing
// behaviour: stub / external-dep packages contribute no edges). The
// underlying DFS lives in toolchain/resolve.osty; callers on the Go
// side build the graph, dispatch here, and translate the returned
// diagnostics back into the host's diag.Diagnostic format.
func DetectImportCycles(input WorkspaceUses) []CycleDiag {
	self := toSelfWorkspaceUses(input)
	diags := selfDetectImportCycles(self)
	out := make([]CycleDiag, 0, len(diags))
	for _, d := range diags {
		out = append(out, CycleDiag{
			Importer: d.importer,
			Target:   d.target,
			Pos:      d.pos,
			EndPos:   d.endPos,
			File:     d.file,
			Message:  d.message,
		})
	}
	return out
}

func toSelfWorkspaceUses(w WorkspaceUses) *SelfWorkspaceUses {
	packages := make([]*SelfPackageUses, 0, len(w.Packages))
	for _, p := range w.Packages {
		uses := make([]*SelfUseEdge, 0, len(p.Uses))
		for _, e := range p.Uses {
			uses = append(uses, &SelfUseEdge{
				target: e.Target,
				pos:    e.Pos,
				endPos: e.EndPos,
				file:   e.File,
			})
		}
		packages = append(packages, &SelfPackageUses{
			path: p.Path,
			uses: uses,
		})
	}
	return &SelfWorkspaceUses{packages: packages}
}

// CfgEnv carries the values that `#[cfg(...)]` predicates compare against.
// Mirrors toolchain/resolve.osty::SelfResolveCfgEnv and the internal/resolve
// Go-side CfgEnv — kept as a separate type so the selfhost package has no
// cycle with internal/resolve.
type CfgEnv struct {
	OS       string   `json:"os,omitempty"`
	Arch     string   `json:"arch,omitempty"`
	Target   string   `json:"target,omitempty"`
	Features []string `json:"features,omitempty"`
}

// toSelf converts the external CfgEnv into the selfhost-generated struct the
// Osty resolver consumes. A nil receiver maps to the disabled sentinel so
// the walk behaves identically to callers that never passed an env.
func (c *CfgEnv) toSelf() *SelfResolveCfgEnv {
	if c == nil {
		return selfResolveCfgDisabled()
	}
	features := c.Features
	if features == nil {
		features = []string{}
	}
	return selfResolveCfgEnv(c.OS, c.Arch, c.Target, features)
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
	return ResolveSourceStructuredWithCfg(src, nil)
}

// ResolveSourceStructuredWithCfg is ResolveSourceStructured plus the
// `#[cfg(...)]` pre-resolve filter. Pass nil to disable filtering while
// still receiving E0405/E0739 validation diagnostics on malformed cfg args.
func ResolveSourceStructuredWithCfg(src []byte, cfg *CfgEnv) ResolveResult {
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
		selfResolveAstFileWithCfg(file, cfg.toSelf()),
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

// ResolveFromSource parses src once and returns the parse diagnostics
// together with the structured resolve result annotated with path.
// Keeps the internal FrontendRun hidden from callers that only need
// source-in / result-out, which is the subprocess-compatible shape.
func ResolveFromSource(src []byte, path string) ([]*diag.Diagnostic, ResolveResult) {
	run := Run(src)
	if run == nil {
		return nil, ResolveResult{}
	}
	return run.Diagnostics(), ResolveStructuredFromRunForPath(run, path)
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

// ResolvePackageStructured re-parses each input file via the self-host
// lexer + parser, merges the per-file AstArenas into a synthetic package
// arena, and runs the self-host resolver over the merged namespace.
// Source text is the sole AST ingress — no *ast.File round-trip. Cfg
// filtering activates when input.Cfg is non-nil.
func ResolvePackageStructured(input PackageResolveInput) (ResolveResult, error) {
	cfg := input.Cfg.toSelf()
	file, layout, err := selfhostBuildPackageAst(input.Files)
	if err != nil {
		return ResolveResult{}, err
	}
	if file == nil {
		return ResolveResult{}, nil
	}
	result := adaptResolveResult(
		selfResolveAstFileWithCfg(file, cfg),
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
