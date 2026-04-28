package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateStructLitSpreadFillsMissingFields(t *testing.T) {
	file := parseLLVMGenFile(t, `struct TomlValue {
    kind: Int
    text: String
    line: Int
}

fn stamp(v: TomlValue, startLine: Int) -> TomlValue {
    TomlValue { ..v, line: startLine }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/struct_lit_spread.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define %TomlValue @stamp(%TomlValue %v, i64 %startLine)",
		"extractvalue %TomlValue %v, 0",
		"extractvalue %TomlValue %v, 1",
		"insertvalue %TomlValue",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "spread literal") {
		t.Fatalf("struct spread regressed to unsupported diagnostic:\n%s", got)
	}
}

func TestGenerateStructLitUpdateShorthandUsesReceiverAsSpread(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Point {
    x: Int
    y: Int
}

fn shift(p: Point, dx: Int) -> Point {
    p { x: p.x + dx }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/struct_lit_update_shorthand.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define %Point @shift(%Point %p, i64 %dx)",
		"extractvalue %Point %p, 1",
		"insertvalue %Point",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
