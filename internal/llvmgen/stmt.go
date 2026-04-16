// stmt.go — statement-position emission: emitBlock, emitStmt dispatcher,
// let/assign/for/return/break/continue/expr-stmt, if-stmt / if-let-stmt,
// list·map·set method-call statements, user-call statements, println, and
// testing-only statement helpers (testing.assert / assertEq / fail / context
// / expectOk / expectError).
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

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

func (g *generator) emitReturningBlock(stmts []ast.Stmt, retType string, retSourceType ast.Type, retListElemTyp string, retMapKeyTyp string, retMapValueTyp string, retSetElemTyp string) error {
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
			v, err := g.emitExprWithHintAndSourceType(s.Value, retSourceType, retListElemTyp, false, retMapKeyTyp, retMapValueTyp, false, retSetElemTyp, false)
			if err != nil {
				return err
			}
			if v.typ != retType {
				return unsupportedf("type-system", "return type %s, want %s", v.typ, retType)
			}
			emitter := g.toOstyEmitter()
			g.releaseGCRoots(emitter)
			llvmReturn(emitter, toOstyValue(v))
			g.takeOstyEmitter(emitter)
			g.leaveBlock()
			return nil
		case *ast.ExprStmt:
			v, err := g.emitExprWithHintAndSourceType(s.X, retSourceType, retListElemTyp, false, retMapKeyTyp, retMapValueTyp, false, retSetElemTyp, false)
			if err != nil {
				return err
			}
			if v.typ != retType {
				return unsupportedf("type-system", "trailing expression type %s, want %s", v.typ, retType)
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
	for _, stmt := range stmts {
		if err := g.emitStmt(stmt); err != nil {
			return err
		}
		if !g.currentReachable {
			break
		}
	}
	return nil
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
	case *ast.ExprStmt:
		if ifExpr, ok := s.X.(*ast.IfExpr); ok {
			return g.emitIfStmt(ifExpr)
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
		if typ != v.typ {
			return unsupportedf("type-system", "let pattern type %s, value %s", typ, v.typ)
		}
	}
	return g.bindLetPattern(stmt.Pattern, v, stmt.Mut)
}

func (g *generator) emitAssign(stmt *ast.AssignStmt) error {
	if stmt.Op != token.ASSIGN {
		return unsupportedf("statement", "compound assignment %q", stmt.Op)
	}
	if len(stmt.Targets) != 1 {
		return unsupported("statement", "multi-target assignment")
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

func (g *generator) emitFieldAssign(target *ast.FieldExpr, rhs ast.Expr) error {
	if target == nil {
		return unsupported("statement", "nil field assignment target")
	}
	if target.IsOptional {
		return unsupported("statement", "optional field assignment is not supported")
	}
	baseIdent, ok := target.X.(*ast.Ident)
	if !ok {
		return unsupportedf("statement", "field assignment base %T", target.X)
	}
	slot, ok := g.lookupBinding(baseIdent.Name)
	if !ok {
		return unsupportedf("name", "assignment to unknown identifier %q", baseIdent.Name)
	}
	if !slot.ptr || !slot.mutable {
		return unsupportedf("statement", "assignment to immutable field %q.%s", baseIdent.Name, target.Name)
	}
	info := g.structsByType[slot.typ]
	if info == nil {
		return unsupportedf("type-system", "field assignment on %s", slot.typ)
	}
	field, ok := info.byName[target.Name]
	if !ok {
		return unsupportedf("expression", "struct %q has no field %q", info.name, target.Name)
	}
	v, err := g.emitExprWithHintAndSourceType(rhs, field.sourceType, field.listElemTyp, field.listElemString, field.mapKeyTyp, field.mapValueTyp, field.mapKeyString, field.setElemTyp, field.setElemString)
	if err != nil {
		return err
	}
	if v.typ != field.typ {
		return unsupportedf("type-system", "field assignment %q.%s type %s, value %s", baseIdent.Name, target.Name, field.typ, v.typ)
	}
	current, err := g.loadIfPointer(slot)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = insertvalue %s %s, %s %s, %d",
		tmp,
		current.typ,
		current.ref,
		v.typ,
		v.ref,
		field.index,
	))
	llvmStore(emitter, toOstyValue(slot), toOstyValue(value{typ: current.typ, ref: tmp}))
	g.takeOstyEmitter(emitter)
	return nil
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
	emitter := g.toOstyEmitter()
	if listUsesTypedRuntime(base.listElemTyp) {
		symbol := listRuntimeSetSymbol(base.listElemTyp)
		g.declareRuntimeSymbol(symbol, "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: base.listElemTyp}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			symbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(index), toOstyValue(loaded)}),
		))
	} else {
		traceSymbol := g.traceCallbackSymbol(base.listElemTyp, g.rootPathsForType(base.listElemTyp))
		addr := g.spillValueAddress(emitter, "list.set", loaded)
		sizeValue := g.emitTypeSize(emitter, base.listElemTyp)
		g.declareRuntimeSymbol(listRuntimeSetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			listRuntimeSetBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(index), {typ: "ptr", name: addr}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
	}
	g.takeOstyEmitter(emitter)
	return nil
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
	iterName, err := identPatternName(stmt.Pattern)
	if err != nil {
		return err
	}
	if iterInfo, ok := g.staticExprInfo(stmt.Iter); ok && iterInfo.typ == "ptr" && iterInfo.listElemTyp != "" {
		return g.emitListFor(stmt, iterName, iterInfo.listElemTyp)
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
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.emitGCSafepoint(emitter)
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func (g *generator) emitWhileFor(stmt *ast.ForStmt) error {
	emitter := g.toOstyEmitter()
	condLabel := llvmNextLabel(emitter, "for.cond")
	bodyLabel := llvmNextLabel(emitter, "for.body")
	continueLabel := llvmNextLabel(emitter, "for.cont")
	endLabel := llvmNextLabel(emitter, "for.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", condLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(condLabel)

	if stmt.Iter == nil {
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", bodyLabel))
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", bodyLabel))
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
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  br i1 %s, label %%%s, label %%%s",
			toOstyValue(cond).name,
			bodyLabel,
			endLabel,
		))
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", bodyLabel))
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
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(continueLabel)
	emitter = g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
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
	condLabel := llvmNextLabel(emitter, "for.cond")
	bodyLabel := llvmNextLabel(emitter, "for.body")
	continueLabel := llvmNextLabel(emitter, "for.cont")
	endLabel := llvmNextLabel(emitter, "for.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", condLabel))
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
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.name, bodyLabel, endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", bodyLabel))
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
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(continueLabel)
	emitter = g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
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
		ret, err = g.emitExprWithHintAndSourceType(stmt.Value, g.returnSourceType, g.returnListElemTyp, false, "", "", false, "", false)
		if err != nil {
			return err
		}
		if ret.typ != g.returnType {
			return unsupportedf("type-system", "return type %s, want %s", ret.typ, g.returnType)
		}
	}
	emitter := g.toOstyEmitter()
	g.releaseGCRoots(emitter)
	switch {
	case stmt.Value == nil && g.returnType == "":
		llvmReturnI32Zero(emitter)
	case stmt.Value == nil && g.returnType == "void":
		emitter.body = append(emitter.body, "  ret void")
	default:
		llvmReturn(emitter, toOstyValue(ret))
	}
	g.takeOstyEmitter(emitter)
	g.leaveBlock()
	return nil
}

func (g *generator) emitExprStmt(expr ast.Expr) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return unsupported("statement", "only println calls are supported as expression statements")
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
	if emitted, err := g.emitOptionalUserCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitUserCallStmt(call); emitted || err != nil {
		return err
	}
	return g.emitPrintln(call)
}

func (g *generator) emitTestingCallStmt(call *ast.CallExpr) (bool, error) {
	method, ok := g.testingCallMethod(call)
	if !ok {
		return false, nil
	}
	switch method {
	case "assert":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return true, unsupported("call", "testing.assert requires one positional argument")
		}
		cond, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return true, err
		}
		return true, g.emitTestingAssertion(cond, g.testingFailureMessage(call, "assert"))
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
	case "expectOk":
		_, err := g.emitTestingExpect(call, false)
		return true, err
	case "expectError":
		_, err := g.emitTestingExpect(call, true)
		return true, err
	default:
		return true, unsupportedf("call", "testing.%s is not supported by LLVM yet", method)
	}
}

func (g *generator) testingCallMethod(call *ast.CallExpr) (string, bool) {
	if call == nil {
		return "", false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
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
	cond, err := g.emitCompare(op, left, right)
	if err != nil {
		return err
	}
	return g.emitTestingAssertion(cond, g.testingFailureMessage(call, name))
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
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.name, okLabel, failLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", failLabel))
	g.emitTestingAbortWithEmitter(emitter, g.testingFailureMessage(call, method), okLabel)
	payload := llvmExtractValue(emitter, toOstyValue(result), payloadType, payloadIndex)
	g.takeOstyEmitter(emitter)
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

func (g *generator) emitTestingAssertion(cond value, message string) error {
	if cond.typ != "i1" {
		return unsupportedf("type-system", "testing assertion condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	okLabel := llvmNextLabel(emitter, "test.ok")
	failLabel := llvmNextLabel(emitter, "test.fail")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.ref, okLabel, failLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", failLabel))
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
	emitter.body = append(emitter.body, "  call void @exit(i32 1)")
	emitter.body = append(emitter.body, "  unreachable")
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", nextLabel))
}

func (g *generator) testingFailureMessage(call *ast.CallExpr, name string) string {
	source := g.sourcePath
	if source == "" {
		source = "<test>"
	} else if abs, err := filepath.Abs(source); err == nil {
		source = abs
	}
	line := 0
	if call != nil {
		line = call.Pos().Line
	}
	return fmt.Sprintf("testing.%s failed at %s:%d", name, source, line)
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
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.elseLabel))
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
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.endLabel))
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
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.elseLabel))
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
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.endLabel))
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

func (g *generator) emitPrintln(call *ast.CallExpr) error {
	id, ok := call.Fn.(*ast.Ident)
	if !ok || id.Name != "println" {
		return unsupported("call", "only println calls are supported")
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
	if field.Name != "push" {
		return false, nil
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return true, unsupported("call", "list.push requires one positional argument")
	}
	g.pushScope()
	defer g.popScope()
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	base, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	if base.typ != "ptr" || elemTyp == "" {
		return true, unsupportedf("type-system", "list receiver type %s", base.typ)
	}
	base = g.protectManagedTemporary("list.base", base)
	arg, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return true, err
	}
	if arg.typ != elemTyp {
		return true, unsupportedf("type-system", "list.push arg type %s, want %s", arg.typ, elemTyp)
	}
	baseValue, err := g.loadIfPointer(base)
	if err != nil {
		return true, err
	}
	argValue, err := g.loadIfPointer(arg)
	if err != nil {
		return true, err
	}
	if g.usesAggregateListABI(elemTyp) {
		return true, g.emitListAggregatePush(baseValue, argValue)
	}
	pushSymbol := listRuntimePushSymbol(elemTyp)
	g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
	emitter = g.toOstyEmitter()
	if listUsesTypedRuntime(elemTyp) {
		g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			pushSymbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(baseValue), toOstyValue(argValue)}),
		))
	} else {
		traceSymbol := g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
		addr := g.spillValueAddress(emitter, "list.push", argValue)
		sizeValue := g.emitTypeSize(emitter, elemTyp)
		g.declareRuntimeSymbol(listRuntimePushBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
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
	if field.Name != "insert" && field.Name != "remove" {
		return false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	if field.Name == "insert" {
		if len(call.Args) != 2 || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return true, unsupported("call", "map.insert requires two positional arguments")
		}
		key, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return true, err
		}
		val, err := g.emitExpr(call.Args[1].Value)
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
	item, err := g.emitExpr(call.Args[0].Value)
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
	if useAggregateABI {
		item, err := g.emitListAggregateGet(iterableValue, indexValue, elemTyp)
		if err != nil {
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
		g.bindLocal(iterName, loaded)
	} else {
		traceSymbol := g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
		emitter = g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", slot, elemTyp))
		sizeValue := g.emitTypeSize(emitter, elemTyp)
		g.declareRuntimeSymbol(listRuntimeGetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			listRuntimeGetBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(iterableValue), llvmI64(loop.current), {typ: "ptr", name: slot}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
		g.takeOstyEmitter(emitter)
		emitter = g.toOstyEmitter()
		loaded := g.loadValueFromAddress(emitter, elemTyp, slot)
		g.takeOstyEmitter(emitter)
		loaded.rootPaths = g.rootPathsForType(elemTyp)
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
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.emitGCSafepoint(emitter)
	llvmRangeEnd(emitter, loop)
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
		emitter := g.toOstyEmitter()
		g.emitGCSafepoint(emitter)
		g.takeOstyEmitter(emitter)
		args, err := g.optionalUserCallArgs(sig, innerSource, baseValue, call)
		if err != nil {
			return err
		}
		emitter = g.toOstyEmitter()
		if sig.ret == "void" {
			emitter.body = append(emitter.body, fmt.Sprintf("  call void @%s(%s)", sig.irName, llvmCallArgs(args)))
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
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.userCallArgs(sig, receiverExpr, call)
	if err != nil {
		g.popScope()
		return true, err
	}
	emitter = g.toOstyEmitter()
	if sig.ret == "void" {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void @%s(%s)", sig.irName, llvmCallArgs(args)))
	} else {
		llvmCall(emitter, sig.ret, sig.irName, args)
	}
	g.takeOstyEmitter(emitter)
	g.popScope()
	return true, nil
}
