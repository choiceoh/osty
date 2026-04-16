package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestGenerateModuleWhileLoopCompat(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 4 {
        sum = sum + i
        i = i + 1
    }
    println(sum)
}
`)

	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source: []byte(`fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 4 {
        sum = sum + i
        i = i + 1
    }
    println(sum)
}
`),
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower returned issues: %v", issues)
	}
	if validateErrs := ir.Validate(mod); len(validateErrs) != 0 {
		t.Fatalf("ir.Validate returned errors: %v", validateErrs)
	}
	out, err := GenerateModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/while_loop_ir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"for.cond",
		"for.body",
		"@printf",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
