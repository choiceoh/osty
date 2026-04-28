package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

// TestCharModuleSurface asserts that std.char loads cleanly and
// exports every public function the module contract promises. A
// missing export usually means a rename without a corresponding
// caller-side update, or a new typo in the module source.
func TestCharModuleSurface(t *testing.T) {
	reg := Load()

	mod := reg.Modules["char"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.char not loaded")
	}

	want := []string{
		"codepoint",
		"isAscii",
		"isAsciiDigit",
		"isAsciiHexDigit",
		"isAsciiOctalDigit",
		"isAsciiBinaryDigit",
		"isAsciiUpper",
		"isAsciiLower",
		"isAsciiAlpha",
		"isAsciiAlphanumeric",
		"isAsciiWhitespace",
		"isAsciiControl",
		"isAsciiPrintable",
		"isAsciiGraphic",
		"isAsciiPunctuation",
		"isNewline",
		"toAsciiUpper",
		"toAsciiLower",
		"eqIgnoreAsciiCase",
		"digitValue",
		"hexDigitValue",
		"octalDigitValue",
		"toDigit",
		"fromAsciiDigit",
		"fromDigit",
	}
	for _, name := range want {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.char missing export %q", name)
			continue
		}
		if sym.Kind != resolve.SymFn {
			t.Errorf("std.char.%s kind = %s, want SymFn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Errorf("std.char.%s not public", name)
		}
	}
}

func TestCharModuleSourcePinsAsciiContracts(t *testing.T) {
	src := charModuleSource(t)
	for _, want := range []string{
		"'0' <= c && c <= '9'",
		"('a' <= c && c <= 'f')",
		"radix < 2 || radix > 36",
		"toAsciiLower(a) == toAsciiLower(b)",
		"('a'.toInt() + d - 10).toChar()",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.char source missing %q", want)
		}
	}
}

func charModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["char"]
	if mod == nil {
		t.Fatal("stdlib char module missing")
	}
	return string(mod.Source)
}
