package check

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// tryBuilderCall intercepts method calls that participate in the
// auto-derived builder protocol (§3.4). Returns (resultType, true) when
// the call was handled, or (nil, false) to let the regular method-call
// path handle it.
//
// Patterns handled:
//   - Type.default()        — if every field has a default
//   - Type.builder()        — if every private field has a default
//   - value.toBuilder()     — whenever builder() is available
//   - builder.field(v)      — fluent setter on a Builder<T>
//   - builder.build()       — finalize; verify required pub fields
//
// A user-defined method of the same name on the struct shadows the
// auto-generated one (§3.4 "Override"): if the struct has a method with
// the same name the regular lookup wins, so tryBuilderCall first checks
// the receiver's method table and bails out when there's a shadowing
// user definition.
func (c *checker) tryBuilderCall(fx *ast.FieldExpr, e *ast.CallExpr, env *env) (types.Type, bool) {
	// Case A: receiver is an in-flight Builder<T>.
	if recvT := c.result.Types[fx.X]; recvT != nil {
		if b, ok := recvT.(*types.Builder); ok {
			return c.builderStep(b, fx, e, env), true
		}
	}
	// The receiver expression hasn't been typed yet for the outer call.
	// Type it now so we can inspect whether we're looking at a Builder,
	// a struct value, or a struct-type reference.
	recvT := c.checkExpr(fx.X, nil, env)
	if b, ok := recvT.(*types.Builder); ok {
		return c.builderStep(b, fx, e, env), true
	}

	// Case B: Type.default() / Type.builder() — receiver is the struct
	// type itself. The resolver hands us an Ident whose Symbol.Kind is
	// SymStruct (or TypeAlias collapsing to a struct).
	if id, isID := fx.X.(*ast.Ident); isID {
		if sym := c.symbol(id); sym != nil {
			if desc, ok := c.result.Descs[sym]; ok && desc.Kind == resolve.SymStruct {
				if _, shadowed := desc.Methods[fx.Name]; shadowed {
					return nil, false // user override wins
				}
				switch fx.Name {
				case "builder":
					return c.builderFromType(desc, e), true
				case "default":
					return c.defaultFromType(desc, e), true
				}
			}
		}
	}

	// Case C: value.toBuilder() — receiver is a struct value.
	if n, ok := types.AsNamed(recvT); ok {
		if desc, ok := c.result.Descs[n.Sym]; ok && desc.Kind == resolve.SymStruct {
			if _, shadowed := desc.Methods[fx.Name]; shadowed {
				return nil, false
			}
			if fx.Name == "toBuilder" {
				return c.toBuilderFromValue(desc, n, e), true
			}
		}
	}
	return nil, false
}

// builderFromType handles `Type.builder()`. The builder is available
// only when every private field has an explicit default (§3.4).
func (c *checker) builderFromType(desc *typeDesc, e *ast.CallExpr) types.Type {
	for _, f := range desc.Fields {
		if !f.Pub && !f.HasDef {
			c.errNode(e, diag.CodeUnknownMethod,
				"struct `%s` has no `builder()`: private field `%s` must have a default",
				desc.Sym.Name, f.Name)
			return types.ErrorType
		}
	}
	if len(e.Args) > 0 {
		c.errNode(e, diag.CodeWrongArgCount,
			"`builder()` takes no arguments, got %d", len(e.Args))
	}
	selfT := &types.Named{Sym: desc.Sym, Args: argsOfGenerics(desc.Generics)}
	return &types.Builder{Struct: selfT, Set: map[string]bool{}}
}

// defaultFromType handles `Type.default()`. Generated only when every
// field has a default or a zero-value type.
func (c *checker) defaultFromType(desc *typeDesc, e *ast.CallExpr) types.Type {
	for _, f := range desc.Fields {
		if f.HasDef {
			continue
		}
		if hasZeroValue(f.Type) {
			continue
		}
		c.errNode(e, diag.CodeUnknownMethod,
			"struct `%s` has no `default()`: field `%s` (`%s`) has no default or zero value",
			desc.Sym.Name, f.Name, f.Type)
		return types.ErrorType
	}
	if len(e.Args) > 0 {
		c.errNode(e, diag.CodeWrongArgCount,
			"`default()` takes no arguments, got %d", len(e.Args))
	}
	return &types.Named{Sym: desc.Sym, Args: argsOfGenerics(desc.Generics)}
}

// toBuilderFromValue handles `value.toBuilder()`. Available whenever
// builder() is available. Preloaded=true so `.build()` treats all
// required fields as already populated.
func (c *checker) toBuilderFromValue(desc *typeDesc, n *types.Named, e *ast.CallExpr) types.Type {
	for _, f := range desc.Fields {
		if !f.Pub && !f.HasDef {
			c.errNode(e, diag.CodeUnknownMethod,
				"struct `%s` has no `toBuilder()`: private field `%s` must have a default",
				desc.Sym.Name, f.Name)
			return types.ErrorType
		}
	}
	if len(e.Args) > 0 {
		c.errNode(e, diag.CodeWrongArgCount,
			"`toBuilder()` takes no arguments, got %d", len(e.Args))
	}
	return &types.Builder{Struct: n, Set: map[string]bool{}, Preloaded: true}
}

// builderStep handles both `.fieldName(v)` (setter) and `.build()` on a
// Builder<T>.
func (c *checker) builderStep(b *types.Builder, fx *ast.FieldExpr, e *ast.CallExpr, env *env) types.Type {
	if fx.Name == "build" {
		return c.builderBuild(b, e)
	}
	return c.builderSet(b, fx, e, env)
}

// builderSet validates a `.field(value)` call on a Builder: the field
// must exist and be pub, the value must have the declared field type.
// Returns a fresh Builder with the field added to Set.
func (c *checker) builderSet(b *types.Builder, fx *ast.FieldExpr, e *ast.CallExpr, env *env) types.Type {
	if b.Struct == nil {
		return types.ErrorType
	}
	desc, ok := c.result.Descs[b.Struct.Sym]
	if !ok {
		return types.ErrorType
	}
	var target *fieldDesc
	for _, f := range desc.Fields {
		if f.Name == fx.Name {
			target = f
			break
		}
	}
	if target == nil {
		c.errNode(fx, diag.CodeUnknownField,
			"builder for `%s` has no setter `%s`", b.Struct.Sym.Name, fx.Name)
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return b
	}
	if !target.Pub {
		c.errNode(fx, diag.CodeUnknownField,
			"builder setter `%s` is not public", fx.Name)
		for _, a := range e.Args {
			c.checkExpr(a.Value, nil, env)
		}
		return b
	}
	if len(e.Args) != 1 {
		c.errNode(e, diag.CodeWrongArgCount,
			"builder setter `%s` takes exactly 1 argument, got %d",
			fx.Name, len(e.Args))
		return b
	}
	// Apply any receiver-side generic substitution.
	ft := target.Type
	if sub := types.BindArgs(desc.Generics, b.Struct.Args); len(sub) > 0 {
		ft = types.Substitute(ft, sub)
	}
	at := c.checkExpr(e.Args[0].Value, ft, env)
	if !types.Assignable(ft, at) {
		c.errMismatch(e.Args[0].Value, ft, at)
	}
	return b.WithField(fx.Name)
}

// builderBuild validates a `.build()` terminal call: every required
// public field (no default) must have been set somewhere earlier in
// the chain.
func (c *checker) builderBuild(b *types.Builder, e *ast.CallExpr) types.Type {
	if len(e.Args) > 0 {
		c.errNode(e, diag.CodeWrongArgCount,
			"`build()` takes no arguments, got %d", len(e.Args))
	}
	if b.Struct == nil {
		return types.ErrorType
	}
	if b.Preloaded {
		return b.Struct
	}
	desc, ok := c.result.Descs[b.Struct.Sym]
	if !ok {
		return b.Struct
	}
	var missing []string
	for _, f := range desc.Fields {
		if !f.Pub || f.HasDef {
			continue
		}
		if !b.Set[f.Name] {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		c.errNode(e, diag.CodeMissingStructField,
			"cannot call `build()`: required field(s) not set: `%s`",
			strings.Join(missing, "`, `"))
	}
	return b.Struct
}

// hasZeroValue reports whether a type has an implicit zero value the
// auto-derived default() can use. Numerics / Bool / String / Bytes /
// Char / Optional / collections qualify; struct/enum without user-
// supplied defaults do not (they'd require their own default()).
func hasZeroValue(t types.Type) bool {
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind.IsNumeric() || v.Kind == types.PBool || v.Kind == types.PString ||
			v.Kind == types.PBytes || v.Kind == types.PChar
	case *types.Optional:
		return true // None
	case *types.Named:
		if v.Sym == nil {
			return false
		}
		// Collections default to empty.
		switch v.Sym.Name {
		case "List", "Map", "Set":
			return true
		}
	}
	return false
}
