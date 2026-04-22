package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
)

// TestPreparePackageMergesMultiFileDecls verifies the headline
// multi-file build contract: every file's top-level fns reach the
// emitted ir.Module in pkg.Files order. This is what allows
// `osty build` on a real on-disk package containing more than one
// `.osty` file to produce a single binary that calls across files.
func TestPreparePackageMergesMultiFileDecls(t *testing.T) {
	fileA := &ast.File{Decls: []ast.Decl{
		&ast.FnDecl{Name: "fromA", Body: &ast.Block{}},
	}}
	fileB := &ast.File{Decls: []ast.Decl{
		&ast.FnDecl{Name: "fromB", Body: &ast.Block{}},
	}}
	pkg := &resolve.Package{
		Name: "multi",
		Files: []*resolve.PackageFile{
			{Path: "/a.osty", File: fileA},
			{Path: "/b.osty", File: fileB},
		},
	}

	entry, err := PreparePackage("main", "/a.osty", pkg, pkg.Files[0], nil)
	if err != nil {
		t.Fatalf("PreparePackage returned error: %v", err)
	}
	if entry.IR == nil {
		t.Fatalf("Entry.IR is nil")
	}

	gotNames := make([]string, 0)
	for _, d := range entry.IR.Decls {
		fn, ok := d.(*ir.FnDecl)
		if !ok {
			continue
		}
		gotNames = append(gotNames, fn.Name)
	}
	want := []string{"fromA", "fromB"}
	if strings.Join(gotNames, ",") != strings.Join(want, ",") {
		t.Fatalf("merged fn order = %v, want %v", gotNames, want)
	}

	// Entry.File should still point at the explicit entryFile (fileA)
	// so diagnostic tooling and the legacy AST bridge keep working
	// against the binary's primary source.
	if entry.File != fileA {
		t.Fatalf("Entry.File != fileA")
	}
}

func TestPreparePackageSingleFileKeepsEntryFileAndDecl(t *testing.T) {
	file := &ast.File{Decls: []ast.Decl{
		&ast.FnDecl{Name: "main", Body: &ast.Block{}},
	}}
	pkg := &resolve.Package{
		Name: "single",
		Files: []*resolve.PackageFile{
			{Path: "/main.osty", File: file},
		},
	}

	entry, err := PreparePackage("main", "/main.osty", pkg, pkg.Files[0], nil)
	if err != nil {
		t.Fatalf("PreparePackage returned error: %v", err)
	}
	if entry.File != file {
		t.Fatalf("Entry.File = %p, want %p", entry.File, file)
	}
	if entry.IR == nil || len(entry.IR.Decls) != 1 {
		t.Fatalf("Entry.IR.Decls len = %d, want 1", len(entry.IR.Decls))
	}
	if fn, ok := entry.IR.Decls[0].(*ir.FnDecl); !ok || fn.Name != "main" {
		t.Fatalf("Entry.IR.Decls[0] = %#v, want ir.FnDecl main", entry.IR.Decls[0])
	}
}

// TestPreparePackageNilPackage rejects the obviously-bad input so a
// regression in the build orchestrator surfaces here instead of as
// a nil-deref deeper in the lowerer.
func TestPreparePackageNilPackage(t *testing.T) {
	_, err := PreparePackage("main", "/x.osty", nil, nil, nil)
	if err == nil {
		t.Fatalf("PreparePackage(nil) returned no error, want one")
	}
}

// TestPreparePackagePicksFirstNonNilFileWhenEntryUnset verifies that
// callers without a designated entry file (e.g. the IR-direct gen
// path) still get a sensible Entry.File so downstream diagnostics
// have something to anchor source positions against.
func TestPreparePackagePicksFirstNonNilFileWhenEntryUnset(t *testing.T) {
	fileA := &ast.File{Decls: []ast.Decl{
		&ast.FnDecl{Name: "fromA", Body: &ast.Block{}},
	}}
	fileB := &ast.File{Decls: []ast.Decl{
		&ast.FnDecl{Name: "fromB", Body: &ast.Block{}},
	}}
	pkg := &resolve.Package{
		Name: "pick",
		Files: []*resolve.PackageFile{
			nil,                             // resolver may emit a nil slot
			{Path: "/skip.osty", File: nil}, // parse-failed slot
			{Path: "/a.osty", File: fileA},  // first viable
			{Path: "/b.osty", File: fileB},
		},
	}

	entry, err := PreparePackage("main", "/auto.osty", pkg, nil, nil)
	if err != nil {
		t.Fatalf("PreparePackage returned error: %v", err)
	}
	if entry.File != fileA {
		t.Fatalf("Entry.File = %p, want fileA = %p (first viable)", entry.File, fileA)
	}
	if entry.IR == nil || len(entry.IR.Decls) != 2 {
		t.Fatalf("Entry.IR.Decls len = %d, want 2", len(entry.IR.Decls))
	}
}
