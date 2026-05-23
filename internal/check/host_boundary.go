package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/semanticdb"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/spanid"
	"github.com/osty/osty/internal/subproc"
	"github.com/osty/osty/internal/toolchain"
)

const nativeCheckerEnv = "OSTY_NATIVE_CHECKER_BIN"

// The checker targets an Osty-native request/response boundary. Production
// installs a subprocess implementation via UseManagedSubprocessChecker;
// OSTY_NATIVE_CHECKER_BIN selects an explicit executable before that hook runs.
//
// The result and request types are api.CheckResult / api.CheckRequest so the
// in-process embedded path and the subprocess exec path speak identical
// shapes with no adapter layer in between.
type nativeChecker interface {
	CheckSourceStructured([]byte) (api.CheckResult, error)
}

type nativePackageChecker interface {
	CheckPackageStructured(api.PackageCheckInput) (api.CheckResult, error)
}

type nativeCheckerExec struct {
	path string
}

func (e nativeCheckerExec) CheckSourceStructured(src []byte) (api.CheckResult, error) {
	return e.run(api.CheckRequest{Source: string(src)})
}

func (e nativeCheckerExec) CheckPackageStructured(input api.PackageCheckInput) (api.CheckResult, error) {
	return e.run(api.CheckRequest{Package: &input})
}

func (e nativeCheckerExec) run(req api.CheckRequest) (api.CheckResult, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return api.CheckResult{}, fmt.Errorf("marshal native checker request: %w", err)
	}
	stdout, stderr, runErr := subproc.Run(e.path, payload)
	if runErr != nil {
		return api.CheckResult{}, runErr
	}
	var checked api.CheckResult
	if err := json.Unmarshal(stdout, &checked); err != nil {
		return api.CheckResult{}, subproc.WrapResponseError(e.path, fmt.Errorf("decode native checker response: %w", err), stdout, stderr)
	}
	checked.EnsureStableIDs()
	return checked, nil
}

// productionNativeCheckerFactory is the single production-path selector.
// Default is "no checker installed" — callers must install one explicitly
// via UseManagedSubprocessChecker (CLI startup) or
// InstallSubprocessCheckerForPackageTests / UseSubprocessCheckerForTest
// (test helpers in testsupport.go).
//
// Pre-gate (b-hard) this returned an embedded in-process checker as a
// silent default. That default has been removed because:
//   - The embedded checker is a frozen seed (internal/selfhost/generated.go)
//     that lags every change to toolchain/*.osty.
//   - Production code paths now route exclusively through the managed
//     subprocess; the embedded fallback was hiding setup bugs rather than
//     providing useful resilience.
//   - All in-tree test packages that exercise the factory install the
//     subprocess via TestMain (see SUBPROCESS_SWITCHOVER.md gate (b-hard)
//     migration history).
var productionNativeCheckerFactory = func() (nativeChecker, string) {
	return nil, "no native checker installed — call check.UseManagedSubprocessChecker (production) or check.InstallSubprocessCheckerForPackageTests / UseSubprocessCheckerForTest (tests)"
}

// UseManagedSubprocessChecker installs the managed subprocess checker as the
// production default for this process. Call once from CLI startup with the
// workspace start path; subsequent check invocations will route through
// `toolchain.EnsureNativeChecker(start)` on the first call (which builds the
// managed binary on demand) and reuse the cached path thereafter.
//
// Gate (b) policy: on managed-build failure the selector returns
// `(nil, "managed native checker unavailable: <reason>")`. Callers surface
// that note through `checkerUnavailableDiag` / `NativePackageCheck` error
// paths; the production CLI no longer silently degrades to the frozen
// in-process checker, because the embedded path can only ever lag behind
// the live `toolchain/*.osty` sources and a silent fallback hides real
// configuration/build problems.
//
// Gate (b-llvm) policy (this PR): the managed binary is now the
// LLVM-built `cmd/osty-native-checker/` artifact, not the Go-built
// `cmd/osty-native-checker/main.go` shell. `toolchain.EnsureNativeChecker`
// requires a resolvable `osty-self` up front (via `selfhostcache.ResolveBinary`,
// which accepts `OSTY_SELF_BIN`, in-tree `toolchain/.osty/out/{debug,release}/llvm/osty-self`
// builds, or the `osty install-self` content-addressed cache) and drives
// `osty build --backend llvm cmd/osty-native-checker/` to produce the binary.
// The Go-built shell survives only for test helpers in `testsupport.go` and
// the `OSTY_NATIVE_CHECKER_BIN` override path; production checker capability
// is now sourced from live `toolchain/*.osty` instead of
// `internal/selfhost/generated.go`.
//
// Test packages that exercise the factory should install a subprocess checker
// from TestMain (see internal/check/main_test.go and testsupport.go). The
// managed LLVM build is intentionally not triggered from every `go test`
// package — that would add seconds and clutter `.osty/` trees.
func UseManagedSubprocessChecker(start string) {
	productionNativeCheckerFactory = func() (nativeChecker, string) {
		path, err := toolchain.EnsureNativeChecker(start)
		if err != nil {
			return nil, fmt.Sprintf("managed native checker unavailable: %v", err)
		}
		return nativeCheckerExec{path: path}, ""
	}
}

var nativeCheckerFactory = defaultNativeChecker

// NativePackageCheck routes a structured package-check request through the
// configured native checker (explicit OSTY_NATIVE_CHECKER_BIN, else the
// factory from UseManagedSubprocessChecker in production, else nil with a
// note). Returns the raw api.CheckResult directly, skipping the
// *Result wrapper that Package() builds for diagnostic-rendering callers.
//
// Use from CLI code paths that previously called
// selfhost.CheckPackageStructured(input) so `osty check` / `osty lint` /
// `osty typecheck` see the same checker as `osty build` / `run` / `test`.
//
// If the factory yields a nil runner (typically because
// OSTY_NATIVE_CHECKER_BIN points at a missing binary), the note is
// propagated through the returned error so callers preserve the existing
// "checker unavailable" surface.
func NativePackageCheck(input api.PackageCheckInput) (api.CheckResult, error) {
	runner, note := nativeCheckerFactory()
	if runner == nil {
		if note == "" {
			note = "no Osty-native checker executable is configured"
		}
		return api.CheckResult{}, errors.New(note)
	}
	if pkg, ok := runner.(nativePackageChecker); ok {
		return pkg.CheckPackageStructured(input)
	}
	return api.CheckResult{}, errors.New("configured native checker does not implement package input")
}

func defaultNativeChecker() (nativeChecker, string) {
	path := strings.TrimSpace(os.Getenv(nativeCheckerEnv))
	if path != "" {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return nil, fmt.Sprintf("%s=%q was not found", nativeCheckerEnv, path)
		}
		return nativeCheckerExec{path: resolved}, ""
	}
	return productionNativeCheckerFactory()
}

type selfhostCheckedSource struct {
	source []byte
	files  []selfhostFileSegment
}

type selfhostFileSegment struct {
	file      *ast.File
	path      string
	source    []byte
	sourceID  spanid.SourceFileID
	scope     *resolve.Scope
	refs      map[ast.NodeID]*resolve.Symbol
	base      int
	sourceMap *sourcemap.Map
	// nativeNodeIDs marks segments where public AST IDs can be translated to
	// native checker NodeIDs as nativeNodeIDBase + publicID - 2.
	nativeNodeIDs    bool
	nativeNodeIDBase int
}

func applySelfhostFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider, privileged bool) {
	if result == nil {
		return
	}
	result.inspectSource = append(result.inspectSource[:0], src...)
	if len(src) == 0 {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			"source bytes were not supplied to the native checker boundary",
		))
		return
	}
	runner, note := nativeCheckerFactory()
	if runner == nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			"no Osty-native checker executable is configured",
			note,
		))
		return
	}
	var (
		checked    api.CheckResult
		checkedSrc selfhostCheckedSource
		err        error
	)
	var fileInput api.PackageCheckInput
	switch r := runner.(type) {
	case nativePackageChecker:
		// File-mode callers already hold the parsed public AST, so prefer the
		// structured package request: it reuses the package checker's direct
		// public-AST -> selfhost-AstArena lowering and avoids routing single-file
		// checks back through the astbridge/public-AST adapter path.
		checkedSrc = selfhostFileStructuredSource(file, rr, src)
		maybeDumpNativeCheckerSource(checkedSrc.source)
		fileInput = selfhostSingleFileCheckInput(file, src, stdlib)
		checked, err = r.CheckPackageStructured(fileInput)
	default:
		checkedSrc = selfhostFileSource(file, rr, src, stdlib)
		maybeDumpNativeCheckerSource(checkedSrc.source)
		checked, err = runner.CheckSourceStructured(checkedSrc.source)
	}
	if err != nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			subproc.FailureNotes("the Osty-native checker executable failed", err)...,
		))
		return
	}
	policy := nativeDiagPolicy{privileged: privileged}
	result.Diags = append(result.Diags, nativeCheckerDiagsForCheckedSource(checkedSrc, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	if len(fileInput.Imports) > 0 {
		result.ImportSurfaces = append(result.ImportSurfaces, fileInput.Imports...)
	}
	result.SemanticDB = semanticdb.FromCheck(checked)
}

// selfhostPackageOutcome bundles the per-package native checker invocation
// result. Both success and unavailability paths populate diags; success
// additionally populates src / telemetry / checked and sets ran=true so
// callers can fold the data back into a Result under whatever locking
// discipline they need.
type selfhostPackageOutcome struct {
	diags     []*diag.Diagnostic
	src       selfhostCheckedSource
	telemetry *NativeCheckerTelemetry
	checked   api.CheckResult
	// imports caches the `PackageCheckInput.Imports` slice the native
	// checker consumed, so `foldSelfhostPackageOutcome` can stash it on
	// `Result.ImportSurfaces` for downstream IR lowering (Task B —
	// cross-pkg fn signature propagation).
	imports []api.PackageCheckImport
	ran     bool
}

// runSelfhostPackageCheck routes a package through the configured native
// checker and returns the bundled outcome. No Result is mutated here so
// the function is safe to call from multiple goroutines; serial and
// worker-pool callers share this single source-of-truth and only differ
// in how they fold the outcome back.
func runSelfhostPackageCheck(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool) selfhostPackageOutcome {
	runner, note := nativeCheckerFactory()
	if runner == nil {
		return selfhostPackageOutcome{
			diags: []*diag.Diagnostic{checkerUnavailableDiag(
				"package",
				"no Osty-native checker executable is configured",
				note,
			)},
		}
	}
	src := selfhostPackageSource(pkg, ws, stdlib)
	maybeDumpNativeCheckerSource(src.source)
	if len(src.source) == 0 {
		return selfhostPackageOutcome{
			diags: []*diag.Diagnostic{checkerUnavailableDiag(
				"package",
				"package source bytes were not available to the native checker boundary",
			)},
		}
	}
	input := selfhostPackageCheckInput(pkg, ws, stdlib, src)
	var (
		checked api.CheckResult
		err     error
	)
	switch r := runner.(type) {
	case nativePackageChecker:
		checked, err = r.CheckPackageStructured(input)
	default:
		checked, err = runner.CheckSourceStructured(src.source)
	}
	if err != nil {
		return selfhostPackageOutcome{
			diags: []*diag.Diagnostic{checkerUnavailableDiag(
				"package",
				subproc.FailureNotes("the Osty-native checker executable failed", err)...,
			)},
		}
	}
	policy := nativeDiagPolicy{privileged: privileged}
	return selfhostPackageOutcome{
		diags:     nativeCheckerDiagsForCheckedSource(src, checked, policy),
		src:       src,
		telemetry: nativeCheckerTelemetry(checked, policy),
		checked:   checked,
		imports:   input.Imports,
		ran:       true,
	}
}

// foldSelfhostPackageOutcome writes outcome into result. When mu is non-nil
// the writes are serialized — used by the workspace parallel worker pool
// where multiple goroutines fold into shared Result-graph fields. For
// single-threaded callers pass nil for mu.
func foldSelfhostPackageOutcome(result *Result, pr *resolve.PackageResult, outcome selfhostPackageOutcome, mu *sync.Mutex) {
	if result == nil {
		return
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	result.Diags = append(result.Diags, outcome.diags...)
	if !outcome.ran {
		return
	}
	result.NativeCheckerTelemetry = outcome.telemetry
	result.NativeCheckResult = cloneNativeCheckResult(outcome.checked)
	if len(outcome.imports) > 0 {
		result.ImportSurfaces = append(result.ImportSurfaces, outcome.imports...)
	}
	attachSemanticDB(result, pr, outcome.checked)
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool) {
	if result == nil || pkg == nil {
		return
	}
	foldSelfhostPackageOutcome(result, pr, runSelfhostPackageCheck(pkg, ws, stdlib, privileged), nil)
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
	if ws == nil {
		return
	}
	// Per-package native checker calls are the slowest stage of
	// `osty check` / `osty build` on multi-package workspaces: each
	// CheckPackageStructured round-trip forks the configured subprocess
	// checker (managed LLVM binary or test-installed shell), typically
	// O(tens of ms) per package and trivially CPU-independent across
	// packages since the input is a read-only view of the resolved
	// workspace. Run them in parallel across a bounded worker pool
	// and fold each result back into the shared type maps under a
	// single mutex — the overlay writes are cheap next to the
	// checker itself, so contention is not a bottleneck.
	//
	// Opt-out via OSTY_CHECK_PARALLEL=0 for debugging ordering-
	// dependent bugs. Cap at GOMAXPROCS so we don't oversubscribe
	// when the checker is in-process (rare); subprocess workers scale down gracefully via
	// the OS scheduler.
	type pkgJob struct {
		path       string
		pkg        *resolve.Package
		pr         *resolve.PackageResult
		result     *Result
		privileged bool
	}
	var jobs []pkgJob
	for path, result := range results {
		pkg := ws.Packages[path]
		if isProviderStdlibPackage(ws, path, pkg) {
			continue
		}
		privileged := isPrivilegedPackagePath(path) || isPrivilegedPackage(pkg)
		jobs = append(jobs, pkgJob{path: path, pkg: pkg, pr: resolved[path], result: result, privileged: privileged})
	}
	if len(jobs) == 0 {
		return
	}
	if len(jobs) == 1 || os.Getenv("OSTY_CHECK_PARALLEL") == "0" {
		for _, j := range jobs {
			applySelfhostPackageResult(j.result, j.pkg, j.pr, ws, stdlib, j.privileged)
		}
		return
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	ch := make(chan pkgJob)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				if j.result == nil || j.pkg == nil {
					continue
				}
				outcome := runSelfhostPackageCheck(j.pkg, ws, stdlib, j.privileged)
				foldSelfhostPackageOutcome(j.result, j.pr, outcome, &mu)
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
}

// maybeDumpNativeCheckerSource writes src to the path named by the
// OSTY_NATIVE_CHECKER_SOURCE_DUMP environment variable. No-op when the
// variable is empty or write fails — strictly a debug aid for inspecting
// the bytes handed to the bootstrapped checker.
func maybeDumpNativeCheckerSource(src []byte) {
	if dump := os.Getenv("OSTY_NATIVE_CHECKER_SOURCE_DUMP"); dump != "" {
		_ = os.WriteFile(dump, src, 0o644)
	}
}

func attachSemanticDB(result *Result, pr *resolve.PackageResult, checked api.CheckResult) {
	if result == nil {
		return
	}
	if pr != nil && pr.SemanticDB != nil {
		result.SemanticDB = pr.SemanticDB.WithCheck(checked)
		return
	}
	result.SemanticDB = semanticdb.FromCheck(checked)
}

func cloneNativeCheckResult(checked api.CheckResult) *api.CheckResult {
	checked.EnsureStableIDs()
	out := checked
	out.Summary.ErrorsByContext = cloneStringIntMap(checked.Summary.ErrorsByContext)
	out.Summary.ErrorDetails = cloneErrorDetailMap(checked.Summary.ErrorDetails)
	out.TypedNodes = append([]api.CheckedNode(nil), checked.TypedNodes...)
	for i := range out.TypedNodes {
		out.TypedNodes[i].Type = cloneTypeRepr(out.TypedNodes[i].Type)
	}
	out.Bindings = append([]api.CheckedBinding(nil), checked.Bindings...)
	for i := range out.Bindings {
		out.Bindings[i].Type = cloneTypeRepr(out.Bindings[i].Type)
	}
	out.Symbols = append([]api.CheckedSymbol(nil), checked.Symbols...)
	for i := range out.Symbols {
		out.Symbols[i].Type = cloneTypeRepr(out.Symbols[i].Type)
	}
	out.Instantiations = append([]api.CheckInstantiation(nil), checked.Instantiations...)
	for i := range out.Instantiations {
		out.Instantiations[i].TypeArgs = cloneTypeReprList(out.Instantiations[i].TypeArgs)
		out.Instantiations[i].TypeArgIDs = append([]int(nil), out.Instantiations[i].TypeArgIDs...)
		out.Instantiations[i].ResultType = cloneTypeRepr(out.Instantiations[i].ResultType)
	}
	out.Diagnostics = cloneNativeDiagnostics(checked.Diagnostics)
	return &out
}

func cloneTypeRepr(src *api.TypeRepr) *api.TypeRepr {
	if src == nil {
		return nil
	}
	out := *src
	out.Args = cloneTypeReprList(src.Args)
	out.Return = cloneTypeRepr(src.Return)
	return &out
}

func cloneTypeReprList(src []api.TypeRepr) []api.TypeRepr {
	if len(src) == 0 {
		return nil
	}
	out := make([]api.TypeRepr, len(src))
	for i := range src {
		out[i] = src[i]
		out[i].Args = cloneTypeReprList(src[i].Args)
		out[i].Return = cloneTypeRepr(src[i].Return)
	}
	return out
}

func cloneNativeDiagnostics(src []api.CheckDiagnosticRecord) []api.CheckDiagnosticRecord {
	if len(src) == 0 {
		return nil
	}
	out := make([]api.CheckDiagnosticRecord, len(src))
	for i := range src {
		out[i] = src[i]
		out[i].Notes = append([]string(nil), src[i].Notes...)
	}
	return out
}
