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
	if req.Entry.IR == nil {
		return nil, fmt.Errorf("llvm backend: missing lowered IR entry")
	}
	opts := llvmgen.Options{
		PackageName: req.Entry.PackageName,
		SourcePath:  req.Entry.SourcePath,
		Source:      req.Entry.Source,
		Target:      req.Layout.Target,
		UseMIR:      useMIRBackend(req.Features, req.Emit),
	}
	// IR is the sole input contract. The backend dispatcher never reaches
	// for req.Entry.File — the AST is a front-end artifact that the LLVM
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
	if opts.UseMIR && req.Entry.MIR != nil {
		irOut, genErr = llvmgen.GenerateFromMIR(req.Entry.MIR, opts)
		if genErr != nil && errors.Is(genErr, llvmgen.ErrUnsupported) {
			// MIR emitter refused — fall back to the HIR path.
			opts.UseMIR = false
			irOut, genErr = llvmgen.GenerateModule(req.Entry.IR, opts)
		}
	} else {
		irOut, genErr = llvmgen.GenerateModule(req.Entry.IR, opts)
	}
	if genErr == nil {
		if err := os.WriteFile(artifacts.LLVMIR, irOut, 0o644); err != nil {
			return nil, err
		}
		warnings := append([]error(nil), req.Entry.IRIssues...)
		out := &Result{
			Backend:   NameLLVM,
			Emit:      req.Emit,
			Artifacts: artifacts,
			Warnings:  warnings,
		}
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

	diag := llvmgen.UnsupportedDiagnosticForError(genErr)
	skeleton := llvmgen.RenderSkeleton(
		req.Entry.PackageName,
		req.Entry.SourcePath,
		string(req.Emit),
		req.Layout.Target,
		errors.New(llvmgen.UnsupportedSummary(diag)),
	)
	if err := os.WriteFile(artifacts.LLVMIR, skeleton, 0o644); err != nil {
		return nil, err
	}
	return &Result{
		Backend:   NameLLVM,
		Emit:      req.Emit,
		Artifacts: artifacts,
		Warnings: append(
			append([]error(nil), req.Entry.IRIssues...),
			errors.New(llvmgen.UnsupportedSummary(diag)),
			ErrLLVMNotImplemented,
		),
	}, ErrLLVMNotImplemented
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
	// `-pthread` enables _REENTRANT for the threading and channel
	// implementations bundled in `runtime/osty_runtime.c`. The matching
	// link flag is injected in `llvmClangLinkBinaryArgs`.
	args = append(args, "-std=c11", "-pthread", "-c", sourcePath, "-o", objectPath)
	return args
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
