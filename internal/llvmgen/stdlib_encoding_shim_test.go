package llvmgen

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestStdEncodingRuntimeLoweringMatchesASTPaths(t *testing.T) {
	g := &generator{stdEncodingAliases: map[string]bool{"encoding": true, "enc": true}}

	ident := func(name string) ast.Expr { return &ast.Ident{Name: name} }
	field := func(x ast.Expr, name string) ast.Expr { return &ast.FieldExpr{X: x, Name: name} }
	call := func(fn ast.Expr) *ast.CallExpr { return &ast.CallExpr{Fn: fn} }

	cases := []struct {
		name    string
		call    *ast.CallExpr
		variant string
		method  string
	}{
		{
			name:    "hex encode",
			call:    call(field(field(ident("encoding"), "hex"), "encode")),
			variant: "hex",
			method:  "encode",
		},
		{
			name:    "base64 decode alias",
			call:    call(field(field(ident("enc"), "base64"), "decode")),
			variant: "base64",
			method:  "decode",
		},
		{
			name:    "base64 url nested encode",
			call:    call(field(field(field(ident("encoding"), "base64"), "url"), "encode")),
			variant: "base64url",
			method:  "encode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, spec, ok := g.stdEncodingRuntimeCallInfo(tc.call)
			if !ok {
				t.Fatalf("stdEncodingRuntimeCallInfo did not match")
			}
			if spec == nil || spec.Variant != tc.variant {
				t.Fatalf("variant = %v, want %q", spec, tc.variant)
			}
			if method == nil || method.Name != tc.method {
				t.Fatalf("method = %v, want %q", method, tc.method)
			}
		})
	}
}

func TestStdEncodingRuntimeLoweringRejectsNonRegistryASTPaths(t *testing.T) {
	g := &generator{stdEncodingAliases: map[string]bool{"encoding": true}}

	ident := func(name string) ast.Expr { return &ast.Ident{Name: name} }
	field := func(x ast.Expr, name string) ast.Expr { return &ast.FieldExpr{X: x, Name: name} }
	call := func(fn ast.Expr) *ast.CallExpr { return &ast.CallExpr{Fn: fn} }

	cases := []struct {
		name string
		call *ast.CallExpr
	}{
		{
			name: "unknown alias",
			call: call(field(field(ident("other"), "hex"), "encode")),
		},
		{
			name: "unknown codec",
			call: call(field(field(ident("encoding"), "ascii85"), "encode")),
		},
		{
			name: "unknown method",
			call: call(field(field(ident("encoding"), "hex"), "encodedLen")),
		},
		{
			name: "optional chain",
			call: call(&ast.FieldExpr{X: field(ident("encoding"), "hex"), Name: "encode", IsOptional: true}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := g.stdEncodingRuntimeCallInfo(tc.call); ok {
				t.Fatalf("stdEncodingRuntimeCallInfo matched unexpected path")
			}
		})
	}
}

func TestStdEncodingRuntimeLoweringMatchesMIRPrefixes(t *testing.T) {
	cases := []struct {
		symbol  string
		variant string
		method  string
	}{
		{symbol: "Hex__encode", variant: "hex", method: "encode"},
		{symbol: "Base64__decode", variant: "base64", method: "decode"},
		{symbol: "Base64Url__encode", variant: "base64url", method: "encode"},
	}
	for _, tc := range cases {
		t.Run(tc.symbol, func(t *testing.T) {
			spec, method, ok := stdEncodingRuntimeLoweringForMIRSymbol(tc.symbol)
			if !ok {
				t.Fatalf("stdEncodingRuntimeLoweringForMIRSymbol did not match")
			}
			if spec == nil || spec.Variant != tc.variant {
				t.Fatalf("variant = %v, want %q", spec, tc.variant)
			}
			if method != tc.method {
				t.Fatalf("method = %q, want %q", method, tc.method)
			}
		})
	}
	if _, _, ok := stdEncodingRuntimeLoweringForMIRSymbol("UrlEncoding__encode"); ok {
		t.Fatalf("UrlEncoding__encode should not be owned by std.encoding runtime lowering registry")
	}
}
