package selfhost

import (
	"runtime"
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
	last := frontCheckLastPathSegment(decl.text)
	return strings.Trim(last, "\"")
}


// selfhostBumpError increments env.errors and records the caller's Go
// function name in env.errorsByContext. The generated native checker
// invokes this at every error-detection site so downstream tooling can
// split the aggregate error count by category (see cmd/osty check
// --dump-native-diags).
//
// The package-qualified function name is trimmed to the bare Go
// identifier, which matches the shape of the enclosing Osty function
// closely enough to serve as a category key without extra bookkeeping.
func selfhostBumpError(env *FrontCheckEnv) {
	env.errors++
	if env.errorsByContext == nil {
		env.errorsByContext = make(map[string]int)
	}
	env.errorsByContext[selfhostErrorCallerContext(2)]++
}

// selfhostBumpErrorWithDetail is the detail-carrying variant: it buckets the
// caller context like selfhostBumpError, then drills one level deeper into
// env.errorDetails[<context>][<detail>]. Used from sites where the category
// alone hides the real hot spot — e.g. frontCheckIdentHint benefits from
// seeing which identifier name went unresolved most often.
func selfhostBumpErrorWithDetail(env *FrontCheckEnv, detail string) {
	env.errors++
	ctx := selfhostErrorCallerContext(2)
	if env.errorsByContext == nil {
		env.errorsByContext = make(map[string]int)
	}
	env.errorsByContext[ctx]++
	if env.errorDetails == nil {
		env.errorDetails = make(map[string]map[string]int)
	}
	bucket := env.errorDetails[ctx]
	if bucket == nil {
		bucket = make(map[string]int)
		env.errorDetails[ctx] = bucket
	}
	bucket[detail]++
}

// selfhostErrorCallerContext resolves a stack frame to a bare Go function
// name. `skip` follows the runtime.Caller contract (0 = this helper, 1 =
// its caller, 2 = the caller's caller, etc.).
func selfhostErrorCallerContext(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return "<unknown>"
	}
	name := runtime.FuncForPC(pc).Name()
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return name
}
