# AI Repair Backlog

_Auto-generated. Refresh with `just airepair-capture` (or rerun in CI)._

**Scanned:** 320 `.osty` file(s)  
**Captured:** 149 residual case(s) — **0** AI-slip(s) airepair rewrote, **149** untouched (toolchain self-host / backend gap, not airepair's job)  
**Corpus coverage:** 16 promoted case(s)

## AI-slip backlog (changed=true)

_No new AI slips this run — airepair didn't need to rewrite anything._

If `Captured` is non-zero above, it's domain code that fails the checker for unrelated reasons (self-host / backend coverage).

## Workflow

1. `just airepair-capture` refreshes `tmp/airepair-cases/` and rewrites this file.
2. For an AI-slip group above: `.bin/osty airepair triage tmp/airepair-cases/` for detail, then `.bin/osty airepair promote tmp/airepair-cases/<case>` to add to `internal/airepair/testdata/corpus/`.
3. The untouched-residual count is a separate signal — it tracks how many `.osty` files in the repo currently fail the checker for non-AI-slip reasons.
