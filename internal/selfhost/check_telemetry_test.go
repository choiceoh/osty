package selfhost

import "testing"

func TestSelfhostDiagnosticTelemetryBucketsErrorDiagnosticsByCode(t *testing.T) {
	diags := []*CheckDiagnostic{
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "type mismatch: expected `Int`, found `String`"},
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "type mismatch: expected `Bool`, found `Int`"},
		{code: "E0700", severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityWarning{}), message: "warning should not count"},
		{severity: DiagnosticSeverity(&DiagnosticSeverity_SeverityError{}), message: "uncoded error"},
	}

	byContext, details := selfhostDiagnosticTelemetry(diags)

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
