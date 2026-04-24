package check

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

const nativeCheckerEnv = "OSTY_NATIVE_CHECKER_BIN"

// The checker targets an Osty-native request/response boundary. The default
// host implementation uses the embedded selfhost checker in-process; callers
// can opt into an external executable by setting OSTY_NATIVE_CHECKER_BIN.
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

// UseEmbeddedNativeChecker forces the in-process selfhost checker for the
// remainder of the process. Use only in developer tooling (bootstrap-gen)
// where the exec + JSON boundary around the managed checker binary is
// pure overhead — the embedded path calls selfhost.CheckSourceStructured
// directly, avoiding the subprocess build step, fork/exec cost, and
// marshal/unmarshal of the checker payload.
func UseEmbeddedNativeChecker() {
	nativeCheckerFactory = func() (nativeChecker, string) {
		return embeddedNativeChecker{}, ""
	}
}

// UseCachedEmbeddedNativeChecker forces the in-process selfhost checker and
// persists structured results under cacheDir. validity scopes cached entries
// to a specific checker version — callers typically pass a hex digest of
// internal/selfhost/generated.go so cache entries are transparently
// invalidated whenever the compiled-in checker changes.
//
// The regen pipeline is the primary consumer: iterating on
// internal/bootstrap/gen/*.go rebuilds osty-bootstrap-gen but leaves the
// merged toolchain source and the selfhost checker byte-identical, so the
// ~tens-of-seconds CheckPackageStructured call collapses to a JSON read
// on the second and subsequent runs.
//
// cacheDir is created on demand. A misformatted cache entry is silently
// rebuilt. The cache is plain JSON so it is trivially introspectable
// with `cat`; no schema migration is attempted, so bump validity if the
// api.CheckResult shape changes.
func UseCachedEmbeddedNativeChecker(cacheDir, validity string) {
	checker := cachedNativeChecker{
		backing:  embeddedNativeChecker{},
		dir:      filepath.Join(cacheDir, validity),
		validity: validity,
	}
	nativeCheckerFactory = func() (nativeChecker, string) {
		return checker, ""
	}
}

// EmbeddedCheckerFingerprint returns a cache-stable identifier for the Go
// sources compiled into the embedded selfhost checker rooted at repoRoot. When
// the bundled generated sources are unavailable it returns the empty string so
// callers can fall back to a coarser validity token.
func EmbeddedCheckerFingerprint(repoRoot string) string {
	h := sha256.New()
	fmt.Fprintf(h, "go=%s\nos=%s\narch=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	files := []string{
		"internal/selfhost/generated.go",
		"internal/selfhost/astbridge/generated.go",
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			return ""
		}
		fmt.Fprintf(h, "%s=%d\n", rel, len(data))
		h.Write(data)
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:12])
}

// UseCachedDefaultNativeChecker wraps whichever checker `defaultNativeChecker`
// would return (embedded by default, or an explicit subprocess override) in
// the on-disk
// cache layer. First-time builds pay the full check cost; second-and-later
// builds with unchanged package inputs short-circuit to a JSON read
// (~microseconds) instead of re-running the checker. Unchanged-package
// granularity gives multi-second wins on incremental `osty check` / `osty
// build` iterations where only one or two packages change per edit.
//
// Calling this from cmd/osty build.go / run.go / query.go activates the
// cache for the lifetime of the process; fingerprint validity is the
// caller's responsibility (typically a digest of internal/selfhost/
// generated.go so a checker-binary update invalidates every entry).
func UseCachedDefaultNativeChecker(cacheDir, validity string) {
	backing, note := defaultNativeChecker()
	if backing == nil {
		// Preserve the error note so callers see the same diagnostic as
		// the uncached path when an explicitly configured checker can't
		// start.
		nativeCheckerFactory = func() (nativeChecker, string) {
			return nil, note
		}
		return
	}
	checker := cachedNativeChecker{
		backing:  backing,
		dir:      filepath.Join(cacheDir, validity),
		validity: validity,
	}
	nativeCheckerFactory = func() (nativeChecker, string) {
		return checker, note
	}
}

// cachedNativeChecker wraps any nativeChecker (embedded, managed exec,
// or future backends) with an on-disk JSON cache keyed by the
// fingerprint of the input. First-time inputs pay the full cost of
// `backing.CheckSourceStructured` / `backing.CheckPackageStructured`;
// subsequent identical inputs hit the cache and return in
// microseconds, which turns `osty check` / `osty build` on a clean
// incremental edit from multi-second into near-zero.
//
// The on-disk entry is validity-scoped: callers pass a version tag
// (typically a hex digest of internal/selfhost/generated.go) so a
// checker-binary update transparently invalidates every entry without
// explicit migration.
type cachedNativeChecker struct {
	backing  nativeChecker
	dir      string
	validity string
}

func (c cachedNativeChecker) CheckSourceStructured(src []byte) (api.CheckResult, error) {
	key := cachedEmbeddedKey("src", src)
	if res, ok := c.read(key); ok {
		return res, nil
	}
	res, err := c.backing.CheckSourceStructured(src)
	if err == nil {
		c.write(key, res)
	}
	return res, err
}

func (c cachedNativeChecker) CheckPackageStructured(input selfhost.PackageCheckInput) (api.CheckResult, error) {
	// Key on the raw source + a stable subset of the import surface.
	// Hashing the full PackageCheckInput through json.Marshal would
	// traverse the entire parsed AST — multi-second for the regen
	// bundle, and unstable when pointer-graph ordering differs across
	// runs (map iteration, slice identity) — which both defeats the
	// cache and makes the hit path slower than the call it replaces.
	key := cachedEmbeddedKey("pkg", packageCheckFingerprint(input))
	if res, ok := c.read(key); ok {
		return res, nil
	}
	// The backing checker may or may not implement the package path;
	// embedded does, the subprocess exec does, and anything else
	// falls through to the single-source entry. We pick the package
	// path explicitly so the subprocess round-trip isn't bypassed.
	var (
		res api.CheckResult
		err error
	)
	if pc, ok := c.backing.(nativePackageChecker); ok {
		res, err = pc.CheckPackageStructured(input)
	} else {
		// Backing doesn't implement the package path; fall back to
		// concatenating file sources so the single-source entry sees
		// a coherent snapshot. This mirrors how the managed checker
		// exec splices the package together pre-subprocess.
		var buf bytes.Buffer
		for _, f := range input.Files {
			buf.Write(f.Source)
			if len(f.Source) > 0 && f.Source[len(f.Source)-1] != '\n' {
				buf.WriteByte('\n')
			}
		}
		res, err = c.backing.CheckSourceStructured(buf.Bytes())
	}
	if err == nil {
		c.write(key, res)
	}
	return res, err
}

func packageCheckFingerprint(input selfhost.PackageCheckInput) []byte {
	h := sha256.New()
	for _, f := range input.Files {
		fmt.Fprintf(h, "file=%s base=%d len=%d\n", f.Name, f.Base, len(f.Source))
		h.Write(f.Source)
		h.Write([]byte{'\n'})
	}
	for _, imp := range input.Imports {
		fmt.Fprintf(h, "import=%s fns=%d types=%d variants=%d fields=%d aliases=%d iface=%d\n",
			imp.Alias,
			len(imp.Functions),
			len(imp.TypeDecls),
			len(imp.Variants),
			len(imp.Fields),
			len(imp.Aliases),
			len(imp.InterfaceExts),
		)
		for _, fn := range imp.Functions {
			fmt.Fprintf(h, "  fn=%s owner=%s recv=%s ret=%s params=%d\n",
				fn.Name, fn.Owner, fn.ReceiverType, fn.ReturnType, len(fn.ParamTypes))
			for i, pt := range fn.ParamTypes {
				fmt.Fprintf(h, "    p%d=%s\n", i, pt)
			}
		}
		for _, td := range imp.TypeDecls {
			fmt.Fprintf(h, "  type=%s kind=%s generics=%d\n", td.Name, td.Kind, len(td.Generics))
		}
		for _, field := range imp.Fields {
			fmt.Fprintf(h, "  field=%s/%s type=%s exported=%t default=%t\n",
				field.Owner, field.Name, field.TypeName, field.Exported, field.HasDefault)
		}
		for _, v := range imp.Variants {
			fmt.Fprintf(h, "  variant=%s/%s fields=%d\n", v.Owner, v.Name, len(v.FieldTypes))
		}
	}
	sum := h.Sum(nil)
	return sum[:]
}

func (c cachedNativeChecker) read(key string) (api.CheckResult, bool) {
	data, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return api.CheckResult{}, false
	}
	var res api.CheckResult
	if err := json.Unmarshal(data, &res); err != nil {
		return api.CheckResult{}, false
	}
	return res, true
}

func (c cachedNativeChecker) write(key string, res api.CheckResult) {
	data, err := json.Marshal(res)
	if err != nil {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	// Atomic swap: write to a sibling file then rename. Avoids a racing
	// reader seeing a half-written entry if two regen pipelines overlap.
	tmp, err := os.CreateTemp(c.dir, key+"-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, filepath.Join(c.dir, key+".json")); err != nil {
		os.Remove(tmpPath)
	}
}

func cachedEmbeddedKey(tag string, data []byte) string {
	sum := sha256.Sum256(data)
	return tag + "-" + hex.EncodeToString(sum[:])
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
	return embeddedNativeChecker{}, ""
}

type selfhostCheckedSource struct {
	source []byte
	files  []selfhostFileSegment
}

type selfhostFileSegment struct {
	file      *ast.File
	scope     *resolve.Scope
	refs      map[ast.NodeID]*resolve.Symbol
	base      int
	sourceMap *sourcemap.Map
}

func applyNativeFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider, privileged bool) {
	applySelfhostFileResult(result, file, rr, src, stdlib, privileged)
}

func applySelfhostFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider, privileged bool) {
	if result == nil {
		return
	}
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
			"the Osty-native checker executable failed",
			err.Error(),
		))
		return
	}
	policy := nativeDiagPolicy{privileged: privileged}
	result.Diags = append(result.Diags, nativeCheckerDiags(checkedSrc.source, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	overlaySelfhostResult(result, checkedSrc, checked)
}

func applyNativePackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool) {
	applySelfhostPackageResult(result, pkg, pr, ws, stdlib, privileged)
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, _ *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool) {
	if result == nil || pkg == nil {
		return
	}
	runner, note := nativeCheckerFactory()
	if runner == nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"no Osty-native checker executable is configured",
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
			"the Osty-native checker executable failed",
			err.Error(),
		))
		return
	}
	policy := nativeDiagPolicy{privileged: privileged}
	result.Diags = append(result.Diags, nativeCheckerDiags(src.source, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	overlaySelfhostResult(result, src, checked)
}

func applyNativeWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
	applySelfhostWorkspaceResults(ws, resolved, results, stdlib)
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, _ map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
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
		jobs = append(jobs, pkgJob{path: path, pkg: pkg, result: result, privileged: privileged})
	}
	if len(jobs) == 0 {
		return
	}
	if len(jobs) == 1 || os.Getenv("OSTY_CHECK_PARALLEL") == "0" {
		for _, j := range jobs {
			applySelfhostPackageResult(j.result, j.pkg, nil, ws, stdlib, j.privileged)
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
				runSelfhostPackageResultLocked(j.result, j.pkg, ws, stdlib, j.privileged, &mu)
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
func runSelfhostPackageResultLocked(result *Result, pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool, mu *sync.Mutex) {
	if result == nil || pkg == nil {
		return
	}
	runner, note := nativeCheckerFactory()
	if runner == nil {
		mu.Lock()
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"no Osty-native checker executable is configured",
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
			"the Osty-native checker executable failed",
			err.Error(),
		))
		mu.Unlock()
		return
	}
	policy := nativeDiagPolicy{privileged: privileged}
	diags := nativeCheckerDiags(src.source, checked, policy)
	telemetry := nativeCheckerTelemetry(checked, policy)

	mu.Lock()
	defer mu.Unlock()
	result.Diags = append(result.Diags, diags...)
	result.NativeCheckerTelemetry = telemetry
	overlaySelfhostResult(result, src, checked)
}

type nativeDiagPolicy struct {
	privileged bool
}

func nativeCheckerTelemetry(checked api.CheckResult, policy nativeDiagPolicy) *NativeCheckerTelemetry {
	summary := filteredNativeSummary(checked, policy)
	if summary.Assignments == 0 && summary.Errors == 0 && len(summary.ErrorsByContext) == 0 {
		return nil
	}
	return &NativeCheckerTelemetry{
		Assignments:     summary.Assignments,
		Accepted:        summary.Accepted,
		Errors:          summary.Errors,
		ErrorsByContext: cloneStringIntMap(summary.ErrorsByContext),
		ErrorDetails:    cloneErrorDetailMap(summary.ErrorDetails),
	}
}

func cloneErrorDetailMap(src map[string]map[string]int) map[string]map[string]int {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]map[string]int, len(src))
	for ctx, inner := range src {
		out[ctx] = cloneStringIntMap(inner)
	}
	return out
}

func cloneStringIntMap(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func nativeCheckerDiags(src []byte, checked api.CheckResult, policy nativeDiagPolicy) []*diag.Diagnostic {
	out := make([]*diag.Diagnostic, 0, len(checked.Diagnostics))
	for _, d := range checked.Diagnostics {
		if shouldSuppressNativeDiag(d, policy) {
			continue
		}
		if converted := convertNativeDiag(src, d); converted != nil {
			out = append(out, converted)
		}
	}
	summary := filteredNativeSummary(checked, policy)
	if summary.Errors == 0 {
		return out
	}
	label := "native checker reported type errors"
	if summary.Errors == 1 {
		label = "native checker reported a type error"
	}
	out = append(out,
		diag.New(diag.Error, fmt.Sprintf("%s: %d error(s)", label, summary.Errors)).
			Code(diag.CodeTypeMismatch).
			Primary(fileStartSpan(src), "native checker summary").
			Note(fmt.Sprintf(
				"native checker accepted %d of %d assignment/return/call checks",
				summary.Accepted,
				summary.Assignments,
			)).
			Build(),
	)
	return out
}

func filteredNativeSummary(checked api.CheckResult, policy nativeDiagPolicy) api.CheckSummary {
	summary := checked.Summary
	if !policy.privileged {
		return summary
	}
	suppressed := 0
	for _, d := range checked.Diagnostics {
		if shouldSuppressNativeDiag(d, policy) && nativeDiagIsError(d) {
			suppressed++
		}
	}
	if suppressed == 0 {
		return summary
	}
	if summary.Errors < suppressed {
		summary.Errors = 0
	} else {
		summary.Errors -= suppressed
	}
	if len(summary.ErrorsByContext) > 0 {
		summary.ErrorsByContext = cloneStringIntMap(summary.ErrorsByContext)
		delete(summary.ErrorsByContext, diag.CodeRuntimePrivilegeViolation)
		if len(summary.ErrorsByContext) == 0 {
			summary.ErrorsByContext = nil
		}
	}
	if len(summary.ErrorDetails) > 0 {
		summary.ErrorDetails = cloneErrorDetailMap(summary.ErrorDetails)
		delete(summary.ErrorDetails, diag.CodeRuntimePrivilegeViolation)
		if len(summary.ErrorDetails) == 0 {
			summary.ErrorDetails = nil
		}
	}
	return summary
}

func shouldSuppressNativeDiag(d api.CheckDiagnosticRecord, policy nativeDiagPolicy) bool {
	return policy.privileged && d.Code == diag.CodeRuntimePrivilegeViolation
}

func nativeDiagIsError(d api.CheckDiagnosticRecord) bool {
	switch strings.ToLower(strings.TrimSpace(d.Severity)) {
	case "warning", "warn", "lint":
		return false
	default:
		return true
	}
}

// convertNativeDiag lifts a per-record structured diagnostic emitted by
// the Osty-native checker (see toolchain/check_diag.osty) into a
// `*diag.Diagnostic`. Start/End are byte offsets in the bundled source
// the native checker consumed; we translate to line/column via a
// single linear scan of that byte buffer. Position accuracy is
// best-effort — the bundled source prepends stdlib imports so the
// reported line/column may diverge from the user's file by a fixed
// offset. Downstream consumers still get the structured code + message
// + notes, which is what the migrated gates (§19.6 E0773, ...) rely on.
func convertNativeDiag(src []byte, d api.CheckDiagnosticRecord) *diag.Diagnostic {
	if d.Code == "" && d.Message == "" {
		return nil
	}
	severity := diag.Error
	switch strings.ToLower(d.Severity) {
	case "warning", "warn":
		severity = diag.Warning
	}
	b := diag.New(severity, d.Message)
	if d.Code != "" {
		b = b.Code(d.Code)
	}
	if d.File != "" {
		b = b.File(d.File)
	}
	b = b.Primary(byteRangeSpan(src, d.Start, d.End), "")
	for _, note := range d.Notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		b = b.Note(note)
	}
	return b.Build()
}

// byteRangeSpan builds a `diag.Span` for a [start, end) byte range
// into `src`. A lightweight line-start index keeps repeated
// conversions off the O(offset) byte-walk path while preserving the
// same 1-based line/column shape. Clamping keeps downstream
// renderers robust even when the native checker reports offsets
// beyond EOF.
func byteRangeSpan(src []byte, start, end int) diag.Span {
	idx := newSelfhostDiagLineIndex(src)
	start = idx.clampOffset(start)
	end = idx.clampOffset(end)
	if end < start {
		end = start
	}
	if idx.total == 0 {
		p := token.Pos{Line: 1, Column: 1, Offset: 0}
		return diag.Span{Start: p, End: p}
	}
	return diag.Span{Start: idx.positionAt(start), End: idx.positionAt(end)}
}

type selfhostDiagLineIndex struct {
	starts []int
	total  int
}

func newSelfhostDiagLineIndex(src []byte) selfhostDiagLineIndex {
	starts := make([]int, 1, 1+len(src)/32)
	starts[0] = 0
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return selfhostDiagLineIndex{starts: starts, total: len(src)}
}

func (idx selfhostDiagLineIndex) clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > idx.total {
		return idx.total
	}
	return offset
}

func (idx selfhostDiagLineIndex) positionAt(offset int) token.Pos {
	offset = idx.clampOffset(offset)
	lineIdx := sort.Search(len(idx.starts), func(i int) bool {
		return idx.starts[i] > offset
	}) - 1
	if lineIdx < 0 {
		lineIdx = 0
	}
	lineStart := idx.starts[lineIdx]
	return token.Pos{
		Line:   lineIdx + 1,
		Column: offset - lineStart + 1,
		Offset: offset,
	}
}

type selfhostSpanKey struct {
	start int
	end   int
}

type selfhostNameSpanKey struct {
	selfhostSpanKey
	name string
}

type selfhostSpanIndex struct {
	exprs       map[selfhostSpanKey]ast.Expr
	exprsByFrom map[int][]ast.Expr
	exprKeys    map[ast.Expr]selfhostSpanKey
	calls       map[selfhostSpanKey]*ast.CallExpr
	callsByFrom map[int][]*ast.CallExpr
	callKeys    map[*ast.CallExpr]selfhostSpanKey
	scopes      map[selfhostSpanKey]*resolve.Scope
	bindings    map[selfhostNameSpanKey]ast.Node
	symbols     map[selfhostNameSpanKey]*resolve.Symbol
}

func (idx *selfhostSpanIndex) bindNode(key selfhostNameSpanKey, n ast.Node) {
	if idx.bindings[key] != nil {
		return
	}
	idx.bindings[key] = n
}

func overlaySelfhostResult(result *Result, src selfhostCheckedSource, checked api.CheckResult) {
	if result == nil {
		return
	}
	idx := buildSelfhostSpanIndex(src)
	for _, node := range checked.TypedNodes {
		key := selfhostSpanKey{start: node.Start, end: node.End}
		expr := idx.lookupExpr(key, node.Kind)
		if expr == nil {
			continue
		}
		t := parseSelfhostTypeName(node.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		result.Types[expr] = t
	}
	for _, binding := range checked.Bindings {
		key := selfhostSpanKey{start: binding.Start, end: binding.End}
		t := parseSelfhostTypeName(binding.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: binding.Name}
		if n := idx.bindings[nameKey]; n != nil {
			result.LetTypes[n] = t
		}
		if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, symbol := range checked.Symbols {
		key := selfhostSpanKey{start: symbol.Start, end: symbol.End}
		t := parseSelfhostTypeName(symbol.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: symbol.Name}
		if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, inst := range checked.Instantiations {
		key := selfhostSpanKey{start: inst.Start, end: inst.End}
		call := idx.lookupCall(key)
		if call == nil || len(inst.TypeArgs) == 0 {
			continue
		}
		args := make([]types.Type, 0, len(inst.TypeArgs))
		for _, name := range inst.TypeArgs {
			if t := parseSelfhostTypeName(name, idx.scopeFor(key)); t != nil {
				args = append(args, t)
			}
		}
		if len(args) == len(inst.TypeArgs) && call.ID != 0 {
			if _, existed := result.InstantiationsByID[call.ID]; !existed {
				result.InstantiationCalls = append(result.InstantiationCalls, call)
			}
			result.InstantiationsByID[call.ID] = args
		}
	}
}

func buildSelfhostSpanIndex(src selfhostCheckedSource) *selfhostSpanIndex {
	idx := &selfhostSpanIndex{
		exprs:       map[selfhostSpanKey]ast.Expr{},
		exprsByFrom: map[int][]ast.Expr{},
		exprKeys:    map[ast.Expr]selfhostSpanKey{},
		calls:       map[selfhostSpanKey]*ast.CallExpr{},
		callsByFrom: map[int][]*ast.CallExpr{},
		callKeys:    map[*ast.CallExpr]selfhostSpanKey{},
		scopes:      map[selfhostSpanKey]*resolve.Scope{},
		bindings:    map[selfhostNameSpanKey]ast.Node{},
		symbols:     map[selfhostNameSpanKey]*resolve.Symbol{},
	}
	for _, file := range src.files {
		if file.file == nil {
			continue
		}
		for _, decl := range file.file.Decls {
			idx.addNode(decl, file.base, file.scope, file.sourceMap)
		}
		for _, stmt := range file.file.Stmts {
			idx.addNode(stmt, file.base, file.scope, file.sourceMap)
		}
		idx.addScopeSubtree(file.scope, file.base, file.sourceMap)
		for _, sym := range file.refs {
			idx.addSymbol(sym, file.base, file.scope, file.sourceMap)
		}
	}
	return idx
}

func (idx *selfhostSpanIndex) scopeFor(key selfhostSpanKey) *resolve.Scope {
	if scope := idx.scopes[key]; scope != nil {
		return scope
	}
	var best *resolve.Scope
	bestSize := int(^uint(0) >> 1)
	for span, scope := range idx.scopes {
		if scope == nil {
			continue
		}
		if span.start > key.start || span.end < key.end {
			continue
		}
		size := span.end - span.start
		if size < bestSize {
			best = scope
			bestSize = size
		}
	}
	return best
}

func (idx *selfhostSpanIndex) addNode(n ast.Node, base int, scope *resolve.Scope, sm *sourcemap.Map) {
	if n == nil {
		return
	}
	// Nilable AST fields (e.g. FnDecl.Body for interface methods without a
	// default) arrive here as a non-nil ast.Node interface wrapping a nil
	// pointer. The `n == nil` guard above misses that; the type switch below
	// would then dereference the nil pointer.
	if rv := reflect.ValueOf(n); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return
	}
	key, haveKey := spanKeyForNode(n, base, sm)
	if haveKey {
		if _, ok := idx.scopes[key]; !ok {
			idx.scopes[key] = scope
		}
		idx.addDeclaredSymbol(n, key, scope)
		if e, ok := n.(ast.Expr); ok {
			if _, have := idx.exprs[key]; !have {
				idx.exprs[key] = e
			}
			idx.exprsByFrom[key.start] = append(idx.exprsByFrom[key.start], e)
			idx.exprKeys[e] = key
			if c, ok := e.(*ast.CallExpr); ok {
				idx.calls[key] = c
				idx.callsByFrom[key.start] = append(idx.callsByFrom[key.start], c)
				idx.callKeys[c] = key
			}
		}
	}
	switch v := n.(type) {
	case *ast.FnDecl:
		idx.addNode(v.Recv, base, scope, sm)
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.StructDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.EnumDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, variant := range v.Variants {
			idx.addNode(variant, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.InterfaceDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, sup := range v.Extends {
			idx.addNode(sup, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.TypeAliasDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		idx.addNode(v.Target, base, scope, sm)
	case *ast.LetDecl:
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.UseDecl:
		for _, d := range v.GoBody {
			idx.addNode(d, base, scope, sm)
		}
	case *ast.Field:
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Default, base, scope, sm)
	case *ast.Variant:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
	case *ast.Param:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
		}
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Default, base, scope, sm)
	case *ast.GenericParam:
		for _, con := range v.Constraints {
			idx.addNode(con, base, scope, sm)
		}
	case *ast.NamedType:
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.OptionalType:
		idx.addNode(v.Inner, base, scope, sm)
	case *ast.TupleType:
		for _, elem := range v.Elems {
			idx.addNode(elem, base, scope, sm)
		}
	case *ast.FnType:
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
	case *ast.Block:
		for _, s := range v.Stmts {
			idx.addNode(s, base, scope, sm)
		}
	case *ast.LetStmt:
		if name := bindingPatternName(v.Pattern); name != "" {
			if patKey, ok := spanKeyForNode(v.Pattern, base, sm); ok {
				idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: patKey, name: name}, v)
			}
		}
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ExprStmt:
		idx.addNode(v.X, base, scope, sm)
	case *ast.AssignStmt:
		for _, t := range v.Targets {
			idx.addNode(t, base, scope, sm)
		}
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ReturnStmt:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ChanSendStmt:
		idx.addNode(v.Channel, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.DeferStmt:
		idx.addNode(v.X, base, scope, sm)
	case *ast.ForStmt:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Iter, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.UnaryExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.BinaryExpr:
		idx.addNode(v.Left, base, scope, sm)
		idx.addNode(v.Right, base, scope, sm)
	case *ast.QuestionExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.CallExpr:
		idx.addNode(v.Fn, base, scope, sm)
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.Arg:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.FieldExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.IndexExpr:
		idx.addNode(v.X, base, scope, sm)
		idx.addNode(v.Index, base, scope, sm)
	case *ast.TurbofishExpr:
		idx.addNode(v.Base, base, scope, sm)
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.RangeExpr:
		idx.addNode(v.Start, base, scope, sm)
		idx.addNode(v.Stop, base, scope, sm)
	case *ast.ParenExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.TupleExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.ListExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.MapExpr:
		for _, e := range v.Entries {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.MapEntry:
		idx.addNode(v.Key, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.StructLit:
		idx.addNode(v.Type, base, scope, sm)
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
		idx.addNode(v.Spread, base, scope, sm)
	case *ast.StructLitField:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.IfExpr:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Cond, base, scope, sm)
		idx.addNode(v.Then, base, scope, sm)
		idx.addNode(v.Else, base, scope, sm)
	case *ast.MatchExpr:
		idx.addNode(v.Scrutinee, base, scope, sm)
		for _, a := range v.Arms {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.MatchArm:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Guard, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.ClosureExpr:
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.LiteralPat:
		idx.addNode(v.Literal, base, scope, sm)
	case *ast.IdentPat:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
		}
	case *ast.TuplePat:
		for _, p := range v.Elems {
			idx.addNode(p, base, scope, sm)
		}
	case *ast.StructPat:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
	case *ast.StructPatField:
		idx.addNode(v.Pattern, base, scope, sm)
	case *ast.VariantPat:
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.RangePat:
		idx.addNode(v.Start, base, scope, sm)
		idx.addNode(v.Stop, base, scope, sm)
	case *ast.OrPat:
		for _, a := range v.Alts {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.BindingPat:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
		}
		idx.addNode(v.Pattern, base, scope, sm)
	}
}

func (idx *selfhostSpanIndex) addScopeSubtree(scope *resolve.Scope, base int, sm *sourcemap.Map) {
	if scope == nil {
		return
	}
	for _, sym := range scope.Symbols() {
		idx.addSymbol(sym, base, scope, sm)
	}
	for _, child := range scope.Children() {
		idx.addScopeSubtree(child, base, sm)
	}
}

func (idx *selfhostSpanIndex) addDeclaredSymbol(n ast.Node, key selfhostSpanKey, scope *resolve.Scope) {
	sym := declaredSymbolForNode(n, scope)
	if sym == nil {
		return
	}
	idx.symbols[selfhostNameSpanKey{selfhostSpanKey: key, name: sym.Name}] = sym
}

func declaredSymbolForNode(n ast.Node, scope *resolve.Scope) *resolve.Symbol {
	if scope == nil {
		return nil
	}
	pkgScope := scope.Parent()
	if pkgScope == nil {
		pkgScope = scope
	}
	switch v := n.(type) {
	case *ast.FnDecl:
		if v.Recv != nil || v.Name == "" {
			return nil
		}
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.StructDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.EnumDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.InterfaceDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.TypeAliasDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.LetDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.Variant:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	default:
		return nil
	}
}

func lookupLocalDeclSymbol(scope *resolve.Scope, name string, decl ast.Node) *resolve.Symbol {
	if scope == nil || name == "" || decl == nil {
		return nil
	}
	sym := scope.LookupLocal(name)
	if sym == nil || sym.Decl != decl {
		return nil
	}
	return sym
}

func (idx *selfhostSpanIndex) lookupExpr(key selfhostSpanKey, kind string) ast.Expr {
	if expr := idx.exprs[key]; expr != nil {
		return expr
	}
	var best ast.Expr
	bestSize := int(^uint(0) >> 1)
	for _, expr := range idx.exprsByFrom[key.start] {
		if kind != "" && selfhostExprKind(expr) != kind {
			continue
		}
		exprKey := idx.exprKeys[expr]
		if exprKey.end < key.end {
			continue
		}
		size := exprKey.end - exprKey.start
		if size < bestSize {
			best = expr
			bestSize = size
		}
	}
	if best != nil {
		return best
	}
	for expr, exprKey := range idx.exprKeys {
		if kind != "" && selfhostExprKind(expr) != kind {
			continue
		}
		if exprKey.start > key.start || exprKey.end < key.end {
			continue
		}
		size := exprKey.end - exprKey.start
		if size < bestSize {
			best = expr
			bestSize = size
		}
	}
	return best
}

func (idx *selfhostSpanIndex) lookupCall(key selfhostSpanKey) *ast.CallExpr {
	if call := idx.calls[key]; call != nil {
		return call
	}
	var best *ast.CallExpr
	bestSize := int(^uint(0) >> 1)
	for _, call := range idx.callsByFrom[key.start] {
		callKey := idx.callKeys[call]
		if callKey.end < key.end {
			continue
		}
		size := callKey.end - callKey.start
		if size < bestSize {
			best = call
			bestSize = size
		}
	}
	if best != nil {
		return best
	}
	for call, callKey := range idx.callKeys {
		if callKey.start > key.start || callKey.end < key.end {
			continue
		}
		size := callKey.end - callKey.start
		if size < bestSize {
			best = call
			bestSize = size
		}
	}
	return best
}

func selfhostExprKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.Ident:
		return "Ident"
	case *ast.IntLit:
		return "IntLit"
	case *ast.FloatLit:
		return "FloatLit"
	case *ast.StringLit:
		return "StringLit"
	case *ast.BoolLit:
		return "BoolLit"
	case *ast.CharLit:
		return "CharLit"
	case *ast.ByteLit:
		return "ByteLit"
	case *ast.UnaryExpr:
		return "Unary"
	case *ast.BinaryExpr:
		return "Binary"
	case *ast.QuestionExpr:
		return "Question"
	case *ast.CallExpr:
		return "Call"
	case *ast.FieldExpr:
		return "Field"
	case *ast.IndexExpr:
		return "Index"
	case *ast.TurbofishExpr:
		return "Turbofish"
	case *ast.RangeExpr:
		return "Range"
	case *ast.ParenExpr:
		return "Paren"
	case *ast.TupleExpr:
		return "Tuple"
	case *ast.ListExpr:
		return "List"
	case *ast.MapExpr:
		return "Map"
	case *ast.StructLit:
		return "StructLit"
	case *ast.IfExpr:
		return "If"
	case *ast.MatchExpr:
		return "Match"
	case *ast.ClosureExpr:
		return "Closure"
	case *ast.Block:
		return "Block"
	default:
		return ""
	}
}

func (idx *selfhostSpanIndex) addSymbol(sym *resolve.Symbol, base int, scope *resolve.Scope, sm *sourcemap.Map) {
	if sym == nil || sym.Decl == nil || sym.Name == "" {
		return
	}
	key, ok := spanKeyForNode(sym.Decl, base, sm)
	if !ok {
		return
	}
	idx.symbols[selfhostNameSpanKey{selfhostSpanKey: key, name: sym.Name}] = sym
	if _, ok := idx.scopes[key]; !ok {
		idx.scopes[key] = scope
	}
}

func spanKeyForNode(n ast.Node, base int, sm *sourcemap.Map) (key selfhostSpanKey, ok bool) {
	if n == nil {
		return selfhostSpanKey{}, false
	}
	defer func() {
		if recover() != nil {
			key = selfhostSpanKey{}
			ok = false
		}
	}()
	start := n.Pos().Offset
	end := n.End().Offset
	if sm != nil {
		if generated, ok := sm.GeneratedSpanForOriginal(diag.Span{
			Start: n.Pos(),
			End:   n.End(),
		}); ok {
			start = generated.Start.Offset
			end = generated.End.Offset
		}
	}
	if end < start {
		return selfhostSpanKey{}, false
	}
	return selfhostSpanKey{
		start: base + start,
		end:   base + end,
	}, true
}

func bindingPatternName(p ast.Pattern) string {
	switch p := p.(type) {
	case *ast.IdentPat:
		return p.Name
	case *ast.BindingPat:
		return p.Name
	default:
		return ""
	}
}

func parseSelfhostTypeName(raw string, scope *resolve.Scope) types.Type {
	text := strings.TrimSpace(raw)
	switch text {
	case "", "Invalid", "Poison":
		return types.ErrorType
	case "()", "Unit":
		return types.Unit
	case "Never":
		return types.Never
	case "UntypedInt":
		return types.UntypedIntVal
	case "UntypedFloat":
		return types.UntypedFloatVal
	}
	if strings.HasSuffix(text, "?") {
		inner := parseSelfhostTypeName(strings.TrimSuffix(text, "?"), scope)
		return &types.Optional{Inner: inner}
	}
	if strings.HasPrefix(text, "fn(") {
		return parseSelfhostFnType(text, scope)
	}
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "("), ")"))
		if inner == "" {
			return types.Unit
		}
		parts := splitSelfhostTypeList(inner)
		if len(parts) == 1 {
			return parseSelfhostTypeName(parts[0], scope)
		}
		elems := make([]types.Type, 0, len(parts))
		for _, part := range parts {
			elems = append(elems, parseSelfhostTypeName(part, scope))
		}
		return &types.Tuple{Elems: elems}
	}
	head, argText, hasArgs := splitSelfhostGeneric(text)
	if p := types.PrimitiveByName(head); p != nil && !hasArgs {
		return p
	}
	if head == "Option" && hasArgs {
		args := splitSelfhostTypeList(argText)
		if len(args) == 1 {
			return &types.Optional{Inner: parseSelfhostTypeName(args[0], scope)}
		}
	}
	args := []types.Type(nil)
	if hasArgs {
		for _, part := range splitSelfhostTypeList(argText) {
			args = append(args, parseSelfhostTypeName(part, scope))
		}
	}
	sym := lookupSelfhostTypeSymbol(head, scope)
	if sym.Kind == resolve.SymGeneric {
		return &types.TypeVar{Sym: sym}
	}
	return &types.Named{Sym: sym, Args: args}
}

func parseSelfhostFnType(text string, scope *resolve.Scope) types.Type {
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return types.ErrorType
	}
	close := matchingSelfhostParen(text, open)
	if close < 0 {
		return types.ErrorType
	}
	paramText := strings.TrimSpace(text[open+1 : close])
	var params []types.Type
	if paramText != "" {
		for _, part := range splitSelfhostTypeList(paramText) {
			params = append(params, parseSelfhostTypeName(part, scope))
		}
	}
	ret := types.Type(types.Unit)
	rest := strings.TrimSpace(text[close+1:])
	if strings.HasPrefix(rest, "->") {
		ret = parseSelfhostTypeName(strings.TrimSpace(strings.TrimPrefix(rest, "->")), scope)
	}
	return &types.FnType{Params: params, Return: ret}
}

func splitSelfhostGeneric(text string) (head, args string, ok bool) {
	depth := 0
	start := -1
	for i, r := range text {
		switch r {
		case '<':
			if depth == 0 {
				start = i
			}
			depth++
		case '>':
			depth--
			if depth == 0 && i == len(text)-1 && start >= 0 {
				return strings.TrimSpace(text[:start]), strings.TrimSpace(text[start+1 : i]), true
			}
		}
	}
	return strings.TrimSpace(text), "", false
}

func splitSelfhostTypeList(text string) []string {
	var out []string
	start := 0
	angle := 0
	paren := 0
	for i, r := range text {
		switch r {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case ',':
			if angle == 0 && paren == 0 {
				part := strings.TrimSpace(text[start:i])
				if part != "" {
					out = append(out, part)
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(text[start:]); part != "" {
		out = append(out, part)
	}
	return out
}

func matchingSelfhostParen(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func lookupSelfhostTypeSymbol(head string, scope *resolve.Scope) *resolve.Symbol {
	name := strings.TrimSpace(head)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	if scope != nil {
		if sym := scope.Lookup(name); sym != nil {
			return sym
		}
	}
	if _, ok := scalarByName[name]; ok {
		return syntheticBuiltinSym(name)
	}
	switch name {
	case "List", "Map", "Set", "Option", "Result", "Error", "Equal", "Ordered", "Hashable", "Chan", "Channel", "Handle", "TaskGroup", "Iter":
		return syntheticBuiltinSym(name)
	}
	if name == "Self" || looksLikeSelfhostGeneric(name) {
		return &resolve.Symbol{Name: name, Kind: resolve.SymGeneric}
	}
	return &resolve.Symbol{Name: name, Kind: resolve.SymTypeAlias}
}

func looksLikeSelfhostGeneric(name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, ".<>(), ") {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z' && len(name) == 1
}

func fileStartSpan(src []byte) diag.Span {
	start := token.Pos{Line: 1, Column: 1, Offset: 0}
	end := start
	if len(src) > 0 {
		end = token.Pos{Line: 1, Column: 2, Offset: 1}
	}
	return diag.Span{Start: start, End: end}
}

func selfhostFileSource(file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var canonicalMap *sourcemap.Map
	if file != nil {
		if canonicalSrc, sm := canonical.SourceWithMap(src, file); len(canonicalSrc) > 0 && bytes.Equal(canonicalSrc, src) {
			canonicalMap = sm
		}
	}
	var b bytes.Buffer
	writeSelfhostImports(&b, nil, stdlib, fileUses(file))
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	base := b.Len()
	b.Write(src)
	if !bytes.HasSuffix(src, []byte("\n")) {
		b.WriteByte('\n')
	}
	var scope *resolve.Scope
	var refs map[ast.NodeID]*resolve.Symbol
	if rr != nil {
		scope = rr.FileScope
		refs = rr.RefsByID
	}
	return selfhostCheckedSource{
		source: b.Bytes(),
		files: []selfhostFileSegment{{
			file:      file,
			scope:     scope,
			refs:      refs,
			base:      base,
			sourceMap: canonicalMap,
		}},
	}
}

func selfhostFileStructuredSource(file *ast.File, rr *resolve.Result, src []byte) selfhostCheckedSource {
	var canonicalMap *sourcemap.Map
	if file != nil {
		if canonicalSrc, sm := canonical.SourceWithMap(src, file); len(canonicalSrc) > 0 && bytes.Equal(canonicalSrc, src) {
			canonicalMap = sm
		}
	}
	var scope *resolve.Scope
	var refs map[ast.NodeID]*resolve.Symbol
	if rr != nil {
		scope = rr.FileScope
		refs = rr.RefsByID
	}
	return selfhostCheckedSource{
		source: append([]byte(nil), src...),
		files: []selfhostFileSegment{{
			file:      file,
			scope:     scope,
			refs:      refs,
			base:      0,
			sourceMap: canonicalMap,
		}},
	}
}

func selfhostPackageSource(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var b bytes.Buffer
	writeSelfhostPackageImports(&b, pkg, ws, stdlib)
	var files []selfhostFileSegment
	for _, pf := range pkg.Files {
		src := pf.CheckerSource()
		if len(src) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		base := b.Len()
		b.Write(src)
		if !bytes.HasSuffix(src, []byte("\n")) {
			b.WriteByte('\n')
		}
		files = append(files, selfhostFileSegment{
			file:      pf.File,
			scope:     pf.FileScope,
			refs:      pf.RefsByID,
			base:      base,
			sourceMap: pf.CanonicalMap,
		})
	}
	return selfhostCheckedSource{source: b.Bytes(), files: files}
}

func writeSelfhostPackageImports(b *bytes.Buffer, pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	if pkg == nil {
		return
	}
	var uses []*ast.UseDecl
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		uses = append(uses, pf.File.Uses...)
	}
	writeSelfhostImports(b, ws, stdlib, uses)
}

func writeSelfhostImports(b *bytes.Buffer, ws *resolve.Workspace, stdlib resolve.StdlibProvider, uses []*ast.UseDecl) {
	seen := map[string]bool{}
	for _, use := range uses {
		dotPath := strings.Join(use.Path, ".")
		target := (*resolve.Package)(nil)
		if ws != nil {
			target = ws.Packages[dotPath]
			if target == nil && ws.Stdlib != nil {
				target = ws.Stdlib.LookupPackage(dotPath)
			}
		}
		if target == nil && stdlib != nil {
			target = stdlib.LookupPackage(dotPath)
		}
		if target == nil {
			continue
		}
		alias := use.Alias
		if alias == "" && len(use.Path) > 0 {
			alias = use.Path[len(use.Path)-1]
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		writeSelfhostPackageImport(b, alias, target)
	}
}

func fileUses(file *ast.File) []*ast.UseDecl {
	if file == nil {
		return nil
	}
	return file.Uses
}

func writeSelfhostPackageImport(b *bytes.Buffer, alias string, pkg *resolve.Package) {
	var body bytes.Buffer
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || !fn.Pub || fn.Recv != nil {
				continue
			}
			fmt.Fprintf(&body, "    fn %s", fn.Name)
			if generics := selfhostGenericParams(fn.Generics); generics != "" {
				body.WriteString(generics)
			}
			body.WriteByte('(')
			for i, param := range fn.Params {
				if i > 0 {
					body.WriteString(", ")
				}
				name := param.Name
				if name == "" {
					name = fmt.Sprintf("arg%d", i)
				}
				fmt.Fprintf(&body, "%s: %s", name, selfhostTypeSource(param.Type))
				// Preserve default-availability for the arity check. The
				// concrete literal value doesn't matter — collectFnDecl
				// only looks at paramNode.left >= 0 to set the "?"
				// paramName prefix. `= ()` is a valid literal default
				// (spec §R18) across all param types.
				if param.Default != nil {
					body.WriteString(" = ()")
				}
			}
			body.WriteByte(')')
			if ret := selfhostTypeSource(fn.ReturnType); ret != "()" {
				fmt.Fprintf(&body, " -> %s", ret)
			}
			body.WriteByte('\n')
		}
	}
	if body.Len() == 0 {
		return
	}
	fmt.Fprintf(b, "use go %q as %s {\n", alias, alias)
	b.Write(body.Bytes())
	b.WriteString("}\n")
}

// selfhostGenericParams formats a fn's generic parameter list for the
// boundary writer. Empty list returns "" (no `<>` emitted). Per
// LANG_SPEC §19.5, runtime intrinsic stubs declare generics like
// `fn read<T: Pod>(p: RawPtr) -> T`; without this the boundary
// dropped `<T: Pod>` and the native checker saw `T` as undeclared,
// producing `ErrType` at every turbofish call site.
func selfhostGenericParams(gps []*ast.GenericParam) string {
	if len(gps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(gps))
	for _, gp := range gps {
		if gp == nil {
			continue
		}
		entry := gp.Name
		if len(gp.Constraints) > 0 {
			bounds := make([]string, 0, len(gp.Constraints))
			for _, c := range gp.Constraints {
				bounds = append(bounds, selfhostTypeSource(c))
			}
			entry += ": " + strings.Join(bounds, " + ")
		}
		parts = append(parts, entry)
	}
	if len(parts) == 0 {
		return ""
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func selfhostTypeSource(t ast.Type) string {
	switch x := t.(type) {
	case nil:
		return "()"
	case *ast.NamedType:
		name := strings.Join(x.Path, ".")
		if name == "" {
			name = "Invalid"
		}
		if len(x.Args) == 0 {
			return name
		}
		args := make([]string, 0, len(x.Args))
		for _, arg := range x.Args {
			args = append(args, selfhostTypeSource(arg))
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *ast.OptionalType:
		return selfhostTypeSource(x.Inner) + "?"
	case *ast.TupleType:
		elems := make([]string, 0, len(x.Elems))
		for _, elem := range x.Elems {
			elems = append(elems, selfhostTypeSource(elem))
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case *ast.FnType:
		params := make([]string, 0, len(x.Params))
		for _, param := range x.Params {
			params = append(params, selfhostTypeSource(param))
		}
		out := "fn(" + strings.Join(params, ", ") + ")"
		if x.ReturnType != nil {
			out += " -> " + selfhostTypeSource(x.ReturnType)
		}
		return out
	default:
		return "Invalid"
	}
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

// syntheticBuiltinSym returns a process-wide Symbol that stands in for a
// builtin when the native checker response names a type outside the current
// resolver scope.
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
