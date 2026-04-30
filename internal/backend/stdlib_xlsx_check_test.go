package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultXlsx pins that std.xlsx's pure-Osty XLSX package
// model type-checks cleanly through the selfhost checker.
func TestStdlibCheckResultXlsx(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "xlsx")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(xlsx) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("xlsx module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
