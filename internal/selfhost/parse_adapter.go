package selfhost

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// Parse runs the bootstrapped pure-Osty lexer and parser, then lowers the
// self-hosted arena into the compiler's public AST.
func Parse(src []byte) (*ast.File, []*diag.Diagnostic) {
	run := Run(src)
	return run.File(), run.Diagnostics()
}

// File lowers the parser arena into the public Go AST.
func (r *FrontendRun) File() *ast.File {
	if r.file != nil {
		return r.file
	}
	l := astLowerer{arena: r.parser.arena, toks: r.Tokens()}
	r.file = l.file()
	return r.file
}

// Diagnostics returns lexer and parser diagnostics from this run.
func (r *FrontendRun) Diagnostics() []*diag.Diagnostic {
	if r.diags != nil {
		return r.diags
	}
	lexDiags := r.LexDiagnostics()
	parseDiags := parseDiagnosticsFromArena(r.parser.arena, r.stream, r.rt)
	diags := make([]*diag.Diagnostic, 0, len(lexDiags)+len(parseDiags))
	diags = append(diags, lexDiags...)
	diags = append(diags, parseDiags...)
	r.diags = dedupeDiagnostics(diags)
	return r.diags
}

func dedupeDiagnostics(in []*diag.Diagnostic) []*diag.Diagnostic {
	out := in[:0]
	seen := map[token.Pos]bool{}
	for _, d := range in {
		if d == nil {
			continue
		}
		pos := d.PrimaryPos()
		if seen[pos] {
			continue
		}
		seen[pos] = true
		out = append(out, d)
	}
	return out
}

func parseDiagnosticsFromArena(arena *AstArena, stream *FrontLexStream, rt runeTable) []*diag.Diagnostic {
	out := make([]*diag.Diagnostic, 0, len(arena.errors))
	for _, e := range arena.errors {
		b := diag.New(diag.Error, e.message).Primary(parseErrorSpan(e, stream, rt), "")
		if e.code != "" {
			b.Code(e.code)
		}
		if e.hint != "" {
			b.Hint(e.hint)
		}
		if e.note != "" {
			b.Note(e.note)
		}
		out = append(out, b.Build())
	}
	return out
}

func parseErrorSpan(e *AstParseError, stream *FrontLexStream, rt runeTable) diag.Span {
	pos := token.Pos{Line: 1, Column: 1}
	if e == nil || e.tokenIndex < 0 || e.tokenIndex >= len(stream.tokens) {
		return diag.Span{Start: pos, End: pos}
	}
	tok := stream.tokens[e.tokenIndex]
	span := rt.span(tok.start, tok.end)
	if span.End.Offset < span.Start.Offset {
		span.End = span.Start
	}
	return span
}

type astLowerer struct {
	arena *AstArena
	toks  []token.Token
}

func (l astLowerer) file() *ast.File {
	f := &ast.File{PosV: l.pos(0), EndV: l.end(len(l.toks) - 1)}
	for _, idx := range l.arena.decls {
		n := l.node(idx)
		if n == nil {
			continue
		}
		if _, ok := n.kind.(*AstNodeKind_AstNLet); ok && l.tok(n.start-1).Kind != token.PUB {
			if s := l.stmt(idx); s != nil {
				f.Stmts = append(f.Stmts, s)
			}
			continue
		}
		if d := l.decl(idx); d != nil {
			if u, ok := d.(*ast.UseDecl); ok {
				f.Uses = append(f.Uses, u)
			} else {
				f.Decls = append(f.Decls, d)
			}
			continue
		}
		if s := l.stmt(idx); s != nil {
			f.Stmts = append(f.Stmts, s)
		}
	}
	return f
}

func (l astLowerer) node(idx int) *AstNode {
	if idx < 0 || idx >= len(l.arena.nodes) {
		return nil
	}
	return l.arena.nodes[idx]
}

func (l astLowerer) tok(idx int) token.Token {
	if idx >= 0 && idx < len(l.toks) {
		return l.toks[idx]
	}
	if idx < 0 && len(l.toks) > 0 {
		return l.toks[0]
	}
	if len(l.toks) > 0 {
		return l.toks[len(l.toks)-1]
	}
	return token.Token{Pos: token.Pos{Line: 1, Column: 1}, End: token.Pos{Line: 1, Column: 1}}
}

func (l astLowerer) pos(idx int) token.Pos { return l.tok(idx).Pos }

func (l astLowerer) end(idx int) token.Pos {
	if idx <= 0 {
		return l.tok(0).End
	}
	return l.tok(idx - 1).End
}

func (l astLowerer) nodePos(n *AstNode) token.Pos {
	if n == nil {
		return l.pos(0)
	}
	return l.pos(n.start)
}

func (l astLowerer) nodeEnd(n *AstNode) token.Pos {
	if n == nil {
		return l.end(0)
	}
	return l.end(n.end)
}

func (l astLowerer) doc(idx int) string {
	if d := l.tok(idx).LeadingDoc; d != "" {
		return d
	}
	if idx > 0 && l.tok(idx-1).Kind == token.PUB {
		return l.tok(idx - 1).LeadingDoc
	}
	return ""
}

func (l astLowerer) mutPos(n *AstNode) token.Pos {
	if n == nil || n.flags != 1 {
		return token.Pos{}
	}
	end := n.end
	if pat := l.node(n.left); pat != nil {
		end = pat.start
	}
	for i := n.start; i < end; i++ {
		tok := l.tok(i)
		if tok.Kind == token.MUT {
			return tok.Pos
		}
	}
	return token.Pos{}
}

func (l astLowerer) decl(idx int) ast.Decl {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNFnDecl:
		return l.fnDecl(n)
	case *AstNodeKind_AstNStructDecl:
		return l.structDecl(n)
	case *AstNodeKind_AstNEnumDecl:
		return l.enumDecl(n)
	case *AstNodeKind_AstNInterfaceDecl:
		return l.interfaceDecl(n)
	case *AstNodeKind_AstNTypeAlias:
		return l.typeAliasDecl(n)
	case *AstNodeKind_AstNUseDecl:
		return l.useDecl(n)
	case *AstNodeKind_AstNLet:
		return l.letDecl(n)
	default:
		return nil
	}
}

func (l astLowerer) fnDecl(n *AstNode) *ast.FnDecl {
	fn := &ast.FnDecl{
		PosV:        l.nodePos(n),
		EndV:        l.nodeEnd(n),
		Pub:         n.flags == 1,
		Name:        n.text,
		Generics:    l.genericParams(n.children2),
		ReturnType:  l.typ(n.left),
		Body:        l.block(n.right),
		DocComment:  l.doc(n.start),
		Annotations: l.annotations(n.extra),
	}
	for i, child := range n.children {
		p := l.param(child)
		if p == nil {
			continue
		}
		if i == 0 && p.Name == "self" {
			cn := l.node(child)
			fn.Recv = &ast.Receiver{PosV: p.PosV, EndV: p.EndV, Mut: cn != nil && cn.flags == 1, MutPos: p.PosV}
			continue
		}
		fn.Params = append(fn.Params, p)
	}
	return fn
}

func (l astLowerer) structDecl(n *AstNode) *ast.StructDecl {
	s := &ast.StructDecl{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Pub: n.flags == 1, Name: n.text, Generics: l.genericParams(n.children2), DocComment: l.doc(n.start), Annotations: l.annotations(n.extra)}
	for _, child := range n.children {
		cn := l.node(child)
		if cn == nil {
			continue
		}
		switch cn.kind.(type) {
		case *AstNodeKind_AstNFnDecl:
			s.Methods = append(s.Methods, l.fnDecl(cn))
		case *AstNodeKind_AstNField_:
			s.Fields = append(s.Fields, l.field(cn))
		}
	}
	return s
}

func (l astLowerer) enumDecl(n *AstNode) *ast.EnumDecl {
	e := &ast.EnumDecl{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Pub: n.flags == 1, Name: n.text, Generics: l.genericParams(n.children2), DocComment: l.doc(n.start), Annotations: l.annotations(n.extra)}
	for _, child := range n.children {
		cn := l.node(child)
		if cn == nil {
			continue
		}
		switch cn.kind.(type) {
		case *AstNodeKind_AstNFnDecl:
			e.Methods = append(e.Methods, l.fnDecl(cn))
		case *AstNodeKind_AstNVariant:
			v := &ast.Variant{PosV: l.nodePos(cn), EndV: l.nodeEnd(cn), Name: cn.text, Annotations: l.annotations(cn.extra), DocComment: l.doc(cn.start)}
			for _, t := range cn.children {
				if ty := l.typ(t); ty != nil {
					v.Fields = append(v.Fields, ty)
				}
			}
			e.Variants = append(e.Variants, v)
		}
	}
	return e
}

func (l astLowerer) interfaceDecl(n *AstNode) *ast.InterfaceDecl {
	it := &ast.InterfaceDecl{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Pub: n.flags == 1, Name: n.text, DocComment: l.doc(n.start), Annotations: l.annotations(n.extra)}
	for _, idx := range n.children2 {
		if ty := l.typ(idx); ty != nil {
			it.Extends = append(it.Extends, ty)
		}
	}
	for _, child := range n.children {
		cn := l.node(child)
		if cn == nil {
			continue
		}
		if _, ok := cn.kind.(*AstNodeKind_AstNFnDecl); ok {
			it.Methods = append(it.Methods, l.fnDecl(cn))
		} else if ty := l.typ(child); ty != nil {
			it.Extends = append(it.Extends, ty)
		}
	}
	return it
}

func (l astLowerer) typeAliasDecl(n *AstNode) *ast.TypeAliasDecl {
	return &ast.TypeAliasDecl{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Pub: n.flags == 1, Name: n.text, Generics: l.genericParams(n.children), Target: l.typ(n.left), DocComment: l.doc(n.start), Annotations: l.annotations(n.extra)}
}

func (l astLowerer) useDecl(n *AstNode) *ast.UseDecl {
	raw := unquoteMaybe(n.text)
	if reconstructed := l.useRawPath(n); reconstructed != "" {
		raw = reconstructed
	}
	u := &ast.UseDecl{PosV: l.nodePos(n), EndV: l.nodeEnd(n), RawPath: raw, IsGoFFI: n.flags == 1}
	if u.IsGoFFI {
		u.GoPath = raw
	} else {
		u.Path = splitPath(raw)
	}
	if len(n.children2) > 0 {
		if a := l.node(n.children2[0]); a != nil {
			u.Alias = a.text
		}
	}
	for _, child := range n.children {
		if d := l.decl(child); d != nil {
			u.GoBody = append(u.GoBody, d)
		}
	}
	return u
}

func (l astLowerer) useRawPath(n *AstNode) string {
	if n == nil {
		return ""
	}
	if n.flags == 1 {
		return l.useGoRawPath(n)
	}
	var b strings.Builder
	for i := n.start + 1; i < n.end; i++ {
		tok := l.tok(i)
		switch tok.Kind {
		case token.IDENT:
			if tok.Value == "as" {
				return b.String()
			}
			b.WriteString(tok.Value)
		case token.DOT, token.SLASH, token.COLON:
			b.WriteString(tok.Kind.String())
		case token.LBRACE, token.NEWLINE, token.EOF:
			return b.String()
		default:
			return b.String()
		}
	}
	return b.String()
}

func (l astLowerer) useGoRawPath(n *AstNode) string {
	for i := n.start + 1; i < n.end; i++ {
		tok := l.tok(i)
		switch tok.Kind {
		case token.STRING, token.RAWSTRING:
			return unquoteMaybe(tok.Value)
		case token.LBRACE, token.NEWLINE, token.EOF:
			return ""
		}
	}
	return ""
}

func (l astLowerer) letDecl(n *AstNode) *ast.LetDecl {
	name := ""
	if p, ok := l.pattern(n.left).(*ast.IdentPat); ok {
		name = p.Name
	}
	return &ast.LetDecl{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Pub: l.tok(n.start-1).Kind == token.PUB, Mut: n.flags == 1, MutPos: l.mutPos(n), Name: name, Type: l.childType(n, 0), Value: l.expr(n.right), DocComment: l.doc(n.start), Annotations: l.annotations(n.extra)}
}

func (l astLowerer) field(n *AstNode) *ast.Field {
	return &ast.Field{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Pub: n.flags == 1, Name: n.text, Type: l.typ(n.right), Default: l.expr(n.left), DocComment: l.doc(n.start), Annotations: l.annotations(n.extra)}
}

func (l astLowerer) param(idx int) *ast.Param {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	p := &ast.Param{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Name: n.text, Type: l.typ(n.right), Default: l.expr(n.left)}
	if n.left >= 0 {
		if left := l.node(n.left); left != nil {
			if _, ok := left.kind.(*AstNodeKind_AstNPattern); ok {
				if pat := l.pattern(n.left); pat != nil {
					p.Pattern = pat
					p.Default = nil
				}
			}
		}
	}
	return p
}

func (l astLowerer) genericParams(ids []int) []*ast.GenericParam {
	out := make([]*ast.GenericParam, 0, len(ids))
	for _, idx := range ids {
		n := l.node(idx)
		if n == nil {
			continue
		}
		g := &ast.GenericParam{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Name: n.text}
		for _, c := range n.children {
			if ty := l.typ(c); ty != nil {
				g.Constraints = append(g.Constraints, ty)
			}
		}
		out = append(out, g)
	}
	return out
}

func (l astLowerer) annotations(idx int) []*ast.Annotation {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	if n.text == "__group" {
		var out []*ast.Annotation
		for _, child := range n.children {
			if a := l.annotation(child); a != nil {
				out = append(out, a)
			}
		}
		return out
	}
	if a := l.annotation(idx); a != nil {
		return []*ast.Annotation{a}
	}
	return nil
}

func (l astLowerer) annotation(idx int) *ast.Annotation {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	a := &ast.Annotation{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Name: n.text}
	for _, child := range n.children {
		cn := l.node(child)
		if cn != nil {
			if _, ok := cn.kind.(*AstNodeKind_AstNField_); ok {
				a.Args = append(a.Args, &ast.AnnotationArg{PosV: l.nodePos(cn), Key: cn.text, Value: l.expr(cn.left)})
				continue
			}
			if _, ok := cn.kind.(*AstNodeKind_AstNIdent); ok {
				a.Args = append(a.Args, &ast.AnnotationArg{PosV: l.nodePos(cn), Key: cn.text})
				continue
			}
		}
		a.Args = append(a.Args, &ast.AnnotationArg{PosV: l.pos(l.node(child).start), Value: l.expr(child)})
	}
	return a
}

func (l astLowerer) typ(idx int) ast.Type {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	if n.text == "optional" {
		return &ast.OptionalType{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Inner: l.typ(n.left)}
	}
	if n.text == "tuple" {
		t := &ast.TupleType{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
		for _, c := range n.children {
			if ty := l.typ(c); ty != nil {
				t.Elems = append(t.Elems, ty)
			}
		}
		return t
	}
	if n.text == "fn" {
		f := &ast.FnType{PosV: l.nodePos(n), EndV: l.nodeEnd(n), ReturnType: l.typ(n.right)}
		for _, c := range n.children {
			if ty := l.typ(c); ty != nil {
				f.Params = append(f.Params, ty)
			}
		}
		return f
	}
	nt := &ast.NamedType{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Path: splitPath(n.text)}
	for _, c := range n.children {
		if ty := l.typ(c); ty != nil {
			nt.Args = append(nt.Args, ty)
		}
	}
	return nt
}

func (l astLowerer) childType(n *AstNode, at int) ast.Type {
	if n == nil || at < 0 || at >= len(n.children) {
		return nil
	}
	return l.typ(n.children[at])
}

func (l astLowerer) stmt(idx int) ast.Stmt {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNLet:
		return &ast.LetStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Pattern: l.pattern(n.left), Mut: n.flags == 1, MutPos: l.mutPos(n), Type: l.childType(n, 0), Value: l.expr(n.right)}
	case *AstNodeKind_AstNReturn:
		return &ast.ReturnStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Value: l.expr(n.left)}
	case *AstNodeKind_AstNBreak:
		return &ast.BreakStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
	case *AstNodeKind_AstNContinue:
		return &ast.ContinueStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
	case *AstNodeKind_AstNDefer:
		return &ast.DeferStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n), X: l.expr(n.left)}
	case *AstNodeKind_AstNFor:
		fs := &ast.ForStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Body: l.block(n.right)}
		if n.text == "forlet" {
			fs.IsForLet = true
			fs.Pattern = l.childPattern(n, 0)
			fs.Iter = l.expr(n.left)
		} else if n.text == "forin" {
			fs.Pattern = l.childPattern(n, 0)
			fs.Iter = l.childExpr(n, 1)
		} else {
			fs.Iter = l.expr(n.left)
		}
		return fs
	case *AstNodeKind_AstNAssign:
		return &ast.AssignStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Op: mapTokenKind(n.op), Targets: []ast.Expr{l.expr(n.left)}, Value: l.expr(n.right)}
	case *AstNodeKind_AstNChanSend:
		return &ast.ChanSendStmt{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Channel: l.expr(n.left), Value: l.expr(n.right)}
	case *AstNodeKind_AstNExprStmt:
		return &ast.ExprStmt{X: l.expr(n.left)}
	default:
		if e := l.expr(idx); e != nil {
			return &ast.ExprStmt{X: e}
		}
	}
	return nil
}

func (l astLowerer) block(idx int) *ast.Block {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	b := &ast.Block{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
	for _, child := range n.children {
		if s := l.stmt(child); s != nil {
			b.Stmts = append(b.Stmts, s)
		}
	}
	return b
}

func (l astLowerer) expr(idx int) ast.Expr {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNIdent:
		return &ast.Ident{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Name: n.text}
	case *AstNodeKind_AstNIntLit:
		return &ast.IntLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Text: n.text}
	case *AstNodeKind_AstNFloatLit:
		return &ast.FloatLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Text: n.text}
	case *AstNodeKind_AstNBoolLit:
		return &ast.BoolLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Value: n.flags == 1}
	case *AstNodeKind_AstNCharLit:
		return &ast.CharLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Value: firstRune(decodedLiteral(l.tok(n.start).Value))}
	case *AstNodeKind_AstNByteLit:
		return &ast.ByteLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Value: firstByte(decodedLiteral(l.tok(n.start).Value))}
	case *AstNodeKind_AstNStringLit:
		t := l.tok(n.start)
		return &ast.StringLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), IsRaw: t.Kind == token.RAWSTRING, IsTriple: t.Triple, Parts: stringPartsToAST(t.Parts)}
	case *AstNodeKind_AstNUnary:
		x := l.expr(n.left)
		return &ast.UnaryExpr{PosV: l.nodePos(n), EndV: exprEnd(x, l.nodeEnd(n)), Op: mapTokenKind(n.op), X: x}
	case *AstNodeKind_AstNBinary:
		left := l.expr(n.left)
		right := l.expr(n.right)
		return &ast.BinaryExpr{PosV: exprPos(left, l.nodePos(n)), EndV: exprEnd(right, l.nodeEnd(n)), Op: mapTokenKind(n.op), Left: left, Right: right}
	case *AstNodeKind_AstNQuestion:
		x := l.expr(n.left)
		return &ast.QuestionExpr{PosV: exprPos(x, l.nodePos(n)), EndV: l.nodeEnd(n), X: x}
	case *AstNodeKind_AstNCall:
		fn := l.expr(n.left)
		c := &ast.CallExpr{PosV: exprPos(fn, l.nodePos(n)), EndV: l.nodeEnd(n), Fn: fn}
		for _, a := range n.children {
			c.Args = append(c.Args, l.arg(a))
		}
		return c
	case *AstNodeKind_AstNField:
		x := l.expr(n.left)
		return &ast.FieldExpr{PosV: exprPos(x, l.nodePos(n)), EndV: l.nodeEnd(n), X: x, Name: n.text, IsOptional: n.flags == 1}
	case *AstNodeKind_AstNIndex:
		x := l.expr(n.left)
		return &ast.IndexExpr{PosV: exprPos(x, l.nodePos(n)), EndV: l.nodeEnd(n), X: x, Index: l.expr(n.right)}
	case *AstNodeKind_AstNTurbofish:
		base := l.expr(n.left)
		tf := &ast.TurbofishExpr{PosV: exprPos(base, l.nodePos(n)), EndV: l.nodeEnd(n), Base: base}
		for _, c := range n.children {
			if ty := l.typ(c); ty != nil {
				tf.Args = append(tf.Args, ty)
			}
		}
		return tf
	case *AstNodeKind_AstNRange:
		start := l.expr(n.left)
		stop := l.expr(n.right)
		return &ast.RangeExpr{PosV: exprPos(start, l.nodePos(n)), EndV: exprEnd(stop, l.nodeEnd(n)), Start: start, Stop: stop, Inclusive: n.flags == 1}
	case *AstNodeKind_AstNParen:
		return &ast.ParenExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n), X: l.expr(n.left)}
	case *AstNodeKind_AstNTuple:
		t := &ast.TupleExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
		for _, c := range n.children {
			if e := l.expr(c); e != nil {
				t.Elems = append(t.Elems, e)
			}
		}
		return t
	case *AstNodeKind_AstNList:
		x := &ast.ListExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
		for _, c := range n.children {
			if e := l.expr(c); e != nil {
				x.Elems = append(x.Elems, e)
			}
		}
		return x
	case *AstNodeKind_AstNMap:
		m := &ast.MapExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Empty: n.flags == 1}
		for i, k := range n.children {
			var v ast.Expr
			if i < len(n.children2) {
				v = l.expr(n.children2[i])
			}
			m.Entries = append(m.Entries, &ast.MapEntry{Key: l.expr(k), Value: v})
		}
		return m
	case *AstNodeKind_AstNStructLit:
		typ := l.expr(n.left)
		sl := &ast.StructLit{PosV: exprPos(typ, l.nodePos(n)), EndV: l.nodeEnd(n), Type: typ, Spread: l.expr(n.right)}
		for _, c := range n.children {
			cn := l.node(c)
			if cn != nil {
				sl.Fields = append(sl.Fields, &ast.StructLitField{PosV: l.nodePos(cn), Name: cn.text, Value: l.expr(cn.left)})
			}
		}
		return sl
	case *AstNodeKind_AstNBlock:
		return l.block(idx)
	case *AstNodeKind_AstNIf:
		ife := &ast.IfExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Cond: l.expr(n.left), Then: l.block(n.right), IsIfLet: n.flags == 1}
		if len(n.children) > 0 {
			ife.Else = l.expr(n.children[0])
		}
		if len(n.children) > 1 {
			ife.Pattern = l.pattern(n.children[1])
		}
		return ife
	case *AstNodeKind_AstNMatch:
		m := &ast.MatchExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Scrutinee: l.expr(n.left)}
		for _, c := range n.children {
			if arm := l.matchArm(c); arm != nil {
				m.Arms = append(m.Arms, arm)
			}
		}
		return m
	case *AstNodeKind_AstNClosure:
		cl := &ast.ClosureExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Body: l.expr(n.left), ReturnType: l.typ(n.right)}
		for _, p := range n.children {
			if param := l.param(p); param != nil {
				cl.Params = append(cl.Params, param)
			}
		}
		return cl
	}
	return nil
}

func exprPos(e ast.Expr, fallback token.Pos) token.Pos {
	if e != nil {
		return e.Pos()
	}
	return fallback
}

func exprEnd(e ast.Expr, fallback token.Pos) token.Pos {
	if e != nil {
		return e.End()
	}
	return fallback
}

func (l astLowerer) arg(idx int) *ast.Arg {
	n := l.node(idx)
	if n != nil {
		if _, ok := n.kind.(*AstNodeKind_AstNField_); ok {
			return &ast.Arg{PosV: l.nodePos(n), Name: n.text, Value: l.expr(n.left)}
		}
	}
	return &ast.Arg{PosV: l.posNodeOrToken(n, idx), Value: l.expr(idx)}
}

func (l astLowerer) posNodeOrToken(n *AstNode, idx int) token.Pos {
	if n != nil {
		return l.nodePos(n)
	}
	return l.pos(idx)
}

func (l astLowerer) matchArm(idx int) *ast.MatchArm {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	a := &ast.MatchArm{PosV: l.nodePos(n), Pattern: l.pattern(n.left), Body: l.expr(n.right)}
	if len(n.children) > 0 {
		a.Guard = l.expr(n.children[0])
	}
	return a
}

func (l astLowerer) childPattern(n *AstNode, at int) ast.Pattern {
	if n == nil || at < 0 || at >= len(n.children) {
		return nil
	}
	return l.pattern(n.children[at])
}

func (l astLowerer) childExpr(n *AstNode, at int) ast.Expr {
	if n == nil || at < 0 || at >= len(n.children) {
		return nil
	}
	return l.expr(n.children[at])
}

func (l astLowerer) pattern(idx int) ast.Pattern {
	n := l.node(idx)
	if n == nil {
		return nil
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNIdent:
		return &ast.IdentPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Name: n.text}
	case *AstNodeKind_AstNTuple:
		t := &ast.TuplePat{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
		for _, c := range n.children {
			if p := l.pattern(c); p != nil {
				t.Elems = append(t.Elems, p)
			}
		}
		return t
	case *AstNodeKind_AstNIntLit, *AstNodeKind_AstNFloatLit, *AstNodeKind_AstNStringLit, *AstNodeKind_AstNCharLit, *AstNodeKind_AstNByteLit, *AstNodeKind_AstNBoolLit:
		return &ast.LiteralPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Literal: l.expr(idx)}
	}
	switch {
	case strings.HasPrefix(n.text, "ident:"):
		return &ast.IdentPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Name: strings.TrimPrefix(n.text, "ident:")}
	case n.text == "wildcard":
		return &ast.WildcardPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
	case strings.HasPrefix(n.text, "literal:"):
		return &ast.LiteralPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Literal: l.literalPatternExpr(n)}
	case n.text == "negLiteral":
		return &ast.LiteralPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Literal: &ast.UnaryExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Op: token.MINUS, X: l.literalPatternExpr(l.node(n.left))}}
	case n.text == "tuple":
		t := &ast.TuplePat{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
		for _, c := range n.children {
			if p := l.pattern(c); p != nil {
				t.Elems = append(t.Elems, p)
			}
		}
		return t
	case strings.HasPrefix(n.text, "variant:"):
		v := &ast.VariantPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Path: splitPath(strings.TrimPrefix(n.text, "variant:"))}
		for _, c := range n.children {
			if p := l.pattern(c); p != nil {
				v.Args = append(v.Args, p)
			}
		}
		return v
	case strings.HasPrefix(n.text, "struct:"):
		s := &ast.StructPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Type: splitPath(strings.TrimPrefix(n.text, "struct:")), Rest: n.flags == 1}
		for _, c := range n.children {
			cn := l.node(c)
			if cn == nil {
				continue
			}
			if cn.text != "" && cn.left >= 0 {
				s.Fields = append(s.Fields, &ast.StructPatField{PosV: l.nodePos(cn), Name: cn.text, Pattern: l.pattern(cn.left)})
			} else if p, ok := l.pattern(c).(*ast.IdentPat); ok {
				s.Fields = append(s.Fields, &ast.StructPatField{PosV: p.PosV, Name: p.Name})
			}
		}
		return s
	case strings.HasPrefix(n.text, "binding:"):
		return &ast.BindingPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Name: strings.TrimPrefix(n.text, "binding:"), Pattern: l.pattern(n.left)}
	case n.text == "range":
		return &ast.RangePat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Start: l.patternLiteralExpr(n.left), Stop: l.patternLiteralExpr(n.right), Inclusive: n.flags == 1}
	case n.text == "or":
		return &ast.OrPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Alts: l.orAlts(n)}
	}
	return &ast.WildcardPat{PosV: l.nodePos(n), EndV: l.nodeEnd(n)}
}

func (l astLowerer) orAlts(n *AstNode) []ast.Pattern {
	var out []ast.Pattern
	var walk func(int)
	walk = func(idx int) {
		cn := l.node(idx)
		if cn != nil && cn.text == "or" {
			walk(cn.left)
			walk(cn.right)
			return
		}
		if p := l.pattern(idx); p != nil {
			out = append(out, p)
		}
	}
	walk(n.left)
	walk(n.right)
	return out
}

func (l astLowerer) patternLiteralExpr(idx int) ast.Expr {
	if idx < 0 {
		return nil
	}
	return l.literalPatternExpr(l.node(idx))
}

func (l astLowerer) literalPatternExpr(n *AstNode) ast.Expr {
	if n == nil {
		return nil
	}
	if n.text == "negLiteral" {
		return &ast.UnaryExpr{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Op: token.MINUS, X: l.literalPatternExpr(l.node(n.left))}
	}
	text := strings.TrimPrefix(n.text, "literal:")
	switch n.op.(type) {
	case *FrontTokenKind_FrontInt:
		return &ast.IntLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Text: text}
	case *FrontTokenKind_FrontFloat:
		return &ast.FloatLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Text: text}
	case *FrontTokenKind_FrontString, *FrontTokenKind_FrontRawString:
		return &ast.StringLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Parts: []ast.StringPart{{IsLit: true, Lit: stringContent(text)}}}
	case *FrontTokenKind_FrontChar:
		return &ast.CharLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Value: firstRune(decodedLiteral(text))}
	case *FrontTokenKind_FrontByte:
		return &ast.ByteLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Value: firstByte(decodedLiteral(text))}
	default:
		return &ast.BoolLit{PosV: l.nodePos(n), EndV: l.nodeEnd(n), Value: text == "true"}
	}
}

func compactPatterns(in []ast.Pattern) []ast.Pattern {
	out := in[:0]
	for _, p := range in {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

func stringPartsToAST(parts []token.StringPart) []ast.StringPart {
	if len(parts) == 0 {
		return []ast.StringPart{{IsLit: true}}
	}
	out := make([]ast.StringPart, 0, len(parts))
	for _, p := range parts {
		if p.Kind == token.PartText {
			out = append(out, ast.StringPart{IsLit: true, Lit: p.Text})
			continue
		}
		out = append(out, ast.StringPart{Expr: tokensToExpr(p.Expr)})
	}
	return out
}

func tokensToExpr(toks []token.Token) ast.Expr {
	toks = trimTokenNewlines(toks)
	if expr := parseTokensAsExpr(toks); expr != nil {
		return expr
	}
	return tokensToExprFast(toks)
}

func parseTokensAsExpr(toks []token.Token) ast.Expr {
	if len(toks) == 0 {
		return nil
	}
	src := tokensSource(toks)
	if src == "" {
		return nil
	}
	file, diags := Parse([]byte("let __interp = " + src))
	if hasErrors(diags) || file == nil || len(file.Stmts) == 0 {
		return nil
	}
	if ls, ok := file.Stmts[0].(*ast.LetStmt); ok {
		return ls.Value
	}
	return nil
}

func hasErrors(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func tokensSource(toks []token.Token) string {
	var b strings.Builder
	for _, tk := range toks {
		if tk.Kind == token.EOF || tk.Kind == token.NEWLINE {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		if tk.Value != "" {
			b.WriteString(tk.Value)
		} else {
			b.WriteString(tk.Kind.String())
		}
	}
	return b.String()
}

func tokensToExprFast(toks []token.Token) ast.Expr {
	toks = trimTokenNewlines(toks)
	if len(toks) == 0 {
		return nil
	}
	if idx := splitTopLevelBinary(toks); idx > 0 {
		left := tokensToExpr(toks[:idx])
		right := tokensToExpr(toks[idx+1:])
		return &ast.BinaryExpr{PosV: toks[0].Pos, EndV: toks[len(toks)-1].End, Op: toks[idx].Kind, Left: left, Right: right}
	}
	if toks[0].Kind == token.MINUS || toks[0].Kind == token.NOT || toks[0].Kind == token.BITNOT {
		return &ast.UnaryExpr{PosV: toks[0].Pos, EndV: toks[len(toks)-1].End, Op: toks[0].Kind, X: tokensToExpr(toks[1:])}
	}
	if toks[0].Kind == token.LPAREN && findTokenClose(toks, 0, token.LPAREN, token.RPAREN) == len(toks)-1 {
		return &ast.ParenExpr{PosV: toks[0].Pos, EndV: toks[len(toks)-1].End, X: tokensToExpr(toks[1 : len(toks)-1])}
	}
	var expr ast.Expr
	switch toks[0].Kind {
	case token.IDENT:
		if toks[0].Value == "true" || toks[0].Value == "false" {
			expr = &ast.BoolLit{PosV: toks[0].Pos, EndV: toks[0].End, Value: toks[0].Value == "true"}
		} else {
			expr = &ast.Ident{PosV: toks[0].Pos, EndV: toks[0].End, Name: toks[0].Value}
		}
	case token.INT:
		expr = &ast.IntLit{PosV: toks[0].Pos, EndV: toks[0].End, Text: toks[0].Value}
	case token.FLOAT:
		expr = &ast.FloatLit{PosV: toks[0].Pos, EndV: toks[0].End, Text: toks[0].Value}
	case token.STRING, token.RAWSTRING:
		expr = &ast.StringLit{PosV: toks[0].Pos, EndV: toks[0].End, IsRaw: toks[0].Kind == token.RAWSTRING, IsTriple: toks[0].Triple, Parts: stringPartsToAST(toks[0].Parts)}
	case token.CHAR:
		expr = &ast.CharLit{PosV: toks[0].Pos, EndV: toks[0].End, Value: firstRune(decodedLiteral(toks[0].Value))}
	case token.BYTE:
		expr = &ast.ByteLit{PosV: toks[0].Pos, EndV: toks[0].End, Value: firstByte(decodedLiteral(toks[0].Value))}
	}
	if expr == nil {
		return &ast.Ident{PosV: toks[0].Pos, EndV: toks[len(toks)-1].End, Name: "__interp"}
	}
	for i := 1; i < len(toks); {
		if toks[i].Kind == token.LPAREN {
			close := findTokenClose(toks, i, token.LPAREN, token.RPAREN)
			call := &ast.CallExpr{PosV: expr.Pos(), Fn: expr}
			if close < 0 {
				call.EndV = toks[len(toks)-1].End
				return call
			}
			for _, span := range splitTokenArgs(toks[i+1 : close]) {
				if len(span) == 0 {
					continue
				}
				call.Args = append(call.Args, &ast.Arg{PosV: span[0].Pos, Value: tokensToExpr(span)})
			}
			call.EndV = toks[close].End
			expr = call
			i = close + 1
			continue
		}
		if i+1 < len(toks) && (toks[i].Kind == token.DOT || toks[i].Kind == token.QDOT) && toks[i+1].Kind == token.IDENT {
			expr = &ast.FieldExpr{PosV: expr.Pos(), EndV: toks[i+1].End, X: expr, Name: toks[i+1].Value, IsOptional: toks[i].Kind == token.QDOT}
			i += 2
			continue
		}
		break
	}
	return expr
}

func trimTokenNewlines(toks []token.Token) []token.Token {
	for len(toks) > 0 && (toks[0].Kind == token.NEWLINE || toks[0].Kind == token.EOF) {
		toks = toks[1:]
	}
	for len(toks) > 0 && (toks[len(toks)-1].Kind == token.NEWLINE || toks[len(toks)-1].Kind == token.EOF) {
		toks = toks[:len(toks)-1]
	}
	return toks
}

func splitTopLevelBinary(toks []token.Token) int {
	best := -1
	bestPrec := 100
	depth := 0
	for i, tk := range toks {
		switch tk.Kind {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
			continue
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		prec := tokenExprPrecedence(tk.Kind)
		if prec > 0 && prec <= bestPrec {
			best = i
			bestPrec = prec
		}
	}
	return best
}

func tokenExprPrecedence(k token.Kind) int {
	switch k {
	case token.OR:
		return 1
	case token.AND:
		return 2
	case token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ:
		return 3
	case token.PLUS, token.MINUS:
		return 4
	case token.STAR, token.SLASH, token.PERCENT:
		return 5
	}
	return 0
}

func findTokenClose(toks []token.Token, start int, open, close token.Kind) int {
	depth := 0
	for i := start; i < len(toks); i++ {
		if toks[i].Kind == open {
			depth++
		} else if toks[i].Kind == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTokenArgs(toks []token.Token) [][]token.Token {
	var out [][]token.Token
	start := 0
	depth := 0
	for i, tk := range toks {
		switch tk.Kind {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			if depth > 0 {
				depth--
			}
		case token.COMMA:
			if depth == 0 {
				if i > start {
					out = append(out, toks[start:i])
				}
				start = i + 1
			}
		}
	}
	if start < len(toks) {
		out = append(out, toks[start:])
	}
	return out
}

func splitPath(s string) []string {
	if s == "" {
		return nil
	}
	if strings.Contains(s, "/") {
		return []string{s}
	}
	return strings.Split(s, ".")
}

func unquoteMaybe(s string) string {
	if v, err := strconv.Unquote(s); err == nil {
		return v
	}
	return s
}

func decodedLiteral(s string) string {
	if strings.HasPrefix(s, "b") {
		s = s[1:]
	}
	switch {
	case len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'"):
		return decodeEscapes(s[1 : len(s)-1])
	case len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\""):
		return decodeEscapes(s[1 : len(s)-1])
	case len(s) >= 3 && strings.HasPrefix(s, `r"`) && strings.HasSuffix(s, `"`):
		return s[2 : len(s)-1]
	}
	return s
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func firstByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}
