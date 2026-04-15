package main

import (
	"fmt"
	"os"

	"github.com/osty/osty/internal/backend"
)

func firstBackendWarning(r *backend.Result) error {
	if r == nil || len(r.Warnings) == 0 {
		return nil
	}
	return r.Warnings[0]
}

func defaultBackendName() string {
	return backend.NameGo.String()
}

func defaultEmitMode(tool string, name backend.Name) backend.EmitMode {
	if tool == "gen" {
		if name == backend.NameLLVM {
			return backend.EmitLLVMIR
		}
		return backend.EmitGoSource
	}
	return backend.EmitBinary
}

func parseCLIBackend(raw string) (backend.Name, error) {
	name, err := backend.ParseName(raw)
	if err != nil {
		return "", err
	}
	return name, nil
}

func parseCLIEmitMode(raw string) (backend.EmitMode, error) {
	mode, err := backend.ParseEmitMode(raw)
	if err != nil {
		return "", err
	}
	return mode, nil
}

func requireImplementedBackend(name backend.Name) error {
	if name != backend.NameGo {
		return fmt.Errorf("backend %q is not implemented yet", name)
	}
	return nil
}

func validateCLIEmit(tool string, name backend.Name, mode backend.EmitMode) error {
	switch tool {
	case "gen":
		switch name {
		case backend.NameGo:
			if mode != backend.EmitGoSource {
				return fmt.Errorf("gen with backend %q cannot emit %q (want %q)", name, mode, backend.EmitGoSource)
			}
		case backend.NameLLVM:
			if mode != backend.EmitLLVMIR {
				return fmt.Errorf("gen with backend %q cannot emit %q (want %q)", name, mode, backend.EmitLLVMIR)
			}
		}
	case "run", "test":
		if mode != backend.EmitBinary {
			return fmt.Errorf("%s requires --emit=%q", tool, backend.EmitBinary)
		}
	}
	return backend.ValidateEmit(name, mode)
}

func resolveBackendAndEmitFlags(tool, backendRaw, emitRaw string) (backend.Name, backend.EmitMode) {
	name, err := parseCLIBackend(backendRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", tool, err)
		os.Exit(2)
	}
	if emitRaw == "" {
		emitRaw = defaultEmitMode(tool, name).String()
	}
	mode, err := parseCLIEmitMode(emitRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", tool, err)
		os.Exit(2)
	}
	if err := validateCLIEmit(tool, name, mode); err != nil {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", tool, err)
		os.Exit(2)
	}
	if err := requireImplementedBackend(name); err != nil {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", tool, err)
		os.Exit(2)
	}
	return name, mode
}

func resolveBackendFlag(tool, raw string) backend.Name {
	name, _ := resolveBackendAndEmitFlags(tool, raw, "")
	return name
}
