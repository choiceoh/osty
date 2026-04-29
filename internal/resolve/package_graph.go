package resolve

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/spanid"
	"github.com/osty/osty/internal/token"
)

// PackageGraph is the first-class compile target IR. It snapshots the packages,
// source files, import edges, stdlib/external-dependency routing, cfg state, and
// source-transform metadata that older callers reached through Workspace
// helpers directly.
type PackageGraph struct {
	Root string

	Packages map[string]*PackageGraphPackage
	Order    []string
	Files    []PackageGraphFile

	Imports          []PackageGraphImport
	Edges            []PackageGraphEdge
	StdlibEdges      []PackageGraphEdge
	ExternalDepEdges []PackageGraphEdge

	Cfg      *CfgEnv
	Features []string

	SourceTransforms []PackageGraphSourceTransform

	workspace *Workspace
}

// PackageGraphPackage is the graph node for one loaded package.
type PackageGraphPackage struct {
	Path              string
	Dir               string
	Name              string
	Package           *Package
	Files             []PackageGraphFile
	IsStdlib          bool
	IsExternalDep     bool
	IsStub            bool
	IsCycleMarker     bool
	RuntimeCapability bool
}

// PackageGraphFile is the graph node for one source file.
type PackageGraphFile struct {
	PackagePath            string
	Path                   string
	File                   *PackageFile
	SourceBytes            int
	OriginalBytes          int
	HasSelfhostRun         bool
	HasPublicAST           bool
	SourceTransformApplied bool
	SourceTransformChanged bool
}

// PackageGraphEdgeKind classifies one package import edge.
type PackageGraphEdgeKind string

const (
	PackageGraphEdgeWorkspace  PackageGraphEdgeKind = "workspace"
	PackageGraphEdgeStdlib     PackageGraphEdgeKind = "stdlib"
	PackageGraphEdgeExternal   PackageGraphEdgeKind = "external"
	PackageGraphEdgeUnresolved PackageGraphEdgeKind = "unresolved"
)

// PackageGraphImport is the use-site level import record. For scoped imports,
// Path is the written member path while TargetPath is the package edge target.
type PackageGraphImport struct {
	PackagePath  string
	FilePath     string
	Path         string
	Alias        string
	TargetPath   string
	IsScoped     bool
	ScopedBase   string
	ScopedMember string
	IsPub        bool
	Pos          token.Pos
	End          token.Pos
	Kind         PackageGraphEdgeKind
	Resolved     bool
}

// PackageGraphEdge is the package-level edge derived from a PackageGraphImport.
type PackageGraphEdge struct {
	From       string
	To         string
	FilePath   string
	ImportPath string
	Alias      string
	Kind       PackageGraphEdgeKind
	IsPub      bool
	Pos        token.Pos
	End        token.Pos
	Resolved   bool
}

// PackageGraphSourceTransform records parser-facing source transform state for
// graph consumers that need to distinguish on-disk bytes from compiler input.
type PackageGraphSourceTransform struct {
	PackagePath string
	FilePath    string
	Applied     bool
	Changed     bool
	InputBytes  int
	OutputBytes int
}

// NewPackageGraph snapshots a Workspace as a first-class package graph. Loading
// still happens before this call; graph construction is side-effect free.
func NewPackageGraph(ws *Workspace) *PackageGraph {
	g := &PackageGraph{
		Packages:  map[string]*PackageGraphPackage{},
		workspace: ws,
	}
	if ws == nil {
		return g
	}
	g.Root = ws.Root
	g.Cfg = cloneCfgEnv(workspaceEffectiveCfgEnv(ws))
	g.Features = sortedCfgFeatures(g.Cfg)

	paths := make([]string, 0, len(ws.Packages))
	for path := range ws.Packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	paths = ws.resolveOrder(paths)
	for _, path := range paths {
		g.addPackage(path, ws.Packages[path])
	}
	for _, path := range paths {
		g.addPackageImports(ws, path, ws.Packages[path])
	}
	return g
}

// NewSingleFilePackageGraph builds a graph for in-memory single-file compile
// paths. When stdlib is provided the graph carries the same stdlib-only
// workspace that ResolveFileSourceDefault has historically used.
func NewSingleFilePackageGraph(src []byte, file *ast.File, stdlib StdlibProvider) *PackageGraph {
	pkg := &Package{
		Name: "<file>",
		Files: []*PackageFile{{
			Path:            "<input>",
			Source:          append([]byte(nil), src...),
			SourceFileID:    spanid.SourceFileIDFor("<input>"),
			CanonicalSource: append([]byte(nil), src...),
			File:            file,
		}},
	}
	if stdlib != nil {
		ws := newStdlibOnlyWorkspace(stdlib)
		ws.Packages[""] = pkg
		return NewPackageGraph(ws)
	}
	g := &PackageGraph{Packages: map[string]*PackageGraphPackage{}}
	g.addPackage("", pkg)
	g.addPackageImports(nil, "", pkg)
	return g
}

// NewPackageGraphForPackage snapshots a standalone already-loaded package.
// Use this for package-mode compile paths that have no workspace/dependency
// provider attached.
func NewPackageGraphForPackage(path string, pkg *Package) *PackageGraph {
	g := &PackageGraph{Packages: map[string]*PackageGraphPackage{}}
	g.addPackage(path, pkg)
	g.addPackageImports(nil, path, pkg)
	return g
}

// Workspace returns a frozen workspace projection for legacy adapters. It
// contains exactly the packages captured in the graph and disables root-based
// lazy loading, so graph consumers do not silently widen the compile target.
func (g *PackageGraph) Workspace() *Workspace {
	return g.resolveWorkspace()
}

// Package returns the loaded resolver package for path, if present.
func (g *PackageGraph) Package(path string) *Package {
	if g == nil || g.Packages == nil {
		return nil
	}
	node := g.Packages[path]
	if node == nil {
		return nil
	}
	return node.Package
}

// PackagePaths returns graph package paths in deterministic dependency order.
func (g *PackageGraph) PackagePaths() []string {
	if g == nil {
		return nil
	}
	return append([]string(nil), g.Order...)
}

// ImportSurfaces projects graph import edges for one package into the
// selfhost package-import surface used by resolve/check adapters.
func (g *PackageGraph) ImportSurfaces(pkgPath string) []api.PackageCheckImport {
	if g == nil {
		return nil
	}
	seen := map[string]string{}
	var out []api.PackageCheckImport
	for _, imp := range g.Imports {
		if imp.PackagePath != pkgPath || imp.Alias == "" || imp.TargetPath == "" {
			continue
		}
		target := g.Package(imp.TargetPath)
		if target == nil && g.workspace != nil && g.workspace.Stdlib != nil {
			target = g.workspace.Stdlib.LookupPackage(imp.TargetPath)
		}
		if target == nil {
			continue
		}
		if prev, ok := seen[imp.Alias]; ok {
			if prev == imp.TargetPath {
				continue
			}
			continue
		}
		seen[imp.Alias] = imp.TargetPath
		out = append(out, PackageExportSurface(imp.TargetPath, imp.Alias, target))
	}
	return out
}

// PackageGraphImportSurfaces is the package-level helper for callers that
// should consume PackageGraph without reaching back into Workspace state.
func PackageGraphImportSurfaces(g *PackageGraph, pkgPath string) []api.PackageCheckImport {
	if g == nil {
		return nil
	}
	return g.ImportSurfaces(pkgPath)
}

// ResolveAll runs resolver output over the graph compile target.
func (g *PackageGraph) ResolveAll() map[string]*PackageResult {
	if g == nil {
		return map[string]*PackageResult{}
	}
	if g.workspace != nil {
		return g.resolveWorkspace().ResolveAll()
	}
	results := map[string]*PackageResult{}
	prelude := NewPrelude()
	for _, path := range g.Order {
		pkg := g.Package(path)
		if pkg == nil {
			continue
		}
		if pkg.isStub {
			results[path] = &PackageResult{PackageScope: nil}
			continue
		}
		results[path] = ResolvePackage(pkg, prelude)
	}
	return results
}

// ResolveGraph is the package-graph entry point for name resolution.
func ResolveGraph(g *PackageGraph) map[string]*PackageResult {
	if g == nil {
		return map[string]*PackageResult{}
	}
	return g.ResolveAll()
}

func (g *PackageGraph) resolveWorkspace() *Workspace {
	if g == nil || g.workspace == nil {
		return nil
	}
	ws := *g.workspace
	ws.Root = ""
	ws.Packages = map[string]*Package{}
	for path, node := range g.Packages {
		if node != nil && node.Package != nil {
			ws.Packages[path] = node.Package
		}
	}
	ws.cfgEnv = cloneCfgEnv(g.Cfg)
	ws.loading = map[string]bool{}
	return &ws
}

func (g *PackageGraph) addPackage(path string, pkg *Package) {
	if pkg == nil {
		return
	}
	if g.Packages == nil {
		g.Packages = map[string]*PackageGraphPackage{}
	}
	node := &PackageGraphPackage{
		Path:              path,
		Dir:               pkg.Dir,
		Name:              pkg.Name,
		Package:           pkg,
		IsStdlib:          strings.HasPrefix(path, StdPrefix),
		IsExternalDep:     pkg.isExternalDep,
		IsStub:            pkg.isStub,
		IsCycleMarker:     pkg.isCycleMarker,
		RuntimeCapability: pkg.RuntimeCapability,
	}
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		file := PackageGraphFile{
			PackagePath:            path,
			Path:                   pf.Path,
			File:                   pf,
			SourceBytes:            len(pf.Source),
			OriginalBytes:          len(pf.OriginalSource),
			HasSelfhostRun:         pf.Run != nil,
			HasPublicAST:           pf.File != nil,
			SourceTransformApplied: pf.SourceTransformApplied,
			SourceTransformChanged: pf.SourceTransformChanged,
		}
		node.Files = append(node.Files, file)
		g.Files = append(g.Files, file)
		if pf.SourceTransformApplied {
			inputBytes := len(pf.Source)
			if pf.OriginalSource != nil {
				inputBytes = len(pf.OriginalSource)
			}
			g.SourceTransforms = append(g.SourceTransforms, PackageGraphSourceTransform{
				PackagePath: path,
				FilePath:    pf.Path,
				Applied:     true,
				Changed:     pf.SourceTransformChanged,
				InputBytes:  inputBytes,
				OutputBytes: len(pf.Source),
			})
		}
	}
	g.Packages[path] = node
	g.Order = append(g.Order, path)
}

func (g *PackageGraph) addPackageImports(ws *Workspace, path string, pkg *Package) {
	for _, imp := range packageGraphImports(pkg, path) {
		imp.Kind = graphEdgeKind(ws, imp.TargetPath)
		imp.Resolved = graphEdgeResolved(ws, imp.TargetPath, imp.Kind)
		g.Imports = append(g.Imports, imp)
		edge := PackageGraphEdge{
			From:       path,
			To:         imp.TargetPath,
			FilePath:   imp.FilePath,
			ImportPath: imp.Path,
			Alias:      imp.Alias,
			Kind:       imp.Kind,
			IsPub:      imp.IsPub,
			Pos:        imp.Pos,
			End:        imp.End,
			Resolved:   imp.Resolved,
		}
		g.Edges = append(g.Edges, edge)
		switch edge.Kind {
		case PackageGraphEdgeStdlib:
			g.StdlibEdges = append(g.StdlibEdges, edge)
		case PackageGraphEdgeExternal:
			g.ExternalDepEdges = append(g.ExternalDepEdges, edge)
		}
	}
}

func packageGraphImports(pkg *Package, pkgPath string) []PackageGraphImport {
	if pkg == nil {
		return nil
	}
	var out []PackageGraphImport
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if pf.Run != nil {
			for _, use := range selfhost.PackageUsesFromRun(pf.Run) {
				if imp, ok := packageGraphImportFromSelfhost(pkgPath, pf, use); ok {
					out = append(out, imp)
				}
			}
			continue
		}
		if pf.File != nil {
			for _, use := range pf.File.Uses {
				if imp, ok := packageGraphImportFromAST(pkgPath, pf, use); ok {
					out = append(out, imp)
				}
			}
			continue
		}
		if len(pf.Source) > 0 {
			run := selfhost.Run(pf.Source)
			for _, use := range selfhost.PackageUsesFromRun(run) {
				if imp, ok := packageGraphImportFromSelfhost(pkgPath, pf, use); ok {
					out = append(out, imp)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		if out[i].Pos.Offset != out[j].Pos.Offset {
			return out[i].Pos.Offset < out[j].Pos.Offset
		}
		return out[i].TargetPath < out[j].TargetPath
	})
	return out
}

func packageGraphImportFromSelfhost(pkgPath string, pf *PackageFile, use selfhost.PackageUseRef) (PackageGraphImport, bool) {
	if use.IsGo || use.Path == "" {
		return PackageGraphImport{}, false
	}
	target := use.Path
	alias := use.Alias
	scopedBase := use.ScopedBase
	if use.IsScoped {
		target = use.ScopedBase
		alias = lastDotSeg(target)
	}
	if alias == "" {
		alias = lastSegment(use.Path)
	}
	if target == "" || alias == "" {
		return PackageGraphImport{}, false
	}
	return PackageGraphImport{
		PackagePath:  pkgPath,
		FilePath:     pf.Path,
		Path:         use.Path,
		Alias:        alias,
		TargetPath:   target,
		IsScoped:     use.IsScoped,
		ScopedBase:   scopedBase,
		ScopedMember: use.ScopedMember,
		IsPub:        use.IsPub,
		Pos:          sourcePosAt(pf.Source, use.Start),
		End:          sourcePosAt(pf.Source, use.End),
	}, true
}

func packageGraphImportFromAST(pkgPath string, pf *PackageFile, use *ast.UseDecl) (PackageGraphImport, bool) {
	if use == nil || use.IsFFI() {
		return PackageGraphImport{}, false
	}
	path := UseKey(use)
	target := path
	alias := useAliasName(use)
	scopedBase := ""
	if use.IsScoped {
		scopedBase = scopedUseBaseKey(use)
		target = scopedBase
		alias = lastDotSeg(target)
	}
	if path == "" || target == "" || alias == "" {
		return PackageGraphImport{}, false
	}
	return PackageGraphImport{
		PackagePath:  pkgPath,
		FilePath:     pf.Path,
		Path:         path,
		Alias:        alias,
		TargetPath:   target,
		IsScoped:     use.IsScoped,
		ScopedBase:   scopedBase,
		ScopedMember: use.ScopedMember,
		IsPub:        use.IsPub,
		Pos:          use.PosV,
		End:          use.EndV,
	}, true
}

func graphEdgeKind(ws *Workspace, target string) PackageGraphEdgeKind {
	if target == "" {
		return PackageGraphEdgeUnresolved
	}
	if strings.HasPrefix(target, StdPrefix) {
		return PackageGraphEdgeStdlib
	}
	if isURLStyle(target) {
		return PackageGraphEdgeExternal
	}
	if ws == nil {
		return PackageGraphEdgeUnresolved
	}
	pkg := ws.Packages[target]
	if pkg == nil {
		return PackageGraphEdgeUnresolved
	}
	if pkg.isExternalDep || packageOutsideWorkspace(ws, pkg) {
		return PackageGraphEdgeExternal
	}
	return PackageGraphEdgeWorkspace
}

func graphEdgeResolved(ws *Workspace, target string, kind PackageGraphEdgeKind) bool {
	if target == "" {
		return false
	}
	if kind == PackageGraphEdgeUnresolved {
		return false
	}
	if ws == nil {
		return false
	}
	return ws.Packages[target] != nil || (ws.Stdlib != nil && ws.Stdlib.LookupPackage(target) != nil)
}

func packageOutsideWorkspace(ws *Workspace, pkg *Package) bool {
	if ws == nil || ws.Root == "" || pkg == nil || pkg.Dir == "" {
		return false
	}
	root, err := filepath.Abs(ws.Root)
	if err != nil {
		return false
	}
	dir, err := filepath.Abs(pkg.Dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func workspaceEffectiveCfgEnv(ws *Workspace) *CfgEnv {
	if ws == nil {
		return nil
	}
	if ws.cfgEnv != nil {
		return ws.cfgEnv
	}
	return DefaultCfgEnv()
}

func cloneCfgEnv(env *CfgEnv) *CfgEnv {
	if env == nil {
		return nil
	}
	out := &CfgEnv{
		OS:       env.OS,
		Arch:     env.Arch,
		Target:   env.Target,
		Features: map[string]bool{},
	}
	for k, v := range env.Features {
		out.Features[k] = v
	}
	return out
}

func sortedCfgFeatures(env *CfgEnv) []string {
	if env == nil {
		return nil
	}
	features := make([]string, 0, len(env.Features))
	for name, enabled := range env.Features {
		if enabled {
			features = append(features, name)
		}
	}
	sort.Strings(features)
	return features
}
