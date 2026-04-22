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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type benchResult struct {
	Pair     string
	Name     string
	GoNs     float64 // ns/op median across --go-count sweeps; NaN when absent
	GoNsCV   float64 // coefficient of variation (stddev/mean) across those sweeps; NaN when <2 samples
	GoBytes  float64
	GoAllocs float64
	OsNs     float64 // median ns/op across --osty-count sweeps
	OsNsCV   float64 // coefficient of variation across those sweeps; NaN when <2 samples
	OsBytes  float64
	OsAllocs float64 // always NaN today — kept for a future Osty accessor
}

type runRecord struct {
	Timestamp time.Time   `json:"timestamp"`
	BenchTime string      `json:"bench_time"`
	PairsDir  string      `json:"pairs_dir"`
	Label     string      `json:"label,omitempty"`
	GitHead   string      `json:"git_head,omitempty"`
	GoCount   int         `json:"go_count,omitempty"`
	GoCPU     string      `json:"go_cpu,omitempty"`
	OstyCount int         `json:"osty_count,omitempty"`
	Machine   machineInfo `json:"machine,omitempty"`
	// BaselineNs is the per-call sampling + loop overhead in nanoseconds,
	// measured via the `baseline_empty` pair if present (so the Osty bench
	// harness's own per-iteration clock pair doesn't poison microbenches).
	// 0 when no baseline ran. Only used for display; never mutates the raw
	// Osty ns/op stored on each result so history stays comparable across
	// runs that did or didn't enable subtraction.
	BaselineNs float64 `json:"baseline_ns,omitempty"`
	// Score is the geomean of osty_ns/go_ns across benches with both
	// sides present. Lower = Osty closer to Go. NaN when no pair
	// contributes. Persisted so the research/loop paths don't have to
	// re-derive it from results (and so backfilling a new metric later
	// doesn't silently mutate the published score of old runs).
	Score   float64       `json:"score,omitempty"`
	Results []benchResult `json:"results"`
}

// machineInfo captures enough about the host to notice when a history
// entry shouldn't be compared against another (different machine, CPU
// throttle change). None of this is used for verdicts — it's an audit
// trail so a surprising regression can be attributed to hardware drift
// instead of a code change.
type machineInfo struct {
	GOOS     string `json:"goos,omitempty"`
	GOARCH   string `json:"goarch,omitempty"`
	NumCPU   int    `json:"num_cpu,omitempty"`
	CPUModel string `json:"cpu_model,omitempty"`
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

// baselinePairName is the pair directory whose job is to measure the
// Osty bench harness's per-iteration sampling + loop overhead. When
// present and --subtract-baseline is on, its median osty ns/op is
// subtracted from every other pair's displayed ns/op. It does NOT
// contribute to the geomean score (it would always anchor near 1x).
const baselinePairName = "baseline_empty"

// findBaselineNs extracts the Osty ns/op of the baseline_empty pair
// from `rows`. Returns 0 when no such pair ran or its osty side is
// missing (i.e. the pair failed to compile on this build).
func findBaselineNs(rows []benchResult) float64 {
	for _, r := range rows {
		if r.Pair != baselinePairName {
			continue
		}
		if math.IsNaN(r.OsNs) {
			continue
		}
		return r.OsNs
	}
	return 0
}

// captureMachineInfo records enough host metadata to attribute surprising
// regressions to hardware drift instead of code changes. Best-effort;
// any missing field falls back to empty string. Deliberately avoids
// running external binaries in a tight loop — called once per run.
func captureMachineInfo() machineInfo {
	return machineInfo{
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		NumCPU:   runtime.NumCPU(),
		CPUModel: detectCPUModel(),
	}
}

func detectCPUModel() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		data, err := os.ReadFile("/proc/cpuinfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "model name") {
					if idx := strings.Index(line, ":"); idx >= 0 {
						return strings.TrimSpace(line[idx+1:])
					}
				}
			}
		}
	}
	return ""
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
		"go_ns":       r.GoNs,
		"go_ns_cv":    r.GoNsCV,
		"go_bytes":    r.GoBytes,
		"go_allocs":   r.GoAllocs,
		"osty_ns":     r.OsNs,
		"osty_ns_cv":  r.OsNsCV,
		"osty_bytes":  r.OsBytes,
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
		Pair     string   `json:"pair"`
		Name     string   `json:"name"`
		GoNs     *float64 `json:"go_ns"`
		GoNsCV   *float64 `json:"go_ns_cv"`
		GoBytes  *float64 `json:"go_bytes"`
		GoAllocs *float64 `json:"go_allocs"`
		OsNs     *float64 `json:"osty_ns"`
		OsNsCV   *float64 `json:"osty_ns_cv"`
		OsBytes  *float64 `json:"osty_bytes"`
		OsAllocs *float64 `json:"osty_allocs"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	r.Pair = m.Pair
	r.Name = m.Name
	r.GoNs = ptrOrNaN(m.GoNs)
	r.GoNsCV = ptrOrNaN(m.GoNsCV)
	r.GoBytes = ptrOrNaN(m.GoBytes)
	r.GoAllocs = ptrOrNaN(m.GoAllocs)
	r.OsNs = ptrOrNaN(m.OsNs)
	r.OsNsCV = ptrOrNaN(m.OsNsCV)
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

type goBenchRunner func(goBin, pairsDir, pair, benchTime string, goCount int, goCPU string) ([]benchResult, error)

type goBenchCache struct {
	entries map[goBenchCacheKey][]benchResult
}

type goBenchCacheKey struct {
	PairsDir    string
	Pair        string
	GoBin       string
	BenchTime   string
	GoCount     int
	GoCPU       string
	Fingerprint string
}

func newGoBenchCache() *goBenchCache {
	return &goBenchCache{entries: map[goBenchCacheKey][]benchResult{}}
}

func runGoBenchCached(cache *goBenchCache, runner goBenchRunner, goBin, pairsDir, pair, benchTime string, goCount int, goCPU string) ([]benchResult, error) {
	if cache == nil {
		return runner(goBin, pairsDir, pair, benchTime, goCount, goCPU)
	}
	if cache.entries == nil {
		cache.entries = map[goBenchCacheKey][]benchResult{}
	}
	key, err := goBenchCacheKeyFor(goBin, pairsDir, pair, benchTime, goCount, goCPU)
	if err != nil {
		return nil, err
	}
	if cached, ok := cache.entries[key]; ok {
		return cloneBenchResults(cached), nil
	}
	rows, err := runner(goBin, pairsDir, pair, benchTime, goCount, goCPU)
	if err != nil {
		return nil, err
	}
	cache.entries[key] = cloneBenchResults(rows)
	return cloneBenchResults(rows), nil
}

func cloneBenchResults(rows []benchResult) []benchResult {
	if len(rows) == 0 {
		return nil
	}
	return append([]benchResult(nil), rows...)
}

func goBenchCacheKeyFor(goBin, pairsDir, pair, benchTime string, goCount int, goCPU string) (goBenchCacheKey, error) {
	fingerprint, err := goBenchInputsFingerprint(filepath.Join(pairsDir, pair, "go"))
	if err != nil {
		return goBenchCacheKey{}, err
	}
	return goBenchCacheKey{
		PairsDir:    filepath.Clean(pairsDir),
		Pair:        pair,
		GoBin:       goBin,
		BenchTime:   benchTime,
		GoCount:     goCount,
		GoCPU:       goCPU,
		Fingerprint: fingerprint,
	}, nil
}

func goBenchInputsFingerprint(pairDir string) (string, error) {
	absPairDir, err := filepath.Abs(pairDir)
	if err != nil {
		return "", fmt.Errorf("abs pair dir %s: %w", pairDir, err)
	}
	h := sha256.New()
	if err := hashDirTree(h, "pair", absPairDir); err != nil {
		return "", err
	}
	if modRoot, ok := findGoModRoot(absPairDir); ok {
		for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
			if err := hashOptionalFile(h, name, filepath.Join(modRoot, name)); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func findGoModRoot(start string) (string, bool) {
	dir := start
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func hashDirTree(h hash.Hash, label, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return hashFile(h, label+"/"+filepath.ToSlash(rel), path)
	})
}

func hashOptionalFile(h hash.Hash, logicalName, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return hashFile(h, logicalName, path)
}

func hashFile(h hash.Hash, logicalName, path string) error {
	if _, err := io.WriteString(h, logicalName+"\n"); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	_, err = io.WriteString(h, "\n")
	return err
}

func main() {
	fs := flag.NewFlagSet("osty-vs-go", flag.ExitOnError)
	benchTime := fs.String("benchtime", "500ms", "per-bench duration target for both Go (-benchtime) and Osty (--benchtime)")
	pairsDir := fs.String("pairs-dir", "benchmarks/osty-vs-go", "root containing <pair>/go/ and <pair>/osty/ subdirs")
	ostyBin := fs.String("osty", "", "path to the osty binary (default: $PWD/.bin/osty if present, else `osty` from PATH)")
	goBin := fs.String("go", "go", "path to the go binary used for `go test -bench`")
	goCount := fs.Int("go-count", 3, "number of independent Go bench sweeps per pair; the runner records the median to reduce scheduler jitter")
	goCPU := fs.String("go-cpu", "1", "value forwarded to `go test -cpu` for stable Go-side scheduling (default 1)")
	ostyCount := fs.Int("osty-count", 1, "number of independent Osty bench sweeps per pair; each sweep triggers a full LLVM compile so values >1 trade wall time for a stddev/CoV estimate (default 1 — single sample, CoV reported as —)")
	subtractBaseline := fs.Bool("subtract-baseline", false, "if set and the `baseline_empty` pair is present, its median ns/op is subtracted from every Osty ns/op as an estimate of the Osty bench harness's own per-iteration clock-sampling overhead. Display-only; raw values are still persisted to history.")
	filter := fs.String("filter", "", "optional regex over pair names; only matching pairs run")
	historyN := fs.Int("history", 0, "if >0, skip running and render the last N runs from "+historyFile+" as an ASCII trend graph")
	label := fs.String("label", "", "optional short label stored with this run (shown in history output)")
	research := fs.Bool("research", false, "skip running and summarize history as an autoresearch journal (champion, latest, per-bench deltas)")
	loopInterval := fs.Duration("loop", 0, "if >0, keep running in a loop with this interval between runs — compiles/edits land between ticks and each run prints a vs-best verdict")
	noiseFrac := fs.Float64("noise", 0.02, "delta fraction below which a vs-best verdict is reported as within-noise (default 2%)")
	autoresearch := fs.Bool("autoresearch", false, "fully autonomous keep-the-best loop (karpathy/autoresearch-style): before each bench sweep run --mutator, then git-commit on NEW BEST or `git reset --hard HEAD` on regression/noise. Requires --mutator and --max-experiments. Moves HEAD to an autoresearch/<ts> branch so the source branch is never modified in place.")
	mutatorCmd := fs.String("mutator", "", "shell command run before each autoresearch iteration. Must modify the working tree; the exit code gates whether the iteration proceeds (nonzero = skip). Stateless — the same command runs every iteration, and it sees the current champion at HEAD.")
	maxExperiments := fs.Int("max-experiments", 0, "cap for --autoresearch: stop after this many bench sweeps (including regressions). 0 = unlimited, use with care.")
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
	if *goCount <= 0 {
		fmt.Fprintln(os.Stderr, "osty-vs-go: --go-count must be > 0")
		os.Exit(2)
	}
	if *ostyCount <= 0 {
		fmt.Fprintln(os.Stderr, "osty-vs-go: --osty-count must be > 0")
		os.Exit(2)
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

	if *autoresearch {
		if *mutatorCmd == "" {
			fmt.Fprintln(os.Stderr, "osty-vs-go: --autoresearch requires --mutator '<cmd>'")
			os.Exit(2)
		}
		if *maxExperiments <= 0 {
			fmt.Fprintln(os.Stderr, "osty-vs-go: --autoresearch requires --max-experiments N (>0) so the loop can't run forever unattended")
			os.Exit(2)
		}
		cfg := autoresearchConfig{
			Mutator:          *mutatorCmd,
			MaxExperiments:   *maxExperiments,
			BenchTime:        *benchTime,
			PairsDir:         *pairsDir,
			OstyBin:          osty,
			GoBin:            *goBin,
			GoCount:          *goCount,
			GoCPU:            *goCPU,
			OstyCount:        *ostyCount,
			SubtractBaseline: *subtractBaseline,
			Label:            *label,
			NoiseFrac:        *noiseFrac,
			PairRE:           pairRE,
			Interval:         *loopInterval,
			GoCache:          newGoBenchCache(),
		}
		if err := runAutoresearch(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *loopInterval > 0 {
		runLoop(*loopInterval, *benchTime, *pairsDir, osty, *goBin, *goCount, *goCPU, *ostyCount, *subtractBaseline, *label, *noiseFrac, pairRE)
		return
	}

	if _, err := runOnce(*benchTime, *pairsDir, osty, *goBin, *goCount, *goCPU, *ostyCount, *subtractBaseline, *label, *noiseFrac, pairRE, nil); err != nil {
		fmt.Fprintf(os.Stderr, "osty-vs-go: %v\n", err)
		os.Exit(1)
	}
}

// runOnce executes one full bench sweep: discover pairs, run each
// side, print the table, append to history, print the vs-best verdict.
// Returns the new record so `--loop` can stream verdicts without
// re-reading history.
func runOnce(benchTime, pairsDir, ostyBin, goBin string, goCount int, goCPU string, ostyCount int, subtractBaseline bool, label string, noiseFrac float64, pairRE *regexp.Regexp, goCache *goBenchCache) (runRecord, error) {
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
	fmt.Printf("# osty-vs-go run #%d%s%s — benchtime=%s, pairs=%d, go-count=%d, osty-count=%d, go-cpu=%s\n\n", seq, labelPart, headPart, benchTime, len(pairs), goCount, ostyCount, goCPU)

	var rows []benchResult
	for _, pair := range pairs {
		goRows, err := runGoBenchCached(goCache, runGoBench, goBin, pairsDir, pair, benchTime, goCount, goCPU)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: go bench %s: %v\n", pair, err)
		}
		osRows, err := runOstyBench(ostyBin, pairsDir, pair, benchTime, ostyCount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: osty bench %s: %v\n", pair, err)
		}
		rows = append(rows, mergePairRows(pair, goRows, osRows)...)
	}

	// baselineNs is the Osty per-iteration sampling overhead estimate.
	// Always 0 when no baseline_empty pair ran or --subtract-baseline is off.
	// Display-only: raw ns numbers stay in `rows` so history comparisons
	// remain apples-to-apples across runs.
	baselineNs := 0.0
	if subtractBaseline {
		baselineNs = findBaselineNs(rows)
	}

	printTable(rows, baselineNs)

	record := runRecord{
		Timestamp:  time.Now(),
		BenchTime:  benchTime,
		PairsDir:   pairsDir,
		Label:      label,
		GitHead:    head,
		GoCount:    goCount,
		GoCPU:      goCPU,
		OstyCount:  ostyCount,
		Machine:    captureMachineInfo(),
		BaselineNs: baselineNs,
		Score:      composite(rows),
		Results:    rows,
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
func runLoop(interval time.Duration, benchTime, pairsDir, ostyBin, goBin string, goCount int, goCPU string, ostyCount int, subtractBaseline bool, label string, noiseFrac float64, pairRE *regexp.Regexp) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	goCache := newGoBenchCache()
	// Run immediately rather than waiting `interval` for the first
	// tick — the user just launched it and expects feedback.
	for {
		if _, err := runOnce(benchTime, pairsDir, ostyBin, goBin, goCount, goCPU, ostyCount, subtractBaseline, label, noiseFrac, pairRE, goCache); err != nil {
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

// autoresearchConfig bundles the per-run autoresearch parameters.
// Collecting them in a struct keeps the main dispatcher from growing
// an unreadable 10-arg function signature and makes it trivial to pass
// the same config into tests that mock out the bench sweep.
type autoresearchConfig struct {
	Mutator          string
	MaxExperiments   int
	BenchTime        string
	PairsDir         string
	OstyBin          string
	GoBin            string
	GoCount          int
	GoCPU            string
	OstyCount        int
	SubtractBaseline bool
	Label            string
	NoiseFrac        float64
	PairRE           *regexp.Regexp
	Interval         time.Duration
	GoCache          *goBenchCache
}

// runAutoresearch is the closest faithful take on karpathy/autoresearch
// we can ship without an in-process LLM: the caller supplies a mutator
// command (any executable that edits the working tree), and this loop
// runs mutate → bench → keep-or-revert against the captured champion.
//
// Safety rails:
//   - Must run inside a git checkout; tree must be clean at launch.
//   - Creates and checks out `autoresearch/<ts>` so the user's source
//     branch is never written to in place. The start branch is printed
//     so the user can `git checkout <it>` when the session ends.
//   - `git reset --hard HEAD` is only called against commits this loop
//     itself made (the autoresearch branch tip). It cannot reach pre-
//     session work because that lives on the start branch.
//   - `--max-experiments` is mandatory so a hung mutator loop can't run
//     indefinitely and burn the machine.
//   - SIGINT / SIGTERM drain gracefully between iterations.
func runAutoresearch(cfg autoresearchConfig) error {
	if err := requireCleanTree(); err != nil {
		return err
	}
	startBranch, err := currentBranch()
	if err != nil {
		return fmt.Errorf("read current branch: %w", err)
	}
	workBranch := fmt.Sprintf("autoresearch/%s", time.Now().Format("20060102-150405"))
	if _, err := gitRun("checkout", "-b", workBranch); err != nil {
		return fmt.Errorf("create autoresearch branch %s: %w", workBranch, err)
	}
	fmt.Printf("# osty-vs-go autoresearch — branch %s (from %s)\n", workBranch, startBranch)
	fmt.Printf("  mutator: %s\n", cfg.Mutator)
	fmt.Printf("  budget:  %d experiments (interval between ticks: %s)\n\n",
		cfg.MaxExperiments, cfg.Interval)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	stats := autoresearchStats{startBranch: startBranch, workBranch: workBranch}
	for i := 1; i <= cfg.MaxExperiments; i++ {
		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "\nosty-vs-go: autoresearch interrupted, exiting")
			stats.interrupted = true
			break
		default:
		}
		if stats.interrupted {
			break
		}
		if err := runAutoresearchIter(cfg, i, &stats); err != nil {
			fmt.Fprintf(os.Stderr, "osty-vs-go: experiment #%d: %v\n", i, err)
			stats.errors++
			continue
		}
		if cfg.Interval > 0 && i < cfg.MaxExperiments {
			fmt.Printf("\n(sleeping %s before next experiment; Ctrl-C to stop)\n", cfg.Interval)
			select {
			case <-sig:
				stats.interrupted = true
			case <-time.After(cfg.Interval):
			}
		}
	}

	printAutoresearchSummary(stats)
	return nil
}

// autoresearchStats aggregates the session counters for the final
// summary. `kept` counts only real NEW BEST promotions; first-run and
// kept-by-tolerance paths still count as kept because they moved HEAD.
type autoresearchStats struct {
	startBranch string
	workBranch  string
	attempted   int
	kept        int
	reverted    int
	mutatorFail int
	errors      int
	interrupted bool
	best        runRecord // champion of this session
	hasBest     bool
}

func runAutoresearchIter(cfg autoresearchConfig, iter int, stats *autoresearchStats) error {
	stats.attempted++
	fmt.Printf("\n=== autoresearch experiment #%d ===\n", iter)

	// Phase 1: run the mutator. Nonzero exit → skip this iteration's
	// bench sweep and revert anything the mutator wrote. This keeps a
	// flaky mutator from contaminating HEAD.
	if err := runMutator(cfg.Mutator); err != nil {
		stats.mutatorFail++
		fmt.Fprintf(os.Stderr, "  mutator failed (%v); reverting any partial writes\n", err)
		_, _ = gitRun("reset", "--hard", "HEAD")
		_, _ = gitRun("clean", "-fd")
		return nil
	}

	// Phase 2: run the bench sweep. runOnce already writes the history
	// file and prints the verdict relative to all-time history.
	rec, err := runOnce(cfg.BenchTime, cfg.PairsDir, cfg.OstyBin, cfg.GoBin, cfg.GoCount, cfg.GoCPU, cfg.OstyCount, cfg.SubtractBaseline, cfg.Label, cfg.NoiseFrac, cfg.PairRE, cfg.GoCache)
	if err != nil {
		// Bench failure shouldn't leave the mutator's changes on HEAD.
		_, _ = gitRun("reset", "--hard", "HEAD")
		_, _ = gitRun("clean", "-fd")
		return err
	}

	// Phase 3: the autonomous decision — against THIS SESSION'S
	// champion, not all-time history. Otherwise a pre-session run on
	// a different machine could lock out every mutation.
	keep := decideKeep(rec, stats.best, stats.hasBest, cfg.NoiseFrac)
	if keep {
		if err := commitKept(iter, rec); err != nil {
			return fmt.Errorf("commit kept experiment #%d: %w", iter, err)
		}
		stats.kept++
		stats.best = rec
		stats.hasBest = true
		fmt.Printf("  -> KEPT (committed on %s)\n", stats.workBranch)
	} else {
		if _, err := gitRun("reset", "--hard", "HEAD"); err != nil {
			return fmt.Errorf("revert rejected experiment #%d: %w", iter, err)
		}
		if _, err := gitRun("clean", "-fd"); err != nil {
			return fmt.Errorf("clean untracked in rejected experiment #%d: %w", iter, err)
		}
		stats.reverted++
		fmt.Printf("  -> REVERTED (HEAD unchanged on %s)\n", stats.workBranch)
	}
	return nil
}

// decideKeep is the one-metric keep-or-discard decision. Pulled into
// a plain function so unit tests can cover the branching matrix
// without spinning up a tmp git repo.
func decideKeep(cur runRecord, best runRecord, hasBest bool, noiseFrac float64) bool {
	if math.IsNaN(cur.Score) {
		// No verdict possible; treat as no improvement so HEAD stays
		// at the session's last committed state.
		return false
	}
	if !hasBest {
		// First qualifying experiment in the session always seeds the
		// champion. Otherwise we'd discard the only data point.
		return true
	}
	// Strictly lower is a keep. Tie-or-worse is a revert — even within
	// the noise band, because "kept a regression by accident" is a
	// worse failure mode than "rejected an indistinguishable improvement".
	// The noise knob still matters for the human-facing verdict; here
	// we want the autonomous loop biased toward reverting noise.
	return cur.Score < best.Score
}

func runMutator(cmdStr string) error {
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func commitKept(iter int, rec runRecord) error {
	if _, err := gitRun("add", "-A"); err != nil {
		return err
	}
	// Empty stage (mutator made no real change) is a valid "kept" only
	// if it's the very first iter; otherwise `git commit` without
	// --allow-empty would error out. We prefer --allow-empty here so
	// the linear history of "one commit per accepted experiment"
	// survives even a degenerate mutator.
	msg := fmt.Sprintf("autoresearch #%d: score %.4f (kept)", iter, rec.Score)
	if _, err := gitRun("commit", "--allow-empty", "-m", msg); err != nil {
		return err
	}
	return nil
}

func requireCleanTree() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("autoresearch requires git in PATH: %w", err)
	}
	if out, err := gitRun("rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return fmt.Errorf("autoresearch must run inside a git working tree")
	}
	out, err := gitRun("status", "--porcelain")
	if err != nil {
		return fmt.Errorf("check tree state: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("autoresearch refuses to run with a dirty tree; commit or stash first\n%s", out)
	}
	return nil
}

func currentBranch() (string, error) {
	out, err := gitRun("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func gitRun(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	return string(out), err
}

func printAutoresearchSummary(s autoresearchStats) {
	fmt.Println("\n=== autoresearch summary ===")
	fmt.Printf("  started from: %s\n", s.startBranch)
	fmt.Printf("  work branch:  %s\n", s.workBranch)
	fmt.Printf("  experiments:  %d attempted, %d kept, %d reverted, %d mutator failures, %d errors\n",
		s.attempted, s.kept, s.reverted, s.mutatorFail, s.errors)
	if s.hasBest {
		fmt.Printf("  session best: score %.4f\n", s.best.Score)
	} else {
		fmt.Println("  session best: none (no experiment produced a valid score)")
	}
	if s.interrupted {
		fmt.Println("  status:       interrupted")
	}
	fmt.Printf("\n  to return to your original branch: git checkout %s\n", s.startBranch)
	fmt.Printf("  to inspect kept experiments:       git log %s\n", s.workBranch)
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
// on the pair's go/ subdir. Multiple independent sweeps can be
// requested via goCount; the runner records the median per metric so a
// noisy Go-side outlier does not dominate the published ratio.
func runGoBench(goBin, pairsDir, pair, benchTime string, goCount int, goCPU string) ([]benchResult, error) {
	pkg := goPackageArg(filepath.Join(pairsDir, pair, "go"))
	runs := make([][]benchResult, 0, goCount)
	for i := 0; i < goCount; i++ {
		args := []string{"test", "-count=1", "-run=^$", "-bench=.", "-benchmem"}
		if goCPU != "" {
			args = append(args, "-cpu="+goCPU)
		}
		args = append(args, "-benchtime="+benchTime, pkg)
		cmd := exec.Command(goBin, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%s test run %d/%d: %w\n%s", goBin, i+1, goCount, err, out)
		}
		runs = append(runs, parseGoBenchOutput(pair, string(out)))
	}
	return aggregateGoBenchRuns(pair, runs), nil
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
			GoNsCV:   math.NaN(),
			GoBytes:  math.NaN(),
			GoAllocs: math.NaN(),
			OsNs:     math.NaN(),
			OsNsCV:   math.NaN(),
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

func aggregateGoBenchRuns(pair string, runs [][]benchResult) []benchResult {
	type series struct {
		ns     []float64
		bytes  []float64
		allocs []float64
	}
	byName := map[string]*series{}
	for _, rows := range runs {
		for _, row := range rows {
			s := byName[row.Name]
			if s == nil {
				s = &series{}
				byName[row.Name] = s
			}
			s.ns = appendFinite(s.ns, row.GoNs)
			s.bytes = appendFinite(s.bytes, row.GoBytes)
			s.allocs = appendFinite(s.allocs, row.GoAllocs)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]benchResult, 0, len(names))
	for _, name := range names {
		s := byName[name]
		out = append(out, benchResult{
			Pair:     pair,
			Name:     name,
			GoNs:     medianFinite(s.ns),
			GoNsCV:   coefficientOfVariation(s.ns),
			GoBytes:  medianFinite(s.bytes),
			GoAllocs: medianFinite(s.allocs),
			OsNs:     math.NaN(),
			OsNsCV:   math.NaN(),
			OsBytes:  math.NaN(),
			OsAllocs: math.NaN(),
		})
	}
	return out
}

func appendFinite(dst []float64, v float64) []float64 {
	if math.IsNaN(v) {
		return dst
	}
	return append(dst, v)
}

func medianFinite(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// coefficientOfVariation returns sample stddev / mean over `values`. NaN
// when fewer than two samples or when the mean is non-positive (the
// ratio isn't meaningful on a zeroed bench). The caller uses this as a
// per-bench "how noisy is this number?" signal — not an error bar, but
// close enough to flag when a ±2% verdict is crying wolf.
func coefficientOfVariation(values []float64) float64 {
	if len(values) < 2 {
		return math.NaN()
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	if mean <= 0 {
		return math.NaN()
	}
	var sqSum float64
	for _, v := range values {
		d := v - mean
		sqSum += d * d
	}
	stddev := math.Sqrt(sqSum / float64(len(values)-1))
	return stddev / mean
}

func runOstyBench(ostyBin, pairsDir, pair, benchTime string, ostyCount int) ([]benchResult, error) {
	dir := filepath.Join(pairsDir, pair, "osty")
	if ostyCount < 1 {
		ostyCount = 1
	}
	runs := make([][]benchResult, 0, ostyCount)
	for i := 0; i < ostyCount; i++ {
		cmd := exec.Command(ostyBin, "test", "--bench", "--serial", "--benchtime="+benchTime, dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%s test --bench run %d/%d: %w\n%s", ostyBin, i+1, ostyCount, err, out)
		}
		runs = append(runs, parseOstyBenchOutput(pair, string(out)))
	}
	return aggregateOstyBenchRuns(pair, runs), nil
}

// aggregateOstyBenchRuns collapses ostyCount independent Osty sweeps
// into per-bench median + CoV, mirroring the Go-side aggregator. One run
// still passes through unchanged (single-sample CoV is NaN by design).
func aggregateOstyBenchRuns(pair string, runs [][]benchResult) []benchResult {
	type series struct {
		ns    []float64
		bytes []float64
	}
	byName := map[string]*series{}
	for _, rows := range runs {
		for _, row := range rows {
			s := byName[row.Name]
			if s == nil {
				s = &series{}
				byName[row.Name] = s
			}
			s.ns = appendFinite(s.ns, row.OsNs)
			s.bytes = appendFinite(s.bytes, row.OsBytes)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]benchResult, 0, len(names))
	for _, name := range names {
		s := byName[name]
		out = append(out, benchResult{
			Pair:     pair,
			Name:     name,
			GoNs:     math.NaN(),
			GoNsCV:   math.NaN(),
			GoBytes:  math.NaN(),
			GoAllocs: math.NaN(),
			OsNs:     medianFinite(s.ns),
			OsNsCV:   coefficientOfVariation(s.ns),
			OsBytes:  medianFinite(s.bytes),
			OsAllocs: math.NaN(),
		})
	}
	return out
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
	ostyBenchSumRE = regexp.MustCompile(`^bench\s+.+?\s+iter=\d+\s+total=\d+ns\s+avg=(\d+)ns(?:\s+bytes/op=(\d+))?`)
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
				GoNsCV:   math.NaN(),
				GoBytes:  math.NaN(),
				GoAllocs: math.NaN(),
				OsNs:     avg,
				OsNsCV:   math.NaN(),
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
			existing.OsNsCV = r.OsNsCV
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

func printTable(rows []benchResult, baselineNs float64) {
	header := []string{
		"pair", "bench",
		"go ns/op", "go ±CV", "osty ns/op", "osty ±CV", "ns osty/go",
		"go B/op", "osty B/op",
		"go allocs/op",
	}
	lines := make([][]string, 0, len(rows))
	for _, r := range rows {
		osDisplay := r.OsNs
		if baselineNs > 0 && !math.IsNaN(osDisplay) {
			osDisplay = osDisplay - baselineNs
			if osDisplay < 0 {
				osDisplay = 0
			}
		}
		lines = append(lines, []string{
			r.Pair, r.Name,
			formatFloat(r.GoNs, 2),
			formatCV(r.GoNsCV),
			formatFloat(osDisplay, 1),
			formatCV(r.OsNsCV),
			formatRatio(osDisplay, r.GoNs),
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
		if r.Pair == baselinePairName {
			continue
		}
		if math.IsNaN(r.GoNs) || math.IsNaN(r.OsNs) || r.GoNs <= 0 {
			continue
		}
		osNs := r.OsNs
		if baselineNs > 0 {
			osNs = osNs - baselineNs
			if osNs <= 0 {
				continue
			}
		}
		logSum += math.Log(osNs / r.GoNs)
		n++
	}
	if n > 0 {
		fmt.Printf("\ngeomean ns osty/go: %.2fx  (over %d benches)\n", math.Exp(logSum/float64(n)), n)
	}
	if baselineNs > 0 {
		fmt.Printf("baseline subtracted: %.1fns per Osty iteration (from `baseline_empty` pair)\n", baselineNs)
	}
}

func formatFloat(v float64, decimals int) string {
	if math.IsNaN(v) {
		return "—"
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

// formatCV renders a coefficient of variation as "±X.Y%", or "—" when
// there's no multi-sample estimate. The ± hints that this is spread
// around the median, not a one-sided error bar.
func formatCV(cv float64) string {
	if math.IsNaN(cv) {
		return "—"
	}
	return fmt.Sprintf("±%.1f%%", cv*100)
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
	// Dynamic noise band: take the larger of the user-supplied --noise
	// and the aggregate per-bench CoV reported on this run. If the
	// measurement itself is noisy, a small delta won't cry "regression".
	effNoise, noiseSrc := effectiveNoise(noiseFrac, current)
	switch {
	case current.Score < best.Score:
		fmt.Printf("\n-> NEW BEST: score %.3f  (prev champion %.3f at run #%d; -%.2f%%)\n",
			current.Score, best.Score, bestIdx, -delta*100)
	case math.Abs(delta) <= effNoise:
		fmt.Printf("\n-> within noise: score %.3f vs best %.3f at run #%d (%+.2f%%; noise band %.2f%% via %s)\n",
			current.Score, best.Score, bestIdx, delta*100, effNoise*100, noiseSrc)
	default:
		fmt.Printf("\n-> regression: score %.3f vs best %.3f at run #%d (%+.2f%%; noise band %.2f%% via %s)\n",
			current.Score, best.Score, bestIdx, delta*100, effNoise*100, noiseSrc)
	}
	printPerBenchDeltas(current, best)
}

// effectiveNoise returns the wider of the user-supplied band and the
// per-bench median Osty CoV captured on this run. Returns the static
// --noise when no CV was captured (single-sample run) so behavior
// matches pre-CV history.
func effectiveNoise(userNoise float64, rec runRecord) (float64, string) {
	var cvs []float64
	for _, r := range rec.Results {
		if !math.IsNaN(r.OsNsCV) {
			cvs = append(cvs, r.OsNsCV)
		}
	}
	if len(cvs) == 0 {
		return userNoise, "--noise"
	}
	measured := medianFinite(cvs)
	if math.IsNaN(measured) || measured <= userNoise {
		return userNoise, "--noise"
	}
	return measured, "measured CoV"
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
