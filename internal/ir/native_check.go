package ir

import (
	"reflect"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
)

type nativeCheckCache struct {
	idx                api.CheckResultIndex
	ready              bool
	typedNodesByNodeID map[int][]*api.CheckedNode
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

func nativeRecordNodeID(nodeID, legacyNode int) int {
	if nodeID != 0 {
		return nodeID
	}
	return legacyNode
}

func (l *lowerer) nativeCheckedType(e ast.Expr) Type {
	id := astNodeID(e)
	if id == 0 {
		return nil
	}
	if _, ok := l.nativeIndex(); !ok {
		return nil
	}
	rec := selectNativeTypedNode(l.native.typedNodesByNodeID[int(id)], e)
	if rec == nil {
		return nil
	}
	return l.fromNativeTypeRepr(rec.Type)
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
	id := astNodeID(e)
	if id == 0 {
		return nil
	}
	idx, ok := l.nativeIndex()
	if !ok {
		return nil
	}
	rec := selectNativeInstantiation(idx.InstantiationsByNodeID[int(id)], e)
	if rec == nil || len(rec.TypeArgs) == 0 {
		return nil
	}
	out := make([]Type, 0, len(rec.TypeArgs))
	for i := range rec.TypeArgs {
		out = append(out, l.fromNativeTypeRepr(&rec.TypeArgs[i]))
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
	id := astNodeID(n)
	if id == 0 {
		return nil
	}
	idx, ok := l.nativeIndex()
	if !ok {
		return nil
	}
	rec := selectNativeBinding(idx.BindingsByNodeID[int(id)], n, name)
	if rec == nil {
		return nil
	}
	return l.fromNativeTypeRepr(rec.Type)
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
	id := astNodeID(n)
	if id == 0 {
		return nil
	}
	idx, ok := l.nativeIndex()
	if !ok {
		return nil
	}
	rec := selectNativeSymbol(idx.SymbolsByNodeID[int(id)], n, name)
	if rec == nil {
		return nil
	}
	return l.fromNativeTypeRepr(rec.Type)
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
