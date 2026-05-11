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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	sourcePath, cleanup, err := stageInput(req)
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
	command := "lir-proto-lower"
	if req.Source == "" && req.MIR != nil {
		command = "lir-proto-lower-mir-json"
	}
	args := []string{command, sourcePath, "--package-name=" + pkgName}
	if req.Target != "" {
		args = append(args, "--target="+req.Target)
	}

	cmd := exec.Command(selfBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), selfForwardArgsEnv+"="+strings.Join(args, "\n"))
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

// stageSource writes the request's source bytes to a temp file so
// the osty-self subcommand can open it. The original `sourcePath`
// is preserved as the basename when set (callers expect the file
// name to round-trip through the staged copy for diagnostic
// `source_filename` lines).
func stageInput(req nativelirproto.Request) (string, func(), error) {
	root, err := os.MkdirTemp("", "osty-native-lirproto-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(root) }

	name := "main.osty"
	if req.SourcePath != "" {
		name = filepath.Base(req.SourcePath)
	}
	data := []byte(req.Source)
	if req.Source == "" && req.MIR != nil {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".mir.json"
		encoded, err := json.Marshal(req.MIR)
		if err != nil {
			return "", cleanup, err
		}
		data = encoded
	}
	stagedPath := filepath.Join(root, name)
	if err := os.WriteFile(stagedPath, data, 0o644); err != nil {
		return "", cleanup, err
	}
	return stagedPath, cleanup, nil
}
