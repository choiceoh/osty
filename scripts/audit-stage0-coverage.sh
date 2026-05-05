#!/usr/bin/env bash
#
# Scans toolchain/*.osty for usage of patterns the stage0 emitter
# (P0-P15) does not cover. Output is a per-file count of pattern
# occurrences plus a grand total. Used by B2 to scope the rewrite
# work needed to fit toolchain through stage0.
#
# Stage0 covers (per `docs/osty_self_bootstrap_design.md` P0-P15):
#   - trivial main / arithmetic
#   - if-else with phi
#   - while / for-in-range
#   - struct field access
#   - list literal/index/len
#   - string concat
#   - aggregate constructor
#
# Stage0 does NOT cover (P16+ frozen):
#   - match expressions
#   - closure literals (|x| ...)
#   - generic functions/types (<T>)
#   - ? error propagation
#   - method dispatch on interfaces
#   - Map operations
#
# Usage:
#   scripts/audit-stage0-coverage.sh [toolchain-dir]
#
# Output is grep-friendly TSV: <file>\t<pattern>\t<count>

set -euo pipefail

dir="${1:-toolchain}"

if [[ ! -d "$dir" ]]; then
	printf 'audit-stage0: directory not found: %s\n' "$dir" >&2
	exit 2
fi

count_pattern() {
	local file="$1"
	local label="$2"
	local pattern="$3"
	local n
	n=$(grep -cE "$pattern" "$file" 2>/dev/null || true)
	if [[ -z "$n" ]]; then n=0; fi
	if [[ "$n" -gt 0 ]]; then
		printf '%s\t%s\t%s\n' "$file" "$label" "$n"
	fi
}

total_match=0
total_closure=0
total_generic=0
total_propagate=0
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

	# `match X {` pattern at the start of a line (excludes
	# string-literal occurrences of "match").
	match_count=$(grep -cE '^[[:space:]]*match[[:space:]]+' "$file" 2>/dev/null || true)
	[[ -z "$match_count" ]] && match_count=0
	total_match=$((total_match + match_count))
	count_pattern "$file" "match" '^[[:space:]]*match[[:space:]]+'

	# Closure literal `|x|` or `|x, y|` — excludes bitwise-or.
	# Looks for the `|...|` between paren and `->` or `{` typical of
	# closure args. Imperfect, but flags the obvious cases.
	closure_count=$(grep -cE '\|[a-zA-Z_][a-zA-Z0-9_, ]*\|[[:space:]]*(\{|->)' "$file" 2>/dev/null || true)
	[[ -z "$closure_count" ]] && closure_count=0
	total_closure=$((total_closure + closure_count))
	count_pattern "$file" "closure" '\|[a-zA-Z_][a-zA-Z0-9_, ]*\|[[:space:]]*(\{|->)'

	# Generic function/type parameters: `<T>` or `<T, U: Bound>`.
	generic_count=$(grep -cE 'fn[[:space:]]+[a-zA-Z_][a-zA-Z0-9_]*<[A-Z]' "$file" 2>/dev/null || true)
	[[ -z "$generic_count" ]] && generic_count=0
	total_generic=$((total_generic + generic_count))
	count_pattern "$file" "generic-fn" 'fn[[:space:]]+[a-zA-Z_][a-zA-Z0-9_]*<[A-Z]'

	# `?` error propagation — `expr?` followed by space, newline, dot.
	propagate_count=$(grep -cE '\?[[:space:].]' "$file" 2>/dev/null || true)
	[[ -z "$propagate_count" ]] && propagate_count=0
	total_propagate=$((total_propagate + propagate_count))
	count_pattern "$file" "?-propagate" '\?[[:space:].]'
done < <(find "$dir" -maxdepth 1 -name '*.osty' -print0)

printf '\n=== summary (production .osty under %s) ===\n' "$dir"
printf 'files            %s\n' "$total_files"
printf 'fn declarations  %s\n' "$total_funcs"
printf 'match exprs      %s\n' "$total_match"
printf 'closures         %s\n' "$total_closure"
printf 'generic fns      %s\n' "$total_generic"
printf '? propagations   %s\n' "$total_propagate"
printf 'total non-stage0 %s\n' "$((total_match + total_closure + total_generic + total_propagate))"
