// stmt.go — statement-position emission: emitBlock, emitStmt dispatcher,
// let/assign/for/return/break/continue/expr-stmt, if-stmt / if-let-stmt,
// list·map·set method-call statements, user-call statements, println, and
// testing-only statement helpers (testing.assert / assertTrue / assertFalse /
// assertEq / assertNe / fail / context / expectOk / expectError).
//
// NOTE(osty-migration): statement emission consumes ast.Stmt shapes and
// drives the generator through side effects — this is the bulk of the
// AST-dependent surface. It will migrate to toolchain/llvmgen.osty only
// after the IR-direct path (doc.go) takes over, at which point ir.osty's
// IrNode tree becomes the stmt input.
package llvmgen

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

func (g *generator) emitReturningBlock(stmts []ast.Stmt, retType string, retSourceType ast.Type, retListElemTyp string, retListElemString bool, retMapKeyTyp string, retMapValueTyp string, retMapKeyString bool, retSetElemTyp string, retSetElemString bool) error {
	if len(stmts) == 0 {
		return unsupported("function-signature", "function body has no return value")
	}
	for i, stmt := range stmts {
		if i != len(stmts)-1 {
			if err := g.emitStmt(stmt); err != nil {
				return err
			}
			if !g.currentReachable {
				return nil
			}
			continue
		}
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if s.Value == nil {
				return unsupported("function-signature", "bare return in value-returning function")
			}
			v, err := g.emitExprWithHintAndSourceType(s.Value, retSourceType, retListElemTyp, retListElemString, retMapKeyTyp, retMapValueTyp, retMapKeyString, retSetElemTyp, retSetElemString)
			if err != nil {
				return err
			}
			// Phase 6d: auto-box a concrete return value into the
			// declared interface type (same policy as emitReturn).
			if retType == "%osty.iface" && v.typ != "%osty.iface" && g.returnSourceType != nil {
				boxed, boxErr := g.boxInterfaceValue(g.returnSourceType, v)
				if boxErr != nil {
					return boxErr
				}
				v = boxed
			}
			if v.typ != retType {
				return unsupportedf("type-system", "return type %s, want %s", v.typ, retType)
			}
			if err := g.emitAllPendingDefers(); err != nil {
				return err
			}
			if !g.currentReachable {
				return nil
			}
			emitter := g.toOstyEmitter()
			g.releaseGCRoots(emitter)
			llvmReturn(emitter, toOstyValue(v))
			g.takeOstyEmitter(emitter)
			g.leaveBlock()
			return nil
		case *ast.ExprStmt:
			v, err := g.emitExprWithHintAndSourceType(s.X, retSourceType, retListElemTyp, retListElemString, retMapKeyTyp, retMapValueTyp, retMapKeyString, retSetElemTyp, retSetElemString)
			if err != nil {
				return err
			}
			// Phase 6d: trailing-expression implicit return auto-boxing.
			if retType == "%osty.iface" && v.typ != "%osty.iface" && g.returnSourceType != nil {
				boxed, boxErr := g.boxInterfaceValue(g.returnSourceType, v)
				if boxErr != nil {
					return boxErr
				}
				v = boxed
			}
			if v.typ != retType {
				return unsupportedf("type-system", "trailing expression type %s, want %s", v.typ, retType)
			}
			if err := g.emitAllPendingDefers(); err != nil {
				return err
			}
			if !g.currentReachable {
				return nil
			}
			emitter := g.toOstyEmitter()
			g.releaseGCRoots(emitter)
			llvmReturn(emitter, toOstyValue(v))
			g.takeOstyEmitter(emitter)
			g.leaveBlock()
			return nil
		default:
			return unsupportedf("statement", "final function statement %T", stmt)
		}
	}
	return nil
}

func (g *generator) emitBlock(stmts []ast.Stmt) error {
	for i := 0; i < len(stmts); i++ {
		stmt := stmts[i]
		// Peephole: `containsKey + getOr + insert` → `map_incr_i64`.
		// The bench-style counter update pattern compiles to 3 locked
		// map ops (one hash lookup each) by default; recognising it
		// here cuts that to a single locked `map[key] = get(key) ?? 0
		// + delta` runtime call.
		if i+1 < len(stmts) {
			if skip, err := g.tryEmitMapIncrPattern(stmts[i], stmts[i+1]); err != nil {
				return err
			} else if skip {
				i++ // consumed two statements
				continue
			}
		}
		if err := g.emitStmt(stmt); err != nil {
			return err
		}
		if !g.currentReachable {
			break
		}
	}
	return nil
}

// tryEmitMapIncrPattern looks for this 2-statement shape:
//
//	let NEXT = if M.containsKey(K) { M.getOr(K, 0) <OP> V } else { V }
//	M.insert(K, NEXT)
//
// And lowers it to a single `osty_rt_map_incr_i64_<suffix>(M, K, V)`
// runtime call that performs the same update under one lock acquire
// pair instead of three. Returns (true, nil) when the pattern matches
// and code was emitted; callers skip both consumed statements.
//
// The match is strict — every shape requirement below must hold;
// any mismatch returns (false, nil) and callers proceed with normal
// emission so semantics stay identical. Currently specialised for
// <OP> = `+` and the implicit default of 0; extension to other
// default-and-op combinations stays in scope for a follow-up if
// measured workloads warrant it.
func (g *generator) tryEmitMapIncrPattern(first, second ast.Stmt) (bool, error) {
	let1, ok := first.(*ast.LetStmt)
	if !ok || let1.Mut || let1.Value == nil {
		return false, nil
	}
	letName, err := identPatternName(let1.Pattern)
	if err != nil {
		return false, nil
	}
	ifExpr, ok := let1.Value.(*ast.IfExpr)
	if !ok || ifExpr.IsIfLet || ifExpr.Then == nil {
		return false, nil
	}
	elseBlock, ok := ifExpr.Else.(*ast.Block)
	if !ok {
		return false, nil
	}
	// Condition: `MAP.containsKey(KEY)`.
	containsCall, ok := ifExpr.Cond.(*ast.CallExpr)
	if !ok || len(containsCall.Args) != 1 {
		return false, nil
	}
	containsField, ok := containsCall.Fn.(*ast.FieldExpr)
	if !ok || containsField.IsOptional || containsField.Name != "containsKey" {
		return false, nil
	}
	mapIdent, ok := containsField.X.(*ast.Ident)
	if !ok {
		return false, nil
	}
	keyArg0 := containsCall.Args[0]
	if keyArg0 == nil || keyArg0.Name != "" || keyArg0.Value == nil {
		return false, nil
	}
	keyIdent, ok := keyArg0.Value.(*ast.Ident)
	if !ok {
		return false, nil
	}
	// Then branch: tail expression is `MAP.getOr(KEY, 0) + DELTA`.
	thenExpr := blockTailExpr(ifExpr.Then)
	if thenExpr == nil {
		return false, nil
	}
	addBin, ok := thenExpr.(*ast.BinaryExpr)
	if !ok || addBin.Op != token.PLUS {
		return false, nil
	}
	getOrCall, ok := addBin.Left.(*ast.CallExpr)
	if !ok || len(getOrCall.Args) != 2 {
		return false, nil
	}
	getOrField, ok := getOrCall.Fn.(*ast.FieldExpr)
	if !ok || getOrField.IsOptional || getOrField.Name != "getOr" {
		return false, nil
	}
	if !identsEqual(getOrField.X, mapIdent) {
		return false, nil
	}
	if a := getOrCall.Args[0]; a == nil || a.Name != "" || !identsEqual(a.Value, keyIdent) {
		return false, nil
	}
	if a := getOrCall.Args[1]; a == nil || a.Name != "" || !isIntLiteral(a.Value, 0) {
		return false, nil
	}
	deltaExpr := addBin.Right
	// Else branch: tail expression is the same DELTA identifier (or
	// expression). Require byte-for-byte identity via the source
	// span so we don't re-evaluate side effects inside DELTA twice.
	elseExpr := blockTailExpr(elseBlock)
	if elseExpr == nil || !astExprTextEq(g, deltaExpr, elseExpr) {
		return false, nil
	}
	// Second stmt: `MAP.insert(KEY, NEXT)`.
	exprStmt, ok := second.(*ast.ExprStmt)
	if !ok {
		return false, nil
	}
	insertCall, ok := exprStmt.X.(*ast.CallExpr)
	if !ok || len(insertCall.Args) != 2 {
		return false, nil
	}
	insertField, ok := insertCall.Fn.(*ast.FieldExpr)
	if !ok || insertField.IsOptional || insertField.Name != "insert" {
		return false, nil
	}
	if !identsEqual(insertField.X, mapIdent) {
		return false, nil
	}
	if a := insertCall.Args[0]; a == nil || a.Name != "" || !identsEqual(a.Value, keyIdent) {
		return false, nil
	}
	if a := insertCall.Args[1]; a == nil || a.Name != "" {
		return false, nil
	}
	nextIdent, ok := insertCall.Args[1].Value.(*ast.Ident)
	if !ok || nextIdent.Name != letName {
		return false, nil
	}
	// Shape verified. Look up the static type info on the map so we
	// pick the right suffix. Only Map<K, Int> is eligible today —
	// the runtime intrinsic is `osty_rt_map_incr_i64_<suffix>`
	// where the i64 reflects the value type.
	mapInfo, ok := g.staticExprInfo(mapIdent)
	if !ok || mapInfo.typ != "ptr" || mapInfo.mapKeyTyp == "" || mapInfo.mapValueTyp != "i64" {
		return false, nil
	}
	suffix := mapKeySuffix(mapInfo.mapKeyTyp, mapInfo.mapKeyString)
	if suffix == "" {
		return false, nil
	}

	// Evaluate the three inputs in source order. MAP is a known
	// ident (side-effect-free), KEY too, DELTA may be any expression.
	mapVal, err := g.emitExpr(mapIdent)
	if err != nil {
		return false, err
	}
	keyVal, err := g.emitExpr(keyIdent)
	if err != nil {
		return false, err
	}
	deltaVal, err := g.emitExpr(deltaExpr)
	if err != nil {
		return false, err
	}
	if deltaVal.typ != "i64" {
		return false, nil
	}
	loadedKey, err := g.loadIfPointer(keyVal)
	if err != nil {
		return false, err
	}
	symbol := mirRtMapSymbol("incr_i64_" + suffix)
	keyTyp := mapInfo.mapKeyTyp
	if mapInfo.mapKeyString {
		keyTyp = "ptr"
	}
	g.declareRuntimeSymbol(symbol, "i64", []paramInfo{
		{typ: "ptr"},
		{typ: keyTyp},
		{typ: "i64"},
	})
	emitter := g.toOstyEmitter()
	_ = llvmCall(emitter, "i64", symbol, []*LlvmValue{
		toOstyValue(mapVal),
		toOstyValue(loadedKey),
		toOstyValue(deltaVal),
	})
	g.takeOstyEmitter(emitter)
	// Bind the let-name so later references (there shouldn't be
	// any in the recognised shape, but be defensive) resolve to a
	// fresh i64 load of the post-incr value. We could skip this
	// since the pattern only uses NEXT as the insert arg which we
	// just emitted, but wiring it keeps the binding scope intact.
	g.bindLocal(letName, value{typ: "i64", ref: "0"})
	return true, nil
}

// blockTailExpr returns the expression value of a block — the tail
// expression statement, if any. Osty blocks may have either a
// trailing ExprStmt (no semicolon) or not; we only accept the
// former shape for the map_incr rewrite since we need a concrete
// expression value.
func blockTailExpr(block *ast.Block) ast.Expr {
	if block == nil || len(block.Stmts) == 0 {
		return nil
	}
	tail, ok := block.Stmts[len(block.Stmts)-1].(*ast.ExprStmt)
	if !ok {
		return nil
	}
	return tail.X
}

// identsEqual reports whether two expressions are the same
// identifier (same Name). Used to verify the same `totals` / `key`
// reference shows up in every slot of the pattern.
func identsEqual(a, b ast.Expr) bool {
	ax, aok := a.(*ast.Ident)
	bx, bok := b.(*ast.Ident)
	return aok && bok && ax.Name == bx.Name
}

// isIntLiteral reports whether expr is the integer literal `n`.
// Used to verify the `0` default in `getOr(key, 0)`.
func isIntLiteral(expr ast.Expr, n int64) bool {
	lit, ok := expr.(*ast.IntLit)
	if !ok {
		return false
	}
	// Strip underscores; reject hex/oct/bin for the 0-literal case
	// we care about here.
	text := strings.ReplaceAll(lit.Text, "_", "")
	if text == "" {
		return false
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return false
	}
	return v == n
}

// astExprTextEq reports whether two expression nodes cover source
// text that compares equal modulo surrounding whitespace. Used as a
// conservative proxy for "structurally identical" for the
// `else DELTA` arm — avoids re-evaluating effects inside DELTA.
func astExprTextEq(g *generator, a, b ast.Expr) bool {
	if a == nil || b == nil {
		return false
	}
	ta := strings.TrimSpace(g.sourceSpanText(a))
	tb := strings.TrimSpace(g.sourceSpanText(b))
	return ta != "" && ta == tb
}

// mapKeySuffix maps an LLVM key type string + string-flag to the
// suffix used by the `osty_rt_map_*_<suffix>` runtime helpers. Delegates
// to the Osty-sourced `mirMapKeySuffix` (`toolchain/mir_generator.osty`).
func mapKeySuffix(llvmTyp string, isString bool) string {
	return mirMapKeySuffix(llvmTyp, isString)
}

func (g *generator) emitStmt(stmt ast.Stmt) error {
	if !g.currentReachable {
		return nil
	}
	switch s := stmt.(type) {
	case *ast.Block:
		return g.emitScopedStmtBlock(s.Stmts)
	case *ast.LetStmt:
		return g.emitLet(s)
	case *ast.AssignStmt:
		return g.emitAssign(s)
	case *ast.ForStmt:
		return g.emitFor(s)
	case *ast.ReturnStmt:
		return g.emitReturn(s)
	case *ast.BreakStmt:
		return g.emitBreak()
	case *ast.ContinueStmt:
		return g.emitContinue()
	case *ast.DeferStmt:
		return g.registerDefer(s)
	case *ast.ExprStmt:
		if ifExpr, ok := s.X.(*ast.IfExpr); ok {
			return g.emitIfStmt(ifExpr)
		}
		if matchExpr, ok := s.X.(*ast.MatchExpr); ok {
			return g.emitMatchStmt(matchExpr)
		}
		return g.emitExprStmt(s.X)
	default:
		return unsupportedf("statement", "statement %T", stmt)
	}
}

func (g *generator) emitLet(stmt *ast.LetStmt) error {
	if stmt.Value == nil {
		return unsupported("statement", "let has no value")
	}
	if stmt.Type == nil {
		if _, ok := stmt.Pattern.(*ast.WildcardPat); ok {
			if _, ok := stmt.Value.(*ast.CallExpr); ok {
				return g.emitExprStmt(stmt.Value)
			}
		}
	}
	hintedListElemTyp := ""
	hintedListElemString := false
	hintedMapKeyTyp := ""
	hintedMapValueTyp := ""
	hintedMapKeyString := false
	hintedSetElemTyp := ""
	hintedSetElemString := false
	if stmt.Type != nil {
		collectTupleTypeFromAST(g.tupleTypes, stmt.Type, g.typeEnv())
		if listElemTyp, listElemString, ok, err := llvmListElementInfo(stmt.Type, g.typeEnv()); err != nil {
			return err
		} else if ok {
			hintedListElemTyp = listElemTyp
			hintedListElemString = listElemString
		}
		if mapKeyTyp, mapValueTyp, mapKeyString, ok, err := llvmMapTypes(stmt.Type, g.typeEnv()); err != nil {
			return err
		} else if ok {
			hintedMapKeyTyp = mapKeyTyp
			hintedMapValueTyp = mapValueTyp
			hintedMapKeyString = mapKeyString
		}
		if setElemTyp, setElemString, ok, err := llvmSetElementType(stmt.Type, g.typeEnv()); err != nil {
			return err
		} else if ok {
			hintedSetElemTyp = setElemTyp
			hintedSetElemString = setElemString
		}
	}
	v, err := g.emitExprWithHintAndSourceType(stmt.Value, stmt.Type, hintedListElemTyp, hintedListElemString, hintedMapKeyTyp, hintedMapValueTyp, hintedMapKeyString, hintedSetElemTyp, hintedSetElemString)
	if err != nil {
		return err
	}
	if stmt.Type != nil {
		typ, err := llvmType(stmt.Type, g.typeEnv())
		if err != nil {
			return err
		}
		// Phase 6b: when a let binds a concrete struct/enum value into
		// an interface-typed slot, box it into a `{data, vtable}` fat
		// pointer before the type-check.
		if typ == "%osty.iface" && v.typ != "%osty.iface" {
			boxed, boxErr := g.boxInterfaceValue(stmt.Type, v)
			if boxErr != nil {
				return boxErr
			}
			v = boxed
		}
		if typ != v.typ {
			return unsupportedf("type-system", "let pattern type %s, value %s", typ, v.typ)
		}
	}
	return g.bindLetPattern(stmt.Pattern, v, stmt.Mut)
}

// boxInterfaceValue synthesises the `%osty.iface` fat pointer holding
// (data_ptr, vtable_ptr) for a concrete value assigned into an
// interface-typed slot. The concrete value is stack-allocated with
// `alloca`, the interface decl is resolved from `ifaceType`, and the
// (impl, iface) vtable symbol is pulled out of `interfaceInfo.impls`.
// Method dispatch through the boxed value is handled separately in
// emitInterfaceMethodCall.
func (g *generator) boxInterfaceValue(ifaceType ast.Type, v value) (value, error) {
	ifaceName := interfaceNominalName(ifaceType)
	if ifaceName == "" {
		return value{}, unsupportedf("type-system", "interface target %T", ifaceType)
	}
	iface := g.interfacesByName[ifaceName]
	if iface == nil {
		return value{}, unsupportedf("type-system", "unknown interface %q", ifaceName)
	}
	implName := strings.TrimPrefix(v.typ, "%")
	if implName == v.typ || implName == "" {
		return value{}, unsupportedf("type-system", "cannot box non-aggregate value %s into interface %q", v.typ, ifaceName)
	}
	var vtableSym string
	for _, impl := range iface.impls {
		if impl.implName == implName {
			vtableSym = impl.vtableSym
			break
		}
	}
	if vtableSym == "" {
		return value{}, unsupportedf("type-system", "type %q does not implement interface %q", implName, ifaceName)
	}
	emitter := g.toOstyEmitter()
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(slot, v.typ))
	emitter.body = append(emitter.body, mirStoreText(v.typ, v.ref, slot))
	step1 := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirInsertValueIfaceCtorText(step1, slot))
	step2 := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirInsertValueIfaceVtableText(step2, step1, vtableSym))
	g.takeOstyEmitter(emitter)
	// Phase 6e: tag the boxed value with its interface AST type so
	// downstream slots (e.g. `let mut s: Iface = …`) remember the
	// declared interface identity and can auto-box reassignments.
	return value{typ: "%osty.iface", ref: step2, sourceType: ifaceType}, nil
}

// interfaceNominalName returns the single-segment interface source
// name from a type annotation, or "" if the annotation isn't a simple
// named interface reference.
func interfaceNominalName(t ast.Type) string {
	nt, ok := t.(*ast.NamedType)
	if !ok || nt == nil {
		return ""
	}
	if len(nt.Path) != 1 {
		return ""
	}
	return nt.Path[0]
}

func (g *generator) emitAssign(stmt *ast.AssignStmt) error {
	if len(stmt.Targets) != 1 {
		return unsupported("statement", "multi-target assignment")
	}
	if stmt.Op != token.ASSIGN {
		return g.emitCompoundAssign(stmt)
	}
	target, ok := stmt.Targets[0].(*ast.Ident)
	if ok {
		slot, ok := g.lookupBinding(target.Name)
		if !ok {
			return unsupportedf("name", "assignment to unknown identifier %q", target.Name)
		}
		if !slot.mutable {
			return unsupportedf("statement", "assignment to immutable identifier %q", target.Name)
		}
		v, err := g.emitExprWithHintAndSourceType(stmt.Value, slot.sourceType, slot.listElemTyp, slot.listElemString, slot.mapKeyTyp, slot.mapValueTyp, slot.mapKeyString, slot.setElemTyp, slot.setElemString)
		if err != nil {
			return err
		}
		// Phase 6e: auto-box a concrete rhs when the binding's declared
		// slot type is an interface. Mirrors the let / return / call-arg
		// boxing paths so reassignment stays consistent with the other
		// interface-adjacent positions.
		if slot.typ == "%osty.iface" && v.typ != "%osty.iface" && slot.sourceType != nil {
			boxed, boxErr := g.boxInterfaceValue(slot.sourceType, v)
			if boxErr != nil {
				return boxErr
			}
			v = boxed
		}
		if v.typ != slot.typ {
			return unsupportedf("type-system", "assignment to %q type %s, value %s", target.Name, slot.typ, v.typ)
		}
		emitter := g.toOstyEmitter()
		llvmStore(emitter, toOstyValue(slot), toOstyValue(v))
		g.postGCWriteIfPointer(emitter, slot, v)
		g.takeOstyEmitter(emitter)
		return nil
	}
	field, ok := stmt.Targets[0].(*ast.FieldExpr)
	if !ok {
		if index, ok := stmt.Targets[0].(*ast.IndexExpr); ok {
			return g.emitIndexAssign(index, stmt.Value)
		}
	}
	if !ok {
		return unsupportedf("statement", "assignment target %T", stmt.Targets[0])
	}
	return g.emitFieldAssign(field, stmt.Value)
}

// compoundBinaryOp maps a compound-assignment token (`+=`, `-=`, …) to the
// binary operator token it desugars to (`+`, `-`, …). Returns false when the
// token is not a compound assignment. Delegates to the Osty-sourced
// `mirCompoundBinaryOpCode` (`toolchain/mir_generator.osty`); the
// Osty-side function takes the int code of every relevant token kind so
// the mapping stays in lockstep with the `internal/token` enum without
// re-implementing the table on the Osty side. The wrapper returns
// `(0, false)` for the no-match sentinel to match the pre-port behavior.
func compoundBinaryOp(op token.Kind) (token.Kind, bool) {
	code := mirCompoundBinaryOpCode(int(op),
		int(token.PLUSEQ), int(token.MINUSEQ), int(token.STAREQ),
		int(token.SLASHEQ), int(token.PERCENTEQ),
		int(token.BITANDEQ), int(token.BITOREQ), int(token.BITXOREQ),
		int(token.SHLEQ), int(token.SHREQ),
		int(token.PLUS), int(token.MINUS), int(token.STAR),
		int(token.SLASH), int(token.PERCENT),
		int(token.BITAND), int(token.BITOR), int(token.BITXOR),
		int(token.SHL), int(token.SHR),
	)
	if code < 0 {
		return 0, false
	}
	return token.Kind(code), true
}

// emitCompoundAssign lowers `x op= v` to `x = x op v`. Ident and single-level
// field targets desugar into a synthetic AssignStmt because re-reading them is
// a pure lookup. Index targets (`xs[i] += v`) instead evaluate the base and
// index exactly once, load the old element, compute `old op v`, and write the
// new value back — desugaring would double-evaluate `i` and break any
// side-effecting index expression.
func (g *generator) emitCompoundAssign(stmt *ast.AssignStmt) error {
	binOp, ok := compoundBinaryOp(stmt.Op)
	if !ok {
		return unsupportedf("statement", "compound assignment %q", stmt.Op)
	}
	target := stmt.Targets[0]
	if index, isIndex := target.(*ast.IndexExpr); isIndex {
		return g.emitIndexCompoundAssign(index, binOp, stmt.Value)
	}
	synth := &ast.BinaryExpr{
		PosV:  target.Pos(),
		EndV:  stmt.Value.End(),
		Op:    binOp,
		Left:  target,
		Right: stmt.Value,
	}
	desugared := &ast.AssignStmt{
		PosV:    stmt.PosV,
		EndV:    stmt.EndV,
		Op:      token.ASSIGN,
		Targets: stmt.Targets,
		Value:   synth,
	}
	return g.emitAssign(desugared)
}

func (g *generator) emitIndexCompoundAssign(target *ast.IndexExpr, binOp token.Kind, rhs ast.Expr) error {
	if target == nil {
		return unsupported("statement", "nil index compound-assignment target")
	}
	base, err := g.emitExpr(target.X)
	if err != nil {
		return err
	}
	if base.listElemTyp == "" {
		return unsupported("statement", "compound assignment on non-list index target")
	}
	index, err := g.emitExpr(target.Index)
	if err != nil {
		return err
	}
	if index.typ != "i64" {
		return unsupportedf("type-system", "list index type %s, want i64", index.typ)
	}
	old, err := g.emitListElementValue(base, index)
	if err != nil {
		return err
	}
	oldLoaded, err := g.loadIfPointer(old)
	if err != nil {
		return err
	}
	rhsVal, err := g.emitExprWithHint(rhs, "", false, "", "", false, "", false)
	if err != nil {
		return err
	}
	rhsLoaded, err := g.loadIfPointer(rhsVal)
	if err != nil {
		return err
	}
	isString := base.listElemTyp == "ptr" && oldLoaded.typ == "ptr"
	newVal, err := g.emitBinaryOpValues(binOp, oldLoaded, rhsLoaded, isString)
	if err != nil {
		return err
	}
	if newVal.typ != base.listElemTyp {
		return unsupportedf("type-system", "compound assignment result type %s, want %s", newVal.typ, base.listElemTyp)
	}
	return g.emitListAssignValue(base, index, newVal)
}

func (g *generator) emitFieldAssign(target *ast.FieldExpr, rhs ast.Expr) error {
	if target == nil {
		return unsupported("statement", "nil field assignment target")
	}
	// Walk the field chain inside-out collecting names, then flip to
	// outer→inner order. A single-level `a.f = v` produces fields=["f"],
	// while `a.b.c = v` produces fields=["b", "c"] with base = `a`.
	fields := []string{}
	cur := target
	for {
		if cur == nil {
			return unsupported("statement", "nil field assignment target")
		}
		if cur.IsOptional {
			return unsupported("statement", "optional field assignment is not supported")
		}
		fields = append([]string{cur.Name}, fields...)
		if next, ok := cur.X.(*ast.FieldExpr); ok {
			cur = next
			continue
		}
		break
	}
	switch base := cur.X.(type) {
	case *ast.Ident:
		return g.emitBindingFieldAssign(base, fields, rhs)
	case *ast.IndexExpr:
		return g.emitIndexedFieldAssign(base, fields, rhs)
	default:
		return unsupportedf("statement", "field assignment base %T", cur.X)
	}
}

func (g *generator) emitBindingFieldAssign(baseIdent *ast.Ident, fields []string, rhs ast.Expr) error {
	if baseIdent == nil {
		return unsupported("statement", "nil field assignment base")
	}
	slot, ok := g.lookupBinding(baseIdent.Name)
	if !ok {
		return unsupportedf("name", "assignment to unknown identifier %q", baseIdent.Name)
	}
	if !slot.ptr {
		materialized, ok, err := g.materializeAddressableLocalBinding(baseIdent.Name)
		if err != nil {
			return err
		}
		if !ok {
			return unsupportedf("statement", "field assignment on non-addressable binding %q", baseIdent.Name)
		}
		slot = materialized
	}
	// NB: we deliberately do not gate on `slot.mutable` here. Osty's
	// checker accepts field-through-param writes for managed context
	// structs (see `cx.env.returnTy = ...` throughout toolchain/), where
	// the parameter itself is not `mut` but the struct carries GC roots
	// so a writable alloca slot was materialised regardless. Mutability
	// is a frontend rule; the backend just lowers what got past check.
	// Resolve field chain against struct type info. Each step must land
	// inside another struct for nested chains; the innermost field
	// determines the rhs coercion target.
	steps := make([]fieldInfo, len(fields))
	curTyp := slot.typ
	for i, name := range fields {
		info := g.structsByType[curTyp]
		if info == nil {
			return unsupportedf("type-system", "field assignment on %s", curTyp)
		}
		field, ok := info.byName[name]
		if !ok {
			return unsupportedf("expression", "struct %q has no field %q", info.name, name)
		}
		steps[i] = field
		curTyp = field.typ
	}
	innermost := steps[len(steps)-1]
	v, err := g.emitExprWithHintAndSourceType(rhs, innermost.sourceType, innermost.listElemTyp, innermost.listElemString, innermost.mapKeyTyp, innermost.mapValueTyp, innermost.mapKeyString, innermost.setElemTyp, innermost.setElemString)
	if err != nil {
		return err
	}
	if v.typ != innermost.typ {
		return unsupportedf("type-system", "field assignment %q.%s type %s, value %s", baseIdent.Name, strings.Join(fields, "."), innermost.typ, v.typ)
	}
	root, err := g.loadIfPointer(slot)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	// Descend: extract each intermediate struct value so the insertvalue
	// rebuild at the leaf has a live current-value to update.
	levels := make([]value, len(steps))
	levels[0] = root
	for i := 1; i < len(steps); i++ {
		prev := levels[i-1]
		extracted := llvmExtractValue(emitter, toOstyValue(prev), steps[i-1].typ, steps[i-1].index)
		levels[i] = fromOstyValue(extracted)
	}
	// Rebuild: innermost insert first, then propagate back up.
	next := v
	for i := len(steps) - 1; i >= 0; i-- {
		rebuilt := llvmInsertValue(emitter, toOstyValue(levels[i]), toOstyValue(next), steps[i].index)
		next = fromOstyValue(rebuilt)
	}
	llvmStore(emitter, toOstyValue(slot), toOstyValue(next))
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitIndexedFieldAssign(target *ast.IndexExpr, fields []string, rhs ast.Expr) error {
	if target == nil {
		return unsupported("statement", "nil indexed field assignment target")
	}
	base, err := g.emitExpr(target.X)
	if err != nil {
		return err
	}
	index, err := g.emitExpr(target.Index)
	if err != nil {
		return err
	}
	root, err := g.emitListElementValue(base, index)
	if err != nil {
		return err
	}
	steps := make([]fieldInfo, len(fields))
	curTyp := root.typ
	for i, name := range fields {
		info := g.structsByType[curTyp]
		if info == nil {
			return unsupportedf("type-system", "field assignment on %s", curTyp)
		}
		field, ok := info.byName[name]
		if !ok {
			return unsupportedf("expression", "struct %q has no field %q", info.name, name)
		}
		steps[i] = field
		curTyp = field.typ
	}
	innermost := steps[len(steps)-1]
	v, err := g.emitExprWithHintAndSourceType(rhs, innermost.sourceType, innermost.listElemTyp, innermost.listElemString, innermost.mapKeyTyp, innermost.mapValueTyp, innermost.mapKeyString, innermost.setElemTyp, innermost.setElemString)
	if err != nil {
		return err
	}
	if v.typ != innermost.typ {
		return unsupportedf("type-system", "field assignment index.%s type %s, value %s", strings.Join(fields, "."), innermost.typ, v.typ)
	}
	emitter := g.toOstyEmitter()
	levels := make([]value, len(steps))
	levels[0] = root
	for i := 1; i < len(steps); i++ {
		prev := levels[i-1]
		extracted := llvmExtractValue(emitter, toOstyValue(prev), steps[i-1].typ, steps[i-1].index)
		levels[i] = fromOstyValue(extracted)
	}
	next := v
	for i := len(steps) - 1; i >= 0; i-- {
		rebuilt := llvmInsertValue(emitter, toOstyValue(levels[i]), toOstyValue(next), steps[i].index)
		next = fromOstyValue(rebuilt)
	}
	g.takeOstyEmitter(emitter)
	return g.emitListAssignValue(base, index, next)
}

func (g *generator) emitIndexAssign(target *ast.IndexExpr, rhs ast.Expr) error {
	if target == nil {
		return unsupported("statement", "nil index assignment target")
	}
	base, err := g.emitExpr(target.X)
	if err != nil {
		return err
	}
	if base.listElemTyp == "" {
		return unsupported("statement", "index assignment currently supports lists only")
	}
	index, err := g.emitExpr(target.Index)
	if err != nil {
		return err
	}
	if index.typ != "i64" {
		return unsupportedf("type-system", "list index type %s, want i64", index.typ)
	}
	v, err := g.emitExprWithHint(rhs, "", false, "", "", false, "", false)
	if err != nil {
		return err
	}
	if v.typ != base.listElemTyp {
		return unsupportedf("type-system", "list assignment value type %s, want %s", v.typ, base.listElemTyp)
	}
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return err
	}
	return g.emitListAssignValue(base, index, loaded)
}

func (g *generator) emitListAssignValue(base, index, v value) error {
	if base.listElemTyp == "" {
		return unsupported("statement", "list assignment currently supports lists only")
	}
	if index.typ != "i64" {
		return unsupportedf("type-system", "list index type %s, want i64", index.typ)
	}
	if v.typ != base.listElemTyp {
		return unsupportedf("type-system", "list assignment value type %s, want %s", v.typ, base.listElemTyp)
	}
	emitter := g.toOstyEmitter()
	if listUsesTypedRuntime(base.listElemTyp) {
		symbol := listRuntimeSetSymbol(base.listElemTyp)
		g.declareRuntimeSymbol(symbol, "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: base.listElemTyp}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			symbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(index), toOstyValue(v)}),
		))
	} else {
		traceSymbol := g.traceCallbackSymbol(base.listElemTyp, g.rootPathsForType(base.listElemTyp))
		addr := g.spillValueAddress(emitter, "list.set", v)
		sizeValue := g.emitTypeSize(emitter, base.listElemTyp)
		g.declareRuntimeSymbol(listRuntimeSetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimeSetBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(index), {typ: "ptr", name: addr}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
	}
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitListElementValue(base, index value) (value, error) {
	if base.listElemTyp == "" {
		return value{}, unsupported("expression", "index expression on non-list base")
	}
	if index.typ != "i64" {
		return value{}, unsupportedf("type-system", "list index type %s, want i64", index.typ)
	}
	if listUsesTypedRuntime(base.listElemTyp) {
		symbol := listRuntimeGetSymbol(base.listElemTyp)
		g.declareRuntimeSymbol(symbol, base.listElemTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, base.listElemTyp, symbol, []*LlvmValue{toOstyValue(base), toOstyValue(index)})
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = base.listElemTyp == "ptr"
		v.rootPaths = g.rootPathsForType(base.listElemTyp)
		return v, nil
	}
	traceSymbol := g.traceCallbackSymbol(base.listElemTyp, g.rootPathsForType(base.listElemTyp))
	emitter := g.toOstyEmitter()
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(slot, base.listElemTyp))
	sizeValue := g.emitTypeSize(emitter, base.listElemTyp)
	g.declareRuntimeSymbol(listRuntimeGetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		listRuntimeGetBytesSymbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(index), {typ: "ptr", name: slot}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
	))
	loaded := g.loadValueFromAddress(emitter, base.listElemTyp, slot)
	g.takeOstyEmitter(emitter)
	loaded.rootPaths = g.rootPathsForType(base.listElemTyp)
	return loaded, nil
}

func (g *generator) emitFor(stmt *ast.ForStmt) error {
	if stmt.IsForLet {
		return g.emitForLet(stmt)
	}
	if stmt.Body == nil {
		return unsupported("control-flow", "for has no body")
	}
	if stmt.Pattern == nil {
		return g.emitWhileFor(stmt)
	}
	// `for (k, v) in m` — map iteration. Detect before the
	// ident-pattern reduction since identPatternName would reject the
	// tuple.
	if tuplePat, ok := stmt.Pattern.(*ast.TuplePat); ok && len(tuplePat.Elems) == 2 {
		if iterInfo, ok := g.staticExprInfo(stmt.Iter); ok &&
			iterInfo.typ == "ptr" &&
			iterInfo.mapKeyTyp != "" && iterInfo.mapValueTyp != "" {
			kName, err := identPatternName(tuplePat.Elems[0])
			if err != nil {
				return err
			}
			vName, err := identPatternName(tuplePat.Elems[1])
			if err != nil {
				return err
			}
			return g.emitMapFor(stmt, kName, vName, iterInfo.mapKeyTyp, iterInfo.mapValueTyp, iterInfo.mapKeyString)
		}
	}
	iterName, err := identPatternName(stmt.Pattern)
	if err != nil {
		return err
	}
	if iterInfo, ok := g.staticExprInfo(stmt.Iter); ok && iterInfo.typ == "ptr" && iterInfo.listElemTyp != "" {
		return g.emitListFor(stmt, iterName, iterInfo.listElemTyp)
	}
	// `for x in set` — snapshot the Set<T> into a List<T> via
	// `osty_rt_set_to_list` and iterate that list. Matches Map's
	// snapshot-then-walk shape: weakly-consistent under concurrent
	// mutation, no out-of-bounds panic. Reaches the injected
	// Set<T>.union / intersect / difference bodies which walk `self`
	// once injection lands those specializations.
	if iterInfo, ok := g.staticExprInfo(stmt.Iter); ok && iterInfo.typ == "ptr" && iterInfo.setElemTyp != "" {
		return g.emitSetFor(stmt, iterName, iterInfo.setElemTyp, iterInfo.setElemString)
	}
	rng, ok := stmt.Iter.(*ast.RangeExpr)
	if !ok {
		return unsupported("control-flow", "only range for-loops are supported")
	}
	if rng.Start == nil || rng.Stop == nil {
		return unsupported("control-flow", "open-ended ranges are not supported")
	}
	start, err := g.emitExpr(rng.Start)
	if err != nil {
		return err
	}
	stop, err := g.emitExpr(rng.Stop)
	if err != nil {
		return err
	}
	if start.typ != "i64" || stop.typ != "i64" {
		return unsupported("type-system", "range bounds must be Int")
	}
	emitter := g.toOstyEmitter()
	loopSafepointSlot := g.allocLoopSafepointCounter(emitter)
	loop := llvmRangeStart(emitter, iterName, toOstyValue(start), toOstyValue(stop), rng.Inclusive)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel("for.cont")
	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    loop.endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	g.bindLocal(iterName, value{typ: "i64", ref: loop.current})
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.emitLoopSafepoint(emitter, loopSafepointSlot)
	llvmRangeEnd(emitter, loop)
	g.attachVectorizeMD(emitter, loop.condLabel)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func (g *generator) emitWhileFor(stmt *ast.ForStmt) error {
	emitter := g.toOstyEmitter()
	loopSafepointSlot := g.allocLoopSafepointCounter(emitter)
	condLabel := llvmNextLabel(emitter, "for.cond")
	bodyLabel := llvmNextLabel(emitter, "for.body")
	continueLabel := llvmNextLabel(emitter, "for.cont")
	endLabel := llvmNextLabel(emitter, "for.end")
	emitter.body = append(emitter.body, mirBrUncondText(condLabel))
	emitter.body = append(emitter.body, mirLabelText(condLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(condLabel)

	if stmt.Iter == nil {
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirBrUncondText(bodyLabel))
		emitter.body = append(emitter.body, mirLabelText(bodyLabel))
		g.takeOstyEmitter(emitter)
	} else {
		cond, err := g.emitExpr(stmt.Iter)
		if err != nil {
			return err
		}
		if cond.typ != "i1" {
			return unsupportedf("type-system", "for condition type %s, want i1", cond.typ)
		}
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirBrCondText(toOstyValue(cond).name, bodyLabel, endLabel))
		emitter.body = append(emitter.body, mirLabelText(bodyLabel))
		g.takeOstyEmitter(emitter)
	}
	g.enterBlock(bodyLabel)

	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(continueLabel)
	emitter = g.toOstyEmitter()
	g.emitLoopSafepoint(emitter, loopSafepointSlot)
	emitter.body = append(emitter.body, mirBrUncondText(condLabel))
	g.attachVectorizeMD(emitter, condLabel)
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	return nil
}

func (g *generator) emitForLet(stmt *ast.ForStmt) error {
	if stmt.Body == nil {
		return unsupported("control-flow", "for has no body")
	}
	if stmt.Pattern == nil {
		return unsupported("control-flow", "for-let requires a pattern")
	}
	if stmt.Iter == nil {
		return unsupported("control-flow", "for-let requires an iterator expression")
	}
	emitter := g.toOstyEmitter()
	loopSafepointSlot := g.allocLoopSafepointCounter(emitter)
	condLabel := llvmNextLabel(emitter, "for.cond")
	bodyLabel := llvmNextLabel(emitter, "for.body")
	continueLabel := llvmNextLabel(emitter, "for.cont")
	endLabel := llvmNextLabel(emitter, "for.end")
	emitter.body = append(emitter.body, mirBrUncondText(condLabel))
	emitter.body = append(emitter.body, mirLabelText(condLabel))
	g.takeOstyEmitter(emitter)

	scrutinee, err := g.emitExpr(stmt.Iter)
	if err != nil {
		return err
	}
	cond, bind, err := g.ifLetCondition(stmt.Pattern, scrutinee)
	if err != nil {
		return err
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirBrCondText(cond.name, bodyLabel, endLabel))
	emitter.body = append(emitter.body, mirLabelText(bodyLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(bodyLabel)

	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	if bind != nil {
		if err := bind(); err != nil {
			if len(g.locals) > scopeDepth {
				g.popScope()
			}
			g.popLoop()
			return err
		}
	}
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(continueLabel)
	emitter = g.toOstyEmitter()
	g.emitLoopSafepoint(emitter, loopSafepointSlot)
	emitter.body = append(emitter.body, mirBrUncondText(condLabel))
	g.attachVectorizeMD(emitter, condLabel)
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	return nil
}

func (g *generator) emitReturn(stmt *ast.ReturnStmt) error {
	if stmt == nil {
		return unsupported("statement", "nil return")
	}
	var ret value
	var err error
	switch {
	case stmt.Value == nil:
		if g.returnType != "" && g.returnType != "void" {
			return unsupported("function-signature", "bare return in value-returning function")
		}
	case g.returnType == "" || g.returnType == "void":
		return unsupported("function-signature", "return with value in void-returning function")
	default:
		ret, err = g.emitExprWithHintAndSourceType(stmt.Value, g.returnSourceType, g.returnListElemTyp, g.returnListElemString, g.returnMapKeyTyp, g.returnMapValueTyp, g.returnMapKeyString, g.returnSetElemTyp, g.returnSetElemString)
		if err != nil {
			return err
		}
		// Phase 6d: auto-box a concrete return value when the declared
		// return type is an interface. Mirrors the `let s: Sized = v`
		// path in emitLet.
		if g.returnType == "%osty.iface" && ret.typ != "%osty.iface" && g.returnSourceType != nil {
			boxed, boxErr := g.boxInterfaceValue(g.returnSourceType, ret)
			if boxErr != nil {
				return boxErr
			}
			ret = boxed
		}
		if ret.typ != g.returnType {
			return unsupportedf("type-system", "return type %s, want %s", ret.typ, g.returnType)
		}
	}
	if err := g.emitAllPendingDefers(); err != nil {
		return err
	}
	if !g.currentReachable {
		return nil
	}
	emitter := g.toOstyEmitter()
	g.releaseGCRoots(emitter)
	switch {
	case stmt.Value == nil && g.returnType == "":
		llvmReturnI32Zero(emitter)
	case stmt.Value == nil && g.returnType == "void":
		emitter.body = append(emitter.body, mirRetVoidText())
	default:
		llvmReturn(emitter, toOstyValue(ret))
	}
	g.takeOstyEmitter(emitter)
	g.leaveBlock()
	return nil
}

func (g *generator) emitExprStmt(expr ast.Expr) error {
	switch expr.(type) {
	case *ast.BoolLit, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.ByteLit:
		// Pure literal statements are semantic no-ops in statement position.
		return nil
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		if block, ok := expr.(*ast.Block); ok {
			return g.emitScopedStmtBlock(block.Stmts)
		}
		_, err := g.emitExpr(expr)
		return err
	}
	if emitted, err := g.emitTestingCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitListMethodCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitMapMethodCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitSetMethodCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitRuntimeFFICallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitStdIoCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitOptionalUserCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitUserCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitIndirectUserCallStmt(call); emitted || err != nil {
		return err
	}
	if id, ok := call.Fn.(*ast.Ident); ok && id.Name == "println" {
		return g.emitPrintln(call)
	}
	return g.emitPrintln(call)
}

func (g *generator) emitTestingCallStmt(call *ast.CallExpr) (bool, error) {
	method, ok := g.testingCallMethod(call)
	if !ok {
		return false, nil
	}
	switch method {
	case "assert", "assertTrue":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return true, unsupportedf("call", "testing.%s requires one positional argument", method)
		}
		cond, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return true, err
		}
		condText := g.sourceSpanText(call.Args[0].Value)
		return true, g.emitTestingAssertionLazy(cond, func() (value, error) {
			return g.buildAssertCondMessage(call, method, condText)
		})
	case "assertFalse":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return true, unsupported("call", "testing.assertFalse requires one positional argument")
		}
		cond, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return true, err
		}
		if cond.typ != "i1" {
			return true, unsupportedf("type-system", "testing.assertFalse condition type %s, want i1", cond.typ)
		}
		emitter := g.toOstyEmitter()
		negated := llvmNotI1(emitter, toOstyValue(cond))
		g.takeOstyEmitter(emitter)
		condText := g.sourceSpanText(call.Args[0].Value)
		return true, g.emitTestingAssertionLazy(fromOstyValue(negated), func() (value, error) {
			return g.buildAssertCondMessage(call, "assertFalse", condText)
		})
	case "assertEq":
		return true, g.emitTestingCompare(call, token.EQ, "assertEq")
	case "assertNe":
		return true, g.emitTestingCompare(call, token.NEQ, "assertNe")
	case "fail":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return true, unsupported("call", "testing.fail requires one positional argument")
		}
		g.emitTestingAbort(g.testingFailureMessage(call, "fail"))
		return true, nil
	case "context":
		return true, g.emitTestingContextStmt(call)
	case "benchmark":
		return true, g.emitTestingBenchmarkStmt(call)
	case "expectOk":
		_, err := g.emitTestingExpect(call, false)
		return true, err
	case "expectError":
		_, err := g.emitTestingExpect(call, true)
		return true, err
	case "snapshot":
		return true, g.emitTestingSnapshot(call)
	case "property", "propertyN", "propertySeeded":
		return true, g.emitTestingProperty(call, method)
	default:
		return true, unsupportedf("call", "testing.%s is not supported by LLVM yet", method)
	}
}

// emitTestingSnapshot lowers `testing.snapshot(name, output)` into a
// call to the runtime helper osty_rt_test_snapshot, which handles the
// golden file lifecycle (create / compare / emit diff + exit). The
// generator pins the test source path at compile time so snapshot
// discovery does not depend on the process working directory — each
// test binary hard-codes its own origin file.
func (g *generator) emitTestingSnapshot(call *ast.CallExpr) error {
	if len(call.Args) != 2 {
		return unsupportedf("call", "testing.snapshot requires two positional arguments")
	}
	for _, arg := range call.Args {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return unsupportedf("call", "testing.snapshot requires positional arguments")
		}
	}
	name, err := g.emitTestingStringArg(call.Args[0].Value, "testing.snapshot name")
	if err != nil {
		return err
	}
	output, err := g.emitTestingStringArg(call.Args[1].Value, "testing.snapshot output")
	if err != nil {
		return err
	}
	g.declareRuntimeSymbol("osty_rt_test_snapshot", "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	sourceLit := llvmStringLiteral(emitter, g.sourcePath)
	llvmCallVoid(emitter, "osty_rt_test_snapshot", []*LlvmValue{toOstyValue(name), toOstyValue(output), sourceLit})
	g.takeOstyEmitter(emitter)
	return nil
}

// emitTestingStringArg evaluates a String-typed argument to one of
// the testing helpers and enforces the ptr check once — shared by
// emitTestingSnapshot and emitRuntimeStringDiff so both surface the
// same diagnostic shape when a non-String sneaks through.
func (g *generator) emitTestingStringArg(expr ast.Expr, field string) (value, error) {
	v, err := g.emitExpr(expr)
	if err != nil {
		return value{}, err
	}
	if v.typ != "ptr" {
		return value{}, unsupportedf("type-system", "%s must be String, got %s", field, v.typ)
	}
	return v, nil
}

func (g *generator) testingCallMethod(call *ast.CallExpr) (string, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return "", false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.testingAliases[alias.Name] {
		return "", false
	}
	return field.Name, true
}

func (g *generator) emitTestingCompare(call *ast.CallExpr, op token.Kind, name string) error {
	if len(call.Args) != 2 {
		return unsupportedf("call", "testing.%s requires two positional arguments", name)
	}
	for _, arg := range call.Args {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return unsupportedf("call", "testing.%s requires positional arguments", name)
		}
	}
	left, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	right, err := g.emitExpr(call.Args[1].Value)
	if err != nil {
		return err
	}
	isStringLeft := g.staticExprIsString(call.Args[0].Value)
	isStringRight := g.staticExprIsString(call.Args[1].Value)
	cond, err := g.emitCompare(op, left, right, isStringLeft || isStringRight)
	if err != nil {
		return err
	}
	leftText := g.sourceSpanText(call.Args[0].Value)
	rightText := g.sourceSpanText(call.Args[1].Value)
	// Runtime value capture happens lazily on the fail branch so a
	// passing assertion pays nothing. The SSA names of `left` and
	// `right` are still valid inside the fail block because the
	// comparison block dominates it.
	return g.emitTestingAssertionLazy(cond, func() (value, error) {
		return g.buildAssertCompareMessage(call, name, leftText, rightText, left, isStringLeft, right, isStringRight)
	})
}

func (g *generator) emitTestingExpect(call *ast.CallExpr, wantErr bool) (value, error) {
	method := "expectOk"
	wantTag := "0"
	payloadIndex := 1
	if wantErr {
		method = "expectError"
		wantTag = "1"
		payloadIndex = 2
	}
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, unsupportedf("call", "testing.%s requires one positional argument", method)
	}
	result, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, err
	}
	info, ok := g.resultTypes[result.typ]
	if !ok {
		return value{}, unsupportedf("type-system", "testing.%s requires a Result<T, E> value", method)
	}
	payloadType := info.okTyp
	if wantErr {
		payloadType = info.errTyp
	}
	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(result), "i64", 0)
	cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: wantTag}))
	okLabel := llvmNextLabel(emitter, "test.expect.ok")
	failLabel := llvmNextLabel(emitter, "test.expect.fail")
	emitter.body = append(emitter.body, mirBrCondText(cond.name, okLabel, failLabel))
	emitter.body = append(emitter.body, mirLabelText(failLabel))
	g.takeOstyEmitter(emitter)
	g.currentBlock = failLabel
	exprText := g.sourceSpanText(call.Args[0].Value)
	msg, err := g.buildAssertCondMessage(call, method, exprText)
	if err != nil {
		return value{}, err
	}
	emitter = g.toOstyEmitter()
	llvmPrintlnString(emitter, toOstyValue(msg))
	g.declareRuntimeSymbol("exit", "void", []paramInfo{{typ: "i32"}})
	emitter.body = append(emitter.body, mirCallExitOneText())
	emitter.body = append(emitter.body, mirUnreachableText())
	emitter.body = append(emitter.body, mirLabelText(okLabel))
	payload := llvmExtractValue(emitter, toOstyValue(result), payloadType, payloadIndex)
	g.takeOstyEmitter(emitter)
	g.currentBlock = okLabel
	out := fromOstyValue(payload)
	out.gcManaged = payloadType == "ptr"
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

func (g *generator) emitTestingContextStmt(call *ast.CallExpr) error {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[1].Value == nil {
		return unsupported("call", "testing.context requires a message and a zero-arg closure")
	}
	closure, ok := call.Args[1].Value.(*ast.ClosureExpr)
	if !ok {
		return unsupported("call", "testing.context requires a closure body")
	}
	if len(closure.Params) != 0 || closure.ReturnType != nil || closure.Body == nil {
		return unsupported("call", "testing.context requires a zero-arg closure with inferred unit return")
	}
	g.pushScope()
	defer g.popScope()
	return g.emitTestingClosureBody(closure.Body)
}

func (g *generator) emitTestingClosureBody(body ast.Expr) error {
	switch expr := body.(type) {
	case *ast.Block:
		return g.emitBlock(expr.Stmts)
	case *ast.IfExpr:
		return g.emitIfStmt(expr)
	default:
		return g.emitExprStmt(expr)
	}
}

// emitTestingBenchmarkStmt inlines `testing.benchmark(N, || {...})` as
// a timing loop — spec §11.4. The trailing expression of the closure
// body (the required `Ok(())` terminator) is dropped on inline; `?` in
// the closure is unsupported because inlined `?` would return from the
// enclosing test main, not the closure.
func (g *generator) emitTestingBenchmarkStmt(call *ast.CallExpr) error {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return unsupported("call", "testing.benchmark requires two positional arguments (iterations, closure)")
	}
	closure, ok := call.Args[1].Value.(*ast.ClosureExpr)
	if !ok {
		return unsupported("call", "testing.benchmark requires a closure literal as its second argument")
	}
	if len(closure.Params) != 0 || closure.Body == nil {
		return unsupported("call", "testing.benchmark requires a zero-arg closure body")
	}
	block, ok := closure.Body.(*ast.Block)
	if !ok {
		return unsupported("call", "testing.benchmark requires a block-bodied closure")
	}

	iters, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	if iters.typ != "i64" {
		return unsupportedf("type-system", "testing.benchmark iterations type %s, want i64", iters.typ)
	}

	stmts := block.Stmts
	if n := len(stmts); n > 0 {
		if _, ok := stmts[n-1].(*ast.ExprStmt); ok {
			stmts = stmts[:n-1]
		}
	}

	// Auto-tune probe: when OSTY_BENCH_TIME_NS > 0 we ignore the user's
	// declared N and estimate a new N from a short probe. The env var is
	// set by `osty test --bench --benchtime <dur>`. Probe overhead (~10
	// body invocations + 2 clock samples) is charged on top of the final
	// bench like warmup.
	g.declareRuntimeSymbol(benchClockRuntimeSymbol(), "i64", nil)
	g.declareRuntimeSymbol(benchTargetRuntimeSymbol(), "i64", nil)
	nslot, err := g.emitBenchAutoTuneN(stmts, iters.ref)
	if err != nil {
		return err
	}

	emitter := g.toOstyEmitter()
	effectiveN := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirLoadI64Text(effectiveN, nslot))
	// Warmup: clamp(N/10, 1, 1000). Cold-cache / branch-predictor misses
	// get amortized before the first clock sample.
	warmupDiv := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSDivI64LiteralText(warmupDiv, effectiveN, "10"))
	warmupGE1Cond := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSGTI64ZeroText(warmupGE1Cond, warmupDiv))
	warmupGE1 := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64LiteralRhsText(warmupGE1, warmupGE1Cond, warmupDiv, "1"))
	warmupCapCond := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSLTI64LiteralText(warmupCapCond, warmupGE1, "1000"))
	warmupTemp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64LiteralRhsText(warmupTemp, warmupCapCond, warmupGE1, "1000"))
	g.takeOstyEmitter(emitter)

	if err := g.emitBenchInlineLoop(stmts, value{typ: "i64", ref: warmupTemp}, "bench.warm", ""); err != nil {
		return err
	}

	// Allocate per-iter samples buffer. The runtime returns NULL when N
	// <= 0, so osty_rt_bench_samples_report is a no-op for that case
	// and the summary line still emits with avg=0.
	g.declareRuntimeSymbol(benchSamplesNewSymbol(), "ptr", []paramInfo{{typ: "i64"}})
	g.declareRuntimeSymbol(benchSamplesRecordSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	g.declareRuntimeSymbol(benchSamplesReportSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	g.declareRuntimeSymbol(benchSamplesFreeSymbol(), "void", []paramInfo{{typ: "ptr"}})
	emitter = g.toOstyEmitter()
	finalN := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirLoadI64Text(finalN, nslot))
	samplesPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallRuntimePtrI64Text(samplesPtr, benchSamplesNewSymbol(), finalN))
	startTemp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallRuntimeI64NoArgsText(startTemp, benchClockRuntimeSymbol()))
	g.takeOstyEmitter(emitter)

	if err := g.emitBenchInlineLoop(stmts, value{typ: "i64", ref: finalN}, "bench.run", samplesPtr); err != nil {
		return err
	}

	emitter = g.toOstyEmitter()
	endTemp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallRuntimeI64NoArgsText(endTemp, benchClockRuntimeSymbol()))
	totalTemp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSubI64Text(totalTemp, endTemp, startTemp))
	finalNReload := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirLoadI64Text(finalNReload, nslot))
	nonZero := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSGTI64ZeroText(nonZero, finalNReload))
	divLabel := llvmNextLabel(emitter, "bench.div")
	skipLabel := llvmNextLabel(emitter, "bench.skip")
	joinLabel := llvmNextLabel(emitter, "bench.join")
	emitter.body = append(emitter.body, mirBrCondText(nonZero, divLabel, skipLabel))
	emitter.body = append(emitter.body, mirLabelText(divLabel))
	divTemp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSDivI64Text(divTemp, totalTemp, finalNReload))
	emitter.body = append(emitter.body, mirBrUncondText(joinLabel))
	emitter.body = append(emitter.body, mirLabelText(skipLabel))
	emitter.body = append(emitter.body, mirBrUncondText(joinLabel))
	emitter.body = append(emitter.body, mirLabelText(joinLabel))
	avgTemp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirPhiI64FromValueOrZeroText(avgTemp, divTemp, divLabel, skipLabel))
	g.takeOstyEmitter(emitter)
	g.currentBlock = joinLabel
	g.currentReachable = true

	itersStr, err := g.emitRuntimeIntToString(value{typ: "i64", ref: finalNReload})
	if err != nil {
		return err
	}
	totalStr, err := g.emitRuntimeIntToString(value{typ: "i64", ref: totalTemp})
	if err != nil {
		return err
	}
	avgStr, err := g.emitRuntimeIntToString(value{typ: "i64", ref: avgTemp})
	if err != nil {
		return err
	}
	var callLine int
	if call != nil {
		callLine = call.Pos().Line
	}
	prefix := fmt.Sprintf("bench %s", g.sourceLineLabel(callLine, "<bench>"))
	msg, err := g.foldAssertionMessage(
		staticAssertPart(prefix+" iter="),
		dynamicAssertPart(itersStr),
		staticAssertPart(" total="),
		dynamicAssertPart(totalStr),
		staticAssertPart("ns avg="),
		dynamicAssertPart(avgStr),
		staticAssertPart("ns"),
	)
	if err != nil {
		return err
	}
	emitter = g.toOstyEmitter()
	llvmPrintlnString(emitter, toOstyValue(msg))
	emitter.body = append(emitter.body, mirCallRuntimeVoidPtrI64Text(benchSamplesReportSymbol(), samplesPtr, finalNReload))
	emitter.body = append(emitter.body, mirCallRuntimeVoidPtrText(benchSamplesFreeSymbol(), samplesPtr))
	g.takeOstyEmitter(emitter)
	return nil
}

func benchClockRuntimeSymbol() string  { return mirRtBenchNowNanosSymbol() }
func benchTargetRuntimeSymbol() string { return mirRtBenchTargetNsSymbol() }

// emitBenchAutoTuneN probes the body when --benchtime is active and
// picks N from that sample. Returns an alloca that downstream code
// loads whenever it needs the effective iteration count.
//
// Algorithm:
//
//	target := osty_rt_bench_target_ns()      // 0 when --benchtime unset
//	if target == 0:
//	    N := user_declared_N
//	else:
//	    run body probe_iters times; measure elapsed
//	    est := target * probe_iters / max(elapsed, 1)
//	    est := est * 6 / 5                   // 20% headroom
//	    N   := clamp(est, 10, 100_000_000)
//
// The probe body runs through the same bench-closure context as the
// timing run, so a `?` failure during probing still aborts cleanly.
func (g *generator) emitBenchAutoTuneN(stmts []ast.Stmt, declaredN string) (string, error) {
	emitter := g.toOstyEmitter()
	nslot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaI64Text(nslot))
	emitter.body = append(emitter.body, mirStoreI64Text(declaredN, nslot))
	target := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallRuntimeI64NoArgsText(target, benchTargetRuntimeSymbol()))
	autoCond := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSGTI64ZeroText(autoCond, target))
	probeLabel := llvmNextLabel(emitter, "bench.probe")
	afterLabel := llvmNextLabel(emitter, "bench.after_probe")
	emitter.body = append(emitter.body, mirBrCondText(autoCond, probeLabel, afterLabel))
	emitter.body = append(emitter.body, mirLabelText(probeLabel))
	probeStart := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallRuntimeI64NoArgsText(probeStart, benchClockRuntimeSymbol()))
	g.takeOstyEmitter(emitter)
	g.currentBlock = probeLabel

	const probeIters int64 = 10
	probeCount := value{typ: "i64", ref: fmt.Sprintf("%d", probeIters)}
	if err := g.emitBenchInlineLoop(stmts, probeCount, "bench.probe", ""); err != nil {
		return "", err
	}

	emitter = g.toOstyEmitter()
	probeEnd := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallRuntimeI64NoArgsText(probeEnd, benchClockRuntimeSymbol()))
	probeElapsed := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSubI64Text(probeElapsed, probeEnd, probeStart))
	// Guard against 0-ns elapsed (clock resolution floor): treat as 1ns
	// so the subsequent sdiv can't trap or explode.
	elapsedPos := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSGTI64ZeroText(elapsedPos, probeElapsed))
	elapsedSafe := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64LiteralRhsText(elapsedSafe, elapsedPos, probeElapsed, "1"))
	targetScaled := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirMulI64LiteralText(targetScaled, target, strconv.FormatInt(probeIters, 10)))
	estRaw := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSDivI64Text(estRaw, targetScaled, elapsedSafe))
	estHead := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirMulI64LiteralText(estHead, estRaw, "6"))
	estAdj := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSDivI64LiteralText(estAdj, estHead, "5"))
	estFloorCond := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSLTI64LiteralText(estFloorCond, estAdj, "10"))
	estFloored := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64LiteralLhsText(estFloored, estFloorCond, "10", estAdj))
	estCapCond := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSGTI64LiteralText(estCapCond, estFloored, "100000000"))
	estFinal := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64LiteralLhsText(estFinal, estCapCond, "100000000", estFloored))
	emitter.body = append(emitter.body, mirStoreI64Text(estFinal, nslot))
	emitter.body = append(emitter.body, mirBrUncondText(afterLabel))
	emitter.body = append(emitter.body, mirLabelText(afterLabel))
	g.takeOstyEmitter(emitter)
	g.currentBlock = afterLabel
	g.currentReachable = true
	return nslot, nil
}

// emitBenchInlineLoop emits `for _ in 0..count { stmts }` with the same
// loop/scope/GC-safepoint plumbing as the primary bench timing loop.
// The closure body is re-inlined on each call so warmup and the timing
// loop share one AST source while getting fresh SSA temps / scopes.
//
// When samplesPtr is non-empty, the loop brackets the body with two
// osty_rt_bench_now_nanos() calls and records `end-start` at
// samples[i]. Per-iter sampling roughly doubles the clock overhead so
// it's only wired in from the stats path, never in warmup.
func (g *generator) emitBenchInlineLoop(stmts []ast.Stmt, count value, labelPrefix, samplesPtr string) error {
	emitter := g.toOstyEmitter()
	var iterStartSlot string
	if samplesPtr != "" {
		// Alloca lives in the block that calls into the loop, never in
		// the loop body — LLVM treats alloca-in-loop as an explicit
		// stack-growing op and mem2reg won't promote it.
		iterStartSlot = llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirAllocaI64Text(iterStartSlot))
	}
	zeroV := &LlvmValue{name: "0", typ: "i64"}
	stopV := &LlvmValue{name: count.ref, typ: "i64"}
	loop := llvmRangeStart(emitter, g.hiddenBenchIterName(), zeroV, stopV, false)
	if samplesPtr != "" {
		tstart := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirCallRuntimeI64NoArgsText(tstart, benchClockRuntimeSymbol()))
		emitter.body = append(emitter.body, mirStoreI64Text(tstart, iterStartSlot))
	}
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel(labelPrefix + ".cont")
	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    loop.endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	g.benchClosureDepth++
	err := g.emitBlock(stmts)
	g.benchClosureDepth--
	if err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if samplesPtr != "" && g.currentReachable {
		emitter = g.toOstyEmitter()
		tstart := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirLoadI64Text(tstart, iterStartSlot))
		tend := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirCallRuntimeI64NoArgsText(tend, benchClockRuntimeSymbol()))
		delta := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirSubI64Text(delta, tend, tstart))
		emitter.body = append(emitter.body, mirCallRuntimeVoidPtrI64I64Text(benchSamplesRecordSymbol(), samplesPtr, loop.current, delta))
		g.takeOstyEmitter(emitter)
	}
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.emitGCSafepointKind(emitter, safepointKindLoop)
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func benchSamplesNewSymbol() string    { return mirRtBenchSymbol("samples_new") }
func benchSamplesRecordSymbol() string { return mirRtBenchSymbol("samples_record") }
func benchSamplesReportSymbol() string { return mirRtBenchSymbol("samples_report") }
func benchSamplesFreeSymbol() string   { return mirRtBenchSymbol("samples_free") }

// benchQuestionFailMessage is the runtime message printed when `?`
// inside a bench closure sees Err / None. The shape mirrors
// testingFailureMessage so `osty test --bench` failure output stays
// uniform with the rest of the testing surface.
func (g *generator) benchQuestionFailMessage(expr *ast.QuestionExpr) string {
	var line int
	if expr != nil {
		line = expr.Pos().Line
	}
	return fmt.Sprintf("bench `?` propagated failure at %s", g.sourceLineLabel(line, "<bench>"))
}

// hiddenBenchIterName allocates a loop-counter name that can't shadow a
// user identifier; llvmRangeStart needs somewhere to bind `%current`.
func (g *generator) hiddenBenchIterName() string {
	g.hiddenLocalID++
	return fmt.Sprintf("__bench_i_%d", g.hiddenLocalID)
}

func (g *generator) emitTestingAssertion(cond value, message string) error {
	if cond.typ != "i1" {
		return unsupportedf("type-system", "testing assertion condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	okLabel := llvmNextLabel(emitter, "test.ok")
	failLabel := llvmNextLabel(emitter, "test.fail")
	emitter.body = append(emitter.body, mirBrCondText(cond.ref, okLabel, failLabel))
	emitter.body = append(emitter.body, mirLabelText(failLabel))
	g.emitTestingAbortWithEmitter(emitter, message, okLabel)
	g.takeOstyEmitter(emitter)
	g.currentBlock = okLabel
	return nil
}

func (g *generator) emitTestingAbort(message string) {
	emitter := g.toOstyEmitter()
	deadLabel := llvmNextLabel(emitter, "test.dead")
	g.emitTestingAbortWithEmitter(emitter, message, deadLabel)
	g.takeOstyEmitter(emitter)
	g.currentBlock = deadLabel
}

func (g *generator) emitTestingAbortWithEmitter(emitter *LlvmEmitter, message string, nextLabel string) {
	text := llvmStringLiteral(emitter, message)
	llvmPrintlnString(emitter, text)
	g.declareRuntimeSymbol("exit", "void", []paramInfo{{typ: "i32"}})
	emitter.body = append(emitter.body, mirCallExitOneText())
	emitter.body = append(emitter.body, mirUnreachableText())
	emitter.body = append(emitter.body, mirLabelText(nextLabel))
}

func (g *generator) testingFailureMessage(call *ast.CallExpr, name string) string {
	var line int
	if call != nil {
		line = call.Pos().Line
	}
	return mirTestingFailureMessage(name, g.sourceLineLabel(line, "<test>"))
}

// sourceLineLabel renders `<abs-path>:<line>` for a diagnostic site.
// When the source path is unset (happens in some in-memory test paths)
// `fallback` stands in so output stays scrapeable.
func (g *generator) sourceLineLabel(line int, fallback string) string {
	source := g.sourcePath
	if source == "" {
		source = fallback
	} else if abs, err := filepath.Abs(source); err == nil {
		source = abs
	}
	return mirSourceLineLabelText(source, strconv.Itoa(line))
}

// emitTestingAssertionLazy mirrors emitTestingAssertion but builds the
// failure message inside the fail branch via buildMessage. Lazy
// construction keeps the passing path cheap: Int/Float/Bool value
// formatting and string concatenation only run on an actual failure,
// never during the hot path where millions of assertions succeed.
//
// buildMessage runs under the fail label so any runtime calls it
// emits (osty_rt_int_to_string, osty_rt_strings_Concat, …) land in
// that block. The returned ptr value is then printed and the program
// exits with status 1.
func (g *generator) emitTestingAssertionLazy(cond value, buildMessage func() (value, error)) error {
	if cond.typ != "i1" {
		return unsupportedf("type-system", "testing assertion condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	okLabel := llvmNextLabel(emitter, "test.ok")
	failLabel := llvmNextLabel(emitter, "test.fail")
	emitter.body = append(emitter.body, mirBrCondText(cond.ref, okLabel, failLabel))
	emitter.body = append(emitter.body, mirLabelText(failLabel))
	g.takeOstyEmitter(emitter)
	g.currentBlock = failLabel
	msg, err := buildMessage()
	if err != nil {
		return err
	}
	emitter = g.toOstyEmitter()
	llvmPrintlnString(emitter, toOstyValue(msg))
	g.declareRuntimeSymbol("exit", "void", []paramInfo{{typ: "i32"}})
	emitter.body = append(emitter.body, mirCallExitOneText())
	emitter.body = append(emitter.body, mirUnreachableText())
	emitter.body = append(emitter.body, mirLabelText(okLabel))
	g.takeOstyEmitter(emitter)
	g.currentBlock = okLabel
	return nil
}

// buildAssertCompareMessage composes the failure message for
// assertEq/assertNe, quoting each argument's original source text and,
// when a runtime formatter is available (Int/Float/Bool/String),
// appending the computed value. Falls back to source-text-only when
// the source bytes were not wired through (no span capture) and to
// location-only when both text and values are missing.
func (g *generator) buildAssertCompareMessage(call *ast.CallExpr, name, leftText, rightText string, left value, isStringLeft bool, right value, isStringRight bool) (value, error) {
	base := g.testingFailureMessage(call, name)
	leftStr, leftHasVal := g.emitAssertArgToString(left, isStringLeft)
	rightStr, rightHasVal := g.emitAssertArgToString(right, isStringRight)
	if leftText == "" && rightText == "" && !leftHasVal && !rightHasVal {
		return g.foldAssertionMessage(staticAssertPart(base))
	}
	parts := []assertMsgPart{staticAssertPart(mirAssertExprLeftFragmentText(base, leftText))}
	if leftHasVal {
		parts = append(parts, staticAssertPart(" = "), dynamicAssertPart(leftStr))
	}
	parts = append(parts, staticAssertPart(mirAssertExprRightFragmentText(rightText)))
	if rightHasVal {
		parts = append(parts, staticAssertPart(" = "), dynamicAssertPart(rightStr))
	}
	// Structural diff: append a line-level diff for assertEq on types
	// that have a multi-line friendly string form — two Strings, or
	// two Lists of the same primitive element. assertNe failures only
	// happen on byte-equal inputs where a diff would be empty, so we
	// skip them. Structs, Maps, and List<struct> are deferred: they
	// need ToString protocol dispatch and per-shape format rules.
	if name == "assertEq" {
		leftDiff, rightDiff, ok, err := g.stringifyForStructuralDiff(left, isStringLeft, right, isStringRight)
		if err != nil {
			return value{}, err
		}
		if ok {
			diff, err := g.emitRuntimeStringDiff(leftDiff, rightDiff)
			if err != nil {
				return value{}, err
			}
			parts = append(parts, staticAssertPart("\ndiff (- left, + right):\n"), dynamicAssertPart(diff))
		}
	}
	return g.foldAssertionMessage(parts...)
}

// listPrimitiveKindID maps an LLVM element type to the elem_kind
// argument accepted by osty_rt_list_primitive_to_string. Returns 0
// when the element type is not a supported primitive — caller falls
// back to no-diff rendering. Delegates to the Osty-sourced
// `mirListPrimitiveKindID` (`toolchain/mir_generator.osty`).
func listPrimitiveKindID(elemTyp string, elemIsString bool) int {
	return mirListPrimitiveKindID(elemTyp, elemIsString)
}

// stringifyForStructuralDiff returns (leftStr, rightStr, true) when
// both operands of an assertEq share a "diffable" shape — today that
// means either both are Strings or both are Lists of the same
// primitive element kind (Int / Float / Bool / String). Anything else
// (mixed shapes, unsupported element types, struct-typed receivers)
// returns ok=false so the caller skips the diff section.
func (g *generator) stringifyForStructuralDiff(left value, isStringLeft bool, right value, isStringRight bool) (value, value, bool, error) {
	if isStringLeft && isStringRight {
		return left, right, true, nil
	}
	kindLeft := listPrimitiveKindID(left.listElemTyp, left.listElemString)
	kindRight := listPrimitiveKindID(right.listElemTyp, right.listElemString)
	if kindLeft == 0 || kindRight == 0 || kindLeft != kindRight {
		return value{}, value{}, false, nil
	}
	leftStr, err := g.emitRuntimeListPrimitiveToString(left, kindLeft)
	if err != nil {
		return value{}, value{}, false, err
	}
	rightStr, err := g.emitRuntimeListPrimitiveToString(right, kindRight)
	if err != nil {
		return value{}, value{}, false, err
	}
	return leftStr, rightStr, true, nil
}

// emitRuntimeListPrimitiveToString lowers a call to
// osty_rt_list_primitive_to_string, which formats a List<T> with
// primitive T as a multi-line String so the line-diff surfaces
// element-level divergences.
func (g *generator) emitRuntimeListPrimitiveToString(v value, kind int) (value, error) {
	if v.typ != "ptr" {
		return value{}, unsupportedf("type-system", "osty_rt_list_primitive_to_string expects a ptr (List<T>), got %s", v.typ)
	}
	g.declareRuntimeSymbol("osty_rt_list_primitive_to_string", "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	kindLit := llvmIntLiteral(kind)
	out := llvmCall(emitter, "ptr", "osty_rt_list_primitive_to_string", []*LlvmValue{toOstyValue(v), kindLit})
	g.takeOstyEmitter(emitter)
	outV := fromOstyValue(out)
	outV.gcManaged = true
	return outV, nil
}

// emitRuntimeStringDiff lowers a call to osty_rt_strings_DiffLines,
// which returns a managed String containing a trim-prefix/suffix line
// diff. An empty result means the two inputs are byte-equal — callers
// that only invoke this under a failing assertion can ignore that case.
func (g *generator) emitRuntimeStringDiff(left, right value) (value, error) {
	if left.typ != "ptr" || right.typ != "ptr" {
		return value{}, unsupportedf("type-system", "osty_rt_strings_DiffLines expects two String (ptr) values, got %s and %s", left.typ, right.typ)
	}
	g.declareRuntimeSymbol("osty_rt_strings_DiffLines", "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", "osty_rt_strings_DiffLines", []*LlvmValue{toOstyValue(left), toOstyValue(right)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	return v, nil
}

// buildAssertCondMessage composes the failure message for
// single-argument assertions (assertTrue/assertFalse/expectOk/
// expectError). The argument expression's source text is quoted after
// the location prefix. Runtime value capture is not emitted here:
// Bool values add no information beyond "failed", and Result payload
// stringification requires per-variant formatter dispatch that is
// tracked as a follow-up.
func (g *generator) buildAssertCondMessage(call *ast.CallExpr, name, exprText string) (value, error) {
	base := g.testingFailureMessage(call, name)
	if exprText == "" {
		return g.foldAssertionMessage(staticAssertPart(base))
	}
	label := "cond"
	if name == "expectOk" || name == "expectError" {
		label = "expr"
	}
	return g.foldAssertionMessage(staticAssertPart(mirAssertExprFragmentText(base, label, exprText)))
}

// assertMsgPart is a single fragment of a failure message: either a
// compile-time string literal or a runtime ptr-String value produced
// by llvmStringLiteral / osty_rt_*_to_string. foldAssertionMessage
// stitches a part sequence into one String via left-folded
// osty_rt_strings_Concat calls.
type assertMsgPart struct {
	static  string
	dynamic value
	isDyn   bool
}

func staticAssertPart(s string) assertMsgPart { return assertMsgPart{static: s} }
func dynamicAssertPart(v value) assertMsgPart { return assertMsgPart{dynamic: v, isDyn: true} }

// foldAssertionMessage materializes the fragment list into a single
// runtime String value. Adjacent static parts are coalesced first so
// we avoid pointless concat calls for purely-static messages.
func (g *generator) foldAssertionMessage(parts ...assertMsgPart) (value, error) {
	coalesced := coalesceAssertParts(parts)
	if len(coalesced) == 0 {
		emitter := g.toOstyEmitter()
		lit := llvmStringLiteral(emitter, "")
		g.takeOstyEmitter(emitter)
		return value{typ: "ptr", ref: lit.name, gcManaged: true}, nil
	}
	var acc value
	for i, p := range coalesced {
		piece := p.dynamic
		if !p.isDyn {
			emitter := g.toOstyEmitter()
			lit := llvmStringLiteral(emitter, p.static)
			g.takeOstyEmitter(emitter)
			piece = value{typ: "ptr", ref: lit.name, gcManaged: true}
		}
		if i == 0 {
			acc = piece
			continue
		}
		joined, err := g.emitRuntimeStringConcat(acc, piece)
		if err != nil {
			return value{}, err
		}
		acc = joined
	}
	return acc, nil
}

func coalesceAssertParts(parts []assertMsgPart) []assertMsgPart {
	var out []assertMsgPart
	for _, p := range parts {
		if p.isDyn {
			out = append(out, p)
			continue
		}
		if p.static == "" {
			continue
		}
		if n := len(out); n > 0 && !out[n-1].isDyn {
			out[n-1].static += p.static
			continue
		}
		out = append(out, p)
	}
	return out
}

// emitAssertArgToString converts a computed assertion argument into a
// display String value. Returns (value, true) when the type has a
// runtime formatter — Int (i64), Float (double), Bool (i1), or
// String (ptr, when the checker marked the expression as String). For
// every other type (structs, Lists, Maps, enums…) returns (zero,
// false) so the caller falls back to source-text-only rendering;
// those shapes need ToString protocol dispatch which is tracked
// separately.
func (g *generator) emitAssertArgToString(v value, isString bool) (value, bool) {
	switch v.typ {
	case "i64":
		out, err := g.emitRuntimeIntToString(v)
		if err != nil {
			return value{}, false
		}
		return out, true
	case "double":
		out, err := g.emitRuntimeFloatToString(v)
		if err != nil {
			return value{}, false
		}
		return out, true
	case "i1":
		out, err := g.emitRuntimeBoolToString(v)
		if err != nil {
			return value{}, false
		}
		return out, true
	case "i32":
		out, err := g.emitRuntimeCharToString(v)
		if err != nil {
			return value{}, false
		}
		return out, true
	case "i8":
		out, err := g.emitRuntimeByteToString(v)
		if err != nil {
			return value{}, false
		}
		return out, true
	case "ptr":
		if isString {
			return v, true
		}
	}
	return value{}, false
}

// sourceSpanText returns the original source text covered by expr's
// span. Returns empty when the generator was not handed the source
// bytes or when the recorded offsets fall outside the source (e.g.
// synthesized nodes from the IR bridge). Interior whitespace is
// collapsed so the quoted expression stays on the same line as the
// location prefix.
func (g *generator) sourceSpanText(expr ast.Expr) string {
	if expr == nil || len(g.source) == 0 {
		return ""
	}
	start := expr.Pos().Offset
	end := expr.End().Offset
	if start < 0 || end <= start || end > len(g.source) {
		return ""
	}
	return normalizeAssertExprText(string(g.source[start:end]))
}

// normalizeAssertExprText flattens newlines/tabs to spaces and collapses
// runs of whitespace so multi-line argument expressions (list literals,
// struct literals) do not break the single-line failure message layout.
func normalizeAssertExprText(text string) string {
	return mirNormalizeAssertExprText(text)
}

func (g *generator) emitIfStmt(expr *ast.IfExpr) error {
	if expr.IsIfLet {
		return g.emitIfLetStmt(expr)
	}
	if expr.Then == nil {
		return unsupported("control-flow", "if has no then block")
	}
	cond, err := g.emitExpr(expr.Cond)
	if err != nil {
		return err
	}
	if cond.typ != "i1" {
		return unsupportedf("type-system", "if condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfStart(emitter, toOstyValue(cond))
	g.takeOstyEmitter(emitter)
	g.enterBlock(labels.thenLabel)
	baseState := g.captureScopeState()
	if err := g.emitScopedStmtBlock(expr.Then.Stmts); err != nil {
		return err
	}
	thenReachable := g.currentReachable
	if thenReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(labels.elseLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(labels.elseLabel)
	if expr.Else != nil {
		if err := g.emitElse(expr.Else); err != nil {
			return err
		}
	}
	elseReachable := g.currentReachable
	if elseReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	if thenReachable || elseReachable {
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirLabelText(labels.endLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(labels.endLabel)
		return nil
	}
	g.leaveBlock()
	return nil
}

func (g *generator) emitIfLetStmt(expr *ast.IfExpr) error {
	if expr.Then == nil {
		return unsupported("control-flow", "if has no then block")
	}
	scrutinee, err := g.emitExpr(expr.Cond)
	if err != nil {
		return err
	}
	cond, bind, err := g.ifLetCondition(expr.Pattern, scrutinee)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.enterBlock(labels.thenLabel)
	baseState := g.captureScopeState()
	scopeDepth := len(g.locals)
	g.pushScope()
	if bind != nil {
		if err := bind(); err != nil {
			if len(g.locals) > scopeDepth {
				g.popScope()
			}
			return err
		}
	}
	if err := g.emitBlock(expr.Then.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	thenReachable := g.currentReachable
	if thenReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(labels.elseLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(labels.elseLabel)
	if expr.Else != nil {
		if err := g.emitElse(expr.Else); err != nil {
			return err
		}
	}
	elseReachable := g.currentReachable
	if elseReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	if thenReachable || elseReachable {
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirLabelText(labels.endLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(labels.endLabel)
		return nil
	}
	g.leaveBlock()
	return nil
}

func (g *generator) emitElse(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.Block:
		return g.emitScopedStmtBlock(e.Stmts)
	case *ast.IfExpr:
		return g.emitIfStmt(e)
	default:
		return unsupportedf("control-flow", "else expression %T", expr)
	}
}

// emitMatchStmt lowers a MatchExpr used in statement position (value is
// discarded). Scope is deliberately narrow: bare enum tag scrutinees (the
// `node.kind` shape the toolchain uses pervasively) with arms that are
// either bare enum variants or a trailing wildcard. Arm bodies run as
// statement blocks so they can contain void-returning calls or early
// returns; their values are never consumed.
//
// Payload match / Result match / guarded match in statement position
// still fall through to the unsupported path for now; they are rare in
// toolchain surface and can land as follow-on work.
func (g *generator) emitMatchStmt(expr *ast.MatchExpr) error {
	if expr == nil || len(expr.Arms) == 0 {
		return unsupported("statement", "empty match statement")
	}
	scrutinee, err := g.emitExpr(expr.Scrutinee)
	if err != nil {
		return err
	}
	if sourceType, ok := g.staticExprSourceType(expr.Scrutinee); ok {
		resolved, resolveErr := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
		if resolveErr == nil {
			if opt, ok := resolved.(*ast.OptionalType); ok && scrutinee.typ == "ptr" {
				return g.emitOptionalMatchStmt(scrutinee, opt.Inner, expr.Arms)
			}
			// `match result { Ok(v) -> …, Err(_) -> … }` as a
			// statement — either when the scrutinee is already the
			// Result aggregate value (emit inlined) or when it's a
			// ptr into a struct-field slot that needs loading first
			// (mirrors the expr-path fallback).
			if info, ok := builtinResultTypeFromAST(resolved, g.typeEnv()); ok {
				if scrutinee.typ == "ptr" {
					emitter := g.toOstyEmitter()
					loaded := llvmLoad(emitter, &LlvmValue{typ: info.typ, name: scrutinee.ref, pointer: true})
					g.takeOstyEmitter(emitter)
					scrutinee = value{typ: info.typ, ref: loaded.name}
				}
				if scrutinee.typ == info.typ {
					return g.emitResultMatchStmt(scrutinee, info, expr.Arms)
				}
			}
		}
	}
	if info, ok := g.resultTypes[scrutinee.typ]; ok {
		return g.emitResultMatchStmt(scrutinee, info, expr.Arms)
	}
	if isPrimitiveLiteralMatchScrutineeType(scrutinee.typ) && isPrimitiveLiteralMatchArms(expr.Arms) {
		return g.emitPrimitiveLiteralMatchStmt(scrutinee, expr.Arms)
	}
	if scrutinee.typ != "i64" {
		return unsupportedf("statement", "match statement scrutinee type %s (only tag-enum i64 supported as statement for now)", scrutinee.typ)
	}
	return g.emitTagEnumMatchStmt(scrutinee, expr.Arms)
}

// emitResultMatchStmt lowers a two-arm `match result { Ok(v) -> …,
// Err(e) -> … }` in statement position. Mirrors emitOptionalMatchStmt's
// basic-block shape: dispatch on the tag field, emit each arm's body
// through `emitMatchArmBodyAsStmt`, rejoin at `match.end` for the
// reachable predecessors. Payload bindings are extracted with the
// same field-index contract the expr path (emitResultMatchArm) uses.
func (g *generator) emitResultMatchStmt(scrutinee value, info builtinResultType, arms []*ast.MatchArm) error {
	if len(arms) != 2 {
		return unsupportedf("statement", "Result match requires exactly 2 arms, got %d", len(arms))
	}
	firstInfo, firstOk, err := g.matchResultPattern(info, arms[0].Pattern)
	if err != nil {
		return err
	}
	if !firstOk {
		return unsupported("statement", "Result match first arm must be Ok(...), Err(...), or _")
	}
	if firstInfo.isWildcard {
		baseState := g.captureScopeState()
		if err := g.emitMatchArmBodyAsStmt(arms[0].Body); err != nil {
			return err
		}
		g.restoreScopeState(baseState)
		return nil
	}
	secondInfo, secondOk, err := g.matchResultPattern(info, arms[1].Pattern)
	if err != nil {
		return err
	}
	if !secondOk {
		return unsupported("statement", "Result match second arm must be Ok(...), Err(...), or _")
	}

	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(scrutinee), "i64", 0)
	cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: fmt.Sprintf("%d", firstInfo.tag)}))
	thenLabel := llvmNextLabel(emitter, "match.result.first")
	elseLabel := llvmNextLabel(emitter, "match.result.second")
	endLabel := llvmNextLabel(emitter, "match.result.end")
	emitter.body = append(emitter.body, mirBrCondText(cond.name, thenLabel, elseLabel))
	emitter.body = append(emitter.body, mirLabelText(thenLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(thenLabel)

	if err := g.bindResultMatchPayload(scrutinee, firstInfo); err != nil {
		return err
	}
	baseState := g.captureScopeState()
	if err := g.emitMatchArmBodyAsStmt(arms[0].Body); err != nil {
		return err
	}
	if g.currentReachable {
		g.branchTo(endLabel)
	}
	g.restoreScopeState(baseState)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(elseLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(elseLabel)

	if err := g.bindResultMatchPayload(scrutinee, secondInfo); err != nil {
		return err
	}
	baseState = g.captureScopeState()
	if err := g.emitMatchArmBodyAsStmt(arms[1].Body); err != nil {
		return err
	}
	if g.currentReachable {
		g.branchTo(endLabel)
	}
	g.restoreScopeState(baseState)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	return nil
}

// bindResultMatchPayload extracts and binds the payload local for a
// Result match arm, matching the expr-side contract in
// emitResultMatchArm: non-wildcard arms with `hasBinding` pull the
// field at `info.fieldIndex` as `info.payloadType` and bind it under
// `info.payloadName`.
func (g *generator) bindResultMatchPayload(scrutinee value, info resultPatternInfo) error {
	if info.isWildcard || !info.hasBinding {
		return nil
	}
	emitter := g.toOstyEmitter()
	payload := llvmExtractValue(emitter, toOstyValue(scrutinee), info.payloadType, info.fieldIndex)
	g.takeOstyEmitter(emitter)
	payloadValue := fromOstyValue(payload)
	payloadValue.gcManaged = info.payloadType == "ptr"
	payloadValue.rootPaths = g.rootPathsForType(info.payloadType)
	if err := g.decorateValueFromSourceType(&payloadValue, builtinResultPayloadSourceType(scrutinee.sourceType, resultVariantName(info.tag))); err != nil {
		return err
	}
	g.bindNamedLocal(info.payloadName, payloadValue, false)
	return nil
}

type optionalMatchPatternInfo struct {
	isSome      bool
	isNone      bool
	isWildcard  bool
	payloadName string
}

func matchOptionalPattern(pattern ast.Pattern) (optionalMatchPatternInfo, bool, error) {
	switch p := pattern.(type) {
	case *ast.WildcardPat:
		return optionalMatchPatternInfo{isWildcard: true}, true, nil
	case *ast.IdentPat:
		switch p.Name {
		case "None":
			return optionalMatchPatternInfo{isNone: true}, true, nil
		case "Some":
			return optionalMatchPatternInfo{}, true, unsupported("statement", "optional Some arm must bind or wildcard its payload")
		default:
			return optionalMatchPatternInfo{}, false, nil
		}
	case *ast.VariantPat:
		if len(p.Path) == 0 || len(p.Path) > 2 {
			return optionalMatchPatternInfo{}, false, nil
		}
		name := p.Path[len(p.Path)-1]
		switch name {
		case "None":
			if len(p.Args) != 0 {
				return optionalMatchPatternInfo{}, true, unsupported("statement", "None arm cannot bind a payload")
			}
			return optionalMatchPatternInfo{isNone: true}, true, nil
		case "Some":
			if len(p.Args) != 1 {
				return optionalMatchPatternInfo{}, true, unsupported("statement", "Some arm must bind exactly one payload")
			}
			switch arg := p.Args[0].(type) {
			case *ast.IdentPat:
				return optionalMatchPatternInfo{isSome: true, payloadName: arg.Name}, true, nil
			case *ast.WildcardPat:
				return optionalMatchPatternInfo{isSome: true}, true, nil
			default:
				return optionalMatchPatternInfo{}, true, unsupportedf("statement", "optional Some payload pattern %T", arg)
			}
		default:
			return optionalMatchPatternInfo{}, false, nil
		}
	default:
		return optionalMatchPatternInfo{}, false, nil
	}
}

func (g *generator) bindOptionalMatchPayload(scrutinee value, innerSource ast.Type, pattern optionalMatchPatternInfo) error {
	if pattern.payloadName == "" {
		return nil
	}
	innerTyp, err := llvmType(innerSource, g.typeEnv())
	if err != nil {
		return err
	}
	payload := value{typ: innerTyp}
	if innerTyp == "ptr" {
		payload.ref = scrutinee.ref
	} else {
		emitter := g.toOstyEmitter()
		payload = g.loadValueFromAddress(emitter, innerTyp, scrutinee.ref)
		g.takeOstyEmitter(emitter)
	}
	payload.sourceType = innerSource
	if listElemTyp, listElemString, ok, err := llvmListElementInfo(innerSource, g.typeEnv()); err == nil && ok {
		payload.listElemTyp = listElemTyp
		payload.listElemString = listElemString
	}
	if mapKeyTyp, mapValueTyp, mapKeyString, ok, err := llvmMapTypes(innerSource, g.typeEnv()); err == nil && ok {
		payload.mapKeyTyp = mapKeyTyp
		payload.mapValueTyp = mapValueTyp
		payload.mapKeyString = mapKeyString
	}
	if setElemTyp, setElemString, ok, err := llvmSetElementType(innerSource, g.typeEnv()); err == nil && ok {
		payload.setElemTyp = setElemTyp
		payload.setElemString = setElemString
	}
	payload.gcManaged = valueNeedsManagedRoot(payload)
	payload.rootPaths = g.rootPathsForType(payload.typ)
	g.bindNamedLocal(pattern.payloadName, payload, false)
	return nil
}

func (g *generator) emitOptionalMatchStmt(scrutinee value, innerSource ast.Type, arms []*ast.MatchArm) error {
	emitter := g.toOstyEmitter()
	endLabel := llvmNextLabel(emitter, "match.end")
	isNil := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpEqPtrNullText(isNil, scrutinee.ref))
	g.takeOstyEmitter(emitter)

	anyReached := false
	for i, arm := range arms {
		if arm == nil {
			return unsupported("statement", "nil match arm")
		}
		pattern, ok, err := matchOptionalPattern(arm.Pattern)
		if err != nil {
			return err
		}
		if !ok {
			return unsupportedf("statement", "optional match arm must be Some/None/wildcard, got %T", arm.Pattern)
		}
		isLast := i == len(arms)-1
		if pattern.isWildcard {
			if !isLast {
				return unsupported("statement", "wildcard match arm must be last")
			}
			baseState := g.captureScopeState()
			if err := g.emitMatchArmGuard(arm, endLabel); err != nil {
				return err
			}
			if err := g.emitMatchArmBodyAsStmt(arm.Body); err != nil {
				return err
			}
			if g.currentReachable {
				g.branchTo(endLabel)
				anyReached = true
			}
			g.restoreScopeState(baseState)
			continue
		}

		emitter := g.toOstyEmitter()
		armLabel := llvmNextLabel(emitter, "match.arm")
		nextLabel := llvmNextLabel(emitter, "match.next")
		if pattern.isSome {
			emitter.body = append(emitter.body, mirBrCondText(isNil, nextLabel, armLabel))
		} else {
			emitter.body = append(emitter.body, mirBrCondText(isNil, armLabel, nextLabel))
		}
		emitter.body = append(emitter.body, mirLabelText(armLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(armLabel)

		baseState := g.captureScopeState()
		if pattern.isSome {
			if err := g.bindOptionalMatchPayload(scrutinee, innerSource, pattern); err != nil {
				return err
			}
		}
		if err := g.emitMatchArmGuard(arm, nextLabel); err != nil {
			return err
		}
		if err := g.emitMatchArmBodyAsStmt(arm.Body); err != nil {
			return err
		}
		if g.currentReachable {
			g.branchTo(endLabel)
			anyReached = true
		}
		g.restoreScopeState(baseState)

		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirLabelText(nextLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(nextLabel)
	}

	if g.currentReachable {
		g.branchTo(endLabel)
		anyReached = true
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	g.currentReachable = anyReached
	return nil
}

// emitMatchArmGuard evaluates an arm guard after a successful pattern match
// and branches to `failLabel` on false. The caller must have already bound
// any pattern variables the guard expression refers to. On success the
// generator is positioned inside a fresh `match.guardOk.N` block so the arm
// body emission proceeds normally. No-op when arm.Guard is nil.
func (g *generator) emitMatchArmGuard(arm *ast.MatchArm, failLabel string) error {
	if arm == nil || arm.Guard == nil {
		return nil
	}
	guard, err := g.emitExpr(arm.Guard)
	if err != nil {
		return err
	}
	if guard.typ != "i1" {
		return unsupportedf("type-system", "match guard type %s, want i1", guard.typ)
	}
	emitter := g.toOstyEmitter()
	guardOk := llvmNextLabel(emitter, "match.guardOk")
	emitter.body = append(emitter.body, mirBrCondText(guard.ref, guardOk, failLabel))
	emitter.body = append(emitter.body, mirLabelText(guardOk))
	g.takeOstyEmitter(emitter)
	g.enterBlock(guardOk)
	return nil
}

func (g *generator) emitTagEnumMatchStmt(scrutinee value, arms []*ast.MatchArm) error {
	emitter := g.toOstyEmitter()
	endLabel := llvmNextLabel(emitter, "match.end")
	g.takeOstyEmitter(emitter)

	anyReached := false
	for i, arm := range arms {
		if arm == nil {
			return unsupported("statement", "nil match arm")
		}
		_, isWildcard := arm.Pattern.(*ast.WildcardPat)
		isLast := i == len(arms)-1

		if isWildcard {
			if !isLast {
				return unsupported("statement", "wildcard match arm must be last")
			}
			baseState := g.captureScopeState()
			if err := g.emitMatchArmGuard(arm, endLabel); err != nil {
				return err
			}
			if err := g.emitMatchArmBodyAsStmt(arm.Body); err != nil {
				return err
			}
			if g.currentReachable {
				g.branchTo(endLabel)
				anyReached = true
			}
			g.restoreScopeState(baseState)
			continue
		}

		tag, ok, err := g.matchEnumTag(arm.Pattern)
		if err != nil {
			return err
		}
		if !ok {
			return unsupportedf("statement", "match statement arm must be a bare enum variant or wildcard, got %T", arm.Pattern)
		}

		emitter := g.toOstyEmitter()
		cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
		armLabel := llvmNextLabel(emitter, "match.arm")
		nextLabel := llvmNextLabel(emitter, "match.next")
		emitter.body = append(emitter.body, mirBrCondText(cond.name, armLabel, nextLabel))
		emitter.body = append(emitter.body, mirLabelText(armLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(armLabel)

		baseState := g.captureScopeState()
		if err := g.emitMatchArmGuard(arm, nextLabel); err != nil {
			return err
		}
		if err := g.emitMatchArmBodyAsStmt(arm.Body); err != nil {
			return err
		}
		if g.currentReachable {
			g.branchTo(endLabel)
			anyReached = true
		}
		g.restoreScopeState(baseState)

		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirLabelText(nextLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(nextLabel)
	}

	// Match with no wildcard leaves the last nextLabel active — treat it
	// as an implicit fall-through so inexhaustive tag-enum matches keep
	// statement flow. The value-position path enforces exhaustiveness;
	// as a statement the coverage check belongs to the checker, not the
	// backend.
	if g.currentReachable {
		g.branchTo(endLabel)
		anyReached = true
	}

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	g.currentReachable = anyReached
	return nil
}

func (g *generator) emitPrimitiveLiteralMatchStmt(scrutinee value, arms []*ast.MatchArm) error {
	emitter := g.toOstyEmitter()
	endLabel := llvmNextLabel(emitter, "match.end")
	g.takeOstyEmitter(emitter)

	anyReached := false
	for i, arm := range arms {
		if arm == nil {
			return unsupported("statement", "nil match arm")
		}
		_, isWildcard := arm.Pattern.(*ast.WildcardPat)
		isLast := i == len(arms)-1

		if isWildcard {
			if !isLast {
				return unsupported("statement", "wildcard match arm must be last")
			}
			baseState := g.captureScopeState()
			if err := g.emitMatchArmGuard(arm, endLabel); err != nil {
				return err
			}
			if err := g.emitMatchArmBodyAsStmt(arm.Body); err != nil {
				return err
			}
			if g.currentReachable {
				g.branchTo(endLabel)
				anyReached = true
			}
			g.restoreScopeState(baseState)
			continue
		}

		litPat, ok := arm.Pattern.(*ast.LiteralPat)
		if !ok || litPat.Literal == nil {
			return unsupportedf("statement", "primitive match arm must be a literal pattern or wildcard, got %T", arm.Pattern)
		}
		litValue, err := g.emitExpr(litPat.Literal)
		if err != nil {
			return err
		}
		if litValue.typ != scrutinee.typ {
			return unsupportedf("type-system", "match literal type %s does not match scrutinee %s", litValue.typ, scrutinee.typ)
		}

		emitter := g.toOstyEmitter()
		cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(litValue))
		armLabel := llvmNextLabel(emitter, "match.arm")
		nextLabel := llvmNextLabel(emitter, "match.next")
		emitter.body = append(emitter.body, mirBrCondText(cond.name, armLabel, nextLabel))
		emitter.body = append(emitter.body, mirLabelText(armLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(armLabel)

		baseState := g.captureScopeState()
		if err := g.emitMatchArmGuard(arm, nextLabel); err != nil {
			return err
		}
		if err := g.emitMatchArmBodyAsStmt(arm.Body); err != nil {
			return err
		}
		if g.currentReachable {
			g.branchTo(endLabel)
			anyReached = true
		}
		g.restoreScopeState(baseState)

		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirLabelText(nextLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(nextLabel)
	}

	if g.currentReachable {
		g.branchTo(endLabel)
		anyReached = true
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	g.currentReachable = anyReached
	return nil
}

func (g *generator) emitMatchArmBodyAsStmt(body ast.Expr) error {
	if body == nil {
		return nil
	}
	switch b := body.(type) {
	case *ast.Block:
		return g.emitScopedStmtBlock(b.Stmts)
	case *ast.IfExpr:
		return g.emitIfStmt(b)
	case *ast.MatchExpr:
		return g.emitMatchStmt(b)
	default:
		return g.emitExprStmt(body)
	}
}

func (g *generator) emitPrintln(call *ast.CallExpr) error {
	id, ok := call.Fn.(*ast.Ident)
	if !ok || id.Name != "println" {
		// Tag with the offending call's source position + Fn type so
		// merged-toolchain probes can grep the buffer immediately.
		// Without a category like FieldExpr/Ident the bare wall is
		// invisible in the histogram (see classifyLLVM015 → "other").
		return unsupportedf("call", "only println calls are supported (got %T %s)", call.Fn, exprPosLabel(call))
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return unsupported("call", "println requires one positional argument")
	}
	v, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	switch v.typ {
	case "i64":
		llvmPrintlnI64(emitter, toOstyValue(v))
	case "double":
		llvmPrintlnF64(emitter, toOstyValue(v))
	case "i1":
		llvmPrintlnBool(emitter, toOstyValue(v))
	case "ptr":
		llvmPrintlnString(emitter, toOstyValue(v))
	default:
		g.takeOstyEmitter(emitter)
		return unsupported("type-system", "println currently supports Int, Float, Bool, and plain String values only")
	}
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitListMethodCallStmt(call *ast.CallExpr) (bool, error) {
	field, elemTyp, _, found := g.listMethodInfo(call)
	if !found {
		return false, nil
	}
	if field.Name != "push" && field.Name != "pop" && field.Name != "insert" && field.Name != "clear" {
		return false, nil
	}
	g.pushScope()
	defer g.popScope()
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	g.takeOstyEmitter(emitter)
	base, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	if base.typ != "ptr" || elemTyp == "" {
		return true, unsupportedf("type-system", "list receiver type %s", base.typ)
	}
	elemSource, _ := g.iterableElemSourceType(field.X)
	base = g.protectManagedTemporary("list.base", base)
	baseValue, err := g.loadIfPointer(base)
	if err != nil {
		return true, err
	}
	if field.Name == "pop" {
		if len(call.Args) != 0 {
			return true, unsupported("call", "list.pop requires no arguments")
		}
		g.declareRuntimeSymbol(listRuntimePopDiscardSymbol(), "void", []paramInfo{{typ: "ptr"}})
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimePopDiscardSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(baseValue)}),
		))
		g.takeOstyEmitter(emitter)
		return true, nil
	}
	if field.Name == "clear" {
		if len(call.Args) != 0 {
			return true, unsupported("call", "list.clear requires no arguments")
		}
		g.declareRuntimeSymbol(listRuntimeClearSymbol(), "void", []paramInfo{{typ: "ptr"}})
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimeClearSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(baseValue)}),
		))
		g.takeOstyEmitter(emitter)
		return true, nil
	}
	if field.Name == "insert" {
		if len(call.Args) != 2 || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return true, unsupported("call", "list.insert requires two positional arguments (index, value)")
		}
		idx, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return true, err
		}
		if idx.typ != "i64" {
			return true, unsupportedf("type-system", "list.insert index type %s, want Int", idx.typ)
		}
		idxValue, err := g.loadIfPointer(idx)
		if err != nil {
			return true, err
		}
		arg, err := g.emitExprWithSourceType(call.Args[1].Value, elemSource)
		if err != nil {
			return true, err
		}
		if arg.typ != elemTyp {
			return true, unsupportedf("type-system", "list.insert value type %s, want %s", arg.typ, elemTyp)
		}
		argValue, err := g.loadIfPointer(arg)
		if err != nil {
			return true, err
		}
		if g.usesAggregateListABI(elemTyp) {
			return true, g.emitListAggregateInsert(baseValue, idxValue, argValue)
		}
		if !listUsesTypedRuntime(elemTyp) {
			return true, unsupportedf("call", "list.insert is currently supported on List<T> with typed-runtime or aggregate element ABI; got element type %s", elemTyp)
		}
		insertSymbol := listRuntimeInsertSymbol(elemTyp)
		g.declareRuntimeSymbol(insertSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: elemTyp}})
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			insertSymbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(baseValue), toOstyValue(idxValue), toOstyValue(argValue)}),
		))
		g.takeOstyEmitter(emitter)
		return true, nil
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return true, unsupported("call", "list.push requires one positional argument")
	}
	arg, err := g.emitExprWithSourceType(call.Args[0].Value, elemSource)
	if err != nil {
		return true, err
	}
	argValue, err := g.loadIfPointer(arg)
	if err != nil {
		return true, err
	}
	// Aggregate element types (`%Struct`, `%Tuple.*`) reach here as a
	// ptr pointing at the aggregate's slot — e.g. a stack-spilled
	// literal produced by `Bucket { ... }` or an `emitListAggregateGet`
	// out-parameter. Load through the ptr so the push path receives
	// the actual aggregate value that matches `elemTyp`. Scalar / ptr
	// elements already arrive at the right width.
	if argValue.typ != elemTyp && argValue.typ == "ptr" && strings.HasPrefix(elemTyp, "%") {
		emitter := g.toOstyEmitter()
		loaded := llvmLoad(emitter, &LlvmValue{typ: elemTyp, name: argValue.ref, pointer: true})
		g.takeOstyEmitter(emitter)
		argValue = fromOstyValue(loaded)
	}
	if argValue.typ != elemTyp {
		return true, unsupportedf("type-system", "list.push arg type %s, want %s", argValue.typ, elemTyp)
	}
	if g.usesAggregateListABI(elemTyp) {
		return true, g.emitListAggregatePush(baseValue, argValue)
	}
	pushSymbol := listRuntimePushSymbol(elemTyp)
	g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
	emitter = g.toOstyEmitter()
	if listUsesTypedRuntime(elemTyp) {
		g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			pushSymbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(baseValue), toOstyValue(argValue)}),
		))
	} else {
		traceSymbol := g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
		addr := g.spillValueAddress(emitter, "list.push", argValue)
		sizeValue := g.emitTypeSize(emitter, elemTyp)
		g.declareRuntimeSymbol(listRuntimePushBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimePushBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(baseValue), {typ: "ptr", name: addr}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
	}
	g.takeOstyEmitter(emitter)
	return true, nil
}

func (g *generator) emitMapMethodCallStmt(call *ast.CallExpr) (bool, error) {
	field, keyTyp, _, keyString, found := g.mapMethodInfo(call)
	if !found {
		return false, nil
	}
	if field.Name != "insert" && field.Name != "remove" && field.Name != "update" && field.Name != "retainIf" && field.Name != "clear" {
		return false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	if field.Name == "update" {
		return true, g.emitMapUpdateStmt(call, base, keyTyp, keyString)
	}
	if field.Name == "retainIf" {
		return true, g.emitMapRetainIfStmt(call, base, keyTyp, keyString)
	}
	if field.Name == "clear" {
		if len(call.Args) != 0 {
			return true, unsupported("call", "map.clear requires no arguments")
		}
		baseLoaded, err := g.loadIfPointer(base)
		if err != nil {
			return true, err
		}
		g.declareRuntimeSymbol(mapRuntimeClearSymbol(), "void", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			mapRuntimeClearSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(baseLoaded)}),
		))
		g.takeOstyEmitter(emitter)
		return true, nil
	}
	if field.Name == "insert" {
		if len(call.Args) != 2 || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return true, unsupported("call", "map.insert requires two positional arguments")
		}
		keySource, valSource, _ := g.iterableMapSourceTypes(field.X)
		key, err := g.emitExprWithSourceType(call.Args[0].Value, keySource)
		if err != nil {
			return true, err
		}
		val, err := g.emitExprWithSourceType(call.Args[1].Value, valSource)
		if err != nil {
			return true, err
		}
		if key.typ != keyTyp || val.typ != base.mapValueTyp {
			return true, unsupportedf("type-system", "map.insert types %s/%s, want %s/%s", key.typ, val.typ, keyTyp, base.mapValueTyp)
		}
		return true, g.emitMapInsert(base, key, val)
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return true, unsupported("call", "map.remove requires one positional argument")
	}
	key, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return true, err
	}
	if key.typ != keyTyp {
		return true, unsupportedf("type-system", "map.remove key type %s, want %s", key.typ, keyTyp)
	}
	loaded, err := g.loadIfPointer(key)
	if err != nil {
		return true, err
	}
	symbol := mapRuntimeRemoveSymbol(keyTyp, keyString)
	g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}})
	emitter := g.toOstyEmitter()
	llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(loaded)})
	g.takeOstyEmitter(emitter)
	return true, nil
}

// emitMapUpdateStmt lowers `m.update(key, f)` — stdlib bodied form is
//
//	let current = self.get(key)
//	self.insert(key, f(current))
//
// The three steps run inside a single `osty_rt_map_lock/unlock`
// critical section so concurrent mutators can't race between read and
// write. The per-map mutex is recursive (see osty_runtime.c), so the
// callback can re-enter the same map (e.g. read self.len()) without
// self-deadlock.
func (g *generator) emitMapUpdateStmt(call *ast.CallExpr, base value, keyTyp string, keyString bool) error {
	if len(call.Args) != 2 ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return unsupported("call", "map.update requires two positional arguments")
	}
	key, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	if key.typ != keyTyp {
		return unsupportedf("type-system", "map.update key type %s, want %s", key.typ, keyTyp)
	}
	loadedKey, err := g.loadIfPointer(key)
	if err != nil {
		return err
	}
	fnVal, err := g.emitExpr(call.Args[1].Value)
	if err != nil {
		return err
	}
	fnVal, sig, err := g.protectFnValueCallback("update.callback", fnVal, "map.update callback")
	if err != nil {
		return err
	}
	if len(sig.params) != 1 {
		return unsupportedf("call", "map.update callback arity must be 1 (got %d)", len(sig.params))
	}

	// Take the per-map lock so the get + callback + insert sequence is
	// atomic w.r.t. other mutators. Recursive so the user callback can
	// safely touch the same map.
	g.declareRuntimeSymbol(mapRuntimeLockSymbol(), "void", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(mapRuntimeUnlockSymbol(), "void", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		mapRuntimeLockSymbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(base)}),
	))
	g.takeOstyEmitter(emitter)

	// Materialise Option<V> for the current value.
	optVal, err := g.emitMapGetCore(base, loadedKey, keyTyp, keyString)
	if err != nil {
		return err
	}

	// f(env, opt) -> V via the indirect-call ABI.
	newVal, err := g.emitProtectedFnValueCall(fnVal, sig, []*LlvmValue{toOstyValue(optVal)})
	if err != nil {
		return err
	}
	if newVal.typ != base.mapValueTyp {
		return unsupportedf("type-system", "map.update callback return %s, want %s", newVal.typ, base.mapValueTyp)
	}

	// Insert under the same lock.
	if err := g.emitMapInsert(base, value{typ: keyTyp, ref: loadedKey.ref}, newVal); err != nil {
		return err
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		mapRuntimeUnlockSymbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(base)}),
	))
	g.takeOstyEmitter(emitter)
	return nil
}

// emitMapRetainIfStmt lowers `m.retainIf(pred)` on top of the
// snapshot-keys iterator (`emitMapIterate`) + victim-collect + second
// remove pass. Even under concurrent mutation of m, the snapshot
// approach guarantees we never trip an out-of-bounds during the walk:
// keys is a frozen List<K> taken once, and per-key get() is atomic
// under the per-map lock. Keys removed by a concurrent thread appear
// as None in get() and are silently skipped.
func (g *generator) emitMapRetainIfStmt(call *ast.CallExpr, base value, keyTyp string, keyString bool) error {
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return unsupported("call", "map.retainIf requires one positional argument")
	}
	predVal, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	predHeld, sig, err := g.protectFnValueCallback("retainif.pred", predVal, "map.retainIf callback")
	if err != nil {
		return err
	}
	if len(sig.params) != 2 || sig.ret != "i1" {
		return unsupportedf("call", "map.retainIf pred arity/ret mismatch (want fn(K,V)->Bool)")
	}
	valTyp := base.mapValueTyp

	baseVal := g.protectManagedTemporary("retainif.map", base)

	// victims = list_new()
	g.declareRuntimeSymbol(listRuntimeNewSymbol(), "ptr", nil)
	emitter := g.toOstyEmitter()
	victims := llvmCall(emitter, "ptr", listRuntimeNewSymbol(), nil)
	g.takeOstyEmitter(emitter)
	victimsVal := fromOstyValue(victims)
	victimsVal.gcManaged = true
	victimsVal.listElemTyp = keyTyp
	victimsVal.listElemString = keyString
	victimsVal = g.protectManagedTemporary("retainif.victims", victimsVal)

	// Pass 1: snapshot-iterate, collect victim keys where pred returns false.
	err = g.emitMapIterate(baseVal, keyTyp, valTyp, keyString, "retainif", func(k, v value) error {
		cond, err := g.emitProtectedFnValueCall(predHeld, sig, []*LlvmValue{
			{typ: keyTyp, name: k.ref},
			{typ: valTyp, name: v.ref},
		})
		if err != nil {
			return err
		}
		emitter := g.toOstyEmitter()
		notCond := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirXorI1NegText(notCond, cond.ref))
		labels := llvmIfExprStart(emitter, &LlvmValue{typ: "i1", name: notCond})
		g.takeOstyEmitter(emitter)
		g.currentBlock = labels.thenLabel

		victimsLoaded, err := g.loadIfPointer(victimsVal)
		if err != nil {
			return err
		}
		pushSym := listRuntimePushSymbol(keyTyp)
		g.declareRuntimeSymbol(pushSym, "void", []paramInfo{{typ: "ptr"}, {typ: keyTyp}})
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			pushSym,
			llvmCallArgs([]*LlvmValue{toOstyValue(victimsLoaded), {typ: keyTyp, name: k.ref}}),
		))
		llvmIfExprElse(emitter, labels)
		g.takeOstyEmitter(emitter)
		g.currentBlock = labels.elseLabel
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirBrUncondText(labels.endLabel))
		emitter.body = append(emitter.body, mirLabelText(labels.endLabel))
		g.takeOstyEmitter(emitter)
		g.currentBlock = labels.endLabel
		return nil
	})
	if err != nil {
		return err
	}

	// Pass 2: remove each victim key. Each remove is atomic under
	// the per-map lock; double-remove or remove-of-already-gone is a
	// no-op (osty_rt_map_remove_raw returns false and leaves the map
	// alone).
	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	victimsLoaded, err := g.loadIfPointer(victimsVal)
	if err != nil {
		return err
	}
	emitter = g.toOstyEmitter()
	vlen := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(victimsLoaded)})
	loop2 := llvmRangeStart(emitter, "retainif_j", llvmIntLiteral(0), vlen, false)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop2.bodyLabel)
	cont2 := g.nextNamedLabel("retainif.cont2")
	g.pushLoop(loopContext{continueLabel: cont2, breakLabel: loop2.endLabel, scopeDepth: len(g.locals)})

	victimsLoaded, err = g.loadIfPointer(victimsVal)
	if err != nil {
		g.popLoop()
		return err
	}
	mapLoaded, err := g.loadIfPointer(baseVal)
	if err != nil {
		g.popLoop()
		return err
	}
	getSym := listRuntimeGetSymbol(keyTyp)
	g.declareRuntimeSymbol(getSym, keyTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter = g.toOstyEmitter()
	vk := llvmCall(emitter, keyTyp, getSym, []*LlvmValue{toOstyValue(victimsLoaded), llvmI64(loop2.current)})
	removeSym := mapRuntimeRemoveSymbol(keyTyp, keyString)
	g.declareRuntimeSymbol(removeSym, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}})
	llvmCall(emitter, "i1", removeSym, []*LlvmValue{toOstyValue(mapLoaded), vk})
	g.takeOstyEmitter(emitter)

	g.popLoop()
	if g.currentReachable {
		g.branchTo(cont2)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(cont2))
	g.emitGCSafepointKind(emitter, safepointKindLoop)
	llvmRangeEnd(emitter, loop2)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop2.endLabel)

	return nil
}

func (g *generator) emitSetMethodCallStmt(call *ast.CallExpr) (bool, error) {
	field, elemTyp, elemString, found := g.setMethodInfo(call)
	if !found || field.Name != "insert" {
		return false, nil
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return true, unsupported("call", "set.insert requires one positional argument")
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	elemSource, _ := g.setElemSourceType(field.X)
	item, err := g.emitExprWithSourceType(call.Args[0].Value, elemSource)
	if err != nil {
		return true, err
	}
	if item.typ != elemTyp {
		return true, unsupportedf("type-system", "set.insert item type %s, want %s", item.typ, elemTyp)
	}
	loaded, err := g.loadIfPointer(item)
	if err != nil {
		return true, err
	}
	symbol := setRuntimeInsertSymbol(elemTyp, elemString)
	g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
	emitter := g.toOstyEmitter()
	llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(loaded)})
	g.takeOstyEmitter(emitter)
	return true, nil
}

// emitMapFor lowers `for (k, v) in m { body }` as an index-based walk:
//
//	%len = call i64 @osty_rt_map_len(%m)
//	for %i = 0; %i < %len; %i++:
//	  alloca %vslot (width by V), %k = call <K> @osty_rt_map_entry_at_<ksuf>(%m, %i, %vslot), load
//	  body(k, v)
//
// This is the infra piece that unlocks `retainIf`, `mergeWith`,
// `mapValues`, and any user-written map iteration. Matches the stdlib
// semantics of a stable-order walk (map preserves insertion order) and
// leaves in-iteration mutation undefined (users collect keys first
// the way `retainIf` does).
func (g *generator) emitMapFor(stmt *ast.ForStmt, kName, vName, keyTyp, valTyp string, keyString bool) error {
	g.pushScope()
	defer g.popScope()
	iterable, err := g.emitExpr(stmt.Iter)
	if err != nil {
		return err
	}
	if iterable.typ != "ptr" {
		return unsupportedf("type-system", "for-(k,v) iterable type %s", iterable.typ)
	}
	iterable = g.protectManagedTemporary("for.map", iterable)
	g.declareRuntimeSymbol(mapRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	iterableValue, err := g.loadIfPointer(iterable)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	lenValue := llvmCall(emitter, "i64", mapRuntimeLenSymbol(), []*LlvmValue{toOstyValue(iterableValue)})
	loop := llvmRangeStart(emitter, kName+"_idx", llvmIntLiteral(0), lenValue, false)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel("for.cont")
	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    loop.endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()

	iterableValue, err = g.loadIfPointer(iterable)
	if err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	indexValue := value{typ: "i64", ref: loop.current}
	keySource, valSource, _ := g.iterableMapSourceTypes(stmt.Iter)

	// Snapshot key and value under the same map lock.
	entryAtSym := mapRuntimeEntryAtSymbol(keyTyp, keyString)
	g.declareRuntimeSymbol(entryAtSym, keyTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
	emitter = g.toOstyEmitter()
	vslot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(vslot, valTyp))
	keyLoaded := llvmCall(emitter, keyTyp, entryAtSym, []*LlvmValue{
		toOstyValue(iterableValue),
		toOstyValue(indexValue),
		{typ: "ptr", name: vslot},
	})
	g.takeOstyEmitter(emitter)
	keyVal := fromOstyValue(keyLoaded)
	keyVal.gcManaged = keyTyp == "ptr"
	keyVal.rootPaths = g.rootPathsForType(keyTyp)
	keyVal.sourceType = keySource
	if err := g.decorateValueFromSourceType(&keyVal, keySource); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	g.bindLocal(kName, keyVal)
	emitter = g.toOstyEmitter()
	valLoaded := g.loadValueFromAddress(emitter, valTyp, vslot)
	g.takeOstyEmitter(emitter)
	valLoaded.gcManaged = valTyp == "ptr"
	valLoaded.rootPaths = g.rootPathsForType(valTyp)
	valLoaded.sourceType = valSource
	if err := g.decorateValueFromSourceType(&valLoaded, valSource); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	g.bindLocal(vName, valLoaded)

	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.emitGCSafepointKind(emitter, safepointKindLoop)
	llvmRangeEnd(emitter, loop)
	g.attachVectorizeMD(emitter, loop.condLabel)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func (g *generator) emitListFor(stmt *ast.ForStmt, iterName, elemTyp string) error {
	g.pushScope()
	defer g.popScope()
	iterable, err := g.emitExpr(stmt.Iter)
	if err != nil {
		return err
	}
	if iterable.typ != "ptr" || elemTyp == "" {
		return unsupportedf("type-system", "for-in iterable type %s", iterable.typ)
	}
	useAggregateABI := g.usesAggregateListABI(elemTyp)
	iterable = g.protectManagedTemporary("for.iter", iterable)
	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	iterableValue, err := g.loadIfPointer(iterable)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	loopSafepointSlot := g.allocLoopSafepointCounter(emitter)
	lenValue := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(iterableValue)})
	loop := llvmRangeStart(emitter, iterName+"_idx", llvmIntLiteral(0), lenValue, false)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel("for.cont")
	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    loop.endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	iterableValue, err = g.loadIfPointer(iterable)
	if err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	indexValue := value{typ: "i64", ref: loop.current}
	elemSource, _ := g.iterableElemSourceType(stmt.Iter)
	if useAggregateABI {
		item, err := g.emitListAggregateGet(iterableValue, indexValue, elemTyp)
		if err != nil {
			g.popScope()
			return err
		}
		item.sourceType = elemSource
		if err := g.decorateValueFromSourceType(&item, elemSource); err != nil {
			g.popScope()
			return err
		}
		g.bindLocal(iterName, item)
	} else if listUsesTypedRuntime(elemTyp) {
		getSymbol := listRuntimeGetSymbol(elemTyp)
		g.declareRuntimeSymbol(getSymbol, elemTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
		emitter = g.toOstyEmitter()
		item := llvmCall(emitter, elemTyp, getSymbol, []*LlvmValue{toOstyValue(iterableValue), llvmI64(loop.current)})
		g.takeOstyEmitter(emitter)
		loaded := fromOstyValue(item)
		loaded.gcManaged = elemTyp == "ptr"
		loaded.rootPaths = g.rootPathsForType(elemTyp)
		loaded.sourceType = elemSource
		if err := g.decorateValueFromSourceType(&loaded, elemSource); err != nil {
			g.popScope()
			return err
		}
		g.bindLocal(iterName, loaded)
	} else {
		traceSymbol := g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
		emitter = g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirAllocaText(slot, elemTyp))
		sizeValue := g.emitTypeSize(emitter, elemTyp)
		g.declareRuntimeSymbol(listRuntimeGetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimeGetBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(iterableValue), llvmI64(loop.current), {typ: "ptr", name: slot}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
		g.takeOstyEmitter(emitter)
		emitter = g.toOstyEmitter()
		loaded := g.loadValueFromAddress(emitter, elemTyp, slot)
		g.takeOstyEmitter(emitter)
		loaded.rootPaths = g.rootPathsForType(elemTyp)
		loaded.sourceType = elemSource
		if err := g.decorateValueFromSourceType(&loaded, elemSource); err != nil {
			g.popScope()
			return err
		}
		g.bindLocal(iterName, loaded)
	}
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.emitLoopSafepoint(emitter, loopSafepointSlot)
	llvmRangeEnd(emitter, loop)
	g.attachVectorizeMD(emitter, loop.condLabel)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

// emitSetFor lowers `for x in set` by snapshoting the Set<T> into a
// List<T> via `osty_rt_set_to_list` and then walking the list with
// the same typed-get helpers emitListFor uses. The snapshot matches
// Map iteration's weakly-consistent semantics: concurrent mutations
// during the loop don't trip out-of-bounds, and entries added after
// the snapshot won't be visited.
func (g *generator) emitSetFor(stmt *ast.ForStmt, iterName, elemTyp string, elemString bool) error {
	g.pushScope()
	defer g.popScope()
	setVal, err := g.emitExpr(stmt.Iter)
	if err != nil {
		return err
	}
	setVal = g.protectManagedTemporary("for.set", setVal)
	setLoaded, err := g.loadIfPointer(setVal)
	if err != nil {
		return err
	}
	g.declareRuntimeSymbol(setRuntimeToListSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	snapshot := llvmCall(emitter, "ptr", setRuntimeToListSymbol(), []*LlvmValue{toOstyValue(setLoaded)})
	g.takeOstyEmitter(emitter)
	snapshotV := fromOstyValue(snapshot)
	snapshotV.gcManaged = true
	snapshotV.listElemTyp = elemTyp
	snapshotV.listElemString = elemString
	snapshotV = g.protectManagedTemporary("for.set.list", snapshotV)

	useAggregateABI := g.usesAggregateListABI(elemTyp)
	emitter = g.toOstyEmitter()
	loopSafepointSlot := g.allocLoopSafepointCounter(emitter)
	lenValue := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(snapshotV)})
	loop := llvmRangeStart(emitter, iterName+"_idx", llvmIntLiteral(0), lenValue, false)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel("for.cont")
	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    loop.endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()

	indexValue := value{typ: "i64", ref: loop.current}
	elemSource, _ := g.iterableElemSourceType(stmt.Iter)
	if useAggregateABI {
		item, err := g.emitListAggregateGet(snapshotV, indexValue, elemTyp)
		if err != nil {
			g.popScope()
			g.popLoop()
			return err
		}
		item.sourceType = elemSource
		g.bindLocal(iterName, item)
	} else if listUsesTypedRuntime(elemTyp) {
		getSymbol := listRuntimeGetSymbol(elemTyp)
		g.declareRuntimeSymbol(getSymbol, elemTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
		emitter = g.toOstyEmitter()
		item := llvmCall(emitter, elemTyp, getSymbol, []*LlvmValue{toOstyValue(snapshotV), llvmI64(loop.current)})
		g.takeOstyEmitter(emitter)
		loaded := fromOstyValue(item)
		loaded.gcManaged = elemTyp == "ptr"
		loaded.rootPaths = g.rootPathsForType(elemTyp)
		loaded.sourceType = elemSource
		g.bindLocal(iterName, loaded)
	} else {
		traceSymbol := g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
		emitter = g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirAllocaText(slot, elemTyp))
		sizeValue := g.emitTypeSize(emitter, elemTyp)
		g.declareRuntimeSymbol(listRuntimeGetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimeGetBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(snapshotV), llvmI64(loop.current), {typ: "ptr", name: slot}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
		g.takeOstyEmitter(emitter)
		emitter = g.toOstyEmitter()
		loaded := g.loadValueFromAddress(emitter, elemTyp, slot)
		g.takeOstyEmitter(emitter)
		loaded.rootPaths = g.rootPathsForType(elemTyp)
		loaded.sourceType = elemSource
		g.bindLocal(iterName, loaded)
	}
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.emitLoopSafepoint(emitter, loopSafepointSlot)
	llvmRangeEnd(emitter, loop)
	g.attachVectorizeMD(emitter, loop.condLabel)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func (g *generator) emitOptionalUserCallStmt(call *ast.CallExpr) (bool, error) {
	sig, innerSource, found, err := g.optionalUserCallTarget(call)
	if !found || err != nil {
		return found, err
	}
	field := call.Fn.(*ast.FieldExpr)
	g.pushScope()
	defer g.popScope()
	base, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	base = g.protectManagedTemporary(sig.name+".optional.self", base)
	baseValue, err := g.loadIfPointer(base)
	if err != nil {
		return true, err
	}
	if err := g.emitOptionalPtrStmt(baseValue, func() error {
		if g.hasVisibleSafepointRoots() {
			emitter := g.toOstyEmitter()
			g.emitGCSafepointKind(emitter, safepointKindCall)
			g.takeOstyEmitter(emitter)
		}
		args, err := g.optionalUserCallArgs(sig, innerSource, baseValue, call)
		if err != nil {
			return err
		}
		emitter := g.toOstyEmitter()
		if sig.ret == "void" {
			emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(sig.irName, llvmCallArgs(args)))
		} else {
			llvmCall(emitter, sig.ret, sig.irName, args)
		}
		g.takeOstyEmitter(emitter)
		return nil
	}); err != nil {
		return true, err
	}
	return true, nil
}

func (g *generator) emitUserCallStmt(call *ast.CallExpr) (bool, error) {
	sig, receiverExpr, found, err := g.userCallTarget(call)
	if err != nil {
		return true, err
	}
	if !found {
		return false, nil
	}
	if g.hasVisibleSafepointRoots() {
		emitter := g.toOstyEmitter()
		g.emitGCSafepointKind(emitter, safepointKindCall)
		g.takeOstyEmitter(emitter)
	}
	g.pushScope()
	args, err := g.userCallArgs(sig, receiverExpr, call)
	if err != nil {
		g.popScope()
		return true, err
	}
	emitter := g.toOstyEmitter()
	if sig.ret == "void" {
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(sig.irName, llvmCallArgs(args)))
	} else {
		llvmCall(emitter, sig.ret, sig.irName, args)
	}
	g.takeOstyEmitter(emitter)
	g.popScope()
	return true, nil
}

func (g *generator) emitIndirectUserCallStmt(call *ast.CallExpr) (bool, error) {
	_, found, err := g.emitIndirectUserCall(call)
	return found, err
}
