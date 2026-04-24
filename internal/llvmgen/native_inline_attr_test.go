package llvmgen

import (
	"strings"
	"testing"
)

// TestNativeOwnedModuleInlineAlwaysAttrOnDefineLine pins the v0.6 A8
// `#[inline(always)]` emission on the IR → MIR → native projection
// path. The HIR-based emitter is covered by fn_attrs_test.go; this
// test ensures the native slice (toolchain/llvmgen.osty +
// native_entry_snapshot.go) picks up the same discriminant from
// `FnDecl.InlineMode` and splices `alwaysinline` into the `define`
// line produced by `llvmRenderFunctionWithAttrs`.
func TestNativeOwnedModuleInlineAlwaysAttrOnDefineLine(t *testing.T) {
	src := `#[inline(always)]
fn tiny(n: Int) -> Int { n + 1 }

fn main() {
    let _ = tiny(42)
}
`
	mod := lowerNativeEntryModule(t, src)
	nativeMod, ok := nativeModuleFromIR(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_inline_always.osty",
	})
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for #[inline(always)] tiny()")
	}
	got := renderNativeOwnedModuleText(nativeMod)
	idx := strings.Index(got, "define i64 @tiny(")
	if idx < 0 {
		t.Fatalf("define line for tiny not found in native IR:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[idx:], '\n')
	if lineEnd < 0 {
		t.Fatalf("define line has no newline terminator:\n%s", got[idx:])
	}
	defineLine := got[idx : idx+lineEnd]
	if !strings.Contains(defineLine, "alwaysinline") {
		t.Fatalf("native-path tiny define line missing `alwaysinline`:\n  %s", defineLine)
	}
}

// TestNativeOwnedModuleInlineNoneStaysClean is the negative control:
// a fn without any inline annotation must produce the attribute-free
// `define ... (...) {` shape on the native path — confirms the
// fnAttrs == "" branch of `llvmRenderFunctionWithAttrs` byte-matches
// legacy output.
func TestNativeOwnedModuleInlineNoneStaysClean(t *testing.T) {
	src := `fn plain(n: Int) -> Int { n + 1 }

fn main() {
    let _ = plain(42)
}
`
	mod := lowerNativeEntryModule(t, src)
	nativeMod, ok := nativeModuleFromIR(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_inline_none.osty",
	})
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for plain()")
	}
	got := renderNativeOwnedModuleText(nativeMod)
	idx := strings.Index(got, "define i64 @plain(")
	if idx < 0 {
		t.Fatalf("define line for plain not found in native IR:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[idx:], '\n')
	if lineEnd < 0 {
		t.Fatalf("define line has no newline terminator:\n%s", got[idx:])
	}
	defineLine := got[idx : idx+lineEnd]
	if !strings.HasSuffix(defineLine, ") {") {
		t.Fatalf("unannotated plain() define line should end `) {` (no attrs), got:\n  %s", defineLine)
	}
}
