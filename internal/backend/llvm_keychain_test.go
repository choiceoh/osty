package backend

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLLVMBackendStdKeychainApiKeyWrapperLowers(t *testing.T) {
	// Assert the `getApiKey` wrapper path on the production stdlib-body
	// injection path: `requireName("provider", ...)` delegates to
	// `get(defaultApiKeyService, ...)`. Skip the `Err(err) -> err.message()`
	// arm because virtual dispatch on the cross-pkg Error interface still
	// needs cross-pkg vtable injection. The Ok arm + a sentinel match-all on
	// Err is enough to drive the wrapper into the IR.
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
