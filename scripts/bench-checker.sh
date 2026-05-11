#!/usr/bin/env bash
# Benchmark embedded vs subprocess native-checker across package shapes.
#
# Output:
#   .profiles/checker-bench/<shape>-<cache>-<mode>.json   (raw hyperfine export)
#   .profiles/checker-bench/summary.md                    (markdown summary table)
#   stdout                                                (same summary, pasteable)
#
# Re-run from scratch:
#   rm -rf .profiles/checker-bench && bash scripts/bench-checker.sh

set -u

# shellcheck disable=SC1007  # CDPATH= is an intentional one-shot unset
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT" || exit 1

OUT_DIR=".profiles/checker-bench"
mkdir -p "$OUT_DIR"

OSTY_BIN=".bin/osty"
CHECKER_BIN=".osty/bin/osty-native-checker"

# ---- prereqs ----------------------------------------------------------------

need() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "bench-checker: $1 not on PATH" >&2
		exit 1
	fi
}
need hyperfine
need jq
need go

if [ ! -x "$OSTY_BIN" ]; then
	echo ">> building $OSTY_BIN"
	go build -o "$OSTY_BIN" ./cmd/osty
fi
if [ ! -x "$CHECKER_BIN" ]; then
	echo ">> building $CHECKER_BIN"
	mkdir -p "$(dirname "$CHECKER_BIN")"
	go build -o "$CHECKER_BIN" ./cmd/osty-native-checker
fi

CHECKER_ABS="$ROOT/$CHECKER_BIN"
OSTY_ABS="$ROOT/$OSTY_BIN"

# ---- env snapshot -----------------------------------------------------------

snapshot_env() {
	{
		echo "## Environment"
		echo
		echo "| key | value |"
		echo "|---|---|"
		echo "| date | $(date -u +%Y-%m-%dT%H:%M:%SZ) |"
		echo "| commit | $(git rev-parse HEAD) |"
		echo "| branch | $(git rev-parse --abbrev-ref HEAD) |"
		if [ "$(uname -s)" = "Darwin" ]; then
			echo "| os | macOS $(sw_vers -productVersion) ($(uname -m)) |"
			echo "| cpu | $(sysctl -n machdep.cpu.brand_string) |"
			echo "| cores | $(sysctl -n hw.physicalcpu) phys / $(sysctl -n hw.logicalcpu) log |"
			mem_bytes=$(sysctl -n hw.memsize)
			echo "| ram | $((mem_bytes / 1024 / 1024 / 1024)) GiB |"
		else
			echo "| os | $(uname -srm) |"
			echo "| cpu | $(grep -m1 'model name' /proc/cpuinfo | sed 's/.*: //') |"
			echo "| cores | $(nproc) logical |"
		fi
		echo "| go | $(go version | awk '{print $3}') |"
		echo "| hyperfine | $(hyperfine --version | awk '{print $2}') |"
		echo "| osty mtime | $(stat -f '%Sm' "$OSTY_BIN" 2>/dev/null || stat -c '%y' "$OSTY_BIN") |"
		echo "| checker mtime | $(stat -f '%Sm' "$CHECKER_BIN" 2>/dev/null || stat -c '%y' "$CHECKER_BIN") |"
		echo
	} >"$OUT_DIR/env.md"
}
snapshot_env

# ---- workload matrix --------------------------------------------------------
#
# shape:tier:path
#   tier=micro|small|large|toolchain (used for runs budget)
#   path is what `osty check <path>` will see
#
SHAPES=(
	"ffi:micro:examples/ffi"
	"concurrency:small:examples/concurrency"
	"gc:large:examples/gc"
	"toolchain:toolchain:toolchain"
)

# warm spot-check: subset of shapes
WARM_SHAPES=(
	"concurrency:small:examples/concurrency"
	"gc:large:examples/gc"
)

runs_for_tier() {
	case "$1" in
	toolchain) echo "--min-runs 5 --max-runs 10" ;;
	large) echo "--min-runs 8 --max-runs 15" ;;
	*) echo "--min-runs 10 --max-runs 30" ;;
	esac
}

# ---- measurement ------------------------------------------------------------
#
# Naming: <shape>-<cache>-<mode>
#   cache: off  = OSTY_CHECKER_CACHE=0 (no cache at all)
#          cold = cache infra on, but `--prepare` wipes per iteration
#          warm = cache infra on, no prepare, warmups prime it
#   mode:  embedded | subprocess

run_one() {
	local label="$1"
	local cache_mode="$2" # off|cold|warm
	local pkg_path="$3"
	local runs_flags="$4"

	local prep_arg=()
	local cache_env_e=""
	local cache_env_s=""
	case "$cache_mode" in
	off)
		cache_env_e="OSTY_CHECKER_CACHE=0"
		cache_env_s="OSTY_CHECKER_CACHE=0"
		;;
	cold)
		prep_arg=(--prepare "rm -rf '$pkg_path/.osty/cache/checker'")
		;;
	warm)
		: # cache on, no prepare, warmup primes
		;;
	*)
		echo "unknown cache_mode: $cache_mode" >&2
		exit 1
		;;
	esac

	# shellcheck disable=SC2206  # intentional word-split of runs_flags
	local runs_arr=($runs_flags)

	local cmd_embedded="$cache_env_e $OSTY_ABS check $pkg_path"
	local cmd_subprocess="$cache_env_s OSTY_NATIVE_CHECKER_BIN=$CHECKER_ABS $OSTY_ABS check $pkg_path"

	echo ""
	echo "==> $label cache=$cache_mode path=$pkg_path"
	hyperfine \
		--warmup 2 \
		"${runs_arr[@]}" \
		"${prep_arg[@]}" \
		--ignore-failure \
		--export-json "$OUT_DIR/$label-$cache_mode.json" \
		-n embedded "$cmd_embedded" \
		-n subprocess "$cmd_subprocess" || true
}

for entry in "${SHAPES[@]}"; do
	IFS=':' read -r shape tier path <<<"$entry"
	runs_str=$(runs_for_tier "$tier")
	run_one "$shape" off "$path" "$runs_str"
	run_one "$shape" cold "$path" "$runs_str"
done

for entry in "${WARM_SHAPES[@]}"; do
	IFS=':' read -r shape tier path <<<"$entry"
	runs_str=$(runs_for_tier "$tier")
	run_one "$shape" warm "$path" "$runs_str"
done

# ---- summary ----------------------------------------------------------------

format_ms() {
	# seconds (float) -> "1234.5 ms" or "12.34 s" depending on magnitude
	local s="$1"
	awk -v s="$s" 'BEGIN {
		if (s + 0 < 1.0) printf "%.1f ms", s * 1000;
		else printf "%.2f s", s;
	}'
}

emit_row() {
	local shape="$1" cache="$2" json="$3"
	[ -f "$json" ] || return 0
	local emb_mean emb_std emb_min sub_mean sub_std sub_min ratio
	emb_mean=$(jq '.results[] | select(.command=="embedded") | .mean' "$json")
	emb_std=$(jq '.results[] | select(.command=="embedded") | .stddev' "$json")
	emb_min=$(jq '.results[] | select(.command=="embedded") | .min' "$json")
	sub_mean=$(jq '.results[] | select(.command=="subprocess") | .mean' "$json")
	sub_std=$(jq '.results[] | select(.command=="subprocess") | .stddev' "$json")
	sub_min=$(jq '.results[] | select(.command=="subprocess") | .min' "$json")
	ratio=$(awk -v s="$sub_mean" -v e="$emb_mean" 'BEGIN { if (e>0) printf "%.2f", s/e; else print "n/a" }')

	printf '| %s | %s | %s ± %s (min %s) | %s ± %s (min %s) | %s× |\n' \
		"$shape" "$cache" \
		"$(format_ms "$emb_mean")" "$(format_ms "$emb_std")" "$(format_ms "$emb_min")" \
		"$(format_ms "$sub_mean")" "$(format_ms "$sub_std")" "$(format_ms "$sub_min")" \
		"$ratio"
}

build_summary() {
	{
		cat "$OUT_DIR/env.md"
		echo "## Cold matrix"
		echo
		echo "| shape | cache | embedded (mean ± σ) | subprocess (mean ± σ) | ratio |"
		echo "|---|---|---|---|---|"
		for entry in "${SHAPES[@]}"; do
			IFS=':' read -r shape _tier _path <<<"$entry"
			emit_row "$shape" "off" "$OUT_DIR/$shape-off.json"
			emit_row "$shape" "cold" "$OUT_DIR/$shape-cold.json"
		done
		echo
		echo "## Warm spot-check"
		echo
		echo "| shape | cache | embedded (mean ± σ) | subprocess (mean ± σ) | ratio |"
		echo "|---|---|---|---|---|"
		for entry in "${WARM_SHAPES[@]}"; do
			IFS=':' read -r shape _tier _path <<<"$entry"
			emit_row "$shape" "warm" "$OUT_DIR/$shape-warm.json"
		done
		echo
		echo "## Legend"
		echo
		echo "- **cache=off**: \`OSTY_CHECKER_CACHE=0\`, no on-disk cache layer."
		echo "- **cache=cold**: cache layer active, \`--prepare\` wipes cache before every iteration."
		echo "- **cache=warm**: cache layer active, warmups prime entries, measured iterations hit cache."
		echo "- **ratio**: subprocess.mean / embedded.mean. <1.0 means subprocess faster."
	} >"$OUT_DIR/summary.md"

	echo
	echo "================ SUMMARY ================"
	cat "$OUT_DIR/summary.md"
	echo
	echo ">> wrote $OUT_DIR/summary.md"
}

build_summary
