package check

import (
	"sort"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

// The Go-native checker and the bootstrapped Osty checker each run their
// own copy of the §19 policy gates (privilege / POD shape / noalloc /
// intrinsic body). That duplication is flagged as a correctness hazard
// in SELFHOST_PORT_MATRIX.md: divergence silently lands in production
// until someone compares the two emitters by hand.
//
// This test nails down parity on the *gate diagnostic codes* — E0770 /
// E0771 / E0772 / E0773 — across a curated corpus. Each case lists the
// source + privileged flag, plus the set of gate codes the port contract
// requires both sides to emit. A failure on either side (Go-only or
// Osty-only) surfaces the exact divergence and blocks further
// consolidation until investigated.
//
// Non-gate diagnostics (E0500, E0717, …) are filtered out — the Osty
// checker runs the full pipeline (resolve + elab + gates) so its diag
// list is wider than the Go gate functions alone.
func TestGatesCrossSideParity(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		privileged bool
		wantCodes  []string
	}{
		{
			name:      "clean-fn-no-gate-diag",
			src:       "fn main() {}\n",
			wantCodes: []string{},
		},
		{
			// Intrinsic with non-empty body — E0773 from both sides.
			name:       "intrinsic-non-empty-body",
			src:        "#[intrinsic] fn raw_null() -> Int { 0 }\n",
			privileged: true,
			wantCodes:  []string{"E0773"},
		},
		{
			// Intrinsic with empty body — no diag.
			name:       "intrinsic-empty-body-ok",
			src:        "#[intrinsic] fn raw_null() -> Int {}\n",
			privileged: true,
			wantCodes:  []string{},
		},
		{
			// Intrinsic signature-only — no diag.
			name:       "intrinsic-signatureless-ok",
			src:        "#[intrinsic] fn raw_null() -> Int\n",
			privileged: true,
			wantCodes:  []string{},
		},
		{
			// Non-privileged package using #[intrinsic] — Go strips
			// E0770 (the gate is skipped); Osty emits unconditionally
			// and the host boundary strips it post-hoc. At the gate
			// boundary (this test) Osty will surface E0770 while Go
			// won't — that asymmetry is flagged in the test
			// expectation so the consolidation plan has to pick one.
			//
			// For now the wantCodes captures what the *Go* emitter
			// produces; the Osty side may diverge, and if it does the
			// assertion below surfaces the extra codes so the team
			// has a concrete list to reconcile.
			name:      "non-privileged-intrinsic",
			src:       "#[intrinsic] fn raw_null() -> Int\n",
			wantCodes: []string{"E0770"},
		},
		{
			// #[pod] struct without #[repr(c)] — E0771 from both
			// sides. The privileged flag is set so E0770 doesn't
			// mask the POD-specific diag.
			name: "pod-missing-repr-c",
			src: `#[pod]
pub struct BadPod { pub x: Int }
`,
			privileged: true,
			wantCodes:  []string{"E0771"},
		},
		{
			// #[pod] with #[repr(c)] and POD-compatible fields —
			// no gate diag.
			name: "pod-well-formed",
			src: `#[pod]
#[repr(c)]
pub struct GoodPod { pub x: Int, pub y: Int }
`,
			privileged: true,
			wantCodes:  []string{},
		},
		{
			// #[no_alloc] fn with an allocating body — E0772.
			name: "noalloc-with-list-literal",
			src: `#[no_alloc]
pub fn allocates() -> List<Int> { [1, 2, 3] }
`,
			privileged: true,
			wantCodes:  []string{"E0772"},
		},
		{
			// #[no_alloc] fn with a scalar body — no diag.
			name: "noalloc-scalar-body",
			src: `#[no_alloc]
pub fn fine() -> Int { 42 }
`,
			privileged: true,
			wantCodes:  []string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			goCodes := runGoGateCodes(t, c.src, c.privileged)
			ostyCodes := runOstyGateCodes(t, c.src, c.privileged)

			wantSorted := append([]string{}, c.wantCodes...)
			sort.Strings(wantSorted)

			if !equalCodeSets(goCodes, wantSorted) {
				t.Errorf("Go gates = %v, want %v", goCodes, wantSorted)
			}
			if !equalCodeSets(ostyCodes, wantSorted) {
				t.Errorf(
					"Osty gates = %v, want %v — divergence from Go; add entry to "+
						"divergence register in SELFHOST_PORT_MATRIX.md hazards if intentional",
					ostyCodes,
					wantSorted,
				)
			}
		})
	}
}

// runGoGateCodes runs the four Go-side policy gates in the same order
// check.File / check.Package invoke them and returns the sorted unique
// set of E0770-E0773 codes they emit.
func runGoGateCodes(t *testing.T, src string, privileged bool) []string {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		for _, d := range parseDiags {
			if d.Severity == diag.Error {
				t.Fatalf("parse error in fixture: %v", parseDiags)
			}
		}
	}
	var diags []*diag.Diagnostic
	diags = append(diags, runPrivilegeGate(file, privileged)...)
	diags = append(diags, runPodShapeChecks(file)...)
	diags = append(diags, runNoAllocChecks(file, nil)...)
	diags = append(diags, runIntrinsicBodyChecks(file)...)
	return filterGateCodes(diags)
}

// runOstyGateCodes invokes the bootstrapped Osty checker and keeps only
// the gate-band codes. The Osty pipeline runs resolve + elab before the
// gates, so its raw diag list is wider — we strip non-gate codes to
// keep the parity comparison apples-to-apples. When `privileged` is
// true we also strip E0770 from Osty's output to match the host
// boundary's post-hoc strip (check_gates.osty §19.2: the gate emits
// unconditionally at the selfhost edge; the Go host removes E0770 in
// privileged mode). Without this the test would trip on a known
// intentional asymmetry rather than a real divergence.
func runOstyGateCodes(t *testing.T, src string, privileged bool) []string {
	t.Helper()
	result := selfhost.CheckSourceStructured([]byte(src))
	out := make([]string, 0, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		if !isGateCode(d.Code) {
			continue
		}
		if privileged && d.Code == "E0770" {
			continue
		}
		out = append(out, d.Code)
	}
	sort.Strings(out)
	return dedupeSortedStrings(out)
}

func filterGateCodes(diags []*diag.Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		if d == nil {
			continue
		}
		if isGateCode(d.Code) {
			out = append(out, d.Code)
		}
	}
	sort.Strings(out)
	return dedupeSortedStrings(out)
}

func isGateCode(code string) bool {
	switch code {
	case "E0770", "E0771", "E0772", "E0773":
		return true
	}
	return false
}

func dedupeSortedStrings(xs []string) []string {
	if len(xs) <= 1 {
		return xs
	}
	out := xs[:1]
	for i := 1; i < len(xs); i++ {
		if xs[i] != out[len(out)-1] {
			out = append(out, xs[i])
		}
	}
	return out
}

func equalCodeSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
