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
	case *ast.UseDecl, *ast.InterfaceDecl, *ast.TypeAliasDecl:
		// Out of scope for the current IR shape. Not an error — these
		// are either consumed by the resolver (Use) or reserved for a
		// later IR expansion (Interface, TypeAlias).
		return nil
	}
	l.note("unsupported top-level decl %T", d)
	return nil
}

func (l *lowerer) lowerFnDecl(fn *ast.FnDecl) *FnDecl {
	out := &FnDecl{
		Name:     fn.Name,
		Return:   l.lowerType(fn.ReturnType),
		Exported: fn.Pub,
		SpanV:    nodeSpan(fn),
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

func (l *lowerer) lowerParam(p *ast.Param) *Param {
	name := p.Name
	if name == "" && p.Pattern != nil {
		// Pattern-destructured closure params aren't representable yet;
		// record an issue and fall back to a placeholder.
		l.note("pattern-destructured parameter at %v not yet supported", p.Pos())
		name = "_"
	}
	out := &Param{
		Name:  name,
		Type:  l.lowerType(p.Type),
		SpanV: nodeSpan(p),
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
	// Qualified paths (pkg.Type) are kept as a dotted string for now;
	// the current IR has no first-class package concept.
	name := nt.Path[len(nt.Path)-1]

	// Primitive scalars short-circuit.
	if len(nt.Path) == 1 && len(nt.Args) == 0 {
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
			return &NamedType{Name: sym.Name, Args: args, Builtin: true}
		case resolve.SymGeneric:
			return &TypeVar{Name: sym.Name, Owner: ""}
		}
		return &NamedType{Name: sym.Name, Args: args}
	}

	// No resolver data available — best effort on the source name.
	return &NamedType{Name: name, Args: args}
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
		if t.Sym != nil {
			name = t.Sym.Name
			builtin = t.Sym.Kind == resolve.SymBuiltin
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = l.fromCheckerType(a)
		}
		return &NamedType{Name: name, Args: args, Builtin: builtin}
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
	}
	// Fall through: if it's actually an if used at statement position,
	// the parser wrapped it in an ExprStmt already; we only get here
	// for deferred constructs.
	l.note("unsupported statement %T at %v", s, s.Pos())
	return &ErrorStmt{Note: fmt.Sprintf("%T", s), SpanV: nodeSpan(s)}
}

func (l *lowerer) lowerLetStmt(s *ast.LetStmt) Stmt {
	// Only bare-ident patterns are lowered directly; destructuring
	// patterns aren't representable in the current IR shape.
	name, ok := simpleBindName(s.Pattern)
	if !ok {
		l.note("destructuring `let` at %v not yet lowered", s.Pos())
		return &ErrorStmt{Note: "destructuring let", SpanV: nodeSpan(s)}
	}
	out := &LetStmt{
		Name:  name,
		Mut:   s.Mut,
		SpanV: nodeSpan(s),
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
	if len(s.Targets) != 1 {
		l.note("multi-target assignment at %v not yet lowered", s.Pos())
		return &ErrorStmt{Note: "multi-assign", SpanV: nodeSpan(s)}
	}
	return &AssignStmt{
		Op:     assignOp(s.Op),
		Target: l.lowerExpr(s.Targets[0]),
		Value:  l.lowerExpr(s.Value),
		SpanV:  nodeSpan(s),
	}
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
	name, ok := simpleBindName(s.Pattern)
	if !ok {
		l.note("for with destructuring pattern at %v not yet lowered", s.Pos())
		return &ErrorStmt{Note: "for destructuring", SpanV: nodeSpan(s)}
	}
	// for x in a..b is a numeric range loop.
	if r, ok := s.Iter.(*ast.RangeExpr); ok && r.Start != nil && r.Stop != nil {
		return &ForStmt{
			Kind:      ForRange,
			Var:       name,
			Start:     l.lowerExpr(r.Start),
			End:       l.lowerExpr(r.Stop),
			Inclusive: r.Inclusive,
			Body:      body,
			SpanV:     nodeSpan(s),
		}
	}
	return &ForStmt{Kind: ForIn, Var: name, Iter: l.lowerExpr(s.Iter), Body: body, SpanV: nodeSpan(s)}
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
		// Parens carry no semantic content in the IR.
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
			out.Kind = identKind(sym.Kind)
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

func identKind(k resolve.SymbolKind) IdentKind {
	switch k {
	case resolve.SymLet:
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
	// Detect a print-family intrinsic.
	if id, ok := e.Fn.(*ast.Ident); ok {
		if k, isIntrinsic := intrinsicByName(id.Name); isIntrinsic {
			out := &IntrinsicCall{Kind: k, SpanV: nodeSpan(e)}
			for _, a := range e.Args {
				if a.Name != "" {
					l.note("keyword arg to intrinsic %s at %v not supported", id.Name, a.Pos())
					continue
				}
				out.Args = append(out.Args, l.lowerExpr(a.Value))
			}
			return out
		}
	}
	out := &CallExpr{
		Callee: l.lowerExpr(e.Fn),
		T:      l.exprType(e),
		SpanV:  nodeSpan(e),
	}
	for _, a := range e.Args {
		if a.Name != "" {
			l.note("keyword arg at %v collapsed to positional in IR", a.Pos())
		}
		out.Args = append(out.Args, l.lowerExpr(a.Value))
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
	var elseBlk *Block
	switch alt := e.Else.(type) {
	case nil:
		// no else; should only appear in statement position.
	case *ast.Block:
		elseBlk = l.lowerBlock(alt)
	case *ast.IfExpr:
		// else-if chain: wrap the inner IfExpr into a block so the
		// shape is uniform.
		inner := l.lowerIfExpr(alt)
		elseBlk = &Block{Result: inner, SpanV: nodeSpan(alt)}
	default:
		lowered := l.lowerExpr(alt.(ast.Expr))
		elseBlk = &Block{Result: lowered, SpanV: nodeSpan(alt)}
	}
	return &IfExpr{Cond: l.lowerExpr(e.Cond), Then: thenBlk, Else: elseBlk, T: t, SpanV: nodeSpan(e)}
}

// ==== Span helpers ====

func posFromToken(p token.Pos) Pos {
	return Pos{Offset: p.Offset, Line: p.Line, Column: p.Column}
}

func nodeSpan(n ast.Node) Span {
	return Span{Start: posFromToken(n.Pos()), End: posFromToken(n.End())}
}
