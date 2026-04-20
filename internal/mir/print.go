package mir

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Print renders a MIR Module in a stable, human-readable form used by
// tests and for inspection during backend development. The format is
// deliberately close to Rust MIR dumps so the shape is recognisable.
func Print(m *Module) string {
	var p printer
	p.printModule(m)
	return p.b.String()
}

// PrintFunction renders a single function using the same format as
// Print.
func PrintFunction(f *Function) string {
	var p printer
	p.printFunction(f)
	return p.b.String()
}

type printer struct {
	b      strings.Builder
	indent int
}

func (p *printer) pad() {
	for i := 0; i < p.indent; i++ {
		p.b.WriteString("    ")
	}
}

func (p *printer) line(format string, args ...any) {
	p.pad()
	fmt.Fprintf(&p.b, format, args...)
	p.b.WriteByte('\n')
}

func (p *printer) printModule(m *Module) {
	if m == nil {
		p.b.WriteString("(nil module)\n")
		return
	}
	p.line("module %q", m.Package)
	if len(m.Uses) > 0 {
		p.indent++
		for _, u := range m.Uses {
			p.printUse(u)
		}
		p.indent--
	}
	if m.Layouts != nil {
		p.printLayouts(m.Layouts)
	}
	for _, g := range m.Globals {
		p.b.WriteByte('\n')
		p.printGlobal(g)
	}
	for _, fn := range m.Functions {
		p.b.WriteByte('\n')
		p.printFunction(fn)
	}
}

func (p *printer) printUse(u *Use) {
	if u == nil {
		return
	}
	switch {
	case u.IsGoFFI:
		p.line("use go %q as %s", u.GoPath, u.Alias)
	case u.IsRuntimeFFI:
		p.line("use runtime %q as %s", u.RuntimePath, u.Alias)
	default:
		path := u.RawPath
		if path == "" {
			path = strings.Join(u.Path, ".")
		}
		p.line("use %s as %s", path, u.Alias)
	}
}

func (p *printer) printLayouts(tab *LayoutTable) {
	names := make([]string, 0, len(tab.Structs))
	for name := range tab.Structs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sl := tab.Structs[name]
		p.b.WriteByte('\n')
		p.line("layout struct %s (mangled=%s) {", sl.Name, orEmpty(sl.Mangled))
		p.indent++
		for _, f := range sl.Fields {
			p.line("[%d] %s: %s", f.Index, f.Name, typeString(f.Type))
		}
		p.indent--
		p.line("}")
	}
	enumNames := make([]string, 0, len(tab.Enums))
	for name := range tab.Enums {
		enumNames = append(enumNames, name)
	}
	sort.Strings(enumNames)
	for _, name := range enumNames {
		el := tab.Enums[name]
		p.b.WriteByte('\n')
		p.line("layout enum %s (mangled=%s, disc=%s) {", el.Name, orEmpty(el.Mangled), typeString(el.Discriminant))
		p.indent++
		for _, v := range el.Variants {
			if len(v.Payload) == 0 {
				p.line("variant %d %s", v.Index, v.Name)
				continue
			}
			parts := make([]string, len(v.Payload))
			for i, f := range v.Payload {
				parts[i] = typeString(f.Type)
			}
			p.line("variant %d %s(%s)", v.Index, v.Name, strings.Join(parts, ", "))
		}
		p.indent--
		p.line("}")
	}
	tupleKeys := make([]string, 0, len(tab.Tuples))
	for key := range tab.Tuples {
		tupleKeys = append(tupleKeys, key)
	}
	sort.Strings(tupleKeys)
	for _, key := range tupleKeys {
		tl := tab.Tuples[key]
		parts := make([]string, len(tl.Fields))
		for i, f := range tl.Fields {
			parts[i] = typeString(f.Type)
		}
		p.b.WriteByte('\n')
		p.line("layout tuple %s (mangled=%s) { %s }", tl.Key, orEmpty(tl.Mangled), strings.Join(parts, ", "))
	}
}

func (p *printer) printGlobal(g *Global) {
	if g == nil {
		return
	}
	mut := ""
	if g.Mut {
		mut = " mut"
	}
	p.line("global%s %s: %s", mut, g.Name, typeString(g.Type))
	if g.Init != nil {
		p.indent++
		p.printFunction(g.Init)
		p.indent--
	}
}

func (p *printer) printFunction(f *Function) {
	if f == nil {
		p.b.WriteString("(nil fn)\n")
		return
	}
	kind := "fn"
	switch {
	case f.IsExternal:
		kind = "extern fn"
	case f.IsIntrinsic:
		kind = "intrinsic fn"
	}
	exported := ""
	if f.Exported {
		exported = " pub"
	}
	paramParts := make([]string, len(f.Params))
	for i, pid := range f.Params {
		loc := f.Local(pid)
		if loc == nil {
			paramParts[i] = fmt.Sprintf("_%d", pid)
			continue
		}
		paramParts[i] = fmt.Sprintf("_%d: %s", pid, typeString(loc.Type))
	}
	p.line("%s%s %s(%s) -> %s {", kind, exported, f.Name, strings.Join(paramParts, ", "), typeString(f.ReturnType))
	p.indent++
	if len(f.Locals) > 0 {
		// emit local declarations in id order, skipping params (already
		// described in the signature) to keep the dump short.
		for _, loc := range f.Locals {
			if loc == nil {
				continue
			}
			if loc.IsParam {
				continue
			}
			annot := ""
			if loc.Mut {
				annot = " mut"
			}
			if loc.IsReturn {
				annot = " return"
			}
			name := loc.Name
			if name == "" {
				name = "_"
			}
			p.line("let%s _%d: %s  // %s", annot, loc.ID, typeString(loc.Type), name)
		}
	}
	if !f.IsExternal {
		for _, bb := range f.Blocks {
			p.b.WriteByte('\n')
			p.printBlock(bb, f)
		}
	}
	p.indent--
	p.line("}")
}

func (p *printer) printBlock(bb *BasicBlock, f *Function) {
	if bb == nil {
		return
	}
	p.line("bb%d:", int(bb.ID))
	p.indent++
	for _, instr := range bb.Instrs {
		p.printInstr(instr, f)
	}
	if bb.Term != nil {
		p.printTerm(bb.Term, f)
	} else {
		p.line("<missing terminator>")
	}
	p.indent--
}

func (p *printer) printInstr(instr Instr, f *Function) {
	switch x := instr.(type) {
	case *AssignInstr:
		p.line("%s = %s", placeString(x.Dest, f), rvalueString(x.Src, f))
	case *CallInstr:
		dest := ""
		if x.Dest != nil {
			dest = placeString(*x.Dest, f) + " = "
		}
		callee := calleeString(x.Callee, f)
		args := operandsString(x.Args, f)
		p.line("%scall %s(%s)", dest, callee, args)
	case *IntrinsicInstr:
		dest := ""
		if x.Dest != nil {
			dest = placeString(*x.Dest, f) + " = "
		}
		args := operandsString(x.Args, f)
		p.line("%sintrinsic %s(%s)", dest, intrinsicName(x.Kind), args)
	case *StorageLiveInstr:
		p.line("storage_live _%d", int(x.Local))
	case *StorageDeadInstr:
		p.line("storage_dead _%d", int(x.Local))
	default:
		p.line("(unknown instr %T)", instr)
	}
}

func (p *printer) printTerm(t Terminator, f *Function) {
	switch x := t.(type) {
	case *GotoTerm:
		p.line("goto -> bb%d", int(x.Target))
	case *BranchTerm:
		p.line("branch %s -> [true: bb%d, false: bb%d]", operandString(x.Cond, f), int(x.Then), int(x.Else))
	case *SwitchIntTerm:
		parts := make([]string, 0, len(x.Cases)+1)
		for _, c := range x.Cases {
			label := c.Label
			if label == "" {
				label = strconv.FormatInt(c.Value, 10)
			}
			parts = append(parts, fmt.Sprintf("%s => bb%d", label, int(c.Target)))
		}
		parts = append(parts, fmt.Sprintf("_ => bb%d", int(x.Default)))
		p.line("switchInt %s -> [%s]", operandString(x.Scrutinee, f), strings.Join(parts, ", "))
	case *ReturnTerm:
		p.line("return")
	case *UnreachableTerm:
		p.line("unreachable")
	default:
		p.line("(unknown terminator %T)", t)
	}
}

// ==== value rendering ====

func placeString(p Place, f *Function) string {
	var b strings.Builder
	fmt.Fprintf(&b, "_%d", int(p.Local))
	for _, proj := range p.Projections {
		b.WriteString(projectionString(proj, f))
	}
	return b.String()
}

func projectionString(proj Projection, f *Function) string {
	switch x := proj.(type) {
	case *FieldProj:
		if x.Name != "" {
			return "." + x.Name
		}
		return fmt.Sprintf(".field%d", x.Index)
	case *TupleProj:
		return fmt.Sprintf(".%d", x.Index)
	case *VariantProj:
		if x.FieldIdx < 0 {
			return "@" + x.Name
		}
		return fmt.Sprintf("@%s.%d", x.Name, x.FieldIdx)
	case *IndexProj:
		return "[" + operandString(x.Index, f) + "]"
	case *DerefProj:
		return ".*"
	}
	return "(?)"
}

func operandString(op Operand, f *Function) string {
	switch x := op.(type) {
	case *CopyOp:
		return placeString(x.Place, f)
	case *MoveOp:
		return "move " + placeString(x.Place, f)
	case *ConstOp:
		return constString(x.Const)
	}
	return "(?)"
}

func operandsString(ops []Operand, f *Function) string {
	parts := make([]string, len(ops))
	for i, op := range ops {
		parts[i] = operandString(op, f)
	}
	return strings.Join(parts, ", ")
}

func rvalueString(rv RValue, f *Function) string {
	switch x := rv.(type) {
	case *UseRV:
		return "use " + operandString(x.Op, f)
	case *UnaryRV:
		return fmt.Sprintf("%s %s", unaryOpName(x.Op), operandString(x.Arg, f))
	case *BinaryRV:
		return fmt.Sprintf("%s %s %s", operandString(x.Left, f), binaryOpName(x.Op), operandString(x.Right, f))
	case *AggregateRV:
		kind := aggregateKindName(x.Kind)
		if x.Kind == AggEnumVariant {
			tag := x.VariantTag
			if tag == "" {
				tag = strconv.Itoa(x.VariantIdx)
			}
			return fmt.Sprintf("aggregate %s %s(%s)", kind, tag, operandsString(x.Fields, f))
		}
		return fmt.Sprintf("aggregate %s(%s)", kind, operandsString(x.Fields, f))
	case *DiscriminantRV:
		return "discriminant " + placeString(x.Place, f)
	case *LenRV:
		return "len " + placeString(x.Place, f)
	case *CastRV:
		return fmt.Sprintf("cast %s %s as %s", castKindName(x.Kind), operandString(x.Arg, f), typeString(x.To))
	case *AddressOfRV:
		return "&" + placeString(x.Place, f)
	case *RefRV:
		return "ref " + placeString(x.Place, f)
	case *NullaryRV:
		return fmt.Sprintf("%s %s", nullaryKindName(x.Kind), typeString(x.T))
	case *GlobalRefRV:
		return fmt.Sprintf("global %s %s", x.Name, typeString(x.T))
	}
	return "(?)"
}

func calleeString(c Callee, f *Function) string {
	switch x := c.(type) {
	case *FnRef:
		return x.Symbol
	case *IndirectCall:
		return "*" + operandString(x.Callee, f)
	}
	return "(?)"
}

func constString(c Const) string {
	switch x := c.(type) {
	case *IntConst:
		return fmt.Sprintf("const %d %s", x.Value, typeString(x.Type()))
	case *BoolConst:
		if x.Value {
			return "const true"
		}
		return "const false"
	case *FloatConst:
		return fmt.Sprintf("const %g %s", x.Value, typeString(x.Type()))
	case *StringConst:
		return fmt.Sprintf("const %q", x.Value)
	case *CharConst:
		return fmt.Sprintf("const '%c'", x.Value)
	case *ByteConst:
		return fmt.Sprintf("const 0x%02x", x.Value)
	case *UnitConst:
		return "const ()"
	case *NullConst:
		return "const none " + typeString(x.Type())
	case *FnConst:
		return "const fn " + x.Symbol
	}
	return "(?)"
}

// ==== enum-style name tables ====

func intrinsicName(k IntrinsicKind) string {
	switch k {
	case IntrinsicPrint:
		return "print"
	case IntrinsicPrintln:
		return "println"
	case IntrinsicEprint:
		return "eprint"
	case IntrinsicEprintln:
		return "eprintln"
	case IntrinsicAbort:
		return "abort"
	case IntrinsicStringConcat:
		return "string_concat"
	case IntrinsicChanMake:
		return "chan_make"
	case IntrinsicChanSend:
		return "chan_send"
	case IntrinsicChanRecv:
		return "chan_recv"
	case IntrinsicChanClose:
		return "chan_close"
	case IntrinsicChanIsClosed:
		return "chan_is_closed"
	case IntrinsicTaskGroup:
		return "task_group"
	case IntrinsicSpawn:
		return "spawn"
	case IntrinsicHandleJoin:
		return "handle_join"
	case IntrinsicGroupCancel:
		return "group_cancel"
	case IntrinsicGroupIsCancelled:
		return "group_is_cancelled"
	case IntrinsicParallel:
		return "parallel"
	case IntrinsicRace:
		return "race"
	case IntrinsicCollectAll:
		return "collect_all"
	case IntrinsicSelect:
		return "select"
	case IntrinsicSelectRecv:
		return "select_recv"
	case IntrinsicSelectSend:
		return "select_send"
	case IntrinsicSelectTimeout:
		return "select_timeout"
	case IntrinsicSelectDefault:
		return "select_default"
	case IntrinsicIsCancelled:
		return "is_cancelled"
	case IntrinsicCheckCancelled:
		return "check_cancelled"
	case IntrinsicYield:
		return "yield"
	case IntrinsicSleep:
		return "sleep"

	// ---- stdlib collections ----
	case IntrinsicListPush:
		return "list_push"
	case IntrinsicListLen:
		return "list_len"
	case IntrinsicListGet:
		return "list_get"
	case IntrinsicListIsEmpty:
		return "list_is_empty"
	case IntrinsicListFirst:
		return "list_first"
	case IntrinsicListLast:
		return "list_last"
	case IntrinsicListSorted:
		return "list_sorted"
	case IntrinsicListContains:
		return "list_contains"
	case IntrinsicListIndexOf:
		return "list_index_of"
	case IntrinsicListToSet:
		return "list_to_set"
	case IntrinsicMapNew:
		return "map_new"
	case IntrinsicMapGet:
		return "map_get"
	case IntrinsicMapSet:
		return "map_set"
	case IntrinsicMapContains:
		return "map_contains"
	case IntrinsicMapLen:
		return "map_len"
	case IntrinsicMapKeys:
		return "map_keys"
	case IntrinsicMapValues:
		return "map_values"
	case IntrinsicMapRemove:
		return "map_remove"
	case IntrinsicSetNew:
		return "set_new"
	case IntrinsicSetInsert:
		return "set_insert"
	case IntrinsicSetContains:
		return "set_contains"
	case IntrinsicSetLen:
		return "set_len"
	case IntrinsicSetToList:
		return "set_to_list"
	case IntrinsicSetRemove:
		return "set_remove"
	case IntrinsicStringLen:
		return "string_len"
	case IntrinsicStringIsEmpty:
		return "string_is_empty"
	case IntrinsicStringContains:
		return "string_contains"
	case IntrinsicStringStartsWith:
		return "string_starts_with"
	case IntrinsicStringEndsWith:
		return "string_ends_with"
	case IntrinsicStringIndexOf:
		return "string_index_of"
	case IntrinsicStringSplit:
		return "string_split"
	case IntrinsicStringTrim:
		return "string_trim"
	case IntrinsicStringToUpper:
		return "string_to_upper"
	case IntrinsicStringToLower:
		return "string_to_lower"
	case IntrinsicStringReplace:
		return "string_replace"
	case IntrinsicStringChars:
		return "string_chars"
	case IntrinsicStringBytes:
		return "string_bytes"
	case IntrinsicBytesLen:
		return "bytes_len"
	case IntrinsicBytesIsEmpty:
		return "bytes_is_empty"
	case IntrinsicBytesGet:
		return "bytes_get"
	case IntrinsicOptionIsSome:
		return "option_is_some"
	case IntrinsicOptionIsNone:
		return "option_is_none"
	case IntrinsicOptionUnwrap:
		return "option_unwrap"
	case IntrinsicOptionUnwrapOr:
		return "option_unwrap_or"
	case IntrinsicResultIsOk:
		return "result_is_ok"
	case IntrinsicResultIsErr:
		return "result_is_err"
	case IntrinsicResultUnwrap:
		return "result_unwrap"
	case IntrinsicResultUnwrapOr:
		return "result_unwrap_or"
	}
	return "invalid"
}

func unaryOpName(op UnaryOp) string {
	switch op {
	case UnNeg:
		return "-"
	case UnPlus:
		return "+"
	case UnNot:
		return "!"
	case UnBitNot:
		return "~"
	}
	return "?"
}

func binaryOpName(op BinaryOp) string {
	switch op {
	case BinAdd:
		return "+"
	case BinSub:
		return "-"
	case BinMul:
		return "*"
	case BinDiv:
		return "/"
	case BinMod:
		return "%"
	case BinEq:
		return "=="
	case BinNeq:
		return "!="
	case BinLt:
		return "<"
	case BinLeq:
		return "<="
	case BinGt:
		return ">"
	case BinGeq:
		return ">="
	case BinAnd:
		return "&&"
	case BinOr:
		return "||"
	case BinBitAnd:
		return "&"
	case BinBitOr:
		return "|"
	case BinBitXor:
		return "^"
	case BinShl:
		return "<<"
	case BinShr:
		return ">>"
	}
	return "?"
}

func aggregateKindName(k AggregateKind) string {
	switch k {
	case AggTuple:
		return "tuple"
	case AggStruct:
		return "struct"
	case AggEnumVariant:
		return "variant"
	case AggList:
		return "list"
	case AggMap:
		return "map"
	case AggClosure:
		return "closure"
	}
	return "?"
}

func castKindName(k CastKind) string {
	switch k {
	case CastIntResize:
		return "int_resize"
	case CastIntToFloat:
		return "int_to_float"
	case CastFloatToInt:
		return "float_to_int"
	case CastFloatResize:
		return "float_resize"
	case CastOptionalWrap:
		return "optional_wrap"
	case CastOptionalUnwrap:
		return "optional_unwrap"
	case CastBitcast:
		return "bitcast"
	}
	return "?"
}

func nullaryKindName(k NullaryRVKind) string {
	switch k {
	case NullaryNone:
		return "none"
	}
	return "?"
}

// ==== misc helpers ====

func typeString(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

func orEmpty(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
