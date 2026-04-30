package lirproto

import (
	"strings"
	"testing"
)

func TestValidateAcceptsPhase1Module(t *testing.T) {
	i64 := IntType(64)
	mod := Module{
		SourcePath: "/tmp/valid.osty",
		TypeDefs:   []TypeDef{{Name: "%Pair", Body: "{ i64, i64 }"}},
		Globals:    []Global{{Name: "@counter", Type: i64, Init: "0"}},
		Functions: []Function{
			{
				Name:   "main",
				Return: i64,
				Blocks: []Block{
					{
						Label: "entry",
						Instrs: []Instr{
							Alloca{Dest: "%slot", Type: i64},
							Store{Value: Operand{Type: i64, Value: "1"}, Ptr: "%slot"},
							Load{Dest: "%v", Type: i64, Ptr: "%slot"},
						},
						Term: Ret{Type: i64, Value: "%v"},
					},
				},
			},
		},
	}

	if errs := Validate(mod); len(errs) != 0 {
		t.Fatalf("Validate() errors = %v, want none", errs)
	}
}

func TestValidateReportsStructuralIssues(t *testing.T) {
	badPtr := Type{LLVM: "i64", Class: TypePtr}
	i64 := IntType(64)
	mod := Module{
		TypeDefs: []TypeDef{{Name: "%Broken"}},
		Globals:  []Global{{Name: "@bad", Type: VoidType()}},
		Functions: []Function{
			{
				Name:   "broken",
				Return: VoidType(),
				Params: []Param{
					{Name: "%p", Type: badPtr},
				},
				Blocks: []Block{
					{
						Label: "entry",
						Instrs: []Instr{
							nil,
							Alloca{Dest: "%void", Type: VoidType()},
							Call{Return: i64},
						},
					},
					{
						Label: "entry",
						Term:  Ret{Value: "%dangling"},
					},
				},
			},
		},
	}

	errs := Validate(mod)
	want := []string{
		`type def[0] "%Broken": missing body`,
		`global[0] "@bad" type: void is not allowed here`,
		`function[0] @broken param[0]: class ptr has LLVM spelling "i64"`,
		`function[0] @broken block[0] instr[0]: nil instruction`,
		`function[0] @broken block[0] instr[1] type: void is not allowed here`,
		`function[0] @broken block[0] instr[2]: missing callee`,
		`function[0] @broken block[0]: missing terminator`,
		`function[0] @broken block[1] "entry": duplicate label first used by block[0]`,
		`function[0] @broken block[1] terminator: ret value "%dangling" without type`,
	}
	for _, needle := range want {
		if !containsError(errs, needle) {
			t.Fatalf("Validate() missing %q\nerrors:\n%s", needle, joinErrors(errs))
		}
	}
}

func containsError(errs []error, needle string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), needle) {
			return true
		}
	}
	return false
}

func joinErrors(errs []error) string {
	var b strings.Builder
	for _, err := range errs {
		b.WriteString("  ")
		b.WriteString(err.Error())
		b.WriteByte('\n')
	}
	return b.String()
}
