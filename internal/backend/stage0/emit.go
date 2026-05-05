package stage0

import (
	"errors"
	"fmt"
	"strings"

	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
)

// ErrUnsupported marks a MIR shape outside the stage0 bootstrap subset.
// Callers (the backend dispatcher) treat this as "stage0 declines"
// rather than a hard build failure — the dispatcher then surfaces the
// usual unsupported skeleton diagnostic with route `stage0`.
var ErrUnsupported = errors.New("stage0: MIR shape outside bootstrap subset")

// EmitMIR lowers `module` to textual LLVM IR for the small MVP subset
// stage0 currently covers. It is invoked only when
// `backend.IsOstySelfMissing` reported the canonical decline; production
// builds with a working `osty-self` route through the LIR Proto
// subprocess instead and never reach this entry.
//
// P1 subset: a single function `fn main() {}` (Unit return, single
// block, no instructions, ReturnTerm). Anything else returns
// ErrUnsupported with a precise reason so future phases (P2…) can
// extend coverage one pattern at a time.
func EmitMIR(module *mir.Module, opts llvmabi.Options) ([]byte, error) {
	if module == nil {
		return nil, fmt.Errorf("stage0: nil MIR module")
	}
	var out strings.Builder
	pkg := packageNameFor(module, opts)
	target := llvmabi.CanonicalLLVMTarget(opts.Target)

	out.WriteString("; Osty stage0 bootstrap LLVM IR\n")
	out.WriteString("; package: " + pkg + "\n")
	if target != "" {
		out.WriteString(fmt.Sprintf("target triple = %q\n", target))
	}
	out.WriteString("\n")

	emittedMain := false
	for _, fn := range module.Functions {
		if fn == nil {
			continue
		}
		if err := emitFunction(&out, fn); err != nil {
			return nil, err
		}
		if fn.Name == "main" {
			emittedMain = true
		}
	}
	if !emittedMain {
		return nil, fmt.Errorf("%w: module has no `main` function", ErrUnsupported)
	}
	return []byte(out.String()), nil
}

func emitFunction(out *strings.Builder, fn *mir.Function) error {
	if fn.IsIntrinsic {
		return fmt.Errorf("%w: intrinsic declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.IsExternal {
		return fmt.Errorf("%w: external declaration %q", ErrUnsupported, fn.Name)
	}
	if reason := trivialMainViolation(fn); reason != "" {
		return fmt.Errorf("%w: function %q: %s", ErrUnsupported, fn.Name, reason)
	}
	out.WriteString("define i32 @main() {\n")
	out.WriteString("entry:\n")
	out.WriteString("  ret i32 0\n")
	out.WriteString("}\n")
	return nil
}

// trivialMainViolation returns an empty string when `fn` matches the
// stage0 P1 subset (`fn main() {}`), otherwise a short reason string
// suitable for an ErrUnsupported diagnostic.
func trivialMainViolation(fn *mir.Function) string {
	if fn.Name != "main" {
		return "stage0 P1 only emits `main`; saw " + fn.Name
	}
	if len(fn.Params) != 0 {
		return "main has parameters"
	}
	if len(fn.Blocks) != 1 {
		return fmt.Sprintf("main has %d blocks; stage0 P1 expects exactly 1", len(fn.Blocks))
	}
	bb := fn.Blocks[0]
	if bb == nil {
		return "main entry block is nil"
	}
	if len(bb.Instrs) != 0 {
		return fmt.Sprintf("main entry block has %d instructions; stage0 P1 expects 0", len(bb.Instrs))
	}
	if _, ok := bb.Term.(*mir.ReturnTerm); !ok {
		return fmt.Sprintf("main terminator is %T; stage0 P1 expects ReturnTerm", bb.Term)
	}
	return ""
}

func packageNameFor(module *mir.Module, opts llvmabi.Options) string {
	if module != nil && module.Package != "" {
		return module.Package
	}
	if opts.PackageName != "" {
		return opts.PackageName
	}
	return "main"
}
