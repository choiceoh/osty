package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Snapshot is a slim, JSON-portable form of Result that records just
// enough to drive a baseline comparison: per-stage timing and counts,
// plus the diagnostic histogram. The full Result includes pointers
// (AST, Resolve, Check, Lint) that can't round-trip through JSON;
// Snapshot is what `osty pipeline --json` emits and what `--baseline`
// reads back.
type Snapshot struct {
	Stages            []SnapshotStage `json:"stages"`
	DiagnosticsByCode map[string]int  `json:"diagnostics_by_code"`
	PerDecl           []DeclTiming    `json:"per_decl,omitempty"`
}

type SnapshotStage struct {
	Name       string         `json:"name"`
	DurationMS float64        `json:"duration_ms"`
	Output     string         `json:"output"`
	Errors     int            `json:"errors"`
	Warnings   int            `json:"warnings"`
	Counts     map[string]int `json:"counts,omitempty"`
}

// ToSnapshot extracts the JSON-portable view of a Result.
func (r Result) ToSnapshot() Snapshot {
	stages := make([]SnapshotStage, len(r.Stages))
	for i, s := range r.Stages {
		stages[i] = SnapshotStage{
			Name:       s.Name,
			DurationMS: float64(s.Duration.Microseconds()) / 1000.0,
			Output:     s.Output,
			Errors:     s.Errors,
			Warnings:   s.Warnings,
			Counts:     s.Counts,
		}
	}
	hist := map[string]int{}
	for _, d := range r.AllDiags {
		c := d.Code
		if c == "" {
			c = "(no code)"
		}
		hist[c]++
	}
	return Snapshot{
		Stages:            stages,
		DiagnosticsByCode: hist,
		PerDecl:           r.PerDecl,
	}
}

// LoadSnapshot decodes a Snapshot from a JSON document previously
// emitted by RenderJSON or ToSnapshot+json.Marshal.
func LoadSnapshot(r io.Reader) (Snapshot, error) {
	var s Snapshot
	dec := json.NewDecoder(r)
	if err := dec.Decode(&s); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// Comparison records the per-stage delta between a baseline and a
// current run. Stages that exist in only one side appear with the
// other side zeroed out.
type Comparison struct {
	Stages    []StageDelta
	DiagDelta map[string][2]int // code → [baselineCount, currentCount]
}

// StageDelta is one row of the comparison table.
type StageDelta struct {
	Name           string
	BaselineMS     float64
	CurrentMS      float64
	DeltaMS        float64 // current - baseline
	DeltaPercent   float64 // 100 * (current - baseline) / baseline; 0 when baseline is 0
	BaselineErrors int
	CurrentErrors  int
	BaselineWarns  int
	CurrentWarns   int
}

// Compare diffs a baseline snapshot against a current Result and
// returns a structured Comparison. Stage matching is by name (not
// position) so adding the optional "gen" stage between runs doesn't
// scramble the output.
func Compare(baseline Snapshot, current Result) Comparison {
	cur := current.ToSnapshot()

	byName := func(stages []SnapshotStage) map[string]SnapshotStage {
		m := make(map[string]SnapshotStage, len(stages))
		for _, s := range stages {
			m[s.Name] = s
		}
		return m
	}
	bMap, cMap := byName(baseline.Stages), byName(cur.Stages)

	// Collect every stage name from either side, in a stable order:
	// keep the current run's order, then append anything baseline-only.
	seen := map[string]bool{}
	var names []string
	for _, s := range cur.Stages {
		if !seen[s.Name] {
			seen[s.Name] = true
			names = append(names, s.Name)
		}
	}
	for _, s := range baseline.Stages {
		if !seen[s.Name] {
			seen[s.Name] = true
			names = append(names, s.Name)
		}
	}

	out := Comparison{DiagDelta: map[string][2]int{}}
	for _, n := range names {
		b := bMap[n]
		c := cMap[n]
		delta := c.DurationMS - b.DurationMS
		var pct float64
		if b.DurationMS > 0 {
			pct = 100 * delta / b.DurationMS
		}
		out.Stages = append(out.Stages, StageDelta{
			Name:           n,
			BaselineMS:     b.DurationMS,
			CurrentMS:      c.DurationMS,
			DeltaMS:        delta,
			DeltaPercent:   pct,
			BaselineErrors: b.Errors,
			CurrentErrors:  c.Errors,
			BaselineWarns:  b.Warnings,
			CurrentWarns:   c.Warnings,
		})
	}

	// Diagnostic histogram delta: union of codes from both sides.
	codes := map[string]bool{}
	for k := range baseline.DiagnosticsByCode {
		codes[k] = true
	}
	for k := range cur.DiagnosticsByCode {
		codes[k] = true
	}
	for k := range codes {
		out.DiagDelta[k] = [2]int{
			baseline.DiagnosticsByCode[k],
			cur.DiagnosticsByCode[k],
		}
	}

	return out
}

// RenderText writes a fixed-width comparison table to w. Negative
// deltas (current is faster than baseline) get a leading "-"; positive
// regressions get "+". A trailing diagnostic-delta block lists every
// code whose count differs between runs.
func (c Comparison) RenderText(w io.Writer) {
	fmt.Fprintf(w, "%-9s  %12s  %12s  %12s  %8s  %s\n",
		"PHASE", "BASELINE", "CURRENT", "Δ", "Δ%", "DIAGS (b→c)")
	fmt.Fprintln(w, strings.Repeat("-", 90))
	var totalB, totalC time.Duration
	for _, sd := range c.Stages {
		diagSummary := ""
		if sd.BaselineErrors != sd.CurrentErrors {
			diagSummary = fmt.Sprintf("err %d→%d", sd.BaselineErrors, sd.CurrentErrors)
		}
		if sd.BaselineWarns != sd.CurrentWarns {
			if diagSummary != "" {
				diagSummary += ", "
			}
			diagSummary += fmt.Sprintf("warn %d→%d", sd.BaselineWarns, sd.CurrentWarns)
		}
		fmt.Fprintf(w, "%-9s  %12s  %12s  %+12s  %+7.1f%%  %s\n",
			sd.Name,
			fmtMS(sd.BaselineMS), fmtMS(sd.CurrentMS),
			fmtMSSigned(sd.DeltaMS), sd.DeltaPercent,
			diagSummary)
		totalB += time.Duration(sd.BaselineMS * float64(time.Millisecond))
		totalC += time.Duration(sd.CurrentMS * float64(time.Millisecond))
	}
	fmt.Fprintln(w, strings.Repeat("-", 90))
	totalDelta := totalC - totalB
	totalPct := 0.0
	if totalB > 0 {
		totalPct = 100 * float64(totalDelta) / float64(totalB)
	}
	fmt.Fprintf(w, "%-9s  %12s  %12s  %+12s  %+7.1f%%\n",
		"TOTAL",
		fmtMS(float64(totalB.Microseconds())/1000),
		fmtMS(float64(totalC.Microseconds())/1000),
		fmtMSSigned(float64(totalDelta.Microseconds())/1000),
		totalPct)

	// Diagnostic delta — only print codes that changed.
	type codeRow struct {
		code string
		b, c int
	}
	var rows []codeRow
	for k, v := range c.DiagDelta {
		if v[0] != v[1] {
			rows = append(rows, codeRow{k, v[0], v[1]})
		}
	}
	if len(rows) > 0 {
		sort.Slice(rows, func(i, j int) bool { return rows[i].code < rows[j].code })
		fmt.Fprintln(w)
		fmt.Fprintln(w, "diagnostic count changes:")
		for _, r := range rows {
			fmt.Fprintf(w, "  %-12s  %d → %d  (Δ %+d)\n", r.code, r.b, r.c, r.c-r.b)
		}
	}
}

func fmtMS(ms float64) string {
	if ms < 1 {
		return fmt.Sprintf("%.1fµs", ms*1000)
	}
	return fmt.Sprintf("%.2fms", ms)
}

func fmtMSSigned(ms float64) string {
	if ms == 0 {
		return "0"
	}
	if ms > 0 {
		return "+" + fmtMS(ms)
	}
	return "-" + fmtMS(-ms)
}
