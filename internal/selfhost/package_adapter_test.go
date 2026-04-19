package selfhost_test

import (
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

func findCheckedBindingType(result selfhost.CheckResult, name string) string {
	for _, binding := range result.Bindings {
		if binding.Name == name {
			return binding.TypeName
		}
	}
	return ""
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
	out, sm := canonical.SourceWithMap(src, file)
	return selfhost.PackageCheckFile{
		Source:    out,
		File:      file,
		SourceMap: sm,
		Base:      base,
	}
}
