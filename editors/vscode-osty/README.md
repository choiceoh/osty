# Osty VS Code Extension

Local VS Code language support for Osty. The extension starts the language
server with:

```sh
osty lsp
```

## Local Install

From the repository root:

```sh
npm --prefix editors/vscode-osty run install-local
```

The installer downloads runtime dependencies if needed, then symlinks this
folder into the local VS Code extension directory.

## Settings

- `osty.languageServer.command`: command to start the server, default `osty`
- `osty.languageServer.args`: command arguments, default `["lsp"]`
- `osty.languageServer.trace.server`: LSP trace level
