package ir

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// TestLowerPackageMergesDeclsFromEveryFile is the headline guarantee
// for ir.LowerPackage: a multi-file package with one fn per file
// produces a single Module whose Decls slice is the concatenation of
// each file's top-level fns, in pkg.Files order. This is the contract
// `cmd/osty/build.go` relies on for multi-file native builds.
func TestLowerPackageMergesDeclsFromEveryFile(t *testing.T) {
	fileA := &ast.File{Decls: []ast.Decl{
		&ast.FnDecl{Name: "alpha", Body: &ast.Block{}},
	}}
	fileB := &ast.File{Decls: []ast.Decl{
		&ast.FnDecl{Name: "beta", Body: &ast.Block{}},
		&ast.FnDecl{Name: "gamma", Body: &ast.Block{}},
	}}
	pkg := &resolve.Package{
		Name: "two_files",
		Files: []*resolve.PackageFile{
			{Path: "/a.osty", File: fileA},
			{Path: "/b.osty", File: fileB},
		},
	}

	mod, issues := LowerPackage("two_files", pkg, nil)
	if len(issues) != 0 {
		t.Fatalf("LowerPackage() issues = %v, want none", issues)
	}
	if mod == nil {
		t.Fatalf("LowerPackage() returned nil module")
	}
	if mod.Package != "two_files" {
		t.Fatalf("Module.Package = %q, want %q", mod.Package, "two_files")
	}
	gotNames := make([]string, 0, len(mod.Decls))
	for _, d := range mod.Decls {
		fn, ok := d.(*FnDecl)
		if !ok {
			t.Fatalf("decl %T, want *FnDecl", d)
		}
		gotNames = append(gotNames, fn.Name)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(gotNames) != len(want) {
		t.Fatalf("got %d decls (%v), want %d (%v)", len(gotNames), gotNames, len(want), want)
	}
	for i, w := range want {
		if gotNames[i] != w {
			t.Fatalf("decl[%d] = %q, want %q (full order: %v)", i, gotNames[i], w, gotNames)
		}
	}
}

// TestLowerPackageHandlesEmptyAndNilFiles guards the two pathological
// inputs that real packages can present: a PackageFile with a nil File
// (parse failed mid-load) and an empty Files slice (resolve found
// nothing). Both must return a non-nil module so the validator and
// downstream emitters keep running.
func TestLowerPackageHandlesEmptyAndNilFiles(t *testing.T) {
	pkg := &resolve.Package{
		Name: "edge",
		Files: []*resolve.PackageFile{
			nil,
			{Path: "/b.osty", File: nil},
			{Path: "/c.osty", File: &ast.File{Decls: []ast.Decl{
				&ast.FnDecl{Name: "only", Body: &ast.Block{}},
			}}},
		},
	}

	mod, issues := LowerPackage("edge", pkg, nil)
	if len(issues) != 0 {
		t.Fatalf("LowerPackage() issues = %v, want none", issues)
	}
	if mod == nil || len(mod.Decls) != 1 {
		t.Fatalf("Module = %+v, want exactly one decl from the only viable file", mod)
	}
	fn, ok := mod.Decls[0].(*FnDecl)
	if !ok || fn.Name != "only" {
		t.Fatalf("Module.Decls[0] = %+v, want FnDecl{Name:\"only\"}", mod.Decls[0])
	}
}

// TestLowerPackageRejectsNilPackage protects callers from accidentally
// silent zero-module builds when the resolver upstream returned nil.
func TestLowerPackageRejectsNilPackage(t *testing.T) {
	mod, issues := LowerPackage("nope", nil, nil)
	if mod != nil {
		t.Fatalf("LowerPackage(nil) module = %+v, want nil", mod)
	}
	if len(issues) != 1 {
		t.Fatalf("LowerPackage(nil) issues = %v, want exactly one", issues)
	}
}

// TestLowerPackageEmptyFilesProducesEmptyModule verifies the "package
// resolved but had no files" case: we still return a non-nil module
// (so Validate / GenerateModule can run normally) and produce no
// issues — an empty Decls slice is a valid module shape.
func TestLowerPackageEmptyFilesProducesEmptyModule(t *testing.T) {
	pkg := &resolve.Package{Name: "empty"}
	mod, issues := LowerPackage("empty", pkg, nil)
	if len(issues) != 0 {
		t.Fatalf("LowerPackage(empty) issues = %v, want none", issues)
	}
	if mod == nil {
		t.Fatalf("LowerPackage(empty) module = nil, want non-nil")
	}
	if len(mod.Decls) != 0 {
		t.Fatalf("Module.Decls = %v, want empty", mod.Decls)
	}
}
