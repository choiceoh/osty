package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// mergeToolchainSources concatenates toolchain files in order into a
// single source buffer. The parser tolerates duplicate
// `use std.strings as strings` stanzas, so repeats across files are
// fine; this is the cheap whole-program approximation the AST path
// supports.
func mergeToolchainSources(t *testing.T, root string, files []string) []byte {
	t.Helper()
	var buf strings.Builder
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join(root, "toolchain", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		buf.WriteString("// === ")
		buf.WriteString(name)
		buf.WriteString(" ===\n")
		buf.Write(src)
		buf.WriteString("\n")
	}
	return []byte(buf.String())
}

// TestProbeLargeFileParity lowers each large / god-node toolchain
// module merged with its cross-module type declarations. Single-file
// sweeps flag every one of these as LLVM011; merging reveals whether
// that wall is a real backend gap or pure cross-module scope.
//
// Coverage: ir.osty, check_env.osty, and the four god-nodes elab /
// parser / lint / resolve. For each entry, `files` is the minimum
// merge set that clears the single-file first wall — picked by walking
// the first-wall type payload back to its declaring toolchain basename.
// Info-only; never fails.
func TestProbeLargeFileParity(t *testing.T) {
	if testing.Short() {
		t.Skip("info-only; skipped in -short")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	cases := []struct {
		name  string
		files []string
	}{
		{
			name:  "check_env",
			files: []string{"frontend.osty", "lexer.osty", "diagnostic.osty", "ty.osty", "check_diag.osty", "check_env.osty"},
		},
		{
			name:  "ir",
			files: []string{"frontend.osty", "parser.osty", "ir.osty"},
		},
		{
			name:  "parser",
			files: []string{"frontend.osty", "parser.osty"},
		},
		{
			name:  "resolve",
			files: []string{"frontend.osty", "parser.osty", "resolve.osty"},
		},
		{
			name:  "lint",
			files: []string{"frontend.osty", "parser.osty", "resolve.osty", "lint.osty"},
		},
		{
			// elab's type signature spans the whole check/diag closure, so
			// each unresolved cross-file type is a scope artifact rather
			// than a new backend wall — the merge set has to include every
			// declaring basename in one shot.
			name: "elab",
			files: []string{
				"frontend.osty",
				"lexer.osty",
				"parser.osty",
				"diagnostic.osty",
				"ty.osty",
				"check_diag.osty",
				"check_env.osty",
				"resolve.osty",
				"lint.osty",
				"solve.osty",
				"core.osty",
				"elab.osty",
			},
		},
	}
	for _, c := range cases {
		merged := mergeToolchainSources(t, root, c.files)
		file, _ := parser.ParseDiagnostics(merged)
		if file == nil {
			t.Logf("  %s (merged=%v): PARSE_FAIL", c.name, c.files)
			continue
		}
		_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/" + c.name + "_merged.osty"})
		t.Logf("  %s (merged=%v): %s", c.name, c.files, formatWall(err))
	}
}

// TestProbeWholeToolchainMerged lowers every non-test toolchain module
// merged into one buffer — reveals the actual global backend surface
// independent of cross-module scope. Info-only. Slow (~6 min), gated
// by -short.
func TestProbeWholeToolchainMerged(t *testing.T) {
	if testing.Short() {
		t.Skip("info-only; slow (~6 min); skipped in -short")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		files = append(files, name)
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("whole-toolchain merged parse returned nil (%d files, %d bytes)", len(files), len(merged))
	}
	_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/toolchain_merged.osty"})
	t.Logf("WHOLE TOOLCHAIN first wall: %s", formatWall(err))
}

// isBootstrapOnlyOstyFile reports whether the source is a
// host-boundary bootstrap adapter — i.e., whether it declares either
//
//   - a `use runtime.golegacy.*` FFI (the legacy Go AST bridge), or
//   - a `use go "..."` FFI (arbitrary Go package host binding)
//
// Both forms require the Osty compiler to be running under the
// Go-hosted bootstrap CLI; neither has native LLVM runtime lowering
// by design. Files carrying either stanza are skipped by the
// native-path whole-toolchain probe. See LLVM_MIGRATION_PLAN.md
// § "astbridge bootstrap-only adapter" for the migration path.
func isBootstrapOnlyOstyFile(src []byte) bool {
	s := string(src)
	return strings.Contains(s, "use runtime.golegacy.") ||
		strings.Contains(s, "use go \"")
}

// TestProbeNativeToolchainMerged is the whole-toolchain probe with
// bootstrap-only files (those that import runtime.golegacy.*) filtered
// out. Reveals the first real LLVM wall along the native self-host
// path — the wall that remains once the CLI is rewired off the
// Go-hosted bridge adapter. Info-only.
//
// Pair with TestProbeWholeToolchainMerged: the difference between the
// two walls tells us how much signal the bootstrap bridge is injecting
// into the histogram. Today: whole-merged → LLVM002 (bridge FFI);
// native-merged → the next real backend gap.
func TestProbeNativeToolchainMerged(t *testing.T) {
	if testing.Short() {
		t.Skip("info-only; slow; skipped in -short")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	var files []string
	var skipped []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if isBootstrapOnlyOstyFile(src) {
			skipped = append(skipped, name)
			continue
		}
		files = append(files, name)
	}
	if len(skipped) == 0 {
		t.Logf("no bootstrap-only files detected; this probe is equivalent to TestProbeWholeToolchainMerged")
	} else {
		t.Logf("bootstrap-only files skipped (%d): %s", len(skipped), strings.Join(skipped, ", "))
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("native-toolchain merged parse returned nil (%d files, %d bytes)", len(files), len(merged))
	}
	_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/toolchain_native_merged.osty"})
	t.Logf("NATIVE TOOLCHAIN first wall: %s", formatWall(err))
}

// TestProbeDiagnosticMergedWithLexer checks whether the LLVM011 wall on
// diagnostic.osty is a real backend gap or just the single-file probe's
// scope limitation. We concatenate diagnostic.osty + lexer.osty (which
// defines FrontLexStream) and feed the joined source through the same
// lowering path. If diagnostic alone fails but the merged source
// succeeds — or fails on a *different* type — the wall is purely
// cross-file scope, not a backend limitation.
//
// Info-only.
func TestProbeDiagnosticMergedWithLexer(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	a, err := os.ReadFile(filepath.Join(root, "toolchain/lexer.osty"))
	if err != nil {
		t.Fatalf("read lexer: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "toolchain/diagnostic.osty"))
	if err != nil {
		t.Fatalf("read diagnostic: %v", err)
	}
	merged := string(a) + "\n" + string(b)
	file, _ := parser.ParseDiagnostics([]byte(merged))
	if file == nil {
		t.Fatalf("parse merged returned nil")
	}
	_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/merged.osty"})
	if err != nil {
		t.Logf("merged source still errors: %v", err)
		if strings.Contains(err.Error(), "FrontLexStream") {
			t.Logf("still complains about FrontLexStream — likely needs more deps merged")
		}
	} else {
		t.Logf("merged source lowers cleanly — diagnostic.osty wall is cross-file scope")
	}
}
