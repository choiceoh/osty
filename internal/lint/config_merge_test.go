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
	assertStrings(t, merged.Deny, "dead_code")
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
	assertStrings(t, merged.Deny, "self_assign")
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
		Deny: []string{"self_assign"},
	}

	merged := child.Merge(parent)

	// child-first, dedup.
	assertStrings(t, merged.Deny, "self_assign", "dead_code")
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
	// unused_let removed from Deny because child allows it.
	assertStrings(t, merged.Deny, "dead_code")
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
	// completely cancelled.
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

	// Deny: union of parent+child, minus child.Allow resolved codes.
	// child.Allow "unused_let" resolves to {"L0001"}, which does not
	// cancel any of parent's deny codes (dead_code→L0020, self_assign→L0042,
	// shadow→shadowed_binding→L0010). So all survive:
	// child-first: ["naming_type", "dead_code", "self_assign", "shadow"]
	assertStrings(t, merged.Deny, "naming_type", "dead_code", "self_assign", "shadow")

	// Exclude: parent-first union.
	assertStrings(t, merged.Exclude, "vendor/**", "gen/**")
}

func TestMergeDoesNotAliasSlices(t *testing.T) {
	parent := Config{
		Allow:   []string{"L0001"},
		Deny:    []string{"dead_code"},
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
	// "all" expands to "*" which matches everything → all deny codes cancelled.
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

	// "unused" resolves to {L0001, L0002, L0003, L0004, L0005, L0006, L0007}
	// which covers all three parent deny codes → Deny becomes empty.
	if len(merged.Deny) != 0 {
		t.Errorf("expected empty Deny (category \"unused\" cancels L0001-L0003), got %v", merged.Deny)
	}
	assertStrings(t, merged.Allow, "unused")
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
