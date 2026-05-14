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

	"github.com/osty/osty/internal/nativelirproto"
)

// TestRunDeclinesWhenOstySelfMissing pins the Slice-2 fall-back
// contract: when neither the `OSTY_SELF_BIN` override nor the
// default `toolchain/.osty/out/...` paths resolve to a real
// binary, `run` returns a `declined: true` response (with the
// missing-binary error in `error`) instead of hard-failing. The
// dispatcher's existing fall-back-on-error policy then emits a
// structured warning and continues with the legacy MIR-direct
// emit. Without this contract, every gate-on caller in a fresh
// worktree would block until `osty build toolchain/` ran first.
func TestRunDeclinesWhenOstySelfMissing(t *testing.T) {
	t.Setenv(SelfBinEnv, "")
	// CWD-relative search hits non-existent paths in t.TempDir.
	t.Chdir(t.TempDir())

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      "fn main() {}\n",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(body), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp nativelirproto.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if !resp.Declined {
		t.Fatalf("Declined = false, want true: %+v", resp)
	}
	if !strings.Contains(resp.Error, "osty-self not found") {
		t.Fatalf("Error = %q, want missing-binary message", resp.Error)
	}
}

// TestRunInvokesOstySelfWithRequestArgs pins the args+stdout
// contract between the Go bridge and `osty-self lir-proto-lower`:
// the bridge stages the source to a temp file, spawns osty-self
// with `lir-proto-lower <staged-path> --package-name=NAME [--target=T]`,
// and forwards stdout to `LLVMIR`. The test uses a fake osty-self
// binary to capture the args and stdin/source content without
// requiring a real self-host build.
func TestRunInvokesOstySelfWithRequestArgs(t *testing.T) {
	bin := buildFakeOstySelf(t)
	captureDir := t.TempDir()
	captureArgs := filepath.Join(captureDir, "args.json")
	captureForwardArgs := filepath.Join(captureDir, "forward_args.txt")
	captureSource := filepath.Join(captureDir, "source.osty")

	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_ARGS", captureArgs)
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_FORWARD_ARGS", captureForwardArgs)
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_SOURCE", captureSource)
	t.Setenv("FAKE_OSTY_SELF_STDOUT", "; lir-proto IR\ndefine i64 @check(i64 %x) {\n  ret i64 %x\n}\n")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      "fn check(n: Int) -> Int { n }\n",
		Target:      "arm64-apple-darwin",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(body), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp nativelirproto.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Declined {
		t.Fatalf("Declined = true, want false: %+v", resp)
	}
	if resp.Error != "" {
		t.Fatalf("Error = %q, want empty: %+v", resp.Error, resp)
	}
	if !strings.Contains(resp.LLVMIR, "define i64 @check(i64 %x)") {
		t.Fatalf("LLVMIR missing fake stdout pass-through:\n%s", resp.LLVMIR)
	}

	args := readCapturedArgs(t, captureArgs)
	if len(args) < 3 {
		t.Fatalf("captured args too short: %v", args)
	}
	if args[0] != "lir-proto-lower" {
		t.Fatalf("args[0] = %q, want lir-proto-lower", args[0])
	}
	if !strings.HasSuffix(args[1], "main.osty") {
		t.Fatalf("args[1] = %q, want staged main.osty path", args[1])
	}
	wantPkg := "--package-name=main"
	wantTarget := "--target=arm64-apple-darwin"
	if !contains(args, wantPkg) {
		t.Fatalf("args missing %q: %v", wantPkg, args)
	}
	if !contains(args, wantTarget) {
		t.Fatalf("args missing %q: %v", wantTarget, args)
	}
	forwarded := strings.Split(readFile(t, captureForwardArgs), "\n")
	if len(forwarded) != len(args) {
		t.Fatalf("forwarded arg count = %d, want %d (%q)", len(forwarded), len(args), readFile(t, captureForwardArgs))
	}
	for i := range args {
		if forwarded[i] != args[i] {
			t.Fatalf("forwarded arg[%d] = %q, want %q (payload %q)", i, forwarded[i], args[i], readFile(t, captureForwardArgs))
		}
	}

	source := readFile(t, captureSource)
	if !strings.Contains(source, "fn check(n: Int) -> Int { n }") {
		t.Fatalf("staged source content unexpected: %q", source)
	}
}

// TestRunMIRPayloadInvokesMirJSONLower pins the production self-host
// boundary: when callers have already produced MIR, the bridge stages
// that JSON and invokes osty-self's MIR-input subcommand instead of
// re-entering the still-partial Osty source compiler.
func TestRunMIRPayloadInvokesMirJSONLower(t *testing.T) {
	bin := buildFakeOstySelf(t)
	captureDir := t.TempDir()
	captureArgs := filepath.Join(captureDir, "args.json")
	captureInput := filepath.Join(captureDir, "input.mir.json")

	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_ARGS", captureArgs)
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_SOURCE", captureInput)
	t.Setenv("FAKE_OSTY_SELF_STDOUT", "; lir-proto MIR IR\ndefine i64 @main() {\n  ret i64 42\n}\n")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      "fn stale_source_path_should_not_be_lowered() {}\n",
		Target:      "x86_64-unknown-linux-gnu",
		MIR: map[string]any{
			"version":     1,
			"packageName": "main",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(body), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp nativelirproto.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Declined {
		t.Fatalf("Declined = true, want false: %+v", resp)
	}

	args := readCapturedArgs(t, captureArgs)
	if len(args) < 3 {
		t.Fatalf("captured args too short: %v", args)
	}
	if args[0] != "lir-proto-lower-mir-json" {
		t.Fatalf("args[0] = %q, want lir-proto-lower-mir-json", args[0])
	}
	if !strings.HasSuffix(args[1], "main.mir.json") {
		t.Fatalf("args[1] = %q, want staged main.mir.json path", args[1])
	}
	if !contains(args, "--target=x86_64-unknown-linux-gnu") {
		t.Fatalf("args missing target: %v", args)
	}
	staged := readFile(t, captureInput)
	if !strings.Contains(staged, `"version":1`) || !strings.Contains(staged, `"packageName":"main"`) {
		t.Fatalf("staged MIR JSON missing payload: %q", staged)
	}
	if strings.Contains(staged, "stale_source_path_should_not_be_lowered") {
		t.Fatalf("staged MIR JSON unexpectedly contains source text: %q", staged)
	}
}

// TestRunDeclinesWhenOstySelfExitsNonZero pins the second
// fall-back: a non-zero exit from osty-self (parse failure,
// unsupported MIR shape, etc.) lands as a `declined: true`
// response carrying the binary's stderr in `error`. The bridge
// never propagates the subprocess error as a hard exit — that
// would defeat the whole point of the dispatcher's fall-back.
func TestRunDeclinesWhenOstySelfExitsNonZero(t *testing.T) {
	bin := buildFakeOstySelf(t)
	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_EXIT", "2")
	t.Setenv("FAKE_OSTY_SELF_STDERR", "lir-proto-lower: parse error at line 3\n")
	t.Setenv("FAKE_OSTY_SELF_STDOUT", "")

	body, err := json.Marshal(nativelirproto.Request{Source: "fn"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(body), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp nativelirproto.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Declined {
		t.Fatalf("Declined = false, want true: %+v", resp)
	}
	if !strings.Contains(resp.Error, "parse error") {
		t.Fatalf("Error = %q, want stderr passthrough", resp.Error)
	}
}

// ---- fake osty-self binary helpers ----

var (
	fakeOstySelfOnce sync.Once
	fakeOstySelfPath string
	fakeOstySelfErr  error
)

func buildFakeOstySelf(t *testing.T) string {
	t.Helper()
	fakeOstySelfOnce.Do(func() {
		fakeOstySelfPath, fakeOstySelfErr = buildFakeOstySelfOnce()
	})
	if fakeOstySelfErr != nil {
		t.Fatalf("build fake osty-self: %v", fakeOstySelfErr)
	}
	return fakeOstySelfPath
}

func buildFakeOstySelfOnce() (string, error) {
	dir, err := os.MkdirTemp("", "fake-osty-self-*")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeOstySelfProgram), 0o644); err != nil {
		return "", fmt.Errorf("write fake osty-self: %w", err)
	}
	name := "fake-osty-self"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build fake osty-self: %w\n%s", err, out)
	}
	return bin, nil
}

func readCapturedArgs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read captured args %s: %v", path, err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatalf("decode captured args: %v", err)
	}
	return args
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

const fakeOstySelfProgram = `package main

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
)

func main() {
	if path := os.Getenv("FAKE_OSTY_SELF_CAPTURE_ARGS"); path != "" {
		data, err := json.Marshal(os.Args[1:])
		if err == nil {
			_ = os.WriteFile(path, data, 0o644)
		}
	}
	if path := os.Getenv("FAKE_OSTY_SELF_CAPTURE_FORWARD_ARGS"); path != "" {
		_ = os.WriteFile(path, []byte(os.Getenv("OSTY_SELF_REBUILD_FORWARD_ARGS")), 0o644)
	}
	if path := os.Getenv("FAKE_OSTY_SELF_CAPTURE_SOURCE"); path != "" && len(os.Args) > 2 {
		// Args[2] is the staged source path (lir-proto-lower <path> ...).
		// Copy its bytes into the capture file so tests can assert on
		// the source contents the bridge passed through.
		if src, err := os.Open(os.Args[2]); err == nil {
			defer src.Close()
			if dst, err := os.Create(path); err == nil {
				defer dst.Close()
				_, _ = io.Copy(dst, src)
			}
		}
	}
	if msg := os.Getenv("FAKE_OSTY_SELF_STDERR"); msg != "" {
		_, _ = os.Stderr.WriteString(msg)
	}
	if out := os.Getenv("FAKE_OSTY_SELF_STDOUT"); out != "" {
		_, _ = os.Stdout.WriteString(out)
	}
	if exit := os.Getenv("FAKE_OSTY_SELF_EXIT"); exit != "" {
		code, err := strconv.Atoi(exit)
		if err == nil {
			os.Exit(code)
		}
	}
}
`
