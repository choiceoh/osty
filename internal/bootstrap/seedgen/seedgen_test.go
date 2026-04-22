package seedgen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/stdlib"
)

func TestNormalizeGeneratedOutputRewritesInterpolatedLenCalls(t *testing.T) {
	src := []byte(`monoAddErr(state, fmt.Sprintf("arity %s %s %s", ostyToString(typeArgs.len()), ostyToString(fnDecl.generics.len()), "Ok(s.len())"))`)
	got := string(normalizeGeneratedOutput(src))
	if want := `ostyToString(len(typeArgs))`; !containsText(got, want) {
		t.Fatalf("normalizeGeneratedOutput() missing %q in %q", want, got)
	}
	if want := `ostyToString(len(fnDecl.generics))`; !containsText(got, want) {
		t.Fatalf("normalizeGeneratedOutput() missing %q in %q", want, got)
	}
	if want := `"Ok(s.len())"`; !containsText(got, want) {
		t.Fatalf("normalizeGeneratedOutput() rewrote string literal unexpectedly: %q", got)
	}
}

func containsText(haystack, needle string) bool {
	return len(needle) != 0 && len(haystack) >= len(needle) && (haystack == needle || containsTextAt(haystack, needle))
}

func containsTextAt(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestMaterializeCanonicalWorkspaceSourcesUsesNativeLoaderCompatibilityPath(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "main.osty")
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    println("{y}")
}
`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	ws, err := resolve.NewWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	ws.Stdlib = stdlib.LoadCached()

	selfhost.ResetAstbridgeLowerCount()
	if _, err := ws.LoadPackageNative(""); err != nil {
		t.Fatalf("LoadPackageNative: %v", err)
	}
	materializeCanonicalWorkspaceSources(ws)
	results := ws.ResolveAll()
	pkg := ws.Packages[""]
	if pkg == nil || len(pkg.Files) != 1 {
		t.Fatalf("root package files = %#v, want exactly one source file", pkg)
	}
	pf := pkg.Files[0]
	if pf == nil {
		t.Fatal("root PackageFile is nil")
	}
	if pf.Run == nil {
		t.Fatal("LoadPackageNative did not retain FrontendRun on the PackageFile")
	}
	if pf.File == nil {
		t.Fatal("native compatibility path did not materialize a public AST")
	}
	if len(pf.CanonicalSource) == 0 {
		t.Fatal("CanonicalSource is empty after materializeCanonicalWorkspaceSources")
	}
	if pf.CanonicalMap == nil {
		t.Fatal("CanonicalMap is nil after materializeCanonicalWorkspaceSources")
	}
	if root := results[""]; root == nil {
		t.Fatal("ResolveAll missing root package result")
	}
}
