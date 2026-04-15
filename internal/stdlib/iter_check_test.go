package stdlib

import (
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestIterOstyFullCheck(t *testing.T) {
	reg := Load()
	src, err := stubs.ReadFile("modules/iter.osty")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		t.Logf("parse %s: %s", d.Severity, d.Error())
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	pkg := &resolve.Package{
		Name: "iter",
		Files: []*resolve.PackageFile{{
			Path:       "modules/iter.osty",
			Source:     src,
			File:       file,
			ParseDiags: parseDiags,
		}},
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	for _, d := range res.Diags {
		t.Logf("resolve %s: %s", d.Severity, d.Error())
		if d.Severity == diag.Error {
			t.Fatalf("resolve error: %s", d.Error())
		}
	}
	chk := check.Package(pkg, res, check.Opts{Primitives: reg.Primitives})
	for _, d := range chk.Diags {
		t.Logf("check %s: %s", d.Severity, d.Error())
		if d.Severity == diag.Error {
			t.Fatalf("check error: %s", d.Error())
		}
	}
	t.Log("iter.osty: parse+resolve+check OK")
}
