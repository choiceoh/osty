package check

import (
	"sort"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

// Three of the four §19 policy gates still have Go reference
// implementations — privilege / POD shape / noalloc — kept around for
// unit-test ergonomics even though their production emitters moved to
// `toolchain/check_gates.osty::runCheckGates`. Intrinsic body (E0773)
// was consolidated end-to-end in #769: the Go file is gone and every
// call-site routes through `selfhost.IntrinsicBodyDiagsForSource`, so
// no parity comparison remains for that gate.
//
// This test nails down parity on the *gate diagnostic codes* —
// E0770 / E0771 / E0772 — across a curated corpus. Each case lists the
// source + privileged flag, plus the set of gate codes the port
// contract requires both sides to emit. A failure on either side
// (Go-only or Osty-only) surfaces the exact divergence and blocks
// further consolidation until investigated.
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
			// Non-privileged package using #[intrinsic] — privilege
			// gate fires (E0770) because the runtime sublanguage
			// surface is off-limits outside privileged packages.
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
	case "E0770", "E0771", "E0772":
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
