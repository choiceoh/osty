#!/usr/bin/env bash
# Build every program in benchmarks/programs/ in release mode and assert
# byte-for-byte output parity between Go and Osty for each.
#
#     ./build-all.sh
#
# Programs are auto-discovered: any directory containing both go/ and osty/
# subdirs is treated as a benchmark pair.

set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"
osty_bin="${OSTY_BIN:-$repo_root/.bin/osty}"

if [ ! -x "$osty_bin" ]; then
    echo "osty binary not found at $osty_bin — run 'just build' first" >&2
    exit 1
fi

failed=()

for prog_dir in "$here"/*/; do
    prog=$(basename "$prog_dir")
    if [ ! -d "$prog_dir/go" ] || [ ! -d "$prog_dir/osty" ]; then
        continue
    fi

    echo ">> $prog"

    echo "   building Go..."
    (cd "$prog_dir/go" && go build -ldflags="-s -w" -o "$prog_dir/bin-go-release" .)

    echo "   building Osty..."
    (cd "$prog_dir/osty" && "$osty_bin" build --release >/dev/null)

    osty_artifact="$prog_dir/osty/.osty/out/release/llvm/$prog"

    echo "   verifying output parity..."
    go_out=$(mktemp) ; osty_out=$(mktemp)
    "$prog_dir/bin-go-release" > "$go_out"
    "$osty_artifact" > "$osty_out"
    if diff -q "$go_out" "$osty_out" >/dev/null; then
        echo "   OK"
    else
        echo "   MISMATCH"
        diff "$go_out" "$osty_out" | head -20
        failed+=("$prog")
    fi
    rm -f "$go_out" "$osty_out"
done

if [ ${#failed[@]} -gt 0 ]; then
    echo
    echo "FAILED: ${failed[*]}" >&2
    exit 1
fi

echo
echo "All programs built and matched."
