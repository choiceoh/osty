package ir

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// LowerFnDecl lowers a single Osty function declaration to its IR form.
//
// Unlike Lower, which consumes a whole file and its resolver/checker
// results, LowerFnDecl is the entry point for lowering one fn body in
// isolation — for example an Osty-bodied stdlib function being injected
// alongside a user module. The caller supplies the resolve and check
// results that cover fn's body; both may be nil when unavailable, with
// the same degraded behavior as Lower (identifier kinds default to
// IdentUnknown and expression types fall back to ErrTypeVal).
//
// Returns the lowered FnDecl plus any non-fatal lowering issues. A nil
// input returns (nil, nil).
func LowerFnDecl(pkgName string, fn *ast.FnDecl, res *resolve.Result, chk *check.Result) (*FnDecl, []error) {
	if fn == nil {
		return nil, nil
	}
	l := &lowerer{pkgName: pkgName, res: res, chk: chk}
	out := l.lowerFnDecl(fn)
	return out, l.issues
}

// Lower converts a type-checked Osty file into an independent IR Module.
//
// pkgName is the module's package name (e.g. "main"). res and chk may be
// nil when the caller has no resolver/checker output available — in that
// case expression types fall back to ErrTypeVal and identifier kinds are
// left as IdentUnknown, so backends that need either should pass real
// data.
//
// The returned []error is the set of non-fatal lowering issues
// encountered (unsupported constructs, missing type info); callers are
// free to ignore them or surface them via their diagnostic machinery.
// The returned Module is always non-nil — it just contains ErrorStmt /
// ErrorExpr nodes in positions that failed.
func Lower(pkgName string, file *ast.File, res *resolve.Result, chk *check.Result) (*Module, []error) {
	l := &lowerer{pkgName: pkgName, file: file, res: res, chk: chk}
	return l.run()
}

// LowerPackage lowers every file in a resolved package into a single
// Module. Top-level declarations from each file are concatenated in
// `pkg.Files` discovery order (lexicographic by path) so the merged
// module's `Decls` slice mirrors the package's source layout
// deterministically.
//
// pkgName names the resulting module — typically `pkg.Name` or "main"
// for binary packages. chk is the package-level check.Result (which
// already covers every file's expressions); each file's per-file
// resolve handles (`pf.Refs`, `pf.TypeRefs`, `pf.FileScope`) are
// reconstructed into a `resolve.Result` on the fly so the lowerer's
// existing per-file machinery keeps working.
//
// The Module's Span comes from the first file's span; non-fatal issues
// from every file are concatenated. A nil package returns a nil module
// and a single descriptive error so callers can distinguish "no work"
// from "successful empty lower". An empty Files slice returns an empty
// (but valid) module so the validator and downstream emitters can run.
func LowerPackage(pkgName string, pkg *resolve.Package, chk *check.Result) (*Module, []error) {
	if pkg == nil {
		return nil, []error{fmt.Errorf("ir.LowerPackage: nil package")}
	}
	mod := &Module{Package: pkgName}
	var issues []error
	for i, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		res := &resolve.Result{
			RefsByID:      pf.RefsByID,
			TypeRefsByID:  pf.TypeRefsByID,
			RefIdents:     pf.RefIdents,
			TypeRefIdents: pf.TypeRefIdents,
			FileScope:     pf.FileScope,
		}
		l := &lowerer{pkgName: pkgName, file: pf.File, res: res, chk: chk}
		fileMod, fileIssues := l.run()
		if i == 0 {
			mod.SpanV = fileMod.SpanV
		}
		mod.Decls = append(mod.Decls, fileMod.Decls...)
		mod.Script = append(mod.Script, fileMod.Script...)
		issues = append(issues, fileIssues...)
	}
	return mod, issues
}

// lowerer holds per-file state for one Lower call.
type lowerer struct {
	pkgName string
	file    *ast.File
	res     *resolve.Result
	chk     *check.Result

	// issues collects non-fatal issues.
	issues []error

	// bindingPatTypes records the inferred IR-side type for `let
	// p = <expr>` bindings keyed by the binding pattern node. The
	// embedded selfhost checker doesn't populate `Types[ident]` /
	// `SymTypes[sym]` for value-bound idents whose Symbol.Decl is
	// the IdentPat (rather than the LetStmt), so `lowerIdent`'s
	// last-resort fallback consults this map by walking the symbol
	// to its IdentPat and looking up the type the lowerer recorded
	// when it processed the LetStmt earlier in the same function.
	bindingPatTypes map[*ast.IdentPat]Type
}

// ==== Top level ====

func (l *lowerer) run() (*Module, []error) {
	mod := &Module{
		Package: l.pkgName,
		SpanV:   l.fileSpan(),
	}
	for _, u := range l.file.Uses {
		if lowered := l.lowerDecl(u); lowered != nil {
			mod.Decls = append(mod.Decls, lowered)
		}
	}
	for _, d := range l.file.Decls {
		if lowered := l.lowerDecl(d); lowered != nil {
			mod.Decls = append(mod.Decls, lowered)
		}
	}
	for _, s := range l.file.Stmts {
		mod.Script = append(mod.Script, l.lowerStmt(s))
	}
	return mod, l.issues
}

func (l *lowerer) fileSpan() Span {
	if l.file == nil {
		return Span{}
	}
	return Span{Start: posFromToken(l.file.PosV), End: posFromToken(l.file.EndV)}
}

// note records a non-fatal issue.
func (l *lowerer) note(format string, args ...any) {
	l.issues = append(l.issues, fmt.Errorf(format, args...))
}

// ==== Declarations ====

func (l *lowerer) lowerDecl(d ast.Decl) Decl {
	switch d := d.(type) {
	case *ast.FnDecl:
		// Methods on types are materialised inside their owning struct
		// or enum declaration. Skip them at the top level; the owner's
		// lowering picks them up through the AST's Methods slice.
		if d.Recv != nil {
			return nil
		}
		return l.lowerFnDecl(d)
	case *ast.StructDecl:
		return l.lowerStructDecl(d)
	case *ast.EnumDecl:
		return l.lowerEnumDecl(d)
	case *ast.LetDecl:
		return l.lowerLetDecl(d)
	case *ast.UseDecl:
		return l.lowerUseDecl(d)
	case *ast.InterfaceDecl:
		return l.lowerInterfaceDecl(d)
	case *ast.TypeAliasDecl:
		return l.lowerTypeAliasDecl(d)
	}
	l.note("unsupported top-level decl %T", d)
	return nil
}

func (l *lowerer) lowerFnDecl(fn *ast.FnDecl) *FnDecl {
	_, vecWidth, vecScalable, vecPredicate := extractVectorizeArgs(fn.Annotations)
	unrollEnable, unrollCount := extractUnrollArgs(fn.Annotations)
	noVec := hasNamedAnnotation(fn.Annotations, "no_vectorize")
	out := &FnDecl{
		Name:         fn.Name,
		Return:       l.lowerType(fn.ReturnType),
		ReceiverMut:  fn.Recv != nil && fn.Recv.Mut,
		Exported:     fn.Pub,
		SpanV:        nodeSpan(fn),
		ExportSymbol: extractExportSymbol(fn.Annotations),
		CABI:         hasNamedAnnotation(fn.Annotations, "c_abi"),
		IsIntrinsic:  hasNamedAnnotation(fn.Annotations, "intrinsic"),
		NoAlloc:      hasNamedAnnotation(fn.Annotations, "no_alloc"),
		// v0.6 A5.2: vectorize is default-on. `#[no_vectorize]` is
		// the sole way to opt out.
		Vectorize:          !noVec,
		NoVectorize:        noVec,
		VectorizeWidth:     vecWidth,
		VectorizeScalable:  vecScalable,
		VectorizePredicate: vecPredicate,
		Parallel:           hasNamedAnnotation(fn.Annotations, "parallel"),
		Unroll:             unrollEnable,
		UnrollCount:        unrollCount,
		InlineMode:         extractInlineMode(fn.Annotations),
		Hot:                hasNamedAnnotation(fn.Annotations, "hot"),
		Cold:               hasNamedAnnotation(fn.Annotations, "cold"),
		TargetFeatures:     extractTargetFeatures(fn.Annotations),
		Pure:               hasNamedAnnotation(fn.Annotations, "pure"),
	}
	out.NoaliasAll, out.NoaliasParams = extractNoaliasArgs(fn.Annotations)
	if out.Return == nil {
		out.Return = TUnit
	}
	for _, gp := range fn.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, fn.Name))
	}
	for _, p := range fn.Params {
		out.Params = append(out.Params, l.lowerParam(p))
	}
	if fn.Body != nil {
		out.Body = l.lowerBlock(fn.Body)
	}
	return out
}

// extractInlineMode reads the v0.6 A8 `#[inline]` family off the
// annotation list and returns the corresponding FnDecl.InlineMode
// value. Bare `#[inline]` → InlineSoft; `#[inline(always)]` →
// InlineAlways; `#[inline(never)]` → InlineNever; absent →
// InlineNone.
func extractInlineMode(annots []*ast.Annotation) int {
	for _, a := range annots {
		if a == nil || a.Name != "inline" {
			continue
		}
		if len(a.Args) == 0 {
			return InlineSoft
		}
		for _, arg := range a.Args {
			if arg == nil {
				continue
			}
			switch arg.Key {
			case "always":
				return InlineAlways
			case "never":
				return InlineNever
			}
		}
		return InlineSoft
	}
	return InlineNone
}

// extractNoaliasArgs reads `#[noalias]` / `#[noalias(p1, p2)]` and
// returns (allParams, []paramName). Bare form → (true, nil).
// Arg-list form → (false, names). Absent → (false, nil).
func extractNoaliasArgs(annots []*ast.Annotation) (bool, []string) {
	for _, a := range annots {
		if a == nil || a.Name != "noalias" {
			continue
		}
		if len(a.Args) == 0 {
			return true, nil
		}
		names := make([]string, 0, len(a.Args))
		for _, arg := range a.Args {
			if arg == nil || arg.Key == "" {
				continue
			}
			names = append(names, arg.Key)
		}
		return false, names
	}
	return false, nil
}

// extractTargetFeatures reads `#[target_feature(...)]` and returns
// each bare-identifier argument in source order, skipping empty
// keys. The resolver has already rejected malformed arg shapes, so
// we can assume well-formed names here.
func extractTargetFeatures(annots []*ast.Annotation) []string {
	var out []string
	for _, a := range annots {
		if a == nil || a.Name != "target_feature" {
			continue
		}
		for _, arg := range a.Args {
			if arg == nil || arg.Key == "" {
				continue
			}
			out = append(out, arg.Key)
		}
	}
	return out
}

// extractExportSymbol reads the `#[export("name")]` annotation from a
// declaration's annotation list (LANG_SPEC §19.6). Returns the empty
// string when the annotation is absent or malformed; the resolver's
// arg validator (`checkExportArgs` in internal/resolve) is the
// authoritative place that rejects a malformed `#[export]`, so at
// this point in the pipeline we only pick up the well-formed cases.
// hasNamedAnnotation reports whether `annots` contains an annotation
// with the given name. Used by `lowerFnDecl` for bare-flag annotations
// (`#[c_abi]`, `#[no_alloc]`, `#[intrinsic]`) where presence alone is
// the signal — argument shape is the resolver's responsibility.
func hasNamedAnnotation(annots []*ast.Annotation, name string) bool {
	for _, a := range annots {
		if a != nil && a.Name == name {
			return true
		}
	}
	return false
}

// extractVectorizeArgs reads `#[vectorize(...)]` metadata from the
// annotation list and returns (enable, width, scalable, predicate).
// `enable` tracks presence of the annotation regardless of args; the
// three other flags are set from the arg list the resolver already
// validated, so we can assume well-formed shape here.
func extractVectorizeArgs(annots []*ast.Annotation) (enable bool, width int, scalable, predicate bool) {
	for _, a := range annots {
		if a == nil || a.Name != "vectorize" {
			continue
		}
		enable = true
		for _, arg := range a.Args {
			if arg == nil {
				continue
			}
			switch arg.Key {
			case "scalable":
				scalable = true
			case "predicate":
				predicate = true
			case "width":
				if lit, ok := arg.Value.(*ast.IntLit); ok {
					if v, ok := parseAnnotationInt(lit.Text); ok {
						width = v
					}
				}
			}
		}
	}
	return
}

// extractUnrollArgs reads `#[unroll]` / `#[unroll(count = N)]` from
// the annotation list and returns (enable, count). `count == 0` means
// the bare form; a positive value means the fixed factor.
func extractUnrollArgs(annots []*ast.Annotation) (enable bool, count int) {
	for _, a := range annots {
		if a == nil || a.Name != "unroll" {
			continue
		}
		enable = true
		for _, arg := range a.Args {
			if arg == nil || arg.Key != "count" {
				continue
			}
			if lit, ok := arg.Value.(*ast.IntLit); ok {
				if v, ok := parseAnnotationInt(lit.Text); ok {
					count = v
				}
			}
		}
	}
	return
}

// parseAnnotationInt parses a decimal/hex/octal/binary integer literal
// text (with optional underscore separators) into an int. Returns
// (value, true) only for non-negative values that fit in int.
func parseAnnotationInt(text string) (int, bool) {
	text = strings.ReplaceAll(text, "_", "")
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
	if err != nil || v < 0 || v > int64(^uint(0)>>1) {
		return 0, false
	}
	return int(v), true
}

func extractExportSymbol(annots []*ast.Annotation) string {
	for _, a := range annots {
		if a == nil || a.Name != "export" {
			continue
		}
		if len(a.Args) != 1 {
			continue
		}
		arg := a.Args[0]
		if arg == nil || arg.Key != "" || arg.Value == nil {
			continue
		}
		lit, ok := arg.Value.(*ast.StringLit)
		if !ok {
			continue
		}
		var buf []byte
		for _, p := range lit.Parts {
			if !p.IsLit {
				// Interpolation is a resolver error; ignore here.
				return ""
			}
			buf = append(buf, p.Lit...)
		}
		return string(buf)
	}
	return ""
}

// jsonFieldOptions extracts `#[json(...)]` metadata from a struct
// field's annotation list. Returns (key, skip, optional) where an
// empty key means "use the field name". The resolver has already
// validated argument shape (E0407) and that `optional` only attaches
// to Option-typed fields (E0408), so this helper accepts any
// argument form silently — malformed inputs cannot reach IR.
func jsonFieldOptions(annots []*ast.Annotation) (key string, skip, optional bool) {
	for _, a := range annots {
		if a == nil || a.Name != "json" {
			continue
		}
		for _, arg := range a.Args {
			switch arg.Key {
			case "key":
				if s, ok := annotationStringLit(arg.Value); ok {
					key = s
				}
			case "skip":
				skip = true
			case "optional":
				optional = true
			}
		}
	}
	return key, skip, optional
}

// jsonVariantOptions extracts `#[json(...)]` metadata from an enum
// variant's annotation list. Returns (tag, skip); empty tag means
// "use the variant name". Only `key` and `skip` are defined for
// variants — `optional` is a field-level knob.
func jsonVariantOptions(annots []*ast.Annotation) (tag string, skip bool) {
	for _, a := range annots {
		if a == nil || a.Name != "json" {
			continue
		}
		for _, arg := range a.Args {
			switch arg.Key {
			case "key":
				if s, ok := annotationStringLit(arg.Value); ok {
					tag = s
				}
			case "skip":
				skip = true
			}
		}
	}
	return tag, skip
}

// annotationStringLit concatenates the literal parts of a string
// literal `ast.Expr`. Interpolation segments force a false return
// since the resolver forbids non-literal annotation arguments.
func annotationStringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	var buf []byte
	for _, p := range lit.Parts {
		if !p.IsLit {
			return "", false
		}
		buf = append(buf, p.Lit...)
	}
	return string(buf), true
}

func (l *lowerer) lowerParam(p *ast.Param) *Param {
	out := &Param{
		Type:  l.lowerType(p.Type),
		SpanV: nodeSpan(p),
	}
	if p.Name != "" {
		out.Name = p.Name
	} else if p.Pattern != nil {
		out.Pattern = l.lowerPattern(p.Pattern)
	}
	if p.Default != nil {
		out.Default = l.lowerExpr(p.Default)
	}
	return out
}

func (l *lowerer) lowerTypeParam(gp *ast.GenericParam, owner string) *TypeParam {
	out := &TypeParam{Name: gp.Name, SpanV: nodeSpan(gp)}
	for _, c := range gp.Constraints {
		if t := l.lowerType(c); t != nil {
			out.Bounds = append(out.Bounds, t)
		}
	}
	return out
}

func (l *lowerer) lowerStructDecl(sd *ast.StructDecl) *StructDecl {
	out := &StructDecl{
		Name:     sd.Name,
		Exported: sd.Pub,
		SpanV:    nodeSpan(sd),
		Pod:      hasNamedAnnotation(sd.Annotations, "pod"),
		ReprC:    hasNamedAnnotation(sd.Annotations, "repr"),
	}
	for _, gp := range sd.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, sd.Name))
	}
	for _, f := range sd.Fields {
		field := &Field{
			Name:     f.Name,
			Type:     l.lowerType(f.Type),
			Exported: f.Pub,
			SpanV:    nodeSpan(f),
		}
		if f.Default != nil {
			field.Default = l.lowerExpr(f.Default)
		}
		field.JSONKey, field.JSONSkip, field.JSONOptional = jsonFieldOptions(f.Annotations)
		out.Fields = append(out.Fields, field)
	}
	for _, m := range sd.Methods {
		out.Methods = append(out.Methods, l.lowerFnDecl(m))
	}
	info := check.ClassifyBuilderDerive(sd)
	out.BuilderDerivable, out.BuilderRequiredFields = info.Derivable, info.Required
	return out
}

func (l *lowerer) lowerEnumDecl(ed *ast.EnumDecl) *EnumDecl {
	out := &EnumDecl{
		Name:     ed.Name,
		Exported: ed.Pub,
		SpanV:    nodeSpan(ed),
	}
	for _, gp := range ed.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, ed.Name))
	}
	for _, v := range ed.Variants {
		variant := &Variant{Name: v.Name, SpanV: nodeSpan(v)}
		for _, ty := range v.Fields {
			variant.Payload = append(variant.Payload, l.lowerType(ty))
		}
		variant.JSONTag, variant.JSONSkip = jsonVariantOptions(v.Annotations)
		out.Variants = append(out.Variants, variant)
	}
	for _, m := range ed.Methods {
		out.Methods = append(out.Methods, l.lowerFnDecl(m))
	}
	return out
}

func (l *lowerer) lowerLetDecl(ld *ast.LetDecl) *LetDecl {
	out := &LetDecl{
		Name:     ld.Name,
		Mut:      ld.Mut,
		Exported: ld.Pub,
		SpanV:    nodeSpan(ld),
	}
	if ld.Type != nil {
		out.Type = l.lowerType(ld.Type)
	}
	if ld.Value != nil {
		out.Value = l.lowerExpr(ld.Value)
		if out.Type == nil {
			out.Type = out.Value.Type()
		}
	}
	return out
}

// ==== Types ====

// lowerType converts an AST Type to IR Type. Returns nil when the input
// is nil (caller substitutes TUnit when that matters).
func (l *lowerer) lowerType(t ast.Type) Type {
	if t == nil {
		return nil
	}
	switch t := t.(type) {
	case *ast.NamedType:
		return l.lowerNamedType(t)
	case *ast.OptionalType:
		return &OptionalType{Inner: l.lowerType(t.Inner)}
	case *ast.TupleType:
		elems := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = l.lowerType(e)
		}
		return &TupleType{Elems: elems}
	case *ast.FnType:
		params := make([]Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = l.lowerType(p)
		}
		ret := l.lowerType(t.ReturnType)
		if ret == nil {
			ret = TUnit
		}
		return &FnType{Params: params, Return: ret}
	}
	l.note("unsupported type node %T", t)
	return ErrTypeVal
}

// lowerNamedType resolves a NamedType to either a primitive, an IR
// NamedType (with Builtin flag populated from the resolver when
// available), or a TypeVar for generic parameter references.
func (l *lowerer) lowerNamedType(nt *ast.NamedType) Type {
	name := nt.Path[len(nt.Path)-1]
	pkg := ""
	if len(nt.Path) > 1 {
		pkg = joinDottedPath(nt.Path[:len(nt.Path)-1])
	}

	// Primitive scalars short-circuit — only when bare (no qualifier, no
	// type args).
	if pkg == "" && len(nt.Args) == 0 {
		if p := primitiveByName(name); p != nil {
			return p
		}
	}

	args := make([]Type, len(nt.Args))
	for i, a := range nt.Args {
		args[i] = l.lowerType(a)
	}

	// Consult the resolver for the head symbol so we can classify
	// builtins vs user declarations vs generic parameters.
	if sym := l.typeRef(nt); sym != nil {
		switch sym.Kind {
		case resolve.SymBuiltin:
			if len(nt.Args) == 0 {
				if p := primitiveByName(sym.Name); p != nil {
					return p
				}
			}
			return &NamedType{Package: pkg, Name: sym.Name, Args: args, Builtin: true}
		case resolve.SymGeneric:
			return &TypeVar{Name: sym.Name, Owner: ""}
		case resolve.SymTypeAlias:
			// Unwrap the alias at IR construction time. Without this,
			// downstream passes (MIR generator's typeSupported,
			// mono's substitution) see the user-declared name as an
			// opaque NamedType with no layout, and fail with
			// `unsupported local type <alias>`. Aliases are pure
			// syntactic sugar in Osty (§3.A type system), so the
			// target type is semantically identical — follow it.
			if aliasDecl, ok := sym.Decl.(*ast.TypeAliasDecl); ok && aliasDecl != nil && aliasDecl.Target != nil {
				return l.lowerType(aliasDecl.Target)
			}
		}
		return &NamedType{Package: pkg, Name: sym.Name, Args: args}
	}

	// No resolver data available — best effort on the source name.
	if pkg == "" {
		switch name {
		case "List", "Map", "Set", "Option", "Result":
			return &NamedType{Package: "", Name: name, Args: args, Builtin: true}
		}
	}
	return &NamedType{Package: pkg, Name: name, Args: args}
}

// joinDottedPath joins a non-empty string slice with '.'.
func joinDottedPath(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "." + p
	}
	return out
}

func (l *lowerer) typeRef(nt *ast.NamedType) *resolve.Symbol {
	if l.res == nil || nt == nil {
		return nil
	}
	return l.res.TypeRefsByID[nt.ID]
}

// primitiveByName maps a scalar type name to the IR singleton.
func primitiveByName(name string) *PrimType {
	switch name {
	case "Int":
		return TInt
	case "Int8":
		return TInt8
	case "Int16":
		return TInt16
	case "Int32":
		return TInt32
	case "Int64":
		return TInt64
	case "UInt8":
		return TUInt8
	case "UInt16":
		return TUInt16
	case "UInt32":
		return TUInt32
	case "UInt64":
		return TUInt64
	case "Byte":
		return TByte
	case "Float":
		return TFloat
	case "Float32":
		return TFloat32
	case "Float64":
		return TFloat64
	case "Bool":
		return TBool
	case "Char":
		return TChar
	case "String":
		return TString
	case "Bytes":
		return TBytes
	case "RawPtr":
		return TRawPtr
	}
	return nil
}

// fromCheckerType converts a checker types.Type into an IR Type. Used
// when lowering expressions where the checker's inferred type is the
// authoritative source.
func (l *lowerer) fromCheckerType(t types.Type) Type {
	if t == nil {
		return nil
	}
	switch t := t.(type) {
	case *types.Primitive:
		if p := primitiveByKind(t.Kind); p != nil {
			return p
		}
		return ErrTypeVal
	case *types.Untyped:
		return l.fromCheckerType(t.Default())
	case *types.Tuple:
		elems := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = l.fromCheckerType(e)
		}
		return &TupleType{Elems: elems}
	case *types.Optional:
		return &OptionalType{Inner: l.fromCheckerType(t.Inner)}
	case *types.FnType:
		params := make([]Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = l.fromCheckerType(p)
		}
		ret := l.fromCheckerType(t.Return)
		if ret == nil {
			ret = TUnit
		}
		return &FnType{Params: params, Return: ret}
	case *types.Named:
		name := "?"
		builtin := false
		pkg := ""
		if t.Sym != nil {
			name = t.Sym.Name
			builtin = t.Sym.Kind == resolve.SymBuiltin
			if t.Sym.Package != nil {
				pkg = t.Sym.Package.Name
			}
			// Unwrap type aliases at checker → IR boundary. Mirrors the
			// same unwrap in lowerNamedType (AST → IR): without it, any
			// expression whose checker-inferred type is a user alias
			// (e.g. `CheckName = String`) produces a NamedType that
			// MIR's typeSupported rejects with
			// `unsupported local type <alias>`. Aliases are pure
			// syntactic sugar — the target type is semantically
			// identical.
			if t.Sym.Kind == resolve.SymTypeAlias {
				if aliasDecl, ok := t.Sym.Decl.(*ast.TypeAliasDecl); ok && aliasDecl != nil && aliasDecl.Target != nil {
					return l.lowerType(aliasDecl.Target)
				}
			}
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = l.fromCheckerType(a)
		}
		return &NamedType{Package: pkg, Name: name, Args: args, Builtin: builtin}
	case *types.TypeVar:
		name := "?"
		if t.Sym != nil {
			name = t.Sym.Name
		}
		return &TypeVar{Name: name}
	case *types.Error:
		return ErrTypeVal
	}
	l.note("unsupported checker type %T", t)
	return ErrTypeVal
}

func primitiveByKind(k types.PrimitiveKind) *PrimType {
	switch k {
	case types.PInt:
		return TInt
	case types.PInt8:
		return TInt8
	case types.PInt16:
		return TInt16
	case types.PInt32:
		return TInt32
	case types.PInt64:
		return TInt64
	case types.PUInt8:
		return TUInt8
	case types.PUInt16:
		return TUInt16
	case types.PUInt32:
		return TUInt32
	case types.PUInt64:
		return TUInt64
	case types.PByte:
		return TByte
	case types.PFloat:
		return TFloat
	case types.PFloat32:
		return TFloat32
	case types.PFloat64:
		return TFloat64
	case types.PBool:
		return TBool
	case types.PChar:
		return TChar
	case types.PString:
		return TString
	case types.PBytes:
		return TBytes
	case types.PRawPtr:
		return TRawPtr
	case types.PUnit:
		return TUnit
	case types.PNever:
		return TNever
	}
	return nil
}

// ==== Blocks and statements ====

func (l *lowerer) lowerBlock(b *ast.Block) *Block {
	out := &Block{SpanV: nodeSpan(b)}
	if len(b.Stmts) == 0 {
		return out
	}
	// The block's "result" is the final expression statement, if any,
	// and if its type is not unit. This matches the checker's view of
	// block-as-expression and lets backends omit an extra Go statement
	// when no value is implicitly returned.
	last := b.Stmts[len(b.Stmts)-1]
	lead := b.Stmts[:len(b.Stmts)-1]
	for _, s := range lead {
		out.Stmts = append(out.Stmts, l.lowerStmt(s))
	}
	if es, ok := last.(*ast.ExprStmt); ok && l.expressionYieldsValue(es.X) {
		out.Result = l.lowerExpr(es.X)
		return out
	}
	out.Stmts = append(out.Stmts, l.lowerStmt(last))
	return out
}

// expressionYieldsValue reports whether treating the expression as a
// block-final implicit result is appropriate: the checker assigned it
// a non-unit type, or — when the checker didn't cover this surface
// — its syntactic shape unambiguously evaluates to a value.
func (l *lowerer) expressionYieldsValue(e ast.Expr) bool {
	if l.chk == nil {
		// No checker; be conservative: don't promote.
		return false
	}
	if t := l.chk.Types[e]; t != nil {
		if p, ok := t.(*types.Primitive); ok {
			if p.Kind == types.PUnit || p.Kind == types.PNever {
				return false
			}
		}
		if _, ok := t.(*types.Error); !ok {
			return true
		}
		// Error type: fall through to the syntactic fallback so
		// non-error literal shapes still promote when the checker
		// didn't infer them.
	}
	// Syntactic fallback: the embedded selfhost checker doesn't
	// always populate `Types[e]` for expressions in tail-of-block
	// position (notably `StructLit` interior surfaces). For shapes
	// that always evaluate to a value of definite non-unit type,
	// promote them to `Block.Result` even when the checker entry is
	// missing or error-typed — otherwise `mir.Lower` treats the body
	// as returning Unit and emits `unreachable` in place of the
	// value return, producing IR that fails LLVM verification for
	// any non-Unit return type. Restricted to shapes that always
	// evaluate to a value (no Block / IfExpr / MatchExpr — those
	// have their own promotion paths).
	switch e.(type) {
	case *ast.StructLit, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
		*ast.CharLit, *ast.BoolLit, *ast.ListExpr, *ast.MapExpr,
		*ast.TupleExpr, *ast.RangeExpr:
		return true
	}
	return false
}

func (l *lowerer) lowerStmt(s ast.Stmt) Stmt {
	switch s := s.(type) {
	case *ast.Block:
		return l.lowerBlock(s)
	case *ast.LetStmt:
		return l.lowerLetStmt(s)
	case *ast.ExprStmt:
		switch x := s.X.(type) {
		case *ast.IfExpr:
			if !x.IsIfLet {
				return l.lowerIfStmt(x)
			}
		case *ast.MatchExpr:
			return l.lowerMatchStmt(x)
		}
		x := l.lowerExpr(s.X)
		return &ExprStmt{X: x, SpanV: Span{Start: posFromToken(s.Pos()), End: posFromToken(s.End())}}
	case *ast.ReturnStmt:
		out := &ReturnStmt{SpanV: nodeSpan(s)}
		if s.Value != nil {
			out.Value = l.lowerExpr(s.Value)
		}
		return out
	case *ast.BreakStmt:
		return &BreakStmt{Label: s.Label, SpanV: nodeSpan(s)}
	case *ast.ContinueStmt:
		return &ContinueStmt{Label: s.Label, SpanV: nodeSpan(s)}
	case *ast.AssignStmt:
		return l.lowerAssignStmt(s)
	case *ast.ForStmt:
		return l.lowerForStmt(s)
	case *ast.DeferStmt:
		return l.lowerDeferStmt(s)
	case *ast.ChanSendStmt:
		return &ChanSendStmt{
			Channel: l.lowerExpr(s.Channel),
			Value:   l.lowerExpr(s.Value),
			SpanV:   nodeSpan(s),
		}
	}
	// Fall through: if it's actually an if used at statement position,
	// the parser wrapped it in an ExprStmt already; we only get here
	// for deferred constructs.
	l.note("unsupported statement %T at %v", s, s.Pos())
	return &ErrorStmt{Note: fmt.Sprintf("%T", s), SpanV: nodeSpan(s)}
}

func (l *lowerer) lowerLetStmt(s *ast.LetStmt) Stmt {
	out := &LetStmt{
		Mut:   s.Mut,
		SpanV: nodeSpan(s),
	}
	if name, ok := simpleBindName(s.Pattern); ok {
		out.Name = name
	} else {
		out.Pattern = l.lowerPattern(s.Pattern)
	}
	if s.Type != nil {
		out.Type = l.lowerType(s.Type)
	}
	if s.Value != nil {
		out.Value = l.lowerExpr(s.Value)
		if out.Type == nil {
			out.Type = out.Value.Type()
		}
	}
	// Record the inferred binding-pattern type so later references
	// to the same name in this function can recover their type via
	// the resolver Symbol → IdentPat → recorded type chain. The
	// embedded selfhost checker doesn't always populate `Types[lit]`
	// for the let RHS, so when the IR-side `out.Type` is poisoned we
	// fall back to deriving the type from the AST shape directly
	// (StructLit head ident → `&NamedType{Name: ...}`). Only the
	// bare `let x = <expr>` shape is recorded; richer patterns
	// (tuple / struct destructure) carry a different per-element
	// shape and would need a structured recovery pass.
	if ip, ok := s.Pattern.(*ast.IdentPat); ok && ip != nil {
		recorded := out.Type
		if recorded == nil || recorded == ErrTypeVal {
			recorded = bindingTypeFromAST(s.Value)
		}
		if recorded != nil && recorded != ErrTypeVal {
			if l.bindingPatTypes == nil {
				l.bindingPatTypes = map[*ast.IdentPat]Type{}
			}
			l.bindingPatTypes[ip] = recorded
		}
	}
	return out
}

// bindingTypeFromAST derives a syntactic IR Type from a let RHS
// expression when the checker hasn't populated the per-node Types
// map. Conservative: only handles shapes whose syntactic head names
// the type unambiguously (StructLit, Type-named call). Returns nil
// for anything else.
func bindingTypeFromAST(e ast.Expr) Type {
	switch n := e.(type) {
	case *ast.StructLit:
		if n == nil {
			return nil
		}
		switch h := n.Type.(type) {
		case *ast.Ident:
			if h.Name != "" {
				return &NamedType{Name: h.Name}
			}
		case *ast.FieldExpr:
			if h.Name != "" {
				return &NamedType{Name: h.Name}
			}
		}
	case *ast.ParenExpr:
		return bindingTypeFromAST(n.X)
	}
	return nil
}

// simpleBindName returns (name, true) when the pattern is just a bare
// IdentPattern.
func simpleBindName(p ast.Pattern) (string, bool) {
	ip, ok := p.(*ast.IdentPat)
	if !ok {
		return "", false
	}
	return ip.Name, true
}

func (l *lowerer) lowerAssignStmt(s *ast.AssignStmt) Stmt {
	out := &AssignStmt{
		Op:    assignOp(s.Op),
		Value: l.lowerExpr(s.Value),
		SpanV: nodeSpan(s),
	}
	for _, t := range s.Targets {
		out.Targets = append(out.Targets, l.lowerExpr(t))
	}
	return out
}

func (l *lowerer) lowerForStmt(s *ast.ForStmt) Stmt {
	body := l.lowerBlock(s.Body)
	// Classify: infinite | while | for-in (range or iterator).
	if s.Pattern == nil && s.Iter == nil {
		return &ForStmt{Kind: ForInfinite, Label: s.Label, Body: body, SpanV: nodeSpan(s)}
	}
	if s.Pattern == nil && s.Iter != nil {
		return &ForStmt{Kind: ForWhile, Label: s.Label, Cond: l.lowerExpr(s.Iter), Body: body, SpanV: nodeSpan(s)}
	}
	var loopVar string
	var loopPat Pattern
	if name, ok := simpleBindName(s.Pattern); ok {
		loopVar = name
	} else {
		loopPat = l.lowerPattern(s.Pattern)
	}
	// for x in a..b is a numeric range loop.
	if r, ok := s.Iter.(*ast.RangeExpr); ok && r.Start != nil && r.Stop != nil {
		return &ForStmt{
			Kind:      ForRange,
			Label:     s.Label,
			Var:       loopVar,
			Pattern:   loopPat,
			Start:     l.lowerExpr(r.Start),
			End:       l.lowerExpr(r.Stop),
			Inclusive: r.Inclusive,
			Body:      body,
			SpanV:     nodeSpan(s),
		}
	}
	return &ForStmt{
		Kind:    ForIn,
		Label:   s.Label,
		Var:     loopVar,
		Pattern: loopPat,
		Iter:    l.lowerExpr(s.Iter),
		Body:    body,
		SpanV:   nodeSpan(s),
	}
}

func (l *lowerer) lowerIfStmt(e *ast.IfExpr) Stmt {
	return &IfStmt{
		Cond:  l.lowerExpr(e.Cond),
		Then:  l.lowerBlock(e.Then),
		Else:  l.lowerElseStmt(e.Else),
		SpanV: nodeSpan(e),
	}
}

func (l *lowerer) lowerElseStmt(alt ast.Expr) *Block {
	switch alt := alt.(type) {
	case nil:
		return nil
	case *ast.Block:
		return l.lowerBlock(alt)
	case *ast.IfExpr:
		if !alt.IsIfLet {
			stmt := l.lowerIfStmt(alt)
			return &Block{Stmts: []Stmt{stmt}, SpanV: nodeSpan(alt)}
		}
		lowered := l.lowerIfExpr(alt)
		return &Block{
			Stmts: []Stmt{&ExprStmt{X: lowered, SpanV: lowered.At()}},
			SpanV: nodeSpan(alt),
		}
	default:
		lowered := l.lowerExpr(alt)
		return &Block{
			Stmts: []Stmt{&ExprStmt{X: lowered, SpanV: lowered.At()}},
			SpanV: lowered.At(),
		}
	}
}

// assignOp maps a token kind to the IR AssignOp.
func assignOp(k token.Kind) AssignOp {
	switch k {
	case token.ASSIGN:
		return AssignEq
	case token.PLUSEQ:
		return AssignAdd
	case token.MINUSEQ:
		return AssignSub
	case token.STAREQ:
		return AssignMul
	case token.SLASHEQ:
		return AssignDiv
	case token.PERCENTEQ:
		return AssignMod
	case token.BITANDEQ:
		return AssignAnd
	case token.BITOREQ:
		return AssignOr
	case token.BITXOREQ:
		return AssignXor
	case token.SHLEQ:
		return AssignShl
	case token.SHREQ:
		return AssignShr
	}
	return AssignEq
}

// ==== Expressions ====

func (l *lowerer) lowerExpr(e ast.Expr) Expr {
	if e == nil {
		l.note("unsupported nil expression")
		return &ErrorExpr{Note: "nil expr", T: ErrTypeVal}
	}
	switch e := e.(type) {
	case *ast.IntLit:
		t := l.exprType(e)
		if t == ErrTypeVal {
			// Default int-literal type when the checker didn't
			// populate Types[e] — covers literals nested inside
			// contexts the Go-hosted checker skips (string interp
			// parts, match arm bodies, etc.). Without this, a
			// stray `+ 1` poisons the enclosing BinaryExpr to
			// ErrType, which cascades to every consumer of the
			// match / block result.
			t = TInt
		}
		return &IntLit{Text: e.Text, T: t, SpanV: nodeSpan(e)}
	case *ast.FloatLit:
		t := l.exprType(e)
		if t == ErrTypeVal {
			t = TFloat
		}
		return &FloatLit{Text: e.Text, T: t, SpanV: nodeSpan(e)}
	case *ast.BoolLit:
		return &BoolLit{Value: e.Value, SpanV: nodeSpan(e)}
	case *ast.CharLit:
		return &CharLit{Value: e.Value, SpanV: nodeSpan(e)}
	case *ast.ByteLit:
		return &ByteLit{Value: e.Value, SpanV: nodeSpan(e)}
	case *ast.StringLit:
		return l.lowerStringLit(e)
	case *ast.Ident:
		return l.lowerIdent(e)
	case *ast.ParenExpr:
		return l.lowerExpr(e.X)
	case *ast.UnaryExpr:
		return l.lowerUnary(e)
	case *ast.BinaryExpr:
		return l.lowerBinary(e)
	case *ast.CallExpr:
		return l.lowerCall(e)
	case *ast.ListExpr:
		return l.lowerList(e)
	case *ast.Block:
		blk := l.lowerBlock(e)
		t := l.exprType(e)
		if t == nil {
			if blk.Result != nil {
				t = blk.Result.Type()
			} else {
				t = TUnit
			}
		}
		return &BlockExpr{Block: blk, T: t, SpanV: nodeSpan(e)}
	case *ast.IfExpr:
		return l.lowerIfExpr(e)
	case *ast.MatchExpr:
		return l.lowerMatchExpr(e)
	case *ast.FieldExpr:
		return l.lowerFieldExpr(e)
	case *ast.IndexExpr:
		loweredX := l.lowerExpr(e.X)
		loweredIndex := l.lowerExpr(e.Index)
		t := l.exprType(e)
		if t == ErrTypeVal {
			if rec := recoverIndexType(loweredX); rec != ErrTypeVal {
				t = rec
			}
		}
		return &IndexExpr{
			X:     loweredX,
			Index: loweredIndex,
			T:     t,
			SpanV: nodeSpan(e),
		}
	case *ast.StructLit:
		return l.lowerStructLit(e)
	case *ast.TupleExpr:
		if len(e.Elems) == 1 {
			return l.lowerExpr(e.Elems[0])
		}
		out := &TupleLit{T: l.exprType(e), SpanV: nodeSpan(e)}
		for _, el := range e.Elems {
			out.Elems = append(out.Elems, l.lowerExpr(el))
		}
		return out
	case *ast.MapExpr:
		return l.lowerMapLit(e)
	case *ast.RangeExpr:
		out := &RangeLit{Inclusive: e.Inclusive, T: l.exprType(e), SpanV: nodeSpan(e)}
		if e.Start != nil {
			out.Start = l.lowerExpr(e.Start)
		}
		if e.Stop != nil {
			out.End = l.lowerExpr(e.Stop)
		}
		return out
	case *ast.QuestionExpr:
		return &QuestionExpr{
			X:     l.lowerExpr(e.X),
			T:     l.exprType(e),
			SpanV: nodeSpan(e),
		}
	case *ast.ClosureExpr:
		return l.lowerClosure(e)
	case *ast.TurbofishExpr:
		return l.lowerTurbofish(e)
	}
	l.note("unsupported expression %T at %v", e, e.Pos())
	return &ErrorExpr{Note: fmt.Sprintf("%T", e), T: ErrTypeVal, SpanV: nodeSpan(e)}
}

func (l *lowerer) exprType(e ast.Expr) Type {
	if l.chk == nil {
		return ErrTypeVal
	}
	t := l.chk.Types[e]
	if t == nil {
		return ErrTypeVal
	}
	return l.fromCheckerType(t)
}

func (l *lowerer) lowerStringLit(s *ast.StringLit) Expr {
	out := &StringLit{IsRaw: s.IsRaw, IsTriple: s.IsTriple, SpanV: nodeSpan(s)}
	for _, p := range s.Parts {
		if p.IsLit {
			out.Parts = append(out.Parts, StringPart{IsLit: true, Lit: p.Lit})
			continue
		}
		// A non-literal part with a nil expression is produced by the
		// parser's error-recovery path when a `{...}` interpolation
		// slot couldn't be parsed cleanly. Downstream consumers
		// (mir.Lower, the MIR emitter) assume every non-lit part has
		// a real Expr, so treat the hole as an empty string literal
		// here rather than letting lowerExpr crash on a nil switch.
		if p.Expr == nil {
			out.Parts = append(out.Parts, StringPart{IsLit: true, Lit: ""})
			continue
		}
		out.Parts = append(out.Parts, StringPart{Expr: l.lowerExpr(p.Expr)})
	}
	return out
}

func (l *lowerer) lowerIdent(id *ast.Ident) Expr {
	out := &Ident{Name: id.Name, SpanV: nodeSpan(id), T: ErrTypeVal}
	var sym *resolve.Symbol
	if l.res != nil {
		if s := l.res.RefsByID[id.ID]; s != nil {
			sym = s
			out.Kind = identKind(s)
		}
	}
	if l.chk != nil {
		if t := l.chk.Types[id]; t != nil {
			out.T = l.fromCheckerType(t)
		} else if sym != nil {
			if st := l.chk.SymTypes[sym]; st != nil {
				out.T = l.fromCheckerType(st)
			}
		}
	}
	// Last-resort operand-based recovery: when neither the per-node
	// Types map nor the SymTypes map covers this ident (the native
	// checker skips expressions nested inside string interpolation
	// parts, for instance), pull the declared type straight off the
	// resolved symbol's declaration node. Today's callers only need
	// the param / local-let shapes — the anchors that feed an
	// IndexExpr inside a `"{xs[i]}"` interpolation — so we keep the
	// match narrow and bail to ErrTypeVal for anything else, which
	// preserves the pre-recovery cascade behaviour for unfamiliar
	// decl forms.
	if (out.T == nil || out.T == ErrTypeVal) && sym != nil {
		if t := identTypeFromDecl(sym.Decl); t != nil {
			out.T = l.lowerType(t)
		}
	}
	return out
}

// identTypeFromDecl extracts the declared AST type from a symbol's
// introducing declaration. Handles the subset of decl shapes that a
// plain ident can resolve to: function / closure param (with or
// without destructuring pattern), and an immutable / mutable `let`
// binding carrying an explicit type annotation. Returns nil for
// every other kind — the caller treats that as "no recovery
// available" and leaves Ident.T at its prior value.
func identTypeFromDecl(decl ast.Node) ast.Type {
	switch d := decl.(type) {
	case *ast.Param:
		if d == nil {
			return nil
		}
		return d.Type
	case *ast.LetStmt:
		if d == nil {
			return nil
		}
		return d.Type
	case *ast.LetDecl:
		if d == nil {
			return nil
		}
		return d.Type
	}
	return nil
}

func (l *lowerer) symbol(id *ast.Ident) *resolve.Symbol {
	if l.res == nil || id == nil {
		return nil
	}
	return l.res.RefsByID[id.ID]
}

func identKind(sym *resolve.Symbol) IdentKind {
	if sym == nil {
		return IdentUnknown
	}
	switch sym.Kind {
	case resolve.SymLet:
		if _, ok := sym.Decl.(*ast.LetDecl); ok {
			return IdentGlobal
		}
		return IdentLocal
	case resolve.SymParam:
		return IdentParam
	case resolve.SymFn:
		return IdentFn
	case resolve.SymVariant:
		return IdentVariant
	case resolve.SymStruct, resolve.SymEnum, resolve.SymInterface, resolve.SymTypeAlias:
		return IdentTypeName
	case resolve.SymBuiltin:
		return IdentBuiltin
	}
	return IdentUnknown
}

func (l *lowerer) lowerUnary(e *ast.UnaryExpr) Expr {
	op, ok := unaryOp(e.Op)
	if !ok {
		l.note("unsupported unary op %v at %v", e.Op, e.Pos())
		return &ErrorExpr{Note: "unary op", T: ErrTypeVal, SpanV: nodeSpan(e)}
	}
	return &UnaryExpr{Op: op, X: l.lowerExpr(e.X), T: l.exprType(e), SpanV: nodeSpan(e)}
}

func unaryOp(k token.Kind) (UnOp, bool) {
	switch k {
	case token.MINUS:
		return UnNeg, true
	case token.PLUS:
		return UnPlus, true
	case token.NOT:
		return UnNot, true
	case token.BITNOT:
		return UnBitNot, true
	}
	return 0, false
}

func (l *lowerer) lowerBinary(e *ast.BinaryExpr) Expr {
	// `??` gets its own IR node so backends don't have to pattern-match
	// on a BinaryExpr with a dedicated op when they have special lowering.
	//
	// Note: we do NOT recover CoalesceExpr.T from its operands. Leaving
	// T=ErrTypeVal keeps the MIR emitter from attempting coalesce
	// lowering (which has a latent bug in the merge-block terminator
	// path) and defers to the legacy HIR path, which emits the
	// `coalesce.some/none/end` labels the test corpus expects.
	if e.Op == token.QQ {
		return &CoalesceExpr{
			Left:  l.lowerExpr(e.Left),
			Right: l.lowerExpr(e.Right),
			T:     l.exprType(e),
			SpanV: nodeSpan(e),
		}
	}
	op, ok := binaryOp(e.Op)
	if !ok {
		l.note("unsupported binary op %v at %v", e.Op, e.Pos())
		return &ErrorExpr{Note: "binary op", T: ErrTypeVal, SpanV: nodeSpan(e)}
	}
	left := l.lowerExpr(e.Left)
	right := l.lowerExpr(e.Right)
	t := l.exprType(e)
	if t == ErrTypeVal {
		t = recoverBinaryType(op, left, right)
	}
	return &BinaryExpr{
		Op:    op,
		Left:  left,
		Right: right,
		T:     t,
		SpanV: nodeSpan(e),
	}
}

// recoverBinaryType derives a binary expression's result type from its
// operand types when the checker did not populate Types[e]. This is a
// best-effort fallback that keeps arithmetic and comparison chains off
// ErrType when the operands themselves are well-typed — without it, a
// single missing TypedNode at the checker boundary poisons every
// enclosing expression and blocks MIR-direct lowering.
//
// The recovery rules mirror the native elaborator (toolchain/elab.osty
// `binOpResultType`) for the subset where result type is derivable from
// operand types without further context.
func recoverBinaryType(op BinOp, left, right Expr) Type {
	if left == nil || right == nil {
		return ErrTypeVal
	}
	lt := left.Type()
	rt := right.Type()
	if lt == ErrTypeVal || rt == ErrTypeVal || lt == nil || rt == nil {
		return ErrTypeVal
	}
	switch op {
	case BinEq, BinNeq, BinLt, BinLeq, BinGt, BinGeq:
		return TBool
	case BinAnd, BinOr:
		return TBool
	case BinAdd:
		// String + String → String (spec §4.6). Otherwise numeric.
		if isPrim(lt, PrimString) && isPrim(rt, PrimString) {
			return TString
		}
		return numericResult(lt, rt)
	case BinSub, BinMul, BinDiv, BinMod:
		return numericResult(lt, rt)
	case BinBitAnd, BinBitOr, BinBitXor, BinShl, BinShr:
		// Bitwise ops preserve the non-untyped operand type.
		if isIntegral(lt) {
			return lt
		}
		if isIntegral(rt) {
			return rt
		}
	}
	return ErrTypeVal
}

// recoverIndexType derives an index expression's element type from
// the base receiver's type when the checker did not populate
// Types[e]. Mirrors the other operand-based recovery helpers
// (recoverBinaryType / recoverBlockType / recoverCallReturnType)
// and targets the concrete shapes `xs[i]` appears in across the
// toolchain:
//
//   - List<T>[i]      → T    (direct element access, the dominant case)
//   - Map<K, V>[k]    → V    (index form that panics on miss; matches
//     the native backend's intrinsic dispatch)
//   - Bytes[i]        → Byte
//   - String[i]       → Char (semantically a code-point read, though
//     real Osty source uses .chars() / .bytes()
//     and almost never String[i] directly)
//
// Returns ErrTypeVal when the base itself is un-typed or non-indexable
// — leaving the cascade behaviour from before the recovery.
func recoverIndexType(base Expr) Type {
	if base == nil {
		return ErrTypeVal
	}
	bt := base.Type()
	if bt == nil || bt == ErrTypeVal {
		return ErrTypeVal
	}
	switch t := bt.(type) {
	case *NamedType:
		if t.Builtin {
			switch t.Name {
			case "List":
				if len(t.Args) == 1 && t.Args[0] != nil {
					return t.Args[0]
				}
			case "Map":
				if len(t.Args) == 2 && t.Args[1] != nil {
					return t.Args[1]
				}
			}
		}
	case *PrimType:
		switch t.Kind {
		case PrimBytes:
			return &PrimType{Kind: PrimByte}
		case PrimString:
			return &PrimType{Kind: PrimChar}
		}
	}
	return ErrTypeVal
}

func isPrim(t Type, k PrimKind) bool {
	if p, ok := t.(*PrimType); ok {
		return p.Kind == k
	}
	return false
}

func isIntegral(t Type) bool {
	p, ok := t.(*PrimType)
	if !ok {
		return false
	}
	switch p.Kind {
	case PrimInt, PrimInt8, PrimInt16, PrimInt32, PrimInt64,
		PrimUInt8, PrimUInt16, PrimUInt32, PrimUInt64, PrimByte:
		return true
	}
	return false
}

func isFloat(t Type) bool {
	p, ok := t.(*PrimType)
	if !ok {
		return false
	}
	switch p.Kind {
	case PrimFloat, PrimFloat32, PrimFloat64:
		return true
	}
	return false
}

// numericResult mirrors `binNumericCommon` in the native elaborator:
// float dominates; otherwise prefer a concrete Int over untyped-int.
func numericResult(lt, rt Type) Type {
	if isFloat(lt) || isFloat(rt) {
		// Prefer the concrete Float type over a polymorphic literal.
		if isPrim(lt, PrimFloat) || isPrim(rt, PrimFloat) {
			return TFloat
		}
		if isFloat(lt) {
			return lt
		}
		return rt
	}
	if !isIntegral(lt) || !isIntegral(rt) {
		return ErrTypeVal
	}
	// Concrete Int wins over the default lane.
	if isPrim(lt, PrimInt) || isPrim(rt, PrimInt) {
		return TInt
	}
	return lt
}

func binaryOp(k token.Kind) (BinOp, bool) {
	switch k {
	case token.PLUS:
		return BinAdd, true
	case token.MINUS:
		return BinSub, true
	case token.STAR:
		return BinMul, true
	case token.SLASH:
		return BinDiv, true
	case token.PERCENT:
		return BinMod, true
	case token.EQ:
		return BinEq, true
	case token.NEQ:
		return BinNeq, true
	case token.LT:
		return BinLt, true
	case token.LEQ:
		return BinLeq, true
	case token.GT:
		return BinGt, true
	case token.GEQ:
		return BinGeq, true
	case token.AND:
		return BinAnd, true
	case token.OR:
		return BinOr, true
	case token.BITAND:
		return BinBitAnd, true
	case token.BITOR:
		return BinBitOr, true
	case token.BITXOR:
		return BinBitXor, true
	case token.SHL:
		return BinShl, true
	case token.SHR:
		return BinShr, true
	}
	return 0, false
}

func (l *lowerer) lowerCall(e *ast.CallExpr) Expr {
	// Detect a print-family intrinsic on a bare identifier.
	if id, ok := e.Fn.(*ast.Ident); ok {
		if k, isIntrinsic := intrinsicByName(id.Name); isIntrinsic {
			out := &IntrinsicCall{Kind: k, SpanV: nodeSpan(e)}
			for _, a := range e.Args {
				out.Args = append(out.Args, l.lowerArg(a))
			}
			return out
		}
		// Check if this is a variant constructor: e.g. Some(42), Ok(x).
		if sym := l.symbol(id); sym != nil {
			if sym.Kind == resolve.SymVariant {
				return l.lowerVariantCall(e, "", sym.Name)
			}
			if sym.Kind == resolve.SymBuiltin && isPreludeVariantName(sym.Name) {
				return l.lowerVariantCall(e, "", sym.Name)
			}
		}
	}
	// Strip a turbofish wrapper to retain its type arguments.
	var typeArgs []Type
	fn := e.Fn
	if tf, ok := fn.(*ast.TurbofishExpr); ok {
		for _, a := range tf.Args {
			typeArgs = append(typeArgs, l.lowerType(a))
		}
		fn = tf.Base
	}
	// Method call: x.name(args).
	if fx, ok := fn.(*ast.FieldExpr); ok {
		if lowered := l.tryLowerBuilderChain(e, fx); lowered != nil {
			return lowered
		}
		if id, ok := fx.X.(*ast.Ident); ok {
			if sym := l.symbol(id); sym != nil {
				if sym.Kind == resolve.SymEnum || sym.Kind == resolve.SymStruct {
					if l.isVariantOfEnum(sym, fx.Name) {
						return l.lowerVariantCall(e, sym.Name, fx.Name)
					}
				}
				// Module-qualified call: `use std.strings` makes
				// `strings.compare(...)` a free-function call on the
				// stdlib module, not a method dispatch on a value.
				// Preserving the qualified FieldExpr shape lets
				// backends rewrite the callsite to a mangled symbol
				// when the module body is injected (see
				// backend.RewriteStdlibCallsites). Without this branch
				// the call would fall into lowerMethodCall and emit a
				// MethodCall node, which backends currently cannot
				// dispatch.
				if sym.Kind == resolve.SymPackage {
					return l.lowerQualifiedCall(e, fx, typeArgs)
				}
			}
		}
		return l.lowerMethodCall(e, fx, typeArgs)
	}
	// Fall back to the checker's monomorphisation record when no
	// turbofish was written but the callee is generic.
	if len(typeArgs) == 0 {
		typeArgs = l.instantiationArgs(e)
	}
	callee := l.lowerExpr(fn)
	t := l.exprType(e)
	if t == ErrTypeVal || t == nil || hasPoisonedTypeArg(t) {
		// Prefer the callee's FnType.Return when the call's own
		// type is missing or carries a poisoned type-arg (the
		// embedded checker sometimes records `Result<Int, Error>`
		// as `Result<Int, <error>>` because the inner `Error`
		// reference doesn't resolve at the native-checker
		// boundary). The callee's FnType is seeded from the
		// resolver's symbol table for top-level fns and tends to
		// carry a fully-resolved return shape.
		if recovered := recoverCallReturnType(callee); recovered != nil && recovered != ErrTypeVal && !hasPoisonedTypeArg(recovered) {
			t = recovered
		}
		// Final fallback for bare-Ident callees whose FnType is
		// also poisoned: re-lower the resolved fn declaration's AST
		// return type. The resolver's Symbol.Decl points at the
		// originating ast.FnDecl, whose ReturnType node carries
		// fully-syntactic names — re-running lowerType on it
		// produces a fresh, non-poisoned IR Type even when the
		// checker's typed-node table dropped the inner reference.
		if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) {
			if id, ok := fn.(*ast.Ident); ok {
				if rec := l.recoverFnDeclReturnType(id); rec != nil && rec != ErrTypeVal && !hasPoisonedTypeArg(rec) {
					t = rec
				}
			}
		}
	}
	out := &CallExpr{
		Callee:   callee,
		TypeArgs: typeArgs,
		T:        t,
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
}

type builderLowerSetter struct {
	name string
	arg  *ast.Arg
}

func (l *lowerer) tryLowerBuilderChain(call *ast.CallExpr, build *ast.FieldExpr) Expr {
	if call == nil || build == nil || build.Name != "build" || build.IsOptional || len(call.Args) != 0 {
		return nil
	}
	var setters []builderLowerSetter
	cursor := build.X
	for {
		inner, ok := cursor.(*ast.CallExpr)
		if !ok {
			return nil
		}
		fe, ok := inner.Fn.(*ast.FieldExpr)
		if !ok || fe.IsOptional {
			return nil
		}
		if fe.Name == "builder" {
			if len(inner.Args) != 0 {
				return nil
			}
			id, ok := fe.X.(*ast.Ident)
			if !ok {
				return nil
			}
			sd := l.structDeclByIdent(id)
			if sd == nil {
				return nil
			}
			if !check.ClassifyBuilderDerive(sd).Derivable {
				return nil
			}
			return l.lowerBuilderStructLit(call, sd, nil, setters)
		}
		if fe.Name == "toBuilder" {
			if len(inner.Args) != 0 {
				return nil
			}
			// Lower the receiver up front so we can use the IR expr's
			// type as a fallback when AST-side struct-decl lookup
			// fails — the embedded selfhost checker doesn't always
			// populate `Types[ident]` for value-bound idents (`let p
			// = Point{...}` followed by `p.toBuilder()`). The lowered
			// IR Ident's `T` field is filled by `lowerIdent`'s
			// `bindingPatTypes` fallback whenever the binding's
			// resolver Symbol points at an `IdentPat` recorded by
			// `lowerLetStmt`.
			recv := l.lowerExpr(fe.X)
			sd := l.structDeclFromReceiver(fe.X)
			if sd == nil && recv != nil {
				sd = l.structDeclByType(recv.Type())
			}
			if sd == nil {
				return nil
			}
			if !check.ClassifyBuilderDerive(sd).Derivable {
				return nil
			}
			return l.lowerBuilderStructLit(call, sd, recv, setters)
		}
		if len(inner.Args) != 1 {
			return nil
		}
		arg := inner.Args[0]
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil
		}
		setters = append(setters, builderLowerSetter{name: fe.Name, arg: arg})
		cursor = fe.X
	}
}

func (l *lowerer) structDeclByIdent(id *ast.Ident) *ast.StructDecl {
	if id == nil {
		return nil
	}
	if sym := l.symbol(id); sym != nil {
		if sd, ok := sym.Decl.(*ast.StructDecl); ok {
			return sd
		}
	}
	return l.structDeclByName(id.Name)
}

func (l *lowerer) structDeclFromReceiver(e ast.Expr) *ast.StructDecl {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ast.StructLit:
		switch head := n.Type.(type) {
		case *ast.Ident:
			return l.structDeclByIdent(head)
		case *ast.FieldExpr:
			return l.structDeclByName(head.Name)
		}
	case *ast.ParenExpr:
		return l.structDeclFromReceiver(n.X)
	case *ast.Ident:
		// Type-name idents resolve directly via the resolver symbol.
		if sd := l.structDeclByIdent(n); sd != nil {
			return sd
		}
		// Value-bound idents (let p = Point{...}) carry their inferred
		// type on the per-node Types map or on the resolver Symbol's
		// SymTypes entry. Mirror the lowerIdent fallback chain so
		// `p.toBuilder()` resolves to Point's StructDecl when one of
		// those maps covers the receiver.
		if sd := l.structDeclByType(l.exprType(e)); sd != nil {
			return sd
		}
		if l.chk != nil {
			if sym := l.symbol(n); sym != nil {
				if st := l.chk.SymTypes[sym]; st != nil {
					if sd := l.structDeclByType(l.fromCheckerType(st)); sd != nil {
						return sd
					}
				}
				// Walk to the declared type on the symbol's
				// declaration node. Covers `let p: Point = ...` and
				// `fn f(p: Point)` with explicit annotations.
				if sd := l.structDeclFromSymbolDecl(sym); sd != nil {
					return sd
				}
				// Final fallback: consult `bindingPatTypes` populated
				// by `lowerLetStmt` for the case `let p = Point
				// {...}` where the binding's `Decl` points at the
				// IdentPat (without an explicit Type node). Read here
				// rather than in `lowerIdent` to keep the per-Ident
				// type-fill path untouched — downstream MIR
				// mut-receiver write-back logic depends on the
				// existing ErrTypeVal-poisoned receiver shape and
				// regresses when value-side idents start carrying
				// real types via the per-node `T` field.
				if l.bindingPatTypes != nil {
					if ip, ok := sym.Decl.(*ast.IdentPat); ok {
						if t := l.bindingPatTypes[ip]; t != nil {
							if sd := l.structDeclByType(t); sd != nil {
								return sd
							}
						}
					}
				}
			}
		}
		return nil
	}
	return l.structDeclByType(l.exprType(e))
}

// structDeclFromSymbolDecl walks the declaration node carried by a
// resolved Symbol and extracts the struct decl behind the binding's
// declared / inferred type. Mirrors the LetStmt / Param branch logic
// the AST checker uses to seed the `Types` map; callers fall back to
// this when neither the per-node Types map nor SymTypes covers the
// receiver's identifier.
func (l *lowerer) structDeclFromSymbolDecl(sym *resolve.Symbol) *ast.StructDecl {
	if sym == nil || sym.Decl == nil {
		return nil
	}
	switch d := sym.Decl.(type) {
	case *ast.LetStmt:
		if d.Type != nil {
			return l.structDeclFromTypeNode(d.Type)
		}
		return l.structDeclFromReceiver(d.Value)
	case *ast.LetDecl:
		if d.Type != nil {
			return l.structDeclFromTypeNode(d.Type)
		}
		return l.structDeclFromReceiver(d.Value)
	case *ast.Param:
		if d.Type != nil {
			return l.structDeclFromTypeNode(d.Type)
		}
	}
	return nil
}

// structDeclFromTypeNode unwraps an `ast.Type` written in source (e.g.
// the `Point` in `let p: Point = ...`) and returns its StructDecl.
// Only the bare `Ident` and `FieldExpr` (qualified) shapes are
// handled — those are what the receiver chain needs; richer type
// expressions (Optional, List<T>, etc.) don't resolve to a struct.
func (l *lowerer) structDeclFromTypeNode(t ast.Type) *ast.StructDecl {
	switch n := t.(type) {
	case *ast.NamedType:
		if len(n.Path) == 1 {
			return l.structDeclByName(n.Path[0])
		}
		if len(n.Path) > 0 {
			return l.structDeclByName(n.Path[len(n.Path)-1])
		}
	}
	return nil
}

func (l *lowerer) structDeclByType(t Type) *ast.StructDecl {
	nt, ok := t.(*NamedType)
	if !ok || nt == nil || nt.Builtin {
		return nil
	}
	return l.structDeclByName(nt.Name)
}

func (l *lowerer) structDeclByName(name string) *ast.StructDecl {
	if name == "" {
		return nil
	}
	if l.file != nil {
		for _, decl := range l.file.Decls {
			if sd, ok := decl.(*ast.StructDecl); ok && sd != nil && sd.Name == name {
				return sd
			}
		}
	}
	if l.res != nil && l.res.FileScope != nil {
		if sym := l.res.FileScope.Lookup(name); sym != nil {
			if sd, ok := sym.Decl.(*ast.StructDecl); ok {
				return sd
			}
		}
	}
	return nil
}

func (l *lowerer) lowerBuilderStructLit(
	call *ast.CallExpr,
	sd *ast.StructDecl,
	spread Expr,
	setters []builderLowerSetter,
) Expr {
	if call == nil || sd == nil {
		return nil
	}
	byName := make(map[string]*ast.Arg, len(setters))
	for _, setter := range setters {
		if setter.arg == nil {
			continue
		}
		if _, exists := byName[setter.name]; exists {
			continue
		}
		byName[setter.name] = setter.arg
	}
	out := &StructLit{
		TypeName: sd.Name,
		T:        l.exprType(call),
		Spread:   spread,
		SpanV:    nodeSpan(call),
	}
	for _, field := range sd.Fields {
		if field == nil || !field.Pub {
			continue
		}
		arg := byName[field.Name]
		if arg == nil || arg.Value == nil {
			continue
		}
		out.Fields = append(out.Fields, StructLitField{
			Name:  field.Name,
			Value: l.lowerExpr(arg.Value),
			SpanV: Span{Start: posFromToken(arg.Pos()), End: posFromToken(arg.End())},
		})
	}
	return out
}

// hasPoisonedTypeArg reports whether `t` carries an `<error>` /
// `ErrTypeVal` somewhere in its type-argument tree. The embedded
// selfhost checker sometimes records `Result<Int, Error>` with the
// second arg dropped to `<error>` because the inner `Error` lookup
// missed at the native-checker boundary; downstream MIR / LLVM
// rendering then produces opaque suffixes (`%Result.i64.opaque`
// instead of `%Result.i64.Error`) that fail LLVM verification when
// the Aggregate's slot type was rendered from the function's
// non-poisoned return type. Callers gate their stdlib-signature
// recovery on this so partial poisoning still routes through the
// recoverMethodReturnType / recoverCallReturnType chain.
func hasPoisonedTypeArg(t Type) bool {
	if t == nil || t == ErrTypeVal {
		return false
	}
	switch x := t.(type) {
	case *NamedType:
		for _, a := range x.Args {
			if a == ErrTypeVal {
				return true
			}
			if hasPoisonedTypeArg(a) {
				return true
			}
		}
	case *OptionalType:
		if x.Inner == ErrTypeVal {
			return true
		}
		return hasPoisonedTypeArg(x.Inner)
	case *TupleType:
		for _, e := range x.Elems {
			if e == ErrTypeVal {
				return true
			}
			if hasPoisonedTypeArg(e) {
				return true
			}
		}
	case *FnType:
		for _, p := range x.Params {
			if p == ErrTypeVal {
				return true
			}
			if hasPoisonedTypeArg(p) {
				return true
			}
		}
		if x.Return == ErrTypeVal {
			return true
		}
		return hasPoisonedTypeArg(x.Return)
	}
	return false
}

// recoverFnDeclReturnType walks the resolver Symbol behind a callee
// Ident, fetches the originating ast.FnDecl, and re-lowers its
// declared return type via `lowerType`. Used as the last-resort
// fallback when both the call's own checker entry and the callee
// Ident's FnType carry poisoned (`<error>`) type args — the AST node
// still has the fully-syntactic source form, so a fresh round-trip
// through `lowerType` produces a non-poisoned IR shape.
func (l *lowerer) recoverFnDeclReturnType(id *ast.Ident) Type {
	if id == nil || l.res == nil {
		return nil
	}
	sym := l.res.RefsByID[id.ID]
	if sym == nil || sym.Decl == nil {
		return nil
	}
	fn, ok := sym.Decl.(*ast.FnDecl)
	if !ok || fn == nil || fn.ReturnType == nil {
		return nil
	}
	return l.lowerType(fn.ReturnType)
}

// recoverCallReturnType pulls the return type off the callee's FnType
// when the checker did not record a type for the call expression. The
// resolver seeds symbol types for top-level fns and `use`-imported
// functions, which propagate to the callee Ident during lowerIdent,
// so the FnType is usually present even when the call's own TypedNode
// is missing at the native-checker boundary.
func recoverCallReturnType(callee Expr) Type {
	if callee == nil {
		return ErrTypeVal
	}
	ct := callee.Type()
	if ct == nil || ct == ErrTypeVal {
		return ErrTypeVal
	}
	if f, ok := ct.(*FnType); ok && f.Return != nil {
		return f.Return
	}
	return ErrTypeVal
}

// recoverMethodCallType patches the one method-call shape that the
// generic callee-return recovery cannot see: `recv.downcast::<T>()`.
// The IR method form stores only the receiver + method name, so the
// synthetic checker signature (`Error.downcast::<T>() -> T?`) is not
// available as a first-class FnType on the lowered node. When the
// checker/native-checker boundary drops the call's own type but still
// records the turbofish args, recover the spec-mandated `T?` surface.
func recoverMethodCallType(name string, typeArgs []Type) Type {
	if name == "downcast" && len(typeArgs) == 1 && typeArgs[0] != nil && typeArgs[0] != ErrTypeVal {
		return &OptionalType{Inner: typeArgs[0]}
	}
	return ErrTypeVal
}

// lowerArg lowers a single call argument, preserving its keyword name
// when present.
func (l *lowerer) lowerArg(a *ast.Arg) Arg {
	return Arg{
		Name:  a.Name,
		Value: l.lowerExpr(a.Value),
		SpanV: Span{Start: posFromToken(a.Pos()), End: posFromToken(a.End())},
	}
}

// instantiationArgs returns the concrete type-argument list the
// checker recorded for this call site (monomorphisation info), or nil
// when the checker did not annotate it.
func (l *lowerer) instantiationArgs(e *ast.CallExpr) []Type {
	if l.chk == nil || l.chk.InstantiationsByID == nil || e == nil {
		return nil
	}
	raw, ok := l.chk.InstantiationsByID[e.ID]
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]Type, 0, len(raw))
	for _, ta := range raw {
		out = append(out, l.fromCheckerType(ta))
	}
	return out
}

// isVariantOfEnum reports whether variantName is a declared variant on
// the enum named by sym. Consults the checker's type description table
// when available.
func (l *lowerer) isVariantOfEnum(sym *resolve.Symbol, variantName string) bool {
	if sym == nil || sym.Kind != resolve.SymEnum || sym.Decl == nil {
		return false
	}
	ed, ok := sym.Decl.(*ast.EnumDecl)
	if !ok {
		return false
	}
	for _, v := range ed.Variants {
		if v.Name == variantName {
			return true
		}
	}
	return false
}

// lowerMethodCall lowers `receiver.name(args)` into an IR MethodCall,
// preserving turbofish type arguments.
// lowerQualifiedCall lowers `module.fn(args)` — where `module` resolves
// to a `use`-imported package alias — as a CallExpr whose callee is a
// FieldExpr, preserving the `(module, fn)` pair for downstream passes
// (stdlib reachability scan, callsite rewriting during stdlib body
// injection). The FieldExpr shape is deliberately chosen to match how a
// caller-constructed IR module would write the same call; a single
// consumer shape keeps ir.Reach and backend.RewriteStdlibCallsites
// uniform.
func (l *lowerer) lowerQualifiedCall(e *ast.CallExpr, fx *ast.FieldExpr, typeArgs []Type) Expr {
	if len(typeArgs) == 0 {
		typeArgs = l.instantiationArgs(e)
	}
	callee := &FieldExpr{
		X:     l.lowerExpr(fx.X),
		Name:  fx.Name,
		T:     l.exprType(fx),
		SpanV: nodeSpan(fx),
	}
	t := l.exprType(e)
	if t == ErrTypeVal {
		t = recoverCallReturnType(callee)
	}
	// The Go-hosted checker doesn't register `use X { fn Y(...) -> R
	// }` member signatures on the package symbol, so `host.Y` ends up
	// as <error> in both the checker types map and the callee's
	// FnType. Fall back to reading the UseDecl body (for inline FFI
	// signatures) or the resolved package scope (for stdlib /
	// workspace modules) directly: the AST already has the signature,
	// we just need to lower it. Without this, MIR's typeSupported
	// rejects the synthetic result temp with `unsupported local type
	// <error>` for every runtime / stdlib module call site.
	if t == ErrTypeVal || t == nil {
		if id, ok := fx.X.(*ast.Ident); ok {
			if sym := l.symbol(id); sym != nil && sym.Kind == resolve.SymPackage {
				if ud, ok := sym.Decl.(*ast.UseDecl); ok && ud != nil {
					if ret := l.lookupUseDeclFnReturn(ud, fx.Name); ret != nil {
						t = ret
					}
				}
				if (t == ErrTypeVal || t == nil) && sym.Package != nil {
					if ret := l.lookupPackageFnReturn(sym.Package, fx.Name); ret != nil {
						t = ret
					}
				}
			}
		}
	}
	out := &CallExpr{
		Callee:   callee,
		TypeArgs: typeArgs,
		T:        t,
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
}

// lookupUseDeclFnReturn scans a `use X { ... }` body for an fn named
// fnName and returns its lowered return type (TUnit when the source
// declared no return type). Returns nil when no matching fn is found.
// This is the fallback path used by lowerQualifiedCall when the
// checker left the call's result type as <error>.
func (l *lowerer) lookupUseDeclFnReturn(ud *ast.UseDecl, fnName string) Type {
	if ud == nil {
		return nil
	}
	for _, d := range ud.GoBody {
		fn, ok := d.(*ast.FnDecl)
		if !ok || fn == nil || fn.Name != fnName {
			continue
		}
		if fn.ReturnType == nil {
			return TUnit
		}
		return l.lowerType(fn.ReturnType)
	}
	return nil
}

// lookupPackageFnReturn finds a top-level public fn named fnName in
// the resolved package and returns its lowered return type. Used by
// lowerQualifiedCall as the fallback when the UseDecl is a bare
// `use std.strings as X` (no inline FFI body) — the fn lives in the
// package's PkgScope, and its AST is on the resolved package file.
func (l *lowerer) lookupPackageFnReturn(pkg *resolve.Package, fnName string) Type {
	if pkg == nil {
		return nil
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || fn == nil || fn.Name != fnName {
				continue
			}
			if fn.ReturnType == nil {
				return TUnit
			}
			return l.lowerType(fn.ReturnType)
		}
	}
	return nil
}

func (l *lowerer) lowerMethodCall(e *ast.CallExpr, fx *ast.FieldExpr, typeArgs []Type) Expr {
	if len(typeArgs) == 0 {
		typeArgs = l.instantiationArgs(e)
	}
	recv := l.lowerExpr(fx.X)
	t := l.exprType(e)
	if t == ErrTypeVal || t == nil || hasPoisonedTypeArg(t) {
		if recovered := recoverMethodCallType(fx.Name, typeArgs); recovered != ErrTypeVal {
			t = recovered
		}
		// Recover from builtin method signatures when the checker
		// left the call type unpopulated or recorded it with poisoned
		// type args. Covers the common shapes from List / Map / Set /
		// String / Bytes: `.len()`, `.isEmpty()`, `.contains(x)`,
		// `.startsWith(s)`, etc., and `.toInt()` / `.toFloat()` on
		// String which produce a `Result<T, Error>` whose `Error`
		// arg the embedded checker sometimes records as `<error>`.
		// Without these, one method call with a checker-skipped
		// receiver poisons every enclosing expression and blocks
		// MIR / propagates `<error>` into Match scrutinee shapes.
		if recovered := recoverMethodReturnType(fx.Name, recv); recovered != nil {
			if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) {
				t = recovered
			}
		}
	}
	out := &MethodCall{
		Receiver: recv,
		Name:     fx.Name,
		TypeArgs: typeArgs,
		T:        t,
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
}

// recoverMethodReturnType derives the return type of a receiver-and-
// method pair for the subset of stdlib intrinsics where the return
// shape is fixed by the name alone. Used as a fallback when the
// Go-hosted checker didn't populate `Types[e]` for the call. Mirrors
// the routing in internal/mir/lower.go:methodToIntrinsic.
func recoverMethodReturnType(name string, recv Expr) Type {
	if recv == nil {
		return nil
	}
	rt := recv.Type()
	if rt == nil || rt == ErrTypeVal {
		return nil
	}
	// Common name-based shortcuts that don't depend on the receiver's
	// concrete generic args.
	switch name {
	case "len":
		if isBuiltinContainer(rt) || isPrim(rt, PrimString) || isPrim(rt, PrimBytes) {
			return TInt
		}
	case "isEmpty":
		if isBuiltinContainer(rt) || isPrim(rt, PrimString) || isPrim(rt, PrimBytes) {
			return TBool
		}
	case "contains", "hasPrefix", "hasSuffix", "startsWith", "endsWith":
		if isPrim(rt, PrimString) || isPrim(rt, PrimBytes) || isBuiltinContainer(rt) {
			return TBool
		}
	case "toUpper", "toLower", "trim", "trimSpace", "trimLeft", "trimRight":
		if isPrim(rt, PrimString) {
			return TString
		}
	case "substring", "slice", "replace", "repeat":
		if isPrim(rt, PrimString) {
			return TString
		}
	case "split":
		if isPrim(rt, PrimString) {
			return &NamedType{Name: "List", Builtin: true, Args: []Type{TString}}
		}
	case "join":
		// `parts.join(sep)` on List<String> returns String.
		if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "List" {
			return TString
		}
	case "toInt":
		// `String.toInt(self) -> Result<Int, Error>`. Char.toInt returns
		// a bare Int, but the prelude / primitive shape disambiguates by
		// receiver type — only the String surface routes through `?`
		// propagation, so we restrict the recovery to that.
		if isPrim(rt, PrimString) {
			return &NamedType{
				Name:    "Result",
				Builtin: true,
				Args: []Type{
					TInt,
					&NamedType{Name: "Error", Builtin: true},
				},
			}
		}
	case "toFloat":
		if isPrim(rt, PrimString) {
			return &NamedType{
				Name:    "Result",
				Builtin: true,
				Args: []Type{
					TFloat,
					&NamedType{Name: "Error", Builtin: true},
				},
			}
		}
	}
	// Element-type returns: List<T>.first / .last / .get → T?, .push → Unit.
	if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "List" && len(nt.Args) == 1 {
		switch name {
		case "first", "last":
			return &OptionalType{Inner: nt.Args[0]}
		case "push":
			return TUnit
		case "sorted":
			return nt
		}
	}
	return nil
}

// isBuiltinContainer reports whether t is one of the builtin
// homogeneous collections whose len/isEmpty/contains return types
// can be derived from the method name alone.
func isBuiltinContainer(t Type) bool {
	nt, ok := t.(*NamedType)
	if !ok || !nt.Builtin {
		return false
	}
	switch nt.Name {
	case "List", "Map", "Set":
		return true
	}
	return false
}

// lowerVariantCall builds a VariantLit from a call whose callee is a
// variant symbol (`Some(42)`) or an enum-qualified variant
// (`Color.Red(255)`).
func (l *lowerer) lowerVariantCall(e *ast.CallExpr, enum, variant string) Expr {
	out := &VariantLit{
		Enum:    enum,
		Variant: variant,
		T:       l.exprType(e),
		SpanV:   nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
}

func intrinsicByName(name string) (IntrinsicKind, bool) {
	switch name {
	case "print":
		return IntrinsicPrint, true
	case "println":
		return IntrinsicPrintln, true
	case "eprint":
		return IntrinsicEprint, true
	case "eprintln":
		return IntrinsicEprintln, true
	}
	return 0, false
}

func isPreludeVariantName(name string) bool {
	switch name {
	case "Some", "None", "Ok", "Err":
		return true
	}
	return false
}

func (l *lowerer) lowerList(e *ast.ListExpr) Expr {
	out := &ListLit{SpanV: nodeSpan(e)}
	for _, el := range e.Elems {
		out.Elems = append(out.Elems, l.lowerExpr(el))
	}
	// Derive the element type from the checker's inferred list type.
	if t := l.exprType(e); t != nil {
		if nt, ok := t.(*NamedType); ok && nt.Name == "List" && len(nt.Args) == 1 {
			out.Elem = nt.Args[0]
		}
	}
	if out.Elem == nil && len(out.Elems) > 0 {
		out.Elem = out.Elems[0].Type()
	}
	// Final fallback: when both the checker's list type and the
	// first lowered element's type are poisoned (`<error>`),
	// inspect the first AST element's syntactic shape to recover a
	// concrete element type. The `[Point {...}]` shorthand is the
	// common case — the literal's head ident names the struct
	// without going through the per-node Types map.
	if (out.Elem == nil || out.Elem == ErrTypeVal) && len(e.Elems) > 0 {
		if t := bindingTypeFromAST(e.Elems[0]); t != nil {
			out.Elem = t
		}
	}
	if out.Elem == nil {
		out.Elem = ErrTypeVal
	}
	return out
}

func (l *lowerer) lowerIfExpr(e *ast.IfExpr) Expr {
	t := l.exprType(e)
	thenBlk := l.lowerBlock(e.Then)
	elseBlk := l.lowerElse(e.Else)
	if t == ErrTypeVal {
		t = recoverBlockType(thenBlk, elseBlk)
	}
	if e.IsIfLet {
		return &IfLetExpr{
			Pattern:   l.lowerPattern(e.Pattern),
			Scrutinee: l.lowerExpr(e.Cond),
			Then:      thenBlk,
			Else:      elseBlk,
			T:         t,
			SpanV:     nodeSpan(e),
		}
	}
	return &IfExpr{Cond: l.lowerExpr(e.Cond), Then: thenBlk, Else: elseBlk, T: t, SpanV: nodeSpan(e)}
}

// recoverBlockType picks a non-Err type from either branch of an if/else.
// When only one branch carries a good type and the other is ErrType —
// typical when the checker's elab reports one branch at default rules
// but drops the enclosing if — prefer the good one.
func recoverBlockType(then, els *Block) Type {
	tt := blockResultType(then)
	et := blockResultType(els)
	if tt != nil && tt != ErrTypeVal {
		return tt
	}
	if et != nil && et != ErrTypeVal {
		return et
	}
	return ErrTypeVal
}

func blockResultType(b *Block) Type {
	if b == nil || b.Result == nil {
		return nil
	}
	return b.Result.Type()
}

// lowerElse normalises an else arm (which is an ast.Expr per the
// parser) into a *Block, or nil for no-else.
func (l *lowerer) lowerElse(alt ast.Expr) *Block {
	switch alt := alt.(type) {
	case nil:
		return nil
	case *ast.Block:
		return l.lowerBlock(alt)
	case *ast.IfExpr:
		inner := l.lowerIfExpr(alt)
		return &Block{Result: inner, SpanV: inner.At()}
	default:
		lowered := l.lowerExpr(alt)
		return &Block{Result: lowered, SpanV: lowered.At()}
	}
}

// ==== Span helpers ====

func posFromToken(p token.Pos) Pos {
	return Pos{Offset: p.Offset, Line: p.Line, Column: p.Column}
}

func nodeSpan(n ast.Node) Span {
	return Span{Start: posFromToken(n.Pos()), End: posFromToken(n.End())}
}

// ==== Additional declarations ====

func (l *lowerer) lowerUseDecl(u *ast.UseDecl) Decl {
	out := &UseDecl{
		Path:         append([]string(nil), u.Path...),
		RawPath:      u.RawPath,
		Alias:        u.Alias,
		IsGoFFI:      u.IsGoFFI,
		IsRuntimeFFI: u.IsRuntimeFFI,
		GoPath:       u.GoPath,
		RuntimePath:  u.RuntimePath,
		SpanV:        nodeSpan(u),
	}
	if out.Alias == "" && len(out.Path) > 0 {
		out.Alias = out.Path[len(out.Path)-1]
	}
	for _, d := range u.GoBody {
		if lowered := l.lowerDecl(d); lowered != nil {
			out.GoBody = append(out.GoBody, lowered)
		}
	}
	return out
}

func (l *lowerer) lowerInterfaceDecl(id *ast.InterfaceDecl) Decl {
	out := &InterfaceDecl{
		Name:     id.Name,
		Exported: id.Pub,
		SpanV:    nodeSpan(id),
	}
	for _, gp := range id.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, id.Name))
	}
	for _, ext := range id.Extends {
		out.Extends = append(out.Extends, l.lowerType(ext))
	}
	for _, m := range id.Methods {
		out.Methods = append(out.Methods, l.lowerFnDecl(m))
	}
	return out
}

func (l *lowerer) lowerTypeAliasDecl(td *ast.TypeAliasDecl) Decl {
	out := &TypeAliasDecl{
		Name:     td.Name,
		Target:   l.lowerType(td.Target),
		Exported: td.Pub,
		SpanV:    nodeSpan(td),
	}
	for _, gp := range td.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, td.Name))
	}
	return out
}

// ==== Additional statements ====

func (l *lowerer) lowerDeferStmt(s *ast.DeferStmt) Stmt {
	// DeferStmt's X is expression-typed but almost always a Block;
	// normalise to always a *Block in the IR so backends don't have to
	// peek at the inner expression.
	out := &DeferStmt{SpanV: nodeSpan(s)}
	if blk, ok := s.X.(*ast.Block); ok {
		out.Body = l.lowerBlock(blk)
		return out
	}
	lowered := l.lowerExpr(s.X)
	out.Body = &Block{
		Stmts: []Stmt{&ExprStmt{X: lowered, SpanV: lowered.At()}},
		SpanV: lowered.At(),
	}
	return out
}

// ==== Additional expressions ====

func (l *lowerer) lowerFieldExpr(e *ast.FieldExpr) Expr {
	// A numeric name (`t.0`) is tuple-indexed access. The parser spells
	// it with a FieldExpr; lift it to TupleAccess to keep backends
	// simple.
	if idx, ok := tupleIndex(e.Name); ok {
		return &TupleAccess{
			X:     l.lowerExpr(e.X),
			Index: idx,
			T:     l.exprType(e),
			SpanV: nodeSpan(e),
		}
	}
	x := l.lowerExpr(e.X)
	t := l.exprType(e)
	if t == ErrTypeVal || t == nil {
		// Recover from the struct declaration when the checker didn't
		// record a type for this field access. Without this, a single
		// checker-skipped FieldExpr propagates ErrType to every
		// `.locals.len()` or `.name + something` chain downstream.
		if recovered := l.recoverFieldType(x.Type(), e.Name); recovered != nil {
			t = recovered
		}
	}
	return &FieldExpr{
		X:        x,
		Name:     e.Name,
		Optional: e.IsOptional,
		T:        t,
		SpanV:    nodeSpan(e),
	}
}

// recoverFieldType resolves a field access `receiverType.fieldName`
// back to the declared field type by consulting the resolver's type
// decl for receiverType. Only handles user structs today —
// enums/interfaces/tuples use different access shapes that do not
// flow through lowerFieldExpr in the same way.
func (l *lowerer) recoverFieldType(receiverType Type, fieldName string) Type {
	if receiverType == nil || receiverType == ErrTypeVal {
		return nil
	}
	nt, ok := receiverType.(*NamedType)
	if !ok || nt.Builtin {
		return nil
	}
	if l.res == nil {
		return nil
	}
	sym := l.res.FileScope.Lookup(nt.Name)
	if sym == nil || sym.Decl == nil {
		return nil
	}
	sd, ok := sym.Decl.(*ast.StructDecl)
	if !ok || sd == nil {
		return nil
	}
	for _, f := range sd.Fields {
		if f == nil || f.Name != fieldName {
			continue
		}
		if f.Type == nil {
			return nil
		}
		return l.lowerType(f.Type)
	}
	return nil
}

// tupleIndex parses a field name like "0" or "12" as a tuple index.
// Returns (idx, true) when the whole string is a non-negative decimal.
func tupleIndex(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	n := 0
	for _, r := range name {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

func (l *lowerer) lowerStructLit(s *ast.StructLit) Expr {
	name := ""
	var headIdent *ast.Ident
	switch h := s.Type.(type) {
	case *ast.Ident:
		name = h.Name
		headIdent = h
	case *ast.FieldExpr:
		// `pkg.Type { ... }` — keep the trailing name; the IR doesn't
		// model packages yet.
		name = h.Name
	}
	out := &StructLit{
		TypeName: name,
		T:        l.exprType(s),
		SpanV:    nodeSpan(s),
	}
	explicit := map[string]bool{}
	for _, f := range s.Fields {
		field := StructLitField{
			Name:  f.Name,
			SpanV: Span{Start: posFromToken(f.Pos()), End: posFromToken(f.End())},
		}
		if f.Value != nil {
			field.Value = l.lowerExpr(f.Value)
		}
		out.Fields = append(out.Fields, field)
		explicit[f.Name] = true
	}
	if s.Spread != nil {
		out.Spread = l.lowerExpr(s.Spread)
	}
	// Inject defaults for unspecified fields when no spread is present.
	// With a spread, missing fields come from the spread base — defaults
	// are only relevant on the bare `T { explicit, ... }` shape. Defaults
	// are pure literals per spec (§3.4: literal-only default values), so
	// `lowerExpr` produces a self-contained IR Expr per construction site.
	if s.Spread == nil && headIdent != nil {
		if sd := l.structDeclByIdent(headIdent); sd != nil {
			for _, df := range sd.Fields {
				if df == nil || df.Default == nil || df.Name == "" {
					continue
				}
				if explicit[df.Name] {
					continue
				}
				out.Fields = append(out.Fields, StructLitField{
					Name:  df.Name,
					Value: l.lowerExpr(df.Default),
					SpanV: nodeSpan(s),
				})
			}
		}
	}
	return out
}

func (l *lowerer) lowerMapLit(m *ast.MapExpr) Expr {
	out := &MapLit{SpanV: nodeSpan(m)}
	for _, en := range m.Entries {
		out.Entries = append(out.Entries, MapEntry{
			Key:   l.lowerExpr(en.Key),
			Value: l.lowerExpr(en.Value),
			SpanV: Span{Start: posFromToken(en.Pos()), End: posFromToken(en.End())},
		})
	}
	if t := l.exprType(m); t != nil {
		if nt, ok := t.(*NamedType); ok && nt.Name == "Map" && len(nt.Args) == 2 {
			out.KeyT = nt.Args[0]
			out.ValT = nt.Args[1]
		}
	}
	if out.KeyT == nil {
		out.KeyT = ErrTypeVal
	}
	if out.ValT == nil {
		out.ValT = ErrTypeVal
	}
	return out
}

func (l *lowerer) lowerClosure(c *ast.ClosureExpr) Expr {
	out := &Closure{
		Return: l.lowerType(c.ReturnType),
		T:      l.exprType(c),
		SpanV:  nodeSpan(c),
	}
	if out.Return == nil {
		out.Return = TUnit
	}
	for _, p := range c.Params {
		out.Params = append(out.Params, l.lowerParam(p))
	}
	// Inline closures (`|acc, n| ...`) leave the per-param AST Type
	// nil because the user didn't annotate them — the checker's
	// inference picks them up via the call-site context (e.g.
	// `xs.fold(0, |acc, n| ...)` resolves to fn(Int, Int) -> Int).
	// `lowerParam` faithfully forwards the nil, but downstream IR
	// validation rejects nil param types ("Closure: param[i] nil
	// Type"). The checker's inferred FnType is in out.T already, so
	// backfill missing param types from there.
	//
	// Closure return is filled the same way when no explicit
	// annotation is present and the inferred FnType has a Return.
	if fnT, ok := out.T.(*FnType); ok && fnT != nil {
		for i, p := range out.Params {
			if p == nil || p.Type != nil || i >= len(fnT.Params) {
				continue
			}
			p.Type = fnT.Params[i]
		}
		if c.ReturnType == nil && fnT.Return != nil && out.Return == TUnit {
			out.Return = fnT.Return
		}
	}
	// Body is always an expression. Wrap non-block bodies in a synthetic
	// block with the expression as the Result.
	if blk, ok := c.Body.(*ast.Block); ok {
		out.Body = l.lowerBlock(blk)
	} else {
		lowered := l.lowerExpr(c.Body)
		out.Body = &Block{Result: lowered, SpanV: lowered.At()}
	}
	// Compute free-variable captures.
	out.Captures = ComputeCaptures(out.Body, out.Params)
	return out
}

func (l *lowerer) lowerTurbofish(tf *ast.TurbofishExpr) Expr {
	// A bare turbofish without a call (`f::<Int>`) — retain the type
	// args on the underlying ident so backends that monomorphise off
	// function references can observe them.
	base := l.lowerExpr(tf.Base)
	typeArgs := make([]Type, 0, len(tf.Args))
	for _, a := range tf.Args {
		typeArgs = append(typeArgs, l.lowerType(a))
	}
	if id, ok := base.(*Ident); ok {
		id.TypeArgs = typeArgs
		return id
	}
	l.note("bare turbofish at %v attached to non-ident base; type args dropped", tf.Pos())
	return base
}

func (l *lowerer) lowerMatchStmt(m *ast.MatchExpr) Stmt {
	out := &MatchStmt{
		Scrutinee: l.lowerExpr(m.Scrutinee),
		Arms:      l.lowerMatchArms(m.Arms),
		SpanV:     nodeSpan(m),
	}
	out.Tree = CompileDecisionTree(out.Scrutinee.Type(), out.Arms)
	return out
}

func (l *lowerer) lowerMatchExpr(m *ast.MatchExpr) Expr {
	out := &MatchExpr{
		Scrutinee: l.lowerExpr(m.Scrutinee),
		T:         l.exprType(m),
		SpanV:     nodeSpan(m),
	}
	out.Arms = l.lowerMatchArms(m.Arms)
	// Recover the match type from its arm bodies when the checker
	// left it as <error>. The checker's type inference for match
	// expressions across large arm sets sometimes loses the common
	// arm type (observed on toolchain/core.osty `corePrintNodeBody`
	// and similar dispatch tables), which then poisons every
	// downstream operation consuming the match result. Unifying from
	// arm bodies keeps the MIR fast path live as long as every arm
	// resolved to the same concrete type.
	if out.T == nil || out.T == ErrTypeVal {
		if recovered := recoverMatchType(out.Arms); recovered != nil && recovered != ErrTypeVal {
			out.T = recovered
		}
	}
	// Compile a decision tree when the arm shapes are specialisable.
	out.Tree = CompileDecisionTree(out.Scrutinee.Type(), out.Arms)
	return out
}

// recoverMatchType returns the common body type across a set of
// match arms, or ErrTypeVal when arms disagree / are not available.
// Used as a fallback when the checker didn't record a type for the
// enclosing match expression. A Block's yielded type is its Result
// expression's type (or TUnit when Result is nil).
func recoverMatchType(arms []*MatchArm) Type {
	var candidate Type
	for _, arm := range arms {
		if arm == nil || arm.Body == nil {
			continue
		}
		t := blockResultType(arm.Body)
		if t == nil || t == ErrTypeVal {
			continue
		}
		if candidate == nil {
			candidate = t
			continue
		}
		if !typesEquivalent(candidate, t) {
			return ErrTypeVal
		}
	}
	if candidate == nil {
		return ErrTypeVal
	}
	return candidate
}

// typesEquivalent is a narrow equality suitable for match-arm
// unification. It's intentionally strict: primitive kinds must match
// exactly, named types compare by (package, name, builtin) tuple.
// Structural types (tuples, optionals, fn types) recurse. Anything
// involving ErrType short-circuits to false so recovery doesn't
// silently accept a poisoned arm.
func typesEquivalent(a, b Type) bool {
	if a == nil || b == nil {
		return false
	}
	if a == ErrTypeVal || b == ErrTypeVal {
		return false
	}
	switch ax := a.(type) {
	case *PrimType:
		bx, ok := b.(*PrimType)
		return ok && ax.Kind == bx.Kind
	case *NamedType:
		bx, ok := b.(*NamedType)
		if !ok || ax.Name != bx.Name || ax.Package != bx.Package || ax.Builtin != bx.Builtin {
			return false
		}
		if len(ax.Args) != len(bx.Args) {
			return false
		}
		for i := range ax.Args {
			if !typesEquivalent(ax.Args[i], bx.Args[i]) {
				return false
			}
		}
		return true
	case *OptionalType:
		bx, ok := b.(*OptionalType)
		return ok && typesEquivalent(ax.Inner, bx.Inner)
	case *TupleType:
		bx, ok := b.(*TupleType)
		if !ok || len(ax.Elems) != len(bx.Elems) {
			return false
		}
		for i := range ax.Elems {
			if !typesEquivalent(ax.Elems[i], bx.Elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (l *lowerer) lowerMatchArms(arms []*ast.MatchArm) []*MatchArm {
	out := make([]*MatchArm, 0, len(arms))
	for _, arm := range arms {
		a := &MatchArm{
			Pattern: l.lowerPattern(arm.Pattern),
			SpanV:   Span{Start: posFromToken(arm.Pos()), End: posFromToken(arm.End())},
		}
		if arm.Guard != nil {
			a.Guard = l.lowerExpr(arm.Guard)
		}
		a.Body = l.lowerArmBody(arm.Body)
		out = append(out, a)
	}
	return out
}

// lowerArmBody normalises a match-arm body (expression or block) into a
// *Block so consumers see a uniform shape.
func (l *lowerer) lowerArmBody(e ast.Expr) *Block {
	if blk, ok := e.(*ast.Block); ok {
		return l.lowerBlock(blk)
	}
	lowered := l.lowerExpr(e)
	return &Block{Result: lowered, SpanV: lowered.At()}
}

// ==== Patterns ====

func (l *lowerer) lowerPattern(p ast.Pattern) Pattern {
	if p == nil {
		return nil
	}
	switch p := p.(type) {
	case *ast.WildcardPat:
		return &WildPat{SpanV: nodeSpan(p)}
	case *ast.IdentPat:
		return &IdentPat{Name: p.Name, SpanV: nodeSpan(p)}
	case *ast.LiteralPat:
		var val Expr
		if p.Literal != nil {
			val = l.lowerExpr(p.Literal)
		}
		return &LitPat{Value: val, SpanV: nodeSpan(p)}
	case *ast.TuplePat:
		out := &TuplePat{SpanV: nodeSpan(p)}
		for _, e := range p.Elems {
			out.Elems = append(out.Elems, l.lowerPattern(e))
		}
		return out
	case *ast.StructPat:
		out := &StructPat{Rest: p.Rest, SpanV: nodeSpan(p)}
		if len(p.Type) > 0 {
			out.TypeName = p.Type[len(p.Type)-1]
		}
		for _, f := range p.Fields {
			field := StructPatField{
				Name: f.Name,
				SpanV: Span{
					Start: posFromToken(f.Pos()),
					End:   posFromToken(f.End()),
				},
			}
			if f.Pattern != nil {
				field.Pattern = l.lowerPattern(f.Pattern)
			}
			out.Fields = append(out.Fields, field)
		}
		return out
	case *ast.VariantPat:
		out := &VariantPat{SpanV: nodeSpan(p)}
		if n := len(p.Path); n >= 1 {
			out.Variant = p.Path[n-1]
			if n >= 2 {
				out.Enum = p.Path[n-2]
			}
		}
		for _, a := range p.Args {
			out.Args = append(out.Args, l.lowerPattern(a))
		}
		return out
	case *ast.RangePat:
		out := &RangePat{Inclusive: p.Inclusive, SpanV: nodeSpan(p)}
		if p.Start != nil {
			out.Low = l.lowerExpr(p.Start)
		}
		if p.Stop != nil {
			out.High = l.lowerExpr(p.Stop)
		}
		return out
	case *ast.OrPat:
		out := &OrPat{SpanV: nodeSpan(p)}
		for _, a := range p.Alts {
			out.Alts = append(out.Alts, l.lowerPattern(a))
		}
		return out
	case *ast.BindingPat:
		return &BindingPat{
			Name:    p.Name,
			Pattern: l.lowerPattern(p.Pattern),
			SpanV:   nodeSpan(p),
		}
	}
	l.note("unsupported pattern %T at %v", p, p.Pos())
	return &ErrorPat{Note: fmt.Sprintf("%T", p), SpanV: nodeSpan(p)}
}
