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
	"examples/dogfood/semver.osty",
	"examples/dogfood/semver_parse.osty",
	"examples/dogfood/frontend.osty",
	"examples/dogfood/parser.osty",
}

const mergedPath = "/tmp/selfhost_merged.osty"

const stringsPrelude = `use go "strings" as strings {
    fn Count(s: String, substr: String) -> Int
    fn Fields(s: String) -> List<String>
    fn HasPrefix(s: String, prefix: String) -> Bool
    fn HasSuffix(s: String, suffix: String) -> Bool
    fn Join(elems: List<String>, sep: String) -> String
    fn Repeat(s: String, count: Int) -> String
    fn Split(s: String, sep: String) -> List<String>
    fn SplitN(s: String, sep: String, n: Int) -> List<String>
    fn TrimPrefix(s: String, prefix: String) -> String
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
	merged, err := mergedSource(root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		return fmt.Errorf("write merged selfhost source: %w", err)
	}

	outPath := filepath.Join(root, "internal/selfhost/generated.go")
	cmd := exec.Command("go", "run", "./cmd/osty", "gen", "--package", "selfhost", "-o", outPath, mergedPath)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate selfhost parser: %w\n%s", err, bytes.TrimSpace(output))
	}
	return patchGenerated(outPath)
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
		b.WriteString(trimmed)
		if !strings.HasSuffix(trimmed, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func stripLeadingStringsUse(src string) string {
	const prefix = `use go "strings" as strings {`
	if !strings.HasPrefix(strings.TrimLeft(src, "\ufeff \t\r\n"), prefix) {
		return src
	}
	lines := strings.SplitAfter(src, "\n")
	started := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !started {
			if strings.HasPrefix(trimmed, prefix) {
				started = true
			}
			continue
		}
		if trimmed == "}" {
			return strings.Join(lines[i+1:], "")
		}
	}
	return src
}

func patchGenerated(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated selfhost code: %w", err)
	}
	src := string(data)
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
