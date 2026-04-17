package main

import (
	"errors"
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
	return backend.NameLLVM.String()
}

func defaultEmitMode(tool string, name backend.Name) backend.EmitMode {
	if tool == "gen" || tool == "pipeline" {
		return backend.EmitLLVMIR
	}
	return backend.EmitBinary
}

func parseCLIBackend(raw string) (backend.Name, error) {
	return backend.ParseName(raw)
}

func parseCLIEmitMode(raw string) (backend.EmitMode, error) {
	mode, err := backend.ParseEmitMode(raw)
	if err != nil {
		return "", err
	}
	return mode, nil
}

func validateCLIEmit(tool string, name backend.Name, mode backend.EmitMode) error {
	switch tool {
	case "gen", "pipeline":
		if name == backend.NameLLVM && mode != backend.EmitLLVMIR {
			return fmt.Errorf("%s with backend %q cannot emit %q (want %q)", tool, name, mode, backend.EmitLLVMIR)
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
	return name, mode
}

func backendFromCLI(tool string, name backend.Name) backend.Backend {
	b, err := backend.New(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", tool, err)
		os.Exit(2)
	}
	return b
}

func exitBackendEmitError(tool string, result *backend.Result, err error) {
	if errors.Is(err, backend.ErrLLVMNotImplemented) {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", tool, err)
		if result != nil {
			if artifact := result.Artifacts.SourcePath(); artifact != "" {
				fmt.Fprintf(os.Stderr, "  artifact: %s\n", artifact)
			}
			if result.Artifacts.RuntimeDir != "" {
				fmt.Fprintf(os.Stderr, "  runtime: %s\n", result.Artifacts.RuntimeDir)
			}
		}
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "osty %s: %v\n", tool, err)
	os.Exit(1)
}
