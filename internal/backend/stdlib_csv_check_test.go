package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultCsv pins that std.csv's pure-Osty implementation
// type-checks cleanly. CSV is a user-facing production module; regressions
// here usually mean a helper drifted away from the canonical collection or
// Result surfaces.
func TestStdlibCheckResultCsv(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "csv")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(csv) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("csv module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
