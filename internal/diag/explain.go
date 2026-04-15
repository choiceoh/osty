package diag

// This file gives downstream tooling (the `osty explain` subcommand,
// the LSP, future IDE plugins) programmatic access to the doc comments
// on the CodeXxx constants in codes.go. The markdown reference in
// ERROR_CODES.md is generated from the same source by cmd/codesdoc;
// this package parses the comments at runtime so a single binary can
// explain any code without shipping ERROR_CODES.md alongside it.
//
// The parser here is intentionally a trimmed port of the logic in
// cmd/codesdoc/main.go — just enough to produce a structured CodeDoc
// per constant. If the comment format ever drifts, update both sides.

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

//go:embed codes.go
var codesSource []byte

// CodeDoc is the parsed documentation for a single diagnostic code.
// Fields mirror the structured sections in the doc comments on each
// CodeXxx constant (see codes.go for the authoritative copies).
type CodeDoc struct {
	Code    string   // e.g. "E0001"
	Name    string   // e.g. "CodeUnterminatedString"
	Summary string   // first paragraph, one line
	Body    []string // subsequent prose paragraphs
	Spec    string   // "Spec:" line text, empty if absent
	Example string   // code snippet from the "Example:" block
	Fix     string   // "Fix:" line text, empty if absent
}

var (
	codeDocs    []CodeDoc
	codeDocByID map[string]CodeDoc
)

func init() {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "codes.go", codesSource, parser.ParseComments)
	if err != nil {
		// The file is embedded from our own source — a parse failure
		// here is a programmer error. Leave the tables empty rather
		// than panicking so downstream tooling degrades gracefully.
		return
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Code") {
					continue
				}
				value, ok := constStringValue(vs.Values, i)
				if !ok {
					continue
				}
				d := parseExplainDoc(vs.Doc)
				if d.Summary == "" && vs.Comment != nil {
					d.Summary = firstCommentLine(vs.Comment)
				}
				codeDocs = append(codeDocs, CodeDoc{
					Code:    value,
					Name:    name.Name,
					Summary: d.Summary,
					Body:    d.Body,
					Spec:    d.Spec,
					Example: d.Example,
					Fix:     d.Fix,
				})
			}
		}
	}
	codeDocByID = make(map[string]CodeDoc, len(codeDocs))
	for _, d := range codeDocs {
		codeDocByID[d.Code] = d
	}
}

// Explain returns the parsed doc for a diagnostic code (e.g. "E0500",
// "W0750", "E2014"). Lxxxx lint codes are documented by package lint —
// call lint.LookupRule for those.
func Explain(code string) (CodeDoc, bool) {
	d, ok := codeDocByID[code]
	return d, ok
}

// AllCodes returns every parsed code in ascending code order.
func AllCodes() []CodeDoc {
	out := make([]CodeDoc, len(codeDocs))
	copy(out, codeDocs)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// ---- parsing helpers (trimmed port of cmd/codesdoc/main.go) ----

type parsedExplainDoc struct {
	Summary string
	Body    []string
	Spec    string
	Example string
	Fix     string
}

func parseExplainDoc(cg *ast.CommentGroup) parsedExplainDoc {
	if cg == nil {
		return parsedExplainDoc{}
	}
	lines := make([]string, 0, len(cg.List))
	for _, c := range cg.List {
		text := strings.TrimPrefix(c.Text, "//")
		if strings.HasPrefix(text, " ") {
			text = text[1:]
		}
		lines = append(lines, text)
	}

	var doc parsedExplainDoc
	var exampleLines []string
	var bodyParas [][]string
	var curPara []string

	flushPara := func() {
		if len(curPara) == 0 {
			return
		}
		if doc.Summary == "" {
			joined := strings.Join(trimAllLines(curPara), " ")
			joined = strings.TrimSuffix(joined, ".")
			if joined != "" {
				doc.Summary = joined + "."
			}
		} else {
			bodyParas = append(bodyParas, append([]string(nil), curPara...))
		}
		curPara = nil
	}

	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
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

	for _, p := range bodyParas {
		doc.Body = append(doc.Body, strings.Join(trimAllLines(p), " "))
	}
	if len(exampleLines) > 0 {
		doc.Example = trimExampleBlock(exampleLines)
	}
	return doc
}

func trimAllLines(xs []string) []string {
	out := make([]string, len(xs))
	for i, s := range xs {
		out[i] = strings.TrimSpace(s)
	}
	return out
}

func trimExampleBlock(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
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

func firstCommentLine(cg *ast.CommentGroup) string {
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

func constStringValue(exprs []ast.Expr, i int) (string, bool) {
	if i >= len(exprs) {
		return "", false
	}
	lit, ok := exprs[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, "\""), true
}
