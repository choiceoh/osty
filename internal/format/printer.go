package format

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

// printFile emits top-level nodes. `use` declarations that sit above
// the first non-use declaration are reordered into alphabetized groups
// (stdlib → external → FFI) with a blank line between groups — gofmt
// style, confined to the leading use block so late `use go` FFI
// imports stay where the author wrote them. Everything below the
// first non-use decl is printed in source offset order, preserving
// decl↔comment adjacency.
func (p *printer) printFile(f *ast.File) {
	if f == nil {
		return
	}
	p.lastSrcLine = 0

	firstBodyOffset := 1<<31 - 1
	if len(f.Decls) > 0 {
		firstBodyOffset = f.Decls[0].Pos().Offset
	}
	if len(f.Stmts) > 0 && f.Stmts[0].Pos().Offset < firstBodyOffset {
		firstBodyOffset = f.Stmts[0].Pos().Offset
	}

	var topUses, bodyUses []*ast.UseDecl
	for _, u := range f.Uses {
		if u.Pos().Offset < firstBodyOffset {
			topUses = append(topUses, u)
		} else {
			bodyUses = append(bodyUses, u)
		}
	}

	// Stable sort so two uses that share a group+key stay in original
	// relative order (matters only for FFI, where multiple `use go`
	// with different bodies can't collide on key anyway).
	sort.SliceStable(topUses, func(i, j int) bool {
		gi, gj := useGroupOrder(topUses[i]), useGroupOrder(topUses[j])
		if gi != gj {
			return gi < gj
		}
		return useSortKey(topUses[i]) < useSortKey(topUses[j])
	})

	// Inside the reordered top-use block, blank lines are managed
	// explicitly — between groups only. triviaBefore fires just for
	// the first use so leading file-header comments attach; later
	// uses bypass it to prevent source-line gaps from a reordered
	// sequence injecting blanks in the middle of a group.
	prevGroup := -1
	for i, u := range topUses {
		u := u
		g := useGroupOrder(u)
		if i == 0 {
			p.triviaBefore(u.Pos())
		} else if g != prevGroup {
			p.blank()
		}
		p.printUseDecl(u)
		prevGroup = g
	}
	if len(topUses) > 0 {
		// Separator between the reordered use block and the body;
		// reset lastSrcLine so body blank-line detection runs
		// against body node positions, not stale use positions.
		p.blank()
		p.lastSrcLine = 0
	}

	// Interleave body uses + other decls + stmts by source offset.
	type topNode struct {
		pos  token.Pos
		end  token.Pos
		emit func()
	}
	nodes := make([]topNode, 0, len(bodyUses)+len(f.Decls)+len(f.Stmts))
	for _, u := range bodyUses {
		u := u
		nodes = append(nodes, topNode{u.Pos(), u.End(), func() { p.printUseDecl(u) }})
	}
	for _, d := range f.Decls {
		d := d
		nodes = append(nodes, topNode{declLeadLinePos(d), d.End(), func() { p.printDecl(d) }})
	}
	for _, s := range f.Stmts {
		s := s
		nodes = append(nodes, topNode{s.Pos(), s.End(), func() { p.printStmt(s) }})
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].pos.Offset < nodes[j].pos.Offset
	})

	for _, n := range nodes {
		p.triviaBefore(n.pos)
		n.emit()
		p.markSrcLine(n.end.Line)
	}
	if p.pendingNL {
		p.flushNL()
	}
}

// useGroupOrder bins a use decl into the canonical group order:
// 0 = stdlib (`std.*`), 1 = external (everything else that's not
// FFI), 2 = Go FFI (`use go "..."`).
func useGroupOrder(u *ast.UseDecl) int {
	if u.IsGoFFI {
		return 2
	}
	if len(u.Path) > 0 && u.Path[0] == "std" {
		return 0
	}
	return 1
}

// useSortKey is the intra-group sort key — the RawPath when set
// (preserves github.com/... style), otherwise the dotted path.
func useSortKey(u *ast.UseDecl) string {
	if u.IsGoFFI {
		return u.GoPath
	}
	if u.RawPath != "" {
		return u.RawPath
	}
	return strings.Join(u.Path, ".")
}

// chainSeg is one link in a method chain, e.g. `.map(|x| x * 2)` in
// `iter.from(xs).map(|x| x * 2)`. The link before the first chainSeg
// is the chain's base — itself an arbitrary expression (often a call).
type chainSeg struct {
	optional bool       // true when written as `?.` instead of `.`
	name     string     // method / field name
	args     []*ast.Arg // nil + isField=true for bare field access
	isField  bool       // no `(args)` follows, just `.name` / `?.name`
	pos      token.Pos  // segment start
	nameEnd  token.Pos  // end of the `.name` portion, before `(` — used
	// by shouldBreakChain so a multi-line arg doesn't feedback-loop the
	// break decision.
	end token.Pos
}

// collectChain walks the left-recursive CallExpr/FieldExpr skeleton of
// a method chain and returns (base, segs) — the chain root and each
// `.method(...)` or `.field` step after it, in source order. Only
// entered from a CallExpr; walks through intermediate field-only
// segments (`a.b.c().d()`) so those don't abort the chain early.
func collectChain(outer *ast.CallExpr) (base ast.Expr, segs []*chainSeg) {
	var e ast.Expr = outer
	for {
		var seg chainSeg
		var next ast.Expr
		switch n := e.(type) {
		case *ast.CallExpr:
			fe, ok := n.Fn.(*ast.FieldExpr)
			if !ok {
				// f(x) with no FieldExpr — this call is the base.
				return n, reverseSegs(segs)
			}
			seg = chainSeg{
				optional: fe.IsOptional,
				name:     fe.Name,
				args:     n.Args,
				pos:      n.Pos(),
				nameEnd:  fe.End(),
				end:      n.End(),
			}
			next = fe.X
		case *ast.FieldExpr:
			seg = chainSeg{
				optional: n.IsOptional,
				name:     n.Name,
				isField:  true,
				pos:      n.Pos(),
				nameEnd:  n.End(),
				end:      n.End(),
			}
			next = n.X
		default:
			return e, reverseSegs(segs)
		}
		// Does the chain continue through `next`?
		switch next.(type) {
		case *ast.CallExpr, *ast.FieldExpr:
			segs = append(segs, &seg)
			e = next
		default:
			// `next` is an atomic root (Ident, ParenExpr, etc.). The
			// current node e is the chain's base.
			return e, reverseSegs(segs)
		}
	}
}

func reverseSegs(s []*chainSeg) []*chainSeg {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

// shouldBreakChain reports whether a chain should render in the
// leading-dot multi-line form. Two triggers fire it:
//   - 3 or more post-base segments (regardless of source layout) —
//     §1.8 encourages the leading-dot style for long chains.
//   - any post-base segment's `.name` that appears on a line below
//     where the previous segment ended — the author already broke the
//     chain in source; preserve that layout.
//
// nameEnd is the end position of the `.name` portion (before the `(`),
// so a multi-line argument block in the segment's call doesn't push
// the measurement onto a later line and feedback-loop fmt(fmt(x)).
func shouldBreakChain(base ast.Expr, segs []*chainSeg) bool {
	if len(segs) == 0 {
		return false
	}
	if len(segs) >= 3 {
		return true
	}
	prev := base.End().Line
	for _, s := range segs {
		if s.nameEnd.Line != prev {
			return true
		}
		prev = s.end.Line
	}
	return false
}

// printMethodChain emits a chain with the base on its own line and
// each subsequent segment indented one level, each prefixed by `.` or
// `?.` per the segment's IsOptional flag (§1.8). Field-only segments
// (no parens) render without the call tail.
func (p *printer) printMethodChain(base ast.Expr, segs []*chainSeg) {
	p.printExpr(base)
	p.indent()
	for _, s := range segs {
		p.nl()
		if s.optional {
			p.write("?.")
		} else {
			p.write(".")
		}
		p.write(s.name)
		if !s.isField {
			printBracketedList(p, "(", ")", s.args, spanMultiline(s.args), func(a *ast.Arg) {
				if a.Name != "" {
					p.write(a.Name)
					p.write(": ")
				}
				p.printExpr(a.Value)
			})
		}
	}
	p.dedent()
}

// spanMultiline reports whether the source broke a comma-separated
// list across lines, detected by an inter-item gap: item i starts
// strictly below where item i-1 ended. A single item that itself spans
// multiple lines (e.g. a block-bodied closure as one argument) does
// NOT force a multi-line layout — its own newlines are already
// subsumed by items[i-1].End().
//
// Zero-valued positions from error-recovered nodes are skipped so a
// missing-position arg next to a real one doesn't spuriously flip the
// decision.
func spanMultiline[T ast.Node](items []T) bool {
	if len(items) < 2 {
		return false
	}
	lastEnd := 0
	for _, it := range items {
		pos := it.Pos().Line
		if pos == 0 {
			continue
		}
		if lastEnd != 0 && pos > lastEnd {
			return true
		}
		if end := it.End().Line; end != 0 {
			lastEnd = end
		}
	}
	return false
}

// printBracketedList renders `open items... close`. Flat form when
// the source was single-line AND the flat rendering fits within
// MaxLineWidth; otherwise multi-line with trailing comma. Caller must
// NOT have emitted the delimiters.
//
// forceMulti=true skips the flat attempt outright — used when the
// source already broke the list across lines (spanMultiline), so we
// don't waste a rollback-rerender cycle.
func printBracketedList[T any](p *printer, open, close string, items []T, forceMulti bool, emit func(T)) {
	if len(items) == 0 {
		p.write(open)
		p.write(close)
		return
	}
	if !forceMulti {
		snap := p.snapshot()
		p.write(open)
		for i, it := range items {
			if i > 0 {
				p.write(", ")
			}
			emit(it)
		}
		p.write(close)
		body := p.buf.Bytes()[snap.bufLen:]
		// Accept flat when it stays on one line and fits the budget.
		if !bytes.Contains(body, []byte{'\n'}) && p.currentCol() <= MaxLineWidth {
			return
		}
		p.restore(snap)
	}
	p.write(open)
	p.nl()
	p.indent()
	for _, it := range items {
		emit(it)
		p.write(",")
		p.nl()
	}
	p.dedent()
	p.write(close)
}

// printDecl dispatches to the specific declaration printer. Each
// specific printer terminates its own last line via nl(), so successive
// decls render one per line without callers stacking extra newlines.
func (p *printer) printDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.UseDecl:
		p.printUseDecl(n)
	case *ast.FnDecl:
		p.printFnDecl(n)
	case *ast.StructDecl:
		p.printStructDecl(n)
	case *ast.EnumDecl:
		p.printEnumDecl(n)
	case *ast.InterfaceDecl:
		p.printInterfaceDecl(n)
	case *ast.TypeAliasDecl:
		p.printTypeAliasDecl(n)
	case *ast.LetDecl:
		p.printLetDecl(n)
	default:
		p.write(fmt.Sprintf("/* unknown decl %T */", d))
		p.nl()
	}
}

func (p *printer) printUseDecl(u *ast.UseDecl) {
	p.write("use ")
	if u.IsGoFFI {
		p.write("go ")
		p.write(fmt.Sprintf("%q", u.GoPath))
		if u.Alias != "" {
			p.write(" as ")
			p.write(u.Alias)
		}
		if len(u.GoBody) > 0 {
			p.printBracedBody(u.Pos().Line, false, func() {
				for _, d := range u.GoBody {
					p.triviaBefore(d.Pos())
					p.printDecl(d)
					p.markSrcLine(d.End().Line)
				}
			})
			return
		}
		p.trailingCommentAfter(u.End().Line)
		p.nl()
		return
	}
	// Prefer RawPath when it contains path separators (preserves the
	// `github.com/...` style); otherwise join the dotted path.
	if u.RawPath != "" && strings.ContainsAny(u.RawPath, "/:") {
		p.write(u.RawPath)
	} else {
		p.write(strings.Join(u.Path, "."))
	}
	if u.Alias != "" {
		p.write(" as ")
		p.write(u.Alias)
	}
	p.trailingCommentAfter(u.End().Line)
	p.nl()
}

// printDocLines emits a "///"-prefixed block from a DocComment string.
// The parser stores multi-line docs joined by '\n' with the prefix
// already stripped, so we re-split and restore the marker.
func (p *printer) printDocLines(doc string) {
	if doc == "" {
		return
	}
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			p.write("///")
		} else {
			p.write("/// ")
			p.write(line)
		}
		p.nl()
	}
}

func (p *printer) printDocAndAnnotations(doc string, annots []*ast.Annotation) {
	p.printDocLines(doc)
	for _, a := range annots {
		p.printAnnotation(a)
		p.nl()
	}
}

func (p *printer) printAnnotation(a *ast.Annotation) {
	p.write("#[")
	p.write(a.Name)
	if len(a.Args) > 0 {
		printBracketedList(p, "(", ")", a.Args, spanMultiline(a.Args), func(arg *ast.AnnotationArg) {
			p.write(arg.Key)
			if arg.Value != nil {
				p.write(" = ")
				p.printExpr(arg.Value)
			}
		})
	}
	p.write("]")
}

func (p *printer) printFnDecl(f *ast.FnDecl) {
	p.printDocAndAnnotations(f.DocComment, f.Annotations)
	p.writePub(f.Pub)
	p.write("fn ")
	p.write(f.Name)
	p.printGenerics(f.Generics)
	// Unify receiver + params into a single list so the bracketed
	// renderer gets one decision point (flat vs multi-line) that
	// accounts for the whole signature — receiver included.
	type paramItem struct {
		isSelf  bool
		mutSelf bool
		param   *ast.Param
	}
	items := make([]paramItem, 0, len(f.Params)+1)
	if f.Recv != nil {
		items = append(items, paramItem{isSelf: true, mutSelf: f.Recv.Mut})
	}
	for _, prm := range f.Params {
		items = append(items, paramItem{param: prm})
	}
	// Multi-line decision is driven by the parameter span (receiver
	// alone never forces multi-line).
	printBracketedList(p, "(", ")", items, spanMultiline(f.Params), func(it paramItem) {
		if it.isSelf {
			if it.mutSelf {
				p.write("mut self")
			} else {
				p.write("self")
			}
			return
		}
		p.printParam(it.param)
	})
	if f.ReturnType != nil {
		p.write(" -> ")
		p.printType(f.ReturnType)
	}
	if f.Body != nil {
		p.write(" ")
		p.printBlock(f.Body)
	}
	// Body is nil for an interface-declared method without a default.
	p.nl()
}

func (p *printer) printGenerics(gs []*ast.GenericParam) {
	if len(gs) == 0 {
		return
	}
	printBracketedList(p, "<", ">", gs, spanMultiline(gs), func(g *ast.GenericParam) {
		p.write(g.Name)
		if len(g.Constraints) > 0 {
			p.write(": ")
			for j, c := range g.Constraints {
				if j > 0 {
					p.write(" + ")
				}
				p.printType(c)
			}
		}
	})
}

func (p *printer) printParam(prm *ast.Param) {
	if prm.Pattern != nil {
		p.printPattern(prm.Pattern)
	} else {
		p.write(prm.Name)
	}
	if prm.Type != nil {
		p.write(": ")
		p.printType(prm.Type)
	}
	if prm.Default != nil {
		p.write(" = ")
		p.printExpr(prm.Default)
	}
}

func (p *printer) printStructDecl(s *ast.StructDecl) {
	p.printDocAndAnnotations(s.DocComment, s.Annotations)
	p.writePub(s.Pub)
	p.write("struct ")
	p.write(s.Name)
	p.printGenerics(s.Generics)
	p.printBracedBody(s.Pos().Line, len(s.Fields) == 0 && len(s.Methods) == 0, func() {
		for _, fld := range s.Fields {
			p.triviaBefore(leadPos(fld.Pos(), fld.Annotations, ""))
			p.printField(fld)
			p.markSrcLine(fld.End().Line)
		}
		for _, m := range s.Methods {
			p.triviaBefore(leadPos(m.Pos(), m.Annotations, m.DocComment))
			p.printFnDecl(m)
			p.markSrcLine(m.End().Line)
		}
	})
}

// printBracedBody renders a `{ ... }` block: either `{}` inline when
// empty, or a newline-delimited indented body. headerLine is the
// source line of the opening keyword; seeding lastSrcLine to it
// suppresses a fabricated blank between the header and the first
// child while still preserving any real blanks inside the body.
func (p *printer) printBracedBody(headerLine int, empty bool, emit func()) {
	if empty {
		p.write(" {}")
		p.nl()
		return
	}
	p.write(" {")
	p.nl()
	p.indent()
	p.lastSrcLine = headerLine
	emit()
	p.dedent()
	p.write("}")
	p.nl()
}

// writePub emits `pub ` conditionally — a micro-helper that collapses
// the `if x.Pub { p.write("pub ") }` prefix on every decl printer.
func (p *printer) writePub(pub bool) {
	if pub {
		p.write("pub ")
	}
}

// leadPos returns the source line above which blank-line preservation
// should measure when rendering a decorated item. It walks backwards
// from the node's own position to the first annotation (if any), then
// up through any attached doc-comment lines. The parser's invariant
// (§1.5) — doc lines immediately precede the item with no blank
// between — makes the line arithmetic exact without needing positions
// for each doc line.
func leadPos(anchor token.Pos, annots []*ast.Annotation, doc string) token.Pos {
	if len(annots) > 0 {
		anchor = annots[0].Pos()
	}
	if doc != "" {
		anchor.Line -= strings.Count(doc, "\n") + 1
	}
	return anchor
}

// declLeadLinePos returns the leading-trivia position for any Decl
// that satisfies ast.Decorated. Non-decorated decls (UseDecl) fall
// back to the node's own Pos().
func declLeadLinePos(d ast.Decl) token.Pos {
	if dec, ok := d.(ast.Decorated); ok {
		return leadPos(d.Pos(), dec.Annots(), dec.Doc())
	}
	return d.Pos()
}

func (p *printer) printField(f *ast.Field) {
	p.printDocAndAnnotations("", f.Annotations)
	p.writePub(f.Pub)
	p.write(f.Name)
	p.write(": ")
	p.printType(f.Type)
	if f.Default != nil {
		p.write(" = ")
		p.printExpr(f.Default)
	}
	p.write(",")
	p.trailingCommentAfter(f.End().Line)
	p.nl()
}

func (p *printer) printEnumDecl(e *ast.EnumDecl) {
	p.printDocAndAnnotations(e.DocComment, e.Annotations)
	p.writePub(e.Pub)
	p.write("enum ")
	p.write(e.Name)
	p.printGenerics(e.Generics)
	p.printBracedBody(e.Pos().Line, len(e.Variants) == 0 && len(e.Methods) == 0, func() {
		for _, v := range e.Variants {
			p.triviaBefore(leadPos(v.Pos(), v.Annotations, v.DocComment))
			p.printVariant(v)
			p.markSrcLine(v.End().Line)
		}
		for _, m := range e.Methods {
			p.triviaBefore(leadPos(m.Pos(), m.Annotations, m.DocComment))
			p.printFnDecl(m)
			p.markSrcLine(m.End().Line)
		}
	})
}

func (p *printer) printVariant(v *ast.Variant) {
	p.printDocAndAnnotations(v.DocComment, v.Annotations)
	p.write(v.Name)
	if len(v.Fields) > 0 {
		p.write("(")
		for i, t := range v.Fields {
			if i > 0 {
				p.write(", ")
			}
			p.printType(t)
		}
		p.write(")")
	}
	p.write(",")
	p.trailingCommentAfter(v.End().Line)
	p.nl()
}

func (p *printer) printInterfaceDecl(i *ast.InterfaceDecl) {
	p.printDocAndAnnotations(i.DocComment, i.Annotations)
	p.writePub(i.Pub)
	p.write("interface ")
	p.write(i.Name)
	p.printGenerics(i.Generics)
	p.printBracedBody(i.Pos().Line, len(i.Extends) == 0 && len(i.Methods) == 0, func() {
		for _, ext := range i.Extends {
			p.triviaBefore(ext.Pos())
			p.printType(ext)
			p.nl()
			p.markSrcLine(ext.End().Line)
		}
		for _, m := range i.Methods {
			p.triviaBefore(leadPos(m.Pos(), m.Annotations, m.DocComment))
			p.printFnDecl(m)
			p.markSrcLine(m.End().Line)
		}
	})
}

func (p *printer) printTypeAliasDecl(t *ast.TypeAliasDecl) {
	p.printDocAndAnnotations(t.DocComment, t.Annotations)
	p.writePub(t.Pub)
	p.write("type ")
	p.write(t.Name)
	p.printGenerics(t.Generics)
	p.write(" = ")
	p.printType(t.Target)
	p.nl()
}

func (p *printer) printLetDecl(l *ast.LetDecl) {
	p.printDocAndAnnotations(l.DocComment, l.Annotations)
	p.writePub(l.Pub)
	p.write("let ")
	if l.Mut {
		p.write("mut ")
	}
	p.write(l.Name)
	if l.Type != nil {
		p.write(": ")
		p.printType(l.Type)
	}
	if l.Value != nil {
		p.write(" = ")
		p.printExpr(l.Value)
	}
	p.nl()
}

func (p *printer) printType(t ast.Type) {
	switch n := t.(type) {
	case *ast.NamedType:
		// Spec §2.5 / §13.3: formatter rewrites Option<T> into T?.
		if len(n.Path) == 1 && n.Path[0] == "Option" && len(n.Args) == 1 {
			p.printType(n.Args[0])
			p.write("?")
			return
		}
		p.write(strings.Join(n.Path, "."))
		if len(n.Args) > 0 {
			p.write("<")
			for i, a := range n.Args {
				if i > 0 {
					p.write(", ")
				}
				p.printType(a)
			}
			p.write(">")
		}
	case *ast.OptionalType:
		p.printType(n.Inner)
		p.write("?")
	case *ast.TupleType:
		p.write("(")
		for i, e := range n.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.printType(e)
		}
		if len(n.Elems) == 1 {
			// Single-element tuple *types* need a trailing comma the
			// same way `(x,)` does for values.
			p.write(",")
		}
		p.write(")")
	case *ast.FnType:
		p.write("fn(")
		for i, pt := range n.Params {
			if i > 0 {
				p.write(", ")
			}
			p.printType(pt)
		}
		p.write(")")
		if n.ReturnType != nil {
			p.write(" -> ")
			p.printType(n.ReturnType)
		}
	default:
		p.write(fmt.Sprintf("/* unknown type %T */", t))
	}
}

func (p *printer) printStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Block:
		p.printBlock(n)
		p.nl()
	case *ast.LetStmt:
		p.write("let ")
		if n.Mut {
			p.write("mut ")
		}
		p.printPattern(n.Pattern)
		if n.Type != nil {
			p.write(": ")
			p.printType(n.Type)
		}
		if n.Value != nil {
			p.write(" = ")
			p.printExpr(n.Value)
		}
		p.trailingCommentAfter(n.End().Line)
		p.nl()
	case *ast.ExprStmt:
		p.printExpr(n.X)
		p.trailingCommentAfter(n.End().Line)
		p.nl()
	case *ast.AssignStmt:
		for i, t := range n.Targets {
			if i > 0 {
				p.write(", ")
			}
			p.printExpr(t)
		}
		p.write(" ")
		p.write(n.Op.String())
		p.write(" ")
		p.printExpr(n.Value)
		p.trailingCommentAfter(n.End().Line)
		p.nl()
	case *ast.ReturnStmt:
		p.write("return")
		if n.Value != nil {
			p.write(" ")
			p.printExpr(n.Value)
		}
		p.trailingCommentAfter(n.End().Line)
		p.nl()
	case *ast.BreakStmt:
		p.write("break")
		p.nl()
	case *ast.ContinueStmt:
		p.write("continue")
		p.nl()
	case *ast.ChanSendStmt:
		p.printExpr(n.Channel)
		p.write(" <- ")
		p.printExpr(n.Value)
		p.nl()
	case *ast.DeferStmt:
		p.write("defer ")
		p.printExpr(n.X)
		p.nl()
	case *ast.ForStmt:
		p.printForStmt(n)
	default:
		p.write(fmt.Sprintf("/* unknown stmt %T */", s))
		p.nl()
	}
}

func (p *printer) printBlock(b *ast.Block) {
	if b == nil || len(b.Stmts) == 0 {
		p.write("{}")
		return
	}
	p.write("{")
	p.nl()
	p.indent()
	p.lastSrcLine = b.Pos().Line
	for _, s := range b.Stmts {
		p.triviaBefore(s.Pos())
		p.printStmt(s)
		p.markSrcLine(s.End().Line)
	}
	p.dedent()
	p.write("}")
}

func (p *printer) printForStmt(n *ast.ForStmt) {
	p.write("for ")
	if n.IsForLet {
		p.write("let ")
		p.printPattern(n.Pattern)
		p.write(" = ")
		p.printExpr(n.Iter)
	} else if n.Pattern != nil {
		p.printPattern(n.Pattern)
		p.write(" in ")
		p.printExpr(n.Iter)
	} else if n.Iter != nil {
		p.printExpr(n.Iter)
	}
	if n.Iter != nil || n.Pattern != nil {
		p.write(" ")
	}
	p.printBlock(n.Body)
	p.nl()
}

func (p *printer) printExpr(e ast.Expr) {
	switch n := e.(type) {
	case *ast.Ident:
		p.write(n.Name)
	case *ast.IntLit:
		p.write(n.Text)
	case *ast.FloatLit:
		p.write(n.Text)
	case *ast.CharLit:
		p.write("'" + escapeForChar(n.Value) + "'")
	case *ast.ByteLit:
		p.write("b'" + escapeForByte(n.Value) + "'")
	case *ast.BoolLit:
		if n.Value {
			p.write("true")
		} else {
			p.write("false")
		}
	case *ast.StringLit:
		p.printStringLit(n)
	case *ast.UnaryExpr:
		p.write(n.Op.String())
		p.printExpr(n.X)
	case *ast.BinaryExpr:
		p.printExpr(n.Left)
		p.write(" ")
		p.write(n.Op.String())
		p.write(" ")
		p.printExpr(n.Right)
	case *ast.QuestionExpr:
		p.printExpr(n.X)
		p.write("?")
	case *ast.CallExpr:
		if base, segs := collectChain(n); shouldBreakChain(base, segs) {
			p.printMethodChain(base, segs)
			return
		}
		p.printExpr(n.Fn)
		printBracketedList(p, "(", ")", n.Args, spanMultiline(n.Args), func(a *ast.Arg) {
			if a.Name != "" {
				p.write(a.Name)
				p.write(": ")
			}
			p.printExpr(a.Value)
		})
	case *ast.FieldExpr:
		p.printExpr(n.X)
		if n.IsOptional {
			p.write("?.")
		} else {
			p.write(".")
		}
		p.write(n.Name)
	case *ast.IndexExpr:
		p.printExpr(n.X)
		p.write("[")
		p.printExpr(n.Index)
		p.write("]")
	case *ast.TurbofishExpr:
		p.printExpr(n.Base)
		p.write("::<")
		for i, a := range n.Args {
			if i > 0 {
				p.write(", ")
			}
			p.printType(a)
		}
		p.write(">")
	case *ast.RangeExpr:
		if n.Start != nil {
			p.printExpr(n.Start)
		}
		if n.Inclusive {
			p.write("..=")
		} else {
			p.write("..")
		}
		if n.Stop != nil {
			p.printExpr(n.Stop)
		}
	case *ast.ParenExpr:
		p.write("(")
		p.printExpr(n.X)
		p.write(")")
	case *ast.TupleExpr:
		ml := spanMultiline(n.Elems)
		// A single-line 1-tuple keeps the mandatory trailing comma to
		// disambiguate from a parenthesized expression (§1.8).
		if len(n.Elems) == 1 && !ml {
			p.write("(")
			p.printExpr(n.Elems[0])
			p.write(",)")
			return
		}
		printBracketedList(p, "(", ")", n.Elems, ml, func(el ast.Expr) {
			p.printExpr(el)
		})
	case *ast.ListExpr:
		printBracketedList(p, "[", "]", n.Elems, spanMultiline(n.Elems), func(el ast.Expr) {
			p.printExpr(el)
		})
	case *ast.MapExpr:
		if n.Empty {
			p.write("{:}")
			return
		}
		printBracketedList(p, "{", "}", n.Entries, spanMultiline(n.Entries), func(en *ast.MapEntry) {
			p.printExpr(en.Key)
			p.write(": ")
			p.printExpr(en.Value)
		})
	case *ast.StructLit:
		p.printStructLit(n)
	case *ast.IfExpr:
		p.printIfExpr(n)
	case *ast.MatchExpr:
		p.printMatchExpr(n)
	case *ast.ClosureExpr:
		p.printClosure(n)
	case *ast.Block:
		p.printBlock(n)
	default:
		p.write(fmt.Sprintf("/* unknown expr %T */", e))
	}
}

func (p *printer) printStringLit(s *ast.StringLit) {
	// Triple-quoted when the source was triple-quoted, or when the
	// content contains a literal newline — a single-quoted string
	// literally cannot hold one (lexer rejects embedded `\n`), and
	// rendering `\n` as the escape form loses readability. In both
	// cases the form is `"""\n...content...\n    """` with the closing
	// line's indent stripped by the lexer on re-parse.
	if s.IsTriple {
		p.printTripleString(s)
		return
	}
	if s.IsRaw {
		p.write("r\"")
		for _, part := range s.Parts {
			if part.IsLit {
				p.write(part.Lit)
			}
		}
		p.write("\"")
		return
	}
	p.write("\"")
	for _, part := range s.Parts {
		if part.IsLit {
			p.write(escapeStringText(part.Lit))
			continue
		}
		p.write("{")
		p.printExpr(part.Expr)
		p.write("}")
	}
	p.write("\"")
}

// printTripleString emits `"""\n...content...\n    """`. The content
// indent is one level deeper than the caller's; the lexer strips
// exactly that prefix on re-parse (§1.6.3), so the round-trip is
// byte-clean. Interpolations stay inline; `\{` / `\}` are re-escaped
// because the parser decoded them into bare braces in PartText.
func (p *printer) printTripleString(s *ast.StringLit) {
	// Sync the deferred-newline machinery — we bypass p.write for the
	// content body and must not leave a pending newline dangling.
	if p.pendingNL {
		p.flushNL()
	}
	if p.atLineStart {
		p.emitIndent()
	}
	opener := `"""`
	escape := escapeTripleStringText
	if s.IsRaw {
		opener = `r"""`
		escape = rawPassthrough
	}
	contentIndent := indentString(p.level + 1)
	p.rawString(opener)
	p.rawByte('\n')
	p.rawString(contentIndent)

	t := tripleEmitter{p: p, indent: contentIndent}
	for _, part := range s.Parts {
		if part.IsLit {
			t.writeText(escape(part.Lit))
			continue
		}
		t.flushIndent()
		p.rawByte('{')
		p.printExpr(part.Expr)
		p.rawByte('}')
	}
	p.rawByte('\n')
	p.rawString(contentIndent)
	p.rawString(`"""`)
}

// tripleEmitter tracks per-line indent insertion for a single triple-
// string emission. needIndent is true after every `\n` the emitter
// writes; the next non-newline write consumes it and prints the indent
// prefix. Deferring this way keeps blank content lines free of trailing
// spaces.
type tripleEmitter struct {
	p          *printer
	indent     string
	needIndent bool
}

// writeText emits a text segment, inserting the content indent after
// every '\n'. Segments between newlines are written whole via rawString
// so a multi-KB triple-string body doesn't pay per-rune decode +
// re-encode costs through WriteRune.
func (t *tripleEmitter) writeText(text string) {
	for {
		nl := strings.IndexByte(text, '\n')
		if nl < 0 {
			if text == "" {
				return
			}
			t.flushIndent()
			t.p.rawString(text)
			return
		}
		if nl > 0 {
			t.flushIndent()
			t.p.rawString(text[:nl])
		}
		t.p.rawByte('\n')
		t.needIndent = true
		text = text[nl+1:]
	}
}

func (t *tripleEmitter) flushIndent() {
	if t.needIndent {
		t.p.rawString(t.indent)
		t.needIndent = false
	}
}

// rawPassthrough is the escape function for raw triple-strings: no
// substitution at all. Used as a drop-in replacement for
// escapeTripleStringText so the part-emission loop stays branch-free.
func rawPassthrough(s string) string { return s }

// fieldIsVisible reports whether a StructLit field has any content to
// emit. The parser's error-recovery path can synthesize empty fields
// (Name=="", Value==nil) that must be skipped to keep `Type {}` tight.
func fieldIsVisible(f *ast.StructLitField) bool {
	return f.Name != "" || f.Value != nil
}

func (p *printer) printStructLit(s *ast.StructLit) {
	// Error-recovery may leave a Fields entry with empty Name and nil
	// Value; such entries emit nothing and must not force the
	// non-empty branch (which would produce `Type { }`).
	visible := 0
	if s.Spread != nil {
		visible++
	}
	for _, f := range s.Fields {
		if fieldIsVisible(f) {
			visible++
		}
	}
	if visible == 0 {
		p.printExpr(s.Type)
		p.write(" {}")
		return
	}
	p.printExpr(s.Type)
	p.write(" {")
	// Decide initial layout based on whether any entry (spread or
	// field) lives on a line other than the type name — the whole
	// StructLit span would flip true for a single field containing a
	// multi-line closure, which is the wrong trigger. The flat render
	// may still be rejected below by the MaxLineWidth budget.
	refLine := s.Type.Pos().Line
	ml := false
	if s.Spread != nil && s.Spread.Pos().Line != refLine {
		ml = true
	}
	for _, f := range s.Fields {
		if f.Pos().Line != refLine {
			ml = true
			break
		}
	}
	emitFlat := func() {
		p.write(" ")
		first := true
		if s.Spread != nil {
			p.write("..")
			p.printExpr(s.Spread)
			first = false
		}
		for _, f := range s.Fields {
			if !first {
				p.write(", ")
			}
			first = false
			p.write(f.Name)
			if f.Value != nil {
				p.write(": ")
				p.printExpr(f.Value)
			}
		}
		p.write(" }")
	}
	emitMulti := func() {
		p.nl()
		p.indent()
		if s.Spread != nil {
			p.write("..")
			p.printExpr(s.Spread)
			p.write(",")
			p.nl()
		}
		for _, f := range s.Fields {
			p.write(f.Name)
			if f.Value != nil {
				p.write(": ")
				p.printExpr(f.Value)
			}
			p.write(",")
			p.nl()
		}
		p.dedent()
		p.write("}")
	}
	if !ml {
		snap := p.snapshot()
		emitFlat()
		body := p.buf.Bytes()[snap.bufLen:]
		if !bytes.Contains(body, []byte{'\n'}) && p.currentCol() <= MaxLineWidth {
			return
		}
		p.restore(snap)
	}
	emitMulti()
}

func (p *printer) printIfExpr(e *ast.IfExpr) {
	p.write("if ")
	if e.IsIfLet {
		p.write("let ")
		p.printPattern(e.Pattern)
		p.write(" = ")
		p.printExpr(e.Cond)
	} else {
		p.printExpr(e.Cond)
	}
	p.write(" ")
	p.printBlock(e.Then)
	if e.Else != nil {
		p.write(" else ")
		switch ev := e.Else.(type) {
		case *ast.IfExpr:
			p.printIfExpr(ev)
		case *ast.Block:
			p.printBlock(ev)
		default:
			p.printExpr(ev)
		}
	}
}

func (p *printer) printMatchExpr(m *ast.MatchExpr) {
	p.write("match ")
	p.printExpr(m.Scrutinee)
	p.write(" {")
	if len(m.Arms) == 0 {
		p.write("}")
		return
	}
	p.nl()
	p.indent()
	p.lastSrcLine = m.Pos().Line
	for _, arm := range m.Arms {
		p.triviaBefore(arm.Pos())
		p.printPattern(arm.Pattern)
		if arm.Guard != nil {
			p.write(" if ")
			p.printExpr(arm.Guard)
		}
		p.write(" -> ")
		p.printExpr(arm.Body)
		p.write(",")
		p.trailingCommentAfter(arm.End().Line)
		p.nl()
		p.markSrcLine(arm.End().Line)
	}
	p.dedent()
	p.write("}")
}

func (p *printer) printClosure(c *ast.ClosureExpr) {
	p.write("|")
	for i, prm := range c.Params {
		if i > 0 {
			p.write(", ")
		}
		p.printParam(prm)
	}
	p.write("|")
	if c.ReturnType != nil {
		p.write(" -> ")
		p.printType(c.ReturnType)
	}
	p.write(" ")
	p.printExpr(c.Body)
}

func (p *printer) printPattern(pat ast.Pattern) {
	switch n := pat.(type) {
	case *ast.WildcardPat:
		p.write("_")
	case *ast.LiteralPat:
		p.printExpr(n.Literal)
	case *ast.IdentPat:
		p.write(n.Name)
	case *ast.TuplePat:
		p.write("(")
		for i, e := range n.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.printPattern(e)
		}
		p.write(")")
	case *ast.StructPat:
		p.write(strings.Join(n.Type, "."))
		p.write(" {")
		if len(n.Fields) == 0 && !n.Rest {
			p.write("}")
			return
		}
		p.write(" ")
		for i, f := range n.Fields {
			if i > 0 {
				p.write(", ")
			}
			p.write(f.Name)
			if f.Pattern != nil {
				p.write(": ")
				p.printPattern(f.Pattern)
			}
		}
		if n.Rest {
			if len(n.Fields) > 0 {
				p.write(", ")
			}
			p.write("..")
		}
		p.write(" }")
	case *ast.VariantPat:
		p.write(strings.Join(n.Path, "."))
		if len(n.Args) > 0 {
			p.write("(")
			for i, a := range n.Args {
				if i > 0 {
					p.write(", ")
				}
				p.printPattern(a)
			}
			p.write(")")
		}
	case *ast.RangePat:
		if n.Start != nil {
			p.printExpr(n.Start)
		}
		if n.Inclusive {
			p.write("..=")
		} else {
			p.write("..")
		}
		if n.Stop != nil {
			p.printExpr(n.Stop)
		}
	case *ast.OrPat:
		for i, a := range n.Alts {
			if i > 0 {
				p.write(" | ")
			}
			p.printPattern(a)
		}
	case *ast.BindingPat:
		p.write(n.Name)
		p.write(" @ ")
		p.printPattern(n.Pattern)
	default:
		p.write(fmt.Sprintf("/* unknown pattern %T */", pat))
	}
}
