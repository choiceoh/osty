package resolve

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/token"
)

func TestBridgeTypeRefsFiltersRecordsByFile(t *testing.T) {
	current := nativeResolveFileInfo{path: "current.osty", base: 0, source: []byte("List<Int>\n")}
	other := nativeResolveFileInfo{path: "other.osty", base: 100, source: []byte("pub struct FrontCheckResult {}\n")}
	nt := &ast.NamedType{
		ID:   ast.NodeID(1),
		PosV: token.Pos{Offset: 0},
		EndV: token.Pos{Offset: 4},
		Path: []string{"List"},
	}

	refsByID, idents := bridgeTypeRefs(
		[]api.ResolvedTypeRef{{
			Name:        "FrontCheckResult",
			File:        other.path,
			Start:       0,
			End:         16,
			TargetNode:  7,
			TargetStart: other.base,
			TargetEnd:   other.base + len("FrontCheckResult"),
			TargetFile:  other.path,
		}},
		[]api.ResolvedSymbol{{
			Name:  "FrontCheckResult",
			Kind:  "type",
			File:  other.path,
			Node:  7,
			Start: other.base,
			End:   other.base + len("FrontCheckResult"),
		}},
		[]nativeResolveFileInfo{current, other},
		current,
		map[int]*ast.NamedType{0: nt},
		map[string]map[int]ast.Node{
			other.path: {
				0: &ast.StructDecl{Name: "FrontCheckResult", PosV: token.Pos{Offset: 0}},
			},
		},
		nil,
	)

	if len(refsByID) != 0 || len(idents) != 0 {
		t.Fatalf("bridgeTypeRefs accepted ref from %q while projecting %q: refs=%#v idents=%#v", other.path, current.path, refsByID, idents)
	}
}

func TestBridgeRefsFiltersRecordsByFile(t *testing.T) {
	current := nativeResolveFileInfo{path: "current.osty", base: 0, source: []byte("helper()\n")}
	other := nativeResolveFileInfo{path: "other.osty", base: 100, source: []byte("pub fn helper() -> Int { 1 }\n")}
	ident := &ast.Ident{
		ID:   ast.NodeID(2),
		PosV: token.Pos{Offset: 0},
		EndV: token.Pos{Offset: len("helper")},
		Name: "helper",
	}

	refsByID, idents := bridgeRefs(
		[]api.ResolvedRef{{
			Name:        "helper",
			File:        other.path,
			Start:       0,
			End:         len("helper"),
			TargetNode:  3,
			TargetStart: other.base,
			TargetEnd:   other.base + len("helper"),
			TargetFile:  other.path,
		}},
		[]api.ResolvedSymbol{{
			Name:  "helper",
			Kind:  "fn",
			File:  other.path,
			Node:  3,
			Start: other.base,
			End:   other.base + len("helper"),
		}},
		[]nativeResolveFileInfo{current, other},
		current,
		map[int]*ast.Ident{0: ident},
		map[string]map[int]ast.Node{
			other.path: {
				0: &ast.FnDecl{Name: "helper", PosV: token.Pos{Offset: 0}},
			},
		},
		nil,
	)

	if len(refsByID) != 0 || len(idents) != 0 {
		t.Fatalf("bridgeRefs accepted ref from %q while projecting %q: refs=%#v idents=%#v", other.path, current.path, refsByID, idents)
	}
}

func TestDefineTopLevelSymbolsFiltersRecordsByFile(t *testing.T) {
	current := nativeResolveFileInfo{path: "current.osty", base: 0, source: []byte("struct Current {}\n")}
	scope := NewScope(nil, "package:test")

	defineTopLevelSymbols(
		scope,
		[]api.ResolvedSymbol{{
			Name:  "Other",
			Kind:  "type",
			File:  "other.osty",
			Depth: 0,
			Start: 0,
			End:   len("Other"),
		}},
		current,
		map[int]ast.Node{
			0: &ast.StructDecl{Name: "Current", PosV: token.Pos{Offset: 0}},
		},
	)

	if sym := scope.LookupLocal("Other"); sym != nil {
		t.Fatalf("defineTopLevelSymbols defined symbol from another file: %#v", sym)
	}
}
