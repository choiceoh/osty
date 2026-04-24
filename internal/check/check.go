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

	// InstantiationsByID records the concrete type-argument list at
	// every generic call site, keyed by the CallExpr's NodeID. The
	// backends read this to emit one monomorphized copy of the callee
	// per distinct argument list.
	InstantiationsByID map[ast.NodeID][]types.Type

	// InstantiationCalls enumerates the call sites behind
	// InstantiationsByID so callers that need to walk every
	// monomorphized call expression do not rely on pointer-keyed map
	// iteration.
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

// SelfhostFile runs type checking for one resolved source file through
// the selfhost checker boundary. Returns a *Result populated from the
// selfhost output (Types / LetTypes / SymTypes / InstantiationsByID /
// Diags — the structured maps lint + IR lowering consume).
//
// The resolver's Result is consumed read-only: this package never
// mutates symbol tables or the AST. Diagnostics from this pass are
// returned in Result.Diags; they may be concatenated with the parser's
// and resolver's diagnostics before display.
//
// Phase 1c.5 collapsed the earlier File / SelfhostFile pair into this
// single entry. The native checker is authoritative for builder-chain
// typing and diagnostics after #833 ported builder auto-derive into
// the selfhost checker + on-demand IR rewriting; the Go-side
// `DesugarBuildersInFile` prepass that File used to carry is gone.
// OnDecl timing bookkeeping moved into this function so no caller
// loses the per-decl phase dump `osty pipeline --per-decl` emits.
func SelfhostFile(f *ast.File, rr *resolve.Result, opts ...Opts) *Result {
	opt := firstOpt(opts)
	result := newResult()
	applyNativeFileResult(result, f, rr, opt.Source, opt.Stdlib, opt.Privileged)
	diag.StampFile(result.Diags, opt.Path)
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
	privileged := isPrivilegedPackage(pkg)
	applyNativePackageResult(result, pkg, pr, nil, opt.Stdlib, privileged)
	stampPackageDiags(result.Diags, pkg)
	// §19 policy gates (privilege / POD / no_alloc / intrinsic body) are
	// now sourced from the bootstrapped Osty checker
	// (toolchain/check_gates.osty::runCheckGates), which `applyNativePackageResult`
	// just consumed. Cross-side parity is pinned by
	// internal/check/gates_diff_test.go::TestGatesCrossSideParity, so the
	// duplicate Go-side runs previously stitched in here were removed to
	// keep the two emitters from drifting.
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
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
	// §19 policy gates are sourced from the Osty checker (see File()
	// comment above). TestGatesCrossSideParity guards against drift.
	for _, e := range walk {
		pkgResult := out[e.path]
		stampPackageDiags(pkgResult.Diags, e.pkg)
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
		Types:              map[ast.Expr]types.Type{},
		LetTypes:           map[ast.Node]types.Type{},
		SymTypes:           map[*resolve.Symbol]types.Type{},
		InstantiationsByID: map[ast.NodeID][]types.Type{},
	}
}

func resultWithSharedMaps(shared *Result) *Result {
	return &Result{
		Types:              shared.Types,
		LetTypes:           shared.LetTypes,
		SymTypes:           shared.SymTypes,
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

func isProviderStdlibPackage(ws *resolve.Workspace, path string, pkg *resolve.Package) bool {
	return ws != nil &&
		ws.Stdlib != nil &&
		strings.HasPrefix(path, resolve.StdPrefix) &&
		ws.Stdlib.LookupPackage(path) == pkg
}
