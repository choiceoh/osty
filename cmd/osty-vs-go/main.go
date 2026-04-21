// osty-vs-go runs matched Go and Osty benchmark pairs side by side and
// prints a comparison table. A "pair" is a directory under
// benchmarks/osty-vs-go/<name>/ with a `go/` subdir (standard
// `go test -bench` layout) and an `osty/` subdir (standard
// `osty test --bench` layout). Benches pair by name as
// `BenchmarkFoo` ↔ `benchFoo`.
//
// The Osty side is the language under test — the LLVM-compiled
// runtime. The Go bits of the Osty toolchain are out of scope here.
//
// Collected metrics per bench:
//
//   - ns/op (both sides)
//   - bytes/op  — Go via `-benchmem`, Osty via the GC's
//     allocated_bytes_total odometer.
//   - allocs/op — Go only; Osty's GC exposes bytes but not a cheap
//     allocation count, so the column says "—" on the Osty side.
//
// Each run is appended to .osty-vs-go-history.jsonl (one JSON object
// per run). `--history <N>` prints the last N runs as an ASCII trend
// graph per bench.
//
// Autoresearch (inspired by karpathy/autoresearch): every run also
// captures the current git HEAD and a single composite score —
// `geomean(ns_osty / ns_go)` across all benches with both sides. The
// tool compares each new run against the best score seen in history
// and prints one of {new best, regression, within-noise, neutral}, so
// the edit-build-bench loop has a one-line keep-or-revert verdict
// without the user grepping through per-bench deltas. `--research`
// walks the history and shows the current champion, the latest run,
// and the deltas that drove the change. `--loop <interval>` blocks
// until Ctrl-C, re-running on every interval so a human (or an
// outer-loop agent) can iterate on the compiler with continuous
// feedback.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type benchResult struct {
	Pair         string
	Name         string
	GoNs         float64 // ns/op; NaN when absent
	GoBytes      float64
	GoAllocs     float64
	OsNs         float64
	OsBytes      float64
	OsAllocs     float64 // always NaN today — kept for a future Osty accessor
}

type runRecord struct {
	Timestamp time.Time     `json:"timestamp"`
	BenchTime string        `json:"bench_time"`
	PairsDir  string        `json:"pairs_dir"`
	Label     string        `json:"label,omitempty"`
	GitHead   string        `json:"git_head,omitempty"`
	// Score is the geomean of osty_ns/go_ns across benches with both
	// sides present. Lower = Osty closer to Go. NaN when no pair
	// contributes. Persisted so the research/loop paths don't have to
	// re-derive it from results (and so backfilling a new metric later
	// doesn't silently mutate the published score of old runs).
	Score   float64       `json:"score,omitempty"`
	Results []benchResult `json:"results"`
}

// MarshalJSON writes NaN Score as omitted ("no verdict" for this run)
// rather than producing the invalid JSON token `NaN`. Same trick as
// benchResult.MarshalJSON.
func (r runRecord) MarshalJSON() ([]byte, error) {
	type alias runRecord
	out := struct {
		Score *float64 `json:"score,omitempty"`
		alias
	}{alias: alias(r)}
	out.alias.Score = 0
	if !math.IsNaN(r.Score) {
		v := r.Score
		out.Score = &v
	}
	return json.Marshal(out)
}

func (r *runRecord) UnmarshalJSON(data []byte) error {
	type alias runRecord
	var in struct {
		Score *float64 `json:"score,omitempty"`
		alias
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*r = runRecord(in.alias)
	if in.Score == nil {
		r.Score = math.NaN()
	} else {
		r.Score = *in.Score
	}
	return nil
}

// composite returns the primary autoresearch metric: geomean of
// osty/go ns ratios across all benches that have both sides. Lower is
// better. NaN when no qualifying pair is present.
func composite(rows []benchResult) float64 {
	var logSum float64
	var n int
	for _, r := range rows {
		if math.IsNaN(r.GoNs) || math.IsNaN(r.OsNs) || r.GoNs <= 0 {
			continue
		}
		logSum += math.Log(r.OsNs / r.GoNs)
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Exp(logSum / float64(n))
}

// gitHead returns the short commit hash the working tree is currently
// at, or "" when we're not inside a git checkout. A "-dirty" suffix
// marks uncommitted changes so a research run against an edited tree
// is labeled for what it is.
func gitHead() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(out))
	// `git diff --quiet` exits nonzero when there are unstaged edits.
	if err := exec.Command("git", "diff", "--quiet").Run(); err != nil {
		head += "-dirty"
	}
	return head
}

// MarshalJSON maps NaN metrics to null so the history file is valid
// JSON. encoding/json rejects NaN with "unsupported value" otherwise,
// which would silently drop the run record and the sequence counter
// would stay at 0.
func (r benchResult) MarshalJSON() ([]byte, error) {
	m := map[string]interface{}{
		"pair": r.Pair,
		"name": r.Name,
	}
	for k, v := range map[string]float64{
		"go_ns":     r.GoNs,
		"go_bytes":  r.GoBytes,
		"go_allocs": r.GoAllocs,
		"osty_ns":   r.OsNs,
		"osty_bytes": r.OsBytes,
		"osty_allocs": r.OsAllocs,
	} {
		if !math.IsNaN(v) {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

func (r *benchResult) UnmarshalJSON(data []byte) error {
	var m struct {
		Pair       string   `json:"pair"`
		Name       string   `json:"name"`
		GoNs       *float64 `json:"go_ns"`
		GoBytes    *float64 `json:"go_bytes"`
		GoAllocs   *float64 `json:"go_allocs"`
		OsNs       *float64 `json:"osty_ns"`
		OsBytes    *float64 `json:"osty_bytes"`
		OsAllocs   *float64 `json:"osty_allocs"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	r.Pair = m.Pair
	r.Name = m.Name
	r.GoNs = ptrOrNaN(m.GoNs)
	r.GoBytes = ptrOrNaN(m.GoBytes)
	r.GoAllocs = ptrOrNaN(m.GoAllocs)
	r.OsNs = ptrOrNaN(m.OsNs)
	r.OsBytes = ptrOrNaN(m.OsBytes)
	r.OsAllocs = ptrOrNaN(m.OsAllocs)
	return nil
}

func ptrOrNaN(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

const historyFile = ".osty-vs-go-history.jsonl"

func main() {
	fs := flag.NewFlagSet("osty-vs-go", flag.ExitOnError)
	benchTime := fs.String("benchtime", "500ms", "per-bench duration target for both Go (-benchtime) and Osty (--benchtime)")
	pairsDir := fs.String("pairs-dir", "benchmarks/osty-vs-go", "root containing <pair>/go/ and <pair>/osty/ subdirs")
	ostyBin := fs.String("osty", "", "path to the osty binary (default: $PWD/.bin/osty if present, else `osty` from PATH)")
	goBin := fs.String("go", "go", "path to the go binary used for `go test -bench`")
	filter := fs.String("filter", "", "optional regex over pair names; only matching pairs run")
	historyN := fs.Int("history", 0, "if >0, skip running and render the last N runs from "+historyFile+" as an ASCII trend graph")
	label := fs.String("label", "", "optional short label stored with this run (shown in history output)")
	research := fs.Bool("research", false, "skip running and summarize history as an autoresearch journal (champion, latest, per-bench deltas)")
	loopInterval := fs.Duration("loop", 0, "if >0, keep running in a loop with this interval between runs — compiles/edits land between ticks and each run prints a vs-best verdict")
	noiseFrac := fs.Float64("noise", 0.02, "delta fraction below which a vs-best verdict is reported as within-noise (default 2%)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if *historyN > 0 {
		if err := printHistory(*historyN); err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *research {
		if err := printResearchJournal(*noiseFrac); err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: %v\n", err)
			os.Exit(1)
		}
		return
	}

	osty := resolveOstyBin(*ostyBin)
	if osty == "" {
		fmt.Fprintln(os.Stderr, "osty-vs-go: could not locate an osty binary. Pass --osty <path> or run `just build` first.")
		os.Exit(2)
	}

	var pairRE *regexp.Regexp
	if *filter != "" {
		re, err := regexp.Compile(*filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: invalid --filter: %v\n", err)
			os.Exit(2)
		}
		pairRE = re
	}

	if *loopInterval > 0 {
		runLoop(*loopInterval, *benchTime, *pairsDir, osty, *goBin, *label, *noiseFrac, pairRE)
		return
	}

	if _, err := runOnce(*benchTime, *pairsDir, osty, *goBin, *label, *noiseFrac, pairRE); err != nil {
		fmt.Fprintf(os.Stderr, "osty-vs-go: %v\n", err)
		os.Exit(1)
	}
}

// runOnce executes one full bench sweep: discover pairs, run each
// side, print the table, append to history, print the vs-best verdict.
// Returns the new record so `--loop` can stream verdicts without
// re-reading history.
func runOnce(benchTime, pairsDir, ostyBin, goBin, label string, noiseFrac float64, pairRE *regexp.Regexp) (runRecord, error) {
	pairs, err := discoverPairs(pairsDir)
	if err != nil {
		return runRecord{}, err
	}
	if pairRE != nil {
		filtered := pairs[:0]
		for _, p := range pairs {
			if pairRE.MatchString(p) {
				filtered = append(filtered, p)
			}
		}
		pairs = filtered
	}
	if len(pairs) == 0 {
		return runRecord{}, fmt.Errorf("no pairs to run")
	}

	seq, err := nextRunSequence()
	if err != nil {
		return runRecord{}, err
	}
	head := gitHead()
	labelPart := ""
	if label != "" {
		labelPart = " (" + label + ")"
	}
	headPart := ""
	if head != "" {
		headPart = " @ " + head
	}
	fmt.Printf("# osty-vs-go run #%d%s%s — benchtime=%s, pairs=%d\n\n", seq, labelPart, headPart, benchTime, len(pairs))

	var rows []benchResult
	for _, pair := range pairs {
		goRows, err := runGoBench(goBin, pairsDir, pair, benchTime)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: go bench %s: %v\n", pair, err)
		}
		osRows, err := runOstyBench(ostyBin, pairsDir, pair, benchTime)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: osty bench %s: %v\n", pair, err)
		}
		rows = append(rows, mergePairRows(pair, goRows, osRows)...)
	}

	printTable(rows)

	record := runRecord{
		Timestamp: time.Now(),
		BenchTime: benchTime,
		PairsDir:  pairsDir,
		Label:     label,
		GitHead:   head,
		Score:     composite(rows),
		Results:   rows,
	}

	// Compare against the best score in history BEFORE appending — we
	// want the verdict to reflect the champion that existed when this
	// run started, not a champion that includes this run.
	prior, _ := readHistory()
	printVsBestVerdict(record, prior, noiseFrac)

	if err := appendHistory(record); err != nil {
		fmt.Fprintf(os.Stderr, "osty-vs-go: history write: %v\n", err)
	}
	return record, nil
}

// runLoop re-runs the full sweep on a fixed cadence until interrupted.
// Karpathy-style: one metric, one loop, keep the best. Between ticks
// the user (or an outer agent) edits Osty sources; each tick's verdict
// tells them whether the edit was an improvement, a regression, or
// noise. Ctrl-C exits cleanly.
func runLoop(interval time.Duration, benchTime, pairsDir, ostyBin, goBin, label string, noiseFrac float64, pairRE *regexp.Regexp) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Run immediately rather than waiting `interval` for the first
	// tick — the user just launched it and expects feedback.
	for {
		if _, err := runOnce(benchTime, pairsDir, ostyBin, goBin, label, noiseFrac, pairRE); err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: %v\n", err)
		}
		fmt.Printf("\n(waiting %s before next sweep; Ctrl-C to stop)\n\n", interval)
		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "osty-vs-go: loop interrupted, exiting")
			return
		case <-ticker.C:
		}
	}
}

// resolveOstyBin picks the Osty binary: explicit flag beats the local
// `.bin/osty` produced by `just build`, which beats `osty` on PATH.
func resolveOstyBin(flagVal string) string {
	if flagVal != "" {
		if _, err := os.Stat(flagVal); err == nil {
			return flagVal
		}
		return ""
	}
	if _, err := os.Stat(".bin/osty"); err == nil {
		abs, err := filepath.Abs(".bin/osty")
		if err == nil {
			return abs
		}
	}
	if path, err := exec.LookPath("osty"); err == nil {
		return path
	}
	return ""
}

func discoverPairs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read pairs dir %s: %w", root, err)
	}
	var pairs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		goSub := filepath.Join(root, e.Name(), "go")
		osSub := filepath.Join(root, e.Name(), "osty")
		goInfo, goErr := os.Stat(goSub)
		osInfo, osErr := os.Stat(osSub)
		if goErr != nil || osErr != nil || !goInfo.IsDir() || !osInfo.IsDir() {
			continue
		}
		pairs = append(pairs, e.Name())
	}
	sort.Strings(pairs)
	return pairs, nil
}

// runGoBench invokes `go test -run=^$ -bench=. -benchmem -benchtime=<t>`
// on the pair's go/ subdir. -run=^$ filters out regular tests; -benchmem
// opts into the B/op + allocs/op columns that `testing` only reports
// on request.
func runGoBench(goBin, pairsDir, pair, benchTime string) ([]benchResult, error) {
	pkg := goPackageArg(filepath.Join(pairsDir, pair, "go"))
	cmd := exec.Command(goBin, "test", "-run=^$", "-bench=.", "-benchmem", "-benchtime="+benchTime, pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s test: %w\n%s", goBin, err, out)
	}
	return parseGoBenchOutput(pair, string(out)), nil
}

// goPackageArg formats a filesystem path for the `go test` argv. Relative
// paths get the `./` prefix that the Go toolchain requires to treat
// them as package paths rather than import paths; absolute paths pass
// through untouched because `./` + `/abs/…` collapses to a nonsense
// relative path and `go test` fails to resolve it.
func goPackageArg(path string) string {
	slash := filepath.ToSlash(path)
	if filepath.IsAbs(path) {
		return slash
	}
	return "./" + slash
}

// Go bench lines with -benchmem look like:
//
//	BenchmarkAdd-10   1_000_000   1.24 ns/op   0 B/op   0 allocs/op
//
// Columns after ns/op are optional (only present with -benchmem) so
// the pattern captures them but lets a bare ns/op line through too.
var goBenchLineRE = regexp.MustCompile(
	`^Benchmark([A-Za-z0-9_]+)(?:-\d+)?\s+\d+\s+([\d.]+)\s+ns/op` +
		`(?:\s+([\d.]+)\s+B/op)?(?:\s+([\d.]+)\s+allocs/op)?`,
)

func parseGoBenchOutput(pair, out string) []benchResult {
	var rows []benchResult
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		m := goBenchLineRE.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		ns, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		row := benchResult{
			Pair:     pair,
			Name:     m[1],
			GoNs:     ns,
			GoBytes:  math.NaN(),
			GoAllocs: math.NaN(),
			OsNs:     math.NaN(),
			OsBytes:  math.NaN(),
			OsAllocs: math.NaN(),
		}
		if m[3] != "" {
			if v, err := strconv.ParseFloat(m[3], 64); err == nil {
				row.GoBytes = v
			}
		}
		if m[4] != "" {
			if v, err := strconv.ParseFloat(m[4], 64); err == nil {
				row.GoAllocs = v
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func runOstyBench(ostyBin, pairsDir, pair, benchTime string) ([]benchResult, error) {
	dir := filepath.Join(pairsDir, pair, "osty")
	cmd := exec.Command(ostyBin, "test", "--bench", "--serial", "--benchtime="+benchTime, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s test --bench: %w\n%s", ostyBin, err, out)
	}
	return parseOstyBenchOutput(pair, string(out)), nil
}

// Osty output, line-oriented:
//
//	ok\tbenchFoo\t<wall>
//	bench <path>:<line> iter=N total=Tns avg=Ans bytes/op=B
//	  min=…ns p50=…ns p99=…ns max=…ns
//
// We key each summary to the most recent `ok\tbench*` name — --serial
// keeps that ordering stable.
var (
	ostyOkLineRE   = regexp.MustCompile(`^ok\t(bench[A-Za-z0-9_]+)\t`)
	ostyBenchSumRE = regexp.MustCompile(`^bench\s+\S+\s+iter=\d+\s+total=\d+ns\s+avg=(\d+)ns(?:\s+bytes/op=(\d+))?`)
)

func parseOstyBenchOutput(pair, out string) []benchResult {
	var rows []benchResult
	scanner := bufio.NewScanner(strings.NewReader(out))
	currentName := ""
	for scanner.Scan() {
		line := scanner.Text()
		if m := ostyOkLineRE.FindStringSubmatch(line); m != nil {
			currentName = m[1]
			continue
		}
		if m := ostyBenchSumRE.FindStringSubmatch(line); m != nil && currentName != "" {
			avg, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				continue
			}
			bytes := math.NaN()
			if m[2] != "" {
				if v, err := strconv.ParseFloat(m[2], 64); err == nil {
					bytes = v
				}
			}
			name := strings.TrimPrefix(currentName, "bench")
			rows = append(rows, benchResult{
				Pair:     pair,
				Name:     name,
				GoNs:     math.NaN(),
				GoBytes:  math.NaN(),
				GoAllocs: math.NaN(),
				OsNs:     avg,
				OsBytes:  bytes,
				OsAllocs: math.NaN(),
			})
			currentName = ""
		}
	}
	return rows
}

func mergePairRows(pair string, goRows, osRows []benchResult) []benchResult {
	byName := map[string]benchResult{}
	for _, r := range goRows {
		byName[r.Name] = r
	}
	for _, r := range osRows {
		if existing, ok := byName[r.Name]; ok {
			existing.OsNs = r.OsNs
			existing.OsBytes = r.OsBytes
			existing.OsAllocs = r.OsAllocs
			byName[r.Name] = existing
			continue
		}
		byName[r.Name] = r
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]benchResult, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out
}

func printTable(rows []benchResult) {
	header := []string{
		"pair", "bench",
		"go ns/op", "osty ns/op", "ns osty/go",
		"go B/op", "osty B/op",
		"go allocs/op",
	}
	lines := make([][]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, []string{
			r.Pair, r.Name,
			formatFloat(r.GoNs, 2),
			formatFloat(r.OsNs, 1),
			formatRatio(r.OsNs, r.GoNs),
			formatFloat(r.GoBytes, 0),
			formatFloat(r.OsBytes, 0),
			formatFloat(r.GoAllocs, 0),
		})
	}
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len(h)
	}
	for _, row := range lines {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	printRow := func(cells []string) {
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = fmt.Sprintf("%-*s", widths[i], c)
		}
		fmt.Println(strings.Join(parts, "  "))
	}
	printRow(header)
	sep := make([]string, len(header))
	for i := range sep {
		sep[i] = strings.Repeat("-", widths[i])
	}
	printRow(sep)
	for _, row := range lines {
		printRow(row)
	}

	var logSum float64
	var n int
	for _, r := range rows {
		if math.IsNaN(r.GoNs) || math.IsNaN(r.OsNs) || r.GoNs <= 0 {
			continue
		}
		logSum += math.Log(r.OsNs / r.GoNs)
		n++
	}
	if n > 0 {
		fmt.Printf("\ngeomean ns osty/go: %.2fx  (over %d benches)\n", math.Exp(logSum/float64(n)), n)
	}
}

func formatFloat(v float64, decimals int) string {
	if math.IsNaN(v) {
		return "—"
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

func formatRatio(osNs, goNs float64) string {
	if math.IsNaN(osNs) || math.IsNaN(goNs) || goNs <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.2fx", osNs/goNs)
}

// bestRun picks the history entry with the lowest composite score.
// Ties break toward the earlier run so a regression doesn't quietly
// replace a champion of equal score.
func bestRun(runs []runRecord) (runRecord, bool) {
	best := runRecord{}
	found := false
	for _, r := range runs {
		if math.IsNaN(r.Score) {
			continue
		}
		if !found || r.Score < best.Score {
			best = r
			found = true
		}
	}
	return best, found
}

// printVsBestVerdict compares a just-completed run against the best
// score in prior history and prints one of:
//
//	-> new best: score 1.234 (prev champion 1.456 at run #3)
//	-> regression: score 1.500 vs best 1.234 at run #3 (+21.6%)
//	-> within noise: score 1.239 vs best 1.234 at run #3 (+0.41%)
//	-> first run: score 1.234 (no prior data)
//
// noiseFrac is the |delta/best| band under which we call it noise
// instead of promoting / regressing. This is the one-line "keep or
// revert" signal the edit-build-bench loop actually needs.
func printVsBestVerdict(current runRecord, prior []runRecord, noiseFrac float64) {
	if math.IsNaN(current.Score) {
		fmt.Println("\n-> no verdict: this run had no paired benches (both sides present)")
		return
	}
	best, ok := bestRun(prior)
	if !ok {
		fmt.Printf("\n-> first run: score %.3f (no prior data)\n", current.Score)
		return
	}
	delta := (current.Score - best.Score) / best.Score
	bestIdx := runIndex(prior, best)
	switch {
	case current.Score < best.Score:
		fmt.Printf("\n-> NEW BEST: score %.3f  (prev champion %.3f at run #%d; -%.2f%%)\n",
			current.Score, best.Score, bestIdx, -delta*100)
	case math.Abs(delta) <= noiseFrac:
		fmt.Printf("\n-> within noise: score %.3f vs best %.3f at run #%d (%+.2f%%)\n",
			current.Score, best.Score, bestIdx, delta*100)
	default:
		fmt.Printf("\n-> regression: score %.3f vs best %.3f at run #%d (%+.2f%%)\n",
			current.Score, best.Score, bestIdx, delta*100)
	}
	printPerBenchDeltas(current, best)
}

// runIndex returns the 1-based absolute index of r in runs (the same
// numbering printHistory and the run header use). 0 if not found.
func runIndex(runs []runRecord, target runRecord) int {
	for i, r := range runs {
		// Timestamp + score uniquely identifies a run in practice;
		// both are written at appendHistory time.
		if r.Timestamp.Equal(target.Timestamp) && r.Score == target.Score {
			return i + 1
		}
	}
	return 0
}

// printPerBenchDeltas shows which benches drove the score change
// relative to the champion, sorted by |pct delta| descending. Only
// benches present on both runs contribute — a bench added mid-history
// doesn't count as a regression because the champion never measured
// it.
func printPerBenchDeltas(current, best runRecord) {
	type delta struct {
		pair, name string
		cur, prev  float64
	}
	var deltas []delta
	for _, cr := range current.Results {
		if math.IsNaN(cr.OsNs) {
			continue
		}
		for _, br := range best.Results {
			if br.Pair == cr.Pair && br.Name == cr.Name && !math.IsNaN(br.OsNs) && br.OsNs > 0 {
				deltas = append(deltas, delta{cr.Pair, cr.Name, cr.OsNs, br.OsNs})
				break
			}
		}
	}
	if len(deltas) == 0 {
		return
	}
	sort.Slice(deltas, func(i, j int) bool {
		di := math.Abs((deltas[i].cur - deltas[i].prev) / deltas[i].prev)
		dj := math.Abs((deltas[j].cur - deltas[j].prev) / deltas[j].prev)
		return di > dj
	})
	// Cap at the top 5 to keep verdict output scannable.
	if len(deltas) > 5 {
		deltas = deltas[:5]
	}
	fmt.Println("   top per-bench deltas (osty ns/op, vs champion):")
	for _, d := range deltas {
		pct := (d.cur - d.prev) / d.prev * 100
		arrow := "="
		switch {
		case pct < -0.5:
			arrow = "↓"
		case pct > 0.5:
			arrow = "↑"
		}
		fmt.Printf("     %s %-26s %10.1f -> %-10.1f (%+.2f%%)\n",
			arrow, d.pair+"."+d.name, d.prev, d.cur, pct)
	}
}

// printResearchJournal reports the full history as a research log:
// the champion, the latest run, the delta, and the per-bench changes
// that drove it. Works without running any benchmarks, so you can
// inspect results between sweeps without triggering a new compile.
func printResearchJournal(noiseFrac float64) error {
	runs, err := readHistory()
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("no history in %s — run `osty-vs-go` at least once first", historyFile)
	}
	fmt.Printf("# osty-vs-go research journal — %d run(s)\n\n", len(runs))
	best, ok := bestRun(runs)
	if !ok {
		fmt.Println("  no run in history has a composite score (need both sides for at least one bench)")
		return nil
	}
	latest := runs[len(runs)-1]
	printRunHeader(runs, best, "champion")
	if latest.Timestamp.Equal(best.Timestamp) {
		fmt.Println("\n  latest run is the current champion.")
		return nil
	}
	printRunHeader(runs, latest, "latest")
	delta := (latest.Score - best.Score) / best.Score
	verdict := "regression"
	switch {
	case latest.Score < best.Score:
		verdict = "improvement (unexpected — latest should have been crowned)"
	case math.Abs(delta) <= noiseFrac:
		verdict = "within noise"
	}
	fmt.Printf("\n  verdict: %s  (%+.2f%% vs champion)\n", verdict, delta*100)
	printPerBenchDeltas(latest, best)

	// Show the run-number gap between champion and latest so the user
	// can tell whether regressions are accumulating or are a single
	// recent bad run.
	bestIdx := runIndex(runs, best)
	latestIdx := len(runs)
	if bestIdx > 0 && latestIdx > bestIdx {
		fmt.Printf("\n  %d run(s) since the champion was set (run #%d -> run #%d)\n",
			latestIdx-bestIdx, bestIdx, latestIdx)
	}
	return nil
}

func printRunHeader(allRuns []runRecord, r runRecord, kind string) {
	idx := runIndex(allRuns, r)
	labelPart := ""
	if r.Label != "" {
		labelPart = " " + r.Label
	}
	headPart := ""
	if r.GitHead != "" {
		headPart = " @ " + r.GitHead
	}
	fmt.Printf("  %s: run #%d%s%s  (score %.3f, %s)\n",
		kind, idx, labelPart, headPart, r.Score,
		r.Timestamp.Local().Format("2006-01-02 15:04:05"))
}

// nextRunSequence scans the history file to pick the next 1-based run
// number. A missing or unreadable history file starts at 1 so a fresh
// checkout's first run shows "#1" instead of "#0".
func nextRunSequence() (int, error) {
	f, err := os.Open(historyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count + 1, nil
}

func appendHistory(rec runRecord) error {
	f, err := os.OpenFile(historyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(rec)
}

func readHistory() ([]runRecord, error) {
	f, err := os.Open(historyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var runs []runRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec runRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse history line: %w", err)
		}
		runs = append(runs, rec)
	}
	return runs, nil
}

// printHistory renders the last N runs as an ASCII trend graph per
// bench. Each row is a single bench keyed by `pair.name`; columns are
// run #1, #2, … left-to-right. The glyph is a simple sparkline over
// Osty ns/op — three-character-wide cells so the numbers fit beside
// the graph.
//
// Runs that don't contain a given bench render as empty cells so a
// bench added mid-history doesn't shift the column alignment for
// earlier points.
func printHistory(lastN int) error {
	runs, err := readHistory()
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("no history in %s — run `osty-vs-go` at least once first", historyFile)
	}
	// Trim to the most recent lastN runs.
	if lastN > len(runs) {
		lastN = len(runs)
	}
	runs = runs[len(runs)-lastN:]

	// Gather all (pair, bench) keys across the window.
	type key struct{ pair, name string }
	seen := map[key]bool{}
	var keys []key
	for _, run := range runs {
		for _, r := range run.Results {
			k := key{r.Pair, r.Name}
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].pair != keys[j].pair {
			return keys[i].pair < keys[j].pair
		}
		return keys[i].name < keys[j].name
	})

	fmt.Printf("# osty-vs-go history — last %d run(s)\n\n", len(runs))
	// Re-read the whole history to get the absolute #N of each kept run.
	allRuns, _ := readHistory()
	base := len(allRuns) - len(runs)
	for i, run := range runs {
		labelPart := ""
		if run.Label != "" {
			labelPart = " " + run.Label
		}
		fmt.Printf("  run %d: #%d %s%s (benchtime=%s)\n",
			i+1, base+i+1, run.Timestamp.Local().Format("2006-01-02 15:04:05"), labelPart, run.BenchTime)
	}
	fmt.Println()

	// For each bench, render a spark line over Osty ns/op.
	// Sparkline uses eight Unicode 1/8-height blocks; we scale to the
	// per-bench max so a fast bench and a slow bench both fill the
	// cell height independently.
	const nameW = 28
	header := fmt.Sprintf("%-*s  trend (osty ns/op)  latest (go ns/op, osty ns/op, osty B/op)", nameW, "pair.bench")
	fmt.Println(header)
	fmt.Println(strings.Repeat("-", len(header)))
	for _, k := range keys {
		label := fmt.Sprintf("%s.%s", k.pair, k.name)
		if len(label) > nameW {
			label = label[:nameW-1] + "…"
		}
		values := make([]float64, len(runs))
		for i, run := range runs {
			v := math.NaN()
			for _, r := range run.Results {
				if r.Pair == k.pair && r.Name == k.name {
					v = r.OsNs
					break
				}
			}
			values[i] = v
		}
		spark := sparkline(values)
		last := latestSummary(runs, k.pair, k.name)
		fmt.Printf("%-*s  %s  %s\n", nameW, label, spark, last)
	}
	return nil
}

// sparkline renders a per-bench trend as a sequence of Unicode 1/8-height
// blocks. The lowest glyph is `▁` rather than a space so a bench that
// regressed to its all-time min still draws a visible cell — blanks
// are reserved for missing measurements.
func sparkline(values []float64) string {
	const glyphs = "▁▂▃▄▅▆▇█"
	runes := []rune(glyphs)
	min := math.Inf(1)
	max := math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) {
			continue
		}
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if math.IsInf(min, 1) {
		return strings.Repeat(" ", len(values))
	}
	var b strings.Builder
	for _, v := range values {
		if math.IsNaN(v) {
			b.WriteRune(' ')
			continue
		}
		idx := 0
		if max > min {
			idx = int(math.Round((v - min) / (max - min) * float64(len(runes)-1)))
		} else {
			idx = len(runes) / 2
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(runes) {
			idx = len(runes) - 1
		}
		b.WriteRune(runes[idx])
	}
	return b.String()
}

func latestSummary(runs []runRecord, pair, name string) string {
	for i := len(runs) - 1; i >= 0; i-- {
		for _, r := range runs[i].Results {
			if r.Pair == pair && r.Name == name {
				return fmt.Sprintf("go %s, osty %s, osty B/op %s",
					formatFloat(r.GoNs, 2),
					formatFloat(r.OsNs, 1),
					formatFloat(r.OsBytes, 0))
			}
		}
	}
	return "—"
}
