package selfhost

import "fmt"

// selfhostSemanticAstFile clones the raw parser arena and applies the
// parser-owned helper lowerings that the arena-native check/resolve path
// still needs for parity with internal/parser's stableLowerer.
func selfhostSemanticAstFile(file *AstFile) *AstFile {
	if file == nil || file.arena == nil {
		return file
	}
	cloned := &AstFile{arena: selfhostCloneAstArena(file.arena)}
	l := &selfhostStableLowerer{arena: cloned.arena}
	for i, idx := range cloned.arena.decls {
		cloned.arena.decls[i] = l.lowerDecl(idx)
	}
	return cloned
}

func selfhostCloneAstArena(src *AstArena) *AstArena {
	if src == nil {
		return nil
	}
	dst := &AstArena{
		nodes: make([]*AstNode, len(src.nodes)),
		decls: append([]int(nil), src.decls...),
		errors: func() []*AstParseError {
			out := make([]*AstParseError, len(src.errors))
			for i, err := range src.errors {
				if err == nil {
					continue
				}
				copy := *err
				out[i] = &copy
			}
			return out
		}(),
	}
	for i, node := range src.nodes {
		if node == nil {
			continue
		}
		copy := *node
		copy.children = append([]int(nil), node.children...)
		copy.children2 = append([]int(nil), node.children2...)
		dst.nodes[i] = &copy
	}
	return dst
}

type selfhostStableLowerer struct {
	arena   *AstArena
	counter int
}

func (l *selfhostStableLowerer) nextTemp(prefix string) string {
	name := fmt.Sprintf("_osty_%s%d", prefix, l.counter)
	l.counter++
	return name
}

func (l *selfhostStableLowerer) lowerDecl(idx int) int {
	n := l.node(idx)
	if n == nil {
		return idx
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNFnDecl:
		n.right = l.lowerBlock(n.right)
	case *AstNodeKind_AstNStructDecl, *AstNodeKind_AstNEnumDecl, *AstNodeKind_AstNInterfaceDecl:
		for _, child := range n.children {
			cn := l.node(child)
			if cn == nil {
				continue
			}
			if _, ok := cn.kind.(*AstNodeKind_AstNFnDecl); ok {
				cn.right = l.lowerBlock(cn.right)
			}
		}
	case *AstNodeKind_AstNLet:
		n.right = l.lowerExpr(n.right)
	}
	return idx
}

func (l *selfhostStableLowerer) lowerBlock(idx int) int {
	n := l.node(idx)
	if n == nil {
		return idx
	}
	n.children = l.lowerStmtList(n.children)
	return idx
}

func (l *selfhostStableLowerer) lowerStmtList(stmts []int) []int {
	if len(stmts) == 0 {
		return stmts
	}
	out := make([]int, 0, len(stmts))
	for _, idx := range stmts {
		out = append(out, l.lowerStmt(idx)...)
	}
	return out
}

func (l *selfhostStableLowerer) lowerStmt(idx int) []int {
	n := l.node(idx)
	if n == nil {
		return []int{idx}
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNBlock:
		n.children = l.lowerStmtList(n.children)
		return []int{idx}
	case *AstNodeKind_AstNLet:
		n.right = l.lowerExpr(n.right)
		if push, ok := l.lowerAppendLetStmt(n); ok {
			return []int{idx, push}
		}
		return []int{idx}
	case *AstNodeKind_AstNExprStmt:
		n.left = l.lowerExpr(n.left)
		if l.lowerAppendExprStmt(n) {
			return []int{idx}
		}
		return []int{idx}
	case *AstNodeKind_AstNAssign:
		n.left = l.lowerExpr(n.left)
		n.right = l.lowerExpr(n.right)
		if l.lowerAppendAssignStmt(n) {
			return []int{idx}
		}
		return []int{idx}
	case *AstNodeKind_AstNReturn, *AstNodeKind_AstNDefer:
		n.left = l.lowerExpr(n.left)
		return []int{idx}
	case *AstNodeKind_AstNFor:
		if len(n.children) > 1 {
			n.children[1] = l.lowerExpr(n.children[1])
		}
		n.right = l.lowerBlock(n.right)
		if temp, ok := l.lowerEnumerateForStmt(n); ok {
			return []int{temp, idx}
		}
		return []int{idx}
	case *AstNodeKind_AstNChanSend:
		n.left = l.lowerExpr(n.left)
		n.right = l.lowerExpr(n.right)
		return []int{idx}
	default:
		return []int{idx}
	}
}

func (l *selfhostStableLowerer) lowerExpr(idx int) int {
	n := l.node(idx)
	if n == nil {
		return idx
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNBlock:
		n.children = l.lowerStmtList(n.children)
		return idx
	case *AstNodeKind_AstNUnary, *AstNodeKind_AstNQuestion, *AstNodeKind_AstNParen:
		n.left = l.lowerExpr(n.left)
		return idx
	case *AstNodeKind_AstNBinary, *AstNodeKind_AstNIndex:
		n.left = l.lowerExpr(n.left)
		n.right = l.lowerExpr(n.right)
		return idx
	case *AstNodeKind_AstNCall:
		n.left = l.lowerExpr(n.left)
		for i, child := range n.children {
			n.children[i] = l.lowerExpr(child)
		}
		l.lowerBuiltinLenCall(n)
		return idx
	case *AstNodeKind_AstNField:
		n.left = l.lowerExpr(n.left)
		return idx
	case *AstNodeKind_AstNTurbofish:
		n.left = l.lowerExpr(n.left)
		return idx
	case *AstNodeKind_AstNRange:
		n.left = l.lowerExpr(n.left)
		n.right = l.lowerExpr(n.right)
		for i, child := range n.children {
			n.children[i] = l.lowerExpr(child)
		}
		return idx
	case *AstNodeKind_AstNTuple, *AstNodeKind_AstNList:
		for i, child := range n.children {
			n.children[i] = l.lowerExpr(child)
		}
		return idx
	case *AstNodeKind_AstNMap:
		for i, child := range n.children {
			n.children[i] = l.lowerExpr(child)
		}
		for i, child := range n.children2 {
			n.children2[i] = l.lowerExpr(child)
		}
		return idx
	case *AstNodeKind_AstNStructLit:
		n.left = l.lowerExpr(n.left)
		n.right = l.lowerExpr(n.right)
		for _, child := range n.children {
			cn := l.node(child)
			if cn != nil {
				cn.left = l.lowerExpr(cn.left)
			}
		}
		return idx
	case *AstNodeKind_AstNIf:
		n.left = l.lowerExpr(n.left)
		n.right = l.lowerBlock(n.right)
		if len(n.children) > 0 {
			n.children[0] = l.lowerExpr(n.children[0])
		}
		return idx
	case *AstNodeKind_AstNMatch:
		n.left = l.lowerExpr(n.left)
		for _, child := range n.children {
			arm := l.node(child)
			if arm == nil {
				continue
			}
			if len(arm.children) > 0 {
				arm.children[0] = l.lowerExpr(arm.children[0])
			}
			arm.right = l.lowerExpr(arm.right)
		}
		return idx
	case *AstNodeKind_AstNClosure:
		n.left = l.lowerExpr(n.left)
		return idx
	default:
		return idx
	}
}

func (l *selfhostStableLowerer) lowerBuiltinLenCall(call *AstNode) bool {
	if call == nil || !l.isCall(call) || len(call.children) != 1 {
		return false
	}
	if !l.isIdentNamed(call.left, "len") {
		return false
	}
	target := call.children[0]
	field := emptyAstNode(AstNodeKind(&AstNodeKind_AstNField{}))
	field.left = target
	field.text = "len"
	field.start = call.start
	field.end = call.end
	call.left = astArenaAdd(l.arena, field)
	call.children = call.children[:0]
	return true
}

func (l *selfhostStableLowerer) lowerAppendAssignStmt(assign *AstNode) bool {
	if assign == nil || !l.isAppendCall(assign.right) {
		return false
	}
	targetName, ok := l.identName(assign.left)
	if !ok {
		return false
	}
	base, item, ok := l.appendCallParts(assign.right)
	if !ok {
		return false
	}
	baseName, ok := l.identName(base)
	if !ok || baseName != targetName {
		return false
	}
	assign.kind = AstNodeKind(&AstNodeKind_AstNExprStmt{})
	assign.left = l.pushCallExpr(base, item, assign.start, assign.end)
	assign.right = -1
	assign.op = FrontTokenKind(&FrontTokenKind_FrontEOF{})
	return true
}

func (l *selfhostStableLowerer) lowerAppendExprStmt(stmt *AstNode) bool {
	if stmt == nil || !l.isAppendCall(stmt.left) {
		return false
	}
	base, item, ok := l.appendCallParts(stmt.left)
	if !ok {
		return false
	}
	if _, ok := l.identName(base); !ok {
		return false
	}
	stmt.left = l.pushCallExpr(base, item, stmt.start, stmt.end)
	return true
}

func (l *selfhostStableLowerer) lowerAppendLetStmt(stmt *AstNode) (int, bool) {
	if stmt == nil {
		return 0, false
	}
	name, ok := l.simpleIdentPatternName(stmt.left)
	if !ok || !l.isAppendCall(stmt.right) {
		return 0, false
	}
	base, item, ok := l.appendCallParts(stmt.right)
	if !ok {
		return 0, false
	}
	baseName, ok := l.identName(base)
	if !ok || baseName == name {
		return 0, false
	}
	stmt.flags = 1
	stmt.right = base
	return l.pushExprStmt(base, item, stmt.start, stmt.end), true
}

func (l *selfhostStableLowerer) lowerEnumerateForStmt(loop *AstNode) (int, bool) {
	if loop == nil || !l.isForIn(loop) {
		return 0, false
	}
	pattern, iterExpr, ok := l.enumerateLoopParts(loop)
	if !ok {
		return 0, false
	}
	indexPattern, valuePattern, ok := l.tuplePatternElems(pattern)
	if !ok {
		return 0, false
	}
	loopPattern, indexExpr, ok := l.enumerateIndexPattern(indexPattern, loop.start, loop.end)
	if !ok {
		return 0, false
	}

	tempName := l.nextTemp("enumerate")
	tempPat := l.identPattern(tempName, loop.start, loop.end)
	tempLet := emptyAstNode(AstNodeKind(&AstNodeKind_AstNLet{}))
	tempLet.left = tempPat
	tempLet.right = iterExpr
	tempLet.children = []int{-1}
	tempLet.start = loop.start
	tempLet.end = loop.end
	tempIdx := astArenaAdd(l.arena, tempLet)

	tempRef := l.identExpr(tempName, loop.start, loop.end)
	indexNode := emptyAstNode(AstNodeKind(&AstNodeKind_AstNIndex{}))
	indexNode.left = tempRef
	indexNode.right = indexExpr
	indexNode.start = loop.start
	indexNode.end = loop.end
	indexIdx := astArenaAdd(l.arena, indexNode)

	prelude := emptyAstNode(AstNodeKind(&AstNodeKind_AstNLet{}))
	prelude.left = valuePattern
	prelude.right = indexIdx
	prelude.children = []int{-1}
	prelude.start = loop.start
	prelude.end = loop.end
	preludeIdx := astArenaAdd(l.arena, prelude)

	body := l.node(loop.right)
	if body == nil {
		body = emptyAstNode(AstNodeKind(&AstNodeKind_AstNBlock{}))
		body.start = loop.start
		body.end = loop.end
		loop.right = astArenaAdd(l.arena, body)
	}
	body.children = append([]int{preludeIdx}, body.children...)

	rangeStart := emptyAstNode(AstNodeKind(&AstNodeKind_AstNIntLit{}))
	rangeStart.text = "0"
	rangeStart.start = loop.start
	rangeStart.end = loop.start
	rangeStartIdx := astArenaAdd(l.arena, rangeStart)

	tempLen := l.zeroArgMethodCall(tempName, "len", loop.start, loop.end)
	rangeNode := emptyAstNode(AstNodeKind(&AstNodeKind_AstNRange{}))
	rangeNode.left = rangeStartIdx
	rangeNode.right = tempLen
	rangeNode.start = loop.start
	rangeNode.end = loop.end
	rangeIdx := astArenaAdd(l.arena, rangeNode)

	loop.children = []int{loopPattern, rangeIdx}
	return tempIdx, true
}

func (l *selfhostStableLowerer) enumerateLoopParts(loop *AstNode) (pattern int, iterExpr int, ok bool) {
	if loop == nil || len(loop.children) < 2 {
		return 0, 0, false
	}
	pattern = loop.children[0]
	iter := loop.children[1]
	call := l.node(iter)
	if call == nil || !l.isCall(call) {
		return 0, 0, false
	}
	if l.isIdentNamed(call.left, "enumerate") && len(call.children) == 1 {
		return pattern, call.children[0], true
	}
	fn := l.node(call.left)
	if fn == nil || !l.isFieldNamed(fn, "enumerate") || len(call.children) != 0 {
		return 0, 0, false
	}
	return pattern, fn.left, true
}

func (l *selfhostStableLowerer) enumerateIndexPattern(idx int, start, end int) (pattern int, expr int, ok bool) {
	if l.isWildcardPattern(idx) {
		name := l.nextTemp("index")
		return l.identPattern(name, start, end), l.identExpr(name, start, end), true
	}
	if name, ok := l.simpleIdentPatternName(idx); ok {
		return idx, l.identExpr(name, start, end), true
	}
	return 0, 0, false
}

func (l *selfhostStableLowerer) appendCallParts(idx int) (base int, item int, ok bool) {
	call := l.node(idx)
	if call == nil || !l.isCall(call) || !l.isIdentNamed(call.left, "append") || len(call.children) != 2 {
		return 0, 0, false
	}
	return call.children[0], call.children[1], true
}

func (l *selfhostStableLowerer) tuplePatternElems(idx int) (first int, second int, ok bool) {
	n := l.node(idx)
	if n == nil {
		return 0, 0, false
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNPattern:
		if n.extra != astPatternTupleKind() || len(n.children) != 2 {
			return 0, 0, false
		}
		return n.children[0], n.children[1], true
	case *AstNodeKind_AstNTuple:
		if len(n.children) != 2 {
			return 0, 0, false
		}
		return n.children[0], n.children[1], true
	default:
		return 0, 0, false
	}
}

func (l *selfhostStableLowerer) pushExprStmt(base int, item int, start, end int) int {
	stmt := emptyAstNode(AstNodeKind(&AstNodeKind_AstNExprStmt{}))
	stmt.left = l.pushCallExpr(base, item, start, end)
	stmt.start = start
	stmt.end = end
	return astArenaAdd(l.arena, stmt)
}

func (l *selfhostStableLowerer) pushCallExpr(base int, item int, start, end int) int {
	name, ok := l.identName(base)
	if !ok {
		name = ""
	}
	field := emptyAstNode(AstNodeKind(&AstNodeKind_AstNField{}))
	field.left = l.identExpr(name, start, end)
	field.text = "push"
	field.start = start
	field.end = end
	fieldIdx := astArenaAdd(l.arena, field)

	call := emptyAstNode(AstNodeKind(&AstNodeKind_AstNCall{}))
	call.left = fieldIdx
	call.children = []int{item}
	call.start = start
	call.end = end
	return astArenaAdd(l.arena, call)
}

func (l *selfhostStableLowerer) zeroArgMethodCall(receiver, method string, start, end int) int {
	field := emptyAstNode(AstNodeKind(&AstNodeKind_AstNField{}))
	field.left = l.identExpr(receiver, start, end)
	field.text = method
	field.start = start
	field.end = end
	fieldIdx := astArenaAdd(l.arena, field)

	call := emptyAstNode(AstNodeKind(&AstNodeKind_AstNCall{}))
	call.left = fieldIdx
	call.start = start
	call.end = end
	return astArenaAdd(l.arena, call)
}

func (l *selfhostStableLowerer) identExpr(name string, start, end int) int {
	n := emptyAstNode(AstNodeKind(&AstNodeKind_AstNIdent{}))
	n.text = name
	n.start = start
	n.end = end
	return astArenaAdd(l.arena, n)
}

func (l *selfhostStableLowerer) identPattern(name string, start, end int) int {
	n := emptyAstNode(AstNodeKind(&AstNodeKind_AstNPattern{}))
	n.text = name
	n.extra = astPatternIdentKind()
	n.start = start
	n.end = end
	return astArenaAdd(l.arena, n)
}

func (l *selfhostStableLowerer) simpleIdentPatternName(idx int) (string, bool) {
	n := l.node(idx)
	if n == nil {
		return "", false
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNPattern:
		if n.extra == astPatternIdentKind() && n.text != "" && n.text != "_" {
			return n.text, true
		}
	case *AstNodeKind_AstNIdent:
		if n.text != "" && n.text != "_" {
			return n.text, true
		}
	}
	return "", false
}

func (l *selfhostStableLowerer) isWildcardPattern(idx int) bool {
	n := l.node(idx)
	if n == nil {
		return false
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNPattern:
		// opExprToForPattern currently preserves `_` as an ident-pattern when a
		// for-in binding starts from an expression tuple, so accept that shape
		// here for parity with the Go parser's enumerate lowering.
		return n.extra == astPatternWildcardKind() || n.text == "_" || n.text == "wildcard"
	case *AstNodeKind_AstNIdent:
		return n.text == "_"
	default:
		return false
	}
}

func (l *selfhostStableLowerer) identName(idx int) (string, bool) {
	n := l.node(idx)
	if n == nil {
		return "", false
	}
	if _, ok := n.kind.(*AstNodeKind_AstNIdent); !ok {
		return "", false
	}
	if n.text == "" {
		return "", false
	}
	return n.text, true
}

func (l *selfhostStableLowerer) isAppendCall(idx int) bool {
	_, _, ok := l.appendCallParts(idx)
	return ok
}

func (l *selfhostStableLowerer) isForIn(n *AstNode) bool {
	return n != nil && l.isFor(n) && n.text == "forin"
}

func (l *selfhostStableLowerer) isFor(n *AstNode) bool {
	if n == nil {
		return false
	}
	_, ok := n.kind.(*AstNodeKind_AstNFor)
	return ok
}

func (l *selfhostStableLowerer) isCall(n *AstNode) bool {
	if n == nil {
		return false
	}
	_, ok := n.kind.(*AstNodeKind_AstNCall)
	return ok
}

func (l *selfhostStableLowerer) isIdentNamed(idx int, name string) bool {
	got, ok := l.identName(idx)
	return ok && got == name
}

func (l *selfhostStableLowerer) isFieldNamed(n *AstNode, name string) bool {
	if n == nil {
		return false
	}
	if _, ok := n.kind.(*AstNodeKind_AstNField); !ok {
		return false
	}
	return n.text == name
}

func (l *selfhostStableLowerer) node(idx int) *AstNode {
	if l == nil || l.arena == nil || idx < 0 || idx >= len(l.arena.nodes) {
		return nil
	}
	return l.arena.nodes[idx]
}
