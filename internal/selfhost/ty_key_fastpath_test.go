package selfhost

import "testing"

func TestTyKeyArgsCommaSeparated(t *testing.T) {
	if got, want := tyKeyArgs([]int{1, 20, 300}), "1,20,300"; got != want {
		t.Fatalf("tyKeyArgs = %q, want %q", got, want)
	}
}

func TestTyKeyNamedMatchesExpected(t *testing.T) {
	if got, want := tyKeyNamed("List", []int{1, 2}), "N|List|1,2"; got != want {
		t.Fatalf("tyKeyNamed = %q, want %q", got, want)
	}
}

func TestTyKeyFnMatchesExpected(t *testing.T) {
	if got, want := tyKeyFn([]int{1, 2}, 3), "F|1,2|3"; got != want {
		t.Fatalf("tyKeyFn = %q, want %q", got, want)
	}
}

func TestTyLookupInternedReturnsExistingNewestMatch(t *testing.T) {
	arena := emptyTyArena()
	first := tyNamed(arena, "List", []int{tInt(arena)})
	second := tyNamed(arena, "List", []int{tInt(arena)})
	if first != second {
		t.Fatalf("tyNamed should intern identical keys: first=%d second=%d", first, second)
	}
}
