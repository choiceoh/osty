package main

import (
	"errors"
	"fmt"

	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/nativelirproto"
)

// init registers the process-bridge LIR Proto runner with the
// dispatcher. When `OSTY_LLVM_LIR_PROTO=1`, the runner spawns
// `osty-native-lirproto` and forwards the request as JSON. As of
// Phase-7 Slice 2, the bridge binary forks the self-hosted
// `osty-self lir-proto-lower` subcommand (built by `osty build
// toolchain/`) which routes MIR through the Osty-owned
// `toolchain/lir_proto.osty` pipeline. The dispatcher's
// fall-back-on-error policy converts a missing osty-self artifact
	// or a declined response into a structured warning + MIR-direct
	// emit so gate-on never hard-fails on a stale worktree.
func init() {
	llvmabi.SetLIRProtoRunner(processLIRProtoRunner{})
}

// processLIRProtoRunner shells out to the managed osty-native-lirproto
// binary. Decline / error responses convert to structured errors so
// the dispatcher's existing fall-back-on-error policy kicks in
// (warning logged, MIR-direct emit continues unchanged).
type processLIRProtoRunner struct{}

var errLIRProtoDeclined = errors.New("osty-native-lirproto declined the request")

func (processLIRProtoRunner) Run(req llvmabi.LIRProtoRequest) ([]byte, error) {
	bridgeReq := nativelirproto.Request{
		PackageName: req.PackageName,
		SourcePath:  req.SourcePath,
		Source:      string(req.Source),
		Target:      req.Target,
	}
	start := req.SourcePath
	if start == "" {
		start = "."
	}
	resp, err := nativelirproto.Run(start, bridgeReq)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		if resp.Declined {
			return nil, fmt.Errorf("%w: %s", errLIRProtoDeclined, resp.Error)
		}
		return nil, errors.New(resp.Error)
	}
	if resp.Declined {
		return nil, errLIRProtoDeclined
	}
	if resp.LLVMIR == "" {
		return nil, fmt.Errorf("%w: empty IR", errLIRProtoDeclined)
	}
	return []byte(resp.LLVMIR), nil
}
