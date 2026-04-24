// Package airepair orchestrates AI-oriented source adaptation for Osty.
//
// The current v1 implementation chains a conservative lexical phase with a
// small structural adaptation phase, then measures front-end diagnostics
// before and after each accepted rewrite so callers can decide whether to
// adopt the candidate source.
package airepair

import (
	"bytes"
	"strings"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/token"
)

type phaseFunc func(src []byte, diags []*diag.Diagnostic) repair.Result

// Mode describes which acceptance heuristics the caller cares about.
type Mode string

const (
	// ModeAutoAssist prioritizes parser success first, then resolve/check
	// quality. This is the default because AI-authored mixed syntax is most
	// useful when the source becomes front-end-runnable at all.
	ModeAutoAssist Mode = "auto"
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
	AcceptedReason    string
	RejectedReason    string
	PassesUsed        int
	DiagnosticsBefore []*diag.Diagnostic
	DiagnosticsAfter  []*diag.Diagnostic
	Before            ProbeStats
	After             ProbeStats
}

// Report is the JSON-friendly form of Result for CLI and tooling consumers.
type Report struct {
	Filename             string             `json:"filename"`
	Mode                 Mode               `json:"mode"`
	Status               ReportStatus       `json:"status"`
	Changed              bool               `json:"changed"`
	Improved             bool               `json:"improved"`
	Accepted             bool               `json:"accepted"`
	AcceptedReason       string             `json:"accepted_reason,omitempty"`
	RejectedReason       string             `json:"rejected_reason,omitempty"`
	PassesUsed           int                `json:"passes_used"`
	Skipped              int                `json:"skipped"`
	Changes              []repair.Change    `json:"changes"`
	ChangeDetails        []ReportChange     `json:"change_details"`
	Summary              ReportSummary      `json:"summary"`
	ResidualPrimaryCode  string             `json:"residual_primary_code,omitempty"`
	ResidualPrimaryHabit string             `json:"residual_primary_habit,omitempty"`
	Before               ProbeStats         `json:"before"`
	After                ProbeStats         `json:"after"`
	DiagnosticsBefore    []*diag.Diagnostic `json:"diagnostics_before"`
	DiagnosticsAfter     []*diag.Diagnostic `json:"diagnostics_after"`
	Source               string             `json:"source"`
}

// ReportChange adds airepair-specific metadata to a user-facing change.
type ReportChange struct {
	Kind        string    `json:"kind"`
	Message     string    `json:"message"`
	Pos         token.Pos `json:"pos"`
	Phase       string    `json:"phase"`
	SourceHabit string    `json:"source_habit"`
	Confidence  float64   `json:"confidence"`
}

// Analyze runs the current airepair pipeline against one source blob.
func Analyze(req Request) Result {
	mode := req.Mode
	if mode == "" {
		mode = ModeAutoAssist
	}

	original := append([]byte(nil), req.Source...)
	beforeStats, beforeDiags := probe(original)
	currentSource := original
	currentStats := beforeStats
	currentDiags := beforeDiags
	combined := repair.Result{Source: currentSource}
	proposed := false
	passesUsed := 0
	rejectedReason := ""

	phases := []phaseFunc{
		func(src []byte, _ []*diag.Diagnostic) repair.Result { return repair.Source(src) },
		func(src []byte, _ []*diag.Diagnostic) repair.Result { return structuralSource(src) },
		diagnosticGuidedSource,
		diagnosticForeignLoopSource,
		diagnosticTupleLoopSource,
		diagnosticSemanticSource,
		func(src []byte, _ []*diag.Diagnostic) repair.Result { return parserCanonicalSource(src) },
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
			if rejectedReason == "" {
				rejectedReason = explainRejectedReason(mode, currentStats, nextStats)
			}
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
	acceptedReason := ""
	if accepted {
		acceptedReason = explainAcceptedReason(mode, beforeStats, currentStats, combined, proposed)
	} else if rejectedReason == "" {
		rejectedReason = "no_improving_candidate"
	}

	return Result{
		Filename:          req.Filename,
		Mode:              mode,
		Original:          original,
		Repaired:          currentSource,
		Repair:            combined,
		Changed:           !bytes.Equal(original, currentSource),
		Improved:          improved,
		Accepted:          accepted,
		AcceptedReason:    acceptedReason,
		RejectedReason:    rejectedReason,
		PassesUsed:        passesUsed,
		DiagnosticsBefore: beforeDiags,
		DiagnosticsAfter:  currentDiags,
		Before:            beforeStats,
		After:             currentStats,
	}
}

// JSONReport returns a serialization-oriented view of Result.
func (r Result) JSONReport() Report {
	details := make([]ReportChange, 0, len(r.Repair.Changes))
	for _, change := range r.Repair.Changes {
		details = append(details, annotateReportChange(change))
	}
	return Report{
		Filename:             r.Filename,
		Mode:                 r.Mode,
		Status:               r.ReportStatus(),
		Changed:              r.Changed,
		Improved:             r.Improved,
		Accepted:             r.Accepted,
		AcceptedReason:       r.AcceptedReason,
		RejectedReason:       r.RejectedReason,
		PassesUsed:           r.PassesUsed,
		Skipped:              r.Repair.Skipped,
		Changes:              append([]repair.Change(nil), r.Repair.Changes...),
		ChangeDetails:        details,
		Summary:              r.ReportSummary(),
		ResidualPrimaryCode:  r.ResidualPrimaryCode(),
		ResidualPrimaryHabit: r.ResidualPrimaryHabit(),
		Before:               r.Before,
		After:                r.After,
		DiagnosticsBefore:    append([]*diag.Diagnostic(nil), r.DiagnosticsBefore...),
		DiagnosticsAfter:     append([]*diag.Diagnostic(nil), r.DiagnosticsAfter...),
		Source:               string(r.Repaired),
	}
}

func explainAcceptedReason(mode Mode, before, after ProbeStats, repaired repair.Result, proposed bool) string {
	if !proposed || (!repairedChanged(repaired) && before == after) {
		return "already_clean"
	}
	if after.Parse.Errors < before.Parse.Errors {
		return "parse_errors_reduced"
	}
	if after.Resolve.Errors < before.Resolve.Errors {
		return "resolve_errors_reduced"
	}
	if after.Check.Errors < before.Check.Errors {
		return "check_errors_reduced"
	}
	if after.TotalWarnings < before.TotalWarnings {
		return "warnings_reduced"
	}
	if mode == ModeRewriteOnly && len(repaired.Changes) > 0 {
		return "rewrite_mode_applied"
	}
	if len(repaired.Changes) > 0 {
		return "non_regressing_rewrite_accepted"
	}
	return "accepted"
}

func explainRejectedReason(mode Mode, before, after ProbeStats) string {
	if after.Parse.Errors > before.Parse.Errors {
		return "parse_regression_blocked"
	}
	if mode == ModeAutoAssist && after.Resolve.Errors > before.Resolve.Errors {
		return "resolve_regression_blocked"
	}
	if mode == ModeAutoAssist && after.Check.Errors > before.Check.Errors {
		return "check_regression_blocked"
	}
	if after.TotalErrors > before.TotalErrors {
		return "front_end_regression_blocked"
	}
	if after.TotalWarnings > before.TotalWarnings {
		return "warning_regression_blocked"
	}
	return "no_improvement"
}

func repairedChanged(repaired repair.Result) bool {
	return len(repaired.Changes) > 0 || repaired.Skipped > 0
}

func (r Result) ResidualPrimaryCode() string {
	if r.After.TotalErrors == 0 {
		return ""
	}
	for _, d := range r.DiagnosticsAfter {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		if d.Code != "" {
			return d.Code
		}
	}
	return "(uncoded)"
}

func (r Result) ResidualPrimaryHabit() string {
	if r.After.TotalErrors == 0 {
		return ""
	}
	for i := len(r.Repair.Changes) - 1; i >= 0; i-- {
		meta := reportChangeMetaForKind(r.Repair.Changes[i].Kind)
		if meta.sourceHabit != "" && meta.sourceHabit != "unknown" {
			return meta.sourceHabit
		}
	}
	switch {
	case bytes.Contains(r.Repaired, []byte(".enumerate()")):
		return "python_enumerate_loop"
	case bytes.Contains(r.Repaired, []byte("append(")):
		return "foreign_append_helper"
	case bytes.Contains(r.Repaired, []byte("len(")):
		return "foreign_len_helper"
	case bytes.Contains(r.Repaired, []byte(".length")):
		return "javascript_length_property"
	default:
		return ""
	}
}

func probe(src []byte) (ProbeStats, []*diag.Diagnostic) {
	// Phase 1c.4: drive the self-host checker directly. selfhost.CheckFromSource
	// merges resolver + checker diagnostics into one CheckResult, so we split
	// them back apart by diag code prefix (E05xx = resolve, everything else =
	// check) to preserve the per-stage count contract ProbeStats exposes to
	// accept/reject heuristics in Analyze().
	//
	// The E05xx = resolve contract is enforced at the codebase level: CLAUDE.md
	// namespaces E0500-E0599 to name-resolution errors, and internal/diag/codes.go
	// tracks every one. A resolve-phase diagnostic emitted outside that band
	// would already break other tooling, so relying on the prefix here doesn't
	// add a new fragility axis.
	parseDiags, checked := selfhost.CheckFromSource(src)
	convertedCheck := selfhost.CheckDiagnosticsAsDiag(src, checked.Diagnostics)

	resolveDiags := make([]*diag.Diagnostic, 0, len(convertedCheck))
	checkDiags := make([]*diag.Diagnostic, 0, len(convertedCheck))
	for _, d := range convertedCheck {
		if d == nil {
			continue
		}
		if strings.HasPrefix(d.Code, "E05") {
			resolveDiags = append(resolveDiags, d)
		} else {
			checkDiags = append(checkDiags, d)
		}
	}

	stats := ProbeStats{
		Parse:   count(parseDiags),
		Resolve: count(resolveDiags),
		Check:   count(checkDiags),
	}
	stats.TotalErrors = stats.Parse.Errors + stats.Resolve.Errors + stats.Check.Errors
	stats.TotalWarnings = stats.Parse.Warnings + stats.Resolve.Warnings + stats.Check.Warnings

	all := make([]*diag.Diagnostic, 0, len(parseDiags)+len(convertedCheck))
	all = append(all, parseDiags...)
	all = append(all, convertedCheck...)
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
	case ModeFrontEndAssist:
		return compareFrontEndAssist(before, after) > 0
	default:
		return compareAutoAssist(before, after) > 0
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
	case ModeFrontEndAssist:
		return compareFrontEndAssist(before, after) >= 0
	default:
		return compareAutoAssist(before, after) >= 0
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

func compareAutoAssist(before, after ProbeStats) int {
	if after.Parse.Errors != before.Parse.Errors {
		if after.Parse.Errors < before.Parse.Errors {
			return 1
		}
		return -1
	}
	if after.Resolve.Errors != before.Resolve.Errors {
		if after.Resolve.Errors < before.Resolve.Errors {
			return 1
		}
		return -1
	}
	if after.Check.Errors != before.Check.Errors {
		if after.Check.Errors < before.Check.Errors {
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
