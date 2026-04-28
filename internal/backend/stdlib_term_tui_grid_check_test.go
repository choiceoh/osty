package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestStdlibCheckResultTermTuiGrid pins that the new terminal/game support
// modules stay within the selfhost checker's supported source subset.
func TestStdlibCheckResultTermTuiGrid(t *testing.T) {
	reg := stdlib.LoadCached()
	for _, module := range []string{"term", "grid", "tui"} {
		t.Run(module, func(t *testing.T) {
			chk := stdlibCheckResult(reg, module)
			if chk == nil {
				t.Fatalf("stdlibCheckResult(%s) = nil, want non-nil *check.Result", module)
			}
			var errs []string
			for _, d := range chk.Diags {
				if d != nil && strings.Contains(d.Error(), "error") {
					errs = append(errs, d.Error())
				}
			}
			if len(errs) > 0 {
				t.Fatalf("%s module check produced %d error diagnostic(s):\n%s",
					module, len(errs), strings.Join(errs, "\n"))
			}
		})
	}
}
