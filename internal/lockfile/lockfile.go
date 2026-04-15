// Package lockfile reads and writes osty.lock, the reproducible
// dependency snapshot that pins every package the resolver picked.
//
// Design notes:
//
//   - One lockfile per project, written alongside osty.toml.
//     Workspaces share a single lockfile at the root.
//
//   - Content is TOML, parsed via internal/tomlparse. Schema:
//
//     version = 1
//
//     [[package]]
//     name = "simple"
//     version = "1.2.3"
//     source = "registry+https://registry.osty.dev"
//     checksum = "sha256:abcdef..."
//     dependencies = ["other 1.0.0"]
//
//   - `source` is a URI-like pin that fully describes where the
//     package came from: "registry+URL", "git+URL?<ref>#<rev>",
//     "path+<relative>".
//
//   - `checksum` is required for registry + git sources; omitted for
//     local path sources. Verified on every resolve.
//
//   - `dependencies` uses the "name version" form (cargo style) so
//     diamond dependencies reference specific nodes, not
//     requirements.
//
// The package does NOT know how to resolve dependencies — that lives
// in internal/pkgmgr. Lockfile just stores / loads the final outcome.
package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/tomlparse"
)

// LockFile is the canonical name at project roots.
const LockFile = "osty.lock"

// SchemaVersion is the integer version field at the top of osty.lock.
// Bumped on incompatible schema changes so older tools refuse to
// load a too-new lockfile instead of silently misinterpreting it.
const SchemaVersion = 1

// Lock is the in-memory representation of osty.lock. Packages is
// sorted by (Name, Version) on write for stable diffs.
type Lock struct {
	Version  int
	Packages []Package
}

// Package is one pinned entry. Every field is required except
// Checksum (empty for local path sources) and Dependencies (empty
// for leaf packages).
type Package struct {
	Name         string
	Version      string
	Source       string       // "registry+URL", "git+URL?tag=v1.0.0#commit", "path+../foo"
	Checksum     string       // "sha256:<hex>" or empty for path sources
	Dependencies []Dependency // "name version" form
}

// Dependency is one edge in the lockfile graph. Name and Version
// point at another Lock.Packages entry; Source is optional — when
// non-empty it disambiguates packages that exist in multiple
// registries / sources with the same name.
type Dependency struct {
	Name    string
	Version string
	Source  string // optional
}

// String renders a dep as it appears in osty.lock:
//
//	"name version" (common)
//	"name version (source)" (when Source is set)
func (d Dependency) String() string {
	if d.Source == "" {
		return fmt.Sprintf("%s %s", d.Name, d.Version)
	}
	return fmt.Sprintf("%s %s (%s)", d.Name, d.Version, d.Source)
}

// ParseDependency parses the string form back into a Dependency.
// Returns an error for unrecognized shapes so a hand-edited lockfile
// doesn't silently deserialize to junk.
func ParseDependency(s string) (Dependency, error) {
	s = strings.TrimSpace(s)
	var src string
	if open := strings.Index(s, "("); open > 0 && strings.HasSuffix(s, ")") {
		src = s[open+1 : len(s)-1]
		s = strings.TrimSpace(s[:open])
	}
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return Dependency{}, fmt.Errorf("invalid dependency %q", s)
	}
	return Dependency{Name: parts[0], Version: parts[1], Source: src}, nil
}

// Read loads osty.lock from dir (the project root directory
// containing osty.toml). Returns (nil, nil) when the file does not
// exist — a missing lockfile is not an error; the resolver just
// generates one on demand.
func Read(dir string) (*Lock, error) {
	p := filepath.Join(dir, LockFile)
	src, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lock, err := Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return lock, nil
}

// Write marshals l and writes it to dir/osty.lock.
func Write(dir string, l *Lock) error {
	p := filepath.Join(dir, LockFile)
	return os.WriteFile(p, Marshal(l), 0o644)
}

// Parse reads osty.lock bytes into a Lock. Refuses lockfiles whose
// `version` field is newer than SchemaVersion — the user is asked to
// upgrade their osty toolchain rather than letting us guess.
func Parse(src []byte) (*Lock, error) {
	root, err := tomlparse.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("osty.lock:%v", err)
	}
	l := &Lock{Version: SchemaVersion}
	if v, ok := root.Get("version"); ok {
		if v.Int == nil {
			return nil, fmt.Errorf("osty.lock: `version` must be an integer")
		}
		l.Version = int(*v.Int)
		if l.Version > SchemaVersion {
			return nil, fmt.Errorf("osty.lock: version %d newer than this toolchain understands (max %d). upgrade osty",
				l.Version, SchemaVersion)
		}
	}
	pkgsVal, ok := root.Get("package")
	if !ok {
		return l, nil
	}
	if pkgsVal.Arr == nil {
		return nil, fmt.Errorf("osty.lock: [[package]] must be an array of tables")
	}
	for _, v := range pkgsVal.Arr.Values {
		if v.Tbl == nil {
			return nil, fmt.Errorf("osty.lock:%d: [[package]] entry must be a table", v.Line)
		}
		p, err := parseLockPackage(v.Tbl)
		if err != nil {
			return nil, err
		}
		l.Packages = append(l.Packages, p)
	}
	return l, nil
}

func parseLockPackage(t *tomlparse.Table) (Package, error) {
	p := Package{}
	name, ok := t.Get("name")
	if !ok || name.Str == nil {
		return Package{}, fmt.Errorf("osty.lock:%d: [[package]].name missing or non-string", t.Line)
	}
	p.Name = *name.Str
	ver, ok := t.Get("version")
	if !ok || ver.Str == nil {
		return Package{}, fmt.Errorf("osty.lock:%d: [[package]].version missing or non-string", t.Line)
	}
	p.Version = *ver.Str
	if v, ok := t.Get("source"); ok {
		if v.Str == nil {
			return Package{}, fmt.Errorf("osty.lock:%d: source must be a string", v.Line)
		}
		p.Source = *v.Str
	}
	if v, ok := t.Get("checksum"); ok {
		if v.Str == nil {
			return Package{}, fmt.Errorf("osty.lock:%d: checksum must be a string", v.Line)
		}
		p.Checksum = *v.Str
	}
	if v, ok := t.Get("dependencies"); ok {
		if v.Arr == nil {
			return Package{}, fmt.Errorf("osty.lock:%d: dependencies must be an array of strings", v.Line)
		}
		for _, d := range v.Arr.Values {
			if d.Str == nil {
				return Package{}, fmt.Errorf("osty.lock:%d: dependency must be a string", d.Line)
			}
			dep, err := ParseDependency(*d.Str)
			if err != nil {
				return Package{}, fmt.Errorf("osty.lock:%d: %w", d.Line, err)
			}
			p.Dependencies = append(p.Dependencies, dep)
		}
	}
	return p, nil
}

// Marshal renders l as TOML bytes. The output is deterministic:
// packages are sorted by (Name, Version) and dependencies within
// each package by (Name, Version) so diffs are small.
func Marshal(l *Lock) []byte {
	var b strings.Builder
	b.WriteString("# This file is auto-generated by osty.\n")
	b.WriteString("# Do not edit manually; `osty update` regenerates it.\n\n")
	fmt.Fprintf(&b, "version = %d\n", l.Version)

	pkgs := append([]Package(nil), l.Packages...)
	sort.SliceStable(pkgs, func(i, j int) bool {
		if pkgs[i].Name != pkgs[j].Name {
			return pkgs[i].Name < pkgs[j].Name
		}
		return pkgs[i].Version < pkgs[j].Version
	})
	for _, p := range pkgs {
		b.WriteString("\n[[package]]\n")
		fmt.Fprintf(&b, "name = %q\n", p.Name)
		fmt.Fprintf(&b, "version = %q\n", p.Version)
		if p.Source != "" {
			fmt.Fprintf(&b, "source = %q\n", p.Source)
		}
		if p.Checksum != "" {
			fmt.Fprintf(&b, "checksum = %q\n", p.Checksum)
		}
		if len(p.Dependencies) > 0 {
			deps := append([]Dependency(nil), p.Dependencies...)
			sort.SliceStable(deps, func(i, j int) bool {
				if deps[i].Name != deps[j].Name {
					return deps[i].Name < deps[j].Name
				}
				return deps[i].Version < deps[j].Version
			})
			b.WriteString("dependencies = [\n")
			for _, d := range deps {
				fmt.Fprintf(&b, "    %q,\n", d.String())
			}
			b.WriteString("]\n")
		}
	}
	return []byte(b.String())
}

// Find looks up a package in l by name + version. Returns nil when
// there is no such entry. Useful for resolver code that wants to
// reuse a previously-pinned version.
func (l *Lock) Find(name, version string) *Package {
	for i := range l.Packages {
		if l.Packages[i].Name == name && l.Packages[i].Version == version {
			return &l.Packages[i]
		}
	}
	return nil
}

// FindByName returns every lockfile entry with the given name.
// Multiple entries are possible when the resolver admits two versions
// of the same package (diamond conflict). Ordering matches on-disk
// order (sorted by Version in a Marshaled file).
func (l *Lock) FindByName(name string) []Package {
	var out []Package
	for _, p := range l.Packages {
		if p.Name == name {
			out = append(out, p)
		}
	}
	return out
}
