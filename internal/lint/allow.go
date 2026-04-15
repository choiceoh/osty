package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

// #[allow(...)] suppression.
//
// A user can opt out of specific lint warnings by tagging a declaration
// with `#[allow(NAME, NAME, ...)]`. Accepted NAMEs:
//
//   - A concrete code: `L0001`, `L0040`, ...
//   - A rule alias (the `Name` field of any entry in registry.go's
//     `allRules` — e.g. `unused_let`, `redundant_bool`, `self_assign`,
//     `too_many_params`, `missing_doc`, ...).
//   - A category alias: `unused`, `shadowing` (also `shadow`),
//     `dead_code` (also `unreachable`), `naming`, `simplify` (also
//     `suspicious`), `complexity`, `docs`. A category alias expands to
//     every rule in that category.
//   - The wildcards `lint` or `all`.
//
// The registry (registry.go) is the single source of truth — new rules
// added there are automatically suppressible by code, name, and
// category without touching this file.
//
// Annotations cover the annotated declaration PLUS every AST node
// nested within it (methods, bodies, inner lets). Each annotation is
// applied to the span `[decl.Pos(), decl.End()]`.

// allowRegion is a single suppression window — a byte / line range plus
// the set of codes it suppresses (nil means "all lint codes").
type allowRegion struct {
	startLine, startCol int
	endLine, endCol     int
	codes               map[string]bool // nil means "any lint code"
}

// filterSuppressed walks the file once to collect every #[allow(...)]
// scope, then drops any lint diagnostic whose primary position falls
// inside a matching region. Other diagnostics (parse errors, resolver
// errors) are untouched — suppression is lint-only.
func (l *linter) filterSuppressed() {
	if len(l.result.Diags) == 0 {
		return
	}
	var regions []allowRegion
	for _, d := range l.file.Decls {
		regions = collectAllowRegions(d, regions)
	}
	if len(regions) == 0 {
		return
	}
	kept := l.result.Diags[:0]
diag_loop:
	for _, d := range l.result.Diags {
		if !isLintCode(d.Code) {
			kept = append(kept, d)
			continue
		}
		pos := d.PrimaryPos()
		for _, r := range regions {
			if !posIn(pos, r) {
				continue
			}
			if r.codes == nil {
				continue diag_loop // suppressed by wildcard
			}
			if r.codes[d.Code] {
				continue diag_loop
			}
		}
		kept = append(kept, d)
	}
	l.result.Diags = kept
}

// posIn tests whether pos falls within region's [start, end] bounds.
func posIn(pos token.Pos, r allowRegion) bool {
	if pos.Line == 0 {
		return false
	}
	if pos.Line < r.startLine || pos.Line > r.endLine {
		return false
	}
	if pos.Line == r.startLine && pos.Column < r.startCol {
		return false
	}
	if pos.Line == r.endLine && pos.Column > r.endCol {
		return false
	}
	return true
}

// collectAllowRegions appends every allow-annotated declaration's
// region to `out`.
func collectAllowRegions(d ast.Decl, out []allowRegion) []allowRegion {
	switch n := d.(type) {
	case *ast.FnDecl:
		if r, ok := allowRegionFor(n, n.Annotations); ok {
			out = append(out, r)
		}
	case *ast.StructDecl:
		if r, ok := allowRegionFor(n, n.Annotations); ok {
			out = append(out, r)
		}
		for _, f := range n.Fields {
			if r, ok := allowRegionFor(f, f.Annotations); ok {
				out = append(out, r)
			}
		}
		for _, m := range n.Methods {
			if r, ok := allowRegionFor(m, m.Annotations); ok {
				out = append(out, r)
			}
		}
	case *ast.EnumDecl:
		if r, ok := allowRegionFor(n, n.Annotations); ok {
			out = append(out, r)
		}
		for _, v := range n.Variants {
			if r, ok := allowRegionFor(v, v.Annotations); ok {
				out = append(out, r)
			}
		}
		for _, m := range n.Methods {
			if r, ok := allowRegionFor(m, m.Annotations); ok {
				out = append(out, r)
			}
		}
	case *ast.InterfaceDecl:
		if r, ok := allowRegionFor(n, n.Annotations); ok {
			out = append(out, r)
		}
		for _, m := range n.Methods {
			if r, ok := allowRegionFor(m, m.Annotations); ok {
				out = append(out, r)
			}
		}
	case *ast.TypeAliasDecl:
		if r, ok := allowRegionFor(n, n.Annotations); ok {
			out = append(out, r)
		}
	case *ast.LetDecl:
		if r, ok := allowRegionFor(n, n.Annotations); ok {
			out = append(out, r)
		}
	}
	return out
}

// allowRegionFor extracts the allow-region for a single annotated node.
// Returns ok=false when the node has no #[allow(...)] annotation.
func allowRegionFor(n ast.Node, anns []*ast.Annotation) (allowRegion, bool) {
	for _, a := range anns {
		if a.Name != "allow" {
			continue
		}
		return allowRegion{
			startLine: n.Pos().Line,
			startCol:  n.Pos().Column,
			endLine:   n.End().Line,
			endCol:    n.End().Column + 10_000, // lenient end to cover the whole line
			codes:     codesFromAllow(a.Args),
		}, true
	}
	return allowRegion{}, false
}

// codesFromAllow converts annotation args into a set of lint codes.
// Returns nil when a wildcard ("lint" / "all") is present, meaning
// "suppress all lint codes on this region".
func codesFromAllow(args []*ast.AnnotationArg) map[string]bool {
	if len(args) == 0 {
		// Bare `#[allow]` with no args suppresses every lint code —
		// convenient for fully generated / vendored declarations.
		return nil
	}
	codes := map[string]bool{}
	for _, a := range args {
		name := a.Key
		if name == "" {
			continue
		}
		if name == "lint" || name == "all" {
			return nil
		}
		for _, code := range resolveAllowName(name) {
			codes[code] = true
		}
	}
	if len(codes) == 0 {
		return map[string]bool{} // nothing matched — don't suppress anything
	}
	return codes
}

// categoryAliases maps user-facing category names (including
// backwards-compatible synonyms) to the canonical Category values
// defined in registry.go. Keeping this list short and explicit lets us
// reserve `Category` string values for display while still accepting
// ergonomic synonyms in config / annotations.
var categoryAliases = map[string]Category{
	"unused":      CategoryUnused,
	"shadow":      CategoryShadowing,
	"shadowing":   CategoryShadowing,
	"dead_code":   CategoryDeadCode,
	"unreachable": CategoryDeadCode,
	"naming":      CategoryNaming,
	"simplify":    CategorySimplify,
	"suspicious":  CategorySimplify,
	"complexity":  CategoryComplexity,
	"docs":        CategoryDocs,
}

// resolveAllowName maps a single argument name to one or more lint
// codes. Unknown names resolve to an empty slice (silently ignored).
//
// Resolution order:
//  1. A literal lint code (e.g. "L0001") — returned as-is.
//  2. A category alias — returned as every code whose Rule belongs to
//     that category.
//  3. A rule name or code from the registry (LookupRule) — returned as
//     a single-element slice.
//
// Categories take precedence over rule names because the alias
// `dead_code` intentionally names both a category and the specific
// rule L0020 — the broader "suppress all dead-code-family rules"
// reading is the useful one.
//
// Because steps 2 and 3 consult the registry directly, adding a new
// rule or rule name is a one-file change in registry.go.
func resolveAllowName(name string) []string {
	// Direct code reference: L0001, L0040, etc.
	if isLintCode(name) {
		return []string{name}
	}
	// Category alias expands to every rule in that category.
	if cat, ok := categoryAliases[name]; ok {
		rules := RulesByCategory(cat)
		codes := make([]string, 0, len(rules))
		for _, r := range rules {
			codes = append(codes, r.Code)
		}
		return codes
	}
	// Rule lookup (by Code or Name) — handles every entry in allRules.
	if r, ok := LookupRule(name); ok {
		return []string{r.Code}
	}
	return nil
}


// isLintCode reports whether a code string belongs to the L-prefixed
// lint namespace.
func isLintCode(code string) bool {
	if len(code) < 2 || code[0] != 'L' {
		return false
	}
	for i := 1; i < len(code); i++ {
		c := code[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
