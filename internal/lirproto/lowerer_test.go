package lirproto

import (
	"testing"

	"github.com/osty/osty/internal/mir"
)

func TestLowererLowerMIREmptyModuleProducesEnvelope(t *testing.T) {
	l := NewLowerer(Config{
		PackageName: "cfgpkg",
		SourcePath:  "/tmp/main.osty",
		Target:      "x86_64-unknown-linux-gnu",
		FeatureGate: map[string]bool{"x": true},
	})
	cfg := l.Config()
	cfg.FeatureGate["x"] = false

	res := l.LowerMIR(&mir.Module{Package: "mirpkg"})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	if got, want := res.Module.PackageName, "cfgpkg"; got != want {
		t.Fatalf("PackageName = %q, want %q", got, want)
	}
	if got, want := res.Module.SourcePath, "/tmp/main.osty"; got != want {
		t.Fatalf("SourcePath = %q, want %q", got, want)
	}
	if got, want := res.Module.Target, "x86_64-unknown-linux-gnu"; got != want {
		t.Fatalf("Target = %q, want %q", got, want)
	}
	if got := l.Config().FeatureGate["x"]; !got {
		t.Fatal("Config() did not return a defensive copy")
	}
}

func TestLowererFallsBackToMIRPackageWhenConfigPackageEmpty(t *testing.T) {
	res := NewLowerer(Config{}).LowerMIR(&mir.Module{Package: "mirpkg"})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	if got, want := res.Module.PackageName, "mirpkg"; got != want {
		t.Fatalf("PackageName = %q, want %q", got, want)
	}
}

func TestLowererNilMIRReportsInvalidInput(t *testing.T) {
	res := NewLowerer(Config{}).LowerMIR(nil)
	if res.OK() {
		t.Fatal("LowerMIR(nil).OK() = true, want false")
	}
	if got, want := len(res.Diagnostics), 1; got != want {
		t.Fatalf("diagnostics len = %d, want %d: %+v", got, want, res.Diagnostics)
	}
	diag := res.Diagnostics[0]
	if diag.Kind != DiagInvalidInput || diag.Severity != SeverityError {
		t.Fatalf("diag = %+v, want invalid input error", diag)
	}
}

func TestLowererUnsupportedMIRShapeReportsDiagnostic(t *testing.T) {
	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package: "main",
		Functions: []*mir.Function{
			{Name: "main"},
		},
	})
	if res.OK() {
		t.Fatal("LowerMIR(unsupported shape).OK() = true, want false")
	}
	if !res.HasUnsupported() {
		t.Fatalf("HasUnsupported() = false, diagnostics = %+v", res.Diagnostics)
	}
}

func TestLowererResetsDiagnosticsBetweenRuns(t *testing.T) {
	l := NewLowerer(Config{})
	first := l.LowerMIR(nil)
	if first.OK() {
		t.Fatal("first LowerMIR(nil).OK() = true, want false")
	}
	second := l.LowerMIR(&mir.Module{})
	if !second.OK() {
		t.Fatalf("second LowerMIR(empty) diagnostics = %+v, want OK", second.Diagnostics)
	}
}
