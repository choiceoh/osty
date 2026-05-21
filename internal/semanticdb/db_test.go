package semanticdb

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost/api"
)

func TestResolveIndexUsesStableIDsAndTargets(t *testing.T) {
	resolved := api.ResolveResult{
		PackageID: "pkg",
		Symbols: []api.ResolvedSymbol{{
			ID:     "sym-helper",
			DeclID: "decl-helper",
			File:   "helper.osty",
			Node:   4,
			Name:   "helper",
			Kind:   "fn",
			Start:  0,
			End:    6,
		}},
		Refs: []api.ResolvedRef{{
			ID:             "ref-helper",
			BindingID:      "binding-helper",
			File:           "main.osty",
			Node:           8,
			Name:           "helper",
			Start:          20,
			End:            26,
			TargetSymbolID: "sym-helper",
			TargetFile:     "helper.osty",
			TargetNode:     4,
			TargetStart:    0,
			TargetEnd:      6,
		}},
		TypeRefs: []api.ResolvedTypeRef{{
			ID:             "type-ref-box",
			File:           "main.osty",
			Node:           9,
			Name:           "Box",
			Start:          30,
			End:            33,
			TargetSymbolID: "sym-box",
		}},
		Diagnostics: []api.ResolveDiagnosticRecord{{
			ID:      "diag",
			Code:    "E0500",
			File:    "main.osty",
			Message: "missing",
			Start:   40,
			End:     41,
		}},
	}
	db := New([]File{{Path: "main.osty", Source: []byte("fn main() {}\n")}}, resolved)

	idx := db.ResolveIndex()
	if idx.SymbolsByStableID["sym-helper"] == nil {
		t.Fatal("missing symbol by stable id")
	}
	if got := idx.SymbolsByTarget[NodeKey{File: "helper.osty", Node: 4, Start: 0, End: 6}]; got == nil || got.Name != "helper" {
		t.Fatalf("symbol by target = %#v, want helper", got)
	}
	if got := idx.RefsByBindingID["binding-helper"]; got == nil || got.TargetSymbolID != "sym-helper" {
		t.Fatalf("ref by binding id = %#v, want helper target", got)
	}
	if refs := idx.RefsByTargetSymbolID["sym-helper"]; len(refs) != 1 || refs[0].Name != "helper" {
		t.Fatalf("refs by target = %#v, want helper ref", refs)
	}
	if typeRefs := idx.TypeRefsByTargetSymbolID["sym-box"]; len(typeRefs) != 1 || typeRefs[0].Name != "Box" {
		t.Fatalf("type refs by target = %#v, want Box ref", typeRefs)
	}
	if diags := idx.DiagnosticsByCode["E0500"]; len(diags) != 1 || diags[0].ID != "diag" {
		t.Fatalf("diagnostics by code = %#v, want diag", diags)
	}
}

func TestWithCheckKeepsResolveDBImmutable(t *testing.T) {
	base := New(nil, api.ResolveResult{PackageID: "pkg"})
	checked := api.CheckResult{
		TypedNodes: []api.CheckedNode{{
			NodeID: 1,
			Kind:   "Ident",
			Start:  0,
			End:    1,
			Type:   &api.TypeRepr{Kind: "primitive", Name: "Int"},
		}},
	}

	withCheck := base.WithCheck(checked)
	if base.Check != nil {
		t.Fatal("WithCheck mutated the original DB")
	}
	if withCheck.Check == nil {
		t.Fatal("WithCheck did not attach checker facts")
	}
	if len(withCheck.CheckIndex().TypedNodesByStableID) != 1 {
		t.Fatalf("check index size = %d, want 1", len(withCheck.CheckIndex().TypedNodesByStableID))
	}
}

func TestFileOffsetHelpers(t *testing.T) {
	original := []byte("let x = 1\n")
	db := New([]File{{
		Path:           "main.osty",
		Base:           10,
		Source:         []byte("let x = 1\n"),
		OriginalSource: original,
	}}, api.ResolveResult{})
	original[0] = 'X'

	file := db.FileForOffset(14)
	if file == nil || file.Path != "main.osty" {
		t.Fatalf("FileForOffset = %#v, want main.osty", file)
	}
	if got := string(file.OriginalSourceBytes()); got != "let x = 1\n" {
		t.Fatalf("OriginalSourceBytes = %q, want cloned original source", got)
	}
	if got, ok := db.PackageOffset("main.osty", 4); !ok || got != 14 {
		t.Fatalf("PackageOffset = (%d, %v), want (14, true)", got, ok)
	}
	if got, ok := db.OriginalOffset("main.osty", 14); !ok || got != 4 {
		t.Fatalf("OriginalOffset = (%d, %v), want (4, true)", got, ok)
	}
}

func TestHoverAtUsesStructuredResolveAndCheckFacts(t *testing.T) {
	src := "fn helper() -> Int { 1 }\nfn main() -> Int { helper() }\n"
	declStart := strings.Index(src, "helper")
	refStart := strings.LastIndex(src, "helper")
	fnType := &api.TypeRepr{
		Kind:   "fn",
		Return: &api.TypeRepr{Kind: "primitive", Name: "Int"},
	}
	db := New([]File{{Path: "main.osty", Source: []byte(src)}}, api.ResolveResult{
		PackageID: "pkg",
		Symbols: []api.ResolvedSymbol{{
			ID:    "sym-helper",
			File:  "main.osty",
			Node:  1,
			Name:  "helper",
			Kind:  "fn",
			Start: declStart,
			End:   declStart + len("helper"),
		}},
		Refs: []api.ResolvedRef{{
			ID:             "ref-helper",
			File:           "main.osty",
			Node:           2,
			Name:           "helper",
			Start:          refStart,
			End:            refStart + len("helper"),
			TargetSymbolID: "sym-helper",
			TargetFile:     "main.osty",
			TargetNode:     1,
			TargetStart:    declStart,
			TargetEnd:      declStart + len("helper"),
		}},
	}).WithCheck(api.CheckResult{
		Symbols: []api.CheckedSymbol{{
			Name:  "helper",
			Kind:  "fn",
			Type:  fnType,
			Start: declStart,
			End:   declStart + len("helper"),
		}},
	})

	hover, ok := db.HoverAt("main.osty", refStart+1)
	if !ok {
		t.Fatal("HoverAt returned !ok for helper ref")
	}
	if hover.Name != "helper" || hover.Kind != "fn" || hover.Type.String() != "fn() -> Int" {
		t.Fatalf("hover = %#v, want helper fn type", hover)
	}
	if hover.Span.Start != refStart || hover.Span.End != refStart+len("helper") {
		t.Fatalf("hover span = %#v, want helper ref span", hover.Span)
	}
}

func TestDefinitionAndReferencesUseStableTargetID(t *testing.T) {
	src := "fn helper() -> Int { 1 }\nfn main() -> Int { helper() }\n"
	declStart := strings.Index(src, "helper")
	refStart := strings.LastIndex(src, "helper")
	db := New([]File{{Path: "main.osty", Source: []byte(src)}}, api.ResolveResult{
		PackageID: "pkg",
		Symbols: []api.ResolvedSymbol{{
			ID:    "sym-helper",
			File:  "main.osty",
			Node:  1,
			Name:  "helper",
			Kind:  "fn",
			Start: declStart,
			End:   declStart + len("helper"),
		}},
		Refs: []api.ResolvedRef{{
			ID:             "ref-helper",
			File:           "main.osty",
			Node:           2,
			Name:           "helper",
			Start:          refStart,
			End:            refStart + len("helper"),
			TargetSymbolID: "sym-helper",
			TargetFile:     "main.osty",
			TargetNode:     1,
			TargetStart:    declStart,
			TargetEnd:      declStart + len("helper"),
		}},
	})

	def, ok := db.DefinitionAt("main.osty", refStart+1)
	if !ok {
		t.Fatal("DefinitionAt returned !ok")
	}
	if def.Start != declStart || def.End != declStart+len("helper") {
		t.Fatalf("definition span = %#v, want helper decl", def)
	}
	refs, target, ok := db.ReferencesAt("main.osty", refStart+1, true)
	if !ok {
		t.Fatal("ReferencesAt returned !ok")
	}
	if target.SymbolID != "sym-helper" {
		t.Fatalf("target symbol id = %q, want sym-helper", target.SymbolID)
	}
	if len(refs) != 2 {
		t.Fatalf("references = %#v, want use + decl", refs)
	}
	if refs[0].Start != refStart || refs[1].Start != declStart {
		t.Fatalf("references = %#v, want use then declaration", refs)
	}
}
