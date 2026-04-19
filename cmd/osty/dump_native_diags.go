package main

// Helpers for `osty check --dump-native-diags`. The flag surfaces the
// bootstrapped native checker's per-context error histogram so callers
// working against aggregate error counts (1700-style summaries) can
// split the tail by diagnostic code without regenerating
// internal/selfhost/generated.go.
//
// Telemetry origin: internal/selfhost/check_telemetry.go buckets the
// typed checker's structured CheckDiagnostic stream by stable code and
// suffixes each detail row with `@Lnn:Cnn` drawn from the diagnostic's
// primary span. host_boundary.go plumbs the resulting maps into
// check.Result.NativeCheckerTelemetry.

import (
	"fmt"
	"os"
	"sort"

	"github.com/osty/osty/internal/check"
)

// dumpNativeDiagsFor prints the native checker's per-context histogram
// for one already-checked scope (file, package, or workspace entry) to
// stderr. Silent when the native checker was unavailable or reported
// zero errors — no output is always preferable to a misleading "0 of 0"
// banner.
func dumpNativeDiagsFor(label string, chk *check.Result) {
	if chk == nil || chk.NativeCheckerTelemetry == nil {
		return
	}
	t := chk.NativeCheckerTelemetry
	if t.Errors == 0 && t.Assignments == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "native checker telemetry: %s\n", label)
	fmt.Fprintf(os.Stderr, "  assignments: %d\n", t.Assignments)
	fmt.Fprintf(os.Stderr, "  accepted:    %d\n", t.Accepted)
	fmt.Fprintf(os.Stderr, "  tail:        %d (= assignments - accepted)\n", t.Assignments-t.Accepted)
	fmt.Fprintf(os.Stderr, "  errors:      %d\n", t.Errors)
	if len(t.ErrorsByContext) == 0 {
		fmt.Fprintf(os.Stderr, "  (no per-context breakdown available)\n")
		return
	}
	type row struct {
		name  string
		count int
	}
	rows := make([]row, 0, len(t.ErrorsByContext))
	total := 0
	for name, count := range t.ErrorsByContext {
		rows = append(rows, row{name: name, count: count})
		total += count
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].name < rows[j].name
	})
	fmt.Fprintf(os.Stderr, "  by context (total %d):\n", total)
	for _, r := range rows {
		fmt.Fprintf(os.Stderr, "    %6d  %s\n", r.count, r.name)
		writeContextDetail(os.Stderr, t.ErrorDetails[r.name])
	}
}

// writeContextDetail prints the top-10 detail rows under a context bucket,
// indented so they visually nest under the parent. No output when the
// bucket is empty — a diagnostic whose primary span is unresolved (or
// missing) yields a bare message with no `@Lnn:Cnn` suffix, and diagnostic
// severities other than error never land here to begin with.
func writeContextDetail(w *os.File, details map[string]int) {
	if len(details) == 0 {
		return
	}
	type row struct {
		name  string
		count int
	}
	rows := make([]row, 0, len(details))
	for name, count := range details {
		rows = append(rows, row{name: name, count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].name < rows[j].name
	})
	limit := 10
	if len(rows) < limit {
		limit = len(rows)
	}
	for i := 0; i < limit; i++ {
		fmt.Fprintf(w, "             %5d  %s\n", rows[i].count, rows[i].name)
	}
	if len(rows) > limit {
		remaining := 0
		for _, r := range rows[limit:] {
			remaining += r.count
		}
		fmt.Fprintf(w, "             %5d  (+ %d more)\n", remaining, len(rows)-limit)
	}
}
