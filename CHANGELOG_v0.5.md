# CHANGELOG — v0.5

Tracks the user-visible slice of the v0.5 spec (`LANG_SPEC_v0.5/`) that
has landed in the compiler today, separate from the spec itself. The
spec is the authority on what v0.5 *will* be; this file is the
authority on what v0.5 *is* right now.

See [`LANG_SPEC_v0.5/18-change-history.md`](./LANG_SPEC_v0.5/18-change-history.md)
for the full v0.4 → v0.5 decision log (15 resolved gaps, G20 – G35) and
[`SPEC_GAPS.md`](./SPEC_GAPS.md) for per-gap rationale.

## Shipped in the compiler

### Syntax

| Form | Status | Notes |
|---|---|---|
| `use path::{a, b as c}` — scoped / grouped imports | **shipped** (G28) | Parsed natively by the self-hosted parser ([`toolchain/parser.osty`](./toolchain/parser.osty)) and lowered through [`internal/selfhost/ast_lower.osty`](./internal/selfhost/ast_lower.osty). One flat `use` per item, rename preserved. The earlier Go-side pre-parse rewrite (`internal/parser/scoped_imports.go`) was retired in af371d1 once the self-hosted parser caught up. |
| `pub use path.Sym` — cross-module re-export | **shipped** (G30) | Parsed natively in [`toolchain/parser.osty`](./toolchain/parser.osty) with the `pub` visibility carried through `ast_lower.osty`; resolver honors the flag and cycles diagnose as `E0552`. The earlier Go-side post-parse `IsPub` flip (`internal/parser/pub_use.go`) was retired in 15cd35a. |
| `#[cfg(key = "value")]` — conditional compilation | **shipped** (G29) | Pre-resolve filter in [`internal/resolve/cfg.go`](./internal/resolve/cfg.go). Keys: `os`, `target`, `arch`, `feature`. Unknown key → `E0405`. Composition forms (`all` / `any` / `not`) are spec-visible but not yet parsed. |
| `#[test]` inline test annotation | **shipped** (G32) | Discovery in [`cmd/osty/test_native.go`](./cmd/osty/test_native.go) accepts any zero-arity function carrying `#[test]`, including those outside `_test.osty`. Legacy `test*` prefix still works. |
| Doctest blocks in `///` comments | **shipped** (G32) | Extraction in [`internal/doctest`](./internal/doctest). `osty test --doc` synthesises a runner per package and routes blocks through the normal test pipeline. |
| `err.downcast::<T>()` — nominal-tag recovery (backend only) | **half-shipped** (G27) | LLVM lowering in [`internal/llvmgen/iface_downcast.go`](./internal/llvmgen/iface_downcast.go) compares the receiver's runtime vtable against `@osty.vtable.<T>__<iface>` and selects between the data ptr and null (ptr-typed `T?`). Ships ahead of the checker; the self-hosted checker still rejects the call, so the path is driven by a `generateFromAST` unit test until the front end catches up. |

### Stdlib — signatures

These modules publish v0.5 signatures so user code that imports and
references them type-checks under the front-end checker. Runtime
behavior for the new surface is still the G18 stub convention —
bodies exist so the stub checker accepts imports.

- **`std.option`** — combinators: `isSome / isNone / isSomeAnd / isNoneOr / contains / take / replace / unwrap / expect / unwrapOr / unwrapOrElse / and / andThen / or / orElse / xor / filter / inspect / map / mapOr / mapOrElse / zip / okOr / okOrElse`.
- **`std.result`** — combinators: `isOk / isErr / isOkAnd / isErrAnd / contains / containsErr / unwrap / expect / unwrapErr / expectErr / unwrapOr / unwrapOrElse / ok / err / and / andThen / or / orElse / inspect / inspectErr / map / mapErr / mapOr / mapOrElse`.
- **`std.collections`** — `List.{chunked, windowed, partition, reduce, scan, flatMap, zip3}` in addition to the existing v0.4 surface. `Map.{getOrInsert, getOrInsertWith, merge, mapValues, filter}`.
- **`std.strings`** — full Unicode-aware chapter. Everything referenced by name in `LANG_SPEC_v0.5/10-standard-library` is now a real `.osty` signature.
- **`std.error`** — `Error.wrap(context)` / `Error.chain()` default methods, `WrappedError` struct, free functions `wrap()` / `rootCause()`.
- **`std.testing`** — `Gen<T>` type + `property / propertyN / propertySeeded` runners.
- **`std.testing.gen`** (new submodule) — 17 generator constructors: primitives (`int`, `intRange`, `bool`, `float`, `char`, `byte`, `asciiString`) and combinators (`oneOf`, `oneOfGens`, `map`, `filter`, `pair`, `triple`, `list`, `listOfSize`, `option`, `result`, `constant`).

### Tooling

- **`osty test --doc`** — extract and run doctest blocks as additional
  test cases. One synthesised `__osty_doctest_runner__.osty` per
  package; `fn test_doc_<owner>_<n>()` per block. Failures report
  which owner + ordinal broke.
- **Inline `#[test]`** discovery rejects `#[test]` on parameterised
  functions (silently skipped) and on a function literally named
  `testing`.

### Diagnostic codes

| Code | Meaning | Where |
|---|---|---|
| `E0405` | Unknown `#[cfg]` key | `internal/resolve/cfg.go` |
| `E0552` | `pub use` cycle | resolver re-export walk |
| `E0553` | `pub use` of a private symbol | resolver |
| `E0554` | Duplicate item in scoped `use path::{...}` | resolver |
| `E0754`–`E0756` | `#[op(...)]` signature / duplicate / not-allowed — reserved for G35 | `internal/diag/codes.go` |
| `E0757` | `as?` on a non-`Error` expression — reserved for G27 | `internal/diag/codes.go` |
| `E0758` / `E0759` | Enum integer discriminant on payload variant / duplicate discriminant — reserved for G31 | `internal/diag/codes.go` |
| `E0763` | Unknown loop label in `break 'lbl` | reserved for G24 |
| `E0764` | Label shadowing | reserved for G24 |
| `E0765` | Implicit narrowing conversion (numeric) | reserved for G34 |

All codes appear in [`ERROR_CODES.md`](./ERROR_CODES.md) (generated) and
have focused tests under `internal/diag/testdata`.

## Not yet shipped

The following v0.5 forms are spec-frozen but still need edits to
`toolchain/parser.osty` / `toolchain/elab.osty`, which route through
the bootstrap-gen regen pipeline
(see [issue #362](https://github.com/choiceoh/osty/issues/362) —
`go generate ./internal/selfhost`). The upstream fallback landed in
f38be21 and scoped / `pub use` parsers have since migrated to the
self-hosted surface (af371d1, 15cd35a), so the blocker is now
narrower than the original #362 framing, but editing the checker
half of these forms still round-trips through the regen path.

- `loop { break value }` — value-returning unbounded loop (G22)
- `'label: for / loop …` + labeled `break 'label` / `continue 'label` (G24)
- Range step `0..100 by 2` (G25)
- Struct update shorthand `receiver { field: value }` (G26)
- Trailing closure `f(x) |y| { body }` (G23)
- `err as? T` downcast syntax (G27) — LLVM lowering shipped (see table above); checker recognition pending so full `osty build` still rejects
- `pub? const fn` — compile-time evaluable functions (G21 extension)
- Enum integer discriminants `pub enum X: Int { A = 1, ... }` (G31)
- `#[op(+)]` operator overload (G35) — six-operator allow-list
- Lossless numeric widening (G34)
- Function-value parameter-name preservation (G20)
- `#[cfg]` composition forms `all(...)` / `any(...)` / `not(...)` (G29 partial)

## Native checker status

The stub stdlib and the v0.5 syntax rewrites above run end-to-end
through parse + resolve. The native checker (`OSTY_NATIVE_CHECKER_BIN`)
does **not** yet know about the newly published method signatures on
`List` / `Map` / `Option` / `Result` / `Error`, so front-end checking
in isolation may flag `E0703` (no method) on the new surface even
though the stdlib signatures are present. This is a checker-side gap
tracked alongside the bootstrap regen work.

## Migration from v0.4

v0.5 is additive by design — every v0.4 program compiles unchanged
under v0.5 grammar. Code that wants to opt in today can do so for
the **shipped** list above; anything under "Not yet shipped" is a
spec-level commitment, not a usable surface, until regen lands.

The project edition remains `edition = "0.4"` in `osty.toml` — there
is no `edition = "0.5"` yet because several of the shipped forms
(scoped imports, `pub use`, `#[cfg]`, `#[test]`) are grammar-additive
and readable by 0.4 tooling.

## Implementation history

Entries touching the shipped rows above, newest first. Hashes are
short; `git log <hash>` for the full message.

- **014a6fe** — `err.downcast::<T>()` LLVM lowering (backend half of G27 / §7.4)
- **607eb19** — `use path.X as Y` stable-alias rewrite migrates from Go side to the self-hosted parser
- **af371d1** — `use path::{ ... }` scoped-use handling migrates to the self-hosted parser; `internal/parser/scoped_imports.go` retired
- **15cd35a** — `pub use` handling migrates to the self-hosted parser; `internal/parser/pub_use.go` retired
- **f38be21** — Bootstrap-gen regen pipeline grows a build gate + checker-miss fallback (narrowed #362)
- **6fd09bb / 23b3f26 / f69461d** — AST-native package checker bridge + prelude-function registration + package-native external checker requests (all three prerequisites for shipping checker halves of the "Not yet shipped" items)
