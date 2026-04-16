// Package mir defines Osty's backend-facing middle intermediate
// representation.
//
// MIR sits below the language-level IR in internal/ir. The pipeline is
//
//	source → lexer → parser → resolve → check → ir.Lower
//	       → ir.Monomorphize → ir.Validate → mir.Lower → mir.Validate → backend
//
// Where HIR (internal/ir) is a normalised tree of source constructs —
// matches, patterns, `if let`, `?`, method syntax, keyword args — MIR
// is a Rust-MIR-style shape of explicit locals, basic blocks, places,
// projections, and terminators. Source sugar is already gone by the
// time MIR is produced:
//
//   - Patterns are lowered to projections + assigns.
//   - `match` / `if let` / `for let` become CFGs with `SwitchInt` and
//     `Branch` terminators.
//   - `?` and optional chaining `?.` become explicit discriminant reads
//     and early-return branches.
//   - Method calls become direct calls with an explicit receiver operand.
//   - Enum constructor sugar becomes `AggregateRV{Kind: EnumVariant}`.
//
// The MIR package has no dependencies on `internal/ast`, `internal/resolve`,
// or `internal/check`; it depends only on `internal/ir` for the type
// surface (reused so backends need only one type vocabulary).
//
// See docs/mir_design.md for the full design rationale, the invariants
// the validator enforces, and the multi-stage migration plan from the
// current HIR→AST bridge in `internal/llvmgen` to direct MIR consumption.
package mir
