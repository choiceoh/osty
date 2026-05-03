package llvmgen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
)

// std.url shim — provides MIR + LLVM support for the spec §10.16 `Url`
// struct so that `url.parse(text) -> Result<Url, Error>` end-to-end
// works through the LLVM backend. The Osty body in
// `internal/stdlib/modules/url.osty` parses RFC 3986 references but
// the body cannot lower (List<String>, recursive helpers, Map writes
// in a loop). This shim sidesteps the body by:
//
//   1. Registering a synthetic `__osty_std_url_Url` struct so user
//      code may hold `Url` as a local type and access fields by name
//      (`u.scheme`, `u.port`, …).
//   2. Wiring `url.parse` to the C runtime parser
//      (`osty_rt_url_parse_*`), then constructing the synthetic Url
//      via the AST/MIR aggregate path.
//
// Phase 1 (this file): synthetic struct + isStdUrlType predicate.
// Phase 2 / 3: C runtime + parse shim land in follow-up commits so
// each piece has its own bisect surface.

const (
	stdUrlSyntheticUrlTypeName = "__osty_std_url_Url"

	ostyRtUrlParseSymbol       = "osty_rt_url_parse"
	ostyRtUrlGetSchemeSymbol   = "osty_rt_url_get_scheme"
	ostyRtUrlGetHostSymbol     = "osty_rt_url_get_host"
	ostyRtUrlGetPortSymbol     = "osty_rt_url_get_port"
	ostyRtUrlGetPathSymbol     = "osty_rt_url_get_path"
	ostyRtUrlGetFragmentSymbol = "osty_rt_url_get_fragment"
	ostyRtUrlHasFragmentSymbol = "osty_rt_url_has_fragment"
	ostyRtUrlGetQuerySymbol    = "osty_rt_url_get_query"
)

// stdUrlSyntheticUrlSourceType is the public AST `NamedType` used to
// stamp `Url`-typed values produced by the shim. Field-access /
// builder code threads this through `value.sourceType` so subsequent
// field lookups recognise the synthetic struct.
var stdUrlSyntheticUrlSourceType ast.Type = &ast.NamedType{
	Path: []string{stdUrlSyntheticUrlTypeName},
}

// stdUrlParseResultSourceType is the `Result<Url, Error>` shape that
// `url.parse` returns. Built lazily on first use.
var stdUrlParseResultSourceType ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdUrlSyntheticUrlSourceType,
		errorSourceTypeSingleton,
	},
}

// isStdUrlType matches the spec §10.16 `Url` struct. It only fires
// when the resolver hasn't already attached a real layout (a user
// program redeclaring `Url` would take precedence) — same gate the
// other synthetic stdlib structs use.
func (g *mirGen) isStdUrlType(t *ir.NamedType) bool {
	if t == nil || t.Name != "Url" {
		return false
	}
	if g.mod != nil && g.mod.Layouts != nil {
		if _, ok := g.mod.Layouts.Structs[mirNamedTypeLayoutKey(t)]; ok {
			return false
		}
	}
	q := t.QualifiedName()
	return q == "Url" || q == "std.url.Url" || q == "url.Url"
}

func collectStdUrlAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "url" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "url"
		}
		out[alias] = true
	}
	return out
}

// ensureStdUrlSyntheticUrlStruct registers the synthetic `Url` struct
// with the AST-level emitter so that field access (`u.scheme` etc.)
// can find each field's offset and LLVM type. The layout matches
// `pub struct Url { scheme: String, host: String, port: Int?, path:
// String, query: Map<String, String>, fragment: String? }` from
// `internal/stdlib/modules/url.osty`.
//
// `port` and `fragment` are `Option<X>` — at LLVM level the Option
// shape is a 2-i64 aggregate `{i64 tag, i64 payload}` (matches
// Result<T, E> emitted elsewhere; payload is bit-cast from the
// concrete type). The synthetic struct embeds this aggregate inline
// so the user-side `match u.port { Some(p) -> …, None -> … }`
// dispatches through the existing Option-on-builtin path.
func ensureStdUrlSyntheticUrlStruct(g *generator) *structInfo {
	if g == nil {
		return nil
	}
	if info := g.structsByName[stdUrlSyntheticUrlTypeName]; info != nil {
		return info
	}
	if g.structsByName == nil {
		g.structsByName = map[string]*structInfo{}
	}
	if g.structsByType == nil {
		g.structsByType = map[string]*structInfo{}
	}
	info := &structInfo{
		name:   stdUrlSyntheticUrlTypeName,
		typ:    llvmStructTypeName(stdUrlSyntheticUrlTypeName),
		byName: map[string]fieldInfo{},
	}
	optionIntSourceType := &ast.NamedType{
		Path: []string{"Option"},
		Args: []ast.Type{&ast.NamedType{Path: []string{"Int"}}},
	}
	optionStringSourceType := &ast.NamedType{
		Path: []string{"Option"},
		Args: []ast.Type{&ast.NamedType{Path: []string{"String"}}},
	}
	mapStringStringSourceType := &ast.NamedType{
		Path: []string{"Map"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"String"}},
			&ast.NamedType{Path: []string{"String"}},
		},
	}
	fields := []fieldInfo{
		{
			name:       "scheme",
			typ:        "ptr",
			index:      0,
			sourceType: &ast.NamedType{Path: []string{"String"}},
		},
		{
			name:       "host",
			typ:        "ptr",
			index:      1,
			sourceType: &ast.NamedType{Path: []string{"String"}},
		},
		{
			name:       "port",
			typ:        "{ i64, i64 }",
			index:      2,
			sourceType: optionIntSourceType,
		},
		{
			name:       "path",
			typ:        "ptr",
			index:      3,
			sourceType: &ast.NamedType{Path: []string{"String"}},
		},
		{
			name:       "query",
			typ:        "ptr",
			index:      4,
			sourceType: mapStringStringSourceType,
		},
		{
			name:       "fragment",
			typ:        "{ i64, i64 }",
			index:      5,
			sourceType: optionStringSourceType,
		},
	}
	info.fields = fields
	for _, field := range fields {
		info.byName[field.name] = field
	}
	g.structs = append(g.structs, info)
	g.structsByName[info.name] = info
	g.structsByType[info.typ] = info
	return info
}
