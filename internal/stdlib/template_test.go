package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestTemplateModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["template"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.template not loaded")
	}
	for _, name := range []string{"render", "renderLoose", "renderRaw", "escapeHtml", "stripTags"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.template missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.template.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.template.%s not public", name)
		}
	}
}

func TestTemplateModuleSourcePinsRendererBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["template"]
	if mod == nil {
		t.Fatal("std.template module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`{{name}}`,
		`{{{name}}}`,
		`pub fn escapeHtml(text: String) -> String`,
		`return Err(error.new("template: missing value: {name}"))`,
		`strings.trimSpace(strings.slice(source, i + 2, end))`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.template source missing %q", want)
		}
	}
}
