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
	g.emitTypeDefs()
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

	// Aggregate type pools. Struct defs come from the module's
	// LayoutTable; tuple defs are discovered on-demand as the emitter
	// walks the function bodies. Both are rendered once before the
	// first function definition.
	structOrder []string                // struct name in LayoutTable order (sorted by name)
	tupleDefs   map[string][]mir.Type   // mangled tuple name → element types
	tupleOrder  []string                // first-use order of tuple names
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
		tupleDefs:     map[string][]mir.Type{},
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
	case *ir.NamedType:
		// User-declared struct (layout must exist in the module's
		// LayoutTable). Enums, lists, maps, sets, optional-named
		// forms are still refused by checkSupported above because
		// they'd need discriminant / runtime handling the MVP doesn't
		// carry yet.
		if g.mod != nil && g.mod.Layouts != nil {
			if _, ok := g.mod.Layouts.Structs[x.Name]; ok {
				return true
			}
		}
	case *ir.TupleType:
		for _, e := range x.Elems {
			if !g.typeSupported(e) {
				return false
			}
		}
		return true
	}
	return false
}

func (g *mirGen) checkInstrSupported(fn *mir.Function, inst mir.Instr) error {
	switch x := inst.(type) {
	case *mir.AssignInstr:
		if err := g.checkProjectionsSupported(fn, x.Dest, "AssignInstr.Dest"); err != nil {
			return err
		}
		return g.checkRValueSupported(fn, x.Src)
	case *mir.CallInstr:
		if x.Dest != nil {
			if err := g.checkProjectionsSupported(fn, *x.Dest, "CallInstr.Dest"); err != nil {
				return err
			}
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

// checkProjectionsSupported walks a Place's projection chain and
// refuses anything outside the aggregate MVP (FieldProj / TupleProj).
// Variant / Index / Deref projections need enum dispatch or runtime
// hooks and belong to a later Stage 3 expansion.
func (g *mirGen) checkProjectionsSupported(fn *mir.Function, p mir.Place, ctx string) error {
	for _, proj := range p.Projections {
		switch proj.(type) {
		case *mir.FieldProj, *mir.TupleProj:
			// allowed
		default:
			return unsupported("mir-mvp", fmt.Sprintf("%s projection %T not yet supported in %s", ctx, proj, fn.Name))
		}
	}
	return nil
}

func (g *mirGen) checkRValueSupported(fn *mir.Function, rv mir.RValue) error {
	switch r := rv.(type) {
	case *mir.UseRV, *mir.UnaryRV, *mir.BinaryRV:
		return nil
	case *mir.AggregateRV:
		switch r.Kind {
		case mir.AggStruct, mir.AggTuple:
			return nil
		}
		return unsupported("mir-mvp", fmt.Sprintf("aggregate kind %d not yet supported in %s", r.Kind, fn.Name))
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

// emitTypeDefs writes aggregate type definitions (structs + tuples)
// into the out buffer, injected before the first `define ` /
// `declare ` line so LLVM can resolve type references during parse.
func (g *mirGen) emitTypeDefs() {
	// Structs from the module's LayoutTable, sorted for determinism.
	if g.mod != nil && g.mod.Layouts != nil {
		names := make([]string, 0, len(g.mod.Layouts.Structs))
		for name := range g.mod.Layouts.Structs {
			names = append(names, name)
		}
		sort.Strings(names)
		g.structOrder = names
	}
	if len(g.structOrder) == 0 && len(g.tupleOrder) == 0 {
		return
	}

	var block strings.Builder
	for _, name := range g.structOrder {
		sl := g.mod.Layouts.Structs[name]
		if sl == nil {
			continue
		}
		parts := make([]string, len(sl.Fields))
		for i, f := range sl.Fields {
			parts[i] = g.llvmType(f.Type)
		}
		block.WriteByte('%')
		block.WriteString(name)
		block.WriteString(" = type { ")
		block.WriteString(strings.Join(parts, ", "))
		block.WriteString(" }\n")
	}
	for _, name := range g.tupleOrder {
		elems := g.tupleDefs[name]
		parts := make([]string, len(elems))
		for i, e := range elems {
			parts[i] = g.llvmType(e)
		}
		block.WriteByte('%')
		block.WriteString(name)
		block.WriteString(" = type { ")
		block.WriteString(strings.Join(parts, ", "))
		block.WriteString(" }\n")
	}
	block.WriteByte('\n')

	body := g.out.String()
	idx := earliestAfter(body, []string{"define ", "declare "})
	if idx < 0 {
		g.out.WriteString(block.String())
		return
	}
	var rewritten strings.Builder
	rewritten.WriteString(body[:idx])
	rewritten.WriteString(block.String())
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
	localT := destLoc.Type
	if isUnitType(localT) && !a.Dest.HasProjections() {
		// Discard: still evaluate the rvalue for side effects.
		_, err := g.evalRValue(a.Src, localT)
		return err
	}
	if !a.Dest.HasProjections() {
		val, err := g.evalRValue(a.Src, localT)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(g.llvmType(localT))
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(g.localSlots[a.Dest.Local])
		g.fnBuf.WriteByte('\n')
		return nil
	}
	// Projected write: read-modify-write. Load the whole aggregate,
	// insertvalue the new element, store back. For nested
	// projections we recurse: insertvalue into the inner aggregate,
	// then insertvalue that into the outer, etc.
	projs := a.Dest.Projections
	leafT := projectionType(projs[len(projs)-1])
	if leafT == nil {
		return unsupported("mir-mvp", "projected write with missing type")
	}
	newLeaf, err := g.evalRValue(a.Src, leafT)
	if err != nil {
		return err
	}
	// Load intermediate aggregates along the projection chain.
	slot := g.localSlots[a.Dest.Local]
	slotLLVM := g.llvmType(localT)
	aggReg := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(aggReg)
	g.fnBuf.WriteString(" = load ")
	g.fnBuf.WriteString(slotLLVM)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteByte('\n')

	// Walk the chain collecting aggregate registers + indices so we
	// can rebuild from the leaf upward.
	type frame struct {
		reg  string
		llvm string
		idx  int
	}
	frames := make([]frame, 0, len(projs))
	curReg := aggReg
	curLLVM := slotLLVM
	curT := localT
	for i, proj := range projs {
		idx, ok := projectionIndex(proj)
		if !ok {
			return unsupported("mir-mvp", fmt.Sprintf("projection %T on write", proj))
		}
		nextT := projectionType(proj)
		if nextT == nil {
			return unsupported("mir-mvp", "projected write: missing projection type")
		}
		nextLLVM := g.llvmType(nextT)
		frames = append(frames, frame{reg: curReg, llvm: curLLVM, idx: idx})
		if i == len(projs)-1 {
			_ = nextT
			break
		}
		next := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(next)
		g.fnBuf.WriteString(" = extractvalue ")
		g.fnBuf.WriteString(curLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(curReg)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(strconv.Itoa(idx))
		g.fnBuf.WriteByte('\n')
		curReg = next
		curLLVM = nextLLVM
		curT = nextT
	}
	_ = curT

	// Rebuild: start from newLeaf, walk frames in reverse.
	inner := newLeaf
	innerLLVM := g.llvmType(leafT)
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		rebuilt := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(rebuilt)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(f.llvm)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(f.reg)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(innerLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(inner)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(strconv.Itoa(f.idx))
		g.fnBuf.WriteByte('\n')
		inner = rebuilt
		innerLLVM = f.llvm
	}
	// Store the rebuilt top-level aggregate back.
	g.fnBuf.WriteString("  store ")
	g.fnBuf.WriteString(slotLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(inner)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(slot)
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
// immediate literal. For aggregate rvalues the result is an LLVM
// SSA value of the aggregate type, built up via a chain of
// `insertvalue` instructions.
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
	case *mir.AggregateRV:
		return g.emitAggregate(r, hintT)
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("rvalue %T", rv))
}

// emitAggregate builds an LLVM aggregate value for a struct or tuple
// via an `insertvalue` chain. Each iteration inserts one field into
// the running aggregate. Starting from `undef` keeps the chain
// dependency-free — LLVM's SROA + mem2reg collapses the undef chain
// into direct reads during opt.
func (g *mirGen) emitAggregate(rv *mir.AggregateRV, hintT mir.Type) (string, error) {
	aggT := rv.T
	if aggT == nil {
		aggT = hintT
	}
	if aggT == nil {
		return "", unsupported("mir-mvp", "aggregate with unknown type")
	}
	elems, err := g.aggregateElementTypes(aggT, rv)
	if err != nil {
		return "", err
	}
	if len(elems) != len(rv.Fields) {
		return "", fmt.Errorf("mir-mvp: aggregate arity mismatch: %d fields, %d types", len(rv.Fields), len(elems))
	}
	aggLLVM := g.llvmType(aggT)
	acc := "undef"
	for i, op := range rv.Fields {
		val, err := g.evalOperand(op, elems[i])
		if err != nil {
			return "", err
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(aggLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(acc)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(g.llvmType(elems[i]))
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(strconv.Itoa(i))
		g.fnBuf.WriteByte('\n')
		acc = tmp
	}
	return acc, nil
}

// aggregateElementTypes returns the element types an aggregate's
// Fields should match. For structs this comes from the LayoutTable
// so field order is authoritative; for tuples it's the type itself.
func (g *mirGen) aggregateElementTypes(aggT mir.Type, rv *mir.AggregateRV) ([]mir.Type, error) {
	switch rv.Kind {
	case mir.AggStruct:
		nt, ok := aggT.(*ir.NamedType)
		if !ok {
			return nil, fmt.Errorf("mir-mvp: struct aggregate type %s", mirTypeString(aggT))
		}
		if g.mod == nil || g.mod.Layouts == nil {
			return nil, fmt.Errorf("mir-mvp: missing layout table for struct %s", nt.Name)
		}
		sl := g.mod.Layouts.Structs[nt.Name]
		if sl == nil {
			return nil, fmt.Errorf("mir-mvp: missing layout for struct %s", nt.Name)
		}
		out := make([]mir.Type, len(sl.Fields))
		for i, f := range sl.Fields {
			out[i] = f.Type
		}
		return out, nil
	case mir.AggTuple:
		tt, ok := aggT.(*ir.TupleType)
		if !ok {
			return nil, fmt.Errorf("mir-mvp: tuple aggregate type %s", mirTypeString(aggT))
		}
		return append([]mir.Type(nil), tt.Elems...), nil
	}
	return nil, fmt.Errorf("mir-mvp: unsupported aggregate kind %d", rv.Kind)
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
	slot, ok := g.localSlots[place.Local]
	if !ok {
		return "", fmt.Errorf("mir-mvp: load from unknown local %d", place.Local)
	}
	if isUnitType(t) {
		return "undef", nil
	}
	if !place.HasProjections() {
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
	// Projected read: load the whole aggregate once, then chain
	// extractvalue per projection. An alternative is to GEP into the
	// slot and load the narrowed field; both map to the same IR after
	// optimisation, and extractvalue keeps the emitter simpler
	// because the LLVM type sequence is available without walking the
	// slot's element type graph.
	baseT := g.localType(place.Local)
	baseLLVM := g.llvmType(baseT)
	aggReg := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(aggReg)
	g.fnBuf.WriteString(" = load ")
	g.fnBuf.WriteString(baseLLVM)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteByte('\n')

	curLLVM := baseLLVM
	curReg := aggReg
	for i, proj := range place.Projections {
		idx, ok := projectionIndex(proj)
		if !ok {
			return "", unsupported("mir-mvp", fmt.Sprintf("projection %T on read", proj))
		}
		elemT := projectionType(proj)
		if elemT == nil {
			// Fall back to the declared expression type for the
			// terminal projection.
			if i == len(place.Projections)-1 {
				elemT = t
			} else {
				return "", unsupported("mir-mvp", "projection missing Type")
			}
		}
		elemLLVM := g.llvmType(elemT)
		next := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(next)
		g.fnBuf.WriteString(" = extractvalue ")
		g.fnBuf.WriteString(curLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(curReg)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(strconv.Itoa(idx))
		g.fnBuf.WriteByte('\n')
		curLLVM = elemLLVM
		curReg = next
	}
	return curReg, nil
}

// projectionIndex returns the field index for a FieldProj / TupleProj.
func projectionIndex(p mir.Projection) (int, bool) {
	switch x := p.(type) {
	case *mir.FieldProj:
		return x.Index, true
	case *mir.TupleProj:
		return x.Index, true
	}
	return 0, false
}

// projectionType returns the result type of a FieldProj / TupleProj.
func projectionType(p mir.Projection) mir.Type {
	switch x := p.(type) {
	case *mir.FieldProj:
		return x.Type
	case *mir.TupleProj:
		return x.Type
	}
	return nil
}

// localType returns the MIR type of a local by id.
func (g *mirGen) localType(id mir.LocalID) mir.Type {
	if loc := g.fn.Local(id); loc != nil {
		return loc.Type
	}
	return nil
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
	case *ir.NamedType:
		return "%" + x.Name
	case *ir.TupleType:
		return "%" + g.tupleName(x)
	}
	// Fallback: ptr works for anything we end up treating as opaque
	// heap data.
	return "ptr"
}

// tupleName returns the mangled LLVM type name for a tuple and
// ensures its type definition is emitted at least once per module.
// Keeping the naming scheme in sync with the legacy emitter
// (`Tuple.<elem1>.<elem2>.…`) makes it easier to cross-check output.
func (g *mirGen) tupleName(t *ir.TupleType) string {
	var parts []string
	for _, e := range t.Elems {
		parts = append(parts, g.llvmTypeForTupleTag(e))
	}
	name := "Tuple." + strings.Join(parts, ".")
	if _, ok := g.tupleDefs[name]; !ok {
		// Defer the actual `%Tuple.* = type { ... }` line until the
		// emitter flushes its type pool so the order is stable.
		g.tupleDefs[name] = append([]mir.Type(nil), t.Elems...)
		g.tupleOrder = append(g.tupleOrder, name)
	}
	return name
}

// llvmTypeForTupleTag renders a type in the compact mangled form used
// inside a tuple type name (dots instead of LLVM keywords). Matches
// the legacy naming (`i64.string.f64.Tuple.i64.i64`).
func (g *mirGen) llvmTypeForTupleTag(t mir.Type) string {
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
			return "f64"
		case ir.PrimFloat32:
			return "f32"
		case ir.PrimString:
			return "string"
		case ir.PrimBytes:
			return "bytes"
		case ir.PrimUnit:
			return "unit"
		}
	case *ir.NamedType:
		return x.Name
	case *ir.TupleType:
		return g.tupleName(x)
	}
	return "opaque"
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
