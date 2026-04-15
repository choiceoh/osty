// Package pipeline runs the full Osty front-end (lex → parse → resolve →
// check → lint) over a single source buffer and records per-phase
// timing + output stats. It is the engine behind the `osty pipeline`
// subcommand and the `--trace` global flag — both want the same
// numbers, just rendered differently.
//
// Single-file mode only. The package isn't trying to subsume the
// manifest-driven build orchestrator; for multi-file packages call
// resolve.LoadPackage / check.Package directly.
package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// Stage records what happened during one pipeline phase. Output is a
// human-readable one-line summary of what the phase produced (e.g.
// "1234 tokens", "42 decls, 89 stmts"); machine consumers should read
// the typed counts instead.
type Stage struct {
	Name     string        // "lex", "parse", "resolve", "check", "lint"
	Duration time.Duration // wall-clock time spent in the phase
	Output   string        // human-readable summary of what came out
	Errors   int           // diagnostic count at Severity == Error
	Warnings int           // diagnostic count at Severity == Warning
	// Counts holds the per-phase metric values keyed by metric name.
	// JSON output uses these. Kept open-ended so individual phases can
	// add more dimensions without reshaping every consumer.
	Counts map[string]int
}

// Result bundles the stage stats with the actual artefacts produced
// along the way. Callers that just want the table can ignore the
// pointers; callers that want to do something with the AST / resolve
// result (e.g. the existing single-file subcommands when --trace is
// set) read them directly.
type Result struct {
	Stages   []Stage
	Tokens   []token.Token
	File     *ast.File
	Resolve  *resolve.Result
	Check    *check.Result
	Lint     *lint.Result
	AllDiags []*diag.Diagnostic // every diag from every phase, in phase order
}

// Run executes lex → parse → resolve → check → lint over src and
// returns a fully populated Result. Phases that depend on a previous
// phase still run even if the previous phase produced errors — the
// front-end is designed to keep going so users see as many problems
// per invocation as possible. The same diagnostic ordering is preserved
// in AllDiags.
//
// stream, if non-nil, receives one human-readable trace line per
// completed phase (used by the --trace global flag). Pass nil to
// suppress streaming output.
func Run(src []byte, stream io.Writer) Result {
	var r Result
	emit := func(s Stage) {
		r.Stages = append(r.Stages, s)
		if stream != nil {
			fmt.Fprintf(stream, "trace: %-8s %8s   %s\n",
				s.Name, formatDuration(s.Duration), traceTail(s))
		}
	}

	// --- lex ---
	t0 := time.Now()
	l := lexer.New(src)
	toks := l.Lex()
	lexDiags := l.Errors()
	r.Tokens = toks
	r.AllDiags = append(r.AllDiags, lexDiags...)
	emit(Stage{
		Name:     "lex",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d tokens", len(toks)),
		Errors:   countSeverity(lexDiags, diag.Error),
		Warnings: countSeverity(lexDiags, diag.Warning),
		Counts:   map[string]int{"tokens": len(toks)},
	})

	// --- parse ---
	t0 = time.Now()
	file, parseDiags := parser.ParseDiagnostics(src)
	r.File = file
	r.AllDiags = append(r.AllDiags, parseDiags...)
	declCount, stmtCount, useCount := 0, 0, 0
	if file != nil {
		declCount = len(file.Decls)
		stmtCount = len(file.Stmts)
		useCount = len(file.Uses)
	}
	emit(Stage{
		Name:     "parse",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d decls, %d top-level stmts, %d use", declCount, stmtCount, useCount),
		Errors:   countSeverity(parseDiags, diag.Error),
		Warnings: countSeverity(parseDiags, diag.Warning),
		Counts: map[string]int{
			"decls": declCount,
			"stmts": stmtCount,
			"uses":  useCount,
		},
	})

	// --- resolve ---
	t0 = time.Now()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	r.Resolve = res
	r.AllDiags = append(r.AllDiags, res.Diags...)
	emit(Stage{
		Name:     "resolve",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d refs, %d type refs", len(res.Refs), len(res.TypeRefs)),
		Errors:   countSeverity(res.Diags, diag.Error),
		Warnings: countSeverity(res.Diags, diag.Warning),
		Counts: map[string]int{
			"refs":      len(res.Refs),
			"type_refs": len(res.TypeRefs),
		},
	})

	// --- check ---
	t0 = time.Now()
	chk := check.File(file, res, check.Opts{Primitives: stdlib.LoadCached().Primitives})
	r.Check = chk
	r.AllDiags = append(r.AllDiags, chk.Diags...)
	typedExprs := 0
	for _, t := range chk.Types {
		if !types.IsError(t) {
			typedExprs++
		}
	}
	emit(Stage{
		Name:     "check",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d typed exprs, %d let bindings", typedExprs, len(chk.LetTypes)),
		Errors:   countSeverity(chk.Diags, diag.Error),
		Warnings: countSeverity(chk.Diags, diag.Warning),
		Counts: map[string]int{
			"typed_exprs":  typedExprs,
			"let_types":    len(chk.LetTypes),
			"sym_types":    len(chk.SymTypes),
			"instantiate":  len(chk.Instantiations),
		},
	})

	// --- lint ---
	t0 = time.Now()
	lr := lint.File(file, res, chk)
	r.Lint = lr
	r.AllDiags = append(r.AllDiags, lr.Diags...)
	emit(Stage{
		Name:     "lint",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d lint findings", len(lr.Diags)),
		Errors:   countSeverity(lr.Diags, diag.Error),
		Warnings: countSeverity(lr.Diags, diag.Warning),
		Counts:   map[string]int{"findings": len(lr.Diags)},
	})

	return r
}

// RenderText writes a fixed-width table summarising the run to w.
// Always finishes with a totals row.
func (r Result) RenderText(w io.Writer) {
	fmt.Fprintf(w, "%-8s  %10s  %-50s  %6s  %6s\n",
		"PHASE", "TIME", "OUTPUT", "ERR", "WARN")
	fmt.Fprintln(w, strings.Repeat("-", 90))
	var total time.Duration
	var totalErr, totalWarn int
	for _, s := range r.Stages {
		fmt.Fprintf(w, "%-8s  %10s  %-50s  %6d  %6d\n",
			s.Name, formatDuration(s.Duration), truncate(s.Output, 50),
			s.Errors, s.Warnings)
		total += s.Duration
		totalErr += s.Errors
		totalWarn += s.Warnings
	}
	fmt.Fprintln(w, strings.Repeat("-", 90))
	fmt.Fprintf(w, "%-8s  %10s  %-50s  %6d  %6d\n",
		"TOTAL", formatDuration(total), "", totalErr, totalWarn)

	// Diagnostic-code histogram — useful when one phase reports lots
	// of findings and you want to know which code dominates.
	if len(r.AllDiags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "diagnostics by code:")
		hist := map[string]int{}
		for _, d := range r.AllDiags {
			c := d.Code
			if c == "" {
				c = "(no code)"
			}
			hist[c]++
		}
		keys := make([]string, 0, len(hist))
		for k := range hist {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s  %d\n", k, hist[k])
		}
	}
}

// RenderJSON writes a machine-readable summary. Schema is intentionally
// flat: top-level keys are "stages" (array) and "diagnostics_by_code"
// (object). Stages preserve their per-metric Counts map verbatim so
// downstream tooling can pick fields without re-parsing the human Output.
func (r Result) RenderJSON(w io.Writer) error {
	type stageJSON struct {
		Name        string         `json:"name"`
		DurationMS  float64        `json:"duration_ms"`
		Output      string         `json:"output"`
		Errors      int            `json:"errors"`
		Warnings    int            `json:"warnings"`
		Counts      map[string]int `json:"counts,omitempty"`
	}
	stages := make([]stageJSON, len(r.Stages))
	for i, s := range r.Stages {
		stages[i] = stageJSON{
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
	out := struct {
		Stages           []stageJSON    `json:"stages"`
		DiagnosticsByCode map[string]int `json:"diagnostics_by_code"`
	}{Stages: stages, DiagnosticsByCode: hist}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ---- helpers ----

func countSeverity(ds []*diag.Diagnostic, sev diag.Severity) int {
	n := 0
	for _, d := range ds {
		if d.Severity == sev {
			n++
		}
	}
	return n
}

// formatDuration prints durations with a unit appropriate to their
// magnitude — micros for sub-millisecond phases, millis otherwise.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000.0)
	case d < time.Second:
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return s[:n-1] + "…"
}

func traceTail(s Stage) string {
	tail := s.Output
	if s.Errors > 0 || s.Warnings > 0 {
		tail = fmt.Sprintf("%s  [err=%d warn=%d]", tail, s.Errors, s.Warnings)
	}
	return tail
}
