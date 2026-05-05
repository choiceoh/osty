package stage0

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
)

func trivialMainModule() *mir.Module {
	return &mir.Module{
		Package: "main",
		Functions: []*mir.Function{
			{
				Name:        "main",
				ReturnType:  ir.TUnit,
				ReturnLocal: 0,
				Locals: []*mir.Local{
					{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
				},
				Entry: 0,
				Blocks: []*mir.BasicBlock{
					{ID: 0, Term: &mir.ReturnTerm{}},
				},
			},
		},
		Layouts: mir.NewLayoutTable(),
	}
}

func TestStage0EmitsTrivialMain(t *testing.T) {
	t.Parallel()
	got, err := EmitMIR(trivialMainModule(), llvmabi.Options{PackageName: "main", Target: "x86_64-unknown-linux-gnu"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"stage0 bootstrap LLVM IR",
		"; package: main",
		"target triple = \"x86_64-unknown-linux-gnu\"",
		"define i32 @main()",
		"ret i32 0",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, s)
		}
	}
}

func TestStage0RejectsNilModule(t *testing.T) {
	t.Parallel()
	_, err := EmitMIR(nil, llvmabi.Options{})
	if err == nil {
		t.Fatal("expected error for nil module")
	}
}

func TestStage0RejectsModuleWithoutMain(t *testing.T) {
	t.Parallel()
	module := &mir.Module{
		Package:   "main",
		Functions: nil,
		Layouts:   mir.NewLayoutTable(),
	}
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsNonMainFunction(t *testing.T) {
	t.Parallel()
	module := trivialMainModule()
	module.Functions[0].Name = "helper"
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "stage0 P1 only emits `main`") {
		t.Fatalf("err message = %q, want stage0 P1 reason", err.Error())
	}
}

func TestStage0RejectsMainWithParameters(t *testing.T) {
	t.Parallel()
	module := trivialMainModule()
	module.Functions[0].Params = []mir.LocalID{0}
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsMainWithInstructions(t *testing.T) {
	t.Parallel()
	module := trivialMainModule()
	module.Functions[0].Blocks[0].Instrs = []mir.Instr{
		&mir.AssignInstr{},
	}
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsIntrinsicDeclaration(t *testing.T) {
	t.Parallel()
	module := trivialMainModule()
	module.Functions[0].IsIntrinsic = true
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0FallsBackPackageName(t *testing.T) {
	t.Parallel()
	module := trivialMainModule()
	module.Package = ""
	got, err := EmitMIR(module, llvmabi.Options{PackageName: "demo"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	if !strings.Contains(string(got), "; package: demo") {
		t.Fatalf("expected fallback package name from opts:\n%s", got)
	}
}
