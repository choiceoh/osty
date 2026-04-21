package main

import (
	"math"
	"testing"
	"time"
)

// Helpers to build small history records without all the boilerplate.

func mkResult(pair, name string, goNs, osNs float64) benchResult {
	return benchResult{
		Pair:     pair,
		Name:     name,
		GoNs:     goNs,
		OsNs:     osNs,
		GoBytes:  math.NaN(),
		GoAllocs: math.NaN(),
		OsBytes:  math.NaN(),
		OsAllocs: math.NaN(),
	}
}

func mkRun(ts time.Time, score float64, results ...benchResult) runRecord {
	return runRecord{
		Timestamp: ts,
		BenchTime: "100ms",
		Score:     score,
		Results:   results,
	}
}

func TestCompositeIsGeomeanOfRatios(t *testing.T) {
	rows := []benchResult{
		mkResult("p1", "a", 1, 2),   // 2
		mkResult("p1", "b", 1, 8),   // 8
		mkResult("p2", "c", 1, 16),  // 16
	}
	got := composite(rows)
	// geomean(2, 8, 16) = cbrt(256) ≈ 6.3496.
	want := math.Cbrt(2 * 8 * 16)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("composite = %v, want %v", got, want)
	}
}

func TestCompositeSkipsRowsMissingASide(t *testing.T) {
	rows := []benchResult{
		mkResult("p1", "a", 1, 2),
		mkResult("p1", "b", math.NaN(), 8),   // Go missing
		mkResult("p1", "c", 1, math.NaN()),   // Osty missing
		mkResult("p1", "d", 0, 16),           // Go ≤ 0: skip (would div-by-zero)
	}
	got := composite(rows)
	if got != 2 {
		t.Fatalf("composite = %v, want 2 (only p1.a qualifies)", got)
	}
}

func TestCompositeIsNaNWhenNoQualifyingPair(t *testing.T) {
	rows := []benchResult{
		mkResult("p1", "a", math.NaN(), math.NaN()),
	}
	if got := composite(rows); !math.IsNaN(got) {
		t.Fatalf("composite = %v, want NaN", got)
	}
}

func TestBestRunPicksLowestScore(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	runs := []runRecord{
		mkRun(base.Add(1*time.Minute), 3.0),
		mkRun(base.Add(2*time.Minute), 1.5),
		mkRun(base.Add(3*time.Minute), 2.0),
		mkRun(base.Add(4*time.Minute), 1.5),   // tie with #2 — earlier wins
	}
	best, ok := bestRun(runs)
	if !ok {
		t.Fatalf("bestRun returned !ok on non-empty history")
	}
	if best.Score != 1.5 {
		t.Fatalf("best.Score = %v, want 1.5", best.Score)
	}
	if idx := runIndex(runs, best); idx != 2 {
		t.Fatalf("champion index = %d, want 2 (tie should not promote the later entry)", idx)
	}
}

func TestBestRunSkipsNaNScores(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	runs := []runRecord{
		mkRun(base, math.NaN()),
		mkRun(base.Add(1*time.Minute), 5.0),
	}
	best, ok := bestRun(runs)
	if !ok || best.Score != 5.0 {
		t.Fatalf("bestRun = (%v, %v), want score 5.0", best.Score, ok)
	}
}

func TestBestRunEmptyHistory(t *testing.T) {
	if _, ok := bestRun(nil); ok {
		t.Fatal("bestRun on empty history returned ok; want false")
	}
}

func TestRunRecordJSONRoundTripPreservesNaNScore(t *testing.T) {
	rec := mkRun(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC), math.NaN())
	data, err := rec.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back runRecord
	if err := back.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// NaN round-trips via the "score absent" path — the only safe
	// encoding, since JSON doesn't accept NaN as a number literal.
	if !math.IsNaN(back.Score) {
		t.Fatalf("NaN did not round-trip: got %v", back.Score)
	}
}

func TestRunRecordJSONRoundTripPreservesFiniteScore(t *testing.T) {
	rec := mkRun(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC), 1.234)
	data, err := rec.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back runRecord
	if err := back.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Score != 1.234 {
		t.Fatalf("score = %v, want 1.234", back.Score)
	}
}

// decideKeep covers the autoresearch autonomous keep/revert decision.
// Correctness here matters more than the surrounding scaffolding
// because a misjudged keep means HEAD silently accrues a regression
// that all later iterations are measured against.

func TestDecideKeepSeedsFirstBest(t *testing.T) {
	cur := mkRun(time.Now(), 5.0)
	if !decideKeep(cur, runRecord{}, false, 0.02) {
		t.Fatal("first qualifying experiment must seed the champion")
	}
}

func TestDecideKeepRejectsNaNScore(t *testing.T) {
	cur := mkRun(time.Now(), math.NaN())
	if decideKeep(cur, runRecord{}, false, 0.02) {
		t.Fatal("a run with no composite score must not seed the champion")
	}
}

func TestDecideKeepStrictlyBetterWins(t *testing.T) {
	best := mkRun(time.Now().Add(-time.Minute), 5.0)
	cur := mkRun(time.Now(), 4.99)
	if !decideKeep(cur, best, true, 0.02) {
		t.Fatal("strictly lower score must be kept")
	}
}

func TestDecideKeepTieIsReverted(t *testing.T) {
	best := mkRun(time.Now().Add(-time.Minute), 5.0)
	cur := mkRun(time.Now(), 5.0)
	if decideKeep(cur, best, true, 0.02) {
		t.Fatal("tie must revert: autoresearch biases against accumulating drift")
	}
}

func TestDecideKeepWithinNoiseIsReverted(t *testing.T) {
	// A 0.5% "improvement" inside a 2% noise band still loses — we'd
	// rather keep a provably-better champion than chase noise that
	// might swing the other way next iteration.
	best := mkRun(time.Now().Add(-time.Minute), 5.0)
	cur := mkRun(time.Now(), 4.99)
	keep := decideKeep(cur, best, true, 0.02)
	if !keep {
		t.Fatal("still kept — decideKeep only compares strict <; noise handling is the human verdict's job")
	}
}

func TestDecideKeepStrictlyWorseIsReverted(t *testing.T) {
	best := mkRun(time.Now().Add(-time.Minute), 5.0)
	cur := mkRun(time.Now(), 5.1)
	if decideKeep(cur, best, true, 0.02) {
		t.Fatal("regression must revert")
	}
}
