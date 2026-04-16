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
		src:     mod,
		out:     &Module{Package: mod.Package, SpanV: mod.SpanV, Layouts: NewLayoutTable()},
		fnSig:   map[string]*fnSignature{},
		methods: map[string]*fnSignature{},
		enums:   map[string]*ir.EnumDecl{},
		structs: map[string]*ir.StructDecl{},
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

	fnSig   map[string]*fnSignature
	methods map[string]*fnSignature
	enums   map[string]*ir.EnumDecl
	structs map[string]*ir.StructDecl
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
	if asMethod {
		symbol = mangleMethodSymbol(owner, fn.Name)
	}
	out := l.newFunction(symbol, fn.Params, retT, fn.SpanV, asMethod)
	out.Exported = fn.Exported
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

	// locals maps HIR-visible names to MIR local IDs. Pushed on let
	// binding, popped at the end of scopes.
	locals []map[string]LocalID

	// paramLocals is a cached view of fn.Params for quick access.
	paramLocals []LocalID

	// self tracks the receiver for method bodies.
	self *ownerContext

	// loopStack is a stack of break/continue destinations.
	loopStack []*loopFrame
}

type ownerContext struct {
	typeName string
	localID  LocalID
}

type loopFrame struct {
	breakBlock    BlockID
	continueBlock BlockID
}

func newBodyState(l *lowerer, fn *Function) *bodyState {
	bs := &bodyState{l: l, fn: fn, cur: fn.Entry}
	bs.pushScope()
	// seed locals from parameters.
	for i, p := range fn.Params {
		loc := fn.Locals[p]
		if loc != nil && loc.Name != "" {
			bs.locals[0][loc.Name] = p
		}
		bs.paramLocals = append(bs.paramLocals, p)
		_ = i
	}
	return bs
}

func (bs *bodyState) pushScope() {
	bs.locals = append(bs.locals, map[string]LocalID{})
}

func (bs *bodyState) popScope() {
	if len(bs.locals) == 0 {
		return
	}
	bs.locals = bs.locals[:len(bs.locals)-1]
}

func (bs *bodyState) bind(name string, id LocalID) {
	if name == "" {
		return
	}
	bs.locals[len(bs.locals)-1][name] = id
}

func (bs *bodyState) lookup(name string) (LocalID, bool) {
	for i := len(bs.locals) - 1; i >= 0; i-- {
		if id, ok := bs.locals[i][name]; ok {
			return id, true
		}
	}
	return 0, false
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
		bs.l.noteIssue("defer not lowered to MIR (stage 2 work)")
	case *ir.ChanSendStmt:
		bs.l.noteIssue("channel send not lowered to MIR (stage 2 work)")
	case *ir.ErrorStmt:
		bs.l.noteIssue("error stmt reached MIR: %s", x.Note)
	default:
		bs.l.noteIssue("unsupported stmt %T", s)
	}
}

func (bs *bodyState) lowerBlockStmt(b *ir.Block) {
	bs.pushScope()
	for _, s := range b.Stmts {
		bs.lowerStmt(s)
	}
	if b.Result != nil {
		// Block used in statement position — we discard the value but
		// still need its side effects.
		_ = bs.lowerExprAsOperand(b.Result)
	}
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
		id := bs.fn.NewLocal(let.Name, t, let.Mut, let.SpanV)
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
	scratch := bs.fn.NewLocal("_scratch", valueType, false, sp)
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
		id := bs.fn.NewLocal(p.Name, scrutineeT, p.Mut, p.SpanV)
		bs.emit(&StorageLiveInstr{Local: id, SpanV: p.SpanV})
		bs.emit(&AssignInstr{
			Dest:  Place{Local: id},
			Src:   &UseRV{Op: &CopyOp{Place: scrutinee, T: scrutineeT}},
			SpanV: p.SpanV,
		})
		bs.bind(p.Name, id)
	case *ir.BindingPat:
		// Bind the whole scrutinee to the outer name, then recurse.
		id := bs.fn.NewLocal(p.Name, scrutineeT, false, p.SpanV)
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
				id := bs.fn.NewLocal(f.Name, ft, false, p.SpanV)
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
	// simple case: single target, AssignEq
	if len(a.Targets) == 1 && a.Op == ir.AssignEq {
		dst, ok := bs.lowerExprToPlace(a.Targets[0])
		if !ok {
			bs.l.noteIssue("assign: unsupported target %T", a.Targets[0])
			return
		}
		bs.lowerExprIntoPlace(a.Value, dst, a.Value.Type())
		return
	}
	// compound / multi-target assignments not yet handled in MIR.
	bs.l.noteIssue("compound/multi-target assign not lowered to MIR (op=%d, targets=%d)",
		a.Op, len(a.Targets))
}

func (bs *bodyState) lowerReturn(r *ir.ReturnStmt) {
	if r.Value != nil {
		bs.lowerExprInto(r.Value, bs.fn.ReturnLocal, bs.fn.ReturnType)
	}
	bs.terminate(&ReturnTerm{SpanV: r.SpanV})
}

func (bs *bodyState) lowerBreak(b *ir.BreakStmt) {
	if len(bs.loopStack) == 0 {
		bs.l.noteIssue("break outside loop")
		return
	}
	top := bs.loopStack[len(bs.loopStack)-1]
	bs.terminate(&GotoTerm{Target: top.breakBlock, SpanV: b.SpanV})
}

func (bs *bodyState) lowerContinue(c *ir.ContinueStmt) {
	if len(bs.loopStack) == 0 {
		bs.l.noteIssue("continue outside loop")
		return
	}
	top := bs.loopStack[len(bs.loopStack)-1]
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
	for _, s := range st.Then.Stmts {
		bs.lowerStmt(s)
	}
	if st.Then.Result != nil {
		_ = bs.lowerExprAsOperand(st.Then.Result)
	}
	bs.popScope()
	bs.terminate(&GotoTerm{Target: merge, SpanV: st.SpanV})

	bs.cur = elseBB
	if st.Else != nil {
		bs.pushScope()
		for _, s := range st.Else.Stmts {
			bs.lowerStmt(s)
		}
		if st.Else.Result != nil {
			_ = bs.lowerExprAsOperand(st.Else.Result)
		}
		bs.popScope()
	}
	bs.terminate(&GotoTerm{Target: merge, SpanV: st.SpanV})

	bs.cur = merge
}

func (bs *bodyState) lowerForStmt(f *ir.ForStmt) {
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
}

func (bs *bodyState) lowerForInfinite(f *ir.ForStmt) {
	header := bs.newBlock(f.SpanV)
	body := bs.newBlock(f.SpanV)
	exit := bs.newBlock(f.SpanV)
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.cur = header
	bs.terminate(&GotoTerm{Target: body, SpanV: f.SpanV})
	bs.loopStack = append(bs.loopStack, &loopFrame{breakBlock: exit, continueBlock: header})
	bs.cur = body
	bs.pushScope()
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
	bs.popScope()
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
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
	bs.loopStack = append(bs.loopStack, &loopFrame{breakBlock: exit, continueBlock: header})
	bs.cur = body
	bs.pushScope()
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
	bs.popScope()
	bs.terminate(&GotoTerm{Target: header, SpanV: f.SpanV})
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
	bs.cur = exit
}

// ==== range / in loop lowering ====

func (bs *bodyState) lowerForRange(f *ir.ForStmt) {
	idx := bs.fn.NewLocal(f.Var, TInt, true, f.SpanV)
	bs.emit(&StorageLiveInstr{Local: idx, SpanV: f.SpanV})
	bs.lowerExprInto(f.Start, idx, TInt)
	end := bs.fn.NewLocal("_end", TInt, false, f.SpanV)
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
	bs.loopStack = append(bs.loopStack, &loopFrame{breakBlock: exit, continueBlock: step})
	bs.cur = body
	bs.pushScope()
	bs.bind(f.Var, idx)
	for _, s := range f.Body.Stmts {
		bs.lowerStmt(s)
	}
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
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
	bs.cur = exit
}

func (bs *bodyState) lowerForIn(f *ir.ForStmt) {
	iterT := f.Iter.Type()
	if !isListType(iterT) {
		bs.l.noteIssue("for-in over non-List iterable is not lowered to MIR yet")
		return
	}
	elemT := listElementType(iterT)
	if elemT == nil {
		elemT = ir.ErrTypeVal
	}
	// capture iterable into a temp so we can re-read length and index
	iter := bs.fn.NewLocal("_iter", iterT, false, f.SpanV)
	bs.emit(&StorageLiveInstr{Local: iter, SpanV: f.SpanV})
	bs.lowerExprInto(f.Iter, iter, iterT)
	lenLocal := bs.fn.NewLocal("_len", TInt, false, f.SpanV)
	bs.emit(&AssignInstr{
		Dest:  Place{Local: lenLocal},
		Src:   &LenRV{Place: Place{Local: iter}, T: TInt},
		SpanV: f.SpanV,
	})
	idx := bs.fn.NewLocal("_idx", TInt, true, f.SpanV)
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
	bs.loopStack = append(bs.loopStack, &loopFrame{breakBlock: exit, continueBlock: step})
	bs.cur = body
	bs.pushScope()
	// load current element: elem = iter[idx]
	elemLocal := bs.fn.NewLocal("_elem", elemT, false, f.SpanV)
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
	bs.loopStack = bs.loopStack[:len(bs.loopStack)-1]
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
	scrutT := scrutinee.Type()
	if scrutT == nil {
		scrutT = ir.ErrTypeVal
	}
	scrutLocal := bs.fn.NewLocal("_scrut", scrutT, false, sp)
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
		}
		bs.popScope()
		bs.terminate(&GotoTerm{Target: merge, SpanV: arm.SpanV})
	}

	bs.cur = merge
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
		id := bs.fn.NewLocal(n.Name, projType, false, mc.sp)
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
		bs.l.noteIssue("closure literal not lowered to MIR (stage 2 work)")
		return &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}}
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
		return &CopyOp{Place: Place{Local: bs.externalGlobalLocal(id.Name, id.T)}, T: id.T}
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

// externalGlobalLocal allocates a local for a top-level global access
// on first use. Subsequent reads reuse it.
func (bs *bodyState) externalGlobalLocal(name string, t Type) LocalID {
	if id, ok := bs.lookup(name); ok {
		return id
	}
	id := bs.fn.NewLocal(name, t, false, Span{})
	bs.bind(name, id)
	// Issue a note — backend consumer will need to materialise the
	// global load.
	bs.l.noteIssue("global read %q materialised as uninitialised local (stage 2 work)", name)
	return id
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
	for _, s := range ie.Then.Stmts {
		bs.lowerStmt(s)
	}
	if ie.Then.Result != nil {
		bs.lowerExprIntoPlace(ie.Then.Result, dest, destT)
	}
	bs.popScope()
	bs.terminate(&GotoTerm{Target: merge, SpanV: ie.SpanV})
	bs.cur = elseBB
	if ie.Else != nil {
		bs.pushScope()
		for _, s := range ie.Else.Stmts {
			bs.lowerStmt(s)
		}
		if ie.Else.Result != nil {
			bs.lowerExprIntoPlace(ie.Else.Result, dest, destT)
		}
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
	for _, s := range be.Block.Stmts {
		bs.lowerStmt(s)
	}
	if be.Block.Result != nil {
		bs.lowerExprIntoPlace(be.Block.Result, dest, destT)
	}
	bs.popScope()
}

// lowerIfLetExprInto handles `if let pat = scrut { … } else { … }`.
// It lowers into a discriminant test + payload binding for Option /
// enum variants, and falls back to an unsupported note otherwise.
func (bs *bodyState) lowerIfLetExprInto(ife *ir.IfLetExpr, dest Place, destT Type) {
	scrut := ife.Scrutinee
	scrutT := scrut.Type()
	scrutLocal := bs.fn.NewLocal("_iflet", scrutT, false, ife.SpanV)
	bs.emit(&StorageLiveInstr{Local: scrutLocal, SpanV: ife.SpanV})
	bs.lowerExprInto(scrut, scrutLocal, scrutT)

	vp, ok := ife.Pattern.(*ir.VariantPat)
	if !ok {
		bs.l.noteIssue("if-let with non-variant pattern not lowered to MIR: %T", ife.Pattern)
		// Always take the else branch to stay conservative.
		if ife.Else != nil {
			bs.pushScope()
			for _, s := range ife.Else.Stmts {
				bs.lowerStmt(s)
			}
			if ife.Else.Result != nil {
				bs.lowerExprIntoPlace(ife.Else.Result, dest, destT)
			}
			bs.popScope()
		}
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
	bs.popScope()
	bs.terminate(&GotoTerm{Target: merge, SpanV: ife.SpanV})
	bs.cur = elseBB
	if ife.Else != nil {
		bs.pushScope()
		for _, s := range ife.Else.Stmts {
			bs.lowerStmt(s)
		}
		if ife.Else.Result != nil {
			bs.lowerExprIntoPlace(ife.Else.Result, dest, destT)
		}
		bs.popScope()
	}
	bs.terminate(&GotoTerm{Target: merge, SpanV: ife.SpanV})
	bs.cur = merge
}

// lowerMatchExprIntoPlace routes through the generic matcher.
func (bs *bodyState) lowerMatchExprIntoPlace(m *ir.MatchExpr, dest Place, destT Type) {
	// For simplicity: allocate a local, drive match into it, then copy
	// into the requested place.
	t := destT
	if t == nil {
		t = m.T
	}
	local := bs.fn.NewLocal("_match", t, false, m.SpanV)
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
	leftT := c.Left.Type()
	leftLocal := bs.fn.NewLocal("_coalesce", leftT, false, c.SpanV)
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
}

// lowerQuestionInto handles `e?`. The receiver must be Option<T> or
// Result<T, E>. For None/Err we early-return the same none/err value
// via the function's return local.
func (bs *bodyState) lowerQuestionInto(q *ir.QuestionExpr, dest Place, destT Type) {
	xT := q.X.Type()
	tmp := bs.fn.NewLocal("_q", xT, false, q.SpanV)
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
	// Wrap the failure into the fn's return type; for now we just copy
	// tmp into the return slot (works when return type is the same as
	// the operand type, as in typical Result/Option propagation).
	bs.emit(&AssignInstr{
		Dest:  Place{Local: bs.fn.ReturnLocal},
		Src:   &UseRV{Op: &CopyOp{Place: Place{Local: tmp}, T: xT}},
		SpanV: q.SpanV,
	})
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
}

// lowerOptionalFieldInto handles `x?.field`.
func (bs *bodyState) lowerOptionalFieldInto(fe *ir.FieldExpr, dest Place, destT Type) {
	recvT := fe.X.Type()
	recvLocal := bs.fn.NewLocal("_opt", recvT, false, fe.SpanV)
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

// resolveCall figures out the callee and reorders keyword args.
func (bs *bodyState) resolveCall(c *ir.CallExpr) ([]Operand, Callee) {
	switch cal := c.Callee.(type) {
	case *ir.Ident:
		if cal.Kind == ir.IdentVariant {
			// variant construction via call-like syntax; return nil to
			// let the caller re-route through lowerExprToRValue.
			return nil, nil
		}
		sig := bs.l.signatureForFn(cal.Name)
		args := bs.orderArgs(c.Args, sig)
		ft := cal.T
		return args, &FnRef{Symbol: cal.Name, Type: ft}
	case *ir.FieldExpr:
		// Callee like `foo.bar`: a package-qualified or method-style call.
		// For runtime FFI we still route through FnRef with the field name.
		recv := bs.lowerExprAsOperand(cal.X)
		args := append([]Operand{recv}, bs.orderArgs(c.Args, nil)...)
		return args, &FnRef{Symbol: cal.Name, Type: cal.T}
	}
	// Indirect call via operand.
	args := bs.orderArgs(c.Args, nil)
	callee := bs.lowerExprAsOperand(c.Callee)
	return args, &IndirectCall{Callee: callee}
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

// ==== misc helpers ====

func (bs *bodyState) freshTemp(t Type, sp Span) LocalID {
	return bs.fn.NewLocal("", t, false, sp)
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
