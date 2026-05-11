# Start Here

## Mission model

Osty is a self-hosting language implementation. The long-term center of gravity
is `toolchain/*.osty`, while Go stays at the host boundary.

## First-pass reading order

1. [`/CLAUDE.md`](../../CLAUDE.md) for repo rules
2. [`/README.md`](../../README.md) for current status and CLI surface
3. [`/ARCHITECTURE.md`](../../ARCHITECTURE.md) for pipeline and package roles
4. [`/LANG_SPEC_v0.5/ABRIDGED.md`](../../LANG_SPEC_v0.5/ABRIDGED.md) for quick language rules
5. [`/LANG_SPEC_v0.5/README.md`](../../LANG_SPEC_v0.5/README.md) and [`/OSTY_GRAMMAR_v0.5.md`](../../OSTY_GRAMMAR_v0.5.md) for authority

## Minimal mental model

- Spec authority: `LANG_SPEC_v0.5/` + `OSTY_GRAMMAR_v0.5.md`
- Implementation status authority: `CHANGELOG_v0.5.md`
- Pipeline authority: `ARCHITECTURE.md`
- User-facing status and command map: `README.md`
- Repo rules for agents: `CLAUDE.md` and `AGENTS.md`

## Working defaults

- Search text with `rg`
- Find files with `fd` or repo globbing
- Prefer targeted `go test -count=1 -vet=off <pkg>` over whole-tree runs
- Prefer `just front`, `just short`, `just lsp <regex>`, `just gen <regex>`, `just diag <regex>`, `just cmd <regex>` when available
- Use `just prepush` for the broad gate

## What to inspect before editing

- the relevant source package
- the nearest spec section
- focused tests in the same subsystem
- any generated-file or bootstrap note touching that area

## If you only remember five things

1. Osty-first, not Go-first
2. spec beats implementation notes
3. generated files are not hand-edited
4. diagnostics need stable codes
5. verify with the narrowest useful loop first
