package parser

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestParseGenericInterface(t *testing.T) {
	src := []byte(`pub interface Cache<K, V> {
    fn get(self, key: K) -> V?
    fn put(mut self, key: K, value: V)
}
`)

	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil || len(file.Decls) != 1 {
		t.Fatalf("parsed decls = %d, want 1", len(file.Decls))
	}

	iface, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("decl[0] = %T, want *ast.InterfaceDecl", file.Decls[0])
	}
	if iface.Name != "Cache" {
		t.Errorf("Name = %q, want %q", iface.Name, "Cache")
	}
	if !iface.Pub {
		t.Error("Pub = false, want true")
	}
	if got, want := len(iface.Generics), 2; got != want {
		t.Fatalf("Generics len = %d, want %d", got, want)
	}
	if got, want := iface.Generics[0].Name, "K"; got != want {
		t.Errorf("Generics[0].Name = %q, want %q", got, want)
	}
	if got, want := iface.Generics[1].Name, "V"; got != want {
		t.Errorf("Generics[1].Name = %q, want %q", got, want)
	}
	if got, want := len(iface.Methods), 2; got != want {
		t.Fatalf("Methods len = %d, want %d", got, want)
	}
	if iface.Methods[0].Name != "get" || iface.Methods[1].Name != "put" {
		t.Errorf("Methods = [%s, %s], want [get, put]",
			iface.Methods[0].Name, iface.Methods[1].Name)
	}
}

func TestParseGenericInterfaceWithBounds(t *testing.T) {
	src := []byte(`interface OrderedMap<K: Ordered, V> {
    fn first(self) -> (K, V)?
}
`)

	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}

	iface, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("decl[0] = %T, want *ast.InterfaceDecl", file.Decls[0])
	}
	if got, want := len(iface.Generics), 2; got != want {
		t.Fatalf("Generics len = %d, want %d", got, want)
	}
	if got := iface.Generics[0]; got.Name != "K" || len(got.Constraints) != 1 {
		t.Errorf("Generics[0] = {Name:%q Constraints:%d}, want {K, 1}",
			got.Name, len(got.Constraints))
	}
	if got := iface.Generics[1]; got.Name != "V" || len(got.Constraints) != 0 {
		t.Errorf("Generics[1] = {Name:%q Constraints:%d}, want {V, 0}",
			got.Name, len(got.Constraints))
	}
}

func TestParseRejectsColonInterfaceSyntax(t *testing.T) {
	// v0.4 grammar requires body-level SuperIface; colon syntax is rejected.
	src := []byte(`interface Reader: Closeable {
    fn read(self) -> Int
}
`)

	_, diags := ParseDiagnostics(src)
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic for colon-interface syntax, got none")
	}

	// Sanity: at least one diag should mention `{` (missing after Reader).
	var found bool
	for _, d := range diags {
		if strings.Contains(d.Message, "{") || strings.Contains(d.Message, "expected") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a diag about missing `{` or `expected` token, got %+v", diags)
	}
}

func TestParseGenericInterfaceWithBodySupers(t *testing.T) {
	// Generic interface with body-level super (v0.4 spec style).
	src := []byte(`pub interface Sequence<T> {
    Iterable
    fn len(self) -> Int
}
`)

	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}

	iface, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("decl[0] = %T, want *ast.InterfaceDecl", file.Decls[0])
	}
	if got, want := len(iface.Generics), 1; got != want {
		t.Fatalf("Generics len = %d, want %d", got, want)
	}
	if iface.Generics[0].Name != "T" {
		t.Errorf("Generics[0].Name = %q, want T", iface.Generics[0].Name)
	}
	if got, want := len(iface.Extends), 1; got != want {
		t.Fatalf("Extends len = %d, want %d", got, want)
	}
	if got, want := len(iface.Methods), 1; got != want {
		t.Fatalf("Methods len = %d, want %d", got, want)
	}
}
