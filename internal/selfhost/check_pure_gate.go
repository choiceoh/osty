package selfhost

import "fmt"

// runPureGate mirrors toolchain/check_gates.osty's E0775 gate inside the
// frozen Go seed, so generated checker execution owns #[pure] diagnostics
// instead of the host result adapter synthesizing them after the fact.
func runPureGate(cx *ElabCx) {
	if cx == nil || cx.ast == nil || cx.ast.arena == nil {
		return
	}
	arena := cx.ast.arena
	pureNames := collectPureFnNames(arena)
	if len(pureNames) == 0 {
		return
	}
	for _, declIdx := range arena.decls {
		node := astArenaNodeAt(arena, declIdx)
		if node == nil {
			continue
		}
		switch node.kind.(type) {
		case *AstNodeKind_AstNFnDecl:
			pureCheckFn(cx, arena, node, pureNames)
		case *AstNodeKind_AstNStructDecl, *AstNodeKind_AstNEnumDecl:
			pureCheckMethods(cx, arena, node, pureNames)
		}
	}
}

func collectPureFnNames(arena *AstArena) map[string]struct{} {
	out := map[string]struct{}{}
	if arena == nil {
		return out
	}
	for _, declIdx := range arena.decls {
		node := astArenaNodeAt(arena, declIdx)
		if node == nil {
			continue
		}
		switch node.kind.(type) {
		case *AstNodeKind_AstNFnDecl:
			if fnHasPureAnnotationAndBody(arena, node) {
				out[node.text] = struct{}{}
			}
		case *AstNodeKind_AstNStructDecl, *AstNodeKind_AstNEnumDecl:
			for _, memberIdx := range node.children {
				member := astArenaNodeAt(arena, memberIdx)
				if member == nil {
					continue
				}
				if _, ok := member.kind.(*AstNodeKind_AstNFnDecl); ok && fnHasPureAnnotationAndBody(arena, member) {
					out[member.text] = struct{}{}
				}
			}
		}
	}
	return out
}

func fnHasPureAnnotationAndBody(arena *AstArena, fn *AstNode) bool {
	return fn != nil && fn.right >= 0 && checkGateAnnotationContains(arena, fn.extra, "pure")
}

func pureCheckFn(cx *ElabCx, arena *AstArena, fn *AstNode, pureNames map[string]struct{}) {
	if !fnHasPureAnnotationAndBody(arena, fn) {
		return
	}
	locals := map[string]struct{}{}
	for _, paramIdx := range fn.children {
		param := astArenaNodeAt(arena, paramIdx)
		if param == nil {
			continue
		}
		if _, ok := param.kind.(*AstNodeKind_AstNParam); ok && param.text != "" && param.text != "self" {
			locals[param.text] = struct{}{}
		}
	}
	pureWalkExpr(cx, arena, fn.right, fn.text, pureNames, locals)
}

func pureCheckMethods(cx *ElabCx, arena *AstArena, parent *AstNode, pureNames map[string]struct{}) {
	if parent == nil {
		return
	}
	for _, memberIdx := range parent.children {
		member := astArenaNodeAt(arena, memberIdx)
		if member == nil {
			continue
		}
		if _, ok := member.kind.(*AstNodeKind_AstNFnDecl); ok {
			pureCheckFn(cx, arena, member, pureNames)
		}
	}
}

func pureEmit(cx *ElabCx, fnName, what, fixHint string, start, end int) {
	if cx == nil || cx.env == nil {
		return
	}
	notes := []string{"LANG_SPEC v0.6 A13: `#[pure]` lowers to LLVM `readnone`, so the body must not write non-local state, perform I/O, allocate, or call impure functions"}
	if fixHint != "" {
		notes = append(notes, "hint: "+fixHint)
	}
	cx.env.diagnostics = append(cx.env.diagnostics, checkDiagWithNotes(
		"E0775",
		fmt.Sprintf("`#[pure]` function `%s` cannot %s", fnName, what),
		start,
		end,
		notes,
	))
}

func pureWalkExpr(cx *ElabCx, arena *AstArena, idx int, fnName string, pureNames map[string]struct{}, locals map[string]struct{}) {
	if arena == nil || idx < 0 || idx >= len(arena.nodes) {
		return
	}
	node := arena.nodes[idx]
	if node == nil {
		return
	}
	switch node.kind.(type) {
	case *AstNodeKind_AstNList:
		pureEmit(cx, fnName, "allocate a list literal", "remove `#[pure]`, pass in precomputed data, or rewrite without managed allocation", node.start, node.end)
		for _, childIdx := range node.children {
			pureWalkExpr(cx, arena, childIdx, fnName, pureNames, locals)
		}
	case *AstNodeKind_AstNMap:
		pureEmit(cx, fnName, "allocate a map literal", "remove `#[pure]`, pass in precomputed data, or rewrite without managed allocation", node.start, node.end)
		for _, childIdx := range node.children {
			pureWalkExpr(cx, arena, childIdx, fnName, pureNames, locals)
		}
		for _, childIdx := range node.children2 {
			pureWalkExpr(cx, arena, childIdx, fnName, pureNames, locals)
		}
	case *AstNodeKind_AstNStructLit:
		pureEmit(cx, fnName, "allocate a struct literal", "return scalar data or drop `#[pure]` until the value construction can be proven allocation-free", node.start, node.end)
		for _, fieldIdx := range node.children {
			field := astArenaNodeAt(arena, fieldIdx)
			if field != nil {
				pureWalkExpr(cx, arena, field.left, fnName, pureNames, locals)
			}
		}
		pureWalkExpr(cx, arena, node.right, fnName, pureNames, locals)
	case *AstNodeKind_AstNClosure:
		pureEmit(cx, fnName, "allocate a closure", "closures capture an environment; use a direct `#[pure]` helper function instead", node.start, node.end)
	case *AstNodeKind_AstNStringLit:
		if isAllocatingStringText(node.text) {
			pureEmit(cx, fnName, classifyAllocatingString(node.text), "only plain `\"...\"` literals are accepted in `#[pure]` bodies", node.start, node.end)
		}
	case *AstNodeKind_AstNCall:
		if !pureCalleeAllowed(arena, node.left, pureNames) {
			what := "call a function that is not `#[pure]`"
			if pureCalleeLooksLikeIO(arena, node.left) {
				what = "perform I/O"
			}
			pureEmit(cx, fnName, what, "mark the callee `#[pure]` only if it is side-effect free, or remove `#[pure]` from this function", node.start, node.end)
		}
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
		for _, argIdx := range node.children {
			pureWalkExpr(cx, arena, argIdx, fnName, pureNames, locals)
		}
	case *AstNodeKind_AstNAssign:
		if !pureAssignTargetLocal(arena, node.left, locals) {
			pureEmit(cx, fnName, "write non-local state", "only assignment to local bindings declared inside the `#[pure]` function is allowed", node.start, node.end)
		}
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
		pureWalkExpr(cx, arena, node.right, fnName, pureNames, locals)
	case *AstNodeKind_AstNChanSend:
		pureEmit(cx, fnName, "send on a channel", "channel sends are observable side effects; remove `#[pure]`", node.start, node.end)
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
		pureWalkExpr(cx, arena, node.right, fnName, pureNames, locals)
	case *AstNodeKind_AstNDefer:
		pureEmit(cx, fnName, "register a deferred effect", "defer runs code after the function returns and is not allowed in `#[pure]` bodies", node.start, node.end)
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
	case *AstNodeKind_AstNLet:
		pureWalkExpr(cx, arena, node.right, fnName, pureNames, locals)
	case *AstNodeKind_AstNBlock:
		scoped := pureCopyLocals(locals)
		for _, stmtIdx := range node.children {
			pureWalkExpr(cx, arena, stmtIdx, fnName, pureNames, scoped)
			stmt := astArenaNodeAt(arena, stmtIdx)
			if stmt != nil {
				if _, ok := stmt.kind.(*AstNodeKind_AstNLet); ok {
					pureCollectPatternBindings(arena, stmt.left, scoped)
				}
			}
		}
	case *AstNodeKind_AstNExprStmt, *AstNodeKind_AstNReturn, *AstNodeKind_AstNQuestion, *AstNodeKind_AstNField, *AstNodeKind_AstNParen, *AstNodeKind_AstNTurbofish:
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
	case *AstNodeKind_AstNFor:
		loopLocals := pureCopyLocals(locals)
		pureCollectPatternBindings(arena, node.left, loopLocals)
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
		if len(node.children) >= 2 {
			pureWalkExpr(cx, arena, node.children[1], fnName, pureNames, locals)
		}
		pureWalkExpr(cx, arena, node.right, fnName, pureNames, loopLocals)
	case *AstNodeKind_AstNIf:
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
		pureWalkExpr(cx, arena, node.right, fnName, pureNames, locals)
		if len(node.children) > 0 {
			pureWalkExpr(cx, arena, node.children[0], fnName, pureNames, locals)
		}
	case *AstNodeKind_AstNMatch:
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
		for _, armIdx := range node.children {
			arm := astArenaNodeAt(arena, armIdx)
			if arm == nil {
				continue
			}
			armLocals := pureCopyLocals(locals)
			pureCollectPatternBindings(arena, arm.left, armLocals)
			if len(arm.children) > 0 {
				pureWalkExpr(cx, arena, arm.children[0], fnName, pureNames, armLocals)
			}
			pureWalkExpr(cx, arena, arm.right, fnName, pureNames, armLocals)
		}
	case *AstNodeKind_AstNUnary:
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
	case *AstNodeKind_AstNBinary, *AstNodeKind_AstNIndex, *AstNodeKind_AstNRange:
		pureWalkExpr(cx, arena, node.left, fnName, pureNames, locals)
		pureWalkExpr(cx, arena, node.right, fnName, pureNames, locals)
	case *AstNodeKind_AstNTuple:
		for _, childIdx := range node.children {
			pureWalkExpr(cx, arena, childIdx, fnName, pureNames, locals)
		}
	}
}

func pureAssignTargetLocal(arena *AstArena, targetIdx int, locals map[string]struct{}) bool {
	target := astArenaNodeAt(arena, targetIdx)
	if target == nil {
		return false
	}
	if _, ok := target.kind.(*AstNodeKind_AstNIdent); !ok {
		return false
	}
	_, ok := locals[target.text]
	return ok
}

func pureCalleeAllowed(arena *AstArena, calleeIdx int, pureNames map[string]struct{}) bool {
	callee := astArenaNodeAt(arena, calleeIdx)
	if callee == nil {
		return false
	}
	switch callee.kind.(type) {
	case *AstNodeKind_AstNIdent, *AstNodeKind_AstNField:
		_, ok := pureNames[callee.text]
		return ok
	case *AstNodeKind_AstNTurbofish:
		return pureCalleeAllowed(arena, callee.left, pureNames)
	default:
		return false
	}
}

func pureCalleeLooksLikeIO(arena *AstArena, calleeIdx int) bool {
	switch pureCalleeLastName(arena, calleeIdx) {
	case "print", "println", "eprint", "eprintln", "read", "readAll", "readToString", "write", "writeAll", "open", "create":
		return true
	default:
		return false
	}
}

func pureCalleeLastName(arena *AstArena, calleeIdx int) string {
	callee := astArenaNodeAt(arena, calleeIdx)
	if callee == nil {
		return ""
	}
	switch callee.kind.(type) {
	case *AstNodeKind_AstNIdent, *AstNodeKind_AstNField:
		return callee.text
	case *AstNodeKind_AstNTurbofish:
		return pureCalleeLastName(arena, callee.left)
	default:
		return ""
	}
}

func pureCollectPatternBindings(arena *AstArena, patIdx int, out map[string]struct{}) {
	pat := astArenaNodeAt(arena, patIdx)
	if pat == nil {
		return
	}
	if _, ok := pat.kind.(*AstNodeKind_AstNPattern); !ok {
		return
	}
	if pat.extra == astPatternIdentKind() || pat.extra == astPatternBindingKind() {
		if pat.text != "" && pat.text != "_" {
			out[pat.text] = struct{}{}
		}
	}
	pureCollectPatternBindings(arena, pat.left, out)
	pureCollectPatternBindings(arena, pat.right, out)
	for _, childIdx := range pat.children {
		pureCollectPatternBindings(arena, childIdx, out)
	}
}

func pureCopyLocals(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
