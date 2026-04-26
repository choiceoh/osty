package llvmgen

// hoistAllocasToEntry rewrites a function body so every `alloca`
// instruction lives in the entry block.
//
// Background: the legacy support_snapshot codegen emits `alloca` lines
// straight into emitter.body wherever the current emit cursor sits. When
// `let s = expr()` is inlined into a loop body, the alloca lands inside
// the loop's basic block instead of the entry block. Each loop iteration
// then re-executes that alloca, growing the C stack by `sizeof(slot)`
// bytes per iteration without ever freeing — a 200K-iteration loop
// accumulates ~1.6 MB of dead stack frames, which the GC has to walk on
// every safepoint scan, turning per-iter cost into O(N) and the whole
// loop into O(N²).
//
// LLVM's mem2reg / SROA passes promote allocas to SSA registers when
// they're in the entry block, and on -O3 they'd normally hoist single-
// use stack slots. The slots we emit fail the "single use" precondition
// because the alloca's address is captured (passed to runtime helpers,
// stored as a GC root), so promotion is blocked. Hoisting the alloca
// itself to entry doesn't unlock promotion, but it does eliminate the
// per-iter stack growth — the same alloca pointer is reused every
// iteration, the store overwrites the slot in place, and stack-walk cost
// stays O(1) regardless of loop trip count.
//
// Algorithm:
//
//  1. Find the index of the first `br` line (terminator of the entry
//     block — every emitted function has one because emit always falls
//     through to a label like `for.cond0:` or returns directly).
//  2. Scan all subsequent lines. Any line of the form
//     `  %name = alloca <ty>...` gets removed from its current position
//     and queued for hoist.
//  3. Insert the hoisted lines just before the entry-block terminator,
//     preserving their original relative order.
//
// Edge cases:
//
//   - No `br` in body (e.g. `define void @f() { ret void }` style):
//     all lines are already in the entry block; nothing to hoist.
//   - Allocas already in the entry block (before the first `br`):
//     left untouched.
//   - Lines that LOOK like alloca but are inside a comment or string:
//     emitted body lines never embed allocas in arbitrary text — every
//     alloca emitter writes via fmt.Sprintf("  %s = alloca ...") with
//     a leading two-space indent — so the prefix check is unambiguous.
//   - Multiple basic blocks past entry: irrelevant. We move every
//     alloca to entry regardless of which block it lived in.
//
// The function is idempotent: running it on already-hoisted IR leaves
// the body unchanged because there are no allocas past the first `br`.
func hoistAllocasToEntry(body []string) []string {
	firstBr := -1
	for i, line := range body {
		if isBrTerminator(line) {
			firstBr = i
			break
		}
	}
	if firstBr < 0 {
		return body
	}

	// Walk body[firstBr+1:] once, collecting alloca lines into `hoisted`
	// and the rest into `kept`.
	hoisted := make([]string, 0)
	kept := make([]string, 0, len(body)-firstBr-1)
	for _, line := range body[firstBr+1:] {
		if isAllocaLine(line) {
			hoisted = append(hoisted, line)
		} else {
			kept = append(kept, line)
		}
	}
	if len(hoisted) == 0 {
		return body
	}

	// Reassemble: entry-block instructions, then hoisted allocas, then
	// the entry-block `br`, then everything after.
	out := make([]string, 0, len(body))
	out = append(out, body[:firstBr]...)
	out = append(out, hoisted...)
	out = append(out, body[firstBr])
	out = append(out, kept...)
	return out
}

// isAllocaLine reports whether `line` is an LLVM `alloca` instruction
// emitted by the snapshot helpers. They all write the form
// `"  %<name> = alloca <ty>..."` via fmt.Sprintf with a leading two-
// space indent. Delegates to the Osty-sourced `mirIsAllocaLine`
// (`toolchain/mir_generator.osty`).
func isAllocaLine(line string) bool {
	return mirIsAllocaLine(line)
}

// isBrTerminator reports whether `line` is the unconditional or
// conditional `br` instruction that terminates a basic block.
// Delegates to `mirIsBrTerminatorLine`.
func isBrTerminator(line string) bool {
	return mirIsBrTerminatorLine(line)
}
