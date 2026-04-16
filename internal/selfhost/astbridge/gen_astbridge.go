//go:build ignore

package main

import (
	"bytes"
	"fmt"
	goast "go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type aliasSpec struct {
	Name    string
	Target  string
	ASTName string
}

type listSpec struct {
	Name string
}

type compactSpec struct {
	Func string
	In   string
	Out  string
}

type tokenKindSpec struct {
	Func string
	Name string
}

var aliases = []aliasSpec{
	{Name: "File", Target: "*ast.File", ASTName: "File"},
	{Name: "Decl", Target: "ast.Decl", ASTName: "Decl"},
	{Name: "FnDecl", Target: "*ast.FnDecl", ASTName: "FnDecl"},
	{Name: "Stmt", Target: "ast.Stmt", ASTName: "Stmt"},
	{Name: "Expr", Target: "ast.Expr", ASTName: "Expr"},
	{Name: "Type", Target: "ast.Type", ASTName: "Type"},
	{Name: "Pattern", Target: "ast.Pattern", ASTName: "Pattern"},
	{Name: "Block", Target: "*ast.Block", ASTName: "Block"},
	{Name: "Annotation", Target: "*ast.Annotation", ASTName: "Annotation"},
	{Name: "AnnotationArg", Target: "*ast.AnnotationArg", ASTName: "AnnotationArg"},
	{Name: "GenericParam", Target: "*ast.GenericParam", ASTName: "GenericParam"},
	{Name: "Receiver", Target: "*ast.Receiver", ASTName: "Receiver"},
	{Name: "Param", Target: "*ast.Param", ASTName: "Param"},
	{Name: "Field", Target: "*ast.Field", ASTName: "Field"},
	{Name: "Variant", Target: "*ast.Variant", ASTName: "Variant"},
	{Name: "Arg", Target: "*ast.Arg", ASTName: "Arg"},
	{Name: "MapEntry", Target: "*ast.MapEntry", ASTName: "MapEntry"},
	{Name: "MatchArm", Target: "*ast.MatchArm", ASTName: "MatchArm"},
	{Name: "StructLitField", Target: "*ast.StructLitField", ASTName: "StructLitField"},
	{Name: "StructPatField", Target: "*ast.StructPatField", ASTName: "StructPatField"},
	{Name: "Pos", Target: "token.Pos"},
	{Name: "Token", Target: "token.Token"},
	{Name: "Kind", Target: "token.Kind"},
}

var nilSpecs = []listSpec{
	{Name: "Decl"},
	{Name: "FnDecl"},
	{Name: "Stmt"},
	{Name: "Expr"},
	{Name: "Type"},
	{Name: "Pattern"},
	{Name: "Block"},
	{Name: "Receiver"},
	{Name: "Param"},
	{Name: "Field"},
	{Name: "Variant"},
	{Name: "Annotation"},
	{Name: "AnnotationArg"},
	{Name: "GenericParam"},
	{Name: "Arg"},
	{Name: "MatchArm"},
	{Name: "StructLitField"},
	{Name: "StructPatField"},
}

var emptyListSpecs = []listSpec{
	{Name: "Decl"},
	{Name: "FnDecl"},
	{Name: "Stmt"},
	{Name: "Expr"},
	{Name: "Type"},
	{Name: "Pattern"},
	{Name: "Annotation"},
	{Name: "AnnotationArg"},
	{Name: "GenericParam"},
	{Name: "Param"},
	{Name: "Field"},
	{Name: "Variant"},
	{Name: "Arg"},
	{Name: "MapEntry"},
	{Name: "MatchArm"},
	{Name: "StructLitField"},
	{Name: "StructPatField"},
}

var compactSpecs = []compactSpec{
	{Func: "compactDecls", In: "Decl", Out: "ast.Decl"},
	{Func: "compactFnDecls", In: "FnDecl", Out: "*ast.FnDecl"},
	{Func: "compactStmts", In: "Stmt", Out: "ast.Stmt"},
	{Func: "compactExprs", In: "Expr", Out: "ast.Expr"},
	{Func: "compactTypes", In: "Type", Out: "ast.Type"},
	{Func: "compactPatterns", In: "Pattern", Out: "ast.Pattern"},
	{Func: "compactGenericParams", In: "GenericParam", Out: "*ast.GenericParam"},
	{Func: "compactParams", In: "Param", Out: "*ast.Param"},
	{Func: "compactFields", In: "Field", Out: "*ast.Field"},
	{Func: "compactVariants", In: "Variant", Out: "*ast.Variant"},
	{Func: "compactAnnotations", In: "Annotation", Out: "*ast.Annotation"},
	{Func: "compactAnnotationArgs", In: "AnnotationArg", Out: "*ast.AnnotationArg"},
	{Func: "compactArgs", In: "Arg", Out: "*ast.Arg"},
	{Func: "compactMapEntries", In: "MapEntry", Out: "*ast.MapEntry"},
	{Func: "compactMatchArms", In: "MatchArm", Out: "*ast.MatchArm"},
	{Func: "compactStructLitFields", In: "StructLitField", Out: "*ast.StructLitField"},
	{Func: "compactStructPatFields", In: "StructPatField", Out: "*ast.StructPatField"},
}

var tokenKindSpecs = []tokenKindSpec{
	{Func: "KindEOF", Name: "EOF"},
	{Func: "KindIllegal", Name: "ILLEGAL"},
	{Func: "KindNewline", Name: "NEWLINE"},
	{Func: "KindIdent", Name: "IDENT"},
	{Func: "KindInt", Name: "INT"},
	{Func: "KindFloat", Name: "FLOAT"},
	{Func: "KindChar", Name: "CHAR"},
	{Func: "KindByte", Name: "BYTE"},
	{Func: "KindString", Name: "STRING"},
	{Func: "KindRawString", Name: "RAWSTRING"},
	{Func: "KindFn", Name: "FN"},
	{Func: "KindStruct", Name: "STRUCT"},
	{Func: "KindEnum", Name: "ENUM"},
	{Func: "KindInterface", Name: "INTERFACE"},
	{Func: "KindType", Name: "TYPE"},
	{Func: "KindLet", Name: "LET"},
	{Func: "KindMut", Name: "MUT"},
	{Func: "KindPub", Name: "PUB"},
	{Func: "KindIf", Name: "IF"},
	{Func: "KindElse", Name: "ELSE"},
	{Func: "KindMatch", Name: "MATCH"},
	{Func: "KindFor", Name: "FOR"},
	{Func: "KindBreak", Name: "BREAK"},
	{Func: "KindContinue", Name: "CONTINUE"},
	{Func: "KindReturn", Name: "RETURN"},
	{Func: "KindUse", Name: "USE"},
	{Func: "KindDefer", Name: "DEFER"},
	{Func: "KindLParen", Name: "LPAREN"},
	{Func: "KindRParen", Name: "RPAREN"},
	{Func: "KindLBrace", Name: "LBRACE"},
	{Func: "KindRBrace", Name: "RBRACE"},
	{Func: "KindLBracket", Name: "LBRACKET"},
	{Func: "KindRBracket", Name: "RBRACKET"},
	{Func: "KindComma", Name: "COMMA"},
	{Func: "KindColon", Name: "COLON"},
	{Func: "KindSemicolon", Name: "SEMICOLON"},
	{Func: "KindDot", Name: "DOT"},
	{Func: "KindPlus", Name: "PLUS"},
	{Func: "KindMinus", Name: "MINUS"},
	{Func: "KindStar", Name: "STAR"},
	{Func: "KindSlash", Name: "SLASH"},
	{Func: "KindPercent", Name: "PERCENT"},
	{Func: "KindEq", Name: "EQ"},
	{Func: "KindNeq", Name: "NEQ"},
	{Func: "KindLt", Name: "LT"},
	{Func: "KindGt", Name: "GT"},
	{Func: "KindLeq", Name: "LEQ"},
	{Func: "KindGeq", Name: "GEQ"},
	{Func: "KindAnd", Name: "AND"},
	{Func: "KindOr", Name: "OR"},
	{Func: "KindNot", Name: "NOT"},
	{Func: "KindBitAnd", Name: "BITAND"},
	{Func: "KindBitOr", Name: "BITOR"},
	{Func: "KindBitXor", Name: "BITXOR"},
	{Func: "KindBitNot", Name: "BITNOT"},
	{Func: "KindShl", Name: "SHL"},
	{Func: "KindShr", Name: "SHR"},
	{Func: "KindAssign", Name: "ASSIGN"},
	{Func: "KindPlusEq", Name: "PLUSEQ"},
	{Func: "KindMinusEq", Name: "MINUSEQ"},
	{Func: "KindStarEq", Name: "STAREQ"},
	{Func: "KindSlashEq", Name: "SLASHEQ"},
	{Func: "KindPercentEq", Name: "PERCENTEQ"},
	{Func: "KindBitAndEq", Name: "BITANDEQ"},
	{Func: "KindBitOrEq", Name: "BITOREQ"},
	{Func: "KindBitXorEq", Name: "BITXOREQ"},
	{Func: "KindShlEq", Name: "SHLEQ"},
	{Func: "KindShrEq", Name: "SHREQ"},
	{Func: "KindQuestion", Name: "QUESTION"},
	{Func: "KindQDot", Name: "QDOT"},
	{Func: "KindQQ", Name: "QQ"},
	{Func: "KindDotDot", Name: "DOTDOT"},
	{Func: "KindDotDotEq", Name: "DOTDOTEQ"},
	{Func: "KindArrow", Name: "ARROW"},
	{Func: "KindChanArrow", Name: "CHANARROW"},
	{Func: "KindColonColon", Name: "COLONCOLON"},
	{Func: "KindUnderscore", Name: "UNDERSCORE"},
	{Func: "KindAt", Name: "AT"},
	{Func: "KindHash", Name: "HASH"},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	types, err := astTypes(filepath.Join(root, "internal/ast/ast.go"))
	if err != nil {
		return err
	}
	if err := validate(types); err != nil {
		return err
	}
	src, err := generate()
	if err != nil {
		return err
	}
	out := filepath.Join(root, "internal/selfhost/astbridge/generated.go")
	return os.WriteFile(out, src, 0o644)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", dir)
		}
		dir = parent
	}
}

func astTypes(path string) (map[string]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	types := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*goast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*goast.TypeSpec)
			if ok {
				types[ts.Name.Name] = true
			}
		}
	}
	return types, nil
}

func validate(types map[string]bool) error {
	var missing []string
	for _, a := range aliases {
		if a.ASTName != "" && !types[a.ASTName] {
			missing = append(missing, a.ASTName)
		}
	}
	for _, c := range compactSpecs {
		name := strings.TrimPrefix(strings.TrimPrefix(c.Out, "*ast."), "ast.")
		if name != c.Out && !types[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("internal/ast missing bridge types: %s", strings.Join(missing, ", "))
}

func generate() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/selfhost/astbridge/gen_astbridge.go from internal/ast/ast.go. DO NOT EDIT.\n\n")
	b.WriteString("package astbridge\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"github.com/osty/osty/internal/ast\"\n")
	b.WriteString("\t\"github.com/osty/osty/internal/token\"\n")
	b.WriteString(")\n\n")
	emitAliases(&b)
	emitNilHelpers(&b)
	emitEmptyLists(&b)
	emitTokenKindHelpers(&b)
	b.WriteString(constructorBody)
	emitCompactHelpers(&b)
	return format.Source(b.Bytes())
}

func emitAliases(b *bytes.Buffer) {
	b.WriteString("type (\n")
	for _, a := range aliases {
		fmt.Fprintf(b, "\t%-16s = %s\n", a.Name, a.Target)
	}
	b.WriteString(")\n\n")
}

func emitNilHelpers(b *bytes.Buffer) {
	for _, s := range nilSpecs {
		fmt.Fprintf(b, "func Nil%s() %s { return nil }\n", s.Name, s.Name)
	}
	for _, s := range nilSpecs {
		fmt.Fprintf(b, "func IsNil%s(v %s) bool { return v == nil }\n", s.Name, s.Name)
	}
	b.WriteByte('\n')
}

func emitEmptyLists(b *bytes.Buffer) {
	for _, s := range emptyListSpecs {
		fmt.Fprintf(b, "func Empty%sList() []%s { return nil }\n", s.Name, s.Name)
	}
	b.WriteByte('\n')
}

func emitTokenKindHelpers(b *bytes.Buffer) {
	for _, k := range tokenKindSpecs {
		fmt.Fprintf(b, "func %s() Kind { return token.%s }\n", k.Func, k.Name)
	}
	b.WriteByte('\n')
}

func emitCompactHelpers(b *bytes.Buffer) {
	for _, s := range compactSpecs {
		fmt.Fprintf(b, "func %s(in []%s) []%s {\n", s.Func, s.In, s.Out)
		fmt.Fprintf(b, "\tout := make([]%s, 0, len(in))\n", s.Out)
		b.WriteString("\tfor _, v := range in {\n")
		b.WriteString("\t\tif v != nil {\n")
		b.WriteString("\t\t\tout = append(out, v)\n")
		b.WriteString("\t\t}\n")
		b.WriteString("\t}\n")
		b.WriteString("\treturn out\n")
		b.WriteString("}\n\n")
	}
	b.WriteString(`func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
`)
}

const constructorBody = `
func FileNode(pos, end Pos, uses []Decl, decls []Decl, stmts []Stmt) File {
	f := &ast.File{PosV: pos, EndV: end, Stmts: compactStmts(stmts)}
	for _, d := range uses {
		if u, ok := d.(*ast.UseDecl); ok && u != nil {
			f.Uses = append(f.Uses, u)
		}
	}
	f.Decls = compactDecls(decls)
	return f
}

func FnDeclNode(pos, end Pos, pub bool, name string, generics []GenericParam, recv Receiver, params []Param, ret Type, body Block, doc string, anns []Annotation) FnDecl {
	return &ast.FnDecl{
		PosV:        pos,
		EndV:        end,
		Pub:         pub,
		Name:        name,
		Generics:    compactGenericParams(generics),
		Recv:        recv,
		Params:      compactParams(params),
		ReturnType:  ret,
		Body:        body,
		DocComment:  doc,
		Annotations: compactAnnotations(anns),
	}
}

func FnDeclAsDecl(fn FnDecl) Decl {
	if fn == nil {
		return nil
	}
	return fn
}

func ReceiverNode(pos, end Pos, mut bool, mutPos Pos) Receiver {
	return &ast.Receiver{PosV: pos, EndV: end, Mut: mut, MutPos: mutPos}
}

func StructDeclNode(pos, end Pos, pub bool, name string, generics []GenericParam, fields []Field, methods []FnDecl, doc string, anns []Annotation) Decl {
	return &ast.StructDecl{
		PosV:        pos,
		EndV:        end,
		Pub:         pub,
		Name:        name,
		Generics:    compactGenericParams(generics),
		Fields:      compactFields(fields),
		Methods:     compactFnDecls(methods),
		DocComment:  doc,
		Annotations: compactAnnotations(anns),
	}
}

func EnumDeclNode(pos, end Pos, pub bool, name string, generics []GenericParam, variants []Variant, methods []FnDecl, doc string, anns []Annotation) Decl {
	return &ast.EnumDecl{
		PosV:        pos,
		EndV:        end,
		Pub:         pub,
		Name:        name,
		Generics:    compactGenericParams(generics),
		Variants:    compactVariants(variants),
		Methods:     compactFnDecls(methods),
		DocComment:  doc,
		Annotations: compactAnnotations(anns),
	}
}

func InterfaceDeclNode(pos, end Pos, pub bool, name string, generics []GenericParam, extends []Type, methods []FnDecl, doc string, anns []Annotation) Decl {
	return &ast.InterfaceDecl{
		PosV:        pos,
		EndV:        end,
		Pub:         pub,
		Name:        name,
		Generics:    compactGenericParams(generics),
		Extends:     compactTypes(extends),
		Methods:     compactFnDecls(methods),
		DocComment:  doc,
		Annotations: compactAnnotations(anns),
	}
}

func TypeAliasDeclNode(pos, end Pos, pub bool, name string, generics []GenericParam, target Type, doc string, anns []Annotation) Decl {
	return &ast.TypeAliasDecl{
		PosV:        pos,
		EndV:        end,
		Pub:         pub,
		Name:        name,
		Generics:    compactGenericParams(generics),
		Target:      target,
		DocComment:  doc,
		Annotations: compactAnnotations(anns),
	}
}

func UseDeclNode(pos, end Pos, raw string, path []string, isGo bool, alias string, body []Decl) Decl {
	u := &ast.UseDecl{PosV: pos, EndV: end, Path: compactStrings(path), RawPath: raw, Alias: alias, IsGoFFI: isGo, GoBody: compactDecls(body)}
	if isGo {
		u.GoPath = raw
	}
	return u
}

func LetDeclNode(pos, end Pos, pub, mut bool, mutPos Pos, name string, typ Type, value Expr, doc string, anns []Annotation) Decl {
	return &ast.LetDecl{
		PosV:        pos,
		EndV:        end,
		Pub:         pub,
		Mut:         mut,
		MutPos:      mutPos,
		Name:        name,
		Type:        typ,
		Value:       value,
		DocComment:  doc,
		Annotations: compactAnnotations(anns),
	}
}

func FieldNode(pos, end Pos, pub bool, name string, typ Type, def Expr, doc string, anns []Annotation) Field {
	return &ast.Field{PosV: pos, EndV: end, Pub: pub, Name: name, Type: typ, Default: def, DocComment: doc, Annotations: compactAnnotations(anns)}
}

func VariantNode(pos, end Pos, name string, fields []Type, anns []Annotation, doc string) Variant {
	return &ast.Variant{PosV: pos, EndV: end, Name: name, Fields: compactTypes(fields), Annotations: compactAnnotations(anns), DocComment: doc}
}

func ParamNode(pos, end Pos, name string, pat Pattern, typ Type, def Expr) Param {
	return &ast.Param{PosV: pos, EndV: end, Name: name, Pattern: pat, Type: typ, Default: def}
}

func GenericParamNode(pos, end Pos, name string, constraints []Type) GenericParam {
	return &ast.GenericParam{PosV: pos, EndV: end, Name: name, Constraints: compactTypes(constraints)}
}

func AnnotationNode(pos, end Pos, name string, args []AnnotationArg) Annotation {
	return &ast.Annotation{PosV: pos, EndV: end, Name: name, Args: compactAnnotationArgs(args)}
}

func AnnotationArgNode(pos Pos, key string, value Expr) AnnotationArg {
	return &ast.AnnotationArg{PosV: pos, Key: key, Value: value}
}

func NamedTypeNode(pos, end Pos, path []string, args []Type) Type {
	return &ast.NamedType{PosV: pos, EndV: end, Path: compactStrings(path), Args: compactTypes(args)}
}

func OptionalTypeNode(pos, end Pos, inner Type) Type {
	return &ast.OptionalType{PosV: pos, EndV: end, Inner: inner}
}

func TupleTypeNode(pos, end Pos, elems []Type) Type {
	return &ast.TupleType{PosV: pos, EndV: end, Elems: compactTypes(elems)}
}

func FnTypeNode(pos, end Pos, params []Type, ret Type) Type {
	return &ast.FnType{PosV: pos, EndV: end, Params: compactTypes(params), ReturnType: ret}
}

func BlockNode(pos, end Pos, stmts []Stmt) Block {
	return &ast.Block{PosV: pos, EndV: end, Stmts: compactStmts(stmts)}
}

func BlockAsStmt(b Block) Stmt {
	if b == nil {
		return nil
	}
	return b
}

func BlockAsExpr(b Block) Expr {
	if b == nil {
		return nil
	}
	return b
}

func LetStmtNode(pos, end Pos, pat Pattern, mut bool, mutPos Pos, typ Type, value Expr) Stmt {
	return &ast.LetStmt{PosV: pos, EndV: end, Pattern: pat, Mut: mut, MutPos: mutPos, Type: typ, Value: value}
}

func ReturnStmtNode(pos, end Pos, value Expr) Stmt {
	return &ast.ReturnStmt{PosV: pos, EndV: end, Value: value}
}

func BreakStmtNode(pos, end Pos) Stmt {
	return &ast.BreakStmt{PosV: pos, EndV: end}
}

func ContinueStmtNode(pos, end Pos) Stmt {
	return &ast.ContinueStmt{PosV: pos, EndV: end}
}

func DeferStmtNode(pos, end Pos, x Expr) Stmt {
	return &ast.DeferStmt{PosV: pos, EndV: end, X: x}
}

func ForStmtNode(pos, end Pos, isForLet bool, pat Pattern, iter Expr, body Block) Stmt {
	return &ast.ForStmt{PosV: pos, EndV: end, IsForLet: isForLet, Pattern: pat, Iter: iter, Body: body}
}

func AssignStmtNode(pos, end Pos, op Kind, target Expr, value Expr) Stmt {
	return &ast.AssignStmt{PosV: pos, EndV: end, Op: op, Targets: []ast.Expr{target}, Value: value}
}

func ChanSendStmtNode(pos, end Pos, channel, value Expr) Stmt {
	return &ast.ChanSendStmt{PosV: pos, EndV: end, Channel: channel, Value: value}
}

func ExprStmtNode(x Expr) Stmt {
	if x == nil {
		return nil
	}
	return &ast.ExprStmt{X: x}
}

func IdentExpr(pos, end Pos, name string) Expr {
	return &ast.Ident{PosV: pos, EndV: end, Name: name}
}

func IntLitExpr(pos, end Pos, text string) Expr {
	return &ast.IntLit{PosV: pos, EndV: end, Text: text}
}

func FloatLitExpr(pos, end Pos, text string) Expr {
	return &ast.FloatLit{PosV: pos, EndV: end, Text: text}
}

func BoolLitExpr(pos, end Pos, value bool) Expr {
	return &ast.BoolLit{PosV: pos, EndV: end, Value: value}
}

func CharLitExpr(pos, end Pos, value string) Expr {
	return &ast.CharLit{PosV: pos, EndV: end, Value: firstRune(value)}
}

func ByteLitExpr(pos, end Pos, value string) Expr {
	return &ast.ByteLit{PosV: pos, EndV: end, Value: firstByte(value)}
}

func StringLitFromToken(pos, end Pos, tok Token) Expr {
	return &ast.StringLit{PosV: pos, EndV: end, IsRaw: tok.Kind == token.RAWSTRING, IsTriple: tok.Triple, Parts: stringPartsToAST(tok.Parts)}
}

func StringLitExpr(pos, end Pos, value string) Expr {
	return &ast.StringLit{PosV: pos, EndV: end, Parts: []ast.StringPart{{IsLit: true, Lit: value}}}
}

func UnaryExprNode(pos, end Pos, op Kind, x Expr) Expr {
	return &ast.UnaryExpr{PosV: pos, EndV: end, Op: op, X: x}
}

func BinaryExprNode(pos, end Pos, op Kind, left, right Expr) Expr {
	return &ast.BinaryExpr{PosV: pos, EndV: end, Op: op, Left: left, Right: right}
}

func QuestionExprNode(pos, end Pos, x Expr) Expr {
	return &ast.QuestionExpr{PosV: pos, EndV: end, X: x}
}

func CallExprNode(pos, end Pos, fn Expr, args []Arg) Expr {
	return &ast.CallExpr{PosV: pos, EndV: end, Fn: fn, Args: compactArgs(args)}
}

func FieldExprNode(pos, end Pos, x Expr, name string, optional bool) Expr {
	return &ast.FieldExpr{PosV: pos, EndV: end, X: x, Name: name, IsOptional: optional}
}

func IndexExprNode(pos, end Pos, x, index Expr) Expr {
	return &ast.IndexExpr{PosV: pos, EndV: end, X: x, Index: index}
}

func TurbofishExprNode(pos, end Pos, base Expr, args []Type) Expr {
	return &ast.TurbofishExpr{PosV: pos, EndV: end, Base: base, Args: compactTypes(args)}
}

func RangeExprNode(pos, end Pos, start, stop Expr, inclusive bool) Expr {
	return &ast.RangeExpr{PosV: pos, EndV: end, Start: start, Stop: stop, Inclusive: inclusive}
}

func ParenExprNode(pos, end Pos, x Expr) Expr {
	return &ast.ParenExpr{PosV: pos, EndV: end, X: x}
}

func TupleExprNode(pos, end Pos, elems []Expr) Expr {
	return &ast.TupleExpr{PosV: pos, EndV: end, Elems: compactExprs(elems)}
}

func ListExprNode(pos, end Pos, elems []Expr) Expr {
	return &ast.ListExpr{PosV: pos, EndV: end, Elems: compactExprs(elems)}
}

func MapEntryNode(key, value Expr) MapEntry {
	return &ast.MapEntry{Key: key, Value: value}
}

func MapExprNode(pos, end Pos, entries []MapEntry, empty bool) Expr {
	return &ast.MapExpr{PosV: pos, EndV: end, Entries: compactMapEntries(entries), Empty: empty}
}

func StructLitFieldNode(pos Pos, name string, value Expr) StructLitField {
	return &ast.StructLitField{PosV: pos, Name: name, Value: value}
}

func StructLitNode(pos, end Pos, typ Expr, fields []StructLitField, spread Expr) Expr {
	return &ast.StructLit{PosV: pos, EndV: end, Type: typ, Fields: compactStructLitFields(fields), Spread: spread}
}

func IfExprNode(pos, end Pos, isIfLet bool, pat Pattern, cond Expr, then Block, alt Expr) Expr {
	return &ast.IfExpr{PosV: pos, EndV: end, IsIfLet: isIfLet, Pattern: pat, Cond: cond, Then: then, Else: alt}
}

func MatchArmNode(pos Pos, pat Pattern, guard, body Expr) MatchArm {
	return &ast.MatchArm{PosV: pos, Pattern: pat, Guard: guard, Body: body}
}

func MatchExprNode(pos, end Pos, scrutinee Expr, arms []MatchArm) Expr {
	return &ast.MatchExpr{PosV: pos, EndV: end, Scrutinee: scrutinee, Arms: compactMatchArms(arms)}
}

func ClosureExprNode(pos, end Pos, params []Param, ret Type, body Expr) Expr {
	return &ast.ClosureExpr{PosV: pos, EndV: end, Params: compactParams(params), ReturnType: ret, Body: body}
}

func ArgNode(pos Pos, name string, value Expr) Arg {
	return &ast.Arg{PosV: pos, Name: name, Value: value}
}

func IdentPatNode(pos, end Pos, name string) Pattern {
	return &ast.IdentPat{PosV: pos, EndV: end, Name: name}
}

func WildcardPatNode(pos, end Pos) Pattern {
	return &ast.WildcardPat{PosV: pos, EndV: end}
}

func LiteralPatNode(pos, end Pos, lit Expr) Pattern {
	return &ast.LiteralPat{PosV: pos, EndV: end, Literal: lit}
}

func TuplePatNode(pos, end Pos, elems []Pattern) Pattern {
	return &ast.TuplePat{PosV: pos, EndV: end, Elems: compactPatterns(elems)}
}

func VariantPatNode(pos, end Pos, path []string, args []Pattern) Pattern {
	return &ast.VariantPat{PosV: pos, EndV: end, Path: compactStrings(path), Args: compactPatterns(args)}
}

func StructPatFieldNode(pos Pos, name string, pat Pattern) StructPatField {
	return &ast.StructPatField{PosV: pos, Name: name, Pattern: pat}
}

func StructPatNode(pos, end Pos, typ []string, fields []StructPatField, rest bool) Pattern {
	return &ast.StructPat{PosV: pos, EndV: end, Type: compactStrings(typ), Fields: compactStructPatFields(fields), Rest: rest}
}

func BindingPatNode(pos, end Pos, name string, pat Pattern) Pattern {
	return &ast.BindingPat{PosV: pos, EndV: end, Name: name, Pattern: pat}
}

func RangePatNode(pos, end Pos, start, stop Expr, inclusive bool) Pattern {
	return &ast.RangePat{PosV: pos, EndV: end, Start: start, Stop: stop, Inclusive: inclusive}
}

func OrPatNode(pos, end Pos, alts []Pattern) Pattern {
	return &ast.OrPat{PosV: pos, EndV: end, Alts: compactPatterns(alts)}
}

func ExprPos(e Expr, fallback Pos) Pos {
	if e == nil {
		return fallback
	}
	return e.Pos()
}

func ExprEnd(e Expr, fallback Pos) Pos {
	if e == nil {
		return fallback
	}
	return e.End()
}

`
