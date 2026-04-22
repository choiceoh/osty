package check

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// TestInspectEmitsRuleLabelsForBasicExpressions walks a small AST and
// verifies that each node yields an InspectRecord with the rule label
// and type we expect. The Result is hand-built so the test is
// independent of the self-hosted checker — it exercises only the
// post-hoc classifier in inspect.go.
func TestInspectEmitsRuleLabelsForBasicExpressions(t *testing.T) {
	src := []byte(`fn id(value: Int) -> Int { value }

fn main() {
    let answer: Int = id(1)
    let total = 2 + answer
}
`)
	file, _ := parseResolvedFile(t, src)

	mainDecl := file.Decls[1].(*ast.FnDecl)
	letAnswer := mainDecl.Body.Stmts[0].(*ast.LetStmt)
	letTotal := mainDecl.Body.Stmts[1].(*ast.LetStmt)
	callID := letAnswer.Value.(*ast.CallExpr)
	argOne := callID.Args[0].Value.(*ast.IntLit)
	idIdent := callID.Fn.(*ast.Ident)
	binop := letTotal.Value.(*ast.BinaryExpr)
	litTwo := binop.Left.(*ast.IntLit)
	answerIdent := binop.Right.(*ast.Ident)

	fnIntToInt := &types.FnType{Params: []types.Type{types.Int}, Return: types.Int}

	chk := &Result{
		Types: map[ast.Expr]types.Type{
			callID:        types.Int,
			argOne:        types.Int,
			idIdent:       fnIntToInt,
			binop:         types.Int,
			litTwo:        types.Int,
			answerIdent:   types.Int,
			mainDecl.Body: types.Unit,
		},
		LetTypes: map[ast.Node]types.Type{
			letAnswer: types.Int,
			letTotal:  types.Int,
		},
		SymTypes:           map[*resolve.Symbol]types.Type{},
		InstantiationsByID: map[ast.NodeID][]types.Type{},
	}

	recs := Inspect(file, chk)

	// Verify we emitted records for both LetStmts at the right lines.
	if !hasRule(recs, "LET", letAnswer.Pos().Line) {
		t.Errorf("missing LET record at line %d", letAnswer.Pos().Line)
	}
	if !hasRule(recs, "LET", letTotal.Pos().Line) {
		t.Errorf("missing LET record at line %d", letTotal.Pos().Line)
	}
	// CALL, VAR, LIT-INT, BINOP should all appear somewhere.
	for _, rule := range []string{"CALL", "VAR", "LIT-INT", "BINOP"} {
		if !hasRuleAnywhere(recs, rule) {
			t.Errorf("missing %s record", rule)
		}
	}
	// The call argument receives an Int hint from the callee's parameter.
	r := findRecordAt(recs, argOne.Pos().Line, argOne.Pos().Column)
	if r == nil {
		t.Fatalf("no record at arg position (line %d col %d)", argOne.Pos().Line, argOne.Pos().Column)
	}
	if r.Rule != "LIT-INT" {
		t.Errorf("arg rule = %q, want LIT-INT", r.Rule)
	}
	if r.Hint == nil || r.Hint.String() != "Int" {
		t.Errorf("arg hint = %v, want Int", r.Hint)
	}

	// The let with annotation records its declared type as the hint.
	letRec := findRecordExact(recs, letAnswer)
	if letRec == nil {
		t.Fatalf("no LET record for letAnswer")
	}
	if letRec.Hint == nil || letRec.Hint.String() != "Int" {
		t.Errorf("letAnswer hint = %v, want Int from annotation", letRec.Hint)
	}

	// The let without annotation has no hint but does have a bound type.
	letTotalRec := findRecordExact(recs, letTotal)
	if letTotalRec == nil {
		t.Fatalf("no LET record for letTotal")
	}
	if letTotalRec.Hint != nil {
		t.Errorf("letTotal hint = %v, want nil (synth mode)", letTotalRec.Hint)
	}
	if letTotalRec.Type == nil || letTotalRec.Type.String() != "Int" {
		t.Errorf("letTotal type = %v, want Int", letTotalRec.Type)
	}
}

// TestInspectTextFormat dumps a tiny record list through the text
// formatter and checks for the expected column shape and ordering.
func TestInspectTextFormat(t *testing.T) {
	recs := []InspectRecord{
		{
			Pos:      token.Pos{Line: 3, Column: 9},
			End:      token.Pos{Line: 3, Column: 10},
			NodeKind: "IntLit",
			Rule:     "LIT-INT",
			Type:     types.Int,
			Hint:     types.Int,
		},
		{
			Pos:      token.Pos{Line: 1, Column: 1},
			End:      token.Pos{Line: 1, Column: 5},
			NodeKind: "Ident",
			Rule:     "VAR",
			Type:     types.Int,
		},
	}
	var buf bytes.Buffer
	if err := FormatInspectText(&buf, recs); err != nil {
		t.Fatalf("FormatInspectText: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2\nOUTPUT:\n%s", len(lines), out)
	}
	// Sorted by position: line 1 first, then line 3.
	if !strings.HasPrefix(lines[0], "1:1-1:5\t") {
		t.Errorf("first line = %q, want prefix 1:1-1:5", lines[0])
	}
	if !strings.Contains(lines[0], "\tVAR\t") {
		t.Errorf("first line missing VAR column: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "3:9-3:10\t") {
		t.Errorf("second line = %q, want prefix 3:9-3:10", lines[1])
	}
	if !strings.Contains(lines[1], "hint=Int") {
		t.Errorf("second line missing hint column: %q", lines[1])
	}
}

// TestInspectJSONFormat round-trips a record through the NDJSON
// emitter and verifies each field survives the encoding.
func TestInspectJSONFormat(t *testing.T) {
	recs := []InspectRecord{{
		Pos:      token.Pos{Line: 2, Column: 5},
		End:      token.Pos{Line: 2, Column: 11},
		NodeKind: "CallExpr",
		Rule:     "CALL",
		Type:     types.Int,
		Hint:     types.Int,
		Notes:    []string{"instantiated [Int]"},
	}}
	var buf bytes.Buffer
	if err := FormatInspectJSON(&buf, recs); err != nil {
		t.Fatalf("FormatInspectJSON: %v", err)
	}
	var got inspectRecordJSONDecoded
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, buf.String())
	}
	if got.Rule != "CALL" || got.NodeKind != "CallExpr" {
		t.Errorf("got = %+v, want Rule=CALL NodeKind=CallExpr", got)
	}
	if got.Type != "Int" || got.Hint != "Int" {
		t.Errorf("got type/hint = %q/%q, want Int/Int", got.Type, got.Hint)
	}
	if len(got.Notes) != 1 || got.Notes[0] != "instantiated [Int]" {
		t.Errorf("notes = %v, want [instantiated [Int]]", got.Notes)
	}
	if got.PosLine != 2 || got.PosColumn != 5 {
		t.Errorf("pos = %d:%d, want 2:5", got.PosLine, got.PosColumn)
	}
}

// TestInspectGenericInstantiation confirms the CALL note surfaces the
// type arguments recorded by the checker. This is the observability
// of §2a.7 (generic instantiation).
func TestInspectGenericInstantiation(t *testing.T) {
	src := []byte(`fn id<T>(value: T) -> T { value }

fn main() {
    let answer = id(1)
}
`)
	file, _ := parseResolvedFile(t, src)
	mainDecl := file.Decls[1].(*ast.FnDecl)
	letAnswer := mainDecl.Body.Stmts[0].(*ast.LetStmt)
	call := letAnswer.Value.(*ast.CallExpr)
	arg := call.Args[0].Value.(*ast.IntLit)

	chk := &Result{
		Types: map[ast.Expr]types.Type{
			call: types.Int,
			arg:  types.Int,
		},
		LetTypes: map[ast.Node]types.Type{
			letAnswer: types.Int,
		},
		SymTypes: map[*resolve.Symbol]types.Type{},
		InstantiationsByID: map[ast.NodeID][]types.Type{
			call.ID: {types.Int},
		},
	}
	recs := Inspect(file, chk)

	callRec := findRecordExact(recs, call)
	if callRec == nil {
		t.Fatalf("no CALL record for id(1)")
	}
	found := false
	for _, n := range callRec.Notes {
		if strings.Contains(n, "instantiated") && strings.Contains(n, "Int") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CALL notes %v do not mention instantiation", callRec.Notes)
	}
}

// ---- helpers ----

// inspectRecordJSONDecoded mirrors the on-the-wire shape. Must stay in
// sync with inspectRecordJSON in inspect_format.go — but this is a
// deliberately separate copy so a field rename in the emitter shows up
// as a test failure here rather than a silent pass.
type inspectRecordJSONDecoded struct {
	PosLine   int      `json:"pos_line"`
	PosColumn int      `json:"pos_column"`
	EndLine   int      `json:"end_line"`
	EndColumn int      `json:"end_column"`
	NodeKind  string   `json:"node_kind"`
	Rule      string   `json:"rule"`
	Type      string   `json:"type,omitempty"`
	Hint      string   `json:"hint,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

func hasRule(recs []InspectRecord, rule string, line int) bool {
	for _, r := range recs {
		if r.Rule == rule && r.Pos.Line == line {
			return true
		}
	}
	return false
}

func hasRuleAnywhere(recs []InspectRecord, rule string) bool {
	for _, r := range recs {
		if r.Rule == rule {
			return true
		}
	}
	return false
}

func findRecordAt(recs []InspectRecord, line, col int) *InspectRecord {
	for i := range recs {
		if recs[i].Pos.Line == line && recs[i].Pos.Column == col {
			return &recs[i]
		}
	}
	return nil
}

// findRecordExact returns the first record whose Pos/End match the
// given node. Used when line+column alone might be ambiguous (e.g.
// two nodes sharing a start position).
func findRecordExact(recs []InspectRecord, n ast.Node) *InspectRecord {
	for i := range recs {
		if recs[i].Pos == n.Pos() && recs[i].End == n.End() {
			return &recs[i]
		}
	}
	return nil
}
