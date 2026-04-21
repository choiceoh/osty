package llvmgen

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

// TestToolchainHasNoPhantomRangeExpr locks the fix for the "LLVM013
// *ast.RangeExpr at 1:16" wall that TestProbeNativeToolchainMerged
// hit before. Root cause: `llvmUnsupportedDiagnostic`'s hint strings
// carried unescaped `{ ... }` which the Osty lexer treats as
// interpolation. The reparsed interpolation expression (` .. `) came
// back as a RangeExpr with position 1:16 (offset 15) in the local
// reparse buffer, which then surfaced as the backend's first wall.
//
// Regression shape: any top-level toolchain declaration that contains
// a *ast.RangeExpr whose reported line is BEFORE the declaration's
// own first line is necessarily phantom — it came from a reparse
// context, not the source file.
func TestToolchainHasNoPhantomRangeExpr(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !ostyToolchainSource(name) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		file, _ := parser.ParseDiagnostics(src)
		if file == nil {
			continue
		}
		for _, decl := range file.Decls {
			declPos := decl.Pos()
			walkPhantomRange(reflect.ValueOf(decl), func(line, col, off int) {
				if line < declPos.Line {
					t.Errorf("%s: decl %q (line %d) has phantom RangeExpr at %d:%d (offset %d) — likely an unescaped `{ ... }` in a string literal",
						name, declLabel(decl), declPos.Line, line, col, off)
				}
			})
		}
	}
}

func ostyToolchainSource(name string) bool {
	if !hasSuffix(name, ".osty") {
		return false
	}
	if hasSuffix(name, "_test.osty") {
		return false
	}
	return true
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func declLabel(d ast.Decl) string {
	switch x := d.(type) {
	case *ast.FnDecl:
		return fmt.Sprintf("fn %s", x.Name)
	case *ast.StructDecl:
		return fmt.Sprintf("struct %s", x.Name)
	case *ast.EnumDecl:
		return fmt.Sprintf("enum %s", x.Name)
	case *ast.UseDecl:
		return fmt.Sprintf("use %s", x.RawPath)
	case *ast.InterfaceDecl:
		return fmt.Sprintf("interface %s", x.Name)
	case *ast.TypeAliasDecl:
		return fmt.Sprintf("type %s", x.Name)
	}
	return fmt.Sprintf("%T", d)
}

func walkPhantomRange(v reflect.Value, visit func(line, col, off int)) {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		if v.Type().Name() == "RangeExpr" {
			p := v.FieldByName("PosV")
			visit(int(p.FieldByName("Line").Int()),
				int(p.FieldByName("Column").Int()),
				int(p.FieldByName("Offset").Int()))
		}
		for i := 0; i < v.NumField(); i++ {
			walkPhantomRange(v.Field(i), visit)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			walkPhantomRange(v.Index(i), visit)
		}
	}
}
