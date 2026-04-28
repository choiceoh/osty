package llvmgen

import (
	"strings"
	"testing"
)

func TestStdFsReadRoutesToRuntimeAndKeepsResultSourceTypeInAST(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes
use std.fs as fs

fn main() {
    match fs.read("input.bin") {
        Ok(data) -> println(bytes.len(data)),
        Err(err) -> println(err.message()),
    }
    match fs.readToString("input.txt") {
        Ok(text) -> println(text),
        Err(err) -> println(err.message()),
    }
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_ast.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_fs_read(ptr)",
		"declare ptr @osty_rt_fs_read_error()",
		"call ptr @osty_rt_fs_read(ptr",
		"call ptr @osty_rt_fs_read_error()",
		"declare ptr @osty_rt_fs_read_string(ptr)",
		"declare ptr @osty_rt_fs_read_string_error()",
		"call ptr @osty_rt_fs_read_string(ptr",
		"call ptr @osty_rt_fs_read_string_error()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsMutationsRouteToRuntimeInAST(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes
use std.fs as fs

fn main() {
    match fs.write("out.bin", bytes.fromString("abc")) {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
    match fs.writeString("out.txt", "hello") {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
    match fs.create("empty.txt") {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
    match fs.rename("out.txt", "renamed.txt") {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
    match fs.copy("renamed.txt", "copied.txt") {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
    match fs.mkdir("one") {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
    match fs.mkdirAll("one/two") {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
    match fs.remove("renamed.txt") {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_mut_ast.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_fs_write_bytes(ptr, ptr)",
		"declare ptr @osty_rt_fs_write_string(ptr, ptr)",
		"declare ptr @osty_rt_fs_create(ptr)",
		"declare ptr @osty_rt_fs_rename(ptr, ptr)",
		"declare ptr @osty_rt_fs_copy(ptr, ptr)",
		"declare ptr @osty_rt_fs_mkdir(ptr)",
		"declare ptr @osty_rt_fs_mkdir_all(ptr)",
		"declare ptr @osty_rt_fs_remove(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsExistsRoutesToRuntimeInAST(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.fs as fs

fn main() {
    println(fs.exists("demo.txt"))
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_exists_ast.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_fs_exists(ptr)",
		"call i1 @osty_rt_fs_exists(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsReadRoutesToRuntimeInMIR(t *testing.T) {
	mod := lowerSrcLLVM(t, `use std.fs as fs

fn main() {
    let raw = fs.read("input.bin")
    let text = fs.readToString("input.txt")
    println(1)
}
`)
	mirMod := buildMIRModuleFromHIR(t, mod)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_mir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_fs_read(ptr)",
		"declare ptr @osty_rt_fs_read_error()",
		"call ptr @osty_rt_fs_read(ptr",
		"call ptr @osty_rt_fs_read_error()",
		"declare ptr @osty_rt_fs_read_string(ptr)",
		"declare ptr @osty_rt_fs_read_string_error()",
		"call ptr @osty_rt_fs_read_string(ptr",
		"call ptr @osty_rt_fs_read_string_error()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsMutationsRouteToRuntimeInMIR(t *testing.T) {
	mod := lowerSrcLLVM(t, `use std.fs as fs

fn main() {
    let a = fs.writeString("out.txt", "hello")
    let b = fs.create("empty.txt")
    let c = fs.rename("out.txt", "renamed.txt")
    let d = fs.copy("renamed.txt", "copied.txt")
    let e = fs.mkdir("one")
    let f = fs.mkdirAll("one/two")
    let g = fs.remove("renamed.txt")
    println(1)
}
`)
	mirMod := buildMIRModuleFromHIR(t, mod)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_mut_mir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_fs_write_string(ptr, ptr)",
		"declare ptr @osty_rt_fs_create(ptr)",
		"declare ptr @osty_rt_fs_rename(ptr, ptr)",
		"declare ptr @osty_rt_fs_copy(ptr, ptr)",
		"declare ptr @osty_rt_fs_mkdir(ptr)",
		"declare ptr @osty_rt_fs_mkdir_all(ptr)",
		"declare ptr @osty_rt_fs_remove(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsExistsRoutesToRuntimeInMIR(t *testing.T) {
	mod := lowerSrcLLVM(t, `use std.fs as fs

fn main() {
    println(fs.exists("demo.txt"))
}
`)
	mirMod := buildMIRModuleFromHIR(t, mod)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_exists_mir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_fs_exists(ptr)",
		"call i1 @osty_rt_fs_exists(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsReadRoutesToRuntimeInNativeOwnedEntry(t *testing.T) {
	mod := lowerNativeEntryModule(t, `use std.fs as fs

fn main() {
    let raw = fs.read("input.bin")
    let text = fs.readToString("input.txt")
    println(1)
}
`)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_native.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported unsupported")
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_fs_read(ptr)",
		"declare ptr @osty_rt_fs_read_error()",
		"declare ptr @osty_rt_fs_read_string(ptr)",
		"declare ptr @osty_rt_fs_read_string_error()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsMutationsRouteToRuntimeInNativeOwnedEntry(t *testing.T) {
	mod := lowerNativeEntryModule(t, `use std.fs as fs

fn main() {
    let a = fs.writeString("out.txt", "hello")
    let b = fs.create("empty.txt")
    let c = fs.rename("out.txt", "renamed.txt")
    let d = fs.copy("renamed.txt", "copied.txt")
    let e = fs.mkdir("one")
    let f = fs.mkdirAll("one/two")
    let g = fs.remove("renamed.txt")
    println(1)
}
`)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_mut_native.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported unsupported")
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_fs_write_string(ptr, ptr)",
		"declare ptr @osty_rt_fs_create(ptr)",
		"declare ptr @osty_rt_fs_rename(ptr, ptr)",
		"declare ptr @osty_rt_fs_copy(ptr, ptr)",
		"declare ptr @osty_rt_fs_mkdir(ptr)",
		"declare ptr @osty_rt_fs_mkdir_all(ptr)",
		"declare ptr @osty_rt_fs_remove(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdFsExistsRoutesToRuntimeInNativeOwnedEntry(t *testing.T) {
	mod := lowerNativeEntryModule(t, `use std.fs as fs

fn main() {
    println(fs.exists("demo.txt"))
}
`)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_fs_exists_native.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported unsupported")
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_fs_exists(ptr)",
		"call i1 @osty_rt_fs_exists(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}
