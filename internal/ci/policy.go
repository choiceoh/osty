package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
)

// nameRE matches the set of characters the manifest validator
// allows for a package name. Duplicated here because
// internal/manifest keeps its regex private; the policy check
// intentionally repeats the constraint so a manifest that somehow
// bypassed validation (hand-edited, wrong toolchain) still fails
// CI loudly.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// checkPolicy enforces per-project hygiene independent of type
// checking: does the manifest advertise enough metadata to be
// useful on a registry? Are workspace members wired correctly?
// Are committed source files within a sane size envelope?
//
// Policy is the "can this project be published or published
// alongside others without embarrassment" check. It is intentionally
// opinionated — CI either enforces these, or it's not CI.
func (r *Runner) checkPolicy() *Check {
	c := &Check{Name: CheckPolicy}
	if r.Manifest == nil {
		c.Note = "no osty.toml found; policy checks skipped"
		return c
	}

	c.Diags = append(c.Diags, policyManifestFields(r.Manifest)...)
	c.Diags = append(c.Diags, policyWorkspace(r.Manifest, r.Root)...)
	c.Diags = append(c.Diags, policyFileSizes(r)...)

	return c
}

// policyManifestFields checks the [package] section advertises the
// minimum a published package needs: name, version, edition,
// license. description + authors are surfaced as warnings — nice
// to have, not a hard fail.
func policyManifestFields(m *manifest.Manifest) []*diag.Diagnostic {
	// Virtual workspaces without [package] legitimately have no
	// fields to check; release validation handles those.
	if !m.HasPackage {
		return nil
	}
	var out []*diag.Diagnostic
	p := m.Package
	if p.Name == "" {
		out = append(out, synthetic(diag.Error, "CI101",
			"package.name is missing in osty.toml"))
	} else if !nameRE.MatchString(p.Name) {
		out = append(out, synthetic(diag.Error, "CI102",
			fmt.Sprintf("package.name %q: expected [a-z][a-z0-9_-]*", p.Name)))
	}
	if p.Version == "" {
		out = append(out, synthetic(diag.Error, "CI103",
			"package.version is missing in osty.toml"))
	}
	if p.Edition == "" {
		out = append(out, synthetic(diag.Warning, "CI104",
			"package.edition is not set — pin an edition (e.g. \"0.4\") to avoid drift"))
	}
	if p.License == "" {
		out = append(out, synthetic(diag.Warning, "CI105",
			"package.license is not set — set an SPDX identifier or leave the key empty if truly unlicensed"))
	}
	if p.Description == "" {
		out = append(out, synthetic(diag.Warning, "CI106",
			"package.description is empty — registry listings will be unhelpful"))
	}
	return out
}

// policyWorkspace verifies workspace members actually exist on
// disk and aren't outside the manifest's directory. A dangling
// member reference is always an error — the resolver silently
// skips them today, so CI's the place that catches them.
func policyWorkspace(m *manifest.Manifest, root string) []*diag.Diagnostic {
	if m.Workspace == nil {
		return nil
	}
	var out []*diag.Diagnostic
	if len(m.Workspace.Members) == 0 {
		out = append(out, synthetic(diag.Error, "CI110",
			"[workspace] declared but members is empty"))
		return out
	}
	for _, mem := range m.Workspace.Members {
		if mem == "" || strings.HasPrefix(mem, "..") || strings.HasPrefix(mem, "/") {
			out = append(out, synthetic(diag.Error, "CI111",
				fmt.Sprintf("workspace member %q escapes the project root", mem)))
			continue
		}
		path := filepath.Join(root, mem)
		info, err := os.Stat(path)
		if err != nil {
			out = append(out, synthetic(diag.Error, "CI112",
				fmt.Sprintf("workspace member %q does not exist at %s", mem, path)))
			continue
		}
		if !info.IsDir() {
			out = append(out, synthetic(diag.Error, "CI113",
				fmt.Sprintf("workspace member %q is not a directory", mem)))
		}
	}
	return out
}

// policyFileSizes enforces Options.MaxFileBytes over every .osty
// source file. A file over the cap is typically an accidentally
// committed generated artifact or blob; failing CI here catches
// it before it pollutes the registry tarball.
func policyFileSizes(r *Runner) []*diag.Diagnostic {
	if r.Opts.MaxFileBytes <= 0 {
		return nil
	}
	var out []*diag.Diagnostic
	for _, path := range r.ostyFiles() {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() > r.Opts.MaxFileBytes {
			out = append(out, synthetic(diag.Warning, "CI120",
				fmt.Sprintf("%s is %d bytes (limit %d)",
					prettyPath(r.Root, path), info.Size(), r.Opts.MaxFileBytes)))
		}
	}
	return out
}
