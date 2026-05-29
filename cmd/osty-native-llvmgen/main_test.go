package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/mirjson"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestWritePackageRequestPreservesRelativePaths(t *testing.T) {
	req := llvmgenRequest{
		Path: "/repo/demo/src/main.osty",
		Package: &llvmgenPackageInput{Files: []llvmgenPackageFile{
			{Path: "/repo/demo/main.osty", Name: "main.osty", Source: "fn root() {}\n"},
			{Path: "/repo/demo/src/main.osty", Name: filepath.Join("src", "main.osty"), Source: "fn nested() {}\n"},
		}},
	}

	root, entry, err := writePackageRequest(req)
	if err != nil {
		t.Fatalf("writePackageRequest error: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if got, want := entry, filepath.Join(root, "src", "main.osty"); got != want {
		t.Fatalf("entry = %q, want %q", got, want)
	}
	if got := readTextFile(t, filepath.Join(root, "main.osty")); got != "fn root() {}\n" {
		t.Fatalf("root file = %q", got)
	}
	if got := readTextFile(t, filepath.Join(root, "src", "main.osty")); got != "fn nested() {}\n" {
		t.Fatalf("nested file = %q", got)
	}
}

func TestWritePackageRequestRejectsDuplicateDestinations(t *testing.T) {
	req := llvmgenRequest{
		Path: "/repo/demo/main.osty",
		Package: &llvmgenPackageInput{Files: []llvmgenPackageFile{
			{Name: "main.osty", Source: "fn a() {}\n"},
			{Name: "main.osty", Source: "fn b() {}\n"},
		}},
	}
	root, _, err := writePackageRequest(req)
	if root != "" {
		t.Cleanup(func() { os.RemoveAll(root) })
	}
	if err == nil || !strings.Contains(err.Error(), "duplicate materialized path") {
		t.Fatalf("writePackageRequest err = %v, want duplicate materialized path", err)
	}
}

func TestRunEmitsLLVMIRForMIRPayload(t *testing.T) {
	bin := buildFakeNativeLIRProto(t)
	t.Setenv("OSTY_NATIVE_LIRPROTO_BIN", bin)
	t.Setenv("FAKE_NATIVE_LIRPROTO_RESPONSE", `{"llvmIr":"; lir-proto\ndefine i64 @main() { ret i64 7 }\n"}`)

	entry := prepareMIRPayloadEntry(t, "fn main() -> Int { 7 }\n")
	payload, err := mirjson.FromModule(entry.MIR)
	if err != nil {
		t.Fatalf("encode MIR: %v", err)
	}
	reqBody, err := json.Marshal(llvmgenRequest{
		Path: entry.SourcePath,
		MIR: &llvmgenMIRInput{
			PackageName: entry.PackageName,
			SourcePath:  entry.SourcePath,
			Source:      string(entry.Source),
			Module:      payload,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(reqBody), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true; warnings=%v", resp.Warnings)
	}
	if !strings.Contains(resp.LLVMIR, "define i64 @main()") || !strings.Contains(resp.LLVMIR, "ret i64 7") {
		t.Fatalf("llvmIr missing MIR-emitted main return:\n%s", resp.LLVMIR)
	}
}

func TestRunMIRPayloadPrefersLIRProtoBridge(t *testing.T) {
	bin := buildFakeNativeLIRProto(t)
	capture := filepath.Join(t.TempDir(), "lir-request.json")
	t.Setenv("OSTY_NATIVE_LIRPROTO_BIN", bin)
	t.Setenv("FAKE_NATIVE_LIRPROTO_CAPTURE", capture)
	t.Setenv("FAKE_NATIVE_LIRPROTO_RESPONSE", `{"llvmIr":"; lir-proto\ndefine i64 @main() { ret i64 99 }\n"}`)

	entry := prepareMIRPayloadEntry(t, "fn main() -> Int { 7 }\n")
	payload, err := mirjson.FromModule(entry.MIR)
	if err != nil {
		t.Fatalf("encode MIR: %v", err)
	}
	reqBody, err := json.Marshal(llvmgenRequest{
		Path: entry.SourcePath,
		MIR: &llvmgenMIRInput{
			PackageName: entry.PackageName,
			SourcePath:  entry.SourcePath,
			Source:      string(entry.Source),
			Target:      "x86_64-unknown-linux-gnu",
			Module:      payload,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(reqBody), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered || !strings.Contains(resp.LLVMIR, "ret i64 99") {
		t.Fatalf("response did not use LIR Proto IR: %#v", resp)
	}
	var lirReq struct {
		PackageName string          `json:"packageName"`
		SourcePath  string          `json:"sourcePath"`
		Source      string          `json:"source"`
		Target      string          `json:"target"`
		MIR         json.RawMessage `json:"mir"`
	}
	decodeCapturedLIRRequest(t, capture, &lirReq)
	if lirReq.PackageName != "main" || lirReq.SourcePath != entry.SourcePath || !strings.Contains(lirReq.Source, "fn main() -> Int { 7 }") || lirReq.Target != "x86_64-unknown-linux-gnu" || len(lirReq.MIR) == 0 {
		t.Fatalf("captured LIR request = %#v", lirReq)
	}
}

func TestRunMIRPayloadDeclinesWhenLIRProtoDeclines(t *testing.T) {
	bin := buildFakeNativeLIRProto(t)
	t.Setenv("OSTY_NATIVE_LIRPROTO_BIN", bin)
	t.Setenv("FAKE_NATIVE_LIRPROTO_RESPONSE", `{"declined":true,"error":"not ready"}`)

	entry := prepareMIRPayloadEntry(t, "fn main() -> Int { 7 }\n")
	payload, err := mirjson.FromModule(entry.MIR)
	if err != nil {
		t.Fatalf("encode MIR: %v", err)
	}
	reqBody, err := json.Marshal(llvmgenRequest{
		Path: entry.SourcePath,
		MIR: &llvmgenMIRInput{
			PackageName: entry.PackageName,
			SourcePath:  entry.SourcePath,
			Source:      string(entry.Source),
			Module:      payload,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(reqBody), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Covered || resp.LLVMIR != "" {
		t.Fatalf("declined LIR Proto should not fall back to Go MIR IR: %#v", resp)
	}
	if len(resp.Warnings) < 2 || !strings.Contains(strings.Join(resp.Warnings, "\n"), "not ready") || !strings.Contains(strings.Join(resp.Warnings, "\n"), "Go MIR emitter fallback has been removed") {
		t.Fatalf("fallback warnings = %#v, want decline reason", resp.Warnings)
	}
}

func prepareMIRPayloadEntry(t *testing.T, src string) backend.Entry {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	reg := stdlib.LoadCached()
	res := resolve.ResolveFileSourceDefault([]byte(src), file, reg)
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	entry, err := backend.PrepareEntry("main", "/tmp/mir_payload.osty", file, res, chk)
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	entry.Source = []byte(src)
	return entry
}

var (
	fakeNativeLIRProtoOnce sync.Once
	fakeNativeLIRProtoPath string
	fakeNativeLIRProtoErr  error
)

func buildFakeNativeLIRProto(t *testing.T) string {
	t.Helper()
	fakeNativeLIRProtoOnce.Do(func() {
		fakeNativeLIRProtoPath, fakeNativeLIRProtoErr = buildFakeNativeLIRProtoOnce()
	})
	if fakeNativeLIRProtoErr != nil {
		t.Fatal(fakeNativeLIRProtoErr)
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

func decodeCapturedLIRRequest(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
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
	resp := os.Getenv("FAKE_NATIVE_LIRPROTO_RESPONSE")
	if resp == "" {
		resp = ` + "`" + `{"declined":true,"error":"fake response missing"}` + "`" + `
	}
	os.Stdout.WriteString(resp)
}
`

func TestRunRejectsInvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := run(strings.NewReader("{"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "decode llvmgen request") {
		t.Fatalf("run error = %v, want decode error", err)
	}
}
