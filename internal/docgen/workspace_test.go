package docgen

import (
	"strings"
	"testing"
)

// TestFromWorkspaceMapDeterministic — packages are emitted in
// lexicographic key order so the index page output is stable across
// builds (matters for `--check`-style CI guards).
func TestFromWorkspaceMapDeterministic(t *testing.T) {
	a := parseSource(t, "pub fn aFn() -> Int { 0 }")
	a.Name = "alpha"
	b := parseSource(t, "pub fn bFn() -> Int { 0 }")
	b.Name = "bravo"
	ws := FromWorkspaceMap("/root", map[string]*Package{
		"bravo": b,
		"alpha": a,
	})
	if len(ws.Packages) != 2 {
		t.Fatalf("expected 2 pkgs, got %d", len(ws.Packages))
	}
	if ws.Packages[0].Name != "alpha" || ws.Packages[1].Name != "bravo" {
		t.Errorf("packages out of order: %s, %s",
			ws.Packages[0].Name, ws.Packages[1].Name)
	}
}

// TestRenderWorkspaceMarkdown — the index lists each package as a
// link with the right extension and uses the package's first decl
// summary as the row's description.
func TestRenderWorkspaceMarkdown(t *testing.T) {
	a := parseSource(t, "/// Alpha pkg.\npub fn aFn() -> Int { 0 }")
	a.Name = "alpha"
	ws := FromWorkspaceMap("/root", map[string]*Package{"alpha": a})
	md := RenderWorkspaceMarkdown(ws, ".md")
	mustContain := []string{
		"# Workspace API documentation",
		"_Root: `/root`_",
		"[`alpha`](alpha.md)",
		"Alpha pkg.",
	}
	for _, want := range mustContain {
		if !strings.Contains(md, want) {
			t.Errorf("workspace markdown missing %q\n---\n%s", want, md)
		}
	}
}

// TestRenderWorkspaceHTML — same shape, HTML-escaped, with link
// targets matching the per-package filename convention.
func TestRenderWorkspaceHTML(t *testing.T) {
	a := parseSource(t, "/// Alpha pkg.\npub fn aFn() -> Int { 0 }")
	a.Name = "alpha"
	ws := FromWorkspaceMap("/root", map[string]*Package{"alpha": a})
	h := RenderWorkspaceHTML(ws, ".html")
	for _, want := range []string{
		"<title>Workspace API docs — Osty</title>",
		`<a href="alpha.html"><code>alpha</code></a>`,
		"Alpha pkg.",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("workspace HTML missing %q\n---\n%s", want, h)
		}
	}
}

// TestPreferredPackageName — picks the first non-empty input,
// falling back to dir basename. Keeps loader-induced empty Names
// from blanking the index page.
func TestPreferredPackageName(t *testing.T) {
	cases := []struct {
		name, importPath, dir, want string
	}{
		{"explicit", "imp", "/x/y", "explicit"},
		{"", "imp", "/x/y", "imp"},
		{"", "", "/x/y", "y"},
		{"", "", "", "package"},
	}
	for _, c := range cases {
		got := PreferredPackageName(c.name, c.importPath, c.dir)
		if got != c.want {
			t.Errorf("(%q,%q,%q) = %q, want %q",
				c.name, c.importPath, c.dir, got, c.want)
		}
	}
}
