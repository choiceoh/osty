package airepair

// ReportStatus classifies the current airepair result for tooling.
type ReportStatus string

const (
	ReportStatusClean            ReportStatus = "clean"
	ReportStatusRepairedClean    ReportStatus = "repaired_clean"
	ReportStatusResidual         ReportStatus = "residual"
	ReportStatusRepairedResidual ReportStatus = "repaired_residual"
)

// ReportSummary is a compact machine-friendly error delta summary.
type ReportSummary struct {
	ParseErrorsReduced    int `json:"parse_errors_reduced"`
	ResolveErrorsReduced  int `json:"resolve_errors_reduced"`
	CheckErrorsReduced    int `json:"check_errors_reduced"`
	TotalErrorsReduced    int `json:"total_errors_reduced"`
	ResidualParseErrors   int `json:"residual_parse_errors"`
	ResidualResolveErrors int `json:"residual_resolve_errors"`
	ResidualCheckErrors   int `json:"residual_check_errors"`
	ResidualErrors        int `json:"residual_errors"`
}

// ReportStatus returns the high-level airepair outcome bucket.
func (r Result) ReportStatus() ReportStatus {
	if r.After.TotalErrors == 0 {
		if r.Changed {
			return ReportStatusRepairedClean
		}
		return ReportStatusClean
	}
	if r.Changed {
		return ReportStatusRepairedResidual
	}
	return ReportStatusResidual
}

// ReportSummary returns a machine-friendly before/after error delta summary.
func (r Result) ReportSummary() ReportSummary {
	return ReportSummary{
		ParseErrorsReduced:    maxZero(r.Before.Parse.Errors - r.After.Parse.Errors),
		ResolveErrorsReduced:  maxZero(r.Before.Resolve.Errors - r.After.Resolve.Errors),
		CheckErrorsReduced:    maxZero(r.Before.Check.Errors - r.After.Check.Errors),
		TotalErrorsReduced:    maxZero(r.Before.TotalErrors - r.After.TotalErrors),
		ResidualParseErrors:   r.After.Parse.Errors,
		ResidualResolveErrors: r.After.Resolve.Errors,
		ResidualCheckErrors:   r.After.Check.Errors,
		ResidualErrors:        r.After.TotalErrors,
	}
}

func maxZero(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
