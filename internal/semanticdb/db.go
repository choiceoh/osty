// Package semanticdb collects authoritative frontend facts in one stable,
// structured surface. It is intentionally independent from the legacy Go AST,
// resolver.Scope, and check.Result maps so new consumers can move away from
// span rematching and AST-keyed overlays one query at a time.
package semanticdb

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/token"
)

// File describes one source segment that contributed facts to the DB. Offsets
// in Resolve and Check facts are package offsets; Base converts them back to a
// file-relative span before CanonicalMap is applied.
type File struct {
	Path           string
	Name           string
	Base           int
	Source         []byte
	OriginalSource []byte
	SourceMap      *sourcemap.Map
	LineStarts     []int
}

// DB is the central semantic database for one frontend run/package. Resolve is
// always value-owned by the DB. Check is optional because resolve-only callers
// can build the DB before type checking runs.
type DB struct {
	PackageID string
	Files     []File
	Resolve   api.ResolveResult
	Check     *api.CheckResult

	resolveIndex *ResolveIndex
	checkIndex   *api.CheckResultIndex
}

// New builds a DB from resolved facts and source segments.
func New(files []File, resolved api.ResolveResult) *DB {
	db := &DB{
		PackageID: resolved.PackageID,
		Files:     cloneFiles(files),
		Resolve:   resolved,
	}
	return db
}

// FromCheck builds a check-only DB. It is useful for arena-direct or
// single-file checker paths that do not have a package resolve DB yet.
func FromCheck(checked api.CheckResult) *DB {
	checked.EnsureStableIDs()
	return &DB{Check: &checked}
}

// WithCheck returns a shallow DB copy with checked facts attached. The original
// DB remains resolve-only, which lets resolver caches stay read-only.
func (db *DB) WithCheck(checked api.CheckResult) *DB {
	checked.EnsureStableIDs()
	if db == nil {
		return FromCheck(checked)
	}
	out := *db
	out.Check = &checked
	out.checkIndex = nil
	return &out
}

// ResolveIndex returns stable lookup tables over resolver facts.
func (db *DB) ResolveIndex() ResolveIndex {
	if db == nil {
		return emptyResolveIndex()
	}
	if db.resolveIndex == nil {
		idx := buildResolveIndex(&db.Resolve)
		db.resolveIndex = &idx
	}
	return *db.resolveIndex
}

// CheckIndex returns stable lookup tables over checker facts.
func (db *DB) CheckIndex() api.CheckResultIndex {
	if db == nil || db.Check == nil {
		return (*api.CheckResult)(nil).Index()
	}
	if db.checkIndex == nil {
		idx := db.Check.Index()
		db.checkIndex = &idx
	}
	return *db.checkIndex
}

// FileForPath returns the source segment for path, if present.
func (db *DB) FileForPath(path string) *File {
	if db == nil || path == "" {
		return nil
	}
	for i := range db.Files {
		if db.Files[i].Path == path {
			return &db.Files[i]
		}
	}
	return nil
}

// FileForOffset returns the source segment that owns a package offset.
func (db *DB) FileForOffset(offset int) *File {
	if db == nil {
		return nil
	}
	for i := range db.Files {
		file := &db.Files[i]
		if len(file.Source) == 0 {
			continue
		}
		if offset >= file.Base && offset <= file.Base+len(file.Source) {
			return file
		}
	}
	return nil
}

// OriginalSourceBytes returns the source bytes that original/source-mapped DB
// spans address. It falls back to Source when no canonicalization occurred.
func (f *File) OriginalSourceBytes() []byte {
	if f == nil {
		return nil
	}
	if len(f.OriginalSource) > 0 {
		return f.OriginalSource
	}
	return f.Source
}

// PackageOffset converts a file-relative byte offset to the DB's package-wide
// offset space.
func (db *DB) PackageOffset(path string, relative int) (int, bool) {
	file := db.FileForPath(path)
	if file == nil || relative < 0 || relative > len(file.Source) {
		return 0, false
	}
	return file.Base + relative, true
}

// OriginalOffset maps a package offset back to the original file's byte offset.
// If the file has no canonical source map, the file-relative offset is returned.
func (db *DB) OriginalOffset(path string, packageOffset int) (int, bool) {
	file := db.FileForPath(path)
	if file == nil {
		return 0, false
	}
	rel := packageOffset - file.Base
	if rel < 0 || rel > len(file.Source) {
		return 0, false
	}
	if file.SourceMap == nil {
		return rel, true
	}
	remapped, ok := file.SourceMap.RemapSpan(diag.Span{
		Start: token.Pos{Offset: rel},
		End:   token.Pos{Offset: rel},
	})
	if !ok {
		return 0, false
	}
	return remapped.Start.Offset, true
}

// NodeKey identifies a selfhost node in package offset space.
type NodeKey struct {
	File       string
	Node       int
	Start, End int
}

// Span is an original-source byte span. It is what editor-facing consumers want
// after DB offsets and canonical source maps have been resolved.
type Span struct {
	File       string
	Start, End int
}

// Hover describes the best semantic record at a source offset.
type Hover struct {
	Name   string
	Kind   string
	Type   *api.TypeRepr
	Span   Span
	Target *api.ResolvedSymbol
}

// Target is a resolved symbol identity selected at a source offset.
type Target struct {
	SymbolID string
	Name     string
	Kind     string
	Span     Span
	DeclSpan Span
	Symbol   *api.ResolvedSymbol
}

// ResolvedRefAt returns the narrowest value/name reference containing offset.
func (db *DB) ResolvedRefAt(path string, offset int) *api.ResolvedRef {
	if db == nil {
		return nil
	}
	best := -1
	bestWidth := 0
	for i := range db.Resolve.Refs {
		rec := &db.Resolve.Refs[i]
		sp, ok := db.originalSpanForRecord(rec.File, rec.Start, rec.End)
		if !ok || !spanContains(sp, path, offset) {
			continue
		}
		width := sp.End - sp.Start
		if best < 0 || width < bestWidth {
			best = i
			bestWidth = width
		}
	}
	if best < 0 {
		return nil
	}
	return &db.Resolve.Refs[best]
}

// ResolvedTypeRefAt returns the narrowest type-name reference containing offset.
func (db *DB) ResolvedTypeRefAt(path string, offset int) *api.ResolvedTypeRef {
	if db == nil {
		return nil
	}
	best := -1
	bestWidth := 0
	for i := range db.Resolve.TypeRefs {
		rec := &db.Resolve.TypeRefs[i]
		sp, ok := db.originalSpanForRecord(rec.File, rec.Start, rec.End)
		if !ok || !spanContains(sp, path, offset) {
			continue
		}
		width := sp.End - sp.Start
		if best < 0 || width < bestWidth {
			best = i
			bestWidth = width
		}
	}
	if best < 0 {
		return nil
	}
	return &db.Resolve.TypeRefs[best]
}

// CheckedBindingAt returns the narrowest checker binding containing offset.
func (db *DB) CheckedBindingAt(path string, offset int) *api.CheckedBinding {
	if db == nil || db.Check == nil {
		return nil
	}
	best := -1
	bestWidth := 0
	for i := range db.Check.Bindings {
		rec := &db.Check.Bindings[i]
		sp, ok := db.originalSpanForRecord("", rec.Start, rec.End)
		if !ok || !spanContains(sp, path, offset) {
			continue
		}
		width := sp.End - sp.Start
		if best < 0 || width < bestWidth {
			best = i
			bestWidth = width
		}
	}
	if best < 0 {
		return nil
	}
	return &db.Check.Bindings[best]
}

// CheckedSymbolAt returns the narrowest checker declaration symbol containing offset.
func (db *DB) CheckedSymbolAt(path string, offset int) *api.CheckedSymbol {
	if db == nil || db.Check == nil {
		return nil
	}
	best := -1
	bestWidth := 0
	for i := range db.Check.Symbols {
		rec := &db.Check.Symbols[i]
		sp, ok := db.originalSpanForRecord("", rec.Start, rec.End)
		if !ok || !spanContains(sp, path, offset) {
			continue
		}
		width := sp.End - sp.Start
		if best < 0 || width < bestWidth {
			best = i
			bestWidth = width
		}
	}
	if best < 0 {
		return nil
	}
	return &db.Check.Symbols[best]
}

// CheckedNodeAt returns the narrowest typed node containing offset.
func (db *DB) CheckedNodeAt(path string, offset int) *api.CheckedNode {
	if db == nil || db.Check == nil {
		return nil
	}
	best := -1
	bestWidth := 0
	for i := range db.Check.TypedNodes {
		rec := &db.Check.TypedNodes[i]
		sp, ok := db.originalSpanForRecord("", rec.Start, rec.End)
		if !ok || !spanContains(sp, path, offset) {
			continue
		}
		width := sp.End - sp.Start
		if best < 0 || width < bestWidth {
			best = i
			bestWidth = width
		}
	}
	if best < 0 {
		return nil
	}
	return &db.Check.TypedNodes[best]
}

// HoverAt returns the best declaration/reference hover payload for path+offset.
func (db *DB) HoverAt(path string, offset int) (Hover, bool) {
	if binding := db.CheckedBindingAt(path, offset); binding != nil {
		sp, _ := db.originalSpanForRecord("", binding.Start, binding.End)
		return Hover{Name: binding.Name, Kind: "binding", Type: binding.Type, Span: sp}, true
	}
	if symbol := db.CheckedSymbolAt(path, offset); symbol != nil {
		sp, _ := db.originalSpanForRecord("", symbol.Start, symbol.End)
		return Hover{Name: symbol.Name, Kind: symbol.Kind, Type: symbol.Type, Span: sp}, true
	}
	if ref := db.ResolvedRefAt(path, offset); ref != nil {
		target := db.ResolveIndex().SymbolsByStableID[ref.TargetSymbolID]
		sp, _ := db.originalSpanForRecord(ref.File, ref.Start, ref.End)
		kind := "binding"
		if target != nil && target.Kind != "" {
			kind = target.Kind
		}
		return Hover{Name: ref.Name, Kind: kind, Type: db.typeForResolvedRef(ref, target), Span: sp, Target: target}, true
	}
	if ref := db.ResolvedTypeRefAt(path, offset); ref != nil {
		target := db.ResolveIndex().SymbolsByStableID[ref.TargetSymbolID]
		sp, _ := db.originalSpanForRecord(ref.File, ref.Start, ref.End)
		kind := "type"
		if target != nil && target.Kind != "" {
			kind = target.Kind
		}
		return Hover{Name: ref.Name, Kind: kind, Type: db.typeForResolvedTypeRef(ref, target), Span: sp, Target: target}, true
	}
	return Hover{}, false
}

// TargetAt returns the resolved target selected at path+offset. It works for
// declaration sites, value/name refs, and type refs.
func (db *DB) TargetAt(path string, offset int) (Target, bool) {
	if symbol := db.resolvedSymbolAt(path, offset); symbol != nil {
		sp, _ := db.originalSpanForRecord(symbol.File, symbol.Start, symbol.End)
		return Target{
			SymbolID: symbol.ID,
			Name:     symbol.Name,
			Kind:     symbol.Kind,
			Span:     sp,
			DeclSpan: sp,
			Symbol:   symbol,
		}, true
	}
	if ref := db.ResolvedRefAt(path, offset); ref != nil {
		target := db.ResolveIndex().SymbolsByStableID[ref.TargetSymbolID]
		sp, _ := db.originalSpanForRecord(ref.File, ref.Start, ref.End)
		decl := db.targetDeclSpan(ref.TargetFile, ref.TargetStart, ref.TargetEnd)
		name, kind := ref.Name, ""
		if target != nil {
			name, kind = target.Name, target.Kind
		}
		return Target{SymbolID: ref.TargetSymbolID, Name: name, Kind: kind, Span: sp, DeclSpan: decl, Symbol: target}, true
	}
	if ref := db.ResolvedTypeRefAt(path, offset); ref != nil {
		target := db.ResolveIndex().SymbolsByStableID[ref.TargetSymbolID]
		sp, _ := db.originalSpanForRecord(ref.File, ref.Start, ref.End)
		decl := db.targetDeclSpan(ref.TargetFile, ref.TargetStart, ref.TargetEnd)
		name, kind := ref.Name, "type"
		if target != nil {
			name, kind = target.Name, target.Kind
		}
		return Target{SymbolID: ref.TargetSymbolID, Name: name, Kind: kind, Span: sp, DeclSpan: decl, Symbol: target}, true
	}
	return Target{}, false
}

// DefinitionAt returns the target declaration span selected at path+offset.
func (db *DB) DefinitionAt(path string, offset int) (Span, bool) {
	target, ok := db.TargetAt(path, offset)
	if !ok || target.DeclSpan.File == "" {
		return Span{}, false
	}
	return target.DeclSpan, true
}

// ReferencesTo returns every ref/type-ref span pointing at target. Declaration
// is included when requested and available.
func (db *DB) ReferencesTo(target Target, includeDecl bool) []Span {
	if db == nil || target.SymbolID == "" {
		return nil
	}
	var out []Span
	for _, ref := range db.ResolveIndex().RefsByTargetSymbolID[target.SymbolID] {
		if sp, ok := db.originalSpanForRecord(ref.File, ref.Start, ref.End); ok {
			out = append(out, sp)
		}
	}
	for _, ref := range db.ResolveIndex().TypeRefsByTargetSymbolID[target.SymbolID] {
		if sp, ok := db.originalSpanForRecord(ref.File, ref.Start, ref.End); ok {
			out = append(out, sp)
		}
	}
	if includeDecl && target.DeclSpan.File != "" {
		out = append(out, target.DeclSpan)
	}
	return out
}

// ReferencesAt resolves path+offset to a target and returns its reference spans.
func (db *DB) ReferencesAt(path string, offset int, includeDecl bool) ([]Span, Target, bool) {
	target, ok := db.TargetAt(path, offset)
	if !ok {
		return nil, Target{}, false
	}
	return db.ReferencesTo(target, includeDecl), target, true
}

func (db *DB) typeForResolvedRef(ref *api.ResolvedRef, target *api.ResolvedSymbol) *api.TypeRepr {
	if db == nil || db.Check == nil || ref == nil {
		return nil
	}
	if binding := db.checkedBindingAtPackageSpan(ref.TargetStart, ref.TargetEnd, ref.Name); binding != nil {
		return binding.Type
	}
	if target != nil {
		if symbol := db.checkedSymbolAtPackageSpan(target.Start, target.End, target.Name); symbol != nil {
			return symbol.Type
		}
	}
	return nil
}

func (db *DB) typeForResolvedTypeRef(ref *api.ResolvedTypeRef, target *api.ResolvedSymbol) *api.TypeRepr {
	if db == nil || db.Check == nil || ref == nil {
		return nil
	}
	if target != nil {
		if symbol := db.checkedSymbolAtPackageSpan(target.Start, target.End, target.Name); symbol != nil {
			return symbol.Type
		}
		if target.Type != nil {
			return target.Type
		}
	}
	return nil
}

func (db *DB) resolvedSymbolAt(path string, offset int) *api.ResolvedSymbol {
	if db == nil {
		return nil
	}
	best := -1
	bestWidth := 0
	for i := range db.Resolve.Symbols {
		rec := &db.Resolve.Symbols[i]
		sp, ok := db.originalSpanForRecord(rec.File, rec.Start, rec.End)
		if !ok || !spanContains(sp, path, offset) {
			continue
		}
		width := sp.End - sp.Start
		if best < 0 || width < bestWidth {
			best = i
			bestWidth = width
		}
	}
	if best < 0 {
		return nil
	}
	return &db.Resolve.Symbols[best]
}

func (db *DB) targetDeclSpan(targetFile string, targetStart, targetEnd int) Span {
	sp, _ := db.originalSpanForRecord(targetFile, targetStart, targetEnd)
	return sp
}

func (db *DB) checkedBindingAtPackageSpan(start, end int, name string) *api.CheckedBinding {
	if db == nil || db.Check == nil {
		return nil
	}
	for i := range db.Check.Bindings {
		rec := &db.Check.Bindings[i]
		if rec.Start == start && rec.End == end && (name == "" || rec.Name == name) {
			return rec
		}
	}
	return nil
}

func (db *DB) checkedSymbolAtPackageSpan(start, end int, name string) *api.CheckedSymbol {
	if db == nil || db.Check == nil {
		return nil
	}
	for i := range db.Check.Symbols {
		rec := &db.Check.Symbols[i]
		if rec.Start == start && rec.End == end && (name == "" || rec.Name == name) {
			return rec
		}
	}
	return nil
}

func (db *DB) originalSpanForRecord(path string, start, end int) (Span, bool) {
	if db == nil {
		return Span{}, false
	}
	if len(db.Files) == 0 {
		return Span{File: path, Start: start, End: end}, true
	}
	file := db.FileForPath(path)
	if file == nil {
		file = db.FileForOffset(start)
	}
	if file == nil {
		return Span{}, false
	}
	startOff, ok := db.OriginalOffset(file.Path, start)
	if !ok {
		return Span{}, false
	}
	endOff, ok := db.OriginalOffset(file.Path, end)
	if !ok {
		endOff = startOff
	}
	if endOff < startOff {
		endOff = startOff
	}
	return Span{File: file.Path, Start: startOff, End: endOff}, true
}

func spanContains(sp Span, path string, offset int) bool {
	if path != "" && sp.File != "" && sp.File != path {
		return false
	}
	if sp.End <= sp.Start {
		return offset == sp.Start
	}
	return offset >= sp.Start && offset < sp.End
}

// ResolveIndex is the resolver half of SemanticDB. Pointer values refer to
// records inside DB.Resolve; treat the DB as immutable while using the index.
type ResolveIndex struct {
	SymbolsByStableID        map[string]*api.ResolvedSymbol
	SymbolsByDeclID          map[string]*api.ResolvedSymbol
	SymbolsByTarget          map[NodeKey]*api.ResolvedSymbol
	RefsByStableID           map[string]*api.ResolvedRef
	RefsByBindingID          map[string]*api.ResolvedRef
	RefsByNode               map[NodeKey][]*api.ResolvedRef
	RefsByTargetSymbolID     map[string][]*api.ResolvedRef
	TypeRefsByStableID       map[string]*api.ResolvedTypeRef
	TypeRefsByNode           map[NodeKey][]*api.ResolvedTypeRef
	TypeRefsByTargetSymbolID map[string][]*api.ResolvedTypeRef
	DiagnosticsByStableID    map[string]*api.ResolveDiagnosticRecord
	DiagnosticsByCode        map[string][]*api.ResolveDiagnosticRecord
}

func emptyResolveIndex() ResolveIndex {
	return ResolveIndex{
		SymbolsByStableID:        map[string]*api.ResolvedSymbol{},
		SymbolsByDeclID:          map[string]*api.ResolvedSymbol{},
		SymbolsByTarget:          map[NodeKey]*api.ResolvedSymbol{},
		RefsByStableID:           map[string]*api.ResolvedRef{},
		RefsByBindingID:          map[string]*api.ResolvedRef{},
		RefsByNode:               map[NodeKey][]*api.ResolvedRef{},
		RefsByTargetSymbolID:     map[string][]*api.ResolvedRef{},
		TypeRefsByStableID:       map[string]*api.ResolvedTypeRef{},
		TypeRefsByNode:           map[NodeKey][]*api.ResolvedTypeRef{},
		TypeRefsByTargetSymbolID: map[string][]*api.ResolvedTypeRef{},
		DiagnosticsByStableID:    map[string]*api.ResolveDiagnosticRecord{},
		DiagnosticsByCode:        map[string][]*api.ResolveDiagnosticRecord{},
	}
}

func buildResolveIndex(result *api.ResolveResult) ResolveIndex {
	if result == nil {
		return emptyResolveIndex()
	}
	idx := ResolveIndex{
		SymbolsByStableID:        make(map[string]*api.ResolvedSymbol, len(result.Symbols)),
		SymbolsByDeclID:          make(map[string]*api.ResolvedSymbol, len(result.Symbols)),
		SymbolsByTarget:          make(map[NodeKey]*api.ResolvedSymbol, len(result.Symbols)),
		RefsByStableID:           make(map[string]*api.ResolvedRef, len(result.Refs)),
		RefsByBindingID:          make(map[string]*api.ResolvedRef, len(result.Refs)),
		RefsByNode:               make(map[NodeKey][]*api.ResolvedRef),
		RefsByTargetSymbolID:     make(map[string][]*api.ResolvedRef),
		TypeRefsByStableID:       make(map[string]*api.ResolvedTypeRef, len(result.TypeRefs)),
		TypeRefsByNode:           make(map[NodeKey][]*api.ResolvedTypeRef),
		TypeRefsByTargetSymbolID: make(map[string][]*api.ResolvedTypeRef),
		DiagnosticsByStableID:    make(map[string]*api.ResolveDiagnosticRecord, len(result.Diagnostics)),
		DiagnosticsByCode:        make(map[string][]*api.ResolveDiagnosticRecord),
	}
	for i := range result.Symbols {
		rec := &result.Symbols[i]
		if rec.ID != "" {
			idx.SymbolsByStableID[rec.ID] = rec
		}
		if rec.DeclID != "" {
			idx.SymbolsByDeclID[rec.DeclID] = rec
		}
		idx.SymbolsByTarget[NodeKey{File: rec.File, Node: rec.Node, Start: rec.Start, End: rec.End}] = rec
	}
	for i := range result.Refs {
		rec := &result.Refs[i]
		if rec.ID != "" {
			idx.RefsByStableID[rec.ID] = rec
		}
		if rec.BindingID != "" {
			idx.RefsByBindingID[rec.BindingID] = rec
		}
		idx.RefsByNode[NodeKey{File: rec.File, Node: rec.Node, Start: rec.Start, End: rec.End}] = append(idx.RefsByNode[NodeKey{File: rec.File, Node: rec.Node, Start: rec.Start, End: rec.End}], rec)
		if rec.TargetSymbolID != "" {
			idx.RefsByTargetSymbolID[rec.TargetSymbolID] = append(idx.RefsByTargetSymbolID[rec.TargetSymbolID], rec)
		}
	}
	for i := range result.TypeRefs {
		rec := &result.TypeRefs[i]
		if rec.ID != "" {
			idx.TypeRefsByStableID[rec.ID] = rec
		}
		idx.TypeRefsByNode[NodeKey{File: rec.File, Node: rec.Node, Start: rec.Start, End: rec.End}] = append(idx.TypeRefsByNode[NodeKey{File: rec.File, Node: rec.Node, Start: rec.Start, End: rec.End}], rec)
		if rec.TargetSymbolID != "" {
			idx.TypeRefsByTargetSymbolID[rec.TargetSymbolID] = append(idx.TypeRefsByTargetSymbolID[rec.TargetSymbolID], rec)
		}
	}
	for i := range result.Diagnostics {
		rec := &result.Diagnostics[i]
		if rec.ID != "" {
			idx.DiagnosticsByStableID[rec.ID] = rec
		}
		if rec.Code != "" {
			idx.DiagnosticsByCode[rec.Code] = append(idx.DiagnosticsByCode[rec.Code], rec)
		}
	}
	return idx
}

func cloneFiles(files []File) []File {
	if len(files) == 0 {
		return nil
	}
	out := make([]File, len(files))
	for i := range files {
		out[i] = files[i]
		out[i].Source = append([]byte(nil), files[i].Source...)
		out[i].OriginalSource = append([]byte(nil), files[i].OriginalSource...)
		out[i].LineStarts = append([]int(nil), files[i].LineStarts...)
	}
	return out
}
