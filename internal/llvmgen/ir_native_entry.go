package llvmgen

import (
	"fmt"
	"strconv"
	"strings"

	ostyir "github.com/osty/osty/internal/ir"
)

type nativeStructFieldInfo struct {
	name     string
	llvmType string
	index    int
	irType   ostyir.Type
}

type nativeStructInfo struct {
	def    *llvmNativeStruct
	byName map[string]nativeStructFieldInfo
}

type nativeTupleInfo struct {
	def           *llvmNativeStruct
	elemLLVMTypes []string
}

type nativeProjectionCtx struct {
	structsByName         map[string]*nativeStructInfo
	tuplesByLLVMType      map[string]*nativeTupleInfo
	tupleOrder            []string
	methodsByOwner        map[string]map[string]nativeMethodInfo
	globalConsts          map[string]nativeConstValue
	mutableGlobals        map[string]bool
	scopes                []map[string]nativeExprInfo
	needsListRT           bool
	needsMapRT            bool
	needsSetRT            bool
	needsStringRT         bool
	stringGlobals         []*LlvmStringGlobal
	nextStringID          int
	currentReturnLLVMType string
}

type nativeExprInfoKind int

const (
	nativeExprInfoInvalid nativeExprInfoKind = iota
	nativeExprInfoScalar
	nativeExprInfoString
	nativeExprInfoList
	nativeExprInfoMap
	nativeExprInfoSet
	nativeExprInfoStruct
	nativeExprInfoOptional
)

type nativeExprInfo struct {
	kind            nativeExprInfoKind
	llvmType        string
	sourceType      ostyir.Type
	structName      string
	listElemType    string
	listElemString  bool
	listElemBytes   bool
	mapKeyType      string
	mapValueType    string
	mapKeyString    bool
	setElemType     string
	setElemString   bool
	optionInnerType string
}

func (ctx *nativeProjectionCtx) pushScope() {
	if ctx == nil {
		return
	}
	ctx.scopes = append(ctx.scopes, map[string]nativeExprInfo{})
}

func (ctx *nativeProjectionCtx) popScope() {
	if ctx == nil || len(ctx.scopes) == 0 {
		return
	}
	ctx.scopes = ctx.scopes[:len(ctx.scopes)-1]
}

func (ctx *nativeProjectionCtx) bindScopeName(name string, info nativeExprInfo) {
	if ctx == nil || name == "" || info.llvmType == "" {
		return
	}
	if len(ctx.scopes) == 0 {
		ctx.pushScope()
	}
	ctx.scopes[len(ctx.scopes)-1][name] = info
}

func (ctx *nativeProjectionCtx) lookupScopeName(name string) (nativeExprInfo, bool) {
	if ctx == nil || name == "" {
		return nativeExprInfo{}, false
	}
	for i := len(ctx.scopes) - 1; i >= 0; i-- {
		if info, ok := ctx.scopes[i][name]; ok {
			return info, true
		}
	}
	return nativeExprInfo{}, false
}

type nativeMethodInfo struct {
	irName      string
	receiverMut bool
}

type nativeConstValue struct {
	llvmType string
	init     string
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
	ctx := &nativeProjectionCtx{
		structsByName:    map[string]*nativeStructInfo{},
		tuplesByLLVMType: map[string]*nativeTupleInfo{},
		methodsByOwner:   map[string]map[string]nativeMethodInfo{},
		globalConsts:     map[string]nativeConstValue{},
		mutableGlobals:   map[string]bool{},
	}
	out := &llvmNativeModule{
		sourcePath:    firstNonEmpty(opts.SourcePath, "<unknown>"),
		target:        opts.Target,
		globals:       make([]*llvmNativeGlobal, 0, len(mod.Decls)),
		structs:       make([]*llvmNativeStruct, 0, len(mod.Decls)),
		stringGlobals: make([]*LlvmStringGlobal, 0),
		functions:     make([]*llvmNativeFunction, 0, len(mod.Decls)+1),
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
			if !nativeRegisterStructMethodHeaders(ctx, d) {
				return nil, false
			}
		case *ostyir.LetDecl:
			ctx.mutableGlobals[d.Name] = d.Mut
		}
	}
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case nil:
			continue
		case *ostyir.UseDecl:
			continue
		case *ostyir.LetDecl:
			global, ok := nativeGlobalLetFromIR(ctx, d)
			if !ok {
				return nil, false
			}
			out.globals = append(out.globals, global)
		case *ostyir.StructDecl:
			if !nativePopulateStructDecl(ctx, d) {
				return nil, false
			}
			for _, method := range d.Methods {
				fn, ok := nativeMethodFunctionFromIR(ctx, d, method)
				if !ok {
					return nil, false
				}
				out.functions = append(out.functions, fn)
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
	out.stringGlobals = append(out.stringGlobals, ctx.stringGlobals...)
	for _, llvmType := range ctx.tupleOrder {
		if info := ctx.tuplesByLLVMType[llvmType]; info != nil {
			out.structs = append(out.structs, info.def)
		}
	}
	if len(mod.Script) != 0 {
		if hasMain {
			return nil, false
		}
		prevReturnType := ctx.currentReturnLLVMType
		ctx.currentReturnLLVMType = "i32"
		ctx.pushScope()
		mainBody, ok := nativeBlockFromStmts(ctx, mod.Script, "i32")
		ctx.popScope()
		ctx.currentReturnLLVMType = prevReturnType
		if !ok {
			return nil, false
		}
		out.functions = append(out.functions, &llvmNativeFunction{
			name:       "main",
			returnType: "i32",
			body:       mainBody,
		})
	}
	out.needsListRuntime = ctx.needsListRT
	out.needsMapRuntime = ctx.needsMapRT
	out.needsSetRuntime = ctx.needsSetRT
	out.needsStringRuntime = ctx.needsStringRT
	return out, true
}

func nativeRegisterStructDecl(decl *ostyir.StructDecl) (*nativeStructInfo, bool) {
	if decl == nil || decl.Name == "" || len(decl.Generics) != 0 {
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
			irType:   field.Type,
		}
	}
	return true
}

func nativeRegisterStructMethodHeaders(ctx *nativeProjectionCtx, decl *ostyir.StructDecl) bool {
	if ctx == nil || decl == nil {
		return false
	}
	methods := ctx.methodsByOwner[decl.Name]
	if methods == nil {
		methods = map[string]nativeMethodInfo{}
		ctx.methodsByOwner[decl.Name] = methods
	}
	for _, method := range decl.Methods {
		if method == nil || len(method.Generics) != 0 || method.IsIntrinsic || method.Body == nil {
			return false
		}
		if _, exists := methods[method.Name]; exists {
			return false
		}
		methods[method.Name] = nativeMethodInfo{
			irName:      llvmMethodIRName(decl.Name, method.Name),
			receiverMut: method.ReceiverMut,
		}
	}
	return true
}

func nativeFunctionFromIR(ctx *nativeProjectionCtx, fn *ostyir.FnDecl) (*llvmNativeFunction, bool) {
	if fn == nil || len(fn.Generics) != 0 || fn.IsIntrinsic || fn.Body == nil {
		return nil, false
	}
	ctx.pushScope()
	defer ctx.popScope()
	retType, ok := nativeFunctionReturnType(ctx, fn)
	if !ok {
		return nil, false
	}
	prevReturnType := ctx.currentReturnLLVMType
	ctx.currentReturnLLVMType = retType
	defer func() { ctx.currentReturnLLVMType = prevReturnType }()
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
		ctx.bindScopeName(param.Name, nativeExprInfoFromLLVMType(llvmType))
		if info, ok := nativeExprInfoFromType(ctx, param.Type); ok {
			ctx.bindScopeName(param.Name, info)
		}
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

func nativeMethodFunctionFromIR(
	ctx *nativeProjectionCtx,
	owner *ostyir.StructDecl,
	fn *ostyir.FnDecl,
) (*llvmNativeFunction, bool) {
	if ctx == nil || owner == nil || fn == nil || len(fn.Generics) != 0 || fn.IsIntrinsic || fn.Body == nil {
		return nil, false
	}
	ownerInfo := ctx.structsByName[owner.Name]
	if ownerInfo == nil {
		return nil, false
	}
	ctx.pushScope()
	defer ctx.popScope()
	retType, ok := nativeFunctionReturnType(ctx, fn)
	if !ok {
		return nil, false
	}
	prevReturnType := ctx.currentReturnLLVMType
	ctx.currentReturnLLVMType = retType
	defer func() { ctx.currentReturnLLVMType = prevReturnType }()
	out := &llvmNativeFunction{
		name:       llvmMethodIRName(owner.Name, fn.Name),
		returnType: retType,
		params: []*llvmNativeParam{{
			name:     "self",
			llvmType: ownerInfo.def.llvmType,
			irType:   llvmMethodReceiverIRType(ownerInfo.def.llvmType, fn.ReceiverMut),
			byRef:    fn.ReceiverMut,
		}},
	}
	ctx.bindScopeName("self", nativeExprInfo{
		kind:       nativeExprInfoStruct,
		llvmType:   ownerInfo.def.llvmType,
		structName: owner.Name,
	})
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
		ctx.bindScopeName(param.Name, nativeExprInfoFromLLVMType(llvmType))
		if info, ok := nativeExprInfoFromType(ctx, param.Type); ok {
			ctx.bindScopeName(param.Name, info)
		}
	}
	body, ok := nativeBlockFromIR(ctx, fn.Body, retType)
	if !ok {
		return nil, false
	}
	out.body = body
	return out, true
}

func nativeBlockFromIR(ctx *nativeProjectionCtx, block *ostyir.Block, fnReturnType string) (*llvmNativeBlock, bool) {
	if block == nil {
		return &llvmNativeBlock{}, true
	}
	ctx.pushScope()
	defer ctx.popScope()
	stmts := block.Stmts
	var tailResult ostyir.Expr
	if block.Result == nil && fnReturnType != "" && fnReturnType != "void" && len(stmts) != 0 {
		if exprStmt, ok := stmts[len(stmts)-1].(*ostyir.ExprStmt); ok && exprStmt != nil && exprStmt.X != nil {
			tailResult = exprStmt.X
			stmts = stmts[:len(stmts)-1]
		}
	}
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
	resultExpr := block.Result
	if resultExpr == nil {
		resultExpr = tailResult
	}
	if resultExpr != nil {
		result, ok := nativeExprFromIR(ctx, resultExpr)
		if !ok {
			return nil, false
		}
		out.hasResult = true
		out.result = result
	}
	return out, true
}

func nativeBlockFromStmts(ctx *nativeProjectionCtx, stmts []ostyir.Stmt, fnReturnType string) (*llvmNativeBlock, bool) {
	ctx.pushScope()
	defer ctx.popScope()
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
		if info, ok := nativeExprTypeInfo(ctx, s.Value); ok {
			ctx.bindScopeName(s.Name, info)
		} else if info, ok := nativeExprInfoFromType(ctx, firstNonNilType(s.Type, s.Value.Type())); ok {
			ctx.bindScopeName(s.Name, info)
		} else {
			ctx.bindScopeName(s.Name, nativeExprInfoFromLLVMType(value.llvmType))
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
		value, ok := nativeExprFromIR(ctx, s.Value)
		if !ok {
			return nil, false
		}
		switch target := s.Targets[0].(type) {
		case *ostyir.Ident:
			if target.Kind == ostyir.IdentGlobal && !ctx.mutableGlobals[target.Name] {
				return nil, false
			}
			return &llvmNativeStmt{
				kind:       llvmNativeStmtAssign,
				name:       target.Name,
				op:         nativeAssignOpString(s.Op),
				childExprs: []*llvmNativeExpr{value},
			}, true
		case *ostyir.FieldExpr:
			return nativeFieldAssignStmtFromIR(ctx, target, value, s.Op)
		default:
			return nil, false
		}
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

func nativeFieldAssignStmtFromIR(
	ctx *nativeProjectionCtx,
	target *ostyir.FieldExpr,
	value *llvmNativeExpr,
	op ostyir.AssignOp,
) (*llvmNativeStmt, bool) {
	base, fieldPath, ok := nativeFieldAssignPathFromIR(ctx, target)
	if !ok || value == nil || len(fieldPath) == 0 {
		return nil, false
	}
	if value.llvmType != fieldPath[len(fieldPath)-1].llvmType {
		return nil, false
	}
	return &llvmNativeStmt{
		kind:       llvmNativeStmtFieldAssign,
		name:       base.Name,
		op:         nativeAssignOpString(op),
		childExprs: []*llvmNativeExpr{value},
		fieldPath:  fieldPath,
	}, true
}

func nativeFieldAssignPathFromIR(
	ctx *nativeProjectionCtx,
	target *ostyir.FieldExpr,
) (*ostyir.Ident, []*llvmNativeFieldPath, bool) {
	if ctx == nil || target == nil {
		return nil, nil, false
	}
	fields := []*ostyir.FieldExpr{}
	cur := target
	for {
		if cur == nil || cur.Optional {
			return nil, nil, false
		}
		fields = append([]*ostyir.FieldExpr{cur}, fields...)
		next, ok := cur.X.(*ostyir.FieldExpr)
		if !ok {
			break
		}
		cur = next
	}
	base, ok := cur.X.(*ostyir.Ident)
	if !ok || !nativeWritableBaseIdent(ctx, base) {
		return nil, nil, false
	}
	fieldPath := make([]*llvmNativeFieldPath, 0, len(fields))
	currentType := base.Type()
	for _, fieldExpr := range fields {
		info, ok := nativeStructInfoFromType(ctx, currentType)
		if !ok {
			return nil, nil, false
		}
		field, ok := info.byName[fieldExpr.Name]
		if !ok {
			return nil, nil, false
		}
		fieldPath = append(fieldPath, &llvmNativeFieldPath{
			llvmType:   field.llvmType,
			fieldIndex: field.index,
		})
		currentType = fieldExpr.Type()
	}
	return base, fieldPath, true
}

func nativeWritableBaseIdent(ctx *nativeProjectionCtx, ident *ostyir.Ident) bool {
	if ctx == nil || ident == nil {
		return false
	}
	switch ident.Kind {
	case ostyir.IdentLocal, ostyir.IdentParam:
		return true
	case ostyir.IdentGlobal:
		return ctx.mutableGlobals[ident.Name]
	default:
		return false
	}
}

func nativeGlobalLetFromIR(ctx *nativeProjectionCtx, decl *ostyir.LetDecl) (*llvmNativeGlobal, bool) {
	if ctx == nil || decl == nil || decl.Name == "" || decl.Value == nil {
		return nil, false
	}
	llvmType, ok := nativeLLVMTypeFromIR(ctx, firstNonNilType(decl.Type, decl.Value.Type()))
	if !ok || llvmType == "void" {
		return nil, false
	}
	value, ok := nativeConstFromIR(ctx, decl.Value)
	if !ok || value.llvmType != llvmType {
		return nil, false
	}
	global := &llvmNativeGlobal{
		name:     decl.Name,
		irName:   llvmGlobalIRName(decl.Name),
		llvmType: llvmType,
		mutable:  decl.Mut,
		init:     value.init,
	}
	ctx.globalConsts[decl.Name] = value
	ctx.mutableGlobals[decl.Name] = decl.Mut
	return global, true
}

func nativeConstFromIR(ctx *nativeProjectionCtx, expr ostyir.Expr) (nativeConstValue, bool) {
	switch e := expr.(type) {
	case nil:
		return nativeConstValue{}, false
	case *ostyir.IntLit:
		text, ok := normalizeNativeIntText(e.Text)
		if !ok {
			return nativeConstValue{}, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nativeConstValue{}, false
		}
		return nativeConstValue{llvmType: llvmType, init: text}, true
	case *ostyir.FloatLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nativeConstValue{}, false
		}
		return nativeConstValue{
			llvmType: llvmType,
			init:     llvmFloatConstLiteral(mustParseNativeFloat(e.Text)),
		}, true
	case *ostyir.BoolLit:
		if e.Value {
			return nativeConstValue{llvmType: "i1", init: "true"}, true
		}
		return nativeConstValue{llvmType: "i1", init: "false"}, true
	case *ostyir.StringLit:
		text, ok := nativePlainStringText(e)
		if !ok {
			return nativeConstValue{}, false
		}
		name := fmt.Sprintf("@.str%d", ctx.nextStringID)
		ctx.nextStringID++
		cstring := llvmCString(text)
		ctx.stringGlobals = append(ctx.stringGlobals, &LlvmStringGlobal{
			name:    name,
			encoded: cstring.encoded,
			byteLen: cstring.byteLen,
		})
		return nativeConstValue{llvmType: "ptr", init: name}, true
	case *ostyir.Ident:
		value, ok := ctx.globalConsts[e.Name]
		return value, ok
	case *ostyir.UnaryExpr:
		value, ok := nativeConstFromIR(ctx, e.X)
		if !ok {
			return nativeConstValue{}, false
		}
		switch nativeUnaryOpString(e.Op) {
		case "+":
			return value, value.llvmType == "i64" || value.llvmType == "double"
		case "-":
			switch value.llvmType {
			case "i64":
				n, err := strconv.ParseInt(value.init, 10, 64)
				if err != nil {
					return nativeConstValue{}, false
				}
				return nativeConstValue{llvmType: "i64", init: strconv.FormatInt(-n, 10)}, true
			case "double":
				f, err := strconv.ParseFloat(value.init, 64)
				if err != nil {
					return nativeConstValue{}, false
				}
				return nativeConstValue{llvmType: "double", init: llvmFloatConstLiteral(-f)}, true
			default:
				return nativeConstValue{}, false
			}
		case "!":
			if value.llvmType == "i1" && (value.init == "true" || value.init == "false") {
				return nativeConstValue{llvmType: "i1", init: strconv.FormatBool(value.init != "true")}, true
			}
			return nativeConstValue{}, false
		default:
			return nativeConstValue{}, false
		}
	case *ostyir.BinaryExpr:
		left, ok := nativeConstFromIR(ctx, e.Left)
		if !ok {
			return nativeConstValue{}, false
		}
		right, ok := nativeConstFromIR(ctx, e.Right)
		if !ok {
			return nativeConstValue{}, false
		}
		return nativeBinaryConst(left, right, e.Op)
	case *ostyir.StructLit:
		info, ok := nativeStructInfoFromType(ctx, e.Type())
		if !ok || e.Spread != nil {
			return nativeConstValue{}, false
		}
		fieldsByName := make(map[string]ostyir.StructLitField, len(e.Fields))
		for _, field := range e.Fields {
			if field.Name == "" {
				return nativeConstValue{}, false
			}
			if _, exists := fieldsByName[field.Name]; exists {
				return nativeConstValue{}, false
			}
			fieldsByName[field.Name] = field
		}
		parts := make([]string, 0, len(info.def.fields))
		for _, field := range info.def.fields {
			entry, ok := fieldsByName[field.name]
			if !ok {
				return nativeConstValue{}, false
			}
			var value nativeConstValue
			if entry.Value == nil {
				value, ok = ctx.globalConsts[field.name]
			} else {
				value, ok = nativeConstFromIR(ctx, entry.Value)
			}
			if !ok || value.llvmType != field.llvmType {
				return nativeConstValue{}, false
			}
			parts = append(parts, value.llvmType+" "+value.init)
		}
		return nativeConstValue{
			llvmType: info.def.llvmType,
			init:     "{ " + strings.Join(parts, ", ") + " }",
		}, true
	default:
		return nativeConstValue{}, false
	}
}

func nativeBinaryConst(left, right nativeConstValue, op ostyir.BinOp) (nativeConstValue, bool) {
	switch op {
	case ostyir.BinAdd, ostyir.BinSub, ostyir.BinMul, ostyir.BinDiv, ostyir.BinMod:
		if left.llvmType == "double" && right.llvmType == "double" {
			lf, err := strconv.ParseFloat(left.init, 64)
			if err != nil {
				return nativeConstValue{}, false
			}
			rf, err := strconv.ParseFloat(right.init, 64)
			if err != nil {
				return nativeConstValue{}, false
			}
			var out float64
			switch op {
			case ostyir.BinAdd:
				out = lf + rf
			case ostyir.BinSub:
				out = lf - rf
			case ostyir.BinMul:
				out = lf * rf
			case ostyir.BinDiv:
				out = lf / rf
			default:
				return nativeConstValue{}, false
			}
			return nativeConstValue{llvmType: "double", init: llvmFloatConstLiteral(out)}, true
		}
		if left.llvmType != "i64" || right.llvmType != "i64" {
			return nativeConstValue{}, false
		}
		li, err := strconv.ParseInt(left.init, 10, 64)
		if err != nil {
			return nativeConstValue{}, false
		}
		ri, err := strconv.ParseInt(right.init, 10, 64)
		if err != nil {
			return nativeConstValue{}, false
		}
		var out int64
		switch op {
		case ostyir.BinAdd:
			out = li + ri
		case ostyir.BinSub:
			out = li - ri
		case ostyir.BinMul:
			out = li * ri
		case ostyir.BinDiv:
			if ri == 0 {
				return nativeConstValue{}, false
			}
			out = li / ri
		case ostyir.BinMod:
			if ri == 0 {
				return nativeConstValue{}, false
			}
			out = li % ri
		}
		return nativeConstValue{llvmType: "i64", init: strconv.FormatInt(out, 10)}, true
	case ostyir.BinAnd, ostyir.BinOr:
		if left.llvmType != "i1" || right.llvmType != "i1" {
			return nativeConstValue{}, false
		}
		lb := left.init == "true"
		rb := right.init == "true"
		out := lb && rb
		if op == ostyir.BinOr {
			out = lb || rb
		}
		return nativeConstValue{llvmType: "i1", init: strconv.FormatBool(out)}, true
	default:
		return nativeConstValue{}, false
	}
}

func firstNonNilType(primary, fallback ostyir.Type) ostyir.Type {
	if primary != nil {
		return primary
	}
	return fallback
}

func mustParseNativeFloat(text string) float64 {
	value, err := strconv.ParseFloat(strings.ReplaceAll(text, "_", ""), 64)
	if err != nil {
		return 0
	}
	return value
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
	case *ostyir.CharLit:
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i32", text: strconv.FormatInt(int64(e.Value), 10)}, true
	case *ostyir.ByteLit:
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i8", text: strconv.FormatInt(int64(e.Value), 10)}, true
	case *ostyir.ListLit:
		listInfo, ok := nativeExprInfoFromType(ctx, e.Type())
		if !ok || listInfo.kind != nativeExprInfoList {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:         llvmNativeExprListLit,
			llvmType:     "ptr",
			elemLLVMType: listInfo.listElemType,
			childExprs:   make([]*llvmNativeExpr, 0, len(e.Elems)),
		}
		for _, elem := range e.Elems {
			value, ok := nativeExprFromIRWithHint(ctx, elem, listInfo.listElemType)
			if !ok || value.llvmType != listInfo.listElemType {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		ctx.needsListRT = true
		return out, true
	case *ostyir.MapLit:
		mapInfo, ok := nativeMapLiteralInfo(ctx, e)
		if !ok || mapInfo.kind != nativeExprInfoMap {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:           llvmNativeExprMapLit,
			llvmType:       "ptr",
			mapKeyLLVMType: mapInfo.mapKeyType,
			mapKeyIsString: mapInfo.mapKeyString,
			childExprs:     make([]*llvmNativeExpr, 0, len(e.Entries)*2),
		}
		for _, entry := range e.Entries {
			key, ok := nativeExprFromIRWithHint(ctx, entry.Key, mapInfo.mapKeyType)
			if !ok || key.llvmType != mapInfo.mapKeyType {
				return nil, false
			}
			value, ok := nativeExprFromIRWithHint(ctx, entry.Value, mapInfo.mapValueType)
			if !ok || value.llvmType != mapInfo.mapValueType {
				return nil, false
			}
			out.childExprs = append(out.childExprs, key, value)
		}
		ctx.needsMapRT = true
		return out, true
	case *ostyir.TupleLit:
		info, ok := nativeTupleInfoFromType(ctx, e.Type())
		if !ok {
			return nil, false
		}
		if len(e.Elems) != len(info.elemLLVMTypes) {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:       llvmNativeExprStructLit,
			llvmType:   info.def.llvmType,
			childExprs: make([]*llvmNativeExpr, 0, len(e.Elems)),
		}
		for i, elem := range e.Elems {
			value, ok := nativeExprFromIRWithHint(ctx, elem, info.elemLLVMTypes[i])
			if !ok || value.llvmType != info.elemLLVMTypes[i] {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.Ident:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			info, ok := ctx.lookupScopeName(e.Name)
			if !ok {
				return nil, false
			}
			llvmType = info.llvmType
		}
		return &llvmNativeExpr{kind: llvmNativeExprIdent, llvmType: llvmType, name: e.Name}, true
	case *ostyir.StructLit:
		info, ok := nativeStructInfoFromType(ctx, e.Type())
		if !ok && e.TypeName != "" {
			info = ctx.structsByName[e.TypeName]
			ok = info != nil
		}
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
			value, ok := nativeExprFromIRWithHint(ctx, entry.Value, field.llvmType)
			if !ok || value.llvmType != field.llvmType {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.FieldExpr:
		if e.Optional {
			baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
			if !ok || baseInfo.kind != nativeExprInfoOptional {
				return nil, false
			}
			baseSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(e.X, baseInfo))
			if !ok {
				return nil, false
			}
			info, ok := nativeStructInfoFromType(ctx, baseSource)
			if !ok {
				return nil, false
			}
			field, ok := info.byName[e.Name]
			if !ok || field.llvmType != "ptr" {
				return nil, false
			}
			base, ok := nativeExprFromIR(ctx, e.X)
			if !ok {
				return nil, false
			}
			return &llvmNativeExpr{
				kind:                llvmNativeExprOptionalField,
				llvmType:            "ptr",
				fieldIndex:          field.index,
				baseLLVMType:        info.def.llvmType,
				optionInnerLLVMType: field.llvmType,
				childExprs:          []*llvmNativeExpr{base},
			}, true
		}
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok || baseInfo.kind != nativeExprInfoStruct || baseInfo.structName == "" {
			return nil, false
		}
		info := ctx.structsByName[baseInfo.structName]
		if info == nil {
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
	case *ostyir.IndexExpr:
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		switch baseInfo.kind {
		case nativeExprInfoList:
			index, ok := nativeExprFromIRWithHint(ctx, e.Index, "i64")
			if !ok {
				return nil, false
			}
			llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
			if !ok {
				resultInfo, ok := nativeExprTypeInfo(ctx, e)
				if !ok || resultInfo.llvmType == "" {
					return nil, false
				}
				llvmType = resultInfo.llvmType
			}
			if llvmType == "" {
				return nil, false
			}
			ctx.needsListRT = true
			return &llvmNativeExpr{
				kind:         llvmNativeExprListIndex,
				llvmType:     llvmType,
				elemLLVMType: baseInfo.listElemType,
				childExprs:   []*llvmNativeExpr{base, index},
			}, true
		case nativeExprInfoMap:
			key, ok := nativeExprFromIRWithHint(ctx, e.Index, baseInfo.mapKeyType)
			if !ok {
				return nil, false
			}
			llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
			if !ok {
				resultInfo, ok := nativeExprTypeInfo(ctx, e)
				if !ok || resultInfo.llvmType == "" {
					return nil, false
				}
				llvmType = resultInfo.llvmType
			}
			if llvmType == "" {
				return nil, false
			}
			ctx.needsMapRT = true
			return &llvmNativeExpr{
				kind:           llvmNativeExprMapIndex,
				llvmType:       llvmType,
				mapKeyLLVMType: baseInfo.mapKeyType,
				mapKeyIsString: baseInfo.mapKeyString,
				childExprs:     []*llvmNativeExpr{base, key},
			}, true
		default:
			return nil, false
		}
	case *ostyir.TupleAccess:
		info, ok := nativeTupleInfoFromType(ctx, e.X.Type())
		if !ok || e.Index < 0 || e.Index >= len(info.elemLLVMTypes) {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprField,
			llvmType:   info.elemLLVMTypes[e.Index],
			fieldIndex: e.Index,
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
			llvmType, ok = nativeBinaryResultLLVMType(e.Op, left.llvmType, right.llvmType)
			if !ok {
				return nil, false
			}
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprBinary,
			llvmType:   llvmType,
			op:         nativeBinaryOpString(e.Op),
			childExprs: []*llvmNativeExpr{left, right},
		}, true
	case *ostyir.QuestionExpr:
		if ctx == nil || ctx.currentReturnLLVMType != "ptr" {
			return nil, false
		}
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok || baseInfo.kind != nativeExprInfoOptional {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			llvmType = firstNonEmpty(baseInfo.optionInnerType, "")
			ok = llvmType != ""
		}
		if !ok || llvmType == "" || (baseInfo.optionInnerType != "" && baseInfo.optionInnerType != llvmType) {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:                llvmNativeExprQuestion,
			llvmType:            llvmType,
			optionInnerLLVMType: firstNonEmpty(baseInfo.optionInnerType, llvmType),
			childExprs:          []*llvmNativeExpr{base},
		}, true
	case *ostyir.CoalesceExpr:
		leftInfo, ok := nativeExprTypeInfo(ctx, e.Left)
		if !ok || leftInfo.kind != nativeExprInfoOptional {
			return nil, false
		}
		left, ok := nativeExprFromIR(ctx, e.Left)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			llvmType = firstNonEmpty(leftInfo.optionInnerType, "")
			ok = llvmType != ""
		}
		if !ok || llvmType == "" || (leftInfo.optionInnerType != "" && leftInfo.optionInnerType != llvmType) {
			return nil, false
		}
		right, ok := nativeExprFromIRWithHint(ctx, e.Right, llvmType)
		if !ok || right.llvmType != llvmType {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:                llvmNativeExprCoalesce,
			llvmType:            llvmType,
			optionInnerLLVMType: firstNonEmpty(leftInfo.optionInnerType, llvmType),
			childExprs:          []*llvmNativeExpr{left, right},
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
	case *ostyir.MethodCall:
		if builtin, ok := nativeBuiltinMethodExprFromIR(ctx, e); ok {
			return builtin, true
		}
		if len(e.TypeArgs) != 0 {
			return nil, false
		}
		ownerName, ok := nativeMethodOwnerName(e.Receiver.Type())
		if !ok {
			return nil, false
		}
		methods := ctx.methodsByOwner[ownerName]
		info, ok := methods[e.Name]
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:          llvmNativeExprCall,
			llvmType:      llvmType,
			name:          info.irName,
			firstArgByRef: info.receiverMut,
			childExprs:    make([]*llvmNativeExpr, 0, len(e.Args)+1),
		}
		if info.receiverMut {
			switch receiver := e.Receiver.(type) {
			case *ostyir.Ident:
				if !nativeWritableBaseIdent(ctx, receiver) {
					return nil, false
				}
				receiverValue, ok := nativeExprFromIR(ctx, receiver)
				if !ok {
					return nil, false
				}
				out.childExprs = append(out.childExprs, receiverValue)
			case *ostyir.FieldExpr:
				base, receiverPath, ok := nativeFieldAssignPathFromIR(ctx, receiver)
				if !ok {
					return nil, false
				}
				receiverValue, ok := nativeExprFromIR(ctx, base)
				if !ok {
					return nil, false
				}
				out.receiverPath = receiverPath
				out.childExprs = append(out.childExprs, receiverValue)
			default:
				return nil, false
			}
		} else {
			receiver, ok := nativeExprFromIR(ctx, e.Receiver)
			if !ok {
				return nil, false
			}
			out.childExprs = append(out.childExprs, receiver)
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

func nativeBinaryResultLLVMType(op ostyir.BinOp, leftType string, rightType string) (string, bool) {
	switch op {
	case ostyir.BinEq, ostyir.BinNeq, ostyir.BinLt, ostyir.BinLeq, ostyir.BinGt, ostyir.BinGeq, ostyir.BinAnd, ostyir.BinOr:
		return "i1", leftType != "" && rightType != ""
	case ostyir.BinAdd, ostyir.BinSub, ostyir.BinMul, ostyir.BinDiv, ostyir.BinMod, ostyir.BinBitAnd, ostyir.BinBitOr, ostyir.BinBitXor, ostyir.BinShl, ostyir.BinShr:
		if leftType == "" || rightType == "" || leftType != rightType {
			return "", false
		}
		return leftType, true
	default:
		return "", false
	}
}

func nativeExprInfoFromLLVMType(llvmType string) nativeExprInfo {
	if llvmType == "" {
		return nativeExprInfo{}
	}
	return nativeExprInfo{kind: nativeExprInfoScalar, llvmType: llvmType}
}

func nativeExprInfoWithSource(info nativeExprInfo, sourceType ostyir.Type) nativeExprInfo {
	info.sourceType = sourceType
	return info
}

func nativeStringExprInfo() nativeExprInfo {
	return nativeExprInfo{kind: nativeExprInfoString, llvmType: "ptr"}
}

func nativeListExprInfo(elemType string, elemString bool, elemBytes bool) nativeExprInfo {
	return nativeExprInfo{
		kind:           nativeExprInfoList,
		llvmType:       "ptr",
		listElemType:   elemType,
		listElemString: elemString,
		listElemBytes:  elemBytes,
	}
}

func nativeMapExprInfo(keyType string, valueType string, keyString bool) nativeExprInfo {
	return nativeExprInfo{
		kind:         nativeExprInfoMap,
		llvmType:     "ptr",
		mapKeyType:   keyType,
		mapValueType: valueType,
		mapKeyString: keyString,
	}
}

func nativeSetExprInfo(elemType string, elemString bool) nativeExprInfo {
	return nativeExprInfo{
		kind:          nativeExprInfoSet,
		llvmType:      "ptr",
		setElemType:   elemType,
		setElemString: elemString,
	}
}

func nativeTypeResolved(t ostyir.Type) bool {
	switch t.(type) {
	case nil, *ostyir.ErrType, *ostyir.TypeVar:
		return false
	default:
		return true
	}
}

func nativeSourceTypeFromExprOrInfo(expr ostyir.Expr, info nativeExprInfo) ostyir.Type {
	if expr != nil && nativeTypeResolved(expr.Type()) {
		return expr.Type()
	}
	if nativeTypeResolved(info.sourceType) {
		return info.sourceType
	}
	switch info.kind {
	case nativeExprInfoString:
		return ostyir.TString
	case nativeExprInfoScalar:
		switch info.llvmType {
		case "i1":
			return ostyir.TBool
		case "i8":
			return ostyir.TByte
		case "i32":
			return ostyir.TChar
		case "i64":
			return ostyir.TInt
		case "float":
			return ostyir.TFloat32
		case "double":
			return ostyir.TFloat64
		}
	}
	return nil
}

func nativeBuiltinArgType(sourceType ostyir.Type, name string, arity int, index int) (ostyir.Type, bool) {
	named, ok := sourceType.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != name || len(named.Args) != arity || index < 0 || index >= len(named.Args) {
		return nil, false
	}
	return named.Args[index], nativeTypeResolved(named.Args[index])
}

func nativeMapLiteralInfo(ctx *nativeProjectionCtx, lit *ostyir.MapLit) (nativeExprInfo, bool) {
	if lit == nil {
		return nativeExprInfo{}, false
	}
	if nativeTypeResolved(lit.KeyT) && nativeTypeResolved(lit.ValT) {
		keyType, ok := nativeLLVMTypeFromIR(ctx, lit.KeyT)
		if !ok || !nativeMapSetKeySupported(keyType, nativeTypeIsString(lit.KeyT)) {
			return nativeExprInfo{}, false
		}
		valueType, ok := nativeLLVMTypeFromIR(ctx, lit.ValT)
		if !ok || valueType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoWithSource(nativeMapExprInfo(keyType, valueType, nativeTypeIsString(lit.KeyT)), lit.Type()), true
	}
	if len(lit.Entries) == 0 {
		return nativeExprInfo{}, false
	}
	keyInfo, ok := nativeExprTypeInfo(ctx, lit.Entries[0].Key)
	if !ok || !nativeMapSetKeySupported(keyInfo.llvmType, keyInfo.kind == nativeExprInfoString) {
		return nativeExprInfo{}, false
	}
	valueInfo, ok := nativeExprTypeInfo(ctx, lit.Entries[0].Value)
	if !ok || valueInfo.llvmType == "" || valueInfo.llvmType == "void" {
		return nativeExprInfo{}, false
	}
	keySource := nativeSourceTypeFromExprOrInfo(lit.Entries[0].Key, keyInfo)
	valueSource := nativeSourceTypeFromExprOrInfo(lit.Entries[0].Value, valueInfo)
	if !nativeTypeResolved(keySource) || !nativeTypeResolved(valueSource) {
		return nativeExprInfo{}, false
	}
	for _, entry := range lit.Entries[1:] {
		otherKey, ok := nativeExprTypeInfo(ctx, entry.Key)
		if !ok || otherKey.llvmType != keyInfo.llvmType || otherKey.kind == nativeExprInfoString != (keyInfo.kind == nativeExprInfoString) {
			return nativeExprInfo{}, false
		}
		otherValue, ok := nativeExprTypeInfo(ctx, entry.Value)
		if !ok || otherValue.llvmType != valueInfo.llvmType {
			return nativeExprInfo{}, false
		}
		if otherSource := nativeSourceTypeFromExprOrInfo(entry.Key, otherKey); !nativeTypeResolved(otherSource) || otherSource.String() != keySource.String() {
			return nativeExprInfo{}, false
		}
		if otherSource := nativeSourceTypeFromExprOrInfo(entry.Value, otherValue); !nativeTypeResolved(otherSource) || otherSource.String() != valueSource.String() {
			return nativeExprInfo{}, false
		}
	}
	return nativeExprInfoWithSource(nativeMapExprInfo(keyInfo.llvmType, valueInfo.llvmType, keyInfo.kind == nativeExprInfoString), &ostyir.NamedType{
		Name:    "Map",
		Builtin: true,
		Args:    []ostyir.Type{keySource, valueSource},
	}), true
}

func nativeExprInfoFromType(ctx *nativeProjectionCtx, t ostyir.Type) (nativeExprInfo, bool) {
	switch tt := t.(type) {
	case nil:
		return nativeExprInfo{}, false
	case *ostyir.ErrType, *ostyir.TypeVar:
		return nativeExprInfo{}, false
	case *ostyir.PrimType:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, tt)
		if !ok || llvmType == "void" {
			return nativeExprInfo{}, false
		}
		if tt.Kind == ostyir.PrimString {
			return nativeExprInfoWithSource(nativeStringExprInfo(), t), true
		}
		return nativeExprInfoWithSource(nativeExprInfoFromLLVMType(llvmType), t), true
	case *ostyir.NamedType:
		if tt.Builtin {
			switch tt.Name {
			case "List":
				if len(tt.Args) != 1 {
					return nativeExprInfo{}, false
				}
				elemType, ok := nativeLLVMTypeFromIR(ctx, tt.Args[0])
				if !ok {
					return nativeExprInfo{}, false
				}
				return nativeExprInfoWithSource(nativeListExprInfo(elemType, nativeTypeIsString(tt.Args[0]), nativeTypeIsBytes(tt.Args[0])), t), true
			case "Map":
				if len(tt.Args) != 2 {
					return nativeExprInfo{}, false
				}
				keyType, ok := nativeLLVMTypeFromIR(ctx, tt.Args[0])
				if !ok {
					return nativeExprInfo{}, false
				}
				valueType, _ := nativeLLVMTypeFromIR(ctx, tt.Args[1])
				return nativeExprInfoWithSource(nativeMapExprInfo(keyType, valueType, nativeTypeIsString(tt.Args[0])), t), true
			case "Set":
				if len(tt.Args) != 1 {
					return nativeExprInfo{}, false
				}
				elemType, ok := nativeLLVMTypeFromIR(ctx, tt.Args[0])
				if !ok {
					return nativeExprInfo{}, false
				}
				return nativeExprInfoWithSource(nativeSetExprInfo(elemType, nativeTypeIsString(tt.Args[0])), t), true
			default:
				return nativeExprInfo{}, false
			}
		}
		info, ok := nativeStructInfoFromType(ctx, tt)
		if !ok {
			return nativeExprInfo{}, false
		}
		return nativeExprInfo{
			kind:       nativeExprInfoStruct,
			llvmType:   info.def.llvmType,
			sourceType: t,
			structName: tt.Name,
		}, true
	case *ostyir.OptionalType:
		innerType, ok := nativeLLVMTypeFromIR(ctx, tt.Inner)
		if !ok || innerType == "" || innerType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfo{
			kind:            nativeExprInfoOptional,
			llvmType:        "ptr",
			sourceType:      t,
			optionInnerType: innerType,
		}, true
	default:
		return nativeExprInfo{}, false
	}
}

func nativeExprTypeInfo(ctx *nativeProjectionCtx, expr ostyir.Expr) (nativeExprInfo, bool) {
	if expr == nil {
		return nativeExprInfo{}, false
	}
	if info, ok := nativeExprInfoFromType(ctx, expr.Type()); ok {
		return info, true
	}
	switch e := expr.(type) {
	case *ostyir.IntLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok || llvmType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(llvmType), true
	case *ostyir.FloatLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok || llvmType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(llvmType), true
	case *ostyir.BoolLit:
		return nativeExprInfoFromLLVMType("i1"), true
	case *ostyir.StringLit:
		return nativeExprInfoWithSource(nativeStringExprInfo(), ostyir.TString), true
	case *ostyir.CharLit:
		return nativeExprInfoWithSource(nativeExprInfoFromLLVMType("i32"), ostyir.TChar), true
	case *ostyir.ByteLit:
		return nativeExprInfoWithSource(nativeExprInfoFromLLVMType("i8"), ostyir.TByte), true
	case *ostyir.Ident:
		if info, ok := ctx.lookupScopeName(e.Name); ok {
			return info, true
		}
		if value, ok := ctx.globalConsts[e.Name]; ok {
			return nativeExprInfoFromLLVMType(value.llvmType), true
		}
		return nativeExprInfo{}, false
	case *ostyir.StructLit:
		if info, ok := nativeExprInfoFromType(ctx, e.Type()); ok {
			return info, true
		}
		if e.TypeName == "" {
			return nativeExprInfo{}, false
		}
		info := ctx.structsByName[e.TypeName]
		if info == nil {
			return nativeExprInfo{}, false
		}
		return nativeExprInfo{
			kind:       nativeExprInfoStruct,
			llvmType:   info.def.llvmType,
			sourceType: &ostyir.NamedType{Name: e.TypeName},
			structName: e.TypeName,
		}, true
	case *ostyir.TupleLit:
		return nativeExprInfoFromType(ctx, e.Type())
	case *ostyir.MapLit:
		return nativeMapLiteralInfo(ctx, e)
	case *ostyir.FieldExpr:
		return nativeFieldExprInfo(ctx, e)
	case *ostyir.IndexExpr:
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok {
			return nativeExprInfo{}, false
		}
		switch baseInfo.kind {
		case nativeExprInfoList:
			elemSource, hasElemSource := nativeBuiltinArgType(baseInfo.sourceType, "List", 1, 0)
			if hasElemSource {
				if info, ok := nativeExprInfoFromType(ctx, elemSource); ok {
					return info, true
				}
			}
			if baseInfo.listElemType == "" {
				return nativeExprInfo{}, false
			}
			if baseInfo.listElemString {
				return nativeExprInfoWithSource(nativeStringExprInfo(), ostyir.TString), true
			}
			if hasElemSource {
				return nativeExprInfoWithSource(nativeExprInfoFromLLVMType(baseInfo.listElemType), elemSource), true
			}
			return nativeExprInfoFromLLVMType(baseInfo.listElemType), true
		case nativeExprInfoMap:
			valueSource, hasValueSource := nativeBuiltinArgType(baseInfo.sourceType, "Map", 2, 1)
			if hasValueSource {
				if info, ok := nativeExprInfoFromType(ctx, valueSource); ok {
					return info, true
				}
			}
			if baseInfo.mapValueType == "" {
				return nativeExprInfo{}, false
			}
			if hasValueSource {
				return nativeExprInfoWithSource(nativeExprInfoFromLLVMType(baseInfo.mapValueType), valueSource), true
			}
			return nativeExprInfoFromLLVMType(baseInfo.mapValueType), true
		default:
			return nativeExprInfo{}, false
		}
	case *ostyir.TupleAccess:
		info, ok := nativeTupleInfoFromType(ctx, e.X.Type())
		if !ok || e.Index < 0 || e.Index >= len(info.elemLLVMTypes) {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(info.elemLLVMTypes[e.Index]), true
	case *ostyir.QuestionExpr:
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok || baseInfo.kind != nativeExprInfoOptional {
			return nativeExprInfo{}, false
		}
		innerSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(e.X, baseInfo))
		if ok {
			if info, ok := nativeExprInfoFromType(ctx, innerSource); ok {
				return info, true
			}
		}
		if baseInfo.optionInnerType == "" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(baseInfo.optionInnerType), true
	case *ostyir.CoalesceExpr:
		leftInfo, ok := nativeExprTypeInfo(ctx, e.Left)
		if !ok || leftInfo.kind != nativeExprInfoOptional {
			return nativeExprInfo{}, false
		}
		innerSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(e.Left, leftInfo))
		if ok {
			if info, ok := nativeExprInfoFromType(ctx, innerSource); ok {
				return info, true
			}
		}
		if leftInfo.optionInnerType == "" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(leftInfo.optionInnerType), true
	case *ostyir.MethodCall:
		return nativeBuiltinMethodReturnInfo(ctx, e)
	default:
		return nativeExprInfo{}, false
	}
}

func nativeFieldExprInfo(ctx *nativeProjectionCtx, expr *ostyir.FieldExpr) (nativeExprInfo, bool) {
	if ctx == nil || expr == nil {
		return nativeExprInfo{}, false
	}
	if expr.Optional {
		baseInfo, ok := nativeExprTypeInfo(ctx, expr.X)
		if !ok || baseInfo.kind != nativeExprInfoOptional {
			return nativeExprInfo{}, false
		}
		baseSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(expr.X, baseInfo))
		if !ok {
			return nativeExprInfo{}, false
		}
		structInfo, ok := nativeStructInfoFromType(ctx, baseSource)
		if !ok {
			return nativeExprInfo{}, false
		}
		fieldInfo, ok := structInfo.byName[expr.Name]
		if !ok || fieldInfo.llvmType != "ptr" {
			return nativeExprInfo{}, false
		}
		optionalField := wrapOptionalIRType(fieldInfo.irType)
		if info, ok := nativeExprInfoFromType(ctx, optionalField); ok {
			return info, true
		}
		return nativeExprInfo{
			kind:            nativeExprInfoOptional,
			llvmType:        "ptr",
			sourceType:      optionalField,
			optionInnerType: fieldInfo.llvmType,
		}, true
	}
	baseInfo, ok := nativeExprTypeInfo(ctx, expr.X)
	if !ok || baseInfo.kind != nativeExprInfoStruct || baseInfo.structName == "" {
		return nativeExprInfo{}, false
	}
	structInfo := ctx.structsByName[baseInfo.structName]
	if structInfo == nil {
		return nativeExprInfo{}, false
	}
	fieldInfo, ok := structInfo.byName[expr.Name]
	if !ok {
		return nativeExprInfo{}, false
	}
	if info, ok := nativeExprInfoFromType(ctx, fieldInfo.irType); ok {
		return info, true
	}
	return nativeExprInfoFromLLVMType(fieldInfo.llvmType), true
}

func nativeBuiltinMethodReturnInfo(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (nativeExprInfo, bool) {
	if ctx == nil || e == nil || e.Receiver == nil {
		return nativeExprInfo{}, false
	}
	if receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver); ok {
		switch receiverInfo.kind {
		case nativeExprInfoOptional:
			switch e.Name {
			case "isSome", "isNone":
				return nativeExprInfoFromLLVMType("i1"), true
			}
		case nativeExprInfoString:
			switch e.Name {
			case "len", "count", "charCount":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty", "startsWith", "endsWith", "contains":
				return nativeExprInfoFromLLVMType("i1"), true
			case "split", "lines":
				return nativeListExprInfo("ptr", true, false), true
			case "trim", "trimStart", "trimEnd", "trimPrefix", "trimSuffix", "join", "replace", "replaceAll", "repeat", "toString":
				return nativeStringExprInfo(), true
			case "chars":
				return nativeListExprInfo("i32", false, false), true
			case "bytes":
				return nativeListExprInfo("i8", false, false), true
			}
		case nativeExprInfoList:
			switch e.Name {
			case "len":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty":
				return nativeExprInfoFromLLVMType("i1"), true
			case "sorted":
				return receiverInfo, true
			case "toSet":
				return nativeSetExprInfo(receiverInfo.listElemType, receiverInfo.listElemString), true
			case "push", "insert":
				return nativeExprInfo{}, false
			}
		case nativeExprInfoMap:
			switch e.Name {
			case "len":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty", "containsKey":
				return nativeExprInfoFromLLVMType("i1"), true
			case "keys":
				return nativeListExprInfo(receiverInfo.mapKeyType, receiverInfo.mapKeyString, false), true
			}
		case nativeExprInfoSet:
			switch e.Name {
			case "len":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty", "contains", "insert", "remove":
				return nativeExprInfoFromLLVMType("i1"), true
			case "toList":
				return nativeListExprInfo(receiverInfo.setElemType, receiverInfo.setElemString, false), true
			}
		}
	}
	return nativeExprInfo{}, false
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

func nativeTupleInfoFromType(ctx *nativeProjectionCtx, t ostyir.Type) (*nativeTupleInfo, bool) {
	if ctx == nil {
		return nil, false
	}
	tuple, ok := t.(*ostyir.TupleType)
	if !ok || tuple == nil {
		return nil, false
	}
	llvmType, ok := nativeLLVMTypeFromIR(ctx, tuple)
	if !ok {
		return nil, false
	}
	info := ctx.tuplesByLLVMType[llvmType]
	return info, info != nil
}

func nativeRegisterTupleType(ctx *nativeProjectionCtx, tuple *ostyir.TupleType) (string, bool) {
	if ctx == nil || tuple == nil {
		return "", false
	}
	elemLLVMTypes := make([]string, 0, len(tuple.Elems))
	fields := make([]*llvmNativeStructField, 0, len(tuple.Elems))
	for _, elem := range tuple.Elems {
		llvmType, ok := nativeLLVMTypeFromIR(ctx, elem)
		if !ok || llvmType == "void" {
			return "", false
		}
		elemLLVMTypes = append(elemLLVMTypes, llvmType)
		fields = append(fields, &llvmNativeStructField{llvmType: llvmType})
	}
	llvmType := llvmTupleTypeName(elemLLVMTypes)
	if _, exists := ctx.tuplesByLLVMType[llvmType]; exists {
		return llvmType, true
	}
	ctx.tuplesByLLVMType[llvmType] = &nativeTupleInfo{
		def: &llvmNativeStruct{
			name:     strings.TrimPrefix(llvmType, "%"),
			llvmType: llvmType,
			fields:   fields,
		},
		elemLLVMTypes: elemLLVMTypes,
	}
	ctx.tupleOrder = append(ctx.tupleOrder, llvmType)
	return llvmType, true
}

func nativeBuiltinMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	if ctx == nil || e == nil || e.Receiver == nil {
		return nil, false
	}
	if expr, ok := nativeOptionMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeStringMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeListMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeMapMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeSetMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	return nil, false
}

func nativeOptionMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoOptional {
		return nil, false
	}
	if len(e.Args) != 0 {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "isSome":
		return &llvmNativeExpr{
			kind:       llvmNativeExprOptionCheck,
			llvmType:   "i1",
			boolValue:  true,
			childExprs: []*llvmNativeExpr{receiver},
		}, true
	case "isNone":
		return &llvmNativeExpr{
			kind:       llvmNativeExprOptionCheck,
			llvmType:   "i1",
			boolValue:  false,
			childExprs: []*llvmNativeExpr{receiver},
		}, true
	default:
		return nil, false
	}
}

func nativeStringMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoString {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i64", llvmStringRuntimeByteLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmStringRuntimeByteLenSymbol(), receiver)), true
	case "startsWith":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i1", llvmStringRuntimeHasPrefixSymbol(), receiver, arg), true
	case "endsWith":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i1", llvmStringRuntimeHasSuffixSymbol(), receiver, arg), true
	case "contains":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i1", llvmStringRuntimeContainsSymbol(), receiver, arg), true
	case "split":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeSplitSymbol(), receiver, arg), true
	case "lines":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeSplitSymbol(), receiver, nativeStringLiteralExpr("\n")), true
	case "join":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeJoinSymbol(), arg, receiver), true
	case "trim":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimSpaceSymbol(), receiver), true
	case "trimStart":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimStartSymbol(), receiver), true
	case "trimEnd":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimEndSymbol(), receiver), true
	case "trimPrefix":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimPrefixSymbol(), receiver, arg), true
	case "trimSuffix":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimSuffixSymbol(), receiver, arg), true
	case "replace":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{"ptr", "ptr"})
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeReplaceSymbol(), receiver, args[0], args[1]), true
	case "replaceAll":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{"ptr", "ptr"})
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeReplaceAllSymbol(), receiver, args[0], args[1]), true
	case "repeat":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "i64")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeRepeatSymbol(), receiver, arg), true
	case "count":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i64", llvmStringRuntimeCountSymbol(), receiver, arg), true
	case "chars":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeCharsSymbol(), receiver), true
	case "bytes":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeBytesSymbol(), receiver), true
	case "charCount":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		ctx.needsListRT = true
		return nativeRuntimeCallExpr(
			"i64",
			llvmListRuntimeLenSymbol(),
			nativeRuntimeCallExpr("ptr", llvmStringRuntimeCharsSymbol(), receiver),
		), true
	case "toString":
		if len(e.Args) != 0 {
			return nil, false
		}
		return receiver, true
	default:
		return nil, false
	}
}

func nativeListMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoList {
		return nil, false
	}
	elemType, elemString := receiverInfo.listElemType, receiverInfo.listElemString
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("i64", llvmListRuntimeLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmListRuntimeLenSymbol(), receiver)), true
	case "sorted":
		if len(e.Args) != 0 {
			return nil, false
		}
		symbol := llvmListRuntimeSortedSymbol(elemType, elemString)
		if symbol == "" {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("ptr", symbol, receiver), true
	case "toSet":
		if len(e.Args) != 0 {
			return nil, false
		}
		if receiverInfo.listElemBytes {
			return nil, false
		}
		symbol := llvmListRuntimeToSetSymbol(elemType, elemString)
		if symbol == "" {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("ptr", symbol, receiver), true
	case "push":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok || !llvmListUsesTypedRuntime(elemType) {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("void", llvmListRuntimePushSymbol(llvmListElementSuffix(elemType)), receiver, arg), true
	case "insert":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{"i64", elemType})
		if !ok || !llvmListUsesTypedRuntime(elemType) {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("void", llvmListRuntimeInsertSymbol(llvmListElementSuffix(elemType)), receiver, args[0], args[1]), true
	default:
		return nil, false
	}
}

func nativeMapMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoMap {
		return nil, false
	}
	keyType, keyString := receiverInfo.mapKeyType, receiverInfo.mapKeyString
	if !nativeMapSetKeySupported(keyType, keyString) {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("i64", llvmMapRuntimeLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmMapRuntimeLenSymbol(), receiver)), true
	case "containsKey":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, keyType)
		if !ok {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("i1", llvmMapRuntimeContainsSymbol(keyType, keyString), receiver, arg), true
	case "keys":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("ptr", llvmMapRuntimeKeysSymbol(), receiver), true
	case "remove":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, keyType)
		if !ok {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("i1", llvmMapRuntimeRemoveSymbol(keyType, keyString), receiver, arg), true
	case "insert":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{keyType, receiverInfo.mapValueType})
		if !ok {
			return nil, false
		}
		ctx.needsMapRT = true
		return &llvmNativeExpr{
			kind:            llvmNativeExprCall,
			llvmType:        "void",
			name:            llvmMapRuntimeInsertSymbol(keyType, keyString),
			spillArgIndices: []int{2},
			childExprs:      []*llvmNativeExpr{receiver, args[0], args[1]},
		}, true
	default:
		return nil, false
	}
}

func nativeSetMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoSet {
		return nil, false
	}
	elemType, elemString := receiverInfo.setElemType, receiverInfo.setElemString
	if !nativeMapSetKeySupported(elemType, elemString) {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i64", llvmSetRuntimeLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmSetRuntimeLenSymbol(), receiver)), true
	case "contains":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i1", llvmSetRuntimeContainsSymbol(elemType, elemString), receiver, arg), true
	case "insert":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i1", llvmSetRuntimeInsertSymbol(elemType, elemString), receiver, arg), true
	case "remove":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i1", llvmSetRuntimeRemoveSymbol(elemType, elemString), receiver, arg), true
	case "toList":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("ptr", llvmSetRuntimeToListSymbol(), receiver), true
	default:
		return nil, false
	}
}

func nativeSinglePositionalArgExpr(ctx *nativeProjectionCtx, args []ostyir.Arg) (*llvmNativeExpr, bool) {
	values, ok := nativePositionalArgsFromIR(ctx, args, 1)
	if !ok {
		return nil, false
	}
	return values[0], true
}

func nativeSinglePositionalArgExprWithHint(ctx *nativeProjectionCtx, args []ostyir.Arg, hintLLVMType string) (*llvmNativeExpr, bool) {
	values, ok := nativePositionalArgsFromIRWithHints(ctx, args, []string{hintLLVMType})
	if !ok {
		return nil, false
	}
	return values[0], true
}

func nativePositionalArgsFromIR(ctx *nativeProjectionCtx, args []ostyir.Arg, expected int) ([]*llvmNativeExpr, bool) {
	if len(args) != expected {
		return nil, false
	}
	out := make([]*llvmNativeExpr, 0, len(args))
	for _, arg := range args {
		if arg.IsKeyword() {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, arg.Value)
		if !ok {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func nativePositionalArgsFromIRWithHints(ctx *nativeProjectionCtx, args []ostyir.Arg, hintLLVMTypes []string) ([]*llvmNativeExpr, bool) {
	if len(args) != len(hintLLVMTypes) {
		return nil, false
	}
	out := make([]*llvmNativeExpr, 0, len(args))
	for i, arg := range args {
		if arg.IsKeyword() {
			return nil, false
		}
		value, ok := nativeExprFromIRWithHint(ctx, arg.Value, hintLLVMTypes[i])
		if !ok {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func nativeExprFromIRWithHint(ctx *nativeProjectionCtx, expr ostyir.Expr, hintLLVMType string) (*llvmNativeExpr, bool) {
	if value, ok := nativeExprFromIR(ctx, expr); ok {
		return value, true
	}
	switch e := expr.(type) {
	case *ostyir.IntLit:
		if hintLLVMType == "" {
			return nil, false
		}
		text, ok := normalizeNativeIntText(e.Text)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: hintLLVMType, text: text}, true
	case *ostyir.FloatLit:
		if hintLLVMType != "double" {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:     llvmNativeExprFloat,
			llvmType: hintLLVMType,
			text:     strings.ReplaceAll(e.Text, "_", ""),
		}, true
	case *ostyir.BoolLit:
		if hintLLVMType != "i1" {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprBool, llvmType: "i1", boolValue: e.Value}, true
	case *ostyir.StringLit:
		if hintLLVMType != "ptr" {
			return nil, false
		}
		text, ok := nativePlainStringText(e)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprString, llvmType: "ptr", text: text}, true
	case *ostyir.CharLit:
		if hintLLVMType != "i32" {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i32", text: strconv.FormatInt(int64(e.Value), 10)}, true
	case *ostyir.ByteLit:
		if hintLLVMType != "i8" {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i8", text: strconv.FormatInt(int64(e.Value), 10)}, true
	default:
		return nil, false
	}
}

func nativeRuntimeCallExpr(llvmType string, name string, args ...*llvmNativeExpr) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:       llvmNativeExprCall,
		llvmType:   llvmType,
		name:       name,
		childExprs: args,
	}
}

func nativeEqZeroExpr(inner *llvmNativeExpr) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:     llvmNativeExprBinary,
		llvmType: "i1",
		op:       "==",
		childExprs: []*llvmNativeExpr{
			inner,
			nativeIntLiteralExpr("0"),
		},
	}
}

func nativeIntLiteralExpr(text string) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:     llvmNativeExprInt,
		llvmType: "i64",
		text:     text,
	}
}

func nativeStringLiteralExpr(text string) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:     llvmNativeExprString,
		llvmType: "ptr",
		text:     text,
	}
}

func nativeListMethodInfo(ctx *nativeProjectionCtx, t ostyir.Type) (elemType string, elemString bool, ok bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "List" || len(named.Args) != 1 {
		return "", false, false
	}
	elemType, ok = nativeLLVMTypeFromIR(ctx, named.Args[0])
	if !ok {
		return "", false, false
	}
	return elemType, nativeTypeIsString(named.Args[0]), true
}

func nativeMapMethodInfo(ctx *nativeProjectionCtx, t ostyir.Type) (keyType string, valueType string, keyString bool, ok bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "Map" || len(named.Args) != 2 {
		return "", "", false, false
	}
	keyType, ok = nativeLLVMTypeFromIR(ctx, named.Args[0])
	if !ok {
		return "", "", false, false
	}
	if valueType, ok = nativeLLVMTypeFromIR(ctx, named.Args[1]); !ok {
		valueType = ""
	}
	return keyType, valueType, nativeTypeIsString(named.Args[0]), true
}

func nativeSetMethodInfo(ctx *nativeProjectionCtx, t ostyir.Type) (elemType string, elemString bool, ok bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "Set" || len(named.Args) != 1 {
		return "", false, false
	}
	elemType, ok = nativeLLVMTypeFromIR(ctx, named.Args[0])
	if !ok {
		return "", false, false
	}
	return elemType, nativeTypeIsString(named.Args[0]), true
}

func unwrapOptionalIRType(t ostyir.Type) (ostyir.Type, bool) {
	opt, ok := t.(*ostyir.OptionalType)
	if !ok || opt == nil || opt.Inner == nil {
		return nil, false
	}
	return opt.Inner, true
}

func wrapOptionalIRType(t ostyir.Type) ostyir.Type {
	if t == nil {
		return nil
	}
	return &ostyir.OptionalType{Inner: t}
}

func listElementType(t ostyir.Type) ostyir.Type {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "List" || len(named.Args) != 1 {
		return nil
	}
	return named.Args[0]
}

func nativeTypeIsString(t ostyir.Type) bool {
	prim, ok := t.(*ostyir.PrimType)
	return ok && prim.Kind == ostyir.PrimString
}

func nativeTypeIsBytes(t ostyir.Type) bool {
	prim, ok := t.(*ostyir.PrimType)
	return ok && prim.Kind == ostyir.PrimBytes
}

func nativeMapSetKeySupported(llvmType string, isString bool) bool {
	if isString {
		return true
	}
	switch llvmType {
	case "i64", "i1", "double", "ptr":
		return true
	default:
		return false
	}
}

func nativeMethodOwnerName(t ostyir.Type) (string, bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || named.Builtin || named.Package != "" || len(named.Args) != 0 {
		return "", false
	}
	return named.Name, true
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
		if tt.Builtin {
			switch tt.Name {
			case "List", "Map", "Set":
				return "ptr", true
			}
			return "", false
		}
		info, ok := nativeStructInfoFromType(ctx, tt)
		if !ok {
			return "", false
		}
		return info.def.llvmType, true
	case *ostyir.OptionalType:
		if _, ok := nativeLLVMTypeFromIR(ctx, tt.Inner); !ok {
			return "", false
		}
		return "ptr", true
	case *ostyir.TupleType:
		return nativeRegisterTupleType(ctx, tt)
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
