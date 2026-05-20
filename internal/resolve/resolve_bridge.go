package resolve

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/token"
)

// resolvePackageViaNative resolves a package using the self-host (Osty)
// resolver and bridges results back to Go's resolve.Result types.
func resolvePackageViaNative(pkg *Package, prelude *Scope) *PackageResult {
	endNative := beginResolvePhase("resolve.native.artifacts")
	result, files, err := nativeResolveArtifacts(pkg)
	endNative()
	if err != nil {
		return &PackageResult{
			Diags: []*diag.Diagnostic{
				diag.New(diag.Error, fmt.Sprintf("native resolve: %v", err)).Build(),
			},
		}
	}
	semanticDB := pkg.nativeResolve.db

	// Build a `path → nativeResolveFileInfo` map once so the
	// per-iteration lookups in this function (defineTopLevelSymbols,
	// processFile) skip the `nativeResolveFileInfoFor` linear scan over
	// `files`. The slice is still threaded into the bridge functions
	// because the per-ref findOrCreateSymbol path needs target-file
	// lookups too, but the dominant cost there is the reflect-based
	// walks; this map shaves the per-file constant.
	filesByPath := make(map[string]nativeResolveFileInfo, len(files))
	for _, f := range files {
		filesByPath[f.path] = f
	}

	pkgScope := NewScope(prelude, "package:"+pkg.Name)
	diags := nativeParseDiagnostics(pkg)
	endDecl := beginResolvePhase("resolve.native.declIndexes")
	declIndexes := make(map[string]map[int]ast.Node, len(pkg.Files))
	// Pre-bucket result.Symbols by sym.File so per-file
	// defineTopLevelSymbols iterates only its own symbols rather than
	// the whole slice with a `sym.File != fi.path` filter for every
	// file. Same O(N×M) → O(total) shape as the ref / typeRef bucket
	// below.
	symbolsByFile := groupSymbolsByFile(result.Symbols)
	for _, pf := range pkg.Files {
		if pf.File == nil || len(pf.Source) == 0 && len(pf.CanonicalSource) == 0 {
			continue
		}
		fi := filesByPath[pf.Path]
		declIdx := buildDeclIndex(pf.File)
		declIndexes[pf.Path] = declIdx
		defineTopLevelSymbolsForFile(pkgScope, symbolsByFile, fi, declIdx)
	}
	endDecl()

	// Pre-bucket refs / typeRefs by source file once so the per-file
	// bridge loop iterates only its own refs instead of the whole
	// `result.Refs` / `result.TypeRefs` slice with a `ref.File != fi.path`
	// filter. On the install-self toolchain build this drops the bridge
	// hot loop from O(files × all-refs) to O(total-refs), eliminating
	// the ~3.7M cross-file ref iterations the previous filter elided
	// per build (see the historical comment inside bridgeTypeRefs).
	//
	// Refs whose `File` is empty (synthesized / cross-cutting refs) used
	// to be processed in every per-file iteration by the old O(N×M)
	// loop, so we preserve that behaviour by collecting them in a
	// shared slice and concatenating with the per-file bucket on each
	// pass. The slice is typically empty, so the concat is free in
	// practice but keeps correctness identical to the pre-fix path.
	endBuckets := beginResolvePhase("resolve.native.bucketRefs")
	refsByFile, refsAllFiles := groupRefsByFile(result.Refs)
	typeRefsByFile, typeRefsAllFiles := groupTypeRefsByFile(result.TypeRefs)
	symByTarget := nativeSymbolByTarget(result.Symbols)
	endBuckets()

	endBridge := beginResolvePhase("resolve.native.bridgeLoop")
	// Per-file bridge is CPU-bound (3 walkReflect calls per file:
	// buildIdentIndex, buildNamedTypeIndex, supplementUseAliasRefs) and
	// per-file outputs land in disjoint PackageFile fields. Fan out
	// across GOMAXPROCS goroutines with a bounded semaphore — the inputs
	// (refsByFile / typeRefsByFile / symByTarget / declIndexes / files /
	// pkgScope) are read-only after the buckets are built above, and
	// each worker creates its own child FileScope (NewScope only reads
	// the parent pointer). Packages with ≤1 file keep the serial path
	// because the goroutine overhead would dominate.
	bridgeFiles := make([]*PackageFile, 0, len(pkg.Files))
	for _, pf := range pkg.Files {
		if pf.File == nil || len(pf.Source) == 0 && len(pf.CanonicalSource) == 0 {
			continue
		}
		bridgeFiles = append(bridgeFiles, pf)
	}
	processFile := func(pf *PackageFile) {
		fi := filesByPath[pf.Path]
		identIdx, typeIdx := buildIdentAndNamedTypeIndex(pf.File)
		fileScope := NewScope(pkgScope, "file:"+pf.Path)

		refsForFile := refsByFile[pf.Path]
		if len(refsAllFiles) > 0 {
			refsForFile = append(refsForFile, refsAllFiles...)
		}
		typeRefsForFile := typeRefsByFile[pf.Path]
		if len(typeRefsAllFiles) > 0 {
			typeRefsForFile = append(typeRefsForFile, typeRefsAllFiles...)
		}

		// declIndexes is read-only after the declIndexes loop above
		// populated every reachable file. We deliberately drop the
		// previous "rebuild + reinsert into shared map" fallback —
		// the skip predicate matches between the two loops, so a
		// missing slot is unreachable, and a shared-map write here
		// would race with the workers.
		refsByID, refIdents := bridgeRefsForFile(refsForFile, symByTarget, files, fi, identIdx, declIndexes, fileScope)
		refsByID, refIdents = supplementUseAliasRefsFromIndex(pf.File, fileScope, identIdx, refsByID, refIdents)
		pf.RefsByID = refsByID
		pf.RefIdents = refIdents

		typeRefsByID, typeRefIdents := bridgeTypeRefsForFile(typeRefsForFile, symByTarget, files, fi, typeIdx, declIndexes, fileScope)
		pf.TypeRefsByID = typeRefsByID
		pf.TypeRefIdents = typeRefIdents

		pf.FileScope = fileScope
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > len(bridgeFiles) {
		workers = len(bridgeFiles)
	}
	if workers <= 1 || len(bridgeFiles) <= 1 {
		for _, pf := range bridgeFiles {
			processFile(pf)
		}
	} else {
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for _, pf := range bridgeFiles {
			pf := pf
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				processFile(pf)
			}()
		}
		wg.Wait()
	}
	endBridge()
	pkg.PkgScope = pkgScope

	diags = append(diags, nativeResolveDiagnosticsFromArtifacts(result, files)...)
	return &PackageResult{PackageScope: pkgScope, SemanticDB: semanticDB, Diags: diags}
}

// groupRefsByFile partitions every ResolvedRef by its originating file
// path. Refs whose `File` field is empty are returned separately as the
// "shared" slice — the old O(N×M) loop processed empty-file refs for
// every per-file iteration, so callers must concatenate this shared
// slice with the per-file bucket to keep behaviour identical.
func groupRefsByFile(refs []api.ResolvedRef) (map[string][]api.ResolvedRef, []api.ResolvedRef) {
	byFile := make(map[string][]api.ResolvedRef, 16)
	var shared []api.ResolvedRef
	for _, ref := range refs {
		if ref.File == "" {
			shared = append(shared, ref)
			continue
		}
		byFile[ref.File] = append(byFile[ref.File], ref)
	}
	return byFile, shared
}

func groupTypeRefsByFile(refs []api.ResolvedTypeRef) (map[string][]api.ResolvedTypeRef, []api.ResolvedTypeRef) {
	byFile := make(map[string][]api.ResolvedTypeRef, 16)
	var shared []api.ResolvedTypeRef
	for _, ref := range refs {
		if ref.File == "" {
			shared = append(shared, ref)
			continue
		}
		byFile[ref.File] = append(byFile[ref.File], ref)
	}
	return byFile, shared
}

// groupSymbolsByFile partitions selfhost-emitted symbols by their owning
// file path so `defineTopLevelSymbolsForFile` can iterate only the
// per-file slice instead of the whole `result.Symbols` slice (with a
// `sym.File != fi.path` filter) once per file. Symbols whose `File` is
// empty land in the shared bucket so they are visited from every
// file — preserving the old wildcard semantics in
// `defineTopLevelSymbols`.
func groupSymbolsByFile(symbols []api.ResolvedSymbol) map[string][]api.ResolvedSymbol {
	byFile := make(map[string][]api.ResolvedSymbol, 16)
	var shared []api.ResolvedSymbol
	for _, sym := range symbols {
		if sym.File == "" {
			shared = append(shared, sym)
			continue
		}
		byFile[sym.File] = append(byFile[sym.File], sym)
	}
	if len(shared) > 0 {
		byFile[""] = shared
	}
	return byFile
}

func nativeParseDiagnostics(pkg *Package) []*diag.Diagnostic {
	if pkg == nil {
		return nil
	}
	var out []*diag.Diagnostic
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		for _, d := range pf.ParseDiags {
			if d == nil {
				continue
			}
			clone := *d
			if clone.File == "" {
				clone.File = pf.Path
			}
			out = append(out, &clone)
		}
	}
	return out
}

func nativeUseAlias(u *ast.UseDecl) string {
	if u == nil {
		return ""
	}
	if u.Alias != "" {
		return u.Alias
	}
	if u.IsFFI() {
		name := lastSeg(u.FFIPath(), '/')
		return lastSeg(name, '.')
	}
	if u.RawPath != "" && lastSeg(u.RawPath, '/') != u.RawPath {
		if name := lastSeg(u.RawPath, '/'); name != "" {
			return name
		}
	}
	if len(u.Path) == 0 {
		return ""
	}
	return lastSeg(u.Path[len(u.Path)-1], '/')
}

func duplicateSymbolDiag(pos token.Pos, name string, prev *Symbol, file string) *diag.Diagnostic {
	kind := SymUnknown
	if prev != nil {
		kind = prev.Kind
	}
	d := diag.New(diag.Error, fmt.Sprintf("`%s` is already defined as a %s", name, kind)).
		Code(diag.CodeDuplicateDecl).
		PrimaryPos(pos, "duplicate declaration here")
	if prev != nil {
		if prev.Pos.Line > 0 {
			d.Secondary(diag.Span{Start: prev.Pos, End: prev.Pos}, "previous declaration here")
		}
	}
	d.Hint("rename one of the declarations or remove the duplicate")
	out := d.Build()
	if out.File == "" {
		out.File = file
	}
	return out
}

func duplicateUseDiag(pos token.Pos, name string, prev *Symbol, file string) *diag.Diagnostic {
	d := diag.New(diag.Error, fmt.Sprintf("import name `%s` is already in scope", name)).
		Code(diag.CodeUseDuplicateName).
		PrimaryPos(pos, "duplicate import name").
		Hint("remove the duplicate import or rename one side with `as`")
	if prev != nil && prev.Pos.Line > 0 {
		d.Secondary(diag.Span{Start: prev.Pos, End: prev.Pos}, "previous binding here")
	}
	out := d.Build()
	if out.File == "" {
		out.File = file
	}
	return out
}

// --- Offset helpers ---

func nativeResolveFileInfoFor(files []nativeResolveFileInfo, path string) nativeResolveFileInfo {
	for _, f := range files {
		if f.path == path {
			return f
		}
	}
	return nativeResolveFileInfo{}
}

func nativeToOriginalOffset(fi nativeResolveFileInfo, mergedOffset int) (int, bool) {
	rel := mergedOffset - fi.base
	if rel < 0 || rel > len(fi.source) {
		return 0, false
	}
	if fi.sourceMap != nil {
		if remapped, ok := fi.sourceMap.RemapSpan(diag.Span{
			Start: token.Pos{Offset: rel},
			End:   token.Pos{Offset: rel},
		}); ok {
			return remapped.Start.Offset, true
		}
	}
	return rel, true
}

// --- AST index builders ---

func buildIdentIndex(file *ast.File) map[int]*ast.Ident {
	idx := make(map[int]*ast.Ident, 64)
	walkReflect(reflect.ValueOf(file), func(id *ast.Ident) {
		if id.ID != 0 {
			idx[id.PosV.Offset] = id
		}
	}, nil)
	return idx
}

func buildNamedTypeIndex(file *ast.File) map[int]*ast.NamedType {
	idx := make(map[int]*ast.NamedType, 32)
	walkReflect(reflect.ValueOf(file), nil, func(nt *ast.NamedType) {
		if nt.ID != 0 {
			idx[nt.PosV.Offset] = nt
		}
	})
	return idx
}

// buildIdentAndNamedTypeIndex is the combined `buildIdentIndex` +
// `buildNamedTypeIndex` walker. The reflect-based AST traversal is the
// dominant cost in the per-file bridge loop (~19s wall on the
// install-self toolchain build), and the two separate walks duplicate
// every traversal step. Combining them halves the walker overhead while
// keeping the per-index map shape identical.
func buildIdentAndNamedTypeIndex(file *ast.File) (map[int]*ast.Ident, map[int]*ast.NamedType) {
	identIdx := make(map[int]*ast.Ident, 64)
	typeIdx := make(map[int]*ast.NamedType, 32)
	walkReflect(reflect.ValueOf(file), func(id *ast.Ident) {
		if id.ID != 0 {
			identIdx[id.PosV.Offset] = id
		}
	}, func(nt *ast.NamedType) {
		if nt.ID != 0 {
			typeIdx[nt.PosV.Offset] = nt
		}
	})
	return identIdx, typeIdx
}

func buildDeclIndex(file *ast.File) map[int]ast.Node {
	idx := make(map[int]ast.Node, 32)
	for _, u := range file.Uses {
		if u != nil {
			idx[u.Pos().Offset] = u
		}
	}
	for _, d := range file.Decls {
		if n, ok := d.(ast.Node); ok {
			idx[n.Pos().Offset] = n
		}
		walkDeclChildren(d, idx)
	}
	for _, s := range file.Stmts {
		indexStmtBindings(s, idx)
	}
	return idx
}

func indexFnGenerics(fn *ast.FnDecl, idx map[int]ast.Node) {
	if fn == nil {
		return
	}
	for _, gp := range fn.Generics {
		if gp != nil {
			idx[gp.Pos().Offset] = gp
		}
	}
	for _, p := range fn.Params {
		if p != nil {
			idx[p.Pos().Offset] = p
		}
	}
	if fn.Recv != nil {
		idx[fn.Recv.Pos().Offset] = fn.Recv
	}
	if fn.Body != nil {
		indexBlockBindings(fn.Body, idx)
	}
}

// indexBlockBindings walks a block to surface `let` bindings (and any
// nested closures' params/bodies) so the selfhost-bridge can resolve a
// ResolvedRef whose target lands inside a function body. The selfhost
// resolver emits Symbols only for depth-0 decls, so inner bindings are
// otherwise opaque to refineNativeSymbolKind / findNearestDecl.
func indexBlockBindings(b *ast.Block, idx map[int]ast.Node) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		indexStmtBindings(s, idx)
	}
}

func indexStmtBindings(s ast.Stmt, idx map[int]ast.Node) {
	switch s := s.(type) {
	case *ast.LetStmt:
		indexPatternBindings(s.Pattern, idx)
		indexExprBindings(s.Value, idx)
	case *ast.ExprStmt:
		indexExprBindings(s.X, idx)
	case *ast.ForStmt:
		indexPatternBindings(s.Pattern, idx)
		indexExprBindings(s.Iter, idx)
		if s.Body != nil {
			indexBlockBindings(s.Body, idx)
		}
	case *ast.AssignStmt:
		indexExprBindings(s.Value, idx)
	case *ast.ReturnStmt:
		indexExprBindings(s.Value, idx)
	case *ast.DeferStmt:
		indexExprBindings(s.X, idx)
	}
}

func indexPatternBindings(p ast.Pattern, idx map[int]ast.Node) {
	switch p := p.(type) {
	case nil:
		return
	case *ast.IdentPat:
		idx[p.Pos().Offset] = p
	case *ast.BindingPat:
		idx[p.Pos().Offset] = p
		indexPatternBindings(p.Pattern, idx)
	case *ast.TuplePat:
		for _, e := range p.Elems {
			indexPatternBindings(e, idx)
		}
	case *ast.StructPat:
		for _, f := range p.Fields {
			if f.Pattern != nil {
				indexPatternBindings(f.Pattern, idx)
			}
		}
	case *ast.VariantPat:
		for _, a := range p.Args {
			indexPatternBindings(a, idx)
		}
	case *ast.OrPat:
		for _, alt := range p.Alts {
			indexPatternBindings(alt, idx)
		}
	}
}

func indexExprBindings(e ast.Expr, idx map[int]ast.Node) {
	switch e := e.(type) {
	case nil:
		return
	case *ast.ClosureExpr:
		for _, p := range e.Params {
			if p != nil {
				idx[p.Pos().Offset] = p
				indexPatternBindings(p.Pattern, idx)
			}
		}
		indexExprBindings(e.Body, idx)
	case *ast.IfExpr:
		indexPatternBindings(e.Pattern, idx)
		indexExprBindings(e.Cond, idx)
		if e.Then != nil {
			indexBlockBindings(e.Then, idx)
		}
		indexExprBindings(e.Else, idx)
	case *ast.MatchExpr:
		indexExprBindings(e.Scrutinee, idx)
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			indexPatternBindings(arm.Pattern, idx)
			indexExprBindings(arm.Body, idx)
		}
	case *ast.Block:
		indexBlockBindings(e, idx)
	case *ast.CallExpr:
		indexExprBindings(e.Fn, idx)
		for _, a := range e.Args {
			if a != nil {
				indexExprBindings(a.Value, idx)
			}
		}
	case *ast.BinaryExpr:
		indexExprBindings(e.Left, idx)
		indexExprBindings(e.Right, idx)
	case *ast.UnaryExpr:
		indexExprBindings(e.X, idx)
	case *ast.FieldExpr:
		indexExprBindings(e.X, idx)
	case *ast.IndexExpr:
		indexExprBindings(e.X, idx)
		indexExprBindings(e.Index, idx)
	case *ast.TupleExpr:
		for _, el := range e.Elems {
			indexExprBindings(el, idx)
		}
	case *ast.ListExpr:
		for _, el := range e.Elems {
			indexExprBindings(el, idx)
		}
	case *ast.MapExpr:
		for _, en := range e.Entries {
			if en != nil {
				indexExprBindings(en.Key, idx)
				indexExprBindings(en.Value, idx)
			}
		}
	case *ast.StructLit:
		for _, f := range e.Fields {
			if f != nil {
				indexExprBindings(f.Value, idx)
			}
		}
	case *ast.QuestionExpr:
		indexExprBindings(e.X, idx)
	case *ast.TurbofishExpr:
		indexExprBindings(e.Base, idx)
	case *ast.RangeExpr:
		indexExprBindings(e.Start, idx)
		indexExprBindings(e.Stop, idx)
		indexExprBindings(e.Step, idx)
	case *ast.LoopExpr:
		if e.Body != nil {
			indexBlockBindings(e.Body, idx)
		}
	}
}

func walkDeclChildren(d ast.Decl, idx map[int]ast.Node) {
	switch d := d.(type) {
	case *ast.EnumDecl:
		for _, gp := range d.Generics {
			if gp != nil {
				idx[gp.Pos().Offset] = gp
			}
		}
		for _, v := range d.Variants {
			idx[v.Pos().Offset] = v
		}
		for _, m := range d.Methods {
			idx[m.Pos().Offset] = m
			indexFnGenerics(m, idx)
		}
	case *ast.StructDecl:
		for _, gp := range d.Generics {
			if gp != nil {
				idx[gp.Pos().Offset] = gp
			}
		}
		for _, f := range d.Fields {
			idx[f.Pos().Offset] = f
		}
		for _, m := range d.Methods {
			idx[m.Pos().Offset] = m
			indexFnGenerics(m, idx)
		}
	case *ast.InterfaceDecl:
		for _, gp := range d.Generics {
			if gp != nil {
				idx[gp.Pos().Offset] = gp
			}
		}
		for _, m := range d.Methods {
			idx[m.Pos().Offset] = m
			indexFnGenerics(m, idx)
		}
	case *ast.FnDecl:
		indexFnGenerics(d, idx)
	case *ast.TypeAliasDecl:
		for _, gp := range d.Generics {
			if gp != nil {
				idx[gp.Pos().Offset] = gp
			}
		}
	}
}

// --- Reflect-based AST walker ---

type identVisitor func(*ast.Ident)
type typeVisitor func(*ast.NamedType)

func walkReflect(v reflect.Value, onIdent identVisitor, onType typeVisitor) {
	if !v.IsValid() {
		return
	}
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		if onIdent != nil {
			if id, ok := v.Addr().Interface().(*ast.Ident); ok {
				onIdent(id)
				return
			}
		}
		if onType != nil {
			if nt, ok := v.Addr().Interface().(*ast.NamedType); ok {
				onType(nt)
				// Don't return — NamedType may contain type args that are themselves NamedType.
			}
		}
		for i := 0; i < v.NumField(); i++ {
			walkReflect(v.Field(i), onIdent, onType)
		}
	}
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		for i := 0; i < v.Len(); i++ {
			walkReflect(v.Index(i), onIdent, onType)
		}
	}
}

// --- Bridge functions ---

func bridgeRefs(
	refs []api.ResolvedRef,
	symbols []api.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	identIdx map[int]*ast.Ident,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) (map[ast.NodeID]*Symbol, []*ast.Ident) {
	return bridgeRefsForFile(filterRefsForFile(refs, fi.path), nativeSymbolByTarget(symbols), files, fi, identIdx, declIndexes, fileScope)
}

// bridgeRefsForFile is the file-scoped variant of bridgeRefs. Callers
// pre-bucket refs by file and pre-build the symbol-target index so the
// inner loop runs O(refs-for-this-file) instead of O(all-refs). The
// historical cross-file gate inside the loop is gone because the input
// slice is already file-filtered (refs whose `File` is "" land in the
// "" bucket and never reach a per-file bridge call).
func bridgeRefsForFile(
	refs []api.ResolvedRef,
	symByTarget map[nativeSymbolTarget]api.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	identIdx map[int]*ast.Ident,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) (map[ast.NodeID]*Symbol, []*ast.Ident) {
	refsByID := make(map[ast.NodeID]*Symbol, len(refs))
	refIdents := make([]*ast.Ident, 0, len(refs))
	symCache := make(map[nativeSymbolTarget]*Symbol)

	for _, ref := range refs {
		origOff, ok := nativeToOriginalOffset(fi, ref.Start)
		if !ok {
			continue
		}
		origEnd, ok := nativeToOriginalOffset(fi, ref.End)
		if !ok {
			origEnd = origOff
		}
		ident := findIdentForResolvedRef(identIdx, ref.Name, origOff, origEnd)
		if ident == nil {
			continue
		}
		sym := findOrCreateSymbol(symCache, ref, symByTarget, files, fi, declIndexes, fileScope)
		refsByID[ident.ID] = sym
		refIdents = append(refIdents, ident)
	}
	return refsByID, refIdents
}

func filterRefsForFile(refs []api.ResolvedRef, path string) []api.ResolvedRef {
	out := refs[:0:0]
	for _, ref := range refs {
		if ref.File != "" && ref.File != path {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func findIdentForResolvedRef(identIdx map[int]*ast.Ident, name string, start, end int) *ast.Ident {
	if ident := identIdx[start]; ident != nil && ident.Name == name {
		return ident
	}
	if end < start {
		end = start
	}
	var best *ast.Ident
	bestDistance := int(^uint(0) >> 1)
	for _, ident := range identIdx {
		if ident == nil || ident.Name != name {
			continue
		}
		idStart := ident.Pos().Offset
		idEnd := ident.End().Offset
		if idEnd < idStart {
			idEnd = idStart
		}
		distance := 0
		switch {
		case idEnd < start:
			distance = start - idEnd
		case idStart > end:
			distance = idStart - end
		}
		if distance < bestDistance {
			bestDistance = distance
			best = ident
		}
	}
	if bestDistance > 8 {
		return nil
	}
	return best
}

func supplementUseAliasRefs(
	file *ast.File,
	fileScope *Scope,
	refsByID map[ast.NodeID]*Symbol,
	refIdents []*ast.Ident,
) (map[ast.NodeID]*Symbol, []*ast.Ident) {
	if file == nil || fileScope == nil {
		return refsByID, refIdents
	}
	aliases := map[string]*Symbol{}
	for _, u := range file.Uses {
		name := nativeUseAlias(u)
		if name == "" {
			continue
		}
		if sym := fileScope.LookupLocal(name); sym != nil {
			aliases[name] = sym
		}
	}
	if len(aliases) == 0 {
		return refsByID, refIdents
	}
	if refsByID == nil {
		refsByID = map[ast.NodeID]*Symbol{}
	}
	walkReflect(reflect.ValueOf(file), func(id *ast.Ident) {
		if id == nil || id.ID == 0 {
			return
		}
		if _, exists := refsByID[id.ID]; exists {
			return
		}
		sym := aliases[id.Name]
		if sym == nil {
			return
		}
		refsByID[id.ID] = sym
		refIdents = append(refIdents, id)
	}, nil)
	return refsByID, refIdents
}

// supplementUseAliasRefsFromIndex is the identIdx-driven variant of
// `supplementUseAliasRefs`. The caller has already walked the file once
// to build `identIdx`, so we reuse that map instead of paying for a
// third reflect-walk per file. Behaviour matches the walk-based path:
// every non-zero-ID ident in the file is considered, and unresolved
// idents that match a `use`-decl alias are bridged to the alias's
// fileScope symbol.
func supplementUseAliasRefsFromIndex(
	file *ast.File,
	fileScope *Scope,
	identIdx map[int]*ast.Ident,
	refsByID map[ast.NodeID]*Symbol,
	refIdents []*ast.Ident,
) (map[ast.NodeID]*Symbol, []*ast.Ident) {
	if file == nil || fileScope == nil {
		return refsByID, refIdents
	}
	aliases := map[string]*Symbol{}
	for _, u := range file.Uses {
		name := nativeUseAlias(u)
		if name == "" {
			continue
		}
		if sym := fileScope.LookupLocal(name); sym != nil {
			aliases[name] = sym
		}
	}
	if len(aliases) == 0 {
		return refsByID, refIdents
	}
	if refsByID == nil {
		refsByID = map[ast.NodeID]*Symbol{}
	}
	for _, id := range identIdx {
		if id == nil || id.ID == 0 {
			continue
		}
		if _, exists := refsByID[id.ID]; exists {
			continue
		}
		sym := aliases[id.Name]
		if sym == nil {
			continue
		}
		refsByID[id.ID] = sym
		refIdents = append(refIdents, id)
	}
	return refsByID, refIdents
}

func bridgeTypeRefs(
	typeRefs []api.ResolvedTypeRef,
	symbols []api.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	typeIdx map[int]*ast.NamedType,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) (map[ast.NodeID]*Symbol, []*ast.NamedType) {
	return bridgeTypeRefsForFile(filterTypeRefsForFile(typeRefs, fi.path), nativeSymbolByTarget(symbols), files, fi, typeIdx, declIndexes, fileScope)
}

// bridgeTypeRefsForFile is the file-scoped variant of bridgeTypeRefs.
// See bridgeRefsForFile for the same pre-bucket rationale — the
// historical comment about 3.7M cross-file ref elisions per
// install-self build motivated the partition; downstream
// `resolverSymbolMatchesSourceName` (PR #1919) still catches the
// residual same-file mismaps that the elision used to cover.
func bridgeTypeRefsForFile(
	typeRefs []api.ResolvedTypeRef,
	symByTarget map[nativeSymbolTarget]api.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	typeIdx map[int]*ast.NamedType,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) (map[ast.NodeID]*Symbol, []*ast.NamedType) {
	typeRefsByID := make(map[ast.NodeID]*Symbol, len(typeRefs))
	typeRefIdents := make([]*ast.NamedType, 0, len(typeRefs))
	symCache := make(map[nativeSymbolTarget]*Symbol)

	for _, ref := range typeRefs {
		origOff, ok := nativeToOriginalOffset(fi, ref.Start)
		if !ok {
			continue
		}
		nt := typeIdx[origOff]
		if nt == nil {
			continue
		}
		sym := findOrCreateTypeSymbol(symCache, ref, symByTarget, files, fi, declIndexes, fileScope)
		typeRefsByID[nt.ID] = sym
		typeRefIdents = append(typeRefIdents, nt)
	}
	return typeRefsByID, typeRefIdents
}

func filterTypeRefsForFile(refs []api.ResolvedTypeRef, path string) []api.ResolvedTypeRef {
	out := refs[:0:0]
	for _, ref := range refs {
		if ref.File != "" && ref.File != path {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func findOrCreateTypeSymbol(
	cache map[nativeSymbolTarget]*Symbol,
	ref api.ResolvedTypeRef,
	symByTarget map[nativeSymbolTarget]api.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) *Symbol {
	key := nativeSymbolTarget{file: ref.TargetFile, node: ref.TargetNode, start: ref.TargetStart, end: ref.TargetEnd}
	if sym, ok := cache[key]; ok {
		return sym
	}
	if ref.TargetNode < 0 {
		return &Symbol{StableID: ref.TargetSymbolID, PackageID: ref.PackageID, Name: ref.Name, Kind: SymBuiltin, Pub: true}
	}
	nativeSym := symByTarget[key]
	targetFI := nativeResolveFileInfoFor(files, ref.TargetFile)
	if targetFI.path == "" && ref.TargetFile == "" {
		targetFI = fi
	}
	targetOrigOff, ok := nativeToOriginalOffset(targetFI, ref.TargetStart)
	if !ok {
		return &Symbol{StableID: ref.TargetSymbolID, PackageID: ref.PackageID, Name: ref.Name, Kind: SymBuiltin, Pub: true}
	}
	declIdx := declIndexes[ref.TargetFile]
	decl := findNearestDecl(declIdx, targetOrigOff)
	if _, isUse := decl.(*ast.UseDecl); isUse && fileScope != nil && ref.TargetFile == fi.path {
		if sym := fileScope.LookupLocal(ref.Name); sym != nil {
			fillSymbolIDsFromTypeRef(sym, ref)
			cache[key] = sym
			return sym
		}
	}
	name := ref.Name
	if nativeSym.Name != "" {
		name = nativeSym.Name
	}
	// The selfhost resolver targets generic-parameter type refs at the
	// enclosing decl's start offset (a Map<K,V>.method's `K` param ref
	// resolves to the FnDecl, not to the GenericParam node). Recover the
	// generic by name when the enclosing decl declares one.
	kind := refineNativeSymbolKind(nativeKindToSymbolKind(nativeSym.Kind), decl)
	if kind != SymGeneric && declHasGenericParam(decl, ref.Name) {
		kind = SymGeneric
	}
	sym := &Symbol{
		StableID:  ref.TargetSymbolID,
		PackageID: ref.PackageID,
		DeclID:    nativeSym.DeclID,
		Name:      name,
		Kind:      kind,
		Pub:       true,
		Decl:      decl,
	}
	if sym.Kind == SymUnknown && nativeSym.Kind == "" {
		sym.Kind = SymUnknown
	}
	if decl != nil {
		sym.Pos = decl.Pos()
	} else {
		sym.Pos = token.Pos{Offset: targetOrigOff}
	}
	cache[key] = sym
	return sym
}

func findOrCreateSymbol(
	cache map[nativeSymbolTarget]*Symbol,
	ref api.ResolvedRef,
	symByTarget map[nativeSymbolTarget]api.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) *Symbol {
	key := nativeSymbolTarget{file: ref.TargetFile, node: ref.TargetNode, start: ref.TargetStart, end: ref.TargetEnd}
	if sym, ok := cache[key]; ok {
		return sym
	}
	nativeSym := symByTarget[key]
	targetFI := nativeResolveFileInfoFor(files, ref.TargetFile)
	if targetFI.path == "" && ref.TargetFile == "" {
		targetFI = fi
	}
	targetOrigOff, ok := nativeToOriginalOffset(targetFI, ref.TargetStart)
	if !ok {
		return &Symbol{StableID: ref.TargetSymbolID, PackageID: ref.PackageID, Name: ref.Name, Kind: SymBuiltin, Pub: true}
	}
	declIdx := declIndexes[ref.TargetFile]
	decl := findNearestDecl(declIdx, targetOrigOff)
	if _, isUse := decl.(*ast.UseDecl); isUse && fileScope != nil && ref.TargetFile == fi.path {
		if sym := fileScope.LookupLocal(ref.Name); sym != nil {
			fillSymbolIDsFromRef(sym, ref)
			cache[key] = sym
			return sym
		}
	}
	sym := &Symbol{
		StableID:  ref.TargetSymbolID,
		PackageID: ref.PackageID,
		DeclID:    nativeSym.DeclID,
		Name:      ref.Name,
		Kind:      refineNativeSymbolKind(nativeKindToSymbolKind(nativeSym.Kind), decl),
		Pub:       true,
		Decl:      decl,
	}
	if sym.Kind == SymUnknown && nativeSym.Kind == "" {
		sym.Kind = SymUnknown
	}
	if decl != nil {
		sym.Pos = decl.Pos()
	} else {
		sym.Pos = token.Pos{Offset: targetOrigOff}
	}
	cache[key] = sym
	return sym
}

func fillSymbolIDsFromRef(sym *Symbol, ref api.ResolvedRef) {
	if sym == nil {
		return
	}
	if sym.StableID == "" {
		sym.StableID = ref.TargetSymbolID
	}
	if sym.PackageID == "" {
		sym.PackageID = ref.PackageID
	}
}

func fillSymbolIDsFromTypeRef(sym *Symbol, ref api.ResolvedTypeRef) {
	if sym == nil {
		return
	}
	if sym.StableID == "" {
		sym.StableID = ref.TargetSymbolID
	}
	if sym.PackageID == "" {
		sym.PackageID = ref.PackageID
	}
}

type nativeSymbolTarget struct {
	file       string
	node       int
	start, end int
}

func nativeSymbolByTarget(symbols []api.ResolvedSymbol) map[nativeSymbolTarget]api.ResolvedSymbol {
	out := make(map[nativeSymbolTarget]api.ResolvedSymbol, len(symbols))
	for _, sym := range symbols {
		out[nativeSymbolTarget{file: sym.File, node: sym.Node, start: sym.Start, end: sym.End}] = sym
	}
	return out
}

func findNearestDecl(declIdx map[int]ast.Node, targetOff int) ast.Node {
	bestOff := -1
	for off := range declIdx {
		if off <= targetOff && off > bestOff {
			bestOff = off
		}
	}
	if bestOff >= 0 {
		return declIdx[bestOff]
	}
	return nil
}

// --- Symbol construction from native symbols ---

// defineTopLevelSymbolsForFile is the pre-bucketed counterpart of
// `defineTopLevelSymbols`. The caller has already partitioned the
// selfhost-emitted Symbols slice by sym.File via `groupSymbolsByFile`,
// so the per-file loop iterates only its own bucket plus the shared
// (`File==""`) bucket — eliminating the `sym.File != fi.path` rescan
// every previous file paid for the whole slice.
func defineTopLevelSymbolsForFile(
	scope *Scope,
	symbolsByFile map[string][]api.ResolvedSymbol,
	fi nativeResolveFileInfo,
	declIdx map[int]ast.Node,
) {
	defineTopLevelSymbols(scope, symbolsByFile[fi.path], fi, declIdx)
	if shared, ok := symbolsByFile[""]; ok && fi.path != "" {
		defineTopLevelSymbols(scope, shared, fi, declIdx)
	}
}

func defineTopLevelSymbols(
	scope *Scope,
	symbols []api.ResolvedSymbol,
	fi nativeResolveFileInfo,
	declIdx map[int]ast.Node,
) {
	for _, sym := range symbols {
		if sym.Depth != 0 {
			continue
		}
		if sym.File != "" && sym.File != fi.path {
			continue
		}
		if sym.Kind == "package" {
			continue
		}
		origOff, ok := nativeToOriginalOffset(fi, sym.Start)
		if !ok {
			continue
		}
		decl := findNearestDecl(declIdx, origOff)
		// The frozen self-host parser drops the `pub` flag on top-level `let`
		// declarations (it never threads `isPub` from `opParseDecl` into
		// `opParseLetStmt`, so the symbol always emits Public=false). Recover
		// it from the AST when the original LetDecl is reachable.
		pub := sym.Public
		if !pub {
			if let, ok := decl.(*ast.LetDecl); ok {
				pub = let.Pub
			}
		}
		kind := refineNativeSymbolKind(nativeKindToSymbolKind(sym.Kind), decl)
		goSym := &Symbol{
			StableID:  sym.ID,
			PackageID: sym.PackageID,
			DeclID:    sym.DeclID,
			Name:      sym.Name,
			Kind:      kind,
			Pub:       pub,
			Decl:      decl,
		}
		if decl != nil {
			goSym.Pos = decl.Pos()
		} else {
			goSym.Pos = token.Pos{Offset: origOff}
		}
		scope.DefineForce(goSym)
	}
}

// refineNativeSymbolKind upgrades the coarse kind that the selfhost
// resolver emits ("type" for every nominal — struct, enum, interface,
// type-alias) into the specific Go-side SymbolKind. Downstream consumers
// like ir.lowerCall key off SymEnum (variant constructor recognition),
// so leaving an enum mis-classified as SymStruct silently regresses
// every `EnumName.Variant(args)` call to a generic MethodCall.
// declHasGenericParam reports whether decl is a top-level decl that
// declares a generic parameter named name. The selfhost resolver emits a
// TypeRef whose target offset is the enclosing decl's start (not the
// generic param's offset), so when the ref's name matches one of the
// decl's declared generics we know it's pointing at that param.
func declHasGenericParam(decl ast.Node, name string) bool {
	if name == "" {
		return false
	}
	var generics []*ast.GenericParam
	switch d := decl.(type) {
	case *ast.FnDecl:
		generics = d.Generics
	case *ast.StructDecl:
		generics = d.Generics
	case *ast.EnumDecl:
		generics = d.Generics
	case *ast.InterfaceDecl:
		generics = d.Generics
	case *ast.TypeAliasDecl:
		generics = d.Generics
	default:
		return false
	}
	for _, gp := range generics {
		if gp != nil && gp.Name == name {
			return true
		}
	}
	return false
}

func refineNativeSymbolKind(kind SymbolKind, decl ast.Node) SymbolKind {
	// A type-ref pointing at a generic parameter has no entry in the
	// selfhost-emitted Symbols slice (only top-level decls and variants
	// land there), so kind comes back as SymUnknown. Recover SymGeneric
	// from the AST so downstream lowerers (ir.lowerType) build a TypeVar
	// instead of an opaque NamedType — without this every generic
	// payload (e.g. `enum Maybe<T> { Some(T) }`) survives monomorph as
	// `T` and the LLVM backend rejects it.
	switch decl.(type) {
	case *ast.GenericParam:
		return SymGeneric
	case *ast.Param:
		return SymParam
	case *ast.IdentPat, *ast.BindingPat:
		return SymLet
	}
	if kind != SymStruct {
		return kind
	}
	switch decl.(type) {
	case *ast.EnumDecl:
		return SymEnum
	case *ast.InterfaceDecl:
		return SymInterface
	case *ast.TypeAliasDecl:
		return SymTypeAlias
	}
	return kind
}

func nativeKindToSymbolKind(kind string) SymbolKind {
	switch kind {
	case "fn":
		return SymFn
	case "type":
		return SymStruct
	case "variant":
		return SymVariant
	case "value":
		return SymLet
	case "generic":
		return SymGeneric
	case "package":
		return SymPackage
	default:
		return SymUnknown
	}
}
