package selfhost_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/bootstrap/seedgen"
)

// TestMatchArmReturnLiftsOutOfIIFE pins the bug-#3 fix from the
// bootstrap/gen transpiler.
//
// Before the fix, a `return` inside a match arm body that sits in
// expression position (e.g. `let x = match e { ... -> { return Y } }`)
// only escaped the synthesized IIFE — not the enclosing Osty function
// — so the value Y was silently bound to x and execution continued.
// The fix lifts any match whose arm bodies contain `return` into a
// statement-position lowering, putting the arm bodies directly in the
// outer Go function so `return` propagates correctly.
//
// This test exercises the failure mode end-to-end: it transpiles a
// program whose only behavior difference between buggy and fixed
// codegen is whether a match-arm `return` propagates, then compiles
// and runs the generated Go and asserts the printed values match the
// language-level semantics.
func TestMatchArmReturnLiftsOutOfIIFE(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bootstrap/gen e2e match-arm-return test in short mode")
	}

	repoRoot := filepath.Join("..", "..")

	// Three call sites distinguish buggy vs. fixed lowering:
	//   classify(0)  — fixed: -1 (return propagates); buggy: -10 (label * 10)
	//   classify(5)  — both: 10 (label = 1)
	//   classify(-3) — both: 0  (label = 0)
	src := `fn classify(n: Int) -> Int {
    let label = match n {
        0 -> { return -1 },
        x if x > 0 -> 1,
        _ -> 0,
    }
    label * 10
}

fn main() {
    println("{classify(0)}")
    println("{classify(5)}")
    println("{classify(-3)}")
}
`

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "match_arm_return.osty")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	genPath := filepath.Join(tmpDir, "match_arm_return.go")

	generated, err := seedgen.Generate(seedgen.Config{
		SourcePath:  srcPath,
		PackageName: "main",
		RepoRoot:    repoRoot,
	})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if err := os.WriteFile(genPath, generated, 0o644); err != nil {
		t.Fatalf("write generated: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("generated file is empty")
	}

	// Sanity check: the lifted lowering declares a result var prefixed
	// `_ml`. If we see the IIFE shape for `classify` instead, the lift
	// did not fire and the test below would silently pass on the
	// buggy output for the wrong reason.
	if !bytes.Contains(generated, []byte("_ml")) {
		t.Fatalf("generated Go does not contain a lifted match result var — "+
			"escaping match was likely emitted as IIFE.\nGenerated:\n%s", generated)
	}

	run := exec.Command("go", "run", genPath)
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated:\n%v\n%s\nGenerated source:\n%s",
			err, bytes.TrimSpace(out), generated)
	}

	got := strings.TrimSpace(string(out))
	want := "-1\n10\n0"
	if got != want {
		t.Fatalf("match-arm return did not propagate.\n got: %q\nwant: %q\n"+
			"Generated:\n%s", got, want, generated)
	}
}
