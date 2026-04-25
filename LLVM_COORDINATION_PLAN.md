# LLVM → Osty Port: Master Coordination Plan

> **Date**: 2026-04-26
> **Status**: Active — four parallel workstreams in flight

---

## 1. Executive Summary

This plan coordinates the porting of the Osty LLVM backend from Go to
native Osty. The overall goal is to shift the execution backend from
`internal/gen` (Go transpiler) to a self-hosted LLVM native backend
owned by `toolchain/llvmgen.osty`.

### Current State Snapshot

| Workstream | Key Artifacts | Completion |
|---|---|---|
| **A. AST→IR Lowering** | `ast_lowering.osty` (666 lines) | ~30% |
| **B. MIR→LLVM Lowering** | `mir_lowering.osty` (707 lines) | ~25% |
| **C. Runtime ABI & Symbols** | `runtime_symbols.osty` (385 lines) | ~60% |
| **D. Smoke Tests** | `smoke_corpus.osty` (806 lines, 22 fixtures) | ~40% |

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

## 3. PR Sequence

| PR | Scope | Depends On |
|----|-------|------------|
| #863 | Function setup + runtime symbols | — |
| #866 | AST lowering patterns | #863 |
| #867 | MIR `?` optional chaining | #866 |
| #868 | Container intrinsics (~150 symbols) | #867 |
| #869 | Call site lowering + indirect calls | #868 |
| #870 | Module-level emission | #869 |
| #871 | String runtime C implementation | #870 |
| #872 | Concurrency/scheduler C runtime | #871 |
| #873 | Close Tier A blockers | #872 |
| #874–878 | Self-host progression (stdlib, pkgmgr, core toolchain) | #873 |
| #879–880 | Multi-file package / workspace emission | #878 |
| #881 | Default backend switch | #880 |
| #882 | Remove Go bootstrap bridge | #881 |

---

## 4. Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Osty transpiler limitations | Medium | High | Use Go bridge as fallback |
| Drift between Osty and Go | High | Medium | Maintain `support_snapshot.go` |
| C runtime missing | High | High | Prioritize osty_runtime.c |
| CFG analysis complexity | Medium | Medium | Defer vectorization analysis |

---

## 5. Success Criteria

1. `toolchain/llvmgen.osty` owns all LLVM backend semantics
2. `support_snapshot.go` shows zero diff on smoke corpus
3. `osty build --backend=llvm` produces identical output to `--backend=go`
4. smoke corpus (80+ fixtures) passes on LLVM backend
5. `osty check toolchain` has no NEW errors from LLVM backend
