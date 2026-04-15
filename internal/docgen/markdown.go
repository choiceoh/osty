package docgen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RenderMarkdown produces one self-contained markdown document for the
// given extracted Package. Layout:
//
//	# <Package.Name>
//	<optional Dir subtitle>
//
//	## Module <file basename>
//
//	### <pub decl signature>
//	<doc prose>
//	<fields / variants / methods>
//
// Anchors for cross-linking follow the common GitHub-style lowercased
// slug so renderers that build a TOC can reference decls directly.
//
// Type references inside a decl's signature are surfaced as a
// "References:" footer because GitHub-flavoured markdown strips
// hyperlinks inside fenced code blocks. The HTML renderer wraps
// references inline instead.
func RenderMarkdown(pkg *Package) string {
	var b strings.Builder
	idx := BuildIndex(pkg)
	fmt.Fprintf(&b, "# Package `%s`\n\n", pkg.Name)
	if pkg.Dir != "" {
		fmt.Fprintf(&b, "_Source directory: `%s`_\n\n", pkg.Dir)
	}

	// Per-file TOC (only modules with at least one pub decl).
	if toc := renderTOC(pkg); toc != "" {
		b.WriteString("## Contents\n\n")
		b.WriteString(toc)
		b.WriteString("\n")
	}

	for _, m := range pkg.Modules {
		if len(m.Decls) == 0 {
			continue
		}
		label := filepath.Base(m.Path)
		if label == "" || label == "." {
			label = m.Path
		}
		fmt.Fprintf(&b, "## `%s`\n\n", label)
		for _, d := range m.Decls {
			renderDecl(&b, d, 3, idx)
		}
	}
	return b.String()
}

// renderTOC builds a bullet list of every documented decl across every
// module, grouped by module. Returns "" if nothing to list — callers
// skip the heading in that case.
func renderTOC(pkg *Package) string {
	var b strings.Builder
	any := false
	for _, m := range pkg.Modules {
		if len(m.Decls) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&b, "- **%s**\n", filepath.Base(m.Path))
		for _, d := range m.Decls {
			fmt.Fprintf(&b, "  - [%s `%s`](#%s)\n",
				d.Kind, d.Name, slug(declAnchor(d)))
		}
	}
	if !any {
		return ""
	}
	return b.String()
}

// renderDecl writes one declaration's section. headingLevel controls
// the depth of the emitted `#` so the renderer can nest methods under
// their parent type cleanly. idx is the package's cross-reference
// index used to surface a "References:" footer when the signature
// touches another documented type.
func renderDecl(b *strings.Builder, d *Decl, headingLevel int, idx Index) {
	fmt.Fprintf(b, "%s %s `%s`\n\n",
		strings.Repeat("#", headingLevel), titleCase(d.Kind.String()), d.Name)

	// Deprecation callout — promoted above the signature so readers
	// see the warning before investing in the API.
	if d.Deprecated != "" {
		renderDeprecation(b, d)
	}

	fmt.Fprintf(b, "```osty\n%s\n```\n\n", d.Signature)
	if d.Line > 0 {
		fmt.Fprintf(b, "_Defined at line %d._\n\n", d.Line)
	}

	// Cross-references — a comma-separated list of links to every
	// other documented type the decl touches. Skipped when there are
	// no references so a simple `fn add(Int, Int) -> Int` doesn't
	// produce noise.
	if refs := referencesIn(d, idx); len(refs) > 0 {
		fmt.Fprintf(b, "_References: %s_\n\n", joinRefLinks(refs, idx))
	}

	// Structured doc: summary + body prose.
	if d.Info.Summary != "" {
		fmt.Fprintf(b, "%s\n\n", d.Info.Summary)
	}
	for _, para := range d.Info.Body {
		fmt.Fprintf(b, "%s\n\n", para)
	}
	// If there was a doc block but no summary parsed (rare — only when
	// the entire doc fit in a labeled section), fall back to the raw
	// block so no content is dropped.
	if d.Info.IsEmpty() && d.Doc != "" {
		b.WriteString(d.Doc)
		if !strings.HasSuffix(d.Doc, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	subHead := strings.Repeat("#", headingLevel+1)

	if len(d.Info.Params) > 0 {
		fmt.Fprintf(b, "%s Parameters\n\n", subHead)
		b.WriteString("| Name | Description |\n|---|---|\n")
		for _, p := range d.Info.Params {
			fmt.Fprintf(b, "| `%s` | %s |\n", p.Name, escapePipes(p.Desc))
		}
		b.WriteByte('\n')
	}
	if d.Info.Returns != "" {
		fmt.Fprintf(b, "%s Returns\n\n%s\n\n", subHead, d.Info.Returns)
	}
	for _, ex := range d.Info.Examples {
		fmt.Fprintf(b, "%s Example\n\n```osty\n%s\n```\n\n", subHead, ex)
	}
	if len(d.Info.See) > 0 {
		fmt.Fprintf(b, "%s See also\n\n", subHead)
		for _, s := range d.Info.See {
			fmt.Fprintf(b, "- `%s`\n", s)
		}
		b.WriteByte('\n')
	}

	if len(d.Fields) > 0 {
		fmt.Fprintf(b, "%s Fields\n\n", subHead)
		// Add a Description column only when at least one field has a
		// doc comment — keeps the simple two-column table for the
		// majority of types whose fields are self-explanatory.
		anyDoc := false
		for _, f := range d.Fields {
			if f.Doc != "" {
				anyDoc = true
				break
			}
		}
		if anyDoc {
			b.WriteString("| Name | Type | Description |\n|---|---|---|\n")
			for _, f := range d.Fields {
				fmt.Fprintf(b, "| `%s` | `%s` | %s |\n",
					f.Name, f.Type, escapePipes(firstLineOf(f.Doc)))
			}
		} else {
			b.WriteString("| Name | Type |\n|---|---|\n")
			for _, f := range d.Fields {
				fmt.Fprintf(b, "| `%s` | `%s` |\n", f.Name, f.Type)
			}
		}
		b.WriteByte('\n')
	}

	if len(d.Variants) > 0 {
		fmt.Fprintf(b, "%s Variants\n\n", subHead)
		for _, v := range d.Variants {
			if len(v.Payload) == 0 {
				fmt.Fprintf(b, "- `%s`", v.Name)
			} else {
				fmt.Fprintf(b, "- `%s(%s)`", v.Name, strings.Join(v.Payload, ", "))
			}
			if v.Doc != "" {
				fmt.Fprintf(b, " — %s", firstLineOf(v.Doc))
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if len(d.Methods) > 0 {
		fmt.Fprintf(b, "%s Methods\n\n", subHead)
		for _, m := range d.Methods {
			renderDecl(b, m, headingLevel+2, idx)
		}
	}
}

// joinRefLinks formats a list of type names as a comma-separated
// markdown link list pointing to each one's anchor.
func joinRefLinks(refs []string, idx Index) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		anchor, ok := idx[r]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("[`%s`](#%s)", r, anchor))
	}
	return strings.Join(parts, ", ")
}

// renderDeprecation formats the `#[deprecated]` callout into a blockquote
// that composes the message, since-version, and replacement hint into
// one paragraph. Separated out to keep renderDecl linear.
func renderDeprecation(b *strings.Builder, d *Decl) {
	parts := []string{"**Deprecated**"}
	if d.DeprecatedSince != "" {
		parts = append(parts, "since "+d.DeprecatedSince)
	}
	line := strings.Join(parts, " ")
	if d.Deprecated != "" && d.Deprecated != "deprecated" {
		line += " — " + d.Deprecated
	}
	fmt.Fprintf(b, "> %s\n", line)
	if d.DeprecatedUse != "" {
		fmt.Fprintf(b, "> Use `%s` instead.\n", d.DeprecatedUse)
	}
	b.WriteByte('\n')
}

// escapePipes replaces `|` with `\|` so a description that contains a
// literal pipe doesn't break the enclosing markdown table. Cheap and
// sufficient — users rarely put pipes in param docs.
func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// declAnchor builds the text the slug function will turn into a link
// target. Matches the heading renderDecl emits.
func declAnchor(d *Decl) string {
	return titleCase(d.Kind.String()) + " " + d.Name
}

// firstLineOf returns everything before the first newline. Used for
// terse variant summaries.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// titleCase is a tiny helper that uppercases the first rune — avoids
// pulling in strings.Title (deprecated) or x/text.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// slug maps a heading to its GitHub-style anchor: lowercase, spaces
// to hyphens, strip backticks and most punctuation. Good-enough for
// intra-doc links.
func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteByte('-')
		}
	}
	return b.String()
}
