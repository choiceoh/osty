package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/llvmabi"
)

func TestBundledRuntimeStage0AuditAliasesLinkWithThinLTO(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	runtimeObjectPath := filepath.Join(dir, bundledRuntimeObjectName)
	irPath := filepath.Join(dir, "stage0_audit_aliases.ll")
	irObjectPath := filepath.Join(dir, "stage0_audit_aliases.o")
	binaryPath := filepath.Join(dir, "stage0_audit_aliases")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	ir := `@.path = private unnamed_addr constant [2 x i8] c".\00"
@.missing = private unnamed_addr constant [26 x i8] c"stage0-alias-missing-path\00"
@.fmt = private unnamed_addr constant [6 x i8] c"%lld\0A\00"
@stderr = external global ptr
@llvm.used = appending global [23 x ptr] [
  ptr @Char__toString,
  ptr @Char__len,
  ptr @std.strings.fromChar,
  ptr @std.strings.compare,
  ptr @std.env.args,
  ptr @std.env.get,
  ptr @std.testing.assert,
  ptr @std.testing.assertTrue,
  ptr @std.testing.assertFalse,
  ptr @std.testing.assertEq,
  ptr @std.testing.assertNe,
  ptr @std.testing.fail,
  ptr @std.fs.readToString,
  ptr @std.fs.walk,
  ptr @std.fs.mkdirAll,
  ptr @std.fs.writeString,
  ptr @std.os.exit,
  ptr @std.process.abort,
  ptr @runtime.path.filepath.Base,
  ptr @runtime.path.filepath.Ext,
  ptr @__interp,
  ptr @i64,
  ptr @stderr
], section "llvm.metadata"

declare ptr @Char__toString(i32)
declare i64 @Char__len(ptr)
declare ptr @std.strings.fromChar(i32)
declare i64 @std.strings.compare(ptr, ptr)
declare ptr @std.env.args()
declare ptr @std.env.get(ptr)
declare void @std.testing.assert(i1)
declare void @std.testing.assertTrue(i1)
declare void @std.testing.assertFalse(i1)
declare void @std.testing.assertEq(i64, i64)
declare void @std.testing.assertNe(i64, i64)
declare void @std.testing.fail(ptr)
declare ptr @std.fs.readToString(ptr)
declare ptr @std.fs.walk(ptr)
declare ptr @std.fs.mkdirAll(ptr)
declare ptr @std.fs.writeString(ptr, ptr)
declare void @std.os.exit(i64)
declare void @std.process.abort(ptr)
declare ptr @runtime.path.filepath.Base(ptr)
declare ptr @runtime.path.filepath.Ext(ptr)
declare ptr @__interp()
declare ptr @i64()
declare i32 @fprintf(ptr, ptr, ...)

define i32 @main() {
entry:
  %from_char = call ptr @std.strings.fromChar(i32 65)
  %to_string = call ptr @Char__toString(i32 66)
  %len = call i64 @Char__len(ptr %from_char)
  %cmp = call i64 @std.strings.compare(ptr %from_char, ptr %to_string)
  %args = call ptr @std.env.args()
  %env = call ptr @std.env.get(ptr %from_char)
  call void @std.testing.assert(i1 true)
  call void @std.testing.assertTrue(i1 true)
  call void @std.testing.assertFalse(i1 false)
  call void @std.testing.assertEq(i64 7, i64 7)
  call void @std.testing.assertNe(i64 7, i64 8)
  %file = call ptr @std.fs.readToString(ptr @.path)
  %walk = call ptr @std.fs.walk(ptr @.missing)
  %mkdir = call ptr @std.fs.mkdirAll(ptr @.path)
  %write = call ptr @std.fs.writeString(ptr @.path, ptr %from_char)
  %base = call ptr @runtime.path.filepath.Base(ptr @.path)
  %ext = call ptr @runtime.path.filepath.Ext(ptr @.path)
  %interp = call ptr @__interp()
  %type_name = call ptr @i64()
  %stderr = load ptr, ptr @stderr
  %printed = call i32 (ptr, ptr, ...) @fprintf(ptr %stderr, ptr @.fmt, i64 %len)
  ret i32 0
}
`
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", irPath, err)
	}

	runClang := func(label string, args []string) {
		t.Helper()
		out, err := exec.Command("clang", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("clang %s failed: %v\n%s", label, err, out)
		}
	}

	runClang("compile runtime", clangCompileCObjectArgs("", "", runtimePath, runtimeObjectPath))
	runClang("compile stage0 alias IR", llvmabi.ClangCompileObjectArgs("", irPath, irObjectPath))
	linkArgs := llvmabi.ClangLinkBinaryArgs("", []string{irObjectPath, runtimeObjectPath}, binaryPath)
	linkArgs = append(linkArgs, clangPlatformRuntimeLinkArgs("")...)
	runClang("link stage0 alias binary", linkArgs)

	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	if got, want := string(out), "1\n"; got != want {
		t.Fatalf("stage0 alias harness output = %q, want %q", got, want)
	}
}

func TestBundledRuntimeCIHostAliasesLinkWithHostObjects(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	runtimeObjectPath := filepath.Join(dir, bundledRuntimeObjectName)
	irPath := filepath.Join(dir, "cihost_aliases.ll")
	irObjectPath := filepath.Join(dir, "cihost_aliases.o")
	binaryPath := filepath.Join(dir, "cihost_aliases")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	ir := `declare i1 @runtime.cihost.HasManifest(ptr)
declare ptr @runtime.cihost.CapturePackageHost(ptr)

define i32 @main() {
entry:
  %ok = call i1 @runtime.cihost.HasManifest(ptr null)
  %pkg = call ptr @runtime.cihost.CapturePackageHost(ptr null)
  ret i32 0
}
`
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", irPath, err)
	}

	runClang := func(label string, args []string) {
		t.Helper()
		out, err := exec.Command("clang", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("clang %s failed: %v\n%s", label, err, out)
		}
	}

	runClang("compile runtime", clangCompileCObjectArgs("", "debug", runtimePath, runtimeObjectPath))
	runClang("compile cihost alias IR", llvmabi.ClangCompileObjectArgs("", irPath, irObjectPath))
	linkArgs := llvmabi.ClangLinkBinaryArgs("", []string{irObjectPath, runtimeObjectPath}, binaryPath)
	linkArgs = append(linkArgs, clangPlatformRuntimeLinkArgs("")...)
	runClang("link cihost alias binary", linkArgs)
}

func TestBundledRuntimeStdOsExecWithReturnsResultBox(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	runtimeObjectPath := filepath.Join(dir, bundledRuntimeObjectName)
	irPath := filepath.Join(dir, "exec_with_result_box.ll")
	irObjectPath := filepath.Join(dir, "exec_with_result_box.o")
	binaryPath := filepath.Join(dir, "exec_with_result_box")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	ir := `%Result = type { i64, ptr }
%ExecOutput = type { i64, ptr, ptr, i1 }

@.cat = private unnamed_addr constant [4 x i8] c"cat\00"
@.input = private unnamed_addr constant [6 x i8] c"probe\00"
@.empty = private unnamed_addr constant [1 x i8] c"\00"

declare ptr @osty_rt_os_exec_input_with(ptr, ptr, ptr, ptr, ptr, i64)

define i32 @main() {
entry:
  %res = call ptr @osty_rt_os_exec_input_with(ptr @.cat, ptr null, ptr @.input, ptr @.empty, ptr null, i64 0)
  %tagp = getelementptr inbounds %Result, ptr %res, i32 0, i32 0
  %tag = load i64, ptr %tagp
  %is_ok = icmp eq i64 %tag, 0
  br i1 %is_ok, label %payload, label %bad_tag

payload:
  %payloadp = getelementptr inbounds %Result, ptr %res, i32 0, i32 1
  %out = load ptr, ptr %payloadp
  %has_payload = icmp ne ptr %out, null
  br i1 %has_payload, label %exit_code, label %bad_payload

exit_code:
  %exitp = getelementptr inbounds %ExecOutput, ptr %out, i32 0, i32 0
  %exit = load i64, ptr %exitp
  %exit_ok = icmp eq i64 %exit, 0
  br i1 %exit_ok, label %ok, label %bad_exit

ok:
  ret i32 0
bad_tag:
  ret i32 10
bad_payload:
  ret i32 11
bad_exit:
  ret i32 12
}
`
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", irPath, err)
	}

	runClang := func(label string, args []string) {
		t.Helper()
		out, err := exec.Command("clang", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("clang %s failed: %v\n%s", label, err, out)
		}
	}

	runClang("compile runtime", clangCompileCObjectArgs("", "debug", runtimePath, runtimeObjectPath))
	runClang("compile std.os execWith result-box IR", llvmabi.ClangCompileObjectArgs("", irPath, irObjectPath))
	linkArgs := llvmabi.ClangLinkBinaryArgs("", []string{irObjectPath, runtimeObjectPath}, binaryPath)
	linkArgs = append(linkArgs, clangPlatformRuntimeLinkArgs("")...)
	runClang("link std.os execWith result-box binary", linkArgs)

	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
}
