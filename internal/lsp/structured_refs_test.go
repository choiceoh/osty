package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

func TestStructuredReferencesUseSelfhostTargetIDs(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, "helper.osty")
	mainPath := filepath.Join(dir, "main.osty")
	helperSrc := []byte("pub fn helper(x: Int) -> Int { x }\n")
	mainSrc := []byte("fn main() {\n    let value = helper(1)\n}\n")
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	analysis := s.analyzePackageContaining(mainPath, mainSrc)
	if analysis == nil {
		t.Fatal("analyzePackageContaining returned nil")
	}
	if len(analysis.structuredRefs) == 0 {
		t.Fatal("analysis missing structured references")
	}
	if len(analysis.structuredSymbols) == 0 {
		t.Fatal("analysis missing structured symbols")
	}

	doc := &document{
		uri:      pathToURI(mainPath),
		src:      mainSrc,
		analysis: analysis,
	}
	helperOff := bytes.Index(mainSrc, []byte("helper"))
	if helperOff < 0 {
		t.Fatal("test source missing helper call")
	}
	ref := structuredReferenceAt(doc, analysis.lines.offsetToLSP(helperOff))
	if ref == nil {
		t.Fatalf("structuredReferenceAt missed helper call at %d", helperOff)
	}
	if ref.targetSymbolID == "" {
		t.Fatal("structured reference missing target symbol id")
	}
	if ref.targetKind != "function" {
		t.Fatalf("target kind = %q, want function", ref.targetKind)
	}
	if ref.targetURI != pathToURI(helperPath) {
		t.Fatalf("target URI = %q, want %q", ref.targetURI, pathToURI(helperPath))
	}

	helperDoc := &document{
		uri:      pathToURI(helperPath),
		src:      helperSrc,
		analysis: analysis,
	}
	helperDeclOff := bytes.Index(helperSrc, []byte("helper"))
	decl := structuredSymbolAt(helperDoc, newLineIndex(helperSrc).offsetToLSP(helperDeclOff))
	if decl == nil {
		t.Fatalf("structuredSymbolAt missed helper declaration at %d", helperDeclOff)
	}
	if decl.id != ref.targetSymbolID {
		t.Fatalf("decl id = %q, want reference target id %q", decl.id, ref.targetSymbolID)
	}

	locs := structuredReferencesForTarget(analysis.structuredRefs, analysis.structuredSymbols, ref.targetSymbolID, true)
	if len(locs) != 2 {
		t.Fatalf("locations = %#v, want call + declaration", locs)
	}
	wantDeclRange := newLineIndex(helperSrc).rangeFromOffsets(
		bytes.Index(helperSrc, []byte("helper")),
		bytes.Index(helperSrc, []byte("helper"))+len("helper"),
	)
	var sawDecl bool
	for _, loc := range locs {
		if loc.URI == pathToURI(helperPath) && loc.Range == wantDeclRange {
			sawDecl = true
		}
	}
	if !sawDecl {
		t.Fatalf("locations missing helper declaration range %#v: %#v", wantDeclRange, locs)
	}

	edits := structuredRenameEditsFor(analysis.structuredRefs, analysis.structuredSymbols, decl.id, "renamed")
	if len(edits[pathToURI(helperPath)]) != 1 || len(edits[pathToURI(mainPath)]) != 1 {
		t.Fatalf("rename edits = %#v, want one declaration and one reference edit", edits)
	}

	if got := hoverForStructuredSymbol(decl).Contents.Value; !strings.Contains(got, "fn helper") {
		t.Fatalf("decl hover = %q, want function signature", got)
	}
	if got := hoverForStructuredReference(ref).Contents.Value; !strings.Contains(got, "fn helper") {
		t.Fatalf("ref hover = %q, want function signature", got)
	}

	analysis.resolve = nil
	analysis.check = nil
	items := s.completionInScope(doc, "hel")
	if len(items) != 1 || items[0].Label != "helper" {
		t.Fatalf("structured completion items = %#v, want helper", items)
	}
	if !strings.Contains(items[0].Detail, "fn helper") {
		t.Fatalf("structured completion detail = %q, want helper signature", items[0].Detail)
	}

	cursor := bytes.Index(mainSrc, []byte("1)"))
	call := enclosingCall(analysis.file, analysis.lines.lspToOsty(analysis.lines.offsetToLSP(cursor)))
	if call == nil {
		t.Fatal("enclosingCall missed helper call")
	}
	info, ok := buildSignatureInfo(call, doc)
	if !ok {
		t.Fatal("buildSignatureInfo did not use structured reference")
	}
	if got, want := info.Label, "fn helper(arg1: Int) -> Int"; got != want {
		t.Fatalf("signature label = %q, want %q", got, want)
	}
}

func TestUndefinedNameFixUsesStructuredSymbols(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, "helper.osty")
	mainPath := filepath.Join(dir, "main.osty")
	helperSrc := []byte("pub fn helper(x: Int) -> Int { x }\n")
	mainSrc := []byte("fn main() {\n    hleper(1)\n}\n")
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	analysis := s.analyzePackageContaining(mainPath, mainSrc)
	if analysis == nil {
		t.Fatal("analyzePackageContaining returned nil")
	}
	analysis.resolve = nil
	doc := &document{
		uri:      pathToURI(mainPath),
		src:      mainSrc,
		analysis: analysis,
	}
	off := bytes.Index(mainSrc, []byte("hleper"))
	rng := analysis.lines.rangeFromOffsets(off, off+len("hleper"))
	actions := undefinedNameFixes(doc, LSPDiagnostic{
		Code:  diag.CodeUndefinedName,
		Range: rng,
	})
	if len(actions) != 1 {
		t.Fatalf("actions = %#v, want one structured fix", actions)
	}
	if got, want := actions[0].Title, "Rename to `helper`"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	edits := actions[0].Edit.Changes[doc.uri]
	if len(edits) != 1 || edits[0].NewText != "helper" {
		t.Fatalf("edits = %#v, want helper replacement", edits)
	}
}

func TestCompletionAfterDotUsesStructuredImportSurface(t *testing.T) {
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	doc := &document{
		analysis: &docAnalysis{
			structuredImports: []structuredImportSurface{
				{
					alias: "lib",
					symbols: []structuredImportSymbol{
						{name: "other", kind: "function", typeText: "fn(String) -> String"},
						{name: "helper", kind: "function", typeText: "fn(Int) -> Int"},
					},
				},
			},
		},
	}

	items := s.completionAfterDot(doc, "lib", "he")
	if len(items) != 1 || items[0].Label != "helper" {
		t.Fatalf("items = %#v, want helper from structured import surface", items)
	}
	if got, want := items[0].Detail, "fn helper(Int) -> Int"; got != want {
		t.Fatalf("detail = %q, want %q", got, want)
	}

	items = s.completionAfterDot(doc, "lib", "zz")
	if len(items) != 0 {
		t.Fatalf("items = %#v, want structured alias with no prefix matches to stay empty", items)
	}
}

func TestCompletionInScopeIgnoresStructuredLocalSymbols(t *testing.T) {
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	doc := &document{
		analysis: &docAnalysis{
			structuredSymbols: []structuredSymbol{
				{name: "helper", kind: "function", typeText: "fn() -> Int", depth: 0},
				{name: "hiddenLocal", kind: "binding", typeText: "Int", depth: 2},
			},
		},
	}

	items := s.completionInScope(doc, "h")
	if len(items) != 1 || items[0].Label != "helper" {
		t.Fatalf("items = %#v, want only top-level structured symbol", items)
	}
}
