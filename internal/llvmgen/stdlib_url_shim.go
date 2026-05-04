package llvmgen

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
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
			typ:        "%Option.i64",
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
			typ:        "%Option.string",
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

// emitStdUrlCallMIR is the MIR-level dispatch for url.parse. Uses
// the same heap-box pattern as os.exec: build the synthetic Url
// struct, hand it to toI64Slot which heap-allocates and returns
// ptr-as-i64, then wrap in Result.
func (g *mirGen) emitStdUrlCallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.url.")
	if method != "parse" {
		return false, nil
	}
	if len(c.Args) != 1 {
		return true, unsupported("mir-mvp", "url.parse requires one String argument")
	}
	text, err := g.evalStringArg(c.Args[0], "url.parse", 0)
	if err != nil {
		return true, err
	}
	return true, g.emitUrlParseMIR(c, text)
}

func (g *mirGen) emitUrlParseMIR(c *mir.CallInstr, text mirRuntimeArg) error {
	g.declareRuntime(ostyRtUrlParseSymbol, mirRuntimeDeclareLine("ptr", ostyRtUrlParseSymbol, "ptr"))
	parsed := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(parsed, "ptr", ostyRtUrlParseSymbol, mirRuntimeArgList([]mirRuntimeArg{text})))
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return unsupported("mir-mvp", "url.parse dest into unknown local")
	}
	okT, _, ok := g.resultSubtypes(destLoc.Type)
	if !ok {
		return unsupported("mir-mvp", "url.parse dest is not Result<Url, Error>")
	}
	resultLLVM := g.llvmType(destLoc.Type)
	urlLLVM := "%" + stdUrlSyntheticUrlTypeName
	g.stdUrlUrlTouched = true

	// Declare accessor symbols.
	g.declareRuntime(ostyRtUrlGetSchemeSymbol, mirRuntimeDeclareLine("ptr", ostyRtUrlGetSchemeSymbol, "ptr"))
	g.declareRuntime(ostyRtUrlGetHostSymbol, mirRuntimeDeclareLine("ptr", ostyRtUrlGetHostSymbol, "ptr"))
	g.declareRuntime(ostyRtUrlGetPortSymbol, mirRuntimeDeclareLine("i64", ostyRtUrlGetPortSymbol, "ptr"))
	g.declareRuntime(ostyRtUrlGetPathSymbol, mirRuntimeDeclareLine("ptr", ostyRtUrlGetPathSymbol, "ptr"))
	g.declareRuntime(ostyRtUrlGetFragmentSymbol, mirRuntimeDeclareLine("ptr", ostyRtUrlGetFragmentSymbol, "ptr"))
	g.declareRuntime(ostyRtUrlHasFragmentSymbol, mirRuntimeDeclareLine("i1", ostyRtUrlHasFragmentSymbol, "ptr"))
	g.declareRuntime(ostyRtUrlGetQuerySymbol, mirRuntimeDeclareLine("ptr", ostyRtUrlGetQuerySymbol, "ptr"))

	// Branch: parsed == null → Err, else build Url from accessors → Ok.
	failed := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(failed, "ptr", parsed, "null"))
	errLabel := g.freshLabel("url.parse.err")
	okLabel := g.freshLabel("url.parse.ok")
	contLabel := g.freshLabel("url.parse.cont")
	g.fnBuf.WriteString(mirBrCondLine(failed, errLabel, okLabel))

	// Err arm.
	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errValue := g.emitResultValue(resultLLVM, false, "0")
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	// Ok arm: pull components, build Url, box via toI64Slot.
	g.fnBuf.WriteString(mirLabelLine(okLabel))
	scheme := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(scheme, "ptr", ostyRtUrlGetSchemeSymbol, mirArgSlotPtr(parsed)))
	host := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(host, "ptr", ostyRtUrlGetHostSymbol, mirArgSlotPtr(parsed)))
	port := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(port, "i64", ostyRtUrlGetPortSymbol, mirArgSlotPtr(parsed)))
	pathReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(pathReg, "ptr", ostyRtUrlGetPathSymbol, mirArgSlotPtr(parsed)))
	queryReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(queryReg, "ptr", ostyRtUrlGetQuerySymbol, mirArgSlotPtr(parsed)))
	fragmentRaw := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(fragmentRaw, "ptr", ostyRtUrlGetFragmentSymbol, mirArgSlotPtr(parsed)))
	hasFragment := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(hasFragment, "i1", ostyRtUrlHasFragmentSymbol, mirArgSlotPtr(parsed)))

	// Build Option<Int> port: -1 → None, else Some. Layout %Option.i64.
	portCmp := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = icmp eq i64 %s, -1\n", portCmp, port))
	portTag := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = select i1 %s, i64 0, i64 1\n", portTag, portCmp))
	portPayload := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = select i1 %s, i64 0, i64 %s\n", portPayload, portCmp, port))
	portStep := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %%Option.i64 undef, i64 %s, 0\n", portStep, portTag))
	portOpt := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %%Option.i64 %s, i64 %s, 1\n", portOpt, portStep, portPayload))

	// Build Option<String> fragment. Layout %Option.string.
	fragTag := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = select i1 %s, i64 1, i64 0\n", fragTag, hasFragment))
	fragPayload := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = ptrtoint ptr %s to i64\n", fragPayload, fragmentRaw))
	fragGated := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = select i1 %s, i64 %s, i64 0\n", fragGated, hasFragment, fragPayload))
	fragStep := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %%Option.string undef, i64 %s, 0\n", fragStep, fragTag))
	fragOpt := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %%Option.string %s, i64 %s, 1\n", fragOpt, fragStep, fragGated))

	// Build the Url struct.
	url0 := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %s undef, ptr %s, 0\n", url0, urlLLVM, scheme))
	url1 := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %s %s, ptr %s, 1\n", url1, urlLLVM, url0, host))
	url2 := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %s %s, %%Option.i64 %s, 2\n", url2, urlLLVM, url1, portOpt))
	url3 := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %s %s, ptr %s, 3\n", url3, urlLLVM, url2, pathReg))
	url4 := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %s %s, ptr %s, 4\n", url4, urlLLVM, url3, queryReg))
	url5 := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = insertvalue %s %s, %%Option.string %s, 5\n", url5, urlLLVM, url4, fragOpt))

	// Box the struct via toI64Slot — same pattern os.exec uses for
	// returning Result<Output, Error>: the Url struct is allocated
	// on the GC heap and the payload slot stores the ptr-as-i64.
	okPayload, err := g.toI64Slot(url5, okT)
	if err != nil {
		return err
	}
	okValue := g.emitResultValue(resultLLVM, true, okPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, errValue, errLabel, okValue, okLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}
