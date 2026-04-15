package lint

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
)

// ApplyFixes rewrites `src` in-place according to every
// MachineApplicable suggestion attached to the diagnostics. Returns the
// new bytes plus a count of how many fixes were applied.
//
// Fixes are applied in REVERSE order by byte offset so earlier offsets
// remain valid. Overlapping fixes are handled conservatively: when two
// fixes touch the same byte range, only the first one (highest offset)
// is applied; subsequent overlaps are skipped and returned via the
// skipped count.
//
// Suggestions with a non-nil CopyFrom span have their replacement
// resolved against the ORIGINAL source bytes, with the template's "%s"
// marker (if any) substituted by the copied substring. A template of
// plain "%s" or an empty string means "copy verbatim".
//
// Non-machine-applicable suggestions are ignored — those are prose
// hints only.
func ApplyFixes(src []byte, diags []*diag.Diagnostic) (out []byte, applied, skipped int) {
	type edit struct {
		start, end  int
		replacement string
	}
	var edits []edit
	for _, d := range diags {
		for _, s := range d.Suggestions {
			if !s.MachineApplicable {
				continue
			}
			start := s.Span.Start.Offset
			end := s.Span.End.Offset
			if start < 0 || end < start || end > len(src) {
				skipped++
				continue
			}
			replacement := s.Replacement
			if s.CopyFrom != nil {
				cs := s.CopyFrom.Start.Offset
				ce := s.CopyFrom.End.Offset
				if cs < 0 || ce < cs || ce > len(src) {
					skipped++
					continue
				}
				copied := string(src[cs:ce])
				if s.Replacement == "" || !strings.Contains(s.Replacement, "%s") {
					replacement = copied
				} else {
					replacement = strings.Replace(s.Replacement, "%s", copied, 1)
				}
			}
			edits = append(edits, edit{start: start, end: end, replacement: replacement})
		}
	}
	if len(edits) == 0 {
		return src, 0, 0
	}
	// Sort by start descending so patches don't invalidate each other's
	// offsets.
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	out = append([]byte(nil), src...)
	var lastStart = len(src) + 1 // sentinel: first fix always smaller
	for _, e := range edits {
		// Overlap check: if this edit's end extends past the start of
		// an earlier-applied edit (i.e. e.end > lastStart), skip.
		if e.end > lastStart {
			skipped++
			continue
		}
		out = append(append([]byte(nil), out[:e.start]...), append([]byte(e.replacement), out[e.end:]...)...)
		applied++
		lastStart = e.start
	}
	return out, applied, skipped
}
