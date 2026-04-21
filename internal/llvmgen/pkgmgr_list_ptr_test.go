package llvmgen

import (
	"strings"
	"testing"
)

// TestListLiteralMixedPtrStringSourceTracking pins down the source-type
// tracking needed by pkgmgr.osty — specifically the `list_mixed_ptr`
// wall that tripped on toolchain/pkgmgr.osty:selfPkgRegistryYankRequest
// before this fix. A List<String> built from a mix of String literals
// plus variables must stay recognised as String-backed ptr values
// through every common production path:
//
//   - `let mut x = "lit"`       — mutable binding of a String literal
//   - `"hi {name}"`             — interpolated String literal
//   - `a + b`                   — runtime string concat
//   - `struct.field`            — field access on a String field
//   - `if cond { "a" } else { "b" }` — phi-merged branches
//
// Each path must carry the String source type so the list-literal
// emitter (expr.go:emitListExprWithHint) can tell all elements are
// String and skip the mixed-ptr diagnostic.
func TestListLiteralMixedPtrStringSourceTracking(t *testing.T) {
	file := parseLLVMGenFile(t, `pub fn endpoint(baseURL: String, segments: List<String>) -> String {
    let mut out = baseURL
    for s in segments {
        out = out + "/" + s
    }
    out
}

pub fn yank(baseURL: String, name: String, version: String, yanked: Bool) -> String {
    let mut segment = "yank"
    if !(yanked) {
        segment = "unyank"
    }
    endpoint(baseURL, ["v1", "crates", name, version, segment])
}

pub fn interpolated(name: String) -> String {
    let mut greeting = "hi {name}"
    endpoint("u", ["static", greeting])
}

pub fn concat(a: String, b: String) -> String {
    let joined = a + b
    endpoint("u", ["static", joined])
}

pub struct Req {
    pub method: String,
    pub url: String,
}

pub fn fromStruct(r: Req) -> String {
    let mut header = r.method
    endpoint("u", ["api", header])
}

pub fn fromIfMerge(x: Int) -> String {
    let kind = if x > 0 { "positive" } else { "other" }
    endpoint("u", ["api", kind])
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/pkgmgr_list_ptr.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define ptr @yank(",
		"define ptr @interpolated(",
		"define ptr @concat(",
		"define ptr @fromStruct(",
		"define ptr @fromIfMerge(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q", want)
		}
	}
}
