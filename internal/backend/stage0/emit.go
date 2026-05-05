package stage0

import (
	"errors"
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ir"
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
// Currently supported function shapes (each gated by an explicit check
// that returns ErrUnsupported with a precise reason):
//
//   - P1: `fn main() {}` — single block, zero instructions, ReturnTerm.
//   - P2a: `fn name() -> Int { N }` — non-main, single block, one
//     AssignInstr writing IntConst to the return local, ReturnTerm.
//
// Any other shape is rejected. Each P2x extension adds one case +
// regression test; surface drift is held back by the stage0 coverage
// gate planned for P3.
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
	if fn.Name == "main" {
		return emitTrivialMain(out, fn)
	}
	return emitIntLiteralReturn(out, fn)
}

func emitTrivialMain(out *strings.Builder, fn *mir.Function) error {
	if reason := trivialMainViolation(fn); reason != "" {
		return fmt.Errorf("%w: function %q: %s", ErrUnsupported, fn.Name, reason)
	}
	out.WriteString("define i32 @main() {\n")
	out.WriteString("entry:\n")
	out.WriteString("  ret i32 0\n")
	out.WriteString("}\n\n")
	return nil
}

// trivialMainViolation returns an empty string when `fn` matches the
// stage0 P1 subset (`fn main() {}`), otherwise a short reason string
// suitable for an ErrUnsupported diagnostic.
func trivialMainViolation(fn *mir.Function) string {
	if fn.Name != "main" {
		return "stage0 expected `main`; saw " + fn.Name
	}
	if len(fn.Params) != 0 {
		return "main has parameters"
	}
	if len(fn.Blocks) != 1 {
		return fmt.Sprintf("main has %d blocks; stage0 expects exactly 1", len(fn.Blocks))
	}
	bb := fn.Blocks[0]
	if bb == nil {
		return "main entry block is nil"
	}
	if len(bb.Instrs) != 0 {
		return fmt.Sprintf("main entry block has %d instructions; stage0 expects 0", len(bb.Instrs))
	}
	if _, ok := bb.Term.(*mir.ReturnTerm); !ok {
		return fmt.Sprintf("main terminator is %T; stage0 expects ReturnTerm", bb.Term)
	}
	return ""
}

// emitIntLiteralReturn handles `fn name() -> Int { N }` for non-main
// functions. The MIR shape is one block with a single AssignInstr
// writing an IntConst to the return local, followed by ReturnTerm.
func emitIntLiteralReturn(out *strings.Builder, fn *mir.Function) error {
	if reason, _ := intLiteralReturnViolation(fn); reason != "" {
		return fmt.Errorf("%w: function %q: %s", ErrUnsupported, fn.Name, reason)
	}
	_, value := intLiteralReturnViolation(fn)
	fmt.Fprintf(out, "define i64 @%s() {\n", fn.Name)
	out.WriteString("entry:\n")
	fmt.Fprintf(out, "  ret i64 %d\n", value)
	out.WriteString("}\n\n")
	return nil
}

// intLiteralReturnViolation returns "" + the constant value when `fn`
// matches the P2a subset; otherwise it returns a reason string and a
// zero value. Returning value alongside reason avoids re-walking the
// MIR after the validation pass.
func intLiteralReturnViolation(fn *mir.Function) (string, int64) {
	if !isPrimType(fn.ReturnType, ir.PrimInt) {
		return fmt.Sprintf("return type %s is not Int", primName(fn.ReturnType)), 0
	}
	if len(fn.Params) != 0 {
		return "function has parameters; stage0 P2a expects zero", 0
	}
	if len(fn.Blocks) != 1 {
		return fmt.Sprintf("function has %d blocks; stage0 P2a expects exactly 1", len(fn.Blocks)), 0
	}
	bb := fn.Blocks[0]
	if bb == nil {
		return "entry block is nil", 0
	}
	if len(bb.Instrs) != 1 {
		return fmt.Sprintf("entry block has %d instructions; stage0 P2a expects exactly 1", len(bb.Instrs)), 0
	}
	if _, ok := bb.Term.(*mir.ReturnTerm); !ok {
		return fmt.Sprintf("terminator is %T; stage0 P2a expects ReturnTerm", bb.Term), 0
	}
	assign, ok := bb.Instrs[0].(*mir.AssignInstr)
	if !ok {
		return fmt.Sprintf("instruction[0] is %T; stage0 P2a expects AssignInstr", bb.Instrs[0]), 0
	}
	if assign.Dest.Local != fn.ReturnLocal || assign.Dest.HasProjections() {
		return "AssignInstr destination is not the return local without projections", 0
	}
	use, ok := assign.Src.(*mir.UseRV)
	if !ok {
		return fmt.Sprintf("AssignInstr.Src is %T; stage0 P2a expects UseRV", assign.Src), 0
	}
	con, ok := use.Op.(*mir.ConstOp)
	if !ok {
		return fmt.Sprintf("UseRV.Op is %T; stage0 P2a expects ConstOp", use.Op), 0
	}
	intc, ok := con.Const.(*mir.IntConst)
	if !ok {
		return fmt.Sprintf("ConstOp.Const is %T; stage0 P2a expects IntConst", con.Const), 0
	}
	if !isPrimType(intc.Type(), ir.PrimInt) {
		return fmt.Sprintf("IntConst type %s is not Int", primName(intc.Type())), 0
	}
	return "", intc.Value
}

func isPrimType(t mir.Type, want ir.PrimKind) bool {
	prim, ok := t.(*ir.PrimType)
	if !ok || prim == nil {
		return false
	}
	return prim.Kind == want
}

func primName(t mir.Type) string {
	if t == nil {
		return "<nil>"
	}
	if prim, ok := t.(*ir.PrimType); ok && prim != nil {
		return prim.String()
	}
	return fmt.Sprintf("%T", t)
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
