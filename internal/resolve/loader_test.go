package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadPackageReadsAllOstyFiles confirms LoadPackage picks up every
// `.osty` file in the directory, sorts them lexicographically for
// deterministic diagnostic order, and excludes `*_test.osty` by default.
func TestLoadPackageReadsAllOstyFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("b.osty", "fn b() {}")
	write("a.osty", "fn a() {}")
	write("c_test.osty", "fn c() {}")                           // excluded
	write("readme.md", "# irrelevant")                          // excluded
	write("sub_dir_marker.osty.bak", "not an osty file really") // excluded

	pkg, err := LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	if len(pkg.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(pkg.Files))
	}
	gotNames := []string{filepath.Base(pkg.Files[0].Path), filepath.Base(pkg.Files[1].Path)}
	want := []string{"a.osty", "b.osty"}
	for i, n := range want {
		if gotNames[i] != n {
			t.Errorf("files[%d] = %q; want %q", i, gotNames[i], n)
		}
	}
}

// TestLoadPackageWithTestsIncludesTestFiles verifies the test-aware
// variant picks up `*_test.osty` files too.
func TestLoadPackageWithTestsIncludesTestFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.osty"), []byte("fn a() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a_test.osty"), []byte("fn testA() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := LoadPackageWithTests(dir)
	if err != nil {
		t.Fatalf("LoadPackageWithTests: %v", err)
	}
	if len(pkg.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(pkg.Files))
	}
}

// TestLoadPackageEmptyDir returns a zero-file package, not an error.
func TestLoadPackageEmptyDir(t *testing.T) {
	dir := t.TempDir()
	pkg, err := LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	if len(pkg.Files) != 0 {
		t.Fatalf("want 0 files, got %d", len(pkg.Files))
	}
	if !strings.HasSuffix(pkg.Dir, filepath.Base(dir)) {
		t.Errorf("Dir looks wrong: %q", pkg.Dir)
	}
}

// TestLoadPackageAndResolve verifies the round-trip: load a directory,
// resolve as one package, observe cross-file references work.
func TestLoadPackageAndResolve(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.osty"),
		[]byte(`pub fn greet(name: String) -> String { "hi, {name}" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.osty"),
		[]byte(`fn main() { println(greet("world")) }`), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := LoadPackage(dir)
	if err != nil {
		t.Fatal(err)
	}
	res := ResolvePackage(pkg, NewPrelude())
	for _, d := range res.Diags {
		t.Errorf("unexpected diag: %s", d.Error())
	}
}
