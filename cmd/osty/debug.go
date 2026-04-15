package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type goFailureReport struct {
	Tool      string
	Action    string
	Args      []string
	WorkDir   string
	Generated []string
	Source    string
	Stderr    string
	Err       error
}

type ostyLocation struct {
	Path   string
	Line   int
	Column int
}

type mappedGoLocation struct {
	GoPath       string
	GoLine       int
	GoColumn     int
	MarkerGoLine int
	Osty         ostyLocation
}

var goLocationRE = regexp.MustCompile(`([^[:space:]()]+\.go):([0-9]+)(?::([0-9]+))?`)

func reportTranspileWarning(tool, sourcePath, generatedPath string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s: transpile warning: %v\n", tool, err)
	if sourcePath != "" {
		fmt.Fprintf(os.Stderr, "  source: %s\n", sourcePath)
	}
	if generatedPath != "" {
		fmt.Fprintf(os.Stderr, "  generated Go: %s\n", generatedPath)
	}
	if sourcePath != "" && generatedPath != "" {
		fmt.Fprintf(os.Stderr, "  reproduce: osty gen %s -o %s\n",
			shellQuote(sourcePath), shellQuote(generatedPath))
	}
}

func reportGoFailure(r goFailureReport) bool {
	category, explanation := classifyGoFailure(r.Stderr)
	mapped, hasMapping := firstMappedGoLocation(r.Stderr, r.Generated, r.WorkDir)
	if category == "" && !hasMapping {
		return false
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s: %s failed", r.Tool, r.Action)
	if r.Err != nil {
		fmt.Fprintf(os.Stderr, ": %v", r.Err)
	}
	fmt.Fprintln(os.Stderr)
	if category != "" {
		fmt.Fprintf(os.Stderr, "  category: %s\n", category)
	}
	if explanation != "" {
		fmt.Fprintf(os.Stderr, "  note: %s\n", explanation)
	}
	if panicLine := firstLineContaining(r.Stderr, "panic:"); panicLine != "" {
		fmt.Fprintf(os.Stderr, "  panic: %s\n", strings.TrimSpace(panicLine))
	}
	if hasMapping {
		delta := mapped.GoLine - mapped.MarkerGoLine
		suffix := ""
		if delta > 0 {
			suffix = fmt.Sprintf(" (nearest marker, +%d generated line(s))", delta)
		}
		fmt.Fprintf(os.Stderr, "  Osty source: %s:%d:%d%s\n",
			mapped.Osty.Path, mapped.Osty.Line, mapped.Osty.Column, suffix)
		fmt.Fprintf(os.Stderr, "  generated Go: %s:%d", mapped.GoPath, mapped.GoLine)
		if mapped.GoColumn > 0 {
			fmt.Fprintf(os.Stderr, ":%d", mapped.GoColumn)
		}
		fmt.Fprintln(os.Stderr)
	} else if len(r.Generated) > 0 {
		fmt.Fprintf(os.Stderr, "  generated Go: %s\n", strings.Join(r.Generated, ", "))
	}
	if r.Source != "" {
		fmt.Fprintf(os.Stderr, "  source: %s\n", r.Source)
	}
	if len(r.Args) > 0 {
		fmt.Fprintf(os.Stderr, "  reproduce: (cd %s && %s)\n",
			shellQuote(workDirOrDot(r.WorkDir)), shellJoin(r.Args))
	}
	return true
}

func classifyGoFailure(stderr string) (category, explanation string) {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "panic:"):
		return "runtime panic",
			"the generated program panicked while running; use the Osty source line below with the stack trace above"
	case strings.Contains(s, "no required module provides package") ||
		strings.Contains(s, "cannot find package") ||
		(strings.Contains(s, "package ") && strings.Contains(s, " is not in std")):
		return "package/import",
			"a generated Go import could not be resolved; check `use go` import paths, vendored deps, and run from the project root"
	case strings.Contains(s, "imported and not used"):
		return "package/import",
			"Go rejected an emitted import; inspect the generated file and the Osty `use` that introduced it"
	case strings.Contains(s, "syntax error:") || strings.Contains(s, "expected "):
		return "transpile output",
			"the transpiler emitted Go that the Go parser rejected; inspect the generated file around the reported line"
	case strings.Contains(s, "undefined:") || strings.Contains(s, "cannot use ") ||
		strings.Contains(s, "not enough arguments") || strings.Contains(s, "too many arguments") ||
		strings.Contains(s, "assignment mismatch") || strings.Contains(s, "multiple-value"):
		return "generated Go type/check",
			"Go type-checking failed after Osty front-end checks passed; inspect the generated symbol near the mapped source line"
	case goLocationRE.MatchString(stderr):
		return "generated Go compile",
			"the Go toolchain reported a generated-file location; the nearest Osty marker is shown below when available"
	default:
		return "", ""
	}
}

func firstMappedGoLocation(output string, generated []string, workDir string) (mappedGoLocation, bool) {
	matches := goLocationRE.FindAllStringSubmatch(output, -1)
	for _, m := range matches {
		raw := strings.TrimPrefix(m[1], "./")
		line, _ := strconv.Atoi(m[2])
		col := 0
		if len(m) > 3 && m[3] != "" {
			col, _ = strconv.Atoi(m[3])
		}
		goPath := resolveGeneratedPath(raw, generated, workDir)
		if goPath == "" || line <= 0 {
			continue
		}
		loc, markerLine, ok := nearestOstyMarker(goPath, line)
		if !ok {
			continue
		}
		return mappedGoLocation{
			GoPath:       goPath,
			GoLine:       line,
			GoColumn:     col,
			MarkerGoLine: markerLine,
			Osty:         loc,
		}, true
	}
	return mappedGoLocation{}, false
}

func resolveGeneratedPath(raw string, generated []string, workDir string) string {
	var candidates []string
	if filepath.IsAbs(raw) {
		candidates = append(candidates, raw)
	} else {
		if workDir != "" {
			candidates = append(candidates, filepath.Join(workDir, raw))
		}
		candidates = append(candidates, raw)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	base := filepath.Base(raw)
	for _, g := range generated {
		if filepath.Base(g) != base {
			continue
		}
		if _, err := os.Stat(g); err == nil {
			if abs, err := filepath.Abs(g); err == nil {
				return abs
			}
			return g
		}
	}
	return ""
}

func nearestOstyMarker(goPath string, goLine int) (ostyLocation, int, bool) {
	f, err := os.Open(goPath)
	if err != nil {
		return ostyLocation{}, 0, false
	}
	defer f.Close()

	var (
		best     ostyLocation
		bestLine int
		lineNo   int
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNo++
		if lineNo > goLine {
			break
		}
		if loc, ok := parseOstyMarker(scanner.Text()); ok {
			best = loc
			bestLine = lineNo
		}
	}
	if bestLine == 0 {
		return ostyLocation{}, 0, false
	}
	return best, bestLine, true
}

func parseOstyMarker(line string) (ostyLocation, bool) {
	const prefix = "// Osty:"
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return ostyLocation{}, false
	}
	body := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	last := strings.LastIndex(body, ":")
	if last < 0 {
		return ostyLocation{}, false
	}
	col, err := strconv.Atoi(body[last+1:])
	if err != nil {
		return ostyLocation{}, false
	}
	body = body[:last]
	prev := strings.LastIndex(body, ":")
	if prev < 0 {
		return ostyLocation{}, false
	}
	lineNo, err := strconv.Atoi(body[prev+1:])
	if err != nil {
		return ostyLocation{}, false
	}
	path := body[:prev]
	if path == "" {
		return ostyLocation{}, false
	}
	return ostyLocation{Path: path, Line: lineNo, Column: col}, true
}

func firstLineContaining(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func workDirOrDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

func shellJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$&;()<>|*?[]{}!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
