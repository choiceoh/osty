package runner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPolicySnapshotParityWithOsty guards against drift between
// Osty policy files under toolchain/ (source of truth) and the Go
// snapshots in this package (consumed at build time).
//
// It parses each Osty file for every `pub fn` and `pub struct` and
// asserts a correspondingly-named exported Go symbol exists
// somewhere in this package. The naming rule is: upper-camel in
// Osty stays upper-camel in Go, lower-camel in Osty is capitalized
// (since Go uses leading capitals for export). Unexported Osty
// identifiers (`fn`/`struct` without `pub`) are helpers and
// intentionally not enforced — they may be inlined or renamed in
// the Go snapshot.
//
// Add a new Osty file to `ostySources` when migrating more CLI
// policy to the toolchain.
//
// When this test fails, either:
//
//   - You changed an Osty file; mirror the change in the Go
//     snapshot and update goNameOf's allowlist if the API
//     genuinely diverges.
//   - You changed a Go snapshot; backport the change to its Osty
//     source so the toolchain stays the authority.
func TestPolicySnapshotParityWithOsty(t *testing.T) {
	ostySources := []string{"runner.osty", "airepair_flags.osty"}

	// The Go side adapts names to Go conventions; RunnerDiag
	// specifically is shortened to Diag here because the full
	// package path makes "runner.Diag" clearer than
	// "runner.RunnerDiag". Other names are a pure capitalisation
	// round-trip.
	goNameOf := func(osty string) string {
		if osty == "RunnerDiag" {
			return "Diag"
		}
		return strings.ToUpper(osty[:1]) + osty[1:]
	}

	exports := collectGoExports(t)

	for _, rel := range ostySources {
		t.Run(rel, func(t *testing.T) {
			ostyPath := repoRelPath(t, "toolchain", rel)
			src, err := os.ReadFile(ostyPath)
			if err != nil {
				t.Fatalf("read %s: %v", ostyPath, err)
			}
			pubFns := extractOstyPubNames(string(src), `pub\s+fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
			pubStructs := extractOstyPubNames(string(src), `pub\s+struct\s+([A-Za-z_][A-Za-z0-9_]*)`)
			if len(pubFns) == 0 && len(pubStructs) == 0 {
				t.Fatalf("%s declared no pub fns/structs — parser regression?", rel)
			}
			for _, name := range pubFns {
				want := goNameOf(name)
				if _, ok := exports[want]; !ok {
					t.Errorf("pub fn %s from %s has no Go export %s", name, rel, want)
				}
			}
			for _, name := range pubStructs {
				want := goNameOf(name)
				if _, ok := exports[want]; !ok {
					t.Errorf("pub struct %s from %s has no Go export %s", name, rel, want)
				}
			}
		})
	}
}

func extractOstyPubNames(src, pattern string) []string {
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringSubmatch(src, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

func collectGoExports(t *testing.T) map[string]struct{} {
	t.Helper()
	dir := repoRelPath(t, "internal", "runner")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	out := map[string]struct{}{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Name.IsExported() {
						out[d.Name.Name] = struct{}{}
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
							out[ts.Name.Name] = struct{}{}
						}
					}
				}
			}
		}
	}
	return out
}

// repoRelPath resolves a repo-relative path by walking up from the
// test's working directory (the package under test) until it finds
// go.mod. This keeps the test robust to `go test ./internal/runner`
// invoked from any cwd.
func repoRelPath(t *testing.T, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod from %s", dir)
		}
		dir = parent
	}
	return filepath.Join(append([]string{dir}, parts...)...)
}
