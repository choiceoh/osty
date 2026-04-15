package check_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// runCheckIface mirrors runCheck (defined in check_test.go) so this
// file is self-contained for the iface-validation regressions.
func runCheckIface(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
	all = append(all, chk.Diags...)
	var errs []*diag.Diagnostic
	for _, d := range all {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs
}

func mustHaveCode(t *testing.T, got []*diag.Diagnostic, code string) {
	t.Helper()
	for _, d := range got {
		if d.Code == code {
			return
		}
	}
	lines := make([]string, 0, len(got))
	for _, d := range got {
		lines = append(lines, d.Code+": "+d.Message)
	}
	t.Fatalf("expected diag code %s; got %d:\n  %s",
		code, len(got), strings.Join(lines, "\n  "))
}

func mustBeOK(t *testing.T, got []*diag.Diagnostic) {
	t.Helper()
	if len(got) == 0 {
		return
	}
	lines := make([]string, 0, len(got))
	for _, d := range got {
		lines = append(lines, d.Code+": "+d.Message)
	}
	t.Fatalf("expected no errors, got %d:\n  %s",
		len(got), strings.Join(lines, "\n  "))
}

// ---- §2.6.1 Interface composition (Extends) ----

// A struct that implements only Reader does NOT satisfy ReadWriter,
// which composes Reader + Writer.
func TestIface_CompositionMissingExtendedMethod(t *testing.T) {
	src := `
pub interface Reader {
    fn read(self) -> Int
}

pub interface Writer {
    fn write(self, n: Int)
}

pub interface ReadWriter {
    Reader
    Writer
}

pub struct Half {
    pub fn read(self) -> Int { 0 }
}

fn take<T: ReadWriter>(x: T) {}

fn main() {
    take(Half {})
}
`
	mustHaveCode(t, runCheckIface(t, src), diag.CodeTypeMismatch)
}

// A struct that implements every method from both extended interfaces
// satisfies the composition.
func TestIface_CompositionFullImpl(t *testing.T) {
	src := `
pub interface Reader {
    fn read(self) -> Int
}

pub interface Writer {
    fn write(self, n: Int)
}

pub interface ReadWriter {
    Reader
    Writer
}

pub struct Both {
    pub fn read(self) -> Int { 0 }
    pub fn write(self, n: Int) {}
}

fn take<T: ReadWriter>(x: T) {}

fn main() {
    take(Both {})
}
`
	mustBeOK(t, runCheckIface(t, src))
}

// ---- §2.6.3 Self substitution ----

// A user interface using `Self` in a non-receiver position must be
// satisfiable by a struct whose method takes the struct's own type.
func TestIface_SelfSubstitutionInUserInterface(t *testing.T) {
	src := `
pub interface Eqish {
    fn equiv(self, other: Self) -> Bool
}

pub struct P {
    pub x: Int,

    pub fn equiv(self, other: P) -> Bool { true }
}

fn take<T: Eqish>(x: T) {}

fn main() {
    take(P { x: 1 })
}
`
	mustBeOK(t, runCheckIface(t, src))
}

// Wrong Self argument type fails the structural check.
func TestIface_SelfSubstitutionMismatch(t *testing.T) {
	src := `
pub interface Eqish {
    fn equiv(self, other: Self) -> Bool
}

pub struct Q {
    pub fn equiv(self, other: Int) -> Bool { true }
}

fn take<T: Eqish>(x: T) {}

fn main() {
    take(Q {})
}
`
	mustHaveCode(t, runCheckIface(t, src), diag.CodeTypeMismatch)
}

// ---- §2.6.2 Default methods ----

// An interface method with a default body need not be re-implemented.
func TestIface_DefaultMethodMayBeOmitted(t *testing.T) {
	src := `
pub interface Greeter {
    fn name(self) -> String
    fn greet(self) -> String { "hello" }
}

pub struct A {
    pub fn name(self) -> String { "a" }
}

fn take<T: Greeter>(x: T) {}

fn main() {
    take(A {})
}
`
	mustBeOK(t, runCheckIface(t, src))
}

// ---- §7 Error built-in interface ----

// A struct with NO methods does NOT satisfy Error.
func TestIface_ErrorRequiresMessage(t *testing.T) {
	src := `
pub struct Bare {}

fn take<T: Error>(e: T) {}

fn main() {
    take(Bare {})
}
`
	mustHaveCode(t, runCheckIface(t, src), diag.CodeTypeMismatch)
}

// A struct with `message(self) -> String` satisfies Error.
func TestIface_ErrorSatisfiedByMessage(t *testing.T) {
	src := `
pub struct MyErr {
    pub fn message(self) -> String { "boom" }
}

fn take<T: Error>(e: T) {}

fn main() {
    take(MyErr {})
}
`
	mustBeOK(t, runCheckIface(t, src))
}

// A struct with `message` of the wrong return type does NOT satisfy.
func TestIface_ErrorWrongMessageSignature(t *testing.T) {
	src := `
pub struct WrongErr {
    pub fn message(self) -> Int { 0 }
}

fn take<T: Error>(e: T) {}

fn main() {
    take(WrongErr {})
}
`
	mustHaveCode(t, runCheckIface(t, src), diag.CodeTypeMismatch)
}

// ---- §2.6.5 Composite-type marker interfaces ----

// List<Float> is NOT Hashable (Float isn't).
func TestIface_HashableRejectsListOfFloat(t *testing.T) {
	src := `
fn key<T: Hashable>(x: T) -> T { x }

fn main() {
    let xs: List<Float> = []
    let _ = key(xs)
}
`
	mustHaveCode(t, runCheckIface(t, src), diag.CodeTypeMismatch)
}

// List<Int> IS Hashable.
func TestIface_HashableAcceptsListOfInt(t *testing.T) {
	src := `
fn key<T: Hashable>(x: T) -> T { x }

fn main() {
    let xs: List<Int> = []
    let _ = key(xs)
}
`
	mustBeOK(t, runCheckIface(t, src))
}

// Tuple of Equal components is Equal; tuple containing a function is
// NOT (function types lack Equal per §2.9). Functions never reach this
// position naturally, so we exercise the simpler all-Equal happy path.
func TestIface_EqualAcceptsTupleOfPrimitives(t *testing.T) {
	src := `
fn take<T: Equal>(x: T) {}

fn main() {
    let p: (Int, String) = (1, "hi")
    take(p)
}
`
	mustBeOK(t, runCheckIface(t, src))
}

// Map<String, Float> is NOT Hashable (values are Float).
func TestIface_HashableRejectsMapWithFloatValues(t *testing.T) {
	src := `
fn key<T: Hashable>(x: T) -> T { x }

fn main() {
    let m: Map<String, Float> = {:}
    let _ = key(m)
}
`
	mustHaveCode(t, runCheckIface(t, src), diag.CodeTypeMismatch)
}

// ---- Generic interface argument substitution ----

// A user generic interface `Iter<T>` is satisfied structurally when
// the concrete `next` returns the substituted type.
func TestIface_GenericInterfaceArgsSubstituted(t *testing.T) {
	src := `
pub interface Iter<T> {
    fn next(self) -> T?
}

pub struct IntIter {
    pub fn next(self) -> Int? { None }
}

fn take<T: Iter<Int>>(x: T) {}

fn main() {
    take(IntIter {})
}
`
	mustBeOK(t, runCheckIface(t, src))
}

// Wrong type-argument substitution: IntIter does NOT satisfy Iter<String>.
func TestIface_GenericInterfaceArgsMismatch(t *testing.T) {
	src := `
pub interface Iter<T> {
    fn next(self) -> T?
}

pub struct IntIter {
    pub fn next(self) -> Int? { None }
}

fn take<T: Iter<String>>(x: T) {}

fn main() {
    take(IntIter {})
}
`
	mustHaveCode(t, runCheckIface(t, src), diag.CodeTypeMismatch)
}

// ---- All-violations diagnostic ----

// Both missing and mismatched methods are reported in one check.
func TestIface_ReportsMissingAndMismatchedTogether(t *testing.T) {
	src := `
pub interface Multi {
    fn a(self) -> Int
    fn b(self) -> String
}

pub struct Bad {
    pub fn a(self) -> String { "wrong" }
}

fn take<T: Multi>(x: T) {}

fn main() {
    take(Bad {})
}
`
	got := runCheckIface(t, src)
	// Expect at least two TypeMismatch diagnostics: one for missing `b`,
	// one for mismatched `a`.
	count := 0
	for _, d := range got {
		if d.Code == diag.CodeTypeMismatch {
			count++
		}
	}
	if count < 2 {
		lines := make([]string, 0, len(got))
		for _, d := range got {
			lines = append(lines, d.Code+": "+d.Message)
		}
		t.Fatalf("expected at least 2 TypeMismatch diags; got %d:\n  %s",
			count, strings.Join(lines, "\n  "))
	}
}
