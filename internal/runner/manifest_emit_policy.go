// manifest_emit_policy.go is the Go snapshot of
// toolchain/manifest_emit.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// ManifestGitSpec mirrors toolchain/manifest_emit.osty's
// ManifestGitSpec — the per-dep git source for the emit path.
//
// Osty: toolchain/manifest_emit.osty:21
type ManifestGitSpec struct {
	URL    string
	Tag    string
	Branch string
	Rev    string
}

// ManifestDepEmitSpec is the host shape the dependency-line
// emitter consumes. Empty-string fields signal "not set"; the
// nested git source uses `URL == ""` as its absent sentinel so
// host call sites don't have to thread a pointer.
//
// Osty: toolchain/manifest_emit.osty:31
type ManifestDepEmitSpec struct {
	Name         string
	VersionReq   string
	Path         string
	Git          ManifestGitSpec
	PackageName  string
	Registry     string
	Optional     bool
	Features     []string
	DefaultFeats bool
}

// RenderManifestDepLine emits one osty.toml dependency entry,
// trailing newline included. Short form (`name = "1.0"`) is
// preferred for version-only deps with default flags; otherwise
// the inline-table form lists each non-default selector.
//
// Osty: toolchain/manifest_emit.osty:46
func RenderManifestDepLine(d ManifestDepEmitSpec) string {
	if manifestDepCanShortForm(d) {
		return d.Name + " = " + TomlBasicString(d.VersionReq) + "\n"
	}
	var parts []string
	if d.VersionReq != "" {
		parts = append(parts, "version = "+TomlBasicString(d.VersionReq))
	}
	if d.Path != "" {
		parts = append(parts, "path = "+TomlBasicString(d.Path))
	}
	if d.Git.URL != "" {
		parts = append(parts, "git = "+TomlBasicString(d.Git.URL))
		if d.Git.Tag != "" {
			parts = append(parts, "tag = "+TomlBasicString(d.Git.Tag))
		}
		if d.Git.Branch != "" {
			parts = append(parts, "branch = "+TomlBasicString(d.Git.Branch))
		}
		if d.Git.Rev != "" {
			parts = append(parts, "rev = "+TomlBasicString(d.Git.Rev))
		}
	}
	if d.PackageName != "" {
		parts = append(parts, "package = "+TomlBasicString(d.PackageName))
	}
	if d.Registry != "" {
		parts = append(parts, "registry = "+TomlBasicString(d.Registry))
	}
	if d.Optional {
		parts = append(parts, "optional = true")
	}
	if len(d.Features) > 0 {
		quoted := make([]string, 0, len(d.Features))
		for _, f := range d.Features {
			quoted = append(quoted, TomlBasicString(f))
		}
		parts = append(parts, "features = ["+strings.Join(quoted, ", ")+"]")
	}
	if !d.DefaultFeats {
		parts = append(parts, "default-features = false")
	}
	return d.Name + " = { " + strings.Join(parts, ", ") + " }\n"
}

func manifestDepCanShortForm(d ManifestDepEmitSpec) bool {
	return d.VersionReq != "" &&
		d.Path == "" &&
		d.Git.URL == "" &&
		d.PackageName == "" &&
		d.Registry == "" &&
		!d.Optional &&
		len(d.Features) == 0 &&
		d.DefaultFeats
}
