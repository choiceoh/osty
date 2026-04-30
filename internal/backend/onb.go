package backend

import (
	"context"
	"fmt"
	"os"

	"github.com/osty/osty/internal/onb"
)

// ErrONBNotImplemented marks ONB requests that reached the dev-backend phase
// boundary before object emission exists.
var ErrONBNotImplemented = onb.ErrNotImplemented

type onbLinker interface {
	LinkBinary(ctx context.Context, objectPaths []string, binaryPath, target string) error
}

// ONBBackend is the LLVM-complementary dev/debug backend. It consumes MIR and
// emits native aarch64 objects directly; LLVM remains the reference/release
// backend.
type ONBBackend struct {
	linker onbLinker
}

func (ONBBackend) Name() Name { return NameONB }

func (b ONBBackend) Emit(ctx context.Context, req Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateEmit(NameONB, req.Emit); err != nil {
		return nil, err
	}
	artifacts := req.Artifacts(NameONB)
	if err := os.MkdirAll(artifacts.OutputDir, 0o755); err != nil {
		return nil, err
	}
	plan, err := onb.BuildPlan(onb.Request{
		Module:       req.Entry.MIR,
		TargetTriple: req.Layout.Target,
		EmitMode:     req.Emit.String(),
		ObjectPath:   artifacts.Object,
		BinaryPath:   artifacts.Binary,
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
	if err != nil {
		return result, err
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
	if err := b.onbLinker().LinkBinary(ctx, []string{artifacts.Object}, artifacts.Binary, req.Layout.Target); err != nil {
		return result, err
	}
	return result, nil
}

func (b ONBBackend) onbLinker() onbLinker {
	if b.linker != nil {
		return b.linker
	}
	return clangToolchain{}
}
