//go:build selfhostgen

package gen

// goKeywords is the set of Go reserved words that MUST NOT appear as a
// bare identifier in generated code. Osty identifiers that happen to
// match one get a trailing underscore.
//
// Osty's own keywords (fn, struct, enum, …) cannot be used as idents in
// the source, so they aren't listed here — the only collisions are Go
// keywords that happen to be valid Osty identifiers.
var goKeywords = map[string]struct{}{
	"break":       {},
	"case":        {},
	"chan":        {},
	"const":       {},
	"continue":    {},
	"default":     {},
	"defer":       {},
	"else":        {},
	"fallthrough": {},
	"for":         {},
	"func":        {},
	"go":          {},
	"goto":        {},
	"if":          {},
	"import":      {},
	"interface":   {},
	"map":         {},
	"package":     {},
	"range":       {},
	"return":      {},
	"select":      {},
	"struct":      {},
	"switch":      {},
	"type":        {},
	"var":         {},
}

// mangleIdent returns a safe Go identifier for an Osty name: reserved
// Go words get a trailing underscore, everything else passes through.
//
// Osty's self/Self keywords are handled at their call sites, not here.
func mangleIdent(name string) string {
	if _, ok := goKeywords[name]; ok {
		return name + "_"
	}
	return name
}
