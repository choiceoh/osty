package osty

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// stableHasher is a thin wrapper around sha256 that exposes a handful
// of length-prefixed write methods. Every variable-length field is
// prefixed with its length so concatenating unrelated fields can't
// collide.
//
// Hashers are pooled via hasherPool because every query execution
// allocates one and the cascade fires several per keystroke; pooling
// keeps the hot path off the heap.
type stableHasher struct{ h hash.Hash }

var hasherPool = sync.Pool{
	New: func() any { return &stableHasher{h: sha256.New()} },
}

func newHasher() *stableHasher {
	s := hasherPool.Get().(*stableHasher)
	s.h.Reset()
	return s
}


// sum returns the accumulated hash and returns the hasher to the
// pool. Callers use this once per top-level hashFn (and once per
// intermediate hash in helpers like the SymTypes symbol-hashing
// inner loop) to avoid heap allocation on the analyze hot path.
func (s *stableHasher) sum() [32]byte {
	var out [32]byte
	s.h.Sum(out[:0])
	hasherPool.Put(s)
	return out
}

func (s *stableHasher) byte(b byte) { s.h.Write([]byte{b}) }

func (s *stableHasher) u32(n uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], n)
	s.h.Write(buf[:])
}

func (s *stableHasher) u64(n uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], n)
	s.h.Write(buf[:])
}

func (s *stableHasher) str(v string) {
	s.u32(uint32(len(v)))
	s.h.Write([]byte(v))
}

func (s *stableHasher) bytes(v []byte) {
	s.u32(uint32(len(v)))
	s.h.Write(v)
}

func (s *stableHasher) bool(v bool) {
	if v {
		s.byte(1)
	} else {
		s.byte(0)
	}
}

func (s *stableHasher) pos(p token.Pos) {
	s.u32(uint32(p.Offset))
	s.u32(uint32(p.Line))
	s.u32(uint32(p.Column))
}

// ---- Parse ----

// hashParseResult fingerprints a [ParseResult]. Because Parse is a
// pure function of [SourceText] and Parse only re-runs when SourceText
// bumped (which implies a source byte difference), the natural hash
// is the source bytes themselves — any byte-level diff produces a new
// Parse hash and dependents must not cut off. Downstream queries
// (Resolve, Check) gain the benefit of cutoff because THEIR hashes
// ignore parse-only differences like whitespace/comments that don't
// alter resolver or checker output.
func hashParseResult(r ParseResult) [32]byte {
	h := newHasher()
	h.bytes(r.Source)
	h.u32(uint32(len(r.Diags)))
	for _, d := range r.Diags {
		hashDiagnostic(h, d)
	}
	return h.sum()
}

// ---- Diagnostics ----

func hashDiagnostic(h *stableHasher, d *diag.Diagnostic) {
	if d == nil {
		h.byte(0)
		return
	}
	h.byte(1)
	h.byte(byte(d.Severity))
	h.str(d.Code)
	h.str(d.Message)
	h.u32(uint32(len(d.Spans)))
	for _, sp := range d.Spans {
		h.pos(sp.Span.Start)
		h.pos(sp.Span.End)
		h.str(sp.Label)
		h.bool(sp.Primary)
	}
	h.u32(uint32(len(d.Notes)))
	for _, n := range d.Notes {
		h.str(n)
	}
	h.str(d.Hint)
	h.u32(uint32(len(d.Suggestions)))
	for _, sg := range d.Suggestions {
		hashSuggestion(h, sg)
	}
}

func hashSuggestion(h *stableHasher, s diag.Suggestion) {
	h.str(s.Label)
	h.str(s.Replacement)
	h.pos(s.Span.Start)
	h.pos(s.Span.End)
	if s.CopyFrom != nil {
		h.byte(1)
		h.pos(s.CopyFrom.Start)
		h.pos(s.CopyFrom.End)
	} else {
		h.byte(0)
	}
	h.bool(s.MachineApplicable)
}

func hashDiagsList(h *stableHasher, ds []*diag.Diagnostic) {
	// Stable sort by (primary-span-offset, code, message). The
	// resolver and checker typically produce diagnostics in source
	// order already, but we sort defensively so hash equality is
	// insensitive to emission order in case that changes.
	sorted := make([]*diag.Diagnostic, len(ds))
	copy(sorted, ds)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		ap := primarySpanOffset(a)
		bp := primarySpanOffset(b)
		if ap != bp {
			return ap < bp
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
	h.u32(uint32(len(sorted)))
	for _, d := range sorted {
		hashDiagnostic(h, d)
	}
}

func primarySpanOffset(d *diag.Diagnostic) int {
	if d == nil {
		return -1
	}
	for _, s := range d.Spans {
		if s.Primary {
			return s.Span.Start.Offset
		}
	}
	if len(d.Spans) > 0 {
		return d.Spans[0].Span.Start.Offset
	}
	return -1
}

// ---- Symbols & Types ----
//
// Symbols and types have content-hash IDs computed once in their
// home packages ([resolve.Symbol.ID], [types.ID]). The hash.go file
// writes those precomputed [32]byte identities into the hasher
// instead of re-serializing each symbol / type's fields. This keeps
// per-query-run hashing to simple byte appends.

func hashSymbol(h *stableHasher, s *resolve.Symbol) {
	if s == nil {
		h.byte(0)
		return
	}
	h.byte(1)
	id := s.ID()
	h.h.Write(id[:])
}

func hashType(h *stableHasher, t types.Type) {
	id := types.ID(t)
	h.h.Write(id[:])
}

// ---- ResolvePackage ----

// hashResolvedPackage fingerprints the ResolvedPackage view. Captures
// every Refs/TypeRefs entry (sorted by head token offset), the
// package scope's exported symbols, and the diag list.
func hashResolvedPackage(rp *ResolvedPackage) [32]byte {
	h := newHasher()
	if rp == nil || rp.pkg == nil {
		return h.sum()
	}
	h.str(rp.pkg.Dir)
	h.str(rp.pkg.Name)

	// Files are already in lexicographic order by Path, but sort
	// defensively.
	files := make([]*resolve.PackageFile, len(rp.pkg.Files))
	copy(files, rp.pkg.Files)
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	h.u32(uint32(len(files)))
	for _, pf := range files {
		h.str(pf.Path)
		hashFileRefs(h, pf)
		h.u32(uint32(len(pf.ParseDiags)))
		for _, d := range pf.ParseDiags {
			hashDiagnostic(h, d)
		}
	}

	// Top-level declarations across all files. This captures
	// "package added/removed a top-level symbol" cases where no
	// existing reference changed but a new name became visible to
	// importers.
	hashPkgTopLevelDecls(h, rp.pkg)

	if rp.res != nil {
		hashDiagsList(h, rp.res.Diags)
	}
	return h.sum()
}

// hashFileRefs captures the per-file resolve output. Refs and TypeRefs
// are sorted by ident position; each entry is (offset, name, symbol).
func hashFileRefs(h *stableHasher, pf *resolve.PackageFile) {
	// Refs
	type refEntry struct {
		off  int
		name string
		sym  *resolve.Symbol
	}
	refs := make([]refEntry, 0, len(pf.RefIdents))
	for _, id := range pf.RefIdents {
		refs = append(refs, refEntry{off: id.Pos().Offset, name: id.Name, sym: pf.RefsByID[id.ID]})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].off != refs[j].off {
			return refs[i].off < refs[j].off
		}
		return refs[i].name < refs[j].name
	})
	h.u32(uint32(len(refs)))
	for _, r := range refs {
		h.u32(uint32(r.off))
		h.str(r.name)
		hashSymbol(h, r.sym)
	}

	// TypeRefs
	type typeRefEntry struct {
		off int
		sym *resolve.Symbol
	}
	tr := make([]typeRefEntry, 0, len(pf.TypeRefIdents))
	for _, nt := range pf.TypeRefIdents {
		tr = append(tr, typeRefEntry{off: nt.Pos().Offset, sym: pf.TypeRefsByID[nt.ID]})
	}
	sort.Slice(tr, func(i, j int) bool { return tr[i].off < tr[j].off })
	h.u32(uint32(len(tr)))
	for _, r := range tr {
		h.u32(uint32(r.off))
		hashSymbol(h, r.sym)
	}
}

// hashPkgTopLevelDecls walks every top-level declaration across all
// files in pkg and emits (path, offset, kind, name) for each.
// Captures the "added a top-level symbol" case that per-file Refs
// can't see when no existing reference changed.
//
// We hash the AST's declarations rather than PkgScope because
// resolve.Scope's internal name map is unexported and the declaration
// list is sufficient: every top-level symbol in PkgScope was installed
// from exactly one of these Decl nodes during declarePass.
func hashPkgTopLevelDecls(h *stableHasher, pkg *resolve.Package) {
	if pkg == nil {
		h.u32(0)
		return
	}
	type entry struct {
		path string
		off  int
		kind string
		name string
		pub  bool
	}
	var entries []entry
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, d := range pf.File.Decls {
			if d == nil {
				continue
			}
			name, kind, pub := declIdentity(d)
			entries = append(entries, entry{
				path: pf.Path,
				off:  d.Pos().Offset,
				kind: kind,
				name: name,
				pub:  pub,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		return entries[i].off < entries[j].off
	})
	h.u32(uint32(len(entries)))
	for _, e := range entries {
		h.str(e.path)
		h.u32(uint32(e.off))
		h.str(e.kind)
		h.str(e.name)
		h.bool(e.pub)
	}
}

// declIdentity projects an AST declaration onto its semantically
// relevant surface: the name, kind tag, and pub flag. This is what
// PkgScope captures after declarePass.
func declIdentity(d ast.Decl) (name, kind string, pub bool) {
	switch dd := d.(type) {
	case *ast.FnDecl:
		return dd.Name, "fn", dd.Pub
	case *ast.StructDecl:
		return dd.Name, "struct", dd.Pub
	case *ast.EnumDecl:
		return dd.Name, "enum", dd.Pub
	case *ast.InterfaceDecl:
		return dd.Name, "iface", dd.Pub
	case *ast.TypeAliasDecl:
		return dd.Name, "alias", dd.Pub
	case *ast.LetDecl:
		return dd.Name, "let", dd.Pub
	case *ast.UseDecl:
		if dd.Alias != "" {
			return dd.Alias, "use", false
		}
		if len(dd.Path) > 0 {
			return dd.Path[len(dd.Path)-1], "use", false
		}
		return "", "use", false
	}
	return "", "node", false
}

// ---- ResolveFile ----

// hashResolveFileResult fingerprints the per-file slice.
func hashResolveFileResult(r *resolve.Result) [32]byte {
	h := newHasher()
	if r == nil {
		return h.sum()
	}
	// Build an ephemeral PackageFile-like shape so we can reuse the
	// file ref hashing.
	pf := &resolve.PackageFile{Refs: r.Refs, TypeRefs: r.TypeRefs}
	hashFileRefs(h, pf)
	hashDiagsList(h, r.Diags)
	return h.sum()
}

// ---- CheckResult ----

// hashCheckResult fingerprints a check result. Because the underlying
// maps are keyed by AST pointers (unstable across re-parses), we
// project each entry onto a (position, type-hash) pair before sorting.
func hashCheckResult(r *check.Result) [32]byte {
	h := newHasher()
	if r == nil {
		return h.sum()
	}
	// Types: map[ast.Expr]types.Type
	type typeEntry struct {
		off int
		t   types.Type
	}
	tEntries := make([]typeEntry, 0, len(r.Types))
	for expr, t := range r.Types {
		if expr == nil {
			continue
		}
		tEntries = append(tEntries, typeEntry{off: expr.Pos().Offset, t: t})
	}
	sort.Slice(tEntries, func(i, j int) bool { return tEntries[i].off < tEntries[j].off })
	h.u32(uint32(len(tEntries)))
	for _, e := range tEntries {
		h.u32(uint32(e.off))
		hashType(h, e.t)
	}

	// LetTypes: map[ast.Node]types.Type
	lEntries := make([]typeEntry, 0, len(r.LetTypes))
	for node, t := range r.LetTypes {
		if node == nil {
			continue
		}
		lEntries = append(lEntries, typeEntry{off: node.Pos().Offset, t: t})
	}
	sort.Slice(lEntries, func(i, j int) bool { return lEntries[i].off < lEntries[j].off })
	h.u32(uint32(len(lEntries)))
	for _, e := range lEntries {
		h.u32(uint32(e.off))
		hashType(h, e.t)
	}

	// SymTypes: map[*Symbol]types.Type — sort by symbol hash.
	type symEntry struct {
		sh [32]byte
		t  types.Type
	}
	sEntries := make([]symEntry, 0, len(r.SymTypes))
	for sym, t := range r.SymTypes {
		ph := newHasher()
		hashSymbol(ph, sym)
		sEntries = append(sEntries, symEntry{sh: ph.sum(), t: t})
	}
	sort.Slice(sEntries, func(i, j int) bool {
		return bytes.Compare(sEntries[i].sh[:], sEntries[j].sh[:]) < 0
	})
	h.u32(uint32(len(sEntries)))
	for _, e := range sEntries {
		h.h.Write(e.sh[:])
		hashType(h, e.t)
	}

	// Instantiations: map[*ast.CallExpr][]types.Type
	type instEntry struct {
		off  int
		args []types.Type
	}
	iEntries := make([]instEntry, 0, len(r.InstantiationCalls))
	for _, call := range r.InstantiationCalls {
		if call == nil {
			continue
		}
		iEntries = append(iEntries, instEntry{off: call.Pos().Offset, args: r.InstantiationsByID[call.ID]})
	}
	sort.Slice(iEntries, func(i, j int) bool { return iEntries[i].off < iEntries[j].off })
	h.u32(uint32(len(iEntries)))
	for _, e := range iEntries {
		h.u32(uint32(e.off))
		h.u32(uint32(len(e.args)))
		for _, a := range e.args {
			hashType(h, a)
		}
	}

	// Diags
	hashDiagsList(h, r.Diags)
	return h.sum()
}

// ---- Lint, IdentIndex, FileDiagnostics ----

func hashLintResult(r *lint.Result) [32]byte {
	h := newHasher()
	if r == nil {
		return h.sum()
	}
	hashDiagsList(h, r.Diags)
	return h.sum()
}

func hashDiagList(ds []*diag.Diagnostic) [32]byte {
	h := newHasher()
	hashDiagsList(h, ds)
	return h.sum()
}

func hashIdentIndex(m map[int]*resolve.Symbol) [32]byte {
	h := newHasher()
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	h.u32(uint32(len(keys)))
	for _, k := range keys {
		h.u32(uint32(k))
		hashSymbol(h, m[k])
	}
	return h.sum()
}

// ---- Workspace check map ----

func hashCheckWorkspaceMap(m map[string]*check.Result) [32]byte {
	h := newHasher()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h.u32(uint32(len(keys)))
	for _, k := range keys {
		h.str(k)
		sub := hashCheckResult(m[k])
		h.h.Write(sub[:])
	}
	return h.sum()
}

// ---- Input hashers ----

func hashBytesInput(b []byte) [32]byte { return sha256.Sum256(b) }

func hashStringSlice(ss []string) [32]byte {
	// Fast path for the single-file case — single-file LSP analyze
	// hits this on every keystroke with a one-element slice.
	if len(ss) <= 1 {
		h := newHasher()
		h.u32(uint32(len(ss)))
		if len(ss) == 1 {
			h.str(ss[0])
		}
		return h.sum()
	}
	h := newHasher()
	sorted := make([]string, len(ss))
	copy(sorted, ss)
	sort.Strings(sorted)
	h.u32(uint32(len(sorted)))
	for _, s := range sorted {
		h.str(s)
	}
	return h.sum()
}

