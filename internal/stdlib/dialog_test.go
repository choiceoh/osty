package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestDialogModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["dialog"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		t.Fatalf("std.dialog not loaded with package scope; registry diagnostics:\n%s", stdlibRebindDiagSummary(reg))
	}

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"Mode", resolve.SymEnum},
		{"Backend", resolve.SymEnum},
		{"Filter", resolve.SymStruct},
		{"Options", resolve.SymStruct},
		{"CommandPlan", resolve.SymStruct},
		{"Selection", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.dialog missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.dialog.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.dialog.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"filter", "extensionFilter", "anyFileFilter",
		"openFileOptions", "openFilesOptions", "folderOptions", "saveFileOptions",
		"selectFile", "selectFiles", "selectFolder", "saveFile",
		"plan", "plans", "run", "runPlan", "commandLine",
		"backendName", "modeName",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.dialog missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.dialog.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.dialog.%s not public", name)
		}
	}
}

func TestDialogModulePinsCommandBackends(t *testing.T) {
	src := dialogModuleSource(t)
	for _, want := range []string{
		"std.dialog -- desktop file/folder/save dialogs",
		"pub enum Mode",
		"OpenFile",
		"OpenFiles",
		"OpenFolder",
		"SaveFile",
		"pub enum Backend",
		"MacOSAppleScript",
		"WindowsPowerShell",
		"Zenity",
		"KDialog",
		"CustomCommand(String, List<String>)",
		"pub fn selectFile(title: String) -> Result<String?, Error>",
		"pub fn selectFiles(options: Options) -> Result<List<String>, Error>",
		"pub fn selectFolder(title: String) -> Result<String?, Error>",
		"pub fn saveFile(title: String, defaultName: String) -> Result<String?, Error>",
		"pub fn plans(mode: Mode, options: Options) -> Result<List<CommandPlan>, Error>",
		"pub fn run(mode: Mode, options: Options) -> Result<Selection, Error>",
		"command: \"osascript\"",
		"choose file with prompt",
		"choose folder with prompt",
		"choose file name with prompt",
		"New-Object System.Windows.Forms.OpenFileDialog",
		"New-Object System.Windows.Forms.FolderBrowserDialog",
		"New-Object System.Windows.Forms.SaveFileDialog",
		"command: \"zenity\"",
		"--file-selection",
		"--directory",
		"--save",
		"command: \"kdialog\"",
		"--getexistingdirectory",
		"--getsavefilename",
		"os.exec(plan.command, plan.args)?",
		"strings.splitLines(stdout)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.dialog source missing %q", want)
		}
	}
}

func TestDialogModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"Options", []string{"withTitle", "withInitialDirectory", "withDefaultName", "withMultiple", "withConfirmOverwrite", "withShowHidden", "withBackend", "withFilter", "withFilters"}},
		{"Selection", []string{"first", "pathOr"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("dialog", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(dialog, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("dialog.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func TestDialogModuleImportSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["dialog"]
	if mod == nil || mod.Package == nil {
		t.Fatal("stdlib dialog module missing package")
	}
	surface := resolve.PackageExportSurface("std.dialog", "dialog", mod.Package)
	for _, want := range []string{
		"selectFile",
		"selectFiles",
		"selectFolder",
		"saveFile",
		"plans",
		"run",
		"runPlan",
		"commandLine",
	} {
		found := false
		for _, fn := range surface.Functions {
			if fn.Owner == "dialog" && fn.Name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("std.dialog import surface missing alias function %q; functions=%d fields=%d types=%d",
				want, len(surface.Functions), len(surface.Fields), len(surface.TypeDecls))
		}
	}
}

func dialogModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["dialog"]
	if mod == nil {
		t.Fatal("stdlib dialog module missing")
	}
	return string(mod.Source)
}
