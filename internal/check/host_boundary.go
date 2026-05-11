package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/semanticdb"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/spanid"
)

const nativeCheckerEnv = "OSTY_NATIVE_CHECKER_BIN"

// The checker targets an Osty-native request/response boundary. The default
// host implementation uses the embedded selfhost checker in-process;
// OSTY_NATIVE_CHECKER_BIN is reserved for explicit override/debug use and must
// not be treated as the primary backend-selection signal.
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
	cmd := exec.Command(e.path)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return api.CheckResult{}, fmt.Errorf("exec %s: %w (%s)", e.path, err, msg)
	}
	var checked api.CheckResult
	if err := json.Unmarshal(out, &checked); err != nil {
		return api.CheckResult{}, fmt.Errorf("decode native checker response: %w", err)
	}
	checked.EnsureStableIDs()
	return checked, nil
}

type embeddedNativeChecker struct{}

func (embeddedNativeChecker) CheckSourceStructured(src []byte) (api.CheckResult, error) {
	return selfhost.CheckSourceStructured(src), nil
}

func (embeddedNativeChecker) CheckPackageStructured(input api.PackageCheckInput) (api.CheckResult, error) {
	return selfhost.CheckPackageStructured(input)
}

var nativeCheckerFactory = defaultNativeChecker

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
			"the OSTY_NATIVE_CHECKER_BIN override is invalid",
			note,
		))
		return
	}
	var (
		checked    api.CheckResult
		checkedSrc selfhostCheckedSource
		err        error
	)
	switch r := runner.(type) {
	case nativePackageChecker:
		// File-mode callers already hold the parsed public AST, so prefer the
		// structured package request: it reuses the package checker's direct
		// public-AST -> selfhost-AstArena lowering and avoids routing single-file
		// checks back through the astbridge/public-AST adapter path.
		checkedSrc = selfhostFileStructuredSource(file, rr, src)
		if dump := os.Getenv("OSTY_NATIVE_CHECKER_SOURCE_DUMP"); dump != "" {
			_ = os.WriteFile(dump, checkedSrc.source, 0o644)
		}
		checked, err = r.CheckPackageStructured(selfhostSingleFileCheckInput(file, src, stdlib))
	default:
		checkedSrc = selfhostFileSource(file, rr, src, stdlib)
		if dump := os.Getenv("OSTY_NATIVE_CHECKER_SOURCE_DUMP"); dump != "" {
			_ = os.WriteFile(dump, checkedSrc.source, 0o644)
		}
		checked, err = runner.CheckSourceStructured(checkedSrc.source)
	}
	if err != nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			"the selected Osty-native checker failed",
			err.Error(),
		))
		return
	}
	policy := nativeDiagPolicy{privileged: privileged}
	result.Diags = append(result.Diags, nativeCheckerDiagsForCheckedSource(checkedSrc, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	result.SemanticDB = semanticdb.FromCheck(checked)

}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool) {
	if result == nil || pkg == nil {
		return
	}
	runner, note := nativeCheckerFactory()
	if runner == nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"the OSTY_NATIVE_CHECKER_BIN override is invalid",
			note,
		))
		return
	}
	src := selfhostPackageSource(pkg, ws, stdlib)
	if dump := os.Getenv("OSTY_NATIVE_CHECKER_SOURCE_DUMP"); dump != "" {
		_ = os.WriteFile(dump, src.source, 0o644)
	}
	if len(src.source) == 0 {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"package source bytes were not available to the native checker boundary",
		))
		return
	}
	var (
		checked api.CheckResult
		err     error
	)
	input := selfhostPackageCheckInput(pkg, ws, stdlib, src)
	switch r := runner.(type) {
	case nativePackageChecker:
		checked, err = r.CheckPackageStructured(input)
	default:
		checked, err = runner.CheckSourceStructured(src.source)
	}
	if err != nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"the selected Osty-native checker failed",
			err.Error(),
		))
		return
	}
	policy := nativeDiagPolicy{privileged: privileged}
	result.Diags = append(result.Diags, nativeCheckerDiagsForCheckedSource(src, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	attachSemanticDB(result, pr, checked)

}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
	if ws == nil {
		return
	}
	// Per-package native checker calls are the slowest stage of
	// `osty check` / `osty build` on multi-package workspaces: each
	// CheckPackageStructured round-trip either forks a subprocess
	// (managed checker) or runs the embedded self-host checker, both
	// O(tens of ms) per package and trivially CPU-independent across
	// packages since the input is a read-only view of the resolved
	// workspace. Run them in parallel across a bounded worker pool
	// and fold each result back into the shared type maps under a
	// single mutex — the overlay writes are cheap next to the
	// checker itself, so contention is not a bottleneck.
	//
	// Opt-out via OSTY_CHECK_PARALLEL=0 for debugging ordering-
	// dependent bugs. Cap at GOMAXPROCS so we don't oversubscribe
	// when embedded; subprocess workers scale down gracefully via
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
				runSelfhostPackageResultLocked(j.result, j.pkg, j.pr, ws, stdlib, j.privileged, &mu)
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
}

// runSelfhostPackageResultLocked performs the native checker call
// outside the lock (thread-safe: the input is a read-only view of the
// resolved workspace, and each runner.CheckPackageStructured call
// carries its own state) and then serializes the overlay writes to
// the shared type maps under `mu`. This is the parallel variant of
// applySelfhostPackageResult; the single-threaded fast path in
// applySelfhostWorkspaceResults still calls the non-locked version.
func runSelfhostPackageResultLocked(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool, mu *sync.Mutex) {
	if result == nil || pkg == nil {
		return
	}
	runner, note := nativeCheckerFactory()
	if runner == nil {
		mu.Lock()
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"the OSTY_NATIVE_CHECKER_BIN override is invalid",
			note,
		))
		mu.Unlock()
		return
	}
	src := selfhostPackageSource(pkg, ws, stdlib)
	if dump := os.Getenv("OSTY_NATIVE_CHECKER_SOURCE_DUMP"); dump != "" {
		_ = os.WriteFile(dump, src.source, 0o644)
	}
	if len(src.source) == 0 {
		mu.Lock()
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"package source bytes were not available to the native checker boundary",
		))
		mu.Unlock()
		return
	}
	var (
		checked api.CheckResult
		err     error
	)
	input := selfhostPackageCheckInput(pkg, ws, stdlib, src)
	switch r := runner.(type) {
	case nativePackageChecker:
		checked, err = r.CheckPackageStructured(input)
	default:
		checked, err = runner.CheckSourceStructured(src.source)
	}
	if err != nil {
		mu.Lock()
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"the selected Osty-native checker failed",
			err.Error(),
		))
		mu.Unlock()
		return
	}
	policy := nativeDiagPolicy{privileged: privileged}
	diags := nativeCheckerDiagsForCheckedSource(src, checked, policy)
	telemetry := nativeCheckerTelemetry(checked, policy)

	mu.Lock()
	defer mu.Unlock()
	result.Diags = append(result.Diags, diags...)
	result.NativeCheckerTelemetry = telemetry
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	attachSemanticDB(result, pr, checked)

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
