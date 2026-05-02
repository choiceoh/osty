package main

import (
	"errors"
	"fmt"

	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/nativelirproto"
)

// init registers the process-bridge LIR Proto runner with the
// dispatcher. When `OSTY_LLVM_LIR_PROTO=1`, the runner spawns
// `osty-native-lirproto` and forwards the request as JSON. Slice-1
// of Phase-7: the binary's body is still a Go wrapper around the
// production MIR-direct path (so output is byte-identical to
// gate-off), but the wire shape + runner registration are now in
// place. A future slice replaces the binary's internals with a
// real call into `toolchain/lir_proto.osty` without touching this
// file or the dispatcher.
func init() {
	llvmgen.SetLIRProtoRunner(processLIRProtoRunner{})
}

// processLIRProtoRunner shells out to the managed osty-native-lirproto
// binary. Decline / error responses convert to structured errors so
// the dispatcher's existing fall-back-on-error policy kicks in
// (warning logged, MIR-direct emit continues unchanged).
type processLIRProtoRunner struct{}

var errLIRProtoDeclined = errors.New("osty-native-lirproto declined the request")

func (processLIRProtoRunner) Run(req llvmgen.LIRProtoRequest) ([]byte, error) {
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
