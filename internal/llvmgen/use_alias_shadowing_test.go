package llvmgen

import (
	"strings"
	"testing"
)

// TestLocalShadowsBytesUseAliasIRPipeline pins the body-aware
// `useAliasFor` gate in the MIR lowerer: when a function imports
// `std.bytes as bytes` but a local binding named `bytes` shadows the
// alias, method calls on that local must lower as List intrinsics
// (the local's actual type) — not as bytes-module free-fns. Without
// the gate, MIR emits `IntrinsicBytesLen` with no receiver because
// `bytesIntrinsicForMethod` matches "len" by name regardless of the
// receiver type, and the LLVM backend rejects the instruction with
// "bytes intrinsic with no receiver".
func TestLocalShadowsBytesUseAliasIRPipeline(t *testing.T) {
	src := `use std.bytes as bytes

fn check() -> Int {
    let mut bytes: List<Int> = []
    bytes.push(0)
    bytes.push(42)
    bytes.len()
}

fn main() {
    let n = check()
    println(n)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/use_alias_shadow_bytes.osty"})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i64 @osty_rt_list_len",
		"call void @osty_rt_list_push",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"@osty_rt_bytes_len",
		"@osty_rt_bytes_push",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generated IR contains unwanted %q (shadowing should keep it on the List path):\n%s", unwanted, got)
		}
	}
}

// TestLocalShadowsStringsUseAliasIRPipeline is the strings-side
// companion: a `use std.strings as strings` import shadowed by a
// local `strings` binding routes method calls through the local's
// type, not the stdlib strings free-fn intrinsics.
func TestLocalShadowsStringsUseAliasIRPipeline(t *testing.T) {
	src := `use std.strings as strings

fn check() -> Int {
    let mut strings: List<Int> = []
    strings.push(1)
    strings.len()
}

fn main() {
    let n = check()
    println(n)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/use_alias_shadow_strings.osty"})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "call i64 @osty_rt_list_len") {
		t.Fatalf("generated IR missing list_len call:\n%s", got)
	}
	if strings.Contains(got, "@osty_rt_strings_") {
		t.Fatalf("shadowed strings local should not route through std.strings intrinsics:\n%s", got)
	}
}
