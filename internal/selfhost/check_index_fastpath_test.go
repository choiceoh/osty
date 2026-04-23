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

func TestCheckHashOwnerNameMatchesJoinedKeyHash(t *testing.T) {
	owner, name := "Owner", "method"
	if got, want := checkHashOwnerName(owner, name), checkHashKey(checkOwnerKey(owner, name)); got != want {
		t.Fatalf("checkHashOwnerName = %d, want %d", got, want)
	}
}

func TestCheckNameIndexOwnerNameReturnsLatestMatch(t *testing.T) {
	keys := []string{
		checkOwnerKey("One", "field"),
		checkOwnerKey("Two", "field"),
		checkOwnerKey("One", "field"),
	}
	hashes := []int{
		checkHashOwnerName("One", "field"),
		checkHashOwnerName("Two", "field"),
		checkHashOwnerName("One", "field"),
	}
	if got := checkNameIndexOwnerName(keys, hashes, "One", "field", checkHashOwnerName("One", "field")); got != 2 {
		t.Fatalf("checkNameIndexOwnerName = %d, want 2", got)
	}
}

func TestCheckLookupExactIndexOwnerNameReturnsLatestValue(t *testing.T) {
	keys := []string{
		checkOwnerKey("One", "field"),
		checkOwnerKey("Two", "field"),
		checkOwnerKey("One", "field"),
	}
	hashes := []int{
		checkHashOwnerName("One", "field"),
		checkHashOwnerName("Two", "field"),
		checkHashOwnerName("One", "field"),
	}
	values := []int{10, 20, 30}
	if got := checkLookupExactIndexOwnerName(keys, hashes, values, "One", "field", checkHashOwnerName("One", "field")); got != 30 {
		t.Fatalf("checkLookupExactIndexOwnerName = %d, want 30", got)
	}
}
