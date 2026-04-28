package llvmgen

import (
	"strings"
	"testing"
)

func TestStdCryptoHashRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.crypto

fn main() {
    let digest = crypto.sha256("abc".toBytes())
    println(digest.toHex())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_crypto_sha256.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_crypto_sha256(ptr)",
		"call ptr @osty_rt_crypto_sha256",
		"declare ptr @osty_rt_bytes_to_hex(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdCryptoHMACRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.crypto as c

fn main() {
    let mac = c.hmac.sha512("key".toBytes(), "abc".toBytes())
    println(mac.toHex())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_crypto_hmac_sha512.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_crypto_hmac_sha512(ptr, ptr)",
		"call ptr @osty_rt_crypto_hmac_sha512",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdCryptoRandomBytesRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.crypto

fn main() {
    let secret = crypto.randomBytes(32)
    println(secret.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_crypto_random_bytes.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_crypto_random_bytes(i64)",
		"call ptr @osty_rt_crypto_random_bytes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdCryptoConstantTimeEqRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.crypto

fn main() {
    println(crypto.constantTimeEq("abc".toBytes(), "abc".toBytes()))
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_crypto_constant_time_eq.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_crypto_constant_time_eq(ptr, ptr)",
		"call i1 @osty_rt_crypto_constant_time_eq",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
