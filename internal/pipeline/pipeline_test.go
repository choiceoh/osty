package pipeline

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const cleanSrc = `fn main() {
    let n = 1 + 2
    println(n)
}
`

const dirtySrc = `fn main() {
    let x = undefined_name
}
`

// Run on a syntactically valid, type-clean program should report
// every phase, no errors, and finite timings.
func TestRunCleanProgram(t *testing.T) {
	r := Run([]byte(cleanSrc), nil)
	wantPhases := []string{"lex", "parse", "resolve", "check", "lint"}
	if got := len(r.Stages); got != len(wantPhases) {
		t.Fatalf("Stages: got %d, want %d", got, len(wantPhases))
	}
	for i, want := range wantPhases {
		if r.Stages[i].Name != want {
			t.Errorf("Stages[%d].Name = %q, want %q", i, r.Stages[i].Name, want)
		}
		if r.Stages[i].Errors != 0 {
			t.Errorf("Stages[%d] (%s) reported %d errors on clean source",
				i, r.Stages[i].Name, r.Stages[i].Errors)
		}
	}
	if r.File == nil {
		t.Errorf("File is nil after parse")
	}
	if r.Resolve == nil || r.Check == nil || r.Lint == nil {
		t.Errorf("missing phase artefacts: resolve=%v check=%v lint=%v",
			r.Resolve != nil, r.Check != nil, r.Lint != nil)
	}
}

// A program with an undefined name should surface at least one
// resolve-phase error in the stage stats and AllDiags.
func TestRunReportsErrors(t *testing.T) {
	r := Run([]byte(dirtySrc), nil)
	var resolveErrs int
	for _, s := range r.Stages {
		if s.Name == "resolve" {
			resolveErrs = s.Errors
			break
		}
	}
	if resolveErrs == 0 {
		t.Fatalf("expected at least one resolve error for undefined name; stages=%+v", r.Stages)
	}
	if len(r.AllDiags) == 0 {
		t.Fatalf("AllDiags empty; expected at least one diagnostic")
	}
}

// The trace stream should receive one line per phase, in order.
func TestRunStreamsTrace(t *testing.T) {
	var buf bytes.Buffer
	Run([]byte(cleanSrc), &buf)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("trace stream: got %d lines, want 5\nfull output:\n%s", len(lines), buf.String())
	}
	for _, want := range []string{"lex", "parse", "resolve", "check", "lint"} {
		found := false
		for _, line := range lines {
			if strings.Contains(line, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("trace stream missing %q phase\nfull output:\n%s", want, buf.String())
		}
	}
}

// RenderJSON should produce a valid document with the expected
// top-level keys and one entry per phase.
func TestRenderJSON(t *testing.T) {
	r := Run([]byte(cleanSrc), nil)
	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var doc struct {
		Stages []struct {
			Name       string  `json:"name"`
			DurationMS float64 `json:"duration_ms"`
		} `json:"stages"`
		DiagnosticsByCode map[string]int `json:"diagnostics_by_code"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(doc.Stages) != 5 {
		t.Errorf("expected 5 stages in JSON, got %d", len(doc.Stages))
	}
}

// PerDecl should record one entry per (decl, phase) pair when enabled.
// cleanSrc has one fn (main), so we expect exactly two entries:
// one for collect, one for check.
func TestRunPerDecl(t *testing.T) {
	r := RunWithConfig([]byte(cleanSrc), nil, Config{PerDecl: true})
	if len(r.PerDecl) != 2 {
		t.Fatalf("PerDecl: got %d entries, want 2; entries=%+v", len(r.PerDecl), r.PerDecl)
	}
	phases := map[string]bool{}
	for _, dt := range r.PerDecl {
		if dt.Name != "main" {
			t.Errorf("PerDecl entry name = %q, want main", dt.Name)
		}
		if dt.Kind != "fn" {
			t.Errorf("PerDecl entry kind = %q, want fn", dt.Kind)
		}
		phases[dt.Phase] = true
	}
	for _, want := range []string{"collect", "check"} {
		if !phases[want] {
			t.Errorf("PerDecl missing %q phase", want)
		}
	}
}

// RunGen should append a sixth "gen" stage and populate GenBytes on a
// Phase-1-clean program. cleanSrc only uses primitives + println, both
// covered by Phase 1.
func TestRunGen(t *testing.T) {
	r := RunWithConfig([]byte(cleanSrc), nil, Config{RunGen: true})
	if len(r.Stages) != 6 {
		t.Fatalf("Stages: got %d, want 6 (lex/parse/resolve/check/lint/gen); names=%v",
			len(r.Stages), stageNames(r.Stages))
	}
	if r.Stages[5].Name != "gen" {
		t.Errorf("Stages[5].Name = %q, want gen", r.Stages[5].Name)
	}
	if len(r.GenBytes) == 0 {
		t.Errorf("GenBytes is empty after a successful gen on clean source")
	}
	if r.GenError != nil {
		t.Errorf("GenError = %v on clean source", r.GenError)
	}
	if !strings.Contains(string(r.GenBytes), "package main") {
		t.Errorf("GenBytes missing 'package main' clause; got: %s", r.GenBytes)
	}
}

func stageNames(ss []Stage) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}
