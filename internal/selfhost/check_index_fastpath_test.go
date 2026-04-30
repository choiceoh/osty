package selfhost

import "testing"

func TestCheckFnKeyPreservesOwnerSeparator(t *testing.T) {
	if got, want := checkFnKey("method", "Owner"), "Owner\x1fmethod"; got != want {
		t.Fatalf("checkFnKey = %q, want %q", got, want)
	}
}

func TestCheckOwnerKeyPreservesOwnerSeparator(t *testing.T) {
	if got, want := checkOwnerKey("Type", "field"), "Type\x1ffield"; got != want {
		t.Fatalf("checkOwnerKey = %q, want %q", got, want)
	}
}

func TestCheckNameIndexReturnsLatestMatch(t *testing.T) {
	names := []string{"alpha", "beta", "alpha"}
	hashes := []int{checkHashKey("alpha"), checkHashKey("beta"), checkHashKey("alpha")}
	if got := checkNameIndex(names, hashes, "alpha", checkHashKey("alpha")); got != 2 {
		t.Fatalf("checkNameIndex = %d, want 2", got)
	}
}

func TestCheckLookupExactIndexReturnsLatestValue(t *testing.T) {
	keys := []string{"alpha", "beta", "alpha"}
	hashes := []int{checkHashKey("alpha"), checkHashKey("beta"), checkHashKey("alpha")}
	values := []int{10, 20, 30}
	if got := checkLookupExactIndex(keys, hashes, values, "alpha", checkHashKey("alpha")); got != 30 {
		t.Fatalf("checkLookupExactIndex = %d, want 30", got)
	}
}

func TestCheckFnIndexSlotReturnsLatestRegistration(t *testing.T) {
	arena := emptyTyArena()
	env := emptyCheckEnv(arena)
	first := &CheckFnSig{name: "value", owner: "Box", retTy: tInt(arena)}
	second := &CheckFnSig{name: "value", owner: "Box", retTy: tString(arena)}

	checkRegisterFn(env, first)
	key := checkFnKey("value", "Box")
	firstSlot, ok := env.global.fnIndex.slots[key]
	if !ok {
		t.Fatalf("fnIndex.slots missing key %q", key)
	}

	checkRegisterFn(env, second)
	if got := len(env.global.fnIndex.keys); got != 1 {
		t.Fatalf("fnIndex.keys len = %d, want 1", got)
	}
	if got := env.global.fnIndex.slots[key]; got != firstSlot {
		t.Fatalf("fnIndex.slots[%q] = %d, want %d", key, got, firstSlot)
	}
	if got := checkLookupFn(env, "value", "Box"); got != second {
		t.Fatalf("checkLookupFn returned %#v, want latest registration", got)
	}
}

func TestCheckNameIndexTableReturnsLatestValue(t *testing.T) {
	table := emptyCheckNameIndexTable(1)
	checkNameIndexTableSet(&table, "alpha", 10)
	checkNameIndexTableSet(&table, "beta", 20)
	checkNameIndexTableSet(&table, "alpha", 30)

	if got := len(table.keys); got != 2 {
		t.Fatalf("table keys len = %d, want 2", got)
	}
	if got := checkNameIndexTableGet(&table, "alpha"); got != 30 {
		t.Fatalf("checkNameIndexTableGet = %d, want 30", got)
	}
}

func TestCheckStackIndexesUseSlotFastPath(t *testing.T) {
	arena := emptyTyArena()
	bindings := emptyCheckBindingStackIndex(1)
	checkBindingStackIndexPush(&bindings, &CheckBinding{name: "item", ty: tInt(arena), mutable: false})
	checkBindingStackIndexPush(&bindings, &CheckBinding{name: "item", ty: tString(arena), mutable: true})

	if got := len(bindings.keys); got != 1 {
		t.Fatalf("binding index keys len = %d, want 1", got)
	}
	if got := checkBindingStackIndexTop(&bindings, "item"); got == nil || got.ty != tString(arena) {
		t.Fatalf("binding top = %#v, want latest string binding", got)
	}

	bounds := emptyCheckIntStackIndex(1)
	checkIntStackIndexPush(&bounds, "T", 1)
	checkIntStackIndexPush(&bounds, "T", 2)
	if got := len(bounds.keys); got != 1 {
		t.Fatalf("generic bound keys len = %d, want 1", got)
	}
	if got := checkIntStackIndexValues(&bounds, "T"); len(got) != 2 || got[1] != 2 {
		t.Fatalf("generic bound stack = %#v, want [1 2]", got)
	}
}

func TestCheckHashKeyIsStable(t *testing.T) {
	if got, want := checkHashKey("hello::world"), checkHashKey("hello::world"); got != want {
		t.Fatalf("checkHashKey is not stable: %d != %d", got, want)
	}
}

func TestCheckOwnerKeyHashMatchesJoinedKeyHash(t *testing.T) {
	owner, name := "Owner", "method"
	key := checkOwnerKey(owner, name)
	if got, want := checkHashKey(key), checkHashKey(checkOwnerKey(owner, name)); got != want {
		t.Fatalf("checkHashKey(owner key) = %d, want %d", got, want)
	}
}

func TestCheckNameIndexOwnerKeyReturnsLatestMatch(t *testing.T) {
	keys := []string{
		checkOwnerKey("One", "field"),
		checkOwnerKey("Two", "field"),
		checkOwnerKey("One", "field"),
	}
	hashes := []int{
		checkHashKey(checkOwnerKey("One", "field")),
		checkHashKey(checkOwnerKey("Two", "field")),
		checkHashKey(checkOwnerKey("One", "field")),
	}
	key := checkOwnerKey("One", "field")
	if got := checkNameIndex(keys, hashes, key, checkHashKey(key)); got != 2 {
		t.Fatalf("checkNameIndex(owner key) = %d, want 2", got)
	}
}

func TestCheckLookupExactIndexOwnerKeyReturnsLatestValue(t *testing.T) {
	keys := []string{
		checkOwnerKey("One", "field"),
		checkOwnerKey("Two", "field"),
		checkOwnerKey("One", "field"),
	}
	hashes := []int{
		checkHashKey(checkOwnerKey("One", "field")),
		checkHashKey(checkOwnerKey("Two", "field")),
		checkHashKey(checkOwnerKey("One", "field")),
	}
	values := []int{10, 20, 30}
	key := checkOwnerKey("One", "field")
	if got := checkLookupExactIndex(keys, hashes, values, key, checkHashKey(key)); got != 30 {
		t.Fatalf("checkLookupExactIndex(owner key) = %d, want 30", got)
	}
}
