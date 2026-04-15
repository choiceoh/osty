package ci

import (
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// checkLint runs the lint pass over every loaded package, mirroring
// what `osty lint DIR --strict` does.
//
// The function reuses the PackageResult Runner.Load already produced
// — re-resolving here would discard the workspace's stdlib / dep
// hookup and emit false-positive "unknown package" diagnostics for
// every `use std.*` site. The manifest's [lint] allow/deny/exclude
// configuration is applied so project-level policy is respected.
//
// Strict promotion of warnings to failure happens in Run, never
// here — that keeps the Diags list a faithful record of what each
// rule reported, regardless of severity overrides.
func (r *Runner) checkLint() *Check {
	c := &Check{Name: CheckLint}
	if len(r.Packages) == 0 {
		c.Note = "no packages loaded"
		return c
	}

	cfg := lintConfigFromManifest(r)
	opts := checkOpts()
	for i, pkg := range r.Packages {
		var pr *resolve.PackageResult
		if i < len(r.Results) {
			pr = r.Results[i]
		}
		if pr == nil {
			// Defensive fallback. Single-file `osty ci .` paths
			// should never land here because Load fills Results,
			// but a future entry point that bypasses Load would.
			pr = resolve.ResolvePackage(pkg, resolve.NewPrelude())
		}
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
	reg := stdlib.LoadCached()
	return check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods}
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
