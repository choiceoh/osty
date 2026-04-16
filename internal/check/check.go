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

// Result is the output of checking one Osty file.
type Result struct {
	// Types maps each AST expression to its inferred type. Untyped
	// numeric literals that were fixed by context appear here as the
	// fixed primitive (Int32, Float, ...); literals without context keep
	// their Untyped form and the caller may default via Untyped.Default().
	Types map[ast.Expr]types.Type

	// LetTypes maps each LetStmt / LetDecl node to the type of the
	// binding (the annotation's type, or the inferred type). For tuple
	// or struct destructuring lets, this is the type of the whole RHS.
	LetTypes map[ast.Node]types.Type

	// SymTypes maps each resolver Symbol to its declared type. For
	// functions/variants the mapped Type is a *FnType constructor;
	// for types themselves it is a *Named or *Primitive.
	SymTypes map[*resolve.Symbol]types.Type

	// Descs maps struct/enum/interface/alias symbols to the collected
	// declaration shape. Consumed by the Go transpiler to emit code.
	Descs map[*resolve.Symbol]*typeDesc

	// Instantiations records the concrete type-argument list at every
	// generic call site (identified by its *ast.CallExpr). The Go
	// transpiler reads this to emit one monomorphized copy of the
	// callee per distinct argument list (§2.7.3).
	Instantiations map[*ast.CallExpr][]types.Type

	// Diags aggregates the diagnostics produced during checking.
	Diags []*diag.Diagnostic
}

// LookupSymType returns the declared type of a resolver symbol, or nil
// when the checker never recorded one (e.g. for a declaration that failed
// to parse). Preferring this over direct map access keeps callers
// crash-proof if the checker's coverage advances.
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

// Opts bundles optional inputs to File / Package / Workspace. A zero
// Opts matches the legacy no-stdlib behavior.
type Opts struct {
	// UseSelfhost makes the bootstrapped Osty checker authoritative for
	// checker diagnostics and expression/binding instantiation facts. The
	// Go side only collects declaration shapes still consumed by gen, lint,
	// and LSP features.
	UseSelfhost bool

	// Source is the raw source for File when UseSelfhost is enabled.
	// Package and Workspace read sources from resolve.PackageFile.
	Source []byte

	// Stdlib supplies std.* package signatures to the selfhost checker
	// when UseSelfhost is enabled outside a Workspace that already has
	// a Stdlib provider attached.
	Stdlib resolve.StdlibProvider

	// Primitives is the stdlib's intrinsic-method table (typically
	// obtained from stdlib.Registry.Primitives). When non-nil, methods
	// declared on `#[intrinsic_methods(...)]` placeholder structs are
	// reachable via `x.name()` calls on primitive receivers.
	Primitives map[types.PrimitiveKind]map[string]*ast.FnDecl

	// ResultMethods is the method table from std.result's canonical
	// Result<T, E> enum. When supplied, builtin Result method
	// signatures are derived from these stdlib declarations; when nil,
	// the checker keeps its bootstrap fallback signatures.
	ResultMethods map[string]*ast.FnDecl

	// OnDecl, if non-nil, is invoked for every top-level declaration
	// the checker visits, once per pass ("collect" or "check"), with
	// the wall-clock time spent in that pass. Used by `osty pipeline
	// --per-decl` to surface which declarations dominate the front-end
	// budget. The callback must be safe to call from the same goroutine
	// the checker runs on; no concurrent invocation is implied.
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

// File runs type checking for one resolved source file.
//
// The resolver's Result is consumed read-only: this package never
// mutates symbol tables or the AST. Diagnostics from this pass are
// returned in Result.Diags; they may be concatenated with the parser's
// and resolver's diagnostics before display.
//
// Passing a single Opts enables stdlib-aware checks — most importantly,
// method calls on primitive receivers consult the provided
// intrinsic-method table.
func File(f *ast.File, rr *resolve.Result, opts ...Opts) *Result {
	opt := firstOpt(opts)
	if opt.UseSelfhost && selfhostRuntimeAvailable() && len(opt.Source) > 0 {
		return selfhostFile(f, rr, opt)
	}
	c := newChecker()
	c.file = f
	c.resolved = rr
	c.onDecl = opt.OnDecl
	c.initBuiltins()
	c.indexPrimitiveMethods(opt.Primitives)
	c.resultMethods = opt.ResultMethods
	c.indexSymbolsFrom(rr)
	c.run()
	return c.result
}

func selfhostFile(f *ast.File, rr *resolve.Result, opt Opts) *Result {
	c := newChecker()
	c.file = f
	c.resolved = rr
	c.onDecl = opt.OnDecl
	c.initBuiltins()
	c.indexPrimitiveMethods(opt.Primitives)
	c.resultMethods = opt.ResultMethods
	c.indexSymbolsFrom(rr)
	for _, d := range f.Decls {
		c.timedCollect(d)
	}
	applySelfhostFileResult(c.result, f, rr, opt.Source, opt.Stdlib)
	c.recordSelfhostCheckPass(f)
	return c.result
}

// Package runs type checking across every file in a resolver Package,
// sharing one type context so partial struct/enum declarations in
// different files are checked as a single entity. The returned Result
// aggregates per-file AST → Type maps and diagnostics in file order.
//
// Pass ordering across files (important for forward references):
//
//  1. Collect — every file's top-level declarations are collected into
//     shared type descriptors before any body check begins.
//  2. Body    — every file's decl bodies are checked against the
//     complete type environment.
//  3. Stmts   — any file's top-level script statements are checked
//     last, with an implicit `fn main()` env.
func Package(pkg *resolve.Package, pr *resolve.PackageResult, opts ...Opts) *Result {
	opt := firstOpt(opts)
	if opt.UseSelfhost && selfhostRuntimeAvailable() && packageSelfhostSourceAvailable(pkg) {
		return selfhostPackage(pkg, pr, opt)
	}
	c := newChecker()
	if len(pkg.Files) == 0 {
		return c.result
	}
	// initBuiltins walks up from a FileScope to the prelude. Any file's
	// scope gets us there — all files in a package share the same
	// prelude through the package scope.
	c.resolved = fileResult(pkg.Files[0])
	c.onDecl = opt.OnDecl
	c.initBuiltins()
	c.indexPrimitiveMethods(opt.Primitives)
	c.resultMethods = opt.ResultMethods

	// Phase A: build symbol indexes from every file's Refs/TypeRefs
	// BEFORE any collect pass runs, so the checker can cross-reference
	// Decls declared elsewhere in the package.
	for _, pf := range pkg.Files {
		c.indexSymbolsFrom(fileResult(pf))
	}

	// Phase B: collect type descriptors across every file using that
	// file's Refs as the current resolver view. We swap c.resolved
	// between files so helpers like c.symbol / c.namedSymbol see the
	// right lookup table for each Decl.
	for _, pf := range pkg.Files {
		c.file = pf.File
		c.resolved = fileResult(pf)
		for _, d := range pf.File.Decls {
			c.timedCollect(d)
		}
	}

	// Phase C: check every file's declaration bodies.
	for _, pf := range pkg.Files {
		c.file = pf.File
		c.resolved = fileResult(pf)
		for _, d := range pf.File.Decls {
			c.timedCheckDecl(d)
		}
	}

	// Phase D: script top-level statements, one implicit main per file.
	for _, pf := range pkg.Files {
		if len(pf.File.Stmts) == 0 {
			continue
		}
		c.file = pf.File
		c.resolved = fileResult(pf)
		e := &env{retType: types.Unit}
		c.checkStmts(pf.File.Stmts, e)
	}
	return c.result
}

func selfhostPackage(pkg *resolve.Package, pr *resolve.PackageResult, opt Opts) *Result {
	c := newChecker()
	if len(pkg.Files) == 0 {
		return c.result
	}
	c.resolved = fileResult(pkg.Files[0])
	c.onDecl = opt.OnDecl
	c.initBuiltins()
	c.indexPrimitiveMethods(opt.Primitives)
	c.resultMethods = opt.ResultMethods
	for _, pf := range pkg.Files {
		c.indexSymbolsFrom(fileResult(pf))
	}
	for _, pf := range pkg.Files {
		c.file = pf.File
		c.resolved = fileResult(pf)
		for _, d := range pf.File.Decls {
			c.timedCollect(d)
		}
	}
	applySelfhostPackageResult(c.result, pkg, pr, nil, opt.Stdlib)
	for _, pf := range pkg.Files {
		c.recordSelfhostCheckPass(pf.File)
	}
	return c.result
}

// Workspace type-checks every package in a resolved workspace as one
// unit, sharing a single checker so cross-package type references
// (`auth.User`, `auth.login(...)`) see the same descriptors the source
// package built during the collect phase. Diagnostics are attributed
// per-package for display, but SymTypes / Descs span the workspace.
//
// Packages without bodies (stdlib stubs, cycle markers) are skipped.
// Provider-backed stdlib packages contribute declarations/signatures to
// the shared type environment, but their stub bodies are not checked as
// user code.
func Workspace(
	ws *resolve.Workspace,
	resolved map[string]*resolve.PackageResult,
	opts ...Opts,
) map[string]*Result {
	opt := firstOpt(opts)
	if opt.UseSelfhost && selfhostRuntimeAvailable() && workspaceSelfhostSourceAvailable(ws, resolved) {
		return selfhostWorkspace(ws, resolved, opt)
	}
	type pkgEntry struct {
		path string
		pkg  *resolve.Package
	}
	var walk []pkgEntry
	for path, pkg := range ws.Packages {
		if pkg == nil || pkg.PkgScope == nil {
			continue
		}
		if _, ok := resolved[path]; !ok {
			continue
		}
		walk = append(walk, pkgEntry{path: path, pkg: pkg})
	}
	if len(walk) == 0 {
		return map[string]*Result{}
	}

	c := newChecker()
	c.onDecl = opt.OnDecl
	c.indexPrimitiveMethods(opt.Primitives)
	c.resultMethods = opt.ResultMethods
	// Seed c.resolved with any file's view so initBuiltins can walk up
	// to the prelude. Every file in the workspace reaches the same
	// prelude, so this is independent of which package we pick first.
	for _, e := range walk {
		if len(e.pkg.Files) > 0 {
			c.resolved = fileResult(e.pkg.Files[0])
			break
		}
	}
	c.initBuiltins()

	// Phase A — index: build the Decl → Symbol reverse index from
	// every file's Refs/TypeRefs up front so later passes can look up
	// bindings declared anywhere in the workspace.
	for _, e := range walk {
		for _, pf := range e.pkg.Files {
			c.indexSymbolsFrom(fileResult(pf))
		}
	}
	// Phase B — collect: type descriptors for every top-level
	// declaration in every package. At the end of this loop SymTypes
	// and Descs describe the whole workspace.
	for _, e := range walk {
		for _, pf := range e.pkg.Files {
			c.file = pf.File
			c.resolved = fileResult(pf)
			for _, d := range pf.File.Decls {
				c.timedCollect(d)
			}
		}
	}
	// Phase C — body: check bodies and top-level script statements.
	// Diagnostics produced during each package's body pass are sliced
	// off the running Diags list so callers can render them against
	// that package's source.
	out := map[string]*Result{}
	for _, e := range walk {
		start := len(c.result.Diags)
		if isProviderStdlibPackage(ws, e.path, e.pkg) {
			out[e.path] = &Result{
				Types:          c.result.Types,
				LetTypes:       c.result.LetTypes,
				SymTypes:       c.result.SymTypes,
				Descs:          c.result.Descs,
				Instantiations: c.result.Instantiations,
			}
			continue
		}
		for _, pf := range e.pkg.Files {
			c.file = pf.File
			c.resolved = fileResult(pf)
			for _, d := range pf.File.Decls {
				c.timedCheckDecl(d)
			}
			if len(pf.File.Stmts) > 0 {
				env := &env{retType: types.Unit}
				c.checkStmts(pf.File.Stmts, env)
			}
		}
		pkgDiags := append([]*diag.Diagnostic(nil), c.result.Diags[start:]...)
		out[e.path] = &Result{
			Types:          c.result.Types,
			LetTypes:       c.result.LetTypes,
			SymTypes:       c.result.SymTypes,
			Descs:          c.result.Descs,
			Instantiations: c.result.Instantiations,
			Diags:          pkgDiags,
		}
	}
	return out
}

func packageSelfhostSourceAvailable(pkg *resolve.Package) bool {
	if pkg == nil {
		return false
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		if len(pf.Source) == 0 {
			return false
		}
	}
	return true
}

func workspaceSelfhostSourceAvailable(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult) bool {
	if ws == nil {
		return false
	}
	for path, pkg := range ws.Packages {
		if pkg == nil || pkg.PkgScope == nil {
			continue
		}
		if _, ok := resolved[path]; !ok {
			continue
		}
		if !packageSelfhostSourceAvailable(pkg) {
			return false
		}
	}
	return true
}

func selfhostWorkspace(
	ws *resolve.Workspace,
	resolved map[string]*resolve.PackageResult,
	opt Opts,
) map[string]*Result {
	type pkgEntry struct {
		path string
		pkg  *resolve.Package
	}
	var walk []pkgEntry
	for path, pkg := range ws.Packages {
		if pkg == nil || pkg.PkgScope == nil {
			continue
		}
		if _, ok := resolved[path]; !ok {
			continue
		}
		walk = append(walk, pkgEntry{path: path, pkg: pkg})
	}
	if len(walk) == 0 {
		return map[string]*Result{}
	}

	c := newChecker()
	c.onDecl = opt.OnDecl
	c.indexPrimitiveMethods(opt.Primitives)
	c.resultMethods = opt.ResultMethods
	for _, e := range walk {
		if len(e.pkg.Files) > 0 {
			c.resolved = fileResult(e.pkg.Files[0])
			break
		}
	}
	c.initBuiltins()
	for _, e := range walk {
		for _, pf := range e.pkg.Files {
			c.indexSymbolsFrom(fileResult(pf))
		}
	}
	for _, e := range walk {
		for _, pf := range e.pkg.Files {
			c.file = pf.File
			c.resolved = fileResult(pf)
			for _, d := range pf.File.Decls {
				c.timedCollect(d)
			}
		}
	}

	out := map[string]*Result{}
	for _, e := range walk {
		out[e.path] = &Result{
			Types:          c.result.Types,
			LetTypes:       c.result.LetTypes,
			SymTypes:       c.result.SymTypes,
			Descs:          c.result.Descs,
			Instantiations: c.result.Instantiations,
		}
	}
	applySelfhostWorkspaceResults(ws, resolved, out, opt.Stdlib)
	for _, e := range walk {
		for _, pf := range e.pkg.Files {
			c.recordSelfhostCheckPass(pf.File)
		}
	}
	return out
}

func isProviderStdlibPackage(ws *resolve.Workspace, path string, pkg *resolve.Package) bool {
	return ws != nil &&
		ws.Stdlib != nil &&
		strings.HasPrefix(path, resolve.StdPrefix) &&
		ws.Stdlib.LookupPackage(path) == pkg
}

// newChecker allocates a checker with empty shared maps; each entry
// point (File / Package) wires the appropriate file + resolver view
// before running the passes.
func newChecker() *checker {
	return &checker{
		result: &Result{
			Types:          map[ast.Expr]types.Type{},
			LetTypes:       map[ast.Node]types.Type{},
			SymTypes:       map[*resolve.Symbol]types.Type{},
			Descs:          map[*resolve.Symbol]*typeDesc{},
			Instantiations: map[*ast.CallExpr][]types.Type{},
		},
		syms:              map[*resolve.Symbol]*symInfo{},
		declToSym:         map[ast.Node]*resolve.Symbol{},
		externalCollected: map[*resolve.Package]bool{},
		externalPkgs:      map[*resolve.Symbol]*resolve.Package{},
	}
}

// fileResult builds a resolve.Result view for one file of a package so
// existing checker helpers (c.symbol, c.namedSymbol) keep using the
// per-file Refs/TypeRefs they were designed for. The FileScope the
// view carries is the file's own — its parent chain already reaches
// the package scope, so top-level lookups traverse correctly.
func fileResult(pf *resolve.PackageFile) *resolve.Result {
	return &resolve.Result{
		Refs:      pf.Refs,
		TypeRefs:  pf.TypeRefs,
		FileScope: pf.FileScope,
	}
}

// indexSymbolsFrom populates declToSym from a specific resolve.Result.
// Called once per file during multi-file checking so every local
// binding visible in ANY file is indexed before bodies are checked.
func (c *checker) indexSymbolsFrom(rr *resolve.Result) {
	add := func(sym *resolve.Symbol) {
		if sym == nil || sym.Decl == nil {
			return
		}
		// First writer wins — two references to the same binding map to
		// one Symbol, so rewrites are no-ops.
		if _, have := c.declToSym[sym.Decl]; !have {
			c.declToSym[sym.Decl] = sym
		}
	}
	for _, sym := range rr.Refs {
		add(sym)
	}
	for _, sym := range rr.TypeRefs {
		add(sym)
	}
}

// checker is the working state for one file's type check.
type checker struct {
	file     *ast.File
	resolved *resolve.Result
	result   *Result

	// onDecl mirrors Opts.OnDecl. When non-nil, the per-decl loops in
	// run() and Package() report wall-clock time per pass.
	onDecl func(decl ast.Decl, phase string, dur time.Duration)

	// syms holds per-symbol type info built during pass 1.
	syms map[*resolve.Symbol]*symInfo

	// builtinByName maps a prelude name ("Int", "String", "List", ...)
	// to its *resolve.Symbol. Populated from the prelude at init.
	builtinByName map[string]*resolve.Symbol

	// declToSym is the reverse index from AST declaration nodes (GenericParam,
	// Param, Receiver, IdentPat, StructPatField, BindingPat, Variant, …) to
	// the resolver Symbol installed for that node. Built once in indexSymbols
	// before pass 1 so O(1) lookups replace the former O(|Refs|) scans.
	declToSym map[ast.Node]*resolve.Symbol

	// primMethods maps each primitive kind to its intrinsic-method table
	// (derived from stdlib.Registry.Primitives in indexPrimitiveMethods).
	// Nil when no Opts.Primitives was supplied; in that case method
	// calls on primitive receivers fall through to the legacy
	// stdlibCallReturn escape hatch.
	primMethods map[types.PrimitiveKind]map[string]*methodDesc

	// resultMethods points at std.result's canonical Result enum
	// methods when the caller supplied a stdlib Registry through Opts.
	// Builtin Result dispatch consults this before using its bootstrap
	// fallback signatures.
	resultMethods map[string]*ast.FnDecl

	// externalCollected tracks imported packages whose declaration
	// surfaces have been collected into result.SymTypes / result.Descs.
	// Single-file checks do not otherwise run a collect pass over stdlib
	// packages, but package calls like `random.seeded()` still need the
	// callee return type and the returned struct's methods.
	externalCollected map[*resolve.Package]bool

	// externalPkgs remembers which loaded package introduced a Named
	// type discovered through a package call. Stdlib modules are
	// resolved but not collected by the user's checker pass; this lets
	// later field/method lookups walk their AST signatures.
	externalPkgs map[*resolve.Symbol]*resolve.Package
}

func (c *checker) ensurePackageCollected(pkg *resolve.Package) {
	if pkg == nil || len(pkg.Files) == 0 {
		return
	}
	if c.externalCollected[pkg] {
		return
	}
	c.externalCollected[pkg] = true

	oldFile, oldResolved := c.file, c.resolved
	for _, pf := range pkg.Files {
		c.indexSymbolsFrom(fileResult(pf))
	}
	for _, pf := range pkg.Files {
		c.file = pf.File
		c.resolved = fileResult(pf)
		for _, d := range pf.File.Decls {
			c.collect(d)
		}
	}
	c.file, c.resolved = oldFile, oldResolved
}

// indexPrimitiveMethods converts the stdlib registry's primitive method
// table into methodDescs the checker can dispatch through. Each
// primitive-method signature contains only primitive types by
// construction, so a narrow AST-to-type conversion (no resolver
// TypeRefs lookup) is sufficient here. `Self` in the signature
// resolves to the target primitive kind so `Int8.abs() -> Self` is
// correctly typed as returning Int8 (not Int).
func (c *checker) indexPrimitiveMethods(src map[types.PrimitiveKind]map[string]*ast.FnDecl) {
	if len(src) == 0 {
		return
	}
	c.primMethods = map[types.PrimitiveKind]map[string]*methodDesc{}
	for kind, methods := range src {
		inner := make(map[string]*methodDesc, len(methods))
		self := primitiveByKind(kind)
		for name, fn := range methods {
			inner[name] = primitiveMethodDesc(fn, self)
		}
		c.primMethods[kind] = inner
	}
}

// primitiveByKind returns the Primitive singleton for a PrimitiveKind
// as a types.Type — a thin wrapper around types.PrimitiveByKind that
// lets callers treat a nil "not a scalar" result as the type-system's
// Error sentinel.
func primitiveByKind(k types.PrimitiveKind) types.Type {
	p := types.PrimitiveByKind(k)
	if p == nil {
		return nil
	}
	return p
}

// primitiveMethodDesc builds a methodDesc from a stdlib FnDecl whose
// parameter and return types are all primitives. `Self` in the
// signature is bound to `selfT` so each kind's expansion sees the
// right receiver type.
func primitiveMethodDesc(fn *ast.FnDecl, selfT types.Type) *methodDesc {
	params := make([]types.Type, 0, len(fn.Params))
	for _, p := range fn.Params {
		params = append(params, primitiveTypeFromASTWithSelf(p.Type, selfT))
	}
	ret := types.Type(types.Unit)
	if fn.ReturnType != nil {
		ret = primitiveTypeFromASTWithSelf(fn.ReturnType, selfT)
	}
	return &methodDesc{
		Name:    fn.Name,
		Pub:     fn.Pub,
		Recv:    fn.Recv,
		Fn:      &types.FnType{Params: params, Return: ret},
		HasBody: fn.Body != nil,
		Params:  fn.Params,
		Decl:    fn,
	}
}

// primitiveTypeFromASTWithSelf extends primitiveTypeFromAST with Self,
// T?, Result<T, E>, List<T>, and Tuple handling so richer primitive
// method signatures (e.g. `toInt32(self) -> Result<Int32, Error>` or
// `checkedAdd(self, other: Self) -> Self?`) resolve correctly.
func primitiveTypeFromASTWithSelf(t ast.Type, selfT types.Type) types.Type {
	if t == nil {
		return types.Unit
	}
	switch n := t.(type) {
	case *ast.NamedType:
		if len(n.Path) == 1 && n.Path[0] == "Self" {
			if selfT != nil {
				return selfT
			}
			return types.ErrorType
		}
		// Result<T, E> as a builtin marker — use the prelude-free
		// representation so the checker treats it structurally.
		if len(n.Path) == 1 && n.Path[0] == "Result" && len(n.Args) == 2 {
			return &types.Named{
				Sym: syntheticBuiltinSym("Result"),
				Args: []types.Type{
					primitiveTypeFromASTWithSelf(n.Args[0], selfT),
					primitiveTypeFromASTWithSelf(n.Args[1], selfT),
				},
			}
		}
		// Scalar primitive or a builtin compound used in a primitive
		// method signature (List, Bytes, Error, Option sym).
		if len(n.Args) == 0 && len(n.Path) == 1 {
			return primitiveTypeFromAST(t)
		}
		// Fallback: builtin named with args.
		args := make([]types.Type, len(n.Args))
		for i, a := range n.Args {
			args[i] = primitiveTypeFromASTWithSelf(a, selfT)
		}
		return &types.Named{
			Sym:  syntheticBuiltinSym(n.Path[len(n.Path)-1]),
			Args: args,
		}
	case *ast.OptionalType:
		return &types.Optional{Inner: primitiveTypeFromASTWithSelf(n.Inner, selfT)}
	case *ast.TupleType:
		if len(n.Elems) == 0 {
			return types.Unit
		}
		if len(n.Elems) == 1 {
			return primitiveTypeFromASTWithSelf(n.Elems[0], selfT)
		}
		elems := make([]types.Type, len(n.Elems))
		for i, e := range n.Elems {
			elems[i] = primitiveTypeFromASTWithSelf(e, selfT)
		}
		return &types.Tuple{Elems: elems}
	case *ast.FnType:
		params := make([]types.Type, len(n.Params))
		for i, p := range n.Params {
			params[i] = primitiveTypeFromASTWithSelf(p, selfT)
		}
		ret := types.Type(types.Unit)
		if n.ReturnType != nil {
			ret = primitiveTypeFromASTWithSelf(n.ReturnType, selfT)
		}
		return &types.FnType{Params: params, Return: ret}
	}
	return types.ErrorType
}

// syntheticBuiltinSym returns a process-wide Symbol that stands in for
// a prelude builtin (e.g. "Result", "List", "Error") when we are
// constructing types outside any resolver context. The cache guarantees
// identity — two lookups of the same name produce the same *Symbol —
// so Named pointers built from primitive signatures compare equal
// across files. Every entry has Kind=SymBuiltin and no Decl.
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
	// Another goroutine may have raced to populate the same entry; keep
	// whichever symbol landed first so pointer identity remains stable.
	if existing, ok := syntheticBuiltins[name]; ok {
		_ = existing
		return
	}
	syntheticBuiltins[name] = sym
}

// primitiveTypeFromAST maps a NamedType whose head names a primitive
// (e.g. `Int`, `Float`, `Bool`) back to the type-system singleton.
// Returns ErrorType for anything else; the stub loader guarantees
// primitive-only signatures upstream, so this fallback only fires when
// the intrinsic stubs drift.
func primitiveTypeFromAST(t ast.Type) types.Type {
	nt, ok := t.(*ast.NamedType)
	if !ok || len(nt.Path) != 1 {
		return types.ErrorType
	}
	if p := types.PrimitiveByName(nt.Path[0]); p != nil {
		return p
	}
	return types.ErrorType
}

// symByDecl returns the resolver Symbol installed for a declaration
// node, or nil when the binding was never referenced (and therefore
// didn't surface in Refs/TypeRefs for the reverse index).
func (c *checker) symByDecl(n ast.Node) *resolve.Symbol {
	return c.declToSym[n]
}

// fnDeclParams returns the declared parameter list for a top-level fn
// symbol so keyword and default-arg matching can inspect it. Returns
// nil for non-fn or builtin symbols; the caller then falls through to
// positional-only checking.
func fnDeclParams(sym *resolve.Symbol) []*ast.Param {
	if sym == nil {
		return nil
	}
	if fn, ok := sym.Decl.(*ast.FnDecl); ok {
		return fn.Params
	}
	return nil
}

// ---- diagnostic helpers ----

func (c *checker) emit(d *diag.Diagnostic) {
	c.result.Diags = append(c.result.Diags, d)
}

func (c *checker) errSpan(start, end token.Pos, code, format string, args ...any) {
	c.emit(diag.New(diag.Error, fmt.Sprintf(format, args...)).
		Code(code).
		Primary(diag.Span{Start: start, End: end}, "").
		Build())
}

func (c *checker) errNode(n ast.Node, code, format string, args ...any) {
	c.errSpan(n.Pos(), n.End(), code, format, args...)
}

// errMismatch is the standard "expected X, got Y" shape. Produces a
// single-span diagnostic; callers that can point at the constraint
// source (an annotation, a parameter type, a return-type clause)
// should prefer errMismatchWithSource for a richer report.
func (c *checker) errMismatch(n ast.Node, want, got types.Type) {
	if types.IsError(want) || types.IsError(got) {
		return // cascade suppression
	}
	c.emit(diag.New(diag.Error,
		fmt.Sprintf("type mismatch: expected `%s`, got `%s`", want, got)).
		Code(diag.CodeTypeMismatch).
		Primary(diag.Span{Start: n.Pos(), End: n.End()},
			fmt.Sprintf("this has type `%s`", got)).
		Build())
}

// errMismatchWithSource augments the standard mismatch with a
// secondary span that points at the site where the expected type
// came from — a let annotation, a fn param, a return type. Mirrors
// Rust's "expected because of this" chain.
func (c *checker) errMismatchWithSource(exprN, sourceN ast.Node, want, got types.Type, sourceLabel string) {
	if types.IsError(want) || types.IsError(got) {
		return
	}
	c.emit(diag.New(diag.Error,
		fmt.Sprintf("type mismatch: expected `%s`, got `%s`", want, got)).
		Code(diag.CodeTypeMismatch).
		Primary(diag.Span{Start: exprN.Pos(), End: exprN.End()},
			fmt.Sprintf("this has type `%s`", got)).
		Secondary(diag.Span{Start: sourceN.Pos(), End: sourceN.End()},
			sourceLabel).
		Build())
}

// errFieldNotFound emits an "unknown field" diagnostic with a
// best-guess typo suggestion drawn from the struct's declared fields.
// Mirrors the resolver's E0500 typo hints for a unified UX across
// name-resolution and type-check errors.
func (c *checker) errFieldNotFound(n ast.Node, structName, got string, candidates []string) {
	b := diag.New(diag.Error,
		fmt.Sprintf("struct `%s` has no field `%s`", structName, got)).
		Code(diag.CodeUnknownField).
		Primary(diag.Span{Start: n.Pos(), End: n.End()}, "no such field")
	if s := suggestFrom(got, candidates); s != "" {
		b.Hint(fmt.Sprintf("did you mean `%s`?", s))
	}
	c.emit(b.Build())
}

// errMethodNotFound is the method-lookup analogue of errFieldNotFound.
// The candidate list should include every method name reachable on
// the receiver type (struct / enum / interface / intrinsic stdlib).
func (c *checker) errMethodNotFound(n ast.Node, recvDescription, got string, candidates []string) {
	b := diag.New(diag.Error,
		fmt.Sprintf("no method `%s` on %s", got, recvDescription)).
		Code(diag.CodeUnknownMethod).
		Primary(diag.Span{Start: n.Pos(), End: n.End()}, "no such method")
	if s := suggestFrom(got, candidates); s != "" {
		b.Hint(fmt.Sprintf("did you mean `%s`?", s))
	}
	c.emit(b.Build())
}

// ---- driver ----

func (c *checker) run() {
	// Pass 1: build per-type descriptors and per-fn signatures, in
	// source-order, so pass-2 body checking can call anything declared
	// anywhere in the file.
	for _, d := range c.file.Decls {
		c.timedCollect(d)
	}

	// Pass 2: check bodies.
	for _, d := range c.file.Decls {
		c.timedCheckDecl(d)
	}

	// Pass 3: script top-level statements, as the implicit main.
	if len(c.file.Stmts) > 0 {
		e := &env{retType: types.Unit}
		c.checkStmts(c.file.Stmts, e)
	}
}

// timedCollect / timedCheckDecl wrap the per-decl passes with a
// wall-clock timer that fires only when an OnDecl callback is wired
// (the no-callback common path stays a single function call). The
// reported phase name matches the pass: "collect" for pass 1, "check"
// for pass 2.
func (c *checker) timedCollect(d ast.Decl) {
	if c.onDecl == nil {
		c.collect(d)
		return
	}
	t0 := time.Now()
	c.collect(d)
	c.onDecl(d, "collect", time.Since(t0))
}

func (c *checker) timedCheckDecl(d ast.Decl) {
	if c.onDecl == nil {
		c.checkDecl(d)
		return
	}
	t0 := time.Now()
	c.checkDecl(d)
	c.onDecl(d, "check", time.Since(t0))
}

func (c *checker) recordSelfhostCheckPass(file *ast.File) {
	if c.onDecl == nil || file == nil {
		return
	}
	for _, d := range file.Decls {
		c.onDecl(d, "check", 0)
	}
}

// symbol returns the resolver's symbol for an Ident, or nil if the
// resolver didn't record one (undefined name).
func (c *checker) symbol(id *ast.Ident) *resolve.Symbol {
	return c.resolved.Refs[id]
}

// namedSymbol returns the resolver's symbol for a NamedType head name.
func (c *checker) namedSymbol(n *ast.NamedType) *resolve.Symbol {
	return c.resolved.TypeRefs[n]
}

// topLevelSym looks up a name the resolver installed at the top level.
// Top-level declarations now live in the package scope (the parent of
// each file's scope); this helper walks the chain so the checker can
// remain oblivious to single-vs-multi-file mode.
func (c *checker) topLevelSym(name string) *resolve.Symbol {
	return c.resolved.FileScope.Lookup(name)
}

// info fetches or lazily inserts the symInfo for a symbol.
func (c *checker) info(sym *resolve.Symbol) *symInfo {
	if sym == nil {
		return nil
	}
	if i, ok := c.syms[sym]; ok {
		return i
	}
	i := &symInfo{}
	c.syms[sym] = i
	return i
}

// setSymType installs `t` as the declared type for `sym` and mirrors it
// into the public Result.SymTypes table. nil types are ignored.
func (c *checker) setSymType(sym *resolve.Symbol, t types.Type) {
	if sym == nil || t == nil {
		return
	}
	c.info(sym).Type = t
	c.result.SymTypes[sym] = t
}

// symTypeOrError reads the recorded type for a symbol, returning the
// error sentinel when the symbol has no type info (a stage upstream
// already diagnosed the problem).
func (c *checker) symTypeOrError(sym *resolve.Symbol) types.Type {
	if sym == nil {
		return types.ErrorType
	}
	if i, ok := c.syms[sym]; ok && i.Type != nil {
		return i.Type
	}
	return types.ErrorType
}

// recordExpr stores the inferred type of an expression, if non-nil.
func (c *checker) recordExpr(e ast.Expr, t types.Type) {
	if e == nil || t == nil {
		return
	}
	c.result.Types[e] = t
}

// ---- Builtin initialization ----

// scalarByName pairs every prelude scalar type name with its Primitive
// singleton. Shared between initBuiltins (pass 1 symbol registration)
// and typeref.builtinScalarType (pass 2 type resolution).
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

// builtinCompoundNames lists prelude names that are modelled as *Named
// with the prelude Symbol as identity: generic collections + Option /
// Result / Error + marker interfaces + concurrency primitives. Their
// method shapes live in the stdlib-escape paths of expr.go.
var builtinCompoundNames = []string{
	"List", "Map", "Set", "Option", "Result",
	"Error", "Equal", "Ordered", "Hashable",
	"Chan", "Channel", "Handle", "TaskGroup",
}

// initBuiltins pre-populates symInfo for every prelude symbol. The
// prelude is the topmost scope in the chain; walk up from FileScope to
// find it, then stamp types onto the Symbols it holds.
func (c *checker) initBuiltins() {
	c.builtinByName = map[string]*resolve.Symbol{}

	root := c.resolved.FileScope
	for root.Parent() != nil {
		root = root.Parent()
	}
	for name, sym := range root.Symbols() {
		c.builtinByName[name] = sym
	}

	for name, t := range scalarByName {
		if sym := c.builtinByName[name]; sym != nil {
			c.setSymType(sym, t)
		}
	}
	for _, name := range builtinCompoundNames {
		if sym := c.builtinByName[name]; sym != nil {
			c.setSymType(sym, &types.Named{Sym: sym})
		}
	}
	for _, name := range []string{"true", "false"} {
		if sym := c.builtinByName[name]; sym != nil {
			c.setSymType(sym, types.Bool)
		}
	}
	// Option/Result variants (Some/None/Ok/Err) and prelude functions
	// (println, print, dbg, …) are handled inline in checkCall; no
	// pre-registered fn signature is needed.
}

// lookupBuiltin returns a builtin symbol by name ("List", "Option", ...).
func (c *checker) lookupBuiltin(name string) *resolve.Symbol {
	return c.builtinByName[name]
}
