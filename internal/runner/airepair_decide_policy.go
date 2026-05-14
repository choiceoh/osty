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
