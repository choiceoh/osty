package llvmgen

import (
	"strings"
	"testing"
)

// TestInterpolationCoercesBoolViaRuntime extends #350's Int/Float
// interpolation coverage to Bool. `"flag={b}"` where b is i1 should now
// funnel through `osty_rt_bool_to_string` just like Int funnels through
// `osty_rt_int_to_string`.
func TestInterpolationCoercesBoolViaRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let b: Bool = true
    println("flag={b}")
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bool_interp.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bool_to_string(i1)",
		"call ptr @osty_rt_bool_to_string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q (Bool interpolation broken):\n%s", want, got)
		}
	}
}
