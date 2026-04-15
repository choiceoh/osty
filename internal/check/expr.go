package check

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// checkExpr computes the type of an expression. `hint`, when non-nil,
// is the expected type in the surrounding context — it lets numeric
// literal inference flow from the outside in (`let x: Int32 = 5` gives
// 5 the type Int32).
//
// The hint is advisory. If the expression's natural type disagrees with
// the hint, no coercion happens here; the caller is responsible for
// emitting a mismatch diagnostic (via checkAssignable / errMismatch).
func (c *checker) checkExpr(e ast.Expr, hint types.Type, env *env) types.Type {
	t := c.exprType(e, hint, env)
	c.recordExpr(e, t)
	return t
}

func (c *checker) exprType(e ast.Expr, hint types.Type, env *env) types.Type {
	if e == nil {
		return types.ErrorType
	}
	switch x := e.(type) {
	case *ast.Ident:
		return c.identType(x, hint)
	case *ast.IntLit:
		return c.intLitType(x, hint)
	case *ast.FloatLit:
		return c.floatLitType(x, hint)
	case *ast.CharLit:
		return types.Char
	case *ast.ByteLit:
		return types.Byte
	case *ast.StringLit:
		for _, p := range x.Parts {
			if p.IsLit || p.Expr == nil {
				continue
			}
			t := c.checkExpr(p.Expr, nil, env)
			if !hasToString(c, t) && !types.IsError(t) {
				c.errNode(p.Expr, diag.CodeInterpolationNonStr,
					"type `%s` does not implement the display protocol `ToString`", t)
			}
		}
		return types.String
	case *ast.BoolLit:
		return types.Bool
	case *ast.UnaryExpr:
		return c.unaryTypeHinted(x, hint, env)
	case *ast.BinaryExpr:
		return c.binaryType(x, env)
	case *ast.QuestionExpr:
		return c.questionType(x, env)
	case *ast.CallExpr:
		return c.callType(x, hint, env)
	case *ast.FieldExpr:
		return c.fieldType(x, env)
	case *ast.IndexExpr:
		return c.indexType(x, env)
	case *ast.TurbofishExpr:
		return c.turbofishType(x, hint, env)
	case *ast.RangeExpr:
		return c.rangeType(x, env)
	case *ast.ParenExpr:
		return c.checkExpr(x.X, hint, env)
	case *ast.TupleExpr:
		return c.tupleType(x, hint, env)
	case *ast.ListExpr:
		return c.listType(x, hint, env)
	case *ast.MapExpr:
		return c.mapType(x, hint, env)
	case *ast.StructLit:
		return c.structLitType(x, env)
	case *ast.IfExpr:
		return c.ifType(x, hint, env)
	case *ast.MatchExpr:
		return c.matchType(x, hint, env)
	case *ast.ClosureExpr:
		return c.closureType(x, hint, env)
	case *ast.Block:
		return c.blockAsExprType(x, hint, env)
	}
	return types.ErrorType
}

// ---- Identifiers / literals ----

func (c *checker) identType(id *ast.Ident, hint types.Type) types.Type {
	sym := c.symbol(id)
	if sym == nil {
		// Resolver already reported; avoid cascading.
		return types.ErrorType
	}

	// Special ID forms the resolver pre-routes to builtins.
	switch sym.Name {
	case "None":
		// None has type Option<?>. When a hint asks for Optional[T],
		// produce that; otherwise a fresh Optional wrapping an
		// unconstrained ErrorType (the checker defers the final type).
		if o, ok := hint.(*types.Optional); ok {
			return o
		}
		return &types.Optional{Inner: types.ErrorType}
	case "true", "false":
		return types.Bool
	}

	c.warnIfDeprecated(sym, id.PosV)
	t := c.symTypeOrError(sym)
	// If the ident references a value-returning symbol (Fn, variant)
	// we return that fn/variant signature. For bare-variant references
	// (`Empty`), the symbol's type is already the enum's Named.
	return t
}

func (c *checker) intLitType(lit *ast.IntLit, hint types.Type) types.Type {
	if hint != nil {
		switch h := hint.(type) {
		case *types.Primitive:
			if h.Kind.IsInteger() || h.Kind.IsFloat() {
				c.checkIntLitFits(lit, h)
				return h
			}
		case *types.Optional:
			if p, ok := h.Inner.(*types.Primitive); ok && p.Kind.IsNumeric() {
				c.checkIntLitFits(lit, p)
				return p
			}
		}
	}
	return types.UntypedIntVal
}

func (c *checker) floatLitType(_ *ast.FloatLit, hint types.Type) types.Type {
	if hint != nil {
		if h, ok := hint.(*types.Primitive); ok && h.Kind.IsFloat() {
			return h
		}
		if o, ok := hint.(*types.Optional); ok {
			if p, ok := o.Inner.(*types.Primitive); ok && p.Kind.IsFloat() {
				return p
			}
		}
	}
	return types.UntypedFloatVal
}

// ---- Unary / binary ----

func (c *checker) unaryType(e *ast.UnaryExpr, env *env) types.Type {
	return c.unaryTypeHinted(e, nil, env)
}

// unaryTypeHinted propagates a surrounding type hint through a prefix
// operator so literal-range checking sees the sign. For `-lit` the
// inner literal bypasses its own positive-range check (the literal
// `128` is legal under a `-` but not on its own in Int8); the outer
// negated check at the unary level replaces it.
func (c *checker) unaryTypeHinted(e *ast.UnaryExpr, hint types.Type, env *env) types.Type {
	// When MINUS wraps an IntLit, check bounds against the NEGATED
	// value only. Evaluate the inner without a primitive hint so its
	// own IntLit-level bounds check doesn't fire on the positive form.
	if e.Op == token.MINUS {
		if _, ok := e.X.(*ast.IntLit); ok {
			t := c.checkExpr(e.X, nil, env)
			if types.IsError(t) {
				return types.ErrorType
			}
			if !types.IsNumeric(t) {
				c.errNode(e, diag.CodeUnaryOpUntyped,
					"operator `%s` is not defined on type `%s`", e.Op, t)
				return types.ErrorType
			}
			// The negated literal adopts the hint type when provided.
			if p, isPrim := hint.(*types.Primitive); isPrim && p.Kind.IsNumeric() {
				c.checkNegatedIntLitFits(e.X.(*ast.IntLit), p, e)
				return p
			}
			return t
		}
	}
	t := c.checkExpr(e.X, hint, env)
	if types.IsError(t) {
		return types.ErrorType
	}
	switch e.Op {
	case token.MINUS:
		if types.IsNumeric(t) {
			return t
		}
	case token.NOT:
		if types.IsBool(t) {
			return types.Bool
		}
	case token.BITNOT:
		if types.IsInteger(t) {
			return t
		}
	}
	c.errNode(e, diag.CodeUnaryOpUntyped,
		"operator `%s` is not defined on type `%s`", e.Op, t)
	return types.ErrorType
}

func (c *checker) binaryType(e *ast.BinaryExpr, env *env) types.Type {
	// Use left's natural type as a hint for the right operand so
	// `5_i32 + 1` keeps both sides at Int32.
	lt := c.checkExpr(e.Left, nil, env)
	rt := c.checkExpr(e.Right, lt, env)
	// Re-evaluate left under rt-as-hint when rt forces a concrete type.
	// Only needed when lt was Untyped.
	if _, ok := lt.(*types.Untyped); ok {
		if rp, ok := rt.(*types.Primitive); ok && (rp.Kind.IsInteger() || rp.Kind.IsFloat()) {
			lt = c.checkExpr(e.Left, rp, env)
		}
	}
	if types.IsError(lt) || types.IsError(rt) {
		return types.ErrorType
	}
	return c.binaryOpResult(e, e.Op, lt, rt)
}

// binaryOpResult validates an operator against its operands and returns
// the result type. The error messages try to match v0.3 §2.2 phrasing.
func (c *checker) binaryOpResult(e *ast.BinaryExpr, op token.Kind, lt, rt types.Type) types.Type {
	switch op {
	// Arithmetic
	case token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT:
		// String concatenation is NOT via `+` in Osty — only numeric.
		if !types.IsNumeric(lt) || !types.IsNumeric(rt) {
			c.errNode(e, diag.CodeBinaryOpUntyped,
				"operator `%s` is not defined on `%s` and `%s`",
				op, lt, rt)
			return types.ErrorType
		}
		res, ok := types.Unify(lt, rt)
		if !ok {
			c.errMismatch(e.Right, lt, rt)
			return lt
		}
		return res
	// Comparison (equality)
	case token.EQ, token.NEQ:
		if !types.IsEqualable(lt) && !types.IsError(lt) {
			c.errNode(e, diag.CodeTypeNotEqual,
				"type `%s` does not implement `Equal`", lt)
			return types.Bool
		}
		// Right must unify with left.
		if _, ok := types.Unify(lt, rt); !ok && !types.IsError(rt) {
			c.errMismatch(e.Right, lt, rt)
		}
		return types.Bool
	// Comparison (ordering)
	case token.LT, token.GT, token.LEQ, token.GEQ:
		if !types.IsOrdered(lt) && !types.IsError(lt) {
			c.errNode(e, diag.CodeTypeNotOrdered,
				"type `%s` does not implement `Ordered`", lt)
			return types.Bool
		}
		if _, ok := types.Unify(lt, rt); !ok && !types.IsError(rt) {
			c.errMismatch(e.Right, lt, rt)
		}
		return types.Bool
	// Logical
	case token.AND, token.OR:
		if !types.IsBool(lt) {
			c.errNode(e.Left, diag.CodeBinaryOpUntyped,
				"logical `%s` requires `Bool`, got `%s`", op, lt)
		}
		if !types.IsBool(rt) {
			c.errNode(e.Right, diag.CodeBinaryOpUntyped,
				"logical `%s` requires `Bool`, got `%s`", op, rt)
		}
		return types.Bool
	// Bitwise + shifts
	case token.BITAND, token.BITOR, token.BITXOR:
		if !types.IsInteger(lt) || !types.IsInteger(rt) {
			c.errNode(e, diag.CodeBinaryOpUntyped,
				"operator `%s` is not defined on `%s` and `%s`",
				op, lt, rt)
			return types.ErrorType
		}
		res, _ := types.Unify(lt, rt)
		return res
	case token.SHL, token.SHR:
		if !types.IsInteger(lt) || !types.IsInteger(rt) {
			c.errNode(e, diag.CodeBinaryOpUntyped,
				"shift requires integer operands, got `%s` and `%s`", lt, rt)
			return types.ErrorType
		}
		return lt
	// Range (produces an iterator-like; use Tuple<a,b> for MVP)
	case token.DOTDOT, token.DOTDOTEQ:
		// Fall-through; normally this is expressed as *RangeExpr, not
		// a BinaryExpr.
		return types.ErrorType
	// Nil-coalescing ??
	case token.QQ:
		inner, ok := types.AsOptional(lt)
		if !ok {
			c.errNode(e.Left, diag.CodeCoalesceNonOptional,
				"left side of `??` must be optional, got `%s`", lt)
			return rt
		}
		if _, ok := types.Unify(inner, rt); !ok && !types.IsError(rt) {
			c.errMismatch(e.Right, inner, rt)
		}
		return inner
	}
	// Unknown op — the parser should have caught it already.
	c.errNode(e, diag.CodeBinaryOpUntyped,
		"operator `%s` is not supported", op)
	return types.ErrorType
}

// ---- Error propagation (?) ----

func (c *checker) questionType(e *ast.QuestionExpr, env *env) types.Type {
	t := c.checkExpr(e.X, nil, env)
	if types.IsError(t) {
		return types.ErrorType
	}
	// Optional: T? ? → T (requires enclosing fn to return Option<_>).
	if inner, ok := types.AsOptional(t); ok {
		if !env.retIsOption {
			c.errNode(e, diag.CodeQuestionBadReturn,
				"`?` on an `Option` value requires the enclosing function to return `Option<_>`")
		}
		return inner
	}
	// Result<T, E> ? → T. Requires the enclosing fn to return a
	// Result, and the propagated Err must be assignable to the
	// enclosing Err type. When the enclosing Err is the `Error`
	// interface (§7), any type that structurally satisfies Error
	// counts (its E must have `message(self) -> String`).
	if n, ok := types.AsNamedByName(t, "Result"); ok {
		if len(n.Args) == 2 {
			if !env.retIsResult {
				c.errNode(e, diag.CodeQuestionBadReturn,
					"`?` on a `Result` value requires the enclosing function to return `Result<_, _>`")
				return n.Args[0]
			}
			errT := n.Args[1]
			encErr := env.retResultErr
			if encErr != nil && !types.IsError(encErr) && !types.Assignable(encErr, errT) {
				if ifaceN, isIface := interfaceNamed(c, encErr); isIface {
					c.satisfies(errT, ifaceN, e)
				} else {
					c.errNode(e, diag.CodeQuestionBadReturn,
						"`?` propagates `%s` but enclosing function returns `Result<_, %s>`",
						errT, encErr)
				}
			}
			return n.Args[0]
		}
	}
	c.errNode(e, diag.CodeQuestionNotPropagate,
		"`?` can only be applied to `Option<_>` or `Result<_, _>`, got `%s`", t)
	return types.ErrorType
}

// ---- Calls, fields, methods ----

func (c *checker) callType(e *ast.CallExpr, hint types.Type, env *env) types.Type {
	// Built-in prelude functions handled by name.
	if id, ok := e.Fn.(*ast.Ident); ok {
		if b := c.tryBuiltinCall(id.Name, e, hint, env); b != nil {
			return b
		}
		// Named constructor of Option / Result variant: Some(x), Ok(v), Err(e), None.
		if t := c.tryVariantCall(id, e, hint, env); t != nil {
			return t
		}
	}
	// Concurrency intrinsics: `thread.chan::<T>(cap)` → Channel<T>,
	// `thread.spawn(f)` → Handle<T>. The generator lowers these to Go
	// primitives; here we only need the checker to agree on the type so
	// downstream method calls (`ch.recv()`, `h.join()`) resolve.
	if t := c.tryThreadCall(e, env); t != nil {
		return t
	}

	// Method call via field: `recv.method(args)`.
	//
	// Special case before the generic method path: if the receiver is
	// a `use` alias (SymPackage), then `pkg.fn(args)` is a call into
	// another package, not a method call. Look the member up in the
	// target package's scope and type-check against its FnType.
	if fx, ok := e.Fn.(*ast.FieldExpr); ok {
		if t, handled := c.tryPackageCall(fx, e, hint, env); handled {
			return t
		}
		return c.methodCallType(fx, e, env)
	}

	// Ident callee: recover the fn's generics + parameter list for
	// monomorphization and keyword/default-arg handling.
	if id, ok := e.Fn.(*ast.Ident); ok {
		if sym := c.symbol(id); sym != nil {
			c.warnIfDeprecated(sym, id.PosV)
			t := c.symTypeOrError(sym)
			fn, isFn := types.AsFn(t)
			if !isFn {
				if types.IsError(t) {
					for _, a := range e.Args {
						c.checkExpr(a.Value, nil, env)
					}
					return types.ErrorType
				}
				c.errNode(e.Fn, diag.CodeNotCallable,
					"value of type `%s` is not callable", t)
				return types.ErrorType
			}
			c.recordExpr(e.Fn, t)
			info := c.info(sym)
			var generics []*types.TypeVar
			if info != nil {
				generics = info.Generics
			}
			params := fnDeclParams(sym)
			return c.applyDeclaredCall(e, fn, generics, params, hint, env)
		}
	}

	// Turbofish callee: strip the turbofish wrapper, reuse its type args
	// as the explicit instantiation.
	if tf, ok := e.Fn.(*ast.TurbofishExpr); ok {
		if id, idOK := tf.Base.(*ast.Ident); idOK {
			if sym := c.symbol(id); sym != nil {
				t := c.symTypeOrError(sym)
				if fn, isFn := types.AsFn(t); isFn {
					c.recordExpr(e.Fn, t)
					info := c.info(sym)
					var generics []*types.TypeVar
					if info != nil {
						generics = info.Generics
					}
					explicit := make([]types.Type, 0, len(tf.Args))
					for _, a := range tf.Args {
						explicit = append(explicit, c.typeOf(a))
					}
					params := fnDeclParams(sym)
					return c.applyDeclaredCallWithExplicit(e, fn, generics, params, explicit, hint, env)
				}
			}
		}
	}

	// Generic call through an arbitrary expression (closures, etc.).
	ft := c.checkExpr(e.Fn, nil, env)
	if types.IsError(ft) {
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.ErrorType
	}
	fn, ok := types.AsFn(ft)
	if !ok {
		c.errNode(e.Fn, diag.CodeNotCallable,
			"value of type `%s` is not callable", ft)
		return types.ErrorType
	}
	return c.applyFnTo(e, e.Args, fn, env)
}

// applyFnTo is the simple (non-generic) apply path: positional arity
// check and per-arg type check. Kept for closure / anonymous fn-type
// calls where generics don't apply.
//
// When the expected parameter type is an interface, the argument is
// validated structurally via satisfies(); non-interface params use the
// usual Assignable relation.
func (c *checker) applyFnTo(call ast.Node, args []*ast.Arg, fn *types.FnType, env *env) types.Type {
	// Over-arity is always wrong. Under-arity is conservatively
	// accepted because default arguments (§3.1) could legitimately
	// forgive the gap and the FnType alone doesn't carry default
	// metadata. When a default-aware shape is wired in, the guard
	// should tighten to `!= len(fn.Params)`.
	if len(args) > len(fn.Params) {
		c.errNode(call, diag.CodeWrongArgCount,
			"too many arguments: expected %d, got %d",
			len(fn.Params), len(args))
	}
	for i, a := range args {
		var pt types.Type
		if i < len(fn.Params) {
			pt = fn.Params[i]
		}
		at := c.checkExpr(a.Value, pt, env)
		if pt == nil || types.IsError(pt) {
			continue
		}
		if ifaceN, isIface := interfaceNamed(c, pt); isIface {
			c.satisfies(at, ifaceN, a.Value)
			continue
		}
		if !c.accepts(pt, at, a.Value) {
			c.errMismatch(a.Value, pt, at)
		}
	}
	return fn.Return
}

// interfaceNamed reports whether a type is an interface Named — either
// a user interface (desc Kind = SymInterface) or a built-in marker
// interface (Equal / Ordered / Hashable / Error).
func interfaceNamed(c *checker, t types.Type) (*types.Named, bool) {
	n, ok := types.AsNamed(t)
	if !ok || n.Sym == nil {
		return nil, false
	}
	switch n.Sym.Name {
	case "Equal", "Ordered", "Hashable", "Error":
		return n, true
	}
	if desc, ok := c.result.Descs[n.Sym]; ok && desc.Kind == resolve.SymInterface {
		return n, true
	}
	return nil, false
}

// applyGenericCall is the monomorphizing call path. Given the fn type
// and its generic type-parameter list, it infers concrete type arguments
// from (a) the surrounding hint for the return type, and (b) each
// positional argument, then substitutes into the param / return types
// and rechecks with the concrete types. The instantiation is recorded
// on Result.Instantiations so the Go transpiler can emit one
// specialized copy per distinct argument list (§2.7.3).
func (c *checker) applyGenericCall(e *ast.CallExpr, fn *types.FnType, generics []*types.TypeVar, hint types.Type, env *env) types.Type {
	return c.applyGenericCallWithArgs(e, fn, generics, nil, hint, env)
}

// applyGenericCallWithArgs is applyGenericCall with an optional explicit
// type-argument list from a turbofish.
func (c *checker) applyGenericCallWithArgs(e *ast.CallExpr, fn *types.FnType, generics []*types.TypeVar, explicit []types.Type, hint types.Type, env *env) types.Type {
	c.checkExplicitGenericArity(e, len(generics), len(explicit))
	if len(generics) == 0 {
		// Non-generic fn — arity and types only.
		return c.applyFnTo(e, e.Args, fn, env)
	}

	// See applyFnTo for the `>`-only rationale: defaults may forgive
	// under-arity but we can't see them from here.
	if len(e.Args) > len(fn.Params) {
		c.errNode(e, diag.CodeWrongArgCount,
			"too many arguments: expected %d, got %d",
			len(fn.Params), len(e.Args))
	}

	sub := make(map[*resolve.Symbol]types.Type, len(generics))
	for i, g := range generics {
		if i < len(explicit) {
			sub[g.Sym] = explicit[i]
		}
	}
	// Hint the return type to seed substitutions (`let y: Int = id(5)`
	// lets us fix T from the expected Int even before walking the
	// argument expression).
	if hint != nil && !types.IsError(hint) {
		inferFromArg(fn.Return, hint, sub)
	}

	// First pass: check each argument with the currently-substituted
	// param type as hint; update sub from each arg's concrete type.
	for i, a := range e.Args {
		if i >= len(fn.Params) {
			c.checkExpr(a.Value, nil, env)
			continue
		}
		pt := types.Substitute(fn.Params[i], sub)
		at := c.checkExpr(a.Value, pt, env)
		inferFromArg(fn.Params[i], at, sub)
	}

	// Default any still-unbound TypeVar to Untyped-default for numeric
	// literals that landed there unhinted.
	for _, g := range generics {
		if t, have := sub[g.Sym]; have {
			if u, ok := t.(*types.Untyped); ok {
				sub[g.Sym] = u.Default()
			}
		}
	}

	// Second pass: verify each arg under the now-concrete param type.
	for i, a := range e.Args {
		if i >= len(fn.Params) {
			continue
		}
		pt := types.Substitute(fn.Params[i], sub)
		at := c.result.Types[a.Value]
		if pt != nil && !types.IsError(pt) && at != nil && !c.accepts(pt, at, a.Value) {
			c.errMismatch(a.Value, pt, at)
		}
	}

	// Record the concrete instantiation for downstream passes.
	instArgs := make([]types.Type, len(generics))
	for i, g := range generics {
		if t, ok := sub[g.Sym]; ok && t != nil {
			instArgs[i] = t
		} else {
			instArgs[i] = g // unbound — keep symbolic
		}
	}
	c.result.Instantiations[e] = instArgs

	// Enforce interface bounds (`T: Ordered`, `T: Hashable`, …) on every
	// inferred instantiation argument.
	for i, g := range generics {
		c.checkBounds(g, instArgs[i], e)
	}

	return types.Substitute(fn.Return, sub)
}

func (c *checker) checkExplicitGenericArity(e *ast.CallExpr, want, got int) {
	if got == 0 || want == got {
		return
	}
	c.errNode(e, diag.CodeGenericArgCount,
		"generic call expects %d type argument(s), got %d", want, got)
}

// methodCallType resolves `recv.method(args)` by looking up the method
// on the receiver type.
// tryPackageCall recognises calls of the shape `pkg.fn(args)` where
// `pkg` is a `use` alias. When `fx.X` resolves to a SymPackage, we
// look `fx.Name` up in that package's PkgScope — the exported symbol
// table populated by ResolvePackage — and call through its FnType.
//
// Returns (resultType, true) when the pattern matched (even if the
// lookup surfaced an error diagnostic), or (nil, false) to let the
// caller fall through to method-call handling for instance receivers.
func (c *checker) tryPackageCall(
	fx *ast.FieldExpr, e *ast.CallExpr, hint types.Type, env *env,
) (types.Type, bool) {
	id, ok := fx.X.(*ast.Ident)
	if !ok {
		return nil, false
	}
	pkgSym := c.symbol(id)
	if pkgSym == nil || pkgSym.Kind != resolve.SymPackage {
		return nil, false
	}
	c.recordExpr(fx.X, types.ErrorType)
	// FFI packages (`use go "path" { fn ... }`) carry their signatures
	// inline in UseDecl.GoBody. The resolver doesn't publish these in a
	// normal PkgScope, so we match by name against the FFI body here so
	// calls like `fmt.Println("x")` type-check against the declared
	// signature and return the declared type.
	if u, ok := pkgSym.Decl.(*ast.UseDecl); ok && u.IsGoFFI {
		if t := c.tryFFICall(u, fx, e, env); t != nil {
			return t, true
		}
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.ErrorType, true
	}
	// Opaque packages (stdlib stubs, FFI without a matching body) have
	// no loaded PkgScope. The resolver already approved the access
	// permissively; produce an ErrorType so subsequent operations
	// degrade gracefully rather than noising up diagnostics with
	// questions we can't answer yet.
	if pkgSym.Package == nil || pkgSym.Package.PkgScope == nil {
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.ErrorType, true
	}
	tgt := pkgSym.Package.PkgScope.LookupLocal(fx.Name)
	if tgt == nil {
		// Resolver already reported E0508; stay silent here and keep
		// type-checking downstream arguments.
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.ErrorType, true
	}
	t := c.symTypeOrError(tgt)
	var generics []*types.TypeVar
	if types.IsError(t) {
		if fnDecl, ok := tgt.Decl.(*ast.FnDecl); ok {
			t = c.externalFnType(fnDecl)
		}
	}
	fn, isFn := types.AsFn(t)
	if !isFn {
		if types.IsError(t) {
			for _, a := range e.Args {
				c.checkExpr(a.Value, nil, env)
			}
			return types.ErrorType, true
		}
		c.errNode(e.Fn, diag.CodeNotCallable,
			"value of type `%s` is not callable", t)
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.ErrorType, true
	}
	c.recordExpr(e.Fn, t)
	info := c.info(tgt)
	if info != nil && len(generics) == 0 {
		generics = info.Generics
	}
	return c.applyGenericCall(e, fn, generics, hint, env), true
}

func (c *checker) methodCallType(fx *ast.FieldExpr, e *ast.CallExpr, env *env) types.Type {
	// Builder protocol (§3.4) takes priority over regular method lookup
	// — Type.builder() / value.toBuilder() / chain setters / .build().
	// tryBuilderCall handles argument type-checking on its own paths.
	if t, handled := c.tryBuilderCall(fx, e, env); handled {
		return t
	}
	recvT := c.checkExpr(fx.X, nil, env)
	// TaskGroup.spawn(|| body) is parametric in the closure's return —
	// intercept here before the standard method lookup so the returned
	// Handle<T> carries the right T.
	if n, ok := types.AsNamed(recvT); ok && n.Sym != nil && n.Sym.Name == "TaskGroup" && fx.Name == "spawn" {
		if len(e.Args) != 1 {
			c.errNode(e, diag.CodeWrongArgCount,
				"`TaskGroup.spawn` takes exactly 1 argument, got %d", len(e.Args))
			return types.ErrorType
		}
		at := c.checkExpr(e.Args[0].Value, nil, env)
		fn, ok := types.AsFn(at)
		if !ok {
			if !types.IsError(at) {
				c.errNode(e.Args[0].Value, diag.CodeTypeMismatch,
					"`TaskGroup.spawn` expects a closure, got `%s`", at)
			}
			return types.ErrorType
		}
		ret := fn.Return
		if ret == nil {
			ret = types.Unit
		}
		return c.handleOf(ret)
	}
	c.recordExpr(fx, types.ErrorType) // placeholder, refine below
	if types.IsError(recvT) {
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.ErrorType
	}
	// Optional receiver with ?. — unwrap, but the result must be
	// wrapped back in Optional.
	inner, wasOptional := types.AsOptional(recvT)
	if fx.IsOptional {
		if !wasOptional {
			c.errNode(fx, diag.CodeOptionalChainOnNon,
				"`?.` requires an optional receiver, got `%s`", recvT)
			inner = recvT
		}
		recvT = inner
	}

	md, sub := c.lookupMethod(recvT, fx.Name)
	if md == nil {
		if _, known := stdlibMethods[fx.Name]; known {
			// Well-known stdlib / builtin method — accept any arguments
			// and produce an approximate return type (escape hatch until
			// the stdlib is modelled in full).
			return c.stdlibCallReturn(fx, recvT, e, env, wasOptional && fx.IsOptional)
		}
		c.errMethodNotFound(fx, fmt.Sprintf("type `%s`", recvT),
			fx.Name, c.methodCandidates(recvT))
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		if wasOptional && fx.IsOptional {
			return &types.Optional{Inner: types.ErrorType}
		}
		return types.ErrorType
	}
	c.warnIfMethodDeprecated(md, fx.PosV)
	// Start with the method's declared Fn, then pre-apply owner generic
	// substitutions from the receiver's concrete type args. Method's own
	// generics (method-local `<U>`) are then inferred per-argument via
	// applyDeclaredCall, which also resolves keyword / default arguments.
	fn := md.Fn
	if len(sub) > 0 {
		fn = &types.FnType{
			Params: make([]types.Type, len(fn.Params)),
			Return: types.Substitute(fn.Return, sub),
		}
		for i, p := range md.Fn.Params {
			fn.Params[i] = types.Substitute(p, sub)
		}
	}
	ret := c.applyDeclaredCall(e, fn, md.Generics, md.Params, nil, env)
	if wasOptional && fx.IsOptional {
		return &types.Optional{Inner: ret}
	}
	return ret
}

// stdlibMethods is the set of well-known method names for which the
// checker accepts any argument types and synthesizes a return type via
// stdlibCallReturn. Escape hatch until the stdlib is modelled in full.
//
// The value is the fixed return type for methods whose result doesn't
// depend on the receiver. Methods whose return type depends on the
// receiver (`get`, `map`, `filter`, …) have a nil entry and go through
// the per-name switch below.
var stdlibMethods = map[string]types.Type{
	"len":      types.Int,
	"isEmpty":  types.Bool,
	"contains": types.Bool,
	"toString": types.String,
	"toInt":    types.Int,
	"toInt32":  types.Int32,
	"toInt64":  types.Int64,
	"toFloat":  types.Float,
	// Receiver-dependent — nil means "dispatch by name".
	"get":       nil,
	"push":      nil,
	"map":       nil,
	"filter":    nil,
	"iter":      nil,
	"entries":   nil,
	"enumerate": nil,
	"keys":      nil,
	"values":    nil,
	"toList":    nil,
	"toSet":     nil,
	"toMap":     nil,
	"chars":     nil,
	"take":      nil,
}

// stdlibCallReturn synthesizes a return type for an escape-hatch stdlib
// method call. For methods whose declared shape is known — len,
// map, filter, push, get — arguments are type-checked against the
// expected signature; other names accept any arguments permissively.
func (c *checker) stdlibCallReturn(fx *ast.FieldExpr, recvT types.Type, e *ast.CallExpr, env *env, optChain bool) types.Type {
	var ret types.Type
	switch fx.Name {
	case "len", "isEmpty":
		c.checkExactArity(e, 0)
		ret = stdlibMethods[fx.Name]
	case "toString":
		c.checkExactArity(e, 0)
		ret = types.String
	case "toInt", "toInt32", "toInt64", "toFloat":
		c.checkExactArity(e, 0)
		ret = stdlibMethods[fx.Name]
	case "contains":
		if len(e.Args) != 1 {
			c.errNode(e, diag.CodeWrongArgCount,
				"`contains` takes 1 argument, got %d", len(e.Args))
			for _, a := range e.Args {
				c.checkExpr(a.Value, nil, env)
			}
		} else {
			elem := stdlibElement(recvT)
			at := c.checkExpr(e.Args[0].Value, elem, env)
			if elem != nil && !types.IsError(elem) && !c.accepts(elem, at, e.Args[0].Value) {
				c.errMismatch(e.Args[0].Value, elem, at)
			}
		}
		ret = types.Bool
	case "get":
		c.checkExactArity(e, 1)
		if len(e.Args) == 1 {
			keyT := stdlibKeyOrIndex(recvT)
			at := c.checkExpr(e.Args[0].Value, keyT, env)
			if keyT != nil && !types.IsError(keyT) && !c.accepts(keyT, at, e.Args[0].Value) {
				c.errMismatch(e.Args[0].Value, keyT, at)
			}
		}
		ret = stdlibGetReturn(recvT)
	case "push":
		c.checkExactArity(e, 1)
		if len(e.Args) == 1 {
			elem := stdlibElement(recvT)
			at := c.checkExpr(e.Args[0].Value, elem, env)
			if elem != nil && !types.IsError(elem) && !c.accepts(elem, at, e.Args[0].Value) {
				c.errMismatch(e.Args[0].Value, elem, at)
			}
		}
		ret = types.Unit
	case "map":
		ret = c.stdlibMap(e, recvT, env)
	case "filter":
		ret = c.stdlibFilter(e, recvT, env)
	case "take":
		c.checkExactArity(e, 1)
		if len(e.Args) == 1 {
			c.checkExpr(e.Args[0].Value, types.Int, env)
		}
		ret = c.listOf(iterElem(recvT))
	case "iter", "toList":
		c.checkExactArity(e, 0)
		ret = c.listOf(iterElem(recvT))
	case "entries", "keys", "values":
		c.checkExactArity(e, 0)
		ret = c.stdlibChainReturn(fx.Name, recvT)
	case "enumerate":
		c.checkExactArity(e, 0)
		ret = c.stdlibChainReturn(fx.Name, recvT)
	case "toSet":
		c.checkExactArity(e, 0)
		ret = c.namedOf("Set", []types.Type{iterElem(recvT)})
	case "toMap":
		c.checkExactArity(e, 0)
		ret = recvT
	case "chars":
		c.checkExactArity(e, 0)
		ret = c.listOf(types.Char)
	default:
		// Unmodelled name — walk args permissively, return Error.
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		ret = types.ErrorType
	}
	if optChain {
		return &types.Optional{Inner: ret}
	}
	return ret
}

// checkExactArity verifies a stdlib call has exactly `want` arguments,
// emitting E0701 otherwise. Args are walked with no hint so further
// errors surface.
func (c *checker) checkExactArity(e *ast.CallExpr, want int) {
	if len(e.Args) == want {
		return
	}
	c.errNode(e, diag.CodeWrongArgCount,
		"expected %d argument(s), got %d", want, len(e.Args))
}

// stdlibElement returns the element type for an iterable receiver.
// For List<T>/Set<T>/Chan<T> that's T; for Map<K,V> it's (K, V). For
// non-iterable receivers returns ErrorType so the caller can skip
// callback shape checks without emitting a type mismatch.
func stdlibElement(recvT types.Type) types.Type {
	return iterElem(recvT)
}

// stdlibKeyOrIndex returns the appropriate indexer type for a
// List<T>.get / Map<K,V>.get / Set<T>.contains call — Int for List,
// the key type for Map, element for Set.
func stdlibKeyOrIndex(recvT types.Type) types.Type {
	if n, ok := recvT.(*types.Named); ok && n.Sym != nil {
		switch n.Sym.Name {
		case "List":
			return types.Int
		case "Map":
			if len(n.Args) == 2 {
				return n.Args[0]
			}
		case "Set":
			if len(n.Args) == 1 {
				return n.Args[0]
			}
		}
	}
	return nil
}

// stdlibMap handles `xs.map(|x| ...)`. The callback's parameter type
// is the receiver's element type; its return type becomes the new
// list's element type. Arity is always 1.
func (c *checker) stdlibMap(e *ast.CallExpr, recvT types.Type, env *env) types.Type {
	if len(e.Args) != 1 {
		c.errNode(e, diag.CodeWrongArgCount,
			"`map` takes 1 callback argument, got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return c.listOf(iterElem(recvT))
	}
	elem := iterElem(recvT)
	hint := &types.FnType{Params: []types.Type{elem}, Return: nil}
	got := c.checkExpr(e.Args[0].Value, hint, env)
	if fn, ok := types.AsFn(got); ok {
		return c.listOf(fn.Return)
	}
	return c.listOf(types.ErrorType)
}

// stdlibFilter handles `xs.filter(|x| -> Bool)`. Callback must return
// Bool; result keeps the receiver's element type.
func (c *checker) stdlibFilter(e *ast.CallExpr, recvT types.Type, env *env) types.Type {
	if len(e.Args) != 1 {
		c.errNode(e, diag.CodeWrongArgCount,
			"`filter` takes 1 callback argument, got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return c.listOf(iterElem(recvT))
	}
	elem := iterElem(recvT)
	hint := &types.FnType{Params: []types.Type{elem}, Return: types.Bool}
	got := c.checkExpr(e.Args[0].Value, hint, env)
	if fn, ok := types.AsFn(got); ok {
		if !types.IsBool(fn.Return) && !types.IsError(fn.Return) {
			c.errNode(e.Args[0].Value, diag.CodeTypeMismatch,
				"`filter` callback must return `Bool`, got `%s`", fn.Return)
		}
	}
	return c.listOf(elem)
}

// stdlibGetReturn returns `T?` for List<T>.get / Set<T>.get, `V?` for
// Map<K,V>.get, and a sentinel Optional<Error> otherwise.
func stdlibGetReturn(recvT types.Type) types.Type {
	if n, ok := recvT.(*types.Named); ok && n.Sym != nil {
		switch n.Sym.Name {
		case "List", "Set":
			if len(n.Args) == 1 {
				return &types.Optional{Inner: n.Args[0]}
			}
		case "Map":
			if len(n.Args) == 2 {
				return &types.Optional{Inner: n.Args[1]}
			}
		}
	}
	return &types.Optional{Inner: types.ErrorType}
}

// stdlibChainReturn approximates the return type of iterator-style
// chaining methods. The goal is to keep `xs.map(|x| ...).filter(...).
// toList()` fluent without fully modelling Iter<T>.
func (c *checker) stdlibChainReturn(name string, recvT types.Type) types.Type {
	elem := iterElem(recvT)
	list := c.listOf(elem)
	switch name {
	case "map", "filter", "take", "iter", "toList":
		return list
	case "entries":
		// Map<K,V>.entries() → List<(K, V)>
		if n, ok := recvT.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Map" && len(n.Args) == 2 {
			tup := &types.Tuple{Elems: []types.Type{n.Args[0], n.Args[1]}}
			return c.listOf(tup)
		}
		return list
	case "enumerate":
		return c.listOf(&types.Tuple{Elems: []types.Type{types.Int, elem}})
	case "keys":
		if n, ok := recvT.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Map" && len(n.Args) == 2 {
			return c.listOf(n.Args[0])
		}
		return list
	case "values":
		if n, ok := recvT.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Map" && len(n.Args) == 2 {
			return c.listOf(n.Args[1])
		}
		return list
	case "toSet":
		return c.namedOf("Set", []types.Type{elem})
	case "toMap":
		return recvT
	case "chars":
		return c.listOf(types.Char)
	case "push":
		return types.Unit
	}
	return list
}

// iterElem is the free-function element extractor used by stdlib
// chains; it handles the built-in iterables. User-defined iterables
// need the method table and go through (*checker).iterElement.
func iterElem(t types.Type) types.Type {
	switch v := t.(type) {
	case *types.Named:
		if v.Sym != nil {
			switch v.Sym.Name {
			case "List", "Set":
				if len(v.Args) == 1 {
					return v.Args[0]
				}
			case "Map":
				if len(v.Args) == 2 {
					return &types.Tuple{Elems: []types.Type{v.Args[0], v.Args[1]}}
				}
			case "Chan", "Channel":
				if len(v.Args) == 1 {
					return v.Args[0]
				}
			}
		}
	case *types.Primitive:
		if v.Kind == types.PString {
			return types.Byte
		}
		if v.Kind == types.PBytes {
			return types.Byte
		}
	}
	return types.ErrorType
}

// iterElement is the full iterator-protocol element resolver (§15).
// It first tries the built-in shapes via iterElem, then walks the
// method table:
//
//   - A type with `iter(self) -> I` for some I that itself has a
//     `next(mut self) -> T?` method iterates over T.
//   - A type with `next(mut self) -> T?` is already an iterator and
//     iterates over T.
//
// Returns ErrorType when no iteration shape could be determined. A
// diagnostic is emitted at `pos` for clearly non-iterable receivers so
// the for-in user sees an actionable message.
func (c *checker) iterElement(t types.Type, pos ast.Node) types.Type {
	if t == nil || types.IsError(t) {
		return types.ErrorType
	}
	if elem := iterElem(t); !types.IsError(elem) {
		return elem
	}
	// Range literal type: modelled as List<Int>, already handled above.
	// User shapes — look for iter() or next().
	if md, sub := c.lookupMethod(t, "iter"); md != nil {
		ret := md.Fn.Return
		if len(sub) > 0 {
			ret = types.Substitute(ret, sub)
		}
		// The iterator's Next() determines the element type.
		if elem := c.iteratorElement(ret); !types.IsError(elem) {
			return elem
		}
	}
	if elem := c.iteratorElement(t); !types.IsError(elem) {
		return elem
	}
	if pos != nil {
		c.errNode(pos, diag.CodeIterableNotProtocol,
			"type `%s` does not implement the iterator protocol (§15)", t)
	}
	return types.ErrorType
}

// iteratorElement reads the `next(self) -> T?` method on an iterator
// type and returns T. Returns ErrorType when the type has no suitable
// next method.
func (c *checker) iteratorElement(t types.Type) types.Type {
	md, sub := c.lookupMethod(t, "next")
	if md == nil {
		return types.ErrorType
	}
	ret := md.Fn.Return
	if len(sub) > 0 {
		ret = types.Substitute(ret, sub)
	}
	inner, ok := types.AsOptional(ret)
	if !ok {
		return types.ErrorType
	}
	return inner
}

func (c *checker) listOf(elem types.Type) types.Type {
	return c.namedOf("List", []types.Type{elem})
}

func (c *checker) resultOf(ok, err types.Type) types.Type {
	return c.namedOf("Result", []types.Type{ok, err})
}

func (c *checker) externalFnType(fn *ast.FnDecl) *types.FnType {
	params := make([]types.Type, 0, len(fn.Params))
	for _, p := range fn.Params {
		params = append(params, c.ffiType(p.Type))
	}
	var ret types.Type = types.Unit
	if fn.ReturnType != nil {
		ret = c.ffiType(fn.ReturnType)
	}
	return &types.FnType{Params: params, Return: ret}
}

func (c *checker) namedOf(name string, args []types.Type) types.Type {
	if sym := c.lookupBuiltin(name); sym != nil {
		return &types.Named{Sym: sym, Args: args}
	}
	return types.ErrorType
}

// lookupMethod finds a method by name on the receiver's type, applying
// substitutions for generic receivers. Returns (desc, sub) where `sub`
// maps the owning type's generics to the receiver's concrete args.
func (c *checker) lookupMethod(recvT types.Type, name string) (*methodDesc, map[*resolve.Symbol]types.Type) {
	switch v := recvT.(type) {
	case *types.Named:
		if desc, ok := c.result.Descs[v.Sym]; ok {
			if md, ok := desc.Methods[name]; ok {
				sub := bindArgs(desc.Generics, v.Args)
				return md, sub
			}
			if desc.Kind == resolve.SymInterface {
				if md, ok := c.interfaceMethodSet(v)[name]; ok {
					return md, nil
				}
			}
		}
		// Builtin compound types carry synthetic method tables.
		if v.Sym != nil && v.Sym.Kind == resolve.SymBuiltin {
			if md := c.builtinNamedMethod(v, name); md != nil {
				return md, nil
			}
		}
	case *types.Primitive:
		// Intrinsic methods from stdlib (populated via Opts.Primitives).
		// Anything not in that table still falls through to the legacy
		// stdlibCallReturn escape hatch below.
		if primMap := c.primMethods[v.Kind]; primMap != nil {
			if md, ok := primMap[name]; ok {
				return md, nil
			}
		}
	case *types.Optional:
		if md := c.optionalMethod(v, name); md != nil {
			return md, nil
		}
	case *types.TypeVar:
		for _, b := range v.Bounds {
			n, ok := b.(*types.Named)
			if !ok {
				continue
			}
			if md, ok := c.interfaceMethodSet(n)[name]; ok {
				return md, nil
			}
		}
	}
	return nil, nil
}

// optionalMethod returns a synthetic methodDesc for Option<T> intrinsic
// methods. T is already substituted into the signature so call-site
// checking proceeds without a separate substitution map.
func (c *checker) optionalMethod(o *types.Optional, name string) *methodDesc {
	t := o.Inner
	opt := o
	switch name {
	case "isSome", "isNone":
		return simpleMethod(name, nil, types.Bool)
	case "unwrap":
		return simpleMethod(name, nil, t)
	case "unwrapOr":
		return simpleMethod(name, []types.Type{t}, t)
	case "orElse":
		fn := &types.FnType{Params: nil, Return: opt}
		return simpleMethod(name, []types.Type{fn}, opt)
	case "map":
		// fn(T) -> U — but U is fresh per call. We approximate by
		// returning Option<?> and let the specific checker path
		// refine via the callback's inferred return.
		fn := &types.FnType{Params: []types.Type{t}, Return: types.ErrorType}
		return simpleMethod(name, []types.Type{fn}, &types.Optional{Inner: types.ErrorType})
	case "toString":
		return simpleMethod(name, nil, types.String)
	}
	return nil
}

// builtinNamedMethod returns a synthetic methodDesc for a builtin
// generic type's intrinsic method — primarily Result<T, E> and the
// List / Map / Set collections whose full stdlib APIs are covered by
// stdlibCallReturn but whose core methods deserve typed signatures.
func (c *checker) builtinNamedMethod(n *types.Named, name string) *methodDesc {
	switch n.Sym.Name {
	case "Result":
		if len(n.Args) != 2 {
			return nil
		}
		return resultMethod(n, name)
	case "Chan", "Channel":
		// §8.5: channel methods recognized by the checker. The gen
		// package rewrites these to Go's built-in chan primitives at
		// call time, so the signatures below only need to type-check
		// the arguments and the return.
		if len(n.Args) != 1 {
			return nil
		}
		t := n.Args[0]
		switch name {
		case "recv":
			return simpleMethod(name, nil, &types.Optional{Inner: t})
		case "close":
			return simpleMethod(name, nil, types.Unit)
		case "send":
			return simpleMethod(name, []types.Type{t}, types.Unit)
		}
	case "Handle":
		if len(n.Args) != 1 {
			return nil
		}
		t := n.Args[0]
		switch name {
		case "join":
			return simpleMethod(name, nil, t)
		}
	case "TaskGroup":
		// §8.1: TaskGroup.spawn is parametric in the closure's return
		// type; methodCallType intercepts it before reaching here so
		// the synthesized desc below is only used as a fallback shape.
		switch name {
		case "spawn":
			fn := &types.FnType{Params: nil, Return: types.ErrorType}
			return simpleMethod(name, []types.Type{fn}, types.ErrorType)
		case "cancel":
			return simpleMethod(name, nil, types.Unit)
		case "isCancelled":
			return simpleMethod(name, nil, types.Bool)
		}
	}
	return nil
}

// resultMethod returns the signature of a Result<T, E> intrinsic method.
func resultMethod(n *types.Named, name string) *methodDesc {
	t := n.Args[0]
	e := n.Args[1]
	switch name {
	case "isOk", "isErr":
		return simpleMethod(name, nil, types.Bool)
	case "unwrap":
		return simpleMethod(name, nil, t)
	case "unwrapErr":
		return simpleMethod(name, nil, e)
	case "unwrapOr":
		return simpleMethod(name, []types.Type{t}, t)
	case "ok":
		return simpleMethod(name, nil, &types.Optional{Inner: t})
	case "err":
		return simpleMethod(name, nil, &types.Optional{Inner: e})
	case "map":
		fn := &types.FnType{Params: []types.Type{t}, Return: types.ErrorType}
		return simpleMethod(name, []types.Type{fn},
			&types.Named{Sym: n.Sym, Args: []types.Type{types.ErrorType, e}})
	case "mapErr":
		fn := &types.FnType{Params: []types.Type{e}, Return: types.ErrorType}
		return simpleMethod(name, []types.Type{fn},
			&types.Named{Sym: n.Sym, Args: []types.Type{t, types.ErrorType}})
	case "toString":
		return simpleMethod(name, nil, types.String)
	}
	return nil
}

// simpleMethod builds a bare methodDesc from a param-type list and
// return type. Used for synthetic Optional / Result / collection
// methods whose signatures are already fully substituted.
func simpleMethod(name string, params []types.Type, ret types.Type) *methodDesc {
	return &methodDesc{
		Name: name,
		Fn:   &types.FnType{Params: params, Return: ret},
	}
}

// tryBuiltinCall handles prelude-defined callables like `println`,
// `print`, `dbg`, `parallel`, `taskGroup`. Returns nil when `name` isn't
// a known builtin.
func (c *checker) tryBuiltinCall(name string, e *ast.CallExpr, hint types.Type, env *env) types.Type {
	switch name {
	case "println", "print", "eprintln", "eprint", "dbg":
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.Unit
	case "spawn":
		// `spawn(|| body)` — takes a 0-arg closure, runs it asynchronously,
		// returns Handle<T> where T is the closure's return type.
		if len(e.Args) != 1 {
			c.errNode(e, diag.CodeWrongArgCount,
				"`spawn` takes exactly 1 argument, got %d", len(e.Args))
			return types.ErrorType
		}
		at := c.checkExpr(e.Args[0].Value, nil, env)
		fn, ok := types.AsFn(at)
		if !ok {
			if !types.IsError(at) {
				c.errNode(e.Args[0].Value, diag.CodeTypeMismatch,
					"`spawn` expects a closure, got `%s`", at)
			}
			return types.ErrorType
		}
		ret := fn.Return
		if ret == nil {
			ret = types.Unit
		}
		return c.handleOf(ret)
	case "taskGroup":
		// §8.1: taskGroup(|g| body) — the closure receives a TaskGroup
		// handle and returns T; the group waits for all spawned tasks
		// before the outer call returns T. The enclosing call's hint
		// (e.g. `return taskGroup(...)` from an fn returning Result<...>)
		// flows into the closure so its `Ok(...)` / `Err(...)` pick up
		// the expected type parameters.
		if len(e.Args) != 1 {
			c.errNode(e, diag.CodeWrongArgCount,
				"`taskGroup` takes exactly 1 argument, got %d", len(e.Args))
			return types.ErrorType
		}
		tgSym := c.topLevelSym("TaskGroup")
		if tgSym == nil {
			return types.ErrorType
		}
		var innerRet types.Type = types.ErrorType
		if hint != nil && !types.IsError(hint) {
			innerRet = hint
		}
		closureHint := &types.FnType{
			Params: []types.Type{&types.Named{Sym: tgSym}},
			Return: innerRet,
		}
		at := c.checkExpr(e.Args[0].Value, closureHint, env)
		if fn, ok := types.AsFn(at); ok {
			if fn.Return != nil {
				return fn.Return
			}
			return types.Unit
		}
		return types.ErrorType
	case "parallel":
		// §8.3 production form:
		//
		//   parallel(items, concurrency, |x| work(x))
		//
		// maps a List<T> through a Result-returning callback and returns
		// List<Result<R, Error>>. Keep the earlier variadic-closure form
		// below for existing tests and simple concurrency expressions.
		if len(e.Args) == 3 {
			itemsT := c.checkExpr(e.Args[0].Value, nil, env)
			if n, ok := types.AsNamed(itemsT); ok && n.Sym != nil && n.Sym.Name == "List" && len(n.Args) == 1 {
				c.checkExpr(e.Args[1].Value, types.Int, env)
				mapperRet := c.resultOf(types.ErrorType, c.namedOf("Error", nil))
				if hn, ok := types.AsNamed(hint); ok && hn.Sym != nil && hn.Sym.Name == "List" && len(hn.Args) == 1 {
					if rn, ok := types.AsNamed(hn.Args[0]); ok && rn.Sym != nil && rn.Sym.Name == "Result" && len(rn.Args) == 2 {
						mapperRet = hn.Args[0]
					}
				}
				mapperHint := &types.FnType{Params: []types.Type{n.Args[0]}, Return: mapperRet}
				got := c.checkExpr(e.Args[2].Value, mapperHint, env)
				if fn, ok := types.AsFn(got); ok && fn.Return != nil {
					return c.listOf(fn.Return)
				}
				return c.listOf(mapperRet)
			}
		}
		// §8.3: parallel(|| a, || b, ...) runs every closure concurrently
		// and returns a List<T> in source order. All closures must
		// agree on their return type.
		if len(e.Args) == 0 {
			return c.listOf(types.Unit)
		}
		var elemT types.Type
		for _, a := range e.Args {
			at := c.checkExpr(a.Value, nil, env)
			fn, ok := types.AsFn(at)
			if !ok {
				if !types.IsError(at) {
					c.errNode(a.Value, diag.CodeTypeMismatch,
						"`parallel` expects closures, got `%s`", at)
				}
				continue
			}
			ret := fn.Return
			if ret == nil {
				ret = types.Unit
			}
			if elemT == nil {
				elemT = ret
			}
		}
		if elemT == nil {
			elemT = types.ErrorType
		}
		return c.listOf(elemT)
	}
	return nil
}

// tryThreadCall handles the `thread.chan::<T>(cap)` and related
// intrinsics. Returns the synthesized return type, or nil when the
// expression isn't a recognized intrinsic.
func (c *checker) tryThreadCall(e *ast.CallExpr, env *env) types.Type {
	// Unwrap optional turbofish to get to `thread.<name>`.
	base := e.Fn
	var typeArgs []ast.Type
	if tf, ok := base.(*ast.TurbofishExpr); ok {
		base = tf.Base
		typeArgs = tf.Args
	}
	fx, ok := base.(*ast.FieldExpr)
	if !ok {
		return nil
	}
	head, ok := fx.X.(*ast.Ident)
	if !ok || head.Name != "thread" {
		return nil
	}
	switch fx.Name {
	case "chan":
		// Signature: thread.chan::<T>(cap: Int) -> Channel<T>
		for _, a := range e.Args {
			c.checkExpr(a.Value, types.Int, env)
		}
		var t types.Type = types.ErrorType
		if len(typeArgs) == 1 {
			t = c.typeOf(typeArgs[0])
		}
		return c.channelOf(t)
	case "spawn":
		// thread.spawn(|| body) → Handle<T>
		if len(e.Args) != 1 {
			c.errNode(e, diag.CodeWrongArgCount,
				"`thread.spawn` takes exactly 1 argument, got %d", len(e.Args))
			return types.ErrorType
		}
		at := c.checkExpr(e.Args[0].Value, nil, env)
		if fn, ok := types.AsFn(at); ok {
			ret := fn.Return
			if ret == nil {
				ret = types.Unit
			}
			return c.handleOf(ret)
		}
		return types.ErrorType
	case "sleep":
		// thread.sleep(dur: Duration) -> (). Accept any arg; the gen
		// stage rewrites N.s / N.ms shorthand into time.Duration.
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.Unit
	case "yield":
		return types.Unit
	case "select":
		// thread.select(|s| { ... }) — the selector arms are method
		// calls on an anonymous selector; we don't model its type,
		// and the gen stage lowers the closure body to a Go select
		// statement. Check the closure permissively.
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return types.Unit
	}
	return nil
}

// tryFFICall matches `pkg.Name(args)` against an FFI body's fn
// declaration and type-checks the call against the declared signature.
// Returns the declared return type, or nil when no matching fn is
// found.
//
// FFI body type references (e.g. `String` in `fn Println(s: String)`)
// aren't walked by the resolver, so c.typeOf can't resolve them via the
// TypeRefs table. We fall back to a name-based prelude lookup here
// which covers the scalar types and common generic prelude shapes.
func (c *checker) tryFFICall(u *ast.UseDecl, fx *ast.FieldExpr, e *ast.CallExpr, env *env) types.Type {
	var fn *ast.FnDecl
	for _, gd := range u.GoBody {
		if f, ok := gd.(*ast.FnDecl); ok && f.Name == fx.Name {
			fn = f
			break
		}
	}
	if fn == nil {
		return nil
	}
	params := make([]types.Type, 0, len(fn.Params))
	for _, p := range fn.Params {
		params = append(params, c.ffiType(p.Type))
	}
	var ret types.Type = types.Unit
	if fn.ReturnType != nil {
		ret = c.ffiType(fn.ReturnType)
	}
	ft := &types.FnType{Params: params, Return: ret}
	c.recordExpr(e.Fn, ft)
	return c.applyFnTo(e, e.Args, ft, env)
}

// ffiType resolves an FFI body AST type reference via name-based prelude
// lookup. Mirrors c.typeOf but doesn't require the resolver's TypeRefs
// table — the FFI body isn't walked by the resolver, so type refs there
// have no recorded symbol.
func (c *checker) ffiType(n ast.Type) types.Type {
	if n == nil {
		return types.Unit
	}
	switch x := n.(type) {
	case *ast.NamedType:
		if len(x.Path) != 1 {
			return types.ErrorType
		}
		name := x.Path[0]
		if t, ok := c.builtinScalarType(name); ok {
			return t
		}
		// Generic prelude types (List<T>, Option<T>, Result<T, E>, Channel<T>).
		if sym := c.topLevelSym(name); sym != nil {
			args := make([]types.Type, 0, len(x.Args))
			for _, a := range x.Args {
				args = append(args, c.ffiType(a))
			}
			if name == "Option" && len(args) == 1 {
				return &types.Optional{Inner: args[0]}
			}
			return &types.Named{Sym: sym, Args: args}
		}
		return types.ErrorType
	case *ast.OptionalType:
		return &types.Optional{Inner: c.ffiType(x.Inner)}
	case *ast.TupleType:
		if len(x.Elems) == 0 {
			return types.Unit
		}
		elems := make([]types.Type, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = c.ffiType(e)
		}
		return &types.Tuple{Elems: elems}
	case *ast.FnType:
		params := make([]types.Type, len(x.Params))
		for i, p := range x.Params {
			params[i] = c.ffiType(p)
		}
		var ret types.Type = types.Unit
		if x.ReturnType != nil {
			ret = c.ffiType(x.ReturnType)
		}
		return &types.FnType{Params: params, Return: ret}
	}
	return types.ErrorType
}

// channelOf builds a Channel<T> Named type bound to the prelude's
// Channel builtin. Returns an error type when the prelude name is
// missing (shouldn't happen in normal builds).
func (c *checker) channelOf(t types.Type) types.Type {
	if sym := c.topLevelSym("Channel"); sym != nil {
		return &types.Named{Sym: sym, Args: []types.Type{t}}
	}
	return types.ErrorType
}

// handleOf builds a Handle<T> Named type bound to the prelude's Handle
// builtin, for the return of spawn.
func (c *checker) handleOf(t types.Type) types.Type {
	if sym := c.topLevelSym("Handle"); sym != nil {
		return &types.Named{Sym: sym, Args: []types.Type{t}}
	}
	return types.ErrorType
}

// tryVariantCall handles calls of the form `Some(x)`, `None()` (rejected
// here), `Ok(v)`, `Err(e)`, plus user-defined variants by name.
func (c *checker) tryVariantCall(id *ast.Ident, e *ast.CallExpr, hint types.Type, env *env) types.Type {
	sym := c.symbol(id)
	if sym == nil {
		return nil
	}
	if sym.Kind == resolve.SymBuiltin {
		switch sym.Name {
		case "Some":
			if len(e.Args) != 1 {
				c.errNode(e, diag.CodeWrongArgCount,
					"`Some` takes exactly 1 argument, got %d", len(e.Args))
				return types.ErrorType
			}
			// Use hint's inner for argument inference when present.
			var innerHint types.Type
			if o, ok := hint.(*types.Optional); ok {
				innerHint = o.Inner
			}
			at := c.checkExpr(e.Args[0].Value, innerHint, env)
			return &types.Optional{Inner: at}
		case "None":
			c.errNode(e, diag.CodeWrongArgCount,
				"`None` is a unit variant — write `None`, not `None()`")
			return types.ErrorType
		case "Ok":
			if len(e.Args) != 1 {
				c.errNode(e, diag.CodeWrongArgCount,
					"`Ok` takes exactly 1 argument, got %d", len(e.Args))
				return types.ErrorType
			}
			var okHint types.Type
			var errHint types.Type
			if n, ok := types.AsNamedBuiltin(hint, "Result"); ok && len(n.Args) == 2 {
				okHint, errHint = n.Args[0], n.Args[1]
			}
			at := c.checkExpr(e.Args[0].Value, okHint, env)
			resSym := c.lookupBuiltin("Result")
			if resSym == nil {
				return types.ErrorType
			}
			if errHint == nil {
				errHint = types.ErrorType
			}
			return &types.Named{Sym: resSym, Args: []types.Type{at, errHint}}
		case "Err":
			if len(e.Args) != 1 {
				c.errNode(e, diag.CodeWrongArgCount,
					"`Err` takes exactly 1 argument, got %d", len(e.Args))
				return types.ErrorType
			}
			var okHint types.Type
			var errHint types.Type
			if n, ok := types.AsNamedBuiltin(hint, "Result"); ok && len(n.Args) == 2 {
				okHint, errHint = n.Args[0], n.Args[1]
			}
			at := c.checkExpr(e.Args[0].Value, errHint, env)
			resSym := c.lookupBuiltin("Result")
			if resSym == nil {
				return types.ErrorType
			}
			if okHint == nil {
				okHint = types.ErrorType
			}
			return &types.Named{Sym: resSym, Args: []types.Type{okHint, at}}
		}
	}

	if sym.Kind == resolve.SymVariant {
		info := c.info(sym)
		if info == nil || info.Enum == nil {
			return nil
		}
		desc := info.Enum
		c.warnIfDeprecated(sym, id.PosV)
		if vd, ok := desc.Variants[sym.Name]; ok {
			c.warnIfVariantDeprecated(vd, id.PosV)
		}
		// Pull type arguments from a matching hint first so the same
		// enum's variants flow concrete args naturally:
		//     let r: Result<Int, Error> = Ok(5)   // 5 is Int
		var tArgs []types.Type
		if n, ok := types.AsNamed(hint); ok && n.Sym == desc.Sym && len(n.Args) == len(desc.Generics) {
			tArgs = n.Args
		} else {
			// Fresh argument slot per generic; we'll refine while
			// checking each payload arg.
			tArgs = make([]types.Type, len(desc.Generics))
			for i := range tArgs {
				tArgs[i] = nil
			}
		}

		// Check argument count.
		fields := info.VariantFields
		if len(e.Args) != len(fields) {
			c.errNode(e, diag.CodeVariantShape,
				"variant `%s` takes %d payload value(s), got %d",
				sym.Name, len(fields), len(e.Args))
		}
		// Check each argument. If the field type is a TypeVar, try to
		// infer it from the argument's actual type.
		sub := map[*resolve.Symbol]types.Type{}
		for i, g := range desc.Generics {
			if i < len(tArgs) && tArgs[i] != nil {
				sub[g.Sym] = tArgs[i]
			}
		}
		for i, a := range e.Args {
			var ft types.Type
			if i < len(fields) {
				ft = fields[i]
				if len(sub) > 0 {
					ft = substituteTypeVars(ft, sub)
				}
			}
			at := c.checkExpr(a.Value, ft, env)
			if i < len(fields) {
				inferFromArg(fields[i], at, sub)
			}
			if ft != nil && !types.IsError(ft) && !c.accepts(ft, at, a.Value) {
				c.errMismatch(a.Value, ft, at)
			}
		}
		// Materialize the concrete type-argument list: hint wins, then
		// inferred; generics we couldn't pin stay as TypeVars to avoid
		// synthesizing wrong concrete types.
		finalArgs := make([]types.Type, len(desc.Generics))
		for i, g := range desc.Generics {
			if t, ok := sub[g.Sym]; ok && t != nil {
				finalArgs[i] = t
				continue
			}
			if i < len(tArgs) && tArgs[i] != nil {
				finalArgs[i] = tArgs[i]
				continue
			}
			finalArgs[i] = g
		}
		// Untyped fallbacks: if we inferred an untyped int/float for a
		// generic, default it rather than leaking the Untyped marker.
		for i, t := range finalArgs {
			if u, ok := t.(*types.Untyped); ok {
				finalArgs[i] = u.Default()
			}
		}
		return &types.Named{Sym: desc.Sym, Args: finalArgs}
	}

	return nil
}

// inferFromArg populates `sub` when `field` is a TypeVar and `arg` is
// concrete. Recursively descends into composite types so a field
// `List<T>` with arg `List<Int>` infers T = Int.
func inferFromArg(field, arg types.Type, sub map[*resolve.Symbol]types.Type) {
	if field == nil || arg == nil {
		return
	}
	switch f := field.(type) {
	case *types.TypeVar:
		if f.Sym == nil {
			return
		}
		if _, have := sub[f.Sym]; have {
			return
		}
		sub[f.Sym] = arg
	case *types.Optional:
		if a, ok := arg.(*types.Optional); ok {
			inferFromArg(f.Inner, a.Inner, sub)
		}
	case *types.Tuple:
		if a, ok := arg.(*types.Tuple); ok && len(f.Elems) == len(a.Elems) {
			for i, e := range f.Elems {
				inferFromArg(e, a.Elems[i], sub)
			}
		}
	case *types.Named:
		if a, ok := arg.(*types.Named); ok && f.Sym == a.Sym && len(f.Args) == len(a.Args) {
			for i, x := range f.Args {
				inferFromArg(x, a.Args[i], sub)
			}
		}
	case *types.FnType:
		if a, ok := arg.(*types.FnType); ok && len(f.Params) == len(a.Params) {
			for i, p := range f.Params {
				inferFromArg(p, a.Params[i], sub)
			}
			inferFromArg(f.Return, a.Return, sub)
		}
	}
}

// ---- Field / index ----

func (c *checker) fieldType(fx *ast.FieldExpr, env *env) types.Type {
	recvT := c.checkExpr(fx.X, nil, env)
	if types.IsError(recvT) {
		return types.ErrorType
	}
	inner, wasOpt := types.AsOptional(recvT)
	if fx.IsOptional {
		if !wasOpt {
			c.errNode(fx, diag.CodeOptionalChainOnNon,
				"`?.` requires an optional receiver, got `%s`", recvT)
			inner = recvT
		}
		recvT = inner
	}
	// Package access (`pkg.Name` without a call). When `pkg` is a
	// loaded package we look `Name` up in its exported scope and
	// return the declared type so the caller can assign it, pass it,
	// etc. Opaque packages (stdlib stubs, FFI) stay on ErrorType
	// since we haven't modelled their members.
	if id, ok := fx.X.(*ast.Ident); ok {
		if s := c.symbol(id); s != nil && s.Kind == resolve.SymPackage {
			if s.Package == nil || s.Package.PkgScope == nil {
				return types.ErrorType
			}
			tgt := s.Package.PkgScope.LookupLocal(fx.Name)
			if tgt == nil {
				// Resolver already reported E0508.
				return types.ErrorType
			}
			return c.symTypeOrError(tgt)
		}
	}
	if n, ok := types.AsNamed(recvT); ok {
		if desc, ok := c.result.Descs[n.Sym]; ok {
			// Field access on a struct — take precedence over method
			// lookup so `foo.bar` reads the field even when a method
			// of the same name exists. (Spec has no such collision:
			// R19 forbids field/method name clashes on a single type.)
			if desc.Kind == resolve.SymStruct {
				for _, f := range desc.Fields {
					if f.Name != fx.Name {
						continue
					}
					c.warnIfFieldDeprecated(f, fx.PosV)
					t := f.Type
					if sub := types.BindArgs(desc.Generics, n.Args); len(sub) > 0 {
						t = types.Substitute(t, sub)
					}
					if wasOpt && fx.IsOptional {
						return &types.Optional{Inner: t}
					}
					return t
				}
			}
			// Method reference (§4.9): `value.method` without `()` is
			// the bound method viewed as a function value. The receiver
			// type is dropped from the signature; type-level generics
			// on the owning type are substituted.
			if md, sub := c.lookupMethod(recvT, fx.Name); md != nil {
				ft := md.Fn
				if len(sub) > 0 {
					ft = &types.FnType{
						Params: make([]types.Type, len(ft.Params)),
						Return: types.Substitute(ft.Return, sub),
					}
					for i, p := range md.Fn.Params {
						ft.Params[i] = types.Substitute(p, sub)
					}
				}
				if wasOpt && fx.IsOptional {
					return &types.Optional{Inner: ft}
				}
				return ft
			}
			if desc.Kind == resolve.SymStruct {
				// Build candidate list from fields + methods so a typo
				// on either kind of member gets the right hint.
				cand := make([]string, 0, len(desc.Fields)+len(desc.Methods))
				for _, f := range desc.Fields {
					cand = append(cand, f.Name)
				}
				for name := range desc.Methods {
					cand = append(cand, name)
				}
				c.errFieldNotFound(fx, n.Sym.Name, fx.Name, cand)
			} else {
				methods := c.interfaceMethodSet(n)
				cand := make([]string, 0, len(desc.Methods)+len(methods))
				for name := range desc.Methods {
					cand = append(cand, name)
				}
				for name := range methods {
					cand = append(cand, name)
				}
				c.errMethodNotFound(fx, fmt.Sprintf("type `%s`", recvT), fx.Name, cand)
			}
			return types.ErrorType
		}
	}
	return types.ErrorType
}

func (c *checker) indexType(e *ast.IndexExpr, env *env) types.Type {
	xt := c.checkExpr(e.X, nil, env)
	it := c.checkExpr(e.Index, nil, env)
	if types.IsError(xt) {
		return types.ErrorType
	}
	// Range-index → slice
	if _, ok := e.Index.(*ast.RangeExpr); ok {
		if n, ok := types.AsNamed(xt); ok && n.Sym != nil && n.Sym.Name == "List" {
			return xt // List slice is List<T>
		}
		if p, ok := xt.(*types.Primitive); ok && (p.Kind == types.PString || p.Kind == types.PBytes) {
			return xt
		}
	}
	// List/Map/Set/Bytes/String scalar index
	if n, ok := types.AsNamed(xt); ok && n.Sym != nil {
		switch n.Sym.Name {
		case "List":
			if len(n.Args) == 1 {
				if !types.IsInteger(it) {
					c.errNode(e.Index, diag.CodeTypeMismatch,
						"list index must be integer, got `%s`", it)
				}
				return n.Args[0]
			}
		case "Map":
			if len(n.Args) == 2 {
				if !types.Assignable(n.Args[0], it) {
					c.errMismatch(e.Index, n.Args[0], it)
				}
				return n.Args[1]
			}
		case "Set":
			// Set is not directly indexable; contains() is the usual API.
			c.errNode(e, diag.CodeNotIndexable,
				"type `%s` is not indexable", xt)
			return types.ErrorType
		}
	}
	if p, ok := xt.(*types.Primitive); ok {
		switch p.Kind {
		case types.PString:
			if !types.IsInteger(it) {
				c.errNode(e.Index, diag.CodeTypeMismatch,
					"string index must be integer, got `%s`", it)
			}
			return types.Byte
		case types.PBytes:
			if !types.IsInteger(it) {
				c.errNode(e.Index, diag.CodeTypeMismatch,
					"bytes index must be integer, got `%s`", it)
			}
			return types.Byte
		}
	}
	c.errNode(e, diag.CodeNotIndexable,
		"type `%s` is not indexable", xt)
	return types.ErrorType
}

// ---- Turbofish / range / tuple / list / map ----

func (c *checker) turbofishType(e *ast.TurbofishExpr, hint types.Type, env *env) types.Type {
	// Turbofish forwards the underlying expression's type; tracking
	// per-site generic instantiation is future work.
	return c.checkExpr(e.Base, hint, env)
}

func (c *checker) rangeType(e *ast.RangeExpr, env *env) types.Type {
	if e.Start != nil {
		c.checkExpr(e.Start, nil, env)
	}
	if e.Stop != nil {
		c.checkExpr(e.Stop, nil, env)
	}
	// Internal representation: a List<Int> conceptually. Real Osty uses
	// an iterator protocol; at the checker level we model it as the
	// primitive integer type so `for i in 0..10` picks up `Int`.
	return c.listOf(types.Int)
}

func (c *checker) tupleType(e *ast.TupleExpr, hint types.Type, env *env) types.Type {
	// Flow hint element-by-element when compatible.
	var hintElems []types.Type
	if t, ok := hint.(*types.Tuple); ok && len(t.Elems) == len(e.Elems) {
		hintElems = t.Elems
	}
	elems := make([]types.Type, len(e.Elems))
	for i, x := range e.Elems {
		var h types.Type
		if i < len(hintElems) {
			h = hintElems[i]
		}
		elems[i] = c.checkExpr(x, h, env)
	}
	if len(elems) == 0 {
		return types.Unit
	}
	if len(elems) == 1 {
		return elems[0] // surface (x,) → x; parser should prevent this
	}
	return &types.Tuple{Elems: elems}
}

func (c *checker) listType(e *ast.ListExpr, hint types.Type, env *env) types.Type {
	var elemHint types.Type
	if n, ok := hint.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "List" && len(n.Args) == 1 {
		elemHint = n.Args[0]
	}
	if len(e.Elems) == 0 {
		// Empty list [] — the hint determines element type; otherwise
		// we produce List<?> with ErrorType element to suppress further
		// complaints. `let xs: List<Int> = []` resolves through hint.
		if elemHint != nil {
			return c.listOf(elemHint)
		}
		return c.listOf(types.ErrorType)
	}
	var elemT types.Type
	for _, x := range e.Elems {
		t := c.checkExpr(x, elemHint, env)
		if elemT == nil {
			elemT = t
			continue
		}
		if u, ok := types.Unify(elemT, t); ok {
			elemT = u
		} else {
			c.errMismatch(x, elemT, t)
		}
	}
	// Fix untyped element to its default if no hint.
	if elemHint == nil {
		if u, ok := elemT.(*types.Untyped); ok {
			elemT = u.Default()
		}
	}
	return c.listOf(elemT)
}

func (c *checker) mapType(e *ast.MapExpr, hint types.Type, env *env) types.Type {
	var kHint, vHint types.Type
	if n, ok := hint.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Map" && len(n.Args) == 2 {
		kHint, vHint = n.Args[0], n.Args[1]
	}
	if e.Empty || len(e.Entries) == 0 {
		if kHint != nil && vHint != nil {
			return c.namedOf("Map", []types.Type{kHint, vHint})
		}
		return c.namedOf("Map", []types.Type{types.ErrorType, types.ErrorType})
	}
	var kT, vT types.Type
	for _, ent := range e.Entries {
		kt := c.checkExpr(ent.Key, kHint, env)
		vt := c.checkExpr(ent.Value, vHint, env)
		if kT == nil {
			kT = kt
		} else if u, ok := types.Unify(kT, kt); ok {
			kT = u
		} else {
			c.errMismatch(ent.Key, kT, kt)
		}
		if vT == nil {
			vT = vt
		} else if u, ok := types.Unify(vT, vt); ok {
			vT = u
		} else {
			c.errMismatch(ent.Value, vT, vt)
		}
	}
	if u, ok := kT.(*types.Untyped); ok {
		kT = u.Default()
	}
	if u, ok := vT.(*types.Untyped); ok {
		vT = u.Default()
	}
	return c.namedOf("Map", []types.Type{kT, vT})
}

// ---- Struct literal ----

func (c *checker) structLitType(e *ast.StructLit, env *env) types.Type {
	// The Type expr is an Ident (or FieldExpr chain for pkg.Type) that
	// the resolver already traced to a SymStruct (in the MVP).
	typeSym := c.structLitSym(e.Type)
	if typeSym == nil {
		// Fall-through: resolve fields so cascading errors surface.
		for _, f := range e.Fields {
			if f.Value != nil {
				c.checkExpr(f.Value, nil, env)
			}
		}
		if e.Spread != nil {
			c.checkExpr(e.Spread, nil, env)
		}
		return types.ErrorType
	}
	desc, ok := c.result.Descs[typeSym]
	if !ok || desc.Kind != resolve.SymStruct {
		c.errNode(e, diag.CodeNotAStruct,
			"`%s` is not a struct", typeSym.Name)
		return types.ErrorType
	}

	selfT := &types.Named{Sym: typeSym, Args: argsOfGenerics(desc.Generics)}
	want := map[string]*fieldDesc{}
	for _, f := range desc.Fields {
		want[f.Name] = f
	}

	// When a spread source is present, the spread's static type must be
	// the same struct — we require identity, not just any struct. This
	// rejects `Point { ..someOtherType }` cleanly.
	spreadCoversAll := false
	if e.Spread != nil {
		spreadT := c.checkExpr(e.Spread, selfT, env)
		if spreadT != nil && !types.IsError(spreadT) {
			n, ok := types.AsNamed(spreadT)
			if !ok || n.Sym != typeSym {
				c.errNode(e.Spread, diag.CodeTypeMismatch,
					"spread source must be a `%s` value, got `%s`",
					typeSym.Name, spreadT)
			} else {
				// Matching struct value carries concrete values for
				// every declared field, so all remaining required
				// fields are satisfied.
				spreadCoversAll = true
			}
		}
	}

	fieldNames := make([]string, 0, len(desc.Fields))
	for _, f := range desc.Fields {
		fieldNames = append(fieldNames, f.Name)
	}
	provided := map[string]bool{}
	for _, f := range e.Fields {
		fd, ok := want[f.Name]
		if !ok {
			b := diag.New(diag.Error,
				fmt.Sprintf("no field `%s` on struct `%s`", f.Name, typeSym.Name)).
				Code(diag.CodeUnknownStructField).
				Primary(diag.Span{Start: f.Pos(), End: f.End()}, "no such field")
			if s := suggestFrom(f.Name, fieldNames); s != "" {
				b.Hint(fmt.Sprintf("did you mean `%s`?", s))
			}
			c.emit(b.Build())
			if f.Value != nil {
				c.checkExpr(f.Value, nil, env)
			}
			continue
		}
		provided[f.Name] = true
		var vt types.Type
		if f.Value != nil {
			vt = c.checkExpr(f.Value, fd.Type, env)
		} else {
			// Field shorthand `{ name }`: look up binding of same name.
			id := &ast.Ident{PosV: f.PosV, EndV: f.PosV, Name: f.Name}
			vt = c.checkExpr(id, fd.Type, env)
		}
		if !c.accepts(fd.Type, vt, f) {
			c.errMismatch(f, fd.Type, vt)
		}
	}
	// Every declared field must be either listed, defaulted, or supplied
	// by a matching spread source. The previous implementation skipped
	// the coverage check whenever any spread was present — that allowed
	// `Point { ..notAPoint }` to be accepted once the spread-type error
	// fired. Now we only honor the spread when it actually has the
	// right type.
	if !spreadCoversAll {
		for name, fd := range want {
			if provided[name] || fd.HasDef {
				continue
			}
			c.errNode(e, diag.CodeMissingStructField,
				"struct literal for `%s` is missing field `%s`",
				typeSym.Name, name)
		}
	}
	return selfT
}

// structLitSym walks a StructLit's Type expr to recover the struct's
// Symbol. Handles bare Ident and pkg.Type FieldExpr chains.
func (c *checker) structLitSym(e ast.Expr) *resolve.Symbol {
	switch x := e.(type) {
	case *ast.Ident:
		return c.symbol(x)
	case *ast.FieldExpr:
		return c.structLitSym(x.X) // MVP: only validate the head
	}
	return nil
}

// ---- If / match / closure / block ----

func (c *checker) ifType(e *ast.IfExpr, hint types.Type, env *env) types.Type {
	if e.IsIfLet {
		// RHS type checked as Cond; pattern bound in `checkPattern`
		// (stmt-side; if-let in expression position still binds).
		condT := c.checkExpr(e.Cond, nil, env)
		// The pattern's binding will be used by the then block body.
		// We reuse bindPatternTypes to install types for any idents.
		c.bindPatternTypes(e.Pattern, condT, env)
	} else {
		cond := c.checkExpr(e.Cond, types.Bool, env)
		if !types.IsBool(cond) && !types.IsError(cond) {
			c.errNode(e.Cond, diag.CodeConditionNotBool,
				"`if` condition must be `Bool`, got `%s`", cond)
		}
	}
	thenT := c.blockAsExprType(e.Then, hint, env)
	var elseT types.Type = types.Unit
	if e.Else != nil {
		elseT = c.checkExpr(e.Else, hint, env)
	} else {
		// `if` without else is only valid when used as a statement
		// (unit-typed); the parser enforces this via context.
		elseT = types.Unit
	}
	if u, ok := types.Unify(thenT, elseT); ok {
		return u
	}
	c.errNode(e, diag.CodeIfBranchMismatch,
		"`if` branches have different types: `%s` vs `%s`", thenT, elseT)
	return thenT
}

func (c *checker) matchType(e *ast.MatchExpr, hint types.Type, env *env) types.Type {
	scrutT := c.checkExpr(e.Scrutinee, nil, env)
	var resultT types.Type
	for _, arm := range e.Arms {
		c.bindPatternTypes(arm.Pattern, scrutT, env)
		if arm.Guard != nil {
			gT := c.checkExpr(arm.Guard, types.Bool, env)
			if !types.IsBool(gT) && !types.IsError(gT) {
				c.errNode(arm.Guard, diag.CodeConditionNotBool,
					"match guard must be `Bool`, got `%s`", gT)
			}
		}
		armT := c.checkExpr(arm.Body, hint, env)
		if resultT == nil {
			resultT = armT
			continue
		}
		if u, ok := types.Unify(resultT, armT); ok {
			resultT = u
			continue
		}
		c.errNode(arm, diag.CodeMatchArmMismatch,
			"match arms have incompatible types: `%s` vs `%s`", resultT, armT)
	}
	c.checkExhaustive(e, scrutT)
	if resultT == nil {
		return types.Unit
	}
	return resultT
}

func (c *checker) closureType(e *ast.ClosureExpr, hint types.Type, parent *env) types.Type {
	// If hint is a FnType, use its param types for closure params that
	// lack annotations.
	var hintFn *types.FnType
	if f, ok := hint.(*types.FnType); ok {
		hintFn = f
	}
	paramTs := make([]types.Type, len(e.Params))
	for i, p := range e.Params {
		if p.Type != nil {
			paramTs[i] = c.typeOf(p.Type)
		} else if hintFn != nil && i < len(hintFn.Params) {
			paramTs[i] = hintFn.Params[i]
		} else {
			paramTs[i] = types.ErrorType
		}
		if p.Name != "" {
			if sym := c.symByDecl(p); sym != nil {
				c.setSymType(sym, paramTs[i])
			}
		}
	}
	var retT types.Type = types.Unit
	if e.ReturnType != nil {
		retT = c.typeOf(e.ReturnType)
	} else if hintFn != nil && hintFn.Return != nil {
		retT = hintFn.Return
	}
	bodyEnv := &env{retType: retT}
	if rn, ok := types.AsNamedByName(retT, "Result"); ok && len(rn.Args) == 2 {
		bodyEnv.retIsResult = true
		bodyEnv.retResultErr = rn.Args[1]
	}
	if _, ok := retT.(*types.Optional); ok {
		bodyEnv.retIsOption = true
	}
	bodyT := c.checkExpr(e.Body, retT, bodyEnv)
	if e.ReturnType != nil && !c.accepts(retT, bodyT, e.Body) {
		c.errMismatch(e.Body, retT, bodyT)
	}
	if e.ReturnType == nil {
		retT = bodyT
	}
	return &types.FnType{Params: paramTs, Return: retT}
}

// blockAsExprType computes the type of a Block when used as an
// expression — the last statement's type (if an ExprStmt), else Unit.
//
// Also performs unreachable-statement detection (E0760): any
// statement that follows a divergent one (return / break / continue
// / a Never-typed expression) produces a diagnostic at the first
// offending line. Reporting once per block keeps the output tight.
func (c *checker) blockAsExprType(b *ast.Block, hint types.Type, env *env) types.Type {
	if b == nil {
		return types.Unit
	}
	n := len(b.Stmts)
	diverged := false
	for i := 0; i < n-1; i++ {
		s := b.Stmts[i]
		if diverged {
			c.errNode(s, diag.CodeUnreachableCode,
				"unreachable statement: the preceding statement always diverges")
			// Continue checking so subsequent type errors still surface,
			// but don't re-emit the unreachable warning — one per block.
			diverged = false
		}
		c.checkStmt(s, env)
		if stmtDiverges(s, c.result.Types) {
			diverged = true
		}
	}
	if n == 0 {
		return types.Unit
	}
	last := b.Stmts[n-1]
	if diverged {
		c.errNode(last, diag.CodeUnreachableCode,
			"unreachable statement: the preceding statement always diverges")
	}
	if es, ok := last.(*ast.ExprStmt); ok {
		return c.checkExpr(es.X, hint, env)
	}
	c.checkStmt(last, env)
	return types.Unit
}

// stmtDiverges reports whether a statement always transfers control
// out of the enclosing block: explicit return / break / continue, an
// expression statement whose type is Never, or a nested block whose
// final statement diverges. Conservative — false for if/match without
// a fully-analyzed per-branch flow (that analysis lives on the
// expression side via typed Never).
func stmtDiverges(s ast.Stmt, exprTypes map[ast.Expr]types.Type) bool {
	switch n := s.(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.ExprStmt:
		if t, ok := exprTypes[n.X]; ok && types.IsNever(t) {
			return true
		}
		// if/match whose branches all diverge also yield Never via
		// blockAsExprType recursion — the map lookup above covers it.
	}
	return false
}
