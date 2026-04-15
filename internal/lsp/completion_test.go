package lsp

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny wrapper used by the package-aware tests below.
func writeFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// fileURI wraps a local path as the `file://<path>` URI the client
// would send — tests rely on the same lookup the server performs.
func fileURI(path string) string {
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// TestCompletionInScopeListsLocalsAndBuiltins opens a scratch buffer
// that defines `greet` and verifies that requesting completion with
// no prefix returns both that user-defined fn and the `println`
// builtin.
func TestCompletionInScopeListsLocalsAndBuiltins(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn greet(name: String) -> String {
    "hi, {name}"
}

fn main() {

}
`
	sess.openDoc(sampleURI, src)

	// Cursor inside `fn main()` body, on the blank line (line 5, col 4).
	sess.send("c1", "textDocument/completion", CompletionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 5, Character: 4},
	})
	resp := sess.waitResponse("c1")
	if resp.Error != nil {
		t.Fatalf("completion error: %+v", resp.Error)
	}
	var list CompletionList
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	has := map[string]bool{}
	for _, it := range list.Items {
		has[it.Label] = true
	}
	for _, name := range []string{"greet", "println", "Int", "String"} {
		if !has[name] {
			t.Errorf("expected completion to include %q; got %d items", name, len(list.Items))
		}
	}
}

// TestCompletionAfterDotListsPackageMembers verifies that typing
// `auth.` inside `main` surfaces the pub symbols from the sibling
// `auth` package.
func TestCompletionAfterDotListsPackageMembers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth", "login.osty"),
		`pub fn login(user: String, pass: String) -> Bool { true }
fn hashPassword(p: String) -> String { p }
`)
	mainPath := filepath.Join(dir, "main.osty")
	mainSrc := `use auth

fn main() {
    auth.
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	// Put the cursor right after `auth.` on line 3 (0-based).
	// Line: "    auth.|"    ← column 9 (0-based)
	sess.send("c2", "textDocument/completion", CompletionParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Position:     Position{Line: 3, Character: 9},
		Context:      &CompletionContext{TriggerKind: TriggerCharacter, TriggerCharacter: "."},
	})
	resp := sess.waitResponse("c2")
	if resp.Error != nil {
		t.Fatalf("completion error: %+v", resp.Error)
	}
	var list CompletionList
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	has := map[string]bool{}
	for _, it := range list.Items {
		has[it.Label] = true
	}
	if !has["login"] {
		t.Errorf("expected `login` in auth.<completion>; got %v", list.Items)
	}
	if has["hashPassword"] {
		t.Errorf("`hashPassword` is not pub and must not appear cross-package")
	}
}

// TestCompletionFiltersByPrefix verifies the typed prefix narrows the
// list — asking for completion after `prin` returns `println` but not
// the rest of the scope.
func TestCompletionFiltersByPrefix(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	src := `fn main() {
    prin
}
`
	sess.openDoc(sampleURI, src)
	sess.send("c3", "textDocument/completion", CompletionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 1, Character: 8}, // after `prin`
	})
	resp := sess.waitResponse("c3")
	var list CompletionList
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	hasPrintln := false
	for _, it := range list.Items {
		if it.Label == "println" {
			hasPrintln = true
		}
		if !strings.HasPrefix(it.Label, "prin") {
			t.Errorf("prefix filter leaked: %q", it.Label)
		}
	}
	if !hasPrintln {
		t.Errorf("expected println to appear for prefix `prin`")
	}
}

// TestHoverAcrossFilesSamePackage verifies that when two files of one
// package are on disk, opening one and hovering over a name declared
// in the OTHER file returns the correct type.
func TestHoverAcrossFilesSamePackage(t *testing.T) {
	dir := t.TempDir()
	// File A: defines `greet`.
	writeFile(t, filepath.Join(dir, "a.osty"),
		`pub fn greet(name: String) -> String { "hi, {name}" }
`)
	// File B: references greet from the same package.
	bPath := filepath.Join(dir, "b.osty")
	bSrc := `fn shout() -> String {
    greet("world")
}
`
	writeFile(t, bPath, bSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	bURI := fileURI(bPath)
	sess.openDoc(bURI, bSrc)

	// `greet` starts at line 1, character 4 (0-based).
	sess.send("h1", "textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: bURI},
		Position:     Position{Line: 1, Character: 4},
	})
	resp := sess.waitResponse("h1")
	if resp.Error != nil {
		t.Fatalf("hover error: %+v", resp.Error)
	}
	if string(resp.Result) == "null" {
		t.Fatal("hover returned null across files")
	}
	var h Hover
	if err := json.Unmarshal(resp.Result, &h); err != nil {
		t.Fatalf("decode hover: %v", err)
	}
	if !strings.Contains(h.Contents.Value, "greet") {
		t.Errorf("hover did not surface greet: %q", h.Contents.Value)
	}
	if !strings.Contains(h.Contents.Value, "String") {
		t.Errorf("hover missing return type: %q", h.Contents.Value)
	}
}

// TestHoverAcrossPackagesViaUse verifies that hovering over a type
// imported from a sibling package (`auth.User`) returns info about the
// cross-package type.
func TestHoverAcrossPackagesViaUse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth", "user.osty"),
		`pub struct User {
    pub name: String,
}
`)
	mainPath := filepath.Join(dir, "main.osty")
	mainSrc := `use auth

fn whoIs(u: auth.User) -> String {
    u.name
}
`
	writeFile(t, mainPath, mainSrc)

	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	mainURI := fileURI(mainPath)
	sess.openDoc(mainURI, mainSrc)

	// Hover over `User` in `auth.User` on line 2 (0-based).
	// Line content: "fn whoIs(u: auth.User) -> String {"
	// Column of `User`: 17 (0-based).
	sess.send("h2", "textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: mainURI},
		Position:     Position{Line: 2, Character: 18},
	})
	resp := sess.waitResponse("h2")
	if resp.Error != nil {
		t.Fatalf("hover error: %+v", resp.Error)
	}
	if string(resp.Result) == "null" {
		t.Fatal("hover returned null for auth.User")
	}
	var h Hover
	if err := json.Unmarshal(resp.Result, &h); err != nil {
		t.Fatalf("decode hover: %v", err)
	}
	if !strings.Contains(h.Contents.Value, "User") {
		t.Errorf("hover did not surface User: %q", h.Contents.Value)
	}
}
