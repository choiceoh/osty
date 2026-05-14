// airepair_decide_policy.go is the Go snapshot of
// toolchain/airepair_decide.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

// ProbeStageStats mirrors toolchain/airepair_decide.osty's
// ProbeStageStats — per-stage diagnostic counts.
//
// Osty: toolchain/airepair_decide.osty:17
type ProbeStageStats struct {
	Errors   int
	Warnings int
}

// ProbeStats mirrors toolchain/airepair_decide.osty's ProbeStats
// — per-source pipeline snapshot the accept/reject scorers
// compare.
//
// Osty: toolchain/airepair_decide.osty:25
type ProbeStats struct {
	Parse         ProbeStageStats
	Resolve       ProbeStageStats
	Check         ProbeStageStats
	TotalErrors   int
	TotalWarnings int
}

// CompareParseAssist returns +1 when `after` improves on `before`
// for parse-only assist, -1 on regression, 0 on tie. Priority:
// parse errors → total errors → total warnings.
//
// Osty: toolchain/airepair_decide.osty:45
func CompareParseAssist(before, after ProbeStats) int {
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

// CompareAutoAssist scores default mode: parse → resolve → check
// → total warnings.
//
// Osty: toolchain/airepair_decide.osty:71
func CompareAutoAssist(before, after ProbeStats) int {
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

// CompareFrontEndAssist scores front-end mode: total errors → parse
// errors → total warnings.
//
// Osty: toolchain/airepair_decide.osty:99
func CompareFrontEndAssist(before, after ProbeStats) int {
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

// RepairedChanged reports whether the lexical repair phase did
// anything observable. Either an applied edit OR a skipped-but-
// attempted edit counts.
//
// Osty: toolchain/airepair_decide.osty:125
func RepairedChanged(changeCount, skipCount int) bool {
	return changeCount > 0 || skipCount > 0
}

// Airepair mode names — match `--airepair-mode` flag values.
const (
	airepairModeRewriteOnly   = "rewrite"
	airepairModeAutoAssist    = "auto"
	airepairModeParseAssist   = "parse"
	airepairModeFrontEndAssist = "frontend"
)

// IsImproved reports whether `after` is a strict improvement on
// `before` under the chosen mode. Defaults to auto-assist for
// unrecognised modes.
//
// Osty: toolchain/airepair_decide.osty:138
func IsImproved(mode string, before, after ProbeStats, repairedChanges int) bool {
	switch mode {
	case airepairModeRewriteOnly:
		return repairedChanges > 0
	case airepairModeParseAssist:
		return CompareParseAssist(before, after) > 0
	case airepairModeFrontEndAssist:
		return CompareFrontEndAssist(before, after) > 0
	}
	return CompareAutoAssist(before, after) > 0
}

// IsAccepted is the looser sibling of IsImproved. No edits + no
// skips short-circuits to true (nothing was tried). Rewrite mode
// always accepts when edits exist. Otherwise the mode's score
// ladder must be non-negative.
//
// Osty: toolchain/airepair_decide.osty:158
func IsAccepted(mode string, before, after ProbeStats, repairedChanges, repairedSkipped int) bool {
	if repairedChanges == 0 && repairedSkipped == 0 {
		return true
	}
	switch mode {
	case airepairModeRewriteOnly:
		return true
	case airepairModeParseAssist:
		return CompareParseAssist(before, after) >= 0
	case airepairModeFrontEndAssist:
		return CompareFrontEndAssist(before, after) >= 0
	}
	return CompareAutoAssist(before, after) >= 0
}

// ExplainAcceptedReason returns a stable string tag classifying
// why the rewriter's output was accepted. Tags are part of the
// public airepair JSON contract.
//
// Osty: toolchain/airepair_decide.osty:179
func ExplainAcceptedReason(mode string, before, after ProbeStats, repairedChanges int, proposed bool) string {
	changed := repairedChanges > 0
	beforeEqAfter := airepairProbeStatsEqual(before, after)
	if !proposed || (!changed && beforeEqAfter) {
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
	if mode == airepairModeRewriteOnly && repairedChanges > 0 {
		return "rewrite_mode_applied"
	}
	if repairedChanges > 0 {
		return "non_regressing_rewrite_accepted"
	}
	return "accepted"
}

// ExplainRejectedReason classifies why an output failed isAccepted
// under the given mode. Tags are part of the public airepair JSON
// contract.
//
// Osty: toolchain/airepair_decide.osty:213
func ExplainRejectedReason(mode string, before, after ProbeStats) string {
	if after.Parse.Errors > before.Parse.Errors {
		return "parse_regression_blocked"
	}
	if mode == airepairModeAutoAssist && after.Resolve.Errors > before.Resolve.Errors {
		return "resolve_regression_blocked"
	}
	if mode == airepairModeAutoAssist && after.Check.Errors > before.Check.Errors {
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

func airepairProbeStatsEqual(a, b ProbeStats) bool {
	return a.Parse == b.Parse &&
		a.Resolve == b.Resolve &&
		a.Check == b.Check &&
		a.TotalErrors == b.TotalErrors &&
		a.TotalWarnings == b.TotalWarnings
}
