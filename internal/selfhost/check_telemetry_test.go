package selfhost

import "testing"

func TestSelfhostDiagnosticTelemetryBucketsErrorDiagnosticsByCode(t *testing.T) {
	diags := []*CheckDiagnostic{
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "type mismatch: expected `Int`, found `String`"},
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "type mismatch: expected `Bool`, found `Int`"},
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityWarning{}), message: "warning should not count"},
		{severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "uncoded error"},
	}

	byContext, details := selfhostDiagnosticTelemetry(diags, nil)

	if got := byContext["E0700"]; got != 2 {
		t.Fatalf("E0700 count = %d, want 2", got)
	}
	if got := byContext["error"]; got != 1 {
		t.Fatalf("fallback error count = %d, want 1", got)
	}
	if got := details["E0700"]["type mismatch: expected `Int`, found `String`"]; got != 1 {
		t.Fatalf("first detail count = %d, want 1", got)
	}
	if got := details["E0700"]["type mismatch: expected `Bool`, found `Int`"]; got != 1 {
		t.Fatalf("second detail count = %d, want 1", got)
	}
	if _, ok := details["E0700"]["warning should not count"]; ok {
		t.Fatalf("warning detail unexpectedly counted")
	}
	if got := details["error"]["uncoded error"]; got != 1 {
		t.Fatalf("fallback detail count = %d, want 1", got)
	}
}

func TestSelfhostDiagnosticTelemetryAppendsSourcePosSuffix(t *testing.T) {
	diags := []*CheckDiagnostic{
		// File-mode lookup (filename empty) → `@Lnn:Cnn` suffix, since the
		// filename already sits in the dump label above the bucket.
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "type mismatch: expected `Int`, found `String`", start: 5, end: 6},
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "type mismatch: expected `Int`, found `String`", start: 9, end: 10},
		// Package-mode lookup (filename present) → `@<file>:Lnn:Cnn` so
		// cross-file parity failures stay distinguishable under one label.
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "type mismatch: expected `Int`, found `String`", start: 17, end: 18},
		{code: "E0701", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "arity off", start: -1, end: -1},
		{code: "E0702", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "unresolved", start: 42, end: 43},
	}

	posLookup := func(tokenIdx int) (string, int, int, bool) {
		switch tokenIdx {
		case 5:
			return "", 10, 3, true
		case 9:
			return "", 10, 20, true
		case 17:
			return "b.osty", 4, 12, true
		}
		return "", 0, 0, false
	}

	_, details := selfhostDiagnosticTelemetry(diags, posLookup)

	if got := details["E0700"]["type mismatch: expected `Int`, found `String` @L10:C3"]; got != 1 {
		t.Fatalf("file-mode E0700 with @L10:C3 = %d, want 1 (details=%v)", got, details["E0700"])
	}
	if got := details["E0700"]["type mismatch: expected `Int`, found `String` @L10:C20"]; got != 1 {
		t.Fatalf("file-mode E0700 with @L10:C20 = %d, want 1 (details=%v)", got, details["E0700"])
	}
	if got := details["E0700"]["type mismatch: expected `Int`, found `String` @b.osty:L4:C12"]; got != 1 {
		t.Fatalf("package-mode E0700 with @b.osty:L4:C12 = %d, want 1 (details=%v)", got, details["E0700"])
	}
	// Structurally-identical messages with distinct source positions no longer
	// collapse into a single bucket — this is the whole point of the suffix.
	if _, collapsed := details["E0700"]["type mismatch: expected `Int`, found `String`"]; collapsed {
		t.Fatalf("un-suffixed detail leaked into E0700 bucket: %v", details["E0700"])
	}
	// Diagnostics whose token index is unresolved keep their bare detail so
	// the suffix stays opt-in and never lies about position.
	if got := details["E0701"]["arity off"]; got != 1 {
		t.Fatalf("unresolved-position detail = %d, want 1 (details=%v)", got, details["E0701"])
	}
	if got := details["E0702"]["unresolved"]; got != 1 {
		t.Fatalf("out-of-range-position detail = %d, want 1 (details=%v)", got, details["E0702"])
	}
}
