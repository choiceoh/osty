package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// runIntrinsicBodyChecks enforces LANG_SPEC §19.6: a function
// carrying `#[intrinsic]` must have an empty body. The accepted
// forms are:
//
//	#[intrinsic] pub fn foo() -> T          // signature only
//	#[intrinsic] pub fn foo() -> T {}       // explicit empty block
//
// Anything in between is rejected with `E0773`. The backend never
// looks at an intrinsic's body — the implementation is supplied by
// the lowering layer per §19.7 — so a non-empty body is silently
// ignored and creates misleading source code.
//
// This check fires regardless of privilege: an `#[intrinsic]` in a
// non-privileged package is already rejected by the privilege gate
// (`E0770`), but if it slipped through (e.g. in a stdlib stub or a
// privileged user package), the body-shape rule applies. Tests run
// against ordinary user fixtures because the privilege gate path
// is independently exercised by `privilege_test.go`.
func runIntrinsicBodyChecks(file *ast.File) []*diag.Diagnostic {
	if file == nil {
		return nil
	}
	var diags []*diag.Diagnostic
	for _, d := range file.Decls {
		switch n := d.(type) {
		case *ast.FnDecl:
			if d := checkIntrinsicBody(n); d != nil {
				diags = append(diags, d)
			}
		case *ast.StructDecl:
			for _, m := range n.Methods {
				if m == nil {
					continue
				}
				if d := checkIntrinsicBody(m); d != nil {
					diags = append(diags, d)
				}
			}
		case *ast.EnumDecl:
			for _, m := range n.Methods {
				if m == nil {
					continue
				}
				if d := checkIntrinsicBody(m); d != nil {
					diags = append(diags, d)
				}
			}
		}
	}
	return diags
}

func checkIntrinsicBody(fn *ast.FnDecl) *diag.Diagnostic {
	if fn == nil {
		return nil
	}
	if !hasIntrinsicAnnotation(fn.Annotations) {
		return nil
	}
	if fn.Body == nil {
		// Body-less signature — the canonical form.
		return nil
	}
	if len(fn.Body.Stmts) == 0 {
		// Empty block — also canonical.
		return nil
	}
	// Body has at least one statement; reject.
	return diag.New(diag.Error,
		"`#[intrinsic]` function `"+fn.Name+"` must have an empty body").
		Code(diag.CodeIntrinsicNonEmptyBody).
		Primary(diag.Span{Start: fn.Body.Pos(), End: fn.Body.End()},
			"intrinsic body must be empty").
		Note("LANG_SPEC §19.6: intrinsic implementations are supplied by the lowering layer; the source body is ignored").
		Note("hint: keep the signature and drop the body, or use `{}`").
		Build()
}

func hasIntrinsicAnnotation(annots []*ast.Annotation) bool {
	for _, a := range annots {
		if a != nil && a.Name == "intrinsic" {
			return true
		}
	}
	return false
}
