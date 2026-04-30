package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestClipboardModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["clipboard"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		t.Fatalf("std.clipboard not loaded with package scope; registry diagnostics:\n%s", stdlibRebindDiagSummary(reg))
	}

	for _, name := range []string{"readText", "writeText", "read", "write", "clear"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.clipboard missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.clipboard.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.clipboard.%s not public", name)
		}
		fn := reg.LookupFnDecl("clipboard", name)
		if fn == nil || fn.Body == nil {
			t.Fatalf("LookupFnDecl(clipboard, %s) = %v, want bodied fn", name, fn)
		}
	}
}

func TestClipboardModulePinsHostCommandStrategy(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["clipboard"]
	if mod == nil {
		t.Fatal("std.clipboard module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn readText() -> Result<String, Error>`,
		`pub fn writeText(text: String) -> Result<(), Error>`,
		`os.execWith(program, args, "", emptyEnv(), timeoutMillis())`,
		`"pbpaste"`,
		`pbcopy`,
		`"wl-paste"`,
		`wl-copy`,
		`"xclip"`,
		`"xsel"`,
		`"osascript"`,
		`"powershell.exe"`,
		`"pwsh"`,
		`"[Console]::Out.Write((Get-Clipboard -Raw))"`,
		`"Set-Clipboard -Value $args[0]"`,
		`"printf %s \"$1\" | pbcopy"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.clipboard source missing %q", want)
		}
	}
}
