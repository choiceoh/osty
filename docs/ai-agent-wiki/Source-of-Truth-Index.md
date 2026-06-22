# Source of Truth Index

## Global docs

- project overview and CLI: [`/README.md`](../../README.md)
- architecture and pipeline: [`/ARCHITECTURE.md`](../../ARCHITECTURE.md)
- repo rules: [`/CLAUDE.md`](../../CLAUDE.md)
- agent defaults: [`/AGENTS.md`](../../AGENTS.md)

## Language authority

- language entrypoint: [`/LANG_SPEC_v0.5/README.md`](../../LANG_SPEC_v0.5/README.md)
- quick language card: [`/LANG_SPEC_v0.5/ABRIDGED.md`](../../LANG_SPEC_v0.5/ABRIDGED.md)
- grammar and decision log: [`/OSTY_GRAMMAR_v0.5.md`](../../OSTY_GRAMMAR_v0.5.md)
- resolved gaps: [`/SPEC_GAPS.md`](../../SPEC_GAPS.md)
- shipped implementation status: [`/CHANGELOG_v0.5.md`](../../CHANGELOG_v0.5.md)

## Diagnostics

- generated code catalog: [`/ERROR_CODES.md`](../../ERROR_CODES.md)
- source definitions: `/internal/diag/codes.go`
- renderer and golden tests: `/internal/diag/`

## Backend and runtime

- migration plan: [`/LLVM_MIGRATION_PLAN.md`](../../LLVM_MIGRATION_PLAN.md)
- gap plan: [`/LLVM_BACKEND_GAP_PLAN.md`](../../LLVM_BACKEND_GAP_PLAN.md)
- artifact layout: [`/LLVM_ARTIFACT_LAYOUT.md`](../../LLVM_ARTIFACT_LAYOUT.md)
- backend corpus: [`/LLVM_BACKEND_CORPUS.md`](../../LLVM_BACKEND_CORPUS.md)
- runtime GC: [`/RUNTIME_GC.md`](../../RUNTIME_GC.md)
- strict backend test baseline (Map iteration mis-lowering, etc.):
  [`/docs/backend-test-failures-audit-2026-05-26.md`](../backend-test-failures-audit-2026-05-26.md)
- self-host artifact/bootstrap docs: `/docs/osty_self_artifact_design.md`, `/docs/osty_self_bootstrap_design.md`
- self-rebuild ratchet script: [`/scripts/verify-self-rebuild`](../../scripts/verify-self-rebuild)
- LIR Proto subprocess entry: [`/cmd/osty-native-lirproto/main.go`](../../cmd/osty-native-lirproto/main.go)
- native checker LLVM parity plan: [`/docs/llvm-selfhost-plan.md`](../../docs/llvm-selfhost-plan.md)
- dual-target checker build/runbook: [`/cmd/osty-native-checker/README.md`](../../cmd/osty-native-checker/README.md)
- subprocess vs embedded checker gates: [`/SUBPROCESS_SWITCHOVER.md`](../../SUBPROCESS_SWITCHOVER.md)

## Useful code anchors

- main CLI: `/cmd/osty/`
- native checker boundary: `/cmd/osty-native-checker/`, `/internal/check/`
- self-host adapters and frozen seed: `/internal/selfhost/`
- toolchain sources: `/toolchain/`
- front-end fixtures and spec corpus: `/testdata/spec/`

## Fast answer heuristics

- “What is allowed?” -> spec + grammar
- “What is shipped today?” -> changelog + README
- “Where should code live?” -> CLAUDE + architecture + repo map
- “What command should I run?” -> justfile + validation page
- “Why is this backend path weird?” -> backend docs + fallback policy notes
