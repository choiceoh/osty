package check

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// Result is the output of checking Osty source.
type Result struct {
	// Types maps each AST expression to its inferred type.
	Types map[ast.Expr]types.Type

	// LetTypes maps each LetStmt / LetDecl node to the type of the
	// binding. For tuple or struct destructuring lets, this is the type
	// of the whole RHS.
	LetTypes map[ast.Node]types.Type

	// SymTypes maps each resolver Symbol to its declared type.
	SymTypes map[*resolve.Symbol]types.Type

	// Instantiations records the concrete type-argument list at every
	// generic call site (identified by its *ast.CallExpr). The Go
	// transpiler reads this to emit one monomorphized copy of the
	// callee per distinct argument list.
	Instantiations map[*ast.CallExpr][]types.Type

	// Diags aggregates the diagnostics produced during checking.
	Diags []*diag.Diagnostic
}

// LookupSymType returns the declared type of a resolver symbol, or nil
// when the checker never recorded one. Preferring this over direct map
// access keeps callers crash-proof if the checker's coverage advances.
func (r *Result) LookupSymType(s *resolve.Symbol) types.Type {
	if r == nil || s == nil {
		return nil
	}
	return r.SymTypes[s]
}

// LookupType returns the type assigned to an expression, or nil if the
// checker did not examine that node.
func (r *Result) LookupType(e ast.Expr) types.Type {
	if r == nil || e == nil {
		return nil
	}
	return r.Types[e]
}

// Opts bundles optional inputs to File / Package / Workspace.
type Opts struct {
	// UseSelfhost is retained for source compatibility. The bootstrapped
	// Osty checker is now the only checker implementation.
	UseSelfhost bool

	// Source is the raw source for File. Package and Workspace read
	// sources from resolve.PackageFile.
	Source []byte

	// Stdlib supplies std.* package signatures to the selfhost checker
	// when checking outside a Workspace that already has a Stdlib
	// provider attached.
	Stdlib resolve.StdlibProvider

	// Primitives and ResultMethods are retained for callers that still
	// build checker options from the stdlib registry. The selfhost
	// checker reads stdlib method surfaces from source instead.
	Primitives    map[types.PrimitiveKind]map[string]*ast.FnDecl
	ResultMethods map[string]*ast.FnDecl

	// OnDecl, if non-nil, is invoked for every top-level declaration,
	// once per compatibility phase ("collect" and "check"). The
	// selfhost checker runs as one pass, so durations are reported as 0.
	OnDecl func(decl ast.Decl, phase string, dur time.Duration)
}

// firstOpt returns the first Opts in the slice, or a zero value when
// the caller passed no options.
func firstOpt(opts []Opts) Opts {
	if len(opts) == 0 {
		return Opts{}
	}
	return opts[0]
}

// File runs self-hosted type checking for one resolved source file.
//
// The resolver's Result is consumed read-only: this package never
// mutates symbol tables or the AST. Diagnostics from this pass are
// returned in Result.Diags; they may be concatenated with the parser's
// and resolver's diagnostics before display.
func File(f *ast.File, rr *resolve.Result, opts ...Opts) *Result {
	opt := firstOpt(opts)
	result := newResult()
	if len(opt.Source) == 0 {
		result.Diags = append(result.Diags, missingSourceDiag("file"))
		return result
	}
	applySelfhostFileResult(result, f, rr, opt.Source, opt.Stdlib)
	recordSelfhostDeclPass(opt.OnDecl, f, "collect")
	recordSelfhostDeclPass(opt.OnDecl, f, "check")
	return result
}

// Package runs self-hosted type checking across every file in a
// resolver Package.
func Package(pkg *resolve.Package, pr *resolve.PackageResult, opts ...Opts) *Result {
	opt := firstOpt(opts)
	result := newResult()
	if pkg == nil || len(pkg.Files) == 0 {
		return result
	}
	if missing := firstMissingPackageSource(pkg); missing != "" {
		result.Diags = append(result.Diags, missingSourceDiag("package "+missing))
		return result
	}
	applySelfhostPackageResult(result, pkg, pr, nil, opt.Stdlib)
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		recordSelfhostDeclPass(opt.OnDecl, pf.File, "collect")
		recordSelfhostDeclPass(opt.OnDecl, pf.File, "check")
	}
	return result
}

// Workspace type-checks every package in a resolved workspace with the
// self-hosted checker. Diagnostics are attributed per-package for
// display, while the structural maps are shared so downstream phases
// can see cross-package types from the same workspace result set.
func Workspace(
	ws *resolve.Workspace,
	resolved map[string]*resolve.PackageResult,
	opts ...Opts,
) map[string]*Result {
	opt := firstOpt(opts)
	type pkgEntry struct {
		path string
		pkg  *resolve.Package
		pr   *resolve.PackageResult
	}
	var walk []pkgEntry
	if ws != nil {
		for path, pkg := range ws.Packages {
			if pkg == nil || pkg.PkgScope == nil {
				continue
			}
			pr, ok := resolved[path]
			if !ok {
				continue
			}
			walk = append(walk, pkgEntry{path: path, pkg: pkg, pr: pr})
		}
	}
	if len(walk) == 0 {
		return map[string]*Result{}
	}

	shared := newResult()
	out := make(map[string]*Result, len(walk))
	for _, e := range walk {
		out[e.path] = resultWithSharedMaps(shared)
	}
	for _, e := range walk {
		result := out[e.path]
		if isProviderStdlibPackage(ws, e.path, e.pkg) || len(e.pkg.Files) == 0 {
			continue
		}
		if missing := firstMissingPackageSource(e.pkg); missing != "" {
			result.Diags = append(result.Diags, missingSourceDiag("package "+missing))
			continue
		}
		applySelfhostPackageResult(result, e.pkg, e.pr, ws, opt.Stdlib)
	}
	for _, e := range walk {
		for _, pf := range e.pkg.Files {
			if pf == nil {
				continue
			}
			recordSelfhostDeclPass(opt.OnDecl, pf.File, "collect")
			recordSelfhostDeclPass(opt.OnDecl, pf.File, "check")
		}
	}
	return out
}

func newResult() *Result {
	return &Result{
		Types:          map[ast.Expr]types.Type{},
		LetTypes:       map[ast.Node]types.Type{},
		SymTypes:       map[*resolve.Symbol]types.Type{},
		Instantiations: map[*ast.CallExpr][]types.Type{},
	}
}

func resultWithSharedMaps(shared *Result) *Result {
	return &Result{
		Types:          shared.Types,
		LetTypes:       shared.LetTypes,
		SymTypes:       shared.SymTypes,
		Instantiations: shared.Instantiations,
	}
}

func firstMissingPackageSource(pkg *resolve.Package) string {
	if pkg == nil {
		return ""
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		if len(pf.Source) == 0 {
			if pf.Path != "" {
				return pf.Path
			}
			return "<unknown>"
		}
	}
	return ""
}

func missingSourceDiag(scope string) *diag.Diagnostic {
	pos := token.Pos{Line: 1, Column: 1, Offset: 0}
	return diag.New(diag.Error, fmt.Sprintf("self-hosted checker requires source bytes for %s checking", scope)).
		Primary(diag.Span{Start: pos, End: pos}, "").
		Note("the legacy Go checker has been removed; pass check.Opts.Source or populate resolve.PackageFile.Source").
		Build()
}

func recordSelfhostDeclPass(onDecl func(ast.Decl, string, time.Duration), file *ast.File, phase string) {
	if onDecl == nil || file == nil {
		return
	}
	for _, d := range file.Decls {
		onDecl(d, phase, 0)
	}
}

func isProviderStdlibPackage(ws *resolve.Workspace, path string, pkg *resolve.Package) bool {
	return ws != nil &&
		ws.Stdlib != nil &&
		strings.HasPrefix(path, resolve.StdPrefix) &&
		ws.Stdlib.LookupPackage(path) == pkg
}

var scalarByName = map[string]types.Type{
	"Int":     types.Int,
	"Int8":    types.Int8,
	"Int16":   types.Int16,
	"Int32":   types.Int32,
	"Int64":   types.Int64,
	"UInt8":   types.UInt8,
	"UInt16":  types.UInt16,
	"UInt32":  types.UInt32,
	"UInt64":  types.UInt64,
	"Byte":    types.Byte,
	"Float":   types.Float,
	"Float32": types.Float32,
	"Float64": types.Float64,
	"Bool":    types.Bool,
	"Char":    types.Char,
	"String":  types.String,
	"Bytes":   types.Bytes,
	"Never":   types.Never,
}

// syntheticBuiltinSym returns a process-wide Symbol that stands in for
// a prelude builtin when constructing types outside any resolver
// context. The cache guarantees identity across files.
func syntheticBuiltinSym(name string) *resolve.Symbol {
	if sym, ok := syntheticBuiltinsRead(name); ok {
		return sym
	}
	sym := &resolve.Symbol{Name: name, Kind: resolve.SymBuiltin}
	syntheticBuiltinsStore(name, sym)
	return sym
}

var (
	syntheticBuiltinsMu sync.RWMutex
	syntheticBuiltins   = map[string]*resolve.Symbol{}
)

func syntheticBuiltinsRead(name string) (*resolve.Symbol, bool) {
	syntheticBuiltinsMu.RLock()
	defer syntheticBuiltinsMu.RUnlock()
	sym, ok := syntheticBuiltins[name]
	return sym, ok
}

func syntheticBuiltinsStore(name string, sym *resolve.Symbol) {
	syntheticBuiltinsMu.Lock()
	defer syntheticBuiltinsMu.Unlock()
	if _, ok := syntheticBuiltins[name]; ok {
		return
	}
	syntheticBuiltins[name] = sym
}
