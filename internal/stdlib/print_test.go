package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestPrintModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["print"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		t.Fatalf("std.print not loaded with package scope; registry diagnostics:\n%s", stdlibRebindDiagSummary(reg))
	}
	for _, name := range []string{
		"DocumentKind", "Backend", "Orientation", "Duplex", "ColorMode",
		"Document", "PageRange", "Options", "Printer", "CommandPlan", "Job",
	} {
		requirePublicType(t, mod, "print", name)
	}
	for _, name := range []string{
		"options", "withPrinter", "withCopies", "withJobName", "withMedia", "withFitToPage",
		"withOrientation", "withDuplex", "withColorMode", "withPageRanges", "withBackend",
		"pageRange", "file", "pdf", "image", "isPrintable", "defaultPrinterName", "printers",
		"plan", "print", "printFile", "printPdf", "printImage",
	} {
		requirePublicFn(t, mod, "print", name)
	}
}

func TestPrintModuleSourcePinsHostCommandSurface(t *testing.T) {
	src := printModuleSource(t)
	for _, want := range []string{
		"std.print - host printer jobs",
		"pub enum Backend",
		"pub struct Options",
		"pub struct Job",
		"pub fn defaultPrinterName() -> Result<String, Error>",
		"pub fn printers() -> Result<List<Printer>, Error>",
		"pub fn plan(doc: Document, opts: Options) -> Result<CommandPlan, Error>",
		"pub fn print(doc: Document, opts: Options) -> Result<Job, Error>",
		"pub fn printPdf(path: String) -> Result<Job, Error>",
		"pub fn printImage(path: String) -> Result<Job, Error>",
		`os.exec("lpstat", ["-d"])`,
		`CommandPlan { command: "lp", args: args }`,
		`CommandPlan { command: "lpr", args: args }`,
		`Start-Process -FilePath {powerShellQuote(doc.path)} -Verb Print`,
		`fs.exists(doc.path)`,
		`media.detectByName(path)`,
		`strings.startsWith(m, "image/")`,
		`pageRangesSpec(opts.pageRanges)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.print source missing %q", want)
		}
	}
}

func TestPrintImportResolvesPdfAndImageWorkflow(t *testing.T) {
	src := `
use std.print as print

pub fn demo(path: String) -> Result<print.CommandPlan, Error> {
    let mut opts = print.options()
    opts = print.withPrinter(opts, "Office")
    opts = print.withCopies(opts, 2)
    opts = print.withDuplex(opts, print.LongEdge)
    opts = print.withColorMode(opts, print.Grayscale)
    opts = print.withPageRanges(opts, [print.pageRange(1, 3)?])
    let doc = if path.endsWith(".pdf") { print.pdf(path) } else { print.image(path) }
    print.plan(doc, opts)
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, Load())
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		t.Errorf("resolver rejected std.print fixture: %s: %s", d.Code, d.Message)
	}
}

func printModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["print"]
	if mod == nil {
		t.Fatal("stdlib print module missing")
	}
	return string(mod.Source)
}
