package resolve

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/osty/osty/internal/parser"
)

type graphTestStdlib struct {
	pkgs map[string]*Package
}

func (s graphTestStdlib) LookupPackage(dotPath string) *Package {
	return s.pkgs[dotPath]
}

type graphTestDeps map[string]string

func (d graphTestDeps) LookupDep(rawPath string) (string, bool) {
	dir, ok := d[rawPath]
	return dir, ok
}

func TestPackageGraphCapturesCompileTargetState(t *testing.T) {
	root := t.TempDir()
	depDir := filepath.Join(root, "dep")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	extDir := filepath.Join(t.TempDir(), "extpkg")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.osty"), []byte(`use dep
use std.fs
use extpkg

fn main() {
    dep.value()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "lib.osty"), []byte(`pub fn value() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "lib.osty"), []byte(`pub fn ext() -> Int { 2 }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	ws.Stdlib = graphTestStdlib{pkgs: map[string]*Package{
		"std.fs": {Name: "fs"},
	}}
	ws.Deps = graphTestDeps{"extpkg": extDir}
	ws.SetCfgEnv(&CfgEnv{
		OS:       "darwin",
		Arch:     "arm64",
		Target:   "darwin",
		Features: map[string]bool{"net": true, "ssl": false},
	})
	ws.SourceTransform = func(path string, src []byte) []byte {
		if filepath.Base(path) == "main.osty" {
			return append(append([]byte(nil), src...), []byte("\n// transformed\n")...)
		}
		return src
	}
	if _, err := ws.LoadPackageNative(""); err != nil {
		t.Fatalf("LoadPackageNative: %v", err)
	}

	graph := NewPackageGraph(ws)
	if graph.Root == "" {
		t.Fatal("PackageGraph.Root is empty")
	}
	if got, want := graph.PackagePaths(), []string{"dep", "extpkg", "std.fs", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PackagePaths() = %#v, want %#v", got, want)
	}
	if graph.Cfg == nil || graph.Cfg.OS != "darwin" || graph.Cfg.Arch != "arm64" || graph.Cfg.Target != "darwin" {
		t.Fatalf("graph cfg = %#v, want darwin/arm64/darwin", graph.Cfg)
	}
	if got, want := graph.Features, []string{"net"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("graph features = %#v, want %#v", got, want)
	}
	changedTransforms := 0
	for _, tr := range graph.SourceTransforms {
		if !tr.Applied {
			t.Fatalf("transform record was not marked applied: %#v", tr)
		}
		if tr.Changed {
			changedTransforms++
			if filepath.Base(tr.FilePath) != "main.osty" || tr.OutputBytes <= tr.InputBytes {
				t.Fatalf("unexpected changed source transform record: %#v", tr)
			}
		}
	}
	if changedTransforms != 1 {
		t.Fatalf("changed source transforms = %d, want 1", changedTransforms)
	}

	kinds := map[string]PackageGraphEdgeKind{}
	for _, edge := range graph.Edges {
		kinds[edge.To] = edge.Kind
		if !edge.Resolved {
			t.Fatalf("edge to %q was not marked resolved: %#v", edge.To, edge)
		}
	}
	if got, want := kinds["dep"], PackageGraphEdgeWorkspace; got != want {
		t.Fatalf("dep edge kind = %q, want %q", got, want)
	}
	if got, want := kinds["std.fs"], PackageGraphEdgeStdlib; got != want {
		t.Fatalf("std.fs edge kind = %q, want %q", got, want)
	}
	if got, want := kinds["extpkg"], PackageGraphEdgeExternal; got != want {
		t.Fatalf("extpkg edge kind = %q, want %q", got, want)
	}
	if len(graph.StdlibEdges) != 1 {
		t.Fatalf("stdlib edges = %d, want 1", len(graph.StdlibEdges))
	}
	if len(graph.ExternalDepEdges) != 1 {
		t.Fatalf("external dep edges = %d, want 1", len(graph.ExternalDepEdges))
	}
	if pkg := graph.Package("extpkg"); pkg == nil || !pkg.isExternalDep {
		t.Fatalf("external package marker missing: %#v", pkg)
	}
}

func TestSingleFilePackageGraphCapturesStdlibImport(t *testing.T) {
	src := []byte(`use std.fs

fn main() {
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	graph := NewSingleFilePackageGraph(src, file, graphTestStdlib{pkgs: map[string]*Package{
		"std.fs": {Name: "fs"},
	}})
	if got, want := graph.PackagePaths(), []string{""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PackagePaths() = %#v, want %#v", got, want)
	}
	if len(graph.Imports) != 1 {
		t.Fatalf("imports = %d, want 1", len(graph.Imports))
	}
	imp := graph.Imports[0]
	if imp.TargetPath != "std.fs" || imp.Kind != PackageGraphEdgeStdlib || !imp.Resolved {
		t.Fatalf("unexpected import graph record: %#v", imp)
	}
}
