// mir_generator.go — Stage 3 MIR-direct LLVM text emitter.
//
// This file provides an alternative to the legacy HIR→AST bridge in
// ir_module.go. Where the legacy path re-materialises the HIR back into
// a source-level AST and lowers from there, the MIR path walks a
// `*mir.Module` directly — MIR already has explicit locals, basic
// blocks, projections, and terminators, so the emitter is substantially
// simpler than the AST-based one.
//
// Stage 3 is opt-in: callers set `Options.UseMIR = true` (and supply a
// MIR module via the backend Entry). The legacy path remains the
// default until parity lands on the full LLVM test corpus; see
// docs/mir_design.md for the migration plan.
//
// MVP scope for this patch:
//   - Primitive types: Int (i64), Int32/Int16/Int8/Byte/UInt*, Bool
//     (i1), Float/Float64 (double), Float32 (float), Unit (void),
//     String (ptr).
//   - Functions with primitive parameters and return types.
//   - Instructions: AssignInstr with Use/Unary/Binary/Const RValues;
//     CallInstr with a direct FnRef callee; IntrinsicInstr for the
//     print family (Int / Bool / Float / String one-argument cases).
//   - Terminators: Goto, Branch, SwitchInt, Return, Unreachable.
//   - Storage: alloca-per-local. LLVM's mem2reg pass will promote to
//     SSA during optimisation, so the emitter stays alloca-simple.
//   - No structs, enums, tuples, lists, maps, optional unwrap, closure
//     aggregates, or concurrency intrinsics. Anything outside the MVP
//     returns an UnsupportedError so the backend dispatcher can fall
//     back to the legacy path.

package llvmgen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

// GenerateFromMIR emits textual LLVM IR from a MIR module. It is the
// Stage 3 entry point into llvmgen. Callers that still want the legacy
// HIR→AST path should call GenerateModule with Options.UseMIR == false.
func GenerateFromMIR(m *mir.Module, opts Options) ([]byte, error) {
	if m == nil {
		return nil, unsupported("source-layout", "nil MIR module")
	}
	g := newMIRGen(m, opts)
	if err := g.checkSupported(); err != nil {
		return nil, err
	}
	g.emitHeader()
	g.discoverFunctions()
	for _, fn := range m.Functions {
		if fn == nil {
			continue
		}
		if err := g.emitFunction(fn); err != nil {
			return nil, err
		}
	}
	g.emitStringPool()
	g.emitRuntimeDeclarations()
	return []byte(g.out.String()), nil
}

// ==== generator state ====

type mirGen struct {
	mod    *mir.Module
	opts   Options
	source string

	out strings.Builder

	// Per-function state (cleared between functions).
	fn          *mir.Function
	tempSeq     int
	blockLabels map[mir.BlockID]string
	localSlots  map[mir.LocalID]string
	fnBuf       strings.Builder

	// Module-level state.
	functionTypes map[string]mirFnSig // symbol → declared signature
	declares      map[string]string   // runtime name → full declaration line
	declareOrder  []string            // insertion order, for stable output
	strings       map[string]string   // literal value → global symbol
	stringOrder   []string            // order-of-insertion
}

// mirFnSig caches a function's LLVM signature so call sites can render
// the expected parameter types without re-walking the MIR.
type mirFnSig struct {
	retLLVM    string
	paramLLVM  []string
	returnType mir.Type
}

func newMIRGen(m *mir.Module, opts Options) *mirGen {
	return &mirGen{
		mod:           m,
		opts:          opts,
		source:        filepath.ToSlash(firstNonEmpty(opts.SourcePath, "<unknown>")),
		functionTypes: map[string]mirFnSig{},
		declares:      map[string]string{},
		strings:       map[string]string{},
	}
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// ==== support check ====

// checkSupported scans the module and returns an UnsupportedError for
// any construct the MVP emitter does not handle. This acts as a gate
// so the backend dispatcher can fall back to the legacy path cleanly.
func (g *mirGen) checkSupported() error {
	for _, fn := range g.mod.Functions {
		if fn == nil {
			continue
		}
		if fn.IsIntrinsic {
			return unsupported("mir-mvp", "intrinsic function declaration "+fn.Name)
		}
		if !fn.IsExternal && len(fn.Blocks) == 0 {
			return unsupported("mir-mvp", "function "+fn.Name+" has no blocks")
		}
		for _, loc := range fn.Locals {
			if loc == nil {
				continue
			}
			if !g.typeSupported(loc.Type) {
				return unsupported("mir-mvp", fmt.Sprintf("unsupported local type %s in %s", mirTypeString(loc.Type), fn.Name))
			}
		}
		if fn.ReturnType != nil && !g.typeSupported(fn.ReturnType) {
			return unsupported("mir-mvp", fmt.Sprintf("unsupported return type %s in %s", mirTypeString(fn.ReturnType), fn.Name))
		}
		for _, bb := range fn.Blocks {
			if bb == nil {
				continue
			}
			for _, inst := range bb.Instrs {
				if err := g.checkInstrSupported(fn, inst); err != nil {
					return err
				}
			}
			if err := g.checkTermSupported(fn, bb.Term); err != nil {
				return err
			}
		}
	}
	if len(g.mod.Globals) > 0 {
		return unsupported("mir-mvp", "top-level globals not yet emitted by MIR backend")
	}
	return nil
}

func (g *mirGen) typeSupported(t mir.Type) bool {
	switch x := t.(type) {
	case nil:
		return false
	case *ir.PrimType:
		switch x.Kind {
		case ir.PrimInt, ir.PrimInt8, ir.PrimInt16, ir.PrimInt32, ir.PrimInt64,
			ir.PrimUInt8, ir.PrimUInt16, ir.PrimUInt32, ir.PrimUInt64,
			ir.PrimByte, ir.PrimBool, ir.PrimChar,
			ir.PrimFloat, ir.PrimFloat32, ir.PrimFloat64,
			ir.PrimString, ir.PrimUnit, ir.PrimNever:
			return true
		}
	}
	return false
}

func (g *mirGen) checkInstrSupported(fn *mir.Function, inst mir.Instr) error {
	switch x := inst.(type) {
	case *mir.AssignInstr:
		if x.Dest.HasProjections() {
			return unsupported("mir-mvp", "projected assignment not yet supported in "+fn.Name)
		}
		return g.checkRValueSupported(fn, x.Src)
	case *mir.CallInstr:
		if x.Dest != nil && x.Dest.HasProjections() {
			return unsupported("mir-mvp", "projected call dest not yet supported in "+fn.Name)
		}
		if _, ok := x.Callee.(*mir.FnRef); !ok {
			return unsupported("mir-mvp", "indirect call not yet supported in "+fn.Name)
		}
	case *mir.IntrinsicInstr:
		if !isSupportedIntrinsic(x.Kind) {
			return unsupported("mir-mvp", fmt.Sprintf("intrinsic %s not yet supported in %s", mirIntrinsicLabel(x.Kind), fn.Name))
		}
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return nil
	default:
		return unsupported("mir-mvp", fmt.Sprintf("instruction %T not yet supported in %s", inst, fn.Name))
	}
	return nil
}

func (g *mirGen) checkRValueSupported(fn *mir.Function, rv mir.RValue) error {
	switch rv.(type) {
	case *mir.UseRV, *mir.UnaryRV, *mir.BinaryRV:
		return nil
	}
	return unsupported("mir-mvp", fmt.Sprintf("rvalue %T not yet supported in %s", rv, fn.Name))
}

func (g *mirGen) checkTermSupported(fn *mir.Function, t mir.Terminator) error {
	switch t.(type) {
	case *mir.GotoTerm, *mir.BranchTerm, *mir.ReturnTerm, *mir.UnreachableTerm, *mir.SwitchIntTerm:
		return nil
	case nil:
		return unsupported("mir-mvp", "block without terminator in "+fn.Name)
	}
	return unsupported("mir-mvp", fmt.Sprintf("terminator %T not yet supported in %s", t, fn.Name))
}

func isSupportedIntrinsic(k mir.IntrinsicKind) bool {
	switch k {
	case mir.IntrinsicPrintln, mir.IntrinsicPrint:
		return true
	}
	return false
}

func mirIntrinsicLabel(k mir.IntrinsicKind) string {
	// Use the MIR printer's label if reachable; fall back to numeric.
	switch k {
	case mir.IntrinsicPrint:
		return "print"
	case mir.IntrinsicPrintln:
		return "println"
	case mir.IntrinsicEprint:
		return "eprint"
	case mir.IntrinsicEprintln:
		return "eprintln"
	}
	return fmt.Sprintf("kind=%d", int(k))
}

// ==== header + runtime declares ====

func (g *mirGen) emitHeader() {
	g.out.WriteString("; Code generated by osty LLVM MIR backend. DO NOT EDIT.\n")
	g.out.WriteString("; Osty: ")
	g.out.WriteString(g.source)
	g.out.WriteByte('\n')
	g.out.WriteString("source_filename = \"")
	g.out.WriteString(g.source)
	g.out.WriteString("\"\n")
	if g.opts.Target != "" {
		g.out.WriteString("target triple = \"")
		g.out.WriteString(g.opts.Target)
		g.out.WriteString("\"\n")
	}
	g.out.WriteByte('\n')
}

// discoverFunctions populates functionTypes so call sites can resolve
// the LLVM signature of target symbols (including external stubs).
func (g *mirGen) discoverFunctions() {
	for _, fn := range g.mod.Functions {
		if fn == nil {
			continue
		}
		sig := mirFnSig{retLLVM: g.llvmType(fn.ReturnType), returnType: fn.ReturnType}
		for _, pid := range fn.Params {
			loc := fn.Local(pid)
			if loc == nil {
				continue
			}
			sig.paramLLVM = append(sig.paramLLVM, g.llvmType(loc.Type))
		}
		g.functionTypes[fn.Name] = sig
	}
}

// emitRuntimeDeclarations writes `declare` lines for every runtime
// symbol the generated code referenced. Writing them at the end keeps
// the emit stream simple (symbols get discovered while emitting
// function bodies, and the final buffer concatenates the function
// bodies before this tail).
func (g *mirGen) emitRuntimeDeclarations() {
	// Note: emitFunction writes into fnBuf, which is flushed to out.
	// emitRuntimeDeclarations is called after all functions have been
	// emitted, so declares already contains everything we need.
	if len(g.declareOrder) == 0 {
		return
	}
	var header strings.Builder
	for _, name := range g.declareOrder {
		header.WriteString(g.declares[name])
		header.WriteByte('\n')
	}
	// Inject the declares before the first "define " line in out.
	body := g.out.String()
	idx := strings.Index(body, "define ")
	if idx < 0 {
		g.out.WriteString(header.String())
		return
	}
	var rewritten strings.Builder
	rewritten.WriteString(body[:idx])
	rewritten.WriteString(header.String())
	rewritten.WriteByte('\n')
	rewritten.WriteString(body[idx:])
	g.out.Reset()
	g.out.WriteString(rewritten.String())
}

func (g *mirGen) declareRuntime(name, signature string) {
	if _, ok := g.declares[name]; ok {
		return
	}
	g.declares[name] = signature
	g.declareOrder = append(g.declareOrder, name)
}

// ==== function emission ====

func (g *mirGen) emitFunction(fn *mir.Function) error {
	g.fn = fn
	g.tempSeq = 0
	g.blockLabels = map[mir.BlockID]string{}
	g.localSlots = map[mir.LocalID]string{}
	g.fnBuf.Reset()

	if fn.IsExternal {
		// External stub: just a declare.
		sig := g.functionTypes[fn.Name]
		g.out.WriteString("declare ")
		g.out.WriteString(sig.retLLVM)
		g.out.WriteString(" @")
		g.out.WriteString(fn.Name)
		g.out.WriteByte('(')
		g.out.WriteString(strings.Join(sig.paramLLVM, ", "))
		g.out.WriteString(")\n\n")
		return nil
	}

	// Allocate names for each block; entry is always `entry`.
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		label := fmt.Sprintf("bb%d", bb.ID)
		if bb.ID == fn.Entry {
			label = "entry"
		}
		g.blockLabels[bb.ID] = label
	}

	// Signature line.
	sig := g.functionTypes[fn.Name]
	g.fnBuf.WriteString("define ")
	g.fnBuf.WriteString(sig.retLLVM)
	g.fnBuf.WriteString(" @")
	g.fnBuf.WriteString(fn.Name)
	g.fnBuf.WriteByte('(')
	for i, pid := range fn.Params {
		if i > 0 {
			g.fnBuf.WriteString(", ")
		}
		loc := fn.Local(pid)
		g.fnBuf.WriteString(g.llvmType(loc.Type))
		g.fnBuf.WriteString(" %arg")
		g.fnBuf.WriteString(strconv.Itoa(i))
	}
	g.fnBuf.WriteString(") {\n")

	// Entry-block preamble: alloca one slot per non-parameter local,
	// and store incoming params into their alloca slots.
	g.emitAllocaPreamble(fn)

	// Emit each block in ID order. The entry block was already opened
	// (its label is implicit in LLVM, but we still emit it for clarity
	// — and because we've appended to the preamble, not the entry
	// block's content per se).
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if bb.ID != fn.Entry {
			g.fnBuf.WriteString(g.blockLabels[bb.ID])
			g.fnBuf.WriteString(":\n")
		}
		for _, inst := range bb.Instrs {
			if err := g.emitInstr(inst); err != nil {
				return err
			}
		}
		if err := g.emitTerm(bb.Term); err != nil {
			return err
		}
	}

	g.fnBuf.WriteString("}\n\n")
	g.out.WriteString(g.fnBuf.String())
	return nil
}

func (g *mirGen) emitAllocaPreamble(fn *mir.Function) {
	// Allocate slots. The return slot is _0.
	for _, loc := range fn.Locals {
		if loc == nil {
			continue
		}
		if loc.IsParam {
			// Param slots still need an alloca so users can mutate
			// them. The alloca is named after the param index so it
			// doesn't clash with the %argN register holding the
			// incoming value.
			slot := fmt.Sprintf("%%p%d", loc.ID)
			g.localSlots[loc.ID] = slot
			if !isUnitType(loc.Type) {
				g.fnBuf.WriteString("  ")
				g.fnBuf.WriteString(slot)
				g.fnBuf.WriteString(" = alloca ")
				g.fnBuf.WriteString(g.llvmType(loc.Type))
				g.fnBuf.WriteByte('\n')
			}
			continue
		}
		slot := fmt.Sprintf("%%l%d", loc.ID)
		g.localSlots[loc.ID] = slot
		if isUnitType(loc.Type) {
			// Unit has no storage.
			continue
		}
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(slot)
		g.fnBuf.WriteString(" = alloca ")
		g.fnBuf.WriteString(g.llvmType(loc.Type))
		g.fnBuf.WriteByte('\n')
	}
	// Store incoming args into their param alloca slots.
	for i, pid := range fn.Params {
		loc := fn.Local(pid)
		if loc == nil || isUnitType(loc.Type) {
			continue
		}
		slot := g.localSlots[pid]
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(g.llvmType(loc.Type))
		g.fnBuf.WriteString(" %arg")
		g.fnBuf.WriteString(strconv.Itoa(i))
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(slot)
		g.fnBuf.WriteByte('\n')
	}
}

// ==== instructions ====

func (g *mirGen) emitInstr(inst mir.Instr) error {
	switch x := inst.(type) {
	case *mir.AssignInstr:
		return g.emitAssign(x)
	case *mir.CallInstr:
		return g.emitCall(x)
	case *mir.IntrinsicInstr:
		return g.emitIntrinsic(x)
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return nil
	}
	return unsupported("mir-mvp", fmt.Sprintf("instruction %T", inst))
}

func (g *mirGen) emitAssign(a *mir.AssignInstr) error {
	destLoc := g.fn.Local(a.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: assign into unknown local %d", a.Dest.Local)
	}
	destT := destLoc.Type
	if isUnitType(destT) {
		// Discard: still evaluate the rvalue for side effects.
		_, err := g.evalRValue(a.Src, destT)
		return err
	}
	val, err := g.evalRValue(a.Src, destT)
	if err != nil {
		return err
	}
	g.fnBuf.WriteString("  store ")
	g.fnBuf.WriteString(g.llvmType(destT))
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(val)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(g.localSlots[a.Dest.Local])
	g.fnBuf.WriteByte('\n')
	return nil
}

func (g *mirGen) emitCall(c *mir.CallInstr) error {
	fnRef, ok := c.Callee.(*mir.FnRef)
	if !ok {
		return unsupported("mir-mvp", "indirect call")
	}
	sig, known := g.functionTypes[fnRef.Symbol]
	if !known {
		return unsupported("mir-mvp", "call to unresolved symbol "+fnRef.Symbol)
	}
	// Lower args.
	argStrs := make([]string, 0, len(c.Args))
	for i, op := range c.Args {
		var paramT mir.Type
		if i < len(sig.paramLLVM) {
			// We don't have direct access to the mir.Type of each
			// param any more — re-derive from the module.
			paramT = g.paramTypeFor(fnRef.Symbol, i)
		}
		if paramT == nil {
			paramT = op.Type()
		}
		val, err := g.evalOperand(op, paramT)
		if err != nil {
			return err
		}
		argStrs = append(argStrs, g.llvmType(paramT)+" "+val)
	}
	// Build the call.
	if c.Dest != nil {
		destLoc := g.fn.Local(c.Dest.Local)
		if destLoc == nil {
			return fmt.Errorf("mir-mvp: call dest into unknown local %d", c.Dest.Local)
		}
		if isUnitType(destLoc.Type) {
			// Call returns unit — emit as void call, no dest.
			g.fnBuf.WriteString("  call void @")
			g.fnBuf.WriteString(fnRef.Symbol)
			g.fnBuf.WriteByte('(')
			g.fnBuf.WriteString(strings.Join(argStrs, ", "))
			g.fnBuf.WriteString(")\n")
			return nil
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = call ")
		g.fnBuf.WriteString(sig.retLLVM)
		g.fnBuf.WriteString(" @")
		g.fnBuf.WriteString(fnRef.Symbol)
		g.fnBuf.WriteByte('(')
		g.fnBuf.WriteString(strings.Join(argStrs, ", "))
		g.fnBuf.WriteString(")\n")
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(g.llvmType(destLoc.Type))
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(g.localSlots[c.Dest.Local])
		g.fnBuf.WriteByte('\n')
		return nil
	}
	// Void / discarded call.
	g.fnBuf.WriteString("  call ")
	g.fnBuf.WriteString(sig.retLLVM)
	g.fnBuf.WriteString(" @")
	g.fnBuf.WriteString(fnRef.Symbol)
	g.fnBuf.WriteByte('(')
	g.fnBuf.WriteString(strings.Join(argStrs, ", "))
	g.fnBuf.WriteString(")\n")
	return nil
}

// paramTypeFor returns the i-th parameter type of the named function.
func (g *mirGen) paramTypeFor(symbol string, i int) mir.Type {
	for _, fn := range g.mod.Functions {
		if fn == nil || fn.Name != symbol {
			continue
		}
		if i < 0 || i >= len(fn.Params) {
			return nil
		}
		loc := fn.Local(fn.Params[i])
		if loc == nil {
			return nil
		}
		return loc.Type
	}
	return nil
}

// emitIntrinsic supports the MVP intrinsic set — currently the print
// family. It emits a printf call with a format string matching the
// argument's type.
func (g *mirGen) emitIntrinsic(i *mir.IntrinsicInstr) error {
	switch i.Kind {
	case mir.IntrinsicPrint, mir.IntrinsicPrintln:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "println with multiple args")
		}
		return g.emitPrintlnLike(i.Args[0], i.Kind == mir.IntrinsicPrintln)
	}
	return unsupported("mir-mvp", fmt.Sprintf("intrinsic %s", mirIntrinsicLabel(i.Kind)))
}

// emitPrintlnLike prints a single primitive value using printf. The
// runtime isn't used here — printf is declared directly.
func (g *mirGen) emitPrintlnLike(op mir.Operand, newline bool) error {
	argT := op.Type()
	var format, llvmT, signExt string
	switch t := argT.(type) {
	case *ir.PrimType:
		switch t.Kind {
		case ir.PrimInt, ir.PrimInt64:
			format, llvmT = "%lld", "i64"
		case ir.PrimInt32, ir.PrimUInt32:
			format, llvmT = "%d", "i32"
		case ir.PrimInt16, ir.PrimUInt16:
			format, llvmT = "%d", "i16"
			signExt = "sext"
		case ir.PrimInt8, ir.PrimUInt8, ir.PrimByte:
			format, llvmT = "%d", "i8"
			signExt = "sext"
		case ir.PrimBool:
			// Use %d for now — the true/false string rendering is a
			// stdlib concern not yet in MVP.
			format, llvmT = "%d", "i1"
			signExt = "zext"
		case ir.PrimFloat, ir.PrimFloat64:
			format, llvmT = "%g", "double"
		case ir.PrimFloat32:
			format, llvmT = "%g", "float"
		case ir.PrimChar:
			format, llvmT = "%d", "i32"
		case ir.PrimString:
			format, llvmT = "%s", "ptr"
		default:
			return unsupported("mir-mvp", "println: unsupported primitive kind")
		}
	default:
		return unsupported("mir-mvp", fmt.Sprintf("println of non-primitive %s", mirTypeString(argT)))
	}
	if newline {
		format += "\n"
	}
	fmtSym := g.stringLiteral(format)

	val, err := g.evalOperand(op, argT)
	if err != nil {
		return err
	}
	// printf takes i32 / double directly; for narrower integer types
	// we need to extend first.
	callArg := val
	callT := llvmT
	switch llvmT {
	case "i8", "i16":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = ")
		g.fnBuf.WriteString(signExt)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to i32\n")
		callArg = tmp
		callT = "i32"
	case "i1":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = zext i1 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to i32\n")
		callArg = tmp
		callT = "i32"
	case "float":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = fpext float ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to double\n")
		callArg = tmp
		callT = "double"
	}
	g.declareRuntime("printf", "declare i32 @printf(ptr, ...)")
	g.fnBuf.WriteString("  call i32 (ptr, ...) @printf(ptr ")
	g.fnBuf.WriteString(fmtSym)
	g.fnBuf.WriteString(", ")
	g.fnBuf.WriteString(callT)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(callArg)
	g.fnBuf.WriteString(")\n")
	return nil
}

// ==== terminators ====

func (g *mirGen) emitTerm(t mir.Terminator) error {
	switch x := t.(type) {
	case *mir.GotoTerm:
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(g.blockLabels[x.Target])
		g.fnBuf.WriteByte('\n')
	case *mir.BranchTerm:
		cond, err := g.evalOperand(x.Cond, mir.TBool)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString("  br i1 ")
		g.fnBuf.WriteString(cond)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(g.blockLabels[x.Then])
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(g.blockLabels[x.Else])
		g.fnBuf.WriteByte('\n')
	case *mir.SwitchIntTerm:
		scrutT := x.Scrutinee.Type()
		scrut, err := g.evalOperand(x.Scrutinee, scrutT)
		if err != nil {
			return err
		}
		llvmT := g.llvmType(scrutT)
		g.fnBuf.WriteString("  switch ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(scrut)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(g.blockLabels[x.Default])
		g.fnBuf.WriteString(" [\n")
		for _, c := range x.Cases {
			g.fnBuf.WriteString("    ")
			g.fnBuf.WriteString(llvmT)
			g.fnBuf.WriteByte(' ')
			g.fnBuf.WriteString(strconv.FormatInt(c.Value, 10))
			g.fnBuf.WriteString(", label %")
			g.fnBuf.WriteString(g.blockLabels[c.Target])
			g.fnBuf.WriteByte('\n')
		}
		g.fnBuf.WriteString("  ]\n")
	case *mir.ReturnTerm:
		if g.fn.ReturnType == nil || isUnitType(g.fn.ReturnType) {
			g.fnBuf.WriteString("  ret void\n")
			return nil
		}
		retSlot := g.localSlots[g.fn.ReturnLocal]
		llvmT := g.llvmType(g.fn.ReturnType)
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = load ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(retSlot)
		g.fnBuf.WriteByte('\n')
		g.fnBuf.WriteString("  ret ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteByte('\n')
	case *mir.UnreachableTerm:
		g.fnBuf.WriteString("  unreachable\n")
	default:
		return unsupported("mir-mvp", fmt.Sprintf("terminator %T", t))
	}
	return nil
}

// ==== rvalue / operand evaluation ====

// evalRValue emits LLVM code for the given RValue and returns an LLVM
// value expression of type `hintT` (the destination type). For
// primitive rvalues the result is a register name (`%N`) or an
// immediate literal.
func (g *mirGen) evalRValue(rv mir.RValue, hintT mir.Type) (string, error) {
	switch r := rv.(type) {
	case *mir.UseRV:
		return g.evalOperand(r.Op, hintT)
	case *mir.UnaryRV:
		arg, err := g.evalOperand(r.Arg, r.T)
		if err != nil {
			return "", err
		}
		return g.emitUnary(r.Op, arg, r.T)
	case *mir.BinaryRV:
		left, err := g.evalOperand(r.Left, r.Left.Type())
		if err != nil {
			return "", err
		}
		right, err := g.evalOperand(r.Right, r.Right.Type())
		if err != nil {
			return "", err
		}
		return g.emitBinary(r.Op, left, right, r.Left.Type(), r.T)
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("rvalue %T", rv))
}

// evalOperand returns an LLVM value for the operand, reading loads
// from alloca slots when the operand is a place.
func (g *mirGen) evalOperand(op mir.Operand, hintT mir.Type) (string, error) {
	switch o := op.(type) {
	case *mir.CopyOp:
		return g.emitLoad(o.Place, o.T)
	case *mir.MoveOp:
		return g.emitLoad(o.Place, o.T)
	case *mir.ConstOp:
		return g.renderConst(o.Const, o.T)
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("operand %T", op))
}

func (g *mirGen) emitLoad(place mir.Place, t mir.Type) (string, error) {
	if place.HasProjections() {
		return "", unsupported("mir-mvp", "place projection")
	}
	slot, ok := g.localSlots[place.Local]
	if !ok {
		return "", fmt.Errorf("mir-mvp: load from unknown local %d", place.Local)
	}
	if isUnitType(t) {
		return "undef", nil
	}
	tmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(" = load ")
	g.fnBuf.WriteString(g.llvmType(t))
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteByte('\n')
	return tmp, nil
}

// renderConst emits a literal as an LLVM immediate. The receiver is
// the generator so we can pool strings.
func (g *mirGen) renderConst(c mir.Const, hintT mir.Type) (string, error) {
	switch k := c.(type) {
	case *mir.IntConst:
		return strconv.FormatInt(k.Value, 10), nil
	case *mir.BoolConst:
		if k.Value {
			return "1", nil
		}
		return "0", nil
	case *mir.FloatConst:
		return formatFloat(k.Value), nil
	case *mir.CharConst:
		return strconv.Itoa(int(k.Value)), nil
	case *mir.ByteConst:
		return strconv.Itoa(int(k.Value)), nil
	case *mir.UnitConst:
		return "undef", nil
	case *mir.StringConst:
		sym := g.stringLiteral(k.Value)
		return sym, nil
	case *mir.NullConst:
		return "null", nil
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("const %T", c))
}

// ==== operators ====

func (g *mirGen) emitUnary(op mir.UnaryOp, arg string, t mir.Type) (string, error) {
	llvmT := g.llvmType(t)
	tmp := g.fresh()
	switch op {
	case mir.UnNeg:
		if isFloatType(t) {
			g.fnBuf.WriteString("  ")
			g.fnBuf.WriteString(tmp)
			g.fnBuf.WriteString(" = fneg ")
			g.fnBuf.WriteString(llvmT)
			g.fnBuf.WriteByte(' ')
			g.fnBuf.WriteString(arg)
			g.fnBuf.WriteByte('\n')
			return tmp, nil
		}
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = sub ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteString(" 0, ")
		g.fnBuf.WriteString(arg)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	case mir.UnPlus:
		// identity
		return arg, nil
	case mir.UnNot:
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = xor i1 ")
		g.fnBuf.WriteString(arg)
		g.fnBuf.WriteString(", 1\n")
		return tmp, nil
	case mir.UnBitNot:
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = xor ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(arg)
		g.fnBuf.WriteString(", -1\n")
		return tmp, nil
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("unary op %d", op))
}

func (g *mirGen) emitBinary(op mir.BinaryOp, left, right string, argT, resT mir.Type) (string, error) {
	argLLVM := g.llvmType(argT)
	resLLVM := g.llvmType(resT)
	isFloat := isFloatType(argT)
	tmp := g.fresh()

	render := func(opStr string, ty string) (string, error) {
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = ")
		g.fnBuf.WriteString(opStr)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(ty)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(left)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(right)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	}

	switch op {
	case mir.BinAdd:
		if isFloat {
			return render("fadd", argLLVM)
		}
		return render("add", argLLVM)
	case mir.BinSub:
		if isFloat {
			return render("fsub", argLLVM)
		}
		return render("sub", argLLVM)
	case mir.BinMul:
		if isFloat {
			return render("fmul", argLLVM)
		}
		return render("mul", argLLVM)
	case mir.BinDiv:
		if isFloat {
			return render("fdiv", argLLVM)
		}
		return render("sdiv", argLLVM)
	case mir.BinMod:
		if isFloat {
			return render("frem", argLLVM)
		}
		return render("srem", argLLVM)
	case mir.BinEq:
		if isFloat {
			return render("fcmp oeq", argLLVM)
		}
		return render("icmp eq", argLLVM)
	case mir.BinNeq:
		if isFloat {
			return render("fcmp one", argLLVM)
		}
		return render("icmp ne", argLLVM)
	case mir.BinLt:
		if isFloat {
			return render("fcmp olt", argLLVM)
		}
		return render("icmp slt", argLLVM)
	case mir.BinLeq:
		if isFloat {
			return render("fcmp ole", argLLVM)
		}
		return render("icmp sle", argLLVM)
	case mir.BinGt:
		if isFloat {
			return render("fcmp ogt", argLLVM)
		}
		return render("icmp sgt", argLLVM)
	case mir.BinGeq:
		if isFloat {
			return render("fcmp oge", argLLVM)
		}
		return render("icmp sge", argLLVM)
	case mir.BinAnd:
		return render("and", "i1")
	case mir.BinOr:
		return render("or", "i1")
	case mir.BinBitAnd:
		return render("and", argLLVM)
	case mir.BinBitOr:
		return render("or", argLLVM)
	case mir.BinBitXor:
		return render("xor", argLLVM)
	case mir.BinShl:
		return render("shl", argLLVM)
	case mir.BinShr:
		return render("ashr", argLLVM)
	}
	_ = resLLVM
	return "", unsupported("mir-mvp", fmt.Sprintf("binary op %d", op))
}

// ==== strings ====

// stringLiteral interns a string constant and returns a `@.str.N`
// global symbol suitable as a `ptr` operand. The pool is emitted at
// the end of the module.
func (g *mirGen) stringLiteral(s string) string {
	if sym, ok := g.strings[s]; ok {
		return sym
	}
	sym := fmt.Sprintf("@.str.%d", len(g.strings))
	g.strings[s] = sym
	g.stringOrder = append(g.stringOrder, s)
	return sym
}

func (g *mirGen) emitStringPool() {
	if len(g.strings) == 0 {
		return
	}
	// Deterministic order: insertion-order (stringOrder).
	// We write the pool before the first function (injected after
	// any runtime declares). For simplicity, append to the out buffer
	// now — final layout has declares + string pool + functions.
	var pool strings.Builder
	for _, s := range g.stringOrder {
		sym := g.strings[s]
		encoded, size := encodeLLVMString(s)
		pool.WriteString(sym)
		pool.WriteString(" = private unnamed_addr constant [")
		pool.WriteString(strconv.Itoa(size))
		pool.WriteString(" x i8] c\"")
		pool.WriteString(encoded)
		pool.WriteString("\"\n")
	}
	pool.WriteByte('\n')
	// Inject before the first "define " or "declare " line, whichever
	// comes first after the header.
	body := g.out.String()
	idx := earliestAfter(body, []string{"define ", "declare "})
	if idx < 0 {
		g.out.WriteString(pool.String())
		return
	}
	var rewritten strings.Builder
	rewritten.WriteString(body[:idx])
	rewritten.WriteString(pool.String())
	rewritten.WriteString(body[idx:])
	g.out.Reset()
	g.out.WriteString(rewritten.String())
}

func earliestAfter(s string, markers []string) int {
	best := -1
	for _, m := range markers {
		if idx := strings.Index(s, m); idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	return best
}

// encodeLLVMString returns the LLVM-style escaped body of s and the
// byte count of the underlying array (including a trailing NUL).
func encodeLLVMString(s string) (string, int) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\22")
		default:
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('\\')
				hex := "0123456789ABCDEF"
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
			}
		}
	}
	// Trailing NUL byte so C printf treats it as a C string.
	b.WriteString("\\00")
	return b.String(), len(s) + 1
}

// ==== type mapping ====

func (g *mirGen) llvmType(t mir.Type) string {
	switch x := t.(type) {
	case *ir.PrimType:
		switch x.Kind {
		case ir.PrimInt, ir.PrimInt64, ir.PrimUInt64:
			return "i64"
		case ir.PrimInt32, ir.PrimUInt32, ir.PrimChar:
			return "i32"
		case ir.PrimInt16, ir.PrimUInt16:
			return "i16"
		case ir.PrimInt8, ir.PrimUInt8, ir.PrimByte:
			return "i8"
		case ir.PrimBool:
			return "i1"
		case ir.PrimFloat, ir.PrimFloat64:
			return "double"
		case ir.PrimFloat32:
			return "float"
		case ir.PrimString, ir.PrimBytes:
			return "ptr"
		case ir.PrimUnit:
			return "void"
		case ir.PrimNever:
			return "void"
		}
	}
	// Fallback: ptr works for anything we end up treating as opaque
	// heap data.
	return "ptr"
}

func (g *mirGen) fresh() string {
	name := fmt.Sprintf("%%t%d", g.tempSeq)
	g.tempSeq++
	return name
}

// ==== helpers ====

func isUnitType(t mir.Type) bool {
	if p, ok := t.(*ir.PrimType); ok {
		return p.Kind == ir.PrimUnit
	}
	return false
}

func isFloatType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	if !ok {
		return false
	}
	switch p.Kind {
	case ir.PrimFloat, ir.PrimFloat32, ir.PrimFloat64:
		return true
	}
	return false
}

func mirTypeString(t mir.Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

func formatFloat(v float64) string {
	// Use enough precision so the result round-trips in LLVM's
	// text-IR float parser. LLVM accepts both decimal and hex-float
	// literals; decimal is fine for our test corpus.
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// Sentinel so goimports doesn't prune sort.
var _ = sort.Strings
