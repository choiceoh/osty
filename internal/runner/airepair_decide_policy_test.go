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
