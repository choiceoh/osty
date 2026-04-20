package llvmgen

import (
	"strings"
	"testing"
)

// Match used as a statement (value discarded) is pervasive in toolchain
// dispatchers like `match node.kind { AstNFnDecl -> collectFnDecl(...) }`.
// Before LLVM012 resolution this hit
// `expression statement *ast.MatchExpr is not a call`.
func TestMatchStmtBareEnumArmsLower(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Fn,
    Struct,
    Enum,
}

fn handleFn(x: Int) {
    println(x)
}

fn handleStruct(x: Int) {
    println(x + 1)
}

fn main() {
    let k: Kind = Kind.Fn
    match k {
        Kind.Fn -> handleFn(1),
        Kind.Struct -> handleStruct(2),
        Kind.Enum -> println(3),
        _ -> {},
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/match_stmt_bare.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	// Expect match lowering labels + a couple of branch comparisons.
	for _, want := range []string{
		"match.arm",
		"match.end",
		"match.next",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Empty block arm bodies (`_ -> {}`) should lower as a trivial
// fall-through — the toolchain uses this pattern as a catch-all with no
// side effect.
func TestMatchStmtEmptyBlockArm(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Tag {
    A,
    B,
}

fn main() {
    let t: Tag = Tag.A
    match t {
        Tag.A -> println(1),
        _ -> {},
    }
}
`)
	_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/match_stmt_empty_block.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}

// Non-exhaustive bare-enum match as a statement is allowed — coverage
// enforcement is the checker's job; the backend just falls through to
// the shared end label when no arm matched.
func TestMatchStmtInexhaustive(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Tag {
    A,
    B,
    C,
}

fn main() {
    let t: Tag = Tag.A
    match t {
        Tag.A -> println(1),
        Tag.B -> println(2),
    }
    println(99)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/match_stmt_inexhaustive.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	// The final println(99) must still be emitted — the match statement
	// converges its end label into main's continuation. Check for the
	// literal constant alongside the match end label to confirm the two
	// pieces line up.
	if !strings.Contains(got, "i64 99") {
		t.Fatalf("expected println(99) constant after match end:\n%s", got)
	}
	if !strings.Contains(got, "match.end") {
		t.Fatalf("expected match.end label so control flow converges:\n%s", got)
	}
}
