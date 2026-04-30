// Package onb contains the Osty Native Backend dev/debug pipeline.
//
// ONB is intentionally not an LLVM replacement. It is the fast development
// backend described in ONB_DESIGN.md: MIR in, simple aarch64 native pipeline
// out, with LLVM remaining the release/reference backend.
package onb

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/osty/osty/internal/mir"
)

var (
	// ErrNotImplemented marks ONB phases whose boundary is wired but whose
	// implementation is still intentionally narrow.
	ErrNotImplemented = errors.New("onb phase slice is not implemented")

	// ErrUnsupportedTarget marks a target outside ONB's initial aarch64 scope.
	ErrUnsupportedTarget = errors.New("onb target is outside phase 1.0 scope")
)

// Request is the backend-owned half of a build request. Host-facing CLI and
// artifact layout stay in internal/backend; ONB starts from MIR.
type Request struct {
	Module       *mir.Module
	TargetTriple string
	EmitMode     string
	ObjectPath   string
	BinaryPath   string
}

// Target is the ONB-normalized target contract for phase 1.0.
type Target struct {
	Triple       string
	OS           string
	Arch         string
	ObjectFormat string
}

// Stage is one planned ONB pipeline stage.
type Stage struct {
	Name   string
	Status string
}

// Plan is the executable ONB phase boundary. Today it records the pipeline that
// must exist before emission can succeed; later phases replace the scaffolded
// stages with real data flowing between them.
type Plan struct {
	Target  Target
	Emit    string
	Program *Program
	Stages  []Stage
}

// ResolveTarget accepts an explicit target triple, or infers the host target
// when the triple is empty. ONB phase 1.0 is aarch64-only.
func ResolveTarget(triple string) (Target, error) {
	raw := strings.TrimSpace(triple)
	if raw == "" {
		raw = hostTriple()
	}
	lower := strings.ToLower(raw)
	if !isAArch64Triple(lower) {
		return Target{}, fmt.Errorf("%w: %q (want darwin/aarch64 or linux/aarch64)", ErrUnsupportedTarget, raw)
	}
	switch {
	case strings.Contains(lower, "darwin") || strings.Contains(lower, "apple"):
		return Target{Triple: raw, OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"}, nil
	case strings.Contains(lower, "linux"):
		return Target{Triple: raw, OS: "linux", Arch: "aarch64", ObjectFormat: "elf"}, nil
	default:
		return Target{}, fmt.Errorf("%w: %q (want darwin/aarch64 or linux/aarch64)", ErrUnsupportedTarget, raw)
	}
}

func hostTriple() string {
	arch := runtime.GOARCH
	if arch == "arm64" {
		arch = "aarch64"
	}
	switch runtime.GOOS {
	case "darwin":
		return arch + "-apple-darwin"
	case "linux":
		return arch + "-unknown-linux-gnu"
	default:
		return arch + "-" + runtime.GOOS
	}
}

func isAArch64Triple(lower string) bool {
	return strings.HasPrefix(lower, "aarch64-") || strings.HasPrefix(lower, "arm64-")
}

// BuildPlan validates the request and returns the phase 1.0 pipeline shape.
func BuildPlan(req Request) (*Plan, error) {
	if req.Module == nil {
		return nil, fmt.Errorf("onb: missing MIR module")
	}
	target, err := ResolveTarget(req.TargetTriple)
	if err != nil {
		return nil, err
	}
	program, err := LowerMIR(req.Module, target)
	if err != nil {
		return nil, err
	}
	objectWriterStatus := "planned"
	relocationStatus := "planned"
	if target.ObjectFormat == "mach-o" {
		objectWriterStatus = "implemented"
		relocationStatus = "implemented"
	}
	return &Plan{
		Target:  target,
		Emit:    req.EmitMode,
		Program: program,
		Stages: []Stage{
			{Name: "MIR consumer", Status: "implemented"},
			{Name: "4-pass minimal optimizer", Status: "planned"},
			{Name: "aarch64 LIR lowerer", Status: "implemented"},
			{Name: "aarch64 assembly renderer", Status: "implemented"},
			{Name: "linear register allocation", Status: "planned"},
			{Name: target.ObjectFormat + " object writer", Status: objectWriterStatus},
			{Name: "string/puts relocations", Status: relocationStatus},
			{Name: "DWARF .debug_line", Status: "planned"},
			{Name: "host linker", Status: "implemented"},
		},
	}, nil
}

// Compile returns the ONB plan plus the next broad pipeline stage that remains
// intentionally planned. The backend adapter drives concrete artifact emission.
func Compile(ctx context.Context, req Request) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := BuildPlan(req)
	if err != nil {
		return nil, err
	}
	return plan, fmt.Errorf("%w: next stage is %s", ErrNotImplemented, plan.NextPlannedStage())
}

// StageSummary returns a compact human-readable stage list for diagnostics.
func (p *Plan) StageSummary() string {
	if p == nil || len(p.Stages) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p.Stages))
	for _, stage := range p.Stages {
		if stage.Status == "" {
			parts = append(parts, stage.Name)
			continue
		}
		parts = append(parts, stage.Name+"="+stage.Status)
	}
	return strings.Join(parts, ", ")
}

// NextPlannedStage returns the first stage still waiting for implementation.
func (p *Plan) NextPlannedStage() string {
	if p == nil {
		return ""
	}
	for _, stage := range p.Stages {
		if stage.Status == "planned" {
			return stage.Name
		}
	}
	return ""
}
