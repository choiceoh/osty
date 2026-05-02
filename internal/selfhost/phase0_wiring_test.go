package selfhost

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPhase0SelfHostWiringExists guards the Phase 0 self-host backend
// wiring: the chain `hirLowerSource` → `mirLowerModule` (which itself
// calls `hirMonomorphizeModule`) → `mirEmitModule` must be present in
// the toolchain source, and `toolchain/main.osty` must dispatch the
// `compile <file>` subcommand to that chain.
//
// The wiring is structural-only today because `osty-self` itself
// can't yet be built via the LLVM backend (the backend trips on
// stdlib body walls before reaching the new entry points — see
// SELFHOST_PORT_MATRIX.md). When the backend catches up this test
// gains a sibling that actually invokes `osty-self compile` on a
// trivial source and asserts the IR contains `target triple` plus
// `define void @main`. Until then the structural gate is the
// authoritative regression guard: any future PR that drops or
// renames a Phase 0 entry point fails this test before the next
// session's runtime test would.
//
// Phase 0 = module-skeleton emission (header + define-headers +
// `entry:` stub returns). Phase 1+ replaces the stub returns with
// real per-instruction lowering.
func TestPhase0SelfHostWiringExists(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}

	cases := []struct {
		path    string
		needles []string
	}{
		{
			path: "toolchain/mir_generator.osty",
			needles: []string{
				"pub fn mirEmitModule(",
				"pub fn mirEmitOpts(",
				"pub struct MirEmitOpts ",
				// Phase 1a — multi-block walk + terminator dispatch.
				// The earlier Phase 0 emitter handled only a single
				// `entry:` stub; Phase 1a iterates `func.blocks` and
				// translates each MirTermKind. Loss of either the
				// per-block iteration or the terminator dispatch
				// would silently fall back to the old stub and
				// produce malformed IR for any function with > 1
				// block (loops, branches, …).
				"for block in func.blocks {",
				"mirEmitTerminatorLine(",
				"mirEmitBlockTargetLabel(",
				"MirTermReturn ->",
				"MirTermGoto ->",
				"MirTermUnreachable ->",
				// Phase 1b — operand resolution + real terminator
				// values. `MirTermReturn` now references
				// `func.returnLocal` via `%v<id>`, and `MirTermBranch`
				// reads its cond operand. Loss of these would silently
				// regress to the literal-zero / unreachable stubs.
				"pub fn mirEmitOperand(",
				"mirEmitReturnTerminator(",
				"mirEmitBranchTerminator(",
				"mirEmitLocalRegName(",
				"MirOpConst -> mirEmitConstOperand",
				"MirConstInt -> mirGenIntToString",
				// Phase 1c — per-instruction emission. The block walk
				// now visits `block.instrs` and dispatches each
				// MirInstrAssign through the rvalue arms (Use, Unary,
				// Binary covered today; rest stub-with-TODO). Loss of
				// either the walk hookup or the rvalue dispatch would
				// silently regress to the empty-body Phase 1b shape
				// where every fn produced ret-stub-only IR.
				"for instr in block.instrs {",
				"mirEmitInstruction(func, instr)",
				"mirEmitAssignInstr(",
				"mirEmitRValue(",
				"mirEmitRValueUse(",
				"mirEmitRValueUnary(",
				"mirEmitRValueBinary(",
				"mirBinaryOpSymbol(",
				"MirInstrAssign -> mirEmitAssignInstr",
				"MirRVBinary -> mirEmitRValueBinary",
				// Phase 1d — Call, Intrinsic, Aggregate (Tuple).
				// Loss of any of these would silently regress the
				// instruction dispatcher to its Phase 1c TODO stub.
				"mirEmitCallInstr(",
				"mirEmitCallReturnType(",
				"mirEmitCallArgList(",
				"mirEmitIntrinsicInstr(",
				"mirEmitIoWriteIntrinsic(",
				"mirEmitAbortIntrinsic(",
				"mirEmitRValueAggregate(",
				"mirEmitTupleAggregate(",
				"mirEmitTupleTypeText(",
				"MirInstrCall -> mirEmitCallInstr",
				"MirInstrIntrinsic -> mirEmitIntrinsicInstr",
				"MirIntrinsicPrintln -> mirEmitIoWriteIntrinsic",
				"MirAggTuple -> mirEmitTupleAggregate",
			},
		},
		{
			path: "toolchain/mir_lower.osty",
			needles: []string{
				// hirMonomorphizeModule must still be invoked from
				// the MIR lowerer — Phase 0 leaves this wiring intact
				// rather than re-routing it. Loss of this call would
				// silently drop monomorphization from the chain.
				"hirMonomorphizeModule(",
			},
		},
		{
			path: "toolchain/monomorph_pass.osty",
			needles: []string{
				"pub fn hirMonomorphizeModule(",
			},
		},
		{
			path: "toolchain/main.osty",
			needles: []string{
				`hasSelfhostCommand(args, "compile")`,
				"runCompile(args)",
				"hirLowerSource(",
				"mirLowerModule(",
				"mirEmitModule(",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			full := filepath.Join(root, tc.path)
			src, err := os.ReadFile(full)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			text := string(src)
			for _, needle := range tc.needles {
				if !strings.Contains(text, needle) {
					t.Errorf("missing wiring %q in %s", needle, tc.path)
				}
			}
		})
	}
}

// TestPhase0DoctorMessageReflectsProgress pins the wording of the
// `--selfhost-doctor` output so a future revert of `runSelfhostDoctor`
// (which used to claim the module emitter was wholly missing) can't
// silently regress the surfaced status. Expanded coverage will tighten
// these bands as Phase 1+ lands.
func TestPhase0DoctorMessageReflectsProgress(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "main.osty"))
	if err != nil {
		t.Fatalf("read main.osty: %v", err)
	}
	text := string(src)
	// The doctor must NOT keep saying "missing stage: MIR-to-LLVM
	// module emitter" once mirEmitModule is wired (Phase 0). It SHOULD
	// surface the next dependency wall (per-instruction body emission).
	regressed := regexp.MustCompile(`missing stage:\s*MIR-to-LLVM module emitter`)
	if regressed.MatchString(text) {
		t.Fatalf("doctor still claims module emitter is missing — Phase 0 mirEmitModule should have flipped this status")
	}
	for _, needle := range []string{
		"phase 0",
		"per-instruction body emission",
	} {
		if !strings.Contains(strings.ToLower(text), needle) {
			t.Errorf("doctor message missing %q (Phase 0 status surface)", needle)
		}
	}
}
