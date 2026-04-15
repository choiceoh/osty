package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// #[allow(...)] suppression.
//
// A user can opt out of specific lint warnings by tagging a declaration
// with `#[allow(NAME, NAME, ...)]`. Accepted NAMEs:
//
//   - A concrete code: `L0001`, `L0040`, ...
//   - A category alias: `unused`, `shadow`, `dead_code`, `naming`,
//     `simplify`
//   - A rule alias: `unused_let`, `unused_param`, `unused_import`,
//     `unused_mut`, `unused_field`, `unused_method`, `ignored_result`,
//     `shadowed_binding`, `dead_code`, `naming_type`, `naming_value`,
//     `naming_variant`, `redundant_bool`, `self_compare`, `self_assign`
//   - The wildcards `lint` or `all`
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

// resolveAllowName maps a single argument name to one or more lint
// codes. Unknown names resolve to an empty slice (silently ignored).
func resolveAllowName(name string) []string {
	// Direct code reference: L0001, L0040, etc.
	if isLintCode(name) {
		return []string{name}
	}
	// Category aliases.
	switch name {
	case "unused":
		return []string{
			diag.CodeUnusedLet, diag.CodeUnusedParam, diag.CodeUnusedImport,
			diag.CodeUnusedMut, diag.CodeUnusedField, diag.CodeUnusedMethod,
			diag.CodeIgnoredResult,
		}
	case "shadow", "shadowing":
		return []string{diag.CodeShadowedBinding}
	case "dead_code", "unreachable":
		return []string{diag.CodeDeadCode}
	case "naming":
		return []string{
			diag.CodeNamingType, diag.CodeNamingValue, diag.CodeNamingVariant,
		}
	case "simplify", "suspicious":
		return []string{
			diag.CodeRedundantBool, diag.CodeSelfCompare, diag.CodeSelfAssign,
		}
	}
	// Individual rule aliases.
	switch name {
	case "unused_let":
		return []string{diag.CodeUnusedLet}
	case "unused_param":
		return []string{diag.CodeUnusedParam}
	case "unused_import":
		return []string{diag.CodeUnusedImport}
	case "unused_mut":
		return []string{diag.CodeUnusedMut}
	case "unused_field":
		return []string{diag.CodeUnusedField}
	case "unused_method":
		return []string{diag.CodeUnusedMethod}
	case "ignored_result":
		return []string{diag.CodeIgnoredResult}
	case "shadowed_binding":
		return []string{diag.CodeShadowedBinding}
	case "naming_type":
		return []string{diag.CodeNamingType}
	case "naming_value":
		return []string{diag.CodeNamingValue}
	case "naming_variant":
		return []string{diag.CodeNamingVariant}
	case "redundant_bool":
		return []string{diag.CodeRedundantBool}
	case "self_compare":
		return []string{diag.CodeSelfCompare}
	case "self_assign":
		return []string{diag.CodeSelfAssign}
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
