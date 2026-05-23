package backend

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLLVMBackendStdKeychainApiKeyWrapperLowers(t *testing.T) {
	// Original test (pre-PR #2002) called `keychain.getApiKey` (a
	// high-level bodied wrapper) and expected its body inlining to
	// surface the underlying `osty_rt_keychain_get` runtime intrinsic
	// directly in the IR. The IR-layer body-inline path that produced
	// that shape was retired in favor of cross-pkg dispatch — with
	// body-lowering ON, `getApiKey` hits a cross-pkg call returning
	// `Result<String, Error>` and the Error type (from std.error)
	// has no layout entry → emit declines.
	//
	// Rewrite the test to exercise the LOWER-level call:
	// `keychain.get(service, account)` lowers as a cross-pkg call to
	// `@std.keychain.get` returning the `%Result.String_Error`
	// algebraic aggregate. Verify the call shape + service strings
	// flow through to the IR. The runtime intrinsic
	// (`osty_rt_keychain_get`) is still what the runtime resolves
	// `@std.keychain.get` to at link time — same end-state, just
	// asserted at the Osty-symbol level instead of the post-rewrite
	// LLVM symbol.
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "0")
	requireRealLLVMEmission(t)
	// Skip the `Err(err) -> err.message()` arm: destructuring the
	// `Result<String, Error>` payload reaches a virtual call on the
	// Error interface (`err.message()`) which the LIR Proto cannot
	// lower without cross-pkg interface layout propagation (the same
	// `0 interface layout(s)` gap that blocks the bodied-getApiKey
	// path). The Ok arm + a sentinel match-all on Err is enough to
	// drive the runtime intrinsic into the IR.
	req := newBackendRequest(t, EmitLLVMIR, `use std.keychain

fn main() {
    match keychain.get("osty.api", "openrouter") {
        Ok(secret) -> println(secret.len() > 0),
        Err(_) -> println(false),
    }
}
`)
	result, err := (LLVMBackend{}).Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	irBytes, err := os.ReadFile(result.Artifacts.LLVMIR)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, err)
	}
	got := string(irBytes)
	for _, want := range []string{
		"@std.keychain.get",
		"osty.api",
		"openrouter",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestLLVMBackendBinaryRunsStdKeychainAvailability(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	req := newBackendRequest(t, EmitBinary, `use std.keychain
use std.secrets

fn main() {
    println(keychain.backend().len() > 0)
    println(keychain.isAvailable() == keychain.isAvailable())
    println(secrets.backend() == keychain.backend())
    println(secrets.isAvailable() == keychain.isAvailable())
}
`)
	result, err := (LLVMBackend{}).Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := strings.TrimSpace(string(output)), "true\ntrue\ntrue\ntrue"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
