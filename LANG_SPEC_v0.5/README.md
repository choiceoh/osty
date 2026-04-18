# Osty Language Specification v0.5

Osty is a general-purpose, statically-typed, garbage-collected programming language.
This directory holds the specification, split into per-section files for easier navigation and editing.

**Status.** v0.5 — current spec baseline. Supersedes v0.4. Closes 15 gaps
(G20–G34) accumulated from the v0.4 usage corpus: value-returning
`loop`, labeled `break`/`continue`, range step `by`, struct update
shorthand, trailing closure, `as?` downcast, `const fn`, enum integer
discriminants, scoped imports, `pub use`, `#[cfg(...)]`, `#[op(...)]`
bounded operator overloading, lossless numeric widening, function
value parameter-name preservation, inline `#[test]` + doctest +
property-based testing. Two v0.4 exclusions ("implicit numeric
conversions", "operator overloading") are replaced with precisely
scoped, opt-in forms.

See [`18-change-history.md`](./18-change-history.md) for the full
v0.1 → v0.2 → v0.3 → v0.4 → v0.5 evolution.

**Companion documents.**

- [`ABRIDGED.md`](./ABRIDGED.md) — short, example-free quick spec for AI
  agents generating or editing Osty code.
- `../OSTY_GRAMMAR_v0.5.md` — formal grammar (R1–R26 + EBNF). The lexer/parser ground truth.
- `../OSTY_GRAMMAR_v0.4.md` — historical v0.4 grammar snapshot.
- `../SPEC_GAPS.md` — archive of resolved gaps per version. No open items as of v0.5.

## `?`-family cheat sheet

Four punctuation operators share the `?` glyph. They look related — three
of them are — but each has a distinct role. Read this table before §4.

| Form | Role | Operand → Result | Example | Spec |
|---|---|---|---|---|
| `expr?` | **Propagate** `Err`/`None` out of the enclosing function | `Result<T,E> → T` or `Option<T> → T` | `let cfg = parse(text)?` | §4.5 |
| `expr?.m` | **Chain** on `Option`; short-circuit to `None` on the first missing step | `Option<T> → Option<U>` | `user?.address?.city` | §4.6 |
| `lhs ?? rhs` | **Unwrap-or-default**; right side is lazy | `Option<T>, T → T` | `user?.name ?? "anon"` | §4.6 |
| `err as? T` | **Downcast** an `Error` to its concrete nominal type (shortcut for `err.downcast::<T>()`) | `Error → T?` | `err as? FsError` | §7.4 (G27) |

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
9. **Change history** (§18) — upgrade notes for v0.1 → v0.5 users.
