// native_entry_snapshot.go snapshots the native-owned primitive module-entry
// slice from toolchain/llvmgen.osty into the bootstrap bridge. The Go side
// projects internal/ir.Module into these LLVM-typed structs, then falls back
// to the legacy IR -> AST bridge when a shape still sits outside this slice.

package llvmgen

import "strings"

type llvmNativeExprKind int

const (
	llvmNativeExprInvalid llvmNativeExprKind = iota
	llvmNativeExprInt
	llvmNativeExprFloat
	llvmNativeExprBool
	llvmNativeExprString
	llvmNativeExprIdent
	llvmNativeExprStructLit
	llvmNativeExprField
	llvmNativeExprUnary
	llvmNativeExprBinary
	llvmNativeExprCall
	llvmNativeExprPrintln
	llvmNativeExprIf
)

type llvmNativeStmtKind int

const (
	llvmNativeStmtInvalid llvmNativeStmtKind = iota
	llvmNativeStmtExpr
	llvmNativeStmtLet
	llvmNativeStmtMutLet
	llvmNativeStmtAssign
	llvmNativeStmtReturn
	llvmNativeStmtIf
	llvmNativeStmtWhile
	llvmNativeStmtRange
)

type llvmNativeExpr struct {
	kind        llvmNativeExprKind
	llvmType    string
	text        string
	name        string
	op          string
	fieldIndex  int
	boolValue   bool
	inclusive   bool
	childExprs  []*llvmNativeExpr
	childBlocks []*llvmNativeBlock
}

type llvmNativeStmt struct {
	kind        llvmNativeStmtKind
	name        string
	llvmType    string
	op          string
	inclusive   bool
	childExprs  []*llvmNativeExpr
	childBlocks []*llvmNativeBlock
}

type llvmNativeBlock struct {
	stmts     []*llvmNativeStmt
	hasResult bool
	result    *llvmNativeExpr
}

type llvmNativeParam struct {
	name     string
	llvmType string
}

type llvmNativeStructField struct {
	name     string
	llvmType string
}

type llvmNativeStruct struct {
	name     string
	llvmType string
	fields   []*llvmNativeStructField
}

type llvmNativeFunction struct {
	name       string
	returnType string
	params     []*llvmNativeParam
	body       *llvmNativeBlock
}

type llvmNativeModule struct {
	sourcePath string
	target     string
	structs    []*llvmNativeStruct
	functions  []*llvmNativeFunction
}

type llvmNativeRenderedFunction struct {
	definition    string
	stringGlobals []*LlvmStringGlobal
	nextStringID  int
}

type llvmNativeMaybeValue struct {
	hasValue bool
	value    *LlvmValue
}

func llvmNativeEmitModule(mod *llvmNativeModule) string {
	if mod == nil {
		return llvmRenderModule("", "", nil)
	}
	typeDefs := make([]string, 0, len(mod.structs))
	definitions := make([]string, 0, len(mod.functions))
	stringGlobals := make([]*LlvmStringGlobal, 0)
	nextStringID := 0
	for _, st := range mod.structs {
		if st == nil {
			continue
		}
		fieldTypes := make([]string, 0, len(st.fields))
		for _, field := range st.fields {
			fieldTypes = append(fieldTypes, field.llvmType)
		}
		typeDefs = append(typeDefs, llvmStructTypeDef(strings.TrimPrefix(st.llvmType, "%"), fieldTypes))
	}
	for _, fn := range mod.functions {
		rendered := llvmNativeEmitFunction(fn, nextStringID)
		nextStringID = rendered.nextStringID
		definitions = append(definitions, rendered.definition)
		stringGlobals = append(stringGlobals, rendered.stringGlobals...)
	}
	return llvmRenderModuleWithGlobalsAndTypes(mod.sourcePath, mod.target, typeDefs, stringGlobals, definitions)
}

func llvmNativeEmitFunction(fn *llvmNativeFunction, startStringID int) llvmNativeRenderedFunction {
	emitter := llvmEmitter()
	emitter.stringId = startStringID
	params := make([]*LlvmParam, 0, len(fn.params))
	for _, param := range fn.params {
		params = append(params, llvmParam(param.name, param.llvmType))
		llvmBind(emitter, param.name, &LlvmValue{
			typ:     param.llvmType,
			name:    "%" + param.name,
			pointer: false,
		})
	}
	block := llvmNativeEmitBlock(emitter, fn.body)
	if !llvmNativeBodyHasTerminator(emitter.body) {
		switch {
		case block.hasValue && fn.returnType != "" && fn.returnType != "void":
			llvmReturn(emitter, block.value)
		case fn.returnType == "i32" && fn.name == "main":
			llvmReturnI32Zero(emitter)
		case fn.returnType == "" || fn.returnType == "void":
			emitter.body = append(emitter.body, "  ret void")
		default:
			llvmReturn(emitter, llvmNativeZeroValue(fn.returnType))
		}
	}
	retType := fn.returnType
	if retType == "" {
		retType = "void"
	}
	return llvmNativeRenderedFunction{
		definition:    llvmRenderFunction(retType, fn.name, params, emitter.body),
		stringGlobals: append([]*LlvmStringGlobal(nil), emitter.stringGlobals...),
		nextStringID:  emitter.stringId,
	}
}

func llvmNativeEmitBlock(emitter *LlvmEmitter, block *llvmNativeBlock) llvmNativeMaybeValue {
	if block == nil {
		return llvmNativeMaybeValue{}
	}
	for _, stmt := range block.stmts {
		llvmNativeEmitStmt(emitter, stmt)
	}
	if block.hasResult && block.result != nil {
		return llvmNativeMaybeValue{hasValue: true, value: llvmNativeEvalExpr(emitter, block.result)}
	}
	return llvmNativeMaybeValue{}
}

func llvmNativeEmitStmt(emitter *LlvmEmitter, stmt *llvmNativeStmt) {
	if stmt == nil {
		return
	}
	switch stmt.kind {
	case llvmNativeStmtExpr:
		if len(stmt.childExprs) > 0 {
			_ = llvmNativeEvalExpr(emitter, stmt.childExprs[0])
		}
	case llvmNativeStmtLet:
		if len(stmt.childExprs) > 0 {
			llvmImmutableLet(emitter, stmt.name, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		}
	case llvmNativeStmtMutLet:
		if len(stmt.childExprs) > 0 {
			llvmMutableLet(emitter, stmt.name, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		}
	case llvmNativeStmtAssign:
		if len(stmt.childExprs) > 0 {
			_ = llvmAssign(emitter, stmt.name, llvmNativeAssignValue(emitter, stmt))
		}
	case llvmNativeStmtReturn:
		if len(stmt.childExprs) > 0 {
			llvmReturn(emitter, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		} else if stmt.llvmType == "i32" {
			llvmReturnI32Zero(emitter)
		} else {
			emitter.body = append(emitter.body, "  ret void")
		}
	case llvmNativeStmtIf:
		if len(stmt.childExprs) == 0 || len(stmt.childBlocks) == 0 {
			return
		}
		labels := llvmIfStart(emitter, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[0])
		llvmIfElse(emitter, labels)
		if len(stmt.childBlocks) > 1 {
			_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[1])
		}
		llvmIfEnd(emitter, labels)
	case llvmNativeStmtWhile:
		if len(stmt.childExprs) == 0 || len(stmt.childBlocks) == 0 {
			return
		}
		condLabel := llvmNextLabel(emitter, "for.cond")
		bodyLabel := llvmNextLabel(emitter, "for.body")
		endLabel := llvmNextLabel(emitter, "for.end")
		emitter.body = append(emitter.body, "  br label %"+condLabel)
		emitter.body = append(emitter.body, condLabel+":")
		cond := llvmNativeEvalExpr(emitter, stmt.childExprs[0])
		emitter.body = append(emitter.body, "  br i1 "+cond.name+", label %"+bodyLabel+", label %"+endLabel)
		emitter.body = append(emitter.body, bodyLabel+":")
		_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[0])
		emitter.body = append(emitter.body, "  br label %"+condLabel)
		emitter.body = append(emitter.body, endLabel+":")
	case llvmNativeStmtRange:
		if len(stmt.childExprs) < 2 || len(stmt.childBlocks) == 0 {
			return
		}
		start := llvmNativeEvalExpr(emitter, stmt.childExprs[0])
		end := llvmNativeEvalExpr(emitter, stmt.childExprs[1])
		var loop *LlvmRangeLoop
		if stmt.inclusive {
			loop = llvmInclusiveRangeStart(emitter, stmt.name, start, end)
		} else {
			loop = llvmRangeStart(emitter, stmt.name, start, end, false)
		}
		_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[0])
		llvmRangeEnd(emitter, loop)
	}
}

func llvmNativeAssignValue(emitter *LlvmEmitter, stmt *llvmNativeStmt) *LlvmValue {
	value := llvmNativeEvalExpr(emitter, stmt.childExprs[0])
	if stmt.op == "" || stmt.op == "=" {
		return value
	}
	current := llvmIdent(emitter, stmt.name)
	return llvmNativeApplyBinary(emitter, stmt.op, current, value, current.typ)
}

func llvmNativeEvalExpr(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if expr == nil {
		return llvmNativeZeroValue("i64")
	}
	switch expr.kind {
	case llvmNativeExprInt:
		return &LlvmValue{typ: expr.llvmType, name: expr.text}
	case llvmNativeExprFloat:
		return &LlvmValue{typ: expr.llvmType, name: expr.text}
	case llvmNativeExprBool:
		name := "false"
		if expr.boolValue {
			name = "true"
		}
		return &LlvmValue{typ: expr.llvmType, name: name}
	case llvmNativeExprString:
		return llvmStringLiteral(emitter, expr.text)
	case llvmNativeExprIdent:
		return llvmIdent(emitter, expr.name)
	case llvmNativeExprStructLit:
		fields := make([]*LlvmValue, 0, len(expr.childExprs))
		for _, child := range expr.childExprs {
			fields = append(fields, llvmNativeEvalExpr(emitter, child))
		}
		return llvmStructLiteral(emitter, expr.llvmType, fields)
	case llvmNativeExprField:
		if len(expr.childExprs) == 0 {
			return llvmNativeZeroValue(expr.llvmType)
		}
		base := llvmNativeEvalExpr(emitter, expr.childExprs[0])
		return llvmExtractValue(emitter, base, expr.llvmType, expr.fieldIndex)
	case llvmNativeExprUnary:
		return llvmNativeEvalUnary(emitter, expr)
	case llvmNativeExprBinary:
		return llvmNativeEvalBinary(emitter, expr)
	case llvmNativeExprCall:
		return llvmNativeEvalCall(emitter, expr)
	case llvmNativeExprPrintln:
		return llvmNativeEvalPrintln(emitter, expr)
	case llvmNativeExprIf:
		return llvmNativeEvalIfExpr(emitter, expr)
	default:
		return llvmNativeZeroValue(expr.llvmType)
	}
}

func llvmNativeEvalUnary(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	value := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	switch expr.op {
	case "+":
		return value
	case "!":
		return llvmNotI1(emitter, value)
	default:
		if expr.llvmType == "double" {
			return llvmBinaryF64(emitter, llvmFloatBinaryInstruction("-"), llvmNativeZeroValue("double"), value)
		}
		return llvmBinaryI64(emitter, llvmIntBinaryInstruction("-"), llvmNativeZeroValue(expr.llvmType), value)
	}
}

func llvmNativeEvalBinary(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) < 2 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	left := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	right := llvmNativeEvalExpr(emitter, expr.childExprs[1])
	return llvmNativeApplyBinary(emitter, expr.op, left, right, expr.llvmType)
}

func llvmNativeApplyBinary(emitter *LlvmEmitter, op string, left, right *LlvmValue, resultType string) *LlvmValue {
	switch op {
	case "&&", "||":
		return llvmLogicalI1(emitter, llvmLogicalInstruction(op), left, right)
	}
	switch resultType {
	case "i1":
		if left.typ == "double" || right.typ == "double" {
			return llvmCompareF64(emitter, llvmFloatComparePredicate(op), left, right)
		}
		return llvmCompare(emitter, llvmIntComparePredicate(op), left, right)
	case "double":
		return llvmBinaryF64(emitter, llvmFloatBinaryInstruction(op), left, right)
	default:
		return llvmBinaryI64(emitter, llvmIntBinaryInstruction(op), left, right)
	}
}

func llvmNativeEvalCall(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	args := make([]*LlvmValue, 0, len(expr.childExprs))
	for _, arg := range expr.childExprs {
		args = append(args, llvmNativeEvalExpr(emitter, arg))
	}
	if expr.llvmType == "" || expr.llvmType == "void" {
		llvmCallVoid(emitter, expr.name, args)
		return llvmNativeZeroValue("i64")
	}
	return llvmCall(emitter, expr.llvmType, expr.name, args)
}

func llvmNativeEvalPrintln(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 {
		return llvmNativeZeroValue("i64")
	}
	value := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	switch value.typ {
	case "double":
		llvmPrintlnF64(emitter, value)
	case "ptr":
		llvmPrintlnString(emitter, value)
	default:
		llvmPrintlnI64(emitter, value)
	}
	return llvmNativeZeroValue("i64")
}

func llvmNativeEvalIfExpr(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 || len(expr.childBlocks) < 2 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	labels := llvmIfExprStart(emitter, llvmNativeEvalExpr(emitter, expr.childExprs[0]))
	thenValue := llvmNativeBlockValue(emitter, expr.childBlocks[0], expr.llvmType)
	llvmIfExprElse(emitter, labels)
	elseValue := llvmNativeBlockValue(emitter, expr.childBlocks[1], expr.llvmType)
	return llvmIfExprEnd(emitter, expr.llvmType, thenValue, elseValue, labels)
}

func llvmNativeBlockValue(emitter *LlvmEmitter, block *llvmNativeBlock, llvmType string) *LlvmValue {
	value := llvmNativeEmitBlock(emitter, block)
	if value.hasValue {
		return value.value
	}
	return llvmNativeZeroValue(llvmType)
}

func llvmNativeZeroValue(llvmType string) *LlvmValue {
	switch llvmType {
	case "", "void":
		return llvmI64("0")
	default:
		return &LlvmValue{typ: llvmType, name: llvmZeroLiteral(llvmType)}
	}
}

func llvmNativeBodyHasTerminator(body []string) bool {
	if len(body) == 0 {
		return false
	}
	last := body[len(body)-1]
	return strings.HasPrefix(last, "  ret ") || strings.HasPrefix(last, "  br ")
}
