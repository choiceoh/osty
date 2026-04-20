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

// Osty: examples/selfhost-core/llvmgen.osty:9:5
type LlvmValue struct {
	typ     string
	name    string
	pointer bool
}

// Osty: examples/selfhost-core/llvmgen.osty:15:5
type LlvmParam struct {
	name string
	typ  string
}

// Osty: examples/selfhost-core/llvmgen.osty:20:5
type LlvmBinding struct {
	name  string
	value *LlvmValue
}

// Osty: examples/selfhost-core/llvmgen.osty:25:5
type LlvmLookup struct {
	found bool
	value *LlvmValue
}

// Osty: examples/selfhost-core/llvmgen.osty:30:5
type LlvmStringGlobal struct {
	name    string
	encoded string
	byteLen int
}

// Osty: examples/selfhost-core/llvmgen.osty:36:5
type LlvmStructField struct {
	typ string
}

// Osty: examples/selfhost-core/llvmgen.osty:40:5
type LlvmCString struct {
	encoded string
	byteLen int
}

// Osty: examples/selfhost-core/llvmgen.osty:45:5
type LlvmEmitter struct {
	temp          int
	label         int
	stringId      int
	body          []string
	locals        []*LlvmBinding
	stringGlobals []*LlvmStringGlobal
}

// Osty: examples/selfhost-core/llvmgen.osty:54:5
type LlvmIfLabels struct {
	thenLabel string
	elseLabel string
	endLabel  string
}

// Osty: examples/selfhost-core/llvmgen.osty:60:5
type LlvmRangeLoop struct {
	condLabel string
	bodyLabel string
	endLabel  string
	iterPtr   string
	current   string
}

// Osty: examples/selfhost-core/llvmgen.osty:68:5
type LlvmSmokeExecutableCase struct {
	name    string
	fixture string
	stdout  string
}

// Osty: examples/selfhost-core/llvmgen.osty:74:5
type LlvmUnsupportedDiagnostic struct {
	code    string
	kind    string
	message string
	hint    string
}

// Osty: examples/selfhost-core/llvmgen.osty:81:5
func llvmEmitter() *LlvmEmitter {
	return &LlvmEmitter{temp: 0, label: 0, stringId: 0, body: make([]string, 0, 1), locals: make([]*LlvmBinding, 0, 1), stringGlobals: make([]*LlvmStringGlobal, 0, 1)}
}

// Osty: examples/selfhost-core/llvmgen.osty:92:5
func llvmI64(name string) *LlvmValue {
	return &LlvmValue{typ: "i64", name: name, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:96:5
func llvmI1(name string) *LlvmValue {
	return &LlvmValue{typ: "i1", name: name, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:100:5
func llvmF64(name string) *LlvmValue {
	return &LlvmValue{typ: "double", name: name, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:104:5
func llvmIntLiteral(value int) *LlvmValue {
	return llvmI64(fmt.Sprintf("%s", ostyToString(value)))
}

// Osty: examples/selfhost-core/llvmgen.osty:108:5
func llvmFloatLiteral(value string) *LlvmValue {
	return llvmF64(fmt.Sprintf("%s", ostyToString(value)))
}

// Osty: examples/selfhost-core/llvmgen.osty:112:5
func llvmEnumVariant(enumName string, tag int) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:113:5
	_ = enumName
	return llvmI64(fmt.Sprintf("%s", ostyToString(tag)))
}

// Osty: examples/selfhost-core/llvmgen.osty:117:5
func llvmEnumPayloadVariant(emitter *LlvmEmitter, typ string, tag int, payload *LlvmValue) *LlvmValue {
	return llvmStructLiteral(emitter, typ, []*LlvmValue{llvmEnumVariant(typ, tag), payload})
}

func llvmEnumBoxedPayloadVariant(emitter *LlvmEmitter, enumTyp string, tag int, payload *LlvmValue, site string) *LlvmValue {
	symbol := "osty.rt.enum_alloc_scalar_v1"
	if payload.typ == "ptr" {
		symbol = "osty.rt.enum_alloc_ptr_v1"
	}
	heapPtr := llvmCall(emitter, "ptr", symbol, []*LlvmValue{llvmStringLiteral(emitter, site)})
	llvmStore(emitter, heapPtr, payload)
	return llvmStructLiteral(emitter, enumTyp, []*LlvmValue{llvmEnumVariant(enumTyp, tag), heapPtr})
}

func llvmEnumBoxedBareVariant(emitter *LlvmEmitter, enumTyp string, tag int) *LlvmValue {
	nullPtr := &LlvmValue{typ: "ptr", name: "null", pointer: false}
	return llvmStructLiteral(emitter, enumTyp, []*LlvmValue{llvmEnumVariant(enumTyp, tag), nullPtr})
}

// Osty: examples/selfhost-core/llvmgen.osty:126:5
func llvmParam(name string, typ string) *LlvmParam {
	return &LlvmParam{name: name, typ: typ}
}

// Osty: examples/selfhost-core/llvmgen.osty:130:5
func llvmBind(emitter *LlvmEmitter, name string, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:131:5
	func() struct{} {
		emitter.locals = append(emitter.locals, &LlvmBinding{name: name, value: value})
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:134:5
func llvmLookup(emitter *LlvmEmitter, name string) *LlvmLookup {
	// Osty: examples/selfhost-core/llvmgen.osty:135:5
	out := &LlvmLookup{found: false, value: llvmI64("0")}
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:136:5
	for _, binding := range emitter.locals {
		// Osty: examples/selfhost-core/llvmgen.osty:137:9
		if binding.name == name {
			// Osty: examples/selfhost-core/llvmgen.osty:138:13
			out = &LlvmLookup{found: true, value: binding.value}
		}
	}
	return out
}

// Osty: examples/selfhost-core/llvmgen.osty:144:5
func llvmIdent(emitter *LlvmEmitter, name string) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:145:5
	lookup := llvmLookup(emitter, name)
	_ = lookup
	// Osty: examples/selfhost-core/llvmgen.osty:146:5
	if !(lookup.found) {
		// Osty: examples/selfhost-core/llvmgen.osty:147:9
		return llvmI64("0")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:149:5
	if lookup.value.pointer {
		// Osty: examples/selfhost-core/llvmgen.osty:150:9
		return llvmLoad(emitter, lookup.value)
	}
	return lookup.value
}

// Osty: examples/selfhost-core/llvmgen.osty:155:5
func llvmLoad(emitter *LlvmEmitter, slot *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:156:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:157:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", ostyToString(tmp), ostyToString(slot.typ), ostyToString(slot.name)))
		return struct{}{}
	}()
	return &LlvmValue{typ: slot.typ, name: tmp, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:161:5
func llvmSlotAsPtr(slot *LlvmValue) *LlvmValue {
	return &LlvmValue{typ: "ptr", name: slot.name, pointer: false}
}

func llvmAllocaSlot(emitter *LlvmEmitter, llvmType string) *LlvmValue {
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", slot, llvmType))
	return &LlvmValue{typ: "ptr", name: slot, pointer: false}
}

func llvmSpillToSlot(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	slot := llvmAllocaSlot(emitter, value.typ)
	emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", value.typ, value.name, slot.name))
	return slot
}

func llvmLoadFromSlot(emitter *LlvmEmitter, slot *LlvmValue, llvmType string) *LlvmValue {
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", tmp, llvmType, slot.name))
	return &LlvmValue{typ: llvmType, name: tmp, pointer: false}
}

func llvmSizeOf(emitter *LlvmEmitter, llvmType string) *LlvmValue {
	gep := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr %s, ptr null, i32 1", gep, llvmType))
	size := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = ptrtoint ptr %s to i64", size, gep))
	return &LlvmValue{typ: "i64", name: size, pointer: false}
}

func llvmMutableLetSlot(emitter *LlvmEmitter, name string, initial *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:166:5
	ptr := llvmNextTemp(emitter)
	_ = ptr
	// Osty: examples/selfhost-core/llvmgen.osty:167:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", ostyToString(ptr), ostyToString(initial.typ)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:168:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", ostyToString(initial.typ), ostyToString(initial.name), ostyToString(ptr)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:169:5
	slot := &LlvmValue{typ: initial.typ, name: ptr, pointer: true}
	_ = slot
	// Osty: examples/selfhost-core/llvmgen.osty:170:5
	llvmBind(emitter, name, slot)
	return slot
}

// Osty: examples/selfhost-core/llvmgen.osty:174:5
func llvmMutableLet(emitter *LlvmEmitter, name string, initial *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:175:5
	_slot := llvmMutableLetSlot(emitter, name, initial)
	_ = _slot
}

// Osty: examples/selfhost-core/llvmgen.osty:178:5
func llvmStore(emitter *LlvmEmitter, slot *LlvmValue, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:179:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", ostyToString(value.typ), ostyToString(value.name), ostyToString(slot.name)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:182:5
func llvmAssign(emitter *LlvmEmitter, name string, value *LlvmValue) bool {
	// Osty: examples/selfhost-core/llvmgen.osty:183:5
	lookup := llvmLookup(emitter, name)
	_ = lookup
	// Osty: examples/selfhost-core/llvmgen.osty:184:5
	if !(lookup.found) || !(lookup.value.pointer) || lookup.value.typ != value.typ {
		// Osty: examples/selfhost-core/llvmgen.osty:185:9
		return false
	}
	// Osty: examples/selfhost-core/llvmgen.osty:187:5
	llvmStore(emitter, lookup.value, value)
	return true
}

// Osty: examples/selfhost-core/llvmgen.osty:191:5
func llvmImmutableLet(emitter *LlvmEmitter, name string, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:192:5
	llvmBind(emitter, name, value)
}

// Osty: examples/selfhost-core/llvmgen.osty:199:5
func llvmBinaryI64(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:205:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:206:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = %s i64 %s, %s", ostyToString(tmp), ostyToString(op), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI64(tmp)
}

// Osty: examples/selfhost-core/llvmgen.osty:210:5
func llvmBinaryF64(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:216:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:217:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = %s double %s, %s", ostyToString(tmp), ostyToString(op), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmF64(tmp)
}

// Osty: examples/selfhost-core/llvmgen.osty:221:5
func llvmCompare(emitter *LlvmEmitter, pred string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:227:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:228:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = icmp %s %s %s, %s", ostyToString(tmp), ostyToString(pred), ostyToString(left.typ), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: examples/selfhost-core/llvmgen.osty:232:5
func llvmCompareF64(emitter *LlvmEmitter, pred string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:238:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:239:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = fcmp %s double %s, %s", ostyToString(tmp), ostyToString(pred), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: examples/selfhost-core/llvmgen.osty:243:5
func llvmNotI1(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:244:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:245:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = xor i1 %s, true", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: examples/selfhost-core/llvmgen.osty:249:5
func llvmLogicalI1(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:255:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:256:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = %s i1 %s, %s", ostyToString(tmp), ostyToString(op), ostyToString(left.name), ostyToString(right.name)))
		return struct{}{}
	}()
	return llvmI1(tmp)
}

// Osty: examples/selfhost-core/llvmgen.osty:260:5
func llvmCall(emitter *LlvmEmitter, ret string, name string, args []*LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:266:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:267:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s @%s(%s)", ostyToString(tmp), ostyToString(ret), ostyToString(name), ostyToString(llvmCallArgs(args))))
		return struct{}{}
	}()
	return &LlvmValue{typ: ret, name: tmp, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:271:5
func llvmCallVoid(emitter *LlvmEmitter, name string, args []*LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:272:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void @%s(%s)", ostyToString(name), ostyToString(llvmCallArgs(args))))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:275:5
func llvmGcRuntimeDeclarations() []string {
	return []string{"declare ptr @osty.gc.alloc_v1(i64, i64, ptr)", "declare void @osty.gc.pre_write_v1(ptr, ptr, i64)", "declare void @osty.gc.post_write_v1(ptr, ptr, i64)", "declare ptr @osty.gc.load_v1(ptr)", "declare void @osty.gc.root_bind_v1(ptr)", "declare void @osty.gc.root_release_v1(ptr)", "declare ptr @osty.rt.enum_alloc_ptr_v1(ptr)", "declare ptr @osty.rt.enum_alloc_scalar_v1(ptr)"}
}

// Osty: examples/selfhost-core/llvmgen.osty:284:5
func llvmGcAlloc(emitter *LlvmEmitter, objectKind int, byteSize int, site string) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty.gc.alloc_v1", []*LlvmValue{llvmIntLiteral(objectKind), llvmIntLiteral(byteSize), llvmStringLiteral(emitter, site)})
}

// Osty: examples/selfhost-core/llvmgen.osty:302:5
func llvmGcPostWrite(emitter *LlvmEmitter, owner *LlvmValue, value *LlvmValue, slotKind int) {
	// Osty: examples/selfhost-core/llvmgen.osty:308:5
	llvmCallVoid(emitter, "osty.gc.post_write_v1", []*LlvmValue{owner, value, llvmIntLiteral(slotKind)})
}

// Osty: examples/selfhost-core/llvmgen.osty:319:5
func llvmGcPreWrite(emitter *LlvmEmitter, owner *LlvmValue, value *LlvmValue, slotKind int) {
	// Osty: examples/selfhost-core/llvmgen.osty:325:5
	llvmCallVoid(emitter, "osty.gc.pre_write_v1", []*LlvmValue{owner, value, llvmIntLiteral(slotKind)})
}

// Osty: examples/selfhost-core/llvmgen.osty:336:5
func llvmGcLoad(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty.gc.load_v1", []*LlvmValue{value})
}

// Osty: examples/selfhost-core/llvmgen.osty:340:5
func llvmGcRootBind(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:341:5
	llvmCallVoid(emitter, "osty.gc.root_bind_v1", []*LlvmValue{value})
}

// Osty: examples/selfhost-core/llvmgen.osty:344:5
func llvmGcRootRelease(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:345:5
	llvmCallVoid(emitter, "osty.gc.root_release_v1", []*LlvmValue{value})
}

// Osty: examples/selfhost-core/llvmgen.osty:327:5
func llvmStructTypeDef(name string, fieldTypes []string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:328:5
	fields := llvmStrings.Join(fieldTypes, ", ")
	_ = fields
	return fmt.Sprintf("%%%s = type { %s }", ostyToString(name), ostyToString(fields))
}

// Osty: examples/selfhost-core/llvmgen.osty:332:5
func llvmStructLiteral(emitter *LlvmEmitter, typ string, fields []*LlvmValue) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:337:5
	current := "undef"
	_ = current
	// Osty: examples/selfhost-core/llvmgen.osty:338:5
	fieldIndex := 0
	_ = fieldIndex
	// Osty: examples/selfhost-core/llvmgen.osty:339:5
	for _, field := range fields {
		// Osty: examples/selfhost-core/llvmgen.osty:340:9
		tmp := llvmNextTemp(emitter)
		_ = tmp
		// Osty: examples/selfhost-core/llvmgen.osty:341:9
		func() struct{} {
			emitter.body = append(emitter.body, fmt.Sprintf("  %s = insertvalue %s %s, %s %s, %s", ostyToString(tmp), ostyToString(typ), ostyToString(current), ostyToString(field.typ), ostyToString(field.name), ostyToString(fieldIndex)))
			return struct{}{}
		}()
		// Osty: examples/selfhost-core/llvmgen.osty:344:9
		current = tmp
		// Osty: examples/selfhost-core/llvmgen.osty:345:9
		func() {
			var _cur1 int = fieldIndex
			var _rhs2 int = 1
			if _rhs2 > 0 && _cur1 > math.MaxInt-_rhs2 {
				panic("integer overflow")
			}
			if _rhs2 < 0 && _cur1 < math.MinInt-_rhs2 {
				panic("integer overflow")
			}
			fieldIndex = _cur1 + _rhs2
		}()
	}
	return &LlvmValue{typ: typ, name: current, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:350:5
func llvmExtractValue(emitter *LlvmEmitter, aggregate *LlvmValue, fieldType string, fieldIndex int) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:356:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:357:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = extractvalue %s %s, %s", ostyToString(tmp), ostyToString(aggregate.typ), ostyToString(aggregate.name), ostyToString(fieldIndex)))
		return struct{}{}
	}()
	return &LlvmValue{typ: fieldType, name: tmp, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:363:5
func llvmPrintlnI64(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:364:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:365:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_i64, i64 %s)", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:368:5
func llvmPrintlnF64(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:369:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:370:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_f64, double %s)", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:372:5
func llvmPrintlnBool(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:373:5
	text := llvmNextTemp(emitter)
	_ = text
	// Osty: examples/selfhost-core/llvmgen.osty:374:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = select i1 %s, ptr @.bool_true, ptr @.bool_false", ostyToString(text), ostyToString(value.name)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:375:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:376:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_str, ptr %s)", ostyToString(tmp), ostyToString(text)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:373:5
func llvmStringLiteral(emitter *LlvmEmitter, text string) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:374:5
	name := fmt.Sprintf("@.str%s", ostyToString(emitter.stringId))
	_ = name
	// Osty: examples/selfhost-core/llvmgen.osty:375:12
	emitter.stringId = func() int {
		var _p3 int = emitter.stringId
		var _rhs4 int = 1
		if _rhs4 > 0 && _p3 > math.MaxInt-_rhs4 {
			panic("integer overflow")
		}
		if _rhs4 < 0 && _p3 < math.MinInt-_rhs4 {
			panic("integer overflow")
		}
		return _p3 + _rhs4
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:376:5
	cstring := llvmCString(text)
	_ = cstring
	// Osty: examples/selfhost-core/llvmgen.osty:377:5
	func() struct{} {
		emitter.stringGlobals = append(emitter.stringGlobals, &LlvmStringGlobal{name: name, encoded: cstring.encoded, byteLen: cstring.byteLen})
		return struct{}{}
	}()
	return &LlvmValue{typ: "ptr", name: name, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:383:5
func llvmCString(text string) *LlvmCString {
	// Osty: examples/selfhost-core/llvmgen.osty:384:5
	encoded := fmt.Sprintf("%s\\00", ostyToString(llvmCStringEscape(text)))
	_ = encoded
	// Osty: examples/selfhost-core/llvmgen.osty:385:5
	byteLen := func() int {
		var _p5 int = len(llvmStrings.Split(text, ""))
		var _rhs6 int = 1
		if _rhs6 > 0 && _p5 > math.MaxInt-_rhs6 {
			panic("integer overflow")
		}
		if _rhs6 < 0 && _p5 < math.MinInt-_rhs6 {
			panic("integer overflow")
		}
		return _p5 + _rhs6
	}()
	_ = byteLen
	return &LlvmCString{encoded: encoded, byteLen: byteLen}
}

// Osty: examples/selfhost-core/llvmgen.osty:389:5
func llvmCStringEscape(text string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:390:5
	encoded := ""
	_ = encoded
	// Osty: examples/selfhost-core/llvmgen.osty:391:5
	for _, unit := range llvmStrings.Split(text, "") {
		// Osty: examples/selfhost-core/llvmgen.osty:392:9
		if unit == "\n" {
			// Osty: examples/selfhost-core/llvmgen.osty:393:13
			encoded = fmt.Sprintf("%s\\0A", ostyToString(encoded))
		} else if unit == "\t" {
			// Osty: examples/selfhost-core/llvmgen.osty:395:13
			encoded = fmt.Sprintf("%s\\09", ostyToString(encoded))
		} else if unit == "\r" {
			// Osty: examples/selfhost-core/llvmgen.osty:397:13
			encoded = fmt.Sprintf("%s\\0D", ostyToString(encoded))
		} else if unit == "\"" {
			// Osty: examples/selfhost-core/llvmgen.osty:399:13
			encoded = fmt.Sprintf("%s\\22", ostyToString(encoded))
		} else if unit == "\\" {
			// Osty: examples/selfhost-core/llvmgen.osty:401:13
			encoded = fmt.Sprintf("%s\\5C", ostyToString(encoded))
		} else if unit == "\x1f" {
			encoded = fmt.Sprintf("%s\\1F", ostyToString(encoded))
		} else {
			// Osty: examples/selfhost-core/llvmgen.osty:403:13
			encoded = fmt.Sprintf("%s%s", ostyToString(encoded), ostyToString(unit))
		}
	}
	return encoded
}

// Osty: examples/selfhost-core/llvmgen.osty:409:5
func llvmPrintlnString(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:410:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:411:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i32 (ptr, ...) @printf(ptr @.fmt_str, ptr %s)", ostyToString(tmp), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:414:5
func llvmIfStart(emitter *LlvmEmitter, cond *LlvmValue) *LlvmIfLabels {
	// Osty: examples/selfhost-core/llvmgen.osty:415:5
	labels := &LlvmIfLabels{thenLabel: llvmNextLabel(emitter, "if.then"), elseLabel: llvmNextLabel(emitter, "if.else"), endLabel: llvmNextLabel(emitter, "if.end")}
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:420:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", ostyToString(cond.name), ostyToString(labels.thenLabel), ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:421:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.thenLabel)))
		return struct{}{}
	}()
	return labels
}

// Osty: examples/selfhost-core/llvmgen.osty:425:5
func llvmIfElse(emitter *LlvmEmitter, labels *LlvmIfLabels) {
	// Osty: examples/selfhost-core/llvmgen.osty:426:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:427:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:430:5
func llvmIfEnd(emitter *LlvmEmitter, labels *LlvmIfLabels) {
	// Osty: examples/selfhost-core/llvmgen.osty:431:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:432:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:435:5
func llvmIfExprStart(emitter *LlvmEmitter, cond *LlvmValue) *LlvmIfLabels {
	// Osty: examples/selfhost-core/llvmgen.osty:436:5
	labels := &LlvmIfLabels{thenLabel: llvmNextLabel(emitter, "if.expr.then"), elseLabel: llvmNextLabel(emitter, "if.expr.else"), endLabel: llvmNextLabel(emitter, "if.expr.end")}
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:441:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", ostyToString(cond.name), ostyToString(labels.thenLabel), ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:442:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.thenLabel)))
		return struct{}{}
	}()
	return labels
}

// Osty: examples/selfhost-core/llvmgen.osty:446:5
func llvmIfExprElse(emitter *LlvmEmitter, labels *LlvmIfLabels) {
	// Osty: examples/selfhost-core/llvmgen.osty:447:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:448:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:451:5
func llvmIfExprEnd(emitter *LlvmEmitter, typ string, thenValue *LlvmValue, elseValue *LlvmValue, labels *LlvmIfLabels) *LlvmValue {
	// Osty: examples/selfhost-core/llvmgen.osty:458:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:459:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(labels.endLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:460:5
	tmp := llvmNextTemp(emitter)
	_ = tmp
	// Osty: examples/selfhost-core/llvmgen.osty:461:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi %s [ %s, %%%s ], [ %s, %%%s ]", ostyToString(tmp), ostyToString(typ), ostyToString(thenValue.name), ostyToString(labels.thenLabel), ostyToString(elseValue.name), ostyToString(labels.elseLabel)))
		return struct{}{}
	}()
	return &LlvmValue{typ: typ, name: tmp, pointer: false}
}

// Osty: examples/selfhost-core/llvmgen.osty:467:5
func llvmInclusiveRangeStart(emitter *LlvmEmitter, iterName string, start *LlvmValue, stop *LlvmValue) *LlvmRangeLoop {
	return llvmRangeStart(emitter, iterName, start, stop, true)
}

// Osty: examples/selfhost-core/llvmgen.osty:476:5
func llvmRangeStart(emitter *LlvmEmitter, iterName string, start *LlvmValue, stop *LlvmValue, inclusive bool) *LlvmRangeLoop {
	// Osty: examples/selfhost-core/llvmgen.osty:483:5
	iterPtr := llvmNextTemp(emitter)
	_ = iterPtr
	// Osty: examples/selfhost-core/llvmgen.osty:484:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca i64", ostyToString(iterPtr)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:485:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store i64 %s, ptr %s", ostyToString(start.name), ostyToString(iterPtr)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:487:5
	loop := &LlvmRangeLoop{condLabel: llvmNextLabel(emitter, "for.cond"), bodyLabel: llvmNextLabel(emitter, "for.body"), endLabel: llvmNextLabel(emitter, "for.end"), iterPtr: iterPtr, current: ""}
	_ = loop
	// Osty: examples/selfhost-core/llvmgen.osty:494:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(loop.condLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:495:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(loop.condLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:497:5
	current := llvmNextTemp(emitter)
	_ = current
	// Osty: examples/selfhost-core/llvmgen.osty:498:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = load i64, ptr %s", ostyToString(current), ostyToString(iterPtr)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:499:5
	cmp := llvmNextTemp(emitter)
	_ = cmp
	// Osty: examples/selfhost-core/llvmgen.osty:500:5
	pred := "slt"
	_ = pred
	// Osty: examples/selfhost-core/llvmgen.osty:501:5
	if inclusive {
		// Osty: examples/selfhost-core/llvmgen.osty:502:9
		pred = "sle"
	}
	// Osty: examples/selfhost-core/llvmgen.osty:504:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = icmp %s i64 %s, %s", ostyToString(cmp), ostyToString(pred), ostyToString(current), ostyToString(stop.name)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:505:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", ostyToString(cmp), ostyToString(loop.bodyLabel), ostyToString(loop.endLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:506:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(loop.bodyLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:507:5
	llvmBind(emitter, iterName, llvmI64(current))
	return &LlvmRangeLoop{condLabel: loop.condLabel, bodyLabel: loop.bodyLabel, endLabel: loop.endLabel, iterPtr: loop.iterPtr, current: current}
}

// Osty: examples/selfhost-core/llvmgen.osty:518:5
func llvmRangeEnd(emitter *LlvmEmitter, loop *LlvmRangeLoop) {
	// Osty: examples/selfhost-core/llvmgen.osty:519:5
	next := llvmNextTemp(emitter)
	_ = next
	// Osty: examples/selfhost-core/llvmgen.osty:520:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = add i64 %s, 1", ostyToString(next), ostyToString(loop.current)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:521:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  store i64 %s, ptr %s", ostyToString(next), ostyToString(loop.iterPtr)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:522:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", ostyToString(loop.condLabel)))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:523:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", ostyToString(loop.endLabel)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:526:5
func llvmReturn(emitter *LlvmEmitter, value *LlvmValue) {
	// Osty: examples/selfhost-core/llvmgen.osty:527:5
	func() struct{} {
		emitter.body = append(emitter.body, fmt.Sprintf("  ret %s %s", ostyToString(value.typ), ostyToString(value.name)))
		return struct{}{}
	}()
}

// Osty: examples/selfhost-core/llvmgen.osty:530:5
func llvmReturnI32Zero(emitter *LlvmEmitter) {
	// Osty: examples/selfhost-core/llvmgen.osty:531:5
	func() struct{} { emitter.body = append(emitter.body, "  ret i32 0"); return struct{}{} }()
}

// Osty: examples/selfhost-core/llvmgen.osty:534:5
func llvmRenderModule(sourcePath string, target string, definitions []string) string {
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, target, make([]string, 0, 1), make([]*LlvmStringGlobal, 0, 1), definitions)
}

// Osty: examples/selfhost-core/llvmgen.osty:538:5
func llvmRenderModuleWithGlobals(sourcePath string, target string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, target, make([]string, 0, 1), stringGlobals, definitions)
}

// Osty: examples/selfhost-core/llvmgen.osty:547:5
func llvmRenderModuleWithGlobalsAndTypes(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, make([]string, 0, 1), definitions)
}

// Osty: examples/selfhost-core/llvmgen.osty:564:5
func llvmRenderModuleWithGcRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmGcRuntimeDeclarations(), definitions)
}

func llvmRenderModuleWithListRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmListRuntimeDeclarations(), definitions)
}

func llvmRenderModuleWithMapRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmMapRuntimeDeclarations(), definitions)
}

func llvmRenderModuleWithSetRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmSetRuntimeDeclarations(), definitions)
}

func llvmRenderModuleWithStringRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmStringRuntimeDeclarations(), definitions)
}

func llvmRenderModuleWithChannelRuntime(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, definitions []string) string {
	return llvmRenderModuleWithRuntimeDeclarations(sourcePath, target, typeDefs, stringGlobals, llvmChanRuntimeDeclarations(), definitions)
}

// Osty: examples/selfhost-core/llvmgen.osty:581:1
func llvmRenderModuleWithRuntimeDeclarations(sourcePath string, target string, typeDefs []string, stringGlobals []*LlvmStringGlobal, runtimeDeclarations []string, definitions []string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:589:5
	lines := []string{"; Code generated by osty LLVM backend. DO NOT EDIT.", fmt.Sprintf("; Osty: %s", ostyToString(sourcePath)), llvmStrings.Join([]string{"source_filename = \"", sourcePath, "\""}, "")}
	_ = lines
	// Osty: examples/selfhost-core/llvmgen.osty:594:5
	if target != "" {
		// Osty: examples/selfhost-core/llvmgen.osty:595:9
		func() struct{} {
			lines = append(lines, llvmStrings.Join([]string{"target triple = \"", target, "\""}, ""))
			return struct{}{}
		}()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:597:5
	func() struct{} { lines = append(lines, ""); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:598:5
	for _, typeDef := range typeDefs {
		// Osty: examples/selfhost-core/llvmgen.osty:599:9
		func() struct{} { lines = append(lines, typeDef); return struct{}{} }()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:601:5
	if len(typeDefs) > 0 {
		// Osty: examples/selfhost-core/llvmgen.osty:602:9
		func() struct{} { lines = append(lines, ""); return struct{}{} }()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:604:5
	func() struct{} {
		lines = append(lines, "@.fmt_i64 = private unnamed_addr constant [5 x i8] c\"%ld\\0A\\00\"")
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:605:5
	func() struct{} {
		lines = append(lines, "@.fmt_f64 = private unnamed_addr constant [6 x i8] c\"%.6f\\0A\\00\"")
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:606:5
	func() struct{} {
		lines = append(lines, "@.fmt_str = private unnamed_addr constant [4 x i8] c\"%s\\0A\\00\"")
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:607:5
	func() struct{} {
		lines = append(lines, "@.bool_true = private unnamed_addr constant [5 x i8] c\"true\\00\"")
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:608:5
	func() struct{} {
		lines = append(lines, "@.bool_false = private unnamed_addr constant [6 x i8] c\"false\\00\"")
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:607:5
	for _, global := range stringGlobals {
		// Osty: examples/selfhost-core/llvmgen.osty:608:9
		func() struct{} {
			lines = append(lines, llvmStrings.Join([]string{global.name, " = private unnamed_addr constant [", fmt.Sprintf("%s", ostyToString(global.byteLen)), " x i8] c\"", global.encoded, "\""}, ""))
			return struct{}{}
		}()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:622:5
	func() struct{} { lines = append(lines, "declare i32 @printf(ptr, ...)"); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:623:5
	for _, runtimeDeclaration := range runtimeDeclarations {
		// Osty: examples/selfhost-core/llvmgen.osty:624:9
		func() struct{} { lines = append(lines, runtimeDeclaration); return struct{}{} }()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:626:5
	firstDefinition := true
	_ = firstDefinition
	// Osty: examples/selfhost-core/llvmgen.osty:627:5
	for _, definition := range definitions {
		// Osty: examples/selfhost-core/llvmgen.osty:628:9
		if firstDefinition {
			// Osty: examples/selfhost-core/llvmgen.osty:629:13
			func() struct{} { lines = append(lines, ""); return struct{}{} }()
			// Osty: examples/selfhost-core/llvmgen.osty:630:13
			firstDefinition = false
		}
		// Osty: examples/selfhost-core/llvmgen.osty:632:9
		func() struct{} { lines = append(lines, definition); return struct{}{} }()
	}
	return llvmStrings.Join(lines, "\n")
}

// Osty: examples/selfhost-core/llvmgen.osty:637:5
func llvmRenderSkeleton(packageName string, sourcePath string, emit string, target string, unsupported string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:644:5
	pkg := packageName
	_ = pkg
	// Osty: examples/selfhost-core/llvmgen.osty:645:5
	if pkg == "" {
		// Osty: examples/selfhost-core/llvmgen.osty:646:9
		pkg = "main"
	}
	// Osty: examples/selfhost-core/llvmgen.osty:648:5
	source := sourcePath
	_ = source
	// Osty: examples/selfhost-core/llvmgen.osty:649:5
	if source == "" {
		// Osty: examples/selfhost-core/llvmgen.osty:650:9
		source = "<unknown>"
	}
	// Osty: examples/selfhost-core/llvmgen.osty:653:5
	lines := []string{"; Osty LLVM backend skeleton", fmt.Sprintf("; package: %s", ostyToString(pkg)), fmt.Sprintf("; source: %s", ostyToString(source)), fmt.Sprintf("; emit: %s", ostyToString(emit))}
	_ = lines
	// Osty: examples/selfhost-core/llvmgen.osty:659:5
	if target != "" {
		// Osty: examples/selfhost-core/llvmgen.osty:660:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("; target: %s", ostyToString(target)))
			return struct{}{}
		}()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:662:5
	if unsupported != "" {
		// Osty: examples/selfhost-core/llvmgen.osty:663:9
		func() struct{} {
			lines = append(lines, fmt.Sprintf("; unsupported: %s", ostyToString(unsupported)))
			return struct{}{}
		}()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:665:5
	func() struct{} { lines = append(lines, "; code generation is not implemented yet"); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:666:5
	func() struct{} { lines = append(lines, ""); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:667:5
	func() struct{} {
		lines = append(lines, llvmStrings.Join([]string{"source_filename = \"", source, "\""}, ""))
		return struct{}{}
	}()
	// Osty: examples/selfhost-core/llvmgen.osty:668:5
	if target != "" {
		// Osty: examples/selfhost-core/llvmgen.osty:669:9
		func() struct{} {
			lines = append(lines, llvmStrings.Join([]string{"target triple = \"", target, "\""}, ""))
			return struct{}{}
		}()
	}
	return llvmStrings.Join([]string{llvmStrings.Join(lines, "\n"), "\n"}, "")
}

// Osty: examples/selfhost-core/llvmgen.osty:674:5
func llvmRenderFunction(ret string, name string, params []*LlvmParam, body []string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:680:5
	lines := []string{fmt.Sprintf("define %s @%s(%s) {", ostyToString(ret), ostyToString(name), ostyToString(llvmParams(params))), "entry:"}
	_ = lines
	// Osty: examples/selfhost-core/llvmgen.osty:681:5
	for _, line := range body {
		// Osty: examples/selfhost-core/llvmgen.osty:682:9
		func() struct{} { lines = append(lines, line); return struct{}{} }()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:684:5
	func() struct{} { lines = append(lines, "}"); return struct{}{} }()
	return llvmStrings.Join([]string{llvmStrings.Join(lines, "\n"), "\n"}, "")
}

// Osty: examples/selfhost-core/llvmgen.osty:688:5
func llvmNeedsObjectArtifact(emit string) bool {
	return emit == "object" || emit == "binary"
}

// Osty: examples/selfhost-core/llvmgen.osty:692:5
func llvmNeedsBinaryArtifact(emit string) bool {
	return emit == "binary"
}

// Osty: examples/selfhost-core/llvmgen.osty:696:5
func llvmClangCompileObjectArgs(target string, irPath string, objectPath string) []string {
	// Osty: examples/selfhost-core/llvmgen.osty:701:5
	var args []string = make([]string, 0, 1)
	_ = args
	// Osty: examples/selfhost-core/llvmgen.osty:702:5
	if target != "" {
		// Osty: examples/selfhost-core/llvmgen.osty:703:9
		func() struct{} { args = append(args, "-target"); return struct{}{} }()
		// Osty: examples/selfhost-core/llvmgen.osty:704:9
		func() struct{} { args = append(args, target); return struct{}{} }()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:706:5
	func() struct{} { args = append(args, "-c"); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:707:5
	func() struct{} { args = append(args, irPath); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:708:5
	func() struct{} { args = append(args, "-o"); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:709:5
	func() struct{} { args = append(args, objectPath); return struct{}{} }()
	return args
}

// Osty: examples/selfhost-core/llvmgen.osty:713:5
func llvmClangLinkBinaryArgs(target string, objectPaths []string, binaryPath string) []string {
	// Osty: examples/selfhost-core/llvmgen.osty:718:5
	var args []string = make([]string, 0, 1)
	_ = args
	// Osty: examples/selfhost-core/llvmgen.osty:719:5
	if target != "" {
		// Osty: examples/selfhost-core/llvmgen.osty:720:9
		func() struct{} { args = append(args, "-target"); return struct{}{} }()
		// Osty: examples/selfhost-core/llvmgen.osty:721:9
		func() struct{} { args = append(args, target); return struct{}{} }()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:723:5
	for _, objectPath := range objectPaths {
		// Osty: examples/selfhost-core/llvmgen.osty:724:9
		func() struct{} { args = append(args, objectPath); return struct{}{} }()
	}
	// Osty: examples/selfhost-core/llvmgen.osty:726:5
	func() struct{} { args = append(args, "-o"); return struct{}{} }()
	// Osty: examples/selfhost-core/llvmgen.osty:727:5
	func() struct{} { args = append(args, binaryPath); return struct{}{} }()
	return args
}

// Osty: examples/selfhost-core/llvmgen.osty:731:5
func llvmMissingClangMessage() string {
	return "llvm backend: clang not found on PATH; install clang or use --emit=llvm-ir"
}

// Osty: examples/selfhost-core/llvmgen.osty:735:5
func llvmMissingBinaryArtifactMessage() string {
	return "llvm backend: missing binary artifact path"
}

// Osty: examples/selfhost-core/llvmgen.osty:739:5
func llvmClangFailureMessage(action string, command string, output string) string {
	return llvmStrings.Join([]string{"llvm backend: clang ", action, " failed\ncommand: ", command, "\n", output}, "")
}

// Osty: examples/selfhost-core/llvmgen.osty:746:5
func llvmUnsupportedBackendErrorMessage() string {
	return "llvm backend: code generation is not implemented yet"
}

// Osty: examples/selfhost-core/llvmgen.osty:750:5
func llvmUnsupportedDiagnostic(kind string, detail string) *LlvmUnsupportedDiagnostic {
	// Osty: examples/selfhost-core/llvmgen.osty:751:5
	if kind == "go-ffi" {
		// Osty: examples/selfhost-core/llvmgen.osty:752:9
		target := detail
		_ = target
		// Osty: examples/selfhost-core/llvmgen.osty:753:9
		if target == "" {
			// Osty: examples/selfhost-core/llvmgen.osty:754:13
			target = "<unknown>"
		}
		// Osty: examples/selfhost-core/llvmgen.osty:756:9
		return &LlvmUnsupportedDiagnostic{code: "LLVM001", kind: "foreign-ffi", message: fmt.Sprintf("Go FFI import %s is not supported by the self-hosted native backend", ostyToString(target)), hint: "replace it with an Osty runtime FFI binding before using the native backend"}
	}
	// Osty: examples/selfhost-core/llvmgen.osty:763:5
	if kind == "runtime-ffi" {
		// Osty: examples/selfhost-core/llvmgen.osty:764:9
		target := detail
		_ = target
		// Osty: examples/selfhost-core/llvmgen.osty:765:9
		if target == "" {
			// Osty: examples/selfhost-core/llvmgen.osty:766:13
			target = "<unknown>"
		}
		// Osty: examples/selfhost-core/llvmgen.osty:768:9
		return &LlvmUnsupportedDiagnostic{code: "LLVM002", kind: "runtime-ffi", message: fmt.Sprintf("Osty runtime FFI import %s needs native runtime lowering", ostyToString(target)), hint: "add the runtime ABI shim and lowering before compiling this source natively"}
	}
	// Osty: examples/selfhost-core/llvmgen.osty:776:5
	if kind == "source-layout" {
		// Osty: examples/selfhost-core/llvmgen.osty:777:9
		return llvmUnsupportedDiagnosticWith("LLVM010", kind, detail, "reshape the file around the current LLVM subset: script statements or a simple main function")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:784:5
	if kind == "type-system" {
		// Osty: examples/selfhost-core/llvmgen.osty:785:9
		return llvmUnsupportedDiagnosticWith("LLVM011", kind, detail, "use Int or Bool values until the LLVM runtime type surface grows")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:792:5
	if kind == "statement" {
		// Osty: examples/selfhost-core/llvmgen.osty:793:9
		return llvmUnsupportedDiagnosticWith("LLVM012", kind, detail, "reduce the statement to let, assignment, if, range-for, return, or println")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:800:5
	if kind == "expression" {
		// Osty: examples/selfhost-core/llvmgen.osty:801:9
		return llvmUnsupportedDiagnosticWith("LLVM013", kind, detail, "reduce the expression to Int, Bool, arithmetic, comparison, call, or value-if forms")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:808:5
	if kind == "control-flow" {
		// Osty: examples/selfhost-core/llvmgen.osty:809:9
		return llvmUnsupportedDiagnosticWith("LLVM014", kind, detail, "use plain if/else or closed Int range loops for the current LLVM backend")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:816:5
	if kind == "call" {
		// Osty: examples/selfhost-core/llvmgen.osty:817:9
		return llvmUnsupportedDiagnosticWith("LLVM015", kind, detail, "call an Osty function with positional Int/Bool arguments or use println as a statement")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:824:5
	if kind == "name" {
		// Osty: examples/selfhost-core/llvmgen.osty:825:9
		return llvmUnsupportedDiagnosticWith("LLVM016", kind, detail, "use simple ASCII identifiers that the LLVM bridge can map directly")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:832:5
	if kind == "function-signature" {
		// Osty: examples/selfhost-core/llvmgen.osty:833:9
		return llvmUnsupportedDiagnosticWith("LLVM017", kind, detail, "use non-generic functions with identifier parameters and Int/Bool types")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:840:5
	if kind == "stdlib-body" {
		// Osty: examples/selfhost-core/llvmgen.osty:841:9
		return llvmUnsupportedDiagnosticWith("LLVM018", kind, detail, "call stdlib functions whose bodies the LLVM backend can currently lower, or add a runtime shim for this symbol")
	}
	// Osty: examples/selfhost-core/llvmgen.osty:849:5
	reason := detail
	_ = reason
	// Osty: examples/selfhost-core/llvmgen.osty:842:5
	if reason == "" {
		// Osty: examples/selfhost-core/llvmgen.osty:843:9
		reason = "source shape is not supported by the current LLVM backend"
	}
	return &LlvmUnsupportedDiagnostic{code: "LLVM000", kind: "unsupported-source", message: reason, hint: "reduce the program to the LLVM smoke subset while the self-hosted native backend grows"}
}

// Osty: examples/selfhost-core/llvmgen.osty:853:5
func llvmUnsupportedDiagnosticWith(code string, kind string, detail string, hint string) *LlvmUnsupportedDiagnostic {
	// Osty: examples/selfhost-core/llvmgen.osty:859:5
	reason := detail
	_ = reason
	// Osty: examples/selfhost-core/llvmgen.osty:860:5
	if reason == "" {
		// Osty: examples/selfhost-core/llvmgen.osty:861:9
		reason = "source shape is not supported by the current LLVM backend"
	}
	return &LlvmUnsupportedDiagnostic{code: code, kind: kind, message: reason, hint: hint}
}

// Osty: examples/selfhost-core/llvmgen.osty:871:5
func llvmUnsupportedSummary(diag *LlvmUnsupportedDiagnostic) string {
	return fmt.Sprintf("%s %s: %s; hint: %s", ostyToString(diag.code), ostyToString(diag.kind), ostyToString(diag.message), ostyToString(diag.hint))
}

func llvmIsCompareOp(op string) bool {
	return op == "==" || op == "!=" || op == "<" || op == ">" || op == "<=" || op == ">="
}

func llvmIntComparePredicate(op string) string {
	switch op {
	case "==":
		return "eq"
	case "!=":
		return "ne"
	case "<":
		return "slt"
	case ">":
		return "sgt"
	case "<=":
		return "sle"
	case ">=":
		return "sge"
	default:
		return ""
	}
}

func llvmFloatComparePredicate(op string) string {
	switch op {
	case "==":
		return "oeq"
	case "!=":
		return "one"
	case "<":
		return "olt"
	case ">":
		return "ogt"
	case "<=":
		return "ole"
	case ">=":
		return "oge"
	default:
		return ""
	}
}

func llvmIntBinaryInstruction(op string) string {
	switch op {
	case "+":
		return "add"
	case "-":
		return "sub"
	case "*":
		return "mul"
	case "/":
		return "sdiv"
	case "%":
		return "srem"
	case "&":
		return "and"
	case "|":
		return "or"
	case "^":
		return "xor"
	case "<<":
		return "shl"
	case ">>":
		return "ashr"
	default:
		return ""
	}
}

func llvmFloatBinaryInstruction(op string) string {
	switch op {
	case "+":
		return "fadd"
	case "-":
		return "fsub"
	case "*":
		return "fmul"
	case "/":
		return "fdiv"
	default:
		return ""
	}
}

func llvmLogicalInstruction(op string) string {
	switch op {
	case "&&":
		return "and"
	case "||":
		return "or"
	default:
		return ""
	}
}

func llvmIsAsciiStringText(text string) bool {
	for _, c := range text {
		if c == '\n' || c == '\t' || c == '\r' || c == '\x1f' {
			continue
		}
		if c < ' ' || c > '~' {
			return false
		}
	}
	return true
}

func llvmIsIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') {
			continue
		}
		if i > 0 && '0' <= c && c <= '9' {
			continue
		}
		return false
	}
	return true
}

func llvmFirstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func llvmIsKnownRuntimeFfiPath(path string) bool {
	if llvmStrings.HasPrefix(path, "runtime.package.") {
		return true
	}
	return path == "runtime.strings" || path == "runtime.path.filepath"
}

func llvmRuntimeFfiAlias(explicitAlias string, lastPath string, runtimePath string) string {
	if explicitAlias != "" {
		return explicitAlias
	}
	if lastPath != "" {
		return lastPath
	}
	parts := llvmStrings.Split(runtimePath, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func llvmRuntimeFfiSymbol(path string, name string) string {
	trimmed := path
	if llvmStrings.HasPrefix(trimmed, "runtime.") {
		trimmed = llvmStrings.TrimPrefix(trimmed, "runtime.")
	}
	out := "osty_rt_"
	for _, c := range trimmed {
		if c == '.' || c == '/' || c == '-' {
			out += "_"
			continue
		}
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
			out += string(c)
			continue
		}
		out += "_"
	}
	return out + "_" + name
}

// Osty: toolchain/llvmgen.osty:1092:1
func llvmListRuntimeNewSymbol() string {
	return "osty_rt_list_new"
}

// Osty: toolchain/llvmgen.osty:1096:1
func llvmListRuntimeLenSymbol() string {
	return "osty_rt_list_len"
}

// Osty: toolchain/llvmgen.osty:1100:1
func llvmListRuntimeSortedI64Symbol() string {
	return "osty_rt_list_sorted_i64"
}

// Osty: toolchain/llvmgen.osty:1104:1
func llvmListRuntimeToSetI64Symbol() string {
	return "osty_rt_list_to_set_i64"
}

// Osty: toolchain/llvmgen.osty (parametric push symbol builder)
func llvmListRuntimePushSymbol(suffix string) string {
	return "osty_rt_list_push_" + suffix
}

func llvmListRuntimeGetSymbol(suffix string) string {
	return "osty_rt_list_get_" + suffix
}

func llvmListRuntimeSetSymbol(suffix string) string {
	return "osty_rt_list_set_" + suffix
}

func llvmListRuntimeSortedSymbol(elemTyp string, isString bool) string {
	if isString {
		return "osty_rt_list_sorted_string"
	}
	switch elemTyp {
	case "i64":
		return "osty_rt_list_sorted_i64"
	case "i1":
		return "osty_rt_list_sorted_i1"
	case "double":
		return "osty_rt_list_sorted_f64"
	}
	return ""
}

func llvmListRuntimeToSetSymbol(elemTyp string, isString bool) string {
	if isString {
		return "osty_rt_list_to_set_string"
	}
	switch elemTyp {
	case "i64":
		return "osty_rt_list_to_set_i64"
	case "i1":
		return "osty_rt_list_to_set_i1"
	case "double":
		return "osty_rt_list_to_set_f64"
	case "ptr":
		return "osty_rt_list_to_set_ptr"
	}
	return ""
}

// Osty: toolchain/llvmgen.osty:1108:1
func llvmMapRuntimeNewSymbol() string {
	return "osty_rt_map_new"
}

// Osty: toolchain/llvmgen.osty:1112:1
func llvmMapRuntimeKeysSymbol() string {
	return "osty_rt_map_keys"
}

func llvmMapRuntimeLenSymbol() string {
	return "osty_rt_map_len"
}

func llvmMapKeySuffix(typ string, isString bool) string {
	if isString {
		return "string"
	}
	switch typ {
	case "i64":
		return "i64"
	case "i1":
		return "i1"
	case "double":
		return "f64"
	case "ptr":
		return "ptr"
	}
	return "bytes"
}

func llvmMapRuntimeContainsSymbol(keyTyp string, isString bool) string {
	return "osty_rt_map_contains_" + llvmMapKeySuffix(keyTyp, isString)
}

func llvmMapRuntimeInsertSymbol(keyTyp string, isString bool) string {
	return "osty_rt_map_insert_" + llvmMapKeySuffix(keyTyp, isString)
}

func llvmMapRuntimeRemoveSymbol(keyTyp string, isString bool) string {
	return "osty_rt_map_remove_" + llvmMapKeySuffix(keyTyp, isString)
}

func llvmMapRuntimeGetOrAbortSymbol(keyTyp string, isString bool) string {
	return "osty_rt_map_get_or_abort_" + llvmMapKeySuffix(keyTyp, isString)
}

// Osty: toolchain/llvmgen.osty:1116:1
func llvmSetRuntimeNewSymbol() string {
	return "osty_rt_set_new"
}

// Osty: toolchain/llvmgen.osty:1120:1
func llvmSetRuntimeLenSymbol() string {
	return "osty_rt_set_len"
}

// Osty: toolchain/llvmgen.osty:1124:1
func llvmSetRuntimeToListSymbol() string {
	return "osty_rt_set_to_list"
}

func llvmSetRuntimeContainsSymbol(elemTyp string, isString bool) string {
	return "osty_rt_set_contains_" + llvmMapKeySuffix(elemTyp, isString)
}

func llvmSetRuntimeInsertSymbol(elemTyp string, isString bool) string {
	return "osty_rt_set_insert_" + llvmMapKeySuffix(elemTyp, isString)
}

func llvmSetRuntimeRemoveSymbol(elemTyp string, isString bool) string {
	return "osty_rt_set_remove_" + llvmMapKeySuffix(elemTyp, isString)
}

// Osty: toolchain/llvmgen.osty:1130:1
func llvmContainerAbiKind(typ string, isString bool) int {
	if isString {
		return 5
	}
	switch typ {
	case "i64":
		return 1
	case "i1":
		return 2
	case "double":
		return 3
	case "ptr":
		return 4
	default:
		return 6
	}
}

func llvmListUsesTypedRuntime(elemTyp string) bool {
	return elemTyp == "i64" || elemTyp == "i1" || elemTyp == "double" || elemTyp == "ptr"
}

func llvmListElementSuffix(typ string) string {
	switch typ {
	case "i64", "i1", "ptr":
		return typ
	case "double":
		return "f64"
	}
	out := ""
	for _, c := range typ {
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
			out += string(c)
			continue
		}
		out += "_"
	}
	if out == "" {
		return "ptr"
	}
	return out
}

func llvmListRuntimeDeclarations() []string {
	return []string{
		"declare ptr @osty_rt_list_new()",
		"declare i64 @osty_rt_list_len(ptr)",
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"declare void @osty_rt_list_push_i1(ptr, i1)",
		"declare void @osty_rt_list_push_f64(ptr, double)",
		"declare void @osty_rt_list_push_ptr(ptr, ptr)",
		"declare i64 @osty_rt_list_get_i64(ptr, i64)",
		"declare i1 @osty_rt_list_get_i1(ptr, i64)",
		"declare double @osty_rt_list_get_f64(ptr, i64)",
		"declare ptr @osty_rt_list_get_ptr(ptr, i64)",
		"declare void @osty_rt_list_set_i64(ptr, i64, i64)",
		"declare void @osty_rt_list_set_i1(ptr, i64, i1)",
		"declare void @osty_rt_list_set_f64(ptr, i64, double)",
		"declare void @osty_rt_list_set_ptr(ptr, i64, ptr)",
	}
}

func llvmListNew(emitter *LlvmEmitter) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_list_new", make([]*LlvmValue, 0))
}

func llvmListLen(emitter *LlvmEmitter, list *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_list_len", []*LlvmValue{list})
}

func llvmListPushI64(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_push_i64", []*LlvmValue{list, value})
}

func llvmListPushI1(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_push_i1", []*LlvmValue{list, value})
}

func llvmListPushF64(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_push_f64", []*LlvmValue{list, value})
}

func llvmListPushPtr(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_push_ptr", []*LlvmValue{list, value})
}

func llvmListPush(emitter *LlvmEmitter, list *LlvmValue, value *LlvmValue) {
	symbol := llvmListRuntimePushSymbol(llvmListElementSuffix(value.typ))
	llvmCallVoid(emitter, symbol, []*LlvmValue{list, value})
}

func llvmListGet(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, elemTyp string) *LlvmValue {
	symbol := llvmListRuntimeGetSymbol(llvmListElementSuffix(elemTyp))
	return llvmCall(emitter, elemTyp, symbol, []*LlvmValue{list, index})
}

func llvmListGetI64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_list_get_i64", []*LlvmValue{list, index})
}

func llvmListGetI1(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_list_get_i1", []*LlvmValue{list, index})
}

func llvmListGetF64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "double", "osty_rt_list_get_f64", []*LlvmValue{list, index})
}

func llvmListGetPtr(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_list_get_ptr", []*LlvmValue{list, index})
}

func llvmListSetI64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_set_i64", []*LlvmValue{list, index, value})
}

func llvmListSetI1(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_set_i1", []*LlvmValue{list, index, value})
}

func llvmListSetF64(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_set_f64", []*LlvmValue{list, index, value})
}

func llvmListSetPtr(emitter *LlvmEmitter, list *LlvmValue, index *LlvmValue, value *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_list_set_ptr", []*LlvmValue{list, index, value})
}

func llvmMapRuntimeDeclarations() []string {
	return []string{
		"declare ptr @osty_rt_map_new()",
		"declare i64 @osty_rt_map_len(ptr)",
		"declare ptr @osty_rt_map_keys(ptr)",
		"declare i1 @osty_rt_map_contains_i64(ptr, i64)",
		"declare i1 @osty_rt_map_contains_i1(ptr, i1)",
		"declare i1 @osty_rt_map_contains_f64(ptr, double)",
		"declare i1 @osty_rt_map_contains_ptr(ptr, ptr)",
		"declare i1 @osty_rt_map_contains_string(ptr, ptr)",
		"declare void @osty_rt_map_insert_i64(ptr, i64, ptr)",
		"declare void @osty_rt_map_insert_i1(ptr, i1, ptr)",
		"declare void @osty_rt_map_insert_f64(ptr, double, ptr)",
		"declare void @osty_rt_map_insert_ptr(ptr, ptr, ptr)",
		"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)",
		"declare i1 @osty_rt_map_remove_i64(ptr, i64)",
		"declare i1 @osty_rt_map_remove_i1(ptr, i1)",
		"declare i1 @osty_rt_map_remove_f64(ptr, double)",
		"declare i1 @osty_rt_map_remove_ptr(ptr, ptr)",
		"declare i1 @osty_rt_map_remove_string(ptr, ptr)",
		"declare void @osty_rt_map_get_or_abort_i64(ptr, i64, ptr)",
		"declare void @osty_rt_map_get_or_abort_i1(ptr, i1, ptr)",
		"declare void @osty_rt_map_get_or_abort_f64(ptr, double, ptr)",
		"declare void @osty_rt_map_get_or_abort_ptr(ptr, ptr, ptr)",
		"declare void @osty_rt_map_get_or_abort_string(ptr, ptr, ptr)",
	}
}

func llvmMapNew(emitter *LlvmEmitter) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_map_new", make([]*LlvmValue, 0))
}

func llvmMapLen(emitter *LlvmEmitter, m *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_map_len", []*LlvmValue{m})
}

func llvmMapKeys(emitter *LlvmEmitter, m *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_map_keys", []*LlvmValue{m})
}

func llvmMapContainsI64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_i64", []*LlvmValue{m, key})
}

func llvmMapContainsI1(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_i1", []*LlvmValue{m, key})
}

func llvmMapContainsF64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_f64", []*LlvmValue{m, key})
}

func llvmMapContainsPtr(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_ptr", []*LlvmValue{m, key})
}

func llvmMapContainsString(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_contains_string", []*LlvmValue{m, key})
}

func llvmMapInsertI64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_insert_i64", []*LlvmValue{m, key, valueSlot})
}

func llvmMapInsertI1(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_insert_i1", []*LlvmValue{m, key, valueSlot})
}

func llvmMapInsertF64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_insert_f64", []*LlvmValue{m, key, valueSlot})
}

func llvmMapInsertPtr(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_insert_ptr", []*LlvmValue{m, key, valueSlot})
}

func llvmMapInsertString(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, valueSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_insert_string", []*LlvmValue{m, key, valueSlot})
}

func llvmMapRemoveI64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_i64", []*LlvmValue{m, key})
}

func llvmMapRemoveI1(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_i1", []*LlvmValue{m, key})
}

func llvmMapRemoveF64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_f64", []*LlvmValue{m, key})
}

func llvmMapRemovePtr(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_ptr", []*LlvmValue{m, key})
}

func llvmMapRemoveString(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_map_remove_string", []*LlvmValue{m, key})
}

func llvmMapGetOrAbortI64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_i64", []*LlvmValue{m, key, outSlot})
}

func llvmMapGetOrAbortI1(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_i1", []*LlvmValue{m, key, outSlot})
}

func llvmMapGetOrAbortF64(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_f64", []*LlvmValue{m, key, outSlot})
}

func llvmMapGetOrAbortPtr(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_ptr", []*LlvmValue{m, key, outSlot})
}

func llvmMapGetOrAbortString(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, outSlot *LlvmValue) {
	llvmCallVoid(emitter, "osty_rt_map_get_or_abort_string", []*LlvmValue{m, key, outSlot})
}

func llvmMapContains(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, isString bool) *LlvmValue {
	symbol := llvmMapRuntimeContainsSymbol(key.typ, isString)
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{m, key})
}

func llvmMapInsert(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, valueSlot *LlvmValue, isString bool) {
	symbol := llvmMapRuntimeInsertSymbol(key.typ, isString)
	llvmCallVoid(emitter, symbol, []*LlvmValue{m, key, valueSlot})
}

func llvmMapRemove(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, isString bool) *LlvmValue {
	symbol := llvmMapRuntimeRemoveSymbol(key.typ, isString)
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{m, key})
}

func llvmMapGetOrAbort(emitter *LlvmEmitter, m *LlvmValue, key *LlvmValue, outSlot *LlvmValue, isString bool) {
	symbol := llvmMapRuntimeGetOrAbortSymbol(key.typ, isString)
	llvmCallVoid(emitter, symbol, []*LlvmValue{m, key, outSlot})
}

func llvmSetRuntimeDeclarations() []string {
	return []string{
		"declare ptr @osty_rt_set_new(i64)",
		"declare i64 @osty_rt_set_len(ptr)",
		"declare ptr @osty_rt_set_to_list(ptr)",
		"declare i1 @osty_rt_set_contains_i64(ptr, i64)",
		"declare i1 @osty_rt_set_contains_i1(ptr, i1)",
		"declare i1 @osty_rt_set_contains_f64(ptr, double)",
		"declare i1 @osty_rt_set_contains_ptr(ptr, ptr)",
		"declare i1 @osty_rt_set_contains_string(ptr, ptr)",
		"declare i1 @osty_rt_set_insert_i64(ptr, i64)",
		"declare i1 @osty_rt_set_insert_i1(ptr, i1)",
		"declare i1 @osty_rt_set_insert_f64(ptr, double)",
		"declare i1 @osty_rt_set_insert_ptr(ptr, ptr)",
		"declare i1 @osty_rt_set_insert_string(ptr, ptr)",
		"declare i1 @osty_rt_set_remove_i64(ptr, i64)",
		"declare i1 @osty_rt_set_remove_i1(ptr, i1)",
		"declare i1 @osty_rt_set_remove_f64(ptr, double)",
		"declare i1 @osty_rt_set_remove_ptr(ptr, ptr)",
		"declare i1 @osty_rt_set_remove_string(ptr, ptr)",
	}
}

func llvmSetNew(emitter *LlvmEmitter, elemKind int) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_set_new", []*LlvmValue{llvmIntLiteral(elemKind)})
}

func llvmSetLen(emitter *LlvmEmitter, set *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_set_len", []*LlvmValue{set})
}

func llvmSetToList(emitter *LlvmEmitter, set *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_set_to_list", []*LlvmValue{set})
}

func llvmSetContainsI64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_i64", []*LlvmValue{set, item})
}

func llvmSetContainsI1(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_i1", []*LlvmValue{set, item})
}

func llvmSetContainsF64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_f64", []*LlvmValue{set, item})
}

func llvmSetContainsPtr(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_ptr", []*LlvmValue{set, item})
}

func llvmSetContainsString(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_contains_string", []*LlvmValue{set, item})
}

func llvmSetInsertI64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_i64", []*LlvmValue{set, item})
}

func llvmSetInsertI1(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_i1", []*LlvmValue{set, item})
}

func llvmSetInsertF64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_f64", []*LlvmValue{set, item})
}

func llvmSetInsertPtr(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_ptr", []*LlvmValue{set, item})
}

func llvmSetInsertString(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_insert_string", []*LlvmValue{set, item})
}

func llvmSetRemoveI64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_i64", []*LlvmValue{set, item})
}

func llvmSetRemoveI1(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_i1", []*LlvmValue{set, item})
}

func llvmSetRemoveF64(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_f64", []*LlvmValue{set, item})
}

func llvmSetRemovePtr(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_ptr", []*LlvmValue{set, item})
}

func llvmSetRemoveString(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_set_remove_string", []*LlvmValue{set, item})
}

func llvmSetContains(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue, isString bool) *LlvmValue {
	symbol := llvmSetRuntimeContainsSymbol(item.typ, isString)
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{set, item})
}

func llvmSetInsert(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue, isString bool) *LlvmValue {
	symbol := llvmSetRuntimeInsertSymbol(item.typ, isString)
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{set, item})
}

func llvmSetRemove(emitter *LlvmEmitter, set *LlvmValue, item *LlvmValue, isString bool) *LlvmValue {
	symbol := llvmSetRuntimeRemoveSymbol(item.typ, isString)
	return llvmCall(emitter, "i1", symbol, []*LlvmValue{set, item})
}

func llvmChanElementSuffix(elemTyp string) string {
	if llvmListUsesTypedRuntime(elemTyp) {
		return llvmListElementSuffix(elemTyp)
	}
	return "bytes_v1"
}

func llvmChanRuntimeMakeSymbol() string {
	return "osty_rt_thread_chan_make"
}

func llvmChanRuntimeSendSymbol(suffix string) string {
	return "osty_rt_thread_chan_send_" + suffix
}

func llvmChanRuntimeSendBytesSymbol() string {
	return "osty_rt_thread_chan_send_bytes_v1"
}

func llvmChanRuntimeRecvSymbol(suffix string) string {
	return "osty_rt_thread_chan_recv_" + suffix
}

func llvmChanRuntimeCloseSymbol() string {
	return "osty_rt_thread_chan_close"
}

func llvmChanRuntimeIsClosedSymbol() string {
	return "osty_rt_thread_chan_is_closed"
}

func llvmChanRuntimeDeclarations() []string {
	return []string{
		"declare ptr @osty_rt_thread_chan_make(i64)",
		"declare void @osty_rt_thread_chan_close(ptr)",
		"declare i1 @osty_rt_thread_chan_is_closed(ptr)",
		"declare void @osty_rt_thread_chan_send_i64(ptr, i64)",
		"declare void @osty_rt_thread_chan_send_i1(ptr, i1)",
		"declare void @osty_rt_thread_chan_send_f64(ptr, double)",
		"declare void @osty_rt_thread_chan_send_ptr(ptr, ptr)",
		"declare void @osty_rt_thread_chan_send_bytes_v1(ptr, ptr, i64)",
		"declare { i64, i64 } @osty_rt_thread_chan_recv_i64(ptr)",
		"declare { i64, i64 } @osty_rt_thread_chan_recv_i1(ptr)",
		"declare { i64, i64 } @osty_rt_thread_chan_recv_f64(ptr)",
		"declare { i64, i64 } @osty_rt_thread_chan_recv_ptr(ptr)",
		"declare { i64, i64 } @osty_rt_thread_chan_recv_bytes_v1(ptr)",
	}
}

func llvmChanMake(emitter *LlvmEmitter, capacity *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", llvmChanRuntimeMakeSymbol(), []*LlvmValue{capacity})
}

func llvmChanSend(emitter *LlvmEmitter, channel *LlvmValue, value *LlvmValue) {
	symbol := llvmChanRuntimeSendSymbol(llvmListElementSuffix(value.typ))
	llvmCallVoid(emitter, symbol, []*LlvmValue{channel, value})
}

func llvmChanSendBytes(emitter *LlvmEmitter, channel *LlvmValue, slot *LlvmValue, size *LlvmValue) {
	llvmCallVoid(emitter, llvmChanRuntimeSendBytesSymbol(), []*LlvmValue{channel, slot, size})
}

func llvmChanRecv(emitter *LlvmEmitter, channel *LlvmValue, elemTyp string) *LlvmValue {
	symbol := llvmChanRuntimeRecvSymbol(llvmChanElementSuffix(elemTyp))
	return llvmCall(emitter, "{ i64, i64 }", symbol, []*LlvmValue{channel})
}

func llvmChanClose(emitter *LlvmEmitter, channel *LlvmValue) {
	llvmCallVoid(emitter, llvmChanRuntimeCloseSymbol(), []*LlvmValue{channel})
}

func llvmChanIsClosed(emitter *LlvmEmitter, channel *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", llvmChanRuntimeIsClosedSymbol(), []*LlvmValue{channel})
}

func llvmClosureEnvTypeName(elemTags []string) string {
	return "ClosureEnv." + llvmStrings.Join(elemTags, ".")
}

func llvmClosureEnvTypeDef(name string, elemTypes []string) string {
	return llvmStructTypeDef(name, elemTypes)
}

func llvmClosureEnvAlloc(emitter *LlvmEmitter, envTypeName string) *LlvmValue {
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %%%s", tmp, envTypeName))
	return &LlvmValue{typ: "ptr", name: tmp, pointer: false}
}

func llvmClosureEnvSlotGep(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, slotIndex int) string {
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr %%%s, ptr %s, i32 0, i32 %d", tmp, envTypeName, envPtr.name, slotIndex))
	return tmp
}

func llvmClosureEnvStoreFn(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, fnSymbol string) {
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, 0)
	emitter.body = append(emitter.body, fmt.Sprintf("  store ptr @%s, ptr %s", fnSymbol, gep))
}

func llvmClosureEnvStoreCapture(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, slotIndex int, value *LlvmValue) {
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, slotIndex)
	emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", value.typ, value.name, gep))
}

func llvmClosureEnvLoadFn(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string) *LlvmValue {
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, 0)
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load ptr, ptr %s", tmp, gep))
	return &LlvmValue{typ: "ptr", name: tmp, pointer: false}
}

func llvmClosureEnvLoadCapture(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, slotIndex int, captureType string) *LlvmValue {
	gep := llvmClosureEnvSlotGep(emitter, envPtr, envTypeName, slotIndex)
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", tmp, captureType, gep))
	return &LlvmValue{typ: captureType, name: tmp, pointer: false}
}

func llvmClosureCallIndirect(emitter *LlvmEmitter, envPtr *LlvmValue, envTypeName string, returnType string, extraArgs []*LlvmValue) *LlvmValue {
	fnPtr := llvmClosureEnvLoadFn(emitter, envPtr, envTypeName)
	args := []*LlvmValue{envPtr}
	paramTypes := []string{"ptr"}
	for _, a := range extraArgs {
		args = append(args, a)
		paramTypes = append(paramTypes, a.typ)
	}
	callType := returnType + " (" + llvmStrings.Join(paramTypes, ", ") + ")"
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s %s(%s)", tmp, callType, fnPtr.name, llvmCallArgs(args)))
	return &LlvmValue{typ: returnType, name: tmp, pointer: false}
}

func llvmClosureThunkName(symbol string) string {
	return "__osty_closure_thunk_" + symbol
}

func llvmClosureThunkDefinition(symbol string, returnType string, paramTypes []string) string {
	headerParts := []string{"ptr %env"}
	argParts := make([]string, 0, len(paramTypes))
	for i, p := range paramTypes {
		headerParts = append(headerParts, fmt.Sprintf("%s %%arg%d", p, i))
		argParts = append(argParts, fmt.Sprintf("%s %%arg%d", p, i))
	}
	header := llvmStrings.Join(headerParts, ", ")
	callArgs := llvmStrings.Join(argParts, ", ")
	thunk := llvmClosureThunkName(symbol)
	lines := []string{
		fmt.Sprintf("define private %s @%s(%s) {", returnType, thunk, header),
		"entry:",
	}
	if returnType == "void" {
		lines = append(lines, fmt.Sprintf("  call void @%s(%s)", symbol, callArgs))
		lines = append(lines, "  ret void")
	} else {
		lines = append(lines, fmt.Sprintf("  %%ret = call %s @%s(%s)", returnType, symbol, callArgs))
		lines = append(lines, fmt.Sprintf("  ret %s %%ret", returnType))
	}
	lines = append(lines, "}")
	return llvmStrings.Join(lines, "\n")
}

func llvmClosureBareFnEnv(emitter *LlvmEmitter, symbol string) *LlvmValue {
	envTypeName := llvmClosureEnvTypeName([]string{"ptr"})
	env := llvmClosureEnvAlloc(emitter, envTypeName)
	llvmClosureEnvStoreFn(emitter, env, envTypeName, llvmClosureThunkName(symbol))
	return env
}

func llvmClosureBareFnEnvTypeDef() string {
	return llvmClosureEnvTypeDef(llvmClosureEnvTypeName([]string{"ptr"}), []string{"ptr"})
}

func llvmStringRuntimeEqualSymbol() string {
	return "osty_rt_strings_Equal"
}

func llvmStringRuntimeHasPrefixSymbol() string {
	return "osty_rt_strings_HasPrefix"
}

func llvmStringRuntimeHasSuffixSymbol() string {
	return "osty_rt_strings_HasSuffix"
}

func llvmStringRuntimeContainsSymbol() string {
	return "osty_rt_strings_Contains"
}

func llvmStringRuntimeSplitSymbol() string {
	return "osty_rt_strings_Split"
}

func llvmStringRuntimeSplitNSymbol() string {
	return "osty_rt_strings_SplitN"
}

func llvmStringRuntimeConcatSymbol() string {
	return "osty_rt_strings_Concat"
}

func llvmIntRuntimeToStringSymbol() string {
	return "osty_rt_int_to_string"
}

func llvmFloatRuntimeToStringSymbol() string {
	return "osty_rt_float_to_string"
}

func llvmBoolRuntimeToStringSymbol() string {
	return "osty_rt_bool_to_string"
}

func llvmStringRuntimeByteLenSymbol() string {
	return "osty_rt_strings_ByteLen"
}

func llvmStringRuntimeCompareSymbol() string {
	return "osty_rt_strings_Compare"
}

func llvmStringRuntimeJoinSymbol() string {
	return "osty_rt_strings_Join"
}

func llvmStringRuntimeTrimPrefixSymbol() string {
	return "osty_rt_strings_TrimPrefix"
}

func llvmStringRuntimeTrimSuffixSymbol() string {
	return "osty_rt_strings_TrimSuffix"
}

func llvmStringRuntimeTrimSpaceSymbol() string {
	return "osty_rt_strings_TrimSpace"
}

func llvmStringRuntimeDeclarations() []string {
	return []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"declare i1 @osty_rt_strings_HasSuffix(ptr, ptr)",
		"declare i1 @osty_rt_strings_Contains(ptr, ptr)",
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare ptr @osty_rt_float_to_string(double)",
		"declare ptr @osty_rt_bool_to_string(i1)",
		"declare i64 @osty_rt_strings_ByteLen(ptr)",
		"declare i64 @osty_rt_strings_Compare(ptr, ptr)",
		"declare ptr @osty_rt_strings_Join(ptr, ptr)",
		"declare ptr @osty_rt_strings_TrimPrefix(ptr, ptr)",
		"declare ptr @osty_rt_strings_TrimSuffix(ptr, ptr)",
		"declare ptr @osty_rt_strings_TrimSpace(ptr)",
	}
}

func llvmStringEqual(emitter *LlvmEmitter, left *LlvmValue, right *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_strings_Equal", []*LlvmValue{left, right})
}

func llvmStringHasPrefix(emitter *LlvmEmitter, value *LlvmValue, prefix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_strings_HasPrefix", []*LlvmValue{value, prefix})
}

func llvmStringHasSuffix(emitter *LlvmEmitter, value *LlvmValue, suffix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i1", "osty_rt_strings_HasSuffix", []*LlvmValue{value, suffix})
}

func llvmStringSplit(emitter *LlvmEmitter, value *LlvmValue, sep *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Split", []*LlvmValue{value, sep})
}

func llvmStringConcat(emitter *LlvmEmitter, left *LlvmValue, right *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Concat", []*LlvmValue{left, right})
}

func llvmIntRuntimeToString(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_int_to_string", []*LlvmValue{value})
}

func llvmFloatRuntimeToString(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_float_to_string", []*LlvmValue{value})
}

func llvmBoolRuntimeToString(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_bool_to_string", []*LlvmValue{value})
}

func llvmStringCompare(emitter *LlvmEmitter, op string, left *LlvmValue, right *LlvmValue) *LlvmValue {
	if op == "==" {
		return llvmStringEqual(emitter, left, right)
	}
	if op == "!=" {
		return llvmNotI1(emitter, llvmStringEqual(emitter, left, right))
	}
	cmp := llvmStringRuntimeCompare(emitter, left, right)
	return llvmCompare(emitter, llvmIntComparePredicate(op), cmp, llvmIntLiteral(0))
}

func llvmStringByteLen(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_strings_ByteLen", []*LlvmValue{value})
}

func llvmStringRuntimeCompare(emitter *LlvmEmitter, left *LlvmValue, right *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "i64", "osty_rt_strings_Compare", []*LlvmValue{left, right})
}

func llvmStringRuntimeJoin(emitter *LlvmEmitter, parts *LlvmValue, sep *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_Join", []*LlvmValue{parts, sep})
}

func llvmStringRuntimeTrimPrefix(emitter *LlvmEmitter, value *LlvmValue, prefix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimPrefix", []*LlvmValue{value, prefix})
}

func llvmStringRuntimeTrimSuffix(emitter *LlvmEmitter, value *LlvmValue, suffix *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimSuffix", []*LlvmValue{value, suffix})
}

func llvmStringRuntimeTrimSpace(emitter *LlvmEmitter, value *LlvmValue) *LlvmValue {
	return llvmCall(emitter, "ptr", "osty_rt_strings_TrimSpace", []*LlvmValue{value})
}

func llvmBuiltinType(name string) string {
	switch name {
	case "Int":
		return "i64"
	case "Float":
		return "double"
	case "Bool":
		return "i1"
	case "String", "Bytes", "Error":
		return "ptr"
	default:
		return ""
	}
}

func llvmRuntimeAbiBuiltinType(name string) string {
	switch name {
	case "Int", "Float", "Bool", "String":
		return llvmBuiltinType(name)
	default:
		return ""
	}
}

func llvmEnumPayloadBuiltinType(name string) string {
	switch name {
	case "Int", "Float", "String":
		return llvmBuiltinType(name)
	default:
		return ""
	}
}

func llvmZeroLiteral(typ string) string {
	switch typ {
	case "double":
		return "0.0"
	case "ptr":
		return "null"
	default:
		return "0"
	}
}

func llvmStructTypeName(name string) string {
	return "%" + name
}

func llvmEnumStorageType(name string, hasPayload bool) string {
	if hasPayload {
		return llvmStructTypeName(name)
	}
	return "i64"
}

func llvmSignatureParamName(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("arg%d", index)
}

func llvmAllowsMainSignature(paramCount int, hasReturnType bool) bool {
	return paramCount == 0 && !hasReturnType
}

func llvmNamedType(name string, pathLen int, argLen int, structType string, enumType string) string {
	if pathLen != 1 || argLen != 0 {
		return "ptr"
	}
	if builtin := llvmBuiltinType(name); builtin != "" {
		return builtin
	}
	if structType != "" {
		return structType
	}
	if enumType != "" {
		return enumType
	}
	return ""
}

func llvmRuntimeAbiNamedType(name string, pathLen int, argLen int, structType string, enumType string) string {
	if pathLen == 1 && argLen == 0 {
		if builtin := llvmRuntimeAbiBuiltinType(name); builtin != "" {
			return builtin
		}
		if structType != "" {
			return structType
		}
		if enumType != "" {
			return enumType
		}
	}
	return "ptr"
}

func llvmEnumPayloadNamedType(name string, pathLen int, argLen int) string {
	if pathLen != 1 || argLen != 0 {
		return ""
	}
	return llvmEnumPayloadBuiltinType(name)
}

func llvmNominalDeclHeaderDiagnostic(kind string, name string, identOk bool, genericCount int, methodCount int) *LlvmUnsupportedDiagnostic {
	if !identOk {
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("%s name %q", kind, name))
	}
	if genericCount != 0 {
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("generic %s %q is not supported", kind, name))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

func llvmFunctionHeaderDiagnostic(name string, identOk bool, hasRecv bool, genericCount int, hasBody bool, isMain bool, paramCount int, hasReturnType bool) *LlvmUnsupportedDiagnostic {
	if !identOk {
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("function name %q", name))
	}
	if genericCount != 0 {
		return llvmUnsupportedDiagnostic("function-signature", "generic functions are not supported")
	}
	if !hasBody {
		return llvmUnsupportedDiagnostic("source-layout", fmt.Sprintf("function %q has no body", name))
	}
	if isMain && !llvmAllowsMainSignature(paramCount, hasReturnType) {
		return llvmUnsupportedDiagnostic("function-signature", "LLVM main must have no params and no return type")
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

func llvmIsRuntimeAbiListType(name string, pathLen int, argLen int) bool {
	return name == "List" && pathLen == 1 && argLen == 1
}

func llvmStructFieldDiagnostic(structName string, fieldName string, identOk bool, hasDefault bool, duplicate bool, recursive bool, detail string) *LlvmUnsupportedDiagnostic {
	if !identOk {
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("struct %q field name %q", structName, fieldName))
	}
	if hasDefault {
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("struct %q field %q has a default value", structName, fieldName))
	}
	if duplicate {
		return llvmUnsupportedDiagnostic("source-layout", fmt.Sprintf("struct %q duplicate field %q", structName, fieldName))
	}
	if detail != "" {
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("struct %q field %q: %s", structName, fieldName, detail))
	}
	if recursive {
		return llvmUnsupportedDiagnosticWith(
			"LLVM011",
			"type-system",
			fmt.Sprintf("struct %q recursive field %q requires indirection", structName, fieldName),
			"break the cycle via an arena index (Int id) or List<T> handle until the LLVM backend grows recursive-struct support",
		)
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

func llvmEnumVariantHeaderDiagnostic(enumName string, variantName string, identOk bool, payloadCount int, duplicate bool) *LlvmUnsupportedDiagnostic {
	if !identOk {
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("enum %q variant name %q", enumName, variantName))
	}
	if payloadCount > 1 {
		return llvmUnsupportedDiagnosticWith(
			"LLVM011",
			"type-system",
			fmt.Sprintf("enum %q variant %q has %d payload fields; the LLVM backend only supports a single scalar or pointer payload per variant", enumName, variantName, payloadCount),
			"flatten the variant to a single field (e.g. a tuple-wrapping struct passed by pointer) or adopt the arena + flat kind-discriminator pattern used by toolchain/core.osty",
		)
	}
	if duplicate {
		return llvmUnsupportedDiagnostic("source-layout", fmt.Sprintf("enum %q duplicate variant %q", enumName, variantName))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

func llvmEnumPayloadDiagnostic(enumName string, variantName string, detail string, expectedType string, actualType string) *LlvmUnsupportedDiagnostic {
	if detail != "" {
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("enum %q variant %q payload: %s", enumName, variantName, detail))
	}
	if expectedType != "" && actualType != "" && expectedType != actualType {
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("enum %q mixes payload types %s and %s; heterogeneous-payload enums require boxed representation (deferred)", enumName, expectedType, actualType))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

func llvmRuntimeFfiHeaderUnsupported(hasRecv bool, genericCount int) string {
	if hasRecv {
		return "methods are not supported"
	}
	if genericCount != 0 {
		return "generic functions are not supported"
	}
	return ""
}

func llvmRuntimeFfiReturnUnsupported(detail string) string {
	if detail != "" {
		return "return type: " + detail
	}
	return ""
}

func llvmRuntimeFfiParamUnsupported(name string, nilParam bool, hasPatternOrDefault bool, detail string) string {
	if nilParam {
		return "nil parameter"
	}
	if hasPatternOrDefault {
		return "pattern/default parameters are not supported"
	}
	if detail != "" {
		return fmt.Sprintf("parameter %q: %s", name, detail)
	}
	return ""
}

func llvmFunctionReturnDiagnostic(name string, detail string) *LlvmUnsupportedDiagnostic {
	if detail != "" {
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("function %q return type: %s", name, detail))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

func llvmFunctionParamDiagnostic(fnName string, paramName string, missingOrPattern bool, hasDefault bool, identOk bool, detail string) *LlvmUnsupportedDiagnostic {
	if missingOrPattern {
		return llvmUnsupportedDiagnostic("function-signature", fmt.Sprintf("function %q has non-identifier parameter", fnName))
	}
	if hasDefault {
		return llvmUnsupportedDiagnostic("function-signature", fmt.Sprintf("function %q has default parameter values", fnName))
	}
	if !identOk {
		return llvmUnsupportedDiagnostic("name", fmt.Sprintf("parameter name %q", paramName))
	}
	if detail != "" {
		return llvmUnsupportedDiagnostic("type-system", fmt.Sprintf("function %q parameter %q: %s", fnName, paramName, detail))
	}
	return llvmUnsupportedDiagnosticWith("", "", "", "")
}

// Osty: examples/selfhost-core/llvmgen.osty:875:5
func llvmSmokeExecutableCorpus() []*LlvmSmokeExecutableCase {
	return []*LlvmSmokeExecutableCase{&LlvmSmokeExecutableCase{name: "minimal", fixture: "minimal_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "scalar", fixture: "scalar_arithmetic.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "control", fixture: "control_flow.osty", stdout: "15\n"}, &LlvmSmokeExecutableCase{name: "booleans", fixture: "booleans.osty", stdout: "7\n"}, &LlvmSmokeExecutableCase{name: "string", fixture: "string_print.osty", stdout: "hello, osty\n"}, &LlvmSmokeExecutableCase{name: "string-escape", fixture: "string_escape_print.osty", stdout: "line one\nquote \" slash \\\n"}, &LlvmSmokeExecutableCase{name: "string-let", fixture: "string_let_print.osty", stdout: "stored string\n"}, &LlvmSmokeExecutableCase{name: "string-return", fixture: "string_return_print.osty", stdout: "from function\n"}, &LlvmSmokeExecutableCase{name: "string-param", fixture: "string_param_print.osty", stdout: "param string\n"}, &LlvmSmokeExecutableCase{name: "string-mut", fixture: "string_mut_print.osty", stdout: "after\n"}, &LlvmSmokeExecutableCase{name: "struct-field", fixture: "struct_field_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-return", fixture: "struct_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-param", fixture: "struct_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-mut", fixture: "struct_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-variant", fixture: "enum_variant_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-return", fixture: "enum_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-param", fixture: "enum_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-mut", fixture: "enum_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match", fixture: "enum_match_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match-return", fixture: "enum_match_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match-param", fixture: "enum_match_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-match-mut", fixture: "enum_match_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload", fixture: "enum_payload_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload-return", fixture: "enum_payload_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload-param", fixture: "enum_payload_param_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "enum-payload-mut", fixture: "enum_payload_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "float-print", fixture: "float_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-arithmetic", fixture: "float_arithmetic_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-return", fixture: "float_return_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-param", fixture: "float_param_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-mutable", fixture: "float_mut_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-compare", fixture: "float_compare_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-struct", fixture: "float_struct_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-enum-payload", fixture: "float_enum_payload_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-return", fixture: "float_payload_return_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-param", fixture: "float_payload_param_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-mut", fixture: "float_payload_mut_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-reversed", fixture: "float_payload_reversed_match_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "float-payload-wildcard", fixture: "float_payload_wildcard_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "string-payload-return", fixture: "string_payload_return_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-param", fixture: "string_payload_param_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-mut", fixture: "string_payload_mut_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-reversed", fixture: "string_payload_reversed_match_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "string-payload-wildcard", fixture: "string_payload_wildcard_print.osty", stdout: "payload string\n"}, &LlvmSmokeExecutableCase{name: "int-if-expr", fixture: "int_if_expr_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "string-if-expr", fixture: "string_if_expr_print.osty", stdout: "chosen string\n"}, &LlvmSmokeExecutableCase{name: "float-if-expr", fixture: "float_if_expr_print.osty", stdout: "42.000000\n"}, &LlvmSmokeExecutableCase{name: "bool-param-return", fixture: "bool_param_return_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "int-range-exclusive", fixture: "int_range_exclusive_print.osty", stdout: "21\n"}, &LlvmSmokeExecutableCase{name: "int-unary", fixture: "int_unary_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "int-modulo", fixture: "int_modulo_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "struct-string-field", fixture: "struct_string_field_print.osty", stdout: "struct string\n"}, &LlvmSmokeExecutableCase{name: "struct-bool-field", fixture: "struct_bool_field_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "bool-mut", fixture: "bool_mut_print.osty", stdout: "42\n"}, &LlvmSmokeExecutableCase{name: "result-question-int", fixture: "result_question_int_print.osty", stdout: "42\n"}}
}

// Osty: examples/selfhost-core/llvmgen.osty:1150:5
func llvmSmokeMinimalPrintIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1151:5
	emitter := llvmEmitter()
	_ = emitter
	// Osty: examples/selfhost-core/llvmgen.osty:1152:5
	value := llvmBinaryI64(emitter, "add", llvmIntLiteral(40), llvmIntLiteral(2))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1153:5
	llvmPrintlnI64(emitter, value)
	// Osty: examples/selfhost-core/llvmgen.osty:1154:5
	llvmReturnI32Zero(emitter)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), emitter.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1164:5
func llvmSmokeGcRuntimeAbiIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1165:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1166:5
	object := llvmGcAlloc(main, 1, 32, "llvm.gc.object")
	_ = object
	// Osty: examples/selfhost-core/llvmgen.osty:1167:5
	llvmGcRootBind(main, object)
	// Osty: examples/selfhost-core/llvmgen.osty:1168:5
	child := llvmGcAlloc(main, 2, 16, "llvm.gc.child")
	_ = child
	// Osty: examples/selfhost-core/llvmgen.osty:1169:5
	llvmGcPreWrite(main, object, child, 0)
	// Osty: examples/selfhost-core/llvmgen.osty:1170:5
	loaded := llvmGcLoad(main, child)
	_ = loaded
	// Osty: examples/selfhost-core/llvmgen.osty:1171:5
	llvmGcPostWrite(main, object, loaded, 0)
	// Osty: examples/selfhost-core/llvmgen.osty:1172:5
	llvmGcRootRelease(main, object)
	// Osty: examples/selfhost-core/llvmgen.osty:1173:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGcRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty (llvmSmokeListBasicIR)
func llvmSmokeListBasicIR(sourcePath string) string {
	main := llvmEmitter()
	list := llvmListNew(main)
	llvmListPushI64(main, list, llvmIntLiteral(10))
	llvmListPushI64(main, list, llvmIntLiteral(20))
	llvmListPushI64(main, list, llvmIntLiteral(30))
	length := llvmListLen(main, list)
	llvmPrintlnI64(main, length)
	second := llvmListGetI64(main, list, llvmIntLiteral(1))
	llvmPrintlnI64(main, second)
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithListRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty (llvmSmokeClosureThunkIR)
func llvmSmokeClosureThunkIR(sourcePath string) string {
	doubleBody := llvmEmitter()
	llvmBind(doubleBody, "x", &LlvmValue{typ: "i64", name: "%x", pointer: false})
	doubled := llvmBinaryI64(doubleBody, "mul", llvmIdent(doubleBody, "x"), llvmIntLiteral(2))
	llvmReturn(doubleBody, doubled)

	main := llvmEmitter()
	env := llvmClosureBareFnEnv(main, "double_val")
	envTypeName := llvmClosureEnvTypeName([]string{"ptr"})
	result := llvmClosureCallIndirect(main, env, envTypeName, "i64", []*LlvmValue{llvmIntLiteral(21)})
	llvmPrintlnI64(main, result)
	llvmReturnI32Zero(main)

	return llvmRenderModuleWithGlobalsAndTypes(
		sourcePath,
		"",
		[]string{llvmClosureBareFnEnvTypeDef()},
		main.stringGlobals,
		[]string{
			llvmRenderFunction("i64", "double_val", []*LlvmParam{llvmParam("x", "i64")}, doubleBody.body),
			llvmClosureThunkDefinition("double_val", "i64", []string{"i64"}),
			llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body),
		},
	)
}

// Osty: toolchain/llvmgen.osty (llvmSmokeClosureBasicIR)
func llvmSmokeClosureBasicIR(sourcePath string) string {
	envTypeName := llvmClosureEnvTypeName([]string{"ptr", "i64"})
	typeDef := llvmClosureEnvTypeDef(envTypeName, []string{"ptr", "i64"})

	body := llvmEmitter()
	llvmBind(body, "env", &LlvmValue{typ: "ptr", name: "%env", pointer: false})
	envArg := llvmIdent(body, "env")
	captured := llvmClosureEnvLoadCapture(body, envArg, envTypeName, 1, "i64")
	llvmReturn(body, captured)

	main := llvmEmitter()
	env := llvmClosureEnvAlloc(main, envTypeName)
	llvmClosureEnvStoreFn(main, env, envTypeName, "closure_body")
	llvmClosureEnvStoreCapture(main, env, envTypeName, 1, llvmIntLiteral(42))
	result := llvmClosureCallIndirect(main, env, envTypeName, "i64", []*LlvmValue{})
	llvmPrintlnI64(main, result)
	llvmReturnI32Zero(main)

	return llvmRenderModuleWithGlobalsAndTypes(
		sourcePath,
		"",
		[]string{typeDef},
		main.stringGlobals,
		[]string{
			llvmRenderFunction("i64", "closure_body", []*LlvmParam{llvmParam("env", "ptr")}, body.body),
			llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body),
		},
	)
}

// Osty: toolchain/llvmgen.osty (llvmSmokeSetBasicIR)
func llvmSmokeSetBasicIR(sourcePath string) string {
	main := llvmEmitter()
	set := llvmSetNew(main, llvmContainerAbiKind("i64", false))
	_ = llvmSetInsertI64(main, set, llvmIntLiteral(10))
	_ = llvmSetInsertI64(main, set, llvmIntLiteral(20))
	_ = llvmSetInsertI64(main, set, llvmIntLiteral(30))
	_ = llvmSetContainsI64(main, set, llvmIntLiteral(20))
	_ = llvmSetRemoveI64(main, set, llvmIntLiteral(20))
	llvmPrintlnI64(main, llvmSetLen(main, set))
	_ = llvmSetToList(main, set)
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithSetRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty (llvmSmokeStringConcatIR)
func llvmSmokeStringConcatIR(sourcePath string) string {
	main := llvmEmitter()
	left := llvmStringLiteral(main, "hello, ")
	right := llvmStringLiteral(main, "osty")
	joined := llvmStringConcat(main, left, right)
	_ = llvmStringHasPrefix(main, joined, left)
	length := llvmStringByteLen(main, joined)
	llvmPrintlnI64(main, length)
	llvmPrintlnString(main, joined)
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithStringRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: toolchain/llvmgen.osty (llvmSmokeMapBasicIR)
func llvmSmokeMapBasicIR(sourcePath string) string {
	main := llvmEmitter()
	m := llvmMapNew(main)
	valueSlot := llvmMutableLetSlot(main, "value", llvmIntLiteral(42))
	llvmMapInsertI64(main, m, llvmIntLiteral(7), llvmSlotAsPtr(valueSlot))
	_ = llvmMapContainsI64(main, m, llvmIntLiteral(7))
	outSlot := llvmMutableLetSlot(main, "out", llvmIntLiteral(0))
	llvmMapGetOrAbortI64(main, m, llvmIntLiteral(7), llvmSlotAsPtr(outSlot))
	llvmPrintlnI64(main, llvmLoad(main, outSlot))
	llvmPrintlnI64(main, llvmMapLen(main, m))
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithMapRuntime(sourcePath, "", make([]string, 0, 1), main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1184:5
func llvmSmokeScalarArithmeticIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1185:5
	add := llvmEmitter()
	_ = add
	// Osty: examples/selfhost-core/llvmgen.osty:1186:5
	llvmBind(add, "a", llvmI64("%a"))
	// Osty: examples/selfhost-core/llvmgen.osty:1187:5
	llvmBind(add, "b", llvmI64("%b"))
	// Osty: examples/selfhost-core/llvmgen.osty:1188:5
	sum := llvmBinaryI64(add, "add", llvmIdent(add, "a"), llvmIdent(add, "b"))
	_ = sum
	// Osty: examples/selfhost-core/llvmgen.osty:1189:5
	llvmReturn(add, sum)
	// Osty: examples/selfhost-core/llvmgen.osty:1191:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1192:5
	value := llvmCall(main, "i64", "add", []*LlvmValue{llvmIntLiteral(40), llvmIntLiteral(2)})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1193:5
	llvmImmutableLet(main, "value", value)
	// Osty: examples/selfhost-core/llvmgen.osty:1194:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "value"), llvmIntLiteral(42))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1195:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1196:5
	llvmPrintlnI64(main, llvmIdent(main, "value"))
	// Osty: examples/selfhost-core/llvmgen.osty:1197:5
	llvmIfElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1198:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:1199:5
	llvmIfEnd(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1200:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "add", []*LlvmParam{llvmParam("a", "i64"), llvmParam("b", "i64")}, add.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1220:5
func llvmSmokeControlFlowIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1221:5
	sumTo := llvmEmitter()
	_ = sumTo
	// Osty: examples/selfhost-core/llvmgen.osty:1222:5
	llvmBind(sumTo, "n", llvmI64("%n"))
	// Osty: examples/selfhost-core/llvmgen.osty:1223:5
	llvmMutableLet(sumTo, "total", llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:1224:5
	loop := llvmInclusiveRangeStart(sumTo, "i", llvmIntLiteral(1), llvmIdent(sumTo, "n"))
	_ = loop
	// Osty: examples/selfhost-core/llvmgen.osty:1225:5
	nextTotal := llvmBinaryI64(sumTo, "add", llvmIdent(sumTo, "total"), llvmIdent(sumTo, "i"))
	_ = nextTotal
	// Osty: examples/selfhost-core/llvmgen.osty:1226:5
	_ = llvmAssign(sumTo, "total", nextTotal)
	// Osty: examples/selfhost-core/llvmgen.osty:1227:5
	llvmRangeEnd(sumTo, loop)
	// Osty: examples/selfhost-core/llvmgen.osty:1228:5
	llvmReturn(sumTo, llvmIdent(sumTo, "total"))
	// Osty: examples/selfhost-core/llvmgen.osty:1230:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1231:5
	value := llvmCall(main, "i64", "sumTo", []*LlvmValue{llvmIntLiteral(5)})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1232:5
	llvmPrintlnI64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:1233:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "sumTo", []*LlvmParam{llvmParam("n", "i64")}, sumTo.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1245:5
func llvmSmokeBooleansIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1246:5
	choose := llvmEmitter()
	_ = choose
	// Osty: examples/selfhost-core/llvmgen.osty:1247:5
	llvmBind(choose, "a", llvmI64("%a"))
	// Osty: examples/selfhost-core/llvmgen.osty:1248:5
	llvmBind(choose, "b", llvmI64("%b"))
	// Osty: examples/selfhost-core/llvmgen.osty:1249:5
	lt := llvmCompare(choose, "slt", llvmIdent(choose, "a"), llvmIdent(choose, "b"))
	_ = lt
	// Osty: examples/selfhost-core/llvmgen.osty:1250:5
	eqZero := llvmCompare(choose, "eq", llvmIdent(choose, "a"), llvmIntLiteral(0))
	_ = eqZero
	// Osty: examples/selfhost-core/llvmgen.osty:1251:5
	nonZero := llvmNotI1(choose, eqZero)
	_ = nonZero
	// Osty: examples/selfhost-core/llvmgen.osty:1252:5
	cond := llvmLogicalI1(choose, "and", lt, nonZero)
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1253:5
	labels := llvmIfExprStart(choose, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1254:5
	thenValue := llvmBinaryI64(choose, "sub", llvmIdent(choose, "b"), llvmIdent(choose, "a"))
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1255:5
	llvmIfExprElse(choose, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1256:5
	elseValue := llvmBinaryI64(choose, "add", llvmIdent(choose, "a"), llvmIdent(choose, "b"))
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1257:5
	result := llvmIfExprEnd(choose, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1258:5
	llvmReturn(choose, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1260:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1261:5
	value := llvmCall(main, "i64", "choose", []*LlvmValue{llvmIntLiteral(3), llvmIntLiteral(10)})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1262:5
	llvmPrintlnI64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:1263:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "choose", []*LlvmParam{llvmParam("a", "i64"), llvmParam("b", "i64")}, choose.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1283:5
func llvmSmokeStringPrintIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1284:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1285:5
	line := llvmStringLiteral(main, "hello, osty")
	_ = line
	// Osty: examples/selfhost-core/llvmgen.osty:1286:5
	llvmPrintlnString(main, line)
	// Osty: examples/selfhost-core/llvmgen.osty:1287:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1299:5
func llvmSmokeStringEscapeIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1300:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1301:5
	line := llvmStringLiteral(main, "line one\nquote \" slash \\")
	_ = line
	// Osty: examples/selfhost-core/llvmgen.osty:1302:5
	llvmPrintlnString(main, line)
	// Osty: examples/selfhost-core/llvmgen.osty:1303:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1315:5
func llvmSmokeStringLetIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1316:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1317:5
	msg := llvmStringLiteral(main, "stored string")
	_ = msg
	// Osty: examples/selfhost-core/llvmgen.osty:1318:5
	llvmImmutableLet(main, "msg", msg)
	// Osty: examples/selfhost-core/llvmgen.osty:1319:5
	llvmPrintlnString(main, llvmIdent(main, "msg"))
	// Osty: examples/selfhost-core/llvmgen.osty:1320:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1332:5
func llvmSmokeStringReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1333:5
	greet := llvmEmitter()
	_ = greet
	// Osty: examples/selfhost-core/llvmgen.osty:1334:5
	llvmReturn(greet, llvmStringLiteral(greet, "from function"))
	// Osty: examples/selfhost-core/llvmgen.osty:1336:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1337:5
	llvmPrintlnString(main, llvmCall(main, "ptr", "greet", make([]*LlvmValue, 0, 1)))
	// Osty: examples/selfhost-core/llvmgen.osty:1338:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", greet.stringGlobals, []string{llvmRenderFunction("ptr", "greet", make([]*LlvmParam, 0, 1), greet.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1351:5
func llvmSmokeStringParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1352:5
	echo := llvmEmitter()
	_ = echo
	// Osty: examples/selfhost-core/llvmgen.osty:1353:5
	llvmBind(echo, "msg", &LlvmValue{typ: "ptr", name: "%msg", pointer: false})
	// Osty: examples/selfhost-core/llvmgen.osty:1354:5
	llvmReturn(echo, llvmIdent(echo, "msg"))
	// Osty: examples/selfhost-core/llvmgen.osty:1356:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1357:5
	value := llvmCall(main, "ptr", "echo", []*LlvmValue{llvmStringLiteral(main, "param string")})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1358:5
	llvmPrintlnString(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:1359:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("ptr", "echo", []*LlvmParam{llvmParam("msg", "ptr")}, echo.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1372:5
func llvmSmokeStringMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1373:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1374:5
	llvmMutableLet(main, "msg", llvmStringLiteral(main, "before"))
	// Osty: examples/selfhost-core/llvmgen.osty:1375:5
	_ = llvmAssign(main, "msg", llvmStringLiteral(main, "after"))
	// Osty: examples/selfhost-core/llvmgen.osty:1376:5
	llvmPrintlnString(main, llvmIdent(main, "msg"))
	// Osty: examples/selfhost-core/llvmgen.osty:1377:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1389:5
func llvmSmokeStructFieldIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1390:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1391:5
	point := llvmStructLiteral(main, "%Point", []*LlvmValue{llvmIntLiteral(40), llvmIntLiteral(2)})
	_ = point
	// Osty: examples/selfhost-core/llvmgen.osty:1392:5
	llvmImmutableLet(main, "point", point)
	// Osty: examples/selfhost-core/llvmgen.osty:1393:5
	sum := llvmBinaryI64(main, "add", llvmExtractValue(main, llvmIdent(main, "point"), "i64", 0), llvmExtractValue(main, llvmIdent(main, "point"), "i64", 1))
	_ = sum
	// Osty: examples/selfhost-core/llvmgen.osty:1399:5
	llvmPrintlnI64(main, sum)
	// Osty: examples/selfhost-core/llvmgen.osty:1400:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Point", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1413:5
func llvmSmokeStructReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1414:5
	makePair := llvmEmitter()
	_ = makePair
	// Osty: examples/selfhost-core/llvmgen.osty:1415:5
	pair := llvmStructLiteral(makePair, "%Pair", []*LlvmValue{llvmIntLiteral(10), llvmIntLiteral(32)})
	_ = pair
	// Osty: examples/selfhost-core/llvmgen.osty:1416:5
	llvmReturn(makePair, pair)
	// Osty: examples/selfhost-core/llvmgen.osty:1418:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1419:5
	returned := llvmCall(main, "%Pair", "makePair", make([]*LlvmValue, 0, 1))
	_ = returned
	// Osty: examples/selfhost-core/llvmgen.osty:1420:5
	llvmImmutableLet(main, "pair", returned)
	// Osty: examples/selfhost-core/llvmgen.osty:1421:5
	sum := llvmBinaryI64(main, "add", llvmExtractValue(main, llvmIdent(main, "pair"), "i64", 0), llvmExtractValue(main, llvmIdent(main, "pair"), "i64", 1))
	_ = sum
	// Osty: examples/selfhost-core/llvmgen.osty:1427:5
	llvmPrintlnI64(main, sum)
	// Osty: examples/selfhost-core/llvmgen.osty:1428:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Pair", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%Pair", "makePair", make([]*LlvmParam, 0, 1), makePair.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1442:5
func llvmSmokeStructParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1443:5
	total := llvmEmitter()
	_ = total
	// Osty: examples/selfhost-core/llvmgen.osty:1444:5
	llvmBind(total, "score", &LlvmValue{typ: "%Score", name: "%score", pointer: false})
	// Osty: examples/selfhost-core/llvmgen.osty:1445:5
	sum := llvmBinaryI64(total, "add", llvmExtractValue(total, llvmIdent(total, "score"), "i64", 0), llvmExtractValue(total, llvmIdent(total, "score"), "i64", 1))
	_ = sum
	// Osty: examples/selfhost-core/llvmgen.osty:1451:5
	llvmReturn(total, sum)
	// Osty: examples/selfhost-core/llvmgen.osty:1453:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1454:5
	score := llvmStructLiteral(main, "%Score", []*LlvmValue{llvmIntLiteral(40), llvmIntLiteral(2)})
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1455:5
	out := llvmCall(main, "i64", "total", []*LlvmValue{score})
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1456:5
	llvmPrintlnI64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1457:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Score", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i64", "total", []*LlvmParam{llvmParam("score", "%Score")}, total.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1471:5
func llvmSmokeStructMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1472:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1473:5
	llvmMutableLet(main, "box", llvmStructLiteral(main, "%Box", []*LlvmValue{llvmIntLiteral(1)}))
	// Osty: examples/selfhost-core/llvmgen.osty:1474:5
	_ = llvmAssign(main, "box", llvmStructLiteral(main, "%Box", []*LlvmValue{llvmIntLiteral(42)}))
	// Osty: examples/selfhost-core/llvmgen.osty:1475:5
	llvmPrintlnI64(main, llvmExtractValue(main, llvmIdent(main, "box"), "i64", 0))
	// Osty: examples/selfhost-core/llvmgen.osty:1476:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Box", []string{"i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1489:5
func llvmSmokeEnumVariantIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1490:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1491:5
	llvmImmutableLet(main, "light", llvmEnumVariant("Light", 1))
	// Osty: examples/selfhost-core/llvmgen.osty:1492:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "light"), llvmEnumVariant("Light", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1493:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1494:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: examples/selfhost-core/llvmgen.osty:1495:5
	llvmIfElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1496:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:1497:5
	llvmIfEnd(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1498:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1509:5
func llvmSmokeEnumReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1510:5
	pick := llvmEmitter()
	_ = pick
	// Osty: examples/selfhost-core/llvmgen.osty:1511:5
	llvmReturn(pick, llvmEnumVariant("Switch", 1))
	// Osty: examples/selfhost-core/llvmgen.osty:1513:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1514:5
	state := llvmCall(main, "i64", "pick", make([]*LlvmValue, 0, 1))
	_ = state
	// Osty: examples/selfhost-core/llvmgen.osty:1515:5
	cond := llvmCompare(main, "eq", state, llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1516:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1517:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: examples/selfhost-core/llvmgen.osty:1518:5
	llvmIfElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1519:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:1520:5
	llvmIfEnd(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1521:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1533:5
func llvmSmokeEnumParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1534:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1535:5
	llvmBind(score, "state", llvmI64("%state"))
	// Osty: examples/selfhost-core/llvmgen.osty:1536:5
	cond := llvmCompare(score, "eq", llvmIdent(score, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1537:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1538:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1539:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1540:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1541:5
	out := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1542:5
	llvmReturn(score, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1544:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1545:5
	result := llvmCall(main, "i64", "score", []*LlvmValue{llvmEnumVariant("Switch", 1)})
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1546:5
	llvmPrintlnI64(main, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1547:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "score", []*LlvmParam{llvmParam("state", "i64")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1559:5
func llvmSmokeEnumMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1560:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1561:5
	llvmMutableLet(main, "state", llvmEnumVariant("Switch", 0))
	// Osty: examples/selfhost-core/llvmgen.osty:1562:5
	_ = llvmAssign(main, "state", llvmEnumVariant("Switch", 1))
	// Osty: examples/selfhost-core/llvmgen.osty:1563:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1564:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1565:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: examples/selfhost-core/llvmgen.osty:1566:5
	llvmIfElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1567:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:1568:5
	llvmIfEnd(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1569:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1580:5
func llvmSmokeEnumMatchIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1581:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1582:5
	llvmImmutableLet(main, "state", llvmEnumVariant("Switch", 1))
	// Osty: examples/selfhost-core/llvmgen.osty:1583:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1584:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1585:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1586:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1587:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1588:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1589:5
	llvmPrintlnI64(main, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1590:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1601:5
func llvmSmokeEnumMatchReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1602:5
	pick := llvmEmitter()
	_ = pick
	// Osty: examples/selfhost-core/llvmgen.osty:1603:5
	llvmReturn(pick, llvmEnumVariant("Switch", 1))
	// Osty: examples/selfhost-core/llvmgen.osty:1605:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1606:5
	state := llvmCall(score, "i64", "pick", make([]*LlvmValue, 0, 1))
	_ = state
	// Osty: examples/selfhost-core/llvmgen.osty:1607:5
	cond := llvmCompare(score, "eq", state, llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1608:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1609:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1610:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1611:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1612:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1613:5
	llvmReturn(score, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1615:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1616:5
	out := llvmCall(main, "i64", "score", make([]*LlvmValue, 0, 1))
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1617:5
	llvmPrintlnI64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1618:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("i64", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1631:5
func llvmSmokeEnumMatchParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1632:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1633:5
	llvmBind(score, "state", llvmI64("%state"))
	// Osty: examples/selfhost-core/llvmgen.osty:1634:5
	cond := llvmCompare(score, "eq", llvmIdent(score, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1635:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1636:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1637:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1638:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1639:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1640:5
	llvmReturn(score, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1642:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1643:5
	out := llvmCall(main, "i64", "score", []*LlvmValue{llvmEnumVariant("Switch", 1)})
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1644:5
	llvmPrintlnI64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1645:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "score", []*LlvmParam{llvmParam("state", "i64")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1657:5
func llvmSmokeEnumMatchMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1658:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1659:5
	llvmMutableLet(main, "state", llvmEnumVariant("Switch", 0))
	// Osty: examples/selfhost-core/llvmgen.osty:1660:5
	_ = llvmAssign(main, "state", llvmEnumVariant("Switch", 1))
	// Osty: examples/selfhost-core/llvmgen.osty:1661:5
	cond := llvmCompare(main, "eq", llvmIdent(main, "state"), llvmEnumVariant("Switch", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1662:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1663:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1664:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1665:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1666:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1667:5
	llvmPrintlnI64(main, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1668:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1679:5
func llvmSmokeEnumPayloadIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1680:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1681:5
	llvmImmutableLet(main, "value", llvmEnumPayloadVariant(main, "%Maybe", 0, llvmIntLiteral(42)))
	// Osty: examples/selfhost-core/llvmgen.osty:1682:5
	state := llvmIdent(main, "value")
	_ = state
	// Osty: examples/selfhost-core/llvmgen.osty:1683:5
	tag := llvmExtractValue(main, state, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:1684:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1685:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1686:5
	thenValue := llvmExtractValue(main, state, "i64", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1687:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1688:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1689:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1690:5
	llvmPrintlnI64(main, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1691:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1704:5
func llvmSmokeEnumPayloadReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1705:5
	pick := llvmEmitter()
	_ = pick
	// Osty: examples/selfhost-core/llvmgen.osty:1706:5
	llvmReturn(pick, llvmEnumPayloadVariant(pick, "%Maybe", 0, llvmIntLiteral(42)))
	// Osty: examples/selfhost-core/llvmgen.osty:1708:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1709:5
	state := llvmCall(score, "%Maybe", "pick", make([]*LlvmValue, 0, 1))
	_ = state
	// Osty: examples/selfhost-core/llvmgen.osty:1710:5
	tag := llvmExtractValue(score, state, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:1711:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1712:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1713:5
	thenValue := llvmExtractValue(score, state, "i64", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1714:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1715:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1716:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1717:5
	llvmReturn(score, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1719:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1720:5
	out := llvmCall(main, "i64", "score", make([]*LlvmValue, 0, 1))
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1721:5
	llvmPrintlnI64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1722:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%Maybe", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("i64", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1737:5
func llvmSmokeEnumPayloadParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1738:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1739:5
	llvmBind(score, "value", &LlvmValue{typ: "%Maybe", name: "%value", pointer: false})
	// Osty: examples/selfhost-core/llvmgen.osty:1740:5
	state := llvmIdent(score, "value")
	_ = state
	// Osty: examples/selfhost-core/llvmgen.osty:1741:5
	tag := llvmExtractValue(score, state, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:1742:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1743:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1744:5
	thenValue := llvmExtractValue(score, state, "i64", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1745:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1746:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1747:5
	result := llvmIfExprEnd(score, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1748:5
	llvmReturn(score, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1750:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1751:5
	arg := llvmEnumPayloadVariant(main, "%Maybe", 0, llvmIntLiteral(42))
	_ = arg
	// Osty: examples/selfhost-core/llvmgen.osty:1752:5
	out := llvmCall(main, "i64", "score", []*LlvmValue{arg})
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1753:5
	llvmPrintlnI64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1754:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i64", "score", []*LlvmParam{llvmParam("value", "%Maybe")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1768:5
func llvmSmokeEnumPayloadMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1769:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1770:5
	llvmMutableLet(main, "value", llvmEnumPayloadVariant(main, "%Maybe", 1, llvmIntLiteral(0)))
	// Osty: examples/selfhost-core/llvmgen.osty:1771:5
	_ = llvmAssign(main, "value", llvmEnumPayloadVariant(main, "%Maybe", 0, llvmIntLiteral(42)))
	// Osty: examples/selfhost-core/llvmgen.osty:1772:5
	state := llvmIdent(main, "value")
	_ = state
	// Osty: examples/selfhost-core/llvmgen.osty:1773:5
	tag := llvmExtractValue(main, state, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:1774:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Maybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1775:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1776:5
	thenValue := llvmExtractValue(main, state, "i64", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1777:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1778:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1779:5
	result := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = result
	// Osty: examples/selfhost-core/llvmgen.osty:1780:5
	llvmPrintlnI64(main, result)
	// Osty: examples/selfhost-core/llvmgen.osty:1781:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Maybe", []string{"i64", "i64"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1794:5
func llvmSmokeFloatPrintIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1795:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1796:5
	llvmPrintlnF64(main, llvmFloatLiteral("42.0"))
	// Osty: examples/selfhost-core/llvmgen.osty:1797:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1806:5
func llvmSmokeFloatArithmeticIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1807:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1808:5
	add := llvmBinaryF64(main, "fadd", llvmFloatLiteral("40.0"), llvmFloatLiteral("2.0"))
	_ = add
	// Osty: examples/selfhost-core/llvmgen.osty:1809:5
	sub := llvmBinaryF64(main, "fsub", add, llvmFloatLiteral("0.0"))
	_ = sub
	// Osty: examples/selfhost-core/llvmgen.osty:1810:5
	mul := llvmBinaryF64(main, "fmul", sub, llvmFloatLiteral("2.5"))
	_ = mul
	// Osty: examples/selfhost-core/llvmgen.osty:1811:5
	div := llvmBinaryF64(main, "fdiv", mul, llvmFloatLiteral("2.5"))
	_ = div
	// Osty: examples/selfhost-core/llvmgen.osty:1812:5
	llvmPrintlnF64(main, div)
	// Osty: examples/selfhost-core/llvmgen.osty:1813:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1822:5
func llvmSmokeFloatReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1823:5
	value := llvmEmitter()
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1824:5
	llvmReturn(value, llvmFloatLiteral("42.0"))
	// Osty: examples/selfhost-core/llvmgen.osty:1826:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1827:5
	out := llvmCall(main, "double", "value", make([]*LlvmValue, 0, 1))
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1828:5
	llvmPrintlnF64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1829:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("double", "value", make([]*LlvmParam, 0, 1), value.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1841:5
func llvmSmokeFloatParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1842:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1843:5
	llvmBind(score, "value", llvmF64("%value"))
	// Osty: examples/selfhost-core/llvmgen.osty:1844:5
	llvmReturn(score, llvmIdent(score, "value"))
	// Osty: examples/selfhost-core/llvmgen.osty:1846:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1847:5
	out := llvmCall(main, "double", "score", []*LlvmValue{llvmFloatLiteral("42.0")})
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1848:5
	llvmPrintlnF64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1849:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("double", "score", []*LlvmParam{llvmParam("value", "double")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1861:5
func llvmSmokeFloatMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1862:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1863:5
	llvmMutableLet(main, "value", llvmFloatLiteral("0.0"))
	// Osty: examples/selfhost-core/llvmgen.osty:1864:5
	_ = llvmAssign(main, "value", llvmFloatLiteral("42.0"))
	// Osty: examples/selfhost-core/llvmgen.osty:1865:5
	llvmPrintlnF64(main, llvmIdent(main, "value"))
	// Osty: examples/selfhost-core/llvmgen.osty:1866:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1877:5
func llvmSmokeFloatCompareIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1878:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1879:5
	value := llvmFloatLiteral("42.0")
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1880:5
	eq := llvmCompareF64(main, "oeq", value, llvmFloatLiteral("42.0"))
	_ = eq
	// Osty: examples/selfhost-core/llvmgen.osty:1881:5
	ne := llvmCompareF64(main, "one", value, llvmFloatLiteral("41.0"))
	_ = ne
	// Osty: examples/selfhost-core/llvmgen.osty:1882:5
	lt := llvmCompareF64(main, "olt", value, llvmFloatLiteral("100.0"))
	_ = lt
	// Osty: examples/selfhost-core/llvmgen.osty:1883:5
	gt := llvmCompareF64(main, "ogt", value, llvmFloatLiteral("0.0"))
	_ = gt
	// Osty: examples/selfhost-core/llvmgen.osty:1884:5
	le := llvmCompareF64(main, "ole", value, llvmFloatLiteral("42.0"))
	_ = le
	// Osty: examples/selfhost-core/llvmgen.osty:1885:5
	ge := llvmCompareF64(main, "oge", value, llvmFloatLiteral("42.0"))
	_ = ge
	// Osty: examples/selfhost-core/llvmgen.osty:1886:5
	cond := llvmLogicalI1(main, "and", llvmLogicalI1(main, "and", llvmLogicalI1(main, "and", eq, ne), llvmLogicalI1(main, "and", lt, gt)), llvmLogicalI1(main, "and", le, ge))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1892:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1893:5
	llvmPrintlnF64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:1894:5
	llvmIfElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1895:5
	llvmPrintlnF64(main, llvmFloatLiteral("0.0"))
	// Osty: examples/selfhost-core/llvmgen.osty:1896:5
	llvmIfEnd(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1897:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1906:5
func llvmSmokeFloatStructIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1907:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1908:5
	value := llvmStructLiteral(main, "%MaybeF", []*LlvmValue{llvmIntLiteral(7), llvmFloatLiteral("42.0")})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1909:5
	llvmImmutableLet(main, "value", value)
	// Osty: examples/selfhost-core/llvmgen.osty:1910:5
	llvmPrintlnF64(main, llvmExtractValue(main, llvmIdent(main, "value"), "double", 1))
	// Osty: examples/selfhost-core/llvmgen.osty:1911:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("MaybeF", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1924:5
func llvmSmokeFloatEnumPayloadIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1925:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1926:5
	value := llvmEnumPayloadVariant(main, "%MaybeF", 0, llvmFloatLiteral("42.0"))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1927:5
	llvmImmutableLet(main, "value", value)
	// Osty: examples/selfhost-core/llvmgen.osty:1928:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:1929:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:1930:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("MaybeF", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1931:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1932:5
	thenValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1933:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1934:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1935:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1936:5
	llvmPrintlnF64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1937:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("MaybeF", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1950:5
func llvmSmokeFloatPayloadReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1951:5
	pick := llvmEmitter()
	_ = pick
	// Osty: examples/selfhost-core/llvmgen.osty:1952:5
	llvmReturn(pick, llvmEnumPayloadVariant(pick, "%FloatMaybe", 0, llvmFloatLiteral("42.0")))
	// Osty: examples/selfhost-core/llvmgen.osty:1954:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1955:5
	valueRef := llvmCall(score, "%FloatMaybe", "pick", make([]*LlvmValue, 0, 1))
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:1956:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:1957:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1958:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1959:5
	thenValue := llvmExtractValue(score, valueRef, "double", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1960:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1961:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1962:5
	out := llvmIfExprEnd(score, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1963:5
	llvmReturn(score, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1965:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1966:5
	value := llvmCall(main, "double", "score", make([]*LlvmValue, 0, 1))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:1967:5
	llvmPrintlnF64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:1968:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%FloatMaybe", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("double", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:1983:5
func llvmSmokeFloatPayloadParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:1984:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:1985:5
	llvmBind(score, "value", &LlvmValue{typ: "%FloatMaybe", name: "%value", pointer: false})
	// Osty: examples/selfhost-core/llvmgen.osty:1986:5
	valueRef := llvmIdent(score, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:1987:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:1988:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:1989:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:1990:5
	thenValue := llvmExtractValue(score, valueRef, "double", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:1991:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:1992:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:1993:5
	out := llvmIfExprEnd(score, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:1994:5
	llvmReturn(score, out)
	// Osty: examples/selfhost-core/llvmgen.osty:1996:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:1997:5
	arg := llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0"))
	_ = arg
	// Osty: examples/selfhost-core/llvmgen.osty:1998:5
	mainOut := llvmCall(main, "double", "score", []*LlvmValue{arg})
	_ = mainOut
	// Osty: examples/selfhost-core/llvmgen.osty:1999:5
	llvmPrintlnF64(main, mainOut)
	// Osty: examples/selfhost-core/llvmgen.osty:2000:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("double", "score", []*LlvmParam{llvmParam("value", "%FloatMaybe")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2014:5
func llvmSmokeFloatPayloadMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2015:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2016:5
	llvmMutableLet(main, "value", llvmEnumPayloadVariant(main, "%FloatMaybe", 1, llvmFloatLiteral("0.0")))
	// Osty: examples/selfhost-core/llvmgen.osty:2017:5
	_ = llvmAssign(main, "value", llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0")))
	// Osty: examples/selfhost-core/llvmgen.osty:2018:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2019:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2020:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2021:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2022:5
	thenValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2023:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2024:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2025:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2026:5
	llvmPrintlnF64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2027:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2040:5
func llvmSmokeFloatPayloadReversedMatchIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2041:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2042:5
	value := llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0"))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2043:5
	llvmImmutableLet(main, "value", value)
	// Osty: examples/selfhost-core/llvmgen.osty:2044:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2045:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2046:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("FloatMaybe", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2047:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2048:5
	thenValue := llvmFloatLiteral("0.0")
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2049:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2050:5
	elseValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2051:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2052:5
	llvmPrintlnF64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2053:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2066:5
func llvmSmokeFloatPayloadWildcardIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2067:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2068:5
	value := llvmEnumPayloadVariant(main, "%FloatMaybe", 0, llvmFloatLiteral("42.0"))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2069:5
	llvmImmutableLet(main, "value", value)
	// Osty: examples/selfhost-core/llvmgen.osty:2070:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2071:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2072:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("FloatMaybe", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2073:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2074:5
	thenValue := llvmExtractValue(main, valueRef, "double", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2075:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2076:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2077:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2078:5
	llvmPrintlnF64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2079:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("FloatMaybe", []string{"i64", "double"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2092:5
func llvmSmokeStringPayloadReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2093:5
	pick := llvmEmitter()
	_ = pick
	// Osty: examples/selfhost-core/llvmgen.osty:2094:5
	llvmReturn(pick, llvmEnumPayloadVariant(pick, "%Label", 0, llvmStringLiteral(pick, "payload string")))
	// Osty: examples/selfhost-core/llvmgen.osty:2096:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:2097:5
	valueRef := llvmCall(score, "%Label", "pick", make([]*LlvmValue, 0, 1))
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2098:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2099:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2100:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2101:5
	thenValue := llvmExtractValue(score, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2102:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2103:5
	elseValue := llvmStringLiteral(score, "no payload")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2104:5
	out := llvmIfExprEnd(score, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2105:5
	llvmReturn(score, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2107:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2108:5
	value := llvmCall(main, "ptr", "score", make([]*LlvmValue, 0, 1))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2109:5
	llvmPrintlnString(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:2110:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("%Label", "pick", make([]*LlvmParam, 0, 1), pick.body), llvmRenderFunction("ptr", "score", make([]*LlvmParam, 0, 1), score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2125:5
func llvmSmokeStringPayloadParamIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2126:5
	score := llvmEmitter()
	_ = score
	// Osty: examples/selfhost-core/llvmgen.osty:2127:5
	llvmBind(score, "value", &LlvmValue{typ: "%Label", name: "%value", pointer: false})
	// Osty: examples/selfhost-core/llvmgen.osty:2128:5
	valueRef := llvmIdent(score, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2129:5
	tag := llvmExtractValue(score, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2130:5
	cond := llvmCompare(score, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2131:5
	labels := llvmIfExprStart(score, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2132:5
	thenValue := llvmExtractValue(score, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2133:5
	llvmIfExprElse(score, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2134:5
	elseValue := llvmStringLiteral(score, "no payload")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2135:5
	out := llvmIfExprEnd(score, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2136:5
	llvmReturn(score, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2138:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2139:5
	arg := llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string"))
	_ = arg
	// Osty: examples/selfhost-core/llvmgen.osty:2140:5
	value := llvmCall(main, "ptr", "score", []*LlvmValue{arg})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2141:5
	llvmPrintlnString(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:2142:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("ptr", "score", []*LlvmParam{llvmParam("value", "%Label")}, score.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2156:5
func llvmSmokeStringPayloadMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2157:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2158:5
	llvmMutableLet(main, "value", llvmEnumPayloadVariant(main, "%Label", 1, llvmStringLiteral(main, "no payload")))
	// Osty: examples/selfhost-core/llvmgen.osty:2159:5
	_ = llvmAssign(main, "value", llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string")))
	// Osty: examples/selfhost-core/llvmgen.osty:2160:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2161:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2162:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2163:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2164:5
	thenValue := llvmExtractValue(main, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2165:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2166:5
	elseValue := llvmStringLiteral(main, "no payload")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2167:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2168:5
	llvmPrintlnString(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2169:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2182:5
func llvmSmokeStringPayloadReversedMatchIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2183:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2184:5
	value := llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string"))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2185:5
	llvmImmutableLet(main, "value", value)
	// Osty: examples/selfhost-core/llvmgen.osty:2186:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2187:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2188:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Label", 1))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2189:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2190:5
	thenValue := llvmStringLiteral(main, "no payload")
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2191:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2192:5
	elseValue := llvmExtractValue(main, valueRef, "ptr", 1)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2193:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2194:5
	llvmPrintlnString(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2195:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2208:5
func llvmSmokeStringPayloadWildcardIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2209:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2210:5
	value := llvmEnumPayloadVariant(main, "%Label", 0, llvmStringLiteral(main, "payload string"))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2211:5
	llvmImmutableLet(main, "value", value)
	// Osty: examples/selfhost-core/llvmgen.osty:2212:5
	valueRef := llvmIdent(main, "value")
	_ = valueRef
	// Osty: examples/selfhost-core/llvmgen.osty:2213:5
	tag := llvmExtractValue(main, valueRef, "i64", 0)
	_ = tag
	// Osty: examples/selfhost-core/llvmgen.osty:2214:5
	cond := llvmCompare(main, "eq", tag, llvmEnumVariant("Label", 0))
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2215:5
	labels := llvmIfExprStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2216:5
	thenValue := llvmExtractValue(main, valueRef, "ptr", 1)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2217:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2218:5
	elseValue := llvmStringLiteral(main, "no payload")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2219:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2220:5
	llvmPrintlnString(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2221:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Label", []string{"i64", "ptr"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2234:5
func llvmSmokeIntIfExprIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2235:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2236:5
	labels := llvmIfExprStart(main, llvmI1("true"))
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2237:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2238:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2239:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2240:5
	out := llvmIfExprEnd(main, "i64", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2241:5
	llvmPrintlnI64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2242:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2253:5
func llvmSmokeStringIfExprIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2254:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2255:5
	labels := llvmIfExprStart(main, llvmI1("true"))
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2256:5
	thenValue := llvmStringLiteral(main, "chosen string")
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2257:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2258:5
	elseValue := llvmStringLiteral(main, "fallback")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2259:5
	out := llvmIfExprEnd(main, "ptr", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2260:5
	llvmPrintlnString(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2261:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobals(sourcePath, "", main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2273:5
func llvmSmokeFloatIfExprIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2274:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2275:5
	labels := llvmIfExprStart(main, llvmI1("true"))
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2276:5
	thenValue := llvmFloatLiteral("42.0")
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2277:5
	llvmIfExprElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2278:5
	elseValue := llvmFloatLiteral("0.0")
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2279:5
	out := llvmIfExprEnd(main, "double", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2280:5
	llvmPrintlnF64(main, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2281:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2292:5
func llvmSmokeBoolParamReturnIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2293:5
	pick := llvmEmitter()
	_ = pick
	// Osty: examples/selfhost-core/llvmgen.osty:2294:5
	llvmBind(pick, "flag", llvmI1("%flag"))
	// Osty: examples/selfhost-core/llvmgen.osty:2295:5
	labels := llvmIfExprStart(pick, llvmIdent(pick, "flag"))
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2296:5
	thenValue := llvmIntLiteral(42)
	_ = thenValue
	// Osty: examples/selfhost-core/llvmgen.osty:2297:5
	llvmIfExprElse(pick, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2298:5
	elseValue := llvmIntLiteral(0)
	_ = elseValue
	// Osty: examples/selfhost-core/llvmgen.osty:2299:5
	out := llvmIfExprEnd(pick, "i64", thenValue, elseValue, labels)
	_ = out
	// Osty: examples/selfhost-core/llvmgen.osty:2300:5
	llvmReturn(pick, out)
	// Osty: examples/selfhost-core/llvmgen.osty:2302:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2303:5
	value := llvmCall(main, "i64", "pick", []*LlvmValue{llvmI1("true")})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2304:5
	llvmPrintlnI64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:2305:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "pick", []*LlvmParam{llvmParam("flag", "i1")}, pick.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2317:5
func llvmSmokeIntRangeExclusiveIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2318:5
	sumTo := llvmEmitter()
	_ = sumTo
	// Osty: examples/selfhost-core/llvmgen.osty:2319:5
	llvmBind(sumTo, "n", llvmI64("%n"))
	// Osty: examples/selfhost-core/llvmgen.osty:2320:5
	llvmMutableLet(sumTo, "total", llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:2321:5
	loop := llvmRangeStart(sumTo, "i", llvmIntLiteral(0), llvmIdent(sumTo, "n"), false)
	_ = loop
	// Osty: examples/selfhost-core/llvmgen.osty:2322:5
	nextTotal := llvmBinaryI64(sumTo, "add", llvmIdent(sumTo, "total"), llvmIdent(sumTo, "i"))
	_ = nextTotal
	// Osty: examples/selfhost-core/llvmgen.osty:2323:5
	_ = llvmAssign(sumTo, "total", nextTotal)
	// Osty: examples/selfhost-core/llvmgen.osty:2324:5
	llvmRangeEnd(sumTo, loop)
	// Osty: examples/selfhost-core/llvmgen.osty:2325:5
	llvmReturn(sumTo, llvmIdent(sumTo, "total"))
	// Osty: examples/selfhost-core/llvmgen.osty:2327:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2328:5
	value := llvmCall(main, "i64", "sumTo", []*LlvmValue{llvmIntLiteral(7)})
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2329:5
	llvmPrintlnI64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:2330:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i64", "sumTo", []*LlvmParam{llvmParam("n", "i64")}, sumTo.body), llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2342:5
func llvmSmokeIntUnaryIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2343:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2344:5
	diff := llvmBinaryI64(main, "sub", llvmIntLiteral(40), llvmIntLiteral(82))
	_ = diff
	// Osty: examples/selfhost-core/llvmgen.osty:2345:5
	value := llvmBinaryI64(main, "sub", llvmIntLiteral(0), diff)
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2346:5
	llvmPrintlnI64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:2347:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2358:5
func llvmSmokeIntModuloIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2359:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2360:5
	value := llvmBinaryI64(main, "srem", llvmIntLiteral(85), llvmIntLiteral(43))
	_ = value
	// Osty: examples/selfhost-core/llvmgen.osty:2361:5
	llvmPrintlnI64(main, value)
	// Osty: examples/selfhost-core/llvmgen.osty:2362:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2373:5
func llvmSmokeStructStringFieldIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2374:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2375:5
	msg := llvmStructLiteral(main, "%Message", []*LlvmValue{llvmStringLiteral(main, "struct string")})
	_ = msg
	// Osty: examples/selfhost-core/llvmgen.osty:2376:5
	llvmImmutableLet(main, "msg", msg)
	// Osty: examples/selfhost-core/llvmgen.osty:2377:5
	llvmPrintlnString(main, llvmExtractValue(main, llvmIdent(main, "msg"), "ptr", 0))
	// Osty: examples/selfhost-core/llvmgen.osty:2378:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Message", []string{"ptr"})}, main.stringGlobals, []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2391:5
func llvmSmokeStructBoolFieldIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2392:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2393:5
	gate := llvmStructLiteral(main, "%Gate", []*LlvmValue{llvmI1("true")})
	_ = gate
	// Osty: examples/selfhost-core/llvmgen.osty:2394:5
	llvmImmutableLet(main, "gate", gate)
	// Osty: examples/selfhost-core/llvmgen.osty:2395:5
	cond := llvmExtractValue(main, llvmIdent(main, "gate"), "i1", 0)
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2396:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2397:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: examples/selfhost-core/llvmgen.osty:2398:5
	llvmIfElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2399:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:2400:5
	llvmIfEnd(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2401:5
	llvmReturnI32Zero(main)
	return llvmRenderModuleWithGlobalsAndTypes(sourcePath, "", []string{llvmStructTypeDef("Gate", []string{"i1"})}, make([]*LlvmStringGlobal, 0, 1), []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2414:5
func llvmSmokeBoolMutableIR(sourcePath string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2415:5
	main := llvmEmitter()
	_ = main
	// Osty: examples/selfhost-core/llvmgen.osty:2416:5
	llvmMutableLet(main, "flag", llvmI1("false"))
	// Osty: examples/selfhost-core/llvmgen.osty:2417:5
	_ = llvmAssign(main, "flag", llvmI1("true"))
	// Osty: examples/selfhost-core/llvmgen.osty:2418:5
	cond := llvmIdent(main, "flag")
	_ = cond
	// Osty: examples/selfhost-core/llvmgen.osty:2419:5
	labels := llvmIfStart(main, cond)
	_ = labels
	// Osty: examples/selfhost-core/llvmgen.osty:2420:5
	llvmPrintlnI64(main, llvmIntLiteral(42))
	// Osty: examples/selfhost-core/llvmgen.osty:2421:5
	llvmIfElse(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2422:5
	llvmPrintlnI64(main, llvmIntLiteral(0))
	// Osty: examples/selfhost-core/llvmgen.osty:2423:5
	llvmIfEnd(main, labels)
	// Osty: examples/selfhost-core/llvmgen.osty:2424:5
	llvmReturnI32Zero(main)
	return llvmRenderModule(sourcePath, "", []string{llvmRenderFunction("i32", "main", make([]*LlvmParam, 0, 1), main.body)})
}

// Osty: examples/selfhost-core/llvmgen.osty:2435:1
func llvmCallArgs(args []*LlvmValue) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2436:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: examples/selfhost-core/llvmgen.osty:2437:5
	for _, arg := range args {
		// Osty: examples/selfhost-core/llvmgen.osty:2438:9
		func() struct{} {
			parts = append(parts, fmt.Sprintf("%s %s", ostyToString(arg.typ), ostyToString(arg.name)))
			return struct{}{}
		}()
	}
	return llvmStrings.Join(parts, ", ")
}

// Osty: examples/selfhost-core/llvmgen.osty:2443:1
func llvmParams(params []*LlvmParam) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2444:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: examples/selfhost-core/llvmgen.osty:2445:5
	for _, param := range params {
		// Osty: examples/selfhost-core/llvmgen.osty:2446:9
		func() struct{} {
			parts = append(parts, fmt.Sprintf("%s %%%s", ostyToString(param.typ), ostyToString(param.name)))
			return struct{}{}
		}()
	}
	return llvmStrings.Join(parts, ", ")
}

// Osty: examples/selfhost-core/llvmgen.osty:2451:1
func llvmNextTemp(emitter *LlvmEmitter) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2452:5
	name := fmt.Sprintf("%%t%s", ostyToString(emitter.temp))
	_ = name
	// Osty: examples/selfhost-core/llvmgen.osty:2453:12
	emitter.temp = func() int {
		var _p7 int = emitter.temp
		var _rhs8 int = 1
		if _rhs8 > 0 && _p7 > math.MaxInt-_rhs8 {
			panic("integer overflow")
		}
		if _rhs8 < 0 && _p7 < math.MinInt-_rhs8 {
			panic("integer overflow")
		}
		return _p7 + _rhs8
	}()
	return name
}

// Osty: examples/selfhost-core/llvmgen.osty:2457:1
func llvmNextLabel(emitter *LlvmEmitter, prefix string) string {
	// Osty: examples/selfhost-core/llvmgen.osty:2458:5
	name := fmt.Sprintf("%s%s", ostyToString(prefix), ostyToString(emitter.label))
	_ = name
	// Osty: examples/selfhost-core/llvmgen.osty:2459:12
	emitter.label = func() int {
		var _p9 int = emitter.label
		var _rhs10 int = 1
		if _rhs10 > 0 && _p9 > math.MaxInt-_rhs10 {
			panic("integer overflow")
		}
		if _rhs10 < 0 && _p9 < math.MinInt-_rhs10 {
			panic("integer overflow")
		}
		return _p9 + _rhs10
	}()
	return name
}
