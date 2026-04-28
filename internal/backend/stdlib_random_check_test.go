package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultRandom pins that std.random's pure-Osty derived
// helpers type-check cleanly. The runtime-backed primitive surface
// should remain usable from helper bodies like choice and shuffle.
func TestStdlibCheckResultRandom(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "random")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(random) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("random module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
