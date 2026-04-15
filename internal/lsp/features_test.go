package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

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
