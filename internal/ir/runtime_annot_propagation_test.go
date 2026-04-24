package ir

import (
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// lowerSrc parses, resolves, and lowers a privileged-mode source
// snippet to an ir.Module. Used by the runtime-annotation IR
// propagation tests.
func lowerSrc(t *testing.T, src string) *Module {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)
	return mod
}

// --- #[no_alloc] -> ir.FnDecl.NoAlloc ---

func TestNoAllocFlowsToFnDecl(t *testing.T) {
	src := `
#[no_alloc]
pub fn pure_arith(a: Int, b: Int) -> Int {
    a + b
}
`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*FnDecl); ok && fd.Name == "pure_arith" {
			if !fd.NoAlloc {
				t.Fatal("ir.FnDecl.NoAlloc not set from #[no_alloc]")
			}
			return
		}
	}
	t.Fatal("pure_arith not in module decls")
}

func TestNoAllocAbsenceIsFalse(t *testing.T) {
	src := `pub fn ordinary() -> Int { 0 }`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*FnDecl); ok && fd.Name == "ordinary" && fd.NoAlloc {
			t.Fatal("plain fn must not carry NoAlloc")
		}
	}
}

// --- #[pod] / #[repr] -> ir.StructDecl.Pod / ReprC ---

func TestPodFlowsToStructDecl(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
pub struct Header {
    pub size: Int32,
}
`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if sd, ok := decl.(*StructDecl); ok && sd.Name == "Header" {
			if !sd.Pod {
				t.Fatal("ir.StructDecl.Pod not set from #[pod]")
			}
			if !sd.ReprC {
				t.Fatal("ir.StructDecl.ReprC not set from #[repr(c)]")
			}
			return
		}
	}
	t.Fatal("Header not in module decls")
}

func TestPodAbsenceIsFalse(t *testing.T) {
	src := `pub struct User { pub name: String }`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if sd, ok := decl.(*StructDecl); ok && sd.Name == "User" {
			if sd.Pod {
				t.Fatal("plain struct must not carry Pod")
			}
			if sd.ReprC {
				t.Fatal("plain struct must not carry ReprC")
			}
			return
		}
	}
	t.Fatal("User not in module decls")
}

// --- #[json(...)] on struct fields -> ir.Field.JSON* ---

func TestJSONFieldAnnotationsFlowToIR(t *testing.T) {
	src := `
pub struct ApiUser {
    #[json(key = "user_id")]
    pub id: Int,
    #[json(key = "full_name")]
    pub name: String,
    #[json(skip)]
    cache: Int,
    #[json(optional)]
    nickname: String?,
    pub age: Int,
}
`
	mod := lowerSrc(t, src)
	var sd *StructDecl
	for _, decl := range mod.Decls {
		if s, ok := decl.(*StructDecl); ok && s.Name == "ApiUser" {
			sd = s
			break
		}
	}
	if sd == nil {
		t.Fatal("ApiUser not in module decls")
	}

	byName := map[string]*Field{}
	for _, f := range sd.Fields {
		byName[f.Name] = f
	}

	cases := []struct {
		field    string
		key      string
		skip     bool
		optional bool
	}{
		{"id", "user_id", false, false},
		{"name", "full_name", false, false},
		{"cache", "", true, false},
		{"nickname", "", false, true},
		{"age", "", false, false},
	}
	for _, c := range cases {
		f := byName[c.field]
		if f == nil {
			t.Errorf("field %q missing from lowered struct", c.field)
			continue
		}
		if f.JSONKey != c.key {
			t.Errorf("field %q JSONKey = %q, want %q", c.field, f.JSONKey, c.key)
		}
		if f.JSONSkip != c.skip {
			t.Errorf("field %q JSONSkip = %v, want %v", c.field, f.JSONSkip, c.skip)
		}
		if f.JSONOptional != c.optional {
			t.Errorf("field %q JSONOptional = %v, want %v", c.field, f.JSONOptional, c.optional)
		}
	}
}

// --- #[json(...)] on enum variants -> ir.Variant.JSON* ---

func TestJSONVariantAnnotationsFlowToIR(t *testing.T) {
	src := `
pub enum Shape {
    #[json(key = "circle")]
    Round(Int),
    #[json(skip)]
    Hidden,
    Square(Int),
}
`
	mod := lowerSrc(t, src)
	var ed *EnumDecl
	for _, decl := range mod.Decls {
		if e, ok := decl.(*EnumDecl); ok && e.Name == "Shape" {
			ed = e
			break
		}
	}
	if ed == nil {
		t.Fatal("Shape not in module decls")
	}

	byName := map[string]*Variant{}
	for _, v := range ed.Variants {
		byName[v.Name] = v
	}

	cases := []struct {
		variant string
		tag     string
		skip    bool
	}{
		{"Round", "circle", false},
		{"Hidden", "", true},
		{"Square", "", false},
	}
	for _, c := range cases {
		v := byName[c.variant]
		if v == nil {
			t.Errorf("variant %q missing from lowered enum", c.variant)
			continue
		}
		if v.JSONTag != c.tag {
			t.Errorf("variant %q JSONTag = %q, want %q", c.variant, v.JSONTag, c.tag)
		}
		if v.JSONSkip != c.skip {
			t.Errorf("variant %q JSONSkip = %v, want %v", c.variant, v.JSONSkip, c.skip)
		}
	}
}

// --- Ensure fields/variants without #[json] leave metadata empty ---

func TestJSONMetadataAbsenceIsZero(t *testing.T) {
	src := `
pub struct Plain { pub x: Int }
pub enum Color { Red, Green }
`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if s, ok := decl.(*StructDecl); ok && s.Name == "Plain" {
			for _, f := range s.Fields {
				if f.JSONKey != "" || f.JSONSkip || f.JSONOptional {
					t.Errorf("plain field carries json metadata: %+v", f)
				}
			}
		}
		if e, ok := decl.(*EnumDecl); ok && e.Name == "Color" {
			for _, v := range e.Variants {
				if v.JSONTag != "" || v.JSONSkip {
					t.Errorf("plain variant carries json metadata: %+v", v)
				}
			}
		}
	}
}

// --- Cloning preserves JSON metadata ---

func TestCloneFieldPreservesJSON(t *testing.T) {
	orig := &Field{
		Name:         "x",
		Type:         TInt,
		Exported:     true,
		JSONKey:      "renamed",
		JSONSkip:     false,
		JSONOptional: true,
	}
	c := cloneField(orig)
	if c.JSONKey != "renamed" || c.JSONOptional != true {
		t.Errorf("cloneField dropped JSON metadata: %+v", c)
	}
}

func TestCloneVariantPreservesJSON(t *testing.T) {
	orig := &Variant{Name: "V", JSONTag: "v-renamed", JSONSkip: true}
	c := cloneVariant(orig)
	if c.JSONTag != "v-renamed" || !c.JSONSkip {
		t.Errorf("cloneVariant dropped JSON metadata: %+v", c)
	}
}

// --- combined: every runtime annotation in one fn ---

func TestAllRuntimeAnnotationsCoexistOnFn(t *testing.T) {
	src := `
#[export("osty.gc.combo_v1")]
#[c_abi]
#[no_alloc]
pub fn combo_v1() -> Int {
    7
}
`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*FnDecl); ok && fd.Name == "combo_v1" {
			if fd.ExportSymbol != "osty.gc.combo_v1" {
				t.Errorf("ExportSymbol = %q", fd.ExportSymbol)
			}
			if !fd.CABI {
				t.Error("CABI not set")
			}
			if !fd.NoAlloc {
				t.Error("NoAlloc not set")
			}
			if fd.IsIntrinsic {
				t.Error("IsIntrinsic must NOT be set (no #[intrinsic])")
			}
			return
		}
	}
	t.Fatal("combo_v1 not in module decls")
}
