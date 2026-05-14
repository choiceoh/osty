package airepair

import (
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/token"
)

// sourceLine aliases runner.SourceLine so the internal call sites
// can keep using a short, package-local name. The struct shape and
// field meanings are owned by toolchain/airepair_source.osty.
type sourceLine = runner.SourceLine

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
	return runner.SrcWantsPythonColonBlockRepair(src) && diagsHaveError(diags)
}

func rewritePythonColonBlocks(src []byte) ([]byte, []repair.Change, bool) {
	lines := runner.SplitSourceLines(src)
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
		if line.Trimmed == "" || runner.IsIgnorablePythonLine(line.Trimmed) {
			out.WriteString(line.Raw)
			continue
		}

		var (
			closings []pythonBlockScope
			ok       bool
		)
		stack, closings, ok = preparePythonBlockClosings(stack, line.Indent)
		if !ok {
			return src, nil, false
		}

		header, headerOK := rewritePythonColonHeader(line.Trimmed)
		if headerOK && header.elseish {
			headerOK = len(closings) > 0 && closings[len(closings)-1].kind == pythonScopeBlock
		}
		if headerOK && header.requiresMatch {
			headerOK = len(stack) > 0 && stack[len(stack)-1].kind == pythonScopeMatch
		}
		if headerOK {
			writePythonClosings(&out, closings, !header.elseish)
			if header.elseish && len(closings) > 0 {
				out.WriteString(line.Indent)
				out.WriteString("} ")
				out.WriteString(header.rewritten)
			} else {
				out.WriteString(line.Indent)
				out.WriteString(header.rewritten)
			}
			if line.HasNewline {
				out.WriteByte('\n')
			}
			stack = append(stack, pythonBlockScope{
				kind:         header.scopeKind,
				headerIndent: line.Indent,
			})
			changes = append(changes, repair.Change{
				Kind:    header.changeKind,
				Message: header.message,
				Pos: token.Pos{
					Offset: line.Start + len(line.Indent),
					Line:   line.LineNo,
					Column: len([]rune(line.Indent)) + 1,
				},
			})
			changed = true
			continue
		}

		writePythonClosings(&out, closings, true)
		out.WriteString(line.Raw)
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

// diagsHaveError reports whether `diags` carries at least one
// error-severity entry. Shared by every `wantsXxxRepair` host
// wrapper around the corresponding `runner.SrcWantsXxxRepair`
// pure-source predicate.
func diagsHaveError(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
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

// rewritePythonColonHeader bridges to runner.RewritePythonColonHeader
// (mirror of toolchain/airepair_python.osty). The struct/enum shape
// here stays package-local — runner's snapshot owns the recogniser
// rules.
func rewritePythonColonHeader(trimmed string) (pythonColonHeader, bool) {
	r := runner.RewritePythonColonHeader(trimmed)
	if !r.Ok {
		return pythonColonHeader{}, false
	}
	return pythonColonHeader{
		rewritten:     r.Rewritten,
		changeKind:    r.ChangeKind,
		message:       r.Message,
		scopeKind:     pythonScopeKind(r.ScopeKind),
		elseish:       r.Elseish,
		requiresMatch: r.RequiresMatch,
	}, true
}
