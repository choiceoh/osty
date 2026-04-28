package llvmgen

import (
	"strings"
	"testing"
)

// TestGenerateIntLiteralMatchExpressionEmitsSelectChain locks the most
// common shape this PR unblocks: `match n { 0 -> ..., 1 -> ..., _ -> ... }`
// on an `Int` scrutinee. The reverse-iterated select chain is the same
// pattern emitTagEnumMatchSelectValue uses for payload-free enum tags,
// just keyed on `icmp eq i64 %n, <lit>` instead of `icmp eq i64
// %scrutinee, <tag>`.
func TestGenerateIntLiteralMatchExpressionEmitsSelectChain(t *testing.T) {
	file := parseLLVMGenFile(t, `fn label(n: Int) -> String {
    match n {
        0 -> "zero",
        1 -> "one",
        _ -> "many",
    }
}

fn main() {
    println(label(1))
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/int_literal_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define ptr @label(i64 %n)",
		"icmp eq i64 %n, 0",
		"icmp eq i64 %n, 1",
		"select i1",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "LLVM013") {
		t.Fatalf("emitted LLVM013 unsupported diagnostic where the new path should fire:\n%s", got)
	}
}

// TestGenerateIntLiteralMatchTwoArmsUsesSelect verifies the small-arm
// case (one literal + wildcard) still routes through the select-chain
// builder rather than falling back to the if-expr-phi path. Two arms
// with a single icmp + select keeps the arithmetic-style match cheap.
func TestGenerateIntLiteralMatchTwoArmsUsesSelect(t *testing.T) {
	file := parseLLVMGenFile(t, `fn isZero(n: Int) -> Int {
    match n {
        0 -> 1,
        _ -> 0,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/int_literal_match_2arm.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define i64 @isZero(i64 %n)",
		"icmp eq i64 %n, 0",
		"select i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
	// Two-arm select-safe path must NOT use phi (which is the chain fallback).
	if strings.Contains(got, "phi i64") {
		t.Fatalf("two-arm select-safe match should not emit phi:\n%s", got)
	}
}

// TestGenerateIntLiteralMatchNonSelectSafeUsesIfPhi exercises the
// fallback path: when an arm body is a multi-statement block (not
// select-safe), the chain emitter splits into nested if-then-else with
// phi instead of select. The asserted shape mirrors what
// emitTagEnumMatchChainValue produces for payload-free tag enums.
func TestGenerateIntLiteralMatchNonSelectSafeUsesIfPhi(t *testing.T) {
	file := parseLLVMGenFile(t, `fn classify(n: Int) -> Int {
    match n {
        0 -> {
            let base = 10
            base + 1
        },
        1 -> {
            let base = 20
            base + 2
        },
        _ -> {
            let base = 30
            base + 3
        },
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/int_literal_match_chain.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define i64 @classify(i64 %n)",
		"icmp eq i64 %n, 0",
		"icmp eq i64 %n, 1",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
}

func TestGenerateCharLiteralMatchExpression(t *testing.T) {
	file := parseLLVMGenFile(t, `fn digit(ch: Char) -> Int {
    match ch {
        '0' -> 0,
        '1' -> 1,
        _ -> -1,
    }
}

fn main() {
    println(digit('1'))
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/char_literal_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define i64 @digit(i32",
		"icmp eq i32",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "LLVM013") {
		t.Fatalf("emitted LLVM013 unsupported diagnostic where char-literal match lowering should fire:\n%s", got)
	}
}

func TestGenerateIntLiteralNarrowsWithByteAndCharSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn byteOr(default: Byte) -> Byte {
    default
}

fn charOr(default: Char) -> Char {
    default
}

fn b() -> Byte {
    byteOr(0)
}

fn c() -> Char {
    charOr(65)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/int_literal_byte_char_hint.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define i8 @byteOr(i8 %default)",
		"define i32 @charOr(i32 %default)",
		"call i8 @byteOr(i8 0)",
		"call i32 @charOr(i32 65)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateIntLiteralNarrowsInsideByteContainersAndFallbacks(t *testing.T) {
	file := parseLLVMGenFile(t, `fn fromList() -> Byte {
    let xs: List<Byte> = [0, 255]
    xs[1]
}

fn fromMap(m: Map<String, Byte>) -> Byte {
    m.getOr("x", 0)
}

fn fromOption(x: Byte?) -> Byte {
    x.unwrapOr(0)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/int_literal_byte_containers.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"store i8 0, ptr",
		"store i8 255, ptr",
		"call i1 @osty_rt_map_get_string(",
		"load i8, ptr",
		"phi i8",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "function \"byteOr\" arg 1 type i64") ||
		strings.Contains(got, "want i8") {
		t.Fatalf("Byte-hinted Int literals regressed to i64:\n%s", got)
	}
}
