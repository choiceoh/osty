package backend

import (
	"strconv"

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

// CanonicalStdlibSymbol returns the MIR-visible symbol for bodyless
// runtime-backed stdlib declarations that must stay routed through
// backend shims rather than injected as Osty source bodies.
func CanonicalStdlibSymbol(module, name string) string {
	return "std." + module + "." + name
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
	type valueKey struct{ module, path, method string }
	type valueRewriteTarget struct{ symbol, typeName string }
	valueSet := map[valueKey]valueRewriteTarget{}
	for _, r := range reached {
		mangled := StdlibMethodSymbol(r.Module, r.Type, r.Method)
		set[key{module: r.Module, typeName: r.Type, method: r.Method}] = mangled
		if r.ValuePath != "" {
			valueSet[valueKey{module: r.Module, path: r.ValuePath, method: r.Method}] = valueRewriteTarget{symbol: mangled, typeName: r.Type}
		}
	}
	valueRewriteCount := 0
	if len(valueSet) > 0 {
		ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
			call, ok := n.(*ir.CallExpr)
			if !ok || call == nil {
				return true
			}
			field, ok := call.Callee.(*ir.FieldExpr)
			if !ok || field == nil || field.Name == "" {
				return true
			}
			module, path, ok := stdlibFieldPath(field.X)
			if !ok || module == "" || len(path) == 0 {
				return true
			}
			vk := valueKey{
				module: module,
				path:   stdlibJoinFieldPath(path),
				method: field.Name,
			}
			target, hit := valueSet[vk]
			if !hit {
				return true
			}
			receiver := stdlibSingletonReceiverExpr(module, vk.path, target.typeName, field.X, field.SpanV)
			args := make([]ir.Arg, 0, len(call.Args)+1)
			args = append(args, ir.Arg{
				Value: receiver,
				SpanV: field.SpanV,
			})
			args = append(args, call.Args...)
			call.Callee = &ir.Ident{
				Name:  target.symbol,
				Kind:  ir.IdentFn,
				T:     stdlibFnType(args, call.T),
				SpanV: field.SpanV,
			}
			call.Args = args
			valueRewriteCount++
			return true
		}), mod)
	}
	swap := map[*ir.MethodCall]*ir.CallExpr{}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		mc, ok := n.(*ir.MethodCall)
		if !ok || mc == nil || mc.Receiver == nil || mc.Name == "" {
			return true
		}
		if module, path, ok := stdlibFieldPath(mc.Receiver); ok && module != "" && len(path) != 0 {
			vk := valueKey{
				module: module,
				path:   stdlibJoinFieldPath(path),
				method: mc.Name,
			}
			if target, hit := valueSet[vk]; hit {
				receiver := stdlibSingletonReceiverExpr(module, vk.path, target.typeName, mc.Receiver, mc.SpanV)
				args := make([]ir.Arg, 0, len(mc.Args)+1)
				args = append(args, ir.Arg{
					Value: receiver,
					SpanV: mc.SpanV,
				})
				args = append(args, mc.Args...)
				swap[mc] = &ir.CallExpr{
					Callee: &ir.Ident{
						Name:  target.symbol,
						Kind:  ir.IdentFn,
						T:     stdlibFnType(args, mc.T),
						SpanV: mc.SpanV,
					},
					TypeArgs: mc.TypeArgs,
					Args:     args,
					T:        mc.T,
					SpanV:    mc.SpanV,
				}
				return true
			}
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
				T:     stdlibFnType(args, mc.T),
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
		return valueRewriteCount
	}
	rw := &stdlibMethodCallsiteSpliceVisitor{swap: swap}
	ir.Walk(rw, mod)
	return valueRewriteCount + rw.count
}

func stdlibFnType(args []ir.Arg, ret ir.Type) ir.Type {
	params := make([]ir.Type, 0, len(args))
	for _, arg := range args {
		if arg.Value == nil || arg.Value.Type() == nil {
			params = append(params, ir.ErrTypeVal)
			continue
		}
		params = append(params, arg.Value.Type())
	}
	if ret == nil {
		ret = ir.ErrTypeVal
	}
	return &ir.FnType{Params: params, Return: ret}
}

func stdlibFieldPath(expr ir.Expr) (string, []string, bool) {
	switch x := expr.(type) {
	case *ir.Ident:
		if x == nil || x.Name == "" {
			return "", nil, false
		}
		return x.Name, nil, true
	case *ir.FieldExpr:
		if x == nil || x.Name == "" {
			return "", nil, false
		}
		module, path, ok := stdlibFieldPath(x.X)
		if !ok {
			return "", nil, false
		}
		return module, append(path, x.Name), true
	default:
		return "", nil, false
	}
}

func stdlibJoinFieldPath(path []string) string {
	out := ""
	for i, part := range path {
		if i > 0 {
			out += "."
		}
		out += part
	}
	return out
}

func stdlibSingletonReceiverExpr(module, path, typeName string, fallback ir.Expr, span ir.Span) ir.Expr {
	switch {
	case module == "encoding" && path == "base64" && typeName == "Base64":
		return stdlibStructLit(module, "Base64", span, ir.StructLitField{
			Name:  "url",
			Value: stdlibStructLit(module, "Base64Url", span),
			SpanV: span,
		})
	case module == "encoding" && path == "base64.url" && typeName == "Base64Url":
		return stdlibStructLit(module, "Base64Url", span)
	case module == "encoding" && path == "hex" && typeName == "Hex":
		return stdlibStructLit(module, "Hex", span)
	case module == "encoding" && path == "url" && typeName == "UrlEncoding":
		return stdlibStructLit(module, "UrlEncoding", span)
	case module == "compress" && path == "gzip" && typeName == "Gzip":
		return stdlibStructLit(module, "Gzip", span)
	case module == "crypto" && path == "hmac" && typeName == "Hmac":
		return stdlibStructLit(module, "Hmac", span)
	case module == "net" && typeName == "Ipv4Addr":
		switch path {
		case "LOCALHOST_V4":
			return stdlibIpv4Lit(127, 0, 0, 1, span)
		case "UNSPECIFIED_V4":
			return stdlibIpv4Lit(0, 0, 0, 0, span)
		case "BROADCAST_V4":
			return stdlibIpv4Lit(255, 255, 255, 255, span)
		}
	case module == "net" && typeName == "Ipv6Addr":
		switch path {
		case "LOCALHOST_V6":
			return stdlibIpv6Lit([]int{0, 0, 0, 0, 0, 0, 0, 1}, span)
		case "UNSPECIFIED_V6":
			return stdlibIpv6Lit([]int{0, 0, 0, 0, 0, 0, 0, 0}, span)
		}
	}
	return fallback
}

func stdlibStructLit(module, typeName string, span ir.Span, fields ...ir.StructLitField) *ir.StructLit {
	return &ir.StructLit{
		TypeName: typeName,
		Fields:   fields,
		T:        &ir.NamedType{Package: module, Name: typeName},
		SpanV:    span,
	}
}

func stdlibIpv4Lit(a, b, c, d int, span ir.Span) *ir.StructLit {
	return stdlibStructLit("net", "Ipv4Addr", span,
		stdlibIntField("a", a, span),
		stdlibIntField("b", b, span),
		stdlibIntField("c", c, span),
		stdlibIntField("d", d, span),
	)
}

func stdlibIpv6Lit(groups []int, span ir.Span) *ir.StructLit {
	elems := make([]ir.Expr, 0, len(groups))
	for _, g := range groups {
		elems = append(elems, stdlibIntLit(g, span))
	}
	return stdlibStructLit("net", "Ipv6Addr", span, ir.StructLitField{
		Name: "groups",
		Value: &ir.ListLit{
			Elems: elems,
			Elem:  ir.TInt,
			SpanV: span,
		},
		SpanV: span,
	})
}

func stdlibIntField(name string, value int, span ir.Span) ir.StructLitField {
	return ir.StructLitField{Name: name, Value: stdlibIntLit(value, span), SpanV: span}
}

func stdlibIntLit(value int, span ir.Span) *ir.IntLit {
	return &ir.IntLit{Text: strconv.Itoa(value), T: ir.TInt, SpanV: span}
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
