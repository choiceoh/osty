package ci

import (
	"github.com/osty/osty/internal/diag"
)

// hasError reports whether any diagnostic in ds has Error severity.
func hasError(ds []*diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// hasWarning reports whether any diagnostic in ds has Warning severity.
func hasWarning(ds []*diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.Warning {
			return true
		}
	}
	return false
}

// synthetic builds a diagnostic for a check whose finding isn't
// anchored to a source position (manifest fields, lockfile state,
// etc.). The CLI renders these as plain header + message; the
// body still appears in --json output with the CI code so tooling
// can route on it.
func synthetic(sev diag.Severity, code, msg string) *diag.Diagnostic {
	return diag.New(sev, msg).Code(code).Build()
}
