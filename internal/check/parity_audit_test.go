package check_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestCheckPackageParityAudit dumps every error-severity diagnostic
// the in-process Go check.Package emits on toolchain/ so we can
// compare against `osty check toolchain/` (which uses the native
// checker boundary). Skipped unless OSTY_CHECK_PARITY_AUDIT=1.
//
// Discrepancy is currently 14 diags vs 0 — figuring out which paths
// disagree is the prerequisite for unblocking ~3,146 toolchain MIR
// functions whose lowering trips on these in-process errors.
func TestCheckPackageParityAudit(t *testing.T) {
	if os.Getenv("OSTY_CHECK_PARITY_AUDIT") == "" {
		t.Skip("set OSTY_CHECK_PARITY_AUDIT=1 to run")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	pkgDir := filepath.Join(repoRoot, "toolchain")
	paths, err := resolve.PackageSourcePaths(pkgDir, false)
	if err != nil {
		t.Fatalf("collect toolchain paths: %v", err)
	}
	pkg, err := resolve.LoadPackageFiles(paths, stdlib.LoadCached())
	if err != nil {
		t.Fatalf("load toolchain: %v", err)
	}
	res := resolve.ResolvePackageDefault(pkg)
	reg := stdlib.LoadCached()
	chk := check.Package(pkg, res, check.Opts{
		Stdlib:             reg,
		Primitives:         reg.Primitives,
		ResultMethods:      reg.ResultMethods,
	})

	type diagRow struct {
		code string
		msg  string
		loc  string
	}
	rows := []diagRow{}
	codeTally := map[string]int{}
	for _, d := range chk.Diags {
		if d.Severity != diag.Error {
			continue
		}
		loc := ""
		if len(d.Spans) > 0 {
			loc = fmt.Sprintf("srcID=%v:line=%d", d.Spans[0].Span.SourceFileID, d.Spans[0].Span.Start.Line)
		}
		rows = append(rows, diagRow{
			code: string(d.Code),
			msg:  d.Message,
			loc:  loc,
		})
		codeTally[string(d.Code)]++
	}

	t.Logf("check.Package on toolchain/: %d error-severity diagnostics", len(rows))

	// Per-code histogram for quick triage.
	type bucket struct {
		code  string
		count int
	}
	buckets := make([]bucket, 0, len(codeTally))
	for c, n := range codeTally {
		buckets = append(buckets, bucket{c, n})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].count != buckets[j].count {
			return buckets[i].count > buckets[j].count
		}
		return buckets[i].code < buckets[j].code
	})
	t.Logf("by code:")
	for _, b := range buckets {
		t.Logf("  %4d  %s", b.count, b.code)
	}

	// Dump all diagnostics so we can compare against `osty check`.
	t.Logf("---- all diagnostics ----")
	for i, r := range rows {
		t.Logf("[%d] %-8s %-50s @ %s", i, r.code, r.msg, r.loc)
	}
}
