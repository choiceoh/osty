package mir

import (
	"testing"

	"github.com/osty/osty/internal/ir"
)

// TestUseDeclFnTypeReadsImports closes the loop documented in
// `docs/llvm-selfhost-plan-cross-pkg-fn-sig-propagation-design.md`:
// a `*ir.UseDecl.Imports` entry must surface its signature through
// `useDeclFnType` so a cross-pkg call site with poisoned IR-level
// Type recovers the proper return type instead of leaking through as
// void. Without this, the stage0 LLVM emitter saw `declare void` for
// every `tc.fn()` cross-pkg call regardless of the dep's real
// signature.
func TestUseDeclFnTypeReadsImports(t *testing.T) {
	intT := ir.TInt
	stringT := ir.TString
	use := &ir.UseDecl{
		RawPath: "toolchain",
		Alias:   "tc",
		Imports: []ir.Decl{
			&ir.FnDecl{
				Name:   "frontInvalidTypeRepr",
				Params: []*ir.Param{},
				Return: &ir.NamedType{Name: "FrontTypeRepr"},
			},
			&ir.FnDecl{
				Name: "checkLookupFn",
				Params: []*ir.Param{
					{Name: "name", Type: stringT},
					{Name: "owner", Type: stringT},
				},
				Return: intT,
			},
		},
	}

	t.Run("zero-arg user return", func(t *testing.T) {
		sig := useDeclFnType(use, "frontInvalidTypeRepr")
		if sig == nil {
			t.Fatal("useDeclFnType returned nil for known import; want recovered FnType")
		}
		if len(sig.Params) != 0 {
			t.Fatalf("Params length = %d, want 0", len(sig.Params))
		}
		nt, ok := sig.Return.(*ir.NamedType)
		if !ok || nt.Name != "FrontTypeRepr" {
			t.Fatalf("Return = %v, want NamedType{Name:FrontTypeRepr}", sig.Return)
		}
	})

	t.Run("multi-arg primitive return", func(t *testing.T) {
		sig := useDeclFnType(use, "checkLookupFn")
		if sig == nil {
			t.Fatal("useDeclFnType returned nil for known multi-arg import")
		}
		if len(sig.Params) != 2 {
			t.Fatalf("Params length = %d, want 2", len(sig.Params))
		}
		if sig.Return != ir.TInt {
			t.Fatalf("Return = %v, want TInt", sig.Return)
		}
	})

	t.Run("unknown name still nil", func(t *testing.T) {
		// A name that's NOT in Imports must still return nil so the
		// upstream recovery path falls through cleanly — the helper is a
		// best-effort recovery, not a substitute for the checker.
		if sig := useDeclFnType(use, "noSuchFn"); sig != nil {
			t.Fatalf("useDeclFnType(use, \"noSuchFn\") = %+v, want nil", sig)
		}
	})
}

// TestUseDeclFnTypePrefersGoBodyOverImports locks the precedence: when
// both an inline FFI body (GoBody) and a cross-pkg import surface
// (Imports) carry the same name, GoBody wins. This is the pre-existing
// semantics — the FFI body explicitly declared in source is the
// strongest signature signal and must not be overridden by a parallel
// auto-populated cross-pkg entry.
func TestUseDeclFnTypePrefersGoBodyOverImports(t *testing.T) {
	use := &ir.UseDecl{
		RawPath: "net/http",
		IsGoFFI: true,
		GoBody: []ir.Decl{
			&ir.FnDecl{
				Name:   "Get",
				Params: []*ir.Param{{Name: "url", Type: ir.TString}},
				Return: ir.TString,
			},
		},
		// Sanity: an Imports entry with the same name + different return
		// type must NOT win.
		Imports: []ir.Decl{
			&ir.FnDecl{Name: "Get", Return: ir.TInt},
		},
	}
	sig := useDeclFnType(use, "Get")
	if sig == nil {
		t.Fatal("useDeclFnType returned nil for GoBody entry")
	}
	if sig.Return != ir.TString {
		t.Fatalf("Return = %v, want TString (GoBody precedence over Imports)", sig.Return)
	}
}

// TestUseDeclFnTypeNilSafety locks the precondition surface: nil use,
// empty name, empty decl slices all return nil cleanly without
// panicking. The recovery helper is called from MIR's
// `resolveQualifiedCall` and `recoverOperandType` paths and must be
// safe in every shape those paths can construct.
func TestUseDeclFnTypeNilSafety(t *testing.T) {
	if got := useDeclFnType(nil, "anything"); got != nil {
		t.Fatalf("useDeclFnType(nil, ...) = %v, want nil", got)
	}
	if got := useDeclFnType(&ir.UseDecl{}, ""); got != nil {
		t.Fatalf("useDeclFnType(.., empty) = %v, want nil", got)
	}
	if got := useDeclFnType(&ir.UseDecl{}, "missing"); got != nil {
		t.Fatalf("useDeclFnType(empty use, ..) = %v, want nil", got)
	}
}
