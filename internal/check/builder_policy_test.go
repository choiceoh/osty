package check

import "testing"

func TestClassifyBuilderDeriveListsTracksRequiredAndOverride(t *testing.T) {
	info := classifyBuilderDeriveLists(
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

	override := classifyBuilderDeriveLists(
		[]string{"x"},
		[]bool{true},
		[]bool{false},
		[]string{"builder"},
	)
	if override.Derivable {
		t.Fatalf("derivable = true, want false when custom builder() exists")
	}

	blocked := classifyBuilderDeriveLists(
		[]string{"x", "hidden"},
		[]bool{true, false},
		[]bool{false, false},
		nil,
	)
	if blocked.Derivable {
		t.Fatalf("derivable = true, want false when private field lacks default")
	}
}
