# markdown-stats — runtime program benchmark (2 of 5)

One of five end-to-end CLI programs in `benchmarks/programs/`. See
the [suite README](../README.md) for the overall shape; this file
documents the workload only.

Classifies 20,000 synthetic markdown-ish lines (heading, subheading,
bullet, blockquote, plain, empty) and emits per-type counts plus
character totals. Stresses Map<String,Int> insert/get + line scan.

Designed to be Osty-buildable — uses `Map<String,Int>` so the build
dispatches through the MIR LLVM lowering path (the alternate path
mishandles String + chains in `startsWith`-driven hot loops).
