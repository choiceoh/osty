package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type lirProtoShadowFixture struct {
	Name       string
	SourcePath string
	Source     string
	Tags       []string
	Needles    []string
}

func TestLIRProtoCurrentGeneratorSourceFixturesShadowParity(t *testing.T) {
	fixtures := loadLIRProtoCurrentGeneratorSourceFixtures(t)
	if got, want := len(fixtures), 9; got != want {
		t.Fatalf("current-generator fixture count = %d, want %d", got, want)
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			mirMod := lowerLIRProtoShadowSourceToMIR(t, fixture.Source)
			out, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: fixture.SourcePath})
			if err != nil {
				t.Fatalf("GenerateFromMIR: %v", err)
			}
			got := string(out)
			for _, want := range fixture.Needles {
				if !strings.Contains(got, want) {
					t.Fatalf("current MIR generator output missing %q for %s:\n%s", want, fixture.Name, got)
				}
			}
		})
	}
}

func loadLIRProtoCurrentGeneratorSourceFixtures(t *testing.T) []lirProtoShadowFixture {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	path := filepath.Join(root, "toolchain", "lir_proto_parity.osty")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse %s diagnostics = %v", path, diags)
	}
	if file == nil {
		t.Fatalf("parse %s returned nil file", path)
	}

	sourceFixtures := map[string]lirProtoShadowFixture{}
	taggedCurrent := map[string]bool{}
	var currentHelper *ast.FnDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		if fn.Name == "lirParityCurrentGeneratorSourceFixtures" {
			currentHelper = fn
			continue
		}
		if !strings.HasPrefix(fn.Name, "lirParitySource") || !strings.HasSuffix(fn.Name, "Fixture") {
			continue
		}
		fixture, ok := parseLIRProtoSourceFixtureDecl(t, fn)
		if !ok {
			continue
		}
		sourceFixtures[fn.Name] = fixture
		if fixture.hasTag("current-generator") {
			taggedCurrent[fn.Name] = true
		}
	}
	if currentHelper == nil {
		t.Fatalf("%s does not define lirParityCurrentGeneratorSourceFixtures", path)
	}

	var fixtures []lirProtoShadowFixture
	for _, ref := range currentGeneratorSourceFixtureRefs(t, currentHelper) {
		fixture, ok := sourceFixtures[ref]
		if !ok {
			t.Fatalf("lirParityCurrentGeneratorSourceFixtures references unknown source fixture %s", ref)
		}
		if !taggedCurrent[ref] {
			t.Fatalf("%s is in lirParityCurrentGeneratorSourceFixtures without current-generator tag", ref)
		}
		if fixture.hasTag("lir-only") {
			t.Fatalf("%s is tagged as both current-generator and lir-only", fixture.Name)
		}
		fixtures = append(fixtures, fixture)
	}
	if len(fixtures) != len(taggedCurrent) {
		for name := range taggedCurrent {
			found := false
			for _, fixture := range fixtures {
				if sourceFixtures[name].Name == fixture.Name {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s has current-generator tag but is missing from lirParityCurrentGeneratorSourceFixtures", name)
			}
		}
		t.Fatalf("current-generator helper fixture count = %d, tagged fixture count = %d", len(fixtures), len(taggedCurrent))
	}
	return fixtures
}

func currentGeneratorSourceFixtureRefs(t *testing.T, fn *ast.FnDecl) []string {
	t.Helper()
	expr := blockFinalExpr(fn.Body)
	list, ok := expr.(*ast.ListExpr)
	if !ok {
		t.Fatalf("%s: expected fixture list, got %T", fn.Name, expr)
	}
	refs := make([]string, 0, len(list.Elems))
	for _, elem := range list.Elems {
		call, ok := elem.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			t.Fatalf("%s: expected zero-arg fixture call, got %T", fn.Name, elem)
		}
		ref := exprName(call.Fn)
		if ref == "" {
			t.Fatalf("%s: expected named fixture call, got %T", fn.Name, call.Fn)
		}
		refs = append(refs, ref)
	}
	return refs
}

func parseLIRProtoSourceFixtureDecl(t *testing.T, fn *ast.FnDecl) (lirProtoShadowFixture, bool) {
	t.Helper()
	if fn.Body == nil || len(fn.Body.Stmts) == 0 {
		return lirProtoShadowFixture{}, false
	}
	expr := blockFinalExpr(fn.Body)
	call, ok := expr.(*ast.CallExpr)
	if !ok || exprName(call.Fn) != "lirParityFixture" || len(call.Args) != 7 {
		return lirProtoShadowFixture{}, false
	}
	kind := exprName(call.Args[1].Value)
	if kind != "LirParitySource" {
		return lirProtoShadowFixture{}, false
	}
	name := stringArg(t, fn.Name, call.Args[0].Value)
	sourcePath := stringArg(t, fn.Name, call.Args[2].Value)
	source := stringArg(t, fn.Name, call.Args[3].Value)
	tags := stringListArg(t, fn.Name, call.Args[5].Value)
	needles := needleListArg(t, fn.Name, call.Args[6].Value)
	if name == "" || source == "" || len(needles) == 0 {
		t.Fatalf("%s has incomplete source fixture metadata", fn.Name)
	}
	return lirProtoShadowFixture{Name: name, SourcePath: sourcePath, Source: source, Tags: tags, Needles: needles}, true
}

func blockFinalExpr(block *ast.Block) ast.Expr {
	if block == nil || len(block.Stmts) == 0 {
		return nil
	}
	last := block.Stmts[len(block.Stmts)-1]
	switch stmt := last.(type) {
	case *ast.ExprStmt:
		return stmt.X
	case *ast.ReturnStmt:
		return stmt.Value
	default:
		return nil
	}
}

func exprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.FieldExpr:
		base := exprName(e.X)
		if base == "" {
			return e.Name
		}
		return base + "." + e.Name
	default:
		return ""
	}
}

func stringArg(t *testing.T, owner string, expr ast.Expr) string {
	t.Helper()
	lit, ok := expr.(*ast.StringLit)
	if !ok || len(lit.Parts) != 1 || !lit.Parts[0].IsLit {
		t.Fatalf("%s: expected plain string literal, got %T", owner, expr)
	}
	return lit.Parts[0].Lit
}

func stringListArg(t *testing.T, owner string, expr ast.Expr) []string {
	t.Helper()
	list, ok := expr.(*ast.ListExpr)
	if !ok {
		t.Fatalf("%s: expected string list, got %T", owner, expr)
	}
	out := make([]string, 0, len(list.Elems))
	for _, elem := range list.Elems {
		out = append(out, stringArg(t, owner, elem))
	}
	return out
}

func needleListArg(t *testing.T, owner string, expr ast.Expr) []string {
	t.Helper()
	list, ok := expr.(*ast.ListExpr)
	if !ok {
		t.Fatalf("%s: expected needle list, got %T", owner, expr)
	}
	out := make([]string, 0, len(list.Elems))
	for _, elem := range list.Elems {
		call, ok := elem.(*ast.CallExpr)
		if !ok || exprName(call.Fn) != "lirParityNeedle" || len(call.Args) < 1 {
			t.Fatalf("%s: expected lirParityNeedle call, got %T", owner, elem)
		}
		out = append(out, stringArg(t, owner, call.Args[0].Value))
	}
	return out
}

func (f lirProtoShadowFixture) hasTag(tag string) bool {
	for _, existing := range f.Tags {
		if existing == tag {
			return true
		}
	}
	return false
}

func lowerLIRProtoShadowSourceToMIR(t *testing.T, src string) *mir.Module {
	t.Helper()
	source := []byte(src)
	file, parseDiags := parser.ParseDiagnostics(source)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics = %v", parseDiags)
	}
	if file == nil {
		t.Fatal("parser returned nil file")
	}
	reg := stdlib.LoadCached()
	res := resolve.ResolveFileSourceDefault(source, file, reg)
	if len(res.Diags) != 0 {
		t.Fatalf("resolve diagnostics = %v", res.Diags)
	}
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        source,
	})
	if len(chk.Diags) != 0 {
		t.Fatalf("check diagnostics = %v", chk.Diags)
	}
	hirMod, issues := ostyir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues = %v", issues)
	}
	monoMod, monoErrs := ostyir.Monomorphize(hirMod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize errors = %v", monoErrs)
	}
	if errs := ostyir.Validate(monoMod); len(errs) != 0 {
		t.Fatalf("ir.Validate errors = %v", errs)
	}
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatal("mir.Lower returned nil")
	}
	if errs := mir.Validate(mirMod); len(errs) != 0 {
		t.Fatalf("mir.Validate errors = %v", errs)
	}
	return mirMod
}
