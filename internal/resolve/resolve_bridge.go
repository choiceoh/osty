package resolve

import (
	"fmt"
	"reflect"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// resolvePackageViaNative resolves a package using the self-host (Osty)
// resolver and bridges results back to Go's resolve.Result types.
func resolvePackageViaNative(pkg *Package, prelude *Scope) *PackageResult {
	result, files, err := nativeResolveArtifacts(pkg)
	if err != nil {
		return &PackageResult{
			Diags: []*diag.Diagnostic{
				diag.New(diag.Error, fmt.Sprintf("native resolve: %v", err)).Build(),
			},
		}
	}

	pkgScope := NewScope(prelude, "package:"+pkg.Name)
	diags := nativeParseDiagnostics(pkg)
	declIndexes := make(map[string]map[int]ast.Node, len(pkg.Files))
	for _, pf := range pkg.Files {
		if pf.File == nil || len(pf.Source) == 0 && len(pf.CanonicalSource) == 0 {
			continue
		}
		fi := nativeResolveFileInfoFor(files, pf.Path)
		declIdx := buildDeclIndex(pf.File)
		declIndexes[pf.Path] = declIdx
		defineTopLevelSymbols(pkgScope, result.Symbols, fi, declIdx)
	}

	for _, pf := range pkg.Files {
		if pf.File == nil || len(pf.Source) == 0 && len(pf.CanonicalSource) == 0 {
			continue
		}
		fi := nativeResolveFileInfoFor(files, pf.Path)
		identIdx := buildIdentIndex(pf.File)
		typeIdx := buildNamedTypeIndex(pf.File)
		declIdx := declIndexes[pf.Path]
		if declIdx == nil {
			declIdx = buildDeclIndex(pf.File)
			declIndexes[pf.Path] = declIdx
		}
		fileScope := NewScope(pkgScope, "file:"+pf.Path)
		nativeDeclareUses(fileScope, pkgScope, pkg, pf, &diags)

		refsByID, refIdents := bridgeRefs(result.Refs, result.Symbols, files, fi, identIdx, declIndexes, fileScope)
		refsByID, refIdents = supplementUseAliasRefs(pf.File, fileScope, refsByID, refIdents)
		pf.RefsByID = refsByID
		pf.RefIdents = refIdents

		typeRefsByID, typeRefIdents := bridgeTypeRefs(result.TypeRefs, result.Symbols, files, fi, typeIdx, declIndexes, fileScope)
		pf.TypeRefsByID = typeRefsByID
		pf.TypeRefIdents = typeRefIdents

		pf.FileScope = fileScope
	}
	pkg.PkgScope = pkgScope

	diags = append(diags, nativeResolveDiagnosticsFromArtifacts(result, files)...)
	return &PackageResult{PackageScope: pkgScope, Diags: diags}
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

func nativeDeclareUses(fileScope, pkgScope *Scope, pkg *Package, pf *PackageFile, diags *[]*diag.Diagnostic) {
	if pf == nil || pf.File == nil || fileScope == nil {
		return
	}
	for _, u := range pf.File.Uses {
		name := nativeUseAlias(u)
		if name == "" {
			continue
		}
		if !u.IsFFI() && pkg != nil && pkg.workspace != nil {
			sym, d := resolveUseBinding(pkg.workspace, u, name, pf.Path)
			if d != nil && diags != nil {
				*diags = append(*diags, d)
			}
			if _, ok := fileScope.Define(sym); !ok {
				continue
			}
			if u.IsPub && sym.Pub && pkgScope != nil && pkgScope != fileScope {
				pkgSym := &Symbol{
					StableID:  sym.StableID,
					PackageID: sym.PackageID,
					DeclID:    sym.DeclID,
					Name:      sym.Name,
					Kind:      sym.Kind,
					Pos:       sym.Pos,
					Decl:      sym.Decl,
					Pub:       sym.Pub,
					Package:   sym.Package,
				}
				pkgScope.Define(pkgSym)
			}
			continue
		}
		sym := placeholderUseSymbol(u, name, SymPackage, u.IsPub)
		if _, ok := fileScope.Define(sym); !ok {
			continue
		}
		if u.IsPub && sym.Pub && pkgScope != nil && pkgScope != fileScope {
			pkgSym := &Symbol{
				StableID:  sym.StableID,
				PackageID: sym.PackageID,
				DeclID:    sym.DeclID,
				Name:      sym.Name,
				Kind:      sym.Kind,
				Pos:       sym.Pos,
				Decl:      sym.Decl,
				Pub:       sym.Pub,
				Package:   sym.Package,
			}
			pkgScope.Define(pkgSym)
		}
	}
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
	return idx
}

func walkDeclChildren(d ast.Decl, idx map[int]ast.Node) {
	switch d := d.(type) {
	case *ast.EnumDecl:
		for _, v := range d.Variants {
			idx[v.Pos().Offset] = v
		}
		for _, m := range d.Methods {
			idx[m.Pos().Offset] = m
		}
	case *ast.StructDecl:
		for _, f := range d.Fields {
			idx[f.Pos().Offset] = f
		}
		for _, m := range d.Methods {
			idx[m.Pos().Offset] = m
		}
	case *ast.InterfaceDecl:
		for _, m := range d.Methods {
			idx[m.Pos().Offset] = m
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
	if v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
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
	refs []selfhost.ResolvedRef,
	symbols []selfhost.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	identIdx map[int]*ast.Ident,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) (map[ast.NodeID]*Symbol, []*ast.Ident) {
	refsByID := make(map[ast.NodeID]*Symbol, len(refs))
	refIdents := make([]*ast.Ident, 0, len(refs))
	symCache := make(map[nativeSymbolTarget]*Symbol)
	symByTarget := nativeSymbolByTarget(symbols)

	for _, ref := range refs {
		if ref.File != "" {
			// Only process refs for this file. The file info already
			// constrains which merged-source offsets are valid, but
			// the File field gives a clearer filter.
			_ = ref.File
		}
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

func bridgeTypeRefs(
	typeRefs []selfhost.ResolvedTypeRef,
	symbols []selfhost.ResolvedSymbol,
	files []nativeResolveFileInfo,
	fi nativeResolveFileInfo,
	typeIdx map[int]*ast.NamedType,
	declIndexes map[string]map[int]ast.Node,
	fileScope *Scope,
) (map[ast.NodeID]*Symbol, []*ast.NamedType) {
	typeRefsByID := make(map[ast.NodeID]*Symbol, len(typeRefs))
	typeRefIdents := make([]*ast.NamedType, 0, len(typeRefs))
	symCache := make(map[nativeSymbolTarget]*Symbol)
	symByTarget := nativeSymbolByTarget(symbols)

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

func findOrCreateTypeSymbol(
	cache map[nativeSymbolTarget]*Symbol,
	ref selfhost.ResolvedTypeRef,
	symByTarget map[nativeSymbolTarget]selfhost.ResolvedSymbol,
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
	sym := &Symbol{
		StableID:  ref.TargetSymbolID,
		PackageID: ref.PackageID,
		DeclID:    nativeSym.DeclID,
		Name:      name,
		Kind:      nativeKindToSymbolKind(nativeSym.Kind),
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
	ref selfhost.ResolvedRef,
	symByTarget map[nativeSymbolTarget]selfhost.ResolvedSymbol,
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
		Kind:      nativeKindToSymbolKind(nativeSym.Kind),
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

func fillSymbolIDsFromRef(sym *Symbol, ref selfhost.ResolvedRef) {
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

func fillSymbolIDsFromTypeRef(sym *Symbol, ref selfhost.ResolvedTypeRef) {
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

func nativeSymbolByTarget(symbols []selfhost.ResolvedSymbol) map[nativeSymbolTarget]selfhost.ResolvedSymbol {
	out := make(map[nativeSymbolTarget]selfhost.ResolvedSymbol, len(symbols))
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

func defineTopLevelSymbols(
	scope *Scope,
	symbols []selfhost.ResolvedSymbol,
	fi nativeResolveFileInfo,
	declIdx map[int]ast.Node,
) {
	for _, sym := range symbols {
		if sym.Depth != 0 {
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
		goSym := &Symbol{
			StableID:  sym.ID,
			PackageID: sym.PackageID,
			DeclID:    sym.DeclID,
			Name:      sym.Name,
			Kind:      nativeKindToSymbolKind(sym.Kind),
			Pub:       sym.Public,
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
