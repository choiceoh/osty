package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultSchedule pins std.schedule's pure-Osty planner:
// cron parsing/matching, interval/daily next-run calculation, and retry task
// state should type-check without a scheduler runtime shim.
func TestStdlibCheckResultSchedule(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "schedule")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(schedule) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("schedule module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
