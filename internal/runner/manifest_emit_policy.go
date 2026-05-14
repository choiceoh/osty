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

// ManifestPackageEmitSpec mirrors the [package] table emit
// payload. HasPackage is the host's `Manifest.HasPackage` flag —
// together with Name/Version it gates emission so a virtual
// workspace manifest omits the entire section.
//
// Osty: toolchain/manifest_emit.osty:211
type ManifestPackageEmitSpec struct {
	HasPackage  bool
	Name        string
	Version     string
	Edition     string
	Description string
	Authors     []string
	License     string
	Repository  string
	Homepage    string
	Keywords    []string
}

// RenderManifestPackageSection emits `[package]\n` plus every
// non-empty field. Returns "" for a virtual workspace shape
// (HasPackage == false AND both Name + Version empty).
//
// Osty: toolchain/manifest_emit.osty:231
func RenderManifestPackageSection(pkg ManifestPackageEmitSpec) string {
	if !pkg.HasPackage && pkg.Name == "" && pkg.Version == "" {
		return ""
	}
	out := "[package]\n"
	out += RenderManifestStringField("name", pkg.Name)
	out += RenderManifestStringField("version", pkg.Version)
	if pkg.Edition != "" {
		out += RenderManifestStringField("edition", pkg.Edition)
	}
	if pkg.Description != "" {
		out += RenderManifestStringField("description", pkg.Description)
	}
	if len(pkg.Authors) > 0 {
		out += RenderManifestStringArrayField("authors", pkg.Authors)
	}
	if pkg.License != "" {
		out += RenderManifestStringField("license", pkg.License)
	}
	if pkg.Repository != "" {
		out += RenderManifestStringField("repository", pkg.Repository)
	}
	if pkg.Homepage != "" {
		out += RenderManifestStringField("homepage", pkg.Homepage)
	}
	if len(pkg.Keywords) > 0 {
		out += RenderManifestStringArrayField("keywords", pkg.Keywords)
	}
	return out
}

// ManifestRegistryEmitSpec mirrors one entry of the host
// `manifest.Registries` slice.
//
// Osty: toolchain/manifest_emit.osty:264
type ManifestRegistryEmitSpec struct {
	Name  string
	URL   string
	Token string
}

// RenderManifestRegistriesSection emits one
// `[registries.<name>]` block per entry. Order is preserved
// from the input slice (the historical Go code did not sort).
// Returns "" when the slice is empty.
//
// Osty: toolchain/manifest_emit.osty:273
func RenderManifestRegistriesSection(registries []ManifestRegistryEmitSpec) string {
	if len(registries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range registries {
		b.WriteString("\n[registries.")
		b.WriteString(r.Name)
		b.WriteString("]\n")
		if r.URL != "" {
			b.WriteString(RenderManifestStringField("url", r.URL))
		}
		if r.Token != "" {
			b.WriteString(RenderManifestStringField("token", r.Token))
		}
	}
	return b.String()
}

// ManifestLintEmitSpec mirrors the [lint] table emit payload.
// Empty Allow AND empty Deny together gate the section out.
//
// Osty: toolchain/manifest_emit.osty:292
type ManifestLintEmitSpec struct {
	Allow []string
	Deny  []string
}

// RenderManifestLintSection emits `\n[lint]\n` + optional allow +
// optional deny. Returns "" when both arrays are empty.
//
// Osty: toolchain/manifest_emit.osty:298
func RenderManifestLintSection(lint ManifestLintEmitSpec) string {
	if len(lint.Allow) == 0 && len(lint.Deny) == 0 {
		return ""
	}
	out := "\n[lint]\n"
	if len(lint.Allow) > 0 {
		out += RenderManifestStringArrayField("allow", lint.Allow)
	}
	if len(lint.Deny) > 0 {
		out += RenderManifestStringArrayField("deny", lint.Deny)
	}
	return out
}

// ManifestWebView2EmitSpec mirrors the nested
// `[gui.webview2]` table. HasDevTools is the host's flag
// distinguishing "field absent" from `devtools = false`.
//
// Osty: toolchain/manifest_emit.osty:315
type ManifestWebView2EmitSpec struct {
	HasDevTools bool
	DevTools    bool
	Runtime     string
}

// ManifestGUIEmitSpec mirrors the [gui] table. HasWebView2 gates
// the nested `[gui.webview2]` block.
//
// Osty: toolchain/manifest_emit.osty:324
type ManifestGUIEmitSpec struct {
	Backend     string
	Entry       string
	HasWebView2 bool
	WebView2    ManifestWebView2EmitSpec
}

// RenderManifestGUISection emits `\n[gui]\n` + optional fields,
// followed by an optional nested `\n[gui.webview2]\n` block.
// Always emitted by the renderer; the host's nil-check on m.GUI
// controls presence.
//
// Osty: toolchain/manifest_emit.osty:334
func RenderManifestGUISection(gui ManifestGUIEmitSpec) string {
	out := "\n[gui]\n"
	if gui.Backend != "" {
		out += RenderManifestStringField("backend", gui.Backend)
	}
	if gui.Entry != "" {
		out += RenderManifestStringField("entry", gui.Entry)
	}
	if gui.HasWebView2 {
		out += "\n[gui.webview2]\n"
		if gui.WebView2.HasDevTools {
			out += RenderManifestBoolField("devtools", gui.WebView2.DevTools)
		}
		if gui.WebView2.Runtime != "" {
			out += RenderManifestStringField("runtime", gui.WebView2.Runtime)
		}
	}
	return out
}
