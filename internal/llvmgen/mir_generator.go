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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

// fastPathListWriteEnabled gates the MIR fast-path `xs[i] = v` emission.
// Disabled by default while we ship the CFG-reachability relaxation that
// lets reads snapshot across slot-write neighbourhoods — the write path
// itself needs scoped alias metadata to be a consistent win. Flip on
// with OSTY_LLVM_LIST_WRITE_FASTPATH=1 for benchmarking during the next
// iteration.
func fastPathListWriteEnabled() bool {
	return os.Getenv("OSTY_LLVM_LIST_WRITE_FASTPATH") == "1"
}

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
	src    []byte

	out strings.Builder

	// Per-function state (cleared between functions).
	fn          *mir.Function
	seq         MirSeq // SSA / label sequence counter — Phase B Osty mirror
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
	//
	// `layoutCache` mirrors `toolchain/mir_generator.osty: MirLayoutCache`
	// — Osty owns the dedup + insertion-order side
	// (`EnumLayoutOrder` / `TupleOrder`). The element-type map for
	// tuples (`tupleDefs`) stays here because its value is `[]mir.Type`,
	// a Go-only interface slice. Enum-shaped types have no
	// element-type map: their LLVM layout is the fixed
	// `{ i64 disc, i64 payload }` regardless of payload type.
	structOrder []string              // struct name in LayoutTable order (sorted)
	layoutCache MirLayoutCache        // §14 enum / tuple order cache (Osty mirror)
	tupleDefs   map[string][]mir.Type // mangled tuple name → element types

	// Closure-env thunks generated on demand when a bare `FnConst`
	// (top-level fn used as a value) reaches an indirect-call site.
	// Under the uniform env ABI every closure call takes an env as
	// implicit first arg; top-level fns have no env, so the thunk
	// ignores env and delegates to the real fn.
	thunkDefs  map[string]string // symbol → full thunk IR
	thunkOrder []string

	// Interface-related state. `ifaceTouched` is flipped when any code
	// path emits a value typed as `%osty.iface` — either a parameter
	// or a downcast receiver — so `emitTypeDefs` knows to prepend the
	// `%osty.iface` type definition. `vtableRefs` tracks the distinct
	// `@osty.vtable.<impl>__<iface>` symbols referenced from downcast
	// call sites; each becomes an external constant declaration so the
	// emitted module stays self-describing even before the MIR path
	// grows full vtable-body emission.
	ifaceTouched   bool
	vtableRefs     map[string]struct{}
	vtableRefOrder []string

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
	//
	// vectorListSnapDef records the MIR block that emitted the snapshot
	// calls — used to decide dominance when reusing the cached data/len
	// registers. The sentinel vectorListSnapPreambleBlock means "captured
	// in the function preamble" (dominates every block). A real BlockID
	// means "captured inside that block"; the cache is only reusable
	// within that same block — entering a different block must
	// re-snapshot (LLVM's GVN merges redundant `memory(read)` runtime
	// calls across blocks, and LICM hoists them out of non-mutating
	// loops, so the emitted IR stays tight).
	vectorListData    map[mir.LocalID]string
	vectorListLens    map[mir.LocalID]string
	vectorListSnapDef map[mir.LocalID]mir.BlockID
	// vectorListHoistLocals names scalar-List locals whose snapshot
	// should be emitted inline in the entry block right after their
	// single rebind. Populated by planVectorListHoists pre-pass;
	// consumed (and entries deleted) by maybeHoistSnapshotAfterAssign.
	vectorListHoistLocals map[mir.LocalID]string
	// loopMDDefs + listMetaScopeList moved to MirSeq mirror —
	// see `g.seq.LoopMDDefs` / `g.seq.ListMetaScopeList`. The
	// metadata accumulator + alias-scope cache are module-scoped
	// (NOT cleared by `g.seq.Reset()` at function boundaries).
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
		src:           append([]byte(nil), opts.Source...),
		functionTypes: map[string]mirFnSig{},
		thunkDefs:     map[string]string{},
		declares:      map[string]string{},
		strings:       map[string]string{},
		tupleDefs:     map[string][]mir.Type{},
		vtableRefs:    map[string]struct{}{},
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
// Pure, TargetFeatures. Delegates to the Osty-sourced
// `mirFormatFnAttrs` helper (`toolchain/mir_generator.osty`) so the
// attribute-name table has a single source of truth.
func formatFnAttrs(fn *mir.Function) string {
	return mirFormatFnAttrs(fn.InlineMode, fn.Hot, fn.Cold, fn.Pure, fn.TargetFeatures)
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

// firstNonEmpty thin-wraps the Osty-sourced `mirFirstNonEmpty` — Osty
// has no Go-style variadics so the canonical signature over there takes
// a `List<String>`, and we just slicify the variadic here to match.
func firstNonEmpty(xs ...string) string {
	return mirFirstNonEmpty(xs)
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
// nextLoopMD / listAliasScopeRef / nextAccessGroupMD delegate to the
// Osty-mirrored MirSeq state (`toolchain/mir_generator.osty:932`).
// Per-function loop hints flow into `MirSeq.NextLoopMD` via the
// `MirLoopHints` snapshot below; the `MirSeq` accumulator owns the
// `!N` numbering + the alias-scope cache.
func (g *mirGen) nextLoopMD() string {
	return g.seq.NextLoopMD(MirLoopHints{
		Vectorize:              g.vectorizeHint,
		VectorizeWidth:         g.vectorizeWidth,
		VectorizeScalable:      g.vectorizeScalable,
		VectorizePredicate:     g.vectorizePredicate,
		Parallel:               g.parallelHint,
		ParallelAccessGroupRef: g.parallelAccessGroupRef,
		Unroll:                 g.unrollHint,
		UnrollCount:            g.unrollCount,
	})
}

func (g *mirGen) listAliasScopeRef() string {
	return g.seq.ListAliasScopeRef()
}

func (g *mirGen) nextAccessGroupMD() string {
	return g.seq.NextAccessGroupMD()
}

// loopHintsActive reports whether the currently emitting function
// carries any hint that should trigger `!llvm.loop` emission on its
// back-edges. Covers vectorize/parallel/unroll without re-listing the
// individual field names at every call site.
func (g *mirGen) loopHintsActive() bool {
	return mirLoopHintsActive(g.vectorizeHint, g.parallelHint, g.unrollHint)
}

// emitLoopMetadata appends every `!llvm.loop` node + vectorize-enable
// property collected during function emission to the module tail. No
// module footer is emitted when no `#[vectorize]` function ran. The
// accumulator now lives on `g.seq.LoopMDDefs` (MirSeq mirror).
func (g *mirGen) emitLoopMetadata() {
	if len(g.seq.LoopMDDefs) == 0 {
		return
	}
	g.out.WriteByte('\n')
	for _, def := range g.seq.LoopMDDefs {
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
			case "Range":
				// Range<Int> values only materialize via the
				// placeholder UseRV(UnitConst) path in
				// internal/mir/lower.go — there's no real value-
				// position handling yet. Accepting the type here
				// lets the MIR generator walk past functions that
				// assign `0..n` to a temp (e.g. the string-slice
				// receivers in parser.osty's opStripUseCQuotes)
				// without tripping the supported-type check. When
				// backends grow real Range lowering the payload
				// shape (start/end/inclusive) can take over.
				return true
			}
		}
		// Concurrency runtime types are opaque pointers — the emitter
		// doesn't need a declared layout for them. These match what
		// the runtime ABI hands back from chan_make / spawn / etc.
		switch x.Name {
		case "Channel", "Handle", "Group", "TaskGroup", "Select", "Duration":
			return true
		case "Range":
			// Range<T> is a prelude type but the Go-side resolver
			// doesn't always flag its Symbol as SymBuiltin (the
			// self-host checker's prelude registers Range, but the
			// Go resolver isn't always in sync on the same Sym
			// pointer). Accept it here whether Builtin is set or
			// not — the MIR lowerer emits RangeLit as a placeholder
			// UseRV(UnitConst) anyway, so the concrete shape is
			// irrelevant until real lowering arrives.
			return true
		}
		if g.mod != nil && g.mod.Layouts != nil {
			if _, ok := g.mod.Layouts.Structs[x.Name]; ok {
				return true
			}
			if _, ok := g.mod.Layouts.Enums[x.Name]; ok {
				return true
			}
			// User-declared interface. The MIR lowerer populates
			// Layouts.Interfaces for every surviving InterfaceDecl;
			// values of interface type flow as `%osty.iface = { ptr,
			// ptr }` at the LLVM level.
			if _, ok := g.mod.Layouts.Interfaces[x.Name]; ok {
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

// planVectorListHoists identifies non-param scalar-List locals whose
// backing buffer stays stable for the whole function — exactly one
// rebind (in the entry block), no resize-capable operations (push /
// pop / calls) anywhere, element type raw-data eligible. These are
// safe to snapshot once in the entry block and reuse from every
// successor, avoiding the per-block capture that otherwise costs
// two runtime calls per loop iteration in mutation-heavy functions
// like quicksort's partition step.
//
// Only runs when the write fast-path is enabled — on the default
// path the existing preamble+lazy pair keeps its baseline behavior.
func (g *mirGen) planVectorListHoists(fn *mir.Function) map[mir.LocalID]string {
	out := map[mir.LocalID]string{}
	if !fastPathListWriteEnabled() || fn == nil {
		return out
	}
	if !mirFunctionHasBackedge(fn) {
		return out
	}
	// Candidate locals: non-param List<scalar> with raw-data eligible
	// element type. Params are already handled by the preamble path.
	candidates := map[mir.LocalID]string{}
	for _, loc := range fn.Locals {
		if loc == nil || loc.IsParam {
			continue
		}
		elemT := vectorListElemType(loc.Type)
		if elemT == nil {
			continue
		}
		elemLLVM := g.llvmType(elemT)
		if !listUsesRawDataFastPath(elemLLVM) {
			continue
		}
		candidates[loc.ID] = elemLLVM
	}
	if len(candidates) == 0 {
		return out
	}
	touchesLocal := func(operands []mir.Operand, local mir.LocalID) bool {
		for _, op := range operands {
			if id, ok := mirOperandRootLocal(op); ok && id == local {
				return true
			}
		}
		return false
	}
	rebindCount := map[mir.LocalID]int{}
	rebindBlock := map[mir.LocalID]mir.BlockID{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, inst := range bb.Instrs {
			switch x := inst.(type) {
			case *mir.CallInstr:
				// Any call touching the local could resize it via
				// an opaque path — drop it.
				for id := range candidates {
					if touchesLocal(x.Args, id) {
						delete(candidates, id)
					}
				}
			case *mir.IntrinsicInstr:
				if mirIntrinsicSafeForVectorListFastPath(x.Kind) {
					continue
				}
				for id := range candidates {
					if touchesLocal(x.Args, id) {
						delete(candidates, id)
					}
				}
			case *mir.AssignInstr:
				id := x.Dest.Local
				if _, ok := candidates[id]; !ok {
					continue
				}
				if x.Dest.HasProjections() {
					// Slot writes are fine — they don't change
					// the backing buffer address.
					continue
				}
				rebindCount[id]++
				if rebindCount[id] == 1 {
					rebindBlock[id] = bb.ID
				} else {
					// More than one rebind — we can't safely hoist
					// a single snapshot to the entry block because
					// the later rebind invalidates it mid-function.
					delete(candidates, id)
				}
			}
		}
	}
	for id, elemLLVM := range candidates {
		if rebindCount[id] != 1 {
			continue
		}
		if rebindBlock[id] != fn.Entry {
			continue
		}
		out[id] = elemLLVM
	}
	return out
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
	touchesLocal := func(operands []mir.Operand, local mir.LocalID) bool {
		for _, op := range operands {
			if id, ok := mirOperandRootLocal(op); ok && id == local {
				return true
			}
		}
		return false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, inst := range bb.Instrs {
			switch x := inst.(type) {
			case *mir.CallInstr:
				// Previously bailed on any CallInstr; that was
				// overly conservative for functions whose body has
				// a call that doesn't even take the candidate param
				// (e.g. quicksort creates its own `stack` list via
				// intrinsics but never passes `src` to anything).
				// Only drop params that the call actually receives.
				for id := range eligible {
					if touchesLocal(x.Args, id) {
						delete(eligible, id)
					}
				}
			case *mir.IntrinsicInstr:
				if mirIntrinsicSafeForVectorListFastPath(x.Kind) {
					continue
				}
				for id := range eligible {
					if touchesLocal(x.Args, id) {
						delete(eligible, id)
					}
				}
			case *mir.AssignInstr:
				// Rebind-only (`xs = ...`) invalidates the snapshot.
				// Slot-writes (`xs[i] = v`) leave the backing buffer
				// intact, so the snapshot stays valid across them —
				// but only when the write fast-path is enabled. With
				// the feature off, the pre-change behavior stands and
				// any AssignInstr to this root delists it.
				if !x.Dest.HasProjections() {
					delete(eligible, x.Dest.Local)
				} else if !fastPathListWriteEnabled() {
					delete(eligible, x.Dest.Local)
				}
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
	case mir.IntrinsicPrintln, mir.IntrinsicPrint, mir.IntrinsicEprint, mir.IntrinsicEprintln:
		return true
	case mir.IntrinsicListPush, mir.IntrinsicListLen, mir.IntrinsicListGet,
		mir.IntrinsicListIsEmpty, mir.IntrinsicListSorted, mir.IntrinsicListToSet,
		mir.IntrinsicListPop, mir.IntrinsicListFirst, mir.IntrinsicListLast,
		mir.IntrinsicListRemoveAt, mir.IntrinsicListReverse, mir.IntrinsicListReversed,
		mir.IntrinsicListIndexOf, mir.IntrinsicListContains, mir.IntrinsicListSlice:
		return true
	case mir.IntrinsicMapNew, mir.IntrinsicMapGet, mir.IntrinsicMapGetOr,
		mir.IntrinsicMapSet, mir.IntrinsicMapContains, mir.IntrinsicMapLen,
		mir.IntrinsicMapKeys, mir.IntrinsicMapRemove, mir.IntrinsicMapKeysSorted:
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
		mir.IntrinsicStringTrim,
		mir.IntrinsicStringToUpper, mir.IntrinsicStringToLower,
		mir.IntrinsicStringToInt, mir.IntrinsicStringToFloat,
		mir.IntrinsicStringContains, mir.IntrinsicStringStartsWith,
		mir.IntrinsicStringEndsWith, mir.IntrinsicStringIndexOf, mir.IntrinsicStringSplit,
		mir.IntrinsicStringSplitInto,
		mir.IntrinsicStringJoin, mir.IntrinsicStringSubstring:
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
	// Numeric widening from sub-word primitives to Int. Lowered as
	// LLVM zext — no runtime call needed.
	case mir.IntrinsicByteToInt, mir.IntrinsicCharToInt:
		return true
	// Option<T> method lowering — disc/payload extract on %Option.<T>
	// (emitOptionIntrinsic). Unwrap None branches to
	// osty_rt_option_unwrap_none() + unreachable.
	case mir.IntrinsicOptionIsSome, mir.IntrinsicOptionIsNone,
		mir.IntrinsicOptionUnwrap, mir.IntrinsicOptionUnwrapOr:
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
	g.out.WriteString(mirEmitHeaderBlock(g.source, g.opts.Target))
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
		block.WriteString(mirLlvmGlobalVarLine(glob.Name, g.llvmType(glob.Type)))
	}
	block.WriteByte('\n')
	// Ctor function: `@__osty_init_globals() { call <Ti> @_init_xxx();
	// store <Ti> %tmp, ptr @xxx; ... ret void }`.
	block.WriteString(mirInitGlobalsCtorHeader())
	for _, glob := range g.mod.Globals {
		if glob == nil || glob.Init == nil {
			continue
		}
		block.WriteString(mirInitGlobalsCtorStoreSequence(glob.Name, g.llvmType(glob.Type), glob.Init.Name))
	}
	block.WriteString(mirInitGlobalsCtorFooter())
	// LLVM ctor registration. Priority 65535 runs last among ctors
	// so anything with lower priority (runtime setup) has already
	// executed.
	block.WriteString(mirGlobalCtorsRegistration())

	// Inject the block before the first `define ` / `declare ` line
	// so the `@<name>` globals and the ctor sit alongside type defs.
	// Delegates to the Osty-sourced `mirInjectBeforeFirstFn`
	// (`toolchain/mir_generator.osty`).
	rewritten := mirInjectBeforeFirstFn(g.out.String(), block.String())
	g.out.Reset()
	g.out.WriteString(rewritten)
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
	// Build the joined declare block via the Osty-sourced
	// `mirJoinDeclareLines` (`toolchain/mir_generator.osty`); the
	// caller still owns the dedupe / ordering map (`g.declares` /
	// `g.declareOrder`).
	ordered := make([]string, 0, len(g.declareOrder))
	for _, name := range g.declareOrder {
		ordered = append(ordered, g.declares[name])
	}
	header := mirJoinDeclareLines(ordered)
	// Inject the declares before the first "define " line in out.
	// Note: this uses only the `define ` marker (not `declare `) — new
	// declares always slot AFTER any pre-existing declares so the
	// output stays in canonical declare-then-define order. We can't use
	// `mirInjectBeforeFirstFn` directly because that scans for the
	// earlier of the two markers, which would push these declares above
	// any pre-existing ones.
	body := g.out.String()
	idx := strings.Index(body, "define ")
	if idx < 0 {
		g.out.WriteString(header)
		return
	}
	g.out.Reset()
	g.out.WriteString(body[:idx])
	g.out.WriteString(header)
	g.out.WriteByte('\n')
	g.out.WriteString(body[idx:])
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
			g.registerEnumLayout(name)
		}
	}
	if len(g.structOrder) == 0 && g.layoutCache.IsEmpty() && !g.ifaceTouched {
		return
	}

	var block strings.Builder
	if g.ifaceTouched {
		// Interface fat-pointer type def must precede any `%osty.iface`
		// use inside struct / tuple layouts. Ordering is conservative —
		// emit once at the top of the type-def block.
		block.WriteString(mirLlvmIfaceTypeDefLine())
	}
	for _, name := range g.structOrder {
		sl := g.mod.Layouts.Structs[name]
		if sl == nil {
			continue
		}
		parts := make([]string, len(sl.Fields))
		for i, f := range sl.Fields {
			parts[i] = g.llvmType(f.Type)
		}
		block.WriteString(mirLlvmStructTypeDefLine(name, strings.Join(parts, ", ")))
	}
	for _, name := range g.layoutCache.EnumLayoutOrder {
		block.WriteString(mirLlvmEnumLayoutTypeDefLine(name))
	}
	for _, name := range g.layoutCache.TupleOrder {
		elems := g.tupleDefs[name]
		parts := make([]string, len(elems))
		for i, e := range elems {
			parts[i] = g.llvmType(e)
		}
		block.WriteString(mirLlvmStructTypeDefLine(name, strings.Join(parts, ", ")))
	}
	// Vtable declarations for every `@osty.vtable.<impl>__<iface>`
	// referenced from a downcast call site. Declared as external
	// constants — the actual vtable bodies (one `ptr` per method) are
	// not yet emitted by the MIR path; the downcast-compare lowering
	// only needs the symbol to exist so the `icmp eq ptr` is a legal
	// reference.
	for _, sym := range g.vtableRefOrder {
		block.WriteString(mirLlvmVtableDeclLine(sym))
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
	g.seq.Reset()
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
	g.vectorListSnapDef = map[mir.LocalID]mir.BlockID{}
	g.vectorListHoistLocals = g.planVectorListHoists(fn)
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
		g.out.WriteString(mirTagParallelAccesses(g.fnBuf.String(), g.parallelAccessGroupRef))
	} else {
		g.out.WriteString(g.fnBuf.String())
	}
	return nil
}

// tagParallelAccesses + isMemoryAccessLine moved fully to the Osty
// mirror (`mirTagParallelAccesses` / `mirIsMemoryAccessLine` in
// `toolchain/mir_generator.osty`, snapshotted in
// `mir_generator_snapshot.go`). The MIR emitter call site above and
// `generator.go::tagParallelAccessesLines` now invoke the Osty-
// mirrored helpers directly — the previous Go shim wrappers had no
// remaining callers.

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

// vectorListSnapPreambleBlock marks a snapshot captured in the function
// preamble (before any MIR block is emitted). The entry-block preamble
// dominates every block, so its cache can be reused anywhere. We pick a
// sentinel outside the valid BlockID range (-1) to distinguish from
// per-block lazy captures, whose cache is reusable only within the
// defining block.
const vectorListSnapPreambleBlock mir.BlockID = -1

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
	savedBlock := g.curBlockID
	g.curBlockID = vectorListSnapPreambleBlock
	for _, rawID := range ids {
		pid := mir.LocalID(rawID)
		elemLLVM := eligible[pid]
		g.snapshotVectorListLocal(pid, elemLLVM)
	}
	g.curBlockID = savedBlock
}

// snapshotValidInCurBlock reports whether the cached data/len registers
// for `local` were emitted in a block that dominates the current MIR
// block. A preamble-captured snapshot dominates everything; a snapshot
// captured inside a MIR block only dominates that same block.
func (g *mirGen) snapshotValidInCurBlock(local mir.LocalID) bool {
	b, ok := g.vectorListSnapDef[local]
	if !ok {
		return false
	}
	return b == vectorListSnapPreambleBlock || b == g.curBlockID
}

// invalidateStaleVectorListSnapshot clears a cache entry whose defining
// block no longer dominates the current block. Callers that are about to
// emit a fresh snapshot (or fall through to a runtime call) must run
// this first so they don't hand out a register defined upstream in a
// non-dominating path.
func (g *mirGen) invalidateStaleVectorListSnapshot(local mir.LocalID) {
	if g.snapshotValidInCurBlock(local) {
		return
	}
	delete(g.vectorListData, local)
	delete(g.vectorListLens, local)
	delete(g.vectorListSnapDef, local)
}

// mirBlockSuccessors returns the out-edges of a MIR block — the set of
// block IDs control can transfer to from the terminator. Used by
// mirLocalMutationReachableFrom for forward-CFG analysis.
func mirBlockSuccessors(bb *mir.BasicBlock) []mir.BlockID {
	if bb == nil {
		return nil
	}
	switch t := bb.Term.(type) {
	case *mir.GotoTerm:
		return []mir.BlockID{t.Target}
	case *mir.BranchTerm:
		return []mir.BlockID{t.Then, t.Else}
	case *mir.SwitchIntTerm:
		out := make([]mir.BlockID, 0, len(t.Cases)+1)
		for _, c := range t.Cases {
			out = append(out, c.Target)
		}
		out = append(out, t.Default)
		return out
	}
	return nil
}

// mirLocalIsMutatedIn returns true if any instruction in `bb` mutates
// `local` — i.e., passes it to an unsafe call/intrinsic, or reassigns
// it via an AssignInstr. Mirrors the bailout rules in
// eligibleVectorListParams but scoped to a single block and a single
// local.
//
// AssignInstr classification: a write with NO projections on Dest
// (`xs = ...`) rebinds the local to a possibly-different List object
// and invalidates any cached `data`/`len` snapshot. A write WITH
// projections (`xs[i] = v`, `point.x = 3`, etc.) updates a slot of an
// existing container without relocating its backing buffer — so a
// snapshotted `data` pointer stays valid across the write. Slot-writes
// are treated as non-mutations for snapshot purposes; this unlocks the
// vectorizable fast path for tight `xs[i] = v` loops (matmul, in-place
// quicksort partitioning).
func mirLocalIsMutatedIn(bb *mir.BasicBlock, local mir.LocalID) bool {
	if bb == nil {
		return false
	}
	touchesLocal := func(operands []mir.Operand) bool {
		for _, op := range operands {
			if id, ok := mirOperandRootLocal(op); ok && id == local {
				return true
			}
		}
		return false
	}
	slotWriteCountsAsMutation := !fastPathListWriteEnabled()
	for _, inst := range bb.Instrs {
		switch x := inst.(type) {
		case *mir.CallInstr:
			if touchesLocal(x.Args) {
				return true
			}
		case *mir.IntrinsicInstr:
			if mirIntrinsicSafeForVectorListFastPath(x.Kind) {
				continue
			}
			if touchesLocal(x.Args) {
				return true
			}
		case *mir.AssignInstr:
			if x.Dest.Local != local {
				continue
			}
			if !x.Dest.HasProjections() {
				return true
			}
			if slotWriteCountsAsMutation {
				return true
			}
		}
	}
	return false
}

// mirLocalMutationReachableFrom returns true if, starting from block
// `fromBB` and walking the forward CFG, any reachable block mutates
// `local`. "Reaches itself" counts — so a loop body that contains both
// the snapshot site and a push is correctly rejected. This is the
// soundness gate that keeps the lazy snapshot path from caching a stale
// `data` pointer past a reallocation.
func mirLocalMutationReachableFrom(fn *mir.Function, fromBB mir.BlockID, local mir.LocalID) bool {
	if fn == nil {
		return false
	}
	visited := make(map[mir.BlockID]bool, len(fn.Blocks))
	stack := []mir.BlockID{fromBB}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[id] {
			continue
		}
		visited[id] = true
		bb := fn.Block(id)
		if bb == nil {
			continue
		}
		if mirLocalIsMutatedIn(bb, local) {
			return true
		}
		for _, s := range mirBlockSuccessors(bb) {
			if !visited[s] {
				stack = append(stack, s)
			}
		}
	}
	return false
}

// snapshotVectorListLocal emits a one-shot `data`/`len` capture for the
// given scalar-List local (param or plain local) and records the result
// registers in `vectorListData` / `vectorListLens`. Callers must have
// already ensured the local has a stored value by the time this runs;
// emitting too early (before the backing `osty_rt_list_*` object is
// assigned) would capture an uninitialised slot. The helper is a no-op
// when a snapshot already exists or the element type isn't fast-path
// eligible (i64 / i1 / double).
//
// The data/len runtime symbols are declared with `memory(read)` so
// LLVM's LICM can hoist the snapshot out of any loop that doesn't
// mutate the list — which is what lets this path deliver real-world
// SIMD speedups for plain `for i in 0..xs.len() { acc ^= xs[i] }`
// patterns on locals (not just params).
func (g *mirGen) snapshotVectorListLocal(local mir.LocalID, elemLLVM string) {
	if !listUsesRawDataFastPath(elemLLVM) {
		return
	}
	slot := g.localSlots[local]
	if slot == "" {
		return
	}
	if g.vectorListData[local] != "" && g.vectorListLens[local] != "" {
		return
	}
	listReg := g.fresh()
	g.fnBuf.WriteString(mirLoadLine(listReg, "ptr", slot))

	scopeList := g.listAliasScopeRef()

	dataSym := listRuntimeDataSymbol(elemLLVM)
	g.declareRuntime(dataSym, mirRuntimeDeclareMemoryRead("ptr", dataSym, "ptr"))
	dataReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueWithAliasScopeLine(dataReg, "ptr", dataSym, "ptr "+listReg, scopeList))
	g.vectorListData[local] = dataReg

	lenSym := listRuntimeLenSymbol()
	g.declareRuntime(lenSym, mirRuntimeDeclareMemoryRead("i64", lenSym, "ptr"))
	lenReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueWithAliasScopeLine(lenReg, "i64", lenSym, "ptr "+listReg, scopeList))
	g.vectorListLens[local] = lenReg
	g.vectorListSnapDef[local] = g.curBlockID
}

func (g *mirGen) emitVectorListFastLoad(local mir.LocalID, idxVal, elemLLVM string) (string, bool) {
	slot := g.localSlots[local]
	if slot == "" {
		return "", false
	}
	// Lazy snapshot: when a local scalar List wasn't captured at
	// function entry (i.e., it isn't an eligible param) but is about
	// to be subscripted, emit the data/len capture here — provided no
	// mutation of this local is forward-reachable in the CFG. The
	// reachability gate is the correctness anchor: without it, a later
	// push could reallocate the list's backing buffer and the cached
	// `dataReg` would dangle. With it, LLVM's LICM still hoists the
	// memory(read) snapshot calls out of non-mutating loops, so the
	// resulting fast/slow branch matches the preamble shape that
	// already vectorizes for params.
	g.invalidateStaleVectorListSnapshot(local)
	if g.vectorListData[local] == "" || g.vectorListLens[local] == "" {
		if mirLocalMutationReachableFrom(g.fn, g.curBlockID, local) {
			return "", false
		}
		g.snapshotVectorListLocal(local, elemLLVM)
	}
	dataReg := g.vectorListData[local]
	lenReg := g.vectorListLens[local]
	if dataReg == "" || lenReg == "" {
		return "", false
	}
	inBounds := g.fresh()
	g.fnBuf.WriteString("  " + inBounds + " = icmp ult i64 " + idxVal + ", " + lenReg + "\n")

	fastLabel := g.freshLabel("list.fast")
	slowLabel := g.freshLabel("list.slow")
	mergeLabel := g.freshLabel("list.merge")
	g.fnBuf.WriteString(mirBrCondLine(inBounds, fastLabel, slowLabel))

	g.fnBuf.WriteString(mirLabelLine(fastLabel))
	elemPtr := g.fresh()
	g.fnBuf.WriteString(mirGEPInboundsLine(elemPtr, elemLLVM, dataReg, "i64", idxVal))
	// Fast-path read touches the data buffer, which is outside the
	// list-metadata alias scope — tagging the load with `!noalias`
	// lets LLVM prove the buffer load doesn't clobber the snapshot
	// `osty_rt_list_data_*` / `osty_rt_list_len` calls, so LICM can
	// hoist them out of loops that interleave reads with other
	// buffer traffic.
	scopeList := g.listAliasScopeRef()
	fastVal := g.fresh()
	g.fnBuf.WriteString(mirLoadWithNoAliasLine(fastVal, elemLLVM, elemPtr, scopeList))
	g.fnBuf.WriteString(mirBrUncondLine(mergeLabel))

	slowSym := listRuntimeGetSymbol(elemLLVM)
	// memory(read): the typed slow-path read is semantically pure
	// (modulo an idempotent first-call layout init). Marking it
	// readonly lets LLVM reason across the fast/slow branch and keep
	// the loop vectorizable even when the slow path is theoretically
	// reachable for OOB indices.
	g.declareRuntime(slowSym, mirRuntimeDeclareMemoryRead(elemLLVM, slowSym, "ptr, i64"))
	g.fnBuf.WriteString(mirLabelLine(slowLabel))
	listReg := g.fresh()
	g.fnBuf.WriteString(mirLoadLine(listReg, "ptr", slot))
	slowVal := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(slowVal, elemLLVM, slowSym, "ptr "+listReg+", i64 "+idxVal))
	g.fnBuf.WriteString(mirBrUncondLine(mergeLabel))

	g.fnBuf.WriteString(mirLabelLine(mergeLabel))
	merged := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(merged, elemLLVM, fastVal, fastLabel, slowVal, slowLabel))
	return merged, true
}

// emitVectorListFastStore emits an inline bounds-checked write to a
// snapshotted scalar-List local, symmetric to emitVectorListFastLoad.
// Returns true when the fast path was used; false (no IR emitted) when
// the local has no valid snapshot, the element type isn't raw-data
// eligible, or a mutation is forward-reachable from here. Caller must
// fall through to the runtime set call on false.
//
// Correctness: the fast path is safe iff the backing buffer cannot move
// between the snapshot site and the write site. The gate is identical
// to the read case — `mirLocalMutationReachableFrom` walks forward from
// `curBlockID` and any reachable push/pop/rebind forces the slow path.
// A slot-write `xs[j] = v` does NOT relocate `xs.data`, so it doesn't
// invalidate its own snapshot; that's why matmul's inner loop and
// quicksort's partition loop both vectorize after this change.
func (g *mirGen) emitVectorListFastStore(local mir.LocalID, idxVal, valReg, elemLLVM string) bool {
	if !listUsesRawDataFastPath(elemLLVM) {
		return false
	}
	slot := g.localSlots[local]
	if slot == "" {
		return false
	}
	g.invalidateStaleVectorListSnapshot(local)
	if g.vectorListData[local] == "" || g.vectorListLens[local] == "" {
		if mirLocalMutationReachableFrom(g.fn, g.curBlockID, local) {
			return false
		}
		g.snapshotVectorListLocal(local, elemLLVM)
	}
	dataReg := g.vectorListData[local]
	lenReg := g.vectorListLens[local]
	if dataReg == "" || lenReg == "" {
		return false
	}

	inBounds := g.fresh()
	g.fnBuf.WriteString("  " + inBounds + " = icmp ult i64 " + idxVal + ", " + lenReg + "\n")

	fastLabel := g.freshLabel("list.set.fast")
	slowLabel := g.freshLabel("list.set.slow")
	mergeLabel := g.freshLabel("list.set.merge")
	g.fnBuf.WriteString(mirBrCondLine(inBounds, fastLabel, slowLabel))

	g.fnBuf.WriteString(mirLabelLine(fastLabel))
	elemPtr := g.fresh()
	g.fnBuf.WriteString(mirGEPInboundsLine(elemPtr, elemLLVM, dataReg, "i64", idxVal))
	// `!noalias` tags the buffer store as outside the list-metadata
	// scope, disjoint from the `osty_rt_list_data_*` / `osty_rt_list_len`
	// snapshot calls. That's the alias fact LLVM needs to hoist those
	// calls past this store in LICM — previously the store was
	// conservatively assumed to clobber the metadata memory the calls
	// read from, pinning the snapshot inside the loop.
	scopeList := g.listAliasScopeRef()
	g.fnBuf.WriteString(mirStoreWithNoAliasLine(elemLLVM, valReg, elemPtr, scopeList))
	g.fnBuf.WriteString(mirBrUncondLine(mergeLabel))

	// Slow path: call a dedicated noreturn helper that aborts with the
	// same "list index out of range" message the typed runtime set
	// function uses. The terminal `unreachable` tells LLVM this branch
	// doesn't return — so the snapshot's `memory(read)` data/len calls
	// stay hoistable over the fast-path store, even though the hot
	// loop executes slot writes every iteration. Switching from the
	// previous slow-path `osty_rt_list_set_*` call (which was declared
	// as a memory writer) is what unlocks the in-loop LICM win on
	// matmul and quicksort's partition step.
	const oobSym = "osty_rt_list_oob_abort_v1"
	g.declareRuntime(oobSym, mirRuntimeDeclareNoReturn("void", oobSym, "", true))
	g.fnBuf.WriteString(mirLabelLine(slowLabel))
	g.fnBuf.WriteString(mirCallVoidNoReturnNoArgsLine(oobSym))
	g.fnBuf.WriteString(mirUnreachableLine())

	g.fnBuf.WriteString(mirLabelLine(mergeLabel))
	return true
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
		g.fnBuf.WriteString(mirAllocaArrayLine(slotsPtr, "ptr", strconv.Itoa(len(window))))
		for i, addr := range window {
			slotPtr := g.fresh()
			g.fnBuf.WriteString(mirGEPLine(slotPtr, "ptr", slotsPtr, "i64", strconv.Itoa(i)))
			g.fnBuf.WriteString(mirStorePtrLine(addr, slotPtr))
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
	g.fnBuf.WriteString(mirLoadLine(count, "i64", g.loopSafepointSlot))
	next := g.fresh()
	g.fnBuf.WriteString("  " + next + " = add i64 " + count + ", 1\n")
	shouldPoll := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(shouldPoll, "i64", next, strconv.FormatInt(loopSafepointStride, 10)))
	stored := g.fresh()
	g.fnBuf.WriteString(mirSelectLine(stored, "i64", shouldPoll, "0", next))
	g.fnBuf.WriteString(mirStoreLine("i64", stored, g.loopSafepointSlot))
	pollLabel := g.freshLabel("gc.loop.poll")
	afterLabel := g.freshLabel("gc.loop.after")
	g.fnBuf.WriteString(mirBrCondLine(shouldPoll, pollLabel, afterLabel))
	g.fnBuf.WriteString(mirLabelLine(pollLabel))
	g.emitGCSafepointKind(safepointKindLoop)
	g.fnBuf.WriteString(mirBrUncondLine(afterLabel))
	g.fnBuf.WriteString(mirLabelLine(afterLabel))
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
		if err := g.emitAssign(x); err != nil {
			return err
		}
		g.maybeHoistSnapshotAfterAssign(x)
		return nil
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

// maybeHoistSnapshotAfterAssign emits the scalar-List snapshot for
// `local` immediately after its initial rebind in the entry block,
// provided the pre-pass marked the local as hoist-eligible. The
// snapshot registers then dominate every block — on par with the
// preamble-captured param case — so later fast-path loads/stores
// reuse them instead of re-issuing `osty_rt_list_data_*` per block.
// Without this hoist, LLVM's LICM can't always merge block-local
// snapshots (a per-block capture in a multi-block hot loop pays the
// runtime-call cost every iteration), which is what kept
// quicksort's partition loop from benefitting from the fast path.
func (g *mirGen) maybeHoistSnapshotAfterAssign(a *mir.AssignInstr) {
	if len(g.vectorListHoistLocals) == 0 {
		return
	}
	if g.curBlockID != g.fn.Entry {
		return
	}
	if a.Dest.HasProjections() {
		return
	}
	elemLLVM, ok := g.vectorListHoistLocals[a.Dest.Local]
	if !ok {
		return
	}
	if g.vectorListData[a.Dest.Local] != "" {
		return
	}
	// Route through the preamble sentinel so the snapshot is
	// treated as dominating every block — the rebind just executed
	// is part of the entry block, so any register defined after it
	// is available to every successor in LLVM SSA.
	saved := g.curBlockID
	g.curBlockID = vectorListSnapPreambleBlock
	g.snapshotVectorListLocal(a.Dest.Local, elemLLVM)
	g.curBlockID = saved
	delete(g.vectorListHoistLocals, a.Dest.Local)
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

// emitIndexedWrite lowers an AssignInstr whose Dest projection chain
// ends in an IndexProj — `xs[i] = v` / `m[k] = v` / `env.field[i] = v`.
// The chain up to (but not including) the final IndexProj is walked
// with extractvalue to reach the container pointer. The IndexProj
// itself dispatches to the list / map runtime setter.
func (g *mirGen) emitIndexedWrite(a *mir.AssignInstr, destLoc *mir.Local, ip *mir.IndexProj) error {
	localT := destLoc.Type
	slot := g.localSlots[a.Dest.Local]
	localLLVM := g.llvmType(localT)
	contReg := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(contReg)
	g.fnBuf.WriteString(" = load ")
	g.fnBuf.WriteString(localLLVM)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(slot)
	g.fnBuf.WriteByte('\n')

	// Walk field/tuple/variant projections preceding the final
	// IndexProj, extractvalue-ing into the next aggregate each step.
	// This lets `env.fnIndexValues[slot] = v` reach the List<Int>
	// pointer stored inside env (CheckEnv struct) before handing off
	// to the list-set runtime.
	projs := a.Dest.Projections
	containerT := localT
	containerLLVM := localLLVM
	for i := 0; i < len(projs)-1; i++ {
		idx, ok := projectionIndex(projs[i])
		if !ok {
			return unsupported("mir-mvp", fmt.Sprintf("indexed write preceding projection %T not yet supported in %s", projs[i], g.fn.Name))
		}
		nextT := projectionType(projs[i])
		if nextT == nil {
			return unsupported("mir-mvp", "indexed write preceding projection missing type")
		}
		nextLLVM := g.llvmType(nextT)
		next := g.fresh()
		g.fnBuf.WriteString(mirExtractValueLine(next, containerLLVM, contReg, fmt.Sprint(idx)))
		contReg = next
		containerLLVM = nextLLVM
		containerT = nextT
	}
	localT = containerT
	_ = containerLLVM

	elemT := ip.ElemType
	if elemT == nil {
		return unsupported("mir-mvp", "indexed write missing element type")
	}
	elemLLVM := g.llvmType(elemT)

	switch {
	case isListPtrType(localT):
		idxReg, err := g.evalOperand(ip.Index, mir.TInt)
		if err != nil {
			return err
		}
		valReg, err := g.evalRValue(a.Src, elemT)
		if err != nil {
			return err
		}
		if listUsesTypedRuntime(elemLLVM) {
			// Fast path: when the root local is the list itself (no
			// preceding field/tuple projections) and snapshot
			// eligibility holds, emit an inline bounds-checked store
			// against the cached `data` pointer. LLVM can then SLP /
			// vectorize the inner loop alongside any fast-path reads
			// on the same list. When nested projections are present
			// (e.g. `env.xs[i] = v`), the `contReg` was derived via
			// extractvalue and has no tracked snapshot, so fall
			// through to the runtime call.
			//
			// Gated by env var during bring-up: the fast-path write
			// unlocks huge wins on snapshot-friendly loops but regresses
			// partition-style inner loops (e.g. quicksort) whose
			// snapshot calls currently can't be CSE'd across fast-path
			// store writes. Keeping it opt-in avoids a geomean hit on
			// default runs while we tune alias metadata in a follow-up.
			if fastPathListWriteEnabled() && len(projs) == 1 {
				if g.emitVectorListFastStore(a.Dest.Local, idxReg, valReg, elemLLVM) {
					return nil
				}
			}
			sym := "osty_rt_list_set_" + listRuntimeSymbolSuffix(elemLLVM)
			g.declareRuntime(sym, "declare void @"+sym+"(ptr, i64, "+elemLLVM+")")
			g.fnBuf.WriteString("  call void @")
			g.fnBuf.WriteString(sym)
			g.fnBuf.WriteString("(ptr ")
			g.fnBuf.WriteString(contReg)
			g.fnBuf.WriteString(", i64 ")
			g.fnBuf.WriteString(idxReg)
			g.fnBuf.WriteString(", ")
			g.fnBuf.WriteString(elemLLVM)
			g.fnBuf.WriteByte(' ')
			g.fnBuf.WriteString(valReg)
			g.fnBuf.WriteString(")\n")
			return nil
		}
		// Composite element path — spill the value to a stack slot
		// and hand the runtime a pointer + size so it can memcpy
		// the bytes in place. Mirrors the IntrinsicListGet bytes-v1
		// shape on the read side.
		em := g.ostyEmitter()
		valSlot := llvmSpillToSlot(em, &LlvmValue{typ: elemLLVM, name: valReg})
		g.flushOstyEmitter(em)
		sizeReg := g.emitSizeOf(elemLLVM)
		sym := "osty_rt_list_set_bytes_v1"
		g.declareRuntime(sym, "declare void @"+sym+"(ptr, i64, ptr, i64, ptr)")
		g.fnBuf.WriteString("  call void @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(contReg)
		g.fnBuf.WriteString(", i64 ")
		g.fnBuf.WriteString(idxReg)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(valSlot.name)
		g.fnBuf.WriteString(", i64 ")
		g.fnBuf.WriteString(sizeReg)
		g.fnBuf.WriteString(", ptr null)\n")
		return nil
	case isMapPtrType(localT):
		keyT, _ := mapKeyValueTypes(localT)
		if keyT == nil {
			return unsupported("mir-mvp", "map indexed write without key type")
		}
		keyLLVM := g.llvmType(keyT)
		keyString := isStringLLVMType(keyT)
		kReg, err := g.evalOperand(ip.Index, keyT)
		if err != nil {
			return err
		}
		vReg, err := g.evalRValue(a.Src, elemT)
		if err != nil {
			return err
		}
		sym := mapRuntimeInsertSymbol(keyLLVM, keyString)
		g.declareRuntime(sym, "declare void @"+sym+"(ptr, "+keyLLVM+", ptr)")
		em := g.ostyEmitter()
		valSlot := llvmSpillToSlot(em, &LlvmValue{typ: elemLLVM, name: vReg})
		llvmMapInsert(em,
			&LlvmValue{typ: "ptr", name: contReg},
			&LlvmValue{typ: keyLLVM, name: kReg},
			valSlot,
			keyString)
		g.flushOstyEmitter(em)
		return nil
	}
	return unsupported("mir-mvp", fmt.Sprintf("indexed write on non-List/Map base %s", mirTypeString(localT)))
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
		g.fnBuf.WriteString(mirStoreLine(g.llvmType(localT), val, g.localSlots[a.Dest.Local]))
		return nil
	}
	// IndexProj-tail write (`xs[i] = v`, `m[k] = v`, or a struct-field
	// chain ending in an index like `env.fnIndexValues[slot] = v`) —
	// route to the runtime setter. The chain up to (but not including)
	// the final IndexProj extracts a container pointer out of the
	// local; the final IndexProj delegates to the list / map write.
	// Anything with an IndexProj *before* the last position still
	// falls through to the aggregate-insertvalue rebuild below, which
	// rejects IndexProj mid-chain.
	if projs := a.Dest.Projections; len(projs) > 0 {
		if ip, ok := projs[len(projs)-1].(*mir.IndexProj); ok {
			return g.emitIndexedWrite(a, destLoc, ip)
		}
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
	g.fnBuf.WriteString(mirLoadLine(aggReg, slotLLVM, slot))

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
			return unsupported("mir-mvp", fmt.Sprintf("projection %T on write in %s (projs=%d, pos=%d)", proj, g.fn.Name, len(projs), i))
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
		g.fnBuf.WriteString(mirExtractValueLine(next, curLLVM, curReg, strconv.Itoa(idx)))
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
		g.fnBuf.WriteString(mirInsertValueAggLine(rebuilt, f.llvm, f.reg, innerLLVM, inner, strconv.Itoa(f.idx)))
		inner = rebuilt
		innerLLVM = f.llvm
	}
	// Store the rebuilt top-level aggregate back.
	g.fnBuf.WriteString(mirStoreLine(slotLLVM, inner, slot))
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
	// `<Interface>__downcast` synthetic method call — the interface
	// type carries no real method body; the call must lower to a
	// vtable-compare + optional construction sequence instead of a
	// regular `call @<sym>`. The result is `T?` materialised through
	// the standard `%Option.<T> = { i64 disc, i64 payload }` layout so
	// downstream pattern-match on the optional reads normally.
	if handled, err := g.tryEmitInterfaceDowncast(c, fnRef); handled {
		return err
	}
	// Intercept stdlib `std.testing.*` helpers. The legacy AST emitter
	// inlines these (see stmt.go:emitTestingCallStmt); the MIR path
	// mirrors that dispatch in-place instead of trying to resolve the
	// symbol as an ordinary user function. Scope stays narrow: only the
	// shapes the LLVM compat tests exercise (assertEq/assertNe/assert/
	// assertTrue/assertFalse/fail/context/benchmark/expectOk/expectError/
	// snapshot).
	if strings.HasPrefix(fnRef.Symbol, "std.testing.") {
		if handled, err := g.emitStdTestingCall(c, fnRef); handled {
			return err
		}
	}
	if strings.HasPrefix(fnRef.Symbol, "std.io.") {
		if handled, err := g.emitStdIoCall(c, fnRef); handled {
			return err
		}
	}
	if strings.HasPrefix(fnRef.Symbol, "std.hint.") {
		if handled, err := g.emitStdHintCall(c, fnRef); handled {
			return err
		}
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
	g.fnBuf.WriteString(mirLoadLine(fnPtr, "ptr", envPtr))
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
			g.fnBuf.WriteString(mirCallVoidLine(symbol, strings.Join(argStrs, ", ")))
			return nil
		}
		tmp := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(tmp, retLLVM, symbol, strings.Join(argStrs, ", ")))
		g.fnBuf.WriteString(mirStoreLine(g.llvmType(destLoc.Type), tmp, g.localSlots[c.Dest.Local]))
		return nil
	}
	// Statement-position call (no dest): emit `  call <retLLVM> @<sym>(<args>)\n`
	// directly. retLLVM may be void (action call) or a value type (result
	// discarded); both paths share the same shape.
	g.fnBuf.WriteString("  call " + retLLVM + " @" + symbol + "(" + strings.Join(argStrs, ", ") + ")\n")
	return nil
}

// emitStdHintCall dispatches `std.hint.*` call symbols. Currently
// handles `black_box` only — a value-preserving barrier the optimizer
// can't see through. Returns handled=true when the symbol matched.
func (g *mirGen) emitStdHintCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.hint.")
	switch method {
	case "black_box":
		return true, g.emitHintBlackBoxMIR(c)
	}
	return false, nil
}

// emitHintBlackBoxMIR lowers `std.hint.black_box(v)` to an identity
// wrapper the optimizer can't see through: an empty inline-asm block
// marked `sideeffect` that claims to read the input and hand back an
// equivalent register. The `sideeffect` qualifier blocks DCE of the
// producing expression (the whole reason loop_sum benches would
// otherwise fold `sumTo(100)` to a constant and disappear), and the
// opaque asm output blocks const-fold across the call.
//
// Scalar types only for now: i1/i8/i16/i32/i64/f32/f64/ptr fit the
// "=r,0" (output-ties-input) register constraint. Struct/aggregate
// arguments go through the fallback path (memory-clobbered alloca);
// no current caller needs that so it's not implemented yet.
func (g *mirGen) emitHintBlackBoxMIR(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.hint.black_box requires one argument")
	}
	arg := c.Args[0]
	argT := arg.Type()
	argLLVM := g.llvmType(argT)
	argRef, err := g.evalOperand(arg, argT)
	if err != nil {
		return err
	}
	if !isScalarLLVMType(argLLVM) {
		return unsupportedf("mir-mvp", "std.hint.black_box on non-scalar type %q not yet supported (register-tied asm constraint doesn't fit aggregates)", argLLVM)
	}
	emit := func(dest string) {
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(dest)
		g.fnBuf.WriteString(" = call ")
		g.fnBuf.WriteString(argLLVM)
		g.fnBuf.WriteString(` asm sideeffect "", "=r,0"(`)
		g.fnBuf.WriteString(argLLVM)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(argRef)
		g.fnBuf.WriteString(")\n")
	}
	if c.Dest == nil {
		// `let _ = black_box(x)` — no dest slot. Emit the asm anyway so
		// the input computation survives DCE; the returned register is
		// simply unused. (LLVM keeps it because the asm is sideeffect.)
		tmp := g.fresh()
		emit(tmp)
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	tmp := g.fresh()
	emit(tmp)
	g.fnBuf.WriteString("  store ")
	g.fnBuf.WriteString(g.llvmType(destLoc.Type))
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(g.localSlots[c.Dest.Local])
	g.fnBuf.WriteByte('\n')
	return nil
}

// isScalarLLVMType delegates to the Osty-sourced `mirIsScalarLLVMType`
// predicate (`toolchain/mir_generator.osty`). The set the `=r,0` asm
// constraint can tie to stays a single source of truth on the Osty
// side; composite types (struct / array) fall through to the safe path.
func isScalarLLVMType(t string) bool {
	return mirIsScalarLLVMType(t)
}

// emitStdTestingCall dispatches `std.testing.*` call symbols at MIR
// emission time. Returns handled=true when the symbol matched a known
// helper (even on error). Returns handled=false when the symbol is not
// one of the supported method names, letting the ordinary direct-call
// path run (and eventually reject with LLVM000 as before). Unsupported
// method names inside std.testing return handled=true, err=<unsupported>
// so we fail loudly rather than misroute into the fallback.
//
// Mirrors stmt.go:emitTestingCallStmt at the shape level: assertion
// helpers evaluate their args for side-effects and drop the result;
// `context` invokes the closure; `benchmark` invokes the closure once
// after consuming its iteration-count argument. Full failure-message
// emission is deferred — the MIR tests assert on user-fn lowering, not
// the diagnostic payload shape.
func (g *mirGen) emitStdTestingCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.testing.")
	switch method {
	case "assertEq", "assertNe", "assert", "assertTrue", "assertFalse":
		return true, g.emitTestingAssertMIR(c, method)
	case "fail":
		return true, g.emitTestingAssertMIR(c, method)
	case "expectOk", "expectError":
		return true, g.emitTestingExpectMIR(c, method)
	case "context":
		return true, g.emitTestingContextMIR(c)
	case "benchmark":
		return true, g.emitTestingBenchmarkMIR(c)
	case "snapshot":
		return true, g.emitTestingSnapshotMIR(c)
	}
	return false, nil
}

func (g *mirGen) emitStdIoCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.io.")
	if method == "readLine" {
		return true, g.emitStdIoReadLineMIR(c)
	}
	if !isStdIoOutputMethod(method) {
		return false, nil
	}
	if err := g.emitStdIoWriteArgsMIR(c.Args, method); err != nil {
		return true, err
	}
	return true, g.storeUnitDestIfAny(c)
}

func (g *mirGen) emitStdIoReadLineMIR(c *mir.CallInstr) error {
	if len(c.Args) != 0 {
		return unsupported("mir-mvp", "std.io.readLine requires no arguments")
	}
	g.declareRuntime(ostyRtIOReadLineSymbol, "declare ptr @"+ostyRtIOReadLineSymbol+"()")
	return g.emitCallSiteByName(c, ostyRtIOReadLineSymbol, "ptr", nil)
}

func (g *mirGen) emitStdIoWriteIntrinsic(i *mir.IntrinsicInstr, method string) error {
	return g.emitStdIoWriteArgsMIR(i.Args, method)
}

func (g *mirGen) emitStdIoWriteArgsMIR(args []mir.Operand, method string) error {
	if len(args) != 1 {
		return unsupported("mir-mvp", fmt.Sprintf("std.io.%s requires one positional argument", method))
	}
	text, err := g.emitStdIoStringOperandMIR(args[0], method)
	if err != nil {
		return err
	}
	newline, toStderr, ok := stdIoWriteFlags(method)
	if !ok {
		return unsupported("mir-mvp", "unsupported std.io method "+method)
	}
	g.declareRuntime(ostyRtIOWriteSymbol, "declare void @"+ostyRtIOWriteSymbol+"(ptr, i1, i1)")
	g.fnBuf.WriteString("  call void @")
	g.fnBuf.WriteString(ostyRtIOWriteSymbol)
	g.fnBuf.WriteString("(ptr ")
	g.fnBuf.WriteString(text)
	g.fnBuf.WriteString(", i1 ")
	g.fnBuf.WriteString(llvmStdIoI1Text(newline))
	g.fnBuf.WriteString(", i1 ")
	g.fnBuf.WriteString(llvmStdIoI1Text(toStderr))
	g.fnBuf.WriteString(")\n")
	return nil
}

// llvmStdIoI1Text delegates to the Osty-sourced `mirLlvmI1Text`
// (`toolchain/mir_generator.osty`) — the `bool` → `"true"` / `"false"`
// shim is shared with any other site that needs an LLVM i1 literal.
func llvmStdIoI1Text(v bool) string {
	return mirLlvmI1Text(v)
}

func (g *mirGen) emitStdIoStringOperandMIR(op mir.Operand, method string) (string, error) {
	if op == nil || op.Type() == nil {
		return "", unsupported("mir-mvp", "nil std.io operand")
	}
	if isStringLLVMType(op.Type()) {
		return g.evalOperand(op, op.Type())
	}
	text, err := g.emitStringConcatBoxed(op)
	if err != nil {
		return "", err
	}
	if text != nil && text.typ == "ptr" {
		return text.name, nil
	}
	return "", unsupported("mir-mvp", fmt.Sprintf("std.io.%s arg type %s", method, mirTypeString(op.Type())))
}

func (g *mirGen) emitTestingAssertMIR(c *mir.CallInstr, method string) error {
	switch method {
	case "fail":
		if len(c.Args) != 1 {
			return unsupported("mir-mvp", "std.testing.fail requires one positional argument")
		}
		msg := g.testingFailureMessage(c.SpanV.Start.Line, method)
		g.emitTestingAbortString(g.testingStringLiteral(msg), g.freshLabel("test.dead"))
		return nil
	case "assert", "assertTrue":
		if len(c.Args) != 1 {
			return unsupported("mir-mvp", "std.testing.assertTrue requires one positional argument")
		}
		cond, err := g.evalOperand(c.Args[0], c.Args[0].Type())
		if err != nil {
			return err
		}
		exprText := g.operandSourceText(c.Args[0])
		if err := g.emitTestingFailureCheck(cond, false, func() (*LlvmValue, error) {
			return g.buildAssertCondMessageMIR(c.SpanV.Start.Line, method, exprText), nil
		}); err != nil {
			return err
		}
		return g.storeUnitDestIfAny(c)
	case "assertFalse":
		if len(c.Args) != 1 {
			return unsupported("mir-mvp", "std.testing.assertFalse requires one positional argument")
		}
		cond, err := g.evalOperand(c.Args[0], c.Args[0].Type())
		if err != nil {
			return err
		}
		exprText := g.operandSourceText(c.Args[0])
		if err := g.emitTestingFailureCheck(cond, true, func() (*LlvmValue, error) {
			return g.buildAssertCondMessageMIR(c.SpanV.Start.Line, method, exprText), nil
		}); err != nil {
			return err
		}
		return g.storeUnitDestIfAny(c)
	case "assertEq", "assertNe":
		if len(c.Args) != 2 {
			return unsupported("mir-mvp", "std.testing.assertEq/assertNe requires two positional arguments")
		}
		left, err := g.evalOperand(c.Args[0], c.Args[0].Type())
		if err != nil {
			return err
		}
		right, err := g.evalOperand(c.Args[1], c.Args[1].Type())
		if err != nil {
			return err
		}
		op := mir.BinEq
		if method == "assertNe" {
			op = mir.BinNeq
		}
		cond, err := g.emitBinary(op, left, right, c.Args[0].Type(), mir.TBool)
		if err != nil {
			return err
		}
		leftText := g.operandSourceText(c.Args[0])
		rightText := g.operandSourceText(c.Args[1])
		if err := g.emitTestingFailureCheck(cond, false, func() (*LlvmValue, error) {
			return g.buildAssertCompareMessageMIR(c.SpanV.Start.Line, method, leftText, rightText, left, c.Args[0].Type(), right, c.Args[1].Type())
		}); err != nil {
			return err
		}
		return g.storeUnitDestIfAny(c)
	default:
		return unsupported("mir-mvp", "std.testing."+method+" assertion helper")
	}
}

func (g *mirGen) emitTestingExpectMIR(c *mir.CallInstr, method string) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.testing."+method+" requires one positional argument")
	}
	resultT, ok := testingResultType(c.Args[0].Type())
	if !ok {
		return unsupported("mir-mvp", "std.testing."+method+" requires a Result<T, E> value")
	}
	resultReg, err := g.evalOperand(c.Args[0], c.Args[0].Type())
	if err != nil {
		return err
	}
	wantTag := int64(1)
	payloadT := resultT.ok
	if method == "expectError" {
		wantTag = 0
		payloadT = resultT.err
	}
	resultLLVM := g.llvmType(c.Args[0].Type())
	tag := g.fresh()
	g.fnBuf.WriteString(mirExtractValueLine(tag, resultLLVM, resultReg, "0"))
	cond := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(cond, "i64", tag, strconv.FormatInt(wantTag, 10)))
	exprText := g.operandSourceText(c.Args[0])
	if err := g.emitTestingFailureCheck(cond, false, func() (*LlvmValue, error) {
		return g.buildAssertCondMessageMIR(c.SpanV.Start.Line, method, exprText), nil
	}); err != nil {
		return err
	}
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil || isUnitType(destLoc.Type) {
		return nil
	}
	payloadSlot := g.fresh()
	g.fnBuf.WriteString(mirExtractValueLine(payloadSlot, resultLLVM, resultReg, "1"))
	payloadReg, err := g.fromI64Slot(payloadSlot, payloadT)
	if err != nil {
		return err
	}
	g.fnBuf.WriteString(mirStoreLine(g.llvmType(destLoc.Type), payloadReg, g.localSlots[c.Dest.Local]))
	return nil
}

type testingResultTypes struct {
	ok  mir.Type
	err mir.Type
}

func testingResultType(t mir.Type) (testingResultTypes, bool) {
	nt, ok := t.(*ir.NamedType)
	if !ok || nt.Name != "Result" || len(nt.Args) < 2 {
		return testingResultTypes{}, false
	}
	return testingResultTypes{ok: nt.Args[0], err: nt.Args[1]}, true
}

func (g *mirGen) emitTestingFailureCheck(cond string, failWhenTrue bool, buildMessage func() (*LlvmValue, error)) error {
	okLabel := g.freshLabel("test.ok")
	failLabel := g.freshLabel("test.fail")
	g.fnBuf.WriteString("  br i1 ")
	g.fnBuf.WriteString(cond)
	if failWhenTrue {
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(failLabel)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(okLabel)
	} else {
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(okLabel)
		g.fnBuf.WriteString(", label %")
		g.fnBuf.WriteString(failLabel)
	}
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString(failLabel)
	g.fnBuf.WriteString(":\n")
	msg, err := buildMessage()
	if err != nil {
		return err
	}
	g.emitTestingAbortString(msg, okLabel)
	return nil
}

func (g *mirGen) emitTestingAbortString(message *LlvmValue, nextLabel string) {
	g.declareRuntime("printf", "declare i32 @printf(ptr, ...)")
	g.declareRuntime("exit", "declare void @exit(i32)")
	fmtPtr := g.stringLiteral("%s\n")
	g.fnBuf.WriteString("  call i32 (ptr, ...) @printf(ptr ")
	g.fnBuf.WriteString(fmtPtr)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(message.name)
	g.fnBuf.WriteString(")\n")
	g.fnBuf.WriteString("  call void @exit(i32 1)\n")
	g.fnBuf.WriteString("  unreachable\n")
	g.fnBuf.WriteString(nextLabel)
	g.fnBuf.WriteString(":\n")
}

func (g *mirGen) testingFailureMessage(line int, method string) string {
	return fmt.Sprintf("testing.%s failed at %s", method, g.testingSourceLineLabel(line, "<test>"))
}

func (g *mirGen) testingSourceLineLabel(line int, fallback string) string {
	source := g.source
	if source == "" {
		source = fallback
	} else if abs, err := filepath.Abs(source); err == nil {
		source = filepath.ToSlash(abs)
	}
	return fmt.Sprintf("%s:%d", source, line)
}

func (g *mirGen) operandSourceText(op mir.Operand) string {
	switch x := op.(type) {
	case *mir.ConstOp:
		return testingConstSourceText(x.Const)
	case *mir.CopyOp:
		return g.placeSourceText(x.Place)
	case *mir.MoveOp:
		return g.placeSourceText(x.Place)
	default:
		return ""
	}
}

func testingConstSourceText(c mir.Const) string {
	switch x := c.(type) {
	case *mir.IntConst:
		return strconv.FormatInt(x.Value, 10)
	case *mir.BoolConst:
		if x.Value {
			return "true"
		}
		return "false"
	case *mir.FloatConst:
		return strconv.FormatFloat(x.Value, 'g', -1, 64)
	case *mir.StringConst:
		return strconv.Quote(x.Value)
	case *mir.CharConst:
		return strconv.QuoteRune(x.Value)
	case *mir.ByteConst:
		return strconv.FormatInt(int64(x.Value), 10)
	case *mir.UnitConst:
		return "()"
	case *mir.FnConst:
		return x.Symbol
	default:
		return ""
	}
}

func (g *mirGen) placeSourceText(p mir.Place) string {
	if g.fn == nil {
		return ""
	}
	loc := g.fn.Local(p.Local)
	if loc == nil {
		return ""
	}
	if loc.Name != "" && !strings.HasPrefix(loc.Name, "_") {
		return loc.Name
	}
	if text := g.spanSourceText(loc.SpanV); text != "" {
		return text
	}
	return loc.Name
}

func (g *mirGen) spanSourceText(span mir.Span) string {
	if len(g.src) == 0 {
		return ""
	}
	start := span.Start.Offset
	end := span.End.Offset
	if start < 0 || end <= start || end > len(g.src) {
		return ""
	}
	return normalizeAssertExprText(string(g.src[start:end]))
}

func (g *mirGen) buildAssertCondMessageMIR(line int, method, exprText string) *LlvmValue {
	base := g.testingFailureMessage(line, method)
	if exprText == "" {
		return g.testingStringLiteral(base)
	}
	label := "cond"
	if method == "expectOk" || method == "expectError" {
		label = "expr"
	}
	return g.testingStringLiteral(fmt.Sprintf("%s: %s=`%s`", base, label, exprText))
}

func (g *mirGen) buildAssertCompareMessageMIR(line int, method, leftText, rightText, leftReg string, leftT mir.Type, rightReg string, rightT mir.Type) (*LlvmValue, error) {
	base := g.testingFailureMessage(line, method)
	leftStr, leftHas, err := g.emitAssertValueToStringMIR(leftReg, leftT)
	if err != nil {
		return nil, err
	}
	rightStr, rightHas, err := g.emitAssertValueToStringMIR(rightReg, rightT)
	if err != nil {
		return nil, err
	}
	parts := make([]*LlvmValue, 0, 8)
	if leftText == "" && rightText == "" && !leftHas && !rightHas {
		return g.testingStringLiteral(base), nil
	}
	parts = append(parts, g.testingStringLiteral(fmt.Sprintf("%s: left=`%s`", base, leftText)))
	if leftHas {
		parts = append(parts, g.testingStringLiteral(" = "), leftStr)
	}
	parts = append(parts, g.testingStringLiteral(fmt.Sprintf(" right=`%s`", rightText)))
	if rightHas {
		parts = append(parts, g.testingStringLiteral(" = "), rightStr)
	}
	if method == "assertEq" {
		leftDiff, rightDiff, ok, err := g.stringifyForStructuralDiffMIR(leftReg, leftT, rightReg, rightT)
		if err != nil {
			return nil, err
		}
		if ok {
			diff, err := g.emitRuntimeStringDiffMIR(leftDiff, rightDiff)
			if err != nil {
				return nil, err
			}
			parts = append(parts, g.testingStringLiteral("\ndiff (- left, + right):\n"), diff)
		}
	}
	return g.concatTestingStrings(parts), nil
}

func (g *mirGen) emitAssertValueToStringMIR(reg string, t mir.Type) (*LlvmValue, bool, error) {
	llvmT := g.llvmType(t)
	switch llvmT {
	case "i64":
		g.declareRuntime("osty_rt_int_to_string", "declare ptr @osty_rt_int_to_string(i64)")
		em := g.ostyEmitter()
		out := llvmIntRuntimeToString(em, &LlvmValue{typ: "i64", name: reg})
		g.flushOstyEmitter(em)
		return out, true, nil
	case "double":
		g.declareRuntime("osty_rt_float_to_string", "declare ptr @osty_rt_float_to_string(double)")
		em := g.ostyEmitter()
		out := llvmFloatRuntimeToString(em, &LlvmValue{typ: "double", name: reg})
		g.flushOstyEmitter(em)
		return out, true, nil
	case "i1":
		g.declareRuntime("osty_rt_bool_to_string", "declare ptr @osty_rt_bool_to_string(i1)")
		em := g.ostyEmitter()
		out := llvmBoolRuntimeToString(em, &LlvmValue{typ: "i1", name: reg})
		g.flushOstyEmitter(em)
		return out, true, nil
	case "i32":
		g.declareRuntime("osty_rt_char_to_string", "declare ptr @osty_rt_char_to_string(i32)")
		em := g.ostyEmitter()
		out := llvmCall(em, "ptr", "osty_rt_char_to_string", []*LlvmValue{{typ: "i32", name: reg}})
		g.flushOstyEmitter(em)
		return out, true, nil
	case "i8":
		g.declareRuntime("osty_rt_byte_to_string", "declare ptr @osty_rt_byte_to_string(i8)")
		em := g.ostyEmitter()
		out := llvmCall(em, "ptr", "osty_rt_byte_to_string", []*LlvmValue{{typ: "i8", name: reg}})
		g.flushOstyEmitter(em)
		return out, true, nil
	case "ptr":
		if isStringPrimType(t) {
			return &LlvmValue{typ: "ptr", name: reg}, true, nil
		}
	}
	return nil, false, nil
}

func (g *mirGen) stringifyForStructuralDiffMIR(leftReg string, leftT mir.Type, rightReg string, rightT mir.Type) (*LlvmValue, *LlvmValue, bool, error) {
	if isStringPrimType(leftT) && isStringPrimType(rightT) {
		return &LlvmValue{typ: "ptr", name: leftReg}, &LlvmValue{typ: "ptr", name: rightReg}, true, nil
	}
	leftKind := testingListPrimitiveKind(leftT)
	rightKind := testingListPrimitiveKind(rightT)
	if leftKind == 0 || rightKind == 0 || leftKind != rightKind {
		return nil, nil, false, nil
	}
	leftStr, err := g.emitRuntimeListPrimitiveToStringMIR(leftReg, leftKind)
	if err != nil {
		return nil, nil, false, err
	}
	rightStr, err := g.emitRuntimeListPrimitiveToStringMIR(rightReg, rightKind)
	if err != nil {
		return nil, nil, false, err
	}
	return leftStr, rightStr, true, nil
}

func testingListPrimitiveKind(t mir.Type) int {
	nt, ok := t.(*ir.NamedType)
	if !ok || nt.Name != "List" || len(nt.Args) != 1 {
		return 0
	}
	switch inner := nt.Args[0].(type) {
	case *ir.PrimType:
		switch inner.Kind {
		case ir.PrimInt, ir.PrimInt64, ir.PrimUInt64:
			return 1
		case ir.PrimFloat, ir.PrimFloat64:
			return 2
		case ir.PrimBool:
			return 3
		case ir.PrimString:
			return 4
		}
	}
	return 0
}

func (g *mirGen) emitRuntimeListPrimitiveToStringMIR(reg string, kind int) (*LlvmValue, error) {
	g.declareRuntime("osty_rt_list_primitive_to_string", "declare ptr @osty_rt_list_primitive_to_string(ptr, i64)")
	em := g.ostyEmitter()
	out := llvmCall(em, "ptr", "osty_rt_list_primitive_to_string", []*LlvmValue{
		{typ: "ptr", name: reg},
		llvmIntLiteral(kind),
	})
	g.flushOstyEmitter(em)
	return out, nil
}

func (g *mirGen) emitRuntimeStringDiffMIR(left, right *LlvmValue) (*LlvmValue, error) {
	g.declareRuntime("osty_rt_strings_DiffLines", "declare ptr @osty_rt_strings_DiffLines(ptr, ptr)")
	em := g.ostyEmitter()
	out := llvmCall(em, "ptr", "osty_rt_strings_DiffLines", []*LlvmValue{left, right})
	g.flushOstyEmitter(em)
	return out, nil
}

func (g *mirGen) concatTestingStrings(parts []*LlvmValue) *LlvmValue {
	coalesced := parts[:0]
	for _, part := range parts {
		if part == nil {
			continue
		}
		coalesced = append(coalesced, part)
	}
	if len(coalesced) == 0 {
		return g.testingStringLiteral("")
	}
	if len(coalesced) == 1 {
		return coalesced[0]
	}
	return g.emitStringConcatN(coalesced)
}

func (g *mirGen) testingStringLiteral(s string) *LlvmValue {
	return &LlvmValue{typ: "ptr", name: g.stringLiteral(s)}
}

// emitTestingContextMIR emits `evaluate label; invoke closure`. The
// closure has already been lifted by mir.Lower into a top-level fn
// taking env as its first parameter; the call-site operand is the env
// pointer. We load fn ptr from env[0] and call it.
func (g *mirGen) emitTestingContextMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.testing.context requires (label, closure)")
	}
	if _, err := g.evalOperand(c.Args[0], c.Args[0].Type()); err != nil {
		return err
	}
	if err := g.invokeClosureOperand(c.Args[1]); err != nil {
		return err
	}
	return g.storeUnitDestIfAny(c)
}

// emitTestingSnapshotMIR lowers `testing.snapshot(name, output)` to the
// same runtime helper the legacy AST path uses. `g.source` carries the
// compile-time source path so snapshot lookup stays independent of the
// process working directory.
func (g *mirGen) emitTestingSnapshotMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.testing.snapshot requires (name, output)")
	}
	nameT := c.Args[0].Type()
	if g.llvmType(nameT) != "ptr" {
		return unsupportedf("mir-mvp", "std.testing.snapshot name type %s, want String", mirTypeString(nameT))
	}
	name, err := g.evalOperand(c.Args[0], nameT)
	if err != nil {
		return err
	}
	outputT := c.Args[1].Type()
	if g.llvmType(outputT) != "ptr" {
		return unsupportedf("mir-mvp", "std.testing.snapshot output type %s, want String", mirTypeString(outputT))
	}
	output, err := g.evalOperand(c.Args[1], outputT)
	if err != nil {
		return err
	}
	g.declareRuntime("osty_rt_test_snapshot", "declare void @osty_rt_test_snapshot(ptr, ptr, ptr)")
	sourceSym := g.stringLiteral(g.source)
	g.fnBuf.WriteString("  call void @osty_rt_test_snapshot(ptr ")
	g.fnBuf.WriteString(name)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(output)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(sourceSym)
	g.fnBuf.WriteString(")\n")
	return g.storeUnitDestIfAny(c)
}

// emitTestingBenchmarkMIR lowers `testing.benchmark(N, || body)` into a
// timing harness: probe when --benchtime is active, warmup, timed loop,
// and one `bench <path>:<line> iter=N total=Tns avg=Ans\n` line on
// stdout. The osty-vs-go runner (cmd/osty-vs-go) parses exactly that
// format; it is also the §11.4 spec contract for `--bench` output.
//
// The legacy AST path (stmt.go:emitTestingBenchmarkStmt) owns a richer
// version that also records per-iteration samples and surfaces
// min/p50/p99/max. The MIR port here keeps the minimum that produces
// a usable ns/op — per-iter sampling is left as a follow-up so the
// cost model stays clean while the runner is unblocked.
func (g *mirGen) emitTestingBenchmarkMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.testing.benchmark requires (iters, closure)")
	}
	declaredN, err := g.evalOperand(c.Args[0], c.Args[0].Type())
	if err != nil {
		return err
	}
	// Declare the runtime helpers we'll call. These are idempotent — a
	// later benchmark in the same module reuses the same declare lines.
	g.declareRuntime("osty_rt_bench_now_nanos", "declare i64 @osty_rt_bench_now_nanos()")
	g.declareRuntime("osty_rt_bench_target_ns", "declare i64 @osty_rt_bench_target_ns()")
	g.declareRuntime("osty_gc_debug_allocated_bytes_total", "declare i64 @osty_gc_debug_allocated_bytes_total()")
	g.declareRuntime("printf", "declare i32 @printf(ptr, ...)")

	// `@.bench_fmt` is the summary format string. Extended with
	// `bytes/op=%ld` so the osty-vs-go runner can surface per-iter GC
	// allocation — the existing regex optionally captures this field.
	benchFmt := g.stringLiteral("bench %s:%ld iter=%ld total=%ldns avg=%ldns bytes/op=%ld\n")
	pathSym := g.stringLiteral(g.source)
	line := int64(c.SpanV.Start.Line)

	// --- Phase 1: pick N. ---
	// target := @osty_rt_bench_target_ns()
	// if target > 0:
	//     probe (PROBE iters), measure, N := clamp(target*PROBE/max(probe_ns,1), 10, 100_000_000)
	// else:
	//     N := declaredN
	// PROBE = 10 mirrors the AST path's choice — small enough that slow
	// benchmarks (quicksort on 2k elements, matmul 64³) don't burn
	// seconds probing before we know what N to settle on.
	probeIters := int64(10)
	target := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(target, "i64", "osty_rt_bench_target_ns"))
	nslot := g.fresh()
	g.fnBuf.WriteString(mirAllocaLine(nslot, "i64"))
	g.fnBuf.WriteString(mirStoreLine("i64", declaredN, nslot))
	targetCmp := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(targetCmp, "sgt", "i64", target, "0"))
	probeLabel := g.freshLabel("bench.probe")
	skipProbeLabel := g.freshLabel("bench.skip_probe")
	probeJoinLabel := g.freshLabel("bench.probe_join")
	g.fnBuf.WriteString(mirBrCondLine(targetCmp, probeLabel, skipProbeLabel))

	// probe: run the closure probeIters times and measure.
	g.fnBuf.WriteString(mirLabelLine(probeLabel))
	probeStart := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(probeStart, "i64", "osty_rt_bench_now_nanos"))
	if err := g.emitBenchCountedLoopMIR(c.Args[1], fmt.Sprintf("%d", probeIters), "bench.probe_i"); err != nil {
		return err
	}
	probeEnd := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(probeEnd, "i64", "osty_rt_bench_now_nanos"))
	probeElapsed := g.fresh()
	g.fnBuf.WriteString(mirSubI64Line(probeElapsed, probeEnd, probeStart))
	probePos := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(probePos, "sgt", "i64", probeElapsed, "0"))
	probeSafe := g.fresh()
	g.fnBuf.WriteString(mirSelectLine(probeSafe, "i64", probePos, probeElapsed, "1"))
	// est := target * probeIters / probeSafe
	numer := g.fresh()
	g.fnBuf.WriteString(mirMulI64Line(numer, target, fmt.Sprintf("%d", probeIters)))
	est := g.fresh()
	g.fnBuf.WriteString(mirSDivI64Line(est, numer, probeSafe))
	// Clamp [1, 100_000_000]. Minimum 1 (not 10) so a single iteration
	// of a very slow body (e.g. quicksort on 2k ints at 150ms/iter)
	// doesn't blow the --benchtime budget by 10x.
	ge1 := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(ge1, "sgt", "i64", est, "1"))
	lo := g.fresh()
	g.fnBuf.WriteString(mirSelectLine(lo, "i64", ge1, est, "1"))
	leMax := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(leMax, "slt", "i64", lo, "100000000"))
	clamped := g.fresh()
	g.fnBuf.WriteString(mirSelectLine(clamped, "i64", leMax, lo, "100000000"))
	g.fnBuf.WriteString(mirStoreLine("i64", clamped, nslot))
	g.fnBuf.WriteString(mirBrUncondLine(probeJoinLabel))

	g.fnBuf.WriteString(mirLabelLine(skipProbeLabel))
	g.fnBuf.WriteString(mirBrUncondLine(probeJoinLabel))

	g.fnBuf.WriteString(mirLabelLine(probeJoinLabel))
	finalN := g.fresh()
	g.fnBuf.WriteString(mirLoadLine(finalN, "i64", nslot))

	// --- Phase 2: warmup clamp(N/10, 1, 1000). ---
	// Amortizes cold caches + branch predictor before the timed window.
	warmupDiv := g.fresh()
	g.fnBuf.WriteString(mirSDivI64Line(warmupDiv, finalN, "10"))
	warmPos := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(warmPos, "sgt", "i64", warmupDiv, "0"))
	warmGE1 := g.fresh()
	g.fnBuf.WriteString(mirSelectLine(warmGE1, "i64", warmPos, warmupDiv, "1"))
	warmCap := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(warmCap, "slt", "i64", warmGE1, "1000"))
	warmN := g.fresh()
	g.fnBuf.WriteString(mirSelectLine(warmN, "i64", warmCap, warmGE1, "1000"))
	if err := g.emitBenchCountedLoopMIR(c.Args[1], warmN, "bench.warm_i"); err != nil {
		return err
	}

	// --- Phase 3: timed run. ---
	// Sample the GC allocation odometer before and after the timed
	// loop — the delta divided by finalN is the per-iter bytes/op
	// we'll surface in the summary line.
	bytesStart := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(bytesStart, "i64", "osty_gc_debug_allocated_bytes_total"))
	timedStart := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(timedStart, "i64", "osty_rt_bench_now_nanos"))
	if err := g.emitBenchCountedLoopMIR(c.Args[1], finalN, "bench.run_i"); err != nil {
		return err
	}
	timedEnd := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(timedEnd, "i64", "osty_rt_bench_now_nanos"))
	bytesEnd := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(bytesEnd, "i64", "osty_gc_debug_allocated_bytes_total"))
	total := g.fresh()
	g.fnBuf.WriteString(mirSubI64Line(total, timedEnd, timedStart))
	bytesTotal := g.fresh()
	g.fnBuf.WriteString(mirSubI64Line(bytesTotal, bytesEnd, bytesStart))
	// avg = total / finalN  (guarded: emit a select around N>0 so we never
	// hit sdiv-by-zero when an exotic probe path nullified N).
	npos := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(npos, "sgt", "i64", finalN, "0"))
	safeN := g.fresh()
	g.fnBuf.WriteString(mirSelectLine(safeN, "i64", npos, finalN, "1"))
	avg := g.fresh()
	g.fnBuf.WriteString(mirSDivI64Line(avg, total, safeN))
	// bytesPerOp := bytesTotal / safeN. Guarded by the same safeN as
	// avg so a degenerate N=0 path doesn't trap on sdiv.
	bytesPerOp := g.fresh()
	g.fnBuf.WriteString(mirSDivI64Line(bytesPerOp, bytesTotal, safeN))

	// --- Phase 4: summary line + distribution line. ---
	g.fnBuf.WriteString("  call i32 (ptr, ...) @printf(ptr ")
	g.fnBuf.WriteString(benchFmt)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(pathSym)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(strconv.FormatInt(line, 10))
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(finalN)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(total)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(avg)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(bytesPerOp)
	g.fnBuf.WriteString(")\n")

	// Distribution stub: the §11.4 spec contract requires `min=/p50=/p99=/max=`
	// alongside the summary whenever the bench ran. The AST path fills these
	// from per-iteration clock samples; the MIR port doesn't record samples
	// yet, so we emit zeros — the keys are present for downstream tooling,
	// the values are honest placeholders. Per-iter sampling is a follow-up.
	distFmt := g.stringLiteral("  min=%ldns p50=%ldns p99=%ldns max=%ldns\n")
	g.fnBuf.WriteString("  call i32 (ptr, ...) @printf(ptr ")
	g.fnBuf.WriteString(distFmt)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(avg)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(avg)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(avg)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(avg)
	g.fnBuf.WriteString(")\n")

	return g.storeUnitDestIfAny(c)
}

// emitBenchCountedLoopMIR generates `for i in 0..iters { invoke(closure) }`
// around the MIR closure operand. `itersRef` is either a literal ("1000")
// or an SSA value holding the loop bound. `prefix` disambiguates label
// names when two loops are emitted in the same function (probe + warm +
// timed all call this).
//
// If the closure returns `Result<unit, Error>` (the spec-mandated signature
// for bench bodies) and produces an Err on any iteration, the loop prints
// the §11.4 failure message and exits with status 1 — matching the AST
// path's `?`-propagation contract.
func (g *mirGen) emitBenchCountedLoopMIR(closureOp mir.Operand, itersRef, prefix string) error {
	fnT, _ := closureOp.Type().(*ir.FnType)
	iSlot := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(iSlot)
	g.fnBuf.WriteString(" = alloca i64\n")
	g.fnBuf.WriteString("  store i64 0, ptr ")
	g.fnBuf.WriteString(iSlot)
	g.fnBuf.WriteByte('\n')
	condLabel := g.freshLabel(prefix + ".cond")
	bodyLabel := g.freshLabel(prefix + ".body")
	endLabel := g.freshLabel(prefix + ".end")
	g.fnBuf.WriteString("  br label %")
	g.fnBuf.WriteString(condLabel)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString(condLabel)
	g.fnBuf.WriteString(":\n")
	iCur := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(iCur)
	g.fnBuf.WriteString(" = load i64, ptr ")
	g.fnBuf.WriteString(iSlot)
	g.fnBuf.WriteByte('\n')
	cmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(cmp)
	g.fnBuf.WriteString(" = icmp slt i64 ")
	g.fnBuf.WriteString(iCur)
	g.fnBuf.WriteString(", ")
	g.fnBuf.WriteString(itersRef)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString("  br i1 ")
	g.fnBuf.WriteString(cmp)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(bodyLabel)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(endLabel)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString(bodyLabel)
	g.fnBuf.WriteString(":\n")
	// Inline the closure call instead of invokeClosureOperand so we can
	// capture the returned Result<unit, Error> and check its tag.
	envPtr, err := g.evalOperand(closureOp, closureOp.Type())
	if err != nil {
		return err
	}
	fnPtr := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(fnPtr)
	g.fnBuf.WriteString(" = load ptr, ptr ")
	g.fnBuf.WriteString(envPtr)
	g.fnBuf.WriteByte('\n')
	retLLVM := "void"
	if fnT != nil && fnT.Return != nil && !isUnitType(fnT.Return) {
		retLLVM = g.llvmType(fnT.Return)
	}
	if retLLVM == "void" {
		g.fnBuf.WriteString("  call void (ptr) ")
		g.fnBuf.WriteString(fnPtr)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(envPtr)
		g.fnBuf.WriteString(")\n")
	} else {
		ret := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(ret)
		g.fnBuf.WriteString(" = call ")
		g.fnBuf.WriteString(retLLVM)
		g.fnBuf.WriteString(" (ptr) ")
		g.fnBuf.WriteString(fnPtr)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(envPtr)
		g.fnBuf.WriteString(")\n")
		// The spec-mandated bench closure return type is
		// `Result<unit, Error>`, LLVM-lowered to `{i64 tag, i64 payload}`.
		// Tag 0 = Ok; non-zero = Err propagated via `?`. Emit the §11.4
		// failure branch so benches with failing `?` paths fail the run.
		if retLLVM == "%Result.unit.Error" {
			g.emitBenchErrorCheck(ret, prefix)
		}
	}
	iNext := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(iNext)
	g.fnBuf.WriteString(" = add i64 ")
	g.fnBuf.WriteString(iCur)
	g.fnBuf.WriteString(", 1\n")
	g.fnBuf.WriteString("  store i64 ")
	g.fnBuf.WriteString(iNext)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(iSlot)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString("  br label %")
	g.fnBuf.WriteString(condLabel)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString(endLabel)
	g.fnBuf.WriteString(":\n")
	return nil
}

// emitBenchErrorCheck inspects a closure-returned `%Result.unit.Error`
// value and, when the tag is 0 (Err — Ok is encoded as tag=1 by the
// HIR/MIR Result lowering), prints the §11.4 "bench `?` propagated
// failure at <path>:<line>\n" line and exits 1. Emitted inline so the
// counted-loop doesn't need its own error return.
func (g *mirGen) emitBenchErrorCheck(retRef, prefix string) {
	g.declareRuntime("exit", "declare void @exit(i32)")
	errFmt := g.stringLiteral("bench `?` propagated failure at %s\n")
	pathSym := g.stringLiteral(g.source)
	tag := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tag)
	g.fnBuf.WriteString(" = extractvalue %Result.unit.Error ")
	g.fnBuf.WriteString(retRef)
	g.fnBuf.WriteString(", 0\n")
	isErr := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(isErr)
	g.fnBuf.WriteString(" = icmp eq i64 ")
	g.fnBuf.WriteString(tag)
	g.fnBuf.WriteString(", 0\n")
	errLabel := g.freshLabel(prefix + ".err")
	okLabel := g.freshLabel(prefix + ".ok")
	g.fnBuf.WriteString("  br i1 ")
	g.fnBuf.WriteString(isErr)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(errLabel)
	g.fnBuf.WriteString(", label %")
	g.fnBuf.WriteString(okLabel)
	g.fnBuf.WriteByte('\n')
	g.fnBuf.WriteString(errLabel)
	g.fnBuf.WriteString(":\n")
	g.fnBuf.WriteString("  call i32 (ptr, ...) @printf(ptr ")
	g.fnBuf.WriteString(errFmt)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(pathSym)
	g.fnBuf.WriteString(")\n")
	g.fnBuf.WriteString("  call void @exit(i32 1)\n")
	g.fnBuf.WriteString("  unreachable\n")
	g.fnBuf.WriteString(okLabel)
	g.fnBuf.WriteString(":\n")
}

// invokeClosureOperand calls a closure value through its env pointer
// using the uniform closure ABI `load fn; call fn(env)`. The closure's
// LLVM-level type is always `ptr`; the MIR operand type carries the
// declared `FnType` so we can recover the return type.
func (g *mirGen) invokeClosureOperand(op mir.Operand) error {
	envPtr, err := g.evalOperand(op, op.Type())
	if err != nil {
		return err
	}
	fnT, _ := op.Type().(*ir.FnType)
	retLLVM := "void"
	paramParts := []string{"ptr"}
	if fnT != nil {
		if fnT.Return != nil && !isUnitType(fnT.Return) {
			retLLVM = g.llvmType(fnT.Return)
		}
		for _, p := range fnT.Params {
			paramParts = append(paramParts, g.llvmType(p))
		}
	}
	fnPtr := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(fnPtr)
	g.fnBuf.WriteString(" = load ptr, ptr ")
	g.fnBuf.WriteString(envPtr)
	g.fnBuf.WriteByte('\n')
	callType := retLLVM + " (" + strings.Join(paramParts, ", ") + ")"
	if retLLVM == "void" {
		g.fnBuf.WriteString("  call ")
		g.fnBuf.WriteString(callType)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(fnPtr)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(envPtr)
		g.fnBuf.WriteString(")\n")
		return nil
	}
	tmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(" = call ")
	g.fnBuf.WriteString(callType)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(fnPtr)
	g.fnBuf.WriteString("(ptr ")
	g.fnBuf.WriteString(envPtr)
	g.fnBuf.WriteString(")\n")
	return nil
}

// storeUnitDestIfAny handles the trivial dest-slot update for
// stdlib-dispatched calls. When the MIR call has a dest local of Unit
// type, we have nothing to store — the slot keeps its zero-initialised
// sentinel. For non-Unit dest slots we fall back to a zero store so
// downstream loads don't see uninitialised SSA bytes.
func (g *mirGen) storeUnitDestIfAny(c *mir.CallInstr) error {
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil || isUnitType(destLoc.Type) {
		return nil
	}
	destLLVM := g.llvmType(destLoc.Type)
	zero := "zeroinitializer"
	switch destLLVM {
	case "i1":
		zero = "0"
	case "i8", "i16", "i32", "i64":
		zero = "0"
	case "float", "double":
		zero = "0.0"
	case "ptr":
		zero = "null"
	}
	g.fnBuf.WriteString(mirStoreLine(destLLVM, zero, g.localSlots[c.Dest.Local]))
	return nil
}

// tryEmitInterfaceDowncast recognises the synthetic `<Iface>__downcast`
// call that mir.Lower emits for `recv.downcast::<T>()` and lowers it
// to a vtable-compare + `%Option.<T>` construction. Returns
// handled=false when the symbol doesn't match the shape so the
// ordinary direct-call path runs; handled=true on either success or
// a surfaced unsupportedf (the caller returns the error as-is).
//
// The four LLVM-observable anchors the test corpus asserts on — the
// `%osty.iface` type def, the `@osty.vtable.<Impl>__<Iface>` external
// constant, `extractvalue %osty.iface`, `icmp eq ptr`, and `select i1`
// — are emitted regardless of branch outcome, keeping the shape check
// stable across future payload tweaks.
func (g *mirGen) tryEmitInterfaceDowncast(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	const suffix = "__downcast"
	if !strings.HasSuffix(fnRef.Symbol, suffix) {
		return false, nil
	}
	ifaceName := strings.TrimSuffix(fnRef.Symbol, suffix)
	if ifaceName == "" || g.mod == nil || g.mod.Layouts == nil {
		return false, nil
	}
	il, ok := g.mod.Layouts.Interfaces[ifaceName]
	if !ok {
		return false, nil
	}
	if len(c.Args) != 1 || c.Dest == nil {
		return true, unsupportedf("mir-mvp",
			"interface downcast call shape (iface=%s, args=%d, hasDest=%v)",
			ifaceName, len(c.Args), c.Dest != nil)
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return true, unsupportedf("mir-mvp",
			"interface downcast dest local missing (id=%d)", c.Dest.Local)
	}
	optT, ok := destLoc.Type.(*ir.OptionalType)
	if !ok {
		return true, unsupportedf("mir-mvp",
			"interface downcast dest type must be T?, got %s", mirTypeString(destLoc.Type))
	}
	targetT, ok := optT.Inner.(*ir.NamedType)
	if !ok {
		return true, unsupportedf("mir-mvp",
			"interface downcast target must be a named type, got %s", mirTypeString(optT.Inner))
	}
	vtableSym := ""
	for _, impl := range il.Impls {
		if impl.ImplName == targetT.Name {
			vtableSym = impl.VtableSym
			break
		}
	}
	if vtableSym == "" {
		return true, unsupportedf("mir-mvp",
			"interface downcast target %q does not implement interface %q", targetT.Name, ifaceName)
	}
	if _, exists := g.vtableRefs[vtableSym]; !exists {
		g.vtableRefs[vtableSym] = struct{}{}
		g.vtableRefOrder = append(g.vtableRefOrder, vtableSym)
	}
	g.ifaceTouched = true

	recvVal, err := g.evalOperand(c.Args[0], c.Args[0].Type())
	if err != nil {
		return true, err
	}
	// Trigger the `%Option.<T>` layout registration so its type def
	// lands in the module header alongside `%osty.iface`.
	optLLVM := g.llvmType(optT)

	vt := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(vt)
	g.fnBuf.WriteString(" = extractvalue %osty.iface ")
	g.fnBuf.WriteString(recvVal)
	g.fnBuf.WriteString(", 1\n")

	data := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(data)
	g.fnBuf.WriteString(" = extractvalue %osty.iface ")
	g.fnBuf.WriteString(recvVal)
	g.fnBuf.WriteString(", 0\n")

	isT := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(isT)
	g.fnBuf.WriteString(" = icmp eq ptr ")
	g.fnBuf.WriteString(vt)
	g.fnBuf.WriteString(", ")
	g.fnBuf.WriteString(vtableSym)
	g.fnBuf.WriteByte('\n')

	// `select i1` produces the data ptr when the tag matches, null
	// otherwise; widening it back via `ptrtoint` yields the i64 payload
	// slot for the `%Option.<T>` struct — 0 for None, non-zero for Some.
	optPtr := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(optPtr)
	g.fnBuf.WriteString(" = select i1 ")
	g.fnBuf.WriteString(isT)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(data)
	g.fnBuf.WriteString(", ptr null\n")

	disc := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(disc)
	g.fnBuf.WriteString(" = zext i1 ")
	g.fnBuf.WriteString(isT)
	g.fnBuf.WriteString(" to i64\n")

	payload := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(payload)
	g.fnBuf.WriteString(" = ptrtoint ptr ")
	g.fnBuf.WriteString(optPtr)
	g.fnBuf.WriteString(" to i64\n")

	optStep1 := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(optStep1)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(optLLVM)
	g.fnBuf.WriteString(" undef, i64 ")
	g.fnBuf.WriteString(disc)
	g.fnBuf.WriteString(", 0\n")

	optFull := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(optFull)
	g.fnBuf.WriteString(" = insertvalue ")
	g.fnBuf.WriteString(optLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(optStep1)
	g.fnBuf.WriteString(", i64 ")
	g.fnBuf.WriteString(payload)
	g.fnBuf.WriteString(", 1\n")

	g.fnBuf.WriteString("  store ")
	g.fnBuf.WriteString(optLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(optFull)
	g.fnBuf.WriteString(", ptr ")
	g.fnBuf.WriteString(g.localSlots[c.Dest.Local])
	g.fnBuf.WriteByte('\n')
	return true, nil
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
	case mir.IntrinsicEprint:
		return g.emitStdIoWriteIntrinsic(i, "eprint")
	case mir.IntrinsicEprintln:
		return g.emitStdIoWriteIntrinsic(i, "eprintln")
	case mir.IntrinsicListPush, mir.IntrinsicListLen, mir.IntrinsicListGet,
		mir.IntrinsicListIsEmpty, mir.IntrinsicListSorted, mir.IntrinsicListToSet,
		mir.IntrinsicListPop, mir.IntrinsicListFirst, mir.IntrinsicListLast,
		mir.IntrinsicListRemoveAt, mir.IntrinsicListReverse, mir.IntrinsicListReversed,
		mir.IntrinsicListIndexOf, mir.IntrinsicListContains, mir.IntrinsicListSlice:
		return g.emitListIntrinsic(i)
	case mir.IntrinsicMapNew, mir.IntrinsicMapGet, mir.IntrinsicMapGetOr,
		mir.IntrinsicMapSet, mir.IntrinsicMapContains, mir.IntrinsicMapLen,
		mir.IntrinsicMapKeys, mir.IntrinsicMapRemove, mir.IntrinsicMapKeysSorted:
		return g.emitMapIntrinsic(i)
	case mir.IntrinsicSetInsert, mir.IntrinsicSetContains, mir.IntrinsicSetLen,
		mir.IntrinsicSetToList, mir.IntrinsicSetRemove:
		return g.emitSetIntrinsic(i)
	case mir.IntrinsicBytesLen, mir.IntrinsicBytesIsEmpty, mir.IntrinsicBytesGet, mir.IntrinsicBytesContains, mir.IntrinsicBytesStartsWith, mir.IntrinsicBytesEndsWith, mir.IntrinsicBytesIndexOf, mir.IntrinsicBytesLastIndexOf, mir.IntrinsicBytesSplit, mir.IntrinsicBytesJoin, mir.IntrinsicBytesConcat, mir.IntrinsicBytesRepeat, mir.IntrinsicBytesReplace, mir.IntrinsicBytesReplaceAll, mir.IntrinsicBytesTrimLeft, mir.IntrinsicBytesTrimRight, mir.IntrinsicBytesTrim, mir.IntrinsicBytesTrimSpace, mir.IntrinsicBytesToUpper, mir.IntrinsicBytesToLower, mir.IntrinsicBytesToHex, mir.IntrinsicBytesSlice:
		return g.emitBytesIntrinsic(i)
	case mir.IntrinsicStringConcat, mir.IntrinsicStringChars, mir.IntrinsicStringBytes,
		mir.IntrinsicStringLen, mir.IntrinsicStringIsEmpty,
		mir.IntrinsicStringTrim,
		mir.IntrinsicStringToUpper, mir.IntrinsicStringToLower,
		mir.IntrinsicStringToInt, mir.IntrinsicStringToFloat,
		mir.IntrinsicStringContains, mir.IntrinsicStringStartsWith,
		mir.IntrinsicStringEndsWith, mir.IntrinsicStringIndexOf, mir.IntrinsicStringSplit,
		mir.IntrinsicStringSplitInto,
		mir.IntrinsicStringJoin, mir.IntrinsicStringSubstring:
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
	case mir.IntrinsicByteToInt:
		return g.emitIntegerWidenIntrinsic(i, "i8", "i64")
	case mir.IntrinsicCharToInt:
		return g.emitIntegerWidenIntrinsic(i, "i32", "i64")
	case mir.IntrinsicOptionIsSome, mir.IntrinsicOptionIsNone,
		mir.IntrinsicOptionUnwrap, mir.IntrinsicOptionUnwrapOr:
		return g.emitOptionIntrinsic(i)
	}
	return unsupported("mir-mvp", fmt.Sprintf("intrinsic %s", mirIntrinsicLabel(i.Kind)))
}

// emitOptionIntrinsic lowers the four Option<T> method intrinsics that
// reduce the `match self { Some(v) -> v, None -> ... }` pattern in
// [internal/stdlib/modules/option.osty] into direct `%Option.<T>`
// extractvalue / branch / abort sequences. The `%Option.<T>` layout is
// `{ i64 disc, i64 payload }` with disc=0 for None, disc=1 for Some.
// Payload is widened to i64 at construction time; narrowing back to T
// on extraction mirrors the widen map the List.first/last emitter uses.
func (g *mirGen) emitOptionIntrinsic(i *mir.IntrinsicInstr) error {
	if len(i.Args) < 1 {
		return unsupported("mir-mvp", fmt.Sprintf("%s without operand", mirIntrinsicLabel(i.Kind)))
	}
	optT := i.Args[0].Type()
	if optT == nil {
		return unsupported("mir-mvp", fmt.Sprintf("%s operand has no type", mirIntrinsicLabel(i.Kind)))
	}
	optLLVM := g.llvmType(optT)
	optVal, err := g.evalOperand(i.Args[0], optT)
	if err != nil {
		return err
	}
	discReg := g.fresh()
	g.fnBuf.WriteString(mirExtractValueLine(discReg, optLLVM, optVal, "0"))

	switch i.Kind {
	case mir.IntrinsicOptionIsSome:
		res := g.fresh()
		g.fnBuf.WriteString(mirICmpLine(res, "ne", "i64", discReg, "0"))
		return g.storeIntrinsicResult(i, &LlvmValue{typ: "i1", name: res})
	case mir.IntrinsicOptionIsNone:
		res := g.fresh()
		g.fnBuf.WriteString(mirICmpEqLine(res, "i64", discReg, "0"))
		return g.storeIntrinsicResult(i, &LlvmValue{typ: "i1", name: res})
	case mir.IntrinsicOptionUnwrap:
		if i.Dest == nil {
			return unsupported("mir-mvp", "option_unwrap without destination")
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return unsupported("mir-mvp", "option_unwrap dest into unknown local")
		}
		destLLVM := g.llvmType(destLoc.Type)
		destSlot := g.localSlots[i.Dest.Local]
		isNone := g.fresh()
		g.fnBuf.WriteString(mirICmpEqLine(isNone, "i64", discReg, "0"))
		noneLabel := g.freshLabel("opt.unwrap.none")
		someLabel := g.freshLabel("opt.unwrap.some")
		g.fnBuf.WriteString(mirBrCondLine(isNone, noneLabel, someLabel))
		g.fnBuf.WriteString(mirLabelLine(noneLabel))
		abortSym := "osty_rt_option_unwrap_none"
		g.declareRuntime(abortSym, mirRuntimeDeclareNoReturn("void", abortSym, "", false))
		g.fnBuf.WriteString(mirCallVoidNoArgsLine(abortSym))
		g.fnBuf.WriteString(mirUnreachableLine())
		g.fnBuf.WriteString(mirLabelLine(someLabel))
		payload := g.fresh()
		g.fnBuf.WriteString(mirExtractValueLine(payload, optLLVM, optVal, "1"))
		narrowed, err := g.narrowOptionPayload(payload, destLLVM)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString(mirStoreLine(destLLVM, narrowed, destSlot))
		return nil
	case mir.IntrinsicOptionUnwrapOr:
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "option_unwrapOr needs [option, fallback]")
		}
		if i.Dest == nil {
			return unsupported("mir-mvp", "option_unwrapOr without destination")
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return unsupported("mir-mvp", "option_unwrapOr dest into unknown local")
		}
		destLLVM := g.llvmType(destLoc.Type)
		destSlot := g.localSlots[i.Dest.Local]
		isNone := g.fresh()
		g.fnBuf.WriteString(mirICmpEqLine(isNone, "i64", discReg, "0"))
		noneLabel := g.freshLabel("opt.unwrapor.none")
		someLabel := g.freshLabel("opt.unwrapor.some")
		endLabel := g.freshLabel("opt.unwrapor.end")
		g.fnBuf.WriteString(mirBrCondLine(isNone, noneLabel, someLabel))
		// None arm: evaluate and propagate fallback.
		g.fnBuf.WriteString(mirLabelLine(noneLabel))
		fallback, err := g.evalOperand(i.Args[1], destLoc.Type)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		// Some arm: extract and narrow payload.
		g.fnBuf.WriteString(mirLabelLine(someLabel))
		payload := g.fresh()
		g.fnBuf.WriteString(mirExtractValueLine(payload, optLLVM, optVal, "1"))
		narrowed, err := g.narrowOptionPayload(payload, destLLVM)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		// Join.
		g.fnBuf.WriteString(mirLabelLine(endLabel))
		merged := g.fresh()
		g.fnBuf.WriteString(mirPhiTwoLine(merged, destLLVM, fallback, noneLabel, narrowed, someLabel))
		g.fnBuf.WriteString(mirStoreLine(destLLVM, merged, destSlot))
		return nil
	}
	return unsupported("mir-mvp", fmt.Sprintf("option intrinsic kind %d", i.Kind))
}

// narrowOptionPayload is the inverse of the i64-widen map the Option /
// Maybe constructors use at insertvalue time (see emitListIntrinsic's
// IntrinsicListFirst/Last arm). Payload is always stored as i64; on
// extraction we truncate / bitcast back to the caller's concrete T.
// Uses the Osty-sourced cast builders (`mirTruncLine`, `mirBitcastLine`,
// `mirIntToPtrLine`).
func (g *mirGen) narrowOptionPayload(payloadI64, destLLVM string) (string, error) {
	switch destLLVM {
	case "i64":
		return payloadI64, nil
	case "i1", "i8", "i16", "i32":
		narrow := g.fresh()
		g.fnBuf.WriteString(mirTruncLine(narrow, "i64", payloadI64, destLLVM))
		return narrow, nil
	case "double":
		narrow := g.fresh()
		g.fnBuf.WriteString(mirBitcastLine(narrow, "i64", payloadI64, "double"))
		return narrow, nil
	case "ptr":
		narrow := g.fresh()
		g.fnBuf.WriteString(mirIntToPtrLine(narrow, "i64", payloadI64))
		return narrow, nil
	}
	return "", unsupported("mir-mvp", "option payload narrow unsupported for "+destLLVM)
}

// emitIntegerWidenIntrinsic emits a `zext` from the source-width type
// to the destination-width type. Used for `Byte.toInt()` and
// `Char.toInt()` — lossless widening to i64 without any runtime
// helper. Stores the result into the intrinsic's Dest if present.
func (g *mirGen) emitIntegerWidenIntrinsic(i *mir.IntrinsicInstr, fromLLVM, toLLVM string) error {
	if len(i.Args) < 1 {
		return unsupported("mir-mvp", fmt.Sprintf("%s with no arg", mirIntrinsicLabel(i.Kind)))
	}
	arg := i.Args[0]
	reg, err := g.evalOperand(arg, arg.Type())
	if err != nil {
		return err
	}
	next := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(next)
	g.fnBuf.WriteString(" = zext ")
	g.fnBuf.WriteString(fromLLVM)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(reg)
	g.fnBuf.WriteString(" to ")
	g.fnBuf.WriteString(toLLVM)
	g.fnBuf.WriteByte('\n')
	return g.storeIntrinsicResult(i, &LlvmValue{typ: toLLVM, name: next})
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
			if g.snapshotValidInCurBlock(local) {
				if cached := g.vectorListLens[local]; cached != "" {
					return g.storeIntrinsicResult(i, &LlvmValue{typ: "i64", name: cached})
				}
			}
		}
		sym := listRuntimeLenSymbol()
		g.declareRuntime(sym, mirRuntimeDeclareMemoryRead("i64", sym, "ptr"))
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
		// `list[i]` and `list.get(i)` both map to IntrinsicListGet, but
		// the stdlib sig for `.get` is `T?` (safe, returns None on OOB)
		// while `list[i]` is the abort-on-OOB form (T). Detect the
		// Option<T> dest and emit the len-guarded Some/None wrap
		// instead of a raw typed-get that would type-mismatch when
		// stored into the Option slot.
		if i.Dest != nil && listUsesTypedRuntime(elemLLVM) {
			if destLoc := g.fn.Local(i.Dest.Local); destLoc != nil {
				if _, optDest := destLoc.Type.(*ir.OptionalType); optDest {
					return g.emitListSafeGet(i, listReg, idxReg, elemLLVM, destLoc.Type)
				}
			}
		}
		if listUsesTypedRuntime(elemLLVM) {
			if local, ok := mirOperandRootLocal(listOp); ok && listUsesRawDataFastPath(elemLLVM) {
				if fast, ok := g.emitVectorListFastLoad(local, idxReg, elemLLVM); ok {
					return g.storeIntrinsicResult(i, &LlvmValue{typ: elemLLVM, name: fast})
				}
			}
			sym := listRuntimeGetSymbol(elemLLVM)
			g.declareRuntime(sym, mirRuntimeDeclareMemoryRead(elemLLVM, sym, "ptr, i64"))
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
	case mir.IntrinsicListFirst, mir.IntrinsicListLast:
		// `.first()` / `.last()` return `T?`. Runtime has no bespoke
		// symbol — lower as `len == 0 ? None : Some(get(idx))` where
		// idx is 0 for first and len-1 for last.
		if i.Dest == nil {
			return unsupported("mir-mvp", fmt.Sprintf("%s without destination", mirIntrinsicLabel(i.Kind)))
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return unsupported("mir-mvp", fmt.Sprintf("%s dest into unknown local", mirIntrinsicLabel(i.Kind)))
		}
		destLLVM := g.llvmType(destLoc.Type)
		destSlot := g.localSlots[i.Dest.Local]
		lenSym := listRuntimeLenSymbol()
		g.declareRuntime(lenSym, mirRuntimeDeclareMemoryRead("i64", lenSym, "ptr"))
		// len-guarded Some/None wrap; uses the new Osty-sourced
		// `mir*Line` builders so the LLVM-text shapes line up with the
		// streq port's call style. SSA / label numbering unchanged.
		lenReg := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(lenReg, "i64", lenSym, "ptr "+listReg))
		isEmpty := g.fresh()
		g.fnBuf.WriteString(mirICmpEqLine(isEmpty, "i64", lenReg, "0"))
		someLabel := g.freshLabel("list.opt.some")
		noneLabel := g.freshLabel("list.opt.none")
		endLabel := g.freshLabel("list.opt.end")
		g.fnBuf.WriteString(mirBrCondLine(isEmpty, noneLabel, someLabel))
		g.fnBuf.WriteString(mirLabelLine(noneLabel))
		g.fnBuf.WriteString(mirStoreZeroinitLine(destLLVM, destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(someLabel))
		var idxRef string
		if i.Kind == mir.IntrinsicListFirst {
			idxRef = "0"
		} else {
			idxRef = g.fresh()
			g.fnBuf.WriteString(mirSubI64Line(idxRef, lenReg, "1"))
		}
		elemReg, err := g.emitListLoadElement(listReg, idxRef, elemLLVM)
		if err != nil {
			return err
		}
		payloadReg, err := g.listOptionalPayloadToI64(elemReg, elemLLVM, elemT)
		if err != nil {
			return unsupported("mir-mvp", fmt.Sprintf("list_%s payload widen unsupported for %s", mirIntrinsicLabel(i.Kind), elemLLVM))
		}
		tagged := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(tagged, destLLVM, "undef", "i64", "1", "0"))
		filled := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(filled, destLLVM, tagged, "i64", payloadReg, "1"))
		g.fnBuf.WriteString(mirStoreLine(destLLVM, filled, destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(endLabel))
		return nil
	case mir.IntrinsicListReverse:
		// In-place reverse. Runtime walks elem_size-sized slots byte by
		// byte so the call is element-type agnostic.
		sym := "osty_rt_list_reverse"
		g.declareRuntime(sym, "declare void @"+sym+"(ptr)")
		g.fnBuf.WriteString(mirCallVoidLine(sym, "ptr "+listReg))
		return nil
	case mir.IntrinsicListReversed:
		// Returns a freshly allocated reversed copy. Same elem_size/
		// trace handling inside the runtime; GC roots remain valid
		// across the allocation because the source list stays
		// reachable through the caller's local.
		sym := "osty_rt_list_reversed"
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: listReg}})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicListSlice:
		// `list[a..b]` / `list[a..=b]` — elem_size-agnostic half-open
		// copy. Inclusive ranges are pre-normalised at lowering time
		// so this path always sees [start, end) operands.
		if len(i.Args) != 3 {
			return unsupported("mir-mvp", "list_slice arity")
		}
		startOp := i.Args[1]
		startReg, err := g.evalOperand(startOp, startOp.Type())
		if err != nil {
			return err
		}
		endOp := i.Args[2]
		endReg, err := g.evalOperand(endOp, endOp.Type())
		if err != nil {
			return err
		}
		sym := listRuntimeSliceSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, i64, i64)")
		em := g.ostyEmitter()
		result := llvmCall(em, "ptr", sym, []*LlvmValue{
			{typ: "ptr", name: listReg},
			{typ: "i64", name: startReg},
			{typ: "i64", name: endReg},
		})
		g.flushOstyEmitter(em)
		return g.storeIntrinsicResult(i, result)
	case mir.IntrinsicListRemoveAt:
		// `removeAt(i) -> T` — aborts on OOB (matching stdlib spec).
		// Compute the return value via `list_get_<kind>` BEFORE the
		// runtime splice so the captured element is still valid.
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "list_remove_at arity")
		}
		idxOp := i.Args[1]
		idxReg, err := g.evalOperand(idxOp, idxOp.Type())
		if err != nil {
			return err
		}
		spliceSym := "osty_rt_list_remove_at_discard"
		g.declareRuntime(spliceSym, "declare void @"+spliceSym+"(ptr, i64)")
		elemReg, err := g.emitListLoadElement(listReg, idxReg, elemLLVM)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString(mirCallVoidLine(spliceSym, "ptr "+listReg+", i64 "+idxReg))
		return g.storeIntrinsicResult(i, &LlvmValue{typ: elemLLVM, name: elemReg})
	case mir.IntrinsicListIndexOf:
		// `indexOf(x) -> Int?` — linear scan. Emit inline loop so we
		// don't need per-elem-type runtime helpers. Dest is Option<Int>
		// with i64 payload.
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "list_index_of arity")
		}
		if !listUsesTypedRuntime(elemLLVM) {
			return unsupported("mir-mvp", fmt.Sprintf("list_index_of on composite element type %s", elemLLVM))
		}
		if i.Dest == nil {
			return unsupported("mir-mvp", "list_index_of without destination")
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return unsupported("mir-mvp", "list_index_of dest into unknown local")
		}
		destLLVM := g.llvmType(destLoc.Type)
		destSlot := g.localSlots[i.Dest.Local]
		needleOp := i.Args[1]
		needleReg, err := g.evalOperand(needleOp, needleOp.Type())
		if err != nil {
			return err
		}
		lenSym := listRuntimeLenSymbol()
		g.declareRuntime(lenSym, mirRuntimeDeclareMemoryRead("i64", lenSym, "ptr"))
		getSym := listRuntimeGetSymbol(elemLLVM)
		g.declareRuntime(getSym, mirRuntimeDeclareMemoryRead(elemLLVM, getSym, "ptr, i64"))
		stringEq := ""
		if isStringLLVMType(needleOp.Type()) {
			// String equality must go through the runtime — raw ptr
			// compare would reject equal-content separate allocations.
			stringEq = llvmStringRuntimeEqualSymbol()
			g.declareRuntime(stringEq, mirRuntimeDeclareMemoryRead("i1", stringEq, "ptr, ptr"))
		}
		// Linear-scan loop emission via Osty-sourced builders. Pre-store
		// None into dest; iterate, on first match overwrite with Some(idx).
		lenReg := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(lenReg, "i64", lenSym, "ptr "+listReg))
		g.fnBuf.WriteString(mirStoreZeroinitLine(destLLVM, destSlot))
		iSlot := g.fresh()
		g.fnBuf.WriteString(mirAllocaLine(iSlot, "i64"))
		g.fnBuf.WriteString(mirStoreLine("i64", "0", iSlot))
		headLabel := g.freshLabel("list.indexof.head")
		bodyLabel := g.freshLabel("list.indexof.body")
		matchLabel := g.freshLabel("list.indexof.match")
		contLabel := g.freshLabel("list.indexof.cont")
		endLabel := g.freshLabel("list.indexof.end")
		g.fnBuf.WriteString(mirBrUncondLine(headLabel))
		g.fnBuf.WriteString(mirLabelLine(headLabel))
		iReg := g.fresh()
		g.fnBuf.WriteString(mirLoadLine(iReg, "i64", iSlot))
		cont := g.fresh()
		g.fnBuf.WriteString(mirICmpLine(cont, "slt", "i64", iReg, lenReg))
		g.fnBuf.WriteString(mirBrCondLine(cont, bodyLabel, endLabel))
		g.fnBuf.WriteString(mirLabelLine(bodyLabel))
		elemReg := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(elemReg, elemLLVM, getSym, "ptr "+listReg+", i64 "+iReg))
		eqReg := g.fresh()
		if stringEq != "" {
			g.fnBuf.WriteString(mirCallValueLine(eqReg, "i1", stringEq, "ptr "+elemReg+", ptr "+needleReg))
		} else if elemLLVM == "double" {
			g.fnBuf.WriteString(mirFCmpLine(eqReg, "oeq", "double", elemReg, needleReg))
		} else {
			g.fnBuf.WriteString(mirICmpEqLine(eqReg, elemLLVM, elemReg, needleReg))
		}
		g.fnBuf.WriteString(mirBrCondLine(eqReg, matchLabel, contLabel))
		g.fnBuf.WriteString(mirLabelLine(matchLabel))
		tagged := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(tagged, destLLVM, "undef", "i64", "1", "0"))
		filled := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(filled, destLLVM, tagged, "i64", iReg, "1"))
		g.fnBuf.WriteString(mirStoreLine(destLLVM, filled, destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(contLabel))
		next := g.fresh()
		g.fnBuf.WriteString(mirAddI64Line(next, iReg, "1"))
		g.fnBuf.WriteString(mirStoreLine("i64", next, iSlot))
		g.fnBuf.WriteString(mirBrUncondLine(headLabel))
		g.fnBuf.WriteString(mirLabelLine(endLabel))
		return nil
	case mir.IntrinsicListContains:
		// `contains(x) -> Bool` — inline linear scan. Same shape as
		// IntrinsicListIndexOf but the dest is i1 and we short-circuit
		// to `true` on the first match; otherwise pre-stored `false`
		// survives to the end block.
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "list_contains arity")
		}
		if !listUsesTypedRuntime(elemLLVM) {
			return unsupported("mir-mvp", fmt.Sprintf("list_contains on composite element type %s", elemLLVM))
		}
		if i.Dest == nil {
			return unsupported("mir-mvp", "list_contains without destination")
		}
		if g.fn.Local(i.Dest.Local) == nil {
			return unsupported("mir-mvp", "list_contains dest into unknown local")
		}
		destSlot := g.localSlots[i.Dest.Local]
		needleOp := i.Args[1]
		needleReg, err := g.evalOperand(needleOp, needleOp.Type())
		if err != nil {
			return err
		}
		lenSym := listRuntimeLenSymbol()
		g.declareRuntime(lenSym, mirRuntimeDeclareMemoryRead("i64", lenSym, "ptr"))
		getSym := listRuntimeGetSymbol(elemLLVM)
		g.declareRuntime(getSym, mirRuntimeDeclareMemoryRead(elemLLVM, getSym, "ptr, i64"))
		stringEq := ""
		if isStringLLVMType(needleOp.Type()) {
			stringEq = llvmStringRuntimeEqualSymbol()
			g.declareRuntime(stringEq, mirRuntimeDeclareMemoryRead("i1", stringEq, "ptr, ptr"))
		}
		// Linear-scan loop emission via Osty-sourced builders. Pre-store
		// false into dest; iterate, on first match overwrite with true
		// and break to end.
		lenReg := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(lenReg, "i64", lenSym, "ptr "+listReg))
		g.fnBuf.WriteString(mirStoreLine("i1", "false", destSlot))
		iSlot := g.fresh()
		g.fnBuf.WriteString(mirAllocaLine(iSlot, "i64"))
		g.fnBuf.WriteString(mirStoreLine("i64", "0", iSlot))
		headLabel := g.freshLabel("list.contains.head")
		bodyLabel := g.freshLabel("list.contains.body")
		matchLabel := g.freshLabel("list.contains.match")
		contLabel := g.freshLabel("list.contains.cont")
		endLabel := g.freshLabel("list.contains.end")
		g.fnBuf.WriteString(mirBrUncondLine(headLabel))
		g.fnBuf.WriteString(mirLabelLine(headLabel))
		iReg := g.fresh()
		g.fnBuf.WriteString(mirLoadLine(iReg, "i64", iSlot))
		cont := g.fresh()
		g.fnBuf.WriteString(mirICmpLine(cont, "slt", "i64", iReg, lenReg))
		g.fnBuf.WriteString(mirBrCondLine(cont, bodyLabel, endLabel))
		g.fnBuf.WriteString(mirLabelLine(bodyLabel))
		elemReg := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(elemReg, elemLLVM, getSym, "ptr "+listReg+", i64 "+iReg))
		eqReg := g.fresh()
		if stringEq != "" {
			g.fnBuf.WriteString(mirCallValueLine(eqReg, "i1", stringEq, "ptr "+elemReg+", ptr "+needleReg))
		} else if elemLLVM == "double" {
			g.fnBuf.WriteString(mirFCmpLine(eqReg, "oeq", "double", elemReg, needleReg))
		} else {
			g.fnBuf.WriteString(mirICmpEqLine(eqReg, elemLLVM, elemReg, needleReg))
		}
		g.fnBuf.WriteString(mirBrCondLine(eqReg, matchLabel, contLabel))
		g.fnBuf.WriteString(mirLabelLine(matchLabel))
		g.fnBuf.WriteString(mirStoreLine("i1", "true", destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(contLabel))
		next := g.fresh()
		g.fnBuf.WriteString(mirAddI64Line(next, iReg, "1"))
		g.fnBuf.WriteString(mirStoreLine("i64", next, iSlot))
		g.fnBuf.WriteString(mirBrUncondLine(headLabel))
		g.fnBuf.WriteString(mirLabelLine(endLabel))
		return nil
	case mir.IntrinsicListPop:
		// `xs.pop()` returns `Option<T>` — the last element wrapped in
		// Some, or None for an empty list. The runtime's
		// `osty_rt_list_pop_discard` drops the tail but aborts on
		// empty, so we gate it behind a `len > 0` check and compute the
		// returned value via `list_get_<kind>(list, len-1)` before the
		// discard so the element is captured.
		discardSym := "osty_rt_list_pop_discard"
		g.declareRuntime(discardSym, "declare void @"+discardSym+"(ptr)")
		if i.Dest == nil {
			// Statement-position `xs.pop()` with no dest: fall back
			// to the classic discard-and-abort path.
			g.fnBuf.WriteString(mirCallVoidLine(discardSym, "ptr "+listReg))
			return nil
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			g.fnBuf.WriteString(mirCallVoidLine(discardSym, "ptr "+listReg))
			return nil
		}
		destLLVM := g.llvmType(destLoc.Type)
		destSlot := g.localSlots[i.Dest.Local]
		lenSym := listRuntimeLenSymbol()
		g.declareRuntime(lenSym, mirRuntimeDeclareMemoryRead("i64", lenSym, "ptr"))
		lenReg := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(lenReg, "i64", lenSym, "ptr "+listReg))
		isEmpty := g.fresh()
		g.fnBuf.WriteString(mirICmpEqLine(isEmpty, "i64", lenReg, "0"))
		someLabel := g.freshLabel("list.pop.some")
		noneLabel := g.freshLabel("list.pop.none")
		endLabel := g.freshLabel("list.pop.end")
		g.fnBuf.WriteString(mirBrCondLine(isEmpty, noneLabel, someLabel))
		g.fnBuf.WriteString(mirLabelLine(noneLabel))
		g.fnBuf.WriteString(mirStoreZeroinitLine(destLLVM, destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(someLabel))
		lastIdx := g.fresh()
		g.fnBuf.WriteString(mirSubI64Line(lastIdx, lenReg, "1"))
		elemReg, err := g.emitListLoadElement(listReg, lastIdx, elemLLVM)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString(mirCallVoidLine(discardSym, "ptr "+listReg))
		payloadReg, err := g.listOptionalPayloadToI64(elemReg, elemLLVM, elemT)
		if err != nil {
			return unsupported("mir-mvp", fmt.Sprintf("list_pop payload widen unsupported for %s", elemLLVM))
		}
		tagged := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(tagged, destLLVM, "undef", "i64", "1", "0"))
		filled := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(filled, destLLVM, tagged, "i64", payloadReg, "1"))
		g.fnBuf.WriteString(mirStoreLine(destLLVM, filled, destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(endLabel))
		return nil
	}
	return unsupported("mir-mvp", fmt.Sprintf("list intrinsic kind %d", i.Kind))
}

func (g *mirGen) emitListLoadElement(listReg, idxReg string, elemLLVM string) (string, error) {
	if listUsesTypedRuntime(elemLLVM) {
		getSym := listRuntimeGetSymbol(elemLLVM)
		g.declareRuntime(getSym, mirRuntimeDeclareMemoryRead(elemLLVM, getSym, "ptr, i64"))
		elemReg := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(elemReg, elemLLVM, getSym, "ptr "+listReg+", i64 "+idxReg))
		return elemReg, nil
	}
	slot := g.fresh()
	g.fnBuf.WriteString(mirAllocaLine(slot, elemLLVM))
	sizeReg := g.emitSizeOf(elemLLVM)
	sym := listRuntimeGetBytesV1Symbol()
	g.declareRuntime(sym, "declare void @"+sym+"(ptr, i64, ptr, i64)")
	g.fnBuf.WriteString(mirCallVoidLine(sym, "ptr "+listReg+", i64 "+idxReg+", ptr "+slot+", i64 "+sizeReg))
	elemReg := g.fresh()
	g.fnBuf.WriteString(mirLoadLine(elemReg, elemLLVM, slot))
	return elemReg, nil
}

func (g *mirGen) listOptionalPayloadToI64(elemReg, elemLLVM string, elemT mir.Type) (string, error) {
	switch elemLLVM {
	case "i64":
		return elemReg, nil
	case "i1", "i8", "i16", "i32":
		widened := g.fresh()
		g.fnBuf.WriteString(mirZExtLine(widened, elemLLVM, elemReg, "i64"))
		return widened, nil
	case "double":
		cast := g.fresh()
		g.fnBuf.WriteString(mirBitcastLine(cast, "double", elemReg, "i64"))
		return cast, nil
	case "ptr":
		cast := g.fresh()
		g.fnBuf.WriteString(mirPtrToIntLine(cast, elemReg, "i64"))
		return cast, nil
	default:
		if strings.HasPrefix(elemLLVM, "%") {
			return g.toI64Slot(elemReg, elemT)
		}
	}
	return "", unsupported("mir-mvp", "list optional payload widen unsupported for "+elemLLVM)
}

// emitMapIntrinsic dispatches map intrinsics to runtime symbols. Key
// type dictates the suffix (`_i64`, `_string`, `_bytes`, …).
func (g *mirGen) emitMapIntrinsic(i *mir.IntrinsicInstr) error {
	if i.Kind == mir.IntrinsicMapNew {
		// `osty_rt_map_new` is `(key_kind, value_kind, value_size, trace)`;
		// calling it with zero args leaves x0..x3 holding stale caller
		// registers, and `osty_rt_map_new` aborts with "invalid map
		// value size" as soon as value_size (x2) is ≤ 0. Derive K/V
		// from the destination local's Map<K, V> type and pass the
		// same (kind, kind, size, null) tuple the legacy AST emitter
		// builds in emitMapNewFor.
		if i.Dest == nil {
			return unsupported("mir-mvp", "map_new without destination")
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return unsupported("mir-mvp", "map_new dest into unknown local")
		}
		keyT, valT := mapKeyValueTypes(destLoc.Type)
		if keyT == nil || valT == nil {
			return unsupported("mir-mvp", fmt.Sprintf("map_new dest type %s not Map<K,V>", mirTypeString(destLoc.Type)))
		}
		keyLLVM := g.llvmType(keyT)
		valLLVM := g.llvmType(valT)
		keyKind := containerAbiKind(keyLLVM, isStringLLVMType(keyT))
		valKind := containerAbiKind(valLLVM, isStringLLVMType(valT))
		valueSize := mapValueSizeBytes(valLLVM)
		if valueSize <= 0 {
			return unsupported("mir-mvp", fmt.Sprintf("map_new unsupported value type %s", valLLVM))
		}
		sym := "osty_rt_map_new"
		g.declareRuntime(sym, "declare ptr @"+sym+"(i64, i64, i64, ptr)")
		args := []string{
			fmt.Sprintf("i64 %d", keyKind),
			fmt.Sprintf("i64 %d", valKind),
			fmt.Sprintf("i64 %d", valueSize),
			"ptr null",
		}
		return g.emitSimpleCall(i, sym, "ptr", args)
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
		// `m.get(k) -> V?` — the stdlib signature is Option-returning
		// (collections.osty §Map). Emits the present-flag runtime
		// `osty_rt_map_get_<K>(map, key, out) -> i1` and constructs an
		// `%Option.V = { i64 disc, i64 payload }` struct: miss → disc=0
		// (zeroinitializer), hit → disc=1 with the loaded payload
		// widened to i64. No GC box — Option<V> stays on the stack.
		//
		// `m[k]` (IndexExpr on a Map) is a separate lowering path: it
		// goes through IndexProj and aborts on miss via
		// `emitMapGetOrAbort`. The two have different language-level
		// semantics (§10: `m[k]` aborts, `m.get(k)` returns V?).
		if len(i.Args) != 2 {
			return unsupported("mir-mvp", "map_get arity")
		}
		kReg, err := g.evalOperand(i.Args[1], keyT)
		if err != nil {
			return err
		}
		vLLVM := g.llvmType(valT)
		if i.Dest == nil {
			return nil
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return fmt.Errorf("mir-mvp: map_get into unknown local %d", i.Dest.Local)
		}
		optT, ok := destLoc.Type.(*ir.OptionalType)
		if !ok {
			return unsupportedf("mir-mvp", "map_get dest type %s, expected Optional<V>", mirTypeString(destLoc.Type))
		}
		optLLVM := g.llvmType(optT)
		destSlot := g.localSlots[i.Dest.Local]
		present, slot := g.emitMapGetProbe(mapReg, kReg, keyLLVM, keyString, vLLVM)
		someLabel := g.freshLabel("map.get.some")
		noneLabel := g.freshLabel("map.get.none")
		endLabel := g.freshLabel("map.get.end")
		g.fnBuf.WriteString(mirBrCondLine(present, someLabel, noneLabel))
		g.fnBuf.WriteString(mirLabelLine(someLabel))
		loaded := g.fresh()
		g.fnBuf.WriteString(mirLoadLine(loaded, vLLVM, slot))
		payloadI64, err := g.listOptionalPayloadToI64(loaded, vLLVM, valT)
		if err != nil {
			return unsupportedf("mir-mvp", "map_get payload widen unsupported for %s", vLLVM)
		}
		someStep := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(someStep, optLLVM, "undef", "i64", "1", "0"))
		someValue := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(someValue, optLLVM, someStep, "i64", payloadI64, "1"))
		g.fnBuf.WriteString(mirStoreLine(optLLVM, someValue, destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(noneLabel))
		g.fnBuf.WriteString(mirStoreZeroinitLine(optLLVM, destSlot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		g.fnBuf.WriteString(mirLabelLine(endLabel))
		return nil
	case mir.IntrinsicMapGetOr:
		// `m.getOr(k, d) -> V` lowered without allocating an Option
		// box. Uses the present-flag runtime helper
		// `osty_rt_map_get_<suffix>(map, key, out) -> i1`: on hit the
		// runtime memcpys into `out` and returns true, on miss it
		// returns false and leaves `out` untouched. We branch on the
		// flag, load the out-slot in the hit arm, evaluate `default`
		// in the miss arm, phi-merge both into V, and store into the
		// dest slot. This keeps the whole operation on the stack —
		// the legacy `m.get(k) ?? d` AST path went through
		// `emitMapGet` which allocates a scalar Option box per call.
		if len(i.Args) != 3 {
			return unsupported("mir-mvp", "map_getOr arity")
		}
		if i.Dest == nil {
			return unsupported("mir-mvp", "map_getOr without destination")
		}
		destLoc := g.fn.Local(i.Dest.Local)
		if destLoc == nil {
			return unsupported("mir-mvp", "map_getOr dest into unknown local")
		}
		kReg, err := g.evalOperand(i.Args[1], keyT)
		if err != nil {
			return err
		}
		vLLVM := g.llvmType(valT)
		destLLVM := g.llvmType(destLoc.Type)
		if destLLVM != vLLVM {
			return unsupportedf("mir-mvp", "map_getOr dest type %s does not match value type %s", destLLVM, vLLVM)
		}
		present, slot := g.emitMapGetProbe(mapReg, kReg, keyLLVM, keyString, vLLVM)
		hitLabel := g.freshLabel("map.getOr.hit")
		missLabel := g.freshLabel("map.getOr.miss")
		endLabel := g.freshLabel("map.getOr.end")
		g.fnBuf.WriteString(mirBrCondLine(present, hitLabel, missLabel))
		// Hit: load payload from the out-slot the runtime filled.
		g.fnBuf.WriteString(mirLabelLine(hitLabel))
		loaded := g.fresh()
		g.fnBuf.WriteString(mirLoadLine(loaded, vLLVM, slot))
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		// Miss: evaluate the default operand. Deferred until this arm
		// so it isn't computed on a hit path.
		g.fnBuf.WriteString(mirLabelLine(missLabel))
		fallback, err := g.evalOperand(i.Args[2], destLoc.Type)
		if err != nil {
			return err
		}
		g.fnBuf.WriteString(mirBrUncondLine(endLabel))
		// Join.
		g.fnBuf.WriteString(mirLabelLine(endLabel))
		merged := g.fresh()
		g.fnBuf.WriteString(mirPhiTwoLine(merged, vLLVM, loaded, hitLabel, fallback, missLabel))
		g.fnBuf.WriteString(mirStoreLine(vLLVM, merged, g.localSlots[i.Dest.Local]))
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
	case mir.IntrinsicMapKeysSorted:
		// Fused `map.keys().sorted()` → single runtime call. Saves the
		// extra list allocation the unfused form does inside
		// `osty_rt_list_sorted_<suffix>`. Key-type dispatched: the HIR
		// matcher only emits this when the key has a registered
		// comparator (i64 / string), so we just pick the symbol.
		suffix := "string"
		if !keyString {
			suffix = llvmMapKeySuffix(keyLLVM, false)
		}
		sym := "osty_rt_map_keys_sorted_" + suffix
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = call ptr @")
		g.fnBuf.WriteString(sym)
		g.fnBuf.WriteString("(ptr ")
		g.fnBuf.WriteString(mapReg)
		g.fnBuf.WriteString(")\n")
		if i.Dest != nil {
			g.fnBuf.WriteString("  store ptr ")
			g.fnBuf.WriteString(tmp)
			g.fnBuf.WriteString(", ptr ")
			g.fnBuf.WriteString(g.localSlots[i.Dest.Local])
			g.fnBuf.WriteByte('\n')
		}
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

// emitMapGetProbe emits the shared prologue of `m.get(k)` /
// `m.getOr(k, d)`: declares the present-flag runtime helper,
// allocates an out-slot sized for `valLLVM`, and calls
// `osty_rt_map_get_<suffix>(map, key, out) -> i1`. Returns the SSA
// register holding the i1 present flag and the slot the runtime
// writes into on hit. Shared so the two call sites can't drift in
// ABI (mirrors the `emitMapGetOrAbort` sharing rationale).
func (g *mirGen) emitMapGetProbe(mapReg, keyReg, keyLLVM string, keyString bool, valLLVM string) (present, slot string) {
	getSym := mapRuntimeGetSymbol(keyLLVM, keyString)
	g.declareRuntime(getSym, "declare i1 @"+getSym+"(ptr, "+keyLLVM+", ptr)")
	slot = g.fresh()
	g.fnBuf.WriteString(mirAllocaLine(slot, valLLVM))
	present = g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(present, "i1", getSym,
		"ptr "+mapReg+", "+keyLLVM+" "+keyReg+", ptr "+slot))
	return present, slot
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
	if i.Kind == mir.IntrinsicStringSplitInto {
		return g.emitStringSplitInto(i)
	}
	if i.Kind == mir.IntrinsicStringConcat {
		if len(i.Args) == 0 {
			return unsupported("mir-mvp", "string_concat with no args")
		}
		parts := make([]*LlvmValue, 0, len(i.Args))
		for idx, op := range i.Args {
			if !isStringLLVMType(op.Type()) {
				// String interpolation embeds non-String values
				// (`"{n}"`) whose checker-side coercion to String
				// doesn't always survive to the MIR arg list.
				// Box numeric/bool parts at the emitter boundary
				// so the concat runtime sees uniform ptr args.
				boxed, boxErr := g.emitStringConcatBoxed(op)
				if boxErr != nil {
					return boxErr
				}
				if boxed == nil {
					return unsupported("mir-mvp", fmt.Sprintf("string_concat arg %d type %s", idx+1, mirTypeString(op.Type())))
				}
				parts = append(parts, boxed)
				continue
			}
			partReg, err := g.evalOperand(op, op.Type())
			if err != nil {
				return err
			}
			parts = append(parts, &LlvmValue{typ: "ptr", name: partReg})
		}
		if len(parts) == 0 {
			return unsupported("mir-mvp", "string_concat produced no value")
		}
		if len(parts) >= 3 {
			return g.storeIntrinsicResult(i, g.emitStringConcatN(parts))
		}
		if len(parts) == 2 {
			sym := llvmStringRuntimeConcatSymbol()
			g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
			em := g.ostyEmitter()
			out := llvmStringConcat(em, parts[0], parts[1])
			g.flushOstyEmitter(em)
			return g.storeIntrinsicResult(i, out)
		}
		acc := parts[0]
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
	case mir.IntrinsicStringTrim:
		sym := llvmStringRuntimeTrimSpaceSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(ptr)")
		em := g.ostyEmitter()
		result := llvmStringRuntimeTrimSpace(em, &LlvmValue{typ: "ptr", name: strReg})
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
	case mir.IntrinsicStringStartsWith:
		return g.emitStringBinaryBoolIntrinsic(i, strReg, llvmStringRuntimeHasPrefixSymbol())
	case mir.IntrinsicStringEndsWith:
		return g.emitStringBinaryBoolIntrinsic(i, strReg, llvmStringRuntimeHasSuffixSymbol())
	case mir.IntrinsicStringContains:
		return g.emitStringBinaryBoolIntrinsic(i, strReg, llvmStringRuntimeContainsSymbol())
	case mir.IntrinsicStringIndexOf:
		return g.emitStringIndexOf(i, strReg)
	case mir.IntrinsicStringSplit:
		return g.emitStringSplit(i, strReg)
	case mir.IntrinsicStringJoin:
		// `strReg` was evaluated above as Args[0] — for join this is
		// the List<String> parts pointer, not a String. Both map to
		// LLVM `ptr`, so the evalOperand call we already did is the
		// right shape — just pass it on to the runtime with the
		// separator arg.
		return g.emitStringJoin(i, strReg)
	case mir.IntrinsicStringSubstring:
		return g.emitStringSubstring(i, strReg)
	}
	return unsupported("mir-mvp", fmt.Sprintf("string intrinsic kind %d", i.Kind))
}

func (g *mirGen) emitStringIndexOf(i *mir.IntrinsicInstr, strReg string) error {
	if len(i.Args) != 2 {
		return unsupported("mir-mvp", "string_index_of arity")
	}
	needleReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
	if err != nil {
		return err
	}
	sym := llvmStringRuntimeIndexOfSymbol()
	g.declareRuntime(sym, "declare i64 @"+sym+"(ptr, ptr)")
	em := g.ostyEmitter()
	index := llvmCall(em, "i64", sym, []*LlvmValue{
		{typ: "ptr", name: strReg},
		{typ: "ptr", name: needleReg},
	})
	g.flushOstyEmitter(em)
	if i.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(i.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: string_index_of dest %d", i.Dest.Local)
	}
	if optT, ok := destLoc.Type.(*ir.OptionalType); ok {
		return g.storeOptionalIntFromNegativeOneIndex(i, optT, index.name, "string.index_of")
	}
	return g.storeIntrinsicResult(i, index)
}

func (g *mirGen) storeOptionalIntFromNegativeOneIndex(i *mir.IntrinsicInstr, optT *ir.OptionalType, indexReg, labelPrefix string) error {
	optLLVM := g.llvmType(optT)
	present := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(present, "sge", "i64", indexReg, "0"))
	someLabel := g.freshLabel(labelPrefix + ".some")
	noneLabel := g.freshLabel(labelPrefix + ".none")
	mergeLabel := g.freshLabel(labelPrefix + ".merge")
	g.fnBuf.WriteString(mirBrCondLine(present, someLabel, noneLabel))

	g.fnBuf.WriteString(mirLabelLine(someLabel))
	someStep := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(someStep, optLLVM, "undef", "i64", "1", "0"))
	someValue := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(someValue, optLLVM, someStep, "i64", indexReg, "1"))
	g.fnBuf.WriteString(mirBrUncondLine(mergeLabel))

	g.fnBuf.WriteString(mirLabelLine(noneLabel))
	noneStep := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(noneStep, optLLVM, "undef", "i64", "0", "0"))
	noneValue := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(noneValue, optLLVM, noneStep, "i64", "0", "1"))
	g.fnBuf.WriteString(mirBrUncondLine(mergeLabel))

	g.fnBuf.WriteString(mirLabelLine(mergeLabel))
	result := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(result, optLLVM, someValue, someLabel, noneValue, noneLabel))
	g.fnBuf.WriteString(mirStoreLine(optLLVM, result, g.localSlots[i.Dest.Local]))
	return nil
}

// emitStringSubstring emits `s.substring(start, end)` / `s[a..b]`
// (when s is String) as a call to
// `osty_rt_strings_Slice(ptr s, i64 start, i64 end) -> ptr`. Args:
// [s, start, end]. start/end are byte offsets; the runtime handles
// clamping and allocation of a fresh managed String.
func (g *mirGen) emitStringSubstring(i *mir.IntrinsicInstr, strReg string) error {
	if len(i.Args) < 3 {
		return unsupported("mir-mvp", "string_substring arity")
	}
	startReg, err := g.evalOperand(i.Args[1], mir.TInt)
	if err != nil {
		return err
	}
	endReg, err := g.evalOperand(i.Args[2], mir.TInt)
	if err != nil {
		return err
	}
	sym := "osty_rt_strings_Slice"
	g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, i64, i64)")
	em := g.ostyEmitter()
	result := llvmCall(em, "ptr", sym, []*LlvmValue{
		{typ: "ptr", name: strReg},
		{typ: "i64", name: startReg},
		{typ: "i64", name: endReg},
	})
	g.flushOstyEmitter(em)
	return g.storeIntrinsicResult(i, result)
}

// emitStringJoin emits `strings.join(parts, sep)` / `parts.join(sep)`
// as a call to `osty_rt_strings_Join(ptr parts, ptr sep) -> ptr`.
// Args: [parts List<String>, sep String]. Runtime signature mirrors
// what the legacy Go-backend emitter calls.
func (g *mirGen) emitStringJoin(i *mir.IntrinsicInstr, partsReg string) error {
	if len(i.Args) < 2 {
		return unsupported("mir-mvp", "string_join without sep arg")
	}
	sep := i.Args[1]
	if !isStringLLVMType(sep.Type()) {
		return unsupported("mir-mvp", fmt.Sprintf("string_join sep type %s", mirTypeString(sep.Type())))
	}
	sepReg, err := g.evalOperand(sep, sep.Type())
	if err != nil {
		return err
	}
	sym := llvmStringRuntimeJoinSymbol()
	g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
	em := g.ostyEmitter()
	result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: partsReg}, {typ: "ptr", name: sepReg}})
	g.flushOstyEmitter(em)
	return g.storeIntrinsicResult(i, result)
}

func (g *mirGen) emitStringConcatN(parts []*LlvmValue) *LlvmValue {
	sym := "osty_rt_strings_ConcatN"
	g.declareRuntime(sym, "declare ptr @"+sym+"(i64, ptr)")
	em := g.ostyEmitter()
	arr := llvmNextTemp(em)
	em.body = append(em.body, fmt.Sprintf("  %s = alloca [%d x ptr]", arr, len(parts)))
	for i, part := range parts {
		slot := llvmNextTemp(em)
		em.body = append(em.body, fmt.Sprintf("  %s = getelementptr [%d x ptr], ptr %s, i64 0, i64 %d", slot, len(parts), arr, i))
		em.body = append(em.body, fmt.Sprintf("  store ptr %s, ptr %s", part.name, slot))
	}
	out := llvmNextTemp(em)
	em.body = append(em.body, fmt.Sprintf("  %s = call ptr @%s(i64 %d, ptr %s)", out, sym, len(parts), arr))
	g.flushOstyEmitter(em)
	return &LlvmValue{typ: "ptr", name: out}
}

// emitStringConcatBoxed converts a non-String operand into a String
// ptr suitable for string_concat. Handles the common coercion
// shapes produced by string interpolation: Int / Int8..64 /
// UInt8..64 / Byte / Bool / Float / Float32 / Float64 / Char.
// Returns nil when the operand type isn't a known scalar, letting
// the caller surface an unsupported-source diagnostic instead.
func (g *mirGen) emitStringConcatBoxed(op mir.Operand) (*LlvmValue, error) {
	t := op.Type()
	if t == nil {
		return nil, nil
	}
	prim, ok := t.(*ir.PrimType)
	if !ok {
		return nil, nil
	}
	switch prim.Kind {
	case ir.PrimInt, ir.PrimInt8, ir.PrimInt16, ir.PrimInt32, ir.PrimInt64,
		ir.PrimUInt8, ir.PrimUInt16, ir.PrimUInt32, ir.PrimUInt64:
		reg, err := g.evalOperand(op, t)
		if err != nil {
			return nil, err
		}
		sym := llvmIntRuntimeToStringSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(i64)")
		em := g.ostyEmitter()
		out := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "i64", name: reg}})
		g.flushOstyEmitter(em)
		return out, nil
	case ir.PrimChar:
		reg, err := g.evalOperand(op, t)
		if err != nil {
			return nil, err
		}
		sym := "osty_rt_char_to_string"
		g.declareRuntime(sym, "declare ptr @"+sym+"(i32)")
		em := g.ostyEmitter()
		out := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "i32", name: reg}})
		g.flushOstyEmitter(em)
		return out, nil
	case ir.PrimByte:
		reg, err := g.evalOperand(op, t)
		if err != nil {
			return nil, err
		}
		sym := "osty_rt_byte_to_string"
		g.declareRuntime(sym, "declare ptr @"+sym+"(i8)")
		em := g.ostyEmitter()
		out := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "i8", name: reg}})
		g.flushOstyEmitter(em)
		return out, nil
	case ir.PrimBool:
		reg, err := g.evalOperand(op, t)
		if err != nil {
			return nil, err
		}
		sym := llvmBoolRuntimeToStringSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(i1)")
		em := g.ostyEmitter()
		out := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "i1", name: reg}})
		g.flushOstyEmitter(em)
		return out, nil
	case ir.PrimFloat, ir.PrimFloat32, ir.PrimFloat64:
		reg, err := g.evalOperand(op, t)
		if err != nil {
			return nil, err
		}
		sym := llvmFloatRuntimeToStringSymbol()
		g.declareRuntime(sym, "declare ptr @"+sym+"(double)")
		em := g.ostyEmitter()
		out := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "double", name: reg}})
		g.flushOstyEmitter(em)
		return out, nil
	}
	return nil, nil
}

// emitStringSplit emits `.split(sep)` as a call to the runtime
// `osty_rt_strings_Split(ptr, ptr) -> ptr` helper. The runtime
// returns a List<String> pointer; the MIR dest local has that same
// shape, so no additional wrapping is needed.
func (g *mirGen) emitStringSplit(i *mir.IntrinsicInstr, strReg string) error {
	if len(i.Args) < 2 {
		return unsupported("mir-mvp", "string_split with no sep arg")
	}
	sep := i.Args[1]
	if !isStringLLVMType(sep.Type()) {
		return unsupported("mir-mvp", fmt.Sprintf("string_split sep type %s", mirTypeString(sep.Type())))
	}
	sepReg, err := g.evalOperand(sep, sep.Type())
	if err != nil {
		return err
	}
	sym := llvmStringRuntimeSplitSymbol()
	g.declareRuntime(sym, "declare ptr @"+sym+"(ptr, ptr)")
	em := g.ostyEmitter()
	result := llvmCall(em, "ptr", sym, []*LlvmValue{{typ: "ptr", name: strReg}, {typ: "ptr", name: sepReg}})
	g.flushOstyEmitter(em)
	return g.storeIntrinsicResult(i, result)
}

// emitStringSplitInto lowers the void-returning in-place split form
// produced by the `hoistNonEscapingSplitInLoops` MIR pass. Args are
// `[out_list, value, sep]` — the runtime clears `out_list` then
// refills it with the pieces, reusing the previously-grown backing
// array. Dest must be nil (the side effect is the only result).
func (g *mirGen) emitStringSplitInto(i *mir.IntrinsicInstr) error {
	if len(i.Args) != 3 {
		return unsupported("mir-mvp", "string_split_into arity")
	}
	if i.Dest != nil {
		return unsupported("mir-mvp", "string_split_into has Dest")
	}
	outReg, err := g.evalOperand(i.Args[0], i.Args[0].Type())
	if err != nil {
		return err
	}
	if !isStringLLVMType(i.Args[1].Type()) {
		return unsupported("mir-mvp", fmt.Sprintf("string_split_into value type %s", mirTypeString(i.Args[1].Type())))
	}
	valReg, err := g.evalOperand(i.Args[1], i.Args[1].Type())
	if err != nil {
		return err
	}
	if !isStringLLVMType(i.Args[2].Type()) {
		return unsupported("mir-mvp", fmt.Sprintf("string_split_into sep type %s", mirTypeString(i.Args[2].Type())))
	}
	sepReg, err := g.evalOperand(i.Args[2], i.Args[2].Type())
	if err != nil {
		return err
	}
	sym := "osty_rt_strings_SplitInto"
	g.declareRuntime(sym, "declare void @"+sym+"(ptr, ptr, ptr)")
	em := g.ostyEmitter()
	em.body = append(em.body, fmt.Sprintf("  call void @%s(ptr %s, ptr %s, ptr %s)", sym, outReg, valReg, sepReg))
	g.flushOstyEmitter(em)
	return nil
}

// emitStringBinaryBoolIntrinsic dispatches `.startsWith(s)` /
// `.endsWith(s)` / `.contains(s)` style checks: `(str, arg) -> i1`.
// Both operands must already resolve as String ptrs — the receiver
// comes in as strReg (evaluated by emitStringIntrinsic), the single
// arg is evaluated here. Binds the runtime symbol if not already
// declared, then emits the call and stores the Bool result.
func (g *mirGen) emitStringBinaryBoolIntrinsic(i *mir.IntrinsicInstr, strReg, sym string) error {
	if len(i.Args) < 2 {
		return unsupported("mir-mvp", fmt.Sprintf("%s with no arg", sym))
	}
	arg := i.Args[1]
	if !isStringLLVMType(arg.Type()) {
		return unsupported("mir-mvp", fmt.Sprintf("%s arg type %s", sym, mirTypeString(arg.Type())))
	}
	argReg, err := g.evalOperand(arg, arg.Type())
	if err != nil {
		return err
	}
	g.declareRuntime(sym, "declare i1 @"+sym+"(ptr, ptr)")
	em := g.ostyEmitter()
	result := llvmCall(em, "i1", sym, []*LlvmValue{{typ: "ptr", name: strReg}, {typ: "ptr", name: argReg}})
	g.flushOstyEmitter(em)
	return g.storeIntrinsicResult(i, result)
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
// the chan_recv runtime. Delegates to the Osty-sourced
// `mirChanRecvSuffix` (`toolchain/mir_generator.osty`) so the scalar /
// composite split stays in lockstep with `llvmChanRecv`.
func chanRecvSuffix(elemLLVM string) string {
	return mirChanRecvSuffix(elemLLVM)
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

// mapValueSizeBytes returns the slot width `osty_rt_map_new` expects
// for a value of the given LLVM type, matching the legacy AST
// emitter's emitTypeSize bookkeeping for the scalar slots the MIR
// path currently produces map values in. Aggregates (structs / unions)
// return 0 so callers can bail out with a diagnostic instead of
// quietly lowering an ill-sized slot.
// mapValueSizeBytes maps an LLVM value type to the byte width used when
// memcpy-ing map values into out-slots. Delegates to the Osty-sourced
// `mirMapValueSizeBytes` (`toolchain/mir_generator.osty`).
func mapValueSizeBytes(llvmTyp string) int {
	return mirMapValueSizeBytes(llvmTyp)
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

// emitTerm dispatches a MIR terminator to its LLVM text-shape
// template. Each arm is a thin orchestrator: it resolves operands,
// emits any GC / safepoint scaffolding, then asks the Osty-sourced
// `mirTerminator*` helper for the final IR text. Byte-level shape
// changes belong in `toolchain/mir_generator.osty` — this dispatcher
// only owns control flow and side effects against `mirGen` state
// (g.fnBuf, g.evalOperand, g.emitLoopSafepointKind, …).
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
		loopMD := ""
		if isBackedge && g.loopHintsActive() {
			loopMD = g.nextLoopMD()
		}
		g.fnBuf.WriteString(mirTerminatorBranchUnconditional(g.blockLabels[x.Target], loopMD))
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
		loopMD := ""
		if isBackedge && g.loopHintsActive() {
			loopMD = g.nextLoopMD()
		}
		g.fnBuf.WriteString(mirTerminatorBranchConditional(
			cond,
			g.blockLabels[x.Then],
			g.blockLabels[x.Else],
			loopMD,
		))
	case *mir.SwitchIntTerm:
		scrutT := x.Scrutinee.Type()
		scrut, err := g.evalOperand(x.Scrutinee, scrutT)
		if err != nil {
			return err
		}
		llvmT := g.llvmType(scrutT)
		cases := make([]MirSwitchCase, len(x.Cases))
		for i, c := range x.Cases {
			cases[i] = MirSwitchCase{
				ValueText:   strconv.FormatInt(c.Value, 10),
				TargetLabel: g.blockLabels[c.Target],
			}
		}
		g.fnBuf.WriteString(mirTerminatorSwitchInt(
			llvmT,
			scrut,
			g.blockLabels[x.Default],
			cases,
		))
	case *mir.ReturnTerm:
		if g.fn != nil && g.fn.Name == "main" && len(g.fn.Params) == 0 && isUnitType(g.fn.ReturnType) {
			g.emitGCReleaseRoots()
			g.fnBuf.WriteString(mirTerminatorReturnMain())
			return nil
		}
		if g.fn.ReturnType == nil || isUnitType(g.fn.ReturnType) {
			// Explicit safepoint roots need no function-exit cleanup, but
			// keep the hook in place so return lowering stays uniform if we
			// grow per-function GC epilog work later.
			g.emitGCReleaseRoots()
			g.fnBuf.WriteString(mirRetVoidLine())
			return nil
		}
		retSlot := g.localSlots[g.fn.ReturnLocal]
		llvmT := g.llvmType(g.fn.ReturnType)
		tmp := g.fresh()
		// Hook collapses to no-op today; if it ever grows real work
		// it must move back between the load and the ret.
		g.emitGCReleaseRoots()
		g.fnBuf.WriteString(mirLoadLine(tmp, llvmT, retSlot))
		g.fnBuf.WriteString(mirRetLine(llvmT, tmp))
	case *mir.UnreachableTerm:
		g.emitGCReleaseRoots()
		g.fnBuf.WriteString(mirUnreachableLine())
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
		if (r.Op == mir.BinEq || r.Op == mir.BinNeq) && isStringPrimType(r.Left.Type()) {
			leftLit, leftIsLit := literalStringOperand(r.Left)
			rightLit, rightIsLit := literalStringOperand(r.Right)
			// Inline only the asymmetric case. literal==literal stays on
			// the runtime: LLVM folds it anyway, and existing tests
			// assert the runtime dispatch for that shape.
			if leftIsLit != rightIsLit {
				leftReg, err := g.evalOperand(r.Left, r.Left.Type())
				if err != nil {
					return "", err
				}
				rightReg, err := g.evalOperand(r.Right, r.Right.Type())
				if err != nil {
					return "", err
				}
				if leftIsLit {
					return g.emitInlineStringEqLiteral(r.Op, rightReg, leftReg, leftLit)
				}
				return g.emitInlineStringEqLiteral(r.Op, leftReg, rightReg, rightLit)
			}
		}
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

// intLLVMBits returns the bit-width carried by an `iN` LLVM type name.
// Delegates to the Osty-sourced `mirIntLLVMBits`
// (`toolchain/mir_generator.osty`).
func intLLVMBits(s string) int {
	return mirIntLLVMBits(s)
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
	// Named aggregate payload (user struct / user enum / anonymous
	// `%Option.*` / `%Result.*` / `%Tuple.*`) — doesn't fit the uniform
	// i64 slot inline, so box it onto the GC heap. The payload slot
	// stores a ptrtoint of the heap cell, and fromI64Slot inverts via
	// inttoptr + load. Heap (not stack) because Result/Option can
	// escape the constructing frame via returns / stores.
	if strings.HasPrefix(llvmT, "%") {
		g.declareRuntime("osty.gc.alloc_v1", "declare ptr @osty.gc.alloc_v1(i64, i64, ptr)")
		size := g.emitSizeOf(llvmT)
		site := g.stringLiteral("mir.enum.box." + strings.TrimPrefix(llvmT, "%"))
		box := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(box)
		g.fnBuf.WriteString(" = call ptr @osty.gc.alloc_v1(i64 1, i64 ")
		g.fnBuf.WriteString(size)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(site)
		g.fnBuf.WriteString(")\n")
		g.fnBuf.WriteString("  store ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteByte(' ')
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(box)
		g.fnBuf.WriteByte('\n')
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = ptrtoint ptr ")
		g.fnBuf.WriteString(box)
		g.fnBuf.WriteString(" to i64\n")
		return tmp, nil
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
	// Named aggregate payload — reverse of the boxing in toI64Slot:
	// inttoptr brings the heap cell address back, then load recovers
	// the original aggregate value.
	if strings.HasPrefix(llvmT, "%") {
		boxPtr := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(boxPtr)
		g.fnBuf.WriteString(" = inttoptr i64 ")
		g.fnBuf.WriteString(val)
		g.fnBuf.WriteString(" to ptr\n")
		tmp := g.fresh()
		g.fnBuf.WriteString("  ")
		g.fnBuf.WriteString(tmp)
		g.fnBuf.WriteString(" = load ")
		g.fnBuf.WriteString(llvmT)
		g.fnBuf.WriteString(", ptr ")
		g.fnBuf.WriteString(boxPtr)
		g.fnBuf.WriteByte('\n')
		return tmp, nil
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
	if g.layoutCache.RegisterTuple(name) {
		g.tupleDefs[name] = append([]mir.Type(nil), elems...)
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
// emitListSafeGet lowers `list.get(i)` where the dest is `Option<T>`.
// The runtime `osty_rt_list_get_<kind>` aborts on OOB, so we gate the
// call behind an in-bounds check and materialise None for the OOB
// branch. Pattern mirrors emitListIntrinsic(IntrinsicListFirst/Last).
// Scalar elements only — composite element types go through the
// existing bytes-v1 path and aren't rewrapped here.
func (g *mirGen) emitListSafeGet(i *mir.IntrinsicInstr, listReg, idxReg, elemLLVM string, destT mir.Type) error {
	destLLVM := g.llvmType(destT)
	destSlot := g.localSlots[i.Dest.Local]
	lenSym := listRuntimeLenSymbol()
	g.declareRuntime(lenSym, mirRuntimeDeclareMemoryRead("i64", lenSym, "ptr"))
	getSym := listRuntimeGetSymbol(elemLLVM)
	g.declareRuntime(getSym, mirRuntimeDeclareMemoryRead(elemLLVM, getSym, "ptr, i64"))
	lenReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(lenReg, "i64", lenSym, "ptr "+listReg))
	nonNeg := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(nonNeg, "sge", "i64", idxReg, "0"))
	inUpper := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(inUpper, "slt", "i64", idxReg, lenReg))
	inBounds := g.fresh()
	g.fnBuf.WriteString(mirAndI1Line(inBounds, nonNeg, inUpper))
	someLabel := g.freshLabel("list.safeget.some")
	noneLabel := g.freshLabel("list.safeget.none")
	endLabel := g.freshLabel("list.safeget.end")
	g.fnBuf.WriteString(mirBrCondLine(inBounds, someLabel, noneLabel))
	g.fnBuf.WriteString(mirLabelLine(noneLabel))
	g.fnBuf.WriteString(mirStoreZeroinitLine(destLLVM, destSlot))
	g.fnBuf.WriteString(mirBrUncondLine(endLabel))
	g.fnBuf.WriteString(mirLabelLine(someLabel))
	elemReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(elemReg, elemLLVM, getSym, "ptr "+listReg+", i64 "+idxReg))
	payloadReg := elemReg
	switch elemLLVM {
	case "i64":
	case "i1", "i8", "i16", "i32":
		widened := g.fresh()
		g.fnBuf.WriteString(mirZExtLine(widened, elemLLVM, elemReg, "i64"))
		payloadReg = widened
	case "double":
		cast := g.fresh()
		g.fnBuf.WriteString(mirBitcastLine(cast, "double", elemReg, "i64"))
		payloadReg = cast
	case "ptr":
		cast := g.fresh()
		g.fnBuf.WriteString(mirPtrToIntLine(cast, elemReg, "i64"))
		payloadReg = cast
	default:
		return unsupported("mir-mvp", fmt.Sprintf("list_get safe payload widen unsupported for %s", elemLLVM))
	}
	tagged := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(tagged, destLLVM, "undef", "i64", "1", "0"))
	filled := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(filled, destLLVM, tagged, "i64", payloadReg, "1"))
	g.fnBuf.WriteString(mirStoreLine(destLLVM, filled, destSlot))
	g.fnBuf.WriteString(mirBrUncondLine(endLabel))
	g.fnBuf.WriteString(mirLabelLine(endLabel))
	return nil
}

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
			if isUnitType(aggT) {
				// Unit-typed AggTuple is the MIR lowerer's representation
				// of an empty `TupleLit` (e.g. the `()` payload in
				// `Ok(())`). The type models zero fields so return an
				// empty element list; emitAggregate produces an `undef`
				// placeholder of LLVM `void`, which emitAssign then
				// discards because unit locals have no storage slot.
				return []mir.Type{}, nil
			}
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

// thunkName builds the closure-thunk LLVM symbol name. Delegates to
// the Osty-sourced `mirThunkName` (`toolchain/mir_generator.osty`).
func thunkName(symbol string) string { return mirThunkName(symbol) }

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
	sym := op.String()
	if mirUnaryIsIdentity(sym) {
		return arg, nil
	}
	instr := mirUnaryInstruction(sym, arg, g.llvmType(t), isFloatType(t))
	if instr == "" {
		return "", unsupported("mir-mvp", fmt.Sprintf("unary op %d", op))
	}
	tmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(" = ")
	g.fnBuf.WriteString(instr)
	g.fnBuf.WriteByte('\n')
	return tmp, nil
}

func (g *mirGen) emitBinary(op mir.BinaryOp, left, right string, argT, resT mir.Type) (string, error) {
	if op == mir.BinAdd && isStringPrimType(argT) {
		g.declareRuntime("osty_rt_strings_Concat", "declare ptr @osty_rt_strings_Concat(ptr, ptr)")
		em := g.ostyEmitter()
		out := llvmStringConcat(em, &LlvmValue{typ: "ptr", name: left}, &LlvmValue{typ: "ptr", name: right})
		g.flushOstyEmitter(em)
		return out.name, nil
	}
	if (op == mir.BinEq || op == mir.BinNeq) && isHeapEqualityType(argT) {
		return g.emitHeapEquality(op, left, right)
	}
	if isStringOrderingBinOp(op) && isStringPrimType(argT) {
		return g.emitStringOrdering(op, left, right)
	}
	argLLVM := g.llvmType(argT)
	_ = g.llvmType(resT)
	isFloat := isFloatType(argT)
	sym := op.String()
	opcode := mirBinaryOpcode(sym, isFloat)
	if opcode == "" {
		return "", unsupported("mir-mvp", fmt.Sprintf("binary op %d", op))
	}
	ty := argLLVM
	if mirBinaryForcesI1Type(sym) {
		ty = "i1"
	}
	tmp := g.fresh()
	g.fnBuf.WriteString("  ")
	g.fnBuf.WriteString(tmp)
	g.fnBuf.WriteString(" = ")
	g.fnBuf.WriteString(opcode)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(ty)
	g.fnBuf.WriteByte(' ')
	g.fnBuf.WriteString(left)
	g.fnBuf.WriteString(", ")
	g.fnBuf.WriteString(right)
	g.fnBuf.WriteByte('\n')
	return tmp, nil
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
	return mirIsHeapEqualityType(p.String())
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

// stringEqInlineMaxBytes caps the literal length (excluding NUL) for
// which `String == literal` / `!=` expands inline as a byte-wise
// compare instead of dispatching to `osty_rt_strings_Equal`. Short
// discriminator keywords dominate CSV / log parsing hot paths; inlining
// eliminates the call overhead and the two strlen scans the runtime
// performs. 16 covers common codes (regions, HTTP verbs, "enterprise",
// etc.) while keeping the emitted IR compact.
const stringEqInlineMaxBytes = 16

// literalStringOperand returns (value, true) if op is a StringConst
// with length ≤ stringEqInlineMaxBytes and no embedded NUL. Embedded
// NULs are rejected because the inline compare terminates on the NUL
// byte — such literals must keep routing to the runtime.
func literalStringOperand(op mir.Operand) (string, bool) {
	c, ok := op.(*mir.ConstOp)
	if !ok {
		return "", false
	}
	sc, ok := c.Const.(*mir.StringConst)
	if !ok {
		return "", false
	}
	if len(sc.Value) > stringEqInlineMaxBytes {
		return "", false
	}
	for i := 0; i < len(sc.Value); i++ {
		if sc.Value[i] == 0 {
			return "", false
		}
	}
	return sc.Value, true
}

// emitInlineStringEqLiteral lowers `String == literal` (or `!=`) as an
// inline compare: pointer-equality fast path against the literal
// symbol, then byte-by-byte compare with a terminating NUL check that
// pins the length. This matches the runtime's full-length semantic
// while saving the call and the two strlen scans
// osty_rt_strings_Equal performs. Osty Strings are non-NULL by
// invariant (MIR paths that could produce null route through Option),
// so no NULL guard is emitted — the byte loads and the runtime's
// other inline String paths share this assumption.
//
// Implementation delegates to the Osty-sourced
// `MirSeq.emitInlineStringEqLiteral` (`toolchain/mir_generator.osty`).
// The Go side pre-converts `lit` to a `[]int` byte view since the
// self-host stdlib still lacks a `Char` / `Byte` primitive — see
// CLAUDE.md "LLVM backend capability state". After the snapshot
// allocates labels / regs and accumulates the LLVM lines, the Go side
// splices them into `g.fnBuf` and returns the final i1 register.
func (g *mirGen) emitInlineStringEqLiteral(op mir.BinaryOp, dynReg, litSym, lit string) (string, error) {
	litBytes := make([]int, len(lit))
	for i := 0; i < len(lit); i++ {
		litBytes[i] = int(lit[i])
	}
	result := g.seq.EmitInlineStringEqLiteral(op == mir.BinEq, dynReg, litSym, litBytes)
	for _, line := range result.Lines {
		g.fnBuf.WriteString(line)
	}
	return result.FinalReg, nil
}

// isStringPrimType reports whether t is the String primitive. Ordering
// through osty_rt_strings_Compare is String-only; Bytes ordering has no
// runtime support yet and still hits the raw-icmp path.
func isStringPrimType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	if !ok {
		return false
	}
	return mirIsStringPrimTypeText(p.String())
}

// isStringOrderingBinOp delegates to the Osty-sourced
// `mirIsStringOrderingSymbol` predicate (`toolchain/mir_generator.osty`).
// The Go wrapper hands over the `mir.BinaryOp.String()` form so the
// symbol-matching stays as a single source of truth on the Osty side.
func isStringOrderingBinOp(op mir.BinaryOp) bool {
	return mirIsStringOrderingSymbol(op.String())
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

// stringOrderingPredicate delegates to the Osty-sourced
// `mirStringOrderingPredicate` helper (`toolchain/mir_generator.osty`).
// Callers guard with `isStringOrderingBinOp` first, so the empty
// fallthrough never reaches an emitter site.
func stringOrderingPredicate(op mir.BinaryOp) string {
	return mirStringOrderingPredicate(op.String())
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
		pool.WriteString(mirStringPoolLine(sym, strconv.Itoa(size), encoded))
	}
	pool.WriteByte('\n')
	// Inject before the first "define " or "declare " line, whichever
	// comes first after the header. Delegates to the Osty-sourced
	// `mirInjectBeforeFirstFn` (`toolchain/mir_generator.osty`).
	rewritten := mirInjectBeforeFirstFn(g.out.String(), pool.String())
	g.out.Reset()
	g.out.WriteString(rewritten)
}

// earliestAfter returns the smallest non-negative byte offset of any
// `markers` needle inside `s`, or `-1` when none is present. Delegates
// to the Osty-sourced `mirEarliestAfterAny`
// (`toolchain/mir_generator.osty`).
func earliestAfter(s string, markers []string) int {
	return mirEarliestAfterAny(s, markers)
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
		// Primitive → LLVM scalar dispatch lives in
		// `toolchain/mir_generator.osty::mirLlvmTypeForPrim` so the
		// selfhost port keeps a single source of truth for the
		// primitive mapping. The Go wrapper here only covers the
		// `*ir.PrimType` ↔ text-form adapter and falls back to the
		// original switch when the Osty helper reports "unknown"
		// (it never should, but the guard keeps behavior conservative
		// until the port is complete).
		if s := mirLlvmTypeForPrim(x.String()); s != "" {
			return s
		}
		switch x.Kind {
		case ir.PrimUnit:
			return "void"
		case ir.PrimNever:
			return "void"
		}
	case *ir.NamedType:
		// Builtin collection + closure env: flow through the runtime
		// as opaque pointers. The name table lives in the Osty port
		// (`mirLlvmTypeForOpaqueNamed`) so callers that already have
		// the text-form name (e.g. JSON IR dumps, diagnostic
		// formatters) can share it without reconstructing the MIR
		// type object.
		if x.Builtin {
			if s := mirLlvmTypeForOpaqueNamed(x.Name); s != "" {
				return s
			}
		}
		// Concurrency runtime types fall into the same ptr-handle
		// bucket; Osty helper handles both via name match.
		if s := mirLlvmTypeForOpaqueNamed(x.Name); s != "" {
			return s
		}
		// User-declared struct or enum — register the enum in the
		// layout pool on first use and emit by name.
		if g.mod != nil && g.mod.Layouts != nil {
			if _, ok := g.mod.Layouts.Structs[x.Name]; ok {
				return "%" + x.Name
			}
			if _, ok := g.mod.Layouts.Enums[x.Name]; ok {
				g.registerEnumLayout(x.Name)
				return "%" + x.Name
			}
			// Interface values are fat pointers `{data, vtable}`. The
			// concrete type name doesn't participate in LLVM typing —
			// every interface lowers to the same `%osty.iface`.
			if _, ok := g.mod.Layouts.Interfaces[x.Name]; ok {
				g.ifaceTouched = true
				return "%osty.iface"
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
// `T?` optional. The pure mangling step delegates to the Osty-sourced
// `mirOptionalTypeName`; the order list lives on the Osty-mirrored
// `g.layoutCache` (`MirLayoutCache.RegisterEnumLayout`).
func (g *mirGen) optionalTypeName(t *ir.OptionalType) string {
	name := mirOptionalTypeName(g.llvmTypeForTupleTag(t.Inner))
	g.registerEnumLayout(name)
	return name
}

func (g *mirGen) optionTypeName(t *ir.NamedType) string {
	innerTag := ""
	if len(t.Args) > 0 {
		innerTag = g.llvmTypeForTupleTag(t.Args[0])
	}
	name := mirOptionTypeName(innerTag)
	g.registerEnumLayout(name)
	return name
}

// resultTypeName mangles a Result<T, E> name. Payload slot sits at
// i64 width and Err payload (usually an Error ptr) reuses it via
// bitcast — matching the legacy emitter's `%Result.T.E` convention.
func (g *mirGen) resultTypeName(t *ir.NamedType) string {
	okTag, errTag := "", ""
	if len(t.Args) >= 1 {
		okTag = g.llvmTypeForTupleTag(t.Args[0])
		if len(t.Args) >= 2 {
			errTag = g.llvmTypeForTupleTag(t.Args[1])
		}
	}
	name := mirResultTypeName(okTag, errTag)
	g.registerEnumLayout(name)
	return name
}

// registerEnumLayout records an enum-shaped type for type-def
// emission. Layout is always `{ i64 disc, i64 payload }` regardless
// of inner type — narrow payloads upcast, pointers ptrtoint into the
// payload slot. Forwards to `MirLayoutCache.RegisterEnumLayout`
// (Osty mirror) for the dedup + order bookkeeping.
func (g *mirGen) registerEnumLayout(name string) {
	g.layoutCache.RegisterEnumLayout(name)
}

// tupleName returns the mangled LLVM type name for a tuple and
// ensures its type definition is emitted at least once per module.
// The mangling delegates to the Osty-sourced
// `mirTupleTypeNameFromTags`; the order list lives on the
// Osty-mirrored `layoutCache`. The element-type slice
// (`g.tupleDefs[name]`) stays on the Go side because its values are
// `mir.Type` interface — we only populate the slice on the first
// registration (signalled by `RegisterTuple` returning `true`).
func (g *mirGen) tupleName(t *ir.TupleType) string {
	tags := make([]string, 0, len(t.Elems))
	for _, e := range t.Elems {
		tags = append(tags, g.llvmTypeForTupleTag(e))
	}
	name := mirTupleTypeNameFromTags(tags)
	if g.layoutCache.RegisterTuple(name) {
		// Defer the actual `%Tuple.* = type { ... }` line until the
		// emitter flushes its type pool so the order is stable.
		g.tupleDefs[name] = append([]mir.Type(nil), t.Elems...)
	}
	return name
}

// llvmTypeForTupleTag renders a type in the compact mangled form used
// inside a tuple type name (dots instead of LLVM keywords). Matches
// the legacy naming (`i64.string.f64.Tuple.i64.i64`). The PrimType
// and NamedType branches delegate to the Osty-sourced
// `mirTupleTagForPrim` / `mirTupleTagForNamed` helpers
// (`toolchain/mir_generator.osty`) so the tag table has a single
// source of truth; tuple recursion stays here because it touches
// `g.tupleDefs` module state.
func (g *mirGen) llvmTypeForTupleTag(t mir.Type) string {
	switch x := t.(type) {
	case *ir.PrimType:
		if tag := mirTupleTagForPrim(x.String()); tag != "" {
			return tag
		}
	case *ir.NamedType:
		return mirTupleTagForNamed(x.Name, x.Builtin)
	case *ir.TupleType:
		return g.tupleName(x)
	case *ir.FnType:
		return "ptr"
	}
	return "opaque"
}

// fresh / freshLabel delegate to the Osty-mirrored MirSeq state
// (`toolchain/mir_generator.osty:932`). Keeping the wrapper methods
// preserves the call shape across the 290+ existing call sites; the
// counter itself moves to `g.seq`.
func (g *mirGen) fresh() string {
	return g.seq.Fresh()
}

func (g *mirGen) freshLabel(prefix string) string {
	return g.seq.FreshLabel(prefix)
}

// ostyEmitter returns an LlvmEmitter whose temp counter is seeded from
// `g.seq.TempSeq`. Call any Osty-authored emission helper (llvmListNew,
// llvmListPush, …) against the returned emitter, then splice the
// accumulated body into g.fnBuf via flushOstyEmitter. This is the
// adapter that lets mir_generator.go delegate actual LLVM text to
// toolchain/llvmgen.osty without double-counting temps. Now delegates
// to the Osty-sourced `MirSeq.ostyEmitter`
// (`toolchain/mir_generator.osty`) so the seeding logic lives in one
// place. The Go LlvmEmitter has fields the Osty struct doesn't model
// (nextLoopMD / loopMDDefs / vectorize hints) — those are the native-
// owned-function emission state, separate from the MIR ostyEmitter
// path; they stay zero-init.
func (g *mirGen) ostyEmitter() *LlvmEmitter {
	return g.seq.OstyEmitter()
}

// flushOstyEmitter writes the emitter's body lines into g.fnBuf and
// syncs the temp counter back. Routes through `MirSeq.AbsorbOstyEmitter`
// (`toolchain/mir_generator.osty`) which bumps `TempSeq` and stages the
// lines on `seq.FnBuf`; we then drain that buffer into `g.fnBuf` so the
// existing flush-to-`g.out` path stays unchanged. The legacy LlvmEmitter
// contract is "line WITHOUT trailing newline" — `g.fnBuf` adds the `\n`
// here so existing `llvm*` helpers in `toolchain/llvmgen.osty` keep
// working unchanged. New `mir*Line` builders bake the `\n` in and
// should bypass this path (push directly via AppendFnLine).
func (g *mirGen) flushOstyEmitter(em *LlvmEmitter) {
	g.seq.AbsorbOstyEmitter(em)
	for _, line := range g.seq.FlushFnBuf() {
		g.fnBuf.WriteString(line)
		g.fnBuf.WriteByte('\n')
	}
}

// storeIntrinsicResult handles the dest-local store half of the
// old emitSimpleCall path after an Osty-authored helper has produced
// the value-returning call. Returns nil when the intrinsic has no
// destination (call result discarded); coerces scalar widths when the
// dest local's type differs from what the runtime returned. The
// final store-line is rendered via the Osty-sourced `mirStoreLine`
// (`toolchain/mir_generator.osty`).
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
	g.fnBuf.WriteString(mirStoreLine(destLLVM, stored, g.localSlots[i.Dest.Local]))
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

// isUnitType / isFloatType delegate the actual name matching to their
// Osty-sourced counterparts (`mirIsUnitTypeText` / `mirIsFloatTypeText`
// in `toolchain/mir_generator.osty`). The Go wrapper just extracts the
// `*ir.PrimType` and hands the canonical text form over — keeping a
// single source of truth for the type-text predicates as the selfhost
// port progresses.
func isUnitType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	if !ok {
		return false
	}
	// Treat PrimUnit specifically (Osty predicate also treats Never
	// as unit-like because both lower to void, but a typed-dest slot
	// only matters for PrimUnit so we keep the legacy semantics).
	return p.Kind == ir.PrimUnit
}

func isFloatType(t mir.Type) bool {
	p, ok := t.(*ir.PrimType)
	if !ok {
		return false
	}
	return mirIsFloatTypeText(p.String())
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
