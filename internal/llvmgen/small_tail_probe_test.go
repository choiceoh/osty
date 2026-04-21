package llvmgen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestProbeSmallTailLower is the regression ratchet for per-file LLVM
// parity on the toolchain/*.osty modules. TestSweepToolchainLargeTail
// is the info-only discovery probe; this test fails loudly if a
// previously-clean module starts re-hitting a wall or if a tracked
// wall moves off its expected code. Keep the clean set in sync with
// the sweep's CLEAN count.
//
// wantWall is the expected wallCode() result for each module; "" means
// CLEAN. When a walled entry actually lowers cleanly, the test logs a
// promotion hint instead of failing.
func TestProbeSmallTailLower(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	cases := []struct {
		rel      string
		wantWall string
	}{
		{"toolchain/airepair_flags.osty", ""},
		{"toolchain/diag_examples.osty", ""},
		{"toolchain/diag_policy.osty", ""},
		{"toolchain/diagnostic.osty", "LLVM011"},
		{"toolchain/manifest_features.osty", ""},
		{"toolchain/package_entry.osty", ""},
		{"toolchain/pkg_policy.osty", ""},
		{"toolchain/profile_flags.osty", ""},
		{"toolchain/scaffold_policy.osty", ""},
		{"toolchain/semver.osty", ""},
		{"toolchain/test_runner.osty", ""},
		// ty.osty calls checkHashKey (defined in check_env.osty), so a
		// single-file probe walls on LLVM015 cross-module scope. The
		// previous "clean" status was a false positive from CRLF
		// mis-lexing; once selfhost.Lex / Run normalize \r\n the call
		// appears with its real *ast.Ident shape and the emitter
		// reports the unresolved callee honestly.
		{"toolchain/ty.osty", "LLVM015"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.rel, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(root, tc.rel)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			file, diags := parser.ParseDiagnostics(src)
			if file == nil {
				t.Fatalf("parse nil (%d diags)", len(diags))
			}
			_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: path})
			got := ""
			if err != nil {
				got = wallCode(err.Error())
			}
			switch {
			case got == tc.wantWall:
				t.Logf("%s: %s", tc.rel, formatWall(err))
			case tc.wantWall == "":
				t.Fatalf("expected clean lowering, got %s: %v", got, err)
			case got == "":
				t.Logf("%s: lowered cleanly (was expected %s — promote to clean)", tc.rel, tc.wantWall)
			default:
				t.Fatalf("unexpected wall (want %s, got %s): %v", tc.wantWall, got, err)
			}
		})
	}
}
