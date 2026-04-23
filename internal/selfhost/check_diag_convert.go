package selfhost

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// CheckDiagnosticsAsDiag converts every CheckDiagnosticRecord in
// result into a *diag.Diagnostic using src to recover 1-based
// line/column positions from the byte offsets the native checker
// produced. Records with empty code AND empty message are dropped
// (they are signal-only placeholders). Severity, code, file, notes,
// and the primary span are preserved verbatim so downstream renderers
// and policy gates see the same shape whether they consumed the
// native CheckResult directly or went through the Go check.Result
// bridge.
//
// Callers that want per-record control should use
// CheckDiagnosticRecordAsDiag instead and filter before conversion.
func CheckDiagnosticsAsDiag(src []byte, records []CheckDiagnosticRecord) []*diag.Diagnostic {
	index := newDiagLineIndex(src)
	out := make([]*diag.Diagnostic, 0, len(records))
	for _, rec := range records {
		if converted := checkDiagnosticRecordAsDiagWithIndex(src, index, rec); converted != nil {
			out = append(out, converted)
		}
	}
	return out
}

// CheckDiagnosticRecordAsDiag lifts one record into a *diag.Diagnostic.
// Returns nil when both Code and Message are empty (so the caller's
// filtered slice stays compact without a dedicated skip branch).
//
// Byte offsets outside src are clamped to the valid range — the
// native checker occasionally reports a past-EOF end when the
// originating token is the trailing EOF marker, and we'd rather emit
// a clamped span than drop the diagnostic. Line/column come from a
// lightweight line-start index over src (no rune table needed; the
// native checker's offsets are already byte-accurate).
func CheckDiagnosticRecordAsDiag(src []byte, rec CheckDiagnosticRecord) *diag.Diagnostic {
	return checkDiagnosticRecordAsDiagWithIndex(src, newDiagLineIndex(src), rec)
}

func checkDiagnosticRecordAsDiagWithIndex(src []byte, index diagLineIndex, rec CheckDiagnosticRecord) *diag.Diagnostic {
	if rec.Code == "" && rec.Message == "" {
		return nil
	}
	severity := diag.Error
	switch strings.ToLower(strings.TrimSpace(rec.Severity)) {
	case "warning", "warn":
		severity = diag.Warning
	}
	b := diag.New(severity, rec.Message)
	if rec.Code != "" {
		b = b.Code(rec.Code)
	}
	if rec.File != "" {
		b = b.File(rec.File)
	}
	b = b.Primary(index.byteRangeSpan(rec.Start, rec.End), "")
	for _, note := range rec.Notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		b = b.Note(note)
	}
	return b.Build()
}

type diagLineIndex struct {
	starts []int
	total  int
}

func newDiagLineIndex(src []byte) diagLineIndex {
	starts := make([]int, 1, 1+len(src)/32)
	starts[0] = 0
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return diagLineIndex{starts: starts, total: len(src)}
}

func (idx diagLineIndex) byteRangeSpan(start, end int) diag.Span {
	start = idx.clampOffset(start)
	end = idx.clampOffset(end)
	if end < start {
		end = start
	}
	if idx.total == 0 {
		p := token.Pos{Line: 1, Column: 1, Offset: 0}
		return diag.Span{Start: p, End: p}
	}
	return diag.Span{
		Start: idx.positionAt(start),
		End:   idx.positionAt(end),
	}
}

func (idx diagLineIndex) clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > idx.total {
		return idx.total
	}
	return offset
}

func (idx diagLineIndex) positionAt(offset int) token.Pos {
	offset = idx.clampOffset(offset)
	lineIdx := sort.Search(len(idx.starts), func(i int) bool {
		return idx.starts[i] > offset
	}) - 1
	if lineIdx < 0 {
		lineIdx = 0
	}
	lineStart := idx.starts[lineIdx]
	return token.Pos{
		Line:   lineIdx + 1,
		Column: offset - lineStart + 1,
		Offset: offset,
	}
}
