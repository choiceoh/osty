#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
extension_dir="$(cd "$script_dir/.." && pwd)"

if ! command -v npm >/dev/null 2>&1; then
  echo "npm is required to install the Osty VS Code extension dependencies." >&2
  exit 1
fi

if [ ! -d "$extension_dir/node_modules/vscode-languageclient" ]; then
  npm --prefix "$extension_dir" install --omit=dev
fi

read_field() {
  node -e "const p=require(process.argv[1]); console.log(p[process.argv[2]])" "$extension_dir/package.json" "$1"
}

publisher="$(read_field publisher)"
name="$(read_field name)"
version="$(read_field version)"
install_name="${publisher}.${name}-${version}"

targets=()
if [ -n "${VSCODE_EXTENSIONS:-}" ]; then
  targets+=("$VSCODE_EXTENSIONS")
fi
targets+=("$HOME/.vscode/extensions")
if [ -d "$HOME/.vscode-server" ]; then
  targets+=("$HOME/.vscode-server/extensions")
fi

seen=""
for target in "${targets[@]}"; do
  case ":$seen:" in
    *":$target:"*) continue ;;
  esac
  seen="$seen:$target"
  mkdir -p "$target"
  dest="$target/$install_name"
  rm -rf "$dest"
  ln -s "$extension_dir" "$dest"
  echo "Installed $install_name -> $dest"
done

echo "Reload VS Code to activate the Osty extension."
