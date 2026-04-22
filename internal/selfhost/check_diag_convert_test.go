package selfhost_test

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

// TestCheckDiagnosticsAsDiagSurfacesIntrinsicViolation verifies the
// end-to-end shape: running CheckSourceStructured on a source with an
// `#[intrinsic]` non-empty-body violation produces a record that, after
// conversion, renders as an *diag.Diagnostic with the expected code,
// severity, primary span, and notes. This is the CLI-side contract
// that future `osty check --native` wiring relies on.
func TestCheckDiagnosticsAsDiagSurfacesIntrinsicViolation(t *testing.T) {
	src := []byte(`#[intrinsic]
fn bad() -> Int {
    42
}
`)
	checked := selfhost.CheckSourceStructured(src)
	diags := selfhost.CheckDiagnosticsAsDiag(src, checked.Diagnostics)
	if len(diags) == 0 {
		t.Fatalf("expected at least one diagnostic for intrinsic violation; got none (source diagnostics=%#v)", checked.Diagnostics)
	}
	var gate *diag.Diagnostic
	for _, d := range diags {
		if d != nil && d.Code == "E0773" {
			gate = d
			break
		}
	}
	if gate == nil {
		codes := make([]string, 0, len(diags))
		for _, d := range diags {
			if d != nil {
				codes = append(codes, d.Code)
			}
		}
		t.Fatalf("expected E0773 in converted diagnostics; got codes=%v", codes)
	}
	if gate.Severity != diag.Error {
		t.Fatalf("E0773 severity = %v, want Error", gate.Severity)
	}
	if len(gate.Spans) == 0 {
		t.Fatalf("E0773 has no spans; want primary span pointing at fn body")
	}
	// Body starts on line 2 (`fn bad() -> Int {`) per the source above;
	// primary span should land somewhere inside the body block.
	primary := gate.Spans[0]
	if primary.Span.Start.Line < 2 {
		t.Fatalf("primary span start line = %d, want ≥ 2 (fn body region)", primary.Span.Start.Line)
	}
	if len(gate.Notes) == 0 {
		t.Fatalf("E0773 has no notes; want LANG_SPEC §19.6 note + hint")
	}
}

// TestCheckDiagnosticsAsDiagDropsEmptyRecords documents the filter:
// records with empty Code AND empty Message are signal-only and must
// not produce a diagnostic. A well-formed record with only a Message
// survives.
func TestCheckDiagnosticsAsDiagDropsEmptyRecords(t *testing.T) {
	records := []selfhost.CheckDiagnosticRecord{
		{}, // empty — should be dropped
		{Message: "message only"},
		{Code: "Exxxx"}, // code only — survives
	}
	diags := selfhost.CheckDiagnosticsAsDiag([]byte("fn main() {}\n"), records)
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2 (empty record should be dropped)\n%#v", len(diags), diags)
	}
	if diags[0].Message != "message only" {
		t.Fatalf("diags[0].Message = %q, want %q", diags[0].Message, "message only")
	}
	if diags[1].Code != "Exxxx" {
		t.Fatalf("diags[1].Code = %q, want %q", diags[1].Code, "Exxxx")
	}
}

// TestCheckDiagnosticsAsDiagIsAstbridgeFree pins the invariant that
// record-to-diagnostic conversion — purely byte-offset → line/column
// math plus struct shaping — never walks into astbridge. Combined
// with the existing CheckSourceStructured / CheckStructuredFromRun
// astbridge-free guarantees, this proves a future native CLI path
// (parse → check → convert → print) stays at count == 0.
func TestCheckDiagnosticsAsDiagIsAstbridgeFree(t *testing.T) {
	src := []byte(`#[intrinsic]
fn bad() -> Int { 42 }
`)
	checked := selfhost.CheckSourceStructured(src)

	selfhost.ResetAstbridgeLowerCount()
	diags := selfhost.CheckDiagnosticsAsDiag(src, checked.Diagnostics)
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("CheckDiagnosticsAsDiag: AstbridgeLowerCount = %d, want 0", got)
	}
	if len(diags) == 0 {
		t.Fatalf("expected at least one converted diagnostic")
	}
}

// TestCheckDiagnosticRecordAsDiagSeverityMapping checks the severity
// case-folding handles the self-host-emitted lowercase + the
// convenience variants.
func TestCheckDiagnosticRecordAsDiagSeverityMapping(t *testing.T) {
	src := []byte("x")
	cases := []struct {
		severity string
		want     diag.Severity
	}{
		{"error", diag.Error},
		{"Error", diag.Error},
		{"ERROR", diag.Error},
		{"warning", diag.Warning},
		{"warn", diag.Warning},
		{"WARN", diag.Warning},
		{"", diag.Error},    // default
		{"info", diag.Error}, // unrecognized → default to Error (not silently downgraded)
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.severity, func(t *testing.T) {
			got := selfhost.CheckDiagnosticRecordAsDiag(src, selfhost.CheckDiagnosticRecord{
				Code:     "Exxxx",
				Severity: tc.severity,
				Message:  "m",
				Start:    0,
				End:      1,
			})
			if got == nil {
				t.Fatalf("nil result")
			}
			if got.Severity != tc.want {
				t.Fatalf("severity for %q = %v, want %v", tc.severity, got.Severity, tc.want)
			}
		})
	}
}
