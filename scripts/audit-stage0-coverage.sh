#!/usr/bin/env bash
#
# Scans toolchain/*.osty for usage of patterns the stage0 emitter
# (P0-P20) does not cover. Output is a per-file count of pattern
# occurrences plus a grand total. Used by B2 to scope the rewrite
# work needed to fit toolchain through stage0.
#
# Stage0 covers (per `docs/osty_self_bootstrap_design.md` P0-P20):
#   - trivial main / arithmetic
#   - if-else with phi (incl. struct phi P17, `||` head P18, else-if chain P19/P20)
#   - while / for-in-range
#   - struct field access
#   - list literal/index/len
#   - string concat, String == String (P16)
#   - aggregate constructor
#
# Stage0 does NOT cover yet:
#   - match expressions (payload-bearing especially)
#   - closure literals (|x| ...)
#   - generic functions/types (<T>)
#   - ? error propagation (early-return form)
#   - method dispatch on interfaces
#   - Map operations
#
# Usage:
#   scripts/audit-stage0-coverage.sh [toolchain-dir]
#
# Output is grep-friendly TSV: <file>\t<pattern>\t<count>
# Summary at end includes:
#   1. Legacy totals (back-compat with prior B2 PR baselines)
#   2. Match-arm classification (payload-bearing vs payload-less vs other)
#   3. `?` breakdown (early-return vs `?.` chain vs `??` coalesce)
# These breakdowns drive B2.2/B2.3 stage0-unlock priority decisions.

set -euo pipefail

dir="${1:-toolchain}"

if [[ ! -d "$dir" ]]; then
	printf 'audit-stage0: directory not found: %s\n' "$dir" >&2
	exit 2
fi

# count_pattern: greps file for label/pattern, accumulates into the
# global TOTAL associative array (keyed by label) and emits a TSV row
# when count > 0. Centralises both the count and the per-file display.
declare -A TOTAL
count_pattern() {
	local file="$1"
	local label="$2"
	local pattern="$3"
	local n
	n=$(grep -cE "$pattern" "$file" 2>/dev/null || true)
	if [[ -z "$n" ]]; then n=0; fi
	TOTAL[$label]=$((${TOTAL[$label]:-0} + n))
	if [[ "$n" -gt 0 ]]; then
		printf '%s\t%s\t%s\n' "$file" "$label" "$n"
	fi
}

total_files=0
total_funcs=0

while IFS= read -r -d '' file; do
	# Skip _test.osty so the audit reflects production scope.
	case "$file" in
		*_test.osty) continue ;;
	esac
	total_files=$((total_files + 1))

	# Function declarations (rough): `pub fn ` or leading `fn `.
	funcs=$(grep -cE '^[[:space:]]*(pub[[:space:]]+)?fn[[:space:]]' "$file" 2>/dev/null || true)
	if [[ -z "$funcs" ]]; then funcs=0; fi
	total_funcs=$((total_funcs + funcs))

	# `match X {` at start of a line (excludes "match" inside strings).
	count_pattern "$file" "match" '^[[:space:]]*match[[:space:]]+'

	# Match-arm classifiers — best-effort regex; can't distinguish
	# nested-match arms but accurate enough to scope B2 work.
	# Head shape determines class: `Variant`, `Variant(...)`, `_`,
	# literal int, literal string, or range.
	count_pattern "$file" "arm-payload-less" \
		'^[[:space:]]*[A-Z][a-zA-Z0-9_]*(\.[A-Z][a-zA-Z0-9_]*)*[[:space:]]*->'
	count_pattern "$file" "arm-payload-bearing" \
		'^[[:space:]]*[A-Z][a-zA-Z0-9_]*\([^)]*\)[[:space:]]*->'
	count_pattern "$file" "arm-wildcard" '^[[:space:]]*_[[:space:]]*->'
	count_pattern "$file" "arm-literal-int" \
		'^[[:space:]]*-?[0-9_]+[[:space:]]*->'
	count_pattern "$file" "arm-literal-str" \
		'^[[:space:]]*"[^"]*"[[:space:]]*->'
	count_pattern "$file" "arm-range" \
		'^[[:space:]]*[-0-9_]+\.\.=?[-0-9_]+[[:space:]]*->'

	count_pattern "$file" "closure" \
		'\|[a-zA-Z_][a-zA-Z0-9_, ]*\|[[:space:]]*(\{|->)'
	count_pattern "$file" "generic-fn" \
		'fn[[:space:]]+[a-zA-Z_][a-zA-Z0-9_]*<[A-Z]'

	# Legacy ?-propagate (over-counts type optionals — see breakdown
	# rows for precision).
	count_pattern "$file" "?-propagate" '\?[[:space:].]'

	# `?` breakdown. Early-return is `expr?` at line end where the
	# preceding char is `)` / `]` / lowercase / `}` — excludes type
	# optionals (`String?`, `T?`).
	count_pattern "$file" "?-early-return" '(\)|\]|[a-z0-9_}])\?$'
	count_pattern "$file" "?-optional-chain" '\?\.'
	count_pattern "$file" "?-coalesce" '\?\?'
done < <(find "$dir" -maxdepth 1 -type f -name '*.osty' -print0)

m=${TOTAL[match]:-0}
c=${TOTAL[closure]:-0}
g=${TOTAL[generic-fn]:-0}
p=${TOTAL[?-propagate]:-0}

printf '\n=== summary (production .osty under %s) ===\n' "$dir"
printf 'files            %s\n' "$total_files"
printf 'fn declarations  %s\n' "$total_funcs"
printf 'match exprs      %s\n' "$m"
printf 'closures         %s\n' "$c"
printf 'generic fns      %s\n' "$g"
printf '? propagations   %s  (legacy regex; see breakdown below)\n' "$p"
printf 'total non-stage0 %s\n' "$((m + c + g + p))"

printf '\n=== match-arm classification ===\n'
printf 'arm payload-less    %s  (Variant -> ...; mechanical if-else rewrite)\n' \
	"${TOTAL[arm-payload-less]:-0}"
printf 'arm payload-bearing %s  (Variant(x) -> ...; needs stage0 unlock or helper extract)\n' \
	"${TOTAL[arm-payload-bearing]:-0}"
printf 'arm wildcard        %s  (_ -> ...; trailing else)\n' \
	"${TOTAL[arm-wildcard]:-0}"
printf 'arm literal-int     %s\n' "${TOTAL[arm-literal-int]:-0}"
printf 'arm literal-str     %s\n' "${TOTAL[arm-literal-str]:-0}"
printf 'arm range           %s\n' "${TOTAL[arm-range]:-0}"

printf '\n=== ? breakdown ===\n'
printf '?-early-return     %s  (expr? at line end; Result/Option propagation; needs stage0 unlock)\n' \
	"${TOTAL[?-early-return]:-0}"
printf '?-optional-chain   %s  (?.field; Option-unwrap chains)\n' \
	"${TOTAL[?-optional-chain]:-0}"
printf '?-coalesce         %s  (?? fallback; nil-coalesce operator)\n' \
	"${TOTAL[?-coalesce]:-0}"

# Per-match classification (awk pass with brace-depth tracking). For
# each `match X {` scope, classifies based on arms at depth==1:
#   - payload-only:   every arm is `Variant(x) -> ...`
#   - bareonly:       every arm is `Variant -> ...` or `_ -> ...`
#   - mixed:          payload arms AND non-payload arms
#   - literal:        literal/range arms (can mix with `_`)
#
# Brace tracking is approximate (string interpolation may skew counts
# by a few %), but enough to drive B2.2-vs-B2.3 unlock priority.

printf '\n=== per-match classification ===\n'
find "$dir" -maxdepth 1 -type f -name '*.osty' -not -name '*_test.osty' -print0 |
	xargs -0 awk '
BEGIN {
	in_match = 0
	depth = 0
	has_payload = 0
	has_bare = 0
	has_literal = 0
}

{
	line = $0
	# Strip line comment
	sub(/\/\/.*$/, "", line)
	# Strip string literals — naive but sufficient for brace counting
	gsub(/"([^"\\]|\\.)*"/, "", line)
}

# match start (only when not already in one)
!in_match && /^[[:space:]]*match[[:space:]]+/ {
	in_match = 1
	depth = 1
	has_payload = 0
	has_bare = 0
	has_literal = 0
	next
}

in_match {
	# Arm classification at depth == 1 (arms of THIS match, not nested)
	if (depth == 1 && line ~ /->/) {
		head = line
		sub(/^[[:space:]]+/, "", head)
		sub(/[[:space:]]*->.*/, "", head)

		if (head ~ /^[A-Z][a-zA-Z0-9_]*\(/) {
			has_payload = 1
		} else if (head ~ /^[A-Z][a-zA-Z0-9_]*(\.[A-Z][a-zA-Z0-9_]*)*$/) {
			has_bare = 1
		} else if (head == "_" || head ~ /^_,/ || head ~ /^_[[:space:]]/) {
			# wildcard — neutral
		} else if (head ~ /^-?[0-9_]+$/ || head ~ /^"[^"]*"$/ || head ~ /\.\.=?/) {
			has_literal = 1
		}
	}

	# Brace depth update
	n_open = gsub(/\{/, "{", line)
	n_close = gsub(/\}/, "}", line)
	depth += n_open - n_close

	if (depth <= 0) {
		match_total++
		if (has_payload && (has_bare || has_literal)) match_mixed++
		else if (has_payload)                          match_payload_only++
		else if (has_literal)                          match_literal++
		else                                           match_bareonly++
		in_match = 0
		depth = 0
	}
}

END {
	printf "matches-total          %d\n", match_total
	printf "matches-bareonly       %d  (Variant + wildcard arms only; pure mechanical if-else)\n", match_bareonly
	printf "matches-payload-only   %d  (Variant(x) arms only; needs unlock OR helper extract)\n", match_payload_only
	printf "matches-mixed          %d  (mix of payload + bare/literal; case-by-case)\n", match_mixed
	printf "matches-literal        %d  (literal/range arms; usually fits if-else cascade)\n", match_literal
}
'
