// Command osty-native-lirproto is the subprocess half of the
// Phase-7 LIR Proto runner bridge. It reads a `LIRProtoRequest`-
// shaped JSON payload on stdin and writes a `LIRProtoResponse`
// JSON object on stdout.
//
// Slice-2 contract: the binary's body is now a thin Go shim that
// stages the source to a temp file and forks the self-hosted
// `osty-self` binary's `lir-proto-lower` subcommand to do the
// actual lowering through the Osty-owned `toolchain/lir_proto.osty`
// pipeline (HIR → MIR → LIR Proto → LLVM IR). The wire shape stays
// identical to Slice 1 — callers in `internal/nativelirproto`,
// `cmd/osty/lir_proto_bridge.go`, and the tests are untouched.
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/nativelirproto"
)

// SelfBinEnv overrides the osty-self lookup. Local development /
// CI uses this to point at a freshly built artifact without having
// to write to the default `.osty/out/...` cache path.
const SelfBinEnv = "OSTY_SELF_BIN"

// defaultSelfBinCandidates lists the paths searched (in order)
// when SelfBinEnv is unset. They mirror the layout `osty build
// toolchain/` writes by default.
var defaultSelfBinCandidates = []string{
	"toolchain/.osty/out/debug/llvm/osty-self",
	"toolchain/.osty/out/release/llvm/osty-self",
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	var req nativelirproto.Request
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
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
	sourcePath, cleanup, err := stageSource(req)
	if cleanup != nil {
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

	cmd := exec.Command(selfBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nativelirproto.Response{Declined: true, Error: msg}, nil
	}
	ir := stdout.String()
	if ir == "" {
		return nativelirproto.Response{Declined: true, Error: "osty-self lir-proto-lower produced empty IR"}, nil
	}
	return nativelirproto.Response{LLVMIR: ir}, nil
}

// resolveOstySelfBin returns the path to the self-host `osty-self`
// binary the lower call should subprocess. SelfBinEnv wins outright;
// otherwise we search the default `osty build toolchain/` output
// paths relative to the current working directory.
func resolveOstySelfBin() (string, error) {
	if override := strings.TrimSpace(os.Getenv(SelfBinEnv)); override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		return "", fmt.Errorf("%s=%q not found", SelfBinEnv, override)
	}
	for _, rel := range defaultSelfBinCandidates {
		abs, err := filepath.Abs(rel)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
	}
	return "", errors.New("osty-self not found; run `osty build toolchain/` or set OSTY_SELF_BIN")
}

// stageSource writes the request's source bytes to a temp file so
// the osty-self subcommand can open it. The original `sourcePath`
// is preserved as the basename when set (callers expect the file
// name to round-trip through the staged copy for diagnostic
// `source_filename` lines).
func stageSource(req nativelirproto.Request) (string, func(), error) {
	root, err := os.MkdirTemp("", "osty-native-lirproto-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(root) }

	name := "main.osty"
	if req.SourcePath != "" {
		name = filepath.Base(req.SourcePath)
	}
	stagedPath := filepath.Join(root, name)
	if err := os.WriteFile(stagedPath, []byte(req.Source), 0o644); err != nil {
		return "", cleanup, err
	}
	return stagedPath, cleanup, nil
}
