# Osty AI Agent Wiki

This folder is a wiki-ready, AI-first navigation layer for the repository.
It is intentionally shorter than the full docs set. When this wiki and the
spec or code disagree, the spec and code win.

## Read this first

1. [`/CLAUDE.md`](../../CLAUDE.md)
2. [`/README.md`](../../README.md)
3. [`/ARCHITECTURE.md`](../../ARCHITECTURE.md)
4. [`/LANG_SPEC_v0.5/README.md`](../../LANG_SPEC_v0.5/README.md)
5. [`/LANG_SPEC_v0.5/ABRIDGED.md`](../../LANG_SPEC_v0.5/ABRIDGED.md)
6. [`/OSTY_GRAMMAR_v0.5.md`](../../OSTY_GRAMMAR_v0.5.md)

## Use this wiki for

- fast task routing
- repo-specific guardrails
- verification shortcuts
- “if you touch X, also read Y” guidance

## Do not use this wiki for

- overriding the language spec
- inventing new syntax or backend policy
- changing generated files by hand
- deciding to implement compiler logic in Go when it belongs in `toolchain/*.osty`

## Start by task type

- New here: [Start Here](./Start-Here.md)
- Repo rules and non-negotiables: [Authority and Non-Negotiables](./Authority-and-Non-Negotiables.md)
- Where code lives: [Repo Map](./Repo-Map.md)
- What to run before and after edits: [Validation and Workflows](./Validation-and-Workflows.md)
- Task-specific routing: [Task Playbooks](./Task-Playbooks.md)
- High-risk areas and common mistakes: [Risk Areas and Footguns](./Risk-Areas-and-Footguns.md)
- Canonical source index: [Source of Truth Index](./Source-of-Truth-Index.md)

## Fast rules

- Prefer `just` recipes when available.
- Prefer focused verification before broad sweeps.
- Compiler policy belongs in Osty first, Go only at host boundaries.
- The pipeline order is fixed: `source -> lexer -> parser -> resolve -> check -> (format / lint / ir / backend)`.
- The public backend is LLVM only.
- Surface syntax changes require spec updates, not implementation-only edits.

## Stop and ask when

- the requested change implies a new language surface without a spec change
- you would need to edit generated artifacts manually
- you are about to add Go logic that could live in `toolchain/*.osty`
- the right source of truth is ambiguous
