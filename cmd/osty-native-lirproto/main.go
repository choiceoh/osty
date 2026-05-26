// Command osty-native-lirproto is the subprocess half of the
// Phase-7 LIR Proto runner bridge. It reads a `nativelirproto.Request`-
// shaped JSON payload on stdin and writes a `nativelirproto.Response`
// JSON object on stdout.
//
// Slice-2 contract: the binary's body is now a thin Go shim that
// stages either source or an already-lowered MIR JSON payload to a
// temp file and forks the self-hosted `osty-self` binary. Source
// requests use `lir-proto-lower`; MIR requests use
// `lir-proto-lower-mir-json` so production backends do not re-enter
// the still-partial Osty source compiler.
//
// Failure modes (returned as `declined: true` so the dispatcher
// falls back to the legacy MIR-direct emit instead of hard-failing):
//
//   - osty-self binary not found (run `osty build toolchain/` first)
//   - osty-self exits non-zero on the lowering subcommand
//   - the staged source can't be read back / written
//
// Tests of the wire shape itself live in `internal/nativelirproto`
// (`TestRunUsesEnvBinaryAndDecodesResponse`,
// `TestRunSurfacesDeclinedResponse`) and use a fake binary instead
// of this one.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/mirjson"
	"github.com/osty/osty/internal/nativelirproto"
	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// SelfBinEnv overrides the osty-self lookup. Local development /
// CI uses this to point at a freshly built artifact without having
// to write to the default `.osty/out/...` cache path. The actual
// lookup is delegated to `selfhostcache.ResolveBinary` which honours
// the same env var.
const SelfBinEnv = "OSTY_SELF_BIN"

// selfForwardArgsEnv is the argv forwarding channel consumed by
// toolchain/main.osty before it falls back to std.env.args().
const selfForwardArgsEnv = "OSTY_SELF_REBUILD_FORWARD_ARGS"

// selfLowerTimeoutEnv overrides the bootstrap guard used around
// self-hosted LIR Proto lowering. Use "0" to disable.
const selfLowerTimeoutEnv = "OSTY_LIRPROTO_SELF_TIMEOUT"

// selfTimeoutCompatMaxBytesEnv bounds the legacy stage0 retry after a
// MIR JSON timeout. Use "0" to disable timeout compatibility retries.
const selfTimeoutCompatMaxBytesEnv = "OSTY_LIRPROTO_TIMEOUT_COMPAT_MAX_BYTES"

// selfSourceCompatMaxBytesEnv bounds the legacy source retry used
// only when an older osty-self cannot handle MIR JSON directly.
// Use "0" to disable the size guard.
const selfSourceCompatMaxBytesEnv = "OSTY_LIRPROTO_SOURCE_COMPAT_MAX_BYTES"

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	var req nativelirproto.Request
	dec := json.NewDecoder(stdin)
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		return fmt.Errorf("decode lirproto request: %w", err)
	}
	resp, err := lower(req)
	if err != nil {
		// Non-decode errors flow back as a structured response so
		// the runner-side dispatcher can convert them into a
		// fall-back warning. stderr stays clean for hard exits
		// (decode failures, env issues).
		resp = nativelirproto.Response{Declined: true, Error: err.Error()}
	}
	return json.NewEncoder(stdout).Encode(resp)
}

func lower(req nativelirproto.Request) (nativelirproto.Response, error) {
	selfBin, err := resolveOstySelfBin()
	if err != nil {
		// Missing osty-self is a recoverable fall-back, not a hard
		// exit — Phase-7 keeps gate-on/gate-off output identical
		// when the self-host artifact isn't built yet.
		return nativelirproto.Response{Declined: true, Error: err.Error()}, nil
	}
	stagedPath, command, cleanup, err := stageInput(req)
	if cleanup != nil && os.Getenv("OSTY_LIRPROTO_KEEP_STAGED") == "" {
		defer cleanup()
	}
	if err != nil {
		return nativelirproto.Response{}, err
	}
	if dbg := os.Getenv("OSTY_LIRPROTO_DEBUG"); dbg != "" {
		fmt.Fprintf(os.Stderr, "[lirproto-debug] staged %s=%s\n", command, stagedPath)
	}

	pkgName := req.PackageName
	if pkgName == "" {
		pkgName = "main"
	}
	args := []string{command, stagedPath, "--package-name=" + pkgName}
	if req.Target != "" {
		args = append(args, "--target="+req.Target)
	}

	resp, declineReason, err := runSelfLower(selfBin, args, stagedPath)
	if err != nil || !resp.Declined {
		return resp, err
	}
	if command == "lir-proto-lower-mir-json" && shouldAttemptMIRJSONFallback(declineReason, stagedPath) {
		return lowerLegacyMIRJSON(req, selfBin)
	}
	return resp, nil
}

func lowerSourceCompat(selfBin string, req nativelirproto.Request) (nativelirproto.Response, error) {
	sourcePath, _, cleanup, err := stageInput(req)
	if cleanup != nil && os.Getenv("OSTY_LIRPROTO_KEEP_STAGED") == "" {
		defer cleanup()
	}
	if err != nil {
		return nativelirproto.Response{}, err
	}
	pkgName := req.PackageName
	if pkgName == "" {
		pkgName = "main"
	}
	args := []string{"lir-proto-lower", sourcePath, "--package-name=" + pkgName}
	if req.Target != "" {
		args = append(args, "--target="+req.Target)
	}
	resp, _, err := runSelfLower(selfBin, args, sourcePath)
	return resp, err
}

func runSelfLower(selfBin string, args []string, stagedPath string) (nativelirproto.Response, string, error) {
	ctx := context.Background()
	timeout := selfLowerTimeout(args, stagedPath)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, selfBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), selfForwardArgsEnv+"="+strings.Join(args, "\n"))
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			command := "command"
			if len(args) > 0 {
				command = args[0]
			}
			msg := fmt.Sprintf("osty-self %s timed out after %s", command, timeout)
			return nativelirproto.Response{Declined: true, Error: msg}, msg, nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nativelirproto.Response{Declined: true, Error: msg}, msg, nil
	}
	ir := normalizeSelfHostedLLVMIR(stdout.String())
	if ir == "" {
		command := "command"
		if len(args) > 0 {
			command = args[0]
		}
		msg := fmt.Sprintf("osty-self %s produced empty IR", command)
		return nativelirproto.Response{Declined: true, Error: msg}, msg, nil
	}
	if reason := validateSelfHostedLLVMIR(ir); reason != "" {
		command := "command"
		if len(args) > 0 {
			command = args[0]
		}
		msg := fmt.Sprintf("osty-self %s produced invalid IR: %s", command, reason)
		return nativelirproto.Response{Declined: true, Error: msg}, msg, nil
	}
	return nativelirproto.Response{LLVMIR: ir}, "", nil
}

func selfLowerTimeout(args []string, stagedPath string) time.Duration {
	if len(args) == 0 {
		return 0
	}
	switch args[0] {
	case "lir-proto-lower-mir-json", "lir-proto-lower":
	default:
		return 0
	}
	if raw := os.Getenv(selfLowerTimeoutEnv); raw != "" {
		if raw == "0" {
			return 0
		}
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	timeout := 20 * time.Second
	if args[0] == "lir-proto-lower-mir-json" {
		timeout += mirJSONSizeTimeoutBudget(stagedInputSize(stagedPath))
	}
	return timeout
}

func mirJSONSizeTimeoutBudget(size int64) time.Duration {
	if size <= 0 {
		return 0
	}
	const mib = int64(1 << 20)
	chunks := size / mib
	if chunks == 0 {
		return 0
	}
	// Large self-rebuild MIR payloads are tens of MiB and can spend
	// several minutes in the Osty-owned LIR Proto backend before
	// producing IR. Keep small probes snappy, but give real toolchain
	// payloads enough room to finish instead of falling back to a
	// partial stage0 bootstrap compiler.
	budget := time.Duration(chunks) * 15 * time.Second
	if budget > 10*time.Minute {
		return 10 * time.Minute
	}
	return budget
}

func stagedInputSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

func normalizeSelfHostedLLVMIR(ir string) string {
	return strings.NewReplacer(
		`\"`, `"`,
		`\{`, `{`,
		`\}`, `}`,
		`\n`, "\n",
		`\\`, `\`,
	).Replace(ir)
}

func isUnsupportedSelfCommand(msg string) bool {
	return strings.Contains(msg, "osty-self: unsupported command") ||
		(strings.Contains(msg, "unsupported command") && strings.Contains(msg, "lir-proto-lower-mir-json"))
}

func isInvalidSelfHostedIR(msg string) bool {
	return strings.Contains(msg, "produced invalid IR")
}

func isSelfHostedTimeout(msg string) bool {
	return strings.Contains(msg, "timed out")
}

func shouldAttemptMIRJSONFallback(msg, stagedPath string) bool {
	if isUnsupportedSelfCommand(msg) || isInvalidSelfHostedIR(msg) {
		return true
	}
	if !isSelfHostedTimeout(msg) {
		return false
	}
	max := timeoutCompatMaxBytes()
	if max <= 0 {
		return false
	}
	size := stagedInputSize(stagedPath)
	return size > 0 && size <= max
}

func validateSelfHostedLLVMIR(ir string) string {
	for i := 0; i < 512; i++ {
		name := fmt.Sprintf("%%t%d", i)
		if strings.Contains(ir, name) && !strings.Contains(ir, name+" =") {
			return "temporary " + name + " is referenced before definition"
		}
	}
	return ""
}

func lowerLegacyMIRJSON(req nativelirproto.Request, selfBin string) (nativelirproto.Response, error) {
	// Older bootstrap seeds know `lir-proto-lower` but predate the
	// MIR JSON entry point. Prefer the already-lowered MIR payload so
	// object/library requests keep their caller-provided entrypoint,
	// then use source re-lowering only as a last compatibility step.
	stage0Resp, triedStage0 := lowerMIRJSONStage0Compat(req)
	if triedStage0 && !stage0Resp.Declined {
		return stage0Resp, nil
	}
	if req.Source == "" {
		if triedStage0 {
			return stage0Resp, nil
		}
		return nativelirproto.Response{Declined: true, Error: "legacy osty-self does not support MIR JSON requests"}, nil
	}
	if max := sourceCompatMaxBytes(); max > 0 && len([]byte(req.Source)) > max {
		sourceResp := nativelirproto.Response{
			Declined: true,
			Error:    fmt.Sprintf("legacy source compat skipped: source is %d bytes, exceeds %d byte guard", len([]byte(req.Source)), max),
		}
		if !triedStage0 {
			return sourceResp, nil
		}
		return nativelirproto.Response{
			Declined: true,
			Error:    combineDeclineErrors(stage0Resp.Error, sourceResp.Error),
		}, nil
	}
	sourceReq := req
	sourceReq.MIR = nil
	sourceResp, err := lowerSourceCompat(selfBin, sourceReq)
	if err != nil || !sourceResp.Declined || !triedStage0 {
		return sourceResp, err
	}
	return nativelirproto.Response{
		Declined: true,
		Error:    combineDeclineErrors(stage0Resp.Error, sourceResp.Error),
	}, nil
}

func lowerMIRJSONStage0Compat(req nativelirproto.Request) (nativelirproto.Response, bool) {
	if req.MIR == nil {
		return nativelirproto.Response{}, false
	}
	encoded, err := json.Marshal(req.MIR)
	if err != nil {
		return nativelirproto.Response{Declined: true, Error: fmt.Sprintf("stage0 MIR compat: marshal MIR JSON: %v", err)}, true
	}
	var payload mirjson.Module
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nativelirproto.Response{Declined: true, Error: fmt.Sprintf("stage0 MIR compat: decode MIR JSON: %v", err)}, true
	}
	mod, err := mirjson.ToModule(&payload)
	if err != nil {
		return nativelirproto.Response{Declined: true, Error: fmt.Sprintf("stage0 MIR compat: convert MIR JSON: %v", err)}, true
	}
	if len(mod.Functions) == 0 {
		return nativelirproto.Response{Declined: true, Error: "stage0 MIR compat: module has no functions"}, true
	}
	if errs := mir.Validate(mod); len(errs) > 0 {
		return nativelirproto.Response{Declined: true, Error: fmt.Sprintf("stage0 MIR compat: invalid MIR: %s", joinErrors(errs))}, true
	}
	pkgName := req.PackageName
	if pkgName == "" {
		pkgName = payload.PackageName
	}
	if pkgName == "" {
		pkgName = "main"
	}
	ir, err := stage0.EmitMIRAllowNoMain(mod, llvmabi.Options{
		PackageName: pkgName,
		SourcePath:  req.SourcePath,
		Source:      []byte(req.Source),
		Target:      req.Target,
		UseMIR:      true,
		EmitGC:      true,
	})
	if err != nil {
		if len(ir) > 0 && stage0.IsListAllDeclines() {
			return nativelirproto.Response{LLVMIR: string(ir)}, true
		}
		return nativelirproto.Response{Declined: true, Error: fmt.Sprintf("stage0 MIR compat: %v", err)}, true
	}
	return nativelirproto.Response{LLVMIR: string(ir)}, true
}

func joinErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "; ")
}

func combineDeclineErrors(primary, secondary string) string {
	switch {
	case primary == "":
		return secondary
	case secondary == "":
		return primary
	default:
		return primary + "; source compat: " + secondary
	}
}

func sourceCompatMaxBytes() int {
	if raw := os.Getenv(selfSourceCompatMaxBytesEnv); raw != "" {
		if raw == "0" {
			return 0
		}
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n >= 0 {
			return n
		}
	}
	return 1 << 20
}

func timeoutCompatMaxBytes() int64 {
	if raw := os.Getenv(selfTimeoutCompatMaxBytesEnv); raw != "" {
		if raw == "0" {
			return 0
		}
		var n int64
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n >= 0 {
			return n
		}
	}
	return 1 << 20
}

// resolveOstySelfBin returns the path to the self-host `osty-self`
// binary the lower call should subprocess.
//
// Lookup order (delegated to `selfhostcache.ResolveBinaryWithFetch`):
//
//  1. `$OSTY_SELF_BIN` env override.
//  2. `toolchain/.osty/out/{debug,release}/llvm/osty-self` — the
//     in-tree build path.
//  3. `.osty/cache/self-host/<sha>-<triple>/osty-self` — the
//     content-addressed artifact cache.
//  4. Network fetch from `$OSTY_SELF_REGISTRY_URL` — only consulted
//     when the env var is set and `$OSTY_SELF_REGISTRY_OFFLINE` is
//     unset. A successful fetch promotes the binary into the local
//     cache so subsequent invocations short-circuit at step 3.
//
// On the no-cache path (env unset, no in-tree build, no cache hit,
// no registry / network failure), the canonical "osty-self not found"
// message is preserved so the upstream `backend.IsOstySelfMissing`
// detection chain keeps working.
func resolveOstySelfBin() (string, error) {
	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		return "", err
	}
	bin, _, err := selfhostcache.ResolveBinaryWithFetch(context.Background(), root, selfhostcache.EnvFetcher())
	if errors.Is(err, selfhostcache.ErrNotCached) {
		return "", errors.New("osty-self not found; run `osty build toolchain/` or set OSTY_SELF_BIN")
	}
	if err != nil {
		return "", err
	}
	return bin, nil
}

// stageInput writes either the source text or the already-lowered MIR
// JSON to a temp file so the osty-self subcommand can open it. MIR
// wins even when callers attach source text for diagnostics; otherwise
// the bridge silently re-enters source lowering and loses the MIR path.
func stageInput(req nativelirproto.Request) (string, string, func(), error) {
	root, err := os.MkdirTemp("", "osty-native-lirproto-*")
	if err != nil {
		return "", "", nil, err
	}
	cleanup := func() {
		if os.Getenv("OSTY_KEEP_TMP") != "" {
			return
		}
		os.RemoveAll(root)
	}

	if req.MIR != nil {
		data, err := json.Marshal(req.MIR)
		if err != nil {
			return "", "", cleanup, fmt.Errorf("marshal MIR JSON input: %w", err)
		}
		name := "main.mir.json"
		if req.SourcePath != "" {
			base := filepath.Base(req.SourcePath)
			ext := filepath.Ext(base)
			if ext != "" {
				base = strings.TrimSuffix(base, ext)
			}
			if base != "" && base != "." {
				name = base + ".mir.json"
			}
		}
		stagedPath := filepath.Join(root, name)
		if err := os.WriteFile(stagedPath, data, 0o644); err != nil {
			return "", "", cleanup, err
		}
		return stagedPath, "lir-proto-lower-mir-json", cleanup, nil
	}

	name := "main.osty"
	if req.SourcePath != "" {
		name = filepath.Base(req.SourcePath)
	}
	data := []byte(req.Source)
	if len(data) == 0 && req.SourcePath != "" {
		if read, err := os.ReadFile(req.SourcePath); err == nil {
			data = read
		}
	}
	if len(data) == 0 {
		return "", "", cleanup, errors.New("source text is empty; lir-proto-lower requires source")
	}
	stagedPath := filepath.Join(root, name)
	if err := os.WriteFile(stagedPath, data, 0o644); err != nil {
		return "", "", cleanup, err
	}
	return stagedPath, "lir-proto-lower", cleanup, nil
}
