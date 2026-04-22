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
	irOut, warnings, genErr := generateLLVMIR(req.Entry, req.Layout.Target, req.Features, req.Emit)
	if err := os.WriteFile(artifacts.LLVMIR, irOut, 0o644); err != nil {
		return nil, err
	}
	out := &Result{
		Backend:   NameLLVM,
		Emit:      req.Emit,
		Artifacts: artifacts,
		Warnings:  warnings,
	}
	if genErr == nil {
		if !llvmgen.NeedsObjectArtifact(req.Emit.String()) {
			return out, nil
		}
		tc := b.llvmToolchain()
		if err := tc.CompileObject(ctx, artifacts.LLVMIR, artifacts.Object, req.Layout.Target); err != nil {
			return out, err
		}
		if !llvmgen.NeedsBinaryArtifact(req.Emit.String()) {
			return out, nil
		}
		if artifacts.Binary == "" {
			return out, fmt.Errorf("%s", llvmgen.MissingBinaryArtifactMessage())
		}
		runtimeObject, err := ensureLocalGCRuntimeObject(ctx, tc, artifacts, req.Layout.Target)
		if err != nil {
			return out, err
		}
		linkObjects := []string{artifacts.Object}
		if runtimeObject != "" {
			linkObjects = append(linkObjects, runtimeObject)
		}
		if err := tc.LinkBinary(ctx, linkObjects, artifacts.Binary, req.Layout.Target); err != nil {
			return out, err
		}
		return out, nil
	}
	return out, genErr
}

// EmitLLVMIRText runs the LLVM lowering pipeline for one prepared entry and
// returns the textual IR bytes directly, without creating artifact paths.
func EmitLLVMIRText(entry Entry, target string, features []string) ([]byte, []error, error) {
	return generateLLVMIR(entry, target, features, EmitLLVMIR)
}

func generateLLVMIR(entry Entry, target string, features []string, emit EmitMode) ([]byte, []error, error) {
	if entry.IR == nil {
		return nil, nil, fmt.Errorf("llvm backend: missing lowered IR entry")
	}
	opts := llvmgen.Options{
		PackageName: entry.PackageName,
		SourcePath:  entry.SourcePath,
		Source:      entry.Source,
		Target:      target,
		UseMIR:      useMIRBackend(features, emit),
	}
	// IR is the sole input contract. The backend dispatcher never reaches
	// for entry.File — the AST is a front-end artifact that the LLVM
	// backend does not consume directly any more.
	//
	// MIR-first dispatch now defaults on the raw `llvm-ir` emission
	// path. Requests can opt back into the legacy HIR→AST bridge with
	// the `legacy-llvmgen` feature, or opt further in with
	// `mir-backend` on object/binary emission while parity continues to
	// grow. On MIR-emitter refusal we still fall back automatically, so
	// enabling the new path cannot reduce coverage.
	var (
		irOut  []byte
		genErr error
	)
	if opts.UseMIR && entry.MIR != nil {
		irOut, genErr = llvmgen.GenerateFromMIR(entry.MIR, opts)
		if genErr != nil && errors.Is(genErr, llvmgen.ErrUnsupported) {
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
	args = append(args, "-std=c11", "-c", sourcePath, "-o", objectPath)
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

// featureEnabled reports whether `name` appears in the features
// slice.
func featureEnabled(features []string, name string) bool {
	for _, f := range features {
		if f == name {
			return true
		}
	}
	return false
}

// useMIRBackend reports whether LLVM emission should prefer the
// MIR-direct path. Raw `llvm-ir` emission is MIR-first by default;
// callers can opt back into the legacy HIR→AST bridge with the
// `legacy-llvmgen` feature, or opt further in on object/binary
// emission with `mir-backend` while parity stabilizes.
func useMIRBackend(features []string, emit EmitMode) bool {
	if featureEnabled(features, "legacy-llvmgen") {
		return false
	}
	if featureEnabled(features, "mir-backend") {
		return true
	}
	return emit == EmitLLVMIR
}
