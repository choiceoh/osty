package nativellvmgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/toolchain"
)

const Env = "OSTY_NATIVE_LLVMGEN_BIN"

type Request struct {
	Path    string        `json:"path,omitempty"`
	Source  string        `json:"source,omitempty"`
	Package *PackageInput `json:"package,omitempty"`
}

type PackageInput struct {
	Files []PackageFile `json:"files,omitempty"`
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
	cmd := exec.Command(path)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return Response{}, fmt.Errorf("exec %s: %w (%s)", path, err, msg)
	}
	var resp Response
	if err := json.Unmarshal(out, &resp); err != nil {
		return Response{}, fmt.Errorf("decode native llvmgen response: %w", err)
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
			Files: files,
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
