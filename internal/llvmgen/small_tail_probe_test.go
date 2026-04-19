package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestProbeSmallTailLower records the current lowering state of the
// small-tail toolchain modules. pkg_policy.osty is expected to lower
// cleanly after the std.strings shim + bare None/Some support; the
// other two still hit broader walls (LLVM014 Iterable for-in, LLVM011
// struct-param) tracked separately.
func TestProbeSmallTailLower(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	cases := []struct {
		rel                string
		expectClean        bool
		allowedWallSnippet string // err must contain this iff not clean
	}{
		{"toolchain/pkg_policy.osty", true, ""},
		{"toolchain/profile_flags.osty", true, ""},
		{"toolchain/diagnostic.osty", false, "LLVM011"},
	}
	for _, tc := range cases {
		path := filepath.Join(root, tc.rel)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", tc.rel, err)
			continue
		}
		file, diags := parser.ParseDiagnostics(src)
		if file == nil {
			t.Errorf("%s: parse nil (%d diags)", tc.rel, len(diags))
			continue
		}
		_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: path})
		if tc.expectClean {
			if err != nil {
				t.Errorf("%s: expected clean lowering, got: %v", tc.rel, err)
			} else {
				t.Logf("%s: lowered cleanly", tc.rel)
			}
			continue
		}
		if err == nil {
			t.Logf("%s: lowered cleanly (was expected to hit %s — revisit probe)", tc.rel, tc.allowedWallSnippet)
			continue
		}
		if !strings.Contains(err.Error(), tc.allowedWallSnippet) {
			t.Errorf("%s: unexpected wall (expected %s): %v", tc.rel, tc.allowedWallSnippet, err)
		} else {
			t.Logf("%s: still on expected wall %s: %v", tc.rel, tc.allowedWallSnippet, err)
		}
	}
}
