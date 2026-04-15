package docgen

import (
	"strings"
	"testing"
)

// TestBuildIndexCoversTypesOnly — only type-bearing decls (struct,
// enum, interface, type alias) end up in the index. Linking from a
// signature to a function or constant would surface as noise without
// adding navigational value.
func TestBuildIndexCoversTypesOnly(t *testing.T) {
	pkg := parseSource(t, `
pub struct User { pub name: String }
pub enum Shape { Empty }
pub interface Reader { fn read() -> Int }
pub type Pair = (Int, Int)
pub fn add(x: Int, y: Int) -> Int { x + y }
pub let MAX: Int = 10
`)
	idx := BuildIndex(pkg)
	for _, want := range []string{"User", "Shape", "Reader", "Pair"} {
		if _, ok := idx[want]; !ok {
			t.Errorf("Index missing %q", want)
		}
	}
	for _, dontWant := range []string{"add", "MAX"} {
		if _, ok := idx[dontWant]; ok {
			t.Errorf("Index should not contain function/constant %q", dontWant)
		}
	}
}

// TestExtractRefs — identifiers inside a signature that match the
// index, with the decl's own name excluded.
func TestExtractRefs(t *testing.T) {
	idx := Index{
		"User":  "struct-user",
		"Error": "enum-error",
	}
	got := extractRefs("fn (self) -> Result<User, Error>", idx, "lookupUser")
	if len(got) != 2 || got[0] != "User" || got[1] != "Error" {
		t.Errorf("extractRefs got %v, want [User Error]", got)
	}

	// Self-name excluded.
	got = extractRefs("fn (self) -> User", idx, "User")
	if len(got) != 0 {
		t.Errorf("self-name should be excluded; got %v", got)
	}

	// Empty index → no refs even on a signature with type-looking words.
	if got := extractRefs("fn add(x: Int) -> Int", Index{}, ""); got != nil {
		t.Errorf("empty index should produce no refs, got %v", got)
	}
}

// TestLinkifyHTML — words found in idx become anchored <a> elements;
// surrounding text is HTML-escaped so the result is `<pre>`-safe.
func TestLinkifyHTML(t *testing.T) {
	idx := Index{"User": "struct-user"}
	out := linkifyHTML("fn lookup(name: String) -> User", idx, "lookup")
	if !strings.Contains(out, `<a href="#struct-user">User</a>`) {
		t.Errorf("expected anchor for User; got %s", out)
	}
	if strings.Contains(out, "<a href=\"#struct-user\">String") {
		t.Errorf("String should not link (not in index)")
	}

	// Angle brackets get escaped.
	out = linkifyHTML("List<User>", idx, "")
	if !strings.Contains(out, "&lt;") || !strings.Contains(out, "&gt;") {
		t.Errorf("angle brackets must be escaped: %s", out)
	}
}

// TestMarkdownReferencesFooter — when a fn signature touches a
// documented type, the renderer surfaces a "References:" link line
// below the code block (since GFM strips links inside fenced code).
func TestMarkdownReferencesFooter(t *testing.T) {
	pkg := parseSource(t, `
pub struct User { pub name: String }

/// Look up.
pub fn lookup(name: String) -> User { User { name } }
`)
	md := RenderMarkdown(pkg)
	if !strings.Contains(md, "_References: [`User`](#struct-user)_") {
		t.Errorf("expected References footer with linked User; got:\n%s", md)
	}
}

// TestFieldDocsRendered — when at least one field carries a `///`
// doc the renderer adds a Description column rather than dropping
// the prose.
func TestFieldDocsRendered(t *testing.T) {
	pkg := parseSource(t, `
pub struct User {
    /// Display name shown in UIs.
    pub name: String,
    pub age: Int? = None,
}
`)
	md := RenderMarkdown(pkg)
	if !strings.Contains(md, "| Name | Type | Description |") {
		t.Errorf("expected Description column; got:\n%s", md)
	}
	if !strings.Contains(md, "Display name shown in UIs.") {
		t.Errorf("expected field doc text in markdown; got:\n%s", md)
	}
}
