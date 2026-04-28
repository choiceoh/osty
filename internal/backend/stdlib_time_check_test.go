package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultTime pins that std.time's pure helper bodies
// type-check cleanly while runtime-backed clock, zone, and formatting
// entries remain declaration-only.
func TestStdlibCheckResultTime(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "time")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(time) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("time module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
