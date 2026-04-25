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

func TestMergeChildOverridesParentAllow(t *testing.T) {
	parent := Config{
		Allow: []string{"L0001", "L0002", "L0003"},
	}
	child := Config{
		Allow: []string{"L0040"},
	}

	merged := child.Merge(parent)

	// Child's Allow replaces parent's entirely.
	assertStrings(t, merged.Allow, "L0040")
}

func TestMergeChildOverridesParentDeny(t *testing.T) {
	parent := Config{
		Deny: []string{"dead_code", "self_assign"},
	}
	child := Config{
		Deny: []string{"naming_type"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Deny, "naming_type")
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

func TestMergeFullOverride(t *testing.T) {
	parent := Config{
		Allow:   []string{"L0001", "L0002"},
		Deny:    []string{"dead_code"},
		Exclude: []string{"vendor/**"},
	}
	child := Config{
		Allow:   []string{"L0040"},
		Deny:    []string{"self_compare"},
		Exclude: []string{"gen/**"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Allow, "L0040")
	assertStrings(t, merged.Deny, "self_compare")
	// Exclude is unioned.
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

func TestMergeExcludeDedupPreservesOrder(t *testing.T) {
	parent := Config{
		Exclude: []string{"a/**", "b/**", "c/**"},
	}
	child := Config{
		Exclude: []string{"c/**", "d/**", "a/**"},
	}

	merged := child.Merge(parent)

	// "a/**" and "c/**" appear from parent first; "d/**" is new from child.
	assertStrings(t, merged.Exclude, "a/**", "b/**", "c/**", "d/**")
}

func TestMergeWildcardAliasInChild(t *testing.T) {
	parent := Config{
		Allow: []string{"L0001", "L0002", "L0003"},
	}
	child := Config{
		Allow: []string{"all"},
	}

	merged := child.Merge(parent)

	// Child's "all" wildcard replaces the parent's specific codes.
	assertStrings(t, merged.Allow, "all")
}

func TestMergeCategoryAliasInChild(t *testing.T) {
	parent := Config{
		Deny: []string{"L0020", "L0021"},
	}
	child := Config{
		Deny: []string{"naming"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Deny, "naming")
}

// --- Cross-field blocking tests ---
//
// When the child has ANY lint field set (Allow, Deny, or Exclude),
// the child's Allow and Deny are used verbatim — even when empty.
// This prevents a parent's Allow or Deny from leaking through when
// the child only sets the other field (or only sets Exclude).

func TestMergeChildAllowBlocksParentDeny(t *testing.T) {
	// Child sets Allow only → childHasLint=true.
	// Child's Allow=["L0040"] is used verbatim.
	// Parent's Deny=["dead_code"] is NOT inherited (child's Deny is empty).
	parent := Config{
		Allow: []string{"L0001"},
		Deny:  []string{"dead_code"},
	}
	child := Config{
		Allow: []string{"L0040"},
	}

	merged := child.Merge(parent)

	assertStrings(t, merged.Allow, "L0040")
	if len(merged.Deny) != 0 {
		t.Errorf("expected empty Deny (child is non-empty, so parent Deny must NOT be inherited), got %v", merged.Deny)
	}
	if len(merged.Exclude) != 0 {
		t.Errorf("expected empty Exclude, got %v", merged.Exclude)
	}
}

func TestMergeChildDenyBlocksParentAllow(t *testing.T) {
	// Child sets Deny only → childHasLint=true.
	// Child's Deny=["naming_type"] is used verbatim.
	// Parent's Allow=["L0001"] is NOT inherited (child's Allow is empty).
	parent := Config{
		Allow: []string{"L0001"},
		Deny:  []string{"dead_code"},
	}
	child := Config{
		Deny: []string{"naming_type"},
	}

	merged := child.Merge(parent)

	if len(merged.Allow) != 0 {
		t.Errorf("expected empty Allow (child is non-empty, so parent Allow must NOT be inherited), got %v", merged.Allow)
	}
	assertStrings(t, merged.Deny, "naming_type")
	if len(merged.Exclude) != 0 {
		t.Errorf("expected empty Exclude, got %v", merged.Exclude)
	}
}

func TestMergeChildExcludeBlocksParentAllowDeny(t *testing.T) {
	// Child sets Exclude only → childHasLint=true.
	// Child's Allow and Deny are both empty and stay empty
	// (parent's Allow and Deny are NOT inherited).
	// Exclude is always unioned.
	parent := Config{
		Allow:   []string{"L0001"},
		Deny:    []string{"dead_code"},
		Exclude: []string{"vendor/**"},
	}
	child := Config{
		Exclude: []string{"gen/**"},
	}

	merged := child.Merge(parent)

	if len(merged.Allow) != 0 {
		t.Errorf("expected empty Allow (child is non-empty, so parent Allow must NOT be inherited), got %v", merged.Allow)
	}
	if len(merged.Deny) != 0 {
		t.Errorf("expected empty Deny (child is non-empty, so parent Deny must NOT be inherited), got %v", merged.Deny)
	}
	assertStrings(t, merged.Exclude, "vendor/**", "gen/**")
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
