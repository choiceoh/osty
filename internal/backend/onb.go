package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/osty/osty/internal/onb"
)

// ErrONBNotImplemented marks ONB requests that reached the dev-backend phase
// boundary before object emission exists.
var ErrONBNotImplemented = onb.ErrNotImplemented

type onbLinker interface {
	LinkBinary(ctx context.Context, objectPaths []string, binaryPath, target string, linkLibraries []string) error
}

// ONBBackend is the LLVM-complementary dev/debug backend. It consumes MIR and
// emits native aarch64 objects directly; LLVM remains the reference/release
// backend.
//
// To make `osty run --backend onb` actually usable on a developer's daily
// dev loop the backend silently falls back to LLVMBackend whenever ONB's MIR
// coverage rejects a shape. The fallback only fires for emit modes that
// produce a runnable artifact (object / binary) — `--emit asm` keeps its
// hard-fail behaviour because the user is asking specifically for ONB's
// readable assembly. Setting OSTY_ONB_STRICT=1 disables the fallback so
// cross-validation harnesses see the raw rejection.
type ONBBackend struct {
	linker       onbLinker
	llvmFallback Backend
	logSink      io.Writer
}

func (ONBBackend) Name() Name { return NameONB }

func (b ONBBackend) Emit(ctx context.Context, req Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateEmit(NameONB, req.Emit); err != nil {
		return nil, err
	}

	started := time.Now()
	timingOn := onb.TimingEnabled()
	ev := onb.TimingEvent{}
	if timingOn {
		target, _ := onb.ResolveTarget(req.Layout.Target)
		ev.Target = target.Triple
		defer func() {
			ev.Elapsed = time.Since(started)
			onb.LogTiming(b.timingSink(), ev)
		}()
	}

	result, err := b.emitNative(ctx, req)
	if err == nil {
		ev.Path = onb.TimingPathNative
		ev.BinaryAt = result.Artifacts.Binary
		return result, nil
	}
	if !b.shouldFallback(req, err) {
		ev.Path = onb.TimingPathError
		ev.Reason = onb.UnsupportedShapeReason(err)
		return result, err
	}
	fallback, fallbackErr := b.runLLVMFallback(ctx, req, err)
	ev.Reason = onb.UnsupportedShapeReason(err)
	if fallbackErr != nil {
		ev.Path = onb.TimingPathError
	} else {
		ev.Path = onb.TimingPathLLVMFallback
	}
	return fallback, fallbackErr
}

// emitNative runs the native ONB pipeline. It returns ErrUnsupportedShape (or
// a wrapping error) when the dev backend cannot lower the MIR — the public
// Emit method then decides whether to fall back to LLVM.
func (b ONBBackend) emitNative(ctx context.Context, req Request) (*Result, error) {
	artifacts := req.Artifacts(NameONB)
	if err := os.MkdirAll(artifacts.OutputDir, 0o755); err != nil {
		return nil, err
	}
	plan, planErr := onb.BuildPlan(onb.Request{
		Module:       req.Entry.MIR,
		TargetTriple: req.Layout.Target,
		EmitMode:     req.Emit.String(),
		ObjectPath:   artifacts.Object,
		BinaryPath:   artifacts.Binary,
		SourcePath:   req.Entry.SourcePath,
		PackageName:  req.Entry.PackageName,
	})
	warnings := append([]error(nil), req.Entry.MIRIssues...)
	if plan != nil {
		warnings = append(warnings, fmt.Errorf("onb phase plan for %s/%s: %s", plan.Target.OS, plan.Target.Arch, plan.StageSummary()))
	}
	result := &Result{
		Backend:   NameONB,
		Emit:      req.Emit,
		Artifacts: artifacts,
		Warnings:  warnings,
	}
	if planErr != nil {
		return result, planErr
	}
	asm, err := onb.RenderAssembly(plan.Program)
	if err != nil {
		return result, err
	}
	if artifacts.Assembly != "" {
		if err := os.WriteFile(artifacts.Assembly, asm, 0o644); err != nil {
			return result, err
		}
	}
	if req.Emit == EmitASM {
		return result, nil
	}
	if req.Emit == EmitObject || req.Emit == EmitBinary {
		object, err := onb.EmitObject(plan.Program)
		if err != nil {
			return result, err
		}
		if artifacts.Object != "" {
			if err := os.WriteFile(artifacts.Object, object, 0o644); err != nil {
				return result, err
			}
		}
		if req.Emit == EmitObject {
			return result, nil
		}
	}
	if artifacts.Binary == "" {
		return result, fmt.Errorf("onb backend: missing binary artifact path")
	}
	if err := b.onbLinker().LinkBinary(ctx, []string{artifacts.Object}, artifacts.Binary, req.Layout.Target, req.LinkLibraries); err != nil {
		return result, err
	}
	return result, nil
}

// shouldFallback decides whether an ONB lowering failure should silently
// delegate to LLVM. We only fall back for emit modes that produce a runnable
// artifact, and only when the failure is an "unsupported MIR shape" sentinel.
// OSTY_ONB_STRICT=1 disables the fallback entirely.
func (b ONBBackend) shouldFallback(req Request, err error) bool {
	if err == nil {
		return false
	}
	if onb.StrictMode() {
		return false
	}
	if req.Emit != EmitObject && req.Emit != EmitBinary {
		return false
	}
	return errors.Is(err, onb.ErrUnsupportedShape)
}

// runLLVMFallback drives LLVM with the same Request, then attaches a warning
// describing why ONB declined so callers can surface the reason without
// burying it in stderr. Result.Backend on the fallback path reflects LLVM —
// honest about what actually built the artifact — and the binary path lives
// under .osty/out/llvm/, which is what `osty run` will execute.
func (b ONBBackend) runLLVMFallback(ctx context.Context, req Request, onbErr error) (*Result, error) {
	llvm := b.llvmBackend()
	result, err := llvm.Emit(ctx, req)
	note := fmt.Errorf("onb fallback: lowering delegated to llvm (%s)", onb.UnsupportedShapeReason(onbErr))
	if result == nil {
		return result, err
	}
	result.Warnings = append([]error{note}, result.Warnings...)
	return result, err
}

func (b ONBBackend) onbLinker() onbLinker {
	if b.linker != nil {
		return b.linker
	}
	return clangToolchain{}
}

func (b ONBBackend) llvmBackend() Backend {
	if b.llvmFallback != nil {
		return b.llvmFallback
	}
	return LLVMBackend{}
}

func (b ONBBackend) timingSink() io.Writer {
	if b.logSink != nil {
		return b.logSink
	}
	return os.Stderr
}
