package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

func TestStdlibCheckResultTemplate(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "template")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(template) = nil, want non-nil *check.Result")
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
		t.Fatalf("template module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}
