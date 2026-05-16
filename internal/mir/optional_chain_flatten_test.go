package mir

import (
	"strings"
	"testing"
)

// TestLowerOptionalChainFlattensWhenFieldIsAlreadyOption covers
// chained `?.field` where an intermediate field is itself
// declared optional, e.g.:
//
//	struct Outer { inner: Inner? }
//	struct Inner { value: Int }
//
//	o?.inner?.value ?? -1   // o: Outer?
//
// The `?.` operator short-circuits on None and returns the field
// value as-is. When the field type is already `T?`, the chained
// `?.` MUST flatten — `o?.inner` is `Inner?`, not `Inner??` — so
// the next `?.value` projects through a single Option layer to
// produce `Int?`. Without flattening, MIR builds an extra Option
// wrapper whose None branch falls back to `none <error>` because
// the synthesised type can't be inferred two levels down.
//
// This complements the simpler `?.field` shape covered in
// `TestLowerOptionalStructPayloadProjectsThroughVariant` (PR
// #1818). See CLAUDE.md §A.6:
//
//	user?.address?.city ?? "unknown"
//
// canonical pattern — `address: Address?` field on a `user: User?`
// receiver returns the city as `String?` (single Option), not
// `String??`.
func TestLowerOptionalChainFlattensWhenFieldIsAlreadyOption(t *testing.T) {
	src := `struct Outer { inner: Inner? }
struct Inner { value: Int }

fn deep(o: Outer?) -> Int {
    o?.inner?.value ?? -1
}
`
	mirText := lowerSourceToMIR(t, src)
	// First-hop type: o?.inner has type Inner? (NOT Inner??).
	// Without the flatten, the intermediate slot was `Inner??`
	// and the second hop's None case carried `<error>`.
	if !strings.Contains(mirText, "Inner?  // _opt") {
		t.Errorf("expected first-hop slot typed `Inner?  // _opt` (flat), got:\n%s", mirText)
	}
	if strings.Contains(mirText, "Inner??  // _opt") {
		t.Errorf("first-hop slot incorrectly typed `Inner??  // _opt` — `?.` should flatten when field is already optional:\n%s", mirText)
	}
	// Final coalesce target is `Int?` (the second hop produces
	// Option<Int> by projecting `.value` on an `Inner` from
	// Some-arm; coalesce default `-1` is Int).
	if !strings.Contains(mirText, "Int?  // _coalesce") {
		t.Errorf("expected coalesce slot `Int?  // _coalesce`, got:\n%s", mirText)
	}
	if strings.Contains(mirText, "<error>") {
		t.Errorf("ErrTypeVal leaked into MIR:\n%s", mirText)
	}
}
