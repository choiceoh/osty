package llvmgen

import (
	"strconv"
	"strings"

	ostyir "github.com/osty/osty/internal/ir"
)

type nativeStructFieldInfo struct {
	name     string
	llvmType string
	index    int
}

type nativeStructInfo struct {
	def    *llvmNativeStruct
	byName map[string]nativeStructFieldInfo
}

type nativeProjectionCtx struct {
	structsByName map[string]*nativeStructInfo
}

// TryGenerateNativeOwnedModule emits textual LLVM IR only through the
// native-owned primitive/control-flow slice mirrored from
// toolchain/llvmgen.osty.
//
// ok=false means "shape not covered yet" and callers should choose a
// fallback path themselves. Unlike GenerateModule, this helper never
// falls back to the transitional IR -> AST bridge.
func TryGenerateNativeOwnedModule(mod *ostyir.Module, opts Options) ([]byte, bool, error) {
	if err := prepareModuleGeneration(mod); err != nil {
		return nil, false, err
	}
	out, ok, err := tryNativeOwnedModule(mod, opts)
	if err != nil || !ok {
		return out, ok, err
	}
	return finalizeLegacyFFISurface(out, mod), true, nil
}

// tryNativeOwnedModule projects the IR module into the native-owned
// primitive/control-flow slice mirrored from toolchain/llvmgen.osty.
// ok=false means "shape not covered yet" and callers should fall back to the
// legacy IR -> AST bridge.
func tryNativeOwnedModule(mod *ostyir.Module, opts Options) ([]byte, bool, error) {
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		return nil, false, nil
	}
	return []byte(llvmNativeEmitModule(nativeMod)), true, nil
}

func nativeModuleFromIR(mod *ostyir.Module, opts Options) (*llvmNativeModule, bool) {
	if mod == nil {
		return nil, false
	}
	ctx := &nativeProjectionCtx{structsByName: map[string]*nativeStructInfo{}}
	out := &llvmNativeModule{
		sourcePath: firstNonEmpty(opts.SourcePath, "<unknown>"),
		target:     opts.Target,
		structs:    make([]*llvmNativeStruct, 0, len(mod.Decls)),
		functions:  make([]*llvmNativeFunction, 0, len(mod.Decls)+1),
	}
	hasMain := false
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case nil:
			continue
		case *ostyir.UseDecl:
			if d.IsFFI() {
				return nil, false
			}
		case *ostyir.StructDecl:
			info, ok := nativeRegisterStructDecl(d)
			if !ok {
				return nil, false
			}
			if _, exists := ctx.structsByName[d.Name]; exists {
				return nil, false
			}
			ctx.structsByName[d.Name] = info
			out.structs = append(out.structs, info.def)
		}
	}
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case nil:
			continue
		case *ostyir.UseDecl:
			continue
		case *ostyir.StructDecl:
			if !nativePopulateStructDecl(ctx, d) {
				return nil, false
			}
		case *ostyir.FnDecl:
			fn, ok := nativeFunctionFromIR(ctx, d)
			if !ok {
				return nil, false
			}
			if fn.name == "main" {
				hasMain = true
			}
			out.functions = append(out.functions, fn)
		default:
			return nil, false
		}
	}
	if len(mod.Script) != 0 {
		if hasMain {
			return nil, false
		}
		mainBody, ok := nativeBlockFromStmts(ctx, mod.Script, "i32")
		if !ok {
			return nil, false
		}
		out.functions = append(out.functions, &llvmNativeFunction{
			name:       "main",
			returnType: "i32",
			body:       mainBody,
		})
	}
	return out, true
}

func nativeRegisterStructDecl(decl *ostyir.StructDecl) (*nativeStructInfo, bool) {
	if decl == nil || decl.Name == "" || len(decl.Generics) != 0 || len(decl.Methods) != 0 {
		return nil, false
	}
	return &nativeStructInfo{
		def: &llvmNativeStruct{
			name:     decl.Name,
			llvmType: llvmStructTypeName(decl.Name),
			fields:   make([]*llvmNativeStructField, 0, len(decl.Fields)),
		},
		byName: map[string]nativeStructFieldInfo{},
	}, true
}

func nativePopulateStructDecl(ctx *nativeProjectionCtx, decl *ostyir.StructDecl) bool {
	if ctx == nil || decl == nil {
		return false
	}
	info := ctx.structsByName[decl.Name]
	if info == nil || len(info.def.fields) != 0 {
		return info != nil
	}
	for i, field := range decl.Fields {
		if field == nil || field.Name == "" || field.Default != nil {
			return false
		}
		if _, exists := info.byName[field.Name]; exists {
			return false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, field.Type)
		if !ok || llvmType == "void" || llvmType == info.def.llvmType {
			return false
		}
		info.def.fields = append(info.def.fields, &llvmNativeStructField{
			name:     field.Name,
			llvmType: llvmType,
		})
		info.byName[field.Name] = nativeStructFieldInfo{
			name:     field.Name,
			llvmType: llvmType,
			index:    i,
		}
	}
	return true
}

func nativeFunctionFromIR(ctx *nativeProjectionCtx, fn *ostyir.FnDecl) (*llvmNativeFunction, bool) {
	if fn == nil || len(fn.Generics) != 0 || fn.IsIntrinsic || fn.Body == nil {
		return nil, false
	}
	retType, ok := nativeFunctionReturnType(ctx, fn)
	if !ok {
		return nil, false
	}
	out := &llvmNativeFunction{
		name:       fn.Name,
		returnType: retType,
		params:     make([]*llvmNativeParam, 0, len(fn.Params)),
	}
	for _, param := range fn.Params {
		if param == nil || param.IsDestructured() || param.Default != nil {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, param.Type)
		if !ok || llvmType == "void" {
			return nil, false
		}
		out.params = append(out.params, &llvmNativeParam{
			name:     param.Name,
			llvmType: llvmType,
		})
	}
	body, ok := nativeBlockFromIR(ctx, fn.Body, retType)
	if !ok {
		return nil, false
	}
	out.body = body
	return out, true
}

func nativeFunctionReturnType(ctx *nativeProjectionCtx, fn *ostyir.FnDecl) (string, bool) {
	if fn == nil {
		return "", false
	}
	if fn.Name == "main" && len(fn.Params) == 0 && nativeIsUnitType(fn.Return) {
		return "i32", true
	}
	return nativeLLVMTypeFromIR(ctx, fn.Return)
}

func nativeBlockFromIR(ctx *nativeProjectionCtx, block *ostyir.Block, fnReturnType string) (*llvmNativeBlock, bool) {
	if block == nil {
		return &llvmNativeBlock{}, true
	}
	out := &llvmNativeBlock{
		stmts: make([]*llvmNativeStmt, 0, len(block.Stmts)),
	}
	for _, stmt := range block.Stmts {
		nativeStmt, ok := nativeStmtFromIR(ctx, stmt, fnReturnType)
		if !ok {
			return nil, false
		}
		if nativeStmt != nil {
			out.stmts = append(out.stmts, nativeStmt)
		}
	}
	if block.Result != nil {
		result, ok := nativeExprFromIR(ctx, block.Result)
		if !ok {
			return nil, false
		}
		out.hasResult = true
		out.result = result
	}
	return out, true
}

func nativeBlockFromStmts(ctx *nativeProjectionCtx, stmts []ostyir.Stmt, fnReturnType string) (*llvmNativeBlock, bool) {
	out := &llvmNativeBlock{
		stmts: make([]*llvmNativeStmt, 0, len(stmts)),
	}
	for _, stmt := range stmts {
		nativeStmt, ok := nativeStmtFromIR(ctx, stmt, fnReturnType)
		if !ok {
			return nil, false
		}
		if nativeStmt != nil {
			out.stmts = append(out.stmts, nativeStmt)
		}
	}
	return out, true
}

func nativeStmtFromIR(ctx *nativeProjectionCtx, stmt ostyir.Stmt, fnReturnType string) (*llvmNativeStmt, bool) {
	switch s := stmt.(type) {
	case nil:
		return nil, true
	case *ostyir.LetStmt:
		if s.Pattern != nil || s.Value == nil {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, s.Value)
		if !ok {
			return nil, false
		}
		kind := llvmNativeStmtLet
		if s.Mut {
			kind = llvmNativeStmtMutLet
		}
		return &llvmNativeStmt{
			kind:       kind,
			name:       s.Name,
			childExprs: []*llvmNativeExpr{value},
		}, true
	case *ostyir.ExprStmt:
		expr, ok := nativeExprFromIR(ctx, s.X)
		if !ok {
			return nil, false
		}
		return &llvmNativeStmt{
			kind:       llvmNativeStmtExpr,
			childExprs: []*llvmNativeExpr{expr},
		}, true
	case *ostyir.AssignStmt:
		if len(s.Targets) != 1 {
			return nil, false
		}
		target, ok := s.Targets[0].(*ostyir.Ident)
		if !ok {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, s.Value)
		if !ok {
			return nil, false
		}
		return &llvmNativeStmt{
			kind:       llvmNativeStmtAssign,
			name:       target.Name,
			op:         nativeAssignOpString(s.Op),
			childExprs: []*llvmNativeExpr{value},
		}, true
	case *ostyir.ReturnStmt:
		out := &llvmNativeStmt{
			kind:     llvmNativeStmtReturn,
			llvmType: fnReturnType,
		}
		if s.Value != nil {
			value, ok := nativeExprFromIR(ctx, s.Value)
			if !ok {
				return nil, false
			}
			out.childExprs = []*llvmNativeExpr{value}
		}
		return out, true
	case *ostyir.IfStmt:
		cond, ok := nativeExprFromIR(ctx, s.Cond)
		if !ok {
			return nil, false
		}
		thenBlock, ok := nativeBlockFromIR(ctx, s.Then, fnReturnType)
		if !ok {
			return nil, false
		}
		out := &llvmNativeStmt{
			kind:        llvmNativeStmtIf,
			childExprs:  []*llvmNativeExpr{cond},
			childBlocks: []*llvmNativeBlock{thenBlock},
		}
		if s.Else != nil {
			elseBlock, ok := nativeBlockFromIR(ctx, s.Else, fnReturnType)
			if !ok {
				return nil, false
			}
			out.childBlocks = append(out.childBlocks, elseBlock)
		}
		return out, true
	case *ostyir.ForStmt:
		switch s.Kind {
		case ostyir.ForWhile:
			cond, ok := nativeExprFromIR(ctx, s.Cond)
			if !ok {
				return nil, false
			}
			body, ok := nativeBlockFromIR(ctx, s.Body, fnReturnType)
			if !ok {
				return nil, false
			}
			return &llvmNativeStmt{
				kind:        llvmNativeStmtWhile,
				childExprs:  []*llvmNativeExpr{cond},
				childBlocks: []*llvmNativeBlock{body},
			}, true
		case ostyir.ForRange:
			if s.IsDestructured() || s.Var == "" {
				return nil, false
			}
			start, ok := nativeExprFromIR(ctx, s.Start)
			if !ok {
				return nil, false
			}
			end, ok := nativeExprFromIR(ctx, s.End)
			if !ok {
				return nil, false
			}
			body, ok := nativeBlockFromIR(ctx, s.Body, fnReturnType)
			if !ok {
				return nil, false
			}
			return &llvmNativeStmt{
				kind:        llvmNativeStmtRange,
				name:        s.Var,
				inclusive:   s.Inclusive,
				childExprs:  []*llvmNativeExpr{start, end},
				childBlocks: []*llvmNativeBlock{body},
			}, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func nativeExprFromIR(ctx *nativeProjectionCtx, expr ostyir.Expr) (*llvmNativeExpr, bool) {
	switch e := expr.(type) {
	case nil:
		return nil, false
	case *ostyir.IntLit:
		text, ok := normalizeNativeIntText(e.Text)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: llvmType, text: text}, true
	case *ostyir.FloatLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:     llvmNativeExprFloat,
			llvmType: llvmType,
			text:     strings.ReplaceAll(e.Text, "_", ""),
		}, true
	case *ostyir.BoolLit:
		return &llvmNativeExpr{kind: llvmNativeExprBool, llvmType: "i1", boolValue: e.Value}, true
	case *ostyir.StringLit:
		text, ok := nativePlainStringText(e)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprString, llvmType: "ptr", text: text}, true
	case *ostyir.Ident:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprIdent, llvmType: llvmType, name: e.Name}, true
	case *ostyir.StructLit:
		info, ok := nativeStructInfoFromType(ctx, e.Type())
		if !ok || e.Spread != nil {
			return nil, false
		}
		fieldsByName := make(map[string]ostyir.StructLitField, len(e.Fields))
		for _, field := range e.Fields {
			if field.Name == "" {
				return nil, false
			}
			if _, exists := fieldsByName[field.Name]; exists {
				return nil, false
			}
			fieldsByName[field.Name] = field
		}
		out := &llvmNativeExpr{
			kind:       llvmNativeExprStructLit,
			llvmType:   info.def.llvmType,
			childExprs: make([]*llvmNativeExpr, 0, len(info.def.fields)),
		}
		for _, field := range info.def.fields {
			entry, ok := fieldsByName[field.name]
			if !ok {
				return nil, false
			}
			if entry.Value == nil {
				out.childExprs = append(out.childExprs, &llvmNativeExpr{
					kind:     llvmNativeExprIdent,
					llvmType: field.llvmType,
					name:     field.name,
				})
				continue
			}
			value, ok := nativeExprFromIR(ctx, entry.Value)
			if !ok || value.llvmType != field.llvmType {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.FieldExpr:
		if e.Optional {
			return nil, false
		}
		info, ok := nativeStructInfoFromType(ctx, e.X.Type())
		if !ok {
			return nil, false
		}
		field, ok := info.byName[e.Name]
		if !ok {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprField,
			llvmType:   field.llvmType,
			name:       field.name,
			fieldIndex: field.index,
			childExprs: []*llvmNativeExpr{base},
		}, true
	case *ostyir.UnaryExpr:
		inner, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprUnary,
			llvmType:   llvmType,
			op:         nativeUnaryOpString(e.Op),
			childExprs: []*llvmNativeExpr{inner},
		}, true
	case *ostyir.BinaryExpr:
		left, ok := nativeExprFromIR(ctx, e.Left)
		if !ok {
			return nil, false
		}
		right, ok := nativeExprFromIR(ctx, e.Right)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprBinary,
			llvmType:   llvmType,
			op:         nativeBinaryOpString(e.Op),
			childExprs: []*llvmNativeExpr{left, right},
		}, true
	case *ostyir.CallExpr:
		callee, ok := e.Callee.(*ostyir.Ident)
		if !ok || len(e.TypeArgs) != 0 {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:       llvmNativeExprCall,
			llvmType:   llvmType,
			name:       callee.Name,
			childExprs: make([]*llvmNativeExpr, 0, len(e.Args)),
		}
		for _, arg := range e.Args {
			if arg.IsKeyword() {
				return nil, false
			}
			value, ok := nativeExprFromIR(ctx, arg.Value)
			if !ok {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.IntrinsicCall:
		if e.Kind != ostyir.IntrinsicPrintln || len(e.Args) != 1 || e.Args[0].IsKeyword() {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, e.Args[0].Value)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprPrintln,
			llvmType:   "void",
			childExprs: []*llvmNativeExpr{value},
		}, true
	case *ostyir.IfExpr:
		cond, ok := nativeExprFromIR(ctx, e.Cond)
		if !ok {
			return nil, false
		}
		thenBlock, ok := nativeBlockFromIR(ctx, e.Then, "")
		if !ok {
			return nil, false
		}
		elseBlock, ok := nativeBlockFromIR(ctx, e.Else, "")
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:        llvmNativeExprIf,
			llvmType:    llvmType,
			childExprs:  []*llvmNativeExpr{cond},
			childBlocks: []*llvmNativeBlock{thenBlock, elseBlock},
		}, true
	default:
		return nil, false
	}
}

func nativeStructInfoFromType(ctx *nativeProjectionCtx, t ostyir.Type) (*nativeStructInfo, bool) {
	if ctx == nil {
		return nil, false
	}
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || named.Builtin || named.Package != "" || len(named.Args) != 0 {
		return nil, false
	}
	info := ctx.structsByName[named.Name]
	return info, info != nil
}

func nativeLLVMTypeFromIR(ctx *nativeProjectionCtx, t ostyir.Type) (string, bool) {
	switch tt := t.(type) {
	case nil:
		return "void", true
	case *ostyir.PrimType:
		switch tt.Kind {
		case ostyir.PrimUnit:
			return "void", true
		default:
			name := legacyPrimTypeName(tt.Kind)
			if name == "" {
				return "", false
			}
			llvmType := llvmBuiltinType(name)
			return llvmType, llvmType != ""
		}
	case *ostyir.NamedType:
		info, ok := nativeStructInfoFromType(ctx, tt)
		if !ok {
			return "", false
		}
		return info.def.llvmType, true
	default:
		return "", false
	}
}

func nativeIsUnitType(t ostyir.Type) bool {
	if t == nil {
		return true
	}
	prim, ok := t.(*ostyir.PrimType)
	return ok && prim.Kind == ostyir.PrimUnit
}

func nativePlainStringText(lit *ostyir.StringLit) (string, bool) {
	if lit == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range lit.Parts {
		if !part.IsLit {
			return "", false
		}
		b.WriteString(part.Lit)
	}
	return b.String(), true
}

func normalizeNativeIntText(text string) (string, bool) {
	raw := strings.ReplaceAll(text, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X"):
		base, raw = 16, raw[2:]
	case strings.HasPrefix(raw, "0o") || strings.HasPrefix(raw, "0O"):
		base, raw = 8, raw[2:]
	case strings.HasPrefix(raw, "0b") || strings.HasPrefix(raw, "0B"):
		base, raw = 2, raw[2:]
	}
	if raw == "" {
		return "", false
	}
	value, err := strconv.ParseInt(raw, base, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(value, 10), true
}

func nativeUnaryOpString(op ostyir.UnOp) string {
	switch op {
	case ostyir.UnNeg:
		return "-"
	case ostyir.UnPlus:
		return "+"
	case ostyir.UnNot:
		return "!"
	default:
		return ""
	}
}

func nativeBinaryOpString(op ostyir.BinOp) string {
	switch op {
	case ostyir.BinAdd:
		return "+"
	case ostyir.BinSub:
		return "-"
	case ostyir.BinMul:
		return "*"
	case ostyir.BinDiv:
		return "/"
	case ostyir.BinMod:
		return "%"
	case ostyir.BinEq:
		return "=="
	case ostyir.BinNeq:
		return "!="
	case ostyir.BinLt:
		return "<"
	case ostyir.BinLeq:
		return "<="
	case ostyir.BinGt:
		return ">"
	case ostyir.BinGeq:
		return ">="
	case ostyir.BinAnd:
		return "&&"
	case ostyir.BinOr:
		return "||"
	case ostyir.BinBitAnd:
		return "&"
	case ostyir.BinBitOr:
		return "|"
	case ostyir.BinBitXor:
		return "^"
	case ostyir.BinShl:
		return "<<"
	case ostyir.BinShr:
		return ">>"
	default:
		return ""
	}
}

func nativeAssignOpString(op ostyir.AssignOp) string {
	switch op {
	case ostyir.AssignEq:
		return "="
	case ostyir.AssignAdd:
		return "+"
	case ostyir.AssignSub:
		return "-"
	case ostyir.AssignMul:
		return "*"
	case ostyir.AssignDiv:
		return "/"
	case ostyir.AssignMod:
		return "%"
	case ostyir.AssignAnd:
		return "&"
	case ostyir.AssignOr:
		return "|"
	case ostyir.AssignXor:
		return "^"
	case ostyir.AssignShl:
		return "<<"
	case ostyir.AssignShr:
		return ">>"
	default:
		return ""
	}
}
