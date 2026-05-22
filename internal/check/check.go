package check

import (
	"fmt"
	"strings"
	"time"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/semanticdb"
	"github.com/osty/osty/internal/subproc"
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
	// entry points. Consumed by `osty check --dump-check-diags`; nil when
	// the native checker was unavailable or reported no errors.
	NativeCheckerTelemetry *NativeCheckerTelemetry

	// NativeCheckResult is the authoritative structured checker result returned
	// by the self-host/native boundary. The legacy maps above are still filled
	// for existing backends and tools, but new consumers should prefer this
	// stable selfhost-node-id structured surface over span-rematching through
	// the Go AST.
	NativeCheckResult *api.CheckResult

	// SemanticDB joins the authoritative resolve and check structured facts
	// when both phases ran through the selfhost frontend. This is the migration
	// surface for consumers that want to leave AST-keyed maps behind.
	SemanticDB *semanticdb.DB

	// ImportSurfaces holds the cross-pkg import data the checker consumed
	// for this file/package, keyed by the use-alias each surface
	// resolves to. Populated by host_boundary when the structured
	// PackageCheckInput.Imports was non-empty; nil when running on a
	// single-file source with no cross-pkg deps. The IR lowerer
	// (`internal/ir/lower.go::lowerUseDecl`) copies the alias-matching
	// surface's pub-fn signatures into `ir.UseDecl.Imports` so MIR's
	// `useDeclFnType` can recover return types for poisoned cross-pkg
	// call sites — closing the void-leak loop documented in
	// `docs/llvm-selfhost-plan-cross-pkg-link-measurement.md` §4.
	ImportSurfaces []api.PackageCheckImport

	// inspectSource is the single-file source used to preserve the legacy
	// Inspect(file, result) helper without reintroducing Go-side inference
	// replay. Package/workspace callers should use the native package inspect
	// path directly.
	inspectSource []byte
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

// NativeResult returns the authoritative structured checker result, if the
// self-host/native boundary ran successfully.
func (r *Result) NativeResult() *api.CheckResult {
	if r == nil {
		return nil
	}
	return r.NativeCheckResult
}

// NativeIndex returns stable-id lookups over the authoritative selfhost result.
// It is empty when the native checker did not run. Compatibility callers may
// keep using the legacy maps above; new downstream consumers should use this
// surface to avoid span/name rematching through the Go AST.
func (r *Result) NativeIndex() api.CheckResultIndex {
	if r == nil || r.NativeCheckResult == nil {
		return (*api.CheckResult)(nil).Index()
	}
	return r.NativeCheckResult.Index()
}

// Semantic returns the central selfhost semantic database, if the current
// checking path had enough structured frontend data to assemble one.
func (r *Result) Semantic() *semanticdb.DB {
	if r == nil {
		return nil
	}
	return r.SemanticDB
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

// SelfhostRun checks an existing selfhost FrontendRun without requiring a
// public *ast.File or resolver Result. It returns the authoritative structured
// checker result and diagnostics, but intentionally leaves the legacy
// AST-keyed maps empty because there is no public AST to overlay onto.
//
// Use this for new front-end consumers that can stay on stable selfhost node
// IDs. Keep SelfhostFile for compatibility paths that still need Types /
// LetTypes / SymTypes keyed by Go AST and resolver symbols.
//
// Routes through NativePackageCheck so the subprocess checker sees the same
// single-file check that `osty check FILE` does. The subprocess re-parses
// opt.Source — cheap for a single file. Parse errors short-circuit before the
// subprocess call because `selfhost.CheckPackageStructured` rejects sources
// whose parse arena carries any error.
func SelfhostRun(run *selfhost.FrontendRun, opts ...Opts) *Result {
	opt := firstOpt(opts)
	result := newResult()
	if run == nil {
		return result
	}
	src := append([]byte(nil), opt.Source...)
	result.inspectSource = append(result.inspectSource[:0], src...)
	parseDiags := run.Diagnostics()
	result.Diags = append(result.Diags, parseDiags...)
	if diagsHaveError(parseDiags) {
		diag.StampFile(result.Diags, opt.Path)
		return result
	}
	checked, err := NativePackageCheck(api.PackageCheckInput{
		Files: []api.PackageCheckFile{{Source: src, Path: opt.Path}},
	})
	if err != nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			subproc.FailureNotes("the Osty-native checker executable failed", err)...,
		))
		return result
	}
	policy := nativeDiagPolicy{privileged: opt.Privileged}
	result.Diags = append(result.Diags, nativeCheckerDiags(src, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	result.SemanticDB = semanticdb.FromCheck(checked)
	diag.StampFile(result.Diags, opt.Path)
	return result
}

func diagsHaveError(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
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
// The native checker is authoritative for builder-chain typing and
// diagnostics after #833 ported builder auto-derive into the selfhost
// checker + on-demand IR rewriting; the Go-side `DesugarBuildersInFile`
// prepass has been removed. OnDecl timing bookkeeping lives here so
// `osty pipeline --per-decl` keeps its per-decl phase dump.
func SelfhostFile(f *ast.File, rr *resolve.Result, opts ...Opts) *Result {
	opt := firstOpt(opts)
	result := newResult()
	applySelfhostFileResult(result, f, rr, opt.Source, opt.Stdlib, opt.Privileged)
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
	applySelfhostPackageResult(result, pkg, pr, nil, opt.Stdlib, privileged)
	stampPackageDiags(result.Diags, pkg)
	// §19 policy gates (privilege / POD / no_alloc / intrinsic body) are
	// now sourced from the bootstrapped Osty checker
	// (toolchain/check_gates.osty::runCheckGates), which `applySelfhostPackageResult`
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
	applySelfhostWorkspaceResults(ws, resolved, out, opt.Stdlib)
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

// PackageGraph returns type-check results for every package in a first-class
// compile-target graph. Workspace-backed graphs keep using the workspace
// adapter while standalone graph packages fall back to per-package checks.
func PackageGraph(
	graph *resolve.PackageGraph,
	resolved map[string]*resolve.PackageResult,
	opts ...Opts,
) map[string]*Result {
	if graph == nil {
		return map[string]*Result{}
	}
	if ws := graph.Workspace(); ws != nil {
		return Workspace(ws, resolved, opts...)
	}
	opt := firstOpt(opts)
	paths := graph.PackagePaths()
	if len(paths) == 0 {
		return map[string]*Result{}
	}
	shared := newResult()
	out := make(map[string]*Result, len(paths))
	for _, path := range paths {
		pkg := graph.Package(path)
		if pkg == nil || pkg.PkgScope == nil {
			continue
		}
		pr := resolved[path]
		result := resultWithSharedMaps(shared)
		out[path] = result
		applySelfhostPackageResult(result, pkg, pr, nil, opt.Stdlib, isPrivilegedPackage(pkg))
		stampPackageDiags(result.Diags, pkg)
		for _, pf := range pkg.Files {
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
