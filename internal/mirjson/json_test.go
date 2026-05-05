package mirjson

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

func TestModuleRoundTripPreservesMIREmissionShape(t *testing.T) {
	orig := roundTripFixtureMIR()
	encoded, err := FromModule(orig)
	if err != nil {
		t.Fatalf("FromModule: %v", err)
	}
	decoded, err := ToModule(encoded)
	if err != nil {
		t.Fatalf("ToModule: %v", err)
	}
	if got, want := mir.Print(decoded), mir.Print(orig); got != want {
		t.Fatalf("round-trip MIR print mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
	if got := decoded.Functions[0].Locals[0].Type.String(); got != "Int" {
		t.Fatalf("return local type = %q, want Int", got)
	}
	if !strings.Contains(decoded.Functions[0].Name, "main") {
		t.Fatalf("function name not preserved: %q", decoded.Functions[0].Name)
	}
	if decoded.Layouts.Structs["pkg.Point"] == nil {
		t.Fatalf("qualified struct layout key was not preserved: %#v", decoded.Layouts.Structs)
	}
}

func roundTripFixtureMIR() *mir.Module {
	fn := &mir.Function{Name: "main", ReturnType: ir.TInt}
	ret := fn.NewLocal("_return", ir.TInt, true, mir.Span{})
	fn.ReturnLocal = ret
	fn.Locals[ret].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	bb := fn.Block(entry)
	bb.Append(&mir.AssignInstr{
		Dest: mir.Place{Local: ret},
		Src:  &mir.UseRV{Op: &mir.ConstOp{Const: &mir.IntConst{Value: 7, T: ir.TInt}, T: ir.TInt}},
	})
	bb.SetTerminator(&mir.ReturnTerm{})
	layouts := mir.NewLayoutTable()
	layouts.Structs["pkg.Point"] = &mir.StructLayout{
		Name:    "Point",
		Mangled: "Point",
		Fields:  []mir.FieldLayout{{Index: 0, Name: "x", Type: ir.TInt}},
	}
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}, Layouts: layouts}
}
