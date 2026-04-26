package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestResolveGenericInterfaceBindsTypeParams asserts the resolver walks
// an `interface Cache<K, V>` declaration without reporting
// undefined-name errors for K / V inside method signatures — the
// generic params must be bound in the interface body scope.
func TestResolveGenericInterfaceBindsTypeParams(t *testing.T) {
	src := []byte(`pub interface Cache<K, V> {
    fn get(self, key: K) -> V?
    fn put(mut self, key: K, value: V)
}
`)

	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected resolver error: %s (%s)", d.Message, d.Code)
		}
	}

	iface := file.Decls[0].(*ast.InterfaceDecl)
	if got, want := len(iface.Generics), 2; got != want {
		t.Fatalf("Generics len = %d, want %d", got, want)
	}
}

// TestResolveUnknownInterfaceBoundEmitsUndefinedName asserts that a
// bound referencing an unknown interface produces E0500 at the bound
// position.
func TestResolveUnknownInterfaceBoundEmitsUndefinedName(t *testing.T) {
	src := []byte(`interface X<T: UnknownBound> {
    fn get(self) -> T
}
`)

	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())

	var found bool
	for _, d := range res.Diags {
		if d.Code == diag.CodeUndefinedName && strings.Contains(d.Message, "UnknownBound") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CodeUndefinedName for UnknownBound, got diags: %+v", res.Diags)
	}
}
