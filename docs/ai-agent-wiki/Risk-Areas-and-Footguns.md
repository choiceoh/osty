# Risk Areas and Footguns

## High-risk files and paths

- `internal/selfhost/generated.go` — frozen seed, not a normal hand-edited source
- `ERROR_CODES.md` — generated file
- backend/runtime ABI files under `internal/backend/runtime/`
- spec files under `LANG_SPEC_v0.5/` and grammar files under `/OSTY_GRAMMAR_v0.5.md`

## Common mistakes

- implementing new compiler logic in Go instead of Osty
- treating `CHANGELOG_v0.5.md` as spec authority
- changing surface syntax without a spec edit
- hand-editing generated artifacts
- using broad whole-tree validation too early
- forgetting focused tests for new diagnostics

## Self-host and fallback traps

- The self-host/native checker path is the default CLI path (managed
  `osty-native-checker` subprocess after `UseManagedSubprocessChecker`; see
  `SUBPROCESS_SWITCHOVER.md`).
- `OSTY_NATIVE_CHECKER_BIN` is for override/debug use.
- Stage0 fallback is emergency-only and opt-in.
- Production behavior should fail clearly rather than silently downgrade.

## Backend traps

- unsupported source shapes should stay on structured diagnostic paths
- runtime ABI changes can affect emitted binaries far from the edited file
- backend docs may describe migration history; check current shipped behavior too

## Spec traps

- `LANG_SPEC_v0.5/` tells you what is authoritative
- `CHANGELOG_v0.5.md` tells you what is shipped now
- “spec-frozen but not shipped” is a real category in this repo

## Testing traps

- broad failures may already exist; baseline first
- the fastest useful loop is usually better than `go test ./...`
- `just` recipes encode repo expectations; prefer them over ad-hoc command drift
