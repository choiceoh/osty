package docgen

import (
	"strings"
)

// DocInfo is the structured form of a `///` doc block. The parser
// recognises a handful of labeled sections — `Params:`, `Returns:`,
// `Example:`, `See:` — so renderers can lay them out with their own
// formatting instead of reprinting the raw block verbatim.
//
// Every field is optional. An empty DocInfo signals "no doc comment
// was attached"; callers should prefer Decl.Doc for the raw source
// (always populated when the parser saw any `///` line) and fall back
// to DocInfo.Summary when they want the cleaned first sentence.
type DocInfo struct {
	// Summary is the first paragraph of the doc comment, re-flowed to
	// a single line. Ends with a period when the source did.
	Summary string
	// Body is every subsequent prose paragraph before the first
	// labeled section. Each element is one paragraph, already
	// re-flowed; renderers separate them with blank lines.
	Body []string
	// Params lists the parameter entries parsed out of a `Params:`
	// block. Order matches source order.
	Params []ParamDoc
	// Returns is the text of a `Returns:` section, re-flowed to a
	// single string. Empty when absent.
	Returns string
	// Examples is every `Example:` block in source order. Leading/
	// trailing blank lines are trimmed and the shared indent stripped.
	Examples []string
	// See is the list of cross-references parsed from a `See:` or
	// `See also:` block — typically bare identifiers like `subtract`
	// or dotted paths like `io.print`.
	See []string
}

// ParamDoc is one entry inside a `Params:` block: `name: description`.
// Descriptions that span multiple indented lines are joined with
// spaces; the renderer is free to break them again on output.
type ParamDoc struct {
	Name string
	Desc string
}

// IsEmpty reports whether the DocInfo contains nothing worth
// rendering. Convenient guard for callers that want to suppress
// entire sections when a decl has no doc at all.
func (d DocInfo) IsEmpty() bool {
	return d.Summary == "" &&
		len(d.Body) == 0 &&
		len(d.Params) == 0 &&
		d.Returns == "" &&
		len(d.Examples) == 0 &&
		len(d.See) == 0
}

// parseDocComment turns the newline-joined text the lexer attached to
// a declaration (Decl.Doc) into a DocInfo. The grammar is intentionally
// lenient:
//
//  - The first non-empty paragraph becomes the Summary.
//  - Subsequent paragraphs become Body until the first labeled section.
//  - A line that begins with `<Label>:` at column 0 (after any leading
//    spaces) opens a section. Currently recognised: `Params`, `Param`,
//    `Arguments`, `Args`, `Returns`, `Return`, `Example`, `Examples`,
//    `See`, `See also`. Unknown labels are treated as prose.
//  - `Example:` sections consume every following line until the next
//    section label or a double-blank break. Shared indentation is
//    stripped so the embedded code starts at column 0.
//  - `Params:` entries may be on the same line (`Params: x: foo`) or
//    in an indented list below the label. Each entry is `name: desc`;
//    continuation lines indented further are appended to the previous
//    entry's description.
func parseDocComment(raw string) DocInfo {
	var info DocInfo
	if raw == "" {
		return info
	}
	lines := strings.Split(raw, "\n")

	// First pass: split into segments. Each segment is either a prose
	// paragraph or a labeled section (including the section's
	// consumed body lines).
	type segment struct {
		label string   // "" for prose paragraphs
		lines []string // raw lines after the `Label:` prefix (for labeled),
		// or the full paragraph (for prose)
		inline string // for labeled sections: the text after "Label:"
	}
	var segs []segment
	var curProse []string

	flushProse := func() {
		if len(curProse) > 0 {
			segs = append(segs, segment{lines: curProse})
			curProse = nil
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if label, rest, ok := matchSectionLabel(trimmed); ok {
			flushProse()
			seg := segment{label: label, inline: rest}
			// Consume following indented / non-empty lines as the
			// section body until the next section or two blank lines.
			i++
			blankRun := 0
			for i < len(lines) {
				next := lines[i]
				nextTrim := strings.TrimSpace(next)
				if nextTrim == "" {
					blankRun++
					if blankRun >= 2 {
						i++
						break
					}
					seg.lines = append(seg.lines, next)
					i++
					continue
				}
				if _, _, ok := matchSectionLabel(nextTrim); ok {
					break
				}
				blankRun = 0
				seg.lines = append(seg.lines, next)
				i++
			}
			segs = append(segs, seg)
			continue
		}

		if trimmed == "" {
			// Paragraph break.
			flushProse()
			i++
			continue
		}

		curProse = append(curProse, trimmed)
		i++
	}
	flushProse()

	// Second pass: convert segments into DocInfo fields.
	for _, s := range segs {
		if s.label == "" {
			// Prose paragraph. First one becomes Summary; rest append
			// to Body.
			joined := strings.Join(s.lines, " ")
			if info.Summary == "" {
				info.Summary = joined
			} else {
				info.Body = append(info.Body, joined)
			}
			continue
		}
		switch s.label {
		case "params", "param", "arguments", "args":
			info.Params = append(info.Params, parseParams(s.inline, s.lines)...)
		case "returns", "return":
			info.Returns = joinInlineAndLines(s.inline, s.lines)
		case "example", "examples":
			ex := trimExampleLines(s.lines)
			// The `Example:` label on its own line means inline is
			// empty; a `Example: let x = 1` form carries the snippet
			// on the label line itself.
			if s.inline != "" {
				if ex == "" {
					ex = s.inline
				} else {
					ex = s.inline + "\n" + ex
				}
			}
			if ex != "" {
				info.Examples = append(info.Examples, ex)
			}
		case "see", "see also":
			info.See = append(info.See, parseSeeList(s.inline, s.lines)...)
		}
	}

	return info
}

// matchSectionLabel reports whether s is `Label:` optionally followed
// by inline text. Returns (lowercased-label, rest, true) on match.
// Called on already-trimmed lines, so leading whitespace is absent.
//
// Multi-word labels like "See also" are handled by checking each
// candidate prefix before falling back to the single-word case.
func matchSectionLabel(s string) (label, rest string, ok bool) {
	// Try multi-word labels first.
	for _, multi := range []string{"See also"} {
		if prefix(s, multi+":") {
			return strings.ToLower(multi), strings.TrimSpace(s[len(multi)+1:]), true
		}
	}
	// Single-word label: everything up to the first ':'.
	colon := strings.IndexByte(s, ':')
	if colon <= 0 || colon > 20 {
		// No colon, or label suspiciously long — not a section.
		return "", "", false
	}
	head := s[:colon]
	// The label must be a bare word (letters only, no spaces) to
	// distinguish from prose like "Note: the behaviour...".
	for _, r := range head {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "", "", false
		}
	}
	lower := strings.ToLower(head)
	switch lower {
	case "params", "param", "arguments", "args",
		"returns", "return",
		"example", "examples",
		"see":
		return lower, strings.TrimSpace(s[colon+1:]), true
	}
	return "", "", false
}

// prefix is strings.HasPrefix case-insensitive. Keeps the label match
// permissive for users who capitalise inconsistently.
func prefix(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	return strings.EqualFold(s[:len(p)], p)
}

// parseParams turns the body of a `Params:` block into ParamDoc
// entries. inline is the text after the label on the same line; the
// lines slice holds any following lines. Both are searched for
// `name: description` rows.
func parseParams(inline string, lines []string) []ParamDoc {
	var out []ParamDoc
	add := func(name, desc string) {
		name = strings.TrimSpace(name)
		desc = strings.TrimSpace(desc)
		if name == "" {
			return
		}
		out = append(out, ParamDoc{Name: name, Desc: desc})
	}

	// If `Params: name: desc` was written on one line, the "inline"
	// portion carries that single entry.
	if inline != "" {
		if name, desc, ok := splitParamLine(inline); ok {
			add(name, desc)
		}
	}

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		// Strip a leading bullet marker so `- x: foo` and `x: foo`
		// both work.
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimPrefix(trimmed, "* ")
		if name, desc, ok := splitParamLine(trimmed); ok {
			add(name, desc)
			continue
		}
		// Continuation line — append to the previous entry's desc.
		if len(out) > 0 {
			if out[len(out)-1].Desc == "" {
				out[len(out)-1].Desc = trimmed
			} else {
				out[len(out)-1].Desc += " " + trimmed
			}
		}
	}
	return out
}

// splitParamLine tries to parse `name: description`. A valid name is
// a bare identifier (letters / digits / underscore). Returns ok=false
// when the line doesn't look like a param entry so the caller can
// treat it as a continuation of the previous one.
func splitParamLine(s string) (name, desc string, ok bool) {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", "", false
	}
	head := s[:colon]
	for _, r := range head {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "", "", false
		}
	}
	return head, strings.TrimSpace(s[colon+1:]), true
}

// joinInlineAndLines re-flows a labeled section's body into one line.
// Used for `Returns:` which is conventionally short but sometimes
// spans continuation lines.
func joinInlineAndLines(inline string, lines []string) string {
	parts := make([]string, 0, len(lines)+1)
	if inline != "" {
		parts = append(parts, inline)
	}
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// parseSeeList expands a `See:` section into its cross-references.
// A single inline value (`See: subtract`) becomes a one-element list;
// a multi-line block (each ref on its own line, optionally bulleted)
// contributes each non-empty line.
func parseSeeList(inline string, lines []string) []string {
	var out []string
	if inline != "" {
		// Allow comma-separated `See: a, b, c` shorthand.
		for _, p := range strings.Split(inline, ",") {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		t = strings.TrimPrefix(t, "- ")
		t = strings.TrimPrefix(t, "* ")
		for _, p := range strings.Split(t, ",") {
			if tt := strings.TrimSpace(p); tt != "" {
				out = append(out, tt)
			}
		}
	}
	return out
}

// trimExampleLines drops leading + trailing blank lines and strips
// the shared indentation so the rendered code block starts at
// column 0. Mirrors codesdoc.trimExample for consistency.
func trimExampleLines(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	minIndent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		count := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || count < minIndent {
			minIndent = count
		}
	}
	if minIndent <= 0 {
		return strings.Join(lines, "\n")
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if len(l) >= minIndent {
			out[i] = l[minIndent:]
		} else {
			out[i] = l
		}
	}
	return strings.Join(out, "\n")
}
