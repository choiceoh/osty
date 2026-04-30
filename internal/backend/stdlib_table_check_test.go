package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultTable pins std.table's pure-Osty dataframe layer
// against the selfhost checker. It leans on std.csv, std.strings, function
// parameters, maps, and nested collection shapes, so it catches several
// practical stdlib drift modes at once.
func TestStdlibCheckResultTable(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "table")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(table) = nil, want non-nil *check.Result")
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
		t.Fatalf("table module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
