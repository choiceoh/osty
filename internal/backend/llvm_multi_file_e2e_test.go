package backend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
)

// TestPreparePackageMultiFileE2EFromRealSources mirrors the
// `examples/multi_file_e2e/` fixture: a `pub fn` and a `pub struct`
// in `lib.osty`, consumed by test functions in `multi_file_test.osty`
// in the same package. The test loads them through the real native
// loader, runs the package-level checker, and threads the result
// through `PreparePackage` so the multi-file plumbing is exercised
// end-to-end up to the point where the LLVM dispatcher receives a
// merged `*ir.Module`.
//
// This is the (A1) gate for the Multi-file native emit/link
// milestone: cross-file `fn` symbols resolve, cross-file struct
// layout stays consistent, and both files contribute to one IR
// module the backend can lower in a single emit step. P0 short-suite
// shapes (Map.update, optional aggregate, generic method turbofish,
// interface dispatch, nested binding pattern) are deliberately
// avoided so the gate measures multi-file infrastructure in
// isolation from unrelated lowering walls.
func TestPreparePackageMultiFileE2EFromRealSources(t *testing.T) {
	dir := t.TempDir()

	libSrc := []byte(`pub struct Point {
    pub x: Int,
    pub y: Int,
}

pub fn addInts(a: Int, b: Int) -> Int {
    a + b
}

pub fn pointSum(p: Point) -> Int {
    p.x + p.y
}

pub fn translate(p: Point, dx: Int, dy: Int) -> Point {
    Point { x: p.x + dx, y: p.y + dy }
}
`)
	if err := os.WriteFile(filepath.Join(dir, "lib.osty"), libSrc, 0o644); err != nil {
		t.Fatalf("write lib.osty: %v", err)
	}

	testSrc := []byte(`use std.testing

fn testCrossFileFnCallReturnsSum() {
    testing.assertEq(addInts(2, 3), 5)
}

fn testCrossFileStructPassedToCrossFileFn() {
    let p = Point { x: 4, y: 9 }
    testing.assertEq(pointSum(p), 13)
}

fn testCrossFileStructReturnedFromCrossFileFn() {
    let p = Point { x: 1, y: 2 }
    let q = translate(p, 10, 20)
    testing.assertEq(q.x, 11)
    testing.assertEq(q.y, 22)
}
`)
	if err := os.WriteFile(filepath.Join(dir, "multi_file_test.osty"), testSrc, 0o644); err != nil {
		t.Fatalf("write multi_file_test.osty: %v", err)
	}

	pkg, err := resolve.LoadPackageForNativeWithTests(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNativeWithTests: %v", err)
	}
	if got := len(pkg.Files); got != 2 {
		t.Fatalf("pkg.Files len = %d, want 2 (lib.osty + multi_file_test.osty)", got)
	}

	chk := check.Package(pkg, nil, check.Opts{})
	for _, d := range chk.Diags {
		if d.Severity == diag.Error {
			t.Fatalf("check produced error: %s", d.Message)
		}
	}

	var entryFile *resolve.PackageFile
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if filepath.Base(pf.Path) == "multi_file_test.osty" {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		t.Fatalf("could not locate multi_file_test.osty as entryFile")
	}

	entry, err := PreparePackage("main", entryFile.Path, pkg, entryFile, chk)
	if err != nil {
		t.Fatalf("PreparePackage: %v", err)
	}
	if entry.IR == nil {
		t.Fatalf("entry.IR is nil")
	}
	if entry.MIR == nil {
		t.Fatalf("entry.MIR is nil — MIR lowering is part of the multi-file emit/link contract")
	}

	wantFns := map[string]bool{
		"addInts":                                false,
		"pointSum":                               false,
		"translate":                              false,
		"testCrossFileFnCallReturnsSum":          false,
		"testCrossFileStructPassedToCrossFileFn": false,
		"testCrossFileStructReturnedFromCrossFileFn": false,
	}
	wantStructs := map[string]bool{"Point": false}

	for _, d := range entry.IR.Decls {
		switch n := d.(type) {
		case *ir.FnDecl:
			if _, ok := wantFns[n.Name]; ok {
				wantFns[n.Name] = true
			}
		case *ir.StructDecl:
			if _, ok := wantStructs[n.Name]; ok {
				wantStructs[n.Name] = true
			}
		}
	}
	for name, seen := range wantFns {
		if !seen {
			t.Errorf("merged IR module missing fn %q (cross-file decl did not reach backend)", name)
		}
	}
	for name, seen := range wantStructs {
		if !seen {
			t.Errorf("merged IR module missing struct %q (cross-file struct did not reach backend)", name)
		}
	}
}
