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
	// Canonicalize the requested target once up front so the header
	// line and every downstream consumer see the same LLVM-format
	// triple. See internal/llvmgen/target.go for the mapping rules.
	opts.Target = CanonicalLLVMTarget(opts.Target)
	g := newMIRGen(m, opts)
	if err := g.checkSupported(); err != nil {
		return nil, err
	}
	g.emitHeader()
	g.discoverFunctions()
	// Emit the init functions FIRST (before user functions) so
	// main-prologue call-site lookup finds their symbol types. They
	// are full LLVM fn definitions just like user functions.
	for _, glob := range m.Globals {
		if glob == nil || glob.Init == nil {
			continue
		}
		if err := g.emitFunction(glob.Init); err != nil {
			return nil, err
		}
	}
	for _, fn := range m.Functions {
		if fn == nil {
			continue
		}
		if err := g.emitFunction(fn); err != nil {
			return nil, err
		}
	}
	g.emitTypeDefs()
	g.emitThunks()
	g.emitGlobalVars()
	g.emitStringPool()
	g.emitRuntimeDeclarations()
	g.emitLoopMetadata()
	return withDataLayout([]byte(g.out.String()), opts.Target), nil
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

	// GC instrumentation state (per-function, populated only when
	// opts.EmitGC is set). gcRoots lists the managed-ptr locals whose
	// slot addresses are passed to each safepoint poll. This keeps the
	// live-set precise without function-long root_bind pinning, so Phase D
	// compaction can still relocate movable objects between polls.
	// gcRootChunks caches the chunked slot-address arrays prepared at
	// function entry; subsequent safepoints reuse them instead of
	// re-emitting alloca/GEP/store scaffolding at each poll site.
	// curBlockID tracks the block we're currently emitting so GotoTerm
	// can detect loop back-edges (goto whose target block ID is <= the
	// current block's ID) and emit a safepoint.
	gcRoots           []mir.LocalID
	gcRootChunks      []mirGCRootChunk
	curBlockID        mir.BlockID
	loopSafepointSlot string

	// Module-scoped safepoint counter — matches the AST emitter's
	// safepoint-v1 ABI so each call site has a stable numeric id.
	nextSafepoint int

	// Module-level state.
	functionTypes map[string]mirFnSig // symbol → declared signature
	declares      map[string]string   // runtime name → full declaration line
	declareOrder  []string            // insertion order, for stable output
	strings       map[string]string   // literal value → global symbol
	stringOrder   []string            // order-of-insertion

	// Aggregate type pools. Struct defs come from the module's
	// LayoutTable; tuple / option / result defs are discovered
	// on-demand as the emitter walks the function bodies. All are
	// rendered once before the first function definition.
	structOrder     []string              // struct name in LayoutTable order (sorted)
	enumLayoutOrder []string              // enum-shaped type names in first-use order
	enumLayouts     map[string]mir.Type   // mangled enum/option name → payload inner type
	tupleDefs       map[string][]mir.Type // mangled tuple name → element types
	tupleOrder      []string              // first-use order of tuple names

	// Closure-env thunks generated on demand when a bare `FnConst`
	// (top-level fn used as a value) reaches an indirect-call site.
	// Under the uniform env ABI every closure call takes an env as
	// implicit first arg; top-level fns have no env, so the thunk
	// ignores env and delegates to the real fn.
	thunkDefs  map[string]string // symbol → full thunk IR
	thunkOrder []string

	// v0.6 A5/A5.1/A6/A7: per-function loop-optimization hints,
	// captured at `emitFunction` entry and cleared between functions.
	// The set mirrors `mir.Function` so later emission sites (loop
	// backedge terminators, load/store tagging) don't have to re-walk
	// the function struct.
	vectorizeHint      bool
	vectorizeWidth     int
	vectorizeScalable  bool
	vectorizePredicate bool
	parallelHint       bool
	unrollHint         bool
	unrollCount        int
	// parallelAccessGroupRef is the `!N` reference of the per-function
	// `!llvm.access.group` metadata node. Allocated lazily on first
	// load/store emission inside a `#[parallel]` function; attached
	// to that load/store as `!llvm.access !N` and referenced from the
	// loop-level `!"llvm.loop.parallel_accesses", !N` property.
	parallelAccessGroupRef string
	// vectorListData / vectorListLens cache scalar list-param snapshots
	// for the conservative raw-buffer fast path used by vectorized,
	// call-free loops. Keys are root local IDs of eligible `List<Int>` /
	// `List<Bool>` / `List<Float>` params.
	vectorListData map[mir.LocalID]string
	vectorListLens map[mir.LocalID]string
	// loopMDDefs accumulates every metadata-node definition (loop
	// hint chains + access groups) for the whole translation unit.
	// Module-scoped numbering keeps IDs unique; flushed at module
	// tail via emitLoopMetadata.
	loopMDDefs []string
}

type mirGCRootChunk struct {
	slotsPtr string
	count    int
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
		thunkDefs:     map[string]string{},
		declares:      map[string]string{},
		strings:       map[string]string{},
		tupleDefs:     map[string][]mir.Type{},
		enumLayouts:   map[string]mir.Type{},
	}
}

// formatFnAttrs renders the v0.6 A8/A9/A10 function-level LLVM
// attributes for a MIR function into a single space-joined string
// suitable for splicing between the parameter list and the body
// brace of a `define`/`declare` line. Returns "" when no attribute
// applies.
//
// The set handled here is the exact mirror of the IR FnDecl fields
// ir.Lower populates from annotations — InlineMode, Hot, Cold,
// TargetFeatures. Shared across MIR and HIR emitters via the legacy
// bridge's reified annotation list on the HIR side.
func formatFnAttrs(fn *mir.Function) string {
	parts := make([]string, 0, 5)
	switch fn.InlineMode {
	case 1: // InlineSoft
		parts = append(parts, "inlinehint")
	case 2: // InlineAlways
		parts = append(parts, "alwaysinline")
	case 3: // InlineNever
		parts = append(parts, "noinline")
	}
	if fn.Hot {
		parts = append(parts, "hot")
	}
	if fn.Cold {
		parts = append(parts, "cold")
	}
	if fn.Pure {
		// v0.6 A13 `#[pure]` → LLVM `readnone`: function reads no
		// memory and has no side effects. Enables caller-side CSE.
		parts = append(parts, "readnone")
	}
	if len(fn.TargetFeatures) > 0 {
		prefixed := make([]string, len(fn.TargetFeatures))
		for i, f := range fn.TargetFeatures {
			// LLVM's target-features string expects each feature
			// prefixed with `+` for enable or `-` for disable. The
			// v0.6 annotation set only supports enable, so we
			// always prepend `+`. Features the user typed with a
			// leading `+` are tolerated by stripping it first.
			prefixed[i] = "+" + strings.TrimPrefix(f, "+")
		}
		parts = append(parts,
			fmt.Sprintf(`"target-features"="%s"`, strings.Join(prefixed, ",")))
	}
	return strings.Join(parts, " ")
}

// noaliasNameSet builds a lookup set of parameter names that should
// receive the LLVM `noalias` attribute. Bare `#[noalias]` is encoded
// as NoaliasAll, producing a nil set that the per-param check treats
// as "yes for every pointer param". The explicit list form produces
// a populated set and the all-flag is false.
func noaliasNameSet(fn *mir.Function) map[string]struct{} {
	if fn.NoaliasAll || len(fn.NoaliasParams) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(fn.NoaliasParams))
	for _, n := range fn.NoaliasParams {
		set[n] = struct{}{}
	}
	return set
}

// paramIsNoalias decides whether a single parameter should receive
// the `noalias` attribute on the LLVM signature line. LLVM rejects
// the attribute on non-pointer types, so we gate on the LLVM-level
// type being `ptr`. The lookup uses the parameter's source-level
// name (from mir.Local.Name), not the emitted `%argN` symbol.
func paramIsNoalias(fn *mir.Function, loc *mir.Local, llvmT string, names map[string]struct{}) bool {
	if llvmT != "ptr" {
		return false
	}
	if fn.NoaliasAll {
		return true
	}
	if names == nil {
		return false
	}
	if loc == nil {
		return false
	}
	_, ok := names[loc.Name]
	return ok
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// nextLoopMD allocates a fresh distinct self-referential `!llvm.loop`
// metadata node whose property list reflects the current function's
// combined loop-optimization hints (`#[vectorize(...)]`, `#[parallel]`,
// `#[unroll(...)]`). Returns the node reference (`!N`) suitable for
// appending to a back-edge terminator as `!llvm.loop !N`.
//
// The property node is always self-referential on its first slot per
// LLVM's loop metadata convention. Remaining slots are the property
// children — each is also an interned `!M` node carrying one
// `llvm.loop.*` key/value pair. Children that correspond to unset
// hints are simply omitted.
//
// Module-scoped numbering keeps all IDs unique across every loop and
// every metadata attachment in the translation unit; the HIR emitter
// in `generator.go` uses the identical layout so the two paths remain
// interchangeable from the LLVM verifier's perspective.
func (g *mirGen) nextLoopMD() string {
	var propRefs []string

	alloc := func(line string) string {
		ref := fmt.Sprintf("!%d", len(g.loopMDDefs))
		g.loopMDDefs = append(g.loopMDDefs, fmt.Sprintf("%s = %s", ref, line))
		return ref
	}

	if g.vectorizeHint {
		propRefs = append(propRefs, alloc(`!{!"llvm.loop.vectorize.enable", i1 true}`))
		if g.vectorizeWidth > 0 {
			propRefs = append(propRefs, alloc(
				fmt.Sprintf(`!{!"llvm.loop.vectorize.width", i32 %d}`, g.vectorizeWidth)))
		}
		if g.vectorizeScalable {
			propRefs = append(propRefs, alloc(`!{!"llvm.loop.vectorize.scalable.enable", i1 true}`))
		}
		if g.vectorizePredicate {
			propRefs = append(propRefs, alloc(`!{!"llvm.loop.vectorize.predicate.enable", i1 true}`))
		}
	}
	if g.parallelHint && g.parallelAccessGroupRef != "" {
		propRefs = append(propRefs, alloc(
			fmt.Sprintf(`!{!"llvm.loop.parallel_accesses", %s}`, g.parallelAccessGroupRef)))
	}
	if g.unrollHint {
		if g.unrollCount > 0 {
			propRefs = append(propRefs, alloc(
				fmt.Sprintf(`!{!"llvm.loop.unroll.count", i32 %d}`, g.unrollCount)))
		} else {
			propRefs = append(propRefs, alloc(`!{!"llvm.loop.unroll.enable", i1 true}`))
		}
	}
	if len(propRefs) == 0 {
		// Nothing to attach — caller should not have invoked us.
		return ""
	}

	loopRef := fmt.Sprintf("!%d", len(g.loopMDDefs))
	children := append([]string{loopRef}, propRefs...)
	g.loopMDDefs = append(g.loopMDDefs,
		fmt.Sprintf("%s = distinct !{%s}", loopRef, strings.Join(children, ", ")))
	return loopRef
}

// nextAccessGroupMD allocates a fresh empty `!llvm.access.group`
// metadata node (the LLVM convention for a parallel-accesses group is
// a distinct empty tuple) and returns its reference. Used once per
// `#[parallel]` function at emitFunction entry; the same reference
// then appears both on load/store attachments (`!llvm.access !N`) and
// inside each loop's `llvm.loop.parallel_accesses` property.
func (g *mirGen) nextAccessGroupMD() string {
	ref := fmt.Sprintf("!%d", len(g.loopMDDefs))
	g.loopMDDefs = append(g.loopMDDefs, fmt.Sprintf("%s = distinct !{}", ref))
	return ref
}

// loopHintsActive reports whether the currently emitting function
// carries any hint that should trigger `!llvm.loop` emission on its
// back-edges. Covers vectorize/parallel/unroll without re-listing the
// individual field names at every call site.
func (g *mirGen) loopHintsActive() bool {
	return g.vectorizeHint || g.parallelHint || g.unrollHint
}

// emitLoopMetadata appends every `!llvm.loop` node + vectorize-enable
// property collected during function emission to the module tail. No
// module footer is emitted when no `#[vectorize]` function ran.
func (g *mirGen) emitLoopMetadata() {
	if len(g.loopMDDefs) == 0 {
		return
	}
	g.out.WriteByte('\n')
	for _, def := range g.loopMDDefs {
		g.out.WriteString(def)
		g.out.WriteByte('\n')
	}
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
			if allowUnusedErrLocal(fn, loc) {
				continue
			}
			if !g.typeSupported(loc.Type) {
				hint := localDefiningSiteHint(fn, loc.ID)
			if hint != "" {
				return unsupported("mir-mvp", fmt.Sprintf("unsupported local type %s in %s (local %s id=%d; %s)", mirTypeString(loc.Type), fn.Name, localDisplayName(loc), loc.ID, hint))
			}
			return unsupported("mir-mvp", fmt.Sprintf("unsupported local type %s in %s (local %s id=%d)", mirTypeString(loc.Type), fn.Name, localDisplayName(loc), loc.ID))
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
	for _, glob := range g.mod.Globals {
		if glob == nil {
			continue
		}
		if glob.Name == "" {
			return unsupported("mir-mvp", "global with empty name")
		}
		if !g.typeSupported(glob.Type) {
			return unsupported("mir-mvp", fmt.Sprintf("global %q has unsupported type %s", glob.Name, mirTypeString(glob.Type)))
		}
		if glob.Init != nil && !glob.Init.IsExternal {
			// Init fns aren't in Module.Functions, so walk them
			// through the same whitelist checks as user fns.
			if err := g.checkFunctionSupported(glob.Init); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkFunctionSupported validates a single function against the
// emitter whitelist. Used for global-init fns, which are not part of
// Module.Functions yet still need the same checks.
func (g *mirGen) checkFunctionSupported(fn *mir.Function) error {
	if fn.IsIntrinsic {
		return unsupported("mir-mvp", "intrinsic init fn "+fn.Name)
	}
	for _, loc := range fn.Locals {
		if loc == nil {
			continue
		}
		if allowUnusedErrLocal(fn, loc) {
			continue
		}
		if !g.typeSupported(loc.Type) {
			hint := localDefiningSiteHint(fn, loc.ID)
			if hint != "" {
				return unsupported("mir-mvp", fmt.Sprintf("unsupported local type %s in %s (local %s id=%d; %s)", mirTypeString(loc.Type), fn.Name, localDisplayName(loc), loc.ID, hint))
			}
			return unsupported("mir-mvp", fmt.Sprintf("unsupported local type %s in %s (local %s id=%d)", mirTypeString(loc.Type), fn.Name, localDisplayName(loc), loc.ID))
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
			ir.PrimString, ir.PrimBytes, ir.PrimRawPtr,
			ir.PrimUnit, ir.PrimNever:
			return true
		}
	case *ir.NamedType:
		// User-declared struct or enum (layout must exist in the
		// module's LayoutTable). Builtin `List<T>`, `Map<K,V>`,
		// `Set<T>` are always `ptr` at the LLVM level so we accept
		// them; actual operations go through runtime intrinsics.
		if x.Builtin {
			switch x.Name {
			case "List", "Map", "Set", "ClosureEnv":
				return true
			}
		}
		// Concurrency runtime types are opaque pointers — the emitter
		// doesn't need a declared layout for them. These match what
		// the runtime ABI hands back from chan_make / spawn / etc.
		switch x.Name {
		case "Channel", "Handle", "Group", "TaskGroup", "Select", "Duration":
			return true
		}
		if g.mod != nil && g.mod.Layouts != nil {
			if _, ok := g.mod.Layouts.Structs[x.Name]; ok {
				return true
			}
			if _, ok := g.mod.Layouts.Enums[x.Name]; ok {
				return true
			}
		}
		// Option<T> / Maybe<T> — treated as a 2-word enum layout.
		switch x.Name {
		case "Option", "Maybe", "Result":
			return true
		}
	case *ir.OptionalType:
		return g.typeSupported(x.Inner)
	case *ir.TupleType:
		for _, e := range x.Elems {
			if !g.typeSupported(e) {
				return false
			}
		}
		return true
	case *ir.FnType:
		// Fn values pass as `ptr` at the LLVM boundary; accept them
		// for now (closures with captures still fall back to legacy).
		return true
	}
	return false
}

// localDisplayName returns a human-readable tag for an ErrType diag
// so "unsupported local type <error>" points at the specific binding.
// When the local is a synthetic temp (no Name), append the first
// instruction that *writes* the temp — that's usually the expression
// whose checker-inferred type leaked through as <error>.
func localDisplayName(loc *mir.Local) string {
	if loc == nil {
		return "<nil>"
	}
	if loc.Name == "" {
		return "<temp>"
	}
	return loc.Name
}

// localDefiningSiteHint walks fn's blocks to find the first
// instruction that assigns to loc.ID, returning a short textual hint
// suitable for embedding in an "unsupported local type <error>"
// diagnostic. Returns "" when no writer is found.
func localDefiningSiteHint(fn *mir.Function, id mir.LocalID) string {
	if fn == nil {
		return ""
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, inst := range bb.Instrs {
			switch x := inst.(type) {
			case *mir.AssignInstr:
				if placeReferencesLocal(x.Dest, id) {
					return fmt.Sprintf("written by assign at %d:%d (src=%s)", x.SpanV.Start.Line, x.SpanV.Start.Column, mirRValueDebugString(x.Src))
				}
			case *mir.CallInstr:
				if x.Dest != nil && placeReferencesLocal(*x.Dest, id) {
					callee := "?"
					if x.Callee != nil {
						callee = mirCalleeDebugString(x.Callee)
					}
					return fmt.Sprintf("written by call `%s` at %d:%d", callee, x.SpanV.Start.Line, x.SpanV.Start.Column)
				}
			case *mir.IntrinsicInstr:
				if x.Dest != nil && placeReferencesLocal(*x.Dest, id) {
					return fmt.Sprintf("written by intrinsic kind=%d at %d:%d", int(x.Kind), x.SpanV.Start.Line, x.SpanV.Start.Column)
				}
			}
		}
	}
	return ""
}

// mirRValueDebugString returns a short type-name tag for a MIR RValue,
// used by localDefiningSiteHint to hint what expression produced an
// ErrType local when the defining instruction was a bare Assign with
// a synthetic span.
func mirRValueDebugString(r mir.RValue) string {
	if r == nil {
		return "nil"
	}
	switch v := r.(type) {
	case *mir.BinaryRV:
		return fmt.Sprintf("BinaryRV(op=%d, T=%s)", int(v.Op), mirTypeString(v.T))
	case *mir.UnaryRV:
		return fmt.Sprintf("UnaryRV(op=%d, T=%s)", int(v.Op), mirTypeString(v.T))
	case *mir.AggregateRV:
		return fmt.Sprintf("AggregateRV(kind=%d, T=%s)", int(v.Kind), mirTypeString(v.T))
	case *mir.UseRV:
		_ = v
		return "UseRV"
	case *mir.CastRV:
		return fmt.Sprintf("CastRV(kind=%d, To=%s)", int(v.Kind), mirTypeString(v.To))
	}
	return fmt.Sprintf("%T", r)
}

// mirCalleeDebugString returns a short identifier for a MIR callee
// suitable for use in a diagnostic hint. Keeps the dependency surface
// shallow (no reflection on unknown callee variants) — unknown shapes
// collapse to their Go type name.
func mirCalleeDebugString(c mir.Callee) string {
	if c == nil {
		return "?"
	}
	switch v := c.(type) {
	case *mir.FnRef:
		return v.Symbol
	}
	return fmt.Sprintf("%T", c)
}

func allowUnusedErrLocal(fn *mir.Function, loc *mir.Local) bool {
	if fn == nil || loc == nil || loc.IsParam || loc.IsReturn {
		return false
	}
	if _, ok := loc.Type.(*ir.ErrType); !ok {
		return false
	}
	// The checker / lowerer can leave a dead temporary at ErrType even
	// when the live value path is still compilable. Keep MIR on the fast
	// path for those pure bookkeeping locals, but continue to fall back
	// when poisoned values participate in real operations or signatures.
	return !localReferenced(fn, loc.ID)
}

func localReferenced(fn *mir.Function, id mir.LocalID) bool {
	if fn == nil {
		return false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, inst := range bb.Instrs {
			if instrReferencesLocal(inst, id) {
				return true
			}
		}
		if termReferencesLocal(bb.Term, id) {
			return true
		}
	}
	return false
}

func instrReferencesLocal(inst mir.Instr, id mir.LocalID) bool {
	switch x := inst.(type) {
	case *mir.AssignInstr:
		return placeReferencesLocal(x.Dest, id) || rvalueReferencesLocal(x.Src, id)
	case *mir.CallInstr:
		if x.Dest != nil && placeReferencesLocal(*x.Dest, id) {
			return true
		}
		if calleeReferencesLocal(x.Callee, id) {
			return true
		}
		for _, arg := range x.Args {
			if operandReferencesLocal(arg, id) {
				return true
			}
		}
	case *mir.IntrinsicInstr:
		if x.Dest != nil && placeReferencesLocal(*x.Dest, id) {
			return true
		}
		for _, arg := range x.Args {
			if operandReferencesLocal(arg, id) {
				return true
			}
		}
	case *mir.StorageLiveInstr:
		return x.Local == id
	case *mir.StorageDeadInstr:
		return x.Local == id
	}
	return false
}

func rvalueReferencesLocal(rv mir.RValue, id mir.LocalID) bool {
	switch x := rv.(type) {
	case *mir.UseRV:
		return operandReferencesLocal(x.Op, id)
	case *mir.UnaryRV:
		return operandReferencesLocal(x.Arg, id)
	case *mir.BinaryRV:
		return operandReferencesLocal(x.Left, id) || operandReferencesLocal(x.Right, id)
	case *mir.AggregateRV:
		for _, field := range x.Fields {
			if operandReferencesLocal(field, id) {
				return true
			}
		}
	case *mir.DiscriminantRV:
		return placeReferencesLocal(x.Place, id)
	case *mir.LenRV:
		return placeReferencesLocal(x.Place, id)
	case *mir.CastRV:
		return operandReferencesLocal(x.Arg, id)
	case *mir.AddressOfRV:
		return placeReferencesLocal(x.Place, id)
	case *mir.RefRV:
		return placeReferencesLocal(x.Place, id)
	}
	return false
}

func calleeReferencesLocal(c mir.Callee, id mir.LocalID) bool {
	switch x := c.(type) {
	case *mir.IndirectCall:
		return operandReferencesLocal(x.Callee, id)
	}
	return false
}

func operandReferencesLocal(op mir.Operand, id mir.LocalID) bool {
	switch x := op.(type) {
	case *mir.CopyOp:
		return placeReferencesLocal(x.Place, id)
	case *mir.MoveOp:
		return placeReferencesLocal(x.Place, id)
	}
	return false
}

func termReferencesLocal(t mir.Terminator, id mir.LocalID) bool {
	switch x := t.(type) {
	case *mir.BranchTerm:
		return operandReferencesLocal(x.Cond, id)
	case *mir.SwitchIntTerm:
		return operandReferencesLocal(x.Scrutinee, id)
	}
	return false
}

func placeReferencesLocal(p mir.Place, id mir.LocalID) bool {
	if p.Local == id {
		return true
	}
	for _, proj := range p.Projections {
		if projReferencesLocal(proj, id) {
			return true
		}
	}
	return false
}

func projReferencesLocal(proj mir.Projection, id mir.LocalID) bool {
	switch x := proj.(type) {
	case *mir.IndexProj:
		return operandReferencesLocal(x.Index, id)
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
		switch x.Callee.(type) {
		case *mir.FnRef, *mir.IndirectCall:
			// both supported now — IndirectCall through a fn-pointer
			// operand.
		default:
			return unsupported("mir-mvp", fmt.Sprintf("call target %T not yet supported in %s", x.Callee, fn.Name))
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
// refuses anything outside the emitter's current coverage. Stage 3.5
// adds IndexProj for list-typed bases (e.g. `xs[i]`). DerefProj still
// belongs to later stages.
func (g *mirGen) checkProjectionsSupported(fn *mir.Function, p mir.Place, ctx string) error {
	for i, proj := range p.Projections {
		switch proj.(type) {
		case *mir.FieldProj, *mir.TupleProj, *mir.VariantProj, *mir.DerefProj:
			// allowed
		case *mir.IndexProj:
			// Accept IndexProj on List and Map bases. Walking the
			// projection chain from scratch would be overkill for the
			// common case (`local[i]` / `m[k]`), so we check the
			// immediate preceding type: if it's the first projection,
			// the base type is the local's type; else it's the
			// previous projection's type.
			var baseT mir.Type
			if i == 0 {
				baseT = localTypeIn(fn, p.Local)
			} else {
				baseT = projectionType(p.Projections[i-1])
			}
			if !isListPtrType(baseT) && !isMapPtrType(baseT) {
				return unsupported("mir-mvp", fmt.Sprintf("%s IndexProj on non-List/Map base %s in %s", ctx, mirTypeString(baseT), fn.Name))
			}
		default:
			return unsupported("mir-mvp", fmt.Sprintf("%s projection %T not yet supported in %s", ctx, proj, fn.Name))
		}
	}
	return nil
}

// isListPtrType reports whether t is a `List<T>` builtin (which
// lowers to a ptr at the LLVM boundary).
func isListPtrType(t mir.Type) bool {
	if nt, ok := t.(*ir.NamedType); ok {
		return nt.Name == "List" && nt.Builtin
	}
	return false
}

// isMapPtrType reports whether t is a `Map<K, V>` builtin (which
// lowers to a ptr at the LLVM boundary). Used by the IndexProj read
// path to dispatch to the map_get_or_abort runtime when the base is
// a map rather than a list.
func isMapPtrType(t mir.Type) bool {
	if nt, ok := t.(*ir.NamedType); ok {
		return nt.Name == "Map" && nt.Builtin
	}
	return false
}

func isSetPtrType(t mir.Type) bool {
	if nt, ok := t.(*ir.NamedType); ok {
		return nt.Name == "Set" && nt.Builtin
	}
	return false
}

func vectorListElemType(t mir.Type) mir.Type {
	if nt, ok := t.(*ir.NamedType); ok && nt.Name == "List" && nt.Builtin && len(nt.Args) > 0 {
		return nt.Args[0]
	}
	return nil
}

func mirOperandRootLocal(op mir.Operand) (mir.LocalID, bool) {
	switch o := op.(type) {
	case *mir.CopyOp:
		return o.Place.Local, true
	case *mir.MoveOp:
		return o.Place.Local, true
	default:
		return 0, false
	}
}

func mirFunctionHasBackedge(fn *mir.Function) bool {
	for _, bb := range fn.Blocks {
		if bb == nil || bb.Term == nil {
			continue
		}
		switch term := bb.Term.(type) {
		case *mir.GotoTerm:
			if term.Target <= bb.ID {
				return true
			}
		case *mir.BranchTerm:
			if term.Then <= bb.ID || term.Else <= bb.ID {
				return true
			}
		case *mir.SwitchIntTerm:
			if term.Default <= bb.ID {
				return true
			}
			for _, c := range term.Cases {
				if c.Target <= bb.ID {
					return true
				}
			}
		}
	}
	return false
}

func mirIntrinsicSafeForVectorListFastPath(k mir.IntrinsicKind) bool {
	switch k {
	case mir.IntrinsicListLen, mir.IntrinsicListIsEmpty:
		return true
	default:
		return false
	}
}

func (g *mirGen) eligibleVectorListParams(fn *mir.Function) map[mir.LocalID]string {
	eligible := map[mir.LocalID]string{}
	if !g.vectorizeHint || !mirFunctionHasBackedge(fn) {
		return eligible
	}
	for _, pid := range fn.Params {
		loc := fn.Local(pid)
		if loc == nil {
			continue
		}
		elemT := vectorListElemType(loc.Type)
		if elemT == nil {
			continue
		}
		elemLLVM := g.llvmType(elemT)
		if listUsesRawDataFastPath(elemLLVM) {
			eligible[pid] = elemLLVM
		}
	}
	if len(eligible) == 0 {
		return eligible
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, inst := range bb.Instrs {
			switch x := inst.(type) {
			case *mir.CallInstr:
				return map[mir.LocalID]string{}
			case *mir.IntrinsicInstr:
				if !mirIntrinsicSafeForVectorListFastPath(x.Kind) {
					return map[mir.LocalID]string{}
				}
			case *mir.AssignInstr:
				delete(eligible, x.Dest.Local)
			}
			if len(eligible) == 0 {
				return eligible
			}
		}
	}
	return eligible
}

func (g *mirGen) checkRValueSupported(fn *mir.Function, rv mir.RValue) error {
	switch r := rv.(type) {
	case *mir.UseRV, *mir.UnaryRV, *mir.BinaryRV,
		*mir.DiscriminantRV, *mir.NullaryRV, *mir.CastRV,
		*mir.GlobalRefRV:
		return nil
	case *mir.AggregateRV:
		switch r.Kind {
		case mir.AggStruct, mir.AggTuple, mir.AggEnumVariant, mir.AggList, mir.AggClosure:
			return nil
		}
		return unsupported("mir-mvp", fmt.Sprintf("aggregate kind %d not yet supported in %s", r.Kind, fn.Name))
	case *mir.LenRV:
		if err := g.checkProjectionsSupported(fn, r.Place, "LenRV.Place"); err != nil {
			return err
		}
		if placeTypeIn(fn, r.Place) == nil {
			return unsupported("mir-mvp", fmt.Sprintf("LenRV on place with unknown type in %s", fn.Name))
		}
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
	case mir.IntrinsicListPush, mir.IntrinsicListLen, mir.IntrinsicListGet,
		mir.IntrinsicListIsEmpty, mir.IntrinsicListSorted, mir.IntrinsicListToSet:
		return true
	case mir.IntrinsicMapNew, mir.IntrinsicMapGet, mir.IntrinsicMapSet,
		mir.IntrinsicMapContains, mir.IntrinsicMapLen, mir.IntrinsicMapKeys,
		mir.IntrinsicMapRemove:
		return true
	case mir.IntrinsicSetInsert, mir.IntrinsicSetContains, mir.IntrinsicSetLen,
		mir.IntrinsicSetToList, mir.IntrinsicSetRemove:
		return true
	case mir.IntrinsicBytesLen, mir.IntrinsicBytesIsEmpty, mir.IntrinsicBytesGet, mir.IntrinsicBytesContains, mir.IntrinsicBytesStartsWith, mir.IntrinsicBytesEndsWith, mir.IntrinsicBytesIndexOf, mir.IntrinsicBytesLastIndexOf, mir.IntrinsicBytesSplit, mir.IntrinsicBytesJoin, mir.IntrinsicBytesConcat, mir.IntrinsicBytesRepeat, mir.IntrinsicBytesReplace, mir.IntrinsicBytesReplaceAll, mir.IntrinsicBytesTrimLeft, mir.IntrinsicBytesTrimRight, mir.IntrinsicBytesTrim, mir.IntrinsicBytesTrimSpace, mir.IntrinsicBytesToUpper, mir.IntrinsicBytesToLower, mir.IntrinsicBytesToHex, mir.IntrinsicBytesSlice:
		return true
	// Stage 5 prep — string → List<Char> / List<Byte> expansions that
	// the legacy emitter routes through `osty_rt_strings_*`. Accepting
	// them here lets `.chars()` / `.bytes()` reach the MIR emitter on
	// object/binary emission instead of falling back.
	case mir.IntrinsicStringConcat, mir.IntrinsicStringChars, mir.IntrinsicStringBytes,
		mir.IntrinsicStringLen, mir.IntrinsicStringIsEmpty,
		mir.IntrinsicStringToUpper, mir.IntrinsicStringToLower,
		mir.IntrinsicStringToInt, mir.IntrinsicStringToFloat:
		return true
	// Concurrency — channels / tasks / select / cancellation / helpers.
	case mir.IntrinsicChanMake, mir.IntrinsicChanSend, mir.IntrinsicChanRecv,
		mir.IntrinsicChanClose, mir.IntrinsicChanIsClosed:
		return true
	case mir.IntrinsicTaskGroup, mir.IntrinsicSpawn, mir.IntrinsicHandleJoin,
		mir.IntrinsicGroupCancel, mir.IntrinsicGroupIsCancelled:
		return true
	case mir.IntrinsicSelect, mir.IntrinsicSelectRecv, mir.IntrinsicSelectSend,
		mir.IntrinsicSelectTimeout, mir.IntrinsicSelectDefault:
		return true
	case mir.IntrinsicIsCancelled, mir.IntrinsicCheckCancelled,
		mir.IntrinsicYield, mir.IntrinsicSleep:
		return true
	case mir.IntrinsicParallel, mir.IntrinsicRace, mir.IntrinsicCollectAll:
		return true
	// LANG_SPEC §19 runtime sublanguage.
	case mir.IntrinsicRawNull:
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
	case mir.IntrinsicStringConcat:
		return "string_concat"
	case mir.IntrinsicStringChars:
		return "string_chars"
	case mir.IntrinsicStringBytes:
		return "string_bytes"
	case mir.IntrinsicStringLen:
		return "string_len"
	case mir.IntrinsicStringIsEmpty:
		return "string_is_empty"
	case mir.IntrinsicStringToInt:
		return "string_to_int"
	case mir.IntrinsicStringToFloat:
		return "string_to_float"
	case mir.IntrinsicRawNull:
		return "raw.null"
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
		sig := mirFnSig{retLLVM: g.functionReturnLLVM(fn), returnType: fn.ReturnType}
		for _, pid := range fn.Params {
			loc := fn.Local(pid)
			if loc == nil {
				continue
			}
			sig.paramLLVM = append(sig.paramLLVM, g.llvmType(loc.Type))
		}
		g.functionTypes[fn.Name] = sig
	}
	// Register global-init fns too so call sites can reference them
	// (currently only the main-prologue wiring does, but recording
	// signatures here keeps the lookup uniform).
	for _, glob := range g.mod.Globals {
		if glob == nil || glob.Init == nil {
			continue
		}
		fn := glob.Init
		sig := mirFnSig{retLLVM: g.functionReturnLLVM(fn), returnType: fn.ReturnType}
		g.functionTypes[fn.Name] = sig
	}
}

func (g *mirGen) functionReturnLLVM(fn *mir.Function) string {
	if fn != nil && fn.Name == "main" && len(fn.Params) == 0 && isUnitType(fn.ReturnType) {
		return "i32"
	}
	return g.llvmType(fn.ReturnType)
}

// emitGlobalVars writes `@<name> = global <T> zeroinitializer` lines
// for each module-level Global, plus an `@osty_global_init` ctor
// function that runs every init fn once at program start. The ctor
// is wired through `@llvm.global_ctors` so it executes before `main`.
func (g *mirGen) emitGlobalVars() {
	if g.mod == nil || len(g.mod.Globals) == 0 {
		return
	}
	var block strings.Builder
	// @<name> = global <T> zeroinitializer — runtime fills this from
	// the ctor below. We don't try to statically inline the init
	// value (some init fns have side effects / non-const expressions);
	// a deferred ctor is the uniform shape.
	for _, glob := range g.mod.Globals {
		if glob == nil {
			continue
		}
		block.WriteByte('@')
		block.WriteString(glob.Name)
		block.WriteString(" = global ")
		block.WriteString(g.llvmType(glob.Type))
		block.WriteString(" zeroinitializer\n")
	}
	block.WriteByte('\n')
	// Ctor function: `@__osty_init_globals() { call <Ti> @_init_xxx();
	// store <Ti> %tmp, ptr @xxx; ... ret void }`.
	block.WriteString("define private void @__osty_init_globals() {\n")
	block.WriteString("entry:\n")
	for _, glob := range g.mod.Globals {
		if glob == nil || glob.Init == nil {
			continue
		}
		initName := glob.Init.Name
		retLLVM := g.llvmType(glob.Type)
		tmp := "%v" + glob.Name
		block.WriteString("  ")
		block.WriteString(tmp)
		block.WriteString(" = call ")
		block.WriteString(retLLVM)
		block.WriteString(" @")
		block.WriteString(initName)
		block.WriteString("()\n")
		block.WriteString("  store ")
		block.WriteString(retLLVM)
		block.WriteByte(' ')
		block.WriteString(tmp)
		block.WriteString(", ptr @")
		block.WriteString(glob.Name)
		block.WriteByte('\n')
	}
	block.WriteString("  ret void\n")
	block.WriteString("}\n\n")
	// LLVM ctor registration. Priority 65535 runs last among ctors
	// so anything with lower priority (runtime setup) has already
	// executed.
	block.WriteString("@llvm.global_ctors = appending global [1 x { i32, ptr, ptr }] [{ i32, ptr, ptr } { i32 65535, ptr @__osty_init_globals, ptr null }]\n\n")

	// Inject the block before the first `define ` / `declare ` line
	// so the `@<name>` globals and the ctor sit alongside type defs.
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

// emitRuntimeDeclarations writes `declare` lines for every runtime
// emitThunks appends any closure thunks generated during function
// emission. Thunks live below the normal user function definitions so
// the output reads top-down: user fns first, then closure shims.
func (g *mirGen) emitThunks() {
	if len(g.thunkOrder) == 0 {
		return
	}
	for _, sym := range g.thunkOrder {
		g.out.WriteString(g.thunkDefs[sym])
	}
}

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
		// User enums also go through the same 2-word layout as Option /
		// Maybe / Result: `{ i64 disc, i64 payload }`. We pre-register
		// them here so they're in the type-def block even if the
		// function bodies haven't referenced their variants yet.
		enumNames := make([]string, 0, len(g.mod.Layouts.Enums))
		for name := range g.mod.Layouts.Enums {
			enumNames = append(enumNames, name)
		}
		sort.Strings(enumNames)
		for _, name := range enumNames {
			g.registerEnumLayout(name, nil)
		}
	}
	if len(g.structOrder) == 0 && len(g.tupleOrder) == 0 && len(g.enumLayoutOrder) == 0 {
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
	for _, name := range g.enumLayoutOrder {
		block.WriteByte('%')
		block.WriteString(name)
		block.WriteString(" = type { i64, i64 }\n")
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
	g.gcRoots = g.gcRoots[:0]
	g.gcRootChunks = g.gcRootChunks[:0]
	// v0.6 A5.2: fn.Vectorize is already default-true unless
	// `#[no_vectorize]` was present (ir.Lower sets it). Mirror here
	// verbatim — the per-fn state machine doesn't need to re-derive
	// the default, just forward what MIR says.
	g.vectorizeHint = fn.Vectorize
	g.vectorizeWidth = fn.VectorizeWidth
	g.vectorizeScalable = fn.VectorizeScalable
	g.vectorizePredicate = fn.VectorizePredicate
	g.parallelHint = fn.Parallel
	g.unrollHint = fn.Unroll
	g.unrollCount = fn.UnrollCount
	g.parallelAccessGroupRef = ""
	g.vectorListData = map[mir.LocalID]string{}
	g.vectorListLens = map[mir.LocalID]string{}
	if g.parallelHint {
		// Allocate the per-function access group eagerly so every
		// load/store site can reference it without branching on lazy
		// init inside hot emission paths.
		g.parallelAccessGroupRef = g.nextAccessGroupMD()
	}

	emitName := fn.Name
	if fn.ExportSymbol != "" {
		emitName = fn.ExportSymbol
	}
	// `#[c_abi]` requests the platform C calling convention. LLVM
	// IR syntax: `declare [cconv] <ret> @<name>(...)`. The `ccc`
	// keyword goes before the return type. Default is also `ccc`,
	// so the explicit keyword is currently a documentation/forward-
	// compatibility marker rather than a behaviour change — but
	// emitting it makes intent visible in the generated IR.
	cconv := ""
	if fn.CABI {
		cconv = "ccc "
	}

	// v0.6 A8/A9/A10: function-level LLVM attributes go between the
	// closing paren of the param list and the `{` that opens the body.
	// `declare` lines accept the same set. Keyword attrs (`noinline`,
	// `alwaysinline`, `inlinehint`, `hot`, `cold`) are bare; string
	// attrs (`"target-features"="+f1,+f2"`) take the key=value form.
	attrs := formatFnAttrs(fn)

	if fn.IsExternal {
		// External stub: just a declare.
		sig := g.functionTypes[fn.Name]
		g.out.WriteString("declare ")
		g.out.WriteString(cconv)
		g.out.WriteString(sig.retLLVM)
		g.out.WriteString(" @")
		g.out.WriteString(emitName)
		g.out.WriteByte('(')
		g.out.WriteString(strings.Join(sig.paramLLVM, ", "))
		g.out.WriteByte(')')
		if attrs != "" {
			g.out.WriteByte(' ')
			g.out.WriteString(attrs)
		}
		g.out.WriteString("\n\n")
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
	// v0.6 A11: decide per-param whether the `noalias` attribute
	// applies. Bare `#[noalias]` stamps every pointer param;
	// `#[noalias(p1, p2)]` stamps only the named ones. Non-pointer
	// params (i64, double, etc.) ignore the attribute — LLVM rejects
	// `noalias` on non-pointer types.
	noaliasNames := noaliasNameSet(fn)
	g.fnBuf.WriteString("define ")
	g.fnBuf.WriteString(cconv)
	g.fnBuf.WriteString(sig.retLLVM)
	g.fnBuf.WriteString(" @")
	g.fnBuf.WriteString(emitName)
	g.fnBuf.WriteByte('(')
	for i, pid := range fn.Params {
		if i > 0 {
			g.fnBuf.WriteString(", ")
		}
		loc := fn.Local(pid)
		llvmT := g.llvmType(loc.Type)
		g.fnBuf.WriteString(llvmT)
		if paramIsNoalias(fn, loc, llvmT, noaliasNames) {
			g.fnBuf.WriteString(" noalias")
		}
		g.fnBuf.WriteString(" %arg")
		g.fnBuf.WriteString(strconv.Itoa(i))
	}
	g.fnBuf.WriteByte(')')
	if attrs != "" {
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(attrs)
	}
	g.fnBuf.WriteString(" {\n")

	// Entry-block preamble: alloca one slot per non-parameter local,
	// and store incoming params into their alloca slots.
	g.emitAllocaPreamble(fn)
	// Register managed-ptr locals as GC roots and take a safepoint at
	// the function entry. No-op unless opts.EmitGC is set.
	g.emitGCEntry(fn)
	g.emitVectorListPreamble(fn)

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
		g.curBlockID = bb.ID
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

	// v0.6 A6: if `#[parallel]` is in effect, annotate every load/store
	// we just emitted inside this function body with `!llvm.access.group
	// !N` so the loop-level `llvm.loop.parallel_accesses` reference has
	// something to point at. We post-process `fnBuf` rather than
	// threading the attachment through ~30 emission sites — the pattern
	// is mechanical (lines starting with `  store ` or containing ` = load `
	// in the leader) and the cost is one linear scan per parallel
	// function.
	if g.parallelHint && g.parallelAccessGroupRef != "" {
		g.out.WriteString(tagParallelAccesses(g.fnBuf.String(), g.parallelAccessGroupRef))
	} else {
		g.out.WriteString(g.fnBuf.String())
	}
	return nil
}

// tagParallelAccesses appends `, !llvm.access.group !N` to every
// load/store line in a function body that does not already carry a
// metadata attachment. Lines outside memory-access shape (branches,
// arithmetic, calls, labels, the function header / closing brace) are
// passed through verbatim. The attachment kind is LLVM's
// `llvm.access.group`, which is what `llvm.loop.parallel_accesses`
// refers to.
func tagParallelAccesses(body string, groupRef string) string {
	var b strings.Builder
	b.Grow(len(body) + len(groupRef)*8)
	suffix := ", !llvm.access.group " + groupRef
	for _, line := range strings.SplitAfter(body, "\n") {
		trimmedNL := strings.TrimRight(line, "\n")
		trailing := line[len(trimmedNL):]
		if isMemoryAccessLine(trimmedNL) && !strings.Contains(trimmedNL, "!llvm.access.group") {
			b.WriteString(trimmedNL)
			b.WriteString(suffix)
			b.WriteString(trailing)
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// isMemoryAccessLine recognises the two textual shapes the MIR emitter
// produces for loads and stores. Everything else — branches, icmp,
// select, call, br, alloca, GEP, phi, labels, arithmetic — is left
// unannotated.
func isMemoryAccessLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if strings.HasPrefix(trimmed, "store ") {
		return true
	}
	// `%tmp = load ...`: leading `%<ident> = load ` marker after any
	// indent. The `= load ` substring anywhere on a line is a safe
	// proxy since LLVM IR has no other instruction spelled that way.
	return strings.Contains(line, " = load ")
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
	if g.opts.EmitGC && loopSafepointStride > 1 {
		g.loopSafepointSlot = "%gc.loop.poll"
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(g.loopSafepointSlot)
		g.fnBuf.WriteString(" = alloca i64\n")
		g.fnBuf.WriteString("  store i64 0, ptr ")
		g.fnBuf.WriteString(g.loopSafepointSlot)
		g.fnBuf.WriteByte('\n')
	} else {
		g.loopSafepointSlot = ""
	}
}

func (g *mirGen) emitVectorListPreamble(fn *mir.Function) {
	eligible := g.eligibleVectorListParams(fn)
	if len(eligible) == 0 {
		return
	}
	ids := make([]int, 0, len(eligible))
	for pid := range eligible {
		ids = append(ids, int(pid))
	}
	sort.Ints(ids)
	for _, rawID := range ids {
		pid := mir.LocalID(rawID)
		elemLLVM := eligible[pid]
		slot := g.localSlots[pid]
		if slot == "" {
			continue
		}
		listReg := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(listReg)
		g.fnBuf.WriteString(" = load ptr, ptr ")
		g.fnBuf.WriteString(slot)
		g.fnBuf.WriteByte('\n')

		dataSym := listRuntimeDataSymbol(elemLLVM)
		g.declareRuntime(dataSym, "declare ptr @"+dataSym+"(ptr)")
		dataReg := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(dataReg)
		g.fnBuf.WriteString(" = call ptr @")
		g.fnBuf.WriteString(dataSym)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(listReg)
		g.fnBuf.WriteString(")\n")
		g.vectorListData[pid] = dataReg

		lenSym := listRuntimeLenSymbol()
		g.declareRuntime(lenSym, "declare i64 @"+lenSym+"(ptr)")
		lenReg := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(lenReg)
		g.fnBuf.WriteString(" = call i64 @")
		g.fnBuf.WriteString(lenSym)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(listReg)
		g.fnBuf.WriteString(")\n")
		g.vectorListLens[pid] = lenReg
	}
}

func (g *mirGen) emitVectorListFastLoad(local mir.LocalID, idxVal, elemLLVM string) (string, bool) {
	dataReg := g.vectorListData[local]
	lenReg := g.vectorListLens[local]
	slot := g.localSlots[local]
	if dataReg == "" || lenReg == "" || slot == "" {
		return "", false
	}
	inBounds := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(inBounds)
	g.fnBuf.WriteString(" = icmp ult i64 ")
	g.fnBuf.WriteString(idxVal)
	g.fnBuf.WriteString(", ")
	g.fnBuf.WriteString(lenReg)
	g.fnBuf.WriteByte('\n')

	fastLabel := g.freshLabel("list.fast")
	slowLabel := g.freshLabel("list.slow")
	mergeLabel := g.freshLabel("list.merge")
	g.fnBuf.WriteString("  br i1 ")
	g.fnBuf.WriteString(inBounds)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(fastLabel)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(slowLabel)
	g.fnBuf.WriteByte('\n')

	g.fnBuf.WriteString(fastLabel)
	g.fnBuf.WriteString(":\n")
	elemPtr := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(elemPtr)
	g.fnBuf.WriteString(" = getelementptr inbounds ")
	g.fnBuf.WriteString(elemLLVM)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(dataReg)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(idxVal)
	g.fnBuf.WriteByte('\n')
	fastVal := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(fastVal)
	g.fnBuf.WriteString(" = load ")
	g.fnBuf.WriteString(elemLLVM)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(elemPtr)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString("  br label %")
	g.fnBuf.WriteString(mergeLabel)
	g.fnBuf.WriteByte('\n')

	slowSym := listRuntimeGetSymbol(elemLLVM)
	g.declareRuntime(slowSym, "declare "+elemLLVM+" @"+slowSym+"(ptr, i64)")
	g.fnBuf.WriteString(slowLabel)
	g.fnBuf.WriteString(":\n")
	listReg := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(listReg)
	g.fnBuf.WriteString(" = load ptr, ptr ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteByte('\n')
	slowVal := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(slowVal)
	g.fnBuf.WriteString(" = call ")
	g.fnBuf.WriteString(elemLLVM)
	g.fnBuf.WriteString(" @")
	g.fnBuf.WriteString(slowSym)
	g.fnBuf.WriteString("(ptr ")
	g.fnBuf.WriteString(listReg)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(idxVal)
	g.fnBuf.WriteString(")\n")
	g.fnBuf.WriteString("  br label %")
	g.fnBuf.WriteString(mergeLabel)
	g.fnBuf.WriteByte('\n')

	g.fnBuf.WriteString(mergeLabel)
	g.fnBuf.WriteString(":\n")
	merged := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(merged)
	g.fnBuf.WriteString(" = phi ")
	g.fnBuf.WriteString(elemLLVM)
	g.fnBuf.WriteString(" [")
	g.fnBuf.WriteString(fastVal)
	g.fnBuf.WriteString(", %")
	g.fnBuf.WriteString(fastLabel)
	g.fnBuf.WriteString("], [")
	g.fnBuf.WriteString(slowVal)
	g.fnBuf.WriteString(", %")
	g.fnBuf.WriteString(slowLabel)
	g.fnBuf.WriteString("]\n")
	return merged, true
}

// ==== GC instrumentation ====
//
// Only emitted when opts.EmitGC is set. The ABI matches the legacy
// AST emitter (see internal/llvmgen/generator.go):
//
//   declare void @osty.gc.safepoint_v1(i64, ptr, i64)
//
// MIR now follows the legacy emitter's precise safepoint shape: each
// poll receives an explicit vector of managed local slot addresses.
// That keeps the live set precise without function-long pinning, so the
// Phase D compactor can relocate movable objects between safepoints.

// isManagedLocal reports whether a local should be tracked as a GC
// root. A local is managed when it lowers to an LLVM ptr AND the MIR
// type is not a function pointer (static code addresses never move).
// Unit-typed locals are skipped — they have no storage.
func (g *mirGen) isManagedLocal(loc *mir.Local) bool {
	if loc == nil || loc.Type == nil {
		return false
	}
	if isUnitType(loc.Type) {
		return false
	}
	if _, isFn := loc.Type.(*ir.FnType); isFn {
		return false
	}
	return g.llvmType(loc.Type) == "ptr"
}

// emitGCEntry records every managed local of the current function and
// emits a safepoint at function entry. Non-param managed slots are
// null-initialised before the first poll so the GC never scans undef
// memory. Unlike the old root_bind path, the roots are supplied
// explicitly at each safepoint as slot addresses, which avoids
// function-long pinning and lets compaction move objects between polls.
func (g *mirGen) emitGCEntry(fn *mir.Function) {
	if !g.opts.EmitGC {
		return
	}
	// Collect managed locals in ID order so the emitted safepoint root
	// arrays stay deterministic across builds.
	for _, loc := range fn.Locals {
		if !g.isManagedLocal(loc) {
			continue
		}
		g.gcRoots = append(g.gcRoots, loc.ID)
	}
	if len(g.gcRoots) == 0 {
		// Still emit an entry safepoint so cancellation / GC polls
		// fire even on pointer-free functions.
		g.emitGCSafepointKind(safepointKindEntry)
		return
	}
	// Zero non-param managed slots before the first safepoint. Param slots
	// already hold the incoming arg from the preamble's store loop.
	for _, id := range g.gcRoots {
		loc := fn.Local(id)
		if loc == nil || loc.IsParam {
			continue
		}
		g.fnBuf.WriteString("  store ptr null, ptr ")
		g.fnBuf.WriteString(g.localSlots[id])
		g.fnBuf.WriteByte('\n')
	}
	g.prepareGCSafepointRootChunks()
	// Entry safepoint.
	g.emitGCSafepointKind(safepointKindEntry)
}

// emitGCReleaseRoots is a legacy hook kept for call-site stability.
// MIR GC lowering now uses explicit safepoint root arrays, so there is
// no function-exit release work left to do.
func (g *mirGen) emitGCReleaseRoots() {
}

func (g *mirGen) visibleGCSafepointRoots() []string {
	out := make([]string, 0, len(g.gcRoots))
	for _, id := range g.gcRoots {
		if slot := g.localSlots[id]; slot != "" {
			out = append(out, slot)
		}
	}
	return out
}

func (g *mirGen) prepareGCSafepointRootChunks() {
	roots := g.visibleGCSafepointRoots()
	start := 0

	g.gcRootChunks = g.gcRootChunks[:0]
	if len(roots) == 0 {
		return
	}
	if safepointRootChunkSize <= 0 {
		safepointRootChunkSize = 1
	}
	for start < len(roots) {
		end := start + safepointRootChunkSize
		if end > len(roots) {
			end = len(roots)
		}
		window := roots[start:end]
		slotsPtr := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(slotsPtr)
		g.fnBuf.WriteString(" = alloca ptr, i64 ")
		g.fnBuf.WriteString(strconv.Itoa(len(window)))
		g.fnBuf.WriteByte('\n')
		for i, addr := range window {
			slotPtr := g.fresh()
			g.fnBuf.WriteString("  ")
			g.fnBuf.WriteString(slotPtr)
			g.fnBuf.WriteString(" = getelementptr ptr, ptr ")
			g.fnBuf.WriteString(slotsPtr)
			g.fnBuf.WriteString(", i64 ")
			g.fnBuf.WriteString(strconv.Itoa(i))
			g.fnBuf.WriteByte('\n')
			g.fnBuf.WriteString("  store ptr ")
			g.fnBuf.WriteString(addr)
			g.fnBuf.WriteString(", ptr ")
			g.fnBuf.WriteString(slotPtr)
			g.fnBuf.WriteByte('\n')
		}
		g.gcRootChunks = append(g.gcRootChunks, mirGCRootChunk{
			slotsPtr: slotsPtr,
			count:    len(window),
		})
		start = end
	}
}

// emitGCSafepointKind emits a safepoint poll tagged with the given kind.
// The kind is packed into the high byte of the id the runtime receives;
// the low 56 bits hold the per-module serial (see encodeSafepointID in
// the legacy emitter). Managed MIR locals are passed as an explicit array
// of slot addresses so the runtime can trace the live set without
// pinning it for the entire function.
func (g *mirGen) emitGCSafepointKind(kind safepointKind) {
	if !g.opts.EmitGC {
		return
	}
	g.declareSafepoint()
	if len(g.gcRootChunks) == 0 {
		serial := g.nextSafepoint
		g.nextSafepoint++
		g.fnBuf.WriteString(llvmRenderSafepointEmpty(encodeSafepointID(kind, serial)))
		g.fnBuf.WriteByte('\n')
		return
	}
	plan := llvmPlanSafepointChunks(int(kind), g.nextSafepoint, len(g.gcRoots), safepointRootChunkSize)
	g.nextSafepoint += len(plan)
	for i, chunk := range plan {
		rootChunk := g.gcRootChunks[i]
		g.fnBuf.WriteString("  call void @osty.gc.safepoint_v1(i64 ")
		g.fnBuf.WriteString(strconv.Itoa(chunk.id))
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(rootChunk.slotsPtr)
		g.fnBuf.WriteString(", i64 ")
		g.fnBuf.WriteString(strconv.Itoa(rootChunk.count))
		g.fnBuf.WriteString(")\n")
	}
}

func (g *mirGen) emitLoopSafepointKind() {
	if !g.opts.EmitGC {
		return
	}
	if g.loopSafepointSlot == "" || loopSafepointStride <= 1 {
		g.emitGCSafepointKind(safepointKindLoop)
		return
	}
	count := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(count)
	g.fnBuf.WriteString(" = load i64, ptr ")
	g.fnBuf.WriteString(g.loopSafepointSlot)
	g.fnBuf.WriteByte('\n')
	next := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(next)
	g.fnBuf.WriteString(" = add i64 ")
	g.fnBuf.WriteString(count)
	g.fnBuf.WriteString(", 1\n")
	shouldPoll := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(shouldPoll)
	g.fnBuf.WriteString(" = icmp eq i64 ")
	g.fnBuf.WriteString(next)
	g.fnBuf.WriteString(", ")
	g.fnBuf.WriteString(strconv.FormatInt(loopSafepointStride, 10))
	g.fnBuf.WriteByte('\n')
	stored := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(stored)
	g.fnBuf.WriteString(" = select i1 ")
	g.fnBuf.WriteString(shouldPoll)
	g.fnBuf.WriteString(", i64 0, i64 ")
	g.fnBuf.WriteString(next)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString("  store i64 ")
	g.fnBuf.WriteString(stored)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(g.loopSafepointSlot)
	g.fnBuf.WriteByte('\n')
	pollLabel := g.freshLabel("gc.loop.poll")
	afterLabel := g.freshLabel("gc.loop.after")
	g.fnBuf.WriteString("  br i1 ")
	g.fnBuf.WriteString(shouldPoll)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(pollLabel)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(afterLabel)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString(pollLabel)
	g.fnBuf.WriteString(":\n")
	g.emitGCSafepointKind(safepointKindLoop)
	g.fnBuf.WriteString("  br label %")
	g.fnBuf.WriteString(afterLabel)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString(afterLabel)
	g.fnBuf.WriteString(":\n")
}

// declareSafepoint registers @osty.gc.safepoint_v1 — split from
// the explicit-root machinery so pointer-free functions don't pull in
// any unused GC declarations.
func (g *mirGen) declareSafepoint() {
	g.declareRuntime("osty.gc.safepoint_v1", "declare void @osty.gc.safepoint_v1(i64, ptr, i64)")
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
	case *mir.StorageLiveInstr:
		return nil
	case *mir.StorageDeadInstr:
		return g.emitStorageDead(x)
	}
	return unsupported("mir-mvp", fmt.Sprintf("instruction %T", inst))
}

func (g *mirGen) emitStorageDead(dead *mir.StorageDeadInstr) error {
	if dead == nil || !g.opts.EmitGC {
		return nil
	}
	loc := g.fn.Local(dead.Local)
	if !g.isManagedLocal(loc) {
		return nil
	}
	slot := g.localSlots[dead.Local]
	if slot == "" {
		return nil
	}
	// The safepoint root arrays cache slot addresses, not values. Nulling
	// the managed slot on StorageDead retires that root without rebuilding
	// the array scaffolding at each poll site.
	g.fnBuf.WriteString("  store ptr null, ptr ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteByte('\n')
	return nil
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
	switch callee := c.Callee.(type) {
	case *mir.FnRef:
		return g.emitDirectCall(c, callee)
	case *mir.IndirectCall:
		return g.emitIndirectCall(c, callee)
	}
	return unsupported("mir-mvp", fmt.Sprintf("call target %T", c.Callee))
}

// emitDirectCall handles `call <ret> @<symbol>(args)` — the common
// case where the callee's signature is known at compile time.
func (g *mirGen) emitDirectCall(c *mir.CallInstr, fnRef *mir.FnRef) error {
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
	return g.emitCallSiteByName(c, fnRef.Symbol, sig.retLLVM, argStrs)
}

// emitIndirectCall handles calls through a closure env pointer. The
// MIR closure ABI packs every closure value as `ptr env` where env =
// `{ ptr fn, cap0, cap1, ... }`. At the call site we:
//
//  1. Take the operand — it is already the env ptr (produced by
//     AggregateRV{AggClosure} boxing, or by the lifted FnConst
//     wrapper below).
//  2. Load the fn ptr from env[0].
//  3. Call the lifted fn with the env as implicit first arg plus the
//     user args that the MIR call site carries.
//
// The user-visible signature (the MIR FnType on the callee operand)
// intentionally does NOT include the env param — the emitter knows
// about env and widens the LLVM signature here.
func (g *mirGen) emitIndirectCall(c *mir.CallInstr, callee *mir.IndirectCall) error {
	calleeOp := callee.Callee
	if calleeOp == nil {
		return unsupported("mir-mvp", "indirect call with nil callee")
	}
	fnT, ok := calleeOp.Type().(*ir.FnType)
	if !ok {
		return unsupported("mir-mvp", fmt.Sprintf("indirect call on non-function type %s", mirTypeString(calleeOp.Type())))
	}
	// Evaluate the operand to get the env ptr.
	envPtr, err := g.evalOperand(calleeOp, calleeOp.Type())
	if err != nil {
		return err
	}
	// Load the fn pointer from env[0]. All closure envs share `ptr` as
	// the slot-0 type, so we read it directly as a bare ptr load.
	fnPtr := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(fnPtr)
	g.fnBuf.WriteString(" = load ptr, ptr ")
	g.fnBuf.WriteString(envPtr)
	g.fnBuf.WriteByte('\n')
	// Lower args against the declared FnType signature (user args).
	retLLVM := "void"
	if fnT.Return != nil && !isUnitType(fnT.Return) {
		retLLVM = g.llvmType(fnT.Return)
	}
	argStrs := make([]string, 0, 1+len(c.Args))
	argStrs = append(argStrs, "ptr "+envPtr) // env as implicit first arg
	for i, op := range c.Args {
		var paramT mir.Type
		if i < len(fnT.Params) {
			paramT = fnT.Params[i]
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
	// Build the LLVM call-type string with env prepended.
	paramParts := make([]string, 0, 1+len(fnT.Params))
	paramParts = append(paramParts, "ptr")
	for _, p := range fnT.Params {
		paramParts = append(paramParts, g.llvmType(p))
	}
	callType := retLLVM + " (" + strings.Join(paramParts, ", ") + ")"
	return g.emitCallSiteIndirect(c, callType, fnPtr, retLLVM, argStrs)
}

// emitCallSiteByName writes the actual `call` / `store` / void-call
// lines for a direct fn-symbol call.
func (g *mirGen) emitCallSiteByName(c *mir.CallInstr, symbol, retLLVM string, argStrs []string) error {
	if c.Dest != nil {
		destLoc := g.fn.Local(c.Dest.Local)
		if destLoc == nil {
			return fmt.Errorf("mir-mvp: call dest into unknown local %d", c.Dest.Local)
		}
		if isUnitType(destLoc.Type) {
			g.fnBuf.WriteString("  call void @")
			g.fnBuf.WriteString(symbol)
			g.fnBuf.WriteByte('(')
			g.fnBuf.WriteString(strings.Join(argStrs, ", "))
			g.fnBuf.WriteString(")\n")
			return nil
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = call ")
		g.fnBuf.WriteString(retLLVM)
		g.fnBuf.WriteString(" @")
		g.fnBuf.WriteString(symbol)
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
	g.fnBuf.WriteString("  call ")
	g.fnBuf.WriteString(retLLVM)
	g.fnBuf.WriteString(" @")
	g.fnBuf.WriteString(symbol)
	g.fnBuf.WriteByte('(')
	g.fnBuf.WriteString(strings.Join(argStrs, ", "))
	g.fnBuf.WriteString(")\n")
	return nil
}

// emitCallSiteIndirect writes the call sequence for an indirect fn
// pointer invocation (`call <ret> (<params>) %ptr(args)`). Dest
// semantics mirror the direct path.
func (g *mirGen) emitCallSiteIndirect(c *mir.CallInstr, callType, ptrVal, retLLVM string, argStrs []string) error {
	if c.Dest != nil {
		destLoc := g.fn.Local(c.Dest.Local)
		if destLoc == nil {
			return fmt.Errorf("mir-mvp: call dest into unknown local %d", c.Dest.Local)
		}
		if isUnitType(destLoc.Type) {
			g.fnBuf.WriteString("  call ")
			g.fnBuf.WriteString(callType)
			g.fnBuf.WriteByte(' ')
			g.fnBuf.WriteString(ptrVal)
			g.fnBuf.WriteByte('(')
			g.fnBuf.WriteString(strings.Join(argStrs, ", "))
			g.fnBuf.WriteString(")\n")
			return nil
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = call ")
		g.fnBuf.WriteString(callType)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(ptrVal)
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
	g.fnBuf.WriteString("  call ")
	g.fnBuf.WriteString(callType)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(ptrVal)
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
	case mir.IntrinsicListPush, mir.IntrinsicListLen, mir.IntrinsicListGet,
		mir.IntrinsicListIsEmpty, mir.IntrinsicListSorted, mir.IntrinsicListToSet:
		return g.emitListIntrinsic(i)
	case mir.IntrinsicMapNew, mir.IntrinsicMapGet, mir.IntrinsicMapSet,
		mir.IntrinsicMapContains, mir.IntrinsicMapLen, mir.IntrinsicMapKeys,
		mir.IntrinsicMapRemove:
		return g.emitMapIntrinsic(i)
	case mir.IntrinsicSetInsert, mir.IntrinsicSetContains, mir.IntrinsicSetLen,
		mir.IntrinsicSetToList, mir.IntrinsicSetRemove:
		return g.emitSetIntrinsic(i)
	case mir.IntrinsicBytesLen, mir.IntrinsicBytesIsEmpty, mir.IntrinsicBytesGet, mir.IntrinsicBytesContains, mir.IntrinsicBytesStartsWith, mir.IntrinsicBytesEndsWith, mir.IntrinsicBytesIndexOf, mir.IntrinsicBytesLastIndexOf, mir.IntrinsicBytesSplit, mir.IntrinsicBytesJoin, mir.IntrinsicBytesConcat, mir.IntrinsicBytesRepeat, mir.IntrinsicBytesReplace, mir.IntrinsicBytesReplaceAll, mir.IntrinsicBytesTrimLeft, mir.IntrinsicBytesTrimRight, mir.IntrinsicBytesTrim, mir.IntrinsicBytesTrimSpace, mir.IntrinsicBytesToUpper, mir.IntrinsicBytesToLower, mir.IntrinsicBytesToHex, mir.IntrinsicBytesSlice:
		return g.emitBytesIntrinsic(i)
	case mir.IntrinsicStringConcat, mir.IntrinsicStringChars, mir.IntrinsicStringBytes,
		mir.IntrinsicStringLen, mir.IntrinsicStringIsEmpty,
		mir.IntrinsicStringToUpper, mir.IntrinsicStringToLower,
		mir.IntrinsicStringToInt, mir.IntrinsicStringToFloat:
		return g.emitStringIntrinsic(i)
	case mir.IntrinsicChanMake, mir.IntrinsicChanSend, mir.IntrinsicChanRecv,
		mir.IntrinsicChanClose, mir.IntrinsicChanIsClosed:
		return g.emitChannelIntrinsic(i)
	case mir.IntrinsicTaskGroup, mir.IntrinsicSpawn, mir.IntrinsicHandleJoin,
		mir.IntrinsicGroupCancel, mir.IntrinsicGroupIsCancelled:
		return g.emitTaskIntrinsic(i)
	case mir.IntrinsicSelect, mir.IntrinsicSelectRecv, mir.IntrinsicSelectSend,
		mir.IntrinsicSelectTimeout, mir.IntrinsicSelectDefault:
		return g.emitSelectIntrinsic(i)
	case mir.IntrinsicIsCancelled, mir.IntrinsicCheckCancelled,
		mir.IntrinsicYield, mir.IntrinsicSleep:
		return g.emitCancelIntrinsic(i)
	case mir.IntrinsicParallel, mir.IntrinsicRace, mir.IntrinsicCollectAll:
		return g.emitConcurrencyHelperIntrinsic(i)
	case mir.IntrinsicRawNull:
		return g.emitRuntimeRawNull(i)
	}
	return unsupported("mir-mvp", fmt.Sprintf("intrinsic %s", mirIntrinsicLabel(i.Kind)))
}

// ==== list / map / set intrinsics ====

// emitListIntrinsic dispatches each list intrinsic to its runtime
// symbol. Element type flows through the receiver (first arg); the
// emitter reads its LLVM form to pick the typed or bytes-fallback
// variant. Dest semantics match the MIR intrinsic contract.
func (g *mirGen) emitListIntrinsic(i *mir.IntrinsicInstr) error {
	if len(i.Args) < 1 {
		return unsupported("mir-mvp", "list intrinsic with no receiver")
	}
	listOp := i.Args[0]
	listReg, err := g.evalOperand(listOp, listOp.Type())
	if err != nil {
		return err
	}
	elemT := listElemType(listOp.Type())
	if elemT == nil {
		return unsupported("mir-mvp", "list intrinsic: missing element type")
	}
	elemLLVM := g.llvmType(elemT)
	switch i.Kind {
	case mir.IntrinsicListPush:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "list_push arity")
		}
		return g.emitListPushOperand(listReg, i.Args[1], elemT)
	case mir.IntrinsicListLen:
		if local, ok := mirOperandRootLocal(listOp); ok {
			if cached := g.vectorListLens[local]; cached != "" {
				return g.storeIntrinsicResult(i, &LlvmValue{typ: "i64", name: cached})
			}
		}
		sym := listRuntimeLenSymbol()
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmListLen(em, &LlvmValue{typ: "ptr", name: listReg})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicListIsEmpty:
		sym := "osty_rt_list_is_empty"
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr)")
		return g.emitSimpleCall(i, sym, "i1", []string{"ptr " + listReg})
	case mir.IntrinsicListGet:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "list_get arity")
		}
		idxOp := i.Args[1]
		idxReg, err := g.evalOperand(idxOp, idxOp.Type())
		if err != nil {
			return err
		}
		if listUsesTypedRuntime(elemLLVM) {
			if local, ok := mirOperandRootLocal(listOp); ok && listUsesRawDataFastPath(elemLLVM) {
				if fast, ok := g.emitVectorListFastLoad(local, idxReg, elemLLVM); ok {
					return g.storeIntrinsicResult(i, &LlvmValue{typ: elemLLVM, name: fast})
				}
			}
			sym := listRuntimeGetSymbol(elemLLVM)
			g.declareRuntime(sym, "declare "+elemLLVM+" @"+sym+"(ptr, i64)")
			em := g.ostyEmitter()
			result := llvmListGet(em,
				&LlvmValue{typ: "ptr", name: listReg},
				&LlvmValue{typ: "i64", name: idxReg},
				elemLLVM)
			g.flushOstyEmitter(em)
			return g.storeIntrinsicResult(i, result)
		}
		// Composite element — use the bytes-v1 helper with a stack
		// slot as the out-pointer. Backend writes the value into the
		// slot; we load it back and store into the intrinsic's dest
		// (if any).
		return g.emitListGetBytes(i, listReg, idxReg, elemT)
	case mir.IntrinsicListSorted:
		elemString := isStringLLVMType(elemT)
		sym := listRuntimeSortedSymbol(elemLLVM, elemString)
		if sym == "" {
			return unsupported("mir-mvp", "list_sorted on element type "+elemLLVM)
		}
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: listReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicListToSet:
		elemString := isStringLLVMType(elemT)
		sym := listRuntimeToSetSymbol(elemLLVM, elemString)
		if sym == "" {
			return unsupported("mir-mvp", "list_to_set on element type "+elemLLVM)
		}
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: listReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	}
	return unsupported("mir-mvp", fmt.Sprintf("list intrinsic kind %d", i.Kind))
}

// emitMapIntrinsic dispatches map intrinsics to runtime symbols. Key
// type dictates the suffix (`_i64`, `_string`, `_bytes`, …).
func (g *mirGen) emitMapIntrinsic(i *mir.IntrinsicInstr) error {
	if i.Kind == mir.IntrinsicMapNew {
		sym := "osty_rt_map_new"
		g.declareRuntime(sym, "declare ptr @"+sym+"()")
		return g.emitSimpleCall(i, sym, "ptr", nil)
	}
	if len(i.Args) < 1 {
		return unsupported("mir-mvp", "map intrinsic with no receiver")
	}
	mapReg, err := g.evalOperand(i.Args[0], i.Args[0].Type())
	if err != nil {
		return err
	}
	keyT, valT := mapKeyValueTypes(i.Args[0].Type())
	keyLLVM := g.llvmType(keyT)
	keyString := isStringLLVMType(keyT)
	_ = valT
	switch i.Kind {
	case mir.IntrinsicMapLen:
		sym := llvmMapRuntimeLenSymbol()
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmMapLen(em, &LlvmValue{typ: "ptr", name: mapReg})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicMapKeys:
		sym := mapRuntimeKeysSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmMapKeys(em, &LlvmValue{typ: "ptr", name: mapReg})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicMapContains:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "map_contains arity")
		}
		kReg, err := g.evalOperand(i.Args[1], keyT)
		if err != nil {
			return err
		}
		sym := mapRuntimeContainsSymbol(keyLLVM, keyString)
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr, "+keyLLVM+")")
		em := g.ostyEmitter()
		result := llvmMapContains(em,
			&LlvmValue{typ: "ptr", name: mapReg},
			&LlvmValue{typ: keyLLVM, name: kReg},
			keyString)
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicMapGet:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "map_get arity")
		}
		kReg, err := g.evalOperand(i.Args[1], keyT)
		if err != nil {
			return err
		}
		vLLVM := g.llvmType(valT)
		// When the destination local's LLVM type matches the map's
		// value type exactly, hand the runtime the dest slot itself —
		// the runtime will memcpy straight into it, skipping the
		// fresh-alloca + load + store we'd otherwise need.
		if i.Dest != nil {
			destLoc := g.fn.Local(i.Dest.Local)
			if destLoc == nil {
				return fmt.Errorf("mir-mvp: map_get into unknown local %d", i.Dest.Local)
			}
			if g.llvmType(destLoc.Type) == vLLVM {
				g.emitMapGetOrAbort(mapReg, kReg, keyLLVM, keyString, vLLVM, g.localSlots[i.Dest.Local])
				return nil
			}
			// Type mismatch (a future coercion shape): fall through
			// to the alloca-load path so the store retains its
			// declared type width.
			loaded := g.emitMapGetOrAbort(mapReg, kReg, keyLLVM, keyString, vLLVM, "")
			g.fnBuf.WriteString("  store ")
			g.fnBuf.WriteString(g.llvmType(destLoc.Type))
			g.fnBuf.WriteByte(' ')
			g.fnBuf.WriteString(loaded)
			g.fnBuf.WriteString(", ptr ")
			g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
			g.fnBuf.WriteByte('\n')
			return nil
		}
		// No destination — still perform the call for its side
		// effects (abort-on-miss).
		g.emitMapGetOrAbort(mapReg, kReg, keyLLVM, keyString, vLLVM, "")
		return nil
	case mir.IntrinsicMapSet:
		if len(i.Args) != 3 {
			return unsupported("mir-mvp", "map_set arity")
		}
		kReg, err := g.evalOperand(i.Args[1], keyT)
		if err != nil {
			return err
		}
		vOp := i.Args[2]
		vReg, err := g.evalOperand(vOp, vOp.Type())
		if err != nil {
			return err
		}
		vLLVM := g.llvmType(vOp.Type())
		sym := mapRuntimeInsertSymbol(keyLLVM, keyString)
		g.declareRuntime(sym, "declare void @"+sym+"(ptr, "+keyLLVM+", ptr)")
		// Spill the value into a stack slot so the runtime can memcpy
		// value_size bytes from it. This replaces the old inttoptr
		// widening shim, which was semantically wrong for primitives
		// (it passed the value bitpattern AS a pointer) and outright
		// impossible for structs / tuples.
		em := g.ostyEmitter()
		slot := llvmSpillToSlot(em, &LlvmValue{typ: vLLVM, name: vReg})
		llvmMapInsert(em,
			&LlvmValue{typ: "ptr", name: mapReg},
			&LlvmValue{typ: keyLLVM, name: kReg},
			slot,
			keyString)
		g.flushOstyEmitter(em)
		return nil
	case mir.IntrinsicMapRemove:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "map_remove arity")
		}
		kReg, err := g.evalOperand(i.Args[1], keyT)
		if err != nil {
			return err
		}
		sym := mapRuntimeRemoveSymbol(keyLLVM, keyString)
		// Runtime returns i1 (true = was present); MIR ignores it — the
		// temp fall-through to DCE is fine but the decl must match the
		// C runtime so the verifier accepts the call.
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr, "+keyLLVM+")")
		em := g.ostyEmitter()
		_ = llvmMapRemove(em,
			&LlvmValue{typ: "ptr", name: mapReg},
			&LlvmValue{typ: keyLLVM, name: kReg},
			keyString)
		g.flushOstyEmitter(em)
		return nil
	}
	return unsupported("mir-mvp", fmt.Sprintf("map intrinsic kind %d", i.Kind))
}

// emitMapGetOrAbort emits the runtime call
// `void osty_rt_map_get_or_abort_<suffix>(ptr map, K key, ptr out)`
// and returns the SSA register holding the loaded value. If
// `outSlot` is non-empty the caller has pre-allocated the
// destination; the helper hands it straight to the runtime, skips
// the post-call load, and returns "". If `outSlot` is empty the
// helper allocates a fresh V-sized slot and loads through it.
//
// Shared between `IntrinsicMapGet` and `IndexProj` on a map base so
// `m.get(k)` and `m[k]` can't drift in ABI.
func (g *mirGen) emitMapGetOrAbort(mapReg, keyReg, keyLLVM string, keyString bool, valLLVM, outSlot string) string {
	sym := mapRuntimeGetOrAbortSymbol(keyLLVM, keyString)
	g.declareRuntime(sym, "declare void @"+sym+"(ptr, "+keyLLVM+", ptr)")
	em := g.ostyEmitter()
	var slot *LlvmValue
	ownsSlot := outSlot == ""
	if ownsSlot {
		slot = llvmAllocaSlot(em, valLLVM)
	} else {
		slot = &LlvmValue{typ: "ptr", name: outSlot, pointer: false}
	}
	llvmMapGetOrAbort(em,
		&LlvmValue{typ: "ptr", name: mapReg},
		&LlvmValue{typ: keyLLVM, name: keyReg},
		slot,
		keyString)
	if !ownsSlot {
		g.flushOstyEmitter(em)
		return ""
	}
	loaded := llvmLoadFromSlot(em, slot, valLLVM)
	g.flushOstyEmitter(em)
	return loaded.name
}

// spillToSlot materialises `val` (an SSA register of `llvmType`) in
// an `alloca <llvmType>` stack slot and returns the slot register.
// Useful when a runtime call takes its value by pointer — composite
// map values, composite list elements, etc. Thin shim over the
// Osty-owned `llvmSpillToSlot`; keep the `mirGen` receiver so existing
// call sites don't need to thread an emitter.
func (g *mirGen) spillToSlot(val, llvmType string) string {
	em := g.ostyEmitter()
	slot := llvmSpillToSlot(em, &LlvmValue{typ: llvmType, name: val})
	g.flushOstyEmitter(em)
	return slot.name
}

// emitSetIntrinsic dispatches set intrinsics to runtime symbols.
func (g *mirGen) emitSetIntrinsic(i *mir.IntrinsicInstr) error {
	if len(i.Args) < 1 {
		return unsupported("mir-mvp", "set intrinsic with no receiver")
	}
	setReg, err := g.evalOperand(i.Args[0], i.Args[0].Type())
	if err != nil {
		return err
	}
	elemT := setElemType(i.Args[0].Type())
	elemLLVM := g.llvmType(elemT)
	elemString := isStringLLVMType(elemT)
	switch i.Kind {
	case mir.IntrinsicSetLen:
		sym := setRuntimeLenSymbol()
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmSetLen(em, &LlvmValue{typ: "ptr", name: setReg})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicSetToList:
		sym := setRuntimeToListSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmSetToList(em, &LlvmValue{typ: "ptr", name: setReg})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicSetContains:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "set_contains arity")
		}
		vReg, err := g.evalOperand(i.Args[1], elemT)
		if err != nil {
			return err
		}
		sym := setRuntimeContainsSymbol(elemLLVM, elemString)
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr, "+elemLLVM+")")
		em := g.ostyEmitter()
		result := llvmSetContains(em,
			&LlvmValue{typ: "ptr", name: setReg},
			&LlvmValue{typ: elemLLVM, name: vReg},
			elemString)
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicSetInsert:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "set_insert arity")
		}
		vReg, err := g.evalOperand(i.Args[1], elemT)
		if err != nil {
			return err
		}
		sym := setRuntimeInsertSymbol(elemLLVM, elemString)
		// Runtime returns i1 (true = newly added); MIR discards the
		// result when this intrinsic is used as a statement. Decl must
		// match the C runtime — the temp ends up DCE'd.
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr, "+elemLLVM+")")
		em := g.ostyEmitter()
		_ = llvmSetInsert(em,
			&LlvmValue{typ: "ptr", name: setReg},
			&LlvmValue{typ: elemLLVM, name: vReg},
			elemString)
		g.flushOstyEmitter(em)
		return nil
	case mir.IntrinsicSetRemove:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "set_remove arity")
		}
		vReg, err := g.evalOperand(i.Args[1], elemT)
		if err != nil {
			return err
		}
		sym := setRuntimeRemoveSymbol(elemLLVM, elemString)
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr, "+elemLLVM+")")
		em := g.ostyEmitter()
		result := llvmSetRemove(em,
			&LlvmValue{typ: "ptr", name: setReg},
			&LlvmValue{typ: elemLLVM, name: vReg},
			elemString)
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	}
	return unsupported("mir-mvp", fmt.Sprintf("set intrinsic kind %d", i.Kind))
}

// emitBytesIntrinsic dispatches bytes primitive methods to runtime
// symbols. Bytes values flow through the runtime as opaque pointers
// to `osty_rt_bytes { ptr data; i64 len }`. The runtime functions
// live beside the list/string helpers in osty_runtime.c.
func (g *mirGen) emitBytesIntrinsic(i *mir.IntrinsicInstr) error {
	if len(i.Args) < 1 {
		return unsupported("mir-mvp", "bytes intrinsic with no receiver")
	}
	bytesOp := i.Args[0]
	bytesReg, err := g.evalOperand(bytesOp, bytesOp.Type())
	if err != nil {
		return err
	}
	switch i.Kind {
	case mir.IntrinsicBytesLen:
		sym := "osty_rt_bytes_len"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "i64", sym, []*LlvmValue{{typ: "ptr", name: bytesReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesIsEmpty:
		sym := "osty_rt_bytes_is_empty"
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "i1", sym, []*LlvmValue{{typ: "ptr", name: bytesReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesGet:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_get arity")
		}
		if i.Dest == nil {
			return nil
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return fmt.Errorf("mir-mvp: bytes_get dest %d", i.Dest.Local)
		}
		optT, ok := destLoc.Type.(*ir.OptionalType)
		if !ok {
			return unsupported("mir-mvp", "bytes_get dest is not optional")
		}
		idxReg, err := g.evalOperand(i.Args[1], mir.TInt)
		if err != nil {
			return err
		}
		lenSym := "osty_rt_bytes_len"
		getSym := "osty_rt_bytes_get"
		g.declareRuntime(lenSym, "declare i64 @"+lenSym+"(ptr)")
		g.declareRuntime(getSym, "declare i8 @"+getSym+"(ptr, i64)")
		em := g.ostyEmitter()
		length := llvmCall(em, "i64", lenSym, []*LlvmValue{{typ: "ptr", name: bytesReg}})
		nonNegative := llvmCompare(em, "sge", &LlvmValue{typ: "i64", name: idxReg}, llvmIntLiteral(0))
		beforeEnd := llvmCompare(em, "slt", &LlvmValue{typ: "i64", name: idxReg}, length)
		inRange := llvmLogicalI1(em, "and", nonNegative, beforeEnd)
		g.flushOstyEmitter(em)

		someLabel := g.freshLabel("bytes.get.some")
		noneLabel := g.freshLabel("bytes.get.none")
		mergeLabel := g.freshLabel("bytes.get.merge")
		g.fnBuf.WriteString("  br i1 ")
		g.fnBuf.WriteString(inRange.name)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteByte('\n')

		optLLVM := g.llvmType(optT)

		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(":\n")
		em = g.ostyEmitter()
		item := llvmCall(em, "i8", getSym, []*LlvmValue{{typ: "ptr", name: bytesReg}, {typ: "i64", name: idxReg}})
		g.flushOstyEmitter(em)
		payload, err := g.toI64Slot(item.name, optT.Inner)
		if err != nil {
			return err
		}
		someStep1 := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(someStep1)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" undef, i64 1, 0\n")
		someValue := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(someValue)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(someStep1)
		g.fnBuf.WriteString(", i64 ")
		g.fnBuf.WriteString(payload)
		g.fnBuf.WriteString(", 1\n")
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteByte('\n')

		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteString(":\n")
		noneStep1 := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(noneStep1)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" undef, i64 0, 0\n")
		noneValue := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(noneValue)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(noneStep1)
		g.fnBuf.WriteString(", i64 0, 1\n")
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteByte('\n')

		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteString(":\n")
		result := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(result)
		g.fnBuf.WriteString(" = phi ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" [ ")
		g.fnBuf.WriteString(someValue)
		g.fnBuf.WriteString(", %")
		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(" ], [ ")
		g.fnBuf.WriteString(noneValue)
		g.fnBuf.WriteString(", %")
		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteString(" ]\n")
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(result)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
		g.fnBuf.WriteByte('\n')
		return nil
	case mir.IntrinsicBytesContains:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_contains arity")
		}
		needleReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_index_of"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		index := llvmCall(em, "i64", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: needleReg},
		})
		result := llvmCompare(em, "sge", index, llvmIntLiteral(0))
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesStartsWith:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_starts_with arity")
		}
		prefixReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_index_of"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		index := llvmCall(em, "i64", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: prefixReg},
		})
		result := llvmCompare(em, "eq", index, llvmIntLiteral(0))
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesEndsWith:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_ends_with arity")
		}
		suffixReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		lastSym := "osty_rt_bytes_last_index_of"
		lenSym := "osty_rt_bytes_len"
		g.declareRuntime(lastSym, "declare i64 @"+lastSym+"(ptr, ptr)")
		g.declareRuntime(lenSym, "declare i64 @"+lenSym+"(ptr)")
		em := g.ostyEmitter()
		index := llvmCall(em, "i64", lastSym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: suffixReg},
		})
		baseLen := llvmCall(em, "i64", lenSym, []*LlvmValue{{typ: "ptr", name: bytesReg}})
		suffixLen := llvmCall(em, "i64", lenSym, []*LlvmValue{{typ: "ptr", name: suffixReg}})
		expected := llvmNextTemp(em)
		em.body = append(em.body, fmt.Sprintf("  %s = sub i64 %s, %s", expected, baseLen.name, suffixLen.name))
		found := llvmCompare(em, "sge", index, llvmIntLiteral(0))
		atEnd := llvmCompare(em, "eq", index, &LlvmValue{typ: "i64", name: expected})
		result := llvmLogicalI1(em, "and", found, atEnd)
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesIndexOf:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_index_of arity")
		}
		if i.Dest == nil {
			return nil
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return fmt.Errorf("mir-mvp: bytes_index_of dest %d", i.Dest.Local)
		}
		optT, ok := destLoc.Type.(*ir.OptionalType)
		if !ok {
			return unsupported("mir-mvp", "bytes_index_of dest is not optional")
		}
		needleReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_index_of"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		index := llvmCall(em, "i64", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: needleReg},
		})
		present := llvmCompare(em, "sge", index, llvmIntLiteral(0))
		g.flushOstyEmitter(em)

		someLabel := g.freshLabel("bytes.index_of.some")
		noneLabel := g.freshLabel("bytes.index_of.none")
		mergeLabel := g.freshLabel("bytes.index_of.merge")
		g.fnBuf.WriteString("  br i1 ")
		g.fnBuf.WriteString(present.name)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteByte('\n')

		optLLVM := g.llvmType(optT)

		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(":\n")
		someStep1 := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(someStep1)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" undef, i64 1, 0\n")
		someValue := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(someValue)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(someStep1)
		g.fnBuf.WriteString(", i64 ")
		g.fnBuf.WriteString(index.name)
		g.fnBuf.WriteString(", 1\n")
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteByte('\n')

		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteString(":\n")
		noneStep1 := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(noneStep1)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" undef, i64 0, 0\n")
		noneValue := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(noneValue)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(noneStep1)
		g.fnBuf.WriteString(", i64 0, 1\n")
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteByte('\n')

		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteString(":\n")
		result := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(result)
		g.fnBuf.WriteString(" = phi ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" [ ")
		g.fnBuf.WriteString(someValue)
		g.fnBuf.WriteString(", %")
		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(" ], [ ")
		g.fnBuf.WriteString(noneValue)
		g.fnBuf.WriteString(", %")
		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteString(" ]\n")
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(result)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
		g.fnBuf.WriteByte('\n')
		return nil
	case mir.IntrinsicBytesLastIndexOf:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_last_index_of arity")
		}
		if i.Dest == nil {
			return nil
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return fmt.Errorf("mir-mvp: bytes_last_index_of dest %d", i.Dest.Local)
		}
		optT, ok := destLoc.Type.(*ir.OptionalType)
		if !ok {
			return unsupported("mir-mvp", "bytes_last_index_of dest is not optional")
		}
		needleReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_last_index_of"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		index := llvmCall(em, "i64", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: needleReg},
		})
		present := llvmCompare(em, "sge", index, llvmIntLiteral(0))
		g.flushOstyEmitter(em)

		someLabel := g.freshLabel("bytes.last_index_of.some")
		noneLabel := g.freshLabel("bytes.last_index_of.none")
		mergeLabel := g.freshLabel("bytes.last_index_of.merge")
		g.fnBuf.WriteString("  br i1 ")
		g.fnBuf.WriteString(present.name)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteByte('\n')

		optLLVM := g.llvmType(optT)

		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(":\n")
		someStep1 := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(someStep1)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" undef, i64 1, 0\n")
		someValue := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(someValue)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(someStep1)
		g.fnBuf.WriteString(", i64 ")
		g.fnBuf.WriteString(index.name)
		g.fnBuf.WriteString(", 1\n")
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteByte('\n')

		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteString(":\n")
		noneStep1 := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(noneStep1)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" undef, i64 0, 0\n")
		noneValue := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(noneValue)
		g.fnBuf.WriteString(" = insertvalue ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(noneStep1)
		g.fnBuf.WriteString(", i64 0, 1\n")
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteByte('\n')

		g.fnBuf.WriteString(mergeLabel)
		g.fnBuf.WriteString(":\n")
		result := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(result)
		g.fnBuf.WriteString(" = phi ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteString(" [ ")
		g.fnBuf.WriteString(someValue)
		g.fnBuf.WriteString(", %")
		g.fnBuf.WriteString(someLabel)
		g.fnBuf.WriteString(" ], [ ")
		g.fnBuf.WriteString(noneValue)
		g.fnBuf.WriteString(", %")
		g.fnBuf.WriteString(noneLabel)
		g.fnBuf.WriteString(" ]\n")
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(optLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(result)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
		g.fnBuf.WriteByte('\n')
		return nil
	case mir.IntrinsicBytesSplit:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_split arity")
		}
		sepReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_split"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: sepReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesJoin:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_join arity")
		}
		partsReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_join"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: partsReg},
			{typ: "ptr", name: bytesReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesConcat:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_concat arity")
		}
		rightReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_concat"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: rightReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesRepeat:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_repeat arity")
		}
		countReg, err := g.evalOperand(i.Args[1], mir.TInt)
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_repeat"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, i64)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "i64", name: countReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesReplace:
		if len(i.Args) != 3 {
			return unsupported("mir-mvp", "bytes_replace arity")
		}
		oldReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		newReg, err := g.evalOperand(i.Args[2], i.Args[2].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_replace"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: oldReg},
			{typ: "ptr", name: newReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesReplaceAll:
		if len(i.Args) != 3 {
			return unsupported("mir-mvp", "bytes_replace_all arity")
		}
		oldReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		newReg, err := g.evalOperand(i.Args[2], i.Args[2].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_replace_all"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: oldReg},
			{typ: "ptr", name: newReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesTrimLeft:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_trim_left arity")
		}
		stripReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_trim_left"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: stripReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesTrimRight:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_trim_right arity")
		}
		stripReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_trim_right"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: stripReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesTrim:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "bytes_trim arity")
		}
		stripReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_trim"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "ptr", name: stripReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesTrimSpace:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "bytes_trim_space arity")
		}
		sym := "osty_rt_bytes_trim_space"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesToUpper:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "bytes_to_upper arity")
		}
		sym := "osty_rt_bytes_to_upper"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesToLower:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "bytes_to_lower arity")
		}
		sym := "osty_rt_bytes_to_lower"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesToHex:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "bytes_to_hex arity")
		}
		sym := "osty_rt_bytes_to_hex"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicBytesSlice:
		if len(i.Args) != 3 {
			return unsupported("mir-mvp", "bytes_slice arity")
		}
		startReg, err := g.evalOperand(i.Args[1], mir.TInt)
		if err != nil {
			return err
		}
		endReg, err := g.evalOperand(i.Args[2], mir.TInt)
		if err != nil {
			return err
		}
		sym := "osty_rt_bytes_slice"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, i64, i64)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: bytesReg},
			{typ: "i64", name: startReg},
			{typ: "i64", name: endReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	}
	return unsupported("mir-mvp", fmt.Sprintf("bytes intrinsic kind %d", i.Kind))
}

// emitStringIntrinsic dispatches String receiver intrinsics that the
// MIR lowerer emits for stdlib method calls (`.chars()`, `.bytes()`,
// `.len()`, `.isEmpty()`, `.toUpper()`, `.toLower()`). Each maps to a
// runtime symbol in `osty_runtime.c`'s strings family.
//
// The Stage 5 prep here mirrors the legacy `expr.go` dispatch: `.len`
// calls `osty_rt_strings_ByteLen`; `.isEmpty` composes a `byte_len == 0`
// compare instead of a dedicated runtime symbol; `.chars` / `.bytes`
// call the list-building helpers that the runtime already ships; and
// `.toUpper()` / `.toLower()` route through ASCII-preserving string
// transforms in the runtime; and `.toInt()` / `.toFloat()` rebuild the
// MIR `{i64 disc, i64 payload}` Result layout after validating the
// parse at the runtime boundary.
func (g *mirGen) emitStringIntrinsic(i *mir.IntrinsicInstr) error {
	if i.Kind == mir.IntrinsicStringConcat {
		if len(i.Args) == 0 {
			return unsupported("mir-mvp", "string_concat with no args")
		}
		sym := llvmStringRuntimeConcatSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
		var acc *LlvmValue
		for idx, op := range i.Args {
			if !isStringLLVMType(op.Type()) {
				return unsupported("mir-mvp", fmt.Sprintf("string_concat arg %d type %s", idx+1, mirTypeString(op.Type())))
			}
			partReg, err := g.evalOperand(op, op.Type())
			if err != nil {
				return err
			}
			part := &LlvmValue{typ: "ptr", name: partReg}
			if acc == nil {
				acc = part
				continue
			}
			em := g.ostyEmitter()
			acc = llvmStringConcat(em, acc, part)
			g.flushOstyEmitter(em)
		}
		if acc == nil {
			return unsupported("mir-mvp", "string_concat produced no value")
		}
		return g.storeIntrinsicResult(i, acc)
	}
	if len(i.Args) < 1 {
		return unsupported("mir-mvp", "string intrinsic with no receiver")
	}
	strOp := i.Args[0]
	strReg, err := g.evalOperand(strOp, strOp.Type())
	if err != nil {
		return err
	}
	switch i.Kind {
	case mir.IntrinsicStringChars:
		sym := "osty_rt_strings_Chars"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: strReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicStringBytes:
		sym := "osty_rt_strings_Bytes"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: strReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicStringLen:
		sym := "osty_rt_strings_ByteLen"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "i64", sym, []*LlvmValue{{typ: "ptr", name: strReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicStringIsEmpty:
		// Runtime has no `_IsEmpty`; reuse `ByteLen` and emit an eq-0
		// compare, matching the legacy emitter's shape.
		sym := "osty_rt_strings_ByteLen"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		size := llvmCall(em, "i64", sym, []*LlvmValue{{typ: "ptr", name: strReg}})
		result := llvmCompare(em, "eq", size, llvmIntLiteral(0))
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicStringToUpper:
		sym := "osty_rt_strings_ToUpper"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: strReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicStringToLower:
		sym := "osty_rt_strings_ToLower"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: strReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicStringToInt:
		return g.emitStringParseResultIntrinsic(i, strReg, "osty_rt_strings_IsValidInt", "osty_rt_strings_ToInt", "i64")
	case mir.IntrinsicStringToFloat:
		return g.emitStringParseResultIntrinsic(i, strReg, "osty_rt_strings_IsValidFloat", "osty_rt_strings_ToFloat", "double")
	}
	return unsupported("mir-mvp", fmt.Sprintf("string intrinsic kind %d", i.Kind))
}

func (g *mirGen) emitStringParseResultIntrinsic(i *mir.IntrinsicInstr, strReg, validateSym, parseSym, parseLLVM string) error {
	if i.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(i.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: string parse dest %d", i.Dest.Local)
	}
	resultT, ok := destLoc.Type.(*ir.NamedType)
	if !ok || resultT.Name != "Result" || len(resultT.Args) < 2 {
		return unsupported("mir-mvp", "string parse dest is not Result")
	}
	resultLLVM := g.llvmType(destLoc.Type)
	g.declareRuntime(validateSym, "declare i1 @"+validateSym+"(ptr)")
	g.declareRuntime(parseSym, "declare "+parseLLVM+" @"+parseSym+"(ptr)")

	em := g.ostyEmitter()
	valid := llvmCall(em, "i1", validateSym, []*LlvmValue{{typ: "ptr", name: strReg}})
	g.flushOstyEmitter(em)

	okLabel := g.freshLabel("string.parse.ok")
	errLabel := g.freshLabel("string.parse.err")
	mergeLabel := g.freshLabel("string.parse.merge")
	g.fnBuf.WriteString("  br i1 ")
	g.fnBuf.WriteString(valid.name)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(okLabel)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(errLabel)
	g.fnBuf.WriteByte('\n')

	g.fnBuf.WriteString(okLabel)
	g.fnBuf.WriteString(":\n")
	em = g.ostyEmitter()
	parsed := llvmCall(em, parseLLVM, parseSym, []*LlvmValue{{typ: "ptr", name: strReg}})
	g.flushOstyEmitter(em)
	payload, err := g.toI64Slot(parsed.name, resultT.Args[0])
	if err != nil {
		return err
	}
	okStep1 := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(okStep1)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(resultLLVM)
	g.fnBuf.WriteString(" undef, i64 1, 0\n")
	okValue := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(okValue)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(resultLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(okStep1)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(payload)
	g.fnBuf.WriteString(", 1\n")
	g.fnBuf.WriteString("  br label %")
	g.fnBuf.WriteString(mergeLabel)
	g.fnBuf.WriteByte('\n')

	g.fnBuf.WriteString(errLabel)
	g.fnBuf.WriteString(":\n")
	errStep1 := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(errStep1)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(resultLLVM)
	g.fnBuf.WriteString(" undef, i64 0, 0\n")
	errValue := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(errValue)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(resultLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(errStep1)
	g.fnBuf.WriteString(", i64 0, 1\n")
	g.fnBuf.WriteString("  br label %")
	g.fnBuf.WriteString(mergeLabel)
	g.fnBuf.WriteByte('\n')

	g.fnBuf.WriteString(mergeLabel)
	g.fnBuf.WriteString(":\n")
	result := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(result)
	g.fnBuf.WriteString(" = phi ")
	g.fnBuf.WriteString(resultLLVM)
	g.fnBuf.WriteString(" [ ")
	g.fnBuf.WriteString(okValue)
	g.fnBuf.WriteString(", %")
	g.fnBuf.WriteString(okLabel)
	g.fnBuf.WriteString(" ], [ ")
	g.fnBuf.WriteString(errValue)
	g.fnBuf.WriteString(", %")
	g.fnBuf.WriteString(errLabel)
	g.fnBuf.WriteString(" ]\n")
	g.fnBuf.WriteString("  store ")
	g.fnBuf.WriteString(resultLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(result)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
	g.fnBuf.WriteByte('\n')
	return nil
}

// ==== concurrency intrinsics ====
//
// The concurrency family lowers to a stable-but-pending Osty runtime
// ABI: symbols are prefixed `osty_rt_thread_*` / `osty_rt_task_*` /
// `osty_rt_select_*` / `osty_rt_cancel_*` / `osty_rt_parallel_*`.
// The runtime that provides them is still under design; for now the
// emitter just produces correct LLVM text and declares the symbols —
// linking will fail until the runtime ships them. This lets MIR-
// consumed programs that mention concurrency pass through the emitter
// without falling back to the legacy path.

// emitChannelIntrinsic dispatches channel ops to runtime symbols.
// Element type is read off the channel's `Channel<T>` NamedType and
// dispatches through a scalar-vs-composite split mirroring the list
// runtime ABI: scalar elements use typed `_i64` / `_i1` / `_f64` /
// `_ptr` suffixes; composites go through the bytes ABI.
func (g *mirGen) emitChannelIntrinsic(i *mir.IntrinsicInstr) error {
	switch i.Kind {
	case mir.IntrinsicChanMake:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "chan_make arity")
		}
		capReg, err := g.evalOperand(i.Args[0], mir.TInt)
		if err != nil {
			return err
		}
		sym := llvmChanRuntimeMakeSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(i64)")
		em := g.ostyEmitter()
		result := llvmChanMake(em, &LlvmValue{typ: "i64", name: capReg})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicChanSend:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "chan_send arity")
		}
		chOp := i.Args[0]
		valOp := i.Args[1]
		chReg, err := g.evalOperand(chOp, chOp.Type())
		if err != nil {
			return err
		}
		elemT := channelElementType(chOp.Type())
		if elemT == nil {
			return unsupported("mir-mvp", "chan_send: missing element type")
		}
		valReg, err := g.evalOperand(valOp, elemT)
		if err != nil {
			return err
		}
		elemLLVM := g.llvmType(elemT)
		chanVal := &LlvmValue{typ: "ptr", name: chReg}
		if listUsesTypedRuntime(elemLLVM) {
			sym := llvmChanRuntimeSendSymbol(llvmListElementSuffix(elemLLVM))
			g.declareRuntime(sym, "declare void @"+sym+"(ptr, "+elemLLVM+")")
			em := g.ostyEmitter()
			llvmChanSend(em, chanVal, &LlvmValue{typ: elemLLVM, name: valReg})
			g.flushOstyEmitter(em)
			return nil
		}
		// Composite element — bytes fallback.
		sym := llvmChanRuntimeSendBytesSymbol()
		g.declareRuntime(sym, "declare void @"+sym+"(ptr, ptr, i64)")
		em := g.ostyEmitter()
		slot := llvmSpillToSlot(em, &LlvmValue{typ: elemLLVM, name: valReg})
		size := llvmSizeOf(em, elemLLVM)
		llvmChanSendBytes(em, chanVal, slot, size)
		g.flushOstyEmitter(em)
		return nil
	case mir.IntrinsicChanRecv:
		// Dest receives Option<T>. The runtime returns a `{i64 disc,
		// i64 payload}` aggregate matching the MIR enum layout, so we
		// call and store directly.
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "chan_recv arity")
		}
		chOp := i.Args[0]
		chReg, err := g.evalOperand(chOp, chOp.Type())
		if err != nil {
			return err
		}
		elemT := channelElementType(chOp.Type())
		if elemT == nil {
			return unsupported("mir-mvp", "chan_recv: missing element type")
		}
		elemLLVM := g.llvmType(elemT)
		sym := llvmChanRuntimeRecvSymbol(llvmChanElementSuffix(elemLLVM))
		g.declareRuntime(sym, "declare { i64, i64 } @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmChanRecv(em, &LlvmValue{typ: "ptr", name: chReg}, elemLLVM)
		if i.Dest == nil {
			g.flushOstyEmitter(em)
			return nil
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			g.flushOstyEmitter(em)
			return fmt.Errorf("mir-mvp: chan_recv dest %d", i.Dest.Local)
		}
		em.body = append(em.body, fmt.Sprintf("  store { i64, i64 } %s, ptr %s", result.name, g.localSlots[i.Dest.Local]))
		g.flushOstyEmitter(em)
		return nil
	case mir.IntrinsicChanClose:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "chan_close arity")
		}
		chReg, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		sym := llvmChanRuntimeCloseSymbol()
		g.declareRuntime(sym, "declare void @"+sym+"(ptr)")
		em := g.ostyEmitter()
		llvmChanClose(em, &LlvmValue{typ: "ptr", name: chReg})
		g.flushOstyEmitter(em)
		return nil
	case mir.IntrinsicChanIsClosed:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "chan_is_closed arity")
		}
		chReg, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		sym := llvmChanRuntimeIsClosedSymbol()
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmChanIsClosed(em, &LlvmValue{typ: "ptr", name: chReg})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	}
	return unsupported("mir-mvp", fmt.Sprintf("channel intrinsic kind %d", i.Kind))
}

// channelElementType pulls T out of `Channel<T>`. Returns nil when t
// isn't a channel shape.
func channelElementType(t mir.Type) mir.Type {
	if nt, ok := t.(*ir.NamedType); ok && nt.Name == "Channel" && len(nt.Args) >= 1 {
		return nt.Args[0]
	}
	return nil
}

// chanRecvSuffix maps an element LLVM type to the `_<suffix>` used by
// the chan_recv runtime. Delegates to the Osty-owned policy so the
// scalar/composite split stays in lockstep with `llvmChanRecv`.
func chanRecvSuffix(elemLLVM string) string {
	return llvmChanElementSuffix(elemLLVM)
}

// emitTaskIntrinsic dispatches structured-task ops to runtime symbols.
// `taskGroup(body)` and `spawn(body)` take a closure env ptr as
// argument; `g.spawn(body)` is the 2-arg form `[group, body]`.
func (g *mirGen) emitTaskIntrinsic(i *mir.IntrinsicInstr) error {
	switch i.Kind {
	case mir.IntrinsicTaskGroup:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "task_group arity")
		}
		arg, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		retLLVM := "void"
		if i.Dest != nil {
			if dl := g.fn.Local(i.Dest.Local); dl != nil && !isUnitType(dl.Type) {
				retLLVM = g.llvmType(dl.Type)
			}
		}
		sym := "osty_rt_task_group"
		g.declareRuntime(sym, "declare "+retLLVM+" @"+sym+"(ptr)")
		return g.emitSimpleCall(i, sym, retLLVM, []string{"ptr " + arg})
	case mir.IntrinsicSpawn:
		// One-arg form: detached spawn(body). Two-arg form:
		// `g.spawn(body)` — group-scoped. Both return a Handle ptr.
		if len(i.Args) != 1 && len(i.Args) != 2 {
			return unsupported("mir-mvp", "spawn arity")
		}
		operandArgs := make([]string, 0, len(i.Args))
		for _, op := range i.Args {
			v, err := g.evalOperand(op, op.Type())
			if err != nil {
				return err
			}
			operandArgs = append(operandArgs, "ptr "+v)
		}
		sym := "osty_rt_task_spawn"
		sig := "declare ptr @" + sym + "(ptr)"
		if len(i.Args) == 2 {
			sym = "osty_rt_task_group_spawn"
			sig = "declare ptr @" + sym + "(ptr, ptr)"
		}
		g.declareRuntime(sym, sig)
		return g.emitSimpleCall(i, sym, "ptr", operandArgs)
	case mir.IntrinsicHandleJoin:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "handle_join arity")
		}
		handle, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		// Handle's T is the value the handle will yield; the runtime
		// returns a ptr-wide slot the caller narrows.
		retLLVM := "ptr"
		if i.Dest != nil {
			if dl := g.fn.Local(i.Dest.Local); dl != nil && !isUnitType(dl.Type) {
				retLLVM = g.llvmType(dl.Type)
			}
		}
		sym := "osty_rt_task_handle_join"
		g.declareRuntime(sym, "declare "+retLLVM+" @"+sym+"(ptr)")
		return g.emitSimpleCall(i, sym, retLLVM, []string{"ptr " + handle})
	case mir.IntrinsicGroupCancel:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "group_cancel arity")
		}
		g.evalOperand(i.Args[0], i.Args[0].Type())
		return g.emitRuntimeVoidCall(i, "osty_rt_task_group_cancel", "ptr")
	case mir.IntrinsicGroupIsCancelled:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "group_is_cancelled arity")
		}
		group, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_task_group_is_cancelled"
		g.declareRuntime(sym, "declare i1 @"+sym+"(ptr)")
		return g.emitSimpleCall(i, sym, "i1", []string{"ptr " + group})
	}
	return unsupported("mir-mvp", fmt.Sprintf("task intrinsic kind %d", i.Kind))
}

// emitSelectIntrinsic handles the select DSL: the outer `select(body)`
// call and the arm registrations (recv / send / timeout / default).
// The arm runtime calls take the select builder as first arg.
func (g *mirGen) emitSelectIntrinsic(i *mir.IntrinsicInstr) error {
	switch i.Kind {
	case mir.IntrinsicSelect:
		return g.emitSimpleConcurrencyCall(i, "osty_rt_select", "void", []mir.Type{nil})
	case mir.IntrinsicSelectRecv:
		return g.emitSimpleConcurrencyCall(i, "osty_rt_select_recv", "void", nil)
	case mir.IntrinsicSelectSend:
		return g.emitSelectSend(i)
	case mir.IntrinsicSelectTimeout:
		return g.emitSimpleConcurrencyCall(i, "osty_rt_select_timeout", "void", nil)
	case mir.IntrinsicSelectDefault:
		return g.emitSimpleConcurrencyCall(i, "osty_rt_select_default", "void", nil)
	}
	return unsupported("mir-mvp", fmt.Sprintf("select intrinsic kind %d", i.Kind))
}

// emitSelectSend lowers `s.send(ch, value, arm)` — a send arm on a
// select builder. Args are [select_builder, channel, value, arm_env];
// the channel element type determines which typed runtime variant we
// dispatch to, mirroring the plain `IntrinsicChanSend` lane (scalar
// i64/i1/f64/ptr go straight, composites spill through the bytes_v1
// surface). The arm closure is invoked after the enqueue succeeds —
// the moral equivalent of `case ch <- v: arm()` in Go select.
func (g *mirGen) emitSelectSend(i *mir.IntrinsicInstr) error {
	if len(i.Args) != 4 {
		return unsupported("mir-mvp", fmt.Sprintf("select_send arity: got %d args", len(i.Args)))
	}
	builderOp := i.Args[0]
	chOp := i.Args[1]
	valOp := i.Args[2]
	armOp := i.Args[3]
	builderReg, err := g.evalOperand(builderOp, builderOp.Type())
	if err != nil {
		return err
	}
	chReg, err := g.evalOperand(chOp, chOp.Type())
	if err != nil {
		return err
	}
	elemT := channelElementType(chOp.Type())
	if elemT == nil {
		return unsupported("mir-mvp", "select_send: missing element type")
	}
	valReg, err := g.evalOperand(valOp, elemT)
	if err != nil {
		return err
	}
	armReg, err := g.evalOperand(armOp, armOp.Type())
	if err != nil {
		return err
	}
	elemLLVM := g.llvmType(elemT)
	if listUsesTypedRuntime(elemLLVM) {
		suffix := llvmListElementSuffix(elemLLVM)
		sym := "osty_rt_select_send_" + suffix
		g.declareRuntime(sym, "declare void @"+sym+"(ptr, ptr, "+elemLLVM+", ptr)")
		g.fnBuf.WriteString("  call void @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(builderReg)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(chReg)
		g.fnBuf.WriteString(", ")
		g.fnBuf.WriteString(elemLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(valReg)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(armReg)
		g.fnBuf.WriteString(")\n")
		return nil
	}
	// Composite element — bytes_v1 route. Spill value to a stack slot,
	// hand the runtime a (ptr, size) pair; it copies into GC storage.
	sym := "osty_rt_select_send_bytes_v1"
	g.declareRuntime(sym, "declare void @"+sym+"(ptr, ptr, ptr, i64, ptr)")
	em := g.ostyEmitter()
	slot := llvmSpillToSlot(em, &LlvmValue{typ: elemLLVM, name: valReg})
	size := llvmSizeOf(em, elemLLVM)
	g.flushOstyEmitter(em)
	g.fnBuf.WriteString("  call void @")
	g.fnBuf.WriteString(sym)
	g.fnBuf.WriteString("(ptr ")
	g.fnBuf.WriteString(builderReg)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(chReg)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(slot.name)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(size.name)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(armReg)
	g.fnBuf.WriteString(")\n")
	return nil
}

// emitCancelIntrinsic handles the cancellation + yield + sleep
// helpers. None take a receiver; they operate on the implicit
// current task.
func (g *mirGen) emitCancelIntrinsic(i *mir.IntrinsicInstr) error {
	switch i.Kind {
	case mir.IntrinsicIsCancelled:
		sym := "osty_rt_cancel_is_cancelled"
		g.declareRuntime(sym, "declare i1 @"+sym+"()")
		return g.emitSimpleCall(i, sym, "i1", nil)
	case mir.IntrinsicCheckCancelled:
		// Returns Result<(), Error> — runtime uses the `{i64, i64}`
		// enum layout like chan_recv.
		sym := "osty_rt_cancel_check_cancelled"
		g.declareRuntime(sym, "declare { i64, i64 } @"+sym+"()")
		if i.Dest == nil {
			g.fnBuf.WriteString("  call { i64, i64 } @")
			g.fnBuf.WriteString(sym)
			g.fnBuf.WriteString("()\n")
			return nil
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = call { i64, i64 } @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteString("()\n")
		g.fnBuf.WriteString("  store { i64, i64 } ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
		g.fnBuf.WriteByte('\n')
		return nil
	case mir.IntrinsicYield:
		sym := "osty_rt_thread_yield"
		g.declareRuntime(sym, "declare void @"+sym+"()")
		g.fnBuf.WriteString("  call void @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteString("()\n")
		return nil
	case mir.IntrinsicSleep:
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "sleep arity")
		}
		dur, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_thread_sleep"
		g.declareRuntime(sym, "declare void @"+sym+"(ptr)")
		g.fnBuf.WriteString("  call void @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(dur)
		g.fnBuf.WriteString(")\n")
		return nil
	}
	return unsupported("mir-mvp", fmt.Sprintf("cancel intrinsic kind %d", i.Kind))
}

// emitConcurrencyHelperIntrinsic handles `parallel` / `race` /
// `collectAll` via their runtime entry points.
func (g *mirGen) emitConcurrencyHelperIntrinsic(i *mir.IntrinsicInstr) error {
	switch i.Kind {
	case mir.IntrinsicParallel:
		// Args: [items, concurrency, f]. Returns List<Result<R, Error>>
		// which the runtime reports as an opaque ptr.
		if len(i.Args) != 3 {
			return unsupported("mir-mvp", "parallel arity")
		}
		items, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		conc, err := g.evalOperand(i.Args[1], mir.TInt)
		if err != nil {
			return err
		}
		f, err := g.evalOperand(i.Args[2], i.Args[2].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_parallel"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, i64, ptr)")
		return g.emitSimpleCall(i, sym, "ptr", []string{"ptr " + items, "i64 " + conc, "ptr " + f})
	case mir.IntrinsicRace:
		// Returns Result<T, Error> — runtime uses the `{i64, i64}` enum
		// layout matching chan_recv / check_cancelled.
		if len(i.Args) != 1 {
			return unsupported("mir-mvp", "race arity")
		}
		body, err := g.evalOperand(i.Args[0], i.Args[0].Type())
		if err != nil {
			return err
		}
		sym := "osty_rt_task_race"
		g.declareRuntime(sym, "declare { i64, i64 } @"+sym+"(ptr)")
		if i.Dest == nil {
			g.fnBuf.WriteString("  call { i64, i64 } @")
			g.fnBuf.WriteString(sym)
			g.fnBuf.WriteString("(ptr ")
			g.fnBuf.WriteString(body)
			g.fnBuf.WriteString(")\n")
			return nil
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = call { i64, i64 } @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(body)
		g.fnBuf.WriteString(")\n")
		g.fnBuf.WriteString("  store { i64, i64 } ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
		g.fnBuf.WriteByte('\n')
		return nil
	case mir.IntrinsicCollectAll:
		return g.emitSimpleConcurrencyCall(i, "osty_rt_task_collect_all", "ptr", nil)
	}
	return unsupported("mir-mvp", fmt.Sprintf("concurrency helper kind %d", i.Kind))
}

// emitSimpleConcurrencyCall is a concise path for concurrency runtime
// calls that take a fixed list of ptr-typed args (one per MIR arg) and
// return the given LLVM type. If `expected` is non-nil its length
// must match `len(i.Args)`.
func (g *mirGen) emitSimpleConcurrencyCall(i *mir.IntrinsicInstr, sym, retLLVM string, expected []mir.Type) error {
	if expected != nil && len(expected) != len(i.Args) {
		return unsupported("mir-mvp", fmt.Sprintf("%s arity: got %d args", sym, len(i.Args)))
	}
	argParts := make([]string, 0, len(i.Args))
	for _, op := range i.Args {
		v, err := g.evalOperand(op, op.Type())
		if err != nil {
			return err
		}
		argParts = append(argParts, "ptr "+v)
	}
	paramParts := make([]string, len(i.Args))
	for idx := range paramParts {
		paramParts[idx] = "ptr"
	}
	g.declareRuntime(sym, "declare "+retLLVM+" @"+sym+"("+strings.Join(paramParts, ", ")+")")
	return g.emitSimpleCall(i, sym, retLLVM, argParts)
}

// emitRuntimeVoidCall emits `call void @<sym>(ptr %arg0)` using the
// already-evaluated operand chain in i.Args.
func (g *mirGen) emitRuntimeVoidCall(i *mir.IntrinsicInstr, sym, argLLVM string) error {
	val, err := g.evalOperand(i.Args[0], i.Args[0].Type())
	if err != nil {
		return err
	}
	g.declareRuntime(sym, "declare void @"+sym+"("+argLLVM+")")
	g.fnBuf.WriteString("  call void @")
	g.fnBuf.WriteString(sym)
	g.fnBuf.WriteByte('(')
	g.fnBuf.WriteString(argLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(val)
	g.fnBuf.WriteString(")\n")
	return nil
}

// emitSimpleCall emits a `call <ret> @<sym>(<args>)` instruction and
// handles the `dest` slot (store into alloca) if present.
func (g *mirGen) emitSimpleCall(i *mir.IntrinsicInstr, sym, retLLVM string, args []string) error {
	joined := strings.Join(args, ", ")
	if i.Dest == nil || retLLVM == "void" {
		g.fnBuf.WriteString("  call ")
		g.fnBuf.WriteString(retLLVM)
		g.fnBuf.WriteString(" @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteByte('(')
		g.fnBuf.WriteString(joined)
		g.fnBuf.WriteString(")\n")
		return nil
	}
	destLoc := g.fn.Local(i.Dest.Local)
	tmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(" = call ")
	g.fnBuf.WriteString(retLLVM)
	g.fnBuf.WriteString(" @")
	g.fnBuf.WriteString(sym)
	g.fnBuf.WriteByte('(')
	g.fnBuf.WriteString(joined)
	g.fnBuf.WriteString(")\n")
	if destLoc == nil {
		return nil
	}
	// Coerce to dest slot type if needed.
	destLLVM := g.llvmType(destLoc.Type)
	stored := tmp
	if destLLVM != retLLVM {
		// Most commonly this is widening a runtime i1 into a 1-bit slot
		// (same width), or a ptr into an i64 etc. — keep conservative.
		if widened, err := g.coerceValue(tmp, retLLVM, destLLVM); err == nil {
			stored = widened
		}
	}
	g.fnBuf.WriteString("  store ")
	g.fnBuf.WriteString(destLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(stored)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
	g.fnBuf.WriteByte('\n')
	return nil
}

// coerceValue applies a cheap cast between two LLVM scalar types. Used
// when a runtime call returns a narrower/wider type than the MIR
// destination's declared type. The set of conversions handled here is
// deliberately narrow — anything unusual returns an error so we hit a
// fall-back rather than emit invalid IR.
func (g *mirGen) coerceValue(val, from, to string) (string, error) {
	if from == to {
		return val, nil
	}
	if from == "i1" && intLLVMBits(to) > 1 {
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = zext i1 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to ")
		g.fnBuf.WriteString(to)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	}
	return val, nil
}

func mapKeyValueTypes(t mir.Type) (mir.Type, mir.Type) {
	if nt, ok := t.(*ir.NamedType); ok && nt.Name == "Map" {
		if len(nt.Args) >= 2 {
			return nt.Args[0], nt.Args[1]
		}
	}
	return nil, nil
}

func setElemType(t mir.Type) mir.Type {
	if nt, ok := t.(*ir.NamedType); ok && nt.Name == "Set" && len(nt.Args) > 0 {
		return nt.Args[0]
	}
	return nil
}

func isStringLLVMType(t mir.Type) bool {
	if p, ok := t.(*ir.PrimType); ok {
		return p.Kind == ir.PrimString
	}
	return false
}

func isBytesType(t mir.Type) bool {
	if p, ok := t.(*ir.PrimType); ok {
		return p.Kind == ir.PrimBytes
	}
	return false
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
		// Loop back-edges — a goto whose target is at or before the
		// current block in CFG order — advance the throttled loop
		// safepoint counter. This matches the legacy emitter's
		// loop-header polling convention while avoiding a full runtime
		// call on every tight-loop iteration. Forward gotos (if/else
		// joins, fall-throughs) do not need extra polls because the
		// function already took one at entry.
		//
		// v0.6 A5: `#[vectorize]` functions opt out of per-iteration
		// polls — the vectorizer cannot analyse a latch with a
		// side-effecting call, and the entry safepoint + post-return
		// safepoint in the caller bracket the window. See §3.8.3.
		isBackedge := x.Target <= g.curBlockID
		if g.opts.EmitGC && isBackedge && !g.vectorizeHint {
			g.emitLoopSafepointKind()
		}
		g.fnBuf.WriteString("  br label %")
		g.fnBuf.WriteString(g.blockLabels[x.Target])
		if isBackedge && g.loopHintsActive() {
			if ref := g.nextLoopMD(); ref != "" {
				g.fnBuf.WriteString(", !llvm.loop ")
				g.fnBuf.WriteString(ref)
			}
		}
		g.fnBuf.WriteByte('\n')
	case *mir.BranchTerm:
		// Same throttled back-edge rule applied to conditional
		// branches — covers `while cond { ... }` style loops where
		// the cond block has a conditional branch back to its own
		// body.
		isBackedge := x.Then <= g.curBlockID || x.Else <= g.curBlockID
		// v0.6 A5 — same vectorize-hint opt-out as the GotoTerm case.
		if g.opts.EmitGC && isBackedge && !g.vectorizeHint {
			g.emitLoopSafepointKind()
		}
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
		if isBackedge && g.loopHintsActive() {
			if ref := g.nextLoopMD(); ref != "" {
				g.fnBuf.WriteString(", !llvm.loop ")
				g.fnBuf.WriteString(ref)
			}
		}
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
		if g.fn != nil && g.fn.Name == "main" && len(g.fn.Params) == 0 && isUnitType(g.fn.ReturnType) {
			g.emitGCReleaseRoots()
			g.fnBuf.WriteString("  ret i32 0\n")
			return nil
		}
		if g.fn.ReturnType == nil || isUnitType(g.fn.ReturnType) {
			// Explicit safepoint roots need no function-exit cleanup, but
			// keep the hook in place so return lowering stays uniform if we
			// grow per-function GC epilog work later.
			g.emitGCReleaseRoots()
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
		// Keep the legacy hook between load and ret for symmetry with the
		// unit-return path; explicit safepoint roots mean this is currently
		// a no-op.
		g.emitGCReleaseRoots()
		g.fnBuf.WriteString("  ret ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteByte('\n')
	case *mir.UnreachableTerm:
		g.emitGCReleaseRoots()
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
	case *mir.DiscriminantRV:
		return g.emitDiscriminantRV(r)
	case *mir.LenRV:
		return g.emitLenRV(r)
	case *mir.NullaryRV:
		return g.emitNullaryRV(r, hintT)
	case *mir.CastRV:
		return g.emitCastRV(r)
	case *mir.GlobalRefRV:
		// Load the declared type from the module-level `@<name>`
		// global. The ctor initialises it before user code runs, so
		// the value is always valid when this load executes.
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = load ")
		g.fnBuf.WriteString(g.llvmType(r.T))
		g.fnBuf.WriteString(", ptr @")
		g.fnBuf.WriteString(r.Name)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("rvalue %T", rv))
}

// emitDiscriminantRV reads the i64 discriminant (field 0) from an
// enum-shaped place. The place's type determines the aggregate's
// LLVM type name.
func (g *mirGen) emitDiscriminantRV(rv *mir.DiscriminantRV) (string, error) {
	baseT := g.localType(rv.Place.Local)
	// If the place has projections, follow them into the actual
	// aggregate type; but for Stage 3.2 we only support a bare local
	// scrutinee, since that is what ir.lowerMatch / lowerIfLet emit.
	if rv.Place.HasProjections() {
		return "", unsupported("mir-mvp", "discriminant through projections")
	}
	slot := g.localSlots[rv.Place.Local]
	if slot == "" {
		return "", fmt.Errorf("mir-mvp: discriminant from unknown local %d", rv.Place.Local)
	}
	agg := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(agg)
	g.fnBuf.WriteString(" = load ")
	g.fnBuf.WriteString(g.llvmType(baseT))
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteByte('\n')
	tmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(" = extractvalue ")
	g.fnBuf.WriteString(g.llvmType(baseT))
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(agg)
	g.fnBuf.WriteString(", 0\n")
	return tmp, nil
}

// emitLenRV lowers MIR's direct length query on a place to the same
// runtime entrypoints used by the intrinsic path. This is the shape
// lowerForIn emits for list iteration, so supporting it keeps those
// loops on the MIR path instead of forcing a legacy fallback.
func (g *mirGen) emitLenRV(rv *mir.LenRV) (string, error) {
	placeT := g.placeType(rv.Place)
	if placeT == nil {
		return "", unsupported("mir-mvp", "LenRV place type is unknown")
	}
	valueReg, err := g.emitLoad(rv.Place, placeT)
	if err != nil {
		return "", err
	}
	em := g.ostyEmitter()
	switch {
	case isListPtrType(placeT):
		sym := listRuntimeLenSymbol()
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		result := llvmListLen(em, &LlvmValue{typ: "ptr", name: valueReg})
		g.flushOstyEmitter(em)
		return result.name, nil
	case isMapPtrType(placeT):
		sym := mapRuntimeLenSymbol()
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		result := llvmMapLen(em, &LlvmValue{typ: "ptr", name: valueReg})
		g.flushOstyEmitter(em)
		return result.name, nil
	case isSetPtrType(placeT):
		sym := setRuntimeLenSymbol()
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		result := llvmSetLen(em, &LlvmValue{typ: "ptr", name: valueReg})
		g.flushOstyEmitter(em)
		return result.name, nil
	case isStringLLVMType(placeT):
		sym := llvmStringRuntimeByteLenSymbol()
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		result := llvmStringByteLen(em, &LlvmValue{typ: "ptr", name: valueReg})
		g.flushOstyEmitter(em)
		return result.name, nil
	case isBytesType(placeT):
		sym := "osty_rt_bytes_len"
		g.declareRuntime(sym, "declare i64 @"+sym+"(ptr)")
		result := llvmCall(em, "i64", sym, []*LlvmValue{{typ: "ptr", name: valueReg}})
		g.flushOstyEmitter(em)
		return result.name, nil
	default:
		return "", unsupported("mir-mvp", "LenRV on unsupported type "+mirTypeString(placeT))
	}
}

// emitNullaryRV materialises nullary values — currently only
// NullaryNone for optional/maybe. We emit `{ 0, 0 }` of the target's
// LLVM type.
func (g *mirGen) emitNullaryRV(rv *mir.NullaryRV, hintT mir.Type) (string, error) {
	if rv.Kind != mir.NullaryNone {
		return "", unsupported("mir-mvp", fmt.Sprintf("nullary rvalue kind %d", rv.Kind))
	}
	target := rv.T
	if target == nil {
		target = hintT
	}
	if target == nil {
		return "", unsupported("mir-mvp", "nullary None with unknown target type")
	}
	aggLLVM := g.llvmType(target)
	step1 := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(step1)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(aggLLVM)
	g.fnBuf.WriteString(" undef, i64 0, 0\n")
	step2 := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(step2)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(aggLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(step1)
	g.fnBuf.WriteString(", i64 0, 1\n")
	return step2, nil
}

// emitCastRV handles numeric resizing, int↔float conversions, and
// optional wrap/unwrap (which is a no-op at the enum-layout level:
// wrap means "insert as Some-tagged value" but MIR emits a separate
// aggregate in practice). For now we support IntResize,
// IntToFloat, FloatToInt, FloatResize, Bitcast; OptionalWrap/Unwrap
// pass through as identity reads since the MIR lowerer already emits
// the necessary aggregate construction.
func (g *mirGen) emitCastRV(rv *mir.CastRV) (string, error) {
	arg, err := g.evalOperand(rv.Arg, rv.From)
	if err != nil {
		return "", err
	}
	fromLLVM := g.llvmType(rv.From)
	toLLVM := g.llvmType(rv.To)
	switch rv.Kind {
	case mir.CastIntResize:
		return g.emitIntResize(arg, fromLLVM, toLLVM)
	case mir.CastIntToFloat:
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = sitofp ")
		g.fnBuf.WriteString(fromLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(arg)
		g.fnBuf.WriteString(" to ")
		g.fnBuf.WriteString(toLLVM)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	case mir.CastFloatToInt:
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = fptosi ")
		g.fnBuf.WriteString(fromLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(arg)
		g.fnBuf.WriteString(" to ")
		g.fnBuf.WriteString(toLLVM)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	case mir.CastFloatResize:
		op := "fpext"
		if toLLVM == "float" && fromLLVM == "double" {
			op = "fptrunc"
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = ")
		g.fnBuf.WriteString(op)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(fromLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(arg)
		g.fnBuf.WriteString(" to ")
		g.fnBuf.WriteString(toLLVM)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	case mir.CastBitcast:
		if fromLLVM == toLLVM {
			return arg, nil
		}
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = bitcast ")
		g.fnBuf.WriteString(fromLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(arg)
		g.fnBuf.WriteString(" to ")
		g.fnBuf.WriteString(toLLVM)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	case mir.CastOptionalWrap, mir.CastOptionalUnwrap:
		// Identity at this stage — the MIR lowerer normally materialises
		// wrap via AggregateRV{EnumVariant} and unwrap via VariantProj,
		// so a bare CastRV with these kinds is a passthrough.
		return arg, nil
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("cast kind %d", rv.Kind))
}

func (g *mirGen) emitIntResize(arg, fromLLVM, toLLVM string) (string, error) {
	if fromLLVM == toLLVM {
		return arg, nil
	}
	fromBits := intLLVMBits(fromLLVM)
	toBits := intLLVMBits(toLLVM)
	if fromBits == 0 || toBits == 0 {
		return "", unsupported("mir-mvp", "int-resize between "+fromLLVM+" and "+toLLVM)
	}
	op := "sext"
	if toBits < fromBits {
		op = "trunc"
	}
	tmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(" = ")
	g.fnBuf.WriteString(op)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(fromLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(arg)
	g.fnBuf.WriteString(" to ")
	g.fnBuf.WriteString(toLLVM)
	g.fnBuf.WriteByte('\n')
	return tmp, nil
}

func intLLVMBits(s string) int {
	switch s {
	case "i1":
		return 1
	case "i8":
		return 8
	case "i16":
		return 16
	case "i32":
		return 32
	case "i64":
		return 64
	}
	return 0
}

// emitAggregate builds an LLVM aggregate value for a struct / tuple /
// enum / list via an `insertvalue` chain (for struct / tuple / enum)
// or a runtime push chain (for list). Each iteration inserts one
// field into the running aggregate. Starting from `undef` keeps the
// chain dependency-free — LLVM's SROA + mem2reg collapses the undef
// chain into direct reads during opt.
func (g *mirGen) emitAggregate(rv *mir.AggregateRV, hintT mir.Type) (string, error) {
	aggT := rv.T
	if aggT == nil {
		aggT = hintT
	}
	if aggT == nil {
		return "", unsupported("mir-mvp", "aggregate with unknown type")
	}
	switch rv.Kind {
	case mir.AggEnumVariant:
		return g.emitEnumVariant(rv, aggT)
	case mir.AggList:
		return g.emitListLiteral(rv, aggT)
	case mir.AggClosure:
		return g.emitClosureEnv(rv)
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

// emitEnumVariant builds `{ i64 disc, i64 payload }` for an enum or
// optional value. Scalar payloads are zext/sext/bitcast into the i64
// slot. Pointer payloads use ptrtoint. Bare variants (no payload)
// fill the slot with zero.
func (g *mirGen) emitEnumVariant(rv *mir.AggregateRV, aggT mir.Type) (string, error) {
	aggLLVM := g.llvmType(aggT)
	// Step 1: insert discriminant.
	disc := strconv.Itoa(rv.VariantIdx)
	tmp1 := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp1)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(aggLLVM)
	g.fnBuf.WriteString(" undef, i64 ")
	g.fnBuf.WriteString(disc)
	g.fnBuf.WriteString(", 0\n")

	// Step 2: insert payload. For single scalar payloads the value
	// lives directly in the i64 slot. For no-payload variants we
	// leave the slot at zero (still emit insert so LLVM's definedness
	// analysis doesn't cling to undef).
	var payloadI64 string
	if len(rv.Fields) == 0 {
		payloadI64 = "0"
	} else if len(rv.Fields) == 1 {
		arg := rv.Fields[0]
		argT := arg.Type()
		val, err := g.evalOperand(arg, argT)
		if err != nil {
			return "", err
		}
		payloadI64, err = g.toI64Slot(val, argT)
		if err != nil {
			return "", err
		}
	} else {
		return "", unsupported("mir-mvp", fmt.Sprintf("enum variant with %d payload fields (only scalar / 0-arity supported)", len(rv.Fields)))
	}
	tmp2 := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp2)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(aggLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(tmp1)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(payloadI64)
	g.fnBuf.WriteString(", 1\n")
	return tmp2, nil
}

// toI64Slot converts an LLVM value of type `t` into the i64 payload
// slot used by the enum layout. It emits the narrowest widening that
// works: zext / sext for integers, bitcast for doubles, ptrtoint for
// pointers. Returns the register holding the i64 value.
func (g *mirGen) toI64Slot(val string, t mir.Type) (string, error) {
	llvmT := g.llvmType(t)
	switch llvmT {
	case "i64":
		return val, nil
	case "i1":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = zext i1 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to i64\n")
		return tmp, nil
	case "i8", "i16", "i32":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = sext ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to i64\n")
		return tmp, nil
	case "double":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = bitcast double ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to i64\n")
		return tmp, nil
	case "float":
		// widen to double, then bitcast
		widened := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(widened)
		g.fnBuf.WriteString(" = fpext float ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to double\n")
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = bitcast double ")
		g.fnBuf.WriteString(widened)
		g.fnBuf.WriteString(" to i64\n")
		return tmp, nil
	case "ptr":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = ptrtoint ptr ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to i64\n")
		return tmp, nil
	case "void":
		return "0", nil
	}
	return "", unsupported("mir-mvp", "enum payload: cannot widen "+llvmT+" to i64")
}

// fromI64Slot reverses toI64Slot: the enum payload slot is an i64 and
// we want it as its declared type. Used by VariantProj reads.
func (g *mirGen) fromI64Slot(val string, targetT mir.Type) (string, error) {
	llvmT := g.llvmType(targetT)
	switch llvmT {
	case "i64":
		return val, nil
	case "i1":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = trunc i64 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to i1\n")
		return tmp, nil
	case "i8", "i16", "i32":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = trunc i64 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
	case "double":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = bitcast i64 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to double\n")
		return tmp, nil
	case "float":
		widened := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(widened)
		g.fnBuf.WriteString(" = bitcast i64 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to double\n")
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = fptrunc double ")
		g.fnBuf.WriteString(widened)
		g.fnBuf.WriteString(" to float\n")
		return tmp, nil
	case "ptr":
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = inttoptr i64 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to ptr\n")
		return tmp, nil
	case "void":
		return "undef", nil
	}
	return "", unsupported("mir-mvp", "variant payload read: cannot narrow i64 to "+llvmT)
}

// emitListLiteral builds a `List<T>` from its element operands via
// the runtime ABI: `osty_rt_list_new()` + a chain of typed pushes.
// Returns the pointer register holding the new list.
// emitClosureEnv boxes a closure into a heap-shaped env struct. The
// MIR lowerer emits `AggregateRV{AggClosure, Fields: [FnConst, caps...]}`
// and expects the lifted function to receive the env ptr as its first
// argument. We allocate the env with a stack alloca (a conservative
// choice — closures that escape the enclosing frame currently need
// heap alloc; see the Stage 3.8 note in docs/mir_design.md) and fill
// in the fn pointer at slot 0 plus captures at slots 1..N.
//
// The env struct type is a synthesised `%ClosureEnv.<signature>`
// declared at module level; multiple closures with the same env
// layout share one type def.
func (g *mirGen) emitClosureEnv(rv *mir.AggregateRV) (string, error) {
	if len(rv.Fields) < 1 {
		return "", unsupported("mir-mvp", "empty closure aggregate")
	}
	// Collect element types: [ptr, cap0, cap1, ...]. The first field
	// is the FnConst — we treat its LLVM type as ptr regardless of
	// what the MIR type claims.
	elemTs := make([]mir.Type, 0, len(rv.Fields))
	elemTs = append(elemTs, mirClosureEnvType()) // slot 0 always ptr
	for i := 1; i < len(rv.Fields); i++ {
		elemTs = append(elemTs, rv.Fields[i].Type())
	}
	envName := g.closureEnvTypeName(elemTs)
	// Alloca the env.
	slot := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteString(" = alloca %")
	g.fnBuf.WriteString(envName)
	g.fnBuf.WriteByte('\n')
	// Store each field. Slot 0 is the fn ptr; slots 1..N are captures.
	for i, op := range rv.Fields {
		var val string
		var err error
		if i == 0 {
			// The first field is ALWAYS the lifted closure fn
			// pointer. Write the bare `@symbol` — the lifted fn
			// already takes env as its first param, no thunk needed.
			val, err = g.evalClosureFnConst(op)
		} else {
			val, err = g.evalOperand(op, elemTs[i])
		}
		if err != nil {
			return "", err
		}
		elemLLVM := g.llvmType(elemTs[i])
		gep := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(gep)
		g.fnBuf.WriteString(" = getelementptr %")
		g.fnBuf.WriteString(envName)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(slot)
		g.fnBuf.WriteString(", i32 0, i32 ")
		g.fnBuf.WriteString(strconv.Itoa(i))
		g.fnBuf.WriteByte('\n')
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(elemLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(gep)
		g.fnBuf.WriteByte('\n')
	}
	return slot, nil
}

// closureEnvTypeName registers a closure-env struct by element types
// and returns its mangled name (e.g. `ClosureEnv.ptr.i64.ptr`). The
// deterministic name makes cross-function sharing possible when two
// closures happen to have the same env layout.
func (g *mirGen) closureEnvTypeName(elems []mir.Type) string {
	parts := make([]string, len(elems))
	for i, t := range elems {
		parts[i] = g.llvmTypeForTupleTag(t)
	}
	name := "ClosureEnv." + strings.Join(parts, ".")
	if _, ok := g.tupleDefs[name]; !ok {
		g.tupleDefs[name] = append([]mir.Type(nil), elems...)
		g.tupleOrder = append(g.tupleOrder, name)
	}
	return name
}

func (g *mirGen) emitListLiteral(rv *mir.AggregateRV, aggT mir.Type) (string, error) {
	elemT := listElemType(aggT)
	if elemT == nil {
		return "", unsupported("mir-mvp", "list literal: missing element type")
	}
	g.declareRuntime(listRuntimeNewSymbol(), "declare ptr @"+listRuntimeNewSymbol()+"()")
	em := g.ostyEmitter()
	listVal := llvmListNew(em)
	g.flushOstyEmitter(em)
	for _, f := range rv.Fields {
		if err := g.emitListPushOperand(listVal.name, f, elemT); err != nil {
			return "", err
		}
	}
	return listVal.name, nil
}

// emitListPushOperand pushes one value onto a list by runtime ABI
// convention — typed push for scalar element types, bytes fallback
// for composite ones.
func (g *mirGen) emitListPushOperand(listReg string, op mir.Operand, elemT mir.Type) error {
	val, err := g.evalOperand(op, elemT)
	if err != nil {
		return err
	}
	elemLLVM := g.llvmType(elemT)
	if listUsesTypedRuntime(elemLLVM) {
		sym := listRuntimePushSymbol(elemLLVM)
		g.declareRuntime(sym, "declare void @"+sym+"(ptr, "+elemLLVM+")")
		em := g.ostyEmitter()
		llvmListPush(em,
			&LlvmValue{typ: "ptr", name: listReg, pointer: false},
			&LlvmValue{typ: elemLLVM, name: val, pointer: false})
		g.flushOstyEmitter(em)
		return nil
	}
	// Composite element (struct / tuple) — stage into a stack slot,
	// compute the size via the `getelementptr null, 1` idiom, and
	// call the bytes helper.
	sym := listRuntimePushBytesV1Symbol()
	g.declareRuntime(sym, "declare void @"+sym+"(ptr, ptr, i64)")
	em := g.ostyEmitter()
	slot := llvmSpillToSlot(em, &LlvmValue{typ: elemLLVM, name: val})
	size := llvmSizeOf(em, elemLLVM)
	llvmCallVoid(em, sym, []*LlvmValue{
		{typ: "ptr", name: listReg},
		slot,
		size,
	})
	g.flushOstyEmitter(em)
	return nil
}

// emitListGetBytes loads a composite list element via the bytes-v1
// runtime helper. Allocates a stack slot sized to the element type,
// asks the runtime to write into it, loads back, and stores into
// the intrinsic's optional destination.
func (g *mirGen) emitListGetBytes(i *mir.IntrinsicInstr, listReg, idxReg string, elemT mir.Type) error {
	elemLLVM := g.llvmType(elemT)
	sym := listRuntimeGetBytesV1Symbol()
	g.declareRuntime(sym, "declare void @"+sym+"(ptr, i64, ptr, i64)")
	em := g.ostyEmitter()
	slot := llvmAllocaSlot(em, elemLLVM)
	size := llvmSizeOf(em, elemLLVM)
	llvmCallVoid(em, sym, []*LlvmValue{
		{typ: "ptr", name: listReg},
		{typ: "i64", name: idxReg},
		slot,
		size,
	})
	if i.Dest == nil {
		g.flushOstyEmitter(em)
		return nil
	}
	destLoc := g.fn.Local(i.Dest.Local)
	if destLoc == nil {
		g.flushOstyEmitter(em)
		return fmt.Errorf("mir-mvp: list_get_bytes into unknown local %d", i.Dest.Local)
	}
	loaded := llvmLoadFromSlot(em, slot, elemLLVM)
	g.flushOstyEmitter(em)
	return g.storeIntrinsicResult(i, loaded)
}

// emitSizeOf returns a fresh register holding the size in bytes of
// the named LLVM type. Uses the standard `getelementptr null, 1`
// idiom — LLVM's constant folder collapses it to a numeric literal
// during opt. Works for any first-class type, including user
// structs referenced by their `%Name` handle. Thin shim over the
// Osty-owned `llvmSizeOf`.
func (g *mirGen) emitSizeOf(llvmType string) string {
	em := g.ostyEmitter()
	size := llvmSizeOf(em, llvmType)
	g.flushOstyEmitter(em)
	return size.name
}

func listElemType(t mir.Type) mir.Type {
	if nt, ok := t.(*ir.NamedType); ok && nt.Name == "List" && len(nt.Args) > 0 {
		return nt.Args[0]
	}
	return nil
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
		// Bare `FnConst` in value position (not a direct-call callee —
		// those use `FnRef` and never flow through evalOperand). Under
		// the uniform closure ABI every fn-typed VALUE must be an env
		// pointer, so we wrap the top-level fn with a thunk that
		// ignores env and delegates.
		if fc, ok := o.Const.(*mir.FnConst); ok {
			return g.emitFnValueWrapper(fc.Symbol, fc.T)
		}
		return g.renderConst(o.Const, o.T)
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("operand %T", op))
}

// emitFnValueWrapper materialises a closure env pointing at a
// top-level fn symbol. The env layout is `{ ptr thunk }` — no
// captures. The thunk has env-first-arg ABI and delegates to the
// real symbol, which keeps the caller's uniform IndirectCall path
// working for fn values that came from a plain fn ref rather than a
// `Closure` expression.
func (g *mirGen) emitFnValueWrapper(symbol string, fnT mir.Type) (string, error) {
	ft, _ := fnT.(*ir.FnType)
	if ft == nil {
		// Fallback: no signature known, can't generate a thunk.
		return "@" + symbol, nil
	}
	g.ensureThunk(symbol, ft)
	// Alloca a 1-field env, store the thunk ptr.
	envTypeName := g.closureEnvTypeName([]mir.Type{mirClosureEnvType()})
	envPtr := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(envPtr)
	g.fnBuf.WriteString(" = alloca %")
	g.fnBuf.WriteString(envTypeName)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString("  store ptr @")
	g.fnBuf.WriteString(thunkName(symbol))
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(envPtr)
	g.fnBuf.WriteByte('\n')
	return envPtr, nil
}

// ensureThunk generates (once per symbol) a thunk function with
// signature `ret (ptr env, user_params...) -> ret`. The body
// discards env and tail-calls the real symbol.
func (g *mirGen) ensureThunk(symbol string, fnT *ir.FnType) {
	if _, ok := g.thunkDefs[symbol]; ok {
		return
	}
	retLLVM := "void"
	if fnT.Return != nil && !isUnitType(fnT.Return) {
		retLLVM = g.llvmType(fnT.Return)
	}
	paramParts := make([]string, 0, len(fnT.Params))
	argParts := make([]string, 0, len(fnT.Params))
	for i, p := range fnT.Params {
		paramParts = append(paramParts, g.llvmType(p)+" %arg"+strconv.Itoa(i))
		argParts = append(argParts, g.llvmType(p)+" %arg"+strconv.Itoa(i))
	}
	headerParams := "ptr %env"
	if len(paramParts) > 0 {
		headerParams += ", " + strings.Join(paramParts, ", ")
	}
	var b strings.Builder
	b.WriteString("define private ")
	b.WriteString(retLLVM)
	b.WriteString(" @")
	b.WriteString(thunkName(symbol))
	b.WriteByte('(')
	b.WriteString(headerParams)
	b.WriteString(") {\n")
	b.WriteString("entry:\n")
	if retLLVM == "void" {
		b.WriteString("  call void @")
		b.WriteString(symbol)
		b.WriteByte('(')
		b.WriteString(strings.Join(argParts, ", "))
		b.WriteString(")\n")
		b.WriteString("  ret void\n")
	} else {
		b.WriteString("  %ret = call ")
		b.WriteString(retLLVM)
		b.WriteString(" @")
		b.WriteString(symbol)
		b.WriteByte('(')
		b.WriteString(strings.Join(argParts, ", "))
		b.WriteString(")\n")
		b.WriteString("  ret ")
		b.WriteString(retLLVM)
		b.WriteString(" %ret\n")
	}
	b.WriteString("}\n\n")
	g.thunkDefs[symbol] = b.String()
	g.thunkOrder = append(g.thunkOrder, symbol)
}

func thunkName(symbol string) string { return "__osty_closure_thunk_" + symbol }

// evalClosureFnConst reads the fn-pointer operand that lives in slot
// 0 of a closure aggregate. Unlike `evalOperand` for a ConstOp{
// FnConst}, this does NOT wrap in a thunk — the MIR lowerer puts
// lifted closure fns here, and those already take env as their first
// parameter. Plain LLVM `@symbol` is what goes into the env slot.
func (g *mirGen) evalClosureFnConst(op mir.Operand) (string, error) {
	if co, ok := op.(*mir.ConstOp); ok {
		if fc, ok := co.Const.(*mir.FnConst); ok {
			if fc.Symbol == "" {
				return "", unsupported("mir-mvp", "closure FnConst with empty symbol")
			}
			return "@" + fc.Symbol, nil
		}
	}
	// Fallback for unexpected shapes: use the ordinary evaluator.
	// Closures with already-wrapped fn ptrs still work, at the cost
	// of an extra thunk indirection.
	return g.evalOperand(op, op.Type())
}

// mirClosureEnvType returns the MIR type used for a closure env
// pointer. Kept in sync with the MIR lowerer's `closureEnvType` —
// both constructs name the same Builtin "ClosureEnv" which llvmType
// maps to `ptr`.
func mirClosureEnvType() mir.Type {
	return &ir.NamedType{Name: "ClosureEnv", Builtin: true}
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
	// curT tracks the MIR type of the running value so IndexProj can
	// tell whether its base is a List (list_get) or a Map
	// (map_get_or_abort). Each projection updates curT to the output
	// of that step.
	curT := baseT
	for i, proj := range place.Projections {
		// DerefProj rebinds the running value to the declared
		// aggregate loaded from the current ptr. Used by the MIR
		// closure lowerer to walk into the env struct: `DerefProj{
		// Type: envStructType}` replaces the ptr in curReg/curLLVM
		// with the struct value in those registers so the next
		// projection (TupleProj i+1) can extract a capture.
		if dp, ok := proj.(*mir.DerefProj); ok {
			innerT := dp.Type
			if innerT == nil {
				return "", unsupported("mir-mvp", "DerefProj without Type")
			}
			innerLLVM := g.llvmType(innerT)
			loaded := g.fresh()
			g.fnBuf.WriteString("  ")
			g.fnBuf.WriteString(loaded)
			g.fnBuf.WriteString(" = load ")
			g.fnBuf.WriteString(innerLLVM)
			g.fnBuf.WriteString(", ptr ")
			g.fnBuf.WriteString(curReg)
			g.fnBuf.WriteByte('\n')
			curReg = loaded
			curLLVM = innerLLVM
			curT = innerT
			continue
		}
		// IndexProj on a List or Map base routes through the runtime —
		// the LLVM view of both is opaque `ptr`, so we can't use
		// `extractvalue`. Dispatch on the running MIR type.
		if ip, ok := proj.(*mir.IndexProj); ok {
			elemT := projectionType(proj)
			if elemT == nil {
				if i == len(place.Projections)-1 {
					elemT = t
				} else {
					return "", unsupported("mir-mvp", "projection missing Type")
				}
			}
			elemLLVM := g.llvmType(elemT)
			// Map IndexProj: delegate to the shared runtime helper so
			// `m[k]` and `m.get(k)` share a single ABI. The helper
			// allocates a fresh out-slot, calls the runtime, loads
			// the result, and returns the SSA register holding the
			// loaded value.
			if isMapPtrType(curT) {
				keyT, _ := mapKeyValueTypes(curT)
				if keyT == nil {
					return "", unsupported("mir-mvp", "map IndexProj without key type")
				}
				keyLLVM := g.llvmType(keyT)
				keyString := isStringLLVMType(keyT)
				kReg, err := g.evalOperand(ip.Index, keyT)
				if err != nil {
					return "", err
				}
				curReg = g.emitMapGetOrAbort(curReg, kReg, keyLLVM, keyString, elemLLVM, "")
				curLLVM = elemLLVM
				curT = elemT
				continue
			}
			idxOp := ip.Index
			idxVal, err := g.evalOperand(idxOp, mir.TInt)
			if err != nil {
				return "", err
			}
			if listUsesTypedRuntime(elemLLVM) {
				if i == 0 && listUsesRawDataFastPath(elemLLVM) {
					if fast, ok := g.emitVectorListFastLoad(place.Local, idxVal, elemLLVM); ok {
						curReg = fast
						curLLVM = elemLLVM
						curT = elemT
						continue
					}
				}
				sym := "osty_rt_list_get_" + listRuntimeSymbolSuffix(elemLLVM)
				g.declareRuntime(sym, "declare "+elemLLVM+" @"+sym+"(ptr, i64)")
				next := g.fresh()
				g.fnBuf.WriteString("  ")
				g.fnBuf.WriteString(next)
				g.fnBuf.WriteString(" = call ")
				g.fnBuf.WriteString(elemLLVM)
				g.fnBuf.WriteString(" @")
				g.fnBuf.WriteString(sym)
				g.fnBuf.WriteString("(ptr ")
				g.fnBuf.WriteString(curReg)
				g.fnBuf.WriteString(", i64 ")
				g.fnBuf.WriteString(idxVal)
				g.fnBuf.WriteString(")\n")
				curReg = next
				curLLVM = elemLLVM
				curT = elemT
				continue
			}
			// Composite element — use bytes_v1 out-pointer ABI.
			slot := g.fresh()
			g.fnBuf.WriteString("  ")
			g.fnBuf.WriteString(slot)
			g.fnBuf.WriteString(" = alloca ")
			g.fnBuf.WriteString(elemLLVM)
			g.fnBuf.WriteByte('\n')
			sizeReg := g.emitSizeOf(elemLLVM)
			sym := "osty_rt_list_get_bytes_v1"
			g.declareRuntime(sym, "declare void @"+sym+"(ptr, i64, ptr, i64)")
			g.fnBuf.WriteString("  call void @")
			g.fnBuf.WriteString(sym)
			g.fnBuf.WriteString("(ptr ")
			g.fnBuf.WriteString(curReg)
			g.fnBuf.WriteString(", i64 ")
			g.fnBuf.WriteString(idxVal)
			g.fnBuf.WriteString(", ptr ")
			g.fnBuf.WriteString(slot)
			g.fnBuf.WriteString(", i64 ")
			g.fnBuf.WriteString(sizeReg)
			g.fnBuf.WriteString(")\n")
			loaded := g.fresh()
			g.fnBuf.WriteString("  ")
			g.fnBuf.WriteString(loaded)
			g.fnBuf.WriteString(" = load ")
			g.fnBuf.WriteString(elemLLVM)
			g.fnBuf.WriteString(", ptr ")
			g.fnBuf.WriteString(slot)
			g.fnBuf.WriteByte('\n')
			curReg = loaded
			curLLVM = elemLLVM
			curT = elemT
			continue
		}
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
		if _, isVariant := proj.(*mir.VariantProj); isVariant {
			// Variant payload lives in the i64 slot; extract i64,
			// then narrow to the declared payload type.
			rawSlot := g.fresh()
			g.fnBuf.WriteString("  ")
			g.fnBuf.WriteString(rawSlot)
			g.fnBuf.WriteString(" = extractvalue ")
			g.fnBuf.WriteString(curLLVM)
			g.fnBuf.WriteByte(' ')
			g.fnBuf.WriteString(curReg)
			g.fnBuf.WriteString(", 1\n")
			narrowed, err := g.fromI64Slot(rawSlot, elemT)
			if err != nil {
				return "", err
			}
			curReg = narrowed
			curLLVM = g.llvmType(elemT)
			curT = elemT
			continue
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
		curT = elemT
	}
	return curReg, nil
}

// projectionIndex returns the field index for a FieldProj / TupleProj.
// VariantProj returns field 1 (the payload slot) because enum layouts
// uniformly use `{ i64 disc, i64 payload }`. IndexProj has no static
// index and returns false — the walker handles it via a runtime call
// rather than an `extractvalue`.
func projectionIndex(p mir.Projection) (int, bool) {
	switch x := p.(type) {
	case *mir.FieldProj:
		return x.Index, true
	case *mir.TupleProj:
		return x.Index, true
	case *mir.VariantProj:
		return 1, true
	}
	return 0, false
}

// projectionType returns the result type of a FieldProj / TupleProj /
// VariantProj / IndexProj. For VariantProj the raw type in the place
// chain is the payload's *declared* type; the enum slot itself is i64
// and we narrow at the read site via fromI64Slot.
func projectionType(p mir.Projection) mir.Type {
	switch x := p.(type) {
	case *mir.FieldProj:
		return x.Type
	case *mir.TupleProj:
		return x.Type
	case *mir.VariantProj:
		return x.Type
	case *mir.IndexProj:
		return x.ElemType
	}
	return nil
}

// localType returns the MIR type of a local by id.
func (g *mirGen) localType(id mir.LocalID) mir.Type {
	return localTypeIn(g.fn, id)
}

func localTypeIn(fn *mir.Function, id mir.LocalID) mir.Type {
	if fn == nil {
		return nil
	}
	if loc := fn.Local(id); loc != nil {
		return loc.Type
	}
	return nil
}

func (g *mirGen) placeType(place mir.Place) mir.Type {
	return placeTypeIn(g.fn, place)
}

func placeTypeIn(fn *mir.Function, place mir.Place) mir.Type {
	t := localTypeIn(fn, place.Local)
	if t == nil {
		return nil
	}
	for _, proj := range place.Projections {
		if next := projectionType(proj); next != nil {
			t = next
			continue
		}
		switch x := proj.(type) {
		case *mir.DerefProj:
			t = x.Type
		default:
			return nil
		}
	}
	return t
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
	case *mir.FnConst:
		// Function-pointer literal. Renders as the global symbol
		// reference — LLVM accepts `@fnname` wherever a ptr value is
		// expected. The `discoverFunctions` pass has already recorded
		// the target's signature so indirect calls can look it up.
		if k.Symbol == "" {
			return "", unsupported("mir-mvp", "FnConst with empty symbol")
		}
		return "@" + k.Symbol, nil
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
	if (op == mir.BinEq || op == mir.BinNeq) && isHeapEqualityType(argT) {
		return g.emitHeapEquality(op, left, right)
	}
	if isStringOrderingBinOp(op) && isStringPrimType(argT) {
		return g.emitStringOrdering(op, left, right)
	}
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

// isHeapEqualityType reports whether MIR `==` / `!=` on values of type t
// should be lowered to a runtime content-equality call instead of a raw
// LLVM icmp on the pointer. Stage-1 covers String and Bytes; Bytes uses
// the String runtime since both values share the C-string ABI.
func isHeapEqualityType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	if !ok {
		return false
	}
	return p.Kind == ir.PrimString || p.Kind == ir.PrimBytes
}

// emitHeapEquality lowers `==` / `!=` on pointer-shaped runtime values
// (String, Bytes) to a call to the appropriate `osty_rt_*_Equal`
// helper, negating the result for `!=`. The helper declarations are
// registered lazily on first use so modules without any heap equality
// stay byte-stable.
func (g *mirGen) emitHeapEquality(op mir.BinaryOp, left, right string) (string, error) {
	sym := llvmStringRuntimeEqualSymbol()
	g.declareRuntime(sym, fmt.Sprintf("declare i1 @%s(ptr, ptr)", sym))
	eq := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(eq)
	g.fnBuf.WriteString(" = call i1 @")
	g.fnBuf.WriteString(sym)
	g.fnBuf.WriteString("(ptr ")
	g.fnBuf.WriteString(left)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(right)
	g.fnBuf.WriteString(")\n")
	if op == mir.BinEq {
		return eq, nil
	}
	neq := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(neq)
	g.fnBuf.WriteString(" = xor i1 ")
	g.fnBuf.WriteString(eq)
	g.fnBuf.WriteString(", true\n")
	return neq, nil
}

// isStringPrimType reports whether t is the String primitive. Ordering
// through osty_rt_strings_Compare is String-only; Bytes ordering has no
// runtime support yet and still hits the raw-icmp path.
func isStringPrimType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	return ok && p.Kind == ir.PrimString
}

func isStringOrderingBinOp(op mir.BinaryOp) bool {
	switch op {
	case mir.BinLt, mir.BinLeq, mir.BinGt, mir.BinGeq:
		return true
	}
	return false
}

// emitStringOrdering lowers `<` / `<=` / `>` / `>=` on String values to
// a call to `osty_rt_strings_Compare` (returning i64 -1/0/+1) followed
// by `icmp <pred> i64 result, 0`. Without this routing the raw icmp
// path would compare the underlying ptrs, which yields undefined
// results for content ordering.
func (g *mirGen) emitStringOrdering(op mir.BinaryOp, left, right string) (string, error) {
	sym := llvmStringRuntimeCompareSymbol()
	g.declareRuntime(sym, fmt.Sprintf("declare i64 @%s(ptr, ptr)", sym))
	cmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(cmp)
	g.fnBuf.WriteString(" = call i64 @")
	g.fnBuf.WriteString(sym)
	g.fnBuf.WriteString("(ptr ")
	g.fnBuf.WriteString(left)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(right)
	g.fnBuf.WriteString(")\n")
	pred := stringOrderingPredicate(op)
	out := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(out)
	g.fnBuf.WriteString(" = icmp ")
	g.fnBuf.WriteString(pred)
	g.fnBuf.WriteString(" i64 ")
	g.fnBuf.WriteString(cmp)
	g.fnBuf.WriteString(", 0\n")
	return out, nil
}

func stringOrderingPredicate(op mir.BinaryOp) string {
	switch op {
	case mir.BinLt:
		return "slt"
	case mir.BinLeq:
		return "sle"
	case mir.BinGt:
		return "sgt"
	case mir.BinGeq:
		return "sge"
	}
	return ""
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
		case ir.PrimRawPtr:
			// LANG_SPEC §19.3 — RawPtr is an opaque pointer-shaped
			// scalar; in opaque-pointer LLVM (the toolchain's required
			// version) it lowers to `ptr`. Width is `sizeof(uintptr_t)`,
			// 8 bytes on the supported 64-bit targets.
			return "ptr"
		case ir.PrimUnit:
			return "void"
		case ir.PrimNever:
			return "void"
		}
	case *ir.NamedType:
		// Builtin collection types flow through the runtime as
		// opaque pointers; there's no LLVM struct for them. Same
		// story for the MIR-lowerer-synthesised `ClosureEnv` which
		// names a pointer into a closure env struct.
		if x.Builtin {
			switch x.Name {
			case "List", "Map", "Set", "Bytes", "ClosureEnv":
				return "ptr"
			}
		}
		// Concurrency runtime types are opaque pointers too — the
		// runtime owns their representation.
		switch x.Name {
		case "Channel", "Handle", "Group", "TaskGroup", "Select", "Duration":
			return "ptr"
		}
		// User-declared struct or enum — register the enum in the
		// layout pool on first use and emit by name.
		if g.mod != nil && g.mod.Layouts != nil {
			if _, ok := g.mod.Layouts.Structs[x.Name]; ok {
				return "%" + x.Name
			}
			if _, ok := g.mod.Layouts.Enums[x.Name]; ok {
				g.registerEnumLayout(x.Name, nil)
				return "%" + x.Name
			}
		}
		// Prelude Option / Maybe / Result. Mint an anonymous
		// `%Option.<T>` / `%Result.<T>.<E>` so the IR carries the
		// element type in its name — mimicking how the legacy
		// emitter names its specialisations.
		switch x.Name {
		case "Option", "Maybe":
			return "%" + g.optionTypeName(x)
		case "Result":
			return "%" + g.resultTypeName(x)
		}
		return "%" + x.Name
	case *ir.OptionalType:
		return "%" + g.optionalTypeName(x)
	case *ir.TupleType:
		return "%" + g.tupleName(x)
	case *ir.FnType:
		return "ptr"
	}
	// Fallback: ptr works for anything we end up treating as opaque
	// heap data.
	return "ptr"
}

// ==== enum layout helpers ====

// optionalTypeName returns a mangled `Option.<T>` name for a surface
// `T?` optional. The layout is always `{ i64 disc, <payload> }` where
// payload is the inner's LLVM type promoted to a slot-sized value.
func (g *mirGen) optionalTypeName(t *ir.OptionalType) string {
	name := "Option." + g.llvmTypeForTupleTag(t.Inner)
	g.registerEnumLayout(name, t.Inner)
	return name
}

func (g *mirGen) optionTypeName(t *ir.NamedType) string {
	inner := mir.Type(nil)
	if len(t.Args) > 0 {
		inner = t.Args[0]
	}
	name := "Option"
	if inner != nil {
		name = "Option." + g.llvmTypeForTupleTag(inner)
	}
	g.registerEnumLayout(name, inner)
	return name
}

// resultTypeName mangles a Result<T, E> name. Payload slot sits at
// i64 width and Err payload (usually an Error ptr) reuses it via
// bitcast — matching the legacy emitter's `%Result.T.E` convention.
func (g *mirGen) resultTypeName(t *ir.NamedType) string {
	name := "Result"
	var inner mir.Type
	if len(t.Args) >= 1 {
		inner = t.Args[0]
		name = "Result." + g.llvmTypeForTupleTag(t.Args[0])
		if len(t.Args) >= 2 {
			name += "." + g.llvmTypeForTupleTag(t.Args[1])
		}
	}
	g.registerEnumLayout(name, inner)
	return name
}

// registerEnumLayout records an enum-shaped type's payload for type
// emission. The MIR emitter always uses `{ i64, <payload> }` where
// payload is the wider of i64 / the inner type's LLVM form. For
// scalar payloads smaller than i64 we upcast at construction;
// pointer payloads sit in an i64 via ptrtoint so the layout stays
// uniform with the legacy convention `%Maybe = type { i64, i64 }`.
func (g *mirGen) registerEnumLayout(name string, inner mir.Type) {
	if _, ok := g.enumLayouts[name]; ok {
		return
	}
	g.enumLayouts[name] = inner
	g.enumLayoutOrder = append(g.enumLayoutOrder, name)
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
		// Builtin types that flow as `ptr` at the LLVM boundary are
		// tagged as "ptr" so mangled names like `ClosureEnv.ptr.i64`
		// stay readable. User named types keep their declared name.
		if x.Builtin {
			switch x.Name {
			case "List", "Map", "Set", "Bytes", "ClosureEnv":
				return "ptr"
			}
		}
		switch x.Name {
		case "Channel", "Handle", "Group", "TaskGroup", "Select", "Duration":
			return "ptr"
		}
		return x.Name
	case *ir.TupleType:
		return g.tupleName(x)
	case *ir.FnType:
		return "ptr"
	}
	return "opaque"
}

func (g *mirGen) fresh() string {
	name := fmt.Sprintf("%%t%d", g.tempSeq)
	g.tempSeq++
	return name
}

func (g *mirGen) freshLabel(prefix string) string {
	label := fmt.Sprintf("%s.%d", prefix, g.tempSeq)
	g.tempSeq++
	return label
}

// ostyEmitter returns an LlvmEmitter whose temp counter is seeded from
// g.tempSeq. Call any Osty-authored emission helper (llvmListNew,
// llvmListPush, …) against the returned emitter, then splice the
// accumulated body into g.fnBuf via flushOstyEmitter. This is the
// adapter that lets mir_generator.go delegate actual LLVM text to
// toolchain/llvmgen.osty without double-counting temps.
func (g *mirGen) ostyEmitter() *LlvmEmitter {
	return &LlvmEmitter{temp: g.tempSeq, body: nil}
}

// flushOstyEmitter writes the emitter's body lines into g.fnBuf and
// syncs the temp counter back. Every Osty helper output line already
// carries its leading 2-space indent — we only need to add the
// trailing newline.
func (g *mirGen) flushOstyEmitter(em *LlvmEmitter) {
	g.tempSeq = em.temp
	for _, line := range em.body {
		g.fnBuf.WriteString(line)
		g.fnBuf.WriteByte('\n')
	}
}

// storeIntrinsicResult handles the dest-local store half of the
// old emitSimpleCall path after an Osty-authored helper has produced
// the value-returning call. Returns nil when the intrinsic has no
// destination (call result discarded); coerces scalar widths when the
// dest local's type differs from what the runtime returned.
func (g *mirGen) storeIntrinsicResult(i *mir.IntrinsicInstr, result *LlvmValue) error {
	if i.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(i.Dest.Local)
	if destLoc == nil {
		return nil
	}
	destLLVM := g.llvmType(destLoc.Type)
	stored := result.name
	if destLLVM != result.typ {
		if widened, err := g.coerceValue(result.name, result.typ, destLLVM); err == nil {
			stored = widened
		}
	}
	g.fnBuf.WriteString("  store ")
	g.fnBuf.WriteString(destLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(stored)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
	g.fnBuf.WriteByte('\n')
	return nil
}

// emitRuntimeRawNull lowers `raw.null()` from LANG_SPEC §19.5. The
// result is the pointer-shaped null constant: in opaque-pointer LLVM
// (the toolchain's required version) that's just `null` as a `ptr`.
// No instruction needed beyond the store into the dest slot.
func (g *mirGen) emitRuntimeRawNull(i *mir.IntrinsicInstr) error {
	if len(i.Args) != 0 {
		return unsupported("mir-mvp", "raw.null() takes no arguments")
	}
	return g.storeIntrinsicResult(i, &LlvmValue{typ: "ptr", name: "null"})
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
