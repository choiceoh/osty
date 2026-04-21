package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var toolchainCheckerFiles = []string{
	"examples/selfhost-core/semver.osty",
	"examples/selfhost-core/semver_parse.osty",
	"toolchain/frontend.osty",
	"toolchain/lexer.osty",
	"toolchain/parser.osty",
	"examples/selfhost-core/formatter_ast.osty",
	"toolchain/check_bridge.osty",
	"toolchain/diag_manifest.osty",
	"toolchain/diag_examples.osty",
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

const stringsPrelude = `use go "strings" as strings {
    fn Count(s: String, substr: String) -> Int
    fn Fields(s: String) -> List<String>
    fn HasPrefix(s: String, prefix: String) -> Bool
    fn HasSuffix(s: String, suffix: String) -> Bool
    fn Join(elems: List<String>, sep: String) -> String
    fn ReplaceAll(s: String, old: String, new: String) -> String
    fn Repeat(s: String, count: Int) -> String
    fn Split(s: String, sep: String) -> List<String>
    fn SplitN(s: String, sep: String, n: Int) -> List<String>
    fn TrimPrefix(s: String, prefix: String) -> String
    fn TrimSpace(s: String) -> String
    fn TrimSuffix(s: String, suffix: String) -> String
}

fn ostyStringsConcat(a: String, b: String) -> String { a + b }
fn ostyStringsChars(s: String) -> List<Char> { s.chars() }
`

func ToolchainCheckerFiles() []string {
	return append([]string(nil), toolchainCheckerFiles...)
}

func MergeToolchainChecker(root string) ([]byte, error) {
	return MergeFiles(root, toolchainCheckerFiles)
}

func MergeFiles(root string, files []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(stringsPrelude)
	b.WriteByte('\n')

	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		b.WriteString("// ---- ")
		b.WriteString(rel)
		b.WriteString(" ----\n")
		trimmed := stripLeadingStringsUse(string(data))
		trimmed = normalizeStdStringsCalls(trimmed)
		trimmed = normalizeWhileLoops(trimmed)
		b.WriteString(trimmed)
		if !strings.HasSuffix(trimmed, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return []byte(b.String()), nil
}

func stripLeadingStringsUse(src string) string {
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

func normalizeStdStringsCalls(src string) string {
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
		// std.strings extras not covered by the Go `strings` package: route
		// through Osty shims defined at the top of the merged bundle.
		{"strings.concat(", "ostyStringsConcat("},
		{"strings.chars(", "ostyStringsChars("},
	} {
		src = strings.ReplaceAll(src, pair[0], pair[1])
	}
	return src
}

// normalizeWhileLoops lowers checker-side `while cond { ... }` sugar to the
// bootstrap parser's existing `for cond { ... }` surface.
func normalizeWhileLoops(src string) string {
	lines := strings.SplitAfter(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "while ") || !strings.HasSuffix(trimmed, "{") {
			continue
		}
		idx := strings.Index(line, "while ")
		if idx < 0 {
			continue
		}
		lines[i] = line[:idx] + "for " + line[idx+len("while "):]
	}
	return strings.Join(lines, "")
}
