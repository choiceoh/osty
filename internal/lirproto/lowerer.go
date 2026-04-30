package lirproto

import (
	"fmt"

	"github.com/osty/osty/internal/mir"
)

// DiagnosticSeverity classifies whether a diagnostic blocks use of the result.
type DiagnosticSeverity string

const (
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

// DiagnosticKind classifies where a lowering diagnostic came from.
type DiagnosticKind string

const (
	DiagUnsupported  DiagnosticKind = "unsupported"
	DiagInvalidInput DiagnosticKind = "invalid_input"
	DiagLoweringBug  DiagnosticKind = "lowering_bug"
	DiagValidation   DiagnosticKind = "validation"
)

// Diagnostic is the structured issue surface for MIR -> LIR Proto lowering.
type Diagnostic struct {
	Severity DiagnosticSeverity
	Kind     DiagnosticKind
	Message  string
	Function string
	Block    string
}

// LowerResult is the complete result of one Lowerer run.
type LowerResult struct {
	Module      Module
	Diagnostics []Diagnostic
}

// OK reports whether the result has no error-severity diagnostics.
func (r LowerResult) OK() bool {
	return !r.HasErrors()
}

// HasErrors reports whether any diagnostic blocks use of the result.
func (r LowerResult) HasErrors() bool {
	for _, diag := range r.Diagnostics {
		if diag.Severity == SeverityError {
			return true
		}
	}
	return false
}

// HasUnsupported reports whether lowering hit a not-yet-covered MIR shape.
func (r LowerResult) HasUnsupported() bool {
	for _, diag := range r.Diagnostics {
		if diag.Kind == DiagUnsupported {
			return true
		}
	}
	return false
}

// Lowerer owns the mutable state for one MIR -> LIR Proto lowering run. It is
// reusable; each LowerMIR call resets per-run state while keeping the config.
type Lowerer struct {
	cfg   Config
	diags []Diagnostic
}

// NewLowerer creates a MIR -> LIR Proto lowerer.
func NewLowerer(cfg Config) *Lowerer {
	return &Lowerer{cfg: cloneConfig(cfg)}
}

// Config returns the lowerer's immutable config snapshot.
func (l *Lowerer) Config() Config {
	if l == nil {
		return Config{}
	}
	return cloneConfig(l.cfg)
}

// LowerMIR lowers a validated MIR module into LIR Proto. The first Phase-2
// slice covers single-block scalar functions and reports structured
// diagnostics for wider MIR shapes.
func (l *Lowerer) LowerMIR(mod *mir.Module) LowerResult {
	if l == nil {
		l = NewLowerer(Config{})
	}
	l.reset()

	out := Module{
		PackageName: l.cfg.PackageName,
		SourcePath:  l.cfg.SourcePath,
		Target:      l.cfg.Target,
	}
	if mod == nil {
		l.error(DiagInvalidInput, "nil MIR module", "", "")
		return LowerResult{Module: out, Diagnostics: append([]Diagnostic(nil), l.diags...)}
	}
	if out.PackageName == "" {
		out.PackageName = mod.Package
	}

	for _, issue := range mod.Issues {
		if issue == nil {
			continue
		}
		l.warn(DiagInvalidInput, issue.Error(), "", "")
	}
	l.lowerMIRModule(&out, mod)
	for _, err := range Validate(out) {
		l.error(DiagValidation, err.Error(), "", "")
	}
	return LowerResult{Module: out, Diagnostics: append([]Diagnostic(nil), l.diags...)}
}

func (l *Lowerer) reset() {
	l.diags = l.diags[:0]
}

func (l *Lowerer) warn(kind DiagnosticKind, msg, fn, block string) {
	l.diag(SeverityWarning, kind, msg, fn, block)
}

func (l *Lowerer) error(kind DiagnosticKind, msg, fn, block string) {
	l.diag(SeverityError, kind, msg, fn, block)
}

func (l *Lowerer) diag(sev DiagnosticSeverity, kind DiagnosticKind, msg, fn, block string) {
	if msg == "" {
		msg = fmt.Sprintf("%s diagnostic", kind)
	}
	l.diags = append(l.diags, Diagnostic{
		Severity: sev,
		Kind:     kind,
		Message:  msg,
		Function: fn,
		Block:    block,
	})
}

func cloneConfig(cfg Config) Config {
	out := cfg
	if cfg.FeatureGate != nil {
		out.FeatureGate = make(map[string]bool, len(cfg.FeatureGate))
		for k, v := range cfg.FeatureGate {
			out.FeatureGate[k] = v
		}
	}
	return out
}
