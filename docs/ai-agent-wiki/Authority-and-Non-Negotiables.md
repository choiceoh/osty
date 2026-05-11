# Authority and Non-Negotiables

## Primary authority

When sources disagree, use this order:

1. `LANG_SPEC_v0.5/`
2. `OSTY_GRAMMAR_v0.5.md`
3. code
4. `CHANGELOG_v0.5.md`
5. `README.md`
6. this wiki

## Non-negotiable repo rules

- If logic can be written in Osty, do not introduce it in Go.
- New compiler policy, algorithms, and language logic should default to `toolchain/*.osty`.
- Go is for host boundaries: CLI entry, I/O, process, filesystem, JSON-RPC, networking, bridge layers, and bootstrap-only paths.
- Do not add external Go dependencies beyond the existing allowed set.
- Do not manually edit generated artifacts such as `ERROR_CODES.md`.
- Do not add new public backend flags. Public backend is LLVM.
- Do not add new syntax, keywords, or annotations without the corresponding spec update.

## Pipeline invariants

- Keep the phase order intact.
- Diagnostics accumulate through the pipeline and render at the end.
- Do not skip directly to later phases when earlier-phase behavior matters.

## Diagnostic invariants

- Every user-facing error needs a stable code.
- New error sites require code definition, docs comment, regeneration, and focused tests.
- Use the existing code families:
  - `E0001-E0099` lexical
  - `E0100-E0199` declarations/statements
  - `E0200-E0299` expressions
  - `E0300-E0399` types/patterns
  - `E0400-E0499` annotations
  - `E0500-E0599` resolution
  - `E0600-E0699` control flow
  - `E0700-E0799` type checking
  - `E2000-E2099` manifest/scaffolding
  - `L0001-L0099` lint

## Two policy facts worth remembering

- `OSTY_NATIVE_CHECKER_BIN` is an override/debug escape hatch, not the normal selection signal.
- Stage0/bootstrap fallback is explicit opt-in only; production builds must not silently route through it.
