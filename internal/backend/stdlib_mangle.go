package backend

import (
	"github.com/osty/osty/internal/ir"
)

// StdlibSymbol returns the mangled IR name for a stdlib function injected
// alongside user code. The scheme is `osty_std_<module>__<name>` — the
// `osty_std_` prefix makes the provenance obvious in generated IR, the
// double underscore separates the module segment from the function name
// so a future `module_with_underscore` cannot collide with a function
// name, and only ASCII identifier characters are used so backends that
// are strict about symbol charsets accept the result unchanged.
//
// StdlibSymbol does not validate its inputs — the caller has already
// looked up the module and fn via stdlib.Registry.
func StdlibSymbol(module, name string) string {
	return "osty_std_" + module + "__" + name
}

// StdlibMethodSymbol returns the mangled IR name for a stdlib struct/
// enum method lowered into a free-function helper alongside user code.
// The scheme is `osty_std_<module>__<type>__<method>`, extending
// StdlibSymbol with one more `__`-delimited segment for the owning
// type. The double-underscore separator preserves the no-collision
// property: a free fn `f` and a method `T.f` in the same module mangle
// to distinct symbols.
//
// As with StdlibSymbol, no input validation; callers have already
// resolved the (module, type, method) triple via
// `Registry.LookupMethodDecl`.
func StdlibMethodSymbol(module, typeName, method string) string {
	return "osty_std_" + module + "__" + typeName + "__" + method
}

// RewriteStdlibCallsites walks mod and rewrites every `module.name(...)`
// call whose (module, name) pair is in reached to a bare call against
// the mangled symbol. reached is the set produced by
// ReachableStdlibFns — only calls with a matching entry are rewritten.
// Other qualified calls (runtime FFI, unknown user aliases) are left
// alone so their own diagnostics remain accurate.
//
// Rewriting happens in place on mod. Returns the number of call sites
// that were updated; 0 means no change.
func RewriteStdlibCallsites(mod *ir.Module, reached []ReachableStdlibFn) int {
	if mod == nil || len(reached) == 0 {
		return 0
	}
	set := map[ir.QualifiedRef]string{}
	for _, r := range reached {
		set[ir.QualifiedRef{Qualifier: r.Module, Name: r.Fn.Name}] = StdlibSymbol(r.Module, r.Fn.Name)
	}
	rw := &stdlibCallsiteRewriter{set: set}
	ir.Walk(rw, mod)
	return rw.count
}

type stdlibCallsiteRewriter struct {
	set   map[ir.QualifiedRef]string
	count int
}

func (r *stdlibCallsiteRewriter) Visit(n ir.Node) ir.Visitor {
	call, ok := n.(*ir.CallExpr)
	if !ok {
		return r
	}
	field, ok := call.Callee.(*ir.FieldExpr)
	if !ok {
		return r
	}
	ident, ok := field.X.(*ir.Ident)
	if !ok || ident.Name == "" || field.Name == "" {
		return r
	}
	mangled, hit := r.set[ir.QualifiedRef{Qualifier: ident.Name, Name: field.Name}]
	if !hit {
		return r
	}
	call.Callee = &ir.Ident{
		Name:  mangled,
		Kind:  ir.IdentFn,
		T:     field.T,
		SpanV: field.SpanV,
	}
	r.count++
	return r
}

// RewriteStdlibMethodCallsites rewrites every `receiver.method(args)`
// call whose (module, type, method) triple is in reached into a free
// CallExpr against the mangled symbol with the receiver prepended as
// the first argument: `osty_std_<module>__<type>__<method>(receiver,
// args...)`. The transformation runs in place on mod and returns the
// number of MethodCall nodes that were replaced.
//
// This is the call-site half of the method-body injection pipeline —
// the body half is `injectReachableStdlibBodies`, which lowers the
// method into a free fn that takes `self` as its first explicit
// parameter so the rewritten call sites resolve cleanly.
//
// MethodCall nodes whose receiver type is not a stdlib NamedType, or
// whose triple isn't in reached (e.g. a method on a user struct that
// happens to share a name with a stdlib method), are left alone so
// existing dispatch paths keep working.
//
// IMPORTANT: this rewriter walks expressions inside the module but
// cannot replace a parent statement's expression in place — it
// mutates each MethodCall node by overwriting fields of the
// surrounding CallExpr is NOT possible because MethodCall is its own
// IR node type. The rewriter therefore operates by collecting an
// (old → new) substitution map and walking again to splice. The
// implementation uses a parent-aware walk that swaps Expr fields
// directly via reflection-free shape matching: every Expr-bearing
// container (BinaryExpr, CallExpr, …) is enumerated explicitly so a
// swap can be performed without losing the surrounding shape.
func RewriteStdlibMethodCallsites(mod *ir.Module, reached []ReachableStdlibMethod) int {
	if mod == nil || len(reached) == 0 {
		return 0
	}
	type key struct{ module, typeName, method string }
	set := map[key]string{}
	for _, r := range reached {
		set[key{module: r.Module, typeName: r.Type, method: r.Method}] = StdlibMethodSymbol(r.Module, r.Type, r.Method)
	}
	swap := map[*ir.MethodCall]*ir.CallExpr{}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		mc, ok := n.(*ir.MethodCall)
		if !ok || mc == nil || mc.Receiver == nil || mc.Name == "" {
			return true
		}
		named, ok := mc.Receiver.Type().(*ir.NamedType)
		if !ok || named == nil || named.Package == "" || named.Name == "" {
			return true
		}
		mangled, hit := set[key{module: named.Package, typeName: named.Name, method: mc.Name}]
		if !hit {
			return true
		}
		// Build the replacement CallExpr in-place. The receiver becomes
		// the first positional argument; original args follow. The new
		// callee is a bare Ident pointing at the mangled symbol, with
		// IdentFn kind so the lowerer / monomorphizer treat it as a
		// function reference rather than a local variable.
		args := make([]ir.Arg, 0, len(mc.Args)+1)
		args = append(args, ir.Arg{
			Value: mc.Receiver,
			SpanV: mc.SpanV,
		})
		args = append(args, mc.Args...)
		swap[mc] = &ir.CallExpr{
			Callee: &ir.Ident{
				Name:  mangled,
				Kind:  ir.IdentFn,
				SpanV: mc.SpanV,
			},
			TypeArgs: mc.TypeArgs,
			Args:     args,
			T:        mc.T,
			SpanV:    mc.SpanV,
		}
		return true
	}), mod)
	if len(swap) == 0 {
		return 0
	}
	rw := &stdlibMethodCallsiteSpliceVisitor{swap: swap}
	ir.Walk(rw, mod)
	return rw.count
}

// stdlibMethodCallsiteSpliceVisitor walks every Expr-bearing slot in
// the IR and replaces any *MethodCall present in `swap` with the
// pre-built CallExpr. Reflection-free: each container shape is matched
// explicitly so replacement is structural rather than name-based.
//
// Exported expression slots covered: stmts (ExprStmt, ReturnStmt,
// AssignStmt, LetStmt), block tail expressions, if/match/loop bodies
// indirectly via their Block children, call args, struct/list/map/
// tuple element exprs, binary/unary/index/field receiver / index
// children, optional-chain, range bounds, lambda bodies, defer exprs.
// `ir.Walk` already descends into all of these, so we simply intercept
// each parent on the way in and rewrite the field that points at a
// hit MethodCall before recursion continues.
type stdlibMethodCallsiteSpliceVisitor struct {
	swap  map[*ir.MethodCall]*ir.CallExpr
	count int
}

func (v *stdlibMethodCallsiteSpliceVisitor) replace(e *ir.Expr) {
	if e == nil {
		return
	}
	mc, ok := (*e).(*ir.MethodCall)
	if !ok {
		return
	}
	repl, hit := v.swap[mc]
	if !hit {
		return
	}
	*e = repl
	v.count++
}

func (v *stdlibMethodCallsiteSpliceVisitor) Visit(n ir.Node) ir.Visitor {
	switch x := n.(type) {
	// ---- Stmts ----
	case *ir.ExprStmt:
		v.replace(&x.X)
	case *ir.ReturnStmt:
		v.replace(&x.Value)
	case *ir.LetStmt:
		v.replace(&x.Value)
	case *ir.AssignStmt:
		for i := range x.Targets {
			v.replace(&x.Targets[i])
		}
		v.replace(&x.Value)
	case *ir.IfStmt:
		v.replace(&x.Cond)
	case *ir.ForStmt:
		v.replace(&x.Cond)
		v.replace(&x.Iter)
		v.replace(&x.Start)
		v.replace(&x.End)
	case *ir.MatchStmt:
		v.replace(&x.Scrutinee)
	case *ir.ChanSendStmt:
		v.replace(&x.Channel)
		v.replace(&x.Value)
	case *ir.Block:
		v.replace(&x.Result)
	// ---- Exprs ----
	case *ir.CallExpr:
		v.replace(&x.Callee)
		for i := range x.Args {
			v.replace(&x.Args[i].Value)
		}
	case *ir.MethodCall:
		// Receiver/args may contain stdlib-method hits that were not
		// the outer MethodCall (e.g. a user method call whose argument
		// contains a stdlib-method expression). The outer-node case
		// already replaced *this* MethodCall when its parent visited;
		// here we just propagate replacement into its still-original
		// children.
		v.replace(&x.Receiver)
		for i := range x.Args {
			v.replace(&x.Args[i].Value)
		}
	case *ir.IntrinsicCall:
		for i := range x.Args {
			v.replace(&x.Args[i].Value)
		}
	case *ir.BinaryExpr:
		v.replace(&x.Left)
		v.replace(&x.Right)
	case *ir.UnaryExpr:
		v.replace(&x.X)
	case *ir.IndexExpr:
		v.replace(&x.X)
		v.replace(&x.Index)
	case *ir.FieldExpr:
		v.replace(&x.X)
	case *ir.TupleAccess:
		v.replace(&x.X)
	case *ir.QuestionExpr:
		v.replace(&x.X)
	case *ir.CoalesceExpr:
		v.replace(&x.Left)
		v.replace(&x.Right)
	case *ir.IfExpr:
		v.replace(&x.Cond)
	case *ir.IfLetExpr:
		v.replace(&x.Scrutinee)
	case *ir.MatchExpr:
		v.replace(&x.Scrutinee)
	case *ir.MatchArm:
		v.replace(&x.Guard)
	case *ir.TupleLit:
		for i := range x.Elems {
			v.replace(&x.Elems[i])
		}
	case *ir.ListLit:
		for i := range x.Elems {
			v.replace(&x.Elems[i])
		}
	case *ir.MapLit:
		for i := range x.Entries {
			v.replace(&x.Entries[i].Key)
			v.replace(&x.Entries[i].Value)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			v.replace(&x.Fields[i].Value)
		}
		v.replace(&x.Spread)
	case *ir.VariantLit:
		for i := range x.Args {
			v.replace(&x.Args[i].Value)
		}
	case *ir.RangeLit:
		v.replace(&x.Start)
		v.replace(&x.End)
	}
	return v
}
