package gen

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

// emitMatch lowers a match expression used in value position.
//
// The output is an IIFE so match remains usable in expressions
// (e.g. `let a = match s { ... }`). Each arm lowers to an `if` with a
// test produced by patternTest and a body that returns the arm expr.
// Guards apply *after* bindings, so they can reference pattern names.
//
// A trailing `panic("unreachable match")` covers the non-exhaustive
// case. Actual exhaustiveness checking is the type checker's job; the
// transpiler just runs the generated code honestly.
//
// When the enclosing statement has lifted this match into statement
// position (because an arm body contains an explicit `return`), the
// IIFE is replaced by a reference to the result var recorded in
// g.matchSubs — see preLiftMatches.
func (g *gen) emitMatch(m *ast.MatchExpr) {
	if sub, ok := g.matchSubs[m]; ok {
		g.body.write(sub)
		return
	}
	retType := "any"
	if t := g.typeOf(m); t != nil && !types.IsError(t) && !types.IsUnit(t) {
		retType = g.goType(t)
	} else if g.retHintGo != "" {
		retType = g.retHintGo
	}
	// Keep retHint alive through arm bodies: each arm returns the IIFE's
	// value, whose type we just pinned above; a nested match/if/Ok/Err at
	// the arm tail should see the same expected type as this match does.
	name := g.freshVar("_m")
	g.body.writef("func() %s { %s := ", retType, name)
	g.emitExpr(m.Scrutinee)
	g.body.writeln("; _ = " + name)
	g.body.indent()

	scrutType := g.typeOf(m.Scrutinee)
	// If the last arm is an unguarded catch-all we know the match is
	// total — skip the trailing panic (Go flags it as unreachable).
	totallyCovered := false
	for i, a := range m.Arms {
		g.emitMatchArm(name, scrutType, a, true)
		if i == len(m.Arms)-1 && g.isCatchAll(a.Pattern) && a.Guard == nil {
			totallyCovered = true
		}
	}
	if !totallyCovered {
		g.body.writeln(`panic("unreachable match")`)
	}
	g.body.dedent()
	g.body.write("}()")
}

// emitMatchStmt lowers a match used in statement position, where the
// arms' return values are discarded. No IIFE wrapper is emitted; the
// result is a sequence of `if <test> { bindings; body }` blocks.
func (g *gen) emitMatchStmt(m *ast.MatchExpr) {
	name := g.freshVar("_m")
	g.body.writef("{ %s := ", name)
	g.emitExpr(m.Scrutinee)
	g.body.writef("; _ = %s\n", name)
	g.body.indent()

	scrutType := g.typeOf(m.Scrutinee)
	for _, a := range m.Arms {
		g.emitMatchArm(name, scrutType, a, false)
	}
	g.body.dedent()
	g.body.writeln("}")
}

// emitMatchArm lowers one `pattern [if guard] -> body,` arm.
//
// When asExpr is true the body produces a return value; when false it
// runs purely for its side-effects. In both cases bindings are written
// before the guard so the guard can reference them.
func (g *gen) emitMatchArm(scrut string, scrutType types.Type, arm *ast.MatchArm, asExpr bool) {
	catchAll := g.isCatchAll(arm.Pattern)

	// Fast path: a wildcard / bare-ident catch-all without a guard
	// always fires, so no outer `if` is needed.
	if catchAll && arm.Guard == nil {
		g.body.writeln("{")
		g.body.indent()
		g.emitPatternBindings(scrut, scrutType, arm.Pattern)
		g.emitArmTrailer(arm.Body, asExpr)
		g.body.dedent()
		g.body.writeln("}")
		return
	}

	if !catchAll {
		g.body.write("if ")
		g.emitPatternTest(scrut, scrutType, arm.Pattern)
		g.body.writeln(" {")
		g.body.indent()
	} else {
		// Catch-all with a guard: bindings + guard-test only.
		g.body.writeln("{")
		g.body.indent()
	}

	g.emitPatternBindings(scrut, scrutType, arm.Pattern)

	if arm.Guard != nil {
		g.body.write("if ")
		g.emitExpr(arm.Guard)
		g.body.writeln(" {")
		g.body.indent()
		g.emitArmTrailer(arm.Body, asExpr)
		g.body.dedent()
		g.body.writeln("}")
	} else {
		g.emitArmTrailer(arm.Body, asExpr)
	}

	g.body.dedent()
	g.body.writeln("}")
}

// emitArmTrailer writes the body of a match arm, either as a return
// (expression mode) or as a plain statement followed by `return` from
// the enclosing IIFE — except in statement mode, where no return is
// emitted.
func (g *gen) emitArmTrailer(body ast.Expr, asExpr bool) {
	if asExpr {
		g.emitArmBody(body)
		g.body.nl()
		return
	}
	// Statement mode: run the body for side-effects and fall through.
	if b, ok := body.(*ast.Block); ok {
		for _, s := range b.Stmts {
			g.emitStmt(s)
		}
		return
	}
	g.emitExpr(body)
	g.body.nl()
}

// emitArmBody writes `[stmts;] return tailExpr` for the arm body.
// A Block body has its leading statements emitted inline and its final
// expression lifted into the return; a bare expression is returned directly.
// Callers must not pre-emit `return ` — this function owns the full return
// statement so multi-stmt blocks don't produce a bare `return` before the
// bindings (see Bug 2 in bootstrap/gen).
func (g *gen) emitArmBody(body ast.Expr) {
	if b, ok := body.(*ast.Block); ok && len(b.Stmts) > 0 {
		last := b.Stmts[len(b.Stmts)-1]
		if es, ok := last.(*ast.ExprStmt); ok {
			for _, s := range b.Stmts[:len(b.Stmts)-1] {
				g.emitStmt(s)
			}
			g.body.write("return ")
			g.emitExpr(es.X)
			return
		}
	}
	g.body.write("return ")
	g.emitExpr(body)
}

// isCatchAll reports whether p matches every value without condition.
//
// An IdentPat whose name happens to be a known enum variant is NOT a
// catch-all — the resolver would have treated it as a variant reference.
// We detect that case here via the variantOwner map so the match lowers
// correctly even when the parser reported an IdentPat.
func (g *gen) isCatchAll(p ast.Pattern) bool {
	switch p := p.(type) {
	case *ast.WildcardPat:
		return true
	case *ast.IdentPat:
		if _, isVar := g.variantOwner[p.Name]; isVar {
			return false
		}
		// Prelude variants (None, Some) also aren't catch-alls.
		switch p.Name {
		case "None", "Some", "Ok", "Err":
			return false
		}
		return true
	}
	return false
}

// isCatchAll is kept as a package function so external callers
// (if any) keep working; it ignores variant-name shadowing.
func isCatchAll(p ast.Pattern) bool {
	switch p.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		return true
	}
	return false
}

// emitPatternTest writes a boolean expression that is true iff the
// scrutinee matches the pattern.
//
// Bindings (IdentPat, BindingPat) don't contribute to the test — they
// always succeed and only matter for emitPatternBindings.
func (g *gen) emitPatternTest(scrut string, scrutType types.Type, p ast.Pattern) {
	switch p := p.(type) {
	case *ast.WildcardPat:
		g.body.write("true")
	case *ast.IdentPat:
		// IdentPat may actually be a bare variant reference when its
		// name matches a declared (or prelude) enum variant. The
		// resolver treats it as a variant in that case; we mirror.
		if owner, ok := g.variantOwner[p.Name]; ok {
			g.body.writef("func() bool { _, ok := %s.(*%s_%s); return ok }()",
				scrut, owner, p.Name)
			return
		}
		switch p.Name {
		case "None":
			g.body.writef("%s == nil", scrut)
			return
		}
		g.body.write("true")
	case *ast.LiteralPat:
		g.body.writef("%s == ", scrut)
		g.emitExpr(p.Literal)
	case *ast.VariantPat:
		// `Some(...)` / `None` / `Shape.Circle(...)`. Option and Result
		// are special because they're not interface-backed.
		//
		// Option lowers to *T; the test is a nil check.
		// Result lowers to a struct with an IsOk tag; the test reads it.
		if len(p.Path) == 0 {
			g.body.write("false")
			return
		}
		vname := p.Path[len(p.Path)-1]
		if isOptionScrut(scrutType) || vname == "Some" || vname == "None" {
			// Avoid routing Ok/Err into the Option path even when the
			// scrutinee type was lost.
			if !isResultVariant(vname) {
				if vname == "Some" {
					g.body.writef("%s != nil", scrut)
				} else if vname == "None" {
					g.body.writef("%s == nil", scrut)
				} else {
					g.body.write("false")
				}
				return
			}
		}
		if isResultScrut(scrutType) || isResultVariant(vname) {
			switch vname {
			case "Ok":
				g.body.writef("%s.IsOk", scrut)
			case "Err":
				g.body.writef("!%s.IsOk", scrut)
			default:
				g.body.write("false")
			}
			return
		}
		owner := g.enumOwnerForPath(scrutType, p.Path)
		g.body.writef("func() bool { _, ok := %s.(*%s_%s); return ok }()",
			scrut, owner, vname)
	case *ast.RangePat:
		g.emitRangeTest(scrut, p)
	case *ast.OrPat:
		g.body.write("(")
		for i, alt := range p.Alts {
			if i > 0 {
				g.body.write(" || ")
			}
			g.emitPatternTest(scrut, scrutType, alt)
		}
		g.body.write(")")
	case *ast.BindingPat:
		// `name @ inner` — binding always succeeds, the test is inner.
		g.emitPatternTest(scrut, scrutType, p.Pattern)
	case *ast.TuplePat:
		g.emitTuplePatTest(scrut, scrutType, p)
	case *ast.StructPat:
		g.emitStructPatTest(scrut, scrutType, p)
	default:
		g.body.writef("true /* TODO: pattern %T */", p)
	}
}

// emitTuplePatTest writes `scrut.F0 matches p0 && scrut.F1 matches p1 && ...`
// for a tuple pattern. Wildcard / bare-ident sub-patterns always succeed
// so they're elided from the conjunction, leaving `true` in the
// degenerate case (pure binding tuple).
func (g *gen) emitTuplePatTest(scrut string, scrutType types.Type, p *ast.TuplePat) {
	var elemTypes []types.Type
	if tup, ok := scrutType.(*types.Tuple); ok {
		elemTypes = tup.Elems
	}
	g.body.write("(")
	wrote := false
	for i, elem := range p.Elems {
		if isCatchAll(elem) {
			continue
		}
		if wrote {
			g.body.write(" && ")
		}
		wrote = true
		field := scrut + ".F" + itoa(i)
		var et types.Type
		if i < len(elemTypes) {
			et = elemTypes[i]
		}
		g.emitPatternTest(field, et, elem)
	}
	if !wrote {
		g.body.write("true")
	}
	g.body.write(")")
}

// emitStructPatTest writes the conjunction of per-field tests for a
// struct pattern. A rest (`..`) or wildcard sub-pattern is a success by
// construction and is omitted from the output.
func (g *gen) emitStructPatTest(scrut string, scrutType types.Type, p *ast.StructPat) {
	g.body.write("(")
	wrote := false
	for _, f := range p.Fields {
		sub := f.Pattern
		if sub == nil {
			// Shorthand `{ name }` — pure binding, always succeeds.
			continue
		}
		if isCatchAll(sub) {
			continue
		}
		if wrote {
			g.body.write(" && ")
		}
		wrote = true
		field := scrut + "." + mangleIdent(f.Name)
		g.emitPatternTest(field, nil, sub)
	}
	if !wrote {
		g.body.write("true")
	}
	g.body.write(")")
}

// emitRangeTest writes `scrut >= start && scrut <(=) stop` for a range
// pattern. Missing bounds become half-open ranges.
func (g *gen) emitRangeTest(scrut string, p *ast.RangePat) {
	first := true
	g.body.write("(")
	if p.Start != nil {
		g.body.writef("%s >= ", scrut)
		g.emitExpr(p.Start)
		first = false
	}
	if p.Stop != nil {
		if !first {
			g.body.write(" && ")
		}
		cmp := "<"
		if p.Inclusive {
			cmp = "<="
		}
		g.body.writef("%s %s ", scrut, cmp)
		g.emitExpr(p.Stop)
	}
	if first && p.Stop == nil {
		// Unbounded both sides — matches everything.
		g.body.write("true")
	}
	g.body.write(")")
}

// emitPatternBindings writes any variable bindings introduced by the
// pattern, given that the test already succeeded. Called AFTER the
// test in the generated if-body.
func (g *gen) emitPatternBindings(scrut string, scrutType types.Type, p ast.Pattern) {
	switch p := p.(type) {
	case *ast.WildcardPat:
		// nothing
	case *ast.IdentPat:
		// Variant-name IdentPat: no binding, just the tag was checked.
		if _, isVar := g.variantOwner[p.Name]; isVar {
			return
		}
		switch p.Name {
		case "None", "Some", "Ok", "Err":
			return
		}
		g.body.writef("%s := %s; _ = %s\n", mangleIdent(p.Name), scrut, mangleIdent(p.Name))
	case *ast.VariantPat:
		if len(p.Args) == 0 {
			return
		}
		vname := p.Path[len(p.Path)-1]
		// Option: `Some(n)` binds by dereferencing the pointer.
		if (isOptionScrut(scrutType) || vname == "Some") && vname == "Some" {
			if len(p.Args) == 1 {
				inner := p.Args[0]
				if id, ok := inner.(*ast.IdentPat); ok {
					g.body.writef("%s := *%s; _ = %s\n",
						mangleIdent(id.Name), scrut, mangleIdent(id.Name))
					return
				}
				if _, ok := inner.(*ast.WildcardPat); ok {
					return
				}
				// Nested patterns — bind via the dereferenced value.
				tmp := g.freshVar("_v")
				g.body.writef("%s := *%s; _ = %s\n", tmp, scrut, tmp)
				g.emitPatternBindings(tmp, nil, inner)
				return
			}
		}
		// Result: `Ok(v)` / `Err(e)` bind from the struct fields.
		if isResultScrut(scrutType) || isResultVariant(vname) {
			if len(p.Args) == 1 {
				inner := p.Args[0]
				field := "Value"
				if vname == "Err" {
					field = "Error"
				}
				if id, ok := inner.(*ast.IdentPat); ok {
					g.body.writef("%s := %s.%s; _ = %s\n",
						mangleIdent(id.Name), scrut, field, mangleIdent(id.Name))
					return
				}
				if _, ok := inner.(*ast.WildcardPat); ok {
					return
				}
				tmp := g.freshVar("_v")
				g.body.writef("%s := %s.%s; _ = %s\n", tmp, scrut, field, tmp)
				g.emitPatternBindings(tmp, nil, inner)
				return
			}
		}
		owner := g.enumOwnerForPath(scrutType, p.Path)
		// Reconstruct via type assertion + per-arg Fi access.
		tmp := g.freshVar("_v")
		g.body.writef("%s := %s.(*%s_%s); _ = %s\n", tmp, scrut, owner, vname, tmp)
		for i, a := range p.Args {
			if _, ok := a.(*ast.WildcardPat); ok {
				continue
			}
			if id, ok := a.(*ast.IdentPat); ok {
				g.body.writef("%s := %s.F%d; _ = %s\n",
					mangleIdent(id.Name), tmp, i, mangleIdent(id.Name))
				continue
			}
			// Nested patterns — recurse.
			nested := g.freshVar("_f")
			g.body.writef("%s := %s.F%d; _ = %s\n", nested, tmp, i, nested)
			g.emitPatternBindings(nested, nil, a)
		}
	case *ast.BindingPat:
		g.body.writef("%s := %s; _ = %s\n",
			mangleIdent(p.Name), scrut, mangleIdent(p.Name))
		g.emitPatternBindings(scrut, scrutType, p.Pattern)
	case *ast.OrPat:
		// Bindings must be consistent across alts by spec; lower by
		// re-binding from the first alt.
		if len(p.Alts) > 0 {
			g.emitPatternBindings(scrut, scrutType, p.Alts[0])
		}
	case *ast.TuplePat:
		// Recurse into each element: `scrut.Fi` is the sub-scrutinee.
		var elemTypes []types.Type
		if tup, ok := scrutType.(*types.Tuple); ok {
			elemTypes = tup.Elems
		}
		for i, elem := range p.Elems {
			if _, ok := elem.(*ast.WildcardPat); ok {
				continue
			}
			field := g.freshVar("_tf")
			g.body.writef("%s := %s.F%d; _ = %s\n", field, scrut, i, field)
			var et types.Type
			if i < len(elemTypes) {
				et = elemTypes[i]
			}
			g.emitPatternBindings(field, et, elem)
		}
	case *ast.StructPat:
		for _, f := range p.Fields {
			if f.Pattern == nil {
				// Shorthand `{ name }` — bind the field directly.
				g.body.writef("%s := %s.%s; _ = %s\n",
					mangleIdent(f.Name), scrut, mangleIdent(f.Name), mangleIdent(f.Name))
				continue
			}
			if _, ok := f.Pattern.(*ast.WildcardPat); ok {
				continue
			}
			if id, ok := f.Pattern.(*ast.IdentPat); ok {
				g.body.writef("%s := %s.%s; _ = %s\n",
					mangleIdent(id.Name), scrut, mangleIdent(f.Name), mangleIdent(id.Name))
				continue
			}
			// Nested pattern: recurse on the field projection.
			sub := g.freshVar("_sf")
			g.body.writef("%s := %s.%s; _ = %s\n",
				sub, scrut, mangleIdent(f.Name), sub)
			g.emitPatternBindings(sub, nil, f.Pattern)
		}
	case *ast.LiteralPat, *ast.RangePat:
		// Literal / range patterns introduce no bindings.
	}
}

// isOptionScrut reports whether the match scrutinee's semantic type is
// `T?` / `Option<T>` — Optional lowers to *T in Go, which takes the
// nil-check path rather than the interface-type-assertion path.
func isOptionScrut(t types.Type) bool {
	if t == nil {
		return false
	}
	if _, ok := t.(*types.Optional); ok {
		return true
	}
	if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Option" {
		return true
	}
	return false
}

// isResultScrut reports whether the match scrutinee's semantic type is
// `Result<T, E>`. Result lowers to a Go struct (Value/Error/IsOk), so
// pattern tests and bindings go through struct field access rather than
// type assertions. When the checker lost the type we still route
// Ok/Err patterns through here — the bare variant names are unambiguous
// in prelude terms.
func isResultScrut(t types.Type) bool {
	if t == nil {
		return false
	}
	if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Result" {
		return true
	}
	return false
}

// isResultVariant reports whether `name` is one of Result's prelude
// variants. Used to route patterns when the scrutinee type wasn't
// inferred (e.g., inside a nested match whose scrutinee is itself a
// pattern binding whose type we lost).
func isResultVariant(name string) bool {
	return name == "Ok" || name == "Err"
}

// enumOwnerForPath returns the owning enum name for a variant pattern,
// preferring the scrutinee's type when available and falling back to
// the file-local variant owner index.
func (g *gen) enumOwnerForPath(scrutType types.Type, path []string) string {
	if len(path) >= 2 {
		// Explicit `EnumName.Variant` form.
		return path[len(path)-2]
	}
	name := path[len(path)-1]
	if owner, ok := g.variantOwner[name]; ok {
		return owner
	}
	// Scrutinee type fallback (e.g., Option<T>).
	if n, ok := scrutType.(*types.Named); ok && n.Sym != nil {
		return n.Sym.Name
	}
	// Prelude Option variants default to "Option".
	switch name {
	case "Some", "None":
		return "Option"
	case "Ok", "Err":
		return "Result"
	}
	return "???"
}

// freshVar returns a unique local name derived from prefix.
func (g *gen) freshVar(prefix string) string {
	g.freshCounter++
	return fmt.Sprintf("%s%d", prefix, g.freshCounter)
}
