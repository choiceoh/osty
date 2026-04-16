//go:build !selfhostgen

package check

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

type selfhostCheckedSource struct {
	source []byte
	files  []selfhostFileSegment
}

type selfhostFileSegment struct {
	file  *ast.File
	scope *resolve.Scope
	refs  map[*ast.Ident]*resolve.Symbol
	base  int
}

func applySelfhostFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) {
	if result == nil || len(src) == 0 {
		return
	}
	checkedSrc := selfhostFileSource(file, rr, src, stdlib)
	checked := selfhost.CheckSourceStructured(checkedSrc.source)
	result.Diags = selfhostCheckerDiags(checkedSrc.source, checked)
	overlaySelfhostResult(result, checkedSrc, checked)
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, _ *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	if result == nil || pkg == nil {
		return
	}
	src := selfhostPackageSource(pkg, ws, stdlib)
	if len(src.source) == 0 {
		return
	}
	checked := selfhost.CheckSourceStructured(src.source)
	result.Diags = selfhostCheckerDiags(src.source, checked)
	overlaySelfhostResult(result, src, checked)
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, _ map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
	if ws == nil {
		return
	}
	for path, result := range results {
		pkg := ws.Packages[path]
		if isProviderStdlibPackage(ws, path, pkg) {
			continue
		}
		applySelfhostPackageResult(result, pkg, nil, ws, stdlib)
	}
}

func selfhostCheckerDiags(src []byte, checked selfhost.CheckResult) []*diag.Diagnostic {
	if checked.Summary.Errors == 0 {
		return nil
	}
	label := "self-hosted checker reported type errors"
	if checked.Summary.Errors == 1 {
		label = "self-hosted checker reported a type error"
	}
	return []*diag.Diagnostic{
		diag.New(diag.Error, fmt.Sprintf("%s: %d error(s)", label, checked.Summary.Errors)).
			Code(diag.CodeTypeMismatch).
			Primary(fileStartSpan(src), "selfhost checker summary").
			Note(fmt.Sprintf(
				"selfhost accepted %d of %d assignment/return/call checks",
				checked.Summary.Accepted,
				checked.Summary.Assignments,
			)).
			Build(),
	}
}

type selfhostSpanKey struct {
	start int
	end   int
}

type selfhostNameSpanKey struct {
	selfhostSpanKey
	name string
}

type selfhostSpanIndex struct {
	exprs       map[selfhostSpanKey]ast.Expr
	exprsByFrom map[int][]ast.Expr
	exprKeys    map[ast.Expr]selfhostSpanKey
	calls       map[selfhostSpanKey]*ast.CallExpr
	callsByFrom map[int][]*ast.CallExpr
	callKeys    map[*ast.CallExpr]selfhostSpanKey
	scopes      map[selfhostSpanKey]*resolve.Scope
	bindings    map[selfhostNameSpanKey]ast.Node
	symbols     map[selfhostNameSpanKey]*resolve.Symbol
}

func overlaySelfhostResult(result *Result, src selfhostCheckedSource, checked selfhost.CheckResult) {
	if result == nil {
		return
	}
	idx := buildSelfhostSpanIndex(src)
	for _, node := range checked.TypedNodes {
		key := selfhostSpanKey{start: node.Start, end: node.End}
		expr := idx.lookupExpr(key, node.Kind)
		if expr == nil {
			continue
		}
		t := parseSelfhostTypeName(node.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		result.Types[expr] = t
	}
	for _, binding := range checked.Bindings {
		key := selfhostSpanKey{start: binding.Start, end: binding.End}
		t := parseSelfhostTypeName(binding.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: binding.Name}
		if n := idx.bindings[nameKey]; n != nil {
			result.LetTypes[n] = t
		}
		if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, symbol := range checked.Symbols {
		key := selfhostSpanKey{start: symbol.Start, end: symbol.End}
		t := parseSelfhostTypeName(symbol.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: symbol.Name}
		if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, inst := range checked.Instantiations {
		key := selfhostSpanKey{start: inst.Start, end: inst.End}
		call := idx.lookupCall(key)
		if call == nil || len(inst.TypeArgs) == 0 {
			continue
		}
		args := make([]types.Type, 0, len(inst.TypeArgs))
		for _, name := range inst.TypeArgs {
			if t := parseSelfhostTypeName(name, idx.scopeFor(key)); t != nil {
				args = append(args, t)
			}
		}
		if len(args) == len(inst.TypeArgs) {
			result.Instantiations[call] = args
		}
	}
}

func buildSelfhostSpanIndex(src selfhostCheckedSource) *selfhostSpanIndex {
	idx := &selfhostSpanIndex{
		exprs:       map[selfhostSpanKey]ast.Expr{},
		exprsByFrom: map[int][]ast.Expr{},
		exprKeys:    map[ast.Expr]selfhostSpanKey{},
		calls:       map[selfhostSpanKey]*ast.CallExpr{},
		callsByFrom: map[int][]*ast.CallExpr{},
		callKeys:    map[*ast.CallExpr]selfhostSpanKey{},
		scopes:      map[selfhostSpanKey]*resolve.Scope{},
		bindings:    map[selfhostNameSpanKey]ast.Node{},
		symbols:     map[selfhostNameSpanKey]*resolve.Symbol{},
	}
	for _, file := range src.files {
		if file.file == nil {
			continue
		}
		for _, decl := range file.file.Decls {
			idx.addNode(decl, file.base, file.scope)
		}
		for _, stmt := range file.file.Stmts {
			idx.addNode(stmt, file.base, file.scope)
		}
		idx.addScopeTreeSymbols(file.scope, file.base)
		idx.addTopLevelDeclSymbols(file.file, file.scope, file.base)
		for _, sym := range file.refs {
			idx.addSymbol(sym, file.base, file.scope)
		}
	}
	return idx
}

func (idx *selfhostSpanIndex) scopeFor(key selfhostSpanKey) *resolve.Scope {
	if scope := idx.scopes[key]; scope != nil {
		return scope
	}
	var best *resolve.Scope
	bestSize := int(^uint(0) >> 1)
	for span, scope := range idx.scopes {
		if scope == nil {
			continue
		}
		if span.start > key.start || span.end < key.end {
			continue
		}
		size := span.end - span.start
		if size < bestSize {
			best = scope
			bestSize = size
		}
	}
	return best
}

func (idx *selfhostSpanIndex) addScopeTreeSymbols(scope *resolve.Scope, base int) {
	if scope == nil {
		return
	}
	for _, sym := range scope.Symbols() {
		idx.addSymbol(sym, base, scope)
	}
	for _, child := range scope.Children() {
		idx.addScopeTreeSymbols(child, base)
	}
}

func (idx *selfhostSpanIndex) addTopLevelDeclSymbols(file *ast.File, scope *resolve.Scope, base int) {
	if file == nil || scope == nil {
		return
	}
	for _, decl := range file.Decls {
		switch n := decl.(type) {
		case *ast.FnDecl:
			idx.addVisibleSymbolForNode(scope, n.Name, n, base)
		case *ast.StructDecl:
			idx.addVisibleSymbolForNode(scope, n.Name, n, base)
		case *ast.EnumDecl:
			idx.addVisibleSymbolForNode(scope, n.Name, n, base)
			for _, variant := range n.Variants {
				idx.addVisibleSymbolForNode(scope, variant.Name, variant, base)
			}
		case *ast.InterfaceDecl:
			idx.addVisibleSymbolForNode(scope, n.Name, n, base)
		case *ast.TypeAliasDecl:
			idx.addVisibleSymbolForNode(scope, n.Name, n, base)
		case *ast.LetDecl:
			idx.addVisibleSymbolForNode(scope, n.Name, n, base)
		}
	}
}

func (idx *selfhostSpanIndex) addVisibleSymbolForNode(scope *resolve.Scope, name string, node ast.Node, base int) {
	if name == "" || node == nil {
		return
	}
	for cur := scope; cur != nil; cur = cur.Parent() {
		sym := cur.LookupLocal(name)
		if sym == nil {
			continue
		}
		idx.addSymbolForNode(sym, node, name, base, scope)
		return
	}
}

func (idx *selfhostSpanIndex) addNode(n ast.Node, base int, scope *resolve.Scope) {
	if n == nil {
		return
	}
	key, haveKey := spanKeyForNode(n, base)
	if haveKey {
		if _, ok := idx.scopes[key]; !ok {
			idx.scopes[key] = scope
		}
		if e, ok := n.(ast.Expr); ok {
			if _, have := idx.exprs[key]; !have {
				idx.exprs[key] = e
			}
			idx.exprsByFrom[key.start] = append(idx.exprsByFrom[key.start], e)
			idx.exprKeys[e] = key
			if c, ok := e.(*ast.CallExpr); ok {
				idx.calls[key] = c
				idx.callsByFrom[key.start] = append(idx.callsByFrom[key.start], c)
				idx.callKeys[c] = key
			}
		}
	}
	switch v := n.(type) {
	case *ast.FnDecl:
		if v.Recv != nil {
			idx.addNode(v.Recv, base, scope)
		}
		for _, g := range v.Generics {
			idx.addNode(g, base, scope)
		}
		for _, p := range v.Params {
			idx.addNode(p, base, scope)
		}
		if v.ReturnType != nil {
			idx.addNode(v.ReturnType, base, scope)
		}
		if v.Body != nil {
			idx.addNode(v.Body, base, scope)
		}
	case *ast.StructDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope)
		}
		for _, f := range v.Fields {
			idx.addNode(f, base, scope)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope)
		}
	case *ast.EnumDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope)
		}
		for _, variant := range v.Variants {
			idx.addNode(variant, base, scope)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope)
		}
	case *ast.InterfaceDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope)
		}
		for _, sup := range v.Extends {
			idx.addNode(sup, base, scope)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope)
		}
	case *ast.TypeAliasDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope)
		}
		if v.Target != nil {
			idx.addNode(v.Target, base, scope)
		}
	case *ast.LetDecl:
		if v.Type != nil {
			idx.addNode(v.Type, base, scope)
		}
		if v.Value != nil {
			idx.addNode(v.Value, base, scope)
		}
	case *ast.UseDecl:
		for _, d := range v.GoBody {
			idx.addNode(d, base, scope)
		}
	case *ast.Field:
		if v.Type != nil {
			idx.addNode(v.Type, base, scope)
		}
		if v.Default != nil {
			idx.addNode(v.Default, base, scope)
		}
	case *ast.Variant:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope)
		}
	case *ast.Param:
		if haveKey && v.Name != "" {
			idx.bindings[selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}] = v
		}
		if v.Pattern != nil {
			idx.addNode(v.Pattern, base, scope)
		}
		if v.Type != nil {
			idx.addNode(v.Type, base, scope)
		}
		if v.Default != nil {
			idx.addNode(v.Default, base, scope)
		}
	case *ast.GenericParam:
		for _, con := range v.Constraints {
			idx.addNode(con, base, scope)
		}
	case *ast.NamedType:
		for _, a := range v.Args {
			idx.addNode(a, base, scope)
		}
	case *ast.OptionalType:
		idx.addNode(v.Inner, base, scope)
	case *ast.TupleType:
		for _, elem := range v.Elems {
			idx.addNode(elem, base, scope)
		}
	case *ast.FnType:
		for _, p := range v.Params {
			idx.addNode(p, base, scope)
		}
		if v.ReturnType != nil {
			idx.addNode(v.ReturnType, base, scope)
		}
	case *ast.Block:
		for _, s := range v.Stmts {
			idx.addNode(s, base, scope)
		}
	case *ast.LetStmt:
		if name := bindingPatternName(v.Pattern); name != "" {
			if patKey, ok := spanKeyForNode(v.Pattern, base); ok {
				idx.bindings[selfhostNameSpanKey{selfhostSpanKey: patKey, name: name}] = v
			}
		}
		if v.Pattern != nil {
			idx.addNode(v.Pattern, base, scope)
		}
		if v.Type != nil {
			idx.addNode(v.Type, base, scope)
		}
		if v.Value != nil {
			idx.addNode(v.Value, base, scope)
		}
	case *ast.ExprStmt:
		idx.addNode(v.X, base, scope)
	case *ast.AssignStmt:
		for _, t := range v.Targets {
			idx.addNode(t, base, scope)
		}
		idx.addNode(v.Value, base, scope)
	case *ast.ReturnStmt:
		idx.addNode(v.Value, base, scope)
	case *ast.ChanSendStmt:
		idx.addNode(v.Channel, base, scope)
		idx.addNode(v.Value, base, scope)
	case *ast.DeferStmt:
		idx.addNode(v.X, base, scope)
	case *ast.ForStmt:
		idx.addNode(v.Pattern, base, scope)
		idx.addNode(v.Iter, base, scope)
		idx.addNode(v.Body, base, scope)
	case *ast.UnaryExpr:
		idx.addNode(v.X, base, scope)
	case *ast.BinaryExpr:
		idx.addNode(v.Left, base, scope)
		idx.addNode(v.Right, base, scope)
	case *ast.QuestionExpr:
		idx.addNode(v.X, base, scope)
	case *ast.CallExpr:
		idx.addNode(v.Fn, base, scope)
		for _, a := range v.Args {
			idx.addNode(a, base, scope)
		}
	case *ast.Arg:
		idx.addNode(v.Value, base, scope)
	case *ast.FieldExpr:
		idx.addNode(v.X, base, scope)
	case *ast.IndexExpr:
		idx.addNode(v.X, base, scope)
		idx.addNode(v.Index, base, scope)
	case *ast.TurbofishExpr:
		idx.addNode(v.Base, base, scope)
		for _, a := range v.Args {
			idx.addNode(a, base, scope)
		}
	case *ast.RangeExpr:
		idx.addNode(v.Start, base, scope)
		idx.addNode(v.Stop, base, scope)
	case *ast.ParenExpr:
		idx.addNode(v.X, base, scope)
	case *ast.TupleExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope)
		}
	case *ast.ListExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope)
		}
	case *ast.MapExpr:
		for _, e := range v.Entries {
			idx.addNode(e, base, scope)
		}
	case *ast.MapEntry:
		idx.addNode(v.Key, base, scope)
		idx.addNode(v.Value, base, scope)
	case *ast.StructLit:
		idx.addNode(v.Type, base, scope)
		for _, f := range v.Fields {
			idx.addNode(f, base, scope)
		}
		idx.addNode(v.Spread, base, scope)
	case *ast.StructLitField:
		idx.addNode(v.Value, base, scope)
	case *ast.IfExpr:
		idx.addNode(v.Pattern, base, scope)
		idx.addNode(v.Cond, base, scope)
		idx.addNode(v.Then, base, scope)
		idx.addNode(v.Else, base, scope)
	case *ast.MatchExpr:
		idx.addNode(v.Scrutinee, base, scope)
		for _, a := range v.Arms {
			idx.addNode(a, base, scope)
		}
	case *ast.MatchArm:
		idx.addNode(v.Pattern, base, scope)
		idx.addNode(v.Guard, base, scope)
		idx.addNode(v.Body, base, scope)
	case *ast.ClosureExpr:
		for _, p := range v.Params {
			idx.addNode(p, base, scope)
		}
		idx.addNode(v.ReturnType, base, scope)
		idx.addNode(v.Body, base, scope)
	case *ast.LiteralPat:
		idx.addNode(v.Literal, base, scope)
	case *ast.IdentPat:
		if haveKey && v.Name != "" {
			idx.bindings[selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}] = v
		}
	case *ast.TuplePat:
		for _, p := range v.Elems {
			idx.addNode(p, base, scope)
		}
	case *ast.StructPat:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope)
		}
	case *ast.StructPatField:
		idx.addNode(v.Pattern, base, scope)
	case *ast.VariantPat:
		for _, a := range v.Args {
			idx.addNode(a, base, scope)
		}
	case *ast.RangePat:
		idx.addNode(v.Start, base, scope)
		idx.addNode(v.Stop, base, scope)
	case *ast.OrPat:
		for _, a := range v.Alts {
			idx.addNode(a, base, scope)
		}
	case *ast.BindingPat:
		if haveKey && v.Name != "" {
			idx.bindings[selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}] = v
		}
		idx.addNode(v.Pattern, base, scope)
	}
}

func (idx *selfhostSpanIndex) lookupExpr(key selfhostSpanKey, kind string) ast.Expr {
	if expr := idx.exprs[key]; expr != nil {
		return expr
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
	for expr, exprKey := range idx.exprKeys {
		if kind != "" && selfhostExprKind(expr) != kind {
			continue
		}
		if exprKey.start > key.start || exprKey.end < key.end {
			continue
		}
		size := exprKey.end - exprKey.start
		if size < bestSize {
			best = expr
			bestSize = size
		}
	}
	return best
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
	for call, callKey := range idx.callKeys {
		if callKey.start > key.start || callKey.end < key.end {
			continue
		}
		size := callKey.end - callKey.start
		if size < bestSize {
			best = call
			bestSize = size
		}
	}
	return best
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

func (idx *selfhostSpanIndex) addSymbol(sym *resolve.Symbol, base int, scope *resolve.Scope) {
	if sym == nil || sym.Decl == nil || sym.Name == "" {
		return
	}
	idx.addSymbolForNode(sym, sym.Decl, sym.Name, base, scope)
}

func (idx *selfhostSpanIndex) addSymbolForNode(sym *resolve.Symbol, node ast.Node, name string, base int, scope *resolve.Scope) {
	if sym == nil || node == nil || name == "" {
		return
	}
	key, ok := spanKeyForNode(node, base)
	if !ok {
		return
	}
	idx.symbols[selfhostNameSpanKey{selfhostSpanKey: key, name: name}] = sym
	if _, ok := idx.scopes[key]; !ok {
		idx.scopes[key] = scope
	}
}

func spanKeyForNode(n ast.Node, base int) (key selfhostSpanKey, ok bool) {
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

func parseSelfhostTypeName(raw string, scope *resolve.Scope) types.Type {
	text := strings.TrimSpace(raw)
	switch text {
	case "", "Invalid":
		return types.ErrorType
	case "()", "Unit":
		return types.Unit
	case "Never":
		return types.Never
	case "UntypedInt":
		return types.UntypedIntVal
	case "UntypedFloat":
		return types.UntypedFloatVal
	}
	if strings.HasSuffix(text, "?") {
		inner := parseSelfhostTypeName(strings.TrimSuffix(text, "?"), scope)
		return &types.Optional{Inner: inner}
	}
	if strings.HasPrefix(text, "fn(") {
		return parseSelfhostFnType(text, scope)
	}
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "("), ")"))
		if inner == "" {
			return types.Unit
		}
		parts := splitSelfhostTypeList(inner)
		if len(parts) == 1 {
			return parseSelfhostTypeName(parts[0], scope)
		}
		elems := make([]types.Type, 0, len(parts))
		for _, part := range parts {
			elems = append(elems, parseSelfhostTypeName(part, scope))
		}
		return &types.Tuple{Elems: elems}
	}
	head, argText, hasArgs := splitSelfhostGeneric(text)
	if p := types.PrimitiveByName(head); p != nil && !hasArgs {
		return p
	}
	if head == "Option" && hasArgs {
		args := splitSelfhostTypeList(argText)
		if len(args) == 1 {
			return &types.Optional{Inner: parseSelfhostTypeName(args[0], scope)}
		}
	}
	args := []types.Type(nil)
	if hasArgs {
		for _, part := range splitSelfhostTypeList(argText) {
			args = append(args, parseSelfhostTypeName(part, scope))
		}
	}
	sym := lookupSelfhostTypeSymbol(head, scope)
	if sym.Kind == resolve.SymGeneric {
		return &types.TypeVar{Sym: sym}
	}
	return &types.Named{Sym: sym, Args: args}
}

func parseSelfhostFnType(text string, scope *resolve.Scope) types.Type {
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return types.ErrorType
	}
	close := matchingSelfhostParen(text, open)
	if close < 0 {
		return types.ErrorType
	}
	paramText := strings.TrimSpace(text[open+1 : close])
	var params []types.Type
	if paramText != "" {
		for _, part := range splitSelfhostTypeList(paramText) {
			params = append(params, parseSelfhostTypeName(part, scope))
		}
	}
	ret := types.Type(types.Unit)
	rest := strings.TrimSpace(text[close+1:])
	if strings.HasPrefix(rest, "->") {
		ret = parseSelfhostTypeName(strings.TrimSpace(strings.TrimPrefix(rest, "->")), scope)
	}
	return &types.FnType{Params: params, Return: ret}
}

func splitSelfhostGeneric(text string) (head, args string, ok bool) {
	depth := 0
	start := -1
	for i, r := range text {
		switch r {
		case '<':
			if depth == 0 {
				start = i
			}
			depth++
		case '>':
			depth--
			if depth == 0 && i == len(text)-1 && start >= 0 {
				return strings.TrimSpace(text[:start]), strings.TrimSpace(text[start+1 : i]), true
			}
		}
	}
	return strings.TrimSpace(text), "", false
}

func splitSelfhostTypeList(text string) []string {
	var out []string
	start := 0
	angle := 0
	paren := 0
	for i, r := range text {
		switch r {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case ',':
			if angle == 0 && paren == 0 {
				part := strings.TrimSpace(text[start:i])
				if part != "" {
					out = append(out, part)
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(text[start:]); part != "" {
		out = append(out, part)
	}
	return out
}

func matchingSelfhostParen(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
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
	case "List", "Map", "Set", "Option", "Result", "Error", "Equal", "Ordered", "Hashable", "Chan", "Channel", "Handle", "TaskGroup", "Iter":
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

func fileStartSpan(src []byte) diag.Span {
	start := token.Pos{Line: 1, Column: 1, Offset: 0}
	end := start
	if len(src) > 0 {
		end = token.Pos{Line: 1, Column: 2, Offset: 1}
	}
	return diag.Span{Start: start, End: end}
}

func selfhostFileSource(file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var b bytes.Buffer
	writeSelfhostImports(&b, nil, stdlib, fileUses(file))
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	base := b.Len()
	b.Write(src)
	if !bytes.HasSuffix(src, []byte("\n")) {
		b.WriteByte('\n')
	}
	var scope *resolve.Scope
	var refs map[*ast.Ident]*resolve.Symbol
	if rr != nil {
		scope = rr.FileScope
		refs = rr.Refs
	}
	return selfhostCheckedSource{
		source: b.Bytes(),
		files: []selfhostFileSegment{{
			file:  file,
			scope: scope,
			refs:  refs,
			base:  base,
		}},
	}
}

func selfhostPackageSource(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var b bytes.Buffer
	writeSelfhostPackageImports(&b, pkg, ws, stdlib)
	var files []selfhostFileSegment
	for _, pf := range pkg.Files {
		if len(pf.Source) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		base := b.Len()
		b.Write(pf.Source)
		if !bytes.HasSuffix(pf.Source, []byte("\n")) {
			b.WriteByte('\n')
		}
		files = append(files, selfhostFileSegment{
			file:  pf.File,
			scope: pf.FileScope,
			refs:  pf.Refs,
			base:  base,
		})
	}
	return selfhostCheckedSource{source: b.Bytes(), files: files}
}

func writeSelfhostPackageImports(b *bytes.Buffer, pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	if pkg == nil {
		return
	}
	var uses []*ast.UseDecl
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		uses = append(uses, pf.File.Uses...)
	}
	writeSelfhostImports(b, ws, stdlib, uses)
}

func writeSelfhostImports(b *bytes.Buffer, ws *resolve.Workspace, stdlib resolve.StdlibProvider, uses []*ast.UseDecl) {
	seen := map[string]bool{}
	for _, use := range uses {
		dotPath := strings.Join(use.Path, ".")
		target := (*resolve.Package)(nil)
		if ws != nil {
			target = ws.Packages[dotPath]
			if target == nil && ws.Stdlib != nil {
				target = ws.Stdlib.LookupPackage(dotPath)
			}
		}
		if target == nil && stdlib != nil {
			target = stdlib.LookupPackage(dotPath)
		}
		if target == nil {
			continue
		}
		alias := use.Alias
		if alias == "" && len(use.Path) > 0 {
			alias = use.Path[len(use.Path)-1]
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		writeSelfhostPackageImport(b, alias, target)
	}
}

func fileUses(file *ast.File) []*ast.UseDecl {
	if file == nil {
		return nil
	}
	return file.Uses
}

func writeSelfhostPackageImport(b *bytes.Buffer, alias string, pkg *resolve.Package) {
	var body bytes.Buffer
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || !fn.Pub || fn.Recv != nil {
				continue
			}
			fmt.Fprintf(&body, "    fn %s(", fn.Name)
			for i, param := range fn.Params {
				if i > 0 {
					body.WriteString(", ")
				}
				name := param.Name
				if name == "" {
					name = fmt.Sprintf("arg%d", i)
				}
				fmt.Fprintf(&body, "%s: %s", name, selfhostTypeSource(param.Type))
			}
			body.WriteByte(')')
			if ret := selfhostTypeSource(fn.ReturnType); ret != "()" {
				fmt.Fprintf(&body, " -> %s", ret)
			}
			body.WriteByte('\n')
		}
	}
	if body.Len() == 0 {
		return
	}
	fmt.Fprintf(b, "use go %q as %s {\n", alias, alias)
	b.Write(body.Bytes())
	b.WriteString("}\n")
}

func selfhostTypeSource(t ast.Type) string {
	switch x := t.(type) {
	case nil:
		return "()"
	case *ast.NamedType:
		name := strings.Join(x.Path, ".")
		if name == "" {
			name = "Invalid"
		}
		if len(x.Args) == 0 {
			return name
		}
		args := make([]string, 0, len(x.Args))
		for _, arg := range x.Args {
			args = append(args, selfhostTypeSource(arg))
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *ast.OptionalType:
		return selfhostTypeSource(x.Inner) + "?"
	case *ast.TupleType:
		elems := make([]string, 0, len(x.Elems))
		for _, elem := range x.Elems {
			elems = append(elems, selfhostTypeSource(elem))
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case *ast.FnType:
		params := make([]string, 0, len(x.Params))
		for _, param := range x.Params {
			params = append(params, selfhostTypeSource(param))
		}
		out := "fn(" + strings.Join(params, ", ") + ")"
		if x.ReturnType != nil {
			out += " -> " + selfhostTypeSource(x.ReturnType)
		}
		return out
	default:
		return "Invalid"
	}
}
