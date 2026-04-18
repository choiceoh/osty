package check

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type fakeNativeChecker struct {
	result nativeCheckResult
	err    error
}

func (f fakeNativeChecker) CheckSourceStructured([]byte) (nativeCheckResult, error) {
	return f.result, f.err
}

func TestNativeBoundaryOverlaysStructuredCheckResult(t *testing.T) {
	src := []byte(`fn id<T>(value: T) -> T { value }

fn main() {
    let answer = id::<Int>(1)
}
`)
	file, res := parseResolvedFile(t, src)
	idDecl := file.Decls[0].(*ast.FnDecl)
	mainDecl := file.Decls[1].(*ast.FnDecl)
	letStmt := mainDecl.Body.Stmts[0].(*ast.LetStmt)
	call := letStmt.Value.(*ast.CallExpr)
	lit := call.Args[0].Value.(*ast.IntLit)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) {
		return fakeNativeChecker{result: nativeCheckResult{
			Summary: nativeCheckSummary{Assignments: 1, Accepted: 1, Errors: 0},
			TypedNodes: []nativeCheckedNode{
				{Kind: "Call", TypeName: "Int", Start: call.Pos().Offset, End: call.End().Offset},
				{Kind: "IntLit", TypeName: "Int", Start: lit.Pos().Offset, End: lit.End().Offset},
			},
			Bindings: []nativeCheckedBinding{
				{Name: "answer", TypeName: "Int", Start: letStmt.Pattern.Pos().Offset, End: letStmt.Pattern.End().Offset},
			},
			Symbols: []nativeCheckedSymbol{
				{Name: "id", Kind: "fn", TypeName: "fn(T) -> T", Start: idDecl.Pos().Offset, End: idDecl.End().Offset},
			},
			Instantiations: []nativeCheckInstantiation{
				{Callee: "id", TypeArgs: []string{"Int"}, Start: call.Pos().Offset, End: call.End().Offset},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LookupType(call); got == nil || got.String() != "Int" {
		t.Fatalf("call type = %v, want Int", got)
	}
	if got := chk.LookupType(lit); got == nil || got.String() != "Int" {
		t.Fatalf("literal type = %v, want Int", got)
	}
	if got := chk.LetTypes[letStmt]; got == nil || got.String() != "Int" {
		t.Fatalf("binding type = %v, want Int", got)
	}
	if got := chk.LookupSymType(res.FileScope.Lookup("id")); got == nil || got.String() != "fn(T) -> T" {
		t.Fatalf("symbol type = %v, want fn(T) -> T", got)
	}
	if len(chk.Instantiations) != 1 {
		t.Fatalf("instantiations = %#v, want one entry", chk.Instantiations)
	}
	if got := chk.Instantiations[call]; len(got) != 1 || got[0].String() != "Int" {
		t.Fatalf("call instantiation = %#v, want [Int]", got)
	}
}

// Regression: FnDecl.Body is nil for interface-declared methods without a
// default. Passing it as ast.Node turned the nil *ast.Block into a typed-nil
// interface that slipped past the n == nil guard in addNode and
// nil-dereferenced inside the Block arm of the type switch.
func TestNativeBoundaryIndexesInterfaceMethodWithoutDefaultBody(t *testing.T) {
	src := []byte(`pub interface Reader {
    fn read(self, n: Int) -> Int
    fn close(self) -> Int { 0 }
}

fn main() {}
`)
	file, res := parseResolvedFile(t, src)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) {
		return fakeNativeChecker{result: nativeCheckResult{
			Summary: nativeCheckSummary{Assignments: 0, Accepted: 0, Errors: 0},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	// Must not panic.
	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
}

func TestNativeBoundaryReportsMissingExecutable(t *testing.T) {
	src := []byte("fn main() {}\n")
	file, res := parseResolvedFile(t, src)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) { return nil, "missing test native checker" }
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 1 {
		t.Fatalf("diagnostics = %#v, want one unavailability diagnostic", chk.Diags)
	}
	if got := chk.Diags[0].Message; !strings.Contains(got, "type checking unavailable for file") {
		t.Fatalf("message = %q, want file unavailability", got)
	}
	if notes := strings.Join(chk.Diags[0].Notes, "\n"); !strings.Contains(notes, "no Osty-native checker executable is configured") || !strings.Contains(notes, "missing test native checker") {
		t.Fatalf("notes = %q, want native checker configuration details", notes)
	}
}

func TestNativeBoundaryPreservesUnreferencedSymbolTypes(t *testing.T) {
	src := []byte(`fn unused(value: Int) -> Int {
    0
}

fn main() {}
`)
	file, res := parseResolvedFile(t, src)
	unusedDecl := file.Decls[0].(*ast.FnDecl)
	param := unusedDecl.Params[0]

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) {
		return fakeNativeChecker{result: nativeCheckResult{
			Summary: nativeCheckSummary{Assignments: 1, Accepted: 1, Errors: 0},
			Bindings: []nativeCheckedBinding{
				{Name: "value", TypeName: "Int", Start: param.Pos().Offset, End: param.End().Offset},
			},
			Symbols: []nativeCheckedSymbol{
				{Name: "unused", Kind: "fn", TypeName: "fn(Int) -> Int", Start: unusedDecl.Pos().Offset, End: unusedDecl.End().Offset},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LookupSymType(res.FileScope.Lookup("unused")); got == nil || got.String() != "fn(Int) -> Int" {
		t.Fatalf("unused fn type = %v, want fn(Int) -> Int", got)
	}
	paramSym := lookupChildScopeSymbol(res.FileScope, "fn:unused", "value")
	if paramSym == nil {
		t.Fatal("expected resolver symbol for unused parameter")
	}
	if got := chk.LookupSymType(paramSym); got == nil || got.String() != "Int" {
		t.Fatalf("unused param type = %v, want Int", got)
	}
}

func TestNativeBoundaryOverlaysCanonicalSourceSpansBackToOriginalAST(t *testing.T) {
	src := []byte(`func main() {
    let xs = [1]
    let n = len(xs)
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[0].(*ast.FnDecl)
	letStmt := mainDecl.Body.Stmts[1].(*ast.LetStmt)
	call := letStmt.Value.(*ast.CallExpr)

	canonicalSrc, _ := canonical.SourceWithMap(src, file)
	start := bytes.Index(canonicalSrc, []byte("xs.len()"))
	if start < 0 {
		t.Fatalf("canonical source = %q, want lowered xs.len()", canonicalSrc)
	}
	end := start + len("xs.len()")

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) {
		return fakeNativeChecker{result: nativeCheckResult{
			Summary: nativeCheckSummary{Assignments: 1, Accepted: 1, Errors: 0},
			TypedNodes: []nativeCheckedNode{
				{Kind: "Call", TypeName: "Int", Start: start, End: end},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: canonicalSrc, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LookupType(call); got == nil || got.String() != "Int" {
		t.Fatalf("call type = %v, want Int", got)
	}
}

func TestFileFallsBackToEmbeddedCheckerWhenManagedArtifactUnavailable(t *testing.T) {
	src := []byte(`fn id<T>(value: T) -> T { value }

fn main() {
    let answer = id::<Int>(1)
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[1].(*ast.FnDecl)
	letStmt := mainDecl.Body.Stmts[0].(*ast.LetStmt)
	call := letStmt.Value.(*ast.CallExpr)

	t.Setenv(nativeCheckerEnv, "")

	oldEnsure := ensureManagedNativeChecker
	ensureManagedNativeChecker = func() (string, error) {
		return "", fmt.Errorf("boom")
	}
	t.Cleanup(func() { ensureManagedNativeChecker = oldEnsure })

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LookupType(call); got == nil || got.String() != "Int" {
		t.Fatalf("call type = %v, want Int", got)
	}
	if got := chk.LetTypes[letStmt]; got == nil || got.String() != "Int" {
		t.Fatalf("binding type = %v, want Int", got)
	}
}

func lookupChildScopeSymbol(root *resolve.Scope, kind, name string) *resolve.Symbol {
	if root == nil {
		return nil
	}
	for _, child := range root.Children() {
		if child.Kind() == kind {
			return child.LookupLocal(name)
		}
		if sym := lookupChildScopeSymbol(child, kind, name); sym != nil {
			return sym
		}
	}
	return nil
}

func parseResolvedFile(t *testing.T, src []byte) (*ast.File, *resolve.Result) {
	t.Helper()
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	if len(res.Diags) != 0 {
		t.Fatalf("resolve diagnostics: %v", res.Diags)
	}
	return file, res
}
