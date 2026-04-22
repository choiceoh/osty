package resolve

import (
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestRefsByIDMatchesRefs verifies that the NodeID-keyed projection of
// Refs / TypeRefs agrees with the pointer-keyed maps for every entry
// whose Ident / NamedType has a nonzero NodeID. This is the parity
// guarantee downstream passes rely on when migrating off *ast.Ident
// keys.
func TestRefsByIDMatchesRefs(t *testing.T) {
	src := []byte(`
fn greet(name: String) -> String {
	let prefix = "hi, "
	prefix + name
}

fn main() {
	let msg = greet("world")
	println(msg)
}
`)
	file, _ := parser.ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse failed")
	}
	res := File(file, NewPrelude())

	if res.RefsByID == nil || res.TypeRefsByID == nil {
		t.Fatal("RefsByID / TypeRefsByID not populated")
	}

	for ident, sym := range res.Refs {
		if ident == nil || ident.ID == 0 {
			continue
		}
		got, ok := res.RefsByID[ident.ID]
		if !ok {
			t.Errorf("RefsByID missing entry for %q (ID=%d)", ident.Name, ident.ID)
			continue
		}
		if got != sym {
			t.Errorf("RefsByID[%d] = %v, want %v", ident.ID, got, sym)
		}
	}
	for nt, sym := range res.TypeRefs {
		if nt == nil || nt.ID == 0 {
			continue
		}
		got, ok := res.TypeRefsByID[nt.ID]
		if !ok {
			t.Errorf("TypeRefsByID missing entry for ID=%d", nt.ID)
			continue
		}
		if got != sym {
			t.Errorf("TypeRefsByID[%d] = %v, want %v", nt.ID, got, sym)
		}
	}

	if len(res.RefsByID) == 0 {
		t.Error("expected at least one RefsByID entry for this source")
	}
}
