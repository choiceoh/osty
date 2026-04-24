package check

// Formatters for InspectRecord. The text form is the default output of
// `osty check --inspect`; the JSON form (NDJSON — one record per line)
// is selected by the global --json flag and is intended for machine
// consumption by editors, CI bots, and future tools that want to drive
// explainers off the inspector stream.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// FormatInspectText writes recs to w as one line per record, sorted by
// source position. Column layout:
//
//	LINE:COL-LINE:COL    RULE         NodeKind        Type           hint=H   note1; note2
//
// Columns separated by tabs so output aligns when piped into `column -t`.
// Records whose Type is nil print "<?>" for the type column so missing
// entries are visually obvious.
func FormatInspectText(w io.Writer, recs []InspectRecord) error {
	sorted := append([]InspectRecord(nil), recs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Pos, sorted[j].Pos
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	for _, r := range sorted {
		line := formatInspectTextLine(r)
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func formatInspectTextLine(r InspectRecord) string {
	pos := fmt.Sprintf("%d:%d-%d:%d",
		r.Pos.Line, r.Pos.Column, r.End.Line, r.End.Column)
	rule := r.Rule
	if rule == "" {
		rule = "-"
	}
	typeStr := "<?>"
	if r.Type != nil {
		typeStr = r.Type.String()
	}
	cols := []string{pos, rule, r.NodeKind, typeStr}
	if r.Hint != nil {
		cols = append(cols, "hint="+r.Hint.String())
	} else {
		cols = append(cols, "")
	}
	if len(r.Notes) > 0 {
		cols = append(cols, strings.Join(r.Notes, "; "))
	}
	return strings.Join(cols, "\t")
}

// FormatInspectJSON writes recs to w as NDJSON, one record per line,
// in the original traversal order (not sorted, so consumers that want
// to reproduce the checker's walk can). Each line is a single JSON
// object with the fields documented on the inspectRecordJSON struct
// below.
func FormatInspectJSON(w io.Writer, recs []InspectRecord) error {
	enc := json.NewEncoder(w)
	for _, r := range recs {
		if err := enc.Encode(inspectRecordJSON{
			PosLine:   r.Pos.Line,
			PosColumn: r.Pos.Column,
			EndLine:   r.End.Line,
			EndColumn: r.End.Column,
			NodeKind:  r.NodeKind,
			Rule:      r.Rule,
			Type:      typeString(r.Type),
			Hint:      typeString(r.Hint),
			Notes:     r.Notes,
		}); err != nil {
			return err
		}
	}
	return nil
}

// inspectRecordJSON is the on-the-wire shape of a record. Kept separate
// from InspectRecord so we never leak types.Type pointers into JSON and
// so the field names are snake_case for conventional consumers.
type inspectRecordJSON struct {
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

func typeString(t interface{ String() string }) string {
	// nil-interface guard: a types.Type is an interface, so we compare
	// for a nil underlying pointer by falling back to reflection-free
	// nil checks through the concrete assertion in callers. Here we
	// accept the generic Stringer and rely on the caller having passed
	// nil rather than a wrapped-nil.
	if t == nil {
		return ""
	}
	return t.String()
}
