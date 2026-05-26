package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	for _, issue := range entry.MIRIssues {
		if strings.Contains(issue.Error(), "for-in over non-List") {
			t.Logf("MIR for-in issue: %v", issue)
		}
	}
	if entry.MIR == nil {
		t.Fatal("nil MIR — front-end could not lower any function")
	}

	type bucket struct {
		key   string
		count int
	}
	tally := map[string]int{}
	stubTally := map[string]int{}
	stubSamples := map[string][]string{}
	emitBrokenTally := map[string]int{}
	emitBrokenSamples := map[string]string{} // first sample per fingerprint
	emitBrokenFunctions := []string{}        // names that emitted but clang rejected
	// declineCategories accumulates one root-cause tag per declined
	// function so the audit summary can report PR-planning buckets
	// alongside the raw shape histogram. See
	// internal/backend/stage0/decline_classifier.go.
	declineCategories := []stage0.DeclineCategory{}
	declineCategoryFns := map[stage0.DeclineCategory][]string{}
	totalFns := 0
	covered := 0
	emitBroken := 0
	skipped := 0
	stubCovered := 0
	realCovered := 0
	filterFn := os.Getenv("OSTY_STAGE0_AUDIT_FN")
	logStubDeclines := os.Getenv("OSTY_STAGE0_AUDIT_STUBS") != ""
	logStubSamples := os.Getenv("OSTY_STAGE0_AUDIT_STUB_SAMPLES") != ""
	stubSampleLimit := 5
	if raw := os.Getenv("OSTY_STAGE0_AUDIT_STUB_SAMPLE_LIMIT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			stubSampleLimit = parsed
		}
	}
	stubSamplesLogged := 0
	clangVerify := stage0AuditClangVerifyEnabled()
	clangPath := ""
	clangTmpDir := ""
	if clangVerify {
		if p, err := exec.LookPath("clang"); err == nil {
			clangPath = p
		} else {
			t.Logf("OSTY_STAGE0_AUDIT_CLANG_VERIFY=1 set but clang not found in PATH (%v); skipping IR validation", err)
			clangVerify = false
		}
		if clangVerify {
			tmp, err := os.MkdirTemp("", "stage0-audit-clang-*")
			if err != nil {
				t.Logf("OSTY_STAGE0_AUDIT_CLANG_VERIFY=1 set but TempDir failed (%v); skipping IR validation", err)
				clangVerify = false
			} else {
				clangTmpDir = tmp
				defer os.RemoveAll(clangTmpDir)
			}
		}
	}
	for _, fn := range entry.MIR.Functions {
		if fn == nil || fn.IsExternal || fn.IsIntrinsic {
			continue
		}
		if filterFn != "" && fn.Name != filterFn {
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
		// When auditing the toolchain's own `main` function, don't
		// inject a synthetic `main` alongside — LLVM rejects two
		// `define i32 @main()` blocks with `invalid redefinition of
		// function 'main'`. The real `main` already satisfies stage0's
		// `module has no main` guard. For every other function, inject
		// the synthetic main so stage0 doesn't decline the module.
		if fn.Name == "main" {
			oneFn.Functions = []*mir.Function{fn}
		} else {
			oneFn.Functions = []*mir.Function{fn, syntheticEmptyMain()}
		}
		irBytes, err := stage0.EmitMIR(&oneFn, llvmabi.Options{PackageName: "audit"})
		if err == nil {
			if clangVerify {
				if reason := runStage0AuditClangVerify(clangPath, clangTmpDir, fn.Name, irBytes); reason != "" {
					emitBroken++
					key := normalizeClangDiagnostic(reason)
					emitBrokenTally[key]++
					if _, seen := emitBrokenSamples[key]; !seen {
						emitBrokenSamples[key] = reason
					}
					emitBrokenFunctions = append(emitBrokenFunctions, fn.Name)
					continue
				}
			}
			covered++
			realCovered++
			continue
		}
		if irBytes, ok := stage0AuditDeclineStubCovered(&oneFn, fn.Name, llvmabi.Options{PackageName: "audit"}); ok {
			if clangVerify {
				if reason := runStage0AuditClangVerify(clangPath, clangTmpDir, fn.Name, irBytes); reason != "" {
					emitBroken++
					key := normalizeClangDiagnostic(reason)
					emitBrokenTally[key]++
					if _, seen := emitBrokenSamples[key]; !seen {
						emitBrokenSamples[key] = reason
					}
					emitBrokenFunctions = append(emitBrokenFunctions, fn.Name)
					continue
				}
			}
			covered++
			stubCovered++
			if logStubDeclines {
				key := normalizeDeclineReason(err.Error())
				if key == "no matching pattern" {
					key = fingerprintFn(fn)
				}
				stubTally[key]++
				if len(stubSamples[key]) < 8 {
					stubSamples[key] = append(stubSamples[key], fn.Name)
				}
				if logStubSamples && stubSamplesLogged < stubSampleLimit {
					stubSamplesLogged++
					t.Logf("---- decline-stub sample %q [%s] ----", fn.Name, key)
					t.Logf("  decline=%v", err)
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
						t.Logf("  bb id=%d term=%T %s instrs=%d", bb.ID, bb.Term, describeAuditTerm(bb.Term), len(bb.Instrs))
						for _, instr := range bb.Instrs {
							t.Logf("    %s", describeAuditInstr(instr))
						}
					}
				}
			}
			continue
		}
		key := normalizeDeclineReason(err.Error())
		if key == "no matching pattern" {
			key = fingerprintFn(fn)
		}
		tally[key]++
		cat := stage0.ClassifyDecline(fn)
		declineCategories = append(declineCategories, cat)
		declineCategoryFns[cat] = append(declineCategoryFns[cat], fn.Name)
	}
	t.Logf("(skipped %d functions whose front-end MIR is UnreachableTerm-only)", skipped)

	if clangVerify {
		t.Logf("toolchain stage0 audit: %d / %d functions covered (%.1f%%) — %d real, %d decline-stub, %d emit-broken (clang rejected)",
			covered, totalFns, 100.0*float64(covered)/float64(totalFns), realCovered, stubCovered, emitBroken)
		if emitBroken > 0 {
			emitBuckets := make([]bucket, 0, len(emitBrokenTally))
			for k, v := range emitBrokenTally {
				emitBuckets = append(emitBuckets, bucket{k, v})
			}
			sort.Slice(emitBuckets, func(i, j int) bool {
				if emitBuckets[i].count != emitBuckets[j].count {
					return emitBuckets[i].count > emitBuckets[j].count
				}
				return emitBuckets[i].key < emitBuckets[j].key
			})
			t.Logf("emit-broken clang diagnostics (top 10):")
			for i, b := range emitBuckets {
				if i >= 10 {
					break
				}
				t.Logf("  %4d  %s", b.count, b.key)
				if sample := emitBrokenSamples[b.key]; sample != "" && sample != b.key {
					// Show one verbatim sample so the bucket is debuggable.
					trimmed := strings.TrimSpace(sample)
					if len(trimmed) > 400 {
						trimmed = trimmed[:400] + "…"
					}
					t.Logf("       sample: %s", trimmed)
				}
			}
			if len(emitBrokenFunctions) > 0 {
				limit := 20
				if len(emitBrokenFunctions) < limit {
					limit = len(emitBrokenFunctions)
				}
				t.Logf("emit-broken function names (first %d): %s", limit, strings.Join(emitBrokenFunctions[:limit], ", "))
			}
		}
	} else {
		t.Logf("toolchain stage0 audit: %d / %d functions covered (%.1f%%) — %d real, %d decline-stub",
			covered, totalFns, 100.0*float64(covered)/float64(totalFns), realCovered, stubCovered)
	}
	if logStubDeclines {
		stubBuckets := make([]bucket, 0, len(stubTally))
		for k, v := range stubTally {
			stubBuckets = append(stubBuckets, bucket{k, v})
		}
		sort.Slice(stubBuckets, func(i, j int) bool {
			if stubBuckets[i].count != stubBuckets[j].count {
				return stubBuckets[i].count > stubBuckets[j].count
			}
			return stubBuckets[i].key < stubBuckets[j].key
		})
		t.Logf("decline-stub reasons:")
		for _, b := range stubBuckets {
			t.Logf("  %4d  %s", b.count, b.key)
			if samples := stubSamples[b.key]; len(samples) > 0 {
				t.Logf("        samples: %s", strings.Join(samples, ", "))
			}
		}
	}

	// L2 (module-level link): emit the FULL toolchain module in one
	// shot, run clang on the combined IR. Catches bugs that only
	// manifest across function boundaries: prototype mismatches,
	// duplicate symbol declarations, type-mismatched cross-function
	// calls. Per-function L1 verification can't see these because
	// each function compiles in isolation.
	if stage0AuditModuleVerifyEnabled() {
		runStage0AuditModuleVerify(t, entry.MIR, clangPath)
	}

	// L3 (binary exec): compile the produced IR + the runtime to an
	// actual executable, run it with a minimal `--version` style
	// invocation, check exit code 0. Catches link-stage bugs missed
	// by `-c` alone (undefined runtime symbols, calling-convention
	// mismatches with the runtime ABI) plus immediate runtime
	// crashes on the trivial entry path.
	if stage0AuditExecVerifyEnabled() {
		runStage0AuditExecVerify(t, entry.MIR, clangPath)
	}

	// L4 (fixed-point): the produced binary should be able to do the
	// same job the source osty does. We don't run the full
	// install-self cycle (would take ~15 min); instead we run
	// progressively heavier subcommands against a tiny fixture file
	// and verify exit 0 + non-empty output:
	//
	//   - `osty parse <fixture>`     — lexer + parser
	//   - `osty check <fixture>`     — front-end type check
	//   - `osty pipeline <fixture>`  — full front-end pipeline
	//
	// Each phase exposes the next stratum of semantic correctness
	// the produced binary inherits (or doesn't) from stage0's lossy
	// lowering. Stops at the first failing phase so the deepest
	// working layer is reported. Full self-build (`osty build
	// toolchain/`) is the heaviest fixed-point check and is left
	// off this routine path — drive it manually with
	// `OSTY_STAGE0_AUDIT_FP_FULL=1` when worthwhile.
	if stage0AuditFixedPointVerifyEnabled() {
		runStage0AuditFixedPointVerify(t, entry.MIR, clangPath)
	}

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
	maxDeclines := 30
	if v := os.Getenv("OSTY_STAGE0_AUDIT_DECLINE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxDeclines = n
		}
	}
	t.Logf("decline reasons (top %d):", maxDeclines)
	for i, b := range buckets {
		if i >= maxDeclines {
			break
		}
		t.Logf("  %4d  %s", b.count, b.key)
	}

	// Per-category roll-up (PR-planning view). The classifier maps each
	// declined function to one root-cause bucket; the count is what
	// PR success deltas are measured against (per
	// docs/stage0_p24_scope.md §6 plan). Sample function names follow
	// so a fixture writer has a starting point.
	categoryBuckets := stage0.NewCategoryBuckets(declineCategories)
	t.Logf("decline categories (root-cause buckets):")
	for _, row := range categoryBuckets {
		t.Logf("  %4d  %s", row.Count, row.Category)
		fnames := declineCategoryFns[row.Category]
		if len(fnames) == 0 {
			continue
		}
		sampleLimit := 5
		if len(fnames) < sampleLimit {
			sampleLimit = len(fnames)
		}
		t.Logf("        samples: %s", strings.Join(fnames[:sampleLimit], ", "))
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
			if filterFn != "" && fn.Name != filterFn {
				continue
			}
			oneFn := *entry.MIR
			// Same redefinition guard as the main loop above —
			// auditing `main` itself must not inject a second
			// synthetic `main` into the module.
			if fn.Name == "main" {
				oneFn.Functions = []*mir.Function{fn}
			} else {
				oneFn.Functions = []*mir.Function{fn, syntheticEmptyMain()}
			}
			_, err := stage0.EmitMIR(&oneFn, llvmabi.Options{PackageName: "audit"})
			if err == nil {
				continue
			}
			fp := fingerprintFn(fn)
			if !topBuckets[fp] || seen[fp] {
				continue
			}
			seen[fp] = true
			t.Logf("---- sample %q [%s] ----", fn.Name, fp)
			t.Logf("  decline=%v", err)
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
				t.Logf("  bb id=%d term=%T %s instrs=%d", bb.ID, bb.Term, describeAuditTerm(bb.Term), len(bb.Instrs))
				for _, instr := range bb.Instrs {
					t.Logf("    %s", describeAuditInstr(instr))
				}
			}
		}
	}
}

func stage0AuditDeclineStubCovered(module *mir.Module, fnName string, opts llvmabi.Options) ([]byte, bool) {
	old, hadOld := os.LookupEnv(stage0.ListAllDeclinesEnv)
	if err := os.Setenv(stage0.ListAllDeclinesEnv, "1"); err != nil {
		return nil, false
	}
	defer func() {
		if hadOld {
			_ = os.Setenv(stage0.ListAllDeclinesEnv, old)
		} else {
			_ = os.Unsetenv(stage0.ListAllDeclinesEnv)
		}
	}()

	irBytes, err := stage0.EmitMIR(module, opts)
	if len(irBytes) == 0 {
		return nil, false
	}
	if err != nil && !errors.Is(err, stage0.ErrUnsupported) {
		return nil, false
	}
	irText := string(irBytes)
	if !strings.Contains(irText, "define ") || !strings.Contains(irText, "@"+fnName+"(") {
		return nil, false
	}
	if !strings.Contains(irText, "osty_rt_stage0_declined") {
		return nil, false
	}
	return irBytes, true
}

func describeAuditInstr(instr mir.Instr) string {
	switch x := instr.(type) {
	case *mir.AssignInstr:
		return fmt.Sprintf("%T dest=%s src=%s", instr, describeAuditPlace(x.Dest), describeAuditRValue(x.Src))
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

func describeAuditTerm(term mir.Terminator) string {
	switch x := term.(type) {
	case *mir.GotoTerm:
		return fmt.Sprintf("target=%d", x.Target)
	case *mir.BranchTerm:
		return fmt.Sprintf("then=%d else=%d cond=%s", x.Then, x.Else, describeAuditOperand(x.Cond))
	case *mir.SwitchIntTerm:
		var b strings.Builder
		for i, c := range x.Cases {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "%d->%d", c.Value, c.Target)
		}
		return fmt.Sprintf("default=%d scrutinee=%s cases=%s", x.Default, describeAuditOperand(x.Scrutinee), b.String())
	default:
		return ""
	}
}

func describeAuditRValue(rv mir.RValue) string {
	switch x := rv.(type) {
	case *mir.UseRV:
		return fmt.Sprintf("Use(%s)", describeAuditOperand(x.Op))
	case *mir.BinaryRV:
		return fmt.Sprintf("Binary(%s, %s, %s)", x.Op.String(), describeAuditOperand(x.Left), describeAuditOperand(x.Right))
	case *mir.UnaryRV:
		return fmt.Sprintf("Unary(%s, %s)", x.Op.String(), describeAuditOperand(x.Arg))
	case *mir.AggregateRV:
		return fmt.Sprintf("Aggregate(%s variant=%d fields=%s type=%s)", x.Kind.String(), x.VariantIdx, describeAuditOperands(x.Fields), classifyType(x.T))
	case *mir.LenRV:
		return fmt.Sprintf("Len(%s)", describeAuditPlace(x.Place))
	case *mir.DiscriminantRV:
		return fmt.Sprintf("Discriminant(%s)", describeAuditPlace(x.Place))
	case *mir.NullaryRV:
		return fmt.Sprintf("Nullary(%s type=%s)", x.Kind.String(), classifyType(x.T))
	default:
		return fmt.Sprintf("%T", rv)
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

// stage0AuditClangVerifyEnabled reports whether the audit should run
// each per-function emitted IR through `clang -c` to catch malformed
// LLVM IR that the audit-time matchers miss (e.g. `use of undefined
// value`, `expected type`, dropped instructions). Default: off, since
// it adds ~200ms per function (≈10 min for a 3000-fn toolchain).
func stage0AuditClangVerifyEnabled() bool {
	switch strings.TrimSpace(os.Getenv("OSTY_STAGE0_AUDIT_CLANG_VERIFY")) {
	case "1", "true", "TRUE", "True", "on", "ON", "On", "yes", "YES", "Yes":
		return true
	}
	return false
}

// runStage0AuditClangVerify writes the emitted IR to a per-function
// .ll file under tmpDir and runs `clang -c -o /dev/null <file>`.
// Returns "" on success or the captured stderr (truncated) when
// clang rejects the IR.
//
// We deliberately use `-c` (compile-only, no linking) so the audit
// catches IR-level bugs (malformed SSA, missing types) without
// dragging in runtime libraries. The synthetic `main` keeps stage0's
// `module has no main` guard happy; clang doesn't care about
// `main`'s body for a `-c` invocation.
func runStage0AuditClangVerify(clangPath, tmpDir, fnName string, ir []byte) string {
	if clangPath == "" || tmpDir == "" {
		return ""
	}
	// Sanitize filename — function names contain characters legal in
	// LLVM IR but illegal as filenames on some hosts.
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
			return r
		}
		return '_'
	}, fnName)
	if safe == "" {
		safe = "fn"
	}
	llPath := filepath.Join(tmpDir, safe+".ll")
	if err := os.WriteFile(llPath, ir, 0o600); err != nil {
		return fmt.Sprintf("write IR file: %v", err)
	}
	defer os.Remove(llPath)
	cmd := exec.Command(clangPath, "-c", "-o", os.DevNull, llPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	combined := strings.TrimSpace(string(out))
	if combined == "" {
		combined = err.Error()
	}
	return combined
}

// normalizeClangDiagnostic collapses per-call-site clang error
// messages into a stable bucket key so the audit histogram can group
// like-shaped failures. Strips file paths, line/column numbers, and
// per-symbol identifiers; keeps the diagnostic kind ("use of undefined
// value", "expected type", "instruction expected to be numbered",
// etc.).
func normalizeClangDiagnostic(diag string) string {
	for _, line := range strings.Split(diag, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "command:") {
			continue
		}
		// Lines look like: "<file>:<line>:<col>: error: <msg>"
		if idx := strings.Index(line, "error: "); idx >= 0 {
			msg := strings.TrimSpace(line[idx+len("error: "):])
			// Drop quoted identifiers / numbers so similar shapes
			// share a bucket. e.g. "'%11'" → "'<id>'".
			normalized := normalizeClangDiagIdentifiers(msg)
			return normalized
		}
		if strings.HasPrefix(line, "error: ") {
			return normalizeClangDiagIdentifiers(strings.TrimSpace(line[len("error: "):]))
		}
	}
	// Fallback: first non-empty line.
	for _, line := range strings.Split(diag, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return normalizeClangDiagIdentifiers(line)
		}
	}
	return "(empty)"
}

func normalizeClangDiagIdentifiers(msg string) string {
	// '%11' / '%foo' → '<id>'
	var b strings.Builder
	i := 0
	for i < len(msg) {
		c := msg[i]
		if c == '\'' {
			// Find matching close quote.
			j := i + 1
			for j < len(msg) && msg[j] != '\'' {
				j++
			}
			if j < len(msg) {
				b.WriteString("'<id>'")
				i = j + 1
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// stage0AuditModuleVerifyEnabled reports whether the audit should run
// `clang -c` on the FULL toolchain module (not per-function). Catches
// bugs visible only across function boundaries: cross-call type
// mismatches, duplicate `declare` lines, missing prototype
// declarations. Per-function `OSTY_STAGE0_AUDIT_CLANG_VERIFY` cannot
// see these because each function emits its own mini-module with
// fresh prototypes.
//
// Cost: one full `EmitMIR` + one `clang -c` invocation. Adds ~30s on
// this toolchain.
func stage0AuditModuleVerifyEnabled() bool {
	switch strings.TrimSpace(os.Getenv("OSTY_STAGE0_AUDIT_MODULE_VERIFY")) {
	case "1", "true", "TRUE", "True", "on", "ON", "On", "yes", "YES", "Yes":
		return true
	}
	return false
}

// stage0AuditExecVerifyEnabled reports whether the audit should link
// the produced IR + runtime objects into an executable and invoke it
// with `--selfhost-doctor` (the lightest of the stage0 binary's
// advertised subcommands). Adds link-stage bug detection (undefined
// symbols, calling-conv mismatches with the runtime ABI) and a
// smoke test for trivial runtime crashes. Stage0 today bottoms out
// at the CLI dispatch banner + process.abort; the audit accepts
// that ceiling and only flags an earlier crash.
//
// Cost: one full link + one process spawn. Adds ~10s on top of
// MODULE_VERIFY (and implies it).
func stage0AuditExecVerifyEnabled() bool {
	switch strings.TrimSpace(os.Getenv("OSTY_STAGE0_AUDIT_EXEC_VERIFY")) {
	case "1", "true", "TRUE", "True", "on", "ON", "On", "yes", "YES", "Yes":
		return true
	}
	return false
}

// runStage0AuditModuleVerify emits the entire toolchain module via
// stage0 (with declines silently dropped — `OSTY_STAGE0_LIST_ALL_DECLINES`
// semantics) and feeds the combined IR through `clang -c`. Reports
// any module-level diagnostics on the test logger.
func runStage0AuditModuleVerify(t *testing.T, module *mir.Module, clangPath string) {
	t.Helper()
	if module == nil {
		t.Logf("module-verify: skipped (nil MIR module)")
		return
	}
	if clangPath == "" {
		var err error
		clangPath, err = exec.LookPath("clang")
		if err != nil {
			t.Logf("module-verify: clang not found in PATH (%v); skipping", err)
			return
		}
	}
	// Emit with declines surfaced as a single aggregated error so we
	// know whether the module would even reach clang in production.
	prevListAll := os.Getenv("OSTY_STAGE0_LIST_ALL_DECLINES")
	defer os.Setenv("OSTY_STAGE0_LIST_ALL_DECLINES", prevListAll)
	os.Setenv("OSTY_STAGE0_LIST_ALL_DECLINES", "1")
	irBytes, err := stage0.EmitMIR(module, llvmabi.Options{PackageName: "audit-module"})
	if err != nil {
		// Aggregated declines come back with partial IR (EmitMIR
		// returns the bytes alongside the error under
		// OSTY_STAGE0_LIST_ALL_DECLINES=1). Log the decline list and
		// continue with the partial IR if any was produced — the
		// remaining functions are still worth clang-verifying.
		short := strings.TrimSpace(err.Error())
		if os.Getenv("OSTY_STAGE0_AUDIT_MODULE_DECLINES_FULL") == "" && len(short) > 400 {
			short = short[:400] + "…"
		}
		t.Logf("module-verify: stage0 reports declines (partial IR continues): %s", short)
		if len(irBytes) == 0 {
			return
		}
	}
	tmp, err := os.MkdirTemp("", "stage0-audit-module-*")
	if err != nil {
		t.Logf("module-verify: TempDir failed: %v", err)
		return
	}
	defer os.RemoveAll(tmp)
	llPath := filepath.Join(tmp, "module.ll")
	if err := os.WriteFile(llPath, irBytes, 0o600); err != nil {
		t.Logf("module-verify: write IR file: %v", err)
		return
	}
	cmd := exec.Command(clangPath, "-c", "-o", os.DevNull, llPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("module-verify: full toolchain module compiled cleanly (%d bytes IR)", len(irBytes))
		return
	}
	combined := strings.TrimSpace(string(out))
	if combined == "" {
		combined = err.Error()
	}
	// Bucket the module-level diagnostics so the histogram matches
	// the per-function bucketing.
	moduleTally := map[string]int{}
	moduleSamples := map[string]string{}
	for _, line := range strings.Split(combined, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "error: ") {
			continue
		}
		key := normalizeClangDiagnostic(line)
		moduleTally[key]++
		if _, seen := moduleSamples[key]; !seen {
			moduleSamples[key] = line
		}
	}
	if len(moduleTally) == 0 {
		// Compiler exited non-zero but no parsed `error:` lines — log
		// the raw output so we don't lose the signal.
		short := combined
		if len(short) > 600 {
			short = short[:600] + "…"
		}
		t.Logf("module-verify: clang failed without parsable error lines: %s", short)
		return
	}
	t.Logf("module-verify: clang found %d distinct module-level diagnostic shape(s):", len(moduleTally))
	type bucket struct {
		key   string
		count int
	}
	bks := make([]bucket, 0, len(moduleTally))
	for k, v := range moduleTally {
		bks = append(bks, bucket{k, v})
	}
	sort.Slice(bks, func(i, j int) bool {
		if bks[i].count != bks[j].count {
			return bks[i].count > bks[j].count
		}
		return bks[i].key < bks[j].key
	})
	for i, b := range bks {
		if i >= 10 {
			t.Logf("  …%d more shapes truncated", len(bks)-10)
			break
		}
		t.Logf("  %4d  %s", b.count, b.key)
		if sample := moduleSamples[b.key]; sample != "" && sample != b.key {
			trimmed := sample
			if len(trimmed) > 300 {
				trimmed = trimmed[:300] + "…"
			}
			t.Logf("       sample: %s", trimmed)
		}
	}
}

// runStage0AuditExecVerify emits the full module, links it against
// the runtime, and runs the produced binary with `--version`. Catches
// link-stage and trivial-runtime crashes that L1/L2 cannot see.
//
// The runtime sources live under
// `internal/backend/runtime/osty_runtime.c`; this harness compiles
// them on demand and links them with the IR. Failure modes reported:
//
//   - link error (undefined symbols, ABI mismatch)
//   - exec failure (segfault / hang / non-zero exit *without* the
//     known stage0 dispatch banner — see
//     `stage0AuditOutputHasKnownAbortMarker`)
//   - exec timeout (defaults to 10s; the smoke target is just
//     `--selfhost-doctor` so this should be sub-second)
func runStage0AuditExecVerify(t *testing.T, module *mir.Module, clangPath string) {
	t.Helper()
	if module == nil {
		t.Logf("exec-verify: skipped (nil MIR module)")
		return
	}
	if clangPath == "" {
		var err error
		clangPath, err = exec.LookPath("clang")
		if err != nil {
			t.Logf("exec-verify: clang not found in PATH (%v); skipping", err)
			return
		}
	}
	prevListAll := os.Getenv("OSTY_STAGE0_LIST_ALL_DECLINES")
	defer os.Setenv("OSTY_STAGE0_LIST_ALL_DECLINES", prevListAll)
	os.Setenv("OSTY_STAGE0_LIST_ALL_DECLINES", "1")
	irBytes, err := stage0.EmitMIR(module, llvmabi.Options{PackageName: "audit-exec"})
	if err != nil {
		// Same deal as module-verify — partial IR comes alongside the
		// declines. Try to link/run what we got; the remaining bugs
		// downstream of declines are still worth surfacing.
		short := strings.TrimSpace(err.Error())
		if len(short) > 400 {
			short = short[:400] + "…"
		}
		t.Logf("exec-verify: stage0 reports declines (partial IR continues): %s", short)
		if len(irBytes) == 0 {
			return
		}
	}
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(filepath.Dir(wd))
	runtimeC := filepath.Join(repoRoot, "internal", "backend", "runtime", "osty_runtime.c")
	if _, err := os.Stat(runtimeC); err != nil {
		t.Logf("exec-verify: runtime C source missing at %s (%v); skipping", runtimeC, err)
		return
	}
	tmp, err := os.MkdirTemp("", "stage0-audit-exec-*")
	if err != nil {
		t.Logf("exec-verify: TempDir failed: %v", err)
		return
	}
	defer func() {
		if os.Getenv("OSTY_STAGE0_AUDIT_KEEP_TMP") == "" {
			os.RemoveAll(tmp)
		} else {
			t.Logf("exec-verify: keeping temp dir %s", tmp)
		}
	}()
	llPath := filepath.Join(tmp, "module.ll")
	if err := os.WriteFile(llPath, irBytes, 0o600); err != nil {
		t.Logf("exec-verify: write IR file: %v", err)
		return
	}
	binPath := filepath.Join(tmp, "audit-exec")
	// Link flags mirror the production `clangPlatformRuntimeLinkArgs`
	// + `clangLinkLibraryArgs` (`internal/backend/llvm.go:425` and
	// `internal/llvmabi/api.go:164`). Without these, the runtime
	// fails to resolve macOS Keychain (`-framework Security
	// CoreFoundation`), zlib (`-lz`), and libm (`-lm`) symbols at
	// link time, swamping audit output with 70+ undefined-symbol
	// errors that aren't actually stage0 emit bugs.
	linkArgs := append([]string{"-O0", "-o", binPath, llPath, runtimeC},
		stage0AuditPlatformLinkArgs()...)
	cmd := exec.Command(clangPath, linkArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		combined := strings.TrimSpace(string(out))
		if combined == "" {
			combined = err.Error()
		}
		// Filter to error lines only; clang -O0 still emits dozens of
		// `-Wdeprecated-declarations` warnings from `osty_runtime.c`'s
		// macOS Keychain bindings that drown the actual link diagnostics.
		var errLines []string
		for _, line := range strings.Split(combined, "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "error:") || strings.Contains(line, "Undefined symbols") || strings.Contains(line, "ld: ") || strings.Contains(line, "_osty_") || strings.Contains(line, "referenced from") || strings.HasPrefix(line, "\"_") {
				errLines = append(errLines, line)
			}
		}
		if len(errLines) == 0 {
			t.Logf("exec-verify: link failed (clang %s) — no error lines parsed; tail of output: %s", strings.Join(linkArgs, " "), truncate(combined, 1200))
			return
		}
		t.Logf("exec-verify: link failed; %d error line(s):", len(errLines))
		for i, line := range errLines {
			if i >= 30 {
				t.Logf("  …%d more error lines", len(errLines)-30)
				break
			}
			t.Logf("  %s", truncate(line, 400))
		}
		return
	}
	t.Logf("exec-verify: linked binary at %s (size %s)", binPath, fileSize(binPath))
	// Smoke test: invoke with `--selfhost-doctor`, the lightest of
	// `toolchain/main.osty`'s advertised subcommands. Two outcomes
	// pass:
	//
	//   1. exit 0 — the produced binary actually handled the command.
	//      Implies stage0 emit + runtime are deep enough to dispatch.
	//   2. SIGABRT (exit 134) + stderr contains the known
	//      `osty-self: unsupported command` marker — main reached
	//      argv dispatch and printed the usage banner before
	//      aborting. Stage0 today bottoms out here because the CLI
	//      switch in `toolchain/main.osty` calls
	//      `process.abort(...)` for every entry point until
	//      per-instruction MIR body emission lands (Phase 1+).
	//
	// Anything else (segfault before main, no output, wrong message)
	// is a real regression and gets logged.
	runCmd := exec.Command(binPath, "--selfhost-doctor")
	runOut, runErr := runCmd.CombinedOutput()
	combined := strings.TrimSpace(string(runOut))
	if runErr == nil {
		t.Logf("exec-verify: binary --selfhost-doctor succeeded; first 200 bytes of output: %s", truncate(combined, 200))
		return
	}
	if stage0AuditOutputHasKnownAbortMarker(combined) {
		t.Logf("exec-verify: binary reached argv dispatch + printed known stage0 banner before abort (%v); stage0 supported subset still routes through process.abort. Output: %s", runErr, truncate(combined, 200))
		return
	}
	t.Logf("exec-verify: binary --selfhost-doctor failed: %v; output: %s", runErr, truncate(combined, 400))
}

// stage0AuditOutputHasKnownAbortMarker reports whether the captured
// stdout+stderr from a stage0-produced binary invocation contains
// one of the known banner strings emitted by `toolchain/main.osty`'s
// CLI dispatch before it falls back to `process.abort`. Used by
// exec-verify and fp-verify to distinguish "binary reached argv
// dispatch and aborted by design" (current stage0 ceiling) from
// "binary crashed earlier" (a real regression).
//
// The markers must match strings the production toolchain CLI
// actually emits so audit pass/fail flips immediately when stage0
// regresses out of being able to print them.
func stage0AuditOutputHasKnownAbortMarker(output string) bool {
	markers := []string{
		"osty-self: unsupported command",
		"host compiler forwarding is disabled",
		"supported subcommands:",
		// New marker from decline-stub banner — the produced binary
		// reached the declined function via real CLI dispatch (rather
		// than the unsupported-command fallback) and aborted from the
		// stub. Emitted by `osty_rt_stage0_declined` for every body
		// `emitDeclineStub` lays down.
		"osty-self: stage0 declined function:",
		"requires the LIR Proto self-host pipeline",
	}
	for _, m := range markers {
		if strings.Contains(output, m) {
			return true
		}
	}
	return false
}

// truncate returns s clipped to n bytes with an ellipsis suffix when
// truncation occurred. Used by audit log helpers; keeps the output
// readable when clang dumps thousand-line type-mismatch reports.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// fileSize returns a human-readable size string for path; falls back
// to "?" on error so audit log lines stay parseable.
func fileSize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "?"
	}
	sz := info.Size()
	switch {
	case sz < 1024:
		return fmt.Sprintf("%dB", sz)
	case sz < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(sz)/1024.0)
	default:
		return fmt.Sprintf("%.1fMB", float64(sz)/(1024.0*1024.0))
	}
}

// stage0AuditFixedPointVerifyEnabled reports whether the audit
// should run the produced binary against fixture Osty source files
// to verify it semantically does the same job the source osty does.
// Implies (re-runs internally) the L3 link step.
//
// Cost: link + 3 short subprocess invocations on tiny fixtures.
// ~10s on top of L3.
func stage0AuditFixedPointVerifyEnabled() bool {
	switch strings.TrimSpace(os.Getenv("OSTY_STAGE0_AUDIT_FP_VERIFY")) {
	case "1", "true", "TRUE", "True", "on", "ON", "On", "yes", "YES", "Yes":
		return true
	}
	return false
}

// stage0AuditFixedPointFullEnabled reports whether the audit should
// additionally run the heaviest fixed-point check: the produced
// binary builds the toolchain itself (a real install-self cycle).
// Cost: ~15 min on top of FP_VERIFY. Off by default; only flip on
// for end-to-end CI verification.
func stage0AuditFixedPointFullEnabled() bool {
	switch strings.TrimSpace(os.Getenv("OSTY_STAGE0_AUDIT_FP_FULL")) {
	case "1", "true", "TRUE", "True", "on", "ON", "On", "yes", "YES", "Yes":
		return true
	}
	return false
}

// runStage0AuditFixedPointVerify links the stage0-produced IR with
// the runtime, then exercises the resulting binary against tiny
// fixture Osty programs. Each subcommand probes a deeper stratum
// of the front-end (lexer → parser → type checker → full pipeline).
// On the first failing phase we log the diagnostic and return,
// since deeper phases would also fail.
func runStage0AuditFixedPointVerify(t *testing.T, module *mir.Module, clangPath string) {
	t.Helper()
	if module == nil {
		t.Logf("fp-verify: skipped (nil MIR module)")
		return
	}
	if clangPath == "" {
		var err error
		clangPath, err = exec.LookPath("clang")
		if err != nil {
			t.Logf("fp-verify: clang not found in PATH (%v); skipping", err)
			return
		}
	}
	prevListAll := os.Getenv("OSTY_STAGE0_LIST_ALL_DECLINES")
	defer os.Setenv("OSTY_STAGE0_LIST_ALL_DECLINES", prevListAll)
	os.Setenv("OSTY_STAGE0_LIST_ALL_DECLINES", "1")
	irBytes, err := stage0.EmitMIR(module, llvmabi.Options{PackageName: "audit-fp"})
	if err != nil {
		short := strings.TrimSpace(err.Error())
		if len(short) > 400 {
			short = short[:400] + "…"
		}
		t.Logf("fp-verify: stage0 reports declines (partial IR continues): %s", short)
		if len(irBytes) == 0 {
			return
		}
	}
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(filepath.Dir(wd))
	runtimeC := filepath.Join(repoRoot, "internal", "backend", "runtime", "osty_runtime.c")
	if _, err := os.Stat(runtimeC); err != nil {
		t.Logf("fp-verify: runtime C source missing at %s (%v); skipping", runtimeC, err)
		return
	}
	tmp, err := os.MkdirTemp("", "stage0-audit-fp-*")
	if err != nil {
		t.Logf("fp-verify: TempDir failed: %v", err)
		return
	}
	defer os.RemoveAll(tmp)
	llPath := filepath.Join(tmp, "module.ll")
	if err := os.WriteFile(llPath, irBytes, 0o600); err != nil {
		t.Logf("fp-verify: write IR file: %v", err)
		return
	}
	binPath := filepath.Join(tmp, "audit-fp-osty")
	linkArgs := append([]string{"-O0", "-o", binPath, llPath, runtimeC},
		stage0AuditPlatformLinkArgs()...)
	cmd := exec.Command(clangPath, linkArgs...)
	out, linkErr := cmd.CombinedOutput()
	if linkErr != nil {
		combined := strings.TrimSpace(string(out))
		var errLines []string
		for _, line := range strings.Split(combined, "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "error:") || strings.Contains(line, "Undefined symbols") || strings.Contains(line, "ld: ") || strings.Contains(line, "_osty_") || strings.Contains(line, "referenced from") || strings.HasPrefix(line, "\"_") {
				errLines = append(errLines, line)
			}
		}
		if len(errLines) == 0 {
			t.Logf("fp-verify: link failed (no parsable errors): %s", truncate(combined, 400))
			return
		}
		t.Logf("fp-verify: link failed; %d error line(s):", len(errLines))
		for i, line := range errLines {
			if i >= 10 {
				t.Logf("  …%d more", len(errLines)-10)
				break
			}
			t.Logf("  %s", truncate(line, 300))
		}
		return
	}

	// Tiny fixture — `println` is the simplest entry that exercises
	// stdlib bridging. The fixture is throwaway; we just need
	// something the produced osty can chew on.
	fixturePath := filepath.Join(tmp, "fp_fixture.osty")
	fixtureSrc := `fn main() {
    println("hello")
}
`
	if err := os.WriteFile(fixturePath, []byte(fixtureSrc), 0o600); err != nil {
		t.Logf("fp-verify: write fixture: %v", err)
		return
	}

	type phase struct {
		name string
		args []string
	}
	// Phase sequence mirrors what `toolchain/main.osty` advertises in
	// its `--selfhost-doctor` banner — `--selfhost-doctor` is the
	// lightest (no fixture needed), `lir-proto-lower` is the next
	// stratum, `compile` is full pipeline. Stage0 today routes every
	// one of these through `process.abort` after printing the
	// dispatch banner, so each phase is graded on three outcomes:
	//
	//   - exit 0 → phase actually handled (real progress)
	//   - known stage0 abort banner + non-zero exit → binary
	//     reached argv dispatch (current ceiling)
	//   - anything else → regression (segfault before dispatch,
	//     missing banner, hang)
	//
	// Order matters for the "deepest reached" log line — each entry
	// implies the previous one's machinery worked inside the
	// produced binary.
	phases := []phase{
		{name: "--selfhost-doctor", args: []string{"--selfhost-doctor"}},
		{name: "lir-proto-lower", args: []string{"lir-proto-lower", fixturePath}},
		{name: "compile", args: []string{"compile", fixturePath}},
	}
	deepest := ""
	for _, p := range phases {
		runCmd := exec.Command(binPath, p.args...)
		runOut, runErr := runCmd.CombinedOutput()
		combined := strings.TrimSpace(string(runOut))
		if runErr == nil {
			deepest = p.name
			t.Logf("fp-verify: `osty %s` ✓ exit 0 (%d bytes output)", strings.Join(p.args, " "), len(runOut))
			continue
		}
		if stage0AuditOutputHasKnownAbortMarker(combined) {
			t.Logf("fp-verify: `osty %s` reached argv dispatch + printed known banner before abort (%v); stage0 ceiling. Output: %s", strings.Join(p.args, " "), runErr, truncate(combined, 200))
			// Treat banner-then-abort as "binary entry works"; do
			// not advance `deepest` (the command was not actually
			// handled) but keep walking so a later phase that
			// exits 0 still gets credited.
			continue
		}
		t.Logf("fp-verify: `osty %s` FAILED with no recognisable stage0 banner: %v", strings.Join(p.args, " "), runErr)
		t.Logf("  output: %s", truncate(combined, 800))
		break
	}
	if deepest == "" {
		t.Logf("fp-verify: stage0 binary entry-point + dispatch reachable; no phase actually handled (current stage0 ceiling)")
	} else {
		t.Logf("fp-verify: produced binary reached phase %q on the fixture", deepest)
	}

	// Optional: full install-self via the produced binary. Heavy
	// (~15 min); intended for one-off CI verification. Off by
	// default. The produced binary is dropped into a temp directory
	// configured to look like a fresh-clone bootstrap so install-self
	// uses stage0 fallback recursively.
	if stage0AuditFixedPointFullEnabled() {
		t.Logf("fp-verify (full): launching produced binary as `install-self` — this can take >10 min")
		runCmd := exec.Command(binPath, "install-self")
		runOut, runErr := runCmd.CombinedOutput()
		if runErr != nil {
			t.Logf("fp-verify (full): produced binary install-self FAILED: %v", runErr)
			t.Logf("  output: %s", truncate(strings.TrimSpace(string(runOut)), 1200))
			return
		}
		t.Logf("fp-verify (full): produced binary completed install-self ✓ (%d bytes output)", len(runOut))
	}
}

// stage0AuditPlatformLinkArgs returns the platform-specific link
// flags the audit harness needs when invoking clang to link a stage0
// IR module + the runtime C source. Mirrors the production
// `clangPlatformRuntimeLinkArgs` + `clangLinkLibraryArgs` chain so
// the audit doesn't burn iterations on link errors that aren't
// actually stage0 bugs (Keychain frameworks, zlib, libm).
func stage0AuditPlatformLinkArgs() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"-ladvapi32"}
	case "darwin":
		return []string{"-pthread", "-lz", "-lm", "-framework", "Security", "-framework", "CoreFoundation"}
	default:
		return []string{"-pthread", "-lz", "-lm"}
	}
}
