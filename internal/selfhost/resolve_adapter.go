package selfhost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost/api"
)

// Resolve-side types re-exported from internal/selfhost/api. See
// selfhost/api/package.go for definitions.
type (
	PackageResolveFile  = api.PackageResolveFile
	PackageResolveInput = api.PackageResolveInput
	UseEdge             = api.UseEdge
	PackageUses         = api.PackageUses
	WorkspaceUses       = api.WorkspaceUses
	CycleDiag           = api.CycleDiag
	MemberLookupStatus  = api.MemberLookupStatus
	MemberLookupResult  = api.MemberLookupResult
)

const (
	MemberLookupOK      = api.MemberLookupOK
	MemberLookupPrivate = api.MemberLookupPrivate
	MemberLookupMissing = api.MemberLookupMissing
)

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

// LookupPackageMember decides which (if any) diagnostic to emit for a
// cross-package `pkg.member` access. The caller owns the Scope lookup
// and symbol deref: `found` must be true when pkgScope returned a
// symbol for `member`, and `public` must reflect that symbol's `pub`
// flag. `typePos` tightens the E0508 wording from "name" to "type"
// when the access site is in a type position. The message wording
// lives in toolchain/resolve.osty so golden snapshots stay stable
// regardless of which side emits the diagnostic.
func LookupPackageMember(pkgName, member string, typePos, found, public bool) MemberLookupResult {
	r := selfLookupPackageMember(pkgName, member, typePos, found, public)
	return MemberLookupResult{
		Status:  MemberLookupStatus(r.status),
		Code:    r.code,
		Message: r.message,
		Primary: r.primary,
		Note:    r.note,
		Hint:    r.hint,
	}
}

// ReexportPrivateDiagnostic owns the E0553 wording for `pub use` of a private
// package member. The Go resolver still knows which imported symbol is private,
// but it converts this selfhost-owned diagnostic shape instead of rendering its
// own message.
func ReexportPrivateDiagnostic(pkgName, member string) MemberLookupResult {
	target := pkgName + "." + member
	return MemberLookupResult{
		Status:  MemberLookupPrivate,
		Code:    diag.CodeReexportPrivate,
		Message: fmt.Sprintf("`pub use` cannot re-export private symbol `%s`", target),
		Primary: "private re-export",
		Note:    fmt.Sprintf("`%s` is declared without `pub` in package `%s`", member, pkgName),
		Hint:    fmt.Sprintf("make `%s` public or remove `pub` from this use", member),
	}
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

// CfgEnv is re-exported from internal/selfhost/api. See
// selfhost/api/package.go for the definition.
type CfgEnv = api.CfgEnv

// cfgEnvToSelf converts the external CfgEnv into the selfhost-
// generated struct the Osty resolver consumes. A nil env maps to the
// disabled sentinel so the walk behaves identically to callers that
// never passed one.
func cfgEnvToSelf(c *CfgEnv) *SelfResolveCfgEnv {
	if c == nil {
		return selfResolveCfgDisabled()
	}
	features := c.Features
	if features == nil {
		features = []string{}
	}
	return selfResolveCfgEnv(c.OS, c.Arch, c.Target, features)
}

// Resolver types are re-exported from internal/selfhost/api via
// type aliases; see selfhost/api/types.go for definitions.
type (
	ResolveSummary          = api.ResolveSummary
	ResolvedSymbol          = api.ResolvedSymbol
	ResolvedRef             = api.ResolvedRef
	ResolvedTypeRef         = api.ResolvedTypeRef
	ResolveDiagnosticRecord = api.ResolveDiagnosticRecord
	ResolveResult           = api.ResolveResult
)

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
	file := selfhostSemanticAstFile(astParseLexedSource(lexed))
	if file == nil {
		return ResolveResult{}
	}
	rt := newRuneTable(lexed.source)
	return adaptResolveResult(
		selfResolveAstFileWithCfg(file, cfgEnvToSelf(cfg)),
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
	file := run.semanticAstFile()
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
		selfhostAssignResolveIDs(&result, "")
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
	selfhostAssignResolveIDs(&result, path)
	return result
}

// ResolvePackageStructured re-parses each input file via the self-host
// lexer + parser, merges the per-file AstArenas into a synthetic package
// arena, and runs the self-host resolver over the merged namespace.
// Source text is the sole AST ingress — no *ast.File round-trip. Cfg
// filtering activates when input.Cfg is non-nil.
func ResolvePackageStructured(input PackageResolveInput) (ResolveResult, error) {
	cfg := cfgEnvToSelf(input.Cfg)
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
	selfhostApplyResolveImportSurfaces(&result, file, input.Imports, func(start, end int) (int, int) {
		return checkNodeOffsetsWithTokenLayout(layout, start, end)
	})
	selfhostAnnotateResolveFiles(&result, input.Files)
	selfhostAssignResolveIDs(&result, selfhostResolvePackageKey(input))
	return result, nil
}

func selfhostApplyResolveImportSurfaces(result *ResolveResult, file *AstFile, imports []PackageCheckImport, offsets func(start, end int) (int, int)) {
	if result == nil || file == nil || file.arena == nil || len(imports) == 0 {
		return
	}
	byAlias := map[string]PackageCheckImport{}
	for _, imp := range imports {
		if imp.Alias == "" {
			continue
		}
		byAlias[imp.Alias] = imp
	}
	for _, declIdx := range file.arena.decls {
		if declIdx < 0 || declIdx >= len(file.arena.nodes) {
			continue
		}
		selfhostApplyResolveUseImportSurface(result, file.arena, declIdx, byAlias, offsets)
	}
}

func selfhostApplyResolveUseImportSurface(result *ResolveResult, arena *AstArena, idx int, imports map[string]PackageCheckImport, offsets func(start, end int) (int, int)) {
	if idx < 0 || idx >= len(arena.nodes) {
		return
	}
	n := arena.nodes[idx]
	if n == nil {
		return
	}
	if _, ok := n.kind.(*AstNodeKind_AstNUseDecl); !ok {
		return
	}
	if arenaUseDeclIsGroup(n) {
		for _, childIdx := range n.children {
			selfhostApplyResolveUseImportSurface(result, arena, childIdx, imports, offsets)
		}
		return
	}
	if !arenaUseDeclIsScoped(n) {
		return
	}
	raw := arenaStringUnquote(n.text)
	base, member, ok := arenaSplitScopedUsePath(raw)
	if !ok {
		return
	}
	imp := imports[arenaUsePathLastSegment(base)]
	if imp.Alias == "" {
		return
	}
	kind, ok := selfhostResolveImportMemberKind(imp, member)
	if !ok {
		return
	}
	name := arenaUseDeclAlias(arena, n)
	if name == "" {
		name = member
	}
	start, end := offsets(n.start, n.end)
	for i := range result.Symbols {
		sym := &result.Symbols[i]
		if sym.Name != name || sym.Start != start || sym.End != end {
			continue
		}
		sym.Kind = kind
		return
	}
}

func selfhostResolveImportMemberKind(imp PackageCheckImport, member string) (string, bool) {
	for _, fn := range imp.Functions {
		if fn.Owner == "" && fn.Name == member {
			return "fn", true
		}
	}
	for _, typ := range imp.TypeDecls {
		if typ.Name == member || arenaLocalTypeName(typ.Name) == member {
			return "type", true
		}
	}
	for _, alias := range imp.Aliases {
		if alias.Name == member || arenaLocalTypeName(alias.Name) == member {
			return "type", true
		}
	}
	for _, variant := range imp.Variants {
		if variant.Name == member {
			return "variant", true
		}
	}
	for _, field := range imp.Fields {
		if field.Owner == imp.Alias && field.Name == member {
			return "value", true
		}
	}
	return "", false
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
		Refs:        make([]ResolvedRef, 0, len(resolved.refList)),
		TypeRefs:    make([]ResolvedTypeRef, 0, len(resolved.typeRefList)),
		Diagnostics: make([]ResolveDiagnosticRecord, 0, len(resolved.diagnostics)),
	}
	for _, sym := range resolved.symbols {
		if sym == nil {
			continue
		}
		start, end := offsets(sym.start, sym.end)
		result.Symbols = append(result.Symbols, ResolvedSymbol{
			Node:   sym.node,
			Name:   sym.name,
			Kind:   sym.kind,
			Type:   apiTypeReprFromLegacyName(sym.typeName),
			Arity:  sym.arity,
			Depth:  sym.depth,
			Start:  start,
			End:    end,
			Public: sym.public,
		})
	}
	for _, ref := range resolved.refList {
		if ref == nil {
			continue
		}
		start, end := selfhostResolveNodeOffsets(file, ref.node, offsets)
		targetStart, targetEnd := offsets(ref.targetStart, ref.targetEnd)
		result.Refs = append(result.Refs, ResolvedRef{
			Name:        ref.name,
			Node:        ref.node,
			Start:       start,
			End:         end,
			TargetNode:  ref.target,
			TargetStart: targetStart,
			TargetEnd:   targetEnd,
		})
	}
	for _, tref := range resolved.typeRefList {
		if tref == nil {
			continue
		}
		start, end := selfhostResolveNodeOffsets(file, tref.node, offsets)
		result.TypeRefs = append(result.TypeRefs, ResolvedTypeRef{
			Name:  tref.name,
			Node:  tref.node,
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
	selfhostAssignResolveIDs(&result, "")
	return result
}

func selfhostResolvePackageKey(input PackageResolveInput) string {
	if input.PackagePath != "" {
		return input.PackagePath
	}
	parts := make([]string, 0, len(input.Files))
	for _, file := range input.Files {
		if file.Path != "" {
			parts = append(parts, file.Path)
		} else if file.Name != "" {
			parts = append(parts, file.Name)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x00")
}

func selfhostAssignResolveIDs(result *ResolveResult, packageKey string) {
	if result == nil {
		return
	}
	packageID := stableResolveID("package", packageKey)
	result.PackageID = packageID
	byTarget := map[string]string{}
	for i := range result.Symbols {
		sym := &result.Symbols[i]
		sym.PackageID = packageID
		sym.DeclID = stableResolveID("decl", packageKey, sym.File, sym.Kind, sym.Name, fmt.Sprint(sym.Node), fmt.Sprint(sym.Start), fmt.Sprint(sym.End))
		sym.ID = stableResolveID("symbol", packageKey, sym.File, sym.Kind, sym.Name, fmt.Sprint(sym.Node), fmt.Sprint(sym.Start), fmt.Sprint(sym.End), fmt.Sprint(sym.Public))
		byTarget[resolveTargetKey(sym.File, sym.Node, sym.Start, sym.End)] = sym.ID
	}
	for i := range result.Refs {
		ref := &result.Refs[i]
		ref.PackageID = packageID
		ref.BindingID = stableResolveID("binding", packageKey, ref.File, ref.Name, fmt.Sprint(ref.Node), fmt.Sprint(ref.Start), fmt.Sprint(ref.End))
		ref.ID = stableResolveID("ref", packageKey, ref.File, ref.Name, fmt.Sprint(ref.Node), fmt.Sprint(ref.Start), fmt.Sprint(ref.End), fmt.Sprint(ref.TargetNode), fmt.Sprint(ref.TargetStart), fmt.Sprint(ref.TargetEnd), ref.TargetFile)
		if id := byTarget[resolveTargetKey(ref.TargetFile, ref.TargetNode, ref.TargetStart, ref.TargetEnd)]; id != "" {
			ref.TargetSymbolID = id
		} else if ref.TargetNode >= 0 {
			ref.TargetSymbolID = stableResolveID("symbol-target", packageKey, ref.TargetFile, ref.Name, fmt.Sprint(ref.TargetNode), fmt.Sprint(ref.TargetStart), fmt.Sprint(ref.TargetEnd))
		}
	}
	for i := range result.TypeRefs {
		ref := &result.TypeRefs[i]
		ref.PackageID = packageID
		ref.ID = stableResolveID("type-ref", packageKey, ref.File, ref.Name, fmt.Sprint(ref.Node), fmt.Sprint(ref.Start), fmt.Sprint(ref.End))
	}
	for i := range result.Diagnostics {
		d := &result.Diagnostics[i]
		d.PackageID = packageID
		d.ID = stableResolveID("diagnostic", packageKey, d.File, d.Code, d.Name, fmt.Sprint(d.Node), fmt.Sprint(d.Start), fmt.Sprint(d.End), d.Message)
	}
}

func resolveTargetKey(file string, node, start, end int) string {
	return file + "\x00" + fmt.Sprint(node) + "\x00" + fmt.Sprint(start) + "\x00" + fmt.Sprint(end)
}

func stableResolveID(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
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
