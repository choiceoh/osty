package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestXmlModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["xml"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.xml not loaded")
	}
	for _, name := range []string{"attr", "escape", "unescape", "isName", "startTag", "endTag", "emptyElement", "element", "tokenize"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.xml missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.xml.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.xml.%s not public", name)
		}
	}
	for _, name := range []string{"XmlAttr", "XmlTag", "XmlToken"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.xml missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.xml.%s not public", name)
		}
	}
}

func TestXmlModuleSourcePinsTokenizerBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["xml"]
	if mod == nil {
		t.Fatal("std.xml module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn tokenize(source: String) -> Result<List<XmlToken>, Error>`,
		`startsAt(source, i, "<!--")`,
		`tokens.push(XmlStart(tag))`,
		`tokens.push(XmlEnd(name))`,
		`return Err(error.new("xml: unknown entity"))`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.xml source missing %q", want)
		}
	}
}
