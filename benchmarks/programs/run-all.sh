#!/usr/bin/env bash
# Run hyperfine wall-clock comparison for every program in
# benchmarks/programs/ and emit a summary table.
#
#     ./run-all.sh                # default --runs 5 --warmup 1
#     RUNS=20 WARMUP=3 ./run-all.sh
#
# Each program contributes one Go bench and one Osty bench in a single
# hyperfine invocation per program (so the Go/Osty pair is sampled
# adjacent in time, minimizing thermal drift between them).

set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
runs="${RUNS:-5}"
warmup="${WARMUP:-1}"

if ! command -v hyperfine >/dev/null 2>&1; then
    echo "hyperfine not found — install via 'brew install hyperfine'" >&2
    exit 1
fi

results_md="$here/.last-results.md"
: > "$results_md"
echo "# osty vs go — runtime program suite" >> "$results_md"
echo >> "$results_md"
echo "| program | go (ms) | osty (ms) | osty/go |" >> "$results_md"
echo "|---|---:|---:|---:|" >> "$results_md"

for prog_dir in "$here"/*/; do
    prog=$(basename "$prog_dir")
    if [ ! -d "$prog_dir/go" ] || [ ! -d "$prog_dir/osty" ]; then
        continue
    fi

    go_bin="$prog_dir/bin-go-release"
    osty_bin="$prog_dir/osty/.osty/out/release/llvm/$prog"

    if [ ! -x "$go_bin" ] || [ ! -x "$osty_bin" ]; then
        echo "[$prog] binaries missing — run ./build-all.sh first" >&2
        exit 1
    fi

    echo
    echo "============================================================"
    echo " $prog"
    echo "============================================================"

    json="$prog_dir/.last-run.json"
    hyperfine --warmup "$warmup" -N --runs "$runs" \
        --export-json "$json" \
        -n "go ($prog)" "$go_bin" \
        -n "osty ($prog)" "$osty_bin"

    if command -v jq >/dev/null 2>&1; then
        go_ms=$(jq -r '.results[0].mean * 1000' "$json")
        osty_ms=$(jq -r '.results[1].mean * 1000' "$json")
        ratio=$(awk -v g="$go_ms" -v o="$osty_ms" 'BEGIN { printf "%.1f", o/g }')
        printf "| %s | %.2f | %.2f | %sx |\n" "$prog" "$go_ms" "$osty_ms" "$ratio" >> "$results_md"
    fi
done

echo
echo "Summary written to $results_md"
[ -f "$results_md" ] && cat "$results_md"
