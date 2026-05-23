#!/usr/bin/env bash
# Evaluate the quality of AI-authored Osty source files.
#
# Input layout:
#   <cases-dir>/
#     <agent-name>/
#       <task>.osty
#       ...
#     <agent-name>/
#       ...
#
# Each .osty file is graded against three axes, using the existing
# osty CLI as the ground-truth checker:
#
#   compile  (50 pts) — parse + resolve + check produce zero errors
#   clean    (30 pts) — `osty airepair` does NOT rewrite the file
#                       (i.e. no AI-slip patterns: foreign syntax,
#                       Python/JS/Go bleed-through, etc.)
#   lint     (20 pts) — `osty lint` produces zero warnings
#
# Per-agent score = weighted average of per-file pass rates.
# Per-agent breakdown also lists the most common slip categories
# (function_keyword, python_for_block, js_strict_equality, ...).
#
# Outputs:
#   --out-dir/scorecard.md    — human-readable Markdown report
#   --out-dir/scorecard.json  — machine-readable aggregate (CI trending)
#   --out-dir/per-file.ndjson — one record per evaluated file
#
# Args:
#   $1 = cases-dir   (required)
#
# Flags (env or CLI, CLI wins):
#   --osty PATH      osty binary (default: $OSTY_BIN or .bin/osty)
#   --out-dir DIR    output directory (default: tmp/agent-eval)
#   --quiet          suppress per-file progress

set -u

osty="${OSTY_BIN:-.bin/osty}"
out_dir="tmp/agent-eval"
quiet=0
cases_dir=""

while [ $# -gt 0 ]; do
	case "$1" in
	--osty)
		osty="$2"
		shift 2
		;;
	--out-dir)
		out_dir="$2"
		shift 2
		;;
	--quiet)
		quiet=1
		shift
		;;
	-h | --help)
		sed -n '2,30p' "$0"
		exit 0
		;;
	-*)
		echo "agent-eval: unknown flag: $1" >&2
		exit 2
		;;
	*)
		if [ -z "$cases_dir" ]; then
			cases_dir="$1"
		else
			echo "agent-eval: unexpected positional arg: $1" >&2
			exit 2
		fi
		shift
		;;
	esac
done

if [ -z "$cases_dir" ]; then
	echo "agent-eval: missing <cases-dir>" >&2
	echo "usage: agent-eval.sh [--osty PATH] [--out-dir DIR] [--quiet] <cases-dir>" >&2
	exit 2
fi

if [ ! -d "$cases_dir" ]; then
	echo "agent-eval: not a directory: $cases_dir" >&2
	exit 2
fi

if [ ! -x "$osty" ]; then
	echo "agent-eval: osty binary not found or not executable: $osty" >&2
	echo "hint: build first (\`just build\`) or pass --osty PATH" >&2
	exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
	echo "agent-eval: jq is required (apt install jq / brew install jq)" >&2
	exit 2
fi

mkdir -p "$out_dir"
per_file="$out_dir/per-file.ndjson"
scorecard_md="$out_dir/scorecard.md"
scorecard_json="$out_dir/scorecard.json"
: >"$per_file"

# Weight policy. Edit here to retune.
W_COMPILE=50
W_CLEAN=30
W_LINT=20

log() {
	if [ "$quiet" -eq 0 ]; then
		printf '%s\n' "$*" >&2
	fi
}

# Probe one .osty file. Emits one NDJSON record to $per_file.
# Args: $1 = agent, $2 = file (path relative to cwd)
probe_file() {
	local agent="$1" file="$2"
	local airepair_json check_json lint_json
	local parse_err resolve_err check_err lint_warn
	local changed status changes_n categories
	local compile_pass clean_pass lint_pass file_score

	# airepair runs the front end internally and reports its own probe stats,
	# so we can derive parse/resolve/check error counts from it without
	# invoking `osty check` separately.
	#
	# NB: the airepair `--json` flag is a subcommand-level flag, so it must
	# come AFTER `airepair`, not as a top-level `osty --json airepair`.
	airepair_json="$("$osty" airepair --json "$file" 2>/dev/null || echo '{}')"
	if [ -z "$airepair_json" ]; then
		airepair_json='{}'
	fi

	parse_err=$(printf '%s' "$airepair_json" | jq -r '.before.parse.errors // 0')
	resolve_err=$(printf '%s' "$airepair_json" | jq -r '.before.resolve.errors // 0')
	check_err=$(printf '%s' "$airepair_json" | jq -r '.before.check.errors // 0')
	changed=$(printf '%s' "$airepair_json" | jq -r '.changed // false')
	status=$(printf '%s' "$airepair_json" | jq -r '.status // "unknown"')
	changes_n=$(printf '%s' "$airepair_json" | jq -r '(.changes // []) | length')
	categories=$(printf '%s' "$airepair_json" | jq -c '[((.changes // [])[] | .Kind)]')

	# Lint emits NDJSON of diagnostic records on stderr (one per line),
	# plus a trailing human summary line we have to strip. Severity 1 is
	# a warning; Severity 0 would be a hard error already counted by
	# airepair's before-stats.
	lint_json="$("$osty" --json lint "$file" 2>&1 1>/dev/null | grep '^{' || true)"
	if [ -n "$lint_json" ]; then
		lint_warn=$(printf '%s\n' "$lint_json" | jq -s '[.[] | select(.Severity == 1)] | length' 2>/dev/null || echo 0)
	else
		lint_warn=0
	fi

	if [ "$parse_err" -eq 0 ] && [ "$resolve_err" -eq 0 ] && [ "$check_err" -eq 0 ]; then
		compile_pass=1
	else
		compile_pass=0
	fi
	if [ "$changed" = "false" ]; then
		clean_pass=1
	else
		clean_pass=0
	fi
	if [ "$lint_warn" -eq 0 ]; then
		lint_pass=1
	else
		lint_pass=0
	fi

	file_score=$((compile_pass * W_COMPILE + clean_pass * W_CLEAN + lint_pass * W_LINT))

	jq -nc \
		--arg agent "$agent" \
		--arg file "$file" \
		--arg status "$status" \
		--argjson parse "$parse_err" \
		--argjson resolve "$resolve_err" \
		--argjson check "$check_err" \
		--argjson lint "$lint_warn" \
		--argjson changes "$changes_n" \
		--argjson categories "$categories" \
		--argjson compile_pass "$compile_pass" \
		--argjson clean_pass "$clean_pass" \
		--argjson lint_pass "$lint_pass" \
		--argjson score "$file_score" \
		'{agent:$agent, file:$file, status:$status,
		  parse_errors:$parse, resolve_errors:$resolve, check_errors:$check,
		  lint_warnings:$lint, airepair_changes:$changes, slip_categories:$categories,
		  compile_pass:$compile_pass, clean_pass:$clean_pass, lint_pass:$lint_pass,
		  score:$score}' >>"$per_file"

	log "  [$agent] $file → score=$file_score (compile=$compile_pass clean=$clean_pass lint=$lint_pass slips=$changes_n warns=$lint_warn)"
}

# ---- main loop ----

agents_found=0
files_total=0
shopt -s nullglob

for agent_path in "$cases_dir"/*/; do
	[ -d "$agent_path" ] || continue
	agent="$(basename "$agent_path")"
	agents_found=$((agents_found + 1))
	log "agent: $agent"
	count_for_agent=0
	while IFS= read -r -d '' file; do
		probe_file "$agent" "$file"
		count_for_agent=$((count_for_agent + 1))
		files_total=$((files_total + 1))
	done < <(find "$agent_path" -type f -name '*.osty' -print0 | sort -z)
	if [ "$count_for_agent" -eq 0 ]; then
		log "  (no .osty files under $agent_path)"
	fi
done

if [ "$agents_found" -eq 0 ]; then
	echo "agent-eval: no agent directories under $cases_dir" >&2
	echo "hint: layout is <cases-dir>/<agent-name>/*.osty" >&2
	exit 2
fi

if [ "$files_total" -eq 0 ]; then
	echo "agent-eval: $agents_found agent directory(ies) found but contain no .osty files" >&2
	jq -n '{global:{agents:0, files:0, mean_score:0, compile_pass_rate:0, clean_pass_rate:0, lint_pass_rate:0}, agents:[]}' >"$scorecard_json"
	{
		echo "# Agent Quality Scorecard"
		echo
		echo "_No .osty files found under \`${cases_dir}\`._"
	} >"$scorecard_md"
	echo "  → ${scorecard_md}"
	echo "  → ${scorecard_json}"
	exit 0
fi

# ---- aggregate ----

# Build per-agent rollups from per-file.ndjson via jq slurp.
agent_rollup="$(jq -s '
	def round100($n): ($n * 100 | floor) / 100;
	group_by(.agent) | map({
		agent: .[0].agent,
		files: length,
		compile_pass_rate: (round100(([.[] | .compile_pass] | add) / length)),
		clean_pass_rate:   (round100(([.[] | .clean_pass]   | add) / length)),
		lint_pass_rate:    (round100(([.[] | .lint_pass]    | add) / length)),
		avg_score:         (round100(([.[] | .score]         | add) / length)),
		total_parse_errors:   ([.[] | .parse_errors]   | add),
		total_resolve_errors: ([.[] | .resolve_errors] | add),
		total_check_errors:   ([.[] | .check_errors]   | add),
		total_lint_warnings:  ([.[] | .lint_warnings]  | add),
		total_slips:          ([.[] | .airepair_changes] | add),
		slip_histogram: (
			[.[] | .slip_categories[]] | group_by(.) |
			map({kind: .[0], n: length}) | sort_by(-.n)
		),
		worst_files: (
			sort_by(.score) | .[0:3] | map({file:.file, score:.score, status:.status})
		)
	}) | sort_by(-.avg_score)
' "$per_file")"

global_rollup="$(jq -s '
	def round100($n): ($n * 100 | floor) / 100;
	{
		agents: ([.[].agent] | unique | length),
		files: length,
		mean_score: (round100(([.[] | .score] | add) / length)),
		compile_pass_rate: (round100(([.[] | .compile_pass] | add) / length)),
		clean_pass_rate:   (round100(([.[] | .clean_pass]   | add) / length)),
		lint_pass_rate:    (round100(([.[] | .lint_pass]    | add) / length))
	}
' "$per_file")"

jq -n \
	--argjson global "$global_rollup" \
	--argjson agents "$agent_rollup" \
	'{global:$global, agents:$agents}' >"$scorecard_json"

# ---- render Markdown ----

{
	echo "# Agent Quality Scorecard"
	echo
	echo "_Generated by \`scripts/agent-eval.sh\`. Refresh with \`just agent-eval <cases-dir>\`._"
	echo
	echo "Weights: compile=${W_COMPILE} · clean (no airepair slip)=${W_CLEAN} · lint=${W_LINT}"
	echo
	echo "## Global"
	echo
	echo "| metric | value |"
	echo "|---|---|"
	jq -r '
		"| agents | \(.agents) |",
		"| files  | \(.files) |",
		"| mean score (0–100) | \(.mean_score) |",
		"| compile pass rate  | \((.compile_pass_rate * 100 | floor))% |",
		"| clean   pass rate  | \((.clean_pass_rate * 100   | floor))% |",
		"| lint    pass rate  | \((.lint_pass_rate * 100    | floor))% |"
	' <<<"$global_rollup"
	echo
	echo "## Per-agent ranking"
	echo
	echo "| rank | agent | files | score | compile% | clean% | lint% | slips |"
	echo "|---:|---|---:|---:|---:|---:|---:|---:|"
	jq -r '
		to_entries | .[] |
		"| \(.key + 1) | \(.value.agent) | \(.value.files) | \(.value.avg_score) | \((.value.compile_pass_rate * 100 | floor))% | \((.value.clean_pass_rate * 100 | floor))% | \((.value.lint_pass_rate * 100 | floor))% | \(.value.total_slips) |"
	' <<<"$agent_rollup"
	echo
	echo "## Per-agent detail"
	echo
	jq -r '
		.[] |
		"### \(.agent)\n",
		"- score: **\(.avg_score)** / 100",
		"- files: \(.files)",
		"- errors: parse=\(.total_parse_errors) resolve=\(.total_resolve_errors) check=\(.total_check_errors)",
		"- lint warnings: \(.total_lint_warnings)",
		"- airepair slips: \(.total_slips)",
		(
			if (.slip_histogram | length) > 0 then
				"\n**Slip categories:**\n",
				(.slip_histogram | .[] | "- `\(.kind)` × \(.n)")
			else
				"\n_No AI slips detected._"
			end
		),
		(
			if (.worst_files | length) > 0 then
				"\n**Worst files:**\n",
				(.worst_files | .[] | "- `\(.file)` — score \(.score) (\(.status))")
			else empty end
		),
		""
	' <<<"$agent_rollup"
} >"$scorecard_md"

# ---- console summary ----

echo "agent-eval: scanned ${files_total} file(s) across ${agents_found} agent(s)"
echo "  → ${scorecard_md}"
echo "  → ${scorecard_json}"
echo "  → ${per_file}"
