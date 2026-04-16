package llvmgen

import (
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/token"
)

// GenerateModule is the sole public entry point of the LLVM backend.
// It consumes the backend-neutral IR (internal/ir) and emits textual
// LLVM IR.
//
// The implementation currently reifies the module back into a legacy
// AST shape through legacyFileFromModule and then hands off to the
// long-standing AST-driven emitter. This is a transitional detail:
// external callers route through IR only, and the in-package test
// helper generateFromAST is unexported. Once the emitter is rewritten
// to consume IR directly, the bridge and the AST helper both go away.
func GenerateModule(mod *ostyir.Module, opts Options) ([]byte, error) {
	if mod == nil {
		return nil, unsupported("source-layout", "nil module")
	}
	if diag, ok := moduleUnsupportedDiagnostic(mod); ok {
		return nil, &UnsupportedError{Diagnostic: diag}
	}
	file, err := legacyFileFromModule(mod)
	if err != nil {
		return nil, err
	}
	return generateASTFile(file, opts)
}

func moduleUnsupportedDiagnostic(mod *ostyir.Module) (UnsupportedDiagnostic, bool) {
	if mod == nil {
		return UnsupportedDiagnostic{}, false
	}
	for _, decl := range mod.Decls {
		use, ok := decl.(*ostyir.UseDecl)
		if !ok || use == nil {
			continue
		}
		if use.IsGoFFI {
			return UnsupportedDiagnosticFor("go-ffi", use.GoPath), true
		}
		if use.IsRuntimeFFI && !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
			return UnsupportedDiagnosticFor("runtime-ffi", use.RuntimePath), true
		}
	}
	return UnsupportedDiagnostic{}, false
}

func legacyFileFromModule(mod *ostyir.Module) (*ast.File, error) {
	start, end := legacySpan(mod.At())
	file := &ast.File{PosV: start, EndV: end}
	for _, decl := range mod.Decls {
		legacyDecl, err := legacyDeclFromIR(decl)
		if err != nil {
			return nil, err
		}
		if legacyDecl == nil {
			continue
		}
		if use, ok := legacyDecl.(*ast.UseDecl); ok {
			file.Uses = append(file.Uses, use)
			continue
		}
		file.Decls = append(file.Decls, legacyDecl)
	}
	for _, stmt := range mod.Script {
		legacyStmt, err := legacyStmtFromIR(stmt)
		if err != nil {
			return nil, err
		}
		if legacyStmt != nil {
			file.Stmts = append(file.Stmts, legacyStmt)
		}
	}
	return file, nil
}

func legacyDeclFromIR(decl ostyir.Decl) (ast.Decl, error) {
	switch d := decl.(type) {
	case nil:
		return nil, nil
	case *ostyir.UseDecl:
		return legacyUseDeclFromIR(d)
	case *ostyir.FnDecl:
		return legacyFnDeclFromIR(d, false)
	case *ostyir.StructDecl:
		return legacyStructDeclFromIR(d)
	case *ostyir.EnumDecl:
		return legacyEnumDeclFromIR(d)
	case *ostyir.InterfaceDecl:
		return legacyInterfaceDeclFromIR(d)
	case *ostyir.TypeAliasDecl:
		return legacyTypeAliasDeclFromIR(d)
	case *ostyir.LetDecl:
		return legacyLetDeclFromIR(d)
	default:
		return nil, unsupportedf("source-layout", "IR declaration %T", decl)
	}
}

func legacyUseDeclFromIR(d *ostyir.UseDecl) (ast.Decl, error) {
	if d == nil {
		return nil, nil
	}
	start, end := legacySpan(d.At())
	out := &ast.UseDecl{
		PosV:         start,
		EndV:         end,
		Path:         append([]string(nil), d.Path...),
		RawPath:      d.RawPath,
		Alias:        d.Alias,
		IsGoFFI:      d.IsGoFFI,
		IsRuntimeFFI: d.IsRuntimeFFI,
		GoPath:       d.GoPath,
		RuntimePath:  d.RuntimePath,
	}
	for _, inner := range d.GoBody {
		legacyInner, err := legacyDeclFromIR(inner)
		if err != nil {
			return nil, err
		}
		if legacyInner != nil {
			out.GoBody = append(out.GoBody, legacyInner)
		}
	}
	return out, nil
}

func legacyFnDeclFromIR(fn *ostyir.FnDecl, asMethod bool) (*ast.FnDecl, error) {
	if fn == nil {
		return nil, nil
	}
	start, end := legacySpan(fn.At())
	out := &ast.FnDecl{
		PosV:       start,
		EndV:       end,
		Pub:        fn.Exported,
		Name:       fn.Name,
		ReturnType: legacyTypeFromIR(fn.Return),
	}
	if asMethod {
		out.Recv = &ast.Receiver{PosV: start, EndV: start}
	}
	for _, gp := range fn.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, param := range fn.Params {
		legacyParam, err := legacyParamFromIR(param)
		if err != nil {
			return nil, err
		}
		out.Params = append(out.Params, legacyParam)
	}
	if fn.Body != nil {
		body, err := legacyBlockFromIR(fn.Body)
		if err != nil {
			return nil, err
		}
		out.Body = body
	}
	return out, nil
}

func legacyTypeParamFromIR(tp *ostyir.TypeParam) *ast.GenericParam {
	if tp == nil {
		return nil
	}
	start, end := legacySpan(tp.At())
	out := &ast.GenericParam{PosV: start, EndV: end, Name: tp.Name}
	for _, bound := range tp.Bounds {
		out.Constraints = append(out.Constraints, legacyTypeFromIR(bound))
	}
	return out
}

func legacyParamFromIR(param *ostyir.Param) (*ast.Param, error) {
	if param == nil {
		return nil, nil
	}
	start, end := legacySpan(param.At())
	out := &ast.Param{
		PosV: start,
		EndV: end,
		Name: param.Name,
		Type: legacyTypeFromIR(param.Type),
	}
	if param.Pattern != nil {
		pat, err := legacyPatternFromIR(param.Pattern)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
	}
	if param.Default != nil {
		value, err := legacyExprFromIR(param.Default)
		if err != nil {
			return nil, err
		}
		out.Default = value
	}
	return out, nil
}

func legacyStructDeclFromIR(sd *ostyir.StructDecl) (*ast.StructDecl, error) {
	if sd == nil {
		return nil, nil
	}
	start, end := legacySpan(sd.At())
	out := &ast.StructDecl{
		PosV: start,
		EndV: end,
		Pub:  sd.Exported,
		Name: sd.Name,
	}
	for _, gp := range sd.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, field := range sd.Fields {
		legacyField, err := legacyFieldFromIR(field)
		if err != nil {
			return nil, err
		}
		out.Fields = append(out.Fields, legacyField)
	}
	for _, method := range sd.Methods {
		legacyMethod, err := legacyFnDeclFromIR(method, true)
		if err != nil {
			return nil, err
		}
		out.Methods = append(out.Methods, legacyMethod)
	}
	return out, nil
}

func legacyFieldFromIR(field *ostyir.Field) (*ast.Field, error) {
	if field == nil {
		return nil, nil
	}
	start, end := legacySpan(field.At())
	out := &ast.Field{
		PosV: start,
		EndV: end,
		Pub:  field.Exported,
		Name: field.Name,
		Type: legacyTypeFromIR(field.Type),
	}
	if field.Default != nil {
		value, err := legacyExprFromIR(field.Default)
		if err != nil {
			return nil, err
		}
		out.Default = value
	}
	return out, nil
}

func legacyEnumDeclFromIR(ed *ostyir.EnumDecl) (*ast.EnumDecl, error) {
	if ed == nil {
		return nil, nil
	}
	start, end := legacySpan(ed.At())
	out := &ast.EnumDecl{
		PosV: start,
		EndV: end,
		Pub:  ed.Exported,
		Name: ed.Name,
	}
	for _, gp := range ed.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, variant := range ed.Variants {
		legacyVariant := legacyVariantFromIR(variant)
		out.Variants = append(out.Variants, legacyVariant)
	}
	for _, method := range ed.Methods {
		legacyMethod, err := legacyFnDeclFromIR(method, true)
		if err != nil {
			return nil, err
		}
		out.Methods = append(out.Methods, legacyMethod)
	}
	return out, nil
}

func legacyVariantFromIR(variant *ostyir.Variant) *ast.Variant {
	if variant == nil {
		return nil
	}
	start, end := legacySpan(variant.At())
	out := &ast.Variant{
		PosV: start,
		EndV: end,
		Name: variant.Name,
	}
	for _, payload := range variant.Payload {
		out.Fields = append(out.Fields, legacyTypeFromIR(payload))
	}
	return out
}

func legacyInterfaceDeclFromIR(id *ostyir.InterfaceDecl) (*ast.InterfaceDecl, error) {
	if id == nil {
		return nil, nil
	}
	start, end := legacySpan(id.At())
	out := &ast.InterfaceDecl{
		PosV: start,
		EndV: end,
		Pub:  id.Exported,
		Name: id.Name,
	}
	for _, gp := range id.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, ext := range id.Extends {
		out.Extends = append(out.Extends, legacyTypeFromIR(ext))
	}
	for _, method := range id.Methods {
		legacyMethod, err := legacyFnDeclFromIR(method, false)
		if err != nil {
			return nil, err
		}
		out.Methods = append(out.Methods, legacyMethod)
	}
	return out, nil
}

func legacyTypeAliasDeclFromIR(td *ostyir.TypeAliasDecl) (*ast.TypeAliasDecl, error) {
	if td == nil {
		return nil, nil
	}
	start, end := legacySpan(td.At())
	out := &ast.TypeAliasDecl{
		PosV:   start,
		EndV:   end,
		Pub:    td.Exported,
		Name:   td.Name,
		Target: legacyTypeFromIR(td.Target),
	}
	for _, gp := range td.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	return out, nil
}

func legacyLetDeclFromIR(ld *ostyir.LetDecl) (*ast.LetDecl, error) {
	if ld == nil {
		return nil, nil
	}
	start, end := legacySpan(ld.At())
	out := &ast.LetDecl{
		PosV: start,
		EndV: end,
		Pub:  ld.Exported,
		Mut:  ld.Mut,
		Name: ld.Name,
		Type: legacyTypeFromIR(ld.Type),
	}
	if ld.Value != nil {
		value, err := legacyExprFromIR(ld.Value)
		if err != nil {
			return nil, err
		}
		out.Value = value
	}
	return out, nil
}

func legacyTypeFromIR(typ ostyir.Type) ast.Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *ostyir.PrimType:
		name := legacyPrimTypeName(t.Kind)
		if name == "" {
			return nil
		}
		return &ast.NamedType{Path: []string{name}}
	case *ostyir.NamedType:
		path := splitQualifiedName(t.Package)
		path = append(path, t.Name)
		out := &ast.NamedType{Path: path}
		for _, arg := range t.Args {
			out.Args = append(out.Args, legacyTypeFromIR(arg))
		}
		return out
	case *ostyir.OptionalType:
		return &ast.OptionalType{Inner: legacyTypeFromIR(t.Inner)}
	case *ostyir.TupleType:
		out := &ast.TupleType{}
		for _, elem := range t.Elems {
			out.Elems = append(out.Elems, legacyTypeFromIR(elem))
		}
		return out
	case *ostyir.FnType:
		out := &ast.FnType{ReturnType: legacyTypeFromIR(t.Return)}
		for _, param := range t.Params {
			out.Params = append(out.Params, legacyTypeFromIR(param))
		}
		return out
	case *ostyir.TypeVar:
		return &ast.NamedType{Path: []string{t.Name}}
	case *ostyir.ErrType:
		return &ast.NamedType{Path: []string{"<error>"}}
	default:
		return nil
	}
}

func legacyPrimTypeName(kind ostyir.PrimKind) string {
	switch kind {
	case ostyir.PrimInt:
		return "Int"
	case ostyir.PrimInt8:
		return "Int8"
	case ostyir.PrimInt16:
		return "Int16"
	case ostyir.PrimInt32:
		return "Int32"
	case ostyir.PrimInt64:
		return "Int64"
	case ostyir.PrimUInt8:
		return "UInt8"
	case ostyir.PrimUInt16:
		return "UInt16"
	case ostyir.PrimUInt32:
		return "UInt32"
	case ostyir.PrimUInt64:
		return "UInt64"
	case ostyir.PrimByte:
		return "Byte"
	case ostyir.PrimFloat:
		return "Float"
	case ostyir.PrimFloat32:
		return "Float32"
	case ostyir.PrimFloat64:
		return "Float64"
	case ostyir.PrimBool:
		return "Bool"
	case ostyir.PrimChar:
		return "Char"
	case ostyir.PrimString:
		return "String"
	case ostyir.PrimBytes:
		return "Bytes"
	default:
		return ""
	}
}

func legacyBlockFromIR(block *ostyir.Block) (*ast.Block, error) {
	if block == nil {
		return nil, nil
	}
	start, end := legacySpan(block.At())
	out := &ast.Block{PosV: start, EndV: end}
	for _, stmt := range block.Stmts {
		legacyStmt, err := legacyStmtFromIR(stmt)
		if err != nil {
			return nil, err
		}
		if legacyStmt != nil {
			out.Stmts = append(out.Stmts, legacyStmt)
		}
	}
	if block.Result != nil {
		resultExpr, err := legacyExprFromIR(block.Result)
		if err != nil {
			return nil, err
		}
		out.Stmts = append(out.Stmts, &ast.ExprStmt{X: resultExpr})
	}
	return out, nil
}

func legacyStmtFromIR(stmt ostyir.Stmt) (ast.Stmt, error) {
	switch s := stmt.(type) {
	case nil:
		return nil, nil
	case *ostyir.Block:
		return legacyBlockFromIR(s)
	case *ostyir.LetStmt:
		return legacyLetStmtFromIR(s)
	case *ostyir.ExprStmt:
		expr, err := legacyExprFromIR(s.X)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	case *ostyir.AssignStmt:
		return legacyAssignStmtFromIR(s)
	case *ostyir.ReturnStmt:
		start, end := legacySpan(s.At())
		out := &ast.ReturnStmt{PosV: start, EndV: end}
		if s.Value != nil {
			value, err := legacyExprFromIR(s.Value)
			if err != nil {
				return nil, err
			}
			out.Value = value
		}
		return out, nil
	case *ostyir.BreakStmt:
		start, end := legacySpan(s.At())
		return &ast.BreakStmt{PosV: start, EndV: end}, nil
	case *ostyir.ContinueStmt:
		start, end := legacySpan(s.At())
		return &ast.ContinueStmt{PosV: start, EndV: end}, nil
	case *ostyir.IfStmt:
		return legacyIfStmtFromIR(s)
	case *ostyir.ForStmt:
		return legacyForStmtFromIR(s)
	case *ostyir.DeferStmt:
		return legacyDeferStmtFromIR(s)
	case *ostyir.MatchStmt:
		expr, err := legacyMatchExprFromIR(&ostyir.MatchExpr{
			Scrutinee: s.Scrutinee,
			Arms:      s.Arms,
			SpanV:     s.At(),
		})
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	default:
		return nil, unsupportedf("statement", "IR statement %T", stmt)
	}
}

func legacyLetStmtFromIR(stmt *ostyir.LetStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	out := &ast.LetStmt{
		PosV: start,
		EndV: end,
		Mut:  stmt.Mut,
		Type: legacyTypeFromIR(stmt.Type),
	}
	if stmt.Pattern != nil {
		pat, err := legacyPatternFromIR(stmt.Pattern)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
	} else {
		out.Pattern = &ast.IdentPat{PosV: start, EndV: end, Name: stmt.Name}
	}
	if stmt.Value != nil {
		value, err := legacyExprFromIR(stmt.Value)
		if err != nil {
			return nil, err
		}
		out.Value = value
	}
	return out, nil
}

func legacyAssignStmtFromIR(stmt *ostyir.AssignStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	out := &ast.AssignStmt{
		PosV: start,
		EndV: end,
		Op:   legacyAssignOp(stmt.Op),
	}
	for _, target := range stmt.Targets {
		legacyTarget, err := legacyExprFromIR(target)
		if err != nil {
			return nil, err
		}
		out.Targets = append(out.Targets, legacyTarget)
	}
	value, err := legacyExprFromIR(stmt.Value)
	if err != nil {
		return nil, err
	}
	out.Value = value
	return out, nil
}

func legacyAssignOp(op ostyir.AssignOp) token.Kind {
	switch op {
	case ostyir.AssignEq:
		return token.ASSIGN
	case ostyir.AssignAdd:
		return token.PLUSEQ
	case ostyir.AssignSub:
		return token.MINUSEQ
	case ostyir.AssignMul:
		return token.STAREQ
	case ostyir.AssignDiv:
		return token.SLASHEQ
	case ostyir.AssignMod:
		return token.PERCENTEQ
	case ostyir.AssignAnd:
		return token.BITANDEQ
	case ostyir.AssignOr:
		return token.BITOREQ
	case ostyir.AssignXor:
		return token.BITXOREQ
	case ostyir.AssignShl:
		return token.SHLEQ
	case ostyir.AssignShr:
		return token.SHREQ
	default:
		return token.ASSIGN
	}
}

func legacyIfStmtFromIR(stmt *ostyir.IfStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	cond, err := legacyExprFromIR(stmt.Cond)
	if err != nil {
		return nil, err
	}
	thenBlock, err := legacyBlockFromIR(stmt.Then)
	if err != nil {
		return nil, err
	}
	var elseExpr ast.Expr
	if stmt.Else != nil {
		elseBlock, err := legacyBlockFromIR(stmt.Else)
		if err != nil {
			return nil, err
		}
		if elseBlock != nil {
			elseExpr = elseBlock
		}
	}
	return &ast.ExprStmt{
		X: &ast.IfExpr{
			PosV: start,
			EndV: end,
			Cond: cond,
			Then: thenBlock,
			Else: elseExpr,
		},
	}, nil
}

func legacyForStmtFromIR(stmt *ostyir.ForStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	out := &ast.ForStmt{PosV: start, EndV: end}
	body, err := legacyBlockFromIR(stmt.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	switch stmt.Kind {
	case ostyir.ForInfinite:
		return out, nil
	case ostyir.ForWhile:
		iter, err := legacyExprFromIR(stmt.Cond)
		if err != nil {
			return nil, err
		}
		out.Iter = iter
		return out, nil
	case ostyir.ForRange:
		pat, err := legacyLoopPattern(stmt, start)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
		startExpr, err := legacyExprFromIR(stmt.Start)
		if err != nil {
			return nil, err
		}
		endExpr, err := legacyExprFromIR(stmt.End)
		if err != nil {
			return nil, err
		}
		out.Iter = &ast.RangeExpr{
			PosV:      start,
			EndV:      end,
			Start:     startExpr,
			Stop:      endExpr,
			Inclusive: stmt.Inclusive,
		}
		return out, nil
	case ostyir.ForIn:
		pat, err := legacyLoopPattern(stmt, start)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
		iter, err := legacyExprFromIR(stmt.Iter)
		if err != nil {
			return nil, err
		}
		out.Iter = iter
		return out, nil
	default:
		return nil, unsupportedf("control-flow", "IR for-kind %d", stmt.Kind)
	}
}

// legacyLoopPattern bridges a for-loop binding back to ast.Pattern.
func legacyLoopPattern(stmt *ostyir.ForStmt, pos token.Pos) (ast.Pattern, error) {
	if stmt.Pattern != nil {
		pat, err := legacyPatternFromIR(stmt.Pattern)
		if err != nil {
			return nil, err
		}
		return pat, nil
	}
	return &ast.IdentPat{PosV: pos, EndV: pos, Name: stmt.Var}, nil
}

func legacyDeferStmtFromIR(stmt *ostyir.DeferStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	body, err := legacyBlockFromIR(stmt.Body)
	if err != nil {
		return nil, err
	}
	return &ast.DeferStmt{PosV: start, EndV: end, X: body}, nil
}

func legacyExprFromIR(expr ostyir.Expr) (ast.Expr, error) {
	switch e := expr.(type) {
	case nil:
		return nil, nil
	case *ostyir.IntLit:
		start, end := legacySpan(e.At())
		return &ast.IntLit{PosV: start, EndV: end, Text: e.Text}, nil
	case *ostyir.FloatLit:
		start, end := legacySpan(e.At())
		return &ast.FloatLit{PosV: start, EndV: end, Text: e.Text}, nil
	case *ostyir.BoolLit:
		start, end := legacySpan(e.At())
		return &ast.BoolLit{PosV: start, EndV: end, Value: e.Value}, nil
	case *ostyir.CharLit:
		start, end := legacySpan(e.At())
		return &ast.CharLit{PosV: start, EndV: end, Value: e.Value}, nil
	case *ostyir.ByteLit:
		start, end := legacySpan(e.At())
		return &ast.ByteLit{PosV: start, EndV: end, Value: e.Value}, nil
	case *ostyir.StringLit:
		return legacyStringLitFromIR(e)
	case *ostyir.UnitLit:
		return nil, unsupported("expression", "unit literals are not yet supported in the LLVM IR bridge")
	case *ostyir.Ident:
		start, end := legacySpan(e.At())
		return &ast.Ident{PosV: start, EndV: end, Name: e.Name}, nil
	case *ostyir.UnaryExpr:
		return legacyUnaryExprFromIR(e)
	case *ostyir.BinaryExpr:
		return legacyBinaryExprFromIR(e)
	case *ostyir.CallExpr:
		return legacyCallExprFromIR(e)
	case *ostyir.IntrinsicCall:
		return legacyIntrinsicCallFromIR(e)
	case *ostyir.ListLit:
		return legacyListLitFromIR(e)
	case *ostyir.BlockExpr:
		return legacyBlockFromIR(e.Block)
	case *ostyir.IfExpr:
		return legacyIfExprFromIR(e)
	case *ostyir.ErrorExpr:
		return nil, unsupportedf("expression", "IR error expression: %s", e.Note)
	case *ostyir.FieldExpr:
		return legacyFieldExprFromIR(e)
	case *ostyir.IndexExpr:
		return legacyIndexExprFromIR(e)
	case *ostyir.MethodCall:
		return legacyMethodCallFromIR(e)
	case *ostyir.StructLit:
		return legacyStructLitFromIR(e)
	case *ostyir.TupleLit:
		return legacyTupleLitFromIR(e)
	case *ostyir.MapLit:
		return legacyMapLitFromIR(e)
	case *ostyir.RangeLit:
		return legacyRangeLitFromIR(e)
	case *ostyir.QuestionExpr:
		return legacyQuestionExprFromIR(e)
	case *ostyir.CoalesceExpr:
		return legacyCoalesceExprFromIR(e)
	case *ostyir.Closure:
		return legacyClosureFromIR(e)
	case *ostyir.VariantLit:
		return legacyVariantLitFromIR(e)
	case *ostyir.MatchExpr:
		return legacyMatchExprFromIR(e)
	case *ostyir.IfLetExpr:
		return legacyIfLetExprFromIR(e)
	case *ostyir.TupleAccess:
		return legacyTupleAccessFromIR(e)
	default:
		return nil, unsupportedf("expression", "IR expression %T", expr)
	}
}

func legacyStringLitFromIR(lit *ostyir.StringLit) (ast.Expr, error) {
	if lit == nil {
		return nil, nil
	}
	start, end := legacySpan(lit.At())
	out := &ast.StringLit{
		PosV:     start,
		EndV:     end,
		IsRaw:    lit.IsRaw,
		IsTriple: lit.IsTriple,
	}
	for _, part := range lit.Parts {
		entry := ast.StringPart{IsLit: part.IsLit, Lit: part.Lit}
		if !part.IsLit {
			expr, err := legacyExprFromIR(part.Expr)
			if err != nil {
				return nil, err
			}
			entry.Expr = expr
		}
		out.Parts = append(out.Parts, entry)
	}
	return out, nil
}

func legacyUnaryExprFromIR(expr *ostyir.UnaryExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	inner, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.UnaryExpr{
		PosV: start,
		EndV: end,
		Op:   legacyUnaryOp(expr.Op),
		X:    inner,
	}, nil
}

func legacyBinaryExprFromIR(expr *ostyir.BinaryExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	left, err := legacyExprFromIR(expr.Left)
	if err != nil {
		return nil, err
	}
	right, err := legacyExprFromIR(expr.Right)
	if err != nil {
		return nil, err
	}
	return &ast.BinaryExpr{
		PosV:  start,
		EndV:  end,
		Op:    legacyBinaryOp(expr.Op),
		Left:  left,
		Right: right,
	}, nil
}

func legacyCallExprFromIR(expr *ostyir.CallExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	fn, err := legacyExprFromIR(expr.Callee)
	if err != nil {
		return nil, err
	}
	if len(expr.TypeArgs) > 0 {
		tf := &ast.TurbofishExpr{PosV: start, EndV: end, Base: fn}
		for _, ta := range expr.TypeArgs {
			tf.Args = append(tf.Args, legacyTypeFromIR(ta))
		}
		fn = tf
	}
	out := &ast.CallExpr{PosV: start, EndV: end, Fn: fn}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

func legacyIntrinsicCallFromIR(expr *ostyir.IntrinsicCall) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.CallExpr{
		PosV: start,
		EndV: end,
		Fn: &ast.Ident{
			PosV: start,
			EndV: start,
			Name: legacyIntrinsicName(expr.Kind),
		},
	}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

// legacyIRArg bridges an ir.Arg into an ast.Arg.
func legacyIRArg(arg ostyir.Arg) (*ast.Arg, error) {
	value, err := legacyExprFromIR(arg.Value)
	if err != nil {
		return nil, err
	}
	pos := value.Pos()
	if arg.SpanV.Start.Line != 0 {
		pos = legacyPos(arg.SpanV.Start)
	}
	return &ast.Arg{PosV: pos, Name: arg.Name, Value: value}, nil
}

func legacyListLitFromIR(expr *ostyir.ListLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.ListExpr{PosV: start, EndV: end}
	for _, elem := range expr.Elems {
		value, err := legacyExprFromIR(elem)
		if err != nil {
			return nil, err
		}
		out.Elems = append(out.Elems, value)
	}
	return out, nil
}

func legacyIfExprFromIR(expr *ostyir.IfExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	cond, err := legacyExprFromIR(expr.Cond)
	if err != nil {
		return nil, err
	}
	thenBlock, err := legacyBlockFromIR(expr.Then)
	if err != nil {
		return nil, err
	}
	elseBlock, err := legacyBlockFromIR(expr.Else)
	if err != nil {
		return nil, err
	}
	var elseExpr ast.Expr
	if elseBlock != nil {
		elseExpr = elseBlock
	}
	return &ast.IfExpr{
		PosV: start,
		EndV: end,
		Cond: cond,
		Then: thenBlock,
		Else: elseExpr,
	}, nil
}

func legacyFieldExprFromIR(expr *ostyir.FieldExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	base, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.FieldExpr{
		PosV:       start,
		EndV:       end,
		X:          base,
		Name:       expr.Name,
		IsOptional: expr.Optional,
	}, nil
}

func legacyIndexExprFromIR(expr *ostyir.IndexExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	base, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	index, err := legacyExprFromIR(expr.Index)
	if err != nil {
		return nil, err
	}
	return &ast.IndexExpr{
		PosV:  start,
		EndV:  end,
		X:     base,
		Index: index,
	}, nil
}

func legacyMethodCallFromIR(expr *ostyir.MethodCall) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	receiver, err := legacyExprFromIR(expr.Receiver)
	if err != nil {
		return nil, err
	}
	fn := ast.Expr(&ast.FieldExpr{
		PosV: start,
		EndV: end,
		X:    receiver,
		Name: expr.Name,
	})
	if len(expr.TypeArgs) != 0 {
		tf := &ast.TurbofishExpr{PosV: start, EndV: end, Base: fn}
		for _, arg := range expr.TypeArgs {
			tf.Args = append(tf.Args, legacyTypeFromIR(arg))
		}
		fn = tf
	}
	out := &ast.CallExpr{PosV: start, EndV: end, Fn: fn}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

func legacyStructLitFromIR(expr *ostyir.StructLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.StructLit{
		PosV: start,
		EndV: end,
		Type: legacyTypeExpr(expr.TypeName, start, end),
	}
	for _, field := range expr.Fields {
		legacyField := &ast.StructLitField{PosV: legacyPos(field.At().Start), Name: field.Name}
		if field.Value != nil {
			value, err := legacyExprFromIR(field.Value)
			if err != nil {
				return nil, err
			}
			legacyField.Value = value
		}
		out.Fields = append(out.Fields, legacyField)
	}
	if expr.Spread != nil {
		spread, err := legacyExprFromIR(expr.Spread)
		if err != nil {
			return nil, err
		}
		out.Spread = spread
	}
	return out, nil
}

func legacyTupleLitFromIR(expr *ostyir.TupleLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.TupleExpr{PosV: start, EndV: end}
	for _, elem := range expr.Elems {
		value, err := legacyExprFromIR(elem)
		if err != nil {
			return nil, err
		}
		out.Elems = append(out.Elems, value)
	}
	return out, nil
}

func legacyMapLitFromIR(expr *ostyir.MapLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.MapExpr{PosV: start, EndV: end, Empty: len(expr.Entries) == 0}
	for _, entry := range expr.Entries {
		key, err := legacyExprFromIR(entry.Key)
		if err != nil {
			return nil, err
		}
		value, err := legacyExprFromIR(entry.Value)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, &ast.MapEntry{Key: key, Value: value})
	}
	return out, nil
}

func legacyRangeLitFromIR(expr *ostyir.RangeLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.RangeExpr{PosV: start, EndV: end, Inclusive: expr.Inclusive}
	var err error
	if expr.Start != nil {
		out.Start, err = legacyExprFromIR(expr.Start)
		if err != nil {
			return nil, err
		}
	}
	if expr.End != nil {
		out.Stop, err = legacyExprFromIR(expr.End)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func legacyQuestionExprFromIR(expr *ostyir.QuestionExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	inner, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.QuestionExpr{PosV: start, EndV: end, X: inner}, nil
}

func legacyCoalesceExprFromIR(expr *ostyir.CoalesceExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	left, err := legacyExprFromIR(expr.Left)
	if err != nil {
		return nil, err
	}
	right, err := legacyExprFromIR(expr.Right)
	if err != nil {
		return nil, err
	}
	return &ast.BinaryExpr{
		PosV:  start,
		EndV:  end,
		Op:    token.QQ,
		Left:  left,
		Right: right,
	}, nil
}

func legacyClosureFromIR(expr *ostyir.Closure) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.ClosureExpr{
		PosV:       start,
		EndV:       end,
		ReturnType: legacyTypeFromIR(expr.Return),
	}
	for _, param := range expr.Params {
		legacyParam, err := legacyParamFromIR(param)
		if err != nil {
			return nil, err
		}
		out.Params = append(out.Params, legacyParam)
	}
	body, err := legacyBlockFromIR(expr.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

func legacyVariantLitFromIR(expr *ostyir.VariantLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	callee := ast.Expr(&ast.Ident{PosV: start, EndV: end, Name: expr.Variant})
	if expr.Enum != "" {
		callee = &ast.FieldExpr{
			PosV: start,
			EndV: end,
			X:    &ast.Ident{PosV: start, EndV: start, Name: expr.Enum},
			Name: expr.Variant,
		}
	}
	if len(expr.Args) == 0 {
		return callee, nil
	}
	out := &ast.CallExpr{PosV: start, EndV: end, Fn: callee}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

func legacyMatchExprFromIR(expr *ostyir.MatchExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	scrutinee, err := legacyExprFromIR(expr.Scrutinee)
	if err != nil {
		return nil, err
	}
	out := &ast.MatchExpr{PosV: start, EndV: end, Scrutinee: scrutinee}
	for _, arm := range expr.Arms {
		legacyArm, err := legacyMatchArmFromIR(arm)
		if err != nil {
			return nil, err
		}
		out.Arms = append(out.Arms, legacyArm)
	}
	return out, nil
}

func legacyMatchArmFromIR(arm *ostyir.MatchArm) (*ast.MatchArm, error) {
	if arm == nil {
		return nil, nil
	}
	start, _ := legacySpan(arm.At())
	pattern, err := legacyPatternFromIR(arm.Pattern)
	if err != nil {
		return nil, err
	}
	out := &ast.MatchArm{PosV: start, Pattern: pattern}
	if arm.Guard != nil {
		guard, err := legacyExprFromIR(arm.Guard)
		if err != nil {
			return nil, err
		}
		out.Guard = guard
	}
	body, err := legacyBlockFromIR(arm.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

func legacyIfLetExprFromIR(expr *ostyir.IfLetExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	pattern, err := legacyPatternFromIR(expr.Pattern)
	if err != nil {
		return nil, err
	}
	scrutinee, err := legacyExprFromIR(expr.Scrutinee)
	if err != nil {
		return nil, err
	}
	thenBlock, err := legacyBlockFromIR(expr.Then)
	if err != nil {
		return nil, err
	}
	elseBlock, err := legacyBlockFromIR(expr.Else)
	if err != nil {
		return nil, err
	}
	var elseExpr ast.Expr
	if elseBlock != nil {
		elseExpr = elseBlock
	}
	return &ast.IfExpr{
		PosV:    start,
		EndV:    end,
		IsIfLet: true,
		Pattern: pattern,
		Cond:    scrutinee,
		Then:    thenBlock,
		Else:    elseExpr,
	}, nil
}

func legacyTupleAccessFromIR(expr *ostyir.TupleAccess) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	base, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.FieldExpr{
		PosV: start,
		EndV: end,
		X:    base,
		Name: strconv.Itoa(expr.Index),
	}, nil
}

func legacyPatternFromIR(pattern ostyir.Pattern) (ast.Pattern, error) {
	switch p := pattern.(type) {
	case nil:
		return nil, nil
	case *ostyir.WildPat:
		start, end := legacySpan(p.At())
		return &ast.WildcardPat{PosV: start, EndV: end}, nil
	case *ostyir.IdentPat:
		start, end := legacySpan(p.At())
		return &ast.IdentPat{PosV: start, EndV: end, Name: p.Name}, nil
	case *ostyir.LitPat:
		start, end := legacySpan(p.At())
		lit, err := legacyExprFromIR(p.Value)
		if err != nil {
			return nil, err
		}
		return &ast.LiteralPat{PosV: start, EndV: end, Literal: lit}, nil
	case *ostyir.TuplePat:
		start, end := legacySpan(p.At())
		out := &ast.TuplePat{PosV: start, EndV: end}
		for _, elem := range p.Elems {
			legacyElem, err := legacyPatternFromIR(elem)
			if err != nil {
				return nil, err
			}
			out.Elems = append(out.Elems, legacyElem)
		}
		return out, nil
	case *ostyir.StructPat:
		start, end := legacySpan(p.At())
		out := &ast.StructPat{
			PosV: start,
			EndV: end,
			Type: splitQualifiedName(p.TypeName),
			Rest: p.Rest,
		}
		for _, field := range p.Fields {
			pat, err := legacyPatternFromIR(field.Pattern)
			if err != nil {
				return nil, err
			}
			out.Fields = append(out.Fields, &ast.StructPatField{
				PosV:    legacyPos(field.At().Start),
				Name:    field.Name,
				Pattern: pat,
			})
		}
		return out, nil
	case *ostyir.VariantPat:
		start, end := legacySpan(p.At())
		out := &ast.VariantPat{PosV: start, EndV: end}
		if p.Enum != "" {
			out.Path = append(out.Path, p.Enum)
		}
		out.Path = append(out.Path, p.Variant)
		for _, arg := range p.Args {
			legacyArg, err := legacyPatternFromIR(arg)
			if err != nil {
				return nil, err
			}
			out.Args = append(out.Args, legacyArg)
		}
		return out, nil
	case *ostyir.RangePat:
		start, end := legacySpan(p.At())
		out := &ast.RangePat{PosV: start, EndV: end, Inclusive: p.Inclusive}
		var err error
		if p.Low != nil {
			out.Start, err = legacyExprFromIR(p.Low)
			if err != nil {
				return nil, err
			}
		}
		if p.High != nil {
			out.Stop, err = legacyExprFromIR(p.High)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case *ostyir.OrPat:
		start, end := legacySpan(p.At())
		out := &ast.OrPat{PosV: start, EndV: end}
		for _, alt := range p.Alts {
			legacyAlt, err := legacyPatternFromIR(alt)
			if err != nil {
				return nil, err
			}
			out.Alts = append(out.Alts, legacyAlt)
		}
		return out, nil
	case *ostyir.BindingPat:
		start, end := legacySpan(p.At())
		inner, err := legacyPatternFromIR(p.Pattern)
		if err != nil {
			return nil, err
		}
		return &ast.BindingPat{
			PosV:    start,
			EndV:    end,
			Name:    p.Name,
			Pattern: inner,
		}, nil
	case *ostyir.ErrorPat:
		return nil, unsupportedf("pattern", "IR error pattern: %s", p.Note)
	default:
		return nil, unsupportedf("pattern", "IR pattern %T", pattern)
	}
}

func legacyUnaryOp(op ostyir.UnOp) token.Kind {
	switch op {
	case ostyir.UnNeg:
		return token.MINUS
	case ostyir.UnPlus:
		return token.PLUS
	case ostyir.UnNot:
		return token.NOT
	case ostyir.UnBitNot:
		return token.BITNOT
	default:
		return token.ILLEGAL
	}
}

func legacyBinaryOp(op ostyir.BinOp) token.Kind {
	switch op {
	case ostyir.BinAdd:
		return token.PLUS
	case ostyir.BinSub:
		return token.MINUS
	case ostyir.BinMul:
		return token.STAR
	case ostyir.BinDiv:
		return token.SLASH
	case ostyir.BinMod:
		return token.PERCENT
	case ostyir.BinEq:
		return token.EQ
	case ostyir.BinNeq:
		return token.NEQ
	case ostyir.BinLt:
		return token.LT
	case ostyir.BinLeq:
		return token.LEQ
	case ostyir.BinGt:
		return token.GT
	case ostyir.BinGeq:
		return token.GEQ
	case ostyir.BinAnd:
		return token.AND
	case ostyir.BinOr:
		return token.OR
	case ostyir.BinBitAnd:
		return token.BITAND
	case ostyir.BinBitOr:
		return token.BITOR
	case ostyir.BinBitXor:
		return token.BITXOR
	case ostyir.BinShl:
		return token.SHL
	case ostyir.BinShr:
		return token.SHR
	default:
		return token.ILLEGAL
	}
}

func legacyIntrinsicName(kind ostyir.IntrinsicKind) string {
	switch kind {
	case ostyir.IntrinsicPrint:
		return "print"
	case ostyir.IntrinsicPrintln:
		return "println"
	case ostyir.IntrinsicEprint:
		return "eprint"
	case ostyir.IntrinsicEprintln:
		return "eprintln"
	default:
		return ""
	}
}

func legacyTypeExpr(name string, start, end token.Pos) ast.Expr {
	parts := splitQualifiedName(name)
	if len(parts) == 0 {
		return &ast.Ident{PosV: start, EndV: end, Name: name}
	}
	expr := ast.Expr(&ast.Ident{PosV: start, EndV: start, Name: parts[0]})
	for _, part := range parts[1:] {
		expr = &ast.FieldExpr{PosV: start, EndV: end, X: expr, Name: part}
	}
	return expr
}

func splitQualifiedName(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

func legacySpan(span ostyir.Span) (token.Pos, token.Pos) {
	return legacyPos(span.Start), legacyPos(span.End)
}

func legacyPos(pos ostyir.Pos) token.Pos {
	return token.Pos{
		Offset: pos.Offset,
		Line:   pos.Line,
		Column: pos.Column,
	}
}
