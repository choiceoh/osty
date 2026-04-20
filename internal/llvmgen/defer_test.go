package llvmgen

import (
	"strings"
	"testing"
)

// TestGenerateDeferAtFunctionScope verifies a defer attached to the
// function's top-level scope fires on the fall-through return, in LIFO
// order with earlier defers.
func TestGenerateDeferAtFunctionScope(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    defer println("first")
    defer println("second")
    println("body")
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/defer_fn_scope.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST returned error: %v", err)
	}

	got := string(ir)
	bodyIdx := strings.Index(got, "body")
	firstIdx := strings.Index(got, "first")
	secondIdx := strings.Index(got, "second")
	if bodyIdx < 0 || firstIdx < 0 || secondIdx < 0 {
		t.Fatalf("expected all println strings to appear in IR:\n%s", got)
	}
	// LIFO: last-registered defer ("second") fires first, then "first".
	if !(bodyIdx < secondIdx && secondIdx < firstIdx) {
		t.Fatalf("defer LIFO ordering broken: body=%d second=%d first=%d\n%s",
			bodyIdx, secondIdx, firstIdx, got)
	}
}

// TestGenerateDeferInRangeLoopBody verifies a defer inside a range-for
// body fires on every iteration boundary (normal fall-through).
func TestGenerateDeferInRangeLoopBody(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    for i in 0..3 {
        defer println("tail")
        println(i)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/defer_range_loop.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST returned error: %v", err)
	}

	got := string(ir)
	if !strings.Contains(got, "tail") {
		t.Fatalf("expected deferred println(\"tail\") to lower into the loop body:\n%s", got)
	}
	if !strings.Contains(got, "for.cont") {
		t.Fatalf("expected range-for scaffold in IR:\n%s", got)
	}
}

// TestGenerateDeferFiresOnLoopBreak verifies that defer runs when
// control flow exits the loop body via `break`, not just on the
// fall-through continue path.
func TestGenerateDeferFiresOnLoopBreak(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    for i in 0..5 {
        defer println("bye")
        if i == 2 {
            break
        }
        println(i)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/defer_loop_break.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST returned error: %v", err)
	}

	got := string(ir)
	// We expect the deferred println's string constant to appear at
	// least twice: once for the fall-through path and once on the
	// break exit path through unwindScopesTo -> popScope.
	count := strings.Count(got, `@.str`) // cheap sanity check: strings are interned
	if count < 1 {
		t.Fatalf("expected at least one interned string constant from defer body:\n%s", got)
	}
	byeOccurrences := strings.Count(got, "bye")
	if byeOccurrences < 2 {
		t.Fatalf("expected defer body to be emitted on both fall-through and break paths, got %d occurrences:\n%s", byeOccurrences, got)
	}
}

// TestGenerateDeferFiresOnLoopContinue verifies defer runs at the
// continue-branch's scope unwind point.
func TestGenerateDeferFiresOnLoopContinue(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    for i in 0..4 {
        defer println("iter")
        if i == 1 {
            continue
        }
        println(i)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/defer_loop_continue.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST returned error: %v", err)
	}

	got := string(ir)
	iterOccurrences := strings.Count(got, "iter")
	if iterOccurrences < 2 {
		t.Fatalf("expected defer body to be emitted on both continue and fall-through paths, got %d occurrences:\n%s", iterOccurrences, got)
	}
}

// TestGenerateDeferFiresOnReturn ensures defers queued across function
// scope flush before an explicit `return` statement lowered inside a
// nested block.
func TestGenerateDeferFiresOnReturn(t *testing.T) {
	file := parseLLVMGenFile(t, `fn run(flag: Bool) -> Int {
    defer println("closing")
    if flag {
        return 1
    }
    return 0
}

fn main() {
    let _ = run(true)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/defer_return.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST returned error: %v", err)
	}

	got := string(ir)
	closingOccurrences := strings.Count(got, "closing")
	if closingOccurrences < 2 {
		t.Fatalf("expected defer body to flush before both returns, got %d occurrences:\n%s", closingOccurrences, got)
	}
}
