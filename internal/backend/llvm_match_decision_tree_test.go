package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsResultMatchPayloadBinding exercises the MIR
// regression where `match result { Ok(v) -> …, Err(e) -> … }` lost its
// decision tree after monomorph-clone (cloneMatchStmt intentionally
// drops Tree so backends recompile on demand). The MIR lowerer's
// fallback path unconditionally gotoed armBlocks[0], which caused two
// visible symptoms:
//
//   - the Err arm was unreachable (always printed the Ok case)
//   - the payload binding `v` resolved to a const fn reference
//     ("const fn v" in MIR; `@v` at LLVM level → global-ref clang error)
//
// The fix recompiles the decision tree in lowerMatch when it's nil.
// This end-to-end test confirms the binary actually dispatches on the
// discriminant and binds the Ok payload as an SSA register.
func TestLLVMBackendBinaryRunsResultMatchPayloadBinding(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn tryParse(s: String) -> Result<Int, Error> {
    Ok(s.toInt()?)
}

fn main() {
    match tryParse("42") {
        Ok(v) -> println(v),
        Err(e) -> println(-1),
    }
    match tryParse("bad") {
        Ok(v) -> println(v),
        Err(e) -> println(-1),
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n-1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsPrimitiveLiteralMatch covers the same
// monomorph-drops-Tree regression on a primitive-literal `match x { 1
// -> …, 2 -> …, _ -> … }`. Before the fix, every iteration printed
// arm 0's body (the fallback's unconditional goto armBlocks[0]),
// collapsing the switch to a no-op.
func TestLLVMBackendBinaryRunsPrimitiveLiteralMatch(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    for x in [1, 2, 3] {
        match x {
            1 -> println(10),
            2 -> println(20),
            _ -> println(0),
        }
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "10\n20\n0\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsCharLiteralMatch covers the Char (i32)
// scrutinee case for the same regression — before the fix the MIR
// emitter walled on "match scrutinee type i32, want enum tag", and
// the MIR path silently took arm 0.
func TestLLVMBackendBinaryRunsCharLiteralMatch(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn classify(c: Char) -> Int {
    match c {
        'a' -> 1,
        'z' -> 2,
        _ -> 0,
    }
}

fn main() {
    println(classify('a'))
    println(classify('m'))
    println(classify('z'))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n0\n2\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsBareEnumIdentMatch(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `enum AuthKind {
    NoAuth
    Plain
    Login
}

fn score(kind: AuthKind) -> Int {
    match kind {
        NoAuth -> 10,
        Plain -> 20,
        Login -> 30,
    }
}

fn main() {
    println(score(Plain))
    println(score(Login))
    println(score(NoAuth))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "20\n30\n10\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsCharMethodInterpolation(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn shout(s: String) -> String {
    let out = ""
    for c in s.chars() {
        out = "{out}{c.toUpper()}"
    }
    out
}

fn main() {
    println(shout("aZ"))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "AZ\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsInjectedStdBytesFrom(t *testing.T) {
	requireClangForBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.bytes

fn main() {
    let raw: List<Byte> = [65.toByte(), 66.toByte()]
    let data = bytes.from(raw)
    println(data.len())
    println(data.toHex())
}
`)
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "2\n4142\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
