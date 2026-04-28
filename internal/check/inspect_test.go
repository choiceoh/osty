package check

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

func TestInspectSourceUsesSelfhostRecords(t *testing.T) {
	src := []byte(`fn id(value: Int) -> Int { value }

fn main() {
    let answer: Int = id(1)
    answer
}
`)
	recs := InspectSource(src, nil)
	if len(recs) == 0 {
		t.Fatal("InspectSource returned no records")
	}
	if !hasRuleAnywhere(recs, "FN-DECL") {
		t.Fatalf("InspectSource missing FN-DECL record: %#v", recs)
	}
	if !hasRuleAnywhere(recs, "BIND") {
		t.Fatalf("InspectSource missing BIND record: %#v", recs)
	}
	for _, r := range recs {
		if r.TypeName != "" && r.Type == nil {
			t.Fatalf("record %s/%s has TypeName %q but nil compatibility Type", r.Rule, r.NodeKind, r.TypeName)
		}
	}
}

func TestInspectRecordsFromSelfhostMapsOffsetsAndRenderedNames(t *testing.T) {
	src := []byte("fn main() {\n    let answer = 42\n}\n")
	start := strings.Index(string(src), "42")
	recs := InspectRecordsFromSelfhost(src, []api.InspectRecord{{
		Start:    start,
		End:      start + 2,
		NodeKind: "IntLit",
		Rule:     "LIT-INT",
		Type:     &api.TypeRepr{Kind: "primitive", Name: "Int"},
		HintName: "Int",
		Notes:    []string{"from selfhost"},
	}})
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	got := recs[0]
	if got.Pos.Line != 2 || got.Pos.Column != 18 {
		t.Fatalf("mapped start = %d:%d, want 2:18", got.Pos.Line, got.Pos.Column)
	}
	if got.TypeName != "Int" || got.Type == nil || got.Type.String() != "Int" {
		t.Fatalf("type = %q / %v, want Int", got.TypeName, got.Type)
	}
	if got.HintName != "Int" {
		t.Fatalf("hint = %q, want Int", got.HintName)
	}
	if len(got.Notes) != 1 || got.Notes[0] != "from selfhost" {
		t.Fatalf("notes = %#v", got.Notes)
	}
}

func TestInspectTextFormat(t *testing.T) {
	recs := []InspectRecord{
		{
			Pos:      token.Pos{Line: 3, Column: 9},
			End:      token.Pos{Line: 3, Column: 10},
			NodeKind: "IntLit",
			Rule:     "LIT-INT",
			TypeName: "Int",
			HintName: "Int",
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
	if !strings.HasPrefix(lines[0], "1:1-1:5\t") || !strings.Contains(lines[0], "\tVAR\t") {
		t.Errorf("first line = %q, want sorted VAR line", lines[0])
	}
	if !strings.HasPrefix(lines[1], "3:9-3:10\t") || !strings.Contains(lines[1], "hint=Int") {
		t.Errorf("second line = %q, want hinted Int line", lines[1])
	}
}

func TestInspectJSONFormat(t *testing.T) {
	recs := []InspectRecord{{
		Pos:      token.Pos{Line: 2, Column: 5},
		End:      token.Pos{Line: 2, Column: 11},
		NodeKind: "CallExpr",
		Rule:     "CALL",
		TypeName: "Int",
		HintName: "Int",
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
}

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

func hasRuleAnywhere(recs []InspectRecord, rule string) bool {
	for _, r := range recs {
		if r.Rule == rule {
			return true
		}
	}
	return false
}
