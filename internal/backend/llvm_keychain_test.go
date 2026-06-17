package backend

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLLVMBackendStdKeychainApiKeyWrapperLowers(t *testing.T) {
	// Pre-PR #2003 the test asserted the `osty_rt_keychain_get`
	// runtime intrinsic appeared in the IR via body inlining of the
	// `getApiKey` wrapper. That IR-layer inline path was retired in
	// favor of cross-pkg dispatch — with body-lowering ON, `getApiKey`
	// now hits a cross-pkg call whose `Result<String, Error>` return
	// references the `Error` interface from std.error. PRs #2004 /
	// #2007 land the type-lowering + interface-boxing pieces but the
	// vtable injection + init-globals render path are still gaps.
	// Re-enable once cross-pkg vtable injection covers the wrapper path
	// with stdlib body injection always on (OSTY_STDLIB_BODY_LOWER gate
	// retired).
	t.Skip("blocked on cross-pkg vtable injection for keychain.getApiKey with stdlib body injection ON")
	requireRealLLVMEmission(t)
	req := newBackendRequest(t, EmitLLVMIR, `use std.keychain

fn main() {
    match keychain.getApiKey("openrouter") {
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
		"@std.keychain.getApiKey",
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
