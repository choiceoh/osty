package llvmgen

import (
	"strings"
	"testing"
)

func TestStdUuidV4RoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.uuid

fn main() {
    let u = uuid.v4()
    println(u.toString())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_uuid_v4.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_uuid_v4()",
		"call ptr @osty_rt_uuid_v4",
		"declare ptr @osty_rt_uuid_to_string(ptr)",
		"call ptr @osty_rt_uuid_to_string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdUuidV7RoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.uuid as u7

fn main() {
    let u = u7.v7()
    println(u.toString())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_uuid_v7.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	if !strings.Contains(got, "call ptr @osty_rt_uuid_v7") {
		t.Fatalf("generated IR missing osty_rt_uuid_v7 call:\n%s", got)
	}
}

func TestStdUuidNilRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.uuid

fn main() {
    let u = uuid.nil()
    println(u.toString())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_uuid_nil.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	if !strings.Contains(got, "call ptr @osty_rt_uuid_nil") {
		t.Fatalf("generated IR missing osty_rt_uuid_nil call:\n%s", got)
	}
}

func TestStdUuidToBytesRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.uuid

fn main() {
    let u = uuid.v4()
    let b = u.toBytes()
    println(b.toHex())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_uuid_to_bytes.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_uuid_to_bytes(ptr)",
		"call ptr @osty_rt_uuid_to_bytes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdUuidV4FromHelperFunctionRoutesToRuntime(t *testing.T) {
	// Same dispatch path as the script-main case but the call sits
	// inside a user-defined function — guards against regressions in
	// the full-file lowering chain.
	file := parseLLVMGenFile(t, `use std.uuid

fn fresh() -> Uuid {
    uuid.v4()
}

fn main() {
    let u = fresh()
    println(u.toString())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_uuid_helper.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "call ptr @osty_rt_uuid_v4") {
		t.Fatalf("generated IR missing osty_rt_uuid_v4 call:\n%s", got)
	}
	if !strings.Contains(got, "call ptr @osty_rt_uuid_to_string") {
		t.Fatalf("generated IR missing osty_rt_uuid_to_string call:\n%s", got)
	}
}

func TestStdUuidParseRoutesToResult(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.uuid

fn demo(text: String) -> String {
    match uuid.parse(text) {
        Ok(u) -> u.toString(),
        Err(_) -> "bad",
    }
}

fn main() {
    println(demo("00000000-0000-0000-0000-000000000000"))
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_uuid_parse.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_uuid_parse(ptr)",
		"call ptr @osty_rt_uuid_parse",
		"declare ptr @osty_rt_uuid_parse_error()",
		"call ptr @osty_rt_uuid_parse_error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
