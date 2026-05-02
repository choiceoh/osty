package nativelirproto

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	fakeNativeLIRProtoOnce sync.Once
	fakeNativeLIRProtoPath string
	fakeNativeLIRProtoErr  error
)

// TestRunUsesEnvBinaryAndDecodesResponse pins the env-override path:
// `OSTY_NATIVE_LIRPROTO_BIN` should bypass `ensureManagedBinary`,
// the request payload should arrive intact at the binary's stdin,
// and the response JSON should round-trip into the typed struct.
func TestRunUsesEnvBinaryAndDecodesResponse(t *testing.T) {
	bin := buildFakeNativeLIRProto(t)
	capture := filepath.Join(t.TempDir(), "request.json")

	t.Setenv(Env, bin)
	t.Setenv("FAKE_NATIVE_LIRPROTO_CAPTURE", capture)
	t.Setenv("FAKE_NATIVE_LIRPROTO_RESPONSE", `{"llvmIr":"define i64 @main()"}`)

	oldEnsure := ensureManagedBinary
	ensureManagedBinary = func(string) (string, error) {
		t.Fatal("ensureManagedBinary should not be called when env override is set")
		return "", nil
	}
	t.Cleanup(func() { ensureManagedBinary = oldEnsure })

	resp, err := Run(".", Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      "fn main() {}\n",
		Target:      "arm64-apple-darwin",
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if resp.LLVMIR != "define i64 @main()" {
		t.Fatalf("llvm ir = %q, want %q", resp.LLVMIR, "define i64 @main()")
	}
	if resp.Declined {
		t.Fatalf("Declined = true, want false on success response")
	}

	var req Request
	decodeCapturedRequest(t, capture, &req)
	if req.PackageName != "main" {
		t.Fatalf("request packageName = %q, want main", req.PackageName)
	}
	if req.Source != "fn main() {}\n" {
		t.Fatalf("request source = %q, want preserved", req.Source)
	}
	if req.Target != "arm64-apple-darwin" {
		t.Fatalf("request target = %q, want arm64-apple-darwin", req.Target)
	}
}

// TestRunSurfacesDeclinedResponse pins the fall-back signaling
// shape: a `{"declined": true, "error": "..."}` body decodes into
// the typed struct and propagates back to the caller, where the
// Phase-7 dispatcher converts it into a structured warning.
func TestRunSurfacesDeclinedResponse(t *testing.T) {
	bin := buildFakeNativeLIRProto(t)

	t.Setenv(Env, bin)
	t.Setenv("FAKE_NATIVE_LIRPROTO_RESPONSE", `{"declined":true,"error":"unsupported source shape"}`)

	resp, err := Run(".", Request{Source: "fn main() {}\n"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !resp.Declined {
		t.Fatal("Declined = false, want true")
	}
	if resp.Error != "unsupported source shape" {
		t.Fatalf("error = %q, want %q", resp.Error, "unsupported source shape")
	}
}

func buildFakeNativeLIRProto(t *testing.T) string {
	t.Helper()
	fakeNativeLIRProtoOnce.Do(func() {
		fakeNativeLIRProtoPath, fakeNativeLIRProtoErr = buildFakeNativeLIRProtoOnce()
	})
	if fakeNativeLIRProtoErr != nil {
		t.Fatalf("build fake native lirproto: %v", fakeNativeLIRProtoErr)
	}
	return fakeNativeLIRProtoPath
}

func buildFakeNativeLIRProtoOnce() (string, error) {
	dir, err := os.MkdirTemp("", "fake-native-lirproto-*")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeNativeLIRProtoProgram), 0o644); err != nil {
		return "", fmt.Errorf("write fake native lirproto: %w", err)
	}
	name := "fake-native-lirproto"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build fake native lirproto: %w\n%s", err, out)
	}
	return bin, nil
}

func decodeCapturedRequest(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

const fakeNativeLIRProtoProgram = `package main

import (
	"io"
	"os"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	if capture := os.Getenv("FAKE_NATIVE_LIRPROTO_CAPTURE"); capture != "" {
		if err := os.WriteFile(capture, data, 0o644); err != nil {
			panic(err)
		}
	}
	_, _ = os.Stdout.WriteString(os.Getenv("FAKE_NATIVE_LIRPROTO_RESPONSE"))
}
`
