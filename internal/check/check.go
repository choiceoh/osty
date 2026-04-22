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

	// InstantiationsByID mirrors Instantiations keyed by ast.NodeID.
	// Populated in parallel with Instantiations so callers can migrate
	// off *ast.CallExpr pointer identity. Self-host ports key on this.
	InstantiationsByID map[ast.NodeID][]types.Type

	// InstantiationCalls enumerates the call sites behind
	// InstantiationsByID, for callers that need to walk all
	// monomorphized call expressions without relying on pointer-keyed
	// map iteration.
	InstantiationCalls []*ast.CallExpr

	// Diags aggregates the diagnostics produced during checking.
	Diags []*diag.Diagnostic

	// NativeCheckerTelemetry carries the per-context error histogram the
	// bootstrapped native checker produced alongside the aggregate summary
	// diagnostic. Populated by host_boundary on the File / Package / Workspace
	// entry points. Consumed by `osty check --dump-native-diags`; nil when
	// the native checker was unavailable or reported no errors.
	NativeCheckerTelemetry *NativeCheckerTelemetry
}

// NativeCheckerTelemetry bundles the counters the bootstrapped native checker
// surfaces to host tooling.
type NativeCheckerTelemetry struct {
	Assignments     int
	Accepted        int
	Errors          int
	ErrorsByContext map[string]int
	// ErrorDetails optionally maps a context key from ErrorsByContext to a
	// finer breakdown of rendered diagnostic messages under that code.
	// Populated by selfhostDiagnosticTelemetry in the selfhost adapter;
	// each message is suffixed with `@Lnn:Cnn` when the native checker
	// surfaced a resolvable span, so identical messages at different
	// source positions remain distinguishable.
	ErrorDetails map[string]map[string]int
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

	// Path is the filesystem path of the file being checked. Used to
	// stamp d.File on every diagnostic so multi-file callers can route
	// each diagnostic to its owning source snippet. Empty when the
	// caller is working with an in-memory buffer without a filesystem
	// path (the diagnostics simply stay unstamped).
	Path string

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

	// Privileged marks the file/package as privileged for the runtime
	// sublanguage (LANG_SPEC §19.2). When false (the default), the
	// privilege gate in privilege.go rejects `#[intrinsic]`, `#[c_abi]`,
	// `#[export]`, `#[no_alloc]`, `use std.runtime.*`, and references
	// to `RawPtr` / `Pod` with E0770.
	//
	// The Package / Workspace entry points compute this from the
	// package path (`std.runtime.*` implies privileged). Callers of
	// File() must supply the flag explicitly; the default false is
	// correct for ordinary user code.
	Privileged bool
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
	// opt.Path is the caller-supplied file path, when known. The File
	// entry point is used from multi-file walkers that do not always
	// bundle a resolve.PackageFile, so we pass the path through opts.
	path := opt.Path
	if d := DesugarBuildersInFile(f, rr); len(d) > 0 {
		diag.StampFile(d, path)
		result.Diags = append(result.Diags, d...)
	}
	applyNativeFileResult(result, f, rr, opt.Source, opt.Stdlib, opt.Privileged)
	diag.StampFile(result.Diags, path)
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
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if d := DesugarBuildersInFile(pf.File, perFileResolveResult(pf)); len(d) > 0 {
			diag.StampFile(d, pf.Path)
			result.Diags = append(result.Diags, d...)
		}
	}
	privileged := isPrivilegedPackage(pkg)
	applyNativePackageResult(result, pkg, pr, nil, opt.Stdlib, privileged)
	stampPackageDiags(result.Diags, pkg)
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if d := runPrivilegeGate(pf.File, privileged); len(d) > 0 {
			diag.StampFile(d, pf.Path)
			result.Diags = appendMissingDiagnostics(result.Diags, d)
		}
		if d := runPodShapeChecks(pf.File); len(d) > 0 {
			diag.StampFile(d, pf.Path)
			result.Diags = appendMissingDiagnostics(result.Diags, d)
		}
		if d := runNoAllocChecks(pf.File, nil); len(d) > 0 {
			diag.StampFile(d, pf.Path)
			result.Diags = appendMissingDiagnostics(result.Diags, d)
		}
		if d := runIntrinsicBodyChecks(pf.File); len(d) > 0 {
			diag.StampFile(d, pf.Path)
			result.Diags = appendMissingDiagnostics(result.Diags, d)
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
		for _, pf := range e.pkg.Files {
			if pf == nil {
				continue
			}
			if d := DesugarBuildersInFile(pf.File, perFileResolveResult(pf)); len(d) > 0 {
				diag.StampFile(d, pf.Path)
				out[e.path].Diags = append(out[e.path].Diags, d...)
			}
		}
	}
	applyNativeWorkspaceResults(ws, resolved, out, opt.Stdlib)
	for _, e := range walk {
		pkgResult := out[e.path]
		stampPackageDiags(pkgResult.Diags, e.pkg)
		for _, pf := range e.pkg.Files {
			if pf == nil {
				continue
			}
			privileged := isPrivilegedPackagePath(e.path) || isPrivilegedPackage(e.pkg)
			if d := runPrivilegeGate(pf.File, privileged); len(d) > 0 {
				diag.StampFile(d, pf.Path)
				pkgResult.Diags = appendMissingDiagnostics(pkgResult.Diags, d)
			}
			if d := runPodShapeChecks(pf.File); len(d) > 0 {
				diag.StampFile(d, pf.Path)
				pkgResult.Diags = appendMissingDiagnostics(pkgResult.Diags, d)
			}
			if d := runNoAllocChecks(pf.File, nil); len(d) > 0 {
				diag.StampFile(d, pf.Path)
				pkgResult.Diags = appendMissingDiagnostics(pkgResult.Diags, d)
			}
			if d := runIntrinsicBodyChecks(pf.File); len(d) > 0 {
				diag.StampFile(d, pf.Path)
				pkgResult.Diags = appendMissingDiagnostics(pkgResult.Diags, d)
			}
			recordSelfhostDeclPass(opt.OnDecl, pf.File, "collect")
			recordSelfhostDeclPass(opt.OnDecl, pf.File, "check")
		}
	}
	return out
}

func newResult() *Result {
	return &Result{
		Types:              map[ast.Expr]types.Type{},
		LetTypes:           map[ast.Node]types.Type{},
		SymTypes:           map[*resolve.Symbol]types.Type{},
		Instantiations:     map[*ast.CallExpr][]types.Type{},
		InstantiationsByID: map[ast.NodeID][]types.Type{},
	}
}

func resultWithSharedMaps(shared *Result) *Result {
	return &Result{
		Types:              shared.Types,
		LetTypes:           shared.LetTypes,
		SymTypes:           shared.SymTypes,
		Instantiations:     shared.Instantiations,
		InstantiationsByID: shared.InstantiationsByID,
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

// stampPackageDiags back-fills d.File for any un-stamped diagnostic in ds,
// using the package's per-file source bytes to disambiguate. The native
// checker bridge returns diagnostics without a file path when several
// files share an offset range; this helper routes each to the file whose
// bytes at the primary position actually contain the identifier named
// in the diagnostic message.
//
// Diagnostics that are already stamped are left alone. Diagnostics with
// no positional information, no backticked identifier in the message,
// or ambiguous matches are skipped — the CLI's pickFile still has the
// final legacy fallback for those.
func stampPackageDiags(ds []*diag.Diagnostic, pkg *resolve.Package) {
	if pkg == nil || len(pkg.Files) == 0 {
		return
	}
	for _, d := range ds {
		if d == nil || d.File != "" {
			continue
		}
		pos := d.PrimaryPos()
		if pos.Line == 0 {
			continue
		}
		name := firstBacktickedIdent(d.Message)
		if name == "" {
			continue
		}
		match := ""
		for _, pf := range pkg.Files {
			if pf == nil {
				continue
			}
			if offsetMatchesName(pf.Source, pos.Offset, name) {
				if match != "" {
					match = ""
					break
				}
				match = pf.Path
			}
		}
		if match != "" {
			d.File = match
		}
	}
}

// firstBacktickedIdent returns the first `identifier` substring of msg,
// or "" when none is present.
func firstBacktickedIdent(msg string) string {
	start := strings.IndexByte(msg, '`')
	if start < 0 {
		return ""
	}
	rest := msg[start+1:]
	end := strings.IndexByte(rest, '`')
	if end <= 0 {
		return ""
	}
	return rest[:end]
}

// offsetMatchesName reports whether src at offset contains name.
func offsetMatchesName(src []byte, offset int, name string) bool {
	if offset < 0 || offset >= len(src) {
		return false
	}
	if offset+len(name) > len(src) {
		return false
	}
	return string(src[offset:offset+len(name)]) == name
}

func appendMissingDiagnostics(dst, extras []*diag.Diagnostic) []*diag.Diagnostic {
	for _, extra := range extras {
		if extra == nil || hasEquivalentDiagnostic(dst, extra) {
			continue
		}
		dst = append(dst, extra)
	}
	return dst
}

func hasEquivalentDiagnostic(ds []*diag.Diagnostic, want *diag.Diagnostic) bool {
	for _, got := range ds {
		if sameDiagnostic(got, want) {
			return true
		}
	}
	return false
}

func sameDiagnostic(a, b *diag.Diagnostic) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Severity != b.Severity || a.Code != b.Code || a.Message != b.Message || a.File != b.File {
		return false
	}
	aPos := a.PrimaryPos()
	bPos := b.PrimaryPos()
	if aPos != bPos {
		return false
	}
	aEnd := diagnosticPrimaryEnd(a)
	bEnd := diagnosticPrimaryEnd(b)
	return aEnd == bEnd
}

func diagnosticPrimaryEnd(d *diag.Diagnostic) token.Pos {
	if d == nil {
		return token.Pos{}
	}
	for _, s := range d.Spans {
		if s.Primary {
			return s.Span.End
		}
	}
	if len(d.Spans) > 0 {
		return d.Spans[0].Span.End
	}
	return token.Pos{}
}

// perFileResolveResult builds the minimal resolve.Result shape the
// builder desugarer consumes (Refs for ident→symbol lookups) from a
// single resolved PackageFile. Returns nil if the file has no refs
// map yet, which leaves the desugarer in local-file-only mode.
func perFileResolveResult(pf *resolve.PackageFile) *resolve.Result {
	if pf == nil || pf.Refs == nil {
		return nil
	}
	return &resolve.Result{
		Refs:          pf.Refs,
		TypeRefs:      pf.TypeRefs,
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
	}
}

func isProviderStdlibPackage(ws *resolve.Workspace, path string, pkg *resolve.Package) bool {
	return ws != nil &&
		ws.Stdlib != nil &&
		strings.HasPrefix(path, resolve.StdPrefix) &&
		ws.Stdlib.LookupPackage(path) == pkg
}
