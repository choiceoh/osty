package llvmgen

import (
	"strings"
	"testing"
)

// TestGenerateResultQuestionExprStruct pins the Tier B pkgmgr Result
// ABI shape for struct payloads. semver_parse.osty returns
// `Result<SemVersion, String>` and chains `?` from `parseSemCore(...)`
// into the enclosing function. This test exercises the same shape
// with a minimal struct payload — the `?` lowering must extract the
// struct from the Ok slot, repackage the err field through
// `zeroinitializer` / `%Struct zeroinitializer`, and continue on the
// Ok branch with the struct value.
func TestGenerateResultQuestionExprStruct(t *testing.T) {
	file := parseLLVMGenFile(t, `pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn makePoint(x: Int, y: Int) -> Result<Point, String> {
    if x < 0 {
        Err("negative x")
    } else {
        Ok(Point { x, y })
    }
}

fn shift(x: Int, y: Int, dx: Int) -> Result<Point, String> {
    let p = makePoint(x, y)?
    Ok(Point { x: p.x + dx, y: p.y })
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/result_struct.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Result._Point.ptr = type { i64, %Point, ptr }",
		"define %Result._Point.ptr @shift(",
		"result.err",
		"result.ok",
		// Err repackage: tag=1, ok=zeroed struct, err=forwarded
		"%Point zeroinitializer, 1",
		// Ok extraction: extract the Point value
		"extractvalue %Result._Point.ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateResultQuestionExprEnumPayload pins the composite enum
// payload variant of the Tier B Result ABI. semver_parse.osty's
// `parseSemPreIdent` returns `Result<SemPreIdent, String>` where
// `SemPreIdent` has `PreNone` / `PreNumber(Int)` / `PreText(String)`
// variants. `?` on that Result must thread the (tag+ptr-payload)
// enum struct through the Result struct and back.
func TestGenerateResultQuestionExprEnumPayload(t *testing.T) {
	file := parseLLVMGenFile(t, `pub enum Pre {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

fn parsePre(text: String) -> Result<Pre, String> {
    if text == "" {
        Err("empty")
    } else if text == "0" {
        Ok(PreNumber(0))
    } else {
        Ok(PreText(text))
    }
}

fn wrap(text: String) -> Result<Pre, String> {
    let p = parsePre(text)?
    Ok(p)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/result_enum.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define %Result._Pre.ptr @wrap(",
		"result.err",
		"result.ok",
		// Err path repackages with zero enum payload + forwarded err.
		"%Pre zeroinitializer, 1",
		// Ok path extracts the enum value and wraps it back.
		"extractvalue %Result._Pre.ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
