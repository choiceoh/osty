package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/runner"
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

// defaultEmitMode delegates the text-vs-binary decision to
// runner (toolchain/runner.osty) and then parses the canonical
// string back into a backend.EmitMode. The Go backend was isolated
// as bootstrap-only in af4e03b so the production registry only
// exposes llvm-ir / binary / object; both of runner's outputs
// (llvm-ir, binary) parse cleanly in the default build.
func defaultEmitMode(tool string, name backend.Name) backend.EmitMode {
	raw := runner.DefaultEmitMode(tool)
	mode, err := backend.ParseEmitMode(raw)
	if err != nil {
		// runner only returns "llvm-ir" or "binary"; both are
		// registered modes, so a parse failure here is a harness bug.
		return backend.EmitBinary
	}
	return mode
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

// validateCLIEmit runs two layers of validation. The tool-level
// rules ("gen with backend go must emit go", "run requires
// binary") live in toolchain/runner.osty so every future subcommand
// that wraps the backend inherits the same UX. The backend's own
// ValidateEmit then enforces capability ("this backend can't
// produce this format at all").
func validateCLIEmit(tool string, name backend.Name, mode backend.EmitMode) error {
	if d := runner.ToolEmitCompat(tool, string(name), string(mode)); d != nil {
		return errors.New(d.Message)
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
