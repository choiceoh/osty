# LLVM → Osty Port: Master Coordination Plan

> **Date**: 2026-04-26
> **Status**: Active — four parallel workstreams in flight
> **Owner**: LLVM backend migration team

---

## 1. Executive Summary

This plan coordinates four parallel workstreams porting the Osty backend from
Go to native Osty. The overall goal is to shift the execution backend from
`internal/gen` (Go transpiler) to a self-hosted LLVM native backend owned by
`toolchain/llvmgen.osty`.

### Current State Snapshot

| Workstream | Key Artifacts | Completion |
|---|---|---|
| **A. AST→IR Lowering** | `ast_lowering.osty` (666 lines) | ~30% |
| **B. MIR→LLVM Lowering** | `mir_lowering.osty` (707 lines) | ~25% |
| **C. Runtime ABI & Symbols** | `runtime_symbols.osty` (385 lines) | ~60% |
| **D. Smoke Tests** | `smoke_corpus.osty` (806 lines, 22 fixtures) | ~40% |
| **Core Merge** | `toolchain/llvmgen.osty` (+1,038 lines) | ✅ Done |

### Architecture

```
Osty Source → lexer → parser → resolve → check
  → ir.Lower (HIR) → mir.Lower (MIR)
  → backend dispatch:
      ├── MIR path → llvmgen.GenerateFromMIR
      └── HIR path → legacyFileFromModule → llvmgen.Generate
                           ↑                    ↑
              internal/llvmgen/ (Go bootstrap)   toolchain/llvmgen.osty
                           ↓                    ↑
              support_snapshot.go ←── drift guard ──→ llvmgen.osty
                           ↓
                    LLVM IR (.ll) → clang → binary
```

---

## 2. Milestones

| # | Milestone | Status |
|---|-----------|--------|
| 1 | First Binary Execution | ✅ Done |
| 2 | Scalar + Control Flow Corpus | ✅ Done |
| 3 | Struct/Enum Smoke | ✅ Done |
| 4 | Float/String Payload Enums | ✅ Done |
| 5 | Osty-Owned Backend Core | 🟡 In Progress |
| 6 | Self-Hosted Front End Passes | 🟡 In Progress |
| 7 | pkgmgr Self-Host | 🔴 Not Started |
| 8 | Tier A Self-Host | 🔴 Not Started |
| 9 | Toolchain Self-Compile | 🔴 Not Started |
| 10 | Default Backend Switch | 🔴 Distant |

---

## 3. Recommended PR Order

| PR | Scope | Depends On |
|----|-------|------------|
| #1 | Function setup + runtime symbols (this branch) | — |
| #2 | AST lowering patterns into llvmgen.osty | #1 |
| #3 | MIR `?` optional chaining | #2 |
| #4 | Container intrinsics (~150 symbols) | #3 |
| #5 | Call site lowering + indirect calls | #4 |
| #6 | Module-level emission | #5 |
| #7 | String runtime C implementation | #6 |
| #8 | Concurrency/scheduler C runtime | #7 |
| #9 | Close Tier A blockers | #8 |
| #10–14 | Self-host progression | #9 |
| #15–16 | Multi-file package emission | #14 |
| #17 | Default backend switch | #16 |
| #18 | Remove Go bootstrap bridge | #17 |

---

## 4. Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Osty transpiler limitations | Medium | High | Use Go bridge as fallback |
| Drift between Osty and Go | High | Medium | Maintain `support_snapshot.go` |
| C runtime missing | High | High | Prioritize osty_runtime.c |
| CFG analysis complexity | Medium | Medium | Defer vectorization analysis |

---

## 5. Completion Criteria

1. `toolchain/llvmgen.osty` owns all LLVM backend semantics
2. `internal/llvmgen/support_snapshot.go` shows zero diff on smoke corpus
3. `osty build --backend=llvm` produces identical output to `--backend=go`
4. smoke corpus (80+ fixtures) passes on LLVM backend
5. `osty check toolchain` has no NEW errors from LLVM backend
