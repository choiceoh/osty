package nativellvmgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/mirjson"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/subproc"
	"github.com/osty/osty/internal/toolchain"
)

const Env = "OSTY_NATIVE_LLVMGEN_BIN"

type Request struct {
	Path    string        `json:"path,omitempty"`
	Source  string        `json:"source,omitempty"`
	Package *PackageInput `json:"package,omitempty"`
	MIR     *MIRInput     `json:"mir,omitempty"`
}

type MIRInput struct {
	PackageName string          `json:"packageName,omitempty"`
	SourcePath  string          `json:"sourcePath,omitempty"`
	Source      string          `json:"source,omitempty"`
	Target      string          `json:"target,omitempty"`
	Module      *mirjson.Module `json:"module,omitempty"`
}

type PackageInput struct {
	Files             []PackageFile `json:"files,omitempty"`
	RuntimeCapability bool          `json:"runtimeCapability,omitempty"`
	// LibraryMode tells the subprocess to skip emitting a top-level
	// `main` function in the resulting LLVM IR. Used by cmd/osty's
	// cross-package dep compilation (PR-G2 / cross-pkg link) so the
	// resulting `.o` can be linked alongside a consumer's `main`
	// without `_main` symbol collision. The non-main bodies of the
	// dep are still emitted so `declare`s in the consumer's IR get
	// resolved.
	LibraryMode bool `json:"libraryMode,omitempty"`
	// PackageName is the dep's declared package name (from its
	// `osty.toml::[package] name`). When set together with
	// LibraryMode, the subprocess rewrites every exported function's
	// LLVM symbol from `<fn>` to `<PackageName>.<fn>` so cross-pkg
	// callers — which mangle via
	// `internal/mir/lower.go::qualifiedSymbol` as
	// `<use.RawPath>.<fn>` — find a matching `define` at link time.
	// Empty PackageName falls back to the historical bare-name
	// emission (no rename) for backwards compatibility with callers
	// that haven't filled the field yet.
	PackageName string `json:"packageName,omitempty"`
}

type PackageFile struct {
	Path   string `json:"path,omitempty"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`
}

type Response struct {
	Covered  bool     `json:"covered"`
	LLVMIR   string   `json:"llvmIr,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

var ensureManagedBinary = func(start string) (string, error) {
	return toolchain.EnsureNativeLLVMGen(start)
}

func ResolveBinary(start string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(Env)); override != "" {
		return override, nil
	}
	return ensureManagedBinary(start)
}

func Run(start string, req Request) (Response, error) {
	path, err := ResolveBinary(start)
	if err != nil {
		return Response{}, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("marshal native llvmgen request: %w", err)
	}
	stdout, stderr, runErr := subproc.Run(path, payload)
	if runErr != nil {
		return Response{}, runErr
	}
	var resp Response
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return Response{}, subproc.WrapResponseError(path, fmt.Errorf("decode native llvmgen response: %w", err), stdout, stderr)
	}
	return resp, nil
}

func TrySource(start, path string, src []byte) ([]byte, bool, []error, error) {
	resp, err := Run(start, Request{
		Path:   path,
		Source: string(src),
	})
	if err != nil {
		return nil, false, nil, err
	}
	return []byte(resp.LLVMIR), resp.Covered, warningErrors(resp.Warnings), nil
}

func TryPackage(start, entryPath string, pkg *resolve.Package) ([]byte, bool, []error, error) {
	req, err := RequestFromPackage(entryPath, pkg)
	if err != nil {
		return nil, false, nil, err
	}
	resp, err := Run(start, req)
	if err != nil {
		return nil, false, nil, err
	}
	return []byte(resp.LLVMIR), resp.Covered, warningErrors(resp.Warnings), nil
}

// TryPackageLibrary compiles a package the same way TryPackage does
// but with `PackageInput.LibraryMode = true`, telling the subprocess
// to skip emitting `main` so the resulting IR can be linked into a
// consumer binary without `_main` symbol collision. Used by the
// cross-package dep `.o` compile path (PR-G2 cross-pkg link).
func TryPackageLibrary(start, entryPath string, pkg *resolve.Package) ([]byte, bool, []error, error) {
	req, err := RequestFromPackage(entryPath, pkg)
	if err != nil {
		return nil, false, nil, err
	}
	if req.Package != nil {
		req.Package.LibraryMode = true
	}
	resp, err := Run(start, req)
	if err != nil {
		return nil, false, nil, err
	}
	return []byte(resp.LLVMIR), resp.Covered, warningErrors(resp.Warnings), nil
}

func TryMIR(start, packageName, sourcePath string, source []byte, module *mir.Module, target string) ([]byte, bool, []error, error) {
	req, err := RequestFromMIR(packageName, sourcePath, source, module, target)
	if err != nil {
		return nil, false, nil, err
	}
	resp, err := Run(start, req)
	if err != nil {
		return nil, false, nil, err
	}
	return []byte(resp.LLVMIR), resp.Covered, warningErrors(resp.Warnings), nil
}

func RequestFromMIR(packageName, sourcePath string, source []byte, module *mir.Module, target string) (Request, error) {
	payload, err := mirjson.FromModule(module)
	if err != nil {
		return Request{}, fmt.Errorf("encode MIR for native llvmgen: %w", err)
	}
	return Request{
		Path: sourcePath,
		MIR: &MIRInput{
			PackageName: packageName,
			SourcePath:  sourcePath,
			Source:      string(source),
			Target:      target,
			Module:      payload,
		},
	}, nil
}

func RequestFromPackage(entryPath string, pkg *resolve.Package) (Request, error) {
	if pkg == nil {
		return Request{}, fmt.Errorf("missing package input for native llvmgen")
	}
	files := make([]PackageFile, 0, len(pkg.Files))
	for i, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		name := strings.TrimSpace(filepath.Base(pf.Path))
		if name == "." || name == string(filepath.Separator) {
			name = ""
		}
		if name == "" {
			name = fmt.Sprintf("file%d.osty", i)
		}
		files = append(files, PackageFile{
			Path:   pf.Path,
			Name:   name,
			Source: string(pf.Source),
		})
	}
	if len(files) == 0 {
		return Request{}, fmt.Errorf("native llvmgen package has no source files")
	}
	return Request{
		Path: entryPath,
		Package: &PackageInput{
			Files:             files,
			RuntimeCapability: pkg.RuntimeCapability,
			PackageName:       pkg.Name,
		},
	}, nil
}

func warningErrors(warnings []string) []error {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]error, 0, len(warnings))
	for _, warning := range warnings {
		if strings.TrimSpace(warning) == "" {
			continue
		}
		out = append(out, errors.New(warning))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
