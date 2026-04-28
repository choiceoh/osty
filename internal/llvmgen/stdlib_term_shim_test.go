package llvmgen

import (
	"strings"
	"testing"
)

func TestStdTermRoutesToRuntimeAndSupportsSizeFields(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.term as term

fn main() {
    println(term.isTerminal())
    match term.size() {
        Ok(size) -> {
            println(size.width)
            println(size.height)
        },
        Err(err) -> println(err.message()),
    }
    match term.write("x") {
        Ok(_) -> println("w"),
        Err(err) -> println(err.message()),
    }
    match term.flush() {
        Ok(_) -> println("f"),
        Err(err) -> println(err.message()),
    }
    match term.setRawMode(false) {
        Ok(_) -> println("raw"),
        Err(err) -> println(err.message()),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_term.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%__osty_std_term_Size = type { i64, i64 }",
		"declare i1 @osty_rt_term_is_terminal()",
		"call i1 @osty_rt_term_is_terminal()",
		"declare i64 @osty_rt_term_width()",
		"declare i64 @osty_rt_term_height()",
		"call i64 @osty_rt_term_width()",
		"call i64 @osty_rt_term_height()",
		"declare ptr @osty_rt_term_write(ptr)",
		"call ptr @osty_rt_term_write(ptr",
		"declare ptr @osty_rt_term_flush()",
		"call ptr @osty_rt_term_flush()",
		"declare ptr @osty_rt_term_set_raw_mode(i1)",
		"call ptr @osty_rt_term_set_raw_mode(i1 false)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}
