// Package manifest parses and writes osty.toml, the per-project
// manifest (spec §13.2).
//
// The manifest declares the project's identity (`[package]`), its
// dependencies (`[dependencies]` + `[dev-dependencies]`), and
// optional overrides for the binary entry point (`[[bin]]`) or
// library root (`[lib]`). Registry sources, git sources, and local
// path sources are all representable; the lockfile (internal/lockfile)
// records the concrete version the resolver picked.
//
// The TOML parsing is provided by toml.go (a small hand-written
// subset, not an external dependency). Any shape the manifest writer
// emits roundtrips cleanly through the parser.
//
// Error handling: Parse returns a single error whose message is
// line-numbered via the underlying TOML parser. Higher-level schema
// violations (missing `[package].name`, bad version-req syntax) also
// carry the offending line.
package manifest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/token"
)

// ManifestFile is the canonical filename written at project roots.
const ManifestFile = "osty.toml"

// Manifest is the in-memory representation of osty.toml.
//
// Every Manifest carries at least one of Package or Workspace — a
// standalone project sets Package; a virtual workspace root sets
// Workspace only; a package-and-workspace root sets both. The
// `[package]` + `[workspace]` coexistence case lets a single directory
// be both a package and the workspace root (matches Cargo's shape).
type Manifest struct {
	Package         Package
	HasPackage      bool // true when [package] was present in the source
	Dependencies    []Dependency
	DevDependencies []Dependency
	Bin             *BinTarget // nil = use default main.osty
	Lib             *LibTarget // nil = use default lib.osty
	Registries      []Registry // [registries.<name>] entries
	Workspace       *Workspace // non-nil when [workspace] is present
	Lint            *Lint      // nil when [lint] absent; defaults apply

	// Profiles are the `[profile.<name>]` tables — custom build
	// profile overrides or new profiles inheriting from the built-in
	// set. Keyed by profile name; a nil map means none declared.
	Profiles map[string]*Profile

	// Targets are the `[target.<triple>]` tables — cross-compilation
	// presets the user has pinned in the manifest. Keyed by triple
	// (e.g. "amd64-linux"); a nil slice means no declared targets.
	Targets []*Target

	// Features maps feature-name → list of dep/feature references
	// that the feature transitively enables. Populated from the
	// `[features]` table.
	Features map[string][]string

	// DefaultFeatures is the value of `[features].default` — the
	// feature set enabled out of the box. `--no-default-features`
	// on the CLI drops this to empty.
	DefaultFeatures []string

	// source and path are used for diagnostic rendering; populated by
	// Load (or by hand via SetSource for bytes-in-memory callers).
	source []byte
	path   string
}

// Source returns the raw TOML bytes the manifest was parsed from, or
// nil if the manifest was constructed in memory. Retained so callers
// can feed it into diag.Formatter for snippet rendering.
func (m *Manifest) Source() []byte { return m.source }

// Path returns the filesystem path the manifest was loaded from, or
// "" if it was parsed from bytes without going through disk.
func (m *Manifest) Path() string { return m.path }

// SetSource attaches src + path to m for later diagnostic rendering.
// Load calls this automatically; callers that build a Manifest from
// bytes may call it too.
func (m *Manifest) SetSource(src []byte, path string) {
	m.source = append([]byte(nil), src...)
	m.path = path
}

// Workspace is the `[workspace]` table. Members is the list of member
// package directories relative to the manifest's directory. The list
// may be empty when Members is implicit via future glob support, but
// today every member must be listed explicitly; an empty list triggers
// CodeManifestWorkspaceEmpty during validation.
type Workspace struct {
	Members []string
}

// Lint is the `[lint]` section, used by `osty lint` to apply
// project-wide suppress / elevate rules. Either list may be empty; both
// missing means the default lint behavior (all warnings reported).
type Lint struct {
	// Allow is the set of lint codes / aliases to unconditionally
	// suppress for this project. See internal/lint.Config for accepted
	// names (concrete codes like "L0001", rule aliases like
	// "naming_value", categories like "unused", or wildcards "lint" /
	// "all").
	Allow []string
	// Deny is the set of lint codes / aliases to elevate to error
	// severity (so CI fails without needing `--strict`).
	Deny []string
	// Exclude is a list of path globs. Files matching any entry are
	// skipped entirely by `osty lint`. `**` is a cross-segment wildcard.
	Exclude []string
}

// Profile mirrors one `[profile.<name>]` table. The Has* flags
// distinguish "set to the zero value" from "unset" so the downstream
// profile package can merge on top of the built-in defaults without
// clobbering unset fields.
type Profile struct {
	Name     string
	Inherits string

	OptLevel int
	Debug    bool
	Strip    bool
	Overflow bool
	Inlining bool
	LTO      bool

	HasOptLevel bool
	HasDebug    bool
	HasStrip    bool
	HasOverflow bool
	HasInlining bool
	HasLTO      bool

	GoFlags []string
	Env     map[string]string

	Pos token.Pos
}

// Target mirrors one `[target.<triple>]` table. A triple follows the
// `<arch>-<os>` convention (e.g. `amd64-linux`, `arm64-darwin`).
// HasCGO distinguishes an absent CGO key from an explicit `cgo =
// false`.
type Target struct {
	Triple string
	CGO    bool
	HasCGO bool
	Env    map[string]string
	Pos    token.Pos
}

// Package is the manifest's `[package]` section.
type Package struct {
	Name        string
	Version     string // SemVer-ish — validated by the self-hosted manifest core
	Edition     string // osty spec version, e.g. "0.3"
	Description string
	Authors     []string
	License     string
	Repository  string
	Homepage    string
	// Keywords are short tags displayed on the registry listing.
	Keywords []string

	// Positions of individual fields in the source, used by Validate
	// to anchor semantic diagnostics at the right line. All line
	// numbers are 1-based; Column is left at 1 because TOML values do
	// not carry column information in the current parser. TablePos
	// points at the `[package]` header itself.
	NamePos    token.Pos
	VersionPos token.Pos
	EditionPos token.Pos
	TablePos   token.Pos
}

// Dependency is one entry under `[dependencies]` or
// `[dev-dependencies]`. Exactly one of VersionReq / Path / Git is set;
// that's enforced at parse time.
//
// A dependency's Name is the local alias the consumer writes in
// `use <name>`; when the remote package name differs, PackageName
// records the canonical name (defaults to Name when unset).
type Dependency struct {
	Name         string
	PackageName  string // defaults to Name when unset
	VersionReq   string // registry source: "1.2", "^1.0", "0.3.*"
	Path         string // local path source (relative to the manifest)
	Git          *GitSource
	Registry     string // non-default registry name ("" = default)
	Optional     bool
	Features     []string // opt-in feature names enabled on the dep
	DefaultFeats bool     // false disables the dep's default features

	// Pos is the source position of the dependency's key, used by
	// Validate to anchor version-requirement diagnostics at the right
	// line. Left at zero for in-memory Dependency values constructed
	// without going through Parse.
	Pos token.Pos
}

// GitSource describes a dependency fetched from a git repository.
//
// Exactly one of Tag, Branch, Rev may be set. When all three are
// empty the resolver uses the default branch HEAD and records the
// resolved commit in osty.lock. Tag is the recommended form because
// it has semver semantics; Branch is the least reproducible form.
type GitSource struct {
	URL    string
	Tag    string
	Branch string
	Rev    string
}

// BinTarget is an optional `[[bin]]` override for projects that don't
// use the default main.osty convention.
type BinTarget struct {
	Name string // executable name; defaults to the package name
	Path string // source file, relative to the manifest
}

// LibTarget is an optional `[lib]` override. Most projects do not need
// to set this — the default lib.osty convention is picked up
// automatically.
type LibTarget struct {
	Path string // source file, relative to the manifest
}

// Registry is one entry in `[registries.<name>]`. When unset, `osty`
// uses the compiled-in default registry URL.
type Registry struct {
	Name  string
	URL   string
	Token string // API token for publishing; read from env in practice
}

// Parse parses osty.toml bytes into a Manifest. Returns a descriptive
// error on malformed TOML or structural schema violations whose shape
// prevents building a Manifest. Semantic requirements such as a root
// [package]/[workspace] table or required package identity fields are
// reported by Validate so they flow through the self-hosted manifest
// validation core.
func Parse(src []byte) (*Manifest, error) {
	root, err := parseTOML(src)
	if err != nil {
		return nil, err
	}
	m := &Manifest{}
	// [package] — optional only when [workspace] is the sole top-level
	// section (virtual workspace root). The two-pass check happens
	// below after [workspace] is parsed so either ordering of tables
	// in the source works.
	pkgV, hasPkg := root.get("package")
	if hasPkg {
		if pkgV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [package] must be a table", pkgV.Line)
		}
		m.Package.TablePos = token.Pos{Line: pkgV.Line, Column: 1}
		if err := parsePackageSection(pkgV.Tbl, &m.Package); err != nil {
			return nil, err
		}
		m.HasPackage = true
	}
	// [dependencies]
	if depsV, ok := root.get("dependencies"); ok {
		if depsV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [dependencies] must be a table", depsV.Line)
		}
		deps, err := parseDepsSection("dependencies", depsV.Tbl)
		if err != nil {
			return nil, err
		}
		m.Dependencies = deps
	}
	// [dev-dependencies]
	if depsV, ok := root.get("dev-dependencies"); ok {
		if depsV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [dev-dependencies] must be a table", depsV.Line)
		}
		deps, err := parseDepsSection("dev-dependencies", depsV.Tbl)
		if err != nil {
			return nil, err
		}
		m.DevDependencies = deps
	}
	// [[bin]] / [bin] (we accept either shape for forward-compat)
	if binV, ok := root.get("bin"); ok {
		bt, err := parseBinSection(binV)
		if err != nil {
			return nil, err
		}
		m.Bin = bt
	}
	// [lib]
	if libV, ok := root.get("lib"); ok {
		if libV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [lib] must be a table", libV.Line)
		}
		lt := &LibTarget{}
		if p, ok := libV.Tbl.get("path"); ok {
			if p.Str == nil {
				return nil, fmt.Errorf("osty.toml:%d: lib.path must be a string", p.Line)
			}
			lt.Path = *p.Str
		}
		m.Lib = lt
	}
	// [registries.<name>]
	if regsV, ok := root.get("registries"); ok {
		if regsV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [registries] must be a table", regsV.Line)
		}
		for _, name := range regsV.Tbl.keys {
			entry, _ := regsV.Tbl.get(name)
			if entry.Tbl == nil {
				return nil, fmt.Errorf("osty.toml:%d: registries.%s must be a table", entry.Line, name)
			}
			r := Registry{Name: name}
			if u, ok := entry.Tbl.get("url"); ok {
				if u.Str == nil {
					return nil, fmt.Errorf("osty.toml:%d: registries.%s.url must be a string", u.Line, name)
				}
				r.URL = *u.Str
			}
			if t, ok := entry.Tbl.get("token"); ok {
				if t.Str == nil {
					return nil, fmt.Errorf("osty.toml:%d: registries.%s.token must be a string", t.Line, name)
				}
				r.Token = *t.Str
			}
			m.Registries = append(m.Registries, r)
		}
	}
	// [workspace]
	if wsV, ok := root.get("workspace"); ok {
		if wsV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [workspace] must be a table", wsV.Line)
		}
		ws := &Workspace{}
		for _, k := range wsV.Tbl.keys {
			kv, _ := wsV.Tbl.get(k)
			switch k {
			case "members":
				members, err := stringArray(kv, "workspace.members")
				if err != nil {
					return nil, err
				}
				ws.Members = members
			default:
				return nil, fmt.Errorf("osty.toml:%d: unknown key `%s` in [workspace]", kv.Line, k)
			}
		}
		m.Workspace = ws
	}
	// [lint]
	if lintV, ok := root.get("lint"); ok {
		if lintV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [lint] must be a table", lintV.Line)
		}
		lc := &Lint{}
		for _, k := range lintV.Tbl.keys {
			kv, _ := lintV.Tbl.get(k)
			switch k {
			case "allow":
				items, err := stringArray(kv, "lint.allow")
				if err != nil {
					return nil, err
				}
				lc.Allow = items
			case "deny":
				items, err := stringArray(kv, "lint.deny")
				if err != nil {
					return nil, err
				}
				lc.Deny = items
			case "exclude":
				items, err := stringArray(kv, "lint.exclude")
				if err != nil {
					return nil, err
				}
				lc.Exclude = items
			default:
				return nil, fmt.Errorf("osty.toml:%d: unknown key `%s` in [lint]", kv.Line, k)
			}
		}
		m.Lint = lc
	}
	// [profile.<name>]
	if profV, ok := root.get("profile"); ok {
		if profV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [profile] must be a table", profV.Line)
		}
		m.Profiles = map[string]*Profile{}
		for _, name := range profV.Tbl.keys {
			entry, _ := profV.Tbl.get(name)
			if entry.Tbl == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s must be a table", entry.Line, name)
			}
			p, err := parseProfileSection(name, entry.Tbl)
			if err != nil {
				return nil, err
			}
			m.Profiles[name] = p
		}
	}
	// [target.<triple>]
	if tgtV, ok := root.get("target"); ok {
		if tgtV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [target] must be a table", tgtV.Line)
		}
		for _, triple := range tgtV.Tbl.keys {
			entry, _ := tgtV.Tbl.get(triple)
			if entry.Tbl == nil {
				return nil, fmt.Errorf("osty.toml:%d: target.%s must be a table", entry.Line, triple)
			}
			t, err := parseTargetSection(triple, entry.Tbl)
			if err != nil {
				return nil, err
			}
			m.Targets = append(m.Targets, t)
		}
	}
	// [features]
	if featV, ok := root.get("features"); ok {
		if featV.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [features] must be a table", featV.Line)
		}
		m.Features = map[string][]string{}
		for _, name := range featV.Tbl.keys {
			entry, _ := featV.Tbl.get(name)
			items, err := stringArray(entry, fmt.Sprintf("features.%s", name))
			if err != nil {
				return nil, err
			}
			if name == "default" {
				m.DefaultFeatures = items
				continue
			}
			m.Features[name] = items
		}
	}
	return m, nil
}

// parseProfileSection extracts a [profile.<name>] table. Each known
// key sets both the value and the corresponding Has* flag so
// downstream merging can tell "absent" from "set-to-zero".
func parseProfileSection(name string, t *tomlTable) (*Profile, error) {
	p := &Profile{Name: name, Pos: token.Pos{Line: t.Line, Column: 1}}
	for _, k := range t.keys {
		v, _ := t.get(k)
		switch k {
		case "inherits":
			if v.Str == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.inherits must be a string", v.Line, name)
			}
			p.Inherits = *v.Str
		case "opt-level":
			if v.Int == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.opt-level must be an integer", v.Line, name)
			}
			p.OptLevel = int(*v.Int)
			p.HasOptLevel = true
		case "debug":
			if v.Bool == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.debug must be a bool", v.Line, name)
			}
			p.Debug = *v.Bool
			p.HasDebug = true
		case "strip":
			if v.Bool == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.strip must be a bool", v.Line, name)
			}
			p.Strip = *v.Bool
			p.HasStrip = true
		case "overflow-checks":
			if v.Bool == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.overflow-checks must be a bool", v.Line, name)
			}
			p.Overflow = *v.Bool
			p.HasOverflow = true
		case "inlining":
			if v.Bool == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.inlining must be a bool", v.Line, name)
			}
			p.Inlining = *v.Bool
			p.HasInlining = true
		case "lto":
			if v.Bool == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.lto must be a bool", v.Line, name)
			}
			p.LTO = *v.Bool
			p.HasLTO = true
		case "go-flags":
			flags, err := stringArray(v, fmt.Sprintf("profile.%s.go-flags", name))
			if err != nil {
				return nil, err
			}
			p.GoFlags = flags
		case "env":
			if v.Tbl == nil {
				return nil, fmt.Errorf("osty.toml:%d: profile.%s.env must be a table of strings", v.Line, name)
			}
			p.Env = map[string]string{}
			for _, ek := range v.Tbl.keys {
				ev, _ := v.Tbl.get(ek)
				if ev.Str == nil {
					return nil, fmt.Errorf("osty.toml:%d: profile.%s.env.%s must be a string", ev.Line, name, ek)
				}
				p.Env[ek] = *ev.Str
			}
		default:
			return nil, fmt.Errorf("osty.toml:%d: unknown key `%s` in profile.%s", v.Line, k, name)
		}
	}
	return p, nil
}

// parseTargetSection extracts a [target.<triple>] table. Supported
// keys today are `cgo` (bool) and `env` (table of strings); unknown
// keys are rejected so typos surface immediately.
func parseTargetSection(triple string, t *tomlTable) (*Target, error) {
	tgt := &Target{Triple: triple, Pos: token.Pos{Line: t.Line, Column: 1}}
	for _, k := range t.keys {
		v, _ := t.get(k)
		switch k {
		case "cgo":
			if v.Bool == nil {
				return nil, fmt.Errorf("osty.toml:%d: target.%s.cgo must be a bool", v.Line, triple)
			}
			tgt.CGO = *v.Bool
			tgt.HasCGO = true
		case "env":
			if v.Tbl == nil {
				return nil, fmt.Errorf("osty.toml:%d: target.%s.env must be a table of strings", v.Line, triple)
			}
			tgt.Env = map[string]string{}
			for _, ek := range v.Tbl.keys {
				ev, _ := v.Tbl.get(ek)
				if ev.Str == nil {
					return nil, fmt.Errorf("osty.toml:%d: target.%s.env.%s must be a string", ev.Line, triple, ek)
				}
				tgt.Env[ek] = *ev.Str
			}
		default:
			return nil, fmt.Errorf("osty.toml:%d: unknown key `%s` in target.%s", v.Line, k, triple)
		}
	}
	return tgt, nil
}

// parsePackageSection fills out *Package from a TOML table. Positions
// of the per-field values are recorded in out.*Pos so Validate's
// diagnostics can point back at the source. Missing semantic fields
// are left empty and reported by the self-hosted validation core.
func parsePackageSection(t *tomlTable, out *Package) error {
	for _, key := range t.keys {
		v, _ := t.get(key)
		switch key {
		case "name":
			if v.Str == nil {
				return fmt.Errorf("osty.toml:%d: package.name must be a string", v.Line)
			}
			out.Name = *v.Str
			out.NamePos = token.Pos{Line: v.Line, Column: 1}
		case "version":
			if v.Str == nil {
				return fmt.Errorf("osty.toml:%d: package.version must be a string", v.Line)
			}
			out.Version = *v.Str
			out.VersionPos = token.Pos{Line: v.Line, Column: 1}
		case "edition":
			if v.Str == nil {
				return fmt.Errorf("osty.toml:%d: package.edition must be a string", v.Line)
			}
			out.Edition = *v.Str
			out.EditionPos = token.Pos{Line: v.Line, Column: 1}
		case "description":
			if v.Str == nil {
				return fmt.Errorf("osty.toml:%d: package.description must be a string", v.Line)
			}
			out.Description = *v.Str
		case "license":
			if v.Str == nil {
				return fmt.Errorf("osty.toml:%d: package.license must be a string", v.Line)
			}
			out.License = *v.Str
		case "repository":
			if v.Str == nil {
				return fmt.Errorf("osty.toml:%d: package.repository must be a string", v.Line)
			}
			out.Repository = *v.Str
		case "homepage":
			if v.Str == nil {
				return fmt.Errorf("osty.toml:%d: package.homepage must be a string", v.Line)
			}
			out.Homepage = *v.Str
		case "authors":
			authors, err := stringArray(v, "package.authors")
			if err != nil {
				return err
			}
			out.Authors = authors
		case "keywords":
			keywords, err := stringArray(v, "package.keywords")
			if err != nil {
				return err
			}
			out.Keywords = keywords
		default:
			// Unknown keys are ignored with a tolerant policy — newer
			// osty versions may add fields and an older tool should
			// still be able to read the manifest.
		}
	}
	return nil
}

// parseDepsSection extracts one or more dependency entries from the
// given table. Each entry is either a bare string (shorthand for
// `version = ...`) or an inline table with the long form.
func parseDepsSection(sectionName string, t *tomlTable) ([]Dependency, error) {
	var out []Dependency
	for _, name := range t.keys {
		v, _ := t.get(name)
		dep, err := parseOneDep(sectionName, name, v)
		if err != nil {
			return nil, err
		}
		out = append(out, dep)
	}
	return out, nil
}

// parseOneDep handles the two shorthand forms of a dependency:
//
//	mylib = "1.0"
//	mylib = { path = "../mylib" }
func parseOneDep(section, name string, v *value) (Dependency, error) {
	d := Dependency{
		Name:         name,
		DefaultFeats: true,
		Pos:          token.Pos{Line: v.Line, Column: 1},
	}
	// Short form: version string.
	if v.Str != nil {
		d.VersionReq = *v.Str
		return d, nil
	}
	if v.Tbl == nil {
		return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s must be a string or inline table",
			v.Line, section, name)
	}
	t := v.Tbl
	for _, k := range t.keys {
		sv, _ := t.get(k)
		switch k {
		case "version":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.version must be a string",
					sv.Line, section, name)
			}
			d.VersionReq = *sv.Str
		case "path":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.path must be a string",
					sv.Line, section, name)
			}
			d.Path = *sv.Str
		case "git":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.git must be a string",
					sv.Line, section, name)
			}
			if d.Git == nil {
				d.Git = &GitSource{}
			}
			d.Git.URL = *sv.Str
		case "tag":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.tag must be a string",
					sv.Line, section, name)
			}
			if d.Git == nil {
				d.Git = &GitSource{}
			}
			d.Git.Tag = *sv.Str
		case "branch":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.branch must be a string",
					sv.Line, section, name)
			}
			if d.Git == nil {
				d.Git = &GitSource{}
			}
			d.Git.Branch = *sv.Str
		case "rev":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.rev must be a string",
					sv.Line, section, name)
			}
			if d.Git == nil {
				d.Git = &GitSource{}
			}
			d.Git.Rev = *sv.Str
		case "package":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.package must be a string",
					sv.Line, section, name)
			}
			d.PackageName = *sv.Str
		case "registry":
			if sv.Str == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.registry must be a string",
					sv.Line, section, name)
			}
			d.Registry = *sv.Str
		case "optional":
			if sv.Bool == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.optional must be a bool",
					sv.Line, section, name)
			}
			d.Optional = *sv.Bool
		case "features":
			feats, err := stringArray(sv, fmt.Sprintf("%s.%s.features", section, name))
			if err != nil {
				return Dependency{}, err
			}
			d.Features = feats
		case "default-features":
			if sv.Bool == nil {
				return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s.default-features must be a bool",
					sv.Line, section, name)
			}
			d.DefaultFeats = *sv.Bool
		default:
			return Dependency{}, fmt.Errorf("osty.toml:%d: unknown key `%s` in %s.%s",
				sv.Line, k, section, name)
		}
	}
	// Exactly one primary source must be set.
	count := 0
	if d.VersionReq != "" {
		count++
	}
	if d.Path != "" {
		count++
	}
	if d.Git != nil {
		count++
	}
	if count == 0 {
		return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s must specify one of `version`, `path`, or `git`",
			v.Line, section, name)
	}
	if count > 1 {
		return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s has multiple sources — use exactly one of `version`, `path`, `git`",
			v.Line, section, name)
	}
	if d.Git != nil {
		setCount := 0
		if d.Git.Tag != "" {
			setCount++
		}
		if d.Git.Branch != "" {
			setCount++
		}
		if d.Git.Rev != "" {
			setCount++
		}
		if setCount > 1 {
			return Dependency{}, fmt.Errorf("osty.toml:%d: %s.%s has multiple git refs — use at most one of `tag`, `branch`, `rev`",
				v.Line, section, name)
		}
	}
	return d, nil
}

// parseBinSection handles `[bin]` (single inline table) and `[[bin]]`
// (array-of-tables) at the top level. Only the first entry of
// `[[bin]]` is kept — multi-binary projects are future work.
func parseBinSection(v *value) (*BinTarget, error) {
	if v.Tbl != nil {
		return binFromTable(v.Tbl)
	}
	if v.Arr != nil && len(v.Arr.Values) > 0 {
		first := v.Arr.Values[0]
		if first.Tbl == nil {
			return nil, fmt.Errorf("osty.toml:%d: [[bin]] entries must be tables", first.Line)
		}
		return binFromTable(first.Tbl)
	}
	return nil, fmt.Errorf("osty.toml:%d: `bin` must be a table or array of tables", v.Line)
}

func binFromTable(t *tomlTable) (*BinTarget, error) {
	bt := &BinTarget{}
	for _, k := range t.keys {
		sv, _ := t.get(k)
		switch k {
		case "name":
			if sv.Str == nil {
				return nil, fmt.Errorf("osty.toml:%d: bin.name must be a string", sv.Line)
			}
			bt.Name = *sv.Str
		case "path":
			if sv.Str == nil {
				return nil, fmt.Errorf("osty.toml:%d: bin.path must be a string", sv.Line)
			}
			bt.Path = *sv.Str
		default:
			return nil, fmt.Errorf("osty.toml:%d: unknown key `%s` in bin", sv.Line, k)
		}
	}
	return bt, nil
}

// stringArray asserts that v is an array of strings and returns it.
func stringArray(v *value, context string) ([]string, error) {
	if v.Arr == nil {
		return nil, fmt.Errorf("osty.toml:%d: %s must be an array of strings", v.Line, context)
	}
	out := make([]string, 0, len(v.Arr.Values))
	for _, e := range v.Arr.Values {
		if e.Str == nil {
			return nil, fmt.Errorf("osty.toml:%d: %s must be an array of strings", e.Line, context)
		}
		out = append(out, *e.Str)
	}
	return out, nil
}

// Marshal serializes the manifest to TOML bytes suitable for writing
// to osty.toml. The output is stable: the package section comes
// first, then dependencies sorted by name, then dev-dependencies,
// then the workspace table (if any).
func Marshal(m *Manifest) []byte {
	var b strings.Builder
	// A virtual workspace may omit [package]. In every other case the
	// package section comes first because it's the most important
	// human-readable summary.
	pkgEmit := m.HasPackage || m.Package.Name != "" || m.Package.Version != ""
	if pkgEmit {
		b.WriteString("[package]\n")
		writeString(&b, "name", m.Package.Name)
		writeString(&b, "version", m.Package.Version)
		if m.Package.Edition != "" {
			writeString(&b, "edition", m.Package.Edition)
		}
		if m.Package.Description != "" {
			writeString(&b, "description", m.Package.Description)
		}
		if len(m.Package.Authors) > 0 {
			writeStringArray(&b, "authors", m.Package.Authors)
		}
		if m.Package.License != "" {
			writeString(&b, "license", m.Package.License)
		}
		if m.Package.Repository != "" {
			writeString(&b, "repository", m.Package.Repository)
		}
		if m.Package.Homepage != "" {
			writeString(&b, "homepage", m.Package.Homepage)
		}
		if len(m.Package.Keywords) > 0 {
			writeStringArray(&b, "keywords", m.Package.Keywords)
		}
	}
	if m.Bin != nil && (m.Bin.Name != "" || m.Bin.Path != "") {
		b.WriteString("\n[bin]\n")
		if m.Bin.Name != "" {
			writeString(&b, "name", m.Bin.Name)
		}
		if m.Bin.Path != "" {
			writeString(&b, "path", m.Bin.Path)
		}
	}
	if m.Lib != nil && m.Lib.Path != "" {
		b.WriteString("\n[lib]\n")
		writeString(&b, "path", m.Lib.Path)
	}
	writeDeps(&b, "dependencies", m.Dependencies)
	writeDeps(&b, "dev-dependencies", m.DevDependencies)
	for _, r := range m.Registries {
		b.WriteString(fmt.Sprintf("\n[registries.%s]\n", r.Name))
		if r.URL != "" {
			writeString(&b, "url", r.URL)
		}
		if r.Token != "" {
			writeString(&b, "token", r.Token)
		}
	}
	if m.Workspace != nil {
		b.WriteString("\n[workspace]\n")
		writeStringArray(&b, "members", m.Workspace.Members)
	}
	if m.Lint != nil && (len(m.Lint.Allow) > 0 || len(m.Lint.Deny) > 0) {
		b.WriteString("\n[lint]\n")
		if len(m.Lint.Allow) > 0 {
			writeStringArray(&b, "allow", m.Lint.Allow)
		}
		if len(m.Lint.Deny) > 0 {
			writeStringArray(&b, "deny", m.Lint.Deny)
		}
	}
	return []byte(b.String())
}

// writeDeps renders a list of dependencies under a `[<section>]`
// header. Short forms (`dep = "1.0"`) are preferred when the
// dependency has only a version requirement; the long inline-table
// form is emitted otherwise.
func writeDeps(b *strings.Builder, section string, deps []Dependency) {
	if len(deps) == 0 {
		return
	}
	fmt.Fprintf(b, "\n[%s]\n", section)
	sorted := append([]Dependency(nil), deps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, d := range sorted {
		writeDepLine(b, d)
	}
}

func writeDepLine(b *strings.Builder, d Dependency) {
	// Short form
	if d.VersionReq != "" &&
		d.Path == "" &&
		d.Git == nil &&
		d.PackageName == "" &&
		d.Registry == "" &&
		!d.Optional &&
		len(d.Features) == 0 &&
		d.DefaultFeats {
		fmt.Fprintf(b, "%s = %q\n", d.Name, d.VersionReq)
		return
	}
	// Long form
	fmt.Fprintf(b, "%s = { ", d.Name)
	var parts []string
	if d.VersionReq != "" {
		parts = append(parts, fmt.Sprintf("version = %q", d.VersionReq))
	}
	if d.Path != "" {
		parts = append(parts, fmt.Sprintf("path = %q", d.Path))
	}
	if d.Git != nil {
		parts = append(parts, fmt.Sprintf("git = %q", d.Git.URL))
		if d.Git.Tag != "" {
			parts = append(parts, fmt.Sprintf("tag = %q", d.Git.Tag))
		}
		if d.Git.Branch != "" {
			parts = append(parts, fmt.Sprintf("branch = %q", d.Git.Branch))
		}
		if d.Git.Rev != "" {
			parts = append(parts, fmt.Sprintf("rev = %q", d.Git.Rev))
		}
	}
	if d.PackageName != "" {
		parts = append(parts, fmt.Sprintf("package = %q", d.PackageName))
	}
	if d.Registry != "" {
		parts = append(parts, fmt.Sprintf("registry = %q", d.Registry))
	}
	if d.Optional {
		parts = append(parts, "optional = true")
	}
	if len(d.Features) > 0 {
		var qs []string
		for _, f := range d.Features {
			qs = append(qs, fmt.Sprintf("%q", f))
		}
		parts = append(parts, fmt.Sprintf("features = [%s]", strings.Join(qs, ", ")))
	}
	if !d.DefaultFeats {
		parts = append(parts, "default-features = false")
	}
	b.WriteString(strings.Join(parts, ", "))
	b.WriteString(" }\n")
}

func writeString(b *strings.Builder, key, val string) {
	fmt.Fprintf(b, "%s = %q\n", key, val)
}

func writeStringArray(b *strings.Builder, key string, vals []string) {
	fmt.Fprintf(b, "%s = [", key)
	for i, v := range vals {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", v)
	}
	b.WriteString("]\n")
}

// FindUp searches cwd and each parent directory for osty.toml,
// returning the absolute path of the first match. Used by commands
// like `osty add` that need to locate the project root.
//
// The caller supplies the walker (os-level), keeping this package
// free of direct filesystem coupling and easy to unit-test.
type Stater interface {
	Stat(path string) (bool, error)
}
