package runner

import "testing"

func mkStats(parse, resolve, check, totalErr, totalWarn int) ProbeStats {
	return ProbeStats{
		Parse:         ProbeStageStats{Errors: parse},
		Resolve:       ProbeStageStats{Errors: resolve},
		Check:         ProbeStageStats{Errors: check},
		TotalErrors:   totalErr,
		TotalWarnings: totalWarn,
	}
}

func TestCompareParseAssist(t *testing.T) {
	cases := []struct {
		name   string
		before ProbeStats
		after  ProbeStats
		want   int
	}{
		{"improved", mkStats(3, 1, 1, 5, 0), mkStats(1, 1, 1, 3, 0), 1},
		{"regressed", mkStats(1, 1, 1, 3, 0), mkStats(2, 1, 1, 4, 0), -1},
		{"tie-falls-to-total", mkStats(1, 2, 1, 4, 0), mkStats(1, 1, 1, 3, 0), 1},
		{"all-equal", mkStats(1, 1, 1, 3, 0), mkStats(1, 1, 1, 3, 0), 0},
		{"warnings-fallback", mkStats(1, 1, 1, 3, 5), mkStats(1, 1, 1, 3, 3), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CompareParseAssist(c.before, c.after); got != c.want {
				t.Errorf("CompareParseAssist = %d, want %d", got, c.want)
			}
		})
	}
}

func TestCompareAutoAssist(t *testing.T) {
	cases := []struct {
		name   string
		before ProbeStats
		after  ProbeStats
		want   int
	}{
		{"check-stage-valued", mkStats(0, 0, 5, 5, 0), mkStats(0, 0, 2, 2, 0), 1},
		{"resolve-stage-valued", mkStats(0, 3, 0, 3, 0), mkStats(0, 1, 0, 1, 0), 1},
		{"parse-regression-dominates", mkStats(1, 5, 5, 11, 0), mkStats(2, 0, 0, 2, 0), -1},
		{"warning-only-improved", mkStats(1, 1, 1, 3, 5), mkStats(1, 1, 1, 3, 2), 1},
		{"warning-only-regressed", mkStats(1, 1, 1, 3, 2), mkStats(1, 1, 1, 3, 5), -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CompareAutoAssist(c.before, c.after); got != c.want {
				t.Errorf("CompareAutoAssist = %d, want %d", got, c.want)
			}
		})
	}
}

func TestCompareFrontEndAssist(t *testing.T) {
	cases := []struct {
		name   string
		before ProbeStats
		after  ProbeStats
		want   int
	}{
		{"by-total", mkStats(2, 2, 2, 6, 0), mkStats(3, 1, 1, 5, 0), 1},
		{"parse-tiebreaker", mkStats(3, 1, 1, 5, 0), mkStats(1, 3, 1, 5, 0), 1},
		// Parse + totals equal, resolve↔check trade places → neutral
		// because the front-end scorer aggregates the stages.
		{"cross-stage-trade-neutral", mkStats(1, 2, 1, 4, 0), mkStats(1, 1, 2, 4, 0), 0},
		{"warning-only-improved", mkStats(1, 1, 1, 3, 5), mkStats(1, 1, 1, 3, 2), 1},
		{"warning-only-regressed", mkStats(1, 1, 1, 3, 2), mkStats(1, 1, 1, 3, 5), -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CompareFrontEndAssist(c.before, c.after); got != c.want {
				t.Errorf("CompareFrontEndAssist = %d, want %d", got, c.want)
			}
		})
	}
}

func TestRepairedChanged(t *testing.T) {
	if RepairedChanged(0, 0) {
		t.Error("RepairedChanged(0,0) should be false")
	}
	if !RepairedChanged(1, 0) {
		t.Error("RepairedChanged(1,0) should be true")
	}
	if !RepairedChanged(0, 1) {
		t.Error("RepairedChanged(0,1) should be true")
	}
	if !RepairedChanged(5, 3) {
		t.Error("RepairedChanged(5,3) should be true")
	}
}

func TestIsImproved(t *testing.T) {
	s := mkStats(1, 1, 1, 3, 0)
	if !IsImproved("rewrite", s, s, 1) {
		t.Error("rewrite + 1 change should be improved")
	}
	if IsImproved("rewrite", s, s, 0) {
		t.Error("rewrite + 0 changes should not be improved")
	}
	before := mkStats(3, 1, 1, 5, 0)
	after := mkStats(1, 1, 1, 3, 0)
	if !IsImproved("parse", before, after, 1) {
		t.Error("parse mode improvement missed")
	}
	if !IsImproved("frontend", mkStats(2, 2, 2, 6, 0), mkStats(2, 1, 1, 4, 0), 1) {
		t.Error("frontend mode improvement missed")
	}
	// Unknown mode → auto fallback.
	if !IsImproved("unknown", mkStats(0, 0, 5, 5, 0), mkStats(0, 0, 2, 2, 0), 1) {
		t.Error("unknown mode should fall back to auto")
	}
}

func TestIsAccepted(t *testing.T) {
	worse := mkStats(5, 5, 5, 15, 0)
	better := mkStats(1, 1, 1, 3, 0)
	// No edits + no skips → always accepted.
	if !IsAccepted("auto", better, worse, 0, 0) {
		t.Error("no-edits should short-circuit to accepted")
	}
	// Rewrite mode → always accepted when edits exist.
	if !IsAccepted("rewrite", better, worse, 1, 0) {
		t.Error("rewrite mode should always accept")
	}
	// Non-regressing tie → accepted.
	s := mkStats(1, 1, 1, 3, 0)
	for _, mode := range []string{"auto", "parse", "frontend"} {
		if !IsAccepted(mode, s, s, 1, 0) {
			t.Errorf("%s mode should accept non-regressing tie", mode)
		}
	}
	// Regression → rejected.
	if IsAccepted("auto", better, worse, 1, 0) {
		t.Error("auto mode should reject regression with edits")
	}
}

func TestExplainAcceptedReason(t *testing.T) {
	s := mkStats(0, 0, 0, 0, 0)
	if got := ExplainAcceptedReason("auto", s, s, 0, false); got != "already_clean" {
		t.Errorf("not-proposed: got %q", got)
	}
	if got := ExplainAcceptedReason("auto", s, s, 0, true); got != "already_clean" {
		t.Errorf("no-changes: got %q", got)
	}
	cases := []struct {
		name       string
		mode       string
		before     ProbeStats
		after      ProbeStats
		changes    int
		wantReason string
	}{
		{"parse drop", "auto", mkStats(3, 1, 1, 5, 0), mkStats(1, 1, 1, 3, 0), 2, "parse_errors_reduced"},
		{"resolve drop", "auto", mkStats(0, 3, 0, 3, 0), mkStats(0, 1, 0, 1, 0), 1, "resolve_errors_reduced"},
		{"check drop", "auto", mkStats(0, 0, 3, 3, 0), mkStats(0, 0, 1, 1, 0), 1, "check_errors_reduced"},
		{"warnings reduced", "auto", mkStats(1, 1, 1, 3, 5), mkStats(1, 1, 1, 3, 2), 1, "warnings_reduced"},
		{"rewrite mode applied", "rewrite", mkStats(1, 1, 1, 3, 0), mkStats(1, 1, 1, 3, 0), 1, "rewrite_mode_applied"},
		{"non-regressing rewrite", "auto", mkStats(1, 1, 1, 3, 0), mkStats(1, 1, 1, 3, 0), 1, "non_regressing_rewrite_accepted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExplainAcceptedReason(c.mode, c.before, c.after, c.changes, true)
			if got != c.wantReason {
				t.Errorf("got %q, want %q", got, c.wantReason)
			}
		})
	}
}

func TestExplainRejectedReason(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		before     ProbeStats
		after      ProbeStats
		wantReason string
	}{
		{"parse regression", "auto", mkStats(1, 1, 1, 3, 0), mkStats(2, 1, 1, 4, 0), "parse_regression_blocked"},
		{"resolve-only-auto", "auto", mkStats(1, 1, 1, 3, 0), mkStats(1, 3, 1, 5, 0), "resolve_regression_blocked"},
		// In parse mode, the resolve-only branch collapses to the
		// total-error check.
		{"resolve-only-parse-falls-to-total", "parse", mkStats(1, 1, 1, 3, 0), mkStats(1, 3, 1, 5, 0), "front_end_regression_blocked"},
		{"check-only-auto", "auto", mkStats(1, 1, 1, 3, 0), mkStats(1, 1, 3, 5, 0), "check_regression_blocked"},
		{"front-end aggregate", "frontend", mkStats(1, 1, 1, 3, 0), mkStats(1, 2, 2, 5, 0), "front_end_regression_blocked"},
		{"warning regression", "auto", mkStats(1, 1, 1, 3, 2), mkStats(1, 1, 1, 3, 5), "warning_regression_blocked"},
		{"no improvement fallback", "auto", mkStats(1, 1, 1, 3, 0), mkStats(1, 1, 1, 3, 0), "no_improvement"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExplainRejectedReason(c.mode, c.before, c.after)
			if got != c.wantReason {
				t.Errorf("got %q, want %q", got, c.wantReason)
			}
		})
	}
}
