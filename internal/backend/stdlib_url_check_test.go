package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultUrl pins that the pure-Osty implementation in
// url.osty type-checks cleanly against the native selfhost checker.
// A regression here usually means a new language-surface dependency
// slipped in (e.g. `if let` pattern, `!c.isDigit()`) that the native
// checker doesn't yet understand. Either refactor the url.osty body
// back to a supported idiom or extend the native checker.
func TestStdlibCheckResultUrl(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "url")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(url) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("url module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
