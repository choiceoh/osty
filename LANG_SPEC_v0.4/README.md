# Osty Language Specification v0.4

Osty is a general-purpose, statically-typed, garbage-collected programming language.
This directory holds the specification, split into per-section files for easier navigation and editing.

**Status.** v0.4 — supersedes v0.3. Closes the remaining v0.3 edge-case decision queue (G13-G18): non-escaping structured-concurrency capabilities, generic method turbofish/method-reference rules, erased function-value arity, closure parameter irrefutability, stable nested witness policy, and checked stdlib protocol stubs. No known open gaps remain at the time of this release. See [`18-change-history.md`](./18-change-history.md) for the full v0.1 → v0.2 → v0.3 → v0.4 evolution.

**Companion documents.**

- `../OSTY_GRAMMAR_v0.4.md` — formal grammar (R1–R26 + EBNF). The lexer/parser ground truth.
- `../SPEC_GAPS.md` — archive of resolved gaps per version. No open items as of v0.4.

## Table of Contents

- [1. Lexical Structure](./01-lexical-structure.md)
- [2. Type System](./02-type-system.md)
- [3. Declarations](./03-declarations.md)
- [4. Expressions](./04-expressions.md)
- [5. Modules and Packages](./05-modules-and-packages.md)
- [6. Scripts](./06-scripts.md)
- [7. The Error Interface](./07-the-error-interface.md)
- [8. Concurrency](./08-concurrency.md)
- [9. Memory Management](./09-memory-management.md)
- **10. Standard Library** — see [`10-standard-library/`](./10-standard-library/README.md)
- [11. Testing](./11-testing.md)
- [12. Foreign Function Interface](./12-foreign-function-interface.md)
- [13. Tooling](./13-tooling.md)
- [14. Excluded Features](./14-excluded-features.md)
- [15. Iteration Protocol](./15-iteration-protocol.md)
- [16. I/O Protocol](./16-io-protocol.md)
- [17. Display and Format Protocol](./17-display-and-format-protocol.md)
- [18. Change History](./18-change-history.md)

## Reading order

The chapters are mostly self-contained, but new readers benefit from this order:

1. **Lexical & types** (§1, §2) — establishes vocabulary.
2. **Declarations & expressions** (§3, §4) — the everyday surface.
3. **Modules, scripts, errors** (§5–§7) — program structure.
4. **Concurrency & memory** (§8, §9) — the runtime model.
5. **Standard library** (§10) — read on demand by package.
6. **Testing, FFI, tooling** (§11–§13) — workflow concerns.
7. **Excluded features** (§14) — what is intentionally absent.
8. **Protocols** (§15–§17) — the interface contracts that bind §10 together.
9. **Change history** (§18) — upgrade notes for v0.1 and v0.2 users.
