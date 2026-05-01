package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultDiff pins std.diff's pure-Osty line differ against
// the selfhost checker. The module leans on nested list mutation, enum
// methods, and structured hunk rendering, so it is a useful regression guard
// for practical source-review tooling.
func TestStdlibCheckResultDiff(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "diff")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(diff) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			msg := d.Error()
			if len(d.Notes) > 0 {
				msg += "\n  notes: " + strings.Join(d.Notes, "\n  ")
			}
			errs = append(errs, msg)
		}
	}
	if len(errs) > 0 {
		t.Fatalf("diff module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
