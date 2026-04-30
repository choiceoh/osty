package lirproto

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

const (
	rtIOWrite       = "osty_rt_io_write"
	rtIntToString   = "osty_rt_int_to_string"
	rtFloatToString = "osty_rt_float_to_string"
	rtBoolToString  = "osty_rt_bool_to_string"
	rtCharToString  = "osty_rt_char_to_string"
	rtByteToString  = "osty_rt_byte_to_string"
)

func (l *Lowerer) lowerMIRModule(out *Module, mod *mir.Module) {
	if len(mod.Globals) > 0 {
		l.error(DiagUnsupported, "MIR globals are not implemented in LIR Proto scalar lowering", "", "")
	}
	if len(mod.Uses) > 0 {
		l.error(DiagUnsupported, "MIR uses/imports are not implemented in LIR Proto scalar lowering", "", "")
	}
	functions := mirFunctionMap(mod.Functions)
	for i, fn := range mod.Functions {
		if fn == nil {
			l.error(DiagInvalidInput, fmt.Sprintf("function[%d]: nil MIR function", i), "", "")
			continue
		}
		startErrors := l.errorCount()
		lowered, ok := newMIRFunctionLowerer(l, out, fn, functions, mod.Layouts).lower()
		if ok && l.errorCount() == startErrors {
			out.Functions = append(out.Functions, lowered)
		}
	}
}

func mirFunctionMap(functions []*mir.Function) map[string]*mir.Function {
	out := make(map[string]*mir.Function, len(functions))
	for _, fn := range functions {
		if fn == nil {
			continue
		}
		if fn.Name != "" {
			out[fn.Name] = fn
		}
		if fn.ExportSymbol != "" {
			out[fn.ExportSymbol] = fn
		}
	}
	return out
}

func (l *Lowerer) errorCount() int {
	count := 0
	for _, diag := range l.diags {
		if diag.Severity == SeverityError {
			count++
		}
	}
	return count
}

type mirFunctionLowerer struct {
	l          *Lowerer
	out        *Module
	fn         *mir.Function
	block      *mir.BasicBlock
	blockLabel string
	prologue   []Instr
	instrs     []Instr
	slots      map[mir.LocalID]string
	types      map[mir.LocalID]Type
	labels     map[mir.BlockID]string
	functions  map[string]*mir.Function
	layouts    *mir.LayoutTable
	temp       int
}

func newMIRFunctionLowerer(l *Lowerer, out *Module, fn *mir.Function, functions map[string]*mir.Function, layouts *mir.LayoutTable) *mirFunctionLowerer {
	return &mirFunctionLowerer{
		l:         l,
		out:       out,
		fn:        fn,
		slots:     map[mir.LocalID]string{},
		types:     map[mir.LocalID]Type{},
		functions: functions,
		layouts:   layouts,
	}
}

func (f *mirFunctionLowerer) lower() (Function, bool) {
	startErrors := f.l.errorCount()
	name := f.functionName()
	if name == "" {
		f.error(DiagInvalidInput, "MIR function has empty name")
	}
	if f.fn.IsExternal {
		f.error(DiagUnsupported, "external MIR functions are not implemented in LIR Proto scalar lowering")
	}
	if f.fn.IsIntrinsic {
		f.error(DiagUnsupported, "intrinsic MIR functions are not implemented in LIR Proto scalar lowering")
	}
	if !f.initBlockLabels() {
		return Function{}, false
	}

	ret, ok := f.lowerType(f.fn.ReturnType)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("unsupported return type %s", mirTypeName(f.fn.ReturnType)))
		ret = VoidType()
	}

	params := f.lowerParams()
	f.lowerLocals()

	blocks := make([]Block, 0, len(f.fn.Blocks))
	for _, bb := range f.orderedBlocks() {
		if bb == nil {
			continue
		}
		f.block = bb
		f.blockLabel = f.labels[bb.ID]
		f.instrs = nil
		if bb.ID == f.fn.Entry {
			f.instrs = append(f.instrs, f.prologue...)
		}
		for _, instr := range bb.Instrs {
			f.lowerInstr(instr)
		}
		term := f.lowerTerm(bb.Term, ret)
		blocks = append(blocks, Block{
			Label:  f.blockLabel,
			Instrs: append([]Instr(nil), f.instrs...),
			Term:   term,
		})
	}
	if f.l.errorCount() != startErrors {
		return Function{}, false
	}

	out := Function{
		Name:   name,
		Return: ret,
		Params: params,
		Blocks: blocks,
	}
	if f.fn.CABI {
		out.CallingConv = "ccc"
	}
	return out, true
}

func (f *mirFunctionLowerer) functionName() string {
	return renderedFunctionName(f.fn)
}

func renderedFunctionName(fn *mir.Function) string {
	if fn == nil {
		return ""
	}
	if fn.ExportSymbol != "" {
		return fn.ExportSymbol
	}
	return fn.Name
}

func (f *mirFunctionLowerer) initBlockLabels() bool {
	if len(f.fn.Blocks) == 0 {
		f.error(DiagUnsupported, "scalar control-flow lowering requires at least one MIR block")
		return false
	}
	f.labels = make(map[mir.BlockID]string, len(f.fn.Blocks))
	seenEntry := false
	ok := true
	for i, bb := range f.fn.Blocks {
		if bb == nil {
			f.error(DiagInvalidInput, fmt.Sprintf("block[%d]: nil MIR block", i))
			ok = false
			continue
		}
		if _, exists := f.labels[bb.ID]; exists {
			f.error(DiagInvalidInput, fmt.Sprintf("duplicate MIR block id %d", bb.ID))
			ok = false
			continue
		}
		label := "entry"
		if bb.ID != f.fn.Entry {
			label = "bb" + strconv.Itoa(int(bb.ID))
		} else {
			seenEntry = true
		}
		f.labels[bb.ID] = label
	}
	if !seenEntry {
		f.error(DiagInvalidInput, fmt.Sprintf("entry block %d is not present", f.fn.Entry))
		ok = false
	}
	return ok
}

func (f *mirFunctionLowerer) orderedBlocks() []*mir.BasicBlock {
	out := make([]*mir.BasicBlock, 0, len(f.fn.Blocks))
	for _, bb := range f.fn.Blocks {
		if bb != nil && bb.ID == f.fn.Entry {
			out = append(out, bb)
			break
		}
	}
	for _, bb := range f.fn.Blocks {
		if bb == nil || bb.ID == f.fn.Entry {
			continue
		}
		out = append(out, bb)
	}
	return out
}

func (f *mirFunctionLowerer) labelForBlock(id mir.BlockID) (string, bool) {
	label, ok := f.labels[id]
	if !ok {
		f.error(DiagInvalidInput, fmt.Sprintf("target block %d is not present", id))
		return "", false
	}
	return label, true
}

func (f *mirFunctionLowerer) lowerParams() []Param {
	params := make([]Param, 0, len(f.fn.Params))
	for _, id := range f.fn.Params {
		loc := f.fn.Local(id)
		if loc == nil {
			f.error(DiagInvalidInput, fmt.Sprintf("param local %d is out of range", id))
			continue
		}
		typ, ok := f.lowerType(loc.Type)
		if !ok || typ.Class == TypeVoid {
			f.error(DiagUnsupported, fmt.Sprintf("unsupported param type %s for local %d", mirTypeName(loc.Type), id))
			continue
		}
		params = append(params, Param{Name: paramName(id), Type: typ})
	}
	return params
}

func (f *mirFunctionLowerer) lowerLocals() {
	for i, loc := range f.fn.Locals {
		if loc == nil {
			f.error(DiagInvalidInput, fmt.Sprintf("local[%d]: nil MIR local", i))
			continue
		}
		typ, ok := f.lowerType(loc.Type)
		if !ok {
			f.error(DiagUnsupported, fmt.Sprintf("unsupported local type %s for local %d", mirTypeName(loc.Type), loc.ID))
			continue
		}
		f.types[loc.ID] = typ
		if typ.Class == TypeVoid {
			continue
		}
		slot := localSlotName(loc.ID)
		f.slots[loc.ID] = slot
		f.prologue = append(f.prologue, Alloca{Dest: slot, Type: typ})
		if loc.IsParam {
			f.prologue = append(f.prologue, Store{
				Value: Operand{Type: typ, Value: paramName(loc.ID)},
				Ptr:   slot,
			})
		}
	}
}

func (f *mirFunctionLowerer) lowerInstr(instr mir.Instr) {
	switch x := instr.(type) {
	case *mir.AssignInstr:
		f.lowerAssign(x)
	case *mir.CallInstr:
		f.lowerCall(x)
	case *mir.IntrinsicInstr:
		f.lowerIntrinsic(x)
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		// Lifetime markers do not affect the current non-GC LIR Proto slices.
	default:
		f.error(DiagUnsupported, fmt.Sprintf("instruction %T is not implemented in LIR Proto scalar lowering", instr))
	}
}

func (f *mirFunctionLowerer) lowerAssign(assign *mir.AssignInstr) {
	if assign == nil {
		f.error(DiagInvalidInput, "nil assign instruction")
		return
	}
	dest := f.fn.Local(assign.Dest.Local)
	if dest == nil {
		f.error(DiagInvalidInput, fmt.Sprintf("assign destination local %d is out of range", assign.Dest.Local))
		return
	}
	destType := f.types[dest.ID]
	if destType.IsZero() {
		var ok bool
		destType, ok = f.lowerType(dest.Type)
		if !ok {
			f.error(DiagUnsupported, fmt.Sprintf("unsupported destination type %s for local %d", mirTypeName(dest.Type), dest.ID))
			return
		}
	}
	if assign.Dest.HasProjections() {
		f.lowerProjectedAssign(assign, dest, destType)
		return
	}
	value, ok := f.lowerRValue(assign.Src, dest.Type, destType)
	if !ok {
		return
	}
	if destType.Class == TypeVoid {
		return
	}
	if value.Type.LLVM != destType.LLVM {
		var coerced bool
		value, coerced = f.coerceOperand(value, rvalueMIRType(assign.Src), dest.Type, destType, "assign")
		if !coerced {
			return
		}
	}
	slot := f.slots[dest.ID]
	if slot == "" {
		f.error(DiagLoweringBug, fmt.Sprintf("local %d has no LIR slot", dest.ID))
		return
	}
	f.instrs = append(f.instrs, Store{Value: value, Ptr: slot})
}

func (f *mirFunctionLowerer) lowerProjectedAssign(assign *mir.AssignInstr, dest *mir.Local, rootType Type) {
	projs := assign.Dest.Projections
	if len(projs) == 0 {
		f.error(DiagLoweringBug, "projected assign called without projections")
		return
	}
	leafMIR := projectionMIRType(projs[len(projs)-1])
	if leafMIR == nil {
		f.error(DiagUnsupported, "projected assign has missing leaf projection type")
		return
	}
	leafType, ok := f.lowerType(leafMIR)
	if !ok || leafType.Class == TypeVoid {
		f.error(DiagUnsupported, fmt.Sprintf("projected assign leaf type %s is not implemented", mirTypeName(leafMIR)))
		return
	}
	value, ok := f.lowerRValue(assign.Src, leafMIR, leafType)
	if !ok {
		return
	}
	f.storeProjectedValue(assign.Dest, dest, rootType, value, rvalueMIRType(assign.Src), "projected assign")
}

func (f *mirFunctionLowerer) storeProjectedValue(place mir.Place, root *mir.Local, rootType Type, value Operand, valueMIR mir.Type, context string) {
	projs := place.Projections
	if len(projs) == 0 {
		f.error(DiagLoweringBug, context+" called without projections")
		return
	}
	if rootType.Class != TypeAggregate {
		f.error(DiagUnsupported, fmt.Sprintf("%s requires aggregate root, got %s", context, rootType))
		return
	}
	leafMIR := projectionMIRType(projs[len(projs)-1])
	if leafMIR == nil {
		f.error(DiagUnsupported, context+" has missing leaf projection type")
		return
	}
	leafType, ok := f.lowerType(leafMIR)
	if !ok || leafType.Class == TypeVoid {
		f.error(DiagUnsupported, fmt.Sprintf("%s leaf type %s is not implemented", context, mirTypeName(leafMIR)))
		return
	}
	if value.Type.LLVM != leafType.LLVM {
		var coerced bool
		value, coerced = f.coerceOperand(value, valueMIR, leafMIR, leafType, context)
		if !coerced {
			return
		}
	}
	slot := f.slots[root.ID]
	if slot == "" {
		f.error(DiagLoweringBug, fmt.Sprintf("local %d has no LIR slot", root.ID))
		return
	}

	base := f.fresh()
	f.instrs = append(f.instrs, Load{Dest: base, Type: rootType, Ptr: slot})

	type frame struct {
		aggregate Operand
		index     int
	}
	frames := make([]frame, 0, len(projs))
	current := Operand{Type: rootType, Value: base}
	for i, proj := range projs {
		if current.Type.Class != TypeAggregate {
			f.error(DiagUnsupported, fmt.Sprintf("projection %T on non-aggregate type %s", proj, current.Type))
			return
		}
		index, ok := projectionIndex(proj)
		if !ok {
			f.error(DiagUnsupported, fmt.Sprintf("projection %T is not implemented in LIR Proto %s", proj, context))
			return
		}
		nextMIR := projectionMIRType(proj)
		if nextMIR == nil {
			f.error(DiagUnsupported, context+" projection has missing type")
			return
		}
		nextType, ok := f.lowerType(nextMIR)
		if !ok || nextType.Class == TypeVoid {
			f.error(DiagUnsupported, fmt.Sprintf("%s projection type %s is not implemented", context, mirTypeName(nextMIR)))
			return
		}
		frames = append(frames, frame{aggregate: current, index: index})
		if i == len(projs)-1 {
			break
		}
		next := f.fresh()
		f.instrs = append(f.instrs, ExtractValue{
			Dest:      next,
			Type:      current.Type,
			Aggregate: current.Value,
			Indices:   []int{index},
		})
		current = Operand{Type: nextType, Value: next}
	}

	rebuilt := value
	for i := len(frames) - 1; i >= 0; i-- {
		fr := frames[i]
		destName := f.fresh()
		f.instrs = append(f.instrs, InsertValue{
			Dest:      destName,
			Type:      fr.aggregate.Type,
			Aggregate: fr.aggregate.Value,
			Value:     rebuilt,
			Indices:   []int{fr.index},
		})
		rebuilt = Operand{Type: fr.aggregate.Type, Value: destName}
	}
	f.instrs = append(f.instrs, Store{Value: rebuilt, Ptr: slot})
}

func (f *mirFunctionLowerer) lowerCall(call *mir.CallInstr) {
	if call == nil {
		f.error(DiagInvalidInput, "nil call instruction")
		return
	}
	fnRef, ok := call.Callee.(*mir.FnRef)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("callee %T is not implemented in LIR Proto direct-call lowering", call.Callee))
		return
	}
	if fnRef.Symbol == "" {
		f.error(DiagInvalidInput, "direct call has empty symbol")
		return
	}
	callee := f.functions[fnRef.Symbol]
	if callee == nil {
		f.error(DiagUnsupported, fmt.Sprintf("direct call to unresolved or external symbol %q is not implemented in this scalar slice", fnRef.Symbol))
		return
	}
	calleeSymbol := renderedFunctionName(callee)
	retType, paramTypes, ok := f.callSignature(fnRef, callee)
	if !ok {
		return
	}
	args, ok := f.lowerCallArgs(call.Args, paramTypes)
	if !ok {
		return
	}

	if retType.Class == TypeVoid {
		f.instrs = append(f.instrs, Call{
			Return: retType,
			Callee: "@" + calleeSymbol,
			Args:   args,
		})
		if call.Dest != nil {
			if call.Dest.HasProjections() {
				f.error(DiagUnsupported, "void call destination projections are not implemented in LIR Proto scalar lowering")
				return
			}
			dest := f.fn.Local(call.Dest.Local)
			if dest == nil {
				f.error(DiagInvalidInput, fmt.Sprintf("void call destination local %d is out of range", call.Dest.Local))
				return
			}
			if !isUnitLikeMIRType(dest.Type) {
				f.error(DiagUnsupported, fmt.Sprintf("void call %q cannot store into non-unit local %d", calleeSymbol, call.Dest.Local))
			}
		}
		return
	}

	destName := ""
	if call.Dest != nil {
		destName = f.fresh()
	}
	f.instrs = append(f.instrs, Call{
		Dest:   destName,
		Return: retType,
		Callee: "@" + calleeSymbol,
		Args:   args,
	})
	if call.Dest == nil {
		return
	}
	f.storeCallResult(*call.Dest, Operand{Type: retType, Value: destName}, calleeSymbol)
}

func (f *mirFunctionLowerer) callSignature(fnRef *mir.FnRef, callee *mir.Function) (Type, []mir.Type, bool) {
	var retMIR mir.Type
	var paramMIR []mir.Type
	if ft, ok := fnRef.Type.(*ir.FnType); ok && ft != nil {
		retMIR = ft.Return
		paramMIR = append(paramMIR, ft.Params...)
	}
	if retMIR == nil {
		retMIR = callee.ReturnType
	}
	if len(paramMIR) == 0 && len(callee.Params) > 0 {
		paramMIR = make([]mir.Type, 0, len(callee.Params))
		for _, id := range callee.Params {
			loc := callee.Local(id)
			if loc == nil {
				f.error(DiagInvalidInput, fmt.Sprintf("callee %q param local %d is out of range", fnRef.Symbol, id))
				return Type{}, nil, false
			}
			paramMIR = append(paramMIR, loc.Type)
		}
	}
	ret, ok := f.lowerType(retMIR)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("direct call %q has unsupported return type %s", fnRef.Symbol, mirTypeName(retMIR)))
		return Type{}, nil, false
	}
	for i, t := range paramMIR {
		paramType, ok := f.lowerType(t)
		if !ok || paramType.Class == TypeVoid {
			f.error(DiagUnsupported, fmt.Sprintf("direct call %q has unsupported param[%d] type %s", fnRef.Symbol, i, mirTypeName(t)))
			return Type{}, nil, false
		}
	}
	return ret, paramMIR, true
}

func (f *mirFunctionLowerer) lowerCallArgs(args []mir.Operand, paramTypes []mir.Type) ([]Operand, bool) {
	if len(args) != len(paramTypes) {
		f.error(DiagInvalidInput, fmt.Sprintf("direct call arg count %d does not match param count %d", len(args), len(paramTypes)))
		return nil, false
	}
	out := make([]Operand, 0, len(args))
	for i, arg := range args {
		paramType, ok := f.lowerType(paramTypes[i])
		if !ok {
			f.error(DiagUnsupported, fmt.Sprintf("direct call param[%d] type %s is not implemented", i, mirTypeName(paramTypes[i])))
			return nil, false
		}
		value, ok := f.lowerOperand(arg, paramTypes[i], paramType)
		if !ok {
			return nil, false
		}
		if value.Type.LLVM != paramType.LLVM {
			var coerced bool
			value, coerced = f.coerceOperand(value, arg.Type(), paramTypes[i], paramType, fmt.Sprintf("direct call arg[%d]", i))
			if !coerced {
				return nil, false
			}
		}
		out = append(out, Operand{Type: paramType, Value: value.Value})
	}
	return out, true
}

func (f *mirFunctionLowerer) storeCallResult(dest mir.Place, result Operand, symbol string) {
	destLoc := f.fn.Local(dest.Local)
	if destLoc == nil {
		f.error(DiagInvalidInput, fmt.Sprintf("call destination local %d is out of range", dest.Local))
		return
	}
	destType, ok := f.lowerType(destLoc.Type)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("call %q destination type %s is not implemented", symbol, mirTypeName(destLoc.Type)))
		return
	}
	if destType.Class == TypeVoid {
		return
	}
	if dest.HasProjections() {
		f.storeProjectedValue(dest, destLoc, destType, result, nil, fmt.Sprintf("call %q result", symbol))
		return
	}
	if destType.LLVM != result.Type.LLVM {
		f.error(DiagUnsupported, fmt.Sprintf("call %q result type %s differs from destination type %s; casts are not implemented in this scalar slice", symbol, result.Type, destType))
		return
	}
	slot := f.slots[dest.Local]
	if slot == "" {
		f.error(DiagLoweringBug, fmt.Sprintf("local %d has no LIR slot", dest.Local))
		return
	}
	f.instrs = append(f.instrs, Store{Value: result, Ptr: slot})
}

func (f *mirFunctionLowerer) lowerIntrinsic(instr *mir.IntrinsicInstr) {
	if instr == nil {
		f.error(DiagInvalidInput, "nil intrinsic instruction")
		return
	}
	switch instr.Kind {
	case mir.IntrinsicPrint, mir.IntrinsicPrintln, mir.IntrinsicEprint, mir.IntrinsicEprintln:
		f.lowerPrintIntrinsic(instr)
	case mir.IntrinsicByteToInt, mir.IntrinsicCharToInt,
		mir.IntrinsicIntToByte, mir.IntrinsicIntToChar,
		mir.IntrinsicByteToChar, mir.IntrinsicCharToByte:
		f.lowerIntegerConversionIntrinsic(instr)
	default:
		f.error(DiagUnsupported, fmt.Sprintf("intrinsic %s is not implemented in LIR Proto scalar lowering", instr.Kind))
	}
}

func (f *mirFunctionLowerer) lowerPrintIntrinsic(instr *mir.IntrinsicInstr) {
	newline, stderr, ok := printIntrinsicFlags(instr.Kind)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("intrinsic %s is not a print-family intrinsic", instr.Kind))
		return
	}
	if len(instr.Args) != 1 {
		f.error(DiagInvalidInput, fmt.Sprintf("%s expects exactly one argument, got %d", instr.Kind, len(instr.Args)))
		return
	}
	if !f.checkVoidIntrinsicDest(instr.Dest, instr.Kind.String()) {
		return
	}
	text, ok := f.lowerPrintTextOperand(instr.Args[0])
	if !ok {
		return
	}
	f.declareRuntime(rtIOWrite, VoidType(), PtrType(), IntType(1), IntType(1))
	f.instrs = append(f.instrs, Call{
		Return: VoidType(),
		Callee: "@" + rtIOWrite,
		Args: []Operand{
			text,
			boolOperand(newline),
			boolOperand(stderr),
		},
	})
}

func (f *mirFunctionLowerer) lowerIntegerConversionIntrinsic(instr *mir.IntrinsicInstr) {
	fromMIR, toMIR, op, ok := integerConversionIntrinsicShape(instr.Kind)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("intrinsic %s is not an integer conversion intrinsic", instr.Kind))
		return
	}
	if len(instr.Args) != 1 {
		f.error(DiagInvalidInput, fmt.Sprintf("%s expects exactly one argument, got %d", instr.Kind, len(instr.Args)))
		return
	}
	if instr.Args[0] == nil {
		f.error(DiagInvalidInput, fmt.Sprintf("%s argument is nil", instr.Kind))
		return
	}
	fromType, ok := f.lowerType(fromMIR)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("%s source type %s is not implemented", instr.Kind, mirTypeName(fromMIR)))
		return
	}
	toType, ok := f.lowerType(toMIR)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("%s result type %s is not implemented", instr.Kind, mirTypeName(toMIR)))
		return
	}
	value, ok := f.lowerOperand(instr.Args[0], fromMIR, fromType)
	if !ok {
		return
	}
	if value.Type.LLVM != fromType.LLVM {
		var coerced bool
		value, coerced = f.coerceOperand(value, instr.Args[0].Type(), fromMIR, fromType, instr.Kind.String()+" argument")
		if !coerced {
			return
		}
	}
	result := f.castOperand(value, op, toType)
	if instr.Dest != nil {
		f.storeIntrinsicResult(*instr.Dest, result, toMIR, instr.Kind.String())
	}
}

func integerConversionIntrinsicShape(kind mir.IntrinsicKind) (from mir.Type, to mir.Type, op string, ok bool) {
	switch kind {
	case mir.IntrinsicByteToInt:
		return mir.TByte, mir.TInt, "zext", true
	case mir.IntrinsicCharToInt:
		return mir.TChar, mir.TInt, "zext", true
	case mir.IntrinsicIntToByte:
		return mir.TInt, mir.TByte, "trunc", true
	case mir.IntrinsicIntToChar:
		return mir.TInt, mir.TChar, "trunc", true
	case mir.IntrinsicByteToChar:
		return mir.TByte, mir.TChar, "zext", true
	case mir.IntrinsicCharToByte:
		return mir.TChar, mir.TByte, "trunc", true
	default:
		return nil, nil, "", false
	}
}

func (f *mirFunctionLowerer) storeIntrinsicResult(dest mir.Place, result Operand, resultMIR mir.Type, label string) {
	destLoc := f.fn.Local(dest.Local)
	if destLoc == nil {
		f.error(DiagInvalidInput, fmt.Sprintf("%s destination local %d is out of range", label, dest.Local))
		return
	}
	destType, ok := f.lowerType(destLoc.Type)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("%s destination type %s is not implemented", label, mirTypeName(destLoc.Type)))
		return
	}
	if destType.Class == TypeVoid {
		return
	}
	if dest.HasProjections() {
		f.storeProjectedValue(dest, destLoc, destType, result, resultMIR, label+" result")
		return
	}
	if destType.LLVM != result.Type.LLVM {
		var coerced bool
		result, coerced = f.coerceOperand(result, resultMIR, destLoc.Type, destType, label+" result")
		if !coerced {
			return
		}
	}
	slot := f.slots[dest.Local]
	if slot == "" {
		f.error(DiagLoweringBug, fmt.Sprintf("local %d has no LIR slot", dest.Local))
		return
	}
	f.instrs = append(f.instrs, Store{Value: result, Ptr: slot})
}

func printIntrinsicFlags(kind mir.IntrinsicKind) (newline bool, stderr bool, ok bool) {
	switch kind {
	case mir.IntrinsicPrint:
		return false, false, true
	case mir.IntrinsicPrintln:
		return true, false, true
	case mir.IntrinsicEprint:
		return false, true, true
	case mir.IntrinsicEprintln:
		return true, true, true
	default:
		return false, false, false
	}
}

func (f *mirFunctionLowerer) checkVoidIntrinsicDest(dest *mir.Place, label string) bool {
	if dest == nil {
		return true
	}
	if dest.HasProjections() {
		f.error(DiagUnsupported, fmt.Sprintf("%s destination projections are not implemented in LIR Proto scalar lowering", label))
		return false
	}
	loc := f.fn.Local(dest.Local)
	if loc == nil {
		f.error(DiagInvalidInput, fmt.Sprintf("%s destination local %d is out of range", label, dest.Local))
		return false
	}
	if !isUnitLikeMIRType(loc.Type) {
		f.error(DiagUnsupported, fmt.Sprintf("%s cannot store into non-unit local %d", label, dest.Local))
		return false
	}
	return true
}

func (f *mirFunctionLowerer) lowerPrintTextOperand(op mir.Operand) (Operand, bool) {
	if op == nil {
		f.error(DiagInvalidInput, "print argument is nil")
		return Operand{}, false
	}
	argMIR := nonPoisonMIRType(op.Type())
	argType, ok := f.lowerType(argMIR)
	if !ok || argType.Class == TypeVoid {
		f.error(DiagUnsupported, fmt.Sprintf("print argument type %s is not implemented in LIR Proto scalar lowering", mirTypeName(argMIR)))
		return Operand{}, false
	}
	value, ok := f.lowerOperand(op, argMIR, argType)
	if !ok {
		return Operand{}, false
	}
	prim, ok := argMIR.(*ir.PrimType)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("print argument type %s is not primitive", mirTypeName(argMIR)))
		return Operand{}, false
	}
	switch prim.Kind {
	case ir.PrimString:
		if value.Type.Class != TypePtr {
			f.error(DiagUnsupported, fmt.Sprintf("string print argument lowered to non-pointer type %s", value.Type))
			return Operand{}, false
		}
		return Operand{Type: PtrType(), Value: value.Value}, true
	case ir.PrimBool:
		return f.callStringRuntime(rtBoolToString, []Type{IntType(1)}, []Operand{value}), true
	case ir.PrimChar:
		return f.callStringRuntime(rtCharToString, []Type{IntType(32)}, []Operand{value}), true
	case ir.PrimByte:
		return f.callStringRuntime(rtByteToString, []Type{IntType(8)}, []Operand{value}), true
	case ir.PrimFloat, ir.PrimFloat64:
		return f.callStringRuntime(rtFloatToString, []Type{FloatType("double")}, []Operand{value}), true
	case ir.PrimFloat32:
		wide := f.castOperand(value, "fpext", FloatType("double"))
		return f.callStringRuntime(rtFloatToString, []Type{FloatType("double")}, []Operand{wide}), true
	case ir.PrimInt, ir.PrimInt8, ir.PrimInt16, ir.PrimInt32, ir.PrimInt64,
		ir.PrimUInt8, ir.PrimUInt16, ir.PrimUInt32, ir.PrimUInt64:
		wide := f.printIntegerOperand(value, prim.Kind)
		return f.callStringRuntime(rtIntToString, []Type{IntType(64)}, []Operand{wide}), true
	default:
		f.error(DiagUnsupported, fmt.Sprintf("print argument type %s is not implemented in LIR Proto scalar lowering", mirTypeName(argMIR)))
		return Operand{}, false
	}
}

func (f *mirFunctionLowerer) printIntegerOperand(value Operand, kind ir.PrimKind) Operand {
	if value.Type.LLVM == "i64" {
		return Operand{Type: IntType(64), Value: value.Value}
	}
	op := "sext"
	switch kind {
	case ir.PrimUInt8, ir.PrimUInt16, ir.PrimUInt32:
		op = "zext"
	}
	return f.castOperand(value, op, IntType(64))
}

func (f *mirFunctionLowerer) coerceOperand(value Operand, fromMIR, toMIR mir.Type, to Type, context string) (Operand, bool) {
	if value.Type.LLVM == to.LLVM {
		return Operand{Type: to, Value: value.Value}, true
	}
	switch {
	case value.Type.Class == TypeInt && to.Class == TypeInt:
		return f.coerceIntOperand(value, fromMIR, to, context)
	case value.Type.Class == TypeFloat && to.Class == TypeFloat:
		return f.coerceFloatOperand(value, to, context)
	default:
		f.error(DiagUnsupported, fmt.Sprintf("%s type coercion from %s to %s is not implemented", context, value.Type, to))
		return Operand{}, false
	}
}

func (f *mirFunctionLowerer) coerceIntOperand(value Operand, fromMIR mir.Type, to Type, context string) (Operand, bool) {
	if value.Type.Class != TypeInt || to.Class != TypeInt {
		f.error(DiagUnsupported, fmt.Sprintf("%s requires integer types, got %s -> %s", context, value.Type, to))
		return Operand{}, false
	}
	if value.Type.LLVM == to.LLVM {
		return Operand{Type: to, Value: value.Value}, true
	}
	fromBits := intTypeBits(value.Type)
	toBits := intTypeBits(to)
	if fromBits == 0 || toBits == 0 {
		f.error(DiagUnsupported, fmt.Sprintf("%s cannot resize integer types %s -> %s", context, value.Type, to))
		return Operand{}, false
	}
	if fromBits == toBits {
		return Operand{Type: to, Value: value.Value}, true
	}
	op := "trunc"
	if toBits > fromBits {
		op = "sext"
		if isUnsignedMIRType(fromMIR) {
			op = "zext"
		}
	}
	return f.castOperand(value, op, to), true
}

func (f *mirFunctionLowerer) coerceFloatOperand(value Operand, to Type, context string) (Operand, bool) {
	if value.Type.Class != TypeFloat || to.Class != TypeFloat {
		f.error(DiagUnsupported, fmt.Sprintf("%s requires float types, got %s -> %s", context, value.Type, to))
		return Operand{}, false
	}
	if value.Type.LLVM == to.LLVM {
		return Operand{Type: to, Value: value.Value}, true
	}
	op := ""
	if value.Type.LLVM == "float" && to.LLVM == "double" {
		op = "fpext"
	} else if value.Type.LLVM == "double" && to.LLVM == "float" {
		op = "fptrunc"
	}
	if op == "" {
		f.error(DiagUnsupported, fmt.Sprintf("%s cannot resize float types %s -> %s", context, value.Type, to))
		return Operand{}, false
	}
	return f.castOperand(value, op, to), true
}

func (f *mirFunctionLowerer) castOperand(value Operand, op string, to Type) Operand {
	dest := f.fresh()
	f.instrs = append(f.instrs, Cast{
		Dest: dest,
		Op:   op,
		From: value,
		To:   to,
	})
	return Operand{Type: to, Value: dest}
}

func (f *mirFunctionLowerer) callStringRuntime(symbol string, params []Type, args []Operand) Operand {
	f.declareRuntime(symbol, PtrType(), params...)
	dest := f.fresh()
	f.instrs = append(f.instrs, Call{
		Dest:   dest,
		Return: PtrType(),
		Callee: "@" + symbol,
		Args:   args,
	})
	return Operand{Type: PtrType(), Value: dest}
}

func (f *mirFunctionLowerer) declareRuntime(symbol string, ret Type, params ...Type) {
	f.out.RuntimeDecls.Declare(RuntimeDecl{
		Symbol: symbol,
		Return: ret,
		Params: append([]Type(nil), params...),
	})
}

func (f *mirFunctionLowerer) lowerTerm(term mir.Terminator, ret Type) Term {
	switch x := term.(type) {
	case *mir.ReturnTerm:
		if ret.Class == TypeVoid {
			return Ret{}
		}
		value, ok := f.loadLocal(f.fn.ReturnLocal)
		if !ok {
			return Unreachable{}
		}
		return Ret{Type: value.Type, Value: value.Value}
	case *mir.GotoTerm:
		target, ok := f.labelForBlock(x.Target)
		if !ok {
			return Unreachable{}
		}
		return Br{Target: target}
	case *mir.BranchTerm:
		cond, ok := f.lowerOperand(x.Cond, mir.TBool, IntType(1))
		if !ok {
			return Unreachable{}
		}
		if cond.Type.LLVM != "i1" {
			f.error(DiagUnsupported, fmt.Sprintf("branch condition requires i1, got %s", cond.Type))
			return Unreachable{}
		}
		thenLabel, thenOK := f.labelForBlock(x.Then)
		elseLabel, elseOK := f.labelForBlock(x.Else)
		if !thenOK || !elseOK {
			return Unreachable{}
		}
		return CondBr{Cond: cond.Value, Then: thenLabel, Else: elseLabel}
	case *mir.SwitchIntTerm:
		return f.lowerSwitchTerm(x)
	case *mir.UnreachableTerm:
		return Unreachable{}
	case nil:
		f.error(DiagInvalidInput, "missing MIR terminator")
	default:
		f.error(DiagUnsupported, fmt.Sprintf("terminator %T is not implemented in LIR Proto scalar lowering", term))
	}
	return Unreachable{}
}

func (f *mirFunctionLowerer) lowerSwitchTerm(term *mir.SwitchIntTerm) Term {
	if term == nil || term.Scrutinee == nil {
		f.error(DiagInvalidInput, "switch terminator has nil scrutinee")
		return Unreachable{}
	}
	scrutinee, ok := f.lowerOperand(term.Scrutinee, term.Scrutinee.Type(), Type{})
	if !ok {
		return Unreachable{}
	}
	if scrutinee.Type.Class != TypeInt {
		f.error(DiagUnsupported, fmt.Sprintf("switch scrutinee requires integer type, got %s", scrutinee.Type))
		return Unreachable{}
	}
	defaultLabel, ok := f.labelForBlock(term.Default)
	if !ok {
		return Unreachable{}
	}
	cases := make([]SwitchCase, 0, len(term.Cases))
	for _, c := range term.Cases {
		label, ok := f.labelForBlock(c.Target)
		if !ok {
			return Unreachable{}
		}
		cases = append(cases, SwitchCase{
			Value: strconv.FormatInt(c.Value, 10),
			Label: label,
		})
	}
	return Switch{
		Type:      scrutinee.Type,
		Scrutinee: scrutinee.Value,
		Default:   defaultLabel,
		Cases:     cases,
	}
}

func (f *mirFunctionLowerer) lowerRValue(rv mir.RValue, hintMIR mir.Type, hint Type) (Operand, bool) {
	switch x := rv.(type) {
	case *mir.UseRV:
		return f.lowerOperand(x.Op, hintMIR, hint)
	case *mir.UnaryRV:
		return f.lowerUnary(x, hintMIR, hint)
	case *mir.BinaryRV:
		return f.lowerBinary(x, hintMIR, hint)
	case *mir.CastRV:
		return f.lowerCast(x, hintMIR, hint)
	case *mir.AggregateRV:
		return f.lowerAggregate(x, hintMIR, hint)
	default:
		f.error(DiagUnsupported, fmt.Sprintf("rvalue %T is not implemented in LIR Proto scalar lowering", rv))
		return Operand{}, false
	}
}

func (f *mirFunctionLowerer) lowerAggregate(rv *mir.AggregateRV, hintMIR mir.Type, hint Type) (Operand, bool) {
	if rv == nil {
		f.error(DiagInvalidInput, "nil aggregate rvalue")
		return Operand{}, false
	}
	switch rv.Kind {
	case mir.AggTuple:
		return f.lowerTupleAggregate(rv, hintMIR, hint)
	case mir.AggStruct:
		return f.lowerStructAggregate(rv, hintMIR, hint)
	default:
		f.error(DiagUnsupported, fmt.Sprintf("aggregate kind %s is not implemented in LIR Proto Phase-3 aggregate slice", rv.Kind))
		return Operand{}, false
	}
}

func (f *mirFunctionLowerer) lowerTupleAggregate(rv *mir.AggregateRV, hintMIR mir.Type, hint Type) (Operand, bool) {
	tupleMIR, ok := firstMIRType(rv.T, hintMIR).(*ir.TupleType)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("tuple aggregate has non-tuple type %s", mirTypeName(firstMIRType(rv.T, hintMIR))))
		return Operand{}, false
	}
	if len(rv.Fields) != len(tupleMIR.Elems) {
		f.error(DiagInvalidInput, fmt.Sprintf("tuple aggregate field count %d does not match type field count %d", len(rv.Fields), len(tupleMIR.Elems)))
		return Operand{}, false
	}
	tupleType, ok := f.lowerType(tupleMIR)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("tuple aggregate type %s is not implemented", mirTypeName(tupleMIR)))
		return Operand{}, false
	}
	current := Operand{Type: tupleType, Value: "undef"}
	for i, field := range rv.Fields {
		elemMIR := tupleMIR.Elems[i]
		elemType, ok := f.lowerType(elemMIR)
		if !ok || elemType.Class == TypeVoid {
			f.error(DiagUnsupported, fmt.Sprintf("tuple field[%d] type %s is not implemented", i, mirTypeName(elemMIR)))
			return Operand{}, false
		}
		value, ok := f.lowerOperand(field, elemMIR, elemType)
		if !ok {
			return Operand{}, false
		}
		if value.Type.LLVM != elemType.LLVM {
			var coerced bool
			value, coerced = f.coerceOperand(value, field.Type(), elemMIR, elemType, fmt.Sprintf("tuple field[%d]", i))
			if !coerced {
				return Operand{}, false
			}
		}
		dest := f.fresh()
		f.instrs = append(f.instrs, InsertValue{
			Dest:      dest,
			Type:      tupleType,
			Aggregate: current.Value,
			Value:     value,
			Indices:   []int{i},
		})
		current = Operand{Type: tupleType, Value: dest}
	}
	return current, true
}

func (f *mirFunctionLowerer) lowerStructAggregate(rv *mir.AggregateRV, hintMIR mir.Type, hint Type) (Operand, bool) {
	named, ok := firstMIRType(rv.T, hintMIR).(*ir.NamedType)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("struct aggregate has non-named type %s", mirTypeName(firstMIRType(rv.T, hintMIR))))
		return Operand{}, false
	}
	layout, layoutName, ok := f.structLayout(named)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("struct aggregate type %s has no MIR layout", mirTypeName(named)))
		return Operand{}, false
	}
	if len(rv.Fields) != len(layout.Fields) {
		f.error(DiagInvalidInput, fmt.Sprintf("struct aggregate field count %d does not match layout field count %d for %s", len(rv.Fields), len(layout.Fields), layoutName))
		return Operand{}, false
	}
	structType, ok := f.lowerType(named)
	if !ok {
		f.error(DiagUnsupported, fmt.Sprintf("struct aggregate type %s is not implemented", mirTypeName(named)))
		return Operand{}, false
	}
	current := Operand{Type: structType, Value: "undef"}
	for i, field := range rv.Fields {
		fieldLayout := layout.Fields[i]
		fieldType, ok := f.lowerType(fieldLayout.Type)
		if !ok || fieldType.Class == TypeVoid {
			f.error(DiagUnsupported, fmt.Sprintf("struct %s field[%d] type %s is not implemented", layoutName, i, mirTypeName(fieldLayout.Type)))
			return Operand{}, false
		}
		value, ok := f.lowerOperand(field, fieldLayout.Type, fieldType)
		if !ok {
			return Operand{}, false
		}
		if value.Type.LLVM != fieldType.LLVM {
			var coerced bool
			value, coerced = f.coerceOperand(value, field.Type(), fieldLayout.Type, fieldType, fmt.Sprintf("struct %s field[%d]", layoutName, i))
			if !coerced {
				return Operand{}, false
			}
		}
		dest := f.fresh()
		f.instrs = append(f.instrs, InsertValue{
			Dest:      dest,
			Type:      structType,
			Aggregate: current.Value,
			Value:     value,
			Indices:   []int{fieldLayout.Index},
		})
		current = Operand{Type: structType, Value: dest}
	}
	return current, true
}

func (f *mirFunctionLowerer) lowerUnary(rv *mir.UnaryRV, hintMIR mir.Type, hint Type) (Operand, bool) {
	if rv == nil {
		f.error(DiagInvalidInput, "nil unary rvalue")
		return Operand{}, false
	}
	arg, ok := f.lowerOperand(rv.Arg, firstMIRType(rv.T, hintMIR), hint)
	if !ok {
		return Operand{}, false
	}
	if rv.Op == mir.UnPlus {
		return arg, true
	}
	resType := arg.Type
	if rv.T != nil {
		if lowered, ok := f.lowerType(rv.T); ok {
			resType = lowered
		}
	}
	dest := f.fresh()
	switch rv.Op {
	case mir.UnNeg:
		switch resType.Class {
		case TypeInt:
			f.instrs = append(f.instrs, Binary{Dest: dest, Op: "sub", Type: resType, Left: zeroValue(resType), Right: arg.Value})
		case TypeFloat:
			f.instrs = append(f.instrs, Unary{Dest: dest, Op: "fneg", Type: resType, Value: arg.Value})
		default:
			f.error(DiagUnsupported, fmt.Sprintf("unary - for %s is not implemented", resType))
			return Operand{}, false
		}
	case mir.UnNot:
		if resType.LLVM != "i1" {
			f.error(DiagUnsupported, fmt.Sprintf("unary ! requires i1, got %s", resType))
			return Operand{}, false
		}
		f.instrs = append(f.instrs, Binary{Dest: dest, Op: "xor", Type: resType, Left: arg.Value, Right: "1"})
	case mir.UnBitNot:
		if resType.Class != TypeInt {
			f.error(DiagUnsupported, fmt.Sprintf("unary ~ requires integer type, got %s", resType))
			return Operand{}, false
		}
		f.instrs = append(f.instrs, Binary{Dest: dest, Op: "xor", Type: resType, Left: arg.Value, Right: "-1"})
	default:
		f.error(DiagUnsupported, fmt.Sprintf("unary op %d is not implemented", rv.Op))
		return Operand{}, false
	}
	return Operand{Type: resType, Value: dest}, true
}

func (f *mirFunctionLowerer) lowerCast(rv *mir.CastRV, hintMIR mir.Type, hint Type) (Operand, bool) {
	if rv == nil {
		f.error(DiagInvalidInput, "nil cast rvalue")
		return Operand{}, false
	}
	if rv.Arg == nil {
		f.error(DiagInvalidInput, "cast rvalue has nil arg")
		return Operand{}, false
	}
	fromMIR := firstMIRType(nonPoisonMIRType(rv.From), nonPoisonMIRType(rv.Arg.Type()))
	toMIR := firstMIRType(nonPoisonMIRType(rv.To), hintMIR)
	fromType, ok := f.lowerType(fromMIR)
	if !ok || fromType.Class == TypeVoid {
		f.error(DiagUnsupported, fmt.Sprintf("cast source type %s is not implemented", mirTypeName(fromMIR)))
		return Operand{}, false
	}
	toType, ok := f.lowerType(toMIR)
	if !ok || toType.Class == TypeVoid {
		f.error(DiagUnsupported, fmt.Sprintf("cast target type %s is not implemented", mirTypeName(toMIR)))
		return Operand{}, false
	}
	value, ok := f.lowerOperand(rv.Arg, fromMIR, fromType)
	if !ok {
		return Operand{}, false
	}
	if value.Type.LLVM != fromType.LLVM {
		value, ok = f.coerceOperand(value, rv.Arg.Type(), fromMIR, fromType, "cast source")
		if !ok {
			return Operand{}, false
		}
	}

	switch rv.Kind {
	case mir.CastIntResize:
		return f.coerceIntOperand(value, fromMIR, toType, "int resize cast")
	case mir.CastIntToFloat:
		if value.Type.Class != TypeInt || toType.Class != TypeFloat {
			f.error(DiagUnsupported, fmt.Sprintf("int-to-float cast requires int -> float, got %s -> %s", value.Type, toType))
			return Operand{}, false
		}
		op := "sitofp"
		if isUnsignedMIRType(fromMIR) {
			op = "uitofp"
		}
		return f.castOperand(value, op, toType), true
	case mir.CastFloatToInt:
		if value.Type.Class != TypeFloat || toType.Class != TypeInt {
			f.error(DiagUnsupported, fmt.Sprintf("float-to-int cast requires float -> int, got %s -> %s", value.Type, toType))
			return Operand{}, false
		}
		op := "fptosi"
		if isUnsignedMIRType(toMIR) {
			op = "fptoui"
		}
		return f.castOperand(value, op, toType), true
	case mir.CastFloatResize:
		return f.coerceFloatOperand(value, toType, "float resize cast")
	case mir.CastBitcast:
		if value.Type.LLVM == toType.LLVM {
			return Operand{Type: toType, Value: value.Value}, true
		}
		return f.castOperand(value, "bitcast", toType), true
	case mir.CastOptionalWrap, mir.CastOptionalUnwrap:
		if value.Type.LLVM == toType.LLVM {
			return Operand{Type: toType, Value: value.Value}, true
		}
		f.error(DiagUnsupported, fmt.Sprintf("optional cast %s from %s to %s is not implemented in LIR Proto scalar lowering", rv.Kind, value.Type, toType))
		return Operand{}, false
	default:
		f.error(DiagUnsupported, fmt.Sprintf("cast kind %s is not implemented in LIR Proto scalar lowering", rv.Kind))
		return Operand{}, false
	}
}

func (f *mirFunctionLowerer) lowerBinary(rv *mir.BinaryRV, hintMIR mir.Type, hint Type) (Operand, bool) {
	if rv == nil {
		f.error(DiagInvalidInput, "nil binary rvalue")
		return Operand{}, false
	}
	argMIR := firstMIRType(nonPoisonMIRType(rv.Left.Type()), nonPoisonMIRType(rv.Right.Type()), hintMIR)
	argType, ok := f.lowerType(argMIR)
	if !ok || argType.Class == TypeVoid {
		f.error(DiagUnsupported, fmt.Sprintf("binary operand type %s is not implemented", mirTypeName(argMIR)))
		return Operand{}, false
	}
	left, ok := f.lowerOperand(rv.Left, argMIR, argType)
	if !ok {
		return Operand{}, false
	}
	right, ok := f.lowerOperand(rv.Right, argMIR, argType)
	if !ok {
		return Operand{}, false
	}
	if left.Type.LLVM != right.Type.LLVM {
		f.error(DiagUnsupported, fmt.Sprintf("binary operand type mismatch %s vs %s", left.Type, right.Type))
		return Operand{}, false
	}

	op, resType, ok := f.binaryOpcode(rv.Op, argMIR, left.Type, rv.T, hint)
	if !ok {
		return Operand{}, false
	}
	if !isComparison(rv.Op) && resType.LLVM != left.Type.LLVM {
		f.error(DiagUnsupported, fmt.Sprintf("binary result type %s differs from operand type %s; casts are not implemented in this scalar slice", resType, left.Type))
		return Operand{}, false
	}
	dest := f.fresh()
	f.instrs = append(f.instrs, Binary{Dest: dest, Op: op, Type: left.Type, Left: left.Value, Right: right.Value})
	return Operand{Type: resType, Value: dest}, true
}

func (f *mirFunctionLowerer) lowerOperand(op mir.Operand, hintMIR mir.Type, hint Type) (Operand, bool) {
	switch x := op.(type) {
	case *mir.CopyOp:
		return f.loadPlace(x.Place)
	case *mir.MoveOp:
		return f.loadPlace(x.Place)
	case *mir.ConstOp:
		return f.lowerConst(x.Const, firstMIRType(x.Type(), hintMIR), hint)
	default:
		f.error(DiagUnsupported, fmt.Sprintf("operand %T is not implemented in LIR Proto scalar lowering", op))
		return Operand{}, false
	}
}

func (f *mirFunctionLowerer) loadPlace(place mir.Place) (Operand, bool) {
	value, ok := f.loadLocal(place.Local)
	if !ok || !place.HasProjections() {
		return value, ok
	}
	return f.projectOperand(value, place.Projections)
}

func (f *mirFunctionLowerer) loadLocal(id mir.LocalID) (Operand, bool) {
	typ := f.types[id]
	if typ.IsZero() {
		loc := f.fn.Local(id)
		if loc == nil {
			f.error(DiagInvalidInput, fmt.Sprintf("local %d is out of range", id))
			return Operand{}, false
		}
		var ok bool
		typ, ok = f.lowerType(loc.Type)
		if !ok {
			f.error(DiagUnsupported, fmt.Sprintf("unsupported local type %s for local %d", mirTypeName(loc.Type), id))
			return Operand{}, false
		}
	}
	if typ.Class == TypeVoid {
		return Operand{Type: typ}, true
	}
	slot := f.slots[id]
	if slot == "" {
		f.error(DiagLoweringBug, fmt.Sprintf("local %d has no LIR slot", id))
		return Operand{}, false
	}
	dest := f.fresh()
	f.instrs = append(f.instrs, Load{Dest: dest, Type: typ, Ptr: slot})
	return Operand{Type: typ, Value: dest}, true
}

func (f *mirFunctionLowerer) projectOperand(value Operand, projections []mir.Projection) (Operand, bool) {
	current := value
	for _, proj := range projections {
		switch x := proj.(type) {
		case *mir.FieldProj:
			if current.Type.Class != TypeAggregate {
				f.error(DiagUnsupported, fmt.Sprintf("field projection on non-aggregate type %s", current.Type))
				return Operand{}, false
			}
			fieldType, ok := f.lowerType(x.Type)
			if !ok || fieldType.Class == TypeVoid {
				f.error(DiagUnsupported, fmt.Sprintf("field projection type %s is not implemented", mirTypeName(x.Type)))
				return Operand{}, false
			}
			dest := f.fresh()
			f.instrs = append(f.instrs, ExtractValue{
				Dest:      dest,
				Type:      current.Type,
				Aggregate: current.Value,
				Indices:   []int{x.Index},
			})
			current = Operand{Type: fieldType, Value: dest}
		case *mir.TupleProj:
			if current.Type.Class != TypeAggregate {
				f.error(DiagUnsupported, fmt.Sprintf("tuple projection on non-aggregate type %s", current.Type))
				return Operand{}, false
			}
			elemType, ok := f.lowerType(x.Type)
			if !ok || elemType.Class == TypeVoid {
				f.error(DiagUnsupported, fmt.Sprintf("tuple projection type %s is not implemented", mirTypeName(x.Type)))
				return Operand{}, false
			}
			dest := f.fresh()
			f.instrs = append(f.instrs, ExtractValue{
				Dest:      dest,
				Type:      current.Type,
				Aggregate: current.Value,
				Indices:   []int{x.Index},
			})
			current = Operand{Type: elemType, Value: dest}
		default:
			f.error(DiagUnsupported, fmt.Sprintf("projection %T is not implemented in LIR Proto Phase-3 aggregate slice", proj))
			return Operand{}, false
		}
	}
	return current, true
}

func projectionIndex(proj mir.Projection) (int, bool) {
	switch x := proj.(type) {
	case *mir.FieldProj:
		return x.Index, true
	case *mir.TupleProj:
		return x.Index, true
	default:
		return 0, false
	}
}

func projectionMIRType(proj mir.Projection) mir.Type {
	switch x := proj.(type) {
	case *mir.FieldProj:
		return x.Type
	case *mir.TupleProj:
		return x.Type
	default:
		return nil
	}
}

func (f *mirFunctionLowerer) lowerConst(c mir.Const, hintMIR mir.Type, hint Type) (Operand, bool) {
	switch x := c.(type) {
	case *mir.IntConst:
		typ, ok := f.lowerType(firstMIRType(x.Type(), hintMIR))
		if !ok {
			f.error(DiagUnsupported, fmt.Sprintf("integer const type %s is not implemented", mirTypeName(firstMIRType(x.Type(), hintMIR))))
			return Operand{}, false
		}
		return Operand{Type: typ, Value: strconv.FormatInt(x.Value, 10)}, true
	case *mir.BoolConst:
		return Operand{Type: IntType(1), Value: boolLiteral(x.Value)}, true
	case *mir.FloatConst:
		typ, ok := f.lowerType(firstMIRType(x.Type(), hintMIR))
		if !ok {
			f.error(DiagUnsupported, fmt.Sprintf("float const type %s is not implemented", mirTypeName(firstMIRType(x.Type(), hintMIR))))
			return Operand{}, false
		}
		return Operand{Type: typ, Value: strconv.FormatFloat(x.Value, 'g', -1, 64)}, true
	case *mir.StringConst:
		typ := PtrType()
		if hint.Class == TypePtr {
			typ = hint
		}
		return Operand{Type: typ, Value: f.out.StringPool.Intern(x.Value)}, true
	case *mir.CharConst:
		return Operand{Type: IntType(32), Value: strconv.Itoa(int(x.Value))}, true
	case *mir.ByteConst:
		return Operand{Type: IntType(8), Value: strconv.Itoa(int(x.Value))}, true
	case *mir.UnitConst:
		return Operand{Type: VoidType()}, true
	case *mir.NullConst:
		typ, ok := f.lowerType(firstMIRType(x.Type(), hintMIR))
		if !ok {
			if hint.Class == TypePtr {
				typ = hint
				ok = true
			}
		}
		if !ok || typ.Class != TypePtr {
			f.error(DiagUnsupported, fmt.Sprintf("null const type %s is not implemented", mirTypeName(firstMIRType(x.Type(), hintMIR))))
			return Operand{}, false
		}
		return Operand{Type: typ, Value: "null"}, true
	case *mir.FnConst:
		if x.Symbol == "" {
			f.error(DiagInvalidInput, "FnConst has empty symbol")
			return Operand{}, false
		}
		return Operand{Type: PtrType(), Value: "@" + x.Symbol}, true
	default:
		f.error(DiagUnsupported, fmt.Sprintf("const %T is not implemented in LIR Proto scalar lowering", c))
		return Operand{}, false
	}
}

func (f *mirFunctionLowerer) binaryOpcode(op mir.BinaryOp, argMIR mir.Type, arg Type, resultMIR mir.Type, hint Type) (string, Type, bool) {
	isFloat := arg.Class == TypeFloat
	isUnsigned := isUnsignedMIRType(argMIR)
	resType := arg
	if isComparison(op) {
		resType = IntType(1)
	} else if resultMIR != nil {
		if lowered, ok := f.lowerType(resultMIR); ok && lowered.Class != TypeVoid {
			resType = lowered
		}
	} else if !hint.IsZero() && hint.Class != TypeVoid {
		resType = hint
	}

	switch op {
	case mir.BinAdd:
		if arg.Class != TypeInt && arg.Class != TypeFloat {
			f.error(DiagUnsupported, fmt.Sprintf("binary + requires numeric operands, got %s", arg))
			return "", Type{}, false
		}
		if isFloat {
			return "fadd", resType, true
		}
		return "add", resType, true
	case mir.BinSub:
		if arg.Class != TypeInt && arg.Class != TypeFloat {
			f.error(DiagUnsupported, fmt.Sprintf("binary - requires numeric operands, got %s", arg))
			return "", Type{}, false
		}
		if isFloat {
			return "fsub", resType, true
		}
		return "sub", resType, true
	case mir.BinMul:
		if arg.Class != TypeInt && arg.Class != TypeFloat {
			f.error(DiagUnsupported, fmt.Sprintf("binary * requires numeric operands, got %s", arg))
			return "", Type{}, false
		}
		if isFloat {
			return "fmul", resType, true
		}
		return "mul", resType, true
	case mir.BinDiv:
		if arg.Class != TypeInt && arg.Class != TypeFloat {
			f.error(DiagUnsupported, fmt.Sprintf("binary / requires numeric operands, got %s", arg))
			return "", Type{}, false
		}
		if isFloat {
			return "fdiv", resType, true
		}
		if isUnsigned {
			return "udiv", resType, true
		}
		return "sdiv", resType, true
	case mir.BinMod:
		if arg.Class != TypeInt && arg.Class != TypeFloat {
			f.error(DiagUnsupported, fmt.Sprintf("binary %% requires numeric operands, got %s", arg))
			return "", Type{}, false
		}
		if isFloat {
			return "frem", resType, true
		}
		if isUnsigned {
			return "urem", resType, true
		}
		return "srem", resType, true
	case mir.BinEq, mir.BinNeq:
		if arg.Class == TypePtr && !isRawPtrMIRType(argMIR) {
			f.error(DiagUnsupported, "pointer/string equality needs runtime semantics and is not implemented in this scalar slice")
			return "", Type{}, false
		}
		pred := "eq"
		if op == mir.BinNeq {
			pred = "ne"
		}
		if isFloat {
			if op == mir.BinEq {
				return "fcmp oeq", resType, true
			}
			return "fcmp one", resType, true
		}
		return "icmp " + pred, resType, true
	case mir.BinLt, mir.BinLeq, mir.BinGt, mir.BinGeq:
		if arg.Class == TypePtr {
			f.error(DiagUnsupported, "pointer/string ordering needs runtime semantics and is not implemented in this scalar slice")
			return "", Type{}, false
		}
		return compareOpcode(op, isFloat, isUnsigned), resType, true
	case mir.BinAnd, mir.BinOr:
		if arg.LLVM != "i1" {
			f.error(DiagUnsupported, fmt.Sprintf("logical op requires i1 operands, got %s", arg))
			return "", Type{}, false
		}
		if op == mir.BinAnd {
			return "and", resType, true
		}
		return "or", resType, true
	case mir.BinBitAnd, mir.BinBitOr, mir.BinBitXor:
		if arg.Class != TypeInt {
			f.error(DiagUnsupported, fmt.Sprintf("bitwise op requires integer operands, got %s", arg))
			return "", Type{}, false
		}
		switch op {
		case mir.BinBitAnd:
			return "and", resType, true
		case mir.BinBitOr:
			return "or", resType, true
		default:
			return "xor", resType, true
		}
	case mir.BinShl, mir.BinShr:
		if arg.Class != TypeInt {
			f.error(DiagUnsupported, fmt.Sprintf("shift op requires integer operands, got %s", arg))
			return "", Type{}, false
		}
		if op == mir.BinShl {
			return "shl", resType, true
		}
		if isUnsigned {
			return "lshr", resType, true
		}
		return "ashr", resType, true
	default:
		f.error(DiagUnsupported, fmt.Sprintf("binary op %d is not implemented", op))
		return "", Type{}, false
	}
}

func compareOpcode(op mir.BinaryOp, isFloat, isUnsigned bool) string {
	if isFloat {
		switch op {
		case mir.BinLt:
			return "fcmp olt"
		case mir.BinLeq:
			return "fcmp ole"
		case mir.BinGt:
			return "fcmp ogt"
		case mir.BinGeq:
			return "fcmp oge"
		}
	}
	prefix := "s"
	if isUnsigned {
		prefix = "u"
	}
	switch op {
	case mir.BinLt:
		return "icmp " + prefix + "lt"
	case mir.BinLeq:
		return "icmp " + prefix + "le"
	case mir.BinGt:
		return "icmp " + prefix + "gt"
	default:
		return "icmp " + prefix + "ge"
	}
}

func (f *mirFunctionLowerer) lowerType(t mir.Type) (Type, bool) {
	switch x := t.(type) {
	case *ir.PrimType:
		switch x.Kind {
		case ir.PrimInt, ir.PrimInt64, ir.PrimUInt64:
			return IntType(64), true
		case ir.PrimInt32, ir.PrimUInt32, ir.PrimChar:
			return IntType(32), true
		case ir.PrimInt16, ir.PrimUInt16:
			return IntType(16), true
		case ir.PrimInt8, ir.PrimUInt8, ir.PrimByte:
			return IntType(8), true
		case ir.PrimBool:
			return IntType(1), true
		case ir.PrimFloat, ir.PrimFloat64:
			return FloatType("double"), true
		case ir.PrimFloat32:
			return FloatType("float"), true
		case ir.PrimString, ir.PrimBytes, ir.PrimRawPtr:
			return PtrType(), true
		case ir.PrimUnit, ir.PrimNever:
			return VoidType(), true
		default:
			return Type{}, false
		}
	case *ir.TupleType:
		return f.lowerTupleType(x)
	case *ir.NamedType:
		return f.lowerNamedType(x)
	default:
		return Type{}, false
	}
}

func (f *mirFunctionLowerer) lowerTupleType(t *ir.TupleType) (Type, bool) {
	if t == nil {
		return Type{}, false
	}
	name, ok := f.tupleTypeName(t)
	if !ok {
		return Type{}, false
	}
	parts := make([]string, 0, len(t.Elems))
	for _, elem := range t.Elems {
		elemType, ok := f.lowerType(elem)
		if !ok || elemType.Class == TypeVoid {
			return Type{}, false
		}
		parts = append(parts, elemType.String())
	}
	f.declareTypeDef(name, "{ "+strings.Join(parts, ", ")+" }")
	return AggregateType(name), true
}

func (f *mirFunctionLowerer) lowerNamedType(t *ir.NamedType) (Type, bool) {
	layout, name, ok := f.structLayout(t)
	if !ok {
		return Type{}, false
	}
	parts := make([]string, 0, len(layout.Fields))
	for _, field := range layout.Fields {
		fieldType, ok := f.lowerType(field.Type)
		if !ok || fieldType.Class == TypeVoid {
			return Type{}, false
		}
		parts = append(parts, fieldType.String())
	}
	typeName := "%" + name
	f.declareTypeDef(typeName, "{ "+strings.Join(parts, ", ")+" }")
	return AggregateType(typeName), true
}

func (f *mirFunctionLowerer) structLayout(t *ir.NamedType) (*mir.StructLayout, string, bool) {
	if t == nil || f.layouts == nil || f.layouts.Structs == nil {
		return nil, "", false
	}
	key := namedTypeLayoutKey(t)
	layout := f.layouts.Structs[key]
	if layout == nil {
		return nil, key, false
	}
	return layout, key, true
}

func namedTypeLayoutKey(t *ir.NamedType) string {
	if t == nil {
		return ""
	}
	if t.Package != "" && !t.Builtin {
		return strings.TrimPrefix(t.Package, "std.") + "." + t.Name
	}
	return t.Name
}

func (f *mirFunctionLowerer) tupleTypeName(t *ir.TupleType) (string, bool) {
	if t == nil {
		return "", false
	}
	tags := make([]string, 0, len(t.Elems))
	for _, elem := range t.Elems {
		tag, ok := f.tupleTag(elem)
		if !ok {
			return "", false
		}
		tags = append(tags, tag)
	}
	return "%Tuple." + strings.Join(tags, "."), true
}

func (f *mirFunctionLowerer) tupleTag(t mir.Type) (string, bool) {
	switch x := t.(type) {
	case *ir.PrimType:
		switch x.Kind {
		case ir.PrimInt, ir.PrimInt64, ir.PrimUInt64:
			return "i64", true
		case ir.PrimInt32, ir.PrimUInt32, ir.PrimChar:
			return "i32", true
		case ir.PrimInt16, ir.PrimUInt16:
			return "i16", true
		case ir.PrimInt8, ir.PrimUInt8, ir.PrimByte:
			return "i8", true
		case ir.PrimBool:
			return "i1", true
		case ir.PrimFloat, ir.PrimFloat64:
			return "f64", true
		case ir.PrimFloat32:
			return "f32", true
		case ir.PrimString:
			return "string", true
		case ir.PrimBytes:
			return "bytes", true
		case ir.PrimUnit:
			return "unit", true
		default:
			return "", false
		}
	case *ir.TupleType:
		name, ok := f.tupleTypeName(x)
		return strings.TrimPrefix(name, "%"), ok
	case *ir.NamedType:
		_, name, ok := f.structLayout(x)
		return name, ok
	default:
		return "", false
	}
}

func (f *mirFunctionLowerer) declareTypeDef(name, body string) {
	if name == "" || body == "" {
		return
	}
	for _, existing := range f.out.TypeDefs {
		if existing.Name != name {
			continue
		}
		if existing.Body != body {
			f.error(DiagLoweringBug, fmt.Sprintf("type definition %s body mismatch: %q vs %q", name, existing.Body, body))
		}
		return
	}
	f.out.TypeDefs = append(f.out.TypeDefs, TypeDef{Name: name, Body: body})
}

func (f *mirFunctionLowerer) fresh() string {
	name := "%t" + strconv.Itoa(f.temp)
	f.temp++
	return name
}

func (f *mirFunctionLowerer) error(kind DiagnosticKind, msg string) {
	fn := ""
	if f.fn != nil {
		fn = f.functionName()
	}
	f.l.error(kind, msg, fn, f.blockLabel)
}

func localSlotName(id mir.LocalID) string {
	return "%l" + strconv.Itoa(int(id))
}

func paramName(id mir.LocalID) string {
	return "%p" + strconv.Itoa(int(id))
}

func boolLiteral(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func boolOperand(v bool) Operand {
	value := "false"
	if v {
		value = "true"
	}
	return Operand{Type: IntType(1), Value: value}
}

func zeroValue(t Type) string {
	switch t.Class {
	case TypeFloat:
		return "0.0"
	case TypePtr:
		return "null"
	default:
		return "0"
	}
}

func isComparison(op mir.BinaryOp) bool {
	switch op {
	case mir.BinEq, mir.BinNeq, mir.BinLt, mir.BinLeq, mir.BinGt, mir.BinGeq:
		return true
	default:
		return false
	}
}

func firstMIRType(types ...mir.Type) mir.Type {
	for _, t := range types {
		if t != nil {
			return t
		}
	}
	return nil
}

func rvalueMIRType(rv mir.RValue) mir.Type {
	switch x := rv.(type) {
	case *mir.UseRV:
		if x.Op != nil {
			return x.Op.Type()
		}
	case *mir.UnaryRV:
		return x.T
	case *mir.BinaryRV:
		return x.T
	case *mir.CastRV:
		return x.To
	case *mir.AggregateRV:
		return x.T
	}
	return nil
}

func nonPoisonMIRType(t mir.Type) mir.Type {
	if _, ok := t.(*ir.ErrType); ok {
		return nil
	}
	return t
}

func intTypeBits(t Type) int {
	if !isIntLLVM(t.LLVM) {
		return 0
	}
	bits, err := strconv.Atoi(t.LLVM[1:])
	if err != nil {
		return 0
	}
	return bits
}

func isUnsignedMIRType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	if !ok {
		return false
	}
	switch p.Kind {
	case ir.PrimUInt8, ir.PrimUInt16, ir.PrimUInt32, ir.PrimUInt64, ir.PrimByte, ir.PrimChar:
		return true
	default:
		return false
	}
}

func isRawPtrMIRType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	return ok && p.Kind == ir.PrimRawPtr
}

func isUnitLikeMIRType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	if !ok {
		return false
	}
	return p.Kind == ir.PrimUnit || p.Kind == ir.PrimNever
}

func mirTypeName(t mir.Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}
