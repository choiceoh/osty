package lirproto

import (
	"fmt"
	"strings"
)

// Validate checks Phase-1 structural invariants for a LIR Proto module. It is
// intentionally shallow: it catches malformed plan nodes before rendering, but
// leaves MIR semantic parity, ABI compatibility, dominance, and SSA-like
// analyses to later phases.
func Validate(m Module) []error {
	var v validator
	v.module(m)
	return v.errs
}

type validator struct {
	errs []error
}

func (v *validator) addf(format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf(format, args...))
}

func (v *validator) module(m Module) {
	for i, td := range m.TypeDefs {
		if td.Name == "" {
			v.addf("type def[%d]: missing name", i)
		}
		if td.Body == "" {
			v.addf("type def[%d] %q: missing body", i, td.Name)
		}
	}
	for i, g := range m.Globals {
		if g.Name == "" {
			v.addf("global[%d]: missing name", i)
		}
		v.typeValue(fmt.Sprintf("global[%d] %q type", i, g.Name), g.Type, false)
	}
	for _, decl := range m.RuntimeDecls.Ordered() {
		v.runtimeDecl(decl)
	}
	for i, fn := range m.Functions {
		v.function(i, fn)
	}
}

func (v *validator) runtimeDecl(d RuntimeDecl) {
	ctx := "runtime decl"
	if d.Symbol != "" {
		ctx += " @" + d.Symbol
	}
	if d.Symbol == "" {
		v.addf("%s: missing symbol", ctx)
	}
	v.typeValue(ctx+" return", d.Return, true)
	for i, p := range d.Params {
		v.typeValue(fmt.Sprintf("%s param[%d]", ctx, i), p, false)
	}
}

func (v *validator) function(idx int, fn Function) {
	ctx := fmt.Sprintf("function[%d]", idx)
	if fn.Name != "" {
		ctx += " @" + fn.Name
	}
	if fn.Name == "" {
		v.addf("%s: missing name", ctx)
	}
	if !fn.Return.IsZero() {
		v.typeValue(ctx+" return", fn.Return, true)
	}
	for i, p := range fn.Params {
		v.typeValue(fmt.Sprintf("%s param[%d]", ctx, i), p.Type, false)
	}
	if len(fn.Blocks) == 0 {
		v.addf("%s: missing blocks", ctx)
		return
	}
	labels := map[string]int{}
	for i, bb := range fn.Blocks {
		label := bb.Label
		if label == "" {
			label = "entry"
		}
		if prev, ok := labels[label]; ok {
			v.addf("%s block[%d] %q: duplicate label first used by block[%d]", ctx, i, label, prev)
		} else {
			labels[label] = i
		}
		v.block(ctx, i, bb)
	}
}

func (v *validator) block(fnCtx string, idx int, bb Block) {
	ctx := fmt.Sprintf("%s block[%d]", fnCtx, idx)
	for i, instr := range bb.Instrs {
		if instr == nil {
			v.addf("%s instr[%d]: nil instruction", ctx, i)
			continue
		}
		v.instr(ctx, i, instr)
	}
	if bb.Term == nil {
		v.addf("%s: missing terminator", ctx)
		return
	}
	v.term(ctx, bb.Term)
}

func (v *validator) instr(blockCtx string, idx int, instr Instr) {
	ctx := fmt.Sprintf("%s instr[%d]", blockCtx, idx)
	switch x := instr.(type) {
	case Alloca:
		v.named(ctx, "dest", x.Dest)
		v.typeValue(ctx+" type", x.Type, false)
		v.align(ctx, x.Align)
	case Load:
		v.named(ctx, "dest", x.Dest)
		v.typeValue(ctx+" type", x.Type, false)
		v.named(ctx, "ptr", x.Ptr)
		v.align(ctx, x.Align)
	case Store:
		v.operand(ctx+" value", x.Value)
		v.named(ctx, "ptr", x.Ptr)
		v.align(ctx, x.Align)
	case Binary:
		v.named(ctx, "dest", x.Dest)
		v.named(ctx, "op", x.Op)
		v.typeValue(ctx+" type", x.Type, false)
		v.named(ctx, "left", x.Left)
		v.named(ctx, "right", x.Right)
	case Unary:
		v.named(ctx, "dest", x.Dest)
		v.named(ctx, "op", x.Op)
		v.typeValue(ctx+" type", x.Type, false)
		v.named(ctx, "value", x.Value)
	case Call:
		if !x.Return.IsZero() {
			v.typeValue(ctx+" return", x.Return, true)
		}
		v.named(ctx, "callee", x.Callee)
		for i, arg := range x.Args {
			v.operand(fmt.Sprintf("%s arg[%d]", ctx, i), arg)
		}
	case Cast:
		v.named(ctx, "dest", x.Dest)
		v.named(ctx, "op", x.Op)
		v.operand(ctx+" from", x.From)
		v.typeValue(ctx+" to", x.To, false)
	case InsertValue:
		v.named(ctx, "dest", x.Dest)
		v.typeValue(ctx+" type", x.Type, false)
		v.named(ctx, "aggregate", x.Aggregate)
		v.operand(ctx+" value", x.Value)
		v.indices(ctx, x.Indices)
	case ExtractValue:
		v.named(ctx, "dest", x.Dest)
		v.typeValue(ctx+" type", x.Type, false)
		v.named(ctx, "aggregate", x.Aggregate)
		v.indices(ctx, x.Indices)
	case Gep:
		v.named(ctx, "dest", x.Dest)
		v.typeValue(ctx+" elem type", x.ElemType, false)
		v.named(ctx, "ptr", x.Ptr)
		if len(x.Indices) == 0 {
			v.addf("%s: missing indices", ctx)
		}
		for i, index := range x.Indices {
			v.operand(fmt.Sprintf("%s index[%d]", ctx, i), index)
		}
	case Comment:
		// Comments are optional and may be empty.
	default:
		v.addf("%s: unknown instruction %T", ctx, instr)
	}
}

func (v *validator) term(blockCtx string, term Term) {
	switch x := term.(type) {
	case Ret:
		if x.Type.IsZero() {
			if x.Value != "" {
				v.addf("%s terminator: ret value %q without type", blockCtx, x.Value)
			}
			return
		}
		v.typeValue(blockCtx+" terminator ret type", x.Type, false)
		v.named(blockCtx+" terminator", "ret value", x.Value)
	case Br:
		v.named(blockCtx+" terminator", "target", x.Target)
	case CondBr:
		v.named(blockCtx+" terminator", "cond", x.Cond)
		v.named(blockCtx+" terminator", "then", x.Then)
		v.named(blockCtx+" terminator", "else", x.Else)
	case Switch:
		v.typeValue(blockCtx+" terminator switch type", x.Type, false)
		v.named(blockCtx+" terminator", "scrutinee", x.Scrutinee)
		v.named(blockCtx+" terminator", "default", x.Default)
		for i, c := range x.Cases {
			if c.Value == "" {
				v.addf("%s terminator switch case[%d]: missing value", blockCtx, i)
			}
			if c.Label == "" {
				v.addf("%s terminator switch case[%d]: missing label", blockCtx, i)
			}
		}
	case Unreachable:
		return
	default:
		v.addf("%s terminator: unknown terminator %T", blockCtx, term)
	}
}

func (v *validator) operand(ctx string, op Operand) {
	v.typeValue(ctx+" type", op.Type, false)
	v.named(ctx, "value", op.Value)
}

func (v *validator) typeValue(ctx string, t Type, allowVoid bool) {
	if t.IsZero() {
		v.addf("%s: missing type", ctx)
		return
	}
	if t.LLVM == "" {
		v.addf("%s: missing LLVM spelling", ctx)
		return
	}
	switch t.Class {
	case TypeUnknown:
		return
	case TypeVoid:
		if t.LLVM != "void" {
			v.addf("%s: class void has LLVM spelling %q", ctx, t.LLVM)
		}
		if !allowVoid {
			v.addf("%s: void is not allowed here", ctx)
		}
	case TypeInt:
		if !isIntLLVM(t.LLVM) {
			v.addf("%s: class int has LLVM spelling %q", ctx, t.LLVM)
		}
	case TypeFloat:
		if !isFloatLLVM(t.LLVM) {
			v.addf("%s: class float has LLVM spelling %q", ctx, t.LLVM)
		}
	case TypePtr:
		if t.LLVM != "ptr" {
			v.addf("%s: class ptr has LLVM spelling %q", ctx, t.LLVM)
		}
	case TypeAggregate:
		if !isAggregateLLVM(t.LLVM) {
			v.addf("%s: class aggregate has LLVM spelling %q", ctx, t.LLVM)
		}
	case TypeFunction:
		if !strings.Contains(t.LLVM, "(") {
			v.addf("%s: class function has LLVM spelling %q", ctx, t.LLVM)
		}
	default:
		v.addf("%s: unknown type class %d", ctx, t.Class)
	}
}

func (v *validator) named(ctx, field, value string) {
	if value == "" {
		v.addf("%s: missing %s", ctx, field)
	}
}

func (v *validator) align(ctx string, align int) {
	if align < 0 {
		v.addf("%s: negative align %d", ctx, align)
	}
}

func (v *validator) indices(ctx string, indices []int) {
	if len(indices) == 0 {
		v.addf("%s: missing indices", ctx)
	}
	for i, idx := range indices {
		if idx < 0 {
			v.addf("%s index[%d]: negative index %d", ctx, i, idx)
		}
	}
}

func isIntLLVM(s string) bool {
	if len(s) < 2 || s[0] != 'i' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isFloatLLVM(s string) bool {
	switch s {
	case "half", "float", "double", "fp128":
		return true
	default:
		return false
	}
}

func isAggregateLLVM(s string) bool {
	return strings.HasPrefix(s, "%") || strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}
