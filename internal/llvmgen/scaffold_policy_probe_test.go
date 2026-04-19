package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestProbeScaffoldPolicyLowers locks in that scaffold_policy.osty —
// the first toolchain module to exercise String ordering comparisons
// (`unit >= "a" && unit <= "z"`) — lowers cleanly through the legacy
// AST path. Before the ordering fix this tripped LLVM011 "compare
// type ptr" because emitRuntimeStringCompare only handled EQ/NEQ.
func TestProbeScaffoldPolicyLowers(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	path := filepath.Join(root, "toolchain/scaffold_policy.osty")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, _ := parser.ParseDiagnostics(src)
	if file == nil {
		t.Fatalf("parse produced nil file")
	}
	out, err := generateFromAST(file, Options{PackageName: "main", SourcePath: path})
	if err != nil {
		t.Fatalf("scaffold_policy.osty failed to lower: %s", err.Error())
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_Compare(ptr, ptr)",
		"call i64 @osty_rt_strings_Compare",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}
