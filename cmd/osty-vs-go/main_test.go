package main

import (
	"math"
	"os"
	"path/filepath"
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
		mkResult("p1", "a", 1, 2),  // 2
		mkResult("p1", "b", 1, 8),  // 8
		mkResult("p2", "c", 1, 16), // 16
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
		mkResult("p1", "b", math.NaN(), 8), // Go missing
		mkResult("p1", "c", 1, math.NaN()), // Osty missing
		mkResult("p1", "d", 0, 16),         // Go ≤ 0: skip (would div-by-zero)
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

func TestMedianFiniteOddCount(t *testing.T) {
	got := medianFinite([]float64{9, 1, 5})
	if got != 5 {
		t.Fatalf("medianFinite odd = %v, want 5", got)
	}
}

func TestMedianFiniteEvenCount(t *testing.T) {
	got := medianFinite([]float64{10, 4, 2, 8})
	if got != 6 {
		t.Fatalf("medianFinite even = %v, want 6", got)
	}
}

func TestMedianFiniteEmpty(t *testing.T) {
	if got := medianFinite(nil); !math.IsNaN(got) {
		t.Fatalf("medianFinite empty = %v, want NaN", got)
	}
}

func TestAggregateGoBenchRunsUsesMedianPerMetric(t *testing.T) {
	runs := [][]benchResult{
		{
			{Pair: "word_freq", Name: "WordFreqTop10", GoNs: 120, GoBytes: 90, GoAllocs: 4, OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
			{Pair: "word_freq", Name: "Helper", GoNs: 40, GoBytes: math.NaN(), GoAllocs: 1, OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
		},
		{
			{Pair: "word_freq", Name: "WordFreqTop10", GoNs: 100, GoBytes: 70, GoAllocs: 2, OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
			{Pair: "word_freq", Name: "Helper", GoNs: 30, GoBytes: math.NaN(), GoAllocs: 3, OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
		},
		{
			{Pair: "word_freq", Name: "WordFreqTop10", GoNs: 140, GoBytes: 110, GoAllocs: 6, OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
			{Pair: "word_freq", Name: "Helper", GoNs: 50, GoBytes: math.NaN(), GoAllocs: 5, OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
		},
	}
	rows := aggregateGoBenchRuns("word_freq", runs)
	if len(rows) != 2 {
		t.Fatalf("aggregateGoBenchRuns len = %d, want 2", len(rows))
	}
	if rows[0].Name != "Helper" || rows[1].Name != "WordFreqTop10" {
		t.Fatalf("aggregateGoBenchRuns order = %+v", rows)
	}
	if rows[0].GoNs != 40 || !math.IsNaN(rows[0].GoBytes) || rows[0].GoAllocs != 3 {
		t.Fatalf("Helper median row = %+v", rows[0])
	}
	if rows[1].GoNs != 120 || rows[1].GoBytes != 90 || rows[1].GoAllocs != 4 {
		t.Fatalf("WordFreqTop10 median row = %+v", rows[1])
	}
}

func TestRunGoBenchCachedReusesGoBaselineWhenInputsStayStable(t *testing.T) {
	root := t.TempDir()
	pairsDir := filepath.Join(root, "benchmarks")
	pairDir := filepath.Join(pairsDir, "simd_stats", "go")
	if err := os.MkdirAll(pairDir, 0o755); err != nil {
		t.Fatalf("mkdir pair dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/bench\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pairDir, "simd_stats_test.go"), []byte("package simdstatsbench\n"), 0o644); err != nil {
		t.Fatalf("write pair file: %v", err)
	}

	cache := newGoBenchCache()
	calls := 0
	runner := func(goBin, pairsDir, pair, benchTime string, goCount int, goCPU string) ([]benchResult, error) {
		calls++
		return []benchResult{mkResult(pair, "SimdStats", float64(calls), math.NaN())}, nil
	}

	first, err := runGoBenchCached(cache, runner, "go", pairsDir, "simd_stats", "2s", 3, "1")
	if err != nil {
		t.Fatalf("first cached run: %v", err)
	}
	second, err := runGoBenchCached(cache, runner, "go", pairsDir, "simd_stats", "2s", 3, "1")
	if err != nil {
		t.Fatalf("second cached run: %v", err)
	}

	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1 (cache hit on second run)", calls)
	}
	if len(second) != 1 || second[0].GoNs != first[0].GoNs {
		t.Fatalf("cached rows = %+v, want %+v", second, first)
	}
}

func TestRunGoBenchCachedInvalidatesWhenGoInputsChange(t *testing.T) {
	root := t.TempDir()
	pairsDir := filepath.Join(root, "benchmarks")
	pairDir := filepath.Join(pairsDir, "record_pipeline", "go")
	if err := os.MkdirAll(pairDir, 0o755); err != nil {
		t.Fatalf("mkdir pair dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/bench\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	goFile := filepath.Join(pairDir, "record_pipeline_test.go")
	if err := os.WriteFile(goFile, []byte("package recordpipelinebench\n"), 0o644); err != nil {
		t.Fatalf("write pair file: %v", err)
	}

	cache := newGoBenchCache()
	calls := 0
	runner := func(goBin, pairsDir, pair, benchTime string, goCount int, goCPU string) ([]benchResult, error) {
		calls++
		return []benchResult{mkResult(pair, "RecordPipeline", float64(calls), math.NaN())}, nil
	}

	first, err := runGoBenchCached(cache, runner, "go", pairsDir, "record_pipeline", "2s", 3, "1")
	if err != nil {
		t.Fatalf("first cached run: %v", err)
	}
	if err := os.WriteFile(goFile, []byte("package recordpipelinebench\n// change\n"), 0o644); err != nil {
		t.Fatalf("rewrite pair file: %v", err)
	}
	second, err := runGoBenchCached(cache, runner, "go", pairsDir, "record_pipeline", "2s", 3, "1")
	if err != nil {
		t.Fatalf("second cached run after change: %v", err)
	}

	if calls != 2 {
		t.Fatalf("runner calls = %d, want 2 after input change", calls)
	}
	if len(second) != 1 || len(first) != 1 || second[0].GoNs == first[0].GoNs {
		t.Fatalf("invalidated rows = %+v, first = %+v", second, first)
	}
}

func TestBestRunPicksLowestScore(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	runs := []runRecord{
		mkRun(base.Add(1*time.Minute), 3.0),
		mkRun(base.Add(2*time.Minute), 1.5),
		mkRun(base.Add(3*time.Minute), 2.0),
		mkRun(base.Add(4*time.Minute), 1.5), // tie with #2 — earlier wins
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

// ----- new harness-reliability primitives -----

func TestCoefficientOfVariationBasic(t *testing.T) {
	// stddev(100, 110, 90) = 10 (sample, n-1); mean = 100; CV = 0.10.
	got := coefficientOfVariation([]float64{100, 110, 90})
	if math.Abs(got-0.10) > 1e-9 {
		t.Fatalf("coefficientOfVariation = %v, want 0.10", got)
	}
}

func TestCoefficientOfVariationNaNBelowTwoSamples(t *testing.T) {
	if got := coefficientOfVariation([]float64{42}); !math.IsNaN(got) {
		t.Fatalf("single sample CV = %v, want NaN", got)
	}
	if got := coefficientOfVariation(nil); !math.IsNaN(got) {
		t.Fatalf("empty CV = %v, want NaN", got)
	}
}

func TestCoefficientOfVariationNaNOnZeroMean(t *testing.T) {
	// Zeroed bench body — CV ratio is undefined; don't pretend otherwise.
	if got := coefficientOfVariation([]float64{0, 0, 0}); !math.IsNaN(got) {
		t.Fatalf("zero-mean CV = %v, want NaN", got)
	}
}

func TestAggregateOstyBenchRunsMedianAndCV(t *testing.T) {
	runs := [][]benchResult{
		{mkOstyRow("matmul", "Matmul", 1000, 1024)},
		{mkOstyRow("matmul", "Matmul", 1100, 1024)},
		{mkOstyRow("matmul", "Matmul", 900, 1024)},
	}
	rows := aggregateOstyBenchRuns("matmul", runs)
	if len(rows) != 1 {
		t.Fatalf("aggregateOstyBenchRuns len = %d, want 1", len(rows))
	}
	if rows[0].OsNs != 1000 {
		t.Fatalf("median ns = %v, want 1000", rows[0].OsNs)
	}
	if math.Abs(rows[0].OsNsCV-0.10) > 1e-9 {
		t.Fatalf("CV = %v, want 0.10", rows[0].OsNsCV)
	}
	// bytes/op should also median-aggregate.
	if rows[0].OsBytes != 1024 {
		t.Fatalf("median bytes = %v, want 1024", rows[0].OsBytes)
	}
}

func TestAggregateOstyBenchRunsSingleSampleCVIsNaN(t *testing.T) {
	runs := [][]benchResult{{mkOstyRow("p", "B", 500, 0)}}
	rows := aggregateOstyBenchRuns("p", runs)
	if len(rows) != 1 {
		t.Fatalf("aggregateOstyBenchRuns len = %d, want 1", len(rows))
	}
	if !math.IsNaN(rows[0].OsNsCV) {
		t.Fatalf("single-sample CV = %v, want NaN", rows[0].OsNsCV)
	}
	if rows[0].OsNs != 500 {
		t.Fatalf("ns = %v, want 500", rows[0].OsNs)
	}
}

func mkOstyRow(pair, name string, osNs, osBytes float64) benchResult {
	return benchResult{
		Pair:     pair,
		Name:     name,
		GoNs:     math.NaN(),
		GoNsCV:   math.NaN(),
		GoBytes:  math.NaN(),
		GoAllocs: math.NaN(),
		OsNs:     osNs,
		OsNsCV:   math.NaN(),
		OsBytes:  osBytes,
		OsAllocs: math.NaN(),
	}
}

func TestFindBaselineNsPicksFromBaselinePair(t *testing.T) {
	rows := []benchResult{
		mkOstyRow("arith", "Add", 100, 0),
		mkOstyRow(baselinePairName, "Empty", 55, 0),
		mkOstyRow("fib", "Fib15", 9000, 0),
	}
	if got := findBaselineNs(rows); got != 55 {
		t.Fatalf("findBaselineNs = %v, want 55", got)
	}
}

func TestFindBaselineNsReturnsZeroWhenAbsent(t *testing.T) {
	rows := []benchResult{mkOstyRow("arith", "Add", 100, 0)}
	if got := findBaselineNs(rows); got != 0 {
		t.Fatalf("findBaselineNs = %v, want 0", got)
	}
}

func TestEffectiveNoiseFallsBackToUserWhenNoCV(t *testing.T) {
	rec := mkRun(time.Now(), 1.5, mkResult("p", "B", 1, 2))
	band, src := effectiveNoise(0.02, rec)
	if band != 0.02 || src != "--noise" {
		t.Fatalf("effectiveNoise with no CV = (%v, %q), want (0.02, --noise)", band, src)
	}
}

func TestEffectiveNoiseTakesMedianCVWhenWider(t *testing.T) {
	// Two benches with 5% and 7% CV — median is 6%, wider than 2% --noise.
	rows := []benchResult{
		{Pair: "p", Name: "A", OsNsCV: 0.05, GoNs: math.NaN(), GoNsCV: math.NaN(), GoBytes: math.NaN(), GoAllocs: math.NaN(), OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
		{Pair: "p", Name: "B", OsNsCV: 0.07, GoNs: math.NaN(), GoNsCV: math.NaN(), GoBytes: math.NaN(), GoAllocs: math.NaN(), OsNs: math.NaN(), OsBytes: math.NaN(), OsAllocs: math.NaN()},
	}
	rec := mkRun(time.Now(), 1.0, rows...)
	band, src := effectiveNoise(0.02, rec)
	if math.Abs(band-0.06) > 1e-9 || src != "measured CoV" {
		t.Fatalf("effectiveNoise = (%v, %q), want (0.06, measured CoV)", band, src)
	}
}

func TestCaptureMachineInfoHasGOOSAndGOARCH(t *testing.T) {
	info := captureMachineInfo()
	if info.GOOS == "" || info.GOARCH == "" {
		t.Fatalf("captureMachineInfo = %+v, want non-empty GOOS/GOARCH", info)
	}
	if info.NumCPU <= 0 {
		t.Fatalf("captureMachineInfo NumCPU = %d, want >0", info.NumCPU)
	}
}

func TestRunRecordJSONRoundTripPreservesMachineAndBaseline(t *testing.T) {
	rec := runRecord{
		Timestamp:  time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC),
		BenchTime:  "500ms",
		Score:      1.5,
		BaselineNs: 42.5,
		Machine: machineInfo{
			GOOS:     "darwin",
			GOARCH:   "arm64",
			NumCPU:   10,
			CPUModel: "Apple M3 Max",
		},
		Results: []benchResult{mkResult("p", "B", 1, 2)},
	}
	data, err := rec.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back runRecord
	if err := back.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.BaselineNs != 42.5 {
		t.Fatalf("BaselineNs = %v, want 42.5", back.BaselineNs)
	}
	if back.Machine != rec.Machine {
		t.Fatalf("Machine = %+v, want %+v", back.Machine, rec.Machine)
	}
}

func TestBenchResultJSONRoundTripPreservesCV(t *testing.T) {
	in := benchResult{
		Pair: "p", Name: "B",
		GoNs: 100, GoNsCV: 0.03,
		GoBytes: math.NaN(), GoAllocs: math.NaN(),
		OsNs: 200, OsNsCV: 0.08,
		OsBytes: math.NaN(), OsAllocs: math.NaN(),
	}
	data, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back benchResult
	if err := back.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.GoNsCV != 0.03 || back.OsNsCV != 0.08 {
		t.Fatalf("CV round-trip: Go=%v Os=%v, want 0.03 / 0.08", back.GoNsCV, back.OsNsCV)
	}
}
