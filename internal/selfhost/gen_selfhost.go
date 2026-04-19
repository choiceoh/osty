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

	"github.com/osty/osty/internal/selfhost/bundle"
)

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
	tmpDir, err := os.MkdirTemp("", "osty-bootstrap-gen-*")
	if err != nil {
		return fmt.Errorf("create selfhost temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	merged, err := bundle.MergeToolchainChecker(root)
	if err != nil {
		return err
	}
	mergedPath := filepath.Join(tmpDir, "selfhost_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		return fmt.Errorf("write merged selfhost source: %w", err)
	}
	tmpOutPath := filepath.Join(tmpDir, "generated.go")
	checkerPath := filepath.Join(tmpDir, "osty-native-checker")
	if err := buildNativeChecker(root, checkerPath); err != nil {
		return err
	}

	cmd := exec.Command(
		"go", "run", "./cmd/osty-bootstrap-gen",
		"--package", "selfhost",
		"-o", tmpOutPath,
		mergedPath,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "OSTY_NATIVE_CHECKER_BIN="+checkerPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate selfhost parser: %w\n%s", err, bytes.TrimSpace(output))
	}
	if bootstrapGenMissingTypes(output) {
		return fmt.Errorf(
			"generate selfhost parser: bootstrap gen emitted untyped Go; refusing to overwrite %s\n%s",
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

func buildNativeChecker(root, outPath string) error {
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/osty-native-checker")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build native checker: %w\n%s", err, bytes.TrimSpace(output))
	}
	return nil
}

func bootstrapGenMissingTypes(output []byte) bool {
	return bytes.Contains(output, []byte("osty-bootstrap-gen: warning: native type checking is unavailable"))
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
	for _, rel := range bundle.ToolchainCheckerFiles() {
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
		{name: "frontTokenListCount", body: frontTokenListCountReplacement},
		{name: "frontTokenAtList", body: frontTokenAtListReplacement},
		{name: "astArenaNodeCount", body: astArenaNodeCountReplacement},
		{name: "astArenaNodeAt", body: astArenaNodeAtReplacement},
		{name: "frontLexTokenAt", body: frontLexTokenAtReplacement},
		{name: "frontLexDiagnosticAt", body: frontLexDiagnosticAtReplacement},
		{name: "frontCommentAt", body: frontCommentAtReplacement},
		{name: "frontStringPartAt", body: frontStringPartAtReplacement},
		{name: "frontInterpolationTokenAt", body: frontInterpolationTokenAtReplacement},
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

const frontTokenListCountReplacement = `func frontTokenListCount(tokens []*FrontToken) int {
	return len(tokens)
}
`

const frontTokenAtListReplacement = `func frontTokenAtList(tokens []*FrontToken, target int) *FrontToken {
	if target < 0 || target >= len(tokens) {
		return frontEOF()
	}
	return tokens[target]
}
`

const astArenaNodeCountReplacement = `func astArenaNodeCount(arena *AstArena) int {
	return len(arena.nodes)
}
`

const astArenaNodeAtReplacement = `func astArenaNodeAt(arena *AstArena, idx int) *AstNode {
	if idx < 0 || idx >= len(arena.nodes) {
		return emptyAstNode(AstNodeKind(&AstNodeKind_AstNError{}))
	}
	return arena.nodes[idx]
}
`

const frontLexTokenAtReplacement = `func frontLexTokenAt(stream *FrontLexStream, target int) *FrontLexToken {
	if target < 0 || target >= len(stream.tokens) {
		return emptyFrontLexToken()
	}
	return stream.tokens[target]
}
`

const frontLexDiagnosticAtReplacement = `func frontLexDiagnosticAt(stream *FrontLexStream, target int) *FrontLexDiagnostic {
	if target < 0 || target >= len(stream.diagnostics) {
		return emptyFrontLexDiagnostic()
	}
	return stream.diagnostics[target]
}
`

const frontCommentAtReplacement = `func frontCommentAt(stream *FrontLexStream, target int) *FrontComment {
	if target < 0 || target >= len(stream.comments) {
		return emptyFrontComment()
	}
	return stream.comments[target]
}
`

const frontStringPartAtReplacement = `func frontStringPartAt(stream *FrontLexStream, target int) *FrontStringPart {
	if target < 0 || target >= len(stream.stringParts) {
		return emptyFrontStringPart()
	}
	return stream.stringParts[target]
}
`

const frontInterpolationTokenAtReplacement = `func frontInterpolationTokenAt(stream *FrontLexStream, target int) *FrontInterpolationToken {
	if target < 0 || target >= len(stream.interpolationTokens) {
		return emptyFrontInterpolationToken()
	}
	return stream.interpolationTokens[target]
}
`
