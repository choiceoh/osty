package ci

import (
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// checkLint runs the lint pass over every loaded package, mirroring
// what `osty lint DIR --strict` does. The manifest's [lint]
// allow/deny/exclude configuration is applied so project-level
// policy is respected under CI.
//
// Under --strict the caller's Options.Strict is used by Run to
// turn warnings into failure; this function never self-promotes
// severity — that would double-count warnings when Run inspects
// the diag list.
func (r *Runner) checkLint() *Check {
	c := &Check{Name: CheckLint}
	if len(r.Packages) == 0 {
		c.Note = "no packages loaded"
		return c
	}

	cfg := lintConfigFromManifest(r)
	opts := checkOpts()
	for _, pkg := range r.Packages {
		// Run resolve + check fresh so we get a PackageResult
		// compatible with lint.Package. When the package was
		// already resolved as part of r.Workspace, its Refs /
		// TypeRefs maps are already populated; re-resolving is
		// cheap compared to re-loading from disk, and gives us
		// a known-clean Diag list untouched by any previous pass.
		pr := resolve.ResolvePackage(pkg, resolve.NewPrelude())
		chk := check.Package(pkg, pr, opts)
		lr := lint.Package(pkg, pr, chk)
		if cfg != nil {
			lr = cfg.Apply(lr)
		}
		// Any parse / resolve / check error is promoted — a
		// package that doesn't type-check can't be lint-clean in
		// any meaningful sense.
		c.Diags = append(c.Diags, pr.Diags...)
		c.Diags = append(c.Diags, chk.Diags...)
		c.Diags = append(c.Diags, lr.Diags...)
	}
	return c
}

// checkOpts mirrors the cmd/osty helper so primitive method
// lookup works during the CI lint pass.
func checkOpts() check.Opts {
	return check.Opts{Primitives: stdlib.LoadCached().Primitives}
}

// lintConfigFromManifest extracts the [lint] section from the
// runner's manifest into a lint.Config, or returns nil when no
// manifest / no [lint] section is present.
func lintConfigFromManifest(r *Runner) *lint.Config {
	if r.Manifest == nil || r.Manifest.Lint == nil {
		return nil
	}
	cfg := &lint.Config{
		Allow:   r.Manifest.Lint.Allow,
		Deny:    r.Manifest.Lint.Deny,
		Exclude: r.Manifest.Lint.Exclude,
	}
	return cfg
}

