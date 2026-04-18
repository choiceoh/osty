package llvmgen

import (
	"strings"
	"testing"
)

// TestForLoopFieldStringCompareAgainstLocal verifies `feature.name == name`
// where `feature` comes from a for-loop over List<FeatureSpec>.
func TestForLoopFieldStringCompareAgainstLocal(t *testing.T) {
	file := parseLLVMGenFile(t, `struct FeatureSpec {
    pub name: String,
    pub deps: List<String>,
}

fn featureExists(name: String, features: List<FeatureSpec>) -> Bool {
    for feature in features {
        if feature.name == name {
            return true
        }
    }
    false
}

fn main() {
    let fs = [FeatureSpec { name: "a", deps: [] }]
    if featureExists("a", fs) { println(1) } else { println(0) }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/mf1.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST returned error: %v", err)
	}
	if !strings.Contains(string(ir), "@osty_rt_strings_Equal") {
		t.Fatalf("expected String compare via osty_rt_strings_Equal; IR:\n%s", ir)
	}
}

// TestForLoopNestedListIterCompareFieldName reproduces the self-reference
// check from toolchain/manifest_features.osty:
//
//	for feature in features { for dep in feature.deps { if dep == feature.name { ... } } }
//
// Both `dep` (inner iter) and `feature.name` (outer iter's struct field) must
// carry a `String` source type through the iter binding so the llvmgen
// compare guard routes to osty_rt_strings_Equal instead of rejecting the
// operands as non-String ptr values (LLVM011).
func TestForLoopNestedListIterCompareFieldName(t *testing.T) {
	file := parseLLVMGenFile(t, `struct FeatureSpec {
    pub name: String,
    pub deps: List<String>,
}

fn countSelfRefs(features: List<FeatureSpec>) -> Int {
    let mut n = 0
    for feature in features {
        for dep in feature.deps {
            if dep == feature.name {
                n = n + 1
            }
        }
    }
    n
}

fn main() {
    let fs = [FeatureSpec { name: "a", deps: ["a"] }]
    let n = countSelfRefs(fs)
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/mf2.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST returned error: %v", err)
	}
	if !strings.Contains(string(ir), "@osty_rt_strings_Equal") {
		t.Fatalf("expected String compare via osty_rt_strings_Equal; IR:\n%s", ir)
	}
}
