package selfhost_test

import (
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

func TestCheckPackageStructuredSingleFileKeepsLetBindings(t *testing.T) {
	src := canonicalSelfhostSource(t, []byte(`fn main() {
    let item = 1
    let value = item
}
`))
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{{Source: src, Base: 0}},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails)
	}
	if got := findCheckedBindingType(checked, "item"); got != "UntypedInt" {
		t.Fatalf("binding type for item = %q, want UntypedInt", got)
	}
	if got := findCheckedBindingType(checked, "value"); got != "UntypedInt" {
		t.Fatalf("binding type for value = %q, want UntypedInt", got)
	}
}

func TestCheckPackageStructuredSingleFileKeepsLetBindingsASTNative(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`fn main() {
    let item = 1
    let value = item
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails)
	}
	if got := findCheckedBindingType(checked, "item"); got != "UntypedInt" {
		t.Fatalf("binding type for item = %q, want UntypedInt", got)
	}
	if got := findCheckedBindingType(checked, "value"); got != "UntypedInt" {
		t.Fatalf("binding type for value = %q, want UntypedInt", got)
	}
}

func TestCheckPackageStructuredHandlesCrossFileImports(t *testing.T) {
	fileA := canonicalSelfhostSource(t, []byte(`use dep

fn helper() -> dep.Item {
    dep.make()
}
`))
	fileB := canonicalSelfhostSource(t, []byte(`fn main() {
    let item = helper()
    let value = item.value
}
`))
	input := selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{
			{Source: fileA, Base: 0},
			{Source: fileB, Base: len(fileA) + 1},
		},
		Imports: []selfhost.PackageCheckImport{{
			Alias: "dep",
			Functions: []selfhost.PackageCheckFn{{
				Name:       "make",
				Owner:      "dep",
				ReturnType: "dep.Item",
			}},
			Fields: []selfhost.PackageCheckField{
				{Owner: "dep", Name: "Item", TypeName: "dep.Item", HasDefault: true},
				{Owner: "dep.Item", Name: "value", TypeName: "Int"},
			},
			TypeDecls: []selfhost.PackageCheckType{{
				Name: "dep.Item",
				Kind: "struct",
			}},
		}},
	}

	checked, err := selfhost.CheckPackageStructured(input)
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails)
	}
	if got := findCheckedBindingType(checked, "item"); got != "dep.Item" {
		t.Fatalf("binding type for item = %q, want dep.Item", got)
	}
	if got := findCheckedBindingType(checked, "value"); got != "Int" {
		t.Fatalf("binding type for value = %q, want Int", got)
	}
}

func TestCheckPackageStructuredHandlesCrossFileImportsASTNative(t *testing.T) {
	fileA := canonicalSelfhostInput(t, []byte(`use dep

fn helper() -> dep.Item {
    dep.make()
}
`), 0)
	fileB := canonicalSelfhostInput(t, []byte(`fn main() {
    let item = helper()
    let value = item.value
}
`), len(fileA.Source)+1)
	input := selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{fileA, fileB},
		Imports: []selfhost.PackageCheckImport{{
			Alias: "dep",
			Functions: []selfhost.PackageCheckFn{{
				Name:       "make",
				Owner:      "dep",
				ReturnType: "dep.Item",
			}},
			Fields: []selfhost.PackageCheckField{
				{Owner: "dep", Name: "Item", TypeName: "dep.Item", HasDefault: true},
				{Owner: "dep.Item", Name: "value", TypeName: "Int"},
			},
			TypeDecls: []selfhost.PackageCheckType{{
				Name: "dep.Item",
				Kind: "struct",
			}},
		}},
	}

	checked, err := selfhost.CheckPackageStructured(input)
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails)
	}
	if got := findCheckedBindingType(checked, "item"); got != "dep.Item" {
		t.Fatalf("binding type for item = %q, want dep.Item", got)
	}
	if got := findCheckedBindingType(checked, "value"); got != "Int" {
		t.Fatalf("binding type for value = %q, want Int", got)
	}
}

func TestCheckPackageStructuredHandlesUseBodyASTNative(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`use go "dep" as dep {
    struct Item {
        value: Int
    }

    fn make() -> dep.Item
}

fn main() {
    let item = dep.make()
    let value = item.value
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails)
	}
	if got := findCheckedBindingType(checked, "item"); got != "dep.Item" {
		t.Fatalf("binding type for item = %q, want dep.Item", got)
	}
	if got := findCheckedBindingType(checked, "value"); got != "Int" {
		t.Fatalf("binding type for value = %q, want Int", got)
	}
}

func TestCheckSourceStructuredReportsIntrinsicBodyViolation(t *testing.T) {
	checked := selfhost.CheckSourceStructured([]byte(`#[intrinsic]
pub fn violator() -> Int { 42 }
`))
	if got := findDiagnosticCode(checked, "E0773"); got == nil {
		t.Fatalf("expected E0773, got diagnostics %#v", checked.Diagnostics)
	}
	if got := checked.Summary.ErrorsByContext["E0773"]; got != 1 {
		t.Fatalf("summary E0773 count = %d, want 1 (summary=%#v)", got, checked.Summary)
	}
}

func TestCheckSourceStructuredClosurePatternParam(t *testing.T) {
	src := canonicalSelfhostSource(t, []byte(`fn main() {
    let f: fn((Int, Int)) -> Int = |(a, b): (Int, Int)| a + b
    let total = f((1, 2))
}
`))
	checked := selfhost.CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if got := findCheckedBindingType(checked, "a"); got != "Int" {
		t.Fatalf("binding type for a = %q, want Int", got)
	}
	if got := findCheckedBindingType(checked, "b"); got != "Int" {
		t.Fatalf("binding type for b = %q, want Int", got)
	}
	if got := findCheckedBindingType(checked, "total"); got != "Int" {
		t.Fatalf("binding type for total = %q, want Int", got)
	}
}

func TestCheckPackageStructuredClosurePatternParamASTNative(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`fn main() {
    let f: fn((Int, Int)) -> Int = |(a, b): (Int, Int)| a + b
    let total = f((1, 2))
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if got := findCheckedBindingType(checked, "a"); got != "Int" {
		t.Fatalf("binding type for a = %q, want Int", got)
	}
	if got := findCheckedBindingType(checked, "b"); got != "Int" {
		t.Fatalf("binding type for b = %q, want Int", got)
	}
	if got := findCheckedBindingType(checked, "total"); got != "Int" {
		t.Fatalf("binding type for total = %q, want Int", got)
	}
}

func TestCheckPackageStructuredScriptTopLevelLetsASTNative(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`let seed = 1
let total = seed
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if got := findCheckedBindingType(checked, "seed"); got != "UntypedInt" {
		t.Fatalf("binding type for seed = %q, want UntypedInt", got)
	}
	if got := findCheckedBindingType(checked, "total"); got != "UntypedInt" {
		t.Fatalf("binding type for total = %q, want UntypedInt", got)
	}
}

func TestCheckPackageStructuredReportsIntrinsicBodyViolationWithPath(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.osty")
	badPath := filepath.Join(dir, "bad.osty")
	good := canonicalSelfhostSource(t, []byte(`pub fn ok() -> Int { 0 }
`))
	bad := canonicalSelfhostSource(t, []byte(`#[intrinsic]
pub fn violator() -> Int { 42 }
`))
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{
			{Source: good, Base: 0, Name: "good.osty", Path: goodPath},
			{Source: bad, Base: len(good) + 1, Name: "bad.osty", Path: badPath},
		},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	got := findDiagnosticCode(checked, "E0773")
	if got == nil {
		t.Fatalf("expected E0773, got diagnostics %#v", checked.Diagnostics)
	}
	if got.File != badPath {
		t.Fatalf("diagnostic file = %q, want %q", got.File, badPath)
	}
	if want := len(good) + 1; got.Start < want {
		t.Fatalf("diagnostic start = %d, want >= %d so second-file base survives", got.Start, want)
	}
	if got := checked.Summary.ErrorsByContext["E0773"]; got != 1 {
		t.Fatalf("summary E0773 count = %d, want 1 (summary=%#v)", got, checked.Summary)
	}
}

func TestCheckPackageStructuredASTNativeRunsNoAllocGate(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`#[no_alloc]
fn main() {
    let items = [1]
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if got := findDiagnosticCode(checked, "E0772"); got == nil {
		t.Fatalf("expected E0772, got diagnostics %#v", checked.Diagnostics)
	}
	if got := checked.Summary.ErrorsByContext["E0772"]; got != 1 {
		t.Fatalf("summary E0772 count = %d, want 1 (summary=%#v)", got, checked.Summary)
	}
}

func TestCheckPackageStructuredImportedDefaultMethodBoundsASTNative(t *testing.T) {
	input := canonicalSelfhostInput(t, []byte(`use dep

struct User {
    name: String

    fn name(self) -> String {
        self.name
    }
}

fn display<T: dep.Named>(value: T) -> String {
    value.label()
}

fn main() {
    let label = display(User { name: "Ada" })
}
`), 0)
	checked, err := selfhost.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{input},
		Imports: []selfhost.PackageCheckImport{{
			Alias: "dep",
			TypeDecls: []selfhost.PackageCheckType{{
				Name: "dep.Named",
				Kind: "interface",
			}},
			RegisterAsIface: []string{"dep.Named"},
			Functions: []selfhost.PackageCheckFn{
				{
					Name:         "name",
					Owner:        "dep.Named",
					ReceiverType: "dep.Named",
					ReturnType:   "String",
				},
				{
					Name:         "label",
					Owner:        "dep.Named",
					ReceiverType: "dep.Named",
					ReturnType:   "String",
					HasBody:      true,
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if got := findCheckedBindingType(checked, "label"); got != "String" {
		t.Fatalf("binding type for label = %q, want String", got)
	}
}

func findCheckedBindingType(result selfhost.CheckResult, name string) string {
	for _, binding := range result.Bindings {
		if binding.Name == name {
			return binding.Type.String()
		}
	}
	return ""
}

func findDiagnosticCode(result selfhost.CheckResult, code string) *selfhost.CheckDiagnosticRecord {
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Code == code {
			return &result.Diagnostics[i]
		}
	}
	return nil
}

func canonicalSelfhostSource(t *testing.T, src []byte) []byte {
	t.Helper()
	file, errs := parser.Parse(src)
	if len(errs) != 0 {
		t.Fatalf("Parse(%q): %v", string(src), errs)
	}
	out, _ := canonical.SourceWithMap(src, file)
	return out
}

func canonicalSelfhostInput(t *testing.T, src []byte, base int) selfhost.PackageCheckFile {
	t.Helper()
	file, errs := parser.Parse(src)
	if len(errs) != 0 {
		t.Fatalf("Parse(%q): %v", string(src), errs)
	}
	out, _ := canonical.SourceWithMap(src, file)
	return selfhost.PackageCheckFile{
		Source: out,
		Base:   base,
	}
}
