package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/nativellvmgen"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type llvmgenRequest = nativellvmgen.Request
type llvmgenPackageInput = nativellvmgen.PackageInput
type llvmgenPackageFile = nativellvmgen.PackageFile
type llvmgenResponse = nativellvmgen.Response

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	var req llvmgenRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode llvmgen request: %w", err)
	}
	var (
		entry backend.Entry
		err   error
	)
	if req.Package != nil {
		entry, err = preparePackageEntry(req)
	} else {
		entry, err = prepareSourceEntry(req)
	}
	if err != nil {
		return err
	}
	ir, ok, warnings, err := backend.TryEmitNativeOwnedLLVMIRText(entry, "")
	if err != nil {
		return fmt.Errorf("emit native llvm-ir: %w", err)
	}
	resp := llvmgenResponse{
		Covered:  ok,
		Warnings: renderWarnings(warnings),
	}
	if ok {
		resp.LLVMIR = string(ir)
	}
	return json.NewEncoder(stdout).Encode(resp)
}

func prepareSourceEntry(req llvmgenRequest) (backend.Entry, error) {
	// Wrap the single source as a one-file package and reuse the
	// workspace/arena pipeline — identical to preparePackageEntry
	// but without requiring req.Package to be set.
	synthetic := llvmgenRequest{
		Path: req.Path,
		Package: &llvmgenPackageInput{
			Files: []llvmgenPackageFile{{
				Path:   req.Path,
				Source: req.Source,
			}},
		},
	}
	return preparePackageEntry(synthetic)
}

func preparePackageEntry(req llvmgenRequest) (backend.Entry, error) {
	root, entryPath, err := writePackageRequest(req)
	if err != nil {
		return backend.Entry{}, err
	}
	defer os.RemoveAll(root)

	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		return backend.Entry{}, err
	}
	ws.Stdlib = stdlib.LoadCached()
	if _, err := ws.LoadPackageArenaFirst(""); err != nil {
		return backend.Entry{}, err
	}
	results := ws.ResolveAll()
	checks := check.Workspace(ws, results, check.Opts{

		Stdlib: ws.Stdlib,
	})
	pkg := ws.Packages[""]
	if pkg == nil {
		return backend.Entry{}, fmt.Errorf("%s: no package sources were loaded", root)
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
		if fp == entryPath {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		return backend.Entry{}, fmt.Errorf("%s is not part of the generated package rooted at %s", entryPath, root)
	}
	chk := checks[""]
	if chk == nil {
		chk = &check.Result{}
	}
	return backend.PreparePackage("main", entryPath, pkg, entryFile, chk)
}

func writePackageRequest(req llvmgenRequest) (string, string, error) {
	root, err := os.MkdirTemp("", "osty-native-llvmgen-*")
	if err != nil {
		return "", "", err
	}
	files := req.Package.Files
	if len(files) == 0 {
		return "", "", fmt.Errorf("prepare llvmgen package: no files provided")
	}
	entryName := packageEntryName(req)
	if entryName == "" {
		entryName = packageFileName(files[0], 0)
	}
	entryPath := ""
	for i, file := range files {
		name := packageFileName(file, i)
		dst := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			os.RemoveAll(root)
			return "", "", err
		}
		if err := os.WriteFile(dst, []byte(file.Source), 0o644); err != nil {
			os.RemoveAll(root)
			return "", "", err
		}
		if filepath.Base(name) == entryName {
			entryPath = dst
		}
	}
	if entryPath == "" {
		entryPath = filepath.Join(root, packageFileName(files[0], 0))
	}
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		os.RemoveAll(root)
		return "", "", err
	}
	return root, absEntry, nil
}

func packageEntryName(req llvmgenRequest) string {
	if req.Path == "" {
		return ""
	}
	return filepath.Base(req.Path)
}

func packageFileName(file llvmgenPackageFile, idx int) string {
	if file.Name != "" {
		return filepath.Base(file.Name)
	}
	if file.Path != "" {
		return filepath.Base(file.Path)
	}
	return fmt.Sprintf("file%d.osty", idx)
}

func renderWarnings(warnings []error) []string {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning == nil {
			continue
		}
		out = append(out, warning.Error())
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
