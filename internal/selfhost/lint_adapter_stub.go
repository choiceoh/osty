//go:build selfhostgen

package selfhost

import "github.com/osty/osty/internal/diag"

func LintDiagnostics(src []byte) []*diag.Diagnostic {
	return nil
}
