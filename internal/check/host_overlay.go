package check

import (
	"reflect"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/types"
)

type selfhostSpanKey struct {
	start int
	end   int
}

type selfhostNameSpanKey struct {
	selfhostSpanKey
	name string
}

type selfhostNameNodeKey struct {
	nodeID int
	name   string
}

type selfhostSpanIndex struct {
	exprs          map[selfhostSpanKey]ast.Expr
	exprsByFrom    map[int][]ast.Expr
	exprKeys       map[ast.Expr]selfhostSpanKey
	exprsByNode    map[int]ast.Expr
	calls          map[selfhostSpanKey]*ast.CallExpr
	callsByFrom    map[int][]*ast.CallExpr
	callKeys       map[*ast.CallExpr]selfhostSpanKey
	callsByNode    map[int]*ast.CallExpr
	scopes         map[selfhostSpanKey]*resolve.Scope
	scopesByNode   map[int]*resolve.Scope
	bindings       map[selfhostNameSpanKey]ast.Node
	bindingsByNode map[selfhostNameNodeKey]selfhostBindingNodeEntry
	symbols        map[selfhostNameSpanKey]*resolve.Symbol
	symbolsByNode  map[selfhostNameNodeKey]selfhostSymbolNodeEntry

	nativeNodeIDBaseBySourceBase map[int]int

	// scopeSpans is `scopes` sorted by span.start asc, materialised lazily on
	// the first scopeFor cache miss. Lets the slow path binary-search the
	// upper bound on candidates instead of scanning the full map every time.
	scopeSpans []scopeSpanEntry
	// scopeQueries memoises scopeFor results (including nil) so each unique
	// query span pays the slow walk at most once. Required because the merged
	// toolchain probe issues hundreds of thousands of queries against an
	// equally large scopes map; the previous full-map scan was effectively
	// O(N²) and timed the pipeline-clean test out at 5 minutes.
	scopeQueries map[selfhostSpanKey]*resolve.Scope

	// exprSpans is `exprKeys` sorted by exprKey.start asc, materialised
	// lazily on the first lookupExpr fallback. Same shape as scopeSpans
	// (sibling slow-path defence). exprQueries memoises results keyed by
	// (key, kind) so repeated misses don't re-walk.
	exprSpans   []exprSpanEntry
	exprQueries map[exprQueryKey]ast.Expr

	// callSpans / callQueries mirror exprSpans / exprQueries for
	// lookupCall — same O(N²) fallback used to dominate the merged
	// toolchain probe alongside scopeFor.
	callSpans   []callSpanEntry
	callQueries map[selfhostSpanKey]*ast.CallExpr
}

// scopeSpanEntry is one row of the sorted scope candidate list.
type scopeSpanEntry struct {
	span  selfhostSpanKey
	scope *resolve.Scope
}

type exprSpanEntry struct {
	span selfhostSpanKey
	expr ast.Expr
	kind string
}

type callSpanEntry struct {
	span selfhostSpanKey
	call *ast.CallExpr
}

type selfhostBindingNodeEntry struct {
	span selfhostSpanKey
	node ast.Node
}

type selfhostSymbolNodeEntry struct {
	span   selfhostSpanKey
	symbol *resolve.Symbol
}

type exprQueryKey struct {
	span selfhostSpanKey
	kind string
}

func (idx *selfhostSpanIndex) bindNode(key selfhostNameSpanKey, n ast.Node) {
	if idx.bindings[key] != nil {
		return
	}
	idx.bindings[key] = n
}

func (idx *selfhostSpanIndex) bindNodeByNativeID(anchor ast.Node, base int, key selfhostSpanKey, name string, n ast.Node) {
	if name == "" || n == nil {
		return
	}
	nodeID, ok := idx.nativeNodeIDForNode(anchor, base)
	if !ok {
		return
	}
	nodeKey := selfhostNameNodeKey{nodeID: nodeID, name: name}
	if idx.bindingsByNode[nodeKey].node != nil {
		return
	}
	idx.bindingsByNode[nodeKey] = selfhostBindingNodeEntry{span: key, node: n}
}

func overlaySelfhostResult(result *Result, src selfhostCheckedSource, checked api.CheckResult) {
	if result == nil {
		return
	}
	checked.EnsureStableIDs()
	idx := buildSelfhostSpanIndex(src)
	for _, node := range checked.TypedNodes {
		key := selfhostSpanKey{start: node.Start, end: node.End}
		var expr ast.Expr
		var scope *resolve.Scope
		if nodeID, ok := selfhostResultNodeID(node.NodeID, node.Node); ok {
			expr = idx.lookupExprByNodeID(nodeID, key, node.Kind)
			if expr != nil {
				scope = idx.scopesByNode[nodeID]
			}
		}
		if expr == nil {
			expr = idx.lookupExpr(key, node.Kind)
		}
		if expr == nil {
			continue
		}
		if scope == nil {
			scope = idx.scopeFor(key)
		}
		t := typeReprToType(node.Type, scope)
		if t == nil {
			continue
		}
		result.Types[expr] = t
	}
	for _, binding := range checked.Bindings {
		key := selfhostSpanKey{start: binding.Start, end: binding.End}
		var scope *resolve.Scope
		var directNode ast.Node
		var directSym *resolve.Symbol
		if nodeID, ok := selfhostResultNodeID(binding.NodeID, binding.Node); ok {
			directNode = idx.lookupBindingByNodeID(nodeID, key, binding.Name)
			directSym = idx.lookupSymbolByNodeID(nodeID, key, binding.Name)
			if directNode != nil || directSym != nil {
				scope = idx.scopesByNode[nodeID]
			}
		}
		if scope == nil {
			scope = idx.scopeFor(key)
		}
		t := typeReprToType(binding.Type, scope)
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: binding.Name}
		if n := directNode; n != nil {
			result.LetTypes[n] = t
		} else if n := idx.bindings[nameKey]; n != nil {
			result.LetTypes[n] = t
		}
		if sym := directSym; sym != nil {
			result.SymTypes[sym] = t
		} else if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, symbol := range checked.Symbols {
		key := selfhostSpanKey{start: symbol.Start, end: symbol.End}
		var scope *resolve.Scope
		var directSym *resolve.Symbol
		if nodeID, ok := selfhostResultNodeID(symbol.NodeID, symbol.Node); ok {
			directSym = idx.lookupSymbolByNodeID(nodeID, key, symbol.Name)
			if directSym != nil {
				scope = idx.scopesByNode[nodeID]
			}
		}
		if scope == nil {
			scope = idx.scopeFor(key)
		}
		t := typeReprToType(symbol.Type, scope)
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: symbol.Name}
		if sym := directSym; sym != nil {
			result.SymTypes[sym] = t
		} else if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, inst := range checked.Instantiations {
		key := selfhostSpanKey{start: inst.Start, end: inst.End}
		var call *ast.CallExpr
		var scope *resolve.Scope
		if nodeID, ok := selfhostResultNodeID(inst.NodeID, inst.Node); ok {
			call = idx.lookupCallByNodeID(nodeID, key)
			if call != nil {
				scope = idx.scopesByNode[nodeID]
			}
		}
		if call == nil {
			call = idx.lookupCall(key)
		}
		if call == nil || len(inst.TypeArgs) == 0 {
			continue
		}
		if scope == nil {
			scope = idx.scopeFor(key)
		}
		args := make([]types.Type, 0, len(inst.TypeArgs))
		for i := range inst.TypeArgs {
			if t := typeReprToType(&inst.TypeArgs[i], scope); t != nil {
				args = append(args, t)
			}
		}
		if len(args) == len(inst.TypeArgs) && call.ID != 0 {
			if _, existed := result.InstantiationsByID[call.ID]; !existed {
				result.InstantiationCalls = append(result.InstantiationCalls, call)
			}
			result.InstantiationsByID[call.ID] = args
		}
	}
}

func buildSelfhostSpanIndex(src selfhostCheckedSource) *selfhostSpanIndex {
	idx := &selfhostSpanIndex{
		exprs:                        map[selfhostSpanKey]ast.Expr{},
		exprsByFrom:                  map[int][]ast.Expr{},
		exprKeys:                     map[ast.Expr]selfhostSpanKey{},
		exprsByNode:                  map[int]ast.Expr{},
		calls:                        map[selfhostSpanKey]*ast.CallExpr{},
		callsByFrom:                  map[int][]*ast.CallExpr{},
		callKeys:                     map[*ast.CallExpr]selfhostSpanKey{},
		callsByNode:                  map[int]*ast.CallExpr{},
		scopes:                       map[selfhostSpanKey]*resolve.Scope{},
		scopesByNode:                 map[int]*resolve.Scope{},
		bindings:                     map[selfhostNameSpanKey]ast.Node{},
		bindingsByNode:               map[selfhostNameNodeKey]selfhostBindingNodeEntry{},
		symbols:                      map[selfhostNameSpanKey]*resolve.Symbol{},
		symbolsByNode:                map[selfhostNameNodeKey]selfhostSymbolNodeEntry{},
		nativeNodeIDBaseBySourceBase: map[int]int{},
	}
	for _, file := range src.files {
		if file.file == nil {
			continue
		}
		if file.nativeNodeIDs {
			idx.nativeNodeIDBaseBySourceBase[file.base] = file.nativeNodeIDBase
		}
		for _, decl := range file.file.Decls {
			idx.addNode(decl, file.base, file.scope, file.sourceMap)
		}
		for _, stmt := range file.file.Stmts {
			idx.addNode(stmt, file.base, file.scope, file.sourceMap)
		}
		idx.addScopeSubtree(file.scope, file.base, file.sourceMap)
		for _, sym := range file.refs {
			idx.addSymbol(sym, file.base, file.scope, file.sourceMap)
		}
	}
	return idx
}

func selfhostResultNodeID(nodeID, legacyNode int) (int, bool) {
	if nodeID != 0 {
		return nodeID, true
	}
	if legacyNode != 0 {
		return legacyNode, true
	}
	// Arena node 0 is valid. Direct lookups still validate span/kind/name
	// before accepting it, so trying 0 is safer than silently forcing the
	// oldest record in each native result back through span rematching.
	return 0, true
}

func (idx *selfhostSpanIndex) scopeFor(key selfhostSpanKey) *resolve.Scope {
	if scope := idx.scopes[key]; scope != nil {
		return scope
	}
	if scope, ok := idx.scopeQueries[key]; ok {
		return scope
	}
	idx.ensureScopeSpans()

	// Binary search for the first index whose span.start exceeds key.start —
	// every candidate enclosing `key` must satisfy span.start ≤ key.start, so
	// only entries in [0, lo) need to be considered.
	list := idx.scopeSpans
	lo, hi := 0, len(list)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if list[mid].span.start <= key.start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	var best *resolve.Scope
	bestSize := int(^uint(0) >> 1)
	for i := lo - 1; i >= 0; i-- {
		e := list[i]
		if best != nil && key.end-e.span.start >= bestSize {
			break
		}
		if e.span.end < key.end {
			continue
		}
		size := e.span.end - e.span.start
		if size < bestSize {
			best = e.scope
			bestSize = size
		}
	}
	if idx.scopeQueries == nil {
		idx.scopeQueries = map[selfhostSpanKey]*resolve.Scope{}
	}
	idx.scopeQueries[key] = best
	return best
}

func (idx *selfhostSpanIndex) ensureScopeSpans() {
	if idx.scopeSpans != nil {
		return
	}
	list := make([]scopeSpanEntry, 0, len(idx.scopes))
	for span, scope := range idx.scopes {
		if scope == nil {
			continue
		}
		list = append(list, scopeSpanEntry{span: span, scope: scope})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].span.start < list[j].span.start
	})
	if list == nil {
		list = []scopeSpanEntry{}
	}
	idx.scopeSpans = list
}

func (idx *selfhostSpanIndex) addNode(n ast.Node, base int, scope *resolve.Scope, sm *sourcemap.Map) {
	if n == nil {
		return
	}
	// Nilable AST fields (e.g. FnDecl.Body for interface methods without a
	// default) arrive here as a non-nil ast.Node interface wrapping a nil
	// pointer. The `n == nil` guard above misses that; the type switch below
	// would then dereference the nil pointer.
	if rv := reflect.ValueOf(n); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return
	}
	key, haveKey := spanKeyForNode(n, base, sm)
	if haveKey {
		if _, ok := idx.scopes[key]; !ok {
			idx.scopes[key] = scope
		}
		if nodeID, ok := idx.nativeNodeIDForNode(n, base); ok {
			if _, have := idx.scopesByNode[nodeID]; !have {
				idx.scopesByNode[nodeID] = scope
			}
		}
		idx.addDeclaredSymbol(n, key, scope, base)
		if e, ok := n.(ast.Expr); ok {
			if _, have := idx.exprs[key]; !have {
				idx.exprs[key] = e
			}
			idx.exprsByFrom[key.start] = append(idx.exprsByFrom[key.start], e)
			idx.exprKeys[e] = key
			if nodeID, ok := idx.nativeNodeIDForNode(n, base); ok {
				idx.exprsByNode[nodeID] = e
			}
			if c, ok := e.(*ast.CallExpr); ok {
				idx.calls[key] = c
				idx.callsByFrom[key.start] = append(idx.callsByFrom[key.start], c)
				idx.callKeys[c] = key
				if nodeID, ok := idx.nativeNodeIDForNode(n, base); ok {
					idx.callsByNode[nodeID] = c
				}
			}
		}
	}
	switch v := n.(type) {
	case *ast.FnDecl:
		idx.addNode(v.Recv, base, scope, sm)
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.StructDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.EnumDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, variant := range v.Variants {
			idx.addNode(variant, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.InterfaceDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, sup := range v.Extends {
			idx.addNode(sup, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.TypeAliasDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		idx.addNode(v.Target, base, scope, sm)
	case *ast.LetDecl:
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.UseDecl:
		for _, d := range v.GoBody {
			idx.addNode(d, base, scope, sm)
		}
	case *ast.Field:
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Default, base, scope, sm)
	case *ast.Variant:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
	case *ast.Param:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
			idx.bindNodeByNativeID(v, base, key, v.Name, v)
		}
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Default, base, scope, sm)
	case *ast.GenericParam:
		for _, con := range v.Constraints {
			idx.addNode(con, base, scope, sm)
		}
	case *ast.NamedType:
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.OptionalType:
		idx.addNode(v.Inner, base, scope, sm)
	case *ast.TupleType:
		for _, elem := range v.Elems {
			idx.addNode(elem, base, scope, sm)
		}
	case *ast.FnType:
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
	case *ast.Block:
		for _, s := range v.Stmts {
			idx.addNode(s, base, scope, sm)
		}
	case *ast.LetStmt:
		if name := bindingPatternName(v.Pattern); name != "" {
			if patKey, ok := spanKeyForNode(v.Pattern, base, sm); ok {
				idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: patKey, name: name}, v)
				idx.bindNodeByNativeID(v.Pattern, base, patKey, name, v)
			}
		}
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ExprStmt:
		idx.addNode(v.X, base, scope, sm)
	case *ast.AssignStmt:
		for _, t := range v.Targets {
			idx.addNode(t, base, scope, sm)
		}
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ReturnStmt:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ChanSendStmt:
		idx.addNode(v.Channel, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.DeferStmt:
		idx.addNode(v.X, base, scope, sm)
	case *ast.ForStmt:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Iter, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.UnaryExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.BinaryExpr:
		idx.addNode(v.Left, base, scope, sm)
		idx.addNode(v.Right, base, scope, sm)
	case *ast.QuestionExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.CallExpr:
		idx.addNode(v.Fn, base, scope, sm)
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.Arg:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.FieldExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.IndexExpr:
		idx.addNode(v.X, base, scope, sm)
		idx.addNode(v.Index, base, scope, sm)
	case *ast.TurbofishExpr:
		idx.addNode(v.Base, base, scope, sm)
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.RangeExpr:
		idx.addNode(v.Start, base, scope, sm)
		idx.addNode(v.Stop, base, scope, sm)
	case *ast.ParenExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.TupleExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.ListExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.MapExpr:
		for _, e := range v.Entries {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.MapEntry:
		idx.addNode(v.Key, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.StructLit:
		idx.addNode(v.Type, base, scope, sm)
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
		idx.addNode(v.Spread, base, scope, sm)
	case *ast.StructLitField:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.IfExpr:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Cond, base, scope, sm)
		idx.addNode(v.Then, base, scope, sm)
		idx.addNode(v.Else, base, scope, sm)
	case *ast.MatchExpr:
		idx.addNode(v.Scrutinee, base, scope, sm)
		for _, a := range v.Arms {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.MatchArm:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Guard, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.ClosureExpr:
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.LiteralPat:
		idx.addNode(v.Literal, base, scope, sm)
	case *ast.IdentPat:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
			idx.bindNodeByNativeID(v, base, key, v.Name, v)
		}
	case *ast.TuplePat:
		for _, p := range v.Elems {
			idx.addNode(p, base, scope, sm)
		}
	case *ast.StructPat:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
	case *ast.StructPatField:
		idx.addNode(v.Pattern, base, scope, sm)
	case *ast.VariantPat:
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.RangePat:
		idx.addNode(v.Start, base, scope, sm)
		idx.addNode(v.Stop, base, scope, sm)
	case *ast.OrPat:
		for _, a := range v.Alts {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.BindingPat:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
			idx.bindNodeByNativeID(v, base, key, v.Name, v)
		}
		idx.addNode(v.Pattern, base, scope, sm)
	}
}

func (idx *selfhostSpanIndex) addScopeSubtree(scope *resolve.Scope, base int, sm *sourcemap.Map) {
	if scope == nil {
		return
	}
	for _, sym := range scope.Symbols() {
		idx.addSymbol(sym, base, scope, sm)
	}
	for _, child := range scope.Children() {
		idx.addScopeSubtree(child, base, sm)
	}
}

func (idx *selfhostSpanIndex) addDeclaredSymbol(n ast.Node, key selfhostSpanKey, scope *resolve.Scope, base int) {
	sym := declaredSymbolForNode(n, scope)
	if sym == nil {
		return
	}
	idx.symbols[selfhostNameSpanKey{selfhostSpanKey: key, name: sym.Name}] = sym
	if nodeID, ok := idx.nativeNodeIDForNode(n, base); ok {
		idx.symbolsByNode[selfhostNameNodeKey{nodeID: nodeID, name: sym.Name}] = selfhostSymbolNodeEntry{
			span:   key,
			symbol: sym,
		}
	}
}

func declaredSymbolForNode(n ast.Node, scope *resolve.Scope) *resolve.Symbol {
	if scope == nil {
		return nil
	}
	pkgScope := scope.Parent()
	if pkgScope == nil {
		pkgScope = scope
	}
	switch v := n.(type) {
	case *ast.FnDecl:
		if v.Recv != nil || v.Name == "" {
			return nil
		}
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.StructDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.EnumDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.InterfaceDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.TypeAliasDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.LetDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.Variant:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	default:
		return nil
	}
}

func lookupLocalDeclSymbol(scope *resolve.Scope, name string, decl ast.Node) *resolve.Symbol {
	if scope == nil || name == "" || decl == nil {
		return nil
	}
	sym := scope.LookupLocal(name)
	if sym == nil || sym.Decl != decl {
		return nil
	}
	return sym
}

func (idx *selfhostSpanIndex) lookupExprByNodeID(nodeID int, key selfhostSpanKey, kind string) ast.Expr {
	expr := idx.exprsByNode[nodeID]
	if expr == nil {
		return nil
	}
	if kind != "" && selfhostExprKind(expr) != kind {
		return nil
	}
	if exprKey, ok := idx.exprKeys[expr]; !ok || exprKey != key {
		return nil
	}
	return expr
}

func (idx *selfhostSpanIndex) lookupExpr(key selfhostSpanKey, kind string) ast.Expr {
	if expr := idx.exprs[key]; expr != nil {
		if kind == "" || selfhostExprKind(expr) == kind {
			return expr
		}
	}
	var best ast.Expr
	bestSize := int(^uint(0) >> 1)
	for _, expr := range idx.exprsByFrom[key.start] {
		if kind != "" && selfhostExprKind(expr) != kind {
			continue
		}
		exprKey := idx.exprKeys[expr]
		if exprKey.end < key.end {
			continue
		}
		size := exprKey.end - exprKey.start
		if size < bestSize {
			best = expr
			bestSize = size
		}
	}
	if best != nil {
		return best
	}
	cacheKey := exprQueryKey{span: key, kind: kind}
	if cached, ok := idx.exprQueries[cacheKey]; ok {
		return cached
	}
	idx.ensureExprSpans()

	list := idx.exprSpans
	lo, hi := 0, len(list)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if list[mid].span.start <= key.start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for i := lo - 1; i >= 0; i-- {
		e := list[i]
		if best != nil && key.end-e.span.start >= bestSize {
			break
		}
		if kind != "" && e.kind != kind {
			continue
		}
		if e.span.end < key.end {
			continue
		}
		size := e.span.end - e.span.start
		if size < bestSize {
			best = e.expr
			bestSize = size
		}
	}
	if idx.exprQueries == nil {
		idx.exprQueries = map[exprQueryKey]ast.Expr{}
	}
	idx.exprQueries[cacheKey] = best
	return best
}

func (idx *selfhostSpanIndex) ensureExprSpans() {
	if idx.exprSpans != nil {
		return
	}
	list := make([]exprSpanEntry, 0, len(idx.exprKeys))
	for expr, span := range idx.exprKeys {
		list = append(list, exprSpanEntry{span: span, expr: expr, kind: selfhostExprKind(expr)})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].span.start < list[j].span.start
	})
	if list == nil {
		list = []exprSpanEntry{}
	}
	idx.exprSpans = list
}

func (idx *selfhostSpanIndex) lookupCallByNodeID(nodeID int, key selfhostSpanKey) *ast.CallExpr {
	call := idx.callsByNode[nodeID]
	if call == nil {
		return nil
	}
	if callKey, ok := idx.callKeys[call]; !ok || callKey != key {
		return nil
	}
	return call
}

func (idx *selfhostSpanIndex) lookupCall(key selfhostSpanKey) *ast.CallExpr {
	if call := idx.calls[key]; call != nil {
		return call
	}
	var best *ast.CallExpr
	bestSize := int(^uint(0) >> 1)
	for _, call := range idx.callsByFrom[key.start] {
		callKey := idx.callKeys[call]
		if callKey.end < key.end {
			continue
		}
		size := callKey.end - callKey.start
		if size < bestSize {
			best = call
			bestSize = size
		}
	}
	if best != nil {
		return best
	}
	if cached, ok := idx.callQueries[key]; ok {
		return cached
	}
	idx.ensureCallSpans()

	list := idx.callSpans
	lo, hi := 0, len(list)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if list[mid].span.start <= key.start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for i := lo - 1; i >= 0; i-- {
		e := list[i]
		if best != nil && key.end-e.span.start >= bestSize {
			break
		}
		if e.span.end < key.end {
			continue
		}
		size := e.span.end - e.span.start
		if size < bestSize {
			best = e.call
			bestSize = size
		}
	}
	if idx.callQueries == nil {
		idx.callQueries = map[selfhostSpanKey]*ast.CallExpr{}
	}
	idx.callQueries[key] = best
	return best
}

func (idx *selfhostSpanIndex) ensureCallSpans() {
	if idx.callSpans != nil {
		return
	}
	list := make([]callSpanEntry, 0, len(idx.callKeys))
	for call, span := range idx.callKeys {
		list = append(list, callSpanEntry{span: span, call: call})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].span.start < list[j].span.start
	})
	if list == nil {
		list = []callSpanEntry{}
	}
	idx.callSpans = list
}

func (idx *selfhostSpanIndex) lookupBindingByNodeID(nodeID int, key selfhostSpanKey, name string) ast.Node {
	entry := idx.bindingsByNode[selfhostNameNodeKey{nodeID: nodeID, name: name}]
	if entry.node == nil || entry.span != key {
		return nil
	}
	return entry.node
}

func (idx *selfhostSpanIndex) lookupSymbolByNodeID(nodeID int, key selfhostSpanKey, name string) *resolve.Symbol {
	entry := idx.symbolsByNode[selfhostNameNodeKey{nodeID: nodeID, name: name}]
	if entry.symbol == nil || entry.span != key {
		return nil
	}
	return entry.symbol
}

func selfhostExprKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.Ident:
		return "Ident"
	case *ast.IntLit:
		return "IntLit"
	case *ast.FloatLit:
		return "FloatLit"
	case *ast.StringLit:
		return "StringLit"
	case *ast.BoolLit:
		return "BoolLit"
	case *ast.CharLit:
		return "CharLit"
	case *ast.ByteLit:
		return "ByteLit"
	case *ast.UnaryExpr:
		return "Unary"
	case *ast.BinaryExpr:
		return "Binary"
	case *ast.QuestionExpr:
		return "Question"
	case *ast.CallExpr:
		return "Call"
	case *ast.FieldExpr:
		return "Field"
	case *ast.IndexExpr:
		return "Index"
	case *ast.TurbofishExpr:
		return "Turbofish"
	case *ast.RangeExpr:
		return "Range"
	case *ast.ParenExpr:
		return "Paren"
	case *ast.TupleExpr:
		return "Tuple"
	case *ast.ListExpr:
		return "List"
	case *ast.MapExpr:
		return "Map"
	case *ast.StructLit:
		return "StructLit"
	case *ast.IfExpr:
		return "If"
	case *ast.MatchExpr:
		return "Match"
	case *ast.ClosureExpr:
		return "Closure"
	case *ast.Block:
		return "Block"
	default:
		return ""
	}
}

func (idx *selfhostSpanIndex) addSymbol(sym *resolve.Symbol, base int, scope *resolve.Scope, sm *sourcemap.Map) {
	if sym == nil || sym.Decl == nil || sym.Name == "" {
		return
	}
	key, ok := spanKeyForNode(sym.Decl, base, sm)
	if !ok {
		return
	}
	idx.symbols[selfhostNameSpanKey{selfhostSpanKey: key, name: sym.Name}] = sym
	if nodeID, ok := idx.nativeNodeIDForNode(sym.Decl, base); ok {
		idx.symbolsByNode[selfhostNameNodeKey{nodeID: nodeID, name: sym.Name}] = selfhostSymbolNodeEntry{
			span:   key,
			symbol: sym,
		}
	}
	if _, ok := idx.scopes[key]; !ok {
		idx.scopes[key] = scope
	}
	if nodeID, ok := idx.nativeNodeIDForNode(sym.Decl, base); ok {
		if _, have := idx.scopesByNode[nodeID]; !have {
			idx.scopesByNode[nodeID] = scope
		}
	}
}

func (idx *selfhostSpanIndex) nativeNodeIDForNode(n ast.Node, sourceBase int) (int, bool) {
	nativeBase, ok := idx.nativeNodeIDBaseBySourceBase[sourceBase]
	if !ok {
		return 0, false
	}
	id, ok := publicASTNodeID(n)
	if !ok || id <= 1 {
		return 0, false
	}
	return nativeBase + int(id) - 2, true
}

func publicASTNodeID(n ast.Node) (ast.NodeID, bool) {
	if n == nil {
		return 0, false
	}
	rv := reflect.ValueOf(n)
	if !rv.IsValid() {
		return 0, false
	}
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return 0, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return 0, false
	}
	field := rv.FieldByName("ID")
	if !field.IsValid() || field.Type() != astNodeIDReflectType {
		return 0, false
	}
	return ast.NodeID(field.Uint()), true
}

var astNodeIDReflectType = reflect.TypeOf(ast.NodeID(0))

func spanKeyForNode(n ast.Node, base int, sm *sourcemap.Map) (key selfhostSpanKey, ok bool) {
	if n == nil {
		return selfhostSpanKey{}, false
	}
	defer func() {
		if recover() != nil {
			key = selfhostSpanKey{}
			ok = false
		}
	}()
	start := n.Pos().Offset
	end := n.End().Offset
	if sm != nil {
		if generated, ok := sm.GeneratedSpanForOriginal(diag.Span{
			Start: n.Pos(),
			End:   n.End(),
		}); ok {
			start = generated.Start.Offset
			end = generated.End.Offset
		}
	}
	if end < start {
		return selfhostSpanKey{}, false
	}
	return selfhostSpanKey{
		start: base + start,
		end:   base + end,
	}, true
}

func bindingPatternName(p ast.Pattern) string {
	switch p := p.(type) {
	case *ast.IdentPat:
		return p.Name
	case *ast.BindingPat:
		return p.Name
	default:
		return ""
	}
}

// typeReprToType converts a structured *api.TypeRepr to a types.Type using
// the given scope for symbol resolution. This replaces the former string
// round-trip through parseSelfhostTypeName. Returns nil when tr is nil so
// callers can continue to use the `if t == nil { continue }` guard.
func typeReprToType(tr *api.TypeRepr, scope *resolve.Scope) types.Type {
	if tr == nil {
		return nil
	}
	switch tr.Kind {
	case "error", "poison":
		return types.ErrorType
	case "primitive":
		switch tr.Name {
		case "", "Invalid", "Poison":
			return types.ErrorType
		case "()", "Unit":
			return types.Unit
		case "Never":
			return types.Never
		case "UntypedInt":
			return types.UntypedIntVal
		case "UntypedFloat":
			return types.UntypedFloatVal
		default:
			if p := types.PrimitiveByName(tr.Name); p != nil {
				return p
			}
			return types.ErrorType
		}
	case "unit":
		return types.Unit
	case "never":
		return types.Never
	case "named":
		sym := lookupSelfhostTypeSymbol(tr.Name, scope)
		var args []types.Type
		for i := range tr.Args {
			args = append(args, typeReprToType(&tr.Args[i], scope))
		}
		if sym.Kind == resolve.SymGeneric {
			return &types.TypeVar{Sym: sym}
		}
		return &types.Named{Sym: sym, Args: args}
	case "optional":
		if tr.Return != nil {
			return &types.Optional{Inner: typeReprToType(tr.Return, scope)}
		}
		return types.ErrorType
	case "tuple":
		if len(tr.Args) == 0 {
			return types.Unit
		}
		if len(tr.Args) == 1 {
			return typeReprToType(&tr.Args[0], scope)
		}
		elems := make([]types.Type, 0, len(tr.Args))
		for i := range tr.Args {
			elems = append(elems, typeReprToType(&tr.Args[i], scope))
		}
		return &types.Tuple{Elems: elems}
	case "fn":
		var params []types.Type
		for i := range tr.Args {
			params = append(params, typeReprToType(&tr.Args[i], scope))
		}
		ret := types.Type(types.Unit)
		if tr.Return != nil {
			ret = typeReprToType(tr.Return, scope)
		}
		return &types.FnType{Params: params, Return: ret}
	case "typevar":
		sym := lookupSelfhostTypeSymbol(tr.Name, scope)
		return &types.TypeVar{Sym: sym}
	case "self":
		// Self type — approximated as error type for now
		return types.ErrorType
	default:
		return types.ErrorType
	}
}

func lookupSelfhostTypeSymbol(head string, scope *resolve.Scope) *resolve.Symbol {
	name := strings.TrimSpace(head)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	if scope != nil {
		if sym := scope.Lookup(name); sym != nil {
			return sym
		}
	}
	if _, ok := scalarByName[name]; ok {
		return syntheticBuiltinSym(name)
	}
	switch name {
	case "List", "Map", "Set", "Option", "Result", "Error", "Equal", "Ordered", "Hashable", "ToString", "Chan", "Channel", "Handle", "TaskGroup", "Iter":
		return syntheticBuiltinSym(name)
	}
	if name == "Self" || looksLikeSelfhostGeneric(name) {
		return &resolve.Symbol{Name: name, Kind: resolve.SymGeneric}
	}
	return &resolve.Symbol{Name: name, Kind: resolve.SymTypeAlias}
}

func looksLikeSelfhostGeneric(name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, ".<>(), ") {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z' && len(name) == 1
}

