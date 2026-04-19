package selfhost

import (
	"strings"
)

// selfhostTypesAliasEqual reports whether two type names differ only by
// a leading `alias.` qualifier on one side. FFI collection registers a
// struct declared inside `use X as alias { struct Foo {} }` under the
// bare name `Foo`; the receiver-side call annotation then refers to it
// as `alias.Foo`. Without this equivalence every cross-use of those
// names trips frontCheckExpectAssignable. Applied recursively over
// generic argument lists so `Option<Manifest>` ~ `Option<host.Manifest>`
// also match.
func selfhostTypesAliasEqual(a string, b string) bool {
	if a == b {
		return true
	}
	aHead, aArgs := selfhostSplitTypeHead(a)
	bHead, bArgs := selfhostSplitTypeHead(b)
	if !selfhostHeadsAliasEqual(aHead, bHead) {
		return false
	}
	if len(aArgs) != len(bArgs) {
		return false
	}
	for i := range aArgs {
		if !selfhostTypesAliasEqual(aArgs[i], bArgs[i]) {
			return false
		}
	}
	return true
}

func selfhostHeadsAliasEqual(a string, b string) bool {
	if a == b {
		return true
	}
	if selfhostStripSingleAliasPrefix(a) == b {
		return true
	}
	if a == selfhostStripSingleAliasPrefix(b) {
		return true
	}
	return false
}

func selfhostStripSingleAliasPrefix(name string) string {
	i := strings.IndexByte(name, '.')
	if i <= 0 {
		return name
	}
	prefix := name[:i]
	// Only strip when the prefix looks like a bare identifier (no nested
	// separators). This avoids collapsing `std.strings` (a real qualified
	// path) or parametric prefixes emitted by the type printer.
	for j := 0; j < len(prefix); j++ {
		c := prefix[j]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (j > 0 && c >= '0' && c <= '9') {
			continue
		}
		return name
	}
	return name[i+1:]
}

// selfhostSplitTypeHead parses a type-printer string of shape
// `Head<arg1, arg2, ...>` into (head, args). Passing through strings
// with no generic brackets returns (name, nil). Nested angle brackets
// inside args are preserved by tracking depth during the comma split.
func selfhostSplitTypeHead(t string) (string, []string) {
	lt := strings.IndexByte(t, '<')
	if lt < 0 || !strings.HasSuffix(t, ">") {
		return t, nil
	}
	head := t[:lt]
	inner := t[lt+1 : len(t)-1]
	if inner == "" {
		return head, nil
	}
	var args []string
	depth := 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(inner[start:i]))
				start = i + 1
			}
		}
	}
	args = append(args, strings.TrimSpace(inner[start:]))
	return head, args
}

// selfhostUseDeclAlias resolves the canonical alias name for a UseDecl
// node. The parser stores the `use ... as <alias>` alias ident in
// children2[0] when present; otherwise the alias falls back to the last
// segment of the raw path, stripped of any surrounding quote characters
// that a Go-FFI path literal (e.g. `use go "strings"`) would otherwise
// retain. Fixing this at collection time makes `env.fns` register the
// alias-qualified fn names the call-site lookup actually uses.
func selfhostUseDeclAlias(file *AstFile, decl *AstNode) string {
	if len(decl.children2) > 0 {
		aliasNode := astArenaNodeAt(file.arena, decl.children2[0])
		if aliasNode != nil && aliasNode.text != "" {
			return aliasNode.text
		}
	}
	last := selfhostLastPathSegment(decl.text)
	return strings.Trim(last, "\"")
}

func selfhostLastPathSegment(path string) string {
	parts := strings.Split(path, ".")
	last := path
	for _, part := range parts {
		last = part
	}
	return last
}

// selfhostDiagnosticTelemetry derives the host-side error histogram from the
// typed checker's structured diagnostics. The old checker reported caller-site
// buckets directly; the typed checker instead exposes stable diagnostic codes,
// so we bucket errors by code and retain the rendered message as detail rows.
func selfhostDiagnosticTelemetry(diags []*CheckDiagnostic) (map[string]int, map[string]map[string]int) {
	if len(diags) == 0 {
		return nil, nil
	}
	var byContext map[string]int
	var details map[string]map[string]int
	for _, d := range diags {
		if !selfhostDiagnosticIsError(d) {
			continue
		}
		ctx := selfhostDiagnosticBucket(d)
		if ctx == "" {
			ctx = "<error>"
		}
		if byContext == nil {
			byContext = make(map[string]int)
		}
		byContext[ctx]++
		detail := strings.TrimSpace(selfhostDiagnosticDetail(d))
		if detail == "" {
			continue
		}
		if details == nil {
			details = make(map[string]map[string]int)
		}
		bucket := details[ctx]
		if bucket == nil {
			bucket = make(map[string]int)
			details[ctx] = bucket
		}
		bucket[detail]++
	}
	return byContext, details
}

func selfhostDiagnosticIsError(d *CheckDiagnostic) bool {
	if d == nil {
		return false
	}
	_, ok := d.severity.(*DiagnosticSeverity_SeverityError)
	return ok
}

func selfhostDiagnosticBucket(d *CheckDiagnostic) string {
	if d == nil {
		return ""
	}
	if code := strings.TrimSpace(d.code); code != "" {
		return code
	}
	return selfhostDiagnosticSeverityLabel(d)
}

func selfhostDiagnosticDetail(d *CheckDiagnostic) string {
	if d == nil {
		return ""
	}
	if msg := strings.TrimSpace(d.message); msg != "" {
		return msg
	}
	return selfhostDiagnosticSeverityLabel(d)
}

func selfhostDiagnosticSeverityLabel(d *CheckDiagnostic) string {
	if d == nil {
		return "<nil>"
	}
	switch d.severity.(type) {
	case *DiagnosticSeverity_SeverityError:
		return "error"
	case *DiagnosticSeverity_SeverityWarning:
		return "warning"
	case *DiagnosticSeverity_SeverityLint:
		return "lint"
	default:
		return "unknown"
	}
}
