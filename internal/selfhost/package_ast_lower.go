package selfhost

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

type selfhostLoweringUnsupported struct {
	reason string
}

func (e *selfhostLoweringUnsupported) Error() string {
	if e == nil || e.reason == "" {
		return "selfhost package adapter: unsupported AST shape"
	}
	return "selfhost package adapter: unsupported AST shape: " + e.reason
}

type selfhostPackageByteFile struct {
	base       int
	end        int
	name       string
	source     []byte
	lineStarts []int
}

type selfhostPackageByteLayout struct {
	files []selfhostPackageByteFile
}

func selfhostCanBuildPackageAstDirect(files []PackageCheckFile) bool {
	haveFile := false
	for _, file := range files {
		if len(file.Source) == 0 {
			continue
		}
		if file.File == nil {
			return false
		}
		haveFile = true
	}
	return haveFile
}

func selfhostBuildPackageAstDirect(files []PackageCheckFile) (*AstFile, *selfhostPackageByteLayout, error) {
	arena := emptyAstArena()
	layout := newSelfhostPackageByteLayout(files)
	haveFile := false
	for _, file := range files {
		if len(file.Source) == 0 {
			continue
		}
		if file.File == nil {
			return nil, nil, &selfhostLoweringUnsupported{reason: "missing public AST on package file"}
		}
		lowerer := selfhostPackageFileLowerer{
			file:  file,
			arena: arena,
		}
		if err := lowerer.lowerFile(file.File); err != nil {
			return nil, nil, err
		}
		haveFile = true
	}
	if !haveFile {
		return nil, layout, nil
	}
	return &AstFile{arena: arena}, layout, nil
}

func newSelfhostPackageByteLayout(files []PackageCheckFile) *selfhostPackageByteLayout {
	layout := &selfhostPackageByteLayout{}
	for _, file := range files {
		if len(file.Source) == 0 {
			continue
		}
		layout.files = append(layout.files, selfhostPackageByteFile{
			base:       file.Base,
			end:        file.Base + len(file.Source),
			name:       file.Name,
			source:     append([]byte(nil), file.Source...),
			lineStarts: selfhostComputeLineStarts(file.Source),
		})
	}
	sort.Slice(layout.files, func(i, j int) bool {
		return layout.files[i].base < layout.files[j].base
	})
	return layout
}

func selfhostComputeLineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func selfhostBytePosLookup(layout *selfhostPackageByteLayout) selfhostTokenPos {
	if layout == nil {
		return nil
	}
	return func(offset int) (string, int, int, bool) {
		file := selfhostByteFileForOffset(layout, offset)
		if file == nil {
			return "", 0, 0, false
		}
		rel := offset - file.base
		if rel < 0 || rel > len(file.source) {
			return "", 0, 0, false
		}
		lineIdx := sort.Search(len(file.lineStarts), func(i int) bool {
			return file.lineStarts[i] > rel
		}) - 1
		if lineIdx < 0 {
			lineIdx = 0
		}
		lineStart := file.lineStarts[lineIdx]
		col := 1 + utf8.RuneCount(file.source[lineStart:rel])
		return file.name, lineIdx + 1, col, true
	}
}

func selfhostByteFileForOffset(layout *selfhostPackageByteLayout, offset int) *selfhostPackageByteFile {
	if layout == nil || len(layout.files) == 0 {
		return nil
	}
	i := sort.Search(len(layout.files), func(i int) bool {
		return layout.files[i].base > offset
	}) - 1
	if i < 0 || i >= len(layout.files) {
		return nil
	}
	file := &layout.files[i]
	if offset < file.base || offset > file.end {
		return nil
	}
	return file
}

func adaptCheckResultWithByteLayout(checked *FrontCheckResult, layout *selfhostPackageByteLayout) CheckResult {
	if checked == nil {
		return CheckResult{}
	}
	result := CheckResult{
		Summary:        adaptCheckSummaryWithContext(checked, selfhostBytePosLookup(layout)),
		TypedNodes:     make([]CheckedNode, 0, len(checked.typedNodes)),
		Bindings:       make([]CheckedBinding, 0, len(checked.bindings)),
		Symbols:        make([]CheckedSymbol, 0, len(checked.symbols)),
		Instantiations: make([]CheckInstantiation, 0, len(checked.instantiations)),
	}
	for _, node := range checked.typedNodes {
		if node == nil {
			continue
		}
		result.TypedNodes = append(result.TypedNodes, CheckedNode{
			Node:     node.node,
			Kind:     node.kind,
			TypeName: node.typeName,
			Start:    node.start,
			End:      node.end,
		})
	}
	for _, binding := range checked.bindings {
		if binding == nil {
			continue
		}
		result.Bindings = append(result.Bindings, CheckedBinding{
			Node:     binding.node,
			Name:     binding.name,
			TypeName: binding.typeName,
			Mutable:  binding.mutable,
			Start:    binding.start,
			End:      binding.end,
		})
	}
	for _, symbol := range checked.symbols {
		if symbol == nil {
			continue
		}
		result.Symbols = append(result.Symbols, CheckedSymbol{
			Node:     symbol.node,
			Kind:     symbol.kind,
			Name:     symbol.name,
			Owner:    symbol.owner,
			TypeName: symbol.typeName,
			Start:    symbol.start,
			End:      symbol.end,
		})
	}
	for _, inst := range checked.instantiations {
		if inst == nil {
			continue
		}
		result.Instantiations = append(result.Instantiations, CheckInstantiation{
			Node:       inst.node,
			Callee:     inst.callee,
			TypeArgs:   append([]string(nil), inst.typeArgs...),
			ResultType: inst.resultType,
			Start:      inst.start,
			End:        inst.end,
		})
	}
	return result
}

type selfhostPackageFileLowerer struct {
	file  PackageCheckFile
	arena *AstArena
}

func (l *selfhostPackageFileLowerer) lowerFile(file *ast.File) error {
	if file == nil {
		return nil
	}
	if len(file.Stmts) > 0 {
		return &selfhostLoweringUnsupported{reason: "script top-level statements"}
	}
	for _, decl := range file.Decls {
		idx, err := l.lowerDecl(decl)
		if err != nil {
			return err
		}
		if idx >= 0 {
			l.arena.decls = append(l.arena.decls, idx)
		}
	}
	return nil
}

func (l *selfhostPackageFileLowerer) lowerDecl(decl ast.Decl) (int, error) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		return l.lowerFnDecl(d)
	case *ast.StructDecl:
		return l.lowerStructDecl(d)
	case *ast.EnumDecl:
		return l.lowerEnumDecl(d)
	case *ast.InterfaceDecl:
		return l.lowerInterfaceDecl(d)
	case *ast.TypeAliasDecl:
		return l.lowerTypeAliasDecl(d)
	case *ast.LetDecl:
		return l.lowerLetDecl(d)
	case *ast.UseDecl:
		return -1, nil
	default:
		return -1, &selfhostLoweringUnsupported{reason: fmt.Sprintf("decl %T", decl)}
	}
}

func (l *selfhostPackageFileLowerer) lowerFnDecl(fn *ast.FnDecl) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNFnDecl{}), fn)
	if err != nil {
		return -1, err
	}
	node.text = fn.Name
	node.flags = boolToInt(fn.Pub)
	node.left, err = l.lowerType(fn.ReturnType)
	if err != nil {
		return -1, err
	}
	node.right, err = l.lowerBlock(fn.Body)
	if err != nil {
		return -1, err
	}
	if fn.Recv != nil {
		recvIdx, err := l.lowerReceiver(fn.Recv)
		if err != nil {
			return -1, err
		}
		node.children = append(node.children, recvIdx)
	}
	for _, param := range fn.Params {
		idx, err := l.lowerParam(param)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	children2, err := l.lowerGenericParams(fn.Generics)
	if err != nil {
		return -1, err
	}
	node.children2 = children2
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerStructDecl(decl *ast.StructDecl) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNStructDecl{}), decl)
	if err != nil {
		return -1, err
	}
	node.text = decl.Name
	node.flags = boolToInt(decl.Pub)
	children2, err := l.lowerGenericParams(decl.Generics)
	if err != nil {
		return -1, err
	}
	node.children2 = children2
	for _, field := range decl.Fields {
		idx, err := l.lowerFieldDecl(field)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	for _, method := range decl.Methods {
		idx, err := l.lowerFnDecl(method)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerEnumDecl(decl *ast.EnumDecl) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNEnumDecl{}), decl)
	if err != nil {
		return -1, err
	}
	node.text = decl.Name
	node.flags = boolToInt(decl.Pub)
	children2, err := l.lowerGenericParams(decl.Generics)
	if err != nil {
		return -1, err
	}
	node.children2 = children2
	for _, variant := range decl.Variants {
		idx, err := l.lowerVariantDecl(variant)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	for _, method := range decl.Methods {
		idx, err := l.lowerFnDecl(method)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerInterfaceDecl(decl *ast.InterfaceDecl) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNInterfaceDecl{}), decl)
	if err != nil {
		return -1, err
	}
	node.text = decl.Name
	node.flags = boolToInt(decl.Pub)
	children2, err := l.lowerGenericParams(decl.Generics)
	if err != nil {
		return -1, err
	}
	node.children2 = children2
	for _, ext := range decl.Extends {
		idx, err := l.lowerType(ext)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	for _, method := range decl.Methods {
		idx, err := l.lowerFnDecl(method)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerTypeAliasDecl(decl *ast.TypeAliasDecl) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNTypeAlias{}), decl)
	if err != nil {
		return -1, err
	}
	node.text = decl.Name
	node.flags = boolToInt(decl.Pub)
	node.left, err = l.lowerType(decl.Target)
	if err != nil {
		return -1, err
	}
	children, err := l.lowerGenericParams(decl.Generics)
	if err != nil {
		return -1, err
	}
	node.children = children
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerLetDecl(decl *ast.LetDecl) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNLetDecl{}), decl)
	if err != nil {
		return -1, err
	}
	node.flags = boolToInt(decl.Mut)
	patIdx, err := l.lowerPattern(&ast.IdentPat{
		PosV: decl.PosV,
		EndV: decl.EndV,
		Name: decl.Name,
	})
	if err != nil {
		return -1, err
	}
	node.left = patIdx
	node.right, err = l.lowerExpr(decl.Value)
	if err != nil {
		return -1, err
	}
	if decl.Type != nil {
		typeIdx, err := l.lowerType(decl.Type)
		if err != nil {
			return -1, err
		}
		if typeIdx >= 0 {
			node.children = append(node.children, typeIdx)
		}
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerReceiver(recv *ast.Receiver) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNParam{}), recv)
	if err != nil {
		return -1, err
	}
	node.text = "self"
	node.flags = boolToInt(recv.Mut)
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerParam(param *ast.Param) (int, error) {
	if param == nil {
		return -1, nil
	}
	if param.Pattern != nil {
		return -1, &selfhostLoweringUnsupported{reason: "closure destructuring params"}
	}
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNParam{}), param)
	if err != nil {
		return -1, err
	}
	node.text = param.Name
	node.right, err = l.lowerType(param.Type)
	if err != nil {
		return -1, err
	}
	node.left, err = l.lowerExpr(param.Default)
	if err != nil {
		return -1, err
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerGenericParams(params []*ast.GenericParam) ([]int, error) {
	var out []int
	for _, param := range params {
		if param == nil {
			continue
		}
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNGenericParam{}), param)
		if err != nil {
			return nil, err
		}
		node.text = param.Name
		for _, bound := range param.Constraints {
			idx, err := l.lowerType(bound)
			if err != nil {
				return nil, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		out = append(out, astArenaAdd(l.arena, node))
	}
	return out, nil
}

func (l *selfhostPackageFileLowerer) lowerFieldDecl(field *ast.Field) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNField_{}), field)
	if err != nil {
		return -1, err
	}
	node.text = field.Name
	node.right, err = l.lowerType(field.Type)
	if err != nil {
		return -1, err
	}
	node.left, err = l.lowerExpr(field.Default)
	if err != nil {
		return -1, err
	}
	node.flags = boolToInt(field.Pub)
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerVariantDecl(variant *ast.Variant) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNVariant{}), variant)
	if err != nil {
		return -1, err
	}
	node.text = variant.Name
	for i, fieldTy := range variant.Fields {
		child, err := l.newNodeForSpan(AstNodeKind(&AstNodeKind_AstNField_{}), node.start, node.end)
		if err != nil {
			return -1, err
		}
		child.text = fmt.Sprintf("%d", i)
		child.right, err = l.lowerType(fieldTy)
		if err != nil {
			return -1, err
		}
		node.children = append(node.children, astArenaAdd(l.arena, child))
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerBlock(block *ast.Block) (int, error) {
	if block == nil {
		return -1, nil
	}
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNBlock{}), block)
	if err != nil {
		return -1, err
	}
	for _, stmt := range block.Stmts {
		idx, err := l.lowerStmt(stmt)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerStmt(stmt ast.Stmt) (int, error) {
	switch s := stmt.(type) {
	case *ast.Block:
		return l.lowerBlock(s)
	case *ast.LetStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNLet{}), s)
		if err != nil {
			return -1, err
		}
		node.flags = boolToInt(s.Mut)
		node.left, err = l.lowerPattern(s.Pattern)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(s.Value)
		if err != nil {
			return -1, err
		}
		if s.Type != nil {
			typeIdx, err := l.lowerType(s.Type)
			if err != nil {
				return -1, err
			}
			if typeIdx >= 0 {
				node.children = append(node.children, typeIdx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ExprStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNExprStmt{}), s)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(s.X)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.AssignStmt:
		if len(s.Targets) != 1 {
			return -1, &selfhostLoweringUnsupported{reason: "multi-assign statement"}
		}
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNAssign{}), s)
		if err != nil {
			return -1, err
		}
		node.op, err = selfhostFrontTokenKind(s.Op)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(s.Targets[0])
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(s.Value)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ReturnStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNReturn{}), s)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(s.Value)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.BreakStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNBreak{}), s)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ContinueStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNContinue{}), s)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ChanSendStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNChanSend{}), s)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(s.Channel)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(s.Value)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.DeferStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNDefer{}), s)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(s.X)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ForStmt:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNFor{}), s)
		if err != nil {
			return -1, err
		}
		switch {
		case s.IsForLet:
			node.text = "forlet"
			node.left, err = l.lowerExpr(s.Iter)
		case s.Pattern != nil:
			node.text = "forin"
			patIdx, patErr := l.lowerPattern(s.Pattern)
			if patErr != nil {
				return -1, patErr
			}
			node.children = append(node.children, patIdx)
			node.left = -1
			iterIdx, iterErr := l.lowerExpr(s.Iter)
			if iterErr != nil {
				return -1, iterErr
			}
			node.children = append(node.children, iterIdx)
		case s.Iter == nil:
			node.text = "infinite"
			node.left = -1
		default:
			node.text = "cond"
			node.left, err = l.lowerExpr(s.Iter)
		}
		if err != nil {
			return -1, err
		}
		if s.IsForLet {
			patIdx, patErr := l.lowerPattern(s.Pattern)
			if patErr != nil {
				return -1, patErr
			}
			if patIdx >= 0 {
				node.children = append(node.children, patIdx)
			}
		}
		node.right, err = l.lowerBlock(s.Body)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	default:
		return -1, &selfhostLoweringUnsupported{reason: fmt.Sprintf("stmt %T", stmt)}
	}
}

func (l *selfhostPackageFileLowerer) lowerExpr(expr ast.Expr) (int, error) {
	switch e := expr.(type) {
	case nil:
		return -1, nil
	case *ast.Ident:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNIdent{}), e)
		if err != nil {
			return -1, err
		}
		node.text = e.Name
		return astArenaAdd(l.arena, node), nil
	case *ast.IntLit:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNIntLit{}), e)
		if err != nil {
			return -1, err
		}
		node.text = e.Text
		return astArenaAdd(l.arena, node), nil
	case *ast.FloatLit:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNFloatLit{}), e)
		if err != nil {
			return -1, err
		}
		node.text = e.Text
		return astArenaAdd(l.arena, node), nil
	case *ast.StringLit:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNStringLit{}), e)
		if err != nil {
			return -1, err
		}
		node.text = l.stringLiteralText(e)
		return astArenaAdd(l.arena, node), nil
	case *ast.BoolLit:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNBoolLit{}), e)
		if err != nil {
			return -1, err
		}
		if e.Value {
			node.text = "true"
		} else {
			node.text = "false"
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.CharLit:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNCharLit{}), e)
		if err != nil {
			return -1, err
		}
		node.text = l.literalSourceText(e)
		return astArenaAdd(l.arena, node), nil
	case *ast.ByteLit:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNByteLit{}), e)
		if err != nil {
			return -1, err
		}
		node.text = l.literalSourceText(e)
		return astArenaAdd(l.arena, node), nil
	case *ast.UnaryExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNUnary{}), e)
		if err != nil {
			return -1, err
		}
		node.op, err = selfhostFrontTokenKind(e.Op)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.X)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.BinaryExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNBinary{}), e)
		if err != nil {
			return -1, err
		}
		node.op, err = selfhostFrontTokenKind(e.Op)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.Left)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(e.Right)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.QuestionExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNQuestion{}), e)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.X)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.CallExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNCall{}), e)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.Fn)
		if err != nil {
			return -1, err
		}
		for _, arg := range e.Args {
			idx, err := l.lowerCallArg(arg)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.FieldExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNField{}), e)
		if err != nil {
			return -1, err
		}
		node.text = e.Name
		node.flags = boolToInt(e.IsOptional)
		node.left, err = l.lowerExpr(e.X)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.IndexExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNIndex{}), e)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.X)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(e.Index)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.TurbofishExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNTurbofish{}), e)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.Base)
		if err != nil {
			return -1, err
		}
		for _, arg := range e.Args {
			idx, err := l.lowerType(arg)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.RangeExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNRange{}), e)
		if err != nil {
			return -1, err
		}
		node.flags = boolToInt(e.Inclusive)
		node.left, err = l.lowerExpr(e.Start)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(e.Stop)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ParenExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNParen{}), e)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.X)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.TupleExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNTuple{}), e)
		if err != nil {
			return -1, err
		}
		for _, elem := range e.Elems {
			idx, err := l.lowerExpr(elem)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ListExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNList{}), e)
		if err != nil {
			return -1, err
		}
		for _, elem := range e.Elems {
			idx, err := l.lowerExpr(elem)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.MapExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNMap{}), e)
		if err != nil {
			return -1, err
		}
		node.flags = boolToInt(e.Empty && len(e.Entries) == 0)
		for _, entry := range e.Entries {
			idx, err := l.lowerMapEntry(entry)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.StructLit:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNStructLit{}), e)
		if err != nil {
			return -1, err
		}
		if name, ok := selfhostPathExprText(e.Type); ok {
			node.text = name
		}
		node.left, err = l.lowerStructOwnerExpr(e.Type)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(e.Spread)
		if err != nil {
			return -1, err
		}
		for _, field := range e.Fields {
			idx, err := l.lowerStructLitField(field)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.IfExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNIf{}), e)
		if err != nil {
			return -1, err
		}
		node.flags = boolToInt(e.IsIfLet)
		node.left, err = l.lowerExpr(e.Cond)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerBlock(e.Then)
		if err != nil {
			return -1, err
		}
		if e.Else != nil {
			idx, err := l.lowerExpr(e.Else)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		if e.Pattern != nil {
			idx, err := l.lowerPattern(e.Pattern)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.MatchExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNMatch{}), e)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.Scrutinee)
		if err != nil {
			return -1, err
		}
		for _, arm := range e.Arms {
			idx, err := l.lowerMatchArm(arm)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.ClosureExpr:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNClosure{}), e)
		if err != nil {
			return -1, err
		}
		for _, param := range e.Params {
			idx, err := l.lowerParam(param)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		node.right, err = l.lowerType(e.ReturnType)
		if err != nil {
			return -1, err
		}
		node.left, err = l.lowerExpr(e.Body)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.Block:
		return l.lowerBlock(e)
	default:
		return -1, &selfhostLoweringUnsupported{reason: fmt.Sprintf("expr %T", expr)}
	}
}

func (l *selfhostPackageFileLowerer) lowerCallArg(arg *ast.Arg) (int, error) {
	if arg == nil {
		return -1, nil
	}
	return l.lowerExpr(arg.Value)
}

func (l *selfhostPackageFileLowerer) lowerMapEntry(entry *ast.MapEntry) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNField_{}), entry)
	if err != nil {
		return -1, err
	}
	node.left, err = l.lowerExpr(entry.Key)
	if err != nil {
		return -1, err
	}
	node.right, err = l.lowerExpr(entry.Value)
	if err != nil {
		return -1, err
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerStructLitField(field *ast.StructLitField) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNField_{}), field)
	if err != nil {
		return -1, err
	}
	node.text = field.Name
	if field.Value != nil {
		node.left, err = l.lowerExpr(field.Value)
		if err != nil {
			return -1, err
		}
	} else {
		shorthand := &ast.Ident{PosV: field.PosV, EndV: field.End(), Name: field.Name}
		node.left, err = l.lowerExpr(shorthand)
		if err != nil {
			return -1, err
		}
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerMatchArm(arm *ast.MatchArm) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNMatchArm{}), arm)
	if err != nil {
		return -1, err
	}
	node.left, err = l.lowerPattern(arm.Pattern)
	if err != nil {
		return -1, err
	}
	node.right, err = l.lowerExpr(arm.Body)
	if err != nil {
		return -1, err
	}
	if arm.Guard != nil {
		idx, err := l.lowerExpr(arm.Guard)
		if err != nil {
			return -1, err
		}
		if idx >= 0 {
			node.children = append(node.children, idx)
		}
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerPattern(pattern ast.Pattern) (int, error) {
	switch p := pattern.(type) {
	case nil:
		return -1, nil
	case *ast.WildcardPat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternWildcardKind()
		return astArenaAdd(l.arena, node), nil
	case *ast.LiteralPat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternLiteralKind()
		node.text = l.patternLiteralText(p.Literal)
		node.left, err = l.lowerExpr(p.Literal)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.IdentPat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternIdentKind()
		node.text = p.Name
		return astArenaAdd(l.arena, node), nil
	case *ast.TuplePat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternTupleKind()
		for _, elem := range p.Elems {
			idx, err := l.lowerPattern(elem)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.StructPat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternStructKind()
		node.text = strings.Join(p.Type, ".")
		node.flags = boolToInt(p.Rest)
		for _, field := range p.Fields {
			idx, err := l.lowerStructPatternField(field)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.VariantPat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternVariantKind()
		node.text = strings.Join(p.Path, ".")
		for _, arg := range p.Args {
			idx, err := l.lowerPattern(arg)
			if err != nil {
				return -1, err
			}
			if idx >= 0 {
				node.children = append(node.children, idx)
			}
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.RangePat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternRangeKind()
		node.flags = boolToInt(p.Inclusive)
		node.left, err = l.lowerExpr(p.Start)
		if err != nil {
			return -1, err
		}
		node.right, err = l.lowerExpr(p.Stop)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	case *ast.OrPat:
		return l.lowerOrPattern(p.Alts, p)
	case *ast.BindingPat:
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), p)
		if err != nil {
			return -1, err
		}
		node.extra = astPatternBindingKind()
		node.text = p.Name
		node.left, err = l.lowerPattern(p.Pattern)
		if err != nil {
			return -1, err
		}
		return astArenaAdd(l.arena, node), nil
	default:
		return -1, &selfhostLoweringUnsupported{reason: fmt.Sprintf("pattern %T", pattern)}
	}
}

func (l *selfhostPackageFileLowerer) lowerOrPattern(alts []ast.Pattern, whole ast.Node) (int, error) {
	if len(alts) == 0 {
		return -1, nil
	}
	if len(alts) == 1 {
		return l.lowerPattern(alts[0])
	}
	left, err := l.lowerPattern(alts[0])
	if err != nil {
		return -1, err
	}
	right, err := l.lowerOrPattern(alts[1:], whole)
	if err != nil {
		return -1, err
	}
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), whole)
	if err != nil {
		return -1, err
	}
	node.extra = astPatternOrKind()
	node.left = left
	node.right = right
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerStructPatternField(field *ast.StructPatField) (int, error) {
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNPattern{}), field)
	if err != nil {
		return -1, err
	}
	node.extra = astPatternFieldKind()
	node.text = field.Name
	node.left, err = l.lowerPattern(field.Pattern)
	if err != nil {
		return -1, err
	}
	return astArenaAdd(l.arena, node), nil
}

func (l *selfhostPackageFileLowerer) lowerStructOwnerExpr(expr ast.Expr) (int, error) {
	if name, ok := selfhostPathExprText(expr); ok {
		node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNIdent{}), expr)
		if err != nil {
			return -1, err
		}
		node.text = name
		return astArenaAdd(l.arena, node), nil
	}
	return l.lowerExpr(expr)
}

func (l *selfhostPackageFileLowerer) lowerType(ty ast.Type) (int, error) {
	if ty == nil {
		return -1, nil
	}
	text, err := selfhostRenderType(ty)
	if err != nil {
		return -1, err
	}
	node, err := l.nodeForPublic(AstNodeKind(&AstNodeKind_AstNType{}), ty)
	if err != nil {
		return -1, err
	}
	node.text = text
	return astArenaAdd(l.arena, node), nil
}

func selfhostRenderType(ty ast.Type) (string, error) {
	switch t := ty.(type) {
	case nil:
		return "", nil
	case *ast.NamedType:
		head := strings.Join(t.Path, ".")
		if len(t.Args) == 0 {
			return head, nil
		}
		args := make([]string, 0, len(t.Args))
		for _, arg := range t.Args {
			rendered, err := selfhostRenderType(arg)
			if err != nil {
				return "", err
			}
			args = append(args, rendered)
		}
		return head + "<" + strings.Join(args, ", ") + ">", nil
	case *ast.OptionalType:
		inner, err := selfhostRenderType(t.Inner)
		if err != nil {
			return "", err
		}
		return inner + "?", nil
	case *ast.TupleType:
		elems := make([]string, 0, len(t.Elems))
		for _, elem := range t.Elems {
			rendered, err := selfhostRenderType(elem)
			if err != nil {
				return "", err
			}
			elems = append(elems, rendered)
		}
		return "(" + strings.Join(elems, ", ") + ")", nil
	case *ast.FnType:
		params := make([]string, 0, len(t.Params))
		for _, param := range t.Params {
			rendered, err := selfhostRenderType(param)
			if err != nil {
				return "", err
			}
			params = append(params, rendered)
		}
		text := "fn(" + strings.Join(params, ", ") + ")"
		if t.ReturnType != nil {
			ret, err := selfhostRenderType(t.ReturnType)
			if err != nil {
				return "", err
			}
			text += " -> " + ret
		}
		return text, nil
	default:
		return "", &selfhostLoweringUnsupported{reason: fmt.Sprintf("type %T", ty)}
	}
}

func selfhostPathExprText(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name, true
	case *ast.FieldExpr:
		if e.IsOptional {
			return "", false
		}
		head, ok := selfhostPathExprText(e.X)
		if !ok {
			return "", false
		}
		return head + "." + e.Name, true
	default:
		return "", false
	}
}

func (l *selfhostPackageFileLowerer) patternLiteralText(expr ast.Expr) string {
	if text := l.literalSourceText(expr); text != "" {
		return text
	}
	switch e := expr.(type) {
	case *ast.IntLit:
		return e.Text
	case *ast.FloatLit:
		return e.Text
	case *ast.BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case *ast.UnaryExpr:
		if e.Op == token.MINUS {
			return "-" + l.patternLiteralText(e.X)
		}
	}
	return ""
}

func (l *selfhostPackageFileLowerer) stringLiteralText(expr *ast.StringLit) string {
	if expr == nil {
		return ""
	}
	if text := l.literalSourceText(expr); text != "" {
		return text
	}
	var b strings.Builder
	for _, part := range expr.Parts {
		if part.IsLit {
			b.WriteString(part.Lit)
		}
	}
	return b.String()
}

func (l *selfhostPackageFileLowerer) literalSourceText(node ast.Node) string {
	start, end, err := l.localOffsets(node)
	if err != nil || start < 0 || end < start || end > len(l.file.Source) {
		return ""
	}
	return string(l.file.Source[start:end])
}

func (l *selfhostPackageFileLowerer) nodeForPublic(kind AstNodeKind, node ast.Node) (*AstNode, error) {
	start, end, err := l.absoluteOffsets(node)
	if err != nil {
		return nil, err
	}
	n, err := l.newNodeForSpan(kind, start, end)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (l *selfhostPackageFileLowerer) newNodeForSpan(kind AstNodeKind, start, end int) (*AstNode, error) {
	n := emptyAstNode(kind)
	n.start = start
	n.end = end
	return n, nil
}

func (l *selfhostPackageFileLowerer) absoluteOffsets(node ast.Node) (int, int, error) {
	start, end, err := l.localOffsets(node)
	if err != nil {
		return 0, 0, err
	}
	return l.file.Base + start, l.file.Base + end, nil
}

func (l *selfhostPackageFileLowerer) localOffsets(node ast.Node) (int, int, error) {
	if node == nil {
		return 0, 0, nil
	}
	start := node.Pos().Offset
	end := node.End().Offset
	if end < start {
		end = start
	}
	if l.file.SourceMap != nil {
		span, ok := l.file.SourceMap.GeneratedSpanForOriginal(diag.Span{
			Start: node.Pos(),
			End:   node.End(),
		})
		if !ok {
			return 0, 0, &selfhostLoweringUnsupported{reason: fmt.Sprintf("missing canonical span for %T", node)}
		}
		start = span.Start.Offset
		end = span.End.Offset
		if end < start {
			end = start
		}
	}
	if start < 0 || end < start || end > len(l.file.Source) {
		return 0, 0, &selfhostLoweringUnsupported{reason: fmt.Sprintf("out-of-range span for %T", node)}
	}
	return start, end, nil
}

func selfhostFrontTokenKind(kind token.Kind) (FrontTokenKind, error) {
	switch kind {
	case token.EOF:
		return FrontTokenKind(&FrontTokenKind_FrontEOF{}), nil
	case token.IDENT:
		return FrontTokenKind(&FrontTokenKind_FrontIdent{}), nil
	case token.INT:
		return FrontTokenKind(&FrontTokenKind_FrontInt{}), nil
	case token.FLOAT:
		return FrontTokenKind(&FrontTokenKind_FrontFloat{}), nil
	case token.CHAR:
		return FrontTokenKind(&FrontTokenKind_FrontChar{}), nil
	case token.BYTE:
		return FrontTokenKind(&FrontTokenKind_FrontByte{}), nil
	case token.STRING:
		return FrontTokenKind(&FrontTokenKind_FrontString{}), nil
	case token.RAWSTRING:
		return FrontTokenKind(&FrontTokenKind_FrontRawString{}), nil
	case token.PLUS:
		return FrontTokenKind(&FrontTokenKind_FrontPlus{}), nil
	case token.MINUS:
		return FrontTokenKind(&FrontTokenKind_FrontMinus{}), nil
	case token.STAR:
		return FrontTokenKind(&FrontTokenKind_FrontStar{}), nil
	case token.SLASH:
		return FrontTokenKind(&FrontTokenKind_FrontSlash{}), nil
	case token.PERCENT:
		return FrontTokenKind(&FrontTokenKind_FrontPercent{}), nil
	case token.EQ:
		return FrontTokenKind(&FrontTokenKind_FrontEq{}), nil
	case token.NEQ:
		return FrontTokenKind(&FrontTokenKind_FrontNeq{}), nil
	case token.LT:
		return FrontTokenKind(&FrontTokenKind_FrontLt{}), nil
	case token.GT:
		return FrontTokenKind(&FrontTokenKind_FrontGt{}), nil
	case token.LEQ:
		return FrontTokenKind(&FrontTokenKind_FrontLeq{}), nil
	case token.GEQ:
		return FrontTokenKind(&FrontTokenKind_FrontGeq{}), nil
	case token.AND:
		return FrontTokenKind(&FrontTokenKind_FrontAnd{}), nil
	case token.OR:
		return FrontTokenKind(&FrontTokenKind_FrontOr{}), nil
	case token.NOT:
		return FrontTokenKind(&FrontTokenKind_FrontNot{}), nil
	case token.BITAND:
		return FrontTokenKind(&FrontTokenKind_FrontBitAnd{}), nil
	case token.BITOR:
		return FrontTokenKind(&FrontTokenKind_FrontBitOr{}), nil
	case token.BITXOR:
		return FrontTokenKind(&FrontTokenKind_FrontBitXor{}), nil
	case token.BITNOT:
		return FrontTokenKind(&FrontTokenKind_FrontBitNot{}), nil
	case token.SHL:
		return FrontTokenKind(&FrontTokenKind_FrontShl{}), nil
	case token.SHR:
		return FrontTokenKind(&FrontTokenKind_FrontShr{}), nil
	case token.ASSIGN:
		return FrontTokenKind(&FrontTokenKind_FrontAssign{}), nil
	case token.PLUSEQ:
		return FrontTokenKind(&FrontTokenKind_FrontPlusEq{}), nil
	case token.MINUSEQ:
		return FrontTokenKind(&FrontTokenKind_FrontMinusEq{}), nil
	case token.STAREQ:
		return FrontTokenKind(&FrontTokenKind_FrontStarEq{}), nil
	case token.SLASHEQ:
		return FrontTokenKind(&FrontTokenKind_FrontSlashEq{}), nil
	case token.PERCENTEQ:
		return FrontTokenKind(&FrontTokenKind_FrontPercentEq{}), nil
	case token.BITANDEQ:
		return FrontTokenKind(&FrontTokenKind_FrontBitAndEq{}), nil
	case token.BITOREQ:
		return FrontTokenKind(&FrontTokenKind_FrontBitOrEq{}), nil
	case token.BITXOREQ:
		return FrontTokenKind(&FrontTokenKind_FrontBitXorEq{}), nil
	case token.SHLEQ:
		return FrontTokenKind(&FrontTokenKind_FrontShlEq{}), nil
	case token.SHREQ:
		return FrontTokenKind(&FrontTokenKind_FrontShrEq{}), nil
	case token.QUESTION:
		return FrontTokenKind(&FrontTokenKind_FrontQuestion{}), nil
	case token.QQ:
		return FrontTokenKind(&FrontTokenKind_FrontQQ{}), nil
	case token.DOTDOT:
		return FrontTokenKind(&FrontTokenKind_FrontDotDot{}), nil
	case token.DOTDOTEQ:
		return FrontTokenKind(&FrontTokenKind_FrontDotDotEq{}), nil
	case token.ARROW:
		return FrontTokenKind(&FrontTokenKind_FrontArrow{}), nil
	case token.CHANARROW:
		return FrontTokenKind(&FrontTokenKind_FrontChanArrow{}), nil
	default:
		return nil, &selfhostLoweringUnsupported{reason: fmt.Sprintf("token kind %s", kind.String())}
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
