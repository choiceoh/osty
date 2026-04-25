# dep-resolver — runtime program benchmark (4 of 5)

One of five end-to-end CLI programs in `benchmarks/programs/`. See
the [suite README](../README.md) for the overall shape; this file
documents the workload only.

Generates 10,000 synthetic build modules with 0-3 deterministic
deps each (always to lower-index modules — guaranteed DAG), runs a
forward longest-path depth pass, then computes a prefix histogram
over the module names. Stresses List<List<Int>> traversal +
Map<String,Int> insert + per-line `split("-")`.
