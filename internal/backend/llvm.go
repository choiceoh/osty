package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmgen"
)

// ErrLLVMNotImplemented marks source shapes that the early LLVM lowering slice
// cannot lower yet. The message is generated from the Osty-owned backend
// diagnostic policy.
var ErrLLVMNotImplemented = errors.New(llvmgen.UnsupportedBackendErrorMessage())

type llvmToolchain interface {
	CompileObject(ctx context.Context, irPath, objectPath, target string) error
	CompileCObject(ctx context.Context, sourcePath, objectPath, target string) error
	LinkBinary(ctx context.Context, objectPaths []string, binaryPath, target string, linkLibraries []string) error
}

type llvmDispatchRoute string

const (
	llvmDispatchUnsupportedPreflight llvmDispatchRoute = "unsupported-preflight"
	llvmDispatchNativeOwned          llvmDispatchRoute = "native-owned"
	llvmDispatchMIRDirect            llvmDispatchRoute = "mir-direct"
)

// LLVMBackend emits textual LLVM IR and can drive a host LLVM-compatible
// toolchain for object/binary artifacts.
type LLVMBackend struct {
	toolchain llvmToolchain
}

func (LLVMBackend) Name() Name { return NameLLVM }

func (b LLVMBackend) Emit(ctx context.Context, req Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateEmit(NameLLVM, req.Emit); err != nil {
		return nil, err
	}
	irOut, warnings, genErr := generateLLVMIR(req.Entry, req.Layout.Target, req.Features, req.Emit)
	if genErr == nil {
		return b.emitPrebuiltIR(ctx, req, irOut, warnings)
	}
	out, err := b.preparePrebuiltIRResult(req, irOut, warnings)
	if err != nil {
		return nil, err
	}
	return out, genErr
}

// EmitLLVMIRText runs the LLVM lowering pipeline for one prepared entry and
// returns the textual IR bytes directly, without creating artifact paths.
func EmitLLVMIRText(entry Entry, target string, features []string) ([]byte, []error, error) {
	return generateLLVMIR(entry, target, features, EmitLLVMIR)
}

// TryEmitNativeOwnedLLVMIRText runs only the native-owned llvmgen fast path
// mirrored from toolchain/llvmgen.osty. It returns ok=false when the entry's
// IR module is still outside that slice and the caller should choose a
// broader MIR backend path.
func TryEmitNativeOwnedLLVMIRText(entry Entry, target string) ([]byte, bool, []error, error) {
	if entry.IR == nil {
		return nil, false, nil, fmt.Errorf("llvm backend: missing lowered IR entry")
	}
	out, ok, err := llvmgen.TryGenerateNativeOwnedModule(entry.IR, llvmgen.Options{
		PackageName: entry.PackageName,
		SourcePath:  entry.SourcePath,
		Source:      entry.Source,
		Target:      target,
	})
	warnings := append([]error(nil), entry.IRIssues...)
	if err != nil || !ok {
		return out, ok, warnings, err
	}
	return out, true, warnings, nil
}

// EmitPrebuiltLLVMIR materializes already-generated LLVM IR into the standard
// backend artifact layout and optionally compiles/links it for object/binary
// requests.
func EmitPrebuiltLLVMIR(ctx context.Context, req Request, irOut []byte, warnings []error) (*Result, error) {
	return LLVMBackend{}.emitPrebuiltIR(ctx, req, irOut, warnings)
}

func (b LLVMBackend) emitPrebuiltIR(ctx context.Context, req Request, irOut []byte, warnings []error) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := b.preparePrebuiltIRResult(req, irOut, warnings)
	if err != nil {
		return nil, err
	}
	if !llvmgen.NeedsObjectArtifact(req.Emit.String()) {
		return out, nil
	}
	tc := b.llvmToolchain()
	if err := tc.CompileObject(ctx, out.Artifacts.LLVMIR, out.Artifacts.Object, req.Layout.Target); err != nil {
		return out, err
	}
	if !llvmgen.NeedsBinaryArtifact(req.Emit.String()) {
		return out, nil
	}
	if out.Artifacts.Binary == "" {
		return out, fmt.Errorf("%s", llvmgen.MissingBinaryArtifactMessage())
	}
	runtimeObject, err := ensureLocalGCRuntimeObject(ctx, tc, out.Artifacts, req.Layout.Target)
	if err != nil {
		return out, err
	}
	linkObjects := []string{out.Artifacts.Object}
	if runtimeObject != "" {
		linkObjects = append(linkObjects, runtimeObject)
	}
	if err := tc.LinkBinary(ctx, linkObjects, out.Artifacts.Binary, req.Layout.Target, req.LinkLibraries); err != nil {
		return out, err
	}
	return out, nil
}

func (b LLVMBackend) preparePrebuiltIRResult(req Request, irOut []byte, warnings []error) (*Result, error) {
	if err := ValidateEmit(NameLLVM, req.Emit); err != nil {
		return nil, err
	}
	artifacts := req.Artifacts(NameLLVM)
	if err := os.MkdirAll(artifacts.OutputDir, 0o755); err != nil {
		return nil, err
	}
	if artifacts.RuntimeDir != "" {
		if err := os.MkdirAll(artifacts.RuntimeDir, 0o755); err != nil {
			return nil, err
		}
	}
	if artifacts.LLVMIR == "" {
		return nil, fmt.Errorf("llvm backend: missing LLVM IR artifact path")
	}
	if err := os.WriteFile(artifacts.LLVMIR, irOut, 0o644); err != nil {
		return nil, err
	}
	return &Result{
		Backend:   NameLLVM,
		Emit:      req.Emit,
		Artifacts: artifacts,
		Warnings:  append([]error(nil), warnings...),
	}, nil
}

func generateLLVMIR(entry Entry, target string, features []string, emit EmitMode) ([]byte, []error, error) {
	if entry.IR == nil {
		return nil, nil, fmt.Errorf("llvm backend: missing lowered IR entry")
	}
	warnings := append([]error(nil), entry.IRIssues...)
	opts := llvmgen.Options{
		PackageName: entry.PackageName,
		SourcePath:  entry.SourcePath,
		Source:      entry.Source,
		Target:      target,
		UseMIR:      useMIRBackend(features, emit),
		EmitGC:      true,
	}
	capabilities := newLLVMDispatchCapabilityMatrix(entry, opts, features, emit)
	if diag, row, ok := capabilities.PreflightBlockingDiagnostic(); ok {
		traceLLVMDispatch("%s rejected %s: %s %s (%s)", llvmDispatchUnsupportedPreflight, entry.SourcePath, diag.Code, diag.Kind, row.ID)
		return renderUnsupportedLLVMIR(entry, target, emit, warnings, diag, llvmDispatchUnsupportedPreflight)
	}
	if capabilities.CanRoute(llvmDispatchNativeOwned) {
		traceLLVMDispatch("%s try package=%s source=%s emit=%s target=%s", llvmDispatchNativeOwned, entry.PackageName, entry.SourcePath, emit, target)
		if out, ok, warnings, err := TryEmitNativeOwnedLLVMIRText(entry, target); err != nil {
			traceLLVMDispatch("%s error: %v", llvmDispatchNativeOwned, err)
			return nil, warnings, err
		} else if ok {
			traceLLVMDispatch("%s covered package=%s source=%s", llvmDispatchNativeOwned, entry.PackageName, entry.SourcePath)
			return out, warnings, nil
		}
		traceLLVMDispatch("%s declined package=%s source=%s", llvmDispatchNativeOwned, entry.PackageName, entry.SourcePath)
	} else {
		traceLLVMDispatch("%s skipped package=%s source=%s", llvmDispatchNativeOwned, entry.PackageName, entry.SourcePath)
	}
	// IR is the sole input contract. The backend dispatcher never reaches
	// for entry.File — the AST is a front-end artifact that the LLVM
	// backend does not consume directly any more.
	//
	// After the native-owned fast path declines coverage, every emit
	// mode — raw `llvm-ir`, object, binary — enters the MIR-direct
	// emitter. MIR refusal now surfaces as the normal unsupported
	// skeleton diagnostic; the LLVM backend no longer retries the
	// legacy HIR bridge behind the user's back.
	route := capabilities.DispatchRoute()
	traceLLVMDispatch("%s emit package=%s source=%s emit=%s target=%s", route, entry.PackageName, entry.SourcePath, emit, target)
	if diag, row, ok := capabilities.RouteBlockingDiagnostic(route); ok {
		traceLLVMDispatch("%s unsupported: %s %s (%s)", route, diag.Code, diag.Kind, row.Subject)
		return renderUnsupportedLLVMIR(entry, target, emit, warnings, diag, route)
	}
	irOut, genErr := emitLLVMFallback(route, entry, opts)
	if genErr == nil {
		traceLLVMDispatch("%s succeeded package=%s source=%s", route, entry.PackageName, entry.SourcePath)
		return irOut, warnings, nil
	}
	traceLLVMDispatch("%s unsupported: %v", route, genErr)
	diag := llvmgen.UnsupportedDiagnosticForError(genErr)
	return renderUnsupportedLLVMIR(entry, target, emit, warnings, diag, route)
}

func llvmFallbackDispatchRoute(opts llvmgen.Options, entry Entry) llvmDispatchRoute {
	return NewLLVMCapabilityMatrix(entry, opts).DispatchRoute()
}

func emitLLVMFallback(route llvmDispatchRoute, entry Entry, opts llvmgen.Options) ([]byte, error) {
	return llvmgen.GenerateFromMIR(entry.MIR, opts)
}

func renderUnsupportedLLVMIR(entry Entry, target string, emit EmitMode, warnings []error, diag llvmgen.UnsupportedDiagnostic, route llvmDispatchRoute) ([]byte, []error, error) {
	summary := llvmUnsupportedTraceSummary(diag, route)
	skeleton := llvmgen.RenderSkeleton(
		entry.PackageName,
		entry.SourcePath,
		string(emit),
		target,
		errors.New(summary),
	)
	warnings = append(warnings,
		errors.New(summary),
		ErrLLVMNotImplemented,
	)
	return skeleton, warnings, ErrLLVMNotImplemented
}

func llvmUnsupportedTraceSummary(diag llvmgen.UnsupportedDiagnostic, route llvmDispatchRoute) string {
	summary := llvmgen.UnsupportedSummary(diag)
	if route == "" {
		return summary
	}
	return summary + "; backend-route: " + string(route)
}

func traceLLVMDispatch(format string, args ...any) {
	if !llvmBackendTraceEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "backend trace: llvm "+format+"\n", args...)
}

func llvmBackendTraceEnabled() bool {
	switch os.Getenv("OSTY_BACKEND_TRACE") {
	case "", "0", "false", "off":
		return false
	default:
		return true
	}
}

func hasInjectedStdlibBodies(mod *ir.Module) bool {
	if mod == nil {
		return false
	}
	for _, decl := range mod.Decls {
		fn, ok := decl.(*ir.FnDecl)
		if !ok || fn == nil {
			continue
		}
		if strings.HasPrefix(fn.Name, "osty_std_") && fn.Body != nil {
			return true
		}
	}
	return false
}

func (b LLVMBackend) llvmToolchain() llvmToolchain {
	if b.toolchain != nil {
		return b.toolchain
	}
	return clangToolchain{}
}

type clangToolchain struct{}

func (clangToolchain) CompileObject(ctx context.Context, irPath, objectPath, target string) error {
	args := llvmgen.ClangCompileObjectArgs(target, irPath, objectPath)
	return runClang(ctx, "compile object", args)
}

func (clangToolchain) CompileCObject(ctx context.Context, sourcePath, objectPath, target string) error {
	args := clangCompileCObjectArgs(target, sourcePath, objectPath)
	return runClang(ctx, "compile runtime", args)
}

func (clangToolchain) LinkBinary(ctx context.Context, objectPaths []string, binaryPath, target string, linkLibraries []string) error {
	args := llvmgen.ClangLinkBinaryArgs(target, objectPaths, binaryPath)
	args = append(args, clangLinkLibraryArgs(linkLibraries)...)
	args = append(args, clangPlatformRuntimeLinkArgs(target)...)
	return runClang(ctx, "link binary", args)
}

func clangPlatformRuntimeLinkArgs(target string) []string {
	var args []string
	if !isWindowsTarget(target) {
		args = append(args, "-lm")
	}
	if isDarwinTarget(target) {
		args = append(args, "-framework", "Security", "-framework", "CoreFoundation")
	}
	if isWindowsTarget(target) {
		args = append(args, "-ladvapi32")
	}
	return args
}

func clangLinkLibraryArgs(libraries []string) []string {
	if len(libraries) == 0 {
		return nil
	}
	args := make([]string, 0, len(libraries))
	for _, lib := range libraries {
		lib = strings.TrimSpace(lib)
		if lib == "" {
			continue
		}
		if strings.HasPrefix(lib, "-") ||
			strings.ContainsAny(lib, `/\`) ||
			strings.HasSuffix(lib, ".a") ||
			strings.HasSuffix(lib, ".so") ||
			strings.HasSuffix(lib, ".dylib") ||
			strings.HasSuffix(lib, ".lib") {
			args = append(args, lib)
			continue
		}
		args = append(args, "-l"+lib)
	}
	return args
}

func clangCompileCObjectArgs(target, sourcePath, objectPath string) []string {
	args := []string{}
	if target != "" {
		args = append(args, "-target", target)
	}
	// `-pthread` enables _REENTRANT for the POSIX threading surface in
	// `runtime/osty_runtime.c`. The Windows branch of the runtime uses
	// Win32 primitives directly (SRWLOCK / CONDITION_VARIABLE / ...)
	// and does not need pthread, so omit the flag for Windows targets;
	// clang-cl / clang-msvc would otherwise warn or fail on it.
	if !isWindowsTarget(target) {
		args = append(args, "-pthread")
	}
	// -O3 + -flto=thin keeps the GC/scheduler runtime in the same tier
	// as the IR compile path (see llvmClangCompileObjectArgs). The
	// thinLTO bitcode lets the linker inline hot primitive runtime
	// helpers (osty_rt_list_get_i64, osty_rt_list_len, …) at every IR
	// call site — without it every `xs[i]` in user code pays a real
	// cross-TU function call and the List<Int>-heavy osty-vs-go
	// workloads (quicksort, matmul, lane_route) stay 10-50x slower
	// than Go for no reason other than missing inlining.
	args = append(args, "-O3", "-flto=thin", "-std=c11", "-c", sourcePath, "-o", objectPath)
	return args
}

// isWindowsTarget reports whether the LLVM target triple names a
// Windows OS component. Empty triples mean "host"; the caller passes
// the runtime.GOOS-derived default in that case.
func isWindowsTarget(target string) bool {
	return strings.Contains(target, "windows")
}

func isDarwinTarget(target string) bool {
	if target == "" {
		return runtime.GOOS == "darwin"
	}
	return strings.Contains(target, "darwin") || strings.Contains(target, "apple")
}

func runClang(ctx context.Context, action string, args []string) error {
	path, err := exec.LookPath("clang")
	if err != nil {
		return fmt.Errorf("%s: %w", llvmgen.MissingClangMessage(), err)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	combined, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(combined))
	if msg == "" {
		msg = "<no output>"
	}
	command := "clang " + strings.Join(args, " ")
	return fmt.Errorf("%s: %w", llvmgen.ClangFailureMessage(action, command, msg), err)
}

// useMIRBackend reports whether LLVM emission should use the
// MIR-direct path. Every emit mode — raw `llvm-ir`, object, binary —
// is MIR-owned once the native-owned fast path declines coverage.
func useMIRBackend(_ []string, _ EmitMode) bool {
	return true
}

func useNativeOwnedLLVMIR(features []string, emit EmitMode) bool {
	for _, f := range features {
		if f == "mir-backend" {
			return false
		}
	}
	switch emit {
	case EmitLLVMIR, EmitObject, EmitBinary:
		return true
	default:
		return false
	}
}

// UseNativeOwnedLLVMIR reports whether the backend's default dispatch would
// prefer the native-owned llvmgen fast path for the given feature set and emit
// mode.
func UseNativeOwnedLLVMIR(features []string, emit EmitMode) bool {
	return useNativeOwnedLLVMIR(features, emit)
}
