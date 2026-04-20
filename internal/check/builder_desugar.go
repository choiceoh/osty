package check

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// DesugarBuildersInFile rewrites auto-derived builder call chains into
// the struct literals they represent (LANG_SPEC §3.3) and emits
// `CodeBuilderMissingRequiredField` (E0774) when a chain ends at
// `.build()` without setters for every required `pub` field.
//
// The rewrite pattern is:
//
//	Point.builder().x(3).y(4).build()   =>   Point { x: 3, y: 4 }
//
// Setter names become struct field names; their argument becomes the
// field value. The original `.builder()` root identifies the struct,
// whose decl must live in the same file (v0.5.first-slice scope —
// cross-package builder resolution is follow-up work).
//
// Mutation happens in place: every Expr slot the walker descends into
// is reassigned with the rewritten value. Call sites that sit under
// unsupported holders (closure bodies, match arm bodies, etc.) are
// walked defensively but the rewrite still applies.
//
// Returns the diagnostics produced during the walk; the File is
// mutated regardless of whether diagnostics were emitted, because a
// partial chain may still be well-typed for downstream phases.
func DesugarBuildersInFile(f *ast.File) []*diag.Diagnostic {
	if f == nil {
		return nil
	}
	w := newBuilderDesugar(f)
	for _, d := range f.Decls {
		w.decl(d)
	}
	return w.diags
}

type builderDesugar struct {
	structs map[string]*ast.StructDecl
	diags   []*diag.Diagnostic
}

func newBuilderDesugar(f *ast.File) *builderDesugar {
	out := &builderDesugar{structs: map[string]*ast.StructDecl{}}
	for _, d := range f.Decls {
		if sd, ok := d.(*ast.StructDecl); ok {
			out.structs[sd.Name] = sd
		}
	}
	return out
}

// ---- decl / stmt / block walkers ----

func (w *builderDesugar) decl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		if n != nil && n.Body != nil {
			w.block(n.Body)
		}
	case *ast.StructDecl:
		if n == nil {
			return
		}
		for _, m := range n.Methods {
			if m != nil && m.Body != nil {
				w.block(m.Body)
			}
		}
		for _, f := range n.Fields {
			if f != nil && f.Default != nil {
				f.Default = w.expr(f.Default)
			}
		}
	case *ast.EnumDecl:
		if n == nil {
			return
		}
		for _, m := range n.Methods {
			if m != nil && m.Body != nil {
				w.block(m.Body)
			}
		}
	case *ast.LetDecl:
		if n != nil && n.Value != nil {
			n.Value = w.expr(n.Value)
		}
	}
}

func (w *builderDesugar) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		w.stmt(s)
	}
}

func (w *builderDesugar) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Block:
		w.block(n)
	case *ast.LetStmt:
		if n.Value != nil {
			n.Value = w.expr(n.Value)
		}
	case *ast.ExprStmt:
		if n.X != nil {
			n.X = w.expr(n.X)
		}
	case *ast.AssignStmt:
		for i, t := range n.Targets {
			n.Targets[i] = w.expr(t)
		}
		if n.Value != nil {
			n.Value = w.expr(n.Value)
		}
	case *ast.ReturnStmt:
		if n.Value != nil {
			n.Value = w.expr(n.Value)
		}
	case *ast.ChanSendStmt:
		if n.Channel != nil {
			n.Channel = w.expr(n.Channel)
		}
		if n.Value != nil {
			n.Value = w.expr(n.Value)
		}
	case *ast.DeferStmt:
		if n.X != nil {
			n.X = w.expr(n.X)
		}
	case *ast.ForStmt:
		if n.Iter != nil {
			n.Iter = w.expr(n.Iter)
		}
		w.block(n.Body)
	}
}

// ---- expression walker ----

// expr walks `e`, recursively rewriting child expressions, and finally
// attempts to desugar the top node if it matches the builder pattern.
// Returns the possibly-rewritten expression; callers must reassign.
func (w *builderDesugar) expr(e ast.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ast.CallExpr:
		if n.Fn != nil {
			n.Fn = w.expr(n.Fn)
		}
		for _, a := range n.Args {
			if a != nil && a.Value != nil {
				a.Value = w.expr(a.Value)
			}
		}
		if lit := w.tryDesugar(n); lit != nil {
			return lit
		}
		return n
	case *ast.FieldExpr:
		if n.X != nil {
			n.X = w.expr(n.X)
		}
		return n
	case *ast.BinaryExpr:
		n.Left = w.expr(n.Left)
		n.Right = w.expr(n.Right)
		return n
	case *ast.UnaryExpr:
		n.X = w.expr(n.X)
		return n
	case *ast.QuestionExpr:
		n.X = w.expr(n.X)
		return n
	case *ast.IndexExpr:
		n.X = w.expr(n.X)
		n.Index = w.expr(n.Index)
		return n
	case *ast.TurbofishExpr:
		n.Base = w.expr(n.Base)
		return n
	case *ast.RangeExpr:
		if n.Start != nil {
			n.Start = w.expr(n.Start)
		}
		if n.Stop != nil {
			n.Stop = w.expr(n.Stop)
		}
		return n
	case *ast.ParenExpr:
		n.X = w.expr(n.X)
		return n
	case *ast.TupleExpr:
		for i := range n.Elems {
			n.Elems[i] = w.expr(n.Elems[i])
		}
		return n
	case *ast.ListExpr:
		for i := range n.Elems {
			n.Elems[i] = w.expr(n.Elems[i])
		}
		return n
	case *ast.MapExpr:
		for _, entry := range n.Entries {
			if entry == nil {
				continue
			}
			if entry.Key != nil {
				entry.Key = w.expr(entry.Key)
			}
			if entry.Value != nil {
				entry.Value = w.expr(entry.Value)
			}
		}
		return n
	case *ast.StructLit:
		for _, f := range n.Fields {
			if f != nil && f.Value != nil {
				f.Value = w.expr(f.Value)
			}
		}
		if n.Spread != nil {
			n.Spread = w.expr(n.Spread)
		}
		return n
	case *ast.IfExpr:
		if n.Cond != nil {
			n.Cond = w.expr(n.Cond)
		}
		w.block(n.Then)
		if n.Else != nil {
			n.Else = w.expr(n.Else)
		}
		return n
	case *ast.MatchExpr:
		if n.Scrutinee != nil {
			n.Scrutinee = w.expr(n.Scrutinee)
		}
		for _, arm := range n.Arms {
			if arm == nil {
				continue
			}
			if arm.Guard != nil {
				arm.Guard = w.expr(arm.Guard)
			}
			if arm.Body != nil {
				arm.Body = w.expr(arm.Body)
			}
		}
		return n
	case *ast.ClosureExpr:
		if n.Body != nil {
			n.Body = w.expr(n.Body)
		}
		return n
	case *ast.Block:
		w.block(n)
		return n
	default:
		return n
	}
}

// ---- desugar core ----

// tryDesugar checks whether `call` is the terminal `.build()` of an
// auto-derived builder chain. When it is, tryDesugar validates the
// required-field set, emits E0774 if any are missing, and returns the
// equivalent struct literal. When the call is not a builder chain (or
// the chain roots in an unknown type), tryDesugar returns nil so the
// caller leaves the original expression in place.
func (w *builderDesugar) tryDesugar(call *ast.CallExpr) ast.Expr {
	if call == nil || len(call.Args) != 0 {
		return nil
	}
	build, ok := call.Fn.(*ast.FieldExpr)
	if !ok || build.Name != "build" || build.IsOptional {
		return nil
	}
	// Walk setters back to the root.
	var setters []builderSetter
	cursor := build.X
	for {
		inner, ok := cursor.(*ast.CallExpr)
		if !ok {
			return nil
		}
		fe, ok := inner.Fn.(*ast.FieldExpr)
		if !ok || fe.IsOptional {
			return nil
		}
		if fe.Name == "builder" {
			if len(inner.Args) != 0 {
				return nil
			}
			typeName, ok := identName(fe.X)
			if !ok {
				return nil
			}
			sd, ok := w.structs[typeName]
			if !ok {
				return nil
			}
			return w.rewrite(call, sd, setters)
		}
		// Intermediate `.field(value)` setter. Must have exactly one
		// positional arg; keyword args are not generated.
		if len(inner.Args) != 1 {
			return nil
		}
		arg := inner.Args[0]
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil
		}
		setters = append(setters, builderSetter{name: fe.Name, value: arg.Value, pos: fe.PosV})
		cursor = fe.X
	}
}

// rewrite constructs the struct literal that replaces the builder
// chain, in source order (setters were collected tail-first so the
// last caller-visible setter wins when duplicated). Missing required
// fields produce an E0774 diagnostic but still yield a literal — the
// literal carries every set field so downstream type-checking can
// diagnose additional problems without cascading "unknown identifier".
// builderSetter is the pending (fieldName, value) pair collected from
// one `.field(value)` call in the chain, along with the source
// position of the field name for diagnostics.
type builderSetter struct {
	name  string
	value ast.Expr
	pos   token.Pos
}

func (w *builderDesugar) rewrite(
	call *ast.CallExpr,
	sd *ast.StructDecl,
	setters []builderSetter,
) ast.Expr {
	// Last-write-wins across duplicated setters.
	values := map[string]ast.Expr{}
	positions := map[string]token.Pos{}
	// setters is tail-first (we walked backwards); iterate tail-first so
	// the first write we see is the rightmost `.x(...)` call.
	seen := map[string]struct{}{}
	for _, s := range setters {
		if _, dup := seen[s.name]; dup {
			continue
		}
		seen[s.name] = struct{}{}
		values[s.name] = s.value
		positions[s.name] = s.pos
	}

	// Collect pub field names in declaration order so missing-field
	// diagnostics match source layout.
	var required []string
	for _, f := range sd.Fields {
		if f == nil || !f.Pub {
			continue
		}
		if f.Default == nil {
			required = append(required, f.Name)
		}
	}

	// G9: every required field must appear in the setter map.
	var missing []string
	for _, name := range required {
		if _, ok := values[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		w.emitMissing(call, sd.Name, missing)
	}

	// Assemble the struct literal. Fields are emitted in declaration
	// order so formatter and tests see a canonical shape regardless of
	// the setter ordering the user wrote.
	lit := &ast.StructLit{
		PosV: call.PosV,
		EndV: call.EndV,
		Type: &ast.Ident{PosV: call.PosV, EndV: call.PosV, Name: sd.Name},
	}
	for _, f := range sd.Fields {
		if f == nil || !f.Pub {
			continue
		}
		val, ok := values[f.Name]
		if !ok {
			// Required-but-missing fields were already diagnosed; skip
			// them in the literal so the downstream checker sees a
			// shape that still parses.
			continue
		}
		lit.Fields = append(lit.Fields, &ast.StructLitField{
			PosV:  positions[f.Name],
			Name:  f.Name,
			Value: val,
		})
	}
	return lit
}

// emitMissing records the G9 diagnostic, listing fields in the order
// they appear in the struct declaration.
func (w *builderDesugar) emitMissing(call *ast.CallExpr, typeName string, missing []string) {
	plural := "field"
	if len(missing) != 1 {
		plural = "fields"
	}
	msg := fmt.Sprintf(
		"builder for `%s` cannot call `.build()`: required %s not set: %s",
		typeName, plural, strings.Join(missing, ", "))
	hint := fmt.Sprintf("set with `.%s(<value>)` before `.build()`", missing[0])
	b := diag.New(diag.Error, msg).
		Code(diag.CodeBuilderMissingRequiredField).
		Primary(diag.Span{Start: call.PosV, End: call.EndV}, "`.build()` call").
		Hint(hint).
		Note("LANG_SPEC §3.3 (G9): builder rejects `.build()` until every `pub` field without a default is set")
	w.diags = append(w.diags, b.Build())
}

// identName returns the bare name of `e` if it is a plain Ident. The
// builder root must be an Ident referring to a struct in the same
// file (FieldExpr/TurbofishExpr paths are follow-up work).
func identName(e ast.Expr) (string, bool) {
	if id, ok := e.(*ast.Ident); ok && id.Name != "" {
		return id.Name, true
	}
	return "", false
}
