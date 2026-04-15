package ci

import (
	"fmt"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr/semver"
)

// checkRelease validates the project is ready to publish. It is
// strictly additive on top of policy + lockfile: those checks
// cover day-to-day hygiene; release imposes the harder constraints
// registry uploads need in order to be reproducible.
//
// Rules enforced:
//
//   - [package] is present and has a strict SemVer 2 version.
//   - license is non-empty (publishing an unlicensed package is
//     legal but almost always a mistake).
//   - no dependency points at a local path (path deps can't be
//     published).
//   - osty.lock exists (so downstream builds reproduce).
//
// Git deps are allowed but warned: registry publishers who depend
// on an unpinned branch usually want to bump that to a tag first.
func (r *Runner) checkRelease() *Check {
	c := &Check{Name: CheckRelease}
	if r.Manifest == nil {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI301",
			"no osty.toml found; nothing to release"))
		return c
	}
	m := r.Manifest
	if !m.HasPackage {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI302",
			"only package projects can be released (virtual workspaces have nothing to publish)"))
		return c
	}
	p := m.Package
	if p.Version == "" {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI303",
			"package.version is missing"))
	} else if _, err := semver.ParseVersion(p.Version); err != nil {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI304",
			fmt.Sprintf("package.version %q is not strict SemVer: %v", p.Version, err)))
	}
	if p.License == "" {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI305",
			"package.license is required for release"))
	}

	all := append([]manifest.Dependency{}, m.Dependencies...)
	all = append(all, m.DevDependencies...)
	for _, d := range all {
		if d.Path != "" {
			c.Diags = append(c.Diags, synthetic(diag.Error, "CI306",
				fmt.Sprintf("dependency %q uses path=%q; path deps cannot be published",
					d.Name, d.Path)))
		}
		if d.Git != nil && d.Git.Tag == "" && d.Git.Rev == "" {
			c.Diags = append(c.Diags, synthetic(diag.Warning, "CI307",
				fmt.Sprintf("dependency %q is tracked by branch; pin to a tag or rev before release",
					d.Name)))
		}
	}

	// Lockfile presence for reproducibility when there are deps.
	if len(m.Dependencies) > 0 || len(m.DevDependencies) > 0 {
		lock, err := lockfile.Read(r.Root)
		if err != nil {
			c.Diags = append(c.Diags, synthetic(diag.Error, "CI308",
				fmt.Sprintf("failed to read osty.lock: %v", err)))
		} else if lock == nil {
			c.Diags = append(c.Diags, synthetic(diag.Error, "CI309",
				"osty.lock is required for release — run `osty build`"))
		}
	}
	return c
}
