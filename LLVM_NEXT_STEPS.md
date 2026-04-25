# LLVM Migration: Recommended Next Steps

> **Date**: 2026-04-26

---

## Immediate Actions (Next 2 Weeks)

### 1. Port Function-Level Setup ✅ DONE

**Status**: Completed in PR #1 (this branch).
- `beginFunction()`, `render()`, `renderFunction()` added to `toolchain/llvmgen.osty`
- GC state, safepoints, loop hints, defer, attributes all merged

### 2. Port Scope Management (Workstream A)

**Task**: Port `captureScopeState`, `restoreScopeState`, `popScope`.

**Why**: Nested function bodies, closures, and block expressions require scope save/restore.

### 3. Close Tier A Blockers (Workstream A + C)

**Current blocker**: `List<String>` literal + method lowering (974 usage sites),
`String` concat/slice (most toolchain files), Closure + env capture.

**Recommended approach**:
1. Run `osty check toolchain` on this branch
2. Group errors by category, address top 3 first
3. Each fix should include a regression test

### 4. Fill MIR Stage 5 Gaps (Workstream B)

**Task**: Implement MIR lowering for `?` / optional chaining → branches.

**After `?`**: `match` patterns → CFG, then `if let` / `for let` destructuring.

### 5. String Runtime Completion (Workstream C)

**Task**: Complete `osty_runtime.c` string operations:
- `slice` (all four forms)
- `chars()` and `bytes()` — returns `List<Char>` / `List<Byte>`

### 6. Add Generic/Interface Smoke Tests (Workstream D)

**Task**: Create smoke fixtures exercising generic function instantiation,
generic struct, interface boxing and dispatch.

---

## Parallelization Opportunities

| Workstream A | Workstream B | Workstream C | Workstream D |
|---|---|---|---|
| Port scope mgmt | Profile 949 errors | String runtime | Generic smoke |
| Port defer mgmt | `?` → branches in MIR | List runtime | Promote corpus |
| Interface vtables | `match` → CFG | GC root table | Benchmark setup |
