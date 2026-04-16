package airepair

import (
	"bytes"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/token"
)

type sourceLine struct {
	start      int
	text       string
	raw        string
	indent     string
	trimmed    string
	hasNewline bool
	lineNo     int
}

type pythonScopeKind int

const (
	pythonScopeBlock pythonScopeKind = iota
	pythonScopeMatch
	pythonScopeMatchArm
)

type pythonBlockScope struct {
	kind         pythonScopeKind
	headerIndent string
	bodyIndent   string
}

type pythonColonHeader struct {
	rewritten     string
	changeKind    string
	message       string
	scopeKind     pythonScopeKind
	elseish       bool
	requiresMatch bool
}

func diagnosticGuidedSource(src []byte, diags []*diag.Diagnostic) repair.Result {
	if !wantsPythonColonBlockRepair(src, diags) {
		return repair.Result{Source: src}
	}
	out, changes, ok := rewritePythonColonBlocks(src)
	if !ok || len(changes) == 0 {
		return repair.Result{Source: src}
	}
	return repair.Result{
		Source:  out,
		Changes: changes,
	}
}

func wantsPythonColonBlockRepair(src []byte, diags []*diag.Diagnostic) bool {
	if !bytes.Contains(src, []byte(":\n")) {
		return false
	}
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func rewritePythonColonBlocks(src []byte) ([]byte, []repair.Change, bool) {
	lines := splitSourceLines(src)
	if len(lines) == 0 {
		return src, nil, false
	}

	var (
		out     strings.Builder
		changes []repair.Change
		stack   []pythonBlockScope
		changed bool
	)

	for _, line := range lines {
		if line.trimmed == "" || isIgnorablePythonLine(line.trimmed) {
			out.WriteString(line.raw)
			continue
		}

		var (
			closings []pythonBlockScope
			ok       bool
		)
		stack, closings, ok = preparePythonBlockClosings(stack, line.indent)
		if !ok {
			return src, nil, false
		}

		header, headerOK := rewritePythonColonHeader(line.trimmed)
		if headerOK && header.elseish {
			headerOK = len(closings) > 0 && closings[len(closings)-1].kind == pythonScopeBlock
		}
		if headerOK && header.requiresMatch {
			headerOK = len(stack) > 0 && stack[len(stack)-1].kind == pythonScopeMatch
		}
		if headerOK {
			writePythonClosings(&out, closings, !header.elseish)
			if header.elseish && len(closings) > 0 {
				out.WriteString(line.indent)
				out.WriteString("} ")
				out.WriteString(header.rewritten)
			} else {
				out.WriteString(line.indent)
				out.WriteString(header.rewritten)
			}
			if line.hasNewline {
				out.WriteByte('\n')
			}
			stack = append(stack, pythonBlockScope{
				kind:         header.scopeKind,
				headerIndent: line.indent,
			})
			changes = append(changes, repair.Change{
				Kind:    header.changeKind,
				Message: header.message,
				Pos: token.Pos{
					Offset: line.start + len(line.indent),
					Line:   line.lineNo,
					Column: len([]rune(line.indent)) + 1,
				},
			})
			changed = true
			continue
		}

		writePythonClosings(&out, closings, true)
		out.WriteString(line.raw)
	}

	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].bodyIndent == "" {
			return src, nil, false
		}
		out.WriteString(stack[i].headerIndent)
		switch stack[i].kind {
		case pythonScopeMatchArm:
			out.WriteString("},\n")
		default:
			out.WriteString("}\n")
		}
		changed = true
	}

	if !changed {
		return src, nil, false
	}
	return []byte(out.String()), changes, true
}

func splitSourceLines(src []byte) []sourceLine {
	if len(src) == 0 {
		return nil
	}
	var lines []sourceLine
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
		lines = append(lines, sourceLine{
			start:      start,
			text:       text,
			raw:        string(src[start:rawEnd]),
			indent:     text[:indentEnd],
			trimmed:    strings.TrimSpace(text),
			hasNewline: hasNewline,
			lineNo:     lineNo,
		})
		start = rawEnd
		lineNo++
	}
	return lines
}

func isIgnorablePythonLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")
}

func preparePythonBlockClosings(stack []pythonBlockScope, indent string) ([]pythonBlockScope, []pythonBlockScope, bool) {
	var closings []pythonBlockScope
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		if top.bodyIndent == "" {
			if len(indent) <= len(top.headerIndent) {
				return stack, nil, false
			}
			top.bodyIndent = indent
			stack[len(stack)-1] = top
			return stack, closings, true
		}
		if len(indent) < len(top.bodyIndent) {
			closings = append(closings, top)
			stack = stack[:len(stack)-1]
			continue
		}
		return stack, closings, true
	}
	return stack, closings, true
}

func writePythonClosings(out *strings.Builder, closings []pythonBlockScope, includeLast bool) {
	limit := len(closings)
	if !includeLast && limit > 0 {
		limit--
	}
	for i := 0; i < limit; i++ {
		out.WriteString(closings[i].headerIndent)
		switch closings[i].kind {
		case pythonScopeMatchArm:
			out.WriteString("},\n")
		default:
			out.WriteString("}\n")
		}
	}
}

func rewritePythonColonHeader(trimmed string) (pythonColonHeader, bool) {
	if strings.HasSuffix(trimmed, "->") {
		head := strings.TrimSpace(strings.TrimSuffix(trimmed, "->"))
		if head == "" {
			return pythonColonHeader{}, false
		}
		return pythonColonHeader{
			rewritten:     head + " -> {",
			changeKind:    "python_arrow_arm_block",
			message:       "wrap a multiline match arm body in Osty braces",
			scopeKind:     pythonScopeMatchArm,
			requiresMatch: true,
		}, true
	}
	if !strings.HasSuffix(trimmed, ":") {
		return pythonColonHeader{}, false
	}
	head := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
	switch {
	case head == "else":
		return pythonColonHeader{
			rewritten:  "else {",
			changeKind: "python_else_block",
			message:    "replace Python-style `else:` block with Osty braces",
			scopeKind:  pythonScopeBlock,
			elseish:    true,
		}, true
	case strings.HasPrefix(head, "else if "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_else_if_block",
			message:    "replace Python-style `else if:` block with Osty braces",
			scopeKind:  pythonScopeBlock,
			elseish:    true,
		}, true
	case strings.HasPrefix(head, "if "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_if_block",
			message:    "replace Python-style `if:` block with Osty braces",
			scopeKind:  pythonScopeBlock,
		}, true
	case strings.HasPrefix(head, "for "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_for_block",
			message:    "replace Python-style `for:` block with Osty braces",
			scopeKind:  pythonScopeBlock,
		}, true
	case strings.HasPrefix(head, "while "):
		return pythonColonHeader{
			rewritten:  "for " + strings.TrimPrefix(head, "while ") + " {",
			changeKind: "python_while_block",
			message:    "replace Python-style `while:` block with Osty braces",
			scopeKind:  pythonScopeBlock,
		}, true
	case strings.HasPrefix(head, "match "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_match_block",
			message:    "replace Python-style `match:` block with Osty braces",
			scopeKind:  pythonScopeMatch,
		}, true
	case strings.HasPrefix(head, "case "):
		return pythonColonHeader{
			rewritten:     strings.TrimPrefix(head, "case ") + " -> {",
			changeKind:    "python_case_arm",
			message:       "replace Python-style `case:` arm with Osty match syntax",
			scopeKind:     pythonScopeMatchArm,
			requiresMatch: true,
		}, true
	case head == "default":
		return pythonColonHeader{
			rewritten:     "_ -> {",
			changeKind:    "python_default_arm",
			message:       "replace Python-style `default:` arm with Osty match syntax",
			scopeKind:     pythonScopeMatchArm,
			requiresMatch: true,
		}, true
	case strings.HasPrefix(head, "fn "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_fn_block",
			message:    "replace Python-style function block with Osty braces",
			scopeKind:  pythonScopeBlock,
		}, true
	case strings.HasPrefix(head, "pub fn "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_fn_block",
			message:    "replace Python-style function block with Osty braces",
			scopeKind:  pythonScopeBlock,
		}, true
	case strings.HasPrefix(head, "struct "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_struct_block",
			message:    "replace Python-style struct block with Osty braces",
			scopeKind:  pythonScopeBlock,
		}, true
	case strings.HasPrefix(head, "interface "):
		return pythonColonHeader{
			rewritten:  head + " {",
			changeKind: "python_interface_block",
			message:    "replace Python-style interface block with Osty braces",
			scopeKind:  pythonScopeBlock,
		}, true
	default:
		return pythonColonHeader{}, false
	}
}
