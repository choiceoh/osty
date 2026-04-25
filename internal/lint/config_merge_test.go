package lint

import (
	"testing"
)

func TestMergeEmptyChildInheritsParent(t *testing.T) {
	parent := Config{
		Allow:   []string{"L0001", "naming_value"},
		Deny:    []string{"dead_code"},
		Exclude: []string{"vendor/**"},
	}
	child := Config{}

	merged := child.Merge(parent)

	assertStrings(t, merged.Allow, "L0001", "naming_value")
	// "dead_code" resolves to L0020.
	assertStrings(t, merged.Deny, "L0020")
	assertStrings(t, merged.Exclude, "vendor/**")
}

func TestMergeEmptyParentIsNoOp(t *testing.T) {
	parent := Config{}
	child := Config{
		Allow:   []string{"L0003"},
		Deny:    []string{"self_assign"},
		Exclude: []string{"gen/**"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Allow, "L0003")
	// "self_assign" resolves to L0042.
	assertStrings(t, merged.Deny, "L0042")
	assertStrings(t, merged.Exclude, "gen/**")
}

func TestMergeBothEmpty(t *testing.T) {
	merged := Config{}.Merge(Config{})

	if len(merged.Allow) != 0 {
		t.Errorf("expected empty Allow, got %v", merged.Allow)
	}
	if len(merged.Deny) != 0 {
		t.Errorf("expected empty Deny, got %v", merged.Deny)
	}
	if len(merged.Exclude) != 0 {
		t.Errorf("expected empty Exclude, got %v", merged.Exclude)
	}
}

func TestMergeAllowUnion(t *testing.T) {
	parent := Config{
		Allow: []string{"L0001"},
	}
	child := Config{
		Allow: []string{"L0040"},
	}

	merged := child.Merge(parent)

	// child-first, dedup.
	assertStrings(t, merged.Allow, "L0040", "L0001")
}

func TestMergeAllowUnionDedup(t *testing.T) {
	parent := Config{
		Allow: []string{"L0001", "L0002"},
	}
	child := Config{
		Allow: []string{"L0002", "L0040"},
	}

	merged := child.Merge(parent)

	// child-first, dedup: L0002 from child, L0040 from child, L0001 from parent.
	assertStrings(t, merged.Allow, "L0002", "L0040", "L0001")
}

func TestMergeDenyUnion(t *testing.T) {
	parent := Config{
		Deny: []string{"dead_code"},
	}
	child := Config{
		Deny: []string{"naming_type"},
	}

	merged := child.Merge(parent)

	// naming_type→L0030, dead_code→L0020; sorted concrete codes.
	assertStrings(t, merged.Deny, "L0020", "L0030")
}

func TestMergeChildAllowCancelsParentDeny(t *testing.T) {
	parent := Config{
		Deny: []string{"dead_code", "unused_let"},
	}
	child := Config{
		Allow: []string{"unused_let"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Allow, "unused_let")
	// unused_let→L0001 cancelled from deny; dead_code→L0020 survives.
	assertStrings(t, merged.Deny, "L0020")
}

func TestMergeChildAllowCancelsParentDenyForSameCode(t *testing.T) {
	parent := Config{
		Deny: []string{"unused_let"},
	}
	child := Config{
		Allow: []string{"unused_let"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Allow, "unused_let")
	// unused_let→L0001 cancelled → empty deny.
	if len(merged.Deny) != 0 {
		t.Errorf("expected empty Deny, got %v", merged.Deny)
	}
}

func TestMergeExcludesUnion(t *testing.T) {
	parent := Config{
		Exclude: []string{"vendor/**", "third_party/**"},
	}
	child := Config{
		Exclude: []string{"gen/**", "vendor/**"},
	}

	merged := child.Merge(parent)

	// Union with dedup; parent patterns first.
	assertStrings(t, merged.Exclude, "vendor/**", "third_party/**", "gen/**")
}

func TestMergeExcludesOnlyParent(t *testing.T) {
	parent := Config{
		Exclude: []string{"vendor/**"},
	}
	child := Config{}

	merged := child.Merge(parent)

	assertStrings(t, merged.Exclude, "vendor/**")
}

func TestMergeExcludesOnlyChild(t *testing.T) {
	parent := Config{}
	child := Config{
		Exclude: []string{"gen/**"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Exclude, "gen/**")
}

func TestMergeFullScenario(t *testing.T) {
	parent := Config{
		Allow:   []string{"L0001"},
		Deny:    []string{"dead_code", "self_assign", "shadow"},
		Exclude: []string{"vendor/**"},
	}
	child := Config{
		Allow:   []string{"unused_let"},
		Deny:    []string{"naming_type"},
		Exclude: []string{"gen/**"},
	}

	merged := child.Merge(parent)

	// Allow: child-first union = ["unused_let", "L0001"]
	assertStrings(t, merged.Allow, "unused_let", "L0001")

	// Deny names merged: ["naming_type", "dead_code", "self_assign", "shadow"]
	// Expand: naming_type→L0030, dead_code→L0020, self_assign→L0042, shadow→L0010
	// child Allow: unused_let→L0001 — no overlap with deny codes
	// Result: sorted concrete codes.
	assertStrings(t, merged.Deny, "L0010", "L0020", "L0030", "L0042")

	// Exclude: parent-first union.
	assertStrings(t, merged.Exclude, "vendor/**", "gen/**")
}

func TestMergeDoesNotAliasSlices(t *testing.T) {
	parent := Config{
		Allow:   []string{"L0001"},
		Deny:    []string{"L0020"},
		Exclude: []string{"vendor/**"},
	}
	child := Config{
		Allow: []string{"L0040"},
	}

	merged := child.Merge(parent)

	// Mutating the merged result should not affect parent or child.
	merged.Allow[0] = "MUTATED"
	merged.Exclude = append(merged.Exclude, "extra/**")

	if parent.Allow[0] != "L0001" {
		t.Errorf("parent.Allow was mutated: %q", parent.Allow[0])
	}
	if child.Allow[0] != "L0040" {
		t.Errorf("child.Allow was mutated: %q", child.Allow[0])
	}
	if len(parent.Exclude) != 1 {
		t.Errorf("parent.Exclude was mutated: %v", parent.Exclude)
	}
}

func TestMergeWildcardAllowCancelsAllDeny(t *testing.T) {
	parent := Config{
		Deny: []string{"dead_code", "self_assign"},
	}
	child := Config{
		Allow: []string{"all"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Allow, "all")
	// "all" expands to {"*": true}; denyCodes has entries but wildcard
	// allow cancels everything → Deny=[].
	if len(merged.Deny) != 0 {
		t.Errorf("expected empty Deny (wildcard \"all\" cancels all), got %v", merged.Deny)
	}
}

func TestMergeCategoryAllowCancelsMatchingDeny(t *testing.T) {
	parent := Config{
		Deny: []string{"L0001", "L0002", "L0003"},
	}
	child := Config{
		Allow: []string{"unused"},
	}

	merged := child.Merge(parent)

	// "unused" resolves to {L0001,L0002,L0003,L0004,L0005,L0006,L0007}.
	// All 3 parent deny codes are cancelled → Deny becomes empty.
	if len(merged.Deny) != 0 {
		t.Errorf("expected empty Deny (category \"unused\" cancels L0001-L0003), got %v", merged.Deny)
	}
	assertStrings(t, merged.Allow, "unused")
}

func TestMergeCategoryDenyMinusSpecificAllow(t *testing.T) {
	parent := Config{
		Deny: []string{"unused"},
	}
	child := Config{
		Allow: []string{"unused_let"},
	}

	merged := child.Merge(parent)

	// parent Deny: "unused" → {L0001,L0002,L0003,L0004,L0005,L0006,L0007,L0008}
	// child Allow: "unused_let" → {L0001}
	// L0001 is cancelled; remaining deny codes are L0002-L0008 sorted.
	assertStrings(t, merged.Deny,
		"L0002", "L0003", "L0004", "L0005", "L0006", "L0007", "L0008",
	)

	// Allow is child-only (parent has no Allow).
	assertStrings(t, merged.Allow, "unused_let")
}

// Wildcard deny in parent must round-trip through Merge so Apply still
// sees a wildcard deny set. Storing the wildcard as "*" would break
// because expandCodeSet only recognizes "lint" and "all" as wildcards.
func TestMergeWildcardDenyRoundTrips(t *testing.T) {
	parent := Config{Deny: []string{"all"}}
	merged := Config{}.Merge(parent)

	denySet := expandCodeSet(merged.Deny)
	if !denySet["*"] {
		t.Errorf("wildcard deny lost in merge: merged.Deny=%v expandsTo=%v", merged.Deny, denySet)
	}
}

// --- helpers ---

func assertStrings(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("at index %d: expected %q, got %q (full: %v)", i, v, got[i], got)
		}
	}
}
