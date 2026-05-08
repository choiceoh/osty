# Osty Language Specification v0.6

Osty is a general-purpose, statically-typed, garbage-collected programming language.
This directory holds the specification, split into per-section files for easier navigation and editing.

**Status.** v0.6 — current spec baseline. Supersedes v0.5. Closes 10 gaps
(G36, G37, G39, G40, G41, G42, G44, G45, G47, G48) accumulated during
the v0.5 use corpus and the 100-PR self-host sprint: capability
parameters, information flow tracking, reproducibility, sealed
construction, error contract, structured intent, API evolution rules,
golden tests, machine-readable context export. Four originally-proposed
gaps (G38, G43, G46, G49) were withdrawn pre-release as low-utility —
see SPEC_GAPS.md "Withdrawn" for sub-rationale.

The v0.6 design north star is *Hidden dependency is forbidden* —
time, randomness, environment, security flow, evolution rules,
intent are all surfaced explicitly through the type system or the
annotation surface.

See [`18-change-history.md`](./18-change-history.md) for the full
v0.1 → v0.2 → v0.3 → v0.4 → v0.5 → v0.6 evolution and per-version
gap closure tables.

**Companion documents.**

- [`ABRIDGED.md`](./ABRIDGED.md) — short, example-free quick spec for AI
  agents generating or editing Osty code.
- [`00-revision.md`](./00-revision.md) — comprehensive decision log for
  the v0.5 → v0.6 batch. Cross-references all 14 decisions with diff
  algorithms, formal IFC inference rules, worked examples, migration
  notes, and the v0.6 cut readiness checklist. Each chapter file in
  this directory is the *normative* source; `00-revision.md` is the
  *one-document overview*.
- `../OSTY_GRAMMAR_v0.6.md` — formal grammar (R1–R29 + EBNF). The
  lexer/parser ground truth.
- `../OSTY_GRAMMAR_v0.5.md` and earlier — historical grammar snapshots.
- `../SPEC_GAPS.md` — archive of resolved gaps per version.
- `../CHANGELOG_v0.6.md` — implementation status (what is *shipped*
  vs what is *spec*).

## `?`-family cheat sheet

Four punctuation operators share the `?` glyph. They look related — three
of them are — but each has a distinct role. Read this table before §4.

| Form | Role | Operand → Result | Example | Spec |
|---|---|---|---|---|
| `expr?` | **Propagate** `Err`/`None` out of the enclosing function | `Result<T,E> → T` or `Option<T> → T` | `let cfg = parse(text)?` | §4.5 |
| `expr?.m` | **Chain** on `Option`; short-circuit to `None` on the first missing step | `Option<T> → Option<U>` | `user?.address?.city` | §4.6 |
| `lhs ?? rhs` | **Unwrap-or-default**; right side is lazy | `Option<T>, T → T` | `user?.name ?? "anon"` | §4.6 |
| `err as? T` | **Downcast** an `Error` to its concrete nominal type (shortcut for `err.downcast::<T>()`) | `Error → T?` | `err as? FsError` | §7.4 |

**Disambiguations (read once, save hours).**

- `?` is legal on both `Result` and `Option`, but the enclosing function
  must return the matching shape; mixing is a compile error — convert
  with `Option.orError(msg)` or `Result.ok()`.
- `?.` stays inside `Option`. It does **not** return from the function.
  To exit a chain, end with `?` (`user?.address?.city?` in a fn
  returning `Option<_>`).
- `??` is `Option`-only. For `Result`, use `.unwrapOr(d)` / `match`.
- `as?` is **not** a general type test. It works only on `Error`
  values, using the nominal tag attached at up-cast time (§7.4). For
  other "is it this variant?" questions, use pattern matching.
- None of the four introduces null/nil. `?` in a type (`T?`) is sugar
  for `Option<T>` (§2.5) — a separate surface.

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
- [19. Runtime Primitives](./19-runtime-primitives.md) — toolchain-only sublanguage; not part of the user prelude
- [**20. Capabilities**](./20-capabilities.md) — environment effects as values (v0.6 G36)
- [**21. Information Flow Tracking**](./21-information-flow.md) — static taint / sanitize / sink (v0.6 G37)

## Reading order

The chapters are mostly self-contained, but new readers benefit from this order:

1. **Lexical & types** (§1, §2) — establishes vocabulary.
2. **Declarations & expressions** (§3, §4) — the everyday surface.
3. **Modules, scripts, errors** (§5–§7) — program structure.
4. **Concurrency, memory, capabilities** (§8, §9, §20) — the runtime model.
5. **Standard library** (§10) — read on demand by package.
6. **Testing, FFI, tooling** (§11–§13) — workflow concerns.
7. **Excluded features** (§14) — what is intentionally absent.
8. **Protocols** (§15–§17) — the interface contracts that bind §10 together.
9. **Information flow** (§21) — security-flow tracking, capabilities (§20) prerequisite.
10. **Change history** (§18) — upgrade notes for v0.1 → v0.6 users.

## v0.6 design north star

> **Hidden dependency is forbidden.** Time, randomness, environment,
> filesystem, network, security flow, API evolution rules, performance
> contracts, intent, and specification — all surfaced through type
> signatures or annotations, never implicit.

Four annotation families implement this principle:

| Family | Surface | Chapters |
|---|---|---|
| **Effectful** (env / IO) | Capability parameters, `#[ambient]`, `#[reproducible]`, `#[pure]` | §20, §3.11 |
| **Security** (sources → sinks) | `#[taint]`, `#[sanitizes]`, `#[requires]` | §21 |
| **Temporal** (versioning) | `#[since]`, `#[stability]`, `#[match_compat]`, `osty publish` | §3.14 |
| **Intent + Determinism** | `#[purpose]`, `#[example]`, `#[fixture]`, `#[error_contract]`, `#[sealed_construct]`, `#[golden]`, `osty context` | §3.12, §3.4.5, §7.5, §11.5, §13.4 |
