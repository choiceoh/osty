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
func RenderMarkdown(pkg *Package) string {
	var b strings.Builder
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
			renderDecl(&b, d, 3)
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
// their parent type cleanly.
func renderDecl(b *strings.Builder, d *Decl, headingLevel int) {
	fmt.Fprintf(b, "%s %s `%s`\n\n",
		strings.Repeat("#", headingLevel), titleCase(d.Kind.String()), d.Name)

	if d.Deprecated != "" {
		fmt.Fprintf(b, "> **Deprecated** — %s\n\n", d.Deprecated)
	}

	fmt.Fprintf(b, "```osty\n%s\n```\n\n", d.Signature)

	if d.Doc != "" {
		b.WriteString(d.Doc)
		// Doc comments are rejoined with newlines by the lexer; ensure
		// the block ends with exactly one blank line.
		if !strings.HasSuffix(d.Doc, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if len(d.Fields) > 0 {
		fmt.Fprintf(b, "%s Fields\n\n", strings.Repeat("#", headingLevel+1))
		b.WriteString("| Name | Type |\n|---|---|\n")
		for _, f := range d.Fields {
			fmt.Fprintf(b, "| `%s` | `%s` |\n", f.Name, f.Type)
		}
		b.WriteByte('\n')
	}

	if len(d.Variants) > 0 {
		fmt.Fprintf(b, "%s Variants\n\n", strings.Repeat("#", headingLevel+1))
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
		fmt.Fprintf(b, "%s Methods\n\n", strings.Repeat("#", headingLevel+1))
		for _, m := range d.Methods {
			renderDecl(b, m, headingLevel+2)
		}
	}
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
