// Package nativelirproto is the Go-side bridge into the
// osty-native-lirproto subprocess. It mirrors `internal/nativellvmgen`
// in shape — Request / Response types + a single Run() that
// JSON-encodes the request, spawns the binary, and decodes the JSON
// response.
//
// Today the binary's internals are a thin Go wrapper around the
// existing MIR-direct emitter (so gate-on output matches gate-off
// exactly). The wire shape is what matters for Phase 7: a future
// slice will replace the binary's body with a real call into the
// Osty-owned `toolchain/lir_proto.osty` lowerer without touching
// any caller of this package.
package nativelirproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/osty/osty/internal/toolchain"
)

// Env names the override env var. When set, callers skip the
// managed-binary lookup and shell out directly. Used in tests and
// for local development.
const Env = "OSTY_NATIVE_LIRPROTO_BIN"

// Request is the single JSON payload the binary consumes via stdin.
// Mirrors `llvmgen.LIRProtoRequest` so the Go runner can forward
// its arg untouched.
type Request struct {
	PackageName string `json:"packageName,omitempty"`
	SourcePath  string `json:"sourcePath,omitempty"`
	Source      string `json:"source,omitempty"`
	Target      string `json:"target,omitempty"`
}

// Response is the JSON payload the binary writes to stdout. `LLVMIR`
// holds the full LLVM IR text on success; `Declined` is true when
// the binary couldn't lower the request and the dispatcher should
// fall back; `Error` carries a structured failure message.
type Response struct {
	LLVMIR   string `json:"llvmIr,omitempty"`
	Declined bool   `json:"declined,omitempty"`
	Error    string `json:"error,omitempty"`
}

var ensureManagedBinary = func(start string) (string, error) {
	return toolchain.EnsureNativeLIRProto(start)
}

// ResolveBinary returns the path the Run helper should exec. The
// `OSTY_NATIVE_LIRPROTO_BIN` env var wins outright; otherwise the
// managed binary under `.osty/toolchain/<version>/` is built lazily.
func ResolveBinary(start string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(Env)); override != "" {
		return override, nil
	}
	return ensureManagedBinary(start)
}

// Run JSON-encodes `req`, spawns the binary, and decodes the JSON
// response. `start` is a working-directory hint used by the managed-
// binary resolver — tests pass the package source path; the
// production caller uses the entry SourcePath.
func Run(start string, req Request) (Response, error) {
	path, err := ResolveBinary(start)
	if err != nil {
		return Response{}, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("marshal native lirproto request: %w", err)
	}
	cmd := exec.Command(path)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return Response{}, fmt.Errorf("exec %s: %w (%s)", path, err, msg)
	}
	var resp Response
	if err := json.Unmarshal(out, &resp); err != nil {
		return Response{}, fmt.Errorf("decode native lirproto response: %w", err)
	}
	return resp, nil
}
