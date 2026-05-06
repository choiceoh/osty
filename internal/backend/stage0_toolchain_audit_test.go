package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
)

// TestStage0ToolchainAudit (skipped in CI) walks every function in
// toolchain/ through stage0 independently and tallies decline reasons.
// Run manually:
//
//	OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/
//
// Output: histogram of stage0 decline reasons sorted by frequency.
// Set OSTY_STAGE0_AUDIT_SAMPLES=1 additionally to dump the first
// declined function in each top-N bucket so we can pattern-match a
// new matcher.
//
// Lets us pick the highest-yield next phase.
func TestStage0ToolchainAudit(t *testing.T) {
	if os.Getenv("OSTY_STAGE0_AUDIT") == "" {
		t.Skip("set OSTY_STAGE0_AUDIT=1 to run audit")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Tests run from internal/backend/, walk up to repo root then into toolchain/.
	repoRoot := filepath.Dir(filepath.Dir(wd))
	pkgDir := filepath.Join(repoRoot, "toolchain")
	pkg, err := resolve.LoadPackageForNative(pkgDir)
	if err != nil {
		t.Fatalf("load toolchain: %v", err)
	}
	chk := check.Package(pkg, nil)
	checkErrors := 0
	for _, d := range chk.Diags {
		if d.Severity == 0 { // Error
			checkErrors++
		}
	}
	t.Logf("check.Package reported %d error-severity diagnostics", checkErrors)

	entry, err := PreparePackage("toolchain", filepath.Join(pkgDir, "main.osty"), pkg, nil, chk)
	if err != nil {
		// MIR lowering may be partial — keep going if Entry.MIR is set.
		t.Logf("prepare returned error (may be partial MIR): %v", err)
	}
	if entry.MIR == nil {
		t.Fatal("nil MIR — front-end could not lower any function")
	}

	// Aggregate MIR-lowerer issues by message prefix so we can see which
	// front-end gaps dominate the UnreachableTerm-only fns.
	issueTally := map[string]int{}
	for _, iss := range entry.MIR.Issues {
		key := iss.Error()
		// strip trailing "<error>" / type detail for bucketing
		if idx := strings.LastIndex(key, ":"); idx > 0 {
			key = key[:idx]
		}
		issueTally[key]++
	}
	type ibucket struct {
		key   string
		count int
	}
	ibs := make([]ibucket, 0, len(issueTally))
	for k, v := range issueTally {
		ibs = append(ibs, ibucket{k, v})
	}
	sort.Slice(ibs, func(i, j int) bool { return ibs[i].count > ibs[j].count })
	t.Logf("MIR-lowerer issues (top 10):")
	for i, b := range ibs {
		if i >= 10 {
			break
		}
		t.Logf("  %4d  %s", b.count, b.key)
	}

	const topBucketsToShow = 30
	const sampleTopN = 5

	// Reuse one Module shell + main stub across the audit loop. stage0
	// only reads the module, so swapping `Functions` per-fn avoids
	// re-allocating a Module struct (and a fresh main stub) per
	// iteration.
	auditModule := *entry.MIR
	mainStub := newSyntheticEmptyMain()

	type bucket struct {
		key   string
		count int
	}
	tally := map[string]int{}
	// One sample function captured per fingerprint key — populated in
	// the same pass so the AUDIT_SAMPLES path doesn't re-emit IR for
	// every function a second time.
	samples := map[string]*mir.Function{}
	totalFns := 0
	covered := 0
	skipped := 0
	for _, fn := range entry.MIR.Functions {
		if fn == nil || fn.IsExternal || fn.IsIntrinsic {
			continue
		}
		// Skip functions whose front-end MIR lowering failed (every
		// block ends with UnreachableTerm and instructions are likely
		// stub-only). These are MIR-lowerer gaps, not stage0 surface
		// gaps — they would never reach stage0 with real production
		// bodies.
		if fnHasOnlyUnreachable(fn) {
			skipped++
			continue
		}
		totalFns++
		auditModule.Functions = []*mir.Function{fn, mainStub}
		_, err := stage0.EmitMIR(&auditModule, llvmabi.Options{PackageName: "audit"})
		if err == nil {
			covered++
			continue
		}
		key := normalizeDeclineReason(err.Error())
		if key == "no matching pattern" {
			key = fingerprintFn(fn)
		}
		tally[key]++
		if _, seen := samples[key]; !seen {
			samples[key] = fn
		}
	}
	t.Logf("(skipped %d functions whose front-end MIR is UnreachableTerm-only)", skipped)
	t.Logf("toolchain stage0 audit: %d / %d functions covered (%.1f%%)",
		covered, totalFns, 100.0*float64(covered)/float64(totalFns))

	buckets := make([]bucket, 0, len(tally))
	for k, v := range tally {
		buckets = append(buckets, bucket{k, v})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].count != buckets[j].count {
			return buckets[i].count > buckets[j].count
		}
		return buckets[i].key < buckets[j].key
	})
	t.Logf("decline reasons (top %d):", topBucketsToShow)
	for i, b := range buckets {
		if i >= topBucketsToShow {
			break
		}
		t.Logf("  %4d  %s", b.count, b.key)
	}

	if os.Getenv("OSTY_STAGE0_AUDIT_SAMPLES") != "" {
		for i := 0; i < sampleTopN && i < len(buckets); i++ {
			fn := samples[buckets[i].key]
			if fn == nil {
				continue
			}
			t.Logf("---- sample %q [%s] ----", fn.Name, buckets[i].key)
			t.Logf("  ReturnLocal=%d Params=%v", fn.ReturnLocal, fn.Params)
			for _, l := range fn.Locals {
				if l == nil {
					continue
				}
				t.Logf("  local#%d name=%q isParam=%v type=%T %v", l.ID, l.Name, l.IsParam, l.Type, l.Type)
			}
			for _, bb := range fn.Blocks {
				if bb == nil {
					continue
				}
				t.Logf("  bb id=%d term=%T instrs=%d", bb.ID, bb.Term, len(bb.Instrs))
				for _, instr := range bb.Instrs {
					t.Logf("    %T", instr)
				}
			}
		}
	}
}

// normalizeDeclineReason strips function-name and other variable parts
// of the decline error so similar shapes bucket together. Audit-only
// brittleness — the parser is tightly coupled to stage0's current
// error message format. If stage0 grows a structured Reason field,
// drop the substring scan in favor of errors.As.
func normalizeDeclineReason(s string) string {
	// Pattern: `stage0: MIR shape outside bootstrap subset: function "NAME": REASON`
	// or:      `stage0: MIR shape outside bootstrap subset: function "NAME" does not match any stage0 pattern`
	const prefix = "stage0: MIR shape outside bootstrap subset: function "
	if !strings.HasPrefix(s, prefix) {
		return s
	}
	rest := s[len(prefix):]
	if !strings.HasPrefix(rest, `"`) {
		return rest
	}
	idx := strings.Index(rest[1:], `"`)
	if idx < 0 {
		return rest
	}
	rest = rest[1+idx+1:]
	rest = strings.TrimPrefix(rest, ":")
	rest = strings.TrimSpace(rest)
	if rest == "does not match any stage0 pattern" {
		return "no matching pattern"
	}
	return rest
}

// fingerprintFn classifies a declined function by structural shape so
// "no matching pattern" buckets into actionable groups.
func fingerprintFn(fn *mir.Function) string {
	retClass := "void"
	if fn.ReturnLocal >= 0 {
		for _, l := range fn.Locals {
			if l != nil && l.ID == fn.ReturnLocal {
				retClass = classifyType(l.Type)
				break
			}
		}
	}
	totalInstrs := 0
	var hasCall, hasIntrinsic, hasAggregate, hasFieldRead, hasFieldWrite, hasSwitchInt bool
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		totalInstrs += len(bb.Instrs)
		if _, ok := bb.Term.(*mir.SwitchIntTerm); ok {
			hasSwitchInt = true
		}
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.AssignInstr:
				if step.Dest.HasProjections() {
					hasFieldWrite = true
				}
				if _, ok := step.Src.(*mir.AggregateRV); ok {
					hasAggregate = true
				}
				if use, ok := step.Src.(*mir.UseRV); ok {
					if cp, ok := use.Op.(*mir.CopyOp); ok && cp.Place.HasProjections() {
						hasFieldRead = true
					}
				}
				if bin, ok := step.Src.(*mir.BinaryRV); ok {
					if cp, ok := bin.Left.(*mir.CopyOp); ok && cp.Place.HasProjections() {
						hasFieldRead = true
					}
					if cp, ok := bin.Right.(*mir.CopyOp); ok && cp.Place.HasProjections() {
						hasFieldRead = true
					}
				}
			case *mir.CallInstr:
				hasCall = true
			case *mir.IntrinsicInstr:
				hasIntrinsic = true
			}
		}
	}
	feats := []string{}
	if hasCall {
		feats = append(feats, "call")
	}
	if hasIntrinsic {
		feats = append(feats, "intr")
	}
	if hasAggregate {
		feats = append(feats, "agg")
	}
	if hasFieldRead {
		feats = append(feats, "fr")
	}
	if hasFieldWrite {
		feats = append(feats, "fw")
	}
	if hasSwitchInt {
		feats = append(feats, "switch")
	}
	if len(feats) == 0 {
		feats = append(feats, "plain")
	}
	return fmt.Sprintf("blocks=%-2d params=%d ret=%-9s instrs=%-3d feats=%s",
		len(fn.Blocks), len(fn.Params), retClass, totalInstrs, strings.Join(feats, ","))
}

func classifyType(t mir.Type) string {
	if t == nil {
		return "nil"
	}
	// PrimType / NamedType / OptionalType / FnType all implement
	// Stringer; defer to their canonical spellings rather than
	// re-implementing the dispatch here.
	switch t.(type) {
	case *ir.PrimType, *ir.NamedType, *ir.OptionalType, *ir.FnType:
		return fmt.Sprint(t)
	}
	return fmt.Sprintf("%T", t)
}

// fnHasOnlyUnreachable reports whether every block in `fn` is
// terminated with UnreachableTerm — front-end gave up lowering.
func fnHasOnlyUnreachable(fn *mir.Function) bool {
	if len(fn.Blocks) == 0 {
		return false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, ok := bb.Term.(*mir.UnreachableTerm); !ok {
			return false
		}
	}
	return true
}

// newSyntheticEmptyMain returns a fresh `fn main() {}` MIR fixture so
// stage0.EmitMIR's "module has no main" guard passes when the audit
// is testing a single non-main function in isolation.
func newSyntheticEmptyMain() *mir.Function {
	return &mir.Function{
		Name:        "main",
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: nil},
		},
		Blocks: []*mir.BasicBlock{
			{ID: 0, Term: &mir.ReturnTerm{}},
		},
	}
}
