package selfhost_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// TestInspectFromSourceProducesBaselineRecords locks the v1.11 inspect
// shape that landed 2026-04-24 (per SELFHOST_PORT_MATRIX.md): each
// top-level fn declaration + each `let` binding ident gets a record.
// The Go-side `check.InspectSource` and the CLI `osty check --inspect`
// both route through this entry point, so verifying it covers them
// both indirectly.
//
// **Not covered today** (dormant — `toolchain/inspect.osty` changes
// don't reach the frozen `internal/selfhost/generated.go` seed):
//   - for-loop body expressions
//   - struct field-chain (`a.b.c.d`) intermediate types
//   - method-call receiver expression types
//   - per-expression notes (generic instantiation markers)
//
// When the LLVM self-hosting flip propagates updates to the production
// native checker, these shapes can be added to inspect.osty and
// re-verified via assertions appended below.
func TestInspectFromSourceProducesBaselineRecords(t *testing.T) {
	src := []byte(`fn main() {
    let xs = [1, 2, 3]
    for x in xs {
        let y = x + 1
    }
}
`)
	recs := selfhost.InspectFromSource(src)
	if len(recs) == 0 {
		t.Fatalf("InspectFromSource returned no records for non-empty source")
	}

	var seenNodeKinds, seenRules []string
	for _, r := range recs {
		seenNodeKinds = append(seenNodeKinds, r.NodeKind)
		seenRules = append(seenRules, r.Rule)
	}
	// At minimum, the inspect output for a simple `fn main() { let xs = ... }`
	// should produce records for the fn declaration and the `xs` let
	// binding. The exact NodeKind / Rule strings are matrix-tracked (see
	// `toolchain/inspect.osty` rule constants); we just check the set is
	// non-empty and contains something resembling a fn decl + a binding.
	if !sliceHasContains(seenNodeKinds, "FnDecl") && !sliceHasContains(seenRules, "FN-DECL") {
		t.Errorf("no FnDecl / FN-DECL record observed; got NodeKinds=%v Rules=%v", seenNodeKinds, seenRules)
	}
	if !sliceHasContains(seenNodeKinds, "Binding") && !sliceHasContains(seenRules, "BIND") && !sliceHasContains(seenRules, "LET") {
		t.Errorf("no Binding / BIND / LET record observed; got NodeKinds=%v Rules=%v", seenNodeKinds, seenRules)
	}
}

func sliceHasContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// TestInspectFromSourceEmptyInputProducesNoRecords — defensive guard
// for the early-return path. Empty source → zero records, no panic.
func TestInspectFromSourceEmptyInputProducesNoRecords(t *testing.T) {
	recs := selfhost.InspectFromSource([]byte{})
	if len(recs) != 0 {
		t.Fatalf("empty source produced %d records, want 0", len(recs))
	}
}
