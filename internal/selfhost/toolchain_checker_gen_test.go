package selfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var toolchainCheckerSourceFiles = []string{
	"examples/selfhost-core/semver.osty",
	"examples/selfhost-core/semver_parse.osty",
	"toolchain/frontend.osty",
	"toolchain/lexer.osty",
	"toolchain/parser.osty",
	"examples/selfhost-core/formatter_ast.osty",
	"toolchain/check_bridge.osty",
	"toolchain/diagnostic.osty",
	"toolchain/check_diag.osty",
	"toolchain/ty.osty",
	"toolchain/core.osty",
	"toolchain/check_env.osty",
	"toolchain/solve.osty",
	"toolchain/elab.osty",
	"toolchain/check.osty",
	"examples/selfhost-core/resolve.osty",
	"examples/selfhost-core/lint.osty",
	"internal/selfhost/ast_lower.osty",
}

const toolchainCheckerStringsPrelude = `use go "strings" as strings {
    fn Count(s: String, substr: String) -> Int
    fn Fields(s: String) -> List<String>
    fn HasPrefix(s: String, prefix: String) -> Bool
    fn HasSuffix(s: String, prefix: String) -> Bool
    fn Join(elems: List<String>, sep: String) -> String
    fn ReplaceAll(s: String, old: String, new: String) -> String
    fn Repeat(s: String, count: Int) -> String
    fn Split(s: String, sep: String) -> List<String>
    fn SplitN(s: String, sep: String, n: Int) -> List<String>
    fn TrimPrefix(s: String, prefix: String) -> String
    fn TrimSpace(s: String) -> String
    fn TrimSuffix(s: String, suffix: String) -> String
}
`

func TestToolchainCheckerSourcesTranspile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping toolchain checker transpile smoke test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	merged, err := mergeToolchainCheckerSources(repoRoot)
	if err != nil {
		t.Fatalf("merge toolchain checker sources: %v", err)
	}

	tmpDir := t.TempDir()
	mergedPath := filepath.Join(tmpDir, "toolchain_checker_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		t.Fatalf("write merged source: %v", err)
	}
	generatedPath := filepath.Join(tmpDir, "toolchain_checker_generated.go")

	cmd := exec.Command(
		"go", "run", "-tags", "selfhostgen", "./cmd/osty", "gen",
		"--backend", "go",
		"--emit", "go",
		"--package", "selfhostsmoke",
		"-o", generatedPath,
		mergedPath,
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("transpile merged toolchain checker: %v\n%s", err, bytes.TrimSpace(out))
	}

	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if len(generated) == 0 {
		t.Fatalf("generated file is empty")
	}
	if !bytes.Contains(generated, []byte("func frontendCheckSourceStructured(")) {
		t.Fatalf("generated file does not include checker entrypoints")
	}
	if !bytes.Contains(generated, []byte("func elabInfer(")) {
		t.Fatalf("generated file does not include elaborator entrypoints")
	}
}

func mergeToolchainCheckerSources(root string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(toolchainCheckerStringsPrelude)
	b.WriteByte('\n')

	for _, rel := range toolchainCheckerSourceFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		b.WriteString("// ---- ")
		b.WriteString(rel)
		b.WriteString(" ----\n")
		trimmed := stripToolchainCheckerStringsUse(string(data))
		trimmed = normalizeToolchainCheckerStdStringsCalls(trimmed)
		b.WriteString(trimmed)
		if !strings.HasSuffix(trimmed, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return []byte(b.String()), nil
}

func stripToolchainCheckerStringsUse(src string) string {
	const goPrefix = `use go "strings" as strings {`
	const stdPrefix = "use std.strings as strings"

	lines := strings.SplitAfter(src, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(trimmed, goPrefix):
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "}" {
					return strings.Join(lines[:i], "") + strings.Join(lines[j+1:], "")
				}
			}
			return src
		case trimmed == stdPrefix:
			return strings.Join(lines[:i], "") + strings.Join(lines[i+1:], "")
		}
	}

	return src
}

func normalizeToolchainCheckerStdStringsCalls(src string) string {
	for _, pair := range [][2]string{
		{"strings.split(", "strings.Split("},
		{"strings.join(", "strings.Join("},
		{"strings.hasPrefix(", "strings.HasPrefix("},
		{"strings.hasSuffix(", "strings.HasSuffix("},
		{"strings.trimSpace(", "strings.TrimSpace("},
		{"strings.replaceAll(", "strings.ReplaceAll("},
		{"strings.trimPrefix(", "strings.TrimPrefix("},
		{"strings.trimSuffix(", "strings.TrimSuffix("},
		{"strings.repeat(", "strings.Repeat("},
		{"strings.count(", "strings.Count("},
		{"strings.fields(", "strings.Fields("},
		{"strings.splitN(", "strings.SplitN("},
	} {
		src = strings.ReplaceAll(src, pair[0], pair[1])
	}
	return src
}
