package selfhost

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var frontendRunFileCallRE = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(?:[Rr]un)\.File\(\)`)
var structuredSelfhostAliasRE = regexp.MustCompile(`selfhost\.(PackageCheckInput|PackageCheckFile|PackageCheckImport|CheckResult|CheckedNode|CheckDiagnosticRecord|ResolveResult|ResolvedRef|ResolvedSymbol|ResolvedTypeRef|PackageResolveInput|PackageResolveFile|CfgEnv)\b`)

func TestProductionFrontendPathsDoNotCallFrontendRunFile(t *testing.T) {
	root := repoRootForAuthorityTest(t)
	for _, dir := range []string{"cmd", "internal"} {
		scanDir := filepath.Join(root, dir)
		err := filepath.WalkDir(scanDir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case ".git", ".osty", ".direnv":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if frontendRunFileCallRE.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					t.Fatalf("%s:%d calls FrontendRun.File(); use FrontendRun/arena/structured results, or LowerPublicFileFromRun at an explicit compatibility boundary", rel, i+1)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
}

func TestNativeConsumersImportStructuredAPITypesDirectly(t *testing.T) {
	root := repoRootForAuthorityTest(t)
	for _, dir := range []string{"cmd/osty", "internal/check", "internal/resolve"} {
		scanDir := filepath.Join(root, filepath.FromSlash(dir))
		err := filepath.WalkDir(scanDir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case ".git", ".osty", ".direnv":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if structuredSelfhostAliasRE.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					t.Fatalf("%s:%d uses a selfhost compatibility alias for structured API data; import internal/selfhost/api directly", rel, i+1)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
}

func repoRootForAuthorityTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("locate repo root from %s: %v", file, err)
	}
	return root
}
