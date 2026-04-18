package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

func TestParseSlashSeparatedUsePath(t *testing.T) {
	src := []byte("use github.com/user/lib as lib\n")

	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil || len(file.Uses) != 1 {
		t.Fatalf("parsed uses = %d, want 1", len(file.Uses))
	}

	use := file.Uses[0]
	if got, want := use.RawPath, "github.com/user/lib"; got != want {
		t.Fatalf("RawPath = %q, want %q", got, want)
	}
	if got, want := use.Alias, "lib"; got != want {
		t.Fatalf("Alias = %q, want %q", got, want)
	}
	if got, want := len(use.Path), 1; got != want {
		t.Fatalf("Path len = %d, want %d", got, want)
	}
	if got, want := use.Path[0], "github.com/user/lib"; got != want {
		t.Fatalf("Path[0] = %q, want %q", got, want)
	}
}

func TestParsePreservesAnnotations(t *testing.T) {
	src := []byte(`#[deprecated(since = "0.5", use = "User.new", message = "migrate")]
pub fn makeUser() {}

struct User {
    #[json(key = "user_name")]
    name: String,
    #[json(skip)]
    email: String,
}
`)

	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil || len(file.Decls) != 2 {
		t.Fatalf("parsed decls = %d, want 2", len(file.Decls))
	}

	fnDecl, ok := file.Decls[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("decl[0] type = %T, want *ast.FnDecl", file.Decls[0])
	}
	if got, want := len(fnDecl.Annotations), 1; got != want {
		t.Fatalf("fn annotation count = %d, want %d", got, want)
	}
	if got, want := fnDecl.Annotations[0].Name, "deprecated"; got != want {
		t.Fatalf("fn annotation name = %q, want %q", got, want)
	}
	assertAnnotationArg(t, fnDecl.Annotations[0], "since", "0.5")
	assertAnnotationArg(t, fnDecl.Annotations[0], "use", "User.new")
	assertAnnotationArg(t, fnDecl.Annotations[0], "message", "migrate")

	structDecl, ok := file.Decls[1].(*ast.StructDecl)
	if !ok {
		t.Fatalf("decl[1] type = %T, want *ast.StructDecl", file.Decls[1])
	}
	if got, want := len(structDecl.Fields), 2; got != want {
		t.Fatalf("field count = %d, want %d", got, want)
	}

	nameField := structDecl.Fields[0]
	if got, want := len(nameField.Annotations), 1; got != want {
		t.Fatalf("name field annotation count = %d, want %d", got, want)
	}
	if got, want := nameField.Annotations[0].Name, "json"; got != want {
		t.Fatalf("name field annotation name = %q, want %q", got, want)
	}
	assertAnnotationArg(t, nameField.Annotations[0], "key", "user_name")

	emailField := structDecl.Fields[1]
	if got, want := len(emailField.Annotations), 1; got != want {
		t.Fatalf("email field annotation count = %d, want %d", got, want)
	}
	if got, want := emailField.Annotations[0].Name, "json"; got != want {
		t.Fatalf("email field annotation name = %q, want %q", got, want)
	}
	if got, want := len(emailField.Annotations[0].Args), 1; got != want {
		t.Fatalf("email field annotation arg count = %d, want %d", got, want)
	}
	arg := emailField.Annotations[0].Args[0]
	if got, want := arg.Key, "skip"; got != want {
		t.Fatalf("email field arg key = %q, want %q", got, want)
	}
	if arg.Value != nil {
		t.Fatalf("email field skip arg unexpectedly had a value: %T", arg.Value)
	}
}

func assertAnnotationArg(t *testing.T, ann *ast.Annotation, key, want string) {
	t.Helper()

	for _, arg := range ann.Args {
		if arg.Key != key {
			continue
		}
		got, ok := stringLiteralValue(arg.Value)
		if !ok {
			t.Fatalf("annotation %q arg %q value type = %T, want single-part string literal", ann.Name, key, arg.Value)
		}
		if got != want {
			t.Fatalf("annotation %q arg %q = %q, want %q", ann.Name, key, got, want)
		}
		return
	}

	t.Fatalf("annotation %q missing arg %q", ann.Name, key)
}

func stringLiteralValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.StringLit)
	if !ok || len(lit.Parts) != 1 || !lit.Parts[0].IsLit {
		return "", false
	}
	return lit.Parts[0].Lit, true
}

func TestParseAcceptsStableAliases(t *testing.T) {
	src := []byte("import std.testing as t\nfunc main() {\n    while false {\n        break\n    }\n}\n")

	result := ParseDetailed(src)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed returned %d diagnostics: %v", len(result.Diagnostics), result.Diagnostics[0])
	}
	if result.File == nil || len(result.File.Uses) != 1 || len(result.File.Decls) != 1 {
		t.Fatalf("parsed file = %#v, want one use and one decl", result.File)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("decl[0] type = %T, want *ast.FnDecl", result.File.Decls[0])
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("fn body stmt count = %d, want 1", len(fn.Body.Stmts))
	}
	if _, ok := fn.Body.Stmts[0].(*ast.ForStmt); !ok {
		t.Fatalf("body stmt type = %T, want *ast.ForStmt from stable `while` alias", fn.Body.Stmts[0])
	}
	if result.Provenance == nil || len(result.Provenance.Aliases) != 3 {
		t.Fatalf("alias provenance = %#v, want 3 stable alias entries", result.Provenance)
	}
}

func TestParseStableAliasesPreservedAsIdentifiers(t *testing.T) {
	// Identifiers named after stable aliases (def, func, function, import, while)
	// must survive when they occupy `name : type` slots — parameters, struct
	// fields, keyword arguments. Otherwise FFI signatures that borrow host
	// names (e.g. astbridge `def: Expr` in ParamNode/FieldNode) become E0001.
	src := []byte(`use host.bridge as bridge {
    fn FieldNode(name: String, def: Int) -> Int
    fn ParamNode(name: String, def: Int) -> Int
}
`)
	result := ParseDetailed(src)
	for _, d := range result.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected parse error: %s — %s", d.Code, d.Message)
		}
	}
	if result.Provenance != nil && len(result.Provenance.Aliases) != 0 {
		t.Fatalf("alias provenance = %#v, want no rewrites for param names", result.Provenance.Aliases)
	}
}

func TestParseDetailedLowersEnumerateLoop(t *testing.T) {
	src := []byte("fn main() {\n    let items = [1, 2]\n    for (i, item) in enumerate(items) {\n        println(item)\n    }\n}\n")

	result := ParseDetailed(src)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed returned %d diagnostics: %v", len(result.Diagnostics), result.Diagnostics[0])
	}
	fn := result.File.Decls[0].(*ast.FnDecl)
	if got, want := len(fn.Body.Stmts), 3; got != want {
		t.Fatalf("body stmt count = %d, want %d", got, want)
	}
	if _, ok := fn.Body.Stmts[1].(*ast.LetStmt); !ok {
		t.Fatalf("stmt[1] type = %T, want enumerate temp *ast.LetStmt", fn.Body.Stmts[1])
	}
	if _, ok := fn.Body.Stmts[2].(*ast.ForStmt); !ok {
		t.Fatalf("stmt[2] type = %T, want lowered *ast.ForStmt", fn.Body.Stmts[2])
	}
	loweredLoop := fn.Body.Stmts[2].(*ast.ForStmt)
	rangeIter, ok := loweredLoop.Iter.(*ast.RangeExpr)
	if !ok {
		t.Fatalf("loop iter type = %T, want *ast.RangeExpr", loweredLoop.Iter)
	}
	if _, ok := rangeIter.Stop.(*ast.CallExpr); !ok {
		t.Fatalf("loop stop type = %T, want len() call", rangeIter.Stop)
	}
	prelude, ok := loweredLoop.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("loop prelude type = %T, want *ast.LetStmt", loweredLoop.Body.Stmts[0])
	}
	if _, ok := prelude.Value.(*ast.IndexExpr); !ok {
		t.Fatalf("loop prelude value type = %T, want *ast.IndexExpr", prelude.Value)
	}
	if result.Provenance == nil || len(result.Provenance.Lowerings) == 0 {
		t.Fatalf("lowering provenance = %#v, want enumerate lowering", result.Provenance)
	}
}

func TestParseDetailedLowersSemanticHelpers(t *testing.T) {
	src := []byte("fn main() {\n    let mut items = [1, 2]\n    let count = len(items)\n    let size = items.length\n    items = append(items, count + size)\n}\n")

	result := ParseDetailed(src)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed returned %d diagnostics: %v", len(result.Diagnostics), result.Diagnostics[0])
	}
	fn := result.File.Decls[0].(*ast.FnDecl)
	if got, want := len(fn.Body.Stmts), 4; got != want {
		t.Fatalf("body stmt count = %d, want %d", got, want)
	}
	countLet := fn.Body.Stmts[1].(*ast.LetStmt)
	if _, ok := countLet.Value.(*ast.CallExpr); !ok {
		t.Fatalf("count value type = %T, want *ast.CallExpr", countLet.Value)
	}
	sizeLet := fn.Body.Stmts[2].(*ast.LetStmt)
	if _, ok := sizeLet.Value.(*ast.CallExpr); !ok {
		t.Fatalf("size value type = %T, want *ast.CallExpr", sizeLet.Value)
	}
	if _, ok := fn.Body.Stmts[3].(*ast.ExprStmt); !ok {
		t.Fatalf("stmt[3] type = %T, want append lowered to *ast.ExprStmt", fn.Body.Stmts[3])
	}
	if result.Provenance == nil || len(result.Provenance.Lowerings) < 3 {
		t.Fatalf("lowering provenance = %#v, want helper lowerings", result.Provenance)
	}
}
