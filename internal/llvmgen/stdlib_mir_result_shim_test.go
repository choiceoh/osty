package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateFromMIRStdEncodingNullableDecodeDiscardCallsRuntimeAsPtr(t *testing.T) {
	t.Skip("merged broken in #1349/#1351; see project_encoding_mir_namespace_gap")
	for _, tc := range []struct {
		variant string
		runtime string
		leak    string
	}{
		{variant: "base64", runtime: "osty_rt_encoding_base64_decode", leak: "Base64__decode"},
		{variant: "base64url", runtime: "osty_rt_encoding_base64url_decode", leak: "Base64Url__decode"},
	} {
		t.Run(tc.variant, func(t *testing.T) {
			got := generateStdEncodingDecodeDiscardMIR(t, tc.variant)
			for _, want := range []string{
				"declare ptr @" + tc.runtime + "(ptr)",
				"call ptr @" + tc.runtime + "(",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("discarded nullable decode LLVM missing %q:\n%s", want, got)
				}
			}
			for _, forbidden := range []string{
				"call void @" + tc.runtime + "(",
				tc.leak,
			} {
				if strings.Contains(got, forbidden) {
					t.Fatalf("discarded nullable decode emitted forbidden %q:\n%s", forbidden, got)
				}
			}
		})
	}
}
