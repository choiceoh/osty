# AI tooling hints

- Prefer isolated task workspaces over editing directly on the repo's main checkout.
- In non-interactive shells, start task workspaces with `cd "$(aitask <slug>)"` and check state with `aistatus`.
- Use `aisave "<type: summary>"` to stage/commit task workspace changes; add `--push` only when you intentionally want the remote branch updated.
- When an agent can choose its shell, prefer a lightweight `bash` login shell over an interactive `zsh` session with extra hooks.
- Use `rg` for fast text search and `fd` for file discovery.
- Use `eza` when directory shape matters and `bat` for quick source previews.
- Use `jq` for JSON inspection/transforms and `yq` for YAML/TOML-style config work.
- Use `hyperfine` for repeatable benchmark comparisons.
- Prefer `watchexec`-based watch loops when you need save-and-rerun iteration.
- Use `ast-grep` when the task is structural rather than textual: API migrations, tree-aware refactors, or "find all nodes shaped like X" queries.
- Keep `rg` for fast text search, but prefer `ast-grep` before writing brittle regexes for Go, JS/TS, Rust, Python, shell, or other supported grammars.
- After creating or editing shell scripts, run `shfmt -w` first and `shellcheck` after formatting.
- Prefer `gotestsum` for larger Go test runs so failures are easier to scan and summarize.
- Use `tokei` when you need a quick size/profile snapshot of the repository.
- Use `ctags` from Homebrew `universal-ctags` when you need a lightweight symbol index outside normal LSP coverage.
- Skip TUI-first tools unless the task explicitly calls for them; plain CLI output is easier for agent workflows.
