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
