package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

func TestStdEncodingRuntimeLoweringMarksHexDecodeMIRResult(t *testing.T) {
	spec, method, ok := stdEncodingRuntimeLoweringForMIRSymbol("Hex__decode")
	if !ok {
		t.Fatalf("Hex__decode did not match std.encoding MIR registry")
	}
	if method != "decode" {
		t.Fatalf("method = %q, want decode", method)
	}
	if spec == nil || spec.Variant != "hex" {
		t.Fatalf("variant = %v, want hex", spec)
	}
	if spec.DecodeMIRKind != stdEncodingDecodeHexBytesResult {
		t.Fatalf("hex decode MIR kind = %v, want stdEncodingDecodeHexBytesResult", spec.DecodeMIRKind)
	}
}

func TestGenerateFromMIRStdEncodingHexDecodeReturnsResult(t *testing.T) {
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
		Name:   "decodeHex",
		Return: resultBytes,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{Result: &ir.CallExpr{
			Callee: &ir.FieldExpr{
				X: &ir.FieldExpr{
					X:    &ir.Ident{Name: "encoding", T: ir.ErrTypeVal},
					Name: "hex",
					T:    ir.ErrTypeVal,
				},
				Name: "decode",
				T:    ir.ErrTypeVal,
			},
			Args: []ir.Arg{{Value: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString}}},
			T:    resultBytes,
		}},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{useDecl, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/std_encoding_hex_decode_mir.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
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
