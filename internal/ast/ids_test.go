package ast

import (
	"reflect"
	"testing"

	"github.com/osty/osty/internal/token"
)

func TestAssignIDsUniqueAndNonZero(t *testing.T) {
	file := &File{
		PosV: token.Pos{Line: 1, Column: 1},
		EndV: token.Pos{Line: 2, Column: 1},
		Decls: []Decl{
			&LetDecl{
				PosV:  token.Pos{Line: 1, Column: 1},
				EndV:  token.Pos{Line: 1, Column: 10},
				Name:  "x",
				Value: &IntLit{PosV: token.Pos{Line: 1, Column: 9}, Text: "1"},
			},
		},
	}

	AssignIDs(file)

	seen := map[NodeID]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Ptr:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
			return
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
			return
		case reflect.Struct:
		default:
			return
		}
		if f := v.FieldByName("ID"); f.IsValid() && f.Type() == nodeIDType {
			id := NodeID(f.Uint())
			if id == 0 {
				t.Errorf("node %s has zero ID", v.Type().Name())
			}
			if seen[id] {
				t.Errorf("duplicate ID %d on %s", id, v.Type().Name())
			}
			seen[id] = true
		}
		for i := 0; i < v.NumField(); i++ {
			walk(v.Field(i))
		}
	}
	walk(reflect.ValueOf(file))

	if len(seen) == 0 {
		t.Fatal("no IDs assigned")
	}
}

func TestAssignIDsIdempotent(t *testing.T) {
	file := &File{
		Decls: []Decl{&LetDecl{Name: "a"}, &LetDecl{Name: "b"}},
	}
	AssignIDs(file)
	firstFile := file.ID
	firstA := file.Decls[0].(*LetDecl).ID
	firstB := file.Decls[1].(*LetDecl).ID

	AssignIDs(file)
	if file.ID != firstFile || file.Decls[0].(*LetDecl).ID != firstA || file.Decls[1].(*LetDecl).ID != firstB {
		t.Fatal("second AssignIDs call changed existing IDs")
	}
}

func TestAssignIDsNilSafe(t *testing.T) {
	AssignIDs(nil)
	var f *File
	AssignIDs(f)
}
