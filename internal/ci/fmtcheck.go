package ci

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/format"
)

// checkFormat verifies every .osty file in the project is already
// canonically formatted. It reuses the same format.Source entry
// point `osty fmt --check` uses, so the two commands can never
// disagree about what "formatted" means.
//
// A file whose parse fails emits a diag.Error; otherwise unformatted
// files emit a single Error-severity diagnostic naming the file.
// The goal is to fail fast on CI — producing a full diff here would
// just re-stream what `osty fmt --check FILE` already prints.
func (r *Runner) checkFormat() *Check {
	c := &Check{Name: CheckFormat}
	files := r.ostyFiles()
	if len(files) == 0 {
		c.Note = "no .osty files found"
		return c
	}

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			c.Diags = append(c.Diags, synthetic(diag.Error, "CI001",
				fmt.Sprintf("%s: %v", prettyPath(r.Root, path), err)))
			continue
		}
		out, fdiags, ferr := format.Source(src)
		if ferr != nil {
			// The underlying parser diagnostics already carry
			// file-local line info. Promote them through ci so
			// the caller sees them in the Report. A format
			// parse-error is always a hard fail — unformatted
			// code we can't even parse can't be verified.
			for _, d := range fdiags {
				c.Diags = append(c.Diags, d)
			}
			c.Diags = append(c.Diags, synthetic(diag.Error, "CI002",
				fmt.Sprintf("%s: format: %v", prettyPath(r.Root, path), ferr)))
			continue
		}
		if string(src) != string(out) {
			c.Diags = append(c.Diags, synthetic(diag.Error, "CI003",
				fmt.Sprintf("%s: not canonically formatted (run `osty fmt --write`)",
					prettyPath(r.Root, path))))
		}
	}
	return c
}

// prettyPath returns p relative to root when possible, falling
// back to the absolute path. Keeps diagnostic output short without
// hiding state the user needs to track down the file.
func prettyPath(root, p string) string {
	if root == "" {
		return p
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "" {
		return p
	}
	return rel
}
