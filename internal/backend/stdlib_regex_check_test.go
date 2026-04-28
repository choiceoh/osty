package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultRegex pins that std.regex's pure one-shot
// wrappers type-check cleanly while RE2 runtime primitives remain
// declaration-only.
func TestStdlibCheckResultRegex(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "regex")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(regex) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("regex module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
