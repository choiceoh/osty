package selfhost

import "testing"

// TestStructuredIntentGateAcceptsValidForms exercises the v0.6 G42
// `#[purpose]` / `#[example]` / `#[fixture]` annotations in their
// canonical shapes and asserts the gate stays silent.
func TestStructuredIntentGateAcceptsValidForms(t *testing.T) {
	src := []byte(`#[fixture(name = "alice")]
fn aliceFixture() -> Int { 42 }

#[purpose("compute the rounded score")]
#[example(input = ["alice"], output = "Ok(42)", uses = "alice")]
fn computeScore(name: String) -> Int { 0 }
`)
	checked := CheckSourceStructured(src)
	for _, d := range checked.Diagnostics {
		switch d.Code {
		case "E0431", "E0432", "E0433", "E0434", "E0435", "E0436":
			t.Fatalf("valid intent surface emitted %s: %#v", d.Code, checked.Diagnostics)
		}
	}
}

// TestStructuredIntentGateRejectsInterpolatedPurpose locks in the
// E0434 contract: `#[purpose]` arguments must be plain string literals.
func TestStructuredIntentGateRejectsInterpolatedPurpose(t *testing.T) {
	src := []byte(`let name = "world"

#[purpose("hello {name}")]
fn greet() -> Int { 0 }
`)
	checked := CheckSourceStructured(src)
	if got := countCode(checked, "E0434"); got != 1 {
		t.Fatalf("E0434 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
}

// TestStructuredIntentGateRejectsNonLiteralPurpose covers the
// `key = value` and identifier purpose forms.
func TestStructuredIntentGateRejectsNonLiteralPurpose(t *testing.T) {
	src := []byte(`#[purpose(text = "intent")]
fn declarative() -> Int { 0 }

#[purpose(intent)]
fn bareIdent() -> Int { 0 }
`)
	checked := CheckSourceStructured(src)
	if got := countCode(checked, "E0434"); got != 2 {
		t.Fatalf("E0434 count = %d, want 2; diagnostics=%#v", got, checked.Diagnostics)
	}
}

// TestStructuredIntentGateRejectsUnknownExampleKey locks in E0435.
func TestStructuredIntentGateRejectsUnknownExampleKey(t *testing.T) {
	src := []byte(`#[example(input = [1], output = "1", tags = "smoke")]
fn identity(x: Int) -> Int { x }
`)
	checked := CheckSourceStructured(src)
	if got := countCode(checked, "E0435"); got != 1 {
		t.Fatalf("E0435 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
}

// TestStructuredIntentGateRejectsFixtureOnStruct locks in E0436.
func TestStructuredIntentGateRejectsFixtureOnStruct(t *testing.T) {
	src := []byte(`#[fixture(name = "basic")]
pub struct SpecFixture {
    pub value: Int,
}

#[fixture(name = "shape")]
pub enum Shape {
    Circle,
    Square,
}
`)
	checked := CheckSourceStructured(src)
	if got := countCode(checked, "E0436"); got != 2 {
		t.Fatalf("E0436 count = %d, want 2; diagnostics=%#v", got, checked.Diagnostics)
	}
}

// TestStructuredIntentGatePreservesExistingDiagnostics ensures the
// new path doesn't regress E0431/E0432/E0433.
func TestStructuredIntentGatePreservesExistingDiagnostics(t *testing.T) {
	src := []byte(`#[fixture(name = "withArg")]
fn withArg(x: Int) -> Int { x }

#[example(input = [1, 2], output = "3")]
fn singleArg(x: Int) -> Int { x }

#[example(input = [1], output = "1", uses = "missing")]
fn unknownFixture(x: Int) -> Int { x }
`)
	checked := CheckSourceStructured(src)
	if got := countCode(checked, "E0432"); got != 1 {
		t.Fatalf("E0432 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
	if got := countCode(checked, "E0431"); got != 1 {
		t.Fatalf("E0431 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
	if got := countCode(checked, "E0433"); got != 1 {
		t.Fatalf("E0433 count = %d, want 1; diagnostics=%#v", got, checked.Diagnostics)
	}
}

func countCode(checked CheckResult, code string) int {
	count := 0
	for _, d := range checked.Diagnostics {
		if d.Code == code {
			count++
		}
	}
	return count
}
