package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/osty/osty/internal/llvmgen"
)

// ErrLLVMNotImplemented marks source shapes that the early LLVM lowering slice
// cannot lower yet. The message is generated from the Osty-owned backend
// diagnostic policy.
var ErrLLVMNotImplemented = errors.New(llvmgen.UnsupportedBackendErrorMessage())

type llvmToolchain interface {
	CompileObject(ctx context.Context, irPath, objectPath, target string) error
	CompileCObject(ctx context.Context, sourcePath, objectPath, target string) error
	LinkBinary(ctx context.Context, objectPaths []string, binaryPath, target string) error
}

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
// fallback path.
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
	if err := tc.LinkBinary(ctx, linkObjects, out.Artifacts.Binary, req.Layout.Target); err != nil {
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
	if useNativeOwnedLLVMIR(features, emit) {
		if out, ok, warnings, err := TryEmitNativeOwnedLLVMIRText(entry, target); err != nil {
			return nil, warnings, err
		} else if ok {
			return out, warnings, nil
		}
	}
	opts := llvmgen.Options{
		PackageName: entry.PackageName,
		SourcePath:  entry.SourcePath,
		Source:      entry.Source,
		Target:      target,
		UseMIR:      useMIRBackend(features, emit),
		EmitGC:      true,
	}
	// IR is the sole input contract. The backend dispatcher never reaches
	// for entry.File — the AST is a front-end artifact that the LLVM
	// backend does not consume directly any more.
	//
	// After the native-owned fast path declines coverage, every emit
	// mode — raw `llvm-ir`, object, binary — prefers the MIR-direct
	// emitter. On MIR-emitter refusal we fall back automatically to
	// the HIR path, so MIR-first cannot reduce coverage.
	var (
		irOut  []byte
		genErr error
	)
	if opts.UseMIR && entry.MIR != nil {
		irOut, genErr = llvmgen.GenerateFromMIR(entry.MIR, opts)
		if genErr != nil && errors.Is(genErr, llvmgen.ErrUnsupported) {
			// OSTY_TRACE_MIR_FALLBACK is the probe env flag that prints
			// which shape made the MIR emitter refuse. Off by default
			// so user-facing `osty test` / `osty build` stays quiet —
			// wire it on when extending MIR coverage to see the
			// specific unsupported symbol/type hint the emitter
			// surfaces.
			if os.Getenv("OSTY_TRACE_MIR_FALLBACK") != "" {
				fmt.Fprintf(os.Stderr, "osty-mir-fallback: %s: %v\n", entry.SourcePath, genErr)
			}
			// MIR emitter refused — fall back to the HIR path.
			opts.UseMIR = false
			irOut, genErr = llvmgen.GenerateModule(entry.IR, opts)
		}
	} else {
		irOut, genErr = llvmgen.GenerateModule(entry.IR, opts)
	}
	warnings := append([]error(nil), entry.IRIssues...)
	if genErr == nil {
		return irOut, warnings, nil
	}
	diag := llvmgen.UnsupportedDiagnosticForError(genErr)
	return llvmgen.RenderSkeleton(
			entry.PackageName,
			entry.SourcePath,
			string(emit),
			target,
			errors.New(llvmgen.UnsupportedSummary(diag)),
		), append(warnings,
			errors.New(llvmgen.UnsupportedSummary(diag)),
			ErrLLVMNotImplemented,
		), ErrLLVMNotImplemented
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

func (clangToolchain) LinkBinary(ctx context.Context, objectPaths []string, binaryPath, target string) error {
	args := llvmgen.ClangLinkBinaryArgs(target, objectPaths, binaryPath)
	return runClang(ctx, "link binary", args)
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

// useMIRBackend reports whether LLVM emission should prefer the
// MIR-direct path. Every emit mode — raw `llvm-ir`, object, binary —
// is MIR-first; the dispatcher falls back to the HIR emitter on
// `ErrUnsupported`, so coverage never regresses.
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
