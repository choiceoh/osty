package ir

import (
	"reflect"
	"testing"
)

// TestBuilderDerivableAllPubNoDefaults: a struct whose fields are all
// pub with no defaults has a derivable builder and every field is
// required (spec §3.3 — HttpConfig-style, minus the defaulted knobs).
func TestBuilderDerivableAllPubNoDefaults(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}
`
	mod := lowerSrc(t, src)
	sd := findStruct(t, mod, "Point")
	if !sd.BuilderDerivable {
		t.Fatal("BuilderDerivable should be true: no private fields, no override")
	}
	if got, want := sd.BuilderRequiredFields, []string{"x", "y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuilderRequiredFields = %v, want %v", got, want)
	}
}

// TestBuilderDerivablePrivateWithDefault: a private field WITH a
// default does not block derivation (spec §3.3: "every private field
// has an explicit default").
func TestBuilderDerivablePrivateWithDefault(t *testing.T) {
	src := `
pub struct HttpConfig {
    pub url: String,
    pub method: String = "GET",
    pub timeout: Int = 30,
    headers: Int = 0,
}
`
	mod := lowerSrc(t, src)
	sd := findStruct(t, mod, "HttpConfig")
	if !sd.BuilderDerivable {
		t.Fatal("BuilderDerivable should be true: private `headers` has default")
	}
	if got, want := sd.BuilderRequiredFields, []string{"url"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuilderRequiredFields = %v, want %v", got, want)
	}
}

// TestBuilderNotDerivablePrivateNoDefault: matches the spec's
// AuthToken example — a private field without a default suppresses
// auto-derive entirely. `required` still lists the pub-no-default
// fields (here, none) so tooling can distinguish "no builder because
// private undefaulted" from "no builder because of user override".
func TestBuilderNotDerivablePrivateNoDefault(t *testing.T) {
	src := `
pub struct AuthToken {
    value: String,
    issuer: String,
}
`
	mod := lowerSrc(t, src)
	sd := findStruct(t, mod, "AuthToken")
	if sd.BuilderDerivable {
		t.Fatal("BuilderDerivable must be false: private field `value` has no default")
	}
	if len(sd.BuilderRequiredFields) != 0 {
		t.Fatalf("BuilderRequiredFields = %v, want empty (no pub fields)", sd.BuilderRequiredFields)
	}
}

// TestBuilderNotDerivableUserOverride: user-supplied associated fn
// `builder` suppresses auto-derive (spec §3.3 "Override" paragraph).
// Required-field list stays populated so diagnostics can still talk
// about the would-be required fields.
func TestBuilderNotDerivableUserOverride(t *testing.T) {
	src := `
pub struct Custom {
    pub url: String,

    pub fn builder() -> Int { 0 }
}
`
	mod := lowerSrc(t, src)
	sd := findStruct(t, mod, "Custom")
	if sd.BuilderDerivable {
		t.Fatal("BuilderDerivable must be false: user defined `builder` associated fn")
	}
	if got, want := sd.BuilderRequiredFields, []string{"url"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuilderRequiredFields = %v, want %v", got, want)
	}
}

// TestBuilderUserMethodDoesNotSuppress: a user-defined `builder` with
// a `self` receiver is a method, not an associated fn, so it does
// NOT satisfy the spec override clause. Auto-derive stays on.
func TestBuilderUserMethodDoesNotSuppress(t *testing.T) {
	src := `
pub struct Gadget {
    pub name: String,

    pub fn builder(self) -> Int { 0 }
}
`
	mod := lowerSrc(t, src)
	sd := findStruct(t, mod, "Gadget")
	if !sd.BuilderDerivable {
		t.Fatal("method named `builder` (with self) must not suppress auto-derive")
	}
}

// TestBuilderRequiredFieldOrderMatchesSource: required field order
// follows declaration order so diagnostics list missing fields in the
// order the author wrote them.
func TestBuilderRequiredFieldOrderMatchesSource(t *testing.T) {
	src := `
pub struct Req {
    pub c: Int,
    pub a: Int,
    pub b: Int = 0,
    pub d: Int,
}
`
	mod := lowerSrc(t, src)
	sd := findStruct(t, mod, "Req")
	if got, want := sd.BuilderRequiredFields, []string{"c", "a", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuilderRequiredFields = %v, want %v (source order)", got, want)
	}
}

// TestCloneStructPreservesBuilderDerive: round-tripping through the
// IR cloner keeps both flags and the required-field slice.
func TestCloneStructPreservesBuilderDerive(t *testing.T) {
	orig := &StructDecl{
		Name:                  "X",
		BuilderDerivable:      true,
		BuilderRequiredFields: []string{"a", "b"},
	}
	c := cloneStructDecl(orig)
	if !c.BuilderDerivable {
		t.Error("cloneStructDecl dropped BuilderDerivable")
	}
	if got, want := c.BuilderRequiredFields, []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("cloneStructDecl BuilderRequiredFields = %v, want %v", got, want)
	}
	// Independent slices — mutating the clone must not affect the original.
	c.BuilderRequiredFields[0] = "mutated"
	if orig.BuilderRequiredFields[0] != "a" {
		t.Error("cloneStructDecl aliased BuilderRequiredFields slice")
	}
}

func TestLowerBuilderChainToStructLit(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().x(3).y(4).build()
}
`
	mod := lowerSrc(t, src)
	mainFn := findFn(t, mod, "main")
	letP := findLet(t, mainFn, "p")
	lit, ok := letP.Value.(*StructLit)
	if !ok {
		t.Fatalf("lowered value = %T, want *StructLit", letP.Value)
	}
	if lit.TypeName != "Point" {
		t.Fatalf("TypeName = %q, want Point", lit.TypeName)
	}
	if lit.Spread != nil {
		t.Fatal("builder().build() should not lower with spread")
	}
	if got := len(lit.Fields); got != 2 {
		t.Fatalf("field count = %d, want 2", got)
	}
	if lit.Fields[0].Name != "x" || lit.Fields[1].Name != "y" {
		t.Fatalf("field order = [%s %s], want [x y]", lit.Fields[0].Name, lit.Fields[1].Name)
	}
}

func TestLowerToBuilderChainToStructLitWithSpread(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point { x: 1, y: 2 }
    let q = p.toBuilder().x(99).build()
}
`
	mod := lowerSrc(t, src)
	mainFn := findFn(t, mod, "main")
	letQ := findLet(t, mainFn, "q")
	lit, ok := letQ.Value.(*StructLit)
	if !ok {
		t.Fatalf("lowered value = %T, want *StructLit", letQ.Value)
	}
	if lit.TypeName != "Point" {
		t.Fatalf("TypeName = %q, want Point", lit.TypeName)
	}
	if lit.Spread == nil {
		t.Fatal("toBuilder().build() should lower with spread")
	}
	if got := len(lit.Fields); got != 1 {
		t.Fatalf("field count = %d, want 1", got)
	}
	if lit.Fields[0].Name != "x" {
		t.Fatalf("field name = %q, want x", lit.Fields[0].Name)
	}
}

func findFn(t *testing.T, mod *Module, name string) *FnDecl {
	t.Helper()
	for _, d := range mod.Decls {
		if fn, ok := d.(*FnDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("fn %q not in module decls", name)
	return nil
}

func findLet(t *testing.T, fn *FnDecl, name string) *LetStmt {
	t.Helper()
	if fn == nil || fn.Body == nil {
		t.Fatalf("fn/body missing while searching let %q", name)
	}
	for _, stmt := range fn.Body.Stmts {
		if let, ok := stmt.(*LetStmt); ok && let.Name == name {
			return let
		}
	}
	t.Fatalf("let %q not in fn body", name)
	return nil
}

func findStruct(t *testing.T, mod *Module, name string) *StructDecl {
	t.Helper()
	for _, d := range mod.Decls {
		if s, ok := d.(*StructDecl); ok && s.Name == name {
			return s
		}
	}
	t.Fatalf("struct %q not in module decls", name)
	return nil
}
