package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

func TestStdlibCheckResultBarcodeQr(t *testing.T) {
	reg := stdlib.LoadCached()
	for _, module := range []string{"barcode", "qr"} {
		t.Run(module, func(t *testing.T) {
			chk := stdlibCheckResult(reg, module)
			if chk == nil {
				t.Fatalf("stdlibCheckResult(%s) = nil, want non-nil *check.Result", module)
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
				t.Fatalf("%s module check produced %d error diagnostic(s):\n%s",
					module, len(errs), strings.Join(errs, "\n"))
			}
		})
	}
}
