# osty-vs-go mutators

Example scripts for `osty-vs-go --autoresearch --mutator '<cmd>'`. A
mutator is any executable that:

1. Modifies files in the working tree.
2. Exits 0 on success (the iteration proceeds), nonzero on failure (the
   iteration is skipped and any partial writes are reverted).

The orchestrator sees only exit code + working-tree diff. Mutators are
otherwise free — shell scripts, Python, Rust binaries, LLM agents
shelling out to an API, whatever you want.

Running the examples from the repo root:

```sh
just build
go run ./cmd/osty-vs-go \
  --autoresearch \
  --mutator benchmarks/osty-vs-go/mutators/noop.sh \
  --max-experiments 3 \
  --benchtime 100ms
```

## Bundled examples

- **`noop.sh`** — adds a comment timestamp to a throwaway file. Useful
  for smoke-testing the orchestrator without caring whether scores
  change; every iteration will revert on regression because the metric
  doesn't actually move.
