package backend

import (
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/stdlib"
)

func TestReachableStdlibFnsEmpty(t *testing.T) {
	if got := ReachableStdlibFns(nil, nil); got != nil {
		t.Fatalf("both nil = %v, want nil", got)
	}
	reg := stdlib.LoadCached()
	if got := ReachableStdlibFns(nil, reg); got != nil {
		t.Fatalf("nil module = %v, want nil", got)
	}
	mod := &ir.Module{Package: "main"}
	if got := ReachableStdlibFns(mod, nil); got != nil {
		t.Fatalf("nil registry = %v, want nil", got)
	}
	if got := ReachableStdlibFns(mod, reg); len(got) != 0 {
		t.Fatalf("empty module = %v, want empty", got)
	}
}

func TestReachableStdlibFnsFindsStringsCompare(t *testing.T) {
	reg := stdlib.LoadCached()
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.CallExpr{
				Callee: &ir.FieldExpr{X: &ir.Ident{Name: "strings"}, Name: "compare"},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "a"}},
					{Value: &ir.Ident{Name: "b"}},
				},
			}},
		},
	}
	got := ReachableStdlibFns(mod, reg)
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1", len(got), got)
	}
	if got[0].Module != "strings" || got[0].Fn.Name != "compare" {
		t.Fatalf("got[0] = {%s, %s}, want {strings, compare}", got[0].Module, got[0].Fn.Name)
	}
	if got[0].Fn.Body == nil {
		t.Fatalf("got[0].Fn.Body = nil, want body of stdlib fn")
	}
}

func TestReachableStdlibFnsSkipsUnknownQualifier(t *testing.T) {
	reg := stdlib.LoadCached()
	// `userAlias.helper(x)` is not a stdlib call.
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.CallExpr{
				Callee: &ir.FieldExpr{X: &ir.Ident{Name: "userAlias"}, Name: "helper"},
				Args:   []ir.Arg{{Value: &ir.Ident{Name: "x"}}},
			}},
		},
	}
	if got := ReachableStdlibFns(mod, reg); len(got) != 0 {
		t.Fatalf("got %v, want no stdlib hits for a user-alias call", got)
	}
}

func TestReachableStdlibMethodsEmpty(t *testing.T) {
	if got := ReachableStdlibMethods(nil, nil); got != nil {
		t.Fatalf("both nil = %v, want nil", got)
	}
	reg := stdlib.LoadCached()
	if got := ReachableStdlibMethods(nil, reg); got != nil {
		t.Fatalf("nil module = %v, want nil", got)
	}
	mod := &ir.Module{Package: "main"}
	if got := ReachableStdlibMethods(mod, nil); got != nil {
		t.Fatalf("nil registry = %v, want nil", got)
	}
	if got := ReachableStdlibMethods(mod, reg); len(got) != 0 {
		t.Fatalf("empty module = %v, want empty", got)
	}
}

func TestReachableStdlibMethodsFindsHexEncode(t *testing.T) {
	reg := stdlib.LoadCached()
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "h", T: hexT},
				Name:     "encode",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "data"}}},
			}},
		},
	}
	got := ReachableStdlibMethods(mod, reg)
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1", len(got), got)
	}
	if got[0].Module != "encoding" || got[0].Type != "Hex" || got[0].Method != "encode" {
		t.Fatalf("got[0] = {%s, %s, %s}, want {encoding, Hex, encode}",
			got[0].Module, got[0].Type, got[0].Method)
	}
	if got[0].Fn == nil || got[0].Fn.Name != "encode" {
		t.Fatalf("got[0].Fn = %v, want encode method AST", got[0].Fn)
	}
}

func TestReachableStdlibMethodsSkipsUnknownMethod(t *testing.T) {
	reg := stdlib.LoadCached()
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "h", T: hexT},
				Name:     "noSuchMethodOnHex",
			}},
		},
	}
	if got := ReachableStdlibMethods(mod, reg); len(got) != 0 {
		t.Fatalf("got %v, want no hit for unknown method", got)
	}
}

func TestReachableStdlibMethodsSkipsUserType(t *testing.T) {
	// ReachMethods already filters empty Package; this verifies that
	// ReachableStdlibMethods preserves that guarantee end-to-end.
	reg := stdlib.LoadCached()
	userT := &ir.NamedType{Package: "", Name: "MyStruct"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "v", T: userT},
				Name:     "encode",
			}},
		},
	}
	if got := ReachableStdlibMethods(mod, reg); len(got) != 0 {
		t.Fatalf("got %v, want no hit for user type method", got)
	}
}

func TestReachableStdlibMethodsDeterministicOrder(t *testing.T) {
	reg := stdlib.LoadCached()
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	b64T := &ir.NamedType{Package: "encoding", Name: "Base64"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "b", T: b64T},
				Name:     "encode",
			}},
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "h", T: hexT},
				Name:     "encode",
			}},
		},
	}
	got := ReachableStdlibMethods(mod, reg)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
	// Sort order: (module, type, method). Both in encoding, so types
	// decide: Base64 < Hex.
	if got[0].Type != "Base64" || got[1].Type != "Hex" {
		t.Fatalf("got order %q, %q; want Base64 before Hex", got[0].Type, got[1].Type)
	}
}

func TestReachableStdlibFnsDeterministicOrder(t *testing.T) {
	reg := stdlib.LoadCached()
	mk := func(mod, name string) ir.Stmt {
		return &ir.ExprStmt{X: &ir.CallExpr{
			Callee: &ir.FieldExpr{X: &ir.Ident{Name: mod}, Name: name},
		}}
	}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			mk("strings", "compare"),
			mk("strings", "compareCI"),
		},
	}
	got := ReachableStdlibFns(mod, reg)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
	if got[0].Fn.Name > got[1].Fn.Name {
		t.Fatalf("got out of order: %q before %q", got[0].Fn.Name, got[1].Fn.Name)
	}
}
