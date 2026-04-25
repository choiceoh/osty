#!/usr/bin/env bash
# Refresh tmp/airepair-cases/ + rewrite todo-airepair.md.
# Used by `just airepair-capture` and the CI workflow so both
# emit the same backlog format.
#
# Args: $1 = osty binary (default .bin/osty)
#       $2 = capture dir (default tmp/airepair-cases)
#       $3 = output markdown (default todo-airepair.md)

set -u

osty="${1:-.bin/osty}"
captures="${2:-tmp/airepair-cases}"
out="${3:-todo-airepair.md}"

mkdir -p "$captures"

scanned=0
while IFS= read -r file; do
	case "$file" in
	testdata/spec/negative/* | internal/airepair/testdata/corpus/*.input.osty)
		continue
		;;
	esac
	"$osty" airepair --capture-dir "$captures" --capture-if residual "$file" >/dev/null 2>&1 || true
	scanned=$((scanned + 1))
done < <(git ls-files '*.osty')

captured=$(find "$captures" -maxdepth 1 -name '*.input.osty' 2>/dev/null | wc -l | tr -d ' ')
promoted=$(find internal/airepair/testdata/corpus -maxdepth 1 -name '*.input.osty' 2>/dev/null | wc -l | tr -d ' ')

# Split signal from noise:
#   changed=true  → airepair actually rewrote something = real AI-slip backlog
#   changed=false → airepair left it untouched, residuals are toolchain
#                   self-host / LLVM backend gaps (not learning fodder)
#
# Mirror the AI-slip cases into a sibling dir so `osty airepair learn`
# only ranks signal, not noise.
ai_dir="${captures}-ai-slips"
rm -rf "$ai_dir"
mkdir -p "$ai_dir"
ai_slips=0
domain_errs=0
if [ "$captured" -gt 0 ]; then
	for report in "$captures"/*.report.json; do
		[ -f "$report" ] || continue
		base="$(basename "$report" .report.json)"
		if grep -q '"changed": true' "$report"; then
			ai_slips=$((ai_slips + 1))
			cp "$captures/$base.report.json" "$ai_dir/" 2>/dev/null || true
			[ -f "$captures/$base.input.osty" ] && cp "$captures/$base.input.osty" "$ai_dir/"
			[ -f "$captures/$base.expected.osty" ] && cp "$captures/$base.expected.osty" "$ai_dir/"
		else
			domain_errs=$((domain_errs + 1))
		fi
	done
fi

{
	echo "# AI Repair Backlog"
	echo
	echo "_Auto-generated. Refresh with \`just airepair-capture\` (or rerun in CI)._"
	echo
	echo "**Scanned:** ${scanned} \`.osty\` file(s)  "
	echo "**Captured:** ${captured} residual case(s) — **${ai_slips}** AI-slip(s) airepair rewrote, **${domain_errs}** untouched (toolchain self-host / backend gap, not airepair's job)  "
	echo "**Corpus coverage:** ${promoted} promoted case(s)"
	echo
	echo "## AI-slip backlog (changed=true)"
	echo
	if [ "$ai_slips" -gt 0 ]; then
		echo '```'
		"$osty" airepair learn --top 10 --corpus internal/airepair/testdata/corpus "$ai_dir" 2>/dev/null |
			sed -n '/^learning priorities:/,$p'
		echo '```'
	else
		echo "_No new AI slips this run — airepair didn't need to rewrite anything._"
		echo
		echo "If \`Captured\` is non-zero above, it's domain code that fails the checker for unrelated reasons (self-host / backend coverage)."
	fi
	echo
	echo "## Workflow"
	echo
	echo "1. \`just airepair-capture\` refreshes \`${captures}/\` and rewrites this file."
	echo "2. For an AI-slip group above: \`${osty} airepair triage ${captures}/\` for detail, then \`${osty} airepair promote ${captures}/<case>\` to add to \`internal/airepair/testdata/corpus/\`."
	echo "3. The untouched-residual count is a separate signal — it tracks how many \`.osty\` files in the repo currently fail the checker for non-AI-slip reasons."
} >"$out"

echo "airepair: captured ${captured} (AI-slips: ${ai_slips}, domain: ${domain_errs}) over ${scanned} files → ${out}"
