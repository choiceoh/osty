package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

func TestCsvModuleSurface(t *testing.T) {
	reg := Load()
	mod := reg.Modules["csv"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.csv not loaded")
	}

	for _, name := range []string{"encode", "encodeWith", "decode", "decodeWith", "decodeHeaders"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.csv missing export %q", name)
			continue
		}
		if sym.Kind != resolve.SymFn {
			t.Errorf("std.csv.%s kind = %s, want SymFn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Errorf("std.csv.%s not public", name)
		}
		fn, ok := sym.Decl.(*ast.FnDecl)
		if !ok || fn.Body == nil {
			t.Errorf("std.csv.%s has no bodied implementation", name)
		}
	}

	sym := mod.Package.PkgScope.LookupLocal("CsvOptions")
	if sym == nil {
		t.Fatalf("std.csv missing CsvOptions")
	}
	if sym.Kind != resolve.SymStruct {
		t.Fatalf("std.csv.CsvOptions kind = %s, want SymStruct", sym.Kind)
	}
	opts, ok := sym.Decl.(*ast.StructDecl)
	if !ok {
		t.Fatalf("std.csv.CsvOptions decl = %T, want *ast.StructDecl", sym.Decl)
	}
	fields := map[string]*ast.Field{}
	for _, f := range opts.Fields {
		fields[f.Name] = f
	}
	for _, name := range []string{"delimiter", "quote", "trimSpace"} {
		f := fields[name]
		if f == nil {
			t.Errorf("CsvOptions missing field %q", name)
			continue
		}
		if !f.Pub {
			t.Errorf("CsvOptions.%s not public", name)
		}
		if f.Default == nil {
			t.Errorf("CsvOptions.%s has no default", name)
		}
	}
}

func TestCsvModuleSourcePinsQualityGuards(t *testing.T) {
	reg := Load()
	mod := reg.Modules["csv"]
	if mod == nil {
		t.Fatalf("std.csv not loaded")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"validateOptions(options)?",
		"abortInvalidOptions(options)",
		"validateHeaders(headers)?",
		"duplicate header",
		"row.len() != headers.len()",
		"record.insert(headers[c], row[c])",
		"options.trimSpace && hasOuterAsciiSpace(field)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("std.csv source missing quality guard %q", want)
		}
	}
	if strings.Contains(src, "record.set(") {
		t.Errorf("std.csv still calls Map.set; use canonical Map.insert")
	}
}
