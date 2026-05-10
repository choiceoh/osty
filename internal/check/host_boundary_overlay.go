package check

import (
	"reflect"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/types"
)

// overlaySelfhostResult adapts selfhost/native structured checker facts onto
// the legacy AST-keyed maps in Result. Native node IDs are preferred when the
// source segment advertises them; exact-span fallback remains for older source
// shapes that do not preserve public↔native node-id translation.
func overlaySelfhostResult(result *Result, src selfhostCheckedSource, checked api.CheckResult) {
	if result == nil || len(src.files) == 0 {
		return
	}
	indexes := make([]overlaySegmentIndex, 0, len(src.files))
	for _, seg := range src.files {
		indexes = append(indexes, buildOverlaySegmentIndex(seg))
	}
	for _, rec := range checked.TypedNodes {
		node := overlayNodeForRecord(indexes, rec.NodeID, rec.Node, rec.Start, rec.End)
		expr, ok := node.(ast.Expr)
		if !ok {
			continue
		}
		result.Types[expr] = typeReprToType(rec.Type)
	}
	for _, rec := range checked.Bindings {
		node := overlayNodeForRecord(indexes, rec.NodeID, rec.Node, rec.Start, rec.End)
		if node == nil {
			continue
		}
		target := overlayBindingTarget(indexes, node)
		if target == nil {
			continue
		}
		result.LetTypes[target] = typeReprToType(rec.Type)
	}
	for _, rec := range checked.Instantiations {
		node := overlayNodeForRecord(indexes, rec.NodeID, rec.Node, rec.Start, rec.End)
		call, ok := node.(*ast.CallExpr)
		if !ok || call == nil {
			continue
		}
		args := make([]types.Type, 0, len(rec.TypeArgs))
		for i := range rec.TypeArgs {
			args = append(args, typeReprToType(&rec.TypeArgs[i]))
		}
		result.InstantiationsByID[call.ID] = args
		already := false
		for _, existing := range result.InstantiationCalls {
			if existing == call {
				already = true
				break
			}
		}
		if !already {
			result.InstantiationCalls = append(result.InstantiationCalls, call)
		}
	}
}

type overlaySegmentIndex struct {
	seg         selfhostFileSegment
	nativeNodes map[int]ast.Node
	spanNodes   map[[2]int][]ast.Node
	parents     map[ast.Node]ast.Node
}

func buildOverlaySegmentIndex(seg selfhostFileSegment) overlaySegmentIndex {
	idx := overlaySegmentIndex{
		seg:         seg,
		nativeNodes: map[int]ast.Node{},
		spanNodes:   map[[2]int][]ast.Node{},
		parents:     map[ast.Node]ast.Node{},
	}
	if seg.file == nil {
		return idx
	}
	walkOverlayAST(seg.file, nil, func(node ast.Node, parent ast.Node) {
		if node == nil {
			return
		}
		idx.parents[node] = parent
		if seg.nativeNodeIDs {
			if id, ok := overlayPublicNodeID(node); ok {
				nativeID := seg.nativeNodeIDBase + int(id) - 2
				idx.nativeNodes[nativeID] = node
			}
		}
		start := node.Pos().Offset + seg.base
		end := node.End().Offset + seg.base
		idx.spanNodes[[2]int{start, end}] = append(idx.spanNodes[[2]int{start, end}], node)
	})
	return idx
}

func overlayNodeForRecord(indexes []overlaySegmentIndex, nodeID, legacyNode, start, end int) ast.Node {
	for _, idx := range indexes {
		if nodeID >= 0 {
			if node, ok := idx.nativeNodes[nodeID]; ok {
				return node
			}
		}
		if legacyNode >= 0 {
			if node, ok := idx.nativeNodes[legacyNode]; ok {
				return node
			}
		}
	}
	key := [2]int{start, end}
	for _, idx := range indexes {
		if nodes := idx.spanNodes[key]; len(nodes) > 0 {
			return nodes[0]
		}
	}
	return nil
}

func overlayBindingTarget(indexes []overlaySegmentIndex, node ast.Node) ast.Node {
	switch n := node.(type) {
	case *ast.LetStmt:
		return n
	case *ast.LetDecl:
		return n
	case ast.Pattern:
		for _, idx := range indexes {
			if parent := idx.parents[node]; parent != nil {
				switch p := parent.(type) {
				case *ast.LetStmt:
					return p
				case *ast.LetDecl:
					return p
				}
			}
		}
	}
	return nil
}

func overlayPublicNodeID(node ast.Node) (ast.NodeID, bool) {
	if node == nil {
		return 0, false
	}
	rv := reflect.ValueOf(node)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return 0, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return 0, false
	}
	field := rv.FieldByName("ID")
	if !field.IsValid() || field.Type() != reflect.TypeOf(ast.NodeID(0)) {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return ast.NodeID(field.Uint()), true
	default:
		return ast.NodeID(field.Int()), true
	}
}

func walkOverlayAST(node ast.Node, parent ast.Node, visit func(ast.Node, ast.Node)) {
	if node == nil {
		return
	}
	visit(node, parent)
	rv := reflect.ValueOf(node)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < rv.NumField(); i++ {
		walkOverlayValue(rv.Field(i), node, visit)
	}
}

func walkOverlayValue(v reflect.Value, parent ast.Node, visit func(ast.Node, ast.Node)) {
	if !v.IsValid() {
		return
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		if node, ok := v.Interface().(ast.Node); ok {
			walkOverlayAST(node, parent, visit)
			return
		}
		walkOverlayValue(v.Elem(), parent, visit)
		return
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkOverlayValue(v.Index(i), parent, visit)
		}
	}
}

func typeReprToType(repr *api.TypeRepr) types.Type {
	if repr == nil {
		return nil
	}
	switch repr.Kind {
	case "primitive":
		if scalar, ok := scalarByName[repr.Name]; ok {
			return scalar
		}
		return &types.Named{Sym: syntheticBuiltinSym(repr.Name)}
	case "named":
		args := make([]types.Type, 0, len(repr.Args))
		for i := range repr.Args {
			args = append(args, typeReprToType(&repr.Args[i]))
		}
		return &types.Named{Sym: syntheticBuiltinSym(repr.Name), Args: args}
	case "tuple":
		elems := make([]types.Type, 0, len(repr.Args))
		for i := range repr.Args {
			elems = append(elems, typeReprToType(&repr.Args[i]))
		}
		if len(elems) == 0 {
			return types.Unit
		}
		return &types.Tuple{Elems: elems}
	case "optional":
		inner := typeReprToType(repr.Return)
		if inner == nil && len(repr.Args) == 1 {
			inner = typeReprToType(&repr.Args[0])
		}
		if inner == nil {
			inner = types.Unit
		}
		return &types.Optional{Inner: inner}
	case "fn":
		params := make([]types.Type, 0, len(repr.Args))
		for i := range repr.Args {
			params = append(params, typeReprToType(&repr.Args[i]))
		}
		ret := typeReprToType(repr.Return)
		if ret == nil {
			ret = types.Unit
		}
		return &types.FnType{Params: params, Return: ret}
	case "unit":
		return types.Unit
	case "never":
		return types.Never
	case "typevar", "self":
		name := repr.Name
		if name == "" {
			name = "Self"
		}
		return &types.TypeVar{Sym: &resolve.Symbol{Name: name, Kind: resolve.SymGeneric}}
	case "error", "poison":
		return types.ErrorType
	default:
		if scalar, ok := scalarByName[repr.Name]; ok {
			return scalar
		}
		return &types.Named{Sym: syntheticBuiltinSym(repr.Name)}
	}
}
