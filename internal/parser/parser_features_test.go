package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
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
