package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// mkPkg parses each source string as an independent file of one
// package, runs the resolver, then returns the checker Result.
func mkPkg(t *testing.T, sources ...string) *Result {
	t.Helper()
	pkg := &resolve.Package{Name: "pkg"}
	for i, src := range sources {
		file, parseDiags := parser.ParseDiagnostics([]byte(src))
		pkg.Files = append(pkg.Files, &resolve.PackageFile{
			Path:       mkPath(i),
			Source:     []byte(src),
			File:       file,
			ParseDiags: parseDiags,
		})
	}
	pr := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	// Fail early if the resolver itself rejected the input.
	for _, d := range pr.Diags {
		if d.Severity == diag.Error {
			t.Fatalf("resolver rejected fixture: %s", d.Error())
		}
	}
	return Package(pkg, pr)
}

func mkPath(i int) string {
	// One tiny helper avoids adding fmt to test imports.
	digits := []byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9'}
	return "file" + string(digits[i%10]) + ".osty"
}

// hasCode reports whether any diagnostic carries the given code.
func hasCode(r *Result, code string) bool {
	for _, d := range r.Diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// TestPackageCrossFileFnCall verifies that one file can call a
// function declared in another file of the same package and that the
// checker agrees on the return type.
func TestPackageCrossFileFnCall(t *testing.T) {
	a := `pub fn add(x: Int, y: Int) -> Int { x + y }`
	b := `fn useIt() -> Int { add(1, 2) }`
	r := mkPkg(t, a, b)
	for _, d := range r.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// TestPackageCrossFileType verifies that a type declared in one file
// can be used as a parameter/return type in another file.
func TestPackageCrossFileType(t *testing.T) {
	a := `pub struct Point { pub x: Int, pub y: Int }`
	b := `fn origin() -> Point { Point { x: 0, y: 0 } }`
	r := mkPkg(t, a, b)
	for _, d := range r.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// TestPackagePartialStructMethodsCheck verifies that methods spread
// across two partial declarations type-check against the merged
// struct's fields.
func TestPackagePartialStructMethodsCheck(t *testing.T) {
	a := `pub struct User {
    pub name: String,
    pub age: Int,
}`
	b := `pub struct User {
    pub fn greet(self) -> String { self.name }
    pub fn birthday(self) -> Int { self.age + 1 }
}`
	r := mkPkg(t, a, b)
	for _, d := range r.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// TestPackageCrossFileEnumMatch verifies an enum declared in file A
// matches cleanly in file B.
func TestPackageCrossFileEnumMatch(t *testing.T) {
	a := `pub enum Shape {
    Circle(Float),
    Rect(Float, Float),
    Empty,
}`
	b := `fn area(s: Shape) -> Float {
    match s {
        Circle(r) -> 3.14 * r * r,
        Rect(w, h) -> w * h,
        Empty -> 0.0,
    }
}`
	r := mkPkg(t, a, b)
	for _, d := range r.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestPackageCrossFileInterfaceComposition(t *testing.T) {
	a := `pub interface Named {
    fn name(self) -> String
}`
	b := `pub interface DisplayNamed {
    Named
}`
	csrc := `pub struct User {
    pub label: String,

    pub fn name(self) -> String { self.label }
}

fn useIt<T: DisplayNamed>(x: T) -> String {
    x.name()
}

fn main() {
    useIt(User { label: "ada" })
}`
	r := mkPkg(t, a, b, csrc)
	for _, d := range r.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// TestPackageCrossFileFieldTypeMismatch verifies the checker catches a
// wrong-type literal in a cross-file struct constructor.
func TestPackageCrossFileFieldTypeMismatch(t *testing.T) {
	a := `pub struct Point { pub x: Int, pub y: Int }`
	b := `fn bad() -> Point { Point { x: "hi", y: 0 } }`
	r := mkPkg(t, a, b)
	if !hasCode(r, diag.CodeTypeMismatch) {
		var got []string
		for _, d := range r.Diags {
			got = append(got, d.Error())
		}
		t.Fatalf("expected E0700 for wrong field type; got:\n  %s",
			strings.Join(got, "\n  "))
	}
}
