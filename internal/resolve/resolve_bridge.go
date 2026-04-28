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
	for _, pf := range pkg.Files {
		if pf.File == nil || len(pf.Source) == 0 && len(pf.CanonicalSource) == 0 {
			continue
		}
		fi := nativeResolveFileInfoFor(files, pf.Path)
		declIdx := buildDeclIndex(pf.File)
		defineTopLevelSymbols(pkgScope, result.Symbols, fi, declIdx)
	}

	for _, pf := range pkg.Files {
		if pf.File == nil || len(pf.Source) == 0 && len(pf.CanonicalSource) == 0 {
			continue
		}
		fi := nativeResolveFileInfoFor(files, pf.Path)
		identIdx := buildIdentIndex(pf.File)
		typeIdx := buildNamedTypeIndex(pf.File)
		declIdx := buildDeclIndex(pf.File)
		fileScope := NewScope(pkgScope, "file:"+pf.Path)
		nativeDeclareUses(fileScope, pkgScope, pkg, pf, &diags)

		refsByID, refIdents := bridgeRefs(result.Refs, fi, identIdx, declIdx)
		pf.RefsByID = refsByID
		pf.RefIdents = refIdents

		typeRefsByID, typeRefIdents := bridgeTypeRefs(result.TypeRefs, fi, typeIdx, fileScope)
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
		sym := &Symbol{
			Name: name,
			Kind: SymPackage,
			Pos:  u.PosV,
			Decl: u,
			Pub:  u.IsPub,
		}
		if !u.IsFFI() && pkg != nil && pkg.workspace != nil {
			targetPath := UseKey(u)
			target, d := pkg.workspace.ResolveUseTarget(targetPath, u.PosV)
			if d != nil && diags != nil {
				*diags = append(*diags, d)
			}
			if target != nil && !target.isCycleMarker {
				sym.Package = target
			}
		}
		if prev, ok := fileScope.Define(sym); !ok {
			if diags != nil {
				*diags = append(*diags, duplicateSymbolDiag(u.PosV, name, prev, pf.Path))
			}
			continue
		}
		if u.IsPub && pkgScope != nil && pkgScope != fileScope {
			pkgSym := &Symbol{
				Name:    sym.Name,
				Kind:    sym.Kind,
				Pos:     sym.Pos,
				Decl:    sym.Decl,
				Pub:     sym.Pub,
				Package: sym.Package,
			}
			if _, ok := pkgScope.Define(pkgSym); !ok {
				// Re-export collisions remain intentionally silent until the
				// workspace-level E0553 pass lands.
			}
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
	fi nativeResolveFileInfo,
	identIdx map[int]*ast.Ident,
	declIdx map[int]ast.Node,
) (map[ast.NodeID]*Symbol, []*ast.Ident) {
	refsByID := make(map[ast.NodeID]*Symbol, len(refs))
	refIdents := make([]*ast.Ident, 0, len(refs))
	symCache := make(map[int]*Symbol)

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
		ident := identIdx[origOff]
		if ident == nil {
			continue
		}
		sym := findOrCreateSymbol(symCache, ref.Name, ref.TargetStart, ref.TargetEnd, fi, declIdx)
		refsByID[ident.ID] = sym
		refIdents = append(refIdents, ident)
	}
	return refsByID, refIdents
}

func bridgeTypeRefs(
	typeRefs []selfhost.ResolvedTypeRef,
	fi nativeResolveFileInfo,
	typeIdx map[int]*ast.NamedType,
	scope *Scope,
) (map[ast.NodeID]*Symbol, []*ast.NamedType) {
	typeRefsByID := make(map[ast.NodeID]*Symbol, len(typeRefs))
	typeRefIdents := make([]*ast.NamedType, 0, len(typeRefs))

	for _, ref := range typeRefs {
		origOff, ok := nativeToOriginalOffset(fi, ref.Start)
		if !ok {
			continue
		}
		nt := typeIdx[origOff]
		if nt == nil {
			continue
		}
		// Look up the target by name in the scope chain.
		sym := scope.LookupType(ref.Name)
		if sym == nil {
			sym = &Symbol{Name: ref.Name, Kind: SymBuiltin, Pub: true}
		}
		typeRefsByID[nt.ID] = sym
		typeRefIdents = append(typeRefIdents, nt)
	}
	return typeRefsByID, typeRefIdents
}

func findOrCreateSymbol(
	cache map[int]*Symbol,
	name string,
	targetStart, targetEnd int,
	fi nativeResolveFileInfo,
	declIdx map[int]ast.Node,
) *Symbol {
	targetOrigOff, ok := nativeToOriginalOffset(fi, targetStart)
	if !ok {
		return &Symbol{Name: name, Kind: SymBuiltin, Pub: true}
	}
	if sym, ok := cache[targetOrigOff]; ok {
		return sym
	}
	decl := findNearestDecl(declIdx, targetOrigOff)
	sym := &Symbol{
		Name: name,
		Kind: SymUnknown,
		Pub:  true,
		Decl: decl,
	}
	if decl != nil {
		sym.Pos = decl.Pos()
	} else {
		sym.Pos = token.Pos{Offset: targetOrigOff}
	}
	cache[targetOrigOff] = sym
	return sym
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
		origOff, ok := nativeToOriginalOffset(fi, sym.Start)
		if !ok {
			continue
		}
		decl := findNearestDecl(declIdx, origOff)
		goSym := &Symbol{
			Name: sym.Name,
			Kind: nativeKindToSymbolKind(sym.Kind),
			Pub:  sym.Public,
			Decl: decl,
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
