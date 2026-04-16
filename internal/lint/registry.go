package lint

import (
	"sort"

	"github.com/osty/osty/internal/diag"
)

// Category groups related rules for config / documentation / UI.
type Category string

const (
	CategoryUnused     Category = "unused"
	CategoryShadowing  Category = "shadowing"
	CategoryDeadCode   Category = "dead_code"
	CategoryNaming     Category = "naming"
	CategorySimplify   Category = "simplify"
	CategoryComplexity Category = "complexity"
	CategoryDocs       Category = "docs"
)

// Rule is the queryable metadata for one lint check. Every L-prefixed
// diagnostic code has exactly one Rule. The registry is the source of
// truth for:
//
//   - `osty lint --explain CODE` (print the rule's description)
//   - `osty lint --list` (enumerate rules)
//   - Config alias names (allow / deny)
//   - LSP capabilities (list available code actions)
type Rule struct {
	Code            string
	Name            string // lowercase alias used in config / allow lists
	Category        Category
	DefaultSeverity diag.Severity // most rules warn; some may promote
	Summary         string        // one-line description
	Description     string        // paragraph(s) — examples included
	// Fixable is true when the rule attaches a machine-applicable
	// suggestion that `osty lint --fix` will auto-apply. UI surfaces
	// (LSP code actions, `osty lint --list`) use this to badge rules
	// as auto-fixable without having to run the analysis first.
	Fixable bool
}

// allRules is the authoritative rule list. Kept in one place so adding
// a new rule is a single-file change. Keep Summary terse (one line) and
// Description self-contained (readable in isolation via `--explain`).
var allRules = []Rule{
	// ---- Unused (L0001-L0009) ----
	{
		Code: diag.CodeUnusedLet, Name: "unused_let",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "`let` binding is never read",
		Description: "A binding introduced by `let` is never referenced. Remove the binding, or rename it with a leading underscore (`_foo`) to mark intentional discarding.",
		Fixable:     true,
	},
	{
		Code: diag.CodeUnusedParam, Name: "unused_param",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "function parameter is never used",
		Description: "A parameter is declared but never read inside the body. Public functions are exempt since their signature is external contract.",
		Fixable:     true,
	},
	{
		Code: diag.CodeUnusedImport, Name: "unused_import",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "`use` alias is never referenced",
		Description: "An imported name is never used. Package mode unions usage across every file, so cross-file references count.",
		Fixable:     true,
	},
	{
		Code: diag.CodeUnusedMut, Name: "unused_mut",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "`let mut` binding is never reassigned",
		Description: "The binding is declared `mut` but is never the target of an assignment, compound assign, index-write, field-write, or method call through it. Drop the `mut` qualifier.",
		Fixable:     true,
	},
	{
		Code: diag.CodeUnusedField, Name: "unused_field",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "struct field is never read",
		Description: "A private struct field never appears in a field read, struct literal, or struct pattern. Public structs are exempt.",
	},
	{
		Code: diag.CodeUnusedMethod, Name: "unused_method",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "private method is never called",
		Description: "A method on a non-public type is never called. Name-based heuristic — methods sharing names across types count as used if any of them are called.",
	},
	{
		Code: diag.CodeIgnoredResult, Name: "ignored_result",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "Result/Option value discarded at statement level",
		Description: "A statement-level expression returns a fallible value (Result<T, E>, Option<T>, or T?) that is silently dropped. Handle it with `?`, `match`, or bind to `_` explicitly.",
	},
	{
		Code: diag.CodeDeadStore, Name: "dead_store",
		Category: CategoryUnused, DefaultSeverity: diag.Warning,
		Summary:     "mutable binding is overwritten before being read",
		Description: "A `let mut x = E1` is followed by `x = E2` without any read of x in between. The first value is dead — the work to compute E1 is wasted.",
	},

	// ---- Shadowing (L0010-L0019) ----
	{
		Code: diag.CodeShadowedBinding, Name: "shadowed_binding",
		Category: CategoryShadowing, DefaultSeverity: diag.Warning,
		Summary:     "inner binding hides an outer name",
		Description: "A `let`, closure param, for-loop pattern, or match-arm pattern reuses a name already bound in an enclosing scope. Rename, or prefix with `_` to mark intentional.",
	},

	// ---- Dead code (L0020-L0029) ----
	{
		Code: diag.CodeDeadCode, Name: "dead_code",
		Category: CategoryDeadCode, DefaultSeverity: diag.Warning,
		Summary:     "statement is unreachable",
		Description: "A statement follows a diverging statement (return, break, continue, panic-like call, or if/else where both branches diverge). Control never reaches it.",
	},
	{
		Code: diag.CodeRedundantElse, Name: "redundant_else",
		Category: CategoryDeadCode, DefaultSeverity: diag.Warning,
		Summary:     "`else` after unconditional `return` is redundant",
		Description: "When the `if` branch always returns, the `else` body can be hoisted one level up for clearer flow.",
	},
	{
		Code: diag.CodeConstantCondition, Name: "constant_condition",
		Category: CategoryDeadCode, DefaultSeverity: diag.Warning,
		Summary:     "`if` condition is always true/false",
		Description: "The condition is a compile-time constant (`true`, `false`, `!true`, `!false`). Delete the `if`, or use the real condition.",
	},
	{
		Code: diag.CodeEmptyBranch, Name: "empty_branch",
		Category: CategoryDeadCode, DefaultSeverity: diag.Warning,
		Summary:     "`if`/`else` body is empty",
		Description: "An empty block `{}` in an `if`/`else` is almost always a placeholder. Fill it in, or restructure the control flow.",
	},
	{
		Code: diag.CodeNeedlessReturn, Name: "needless_return",
		Category: CategoryDeadCode, DefaultSeverity: diag.Warning,
		Summary:     "`return x` at tail is redundant",
		Description: "Osty blocks return their tail expression implicitly (§6). Drop the `return` keyword when the `return` is the last statement.",
		Fixable:     true,
	},
	{
		Code: diag.CodeIdenticalBranches, Name: "identical_branches",
		Category: CategoryDeadCode, DefaultSeverity: diag.Warning,
		Summary:     "both `if`/`else` branches are identical",
		Description: "Both branches evaluate to the same expression, so the condition has no effect. Replace the `if` with the expression directly.",
	},
	{
		Code: diag.CodeEmptyLoopBody, Name: "empty_loop_body",
		Category: CategoryDeadCode, DefaultSeverity: diag.Warning,
		Summary:     "loop body is empty",
		Description: "A `for` loop with an empty body usually indicates a forgotten body or a missed idiom. Fill in the body or drop the loop.",
	},

	// ---- Naming (L0030-L0039) ----
	{
		Code: diag.CodeNamingType, Name: "naming_type",
		Category: CategoryNaming, DefaultSeverity: diag.Warning,
		Summary:     "type name is not UpperCamelCase",
		Description: "Types (struct, enum, interface, type alias, generic parameter) should be UpperCamelCase per the v0.4 spec examples.",
	},
	{
		Code: diag.CodeNamingValue, Name: "naming_value",
		Category: CategoryNaming, DefaultSeverity: diag.Warning,
		Summary:     "value name is not lowerCamelCase",
		Description: "Functions, methods, `let` bindings, and parameters should be lowerCamelCase per the v0.4 spec examples.",
	},
	{
		Code: diag.CodeNamingVariant, Name: "naming_variant",
		Category: CategoryNaming, DefaultSeverity: diag.Warning,
		Summary:     "enum variant is not UpperCamelCase",
		Description: "Enum variants should be UpperCamelCase.",
	},

	// ---- Simplify (L0040-L0049) ----
	{
		Code: diag.CodeRedundantBool, Name: "redundant_bool",
		Category: CategorySimplify, DefaultSeverity: diag.Warning,
		Summary:     "`if c { true } else { false }` collapses to `c`",
		Description: "Returning the condition directly (or `!cond`) is clearer.",
		Fixable:     true,
	},
	{
		Code: diag.CodeSelfCompare, Name: "self_compare",
		Category: CategorySimplify, DefaultSeverity: diag.Warning,
		Summary:     "operand compared with itself",
		Description: "`x == x`, `x != x`, `x < x`, … always evaluates to a constant. Likely a copy-paste bug. Function calls on both sides are NOT flagged.",
	},
	{
		Code: diag.CodeSelfAssign, Name: "self_assign",
		Category: CategorySimplify, DefaultSeverity: diag.Warning,
		Summary:     "self-assignment is a no-op",
		Description: "`x = x` does nothing. Likely a copy-paste bug — check the intended RHS.",
		Fixable:     true,
	},
	{
		Code: diag.CodeDoubleNegation, Name: "double_negation",
		Category: CategorySimplify, DefaultSeverity: diag.Warning,
		Summary:     "`!!x` is a no-op on Bool",
		Description: "Drop both `!` operators.",
		Fixable:     true,
	},
	{
		Code: diag.CodeBoolLiteralCompare, Name: "bool_literal_compare",
		Category: CategorySimplify, DefaultSeverity: diag.Warning,
		Summary:     "comparison against a bool literal is redundant",
		Description: "`x == true` is just `x`; `x == false` is `!x`. Let the Bool speak for itself.",
		Fixable:     true,
	},
	{
		Code: diag.CodeNegatedBoolLiteral, Name: "negated_bool_literal",
		Category: CategorySimplify, DefaultSeverity: diag.Warning,
		Summary:     "negated bool literal `!true` / `!false`",
		Description: "Use the opposite literal directly.",
		Fixable:     true,
	},

	// ---- Complexity (L0050-L0069) ----
	{
		Code: diag.CodeTooManyParams, Name: "too_many_params",
		Category: CategoryComplexity, DefaultSeverity: diag.Warning,
		Summary:     "function takes too many parameters",
		Description: "Long parameter lists are hard to call correctly. Group related parameters into a struct, or split the function.",
	},
	{
		Code: diag.CodeFunctionTooLong, Name: "function_too_long",
		Category: CategoryComplexity, DefaultSeverity: diag.Warning,
		Summary:     "function body is too long",
		Description: "Long functions are hard to review and test. Extract cohesive chunks into helper functions.",
	},
	{
		Code: diag.CodeDeepNesting, Name: "deep_nesting",
		Category: CategoryComplexity, DefaultSeverity: diag.Warning,
		Summary:     "control-flow is nested too deeply",
		Description: "Deeply nested code is hard to follow. Use early returns (guard clauses) or extract inner branches into helpers.",
	},

	// ---- Docs (L0070-L0079) ----
	{
		Code: diag.CodeMissingDoc, Name: "missing_doc",
		Category: CategoryDocs, DefaultSeverity: diag.Warning,
		Summary:     "public declaration has no doc comment",
		Description: "Public items are the module's external contract — even a one-line `///` helps consumers use them correctly.",
	},
}

// Rules returns a copy of the full rule list, sorted by code.
func Rules() []Rule {
	out := append([]Rule(nil), allRules...)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// LookupRule finds a rule by its code (L0001) or by its alias
// (unused_let). Returns ok=false if neither matches.
func LookupRule(name string) (Rule, bool) {
	for _, r := range allRules {
		if r.Code == name || r.Name == name {
			return r, true
		}
	}
	return Rule{}, false
}

// RulesByCategory returns the rules belonging to a category, sorted
// by code. Returns an empty slice for unknown categories.
func RulesByCategory(c Category) []Rule {
	var out []Rule
	for _, r := range allRules {
		if r.Category == c {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Categories returns every distinct category, alphabetically.
func Categories() []Category {
	seen := map[Category]bool{}
	var out []Category
	for _, r := range allRules {
		if !seen[r.Category] {
			seen[r.Category] = true
			out = append(out, r.Category)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
