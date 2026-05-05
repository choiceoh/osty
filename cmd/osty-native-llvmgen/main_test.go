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
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mirjson"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func runLLVMGenSource(t *testing.T, path string, source string) llvmgenResponse {
	t.Helper()
	reqBody, err := json.Marshal(llvmgenRequest{Path: path, Source: source})
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
	return resp
}

func TestRunEmitsNativeOwnedLLVMIRForSource(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"fn pick(flag: Bool) -> Int { if flag { 42 } else { 0 } }\nfn main() { let mut i = 0 let mut sum = 0 for i < 3 { sum = sum + pick(i == 2) i = i + 1 } println(sum) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	if !strings.Contains(resp.LLVMIR, "define i64 @pick(i1 %flag)") {
		t.Fatalf("llvmIr missing pick definition:\n%s", resp.LLVMIR)
	}
	if !strings.Contains(resp.LLVMIR, "for.cond") {
		t.Fatalf("llvmIr missing loop label:\n%s", resp.LLVMIR)
	}
}

func TestRunEmitsNativeOwnedLLVMIRForPackage(t *testing.T) {
	reqBody, err := json.Marshal(llvmgenRequest{
		Path: "b.osty",
		Package: &llvmgenPackageInput{
			Files: []llvmgenPackageFile{
				{Name: "a.osty", Source: "pub fn helper() -> Int { 1 }\n"},
				{Name: "b.osty", Source: "fn main() { println(helper()) }\n"},
			},
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
		t.Fatalf("covered = false, want true")
	}
	if !strings.Contains(resp.LLVMIR, "@helper") {
		t.Fatalf("llvmIr missing helper symbol:\n%s", resp.LLVMIR)
	}
	if !strings.Contains(resp.LLVMIR, "@main") {
		t.Fatalf("llvmIr missing main symbol:\n%s", resp.LLVMIR)
	}
}

func TestRunEmitsLLVMIRForMIRPayload(t *testing.T) {
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
	if !strings.Contains(resp.LLVMIR, "define i64 @main()") || !strings.Contains(resp.LLVMIR, "store i64 7") {
		t.Fatalf("llvmIr missing MIR-emitted main return:\n%s", resp.LLVMIR)
	}
}

func TestRunMIRPayloadPrefersLIRProtoWhenSelected(t *testing.T) {
	bin := buildFakeNativeLIRProto(t)
	capture := filepath.Join(t.TempDir(), "lir-request.json")
	t.Setenv("OSTY_LLVM_LIR_PROTO", "1")
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
		PackageName string `json:"packageName"`
		SourcePath  string `json:"sourcePath"`
		Source      string `json:"source"`
		Target      string `json:"target"`
	}
	decodeCapturedLIRRequest(t, capture, &lirReq)
	if lirReq.PackageName != "main" || lirReq.SourcePath != entry.SourcePath || lirReq.Source == "" || lirReq.Target != "x86_64-unknown-linux-gnu" {
		t.Fatalf("captured LIR request = %#v", lirReq)
	}
}

func TestRunMIRPayloadFallsBackWhenLIRProtoDeclines(t *testing.T) {
	bin := buildFakeNativeLIRProto(t)
	t.Setenv("OSTY_LLVM_LIR_PROTO", "1")
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
	if !resp.Covered || !strings.Contains(resp.LLVMIR, "store i64 7") {
		t.Fatalf("fallback did not emit Go MIR IR: %#v", resp)
	}
	if len(resp.Warnings) == 0 || !strings.Contains(resp.Warnings[0], "not ready") {
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

func TestRunEmitsNativeOwnedLLVMIRForStructFieldAssign(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"struct Pair { left: Int, right: Int }\nfn main() { let mut pair = Pair { left: 1, right: 2 } pair.left = 3 println(pair.left) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"%Pair = type { i64, i64 }",
		"extractvalue %Pair",
		"insertvalue %Pair",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunEmitsNativeOwnedLLVMIRForListIndex(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"fn main() { let xs = [1, 2] println(xs[0]) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"declare ptr @osty_rt_list_new()",
		"call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_i64(",
		"call i64 @osty_rt_list_get_i64(",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunReportsCoveredForOptionalCoalesceSource(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"fn resolve(name: String?) -> String {\n    name ?? \"anonymous\"\n}\n\nfn main() {}\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"define ptr @resolve(ptr %name)",
		"coalesce.some",
		"coalesce.none",
		"phi ptr",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunUsesNativeOwnedExportAndCABIShape(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"#[export(\"osty.gc.native_entry_v1\")]\n#[c_abi]\npub fn native_entry_v1() -> Int { 0 }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	if !strings.Contains(resp.LLVMIR, "define ccc i64 @native_entry_v1()") {
		t.Fatalf("llvmIr missing C ABI function definition:\n%s", resp.LLVMIR)
	}
	if !strings.Contains(resp.LLVMIR, "@osty.gc.native_entry_v1 = dso_local alias ptr, ptr @native_entry_v1") {
		t.Fatalf("llvmIr missing export alias:\n%s", resp.LLVMIR)
	}
}

func TestRunCoversRuntimeStringsSplitAndListToSet(t *testing.T) {
	source := `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    let items = strings.Split("pear,apple", ",")
    let seen = items.toSet()
    println(seen.contains("pear"))
}
`
	resp := runLLVMGenSource(t, "main.osty", source)
	if !resp.Covered {
		entry, err := prepareSourceEntry(llvmgenRequest{Path: "main.osty", Source: source})
		if err != nil {
			t.Fatalf("covered = false, want true; prepareSourceEntry error: %v", err)
		}
		t.Fatalf("covered = false, want true\n%s", ostyir.Print(entry.IR))
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"call ptr @osty_rt_strings_Split(",
		"declare ptr @osty_rt_list_to_set_string(ptr)",
		"call i1 @osty_rt_set_contains_string(",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunDefersStdTestingHelpersToMIRBackend(t *testing.T) {
	resp := runLLVMGenSource(t, "main.osty", `use std.testing

enum CalcError {
    DivideByZero,
}

fn div(a: Int, b: Int) -> Result<Int, CalcError> {
    if b == 0 { Err(DivideByZero) } else { Ok(a / b) }
}

fn main() {
    let q = testing.expectOk(div(10, 2))
    testing.assertEq(q, 5)
    testing.expectError(div(1, 0))
}
`)
	if resp.Covered {
		t.Fatalf("covered = true, want false (std.testing should route to MIR backend)")
	}
}

func TestRunCoversNestedStructBindingPattern(t *testing.T) {
	resp := runLLVMGenSource(t, "main.osty", `struct Inner {
    x: Int
}

struct Outer {
    inner: Inner
}

fn main() {
    let outer @ Outer { inner: Inner { x } } = Outer { inner: Inner { x: 7 } }
    println(x)
    println(outer.inner.x)
}
`)
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"%Inner = type { i64 }",
		"%Outer = type { %Inner }",
		"extractvalue %Outer",
		"extractvalue %Inner",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunRejectsInvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := run(strings.NewReader("{"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "decode llvmgen request") {
		t.Fatalf("run error = %v, want decode error", err)
	}
}
