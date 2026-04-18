package selfhost

import (
	"runtime"
	"strings"
)

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
