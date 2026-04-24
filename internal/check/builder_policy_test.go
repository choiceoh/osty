package check

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestClassifyBuilderDeriveListsTracksRequiredAndOverride(t *testing.T) {
	info := ClassifyBuilderDeriveLists(
		[]string{"x", "hidden", "name"},
		[]bool{true, false, true},
		[]bool{false, true, true},
		nil,
	)
	if !info.Derivable {
		t.Fatalf("derivable = false, want true when private fields have defaults")
	}
	if got, want := len(info.Required), 1; got != want || info.Required[0] != "x" {
		t.Fatalf("required = %v, want [x]", info.Required)
	}

	override := ClassifyBuilderDeriveLists(
		[]string{"x"},
		[]bool{true},
		[]bool{false},
		[]string{"builder"},
	)
	if override.Derivable {
		t.Fatalf("derivable = true, want false when custom builder() exists")
	}

	methodNamedBuilder := ClassifyBuilderDerive(&ast.StructDecl{
		Fields: []*ast.Field{
			{Name: "x", Pub: true},
		},
		Methods: []*ast.FnDecl{
			{Name: "builder", Recv: &ast.Receiver{}},
		},
	})
	if !methodNamedBuilder.Derivable {
		t.Fatalf("derivable = false, want true when only a method named builder(self) exists")
	}

	blocked := ClassifyBuilderDeriveLists(
		[]string{"x", "hidden"},
		[]bool{true, false},
		[]bool{false, false},
		nil,
	)
	if blocked.Derivable {
		t.Fatalf("derivable = true, want false when private field lacks default")
	}
}
