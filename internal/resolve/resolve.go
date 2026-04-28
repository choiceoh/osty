package resolve

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// Result is the output of resolving one Osty file.
type Result struct {
	// RefsByID maps each resolved Ident's NodeID to the symbol it
	// refers to. Idents that failed resolution are not present in the
	// map (a corresponding diagnostic is emitted instead).
	RefsByID map[ast.NodeID]*Symbol
	// TypeRefsByID maps each NamedType's NodeID to the symbol the head
	// name refers to (e.g. for `Map<String, Int>` it records the ref
	// for `Map`). Only the head symbol is recorded; type arguments are
	// resolved recursively under their own NamedType keys.
	TypeRefsByID map[ast.NodeID]*Symbol
	// RefIdents / TypeRefIdents enumerate the nodes behind RefsByID /
	// TypeRefsByID, in no particular order. Callers that need to walk
	// every resolved identifier iterate these instead of the maps so
	// ports to the self-hosted compiler map cleanly onto
	// List<&Ident> / List<&NamedType>.
	RefIdents     []*ast.Ident
	TypeRefIdents []*ast.NamedType
	// FileScope is the file-level scope (children of the prelude). All
	// top-level declarations live here.
	FileScope *Scope
	// Diags collects every diagnostic produced during resolution.
	Diags []*diag.Diagnostic
}

// fileWithStdlib runs name resolution over a single parsed source file
// with an optional StdlibProvider: `use std.*` imports in
// the source resolve through the provider before falling back to the
// opaque-stub behavior. Passing a nil provider is equivalent to File.
//
// Unexported — external callers should use ResolveFileDefault.
func fileWithStdlib(file *ast.File, prelude *Scope, stdlib StdlibProvider) *Result {
	pkg := &Package{
		Name: "<file>",
		Files: []*PackageFile{{
			Path: "<input>",
			File: file,
		}},
	}
	if stdlib != nil {
		pkg.workspace = newStdlibOnlyWorkspace(stdlib)
	}
	pr := ResolvePackage(pkg, prelude)
	pf := pkg.Files[0]
	return &Result{
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
		Diags:         pr.Diags,
	}
}

// newStdlibOnlyWorkspace builds a Workspace whose sole purpose is to
// route `std.*` imports through the given provider. It has no on-disk
// Root, so non-std `use` targets still report as unknown packages —
// exactly the behavior a single-file compile should have.
func newStdlibOnlyWorkspace(stdlib StdlibProvider) *Workspace {
	return &Workspace{
		Root:       "",
		Packages:   map[string]*Package{},
		Stdlib:     stdlib,
		stdlibStub: true,
		loading:    map[string]bool{},
	}
}

// ResolvePackage runs name resolution over every file in pkg as a single
// namespace (§5.1). Top-level declarations share a package scope that is
// a child of the given prelude; each file gets its own file-scope child
// for `use` aliases.
//
// The resolver mutates pkg.Files[i].FileScope / Refs / TypeRefs in place.
// Existing parser diagnostics on each file are also merged into the
// returned Diags list.
//
// The authoritative execution path is the selfhost resolver
// (`toolchain/resolve.osty`) projected back onto Go's PackageResult /
// Scope / Symbol shapes. A Go-native fallback remains only for callers
// that provide an AST without source bytes (see ResolveFileDefault).
func ResolvePackage(pkg *Package, prelude *Scope) *PackageResult {
	if pkg == nil {
		return &PackageResult{}
	}
	if !canResolveViaNative(pkg) {
		r := newPkgResolver(pkg, prelude)
		r.declarePass(pkg)
		r.bodyPass(pkg)
		return &PackageResult{
			PackageScope: pkg.PkgScope,
			Diags:        r.diags,
		}
	}
	pkg.EnsureFiles()
	pkg.MaterializeCanonicalSources()
	return resolvePackageViaNative(pkg, prelude)
}

func canResolveViaNative(pkg *Package) bool {
	if len(pkg.Files) == 0 {
		return false
	}
	for _, pf := range pkg.Files {
		if len(pf.Source) == 0 && len(pf.CanonicalSource) == 0 && pf.File != nil {
			return false
		}
	}
	return true
}

// newPkgResolver initializes shared resolver state for a package walk.
// A package whose PkgScope is already populated (e.g. one supplied by a
// StdlibProvider) keeps its scope untouched — subsequent declare/body
// passes run against that pre-populated scope rather than replacing it.
func newPkgResolver(pkg *Package, prelude *Scope) *resolver {
	pkgScope := pkg.PkgScope
	if pkgScope == nil {
		pkgScope = NewScope(prelude, "package:"+pkg.Name)
		pkg.PkgScope = pkgScope
	}
	r := &resolver{
		refs:      map[*ast.Ident]*Symbol{},
		typeRefs:  map[*ast.NamedType]*Symbol{},
		current:   pkgScope,
		pkgScope:  pkgScope,
		owningPkg: pkg,
	}
	for _, f := range pkg.Files {
		for _, pd := range f.ParseDiags {
			if pd.File == "" {
				pd.File = f.Path
			}
			r.diags = append(r.diags, pd)
		}
	}
	return r
}

// declarePass installs every file's top-level symbols into the package
// scope, merging partial struct/enum declarations. Designed to run
// across all packages in a workspace before any body pass begins, so
// cross-package references in a later pass find the target already
// populated.
func (r *resolver) declarePass(pkg *Package) {
	merged := map[string]*mergedDecl{}
	for _, f := range pkg.Files {
		r.current = r.pkgScope
		r.filePath = f.Path
		for _, d := range f.File.Decls {
			r.declareTopLevelPackage(d, merged)
		}
	}
	r.filePath = ""
}

// bodyPass walks each file's declarations and top-level statements in
// turn, resolving bodies against the shared package scope + per-file
// use-alias scope. Assumes declarePass has already populated pkgScope
// for this package AND every package it references.
func (r *resolver) bodyPass(pkg *Package) {
	for _, f := range pkg.Files {
		fileScope := NewScope(r.pkgScope, "file:"+f.Path)
		f.FileScope = fileScope

		r.current = fileScope
		r.refs = map[*ast.Ident]*Symbol{}
		r.typeRefs = map[*ast.NamedType]*Symbol{}
		r.file = f.File
		r.filePath = f.Path

		for _, u := range f.File.Uses {
			r.declareUse(u)
		}
		for _, d := range f.File.Decls {
			r.resolveDecl(d)
		}
		if len(f.File.Stmts) > 0 {
			restore := r.enterFn(false)
			for _, s := range f.File.Stmts {
				if ds, ok := s.(*ast.DeferStmt); ok {
					r.emit(diag.New(diag.Error,
						"`defer` is not allowed at the top level of a script").
						Code(diag.CodeDeferAtScriptTop).
						PrimaryPos(ds.PosV, "top-level defer").
						Note("v0.4 §6 / §18.3: `defer` must appear inside an explicit `fn` body; the implicit main of a script does not accept it").
						Hint("wrap the deferred cleanup in an `fn` you invoke from the script body").
						Build())
					continue
				}
				r.resolveStmt(s)
			}
			restore()
		}
		f.RefsByID, f.RefIdents = projectRefsByID(r.refs)
		f.TypeRefsByID, f.TypeRefIdents = projectTypeRefsByID(r.typeRefs)
	}
}

// projectRefsByID rekeys Refs by ast.NodeID and emits the backing
// ident list. Idents with ID == 0 (synthetic, never stamped by the
// parser) are skipped; downstream consumers that key on NodeID treat
// zero as unassigned.
func projectRefsByID(refs map[*ast.Ident]*Symbol) (map[ast.NodeID]*Symbol, []*ast.Ident) {
	if len(refs) == 0 {
		return map[ast.NodeID]*Symbol{}, nil
	}
	byID := make(map[ast.NodeID]*Symbol, len(refs))
	idents := make([]*ast.Ident, 0, len(refs))
	for n, sym := range refs {
		if n == nil || n.ID == 0 {
			continue
		}
		byID[n.ID] = sym
		idents = append(idents, n)
	}
	return byID, idents
}

func projectTypeRefsByID(refs map[*ast.NamedType]*Symbol) (map[ast.NodeID]*Symbol, []*ast.NamedType) {
	if len(refs) == 0 {
		return map[ast.NodeID]*Symbol{}, nil
	}
	byID := make(map[ast.NodeID]*Symbol, len(refs))
	nts := make([]*ast.NamedType, 0, len(refs))
	for n, sym := range refs {
		if n == nil || n.ID == 0 {
			continue
		}
		byID[n.ID] = sym
		nts = append(nts, n)
	}
	return byID, nts
}

// resolver is the working state during one pass over a package. Each
// file is walked sequentially — `file`, `refs`, and `typeRefs` are
// re-pointed at the current file's state before Pass 2 begins.
type resolver struct {
	file     *ast.File
	filePath string // filesystem path of the current file; stamped onto emitted diagnostics
	refs     map[*ast.Ident]*Symbol
	typeRefs map[*ast.NamedType]*Symbol
	current  *Scope // the scope being populated/resolved
	diags    []*diag.Diagnostic

	// pkgScope is the shared top-level scope for the package. All
	// non-`use` top-level declarations live here, regardless of which
	// source file they came from. `Self` bindings, the partial-decl
	// merge table, and cross-file lookups all read from it.
	pkgScope *Scope
	// owningPkg points at the Package whose files this resolver is
	// walking. Non-nil only when invoked from ResolvePackage; set back
	// to nil between packages so a leaked reference to an old package
	// can't spuriously satisfy a `use` lookup.
	owningPkg *Package

	// methodCtx controls `self` / `Self` resolution for methods and
	// type bodies; see enterMethod / enterTypeBody.
	methodCtx methodCtx

	// flowCtx gates `break` / `continue` / `return` / `defer` and the
	// interface-default field-access rule. Mutated only through
	// enterFn / enterLoop so save/restore can't drift.
	flowCtx flowCtx
}

// flowCtx bundles the control-flow state that changes when entering a
// function-like body (fn, method, closure) or a loop. Using booleans
// instead of depth counters reflects how the checks actually read the
// state (each field is tested only for presence).
type flowCtx struct {
	// inFn is true inside any function-like body: top-level fn, method,
	// closure, or the implicit main of a script file.
	inFn bool
	// inLoop is true inside a `for` body. Nested loops don't need a
	// counter because `break`/`continue` always target the innermost.
	inLoop bool
	// loopLabels is the lexical stack of currently-visible loop labels.
	loopLabels []loopLabel
	// inIfaceDefault is true inside an interface method's default body.
	// Field access via `self.x` is rejected there per §2.6.2.
	inIfaceDefault bool
}

type loopLabel struct {
	Name string
	Pos  token.Pos
	End  token.Pos
}

// methodCtx bundles the state that controls `self` / `Self` semantics
// inside function / type bodies.
type methodCtx struct {
	// selfType points at the enclosing struct/enum/interface's symbol
	// when we are inside that type's body. `Self` resolves to this.
	selfType *Symbol
	// inMethod is true when a `self` identifier should resolve to the
	// receiver bound by the enclosing method signature.
	inMethod bool
	// selfSym is the synthetic Symbol bound to `self` inside a method
	// body. Only meaningful when inMethod is true.
	selfSym *Symbol
}

// ---- Diagnostic helpers ----

func (r *resolver) errorf(pos token.Pos, code, format string, args ...any) {
	d := diag.New(diag.Error, fmt.Sprintf(format, args...)).
		Code(code).
		PrimaryPos(pos, "").
		Build()
	if d.File == "" {
		d.File = r.filePath
	}
	r.diags = append(r.diags, d)
}

func (r *resolver) emit(d *diag.Diagnostic) {
	if d.File == "" {
		d.File = r.filePath
	}
	r.diags = append(r.diags, d)
}

// ---- Pass 0: use declarations ----

func (r *resolver) declareUse(u *ast.UseDecl) {
	name := u.Alias
	if name == "" {
		// Default alias is the last path segment (or the FFI path's basename).
		// Per §5.2 / §12.1.
		if u.IsFFI() {
			name = lastSeg(u.FFIPath(), '/')
			name = lastSeg(name, '.')
		} else if u.RawPath != "" && strings.Contains(u.RawPath, "/") {
			name = lastSeg(u.RawPath, '/')
		} else if len(u.Path) > 0 {
			name = lastSeg(u.Path[len(u.Path)-1], '/')
		}
	}
	if name == "" {
		return // malformed; parser already reported
	}
	sym := &Symbol{
		Name: name,
		Kind: SymPackage,
		Pos:  u.PosV,
		Decl: u,
		// v0.5 (G30) §5: `pub use` re-exports the aliased package at
		// the package scope, letting dependents `use parent.alias`
		// as if it were declared locally. Plain `use` stays file-
		// private.
		Pub: u.IsPub,
	}
	// If we're running inside a Workspace, resolve the `use` target and
	// attach the loaded Package to the symbol so member-access lookups
	// (`pkg.Name`) can navigate to it. FFI imports stay opaque — they
	// never point at an on-disk Osty package.
	if !u.IsFFI() && r.pkgScope != nil && r.pkg() != nil && r.pkg().workspace != nil {
		targetPath := UseKey(u)
		pkg, d := r.pkg().workspace.ResolveUseTarget(targetPath, u.PosV)
		if d != nil {
			r.emit(d)
		}
		if pkg != nil && !pkg.isCycleMarker {
			sym.Package = pkg
		}
	}
	if prev, ok := r.current.Define(sym); !ok {
		r.duplicate(u.PosV, name, prev)
	}
	// `pub use` also lives in the package scope so other packages can
	// reach the re-export through ordinary dotted lookup. The file
	// scope definition above still serves the lexical lookup inside
	// this file.
	if u.IsPub && r.pkgScope != nil && r.pkgScope != r.current {
		// Fresh Symbol instead of `*sym` copy so package- and file-scope
		// symbols don't alias identity — and so sync.Once (idOnce) isn't
		// byte-copied (vet's copylocks check).
		pkgSym := &Symbol{
			Name:    sym.Name,
			Kind:    sym.Kind,
			Pos:     sym.Pos,
			Decl:    sym.Decl,
			Pub:     sym.Pub,
			Package: sym.Package,
		}
		if _, ok := r.pkgScope.Define(pkgSym); !ok {
			// Collision against an already-declared pkg-scope symbol
			// is a re-export-over-existing conflict. Surface via the
			// duplicate reporter using the existing symbol's location.
			// Intentionally silent here for v0.5 MVP — the duplicate
			// path only flags the file-scope conflict above, and
			// refining the re-export-specific message (E0553) is
			// Phase 2.2+ follow-up when the full cycle / visibility
			// checker lands.
		}
	}
}

// pkg returns the Package this resolver is currently walking, if any.
// Single-file File() invocations wrap their input in a synthetic
// one-file Package whose workspace is nil; only workspace-driven flows
// have a non-nil workspace pointer.
func (r *resolver) pkg() *Package {
	// The resolver keeps per-file state in r.file; we locate the
	// enclosing Package by inspecting the pkgScope's user data — but
	// for simplicity and to avoid circular refs, we stash the pointer
	// in a dedicated field managed by ResolvePackage.
	return r.owningPkg
}

// resolvePackageMember handles the expression-position `pkg.name` shape
// where `pkg` resolved to a SymPackage. Records the resolved target in
// r.refs for the FieldExpr's synthetic child (we create a fake Ident at
// the member position to slot into the refs map) and emits a diagnostic
// when the name is missing or private.
func (r *resolver) resolvePackageMember(f *ast.FieldExpr, pkgSym *Symbol) {
	_ = r.lookupPackageMember(pkgSym, f.Name, f.EndV /*typePos*/, false)
}

// lookupPackageMember walks the target package's exported scope for
// `member`. Returns the resolved Symbol on success or nil after
// emitting an appropriate diagnostic (E0507/E0508/E0509) at refPos.
// `typePos` tightens error wording when the caller is in a type
// position ("undefined type in package X") vs. a value position
// ("undefined name in package X"). The Scope lookup stays on the Go
// side; the diagnostic wording is decided by
// toolchain/resolve.osty::selfLookupPackageMember via the
// selfhost.LookupPackageMember bridge so Osty owns the single source
// of truth for E0507 / E0508 messaging.
func (r *resolver) lookupPackageMember(pkgSym *Symbol, member string, refPos token.Pos, typePos bool) *Symbol {
	pkg := pkgSym.Package
	if pkg == nil || pkg.isStub {
		// Opaque package (FFI, unloaded stdlib, or URL import). Member
		// existence isn't checked — a future pass with type info will
		// refine this. Stay silent so the common `std.fs.readFile`
		// pattern doesn't flood `osty check` with warnings.
		return nil
	}
	if pkg.PkgScope == nil {
		// The package loaded but resolution hasn't populated its scope
		// yet. This can happen for cycle-marker packages — leave the
		// reference unbound; a prior E0506 should already cover it.
		return nil
	}
	sym := pkg.PkgScope.LookupLocal(member)
	found := sym != nil
	public := found && sym.Pub
	res := selfhost.LookupPackageMember(pkgSym.Name, member, typePos, found, public)
	switch res.Status {
	case selfhost.MemberLookupOK:
		return sym
	case selfhost.MemberLookupMissing:
		r.emit(diag.New(diag.Error, res.Message).
			Code(res.Code).
			PrimaryPos(refPos, res.Primary).
			Build())
		return nil
	case selfhost.MemberLookupPrivate:
		r.emit(diag.New(diag.Error, res.Message).
			Code(res.Code).
			PrimaryPos(refPos, res.Primary).
			Note(res.Note).
			Hint(res.Hint).
			Build())
		return nil
	}
	return nil
}

func lastSeg(s string, sep byte) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == sep {
			return s[i+1:]
		}
	}
	return s
}

// ---- Pass 1: top-level declarations ----

// declareTopLevelPackage installs top-level declarations into the shared package
// scope. Duplicate names between files are reported as cross-file conflicts,
// with the narrow exception of `struct` / `enum` partial declarations whose
// merging is handled by mergePartial.
//
// The handler builds the Symbol it would have installed and delegates
// to mergePartial for the insertion / duplicate logic. For enums, the
// variant symbols are registered under the same canonical enum Symbol
// so `Color.Red` and `Red` both land on the same enum declaration.
func (r *resolver) declareTopLevelPackage(d ast.Decl, merged map[string]*mergedDecl) {
	r.checkAnnotations(topLevelAnnotations(d), ast.TargetTopLevelDecl)
	switch n := d.(type) {
	case *ast.FnDecl:
		r.mergePartial(r.pkgScope, merged, n.Name, &Symbol{
			Name: n.Name, Kind: SymFn, Pos: n.PosV, Decl: n, Pub: n.Pub,
		}, d)
	case *ast.StructDecl:
		r.mergePartial(r.pkgScope, merged, n.Name, &Symbol{
			Name: n.Name, Kind: SymStruct, Pos: n.PosV, Decl: n, Pub: n.Pub,
		}, d)
	case *ast.EnumDecl:
		r.mergePartial(r.pkgScope, merged, n.Name, &Symbol{
			Name: n.Name, Kind: SymEnum, Pos: n.PosV, Decl: n, Pub: n.Pub,
		}, d)
		for _, v := range n.Variants {
			r.checkAnnotations(v.Annotations, ast.TargetVariant)
			r.mergePartial(r.pkgScope, merged, v.Name, &Symbol{
				Name: v.Name, Kind: SymVariant, Pos: v.PosV, Decl: v, Pub: n.Pub,
			}, d)
		}
	case *ast.InterfaceDecl:
		r.mergePartial(r.pkgScope, merged, n.Name, &Symbol{
			Name: n.Name, Kind: SymInterface, Pos: n.PosV, Decl: n, Pub: n.Pub,
		}, d)
	case *ast.TypeAliasDecl:
		r.mergePartial(r.pkgScope, merged, n.Name, &Symbol{
			Name: n.Name, Kind: SymTypeAlias, Pos: n.PosV, Decl: n, Pub: n.Pub,
		}, d)
	case *ast.LetDecl:
		r.mergePartial(r.pkgScope, merged, n.Name, &Symbol{
			Name: n.Name, Kind: SymLet, Pos: n.PosV, Decl: n, Pub: n.Pub,
		}, d)
	}
}

// topLevelAnnotations pulls the Annotations slice from whichever concrete
// decl type was passed. Returns nil for decl kinds that don't carry
// annotations (currently all six carry them, but the default keeps the
// helper safe against future additions).
func topLevelAnnotations(d ast.Decl) []*ast.Annotation {
	switch n := d.(type) {
	case *ast.FnDecl:
		return n.Annotations
	case *ast.StructDecl:
		return n.Annotations
	case *ast.EnumDecl:
		return n.Annotations
	case *ast.InterfaceDecl:
		return n.Annotations
	case *ast.TypeAliasDecl:
		return n.Annotations
	case *ast.LetDecl:
		return n.Annotations
	}
	return nil
}

// checkAnnotations validates the annotations on a declaration against
// v0.2 R26 and v0.4 §18.1:
//
//   - unknown names are flagged with E0400 here;
//   - the annotation's target kind must be permitted (E0607);
//   - the same annotation name may not appear twice on one target — for
//     example `#[deprecated] #[deprecated] fn …` is rejected (E0609).
func (r *resolver) checkAnnotations(annots []*ast.Annotation, target ast.AnnotationTarget) {
	var seen map[string]*ast.Annotation
	for _, a := range annots {
		if !ast.IsAllowedAnnotation(a.Name) {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("unknown annotation `#[%s]`", a.Name)).
				Code(diag.CodeUnknownAnnotation).
				Primary(diag.Span{Start: a.PosV, End: a.EndV},
					"this annotation name is not recognized").
				Note("v0.4 §18.1: only `#[json]`, `#[deprecated]`, `#[allow]`, `#[cfg]`, `#[op]`, `#[test]`, `#[vectorize]`, `#[no_vectorize]`, `#[parallel]`, `#[unroll]`, `#[inline]`, `#[hot]`, `#[cold]`, `#[target_feature]`, `#[noalias]`, `#[pure]`, and the runtime sublanguage annotations are permitted").
				Build())
			continue
		}
		if !ast.AnnotationAllowedAt(a.Name, target) {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("annotation `#[%s]` is not allowed here", a.Name)).
				Code(diag.CodeAnnotationBadTarget).
				Primary(diag.Span{Start: a.PosV, End: a.EndV},
					"annotation on the wrong kind of declaration").
				Note(fmt.Sprintf("`#[%s]` is permitted on %s", a.Name, ast.AnnotationTargetString(a.Name))).
				Build())
		}
		if seen == nil {
			seen = map[string]*ast.Annotation{}
		}
		if prev, dup := seen[a.Name]; dup {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("annotation `#[%s]` is repeated on the same target", a.Name)).
				Code(diag.CodeDuplicateAnnotation).
				Primary(diag.Span{Start: a.PosV, End: a.EndV},
					"duplicate annotation here").
				Secondary(diag.Span{Start: prev.PosV, End: prev.EndV},
					"first occurrence here").
				Note("v0.4 §18.1: the same annotation name may not appear more than once on a single target").
				Build())
			continue
		}
		seen[a.Name] = a
		r.checkAnnotationArgs(a, target)
	}
}

// checkAnnotationArgs validates each `#[name(arg = value)]` argument
// against the fixed signature for that annotation (§3.8 / §19.6).
// Unknown keys and wrong-typed values are both reported here.
func (r *resolver) checkAnnotationArgs(a *ast.Annotation, target ast.AnnotationTarget) {
	switch a.Name {
	case "json":
		r.checkJSONArgs(a, target)
	case "deprecated":
		r.checkDeprecatedArgs(a)
	case "repr":
		r.checkReprArgs(a)
	case "export":
		r.checkExportArgs(a)
	case "intrinsic", "pod", "c_abi", "no_alloc":
		r.checkNoArgsRuntime(a)
	case "vectorize":
		r.checkVectorizeArgs(a)
	case "no_vectorize":
		r.checkNoVectorizeArgs(a)
	case "parallel":
		r.checkParallelArgs(a)
	case "unroll":
		r.checkUnrollArgs(a)
	case "inline":
		r.checkInlineArgs(a)
	case "hot", "cold", "pure":
		r.checkBareFlag(a)
	case "target_feature":
		r.checkTargetFeatureArgs(a)
	case "noalias":
		r.checkNoaliasArgs(a)
	}
}

// checkReprArgs validates `#[repr(c)]` per LANG_SPEC §19.6: exactly
// one bare-flag argument whose key is `c`. The v0.4 spec allocates
// only the `c` form; future repr modes (e.g. `packed`) would extend
// this set but are not part of v0.5.
func (r *resolver) checkReprArgs(a *ast.Annotation) {
	if len(a.Args) == 0 {
		r.emit(diag.New(diag.Error,
			"`#[repr]` requires a layout argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.PosV, "missing argument `(c)`").
			Note("LANG_SPEC §19.6: the only accepted form is `#[repr(c)]`").
			Build())
		return
	}
	if len(a.Args) > 1 {
		r.emit(diag.New(diag.Error,
			"`#[repr]` accepts exactly one argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.Args[1].PosV, "extra argument").
			Build())
	}
	arg := a.Args[0]
	if !isFlagOrTrue(arg) {
		r.emit(diag.New(diag.Error,
			"`#[repr]` takes a bare-flag argument, not a value").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(arg.PosV, "expected `c`").
			Build())
		return
	}
	if arg.Key != "c" {
		r.emit(diag.New(diag.Error,
			fmt.Sprintf("`#[repr(%s)]` is not a recognized layout", arg.Key)).
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(arg.PosV, "only `#[repr(c)]` is accepted").
			Note("LANG_SPEC §19.6: v0.5 recognizes only the `c` layout").
			Build())
	}
}

// checkExportArgs validates `#[export("symbol")]` per LANG_SPEC §19.6:
// exactly one positional string-literal argument naming the emitted
// symbol. The argument has no key — the annotation-argument parser
// stores the string literal as `Key="" Value=StringLit`.
func (r *resolver) checkExportArgs(a *ast.Annotation) {
	if len(a.Args) == 0 {
		r.emit(diag.New(diag.Error,
			"`#[export]` requires a symbol-name argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.PosV, "missing string literal, e.g. `(\"osty.gc.alloc_v1\")`").
			Note("LANG_SPEC §19.6: `#[export(\"name\")]` takes one string literal").
			Build())
		return
	}
	if len(a.Args) > 1 {
		r.emit(diag.New(diag.Error,
			"`#[export]` accepts exactly one argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.Args[1].PosV, "extra argument").
			Build())
	}
	arg := a.Args[0]
	if arg.Key != "" {
		r.emit(diag.New(diag.Error,
			"`#[export]` takes a positional string literal, not a key-value argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(arg.PosV, fmt.Sprintf("remove `%s =`", arg.Key)).
			Build())
		return
	}
	name, ok := stringArg(arg.Value)
	if !ok {
		r.emit(diag.New(diag.Error,
			"`#[export]` requires a string literal argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(arg.PosV, "expected string literal").
			Build())
		return
	}
	if name == "" {
		r.emit(diag.New(diag.Error,
			"`#[export]` symbol name must be non-empty").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(arg.PosV, "empty symbol name").
			Build())
	}
}

// checkNoArgsRuntime validates the runtime annotations that take no
// arguments: `#[intrinsic]`, `#[pod]`, `#[c_abi]`, `#[no_alloc]`.
// Any argument is rejected.
func (r *resolver) checkNoArgsRuntime(a *ast.Annotation) {
	if len(a.Args) == 0 {
		return
	}
	r.emit(diag.New(diag.Error,
		fmt.Sprintf("`#[%s]` does not take arguments", a.Name)).
		Code(diag.CodeAnnotationBadArg).
		PrimaryPos(a.Args[0].PosV, "unexpected argument").
		Note("LANG_SPEC §19.6: this annotation is a bare flag").
		Build())
}

// checkVectorizeArgs validates `#[vectorize]` per v0.6 A5 / A5.1.
// The bare-flag form is still accepted ("compiler chooses everything")
// and three optional tuning args are now permitted:
//
//   - `scalable` — prefer scalable vectorization (SVE, RVV) over
//     fixed-width (NEON). Bare flag.
//   - `predicate` — enable tail folding so the vectorizer processes
//     trip counts that are not a multiple of the vector width via
//     masked operations instead of a scalar tail loop. Bare flag.
//   - `width = N` — force the vectorization factor to exactly N
//     (positive int literal, 1..1024). Unlocks AVX-512 ZMM on Intel,
//     where the cost model otherwise refuses 512-bit vectors.
//
// Unknown keys, duplicate keys, and out-of-range widths are rejected
// with E0739.
func (r *resolver) checkVectorizeArgs(a *ast.Annotation) {
	var hasScalable, hasPredicate, hasWidth bool
	for _, arg := range a.Args {
		if arg == nil {
			continue
		}
		switch arg.Key {
		case "scalable":
			if hasScalable {
				r.emit(diag.New(diag.Error,
					"duplicate `scalable` in `#[vectorize(...)]`").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "second occurrence").
					Build())
				continue
			}
			hasScalable = true
			if !isFlagOrTrue(arg) {
				r.emit(diag.New(diag.Error,
					"`scalable` in `#[vectorize(...)]` is a bare flag").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "expected `scalable`, not `scalable = ...`").
					Build())
			}
		case "predicate":
			if hasPredicate {
				r.emit(diag.New(diag.Error,
					"duplicate `predicate` in `#[vectorize(...)]`").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "second occurrence").
					Build())
				continue
			}
			hasPredicate = true
			if !isFlagOrTrue(arg) {
				r.emit(diag.New(diag.Error,
					"`predicate` in `#[vectorize(...)]` is a bare flag").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "expected `predicate`, not `predicate = ...`").
					Build())
			}
		case "width":
			if hasWidth {
				r.emit(diag.New(diag.Error,
					"duplicate `width` in `#[vectorize(...)]`").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "second occurrence").
					Build())
				continue
			}
			hasWidth = true
			w, ok := positiveIntArg(arg.Value)
			if !ok {
				r.emit(diag.New(diag.Error,
					"`width` in `#[vectorize(...)]` requires a positive integer literal").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "expected `width = <1..1024>`").
					Build())
				continue
			}
			if w < 1 || w > 1024 {
				r.emit(diag.New(diag.Error,
					"`width` in `#[vectorize(...)]` must be in 1..1024").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "value out of range").
					Build())
			}
		case "":
			r.emit(diag.New(diag.Error,
				"`#[vectorize(...)]` arguments must be named").
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "positional argument not allowed").
				Build())
		default:
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("unknown `#[vectorize]` argument `%s`", arg.Key)).
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "not a recognized key").
				Note("accepted: `scalable`, `predicate`, `width = N`").
				Build())
		}
	}
}

// checkInlineArgs validates `#[inline]` (v0.6 A8). Accepts the bare
// form and two sub-flags `always` / `never`, which pick the hard LLVM
// attribute (`alwaysinline` / `noinline`) vs the default soft hint
// (`inlinehint`).
func (r *resolver) checkInlineArgs(a *ast.Annotation) {
	switch len(a.Args) {
	case 0:
		return
	case 1:
		arg := a.Args[0]
		if arg == nil {
			return
		}
		switch arg.Key {
		case "always", "never":
			if !isFlagOrTrue(arg) {
				r.emit(diag.New(diag.Error,
					fmt.Sprintf("`%s` in `#[inline(...)]` is a bare flag", arg.Key)).
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV,
						fmt.Sprintf("expected `%s`, not `%s = ...`", arg.Key, arg.Key)).
					Build())
			}
		default:
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("unknown `#[inline]` argument `%s`", arg.Key)).
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "not a recognized flag").
				Note("accepted: `always` (force inlining), `never` (forbid inlining), or no argument (soft hint)").
				Build())
		}
	default:
		r.emit(diag.New(diag.Error,
			"`#[inline(...)]` takes at most one argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.Args[1].PosV, "extra argument").
			Note("use either `#[inline]`, `#[inline(always)]`, or `#[inline(never)]`").
			Build())
	}
}

// checkBareFlag is the shared validator for annotations whose entire
// surface is the bare-flag form: `#[hot]`, `#[cold]`, and similar.
// Any argument is rejected.
func (r *resolver) checkBareFlag(a *ast.Annotation) {
	if len(a.Args) == 0 {
		return
	}
	r.emit(diag.New(diag.Error,
		fmt.Sprintf("`#[%s]` does not take arguments", a.Name)).
		Code(diag.CodeAnnotationBadArg).
		PrimaryPos(a.Args[0].PosV, "unexpected argument").
		Build())
}

// checkTargetFeatureArgs validates `#[target_feature(...)]` (v0.6
// A10): at least one bare-identifier argument naming a CPU feature.
// Duplicate feature names are rejected so the backend doesn't have
// to dedupe; malformed shapes (`feature = "value"` or empty arg
// lists) are rejected with a pointed hint.
func (r *resolver) checkTargetFeatureArgs(a *ast.Annotation) {
	if len(a.Args) == 0 {
		r.emit(diag.New(diag.Error,
			"`#[target_feature(...)]` requires at least one feature name").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.PosV, "empty feature list").
			Note("example: `#[target_feature(avx512f, avx512bw)]`").
			Build())
		return
	}
	seen := map[string]bool{}
	for _, arg := range a.Args {
		if arg == nil {
			continue
		}
		if arg.Key == "" {
			r.emit(diag.New(diag.Error,
				"`#[target_feature(...)]` feature names must be bare identifiers").
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "expected a feature name").
				Build())
			continue
		}
		if arg.Value != nil && !isFlagOrTrue(arg) {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("feature `%s` in `#[target_feature(...)]` takes no value", arg.Key)).
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV,
					fmt.Sprintf("expected `%s`, not `%s = ...`", arg.Key, arg.Key)).
				Build())
			continue
		}
		if seen[arg.Key] {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("duplicate feature `%s` in `#[target_feature(...)]`", arg.Key)).
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "second occurrence").
				Build())
			continue
		}
		seen[arg.Key] = true
	}
}

// checkNoaliasArgs validates `#[noalias]` / `#[noalias(p1, p2)]`
// (v0.6 A11). Bare form promises every pointer parameter is noalias;
// the arg-list form names specific parameters. Each argument must be
// a bare identifier (a parameter name); key = value shapes and
// duplicates are rejected. The resolver does not cross-check that
// the names match actual parameters — that check lives in the
// emitter since it already iterates params, and keeping it out of
// the resolver keeps the rule independent of param type analysis.
func (r *resolver) checkNoaliasArgs(a *ast.Annotation) {
	if len(a.Args) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, arg := range a.Args {
		if arg == nil {
			continue
		}
		if arg.Key == "" {
			r.emit(diag.New(diag.Error,
				"`#[noalias(...)]` parameter names must be bare identifiers").
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "expected a parameter name").
				Build())
			continue
		}
		if arg.Value != nil && !isFlagOrTrue(arg) {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("parameter `%s` in `#[noalias(...)]` takes no value", arg.Key)).
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV,
					fmt.Sprintf("expected `%s`, not `%s = ...`", arg.Key, arg.Key)).
				Build())
			continue
		}
		if seen[arg.Key] {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("duplicate parameter `%s` in `#[noalias(...)]`", arg.Key)).
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "second occurrence").
				Build())
			continue
		}
		seen[arg.Key] = true
	}
}

// checkNoVectorizeArgs validates `#[no_vectorize]` (v0.6 A5.2), a
// bare-flag opt-out. Any argument is rejected.
func (r *resolver) checkNoVectorizeArgs(a *ast.Annotation) {
	if len(a.Args) == 0 {
		return
	}
	r.emit(diag.New(diag.Error,
		"`#[no_vectorize]` does not take arguments").
		Code(diag.CodeAnnotationBadArg).
		PrimaryPos(a.Args[0].PosV, "unexpected argument").
		Note("v0.6 A5.2: `#[no_vectorize]` is a bare flag that opts a function out of the default vectorize treatment").
		Build())
}

// checkParallelArgs validates `#[parallel]` (v0.6 A6), a bare-flag
// annotation. Any argument is rejected.
func (r *resolver) checkParallelArgs(a *ast.Annotation) {
	if len(a.Args) == 0 {
		return
	}
	r.emit(diag.New(diag.Error,
		"`#[parallel]` does not take arguments").
		Code(diag.CodeAnnotationBadArg).
		PrimaryPos(a.Args[0].PosV, "unexpected argument").
		Note("v0.6 A6: `#[parallel]` is a bare flag asserting no loop-carried memory dependencies").
		Build())
}

// checkUnrollArgs validates `#[unroll]` / `#[unroll(count = N)]`
// (v0.6 A7). Accepts either the bare form (no args — compiler picks
// factor) or a single `count = <1..1024>` key/value pair. Positional
// values are not supported because the v0.5 annotation grammar only
// produces `key` or `key = value` arg shapes.
func (r *resolver) checkUnrollArgs(a *ast.Annotation) {
	switch len(a.Args) {
	case 0:
		return
	case 1:
		arg := a.Args[0]
		if arg == nil {
			return
		}
		if arg.Key != "count" {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("unknown `#[unroll]` argument `%s`", arg.Key)).
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "not a recognized key").
				Note("accepted: `count = N` for a fixed unroll factor").
				Build())
			return
		}
		n, ok := positiveIntArg(arg.Value)
		if !ok {
			r.emit(diag.New(diag.Error,
				"`#[unroll(count = N)]` requires a positive integer literal").
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "expected `count = <1..1024>`").
				Build())
			return
		}
		if n < 1 || n > 1024 {
			r.emit(diag.New(diag.Error,
				"`#[unroll(count = N)]` value must be in 1..1024").
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "value out of range").
				Build())
		}
	default:
		r.emit(diag.New(diag.Error,
			"`#[unroll(...)]` takes at most one argument").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.Args[1].PosV, "extra argument").
			Build())
	}
}

// positiveIntArg extracts the value of a positive integer literal
// annotation argument. Returns (value, true) only when the expression
// is an `*ast.IntLit` whose text parses as a non-negative int64.
func positiveIntArg(e ast.Expr) (int64, bool) {
	lit, ok := e.(*ast.IntLit)
	if !ok {
		return 0, false
	}
	text := strings.ReplaceAll(lit.Text, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(text, "0x"), strings.HasPrefix(text, "0X"):
		base = 16
		text = text[2:]
	case strings.HasPrefix(text, "0o"), strings.HasPrefix(text, "0O"):
		base = 8
		text = text[2:]
	case strings.HasPrefix(text, "0b"), strings.HasPrefix(text, "0B"):
		base = 2
		text = text[2:]
	}
	if text == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// checkJSONArgs validates #[json(...)] argument shapes per §3.8.1:
//
//   - key   = "<string>"
//   - skip  (flag) or skip = true
//   - optional (flag) or optional = true
//
// The combined-argument constraints (skip mutually excludes key /
// optional) are enforced here too.
func (r *resolver) checkJSONArgs(a *ast.Annotation, target ast.AnnotationTarget) {
	var hasKey, hasSkip, hasOptional bool
	for _, arg := range a.Args {
		switch arg.Key {
		case "key":
			hasKey = true
			if _, ok := stringArg(arg.Value); !ok {
				r.emit(diag.New(diag.Error,
					"`#[json(key = ...)]` requires a string literal").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "expected string literal here").
					Build())
			}
		case "skip":
			hasSkip = true
			if !isFlagOrTrue(arg) {
				r.emit(diag.New(diag.Error,
					"`#[json(skip)]` takes no value or `skip = true`").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "unexpected value").
					Build())
			}
		case "optional":
			hasOptional = true
			if target != ast.TargetStructField {
				r.emit(diag.New(diag.Error,
					"`#[json(optional)]` is only valid on struct fields").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "not a struct field option").
					Build())
			}
			if !isFlagOrTrue(arg) {
				r.emit(diag.New(diag.Error,
					"`#[json(optional)]` takes no value or `optional = true`").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "unexpected value").
					Build())
			}
		default:
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("`#[json]` does not accept argument `%s`", arg.Key)).
				Code(diag.CodeUnknownAnnotation).
				PrimaryPos(arg.PosV, "unknown argument").
				Note("valid arguments are `key`, `skip`, `optional`").
				Build())
		}
	}
	if hasSkip && (hasKey || hasOptional) {
		r.emit(diag.New(diag.Error,
			"`skip` is mutually exclusive with `key` and `optional`").
			Code(diag.CodeAnnotationBadArg).
			PrimaryPos(a.PosV, "remove `skip` or the other argument").
			Build())
	}
}

// checkDeprecatedArgs validates #[deprecated(...)] per §3.8.2. All
// three arguments are optional and each accepts a string literal.
func (r *resolver) checkDeprecatedArgs(a *ast.Annotation) {
	for _, arg := range a.Args {
		switch arg.Key {
		case "since", "use", "message":
			if arg.Value == nil {
				r.emit(diag.New(diag.Error,
					fmt.Sprintf("`#[deprecated(%s = ...)]` requires a string literal", arg.Key)).
					Code(diag.CodeUnknownAnnotation).
					PrimaryPos(arg.PosV, "missing value").
					Build())
				continue
			}
			if _, ok := stringArg(arg.Value); !ok {
				r.emit(diag.New(diag.Error,
					fmt.Sprintf("`#[deprecated(%s = ...)]` requires a string literal", arg.Key)).
					Code(diag.CodeUnknownAnnotation).
					PrimaryPos(arg.PosV, "expected string literal").
					Build())
			}
		default:
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("`#[deprecated]` does not accept argument `%s`", arg.Key)).
				Code(diag.CodeUnknownAnnotation).
				PrimaryPos(arg.PosV, "unknown argument").
				Note("valid arguments are `since`, `use`, `message`").
				Build())
		}
	}
}

// stringArg extracts the static string value of an annotation
// argument, returning (value, true) only for non-interpolated string
// literals.
func stringArg(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, p := range lit.Parts {
		if !p.IsLit {
			return "", false
		}
		b.WriteString(p.Lit)
	}
	return b.String(), true
}

// isFlagOrTrue reports whether an annotation argument is bare (no
// value) or explicitly set to `true`.
func isFlagOrTrue(arg *ast.AnnotationArg) bool {
	if arg.Value == nil {
		return true
	}
	b, ok := arg.Value.(*ast.BoolLit)
	return ok && b.Value
}

func (r *resolver) duplicate(pos token.Pos, name string, prev *Symbol) {
	d := diag.New(diag.Error,
		fmt.Sprintf("`%s` is already defined as a %s", name, prev.Kind)).
		Code(diag.CodeDuplicateDecl).
		PrimaryPos(pos, "duplicate declaration here")
	if prev.Pos.Line > 0 {
		d.Secondary(diag.Span{Start: prev.Pos, End: prev.Pos},
			"previous declaration here")
	}
	d.Hint("rename one of the declarations or remove the duplicate")
	r.emit(d.Build())
}

// ---- Pass 2: declaration bodies ----

func (r *resolver) resolveDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		r.resolveFnDecl(n, nil, false)
	case *ast.StructDecl:
		r.resolveStructDecl(n)
	case *ast.EnumDecl:
		r.resolveEnumDecl(n)
	case *ast.InterfaceDecl:
		r.resolveInterfaceDecl(n)
	case *ast.TypeAliasDecl:
		r.resolveTypeAliasDecl(n)
	case *ast.LetDecl:
		// Top-level let value: resolved in file scope.
		if n.Type != nil {
			r.resolveType(n.Type)
		}
		if n.Value != nil {
			r.resolveExpr(n.Value)
		}
	case *ast.UseDecl:
		// Already handled in pass 0.
	}
}

// resolveFnDecl walks a fn or method body. selfType is non-nil when
// the fn is a method; ifaceDefault is true when it is a default-method
// body inside an interface (additional restrictions apply — §2.6.2).
func (r *resolver) resolveFnDecl(f *ast.FnDecl, selfType *Symbol, ifaceDefault bool) {
	scope := NewScope(r.current, "fn:"+f.Name)
	r.withScope(scope, func() {
		r.declareGenerics(f.Generics)
		var restoreMethod func()
		if f.Recv != nil {
			selfSym := &Symbol{Name: "self", Kind: SymParam, Pos: f.Recv.PosV, Decl: f.Recv}
			r.defineLocal("self", selfSym)
			restoreMethod = r.enterMethod(selfSym, selfType)
		}
		for _, p := range f.Params {
			r.resolveType(p.Type)
			if p.Default != nil {
				r.resolveExpr(p.Default)
			}
			if p.Name != "" {
				r.defineLocal(p.Name, &Symbol{
					Name: p.Name, Kind: SymParam, Pos: p.PosV, Decl: p,
				})
			}
		}
		if f.ReturnType != nil {
			r.resolveType(f.ReturnType)
		}
		if f.Body != nil {
			restoreFn := r.enterFn(ifaceDefault)
			r.resolveBlock(f.Body)
			restoreFn()
		}
		if restoreMethod != nil {
			restoreMethod()
		}
	})
}

// enterMethod pushes the method-body context and returns a restore
// callback. The call sequence is:
//
//	restore := r.enterMethod(selfSym, selfType)
//	defer restore()
//
// or an inline equivalent. This keeps the three related flags (selfType,
// inMethod, selfSym) in lockstep.
func (r *resolver) enterMethod(selfSym, selfType *Symbol) func() {
	prev := r.methodCtx
	r.methodCtx = methodCtx{
		selfType: selfType,
		inMethod: true,
		selfSym:  selfSym,
	}
	return func() { r.methodCtx = prev }
}

// enterTypeBody pushes the `Self`-only context (no `self` binding).
// Used when walking struct/enum/interface fields, variants, and bounds.
func (r *resolver) enterTypeBody(selfType *Symbol) func() {
	prev := r.methodCtx
	r.methodCtx = methodCtx{selfType: selfType}
	return func() { r.methodCtx = prev }
}

// enterFn pushes a fresh function-like flow context: `return`/`defer`
// become legal, and the loop context is reset so `break`/`continue`
// inside the body must refer to a loop inside THIS function, not to
// some enclosing caller's loop. `ifaceDefault` is sticky to this frame
// only; nested closures / methods start with it reset to false.
func (r *resolver) enterFn(ifaceDefault bool) func() {
	prev := r.flowCtx
	r.flowCtx = flowCtx{inFn: true, inIfaceDefault: ifaceDefault}
	return func() { r.flowCtx = prev }
}

// enterLoop marks the current context as inside a loop body for the
// duration of the returned restore callback, optionally pushing a
// visible `'label` binding for labeled loops.
func (r *resolver) enterLoop(label string, labelPos, labelEnd token.Pos) func() {
	prev := r.flowCtx
	next := prev
	next.inLoop = true
	if label != "" {
		next.loopLabels = append(append([]loopLabel(nil), prev.loopLabels...), loopLabel{
			Name: label,
			Pos:  labelPos,
			End:  labelEnd,
		})
	}
	r.flowCtx = next
	return func() { r.flowCtx = prev }
}

func (r *resolver) loopLabelInScope(name string) (*loopLabel, bool) {
	for i := len(r.flowCtx.loopLabels) - 1; i >= 0; i-- {
		lbl := &r.flowCtx.loopLabels[i]
		if lbl.Name == name {
			return lbl, true
		}
	}
	return nil, false
}

func (r *resolver) checkLoopLabelShadow(name string, pos, end token.Pos) {
	if name == "" {
		return
	}
	if prev, ok := r.loopLabelInScope(name); ok {
		r.emit(diag.New(diag.Error,
			fmt.Sprintf("loop label `%s` shadows an outer loop label", name)).
			Code(diag.CodeLabelShadow).
			Primary(diag.Span{Start: pos, End: end}, "shadowed loop label").
			Note(fmt.Sprintf("outer `%s` label is already in scope at %s", prev.Name, prev.Pos)).
			Build())
	}
}

func (r *resolver) resolveLoopLabelRef(name string, pos, end token.Pos) {
	if name == "" || !r.flowCtx.inLoop {
		return
	}
	if _, ok := r.loopLabelInScope(name); ok {
		return
	}
	r.emit(diag.New(diag.Error,
		fmt.Sprintf("undefined loop label `%s`", name)).
		Code(diag.CodeUndefinedLabel).
		Primary(diag.Span{Start: pos, End: end}, "unknown loop label").
		Build())
}

// declareGenerics defines every generic parameter in the current scope
// and resolves each parameter's bounds against that same scope (so `T`
// bound by `Container<T>` sees sibling params).
func (r *resolver) declareGenerics(gs []*ast.GenericParam) {
	for _, g := range gs {
		r.defineLocal(g.Name, &Symbol{
			Name: g.Name, Kind: SymGeneric, Pos: g.PosV, Decl: g,
		})
	}
	for _, g := range gs {
		for _, c := range g.Constraints {
			r.resolveType(c)
		}
	}
}

func (r *resolver) resolveStructDecl(s *ast.StructDecl) {
	r.resolveTypeBody(s.Name, "struct", s.PosV, s, s.Generics, func(selfSym *Symbol) {
		r.checkDuplicateFields(s.Fields)
		r.checkDuplicateMethods(s.Methods)
		for _, fld := range s.Fields {
			r.checkAnnotations(fld.Annotations, ast.TargetStructField)
			r.checkJSONFieldArgs(fld)
			r.resolveType(fld.Type)
			if fld.Default != nil {
				r.resolveExpr(fld.Default)
			}
		}
		for _, m := range s.Methods {
			r.checkAnnotations(m.Annotations, ast.TargetMethod)
			r.resolveFnDecl(m, selfSym, false)
		}
	})
}

func (r *resolver) resolveEnumDecl(e *ast.EnumDecl) {
	r.resolveTypeBody(e.Name, "enum", e.PosV, e, e.Generics, func(selfSym *Symbol) {
		r.checkDuplicateVariants(e.Variants)
		r.checkDuplicateVariantJSONTags(e)
		r.checkDuplicateMethods(e.Methods)
		// Variant annotations were already validated in declareTopLevel;
		// only resolve variant-field types here.
		for _, v := range e.Variants {
			for _, t := range v.Fields {
				r.resolveType(t)
			}
		}
		for _, m := range e.Methods {
			r.checkAnnotations(m.Annotations, ast.TargetMethod)
			r.resolveFnDecl(m, selfSym, false)
		}
	})
}

func (r *resolver) resolveInterfaceDecl(i *ast.InterfaceDecl) {
	r.resolveTypeBody(i.Name, "interface", i.PosV, i, i.Generics, func(selfSym *Symbol) {
		r.checkDuplicateMethods(i.Methods)
		for _, ext := range i.Extends {
			r.resolveType(ext)
		}
		for _, m := range i.Methods {
			// §2.6.2: default bodies (Body != nil) may not access
			// struct fields via `self.x`.
			r.resolveFnDecl(m, selfSym, m.Body != nil)
		}
	})
}

// checkDuplicateFields, checkDuplicateVariants, and checkDuplicateMethods
// all detect the same shape: a list of named AST members where duplicates
// produce a CodeDuplicateDecl with a primary span on the second
// occurrence and a secondary span pointing at the first. Only the noun
// and the method-specific partial-declaration note differ, so the three
// callers share a single generic helper.

func (r *resolver) checkDuplicateFields(fields []*ast.Field) {
	checkDuplicateNamed(r, fields, "field", "")
}

func (r *resolver) checkDuplicateVariants(variants []*ast.Variant) {
	checkDuplicateNamed(r, variants, "variant", "")
}

func (r *resolver) checkDuplicateVariantJSONTags(e *ast.EnumDecl) {
	type record struct {
		variant *ast.Variant
		tag     string
	}
	seen := map[string]record{}
	for _, v := range e.Variants {
		tag, skipped := jsonVariantTag(v)
		if skipped {
			continue
		}
		if prev, ok := seen[tag]; ok {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("enum `%s` has duplicate JSON tag `%s`", e.Name, tag)).
				Code(diag.CodeDuplicateDecl).
				Primary(diag.Span{Start: v.Pos(), End: v.End()},
					"duplicate JSON tag here").
				Secondary(diag.Span{Start: prev.variant.Pos(), End: prev.variant.End()},
					"previous JSON tag here").
				Build())
			continue
		}
		seen[tag] = record{variant: v, tag: tag}
	}
}

func (r *resolver) checkJSONFieldArgs(f *ast.Field) {
	for _, ann := range f.Annotations {
		if ann.Name != "json" {
			continue
		}
		for _, arg := range ann.Args {
			if arg.Key != "optional" {
				continue
			}
			if !jsonFieldTypeAllowsOptional(f.Type) {
				r.emit(diag.New(diag.Error,
					"`#[json(optional)]` requires an optional field type").
					Code(diag.CodeAnnotationBadArg).
					PrimaryPos(arg.PosV, "field type is not optional").
					Build())
			}
		}
	}
}

func jsonVariantTag(v *ast.Variant) (string, bool) {
	tag := v.Name
	for _, ann := range v.Annotations {
		if ann.Name != "json" {
			continue
		}
		for _, arg := range ann.Args {
			switch arg.Key {
			case "key":
				if s, ok := stringArg(arg.Value); ok {
					tag = s
				}
			case "skip":
				return tag, true
			}
		}
	}
	return tag, false
}

func jsonFieldTypeAllowsOptional(t ast.Type) bool {
	switch t := t.(type) {
	case *ast.OptionalType:
		return true
	case *ast.NamedType:
		return len(t.Path) == 1 && t.Path[0] == "Option"
	default:
		return false
	}
}

func (r *resolver) checkDuplicateMethods(methods []*ast.FnDecl) {
	checkDuplicateNamed(r, methods, "method",
		"v0.2 R19: methods spread across partial declarations must have unique names")
}

// namedNode is the small intersection of ast.Node types needed by the
// duplicate-detection helper: a name and a rendering span.
type namedNode interface {
	comparable
	ast.Node
}

func checkDuplicateNamed[T namedNode](r *resolver, items []T, noun, note string) {
	if len(items) < 2 {
		return
	}
	type record struct {
		item T
		name string
	}
	seen := map[string]record{}
	for _, it := range items {
		name := nameOf(it)
		if prev, ok := seen[name]; ok {
			b := diag.New(diag.Error,
				fmt.Sprintf("%s `%s` is declared more than once", noun, name)).
				Code(diag.CodeDuplicateDecl).
				Primary(diag.Span{Start: it.Pos(), End: it.End()},
					fmt.Sprintf("duplicate %s here", noun)).
				Secondary(diag.Span{Start: prev.item.Pos(), End: prev.item.End()},
					"previous declaration here")
			if note != "" {
				b.Note(note)
			}
			r.emit(b.Build())
			continue
		}
		seen[name] = record{item: it, name: name}
	}
}

// nameOf extracts the textual name from an AST node whose concrete type
// is one of the members the duplicate checker handles.
func nameOf(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Field:
		return v.Name
	case *ast.Variant:
		return v.Name
	case *ast.FnDecl:
		return v.Name
	}
	return ""
}

// resolveTypeBody is the shared walker for struct/enum/interface
// declarations. It opens a scope named after the type, declares the
// generic parameters, binds `Self` to the declared type, invokes the
// kind-specific body callback, and restores state on exit.
func (r *resolver) resolveTypeBody(
	name, kind string,
	pos token.Pos,
	decl ast.Node,
	generics []*ast.GenericParam,
	body func(selfSym *Symbol),
) {
	scope := NewScope(r.current, kind+":"+name)
	r.withScope(scope, func() {
		r.declareGenerics(generics)
		// The canonical Symbol for the type lives in pkgScope (all
		// top-level declarations do in multi-file mode, and single-file
		// mode delegates to ResolvePackage so pkgScope is always set).
		var selfSym *Symbol
		if r.pkgScope != nil {
			selfSym = r.pkgScope.LookupLocal(name)
		}
		if selfSym == nil {
			// Fallback: resolve through the current scope chain. Handles
			// any future path where types are nested inside an inner
			// scope (e.g. generic-parameter bounds referencing a sibling).
			selfSym = r.current.Lookup(name)
		}
		restore := r.enterTypeBody(selfSym)
		defer restore()
		r.defineLocal("Self", &Symbol{
			Name: "Self", Kind: SymTypeAlias, Pos: pos, Decl: decl,
		})
		body(selfSym)
	})
}

func (r *resolver) resolveTypeAliasDecl(t *ast.TypeAliasDecl) {
	scope := NewScope(r.current, "type:"+t.Name)
	r.withScope(scope, func() {
		r.declareGenerics(t.Generics)
		r.resolveType(t.Target)
	})
}

// ---- Block / statement / expression ----

func (r *resolver) resolveBlock(b *ast.Block) {
	scope := NewScope(r.current, "block")
	r.withScope(scope, func() {
		for _, s := range b.Stmts {
			r.resolveStmt(s)
		}
	})
}

func (r *resolver) resolveStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.LetStmt:
		// Resolve RHS first — bindings introduced by the pattern are NOT
		// in scope inside the value (`let x = x` looks up the OUTER x).
		if n.Type != nil {
			r.resolveType(n.Type)
		}
		if n.Value != nil {
			r.resolveExpr(n.Value)
		}
		r.bindPattern(n.Pattern)
	case *ast.ExprStmt:
		r.resolveExpr(n.X)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			r.resolveExpr(t)
		}
		r.resolveExpr(n.Value)
	case *ast.ChanSendStmt:
		r.resolveExpr(n.Channel)
		r.resolveExpr(n.Value)
	case *ast.ReturnStmt:
		if !r.flowCtx.inFn {
			r.errorf(n.PosV, diag.CodeReturnOutsideFn,
				"`return` is only valid inside a function body")
		}
		if n.Value != nil {
			r.resolveExpr(n.Value)
		}
	case *ast.BreakStmt:
		if !r.flowCtx.inLoop {
			r.errorf(n.PosV, diag.CodeBreakOutsideLoop,
				"`break` is only valid inside a `for` loop")
		}
		r.resolveLoopLabelRef(n.Label, n.LabelPos, n.LabelEnd)
		if n.Value != nil {
			r.resolveExpr(n.Value)
		}
	case *ast.ContinueStmt:
		if !r.flowCtx.inLoop {
			r.errorf(n.PosV, diag.CodeContinueOutsideLoop,
				"`continue` is only valid inside a `for` loop")
		}
		r.resolveLoopLabelRef(n.Label, n.LabelPos, n.LabelEnd)
	case *ast.DeferStmt:
		if !r.flowCtx.inFn {
			r.errorf(n.PosV, diag.CodeDeferOutsideFn,
				"`defer` is only valid inside a function body")
		}
		r.resolveExpr(n.X)
	case *ast.ForStmt:
		r.resolveForStmt(n)
	case *ast.Block:
		r.resolveBlock(n)
	}
}

func (r *resolver) resolveForStmt(f *ast.ForStmt) {
	scope := NewScope(r.current, "for")
	r.withScope(scope, func() {
		// Resolve the iterator/condition first; the loop pattern's bindings
		// are not visible in the iterator (matches `let` semantics).
		if f.Iter != nil {
			r.resolveExpr(f.Iter)
		}
		if f.Pattern != nil {
			r.bindPattern(f.Pattern)
		}
		if f.Body != nil {
			r.checkLoopLabelShadow(f.Label, f.LabelPos, f.LabelEnd)
			restore := r.enterLoop(f.Label, f.LabelPos, f.LabelEnd)
			for _, s := range f.Body.Stmts {
				r.resolveStmt(s)
			}
			restore()
		}
	})
}

func (r *resolver) resolveExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Ident:
		r.resolveIdent(n)
	case *ast.IntLit, *ast.FloatLit, *ast.CharLit, *ast.ByteLit, *ast.BoolLit:
		// nothing to resolve
	case *ast.StringLit:
		for _, p := range n.Parts {
			if !p.IsLit && p.Expr != nil {
				r.resolveExpr(p.Expr)
			}
		}
	case *ast.UnaryExpr:
		r.resolveExpr(n.X)
	case *ast.BinaryExpr:
		r.resolveExpr(n.Left)
		r.resolveExpr(n.Right)
	case *ast.QuestionExpr:
		r.resolveExpr(n.X)
	case *ast.CallExpr:
		// A FieldExpr used as the callee is usually a method call, and
		// we skip member resolution for it (the type checker handles
		// method dispatch). The EXCEPTION is a package reference like
		// `pkg.fn(args)`: the base resolves to SymPackage, and the
		// member is a top-level export we CAN look up right now.
		if fx, ok := n.Fn.(*ast.FieldExpr); ok {
			r.resolveExpr(fx.X)
			if id, ok := fx.X.(*ast.Ident); ok {
				if sym := r.refs[id]; sym != nil && sym.Kind == SymPackage {
					r.lookupPackageMember(sym, fx.Name, fx.EndV /*typePos*/, false)
				}
			}
		} else {
			r.resolveExpr(n.Fn)
		}
		for _, a := range n.Args {
			r.resolveExpr(a.Value)
		}
	case *ast.FieldExpr:
		r.resolveExpr(n.X)
		// `pkg.Name` on a bare package identifier — look the member up
		// in the target package's exported scope. This is the one
		// member-access shape the name resolver can handle without type
		// info; actual struct/instance member access stays deferred to
		// the type checker.
		if id, ok := n.X.(*ast.Ident); ok {
			if sym := r.refs[id]; sym != nil && sym.Kind == SymPackage {
				r.resolvePackageMember(n, sym)
				return
			}
		}
		// v0.2 §2.6.2: interface default method bodies may not access
		// fields on `self`. We detect the textually-visible pattern
		// `self.name`; the type checker will refine this once member
		// resolution is implemented.
		if r.flowCtx.inIfaceDefault && !n.IsOptional {
			if id, ok := n.X.(*ast.Ident); ok && id.Name == "self" {
				r.emit(diag.New(diag.Error,
					"interface default method bodies may not access fields on `self`").
					Code(diag.CodeInterfaceDefaultField).
					Primary(diag.Span{Start: n.PosV, End: n.EndV},
						"field access on `self` here").
					Note("v0.2 §2.6.2: default bodies may call other methods on the interface but may not read fields — the interface does not know the implementing type's field layout").
					Hint("expose the value through an interface method and call that instead").
					Build())
			}
		}
	case *ast.IndexExpr:
		r.resolveExpr(n.X)
		r.resolveExpr(n.Index)
	case *ast.TurbofishExpr:
		r.resolveExpr(n.Base)
		for _, t := range n.Args {
			r.resolveType(t)
		}
	case *ast.RangeExpr:
		r.resolveExpr(n.Start)
		r.resolveExpr(n.Stop)
	case *ast.ParenExpr:
		r.resolveExpr(n.X)
	case *ast.TupleExpr:
		for _, x := range n.Elems {
			r.resolveExpr(x)
		}
	case *ast.ListExpr:
		for _, x := range n.Elems {
			r.resolveExpr(x)
		}
	case *ast.MapExpr:
		for _, e := range n.Entries {
			r.resolveExpr(e.Key)
			r.resolveExpr(e.Value)
		}
	case *ast.StructLit:
		r.resolveExpr(n.Type)
		if n.Spread != nil {
			r.resolveExpr(n.Spread)
		}
		for _, f := range n.Fields {
			if f.Value != nil {
				r.resolveExpr(f.Value)
			}
		}
	case *ast.IfExpr:
		r.resolveIfExpr(n)
	case *ast.MatchExpr:
		r.resolveMatchExpr(n)
	case *ast.LoopExpr:
		if n.Body != nil {
			r.checkLoopLabelShadow(n.Label, n.LabelPos, n.LabelEnd)
			restore := r.enterLoop(n.Label, n.LabelPos, n.LabelEnd)
			r.resolveBlock(n.Body)
			restore()
		}
	case *ast.ClosureExpr:
		r.resolveClosure(n)
	case *ast.Block:
		r.resolveBlock(n)
	}
}

func (r *resolver) resolveIdent(id *ast.Ident) {
	if id.Name == "_" {
		r.emit(diag.New(diag.Error,
			"`_` is only valid as a pattern wildcard, not as an expression").
			Code(diag.CodeWildcardInExpr).
			PrimaryPos(id.PosV, "wildcard used as a value").
			Hint("use `let _ = expr` to ignore a value").
			Build())
		return
	}
	if id.Name == "<error>" {
		return
	}
	// `self` requires special handling: it is not a normal identifier.
	if id.Name == "self" {
		if !r.methodCtx.inMethod || r.methodCtx.selfSym == nil {
			r.errorf(id.PosV, diag.CodeSelfOutsideMethod,
				"`self` is only valid inside a method body")
			return
		}
		r.refs[id] = r.methodCtx.selfSym
		return
	}
	if id.Name == "Self" {
		if r.methodCtx.selfType == nil {
			r.errorf(id.PosV, diag.CodeSelfTypeOutside,
				"`Self` is only valid inside a `struct`, `enum`, or `interface` body")
			return
		}
		r.refs[id] = r.methodCtx.selfType
		return
	}
	sym := r.current.Lookup(id.Name)
	if sym == nil {
		r.emitUndefined(id)
		return
	}
	r.refs[id] = sym
}

// emitUndefined produces the canonical "undefined name" diagnostic, with
// a hint that scans nearby symbols for a typo suggestion.
func (r *resolver) emitUndefined(id *ast.Ident) {
	d := diag.New(diag.Error,
		fmt.Sprintf("undefined name `%s`", id.Name)).
		Code(diag.CodeUndefinedName).
		PrimaryPos(id.PosV, "not in scope")
	if hint := r.suggestSimilar(id.Name); hint != "" {
		d.Hint(fmt.Sprintf("did you mean `%s`?", hint))
	}
	r.emit(d.Build())
}

// suggestSimilar walks the visible scope chain and returns the name
// closest to `name` within a Levenshtein distance of 2. Returns "" if
// nothing qualifies. Short-circuits as soon as a distance-1 match is
// found (nothing beats that short of an exact match, which means
// Lookup would have succeeded).
func (r *resolver) suggestSimilar(name string) string {
	best := ""
	bestDist := 3
	var buf1, buf2 []int
	for cur := r.current; cur != nil; cur = cur.parent {
		for n := range cur.syms {
			// Cheap length-difference pre-filter. `bestDist` tightens
			// as we find closer matches so the search narrows quickly.
			if diff := len(n) - len(name); diff > bestDist-1 || -diff > bestDist-1 {
				continue
			}
			d := levenshteinBounded(name, n, bestDist, &buf1, &buf2)
			if d < bestDist {
				best = n
				bestDist = d
				if bestDist == 1 {
					return best
				}
			}
		}
	}
	return best
}

// levenshteinBounded computes edit distance between a and b, giving up
// early when the best row minimum exceeds `limit`. The caller provides
// two reusable int slices so the work doesn't reallocate between calls.
// Returns `limit` when the bound is exceeded.
func levenshteinBounded(a, b string, limit int, pbuf1, pbuf2 *[]int) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la-lb >= limit || lb-la >= limit {
		return limit
	}

	prev := growInts(pbuf1, lb+1)
	cur := growInts(pbuf2, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := prev[j] + 1
			if x := cur[j-1] + 1; x < d {
				d = x
			}
			if x := prev[j-1] + cost; x < d {
				d = x
			}
			cur[j] = d
			if d < rowMin {
				rowMin = d
			}
		}
		if rowMin >= limit {
			return limit
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

// growInts resizes (*p) to at least n entries, reusing the backing
// array when possible, and returns the slice zeroed up to n.
func growInts(p *[]int, n int) []int {
	if cap(*p) < n {
		*p = make([]int, n)
		return *p
	}
	s := (*p)[:n]
	for i := range s {
		s[i] = 0
	}
	return s
}

func (r *resolver) resolveIfExpr(n *ast.IfExpr) {
	if n.IsIfLet {
		// `if let pat = rhs { ... }` — pattern is bound for the THEN
		// block only.
		r.resolveExpr(n.Cond)
		scope := NewScope(r.current, "if-let")
		r.withScope(scope, func() {
			r.bindPattern(n.Pattern)
			r.resolveBlock(n.Then)
		})
	} else {
		r.resolveExpr(n.Cond)
		r.resolveBlock(n.Then)
	}
	if n.Else != nil {
		r.resolveExpr(n.Else)
	}
}

func (r *resolver) resolveMatchExpr(n *ast.MatchExpr) {
	r.resolveExpr(n.Scrutinee)
	for _, arm := range n.Arms {
		scope := NewScope(r.current, "match-arm")
		r.withScope(scope, func() {
			r.bindPattern(arm.Pattern)
			if arm.Guard != nil {
				r.resolveExpr(arm.Guard)
			}
			r.resolveExpr(arm.Body)
		})
	}
}

func (r *resolver) resolveClosure(c *ast.ClosureExpr) {
	scope := NewScope(r.current, "closure")
	r.withScope(scope, func() {
		for _, p := range c.Params {
			if p.Type != nil {
				r.resolveType(p.Type)
			}
			if p.Pattern != nil {
				r.bindPattern(p.Pattern)
			} else if p.Name != "" {
				r.defineLocal(p.Name, &Symbol{
					Name: p.Name, Kind: SymParam, Pos: p.PosV, Decl: p,
				})
			}
		}
		if c.ReturnType != nil {
			r.resolveType(c.ReturnType)
		}
		// Closures are function-like: `break` belongs to a loop inside
		// THIS closure, `return` / `defer` target the closure itself.
		// enterFn also wipes `inIfaceDefault`, so a closure nested in
		// a default body isn't spuriously subject to the field rule.
		restore := r.enterFn(false)
		r.resolveExpr(c.Body)
		restore()
	})
}

// ---- Patterns ----

// bindOrPattern implements v0.2 §4.3.1: every alternative of `A | B | C`
// must bind the same names. Each alternative is walked in a disposable
// scope so bindings don't leak and per-alternative duplicates still error;
// the intersection of all alternatives is then committed to the real
// scope. Names bound by only some alternatives produce a diagnostic, in
// stable source order across runs.
func (r *resolver) bindOrPattern(p *ast.OrPat) {
	if len(p.Alts) == 0 {
		return
	}
	altSets := make([]map[string]*Symbol, len(p.Alts))
	realScope := r.current
	// Or-alt scopes are throwaway: they exist only to detect
	// per-alternative duplicates. NewScope registers them as children of
	// realScope, so trim them off afterward to keep the scope tree (used
	// by `osty resolve --scopes`) free of internal bookkeeping scopes.
	childrenBefore := len(realScope.children)
	for i, alt := range p.Alts {
		tmp := NewScope(realScope, "or-alt")
		r.current = tmp
		r.bindPattern(alt)
		r.current = realScope
		altSets[i] = tmp.syms
	}
	realScope.children = realScope.children[:childrenBefore]

	// Count how many alternatives each name appears in. Names that hit
	// the full count are shared; anything less is an error.
	counts := map[string]int{}
	firstSym := map[string]*Symbol{}
	var namesInOrder []string
	for _, set := range altSets {
		keys := sortedKeys(set)
		for _, name := range keys {
			if _, seen := counts[name]; !seen {
				namesInOrder = append(namesInOrder, name)
				firstSym[name] = set[name]
			}
			counts[name]++
		}
	}
	n := len(altSets)
	for _, name := range namesInOrder {
		sym := firstSym[name]
		if counts[name] == n {
			realScope.DefineForce(sym)
			continue
		}
		r.emit(diag.New(diag.Error,
			fmt.Sprintf("name `%s` is not bound by every alternative of the or-pattern", name)).
			Code(diag.CodeOrPatternBindingMismatch).
			PrimaryPos(sym.Pos, "bound here").
			Note("v0.2 §4.3.1: every alternative of an or-pattern must bind the same names with the same types").
			Hint(fmt.Sprintf("add `%s` to the other alternatives or remove it from this one", name)).
			Build())
	}
}

// sortedKeys returns the keys of m in lexicographic order. Used to make
// map iteration deterministic where diagnostic order matters.
func sortedKeys(m map[string]*Symbol) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// bindPattern walks a pattern, defining all introduced bindings and
// resolving any literal / variant references it contains.
func (r *resolver) bindPattern(p ast.Pattern) {
	if p == nil {
		return
	}
	switch n := p.(type) {
	case *ast.WildcardPat:
		// nothing to bind
	case *ast.LiteralPat:
		r.resolveExpr(n.Literal)
	case *ast.IdentPat:
		// An IdentPat with an uppercase first letter could be a bare
		// variant ref (`None`, `Empty`); otherwise it introduces a new
		// binding.
		if isUpper(n.Name) {
			if sym := r.current.Lookup(n.Name); sym != nil && sym.Kind == SymVariant {
				// Variant reference — no binding introduced.
				return
			}
			// Unknown uppercase name in pattern: fall through to bind it.
		}
		r.defineLocal(n.Name, &Symbol{
			Name: n.Name, Kind: SymLet, Pos: n.PosV, Decl: n,
		})
	case *ast.TuplePat:
		for _, elem := range n.Elems {
			r.bindPattern(elem)
		}
	case *ast.StructPat:
		// Resolve the type path.
		r.resolvePath(n.Type, n.PosV)
		for _, f := range n.Fields {
			if f.Pattern != nil {
				r.bindPattern(f.Pattern)
			} else {
				// Shorthand: `Point { x, y }` introduces `x` and `y`.
				r.defineLocal(f.Name, &Symbol{
					Name: f.Name, Kind: SymLet, Pos: f.PosV, Decl: f,
				})
			}
		}
	case *ast.VariantPat:
		// First path segment must resolve to a variant (or to an enum
		// type when the pattern is qualified like `Color.Red`).
		r.resolvePath(n.Path, n.PosV)
		for _, a := range n.Args {
			r.bindPattern(a)
		}
	case *ast.RangePat:
		r.resolveExpr(n.Start)
		r.resolveExpr(n.Stop)
	case *ast.OrPat:
		r.bindOrPattern(n)
	case *ast.BindingPat:
		r.defineLocal(n.Name, &Symbol{
			Name: n.Name, Kind: SymLet, Pos: n.PosV, Decl: n,
		})
		r.bindPattern(n.Pattern)
	}
}

func isUpper(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

// resolvePath looks up a dotted name like `Color.Red`. The first segment
// is resolved against the current scope; subsequent segments are deferred
// (member access — handled once type info is available). Returns the
// head symbol if resolution succeeded, or nil.
func (r *resolver) resolvePath(path []string, pos token.Pos) *Symbol {
	if len(path) == 0 {
		return nil
	}
	first := path[0]
	sym := r.current.Lookup(first)
	if sym == nil {
		r.errorf(pos, diag.CodeUndefinedName,
			"undefined name `%s`", first)
		return nil
	}
	return sym
}

// ---- Types ----

func (r *resolver) resolveType(t ast.Type) {
	if t == nil {
		return
	}
	switch n := t.(type) {
	case *ast.NamedType:
		first := n.Path[0]
		switch {
		case first == "Self":
			if r.methodCtx.selfType == nil {
				r.errorf(n.PosV, diag.CodeSelfTypeOutside,
					"`Self` is only valid inside a `struct`, `enum`, or `interface` body")
			} else {
				r.typeRefs[n] = r.methodCtx.selfType
			}
		default:
			sym, shadow := r.current.LookupTypeHead(first)
			switch {
			case sym == nil:
				if shadow != nil {
					r.errorf(n.PosV, diag.CodeWrongSymbolKind,
						"`%s` is a %s, not a type", first, shadow.Kind)
				} else {
					r.errorf(n.PosV, diag.CodeUndefinedName,
						"undefined type `%s`", first)
				}
			case sym.Kind == SymPackage && len(n.Path) >= 2:
				// `pkg.Type` — look the tail up in the target package's
				// exported scope and attach that symbol. With only one
				// segment (just `pkg`) it's a bad type position, but we
				// still record the package symbol.
				tail := n.Path[1]
				resolved := r.lookupPackageMember(sym, tail, n.PosV /*typePos*/, true)
				if resolved != nil {
					r.typeRefs[n] = resolved
				} else {
					r.typeRefs[n] = sym
				}
			default:
				r.typeRefs[n] = sym
			}
		}
		for _, a := range n.Args {
			r.resolveType(a)
		}
	case *ast.OptionalType:
		r.resolveType(n.Inner)
	case *ast.TupleType:
		for _, e := range n.Elems {
			r.resolveType(e)
		}
	case *ast.FnType:
		for _, p := range n.Params {
			r.resolveType(p)
		}
		if n.ReturnType != nil {
			r.resolveType(n.ReturnType)
		}
	}
}

// ---- Scope helpers ----

func (r *resolver) withScope(s *Scope, fn func()) {
	prev := r.current
	r.current = s
	fn()
	r.current = prev
}

func (r *resolver) defineLocal(name string, sym *Symbol) {
	if name == "_" {
		return // wildcard binding does not introduce a name
	}
	if prev := r.current.LookupLocal(name); prev != nil {
		// Inner-scope shadowing of `let` is allowed via successive blocks.
		// Within the SAME scope, redefinition is an error.
		r.duplicate(sym.Pos, name, prev)
		return
	}
	r.current.DefineForce(sym)
}

// ResolvePackageDefault resolves a single package using the standard
// prelude (NewPrelude). This is the convenience entry point for callers
// that do not share a prelude across multiple packages; internally it
// allocates a fresh prelude on each call.
//
// Callers that resolve many packages in a loop (workspace mode, CI,
// stdlib loading) should prefer Workspace.ResolveAll or explicitly
// reuse a single prelude with ResolvePackage to avoid redundant
// allocations.
func ResolvePackageDefault(pkg *Package) *PackageResult {
	return ResolvePackage(pkg, NewPrelude())
}

// ResolveFileDefault runs single-file name resolution using the
// standard prelude and the given stdlib provider. This AST-only API keeps the
// legacy Go fallback for callers that no longer have source bytes; new
// single-file entry points should prefer ResolveFileSourceDefault so the
// selfhost resolver remains authoritative.
func ResolveFileDefault(file *ast.File, stdlib StdlibProvider) *Result {
	return fileWithStdlib(file, NewPrelude(), stdlib)
}

// ResolveFileSourceDefault runs single-file resolution from source bytes and the
// already-parsed AST, using the selfhost resolver as the source of truth while
// still projecting refs and scopes back onto the Go AST.
func ResolveFileSourceDefault(src []byte, file *ast.File, stdlib StdlibProvider) *Result {
	pkg := &Package{
		Name: "<file>",
		Files: []*PackageFile{{
			Path:            "<input>",
			Source:          append([]byte(nil), src...),
			CanonicalSource: append([]byte(nil), src...),
			File:            file,
		}},
	}
	if stdlib != nil {
		pkg.workspace = newStdlibOnlyWorkspace(stdlib)
	}
	pr := ResolvePackage(pkg, NewPrelude())
	pf := pkg.Files[0]
	return &Result{
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
		Diags:         pr.Diags,
	}
}
