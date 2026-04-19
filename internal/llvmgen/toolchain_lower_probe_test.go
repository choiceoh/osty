package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestProbeToolchainSemverAndDiagPolicyLower confirms the std.strings
// shim unblocks the two toolchain modules that trip LLVM016 on clean HEAD.
// It is a scoped probe; broader toolchain coverage still fails on unrelated
// walls (e.g. LLVM011 generic `T`, LLVM014 Iterable lowering), so we only
// assert "no LLVM016 strings alias" for these two files.
func TestProbeToolchainSemverAndDiagPolicyLower(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}

	cases := []string{
		"toolchain/semver.osty",
		"toolchain/diag_policy.osty",
	}
	for _, rel := range cases {
		path := filepath.Join(root, rel)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: read: %v", rel, err)
		}
		file, diags := parser.ParseDiagnostics(src)
		if file == nil {
			t.Fatalf("%s: parse returned nil file (%d diags)", rel, len(diags))
		}

		_, err = generateFromAST(file, Options{
			PackageName: "main",
			SourcePath:  path,
		})
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "LLVM016") && strings.Contains(msg, `"strings"`) {
				t.Errorf("%s: still blocked by LLVM016 strings alias: %v", rel, err)
			} else {
				t.Logf("%s: lowered past LLVM016 (remaining wall: %v)", rel, err)
			}
		} else {
			t.Logf("%s: lowered cleanly", rel)
		}
	}
}
