// diag_policy.go is the Go snapshot of
// toolchain/diag_policy.osty. Osty is the source of truth; the
// drift test in this package enforces parity.
package runner

import "strings"

// IsDeferredGenCheckDiag reports whether an error-severity
// diagnostic from `osty gen`'s checker is a "native type checker
// unavailable" marker. Those get demoted from blocking errors to
// notes so gen can still emit an artifact while surfacing the
// limitation.
//
// severity uses internal/diag's stringly form ("error", "warning",
// "note"). Only "error" gates this branch.
//
// Osty: toolchain/diag_policy.osty:22
func IsDeferredGenCheckDiag(severity, message string) bool {
	if severity != "error" {
		return false
	}
	return strings.HasPrefix(message, "type checking unavailable for ")
}

// ScaffoldExitCode maps a scaffold diagnostic code to the process
// exit code the CLI should surface. See the Osty source for the
// full rule table.
//
// Osty: toolchain/diag_policy.osty:42
func ScaffoldExitCode(diagCode string) int {
	if diagCode == "" {
		return 0
	}
	if diagCode == "E2050" {
		return 2
	}
	return 1
}
