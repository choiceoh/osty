package main

import (
	"testing"

	"github.com/osty/osty/internal/manifest"
)

// TestRemoveDepFromBothLists confirms removeDep clears a name from
// the normal dependency list and leaves the dev list alone when the
// devOnly flag is false (default behavior).
func TestRemoveDepFromBothLists(t *testing.T) {
	m := &manifest.Manifest{
		Dependencies: []manifest.Dependency{
			{Name: "foo"}, {Name: "bar"},
		},
		DevDependencies: []manifest.Dependency{
			{Name: "foo"}, {Name: "baz"},
		},
	}
	if !removeDep(m, "foo", false) {
		t.Fatalf("expected removeDep to report removal")
	}
	for _, d := range m.Dependencies {
		if d.Name == "foo" {
			t.Errorf("foo still in [dependencies]")
		}
	}
	for _, d := range m.DevDependencies {
		if d.Name == "foo" {
			t.Errorf("foo still in [dev-dependencies]")
		}
	}
}

// TestRemoveDevOnly: devOnly must skip the normal list. The same
// name in [dependencies] should remain untouched.
func TestRemoveDevOnly(t *testing.T) {
	m := &manifest.Manifest{
		Dependencies:    []manifest.Dependency{{Name: "shared"}},
		DevDependencies: []manifest.Dependency{{Name: "shared"}},
	}
	if !removeDep(m, "shared", true) {
		t.Fatalf("expected removeDep to report removal")
	}
	if len(m.Dependencies) != 1 || m.Dependencies[0].Name != "shared" {
		t.Errorf("normal dep was modified: %+v", m.Dependencies)
	}
	if len(m.DevDependencies) != 0 {
		t.Errorf("dev dep should be empty: %+v", m.DevDependencies)
	}
}

// TestRemoveDepNotFound: a missing name returns false so the CLI
// can report the error instead of silently succeeding.
func TestRemoveDepNotFound(t *testing.T) {
	m := &manifest.Manifest{Dependencies: []manifest.Dependency{{Name: "a"}}}
	if removeDep(m, "missing", false) {
		t.Errorf("expected false for missing name")
	}
}
