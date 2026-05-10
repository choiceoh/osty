package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/spanid"
	"github.com/osty/osty/internal/token"
)

type nativeDiagPolicy struct {
	privileged bool
}

func nativeCheckerTelemetry(checked api.CheckResult, policy nativeDiagPolicy) *NativeCheckerTelemetry {
	summary := filteredNativeSummary(checked, policy)
	if summary.Assignments == 0 && summary.Errors == 0 && len(summary.ErrorsByContext) == 0 {
		return nil
	}
	return &NativeCheckerTelemetry{
		Assignments:     summary.Assignments,
		Accepted:        summary.Accepted,
		Errors:          summary.Errors,
		ErrorsByContext: cloneStringIntMap(summary.ErrorsByContext),
		ErrorDetails:    cloneErrorDetailMap(summary.ErrorDetails),
	}
}

func cloneErrorDetailMap(src map[string]map[string]int) map[string]map[string]int {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]map[string]int, len(src))
	for ctx, inner := range src {
		out[ctx] = cloneStringIntMap(inner)
	}
	return out
}

func cloneStringIntMap(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func nativeCheckerDiags(src []byte, checked api.CheckResult, policy nativeDiagPolicy) []*diag.Diagnostic {
	out := make([]*diag.Diagnostic, 0, len(checked.Diagnostics))
	for _, d := range checked.Diagnostics {
		if shouldSuppressNativeDiag(d, policy) {
			continue
		}
		if converted := convertNativeDiag(src, d); converted != nil {
			out = append(out, converted)
		}
	}
	summary := filteredNativeSummary(checked, policy)
	if summary.Errors == 0 {
		return out
	}
	label := "native checker reported type errors"
	if summary.Errors == 1 {
		label = "native checker reported a type error"
	}
	out = append(out,
		diag.New(diag.Error, fmt.Sprintf("%s: %d error(s)", label, summary.Errors)).
			Code(diag.CodeTypeMismatch).
			Primary(fileStartSpan(src), "native checker summary").
			Note(fmt.Sprintf(
				"native checker accepted %d of %d assignment/return/call checks",
				summary.Accepted,
				summary.Assignments,
			)).
			Build(),
	)
	return out
}

func nativeCheckerDiagsForCheckedSource(src selfhostCheckedSource, checked api.CheckResult, policy nativeDiagPolicy) []*diag.Diagnostic {
	out := make([]*diag.Diagnostic, 0, len(checked.Diagnostics))
	for _, d := range checked.Diagnostics {
		if shouldSuppressNativeDiag(d, policy) {
			continue
		}
		if converted := convertNativeDiagForCheckedSource(src, d); converted != nil {
			out = append(out, converted)
		}
	}
	summary := filteredNativeSummary(checked, policy)
	if summary.Errors == 0 {
		return out
	}
	label := "native checker reported type errors"
	if summary.Errors == 1 {
		label = "native checker reported a type error"
	}
	out = append(out,
		diag.New(diag.Error, fmt.Sprintf("%s: %d error(s)", label, summary.Errors)).
			Code(diag.CodeTypeMismatch).
			Primary(fileStartSpan(src.source), "native checker summary").
			Note(fmt.Sprintf(
				"native checker accepted %d of %d assignment/return/call checks",
				summary.Accepted,
				summary.Assignments,
			)).
			Build(),
	)
	return out
}

func convertNativeDiagForCheckedSource(src selfhostCheckedSource, d api.CheckDiagnosticRecord) *diag.Diagnostic {
	seg, relStart, relEnd, ok := nativeDiagSegment(src, d)
	if !ok {
		return convertNativeDiag(src.source, d)
	}
	mapped := d
	mapped.Start = relStart
	mapped.End = relEnd
	if mapped.File == "" {
		mapped.File = seg.path
	}
	if mapped.SourceFileID == "" && seg.sourceID != "" {
		mapped.SourceFileID = string(seg.sourceID)
	}
	if mapped.SpanID == "" && mapped.SourceFileID != "" {
		mapped.SpanID = string(spanid.SpanIDFor(spanid.SourceFileID(mapped.SourceFileID), mapped.Start, mapped.End))
	}
	if seg.base != 0 {
		mapped.Provenance = append(mapped.Provenance, api.SpanProvenanceRecord{
			Kind:         string(spanid.ProvenanceSelfhostShift),
			SourceFileID: mapped.SourceFileID,
			SpanID:       mapped.SpanID,
			Detail:       fmt.Sprintf("base:%d", seg.base),
		})
	}
	return convertNativeDiag(seg.source, mapped)
}

func nativeDiagSegment(src selfhostCheckedSource, d api.CheckDiagnosticRecord) (selfhostFileSegment, int, int, bool) {
	for _, seg := range src.files {
		if d.File != "" && seg.path != "" && d.File != seg.path {
			continue
		}
		if len(seg.source) == 0 {
			continue
		}
		relStart := d.Start - seg.base
		relEnd := d.End - seg.base
		if relStart < 0 || relStart > len(seg.source) {
			continue
		}
		if relEnd < relStart {
			relEnd = relStart
		}
		if relEnd > len(seg.source) {
			relEnd = len(seg.source)
		}
		return seg, relStart, relEnd, true
	}
	return selfhostFileSegment{}, 0, 0, false
}

func filteredNativeSummary(checked api.CheckResult, policy nativeDiagPolicy) api.CheckSummary {
	summary := checked.Summary
	if !policy.privileged {
		return summary
	}
	suppressed := 0
	for _, d := range checked.Diagnostics {
		if shouldSuppressNativeDiag(d, policy) && nativeDiagIsError(d) {
			suppressed++
		}
	}
	if suppressed == 0 {
		return summary
	}
	if summary.Errors < suppressed {
		summary.Errors = 0
	} else {
		summary.Errors -= suppressed
	}
	if len(summary.ErrorsByContext) > 0 {
		summary.ErrorsByContext = cloneStringIntMap(summary.ErrorsByContext)
		delete(summary.ErrorsByContext, diag.CodeRuntimePrivilegeViolation)
		if len(summary.ErrorsByContext) == 0 {
			summary.ErrorsByContext = nil
		}
	}
	if len(summary.ErrorDetails) > 0 {
		summary.ErrorDetails = cloneErrorDetailMap(summary.ErrorDetails)
		delete(summary.ErrorDetails, diag.CodeRuntimePrivilegeViolation)
		if len(summary.ErrorDetails) == 0 {
			summary.ErrorDetails = nil
		}
	}
	return summary
}

func shouldSuppressNativeDiag(d api.CheckDiagnosticRecord, policy nativeDiagPolicy) bool {
	return policy.privileged && d.Code == diag.CodeRuntimePrivilegeViolation
}

func nativeDiagIsError(d api.CheckDiagnosticRecord) bool {
	switch strings.ToLower(strings.TrimSpace(d.Severity)) {
	case "warning", "warn", "lint":
		return false
	default:
		return true
	}
}

// convertNativeDiag lifts a per-record structured diagnostic emitted by
// the Osty-native checker (see toolchain/check_diag.osty) into a
// `*diag.Diagnostic`. Modern records carry checker-owned display
// positions; older subprocesses that only send byte offsets still fall
// back to a source scan.
func convertNativeDiag(src []byte, d api.CheckDiagnosticRecord) *diag.Diagnostic {
	if d.Code == "" && d.Message == "" {
		return nil
	}
	severity := diag.Error
	switch strings.ToLower(d.Severity) {
	case "warning", "warn":
		severity = diag.Warning
	}
	b := diag.New(severity, d.Message)
	if d.Code != "" {
		b = b.Code(d.Code)
	}
	if d.File != "" {
		b = b.File(d.File)
	}
	b = b.Primary(nativeDiagSpanWithIdentity(src, d), "")
	for _, note := range d.Notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		b = b.Note(note)
	}
	return b.Build()
}

func nativeDiagSpan(src []byte, d api.CheckDiagnosticRecord) diag.Span {
	if d.StartLine > 0 && d.StartColumn > 0 {
		endLine := d.EndLine
		endColumn := d.EndColumn
		if endLine <= 0 {
			endLine = d.StartLine
		}
		if endColumn <= 0 {
			endColumn = d.StartColumn
		}
		return diag.Span{
			Start: token.Pos{Line: d.StartLine, Column: d.StartColumn, Offset: d.Start},
			End:   token.Pos{Line: endLine, Column: endColumn, Offset: d.End},
		}
	}
	return byteRangeSpan(src, d.Start, d.End)
}

func nativeDiagSpanWithIdentity(src []byte, d api.CheckDiagnosticRecord) diag.Span {
	span := nativeDiagSpan(src, d)
	if d.SourceFileID != "" {
		span = diag.StampSpanSourceFileID(span, spanid.SourceFileID(d.SourceFileID))
	}
	if d.SpanID != "" {
		span.ID = spanid.SpanID(d.SpanID)
	}
	for _, p := range d.Provenance {
		span.Provenance = spanid.AppendProvenance(span.Provenance, spanid.Provenance{
			Kind:         spanid.ProvenanceKind(p.Kind),
			SourceFileID: spanid.SourceFileID(p.SourceFileID),
			SpanID:       spanid.SpanID(p.SpanID),
			Detail:       p.Detail,
		})
	}
	return span
}

// byteRangeSpan builds a `diag.Span` for a [start, end) byte range
// into `src`. A lightweight line-start index keeps repeated
// conversions off the O(offset) byte-walk path while preserving the
// same 1-based line/column shape. Clamping keeps downstream
// renderers robust even when the native checker reports offsets
// beyond EOF.
func byteRangeSpan(src []byte, start, end int) diag.Span {
	idx := newSelfhostDiagLineIndex(src)
	start = idx.clampOffset(start)
	end = idx.clampOffset(end)
	if end < start {
		end = start
	}
	if idx.total == 0 {
		p := token.Pos{Line: 1, Column: 1, Offset: 0}
		return diag.Span{Start: p, End: p}
	}
	return diag.Span{Start: idx.positionAt(start), End: idx.positionAt(end)}
}

type selfhostDiagLineIndex struct {
	starts []int
	total  int
}

func newSelfhostDiagLineIndex(src []byte) selfhostDiagLineIndex {
	starts := make([]int, 1, 1+len(src)/32)
	starts[0] = 0
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return selfhostDiagLineIndex{starts: starts, total: len(src)}
}

func (idx selfhostDiagLineIndex) clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > idx.total {
		return idx.total
	}
	return offset
}

func (idx selfhostDiagLineIndex) positionAt(offset int) token.Pos {
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

func fileStartSpan(src []byte) diag.Span {
	start := token.Pos{Line: 1, Column: 1, Offset: 0}
	end := start
	if len(src) > 0 {
		end = token.Pos{Line: 1, Column: 2, Offset: 1}
	}
	return diag.Span{Start: start, End: end}
}
