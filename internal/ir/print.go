package ir

import (
	"fmt"
	"strconv"
	"strings"
)

// Print renders a Module to an S-expression-like debug form useful for
// tests and visual inspection. The output is intentionally compact and
// stable — not a source formatter. For pretty-printed Osty, reparse
// the generated output through `internal/format`.
func Print(m *Module) string {
	p := &printer{indent: 0}
	p.printModule(m)
	return p.b.String()
}

// PrintNode renders any single IR node using the same format as Print.
func PrintNode(n Node) string {
	p := &printer{indent: 0}
	p.printNode(n)
	return p.b.String()
}

type printer struct {
	b      strings.Builder
	indent int
}

func (p *printer) writef(format string, args ...any) {
	fmt.Fprintf(&p.b, format, args...)
}

func (p *printer) nl() {
	p.b.WriteByte('\n')
	for i := 0; i < p.indent; i++ {
		p.b.WriteString("  ")
	}
}

func (p *printer) printModule(m *Module) {
	if m == nil {
		p.b.WriteString("(nil module)")
		return
	}
	p.writef("(module %q", m.Package)
	p.indent++
	for _, d := range m.Decls {
		p.nl()
		p.printDecl(d)
	}
	if len(m.Script) > 0 {
		p.nl()
		p.b.WriteString("(script")
		p.indent++
		for _, s := range m.Script {
			p.nl()
			p.printStmt(s)
		}
		p.indent--
		p.b.WriteByte(')')
	}
	p.indent--
	p.b.WriteByte(')')
}

func (p *printer) printDecl(d Decl) {
	switch d := d.(type) {
	case *FnDecl:
		p.writef("(fn %s", d.Name)
		if d.Exported {
			p.b.WriteString(" pub")
		}
		if len(d.Generics) > 0 {
			p.b.WriteString(" [")
			for i, g := range d.Generics {
				if i > 0 {
					p.b.WriteByte(' ')
				}
				p.b.WriteString(g.Name)
			}
			p.b.WriteByte(']')
		}
		p.b.WriteString(" (")
		for i, pm := range d.Params {
			if i > 0 {
				p.b.WriteByte(' ')
			}
			p.writef("%s:%s", pm.Name, typeString(pm.Type))
		}
		p.writef(") -> %s", typeString(d.Return))
		if d.Body != nil {
			p.indent++
			p.nl()
			p.printBlock(d.Body)
			p.indent--
		}
		p.b.WriteByte(')')
	case *StructDecl:
		p.writef("(struct %s", d.Name)
		if d.Exported {
			p.b.WriteString(" pub")
		}
		p.indent++
		for _, f := range d.Fields {
			p.nl()
			p.writef("(field %s %s)", f.Name, typeString(f.Type))
		}
		for _, m := range d.Methods {
			p.nl()
			p.printDecl(m)
		}
		p.indent--
		p.b.WriteByte(')')
	case *EnumDecl:
		p.writef("(enum %s", d.Name)
		p.indent++
		for _, v := range d.Variants {
			p.nl()
			p.writef("(variant %s", v.Name)
			for _, pl := range v.Payload {
				p.writef(" %s", typeString(pl))
			}
			p.b.WriteByte(')')
		}
		for _, m := range d.Methods {
			p.nl()
			p.printDecl(m)
		}
		p.indent--
		p.b.WriteByte(')')
	case *InterfaceDecl:
		p.writef("(interface %s", d.Name)
		p.indent++
		for _, m := range d.Methods {
			p.nl()
			p.printDecl(m)
		}
		p.indent--
		p.b.WriteByte(')')
	case *TypeAliasDecl:
		p.writef("(alias %s %s)", d.Name, typeString(d.Target))
	case *LetDecl:
		p.writef("(let %s %s ", d.Name, typeString(d.Type))
		p.printExpr(d.Value)
		p.b.WriteByte(')')
	case *UseDecl:
		path := strings.Join(d.Path, ".")
		if d.IsGoFFI {
			path = d.GoPath
		} else if d.IsRuntimeFFI {
			path = d.RuntimePath
		}
		p.writef("(use %s", path)
		if d.Alias != "" && (len(d.Path) == 0 || d.Alias != d.Path[len(d.Path)-1]) {
			p.writef(" as=%s", d.Alias)
		}
		p.b.WriteByte(')')
	default:
		p.writef("(decl %T)", d)
	}
}

func (p *printer) printBlock(b *Block) {
	if b == nil {
		p.b.WriteString("(block)")
		return
	}
	p.b.WriteString("(block")
	p.indent++
	for _, s := range b.Stmts {
		p.nl()
		p.printStmt(s)
	}
	if b.Result != nil {
		p.nl()
		p.b.WriteString("(result ")
		p.printExpr(b.Result)
		p.b.WriteByte(')')
	}
	p.indent--
	p.b.WriteByte(')')
}

func (p *printer) printStmt(s Stmt) {
	switch s := s.(type) {
	case *Block:
		p.printBlock(s)
	case *LetStmt:
		if s.Pattern != nil {
			p.writef("(let-pat %s %s ", PatternString(s.Pattern), typeString(s.Type))
		} else {
			p.writef("(let %s %s ", s.Name, typeString(s.Type))
		}
		if s.Value != nil {
			p.printExpr(s.Value)
		} else {
			p.b.WriteString("nil")
		}
		p.b.WriteByte(')')
	case *ExprStmt:
		p.b.WriteString("(stmt ")
		p.printExpr(s.X)
		p.b.WriteByte(')')
	case *AssignStmt:
		p.writef("(assign %s", assignOpName(s.Op))
		for _, t := range s.Targets {
			p.b.WriteByte(' ')
			p.printExpr(t)
		}
		p.b.WriteString(" := ")
		p.printExpr(s.Value)
		p.b.WriteByte(')')
	case *ReturnStmt:
		p.b.WriteString("(return")
		if s.Value != nil {
			p.b.WriteByte(' ')
			p.printExpr(s.Value)
		}
		p.b.WriteByte(')')
	case *BreakStmt:
		p.b.WriteString("(break)")
	case *ContinueStmt:
		p.b.WriteString("(continue)")
	case *IfStmt:
		p.b.WriteString("(if ")
		p.printExpr(s.Cond)
		p.indent++
		p.nl()
		p.printBlock(s.Then)
		if s.Else != nil {
			p.nl()
			p.b.WriteString("else ")
			p.printBlock(s.Else)
		}
		p.indent--
		p.b.WriteByte(')')
	case *ForStmt:
		p.writef("(for %s", forKindName(s.Kind))
		if s.Var != "" {
			p.writef(" %s", s.Var)
		}
		p.indent++
		if s.Cond != nil {
			p.nl()
			p.b.WriteString("cond=")
			p.printExpr(s.Cond)
		}
		if s.Iter != nil {
			p.nl()
			p.b.WriteString("iter=")
			p.printExpr(s.Iter)
		}
		if s.Start != nil {
			p.nl()
			p.b.WriteString("start=")
			p.printExpr(s.Start)
		}
		if s.End != nil {
			p.nl()
			p.b.WriteString("end=")
			p.printExpr(s.End)
		}
		if s.Body != nil {
			p.nl()
			p.printBlock(s.Body)
		}
		p.indent--
		p.b.WriteByte(')')
	case *DeferStmt:
		p.b.WriteString("(defer ")
		p.printBlock(s.Body)
		p.b.WriteByte(')')
	case *ChanSendStmt:
		p.b.WriteString("(send ")
		p.printExpr(s.Channel)
		p.b.WriteByte(' ')
		p.printExpr(s.Value)
		p.b.WriteByte(')')
	case *MatchStmt:
		p.b.WriteString("(match-stmt ")
		p.printExpr(s.Scrutinee)
		p.indent++
		for _, arm := range s.Arms {
			p.nl()
			p.printMatchArm(arm)
		}
		p.indent--
		p.b.WriteByte(')')
	case *ErrorStmt:
		p.writef("(err-stmt %q)", s.Note)
	default:
		p.writef("(stmt? %T)", s)
	}
}

func (p *printer) printExpr(e Expr) {
	switch e := e.(type) {
	case nil:
		p.b.WriteString("nil")
	case *IntLit:
		p.b.WriteString(e.Text)
	case *FloatLit:
		p.b.WriteString(e.Text)
	case *BoolLit:
		if e.Value {
			p.b.WriteString("true")
		} else {
			p.b.WriteString("false")
		}
	case *CharLit:
		p.writef("'%c'", e.Value)
	case *ByteLit:
		p.writef("b%d", e.Value)
	case *StringLit:
		p.b.WriteByte('"')
		for _, part := range e.Parts {
			if part.IsLit {
				p.b.WriteString(strings.ReplaceAll(part.Lit, "\"", "\\\""))
			} else {
				p.b.WriteByte('{')
				p.printExpr(part.Expr)
				p.b.WriteByte('}')
			}
		}
		p.b.WriteByte('"')
	case *UnitLit:
		p.b.WriteString("()")
	case *Ident:
		p.b.WriteString(e.Name)
		if e.Kind != IdentUnknown {
			p.writef("#%s", identKindName(e.Kind))
		}
	case *UnaryExpr:
		p.writef("(%s ", unOpName(e.Op))
		p.printExpr(e.X)
		p.b.WriteByte(')')
	case *BinaryExpr:
		p.writef("(%s ", binOpName(e.Op))
		p.printExpr(e.Left)
		p.b.WriteByte(' ')
		p.printExpr(e.Right)
		p.b.WriteByte(')')
	case *CallExpr:
		p.b.WriteString("(call ")
		p.printExpr(e.Callee)
		for _, a := range e.Args {
			p.b.WriteByte(' ')
			p.printExpr(a)
		}
		p.b.WriteByte(')')
	case *IntrinsicCall:
		p.writef("(intrinsic %s", intrinsicKindName(e.Kind))
		for _, a := range e.Args {
			p.b.WriteByte(' ')
			p.printExpr(a)
		}
		p.b.WriteByte(')')
	case *MethodCall:
		p.b.WriteString("(mcall ")
		p.printExpr(e.Receiver)
		p.writef(" %s", e.Name)
		if len(e.TypeArgs) > 0 {
			p.b.WriteString(" ::<")
			for i, ta := range e.TypeArgs {
				if i > 0 {
					p.b.WriteByte(',')
				}
				p.b.WriteString(typeString(ta))
			}
			p.b.WriteByte('>')
		}
		for _, a := range e.Args {
			p.b.WriteByte(' ')
			p.printExpr(a)
		}
		p.b.WriteByte(')')
	case *ListLit:
		p.b.WriteString("[")
		for i, el := range e.Elems {
			if i > 0 {
				p.b.WriteString(" ")
			}
			p.printExpr(el)
		}
		p.b.WriteByte(']')
	case *MapLit:
		p.b.WriteString("{")
		for i, en := range e.Entries {
			if i > 0 {
				p.b.WriteString(", ")
			}
			p.printExpr(en.Key)
			p.b.WriteString(":")
			p.printExpr(en.Value)
		}
		p.b.WriteByte('}')
	case *TupleLit:
		p.b.WriteByte('(')
		for i, el := range e.Elems {
			if i > 0 {
				p.b.WriteString(", ")
			}
			p.printExpr(el)
		}
		p.b.WriteByte(')')
	case *StructLit:
		p.writef("(%s {", e.TypeName)
		for i, f := range e.Fields {
			if i > 0 {
				p.b.WriteString(", ")
			}
			p.writef("%s:", f.Name)
			if f.Value != nil {
				p.printExpr(f.Value)
			} else {
				p.b.WriteString(f.Name)
			}
		}
		if e.Spread != nil {
			p.b.WriteString(" ..")
			p.printExpr(e.Spread)
		}
		p.b.WriteString("})")
	case *RangeLit:
		if e.Start != nil {
			p.printExpr(e.Start)
		}
		if e.Inclusive {
			p.b.WriteString("..=")
		} else {
			p.b.WriteString("..")
		}
		if e.End != nil {
			p.printExpr(e.End)
		}
	case *QuestionExpr:
		p.b.WriteString("(?")
		p.b.WriteByte(' ')
		p.printExpr(e.X)
		p.b.WriteByte(')')
	case *CoalesceExpr:
		p.b.WriteString("(?? ")
		p.printExpr(e.Left)
		p.b.WriteByte(' ')
		p.printExpr(e.Right)
		p.b.WriteByte(')')
	case *FieldExpr:
		p.b.WriteByte('(')
		if e.Optional {
			p.b.WriteString("?.")
		} else {
			p.b.WriteByte('.')
		}
		p.b.WriteByte(' ')
		p.printExpr(e.X)
		p.writef(" %s)", e.Name)
	case *TupleAccess:
		p.b.WriteString("(.")
		p.writef("%d ", e.Index)
		p.printExpr(e.X)
		p.b.WriteByte(')')
	case *IndexExpr:
		p.b.WriteString("(idx ")
		p.printExpr(e.X)
		p.b.WriteByte(' ')
		p.printExpr(e.Index)
		p.b.WriteByte(')')
	case *Closure:
		p.b.WriteString("(\\(")
		for i, pm := range e.Params {
			if i > 0 {
				p.b.WriteByte(' ')
			}
			p.writef("%s:%s", pm.Name, typeString(pm.Type))
		}
		p.writef(") -> %s ", typeString(e.Return))
		p.printBlock(e.Body)
		p.b.WriteByte(')')
	case *VariantLit:
		p.b.WriteByte('(')
		if e.Enum != "" {
			p.writef("%s.", e.Enum)
		}
		p.b.WriteString(e.Variant)
		for _, a := range e.Args {
			p.b.WriteByte(' ')
			p.printExpr(a)
		}
		p.b.WriteByte(')')
	case *BlockExpr:
		p.printBlock(e.Block)
	case *IfExpr:
		p.b.WriteString("(if-expr ")
		p.printExpr(e.Cond)
		p.b.WriteByte(' ')
		p.printBlock(e.Then)
		if e.Else != nil {
			p.b.WriteString(" else ")
			p.printBlock(e.Else)
		}
		p.b.WriteByte(')')
	case *IfLetExpr:
		p.writef("(if-let %s ", PatternString(e.Pattern))
		p.printExpr(e.Scrutinee)
		p.b.WriteByte(' ')
		p.printBlock(e.Then)
		if e.Else != nil {
			p.b.WriteString(" else ")
			p.printBlock(e.Else)
		}
		p.b.WriteByte(')')
	case *MatchExpr:
		p.b.WriteString("(match ")
		p.printExpr(e.Scrutinee)
		p.indent++
		for _, arm := range e.Arms {
			p.nl()
			p.printMatchArm(arm)
		}
		p.indent--
		p.b.WriteByte(')')
	case *ErrorExpr:
		p.writef("(err %q)", e.Note)
	default:
		p.writef("(expr? %T)", e)
	}
}

func (p *printer) printNode(n Node) {
	switch n := n.(type) {
	case *Module:
		p.printModule(n)
	case Decl:
		p.printDecl(n)
	case Stmt:
		p.printStmt(n)
	case Expr:
		p.printExpr(n)
	case Pattern:
		p.b.WriteString(PatternString(n))
	default:
		p.writef("<%T>", n)
	}
}

func (p *printer) printMatchArm(a *MatchArm) {
	p.writef("(arm %s", PatternString(a.Pattern))
	if a.Guard != nil {
		p.b.WriteString(" if=")
		p.printExpr(a.Guard)
	}
	p.b.WriteByte(' ')
	p.printBlock(a.Body)
	p.b.WriteByte(')')
}

// ---- Name helpers ----

func assignOpName(o AssignOp) string {
	switch o {
	case AssignEq:
		return "="
	case AssignAdd:
		return "+="
	case AssignSub:
		return "-="
	case AssignMul:
		return "*="
	case AssignDiv:
		return "/="
	case AssignMod:
		return "%="
	case AssignAnd:
		return "&="
	case AssignOr:
		return "|="
	case AssignXor:
		return "^="
	case AssignShl:
		return "<<="
	case AssignShr:
		return ">>="
	}
	return "?="
}

func forKindName(k ForKind) string {
	switch k {
	case ForInfinite:
		return "inf"
	case ForWhile:
		return "while"
	case ForRange:
		return "range"
	case ForIn:
		return "in"
	}
	return "?"
}

func unOpName(o UnOp) string {
	switch o {
	case UnNeg:
		return "neg"
	case UnPlus:
		return "pos"
	case UnNot:
		return "not"
	case UnBitNot:
		return "bnot"
	}
	return "?"
}

func binOpName(o BinOp) string {
	switch o {
	case BinAdd:
		return "+"
	case BinSub:
		return "-"
	case BinMul:
		return "*"
	case BinDiv:
		return "/"
	case BinMod:
		return "%"
	case BinEq:
		return "=="
	case BinNeq:
		return "!="
	case BinLt:
		return "<"
	case BinLeq:
		return "<="
	case BinGt:
		return ">"
	case BinGeq:
		return ">="
	case BinAnd:
		return "&&"
	case BinOr:
		return "||"
	case BinBitAnd:
		return "&"
	case BinBitOr:
		return "|"
	case BinBitXor:
		return "^"
	case BinShl:
		return "<<"
	case BinShr:
		return ">>"
	}
	return "?"
}

func intrinsicKindName(k IntrinsicKind) string {
	switch k {
	case IntrinsicPrint:
		return "print"
	case IntrinsicPrintln:
		return "println"
	case IntrinsicEprint:
		return "eprint"
	case IntrinsicEprintln:
		return "eprintln"
	}
	return "?"
}

func identKindName(k IdentKind) string {
	switch k {
	case IdentLocal:
		return "local"
	case IdentParam:
		return "param"
	case IdentFn:
		return "fn"
	case IdentVariant:
		return "variant"
	case IdentTypeName:
		return "type"
	case IdentGlobal:
		return "global"
	case IdentBuiltin:
		return "builtin"
	}
	return "?"
}

// Quote is a convenience for printing quoted IR text in tests.
func Quote(s string) string { return strconv.Quote(s) }
