// Package airepair orchestrates AI-oriented source adaptation for Osty.
//
// The current v1 implementation chains a conservative lexical phase with a
// small structural adaptation phase, then measures front-end diagnostics
// before and after each accepted rewrite so callers can decide whether to
// adopt the candidate source.
package airepair

import (
	"bytes"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type phaseFunc func(src []byte, diags []*diag.Diagnostic) repair.Result

// Mode describes which acceptance heuristics the caller cares about.
type Mode string

const (
	// ModeRewriteOnly runs the lexical repair phase and reports its result
	// without treating front-end metrics as the primary objective.
	ModeRewriteOnly Mode = "rewrite"
	// ModeParseAssist prioritizes reducing parser diagnostics.
	ModeParseAssist Mode = "parse"
	// ModeFrontEndAssist prioritizes reducing parser/resolve/check diagnostics.
	ModeFrontEndAssist Mode = "frontend"
)

// Request configures one airepair run.
type Request struct {
	Source   []byte
	Filename string
	Mode     Mode
	// MaxPasses limits how many airepair phases may run. Zero means
	// "run the full currently-enabled pipeline".
	MaxPasses int
}

// StageStats is the diagnostic count for one front-end stage.
type StageStats struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// ProbeStats summarizes parser/resolve/check diagnostics for one source.
type ProbeStats struct {
	Parse         StageStats `json:"parse"`
	Resolve       StageStats `json:"resolve"`
	Check         StageStats `json:"check"`
	TotalErrors   int        `json:"total_errors"`
	TotalWarnings int        `json:"total_warnings"`
}

// Result is the full output of one airepair run.
type Result struct {
	Filename          string
	Mode              Mode
	Original          []byte
	Repaired          []byte
	Repair            repair.Result
	Changed           bool
	Improved          bool
	Accepted          bool
	PassesUsed        int
	DiagnosticsBefore []*diag.Diagnostic
	DiagnosticsAfter  []*diag.Diagnostic
	Before            ProbeStats
	After             ProbeStats
}

// Report is the JSON-friendly form of Result for CLI and tooling consumers.
type Report struct {
	Filename          string             `json:"filename"`
	Mode              Mode               `json:"mode"`
	Changed           bool               `json:"changed"`
	Improved          bool               `json:"improved"`
	Accepted          bool               `json:"accepted"`
	PassesUsed        int                `json:"passes_used"`
	Skipped           int                `json:"skipped"`
	Changes           []repair.Change    `json:"changes"`
	Before            ProbeStats         `json:"before"`
	After             ProbeStats         `json:"after"`
	DiagnosticsBefore []*diag.Diagnostic `json:"diagnostics_before"`
	DiagnosticsAfter  []*diag.Diagnostic `json:"diagnostics_after"`
	Source            string             `json:"source"`
}

// Analyze runs the current airepair pipeline against one source blob.
func Analyze(req Request) Result {
	mode := req.Mode
	if mode == "" {
		mode = ModeFrontEndAssist
	}

	original := append([]byte(nil), req.Source...)
	beforeStats, beforeDiags := probe(original)
	currentSource := original
	currentStats := beforeStats
	currentDiags := beforeDiags
	combined := repair.Result{Source: currentSource}
	proposed := false
	passesUsed := 0

	phases := []phaseFunc{
		func(src []byte, _ []*diag.Diagnostic) repair.Result { return repair.Source(src) },
		func(src []byte, _ []*diag.Diagnostic) repair.Result { return structuralSource(src) },
		diagnosticGuidedSource,
	}
	if req.MaxPasses <= 0 || req.MaxPasses > len(phases) {
		req.MaxPasses = len(phases)
	}
	if req.MaxPasses < len(phases) {
		phases = phases[:req.MaxPasses]
	}

	for _, phase := range phases {
		candidate := phase(currentSource, currentDiags)
		if len(candidate.Changes) == 0 && candidate.Skipped == 0 {
			continue
		}
		proposed = true
		nextStats, nextDiags := probe(candidate.Source)
		if !isAccepted(mode, currentStats, nextStats, candidate) {
			continue
		}
		currentSource = append([]byte(nil), candidate.Source...)
		currentStats = nextStats
		currentDiags = nextDiags
		combined = mergeRepairResults(combined, candidate)
		passesUsed++
	}

	combined.Source = currentSource
	improved := isImproved(mode, beforeStats, currentStats, combined)
	accepted := !proposed || passesUsed > 0

	return Result{
		Filename:          req.Filename,
		Mode:              mode,
		Original:          original,
		Repaired:          currentSource,
		Repair:            combined,
		Changed:           !bytes.Equal(original, currentSource),
		Improved:          improved,
		Accepted:          accepted,
		PassesUsed:        passesUsed,
		DiagnosticsBefore: beforeDiags,
		DiagnosticsAfter:  currentDiags,
		Before:            beforeStats,
		After:             currentStats,
	}
}

// JSONReport returns a serialization-oriented view of Result.
func (r Result) JSONReport() Report {
	return Report{
		Filename:          r.Filename,
		Mode:              r.Mode,
		Changed:           r.Changed,
		Improved:          r.Improved,
		Accepted:          r.Accepted,
		PassesUsed:        r.PassesUsed,
		Skipped:           r.Repair.Skipped,
		Changes:           append([]repair.Change(nil), r.Repair.Changes...),
		Before:            r.Before,
		After:             r.After,
		DiagnosticsBefore: append([]*diag.Diagnostic(nil), r.DiagnosticsBefore...),
		DiagnosticsAfter:  append([]*diag.Diagnostic(nil), r.DiagnosticsAfter...),
		Source:            string(r.Repaired),
	}
}

func probe(src []byte) (ProbeStats, []*diag.Diagnostic) {
	file, parseDiags := parser.ParseDiagnostics(src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	chk := check.File(file, res, checkOptsForSource(src))

	stats := ProbeStats{
		Parse:   count(parseDiags),
		Resolve: count(res.Diags),
		Check:   count(chk.Diags),
	}
	stats.TotalErrors = stats.Parse.Errors + stats.Resolve.Errors + stats.Check.Errors
	stats.TotalWarnings = stats.Parse.Warnings + stats.Resolve.Warnings + stats.Check.Warnings

	all := make([]*diag.Diagnostic, 0, len(parseDiags)+len(res.Diags)+len(chk.Diags))
	all = append(all, parseDiags...)
	all = append(all, res.Diags...)
	all = append(all, chk.Diags...)
	return stats, all
}

func count(diags []*diag.Diagnostic) StageStats {
	var out StageStats
	for _, d := range diags {
		switch d.Severity {
		case diag.Error:
			out.Errors++
		case diag.Warning:
			out.Warnings++
		}
	}
	return out
}

func isImproved(mode Mode, before, after ProbeStats, repaired repair.Result) bool {
	switch mode {
	case ModeRewriteOnly:
		return len(repaired.Changes) > 0
	case ModeParseAssist:
		return compareParseAssist(before, after) > 0
	default:
		return compareFrontEndAssist(before, after) > 0
	}
}

func isAccepted(mode Mode, before, after ProbeStats, repaired repair.Result) bool {
	if len(repaired.Changes) == 0 && repaired.Skipped == 0 {
		return true
	}
	switch mode {
	case ModeRewriteOnly:
		return true
	case ModeParseAssist:
		return compareParseAssist(before, after) >= 0
	default:
		return compareFrontEndAssist(before, after) >= 0
	}
}

// compare* returns:
//
//	 1 when after is better than before
//	 0 when they are equivalent for that mode
//	-1 when after regresses the source for that mode
func compareParseAssist(before, after ProbeStats) int {
	if after.Parse.Errors != before.Parse.Errors {
		if after.Parse.Errors < before.Parse.Errors {
			return 1
		}
		return -1
	}
	if after.TotalErrors != before.TotalErrors {
		if after.TotalErrors < before.TotalErrors {
			return 1
		}
		return -1
	}
	if after.TotalWarnings != before.TotalWarnings {
		if after.TotalWarnings < before.TotalWarnings {
			return 1
		}
		return -1
	}
	return 0
}

func compareFrontEndAssist(before, after ProbeStats) int {
	if after.TotalErrors != before.TotalErrors {
		if after.TotalErrors < before.TotalErrors {
			return 1
		}
		return -1
	}
	if after.Parse.Errors != before.Parse.Errors {
		if after.Parse.Errors < before.Parse.Errors {
			return 1
		}
		return -1
	}
	if after.TotalWarnings != before.TotalWarnings {
		if after.TotalWarnings < before.TotalWarnings {
			return 1
		}
		return -1
	}
	return 0
}

func checkOptsForSource(src []byte) check.Opts {
	reg := stdlib.LoadCached()
	return check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        src,
	}
}

func mergeRepairResults(base, next repair.Result) repair.Result {
	out := base
	out.Source = next.Source
	out.Changes = append(out.Changes, next.Changes...)
	out.Skipped += next.Skipped
	return out
}
