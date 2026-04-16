//go:build selfhostgen

package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// collect is pass 1: walk every top-level declaration and record enough
// type-level information about it for pass 2 (body checking) to call,
// dereference, and destructure references to it.
//
// Collection is deliberately side-effect-free on the AST. Everything
// lives in:
//
//   - c.result.Descs — struct / enum / interface / alias descriptors,
//     keyed by the declaration's Symbol.
//   - c.result.SymTypes / c.syms — the Symbol-level type (a *Named for
//     nominal types, a *FnType for top-level functions, etc.).
func (c *checker) collect(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		c.collectFn(n, nil)
	case *ast.StructDecl:
		c.collectStruct(n)
	case *ast.EnumDecl:
		c.collectEnum(n)
	case *ast.InterfaceDecl:
		c.collectInterface(n)
	case *ast.TypeAliasDecl:
		c.collectAlias(n)
	case *ast.LetDecl:
		c.collectLet(n)
	case *ast.UseDecl:
		// FFI `use go "path" { fn Foo(...); struct Bar { … } }` bodies
		// carry inline declarations the checker should type-check too,
		// even though the exported-member lookup (`pkg.Foo`) stays
		// opaque in this MVP. Collecting now keeps per-fn signatures
		// valid — anything mistyped in the FFI surface fires E0300 etc.
		for _, gd := range n.GoBody {
			c.collect(gd)
		}
	}
}

// collectGenerics builds TypeVars for a decl's generic params and
// attaches each to the resolver-created SymGeneric entry.
func (c *checker) collectGenerics(gs []*ast.GenericParam) []*types.TypeVar {
	if len(gs) == 0 {
		return nil
	}
	tvs := make([]*types.TypeVar, len(gs))
	for i, g := range gs {
		// The resolver installed one Symbol per generic param in the
		// decl's scope. Unfortunately we don't have direct access to
		// those scopes here — but the resolver stored each as a
		// SymGeneric with g as its Decl. Rather than walking the scope
		// graph, we use the AST node itself (*ast.GenericParam) as the
		// identity substitute: we synthesize a tiny Symbol whose Decl
		// matches. The resolver uses pointer identity for scope lookup
		// so calls in the body will hit the same TypeVar via the Refs
		// table (sym.Decl == g).
		//
		// In practice, pass 2 resolves a `T` reference by:
		//   1) looking up the Symbol in refs[ident]
		//   2) consulting c.syms[sym]
		// so we just need to register against that same Symbol. We walk
		// the resolver file scope to find it.
		sym := c.findGenericSymbol(g)
		tv := &types.TypeVar{Sym: sym}
		tvs[i] = tv
		if sym != nil {
			c.setSymType(sym, tv)
		}
	}
	// Constraints are resolved once generics are registered, so bounds
	// that reference sibling params (`T: Container<U>`) parse correctly.
	for i, g := range gs {
		for _, con := range g.Constraints {
			if t := c.typeOf(con); t != nil {
				tvs[i].Bounds = append(tvs[i].Bounds, t)
			}
		}
	}
	return tvs
}

// findGenericSymbol returns the resolver Symbol for a GenericParam,
// falling back to a synthesized placeholder when the generic is never
// referenced (unused type parameters are legal and shouldn't crash the
// collector).
func (c *checker) findGenericSymbol(g *ast.GenericParam) *resolve.Symbol {
	if sym := c.symByDecl(g); sym != nil {
		return sym
	}
	synth := &resolve.Symbol{Name: g.Name, Kind: resolve.SymGeneric, Pos: g.PosV, Decl: g}
	c.declToSym[g] = synth
	return synth
}

// collectLet gathers a top-level `let NAME = value`.
func (c *checker) collectLet(n *ast.LetDecl) {
	sym := c.topLevelSym(n.Name)
	if sym == nil {
		return
	}
	var t types.Type
	if n.Type != nil {
		t = c.typeOf(n.Type)
	}
	// Value type is resolved in pass 2 because let decls can reference
	// top-level declarations that haven't been collected yet. For now
	// we record the annotation; if none, we mark the symbol with
	// ErrorType and infer it in pass 2.
	if t == nil {
		t = types.ErrorType
	}
	c.setSymType(sym, t)
	if info := c.info(sym); info != nil {
		info.Mut = n.Mut
	}
}

// collectStruct records the field and method shapes of a struct. When
// the same struct name has already been collected from another file of
// the package (partial declarations per v0.2 R19), this pass ADDS the
// new file's contributions to the existing descriptor rather than
// replacing it.
func (c *checker) collectStruct(n *ast.StructDecl) {
	sym := c.topLevelSym(n.Name)
	if sym == nil {
		return
	}
	desc, existing := c.result.Descs[sym]
	if !existing {
		desc = &typeDesc{
			Sym:     sym,
			Kind:    resolve.SymStruct,
			Methods: map[string]*methodDesc{},
		}
		c.result.Descs[sym] = desc
		desc.Generics = c.collectGenerics(n.Generics)
		selfT := &types.Named{Sym: sym, Args: argsOfGenerics(desc.Generics)}
		c.setSymType(sym, selfT)
	}

	// Fields (§3.4 R19: at most one partial contributes fields; the
	// resolver flagged any violation, so we append whatever this
	// declaration carries and later passes see the union).
	for _, f := range n.Fields {
		desc.Fields = append(desc.Fields, &fieldDesc{
			Name:   f.Name,
			Type:   c.typeOf(f.Type),
			Pub:    f.Pub,
			HasDef: f.Default != nil,
			Decl:   f,
		})
	}

	// Methods spread across partial declarations (R19). collectFn
	// installs each method into desc.Methods; the resolver already
	// ensured method names are unique across files.
	for _, m := range n.Methods {
		c.collectFn(m, desc)
	}
}

// collectEnum records the variants and methods of an enum. Like
// collectStruct, partial declarations across files are merged into one
// typeDesc so variants from any file and methods from any file are
// visible to the body pass.
func (c *checker) collectEnum(n *ast.EnumDecl) {
	sym := c.topLevelSym(n.Name)
	if sym == nil {
		return
	}
	desc, existing := c.result.Descs[sym]
	if !existing {
		desc = &typeDesc{
			Sym:          sym,
			Kind:         resolve.SymEnum,
			Methods:      map[string]*methodDesc{},
			Variants:     map[string]*variantDesc{},
			VariantOrder: make([]string, 0, len(n.Variants)),
		}
		c.result.Descs[sym] = desc
		desc.Generics = c.collectGenerics(n.Generics)
	}
	// selfT is recomputed on every partial so variant-constructor
	// setSymType below has access to it regardless of which partial
	// first reached this enum.
	selfT := &types.Named{Sym: sym, Args: argsOfGenerics(desc.Generics)}
	if !existing {
		c.setSymType(sym, selfT)
	}

	// Variants. Bare variant values take the enum's type; tuple-like
	// variants expose a constructor fn. Stash the resolver Symbol on
	// variantDesc so pattern dispatch can find it in O(1).
	for _, v := range n.Variants {
		vd := &variantDesc{Name: v.Name, Decl: v}
		for _, f := range v.Fields {
			vd.Fields = append(vd.Fields, c.typeOf(f))
		}
		desc.Variants[v.Name] = vd
		desc.VariantOrder = append(desc.VariantOrder, v.Name)

		vsym := c.topLevelSym(v.Name)
		if vsym == nil {
			continue
		}
		vd.Sym = vsym
		info := c.info(vsym)
		info.Enum = desc
		info.VariantFields = vd.Fields
		if len(vd.Fields) == 0 {
			c.setSymType(vsym, selfT)
		} else {
			c.setSymType(vsym, &types.FnType{Params: vd.Fields, Return: selfT})
		}
	}

	// Methods.
	for _, m := range n.Methods {
		c.collectFn(m, desc)
	}
}

// collectInterface records the declared method signatures of an interface.
func (c *checker) collectInterface(n *ast.InterfaceDecl) {
	sym := c.topLevelSym(n.Name)
	if sym == nil {
		return
	}
	desc := &typeDesc{
		Sym:              sym,
		Kind:             resolve.SymInterface,
		Methods:          map[string]*methodDesc{},
		InterfaceMethods: map[string]*methodDesc{},
	}
	c.result.Descs[sym] = desc
	desc.Generics = c.collectGenerics(n.Generics)
	selfT := &types.Named{Sym: sym, Args: argsOfGenerics(desc.Generics)}
	c.setSymType(sym, selfT)

	for _, ext := range n.Extends {
		if t := c.typeOf(ext); t != nil {
			desc.InterfaceExtends = append(desc.InterfaceExtends, t)
		}
	}
	for _, m := range n.Methods {
		md := c.methodDescOf(m, desc)
		desc.InterfaceMethods[m.Name] = md
		// Default-body methods are still regular methods for type checking.
		if m.Body != nil {
			desc.Methods[m.Name] = md
		}
	}
}

// collectAlias records a type alias. Aliases are transparent — they
// don't introduce a new type but instead re-export the Target.
func (c *checker) collectAlias(n *ast.TypeAliasDecl) {
	sym := c.topLevelSym(n.Name)
	if sym == nil {
		return
	}
	desc := &typeDesc{
		Sym:  sym,
		Kind: resolve.SymTypeAlias,
	}
	c.result.Descs[sym] = desc
	desc.Generics = c.collectGenerics(n.Generics)
	desc.Alias = c.typeOf(n.Target)
	c.setSymType(sym, desc.Alias)
}

// collectFn handles top-level functions and methods on struct/enum/
// interface. `owner` is nil for top-level fn; otherwise it is the
// typeDesc of the enclosing type (used to inherit its generics).
//
// Side effects:
//   - methods are registered on `owner.Methods[name]`;
//   - top-level fns have their FnType and generic list stamped onto
//     the file-scope Symbol via setSymType + symInfo.Generics.
func (c *checker) collectFn(n *ast.FnDecl, owner *typeDesc) {
	md := c.methodDescOf(n, owner)
	if owner != nil {
		owner.Methods[n.Name] = md
		return
	}
	sym := c.topLevelSym(n.Name)
	if sym == nil {
		return
	}
	c.setSymType(sym, md.Fn)
	// Stamp the fn's own generics onto symInfo so call-site
	// monomorphization can infer type args at Ident callees.
	if info := c.info(sym); info != nil {
		info.Generics = md.Generics
	}
}

// methodDescOf builds a methodDesc from an FnDecl without registering it
// anywhere. Used by interface declarations (signatures only) and shared
// with collectFn, which registers the result.
func (c *checker) methodDescOf(n *ast.FnDecl, owner *typeDesc) *methodDesc {
	var ownerGenerics []*types.TypeVar
	if owner != nil {
		ownerGenerics = owner.Generics
	}
	ownGenerics := c.collectGenerics(n.Generics)

	params := make([]types.Type, 0, len(n.Params))
	for _, p := range n.Params {
		params = append(params, c.typeOf(p.Type))
	}
	var ret types.Type = types.Unit
	if n.ReturnType != nil {
		ret = c.typeOf(n.ReturnType)
	}
	return &methodDesc{
		Name:          n.Name,
		Pub:           n.Pub,
		Recv:          n.Recv,
		Fn:            &types.FnType{Params: params, Return: ret},
		HasBody:       n.Body != nil,
		Params:        n.Params,
		Decl:          n,
		Owner:         owner,
		Generics:      ownGenerics,
		OwnerGenerics: ownerGenerics,
	}
}

// argsOfGenerics promotes [TypeVar] to [Type], used as the `Args` of a
// self-referential Named (the struct's own named type seen from inside).
func argsOfGenerics(tvs []*types.TypeVar) []types.Type {
	if len(tvs) == 0 {
		return nil
	}
	out := make([]types.Type, len(tvs))
	for i, tv := range tvs {
		out[i] = tv
	}
	return out
}
