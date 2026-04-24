package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func collectToolchainProbeFiles(dir string, skipBootstrapOnly bool) ([]string, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var files []string
	var skipped []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		if !skipBootstrapOnly {
			files = append(files, name)
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
	return files, skipped, nil
}

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
	files, _, err := collectToolchainProbeFiles(dir, false)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
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
//   - a `use runtime.golegacy.*` FFI (the legacy Go AST bridge),
//   - a `use runtime.cihost` FFI (the CI runner's Go host adapter), or
//   - a `use go "..."` FFI (arbitrary Go package host binding)
//
// Both forms require the Osty compiler to be running under the
// Go-hosted bootstrap CLI; neither has native LLVM runtime lowering
// by design. Files carrying either stanza are skipped by the
// native-path whole-toolchain probe. See LLVM_MIGRATION_PLAN.md
// § "astbridge bootstrap-only adapter" for the migration path.
func isBootstrapOnlyOstyFile(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "use runtime.golegacy.") ||
			strings.HasPrefix(trimmed, "use runtime.cihost") ||
			strings.HasPrefix(trimmed, "use go \"") {
			return true
		}
	}
	return false
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
	files, skipped, err := collectToolchainProbeFiles(dir, true)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
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

// TestProbeNativeToolchainMergedMIR is the MIR-path twin of
// TestProbeNativeToolchainMerged. The AST-only probe uses
// `generateFromAST`, which bypasses the IR→MIR lowering and measures the
// legacy HIR→AST bridge surface. The real backend
// (internal/backend/llvm.go) dispatches MIR-first and only falls back to
// the legacy path on ErrUnsupported, so the AST probe's wall is
// systematically more pessimistic than the self-host critical path.
//
// This probe runs the merged native toolchain through the full
// parse → resolve → check → ir.Lower → ir.Monomorphize → mir.Lower →
// GenerateFromMIR pipeline and reports the first stage that walls
// (along with its message). Info-only.
//
// The probe logs checker/lowering counts and keeps going so we can see
// where the MIR emitter itself walls.
func TestProbeNativeToolchainMergedMIR(t *testing.T) {
	if testing.Short() {
		t.Skip("info-only; slow; skipped in -short")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	files, skipped, err := collectToolchainProbeFiles(dir, true)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	if len(skipped) > 0 {
		t.Logf("bootstrap-only files skipped (%d): %s", len(skipped), strings.Join(skipped, ", "))
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("native-toolchain merged parse returned nil (%d files, %d bytes)", len(files), len(merged))
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        merged,
		Privileged:    true,
	})
	if chk != nil {
		t.Logf("check: %d diag(s)", len(chk.Diags))
	}
	mod, irIssues := ir.Lower("main", file, res, chk)
	if len(irIssues) > 0 {
		t.Logf("ir.Lower: %d issue(s); first = %v", len(irIssues), irIssues[0])
	}
	if mod == nil {
		t.Fatalf("ir.Lower returned nil module")
	}
	monoMod, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) > 0 {
		t.Logf("ir.Monomorphize: %d err(s); first = %v", len(monoErrs), monoErrs[0])
	}
	if monoMod == nil {
		t.Fatalf("ir.Monomorphize returned nil module")
	}
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatalf("mir.Lower returned nil module")
	}
	_, err = GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/toolchain_native_merged_mir.osty"})
	t.Logf("NATIVE TOOLCHAIN (MIR) first wall: %s", formatWall(err))
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
