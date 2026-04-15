package docgen

import (
	"strings"
	"testing"
)

// TestRenderHTMLBasics asserts the structural landmarks a consumer
// (static-site embedder, hand-rolled scraper) can rely on: doctype,
// package heading with code wrapper, TOC nav, per-decl article with
// stable anchor id, and the signature rendered inside <pre><code>.
func TestRenderHTMLBasics(t *testing.T) {
	pkg := parseSource(t, `
/// Greeter.
pub struct Hello {
    pub name: String,

    /// Say hi.
    pub fn greet(self) -> String { "hi" }
}
`)
	h := RenderHTML(pkg)
	for _, want := range []string{
		"<!doctype html>",
		"<title>test — Osty API docs</title>",
		"<h1>Package <code>test</code></h1>",
		`<nav class="toc">`,
		`id="struct-hello"`,
		`<pre class="sig"><code>pub struct Hello</code></pre>`,
		"<h4>Fields</h4>",
		"<h4>Methods</h4>",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

// TestRenderHTMLEscape confirms that user-supplied text is escaped so
// a malicious or merely unusual decl name / doc body can't inject
// markup into the generated page.
func TestRenderHTMLEscape(t *testing.T) {
	pkg := parseSource(t, `
/// Doc with <script>alert(1)</script> pseudo-html.
pub fn weird() -> Int { 0 }
`)
	h := RenderHTML(pkg)
	if strings.Contains(h, "<script>") {
		t.Errorf("<script> leaked into HTML output — should be escaped")
	}
	if !strings.Contains(h, "&lt;script&gt;") {
		t.Errorf("expected escaped <script> entity in HTML output")
	}
}

// TestRenderHTMLDeprecation confirms the styled <aside> callout with
// since/use metadata lands in the right slot.
func TestRenderHTMLDeprecation(t *testing.T) {
	pkg := parseSource(t, `
#[deprecated(since = "0.5", use = "newFn", message = "use newFn instead")]
pub fn oldFn() -> Int { 0 }
`)
	h := RenderHTML(pkg)
	mustContain := []string{
		`<aside class="deprecated">`,
		"since 0.5",
		"use newFn instead",
		"Use <code>newFn</code> instead.",
	}
	for _, want := range mustContain {
		if !strings.Contains(h, want) {
			t.Errorf("HTML deprecation missing %q\n---\n%s", want, h)
		}
	}
}
