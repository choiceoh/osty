package selfhost

import (
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
	out := make([]*diag.Diagnostic, 0, len(records))
	for _, rec := range records {
		if converted := CheckDiagnosticRecordAsDiag(src, rec); converted != nil {
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
// single linear scan of src (no rune table needed; the native
// checker's offsets are already byte-accurate).
func CheckDiagnosticRecordAsDiag(src []byte, rec CheckDiagnosticRecord) *diag.Diagnostic {
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
	b = b.Primary(byteRangeSpanForDiag(src, rec.Start, rec.End), "")
	for _, note := range rec.Notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		b = b.Note(note)
	}
	return b.Build()
}

// byteRangeSpanForDiag builds a diag.Span over the [start, end) byte
// range in src. Mirrors internal/check/host_boundary.go:byteRangeSpan
// so both entry points produce identical spans for equivalent input
// — keep the two in sync.
func byteRangeSpanForDiag(src []byte, start, end int) diag.Span {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if len(src) == 0 {
		p := token.Pos{Line: 1, Column: 1, Offset: 0}
		return diag.Span{Start: p, End: p}
	}
	if start > len(src) {
		start = len(src)
	}
	if end > len(src) {
		end = len(src)
	}
	return diag.Span{
		Start: positionAtOffsetForDiag(src, start),
		End:   positionAtOffsetForDiag(src, end),
	}
}

func positionAtOffsetForDiag(src []byte, offset int) token.Pos {
	line := 1
	col := 1
	for i := 0; i < offset && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return token.Pos{Line: line, Column: col, Offset: offset}
}
