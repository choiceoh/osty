package llvmgen

import (
	"strings"
	"testing"
)

func TestStdIoOutputCallsRouteToRuntimeWriteInAST(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.io as io

fn main() {
    print(1)
    println(true)
    io.eprint("err")
    io.eprintln("warn")
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_io_ast.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare void @osty_rt_io_write(ptr, i1, i1)",
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare ptr @osty_rt_bool_to_string(i1)",
		", i1 false, i1 false)",
		", i1 true, i1 false)",
		", i1 false, i1 true)",
		", i1 true, i1 true)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
	if gotCalls := strings.Count(got, "call void @osty_rt_io_write("); gotCalls != 4 {
		t.Fatalf("osty_rt_io_write call count = %d, want 4\n%s", gotCalls, got)
	}
}

func TestStdIoOutputCallsRouteToRuntimeWriteInMIR(t *testing.T) {
	mod := lowerSrcLLVM(t, `use std.io as io

fn main() {
    io.print("one")
    io.eprintln("two")
    eprint(1)
    eprintln(true)
}
`)
	mirMod := buildMIRModuleFromHIR(t, mod)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_io_mir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare void @osty_rt_io_write(ptr, i1, i1)",
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare ptr @osty_rt_bool_to_string(i1)",
		", i1 false, i1 false)",
		", i1 true, i1 true)",
		", i1 false, i1 true)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
	if gotCalls := strings.Count(got, "call void @osty_rt_io_write("); gotCalls != 4 {
		t.Fatalf("osty_rt_io_write call count = %d, want 4\n%s", gotCalls, got)
	}
}

func TestStdIoOutputCallsRouteToRuntimeWriteInNativeOwnedEntry(t *testing.T) {
	mod := lowerNativeEntryModule(t, `use std.io as io

fn main() {
    print(1)
    io.println("two")
    eprint(true)
    io.eprintln("four")
}
`)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_io_native.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported unsupported")
	}
	got := string(out)
	for _, want := range []string{
		"declare void @osty_rt_io_write(ptr, i1, i1)",
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare ptr @osty_rt_bool_to_string(i1)",
		", i1 false, i1 false)",
		", i1 true, i1 false)",
		", i1 false, i1 true)",
		", i1 true, i1 true)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
	if gotCalls := strings.Count(got, "call void @osty_rt_io_write("); gotCalls != 4 {
		t.Fatalf("osty_rt_io_write call count = %d, want 4\n%s", gotCalls, got)
	}
}
