package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// lintDocs implements L0070: a `pub` declaration carries no doc
// comment.
//
// `pub` items are the module's external contract; even a one-line
// summary helps consumers pick the right API. The rule fires only when
// no `///` block was attached at parse time.
func (l *linter) lintDocs() {
	for _, d := range l.file.Decls {
		l.docsDecl(d)
	}
}

func (l *linter) docsDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		if n.Pub && n.DocComment == "" {
			l.missingDoc(n, n.Name, "function")
		}
	case *ast.StructDecl:
		if n.Pub {
			if n.DocComment == "" {
				l.missingDoc(n, n.Name, "struct")
			}
			// Methods under a pub struct are part of the API too.
			for _, m := range n.Methods {
				if m.Pub && m.DocComment == "" {
					l.missingDoc(m, m.Name, "method")
				}
			}
		}
	case *ast.EnumDecl:
		if n.Pub {
			if n.DocComment == "" {
				l.missingDoc(n, n.Name, "enum")
			}
			for _, m := range n.Methods {
				if m.Pub && m.DocComment == "" {
					l.missingDoc(m, m.Name, "method")
				}
			}
		}
	case *ast.InterfaceDecl:
		if n.Pub && n.DocComment == "" {
			l.missingDoc(n, n.Name, "interface")
		}
	case *ast.TypeAliasDecl:
		if n.Pub && n.DocComment == "" {
			l.missingDoc(n, n.Name, "type alias")
		}
	case *ast.LetDecl:
		if n.Pub && n.DocComment == "" {
			l.missingDoc(n, n.Name, "binding")
		}
	}
}

func (l *linter) missingDoc(n ast.Node, name, kind string) {
	l.emit(diag.New(diag.Warning,
		"public "+kind+" `"+name+"` has no doc comment").
		Code(diag.CodeMissingDoc).
		Primary(diag.Span{Start: n.Pos(), End: n.End()},
			"missing doc").
		Hint("add a `///` summary above the declaration, or drop `pub` if internal").
		Build())
}
