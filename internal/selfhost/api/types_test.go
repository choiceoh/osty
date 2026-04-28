package api

import "testing"

func TestCheckResultIndexUsesStableIDs(t *testing.T) {
	result := CheckResult{
		TypedNodes: []CheckedNode{
			{Node: 0, NodeID: 0, Kind: "Root", TypeID: 7},
			{Node: 901, NodeID: 42, Kind: "Call", TypeID: 8},
		},
		Bindings: []CheckedBinding{
			{Node: 0, NodeID: 0, BindingID: 11, Name: "root", TypeID: 7},
			{Node: 901, NodeID: 42, BindingID: 12, Name: "value", TypeID: 8},
		},
		Symbols: []CheckedSymbol{
			{Node: 901, NodeID: 42, SymbolID: 21, Name: "value", TypeID: 8},
		},
		Instantiations: []CheckInstantiation{
			{Node: 901, NodeID: 42, InstantiationID: 31, Callee: "id", TypeArgIDs: []int{8}, ResultTypeID: 8},
		},
	}

	idx := result.Index()

	if got := idx.TypedNodesByNodeID[0]; got == nil || got.Kind != "Root" {
		t.Fatalf("typed node 0 = %#v, want stable node id 0 Root", got)
	}
	if got := idx.TypedNodesByNodeID[42]; got == nil || got.Kind != "Call" {
		t.Fatalf("typed node 42 = %#v, want Call", got)
	}
	if got := idx.BindingsByID[12]; got == nil || got.Name != "value" {
		t.Fatalf("binding 12 = %#v, want value", got)
	}
	if got := idx.BindingsByNodeID[42]; len(got) != 1 || got[0].BindingID != 12 {
		t.Fatalf("bindings by node 42 = %#v, want binding 12", got)
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
}

func TestCheckResultIndexFallsBackToLegacyNodeAlias(t *testing.T) {
	result := CheckResult{
		TypedNodes: []CheckedNode{{Node: 77, Kind: "IntLit"}},
		Bindings:   []CheckedBinding{{Node: 77, BindingID: 1, Name: "value"}},
	}

	idx := result.Index()
	if got := idx.TypedNodesByNodeID[77]; got == nil || got.Kind != "IntLit" {
		t.Fatalf("legacy typed node 77 = %#v, want IntLit", got)
	}
	if got := idx.BindingsByNodeID[77]; len(got) != 1 || got[0].Name != "value" {
		t.Fatalf("legacy bindings by node 77 = %#v, want value", got)
	}
}

func TestNilCheckResultIndexIsEmpty(t *testing.T) {
	idx := (*CheckResult)(nil).Index()
	if len(idx.TypedNodesByNodeID) != 0 || len(idx.BindingsByID) != 0 || len(idx.SymbolsByID) != 0 || len(idx.InstantiationsByID) != 0 {
		t.Fatalf("nil result index = %#v, want empty maps", idx)
	}
}
