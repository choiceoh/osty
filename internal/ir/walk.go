package ir

// Visitor is the callback interface for Walk. Visit is invoked for
// every Node encountered during traversal; the returned Visitor is
// used for the node's children (nil skips descent). This mirrors
// Go's ast.Walk but over ir.Node.
type Visitor interface {
	Visit(n Node) Visitor
}

// VisitorFunc adapts a plain function to the Visitor interface. The
// returned sub-visitor is the same function, so callers that don't
// need per-subtree state can pass a single closure.
type VisitorFunc func(n Node) bool

// Visit satisfies Visitor. Returning false stops descent into n's
// children.
func (f VisitorFunc) Visit(n Node) Visitor {
	if f(n) {
		return f
	}
	return nil
}

// Walk traverses an IR subtree rooted at n, invoking v.Visit on each
// node in pre-order. After visiting a node, Walk descends into its
// children using the Visitor returned by Visit. A nil sub-visitor
// prunes the subtree.
//
// Walk is defensively nil-safe: walking a nil Node is a no-op.
func Walk(v Visitor, n Node) {
	if v == nil || n == nil {
		return
	}
	w := v.Visit(n)
	if w == nil {
		return
	}
	walkChildren(w, n)
}

// walkChildren dispatches on the concrete node type and walks every
// child node (decls, stmts, exprs, patterns) in source order.
func walkChildren(v Visitor, n Node) {
	switch n := n.(type) {

	// ---- Module ----
	case *Module:
		for _, d := range n.Decls {
			Walk(v, d)
		}
		for _, s := range n.Script {
			Walk(v, s)
		}

	// ---- Decls ----
	case *FnDecl:
		for _, g := range n.Generics {
			Walk(v, g)
		}
		for _, p := range n.Params {
			Walk(v, p)
		}
		if n.Body != nil {
			Walk(v, n.Body)
		}
	case *StructDecl:
		for _, g := range n.Generics {
			Walk(v, g)
		}
		for _, f := range n.Fields {
			Walk(v, f)
		}
		for _, m := range n.Methods {
			Walk(v, m)
		}
	case *EnumDecl:
		for _, g := range n.Generics {
			Walk(v, g)
		}
		for _, vr := range n.Variants {
			Walk(v, vr)
		}
		for _, m := range n.Methods {
			Walk(v, m)
		}
	case *LetDecl:
		if n.Value != nil {
			Walk(v, n.Value)
		}
	case *InterfaceDecl:
		for _, g := range n.Generics {
			Walk(v, g)
		}
		for _, m := range n.Methods {
			Walk(v, m)
		}
	case *TypeAliasDecl:
		for _, g := range n.Generics {
			Walk(v, g)
		}
	case *UseDecl:
		for _, d := range n.GoBody {
			Walk(v, d)
		}

	// ---- Decl sub-nodes ----
	case *Param:
		if n.Default != nil {
			Walk(v, n.Default)
		}
	case *Field:
		if n.Default != nil {
			Walk(v, n.Default)
		}
	case *TypeParam:
		// bounds are types, not nodes; nothing to walk
	case *Variant:
		// payload slots are types, not nodes; nothing to walk

	// ---- Stmts ----
	case *Block:
		for _, s := range n.Stmts {
			Walk(v, s)
		}
		if n.Result != nil {
			Walk(v, n.Result)
		}
	case *LetStmt:
		if n.Pattern != nil {
			Walk(v, n.Pattern)
		}
		if n.Value != nil {
			Walk(v, n.Value)
		}
	case *ExprStmt:
		Walk(v, n.X)
	case *AssignStmt:
		for _, t := range n.Targets {
			Walk(v, t)
		}
		Walk(v, n.Value)
	case *ReturnStmt:
		if n.Value != nil {
			Walk(v, n.Value)
		}
	case *BreakStmt, *ContinueStmt:
		// leaves
	case *IfStmt:
		Walk(v, n.Cond)
		if n.Then != nil {
			Walk(v, n.Then)
		}
		if n.Else != nil {
			Walk(v, n.Else)
		}
	case *ForStmt:
		if n.Cond != nil {
			Walk(v, n.Cond)
		}
		if n.Iter != nil {
			Walk(v, n.Iter)
		}
		if n.Start != nil {
			Walk(v, n.Start)
		}
		if n.End != nil {
			Walk(v, n.End)
		}
		if n.Body != nil {
			Walk(v, n.Body)
		}
	case *DeferStmt:
		if n.Body != nil {
			Walk(v, n.Body)
		}
	case *ChanSendStmt:
		Walk(v, n.Channel)
		Walk(v, n.Value)
	case *MatchStmt:
		Walk(v, n.Scrutinee)
		for _, a := range n.Arms {
			Walk(v, a)
		}
	case *ErrorStmt:
		// leaf

	// ---- Exprs ----
	case *IntLit, *FloatLit, *BoolLit, *CharLit, *ByteLit, *UnitLit:
		// leaves
	case *StringLit:
		for _, p := range n.Parts {
			if !p.IsLit && p.Expr != nil {
				Walk(v, p.Expr)
			}
		}
	case *Ident:
		// leaf
	case *UnaryExpr:
		Walk(v, n.X)
	case *BinaryExpr:
		Walk(v, n.Left)
		Walk(v, n.Right)
	case *CallExpr:
		Walk(v, n.Callee)
		for _, a := range n.Args {
			Walk(v, a)
		}
	case *IntrinsicCall:
		for _, a := range n.Args {
			Walk(v, a)
		}
	case *MethodCall:
		Walk(v, n.Receiver)
		for _, a := range n.Args {
			Walk(v, a)
		}
	case *ListLit:
		for _, e := range n.Elems {
			Walk(v, e)
		}
	case *MapLit:
		for _, en := range n.Entries {
			Walk(v, en.Key)
			Walk(v, en.Value)
		}
	case *TupleLit:
		for _, e := range n.Elems {
			Walk(v, e)
		}
	case *StructLit:
		for _, f := range n.Fields {
			if f.Value != nil {
				Walk(v, f.Value)
			}
		}
		if n.Spread != nil {
			Walk(v, n.Spread)
		}
	case *RangeLit:
		if n.Start != nil {
			Walk(v, n.Start)
		}
		if n.End != nil {
			Walk(v, n.End)
		}
	case *QuestionExpr:
		Walk(v, n.X)
	case *CoalesceExpr:
		Walk(v, n.Left)
		Walk(v, n.Right)
	case *FieldExpr:
		Walk(v, n.X)
	case *TupleAccess:
		Walk(v, n.X)
	case *IndexExpr:
		Walk(v, n.X)
		Walk(v, n.Index)
	case *Closure:
		for _, p := range n.Params {
			Walk(v, p)
		}
		if n.Body != nil {
			Walk(v, n.Body)
		}
	case *VariantLit:
		for _, a := range n.Args {
			Walk(v, a)
		}
	case *BlockExpr:
		if n.Block != nil {
			Walk(v, n.Block)
		}
	case *IfExpr:
		Walk(v, n.Cond)
		if n.Then != nil {
			Walk(v, n.Then)
		}
		if n.Else != nil {
			Walk(v, n.Else)
		}
	case *IfLetExpr:
		Walk(v, n.Pattern)
		Walk(v, n.Scrutinee)
		if n.Then != nil {
			Walk(v, n.Then)
		}
		if n.Else != nil {
			Walk(v, n.Else)
		}
	case *MatchExpr:
		Walk(v, n.Scrutinee)
		for _, a := range n.Arms {
			Walk(v, a)
		}
	case *MatchArm:
		Walk(v, n.Pattern)
		if n.Guard != nil {
			Walk(v, n.Guard)
		}
		if n.Body != nil {
			Walk(v, n.Body)
		}
	case *ErrorExpr:
		// leaf

	// ---- Patterns ----
	case *WildPat, *IdentPat, *ErrorPat:
		// leaves
	case *LitPat:
		if n.Value != nil {
			Walk(v, n.Value)
		}
	case *TuplePat:
		for _, e := range n.Elems {
			Walk(v, e)
		}
	case *StructPat:
		for _, f := range n.Fields {
			if f.Pattern != nil {
				Walk(v, f.Pattern)
			}
		}
	case *VariantPat:
		for _, a := range n.Args {
			Walk(v, a)
		}
	case *RangePat:
		if n.Low != nil {
			Walk(v, n.Low)
		}
		if n.High != nil {
			Walk(v, n.High)
		}
	case *OrPat:
		for _, a := range n.Alts {
			Walk(v, a)
		}
	case *BindingPat:
		if n.Pattern != nil {
			Walk(v, n.Pattern)
		}
	}
}

// Inspect is a shorthand for Walk(VisitorFunc(fn), n) — the most
// common form in client code.
func Inspect(n Node, fn func(Node) bool) {
	Walk(VisitorFunc(fn), n)
}
