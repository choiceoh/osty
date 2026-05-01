package llvmgen

import (
	"strings"
	"testing"
)

func TestStdRegexCompileRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.regex

fn main() {
    match regex.compile(r"\d+") {
        Ok(_) -> println("ok"),
        Err(_) -> println("err"),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_regex_compile.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_regex_compile(ptr)",
		"call ptr @osty_rt_regex_compile",
		"declare ptr @osty_rt_regex_compile_error()",
		"call ptr @osty_rt_regex_compile_error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdRegexMatchesRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.regex

fn check(re: Regex, s: String) -> Bool {
    re.matches(s)
}

fn main() {
    match regex.compile(r"^\d+$") {
        Ok(re) -> {
            if check(re, "123") {
                println("yes")
            } else {
                println("no")
            }
        },
        Err(_) -> println("compile failed"),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_regex_matches.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_regex_matches(ptr, ptr)",
		"call i1 @osty_rt_regex_matches",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdRegexReplaceRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.regex

fn run(re: Regex) -> String {
    re.replace("hello world", "X")
}

fn main() {
    match regex.compile(r"\w+") {
        Ok(re) -> println(run(re)),
        Err(_) -> println("compile failed"),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_regex_replace.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_regex_replace(ptr, ptr, ptr)",
		"call ptr @osty_rt_regex_replace",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdRegexReplaceAllAndSplitRouteToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.regex

fn run(re: Regex) -> String {
    re.replaceAll("a-b-c", ".")
}

fn parts(re: Regex) -> List<String> {
    re.split("a-b-c-d")
}

fn main() {
    match regex.compile(r"-") {
        Ok(re) -> {
            println(run(re))
            let _ = parts(re)
        },
        Err(_) -> println("compile failed"),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_regex_replace_all_split.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_regex_replace_all(ptr, ptr, ptr)",
		"call ptr @osty_rt_regex_replace_all",
		"declare ptr @osty_rt_regex_split(ptr, ptr)",
		"call ptr @osty_rt_regex_split",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdRegexCapturesAllRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.regex

fn run(re: Regex) -> List<Captures> {
    re.capturesAll("a 1 b 22 c 333")
}

fn main() {
    match regex.compile(r"\d+") {
        Ok(re) -> {
            let _ = run(re)
            println("ok")
        },
        Err(_) -> println("compile failed"),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_regex_captures_all.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_regex_captures_all(ptr, ptr)",
		"call ptr @osty_rt_regex_captures_all",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdRegexFindAllRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.regex

fn run(re: Regex) -> Int {
    re.findAll("alpha beta").len()
}

fn main() {
    match regex.compile(r"\w+") {
        Ok(re) -> {
            let n = run(re)
            println("count")
        },
        Err(_) -> println("compile failed"),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_regex_find_all.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_regex_find_all(ptr, ptr)",
		"call ptr @osty_rt_regex_find_all",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdRegexCapturesNamedRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.regex

fn main() {
    match regex.compile(r"(?P<area>\d+)") {
        Ok(re) -> {
            match re.captures("42") {
                Some(c) -> {
                    match c.named("area") {
                        Some(s) -> println(s),
                        None    -> println("none"),
                    }
                },
                None -> println("nm"),
            }
        },
        Err(_) -> println("compile failed"),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_regex_captures_named.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_regex_captures_named(ptr, ptr)",
		"call ptr @osty_rt_regex_captures_named",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
