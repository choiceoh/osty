# expr-calc — runtime program benchmark (5 of 5)

One of five end-to-end CLI programs in `benchmarks/programs/`. See
the [suite README](../README.md) for the overall shape; this file
documents the workload only.

5,000 fixed-shape arithmetic expressions (5 single-digit operands
joined by `+`/`-`/`*`), each tokenized via String.split, converted to
postfix via shunting-yard with a preallocated op stack, then
evaluated on a stack-machine. Stresses tokenize + parser dispatch +
stack-machine eval — the closest analog in the suite to what Osty's
own self-host compiler does.
