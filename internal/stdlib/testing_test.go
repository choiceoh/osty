package stdlib

import (
	"strings"
	"testing"
)

func TestTestingModuleSurface(t *testing.T) {
	reg := LoadCached()
	if reg.Modules["testing"] == nil {
		t.Fatal("stdlib testing module missing")
	}
	for _, name := range []string{
		"assert", "assertTrue", "assertFalse", "assertEq", "assertNe",
		"expectOk", "expectError", "fail", "context", "benchmark",
		"snapshot", "property", "propertyN", "propertySeeded",
	} {
		if fn := reg.LookupFnDecl("testing", name); fn == nil {
			t.Fatalf("LookupFnDecl(testing, %s) = nil", name)
		}
	}
}

func TestTestingGenModuleSurface(t *testing.T) {
	reg := LoadCached()
	if reg.Modules["testing_gen"] == nil {
		t.Fatal("stdlib testing_gen module missing")
	}
	for _, name := range []string{
		"int", "intRange", "bool", "float", "char", "byte", "asciiString",
		"oneOf", "map", "filter", "pair", "triple", "list", "listOfSize",
		"option", "result", "constant", "oneOfGens",
	} {
		if fn := reg.LookupFnDecl("testing_gen", name); fn == nil {
			t.Fatalf("LookupFnDecl(testing_gen, %s) = nil", name)
		}
	}
}

func TestTestingGenModulePinsImplementedCombinators(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["testing_gen"]
	if mod == nil {
		t.Fatal("stdlib testing_gen module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"pub fn list<T>",
		"pub fn listOfSize<T>",
		"pub fn option<T>",
		"pub fn result<T, E>",
		"pub fn oneOfGens<T>",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("testing_gen source missing %q", want)
		}
	}
}
