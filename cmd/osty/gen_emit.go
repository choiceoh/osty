package main

import (
	"context"
	"fmt"
	"os"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/nativellvmgen"
)

var tryExternalGenLLVMIR = func(entry *genPackageEntry) ([]byte, bool, []error, error) {
	if entry == nil || entry.pkg == nil {
		return nil, false, nil, nil
	}
	return nativellvmgen.TryPackage(".", entry.sourcePath, entry.pkg)
}

func prepareGenBackendEntry(pkgName string, entry *genPackageEntry) (backend.Entry, error) {
	if entry == nil {
		return backend.Entry{}, fmt.Errorf("missing gen entry")
	}
	if entry.pkg == nil {
		return backend.Entry{}, fmt.Errorf("missing package input for gen")
	}
	if countLowerableFiles(entry.pkg) > 0 {
		return backend.PreparePackage(pkgName, entry.sourcePath, entry.pkg, entry.file, entry.chk)
	}
	file, src, err := parseGenEmitFile(entry.pkg)
	if err != nil {
		return backend.Entry{}, err
	}
	backendEntry, err := backend.PrepareEntry(pkgName, entry.sourcePath, file, entry.fileResult(), entry.chk)
	if err != nil {
		return backend.Entry{}, err
	}
	backendEntry.Source = src
	return backendEntry, nil
}

func emitGenArtifact(name backend.Name, mode backend.EmitMode, pkgName string, entry *genPackageEntry) ([]byte, *backend.Result, error) {
	backendEntry, err := prepareGenBackendEntry(pkgName, entry)
	if err != nil {
		return nil, nil, err
	}
	if name == backend.NameLLVM && mode == backend.EmitLLVMIR {
		out, warnings, emitErr := emitGenLLVMIR(backendEntry, entry)
		return out, &backend.Result{
			Backend:  name,
			Emit:     mode,
			Warnings: warnings,
		}, emitErr
	}
	return emitGenArtifactViaBackend(name, mode, backendEntry)
}

func emitGenLLVMIR(entry backend.Entry, pkgEntry *genPackageEntry) ([]byte, []error, error) {
	if out, ok, warnings, err := tryExternalGenLLVMIR(pkgEntry); err == nil && ok {
		return out, warnings, nil
	}
	if out, ok, warnings, err := backend.TryEmitNativeOwnedLLVMIRText(entry, ""); err == nil && ok {
		return out, warnings, nil
	}
	return backend.EmitLLVMIRText(entry, "", nil)
}

func emitGenArtifactViaBackend(name backend.Name, mode backend.EmitMode, entry backend.Entry) ([]byte, *backend.Result, error) {
	b := backendFromCLI("gen", name)
	tmpRoot, err := os.MkdirTemp("", "osty-gen-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmpRoot)

	result, emitErr := b.Emit(context.Background(), backend.Request{
		Layout: backend.Layout{
			Root:    tmpRoot,
			Profile: "gen",
		},
		Emit:  mode,
		Entry: entry,
	})
	if result == nil {
		return nil, nil, emitErr
	}
	artifact := result.Artifacts.SourcePath()
	if artifact == "" {
		if emitErr != nil {
			return nil, result, emitErr
		}
		return nil, result, fmt.Errorf("backend %q did not produce a source artifact", name)
	}
	data, readErr := os.ReadFile(artifact)
	if readErr != nil {
		if emitErr != nil {
			return nil, result, emitErr
		}
		return nil, result, readErr
	}
	return data, result, emitErr
}
