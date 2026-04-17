// test_runner_policy.go is the Go snapshot of
// toolchain/test_runner.osty. Osty is the source of truth; the
// drift test in this package enforces parity.
package runner

import "strings"

// ResolveTestWorkers picks the worker-pool size for `osty test`
// given the flag state and discovered test count. See the Osty
// source for the precedence table.
//
// Osty: toolchain/test_runner.osty:24
func ResolveTestWorkers(serial bool, jobs, cpuCount, testCount int) int {
	if serial {
		return 1
	}
	effective := jobs
	if effective < 0 {
		effective = 0
	}
	if effective == 0 {
		effective = cpuCount
	}
	if effective < 1 {
		effective = 1
	}
	if effective > testCount {
		effective = testCount
	}
	return effective
}

// MatchesTestFilters returns true when `name` should be executed
// given the user's positional filter list. Empty filter list →
// run everything. Empty entries ignored. Otherwise substring
// match (cargo-style).
//
// Osty: toolchain/test_runner.osty:57
func MatchesTestFilters(name string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if f == "" {
			continue
		}
		if strings.Contains(name, f) {
			return true
		}
	}
	return false
}

// SanitizeNativeTestName turns a test_* identifier into a
// filesystem-safe directory stem. Letters/digits pass through;
// everything else collapses to `_`. Empty input → "osty_test".
//
// Osty: toolchain/test_runner.osty:75
func SanitizeNativeTestName(name string) string {
	if name == "" {
		return "osty_test"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "osty_test"
	}
	return b.String()
}
