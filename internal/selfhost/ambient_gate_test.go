package selfhost

import "testing"

func TestAmbientGateAcceptsEntryPoints(t *testing.T) {
	src := []byte(`#[ambient(clock)]
fn main() {}

#[ambient(rng)]
fn test_build_id() {}

#[ambient(env)]
fn testBuildId() {}

#[ambient(fs)]
fn bench_io() {}

#[ambient(net)]
fn benchParseJson() {}

#[test]
#[ambient(console)]
fn deterministic_console() {}
`)
	checked := CheckSourceStructured(src)
	for _, d := range checked.Diagnostics {
		if d.Code == "E0780" || d.Code == "E0781" {
			t.Fatalf("entry-point ambient should not emit %s: %#v", d.Code, checked.Diagnostics)
		}
	}
}

func TestAmbientGateRejectsNonEntryFunctionsAndNonFunctionDecls(t *testing.T) {
	src := []byte(`#[ambient(clock)]
fn helper() {}

#[ambient(rng)]
pub struct BadStruct {
    pub value: Int,
}

#[ambient(env)]
interface BadInterface {
    fn get(self) -> Int
}

#[ambient(fs)]
type BadAlias = Int
`)
	checked := CheckSourceStructured(src)
	if got := countAmbientCode(checked, "E0780"); got != 4 {
		t.Fatalf("E0780 count = %d, want 4; diagnostics=%#v", got, checked.Diagnostics)
	}
	if got := countAmbientCode(checked, "E0781"); got != 0 {
		t.Fatalf("E0781 count = %d, want 0; diagnostics=%#v", got, checked.Diagnostics)
	}
}

func TestAmbientGateRejectsUnknownCanonicalName(t *testing.T) {
	src := []byte(`#[ambient(database)]
fn main() {}
`)
	checked := CheckSourceStructured(src)
	if got := countAmbientCode(checked, "E0781"); got != 1 {
		t.Fatalf("E0781 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
	if got := countAmbientCode(checked, "E0780"); got != 0 {
		t.Fatalf("E0780 count = %d, want 0; diagnostics=%#v", got, checked.Diagnostics)
	}
}

func countAmbientCode(checked CheckResult, code string) int {
	count := 0
	for _, d := range checked.Diagnostics {
		if d.Code == code {
			count++
		}
	}
	return count
}
