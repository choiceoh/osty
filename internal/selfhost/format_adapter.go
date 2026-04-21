package selfhost

import (
	"bytes"
	"fmt"

	"github.com/osty/osty/internal/diag"
)

// FormatterCheckResult reports whether the self-host formatter would rewrite
// the original source bytes and returns the canonical output.
type FormatterCheckResult struct {
	Changed bool
	Output  []byte
}

// FormatSource runs the bootstrap-generated pure-Osty formatter. This exists
// so Go-side tests and drift guards can exercise the self-host printer through
// a stable adapter without routing user-facing formatting away from
// internal/format.Source, which remains the canonical CLI contract.
func FormatSource(src []byte) ([]byte, []*diag.Diagnostic, error) {
	normalized := normalizeFormatterInput(src)
	formatted := ostyFormatSource(string(normalized))
	if formatted.ok {
		return []byte(formatted.output), nil, nil
	}
	diags, err := formatFailure(normalized, formatted.message)
	return nil, diags, err
}

// FormatCheck reports whether FormatSource would change the original source
// bytes. Raw-byte comparison is intentional so BOM/CRLF normalization counts
// as a formatting change just like the public `osty fmt --engine=osty` path.
func FormatCheck(src []byte) (FormatterCheckResult, []*diag.Diagnostic, error) {
	normalized := normalizeFormatterInput(src)
	formatted := ostyFormatSource(string(normalized))
	if formatted.ok {
		out := []byte(formatted.output)
		return FormatterCheckResult{
			Changed: !bytes.Equal(src, out),
			Output:  out,
		}, nil, nil
	}
	var zero FormatterCheckResult
	diags, err := formatFailure(normalized, formatted.message)
	return zero, diags, err
}

func formatFailure(src []byte, message string) ([]*diag.Diagnostic, error) {
	diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		return diags, fmt.Errorf("cannot format file with parse errors")
	}
	return nil, fmt.Errorf("selfhost formatter failed: %s", message)
}

func normalizeFormatterInput(src []byte) []byte {
	src = bytes.TrimPrefix(src, []byte{0xEF, 0xBB, 0xBF})
	if !bytes.Contains(src, []byte{'\r'}) {
		return src
	}
	out := make([]byte, 0, len(src))
	for idx := 0; idx < len(src); idx++ {
		if src[idx] != '\r' {
			out = append(out, src[idx])
			continue
		}
		if idx+1 < len(src) && src[idx+1] == '\n' {
			continue
		}
		out = append(out, '\n')
	}
	return out
}
