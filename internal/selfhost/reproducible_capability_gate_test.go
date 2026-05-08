package selfhost

import "testing"

func TestReproducibleCapabilityGateAcceptsReproducibleMethods(t *testing.T) {
	src := []byte(`#[reproducible_capability]
interface HashCap {
    #[reproducible]
    fn hash(self, value: String) -> String
}
`)
	checked := CheckSourceStructured(src)
	if got := countReproducibleCapabilityCode(checked, "E0783"); got != 0 {
		t.Fatalf("E0783 count = %d, want 0; diagnostics=%#v", got, checked.Diagnostics)
	}
}

func TestReproducibleCapabilityGateRejectsNonReproducibleMethod(t *testing.T) {
	src := []byte(`#[reproducible_capability]
interface HashCap {
    fn hash(self, value: String) -> String
}
`)
	checked := CheckSourceStructured(src)
	if got := countReproducibleCapabilityCode(checked, "E0783"); got != 1 {
		t.Fatalf("E0783 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
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

func TestCapabilitySignatureGateRejectsPureCapability(t *testing.T) {
	src := []byte(`#[reproducible_capability]
interface HashCap {
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
