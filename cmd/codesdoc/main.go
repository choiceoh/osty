// Command codesdoc regenerates the diagnostic artifacts that derive
// from the CodeXxx constants in internal/diag/codes.go:
//
//   - ERROR_CODES.md (user-facing markdown reference)
//   - toolchain/diag_manifest.osty (Osty-side code→family lookup)
//
// codes.go is the single source of truth for diagnostic documentation:
// each constant carries its rule summary, spec reference, example, and
// fix inline, and its source-file phase grouping dictates the family
// classification used by the self-hosted tooling.
//
// Usage:
//
//	codesdoc -in internal/diag/codes.go -out ERROR_CODES.md
//	codesdoc -in internal/diag/codes.go -check ERROR_CODES.md
//	codesdoc -in internal/diag/codes.go -manifest toolchain/diag_manifest.osty
//	codesdoc -in internal/diag/codes.go -manifest-check toolchain/diag_manifest.osty
//
// Flags:
//
//	-in PATH              Path to codes.go (required)
//	-out PATH             Write markdown to this path (use "-" for stdout)
//	-w PATH               Alias for -out; writes to the given file
//	-check PATH           Diff generated markdown against this file; exit 1 on mismatch
//	-manifest PATH        Write Osty manifest to this path
//	-manifest-check PATH  Diff generated manifest against this file; exit 1 on mismatch
//
// Comment format expected on each CodeXxx constant:
//
//	// <summary — one line>.
//	//
//	// <optional longer prose, one or more paragraphs>
//	//
//	// Spec: v0.3 §X.Y
//	// Example:
//	//   <osty code, indented after the "Example:" line>
//	// Fix: <one-sentence remedy>
//	CodeXxx = "Exxxx"
//
// All sections except the summary are optional. Multi-paragraph prose
// is separated by blank `//` lines. The phase-group heading in the
// emitted markdown comes from the leading block-comment above each
// `const (...)` group (e.g. `// Lexical.`).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

func main() {
	var (
		inPath            string
		outPath           string
		checkPath         string
		manifestPath      string
		manifestCheckPath string
		harvestPath       string
		harvestCheckPath  string
	)
	flag.StringVar(&inPath, "in", "", "path to codes.go (required)")
	flag.StringVar(&outPath, "out", "", `write markdown to this path ("-" for stdout)`)
	flag.StringVar(&outPath, "w", "", "alias for -out")
	flag.StringVar(&checkPath, "check", "", "diff generated markdown against this file; exit 1 on mismatch")
	flag.StringVar(&manifestPath, "manifest", "", "write Osty code→family manifest to this path")
	flag.StringVar(&manifestCheckPath, "manifest-check", "", "diff generated manifest against this file; exit 1 on mismatch")
	flag.StringVar(&harvestPath, "harvest-cases", "", "write Osty harvest cases (one per Example block) to this path")
	flag.StringVar(&harvestCheckPath, "harvest-cases-check", "", "diff generated harvest cases against this file; exit 1 on mismatch")
	flag.Parse()

	if inPath == "" {
		fmt.Fprintln(os.Stderr, "codesdoc: -in is required")
		flag.Usage()
		os.Exit(2)
	}

	src, err := os.ReadFile(inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
		os.Exit(1)
	}

	doc, err := parseCodes(inPath, src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
		os.Exit(1)
	}

	manifestMode := manifestPath != "" || manifestCheckPath != ""
	harvestMode := harvestPath != "" || harvestCheckPath != ""
	markdownMode := outPath != "" || checkPath != "" || (!manifestMode && !harvestMode)
	if markdownMode && outPath == "" && checkPath == "" {
		outPath = "-"
	}

	if markdownMode {
		runMarkdown(doc, outPath, checkPath)
	}
	if manifestMode {
		runManifest(doc, manifestPath, manifestCheckPath)
	}
	if harvestMode {
		runHarvest(doc, harvestPath, harvestCheckPath)
	}
}

func runMarkdown(doc *parsedDocs, outPath, checkPath string) {
	generated := renderMarkdown(doc)
	switch {
	case checkPath != "":
		existing, err := os.ReadFile(checkPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
		if !bytes.Equal(normalize(existing), []byte(generated)) {
			fmt.Fprintf(os.Stderr, "codesdoc: %s is out of date\n", checkPath)
			fmt.Fprintln(os.Stderr, "  run `go generate ./internal/diag/...` to regenerate")
			os.Exit(1)
		}
	case outPath == "-":
		if _, err := os.Stdout.WriteString(generated); err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
	default:
		if err := os.WriteFile(outPath, []byte(generated), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
	}
}

func runHarvest(doc *parsedDocs, outPath, checkPath string) {
	generated := renderHarvest(doc)
	switch {
	case checkPath != "":
		existing, err := os.ReadFile(checkPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
		if !bytes.Equal(normalize(existing), []byte(generated)) {
			fmt.Fprintf(os.Stderr, "codesdoc: %s is out of date\n", checkPath)
			fmt.Fprintln(os.Stderr, "  run `go generate ./internal/diag/...` to regenerate")
			os.Exit(1)
		}
	case outPath == "-":
		if _, err := os.Stdout.WriteString(generated); err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
	default:
		if err := os.WriteFile(outPath, []byte(generated), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
	}
}

func runManifest(doc *parsedDocs, outPath, checkPath string) {
	generated := renderManifest(doc)
	switch {
	case checkPath != "":
		existing, err := os.ReadFile(checkPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
		if !bytes.Equal(normalize(existing), []byte(generated)) {
			fmt.Fprintf(os.Stderr, "codesdoc: %s is out of date\n", checkPath)
			fmt.Fprintln(os.Stderr, "  run `go generate ./internal/diag/...` to regenerate")
			os.Exit(1)
		}
	case outPath == "-":
		if _, err := os.Stdout.WriteString(generated); err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
	default:
		if err := os.WriteFile(outPath, []byte(generated), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "codesdoc: %v\n", err)
			os.Exit(1)
		}
	}
}

// normalize strips a UTF-8 BOM (if any) and rewrites CRLF → LF so the
// -check diff is stable across editors and platforms.
func normalize(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return b
}

// ---- parse ----

// codeEntry is one parsed CodeXxx constant.
type codeEntry struct {
	Name  string // e.g. "CodeUnterminatedString"
	Value string // e.g. "E0001"
	Doc   parsedDoc
}

// parsedDoc is the structured content of a code's doc comment.
type parsedDoc struct {
	Summary string   // the first paragraph, collapsed to one line
	Body    []string // subsequent prose paragraphs, preserved as-is
	Spec    string   // e.g. "v0.3 §1.6.1", empty if absent
	Example string   // raw osty code snippet, empty if absent
	Fix     string   // the "Fix:" line's text, empty if absent
}

// phaseGroup bundles consecutive codeEntries that share a phase heading.
type phaseGroup struct {
	Heading string // e.g. "Lexical (E0001–E0099)"
	Entries []codeEntry
}

// parsedDocs is everything the generator needs to render the markdown.
type parsedDocs struct {
	Groups []phaseGroup
}

// parseCodes reads codes.go, walks its top-level const blocks, and
// returns one parsedDocs with phase groups in source order.
//
// Phase headings are single-line comments that appear INSIDE a `const(
// ... )` block but are NOT a ValueSpec's Doc — typically separator
// comments like `// Lexical.` that sit between two groups of specs.
// The heading applies to every subsequent spec until the next such
// comment, or until the end of the const block.
func parseCodes(filename string, src []byte) (*parsedDocs, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}

	// Collect every comment group that's attached as a spec's Doc —
	// those are NOT phase headings even if they look like one.
	attached := map[*ast.CommentGroup]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok && vs.Doc != nil {
				attached[vs.Doc] = true
			}
		}
	}

	// Current phase heading. Starts empty; the first spec encountered
	// before any phase separator gets a synthetic heading derived from
	// its value.
	var groups []phaseGroup
	curHeading := ""
	curEntries := []codeEntry{}

	flushGroup := func() {
		if len(curEntries) == 0 {
			return
		}
		heading := curHeading
		if heading == "" {
			heading = defaultHeadingFor(curEntries[0].Value)
		}
		groups = append(groups, phaseGroup{
			Heading: rangeSuffix(heading, curEntries),
			Entries: append([]codeEntry(nil), curEntries...),
		})
		curEntries = nil
	}

	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// Merge all phase-heading comments that sit inside this block
		// with all the specs, in source order. Then walk.
		type event struct {
			pos     token.Pos
			heading string // non-empty for phase headings
			spec    *ast.ValueSpec
		}
		var events []event
		// Phase-heading candidates: comments inside the block and not
		// a spec's Doc.
		for _, cg := range f.Comments {
			if cg == nil || len(cg.List) == 0 {
				continue
			}
			if cg.Pos() < gd.Pos() || cg.End() > gd.End() {
				continue
			}
			if attached[cg] {
				continue
			}
			h := phaseHeadingFromComment(cg)
			if h == "" {
				continue
			}
			events = append(events, event{pos: cg.Pos(), heading: h})
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			events = append(events, event{pos: vs.Pos(), spec: vs})
		}
		sort.Slice(events, func(i, j int) bool {
			return events[i].pos < events[j].pos
		})

		// The GenDecl's own Doc comment, if present and looks like a
		// heading, seeds the first phase.
		if h := phaseHeadingFromComment(gd.Doc); h != "" && curHeading == "" {
			curHeading = h
		}

		for _, ev := range events {
			if ev.heading != "" {
				flushGroup()
				curHeading = ev.heading
				continue
			}
			vs := ev.spec
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Code") {
					continue
				}
				value, ok := constStringValue(vs.Values, i)
				if !ok {
					continue
				}
				// Trailing-comment fallback: if vs.Doc is absent but
				// vs.Comment (the //-line after the value) exists, use
				// its first line as the summary.
				doc := parseDocComment(vs.Doc)
				if doc.Summary == "" && vs.Comment != nil {
					doc.Summary = firstLine(vs.Comment)
				}
				curEntries = append(curEntries, codeEntry{
					Name:  name.Name,
					Value: value,
					Doc:   doc,
				})
			}
		}
		// End of this GenDecl — flush and reset heading so a subsequent
		// const block starts fresh.
		flushGroup()
		curHeading = ""
	}

	if len(groups) == 0 {
		return nil, errors.New("no CodeXxx constants found")
	}
	return &parsedDocs{Groups: groups}, nil
}

// phaseHeadingFromComment decides whether a comment group is a phase
// separator. A phase separator is a single short line ending with `.`
// OR of the form "Xxxxx (Ennnn-Ennnn)" or "Lxxxx-Lyyyy : description".
// Multi-line comments are treated as prose, not a heading.
func phaseHeadingFromComment(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	// Skip comment groups whose text spans multiple non-blank lines —
	// those are prose blocks, not phase separators.
	nonBlank := 0
	for _, c := range cg.List {
		t := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		if t != "" {
			nonBlank++
		}
	}
	if nonBlank != 1 {
		return ""
	}
	line := firstLine(cg)
	if line == "" {
		return ""
	}
	// Reject lines that look like structured-doc fields so users who
	// typed `// Fix: ...` or `// Spec: ...` in the wrong spot don't
	// accidentally get a section break.
	for _, pfx := range []string{"Spec:", "Fix:", "Example:"} {
		if strings.HasPrefix(line, pfx) {
			return ""
		}
	}
	// Strip a trailing period and/or parenthetical range.
	h := strings.TrimSuffix(line, ".")
	return strings.TrimSpace(h)
}

// firstLine returns the first non-empty line of a comment group,
// stripped of the `// ` prefix.
func firstLine(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	for _, c := range cg.List {
		t := strings.TrimPrefix(c.Text, "//")
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		return t
	}
	return ""
}

// constStringValue pulls the string literal from a ValueSpec's i-th
// Value expression. Returns (value, true) on success, ("", false) for
// non-string values.
func constStringValue(exprs []ast.Expr, i int) (string, bool) {
	if i >= len(exprs) {
		return "", false
	}
	lit, ok := exprs[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	// lit.Value is "\"E0001\"" — trim quotes.
	return strings.Trim(lit.Value, "\""), true
}

// parseDocComment turns a CommentGroup like
//
//	// summary line.
//	//
//	// body paragraph one.
//	//
//	// Spec: v0.3 §X.Y
//	// Example:
//	//   osty snippet line 1
//	//   osty snippet line 2
//	// Fix: remedy
//
// into a structured parsedDoc. Missing sections are zero-valued; missing
// summary yields an empty Summary (the generator will surface a TODO).
func parseDocComment(cg *ast.CommentGroup) parsedDoc {
	if cg == nil {
		return parsedDoc{}
	}
	// Convert lines to plain text (strip `// ` / `//` prefixes).
	lines := make([]string, 0, len(cg.List))
	for _, c := range cg.List {
		text := strings.TrimPrefix(c.Text, "//")
		// Don't TrimSpace yet — we need to preserve indentation inside
		// Example: blocks. But drop one leading space if present,
		// which is the idiomatic Go comment prefix.
		if strings.HasPrefix(text, " ") {
			text = text[1:]
		}
		lines = append(lines, text)
	}

	var doc parsedDoc
	var exampleLines []string
	var bodyParas [][]string
	var curPara []string

	flushPara := func() {
		if len(curPara) == 0 {
			return
		}
		if doc.Summary == "" {
			// First paragraph is the summary — collapse to one line.
			doc.Summary = strings.TrimSuffix(
				strings.Join(strimAll(curPara), " "), ".")
			if doc.Summary != "" {
				doc.Summary += "."
			}
		} else {
			bodyParas = append(bodyParas, append([]string(nil), curPara...))
		}
		curPara = nil
	}

	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Section markers come first.
		switch {
		case strings.HasPrefix(trimmed, "Spec:"):
			flushPara()
			section = ""
			doc.Spec = strings.TrimSpace(strings.TrimPrefix(trimmed, "Spec:"))
			continue
		case strings.HasPrefix(trimmed, "Fix:"):
			flushPara()
			section = ""
			doc.Fix = strings.TrimSpace(strings.TrimPrefix(trimmed, "Fix:"))
			continue
		case trimmed == "Example:":
			flushPara()
			section = "example"
			continue
		}
		if section == "example" {
			// Preserve the original line (with its leading indentation
			// after the `// ` prefix). Blank lines end the block ONLY if
			// followed by a non-indented line; but to keep the format
			// simple we take every following line up to the next
			// section marker as part of the example.
			exampleLines = append(exampleLines, line)
			continue
		}
		if trimmed == "" {
			flushPara()
			continue
		}
		curPara = append(curPara, line)
	}
	flushPara()

	doc.Body = joinParagraphs(bodyParas)
	if len(exampleLines) > 0 {
		doc.Example = trimExample(exampleLines)
	}
	return doc
}

// strimAll is strings.TrimSpace applied to every entry.
func strimAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, s := range xs {
		out[i] = strings.TrimSpace(s)
	}
	return out
}

// joinParagraphs re-flows each paragraph to a single line and returns
// the list.
func joinParagraphs(paras [][]string) []string {
	out := make([]string, 0, len(paras))
	for _, p := range paras {
		out = append(out, strings.Join(strimAll(p), " "))
	}
	return out
}

// trimExample removes leading/trailing empty lines and the shared
// indentation of the example block. Comment lines are `  code` after
// the `// ` prefix strip; we trim the common 2-space indent.
func trimExample(lines []string) string {
	// Trim trailing blanks.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// Trim leading blanks.
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	// Compute smallest leading-space count (ignoring blanks).
	minIndent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		count := len(l) - len(strings.TrimLeft(l, " "))
		if minIndent < 0 || count < minIndent {
			minIndent = count
		}
	}
	if minIndent <= 0 {
		return strings.Join(lines, "\n")
	}
	for i, l := range lines {
		if len(l) >= minIndent {
			lines[i] = l[minIndent:]
		}
	}
	return strings.Join(lines, "\n")
}

// defaultHeadingFor invents a heading when the const block has no
// leading doc comment. Rare — only hit during development of new
// blocks.
func defaultHeadingFor(code string) string {
	switch {
	case strings.HasPrefix(code, "E"):
		return "Errors starting at " + code
	case strings.HasPrefix(code, "W"):
		return "Warnings starting at " + code
	case strings.HasPrefix(code, "L"):
		return "Lint warnings starting at " + code
	}
	return "Miscellaneous"
}

// rangeSuffix appends an Exxxx–Eyyyy range to the heading if the
// entries span more than one code.
func rangeSuffix(heading string, entries []codeEntry) string {
	if len(entries) == 0 {
		return heading
	}
	lo := entries[0].Value
	hi := entries[len(entries)-1].Value
	if strings.Contains(heading, "(") {
		return heading
	}
	if lo == hi {
		return fmt.Sprintf("%s (%s)", heading, lo)
	}
	return fmt.Sprintf("%s (%s–%s)", heading, lo, hi)
}

// ---- render ----

const preamble = `# Osty Diagnostic Reference

Every diagnostic the compiler front-end emits carries a stable code.
This document is the authoritative list; when ` + "`osty check`" + ` produces
an error, searching its code here shows the rule, a minimal
reproduction, and the usual fix.

This file is **generated from ` + "`internal/diag/codes.go`" + `**. Edit the
doc comments there, then run ` + "`go generate ./internal/diag/...`" + `.

Codes are namespaced by phase:

| Range | Phase |
|---|---|
| ` + "`E0001–E0099`" + ` | Lexical |
| ` + "`E0100–E0199`" + ` | Declarations / statements |
| ` + "`E0200–E0299`" + ` | Expressions |
| ` + "`E0300–E0399`" + ` | Types / patterns |
| ` + "`E0400–E0499`" + ` | Annotations |
| ` + "`E0500–E0599`" + ` | Name resolution |
| ` + "`E0600–E0699`" + ` | Control flow / context checks |
| ` + "`E0700–E0799`" + ` | Type checking |
| ` + "`W0750`" + ` | Deprecation warning |
| ` + "`E2000–E2099`" + ` | Manifest / scaffolding |
| ` + "`L0001–L0099`" + ` | Lint warnings (` + "`osty lint`" + `) |

---

`

const trailer = `## How codes are assigned

- A new diagnostic gets the next unused code in its phase's range.
- Existing codes never change meaning; if a rule is reformulated the
  diagnostic keeps its code and the message is updated.
- Codes are exported from ` + "`internal/diag/codes.go`" + ` as ` + "`CodeXxx`" + `
  constants so tests and downstream tooling (LSP, docs generator)
  can reference them by name.
`

// renderMarkdown emits the ERROR_CODES.md body from a parsedDocs.
// Groups are emitted in source order — whatever ordering codes.go
// declares is the ordering readers see. No implicit sort.
func renderMarkdown(doc *parsedDocs) string {
	var b strings.Builder
	b.WriteString(preamble)

	for _, g := range doc.Groups {
		fmt.Fprintf(&b, "## %s\n\n", g.Heading)
		for _, e := range g.Entries {
			renderEntry(&b, e)
		}
		b.WriteString("---\n\n")
	}

	b.WriteString(trailer)
	return b.String()
}

// renderEntry writes one code's markdown entry.
func renderEntry(b *strings.Builder, e codeEntry) {
	fmt.Fprintf(b, "### %s — `%s`\n\n", e.Value, e.Name)
	if e.Doc.Summary == "" {
		fmt.Fprintf(b, "_TODO: add a doc comment for this code in internal/diag/codes.go._\n\n")
		return
	}
	fmt.Fprintf(b, "%s\n", e.Doc.Summary)
	for _, para := range e.Doc.Body {
		fmt.Fprintf(b, "\n%s\n", para)
	}
	if e.Doc.Spec != "" {
		fmt.Fprintf(b, "\nSpec: %s\n", e.Doc.Spec)
	}
	if e.Doc.Example != "" {
		fmt.Fprintf(b, "\n```osty\n%s\n```\n", e.Doc.Example)
	}
	if e.Doc.Fix != "" {
		fmt.Fprintf(b, "\n**Fix**: %s\n", e.Doc.Fix)
	}
	b.WriteString("\n")
}

// ---- manifest render ----

// headingFamily maps the phase heading (as written in codes.go, minus
// the auto-appended range suffix) to the Osty DiagnosticFamily variant
// name. Add entries here when codes.go introduces a new phase section.
//
// Headings that share a family prefix (e.g. "Manifest — TOML syntax."
// vs "Manifest — schema.") are matched by prefix via headingToFamily.
var headingFamily = map[string]string{
	"Lexical":                  "FamilyLexical",
	"Declarations & statements": "FamilyDeclaration",
	"Expressions":              "FamilyExpression",
	"Types & patterns":         "FamilyTypePattern",
	"Annotations":              "FamilyAnnotation",
	"Name resolution":          "FamilyResolution",
	"Control flow / context":   "FamilyControlFlow",
	"Type checking":            "FamilyTypeChecking",
	"Deprecation warning":      "FamilyWarning",
	"Runtime sublanguage":      "FamilyTypeChecking", // TODO: dedicated FamilyRuntime once generated.go accommodates it
	"Scaffolding":              "FamilyScaffold",
}

// headingPrefixFamily matches headings by prefix when multiple
// subsections share a family (e.g. "Manifest — TOML syntax." →
// FamilyManifest). Iterated in declaration order so the first matching
// prefix wins.
var headingPrefixFamily = []struct {
	Prefix string
	Family string
}{
	{"Manifest", "FamilyManifest"},
	{"Lint", "FamilyLint"},
	{"Type checking", "FamilyTypeChecking"},
	{"Name resolution", "FamilyResolution"},
	{"Annotations", "FamilyAnnotation"},
}

// familyForHeading classifies a parsed phase heading. Returns the empty
// string when no mapping matches; callers treat that as a fatal
// generator error so unknown headings can't silently degrade to
// FamilyUnknown at runtime.
func familyForHeading(heading string) string {
	trimmed := stripRangeSuffix(heading)
	if fam, ok := headingFamily[trimmed]; ok {
		return fam
	}
	for _, p := range headingPrefixFamily {
		if strings.HasPrefix(trimmed, p.Prefix) {
			return p.Family
		}
	}
	return ""
}

// stripRangeSuffix removes the " (Exxxx–Eyyyy)" suffix that
// rangeSuffix appends to phase headings for markdown rendering.
func stripRangeSuffix(h string) string {
	if i := strings.LastIndex(h, " ("); i >= 0 {
		return strings.TrimSpace(h[:i])
	}
	return strings.TrimSpace(h)
}

// renderManifest emits the Osty source for toolchain/diag_manifest.osty.
// It generates a single exhaustive code→family function; unlisted codes
// fall through to FamilyUnknown.
func renderManifest(doc *parsedDocs) string {
	var b strings.Builder
	b.WriteString(`// AUTO-GENERATED by ` + "`go generate ./internal/diag/...`" + `. Do not edit.
// Source: internal/diag/codes.go — one entry per stable code.
//
// Consumed by toolchain/diagnostic.osty's diagnosticFamily function
// so the self-hosted tooling gets the same classification the Go side
// uses without maintaining a parallel range table.

/// diagnosticFamilyForCode returns the phase family for a stable code.
/// Generated exhaustively from the phase groupings in codes.go.
/// Unknown codes return FamilyUnknown — extend codes.go rather than
/// editing this file.
pub fn diagnosticFamilyForCode(code: String) -> DiagnosticFamily {
`)

	for _, g := range doc.Groups {
		fam := familyForHeading(g.Heading)
		if fam == "" {
			// Fatal: unknown phase heading means generator mapping is
			// out of date. Fail loud so the missing entry gets added
			// instead of silently classifying as FamilyUnknown.
			fmt.Fprintf(os.Stderr, "codesdoc: unknown phase heading %q — add it to headingFamily in cmd/codesdoc/main.go\n", g.Heading)
			os.Exit(1)
		}
		fmt.Fprintf(&b, "    // %s\n", g.Heading)
		for _, e := range g.Entries {
			fmt.Fprintf(&b, "    if code == \"%s\" { return %s }\n", e.Value, fam)
		}
	}

	b.WriteString("    FamilyUnknown\n}\n")
	return b.String()
}

// ---- harvest render ----

// harvestPhaseForFamily maps the Osty DiagnosticFamily variant name to
// the self-host pipeline stage that can observe the diagnostic. The
// Osty-side harvest runner dispatches to selfResolveSource vs
// frontendCheckSource vs selfLintSource based on this phase string.
//
// Manifest/Scaffold codes aren't reachable from a String input via the
// current self-host entry points, so they're tagged "skip" and the
// harvest runner never executes them.
var harvestPhaseForFamily = map[string]string{
	"FamilyLexical":      "resolve",
	"FamilyDeclaration":  "resolve",
	"FamilyExpression":   "resolve",
	"FamilyTypePattern":  "resolve",
	"FamilyAnnotation":   "resolve",
	"FamilyResolution":   "resolve",
	"FamilyControlFlow":  "check",
	"FamilyTypeChecking": "check",
	"FamilyWarning":      "check",
	"FamilyLint":         "lint",
	"FamilyManifest":     "skip",
	"FamilyScaffold":     "skip",
}

// unsafeForBootstrapGen reports whether an example string would trip
// the Go-hosted bootstrap transpiler's lexer when embedded as an Osty
// string literal. Today the known trigger is the `\{` + `=>` pair; add
// more conditions here (instead of broadening the filter) so each
// carved-out code stays traceable.
func unsafeForBootstrapGen(example string) bool {
	hasBrace := strings.Contains(example, "{") || strings.Contains(example, "}")
	hasFatArrow := strings.Contains(example, "=>")
	return hasBrace && hasFatArrow
}

// proseExample reports whether an example contains prose placeholders
// (triple-dot, unicode right-arrow, ellipsis) that make it unrunnable
// as real Osty source. These examples illustrate the rule in
// ERROR_CODES.md but can't be harvested — skip them rather than emit
// known-failing cases.
func proseExample(example string) bool {
	return strings.Contains(example, "...") ||
		strings.Contains(example, "…") ||
		strings.Contains(example, "→")
}

// ostyEscape escapes a byte slice so it's safe to drop inside an Osty
// regular string literal ("..."). Covers interpolation braces (§1.6.3)
// and the usual control chars.
func ostyEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '{':
			b.WriteString(`\{`)
		case '}':
			b.WriteString(`\}`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderHarvest emits the Osty source for toolchain/diag_examples.osty,
// a generated list of (code, example, phase) records that the harvest
// runner exercises to prove every documented example still fires its
// advertised diagnostic.
func renderHarvest(doc *parsedDocs) string {
	var b strings.Builder
	b.WriteString(`// AUTO-GENERATED by ` + "`go generate ./internal/diag/...`" + `. Do not edit.
// Source: internal/diag/codes.go Example: blocks.
//
// Consumed by toolchain/diag_harvest.osty — one record per code that
// ships a minimal reproduction in its doc comment.

/// DiagHarvestCase pairs a stable diagnostic code with a minimal source
/// snippet from its ` + "`Example:`" + ` doc block, plus the self-host pipeline
/// phase expected to observe it.
pub struct DiagHarvestCase {
    pub code: String,
    pub example: String,
    pub phase: String,
}

/// diagHarvestCases returns the generated list. Order matches codes.go.
pub fn diagHarvestCases() -> List<DiagHarvestCase> {
    [
`)

	first := true
	for _, g := range doc.Groups {
		fam := familyForHeading(g.Heading)
		phase, ok := harvestPhaseForFamily[fam]
		if !ok {
			phase = "skip"
		}
		for _, e := range g.Entries {
			if e.Doc.Example == "" {
				continue
			}
			// Bootstrap-gen's lexer mishandles Osty's `\{` brace-escape
			// inside string literals — it treats it as an interpolation
			// opener and then chokes on the "code" between the braces.
			// Until the Go-hosted transpiler is retired, drop any
			// example that would be embedded into the selfhost bundle
			// with both a `\{` escape and a token (`=>`) that's a
			// static error outside an expression context.
			if unsafeForBootstrapGen(e.Doc.Example) || proseExample(e.Doc.Example) {
				continue
			}
			if !first {
				b.WriteString(",\n")
			}
			first = false
			fmt.Fprintf(&b,
				"        DiagHarvestCase { code: \"%s\", example: \"%s\", phase: \"%s\" }",
				e.Value,
				ostyEscape(e.Doc.Example),
				phase,
			)
		}
	}
	if !first {
		b.WriteString(",\n")
	}
	b.WriteString("    ]\n}\n")
	return b.String()
}
