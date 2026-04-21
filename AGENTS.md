# Repo Defaults

- Prefer `just` recipes for common loops instead of ad-hoc shell commands.
- Fast verification first: use `just front`, `just short`, or focused `just lsp <regex>` / `just gen <regex>` before broader sweeps.
- Use `just build-checker` when you want the native checker prebuilt for repeated runs; `.envrc` points `OSTY_NATIVE_CHECKER_BIN` at the prebuilt binary when present and otherwise falls back to `scripts/osty-native-checker`.
- Use `just repair-check`, `just ci`, and `just verify-selfhost` for wider validation when touching CLI, toolchain, or generated outputs.
- Prefer targeted `go test -count=1 -vet=off <pkg>` over whole-tree runs unless the change is broad; run `just vet` separately when needed.
- Local generated artifacts live in `.bin/`, `.osty/`, `.profiles/`, and `.direnv/`.
