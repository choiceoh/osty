// manifest_emit_policy.go is the Go snapshot of
// toolchain/manifest_emit.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import (
	"sort"
	"strings"
)

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

// RenderManifestStringField emits a `key = "value"` line (newline
// included). Wraps TomlBasicString so callers don't duplicate the
// quoting policy at every section.
//
// Osty: toolchain/manifest_emit.osty:108
func RenderManifestStringField(key, val string) string {
	return key + " = " + TomlBasicString(val) + "\n"
}

// RenderManifestStringArrayField emits a `key = ["a", "b", ...]`
// line. Empty arrays render as `key = []`; the host suppresses the
// call when it doesn't want the field at all.
//
// Osty: toolchain/manifest_emit.osty:114
func RenderManifestStringArrayField(key string, vals []string) string {
	quoted := make([]string, 0, len(vals))
	for _, v := range vals {
		quoted = append(quoted, TomlBasicString(v))
	}
	return key + " = [" + strings.Join(quoted, ", ") + "]\n"
}

// RenderManifestBoolField emits `key = true` / `key = false`.
//
// Osty: toolchain/manifest_emit.osty:122
func RenderManifestBoolField(key string, val bool) string {
	if val {
		return key + " = true\n"
	}
	return key + " = false\n"
}

// RenderManifestDepsSection emits a `[section]` header followed
// by one dep line per entry, sorted by Name. Returns the empty
// string when deps is empty so the host can call it unconditionally
// without producing an orphan header.
//
// Osty: toolchain/manifest_emit.osty:132
func RenderManifestDepsSection(section string, deps []ManifestDepEmitSpec) string {
	if len(deps) == 0 {
		return ""
	}
	sorted := make([]ManifestDepEmitSpec, len(deps))
	copy(sorted, deps)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	b.WriteString("\n[")
	b.WriteString(section)
	b.WriteString("]\n")
	for _, d := range sorted {
		b.WriteString(RenderManifestDepLine(d))
	}
	return b.String()
}

// ManifestBinEmitSpec mirrors the host manifest.BinTarget. Empty
// Name+Path signals "no [bin] section"; the renderer elides the
// block in that case.
//
// Osty: toolchain/manifest_emit.osty:147
type ManifestBinEmitSpec struct {
	Name string
	Path string
}

// RenderManifestBinSection emits `\n[bin]\n` plus optional name
// and path lines. Returns "" when both fields are empty so the
// host can call unconditionally.
//
// Osty: toolchain/manifest_emit.osty:156
func RenderManifestBinSection(bin ManifestBinEmitSpec) string {
	if bin.Name == "" && bin.Path == "" {
		return ""
	}
	out := "\n[bin]\n"
	if bin.Name != "" {
		out += RenderManifestStringField("name", bin.Name)
	}
	if bin.Path != "" {
		out += RenderManifestStringField("path", bin.Path)
	}
	return out
}

// ManifestLibEmitSpec mirrors the host manifest.LibTarget.
//
// Osty: toolchain/manifest_emit.osty:171
type ManifestLibEmitSpec struct {
	Path string
}

// RenderManifestLibSection emits `\n[lib]\npath = "..."\n` or the
// empty string when Path is empty.
//
// Osty: toolchain/manifest_emit.osty:176
func RenderManifestLibSection(lib ManifestLibEmitSpec) string {
	if lib.Path == "" {
		return ""
	}
	return "\n[lib]\n" + RenderManifestStringField("path", lib.Path)
}

// ManifestWorkspaceEmitSpec mirrors the host manifest.Workspace.
//
// Osty: toolchain/manifest_emit.osty:185
type ManifestWorkspaceEmitSpec struct {
	Members []string
}

// RenderManifestWorkspaceSection emits the [workspace] block;
// members is always rendered even when empty.
//
// Osty: toolchain/manifest_emit.osty:192
func RenderManifestWorkspaceSection(ws ManifestWorkspaceEmitSpec) string {
	return "\n[workspace]\n" + RenderManifestStringArrayField("members", ws.Members)
}

// ManifestCapabilitiesEmitSpec mirrors the host
// manifest.Capabilities.
//
// Osty: toolchain/manifest_emit.osty:199
type ManifestCapabilitiesEmitSpec struct {
	Runtime bool
}

// RenderManifestCapabilitiesSection emits the [capabilities]
// block. Always emitted by the renderer; the host's nil-check
// controls presence.
//
// Osty: toolchain/manifest_emit.osty:204
func RenderManifestCapabilitiesSection(caps ManifestCapabilitiesEmitSpec) string {
	return "\n[capabilities]\n" + RenderManifestBoolField("runtime", caps.Runtime)
}
