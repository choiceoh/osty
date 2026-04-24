package check

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/stdlib"
)

type fakeNativeChecker struct {
	result api.CheckResult
	err    error
}

func (f fakeNativeChecker) CheckSourceStructured([]byte) (api.CheckResult, error) {
	return f.result, f.err
}

type fakeNativePackageChecker struct {
	t            *testing.T
	result       api.CheckResult
	err          error
	sourceCalls  int
	packageCalls int
	imports      []selfhost.PackageCheckImport
}

func (f *fakeNativePackageChecker) CheckSourceStructured([]byte) (api.CheckResult, error) {
	f.sourceCalls++
	if f.t != nil {
		f.t.Fatalf("file-mode checker unexpectedly used raw source path")
	}
	return api.CheckResult{}, fmt.Errorf("unexpected raw source path")
}

func (f *fakeNativePackageChecker) CheckPackageStructured(input selfhost.PackageCheckInput) (api.CheckResult, error) {
	f.packageCalls++
	f.imports = append([]selfhost.PackageCheckImport(nil), input.Imports...)
	return f.result, f.err
}

func TestNativeBoundaryPrefersStructuredPackageCheckerForFileMode(t *testing.T) {
	src := []byte(`fn main() {
    let value = 1
}
`)
	file, res := parseResolvedFile(t, src)
	letStmt := file.Decls[0].(*ast.FnDecl).Body.Stmts[0].(*ast.LetStmt)

	fake := &fakeNativePackageChecker{
		t: t,
		result: api.CheckResult{
			Summary: api.CheckSummary{Assignments: 1, Accepted: 1, Errors: 0},
			Bindings: []api.CheckedBinding{{
				Name:     "value",
				TypeName: "UntypedInt",
				Start:    letStmt.Pattern.Pos().Offset,
				End:      letStmt.Pattern.End().Offset,
			}},
		},
	}

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) { return fake, "" }
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if fake.packageCalls != 1 {
		t.Fatalf("packageCalls = %d, want 1", fake.packageCalls)
	}
	if fake.sourceCalls != 0 {
		t.Fatalf("sourceCalls = %d, want 0", fake.sourceCalls)
	}
	if got := chk.LetTypes[letStmt]; got == nil || got.String() != "untyped-int" {
		t.Fatalf("binding type = %v, want untyped-int", got)
	}
}

func TestNativeBoundaryBuildsImportSurfacesForStructuredFileMode(t *testing.T) {
	src := []byte(`use std.strings as strings

fn main() {
    let joined = strings.join(["a"], ",")
}
`)
	file, res := parseResolvedFile(t, src)

	fake := &fakeNativePackageChecker{t: t}
	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) { return fake, "" }
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	_ = SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})

	if fake.packageCalls != 1 {
		t.Fatalf("packageCalls = %d, want 1", fake.packageCalls)
	}
	foundStrings := false
	for _, imp := range fake.imports {
		if imp.Alias == "strings" && (len(imp.Functions) > 0 || len(imp.Fields) > 0 || len(imp.TypeDecls) > 0) {
			foundStrings = true
			break
		}
	}
	if !foundStrings {
		t.Fatalf("structured file-mode imports = %#v, want populated std.strings surface", fake.imports)
	}
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
		return fakeNativeChecker{result: api.CheckResult{
			Summary: api.CheckSummary{Assignments: 1, Accepted: 1, Errors: 0},
			TypedNodes: []api.CheckedNode{
				{Kind: "Call", TypeName: "Int", Start: call.Pos().Offset, End: call.End().Offset},
				{Kind: "IntLit", TypeName: "Int", Start: lit.Pos().Offset, End: lit.End().Offset},
			},
			Bindings: []api.CheckedBinding{
				{Name: "answer", TypeName: "Int", Start: letStmt.Pattern.Pos().Offset, End: letStmt.Pattern.End().Offset},
			},
			Symbols: []api.CheckedSymbol{
				{Name: "id", Kind: "fn", TypeName: "fn(T) -> T", Start: idDecl.Pos().Offset, End: idDecl.End().Offset},
			},
			Instantiations: []api.CheckInstantiation{
				{Callee: "id", TypeArgs: []string{"Int"}, Start: call.Pos().Offset, End: call.End().Offset},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
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
	if len(chk.InstantiationsByID) != 1 {
		t.Fatalf("instantiations = %#v, want one entry", chk.InstantiationsByID)
	}
	if got := chk.InstantiationsByID[call.ID]; len(got) != 1 || got[0].String() != "Int" {
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
		return fakeNativeChecker{result: api.CheckResult{
			Summary: api.CheckSummary{Assignments: 0, Accepted: 0, Errors: 0},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	// Must not panic.
	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
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

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
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
		return fakeNativeChecker{result: api.CheckResult{
			Summary: api.CheckSummary{Assignments: 1, Accepted: 1, Errors: 0},
			Bindings: []api.CheckedBinding{
				{Name: "value", TypeName: "Int", Start: param.Pos().Offset, End: param.End().Offset},
			},
			Symbols: []api.CheckedSymbol{
				{Name: "unused", Kind: "fn", TypeName: "fn(Int) -> Int", Start: unusedDecl.Pos().Offset, End: unusedDecl.End().Offset},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
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

func TestConvertNativeDiagPreservesFile(t *testing.T) {
	got := convertNativeDiag([]byte("fn main() {}\n"), api.CheckDiagnosticRecord{
		Code:     diag.CodeIntrinsicNonEmptyBody,
		Severity: "error",
		Message:  "`#[intrinsic]` function `violator` must have an empty body",
		Start:    0,
		End:      0,
		File:     "/tmp/bad.osty",
	})
	if got == nil {
		t.Fatal("convertNativeDiag returned nil")
	}
	if got.File != "/tmp/bad.osty" {
		t.Fatalf("diag.File = %q, want /tmp/bad.osty", got.File)
	}
}

func TestNativeBoundaryDoesNotReplayGoPolicyGates(t *testing.T) {
	src := []byte(`#[intrinsic]
pub fn raw_null() -> Int { 0 }
`)
	file, res := parseResolvedFile(t, src)
	fn := file.Decls[0].(*ast.FnDecl)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) {
		return fakeNativeChecker{result: api.CheckResult{
			Summary: api.CheckSummary{},
			Diagnostics: []api.CheckDiagnosticRecord{
				{
					Code:     diag.CodeIntrinsicNonEmptyBody,
					Severity: "error",
					Message:  "`#[intrinsic]` function `raw_null` must have an empty body",
					Start:    fn.Body.Pos().Offset,
					End:      fn.Body.End().Offset,
				},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})

	got := 0
	for _, d := range chk.Diags {
		if d != nil && d.Code == diag.CodeIntrinsicNonEmptyBody {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("expected exactly one native E0773 diagnostic, got %d: %#v", got, chk.Diags)
	}
}

func TestNativeBoundarySuppressesPrivilegeDiagnosticsInPrivilegedMode(t *testing.T) {
	src := []byte(`#[intrinsic]
pub fn raw_null() -> Int { }
`)
	file, res := parseResolvedFile(t, src)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = func() (nativeChecker, string) {
		return fakeNativeChecker{result: api.CheckResult{
			Summary: api.CheckSummary{
				Assignments:     1,
				Accepted:        1,
				Errors:          1,
				ErrorsByContext: map[string]int{diag.CodeRuntimePrivilegeViolation: 1},
				ErrorDetails: map[string]map[string]int{
					diag.CodeRuntimePrivilegeViolation: {
						"`#[intrinsic]` is a runtime-only annotation": 1,
					},
				},
			},
			Diagnostics: []api.CheckDiagnosticRecord{
				{
					Code:     diag.CodeRuntimePrivilegeViolation,
					Severity: "error",
					Message:  "`#[intrinsic]` is a runtime-only annotation",
					Start:    0,
					End:      0,
				},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached(), Privileged: true})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected privileged host boundary to suppress E0770 diagnostics, got %#v", chk.Diags)
	}
	if chk.NativeCheckerTelemetry == nil {
		t.Fatal("expected native telemetry to survive filtering")
	}
	if chk.NativeCheckerTelemetry.Errors != 0 {
		t.Fatalf("telemetry errors = %d, want 0", chk.NativeCheckerTelemetry.Errors)
	}
	if got := chk.NativeCheckerTelemetry.ErrorsByContext[diag.CodeRuntimePrivilegeViolation]; got != 0 {
		t.Fatalf("telemetry retained privileged bucket count %d", got)
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
		return fakeNativeChecker{result: api.CheckResult{
			Summary: api.CheckSummary{Assignments: 1, Accepted: 1, Errors: 0},
			TypedNodes: []api.CheckedNode{
				{Kind: "Call", TypeName: "Int", Start: start, End: end},
			},
		}}, ""
	}
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: canonicalSrc, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LookupType(call); got == nil || got.String() != "Int" {
		t.Fatalf("call type = %v, want Int", got)
	}
}

func TestFileUsesEmbeddedCheckerByDefaultWhenEnvUnset(t *testing.T) {
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

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
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
