package stdlib

import (
	"strings"
	"testing"
)

func TestRandomModuleDerivedHelpersAreBodied(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["random"]
	if mod == nil {
		t.Fatal("stdlib random module missing")
	}

	for _, name := range []string{"next", "nextBytes", "choice", "shuffle"} {
		fn := reg.LookupMethodDecl("random", "Rng", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(random, Rng, %s) = nil, want stdlib helper", name)
		}
		if fn.Body == nil {
			t.Fatalf("random.Rng.%s body = nil, want Osty helper body", name)
		}
	}
}

func TestRandomShuffleSourcePinsFisherYates(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["random"]
	if mod == nil {
		t.Fatal("stdlib random module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"for i > 1",
		"let j = self.int(0, i + 1)",
		"let tmp = items[i]",
		"items[i] = items[j]",
		"items[j] = tmp",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("random shuffle source missing %q", want)
		}
	}
}
