package ir

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
)

type nativeCheckCache struct {
	idx                   api.CheckResultIndex
	ready                 bool
	typedNodesByNodeID    map[int][]*api.CheckedNode
	typedNodesByByteRange map[[2]int][]*api.CheckedNode
}

func (l *lowerer) nativeIndex() (api.CheckResultIndex, bool) {
	if l.native.ready {
		return l.native.idx, l.chk != nil && l.chk.NativeResult() != nil
	}
	l.native.ready = true
	if l.chk == nil || l.chk.NativeResult() == nil {
		return l.native.idx, false
	}
	native := l.chk.NativeResult()
	native.EnsureStableIDs()
	l.native.idx = native.Index()
	l.native.typedNodesByNodeID = typedNodesByNodeID(native.TypedNodes)
	l.native.typedNodesByByteRange = typedNodesByByteRange(native.TypedNodes)
	return l.native.idx, true
}

func typedNodesByNodeID(nodes []api.CheckedNode) map[int][]*api.CheckedNode {
	out := make(map[int][]*api.CheckedNode, len(nodes))
	for i := range nodes {
		id := nativeRecordNodeID(nodes[i].NodeID, nodes[i].Node)
		if id == 0 {
			continue
		}
		out[id] = append(out[id], &nodes[i])
	}
	return out
}

// typedNodesByByteRange indexes typed nodes by their source byte range,
// keyed as [start, end]. Used as the byte-range fallback in
// nativeCheckedType when the AST NodeID does not match the selfhost
// arena id (the two namespaces are independent). The CheckResultIndex
// TypedNodesByNodeKey uses a content-hash key (`stableNodeKey`) that
// includes the node kind, so it cannot be queried from an AST node
// alone; this raw index closes that gap.
func typedNodesByByteRange(nodes []api.CheckedNode) map[[2]int][]*api.CheckedNode {
	out := make(map[[2]int][]*api.CheckedNode, len(nodes))
	for i := range nodes {
		if nodes[i].Start == 0 && nodes[i].End == 0 {
			continue
		}
		key := [2]int{nodes[i].Start, nodes[i].End}
		out[key] = append(out[key], &nodes[i])
	}
	return out
}

func nativeRecordNodeID(nodeID, legacyNode int) int {
	if nodeID != 0 {
		return nodeID
	}
	return legacyNode
}

func (l *lowerer) nativeCheckedType(e ast.Expr) Type {
	_, ok := l.nativeIndex()
	if !ok {
		return nil
	}

	// Fast path: NodeID lookup.
	if id := int(astNodeID(e)); id != 0 {
		if rec := selectNativeTypedNode(l.native.typedNodesByNodeID[id], e); rec != nil {
			return l.fromNativeTypeRepr(rec.Type)
		}
	}

	// Byte-range fallback, restricted to ClosureExpr. AST NodeIDs and
	// selfhost arena ids live in separate namespaces, so the byID path
	// misses whenever the two diverge. We restrict this fallback to
	// closures because:
	//   1. Closures are the one case where the IR-level fallbacks
	//      (signatureForFn, stdlibFreeFnReturnType, recoverOperandType
	//      etc.) cannot recover the inferred FnType — there is no
	//      signature table to consult, and `lowerClosure` relies on
	//      `out.T` to backfill per-param Types for inline closures like
	//      `|acc, n| acc + n`.
	//   2. Widening the fallback to every expression shape lets the
	//      byte-range hit win over the existing recovery chain, which
	//      observably reduces stage0 coverage of `toolchain/` functions
	//      (selfhost typed nodes occasionally carry pre-inference
	//      shapes that the MIR signature tables would resolve better).
	if _, isClosure := e.(*ast.ClosureExpr); !isClosure {
		return nil
	}
	start, end, hasRange := astNodeByteRange(e)
	if !hasRange {
		return nil
	}
	for _, rec := range l.native.typedNodesByByteRange[[2]int{start, end}] {
		if rec == nil || rec.Type == nil || rec.Kind != "Closure" {
			continue
		}
		return l.fromNativeTypeRepr(rec.Type)
	}
	return nil
}

func selectNativeTypedNode(records []*api.CheckedNode, n ast.Node) *api.CheckedNode {
	var out *api.CheckedNode
	for _, rec := range records {
		if rec == nil || !nativeRecordMatchesNode(rec.Start, rec.End, n) {
			continue
		}
		if out != nil {
			return nil
		}
		out = rec
	}
	return out
}

func (l *lowerer) nativeInstantiationArgs(e *ast.CallExpr) []Type {
	idx, ok := l.nativeIndex()
	if !ok {
		return nil
	}

	// SemanticDB records instantiations against the callee identifier's
	// NodeID, not the enclosing CallExpr's NodeID. Empirically for
	// `id(42)`: AST CallExpr.ID=13, AST Fn.ID=11, SemanticDB
	// inst.NodeID=11. Try the callee Ident first.
	//
	// Byte ranges differ between AST and SemanticDB (the inst is anchored
	// at the argument list, e.g. `(42)` rather than the callee `id` or
	// the full call) so we cannot use `selectNativeInstantiation`'s
	// byte-range filter here — take the first non-empty record matched
	// by NodeID. The selfhost arena assigns one inst per call site so
	// the slice is single-entry in practice.
	if calleeID := calleeIdentNodeID(e.Fn); calleeID != 0 {
		for _, rec := range idx.InstantiationsByNodeID[calleeID] {
			if rec != nil && len(rec.TypeArgs) > 0 {
				return l.nativeInstantiationTypes(rec.TypeArgs)
			}
		}
	}

	// Fallback: CallExpr's own NodeID. Synthetic test data
	// (`TestLowerInstantiationArgsUseNativeIndexWithoutLegacyMap`)
	// anchors records to the call expression directly, and a future
	// selfhost frontend change could do the same — keep this path as
	// a safety net.
	if id := int(astNodeID(e)); id != 0 {
		if rec := selectNativeInstantiation(idx.InstantiationsByNodeID[id], e); rec != nil && len(rec.TypeArgs) > 0 {
			return l.nativeInstantiationTypes(rec.TypeArgs)
		}
	}
	return nil
}

// calleeIdentNodeID returns the AST NodeID for the callee identifier
// of a call expression's `Fn` slot. Handles three shapes:
//   - direct `f(x)` → Ident node
//   - turbofish `f::<T>(x)` → TurbofishExpr wrapping an Ident
//   - method/path-qualified `x.f(...)` / `m.f(...)` → FieldExpr
//
// Returns 0 for shapes we don't track instantiations against.
func calleeIdentNodeID(fn ast.Expr) int {
	switch x := fn.(type) {
	case *ast.Ident:
		return int(x.ID)
	case *ast.TurbofishExpr:
		return calleeIdentNodeID(x.Base)
	case *ast.FieldExpr:
		return int(x.ID)
	}
	return 0
}

func (l *lowerer) nativeInstantiationTypes(args []api.TypeRepr) []Type {
	out := make([]Type, 0, len(args))
	for i := range args {
		out = append(out, l.fromNativeTypeRepr(&args[i]))
	}
	return out
}

func selectNativeInstantiation(records []*api.CheckInstantiation, n ast.Node) *api.CheckInstantiation {
	var out *api.CheckInstantiation
	for _, rec := range records {
		if rec == nil || !nativeRecordMatchesNode(rec.Start, rec.End, n) {
			continue
		}
		if out != nil {
			return nil
		}
		out = rec
	}
	return out
}

func (l *lowerer) nativeBindingTypeForSymbol(sym *resolve.Symbol) Type {
	if sym == nil {
		return nil
	}
	return l.nativeBindingType(sym.Decl, sym.Name)
}

func (l *lowerer) nativeBindingType(n ast.Node, name string) Type {
	idx, ok := l.nativeIndex()
	if !ok {
		return nil
	}

	// Fast path: NodeID lookup.
	if id := int(astNodeID(n)); id != 0 {
		if rec := selectNativeBinding(idx.BindingsByNodeID[id], n, name); rec != nil {
			return l.fromNativeTypeRepr(rec.Type)
		}
	}

	// Fallback: span-based lookup via NodeKey.
	start, end, hasRange := astNodeByteRange(n)
	if !hasRange {
		return nil
	}
	key := fmt.Sprintf("%d:%d", start, end)
	for _, rec := range idx.BindingsByNodeKey[key] {
		if rec != nil && rec.Type != nil && (name == "" || rec.Name == name) {
			return l.fromNativeTypeRepr(rec.Type)
		}
	}
	return nil
}

func selectNativeBinding(records []*api.CheckedBinding, n ast.Node, name string) *api.CheckedBinding {
	var out *api.CheckedBinding
	for _, rec := range records {
		if rec == nil || (name != "" && rec.Name != name) || !nativeRecordMatchesNode(rec.Start, rec.End, n) {
			continue
		}
		if out != nil {
			return nil
		}
		out = rec
	}
	return out
}

func (l *lowerer) nativeSymbolType(sym *resolve.Symbol) Type {
	if sym == nil {
		return nil
	}
	return l.nativeSymbolTypeForNode(sym.Decl, sym.Name)
}

func (l *lowerer) nativeSymbolTypeForNode(n ast.Node, name string) Type {
	idx, ok := l.nativeIndex()
	if !ok {
		return nil
	}

	// Fast path: NodeID lookup.
	if id := int(astNodeID(n)); id != 0 {
		if rec := selectNativeSymbol(idx.SymbolsByNodeID[id], n, name); rec != nil {
			return l.fromNativeTypeRepr(rec.Type)
		}
	}

	// Fallback: span-based lookup via NodeKey.
	start, end, hasRange := astNodeByteRange(n)
	if !hasRange {
		return nil
	}
	key := fmt.Sprintf("%d:%d", start, end)
	for _, rec := range idx.SymbolsByNodeKey[key] {
		if rec != nil && rec.Type != nil && (name == "" || rec.Name == name) {
			return l.fromNativeTypeRepr(rec.Type)
		}
	}
	return nil
}

func selectNativeSymbol(records []*api.CheckedSymbol, n ast.Node, name string) *api.CheckedSymbol {
	var out *api.CheckedSymbol
	for _, rec := range records {
		if rec == nil || (name != "" && rec.Name != name) || !nativeRecordMatchesNode(rec.Start, rec.End, n) {
			continue
		}
		if out != nil {
			return nil
		}
		out = rec
	}
	return out
}

func (l *lowerer) fromNativeTypeRepr(tr *api.TypeRepr) Type {
	if tr == nil {
		return nil
	}
	switch tr.Kind {
	case "error", "poison":
		return ErrTypeVal
	case "primitive":
		switch tr.Name {
		case "", "Invalid", "Poison":
			return ErrTypeVal
		case "UntypedInt":
			return TInt
		case "UntypedFloat":
			return TFloat
		case "()", "Unit":
			return TUnit
		case "Never":
			return TNever
		default:
			if p := primitiveByName(tr.Name); p != nil {
				return p
			}
			return ErrTypeVal
		}
	case "unit":
		return TUnit
	case "never":
		return TNever
	case "optional":
		inner := l.fromNativeTypeRepr(tr.Return)
		if inner == nil {
			inner = ErrTypeVal
		}
		return &OptionalType{Inner: inner}
	case "tuple":
		elems := make([]Type, len(tr.Args))
		for i := range tr.Args {
			elems[i] = l.fromNativeTypeRepr(&tr.Args[i])
		}
		return &TupleType{Elems: elems}
	case "fn":
		params := make([]Type, len(tr.Args))
		for i := range tr.Args {
			params[i] = l.fromNativeTypeRepr(&tr.Args[i])
		}
		ret := l.fromNativeTypeRepr(tr.Return)
		if ret == nil {
			ret = TUnit
		}
		return &FnType{Params: params, Return: ret}
	case "named":
		return l.nativeNamedType(tr)
	case "typevar":
		name := tr.Name
		if name == "" {
			name = "?"
		}
		return &TypeVar{Name: name}
	case "self":
		return &TypeVar{Name: "Self"}
	default:
		if tr.Name != "" {
			return l.nativeNamedType(tr)
		}
		return ErrTypeVal
	}
}

func (l *lowerer) nativeNamedType(tr *api.TypeRepr) Type {
	name := tr.Name
	if name == "" {
		name = tr.Path
	}
	if name == "" {
		return ErrTypeVal
	}
	pkg, base := splitQualifiedTypeName(name)
	if pkg == "" && len(tr.Args) == 0 {
		if p := primitiveByName(base); p != nil {
			return p
		}
	}
	if alias := l.localTypeAliasTarget(pkg, base); alias != nil {
		return alias
	}
	args := make([]Type, len(tr.Args))
	for i := range tr.Args {
		args[i] = l.fromNativeTypeRepr(&tr.Args[i])
	}
	builtin := pkg == "" && isNativeBuiltinNamedType(base)
	return &NamedType{Package: pkg, Name: base, Args: args, Builtin: builtin}
}

func splitQualifiedTypeName(name string) (string, string) {
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || dot == len(name)-1 {
		return "", name
	}
	return name[:dot], name[dot+1:]
}

func isNativeBuiltinNamedType(name string) bool {
	switch name {
	case "List", "Map", "Set", "Option", "Result":
		return true
	default:
		return false
	}
}

func (l *lowerer) localTypeAliasTarget(pkg, name string) Type {
	if pkg != "" || name == "" || l.file == nil {
		return nil
	}
	for _, decl := range l.file.Decls {
		alias, ok := decl.(*ast.TypeAliasDecl)
		if !ok || alias == nil || alias.Name != name || alias.Target == nil {
			continue
		}
		return l.lowerType(alias.Target)
	}
	return nil
}

var astNodeIDType = reflect.TypeOf(ast.NodeID(0))

func astNodeID(n ast.Node) ast.NodeID {
	if n == nil {
		return 0
	}
	v := reflect.ValueOf(n)
	if v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0
	}
	f := v.FieldByName("ID")
	if !f.IsValid() || f.Type() != astNodeIDType {
		return 0
	}
	return ast.NodeID(f.Uint())
}

func nativeRecordMatchesNode(start, end int, n ast.Node) bool {
	nodeStart, nodeEnd, ok := astNodeByteRange(n)
	if !ok {
		return true
	}
	return start == nodeStart && end == nodeEnd
}

func astNodeByteRange(n ast.Node) (int, int, bool) {
	if n == nil {
		return 0, 0, false
	}
	start := n.Pos()
	end := n.End()
	if start.Line == 0 && start.Column == 0 && start.Offset == 0 &&
		end.Line == 0 && end.Column == 0 && end.Offset == 0 {
		return 0, 0, false
	}
	return start.Offset, end.Offset, true
}
