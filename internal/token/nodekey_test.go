package token

import "testing"

func TestNodeKeyOfMirrorsOffset(t *testing.T) {
	p := Pos{Offset: 42, Line: 7, Column: 3}
	k := NodeKeyOf(p)
	if k.Offset != 42 {
		t.Fatalf("NodeKeyOf dropped offset: got %d", k.Offset)
	}
	// Line / Column intentionally not part of the key — offset is
	// enough to disambiguate within a single file.
}

func TestNodeKeyEqualityAcrossConstructions(t *testing.T) {
	a := NodeKey{Offset: 10}
	b := NodeKeyOf(Pos{Offset: 10, Line: 2, Column: 5})
	if a != b {
		t.Fatalf("same offset must produce equal NodeKey; got %+v vs %+v", a, b)
	}
}

func TestNodeKeyDistinctByOffset(t *testing.T) {
	a := NodeKey{Offset: 10}
	b := NodeKey{Offset: 11}
	if a == b {
		t.Fatal("different offsets must produce distinct NodeKey values")
	}
}

func TestPackageNodeKeyCombinesPathAndOffset(t *testing.T) {
	a := PackageNodeKeyOf("/pkg/a.osty", Pos{Offset: 10})
	b := PackageNodeKeyOf("/pkg/b.osty", Pos{Offset: 10})
	if a == b {
		t.Fatal("same offset in different files must produce distinct PackageNodeKey")
	}
	c := PackageNodeKeyOf("/pkg/a.osty", Pos{Offset: 10})
	if a != c {
		t.Fatal("same path + offset must produce equal PackageNodeKey")
	}
}
