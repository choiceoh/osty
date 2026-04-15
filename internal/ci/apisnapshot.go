// api snapshot: a stable JSON description of a package's exported
// API surface. The snapshot is intentionally minimal — just enough
// structure to detect breaking removals between two versions.
// Richer signature diffing (type-level compatibility) is future
// work that the type checker's generalised type printing will
// unlock.

package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/osty/osty/internal/resolve"
)

// SnapshotSchemaVersion is the integer embedded in every emitted
// snapshot. Bumped on incompatible field changes so older tools
// refuse to load a too-new snapshot instead of silently
// misinterpreting it.
const SnapshotSchemaVersion = 1

// Snapshot is the on-disk representation of a package's public
// API. Fields are JSON-stable (camelCase) so snapshots committed
// today are readable by every later toolchain.
type Snapshot struct {
	Schema   int      `json:"schema"`
	Package  string   `json:"package"`
	Version  string   `json:"version,omitempty"`
	Edition  string   `json:"edition,omitempty"`
	Symbols  []Symbol `json:"symbols"`
}

// Symbol is one exported top-level declaration.
//
// Kind mirrors resolve.SymbolKind.String() so snapshots are
// human-readable without needing a lookup table. Sig is a
// best-effort signature hash — for functions it's a stringified
// parameter / return shape; for other kinds it's currently empty
// but reserved for future fills.
type Symbol struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Sig  string `json:"sig,omitempty"`
}

// CapturePackage walks a resolved Package and returns the
// Snapshot of its exported API. Only symbols with Pub==true are
// recorded; private declarations are explicitly excluded so
// internal refactors don't show up as "breaking".
//
// pkgVersion / edition may be empty when the caller doesn't have
// a manifest handy (ad-hoc `osty ci snapshot DIR` against a bare
// tree).
func CapturePackage(pkg *resolve.Package, pkgVersion, edition string) *Snapshot {
	s := &Snapshot{
		Schema:  SnapshotSchemaVersion,
		Version: pkgVersion,
		Edition: edition,
	}
	if pkg == nil {
		return s
	}
	s.Package = pkg.Name
	if pkg.PkgScope == nil {
		return s
	}
	for name, sym := range pkg.PkgScope.Symbols() {
		if sym == nil || sym.IsBuiltin() {
			continue
		}
		// Private declarations aren't part of the exported API;
		// changes to them are never "breaking" by definition.
		if !sym.Pub {
			continue
		}
		s.Symbols = append(s.Symbols, Symbol{
			Name: name,
			Kind: sym.Kind.String(),
		})
	}
	sort.Slice(s.Symbols, func(i, j int) bool {
		if s.Symbols[i].Name != s.Symbols[j].Name {
			return s.Symbols[i].Name < s.Symbols[j].Name
		}
		return s.Symbols[i].Kind < s.Symbols[j].Kind
	})
	return s
}

// WriteSnapshot writes s to path with indentation suitable for
// human review and small diff hunks — the same formatting style
// `osty.lock` uses for the same reason.
func WriteSnapshot(path string, s *Snapshot) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Trailing newline so POSIX tools (cat, diff, git) behave.
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

// ReadSnapshot loads a snapshot from disk. Returns an error for
// malformed JSON, unknown schema versions, or missing files.
func ReadSnapshot(path string) (*Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Schema == 0 {
		// Defensive: accept a missing schema field as v1 so
		// hand-crafted baselines still load.
		s.Schema = 1
	}
	if s.Schema > SnapshotSchemaVersion {
		return nil, fmt.Errorf("%s: snapshot schema %d newer than this toolchain (max %d)",
			path, s.Schema, SnapshotSchemaVersion)
	}
	return &s, nil
}

// Diff is the set of differences between two snapshots.
type Diff struct {
	Removed []Symbol // in baseline, gone from current → breaking
	Added   []Symbol // new in current, not in baseline → additive
	Changed []Symbol // same (name, kind) but other metadata differs
}

// Compare reports removed / added / signature-changed symbols
// between baseline and current. "Changed" today is intentionally
// narrow — when both sides record a non-empty Sig that differs we
// flag it, but since CapturePackage doesn't yet populate Sig this
// bucket stays empty in practice. The field is public anyway so
// downstream tools (and future type-aware snapshots) can populate
// it without another API break.
func Compare(baseline, current *Snapshot) Diff {
	var d Diff
	if baseline == nil || current == nil {
		return d
	}
	cur := map[string]Symbol{}
	for _, s := range current.Symbols {
		cur[symKey(s)] = s
	}
	base := map[string]Symbol{}
	for _, s := range baseline.Symbols {
		base[symKey(s)] = s
	}
	for k, bs := range base {
		cs, ok := cur[k]
		if !ok {
			d.Removed = append(d.Removed, bs)
			continue
		}
		if bs.Sig != "" && cs.Sig != "" && bs.Sig != cs.Sig {
			d.Changed = append(d.Changed, cs)
		}
	}
	for k, cs := range cur {
		if _, ok := base[k]; !ok {
			d.Added = append(d.Added, cs)
		}
	}
	sort.Slice(d.Removed, func(i, j int) bool { return d.Removed[i].Name < d.Removed[j].Name })
	sort.Slice(d.Added, func(i, j int) bool { return d.Added[i].Name < d.Added[j].Name })
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].Name < d.Changed[j].Name })
	return d
}

// symKey is the (name, kind) identity of a symbol. Kind is part
// of the key because a rename-and-retype (struct → fn with same
// name) is a breaking removal AND a breaking addition, not a
// signature change.
func symKey(s Symbol) string { return s.Kind + " " + s.Name }
