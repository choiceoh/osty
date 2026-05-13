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

	"github.com/osty/osty/internal/runner"
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

// ---- parsing helpers (host glue around runner.ParseExplainDoc) ----

// parsedExplainDoc aliases runner.ParsedExplainDoc so the local
// init wiring keeps its existing field names. The parsing policy
// itself lives in toolchain/diag_explain.osty.
type parsedExplainDoc = runner.ParsedExplainDoc

// parseExplainDoc strips the `//` prefix and an optional leading
// space from each comment in `cg`, then delegates to the
// toolchain policy for the actual sectioning.
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
	return runner.ParseExplainDoc(lines)
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
