// airepair_source_policy.go is the Go snapshot of
// toolchain/airepair_source.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import (
	"bytes"
	"strings"
)

// SourceLine mirrors toolchain/airepair_source.osty's SourceLine.
// Field order matches the Osty struct so the drift test can
// confirm parity.
//
// Osty: toolchain/airepair_source.osty:34
type SourceLine struct {
	Start      int
	Text       string
	Raw        string
	Indent     string
	Trimmed    string
	HasNewline bool
	LineNo     int
}

// SplitSourceLines walks `src` byte-by-byte and produces one
// SourceLine per line, splitting on `\n`. Empty input returns
// nil. The last line is reported even when it has no trailing
// newline (with HasNewline=false).
//
// Indent extraction only counts ASCII space (0x20) and tab
// (0x09); other whitespace bytes are left in the body.
//
// Osty: toolchain/airepair_source.osty:50
func SplitSourceLines(src []byte) []SourceLine {
	if len(src) == 0 {
		return nil
	}
	var lines []SourceLine
	start := 0
	lineNo := 1
	for start < len(src) {
		end := start
		for end < len(src) && src[end] != '\n' {
			end++
		}
		rawEnd := end
		hasNewline := false
		if end < len(src) && src[end] == '\n' {
			rawEnd = end + 1
			hasNewline = true
		}
		text := string(src[start:end])
		indentEnd := 0
		for indentEnd < len(text) {
			if text[indentEnd] != ' ' && text[indentEnd] != '\t' {
				break
			}
			indentEnd++
		}
		lines = append(lines, SourceLine{
			Start:      start,
			Text:       text,
			Raw:        string(src[start:rawEnd]),
			Indent:     text[:indentEnd],
			Trimmed:    strings.TrimSpace(text),
			HasNewline: hasNewline,
			LineNo:     lineNo,
		})
		start = rawEnd
		lineNo++
	}
	return lines
}

// IsIgnorablePythonLine reports whether a `trimmed` source line
// is "noise" the dispatchers should skip with a verbatim copy.
// The two recognised noise prefixes are line comments (`//`) and
// Python-style `#` lines.
//
// Osty: toolchain/airepair_source.osty:97
func IsIgnorablePythonLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")
}

// SrcWantsForeignLoopRepair reports whether `src` contains any
// of the byte markers the foreign-loop rewriter knows how to
// transform.
//
// Osty: toolchain/airepair_source.osty:103
func SrcWantsForeignLoopRepair(src []byte) bool {
	if !bytes.Contains(src, []byte("for ")) {
		return false
	}
	return bytes.Contains(src, []byte(" of ")) ||
		bytes.Contains(src, []byte("range(")) ||
		bytes.Contains(src, []byte("enumerate("))
}

// SrcWantsSemanticRepair reports whether `src` contains any of
// the byte markers the semantic rewriter family needs.
//
// Osty: toolchain/airepair_source.osty:114
func SrcWantsSemanticRepair(src []byte) bool {
	return bytes.Contains(src, []byte(".enumerate()")) ||
		bytes.Contains(src, []byte("append(")) ||
		bytes.Contains(src, []byte("len(")) ||
		bytes.Contains(src, []byte(".length"))
}

// SrcWantsTupleLoopRepair reports whether `src` looks like it
// might contain a bare-tuple `for` header. Requires all three of
// `for `, `,`, and ` in `.
//
// Osty: toolchain/airepair_source.osty:124
func SrcWantsTupleLoopRepair(src []byte) bool {
	return bytes.Contains(src, []byte("for ")) &&
		bytes.Contains(src, []byte(",")) &&
		bytes.Contains(src, []byte(" in "))
}

// SrcWantsPythonColonBlockRepair reports whether `src` contains
// the `:\n` byte sequence — the universal marker for
// Python-style colon-terminated block headers.
//
// Osty: toolchain/airepair_source.osty:134
func SrcWantsPythonColonBlockRepair(src []byte) bool {
	return bytes.Contains(src, []byte(":\n"))
}
