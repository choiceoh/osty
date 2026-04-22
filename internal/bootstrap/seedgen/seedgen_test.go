package seedgen

import "testing"

func TestNormalizeGeneratedOutputRewritesInterpolatedLenCalls(t *testing.T) {
	src := []byte(`monoAddErr(state, fmt.Sprintf("arity %s %s %s", ostyToString(typeArgs.len()), ostyToString(fnDecl.generics.len()), "Ok(s.len())"))`)
	got := string(normalizeGeneratedOutput(src))
	if want := `ostyToString(len(typeArgs))`; !containsText(got, want) {
		t.Fatalf("normalizeGeneratedOutput() missing %q in %q", want, got)
	}
	if want := `ostyToString(len(fnDecl.generics))`; !containsText(got, want) {
		t.Fatalf("normalizeGeneratedOutput() missing %q in %q", want, got)
	}
	if want := `"Ok(s.len())"`; !containsText(got, want) {
		t.Fatalf("normalizeGeneratedOutput() rewrote string literal unexpectedly: %q", got)
	}
}

func containsText(haystack, needle string) bool {
	return len(needle) != 0 && len(haystack) >= len(needle) && (haystack == needle || containsTextAt(haystack, needle))
}

func containsTextAt(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
