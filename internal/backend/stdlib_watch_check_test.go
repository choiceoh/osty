package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

func TestStdlibCheckResultWatch(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "watch")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(watch) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("watch module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
