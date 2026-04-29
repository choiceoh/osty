package main

import (
	"context"
	"fmt"
	"os"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/nativellvmgen"
	ostyquery "github.com/osty/osty/internal/query/osty"
	"github.com/osty/osty/internal/resolve"
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
	if entry.eng != nil {
		lower := entry.lower
		lower.PackageName = pkgName
		lower.SourcePath = entry.sourcePath
		lower.EntryPath = entry.sourcePath
		lowered := entry.eng.Queries.LowerMIRPackage.Get(entry.eng.DB, lower)
		return lowered.Entry, lowered.Err
	}
	if countLowerableFiles(entry.pkg) > 0 {
		graph := entry.graph
		if graph == nil {
			graph = resolve.NewPackageGraphForPackage(entry.pkgPath, entry.pkg)
		}
		return backend.PrepareGraphPackage(pkgName, entry.sourcePath, graph, entry.pkgPath, entry.file, entry.chk)
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
	if name == backend.NameLLVM && mode == backend.EmitLLVMIR {
		if out, ok, warnings, err := tryExternalGenLLVMIR(entry); err == nil && ok {
			return out, &backend.Result{
				Backend:  name,
				Emit:     mode,
				Warnings: warnings,
			}, nil
		}
	}
	if entry != nil && entry.eng != nil {
		return emitGenArtifactViaQuery(name, mode, pkgName, entry)
	}
	backendEntry, err := prepareGenBackendEntry(pkgName, entry)
	if err != nil {
		return nil, nil, err
	}
	return emitGenArtifactViaBackend(name, mode, backendEntry)
}

func emitGenArtifactViaQuery(name backend.Name, mode backend.EmitMode, pkgName string, entry *genPackageEntry) ([]byte, *backend.Result, error) {
	if entry == nil || entry.eng == nil {
		return nil, nil, fmt.Errorf("missing query graph for gen")
	}
	tmpRoot, err := os.MkdirTemp("", "osty-gen-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmpRoot)

	lower := entry.lower
	lower.PackageName = pkgName
	lower.SourcePath = entry.sourcePath
	lower.EntryPath = entry.sourcePath
	target := ostyquery.NewEmitTarget(lower, name, mode, backend.Layout{
		Root:    tmpRoot,
		Profile: "gen",
	}, "", nil)
	emitted := entry.eng.Queries.Emit.Get(entry.eng.DB, target)
	if emitted.Result == nil {
		return nil, nil, emitted.Err
	}
	artifact := emitted.Result.Artifacts.SourcePath()
	if artifact == "" {
		if emitted.Err != nil {
			return nil, emitted.Result, emitted.Err
		}
		return nil, emitted.Result, fmt.Errorf("backend %q did not produce a source artifact", name)
	}
	data, readErr := os.ReadFile(artifact)
	if readErr != nil {
		if emitted.Err != nil {
			return nil, emitted.Result, emitted.Err
		}
		return nil, emitted.Result, readErr
	}
	return data, emitted.Result, emitted.Err
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
