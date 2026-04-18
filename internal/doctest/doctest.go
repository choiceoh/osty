// Package doctest extracts runnable Osty code blocks from `///` doc
// comments on declarations. It implements v0.5 (G32) §11 doctest
// discovery — the feature that lets library docs double as executable
// examples.
//
// Extraction is source-format driven: the extractor reads the
// DocComment string attached to each declaration, finds fenced code
// blocks that declare `osty` as the language, and returns one Doctest
// per block. It does not compile or run anything.
//
// Example doc source:
//
//	/// Returns the first element.
//	///
//	/// ```osty
//	/// let xs = [1, 2, 3]
//	/// testing.assertEq(xs.first(), Some(1))
//	/// ```
//	pub fn first<T>(xs: List<T>) -> T?
//
// A single Decl may host zero, one, or multiple blocks. Fence
// recognition:
//
//   - opens with three backticks followed immediately by the language
//     tag `osty` (case-insensitive), trimmed of surrounding whitespace
//   - a bare ``` opens a non-osty block and is ignored; non-osty
//     blocks must still be balanced so the extractor can skip them
//   - closes with three backticks on a line by itself (post-trim)
//
// v0.5 scope limits:
//
//   - Only `osty` language tag is treated as runnable; variants like
//     `osty ignore` or `osty no_run` are deferred to a later pass.
//   - Indented / tilde fence forms are unsupported; only ``` works.
//   - Nested fences are not supported (Osty itself has no backtick
//     string syntax, so the ambiguity does not arise in practice).

package doctest

import (
	"strings"

	"github.com/osty/osty/internal/ast"
)

// Doctest is one extracted runnable example. Owner names the
// declaration that hosted the block; OrdinalInOwner disambiguates
// when a single Decl carries multiple blocks (1-based).
type Doctest struct {
	// Owner is the name of the hosting declaration. Fn/struct/enum/
	// interface/type/let all work; an empty owner indicates the
	// block came from a file-level doc (not currently extracted).
	Owner string
	// OrdinalInOwner counts from 1 within the same Owner so the
	// generated test name stays stable across re-extractions.
	OrdinalInOwner int
	// Source is the raw Osty code between the fences, newline-
	// delimited. Leading `/// ` prefix and any common indentation
	// are stripped per the comment-stripping rules.
	Source string
}

// Extract walks every annotatable declaration in file and returns
// the Osty-language doctests it carries. Returns an empty slice for
// a nil file or a file with no doc-hosting decls.
//
// Order is source order: within a file, owners come in declaration
// order; within an owner, blocks come in the order they appear.
func Extract(file *ast.File) []Doctest {
	if file == nil {
		return nil
	}
	var out []Doctest
	for _, decl := range file.Decls {
		name, doc := ownerOf(decl)
		if doc == "" {
			continue
		}
		cleaned := stripCommentPrefix(doc)
		blocks := extractFencedOsty(cleaned)
		for i, src := range blocks {
			out = append(out, Doctest{
				Owner:          name,
				OrdinalInOwner: i + 1,
				Source:         src,
			})
		}
	}
	return out
}

// ownerOf returns (name, docComment) for declarations that can host
// doc comments. Returns ("", "") for kinds that don't.
func ownerOf(d ast.Decl) (string, string) {
	switch x := d.(type) {
	case *ast.FnDecl:
		return x.Name, x.DocComment
	case *ast.StructDecl:
		return x.Name, x.DocComment
	case *ast.EnumDecl:
		return x.Name, x.DocComment
	case *ast.InterfaceDecl:
		return x.Name, x.DocComment
	case *ast.TypeAliasDecl:
		return x.Name, x.DocComment
	case *ast.LetDecl:
		return x.Name, x.DocComment
	}
	return "", ""
}

// stripCommentPrefix removes the leading `///` (and optional single
// space) from every line of a raw doc comment. Lines that consist
// only of `///` become empty. Indentation inside the Osty code block
// is preserved — only the comment marker is removed.
func stripCommentPrefix(doc string) string {
	lines := strings.Split(doc, "\n")
	out := make([]string, len(lines))
	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		if !strings.HasPrefix(trimmed, "///") {
			// Already de-prefixed line (rare for parser-captured
			// doc, but handle it idempotently).
			out[i] = ln
			continue
		}
		// Drop the `///` and exactly one following space if present.
		body := trimmed[3:]
		if strings.HasPrefix(body, " ") {
			body = body[1:]
		}
		out[i] = body
	}
	return strings.Join(out, "\n")
}

// extractFencedOsty scans text for ``` fences and returns the body
// of every block whose opening fence tags the language as `osty`.
// Other fenced blocks are consumed but discarded.
func extractFencedOsty(text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		tag, ok := openingFence(lines[i])
		if !ok {
			i++
			continue
		}
		// Find closing fence.
		start := i + 1
		end := start
		for end < len(lines) && !isClosingFence(lines[end]) {
			end++
		}
		if strings.EqualFold(tag, "osty") {
			out = append(out, strings.Join(lines[start:end], "\n"))
		}
		// Skip past the closing fence (or EOF).
		i = end + 1
	}
	return out
}

// openingFence returns (tag, true) when line opens a fenced code
// block; tag is the portion after the backticks, trimmed.
func openingFence(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "```") {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[3:])
	return rest, true
}

// isClosingFence reports whether line is a bare ``` (possibly with
// surrounding whitespace). The language tag is not allowed on a
// closing fence in CommonMark, and we match that convention.
func isClosingFence(line string) bool {
	return strings.TrimSpace(line) == "```"
}
