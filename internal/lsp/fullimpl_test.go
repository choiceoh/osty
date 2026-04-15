package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenameTargetsNameNotKeyword verifies the edits produced by
// rename span exactly the identifier, not the preceding keyword.
// Before the name-span work, renaming `greet` at the declaration
// would return an edit whose Range covered the whole `fn greet` or
// `pub fn greet` tokens, rewriting the keyword as well.
func TestRenameTargetsNameNotKeyword(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `pub fn greet() {}

fn main() {
    greet()
}
`
	sess.openDoc(sampleURI, src)
	sess.send("rn", "textDocument/rename", RenameParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 3, Character: 6}, // inside the call `greet`
		NewName:      "hello",
	})
	resp := sess.waitResponse("rn")
	if resp.Error != nil {
		t.Fatalf("rename error: %+v", resp.Error)
	}
	var edit WorkspaceEdit
	if err := json.Unmarshal(resp.Result, &edit); err != nil {
		t.Fatal(err)
	}
	edits := edit.Changes[sampleURI]
	if len(edits) == 0 {
		t.Fatal("no edits")
	}
	// Every edit's range length must equal len("greet") = 5 columns,
	// no more (which would mean we swept keywords).
	for _, e := range edits {
		if e.Range.Start.Line != e.Range.End.Line {
			t.Errorf("edit spans multiple lines: %+v", e.Range)
			continue
		}
		width := e.Range.End.Character - e.Range.Start.Character
		if width != 5 {
			t.Errorf("edit width = %d, want 5 (for `greet`): %+v", width, e.Range)
		}
		if e.NewText != "hello" {
			t.Errorf("NewText = %q, want 'hello'", e.NewText)
		}
	}
}

// TestDefinitionJumpsToNameNotKeyword verifies that go-to-definition
// lands on the identifier, not on the `pub fn` prefix. Before the
// narrowing, the range's Start matched the first char of the decl
// (i.e. 'p' in `pub`).
func TestDefinitionJumpsToNameNotKeyword(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `pub fn greet() {}

fn main() {
    greet()
}
`
	sess.openDoc(sampleURI, src)
	sess.send("def", "textDocument/definition", DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 3, Character: 6},
	})
	resp := sess.waitResponse("def")
	if resp.Error != nil {
		t.Fatalf("definition error: %+v", resp.Error)
	}
	var loc Location
	if err := json.Unmarshal(resp.Result, &loc); err != nil {
		t.Fatal(err)
	}
	// `pub fn greet()` — chars 0..2 = "pub", 3 = " ", 4..5 = "fn",
	// 6 = " ", 7..11 = "greet". Name starts at column 7.
	if loc.Range.Start.Line != 0 {
		t.Errorf("expected line 0, got %d", loc.Range.Start.Line)
	}
	if loc.Range.Start.Character != 7 {
		t.Errorf("expected column 7 (name start), got %d", loc.Range.Start.Character)
	}
	if loc.Range.End.Character != 12 {
		t.Errorf("expected column 12 (name end), got %d", loc.Range.End.Character)
	}
}

// TestWorkspaceSymbolIndexesUnopenedFiles verifies the workspace
// indexer picks up files the user never opened. We create two files,
// open only one, then query for a symbol defined in the other.
func TestWorkspaceSymbolIndexesUnopenedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "opened.osty"),
		`fn openedDecl() {}
`)
	writeFile(t, filepath.Join(dir, "hidden.osty"),
		`fn hiddenDecl() {}
`)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	openedURI := fileURI(filepath.Join(dir, "opened.osty"))
	sess.openDoc(openedURI, "fn openedDecl() {}\n")

	sess.send("ws", "workspace/symbol", WorkspaceSymbolParams{Query: "hidden"})
	resp := sess.waitResponse("ws")
	if resp.Error != nil {
		t.Fatalf("workspaceSymbol error: %+v", resp.Error)
	}
	var syms []SymbolInformation
	if err := json.Unmarshal(resp.Result, &syms); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sym := range syms {
		if sym.Name == "hiddenDecl" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected hiddenDecl in workspace symbols; got %+v", syms)
	}
}

// TestReferencesNoDuplicates confirms the dedup pass. We construct a
// tiny cross-file setup where the same declaration is reachable from
// multiple package views and check that each location appears once.
func TestReferencesNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.osty"),
		`pub fn shared() {}
`)
	bPath := filepath.Join(dir, "b.osty")
	bSrc := `fn call() {
    shared()
}
`
	writeFile(t, bPath, bSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	bURI := fileURI(bPath)
	sess.openDoc(bURI, bSrc)

	sess.send("r", "textDocument/references", ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: bURI},
		Position:     Position{Line: 1, Character: 5},
		Context:      ReferenceContext{IncludeDeclaration: true},
	})
	resp := sess.waitResponse("r")
	if resp.Error != nil {
		t.Fatalf("references error: %+v", resp.Error)
	}
	var locs []Location
	if err := json.Unmarshal(resp.Result, &locs); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, loc := range locs {
		key := loc.URI + "@" + string(rune(loc.Range.Start.Line)) + "," + string(rune(loc.Range.Start.Character))
		if seen[key] {
			t.Errorf("duplicate location: %+v", loc)
		}
		seen[key] = true
	}
}

// TestCodeActionUsesResolverScope validates that the quick-fix list
// contains the scope-native match (`println`) when the typo is a
// builtin near-miss. Before the NearbyNames switch this path went
// through a local Levenshtein; now it consults the resolver.
func TestCodeActionUsesResolverScope(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn main() {
    prinln("hi")
}
`
	sess.openDoc(sampleURI, src)
	diagLSP := LSPDiagnostic{
		Range: Range{
			Start: Position{Line: 1, Character: 4},
			End:   Position{Line: 1, Character: 10},
		},
		Code:    "E0500",
		Message: "undefined name",
	}
	sess.send("ca", "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Range:        diagLSP.Range,
		Context:      CodeActionContext{Diagnostics: []LSPDiagnostic{diagLSP}},
	})
	resp := sess.waitResponse("ca")
	if resp.Error != nil {
		t.Fatalf("codeAction error: %+v", resp.Error)
	}
	var actions []CodeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatal(err)
	}
	// First candidate should be the closest (edit-distance 1 from
	// `prinln` to `println`).
	if len(actions) == 0 {
		t.Fatal("expected at least one code action")
	}
	first := actions[0].Title
	if !strings.Contains(first, "println") {
		t.Errorf("expected `println` to be the top candidate, got %q", first)
	}
}
