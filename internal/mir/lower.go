package mir

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ir"
)

// Lower converts a monomorphic HIR module into a MIR module. The
// caller is expected to have already run `ir.Monomorphize` on the HIR
// — `Lower` does not handle generic declarations and returns an
// unsupported issue when it encounters one.
//
// The returned *Module is always non-nil (even when the issues slice
// is non-empty). Non-fatal issues are reported via Module.Issues so
// that callers can decide whether to fall back to the HIR-based
// backend path. Fatal bugs — shape invariants the lowerer itself
// breaks — are caught by `mir.Validate` afterwards.
func Lower(mod *ir.Module) *Module {
	if mod == nil {
		return &Module{Package: "", Layouts: NewLayoutTable()}
	}
	l := &lowerer{
		src:        mod,
		out:        &Module{Package: mod.Package, SpanV: mod.SpanV, Layouts: NewLayoutTable()},
		fnSig:      map[string]*fnSignature{},
		methods:    map[string]*fnSignature{},
		enums:      map[string]*ir.EnumDecl{},
		structs:    map[string]*ir.StructDecl{},
		globals:    map[string]*ir.LetDecl{},
		useAliases: map[string]*ir.UseDecl{},
	}
	l.run()
	return l.out
}

// ==== signatures + lookup tables ====

type fnSignature struct {
	ir      *ir.FnDecl
	owner   string // "" for free fns; type name for methods
	symbol  string
	params  []*ir.Param
	retType ir.Type
}

// signatureForDirectCall returns the signature of a free fn by source
// name, or nil if not known.
func (l *lowerer) signatureForFn(name string) *fnSignature {
	return l.fnSig[name]
}

// signatureForMethod returns the signature of a type method, or nil
// when unknown.
func (l *lowerer) signatureForMethod(typeName, method string) *fnSignature {
	return l.methods[methodKey(typeName, method)]
}

func methodKey(typeName, method string) string {
	return typeName + "::" + method
}

// ==== lowerer state ====

type lowerer struct {
	src *ir.Module
	out *Module

	fnSig      map[string]*fnSignature
	methods    map[string]*fnSignature
	enums      map[string]*ir.EnumDecl
	structs    map[string]*ir.StructDecl
	globals    map[string]*ir.LetDecl
	useAliases map[string]*ir.UseDecl // alias name → use decl
	closureSeq int
}

func (l *lowerer) noteIssue(format string, args ...any) {
	l.out.Issues = append(l.out.Issues, fmt.Errorf(format, args...))
}

// run performs the two-pass lowering: collect signatures + layouts,
// then emit each function and global.
func (l *lowerer) run() {
	l.collectSignatures()
	l.buildLayouts()
	l.emitDeclarations()
	l.emitScript()
}

// ==== pass 1: signatures + layouts ====

func (l *lowerer) collectSignatures() {
	for _, d := range l.src.Decls {
		switch x := d.(type) {
		case *ir.FnDecl:
			if len(x.Generics) > 0 {
				l.noteIssue("generic free fn %q reached MIR (monomorphize first)", x.Name)
				continue
			}
			sig := &fnSignature{
				ir:      x,
				symbol:  x.Name,
				params:  x.Params,
				retType: x.Return,
			}
			l.fnSig[x.Name] = sig
		case *ir.StructDecl:
			if len(x.Generics) > 0 {
				l.noteIssue("generic struct %q reached MIR (monomorphize first)", x.Name)
				continue
			}
			l.structs[x.Name] = x
			for _, m := range x.Methods {
				sig := &fnSignature{
					ir:      m,
					owner:   x.Name,
					symbol:  mangleMethodSymbol(x.Name, m.Name),
					params:  m.Params,
					retType: m.Return,
				}
				l.methods[methodKey(x.Name, m.Name)] = sig
			}
		case *ir.EnumDecl:
			if len(x.Generics) > 0 {
				l.noteIssue("generic enum %q reached MIR (monomorphize first)", x.Name)
				continue
			}
			l.enums[x.Name] = x
			for _, m := range x.Methods {
				sig := &fnSignature{
					ir:      m,
					owner:   x.Name,
					symbol:  mangleMethodSymbol(x.Name, m.Name),
					params:  m.Params,
					retType: m.Return,
				}
				l.methods[methodKey(x.Name, m.Name)] = sig
			}
		case *ir.LetDecl:
			l.globals[x.Name] = x
		case *ir.UseDecl:
			l.emitUse(x)
		}
	}
}

func (l *lowerer) buildLayouts() {
	for name, s := range l.structs {
		sl := &StructLayout{Name: name, Mangled: name}
		for i, f := range s.Fields {
			sl.Fields = append(sl.Fields, FieldLayout{
				Index: i,
				Name:  f.Name,
				Type:  f.Type,
			})
		}
		l.out.Layouts.Structs[name] = sl
	}
	for name, e := range l.enums {
		el := &EnumLayout{
			Name:         name,
			Mangled:      name,
			Discriminant: TInt,
		}
		for i, v := range e.Variants {
			vl := VariantLayout{Index: i, Name: v.Name}
			for j, p := range v.Payload {
				vl.Payload = append(vl.Payload, FieldLayout{
					Index: j,
					Name:  fmt.Sprintf("_%d", j),
					Type:  p,
				})
			}
			el.Variants = append(el.Variants, vl)
		}
		l.out.Layouts.Enums[name] = el
	}
}

func (l *lowerer) emitUse(u *ir.UseDecl) {
	if u == nil {
		return
	}
	if u.Alias != "" {
		l.useAliases[u.Alias] = u
	}
	l.out.Uses = append(l.out.Uses, &Use{
		Path:         append([]string(nil), u.Path...),
		RawPath:      u.RawPath,
		Alias:        u.Alias,
		IsGoFFI:      u.IsGoFFI,
		IsRuntimeFFI: u.IsRuntimeFFI,
		GoPath:       u.GoPath,
		RuntimePath:  u.RuntimePath,
		SpanV:        u.SpanV,
	})
}

// ==== pass 2: emit functions ====

func (l *lowerer) emitDeclarations() {
	for _, d := range l.src.Decls {
		switch x := d.(type) {
		case *ir.FnDecl:
			if len(x.Generics) > 0 {
				continue
			}
			fn := l.lowerFunction(x, "", false)
			if fn != nil {
				l.out.Functions = append(l.out.Functions, fn)
			}
		case *ir.StructDecl:
			if len(x.Generics) > 0 {
				continue
			}
			for _, m := range x.Methods {
				if fn := l.lowerFunction(m, x.Name, true); fn != nil {
					l.out.Functions = append(l.out.Functions, fn)
				}
			}
		case *ir.EnumDecl:
			if len(x.Generics) > 0 {
				continue
			}
			for _, m := range x.Methods {
				if fn := l.lowerFunction(m, x.Name, true); fn != nil {
					l.out.Functions = append(l.out.Functions, fn)
				}
			}
		case *ir.LetDecl:
			if g := l.lowerGlobal(x); g != nil {
				l.out.Globals = append(l.out.Globals, g)
			}
		}
	}
}

func (l *lowerer) emitScript() {
	if len(l.src.Script) == 0 {
		return
	}
	// Treat script statements as the body of a synthetic main(). The
	// backend conventionally does the same thing.
	fn := l.newFunction("main", nil, TUnit, ir.Span{}, false)
	bs := newBodyState(l, fn)
	for _, s := range l.src.Script {
		bs.lowerStmt(s)
	}
	bs.finishNoReturn()
	l.out.Functions = append(l.out.Functions, fn)
}

// lowerFunction emits a MIR function from one HIR FnDecl. asMethod
// routes top-level `Self` access through the receiver parameter.
func (l *lowerer) lowerFunction(fn *ir.FnDecl, owner string, asMethod bool) *Function {
	if fn == nil {
		return nil
	}
	retT := fn.Return
	if retT == nil {
		retT = TUnit
	}
	symbol := fn.Name
	// `Receiver` is not carried through `FnDecl.Params` in the IR; the
	// owner's name + ReceiverMut flag is all that survives. Synthesise
	// a self param at position 0 for methods so `paramLocals[0]` is
	// well-defined downstream (bodyState.self points here).
	params := fn.Params
	if asMethod {
		symbol = mangleMethodSymbol(owner, fn.Name)
		selfParam := &ir.Param{
			Name:  "self",
			Type:  &ir.NamedType{Name: owner},
			SpanV: fn.SpanV,
		}
		params = append([]*ir.Param{selfParam}, fn.Params...)
	}
	out := l.newFunction(symbol, params, retT, fn.SpanV, asMethod)
	out.Exported = fn.Exported
	out.ExportSymbol = fn.ExportSymbol
	out.CABI = fn.CABI
	out.Vectorize = fn.Vectorize
	out.NoVectorize = fn.NoVectorize
	out.VectorizeWidth = fn.VectorizeWidth
	out.VectorizeScalable = fn.VectorizeScalable
	out.VectorizePredicate = fn.VectorizePredicate
	out.Parallel = fn.Parallel
	out.Unroll = fn.Unroll
	out.UnrollCount = fn.UnrollCount
	out.InlineMode = fn.InlineMode
	out.Hot = fn.Hot
	out.Cold = fn.Cold
	if len(fn.TargetFeatures) > 0 {
		out.TargetFeatures = append([]string(nil), fn.TargetFeatures...)
	}
	out.NoaliasAll = fn.NoaliasAll
	if len(fn.NoaliasParams) > 0 {
		out.NoaliasParams = append([]string(nil), fn.NoaliasParams...)
	}
	out.Pure = fn.Pure
	out.IsIntrinsic = fn.IsIntrinsic
	if fn.Body == nil {
		out.IsExternal = true
		// Strip blocks for external stubs: the validator rejects empty
		// block lists for non-external functions, and external ones
		// must have zero blocks.
		out.Blocks = nil
		out.Entry = 0
		return out
	}
	bs := newBodyState(l, out)
	if asMethod {
		bs.self = &ownerContext{typeName: owner, localID: bs.paramLocals[0]}
	}
	for _, s := range fn.Body.Stmts {
		bs.lowerStmt(s)
	}
	if fn.Body.Result != nil {
		// trailing expression — lower into the return local and return
		bs.lowerExprInto(fn.Body.Result, out.ReturnLocal, retT)
		bs.runDefers(fn.Body.SpanV)
		bs.terminate(&ReturnTerm{SpanV: fn.Body.SpanV})
		return out
	}
	bs.finishNoReturn()
	return out
}

// lowerGlobal emits a MIR global. The initialiser is a zero-arg
// function that returns the value; callers use it as they see fit
// (LLVM materialises this as a `@_init_xxx()` routine today).
func (l *lowerer) lowerGlobal(let *ir.LetDecl) *Global {
	if let == nil {
		return nil
	}
	t := let.Type
	if t == nil && let.Value != nil {
		t = let.Value.Type()
	}
	if t == nil {
		l.noteIssue("global %q: unknown type", let.Name)
		return nil
	}
	g := &Global{Name: let.Name, Type: t, Mut: let.Mut, SpanV: let.SpanV}
	if let.Value != nil {
		initName := "_init_" + let.Name
		initFn := l.newFunction(initName, nil, t, let.SpanV, false)
		bs := newBodyState(l, initFn)
		bs.lowerExprInto(let.Value, initFn.ReturnLocal, t)
		bs.terminate(&ReturnTerm{SpanV: let.SpanV})
		g.Init = initFn
	}
	return g
}

// newFunction creates a function shell, allocating the return local
// and any declared parameters. Returns ids of param locals in
// bodyState.paramLocals via the caller's access.
func (l *lowerer) newFunction(name string, params []*ir.Param, retT Type, sp Span, asMethod bool) *Function {
	fn := &Function{Name: name, ReturnType: retT, SpanV: sp}
	// _0 is always the return slot.
	fn.ReturnLocal = fn.NewLocal("_return", retT, true, sp)
	fn.Locals[fn.ReturnLocal].IsReturn = true
	// parameters become subsequent locals
	for _, p := range params {
		paramName := p.Name
		if paramName == "" {
			paramName = fmt.Sprintf("arg%d", len(fn.Params))
		}
		id := fn.NewLocal(paramName, p.Type, true, p.SpanV)
		fn.Locals[id].IsParam = true
		fn.Params = append(fn.Params, id)
	}
	// entry block
	fn.NewBlock(sp)
	fn.Entry = 0
	return fn
}

// ==== body lowering ====

// bodyState carries per-function cursor state: current block, loop
// stack, local lookup, and the closed-over function object we're
// populating.
type bodyState struct {
	l   *lowerer
	fn  *Function
	cur BlockID

	// locals maps HIR-visible names to MIR local IDs and tracks the
	// scope-owned locals that should receive StorageDead on scope exit.
	locals []scopeFrame

	// paramLocals is a cached view of fn.Params for quick access.
	paramLocals []LocalID

	// self tracks the receiver for method bodies.
	self *ownerContext

	// loopStack is a stack of break/continue destinations.
	loopStack []*loopFrame

	// deferFrames is a stack of per-scope defer bodies. Index 0 is
	// the function-level frame (seeded in newBodyState); deeper
	// frames are pushed every time the lowerer enters an inner
	// block scope (explicit Block stmt, `if`/`match` arm, loop body).
	// Each frame holds HIR blocks in *source* order; replay inlines
	// them in LIFO at the appropriate exit point.
	deferFrames [][]*ir.Block
}

type ownerContext struct {
	typeName string
	localID  LocalID
}

type scopeFrame struct {
	names  map[string]LocalID
	locals []LocalID
}

type loopFrame struct {
	breakBlock    BlockID
	continueBlock BlockID
	// deferDepth records the defer-frame depth active at the top of
	// the loop body. `break` and `continue` unwind inner frames
	// *inclusive* of this one — every iteration enters the body
	// scope fresh, so the body's defers run on both exit paths.
	deferDepth int
	// scopeDepth records the local-scope depth of the loop body so
	// break/continue can retire loop-body locals before control jumps
	// to the header/step/exit blocks.
	scopeDepth int
}

func newBodyState(l *lowerer, fn *Function) *bodyState {
	bs := &bodyState{l: l, fn: fn, cur: fn.Entry}
	bs.pushScope()
	// Function-level defer frame. Stays for the lifetime of the
	// function and is replayed on every exit edge (return, `?`
	// propagation, fall-through).
	bs.pushDeferScope()
	// seed locals from parameters.
	for i, p := range fn.Params {
		loc := fn.Locals[p]
		if loc != nil && loc.Name != "" {
			bs.locals[0].names[loc.Name] = p
		}
		bs.paramLocals = append(bs.paramLocals, p)
		_ = i
	}
	return bs
}

func (bs *bodyState) pushScope() {
	bs.locals = append(bs.locals, scopeFrame{names: map[string]LocalID{}})
}

func (bs *bodyState) popScope() {
	if len(bs.locals) == 0 {
		return
	}
	bs.emitStorageDeadForScope(&bs.locals[len(bs.locals)-1])
	bs.locals = bs.locals[:len(bs.locals)-1]
}

func (bs *bodyState) bind(name string, id LocalID) {
	if name == "" {
		return
	}
	bs.locals[len(bs.locals)-1].names[name] = id
}

func (bs *bodyState) lookup(name string) (LocalID, bool) {
	for i := len(bs.locals) - 1; i >= 0; i-- {
		if id, ok := bs.locals[i].names[name]; ok {
			return id, true
		}
	}
	return 0, false
}

func (bs *bodyState) registerScopeLocal(id LocalID) {
	if len(bs.locals) == 0 {
		return
	}
	loc := bs.fn.Local(id)
	if loc == nil || loc.IsParam || loc.IsReturn {
		return
	}
	bs.locals[len(bs.locals)-1].locals = append(bs.locals[len(bs.locals)-1].locals, id)
}

func (bs *bodyState) currentScopeDepth() int {
	return len(bs.locals) - 1
}

func (bs *bodyState) emitStorageDeadFromDepth(depth int) {
	if depth < 0 {
		depth = 0
	}
	for i := len(bs.locals) - 1; i >= depth; i-- {
		bs.emitStorageDeadForScope(&bs.locals[i])
	}
}

func (bs *bodyState) emitStorageDeadForScope(scope *scopeFrame) {
	if scope == nil || len(scope.locals) == 0 {
		return
	}
	bb := bs.currentBlock()
	if bb == nil || bb.Term != nil {
		scope.locals = nil
		return
	}
	for i := len(scope.locals) - 1; i >= 0; i-- {
		id := scope.locals[i]
		sp := Span{}
		if loc := bs.fn.Local(id); loc != nil {
			sp = loc.SpanV
		}
		bs.emit(&StorageDeadInstr{Local: id, SpanV: sp})
	}
	scope.locals = nil
}

func (bs *bodyState) newLocal(name string, t Type, mut bool, sp Span) LocalID {
	id := bs.fn.NewLocal(name, t, mut, sp)
	bs.registerScopeLocal(id)
	return id
}

// ==== cursor + block helpers ====

func (bs *bodyState) currentBlock() *BasicBlock { return bs.fn.Block(bs.cur) }

func (bs *bodyState) emit(instr Instr) {
	bb := bs.currentBlock()
	if bb == nil {
		return
	}
	if bb.Term != nil {
		// Unreachable code after a terminator — start a fresh orphan
		// block so validation still accepts the module. The new block
		// becomes the cursor.
		sp := SpanOfInstr(instr)
		next := bs.fn.NewBlock(sp)
		bs.cur = next
		bb = bs.currentBlock()
	}
	bb.Append(instr)
}

func (bs *bodyState) terminate(t Terminator) {
	bb := bs.currentBlock()
	if bb == nil {
		return
	}
	if bb.Term == nil {
		bb.SetTerminator(t)
	}
}

// switchTo installs a new cursor and returns the previous.
func (bs *bodyState) switchTo(id BlockID) BlockID {
	prev := bs.cur
	bs.cur = id
	return prev
}

// newBlock allocates a fresh block and returns its ID (does not
// switch to it).
func (bs *bodyState) newBlock(sp Span) BlockID {
	return bs.fn.NewBlock(sp)
}

// gotoBlock terminates the current block with a goto and switches to
// target.
func (bs *bodyState) gotoBlock(target BlockID, sp Span) {
	bs.terminate(&GotoTerm{Target: target, SpanV: sp})
	bs.cur = target
}

// ==== statement lowering ====

func (bs *bodyState) lowerStmt(s ir.Stmt) {
	switch x := s.(type) {
	case nil:
		return
	case *ir.Block:
		bs.lowerBlockStmt(x)
	case *ir.LetStmt:
		bs.lowerLet(x)
	case *ir.ExprStmt:
		bs.lowerExprStmt(x)
	case *ir.AssignStmt:
		bs.lowerAssign(x)
	case *ir.ReturnStmt:
		bs.lowerReturn(x)
	case *ir.BreakStmt:
		bs.lowerBreak(x)
	case *ir.ContinueStmt:
		bs.lowerContinue(x)
	case *ir.IfStmt:
		bs.lowerIfStmt(x)
	case *ir.ForStmt:
		bs.lowerForStmt(x)
	case *ir.MatchStmt:
		bs.lowerMatchStmt(x)
	case *ir.DeferStmt:
		bs.addDefer(x.Body)
	case *ir.ChanSendStmt:
		ch := bs.lowerExprAsOperand(x.Channel)
		val := bs.lowerExprAsOperand(x.Value)
		bs.emit(&IntrinsicInstr{
			Dest:  nil,
			Kind:  IntrinsicChanSend,
			Args:  []Operand{ch, val},
			SpanV: x.SpanV,
		})
	case *ir.ErrorStmt:
		bs.l.noteIssue("error stmt reached MIR: %s", x.Note)
	default:
		bs.l.noteIssue("unsupported stmt %T", s)
	}
}

func (bs *bodyState) lowerBlockStmt(b *ir.Block) {
	bs.pushScope()
	bs.pushDeferScope()
	for _, s := range b.Stmts {
		bs.lowerStmt(s)
	}
	if b.Result != nil {
		// Block used in statement position — we discard the value but
		// still need its side effects.
		_ = bs.lowerExprAsOperand(b.Result)
	}
	bs.replayTopFrame(b.SpanV)
	bs.popDeferScope()
	bs.popScope()
}

func (bs *bodyState) lowerLet(let *ir.LetStmt) {
	t := let.Type
	if t == nil && let.Value != nil {
		t = let.Value.Type()
	}
	if t == nil {
		t = ir.ErrTypeVal
	}
	switch {
	case let.Pattern != nil:
		bs.lowerLetPattern(let.Pattern, let.Value, t, let.SpanV)
	case let.Name != "":
		id := bs.newLocal(let.Name, t, let.Mut, let.SpanV)
		bs.emit(&StorageLiveInstr{Local: id, SpanV: let.SpanV})
		if let.Value != nil {
			bs.lowerExprInto(let.Value, id, t)
		}
		bs.bind(let.Name, id)
	default:
		bs.l.noteIssue("let stmt with neither name nor pattern")
	}
}

func (bs *bodyState) lowerLetPattern(pat ir.Pattern, value ir.Expr, valueType Type, sp Span) {
	scratch := bs.newLocal("_scratch", valueType, false, sp)
	bs.emit(&StorageLiveInstr{Local: scratch, SpanV: sp})
	if value != nil {
		bs.lowerExprInto(value, scratch, valueType)
	}
	scrutPlace := Place{Local: scratch}
	bs.bindPattern(pat, scrutPlace, valueType, sp)
}

// bindPattern walks an irrefutable pattern and emits Assign
// instructions that pull leaves out of scrutinee into named locals.
// For refutable patterns (reaching let bindings) this is a lowering
// bug — the checker already forbids it — but the helper still emits
// best-effort assignments for whatever bindings do appear.
func (bs *bodyState) bindPattern(pat ir.Pattern, scrutinee Place, scrutineeT Type, sp Span) {
	switch p := pat.(type) {
	case nil:
		return
	case *ir.WildPat:
		return
	case *ir.IdentPat:
		id := bs.newLocal(p.Name, scrutineeT, p.Mut, p.SpanV)
		bs.emit(&StorageLiveInstr{Local: id, SpanV: p.SpanV})
		bs.emit(&AssignInstr{
			Dest:  Place{Local: id},
			Src:   &UseRV{Op: &CopyOp{Place: scrutinee, T: scrutineeT}},
			SpanV: p.SpanV,
		})
		bs.bind(p.Name, id)
	case *ir.BindingPat:
		// Bind the whole scrutinee to the outer name, then recurse.
		id := bs.newLocal(p.Name, scrutineeT, false, p.SpanV)
		bs.emit(&StorageLiveInstr{Local: id, SpanV: p.SpanV})
		bs.emit(&AssignInstr{
			Dest:  Place{Local: id},
			Src:   &UseRV{Op: &CopyOp{Place: scrutinee, T: scrutineeT}},
			SpanV: p.SpanV,
		})
		bs.bind(p.Name, id)
		bs.bindPattern(p.Pattern, scrutinee, scrutineeT, sp)
	case *ir.TuplePat:
		elemTypes := tupleElementTypes(scrutineeT)
		for i, elem := range p.Elems {
			et := elementTypeAt(elemTypes, i)
			child := scrutinee.Project(&TupleProj{Index: i, Type: et})
			bs.bindPattern(elem, child, et, p.SpanV)
		}
	case *ir.StructPat:
		info := bs.l.structFromType(scrutineeT, p.TypeName)
		for _, f := range p.Fields {
			ft := bs.l.fieldType(info, f.Name)
			proj := &FieldProj{Index: bs.l.fieldIndex(info, f.Name), Name: f.Name, Type: ft}
			child := scrutinee.Project(proj)
			if f.Pattern != nil {
				bs.bindPattern(f.Pattern, child, ft, p.SpanV)
			} else {
				id := bs.newLocal(f.Name, ft, false, p.SpanV)
				bs.emit(&StorageLiveInstr{Local: id, SpanV: p.SpanV})
				bs.emit(&AssignInstr{
					Dest:  Place{Local: id},
					Src:   &UseRV{Op: &CopyOp{Place: child, T: ft}},
					SpanV: p.SpanV,
				})
				bs.bind(f.Name, id)
			}
		}
	case *ir.VariantPat:
		// Payload destructuring of a known variant — caller is
		// responsible for knowing the pattern matches (e.g. reachable
		// after a decision-tree branch).
		layout := bs.l.variantLayout(p.Enum, p.Variant)
		for i, arg := range p.Args {
			pt := variantPayloadType(layout, i)
			proj := &VariantProj{
				Variant:  variantIndex(layout),
				Name:     p.Variant,
				FieldIdx: i,
				Type:     pt,
			}
			child := scrutinee.Project(proj)
			bs.bindPattern(arg, child, pt, p.SpanV)
		}
	case *ir.LitPat, *ir.RangePat, *ir.OrPat:
		// Irrefutable patterns only — refutable ones are decision-tree
		// driven. Ignore silently when reached from a let.
	case *ir.ErrorPat:
		bs.l.noteIssue("error pattern reached MIR: %s", p.Note)
	default:
		bs.l.noteIssue("unsupported pattern in let: %T", pat)
	}
}

func (bs *bodyState) lowerExprStmt(e *ir.ExprStmt) {
	_ = bs.lowerExprAsOperand(e.X)
}

func (bs *bodyState) lowerAssign(a *ir.AssignStmt) {
	if len(a.Targets) == 0 {
		return
	}
	// Compound assignment (`+=`, `-=`, etc.) lowers as `x = x op rhs`
	// and then delegates to the simple path. For multi-target stmts
	// we only support the `=` op (HIR guarantees this by construction).
	if a.Op != ir.AssignEq {
		if len(a.Targets) != 1 {
			bs.l.noteIssue("compound assign requires a single target (got %d)", len(a.Targets))
			return
		}
		bs.lowerCompoundAssign(a)
		return
	}
	// Multi-target assignment: `(a, b) = (c, d)`. Lower RHS into a
	// scratch tuple, then destructure into each target via projections.
	if len(a.Targets) > 1 {
		bs.lowerMultiAssign(a)
		return
	}
	// Single-target `x = rhs`.
	dst, ok := bs.lowerExprToPlace(a.Targets[0])
	if !ok {
		bs.l.noteIssue("assign: unsupported target %T", a.Targets[0])
		return
	}
	bs.lowerExprIntoPlace(a.Value, dst, a.Value.Type())
}

// lowerCompoundAssign expands `x op= y` into `x = x op y` over a MIR
// BinaryRV. The left-hand side is read once and written once; we
// evaluate RHS into a fresh temp first so call-time side effects run
// before the implicit read of x.
func (bs *bodyState) lowerCompoundAssign(a *ir.AssignStmt) {
	target := a.Targets[0]
	dst, ok := bs.lowerExprToPlace(target)
	if !ok {
		bs.l.noteIssue("compound assign: unsupported target %T", target)
		return
	}
	op, okOp := assignOpToBinOp(a.Op)
	if !okOp {
		bs.l.noteIssue("compound assign: unknown op %d", a.Op)
		return
	}
	lhsT := target.Type()
	if lhsT == nil {
		lhsT = a.Value.Type()
	}
	rhs := bs.lowerExprAsOperand(a.Value)
	lhs := &CopyOp{Place: dst, T: lhsT}
	bs.emit(&AssignInstr{
		Dest:  dst,
		Src:   &BinaryRV{Op: op, Left: lhs, Right: rhs, T: lhsT},
		SpanV: a.SpanV,
	})
}

// lowerMultiAssign lowers `(a, b, ...) = rhs` by binding rhs into a
// scratch tuple-typed local and then assigning each target from a
// TupleProj into that scratch.
func (bs *bodyState) lowerMultiAssign(a *ir.AssignStmt) {
	rhsT := a.Value.Type()
	scratch := bs.newLocal("_mtuple", rhsT, false, a.SpanV)
	bs.emit(&StorageLiveInstr{Local: scratch, SpanV: a.SpanV})
	bs.lowerExprInto(a.Value, scratch, rhsT)
	elemTs := tupleElementTypes(rhsT)
	for i, target := range a.Targets {
		dst, ok := bs.lowerExprToPlace(target)
		if !ok {
			bs.l.noteIssue("multi-target assign: unsupported target %T", target)
			continue
		}
		et := elementTypeAt(elemTs, i)
		if et == nil {
			et = target.Type()
		}
		src := &CopyOp{
			Place: Place{Local: scratch}.Project(&TupleProj{Index: i, Type: et}),
			T:     et,
		}
		bs.emit(&AssignInstr{
			Dest:  dst,
			Src:   &UseRV{Op: src},
			SpanV: a.SpanV,
		})
	}
}

// assignOpToBinOp maps ir.AssignOp compound variants to their MIR
// BinaryOp equivalents. Returns false for AssignEq (pure assign has
// no operator) and for unknown values.
func assignOpToBinOp(op ir.AssignOp) (BinaryOp, bool) {
	switch op {
	case ir.AssignAdd:
		return BinAdd, true
	case ir.AssignSub:
		return BinSub, true
	case ir.AssignMul:
		return BinMul, true
	case ir.AssignDiv:
		return BinDiv, true
	case ir.AssignMod:
		return BinMod, true
	case ir.AssignAnd:
		return BinBitAnd, true
	case ir.AssignOr:
		return BinBitOr, true
	case ir.AssignXor:
		return BinBitXor, true
	case ir.AssignShl:
		return BinShl, true
	case ir.AssignShr:
		return BinShr, true
	}
	return BinInvalid, false
}

func (bs *bodyState) lowerReturn(r *ir.ReturnStmt) {
	if r.Value != nil {
		bs.lowerExprInto(r.Value, bs.fn.ReturnLocal, bs.fn.ReturnType)
	}
	bs.runDefers(r.SpanV)
	bs.terminate(&ReturnTerm{SpanV: r.SpanV})
}

// ==== defer scoping ====
//
// Defer is scoped to the enclosing block. Each block pushes a fresh
// defer frame on entry and pops it on exit. At every control-flow
// exit edge the lowerer inlines the relevant frames in LIFO order:
//
//   - normal fall-through at block exit: replay+pop the top frame
//   - `return` / `?` propagation / trailing-expr / implicit-unit:
//     replay every frame (function-level through current) in LIFO
//   - `break` / `continue`: replay frames down to and including the
//     enclosing loop body's frame. Continue re-enters the body, so
//     on the next iteration a fresh body frame is pushed and the
//     body's defers re-register at compile time inside the same
//     block. Statically this means the same MIR-inlined defer body
//     appears at each exit point, not once per iteration.

func (bs *bodyState) pushDeferScope() {
	bs.deferFrames = append(bs.deferFrames, nil)
}

func (bs *bodyState) popDeferScope() {
	if len(bs.deferFrames) == 0 {
		return
	}
	bs.deferFrames = bs.deferFrames[:len(bs.deferFrames)-1]
}

// addDefer registers a deferred block with the innermost frame.
func (bs *bodyState) addDefer(body *ir.Block) {
	if body == nil || len(bs.deferFrames) == 0 {
		return
	}
	top := len(bs.deferFrames) - 1
	bs.deferFrames[top] = append(bs.deferFrames[top], body)
}

// replayDefersFromDepth inlines defer frames from the top of the
// stack down to depth (inclusive). depth=0 replays everything,
// matching the behaviour expected at `return` / `?`.
func (bs *bodyState) replayDefersFromDepth(depth int, sp Span) {
	if depth < 0 {
		depth = 0
	}
	// Skip when the cursor block is already terminated — the exit
	// edge that ran the terminator has already performed its own
	// replay, and we must not emit instructions after a terminator.
	bb := bs.currentBlock()
	if bb != nil && bb.Term != nil {
		return
	}
	for i := len(bs.deferFrames) - 1; i >= depth; i-- {
		bs.replayDeferFrame(bs.deferFrames[i], sp)
	}
}

// replayTopFrame inlines only the innermost defer frame at the
// current cursor. Used for normal block fall-through, before the
// caller pops the frame.
func (bs *bodyState) replayTopFrame(sp Span) {
	if len(bs.deferFrames) == 0 {
		return
	}
	bb := bs.currentBlock()
	if bb != nil && bb.Term != nil {
		return
	}
	bs.replayDeferFrame(bs.deferFrames[len(bs.deferFrames)-1], sp)
}

// replayDeferFrame inlines one frame in LIFO order. Each body runs
// in its own name scope so local bindings from the surrounding
// block don't leak into the cleanup logic.
func (bs *bodyState) replayDeferFrame(frame []*ir.Block, sp Span) {
	for i := len(frame) - 1; i >= 0; i-- {
		body := frame[i]
		if body == nil {
			continue
		}
		bs.pushScope()
		for _, s := range body.Stmts {
			bs.lowerStmt(s)
		}
		if body.Result != nil {
			_ = bs.lowerExprAsOperand(body.Result)
		}
		bs.popScope()
	}
	_ = sp
}

// runDefers replays every defer frame (function-level + all inner
// frames) at the cursor. Called at return / ? / fall-through exits.
func (bs *bodyState) runDefers(sp Span) {
	bs.replayDefersFromDepth(0, sp)
}

// currentDeferDepth returns the number of defer frames currently in
// place. Recorded in loopFrame so break/continue know which frames
// to unwind.
func (bs *bodyState) currentDeferDepth() int {
	return len(bs.deferFrames)
}

func (bs *bodyState) lowerBreak(b *ir.BreakStmt) {
	if len(bs.loopStack) == 0 {
		bs.l.noteIssue("break outside loop")
		return
	}
	top := bs.loopStack[len(bs.loopStack)-1]
	bs.replayDefersFromDepth(top.deferDepth, b.SpanV)
	bs.emitStorageDeadFromDepth(top.scopeDepth)
	bs.terminate(&GotoTerm{Target: top.breakBlock, SpanV: b.SpanV})
}

func (bs *bodyState) lowerContinue(c *ir.ContinueStmt) {
	if len(bs.loopStack) == 0 {
		bs.l.noteIssue("continue outside loop")
		return
	}
	top := bs.loopStack[len(bs.loopStack)-1]
	bs.replayDefersFromDepth(top.deferDepth, c.SpanV)
	bs.emitStorageDeadFromDepth(top.scopeDepth)
	bs.terminate(&GotoTerm{Target: top.continueBlock, SpanV: c.SpanV})
}

func (bs *bodyState) lowerIfStmt(st *ir.IfStmt) {
	cond := bs.lowerExprAsOperand(st.Cond)
	thenBB := bs.newBlock(st.SpanV)
	elseBB := bs.newBlock(st.SpanV)
	merge := bs.newBlock(st.SpanV)
	bs.terminate(&BranchTerm{Cond: cond, Then: thenBB, Else: elseBB, SpanV: st.SpanV})

	bs.cur = thenBB
	bs.pushScope()
	bs.pushDeferScope()
	for _, s := range st.Then.Stmts {
		bs.lowerStmt(s)
	}
	if st.Then.Result != nil {
		_ = bs.lowerExprAsOperand(st.Then.Result)
	}
	bs.replayTopFrame(st.Then.SpanV)
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: merge, SpanV: st.SpanV})

	bs.cur = elseBB
	if st.Else != nil {
		bs.pushScope()
		bs.pushDeferScope()
		for _, s := range st.Else.Stmts {
			bs.lowerStmt(s)
		}
		if st.Else.Result != nil {
			_ = bs.lowerExprAsOperand(st.Else.Result)
		}
		bs.replayTopFrame(st.Else.SpanV)
		bs.popDeferScope()
		bs.popScope()
	}
	bs.terminate(&GotoTerm{Target: merge, SpanV: st.SpanV})

	bs.cur = merge
}

func (bs *bodyState) lowerForStmt(f *ir.ForStmt) {
	bs.pushScope()
	switch f.Kind {
	case ir.ForInfinite:
		bs.lowerForInfinite(f)
	case ir.ForWhile:
		bs.lowerForWhile(f)
	case ir.ForRange:
		bs.lowerForRange(f)
	case ir.ForIn:
		bs.lowerForIn(f)
	default:
		bs.l.noteIssue("unsupported for kind %d", f.Kind)
	}
	bs.popScope()
}

func (bs *bodyState) lowerForInfinite(f *ir.ForStmt) {
	header := bs.newBlock(f.SpanV)
	body := bs.newBlock(f.SpanV)
	exit := bs.newBlock(f.SpanV)
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = header
	bs.terminate(&GotoTerm{Target: body, SpanV: f.SpanV})
	bs.cur = body
	bs.pushScope()
	bs.pushDeferScope()
	bs.loopStack = append(bs.loopStack, &loopFrame{
		breakBlock: exit, continueBlock: header, deferDepth: len(bs.deferFrames) - 1, scopeDepth: bs.currentScopeDepth(),
	})
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
	bs.replayTopFrame(f.Body.SpanV)
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = exit
}

func (bs *bodyState) lowerForWhile(f *ir.ForStmt) {
	header := bs.newBlock(f.SpanV)
	body := bs.newBlock(f.SpanV)
	exit := bs.newBlock(f.SpanV)
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = header
	cond := bs.lowerExprAsOperand(f.Cond)
	bs.terminate(&BranchTerm{Cond: cond, Then: body, Else: exit, SpanV: f.SpanV})
	bs.cur = body
	bs.pushScope()
	bs.pushDeferScope()
	bs.loopStack = append(bs.loopStack, &loopFrame{
		breakBlock: exit, continueBlock: header, deferDepth: len(bs.deferFrames) - 1, scopeDepth: bs.currentScopeDepth(),
	})
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
	bs.replayTopFrame(f.Body.SpanV)
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = exit
}

// ==== range / in loop lowering ====

func (bs *bodyState) lowerForRange(f *ir.ForStmt) {
	idx := bs.newLocal(f.Var, TInt, true, f.SpanV)
	bs.emit(&StorageLiveInstr{Local: idx, SpanV: f.SpanV})
	bs.lowerExprInto(f.Start, idx, TInt)
	end := bs.newLocal("_end", TInt, false, f.SpanV)
	bs.lowerExprInto(f.End, end, TInt)
	bs.bind(f.Var, idx)
	header := bs.newBlock(f.SpanV)
	body := bs.newBlock(f.SpanV)
	step := bs.newBlock(f.SpanV)
	exit := bs.newBlock(f.SpanV)
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = header
	cmpOp := BinLt
	if f.Inclusive {
		cmpOp = BinLeq
	}
	cmp := bs.freshTemp(TBool, f.SpanV)
	bs.emit(&AssignInstr{
		Dest: Place{Local: cmp},
		Src: &BinaryRV{
			Op:    cmpOp,
			Left:  &CopyOp{Place: Place{Local: idx}, T: TInt},
			Right: &CopyOp{Place: Place{Local: end}, T: TInt},
			T:     TBool,
		},
		SpanV: f.SpanV,
	})
	bs.terminate(&BranchTerm{
		Cond:  &CopyOp{Place: Place{Local: cmp}, T: TBool},
		Then:  body,
		Else:  exit,
		SpanV: f.SpanV,
	})
	bs.cur = body
	bs.pushScope()
	bs.pushDeferScope()
	bs.loopStack = append(bs.loopStack, &loopFrame{
		breakBlock: exit, continueBlock: step, deferDepth: len(bs.deferFrames) - 1, scopeDepth: bs.currentScopeDepth(),
	})
	bs.bind(f.Var, idx)
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
	bs.replayTopFrame(f.Body.SpanV)
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: step, SpanV: f.SpanV})
	bs.cur = step
	bs.emit(&AssignInstr{
		Dest: Place{Local: idx},
		Src: &BinaryRV{
			Op:    BinAdd,
			Left:  &CopyOp{Place: Place{Local: idx}, T: TInt},
			Right: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt},
			T:     TInt,
		},
		SpanV: f.SpanV,
	})
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = exit
}

func (bs *bodyState) lowerForIn(f *ir.ForStmt) {
	iterT := f.Iter.Type()
	if isChannelType(iterT) {
		bs.lowerForInChannel(f, iterT)
		return
	}
	if !isListType(iterT) {
		bs.l.noteIssue("for-in over non-List/Channel iterable is not lowered to MIR yet")
		return
	}
	elemT := listElementType(iterT)
	if elemT == nil {
		elemT = ir.ErrTypeVal
	}
	// capture iterable into a temp so we can re-read length and index
	iter := bs.newLocal("_iter", iterT, false, f.SpanV)
	bs.emit(&StorageLiveInstr{Local: iter, SpanV: f.SpanV})
	bs.lowerExprInto(f.Iter, iter, iterT)
	lenLocal := bs.newLocal("_len", TInt, false, f.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: lenLocal},
		Src:   &LenRV{Place: Place{Local: iter}, T: TInt},
		SpanV: f.SpanV,
	})
	idx := bs.newLocal("_idx", TInt, true, f.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: idx},
		Src:   &UseRV{Op: &ConstOp{Const: &IntConst{Value: 0, T: TInt}, T: TInt}},
		SpanV: f.SpanV,
	})
	header := bs.newBlock(f.SpanV)
	body := bs.newBlock(f.SpanV)
	step := bs.newBlock(f.SpanV)
	exit := bs.newBlock(f.SpanV)
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = header
	cmp := bs.freshTemp(TBool, f.SpanV)
	bs.emit(&AssignInstr{
		Dest: Place{Local: cmp},
		Src: &BinaryRV{
			Op:    BinLt,
			Left:  &CopyOp{Place: Place{Local: idx}, T: TInt},
			Right: &CopyOp{Place: Place{Local: lenLocal}, T: TInt},
			T:     TBool,
		},
		SpanV: f.SpanV,
	})
	bs.terminate(&BranchTerm{
		Cond:  &CopyOp{Place: Place{Local: cmp}, T: TBool},
		Then:  body,
		Else:  exit,
		SpanV: f.SpanV,
	})
	bs.cur = body
	bs.pushScope()
	bs.pushDeferScope()
	bs.loopStack = append(bs.loopStack, &loopFrame{
		breakBlock: exit, continueBlock: step, deferDepth: len(bs.deferFrames) - 1, scopeDepth: bs.currentScopeDepth(),
	})
	// load current element: elem = iter[idx]
	elemLocal := bs.newLocal("_elem", elemT, false, f.SpanV)
	elemPlace := Place{Local: iter, Projections: []Projection{
		&IndexProj{
			Index:    &CopyOp{Place: Place{Local: idx}, T: TInt},
			ElemType: elemT,
		},
	}}
	bs.emit(&AssignInstr{
		Dest:  Place{Local: elemLocal},
		Src:   &UseRV{Op: &CopyOp{Place: elemPlace, T: elemT}},
		SpanV: f.SpanV,
	})
	if f.Pattern != nil {
		bs.bindPattern(f.Pattern, Place{Local: elemLocal}, elemT, f.SpanV)
	} else if f.Var != "" {
		bs.bind(f.Var, elemLocal)
	}
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
	bs.replayTopFrame(f.Body.SpanV)
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: step, SpanV: f.SpanV})
	bs.cur = step
	bs.emit(&AssignInstr{
		Dest: Place{Local: idx},
		Src: &BinaryRV{
			Op:    BinAdd,
			Left:  &CopyOp{Place: Place{Local: idx}, T: TInt},
			Right: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt},
			T:     TInt,
		},
		SpanV: f.SpanV,
	})
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = exit
}

// lowerForInChannel lowers `for x in channel { body }`. Each iteration
// reads a value via IntrinsicChanRecv (which returns Option<T>) and
// exits when the receive yields None — matching the spec's
// "`for x in ch` terminates when the channel is closed and drained,
// or when the surrounding task is cancelled" semantics.
func (bs *bodyState) lowerForInChannel(f *ir.ForStmt, iterT Type) {
	elemT := channelElementType(iterT)
	optT := &ir.OptionalType{Inner: elemT}
	iter := bs.newLocal("_chan", iterT, false, f.SpanV)
	bs.emit(&StorageLiveInstr{Local: iter, SpanV: f.SpanV})
	bs.lowerExprInto(f.Iter, iter, iterT)

	header := bs.newBlock(f.SpanV)
	body := bs.newBlock(f.SpanV)
	step := bs.newBlock(f.SpanV)
	exit := bs.newBlock(f.SpanV)
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})

	bs.cur = header
	// opt := chan_recv(iter); switch opt { Some => body, None => exit }
	opt := bs.newLocal("_recv", optT, false, f.SpanV)
	bs.emit(&IntrinsicInstr{
		Dest:  &Place{Local: opt},
		Kind:  IntrinsicChanRecv,
		Args:  []Operand{&CopyOp{Place: Place{Local: iter}, T: iterT}},
		SpanV: f.SpanV,
	})
	disc := bs.freshTemp(TInt, f.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: disc},
		Src:   &DiscriminantRV{Place: Place{Local: opt}, T: TInt},
		SpanV: f.SpanV,
	})
	bs.terminate(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: disc}, T: TInt},
		Cases:     []SwitchCase{{Value: someTagOf(optT), Target: body, Label: "Some"}},
		Default:   exit,
		SpanV:     f.SpanV,
	})

	bs.cur = body
	bs.pushScope()
	bs.pushDeferScope()
	bs.loopStack = append(bs.loopStack, &loopFrame{
		breakBlock: exit, continueBlock: step, deferDepth: len(bs.deferFrames) - 1, scopeDepth: bs.currentScopeDepth(),
	})
	// Unwrap the payload into a named local.
	elemLocal := bs.newLocal("_elem", elemT, false, f.SpanV)
	payloadProj := &VariantProj{
		Variant:  int(someTagOf(optT)),
		Name:     "Some",
		FieldIdx: 0,
		Type:     elemT,
	}
	bs.emit(&AssignInstr{
		Dest:  Place{Local: elemLocal},
		Src:   &UseRV{Op: &CopyOp{Place: Place{Local: opt}.Project(payloadProj), T: elemT}},
		SpanV: f.SpanV,
	})
	if f.Pattern != nil {
		bs.bindPattern(f.Pattern, Place{Local: elemLocal}, elemT, f.SpanV)
	} else if f.Var != "" {
		bs.bind(f.Var, elemLocal)
	}
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
	bs.replayTopFrame(f.Body.SpanV)
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: step, SpanV: f.SpanV})

	bs.cur = step
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = exit
}

// ==== match lowering ====

func (bs *bodyState) lowerMatchStmt(m *ir.MatchStmt) {
	// Discard the result; we only care about side effects.
	bs.lowerMatch(m.Scrutinee, m.Arms, m.Tree, m.SpanV, nil, TUnit)
}

// lowerMatchExprInto lowers a MatchExpr into dest.
func (bs *bodyState) lowerMatchExprInto(m *ir.MatchExpr, dest LocalID, destT Type) {
	bs.lowerMatch(m.Scrutinee, m.Arms, m.Tree, m.SpanV, &dest, destT)
}

// lowerMatch drives match lowering using an optional decision tree.
func (bs *bodyState) lowerMatch(scrutinee ir.Expr, arms []*ir.MatchArm, tree ir.DecisionNode, sp Span, dest *LocalID, destT Type) {
	bs.pushScope()
	scrutT := scrutinee.Type()
	if scrutT == nil {
		scrutT = ir.ErrTypeVal
	}
	scrutLocal := bs.newLocal("_scrut", scrutT, false, sp)
	bs.emit(&StorageLiveInstr{Local: scrutLocal, SpanV: sp})
	bs.lowerExprInto(scrutinee, scrutLocal, scrutT)

	merge := bs.newBlock(sp)
	armBlocks := make([]BlockID, len(arms))
	for i, arm := range arms {
		armBlocks[i] = bs.newBlock(arm.SpanV)
		_ = arm
	}

	// If we have a decision tree, use it.
	if tree != nil {
		ctx := &matchContext{
			bs:         bs,
			scrutLocal: scrutLocal,
			scrutT:     scrutT,
			armBlocks:  armBlocks,
			merge:      merge,
			sp:         sp,
		}
		ctx.lowerTree(tree)
	} else {
		// No decision tree: keep the module structurally valid by
		// routing to arm 0 and flag an issue so the caller knows to
		// fall back to the HIR backend path.
		bs.l.noteIssue("match without decision tree not lowered to MIR")
		if len(armBlocks) > 0 {
			bs.terminate(&GotoTerm{Target: armBlocks[0], SpanV: sp})
		} else {
			bs.terminate(&UnreachableTerm{SpanV: sp})
		}
	}

	// Lower each arm body into its block.
	for i, arm := range arms {
		bs.cur = armBlocks[i]
		bs.pushScope()
		bs.pushDeferScope()
		// The decision tree already introduced bindings via MIR assigns.
		// For tree-less fallback we would destructure here; we skip.
		if arm.Body != nil {
			for _, s := range arm.Body.Stmts {
				bs.lowerStmt(s)
			}
			if dest != nil && arm.Body.Result != nil {
				bs.lowerExprInto(arm.Body.Result, *dest, destT)
			} else if arm.Body.Result != nil {
				_ = bs.lowerExprAsOperand(arm.Body.Result)
			}
			bs.replayTopFrame(arm.Body.SpanV)
		}
		bs.popDeferScope()
		bs.popScope()
		bs.terminate(&GotoTerm{Target: merge, SpanV: arm.SpanV})
	}

	bs.cur = merge
	bs.popScope()
}

type matchContext struct {
	bs         *bodyState
	scrutLocal LocalID
	scrutT     Type
	armBlocks  []BlockID
	merge      BlockID
	sp         Span
}

// lowerTree translates a HIR decision tree into MIR control flow that
// converges on the appropriate arm block. It assumes the current
// cursor is the block where the decision cascade should begin.
func (mc *matchContext) lowerTree(node ir.DecisionNode) {
	bs := mc.bs
	switch n := node.(type) {
	case *ir.DecisionLeaf:
		if n.ArmIndex < 0 || n.ArmIndex >= len(mc.armBlocks) {
			bs.terminate(&UnreachableTerm{SpanV: mc.sp})
			return
		}
		bs.terminate(&GotoTerm{Target: mc.armBlocks[n.ArmIndex], SpanV: mc.sp})
	case *ir.DecisionFail:
		bs.terminate(&UnreachableTerm{SpanV: mc.sp})
	case *ir.DecisionBind:
		// Emit Assign dest = scrutinee[proj]
		projType := projectionResultType(bs.l, mc.scrutT, n.Proj)
		if n.T != nil {
			projType = n.T
		}
		place := projectionToPlace(bs.l, mc.scrutLocal, mc.scrutT, n.Proj)
		id := bs.newLocal(n.Name, projType, false, mc.sp)
		bs.emit(&StorageLiveInstr{Local: id, SpanV: mc.sp})
		bs.emit(&AssignInstr{
			Dest:  Place{Local: id},
			Src:   &UseRV{Op: &CopyOp{Place: place, T: projType}},
			SpanV: mc.sp,
		})
		bs.bind(n.Name, id)
		mc.lowerTree(n.Next)
	case *ir.DecisionGuard:
		if n.Cond == nil {
			// Unconditional edge to Then.
			mc.lowerTree(n.Then)
			return
		}
		cond := bs.lowerExprAsOperand(n.Cond)
		thenBB := bs.newBlock(mc.sp)
		elseBB := bs.newBlock(mc.sp)
		bs.terminate(&BranchTerm{Cond: cond, Then: thenBB, Else: elseBB, SpanV: mc.sp})
		bs.cur = thenBB
		mc.lowerTree(n.Then)
		bs.cur = elseBB
		mc.lowerTree(n.Else)
	case *ir.DecisionSwitch:
		mc.lowerSwitch(n)
	default:
		bs.l.noteIssue("decision tree: unsupported node %T", node)
		bs.terminate(&UnreachableTerm{SpanV: mc.sp})
	}
}

func (mc *matchContext) lowerSwitch(sw *ir.DecisionSwitch) {
	bs := mc.bs
	projType := projectionResultType(bs.l, mc.scrutT, sw.Proj)
	projPlace := projectionToPlace(bs.l, mc.scrutLocal, mc.scrutT, sw.Proj)
	switch sw.Kind {
	case ir.SwitchVariant:
		// Read discriminant and dispatch.
		disc := bs.freshTemp(TInt, mc.sp)
		bs.emit(&AssignInstr{
			Dest:  Place{Local: disc},
			Src:   &DiscriminantRV{Place: projPlace, T: TInt},
			SpanV: mc.sp,
		})
		cases := make([]SwitchCase, 0, len(sw.Cases))
		caseBlocks := make([]BlockID, len(sw.Cases))
		for i := range sw.Cases {
			caseBlocks[i] = bs.newBlock(mc.sp)
		}
		defaultBB := bs.newBlock(mc.sp)
		for i, c := range sw.Cases {
			varIdx := bs.l.variantIndexByName(projType, c.Variant)
			cases = append(cases, SwitchCase{
				Value:  int64(varIdx),
				Target: caseBlocks[i],
				Label:  c.Variant,
			})
		}
		bs.terminate(&SwitchIntTerm{
			Scrutinee: &CopyOp{Place: Place{Local: disc}, T: TInt},
			Cases:     cases,
			Default:   defaultBB,
			SpanV:     mc.sp,
		})
		for i, c := range sw.Cases {
			bs.cur = caseBlocks[i]
			mc.lowerTree(c.Body)
		}
		bs.cur = defaultBB
		if sw.Default != nil {
			mc.lowerTree(sw.Default)
		} else {
			bs.terminate(&UnreachableTerm{SpanV: mc.sp})
		}
	case ir.SwitchBool:
		// Two-way branch on bool projection.
		thenBB := bs.newBlock(mc.sp)
		elseBB := bs.newBlock(mc.sp)
		bs.terminate(&BranchTerm{
			Cond:  &CopyOp{Place: projPlace, T: TBool},
			Then:  thenBB,
			Else:  elseBB,
			SpanV: mc.sp,
		})
		// Cases are either {true} or {false} or both; run them.
		trueBB, falseBB := thenBB, elseBB
		trueHandled, falseHandled := false, false
		for _, c := range sw.Cases {
			if strings.EqualFold(c.Label, "true") {
				bs.cur = trueBB
				mc.lowerTree(c.Body)
				trueHandled = true
			} else {
				bs.cur = falseBB
				mc.lowerTree(c.Body)
				falseHandled = true
			}
		}
		if !trueHandled {
			bs.cur = trueBB
			if sw.Default != nil {
				mc.lowerTree(sw.Default)
			} else {
				bs.terminate(&UnreachableTerm{SpanV: mc.sp})
			}
		}
		if !falseHandled {
			bs.cur = falseBB
			if sw.Default != nil {
				mc.lowerTree(sw.Default)
			} else {
				bs.terminate(&UnreachableTerm{SpanV: mc.sp})
			}
		}
	case ir.SwitchLit:
		// Chain of eq-branches. First try integer cases into a SwitchInt.
		// If any case has a non-integer literal we fall back to the
		// branch chain.
		allInt := true
		intVals := make([]int64, 0, len(sw.Cases))
		for _, c := range sw.Cases {
			il, ok := c.Lit.(*ir.IntLit)
			if !ok {
				allInt = false
				break
			}
			v, err := strconv.ParseInt(il.Text, 0, 64)
			if err != nil {
				allInt = false
				break
			}
			intVals = append(intVals, v)
		}
		if allInt {
			cases := make([]SwitchCase, len(sw.Cases))
			bodies := make([]BlockID, len(sw.Cases))
			for i := range sw.Cases {
				bodies[i] = bs.newBlock(mc.sp)
				cases[i] = SwitchCase{Value: intVals[i], Target: bodies[i], Label: sw.Cases[i].Label}
			}
			def := bs.newBlock(mc.sp)
			bs.terminate(&SwitchIntTerm{
				Scrutinee: &CopyOp{Place: projPlace, T: projType},
				Cases:     cases,
				Default:   def,
				SpanV:     mc.sp,
			})
			for i, c := range sw.Cases {
				bs.cur = bodies[i]
				mc.lowerTree(c.Body)
			}
			bs.cur = def
			if sw.Default != nil {
				mc.lowerTree(sw.Default)
			} else {
				bs.terminate(&UnreachableTerm{SpanV: mc.sp})
			}
			return
		}
		// Mixed / non-integer literal chain.
		var defTarget BlockID
		if sw.Default != nil {
			defTarget = bs.newBlock(mc.sp)
		} else {
			defTarget = bs.newBlock(mc.sp)
		}
		next := defTarget
		for i := len(sw.Cases) - 1; i >= 0; i-- {
			c := sw.Cases[i]
			caseBB := bs.newBlock(mc.sp)
			testBB := bs.newBlock(mc.sp)
			// Lower the literal into an operand.
			rhs := bs.lowerExprAsOperand(c.Lit)
			cmp := bs.freshTemp(TBool, mc.sp)
			bs.emit(&AssignInstr{
				Dest: Place{Local: cmp},
				Src: &BinaryRV{
					Op:    BinEq,
					Left:  &CopyOp{Place: projPlace, T: projType},
					Right: rhs,
					T:     TBool,
				},
				SpanV: mc.sp,
			})
			bs.terminate(&BranchTerm{
				Cond:  &CopyOp{Place: Place{Local: cmp}, T: TBool},
				Then:  caseBB,
				Else:  next,
				SpanV: mc.sp,
			})
			bs.cur = caseBB
			mc.lowerTree(c.Body)
			bs.cur = testBB
			next = testBB
		}
		bs.cur = defTarget
		if sw.Default != nil {
			mc.lowerTree(sw.Default)
		} else {
			bs.terminate(&UnreachableTerm{SpanV: mc.sp})
		}
	case ir.SwitchTuple:
		// A SwitchTuple has exactly one case that destructures; no
		// actual conditional branch is needed beyond the bindings.
		if len(sw.Cases) > 0 {
			mc.lowerTree(sw.Cases[0].Body)
		} else if sw.Default != nil {
			mc.lowerTree(sw.Default)
		} else {
			bs.terminate(&UnreachableTerm{SpanV: mc.sp})
		}
	default:
		bs.l.noteIssue("unsupported switch kind %d", sw.Kind)
		bs.terminate(&UnreachableTerm{SpanV: mc.sp})
	}
}

// ==== expression lowering ====

// lowerExprInto lowers e and stores its value in dest.
func (bs *bodyState) lowerExprInto(e ir.Expr, dest LocalID, destT Type) {
	bs.lowerExprIntoPlace(e, Place{Local: dest}, destT)
}

// lowerExprIntoPlace lowers e and stores its value in dest (which may
// have projections).
func (bs *bodyState) lowerExprIntoPlace(e ir.Expr, dest Place, destT Type) {
	switch x := e.(type) {
	case nil:
		return
	case *ir.IfExpr:
		bs.lowerIfExprInto(x, dest, destT)
		return
	case *ir.IfLetExpr:
		bs.lowerIfLetExprInto(x, dest, destT)
		return
	case *ir.BlockExpr:
		bs.lowerBlockExprInto(x, dest, destT)
		return
	case *ir.MatchExpr:
		bs.lowerMatchExprIntoPlace(x, dest, destT)
		return
	case *ir.CoalesceExpr:
		bs.lowerCoalesceInto(x, dest, destT)
		return
	case *ir.QuestionExpr:
		bs.lowerQuestionInto(x, dest, destT)
		return
	case *ir.FieldExpr:
		if x.Optional {
			bs.lowerOptionalFieldInto(x, dest, destT)
			return
		}
	case *ir.MethodCall:
		bs.lowerMethodCallInto(x, dest, destT)
		return
	case *ir.CallExpr:
		bs.lowerCallExprInto(x, &dest, destT)
		return
	case *ir.IntrinsicCall:
		bs.lowerIntrinsicCallInto(x, &dest)
		return
	case *ir.MapLit:
		bs.lowerMapLitInto(x, dest, destT)
		return
	}
	// Default path: lower to an rvalue and assign.
	rv := bs.lowerExprToRValue(e, destT)
	if rv == nil {
		return
	}
	bs.emit(&AssignInstr{Dest: dest, Src: rv, SpanV: exprSpan(e)})
}

// lowerExprAsOperand lowers e and returns an operand holding its
// value. Used for call arguments and conditions.
func (bs *bodyState) lowerExprAsOperand(e ir.Expr) Operand {
	switch x := e.(type) {
	case nil:
		return &ConstOp{Const: &UnitConst{}, T: TUnit}
	case *ir.IntLit:
		v, _ := strconv.ParseInt(strings.ReplaceAll(x.Text, "_", ""), 0, 64)
		t := x.T
		if t == nil {
			t = TInt
		}
		return &ConstOp{Const: &IntConst{Value: v, T: t}, T: t}
	case *ir.FloatLit:
		v, _ := strconv.ParseFloat(strings.ReplaceAll(x.Text, "_", ""), 64)
		t := x.T
		if t == nil {
			t = TFloat
		}
		return &ConstOp{Const: &FloatConst{Value: v, T: t}, T: t}
	case *ir.BoolLit:
		return &ConstOp{Const: &BoolConst{Value: x.Value}, T: TBool}
	case *ir.CharLit:
		return &ConstOp{Const: &CharConst{Value: x.Value}, T: TChar}
	case *ir.ByteLit:
		return &ConstOp{Const: &ByteConst{Value: x.Value}, T: TByte}
	case *ir.UnitLit:
		return &ConstOp{Const: &UnitConst{}, T: TUnit}
	case *ir.StringLit:
		return bs.lowerStringLit(x)
	case *ir.Ident:
		return bs.lowerIdent(x)
	}
	// For anything else: allocate a temp and lower into it.
	t := e.Type()
	if t == nil {
		t = ir.ErrTypeVal
	}
	tmp := bs.freshTemp(t, exprSpan(e))
	bs.lowerExprInto(e, tmp, t)
	return &CopyOp{Place: Place{Local: tmp}, T: t}
}

// lowerExprToRValue lowers a non-control-flow expression into an
// RValue suitable for the RHS of an Assign.
func (bs *bodyState) lowerExprToRValue(e ir.Expr, hint Type) RValue {
	switch x := e.(type) {
	case nil:
		return &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}}
	case *ir.UnaryExpr:
		return &UnaryRV{
			Op:  mapUnaryOp(x.Op),
			Arg: bs.lowerExprAsOperand(x.X),
			T:   x.T,
		}
	case *ir.BinaryExpr:
		return &BinaryRV{
			Op:    mapBinaryOp(x.Op),
			Left:  bs.lowerExprAsOperand(x.Left),
			Right: bs.lowerExprAsOperand(x.Right),
			T:     x.T,
		}
	case *ir.FieldExpr:
		if !x.Optional {
			// Plain field access; lower receiver into a place and
			// project.
			recvPlace, ok := bs.lowerExprToPlace(x.X)
			if !ok {
				// Fall back: lower X into a temp.
				t := x.X.Type()
				tmp := bs.freshTemp(t, exprSpan(x.X))
				bs.lowerExprInto(x.X, tmp, t)
				recvPlace = Place{Local: tmp}
			}
			info := bs.l.structFromType(x.X.Type(), "")
			idx := bs.l.fieldIndex(info, x.Name)
			proj := &FieldProj{Index: idx, Name: x.Name, Type: x.T}
			return &UseRV{Op: &CopyOp{Place: recvPlace.Project(proj), T: x.T}}
		}
		// Optional field — handled via lowerOptionalFieldInto (the
		// caller should have routed us through lowerExprIntoPlace).
		bs.l.noteIssue("optional field expression reached lowerExprToRValue")
		return &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}}
	case *ir.TupleAccess:
		recvPlace, ok := bs.lowerExprToPlace(x.X)
		if !ok {
			t := x.X.Type()
			tmp := bs.freshTemp(t, exprSpan(x.X))
			bs.lowerExprInto(x.X, tmp, t)
			recvPlace = Place{Local: tmp}
		}
		return &UseRV{Op: &CopyOp{Place: recvPlace.Project(&TupleProj{Index: x.Index, Type: x.T}), T: x.T}}
	case *ir.IndexExpr:
		recvPlace, ok := bs.lowerExprToPlace(x.X)
		if !ok {
			t := x.X.Type()
			tmp := bs.freshTemp(t, exprSpan(x.X))
			bs.lowerExprInto(x.X, tmp, t)
			recvPlace = Place{Local: tmp}
		}
		idx := bs.lowerExprAsOperand(x.Index)
		return &UseRV{Op: &CopyOp{Place: recvPlace.Project(&IndexProj{Index: idx, ElemType: x.T}), T: x.T}}
	case *ir.TupleLit:
		fields := make([]Operand, len(x.Elems))
		for i, e := range x.Elems {
			fields[i] = bs.lowerExprAsOperand(e)
		}
		return &AggregateRV{Kind: AggTuple, Fields: fields, T: x.T}
	case *ir.ListLit:
		fields := make([]Operand, len(x.Elems))
		for i, e := range x.Elems {
			fields[i] = bs.lowerExprAsOperand(e)
		}
		lt := hint
		if lt == nil {
			lt = x.Type()
		}
		return &AggregateRV{Kind: AggList, Fields: fields, T: lt}
	case *ir.StructLit:
		return bs.lowerStructLit(x, hint)
	case *ir.VariantLit:
		return bs.lowerVariantLit(x, hint)
	case *ir.RangeLit:
		// Range values — unsupported in value position for MIR stage 1.
		bs.l.noteIssue("range literal in value position not lowered to MIR")
		return &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}}
	case *ir.Closure:
		return bs.lowerClosure(x, hint)
	case *ir.ErrorExpr:
		bs.l.noteIssue("error expression reached MIR: %s", x.Note)
		return &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}}
	}
	// Fallback: lower as operand and wrap in UseRV.
	return &UseRV{Op: bs.lowerExprAsOperand(e)}
}

// lowerExprToPlace tries to describe e as a Place without materialising
// intermediate storage. Returns false when the expression is not a
// place expression (arithmetic, call, etc.).
func (bs *bodyState) lowerExprToPlace(e ir.Expr) (Place, bool) {
	switch x := e.(type) {
	case *ir.Ident:
		if id, ok := bs.lookup(x.Name); ok {
			return Place{Local: id}, true
		}
	case *ir.FieldExpr:
		if x.Optional {
			return Place{}, false
		}
		recv, ok := bs.lowerExprToPlace(x.X)
		if !ok {
			return Place{}, false
		}
		info := bs.l.structFromType(x.X.Type(), "")
		idx := bs.l.fieldIndex(info, x.Name)
		return recv.Project(&FieldProj{Index: idx, Name: x.Name, Type: x.T}), true
	case *ir.TupleAccess:
		recv, ok := bs.lowerExprToPlace(x.X)
		if !ok {
			return Place{}, false
		}
		return recv.Project(&TupleProj{Index: x.Index, Type: x.T}), true
	case *ir.IndexExpr:
		recv, ok := bs.lowerExprToPlace(x.X)
		if !ok {
			return Place{}, false
		}
		idx := bs.lowerExprAsOperand(x.Index)
		return recv.Project(&IndexProj{Index: idx, ElemType: x.T}), true
	}
	return Place{}, false
}

// lowerIdent turns an Ident into an operand.
func (bs *bodyState) lowerIdent(id *ir.Ident) Operand {
	switch id.Kind {
	case ir.IdentFn:
		fnType := id.T
		return &ConstOp{Const: &FnConst{Symbol: id.Name, T: fnType}, T: fnType}
	case ir.IdentGlobal:
		// Materialise a fresh local holding the current global value
		// via a GlobalRefRV; backends pick their own strategy
		// (static slot, init-on-first-use). We allocate one temp per
		// read — redundancy elimination is a later optimisation pass.
		t := id.T
		if t == nil {
			if decl, ok := bs.l.globals[id.Name]; ok {
				t = decl.Type
				if t == nil && decl.Value != nil {
					t = decl.Value.Type()
				}
			}
		}
		if t == nil {
			t = ir.ErrTypeVal
		}
		tmp := bs.freshTemp(t, id.SpanV)
		bs.emit(&AssignInstr{
			Dest:  Place{Local: tmp},
			Src:   &GlobalRefRV{Name: id.Name, T: t},
			SpanV: id.SpanV,
		})
		return &CopyOp{Place: Place{Local: tmp}, T: t}
	case ir.IdentVariant:
		// bare variant → aggregate with no payload.
		t := id.T
		if t == nil {
			t = ir.ErrTypeVal
		}
		tmp := bs.freshTemp(t, id.SpanV)
		idx := bs.l.variantIndexByName(t, id.Name)
		bs.emit(&AssignInstr{
			Dest: Place{Local: tmp},
			Src: &AggregateRV{
				Kind:       AggEnumVariant,
				Fields:     nil,
				T:          t,
				VariantIdx: idx,
				VariantTag: id.Name,
			},
			SpanV: id.SpanV,
		})
		return &CopyOp{Place: Place{Local: tmp}, T: t}
	}
	if localID, ok := bs.lookup(id.Name); ok {
		loc := bs.fn.Local(localID)
		t := id.T
		if loc != nil && loc.Type != nil {
			t = loc.Type
		}
		return &CopyOp{Place: Place{Local: localID}, T: t}
	}
	// Unknown identifier — possibly a top-level fn not captured in our
	// signature table; fall back to FnConst.
	if id.Name != "" {
		t := id.T
		if t == nil {
			t = ir.ErrTypeVal
		}
		return &ConstOp{Const: &FnConst{Symbol: id.Name, T: t}, T: t}
	}
	return &ConstOp{Const: &UnitConst{}, T: TUnit}
}

// lowerStringLit returns an operand for a StringLit.
func (bs *bodyState) lowerStringLit(s *ir.StringLit) Operand {
	if !hasInterpolation(s) {
		var b strings.Builder
		for _, p := range s.Parts {
			if p.IsLit {
				b.WriteString(p.Lit)
			}
		}
		return &ConstOp{Const: &StringConst{Value: b.String()}, T: TString}
	}
	// Interpolated: emit a string_concat intrinsic call.
	args := make([]Operand, 0, len(s.Parts))
	for _, p := range s.Parts {
		if p.IsLit {
			args = append(args, &ConstOp{Const: &StringConst{Value: p.Lit}, T: TString})
			continue
		}
		op := bs.lowerExprAsOperand(p.Expr)
		args = append(args, op)
	}
	tmp := bs.freshTemp(TString, s.SpanV)
	bs.emit(&IntrinsicInstr{
		Dest:  &Place{Local: tmp},
		Kind:  IntrinsicStringConcat,
		Args:  args,
		SpanV: s.SpanV,
	})
	return &CopyOp{Place: Place{Local: tmp}, T: TString}
}

// lowerIfExprInto lowers an IfExpr by reusing the stmt-form lowering
// and threading a result slot.
func (bs *bodyState) lowerIfExprInto(ie *ir.IfExpr, dest Place, destT Type) {
	cond := bs.lowerExprAsOperand(ie.Cond)
	thenBB := bs.newBlock(ie.SpanV)
	elseBB := bs.newBlock(ie.SpanV)
	merge := bs.newBlock(ie.SpanV)
	bs.terminate(&BranchTerm{Cond: cond, Then: thenBB, Else: elseBB, SpanV: ie.SpanV})
	bs.cur = thenBB
	bs.pushScope()
	bs.pushDeferScope()
	for _, s := range ie.Then.Stmts {
		bs.lowerStmt(s)
	}
	if ie.Then.Result != nil {
		bs.lowerExprIntoPlace(ie.Then.Result, dest, destT)
	}
	bs.replayTopFrame(ie.Then.SpanV)
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: merge, SpanV: ie.SpanV})
	bs.cur = elseBB
	if ie.Else != nil {
		bs.pushScope()
		bs.pushDeferScope()
		for _, s := range ie.Else.Stmts {
			bs.lowerStmt(s)
		}
		if ie.Else.Result != nil {
			bs.lowerExprIntoPlace(ie.Else.Result, dest, destT)
		}
		bs.replayTopFrame(ie.Else.SpanV)
		bs.popDeferScope()
		bs.popScope()
	}
	bs.terminate(&GotoTerm{Target: merge, SpanV: ie.SpanV})
	bs.cur = merge
}

// lowerBlockExprInto lowers a BlockExpr into dest.
func (bs *bodyState) lowerBlockExprInto(be *ir.BlockExpr, dest Place, destT Type) {
	if be.Block == nil {
		return
	}
	bs.pushScope()
	bs.pushDeferScope()
	for _, s := range be.Block.Stmts {
		bs.lowerStmt(s)
	}
	if be.Block.Result != nil {
		bs.lowerExprIntoPlace(be.Block.Result, dest, destT)
	}
	bs.replayTopFrame(be.Block.SpanV)
	bs.popDeferScope()
	bs.popScope()
}

// lowerIfLetExprInto handles `if let pat = scrut { … } else { … }`.
// It lowers into a discriminant test + payload binding for Option /
// enum variants, and falls back to an unsupported note otherwise.
func (bs *bodyState) lowerIfLetExprInto(ife *ir.IfLetExpr, dest Place, destT Type) {
	bs.pushScope()
	scrut := ife.Scrutinee
	scrutT := scrut.Type()
	scrutLocal := bs.newLocal("_iflet", scrutT, false, ife.SpanV)
	bs.emit(&StorageLiveInstr{Local: scrutLocal, SpanV: ife.SpanV})
	bs.lowerExprInto(scrut, scrutLocal, scrutT)

	vp, ok := ife.Pattern.(*ir.VariantPat)
	if !ok {
		bs.l.noteIssue("if-let with non-variant pattern not lowered to MIR: %T", ife.Pattern)
		// Always take the else branch to stay conservative.
		if ife.Else != nil {
			bs.pushScope()
			bs.pushDeferScope()
			for _, s := range ife.Else.Stmts {
				bs.lowerStmt(s)
			}
			if ife.Else.Result != nil {
				bs.lowerExprIntoPlace(ife.Else.Result, dest, destT)
			}
			bs.replayTopFrame(ife.Else.SpanV)
			bs.popDeferScope()
			bs.popScope()
		}
		bs.popScope()
		return
	}
	varIdx := bs.l.variantIndexByName(scrutT, vp.Variant)
	disc := bs.freshTemp(TInt, ife.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: disc},
		Src:   &DiscriminantRV{Place: Place{Local: scrutLocal}, T: TInt},
		SpanV: ife.SpanV,
	})
	thenBB := bs.newBlock(ife.SpanV)
	elseBB := bs.newBlock(ife.SpanV)
	merge := bs.newBlock(ife.SpanV)
	bs.terminate(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: disc}, T: TInt},
		Cases:     []SwitchCase{{Value: int64(varIdx), Target: thenBB, Label: vp.Variant}},
		Default:   elseBB,
		SpanV:     ife.SpanV,
	})
	bs.cur = thenBB
	bs.pushScope()
	bs.pushDeferScope()
	// Bind payload elements.
	layout := bs.l.variantLayout(vp.Enum, vp.Variant)
	for i, arg := range vp.Args {
		pt := variantPayloadType(layout, i)
		proj := &VariantProj{Variant: variantIndex(layout), Name: vp.Variant, FieldIdx: i, Type: pt}
		child := Place{Local: scrutLocal}.Project(proj)
		bs.bindPattern(arg, child, pt, ife.SpanV)
	}
	for _, s := range ife.Then.Stmts {
		bs.lowerStmt(s)
	}
	if ife.Then.Result != nil {
		bs.lowerExprIntoPlace(ife.Then.Result, dest, destT)
	}
	bs.replayTopFrame(ife.Then.SpanV)
	bs.popDeferScope()
	bs.popScope()
	bs.terminate(&GotoTerm{Target: merge, SpanV: ife.SpanV})
	bs.cur = elseBB
	if ife.Else != nil {
		bs.pushScope()
		bs.pushDeferScope()
		for _, s := range ife.Else.Stmts {
			bs.lowerStmt(s)
		}
		if ife.Else.Result != nil {
			bs.lowerExprIntoPlace(ife.Else.Result, dest, destT)
		}
		bs.replayTopFrame(ife.Else.SpanV)
		bs.popDeferScope()
		bs.popScope()
	}
	bs.terminate(&GotoTerm{Target: merge, SpanV: ife.SpanV})
	bs.cur = merge
	bs.popScope()
}

// lowerMatchExprIntoPlace routes through the generic matcher.
func (bs *bodyState) lowerMatchExprIntoPlace(m *ir.MatchExpr, dest Place, destT Type) {
	// For simplicity: allocate a local, drive match into it, then copy
	// into the requested place.
	t := destT
	if t == nil {
		t = m.T
	}
	local := bs.newLocal("_match", t, false, m.SpanV)
	bs.emit(&StorageLiveInstr{Local: local, SpanV: m.SpanV})
	bs.lowerMatch(m.Scrutinee, m.Arms, m.Tree, m.SpanV, &local, t)
	bs.emit(&AssignInstr{
		Dest:  dest,
		Src:   &UseRV{Op: &CopyOp{Place: Place{Local: local}, T: t}},
		SpanV: m.SpanV,
	})
}

// lowerCoalesceInto handles `left ?? right`.
func (bs *bodyState) lowerCoalesceInto(c *ir.CoalesceExpr, dest Place, destT Type) {
	bs.pushScope()
	leftT := c.Left.Type()
	leftLocal := bs.newLocal("_coalesce", leftT, false, c.SpanV)
	bs.emit(&StorageLiveInstr{Local: leftLocal, SpanV: c.SpanV})
	bs.lowerExprInto(c.Left, leftLocal, leftT)
	disc := bs.freshTemp(TInt, c.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: disc},
		Src:   &DiscriminantRV{Place: Place{Local: leftLocal}, T: TInt},
		SpanV: c.SpanV,
	})
	someBB := bs.newBlock(c.SpanV)
	noneBB := bs.newBlock(c.SpanV)
	merge := bs.newBlock(c.SpanV)
	bs.terminate(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: disc}, T: TInt},
		Cases:     []SwitchCase{{Value: someTagOf(leftT), Target: someBB, Label: "Some"}},
		Default:   noneBB,
		SpanV:     c.SpanV,
	})
	bs.cur = someBB
	payloadProj := &VariantProj{Variant: int(someTagOf(leftT)), Name: "Some", FieldIdx: 0, Type: destT}
	bs.emit(&AssignInstr{
		Dest:  dest,
		Src:   &UseRV{Op: &CopyOp{Place: Place{Local: leftLocal}.Project(payloadProj), T: destT}},
		SpanV: c.SpanV,
	})
	bs.terminate(&GotoTerm{Target: merge, SpanV: c.SpanV})
	bs.cur = noneBB
	bs.lowerExprIntoPlace(c.Right, dest, destT)
	bs.terminate(&GotoTerm{Target: merge, SpanV: c.SpanV})
	bs.cur = merge
	bs.popScope()
}

// lowerQuestionInto handles `e?`. The receiver must be Option<T> or
// Result<T, E>. On the error path we rebuild the none/err value in
// the enclosing function's return type and early-return.
//
// The receiver's value type need not match the function's return
// type; for Result, the Ok payload differs (A vs B) but the Err
// payload is the shared tag type E, so the error path extracts
// tmp.@Err.0 and rewraps it into a fresh Result<B, E>. For Option, the
// error path is just "None of the return type" — materialised via
// NullaryRV{NullaryNone}.
func (bs *bodyState) lowerQuestionInto(q *ir.QuestionExpr, dest Place, destT Type) {
	bs.pushScope()
	xT := q.X.Type()
	tmp := bs.newLocal("_q", xT, false, q.SpanV)
	bs.emit(&StorageLiveInstr{Local: tmp, SpanV: q.SpanV})
	bs.lowerExprInto(q.X, tmp, xT)
	disc := bs.freshTemp(TInt, q.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: disc},
		Src:   &DiscriminantRV{Place: Place{Local: tmp}, T: TInt},
		SpanV: q.SpanV,
	})
	okBB := bs.newBlock(q.SpanV)
	errBB := bs.newBlock(q.SpanV)
	bs.terminate(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: disc}, T: TInt},
		Cases:     []SwitchCase{{Value: okTagOf(xT), Target: okBB, Label: okVariantName(xT)}},
		Default:   errBB,
		SpanV:     q.SpanV,
	})
	bs.cur = errBB
	bs.rebuildErrorIntoReturn(tmp, xT, q.SpanV)
	bs.runDefers(q.SpanV)
	bs.terminate(&ReturnTerm{SpanV: q.SpanV})
	bs.cur = okBB
	payloadProj := &VariantProj{
		Variant:  int(okTagOf(xT)),
		Name:     okVariantName(xT),
		FieldIdx: 0,
		Type:     destT,
	}
	bs.emit(&AssignInstr{
		Dest:  dest,
		Src:   &UseRV{Op: &CopyOp{Place: Place{Local: tmp}.Project(payloadProj), T: destT}},
		SpanV: q.SpanV,
	})
	bs.popScope()
}

// rebuildErrorIntoReturn materialises the error value for a `?`
// propagation in the shape of the enclosing function's return type.
// `operand` holds the Option/Result the user wrote; `operandT` is its
// type. The function writes into the function's return local.
func (bs *bodyState) rebuildErrorIntoReturn(operand LocalID, operandT Type, sp Span) {
	retT := bs.fn.ReturnType
	// Fast path: types match exactly (e.g. `Result<A, E>?` inside a fn
	// that also returns `Result<A, E>`). Just copy.
	if typesMatch(operandT, retT) {
		bs.emit(&AssignInstr{
			Dest:  Place{Local: bs.fn.ReturnLocal},
			Src:   &UseRV{Op: &CopyOp{Place: Place{Local: operand}, T: operandT}},
			SpanV: sp,
		})
		return
	}
	switch classifyQShape(operandT) {
	case qShapeOption:
		// Option<A> propagating into any Option<B> (or B?): the error
		// value is just None of the return type.
		bs.emit(&AssignInstr{
			Dest:  Place{Local: bs.fn.ReturnLocal},
			Src:   &NullaryRV{Kind: NullaryNone, T: retT},
			SpanV: sp,
		})
	case qShapeResult:
		// Result<A, E> propagating into Result<B, E>: extract the Err
		// payload, rebuild as Err(payload) in the return type.
		errT := resultErrType(operandT)
		errPayload := Place{Local: operand}.Project(&VariantProj{
			Variant:  int(errTagOf(operandT)),
			Name:     "Err",
			FieldIdx: 0,
			Type:     errT,
		})
		bs.emit(&AssignInstr{
			Dest: Place{Local: bs.fn.ReturnLocal},
			Src: &AggregateRV{
				Kind:       AggEnumVariant,
				Fields:     []Operand{&CopyOp{Place: errPayload, T: errT}},
				T:          retT,
				VariantIdx: int(errTagOf(retT)),
				VariantTag: "Err",
			},
			SpanV: sp,
		})
	default:
		// Unknown propagation target — conservative copy, note an
		// issue so callers stay on the HIR path.
		bs.l.noteIssue("? propagation: cannot rebuild %s in return type %s",
			typeString(operandT), typeString(retT))
		bs.emit(&AssignInstr{
			Dest:  Place{Local: bs.fn.ReturnLocal},
			Src:   &UseRV{Op: &CopyOp{Place: Place{Local: operand}, T: operandT}},
			SpanV: sp,
		})
	}
}

// qShape classifies a type for `?` propagation purposes.
type qShape int

const (
	qShapeUnknown qShape = iota
	qShapeOption
	qShapeResult
)

func classifyQShape(t Type) qShape {
	switch x := t.(type) {
	case *ir.OptionalType:
		return qShapeOption
	case *ir.NamedType:
		switch x.Name {
		case "Option", "Maybe":
			return qShapeOption
		case "Result":
			return qShapeResult
		}
	}
	return qShapeUnknown
}

func resultErrType(t Type) Type {
	if nt, ok := t.(*ir.NamedType); ok && nt.Name == "Result" && len(nt.Args) >= 2 {
		return nt.Args[1]
	}
	return ir.ErrTypeVal
}

// errTagOf returns the discriminant value for the error arm of a `?`-
// able enum (Err / None). For Option/Maybe the error arm is None == 0;
// for Result, Err == 0.
func errTagOf(t Type) int64 {
	return 0
}

// typesMatch is a cheap structural check used to decide when a `?`
// propagation can just copy the receiver into the return slot without
// re-wrapping. We rely on ir.Type.String() — it is canonical post-
// monomorphisation and cheap to produce.
func typesMatch(a, b Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}

// lowerOptionalFieldInto handles `x?.field`.
func (bs *bodyState) lowerOptionalFieldInto(fe *ir.FieldExpr, dest Place, destT Type) {
	recvT := fe.X.Type()
	recvLocal := bs.newLocal("_opt", recvT, false, fe.SpanV)
	bs.emit(&StorageLiveInstr{Local: recvLocal, SpanV: fe.SpanV})
	bs.lowerExprInto(fe.X, recvLocal, recvT)
	disc := bs.freshTemp(TInt, fe.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: disc},
		Src:   &DiscriminantRV{Place: Place{Local: recvLocal}, T: TInt},
		SpanV: fe.SpanV,
	})
	someBB := bs.newBlock(fe.SpanV)
	noneBB := bs.newBlock(fe.SpanV)
	merge := bs.newBlock(fe.SpanV)
	bs.terminate(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: disc}, T: TInt},
		Cases:     []SwitchCase{{Value: someTagOf(recvT), Target: someBB, Label: "Some"}},
		Default:   noneBB,
		SpanV:     fe.SpanV,
	})
	bs.cur = someBB
	// Extract payload, project field, wrap back in Some.
	innerT := optionInnerType(recvT)
	payloadProj := &VariantProj{Variant: int(someTagOf(recvT)), Name: "Some", FieldIdx: 0, Type: innerT}
	info := bs.l.structFromType(innerT, "")
	fieldT := bs.l.fieldType(info, fe.Name)
	idx := bs.l.fieldIndex(info, fe.Name)
	fieldPlace := Place{Local: recvLocal}.Project(payloadProj).Project(&FieldProj{Index: idx, Name: fe.Name, Type: fieldT})
	// destT is T? (Option wrapper); wrap.
	bs.emit(&AssignInstr{
		Dest: dest,
		Src: &AggregateRV{
			Kind:       AggEnumVariant,
			Fields:     []Operand{&CopyOp{Place: fieldPlace, T: fieldT}},
			T:          destT,
			VariantIdx: int(someTagOf(destT)),
			VariantTag: "Some",
		},
		SpanV: fe.SpanV,
	})
	bs.terminate(&GotoTerm{Target: merge, SpanV: fe.SpanV})
	bs.cur = noneBB
	bs.emit(&AssignInstr{
		Dest: dest,
		Src: &NullaryRV{
			Kind: NullaryNone,
			T:    destT,
		},
		SpanV: fe.SpanV,
	})
	bs.terminate(&GotoTerm{Target: merge, SpanV: fe.SpanV})
	bs.cur = merge
}

// ==== calls ====

func (bs *bodyState) lowerCallExprInto(c *ir.CallExpr, dest *Place, destT Type) {
	// Concurrency-intrinsic fast path:
	//
	//   - Bare Ident callee: `taskGroup(...)`, `parallel(...)`,
	//     `race(...)`, `collectAll(...)`. These reach prelude-visible
	//     names so `qualifier == ""`.
	//   - FieldExpr callee whose X names a use alias for a concurrency
	//     module: `thread.spawn(f)`, `std.thread.chan::<Int>(0)`. The
	//     qualifier is the use decl's raw path or alias; we lookup
	//     the intrinsic and emit it directly.
	if id, ok := c.Callee.(*ir.Ident); ok {
		if kind := concurrencyIntrinsicForFree("", id.Name); kind != IntrinsicInvalid {
			bs.emitConcurrencyIntrinsic(kind, c.Args, dest, destT, c.SpanV)
			return
		}
	}
	if fx, ok := c.Callee.(*ir.FieldExpr); ok {
		if use := bs.l.useAliasFor(fx.X); use != nil {
			if kind := concurrencyIntrinsicForFree(qualifierOf(use), fx.Name); kind != IntrinsicInvalid {
				bs.emitConcurrencyIntrinsic(kind, c.Args, dest, destT, c.SpanV)
				return
			}
			if kind := runtimeIntrinsicForFree(qualifierOf(use), fx.Name); kind != IntrinsicInvalid {
				bs.emitRuntimeIntrinsic(kind, c.Args, dest, destT, c.SpanV)
				return
			}
		}
	}
	args, callee := bs.resolveCall(c)
	if callee == nil {
		bs.l.noteIssue("unsupported call: callee %T", c.Callee)
		return
	}
	if dest != nil && isUnit(destT) {
		dest = nil
	}
	bs.emit(&CallInstr{Dest: dest, Callee: callee, Args: args, SpanV: c.SpanV})
}

// emitConcurrencyIntrinsic lowers args in source order and emits a
// single IntrinsicInstr with the given kind. The destination is
// discarded (set to nil) for kinds whose runtime ABI returns no value
// (send, close, cancel, yield).
func (bs *bodyState) emitConcurrencyIntrinsic(kind IntrinsicKind, args []ir.Arg, dest *Place, destT Type, sp Span) {
	out := make([]Operand, len(args))
	for i, a := range args {
		out[i] = bs.lowerExprAsOperand(a.Value)
	}
	destPtr := dest
	if destPtr != nil && isUnit(destT) {
		destPtr = nil
	}
	if isVoidConcurrencyIntrinsic(kind) {
		destPtr = nil
	}
	bs.emit(&IntrinsicInstr{Dest: destPtr, Kind: kind, Args: out, SpanV: sp})
}

// emitRuntimeIntrinsic lowers args in source order and emits a single
// IntrinsicInstr for a §19 runtime intrinsic. Void-returning kinds
// (free / zero / copy / write) will need their own dest-clearing
// branch when they land; today every supported runtime intrinsic
// returns a value, so the unit-check above suffices.
func (bs *bodyState) emitRuntimeIntrinsic(kind IntrinsicKind, args []ir.Arg, dest *Place, destT Type, sp Span) {
	out := make([]Operand, len(args))
	for i, a := range args {
		out[i] = bs.lowerExprAsOperand(a.Value)
	}
	destPtr := dest
	if destPtr != nil && isUnit(destT) {
		destPtr = nil
	}
	bs.emit(&IntrinsicInstr{Dest: destPtr, Kind: kind, Args: out, SpanV: sp})
}

// isVoidConcurrencyIntrinsic reports whether an intrinsic produces no
// MIR-visible value (send, close, cancel, yield, select arms). Used
// so that calls in expression position do not carry a stale dest.
func isVoidConcurrencyIntrinsic(kind IntrinsicKind) bool {
	switch kind {
	case IntrinsicChanSend, IntrinsicChanClose,
		IntrinsicGroupCancel, IntrinsicYield,
		IntrinsicSelectRecv, IntrinsicSelectSend,
		IntrinsicSelectTimeout, IntrinsicSelectDefault:
		return true
	}
	return false
}

// resolveCall figures out the callee and reorders keyword args.
func (bs *bodyState) resolveCall(c *ir.CallExpr) ([]Operand, Callee) {
	switch cal := c.Callee.(type) {
	case *ir.Ident:
		if cal.Kind == ir.IdentVariant {
			// variant construction via call-like syntax; return nil to
			// let the caller re-route through lowerExprToRValue.
			return nil, nil
		}
		// Local / param idents holding a fn-typed value route through
		// an indirect call — they are NOT module symbols. Globals
		// that happen to hold a fn value would also land here today,
		// but MIR lowering for global reads still goes through the
		// generic path; revisit if that shape becomes common.
		if cal.Kind == ir.IdentLocal || cal.Kind == ir.IdentParam {
			calleeOp := bs.lowerIdent(cal)
			args := bs.orderArgs(c.Args, nil)
			return args, &IndirectCall{Callee: calleeOp}
		}
		sig := bs.l.signatureForFn(cal.Name)
		args := bs.orderArgs(c.Args, sig)
		ft := cal.T
		return args, &FnRef{Symbol: cal.Name, Type: ft}
	case *ir.FieldExpr:
		// Callee shaped `x.name(...)`. In practice HIR's own lowerer
		// routes user-visible method syntax through MethodCall, so the
		// only ways this branch fires are:
		//
		//   - package-qualified calls: `strings.Split(s, ",")` — `x` is
		//     a Use alias and the call is a plain symbol call, NOT a
		//     receiver-method call.
		//   - first-class field holding a callable value: `r.handler(x)`
		//     when `handler` is a closure / fn-typed field.
		//
		// Treating `x` as a receiver for the first case would produce
		// `Split(strings, s, ",")`, which is the wrong ABI and loses
		// the qualifier identity. So: classify before lowering.
		if use := bs.l.useAliasFor(cal.X); use != nil {
			return bs.resolveQualifiedCall(use, cal.Name, cal.T, c.Args)
		}
		// Not a package alias — fall back to an indirect call through
		// the resolved field value. This preserves the fn-pointer-as-
		// field shape without guessing at receiver ABI.
		recvPlace, ok := bs.lowerExprToPlace(cal.X)
		var calleeOp Operand
		if ok {
			fieldType := cal.T
			info := bs.l.structFromType(cal.X.Type(), "")
			idx := bs.l.fieldIndex(info, cal.Name)
			proj := &FieldProj{Index: idx, Name: cal.Name, Type: fieldType}
			calleeOp = &CopyOp{Place: recvPlace.Project(proj), T: fieldType}
		} else {
			calleeOp = bs.lowerExprAsOperand(c.Callee)
		}
		args := bs.orderArgs(c.Args, nil)
		return args, &IndirectCall{Callee: calleeOp}
	}
	// Indirect call via operand.
	args := bs.orderArgs(c.Args, nil)
	callee := bs.lowerExprAsOperand(c.Callee)
	return args, &IndirectCall{Callee: callee}
}

// resolveQualifiedCall lowers `alias.name(args...)` where `alias` is a
// `use` import. The returned Callee is a direct FnRef whose symbol
// encodes the qualifier, and the argument list does NOT include a
// synthetic receiver.
func (bs *bodyState) resolveQualifiedCall(use *ir.UseDecl, name string, t Type, args []ir.Arg) ([]Operand, Callee) {
	out := make([]Operand, len(args))
	for i, a := range args {
		out[i] = bs.lowerExprAsOperand(a.Value)
	}
	return out, &FnRef{Symbol: qualifiedSymbol(use, name), Type: t}
}

// qualifiedSymbol returns the MIR-visible symbol for a package- or
// FFI-qualified call. We keep the qualifier in the symbol so backends
// can route the call to the right module (Go FFI bridge, runtime ABI
// symbol, or a regular Osty package). Backends do their own mangling
// on top.
func qualifiedSymbol(use *ir.UseDecl, name string) string {
	if use == nil {
		return name
	}
	switch {
	case use.IsRuntimeFFI && use.RuntimePath != "":
		return use.RuntimePath + "." + name
	case use.IsGoFFI && use.GoPath != "":
		return use.GoPath + "." + name
	}
	if use.RawPath != "" {
		return use.RawPath + "." + name
	}
	if use.Alias != "" {
		return use.Alias + "." + name
	}
	return name
}

// useAliasFor returns the use decl whose alias matches the ident, or
// nil if expr is not a bare alias reference.
func (l *lowerer) useAliasFor(e ir.Expr) *ir.UseDecl {
	id, ok := e.(*ir.Ident)
	if !ok {
		return nil
	}
	if id.Name == "" {
		return nil
	}
	return l.useAliases[id.Name]
}

func (bs *bodyState) orderArgs(args []ir.Arg, sig *fnSignature) []Operand {
	if sig == nil {
		// No signature available — preserve source order, ignore kw
		// names. Safe for direct positional calls.
		out := make([]Operand, len(args))
		for i, a := range args {
			out[i] = bs.lowerExprAsOperand(a.Value)
		}
		return out
	}
	// Map kw → index.
	paramCount := len(sig.params)
	out := make([]Operand, paramCount)
	filled := make([]bool, paramCount)
	// positional first
	pos := 0
	for _, a := range args {
		if a.IsKeyword() {
			for i, p := range sig.params {
				if p.Name == a.Name {
					out[i] = bs.lowerExprAsOperand(a.Value)
					filled[i] = true
					break
				}
			}
			continue
		}
		if pos < paramCount {
			out[pos] = bs.lowerExprAsOperand(a.Value)
			filled[pos] = true
		}
		pos++
	}
	// defaults for any unfilled.
	for i, f := range filled {
		if f {
			continue
		}
		p := sig.params[i]
		if p.Default != nil {
			out[i] = bs.lowerExprAsOperand(p.Default)
			continue
		}
		// leave nil; validator catches it if backend relies on arity.
		bs.l.noteIssue("call %q: unfilled param %d (%s)", sig.symbol, i, p.Name)
	}
	return out
}

func (bs *bodyState) lowerMethodCallInto(mc *ir.MethodCall, dest Place, destT Type) {
	// Package/FFI-qualified calls land here when HIR lowered `pkg.fn()`
	// into MethodCall{Receiver: Ident(pkg), Name: fn}. The receiver is
	// a use alias, not a value — fast-path to a concurrency intrinsic
	// when the qualifier targets `thread`, or emit a direct qualified
	// call with no synthetic `self` argument otherwise.
	if use := bs.l.useAliasFor(mc.Receiver); use != nil {
		if kind := concurrencyIntrinsicForFree(qualifierOf(use), mc.Name); kind != IntrinsicInvalid {
			bs.emitConcurrencyIntrinsic(kind, mc.Args, &dest, destT, mc.SpanV)
			return
		}
		if kind := runtimeIntrinsicForFree(qualifierOf(use), mc.Name); kind != IntrinsicInvalid {
			bs.emitRuntimeIntrinsic(kind, mc.Args, &dest, destT, mc.SpanV)
			return
		}
		args := make([]Operand, len(mc.Args))
		for i, a := range mc.Args {
			args[i] = bs.lowerExprAsOperand(a.Value)
		}
		destPtr := &dest
		if isUnit(destT) {
			destPtr = nil
		}
		bs.emit(&CallInstr{
			Dest:   destPtr,
			Callee: &FnRef{Symbol: qualifiedSymbol(use, mc.Name), Type: mc.T},
			Args:   args,
			SpanV:  mc.SpanV,
		})
		return
	}

	// Concurrency-intrinsic fast path for method syntax on runtime-
	// owned concurrency types: `ch.recv()`, `ch.close()`, `h.join()`,
	// `g.spawn(f)`, `g.cancel()`, `g.isCancelled()`. The receiver
	// becomes the first intrinsic arg; user args follow in source
	// order.
	if kind := concurrencyIntrinsicForMethod(mc.Receiver.Type(), mc.Name); kind != IntrinsicInvalid {
		recv := bs.lowerExprAsOperand(mc.Receiver)
		args := []Operand{recv}
		for _, a := range mc.Args {
			args = append(args, bs.lowerExprAsOperand(a.Value))
		}
		destPtr := &dest
		if isUnit(destT) {
			destPtr = nil
		}
		if isVoidConcurrencyIntrinsic(kind) {
			destPtr = nil
		}
		bs.emit(&IntrinsicInstr{
			Dest:  destPtr,
			Kind:  kind,
			Args:  args,
			SpanV: mc.SpanV,
		})
		return
	}

	// Stdlib-intrinsic fast path: List / Map / Set / String / Bytes /
	// Option / Result methods. Matches the concurrency path above but
	// for the primitive types whose method bodies in stdlib just
	// return default values — the runtime handles the real work.
	if kind := stdlibIntrinsicForMethod(mc.Receiver.Type(), mc.Name); kind != IntrinsicInvalid {
		recv := bs.lowerExprAsOperand(mc.Receiver)
		args := []Operand{recv}
		for _, a := range mc.Args {
			args = append(args, bs.lowerExprAsOperand(a.Value))
		}
		destPtr := &dest
		if isUnit(destT) {
			destPtr = nil
		}
		if isVoidStdlibIntrinsic(kind) {
			destPtr = nil
		}
		bs.emit(&IntrinsicInstr{
			Dest:  destPtr,
			Kind:  kind,
			Args:  args,
			SpanV: mc.SpanV,
		})
		return
	}

	recv := bs.lowerExprAsOperand(mc.Receiver)
	typeName := typeNameOf(mc.Receiver.Type())
	sig := bs.l.signatureForMethod(typeName, mc.Name)
	symbol := mc.Name
	if sig != nil {
		symbol = sig.symbol
	} else if typeName != "" {
		symbol = mangleMethodSymbol(typeName, mc.Name)
	}
	callArgs := []Operand{recv}
	var rest []Operand
	if sig != nil {
		// The stored signature has `self` as its first parameter;
		// build a virtual signature covering only the non-receiver
		// params so orderArgs/kwargs lookups match the call site.
		rest = bs.orderArgs(mc.Args, methodCallSig(sig))
	} else {
		rest = make([]Operand, len(mc.Args))
		for i, a := range mc.Args {
			rest[i] = bs.lowerExprAsOperand(a.Value)
		}
	}
	callArgs = append(callArgs, rest...)
	destPtr := &dest
	if isUnit(destT) {
		destPtr = nil
	}
	bs.emit(&CallInstr{
		Dest:   destPtr,
		Callee: &FnRef{Symbol: symbol, Type: mc.T},
		Args:   callArgs,
		SpanV:  mc.SpanV,
	})
}

func (bs *bodyState) lowerMapLitInto(m *ir.MapLit, dest Place, destT Type) {
	mapT := destT
	if mapT == nil {
		mapT = m.Type()
	}
	destPtr := &dest
	if isUnit(mapT) {
		destPtr = nil
	}
	bs.emit(&IntrinsicInstr{
		Dest:  destPtr,
		Kind:  IntrinsicMapNew,
		SpanV: m.SpanV,
	})
	if len(m.Entries) == 0 {
		return
	}
	for _, entry := range m.Entries {
		bs.emit(&IntrinsicInstr{
			Kind: IntrinsicMapSet,
			Args: []Operand{
				&CopyOp{Place: dest, T: mapT},
				bs.lowerExprAsOperand(entry.Key),
				bs.lowerExprAsOperand(entry.Value),
			},
			SpanV: entry.SpanV,
		})
	}
}

func (bs *bodyState) lowerIntrinsicCallInto(ic *ir.IntrinsicCall, dest *Place) {
	args := make([]Operand, len(ic.Args))
	for i, a := range ic.Args {
		args[i] = bs.lowerExprAsOperand(a.Value)
	}
	kind := IntrinsicInvalid
	switch ic.Kind {
	case ir.IntrinsicPrint:
		kind = IntrinsicPrint
	case ir.IntrinsicPrintln:
		kind = IntrinsicPrintln
	case ir.IntrinsicEprint:
		kind = IntrinsicEprint
	case ir.IntrinsicEprintln:
		kind = IntrinsicEprintln
	}
	bs.emit(&IntrinsicInstr{Dest: dest, Kind: kind, Args: args, SpanV: ic.SpanV})
}

func (bs *bodyState) lowerStructLit(sl *ir.StructLit, hint Type) RValue {
	t := sl.T
	if t == nil {
		t = hint
	}
	info := bs.l.structFromType(t, sl.TypeName)
	fieldOrder := bs.l.fieldNames(info)
	if len(fieldOrder) == 0 {
		// Unknown struct shape: emit a best-effort aggregate with the
		// provided fields in source order.
		fields := make([]Operand, len(sl.Fields))
		for i, f := range sl.Fields {
			fields[i] = bs.lowerExprAsOperand(f.Value)
		}
		return &AggregateRV{Kind: AggStruct, Fields: fields, T: t}
	}
	fields := make([]Operand, len(fieldOrder))
	filled := make([]bool, len(fieldOrder))
	for _, f := range sl.Fields {
		i := indexOf(fieldOrder, f.Name)
		if i < 0 {
			bs.l.noteIssue("struct lit: unknown field %q", f.Name)
			continue
		}
		if f.Value == nil {
			// shorthand: lookup local
			if id, ok := bs.lookup(f.Name); ok {
				fields[i] = &CopyOp{Place: Place{Local: id}, T: bs.fn.Local(id).Type}
				filled[i] = true
				continue
			}
		} else {
			fields[i] = bs.lowerExprAsOperand(f.Value)
			filled[i] = true
		}
	}
	if sl.Spread != nil {
		spread, ok := bs.lowerExprToPlace(sl.Spread)
		if !ok {
			spreadT := sl.Spread.Type()
			tmp := bs.freshTemp(spreadT, exprSpan(sl.Spread))
			bs.lowerExprInto(sl.Spread, tmp, spreadT)
			spread = Place{Local: tmp}
		}
		for i, name := range fieldOrder {
			if filled[i] {
				continue
			}
			ft := bs.l.fieldType(info, name)
			fields[i] = &CopyOp{
				Place: spread.Project(&FieldProj{Index: i, Name: name, Type: ft}),
				T:     ft,
			}
		}
	}
	// Any remaining unfilled slot: default value.
	for i, ok := range filled {
		if ok || fields[i] != nil {
			continue
		}
		bs.l.noteIssue("struct lit: missing field %q", fieldOrder[i])
		fields[i] = &ConstOp{Const: &UnitConst{}, T: TUnit}
	}
	return &AggregateRV{Kind: AggStruct, Fields: fields, T: t}
}

func (bs *bodyState) lowerVariantLit(v *ir.VariantLit, hint Type) RValue {
	t := v.T
	if t == nil {
		t = hint
	}
	idx := bs.l.variantIndexByName(t, v.Variant)
	fields := make([]Operand, len(v.Args))
	for i, a := range v.Args {
		fields[i] = bs.lowerExprAsOperand(a.Value)
	}
	return &AggregateRV{
		Kind:       AggEnumVariant,
		Fields:     fields,
		T:          t,
		VariantIdx: idx,
		VariantTag: v.Variant,
	}
}

// ==== concurrency intrinsic recognition ====

// runtimeIntrinsicForFree maps a `std.runtime.raw`-qualified free
// function call to its MIR intrinsic kind, per LANG_SPEC §19.5.
// Returns IntrinsicInvalid when the qualifier/name pair is not a
// known runtime intrinsic.
//
// `qualifier` is the `use`-level RawPath; for `use std.runtime.raw`
// that is the literal "std.runtime.raw". Aliased forms
// (`use std.runtime.raw as raw`) would arrive here with the same
// RawPath, so the alias name is irrelevant — the resolver canonicalises
// to the full path.
func runtimeIntrinsicForFree(qualifier, name string) IntrinsicKind {
	if qualifier != "std.runtime.raw" {
		return IntrinsicInvalid
	}
	switch name {
	case "null":
		return IntrinsicRawNull
	}
	return IntrinsicInvalid
}

// concurrencyIntrinsicForFree maps a prelude / `thread`-namespaced
// top-level function name to its MIR intrinsic kind. Returns
// IntrinsicInvalid when the name is not a known concurrency primitive.
//
// The lowerer calls this for `Ident{Kind: IdentFn}` callees and for
// package-qualified callees whose use decl targets `std.thread` —
// the function call paths normalise both sources into the bare name
// here. `qualifier` is the `use`-level prefix (e.g. "std.thread",
// "thread") when the call was qualified, or "" for prelude-visible
// names like `taskGroup`.
func concurrencyIntrinsicForFree(qualifier, name string) IntrinsicKind {
	threadNs := qualifier == "thread" || qualifier == "std.thread"
	preludeOnly := qualifier == ""
	switch name {
	case "taskGroup":
		if preludeOnly {
			return IntrinsicTaskGroup
		}
	case "parallel":
		if preludeOnly {
			return IntrinsicParallel
		}
	case "race":
		if preludeOnly || threadNs {
			return IntrinsicRace
		}
	case "collectAll":
		if preludeOnly || threadNs {
			return IntrinsicCollectAll
		}
	case "spawn":
		if threadNs {
			return IntrinsicSpawn
		}
	case "chan":
		if threadNs {
			return IntrinsicChanMake
		}
	case "select":
		if threadNs {
			return IntrinsicSelect
		}
	case "isCancelled":
		if threadNs {
			return IntrinsicIsCancelled
		}
	case "checkCancelled":
		if threadNs {
			return IntrinsicCheckCancelled
		}
	case "yield":
		if threadNs {
			return IntrinsicYield
		}
	case "sleep":
		if threadNs {
			return IntrinsicSleep
		}
	}
	return IntrinsicInvalid
}

// concurrencyIntrinsicForMethod maps a (receiverType, methodName)
// pair to its MIR intrinsic kind. Used for method calls like
// `ch.recv()`, `g.spawn(f)`, `h.join()`. Returns IntrinsicInvalid
// when not a concurrency method.
func concurrencyIntrinsicForMethod(receiverType Type, name string) IntrinsicKind {
	recv := typeNameOf(receiverType)
	switch recv {
	case "Channel":
		switch name {
		case "recv":
			return IntrinsicChanRecv
		case "close":
			return IntrinsicChanClose
		case "isClosed":
			return IntrinsicChanIsClosed
		}
	case "Handle":
		if name == "join" {
			return IntrinsicHandleJoin
		}
	case "Group", "TaskGroup":
		switch name {
		case "spawn":
			return IntrinsicSpawn
		case "cancel":
			return IntrinsicGroupCancel
		case "isCancelled":
			return IntrinsicGroupIsCancelled
		}
	case "Select":
		// Arms of the `thread.select(|s| body)` DSL. Each method is
		// a builder call whose signature in stdlib returns `self`, but
		// the runtime treats them as arm registrations. MIR makes that
		// explicit by emitting a dedicated intrinsic per arm.
		switch name {
		case "recv":
			return IntrinsicSelectRecv
		case "send":
			return IntrinsicSelectSend
		case "timeout":
			return IntrinsicSelectTimeout
		case "default":
			return IntrinsicSelectDefault
		}
	}
	return IntrinsicInvalid
}

// stdlibIntrinsicForMethod maps a (receiverType, methodName) pair on
// the built-in primitive types (List, Map, Set, String, Bytes,
// Option, Result) to its MIR intrinsic kind. Returns
// IntrinsicInvalid when the call is not a recognised stdlib method.
//
// The recogniser runs *after* the concurrency recogniser in the
// method-call path so concurrency receivers (Channel, Handle, Group,
// Select) never accidentally shadow the primitive names.
func stdlibIntrinsicForMethod(receiverType Type, name string) IntrinsicKind {
	// Option<T> has two surface forms — NamedType{"Option"} and
	// OptionalType (surface `T?`) — handle OptionalType first so the
	// ordinary typeNameOf branch below doesn't need a special case.
	if _, ok := receiverType.(*ir.OptionalType); ok {
		switch name {
		case "isSome":
			return IntrinsicOptionIsSome
		case "isNone":
			return IntrinsicOptionIsNone
		case "unwrap":
			return IntrinsicOptionUnwrap
		case "unwrapOr":
			return IntrinsicOptionUnwrapOr
		}
		return IntrinsicInvalid
	}
	// String and Bytes are primitive types, not named types, so a
	// plain typeNameOf() lookup misses them. Dispatch by PrimKind
	// first, then fall through to the named-type table for List /
	// Map / Set / Option / Result / Maybe.
	if pt, ok := receiverType.(*ir.PrimType); ok {
		switch pt.Kind {
		case ir.PrimString:
			return stringIntrinsicForMethod(name)
		case ir.PrimBytes:
			return bytesIntrinsicForMethod(name)
		}
		return IntrinsicInvalid
	}
	switch typeNameOf(receiverType) {
	case "List":
		switch name {
		case "push":
			return IntrinsicListPush
		case "len":
			return IntrinsicListLen
		case "get":
			return IntrinsicListGet
		case "isEmpty":
			return IntrinsicListIsEmpty
		case "first":
			return IntrinsicListFirst
		case "last":
			return IntrinsicListLast
		case "sorted":
			return IntrinsicListSorted
		case "contains":
			return IntrinsicListContains
		case "indexOf":
			return IntrinsicListIndexOf
		case "toSet":
			return IntrinsicListToSet
		}
	case "Map":
		switch name {
		case "get":
			return IntrinsicMapGet
		case "set":
			return IntrinsicMapSet
		case "contains":
			return IntrinsicMapContains
		case "len":
			return IntrinsicMapLen
		case "keys":
			return IntrinsicMapKeys
		case "values":
			return IntrinsicMapValues
		case "remove":
			return IntrinsicMapRemove
		}
	case "Set":
		switch name {
		case "insert":
			return IntrinsicSetInsert
		case "contains":
			return IntrinsicSetContains
		case "len":
			return IntrinsicSetLen
		case "toList":
			return IntrinsicSetToList
		case "remove":
			return IntrinsicSetRemove
		}
	case "Option", "Maybe":
		switch name {
		case "isSome":
			return IntrinsicOptionIsSome
		case "isNone":
			return IntrinsicOptionIsNone
		case "unwrap":
			return IntrinsicOptionUnwrap
		case "unwrapOr":
			return IntrinsicOptionUnwrapOr
		}
	case "Result":
		switch name {
		case "isOk":
			return IntrinsicResultIsOk
		case "isErr":
			return IntrinsicResultIsErr
		case "unwrap":
			return IntrinsicResultUnwrap
		case "unwrapOr":
			return IntrinsicResultUnwrapOr
		}
	}
	return IntrinsicInvalid
}

// isVoidStdlibIntrinsic reports whether a stdlib intrinsic produces
// no MIR-visible value. Used so that `list.push(x)` called in
// expression position doesn't carry a stale dest.
func isVoidStdlibIntrinsic(kind IntrinsicKind) bool {
	switch kind {
	case IntrinsicListPush, IntrinsicMapSet, IntrinsicMapRemove,
		IntrinsicSetInsert:
		return true
	}
	return false
}

// stringIntrinsicForMethod looks up a method name against the String
// primitive. Split out from stdlibIntrinsicForMethod because String
// has no NamedType form.
func stringIntrinsicForMethod(name string) IntrinsicKind {
	switch name {
	case "len":
		return IntrinsicStringLen
	case "isEmpty":
		return IntrinsicStringIsEmpty
	case "contains":
		return IntrinsicStringContains
	case "startsWith":
		return IntrinsicStringStartsWith
	case "endsWith":
		return IntrinsicStringEndsWith
	case "indexOf":
		return IntrinsicStringIndexOf
	case "split":
		return IntrinsicStringSplit
	case "trim":
		return IntrinsicStringTrim
	case "toUpper":
		return IntrinsicStringToUpper
	case "toLower":
		return IntrinsicStringToLower
	case "replace":
		return IntrinsicStringReplace
	case "chars":
		return IntrinsicStringChars
	case "bytes":
		return IntrinsicStringBytes
	}
	return IntrinsicInvalid
}

// bytesIntrinsicForMethod looks up a method name against the Bytes
// primitive.
func bytesIntrinsicForMethod(name string) IntrinsicKind {
	switch name {
	case "len":
		return IntrinsicBytesLen
	case "isEmpty":
		return IntrinsicBytesIsEmpty
	case "get":
		return IntrinsicBytesGet
	case "contains":
		return IntrinsicBytesContains
	case "startsWith":
		return IntrinsicBytesStartsWith
	case "indexOf":
		return IntrinsicBytesIndexOf
	case "concat":
		return IntrinsicBytesConcat
	case "repeat":
		return IntrinsicBytesRepeat
	}
	return IntrinsicInvalid
}

// qualifierOf extracts the package qualifier from a Use decl, or ""
// when no use is associated. Used for concurrency-intrinsic lookup.
func qualifierOf(use *ir.UseDecl) string {
	if use == nil {
		return ""
	}
	if use.RawPath != "" {
		return use.RawPath
	}
	if use.Alias != "" {
		return use.Alias
	}
	return ""
}

// ==== closures ====

// closureEnvType is the MIR type for a closure env pointer. It prints
// as `ClosureEnv` and the LLVM emitter maps it to `ptr`.
var closureEnvType = &ir.NamedType{Name: "ClosureEnv", Builtin: true}

// lowerClosure lifts a Closure body into a fresh top-level MIR
// function and materialises the closure value as an
// `AggregateRV{Kind: AggClosure}` whose first field is the fn symbol
// and whose remaining fields are the captured operands.
//
// The lifted function uses an **env-first-arg ABI**:
//
//	fn <symbol>(env ClosureEnv, userArg0, userArg1, ...) -> R
//
// where `env` is a pointer to a heap-boxed struct
// `{ ptr fn, <cap0>, <cap1>, ... }`. Inside the body, capture
// references resolve to per-capture locals populated at entry via
// `DerefProj + TupleProj` loads off the env. This uniform ABI lets
// the MIR emitter dispatch all `IndirectCall` sites through the same
// `load fn ptr from env[0]; call fn(env, user_args...)` sequence
// regardless of whether the closure captures.
func (bs *bodyState) lowerClosure(cl *ir.Closure, hint Type) RValue {
	if cl == nil {
		return &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}}
	}
	closureT := cl.T
	if closureT == nil {
		closureT = hint
	}
	if closureT == nil {
		closureT = ir.ErrTypeVal
	}
	symbol := bs.l.nextClosureSymbol(bs.fn.Name)
	retT := cl.Return
	if retT == nil {
		retT = TUnit
	}

	// Build the lifted function. First param is the env ptr; user
	// params follow. Captures are NOT params — the entry block loads
	// them from the env.
	lifted := &Function{Name: symbol, ReturnType: retT, SpanV: cl.SpanV}
	lifted.ReturnLocal = lifted.NewLocal("_return", retT, true, cl.SpanV)
	lifted.Locals[lifted.ReturnLocal].IsReturn = true

	envLocal := lifted.NewLocal("_env", closureEnvType, false, cl.SpanV)
	lifted.Locals[envLocal].IsParam = true
	lifted.Params = append(lifted.Params, envLocal)

	// User-level parameters.
	for _, p := range cl.Params {
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("arg%d", len(lifted.Params)-1)
		}
		pt := p.Type
		if pt == nil {
			pt = ir.ErrTypeVal
		}
		id := lifted.NewLocal(name, pt, false, p.SpanV)
		lifted.Locals[id].IsParam = true
		lifted.Params = append(lifted.Params, id)
	}

	// Entry block and body.
	lifted.NewBlock(cl.SpanV)
	lifted.Entry = 0
	liftedBS := newBodyState(bs.l, lifted)

	// Env struct layout: `(ptr fn, cap0_type, cap1_type, ...)`. Slot 0
	// holds the fn pointer at runtime; slots 1..N hold captures.
	envStructElems := make([]ir.Type, 0, 1+len(cl.Captures))
	envStructElems = append(envStructElems, closureEnvType) // ptr for fn slot
	for _, cap := range cl.Captures {
		ct := cap.T
		if ct == nil {
			ct = ir.ErrTypeVal
		}
		envStructElems = append(envStructElems, ct)
	}
	envStructType := &ir.TupleType{Elems: envStructElems}

	// Emit per-capture loads at the top of the lifted body. Each
	// capture gets a dedicated local bound to its HIR-level name so
	// the rest of the body lowers unchanged — reads of the capture
	// resolve through the regular locals lookup.
	for i, cap := range cl.Captures {
		ct := cap.T
		if ct == nil {
			ct = ir.ErrTypeVal
		}
		capName := cap.Name
		if capName == "" {
			capName = fmt.Sprintf("_cap%d", i)
		}
		capLocal := lifted.NewLocal(capName, ct, cap.Mut, cap.SpanV)
		liftedBS.emit(&AssignInstr{
			Dest: Place{Local: capLocal},
			Src: &UseRV{Op: &CopyOp{
				Place: Place{
					Local: envLocal,
					Projections: []Projection{
						&DerefProj{Type: envStructType},
						&TupleProj{Index: i + 1, Type: ct},
					},
				},
				T: ct,
			}},
			SpanV: cap.SpanV,
		})
		liftedBS.bind(capName, capLocal)
	}

	if cl.Body != nil {
		for _, s := range cl.Body.Stmts {
			liftedBS.lowerStmt(s)
		}
		if cl.Body.Result != nil {
			liftedBS.lowerExprInto(cl.Body.Result, lifted.ReturnLocal, retT)
			liftedBS.runDefers(cl.Body.SpanV)
			liftedBS.terminate(&ReturnTerm{SpanV: cl.Body.SpanV})
		} else {
			liftedBS.finishNoReturn()
		}
	} else {
		liftedBS.finishNoReturn()
	}
	bs.l.out.Functions = append(bs.l.out.Functions, lifted)

	// Materialise the closure value. First field is a FnConst naming
	// the lifted function; remaining fields carry the capture
	// operands read from the enclosing scope.
	fnType := &ir.FnType{Return: retT}
	for _, p := range cl.Params {
		fnType.Params = append(fnType.Params, p.Type)
	}
	fields := make([]Operand, 0, 1+len(cl.Captures))
	fields = append(fields, &ConstOp{Const: &FnConst{Symbol: symbol, T: fnType}, T: fnType})
	for _, cap := range cl.Captures {
		fields = append(fields, bs.captureOperand(cap))
	}
	return &AggregateRV{
		Kind:   AggClosure,
		Fields: fields,
		T:      closureT,
	}
}

// captureOperand produces an Operand that reads a single capture from
// the enclosing scope. A capture can resolve to a local, a parameter,
// an outer closure's prefix-capture, a top-level fn, a global, or the
// enclosing method's receiver; each case has a distinct lowering.
func (bs *bodyState) captureOperand(cap *ir.Capture) Operand {
	if cap == nil {
		return &ConstOp{Const: &UnitConst{}, T: TUnit}
	}
	t := cap.T
	if t == nil {
		t = ir.ErrTypeVal
	}
	switch cap.Kind {
	case ir.CaptureFn:
		return &ConstOp{Const: &FnConst{Symbol: cap.Name, T: t}, T: t}
	case ir.CaptureSelf:
		if bs.self != nil {
			return &CopyOp{Place: Place{Local: bs.self.localID}, T: t}
		}
	case ir.CaptureGlobal:
		tmp := bs.freshTemp(t, cap.SpanV)
		bs.emit(&AssignInstr{
			Dest:  Place{Local: tmp},
			Src:   &GlobalRefRV{Name: cap.Name, T: t},
			SpanV: cap.SpanV,
		})
		return &CopyOp{Place: Place{Local: tmp}, T: t}
	}
	// Local, param, or an outer closure's prefix capture already lives
	// in a MIR local with the same source-level name.
	if id, ok := bs.lookup(cap.Name); ok {
		return &CopyOp{Place: Place{Local: id}, T: t}
	}
	bs.l.noteIssue("closure capture %q not resolved in enclosing scope", cap.Name)
	return &ConstOp{Const: &UnitConst{}, T: TUnit}
}

// nextClosureSymbol returns a unique symbol for a lifted closure
// anchored at parent.
func (l *lowerer) nextClosureSymbol(parent string) string {
	l.closureSeq++
	if parent == "" {
		return fmt.Sprintf("closure_%d", l.closureSeq)
	}
	return fmt.Sprintf("%s__closure%d", parent, l.closureSeq)
}

// ==== misc helpers ====

func (bs *bodyState) freshTemp(t Type, sp Span) LocalID {
	return bs.newLocal("", t, false, sp)
}

// finishNoReturn ensures the current block ends in a terminator. For
// unit-returning functions we insert an implicit Return; otherwise an
// Unreachable. Callers invoke this at the end of lowerFunction when
// the HIR body did not provide a trailing expression.
func (bs *bodyState) finishNoReturn() {
	bb := bs.currentBlock()
	if bb != nil && bb.Term != nil {
		return
	}
	if isUnit(bs.fn.ReturnType) {
		// Seed return local with unit.
		bs.emit(&AssignInstr{
			Dest:  Place{Local: bs.fn.ReturnLocal},
			Src:   &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}},
			SpanV: bs.fn.SpanV,
		})
		bs.runDefers(bs.fn.SpanV)
		bs.terminate(&ReturnTerm{SpanV: bs.fn.SpanV})
		return
	}
	// Non-unit return with no explicit return — shouldn't happen for
	// well-typed Osty, but keep the function validatable.
	bs.terminate(&UnreachableTerm{SpanV: bs.fn.SpanV})
}

// ==== helpers that bridge HIR-level knowledge to layouts ====

type lookupStruct struct {
	decl *ir.StructDecl
}

func (l *lowerer) structFromType(t ir.Type, fallbackName string) *lookupStruct {
	name := fallbackName
	if name == "" {
		name = typeNameOf(t)
	}
	if name == "" {
		return &lookupStruct{}
	}
	if d, ok := l.structs[name]; ok {
		return &lookupStruct{decl: d}
	}
	return &lookupStruct{}
}

func (l *lowerer) fieldType(info *lookupStruct, name string) Type {
	if info == nil || info.decl == nil {
		return ir.ErrTypeVal
	}
	for _, f := range info.decl.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return ir.ErrTypeVal
}

func (l *lowerer) fieldIndex(info *lookupStruct, name string) int {
	if info == nil || info.decl == nil {
		return 0
	}
	for i, f := range info.decl.Fields {
		if f.Name == name {
			return i
		}
	}
	return 0
}

func (l *lowerer) fieldNames(info *lookupStruct) []string {
	if info == nil || info.decl == nil {
		return nil
	}
	out := make([]string, len(info.decl.Fields))
	for i, f := range info.decl.Fields {
		out[i] = f.Name
	}
	return out
}

type variantInfo struct {
	enum    *ir.EnumDecl
	variant *ir.Variant
	index   int
}

func (l *lowerer) variantLayout(enum, name string) *variantInfo {
	if enum == "" {
		// Builtin (Option/Result) — fall back to synthesised layout.
		return nil
	}
	e, ok := l.enums[enum]
	if !ok {
		return nil
	}
	for i, v := range e.Variants {
		if v.Name == name {
			return &variantInfo{enum: e, variant: v, index: i}
		}
	}
	return nil
}

func (l *lowerer) variantIndexByName(t ir.Type, name string) int {
	// Well-known builtins first.
	switch name {
	case "None":
		return 0
	case "Some":
		return 1
	case "Err":
		return 0
	case "Ok":
		return 1
	}
	typeName := typeNameOf(t)
	if typeName == "" {
		return 0
	}
	if e, ok := l.enums[typeName]; ok {
		for i, v := range e.Variants {
			if v.Name == name {
				return i
			}
		}
	}
	return 0
}

// ==== adapter helpers ====

func variantIndex(info *variantInfo) int {
	if info == nil {
		return 0
	}
	return info.index
}

func variantPayloadType(info *variantInfo, i int) Type {
	if info == nil || info.variant == nil {
		return ir.ErrTypeVal
	}
	if i < 0 || i >= len(info.variant.Payload) {
		return ir.ErrTypeVal
	}
	return info.variant.Payload[i]
}

// projectionToPlace turns a HIR decision-tree projection chain into a
// MIR Place rooted at the scrutinee local.
func projectionToPlace(l *lowerer, scrutLocal LocalID, scrutT ir.Type, proj *ir.Projection) Place {
	// Reverse-walk into a slice root→leaf.
	if proj == nil {
		return Place{Local: scrutLocal}
	}
	chain := flattenProjection(proj)
	out := Place{Local: scrutLocal}
	curT := scrutT
	for _, node := range chain {
		switch node.Kind {
		case ir.ProjScrutinee:
			continue
		case ir.ProjTuple:
			elemTs := tupleElementTypes(curT)
			nextT := elementTypeAt(elemTs, node.Index)
			out = out.Project(&TupleProj{Index: node.Index, Type: nextT})
			curT = nextT
		case ir.ProjField:
			info := l.structFromType(curT, "")
			nextT := l.fieldType(info, node.Field)
			out = out.Project(&FieldProj{
				Index: l.fieldIndex(info, node.Field),
				Name:  node.Field,
				Type:  nextT,
			})
			curT = nextT
		case ir.ProjVariant:
			// Select payload of a variant.
			innerT := variantLookupPayloadType(l, curT, node.Variant, 0)
			out = out.Project(&VariantProj{
				Variant:  l.variantIndexByName(curT, node.Variant),
				Name:     node.Variant,
				FieldIdx: -1,
				Type:     innerT,
			})
			curT = innerT
		case ir.ProjVariantN:
			// If a prior ProjVariant already descended into a scalar
			// payload, a following ProjVariantN(0) is redundant — skip
			// the extra projection so the MIR place stays well-typed
			// and the backend emits a single load.
			if isScalarPayload(curT) && node.Index == 0 {
				continue
			}
			nextT := variantLookupPayloadType(l, curT, node.Variant, node.Index)
			out = out.Project(&VariantProj{
				Variant:  l.variantIndexByName(curT, node.Variant),
				Name:     node.Variant,
				FieldIdx: node.Index,
				Type:     nextT,
			})
			curT = nextT
		}
	}
	return out
}

// projectionResultType returns the type produced by a projection
// chain starting from scrutT.
func projectionResultType(l *lowerer, scrutT ir.Type, proj *ir.Projection) Type {
	if proj == nil {
		return scrutT
	}
	chain := flattenProjection(proj)
	curT := scrutT
	for _, node := range chain {
		switch node.Kind {
		case ir.ProjScrutinee:
			continue
		case ir.ProjTuple:
			curT = elementTypeAt(tupleElementTypes(curT), node.Index)
		case ir.ProjField:
			info := l.structFromType(curT, "")
			curT = l.fieldType(info, node.Field)
		case ir.ProjVariant:
			curT = variantLookupPayloadType(l, curT, node.Variant, 0)
		case ir.ProjVariantN:
			curT = variantLookupPayloadType(l, curT, node.Variant, node.Index)
		}
	}
	return curT
}

// flattenProjection walks base→leaf and returns the chain in
// root-to-leaf order.
func flattenProjection(p *ir.Projection) []*ir.Projection {
	var stack []*ir.Projection
	for cur := p; cur != nil; cur = cur.Base {
		stack = append(stack, cur)
	}
	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	return stack
}

func variantLookupPayloadType(l *lowerer, scrutT ir.Type, variant string, idx int) Type {
	// If the "scrutinee" at this level is already the payload's scalar
	// (because a prior ProjVariant already descended past the payload
	// tuple boundary), then a ProjVariantN at idx==0 is a redundant
	// projection — just return the same type.
	if isScalarPayload(scrutT) && idx == 0 {
		return scrutT
	}
	typeName := typeNameOf(scrutT)
	if typeName == "" {
		return ir.ErrTypeVal
	}
	if e, ok := l.enums[typeName]; ok {
		for _, v := range e.Variants {
			if v.Name == variant {
				if idx < 0 {
					if len(v.Payload) == 1 {
						return v.Payload[0]
					}
					return ir.ErrTypeVal
				}
				if idx < len(v.Payload) {
					return v.Payload[idx]
				}
			}
		}
	}
	// Builtin Option/Result heuristics
	switch typeName {
	case "Option", "Maybe":
		if nt, ok := scrutT.(*ir.NamedType); ok && len(nt.Args) >= 1 {
			return nt.Args[0]
		}
	case "Result":
		if nt, ok := scrutT.(*ir.NamedType); ok && len(nt.Args) >= 2 {
			if variant == "Ok" {
				return nt.Args[0]
			}
			if variant == "Err" {
				return nt.Args[1]
			}
		}
	}
	// Optional types in surface form T?
	if ot, ok := scrutT.(*ir.OptionalType); ok {
		return ot.Inner
	}
	return ir.ErrTypeVal
}

// ==== misc pure helpers ====

func typeNameOf(t ir.Type) string {
	switch x := t.(type) {
	case *ir.NamedType:
		return x.Name
	case *ir.OptionalType:
		return "Option"
	}
	return ""
}

func isListType(t ir.Type) bool {
	if nt, ok := t.(*ir.NamedType); ok {
		return nt.Name == "List" && nt.Builtin
	}
	return false
}

// isChannelType reports whether t is a `Channel<T>`. The stdlib
// declares Channel as a named type rather than a primitive, so we
// accept both Builtin=false and Builtin=true to match HIR's view.
func isChannelType(t ir.Type) bool {
	if nt, ok := t.(*ir.NamedType); ok {
		return nt.Name == "Channel"
	}
	return false
}

// channelElementType returns the T of a `Channel<T>`, or ErrType
// when t isn't a channel.
func channelElementType(t ir.Type) Type {
	if nt, ok := t.(*ir.NamedType); ok && len(nt.Args) >= 1 {
		return nt.Args[0]
	}
	return ir.ErrTypeVal
}

// isScalarPayload reports whether a type is a primitive/scalar shape
// that already represents a variant's single payload element. Used to
// detect redundant ProjVariantN steps after a ProjVariant has already
// descended into a scalar payload.
func isScalarPayload(t ir.Type) bool {
	switch t.(type) {
	case *ir.PrimType:
		return true
	case *ir.OptionalType:
		return true
	case *ir.FnType:
		return true
	}
	return false
}

func listElementType(t ir.Type) Type {
	if nt, ok := t.(*ir.NamedType); ok && len(nt.Args) >= 1 {
		return nt.Args[0]
	}
	return ir.ErrTypeVal
}

func isUnit(t ir.Type) bool {
	if p, ok := t.(*ir.PrimType); ok {
		return p.Kind == ir.PrimUnit
	}
	return false
}

func tupleElementTypes(t ir.Type) []Type {
	if tt, ok := t.(*ir.TupleType); ok {
		return tt.Elems
	}
	return nil
}

func elementTypeAt(ts []Type, i int) Type {
	if i < 0 || i >= len(ts) {
		return ir.ErrTypeVal
	}
	return ts[i]
}

func hasInterpolation(s *ir.StringLit) bool {
	for _, p := range s.Parts {
		if !p.IsLit {
			return true
		}
	}
	return false
}

func mapUnaryOp(op ir.UnOp) UnaryOp {
	switch op {
	case ir.UnNeg:
		return UnNeg
	case ir.UnPlus:
		return UnPlus
	case ir.UnNot:
		return UnNot
	case ir.UnBitNot:
		return UnBitNot
	}
	return UnInvalid
}

func mapBinaryOp(op ir.BinOp) BinaryOp {
	switch op {
	case ir.BinAdd:
		return BinAdd
	case ir.BinSub:
		return BinSub
	case ir.BinMul:
		return BinMul
	case ir.BinDiv:
		return BinDiv
	case ir.BinMod:
		return BinMod
	case ir.BinEq:
		return BinEq
	case ir.BinNeq:
		return BinNeq
	case ir.BinLt:
		return BinLt
	case ir.BinLeq:
		return BinLeq
	case ir.BinGt:
		return BinGt
	case ir.BinGeq:
		return BinGeq
	case ir.BinAnd:
		return BinAnd
	case ir.BinOr:
		return BinOr
	case ir.BinBitAnd:
		return BinBitAnd
	case ir.BinBitOr:
		return BinBitOr
	case ir.BinBitXor:
		return BinBitXor
	case ir.BinShl:
		return BinShl
	case ir.BinShr:
		return BinShr
	}
	return BinInvalid
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

func exprSpan(e ir.Expr) Span {
	if e == nil {
		return Span{}
	}
	return e.At()
}

// ==== Option / Result tag helpers ====

// someTagOf returns the discriminant value of the "present" arm of an
// optional or Maybe/Option enum. Defaults to 1 which matches the
// convention in internal/llvmgen (None=0, Some=1).
func someTagOf(t ir.Type) int64 {
	return 1
}

// okTagOf returns the discriminant value of the "happy" arm of a
// `?`-able enum. For Option it's 1 (Some); for Result it's also 1
// (Ok) in the current LLVM layout.
func okTagOf(t ir.Type) int64 {
	return 1
}

// okVariantName returns the name of the happy arm for `?`.
func okVariantName(t ir.Type) string {
	if _, ok := t.(*ir.OptionalType); ok {
		return "Some"
	}
	name := typeNameOf(t)
	switch name {
	case "Option", "Maybe":
		return "Some"
	case "Result":
		return "Ok"
	}
	return "Ok"
}

func optionInnerType(t ir.Type) Type {
	if ot, ok := t.(*ir.OptionalType); ok {
		return ot.Inner
	}
	if nt, ok := t.(*ir.NamedType); ok && len(nt.Args) >= 1 {
		return nt.Args[0]
	}
	return ir.ErrTypeVal
}

// mangleMethodSymbol returns the method symbol name shared with the
// llvmgen convention (Type__method).
func mangleMethodSymbol(owner, method string) string {
	return owner + "__" + method
}

// methodCallSig returns a copy of sig without its leading receiver
// parameter, for use when resolving call-site arguments (which never
// include `self`).
func methodCallSig(sig *fnSignature) *fnSignature {
	if sig == nil || len(sig.params) == 0 {
		return sig
	}
	return &fnSignature{
		ir:      sig.ir,
		owner:   sig.owner,
		symbol:  sig.symbol,
		params:  sig.params[1:],
		retType: sig.retType,
	}
}
