package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/osty/osty/internal/diag"
)

// TestReferencesFindsAllUsages opens a small file and asks for
// references on a function name, verifying both the declaration and
// the call site are returned.
func TestReferencesFindsAllUsages(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn greet(name: String) -> String {
    "hi, {name}"
}

fn main() {
    greet("world")
    greet("osty")
}
`
	sess.openDoc(sampleURI, src)

	// Cursor on `greet` inside the first call — line 5 (0-based),
	// column 4 where `greet` starts.
	sess.send("r1", "textDocument/references", ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 5, Character: 6},
		Context:      ReferenceContext{IncludeDeclaration: true},
	})
	resp := sess.waitResponse("r1")
	if resp.Error != nil {
		t.Fatalf("references error: %+v", resp.Error)
	}
	var locs []Location
	if err := json.Unmarshal(resp.Result, &locs); err != nil {
		t.Fatal(err)
	}
	// Expect at minimum the two call sites; declaration may or may
	// not be included depending on how the decl site span is shaped.
	if len(locs) < 2 {
		t.Fatalf("expected ≥2 references, got %d: %+v", len(locs), locs)
	}
}

// TestRenameProducesWorkspaceEdit verifies rename returns one
// TextEdit per reference with the new name as replacement text.
func TestRenameProducesWorkspaceEdit(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn greet() {}

fn main() {
    greet()
}
`
	sess.openDoc(sampleURI, src)
	sess.send("rn1", "textDocument/rename", RenameParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 3, Character: 6},
		NewName:      "hello",
	})
	resp := sess.waitResponse("rn1")
	if resp.Error != nil {
		t.Fatalf("rename error: %+v", resp.Error)
	}
	var edit WorkspaceEdit
	if err := json.Unmarshal(resp.Result, &edit); err != nil {
		t.Fatal(err)
	}
	if len(edit.Changes) == 0 {
		t.Fatal("no changes in WorkspaceEdit")
	}
	for _, edits := range edit.Changes {
		for _, te := range edits {
			if te.NewText != "hello" {
				t.Errorf("expected NewText=hello, got %q", te.NewText)
			}
		}
	}
}

// TestWorkspaceSymbolFindsFunction verifies workspace/symbol with a
// partial query surfaces matching declarations.
func TestWorkspaceSymbolFindsFunction(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn findUser(id: Int) -> String { "" }
fn findOrder() -> Int { 0 }
fn main() {}
`
	sess.openDoc(sampleURI, src)
	sess.send("ws1", "workspace/symbol", WorkspaceSymbolParams{Query: "find"})
	resp := sess.waitResponse("ws1")
	if resp.Error != nil {
		t.Fatalf("workspaceSymbol error: %+v", resp.Error)
	}
	var syms []SymbolInformation
	if err := json.Unmarshal(resp.Result, &syms); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range syms {
		names[s.Name] = true
	}
	if !names["findUser"] || !names["findOrder"] {
		t.Errorf("expected findUser and findOrder, got %v", names)
	}
	if names["main"] {
		t.Errorf("main should not match query 'find'")
	}
}

// TestSignatureHelpShowsParams checks that the popup content reports
// the declared parameter list.
func TestSignatureHelpShowsParams(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn add(a: Int, b: Int) -> Int { a + b }
fn main() {
    add(1, )
}
`
	sess.openDoc(sampleURI, src)
	// Cursor after the first comma, expecting activeParameter == 1.
	sess.send("sh1", "textDocument/signatureHelp", SignatureHelpParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 2, Character: 11},
	})
	resp := sess.waitResponse("sh1")
	if resp.Error != nil {
		t.Fatalf("signatureHelp error: %+v", resp.Error)
	}
	if string(resp.Result) == "null" {
		t.Fatal("signatureHelp returned null")
	}
	var help SignatureHelp
	if err := json.Unmarshal(resp.Result, &help); err != nil {
		t.Fatal(err)
	}
	if len(help.Signatures) != 1 {
		t.Fatalf("expected 1 signature, got %d", len(help.Signatures))
	}
	sig := help.Signatures[0]
	if !strings.Contains(sig.Label, "a: Int") || !strings.Contains(sig.Label, "b: Int") {
		t.Errorf("signature label missing params: %q", sig.Label)
	}
	if help.ActiveParameter != 1 {
		t.Errorf("activeParameter = %d, want 1", help.ActiveParameter)
	}
}

// TestInlayHintEmitsTypeForLet verifies an unannotated let binding
// gets a `: T` inlay hint.
func TestInlayHintEmitsTypeForLet(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn main() {
    let x = 42
    let y: String = "hi"
    let z = true
}
`
	sess.openDoc(sampleURI, src)
	sess.send("ih1", "textDocument/inlayHint", InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 10, Character: 0},
		},
	})
	resp := sess.waitResponse("ih1")
	if resp.Error != nil {
		t.Fatalf("inlayHint error: %+v", resp.Error)
	}
	var hints []InlayHint
	if err := json.Unmarshal(resp.Result, &hints); err != nil {
		t.Fatal(err)
	}
	// Expect hints for x and z (not y, which has an annotation).
	var labels []string
	for _, h := range hints {
		labels = append(labels, h.Label)
	}
	hasInt, hasBool, hasString := false, false, false
	for _, l := range labels {
		if strings.Contains(l, "Int") {
			hasInt = true
		}
		if strings.Contains(l, "Bool") {
			hasBool = true
		}
		if strings.Contains(l, "String") {
			hasString = true
		}
	}
	if !hasInt || !hasBool {
		t.Errorf("expected Int and Bool hints, labels: %v", labels)
	}
	if hasString {
		t.Errorf("should NOT hint annotated let; labels: %v", labels)
	}
}

// TestSemanticTokensEmitsData does a coarse check that the encoder
// produced a non-empty token array with the 5-integer-per-token
// structure.
func TestSemanticTokensEmitsData(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn main() {
    let x = 42
}
`
	sess.openDoc(sampleURI, src)
	sess.send("st1", "textDocument/semanticTokens/full", SemanticTokensParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
	})
	resp := sess.waitResponse("st1")
	if resp.Error != nil {
		t.Fatalf("semanticTokens error: %+v", resp.Error)
	}
	var st SemanticTokens
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Data) == 0 {
		t.Fatal("expected non-empty token data")
	}
	if len(st.Data)%5 != 0 {
		t.Errorf("token data must be a multiple of 5; got %d", len(st.Data))
	}
}

// TestCodeActionRenameUndefined verifies that an undefined-name
// diagnostic surfaces a "Rename to …" quick fix.
func TestCodeActionRenameUndefined(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	// `prinln` is a typo for the builtin `println`.
	src := `fn main() {
    prinln("hi")
}
`
	sess.openDoc(sampleURI, src)

	// We need the server's diagnostic for this line so the code
	// action handler has something to map. Ask for diagnostics by
	// re-triggering analysis via a no-op change, then fetch via
	// the session's pending queue.
	diagLSP := LSPDiagnostic{
		Range: Range{
			Start: Position{Line: 1, Character: 4},
			End:   Position{Line: 1, Character: 10},
		},
		Code:    diag.CodeUndefinedName,
		Message: "undefined name `prinln`",
	}
	sess.send("ca1", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Range:        diagLSP.Range,
		Context:      CodeActionContext{Diagnostics: []LSPDiagnostic{diagLSP}},
	})
	resp := sess.waitResponse("ca1")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range actions {
		if strings.Contains(a.Title, "println") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a rename-to-println quick fix, got %+v", actions)
	}
}

// TestLintDiagnosticsPublished verifies that lint warnings (L-codes)
// now arrive in publishDiagnostics so editors paint squigglies and
// the per-diagnostic quick-fix handlers actually have something to
// fire on.
func TestLintDiagnosticsPublished(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn main() {
    let unused = 1
    let x = 2
    println(x)
}
`
	sess.send("", "textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI: sampleURI, LanguageID: "osty", Version: 1, Text: src,
		},
	})
	notifs := sess.drainNotifications("textDocument/publishDiagnostics", 2*time.Second)
	if len(notifs) == 0 {
		t.Fatal("no diagnostics published")
	}
	var pp PublishDiagnosticsParams
	if err := json.Unmarshal(notifs[len(notifs)-1].Params, &pp); err != nil {
		t.Fatal(err)
	}
	foundL0001 := false
	for _, d := range pp.Diagnostics {
		if d.Code == diag.CodeUnusedLet {
			foundL0001 = true
			break
		}
	}
	if !foundL0001 {
		t.Errorf("expected L0001 (unused let) in published diagnostics, got %+v", pp.Diagnostics)
	}
}

// TestOrganizeImportsBailsOnComment verifies we refuse to rewrite
// the use block when a line comment sits between two imports — the
// AST doesn't track that comment, so rewriting would silently drop
// it. The action must simply not be offered in that case.
func TestOrganizeImportsBailsOnComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zeta", "z.osty"),
		`pub fn zz() {}
`)
	writeFile(t, filepath.Join(dir, "alpha", "a.osty"),
		`pub fn aa() {}
`)
	mainPath := filepath.Join(dir, "main.osty")
	// Out-of-order imports with a comment between them. Without
	// the comment we'd offer a sort action; with it we must bail.
	mainSrc := `use zeta
// important ordering note
use alpha

fn main() {
    zeta.zz()
    alpha.aa()
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	sess.send("oi4", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Range:        Range{},
		Context: CodeActionContext{
			Only: []string{CodeActionSourceOrganizeImports},
		},
	})
	resp := sess.waitResponse("oi4")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Errorf("expected no organizeImports when comment between uses; got %+v", actions)
	}
}

// TestOrganizeImportsPreservesTrailingBlank checks that the rewrite
// doesn't swallow blank lines the author inserted after the last
// use — that whitespace belongs to the next declaration and must
// survive.
func TestOrganizeImportsPreservesTrailingBlank(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zeta", "z.osty"),
		`pub fn zz() {}
`)
	writeFile(t, filepath.Join(dir, "alpha", "a.osty"),
		`pub fn aa() {}
`)
	mainPath := filepath.Join(dir, "main.osty")
	// Out-of-order but clean (no comments).
	mainSrc := `use zeta
use alpha

fn main() {
    zeta.zz()
    alpha.aa()
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	sess.send("oi5", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Range:        Range{},
		Context: CodeActionContext{
			Only: []string{CodeActionSourceOrganizeImports},
		},
	})
	resp := sess.waitResponse("oi5")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) == 0 {
		t.Fatal("expected a sort action")
	}
	edits := actions[0].Edit.Changes[mainURI]
	if len(edits) != 1 {
		t.Fatalf("expected one edit, got %d", len(edits))
	}
	// Simulate applying the edit to the original source and confirm
	// the blank line before `fn main` survives.
	li := newLineIndex([]byte(mainSrc))
	startOff := offsetFromLSP(li, edits[0].Range.Start)
	endOff := offsetFromLSP(li, edits[0].Range.End)
	if startOff < 0 || endOff > len(mainSrc) || startOff > endOff {
		t.Fatalf("bad edit range: %+v", edits[0].Range)
	}
	after := mainSrc[:startOff] + edits[0].NewText + mainSrc[endOff:]
	if !strings.Contains(after, "use alpha\nuse zeta\n\nfn main()") {
		t.Errorf("trailing blank line lost. Applied result:\n%s", after)
	}
}

// offsetFromLSP is a small helper that walks a lineIndex to convert
// an LSP Position back into a byte offset — used by the organize
// tests above to simulate applying the returned edits.
func offsetFromLSP(li *lineIndex, p Position) int {
	if int(p.Line) >= len(li.lines) {
		return len(li.src)
	}
	return li.lspToOsty(p).Offset
}

// TestOrganizeImportsDropsUnused opens a file with one used and one
// unused `use` and verifies source.organizeImports returns an edit
// that rewrites the import block without the dead line.
func TestOrganizeImportsDropsUnused(t *testing.T) {
	dir := t.TempDir()
	// Sibling packages so the `use` decls resolve against a real
	// workspace — otherwise the resolver may not flag the unused
	// imports via L0003.
	writeFile(t, filepath.Join(dir, "auth", "login.osty"),
		`pub fn login() {}
`)
	writeFile(t, filepath.Join(dir, "util", "helper.osty"),
		`pub fn noop() {}
`)
	mainPath := filepath.Join(dir, "main.osty")
	mainSrc := `use util
use auth

fn main() {
    auth.login()
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	sess.send("oi1", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 0, Character: 0},
		},
		Context: CodeActionContext{
			Only: []string{CodeActionSourceOrganizeImports},
		},
	})
	resp := sess.waitResponse("oi1")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) == 0 {
		t.Fatal("expected an organizeImports action")
	}
	a := actions[0]
	if a.Kind != CodeActionSourceOrganizeImports {
		t.Errorf("kind = %q, want %q", a.Kind, CodeActionSourceOrganizeImports)
	}
	if a.Edit == nil || len(a.Edit.Changes[mainURI]) == 0 {
		t.Fatalf("no edits in action: %+v", a)
	}
	newText := a.Edit.Changes[mainURI][0].NewText
	if strings.Contains(newText, "use util") {
		t.Errorf("expected `use util` to be removed; got: %q", newText)
	}
	if !strings.Contains(newText, "use auth") {
		t.Errorf("expected `use auth` to survive; got: %q", newText)
	}
}

// TestOrganizeImportsSortsAlphabetically verifies a grab-bag of
// imports comes back in sorted order so the edit is a genuine
// canonicalization and not just a passthrough.
func TestOrganizeImportsSortsAlphabetically(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zeta", "z.osty"),
		`pub fn zz() {}
`)
	writeFile(t, filepath.Join(dir, "alpha", "a.osty"),
		`pub fn aa() {}
`)
	writeFile(t, filepath.Join(dir, "beta", "b.osty"),
		`pub fn bb() {}
`)
	mainPath := filepath.Join(dir, "main.osty")
	mainSrc := `use zeta
use alpha
use beta

fn main() {
    zeta.zz()
    alpha.aa()
    beta.bb()
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	sess.send("oi2", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Range:        Range{},
		Context: CodeActionContext{
			Only: []string{CodeActionSourceOrganizeImports},
		},
	})
	resp := sess.waitResponse("oi2")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) == 0 {
		t.Fatal("expected a sort-imports action")
	}
	newText := actions[0].Edit.Changes[mainURI][0].NewText
	// Alpha < beta < zeta — indices must be ascending in the rewrite.
	ai := strings.Index(newText, "use alpha")
	bi := strings.Index(newText, "use beta")
	zi := strings.Index(newText, "use zeta")
	if ai < 0 || bi < 0 || zi < 0 {
		t.Fatalf("missing use line in %q", newText)
	}
	if !(ai < bi && bi < zi) {
		t.Errorf("uses not sorted alphabetically: alpha@%d beta@%d zeta@%d\n%s",
			ai, bi, zi, newText)
	}
}

// TestOrganizeImportsNoOpOnCleanFile confirms that a file whose
// imports are already sorted and used yields no organizeImports
// action — the lightbulb shouldn't light up on clean code.
func TestOrganizeImportsNoOpOnCleanFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "a.osty"),
		`pub fn aa() {}
`)
	writeFile(t, filepath.Join(dir, "beta", "b.osty"),
		`pub fn bb() {}
`)
	mainPath := filepath.Join(dir, "main.osty")
	mainSrc := `use alpha
use beta

fn main() {
    alpha.aa()
    beta.bb()
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	sess.send("oi3", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Range:        Range{},
		Context: CodeActionContext{
			Only: []string{CodeActionSourceOrganizeImports},
		},
	})
	resp := sess.waitResponse("oi3")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Errorf("expected no actions for clean imports, got %+v", actions)
	}
}

// TestFixAllBundlesSuggestions verifies source.fixAll.osty rolls
// multiple machine-applicable lint fixes into one edit.
func TestFixAllBundlesSuggestions(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	// Two unused bindings — each carries a MachineApplicable
	// "prefix with _" suggestion.
	src := `fn main() {
    let alpha = 1
    let beta = 2
}
`
	sess.openDoc(sampleURI, src)

	sess.send("fa1", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Range:        Range{},
		Context: CodeActionContext{
			Only: []string{CodeActionSourceFixAllOsty},
		},
	})
	resp := sess.waitResponse("fa1")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) == 0 {
		t.Fatal("expected a fixAll action")
	}
	a := actions[0]
	if a.Kind != CodeActionSourceFixAllOsty {
		t.Errorf("kind = %q, want %q", a.Kind, CodeActionSourceFixAllOsty)
	}
	if a.Edit == nil {
		t.Fatal("fixAll action missing edit")
	}
	edits := a.Edit.Changes[sampleURI]
	// Expect at least two fixes — one per unused binding. The exact
	// number may be higher if the checker also contributes
	// suggestions for the same diagnostics.
	if len(edits) < 2 {
		t.Errorf("expected ≥2 bundled edits, got %d: %+v", len(edits), edits)
	}
}

// TestCodeActionCapabilityAdvertisesKinds confirms the Initialize
// response lists the source-action kinds clients need to see before
// they'll run them on save.
func TestCodeActionCapabilityAdvertisesKinds(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()

	sess.send("cap", "initialize", InitializeParams{})
	resp := sess.waitResponse("cap")
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	var res InitializeResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.Capabilities.CodeActionProvider == nil {
		t.Fatal("no codeActionProvider advertised")
	}
	kinds := res.Capabilities.CodeActionProvider.CodeActionKinds
	wants := []string{
		CodeActionQuickFix,
		CodeActionSourceOrganizeImports,
		CodeActionSourceFixAllOsty,
	}
	for _, w := range wants {
		found := false
		for _, k := range kinds {
			if k == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("capability missing kind %q; got %v", w, kinds)
		}
	}
}

// TestReferencesAcrossPackages confirms references walk every loaded
// package rather than stopping at the current file.
func TestReferencesAcrossPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth", "login.osty"),
		`pub fn login() {}
`)
	mainPath := filepath.Join(dir, "main.osty")
	mainSrc := `use auth

fn main() {
    auth.login()
    auth.login()
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	// Click on `auth` on line 3 (the use reference).
	sess.send("xr1", "textDocument/references", ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Position:     Position{Line: 3, Character: 5},
		Context:      ReferenceContext{IncludeDeclaration: false},
	})
	resp := sess.waitResponse("xr1")
	if resp.Error != nil {
		t.Fatalf("references error: %+v", resp.Error)
	}
	var locs []Location
	if err := json.Unmarshal(resp.Result, &locs); err != nil {
		t.Fatal(err)
	}
	// Expect both call sites of `auth.login()`.
	if len(locs) < 2 {
		t.Errorf("expected ≥2 references across packages, got %d: %+v", len(locs), locs)
	}
}
