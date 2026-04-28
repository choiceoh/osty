package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultEncoding pins that std.encoding is now a real
// pure-Osty codec module, not a declaration-only runtime surface.
func TestStdlibCheckResultEncoding(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "encoding")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(encoding) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("encoding module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
