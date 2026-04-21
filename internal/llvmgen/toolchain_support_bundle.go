package llvmgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var toolchainSupportFiles = []string{
	"toolchain/llvmgen.osty",
}

const llvmgenStringsPrelude = `use go "strings" as llvmStrings {
    fn Contains(s: String, substr: String) -> Bool
    fn HasPrefix(s: String, prefix: String) -> Bool
    fn Join(elems: List<String>, sep: String) -> String
    fn Split(s: String, sep: String) -> List<String>
    fn TrimPrefix(s: String, prefix: String) -> String
}

fn ostyStringsContains(s: String, substr: String) -> Bool { llvmStrings.Contains(s, substr) }
fn ostyStringsHasPrefix(s: String, prefix: String) -> Bool { llvmStrings.HasPrefix(s, prefix) }
`

// ToolchainSupportFiles returns the Osty-authored llvmgen helper sources that
// can be transpiled through the bootstrap path today.
func ToolchainSupportFiles() []string {
	return append([]string(nil), toolchainSupportFiles...)
}

// MergeToolchainSupport prepends the minimal Go-hosted strings surface the
// bootstrap transpiler needs, then concatenates the llvmgen helper sources into
// one standalone .osty file.
func MergeToolchainSupport(root string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(llvmgenStringsPrelude)
	b.WriteByte('\n')

	for _, rel := range toolchainSupportFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		b.WriteString("// ---- ")
		b.WriteString(rel)
		b.WriteString(" ----\n")
		trimmed := stripLeadingLlvmgenStringsUse(string(data))
		trimmed = normalizeLlvmgenStdStringsCalls(trimmed)
		b.WriteString(trimmed)
		if !strings.HasSuffix(trimmed, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return []byte(b.String()), nil
}

func stripLeadingLlvmgenStringsUse(src string) string {
	const prefix = "use std.strings as llvmStrings"

	lines := strings.SplitAfter(src, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != prefix {
			continue
		}
		return strings.Join(lines[:i], "") + strings.Join(lines[i+1:], "")
	}
	return src
}

func normalizeLlvmgenStdStringsCalls(src string) string {
	for _, pair := range [][2]string{
		{"llvmStrings.join(", "llvmStrings.Join("},
		{"llvmStrings.split(", "llvmStrings.Split("},
		{"llvmStrings.trimPrefix(", "llvmStrings.TrimPrefix("},
		{"target.contains(", "ostyStringsContains(target, "},
		{"path.startsWith(", "ostyStringsHasPrefix(path, "},
		{"trimmed.startsWith(", "ostyStringsHasPrefix(trimmed, "},
	} {
		src = strings.ReplaceAll(src, pair[0], pair[1])
	}
	return src
}
