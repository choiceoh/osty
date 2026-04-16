package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/resolve"
)

// GoBackend wraps the existing Go transpiler behind the backend contract.
type GoBackend struct{}

func (GoBackend) Name() Name { return NameGo }

// Emit writes generated Go source to the backend-aware artifact path. For
// EmitBinary, callers still invoke `go build` themselves; this method prepares
// the source artifact and reports the conventional binary path.
func (GoBackend) Emit(ctx context.Context, req Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateEmit(NameGo, req.Emit); err != nil {
		return nil, err
	}
	if req.Entry.File == nil {
		return nil, fmt.Errorf("go backend: nil entry file")
	}
	pkgName := req.Entry.PackageName
	if pkgName == "" {
		pkgName = "main"
	}
	res := req.Entry.Resolve
	if res == nil {
		res = &resolve.Result{}
	}
	chk := req.Entry.Check
	if chk == nil {
		chk = &check.Result{}
	}

	artifacts := req.Artifacts(NameGo)
	if err := os.MkdirAll(artifacts.OutputDir, 0o755); err != nil {
		return nil, err
	}
	src, genErr := gen.GenerateMapped(pkgName, req.Entry.File, res, chk, req.Entry.SourcePath)
	src = prependGoBuildConstraints(src, req.Features)
	if err := os.WriteFile(artifacts.GoSource, src, 0o644); err != nil {
		return nil, err
	}

	out := &Result{
		Backend:   NameGo,
		Emit:      req.Emit,
		Artifacts: artifacts,
	}
	if genErr != nil {
		out.Warnings = append(out.Warnings, genErr)
	}
	return out, nil
}

func prependGoBuildConstraints(src []byte, features []string) []byte {
	if len(features) == 0 {
		return src
	}
	constraints := make([]string, 0, len(features))
	for _, f := range features {
		if f == "" {
			continue
		}
		constraints = append(constraints, "feat_"+f)
	}
	if len(constraints) == 0 {
		return src
	}
	sort.Strings(constraints)
	header := "//go:build " + join(constraints, " && ") + "\n\n"
	return append([]byte(header), src...)
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}

// SourcePath returns a clean source path for diagnostics. Kept as a helper so
// callers do not need to know the artifact struct's field names.
func (a Artifacts) SourcePath() string {
	if a.GoSource != "" {
		return filepath.Clean(a.GoSource)
	}
	if a.LLVMIR != "" {
		return filepath.Clean(a.LLVMIR)
	}
	return ""
}
