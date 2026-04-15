package docgen

import (
	"fmt"
	"html"
	"path/filepath"
	"strings"
)

// RenderHTML produces a self-contained HTML document for pkg. The
// output is a single file with inline CSS — no external assets, no
// build step — so callers can drop it straight into a static-site tree.
//
// Layout mirrors the markdown renderer: package heading, per-module
// TOC, then one `<section>` per decl with sub-sections for parameters,
// returns, examples, fields, variants, and methods. Every decl gets
// a stable id derived from its kind+name for anchored links.
func RenderHTML(pkg *Package) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s — Osty API docs</title>\n", html.EscapeString(pkg.Name))
	b.WriteString(htmlStylesheet)
	b.WriteString("</head>\n<body>\n<main>\n")

	fmt.Fprintf(&b, "<h1>Package <code>%s</code></h1>\n", html.EscapeString(pkg.Name))
	if pkg.Dir != "" {
		fmt.Fprintf(&b, "<p class=\"src-path\">Source directory: <code>%s</code></p>\n",
			html.EscapeString(pkg.Dir))
	}

	if toc := renderHTMLTOC(pkg); toc != "" {
		b.WriteString("<nav class=\"toc\"><h2>Contents</h2>\n")
		b.WriteString(toc)
		b.WriteString("</nav>\n")
	}

	for _, m := range pkg.Modules {
		if len(m.Decls) == 0 {
			continue
		}
		label := filepath.Base(m.Path)
		if label == "" || label == "." {
			label = m.Path
		}
		fmt.Fprintf(&b, "<section class=\"module\">\n<h2><code>%s</code></h2>\n",
			html.EscapeString(label))
		for _, d := range m.Decls {
			renderHTMLDecl(&b, d, 3)
		}
		b.WriteString("</section>\n")
	}

	b.WriteString("</main>\n</body>\n</html>\n")
	return b.String()
}

// renderHTMLTOC walks every module and emits a bullet list linking
// each documented decl to its anchored section. Empty modules are
// skipped so the TOC reflects what the body actually contains.
func renderHTMLTOC(pkg *Package) string {
	var b strings.Builder
	any := false
	for _, m := range pkg.Modules {
		if len(m.Decls) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&b, "<h3><code>%s</code></h3>\n<ul>\n",
			html.EscapeString(filepath.Base(m.Path)))
		for _, d := range m.Decls {
			fmt.Fprintf(&b, "  <li><a href=\"#%s\">%s <code>%s</code></a></li>\n",
				slug(declAnchor(d)),
				html.EscapeString(titleCase(d.Kind.String())),
				html.EscapeString(d.Name))
		}
		b.WriteString("</ul>\n")
	}
	if !any {
		return ""
	}
	return b.String()
}

// renderHTMLDecl writes one declaration's article. Nested methods
// recurse with an incremented heading level, capped at <h6> per HTML's
// own maximum — deeper nesting falls back to a styled class-only div
// so structure is preserved even when the heading depth bottoms out.
func renderHTMLDecl(b *strings.Builder, d *Decl, level int) {
	if level > 6 {
		level = 6
	}
	anchor := slug(declAnchor(d))
	kind := titleCase(d.Kind.String())

	fmt.Fprintf(b, "<article class=\"decl kind-%s\" id=\"%s\">\n",
		d.Kind.String(), anchor)
	fmt.Fprintf(b, "<h%d>%s <code>%s</code></h%d>\n",
		level, html.EscapeString(kind), html.EscapeString(d.Name), level)

	if d.Deprecated != "" {
		renderHTMLDeprecation(b, d)
	}

	fmt.Fprintf(b, "<pre class=\"sig\"><code>%s</code></pre>\n",
		html.EscapeString(d.Signature))
	if d.Line > 0 {
		fmt.Fprintf(b, "<p class=\"source-line\">Defined at line %d.</p>\n", d.Line)
	}

	if d.Info.Summary != "" {
		fmt.Fprintf(b, "<p class=\"summary\">%s</p>\n",
			html.EscapeString(d.Info.Summary))
	}
	for _, para := range d.Info.Body {
		fmt.Fprintf(b, "<p>%s</p>\n", html.EscapeString(para))
	}
	if d.Info.IsEmpty() && d.Doc != "" {
		// Preserve the raw block as <pre> rather than trying to render
		// it — this is the fallback for doc text we failed to parse.
		fmt.Fprintf(b, "<pre class=\"raw-doc\">%s</pre>\n",
			html.EscapeString(d.Doc))
	}

	if len(d.Info.Params) > 0 {
		b.WriteString("<h4>Parameters</h4>\n<table class=\"params\">\n")
		b.WriteString("<thead><tr><th>Name</th><th>Description</th></tr></thead>\n<tbody>\n")
		for _, p := range d.Info.Params {
			fmt.Fprintf(b, "<tr><td><code>%s</code></td><td>%s</td></tr>\n",
				html.EscapeString(p.Name), html.EscapeString(p.Desc))
		}
		b.WriteString("</tbody></table>\n")
	}
	if d.Info.Returns != "" {
		fmt.Fprintf(b, "<h4>Returns</h4>\n<p>%s</p>\n",
			html.EscapeString(d.Info.Returns))
	}
	for _, ex := range d.Info.Examples {
		fmt.Fprintf(b, "<h4>Example</h4>\n<pre class=\"example\"><code>%s</code></pre>\n",
			html.EscapeString(ex))
	}
	if len(d.Info.See) > 0 {
		b.WriteString("<h4>See also</h4>\n<ul class=\"see-also\">\n")
		for _, s := range d.Info.See {
			fmt.Fprintf(b, "<li><code>%s</code></li>\n", html.EscapeString(s))
		}
		b.WriteString("</ul>\n")
	}

	if len(d.Fields) > 0 {
		b.WriteString("<h4>Fields</h4>\n<table class=\"fields\">\n")
		b.WriteString("<thead><tr><th>Name</th><th>Type</th></tr></thead>\n<tbody>\n")
		for _, f := range d.Fields {
			fmt.Fprintf(b, "<tr><td><code>%s</code></td><td><code>%s</code></td></tr>\n",
				html.EscapeString(f.Name), html.EscapeString(f.Type))
		}
		b.WriteString("</tbody></table>\n")
	}

	if len(d.Variants) > 0 {
		b.WriteString("<h4>Variants</h4>\n<ul class=\"variants\">\n")
		for _, v := range d.Variants {
			b.WriteString("<li><code>")
			b.WriteString(html.EscapeString(v.Name))
			if len(v.Payload) > 0 {
				b.WriteString("(")
				escaped := make([]string, len(v.Payload))
				for i, p := range v.Payload {
					escaped[i] = html.EscapeString(p)
				}
				b.WriteString(strings.Join(escaped, ", "))
				b.WriteString(")")
			}
			b.WriteString("</code>")
			if v.Doc != "" {
				fmt.Fprintf(b, " — %s", html.EscapeString(firstLineOf(v.Doc)))
			}
			b.WriteString("</li>\n")
		}
		b.WriteString("</ul>\n")
	}

	if len(d.Methods) > 0 {
		b.WriteString("<h4>Methods</h4>\n<div class=\"methods\">\n")
		for _, m := range d.Methods {
			renderHTMLDecl(b, m, level+1)
		}
		b.WriteString("</div>\n")
	}

	b.WriteString("</article>\n")
}

// renderHTMLDeprecation emits the deprecation callout as a <aside>
// styled to stand out from the decl body. Mirrors the markdown
// renderer's blockquote layout.
func renderHTMLDeprecation(b *strings.Builder, d *Decl) {
	fmt.Fprintf(b, "<aside class=\"deprecated\">\n<strong>Deprecated</strong>")
	if d.DeprecatedSince != "" {
		fmt.Fprintf(b, " since %s", html.EscapeString(d.DeprecatedSince))
	}
	if d.Deprecated != "" && d.Deprecated != "deprecated" {
		fmt.Fprintf(b, " — %s", html.EscapeString(d.Deprecated))
	}
	if d.DeprecatedUse != "" {
		fmt.Fprintf(b, "<br>Use <code>%s</code> instead.",
			html.EscapeString(d.DeprecatedUse))
	}
	b.WriteString("\n</aside>\n")
}

// htmlStylesheet is a minimal, self-contained style block so the
// generated page is readable without any external CSS. The palette is
// GitHub-ish neutral; users who want a custom theme can post-process
// the HTML or strip the <style> block.
const htmlStylesheet = `<style>
  :root {
    --fg: #24292f;
    --muted: #6e7781;
    --border: #d0d7de;
    --accent: #0969da;
    --bg-code: #f6f8fa;
    --bg-depr: #fff8c5;
    --bg-depr-border: #d4a72c;
  }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
    color: var(--fg);
    max-width: 920px;
    margin: 2rem auto;
    padding: 0 1.25rem;
    line-height: 1.55;
  }
  h1, h2, h3, h4, h5, h6 { margin-top: 1.5em; }
  h1 { border-bottom: 1px solid var(--border); padding-bottom: .3em; }
  code { background: var(--bg-code); padding: .15em .35em; border-radius: 4px; font-size: .92em; }
  pre { background: var(--bg-code); padding: .9em; border-radius: 6px; overflow-x: auto; }
  pre code { background: none; padding: 0; }
  table { border-collapse: collapse; margin: .5rem 0 1rem; width: 100%; }
  th, td { border: 1px solid var(--border); padding: .4rem .6rem; text-align: left; vertical-align: top; }
  th { background: var(--bg-code); }
  nav.toc { background: var(--bg-code); padding: .9rem 1.1rem; border-radius: 6px; }
  nav.toc h2 { margin-top: 0; font-size: 1.05rem; }
  nav.toc ul { margin: .2rem 0 .8rem 1rem; padding: 0; }
  nav.toc a { color: var(--accent); text-decoration: none; }
  nav.toc a:hover { text-decoration: underline; }
  aside.deprecated {
    background: var(--bg-depr);
    border-left: 3px solid var(--bg-depr-border);
    padding: .6rem .8rem;
    border-radius: 4px;
    margin: .6rem 0;
  }
  .src-path, .source-line { color: var(--muted); font-size: .9em; }
  article.decl { margin-top: 1.6rem; }
  .methods { margin-left: 1rem; border-left: 2px solid var(--border); padding-left: 1rem; }
  .summary { font-size: 1.05em; }
</style>
`
