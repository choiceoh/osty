package backend

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLLVMBackendStdKeychainApiKeyWrapperLowers(t *testing.T) {
	// Assert the `keychain.getApiKey` wrapper survives lowering as a
	// cross-pkg symbol (`@std.keychain.getApiKey`) so regressions that
	// drop the wrapper are caught. The Ok arm plus a match-all Err arm
	// drives the wrapper without requiring virtual dispatch on
	// `std.error.Error` in the Err branch.
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
