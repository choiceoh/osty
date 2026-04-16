// api snapshot: a stable JSON description of every package's
// exported API surface in the project. The snapshot drives the
// `--semver` break detector by giving "before" and "after" a
// canonical, position-independent shape to diff.
//
// Structural choices:
//
//   - Symbols include not just top-level pub declarations but also
//     pub fields of pub structs and every variant of a pub enum,
//     keyed as "Type.Member" so a removed/renamed field produces
//     a Removed entry in the diff exactly the way a removed
//     top-level fn does.
//   - Function and struct signatures are encoded into Sig as a
//     compact text rendering (`(Int, String) -> Bool`,
//     `{name: String, age: Int}`, `Some(Int) | None`). Comparing
//     Sig as a string makes the diff insensitive to AST positions
//     and whitespace while still catching every parameter / return /
//     field type change.
//   - Method receivers, generic parameters, and constraint lists
//     are part of Sig too — adding a constraint is a breaking
//     change for downstream callers.

package cihost

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// SnapshotSchemaVersion is the integer embedded in every emitted
// snapshot. Bumped on incompatible field changes so older tools
// refuse to load a too-new snapshot instead of silently
// misinterpreting it.
const SnapshotSchemaVersion = 2

// Snapshot is the on-disk representation of a project's public
// API. Fields are JSON-stable (camelCase) so snapshots committed
// today are readable by every later toolchain.
//
// In v2 the top-level Symbols field is per-package: a workspace
// snapshot records every loaded package keyed by import path.
// Single-package projects still write one entry under the empty
// string for backwards compatibility with v1 readers (the v1
// `package` + `symbols` fields are also written when the project
// has exactly one package, so older tools keep working).
type Snapshot struct {
	Schema   int                 `json:"schema"`
	Package  string              `json:"package,omitempty"` // legacy/v1 single-package convenience
	Version  string              `json:"version,omitempty"`
	Edition  string              `json:"edition,omitempty"`
	Symbols  []Symbol            `json:"symbols,omitempty"` // legacy/v1 single-package convenience
	Packages map[string][]Symbol `json:"packages,omitempty"`
}

// Symbol is one exported declaration. Top-level decls use a bare
// Name; struct fields and enum variants use `Owner.Member`.
//
// Sig is a best-effort textual signature — see the package
// docstring for the format. Empty Sig means "structural shape
// not relevant" (e.g. variants without payloads).
type Symbol struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Sig  string `json:"sig,omitempty"`
}

// CapturePackage walks pkg's source files for top-level pub
// declarations + their pub members and returns the per-package
// Symbol list. Private declarations are skipped on purpose:
// changing them is never breaking by definition.
//
// Workspace callers should invoke CapturePackage per package and
// merge the results with NewWorkspaceSnapshot.
//
// pkgVersion / edition propagate into the Snapshot only via the
// workspace-level constructor; this entry point returns the raw
// symbol list so it composes cleanly across packages.
func CapturePackage(pkg *resolve.Package) []Symbol {
	if pkg == nil {
		return nil
	}
	var out []Symbol
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, d := range pf.File.Decls {
			out = append(out, capturePubDecl(d)...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// NewSingleSnapshot builds a Snapshot for a one-package project,
// populating both the v1 (`package`+`symbols`) and v2
// (`packages`) views so old + new readers both see the data.
func NewSingleSnapshot(pkg *resolve.Package, version, edition string) *Snapshot {
	syms := CapturePackage(pkg)
	name := ""
	if pkg != nil {
		name = pkg.Name
	}
	s := &Snapshot{
		Schema:  SnapshotSchemaVersion,
		Package: name,
		Version: version,
		Edition: edition,
		Symbols: syms,
	}
	s.Packages = map[string][]Symbol{name: syms}
	return s
}

// NewWorkspaceSnapshot collects every package in pkgs into one
// Snapshot. Packages keyed by Name; if two packages share a name
// (legitimate in disjoint workspace members) the later entry
// wins — but `osty ci snapshot` operates over a single workspace
// where names are unique, so the conflict is impossible in
// practice.
//
// version / edition come from the manifest's [package] section
// when one exists; for a virtual workspace they're empty.
func NewWorkspaceSnapshot(pkgs []*resolve.Package, version, edition string) *Snapshot {
	s := &Snapshot{
		Schema:   SnapshotSchemaVersion,
		Version:  version,
		Edition:  edition,
		Packages: map[string][]Symbol{},
	}
	for _, p := range pkgs {
		if p == nil {
			continue
		}
		s.Packages[p.Name] = CapturePackage(p)
	}
	// Single-package convenience: also fill the v1 fields so v1
	// readers don't see an empty snapshot.
	if len(pkgs) == 1 && pkgs[0] != nil {
		s.Package = pkgs[0].Name
		s.Symbols = s.Packages[pkgs[0].Name]
	}
	return s
}

// capturePubDecl returns the Symbol list contributed by one
// top-level decl. Private decls return nil.
func capturePubDecl(d ast.Decl) []Symbol {
	switch x := d.(type) {
	case *ast.FnDecl:
		// Methods (Recv != nil) appear inside StructDecl.Methods
		// / EnumDecl.Methods / InterfaceDecl.Methods; the raw
		// Decls list only contains free functions, so we never
		// double-count methods here.
		if !x.Pub || x.Recv != nil {
			return nil
		}
		return []Symbol{{
			Name: x.Name,
			Kind: "function",
			Sig:  formatFnSig(x),
		}}
	case *ast.StructDecl:
		if !x.Pub {
			return nil
		}
		out := []Symbol{{
			Name: x.Name,
			Kind: "struct",
			Sig:  formatStructSig(x),
		}}
		for _, f := range x.Fields {
			if f == nil || !f.Pub {
				continue
			}
			out = append(out, Symbol{
				Name: x.Name + "." + f.Name,
				Kind: "field",
				Sig:  formatType(f.Type),
			})
		}
		for _, m := range x.Methods {
			if m == nil || !m.Pub {
				continue
			}
			out = append(out, Symbol{
				Name: x.Name + "." + m.Name,
				Kind: "method",
				Sig:  formatFnSig(m),
			})
		}
		return out
	case *ast.EnumDecl:
		if !x.Pub {
			return nil
		}
		out := []Symbol{{
			Name: x.Name,
			Kind: "enum",
			Sig:  formatEnumSig(x),
		}}
		// Every variant is part of the enum's pub surface — the
		// language has no notion of a private variant — so we
		// always record them. Removing a variant is breaking
		// regardless of pub-ness on the enum itself.
		for _, v := range x.Variants {
			if v == nil {
				continue
			}
			out = append(out, Symbol{
				Name: x.Name + "." + v.Name,
				Kind: "variant",
				Sig:  formatVariantSig(v),
			})
		}
		for _, m := range x.Methods {
			if m == nil || !m.Pub {
				continue
			}
			out = append(out, Symbol{
				Name: x.Name + "." + m.Name,
				Kind: "method",
				Sig:  formatFnSig(m),
			})
		}
		return out
	case *ast.InterfaceDecl:
		if !x.Pub {
			return nil
		}
		out := []Symbol{{
			Name: x.Name,
			Kind: "interface",
			Sig:  formatInterfaceSig(x),
		}}
		for _, m := range x.Methods {
			if m == nil {
				continue
			}
			out = append(out, Symbol{
				Name: x.Name + "." + m.Name,
				Kind: "method",
				Sig:  formatFnSig(m),
			})
		}
		return out
	case *ast.TypeAliasDecl:
		if !x.Pub {
			return nil
		}
		return []Symbol{{
			Name: x.Name,
			Kind: "type alias",
			Sig:  formatGenerics(x.Generics) + "= " + formatType(x.Target),
		}}
	case *ast.LetDecl:
		if !x.Pub {
			return nil
		}
		// Top-level `pub let` advertises a value binding; sig is
		// the type annotation when present.
		return []Symbol{{
			Name: x.Name,
			Kind: "binding",
			Sig:  formatType(x.Type),
		}}
	}
	return nil
}

// ---- Signature renderers ----

func formatFnSig(f *ast.FnDecl) string {
	var b strings.Builder
	if f.Recv != nil {
		if f.Recv.Mut {
			b.WriteString("(mut self) ")
		} else {
			b.WriteString("(self) ")
		}
	}
	b.WriteString(formatGenerics(f.Generics))
	b.WriteString("(")
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		if p == nil {
			continue
		}
		if p.Name != "" {
			b.WriteString(p.Name)
			b.WriteString(": ")
		}
		b.WriteString(formatType(p.Type))
	}
	b.WriteString(")")
	if f.ReturnType != nil {
		b.WriteString(" -> ")
		b.WriteString(formatType(f.ReturnType))
	}
	return b.String()
}

func formatStructSig(s *ast.StructDecl) string {
	var b strings.Builder
	b.WriteString(formatGenerics(s.Generics))
	b.WriteString("{")
	first := true
	for _, f := range s.Fields {
		if f == nil || !f.Pub {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(formatType(f.Type))
	}
	b.WriteString("}")
	return b.String()
}

func formatEnumSig(e *ast.EnumDecl) string {
	var b strings.Builder
	b.WriteString(formatGenerics(e.Generics))
	for i, v := range e.Variants {
		if v == nil {
			continue
		}
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(formatVariantSig(v))
	}
	return b.String()
}

func formatVariantSig(v *ast.Variant) string {
	if len(v.Fields) == 0 {
		return v.Name
	}
	var parts []string
	for _, t := range v.Fields {
		parts = append(parts, formatType(t))
	}
	return v.Name + "(" + strings.Join(parts, ", ") + ")"
}

func formatInterfaceSig(i *ast.InterfaceDecl) string {
	var b strings.Builder
	b.WriteString(formatGenerics(i.Generics))
	b.WriteString("{")
	for j, m := range i.Methods {
		if m == nil {
			continue
		}
		if j > 0 {
			b.WriteString("; ")
		}
		b.WriteString(m.Name)
		b.WriteString(formatFnSig(m))
	}
	b.WriteString("}")
	return b.String()
}

func formatGenerics(gs []*ast.GenericParam) string {
	if len(gs) == 0 {
		return ""
	}
	var parts []string
	for _, g := range gs {
		if g == nil {
			continue
		}
		s := g.Name
		if len(g.Constraints) > 0 {
			var cparts []string
			for _, c := range g.Constraints {
				cparts = append(cparts, formatType(c))
			}
			s += ": " + strings.Join(cparts, " + ")
		}
		parts = append(parts, s)
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func formatType(t ast.Type) string {
	if t == nil {
		return "()"
	}
	switch x := t.(type) {
	case *ast.NamedType:
		s := strings.Join(x.Path, ".")
		if len(x.Args) > 0 {
			var parts []string
			for _, a := range x.Args {
				parts = append(parts, formatType(a))
			}
			s += "<" + strings.Join(parts, ", ") + ">"
		}
		return s
	case *ast.OptionalType:
		return formatType(x.Inner) + "?"
	case *ast.TupleType:
		var parts []string
		for _, e := range x.Elems {
			parts = append(parts, formatType(e))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *ast.FnType:
		var parts []string
		for _, p := range x.Params {
			parts = append(parts, formatType(p))
		}
		s := "fn(" + strings.Join(parts, ", ") + ")"
		if x.ReturnType != nil {
			s += " -> " + formatType(x.ReturnType)
		}
		return s
	}
	return "?"
}

// ---- Snapshot I/O ----

// WriteSnapshot writes s to path with indentation suitable for
// human review and small diff hunks — the same formatting style
// `osty.lock` uses for the same reason.
func WriteSnapshot(path string, s *Snapshot) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

// ReadSnapshot loads a snapshot from disk. v1 snapshots (no
// `packages` field) are upgraded in memory: their flat Symbols
// list is moved under the empty-string key so Compare can treat
// every snapshot uniformly.
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
		s.Schema = 1
	}
	if s.Schema > SnapshotSchemaVersion {
		return nil, fmt.Errorf("%s: snapshot schema %d newer than this toolchain (max %d)",
			path, s.Schema, SnapshotSchemaVersion)
	}
	if s.Packages == nil {
		s.Packages = map[string][]Symbol{}
		s.Packages[s.Package] = s.Symbols
	}
	return &s, nil
}

// Diff is the set of differences between two snapshots, broken
// down per package.
type Diff struct {
	Removed []SymbolRef // in baseline, gone from current → breaking
	Added   []SymbolRef // new in current, not in baseline → additive
	Changed []SymbolRef // same (name, kind), differing Sig → breaking
}

// SymbolRef is a Symbol annotated with the package it lives in.
// Workspace-aware diffs need this so a single CI408 message can
// say "auth.User.email was removed" instead of just "User.email".
type SymbolRef struct {
	Pkg    string
	Symbol Symbol
}

// Qualified returns "pkg.Name" or just "Name" when pkg is empty.
func (s SymbolRef) Qualified() string {
	if s.Pkg == "" {
		return s.Symbol.Name
	}
	return s.Pkg + "." + s.Symbol.Name
}

// Compare reports removed / added / signature-changed symbols
// between baseline and current, fanning out across every package
// either side advertises. A package present in baseline but not
// in current is treated as "every symbol removed" — usually a
// rename, surfaced as breaking so the user notices.
func Compare(baseline, current *Snapshot) Diff {
	var d Diff
	if baseline == nil || current == nil {
		return d
	}
	pkgs := unionPkgKeys(baseline, current)
	for _, p := range pkgs {
		comparePackage(p, baseline.Packages[p], current.Packages[p], &d)
	}
	sortSymbolRefs(d.Removed)
	sortSymbolRefs(d.Added)
	sortSymbolRefs(d.Changed)
	return d
}

func comparePackage(pkg string, base, cur []Symbol, out *Diff) {
	curIdx := indexByKey(cur)
	baseIdx := indexByKey(base)
	for k, bs := range baseIdx {
		cs, ok := curIdx[k]
		if !ok {
			out.Removed = append(out.Removed, SymbolRef{Pkg: pkg, Symbol: bs})
			continue
		}
		if bs.Sig != cs.Sig {
			out.Changed = append(out.Changed, SymbolRef{Pkg: pkg, Symbol: cs})
		}
	}
	for k, cs := range curIdx {
		if _, ok := baseIdx[k]; !ok {
			out.Added = append(out.Added, SymbolRef{Pkg: pkg, Symbol: cs})
		}
	}
}

func unionPkgKeys(a, b *Snapshot) []string {
	keys := map[string]bool{}
	for k := range a.Packages {
		keys[k] = true
	}
	for k := range b.Packages {
		keys[k] = true
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func indexByKey(syms []Symbol) map[string]Symbol {
	out := make(map[string]Symbol, len(syms))
	for _, s := range syms {
		out[symKey(s)] = s
	}
	return out
}

// symKey is the (name, kind) identity of a symbol. Kind is part
// of the key because a rename-and-retype (struct → fn with same
// name) is a breaking removal AND a breaking addition, not a
// signature change.
func symKey(s Symbol) string { return s.Kind + " " + s.Name }

func sortSymbolRefs(rs []SymbolRef) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Pkg != rs[j].Pkg {
			return rs[i].Pkg < rs[j].Pkg
		}
		return rs[i].Symbol.Name < rs[j].Symbol.Name
	})
}
