package ir

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

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

// lowerer holds per-file state for one Lower call.
type lowerer struct {
	pkgName string
	file    *ast.File
	res     *resolve.Result
	chk     *check.Result

	// issues collects non-fatal issues.
	issues []error
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
	out := &FnDecl{
		Name:         fn.Name,
		Return:       l.lowerType(fn.ReturnType),
		ReceiverMut:  fn.Recv != nil && fn.Recv.Mut,
		Exported:     fn.Pub,
		SpanV:        nodeSpan(fn),
		ExportSymbol: extractExportSymbol(fn.Annotations),
	}
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

// extractExportSymbol reads the `#[export("name")]` annotation from a
// declaration's annotation list (LANG_SPEC §19.6). Returns the empty
// string when the annotation is absent or malformed; the resolver's
// arg validator (`checkExportArgs` in internal/resolve) is the
// authoritative place that rejects a malformed `#[export]`, so at
// this point in the pipeline we only pick up the well-formed cases.
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
		out.Fields = append(out.Fields, field)
	}
	for _, m := range sd.Methods {
		out.Methods = append(out.Methods, l.lowerFnDecl(m))
	}
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
		}
		return &NamedType{Package: pkg, Name: sym.Name, Args: args}
	}

	// No resolver data available — best effort on the source name.
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
	if l.res == nil {
		return nil
	}
	return l.res.TypeRefs[nt]
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
	} else {
		out.Stmts = append(out.Stmts, l.lowerStmt(last))
	}
	return out
}

// expressionYieldsValue reports whether treating the expression as a
// block-final implicit result is appropriate: the checker assigned it
// a non-unit type.
func (l *lowerer) expressionYieldsValue(e ast.Expr) bool {
	if l.chk == nil {
		// No checker; be conservative: don't promote.
		return false
	}
	t := l.chk.Types[e]
	if t == nil {
		return false
	}
	if p, ok := t.(*types.Primitive); ok {
		if p.Kind == types.PUnit || p.Kind == types.PNever {
			return false
		}
	}
	if _, ok := t.(*types.Error); ok {
		return false
	}
	return true
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
		return &BreakStmt{SpanV: nodeSpan(s)}
	case *ast.ContinueStmt:
		return &ContinueStmt{SpanV: nodeSpan(s)}
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
	return out
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
		return &ForStmt{Kind: ForInfinite, Body: body, SpanV: nodeSpan(s)}
	}
	if s.Pattern == nil && s.Iter != nil {
		return &ForStmt{Kind: ForWhile, Cond: l.lowerExpr(s.Iter), Body: body, SpanV: nodeSpan(s)}
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
	switch e := e.(type) {
	case *ast.IntLit:
		return &IntLit{Text: e.Text, T: l.exprType(e), SpanV: nodeSpan(e)}
	case *ast.FloatLit:
		return &FloatLit{Text: e.Text, T: l.exprType(e), SpanV: nodeSpan(e)}
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
		return &IndexExpr{
			X:     l.lowerExpr(e.X),
			Index: l.lowerExpr(e.Index),
			T:     l.exprType(e),
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
		} else {
			out.Parts = append(out.Parts, StringPart{Expr: l.lowerExpr(p.Expr)})
		}
	}
	return out
}

func (l *lowerer) lowerIdent(id *ast.Ident) Expr {
	out := &Ident{Name: id.Name, SpanV: nodeSpan(id), T: ErrTypeVal}
	if l.res != nil {
		if sym := l.res.Refs[id]; sym != nil {
			out.Kind = identKind(sym)
		}
	}
	if l.chk != nil {
		if t := l.chk.Types[id]; t != nil {
			out.T = l.fromCheckerType(t)
		} else if sym := l.symbol(id); sym != nil {
			if st := l.chk.SymTypes[sym]; st != nil {
				out.T = l.fromCheckerType(st)
			}
		}
	}
	return out
}

func (l *lowerer) symbol(id *ast.Ident) *resolve.Symbol {
	if l.res == nil {
		return nil
	}
	return l.res.Refs[id]
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
	return &BinaryExpr{
		Op:    op,
		Left:  l.lowerExpr(e.Left),
		Right: l.lowerExpr(e.Right),
		T:     l.exprType(e),
		SpanV: nodeSpan(e),
	}
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
		if id, ok := fx.X.(*ast.Ident); ok {
			if sym := l.symbol(id); sym != nil &&
				(sym.Kind == resolve.SymEnum || sym.Kind == resolve.SymStruct) {
				if l.isVariantOfEnum(sym, fx.Name) {
					return l.lowerVariantCall(e, sym.Name, fx.Name)
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
	out := &CallExpr{
		Callee:   l.lowerExpr(fn),
		TypeArgs: typeArgs,
		T:        l.exprType(e),
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
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
	if l.chk == nil || l.chk.Instantiations == nil {
		return nil
	}
	raw, ok := l.chk.Instantiations[e]
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
func (l *lowerer) lowerMethodCall(e *ast.CallExpr, fx *ast.FieldExpr, typeArgs []Type) Expr {
	if len(typeArgs) == 0 {
		typeArgs = l.instantiationArgs(e)
	}
	out := &MethodCall{
		Receiver: l.lowerExpr(fx.X),
		Name:     fx.Name,
		TypeArgs: typeArgs,
		T:        l.exprType(e),
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
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
	if out.Elem == nil {
		out.Elem = ErrTypeVal
	}
	return out
}

func (l *lowerer) lowerIfExpr(e *ast.IfExpr) Expr {
	t := l.exprType(e)
	thenBlk := l.lowerBlock(e.Then)
	elseBlk := l.lowerElse(e.Else)
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
	return &FieldExpr{
		X:        l.lowerExpr(e.X),
		Name:     e.Name,
		Optional: e.IsOptional,
		T:        l.exprType(e),
		SpanV:    nodeSpan(e),
	}
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
	switch h := s.Type.(type) {
	case *ast.Ident:
		name = h.Name
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
	for _, f := range s.Fields {
		field := StructLitField{
			Name:  f.Name,
			SpanV: Span{Start: posFromToken(f.Pos()), End: posFromToken(f.End())},
		}
		if f.Value != nil {
			field.Value = l.lowerExpr(f.Value)
		}
		out.Fields = append(out.Fields, field)
	}
	if s.Spread != nil {
		out.Spread = l.lowerExpr(s.Spread)
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
	// Compile a decision tree when the arm shapes are specialisable.
	out.Tree = CompileDecisionTree(out.Scrutinee.Type(), out.Arms)
	return out
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
