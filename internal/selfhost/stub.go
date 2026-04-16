package selfhost

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

type CheckSummary struct {
	Assignments int
	Accepted    int
	Errors      int
}

type CheckedNode struct {
	Node     int
	Kind     string
	TypeName string
	Start    int
	End      int
}

type CheckedBinding struct {
	Node     int
	Name     string
	TypeName string
	Mutable  bool
	Start    int
	End      int
}

type CheckedSymbol struct {
	Node     int
	Kind     string
	Name     string
	Owner    string
	TypeName string
	Start    int
	End      int
}

type CheckInstantiation struct {
	Node       int
	Callee     string
	TypeArgs   []string
	ResultType string
	Start      int
	End        int
}

type CheckResult struct {
	Summary        CheckSummary
	TypedNodes     []CheckedNode
	Bindings       []CheckedBinding
	Symbols        []CheckedSymbol
	Instantiations []CheckInstantiation
}

func Lex(src []byte) ([]token.Token, []*diag.Diagnostic, []token.Comment) {
	pos := unsupportedPos()
	return []token.Token{{Kind: token.EOF, Pos: pos, End: pos}}, []*diag.Diagnostic{unsupportedDiag("lexing")}, nil
}

func Parse(src []byte) (*ast.File, []*diag.Diagnostic) {
	pos := unsupportedPos()
	return &ast.File{PosV: pos, EndV: pos}, []*diag.Diagnostic{unsupportedDiag("parsing")}
}

func CheckSource(src []byte) CheckSummary {
	return CheckSourceStructured(src).Summary
}

func CheckSourceStructured(src []byte) CheckResult {
	return CheckResult{
		Summary: CheckSummary{Errors: 1},
	}
}

func LintDiagnostics(src []byte) []*diag.Diagnostic {
	return []*diag.Diagnostic{unsupportedDiag("selfhost lint")}
}

func unsupportedDiag(phase string) *diag.Diagnostic {
	pos := unsupportedPos()
	return diag.New(diag.Error, fmt.Sprintf("selfhost %s is unavailable", phase)).
		Primary(diag.Span{Start: pos, End: pos}, "").
		Note("the generated selfhost frontend and generators were removed").
		Build()
}

func unsupportedPos() token.Pos {
	return token.Pos{Line: 1, Column: 1, Offset: 0}
}
