package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestI18nModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["i18n"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.i18n not loaded")
	}
	for _, name := range []string{"locale", "parseLocale", "normalizeTag", "fallbackTags", "catalog", "translate", "translateDefault", "format", "pluralCategory", "selectPlural"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.i18n missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.i18n.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.i18n.%s not public", name)
		}
	}
	for _, name := range []string{"Locale", "MessageCatalog"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.i18n missing type %q", name)
		}
		if sym.Kind != resolve.SymStruct {
			t.Fatalf("std.i18n.%s kind = %s, want struct", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.i18n.%s not public", name)
		}
	}
}

func TestI18nModuleSourcePinsCatalogBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["i18n"]
	if mod == nil {
		t.Fatal("std.i18n module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn fallbackTags(tag: String) -> List<String>`,
		`pub fn format(message: String, values: Map<String, String>) -> String`,
		`message[i].toInt() == 123`,
		`pub fn pluralCategory(tag: String, count: Int) -> String`,
		`lang == "ja" || lang == "ko" || lang == "zh"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.i18n source missing %q", want)
		}
	}
}
