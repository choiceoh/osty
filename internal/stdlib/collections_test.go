package stdlib

import (
	"strings"
	"testing"
)

func TestCollectionsListReverseIsBodied(t *testing.T) {
	reg := LoadCached()
	fn := reg.LookupMethodDecl("collections", "List", "reverse")
	if fn == nil {
		t.Fatal("LookupMethodDecl(collections, List, reverse) = nil, want stdlib helper")
	}
	if fn.Body == nil {
		t.Fatal("List.reverse body = nil, want Osty helper body")
	}
}

func TestCollectionsListReverseSourcePinsSwapLoop(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["collections"]
	if mod == nil {
		t.Fatal("stdlib collections module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"let mut lo = 0",
		"let mut hi = self.len()",
		"for lo < hi",
		"let tmp = self[lo]",
		"self[lo] = self[hi]",
		"self[hi] = tmp",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("List.reverse source missing %q", want)
		}
	}
}
