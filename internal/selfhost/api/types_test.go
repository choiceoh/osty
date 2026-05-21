package api

import "testing"

func TestCheckResultIndexUsesStableIDs(t *testing.T) {
	result := CheckResult{
		TypedNodes: []CheckedNode{
			{NodeID: 0, Kind: "Root", TypeID: 7},
			{NodeID: 42, Kind: "Call", TypeID: 8},
		},
		Bindings: []CheckedBinding{
			{NodeID: 0, Name: "root", TypeID: 7},
			{NodeID: 42, Name: "value", TypeID: 8},
		},
		Symbols: []CheckedSymbol{
			{NodeID: 42, SymbolID: 21, Name: "value", TypeID: 8},
		},
		Instantiations: []CheckInstantiation{
			{NodeID: 42, InstantiationID: 31, Callee: "id", TypeArgIDs: []int{8}, ResultTypeID: 8},
		},
	}
	result.EnsureStableIDs()

	idx := result.Index()

	if got := idx.TypedNodesByNodeID[0]; got == nil || got.Kind != "Root" {
		t.Fatalf("typed node 0 = %#v, want stable node id 0 Root", got)
	}
	if got := idx.TypedNodesByNodeID[42]; got == nil || got.Kind != "Call" {
		t.Fatalf("typed node 42 = %#v, want Call", got)
	}
	if got := idx.BindingsByNodeID[42]; len(got) != 1 || got[0].Name != "value" {
		t.Fatalf("bindings by node 42 = %#v, want binding value", got)
	}
	if got := idx.SymbolsByID[21]; got == nil || got.Name != "value" {
		t.Fatalf("symbol 21 = %#v, want value", got)
	}
	if got := idx.InstantiationsByID[31]; got == nil || got.Callee != "id" {
		t.Fatalf("instantiation 31 = %#v, want id", got)
	}
	if got := idx.InstantiationsByNodeID[42]; len(got) != 1 || got[0].ResultTypeID != 8 {
		t.Fatalf("instantiations by node 42 = %#v, want result type id 8", got)
	}
	if got := idx.TypedNodesByStableID[result.TypedNodes[1].ID]; got == nil || got.Kind != "Call" {
		t.Fatalf("typed node stable id = %#v, want Call", got)
	}
	if got := idx.TypedNodesByNodeKey[result.TypedNodes[1].NodeKey]; got == nil || got.Kind != "Call" {
		t.Fatalf("typed node node key = %#v, want Call", got)
	}
	if got := idx.BindingsByStableID[result.Bindings[1].ID]; got == nil || got.Name != "value" {
		t.Fatalf("binding stable id = %#v, want value", got)
	}
	if got := idx.SymbolsByStableID[result.Symbols[0].ID]; got == nil || got.Name != "value" {
		t.Fatalf("symbol stable id = %#v, want value", got)
	}
	if got := idx.InstantiationsByStableID[result.Instantiations[0].ID]; got == nil || got.Callee != "id" {
		t.Fatalf("instantiation stable id = %#v, want id", got)
	}
}

func TestStableTypeKeyIgnoresArenaTypeID(t *testing.T) {
	a := CheckResult{TypedNodes: []CheckedNode{{
		Kind:   "Ident",
		TypeID: 1,
		Type:   &TypeRepr{Kind: "named", Name: "Box", Args: []TypeRepr{{Kind: "primitive", Name: "Int"}}},
		Start:  10,
		End:    13,
	}}}
	b := CheckResult{TypedNodes: []CheckedNode{{
		Kind:   "Ident",
		TypeID: 99,
		Type:   &TypeRepr{Kind: "named", Name: "Box", Args: []TypeRepr{{Kind: "primitive", Name: "Int"}}},
		Start:  10,
		End:    13,
	}}}
	a.EnsureStableIDs()
	b.EnsureStableIDs()
	if a.TypedNodes[0].TypeKey == "" {
		t.Fatal("missing type key")
	}
	if a.TypedNodes[0].TypeKey != b.TypedNodes[0].TypeKey {
		t.Fatalf("type keys differ across arena TypeID changes: %q != %q", a.TypedNodes[0].TypeKey, b.TypedNodes[0].TypeKey)
	}
	if a.TypedNodes[0].ID != b.TypedNodes[0].ID {
		t.Fatalf("record ids differ across arena TypeID changes: %q != %q", a.TypedNodes[0].ID, b.TypedNodes[0].ID)
	}
}

func TestNilCheckResultIndexIsEmpty(t *testing.T) {
	idx := (*CheckResult)(nil).Index()
	if len(idx.TypedNodesByNodeID) != 0 ||
		len(idx.TypedNodesByStableID) != 0 ||
		len(idx.BindingsByStableID) != 0 ||
		len(idx.SymbolsByID) != 0 ||
		len(idx.SymbolsByStableID) != 0 ||
		len(idx.InstantiationsByID) != 0 ||
		len(idx.InstantiationsByStableID) != 0 {
		t.Fatalf("nil result index = %#v, want empty maps", idx)
	}
}
