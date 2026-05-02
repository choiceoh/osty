// Command osty-native-lirproto is the subprocess half of the
// Phase-7 LIR Proto runner bridge. It reads a `LIRProtoRequest`-
// shaped JSON payload on stdin, runs the request through the
// production lower → emit chain, and writes a `LIRProtoResponse`
// JSON object on stdout.
//
// Slice-1 contract: the binary's body is a thin Go wrapper around
// the existing MIR-direct emitter, so gate-on output matches
// gate-off byte-for-byte. The wire shape is what matters — a
// future slice replaces this body with a real call into the
// Osty-owned `toolchain/lir_proto.osty` lowerer without touching
// any caller of the bridge package.
//
// The binary intentionally scrubs `OSTY_LLVM_LIR_PROTO` from its
// own environment before invoking `backend.EmitLLVMIRText` so the
// gate check inside `generateLLVMIR` doesn't re-enter the runner
// (which would recurse into another subprocess).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/nativelirproto"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	// Disable the Phase-7 gate inside this subprocess so
	// `backend.EmitLLVMIRText` bypasses the LIR Proto runner check
	// and goes straight through the production MIR-direct path. If
	// we left the env var set, the dispatcher would re-spawn this
	// binary — infinite recursion.
	_ = os.Unsetenv(llvmgen.LIRProtoEnvVar)

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
	entry, cleanup, err := prepareEntry(req)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nativelirproto.Response{}, err
	}
	ir, _, emitErr := backend.EmitLLVMIRText(entry, req.Target, nil)
	if emitErr != nil {
		return nativelirproto.Response{}, emitErr
	}
	if len(ir) == 0 {
		return nativelirproto.Response{Declined: true, Error: "empty IR"}, nil
	}
	return nativelirproto.Response{LLVMIR: string(ir)}, nil
}

// prepareEntry mirrors osty-native-llvmgen's source-staging path:
// write the source bytes to a temp dir, stand up the workspace +
// resolver + checker, and produce the `backend.Entry` the emit
// pipeline consumes.
func prepareEntry(req nativelirproto.Request) (backend.Entry, func(), error) {
	root, err := os.MkdirTemp("", "osty-native-lirproto-*")
	if err != nil {
		return backend.Entry{}, nil, err
	}
	cleanup := func() { os.RemoveAll(root) }

	entryName := "main.osty"
	if req.SourcePath != "" {
		entryName = filepath.Base(req.SourcePath)
	}
	entryPath := filepath.Join(root, entryName)
	if err := os.WriteFile(entryPath, []byte(req.Source), 0o644); err != nil {
		return backend.Entry{}, cleanup, err
	}
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		return backend.Entry{}, cleanup, err
	}

	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		return backend.Entry{}, cleanup, err
	}
	ws.Stdlib = stdlib.LoadCached()
	if _, err := ws.LoadPackageNative(""); err != nil {
		return backend.Entry{}, cleanup, err
	}
	graph := resolve.NewPackageGraph(ws)
	results := resolve.ResolveGraph(graph)
	checks := check.PackageGraph(graph, results, check.Opts{Stdlib: ws.Stdlib})

	pkg := ws.Packages[""]
	if pkg == nil {
		return backend.Entry{}, cleanup, fmt.Errorf("%s: no package sources were loaded", root)
	}
	var entryFile *resolve.PackageFile
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		fp, err := filepath.Abs(pf.Path)
		if err != nil {
			continue
		}
		if fp == absEntry {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		return backend.Entry{}, cleanup, fmt.Errorf("%s is not part of the generated package rooted at %s", absEntry, root)
	}
	chk := checks[""]
	if chk == nil {
		chk = &check.Result{}
	}
	pkgName := req.PackageName
	if pkgName == "" {
		pkgName = "main"
	}
	entry, err := backend.PrepareGraphPackage(pkgName, absEntry, graph, "", entryFile, chk)
	if err != nil {
		return backend.Entry{}, cleanup, err
	}
	return entry, cleanup, nil
}
