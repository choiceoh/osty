package selfhost

import (
	"reflect"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

type publicStableIDKey struct {
	start int
	end   int
	kind  string
}

// assignPublicStableIDs preserves the selfhost parser arena identity on the
// public AST compatibility surface. Public NodeID 1 is reserved for the file
// root because ast.NodeID uses 0 as the unassigned sentinel; arena node N maps
// to public NodeID N+2.
func assignPublicStableIDs(file *ast.File, arena *AstArena, toks []token.Token) {
	if file == nil || arena == nil {
		return
	}

	byKey := map[publicStableIDKey][]ast.NodeID{}
	for idx, n := range arena.nodes {
		if n == nil {
			continue
		}
		id := ast.NodeID(idx + 2)
		for _, span := range publicStableArenaSpans(arena, n, toks) {
			for _, kind := range publicStableArenaKinds(n) {
				key := publicStableIDKey{start: span.start, end: span.end, kind: kind}
				byKey[key] = append(byKey[key], id)
			}
		}
	}

	used := map[ast.NodeID]bool{}
	assignPublicStableIDValue(reflect.ValueOf(file), byKey, used)
}

type publicStableSpan struct {
	start int
	end   int
}

func publicStableArenaSpans(arena *AstArena, n *AstNode, toks []token.Token) []publicStableSpan {
	start, end, ok := publicStableArenaSpan(n, toks)
	if !ok {
		return nil
	}
	spans := []publicStableSpan{{start: start, end: end}}
	if publicStart, ok := publicStableArenaPublicStart(arena, n, toks); ok && publicStart != start {
		spans = append(spans, publicStableSpan{start: publicStart, end: end})
	}
	return spans
}

func publicStableArenaSpan(n *AstNode, toks []token.Token) (int, int, bool) {
	if n.start < 0 || n.start >= len(toks) {
		return 0, 0, false
	}
	start := toks[n.start].Pos.Offset
	end := toks[n.start].End.Offset
	if n.end > 0 {
		endIdx := n.end - 1
		if endIdx < 0 {
			endIdx = 0
		}
		if endIdx >= len(toks) {
			endIdx = len(toks) - 1
		}
		end = toks[endIdx].End.Offset
	}
	if end < start {
		end = start
	}
	return start, end, true
}

func publicStableArenaPublicStart(arena *AstArena, n *AstNode, toks []token.Token) (int, bool) {
	if arena == nil || n == nil {
		return 0, false
	}
	switch astNodeKindName(n.kind) {
	case "Binary", "Call", "Field", "Index", "Range", "Question", "Turbofish", "StructLit":
		return publicStableArenaChildStart(arena, n.left, toks)
	default:
		return 0, false
	}
}

func publicStableArenaChildStart(arena *AstArena, idx int, toks []token.Token) (int, bool) {
	if arena == nil || idx < 0 || idx >= len(arena.nodes) {
		return 0, false
	}
	child := arena.nodes[idx]
	if child == nil {
		return 0, false
	}
	if start, ok := publicStableArenaPublicStart(arena, child, toks); ok {
		return start, true
	}
	start, _, ok := publicStableArenaSpan(child, toks)
	return start, ok
}

func publicStableArenaKinds(n *AstNode) []string {
	if n == nil {
		return nil
	}
	switch astNodeKindName(n.kind) {
	case "Field_":
		return []string{"Field_", "AnnotationArg", "StructLitField", "StructPatField"}
	case "Field":
		return []string{"Field", "Arg"}
	case "Type":
		return []string{"Type", "NamedType", "OptionalType", "TupleType", "FnType"}
	case "Pattern":
		return []string{"Pattern", "IdentPat", "WildcardPat", "LiteralPat", "TuplePat", "StructPat", "VariantPat", "BindingPat", "RangePat", "OrPat"}
	case "Ident":
		return []string{"Ident", "IdentPat", "AnnotationArg"}
	case "Let":
		return []string{"Let", "LetStmt", "LetDecl"}
	case "For":
		return []string{"For", "ForStmt", "LoopExpr"}
	case "Call":
		return []string{"Call", "CallExpr"}
	case "Binary":
		return []string{"Binary", "BinaryExpr"}
	case "Unary":
		return []string{"Unary", "UnaryExpr"}
	case "Question":
		return []string{"Question", "QuestionExpr"}
	case "Index":
		return []string{"Index", "IndexExpr"}
	case "Range":
		return []string{"Range", "RangeExpr"}
	case "If":
		return []string{"If", "IfExpr"}
	case "Match":
		return []string{"Match", "MatchExpr"}
	case "Closure":
		return []string{"Closure", "ClosureExpr"}
	case "List":
		return []string{"List", "ListExpr"}
	case "Tuple":
		return []string{"Tuple", "TupleExpr"}
	case "Map":
		return []string{"Map", "MapExpr"}
	case "StructLit":
		return []string{"StructLit", "StructLit"}
	case "Paren":
		return []string{"Paren", "ParenExpr"}
	case "Turbofish":
		return []string{"Turbofish", "TurbofishExpr"}
	case "ExprStmt":
		return []string{"ExprStmt"}
	case "Param":
		return []string{"Param", "Receiver"}
	default:
		return []string{astNodeKindName(n.kind)}
	}
}

func assignPublicStableIDValue(v reflect.Value, byKey map[publicStableIDKey][]ast.NodeID, used map[ast.NodeID]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		assignPublicStableIDValue(v.Elem(), byKey, used)
		return
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			assignPublicStableIDValue(v.Index(i), byKey, used)
		}
		return
	case reflect.Struct:
		// ok — assign this node, then recurse into children.
	default:
		return
	}

	assignPublicStableIDForStruct(v, byKey, used)
	for i := 0; i < v.NumField(); i++ {
		assignPublicStableIDValue(v.Field(i), byKey, used)
	}
}

func assignPublicStableIDForStruct(v reflect.Value, byKey map[publicStableIDKey][]ast.NodeID, used map[ast.NodeID]bool) {
	idField := v.FieldByName("ID")
	if !idField.IsValid() || idField.Type() != astNodeIDReflectType || !idField.CanSet() || idField.Uint() != 0 {
		return
	}

	if v.Type().Name() == "File" {
		idField.SetUint(1)
		used[1] = true
		return
	}

	node, ok := publicStableNodeFromStruct(v)
	if !ok {
		return
	}
	for _, kind := range publicStablePublicKinds(v.Type().Name()) {
		key := publicStableIDKey{start: node.Pos().Offset, end: node.End().Offset, kind: kind}
		for _, id := range byKey[key] {
			if used[id] {
				continue
			}
			idField.SetUint(uint64(id))
			used[id] = true
			return
		}
	}
}

func publicStableNodeFromStruct(v reflect.Value) (ast.Node, bool) {
	if !v.CanAddr() {
		return nil, false
	}
	if !v.Addr().CanInterface() {
		return nil, false
	}
	node, ok := v.Addr().Interface().(ast.Node)
	return node, ok
}

func publicStablePublicKinds(name string) []string {
	switch name {
	case "File":
		return []string{"File"}
	case "FnDecl":
		return []string{"FnDecl"}
	case "StructDecl":
		return []string{"StructDecl"}
	case "EnumDecl":
		return []string{"EnumDecl"}
	case "InterfaceDecl":
		return []string{"InterfaceDecl"}
	case "TypeAliasDecl":
		return []string{"TypeAlias"}
	case "UseDecl":
		return []string{"UseDecl"}
	case "LetDecl":
		return []string{"LetDecl", "Let"}
	case "LetStmt":
		return []string{"LetStmt", "Let"}
	case "ReturnStmt":
		return []string{"Return"}
	case "BreakStmt":
		return []string{"Break"}
	case "ContinueStmt":
		return []string{"Continue"}
	case "DeferStmt":
		return []string{"Defer"}
	case "ForStmt":
		return []string{"ForStmt", "For"}
	case "AssignStmt":
		return []string{"Assign"}
	case "ChanSendStmt":
		return []string{"ChanSend"}
	case "ExprStmt":
		return []string{"ExprStmt"}
	case "Block":
		return []string{"Block"}
	case "LoopExpr":
		return []string{"LoopExpr", "For"}
	case "Ident":
		return []string{"Ident"}
	case "IntLit":
		return []string{"IntLit"}
	case "FloatLit":
		return []string{"FloatLit"}
	case "StringLit":
		return []string{"StringLit"}
	case "BoolLit":
		return []string{"BoolLit"}
	case "CharLit":
		return []string{"CharLit"}
	case "ByteLit":
		return []string{"ByteLit"}
	case "BinaryExpr":
		return []string{"BinaryExpr", "Binary"}
	case "UnaryExpr":
		return []string{"UnaryExpr", "Unary"}
	case "CallExpr":
		return []string{"CallExpr", "Call"}
	case "FieldExpr":
		return []string{"Field"}
	case "IndexExpr":
		return []string{"IndexExpr", "Index"}
	case "RangeExpr":
		return []string{"RangeExpr", "Range"}
	case "IfExpr":
		return []string{"IfExpr", "If"}
	case "MatchExpr":
		return []string{"MatchExpr", "Match"}
	case "ClosureExpr":
		return []string{"ClosureExpr", "Closure"}
	case "ListExpr":
		return []string{"ListExpr", "List"}
	case "TupleExpr":
		return []string{"TupleExpr", "Tuple"}
	case "MapExpr":
		return []string{"MapExpr", "Map"}
	case "StructLit":
		return []string{"StructLit"}
	case "ParenExpr":
		return []string{"ParenExpr", "Paren"}
	case "QuestionExpr":
		return []string{"QuestionExpr", "Question"}
	case "TurbofishExpr":
		return []string{"TurbofishExpr", "Turbofish"}
	case "NamedType", "OptionalType", "TupleType", "FnType":
		return []string{name, "Type"}
	case "IdentPat", "WildcardPat", "LiteralPat", "TuplePat", "StructPat", "VariantPat", "BindingPat", "RangePat", "OrPat":
		return []string{name, "Pattern", "Ident"}
	case "Annotation":
		return []string{"Annotation"}
	case "AnnotationArg":
		return []string{"AnnotationArg", "Field_", "Ident"}
	case "GenericParam":
		return []string{"GenericParam"}
	case "Receiver":
		return []string{"Receiver", "Param"}
	case "Param":
		return []string{"Param"}
	case "Field":
		return []string{"Field_"}
	case "Variant":
		return []string{"Variant"}
	case "Arg":
		return []string{"Arg", "Field"}
	case "MapEntry":
		return []string{"MapEntry"}
	case "MatchArm":
		return []string{"MatchArm"}
	case "StructLitField":
		return []string{"StructLitField", "Field_"}
	case "StructPatField":
		return []string{"StructPatField", "Field_"}
	default:
		return []string{name}
	}
}

var astNodeIDReflectType = reflect.TypeOf(ast.NodeID(0))
