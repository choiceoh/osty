package check

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// DesugarBuildersInFile rewrites auto-derived builder call chains into
// the struct literals they represent (LANG_SPEC §3.3) and emits
// `CodeBuilderMissingRequiredField` (E0774) when a chain ends at
// `.build()` without setters for every required `pub` field.
//
// Two chain roots are recognised:
//
//	Type.builder().x(3).y(4).build()   =>   Type { x: 3, y: 4 }
//	value.toBuilder().x(3).build()     =>   Type { ..value, x: 3 }
//
// Setter names become struct field names; their argument becomes the
// field value. The `.builder()` root identifies the struct by name;
// the `.toBuilder()` root needs the receiver's type, which is
// recovered statically from: (a) a struct-literal receiver, (b) a
// `Type.builder()…build()` chain receiver, or (c) an identifier
// bound earlier in the same block by one of those two forms.
//
// When `resolve.Result` is supplied, `Ident.builder()` calls resolve
// through the resolver's ref map first, which finds structs imported
// from other packages via `use`. A nil resolve result falls back to
// the same-file struct table.
//
// Returns the diagnostics produced during the walk; the File is
// mutated regardless, because a partial chain may still be
// well-typed for downstream phases.
func DesugarBuildersInFile(f *ast.File, rr *resolve.Result) []*diag.Diagnostic {
	if f == nil {
		return nil
	}
	w := newBuilderDesugar(f, rr)
	for _, d := range f.Decls {
		w.decl(d)
	}
	return w.diags
}

type builderDesugar struct {
	localStructs map[string]*ast.StructDecl
	builderInfo  map[*ast.StructDecl]builderDeriveInfo
	refs         map[ast.NodeID]*resolve.Symbol
	diags        []*diag.Diagnostic
	// scope is a stack of per-block variable → struct-decl bindings
	// used to recover the receiver type of `ident.toBuilder()` when
	// `ident` was bound in the same block by a form we can trace.
	scope []map[string]*ast.StructDecl
}

func newBuilderDesugar(f *ast.File, rr *resolve.Result) *builderDesugar {
	out := &builderDesugar{
		localStructs: map[string]*ast.StructDecl{},
		builderInfo:  map[*ast.StructDecl]builderDeriveInfo{},
	}
	if rr != nil {
		out.refs = rr.RefsByID
	}
	for _, d := range f.Decls {
		if sd, ok := d.(*ast.StructDecl); ok {
			out.localStructs[sd.Name] = sd
		}
	}
	return out
}

// structByIdent returns the struct declaration an Ident resolves to.
// The local-file table is consulted first so unit tests without a
// resolve.Result keep working; on a miss, the resolver's Refs map
// picks up imported structs whose decl lives in another file.
func (w *builderDesugar) structByIdent(id *ast.Ident) *ast.StructDecl {
	if id == nil {
		return nil
	}
	if sd, ok := w.localStructs[id.Name]; ok {
		return sd
	}
	if w.refs == nil {
		return nil
	}
	sym := w.refs[id.ID]
	if sym == nil || sym.Kind != resolve.SymStruct {
		return nil
	}
	if sd, ok := sym.Decl.(*ast.StructDecl); ok {
		return sd
	}
	return nil
}

func (w *builderDesugar) deriveInfo(sd *ast.StructDecl) builderDeriveInfo {
	if sd == nil {
		return builderDeriveInfo{}
	}
	if info, ok := w.builderInfo[sd]; ok {
		return info
	}
	info := classifyBuilderDerive(sd)
	w.builderInfo[sd] = info
	return info
}

// ---- block-scope binding tracking (for Ident.toBuilder()) ----

func (w *builderDesugar) pushScope() {
	w.scope = append(w.scope, map[string]*ast.StructDecl{})
}

func (w *builderDesugar) popScope() {
	if n := len(w.scope); n > 0 {
		w.scope = w.scope[:n-1]
	}
}

func (w *builderDesugar) bindLocal(name string, sd *ast.StructDecl) {
	if name == "" || sd == nil || len(w.scope) == 0 {
		return
	}
	w.scope[len(w.scope)-1][name] = sd
}

func (w *builderDesugar) lookupLocal(name string) *ast.StructDecl {
	for i := len(w.scope) - 1; i >= 0; i-- {
		if sd, ok := w.scope[i][name]; ok {
			return sd
		}
	}
	return nil
}

// structFromReceiver returns the struct declaration the receiver of
// `.toBuilder()` resolves to, considering the three traceable shapes.
// Returns nil when the type is unknown to static analysis.
func (w *builderDesugar) structFromReceiver(e ast.Expr) *ast.StructDecl {
	switch n := e.(type) {
	case *ast.StructLit:
		if id, ok := n.Type.(*ast.Ident); ok {
			return w.structByIdent(id)
		}
	case *ast.Ident:
		return w.lookupLocal(n.Name)
	case *ast.ParenExpr:
		return w.structFromReceiver(n.X)
	}
	return nil
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
	w.pushScope()
	defer w.popScope()
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
		// After rewriting, snapshot the bound variable's type when
		// we can recover it statically. The check runs on the
		// post-rewrite value so a builder chain already collapsed to
		// a struct literal is discovered uniformly.
		w.recordLetBinding(n)
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

// recordLetBinding registers `let x = StructLit` (after the rewriter
// has a chance to collapse a chain) into the current block scope so
// downstream `x.toBuilder()` call sites know the receiver's type.
func (w *builderDesugar) recordLetBinding(ls *ast.LetStmt) {
	if ls == nil || ls.Value == nil {
		return
	}
	id, ok := ls.Pattern.(*ast.IdentPat)
	if !ok || id.Name == "" {
		return
	}
	if sd := w.structFromReceiver(ls.Value); sd != nil {
		w.bindLocal(id.Name, sd)
	}
}

// ---- expression walker ----

// expr walks `e`, recursively rewriting child expressions, and
// finally attempts to desugar the top node if it matches the builder
// pattern. Returns the possibly-rewritten expression; callers must
// reassign.
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

// builderSetter is the pending (fieldName, value) pair collected from
// one `.field(value)` call in the chain, along with the source
// position of the field name for diagnostics.
type builderSetter struct {
	name  string
	value ast.Expr
	pos   token.Pos
}

// tryDesugar checks whether `call` is the terminal `.build()` of an
// auto-derived builder chain. When it is, tryDesugar validates the
// required-field set, emits E0774 if any are missing, and returns
// the equivalent struct literal. When the call is not a builder
// chain (or the chain roots in an unknown type), tryDesugar returns
// nil so the caller leaves the original expression in place.
func (w *builderDesugar) tryDesugar(call *ast.CallExpr) ast.Expr {
	if call == nil || len(call.Args) != 0 {
		return nil
	}
	build, ok := call.Fn.(*ast.FieldExpr)
	if !ok || build.Name != "build" || build.IsOptional {
		return nil
	}
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
			id, ok := fe.X.(*ast.Ident)
			if !ok {
				return nil
			}
			sd := w.structByIdent(id)
			if sd == nil {
				return nil
			}
			info := w.deriveInfo(sd)
			if !info.Derivable {
				return nil
			}
			return w.rewriteFromBuilder(call, sd, info.Required, setters)
		}
		if fe.Name == "toBuilder" {
			if len(inner.Args) != 0 {
				return nil
			}
			sd := w.structFromReceiver(fe.X)
			if sd == nil {
				return nil
			}
			if !w.deriveInfo(sd).Derivable {
				return nil
			}
			return w.rewriteFromToBuilder(call, sd, fe.X, setters)
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

// rewriteFromBuilder handles the `Type.builder()…build()` chain. The
// resulting struct literal names the type, carries each set field in
// declaration order, and omits required-but-missing fields after
// recording a diagnostic (the downstream checker will then see a
// partial literal that still type-checks for every written field).
func (w *builderDesugar) rewriteFromBuilder(
	call *ast.CallExpr,
	sd *ast.StructDecl,
	required []string,
	setters []builderSetter,
) ast.Expr {
	values, positions := collapseSetters(setters)
	missing := missingRequired(required, values)
	if len(missing) > 0 {
		w.emitMissing(call, sd.Name, missing)
	}
	return buildStructLit(call, sd, values, positions, nil)
}

// rewriteFromToBuilder handles `receiver.toBuilder()…build()`. The
// struct literal spreads the receiver so unset fields inherit their
// prior values — per LANG_SPEC §3.3 "Returns a builder preloaded with
// all current field values". G9 therefore does not apply: every
// required field is already populated by the spread. The setters
// simply overwrite the fields the user named.
func (w *builderDesugar) rewriteFromToBuilder(
	call *ast.CallExpr,
	sd *ast.StructDecl,
	spread ast.Expr,
	setters []builderSetter,
) ast.Expr {
	values, positions := collapseSetters(setters)
	return buildStructLit(call, sd, values, positions, spread)
}

// collapseSetters applies last-write-wins over duplicated field
// names and returns the resulting value / position maps. `setters`
// is tail-first (the walker pushed each as it descended), so the
// first occurrence we see in iteration order is the rightmost
// setter the user wrote.
func collapseSetters(setters []builderSetter) (
	values map[string]ast.Expr, positions map[string]token.Pos,
) {
	values = map[string]ast.Expr{}
	positions = map[string]token.Pos{}
	seen := map[string]struct{}{}
	for _, s := range setters {
		if _, dup := seen[s.name]; dup {
			continue
		}
		seen[s.name] = struct{}{}
		values[s.name] = s.value
		positions[s.name] = s.pos
	}
	return values, positions
}

// missingRequired returns the required field names absent from the
// values map, in the original declaration order.
func missingRequired(required []string, values map[string]ast.Expr) []string {
	var out []string
	for _, name := range required {
		if _, ok := values[name]; !ok {
			out = append(out, name)
		}
	}
	return out
}

// buildStructLit assembles the rewritten struct literal in
// declaration order. When `spread` is non-nil the literal carries
// `..spread` so unset fields inherit the receiver's values (the
// `toBuilder` case).
func buildStructLit(
	call *ast.CallExpr,
	sd *ast.StructDecl,
	values map[string]ast.Expr,
	positions map[string]token.Pos,
	spread ast.Expr,
) ast.Expr {
	lit := &ast.StructLit{
		PosV:   call.PosV,
		EndV:   call.EndV,
		Type:   &ast.Ident{PosV: call.PosV, EndV: call.PosV, Name: sd.Name},
		Spread: spread,
	}
	for _, f := range sd.Fields {
		if f == nil || !f.Pub {
			continue
		}
		val, ok := values[f.Name]
		if !ok {
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
