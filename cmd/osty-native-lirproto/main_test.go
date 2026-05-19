package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/mirjson"
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

func TestRunMIRPayloadNormalizesSelfHostedEscapedLLVM(t *testing.T) {
	bin := buildFakeOstySelf(t)
	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_STDOUT", `; lir-proto\nsource_filename = \"demo\"\ndefine i64 @main() \{\n  ret i64 42\n\}\n`)

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
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
	want := "; lir-proto\nsource_filename = \"demo\"\ndefine i64 @main() {\n  ret i64 42\n}\n"
	if resp.LLVMIR != want {
		t.Fatalf("LLVMIR normalization drifted:\n--- got ---\n%q\n--- want ---\n%q", resp.LLVMIR, want)
	}
}

func TestValidateSelfHostedLLVMIRCatchesUndefinedTemps(t *testing.T) {
	ir := "define i64 @main() {\nentry:\n  ret i64 %t0\n}\n"
	if got := validateSelfHostedLLVMIR(ir); !strings.Contains(got, "%t0") {
		t.Fatalf("validateSelfHostedLLVMIR() = %q, want undefined %%t0", got)
	}
	valid := "define i64 @main() {\nentry:\n  %t0 = add i64 0, 42\n  ret i64 %t0\n}\n"
	if got := validateSelfHostedLLVMIR(valid); got != "" {
		t.Fatalf("validateSelfHostedLLVMIR(valid) = %q, want empty", got)
	}
}

func TestRunMIRPayloadRetriesSourceWhenMirJSONUnsupported(t *testing.T) {
	bin := buildFakeOstySelf(t)
	captureDir := t.TempDir()
	captureArgs := filepath.Join(captureDir, "args.json")
	captureSource := filepath.Join(captureDir, "source.osty")

	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_REJECT_MIR_JSON", "1")
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_ARGS", captureArgs)
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_SOURCE", captureSource)
	t.Setenv("FAKE_OSTY_SELF_STDOUT", "; compat source IR\ndefine i64 @main() {\n  ret i64 7\n}\n")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      "fn main() -> Int { 7 }\n",
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
	if !strings.Contains(resp.LLVMIR, "compat source IR") {
		t.Fatalf("LLVMIR missing compat output:\n%s", resp.LLVMIR)
	}
	args := readCapturedArgs(t, captureArgs)
	if len(args) < 2 || args[0] != "lir-proto-lower" {
		t.Fatalf("compat args = %v, want source lir-proto-lower retry", args)
	}
	if staged := readFile(t, captureSource); !strings.Contains(staged, "fn main() -> Int { 7 }") {
		t.Fatalf("compat retry staged %q, want original source", staged)
	}
}

func TestRunMIRPayloadUsesStage0CompatWhenMirJSONUnsupported(t *testing.T) {
	bin := buildFakeOstySelf(t)
	captureDir := t.TempDir()
	captureArgs := filepath.Join(captureDir, "args.json")

	payload, err := mirjson.FromModule(&mir.Module{
		Package: "main",
		Functions: []*mir.Function{
			{
				Name:        "zero",
				ReturnType:  ir.TInt,
				ReturnLocal: 0,
				Locals: []*mir.Local{
					{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
				},
				Entry: 0,
				Blocks: []*mir.BasicBlock{
					{
						ID: 0,
						Instrs: []mir.Instr{
							&mir.AssignInstr{
								Dest: mir.Place{Local: 0},
								Src: &mir.UseRV{Op: &mir.ConstOp{
									Const: &mir.IntConst{Value: 7, T: ir.TInt},
									T:     ir.TInt,
								}},
							},
						},
						Term: &mir.ReturnTerm{},
					},
				},
			},
		},
		Layouts: mir.NewLayoutTable(),
	})
	if err != nil {
		t.Fatalf("build MIR JSON payload: %v", err)
	}

	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_REJECT_MIR_JSON", "1")
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_ARGS", captureArgs)
	t.Setenv("FAKE_OSTY_SELF_STDOUT", "; source fallback should not run\n")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/zero.osty",
		MIR:         payload,
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
	if !strings.Contains(resp.LLVMIR, "define i64 @zero()") || !strings.Contains(resp.LLVMIR, "ret i64 7") {
		t.Fatalf("LLVMIR missing stage0 compat function:\n%s", resp.LLVMIR)
	}
	if strings.Contains(resp.LLVMIR, "source fallback should not run") {
		t.Fatalf("stage0 compat unexpectedly used source fallback:\n%s", resp.LLVMIR)
	}
	args := readCapturedArgs(t, captureArgs)
	if len(args) < 2 || args[0] != "lir-proto-lower-mir-json" {
		t.Fatalf("first args = %v, want MIR JSON attempt before compat", args)
	}
}

func TestRunMIRPayloadPreservesLargeIntJSONNumbers(t *testing.T) {
	bin := buildFakeOstySelf(t)

	payload, err := mirjson.FromModule(&mir.Module{
		Package: "main",
		Functions: []*mir.Function{
			{
				Name:        "main",
				ReturnType:  ir.TInt,
				ReturnLocal: 0,
				Locals: []*mir.Local{
					{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
				},
				Entry: 0,
				Blocks: []*mir.BasicBlock{
					{
						ID: 0,
						Instrs: []mir.Instr{
							&mir.AssignInstr{
								Dest: mir.Place{Local: 0},
								Src: &mir.UseRV{Op: &mir.ConstOp{
									Const: &mir.IntConst{Value: math.MaxInt64, T: ir.TInt},
									T:     ir.TInt,
								}},
							},
						},
						Term: &mir.ReturnTerm{},
					},
				},
			},
		},
		Layouts: mir.NewLayoutTable(),
	})
	if err != nil {
		t.Fatalf("build MIR JSON payload: %v", err)
	}

	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_REJECT_MIR_JSON", "1")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		MIR:         payload,
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
	if !strings.Contains(resp.LLVMIR, "ret i64 9223372036854775807") {
		t.Fatalf("LLVMIR lost large int literal:\n%s", resp.LLVMIR)
	}
}

func TestRunMIRPayloadAcceptsStage0PartialIRWhenListAllDeclines(t *testing.T) {
	bin := buildFakeOstySelf(t)

	payload, err := mirjson.FromModule(&mir.Module{
		Package: "main",
		Functions: []*mir.Function{
			{
				Name:        "main",
				ReturnType:  ir.TInt,
				ReturnLocal: 0,
				Locals: []*mir.Local{
					{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
				},
				Entry: 0,
				Blocks: []*mir.BasicBlock{
					{
						ID: 0,
						Instrs: []mir.Instr{
							&mir.AssignInstr{
								Dest: mir.Place{Local: 0},
								Src: &mir.UseRV{Op: &mir.ConstOp{
									Const: &mir.IntConst{Value: 7, T: ir.TInt},
									T:     ir.TInt,
								}},
							},
						},
						Term: &mir.ReturnTerm{},
					},
				},
			},
			{
				Name:        "declinedHelper",
				ReturnType:  ir.TInt,
				ReturnLocal: 0,
				Locals: []*mir.Local{
					{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
				},
				Entry: 0,
				Blocks: []*mir.BasicBlock{
					{ID: 0, Term: &mir.GotoTerm{Target: 0}},
				},
			},
		},
		Layouts: mir.NewLayoutTable(),
	})
	if err != nil {
		t.Fatalf("build MIR JSON payload: %v", err)
	}

	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_REJECT_MIR_JSON", "1")
	t.Setenv(stage0.ListAllDeclinesEnv, "1")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		MIR:         payload,
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
		t.Fatalf("Declined = true, want partial stage0 IR: %+v", resp)
	}
	if !strings.Contains(resp.LLVMIR, "define i64 @main()") || !strings.Contains(resp.LLVMIR, "ret i64 7") {
		t.Fatalf("LLVMIR missing emitted main:\n%s", resp.LLVMIR)
	}
	if !strings.Contains(resp.LLVMIR, "define i64 @declinedHelper()") ||
		!strings.Contains(resp.LLVMIR, "osty_rt_stage0_declined") {
		t.Fatalf("LLVMIR missing decline stub:\n%s", resp.LLVMIR)
	}
}

func TestRunMIRPayloadUsesStage0CompatWhenMirJSONTimesOut(t *testing.T) {
	bin := buildFakeOstySelf(t)
	captureDir := t.TempDir()
	captureArgs := filepath.Join(captureDir, "args.json")

	payload, err := mirjson.FromModule(&mir.Module{
		Package: "main",
		Functions: []*mir.Function{
			{
				Name:        "main",
				ReturnType:  ir.TInt,
				ReturnLocal: 0,
				Locals: []*mir.Local{
					{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
				},
				Entry: 0,
				Blocks: []*mir.BasicBlock{
					{
						ID: 0,
						Instrs: []mir.Instr{
							&mir.AssignInstr{
								Dest: mir.Place{Local: 0},
								Src: &mir.UseRV{Op: &mir.ConstOp{
									Const: &mir.IntConst{Value: 42, T: ir.TInt},
									T:     ir.TInt,
								}},
							},
						},
						Term: &mir.ReturnTerm{},
					},
				},
			},
		},
		Layouts: mir.NewLayoutTable(),
	})
	if err != nil {
		t.Fatalf("build MIR JSON payload: %v", err)
	}

	t.Setenv(SelfBinEnv, bin)
	t.Setenv(selfLowerTimeoutEnv, "500ms")
	t.Setenv("FAKE_OSTY_SELF_SLEEP_MS", "3000")
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_ARGS", captureArgs)

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		MIR:         payload,
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
	if !strings.Contains(resp.LLVMIR, "define i64 @main()") || !strings.Contains(resp.LLVMIR, "ret i64 42") {
		t.Fatalf("LLVMIR missing timed-out stage0 compat fallback:\n%s", resp.LLVMIR)
	}
	args := readCapturedArgs(t, captureArgs)
	if len(args) < 2 || args[0] != "lir-proto-lower-mir-json" {
		t.Fatalf("first args = %v, want MIR JSON attempt before compat", args)
	}
}

func TestRunMIRPayloadSkipsTimeoutCompatWhenPayloadExceedsGuard(t *testing.T) {
	bin := buildFakeOstySelf(t)
	captureDir := t.TempDir()
	captureArgs := filepath.Join(captureDir, "args.json")

	t.Setenv(SelfBinEnv, bin)
	t.Setenv(selfLowerTimeoutEnv, "100ms")
	t.Setenv(selfTimeoutCompatMaxBytesEnv, "1")
	t.Setenv("FAKE_OSTY_SELF_SLEEP_MS", "3000")
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_ARGS", captureArgs)

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		MIR: map[string]any{
			"version":     1,
			"packageName": "main",
			"padding":     strings.Repeat("x", 2048),
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
	if !resp.Declined {
		t.Fatalf("Declined = false, want timeout decline: %+v", resp)
	}
	if !strings.Contains(resp.Error, "lir-proto-lower-mir-json timed out") {
		t.Fatalf("Error = %q, want MIR JSON timeout", resp.Error)
	}
	args := readCapturedArgs(t, captureArgs)
	if len(args) < 2 || args[0] != "lir-proto-lower-mir-json" {
		t.Fatalf("args = %v, want only MIR JSON attempt", args)
	}
}

func TestSelfLowerTimeoutScalesWithMirJSONSize(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.mir.json")
	large := filepath.Join(dir, "large.mir.json")
	if err := os.WriteFile(small, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write small payload: %v", err)
	}
	if err := os.WriteFile(large, bytes.Repeat([]byte{'x'}, 3<<20), 0o644); err != nil {
		t.Fatalf("write large payload: %v", err)
	}

	if got := selfLowerTimeout([]string{"lir-proto-lower-mir-json", small}, small); got != 20*time.Second {
		t.Fatalf("small MIR timeout = %s, want 20s", got)
	}
	if got := selfLowerTimeout([]string{"lir-proto-lower-mir-json", large}, large); got != 35*time.Second {
		t.Fatalf("large MIR timeout = %s, want 35s", got)
	}
	if got := selfLowerTimeout([]string{"lir-proto-lower", large}, large); got != 20*time.Second {
		t.Fatalf("source timeout = %s, want 20s", got)
	}
}

func TestRunSourceLowerTimesOut(t *testing.T) {
	bin := buildFakeOstySelf(t)
	t.Setenv(SelfBinEnv, bin)
	t.Setenv(selfLowerTimeoutEnv, "100ms")
	t.Setenv("FAKE_OSTY_SELF_SLEEP_MS", "3000")
	t.Setenv("FAKE_OSTY_SELF_STDOUT", "; should not arrive\ndefine i64 @main() {\n  ret i64 1\n}\n")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      "fn main() -> Int { 1 }\n",
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
		t.Fatalf("Declined = false, want timeout decline: %+v", resp)
	}
	if !strings.Contains(resp.Error, "lir-proto-lower timed out") {
		t.Fatalf("Error = %q, want source lower timeout", resp.Error)
	}
}

func TestRunMIRPayloadSkipsLargeLegacySourceCompat(t *testing.T) {
	bin := buildFakeOstySelf(t)
	captureDir := t.TempDir()
	captureArgs := filepath.Join(captureDir, "args.json")

	t.Setenv(SelfBinEnv, bin)
	t.Setenv("FAKE_OSTY_SELF_REJECT_MIR_JSON", "1")
	t.Setenv("FAKE_OSTY_SELF_CAPTURE_ARGS", captureArgs)
	t.Setenv("FAKE_OSTY_SELF_STDOUT", "; source compat should not run\ndefine i64 @main() {\n  ret i64 9\n}\n")
	t.Setenv(selfSourceCompatMaxBytesEnv, "8")

	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      strings.Repeat("fn x() {}\n", 4),
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
	if !resp.Declined {
		t.Fatalf("Declined = false, want guarded decline: %+v", resp)
	}
	if !strings.Contains(resp.Error, "legacy source compat skipped") {
		t.Fatalf("Error = %q, want source compat guard", resp.Error)
	}
	args := readCapturedArgs(t, captureArgs)
	if len(args) < 2 || args[0] != "lir-proto-lower-mir-json" {
		t.Fatalf("args = %v, want only MIR JSON attempt", args)
	}
}

func TestUnsupportedSelfCommandDetection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{msg: "osty-self: unsupported command; host compiler forwarding is disabled", want: true},
		{msg: "unsupported command: lir-proto-lower-mir-json", want: true},
		{msg: "unsupported command: compile", want: false},
		{msg: "parse error", want: false},
	}
	for _, tc := range cases {
		if got := isUnsupportedSelfCommand(tc.msg); got != tc.want {
			t.Fatalf("isUnsupportedSelfCommand(%q) = %t, want %t", tc.msg, got, tc.want)
		}
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
	"time"
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
	if sleep := os.Getenv("FAKE_OSTY_SELF_SLEEP_MS"); sleep != "" {
		ms, err := strconv.Atoi(sleep)
		if err == nil && ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}
	if os.Getenv("FAKE_OSTY_SELF_REJECT_MIR_JSON") != "" && len(os.Args) > 1 && os.Args[1] == "lir-proto-lower-mir-json" {
		_, _ = os.Stderr.WriteString("osty-self: unsupported command; host compiler forwarding is disabled\n")
		os.Exit(1)
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
