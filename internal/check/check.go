package check

import (
	"fmt"
	"strings"
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
	// backend emission reads this to emit one monomorphized copy of the
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
	// UseSelfhost is retained for source compatibility. Native checker
	// selection now happens inside the host boundary instead.
	UseSelfhost bool

	// UseGolegacy is retained for callers that already switched names.
	// It is currently ignored as well.
	UseGolegacy bool

	// Source is the raw source for File. Package and Workspace read
	// sources from resolve.PackageFile.
	Source []byte

	// Stdlib supplies std.* package signatures to the legacy checker
	// when checking outside a Workspace that already has a Stdlib
	// provider attached.
	Stdlib resolve.StdlibProvider

	// Primitives and ResultMethods are retained for callers that still
	// build checker options from the stdlib registry. The legacy
	// checker reads stdlib method surfaces from source instead.
	Primitives    map[types.PrimitiveKind]map[string]*ast.FnDecl
	ResultMethods map[string]*ast.FnDecl

	// OnDecl, if non-nil, is invoked for every top-level declaration,
	// once per compatibility phase ("collect" and "check"). The
	// checker runs as one pass, so durations are reported as 0.
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

// File runs type checking for one resolved source file when a native
// checker boundary is available. Otherwise it returns an
// unavailability diagnostic.
//
// The resolver's Result is consumed read-only: this package never
// mutates symbol tables or the AST. Diagnostics from this pass are
// returned in Result.Diags; they may be concatenated with the parser's
// and resolver's diagnostics before display.
func File(f *ast.File, rr *resolve.Result, opts ...Opts) *Result {
	opt := firstOpt(opts)
	result := newResult()
	applyNativeFileResult(result, f, rr, opt.Source, opt.Stdlib)
	if d := runNoAllocChecks(f, rr); len(d) > 0 {
		result.Diags = append(result.Diags, d...)
	}
	recordSelfhostDeclPass(opt.OnDecl, f, "collect")
	recordSelfhostDeclPass(opt.OnDecl, f, "check")
	return result
}

// Package runs type checking across every file in a resolver Package
// when a native checker boundary is available. Otherwise it returns an
// unavailability diagnostic.
func Package(pkg *resolve.Package, pr *resolve.PackageResult, opts ...Opts) *Result {
	opt := firstOpt(opts)
	result := newResult()
	if pkg == nil || len(pkg.Files) == 0 {
		return result
	}
	applyNativePackageResult(result, pkg, pr, nil, opt.Stdlib)
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if d := runNoAllocChecks(pf.File, nil); len(d) > 0 {
			result.Diags = append(result.Diags, d...)
		}
		recordSelfhostDeclPass(opt.OnDecl, pf.File, "collect")
		recordSelfhostDeclPass(opt.OnDecl, pf.File, "check")
	}
	return result
}

// Workspace returns type-check results for every package in a resolved
// workspace. Structural maps remain shared so downstream phases can
// keep traversing a stable Result shape even when the native checker
// is unavailable for one or more packages.
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
	applyNativeWorkspaceResults(ws, resolved, out, opt.Stdlib)
	for _, e := range walk {
		pkgResult := out[e.path]
		for _, pf := range e.pkg.Files {
			if pf == nil {
				continue
			}
			if d := runNoAllocChecks(pf.File, nil); len(d) > 0 {
				pkgResult.Diags = append(pkgResult.Diags, d...)
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

func checkerUnavailableDiag(scope string, notes ...string) *diag.Diagnostic {
	pos := token.Pos{Line: 1, Column: 1, Offset: 0}
	b := diag.New(diag.Error, fmt.Sprintf("type checking unavailable for %s", scope)).
		Primary(diag.Span{Start: pos, End: pos}, "")
	for _, note := range notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		b.Note(note)
	}
	return b.Build()
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
