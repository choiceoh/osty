# AI Repair Backlog

_Auto-generated. Refresh with `just airepair-capture` (or rerun in CI)._

**Scanned:** 402 `.osty` file(s)  
**Captured:** 217 residual case(s) — **1** AI-slip(s) airepair rewrote, **216** untouched (toolchain self-host / backend gap, not airepair's job)  
**Corpus coverage:** 16 promoted case(s)

## AI-slip backlog (changed=true)

```
learning priorities:
  1. javascript_for_of_loop -> E0703  score=135 cases=1 residual_errors=1 stage=check action=promote_and_fix_check representative=webview2 corpus=uncovered
     next: promote webview2 into the corpus, then investigate the check-stage residual for javascript_for_of_loop -> E0703
```

## Workflow

1. `just airepair-capture` refreshes `tmp/airepair-cases/` and rewrites this file.
2. For an AI-slip group above: `.bin/osty airepair triage tmp/airepair-cases/` for detail, then `.bin/osty airepair promote tmp/airepair-cases/<case>` to add to `internal/airepair/testdata/corpus/`.
3. The untouched-residual count is a separate signal — it tracks how many `.osty` files in the repo currently fail the checker for non-AI-slip reasons.
