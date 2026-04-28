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
