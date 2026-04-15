package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// Read loads osty.toml from path and parses it. Convenience wrapper
// around ReadFile + Parse so callers don't need to import `os` for
// the common case.
func Read(path string) (*Manifest, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Load reads, parses, and validates the manifest at path in one call.
// It returns the parsed Manifest (non-nil iff parse succeeded), every
// diagnostic produced by parse + validate, and a filesystem error iff
// the file itself could not be read. Callers should render the
// diagnostics even when Manifest is non-nil — Validate may append
// warnings the user wants to see.
//
// The source bytes are attached to the returned Manifest so callers
// can feed them into diag.Formatter for snippet rendering without a
// second read.
func Load(path string) (*Manifest, []*diag.Diagnostic, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			d := diag.New(diag.Error,
				fmt.Sprintf("no manifest found at %s", path)).
				Code(diag.CodeManifestNotFound).
				PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
				Hint("run `osty new NAME` or `osty init` to create one").
				Build()
			return nil, []*diag.Diagnostic{d}, nil
		}
		d := diag.New(diag.Error,
			fmt.Sprintf("cannot read %s: %v", path, err)).
			Code(diag.CodeManifestReadError).
			PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
			Build()
		return nil, []*diag.Diagnostic{d}, err
	}
	m, diags := ParseDiagnostics(src, path)
	if m != nil {
		// Attach source + path to the Manifest so callers can render
		// diagnostics without passing src around separately.
		m.SetSource(src, path)
		diags = append(diags, Validate(m)...)
	}
	return m, diags, nil
}

// LoadDir locates the manifest for the project rooted at dir and
// runs Load on it. If dir has no osty.toml directly, the search
// walks upward per FindRoot.
func LoadDir(dir string) (*Manifest, []*diag.Diagnostic, error) {
	root, err := FindRoot(dir)
	if err != nil {
		d := diag.New(diag.Error, err.Error()).
			Code(diag.CodeManifestNotFound).
			PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
			Hint("run `osty new NAME` or `osty init` to create one").
			Build()
		return nil, []*diag.Diagnostic{d}, nil
	}
	return Load(filepath.Join(root, ManifestFile))
}

// Write marshals m and writes it to path with 0644 permissions.
// Overwrites any existing file at that location.
func Write(path string, m *Manifest) error {
	return os.WriteFile(path, Marshal(m), 0o644)
}

// FindRoot walks up from start looking for osty.toml. Returns the
// directory containing the manifest (not the manifest path itself)
// so callers can chdir / build paths relative to it.
//
// Stops at the first filesystem root boundary. Returns an error with
// the starting directory in the message when no manifest is found —
// the commands that use this surface the error to the user so they
// know to run from inside a project tree.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, ManifestFile)
		if _, err := os.Stat(candidate); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find %s in %s or any parent directory", ManifestFile, start)
		}
		dir = parent
	}
}
