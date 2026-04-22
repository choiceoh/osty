package ir

// SubstEnv maps a generic type parameter name to its concrete
// replacement Type. Backed by a plain map so callers can build it in
// whatever order they need.
type SubstEnv map[string]Type

// SubstituteTypes walks an IR subtree and replaces every TypeVar whose
// Name is present in env with a fresh clone of env[Name]. Mutation is
// in place — callers that need to preserve the input tree should pass a
// cloned copy (see Clone).
//
// A nil env is a no-op; a nil subtree is a no-op. The function does not
// cross into sibling decls through symbol references; substitution stops
// at the provided root.
func SubstituteTypes(n Node, env SubstEnv) {
	if n == nil || len(env) == 0 {
		return
	}
	subst(n, env)
}

// substType replaces TypeVars in-place within t and returns the
// potentially-replaced type. NamedType etc. are mutated in place so
// structural identity is preserved for nested Args.
func substType(t Type, env SubstEnv) Type {
	if t == nil || len(env) == 0 {
		return t
	}
	switch t := t.(type) {
	case *TypeVar:
		if rep, ok := env[t.Name]; ok {
			return CloneType(rep)
		}
		return t
	case *NamedType:
		for i, a := range t.Args {
			t.Args[i] = substType(a, env)
		}
		return t
	case *OptionalType:
		t.Inner = substType(t.Inner, env)
		return t
	case *TupleType:
		for i, e := range t.Elems {
			t.Elems[i] = substType(e, env)
		}
		return t
	case *FnType:
		for i, p := range t.Params {
			t.Params[i] = substType(p, env)
		}
		t.Return = substType(t.Return, env)
		return t
	case *PrimType, *ErrType:
		return t
	}
	return t
}

// substTypes replaces TypeVars in-place in a slice.
func substTypes(ts []Type, env SubstEnv) {
	for i, t := range ts {
		ts[i] = substType(t, env)
	}
}

// subst dispatches by node kind and substitutes both embedded Type
// fields and recursively walks child nodes.
func subst(n Node, env SubstEnv) {
	switch n := n.(type) {

	// ---- Module ----
	case *Module:
		for _, d := range n.Decls {
			subst(d, env)
		}
		for _, s := range n.Script {
			subst(s, env)
		}

	// ---- Decls ----
	case *FnDecl:
		n.Return = substType(n.Return, env)
		for _, p := range n.Params {
			subst(p, env)
		}
		if n.Body != nil {
			subst(n.Body, env)
		}
	case *StructDecl:
		for _, f := range n.Fields {
			subst(f, env)
		}
		for _, m := range n.Methods {
			subst(m, env)
		}
	case *EnumDecl:
		for _, v := range n.Variants {
			substTypes(v.Payload, env)
		}
		for _, m := range n.Methods {
			subst(m, env)
		}
	case *LetDecl:
		n.Type = substType(n.Type, env)
		if n.Value != nil {
			subst(n.Value, env)
		}
	case *InterfaceDecl:
		substTypes(n.Extends, env)
		for _, m := range n.Methods {
			subst(m, env)
		}
	case *TypeAliasDecl:
		n.Target = substType(n.Target, env)
	case *UseDecl:
		for _, d := range n.GoBody {
			subst(d, env)
		}

	// ---- Decl sub-nodes ----
	case *Param:
		n.Type = substType(n.Type, env)
		if n.Default != nil {
			subst(n.Default, env)
		}
	case *Field:
		n.Type = substType(n.Type, env)
		if n.Default != nil {
			subst(n.Default, env)
		}

	// ---- Stmts ----
	case *Block:
		for _, s := range n.Stmts {
			subst(s, env)
		}
		if n.Result != nil {
			subst(n.Result, env)
		}
	case *LetStmt:
		n.Type = substType(n.Type, env)
		if n.Value != nil {
			subst(n.Value, env)
		}
	case *ExprStmt:
		if n.X != nil {
			subst(n.X, env)
		}
	case *AssignStmt:
		for _, t := range n.Targets {
			subst(t, env)
		}
		if n.Value != nil {
			subst(n.Value, env)
		}
	case *ReturnStmt:
		if n.Value != nil {
			subst(n.Value, env)
		}
	case *BreakStmt, *ContinueStmt:
		// leaves
	case *IfStmt:
		if n.Cond != nil {
			subst(n.Cond, env)
		}
		if n.Then != nil {
			subst(n.Then, env)
		}
		if n.Else != nil {
			subst(n.Else, env)
		}
	case *ForStmt:
		if n.Cond != nil {
			subst(n.Cond, env)
		}
		if n.Iter != nil {
			subst(n.Iter, env)
		}
		if n.Start != nil {
			subst(n.Start, env)
		}
		if n.End != nil {
			subst(n.End, env)
		}
		if n.Body != nil {
			subst(n.Body, env)
		}
	case *DeferStmt:
		if n.Body != nil {
			subst(n.Body, env)
		}
	case *ChanSendStmt:
		subst(n.Channel, env)
		subst(n.Value, env)
	case *MatchStmt:
		subst(n.Scrutinee, env)
		for _, a := range n.Arms {
			substMatchArm(a, env)
		}
	case *ErrorStmt:
		// leaf

	// ---- Exprs with Type field (via Type() accessor) ----
	case *IntLit:
		n.T = substType(n.T, env)
	case *FloatLit:
		n.T = substType(n.T, env)
	case *BoolLit, *CharLit, *ByteLit, *UnitLit:
		// Primitive-only; no TypeVar possible.
	case *StringLit:
		for _, p := range n.Parts {
			if !p.IsLit && p.Expr != nil {
				subst(p.Expr, env)
			}
		}
	case *Ident:
		n.T = substType(n.T, env)
		substTypes(n.TypeArgs, env)
	case *UnaryExpr:
		n.T = substType(n.T, env)
		if n.X != nil {
			subst(n.X, env)
		}
	case *BinaryExpr:
		n.T = substType(n.T, env)
		if n.Left != nil {
			subst(n.Left, env)
		}
		if n.Right != nil {
			subst(n.Right, env)
		}
	case *CallExpr:
		n.T = substType(n.T, env)
		substTypes(n.TypeArgs, env)
		if n.Callee != nil {
			subst(n.Callee, env)
		}
		for i := range n.Args {
			if n.Args[i].Value != nil {
				subst(n.Args[i].Value, env)
			}
		}
	case *IntrinsicCall:
		for i := range n.Args {
			if n.Args[i].Value != nil {
				subst(n.Args[i].Value, env)
			}
		}
	case *MethodCall:
		n.T = substType(n.T, env)
		substTypes(n.TypeArgs, env)
		if n.Receiver != nil {
			subst(n.Receiver, env)
		}
		for i := range n.Args {
			if n.Args[i].Value != nil {
				subst(n.Args[i].Value, env)
			}
		}
	case *ListLit:
		n.Elem = substType(n.Elem, env)
		for _, e := range n.Elems {
			subst(e, env)
		}
	case *MapLit:
		n.KeyT = substType(n.KeyT, env)
		n.ValT = substType(n.ValT, env)
		for i := range n.Entries {
			if n.Entries[i].Key != nil {
				subst(n.Entries[i].Key, env)
			}
			if n.Entries[i].Value != nil {
				subst(n.Entries[i].Value, env)
			}
		}
	case *TupleLit:
		n.T = substType(n.T, env)
		for _, e := range n.Elems {
			subst(e, env)
		}
	case *StructLit:
		n.T = substType(n.T, env)
		for i := range n.Fields {
			if n.Fields[i].Value != nil {
				subst(n.Fields[i].Value, env)
			}
		}
		if n.Spread != nil {
			subst(n.Spread, env)
		}
	case *VariantLit:
		n.T = substType(n.T, env)
		for i := range n.Args {
			if n.Args[i].Value != nil {
				subst(n.Args[i].Value, env)
			}
		}
	case *BlockExpr:
		n.T = substType(n.T, env)
		if n.Block != nil {
			subst(n.Block, env)
		}
	case *IfExpr:
		n.T = substType(n.T, env)
		if n.Cond != nil {
			subst(n.Cond, env)
		}
		if n.Then != nil {
			subst(n.Then, env)
		}
		if n.Else != nil {
			subst(n.Else, env)
		}
	case *IfLetExpr:
		n.T = substType(n.T, env)
		if n.Scrutinee != nil {
			subst(n.Scrutinee, env)
		}
		if n.Then != nil {
			subst(n.Then, env)
		}
		if n.Else != nil {
			subst(n.Else, env)
		}
	case *MatchExpr:
		n.T = substType(n.T, env)
		if n.Scrutinee != nil {
			subst(n.Scrutinee, env)
		}
		for _, a := range n.Arms {
			substMatchArm(a, env)
		}
	case *FieldExpr:
		n.T = substType(n.T, env)
		if n.X != nil {
			subst(n.X, env)
		}
	case *IndexExpr:
		n.T = substType(n.T, env)
		if n.X != nil {
			subst(n.X, env)
		}
		if n.Index != nil {
			subst(n.Index, env)
		}
	case *TupleAccess:
		n.T = substType(n.T, env)
		if n.X != nil {
			subst(n.X, env)
		}
	case *RangeLit:
		n.T = substType(n.T, env)
		if n.Start != nil {
			subst(n.Start, env)
		}
		if n.End != nil {
			subst(n.End, env)
		}
	case *QuestionExpr:
		n.T = substType(n.T, env)
		if n.X != nil {
			subst(n.X, env)
		}
	case *CoalesceExpr:
		n.T = substType(n.T, env)
		if n.Left != nil {
			subst(n.Left, env)
		}
		if n.Right != nil {
			subst(n.Right, env)
		}
	case *Closure:
		n.Return = substType(n.Return, env)
		n.T = substType(n.T, env)
		for _, p := range n.Params {
			subst(p, env)
		}
		// Closure params whose Type was already nil before substitution
		// (lowerClosure backfill missed them — e.g., when a generic
		// fn is monomorphized and its inner closure's inferred FnType
		// uses TypeVars that are tracked only on Closure.T, not on
		// each Param.Type) would leave validator tripping on
		// "Closure: param[i] nil Type". Re-run the same backfill that
		// lowerClosure does, now that Closure.T has been substituted
		// — so the FnType's substituted param slots are the source of
		// truth.
		if fnT, ok := n.T.(*FnType); ok && fnT != nil {
			for i, p := range n.Params {
				if p == nil || p.Type != nil || i >= len(fnT.Params) {
					continue
				}
				p.Type = fnT.Params[i]
			}
		}
		for _, c := range n.Captures {
			c.T = substType(c.T, env)
		}
		if n.Body != nil {
			subst(n.Body, env)
		}
	case *ErrorExpr:
		n.T = substType(n.T, env)
	}
}

func substMatchArm(a *MatchArm, env SubstEnv) {
	if a == nil {
		return
	}
	if a.Guard != nil {
		subst(a.Guard, env)
	}
	if a.Body != nil {
		subst(a.Body, env)
	}
}
