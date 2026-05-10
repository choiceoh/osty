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
	"github.com/osty/osty/internal/semanticdb"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/spanid"
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

func (c cachedNativeChecker) CheckPackageStructured(input api.PackageCheckInput) (api.CheckResult, error) {
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

func packageCheckFingerprint(input api.PackageCheckInput) []byte {
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
			fmt.Fprintf(h, "  fn=%s owner=%s recv=%s recvRepr=%s ret=%s retRepr=%s params=%d reprParams=%d\n",
				fn.Name, fn.Owner, fn.ReceiverType, checkFingerprintTypeRepr(fn.ReceiverTypeRepr), fn.ReturnType, checkFingerprintTypeRepr(fn.ReturnTypeRepr), len(fn.ParamTypes), len(fn.ParamTypeReprs))
			for i, pt := range fn.ParamTypes {
				fmt.Fprintf(h, "    p%d=%s\n", i, pt)
			}
			for i := range fn.ParamTypeReprs {
				fmt.Fprintf(h, "    pr%d=%s\n", i, fn.ParamTypeReprs[i].String())
			}
			for _, bound := range fn.GenericBounds {
				fmt.Fprintf(h, "    bound=%s:%s:%s\n", bound.TyParam, bound.InterfaceType, checkFingerprintTypeRepr(bound.InterfaceTypeRepr))
			}
		}
		for _, td := range imp.TypeDecls {
			fmt.Fprintf(h, "  type=%s kind=%s generics=%d\n", td.Name, td.Kind, len(td.Generics))
			for _, bound := range td.GenericBounds {
				fmt.Fprintf(h, "    typeBound=%s:%s:%s\n", bound.TyParam, bound.InterfaceType, checkFingerprintTypeRepr(bound.InterfaceTypeRepr))
			}
		}
		for _, field := range imp.Fields {
			fmt.Fprintf(h, "  field=%s/%s type=%s typeRepr=%s exported=%t default=%t\n",
				field.Owner, field.Name, field.TypeName, checkFingerprintTypeRepr(field.Type), field.Exported, field.HasDefault)
		}
		for _, v := range imp.Variants {
			fmt.Fprintf(h, "  variant=%s/%s fields=%d reprFields=%d\n", v.Owner, v.Name, len(v.FieldTypes), len(v.FieldTypeReprs))
			for i := range v.FieldTypeReprs {
				fmt.Fprintf(h, "    vr%d=%s\n", i, v.FieldTypeReprs[i].String())
			}
		}
		for _, alias := range imp.Aliases {
			fmt.Fprintf(h, "  alias=%s target=%s targetRepr=%s generics=%d\n",
				alias.Name, alias.Target, checkFingerprintTypeRepr(alias.TargetRepr), len(alias.Generics))
		}
		for _, ext := range imp.InterfaceExts {
			fmt.Fprintf(h, "  ifaceExt=%s iface=%s ifaceRepr=%s\n",
				ext.Owner, ext.InterfaceType, checkFingerprintTypeRepr(ext.InterfaceTypeRepr))
		}
	}
	sum := h.Sum(nil)
	return sum[:]
}

func checkFingerprintTypeRepr(repr *api.TypeRepr) string {
	if repr == nil {
		return ""
	}
	return repr.String()
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
	res.EnsureStableIDs()
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

func applyNativeFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider, privileged bool, populateLegacy bool) {
	applySelfhostFileResult(result, file, rr, src, stdlib, privileged, populateLegacy)
}

func applySelfhostFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider, privileged bool, populateLegacy bool) {
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
	result.Diags = append(result.Diags, nativeCheckerDiagsForCheckedSource(checkedSrc, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	result.SemanticDB = semanticdb.FromCheck(checked)
	if populateLegacy {
		overlaySelfhostResult(result, checkedSrc, checked)
	}
}

func applyNativePackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool, populateLegacy bool) {
	applySelfhostPackageResult(result, pkg, pr, ws, stdlib, privileged, populateLegacy)
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool, populateLegacy bool) {
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
	result.Diags = append(result.Diags, nativeCheckerDiagsForCheckedSource(src, checked, policy)...)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked, policy)
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	attachSemanticDB(result, pr, checked)
	if populateLegacy {
		overlaySelfhostResult(result, src, checked)
	}
}

func applyNativeWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider, populateLegacy bool) {
	applySelfhostWorkspaceResults(ws, resolved, results, stdlib, populateLegacy)
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider, populateLegacy bool) {
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
			applySelfhostPackageResult(j.result, j.pkg, j.pr, ws, stdlib, j.privileged, populateLegacy)
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
				runSelfhostPackageResultLocked(j.result, j.pkg, j.pr, ws, stdlib, j.privileged, populateLegacy, &mu)
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
func runSelfhostPackageResultLocked(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider, privileged bool, populateLegacy bool, mu *sync.Mutex) {
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
	diags := nativeCheckerDiagsForCheckedSource(src, checked, policy)
	telemetry := nativeCheckerTelemetry(checked, policy)

	mu.Lock()
	defer mu.Unlock()
	result.Diags = append(result.Diags, diags...)
	result.NativeCheckerTelemetry = telemetry
	result.NativeCheckResult = cloneNativeCheckResult(checked)
	attachSemanticDB(result, pr, checked)
	if populateLegacy {
		overlaySelfhostResult(result, src, checked)
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

func nativeCheckerDiagsForCheckedSource(src selfhostCheckedSource, checked api.CheckResult, policy nativeDiagPolicy) []*diag.Diagnostic {
	out := make([]*diag.Diagnostic, 0, len(checked.Diagnostics))
	for _, d := range checked.Diagnostics {
		if shouldSuppressNativeDiag(d, policy) {
			continue
		}
		if converted := convertNativeDiagForCheckedSource(src, d); converted != nil {
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
			Primary(fileStartSpan(src.source), "native checker summary").
			Note(fmt.Sprintf(
				"native checker accepted %d of %d assignment/return/call checks",
				summary.Accepted,
				summary.Assignments,
			)).
			Build(),
	)
	return out
}

func convertNativeDiagForCheckedSource(src selfhostCheckedSource, d api.CheckDiagnosticRecord) *diag.Diagnostic {
	seg, relStart, relEnd, ok := nativeDiagSegment(src, d)
	if !ok {
		return convertNativeDiag(src.source, d)
	}
	mapped := d
	mapped.Start = relStart
	mapped.End = relEnd
	if mapped.File == "" {
		mapped.File = seg.path
	}
	if mapped.SourceFileID == "" && seg.sourceID != "" {
		mapped.SourceFileID = string(seg.sourceID)
	}
	if mapped.SpanID == "" && mapped.SourceFileID != "" {
		mapped.SpanID = string(spanid.SpanIDFor(spanid.SourceFileID(mapped.SourceFileID), mapped.Start, mapped.End))
	}
	if seg.base != 0 {
		mapped.Provenance = append(mapped.Provenance, api.SpanProvenanceRecord{
			Kind:         string(spanid.ProvenanceSelfhostShift),
			SourceFileID: mapped.SourceFileID,
			SpanID:       mapped.SpanID,
			Detail:       fmt.Sprintf("base:%d", seg.base),
		})
	}
	return convertNativeDiag(seg.source, mapped)
}

func nativeDiagSegment(src selfhostCheckedSource, d api.CheckDiagnosticRecord) (selfhostFileSegment, int, int, bool) {
	for _, seg := range src.files {
		if d.File != "" && seg.path != "" && d.File != seg.path {
			continue
		}
		if len(seg.source) == 0 {
			continue
		}
		relStart := d.Start - seg.base
		relEnd := d.End - seg.base
		if relStart < 0 || relStart > len(seg.source) {
			continue
		}
		if relEnd < relStart {
			relEnd = relStart
		}
		if relEnd > len(seg.source) {
			relEnd = len(seg.source)
		}
		return seg, relStart, relEnd, true
	}
	return selfhostFileSegment{}, 0, 0, false
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
// `*diag.Diagnostic`. Modern records carry checker-owned display
// positions; older subprocesses that only send byte offsets still fall
// back to a source scan.
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
	b = b.Primary(nativeDiagSpanWithIdentity(src, d), "")
	for _, note := range d.Notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		b = b.Note(note)
	}
	return b.Build()
}

func nativeDiagSpan(src []byte, d api.CheckDiagnosticRecord) diag.Span {
	if d.StartLine > 0 && d.StartColumn > 0 {
		endLine := d.EndLine
		endColumn := d.EndColumn
		if endLine <= 0 {
			endLine = d.StartLine
		}
		if endColumn <= 0 {
			endColumn = d.StartColumn
		}
		return diag.Span{
			Start: token.Pos{Line: d.StartLine, Column: d.StartColumn, Offset: d.Start},
			End:   token.Pos{Line: endLine, Column: endColumn, Offset: d.End},
		}
	}
	return byteRangeSpan(src, d.Start, d.End)
}

func nativeDiagSpanWithIdentity(src []byte, d api.CheckDiagnosticRecord) diag.Span {
	span := nativeDiagSpan(src, d)
	if d.SourceFileID != "" {
		span = diag.StampSpanSourceFileID(span, spanid.SourceFileID(d.SourceFileID))
	}
	if d.SpanID != "" {
		span.ID = spanid.SpanID(d.SpanID)
	}
	for _, p := range d.Provenance {
		span.Provenance = spanid.AppendProvenance(span.Provenance, spanid.Provenance{
			Kind:         spanid.ProvenanceKind(p.Kind),
			SourceFileID: spanid.SourceFileID(p.SourceFileID),
			SpanID:       spanid.SpanID(p.SpanID),
			Detail:       p.Detail,
		})
	}
	return span
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
			source:    append([]byte(nil), src...),
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
			file:          file,
			source:        append([]byte(nil), src...),
			scope:         scope,
			refs:          refs,
			base:          0,
			sourceMap:     canonicalMap,
			nativeNodeIDs: true,
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
			path:      pf.Path,
			source:    append([]byte(nil), src...),
			sourceID:  checkSourceFileID(pf),
			scope:     pf.FileScope,
			refs:      pf.RefsByID,
			base:      base,
			sourceMap: pf.CheckerSourceMap(),
		})
	}
	return selfhostCheckedSource{source: b.Bytes(), files: files}
}

func checkSourceFileID(pf *resolve.PackageFile) spanid.SourceFileID {
	if pf == nil {
		return ""
	}
	if pf.SourceFileID != "" {
		return pf.SourceFileID
	}
	return spanid.SourceFileIDFor(pf.Path)
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
		if use.IsScoped {
			dotPath = strings.Join(use.ScopedBase, ".")
		}
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
		if use.IsScoped {
			alias = lastPathSegment(dotPath)
		} else if alias == "" && len(use.Path) > 0 {
			alias = use.Path[len(use.Path)-1]
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		writeSelfhostPackageImport(b, alias, target)
	}
}

func lastPathSegment(path string) string {
	if path == "" {
		return ""
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
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
