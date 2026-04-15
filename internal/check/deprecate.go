package check

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// warnIfDeprecated emits W0750 at `pos` when `sym` (or its declaration
// node) carries a `#[deprecated]` annotation. Called by every site that
// reads a symbol's value or type at runtime: identifier references,
// method / field lookups, type references.
//
// The warning preserves the annotation's `since` / `use` / `message`
// arguments so downstream tools can surface the migration path.
func (c *checker) warnIfDeprecated(sym *resolve.Symbol, pos token.Pos) {
	if sym == nil || sym.Decl == nil {
		return
	}
	ann, found := findDeprecated(nodeAnnotations(sym.Decl))
	if !found {
		return
	}
	c.emit(buildDeprecationWarning(sym.Name, ann, pos))
}

// warnIfFieldDeprecated handles the field-level variant (§3.8.2 "for
// deprecated fields, each read or write of the field").
func (c *checker) warnIfFieldDeprecated(f *fieldDesc, pos token.Pos) {
	if f == nil || f.Decl == nil {
		return
	}
	ann, found := findDeprecated(f.Decl.Annotations)
	if !found {
		return
	}
	c.emit(buildDeprecationWarning(f.Name, ann, pos))
}

// warnIfVariantDeprecated handles the variant-level variant.
func (c *checker) warnIfVariantDeprecated(v *variantDesc, pos token.Pos) {
	if v == nil || v.Decl == nil {
		return
	}
	ann, found := findDeprecated(v.Decl.Annotations)
	if !found {
		return
	}
	c.emit(buildDeprecationWarning(v.Name, ann, pos))
}

// warnIfMethodDeprecated handles deprecation on methods.
func (c *checker) warnIfMethodDeprecated(md *methodDesc, pos token.Pos) {
	if md == nil || md.Decl == nil {
		return
	}
	ann, found := findDeprecated(md.Decl.Annotations)
	if !found {
		return
	}
	c.emit(buildDeprecationWarning(md.Name, ann, pos))
}

// findDeprecated scans an annotation list for `#[deprecated(...)]` and
// returns it along with a found flag.
func findDeprecated(annots []*ast.Annotation) (*ast.Annotation, bool) {
	for _, a := range annots {
		if a.Name == "deprecated" {
			return a, true
		}
	}
	return nil, false
}

// nodeAnnotations pulls the Annotations slice from whichever concrete
// decl type backs a Symbol. Returns nil for kinds without annotations.
func nodeAnnotations(n ast.Node) []*ast.Annotation {
	switch v := n.(type) {
	case *ast.FnDecl:
		return v.Annotations
	case *ast.StructDecl:
		return v.Annotations
	case *ast.EnumDecl:
		return v.Annotations
	case *ast.InterfaceDecl:
		return v.Annotations
	case *ast.TypeAliasDecl:
		return v.Annotations
	case *ast.LetDecl:
		return v.Annotations
	case *ast.Field:
		return v.Annotations
	case *ast.Variant:
		return v.Annotations
	}
	return nil
}

// buildDeprecationWarning formats the user-facing warning, lifting the
// annotation's arguments (since / use / message) into the message.
func buildDeprecationWarning(name string, ann *ast.Annotation, pos token.Pos) *diag.Diagnostic {
	var parts []string
	for _, a := range ann.Args {
		switch a.Key {
		case "since":
			if s, ok := literalString(a.Value); ok {
				parts = append(parts, "since "+s)
			}
		case "use":
			if s, ok := literalString(a.Value); ok {
				parts = append(parts, "use `"+s+"` instead")
			}
		case "message":
			if s, ok := literalString(a.Value); ok {
				parts = append(parts, s)
			}
		}
	}
	msg := "`" + name + "` is deprecated"
	if len(parts) > 0 {
		msg += " — " + strings.Join(parts, "; ")
	}
	return diag.New(diag.Warning, msg).
		Code(diag.CodeDeprecatedUse).
		PrimaryPos(pos, "").
		Build()
}

// literalString extracts the value of a string-literal annotation arg.
// Returns ("", false) for non-literal / non-string values.
func literalString(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, p := range lit.Parts {
		if !p.IsLit {
			return "", false // interpolated — not a static string
		}
		b.WriteString(p.Lit)
	}
	return b.String(), true
}
