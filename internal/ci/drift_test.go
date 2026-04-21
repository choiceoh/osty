package ci

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

// TestCoreSnapshotParityWithOsty guards against drift between
// toolchain/ci.osty (long-term source of truth) and
// core_snapshot.go (hand-maintained Go snapshot consumed at
// build time). When either side changes without the other, this
// test fails with a pointer to the missing symbol.
//
// Unlike internal/runner's drift test, the ci snapshot preserves
// Osty casing verbatim — `pub fn ciIsStrictSemver` stays
// `ciIsStrictSemver` in Go rather than being capitalised for Go
// export conventions. That's a deliberate choice for the ci
// package: the pub fns are package-internal helpers the runner
// reaches for at test time, not cross-package API.
func TestCoreSnapshotParityWithOsty(t *testing.T) {
	ostyPath := repoRelPath(t, "toolchain", "ci.osty")
	src, err := os.ReadFile(ostyPath)
	if err != nil {
		t.Fatalf("read %s: %v", ostyPath, err)
	}
	text := string(src)

	names := collectCiPubNames(text)
	if len(names.fns)+len(names.types)+len(names.bindings) == 0 {
		t.Fatalf("%s declared no pub fns/types/bindings — parser regression?", ostyPath)
	}

	syms := collectCiGoSymbols(t)
	for _, n := range names.fns {
		if _, ok := syms.funcs[n]; ok {
			continue
		}
		if _, ok := syms.methods[n]; ok {
			continue
		}
		t.Errorf("pub fn %s in toolchain/ci.osty has no Go counterpart in core_snapshot.go", n)
	}
	for _, n := range names.types {
		if _, ok := syms.types[n]; !ok {
			t.Errorf("pub type/struct %s in toolchain/ci.osty has no Go counterpart in core_snapshot.go", n)
		}
	}
	for _, n := range names.bindings {
		if _, ok := syms.bindings[n]; !ok {
			t.Errorf("pub let %s in toolchain/ci.osty has no Go counterpart in core_snapshot.go", n)
		}
	}
}

type ciOstyPubNames struct {
	fns      []string
	types    []string
	bindings []string
}

var (
	ciPubFnRe      = regexp.MustCompile(`(?m)^\s*pub\s+fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
	ciPubTypeRe    = regexp.MustCompile(`(?m)^\s*pub\s+(?:struct|enum|interface|type)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	ciPubBindingRe = regexp.MustCompile(`(?m)^\s*pub\s+let\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func collectCiPubNames(src string) ciOstyPubNames {
	return ciOstyPubNames{
		fns:      uniqueCiMatches(ciPubFnRe, src),
		types:    uniqueCiMatches(ciPubTypeRe, src),
		bindings: uniqueCiMatches(ciPubBindingRe, src),
	}
}

func uniqueCiMatches(re *regexp.Regexp, src string) []string {
	matches := re.FindAllStringSubmatch(src, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

type ciGoSymbols struct {
	funcs    map[string]struct{}
	methods  map[string]struct{}
	types    map[string]struct{}
	bindings map[string]struct{}
}

func collectCiGoSymbols(t *testing.T) ciGoSymbols {
	t.Helper()
	dir := repoRelPath(t, "internal", "ci")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	out := ciGoSymbols{
		funcs:    map[string]struct{}{},
		methods:  map[string]struct{}{},
		types:    map[string]struct{}{},
		bindings: map[string]struct{}{},
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv != nil {
						out.methods[d.Name.Name] = struct{}{}
					} else {
						out.funcs[d.Name.Name] = struct{}{}
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							out.types[s.Name.Name] = struct{}{}
						case *ast.ValueSpec:
							for _, n := range s.Names {
								out.bindings[n.Name] = struct{}{}
							}
						}
					}
				}
			}
		}
	}
	return out
}

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
