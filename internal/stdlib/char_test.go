package stdlib

import (
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
