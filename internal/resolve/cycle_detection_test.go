package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// End-to-end coverage for the Osty-ported DFS in
// toolchain/resolve.osty::selfDetectImportCycles. Sets up a two-package
// workspace where each package imports the other, runs ResolveAll, and
// asserts that E0506 fires with the cycle-closing use-site span.
func TestWorkspaceDetectsCyclicImportTwoPackage(t *testing.T) {
	root := t.TempDir()
	pkgA := filepath.Join(root, "alpha")
	pkgB := filepath.Join(root, "beta")
	if err := os.MkdirAll(pkgA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0o755); err != nil {
		t.Fatal(err)
	}
	aFile := filepath.Join(pkgA, "lib.osty")
	bFile := filepath.Join(pkgB, "lib.osty")
	if err := os.WriteFile(aFile, []byte(`use beta

pub fn hello() -> Int { 0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bFile, []byte(`use alpha

pub fn world() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	for _, p := range WorkspacePackagePaths(root) {
		if _, err := ws.LoadPackageArenaFirst(p); err != nil {
			t.Fatalf("LoadPackage %s: %v", p, err)
		}
	}
	results := ws.ResolveAll()

	var cycleDiag *diag.Diagnostic
	for _, pr := range results {
		if pr == nil {
			continue
		}
		for _, d := range pr.Diags {
			if d.Code == diag.CodeCyclicImport {
				cycleDiag = d
				break
			}
		}
		if cycleDiag != nil {
			break
		}
	}
	if cycleDiag == nil {
		t.Fatalf("expected E0506 for alpha↔beta cycle")
	}
	if !strings.Contains(cycleDiag.Message, "cyclic import") {
		t.Errorf("expected cyclic-import wording, got %q", cycleDiag.Message)
	}
	// The diag should anchor on a use-site — Span must be non-zero.
	if len(cycleDiag.Spans) == 0 || cycleDiag.Spans[0].Span.Start.Line == 0 {
		t.Errorf("expected diag span to carry source position, got %#v", cycleDiag.Spans)
	}
}

// Three-package cycle: alpha → beta → gamma → alpha. Exactly one
// back-edge should emit E0506 (the gamma → alpha edge closes the cycle
// during DFS starting from alpha).
func TestWorkspaceDetectsCyclicImportThreePackage(t *testing.T) {
	root := t.TempDir()
	pkgs := map[string]string{
		"alpha": "use beta\n\npub fn a() -> Int { 0 }\n",
		"beta":  "use gamma\n\npub fn b() -> Int { 0 }\n",
		"gamma": "use alpha\n\npub fn c() -> Int { 0 }\n",
	}
	for name, src := range pkgs {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "lib.osty"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	for _, p := range WorkspacePackagePaths(root) {
		if _, err := ws.LoadPackageArenaFirst(p); err != nil {
			t.Fatalf("LoadPackage %s: %v", p, err)
		}
	}
	results := ws.ResolveAll()

	cycleCount := 0
	for _, pr := range results {
		if pr == nil {
			continue
		}
		for _, d := range pr.Diags {
			if d.Code == diag.CodeCyclicImport {
				cycleCount++
			}
		}
	}
	// Depending on workspace DFS ordering, the in-loop check can also
	// fire a CodeCyclicImport from cycleMarker during LoadPackage. The
	// port only affects detectCycles (post-load DFS), which emits
	// exactly one back-edge per cycle.
	if cycleCount == 0 {
		t.Fatalf("expected E0506 for 3-package cycle, got 0")
	}
}

// No-cycle baseline: the DAG alpha → beta → gamma should resolve
// without any CodeCyclicImport diag.
func TestWorkspaceNoCycleOnDAG(t *testing.T) {
	root := t.TempDir()
	pkgs := map[string]string{
		"alpha": "use beta\n\npub fn a() -> Int { 0 }\n",
		"beta":  "use gamma\n\npub fn b() -> Int { 0 }\n",
		"gamma": "pub fn c() -> Int { 0 }\n",
	}
	for name, src := range pkgs {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "lib.osty"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	for _, p := range WorkspacePackagePaths(root) {
		if _, err := ws.LoadPackageArenaFirst(p); err != nil {
			t.Fatalf("LoadPackage %s: %v", p, err)
		}
	}
	results := ws.ResolveAll()

	for _, pr := range results {
		if pr == nil {
			continue
		}
		for _, d := range pr.Diags {
			if d.Code == diag.CodeCyclicImport {
				t.Fatalf("DAG workspace should emit no E0506, got %q at %#v", d.Message, d.Spans)
			}
		}
	}
}
