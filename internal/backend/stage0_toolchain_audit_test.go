package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestStage0ToolchainAudit (skipped in CI) walks every function in
// toolchain/ through stage0 independently and tallies decline reasons.
// Run manually:
//
//	go test -run TestStage0ToolchainAudit -v ./internal/backend/
//
// Output: histogram of stage0 decline reasons sorted by frequency.
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
	paths, err := resolve.PackageSourcePaths(pkgDir, false)
	if err != nil {
		t.Fatalf("collect toolchain paths: %v", err)
	}
	reg := stdlib.LoadCached()
	pkg, err := resolve.LoadPackageFiles(paths, reg)
	if err != nil {
		t.Fatalf("load toolchain: %v", err)
	}
	res := resolve.ResolvePackageDefault(pkg)
	chk := check.Package(pkg, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
	})

	entry, err := PreparePackage("toolchain", filepath.Join(pkgDir, "main.osty"), pkg, nil, chk)
	if err != nil {
		// MIR lowering may be partial — keep going if Entry.MIR is set.
		t.Logf("prepare returned error (may be partial MIR): %v", err)
	}
	if entry.MIR == nil {
		t.Fatal("nil MIR — front-end could not lower any function")
	}

	type bucket struct {
		key   string
		count int
	}
	tally := map[string]int{}
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
		oneFn := *entry.MIR
		oneFn.Functions = []*mir.Function{fn, syntheticEmptyMain()}
		_, err := stage0.EmitMIR(&oneFn, llvmabi.Options{PackageName: "audit"})
		if err == nil {
			covered++
			continue
		}
		key := normalizeDeclineReason(err.Error())
		if key == "no matching pattern" {
			key = fingerprintFn(fn)
		}
		tally[key]++
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
	t.Logf("decline reasons (top 30):")
	max := 30
	for i, b := range buckets {
		if i >= max {
			break
		}
		t.Logf("  %4d  %s", b.count, b.key)
	}

	// Pick the first declined function in the top 3 buckets and print
	// its detailed shape so we can pattern-match a new matcher.
	if os.Getenv("OSTY_STAGE0_AUDIT_SAMPLES") != "" {
		sampleLimit := 5
		if raw := os.Getenv("OSTY_STAGE0_AUDIT_SAMPLE_LIMIT"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
				sampleLimit = parsed
			}
		}
		topBuckets := map[string]bool{}
		for i := 0; i < sampleLimit && i < len(buckets); i++ {
			topBuckets[buckets[i].key] = true
		}
		seen := map[string]bool{}
		for _, fn := range entry.MIR.Functions {
			if fn == nil || fn.IsExternal || fn.IsIntrinsic {
				continue
			}
			oneFn := *entry.MIR
			oneFn.Functions = []*mir.Function{fn, syntheticEmptyMain()}
			if _, err := stage0.EmitMIR(&oneFn, llvmabi.Options{PackageName: "audit"}); err == nil {
				continue
			}
			fp := fingerprintFn(fn)
			if !topBuckets[fp] || seen[fp] {
				continue
			}
			seen[fp] = true
			t.Logf("---- sample %q [%s] ----", fn.Name, fp)
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
					t.Logf("    %s", describeAuditInstr(instr))
				}
			}
		}
	}
}

func describeAuditInstr(instr mir.Instr) string {
	switch x := instr.(type) {
	case *mir.AssignInstr:
		return fmt.Sprintf("%T dest=%s src=%T", instr, describeAuditPlace(x.Dest), x.Src)
	case *mir.CallInstr:
		callee := fmt.Sprintf("%T", x.Callee)
		if ref, ok := x.Callee.(*mir.FnRef); ok {
			callee = fmt.Sprintf("FnRef{%s type=%s}", ref.Symbol, classifyType(ref.Type))
		}
		dest := "<nil>"
		if x.Dest != nil {
			dest = describeAuditPlace(*x.Dest)
		}
		return fmt.Sprintf("%T dest=%s callee=%s args=%s", instr, dest, callee, describeAuditOperands(x.Args))
	case *mir.IntrinsicInstr:
		dest := "<nil>"
		if x.Dest != nil {
			dest = describeAuditPlace(*x.Dest)
		}
		return fmt.Sprintf("%T dest=%s kind=%s args=%s", instr, dest, x.Kind.String(), describeAuditOperands(x.Args))
	default:
		return fmt.Sprintf("%T", instr)
	}
}

func describeAuditPlace(p mir.Place) string {
	if p.HasProjections() {
		return fmt.Sprintf("local#%d+proj", p.Local)
	}
	return fmt.Sprintf("local#%d", p.Local)
}

func describeAuditOperands(ops []mir.Operand) string {
	if len(ops) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, describeAuditOperand(op))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func describeAuditOperand(op mir.Operand) string {
	switch x := op.(type) {
	case *mir.CopyOp:
		return fmt.Sprintf("Copy(%s type=%s)", describeAuditPlace(x.Place), classifyType(x.Type()))
	case *mir.ConstOp:
		return fmt.Sprintf("Const(%T type=%s)", x.Const, classifyType(x.Type()))
	default:
		return fmt.Sprintf("%T type=%s", op, classifyType(op.Type()))
	}
}

// normalizeDeclineReason strips function-name and other variable parts
// of the decline error so similar shapes bucket together.
func normalizeDeclineReason(s string) string {
	// Pattern: `stage0: MIR shape outside bootstrap subset: function "NAME": REASON`
	// or:      `stage0: MIR shape outside bootstrap subset: function "NAME" does not match any stage0 pattern`
	const prefix = "stage0: MIR shape outside bootstrap subset: function "
	if !strings.HasPrefix(s, prefix) {
		return s
	}
	rest := s[len(prefix):]
	// strip the quoted name
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
// "no matching pattern" buckets into actionable groups (e.g. "5-block
// chained-if returning struct" → P17 candidate).
func fingerprintFn(fn *mir.Function) string {
	nBlocks := len(fn.Blocks)
	nParams := len(fn.Params)
	hasReturn := fn.ReturnLocal >= 0
	retClass := "void"
	if hasReturn {
		for _, l := range fn.Locals {
			if l == nil {
				continue
			}
			if l.ID == fn.ReturnLocal {
				retClass = classifyType(l.Type)
				break
			}
		}
	}
	totalInstrs := 0
	hasCall := false
	hasIntrinsic := false
	hasAggregate := false
	hasFieldRead := false
	hasFieldWrite := false
	hasMatch := false
	hasSwitchInt := false
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		totalInstrs += len(bb.Instrs)
		switch bb.Term.(type) {
		case *mir.SwitchIntTerm:
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
				_ = step
			}
		}
	}
	_ = hasMatch
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
		nBlocks, nParams, retClass, totalInstrs, strings.Join(feats, ","))
}

func classifyType(t mir.Type) string {
	if t == nil {
		return "nil"
	}
	switch ty := t.(type) {
	case *ir.PrimType:
		return ty.String()
	case *ir.NamedType:
		return "named:" + ty.Name
	case *ir.OptionalType:
		return "opt"
	case *ir.FnType:
		return "fn"
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

func syntheticEmptyMain() *mir.Function {
	// Reuse mir.Function constructor — we just need a fn named "main"
	// with one block + ReturnTerm so stage0 doesn't reject "no main".
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

// fmt-import side import (kept so this file compiles when only using mir).
var _ = fmt.Sprintf
