package selfhost_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// TestCheckPackageStructuredCrossPkgDefaultArgViaParamDefaults documents the
// expected behavior when an imported package surface declares a function
// with a defaulted trailing parameter through the `ParamDefaults` API field
// instead of the `?`-prefixed paramNames convention. The dispatch site
// should treat the call `dep.make(1)` as legal (the trailing `y` is filled
// from the default).
//
// Background: the arena-walking import surface
// (internal/selfhost/import_surface_arena.go::arenaBuildImportedFn) sets
// BOTH the `?`-prefixed name and the parallel `ParamDefaults` bool slice.
// `paramDefaultCount` (generated.go mirror of toolchain/check.osty's
// counterpart) only consults the `?` prefix. User-supplied surfaces (Go
// tests, future external probes) that fill `ParamDefaults` but not the
// `?`-prefix lost the default information, surfacing as an arity error.
func TestCheckPackageStructuredCrossPkgDefaultArgViaParamDefaults(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`use dep

fn main() {
    let answer = dep.make(1)
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
		Imports: []selfhost.PackageCheckImport{{
			Alias: "dep",
			Functions: []selfhost.PackageCheckFn{{
				Name:          "make",
				Owner:         "dep",
				ReturnType:    "Int",
				ParamNames:    []string{"x", "y"},
				ParamTypes:    []string{"Int", "Int"},
				ParamDefaults: []bool{false, true},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		var diagSummary []string
		for _, d := range checked.Diagnostics {
			diagSummary = append(diagSummary, d.Code+": "+d.Message)
		}
		t.Fatalf("summary errors = %d, want 0; diagnostics=%s", checked.Summary.Errors, strings.Join(diagSummary, "; "))
	}
}

// TestCheckPackageStructuredCrossPkgDefaultArgRejectsBelowMinArity locks the
// negative arm of the same surface: passing zero args to `dep.make(x, ?y)`
// still surfaces an arity error because only the trailing `y` carries a
// default (`min_arity = total - trailing_defaults = 1`).
func TestCheckPackageStructuredCrossPkgDefaultArgRejectsBelowMinArity(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`use dep

fn main() {
    let answer = dep.make()
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
		Imports: []selfhost.PackageCheckImport{{
			Alias: "dep",
			Functions: []selfhost.PackageCheckFn{{
				Name:          "make",
				Owner:         "dep",
				ReturnType:    "Int",
				ParamNames:    []string{"x", "y"},
				ParamTypes:    []string{"Int", "Int"},
				ParamDefaults: []bool{false, true},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors == 0 {
		t.Fatalf("summary errors = 0, want >= 1 (no args below min_arity=1 should still fail)")
	}
	if findDiagnosticCode(checked, "E0701") == nil {
		t.Fatalf("expected E0701 arity diagnostic, got %+v", checked.Diagnostics)
	}
}

// TestCheckPackageStructuredCrossPkgDefaultArgPreservesQuestionPrefix locks the
// idempotency: when the caller already pre-encoded the `?` prefix on
// paramNames (the arena-walker shape), the parallel `ParamDefaults` slice
// must not cause a double-prefix.
func TestCheckPackageStructuredCrossPkgDefaultArgPreservesQuestionPrefix(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`use dep

fn main() {
    let answer = dep.make(1)
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
		Imports: []selfhost.PackageCheckImport{{
			Alias: "dep",
			Functions: []selfhost.PackageCheckFn{{
				Name:          "make",
				Owner:         "dep",
				ReturnType:    "Int",
				ParamNames:    []string{"x", "?y"},
				ParamTypes:    []string{"Int", "Int"},
				ParamDefaults: []bool{false, true},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		var diagSummary []string
		for _, d := range checked.Diagnostics {
			diagSummary = append(diagSummary, d.Code+": "+d.Message)
		}
		t.Fatalf("summary errors = %d, want 0 (single ?-prefix should survive); diagnostics=%s", checked.Summary.Errors, strings.Join(diagSummary, "; "))
	}
}

// TestSelfhostImportFnParamNamesSynthesizesQuestionPrefix unit-tests the
// helper directly so regressions in the synthesis logic surface even when
// the higher-level checker path is exercised through other surfaces.
func TestSelfhostImportFnParamNamesSynthesizesQuestionPrefix(t *testing.T) {
	cases := []struct {
		name          string
		paramNames    []string
		paramDefaults []bool
		paramArity    int
		want          []string
	}{
		{
			name:          "synthesize trailing default",
			paramNames:    []string{"x", "y"},
			paramDefaults: []bool{false, true},
			paramArity:    2,
			want:          []string{"x", "?y"},
		},
		{
			name:          "already prefixed stays single",
			paramNames:    []string{"x", "?y"},
			paramDefaults: []bool{false, true},
			paramArity:    2,
			want:          []string{"x", "?y"},
		},
		{
			name:          "empty defaults preserves names",
			paramNames:    []string{"x", "y"},
			paramDefaults: nil,
			paramArity:    2,
			want:          []string{"x", "y"},
		},
		{
			name:          "all defaults",
			paramNames:    []string{"x", "y"},
			paramDefaults: []bool{true, true},
			paramArity:    2,
			want:          []string{"?x", "?y"},
		},
		// LANG_SPEC v0.5 §3 only allows trailing defaults; a `true` entry
		// before the first trailing `true` must NOT be marked `?` because
		// `paramDefaultCount` would reset on the first bare name and
		// discard a mid-list `?` anyway — synthesizing one there only
		// produces a misleading signature.
		{
			name:          "non-trailing true is ignored",
			paramNames:    []string{"x", "y", "z"},
			paramDefaults: []bool{true, false, true},
			paramArity:    3,
			want:          []string{"x", "y", "?z"},
		},
		{
			name:          "trailing run starts mid-list",
			paramNames:    []string{"a", "b", "c", "d"},
			paramDefaults: []bool{false, false, true, true},
			paramArity:    4,
			want:          []string{"a", "b", "?c", "?d"},
		},
		// When the caller fills `ParamDefaults` + `ParamTypes` but omits
		// `ParamNames`, the helper synthesizes placeholder names so the
		// checker's `paramDefaultCount` can scan trailing `?`-prefixes.
		{
			name:          "synthesize names when ParamNames omitted",
			paramNames:    nil,
			paramDefaults: []bool{false, true},
			paramArity:    2,
			want:          []string{"arg0", "?arg1"},
		},
		{
			name:          "ParamNames shorter than arity pads",
			paramNames:    []string{"x"},
			paramDefaults: []bool{false, true},
			paramArity:    2,
			want:          []string{"x", "?arg1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selfhost.ImportFnParamNamesForTest(tc.paramNames, tc.paramDefaults, tc.paramArity)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCheckPackageStructuredCrossPkgDefaultArgWithOmittedParamNames locks
// the end-to-end behavior when an external import surface populates
// `ParamTypes` and `ParamDefaults` but leaves `ParamNames` nil (the field
// is `json:",omitempty"` on PackageCheckFn). The helper synthesizes
// placeholder names so `paramDefaultCount` sees a trailing `?`-prefix and
// the call `dep.make(1)` typechecks.
func TestCheckPackageStructuredCrossPkgDefaultArgWithOmittedParamNames(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`use dep

fn main() {
    let answer = dep.make(1)
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
		Imports: []selfhost.PackageCheckImport{{
			Alias: "dep",
			Functions: []selfhost.PackageCheckFn{{
				Name:          "make",
				Owner:         "dep",
				ReturnType:    "Int",
				ParamTypes:    []string{"Int", "Int"},
				ParamDefaults: []bool{false, true},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		var diagSummary []string
		for _, d := range checked.Diagnostics {
			diagSummary = append(diagSummary, d.Code+": "+d.Message)
		}
		t.Fatalf("summary errors = %d, want 0 (omitted ParamNames should not block trailing default); diagnostics=%s", checked.Summary.Errors, strings.Join(diagSummary, "; "))
	}
}
