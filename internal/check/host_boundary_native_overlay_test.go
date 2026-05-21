package check

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

func TestOverlaySelfhostResultPrefersNativeNodeIDForTypedNodes(t *testing.T) {
	start := token.Pos{Line: 1, Column: 1, Offset: 0}
	end := token.Pos{Line: 1, Column: 2, Offset: 1}
	first := &ast.Ident{ID: 12, PosV: start, EndV: end, Name: "first"}
	second := &ast.Ident{ID: 2, PosV: start, EndV: end, Name: "second"}
	file := &ast.File{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{ID: 20, X: first},
			&ast.ExprStmt{ID: 21, X: second},
		},
		PosV: start,
		EndV: end,
	}
	result := &Result{
		Types:              map[ast.Expr]types.Type{},
		LetTypes:           map[ast.Node]types.Type{},
		SymTypes:           map[*resolve.Symbol]types.Type{},
		InstantiationsByID: map[ast.NodeID][]types.Type{},
	}

	overlaySelfhostResult(result, nativeIDTestSource(file), api.CheckResult{
		TypedNodes: []api.CheckedNode{{
			NodeID: 0,
			Kind:   "Ident",
			Type:   &api.TypeRepr{Kind: "primitive", Name: "Int"},
			Start:  0,
			End:    1,
		}},
	})

	if got := result.Types[second]; got != types.Int {
		t.Fatalf("second type = %v, want Int", got)
	}
	if got := result.Types[first]; got != nil {
		t.Fatalf("first type = %v, want nil; overlay fell back to span match", got)
	}
}

func TestOverlaySelfhostResultPrefersNativeNodeIDForInstantiations(t *testing.T) {
	start := token.Pos{Line: 1, Column: 1, Offset: 0}
	end := token.Pos{Line: 1, Column: 5, Offset: 4}
	firstFn := &ast.Ident{ID: 30, PosV: start, EndV: end, Name: "id"}
	secondFn := &ast.Ident{ID: 31, PosV: start, EndV: end, Name: "id"}
	first := &ast.CallExpr{ID: 20, PosV: start, EndV: end, Fn: firstFn}
	second := &ast.CallExpr{ID: 21, PosV: start, EndV: end, Fn: secondFn}
	file := &ast.File{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{ID: 22, X: first},
			&ast.ExprStmt{ID: 23, X: second},
		},
		PosV: start,
		EndV: end,
	}
	result := &Result{
		Types:              map[ast.Expr]types.Type{},
		LetTypes:           map[ast.Node]types.Type{},
		SymTypes:           map[*resolve.Symbol]types.Type{},
		InstantiationsByID: map[ast.NodeID][]types.Type{},
	}

	overlaySelfhostResult(result, nativeIDTestSource(file), api.CheckResult{
		Instantiations: []api.CheckInstantiation{{
			NodeID:   18,
			Callee:   "id",
			TypeArgs: []api.TypeRepr{{Kind: "primitive", Name: "Int"}},
			Start:    0,
			End:      4,
		}},
	})

	if got := result.InstantiationsByID[first.ID]; len(got) != 1 || got[0] != types.Int {
		t.Fatalf("first instantiation = %v, want [Int]", got)
	}
	if got := result.InstantiationsByID[second.ID]; got != nil {
		t.Fatalf("second instantiation = %v, want nil; overlay fell back to span match", got)
	}
}

func TestOverlaySelfhostResultPrefersNativeNodeIDForBindings(t *testing.T) {
	start := token.Pos{Line: 1, Column: 5, Offset: 4}
	end := token.Pos{Line: 1, Column: 10, Offset: 9}
	firstPat := &ast.IdentPat{ID: 40, PosV: start, EndV: end, Name: "value"}
	secondPat := &ast.IdentPat{ID: 41, PosV: start, EndV: end, Name: "value"}
	first := &ast.LetStmt{ID: 42, PosV: start, EndV: end, Pattern: firstPat}
	second := &ast.LetStmt{ID: 43, PosV: start, EndV: end, Pattern: secondPat}
	file := &ast.File{
		Stmts: []ast.Stmt{first, second},
		PosV:  start,
		EndV:  end,
	}
	result := &Result{
		Types:              map[ast.Expr]types.Type{},
		LetTypes:           map[ast.Node]types.Type{},
		SymTypes:           map[*resolve.Symbol]types.Type{},
		InstantiationsByID: map[ast.NodeID][]types.Type{},
	}

	overlaySelfhostResult(result, nativeIDTestSource(file), api.CheckResult{
		Bindings: []api.CheckedBinding{{
			NodeID: 39,
			Name:   "value",
			Type:   &api.TypeRepr{Kind: "primitive", Name: "Bool"},
			Start:  4,
			End:    9,
		}},
	})

	if got := result.LetTypes[second]; got != types.Bool {
		t.Fatalf("second binding type = %v, want Bool", got)
	}
	if got := result.LetTypes[first]; got != nil {
		t.Fatalf("first binding type = %v, want nil; overlay fell back to span match", got)
	}
}

func nativeIDTestSource(file *ast.File) selfhostCheckedSource {
	return selfhostCheckedSource{
		source: []byte("test\n"),
		files: []selfhostFileSegment{{
			file:          file,
			nativeNodeIDs: true,
		}},
	}
}
