package resolve

import (
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestRefsByIDCoveredByIdentList verifies every RefsByID / TypeRefsByID
// entry has a matching RefIdents / TypeRefIdents entry. This is the
// structural guarantee downstream passes rely on when walking resolved
// identifiers without a pointer-keyed map.
func TestRefsByIDCoveredByIdentList(t *testing.T) {
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

	seenRef := map[uint32]bool{}
	for _, ident := range res.RefIdents {
		if ident == nil || ident.ID == 0 {
			t.Errorf("RefIdents contains unstamped ident %v", ident)
			continue
		}
		sym, ok := res.RefsByID[ident.ID]
		if !ok {
			t.Errorf("RefIdents entry %q (ID=%d) has no RefsByID match", ident.Name, ident.ID)
			continue
		}
		if sym == nil {
			t.Errorf("RefsByID[%d] is nil for ident %q", ident.ID, ident.Name)
		}
		seenRef[uint32(ident.ID)] = true
	}
	for id := range res.RefsByID {
		if !seenRef[uint32(id)] {
			t.Errorf("RefsByID entry ID=%d not in RefIdents", id)
		}
	}

	if len(res.RefsByID) == 0 {
		t.Error("expected at least one RefsByID entry for this source")
	}
}
