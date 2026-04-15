package types

import (
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestIdenticalBuiltinNamedAcrossPreludes(t *testing.T) {
	a := &Named{
		Sym: &resolve.Symbol{Name: "Result", Kind: resolve.SymBuiltin},
		Args: []Type{
			String,
			&Named{Sym: &resolve.Symbol{Name: "Error", Kind: resolve.SymBuiltin}},
		},
	}
	b := &Named{
		Sym: &resolve.Symbol{Name: "Result", Kind: resolve.SymBuiltin},
		Args: []Type{
			String,
			&Named{Sym: &resolve.Symbol{Name: "Error", Kind: resolve.SymBuiltin}},
		},
	}
	if !Identical(a, b) {
		t.Fatalf("builtin named types from separate preludes should compare identical: %s vs %s", a, b)
	}
}
