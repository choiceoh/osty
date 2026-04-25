package runner

import "testing"

func TestParseAiRepairModeKnownValues(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "auto"},
		{"auto", "auto"},
		{"rewrite", "rewrite"},
		{"parse", "parse"},
		{"frontend", "frontend"},
	}
	for _, c := range cases {
		got := ParseAiRepairMode(c.in)
		if !got.Ok {
			t.Errorf("ParseAiRepairMode(%q).Ok = false, want true", c.in)
		}
		if got.Mode != c.want {
			t.Errorf("ParseAiRepairMode(%q).Mode = %q, want %q", c.in, got.Mode, c.want)
		}
	}
}

func TestParseAiRepairModeRejectsUnknown(t *testing.T) {
	got := ParseAiRepairMode("nope")
	if got.Ok {
		t.Errorf("ParseAiRepairMode(nope).Ok = true, want false")
	}
	if got.Mode != "" {
		t.Errorf("ParseAiRepairMode(nope).Mode = %q, want empty", got.Mode)
	}
}

func TestParseAiRepairCaptureModeKnownValues(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "residual"},
		{"residual", "residual"},
		{"changed", "changed"},
		{"always", "always"},
	}
	for _, c := range cases {
		got := ParseAiRepairCaptureMode(c.in)
		if !got.Ok {
			t.Errorf("ParseAiRepairCaptureMode(%q).Ok = false, want true", c.in)
		}
		if got.Mode != c.want {
			t.Errorf("ParseAiRepairCaptureMode(%q).Mode = %q, want %q", c.in, got.Mode, c.want)
		}
	}
}

func TestParseAiRepairCaptureModeRejectsUnknown(t *testing.T) {
	got := ParseAiRepairCaptureMode("sometimes")
	if got.Ok {
		t.Errorf("ParseAiRepairCaptureMode(sometimes).Ok = true, want false")
	}
}

func TestUsesFrontEndAIRepair(t *testing.T) {
	enabled := []string{"check", "typecheck", "resolve", "lint", "pipeline"}
	for _, cmd := range enabled {
		if !UsesFrontEndAIRepair(cmd) {
			t.Errorf("UsesFrontEndAIRepair(%q) = false, want true", cmd)
		}
	}
	disabled := []string{"run", "build", "test", "gen", ""}
	for _, cmd := range disabled {
		if UsesFrontEndAIRepair(cmd) {
			t.Errorf("UsesFrontEndAIRepair(%q) = true, want false", cmd)
		}
	}
}

func TestShouldCaptureAiRepair(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		changed     bool
		totalErrors int
		want        bool
	}{
		{"always-no-changes-no-errors", "always", false, 0, true},
		{"always-changed-errors", "always", true, 5, true},
		{"changed-true", "changed", true, 0, true},
		{"changed-false", "changed", false, 5, false},
		{"residual-errors-remain", "residual", false, 1, true},
		{"residual-no-errors", "residual", true, 0, false},
		{"empty-defaults-to-residual-no-errors", "", false, 0, false},
		{"empty-defaults-to-residual-with-errors", "", false, 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldCaptureAiRepair(c.mode, c.changed, c.totalErrors); got != c.want {
				t.Errorf("ShouldCaptureAiRepair(%q, %v, %d) = %v, want %v",
					c.mode, c.changed, c.totalErrors, got, c.want)
			}
		})
	}
}
