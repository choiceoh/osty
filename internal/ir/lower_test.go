package ir

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestLowerClassifiesTopLevelLetRefsAsGlobal(t *testing.T) {
	global := &ast.LetDecl{
		Name:  "g",
		Value: &ast.IntLit{Text: "1"},
	}
	ref := &ast.Ident{ID: 1, Name: "g"}
	closure := &ast.ClosureExpr{Body: ref}
	file := &ast.File{
		Decls: []ast.Decl{global},
		Stmts: []ast.Stmt{&ast.ExprStmt{X: closure}},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			ref.ID: {Name: "g", Kind: resolve.SymLet, Decl: global},
		},
		RefIdents: []*ast.Ident{ref},
	}

	mod, issues := Lower("main", file, res, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	cl, ok := stmt.X.(*Closure)
	if !ok {
		t.Fatalf("stmt.X = %T, want *Closure", stmt.X)
	}
	if len(cl.Captures) != 1 {
		t.Fatalf("closure captures = %+v, want 1 global capture", cl.Captures)
	}
	if cl.Captures[0].Kind != CaptureGlobal {
		t.Fatalf("capture kind = %v, want %v", cl.Captures[0].Kind, CaptureGlobal)
	}
	id, ok := cl.Body.Result.(*Ident)
	if !ok {
		t.Fatalf("closure body result = %T, want *Ident", cl.Body.Result)
	}
	if id.Kind != IdentGlobal {
		t.Fatalf("ident kind = %v, want %v", id.Kind, IdentGlobal)
	}
}

func TestLowerPreludeVariantCallBecomesVariantLit(t *testing.T) {
	some := &ast.Ident{ID: 1, Name: "Some"}
	call := &ast.CallExpr{
		Fn: some,
		Args: []*ast.Arg{{
			Value: &ast.IntLit{Text: "1"},
		}},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: call}},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			some.ID: {Name: "Some", Kind: resolve.SymBuiltin, Pub: true},
		},
		RefIdents: []*ast.Ident{some},
	}

	mod, issues := Lower("main", file, res, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	lit, ok := stmt.X.(*VariantLit)
	if !ok {
		t.Fatalf("stmt.X = %T, want *VariantLit", stmt.X)
	}
	if lit.Enum != "" {
		t.Fatalf("variant enum = %q, want empty prelude enum", lit.Enum)
	}
	if lit.Variant != "Some" {
		t.Fatalf("variant name = %q, want %q", lit.Variant, "Some")
	}
	if got := len(lit.Args); got != 1 {
		t.Fatalf("variant args = %d, want 1", got)
	}
}

func TestLowerStatementIfBecomesIfStmt(t *testing.T) {
	cond := &ast.BoolLit{Value: true}
	ifExpr := &ast.IfExpr{
		Cond: cond,
		Then: &ast.Block{
			Stmts: []ast.Stmt{&ast.ExprStmt{X: &ast.IntLit{Text: "1"}}},
		},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: ifExpr}},
	}

	mod, issues := Lower("main", file, nil, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	if _, ok := mod.Script[0].(*IfStmt); !ok {
		t.Fatalf("script[0] = %T, want *IfStmt", mod.Script[0])
	}
}

func TestLowerStatementMatchBecomesMatchStmt(t *testing.T) {
	match := &ast.MatchExpr{
		Scrutinee: &ast.IntLit{Text: "1"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.LiteralPat{Literal: &ast.IntLit{Text: "1"}},
				Body:    &ast.IntLit{Text: "10"},
			},
			{
				Pattern: &ast.WildcardPat{},
				Body:    &ast.IntLit{Text: "0"},
			},
		},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: match}},
	}

	mod, issues := Lower("main", file, nil, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	stmt, ok := mod.Script[0].(*MatchStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *MatchStmt", mod.Script[0])
	}
	if stmt.Tree == nil {
		t.Fatal("MatchStmt.Tree = nil, want compiled decision tree")
	}
	if got := len(stmt.Arms); got != 2 {
		t.Fatalf("match arms = %d, want 2", got)
	}
}

func TestLowerMethodCallRecoversDowncastOptionalType(t *testing.T) {
	src := `interface Printable {
    fn show(self) -> String
}

struct Note {
    pub msg: String,

    pub fn show(self) -> String {
        self.msg
    }
}

fn probe(p: Printable) -> Note? {
    return p.downcast::<Note>()
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})

	probe, ok := file.Decls[len(file.Decls)-1].(*ast.FnDecl)
	if !ok || probe == nil || probe.Body == nil {
		t.Fatalf("probe decl = %T, want *ast.FnDecl with body", file.Decls[len(file.Decls)-1])
	}
	if len(probe.Body.Stmts) != 1 {
		t.Fatalf("probe body stmts = %d, want 1", len(probe.Body.Stmts))
	}
	retStmt, ok := probe.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok || retStmt == nil {
		t.Fatalf("probe stmt = %T, want *ast.ReturnStmt", probe.Body.Stmts[0])
	}
	call, ok := retStmt.Value.(*ast.CallExpr)
	if !ok || call == nil {
		t.Fatalf("probe return value = %T, want *ast.CallExpr", retStmt.Value)
	}
	delete(chk.Types, call)

	mod, issues := Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	var irProbe *FnDecl
	for _, decl := range mod.Decls {
		if fn, ok := decl.(*FnDecl); ok && fn.Name == "probe" {
			irProbe = fn
			break
		}
	}
	if irProbe == nil || irProbe.Body == nil {
		t.Fatal("lowered probe function missing body")
	}
	if len(irProbe.Body.Stmts) != 1 {
		t.Fatalf("lowered probe stmts = %d, want 1", len(irProbe.Body.Stmts))
	}
	irRet, ok := irProbe.Body.Stmts[0].(*ReturnStmt)
	if !ok || irRet == nil {
		t.Fatalf("lowered probe stmt = %T, want *ReturnStmt", irProbe.Body.Stmts[0])
	}
	mc, ok := irRet.Value.(*MethodCall)
	if !ok {
		t.Fatalf("probe return value = %T, want *MethodCall", irRet.Value)
	}
	opt, ok := mc.T.(*OptionalType)
	if !ok {
		t.Fatalf("method call type = %T (%v), want *OptionalType", mc.T, mc.T)
	}
	named, ok := opt.Inner.(*NamedType)
	if !ok {
		t.Fatalf("optional inner = %T (%v), want *NamedType", opt.Inner, opt.Inner)
	}
	if named.Name != "Note" {
		t.Fatalf("optional inner name = %q, want %q", named.Name, "Note")
	}
}

func TestLowerUseDeclRecoversBuiltinGenericTypesWithoutResolverTypeRefs(t *testing.T) {
	src := `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})

	mod, issues := Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("decls = %d, want 1", len(mod.Decls))
	}
	use, ok := mod.Decls[0].(*UseDecl)
	if !ok || use == nil {
		t.Fatalf("decl = %T, want *UseDecl", mod.Decls[0])
	}
	if got, want := len(use.GoBody), 1; got != want {
		t.Fatalf("use.GoBody len = %d, want %d", got, want)
	}
	fn, ok := use.GoBody[0].(*FnDecl)
	if !ok || fn == nil {
		t.Fatalf("use.GoBody[0] = %T, want *FnDecl", use.GoBody[0])
	}
	ret, ok := fn.Return.(*NamedType)
	if !ok || ret == nil {
		t.Fatalf("fn return = %T (%v), want *NamedType", fn.Return, fn.Return)
	}
	if !ret.Builtin || ret.Name != "List" {
		t.Fatalf("fn return = %#v, want builtin List", ret)
	}
	if got, want := len(ret.Args), 1; got != want {
		t.Fatalf("return args = %d, want %d", got, want)
	}
	if ret.Args[0] != TString {
		t.Fatalf("return inner = %v, want TString", ret.Args[0])
	}
}
