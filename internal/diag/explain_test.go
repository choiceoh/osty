package diag

import (
	"strings"
	"testing"
)

// Smoke-test the embedded-codes parser: a well-known code should
// parse with a non-empty summary, and an unknown code should miss.
func TestExplainKnownCode(t *testing.T) {
	d, ok := Explain("E0001")
	if !ok {
		t.Fatalf("Explain(E0001): want ok, got miss")
	}
	if d.Name != "CodeUnterminatedString" {
		t.Errorf("Explain(E0001).Name = %q, want CodeUnterminatedString", d.Name)
	}
	if d.Summary == "" {
		t.Errorf("Explain(E0001).Summary is empty")
	}
	if d.Fix == "" {
		t.Errorf("Explain(E0001).Fix is empty")
	}
}

func TestExplainUnknownCode(t *testing.T) {
	if _, ok := Explain("E9999"); ok {
		t.Errorf("Explain(E9999): want miss, got hit")
	}
}

// Every constant in codes.go should surface through AllCodes().
// A regression here means init() silently dropped an entry — most
// likely because the comment format drifted from what the parser
// expects.
func TestAllCodesNonEmpty(t *testing.T) {
	all := AllCodes()
	if len(all) < 20 {
		t.Fatalf("AllCodes(): want at least 20 entries, got %d", len(all))
	}
	// Codes should be sorted.
	for i := 1; i < len(all); i++ {
		if all[i-1].Code > all[i].Code {
			t.Fatalf("AllCodes() not sorted: %s before %s", all[i-1].Code, all[i].Code)
		}
	}
	// Every entry's Code should start with E, W, or L — no stray
	// constants leaking in from unrelated renames.
	for _, d := range all {
		if !strings.ContainsAny(string(d.Code[0]), "EWL") {
			t.Errorf("unexpected code prefix: %q", d.Code)
		}
	}
}
