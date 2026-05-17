package main

import (
	"testing"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/mir"
)

// TestStripMainForLibraryModeRemovesMainAndKeepsOthers locks the
// PR-G2 dep-as-library helper behavior: only the `main` function is
// stripped, every other MIR function survives. Without this, a future
// over-aggressive filter could silently drop pub helpers and break
// the link step downstream.
func TestStripMainForLibraryModeRemovesMainAndKeepsOthers(t *testing.T) {
	mainFn := &mir.Function{Name: "main"}
	helperFn := &mir.Function{Name: "frontInvalidTypeRepr"}
	otherFn := &mir.Function{Name: "checkLookupFn"}

	entry := &backend.Entry{
		MIR: &mir.Module{
			Functions: []*mir.Function{mainFn, helperFn, otherFn},
		},
	}
	stripMainForLibraryMode(entry)

	if len(entry.MIR.Functions) != 2 {
		t.Fatalf("Functions length = %d, want 2 (after stripping main)", len(entry.MIR.Functions))
	}
	for _, fn := range entry.MIR.Functions {
		if fn.Name == "main" {
			t.Errorf("`main` survived strip: still in Functions list")
		}
	}
	names := map[string]bool{}
	for _, fn := range entry.MIR.Functions {
		names[fn.Name] = true
	}
	if !names["frontInvalidTypeRepr"] {
		t.Errorf("frontInvalidTypeRepr was stripped (should survive)")
	}
	if !names["checkLookupFn"] {
		t.Errorf("checkLookupFn was stripped (should survive)")
	}
}

// TestStripMainForLibraryModeHandlesNilSafely guards against nil
// entry / nil MIR / nil function slice — preparePackageEntry can fail
// in ways that produce partial entries, and the strip helper runs
// unconditionally after a successful prepare call. Without this
// test, a regression that crashes on nil could ship undetected
// because the cross-pkg dep path is opt-in via env var (only fires
// in dev / one-off CI runs).
func TestStripMainForLibraryModeHandlesNilSafely(t *testing.T) {
	stripMainForLibraryMode(nil)
	stripMainForLibraryMode(&backend.Entry{})

	entry := &backend.Entry{MIR: &mir.Module{}}
	stripMainForLibraryMode(entry)
	if len(entry.MIR.Functions) != 0 {
		t.Fatalf("Functions length = %d, want 0 (no-op on empty)", len(entry.MIR.Functions))
	}

	entry2 := &backend.Entry{
		MIR: &mir.Module{
			Functions: []*mir.Function{nil, {Name: "main"}, nil, {Name: "helper"}},
		},
	}
	stripMainForLibraryMode(entry2)
	if len(entry2.MIR.Functions) != 1 {
		t.Fatalf("Functions length = %d, want 1 (helper survived, nils + main stripped)", len(entry2.MIR.Functions))
	}
	if entry2.MIR.Functions[0].Name != "helper" {
		t.Fatalf("survivor = %q, want \"helper\"", entry2.MIR.Functions[0].Name)
	}
}
