//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var sourceFiles = []string{
	"examples/selfhost-core/semver.osty",
	"examples/selfhost-core/semver_parse.osty",
	"examples/selfhost-core/frontend.osty",
	"toolchain/lexer.osty",
	"toolchain/parser.osty",
	"examples/selfhost-core/formatter_ast.osty",
	"examples/selfhost-core/check_bridge.osty",
	"examples/selfhost-core/check.osty",
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
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	outPath := filepath.Join(root, "internal/selfhost/generated.go")
	if upToDate, err := generatedSelfhostUpToDate(root, outPath); err == nil && upToDate {
		return nil
	} else if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "osty-selfhostgen-*")
	if err != nil {
		return fmt.Errorf("create selfhost temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	merged, err := mergedSource(root)
	if err != nil {
		return err
	}
	mergedPath := filepath.Join(tmpDir, "selfhost_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		return fmt.Errorf("write merged selfhost source: %w", err)
	}
	tmpOutPath := filepath.Join(tmpDir, "generated.go")

	cmd := exec.Command(
		"go", "run", "-tags", "selfhostgen", "./cmd/osty", "gen",
		"--backend", "go",
		"--emit", "go",
		"--package", "selfhost",
		"-o", tmpOutPath,
		mergedPath,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate selfhost parser: %w\n%s", err, bytes.TrimSpace(output))
	}
	if selfhostgenMissingTypes(output) {
		return fmt.Errorf(
			"generate selfhost parser: selfhostgen emitted untyped bootstrap Go; refusing to overwrite %s\n%s",
			outPath,
			bytes.TrimSpace(output),
		)
	}
	if err := patchGenerated(tmpOutPath); err != nil {
		return err
	}
	data, err := os.ReadFile(tmpOutPath)
	if err != nil {
		return fmt.Errorf("read patched selfhost code: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("install generated selfhost code: %w", err)
	}
	return nil
}

func selfhostgenMissingTypes(output []byte) bool {
	return bytes.Contains(output, []byte("osty gen: warning: native type checking is unavailable"))
}

func generatedSelfhostUpToDate(root, outPath string) (bool, error) {
	outInfo, err := os.Stat(outPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat generated selfhost code: %w", err)
	}
	newest := outInfo.ModTime()
	checkPath := func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	}
	if err := checkPath(filepath.Join(root, "internal/selfhost/gen_selfhost.go")); err != nil {
		return false, fmt.Errorf("stat selfhost generator: %w", err)
	}
	for _, rel := range sourceFiles {
		if err := checkPath(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return false, fmt.Errorf("stat %s: %w", rel, err)
		}
	}
	return !outInfo.ModTime().Before(newest), nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", dir)
		}
		dir = parent
	}
}

func mergedSource(root string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(stringsPrelude)
	b.WriteByte('\n')
	for _, rel := range sourceFiles {
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
		b.WriteString(trimmed)
		if !strings.HasSuffix(trimmed, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func stripLeadingStringsUse(src string) string {
	trimmed := strings.TrimLeft(src, "\ufeff \t\r\n")

	// Handle Go FFI form: use go "strings" as strings { ... }
	const goPrefix = `use go "strings" as strings {`
	if strings.HasPrefix(trimmed, goPrefix) {
		lines := strings.SplitAfter(src, "\n")
		started := false
		for i, line := range lines {
			t := strings.TrimSpace(line)
			if !started {
				if strings.HasPrefix(t, goPrefix) {
					started = true
				}
				continue
			}
			if t == "}" {
				return strings.Join(lines[i+1:], "")
			}
		}
		return src
	}

	// Handle std.strings form: use std.strings as strings  (single line)
	const stdPrefix = "use std.strings as strings"
	if strings.HasPrefix(trimmed, stdPrefix) {
		idx := strings.Index(src, stdPrefix)
		end := strings.IndexByte(src[idx:], '\n')
		if end >= 0 {
			return src[:idx] + src[idx+end+1:]
		}
		return src[:idx]
	}

	return src
}

// normalizeStdStringsCalls rewrites camelCase strings.xxx() calls (from
// std.strings) to PascalCase so they match the Go FFI declarations in
// stringsPrelude. Only applies to files that originally used std.strings.
func normalizeStdStringsCalls(src string) string {
	// Only touch files that had the std.strings import (now stripped).
	// We detect this by checking whether any camelCase strings.* call appears.
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

func patchGenerated(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated selfhost code: %w", err)
	}
	src := string(data)
	src = normalizeGeneratedSourceComment(src)
	for _, fn := range []struct {
		name string
		body string
	}{
		{name: "frontPositionAt", body: frontPositionAtReplacement},
		{name: "frontUnitAt", body: frontUnitAtReplacement},
	} {
		var err error
		src, err = replaceGeneratedFunction(src, fn.name, fn.body)
		if err != nil {
			return err
		}
	}
	if strings.Contains(src, "sync.Mutex") && !strings.Contains(src, "\n\t\"sync\"\n") {
		src = strings.Replace(src, "\n\t\"strings\"\n", "\n\t\"strings\"\n\t\"sync\"\n", 1)
	}
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format generated selfhost code: %w", err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("write generated selfhost code: %w", err)
	}
	return nil
}

func normalizeGeneratedSourceComment(src string) string {
	const prefix = "// Osty source: "
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "selfhost_merged.osty") {
			lines[i] = prefix + "/tmp/selfhost_merged.osty"
			break
		}
	}
	return strings.Join(lines, "\n")
}

func replaceGeneratedFunction(src, name, replacement string) (string, error) {
	start := strings.Index(src, "func "+name+"(")
	if start < 0 {
		return "", fmt.Errorf("generated function %s not found", name)
	}
	open := strings.IndexByte(src[start:], '{')
	if open < 0 {
		return "", fmt.Errorf("generated function %s has no body", name)
	}
	open += start
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[:start] + replacement + src[i+1:], nil
			}
		}
	}
	return "", fmt.Errorf("generated function %s body is unterminated", name)
}

const frontPositionAtReplacement = `type frontPositionCacheState struct {
	units  []string
	target int
	offset int
	line   int
	column int
	skipLf bool
}

var frontPositionCacheMu sync.Mutex
var frontPositionCache frontPositionCacheState

func frontSameUnits(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}

func frontPositionAt(units []string, target int) *FrontPos {
	if target < 0 {
		target = 0
	}
	if target > len(units) {
		target = len(units)
	}

	frontPositionCacheMu.Lock()
	defer frontPositionCacheMu.Unlock()

	if !frontSameUnits(frontPositionCache.units, units) || target < frontPositionCache.target {
		frontPositionCache = frontPositionCacheState{
			units:  units,
			target: 0,
			offset: 0,
			line:   1,
			column: 1,
			skipLf: false,
		}
	}

	offset := frontPositionCache.offset
	line := frontPositionCache.line
	column := frontPositionCache.column
	skipLf := frontPositionCache.skipLf
	for idx := frontPositionCache.target; idx < target; idx++ {
		unit := units[idx]
		next := ""
		if idx+1 < len(units) {
			next = units[idx+1]
		}
		if skipLf {
			skipLf = false
			offset = offset + 1
		} else if unit == "\r" {
			line = line + 1
			column = 1
			offset = offset + 1
			if next == "\n" {
				skipLf = true
			}
		} else if unit == "\n" {
			line = line + 1
			column = 1
			offset = offset + 1
		} else {
			column = column + 1
			offset = offset + 1
		}
	}
	frontPositionCache.target = target
	frontPositionCache.offset = offset
	frontPositionCache.line = line
	frontPositionCache.column = column
	frontPositionCache.skipLf = skipLf
	return frontPos(offset, line, column)
}
`

const frontUnitAtReplacement = `func frontUnitAt(units []string, target int) string {
	if target < 0 || target >= len(units) {
		return ""
	}
	return units[target]
}
`
