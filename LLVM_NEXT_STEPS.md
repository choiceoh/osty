# LLVM Migration: Recommended Next Steps

> **Date**: 2026-04-26

---

## Immediate Actions (Next 2 Weeks)

### 1. Port Function-Level Setup ✅ DONE (PR #863)

**Status**: Completed in PR #863.
- `beginFunction()`, `render()`, `renderFunction()` added to `toolchain/llvmgen.osty`
- GC state, safepoints, loop hints, defer, attributes all merged

### 2. Port AST Lowering Patterns ✅ DONE (PR #866)

**Status**: Completed in PR #866.
- `llvmTypeFor`, `llvmTypeHeadName` — type mapping
- `emitLetLowering`, `emitAssignLowering` — bindings
- `emitIfStmtLowering`, `emitIfExprLowering` — conditionals
- `emitForRangeLowering`, `emitForWhileLowering` — loops
- `emitCallLowering`, `emitReturnLowering` — calls/returns
- `emitStructLitLowering`, `emitClosureEnvAllocation` — aggregates/closures

### 3. Close Tier A Blockers (Workstream A + C) — 🟡 Priority

**Current blocker**: `List<String>` literal + method lowering (974 usage sites),
`String` concat/slice (most toolchain files), Closure + env capture.

**Recommended approach**:
1. Run `osty check toolchain` on this branch
2. Group errors by category, address the top 3 first
3. Each fix should include a regression test in `testdata/backend/llvm_smoke/`

### 4. Fill MIR Stage 5 Gaps (Workstream B) — 🟡 Priority

**Task**: Implement MIR lowering for `?` / optional chaining → branches.

**After `?`**: `match` patterns → CFG, then `if let` / `for let` destructuring.

### 5. String Runtime Completion (Workstream C) — 🟡 Priority

**Task**: Complete `osty_runtime.c` string operations:
- `concat` (already has runtime intrinsic, needs C implementation)
- `slice` (all four forms: range, half-open, inclusive, open-high)
- `chars()` and `bytes()` — returns `List<Char>` / `List<Byte>`

### 6. Add Generic/Interface Smoke Tests (Workstream D) — 🟢 Priority

**Task**: Create smoke fixtures that exercise:
- Generic function instantiation (`id::<Int>`)
- Generic struct (`Pair<Int, Float>`)
- Interface boxing and dispatch

---

## Parallelization Opportunities

| Workstream A | Workstream B | Workstream C | Workstream D |
|---|---|---|---|
| Port scope mgmt | Profile 949 errors | String runtime | Generic smoke |
| Port defer mgmt | `?` → branches in MIR | List runtime | Promote corpus |
| Interface vtables | `match` → CFG | GC root table | Benchmark setup |
