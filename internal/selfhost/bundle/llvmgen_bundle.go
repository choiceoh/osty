package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var toolchainLLVMGenFiles = []string{
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

// ToolchainLLVMGenFiles returns the Osty-authored llvmgen helper sources that
// can already travel through the bootstrap transpiler unchanged.
func ToolchainLLVMGenFiles() []string {
	return append([]string(nil), toolchainLLVMGenFiles...)
}

// MergeToolchainLLVMGen prepends the minimal Go-hosted strings surface the
// bootstrap transpiler needs, then concatenates the llvmgen helper sources into
// one standalone .osty file.
func MergeToolchainLLVMGen(root string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(llvmgenStringsPrelude)
	b.WriteByte('\n')

	for _, rel := range toolchainLLVMGenFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		b.WriteString("// ---- ")
		b.WriteString(rel)
		b.WriteString(" ----\n")
		trimmed := stripLeadingLLVMGenStringsUse(string(data))
		trimmed = normalizeLLVMGenStdStringsCalls(trimmed)
		b.WriteString(trimmed)
		if !strings.HasSuffix(trimmed, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return []byte(b.String()), nil
}

func stripLeadingLLVMGenStringsUse(src string) string {
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

func normalizeLLVMGenStdStringsCalls(src string) string {
	for _, pair := range [][2]string{
		{"llvmStrings.hasPrefix(", "llvmStrings.HasPrefix("},
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
