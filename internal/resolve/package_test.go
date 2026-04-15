package resolve

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

// resolvePkg is a small helper that parses each source string as an
// independent file and resolves them together as a single package.
// Source strings are ordered so tests can control which file is seen
// first (relevant for partial-declaration merge diagnostics).
func resolvePkg(t *testing.T, sources ...string) *PackageResult {
	t.Helper()
	pkg := &Package{Name: "test"}
	for i, src := range sources {
		file, parseDiags := parser.ParseDiagnostics([]byte(src))
		pkg.Files = append(pkg.Files, &PackageFile{
			Path:       "file" + itoa(i) + ".osty",
			Source:     []byte(src),
			File:       file,
			ParseDiags: parseDiags,
		})
	}
	return ResolvePackage(pkg, NewPrelude())
}

// TestPackageCrossFileReference verifies that a top-level declaration in
// one file is visible from another file in the same package without
// needing a `use`.
func TestPackageCrossFileReference(t *testing.T) {
	a := `pub fn greeter(name: String) -> String { "hi, {name}" }`
	b := `fn shout() -> String { greeter("world") }`
	res := resolvePkg(t, a, b)
	if len(res.Diags) != 0 {
		for _, d := range res.Diags {
			t.Errorf("unexpected diag: %s", d.Error())
		}
	}
}

// TestPackageDuplicateAcrossFiles verifies that two non-mergeable
// declarations (e.g. two `fn` with the same name) in different files
// still produce E0501.
func TestPackageDuplicateAcrossFiles(t *testing.T) {
	a := `fn dup() {}`
	b := `fn dup() {}`
	res := resolvePkg(t, a, b)
	if findPkgDiag(res, diag.CodeDuplicateDecl) == nil {
		t.Fatalf("expected E0501, got %v", res.Diags)
	}
}

// TestPackagePartialStructFieldsAndMethods verifies that a struct split
// across two files — fields in one, methods in another — resolves
// cleanly per R19.
func TestPackagePartialStructFieldsAndMethods(t *testing.T) {
	a := `pub struct User {
    pub name: String,
    pub age: Int,
}`
	b := `pub struct User {
    pub fn greet(self) -> String { "hi, {self.name}" }
    pub fn birthday(self) -> Int { self.age + 1 }
}`
	res := resolvePkg(t, a, b)
	if len(res.Diags) != 0 {
		for _, d := range res.Diags {
			t.Errorf("unexpected diag: %s", d.Error())
		}
	}
}

// TestPackagePartialPubMismatch verifies R19: two partial decls of the
// same struct must agree on `pub`.
func TestPackagePartialPubMismatch(t *testing.T) {
	a := `pub struct User { pub name: String }`
	b := `struct User {
    fn greet(self) -> String { self.name }
}`
	res := resolvePkg(t, a, b)
	if findPkgDiag(res, diag.CodeDuplicateDecl) == nil {
		t.Fatalf("expected pub-mismatch diagnostic, got %v", res.Diags)
	}
	found := false
	for _, d := range res.Diags {
		if strings.Contains(d.Message, "visibility") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a `visibility` diagnostic; got %v", res.Diags)
	}
}

// TestPackagePartialGenericMismatch verifies R19: type parameters must
// match across partial declarations.
func TestPackagePartialGenericMismatch(t *testing.T) {
	a := `pub struct Box<T> { pub inner: T }`
	b := `pub struct Box<U> {
    pub fn get(self) -> U { self.inner }
}`
	res := resolvePkg(t, a, b)
	found := false
	for _, d := range res.Diags {
		if strings.Contains(d.Message, "type parameters") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a `type parameters` diagnostic; got %v", res.Diags)
	}
}

// TestPackagePartialEnumVariantsPerFile verifies that bare-variant
// references still work across files when an enum is defined in one file
// and used in another.
func TestPackagePartialEnumVariantsPerFile(t *testing.T) {
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
	res := resolvePkg(t, a, b)
	if len(res.Diags) != 0 {
		for _, d := range res.Diags {
			t.Errorf("unexpected diag: %s", d.Error())
		}
	}
}

// TestPackageUseAliasFileLocal verifies that a `use` alias in one file
// is NOT visible from another file in the same package. Each file's
// imports are private to that file.
func TestPackageUseAliasFileLocal(t *testing.T) {
	// File A defines `fs` via `use`. File B references `fs` without a
	// `use` of its own — this should fail as undefined.
	a := `use std.fs
fn fromA() { let x = fs }`
	b := `fn fromB() { let y = fs }`
	res := resolvePkg(t, a, b)
	// File A must be clean; file B must hit E0500.
	if findPkgDiag(res, diag.CodeUndefinedName) == nil {
		t.Fatalf("expected undefined-name in file B, got %v", res.Diags)
	}
}

// TestPackageConflictingUseAliasAcrossFiles verifies that each file may
// bind the same `use` alias without interference — the aliases are
// file-scope not package-scope.
func TestPackageConflictingUseAliasAcrossFiles(t *testing.T) {
	a := `use std.fs
fn fromA() { let x = fs }`
	b := `use std.fs
fn fromB() { let y = fs }`
	res := resolvePkg(t, a, b)
	if len(res.Diags) != 0 {
		for _, d := range res.Diags {
			t.Errorf("unexpected diag: %s", d.Error())
		}
	}
}

// TestPackagePartialFieldsInMultipleDecls verifies R19: struct fields
// may live in exactly one partial declaration.
func TestPackagePartialFieldsInMultipleDecls(t *testing.T) {
	a := `pub struct User { pub name: String }`
	b := `pub struct User { pub email: String }`
	res := resolvePkg(t, a, b)
	found := false
	for _, d := range res.Diags {
		if strings.Contains(d.Message, "declare fields in exactly one") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected R19 field-spread diagnostic, got %v", res.Diags)
	}
}

// TestPackagePartialVariantsInMultipleDecls verifies R19 for enums.
func TestPackagePartialVariantsInMultipleDecls(t *testing.T) {
	a := `pub enum Color { Red, Green }`
	b := `pub enum Color { Blue }`
	res := resolvePkg(t, a, b)
	found := false
	for _, d := range res.Diags {
		if strings.Contains(d.Message, "declare variants in exactly one") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected R19 variant-spread diagnostic, got %v", res.Diags)
	}
}

// TestPackagePartialMethodsSpread verifies that methods may legally
// spread across multiple partial declarations (R19 positive case).
func TestPackagePartialMethodsSpread(t *testing.T) {
	a := `pub struct User {
    pub name: String,
    pub fn greet(self) -> String { self.name }
}`
	b := `pub struct User {
    pub fn shout(self) -> String { self.name }
}`
	res := resolvePkg(t, a, b)
	if len(res.Diags) != 0 {
		for _, d := range res.Diags {
			t.Errorf("unexpected diag: %s", d.Error())
		}
	}
}

// TestPackagePartialMethodNameCollision verifies R19: methods spread
// across partial declarations must have unique names.
func TestPackagePartialMethodNameCollision(t *testing.T) {
	a := `pub struct User {
    pub name: String,
    pub fn greet(self) -> String { "a" }
}`
	b := `pub struct User {
    pub fn greet(self) -> String { "b" }
}`
	res := resolvePkg(t, a, b)
	if findPkgDiag(res, diag.CodeDuplicateDecl) == nil {
		t.Fatalf("expected E0501 for duplicate method `greet` across partials, got %v",
			res.Diags)
	}
}

// TestPackagePartialMethodNamesDistinct verifies methods with different
// names across partial declarations are accepted.
func TestPackagePartialMethodNamesDistinct(t *testing.T) {
	a := `pub struct User {
    pub name: String,
    pub fn greet(self) -> String { "a" }
}`
	b := `pub struct User {
    pub fn leave(self) -> String { "b" }
}`
	res := resolvePkg(t, a, b)
	if d := findPkgDiag(res, diag.CodeDuplicateDecl); d != nil {
		t.Fatalf("unexpected duplicate diagnostic on distinct method names: %s", d.Error())
	}
}

// findPkgDiag returns the first diagnostic matching code, or nil.
func findPkgDiag(res *PackageResult, code string) *diag.Diagnostic {
	for _, d := range res.Diags {
		if d.Code == code {
			return d
		}
	}
	return nil
}
