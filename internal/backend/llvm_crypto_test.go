package backend

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
)

var ostyTestStringEscaper = strings.NewReplacer(
	"\\", "\\\\",
	"\"", "\\\"",
	"{", "\\{",
	"}", "\\}",
)

func ostyEscapeTestString(s string) string {
	return ostyTestStringEscaper.Replace(s)
}

func TestLLVMBackendBinaryRunsStdCryptoDigestsAndHMAC(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.crypto

fn main() {
    let data = "abc".toBytes()
    let key = "key".toBytes()
    let msg = "The quick brown fox jumps over the lazy dog".toBytes()

    println(crypto.sha256(data).toHex())
    println(crypto.sha512(data).toHex())
    println(crypto.sha1(data).toHex())
    println(crypto.md5(data).toHex())
    println(crypto.hmac.sha256(key, msg).toHex())
    println(crypto.hmac.sha512(key, msg).toHex())
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

	want := "" +
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n" +
		"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a" +
		"2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f\n" +
		"a9993e364706816aba3e25717850c26c9cd0d89d\n" +
		"900150983cd24fb0d6963f7d28e17f72\n" +
		"f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8\n" +
		"b42af09057bac1e2d41708e48a902e09b5ff7f12ab428a4fe86653c73dd248fb" +
		"82f948a549f7b791a5b41915ee4d1ec3935357e4e2317250d0372afa2ebeeb3a\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsStdCryptoRandomAndConstantTimeEq(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.crypto

fn main() {
    let token = crypto.randomBytes(32)
    println(token.len())
    println(crypto.constantTimeEq(token, token))
    println(crypto.constantTimeEq(token, "different".toBytes()))
    println(crypto.randomBytes(0).len())
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
	if got, want := string(output), "32\ntrue\nfalse\n0\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsStdCryptoLongInputs(t *testing.T) {
	parallelClangBackendTest(t)

	dataText := strings.Repeat("abc", 1000)
	keyText := strings.Repeat("a", 200)
	dataBytes := []byte(dataText)
	keyBytes := []byte(keyText)
	sha256Sum := sha256.Sum256(dataBytes)
	sha512Sum := sha512.Sum512(dataBytes)
	hmac256 := hmac.New(sha256.New, keyBytes)
	hmac256.Write(dataBytes)
	hmac512 := hmac.New(sha512.New, keyBytes)
	hmac512.Write(dataBytes)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.crypto

fn main() {
    let data = "abc".toBytes().repeat(1000)
    let key = "a".toBytes().repeat(200)
    println(crypto.sha256(data).toHex())
    println(crypto.sha512(data).toHex())
    println(crypto.hmac.sha256(key, data).toHex())
    println(crypto.hmac.sha512(key, data).toHex())
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

	want := "" +
		hex.EncodeToString(sha256Sum[:]) + "\n" +
		hex.EncodeToString(sha512Sum[:]) + "\n" +
		hex.EncodeToString(hmac256.Sum(nil)) + "\n" +
		hex.EncodeToString(hmac512.Sum(nil)) + "\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdCryptoMatchesGoReferenceMatrix(t *testing.T) {
	parallelClangBackendTest(t)

	type cryptoCase struct {
		data string
		key  string
	}
	cases := []cryptoCase{
		{data: "", key: ""},
		{data: "a", key: "z"},
		{data: "osty-crypto-regression", key: "integration-key"},
		{data: "punctuation-!@#$%^&*()-_=+[]{};:,.<>/?", key: "K3y-With-Mixed-ASCII"},
		{data: strings.Repeat("ab", 257), key: strings.Repeat("cd", 131)},
	}

	var src strings.Builder
	src.WriteString("use std.crypto\n\n")
	src.WriteString("fn emitCase(dataText: String, keyText: String) {\n")
	src.WriteString("    let data = dataText.toBytes()\n")
	src.WriteString("    let key = keyText.toBytes()\n")
	src.WriteString("    println(crypto.sha256(data).toHex())\n")
	src.WriteString("    println(crypto.sha512(data).toHex())\n")
	src.WriteString("    println(crypto.sha1(data).toHex())\n")
	src.WriteString("    println(crypto.md5(data).toHex())\n")
	src.WriteString("    println(crypto.hmac.sha256(key, data).toHex())\n")
	src.WriteString("    println(crypto.hmac.sha512(key, data).toHex())\n")
	src.WriteString("    println(crypto.constantTimeEq(data, data))\n")
	src.WriteString("    println(crypto.constantTimeEq(data, key))\n")
	src.WriteString("}\n\n")
	src.WriteString("fn main() {\n")
	for _, tc := range cases {
		src.WriteString(`    emitCase("`)
		src.WriteString(ostyEscapeTestString(tc.data))
		src.WriteString(`", "`)
		src.WriteString(ostyEscapeTestString(tc.key))
		src.WriteString(`")` + "\n")
	}
	src.WriteString("}\n")

	var want strings.Builder
	for _, tc := range cases {
		data := []byte(tc.data)
		key := []byte(tc.key)
		sha256Sum := sha256.Sum256(data)
		sha512Sum := sha512.Sum512(data)
		sha1Sum := sha1.Sum(data)
		md5Sum := md5.Sum(data)
		h256 := hmac.New(sha256.New, key)
		h256.Write(data)
		h512 := hmac.New(sha512.New, key)
		h512.Write(data)
		want.WriteString(hex.EncodeToString(sha256Sum[:]) + "\n")
		want.WriteString(hex.EncodeToString(sha512Sum[:]) + "\n")
		want.WriteString(hex.EncodeToString(sha1Sum[:]) + "\n")
		want.WriteString(hex.EncodeToString(md5Sum[:]) + "\n")
		want.WriteString(hex.EncodeToString(h256.Sum(nil)) + "\n")
		want.WriteString(hex.EncodeToString(h512.Sum(nil)) + "\n")
		want.WriteString("true\n")
		if hmac.Equal(data, key) {
			want.WriteString("true\n")
		} else {
			want.WriteString("false\n")
		}
	}

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, src.String())
	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got := string(output); got != want.String() {
		t.Fatalf("binary stdout = %q, want %q", got, want.String())
	}
}
