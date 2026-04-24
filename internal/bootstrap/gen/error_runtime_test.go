package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestGenerateConcreteErrorHelpers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	src := `enum FsError {
    NotFound(String)

    fn message(self) -> String {
        match self {
            NotFound(path) -> "missing {path}",
        }
    }
}

fn main() {
    let err = FsError.NotFound("settings.osty")
    let _parent: Error? = err.source()
    let _wrapped: Error = err.wrap("load")
    let _chain: List<Error> = err.chain()
    let _exact: FsError? = err.downcast::<FsError>()
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := bootstrapGeneratedSource(t, path)
	for _, want := range []string{
		"ostyErrorSource(err)",
		"ostyErrorWrap(err, \"load\")",
		"ostyErrorChain(err)",
		"ostyErrorDowncast[FsError](err)",
		"type ostyWrappedError struct",
		"func ostyErrorWrap(err any, context string) any",
		"func ostyErrorChain(err any) []any",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated Go missing %q\n%s", want, out)
		}
	}
}

func bootstrapGeneratedSource(t *testing.T, path string) string {
	t.Helper()

	absPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	reg := stdlib.LoadCached()
	opts := check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
	}

	ws, err := resolve.NewWorkspace(filepath.Dir(absPath))
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	ws.Stdlib = reg
	if _, err := ws.LoadPackageArenaFirst(""); err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	results := ws.ResolveAll()
	pkg := ws.Packages[""]
	if pkg == nil {
		t.Fatal("root package is missing")
	}
	rootRes := results[""]
	if rootRes == nil {
		t.Fatal("root resolve result is missing")
	}
	checks := check.Workspace(ws, results, opts)
	chk := checks[""]
	if chk == nil {
		t.Fatal("root check result is missing")
	}

	var entryFile *resolve.PackageFile
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		fp, err := filepath.Abs(pf.Path)
		if err == nil && fp == absPath {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		t.Fatalf("entry file %s not found in package", absPath)
	}

	fileRes := &resolve.Result{
		FileScope: entryFile.FileScope,
		Diags:     rootRes.Diags,
	}
	out, err := GenerateMapped("main", entryFile.File, fileRes, chk, absPath)
	if err != nil {
		t.Fatalf("GenerateMapped: %v\n%s", err, out)
	}
	return string(out)
}
