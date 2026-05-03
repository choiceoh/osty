package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

func TestStdEncodingRuntimeLoweringMarksDecodeMIRResult(t *testing.T) {
	cases := []struct {
		symbol string
		kind   stdEncodingRuntimeDecodeKind
	}{
		{symbol: "Hex__decode", kind: stdEncodingDecodeHexBytesResult},
		{symbol: "Base64__decode", kind: stdEncodingDecodeNullableBytesResult},
		{symbol: "Base64Url__decode", kind: stdEncodingDecodeNullableBytesResult},
	}
	for _, tc := range cases {
		t.Run(tc.symbol, func(t *testing.T) {
			spec, method, ok := stdEncodingRuntimeLoweringForMIRSymbol(tc.symbol)
			if !ok {
				t.Fatalf("%s did not match std.encoding MIR registry", tc.symbol)
			}
			if method != "decode" {
				t.Fatalf("method = %q, want decode", method)
			}
			if spec == nil {
				t.Fatalf("matched nil spec")
			}
			if spec.DecodeMIRKind != tc.kind {
				t.Fatalf("decode MIR kind = %v, want %v", spec.DecodeMIRKind, tc.kind)
			}
		})
	}
}

func TestGenerateFromMIRStdEncodingHexDecodeReturnsResult(t *testing.T) {
	got := generateStdEncodingDecodeMIR(t, "hex")
	for _, want := range []string{
		"declare i1 @osty_rt_bytes_is_valid_hex(ptr)",
		"declare ptr @osty_rt_bytes_from_hex(ptr)",
		"call i1 @osty_rt_bytes_is_valid_hex(",
		"call ptr @osty_rt_bytes_from_hex(",
		"insertvalue",
		"phi",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated MIR LLVM missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Hex__decode") {
		t.Fatalf("Hex__decode leaked as an unresolved call:\n%s", got)
	}
}

func TestGenerateFromMIRStdEncodingBase64DecodeReturnsResult(t *testing.T) {
	for _, tc := range []struct {
		variant string
		runtime string
		leak    string
	}{
		{variant: "base64", runtime: "osty_rt_encoding_base64_decode", leak: "Base64__decode"},
		{variant: "base64url", runtime: "osty_rt_encoding_base64url_decode", leak: "Base64Url__decode"},
	} {
		t.Run(tc.variant, func(t *testing.T) {
			got := generateStdEncodingDecodeMIR(t, tc.variant)
			for _, want := range []string{
				"declare ptr @" + tc.runtime + "(ptr)",
				"call ptr @" + tc.runtime + "(",
				"icmp eq ptr",
				"insertvalue",
				"phi",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("generated MIR LLVM missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, tc.leak) {
				t.Fatalf("%s leaked as an unresolved call:\n%s", tc.leak, got)
			}
		})
	}
}

func generateStdEncodingDecodeMIR(t *testing.T, variant string) string {
	t.Helper()
	useDecl := &ir.UseDecl{
		Path:    []string{"std", "encoding"},
		RawPath: "std.encoding",
		Alias:   "encoding",
	}
	resultBytes := &ir.NamedType{
		Name:    "Result",
		Builtin: true,
		Args: []ir.Type{
			ir.TBytes,
			&ir.NamedType{Name: "Error", Builtin: true},
		},
	}
	fn := &ir.FnDecl{
		Name:   "decodeValue",
		Return: resultBytes,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{Result: &ir.CallExpr{
			Callee: stdEncodingDecodeCallee(variant),
			Args:   []ir.Arg{{Value: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString}}},
			T:      resultBytes,
		}},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{useDecl, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/std_encoding_" + variant + "_decode_mir.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	return string(out)
}

func stdEncodingDecodeCallee(variant string) ir.Expr {
	encoding := &ir.Ident{Name: "encoding", T: ir.ErrTypeVal}
	switch variant {
	case "hex":
		return &ir.FieldExpr{
			X:    &ir.FieldExpr{X: encoding, Name: "hex", T: ir.ErrTypeVal},
			Name: "decode",
			T:    ir.ErrTypeVal,
		}
	case "base64":
		return &ir.FieldExpr{
			X:    &ir.FieldExpr{X: encoding, Name: "base64", T: ir.ErrTypeVal},
			Name: "decode",
			T:    ir.ErrTypeVal,
		}
	case "base64url":
		return &ir.FieldExpr{
			X: &ir.FieldExpr{
				X:    &ir.FieldExpr{X: encoding, Name: "base64", T: ir.ErrTypeVal},
				Name: "url",
				T:    ir.ErrTypeVal,
			},
			Name: "decode",
			T:    ir.ErrTypeVal,
		}
	default:
		return &ir.Ident{Name: "missing", T: ir.ErrTypeVal}
	}
}
