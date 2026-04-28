package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestCliModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["cli"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.cli not loaded")
	}
	for _, name := range []string{"flag", "option", "optionDefault", "requiredOption", "parse", "parseEnv", "usage"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.cli missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.cli.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.cli.%s not public", name)
		}
	}
	for _, name := range []string{"FlagSpec", "ParsedArgs"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.cli missing type %q", name)
		}
		if sym.Kind != resolve.SymStruct {
			t.Fatalf("std.cli.%s kind = %s, want struct", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.cli.%s not public", name)
		}
	}
}

func TestCliModuleSourcePinsParserBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["cli"]
	if mod == nil {
		t.Fatal("std.cli module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn parse(args: List<String>, specs: List<FlagSpec>) -> ParsedArgs`,
		`pub fn parseEnv(specs: List<FlagSpec>) -> ParsedArgs`,
		`arg == "--"`,
		`splitInlineValue(strings.slice(arg, 2, arg.len()))`,
		`errors.push("cli: missing required option --{spec.name}")`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.cli source missing %q", want)
		}
	}
}
