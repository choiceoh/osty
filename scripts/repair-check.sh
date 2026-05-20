#!/usr/bin/env bash
# Run `osty repair --check` on every tracked .osty file, skipping files
# whose content was already verified under this osty build.
#
# Cache layout:
#   .osty/cache/repair-check/<osty-sha-16>/<file-sha-32>
# Each cache entry is an empty marker file. Stale <osty-sha> dirs from
# previous builds are GC'd on each run.
#
# Override parallelism: REPAIR_CHECK_PARALLEL=N (default 4).
# Force full re-check: rm -rf .osty/cache/repair-check

set -u

osty="${1:-.bin/osty}"
parallel_n="${REPAIR_CHECK_PARALLEL:-4}"

if [ ! -x "$osty" ]; then
	echo "repair-check: $osty not built" >&2
	exit 1
fi

osty_sha=$(shasum -a 256 "$osty" | head -c 16)
cache_root=".osty/cache/repair-check"
cache_dir="$cache_root/$osty_sha"
mkdir -p "$cache_dir"

find "$cache_root" -mindepth 1 -maxdepth 1 -type d ! -name "$osty_sha" \
	-exec rm -rf {} + 2>/dev/null || true

files=$(git ls-files '*.osty' |
	grep -vE '^(testdata/spec/negative/|internal/airepair/testdata/corpus/[^/]+\.input\.osty$)' || true)
[ -z "$files" ] && exit 0

# Single batched shasum (496 files: ~0.3s vs ~13s for per-file invocations).
hash_pairs=$(printf '%s\n' "$files" | xargs shasum -a 256)

# Pipe-separated marker|file pairs for entries that lack a cache hit.
# On Windows-style toolchains shasum opens files in binary mode and
# prefixes the filename with `*` (e.g. `<hash> *toolchain/ty.osty`).
# Strip the marker so `osty repair --check` receives a plain path.
needs_check=$(printf '%s\n' "$hash_pairs" | while read -r hash file; do
	marker="$cache_dir/${hash:0:32}"
	file=${file#\*}
	[ -f "$marker" ] && continue
	printf '%s|%s\n' "$marker" "$file"
done)
[ -z "$needs_check" ] && exit 0

printf '%s\n' "$needs_check" | xargs -n 1 -P "$parallel_n" -I {} bash -c '
	marker="${1%%|*}"
	file="${1#*|}"
	"$2" repair --check "$file" && : > "$marker"
' _ {} "$osty"
