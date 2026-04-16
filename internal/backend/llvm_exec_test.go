package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestLLVMSmokeCorpusExecutableParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping LLVM executable smoke corpus in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("LLVM executable smoke corpus is not wired for Windows host linking yet")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH; skipping LLVM executable smoke corpus")
	}

	root := repoRoot(t)
	for _, tc := range llvmgen.SmokeExecutableCorpus() {
		t.Run(tc.Name, func(t *testing.T) {
			fixture := filepath.Join(root, "testdata", "backend", "llvm_smoke", tc.Fixture)
			file, res, chk := parseResolveCheckFixture(t, fixture)
			outRoot := t.TempDir()
			result, err := LLVMBackend{}.Emit(context.Background(), Request{
				Layout:     Layout{Root: outRoot, Profile: "debug"},
				Emit:       EmitBinary,
				BinaryName: tc.Name,
				Entry: Entry{
					PackageName: "main",
					SourcePath:  fixture,
					File:        file,
					Resolve:     res,
					Check:       chk,
				},
			})
			if err != nil {
				t.Fatalf("EmitBinary(%s): %v", fixture, err)
			}
			if result == nil || result.Artifacts.Binary == "" {
				t.Fatalf("EmitBinary(%s) returned no binary artifact: %+v", fixture, result)
			}
			for name, artifact := range map[string]string{
				"llvm ir": result.Artifacts.LLVMIR,
				"object":  result.Artifacts.Object,
				"binary":  result.Artifacts.Binary,
			} {
				if _, err := os.Stat(artifact); err != nil {
					t.Fatalf("%s artifact missing at %s: %v", name, artifact, err)
				}
			}
			combined, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("run %s: %v\n%s", result.Artifacts.Binary, err, combined)
			}
			if string(combined) != tc.Stdout {
				t.Fatalf("stdout for %s = %q, want %q", tc.Name, combined, tc.Stdout)
			}
		})
	}
}

func parseResolveCheckFixture(t *testing.T, path string) (*ast.File, *resolve.Result, *check.Result) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse %s: %v", path, diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	return file, res, chk
}
