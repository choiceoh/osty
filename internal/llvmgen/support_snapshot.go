// support_snapshot.go snapshots the Osty-authored LLVM helper surface into
// the native backend package so the bridge file can stay deleted while
// toolchain sources remain the long-term owner.

package llvmgen

import (
	"fmt"
	"math"
	llvmStrings "strings"
)

type ostyStringer interface {
	toString() string
}

func ostyToString(v any) string {
	if s, ok := v.(ostyStringer); ok {
		return s.toString()
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}

// Osty: toolchain/llvmgen.osty:11:5
type LlvmValue struct {
	typ     string
	name    string
	pointer bool
}

// Osty: toolchain/llvmgen.osty:17:5
type LlvmParam struct {
	name string
	typ  string
}

// Osty: toolchain/llvmgen.osty:22:5
type LlvmBinding struct {
	name  string
	value *LlvmValue
}

// Osty: toolchain/llvmgen.osty:27:5
type LlvmLookup struct {
	found bool
	value *LlvmValue
}

// Osty: toolchain/llvmgen.osty:32:5
type LlvmStringGlobal struct {
	name    string
	encoded string
	byteLen int
}

// Osty: toolchain/llvmgen.osty:38:5
type LlvmStructField struct {
	typ string
}

// Osty: toolchain/llvmgen.osty:42:5
type LlvmCString struct {
	encoded string
	byteLen int
}

// Osty: toolchain/llvmgen.osty:47:5
type LlvmEmitter struct {
	temp           int
	label          int
	stringId       int
	body           []string
	locals         []*LlvmBinding
	stringGlobals  []*LlvmStringGlobal
	nativeListData map[string]*LlvmValue
	nativeListLens map[string]*LlvmValue
}

// Osty: toolchain/llvmgen.osty:56:5
type LlvmIfLabels struct {
	thenLabel string
	elseLabel string
	endLabel  string
}

// Osty: toolchain/llvmgen.osty:62:5
type LlvmRangeLoop struct {
	condLabel string
	bodyLabel string
	endLabel  string
	iterPtr   string
	current   string
}

// Osty: toolchain/llvmgen.osty:70:5
type LlvmSmokeExecutableCase struct {
	name    string
	fixture string
	stdout  string
}

// Osty: toolchain/llvmgen.osty:76:5
type LlvmUnsupportedDiagnostic struct {
	code    string
	kind    string
	message string
	hint    string
}

// Osty: toolchain/llvmgen.osty:83:5
func llvmEmitter() *LlvmEmitter {
	return &LlvmEmitter{
		temp:           0,
		label:          0,
		stringId:       0,
		body:           make([]string, 0, 1),
		locals:         make([]*LlvmBinding, 0, 1),
		stringGlobals:  make([]*LlvmStringGlobal, 0, 1),
		nativeListData: map[string]*LlvmValue{},
		nativeListLens: map[string]*LlvmValue{},
	}
}

// Osty: toolchain/llvmgen.osty:94:5
func llvmI64(name string) *LlvmValue {
	return &LlvmValue{typ: "i64", name: name, pointer: false}
}

// Osty: toolchain/llvmgen.osty:98:5
func llvmI1(name string) *LlvmValue {
	return &LlvmValue{typ: "i1", name: name, pointer: false}
}

// Osty: toolchain/llvmgen.osty:102:5
func llvmF64(name string) *LlvmValue {
	return &LlvmValue{typ: "double", name: name, pointer: false}
}

// Osty: toolchain/llvmgen.osty:106:5
func llvmIntLiteral(value int) *LlvmValue {
	return llvmI64(fmt.Sprintf("%s", ostyToString(value)))
}

// Osty: toolchain/llvmgen.osty:110:5
func llvmFloatLiteral(value string) *LlvmValue {
	return llvmF64(fmt.Sprintf("%s", ostyToString(value)))
}

// Osty: toolchain/llvmgen.osty:114:5
func llvmEnumVariant(enumName string, tag int) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:115:5
	_ = enumName
	return llvmI64(fmt.Sprintf("%s", ostyToString(tag)))
}

// Osty: toolchain/llvmgen.osty:119:5
func llvmEnumPayloadVariant(emitter *LlvmEmitter, typ string, tag int, payload *LlvmValue) *LlvmValue {
	return llvmStructLiteral(emitter, typ, []*LlvmValue{llvmEnumVariant(typ, tag), payload})
}

// Osty: toolchain/llvmgen.osty:135:5
func llvmEnumBoxedPayloadVariant(emitter *LlvmEmitter, enumTyp string, tag int, payload *LlvmValue, site string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:142:5
	symbol := func() string {
		if payload.typ == "ptr" {
			return "osty.rt.enum_alloc_ptr_v1"
		} else {
			return "osty.rt.enum_alloc_scalar_v1"
		}
	}()
	_ = symbol
	// Osty: toolchain/llvmgen.osty:147:5
	heapPtr := llvmCall(emitter, "ptr", symbol, []*LlvmValue{llvmStringLiteral(emitter, site)})
	_ = heapPtr
	// Osty: toolchain/llvmgen.osty:153:5
	llvmStore(emitter, heapPtr, payload)
	return llvmStructLiteral(emitter, enumTyp, []*LlvmValue{llvmEnumVariant(enumTyp, tag), heapPtr})
}

// Osty: toolchain/llvmgen.osty:160:5
func llvmEnumBoxedBareVariant(emitter *LlvmEmitter, enumTyp string, tag int) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:165:5
	nullPtr := &LlvmValue{typ: "ptr", name: "null", pointer: false}
	_ = nullPtr
	return llvmStructLiteral(emitter, enumTyp, []*LlvmValue{llvmEnumVariant(enumTyp, tag), nullPtr})
}

// Osty: toolchain/llvmgen.osty:169:5
func llvmParam(name string, typ string) *LlvmParam {
	return &LlvmParam{name: name, typ: typ}
}

// Osty: toolchain/llvmgen.osty:173:5
func llvmBind(emitter *LlvmEmitter, name string, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:174:5
	func() struct{} {
		emitter.locals = append(emitter.locals, &LlvmBinding{name: name, value: value})
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:177:5
func llvmLookup(emitter *LlvmEmitter, name string) *LlvmLookup {
	// Osty: toolchain/llvmgen.osty:178:5
	out := &LlvmLookup{found: false, value: llvmI64("0")}
	_ = out
	// Osty: toolchain/llvmgen.osty:179:5
	for _, binding := range emitter.locals {
		// Osty: toolchain/llvmgen.osty:180:9
		if binding.name == name {
			// Osty: toolchain/llvmgen.osty:181:13
			out = &LlvmLookup{found: true, value: binding.value}
		}
	}
	return out
}

// Osty: toolchain/llvmgen.osty:187:5
func llvmIdent(emitter *LlvmEmitter, name string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:188:5
	lookup := llvmLookup(emitter, name)
	_ = lookup
	// Osty: toolchain/llvmgen.osty:189:5
	if !(lookup.found) {
		// Osty: toolchain/llvmgen.osty:190:9
		return llvmI64("0")
	}
	// Osty: toolchain/llvmgen.osty:192:5
	if lookup.value.pointer {
		// Osty: toolchain/llvmgen.osty:193:9
		return llvmLoad(emitter, lookup.value)
	}
	return lookup.value
}

// Osty: toolchain/llvmgen.osty:198:5
func llvmLoad(emitter *LlvmEmitter, slot *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:199:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:200:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", ostyToString(tmp), ostyToString(slot.typ), ostyToString(slot.name)))
		return struct{}{}
	}()
	return &LlvmValue{typ: slot.typ, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:204:5
func llvmMutableLetSlot(emitter *LlvmEmitter, name string, initial *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:209:5
	ptr := llvmNextTemp(emitter)
	_ = ptr
	// Osty: toolchain/llvmgen.osty:210:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", ostyToString(ptr), ostyToString(initial.typ)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:211:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", ostyToString(initial.typ), ostyToString(initial.name), ostyToString(ptr)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:212:5
	slot := &LlvmValue{typ: initial.typ, name: ptr, pointer: true}
	_ = slot
	// Osty: toolchain/llvmgen.osty:213:5
	llvmBind(emitter, name, slot)
	return slot
}

// Osty: toolchain/llvmgen.osty:217:5
func llvmMutableLet(emitter *LlvmEmitter, name string, initial *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:218:5
	_slot := llvmMutableLetSlot(emitter, name, initial)
	_ = _slot
}

// Osty: toolchain/llvmgen.osty:221:5
func llvmStore(emitter *LlvmEmitter, slot *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:222:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", ostyToString(value.typ), ostyToString(value.name), ostyToString(slot.name)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:230:5
func llvmSlotAsPtr(slot *LlvmValue) *LlvmValue {
	return &LlvmValue{typ: "ptr", name: slot.name, pointer: false}
}

// Osty: toolchain/llvmgen.osty:238:5
func llvmAllocaSlot(emitter *LlvmEmitter, llvmType string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:239:5
	slot := llvmNextTemp(emitter)
	_ = slot
	// Osty: toolchain/llvmgen.osty:240:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", ostyToString(slot), ostyToString(llvmType)))
		return struct{}{}
	}()
	return &LlvmValue{typ: "ptr", name: slot, pointer: false}
}

// Osty: toolchain/llvmgen.osty:248:5
func llvmSpillToSlot(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:249:5
	slot := llvmAllocaSlot(emitter, value.typ)
	_ = slot
	// Osty: toolchain/llvmgen.osty:250:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", ostyToString(value.typ), ostyToString(value.name), ostyToString(slot.name)))
		return struct{}{}
	}()
	return slot
}

// Osty: toolchain/llvmgen.osty:258:5
func llvmLoadFromSlot(emitter *LlvmEmitter, slot *LlvmValue, llvmType string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:263:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:264:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", ostyToString(tmp), ostyToString(llvmType), ostyToString(slot.name)))
		return struct{}{}
	}()
	return &LlvmValue{typ: llvmType, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:273:5
func llvmSizeOf(emitter *LlvmEmitter, llvmType string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:274:5
	gep := llvmNextTemp(emitter)
	_ = gep
	// Osty: toolchain/llvmgen.osty:275:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr %s, ptr null, i32 1", ostyToString(gep), ostyToString(llvmType)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:276:5
	size := llvmNextTemp(emitter)
	_ = size
	// Osty: toolchain/llvmgen.osty:277:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = ptrtoint ptr %s to i64", ostyToString(size), ostyToString(gep)))
		return struct{}{}
	}()
	return &LlvmValue{typ: "i64", name: size, pointer: false}
}

// Osty: toolchain/llvmgen.osty:281:5
func llvmAssign(emitter *LlvmEmitter, name string, value *LlvmValue) bool {
	// Osty: toolchain/llvmgen.osty:282:5
	lookup := llvmLookup(emitter, name)
	_ = lookup
	// Osty: toolchain/llvmgen.osty:283:5
	if !(lookup.found) || !(lookup.value.pointer) || lookup.value.typ != value.typ {
		// Osty: toolchain/llvmgen.osty:284:9
		return false
	}
	// Osty: toolchain/llvmgen.osty:286:5
	llvmStore(emitter, lookup.value, value)
	return true
}

// Osty: toolchain/llvmgen.osty:290:5
func llvmImmutableLet(emitter *LlvmEmitter, name string, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:291:5
	llvmBind(emitter, name, value)
}

// Osty: toolchain/llvmgen.osty:298:5
func llvmBinaryI64(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:304:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:305:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = %s i64 %s, %s", ostyToString(tmp), ostyToString(op), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI64(tmp)
}

// Osty: toolchain/llvmgen.osty:309:5
func llvmBinaryF64(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:315:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:316:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = %s double %s, %s", ostyToString(tmp), ostyToString(op), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmF64(tmp)
}

// Osty: toolchain/llvmgen.osty:320:5
func llvmCompare(emitter *LlvmEmitter, pred string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:326:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:327:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = icmp %s %s %s, %s", ostyToString(tmp), ostyToString(pred), ostyToString(left.typ), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: toolchain/llvmgen.osty:331:5
func llvmCompareF64(emitter *LlvmEmitter, pred string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:337:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:338:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = fcmp %s double %s, %s", ostyToString(tmp), ostyToString(pred), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: toolchain/llvmgen.osty:342:5
func llvmNotI1(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:343:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:344:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = xor i1 %s, true", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: toolchain/llvmgen.osty:348:5
func llvmLogicalI1(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:354:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:355:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = %s i1 %s, %s", ostyToString(tmp), ostyToString(op), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: toolchain/llvmgen.osty:359:5
func llvmCall(emitter *LlvmEmitter, ret string, name string, args []*LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:365:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:366:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s @%s(%s)", ostyToString(tmp), ostyToString(ret), ostyToString(name), ostyToString(llvmCallArgs(args))))
		return struct{}{}
	}()
	return &LlvmValue{typ: ret, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:370:5
func llvmCallVoid(emitter *LlvmEmitter, name string, args []*LlvmValue) {
	// Osty: toolchain/llvmgen.osty:371:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void @%s(%s)", ostyToString(name), ostyToString(llvmCallArgs(args))))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:374:5
func llvmGcRuntimeDeclarations() []string {
	return []string{"declare ptr @osty.gc.alloc_v1(i64, i64, ptr)", "declare void @osty.gc.pre_write_v1(ptr, ptr, i64)", "declare void @osty.gc.post_write_v1(ptr, ptr, i64)", "declare ptr @osty.gc.load_v1(ptr)", "declare void @osty.gc.root_bind_v1(ptr)", "declare void @osty.gc.root_release_v1(ptr)", "declare ptr @osty.rt.enum_alloc_ptr_v1(ptr)", "declare ptr @osty.rt.enum_alloc_scalar_v1(ptr)"}
}

// Osty: toolchain/llvmgen.osty:387:5
func llvmGcAlloc(emitter *LlvmEmitter, objectKind int, byteSize int, site string) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty.gc.alloc_v1", []*LlvmValue{llvmIntLiteral(objectKind), llvmIntLiteral(byteSize), llvmStringLiteral(emitter, site)})
}

// Osty: toolchain/llvmgen.osty:405:5
func llvmGcPostWrite(emitter *LlvmEmitter, owner *LlvmValue, value *LlvmValue, slotKind int) {
	// Osty: toolchain/llvmgen.osty:411:5
	llvmCallVoid(emitter, "osty.gc.post_write_v1", []*LlvmValue{owner, value, llvmIntLiteral(slotKind)})
}

// Osty: toolchain/llvmgen.osty:422:5
func llvmGcPreWrite(emitter *LlvmEmitter, owner *LlvmValue, value *LlvmValue, slotKind int) {
	// Osty: toolchain/llvmgen.osty:428:5
	llvmCallVoid(emitter, "osty.gc.pre_write_v1", []*LlvmValue{owner, value, llvmIntLiteral(slotKind)})
}

// Osty: toolchain/llvmgen.osty:439:5
func llvmGcLoad(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty.gc.load_v1", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:443:5
func llvmGcRootBind(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:444:5
	llvmCallVoid(emitter, "osty.gc.root_bind_v1", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:447:5
func llvmGcRootRelease(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:448:5
	llvmCallVoid(emitter, "osty.gc.root_release_v1", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:460:5
func llvmSafepointKindUnspecified() int {
	return 0
}

// Osty: toolchain/llvmgen.osty:462:5
func llvmSafepointKindEntry() int {
	return 1
}

// Osty: toolchain/llvmgen.osty:464:5
func llvmSafepointKindCall() int {
	return 2
}

// Osty: toolchain/llvmgen.osty:466:5
func llvmSafepointKindLoop() int {
	return 3
}

// Osty: toolchain/llvmgen.osty:468:5
func llvmSafepointKindAlloc() int {
	return 4
}

// Osty: toolchain/llvmgen.osty:470:5
func llvmSafepointKindYield() int {
	return 5
}

// Osty: toolchain/llvmgen.osty:477:5
func llvmEncodeSafepointId(kind int, serial int) int {
	// Osty: toolchain/llvmgen.osty:478:5
	mask := func() int {
		var _p1 int = (1 << 56)
		var _rhs2 int = 1
		if _rhs2 < 0 && _p1 > math.MaxInt+_rhs2 {
			panic("integer overflow")
		}
		if _rhs2 > 0 && _p1 < math.MinInt+_rhs2 {
			panic("integer overflow")
		}
		return _p1 - _rhs2
	}()
	_ = mask
	return (kind << 56) | (serial & mask)
}

// Osty: toolchain/llvmgen.osty:492:5
func llvmSafepointDefaultRootChunkSize() int {
	return 4096
}

// Osty: toolchain/llvmgen.osty:497:5
type LlvmSafepointChunk struct {
	start int
	end   int
	id    int
}

// Osty: toolchain/llvmgen.osty:510:5
func llvmPlanSafepointChunks(kind int, firstSerial int, rootCount int, chunkSize int) []*LlvmSafepointChunk {
	// Osty: toolchain/llvmgen.osty:516:5
	var chunks []*LlvmSafepointChunk = make([]*LlvmSafepointChunk, 0, 1)
	_ = chunks
	// Osty: toolchain/llvmgen.osty:517:5
	if rootCount <= 0 {
		// Osty: toolchain/llvmgen.osty:518:9
		return chunks
	}
	// Osty: toolchain/llvmgen.osty:520:5
	size := func() int {
		if chunkSize <= 0 {
			return rootCount
		} else {
			return chunkSize
		}
	}()
	_ = size
	// Osty: toolchain/llvmgen.osty:521:5
	serial := firstSerial
	_ = serial
	// Osty: toolchain/llvmgen.osty:522:5
	for start := 0; start < rootCount; start++ {
		// Osty: toolchain/llvmgen.osty:523:9
		if func() int {
			var _p3 int = start
			var _rhs4 int = size
			if _rhs4 == 0 {
				panic("integer modulo by zero")
			}
			if _p3 == math.MinInt && _rhs4 == int(-1) {
				panic("integer overflow")
			}
			return _p3 % _rhs4
		}() != 0 {
			// Osty: toolchain/llvmgen.osty:524:13
			continue
		}
		// Osty: toolchain/llvmgen.osty:526:9
		end := func() int {
			var _p5 int = start
			var _rhs6 int = size
			if _rhs6 > 0 && _p5 > math.MaxInt-_rhs6 {
				panic("integer overflow")
			}
			if _rhs6 < 0 && _p5 < math.MinInt-_rhs6 {
				panic("integer overflow")
			}
			return _p5 + _rhs6
		}()
		_ = end
		// Osty: toolchain/llvmgen.osty:527:9
		if end > rootCount {
			// Osty: toolchain/llvmgen.osty:528:13
			end = rootCount
		}
		// Osty: toolchain/llvmgen.osty:530:9
		func() struct{} {
			chunks = append(chunks, &LlvmSafepointChunk{start: start, end: end, id: llvmEncodeSafepointId(kind, serial)})
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:537:9
		func() {
			var _cur7 int = serial
			var _rhs8 int = 1
			if _rhs8 > 0 && _cur7 > math.MaxInt-_rhs8 {
				panic("integer overflow")
			}
			if _rhs8 < 0 && _cur7 < math.MinInt-_rhs8 {
				panic("integer overflow")
			}
			serial = _cur7 + _rhs8
		}()
	}
	return chunks
}

// Osty: toolchain/llvmgen.osty:551:5
func llvmClosureEnvGcKind() int {
	return 1029
}

// Osty: toolchain/llvmgen.osty:558:5
func llvmClosureEnvPhase1CaptureCount() int {
	return 0
}

// Osty: toolchain/llvmgen.osty:569:5
func llvmEmitClosureEnvAllocRuntime(emitter *LlvmEmitter, captureCount int, siteName string, thunkSymbol string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:575:5
	envTemp := llvmNextTemp(emitter)
	_ = envTemp
	// Osty: toolchain/llvmgen.osty:576:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call ptr @osty.rt.closure_env_alloc_v1(i64 %s, ptr %s)", ostyToString(envTemp), ostyToString(captureCount), ostyToString(siteName)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:579:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store ptr @%s, ptr %s", ostyToString(thunkSymbol), ostyToString(envTemp)))
		return struct{}{}
	}()
	return &LlvmValue{typ: "ptr", name: envTemp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:587:5
func llvmRenderSafepointEmpty(id int) string {
	return fmt.Sprintf("  call void @osty.gc.safepoint_v1(i64 %s, ptr null, i64 0)", ostyToString(id))
}

// Osty: toolchain/llvmgen.osty:594:5
func llvmEmitSafepointEmpty(emitter *LlvmEmitter, id int) {
	// Osty: toolchain/llvmgen.osty:595:5
	func() struct{} { emitter.body = append(emitter.body, llvmRenderSafepointEmpty(id)); return struct{}{} }()
}

// Osty: toolchain/llvmgen.osty:604:5
func llvmEmitSafepointWithRoots(emitter *LlvmEmitter, id int, rootAddresses []string) {
	// Osty: toolchain/llvmgen.osty:609:5
	count := len(rootAddresses)
	_ = count
	// Osty: toolchain/llvmgen.osty:610:5
	slotsPtr := llvmNextTemp(emitter)
	_ = slotsPtr
	// Osty: toolchain/llvmgen.osty:611:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca ptr, i64 %s", ostyToString(slotsPtr), ostyToString(count)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:612:5
	for i := 0; i < count; i++ {
		// Osty: toolchain/llvmgen.osty:613:9
		slotPtr := llvmNextTemp(emitter)
		_ = slotPtr
		// Osty: toolchain/llvmgen.osty:614:9
		addr := rootAddresses[i]
		_ = addr
		// Osty: toolchain/llvmgen.osty:615:9
		func() struct{} {
			emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr ptr, ptr %s, i64 %s", ostyToString(slotPtr), ostyToString(slotsPtr), ostyToString(i)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:616:9
		func() struct{} {
			emitter.body = append(emitter.body, fmt.Sprintf("  store ptr %s, ptr %s", ostyToString(addr), ostyToString(slotPtr)))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:618:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void @osty.gc.safepoint_v1(i64 %s, ptr %s, i64 %s)", ostyToString(id), ostyToString(slotsPtr), ostyToString(count)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:623:5
func llvmStructTypeDef(name string, fieldTypes []string) string {
	// Osty: toolchain/llvmgen.osty:624:5
	fields := llvmStrings.Join(fieldTypes, ", ")
	_ = fields
	return fmt.Sprintf("%%%s = type { %s }", ostyToString(name), ostyToString(fields))
}

// Osty: toolchain/llvmgen.osty:628:5
func llvmStructLiteral(emitter *LlvmEmitter, typ string, fields []*LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:633:5
	current := "undef"
	_ = current
	// Osty: toolchain/llvmgen.osty:634:5
	fieldIndex := 0
	_ = fieldIndex
	// Osty: toolchain/llvmgen.osty:635:5
	for _, field := range fields {
		// Osty: toolchain/llvmgen.osty:636:9
		tmp := llvmNextTemp(emitter)
		_ = tmp
		// Osty: toolchain/llvmgen.osty:637:9
		func() struct{} {
			emitter.body = append(emitter.body, fmt.Sprintf("  %s = insertvalue %s %s, %s %s, %s", ostyToString(tmp), ostyToString(typ), ostyToString(current), ostyToString(field.typ), ostyToString(field.name), ostyToString(fieldIndex)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:640:9
		current = tmp
		// Osty: toolchain/llvmgen.osty:641:9
		func() {
			var _cur9 int = fieldIndex
			var _rhs10 int = 1
			if _rhs10 > 0 && _cur9 > math.MaxInt-_rhs10 {
				panic("integer overflow")
			}
			if _rhs10 < 0 && _cur9 < math.MinInt-_rhs10 {
				panic("integer overflow")
			}
			fieldIndex = _cur9 + _rhs10
		}()
	}
	return &LlvmValue{typ: typ, name: current, pointer: false}
}

// Osty: toolchain/llvmgen.osty:646:5
func llvmExtractValue(emitter *LlvmEmitter, aggregate *LlvmValue, fieldType string, fieldIndex int) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:652:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:653:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = extractvalue %s %s, %s", ostyToString(tmp), ostyToString(aggregate.typ), ostyToString(aggregate.name), ostyToString(fieldIndex)))
		return struct{}{}
	}()
	return &LlvmValue{typ: fieldType, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:657:5
func llvmInsertValue(emitter *LlvmEmitter, aggregate *LlvmValue, field *LlvmValue, fieldIndex int) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:663:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:664:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = insertvalue %s %s, %s %s, %s", ostyToString(tmp), ostyToString(aggregate.typ), ostyToString(aggregate.name), ostyToString(field.typ), ostyToString(field.name), ostyToString(fieldIndex)))
		return struct{}{}
	}()
	return &LlvmValue{typ: aggregate.typ, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:670:5
func llvmPrintlnI64(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:671:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:672:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_i64, i64 %s)", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:675:5
func llvmPrintlnF64(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:676:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:677:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_f64, double %s)", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:680:5
func llvmPrintlnBool(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:681:5
	text := llvmNextTemp(emitter)
	_ = text
	// Osty: toolchain/llvmgen.osty:682:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = select i1 %s, ptr @.bool_true, ptr @.bool_false", ostyToString(text), ostyToString(value.name)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:683:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:684:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_str, ptr %s)", ostyToString(tmp), ostyToString(text)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:687:5
func llvmStringLiteral(emitter *LlvmEmitter, text string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:688:5
	name := fmt.Sprintf("@.str%s", ostyToString(emitter.stringId))
	_ = name
	// Osty: toolchain/llvmgen.osty:689:12
	emitter.stringId = func() int {
		var _p11 int = emitter.stringId
		var _rhs12 int = 1
		if _rhs12 > 0 && _p11 > math.MaxInt-_rhs12 {
			panic("integer overflow")
		}
		if _rhs12 < 0 && _p11 < math.MinInt-_rhs12 {
			panic("integer overflow")
		}
		return _p11 + _rhs12
	}()
	// Osty: toolchain/llvmgen.osty:690:5
	cstring := llvmCString(text)
	_ = cstring
	// Osty: toolchain/llvmgen.osty:691:5
	func() struct{} {
		emitter.stringGlobals = append(emitter.stringGlobals, &LlvmStringGlobal{name: name, encoded: cstring.encoded, byteLen: cstring.byteLen})
		return struct{}{}
	}()
	return &LlvmValue{typ: "ptr", name: name, pointer: false}
}

// Osty: toolchain/llvmgen.osty:697:5
func llvmCString(text string) *LlvmCString {
	// Osty: toolchain/llvmgen.osty:702:5
	encoded := fmt.Sprintf("%s\\00", ostyToString(llvmCStringEscape(text)))
	_ = encoded
	// Osty: toolchain/llvmgen.osty:703:5
	byteLen := func() int {
		var _p13 int = len([]byte(text))
		var _rhs14 int = 1
		if _rhs14 > 0 && _p13 > math.MaxInt-_rhs14 {
			panic("integer overflow")
		}
		if _rhs14 < 0 && _p13 < math.MinInt-_rhs14 {
			panic("integer overflow")
		}
		return _p13 + _rhs14
	}()
	_ = byteLen
	return &LlvmCString{encoded: encoded, byteLen: byteLen}
}

// Osty: toolchain/llvmgen.osty:722:5
func llvmCStringEscape(text string) string {
	var b llvmStrings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '"':
			b.WriteString("\\22")
		case c == '\\':
			b.WriteString("\\5C")
		case c >= 0x20 && c <= 0x7E:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\%02X", c)
		}
	}
	return b.String()
}

// Osty: toolchain/llvmgen.osty:731:1
func llvmHexByte(n int) string {
	// Osty: toolchain/llvmgen.osty:732:5
	hi := func() int {
		var _p15 int = (n / 16)
		var _rhs16 int = 16
		if _rhs16 == 0 {
			panic("integer modulo by zero")
		}
		if _p15 == math.MinInt && _rhs16 == int(-1) {
			panic("integer overflow")
		}
		return _p15 % _rhs16
	}()
	_ = hi
	// Osty: toolchain/llvmgen.osty:733:5
	lo := func() int {
		var _p17 int = n
		var _rhs18 int = 16
		if _rhs18 == 0 {
			panic("integer modulo by zero")
		}
		if _p17 == math.MinInt && _rhs18 == int(-1) {
			panic("integer overflow")
		}
		return _p17 % _rhs18
	}()
	_ = lo
	return fmt.Sprintf("%s%s", ostyToString(llvmHexDigit(hi)), ostyToString(llvmHexDigit(lo)))
}

// Osty: toolchain/llvmgen.osty:737:1
func llvmHexDigit(n int) string {
	return func() string {
		if n < 10 {
			return fmt.Sprintf("%s", ostyToString(n))
		} else if n == 10 {
			return "A"
		} else if n == 11 {
			return "B"
		} else if n == 12 {
			return "C"
		} else if n == 13 {
			return "D"
		} else if n == 14 {
			return "E"
		} else {
			return "F"
		}
	}()
}

// Osty: toolchain/llvmgen.osty:755:5
func llvmPrintlnString(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:756:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:757:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_str, ptr %s)", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:760:5
func llvmIfStart(emitter *LlvmEmitter, cond *LlvmValue) *LlvmIfLabels {
	// Osty: toolchain/llvmgen.osty:761:5
	labels := &LlvmIfLabels{thenLabel: llvmNextLabel(emitter, "if.then"), elseLabel: llvmNextLabel(emitter, "if.else"), endLabel: llvmNextLabel(emitter, "if.end")}
	_ = labels
	// Osty: toolchain/llvmgen.osty:766:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", ostyToString(cond.name), ostyToString(labels.thenLabel), ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:767:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.thenLabel)))
		return struct{}{}
	}()
	return labels
}

// Osty: toolchain/llvmgen.osty:771:5
func llvmIfElse(emitter *LlvmEmitter, labels *LlvmIfLabels) {
	// Osty: toolchain/llvmgen.osty:772:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:773:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:776:5
func llvmIfEnd(emitter *LlvmEmitter, labels *LlvmIfLabels) {
	// Osty: toolchain/llvmgen.osty:777:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:778:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:781:5
func llvmIfExprStart(emitter *LlvmEmitter, cond *LlvmValue) *LlvmIfLabels {
	// Osty: toolchain/llvmgen.osty:782:5
	labels := &LlvmIfLabels{thenLabel: llvmNextLabel(emitter, "if.expr.then"), elseLabel: llvmNextLabel(emitter, "if.expr.else"), endLabel: llvmNextLabel(emitter, "if.expr.end")}
	_ = labels
	// Osty: toolchain/llvmgen.osty:787:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", ostyToString(cond.name), ostyToString(labels.thenLabel), ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:788:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.thenLabel)))
		return struct{}{}
	}()
	return labels
}

// Osty: toolchain/llvmgen.osty:792:5
func llvmIfExprElse(emitter *LlvmEmitter, labels *LlvmIfLabels) {
	// Osty: toolchain/llvmgen.osty:793:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:794:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:797:5
func llvmIfExprEnd(emitter *LlvmEmitter, typ string, thenValue *LlvmValue, elseValue *LlvmValue, labels *LlvmIfLabels) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:804:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:805:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:806:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:807:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi %s [ %s, %%%s ], [ %s, %%%s ]", ostyToString(tmp), ostyToString(typ), ostyToString(thenValue.name), ostyToString(labels.thenLabel), ostyToString(elseValue.name), ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
	return &LlvmValue{typ: typ, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:813:5
func llvmInclusiveRangeStart(emitter *LlvmEmitter, iterName string, start *LlvmValue, stop *LlvmValue) *LlvmRangeLoop {
	return llvmRangeStart(emitter, iterName, start, stop, true)
}

// Osty: toolchain/llvmgen.osty:822:5
func llvmRangeStart(emitter *LlvmEmitter, iterName string, start *LlvmValue, stop *LlvmValue, inclusive bool) *LlvmRangeLoop {
	// Osty: toolchain/llvmgen.osty:829:5
	iterPtr := llvmNextTemp(emitter)
	_ = iterPtr
	// Osty: toolchain/llvmgen.osty:830:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca i64", ostyToString(iterPtr)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:831:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store i64 %s, ptr %s", ostyToString(start.name), ostyToString(iterPtr)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:833:5
	loop := &LlvmRangeLoop{condLabel: llvmNextLabel(emitter, "for.cond"), bodyLabel: llvmNextLabel(emitter, "for.body"), endLabel: llvmNextLabel(emitter, "for.end"), iterPtr: iterPtr, current: ""}
	_ = loop
	// Osty: toolchain/llvmgen.osty:840:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(loop.condLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:841:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(loop.condLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:843:5
	current := llvmNextTemp(emitter)
	_ = current
	// Osty: toolchain/llvmgen.osty:844:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load i64, ptr %s", ostyToString(current), ostyToString(iterPtr)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:845:5
	cmp := llvmNextTemp(emitter)
	_ = cmp
	// Osty: toolchain/llvmgen.osty:846:5
	pred := "slt"
	_ = pred
	// Osty: toolchain/llvmgen.osty:847:5
	if inclusive {
		// Osty: toolchain/llvmgen.osty:848:9
		pred = "sle"
	}
	// Osty: toolchain/llvmgen.osty:850:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = icmp %s i64 %s, %s", ostyToString(cmp), ostyToString(pred), ostyToString(current), ostyToString(stop.name)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:851:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", ostyToString(cmp), ostyToString(loop.bodyLabel), ostyToString(loop.endLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:852:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(loop.bodyLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:853:5
	llvmBind(emitter, iterName, llvmI64(current))
	return &LlvmRangeLoop{condLabel: loop.condLabel, bodyLabel: loop.bodyLabel, endLabel: loop.endLabel, iterPtr: loop.iterPtr, current: current}
}

// Osty: toolchain/llvmgen.osty:864:5
func llvmRangeEnd(emitter *LlvmEmitter, loop *LlvmRangeLoop) {
	// Osty: toolchain/llvmgen.osty:865:5
	next := llvmNextTemp(emitter)
	_ = next
	// Osty: toolchain/llvmgen.osty:866:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = add i64 %s, 1", ostyToString(next), ostyToString(loop.current)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:867:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store i64 %s, ptr %s", ostyToString(next), ostyToString(loop.iterPtr)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:868:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(loop.condLabel)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:869:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(loop.endLabel)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:872:5
func llvmReturn(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:873:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  ret %s %s", ostyToString(value.typ), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:876:5
func llvmReturnI32Zero(emitter *LlvmEmitter) {
	// Osty: toolchain/llvmgen.osty:877:5
	func() struct{} { emitter.body = append(emitter.body, "  ret i32 0"); return struct{}{} }()
}

// Osty: toolchain/llvmgen.osty:880:5
func llvmRenderModule(sourcePath string, target string, definitions []string) string {
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, target, make([]string, 0, 1), make([]*LlvmStringGlobal, 0, 1), definitions)
}

// Osty: toolchain/llvmgen.osty:884:5
func llvmRenderModuleWithGlobals(sourcePath string, target string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, target, make([]string, 0, 1), stringGlobals, definitions)
}

// Osty: toolchain/llvmgen.osty:893:5
func llvmRenderModuleWithGlobalsAndTypes(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, make([]string, 0, 1), definitions)
}

// Osty: toolchain/llvmgen.osty:910:5
func llvmRenderModuleWithGcRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmGcRuntimeDeclarations(), definitions)
}

// Osty: toolchain/llvmgen.osty:927:5
func llvmRenderModuleWithListRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmListRuntimeDeclarations(), definitions)
}

// Osty: toolchain/llvmgen.osty:944:5
func llvmRenderModuleWithMapRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmMapRuntimeDeclarations(), definitions)
}

// Osty: toolchain/llvmgen.osty:961:5
func llvmRenderModuleWithSetRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmSetRuntimeDeclarations(), definitions)
}

// Osty: toolchain/llvmgen.osty:978:5
func llvmRenderModuleWithChannelRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmChanRuntimeDeclarations(), definitions)
}

// Osty: toolchain/llvmgen.osty:995:5
func llvmRenderModuleWithStringRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmStringRuntimeDeclarations(), definitions)
}

// Osty: toolchain/llvmgen.osty:1012:1
func llvmRenderModuleWithRuntimeDeclarations(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, runtimeDeclarations []string, definitions []string) string {
	// Osty: toolchain/llvmgen.osty:1020:5
	lines := []string{"; Code generated by osty LLVM backend. DO NOT EDIT.", fmt.Sprintf("; Osty: %s", ostyToString(sourcePath)), llvmStrings.Join([]string{"source_filename = \"", sourcePath, "\""}, "")}
	_ = lines
	// Osty: toolchain/llvmgen.osty:1025:5
	if target != "" {
		// Osty: toolchain/llvmgen.osty:1026:9
		func() struct{} {
			lines = append(lines, llvmStrings.Join([]string{"target triple = \"", target, "\""}, ""))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:1028:5
	func() struct{} { lines = append(lines, ""); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1029:5
	for _, typeDef := range typeDefs {
		// Osty: toolchain/llvmgen.osty:1030:9
		func() struct{} { lines = append(lines, typeDef); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1032:5
	if len(typeDefs) > 0 {
		// Osty: toolchain/llvmgen.osty:1033:9
		func() struct{} { lines = append(lines, ""); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1035:5
	func() struct{} {
		lines = append(lines, "@.fmt_i64 = private unnamed_addr constant [5 x i8] c\"%ld\\0A\\00\"")
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:1036:5
	func() struct{} {
		lines = append(lines, "@.fmt_f64 = private unnamed_addr constant [6 x i8] c\"%.6f\\0A\\00\"")
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:1037:5
	func() struct{} {
		lines = append(lines, "@.fmt_str = private unnamed_addr constant [4 x i8] c\"%s\\0A\\00\"")
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:1038:5
	func() struct{} {
		lines = append(lines, "@.bool_true = private unnamed_addr constant [5 x i8] c\"true\\00\"")
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:1039:5
	func() struct{} {
		lines = append(lines, "@.bool_false = private unnamed_addr constant [6 x i8] c\"false\\00\"")
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:1040:5
	for _, global := range stringGlobals {
		// Osty: toolchain/llvmgen.osty:1041:9
		func() struct{} {
			lines = append(lines, llvmStrings.Join([]string{global.name, " = private unnamed_addr constant [", fmt.Sprintf("%s", ostyToString(global.byteLen)), " x i8] c\"", global.encoded, "\""}, ""))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:1055:5
	func() struct{} { lines = append(lines, "declare i32 @printf(ptr, ...)"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1056:5
	for _, runtimeDeclaration := range runtimeDeclarations {
		// Osty: toolchain/llvmgen.osty:1057:9
		func() struct{} { lines = append(lines, runtimeDeclaration); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1059:5
	firstDefinition := true
	_ = firstDefinition
	// Osty: toolchain/llvmgen.osty:1060:5
	for _, definition := range definitions {
		// Osty: toolchain/llvmgen.osty:1061:9
		if firstDefinition {
			// Osty: toolchain/llvmgen.osty:1062:13
			func() struct{} { lines = append(lines, ""); return struct{}{} }()
			// Osty: toolchain/llvmgen.osty:1063:13
			firstDefinition = false
		}
		// Osty: toolchain/llvmgen.osty:1065:9
		func() struct{} { lines = append(lines, definition); return struct{}{} }()
	}
	return llvmStrings.Join(lines, "\n")
}

// Osty: toolchain/llvmgen.osty:1070:5
func llvmRenderSkeleton(packageName string, sourcePath string, emit string, target string, unsupported string) string {
	// Osty: toolchain/llvmgen.osty:1077:5
	pkg := packageName
	_ = pkg
	// Osty: toolchain/llvmgen.osty:1078:5
	if pkg == "" {
		// Osty: toolchain/llvmgen.osty:1079:9
		pkg = "main"
	}
	// Osty: toolchain/llvmgen.osty:1081:5
	source := sourcePath
	_ = source
	// Osty: toolchain/llvmgen.osty:1082:5
	if source == "" {
		// Osty: toolchain/llvmgen.osty:1083:9
		source = "<unknown>"
	}
	// Osty: toolchain/llvmgen.osty:1086:5
	lines := []string{"; Osty LLVM backend skeleton", fmt.Sprintf("; package: %s", ostyToString(pkg)), fmt.Sprintf("; source: %s", ostyToString(source)), fmt.Sprintf("; emit: %s", ostyToString(emit))}
	_ = lines
	// Osty: toolchain/llvmgen.osty:1092:5
	if target != "" {
		// Osty: toolchain/llvmgen.osty:1093:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("; target: %s", ostyToString(target)))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:1095:5
	if unsupported != "" {
		// Osty: toolchain/llvmgen.osty:1096:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("; unsupported: %s", ostyToString(unsupported)))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:1098:5
	func() struct{} { lines = append(lines, "; code generation is not implemented yet"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1099:5
	func() struct{} { lines = append(lines, ""); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1100:5
	func() struct{} {
		lines = append(lines, llvmStrings.Join([]string{"source_filename = \"", source, "\""}, ""))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:1101:5
	if target != "" {
		// Osty: toolchain/llvmgen.osty:1102:9
		func() struct{} {
			lines = append(lines, llvmStrings.Join([]string{"target triple = \"", target, "\""}, ""))
			return struct{}{}
		}()
	}
	return llvmStrings.Join([]string{llvmStrings.Join(lines, "\n"), "\n"}, "")
}

// Osty: toolchain/llvmgen.osty:1107:5
func llvmRenderFunction(ret string, name string, params []*LlvmParam, body []string) string {
	// Osty: toolchain/llvmgen.osty:1113:5
	lines := []string{fmt.Sprintf("define %s @%s(%s) {", ostyToString(ret), ostyToString(name), ostyToString(llvmParams(params))), "entry:"}
	_ = lines
	// Osty: toolchain/llvmgen.osty:1114:5
	for _, line := range body {
		// Osty: toolchain/llvmgen.osty:1115:9
		func() struct{} { lines = append(lines, line); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1117:5
	func() struct{} { lines = append(lines, "}"); return struct{}{} }()
	return llvmStrings.Join([]string{llvmStrings.Join(lines, "\n"), "\n"}, "")
}

// Osty: toolchain/llvmgen.osty:1133:5
func llvmRenderClosureThunk(retType string, thunkSymbol string, paramTypes []string, realSymbol string) string {
	// Osty: toolchain/llvmgen.osty:1139:5
	ret := func() string {
		if retType == "" {
			return "void"
		} else {
			return retType
		}
	}()
	_ = ret
	// Osty: toolchain/llvmgen.osty:1140:5
	var paramParts []string = []string{"ptr %env"}
	_ = paramParts
	// Osty: toolchain/llvmgen.osty:1141:5
	var argParts []string = make([]string, 0, 1)
	_ = argParts
	// Osty: toolchain/llvmgen.osty:1142:5
	for i := 0; i < len(paramTypes); i++ {
		// Osty: toolchain/llvmgen.osty:1143:9
		pt := paramTypes[i]
		_ = pt
		// Osty: toolchain/llvmgen.osty:1144:9
		func() struct{} {
			paramParts = append(paramParts, fmt.Sprintf("%s %%arg%s", ostyToString(pt), ostyToString(i)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:1145:9
		func() struct{} {
			argParts = append(argParts, fmt.Sprintf("%s %%arg%s", ostyToString(pt), ostyToString(i)))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:1147:5
	params := llvmStrings.Join(paramParts, ", ")
	_ = params
	// Osty: toolchain/llvmgen.osty:1148:5
	args := llvmStrings.Join(argParts, ", ")
	_ = args
	// Osty: toolchain/llvmgen.osty:1149:5
	var lines []string = make([]string, 0, 1)
	_ = lines
	// Osty: toolchain/llvmgen.osty:1150:5
	func() struct{} {
		lines = append(lines, fmt.Sprintf("define private %s @%s(%s) {", ostyToString(ret), ostyToString(thunkSymbol), ostyToString(params)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:1151:5
	func() struct{} { lines = append(lines, "entry:"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1152:5
	if ret == "void" {
		// Osty: toolchain/llvmgen.osty:1153:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("  call void @%s(%s)", ostyToString(realSymbol), ostyToString(args)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:1154:9
		func() struct{} { lines = append(lines, "  ret void"); return struct{}{} }()
	} else {
		// Osty: toolchain/llvmgen.osty:1156:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("  %%__ret = call %s @%s(%s)", ostyToString(ret), ostyToString(realSymbol), ostyToString(args)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:1157:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("  ret %s %%__ret", ostyToString(ret)))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:1159:5
	func() struct{} { lines = append(lines, "}"); return struct{}{} }()
	return llvmStrings.Join([]string{llvmStrings.Join(lines, "\n"), "\n"}, "")
}

// Osty: toolchain/llvmgen.osty:1162:5
func llvmNeedsObjectArtifact(emit string) bool {
	return emit == "object" || emit == "binary"
}

// Osty: toolchain/llvmgen.osty:1166:5
func llvmNeedsBinaryArtifact(emit string) bool {
	return emit == "binary"
}

// Osty: toolchain/llvmgen.osty:1170:5
func llvmClangCompileObjectArgs(target string, irPath string, objectPath string) []string {
	// Osty: toolchain/llvmgen.osty:1175:5
	var args []string = make([]string, 0, 1)
	_ = args
	// Osty: toolchain/llvmgen.osty:1176:5
	if target != "" {
		// Osty: toolchain/llvmgen.osty:1177:9
		func() struct{} { args = append(args, "-target"); return struct{}{} }()
		// Osty: toolchain/llvmgen.osty:1178:9
		func() struct{} { args = append(args, target); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1179:5 — baseline -O2 for production
	// IR compilation. -O0 (the previous default) stranded every
	// alloca/load/store emitted by the backend and disabled
	// vectorization, adding ~100x on simd-friendly benches.
	func() struct{} { args = append(args, "-O2"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1180:5
	func() struct{} { args = append(args, "-c"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1181:5
	func() struct{} { args = append(args, irPath); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1182:5
	func() struct{} { args = append(args, "-o"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1183:5
	func() struct{} { args = append(args, objectPath); return struct{}{} }()
	return args
}

// Osty: toolchain/llvmgen.osty:1187:5
func llvmClangLinkBinaryArgs(target string, objectPaths []string, binaryPath string) []string {
	// Osty: toolchain/llvmgen.osty:1192:5
	var args []string = make([]string, 0, 1)
	_ = args
	// Osty: toolchain/llvmgen.osty:1193:5
	if target != "" {
		// Osty: toolchain/llvmgen.osty:1194:9
		func() struct{} { args = append(args, "-target"); return struct{}{} }()
		// Osty: toolchain/llvmgen.osty:1195:9
		func() struct{} { args = append(args, target); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1197:5
	for _, objectPath := range objectPaths {
		// Osty: toolchain/llvmgen.osty:1198:9
		func() struct{} { args = append(args, objectPath); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1204:5
	if !llvmStrings.Contains(target, "windows") {
		// Osty: toolchain/llvmgen.osty:1205:9
		func() struct{} { args = append(args, "-pthread"); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:1207:5
	func() struct{} { args = append(args, "-o"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:1208:5
	func() struct{} { args = append(args, binaryPath); return struct{}{} }()
	return args
}

// Osty: toolchain/llvmgen.osty:1212:5
func llvmMissingClangMessage() string {
	return "llvm backend: clang not found on PATH; install clang or use --emit=llvm-ir"
}

// Osty: toolchain/llvmgen.osty:1216:5
func llvmMissingBinaryArtifactMessage() string {
	return "llvm backend: missing binary artifact path"
}

// Osty: toolchain/llvmgen.osty:1220:5
func llvmClangFailureMessage(action string, command string, output string) string {
	return llvmStrings.Join([]string{"llvm backend: clang ", action, " failed\ncommand: ", command, "\n", output}, "")
}

// Osty: toolchain/llvmgen.osty:1234:5
func llvmUnsupportedBackendErrorMessage() string {
	return "llvm backend: code generation is not implemented yet"
}

// Osty: toolchain/llvmgen.osty:1238:5
func llvmUnsupportedDiagnostic(kind string, detail string) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:1239:5
	if kind == "go-ffi" {
		// Osty: toolchain/llvmgen.osty:1240:9
		target := detail
		_ = target
		// Osty: toolchain/llvmgen.osty:1241:9
		if target == "" {
			// Osty: toolchain/llvmgen.osty:1242:13
			target = "<unknown>"
		}
		// Osty: toolchain/llvmgen.osty:1244:9
		return &LlvmUnsupportedDiagnostic{code: "LLVM001", kind: "foreign-ffi", message: fmt.Sprintf("Go FFI import %s is not supported by the self-hosted native backend", ostyToString(target)), hint: "rewrite the binding as `use runtime.cabi.<lib>` with an item block for extern C symbols, or `use runtime.<surface>` with an item block for an osty_rt_* runtime ABI helper (LANG_SPEC_v0.5 §12.8)"}
	}
	// Osty: toolchain/llvmgen.osty:1251:5
	if kind == "runtime-ffi" {
		// Osty: toolchain/llvmgen.osty:1252:9
		target := detail
		_ = target
		// Osty: toolchain/llvmgen.osty:1253:9
		if target == "" {
			// Osty: toolchain/llvmgen.osty:1254:13
			target = "<unknown>"
		}
		// Osty: toolchain/llvmgen.osty:1256:9
		return &LlvmUnsupportedDiagnostic{code: "LLVM002", kind: "runtime-ffi", message: fmt.Sprintf("Osty runtime FFI import %s needs native runtime lowering", ostyToString(target)), hint: "add the runtime ABI shim and lowering before compiling this source natively"}
	}
	// Osty: toolchain/llvmgen.osty:1264:5
	if kind == "source-layout" {
		// Osty: toolchain/llvmgen.osty:1265:9
		return llvmUnsupportedDiagnosticWith("LLVM010", kind, detail, "reshape the file around the current LLVM subset: script statements or a simple main function")
	}
	// Osty: toolchain/llvmgen.osty:1272:5
	if kind == "type-system" {
		// Osty: toolchain/llvmgen.osty:1273:9
		return llvmUnsupportedDiagnosticWith("LLVM011", kind, detail, "use Int or Bool values until the LLVM runtime type surface grows")
	}
	// Osty: toolchain/llvmgen.osty:1280:5
	if kind == "statement" {
		// Osty: toolchain/llvmgen.osty:1281:9
		return llvmUnsupportedDiagnosticWith("LLVM012", kind, detail, "reduce the statement to let, assignment, if, range-for, return, or println")
	}
	// Osty: toolchain/llvmgen.osty:1288:5
	if kind == "expression" {
		// Osty: toolchain/llvmgen.osty:1289:9
		return llvmUnsupportedDiagnosticWith("LLVM013", kind, detail, "reduce the expression to Int, Bool, arithmetic, comparison, call, or value-if forms")
	}
	// Osty: toolchain/llvmgen.osty:1296:5
	if kind == "control-flow" {
		// Osty: toolchain/llvmgen.osty:1297:9
		return llvmUnsupportedDiagnosticWith("LLVM014", kind, detail, "use plain if/else or closed Int range loops for the current LLVM backend")
	}
	// Osty: toolchain/llvmgen.osty:1304:5
	if kind == "call" {
		// Osty: toolchain/llvmgen.osty:1305:9
		return llvmUnsupportedDiagnosticWith("LLVM015", kind, detail, "call an Osty function with positional Int/Bool arguments or use println as a statement")
	}
	// Osty: toolchain/llvmgen.osty:1312:5
	if kind == "name" {
		// Osty: toolchain/llvmgen.osty:1313:9
		return llvmUnsupportedDiagnosticWith("LLVM016", kind, detail, "use simple ASCII identifiers that the LLVM bridge can map directly")
	}
	// Osty: toolchain/llvmgen.osty:1320:5
	if kind == "function-signature" {
		// Osty: toolchain/llvmgen.osty:1321:9
		return llvmUnsupportedDiagnosticWith("LLVM017", kind, detail, "use non-generic functions with identifier parameters and Int/Bool types")
	}
	// Osty: toolchain/llvmgen.osty:1328:5
	if kind == "stdlib-body" {
		// Osty: toolchain/llvmgen.osty:1329:9
		return llvmUnsupportedDiagnosticWith("LLVM018", kind, detail, "call stdlib functions whose bodies the LLVM backend can currently lower, or add a runtime shim for this symbol")
	}
	// Osty: toolchain/llvmgen.osty:1337:5
	reason := detail
	_ = reason
	// Osty: toolchain/llvmgen.osty:1338:5
	if reason == "" {
		// Osty: toolchain/llvmgen.osty:1339:9
		reason = "source shape is not supported by the current LLVM backend"
	}
	return &LlvmUnsupportedDiagnostic{code: "LLVM000", kind: "unsupported-source", message: reason, hint: "reduce the program to the LLVM smoke subset while the self-hosted native backend grows"}
}

// Osty: toolchain/llvmgen.osty:1349:5
func llvmUnsupportedDiagnosticWith(code string, kind string, detail string, hint string) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:1355:5
	reason := detail
	_ = reason
	// Osty: toolchain/llvmgen.osty:1356:5
	if reason == "" {
		// Osty: toolchain/llvmgen.osty:1357:9
		reason = "source shape is not supported by the current LLVM backend"
	}
	return &LlvmUnsupportedDiagnostic{code: code, kind: kind, message: reason, hint: hint}
}

// Osty: toolchain/llvmgen.osty:1367:5
func llvmUnsupportedSummary(diag *LlvmUnsupportedDiagnostic) string {
	return fmt.Sprintf("%s %s: %s; hint: %s", ostyToString(diag.code), ostyToString(diag.kind), ostyToString(diag.message), ostyToString(diag.hint))
}

// Osty: toolchain/llvmgen.osty:1371:5
func llvmIsCompareOp(op string) bool {
	return op == "==" || op == "!=" || op == "<" || op == ">" || op == "<=" || op == ">="
}

// Osty: toolchain/llvmgen.osty:1375:5
func llvmIntComparePredicate(op string) string {
	// Osty: toolchain/llvmgen.osty:1376:5
	if op == "==" {
		// Osty: toolchain/llvmgen.osty:1377:9
		return "eq"
	}
	// Osty: toolchain/llvmgen.osty:1379:5
	if op == "!=" {
		// Osty: toolchain/llvmgen.osty:1380:9
		return "ne"
	}
	// Osty: toolchain/llvmgen.osty:1382:5
	if op == "<" {
		// Osty: toolchain/llvmgen.osty:1383:9
		return "slt"
	}
	// Osty: toolchain/llvmgen.osty:1385:5
	if op == ">" {
		// Osty: toolchain/llvmgen.osty:1386:9
		return "sgt"
	}
	// Osty: toolchain/llvmgen.osty:1388:5
	if op == "<=" {
		// Osty: toolchain/llvmgen.osty:1389:9
		return "sle"
	}
	// Osty: toolchain/llvmgen.osty:1391:5
	if op == ">=" {
		// Osty: toolchain/llvmgen.osty:1392:9
		return "sge"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1401:5
func llvmUnsignedIntComparePredicate(op string) string {
	// Osty: toolchain/llvmgen.osty:1402:5
	if op == "==" {
		// Osty: toolchain/llvmgen.osty:1403:9
		return "eq"
	}
	// Osty: toolchain/llvmgen.osty:1405:5
	if op == "!=" {
		// Osty: toolchain/llvmgen.osty:1406:9
		return "ne"
	}
	// Osty: toolchain/llvmgen.osty:1408:5
	if op == "<" {
		// Osty: toolchain/llvmgen.osty:1409:9
		return "ult"
	}
	// Osty: toolchain/llvmgen.osty:1411:5
	if op == ">" {
		// Osty: toolchain/llvmgen.osty:1412:9
		return "ugt"
	}
	// Osty: toolchain/llvmgen.osty:1414:5
	if op == "<=" {
		// Osty: toolchain/llvmgen.osty:1415:9
		return "ule"
	}
	// Osty: toolchain/llvmgen.osty:1417:5
	if op == ">=" {
		// Osty: toolchain/llvmgen.osty:1418:9
		return "uge"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1423:5
func llvmFloatComparePredicate(op string) string {
	// Osty: toolchain/llvmgen.osty:1424:5
	if op == "==" {
		// Osty: toolchain/llvmgen.osty:1425:9
		return "oeq"
	}
	// Osty: toolchain/llvmgen.osty:1427:5
	if op == "!=" {
		// Osty: toolchain/llvmgen.osty:1428:9
		return "one"
	}
	// Osty: toolchain/llvmgen.osty:1430:5
	if op == "<" {
		// Osty: toolchain/llvmgen.osty:1431:9
		return "olt"
	}
	// Osty: toolchain/llvmgen.osty:1433:5
	if op == ">" {
		// Osty: toolchain/llvmgen.osty:1434:9
		return "ogt"
	}
	// Osty: toolchain/llvmgen.osty:1436:5
	if op == "<=" {
		// Osty: toolchain/llvmgen.osty:1437:9
		return "ole"
	}
	// Osty: toolchain/llvmgen.osty:1439:5
	if op == ">=" {
		// Osty: toolchain/llvmgen.osty:1440:9
		return "oge"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1445:5
func llvmIntBinaryInstruction(op string) string {
	// Osty: toolchain/llvmgen.osty:1446:5
	if op == "+" {
		// Osty: toolchain/llvmgen.osty:1447:9
		return "add"
	}
	// Osty: toolchain/llvmgen.osty:1449:5
	if op == "-" {
		// Osty: toolchain/llvmgen.osty:1450:9
		return "sub"
	}
	// Osty: toolchain/llvmgen.osty:1452:5
	if op == "*" {
		// Osty: toolchain/llvmgen.osty:1453:9
		return "mul"
	}
	// Osty: toolchain/llvmgen.osty:1455:5
	if op == "/" {
		// Osty: toolchain/llvmgen.osty:1456:9
		return "sdiv"
	}
	// Osty: toolchain/llvmgen.osty:1458:5
	if op == "%" {
		// Osty: toolchain/llvmgen.osty:1459:9
		return "srem"
	}
	// Osty: toolchain/llvmgen.osty:1461:5
	if op == "&" {
		// Osty: toolchain/llvmgen.osty:1462:9
		return "and"
	}
	// Osty: toolchain/llvmgen.osty:1464:5
	if op == "|" {
		// Osty: toolchain/llvmgen.osty:1465:9
		return "or"
	}
	// Osty: toolchain/llvmgen.osty:1467:5
	if op == "^" {
		// Osty: toolchain/llvmgen.osty:1468:9
		return "xor"
	}
	// Osty: toolchain/llvmgen.osty:1470:5
	if op == "<<" {
		// Osty: toolchain/llvmgen.osty:1471:9
		return "shl"
	}
	// Osty: toolchain/llvmgen.osty:1473:5
	if op == ">>" {
		// Osty: toolchain/llvmgen.osty:1474:9
		return "ashr"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1479:5
func llvmFloatBinaryInstruction(op string) string {
	// Osty: toolchain/llvmgen.osty:1480:5
	if op == "+" {
		// Osty: toolchain/llvmgen.osty:1481:9
		return "fadd"
	}
	// Osty: toolchain/llvmgen.osty:1483:5
	if op == "-" {
		// Osty: toolchain/llvmgen.osty:1484:9
		return "fsub"
	}
	// Osty: toolchain/llvmgen.osty:1486:5
	if op == "*" {
		// Osty: toolchain/llvmgen.osty:1487:9
		return "fmul"
	}
	// Osty: toolchain/llvmgen.osty:1489:5
	if op == "/" {
		// Osty: toolchain/llvmgen.osty:1490:9
		return "fdiv"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1495:5
func llvmLogicalInstruction(op string) string {
	// Osty: toolchain/llvmgen.osty:1496:5
	if op == "&&" {
		// Osty: toolchain/llvmgen.osty:1497:9
		return "and"
	}
	// Osty: toolchain/llvmgen.osty:1499:5
	if op == "||" {
		// Osty: toolchain/llvmgen.osty:1500:9
		return "or"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1514:5
func llvmIsAsciiStringText(text string) bool {
	// Osty: toolchain/llvmgen.osty:1515:5
	_ = text
	return true
}

// Osty: toolchain/llvmgen.osty:1519:5
func llvmIsIdent(name string) bool {
	// Osty: toolchain/llvmgen.osty:1520:5
	if name == "" {
		// Osty: toolchain/llvmgen.osty:1521:9
		return false
	}
	// Osty: toolchain/llvmgen.osty:1523:5
	chars := []rune(name)
	_ = chars
	// Osty: toolchain/llvmgen.osty:1524:5
	for i := 0; i < len(chars); i++ {
		// Osty: toolchain/llvmgen.osty:1525:9
		c := chars[i]
		_ = c
		// Osty: toolchain/llvmgen.osty:1526:9
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			// Osty: toolchain/llvmgen.osty:1527:13
			continue
		}
		// Osty: toolchain/llvmgen.osty:1529:9
		if i > 0 && c >= '0' && c <= '9' {
			// Osty: toolchain/llvmgen.osty:1530:13
			continue
		}
		// Osty: toolchain/llvmgen.osty:1532:9
		return false
	}
	return true
}

// Osty: toolchain/llvmgen.osty:1537:5
func llvmFirstNonEmpty(a string, b string) string {
	// Osty: toolchain/llvmgen.osty:1538:5
	if a != "" {
		// Osty: toolchain/llvmgen.osty:1539:9
		return a
	}
	return b
}

// Osty: toolchain/llvmgen.osty:1544:5
func llvmIsKnownRuntimeFfiPath(path string) bool {
	// Osty: toolchain/llvmgen.osty:1545:5
	if llvmStrings.HasPrefix(path, "runtime.package.") {
		// Osty: toolchain/llvmgen.osty:1546:9
		return true
	}
	// Osty: toolchain/llvmgen.osty:1548:5
	if llvmStrings.HasPrefix(path, "runtime.cabi.") || path == "runtime.cabi" {
		// Osty: toolchain/llvmgen.osty:1549:9
		return true
	}
	return path == "runtime.strings" || path == "runtime.path.filepath"
}

// Osty: toolchain/llvmgen.osty:1554:5
func llvmRuntimeFfiAlias(explicitAlias string, lastPath string, runtimePath string) string {
	// Osty: toolchain/llvmgen.osty:1555:5
	if explicitAlias != "" {
		// Osty: toolchain/llvmgen.osty:1556:9
		return explicitAlias
	}
	// Osty: toolchain/llvmgen.osty:1558:5
	if lastPath != "" {
		// Osty: toolchain/llvmgen.osty:1559:9
		return lastPath
	}
	// Osty: toolchain/llvmgen.osty:1561:5
	parts := llvmStrings.Split(runtimePath, ".")
	_ = parts
	// Osty: toolchain/llvmgen.osty:1562:5
	if len(parts) == 0 {
		// Osty: toolchain/llvmgen.osty:1563:9
		return ""
	}
	return parts[func() int {
		var _p19 int = len(parts)
		var _rhs20 int = 1
		if _rhs20 < 0 && _p19 > math.MaxInt+_rhs20 {
			panic("integer overflow")
		}
		if _rhs20 > 0 && _p19 < math.MinInt+_rhs20 {
			panic("integer overflow")
		}
		return _p19 - _rhs20
	}()]
}

// Osty: toolchain/llvmgen.osty:1568:5
func llvmRuntimeFfiSymbol(path string, name string) string {
	// Osty: toolchain/llvmgen.osty:1574:5
	if path == "runtime.cabi" || llvmStrings.HasPrefix(path, "runtime.cabi.") {
		// Osty: toolchain/llvmgen.osty:1575:9
		return name
	}
	// Osty: toolchain/llvmgen.osty:1577:5
	trimmed := path
	_ = trimmed
	// Osty: toolchain/llvmgen.osty:1578:5
	if llvmStrings.HasPrefix(trimmed, "runtime.") {
		// Osty: toolchain/llvmgen.osty:1579:9
		trimmed = llvmStrings.TrimPrefix(trimmed, "runtime.")
	}
	// Osty: toolchain/llvmgen.osty:1581:5
	out := "osty_rt_"
	_ = out
	// Osty: toolchain/llvmgen.osty:1582:5
	for _, c := range []rune(trimmed) {
		// Osty: toolchain/llvmgen.osty:1583:9
		if c == '.' || c == '/' || c == '-' {
			// Osty: toolchain/llvmgen.osty:1584:13
			out = fmt.Sprintf("%s_", ostyToString(out))
			// Osty: toolchain/llvmgen.osty:1585:13
			continue
		}
		// Osty: toolchain/llvmgen.osty:1587:9
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			// Osty: toolchain/llvmgen.osty:1588:13
			out = fmt.Sprintf("%s%s", ostyToString(out), string(c))
			// Osty: toolchain/llvmgen.osty:1589:13
			continue
		}
		// Osty: toolchain/llvmgen.osty:1591:9
		out = fmt.Sprintf("%s_", ostyToString(out))
	}
	return fmt.Sprintf("%s_%s", ostyToString(out), ostyToString(name))
}

// Osty: toolchain/llvmgen.osty:1602:5
func llvmListRuntimeNewSymbol() string {
	return "osty_rt_list_new"
}

// Osty: toolchain/llvmgen.osty:1606:5
func llvmListRuntimeLenSymbol() string {
	return "osty_rt_list_len"
}

// Osty: toolchain/llvmgen.osty:1610:5
func llvmListRuntimeSortedI64Symbol() string {
	return "osty_rt_list_sorted_i64"
}

// Osty: toolchain/llvmgen.osty:1614:5
func llvmListRuntimeToSetI64Symbol() string {
	return "osty_rt_list_to_set_i64"
}

// Osty: toolchain/llvmgen.osty:1621:5
func llvmListRuntimePushSymbol(suffix string) string {
	return fmt.Sprintf("osty_rt_list_push_%s", ostyToString(suffix))
}

// Osty: toolchain/llvmgen.osty:1625:5
func llvmListRuntimeGetSymbol(suffix string) string {
	return fmt.Sprintf("osty_rt_list_get_%s", ostyToString(suffix))
}

// Osty: toolchain/llvmgen.osty:1629:5
func llvmListRuntimeSetSymbol(suffix string) string {
	return fmt.Sprintf("osty_rt_list_set_%s", ostyToString(suffix))
}

// Osty: toolchain/llvmgen.osty:1633:5
func llvmListRuntimeInsertSymbol(suffix string) string {
	return fmt.Sprintf("osty_rt_list_insert_%s", ostyToString(suffix))
}

// Osty: toolchain/llvmgen.osty:1642:5
func llvmListRuntimeSortedSymbol(elemTyp string, isString bool) string {
	// Osty: toolchain/llvmgen.osty:1643:5
	if isString {
		// Osty: toolchain/llvmgen.osty:1644:9
		return "osty_rt_list_sorted_string"
	}
	// Osty: toolchain/llvmgen.osty:1646:5
	if elemTyp == "i64" {
		// Osty: toolchain/llvmgen.osty:1647:9
		return "osty_rt_list_sorted_i64"
	}
	// Osty: toolchain/llvmgen.osty:1649:5
	if elemTyp == "i1" {
		// Osty: toolchain/llvmgen.osty:1650:9
		return "osty_rt_list_sorted_i1"
	}
	// Osty: toolchain/llvmgen.osty:1652:5
	if elemTyp == "double" {
		// Osty: toolchain/llvmgen.osty:1653:9
		return "osty_rt_list_sorted_f64"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1662:5
func llvmListRuntimeToSetSymbol(elemTyp string, isString bool) string {
	// Osty: toolchain/llvmgen.osty:1663:5
	if isString {
		// Osty: toolchain/llvmgen.osty:1664:9
		return "osty_rt_list_to_set_string"
	}
	// Osty: toolchain/llvmgen.osty:1666:5
	if elemTyp == "i64" {
		// Osty: toolchain/llvmgen.osty:1667:9
		return "osty_rt_list_to_set_i64"
	}
	// Osty: toolchain/llvmgen.osty:1669:5
	if elemTyp == "i1" {
		// Osty: toolchain/llvmgen.osty:1670:9
		return "osty_rt_list_to_set_i1"
	}
	// Osty: toolchain/llvmgen.osty:1672:5
	if elemTyp == "double" {
		// Osty: toolchain/llvmgen.osty:1673:9
		return "osty_rt_list_to_set_f64"
	}
	// Osty: toolchain/llvmgen.osty:1675:5
	if elemTyp == "ptr" {
		// Osty: toolchain/llvmgen.osty:1676:9
		return "osty_rt_list_to_set_ptr"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1681:5
func llvmMapRuntimeNewSymbol() string {
	return "osty_rt_map_new"
}

// Osty: toolchain/llvmgen.osty:1685:5
func llvmMapRuntimeKeysSymbol() string {
	return "osty_rt_map_keys"
}

// Osty: toolchain/llvmgen.osty:1689:5
func llvmMapRuntimeLenSymbol() string {
	return "osty_rt_map_len"
}

// Osty: toolchain/llvmgen.osty:1697:5
func llvmMapKeySuffix(typ string, isString bool) string {
	// Osty: toolchain/llvmgen.osty:1698:5
	if isString {
		// Osty: toolchain/llvmgen.osty:1699:9
		return "string"
	}
	// Osty: toolchain/llvmgen.osty:1701:5
	if typ == "i64" {
		// Osty: toolchain/llvmgen.osty:1702:9
		return "i64"
	}
	// Osty: toolchain/llvmgen.osty:1704:5
	if typ == "i1" {
		// Osty: toolchain/llvmgen.osty:1705:9
		return "i1"
	}
	// Osty: toolchain/llvmgen.osty:1707:5
	if typ == "double" {
		// Osty: toolchain/llvmgen.osty:1708:9
		return "f64"
	}
	// Osty: toolchain/llvmgen.osty:1710:5
	if typ == "ptr" {
		// Osty: toolchain/llvmgen.osty:1711:9
		return "ptr"
	}
	return "bytes"
}

// Osty: toolchain/llvmgen.osty:1716:5
func llvmMapRuntimeContainsSymbol(keyTyp string, isString bool) string {
	return fmt.Sprintf("osty_rt_map_contains_%s", ostyToString(llvmMapKeySuffix(keyTyp, isString)))
}

// Osty: toolchain/llvmgen.osty:1720:5
func llvmMapRuntimeInsertSymbol(keyTyp string, isString bool) string {
	return fmt.Sprintf("osty_rt_map_insert_%s", ostyToString(llvmMapKeySuffix(keyTyp, isString)))
}

// Osty: toolchain/llvmgen.osty:1724:5
func llvmMapRuntimeRemoveSymbol(keyTyp string, isString bool) string {
	return fmt.Sprintf("osty_rt_map_remove_%s", ostyToString(llvmMapKeySuffix(keyTyp, isString)))
}

// Osty: toolchain/llvmgen.osty:1728:5
func llvmMapRuntimeGetOrAbortSymbol(keyTyp string, isString bool) string {
	return fmt.Sprintf("osty_rt_map_get_or_abort_%s", ostyToString(llvmMapKeySuffix(keyTyp, isString)))
}

// Osty: toolchain/llvmgen.osty:1732:5
func llvmSetRuntimeNewSymbol() string {
	return "osty_rt_set_new"
}

// Osty: toolchain/llvmgen.osty:1736:5
func llvmSetRuntimeLenSymbol() string {
	return "osty_rt_set_len"
}

// Osty: toolchain/llvmgen.osty:1740:5
func llvmSetRuntimeToListSymbol() string {
	return "osty_rt_set_to_list"
}

// Osty: toolchain/llvmgen.osty:1749:5
func llvmSetRuntimeContainsSymbol(elemTyp string, isString bool) string {
	return fmt.Sprintf("osty_rt_set_contains_%s", ostyToString(llvmMapKeySuffix(elemTyp, isString)))
}

// Osty: toolchain/llvmgen.osty:1753:5
func llvmSetRuntimeInsertSymbol(elemTyp string, isString bool) string {
	return fmt.Sprintf("osty_rt_set_insert_%s", ostyToString(llvmMapKeySuffix(elemTyp, isString)))
}

// Osty: toolchain/llvmgen.osty:1757:5
func llvmSetRuntimeRemoveSymbol(elemTyp string, isString bool) string {
	return fmt.Sprintf("osty_rt_set_remove_%s", ostyToString(llvmMapKeySuffix(elemTyp, isString)))
}

// Osty: toolchain/llvmgen.osty:1763:5
func llvmContainerAbiKind(typ string, isString bool) int {
	// Osty: toolchain/llvmgen.osty:1764:5
	if isString {
		// Osty: toolchain/llvmgen.osty:1765:9
		return 5
	}
	// Osty: toolchain/llvmgen.osty:1767:5
	if typ == "i64" {
		// Osty: toolchain/llvmgen.osty:1768:9
		return 1
	}
	// Osty: toolchain/llvmgen.osty:1770:5
	if typ == "i1" {
		// Osty: toolchain/llvmgen.osty:1771:9
		return 2
	}
	// Osty: toolchain/llvmgen.osty:1773:5
	if typ == "double" {
		// Osty: toolchain/llvmgen.osty:1774:9
		return 3
	}
	// Osty: toolchain/llvmgen.osty:1776:5
	if typ == "ptr" {
		// Osty: toolchain/llvmgen.osty:1777:9
		return 4
	}
	return 6
}

// Osty: toolchain/llvmgen.osty:1786:5
func llvmListUsesTypedRuntime(elemTyp string) bool {
	return elemTyp == "i64" || elemTyp == "i1" || elemTyp == "double" || elemTyp == "ptr"
}

// Osty: toolchain/llvmgen.osty:1795:5
func llvmListElementSuffix(typ string) string {
	// Osty: toolchain/llvmgen.osty:1796:5
	if typ == "i64" || typ == "i1" || typ == "ptr" {
		// Osty: toolchain/llvmgen.osty:1797:9
		return typ
	}
	// Osty: toolchain/llvmgen.osty:1799:5
	if typ == "double" {
		// Osty: toolchain/llvmgen.osty:1800:9
		return "f64"
	}
	// Osty: toolchain/llvmgen.osty:1802:5
	out := ""
	_ = out
	// Osty: toolchain/llvmgen.osty:1803:5
	for _, c := range []rune(typ) {
		// Osty: toolchain/llvmgen.osty:1804:9
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			// Osty: toolchain/llvmgen.osty:1805:13
			out = fmt.Sprintf("%s%s", ostyToString(out), string(c))
			// Osty: toolchain/llvmgen.osty:1806:13
			continue
		}
		// Osty: toolchain/llvmgen.osty:1808:9
		out = fmt.Sprintf("%s_", ostyToString(out))
	}
	// Osty: toolchain/llvmgen.osty:1810:5
	if out == "" {
		// Osty: toolchain/llvmgen.osty:1811:9
		return "ptr"
	}
	return out
}

// Osty: toolchain/llvmgen.osty:1819:5
func llvmListRuntimeDeclarations() []string {
	return []string{
		"declare ptr @osty_rt_list_new()",
		"declare i64 @osty_rt_list_len(ptr)",
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"declare void @osty_rt_list_push_i1(ptr, i1)",
		"declare void @osty_rt_list_push_f64(ptr, double)",
		"declare void @osty_rt_list_push_ptr(ptr, ptr)",
		"declare void @osty_rt_list_push_bytes_v1(ptr, ptr, i64)",
		"declare void @osty_rt_list_insert_i64(ptr, i64, i64)",
		"declare void @osty_rt_list_insert_i1(ptr, i64, i1)",
		"declare void @osty_rt_list_insert_f64(ptr, i64, double)",
		"declare void @osty_rt_list_insert_ptr(ptr, i64, ptr)",
		"declare i64 @osty_rt_list_get_i64(ptr, i64)",
		"declare i1 @osty_rt_list_get_i1(ptr, i64)",
		"declare double @osty_rt_list_get_f64(ptr, i64)",
		"declare ptr @osty_rt_list_get_ptr(ptr, i64)",
		"declare ptr @osty_rt_list_data_i64(ptr)",
		"declare ptr @osty_rt_list_data_i1(ptr)",
		"declare ptr @osty_rt_list_data_f64(ptr)",
		"declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)",
		"declare void @osty_rt_list_set_i64(ptr, i64, i64)",
		"declare void @osty_rt_list_set_i1(ptr, i64, i1)",
		"declare void @osty_rt_list_set_f64(ptr, i64, double)",
		"declare void @osty_rt_list_set_ptr(ptr, i64, ptr)",
		"declare ptr @osty_rt_list_sorted_i64(ptr)",
		"declare ptr @osty_rt_list_sorted_i1(ptr)",
		"declare ptr @osty_rt_list_sorted_f64(ptr)",
		"declare ptr @osty_rt_list_sorted_string(ptr)",
		"declare ptr @osty_rt_list_to_set_i64(ptr)",
		"declare ptr @osty_rt_list_to_set_i1(ptr)",
		"declare ptr @osty_rt_list_to_set_f64(ptr)",
		"declare ptr @osty_rt_list_to_set_ptr(ptr)",
		"declare ptr @osty_rt_list_to_set_string(ptr)",
	}
}

// Osty: toolchain/llvmgen.osty:1856:5
func llvmListNew(emitter *LlvmEmitter) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_list_new", make([]*LlvmValue, 0, 1))
}

// Osty: toolchain/llvmgen.osty:1860:5
func llvmListLen(emitter *LlvmEmitter, list *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_list_len", []*LlvmValue{list})
}

// Osty: toolchain/llvmgen.osty:1864:5
func llvmListPushI64(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1865:5
	llvmCallVoid(emitter, "osty_rt_list_push_i64", []*LlvmValue{list, value})
}

// Osty: toolchain/llvmgen.osty:1868:5
func llvmListPushI1(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1869:5
	llvmCallVoid(emitter, "osty_rt_list_push_i1", []*LlvmValue{list, value})
}

// Osty: toolchain/llvmgen.osty:1872:5
func llvmListPushF64(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1873:5
	llvmCallVoid(emitter, "osty_rt_list_push_f64", []*LlvmValue{list, value})
}

// Osty: toolchain/llvmgen.osty:1876:5
func llvmListPushPtr(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1877:5
	llvmCallVoid(emitter, "osty_rt_list_push_ptr", []*LlvmValue{list, value})
}

// Osty: toolchain/llvmgen.osty:1885:5
func llvmListPush(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1886:5
	symbol := llvmListRuntimePushSymbol(llvmListElementSuffix(value.typ))
	_ = symbol
	// Osty: toolchain/llvmgen.osty:1887:5
	llvmCallVoid(emitter, symbol, []*LlvmValue{list, value})
}

// Osty: toolchain/llvmgen.osty:1895:5
func llvmListGet(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, elemTyp string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:1901:5
	symbol := llvmListRuntimeGetSymbol(llvmListElementSuffix(elemTyp))
	_ = symbol
	return llvmCall(emitter, elemTyp, symbol, []*LlvmValue{list, index})
}

func llvmListData(emitter *LlvmEmitter, list *LlvmValue, elemTyp string) *LlvmValue {
	return llvmCall(emitter, "ptr", listRuntimeDataSymbol(elemTyp), []*LlvmValue{list})
}

// Osty: toolchain/llvmgen.osty:1905:5
func llvmListGetI64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_list_get_i64", []*LlvmValue{list, index})
}

// Osty: toolchain/llvmgen.osty:1909:5
func llvmListGetI1(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_list_get_i1", []*LlvmValue{list, index})
}

// Osty: toolchain/llvmgen.osty:1913:5
func llvmListGetF64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "double", "osty_rt_list_get_f64", []*LlvmValue{list, index})
}

// Osty: toolchain/llvmgen.osty:1917:5
func llvmListGetPtr(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_list_get_ptr", []*LlvmValue{list, index})
}

// Osty: toolchain/llvmgen.osty:1921:5
func llvmListSetI64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1922:5
	llvmCallVoid(emitter, "osty_rt_list_set_i64", []*LlvmValue{list, index, value})
}

// Osty: toolchain/llvmgen.osty:1925:5
func llvmListSetI1(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1926:5
	llvmCallVoid(emitter, "osty_rt_list_set_i1", []*LlvmValue{list, index, value})
}

// Osty: toolchain/llvmgen.osty:1929:5
func llvmListSetF64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1930:5
	llvmCallVoid(emitter, "osty_rt_list_set_f64", []*LlvmValue{list, index, value})
}

// Osty: toolchain/llvmgen.osty:1933:5
func llvmListSetPtr(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:1934:5
	llvmCallVoid(emitter, "osty_rt_list_set_ptr", []*LlvmValue{list, index, value})
}

// Osty: toolchain/llvmgen.osty:1943:5
func llvmMapRuntimeDeclarations() []string {
	return []string{"declare ptr @osty_rt_map_new()", "declare i64 @osty_rt_map_len(ptr)", "declare ptr @osty_rt_map_keys(ptr)", "declare i1 @osty_rt_map_contains_i64(ptr, i64)", "declare i1 @osty_rt_map_contains_i1(ptr, i1)", "declare i1 @osty_rt_map_contains_f64(ptr, double)", "declare i1 @osty_rt_map_contains_ptr(ptr, ptr)", "declare i1 @osty_rt_map_contains_string(ptr, ptr)", "declare void @osty_rt_map_insert_i64(ptr, i64, ptr)", "declare void @osty_rt_map_insert_i1(ptr, i1, ptr)", "declare void @osty_rt_map_insert_f64(ptr, double, ptr)", "declare void @osty_rt_map_insert_ptr(ptr, ptr, ptr)", "declare void @osty_rt_map_insert_string(ptr, ptr, ptr)", "declare i1 @osty_rt_map_remove_i64(ptr, i64)", "declare i1 @osty_rt_map_remove_i1(ptr, i1)", "declare i1 @osty_rt_map_remove_f64(ptr, double)", "declare i1 @osty_rt_map_remove_ptr(ptr, ptr)", "declare i1 @osty_rt_map_remove_string(ptr, ptr)", "declare void @osty_rt_map_get_or_abort_i64(ptr, i64, ptr)", "declare void @osty_rt_map_get_or_abort_i1(ptr, i1, ptr)", "declare void @osty_rt_map_get_or_abort_f64(ptr, double, ptr)", "declare void @osty_rt_map_get_or_abort_ptr(ptr, ptr, ptr)", "declare void @osty_rt_map_get_or_abort_string(ptr, ptr, ptr)"}
}

// Osty: toolchain/llvmgen.osty:1971:5
func llvmMapNew(emitter *LlvmEmitter) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_map_new", make([]*LlvmValue, 0, 1))
}

// Osty: toolchain/llvmgen.osty:1975:5
func llvmMapLen(emitter *LlvmEmitter, map_ *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_map_len", []*LlvmValue{map_})
}

// Osty: toolchain/llvmgen.osty:1979:5
func llvmMapKeys(emitter *LlvmEmitter, map_ *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_map_keys", []*LlvmValue{map_})
}

// Osty: toolchain/llvmgen.osty:1983:5
func llvmMapContainsI64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_i64", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:1987:5
func llvmMapContainsI1(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_i1", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:1991:5
func llvmMapContainsF64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_f64", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:1995:5
func llvmMapContainsPtr(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_ptr", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:1999:5
func llvmMapContainsString(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_string", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2007:5
func llvmMapInsertI64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2008:5
	llvmCallVoid(emitter, "osty_rt_map_insert_i64", []*LlvmValue{map_, key, valueSlot})
}

// Osty: toolchain/llvmgen.osty:2011:5
func llvmMapInsertI1(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2012:5
	llvmCallVoid(emitter, "osty_rt_map_insert_i1", []*LlvmValue{map_, key, valueSlot})
}

// Osty: toolchain/llvmgen.osty:2015:5
func llvmMapInsertF64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2016:5
	llvmCallVoid(emitter, "osty_rt_map_insert_f64", []*LlvmValue{map_, key, valueSlot})
}

// Osty: toolchain/llvmgen.osty:2019:5
func llvmMapInsertPtr(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2020:5
	llvmCallVoid(emitter, "osty_rt_map_insert_ptr", []*LlvmValue{map_, key, valueSlot})
}

// Osty: toolchain/llvmgen.osty:2023:5
func llvmMapInsertString(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2024:5
	llvmCallVoid(emitter, "osty_rt_map_insert_string", []*LlvmValue{map_, key, valueSlot})
}

// Osty: toolchain/llvmgen.osty:2027:5
func llvmMapRemoveI64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_i64", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2031:5
func llvmMapRemoveI1(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_i1", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2035:5
func llvmMapRemoveF64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_f64", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2039:5
func llvmMapRemovePtr(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_ptr", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2043:5
func llvmMapRemoveString(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_string", []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2050:5
func llvmMapGetOrAbortI64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2051:5
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_i64", []*LlvmValue{map_, key, outSlot})
}

// Osty: toolchain/llvmgen.osty:2054:5
func llvmMapGetOrAbortI1(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2055:5
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_i1", []*LlvmValue{map_, key, outSlot})
}

// Osty: toolchain/llvmgen.osty:2058:5
func llvmMapGetOrAbortF64(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2059:5
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_f64", []*LlvmValue{map_, key, outSlot})
}

// Osty: toolchain/llvmgen.osty:2062:5
func llvmMapGetOrAbortPtr(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2063:5
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_ptr", []*LlvmValue{map_, key, outSlot})
}

// Osty: toolchain/llvmgen.osty:2066:5
func llvmMapGetOrAbortString(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2067:5
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_string", []*LlvmValue{map_, key, outSlot})
}

// Osty: toolchain/llvmgen.osty:2074:5
func llvmMapContains(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, isString bool) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2080:5
	symbol := llvmMapRuntimeContainsSymbol(key.typ, isString)
	_ = symbol
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2088:5
func llvmMapInsert(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, valueSlot *LlvmValue, isString bool) {
	// Osty: toolchain/llvmgen.osty:2095:5
	symbol := llvmMapRuntimeInsertSymbol(key.typ, isString)
	_ = symbol
	// Osty: toolchain/llvmgen.osty:2096:5
	llvmCallVoid(emitter, symbol, []*LlvmValue{map_, key, valueSlot})
}

// Osty: toolchain/llvmgen.osty:2101:5
func llvmMapRemove(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, isString bool) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2107:5
	symbol := llvmMapRuntimeRemoveSymbol(key.typ, isString)
	_ = symbol
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{map_, key})
}

// Osty: toolchain/llvmgen.osty:2114:5
func llvmMapGetOrAbort(emitter *LlvmEmitter, map_ *LlvmValue, key *LlvmValue, outSlot *LlvmValue, isString bool) {
	// Osty: toolchain/llvmgen.osty:2121:5
	symbol := llvmMapRuntimeGetOrAbortSymbol(key.typ, isString)
	_ = symbol
	// Osty: toolchain/llvmgen.osty:2122:5
	llvmCallVoid(emitter, symbol, []*LlvmValue{map_, key, outSlot})
}

// Osty: toolchain/llvmgen.osty:2130:5
func llvmSetRuntimeDeclarations() []string {
	return []string{"declare ptr @osty_rt_set_new(i64)", "declare i64 @osty_rt_set_len(ptr)", "declare ptr @osty_rt_set_to_list(ptr)", "declare i1 @osty_rt_set_contains_i64(ptr, i64)", "declare i1 @osty_rt_set_contains_i1(ptr, i1)", "declare i1 @osty_rt_set_contains_f64(ptr, double)", "declare i1 @osty_rt_set_contains_ptr(ptr, ptr)", "declare i1 @osty_rt_set_contains_string(ptr, ptr)", "declare i1 @osty_rt_set_insert_i64(ptr, i64)", "declare i1 @osty_rt_set_insert_i1(ptr, i1)", "declare i1 @osty_rt_set_insert_f64(ptr, double)", "declare i1 @osty_rt_set_insert_ptr(ptr, ptr)", "declare i1 @osty_rt_set_insert_string(ptr, ptr)", "declare i1 @osty_rt_set_remove_i64(ptr, i64)", "declare i1 @osty_rt_set_remove_i1(ptr, i1)", "declare i1 @osty_rt_set_remove_f64(ptr, double)", "declare i1 @osty_rt_set_remove_ptr(ptr, ptr)", "declare i1 @osty_rt_set_remove_string(ptr, ptr)"}
}

// Osty: toolchain/llvmgen.osty:2156:5
func llvmSetNew(emitter *LlvmEmitter, elemKind int) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_set_new", []*LlvmValue{llvmIntLiteral(elemKind)})
}

// Osty: toolchain/llvmgen.osty:2160:5
func llvmSetLen(emitter *LlvmEmitter, set *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_set_len", []*LlvmValue{set})
}

// Osty: toolchain/llvmgen.osty:2164:5
func llvmSetToList(emitter *LlvmEmitter, set *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_set_to_list", []*LlvmValue{set})
}

// Osty: toolchain/llvmgen.osty:2168:5
func llvmSetContainsI64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_i64", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2172:5
func llvmSetContainsI1(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_i1", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2176:5
func llvmSetContainsF64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_f64", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2180:5
func llvmSetContainsPtr(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_ptr", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2184:5
func llvmSetContainsString(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_string", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2188:5
func llvmSetInsertI64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_i64", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2192:5
func llvmSetInsertI1(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_i1", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2196:5
func llvmSetInsertF64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_f64", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2200:5
func llvmSetInsertPtr(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_ptr", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2204:5
func llvmSetInsertString(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_string", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2208:5
func llvmSetRemoveI64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_i64", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2212:5
func llvmSetRemoveI1(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_i1", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2216:5
func llvmSetRemoveF64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_f64", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2220:5
func llvmSetRemovePtr(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_ptr", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2224:5
func llvmSetRemoveString(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_string", []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2230:5
func llvmSetContains(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue, isString bool) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2236:5
	symbol := llvmSetRuntimeContainsSymbol(item.typ, isString)
	_ = symbol
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2242:5
func llvmSetInsert(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue, isString bool) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2248:5
	symbol := llvmSetRuntimeInsertSymbol(item.typ, isString)
	_ = symbol
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2254:5
func llvmSetRemove(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue, isString bool) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2260:5
	symbol := llvmSetRuntimeRemoveSymbol(item.typ, isString)
	_ = symbol
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{set, item})
}

// Osty: toolchain/llvmgen.osty:2268:5
func llvmChanElementSuffix(elemTyp string) string {
	// Osty: toolchain/llvmgen.osty:2269:5
	if llvmListUsesTypedRuntime(elemTyp) {
		// Osty: toolchain/llvmgen.osty:2270:9
		return llvmListElementSuffix(elemTyp)
	}
	return "bytes_v1"
}

// Osty: toolchain/llvmgen.osty:2275:5
func llvmChanRuntimeMakeSymbol() string {
	return "osty_rt_thread_chan_make"
}

// Osty: toolchain/llvmgen.osty:2279:5
func llvmChanRuntimeSendSymbol(suffix string) string {
	return fmt.Sprintf("osty_rt_thread_chan_send_%s", ostyToString(suffix))
}

// Osty: toolchain/llvmgen.osty:2283:5
func llvmChanRuntimeSendBytesSymbol() string {
	return "osty_rt_thread_chan_send_bytes_v1"
}

// Osty: toolchain/llvmgen.osty:2287:5
func llvmChanRuntimeRecvSymbol(suffix string) string {
	return fmt.Sprintf("osty_rt_thread_chan_recv_%s", ostyToString(suffix))
}

// Osty: toolchain/llvmgen.osty:2291:5
func llvmChanRuntimeCloseSymbol() string {
	return "osty_rt_thread_chan_close"
}

// Osty: toolchain/llvmgen.osty:2295:5
func llvmChanRuntimeIsClosedSymbol() string {
	return "osty_rt_thread_chan_is_closed"
}

// Osty: toolchain/llvmgen.osty:2305:5
func llvmChanRuntimeDeclarations() []string {
	return []string{"declare ptr @osty_rt_thread_chan_make(i64)", "declare void @osty_rt_thread_chan_close(ptr)", "declare i1 @osty_rt_thread_chan_is_closed(ptr)", "declare void @osty_rt_thread_chan_send_i64(ptr, i64)", "declare void @osty_rt_thread_chan_send_i1(ptr, i1)", "declare void @osty_rt_thread_chan_send_f64(ptr, double)", "declare void @osty_rt_thread_chan_send_ptr(ptr, ptr)", "declare void @osty_rt_thread_chan_send_bytes_v1(ptr, ptr, i64)", "declare { i64, i64 } @osty_rt_thread_chan_recv_i64(ptr)", "declare { i64, i64 } @osty_rt_thread_chan_recv_i1(ptr)", "declare { i64, i64 } @osty_rt_thread_chan_recv_f64(ptr)", "declare { i64, i64 } @osty_rt_thread_chan_recv_ptr(ptr)", "declare { i64, i64 } @osty_rt_thread_chan_recv_bytes_v1(ptr)"}
}

// Osty: toolchain/llvmgen.osty:2324:5
func llvmChanMake(emitter *LlvmEmitter, capacity *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", llvmChanRuntimeMakeSymbol(), []*LlvmValue{capacity})
}

// Osty: toolchain/llvmgen.osty:2332:5
func llvmChanSend(emitter *LlvmEmitter, channel *LlvmValue, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2333:5
	symbol := llvmChanRuntimeSendSymbol(llvmListElementSuffix(value.typ))
	_ = symbol
	// Osty: toolchain/llvmgen.osty:2334:5
	llvmCallVoid(emitter, symbol, []*LlvmValue{channel, value})
}

// Osty: toolchain/llvmgen.osty:2341:5
func llvmChanSendBytes(emitter *LlvmEmitter, channel *LlvmValue, slot *LlvmValue, size *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2347:5
	llvmCallVoid(emitter, llvmChanRuntimeSendBytesSymbol(), []*LlvmValue{channel, slot, size})
}

// Osty: toolchain/llvmgen.osty:2354:5
func llvmChanRecv(emitter *LlvmEmitter, channel *LlvmValue, elemTyp string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2355:5
	symbol := llvmChanRuntimeRecvSymbol(llvmChanElementSuffix(elemTyp))
	_ = symbol
	return llvmCall(emitter, "{ i64, i64 }", symbol, []*LlvmValue{channel})
}

// Osty: toolchain/llvmgen.osty:2360:5
func llvmChanClose(emitter *LlvmEmitter, channel *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2361:5
	llvmCallVoid(emitter, llvmChanRuntimeCloseSymbol(), []*LlvmValue{channel})
}

// Osty: toolchain/llvmgen.osty:2366:5
func llvmChanIsClosed(emitter *LlvmEmitter, channel *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", llvmChanRuntimeIsClosedSymbol(), []*LlvmValue{channel})
}

// Osty: toolchain/llvmgen.osty:2376:5
func llvmClosureEnvTypeName(elemTags []string) string {
	// Osty: toolchain/llvmgen.osty:2377:5
	joined := llvmStrings.Join(elemTags, ".")
	_ = joined
	return fmt.Sprintf("ClosureEnv.%s", ostyToString(joined))
}

// Osty: toolchain/llvmgen.osty:2385:5
func llvmClosureEnvTypeDef(name string, elemTypes []string) string {
	return llvmStructTypeDef(name, elemTypes)
}

// Osty: toolchain/llvmgen.osty:2394:5
func llvmClosureEnvAlloc(emitter *LlvmEmitter, envTypeName string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2395:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:2396:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %%%s", ostyToString(tmp), ostyToString(envTypeName)))
		return struct{}{}
	}()
	return &LlvmValue{typ: "ptr", name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:2400:1
func llvmClosureEnvSlotGep(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, slotIndex int) string {
	// Osty: toolchain/llvmgen.osty:2406:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:2407:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr %%%s, ptr %s, i32 0, i32 %s", ostyToString(tmp), ostyToString(envTypeName), ostyToString(envPtr.name), ostyToString(slotIndex)))
		return struct{}{}
	}()
	return tmp
}

// Osty: toolchain/llvmgen.osty:2416:5
func llvmClosureEnvStoreFn(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, fnSymbol string) {
	// Osty: toolchain/llvmgen.osty:2422:5
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, 0)
	_ = gep
	// Osty: toolchain/llvmgen.osty:2423:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store ptr @%s, ptr %s", ostyToString(fnSymbol), ostyToString(gep)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:2429:5
func llvmClosureEnvStoreCapture(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, slotIndex int, value *LlvmValue) {
	// Osty: toolchain/llvmgen.osty:2436:5
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, slotIndex)
	_ = gep
	// Osty: toolchain/llvmgen.osty:2437:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", ostyToString(value.typ), ostyToString(value.name), ostyToString(gep)))
		return struct{}{}
	}()
}

// Osty: toolchain/llvmgen.osty:2441:5
func llvmClosureEnvLoadFn(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2446:5
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, 0)
	_ = gep
	// Osty: toolchain/llvmgen.osty:2447:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:2448:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load ptr, ptr %s", ostyToString(tmp), ostyToString(gep)))
		return struct{}{}
	}()
	return &LlvmValue{typ: "ptr", name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:2455:5
func llvmClosureEnvLoadCapture(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, slotIndex int, captureType string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2462:5
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, slotIndex)
	_ = gep
	// Osty: toolchain/llvmgen.osty:2463:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:2464:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", ostyToString(tmp), ostyToString(captureType), ostyToString(gep)))
		return struct{}{}
	}()
	return &LlvmValue{typ: captureType, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:2474:5
func llvmClosureCallIndirect(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, returnType string, extraArgs []*LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2481:5
	fnPtr := llvmClosureEnvLoadFn(emitter, envPtr, envTypeName)
	_ = fnPtr
	// Osty: toolchain/llvmgen.osty:2482:5
	args := []*LlvmValue{envPtr}
	_ = args
	// Osty: toolchain/llvmgen.osty:2483:5
	var paramTypes []string = []string{"ptr"}
	_ = paramTypes
	// Osty: toolchain/llvmgen.osty:2484:5
	for _, arg := range extraArgs {
		// Osty: toolchain/llvmgen.osty:2485:9
		func() struct{} { args = append(args, arg); return struct{}{} }()
		// Osty: toolchain/llvmgen.osty:2486:9
		func() struct{} { paramTypes = append(paramTypes, arg.typ); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:2488:5
	paramList := llvmStrings.Join(paramTypes, ", ")
	_ = paramList
	// Osty: toolchain/llvmgen.osty:2489:5
	callType := fmt.Sprintf("%s (%s)", ostyToString(returnType), ostyToString(paramList))
	_ = callType
	// Osty: toolchain/llvmgen.osty:2490:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:2491:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s %s(%s)", ostyToString(tmp), ostyToString(callType), ostyToString(fnPtr.name), ostyToString(llvmCallArgs(args))))
		return struct{}{}
	}()
	return &LlvmValue{typ: returnType, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:2502:5
func llvmFnValueCallIndirect(emitter *LlvmEmitter, returnType string, envPtr *LlvmValue, extraArgs []*LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2508:5
	ret := func() string {
		if returnType == "" {
			return "void"
		} else {
			return returnType
		}
	}()
	_ = ret
	// Osty: toolchain/llvmgen.osty:2509:5
	fnPtr := llvmNextTemp(emitter)
	_ = fnPtr
	// Osty: toolchain/llvmgen.osty:2510:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load ptr, ptr %s", ostyToString(fnPtr), ostyToString(envPtr.name)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:2511:5
	args := []*LlvmValue{envPtr}
	_ = args
	// Osty: toolchain/llvmgen.osty:2512:5
	var paramTypes []string = []string{"ptr"}
	_ = paramTypes
	// Osty: toolchain/llvmgen.osty:2513:5
	for _, arg := range extraArgs {
		// Osty: toolchain/llvmgen.osty:2514:9
		func() struct{} { args = append(args, arg); return struct{}{} }()
		// Osty: toolchain/llvmgen.osty:2515:9
		func() struct{} { paramTypes = append(paramTypes, arg.typ); return struct{}{} }()
	}
	// Osty: toolchain/llvmgen.osty:2517:5
	paramList := llvmStrings.Join(paramTypes, ", ")
	_ = paramList
	// Osty: toolchain/llvmgen.osty:2518:5
	callType := fmt.Sprintf("%s (%s)", ostyToString(ret), ostyToString(paramList))
	_ = callType
	// Osty: toolchain/llvmgen.osty:2519:5
	if ret == "void" {
		// Osty: toolchain/llvmgen.osty:2520:9
		func() struct{} {
			emitter.body = append(emitter.body, fmt.Sprintf("  call %s %s(%s)", ostyToString(callType), ostyToString(fnPtr), ostyToString(llvmCallArgs(args))))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:2521:9
		return &LlvmValue{typ: "void", name: "", pointer: false}
	}
	// Osty: toolchain/llvmgen.osty:2523:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: toolchain/llvmgen.osty:2524:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s %s(%s)", ostyToString(tmp), ostyToString(callType), ostyToString(fnPtr), ostyToString(llvmCallArgs(args))))
		return struct{}{}
	}()
	return &LlvmValue{typ: ret, name: tmp, pointer: false}
}

// Osty: toolchain/llvmgen.osty:2533:5
func llvmClosureThunkName(symbol string) string {
	return fmt.Sprintf("__osty_closure_thunk_%s", ostyToString(symbol))
}

// Osty: toolchain/llvmgen.osty:2543:5
func llvmClosureThunkDefinition(symbol string, returnType string, paramTypes []string) string {
	// Osty: toolchain/llvmgen.osty:2548:5
	ret := func() string {
		if returnType == "" {
			return "void"
		} else {
			return returnType
		}
	}()
	_ = ret
	// Osty: toolchain/llvmgen.osty:2549:5
	var headerParts []string = []string{"ptr %env"}
	_ = headerParts
	// Osty: toolchain/llvmgen.osty:2550:5
	var argParts []string = make([]string, 0, 1)
	_ = argParts
	// Osty: toolchain/llvmgen.osty:2551:5
	i := 0
	_ = i
	// Osty: toolchain/llvmgen.osty:2552:5
	for _, paramType := range paramTypes {
		// Osty: toolchain/llvmgen.osty:2553:9
		func() struct{} {
			headerParts = append(headerParts, fmt.Sprintf("%s %%arg%s", ostyToString(paramType), ostyToString(i)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:2554:9
		func() struct{} {
			argParts = append(argParts, fmt.Sprintf("%s %%arg%s", ostyToString(paramType), ostyToString(i)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:2555:9
		func() {
			var _cur21 int = i
			var _rhs22 int = 1
			if _rhs22 > 0 && _cur21 > math.MaxInt-_rhs22 {
				panic("integer overflow")
			}
			if _rhs22 < 0 && _cur21 < math.MinInt-_rhs22 {
				panic("integer overflow")
			}
			i = _cur21 + _rhs22
		}()
	}
	// Osty: toolchain/llvmgen.osty:2557:5
	header := llvmStrings.Join(headerParts, ", ")
	_ = header
	// Osty: toolchain/llvmgen.osty:2558:5
	callArgs := llvmStrings.Join(argParts, ", ")
	_ = callArgs
	// Osty: toolchain/llvmgen.osty:2559:5
	thunk := llvmClosureThunkName(symbol)
	_ = thunk
	// Osty: toolchain/llvmgen.osty:2560:5
	var lines []string = make([]string, 0, 1)
	_ = lines
	// Osty: toolchain/llvmgen.osty:2561:5
	func() struct{} {
		lines = append(lines, fmt.Sprintf("define private %s @%s(%s) {", ostyToString(ret), ostyToString(thunk), ostyToString(header)))
		return struct{}{}
	}()
	// Osty: toolchain/llvmgen.osty:2562:5
	func() struct{} { lines = append(lines, "entry:"); return struct{}{} }()
	// Osty: toolchain/llvmgen.osty:2563:5
	if ret == "void" {
		// Osty: toolchain/llvmgen.osty:2564:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("  call void @%s(%s)", ostyToString(symbol), ostyToString(callArgs)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:2565:9
		func() struct{} { lines = append(lines, "  ret void"); return struct{}{} }()
	} else {
		// Osty: toolchain/llvmgen.osty:2567:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("  %%ret = call %s @%s(%s)", ostyToString(ret), ostyToString(symbol), ostyToString(callArgs)))
			return struct{}{}
		}()
		// Osty: toolchain/llvmgen.osty:2568:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("  ret %s %%ret", ostyToString(ret)))
			return struct{}{}
		}()
	}
	// Osty: toolchain/llvmgen.osty:2570:5
	func() struct{} { lines = append(lines, "}"); return struct{}{} }()
	return llvmStrings.Join(lines, "\n")
}

// Osty: toolchain/llvmgen.osty:2579:5
func llvmClosureBareFnEnv(emitter *LlvmEmitter, symbol string) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2580:5
	envTypeName := llvmClosureEnvTypeName([]string{"ptr"})
	_ = envTypeName
	// Osty: toolchain/llvmgen.osty:2581:5
	env := llvmClosureEnvAlloc(emitter, envTypeName)
	_ = env
	// Osty: toolchain/llvmgen.osty:2582:5
	llvmClosureEnvStoreFn(emitter, env, envTypeName, llvmClosureThunkName(symbol))
	return env
}

// Osty: toolchain/llvmgen.osty:2589:5
func llvmClosureBareFnEnvTypeDef() string {
	return llvmClosureEnvTypeDef(llvmClosureEnvTypeName([]string{"ptr"}), []string{"ptr"})
}

// Osty: toolchain/llvmgen.osty:2593:5
func llvmStringRuntimeEqualSymbol() string {
	return "osty_rt_strings_Equal"
}

// Osty: toolchain/llvmgen.osty:2597:5
func llvmStringRuntimeHasPrefixSymbol() string {
	return "osty_rt_strings_HasPrefix"
}

// Osty: toolchain/llvmgen.osty:2601:5
func llvmStringRuntimeHasSuffixSymbol() string {
	return "osty_rt_strings_HasSuffix"
}

// Osty: toolchain/llvmgen.osty:2605:5
func llvmStringRuntimeContainsSymbol() string {
	return "osty_rt_strings_Contains"
}

// Osty: toolchain/llvmgen.osty:2609:5
func llvmStringRuntimeSplitSymbol() string {
	return "osty_rt_strings_Split"
}

// Osty: toolchain/llvmgen.osty:2613:5
func llvmStringRuntimeSplitNSymbol() string {
	return "osty_rt_strings_SplitN"
}

// Osty: toolchain/llvmgen.osty:2617:5
func llvmStringRuntimeConcatSymbol() string {
	return "osty_rt_strings_Concat"
}

// Osty: toolchain/llvmgen.osty:2621:5
func llvmIntRuntimeToStringSymbol() string {
	return "osty_rt_int_to_string"
}

// Osty: toolchain/llvmgen.osty:2625:5
func llvmFloatRuntimeToStringSymbol() string {
	return "osty_rt_float_to_string"
}

// Osty: toolchain/llvmgen.osty:2629:5
func llvmBoolRuntimeToStringSymbol() string {
	return "osty_rt_bool_to_string"
}

// Osty: toolchain/llvmgen.osty:2633:5
func llvmStringRuntimeByteLenSymbol() string {
	return "osty_rt_strings_ByteLen"
}

// Osty: toolchain/llvmgen.osty:2637:5
func llvmStringRuntimeCountSymbol() string {
	return "osty_rt_strings_Count"
}

// Osty: toolchain/llvmgen.osty:2641:5
func llvmStringRuntimeIndexOfSymbol() string {
	return "osty_rt_strings_IndexOf"
}

// Osty: toolchain/llvmgen.osty:2645:5
func llvmStringRuntimeCompareSymbol() string {
	return "osty_rt_strings_Compare"
}

// Osty: toolchain/llvmgen.osty:2649:5
func llvmStringRuntimeJoinSymbol() string {
	return "osty_rt_strings_Join"
}

// Osty: toolchain/llvmgen.osty:2653:5
func llvmStringRuntimeRepeatSymbol() string {
	return "osty_rt_strings_Repeat"
}

// Osty: toolchain/llvmgen.osty:2657:5
func llvmStringRuntimeReplaceSymbol() string {
	return "osty_rt_strings_Replace"
}

// Osty: toolchain/llvmgen.osty:2661:5
func llvmStringRuntimeReplaceAllSymbol() string {
	return "osty_rt_strings_ReplaceAll"
}

// Osty: toolchain/llvmgen.osty:2665:5
func llvmStringRuntimeSliceSymbol() string {
	return "osty_rt_strings_Slice"
}

// Osty: toolchain/llvmgen.osty:2669:5
func llvmStringRuntimeToUpperSymbol() string {
	return "osty_rt_strings_ToUpper"
}

// Osty: toolchain/llvmgen.osty:2673:5
func llvmStringRuntimeToLowerSymbol() string {
	return "osty_rt_strings_ToLower"
}

// Osty: toolchain/llvmgen.osty:2677:5
func llvmStringRuntimeIsValidIntSymbol() string {
	return "osty_rt_strings_IsValidInt"
}

// Osty: toolchain/llvmgen.osty:2681:5
func llvmStringRuntimeToIntSymbol() string {
	return "osty_rt_strings_ToInt"
}

// Osty: toolchain/llvmgen.osty:2685:5
func llvmStringRuntimeIsValidFloatSymbol() string {
	return "osty_rt_strings_IsValidFloat"
}

// Osty: toolchain/llvmgen.osty:2689:5
func llvmStringRuntimeToFloatSymbol() string {
	return "osty_rt_strings_ToFloat"
}

// Osty: toolchain/llvmgen.osty:2693:5
func llvmStringRuntimeTrimStartSymbol() string {
	return "osty_rt_strings_TrimStart"
}

// Osty: toolchain/llvmgen.osty:2697:5
func llvmStringRuntimeTrimEndSymbol() string {
	return "osty_rt_strings_TrimEnd"
}

// Osty: toolchain/llvmgen.osty:2701:5
func llvmStringRuntimeTrimPrefixSymbol() string {
	return "osty_rt_strings_TrimPrefix"
}

// Osty: toolchain/llvmgen.osty:2705:5
func llvmStringRuntimeTrimSuffixSymbol() string {
	return "osty_rt_strings_TrimSuffix"
}

// Osty: toolchain/llvmgen.osty:2709:5
func llvmStringRuntimeTrimSpaceSymbol() string {
	return "osty_rt_strings_TrimSpace"
}

// Osty: toolchain/llvmgen.osty:2713:5
func llvmStringRuntimeCharsSymbol() string {
	return "osty_rt_strings_Chars"
}

// Osty: toolchain/llvmgen.osty:2717:5
func llvmStringRuntimeBytesSymbol() string {
	return "osty_rt_strings_Bytes"
}

// Osty: toolchain/llvmgen.osty:2721:5
func llvmStringRuntimeToBytesSymbol() string {
	return "osty_rt_strings_ToBytes"
}

// Osty: toolchain/llvmgen.osty:2731:5
func llvmStringRuntimeDeclarations() []string {
	return []string{"declare i1 @osty_rt_strings_Equal(ptr, ptr)", "declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)", "declare i1 @osty_rt_strings_HasSuffix(ptr, ptr)", "declare i1 @osty_rt_strings_Contains(ptr, ptr)", "declare ptr @osty_rt_strings_Split(ptr, ptr)", "declare ptr @osty_rt_strings_Concat(ptr, ptr)", "declare ptr @osty_rt_int_to_string(i64)", "declare ptr @osty_rt_float_to_string(double)", "declare ptr @osty_rt_bool_to_string(i1)", "declare i64 @osty_rt_strings_ByteLen(ptr)", "declare i64 @osty_rt_strings_Compare(ptr, ptr)", "declare i64 @osty_rt_strings_Count(ptr, ptr)", "declare i64 @osty_rt_strings_IndexOf(ptr, ptr)", "declare ptr @osty_rt_strings_Join(ptr, ptr)", "declare ptr @osty_rt_strings_Repeat(ptr, i64)", "declare ptr @osty_rt_strings_Replace(ptr, ptr, ptr)", "declare ptr @osty_rt_strings_ReplaceAll(ptr, ptr, ptr)", "declare ptr @osty_rt_strings_Slice(ptr, i64, i64)", "declare ptr @osty_rt_strings_ToUpper(ptr)", "declare ptr @osty_rt_strings_ToLower(ptr)", "declare i1 @osty_rt_strings_IsValidInt(ptr)", "declare i64 @osty_rt_strings_ToInt(ptr)", "declare i1 @osty_rt_strings_IsValidFloat(ptr)", "declare double @osty_rt_strings_ToFloat(ptr)", "declare ptr @osty_rt_strings_TrimStart(ptr)", "declare ptr @osty_rt_strings_TrimEnd(ptr)", "declare ptr @osty_rt_strings_TrimPrefix(ptr, ptr)", "declare ptr @osty_rt_strings_TrimSuffix(ptr, ptr)", "declare ptr @osty_rt_strings_TrimSpace(ptr)", "declare ptr @osty_rt_strings_Chars(ptr)", "declare ptr @osty_rt_strings_Bytes(ptr)", "declare ptr @osty_rt_strings_ToBytes(ptr)"}
}

// Osty: toolchain/llvmgen.osty:2768:5
func llvmStringEqual(emitter *LlvmEmitter, left *LlvmValue, right *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_strings_Equal", []*LlvmValue{left, right})
}

// Osty: toolchain/llvmgen.osty:2772:5
func llvmStringHasPrefix(emitter *LlvmEmitter, value *LlvmValue, prefix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_strings_HasPrefix", []*LlvmValue{value, prefix})
}

// Osty: toolchain/llvmgen.osty:2776:5
func llvmStringHasSuffix(emitter *LlvmEmitter, value *LlvmValue, suffix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_strings_HasSuffix", []*LlvmValue{value, suffix})
}

// Osty: toolchain/llvmgen.osty:2780:5
func llvmStringSplit(emitter *LlvmEmitter, value *LlvmValue, sep *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Split", []*LlvmValue{value, sep})
}

// Osty: toolchain/llvmgen.osty:2784:5
func llvmStringConcat(emitter *LlvmEmitter, left *LlvmValue, right *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Concat", []*LlvmValue{left, right})
}

// Osty: toolchain/llvmgen.osty:2788:5
func llvmIntRuntimeToString(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_int_to_string", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2792:5
func llvmFloatRuntimeToString(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_float_to_string", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2796:5
func llvmBoolRuntimeToString(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_bool_to_string", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2800:5
func llvmStringCompare(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: toolchain/llvmgen.osty:2806:5
	if op == "==" {
		// Osty: toolchain/llvmgen.osty:2807:9
		return llvmStringEqual(emitter, left, right)
	}
	// Osty: toolchain/llvmgen.osty:2809:5
	if op == "!=" {
		// Osty: toolchain/llvmgen.osty:2810:9
		return llvmNotI1(emitter, llvmStringEqual(emitter, left, right))
	}
	// Osty: toolchain/llvmgen.osty:2812:5
	cmp := llvmStringRuntimeCompare(emitter, left, right)
	_ = cmp
	return llvmCompare(emitter, llvmIntComparePredicate(op), cmp, llvmIntLiteral(0))
}

// Osty: toolchain/llvmgen.osty:2816:5
func llvmStringByteLen(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_strings_ByteLen", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2820:5
func llvmStringRuntimeCount(emitter *LlvmEmitter, value *LlvmValue, substr *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_strings_Count", []*LlvmValue{value, substr})
}

// Osty: toolchain/llvmgen.osty:2828:5
func llvmStringRuntimeIndexOf(emitter *LlvmEmitter, value *LlvmValue, substr *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_strings_IndexOf", []*LlvmValue{value, substr})
}

// Osty: toolchain/llvmgen.osty:2836:5
func llvmStringRuntimeCompare(emitter *LlvmEmitter, left *LlvmValue, right *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_strings_Compare", []*LlvmValue{left, right})
}

// Osty: toolchain/llvmgen.osty:2844:5
func llvmStringRuntimeJoin(emitter *LlvmEmitter, parts *LlvmValue, sep *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Join", []*LlvmValue{parts, sep})
}

// Osty: toolchain/llvmgen.osty:2852:5
func llvmStringRuntimeRepeat(emitter *LlvmEmitter, value *LlvmValue, n *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Repeat", []*LlvmValue{value, n})
}

// Osty: toolchain/llvmgen.osty:2860:5
func llvmStringRuntimeReplace(emitter *LlvmEmitter, value *LlvmValue, old *LlvmValue, newValue *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Replace", []*LlvmValue{value, old, newValue})
}

// Osty: toolchain/llvmgen.osty:2869:5
func llvmStringRuntimeReplaceAll(emitter *LlvmEmitter, value *LlvmValue, old *LlvmValue, newValue *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_ReplaceAll", []*LlvmValue{value, old, newValue})
}

// Osty: toolchain/llvmgen.osty:2878:5
func llvmStringRuntimeSlice(emitter *LlvmEmitter, value *LlvmValue, start *LlvmValue, end *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Slice", []*LlvmValue{value, start, end})
}

// Osty: toolchain/llvmgen.osty:2887:5
func llvmStringRuntimeToUpper(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_ToUpper", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2891:5
func llvmStringRuntimeToLower(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_ToLower", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2895:5
func llvmStringRuntimeTrimStart(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimStart", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2899:5
func llvmStringRuntimeTrimEnd(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimEnd", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2903:5
func llvmStringRuntimeTrimPrefix(emitter *LlvmEmitter, value *LlvmValue, prefix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimPrefix", []*LlvmValue{value, prefix})
}

// Osty: toolchain/llvmgen.osty:2911:5
func llvmStringRuntimeTrimSuffix(emitter *LlvmEmitter, value *LlvmValue, suffix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimSuffix", []*LlvmValue{value, suffix})
}

// Osty: toolchain/llvmgen.osty:2919:5
func llvmStringRuntimeTrimSpace(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimSpace", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2923:5
func llvmStringChars(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Chars", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2927:5
func llvmStringBytes(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Bytes", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2931:5
func llvmStringToBytes(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_ToBytes", []*LlvmValue{value})
}

// Osty: toolchain/llvmgen.osty:2935:5
func llvmBuiltinType(name string) string {
	// Osty: toolchain/llvmgen.osty:2936:5
	if name == "Int" {
		// Osty: toolchain/llvmgen.osty:2937:9
		return "i64"
	}
	// Osty: toolchain/llvmgen.osty:2939:5
	if name == "Float" {
		// Osty: toolchain/llvmgen.osty:2940:9
		return "double"
	}
	// Osty: toolchain/llvmgen.osty:2942:5
	if name == "Bool" {
		// Osty: toolchain/llvmgen.osty:2943:9
		return "i1"
	}
	// Osty: toolchain/llvmgen.osty:2945:5
	if name == "Char" {
		// Osty: toolchain/llvmgen.osty:2946:9
		return "i32"
	}
	// Osty: toolchain/llvmgen.osty:2948:5
	if name == "Byte" {
		// Osty: toolchain/llvmgen.osty:2949:9
		return "i8"
	}
	// Osty: toolchain/llvmgen.osty:2951:5
	if name == "String" || name == "Bytes" || name == "Error" {
		// Osty: toolchain/llvmgen.osty:2952:9
		return "ptr"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:2957:5
func llvmRuntimeAbiBuiltinType(name string) string {
	// Osty: toolchain/llvmgen.osty:2958:5
	if name == "Int" || name == "Float" || name == "Bool" || name == "Char" || name == "Byte" || name == "String" {
		// Osty: toolchain/llvmgen.osty:2959:9
		return llvmBuiltinType(name)
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:2964:5
func llvmEnumPayloadBuiltinType(name string) string {
	// Osty: toolchain/llvmgen.osty:2965:5
	if name == "Int" || name == "Float" || name == "Char" || name == "Byte" || name == "String" {
		// Osty: toolchain/llvmgen.osty:2966:9
		return llvmBuiltinType(name)
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:2971:5
func llvmZeroLiteral(typ string) string {
	// Osty: toolchain/llvmgen.osty:2972:5
	if typ == "double" {
		// Osty: toolchain/llvmgen.osty:2973:9
		return "0.0"
	}
	// Osty: toolchain/llvmgen.osty:2975:5
	if typ == "ptr" {
		// Osty: toolchain/llvmgen.osty:2976:9
		return "null"
	}
	return "0"
}

// Osty: toolchain/llvmgen.osty:2981:5
func llvmStructTypeName(name string) string {
	return fmt.Sprintf("%%%s", ostyToString(name))
}

// Osty: toolchain/llvmgen.osty:2985:5
func llvmEnumStorageType(name string, hasPayload bool) string {
	// Osty: toolchain/llvmgen.osty:2986:5
	if hasPayload {
		// Osty: toolchain/llvmgen.osty:2987:9
		return llvmStructTypeName(name)
	}
	return "i64"
}

// Osty: toolchain/llvmgen.osty:2992:5
func llvmSignatureParamName(name string, index int) string {
	// Osty: toolchain/llvmgen.osty:2993:5
	if name != "" {
		// Osty: toolchain/llvmgen.osty:2994:9
		return name
	}
	return fmt.Sprintf("arg%s", ostyToString(index))
}

// Osty: toolchain/llvmgen.osty:2999:5
func llvmAllowsMainSignature(paramCount int, hasReturnType bool) bool {
	return paramCount == 0 && !(hasReturnType)
}

// Osty: toolchain/llvmgen.osty:3003:5
func llvmNamedType(name string, pathLen int, argLen int, structType string, enumType string) string {
	// Osty: toolchain/llvmgen.osty:3010:5
	if pathLen != 1 || argLen != 0 {
		// Osty: toolchain/llvmgen.osty:3011:9
		return "ptr"
	}
	// Osty: toolchain/llvmgen.osty:3013:5
	builtin := llvmBuiltinType(name)
	_ = builtin
	// Osty: toolchain/llvmgen.osty:3014:5
	if builtin != "" {
		// Osty: toolchain/llvmgen.osty:3015:9
		return builtin
	}
	// Osty: toolchain/llvmgen.osty:3017:5
	if structType != "" {
		// Osty: toolchain/llvmgen.osty:3018:9
		return structType
	}
	// Osty: toolchain/llvmgen.osty:3020:5
	if enumType != "" {
		// Osty: toolchain/llvmgen.osty:3021:9
		return enumType
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:3026:5
func llvmRuntimeAbiNamedType(name string, pathLen int, argLen int, structType string, enumType string) string {
	// Osty: toolchain/llvmgen.osty:3033:5
	if pathLen == 1 && argLen == 0 {
		// Osty: toolchain/llvmgen.osty:3034:9
		builtin := llvmRuntimeAbiBuiltinType(name)
		_ = builtin
		// Osty: toolchain/llvmgen.osty:3035:9
		if builtin != "" {
			// Osty: toolchain/llvmgen.osty:3036:13
			return builtin
		}
		// Osty: toolchain/llvmgen.osty:3038:9
		if structType != "" {
			// Osty: toolchain/llvmgen.osty:3039:13
			return structType
		}
		// Osty: toolchain/llvmgen.osty:3041:9
		if enumType != "" {
			// Osty: toolchain/llvmgen.osty:3042:13
			return enumType
		}
	}
	return "ptr"
}

// Osty: toolchain/llvmgen.osty:3048:5
func llvmEnumPayloadNamedType(name string, pathLen int, argLen int) string {
	// Osty: toolchain/llvmgen.osty:3049:5
	if pathLen != 1 || argLen != 0 {
		// Osty: toolchain/llvmgen.osty:3050:9
		return ""
	}
	return llvmEnumPayloadBuiltinType(name)
}

// Osty: toolchain/llvmgen.osty:3055:5
func llvmNominalDeclHeaderDiagnostic(kind string, name string, identOk bool, genericCount int, methodCount int) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:3062:5
	if !(identOk) {
		// Osty: toolchain/llvmgen.osty:3063:9
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("%s name \"%s\"", ostyToString(kind), ostyToString(name)))
	}
	// Osty: toolchain/llvmgen.osty:3065:5
	if genericCount != 0 {
		// Osty: toolchain/llvmgen.osty:3066:9
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("generic %s \"%s\" is not supported", ostyToString(kind), ostyToString(name)))
	}
	// Osty: toolchain/llvmgen.osty:3071:5
	_ = methodCount
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: toolchain/llvmgen.osty:3075:5
func llvmFunctionHeaderDiagnostic(name string, identOk bool, hasRecv bool, genericCount int, hasBody bool, isMain bool, paramCount int, hasReturnType bool) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:3085:5
	if !(identOk) {
		// Osty: toolchain/llvmgen.osty:3086:9
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("function name \"%s\"", ostyToString(name)))
	}
	// Osty: toolchain/llvmgen.osty:3088:5
	if genericCount != 0 {
		// Osty: toolchain/llvmgen.osty:3089:9
		return llvmUnsupportedDiagnostic("function-signature", "generic functions are not supported")
	}
	// Osty: toolchain/llvmgen.osty:3094:5
	if !(hasBody) {
		// Osty: toolchain/llvmgen.osty:3095:9
		return llvmUnsupportedDiagnostic("source-layout", fmt.Sprintf("function \"%s\" has no body", ostyToString(name)))
	}
	// Osty: toolchain/llvmgen.osty:3097:5
	if isMain && !(llvmAllowsMainSignature(paramCount, hasReturnType)) {
		// Osty: toolchain/llvmgen.osty:3098:9
		return llvmUnsupportedDiagnostic("function-signature", "LLVM main must have no params and no return type")
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: toolchain/llvmgen.osty:3106:5
func llvmIsRuntimeAbiListType(name string, pathLen int, argLen int) bool {
	return name == "List" && pathLen == 1 && argLen == 1
}

// Osty: toolchain/llvmgen.osty:3110:5
func llvmStructFieldDiagnostic(structName string, fieldName string, identOk bool, hasDefault bool, duplicate bool, recursive bool, detail string) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:3119:5
	if !(identOk) {
		// Osty: toolchain/llvmgen.osty:3120:9
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("struct \"%s\" field name \"%s\"", ostyToString(structName), ostyToString(fieldName)))
	}
	// Osty: toolchain/llvmgen.osty:3125:5
	if hasDefault {
		// Osty: toolchain/llvmgen.osty:3126:9
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("struct \"%s\" field \"%s\" has a default value", ostyToString(structName), ostyToString(fieldName)))
	}
	// Osty: toolchain/llvmgen.osty:3131:5
	if duplicate {
		// Osty: toolchain/llvmgen.osty:3132:9
		return llvmUnsupportedDiagnostic("source-layout", fmt.Sprintf("struct \"%s\" duplicate field \"%s\"", ostyToString(structName), ostyToString(fieldName)))
	}
	// Osty: toolchain/llvmgen.osty:3137:5
	if detail != "" {
		// Osty: toolchain/llvmgen.osty:3138:9
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("struct \"%s\" field \"%s\": %s", ostyToString(structName), ostyToString(fieldName), ostyToString(detail)))
	}
	// Osty: toolchain/llvmgen.osty:3143:5
	if recursive {
		// Osty: toolchain/llvmgen.osty:3144:9
		return llvmUnsupportedDiagnosticWith("LLVM011", "type-system", fmt.Sprintf("struct \"%s\" recursive field \"%s\" requires indirection", ostyToString(structName), ostyToString(fieldName)), "break the cycle via an arena index (Int id) or List<T> handle until the LLVM backend grows recursive-struct support")
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: toolchain/llvmgen.osty:3154:5
func llvmEnumVariantHeaderDiagnostic(enumName string, variantName string, identOk bool, payloadCount int, duplicate bool) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:3161:5
	if !(identOk) {
		// Osty: toolchain/llvmgen.osty:3162:9
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("enum \"%s\" variant name \"%s\"", ostyToString(enumName), ostyToString(variantName)))
	}
	// Osty: toolchain/llvmgen.osty:3167:5
	if duplicate {
		// Osty: toolchain/llvmgen.osty:3168:9
		return llvmUnsupportedDiagnostic("source-layout", fmt.Sprintf("enum \"%s\" duplicate variant \"%s\"", ostyToString(enumName), ostyToString(variantName)))
	}
	// Osty: toolchain/llvmgen.osty:3173:5
	_ = payloadCount
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: toolchain/llvmgen.osty:3177:5
func llvmEnumBoxedMultiFieldDiagnostic(enumName string, variantName string, payloadCount int) *LlvmUnsupportedDiagnostic {
	return llvmUnsupportedDiagnosticWith("LLVM011", "type-system", fmt.Sprintf("enum \"%s\" variant \"%s\" has %s payload fields with heterogeneous types across variants; boxed multi-field payloads are not supported yet", ostyToString(enumName), ostyToString(variantName), ostyToString(payloadCount)), "keep a single boxed payload per variant, or make all variant payloads share one scalar/pointer type so the inline multi-slot layout can be used")
}

// Osty: toolchain/llvmgen.osty:3190:5
func llvmEnumPayloadDiagnostic(enumName string, variantName string, detail string, expectedType string, actualType string) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:3197:5
	if detail != "" {
		// Osty: toolchain/llvmgen.osty:3198:9
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("enum \"%s\" variant \"%s\" payload: %s", ostyToString(enumName), ostyToString(variantName), ostyToString(detail)))
	}
	// Osty: toolchain/llvmgen.osty:3203:5
	if expectedType != "" && actualType != "" && expectedType != actualType {
		// Osty: toolchain/llvmgen.osty:3204:9
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("enum \"%s\" mixes payload types %s and %s; heterogeneous-payload enums require boxed representation (deferred)", ostyToString(enumName), ostyToString(expectedType), ostyToString(actualType)))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: toolchain/llvmgen.osty:3212:5
func llvmRuntimeFfiHeaderUnsupported(hasRecv bool, genericCount int) string {
	// Osty: toolchain/llvmgen.osty:3213:5
	if hasRecv {
		// Osty: toolchain/llvmgen.osty:3214:9
		return "methods are not supported"
	}
	// Osty: toolchain/llvmgen.osty:3216:5
	if genericCount != 0 {
		// Osty: toolchain/llvmgen.osty:3217:9
		return "generic functions are not supported"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:3222:5
func llvmRuntimeFfiReturnUnsupported(detail string) string {
	// Osty: toolchain/llvmgen.osty:3223:5
	if detail != "" {
		// Osty: toolchain/llvmgen.osty:3224:9
		return fmt.Sprintf("return type: %s", ostyToString(detail))
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:3229:5
func llvmRuntimeFfiParamUnsupported(name string, nilParam bool, hasPatternOrDefault bool, detail string) string {
	// Osty: toolchain/llvmgen.osty:3235:5
	if nilParam {
		// Osty: toolchain/llvmgen.osty:3236:9
		return "nil parameter"
	}
	// Osty: toolchain/llvmgen.osty:3238:5
	if hasPatternOrDefault {
		// Osty: toolchain/llvmgen.osty:3239:9
		return "pattern/default parameters are not supported"
	}
	// Osty: toolchain/llvmgen.osty:3241:5
	if detail != "" {
		// Osty: toolchain/llvmgen.osty:3242:9
		return fmt.Sprintf("parameter \"%s\": %s", ostyToString(name), ostyToString(detail))
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:3247:5
func llvmFunctionReturnDiagnostic(name string, detail string) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:3248:5
	if detail != "" {
		// Osty: toolchain/llvmgen.osty:3249:9
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("function \"%s\" return type: %s", ostyToString(name), ostyToString(detail)))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: toolchain/llvmgen.osty:3254:5
func llvmFunctionParamDiagnostic(fnName string, paramName string, missingOrPattern bool, hasDefault bool, identOk bool, detail string) *LlvmUnsupportedDiagnostic {
	// Osty: toolchain/llvmgen.osty:3262:5
	if missingOrPattern {
		// Osty: toolchain/llvmgen.osty:3263:9
		return llvmUnsupportedDiagnostic("function-signature", fmt.Sprintf("function \"%s\" has non-identifier parameter", ostyToString(fnName)))
	}
	// Osty: toolchain/llvmgen.osty:3268:5
	if hasDefault {
		// Osty: toolchain/llvmgen.osty:3269:9
		return llvmUnsupportedDiagnostic("function-signature", fmt.Sprintf("function \"%s\" has default parameter values", ostyToString(fnName)))
	}
	// Osty: toolchain/llvmgen.osty:3274:5
	if !(identOk) {
		// Osty: toolchain/llvmgen.osty:3275:9
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("parameter name \"%s\"", ostyToString(paramName)))
	}
	// Osty: toolchain/llvmgen.osty:3277:5
	if detail != "" {
		// Osty: toolchain/llvmgen.osty:3278:9
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("function \"%s\" parameter \"%s\": %s", ostyToString(fnName), ostyToString(paramName), ostyToString(detail)))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: toolchain/llvmgen.osty:3286:5
func llvmSmokeExecutableCorpus() []*LlvmSmokeExecutableCase {
	return []*LlvmSmokeExecutableCase{&LlvmSmokeExecutableCase{name: "minimal", fixture: "minimal_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "scalar", fixture: "scalar_arithmetic.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "control", fixture: "control_flow.osty", stdout: "15\n"}, &LlvmSmokeExecutableCase{name: "booleans", fixture: "booleans.osty", stdout: "7\n"}, &LlvmSmokeExecutableCase{name: "string", fixture: "string_print.osty", stdout: "hello, osty\n"}, &LlvmSmokeExecutableCase{name: "string-escape", fixture: "string_escape_print.osty", stdout: "line one\nquote \" slash \\\n"}, &LlvmSmokeExecutableCase{name: "string-let", fixture: "string_let_print.osty", stdout: "stored string\n"}, &LlvmSmokeExecutableCase{name: "string-return", fixture: "string_return_print.osty", stdout: "from function\n"}, &LlvmSmokeExecutableCase{name: "string-param", fixture: "string_param_print.osty", stdout: "param string\n"}, &LlvmSmokeExecutableCase{name: "string-mut", fixture: "string_mut_print.osty", stdout: "after\n"}, &LlvmSmokeExecutableCase{name: "struct-field", fixture: "struct_field_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-return", fixture: "struct_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-param", fixture: "struct_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-mut", fixture: "struct_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-variant", fixture: "enum_variant_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-return", fixture: "enum_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-param", fixture: "enum_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-mut", fixture: "enum_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match", fixture: "enum_match_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match-return", fixture: "enum_match_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match-param", fixture: "enum_match_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match-mut", fixture: "enum_match_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload", fixture: "enum_payload_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload-return", fixture: "enum_payload_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload-param", fixture: "enum_payload_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload-mut", fixture: "enum_payload_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "float-print", fixture: "float_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-arithmetic", fixture: "float_arithmetic_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-return", fixture: "float_return_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-param", fixture: "float_param_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-mutable", fixture: "float_mut_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-compare", fixture: "float_compare_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-struct", fixture: "float_struct_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-enum-payload", fixture: "float_enum_payload_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-return", fixture: "float_payload_return_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-param", fixture: "float_payload_param_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-mut", fixture: "float_payload_mut_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-reversed", fixture: "float_payload_reversed_match_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-wildcard", fixture: "float_payload_wildcard_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "string-payload-return", fixture: "string_payload_return_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-param", fixture: "string_payload_param_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-mut", fixture: "string_payload_mut_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-reversed", fixture: "string_payload_reversed_match_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-wildcard", fixture: "string_payload_wildcard_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "int-if-expr", fixture: "int_if_expr_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "string-if-expr", fixture: "string_if_expr_print.osty", stdout: "chosen string\n"}, &LlvmSmokeExecutableCase{name: "float-if-expr", fixture: "float_if_expr_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "bool-param-return", fixture: "bool_param_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "int-range-exclusive", fixture: "int_range_exclusive_print.osty", stdout: "21\n"}, &LlvmSmokeExecutableCase{name: "int-unary", fixture: "int_unary_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "int-modulo", fixture: "int_modulo_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-string-field", fixture: "struct_string_field_print.osty", stdout: "struct string\n"}, &LlvmSmokeExecutableCase{name: "struct-bool-field", fixture: "struct_bool_field_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "bool-mut", fixture: "bool_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "result-question-int", fixture: "result_question_int_print.osty", stdout: "42\n"}}
}

// Osty: toolchain/llvmgen.osty:3566:5
func llvmSmokeMinimalPrintIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3567:5
	emitter := llvmEmitter()
	_ = emitter
	// Osty: toolchain/llvmgen.osty:3568:5
	value := llvmBinaryI64(emitter, "add", llvmIntLiteral(40), llvmIntLiteral(2))
	_ = value
	// Osty: toolchain/llvmgen.osty:3569:5
	llvmPrintlnI64(emitter, value)
	// Osty: toolchain/llvmgen.osty:3570:5
	llvmReturnI32Zero(emitter)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), emitter.body)})
}

// Osty: toolchain/llvmgen.osty:3580:5
func llvmSmokeGcRuntimeAbiIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3581:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3582:5
	object := llvmGcAlloc(main, 1, 32, "llvm.gc.object")
	_ = object
	// Osty: toolchain/llvmgen.osty:3583:5
	llvmGcRootBind(main, object)
	// Osty: toolchain/llvmgen.osty:3584:5
	child := llvmGcAlloc(main, 2, 16, "llvm.gc.child")
	_ = child
	// Osty: toolchain/llvmgen.osty:3585:5
	llvmGcPreWrite(main, object, child, 0)
	// Osty: toolchain/llvmgen.osty:3586:5
	loaded := llvmGcLoad(main, child)
	_ = loaded
	// Osty: toolchain/llvmgen.osty:3587:5
	llvmGcPostWrite(main, object, loaded, 0)
	// Osty: toolchain/llvmgen.osty:3588:5
	llvmGcRootRelease(main, object)
	// Osty: toolchain/llvmgen.osty:3589:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGcRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3609:5
func llvmSmokeClosureThunkIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3610:5
	doubleBody := llvmEmitter()
	_ = doubleBody
	// Osty: toolchain/llvmgen.osty:3611:5
	llvmBind(doubleBody, "x", &LlvmValue{typ: "i64", name: "%x", pointer: false})
	// Osty: toolchain/llvmgen.osty:3612:5
	doubled := llvmBinaryI64(doubleBody, "mul", llvmIdent(doubleBody, "x"), llvmIntLiteral(2))
	_ = doubled
	// Osty: toolchain/llvmgen.osty:3618:5
	llvmReturn(doubleBody, doubled)
	// Osty: toolchain/llvmgen.osty:3620:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3621:5
	env := llvmClosureBareFnEnv(main, "double_val")
	_ = env
	// Osty: toolchain/llvmgen.osty:3622:5
	envTypeName := llvmClosureEnvTypeName([]string{"ptr"})
	_ = envTypeName
	// Osty: toolchain/llvmgen.osty:3623:5
	result := llvmClosureCallIndirect(main, env, envTypeName, "i64", []*LlvmValue{llvmIntLiteral(21)})
	_ = result
	// Osty: toolchain/llvmgen.osty:3630:5
	llvmPrintlnI64(main, result)
	// Osty: toolchain/llvmgen.osty:3631:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmClosureBareFnEnvTypeDef()}, main.stringGlobals, []string{llvmRenderFunction("i64", "double_val", []*LlvmParam{llvmParam("x", "i64")}, doubleBody.body), llvmClosureThunkDefinition("double_val", "i64", []string{"i64"}), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3658:5
func llvmSmokeClosureBasicIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3659:5
	envTypeName := llvmClosureEnvTypeName([]string{"ptr", "i64"})
	_ = envTypeName
	// Osty: toolchain/llvmgen.osty:3660:5
	typeDef := llvmClosureEnvTypeDef(envTypeName, []string{"ptr", "i64"})
	_ = typeDef
	// Osty: toolchain/llvmgen.osty:3662:5
	body := llvmEmitter()
	_ = body
	// Osty: toolchain/llvmgen.osty:3663:5
	llvmBind(body, "env", &LlvmValue{typ: "ptr", name: "%env", pointer: false})
	// Osty: toolchain/llvmgen.osty:3664:5
	envArg := llvmIdent(body, "env")
	_ = envArg
	// Osty: toolchain/llvmgen.osty:3665:5
	captured := llvmClosureEnvLoadCapture(body, envArg, envTypeName, 1, "i64")
	_ = captured
	// Osty: toolchain/llvmgen.osty:3666:5
	llvmReturn(body, captured)
	// Osty: toolchain/llvmgen.osty:3668:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3669:5
	env := llvmClosureEnvAlloc(main, envTypeName)
	_ = env
	// Osty: toolchain/llvmgen.osty:3670:5
	llvmClosureEnvStoreFn(main, env, envTypeName, "closure_body")
	// Osty: toolchain/llvmgen.osty:3671:5
	llvmClosureEnvStoreCapture(main, env, envTypeName, 1, llvmIntLiteral(42))
	// Osty: toolchain/llvmgen.osty:3672:5
	result := llvmClosureCallIndirect(main, env, envTypeName, "i64", make([]*LlvmValue, 0, 1))
	_ = result
	// Osty: toolchain/llvmgen.osty:3673:5
	llvmPrintlnI64(main, result)
	// Osty: toolchain/llvmgen.osty:3674:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{typeDef}, main.stringGlobals, []string{llvmRenderFunction("i64", "closure_body", []*LlvmParam{llvmParam("env", "ptr")}, body.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3698:5
func llvmSmokeSetBasicIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3699:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3700:5
	set := llvmSetNew(main, llvmContainerAbiKind("i64", false))
	_ = set
	// Osty: toolchain/llvmgen.osty:3701:5
	_a := llvmSetInsertI64(main, set, llvmIntLiteral(10))
	_ = _a
	// Osty: toolchain/llvmgen.osty:3702:5
	_b := llvmSetInsertI64(main, set, llvmIntLiteral(20))
	_ = _b
	// Osty: toolchain/llvmgen.osty:3703:5
	_c := llvmSetInsertI64(main, set, llvmIntLiteral(30))
	_ = _c
	// Osty: toolchain/llvmgen.osty:3704:5
	_present := llvmSetContainsI64(main, set, llvmIntLiteral(20))
	_ = _present
	// Osty: toolchain/llvmgen.osty:3705:5
	_removed := llvmSetRemoveI64(main, set, llvmIntLiteral(20))
	_ = _removed
	// Osty: toolchain/llvmgen.osty:3706:5
	llvmPrintlnI64(main, llvmSetLen(main, set))
	// Osty: toolchain/llvmgen.osty:3707:5
	_keys := llvmSetToList(main, set)
	_ = _keys
	// Osty: toolchain/llvmgen.osty:3708:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithSetRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3726:5
func llvmSmokeStringConcatIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3727:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3728:5
	left := llvmStringLiteral(main, "hello, ")
	_ = left
	// Osty: toolchain/llvmgen.osty:3729:5
	right := llvmStringLiteral(main, "osty")
	_ = right
	// Osty: toolchain/llvmgen.osty:3730:5
	joined := llvmStringConcat(main, left, right)
	_ = joined
	// Osty: toolchain/llvmgen.osty:3731:5
	prefixed := llvmStringHasPrefix(main, joined, left)
	_ = prefixed
	// Osty: toolchain/llvmgen.osty:3732:5
	_unused := prefixed
	_ = _unused
	// Osty: toolchain/llvmgen.osty:3733:5
	length := llvmStringByteLen(main, joined)
	_ = length
	// Osty: toolchain/llvmgen.osty:3734:5
	llvmPrintlnI64(main, length)
	// Osty: toolchain/llvmgen.osty:3735:5
	llvmPrintlnString(main, joined)
	// Osty: toolchain/llvmgen.osty:3736:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithStringRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3754:5
func llvmSmokeMapBasicIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3755:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3756:5
	map_ := llvmMapNew(main)
	_ = map_
	// Osty: toolchain/llvmgen.osty:3757:5
	valueSlot := llvmMutableLetSlot(main, "value", llvmIntLiteral(42))
	_ = valueSlot
	// Osty: toolchain/llvmgen.osty:3758:5
	llvmMapInsertI64(main, map_, llvmIntLiteral(7), llvmSlotAsPtr(valueSlot))
	// Osty: toolchain/llvmgen.osty:3759:5
	_present := llvmMapContainsI64(main, map_, llvmIntLiteral(7))
	_ = _present
	// Osty: toolchain/llvmgen.osty:3760:5
	outSlot := llvmMutableLetSlot(main, "out", llvmIntLiteral(0))
	_ = outSlot
	// Osty: toolchain/llvmgen.osty:3761:5
	llvmMapGetOrAbortI64(main, map_, llvmIntLiteral(7), llvmSlotAsPtr(outSlot))
	// Osty: toolchain/llvmgen.osty:3762:5
	llvmPrintlnI64(main, llvmLoad(main, outSlot))
	// Osty: toolchain/llvmgen.osty:3763:5
	llvmPrintlnI64(main, llvmMapLen(main, map_))
	// Osty: toolchain/llvmgen.osty:3764:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithMapRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3781:5
func llvmSmokeListBasicIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3782:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3783:5
	list := llvmListNew(main)
	_ = list
	// Osty: toolchain/llvmgen.osty:3784:5
	llvmListPushI64(main, list, llvmIntLiteral(10))
	// Osty: toolchain/llvmgen.osty:3785:5
	llvmListPushI64(main, list, llvmIntLiteral(20))
	// Osty: toolchain/llvmgen.osty:3786:5
	llvmListPushI64(main, list, llvmIntLiteral(30))
	// Osty: toolchain/llvmgen.osty:3787:5
	len := llvmListLen(main, list)
	_ = len
	// Osty: toolchain/llvmgen.osty:3788:5
	llvmPrintlnI64(main, len)
	// Osty: toolchain/llvmgen.osty:3789:5
	second := llvmListGetI64(main, list, llvmIntLiteral(1))
	_ = second
	// Osty: toolchain/llvmgen.osty:3790:5
	llvmPrintlnI64(main, second)
	// Osty: toolchain/llvmgen.osty:3791:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithListRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3804:5
func llvmSmokeScalarArithmeticIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3805:5
	add := llvmEmitter()
	_ = add
	// Osty: toolchain/llvmgen.osty:3806:5
	llvmBind(add, "a", llvmI64("%a"))
	// Osty: toolchain/llvmgen.osty:3807:5
	llvmBind(add, "b", llvmI64("%b"))
	// Osty: toolchain/llvmgen.osty:3808:5
	sum := llvmBinaryI64(add, "add", llvmIdent(add, "a"), llvmIdent(add, "b"))
	_ = sum
	// Osty: toolchain/llvmgen.osty:3809:5
	llvmReturn(add, sum)
	// Osty: toolchain/llvmgen.osty:3811:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3812:5
	value := llvmCall(main, "i64", "add", []*LlvmValue{llvmIntLiteral(40), llvmIntLiteral(2)})
	_ = value
	// Osty: toolchain/llvmgen.osty:3813:5
	llvmImmutableLet(main, "value", value)
	// Osty: toolchain/llvmgen.osty:3814:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "value"), llvmIntLiteral(42))
	_ = cond
	// Osty: toolchain/llvmgen.osty:3815:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:3816:5
	llvmPrintlnI64(main, llvmIdent(main, "value"))
	// Osty: toolchain/llvmgen.osty:3817:5
	llvmIfElse(main, labels)
	// Osty: toolchain/llvmgen.osty:3818:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:3819:5
	llvmIfEnd(main, labels)
	// Osty: toolchain/llvmgen.osty:3820:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "add", []*LlvmParam{llvmParam("a", "i64"), llvmParam("b", "i64")}, add.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3840:5
func llvmSmokeControlFlowIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3841:5
	sumTo := llvmEmitter()
	_ = sumTo
	// Osty: toolchain/llvmgen.osty:3842:5
	llvmBind(sumTo, "n", llvmI64("%n"))
	// Osty: toolchain/llvmgen.osty:3843:5
	llvmMutableLet(sumTo, "total", llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:3844:5
	loop := llvmInclusiveRangeStart(sumTo, "i", llvmIntLiteral(1), llvmIdent(sumTo, "n"))
	_ = loop
	// Osty: toolchain/llvmgen.osty:3845:5
	nextTotal := llvmBinaryI64(sumTo, "add", llvmIdent(sumTo, "total"), llvmIdent(sumTo, "i"))
	_ = nextTotal
	// Osty: toolchain/llvmgen.osty:3846:5
	_ = llvmAssign(sumTo, "total", nextTotal)
	// Osty: toolchain/llvmgen.osty:3847:5
	llvmRangeEnd(sumTo, loop)
	// Osty: toolchain/llvmgen.osty:3848:5
	llvmReturn(sumTo, llvmIdent(sumTo, "total"))
	// Osty: toolchain/llvmgen.osty:3850:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3851:5
	value := llvmCall(main, "i64", "sumTo", []*LlvmValue{llvmIntLiteral(5)})
	_ = value
	// Osty: toolchain/llvmgen.osty:3852:5
	llvmPrintlnI64(main, value)
	// Osty: toolchain/llvmgen.osty:3853:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "sumTo", []*LlvmParam{llvmParam("n", "i64")}, sumTo.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3865:5
func llvmSmokeBooleansIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3866:5
	choose := llvmEmitter()
	_ = choose
	// Osty: toolchain/llvmgen.osty:3867:5
	llvmBind(choose, "a", llvmI64("%a"))
	// Osty: toolchain/llvmgen.osty:3868:5
	llvmBind(choose, "b", llvmI64("%b"))
	// Osty: toolchain/llvmgen.osty:3869:5
	lt := llvmCompare(choose, "slt", llvmIdent(choose, "a"), llvmIdent(choose, "b"))
	_ = lt
	// Osty: toolchain/llvmgen.osty:3870:5
	eqZero := llvmCompare(choose, "eq", llvmIdent(choose, "a"), llvmIntLiteral(0))
	_ = eqZero
	// Osty: toolchain/llvmgen.osty:3871:5
	nonZero := llvmNotI1(choose, eqZero)
	_ = nonZero
	// Osty: toolchain/llvmgen.osty:3872:5
	cond := llvmLogicalI1(choose, "and", lt, nonZero)
	_ = cond
	// Osty: toolchain/llvmgen.osty:3873:5
	labels := llvmIfExprStart(choose, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:3874:5
	thenValue := llvmBinaryI64(choose, "sub", llvmIdent(choose, "b"), llvmIdent(choose, "a"))
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:3875:5
	llvmIfExprElse(choose, labels)
	// Osty: toolchain/llvmgen.osty:3876:5
	elseValue := llvmBinaryI64(choose, "add", llvmIdent(choose, "a"), llvmIdent(choose, "b"))
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:3877:5
	result := llvmIfExprEnd(choose, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:3878:5
	llvmReturn(choose, result)
	// Osty: toolchain/llvmgen.osty:3880:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3881:5
	value := llvmCall(main, "i64", "choose", []*LlvmValue{llvmIntLiteral(3), llvmIntLiteral(10)})
	_ = value
	// Osty: toolchain/llvmgen.osty:3882:5
	llvmPrintlnI64(main, value)
	// Osty: toolchain/llvmgen.osty:3883:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "choose", []*LlvmParam{llvmParam("a", "i64"), llvmParam("b", "i64")}, choose.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3903:5
func llvmSmokeStringPrintIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3904:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3905:5
	line := llvmStringLiteral(main, "hello, osty")
	_ = line
	// Osty: toolchain/llvmgen.osty:3906:5
	llvmPrintlnString(main, line)
	// Osty: toolchain/llvmgen.osty:3907:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3919:5
func llvmSmokeStringEscapeIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3920:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3921:5
	line := llvmStringLiteral(main, "line one\nquote \" slash \\")
	_ = line
	// Osty: toolchain/llvmgen.osty:3922:5
	llvmPrintlnString(main, line)
	// Osty: toolchain/llvmgen.osty:3923:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3935:5
func llvmSmokeStringLetIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3936:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3937:5
	msg := llvmStringLiteral(main, "stored string")
	_ = msg
	// Osty: toolchain/llvmgen.osty:3938:5
	llvmImmutableLet(main, "msg", msg)
	// Osty: toolchain/llvmgen.osty:3939:5
	llvmPrintlnString(main, llvmIdent(main, "msg"))
	// Osty: toolchain/llvmgen.osty:3940:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3952:5
func llvmSmokeStringReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3953:5
	greet := llvmEmitter()
	_ = greet
	// Osty: toolchain/llvmgen.osty:3954:5
	llvmReturn(greet, llvmStringLiteral(greet, "from function"))
	// Osty: toolchain/llvmgen.osty:3956:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3957:5
	llvmPrintlnString(main, llvmCall(main, "ptr", "greet", make([]*LlvmValue, 0, 1)))
	// Osty: toolchain/llvmgen.osty:3958:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", greet.stringGlobals, []string{llvmRenderFunction("ptr", "greet", make([]*LlvmParam, 0, 1), greet.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3971:5
func llvmSmokeStringParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3972:5
	echo := llvmEmitter()
	_ = echo
	// Osty: toolchain/llvmgen.osty:3973:5
	llvmBind(echo, "msg", &LlvmValue{typ: "ptr", name: "%msg", pointer: false})
	// Osty: toolchain/llvmgen.osty:3974:5
	llvmReturn(echo, llvmIdent(echo, "msg"))
	// Osty: toolchain/llvmgen.osty:3976:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3977:5
	value := llvmCall(main, "ptr", "echo", []*LlvmValue{llvmStringLiteral(main, "param string")})
	_ = value
	// Osty: toolchain/llvmgen.osty:3978:5
	llvmPrintlnString(main, value)
	// Osty: toolchain/llvmgen.osty:3979:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("ptr", "echo", []*LlvmParam{llvmParam("msg", "ptr")}, echo.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:3992:5
func llvmSmokeStringMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:3993:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:3994:5
	llvmMutableLet(main, "msg", llvmStringLiteral(main, "before"))
	// Osty: toolchain/llvmgen.osty:3995:5
	_ = llvmAssign(main, "msg", llvmStringLiteral(main, "after"))
	// Osty: toolchain/llvmgen.osty:3996:5
	llvmPrintlnString(main, llvmIdent(main, "msg"))
	// Osty: toolchain/llvmgen.osty:3997:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4009:5
func llvmSmokeStructFieldIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4010:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4011:5
	point := llvmStructLiteral(main, "%Point", []*LlvmValue{llvmIntLiteral(40), llvmIntLiteral(2)})
	_ = point
	// Osty: toolchain/llvmgen.osty:4012:5
	llvmImmutableLet(main, "point", point)
	// Osty: toolchain/llvmgen.osty:4013:5
	sum := llvmBinaryI64(main, "add", llvmExtractValue(main, llvmIdent(main, "point"), "i64", 0), llvmExtractValue(main, llvmIdent(main, "point"), "i64", 1))
	_ = sum
	// Osty: toolchain/llvmgen.osty:4019:5
	llvmPrintlnI64(main, sum)
	// Osty: toolchain/llvmgen.osty:4020:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Point", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4035:5
func llvmSmokeStructReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4036:5
	makePair := llvmEmitter()
	_ = makePair
	// Osty: toolchain/llvmgen.osty:4037:5
	pair := llvmStructLiteral(makePair, "%Pair", []*LlvmValue{llvmIntLiteral(10), llvmIntLiteral(32)})
	_ = pair
	// Osty: toolchain/llvmgen.osty:4038:5
	llvmReturn(makePair, pair)
	// Osty: toolchain/llvmgen.osty:4040:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4041:5
	returned := llvmCall(main, "%Pair", "makePair", make([]*LlvmValue, 0, 1))
	_ = returned
	// Osty: toolchain/llvmgen.osty:4042:5
	llvmImmutableLet(main, "pair", returned)
	// Osty: toolchain/llvmgen.osty:4043:5
	sum := llvmBinaryI64(main, "add", llvmExtractValue(main, llvmIdent(main, "pair"), "i64", 0), llvmExtractValue(main, llvmIdent(main, "pair"), "i64", 1))
	_ = sum
	// Osty: toolchain/llvmgen.osty:4049:5
	llvmPrintlnI64(main, sum)
	// Osty: toolchain/llvmgen.osty:4050:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Pair", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%Pair", "makePair", make([]*LlvmParam, 0, 1), makePair.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4066:5
func llvmSmokeStructParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4067:5
	total := llvmEmitter()
	_ = total
	// Osty: toolchain/llvmgen.osty:4068:5
	llvmBind(total, "score", &LlvmValue{typ: "%Score", name: "%score", pointer: false})
	// Osty: toolchain/llvmgen.osty:4069:5
	sum := llvmBinaryI64(total, "add", llvmExtractValue(total, llvmIdent(total, "score"), "i64", 0), llvmExtractValue(total, llvmIdent(total, "score"), "i64", 1))
	_ = sum
	// Osty: toolchain/llvmgen.osty:4075:5
	llvmReturn(total, sum)
	// Osty: toolchain/llvmgen.osty:4077:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4078:5
	score := llvmStructLiteral(main, "%Score", []*LlvmValue{llvmIntLiteral(40), llvmIntLiteral(2)})
	_ = score
	// Osty: toolchain/llvmgen.osty:4079:5
	out := llvmCall(main, "i64", "total", []*LlvmValue{score})
	_ = out
	// Osty: toolchain/llvmgen.osty:4080:5
	llvmPrintlnI64(main, out)
	// Osty: toolchain/llvmgen.osty:4081:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Score", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i64", "total", []*LlvmParam{llvmParam("score", "%Score")}, total.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4097:5
func llvmSmokeStructMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4098:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4099:5
	llvmMutableLet(main, "box", llvmStructLiteral(main, "%Box", []*LlvmValue{llvmIntLiteral(1)}))
	// Osty: toolchain/llvmgen.osty:4100:5
	_ = llvmAssign(main, "box", llvmStructLiteral(main, "%Box", []*LlvmValue{llvmIntLiteral(42)}))
	// Osty: toolchain/llvmgen.osty:4101:5
	llvmPrintlnI64(main, llvmExtractValue(main, llvmIdent(main, "box"), "i64", 0))
	// Osty: toolchain/llvmgen.osty:4102:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Box", []string{"i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4117:5
func llvmSmokeEnumVariantIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4118:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4119:5
	llvmImmutableLet(main, "light", llvmEnumVariant("Light", 1))
	// Osty: toolchain/llvmgen.osty:4120:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "light"), llvmEnumVariant("Light", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4121:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4122:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: toolchain/llvmgen.osty:4123:5
	llvmIfElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4124:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:4125:5
	llvmIfEnd(main, labels)
	// Osty: toolchain/llvmgen.osty:4126:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4137:5
func llvmSmokeEnumReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4138:5
	pick := llvmEmitter()
	_ = pick
	// Osty: toolchain/llvmgen.osty:4139:5
	llvmReturn(pick, llvmEnumVariant("Switch", 1))
	// Osty: toolchain/llvmgen.osty:4141:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4142:5
	state := llvmCall(main, "i64", "pick", make([]*LlvmValue, 0, 1))
	_ = state
	// Osty: toolchain/llvmgen.osty:4143:5
	cond := llvmCompare(main, "eq", state, llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4144:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4145:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: toolchain/llvmgen.osty:4146:5
	llvmIfElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4147:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:4148:5
	llvmIfEnd(main, labels)
	// Osty: toolchain/llvmgen.osty:4149:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4161:5
func llvmSmokeEnumParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4162:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4163:5
	llvmBind(score, "state", llvmI64("%state"))
	// Osty: toolchain/llvmgen.osty:4164:5
	cond := llvmCompare(score, "eq", llvmIdent(score, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4165:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4166:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4167:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4168:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4169:5
	out := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4170:5
	llvmReturn(score, out)
	// Osty: toolchain/llvmgen.osty:4172:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4173:5
	result := llvmCall(main, "i64", "score", []*LlvmValue{llvmEnumVariant("Switch", 1)})
	_ = result
	// Osty: toolchain/llvmgen.osty:4174:5
	llvmPrintlnI64(main, result)
	// Osty: toolchain/llvmgen.osty:4175:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "score", []*LlvmParam{llvmParam("state", "i64")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4187:5
func llvmSmokeEnumMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4188:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4189:5
	llvmMutableLet(main, "state", llvmEnumVariant("Switch", 0))
	// Osty: toolchain/llvmgen.osty:4190:5
	_ = llvmAssign(main, "state", llvmEnumVariant("Switch", 1))
	// Osty: toolchain/llvmgen.osty:4191:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4192:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4193:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: toolchain/llvmgen.osty:4194:5
	llvmIfElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4195:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:4196:5
	llvmIfEnd(main, labels)
	// Osty: toolchain/llvmgen.osty:4197:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4208:5
func llvmSmokeEnumMatchIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4209:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4210:5
	llvmImmutableLet(main, "state", llvmEnumVariant("Switch", 1))
	// Osty: toolchain/llvmgen.osty:4211:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4212:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4213:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4214:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4215:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4216:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4217:5
	llvmPrintlnI64(main, result)
	// Osty: toolchain/llvmgen.osty:4218:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4229:5
func llvmSmokeEnumMatchReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4230:5
	pick := llvmEmitter()
	_ = pick
	// Osty: toolchain/llvmgen.osty:4231:5
	llvmReturn(pick, llvmEnumVariant("Switch", 1))
	// Osty: toolchain/llvmgen.osty:4233:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4234:5
	state := llvmCall(score, "i64", "pick", make([]*LlvmValue, 0, 1))
	_ = state
	// Osty: toolchain/llvmgen.osty:4235:5
	cond := llvmCompare(score, "eq", state, llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4236:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4237:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4238:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4239:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4240:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4241:5
	llvmReturn(score, result)
	// Osty: toolchain/llvmgen.osty:4243:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4244:5
	out := llvmCall(main, "i64", "score", make([]*LlvmValue, 0, 1))
	_ = out
	// Osty: toolchain/llvmgen.osty:4245:5
	llvmPrintlnI64(main, out)
	// Osty: toolchain/llvmgen.osty:4246:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("i64", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4259:5
func llvmSmokeEnumMatchParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4260:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4261:5
	llvmBind(score, "state", llvmI64("%state"))
	// Osty: toolchain/llvmgen.osty:4262:5
	cond := llvmCompare(score, "eq", llvmIdent(score, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4263:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4264:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4265:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4266:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4267:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4268:5
	llvmReturn(score, result)
	// Osty: toolchain/llvmgen.osty:4270:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4271:5
	out := llvmCall(main, "i64", "score", []*LlvmValue{llvmEnumVariant("Switch", 1)})
	_ = out
	// Osty: toolchain/llvmgen.osty:4272:5
	llvmPrintlnI64(main, out)
	// Osty: toolchain/llvmgen.osty:4273:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "score", []*LlvmParam{llvmParam("state", "i64")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4285:5
func llvmSmokeEnumMatchMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4286:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4287:5
	llvmMutableLet(main, "state", llvmEnumVariant("Switch", 0))
	// Osty: toolchain/llvmgen.osty:4288:5
	_ = llvmAssign(main, "state", llvmEnumVariant("Switch", 1))
	// Osty: toolchain/llvmgen.osty:4289:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4290:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4291:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4292:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4293:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4294:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4295:5
	llvmPrintlnI64(main, result)
	// Osty: toolchain/llvmgen.osty:4296:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4307:5
func llvmSmokeEnumPayloadIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4308:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4309:5
	llvmImmutableLet(main, "value", llvmEnumPayloadVariant(main, "%Maybe", 0, llvmIntLiteral(42)))
	// Osty: toolchain/llvmgen.osty:4310:5
	state := llvmIdent(main, "value")
	_ = state
	// Osty: toolchain/llvmgen.osty:4311:5
	tag := llvmExtractValue(main, state, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4312:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4313:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4314:5
	thenValue := llvmExtractValue(main, state, "i64", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4315:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4316:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4317:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4318:5
	llvmPrintlnI64(main, result)
	// Osty: toolchain/llvmgen.osty:4319:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4334:5
func llvmSmokeEnumPayloadReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4335:5
	pick := llvmEmitter()
	_ = pick
	// Osty: toolchain/llvmgen.osty:4336:5
	llvmReturn(pick, llvmEnumPayloadVariant(pick, "%Maybe", 0, llvmIntLiteral(42)))
	// Osty: toolchain/llvmgen.osty:4338:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4339:5
	state := llvmCall(score, "%Maybe", "pick", make([]*LlvmValue, 0, 1))
	_ = state
	// Osty: toolchain/llvmgen.osty:4340:5
	tag := llvmExtractValue(score, state, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4341:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4342:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4343:5
	thenValue := llvmExtractValue(score, state, "i64", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4344:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4345:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4346:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4347:5
	llvmReturn(score, result)
	// Osty: toolchain/llvmgen.osty:4349:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4350:5
	out := llvmCall(main, "i64", "score", make([]*LlvmValue, 0, 1))
	_ = out
	// Osty: toolchain/llvmgen.osty:4351:5
	llvmPrintlnI64(main, out)
	// Osty: toolchain/llvmgen.osty:4352:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%Maybe", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("i64", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4369:5
func llvmSmokeEnumPayloadParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4370:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4371:5
	llvmBind(score, "value", &LlvmValue{typ: "%Maybe", name: "%value", pointer: false})
	// Osty: toolchain/llvmgen.osty:4372:5
	state := llvmIdent(score, "value")
	_ = state
	// Osty: toolchain/llvmgen.osty:4373:5
	tag := llvmExtractValue(score, state, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4374:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4375:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4376:5
	thenValue := llvmExtractValue(score, state, "i64", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4377:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4378:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4379:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4380:5
	llvmReturn(score, result)
	// Osty: toolchain/llvmgen.osty:4382:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4383:5
	arg := llvmEnumPayloadVariant(main, "%Maybe", 0, llvmIntLiteral(42))
	_ = arg
	// Osty: toolchain/llvmgen.osty:4384:5
	out := llvmCall(main, "i64", "score", []*LlvmValue{arg})
	_ = out
	// Osty: toolchain/llvmgen.osty:4385:5
	llvmPrintlnI64(main, out)
	// Osty: toolchain/llvmgen.osty:4386:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i64", "score", []*LlvmParam{llvmParam("value", "%Maybe")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4402:5
func llvmSmokeEnumPayloadMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4403:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4404:5
	llvmMutableLet(main, "value", llvmEnumPayloadVariant(main, "%Maybe", 1, llvmIntLiteral(0)))
	// Osty: toolchain/llvmgen.osty:4405:5
	_ = llvmAssign(main, "value", llvmEnumPayloadVariant(main, "%Maybe", 0, llvmIntLiteral(42)))
	// Osty: toolchain/llvmgen.osty:4406:5
	state := llvmIdent(main, "value")
	_ = state
	// Osty: toolchain/llvmgen.osty:4407:5
	tag := llvmExtractValue(main, state, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4408:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4409:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4410:5
	thenValue := llvmExtractValue(main, state, "i64", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4411:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4412:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4413:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: toolchain/llvmgen.osty:4414:5
	llvmPrintlnI64(main, result)
	// Osty: toolchain/llvmgen.osty:4415:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4430:5
func llvmSmokeFloatPrintIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4431:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4432:5
	llvmPrintlnF64(main, llvmFloatLiteral("42.0"))
	// Osty: toolchain/llvmgen.osty:4433:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4444:5
func llvmSmokeFloatArithmeticIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4445:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4446:5
	add := llvmBinaryF64(main, "fadd", llvmFloatLiteral("40.0"), llvmFloatLiteral("2.0"))
	_ = add
	// Osty: toolchain/llvmgen.osty:4447:5
	sub := llvmBinaryF64(main, "fsub", add, llvmFloatLiteral("0.0"))
	_ = sub
	// Osty: toolchain/llvmgen.osty:4448:5
	mul := llvmBinaryF64(main, "fmul", sub, llvmFloatLiteral("2.5"))
	_ = mul
	// Osty: toolchain/llvmgen.osty:4449:5
	div := llvmBinaryF64(main, "fdiv", mul, llvmFloatLiteral("2.5"))
	_ = div
	// Osty: toolchain/llvmgen.osty:4450:5
	llvmPrintlnF64(main, div)
	// Osty: toolchain/llvmgen.osty:4451:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4462:5
func llvmSmokeFloatReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4463:5
	value := llvmEmitter()
	_ = value
	// Osty: toolchain/llvmgen.osty:4464:5
	llvmReturn(value, llvmFloatLiteral("42.0"))
	// Osty: toolchain/llvmgen.osty:4466:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4467:5
	out := llvmCall(main, "double", "value", make([]*LlvmValue, 0, 1))
	_ = out
	// Osty: toolchain/llvmgen.osty:4468:5
	llvmPrintlnF64(main, out)
	// Osty: toolchain/llvmgen.osty:4469:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("double", "value", make([]*LlvmParam, 0, 1), value.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4481:5
func llvmSmokeFloatParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4482:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4483:5
	llvmBind(score, "value", llvmF64("%value"))
	// Osty: toolchain/llvmgen.osty:4484:5
	llvmReturn(score, llvmIdent(score, "value"))
	// Osty: toolchain/llvmgen.osty:4486:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4487:5
	out := llvmCall(main, "double", "score", []*LlvmValue{llvmFloatLiteral("42.0")})
	_ = out
	// Osty: toolchain/llvmgen.osty:4488:5
	llvmPrintlnF64(main, out)
	// Osty: toolchain/llvmgen.osty:4489:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("double", "score", []*LlvmParam{llvmParam("value", "double")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4501:5
func llvmSmokeFloatMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4502:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4503:5
	llvmMutableLet(main, "value", llvmFloatLiteral("0.0"))
	// Osty: toolchain/llvmgen.osty:4504:5
	_ = llvmAssign(main, "value", llvmFloatLiteral("42.0"))
	// Osty: toolchain/llvmgen.osty:4505:5
	llvmPrintlnF64(main, llvmIdent(main, "value"))
	// Osty: toolchain/llvmgen.osty:4506:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4517:5
func llvmSmokeFloatCompareIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4518:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4519:5
	value := llvmFloatLiteral("42.0")
	_ = value
	// Osty: toolchain/llvmgen.osty:4520:5
	eq := llvmCompareF64(main, "oeq", value, llvmFloatLiteral("42.0"))
	_ = eq
	// Osty: toolchain/llvmgen.osty:4521:5
	ne := llvmCompareF64(main, "one", value, llvmFloatLiteral("41.0"))
	_ = ne
	// Osty: toolchain/llvmgen.osty:4522:5
	lt := llvmCompareF64(main, "olt", value, llvmFloatLiteral("100.0"))
	_ = lt
	// Osty: toolchain/llvmgen.osty:4523:5
	gt := llvmCompareF64(main, "ogt", value, llvmFloatLiteral("0.0"))
	_ = gt
	// Osty: toolchain/llvmgen.osty:4524:5
	le := llvmCompareF64(main, "ole", value, llvmFloatLiteral("42.0"))
	_ = le
	// Osty: toolchain/llvmgen.osty:4525:5
	ge := llvmCompareF64(main, "oge", value, llvmFloatLiteral("42.0"))
	_ = ge
	// Osty: toolchain/llvmgen.osty:4526:5
	cond := llvmLogicalI1(main, "and", llvmLogicalI1(main, "and", llvmLogicalI1(main, "and", eq, ne), llvmLogicalI1(main, "and", lt, gt)), llvmLogicalI1(main, "and", le, ge))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4537:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4538:5
	llvmPrintlnF64(main, value)
	// Osty: toolchain/llvmgen.osty:4539:5
	llvmIfElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4540:5
	llvmPrintlnF64(main, llvmFloatLiteral("0.0"))
	// Osty: toolchain/llvmgen.osty:4541:5
	llvmIfEnd(main, labels)
	// Osty: toolchain/llvmgen.osty:4542:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4553:5
func llvmSmokeFloatStructIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4554:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4555:5
	value := llvmStructLiteral(main, "%MaybeF", []*LlvmValue{llvmIntLiteral(7), llvmFloatLiteral("42.0")})
	_ = value
	// Osty: toolchain/llvmgen.osty:4556:5
	llvmImmutableLet(main, "value", value)
	// Osty: toolchain/llvmgen.osty:4557:5
	llvmPrintlnF64(main, llvmExtractValue(main, llvmIdent(main, "value"), "double", 1))
	// Osty: toolchain/llvmgen.osty:4558:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("MaybeF", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4573:5
func llvmSmokeFloatEnumPayloadIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4574:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4575:5
	value := llvmEnumPayloadVariant(main, "%MaybeF", 0, llvmFloatLiteral("42.0"))
	_ = value
	// Osty: toolchain/llvmgen.osty:4576:5
	llvmImmutableLet(main, "value", value)
	// Osty: toolchain/llvmgen.osty:4577:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4578:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4579:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("MaybeF", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4580:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4581:5
	thenValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4582:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4583:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4584:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4585:5
	llvmPrintlnF64(main, out)
	// Osty: toolchain/llvmgen.osty:4586:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("MaybeF", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4601:5
func llvmSmokeFloatPayloadReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4602:5
	pick := llvmEmitter()
	_ = pick
	// Osty: toolchain/llvmgen.osty:4603:5
	llvmReturn(pick, llvmEnumPayloadVariant(pick, "%FloatMaybe", 0, llvmFloatLiteral("42.0")))
	// Osty: toolchain/llvmgen.osty:4605:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4606:5
	valueRef := llvmCall(score, "%FloatMaybe", "pick", make([]*LlvmValue, 0, 1))
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4607:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4608:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4609:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4610:5
	thenValue := llvmExtractValue(score, valueRef, "double", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4611:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4612:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4613:5
	out := llvmIfExprEnd(score, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4614:5
	llvmReturn(score, out)
	// Osty: toolchain/llvmgen.osty:4616:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4617:5
	value := llvmCall(main, "double", "score", make([]*LlvmValue, 0, 1))
	_ = value
	// Osty: toolchain/llvmgen.osty:4618:5
	llvmPrintlnF64(main, value)
	// Osty: toolchain/llvmgen.osty:4619:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%FloatMaybe", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("double", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4636:5
func llvmSmokeFloatPayloadParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4637:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4638:5
	llvmBind(score, "value", &LlvmValue{typ: "%FloatMaybe", name: "%value", pointer: false})
	// Osty: toolchain/llvmgen.osty:4639:5
	valueRef := llvmIdent(score, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4640:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4641:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4642:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4643:5
	thenValue := llvmExtractValue(score, valueRef, "double", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4644:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4645:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4646:5
	out := llvmIfExprEnd(score, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4647:5
	llvmReturn(score, out)
	// Osty: toolchain/llvmgen.osty:4649:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4650:5
	arg := llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0"))
	_ = arg
	// Osty: toolchain/llvmgen.osty:4651:5
	mainOut := llvmCall(main, "double", "score", []*LlvmValue{arg})
	_ = mainOut
	// Osty: toolchain/llvmgen.osty:4652:5
	llvmPrintlnF64(main, mainOut)
	// Osty: toolchain/llvmgen.osty:4653:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("double", "score", []*LlvmParam{llvmParam("value", "%FloatMaybe")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4669:5
func llvmSmokeFloatPayloadMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4670:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4671:5
	llvmMutableLet(main, "value", llvmEnumPayloadVariant(main, "%FloatMaybe", 1, llvmFloatLiteral("0.0")))
	// Osty: toolchain/llvmgen.osty:4676:5
	_ = llvmAssign(main, "value", llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0")))
	// Osty: toolchain/llvmgen.osty:4681:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4682:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4683:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4684:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4685:5
	thenValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4686:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4687:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4688:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4689:5
	llvmPrintlnF64(main, out)
	// Osty: toolchain/llvmgen.osty:4690:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4705:5
func llvmSmokeFloatPayloadReversedMatchIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4706:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4707:5
	value := llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0"))
	_ = value
	// Osty: toolchain/llvmgen.osty:4708:5
	llvmImmutableLet(main, "value", value)
	// Osty: toolchain/llvmgen.osty:4709:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4710:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4711:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("FloatMaybe", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4712:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4713:5
	thenValue := llvmFloatLiteral("0.0")
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4714:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4715:5
	elseValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4716:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4717:5
	llvmPrintlnF64(main, out)
	// Osty: toolchain/llvmgen.osty:4718:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4733:5
func llvmSmokeFloatPayloadWildcardIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4734:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4735:5
	value := llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0"))
	_ = value
	// Osty: toolchain/llvmgen.osty:4736:5
	llvmImmutableLet(main, "value", value)
	// Osty: toolchain/llvmgen.osty:4737:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4738:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4739:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4740:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4741:5
	thenValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4742:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4743:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4744:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4745:5
	llvmPrintlnF64(main, out)
	// Osty: toolchain/llvmgen.osty:4746:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4761:5
func llvmSmokeStringPayloadReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4762:5
	pick := llvmEmitter()
	_ = pick
	// Osty: toolchain/llvmgen.osty:4763:5
	llvmReturn(pick, llvmEnumPayloadVariant(pick, "%Label", 0, llvmStringLiteral(pick, "payload string")))
	// Osty: toolchain/llvmgen.osty:4768:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4769:5
	valueRef := llvmCall(score, "%Label", "pick", make([]*LlvmValue, 0, 1))
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4770:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4771:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4772:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4773:5
	thenValue := llvmExtractValue(score, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4774:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4775:5
	elseValue := llvmStringLiteral(score, "no payload")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4776:5
	out := llvmIfExprEnd(score, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4777:5
	llvmReturn(score, out)
	// Osty: toolchain/llvmgen.osty:4779:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4780:5
	value := llvmCall(main, "ptr", "score", make([]*LlvmValue, 0, 1))
	_ = value
	// Osty: toolchain/llvmgen.osty:4781:5
	llvmPrintlnString(main, value)
	// Osty: toolchain/llvmgen.osty:4782:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%Label", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("ptr", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4799:5
func llvmSmokeStringPayloadParamIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4800:5
	score := llvmEmitter()
	_ = score
	// Osty: toolchain/llvmgen.osty:4801:5
	llvmBind(score, "value", &LlvmValue{typ: "%Label", name: "%value", pointer: false})
	// Osty: toolchain/llvmgen.osty:4802:5
	valueRef := llvmIdent(score, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4803:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4804:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4805:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4806:5
	thenValue := llvmExtractValue(score, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4807:5
	llvmIfExprElse(score, labels)
	// Osty: toolchain/llvmgen.osty:4808:5
	elseValue := llvmStringLiteral(score, "no payload")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4809:5
	out := llvmIfExprEnd(score, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4810:5
	llvmReturn(score, out)
	// Osty: toolchain/llvmgen.osty:4812:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4813:5
	arg := llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string"))
	_ = arg
	// Osty: toolchain/llvmgen.osty:4814:5
	value := llvmCall(main, "ptr", "score", []*LlvmValue{arg})
	_ = value
	// Osty: toolchain/llvmgen.osty:4815:5
	llvmPrintlnString(main, value)
	// Osty: toolchain/llvmgen.osty:4816:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("ptr", "score", []*LlvmParam{llvmParam("value", "%Label")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4832:5
func llvmSmokeStringPayloadMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4833:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4834:5
	llvmMutableLet(main, "value", llvmEnumPayloadVariant(main, "%Label", 1, llvmStringLiteral(main, "no payload")))
	// Osty: toolchain/llvmgen.osty:4839:5
	_ = llvmAssign(main, "value", llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string")))
	// Osty: toolchain/llvmgen.osty:4844:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4845:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4846:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4847:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4848:5
	thenValue := llvmExtractValue(main, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4849:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4850:5
	elseValue := llvmStringLiteral(main, "no payload")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4851:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4852:5
	llvmPrintlnString(main, out)
	// Osty: toolchain/llvmgen.osty:4853:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4868:5
func llvmSmokeStringPayloadReversedMatchIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4869:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4870:5
	value := llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string"))
	_ = value
	// Osty: toolchain/llvmgen.osty:4871:5
	llvmImmutableLet(main, "value", value)
	// Osty: toolchain/llvmgen.osty:4872:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4873:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4874:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Label", 1))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4875:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4876:5
	thenValue := llvmStringLiteral(main, "no payload")
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4877:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4878:5
	elseValue := llvmExtractValue(main, valueRef, "ptr", 1)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4879:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4880:5
	llvmPrintlnString(main, out)
	// Osty: toolchain/llvmgen.osty:4881:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4896:5
func llvmSmokeStringPayloadWildcardIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4897:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4898:5
	value := llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string"))
	_ = value
	// Osty: toolchain/llvmgen.osty:4899:5
	llvmImmutableLet(main, "value", value)
	// Osty: toolchain/llvmgen.osty:4900:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: toolchain/llvmgen.osty:4901:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: toolchain/llvmgen.osty:4902:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: toolchain/llvmgen.osty:4903:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:4904:5
	thenValue := llvmExtractValue(main, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4905:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4906:5
	elseValue := llvmStringLiteral(main, "no payload")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4907:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4908:5
	llvmPrintlnString(main, out)
	// Osty: toolchain/llvmgen.osty:4909:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4924:5
func llvmSmokeIntIfExprIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4925:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4926:5
	labels := llvmIfExprStart(main, llvmI1("true"))
	_ = labels
	// Osty: toolchain/llvmgen.osty:4927:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4928:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4929:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4930:5
	out := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4931:5
	llvmPrintlnI64(main, out)
	// Osty: toolchain/llvmgen.osty:4932:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4943:5
func llvmSmokeStringIfExprIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4944:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4945:5
	labels := llvmIfExprStart(main, llvmI1("true"))
	_ = labels
	// Osty: toolchain/llvmgen.osty:4946:5
	thenValue := llvmStringLiteral(main, "chosen string")
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4947:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4948:5
	elseValue := llvmStringLiteral(main, "fallback")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4949:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4950:5
	llvmPrintlnString(main, out)
	// Osty: toolchain/llvmgen.osty:4951:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4963:5
func llvmSmokeFloatIfExprIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4964:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4965:5
	labels := llvmIfExprStart(main, llvmI1("true"))
	_ = labels
	// Osty: toolchain/llvmgen.osty:4966:5
	thenValue := llvmFloatLiteral("42.0")
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4967:5
	llvmIfExprElse(main, labels)
	// Osty: toolchain/llvmgen.osty:4968:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4969:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4970:5
	llvmPrintlnF64(main, out)
	// Osty: toolchain/llvmgen.osty:4971:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:4982:5
func llvmSmokeBoolParamReturnIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:4983:5
	pick := llvmEmitter()
	_ = pick
	// Osty: toolchain/llvmgen.osty:4984:5
	llvmBind(pick, "flag", llvmI1("%flag"))
	// Osty: toolchain/llvmgen.osty:4985:5
	labels := llvmIfExprStart(pick, llvmIdent(pick, "flag"))
	_ = labels
	// Osty: toolchain/llvmgen.osty:4986:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: toolchain/llvmgen.osty:4987:5
	llvmIfExprElse(pick, labels)
	// Osty: toolchain/llvmgen.osty:4988:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: toolchain/llvmgen.osty:4989:5
	out := llvmIfExprEnd(pick, "i64", thenValue, elseValue, labels)
	_ = out
	// Osty: toolchain/llvmgen.osty:4990:5
	llvmReturn(pick, out)
	// Osty: toolchain/llvmgen.osty:4992:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:4993:5
	value := llvmCall(main, "i64", "pick", []*LlvmValue{llvmI1("true")})
	_ = value
	// Osty: toolchain/llvmgen.osty:4994:5
	llvmPrintlnI64(main, value)
	// Osty: toolchain/llvmgen.osty:4995:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "pick", []*LlvmParam{llvmParam("flag", "i1")}, pick.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:5007:5
func llvmSmokeIntRangeExclusiveIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:5008:5
	sumTo := llvmEmitter()
	_ = sumTo
	// Osty: toolchain/llvmgen.osty:5009:5
	llvmBind(sumTo, "n", llvmI64("%n"))
	// Osty: toolchain/llvmgen.osty:5010:5
	llvmMutableLet(sumTo, "total", llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:5011:5
	loop := llvmRangeStart(sumTo, "i", llvmIntLiteral(0), llvmIdent(sumTo, "n"), false)
	_ = loop
	// Osty: toolchain/llvmgen.osty:5012:5
	nextTotal := llvmBinaryI64(sumTo, "add", llvmIdent(sumTo, "total"), llvmIdent(sumTo, "i"))
	_ = nextTotal
	// Osty: toolchain/llvmgen.osty:5013:5
	_ = llvmAssign(sumTo, "total", nextTotal)
	// Osty: toolchain/llvmgen.osty:5014:5
	llvmRangeEnd(sumTo, loop)
	// Osty: toolchain/llvmgen.osty:5015:5
	llvmReturn(sumTo, llvmIdent(sumTo, "total"))
	// Osty: toolchain/llvmgen.osty:5017:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:5018:5
	value := llvmCall(main, "i64", "sumTo", []*LlvmValue{llvmIntLiteral(7)})
	_ = value
	// Osty: toolchain/llvmgen.osty:5019:5
	llvmPrintlnI64(main, value)
	// Osty: toolchain/llvmgen.osty:5020:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "sumTo", []*LlvmParam{llvmParam("n", "i64")}, sumTo.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:5032:5
func llvmSmokeIntUnaryIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:5033:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:5034:5
	diff := llvmBinaryI64(main, "sub", llvmIntLiteral(40), llvmIntLiteral(82))
	_ = diff
	// Osty: toolchain/llvmgen.osty:5035:5
	value := llvmBinaryI64(main, "sub", llvmIntLiteral(0), diff)
	_ = value
	// Osty: toolchain/llvmgen.osty:5036:5
	llvmPrintlnI64(main, value)
	// Osty: toolchain/llvmgen.osty:5037:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:5048:5
func llvmSmokeIntModuloIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:5049:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:5050:5
	value := llvmBinaryI64(main, "srem", llvmIntLiteral(85), llvmIntLiteral(43))
	_ = value
	// Osty: toolchain/llvmgen.osty:5051:5
	llvmPrintlnI64(main, value)
	// Osty: toolchain/llvmgen.osty:5052:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:5063:5
func llvmSmokeStructStringFieldIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:5064:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:5065:5
	msg := llvmStructLiteral(main, "%Message", []*LlvmValue{llvmStringLiteral(main, "struct string")})
	_ = msg
	// Osty: toolchain/llvmgen.osty:5066:5
	llvmImmutableLet(main, "msg", msg)
	// Osty: toolchain/llvmgen.osty:5067:5
	llvmPrintlnString(main, llvmExtractValue(main, llvmIdent(main, "msg"), "ptr", 0))
	// Osty: toolchain/llvmgen.osty:5068:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Message", []string{"ptr"})}, main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:5083:5
func llvmSmokeStructBoolFieldIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:5084:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:5085:5
	gate := llvmStructLiteral(main, "%Gate", []*LlvmValue{llvmI1("true")})
	_ = gate
	// Osty: toolchain/llvmgen.osty:5086:5
	llvmImmutableLet(main, "gate", gate)
	// Osty: toolchain/llvmgen.osty:5087:5
	cond := llvmExtractValue(main, llvmIdent(main, "gate"), "i1", 0)
	_ = cond
	// Osty: toolchain/llvmgen.osty:5088:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:5089:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: toolchain/llvmgen.osty:5090:5
	llvmIfElse(main, labels)
	// Osty: toolchain/llvmgen.osty:5091:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:5092:5
	llvmIfEnd(main, labels)
	// Osty: toolchain/llvmgen.osty:5093:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Gate", []string{"i1"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:5108:5
func llvmSmokeBoolMutableIR(sourcePath string) string {
	// Osty: toolchain/llvmgen.osty:5109:5
	main := llvmEmitter()
	_ = main
	// Osty: toolchain/llvmgen.osty:5110:5
	llvmMutableLet(main, "flag", llvmI1("false"))
	// Osty: toolchain/llvmgen.osty:5111:5
	_ = llvmAssign(main, "flag", llvmI1("true"))
	// Osty: toolchain/llvmgen.osty:5112:5
	cond := llvmIdent(main, "flag")
	_ = cond
	// Osty: toolchain/llvmgen.osty:5113:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: toolchain/llvmgen.osty:5114:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: toolchain/llvmgen.osty:5115:5
	llvmIfElse(main, labels)
	// Osty: toolchain/llvmgen.osty:5116:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: toolchain/llvmgen.osty:5117:5
	llvmIfEnd(main, labels)
	// Osty: toolchain/llvmgen.osty:5118:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty:5129:1
func llvmCallArgs(args []*LlvmValue) string {
	// Osty: toolchain/llvmgen.osty:5130:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: toolchain/llvmgen.osty:5131:5
	for _, arg := range args {
		// Osty: toolchain/llvmgen.osty:5132:9
		func() struct{} {
			parts = append(parts, fmt.Sprintf("%s %s", ostyToString(arg.typ), ostyToString(arg.name)))
			return struct{}{}
		}()
	}
	return llvmStrings.Join(parts, ", ")
}

// Osty: toolchain/llvmgen.osty:5137:1
func llvmParams(params []*LlvmParam) string {
	// Osty: toolchain/llvmgen.osty:5138:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: toolchain/llvmgen.osty:5139:5
	for _, param := range params {
		// Osty: toolchain/llvmgen.osty:5140:9
		func() struct{} {
			parts = append(parts, fmt.Sprintf("%s %%%s", ostyToString(param.typ), ostyToString(param.name)))
			return struct{}{}
		}()
	}
	return llvmStrings.Join(parts, ", ")
}

// Osty: toolchain/llvmgen.osty:5145:1
func llvmNextTemp(emitter *LlvmEmitter) string {
	// Osty: toolchain/llvmgen.osty:5146:5
	name := fmt.Sprintf("%%t%s", ostyToString(emitter.temp))
	_ = name
	// Osty: toolchain/llvmgen.osty:5147:12
	emitter.temp = func() int {
		var _p23 int = emitter.temp
		var _rhs24 int = 1
		if _rhs24 > 0 && _p23 > math.MaxInt-_rhs24 {
			panic("integer overflow")
		}
		if _rhs24 < 0 && _p23 < math.MinInt-_rhs24 {
			panic("integer overflow")
		}
		return _p23 + _rhs24
	}()
	return name
}

// Osty: toolchain/llvmgen.osty:5151:1
func llvmNextLabel(emitter *LlvmEmitter, prefix string) string {
	// Osty: toolchain/llvmgen.osty:5152:5
	name := fmt.Sprintf("%s%s", ostyToString(prefix), ostyToString(emitter.label))
	_ = name
	// Osty: toolchain/llvmgen.osty:5153:12
	emitter.label = func() int {
		var _p25 int = emitter.label
		var _rhs26 int = 1
		if _rhs26 > 0 && _p25 > math.MaxInt-_rhs26 {
			panic("integer overflow")
		}
		if _rhs26 < 0 && _p25 < math.MinInt-_rhs26 {
			panic("integer overflow")
		}
		return _p25 + _rhs26
	}()
	return name
}
