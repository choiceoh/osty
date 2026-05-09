package selfhost

import "testing"

// (cut D pre-release amendment removed `#[reproducible_capability]`
//  annotation + `E0783` gate. Capability determinism is now inferred
//  from per-method `#[reproducible]` / `#[pure]` annotations on the
//  interface surface — surfacing happens at the call site through
//  `E0784` / `E0785`. The tests below pin the inference + signature
//  gates that remain.)

// An interface where every method is `#[reproducible]` is
// auto-classified as a deterministic capability — receiving it inside
// a `#[reproducible]` function does NOT trip `E0784`.
func TestInferredReproducibleCapabilityAllowedInReproducible(t *testing.T) {
	src := []byte(`interface HashCap {
    #[reproducible]
    fn hash(self, value: String) -> String
}

#[reproducible]
fn fingerprint(hash: HashCap, value: String) -> String {
    value
}
`)
	checked := CheckSourceStructured(src)
	if got := countReproducibleCapabilityCode(checked, "E0784"); got != 0 {
		t.Fatalf("E0784 count = %d, want 0 (inferred reproducible capability should be accepted); diagnostics=%#v", got, checked.Diagnostics)
	}
}

// An interface with at least one method missing `#[reproducible]` /
// `#[pure]` is NOT a deterministic capability. Receiving it in a
// `#[reproducible]` function is the *call-site* surface — but the
// signature-local gate currently only checks canonical capability
// names + interfaces classified as reproducible (allow-list). A
// non-classified user interface therefore neither qualifies for the
// allow nor trips `E0784` directly. This test pins that the inference
// excludes mixed-annotation interfaces from the allow-list.
func TestInferenceRejectsMixedAnnotationInterface(t *testing.T) {
	src := []byte(`interface Sloppy {
    #[reproducible]
    fn hash(self, value: String) -> String

    fn salt(self) -> String
}
`)
	checked := CheckSourceStructured(src)
	// No diagnostics expected at the declaration — inference is silent;
	// the classification only matters at call sites.
	if got := countReproducibleCapabilityCode(checked, "E0783"); got != 0 {
		t.Fatalf("E0783 count = %d, want 0 (gate removed in cut D); diagnostics=%#v", got, checked.Diagnostics)
	}
}

func TestCapabilitySignatureGateRejectsReproducibleNonDetCapability(t *testing.T) {
	src := []byte(`#[reproducible]
fn buildId(clock: Clock) -> Int {
    0
}
`)
	checked := CheckSourceStructured(src)
	if got := countReproducibleCapabilityCode(checked, "E0784"); got != 1 {
		t.Fatalf("E0784 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
}

func TestCapabilitySignatureGateAllowsReproducibleConsole(t *testing.T) {
	src := []byte(`#[reproducible]
fn report(console: Console) {
}
`)
	checked := CheckSourceStructured(src)
	if got := countReproducibleCapabilityCode(checked, "E0784"); got != 0 {
		t.Fatalf("E0784 count = %d, want 0; diagnostics=%#v", got, checked.Diagnostics)
	}
}

// `#[pure]` rejects ALL capability parameters — even an inferred
// reproducible capability — because purity is strictly stronger than
// reproducibility (§3.11 / §20.4).
func TestCapabilitySignatureGateRejectsPureCapability(t *testing.T) {
	src := []byte(`interface HashCap {
    #[reproducible]
    fn hash(self, value: String) -> String
}

#[pure]
fn digest(hash: HashCap, value: String) -> String {
    value
}
`)
	checked := CheckSourceStructured(src)
	if got := countReproducibleCapabilityCode(checked, "E0785"); got != 1 {
		t.Fatalf("E0785 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
}

func countReproducibleCapabilityCode(checked CheckResult, code string) int {
	count := 0
	for _, d := range checked.Diagnostics {
		if d.Code == code {
			count++
		}
	}
	return count
}
