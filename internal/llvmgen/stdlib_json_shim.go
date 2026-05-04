package llvmgen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/mir"
)

// std.json shim — Phase B/C scope. Wires `json.parseValue(text)` to
// `osty_rt_json_parse` and returns Result<Json, Error> where Json is
// the opaque ptr the C runtime returns. Pattern matching on Json
// variants (`Json.Null`, `Json.Int(n)`, …) lands in a follow-up cycle
// once the synthetic enum layout is wired through MIR; for now users
// can discriminate Ok/Err and pass the opaque Json around.
//
// `Json` is whitelisted as an opaque ptr type in mir_generator.go's
// `typeSupported` list so user code may hold it as a local without
// tripping the "unsupported local type Json" wall.

const (
	ostyRtJsonParseSymbol     = "osty_rt_json_parse"
	ostyRtJsonKindSymbol      = "osty_rt_json_kind"
	ostyRtJsonGetBoolSymbol   = "osty_rt_json_get_bool"
	ostyRtJsonGetIntSymbol    = "osty_rt_json_get_int"
	ostyRtJsonGetFloatSymbol  = "osty_rt_json_get_float"
	ostyRtJsonGetStringSymbol = "osty_rt_json_get_string"
)

// emitStdJsonCallMIR is the MIR-level dispatch for json.parseValue.
// Mirrors url.parse: call C runtime, branch on null, build Result.
// The Json payload is a ptr (heap-allocated by the C runtime), so
// the okPayload is the raw ptr-as-i64 — no toI64Slot heap-box needed
// because the C runtime already returns a heap address.
func (g *mirGen) emitStdJsonCallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.json.")
	if method != "parseValue" {
		return false, nil
	}
	if len(c.Args) != 1 {
		return true, unsupported("mir-mvp", "json.parseValue requires one String argument")
	}
	text, err := g.evalStringArg(c.Args[0], "json.parseValue", 0)
	if err != nil {
		return true, err
	}
	return true, g.emitJsonParseValueMIR(c, text)
}

func (g *mirGen) emitJsonParseValueMIR(c *mir.CallInstr, text mirRuntimeArg) error {
	g.declareRuntime(ostyRtJsonParseSymbol, mirRuntimeDeclareLine("ptr", ostyRtJsonParseSymbol, "ptr"))
	parsed := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(parsed, "ptr", ostyRtJsonParseSymbol, mirRuntimeArgList([]mirRuntimeArg{text})))
	if c.Dest == nil {
		return nil
	}
	return g.emitPtrResultFromNullable(c, parsed, mir.TBytes, "")
}

// stdJsonParseValueResultSourceType is the public AST shape for
// json.parseValue. Builds Result<Json, Error> where Json is treated
// as a builtin name (opaque ptr at the LLVM level).
var stdJsonParseValueResultSourceType ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		&ast.NamedType{Path: []string{"Json"}},
		errorSourceTypeSingleton,
	},
}
