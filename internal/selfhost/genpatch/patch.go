package genpatch

import (
	"fmt"
	"strings"
)

// NormalizeGeneratedSourceComment rewrites the source file markers that
// bootstrap-gen emits so the generated file stays byte-stable across hosts.
func NormalizeGeneratedSourceComment(src string) string {
	const (
		topPrefix  = "// Osty source: "
		declPrefix = "// Osty: "
		mergedFile = "selfhost_merged.osty"
		canonical  = "/tmp/" + mergedFile
	)
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		if strings.HasPrefix(trimmed, topPrefix) && strings.HasSuffix(trimmed, mergedFile) {
			lines[i] = indent + topPrefix + canonical
			continue
		}
		if !strings.HasPrefix(trimmed, declPrefix) {
			continue
		}
		idx := strings.Index(trimmed, mergedFile)
		if idx < 0 {
			continue
		}
		lines[i] = indent + declPrefix + canonical + trimmed[idx+len(mergedFile):]
	}
	return strings.Join(lines, "\n")
}

// ReplaceGeneratedFunction swaps one generated Go function by name while
// correctly ignoring braces that appear inside comments or string literals.
func ReplaceGeneratedFunction(src, name, replacement string) (string, error) {
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
	inLineComment := false
	inBlockComment := false
	inString := false
	inRune := false
	inRawString := false
	escaped := false
	for i := open; i < len(src); i++ {
		if inLineComment {
			if src[i] == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if src[i] == '\\' {
				escaped = true
				continue
			}
			if src[i] == '"' {
				inString = false
			}
			continue
		}
		if inRune {
			if escaped {
				escaped = false
				continue
			}
			if src[i] == '\\' {
				escaped = true
				continue
			}
			if src[i] == '\'' {
				inRune = false
			}
			continue
		}
		if inRawString {
			if src[i] == '`' {
				inRawString = false
			}
			continue
		}
		if src[i] == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				inLineComment = true
				i++
				continue
			}
			if src[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}
		if src[i] == '"' {
			inString = true
			continue
		}
		if src[i] == '\'' {
			inRune = true
			continue
		}
		if src[i] == '`' {
			inRawString = true
			continue
		}
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
