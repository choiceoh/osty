package selfhost_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/stdlib"
)

func TestPackageImportSurfaceRebindsStdOptionResultBuiltinTypes(t *testing.T) {
	reg := stdlib.LoadCached()
	optionRun := selfhost.Run(reg.Modules["option"].Source)
	resultRun := selfhost.Run(reg.Modules["result"].Source)
	if optionRun == nil || resultRun == nil {
		t.Fatal("expected selfhost runs for std.option/std.result")
	}

	optionSurface := selfhost.PackageImportSurface("std.option", "option", []*selfhost.FrontendRun{optionRun})
	resultSurface := selfhost.PackageImportSurface("std.result", "result", []*selfhost.FrontendRun{resultRun})

	assertNoQualifiedBuiltin := func(t *testing.T, surface selfhost.PackageCheckImport, bad string) {
		t.Helper()
		for _, fn := range surface.Functions {
			if containsQualifiedType(fn.ReturnType, bad) {
				t.Fatalf("fn %s return type leaked qualified builtin %q: %s", fn.Name, bad, fn.ReturnType)
			}
			for _, param := range fn.ParamTypes {
				if containsQualifiedType(param, bad) {
					t.Fatalf("fn %s param type leaked qualified builtin %q: %s", fn.Name, bad, param)
				}
			}
		}
		for _, field := range surface.Fields {
			if containsQualifiedType(field.TypeName, bad) {
				t.Fatalf("field %s leaked qualified builtin %q: %s", field.Name, bad, field.TypeName)
			}
		}
	}

	assertNoQualifiedBuiltin(t, optionSurface, "option.Option")
	assertNoQualifiedBuiltin(t, resultSurface, "result.Result")
}

func TestPackageImportSurfaceEmitsStructuredTypeReprs(t *testing.T) {
	run := selfhost.Run([]byte(`pub struct Box<T> {
    pub value: T
}

pub fn make(value: Int) -> Box<Int> {
    Box { value }
}
`))
	if run == nil {
		t.Fatal("Run returned nil")
	}
	surface := selfhost.PackageImportSurface("dep", "dep", []*selfhost.FrontendRun{run})

	var makeFn *selfhost.PackageCheckFn
	for i := range surface.Functions {
		if surface.Functions[i].Name == "make" && surface.Functions[i].Owner == "" {
			makeFn = &surface.Functions[i]
			break
		}
	}
	if makeFn == nil {
		t.Fatalf("missing make function in surface: %#v", surface.Functions)
	}
	if makeFn.ReturnTypeRepr == nil || makeFn.ReturnTypeRepr.String() != "dep.Box<Int>" {
		t.Fatalf("make return TypeRepr = %#v, want dep.Box<Int>", makeFn.ReturnTypeRepr)
	}
	if len(makeFn.ParamTypeReprs) != 1 || makeFn.ParamTypeReprs[0].String() != "Int" {
		t.Fatalf("make param TypeReprs = %#v, want [Int]", makeFn.ParamTypeReprs)
	}

	var valueField *selfhost.PackageCheckField
	for i := range surface.Fields {
		if surface.Fields[i].Owner == "dep.Box" && surface.Fields[i].Name == "value" {
			valueField = &surface.Fields[i]
			break
		}
	}
	if valueField == nil {
		t.Fatalf("missing dep.Box.value field in surface: %#v", surface.Fields)
	}
	if valueField.Type == nil || valueField.Type.Kind != "typevar" || valueField.Type.Name != "T" {
		t.Fatalf("value field TypeRepr = %#v, want typevar T", valueField.Type)
	}
}

func containsQualifiedType(text, qualified string) bool {
	if text == qualified {
		return true
	}
	for _, suffix := range []string{"<", "?", ",", ")", ">", " "} {
		if strings.Contains(text, qualified+suffix) {
			return true
		}
	}
	return false
}
