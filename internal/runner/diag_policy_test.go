package runner

import "testing"

func TestIsDeferredGenCheckDiag(t *testing.T) {
	cases := []struct {
		name          string
		severity, msg string
		want          bool
	}{
		{"error-with-marker-prefix", "error", "type checking unavailable for /tmp/foo.osty", true},
		{"warning-even-with-marker", "warning", "type checking unavailable for /tmp/foo.osty", false},
		{"note-even-with-marker", "note", "type checking unavailable for /tmp/foo.osty", false},
		{"error-without-marker", "error", "undefined name `foo`", false},
		{"error-empty-message", "error", "", false},
		{"empty-severity", "", "type checking unavailable for x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsDeferredGenCheckDiag(c.severity, c.msg); got != c.want {
				t.Errorf("IsDeferredGenCheckDiag(%q, %q) = %v, want %v",
					c.severity, c.msg, got, c.want)
			}
		})
	}
}

func TestScaffoldExitCode(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"", 0},
		{"E2050", 2},
		{"E2051", 1},
		{"E9999", 1},
		{"L0001", 1},
	}
	for _, c := range cases {
		if got := ScaffoldExitCode(c.code); got != c.want {
			t.Errorf("ScaffoldExitCode(%q) = %d, want %d", c.code, got, c.want)
		}
	}
}
