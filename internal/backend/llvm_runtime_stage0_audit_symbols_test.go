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
@.fmt = private unnamed_addr constant [6 x i8] c"%lld\0A\00"
@stderr = external global ptr
@llvm.used = appending global [14 x ptr] [
  ptr @Char__toString,
  ptr @Char__len,
  ptr @std.strings.fromChar,
  ptr @std.strings.compare,
  ptr @std.env.args,
  ptr @std.env.get,
  ptr @std.fs.readToString,
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
declare ptr @std.fs.readToString(ptr)
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
  %file = call ptr @std.fs.readToString(ptr @.path)
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

	runClang("compile runtime", clangCompileCObjectArgs("", runtimePath, runtimeObjectPath))
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
