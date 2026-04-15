package docgen

import (
	"sort"
	"strings"
)

// Index maps a documented type name to its anchor slug. Built once
// per Package and consumed by both the markdown and HTML renderers
// to surface cross-references.
//
// Only documented types — KindStruct, KindEnum, KindInterface,
// KindTypeAlias — appear here. Functions and constants don't show up
// in signatures as type references, so linking them would only
// produce noise. The map is keyed by simple name (no path qualifier);
// the renderer is package-scoped so qualifying isn't necessary.
type Index map[string]string

// BuildIndex collects every documented type's anchor across every
// module in pkg. Method-nested decls aren't indexed because they
// can't appear as type references — only top-level types can.
func BuildIndex(pkg *Package) Index {
	idx := Index{}
	for _, m := range pkg.Modules {
		for _, d := range m.Decls {
			switch d.Kind {
			case KindStruct, KindEnum, KindInterface, KindTypeAlias:
				idx[d.Name] = slug(declAnchor(d))
			}
		}
	}
	return idx
}

// extractRefs walks a rendered signature/type string and returns the
// distinct identifiers that match an entry in idx. The walker is a
// minimal hand-rolled scanner: identifiers are runs of [A-Za-z0-9_]
// starting with a letter or `_`. Anything else (punctuation, generic
// brackets, arrows) is a separator. The exclude argument suppresses
// the decl's own name so a struct's signature doesn't link back to
// itself.
//
// Order is deterministic — references appear in the order they first
// occur in the input — so renderers produce stable output across
// runs.
func extractRefs(s string, idx Index, exclude string) []string {
	if len(idx) == 0 || s == "" {
		return nil
	}
	var refs []string
	seen := map[string]bool{}
	i := 0
	for i < len(s) {
		c := s[i]
		if isIdentStart(c) {
			j := i + 1
			for j < len(s) && isIdentCont(s[j]) {
				j++
			}
			word := s[i:j]
			i = j
			if word == exclude || seen[word] {
				continue
			}
			if _, ok := idx[word]; ok {
				seen[word] = true
				refs = append(refs, word)
			}
			continue
		}
		i++
	}
	return refs
}

// referencesIn collects type references for a Decl from its signature
// + (for structs/enums) its field/variant payload types. Includes
// methods so the "References:" footer covers everything the reader
// might want to navigate to from this decl's section.
func referencesIn(d *Decl, idx Index) []string {
	var collected []string
	seen := map[string]bool{}
	add := func(text string) {
		for _, r := range extractRefs(text, idx, d.Name) {
			if seen[r] {
				continue
			}
			seen[r] = true
			collected = append(collected, r)
		}
	}
	add(d.Signature)
	add(d.AliasTarget)
	add(d.ConstType)
	for _, f := range d.Fields {
		add(f.Type)
	}
	for _, v := range d.Variants {
		for _, p := range v.Payload {
			add(p)
		}
	}
	for _, m := range d.Methods {
		add(m.Signature)
	}
	// Stable secondary sort — keep first-occurrence order primarily,
	// but use SliceStable so callers can rely on determinism.
	sort.SliceStable(collected, func(i, j int) bool { return false })
	return collected
}

func isIdentStart(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// linkifyHTML rewrites every word in s that appears in idx (and isn't
// the excluded self-name) into an <a href="#anchor">word</a>. The
// surrounding text is HTML-escaped so the caller can drop the result
// into a `<pre>` block without further escaping.
//
// Used by the HTML renderer to make signature types clickable in-
// place — the markdown renderer can't do this because GFM strips
// links inside fenced code blocks.
func linkifyHTML(s string, idx Index, exclude string) string {
	if len(idx) == 0 || s == "" {
		return htmlEscape(s)
	}
	var out strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if isIdentStart(c) {
			j := i + 1
			for j < len(s) && isIdentCont(s[j]) {
				j++
			}
			word := s[i:j]
			if anchor, ok := idx[word]; ok && word != exclude {
				out.WriteString(`<a href="#`)
				out.WriteString(anchor)
				out.WriteString(`">`)
				out.WriteString(htmlEscape(word))
				out.WriteString(`</a>`)
			} else {
				out.WriteString(htmlEscape(word))
			}
			i = j
			continue
		}
		out.WriteString(htmlEscape(string(c)))
		i++
	}
	return out.String()
}

// htmlEscape is a small wrapper around the stdlib's HTML escape so
// callers don't need to import html in two places. Inlined to avoid
// pulling html into xref.go just for this one call.
func htmlEscape(s string) string {
	// Manual replacement keeps the function dependency-free; the set
	// matches html.EscapeString for the characters that matter inside
	// a <pre> block.
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return r.Replace(s)
}
