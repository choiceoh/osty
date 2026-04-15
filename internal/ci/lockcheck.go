package ci

import (
	"fmt"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
)

// checkLockfile verifies osty.lock exists and its [[package]]
// entries describe every non-path dependency declared in the
// manifest. A stale lockfile ships bad version pins — exactly the
// class of bug CI should catch before release, so we flag
// divergence as Error.
//
// The check is deliberately shallow: it does NOT re-resolve the
// graph (that's the job of `osty build`). It only asserts the
// presence of entries matching the manifest's declared deps, so
// CI can run without network access.
func (r *Runner) checkLockfile() *Check {
	c := &Check{Name: CheckLockfile}
	if r.Manifest == nil {
		c.Note = "no osty.toml found; lockfile check skipped"
		return c
	}
	if len(r.Manifest.Dependencies) == 0 && len(r.Manifest.DevDependencies) == 0 {
		c.Note = "no dependencies declared"
		return c
	}

	lock, err := lockfile.Read(r.Root)
	if err != nil {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI201",
			fmt.Sprintf("failed to read osty.lock: %v", err)))
		return c
	}
	if lock == nil {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI202",
			"osty.lock is missing — run `osty build` to generate it"))
		return c
	}
	if lock.Version > lockfile.SchemaVersion {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI203",
			fmt.Sprintf("osty.lock schema version %d is newer than this toolchain (max %d)",
				lock.Version, lockfile.SchemaVersion)))
	}

	all := append([]manifest.Dependency{}, r.Manifest.Dependencies...)
	all = append(all, r.Manifest.DevDependencies...)
	for _, d := range all {
		name := d.PackageName
		if name == "" {
			name = d.Name
		}
		if !lockHas(lock, name) {
			c.Diags = append(c.Diags, synthetic(diag.Error, "CI204",
				fmt.Sprintf("dependency %q declared in osty.toml is not pinned in osty.lock (run `osty update`)",
					name)))
		}
	}
	return c
}

// lockHas reports whether name appears anywhere in lock's
// [[package]] entries. We deliberately don't check the version
// requirement here — that'd require re-running resolution.
func lockHas(lock *lockfile.Lock, name string) bool {
	for _, p := range lock.Packages {
		if p.Name == name {
			return true
		}
	}
	return false
}
