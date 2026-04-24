package ir

// Clone returns a deep copy of an IR subtree. Primitive type singletons
// (TInt, TString, etc. defined in ir.go) are shared — they're immutable
// and copying them would break pointer equality the rest of the package
// relies on. Every other node, type, and container is freshly allocated.
//
// Clone exists primarily to support monomorphization: a generic fn body
// is cloned once per concrete type tuple before type parameters are
// substituted in place. Clients that need a plain traversal should reach
// for Walk / Inspect instead.
//
// Clone is defensively nil-safe: Clone(nil) returns nil.
func Clone(n Node) Node {
	if n == nil {
		return nil
	}
	return cloneNode(n)
}

// CloneType deep-copies a Type. Primitive singletons are returned as-is.
// Returns nil when the input is nil.
func CloneType(t Type) Type {
	if t == nil {
		return nil
	}
	switch t := t.(type) {
	case *PrimType:
		// Primitive types are shared singletons (TInt, TString, …).
		return t
	case *NamedType:
		out := &NamedType{
			Package: t.Package,
			Name:    t.Name,
			Builtin: t.Builtin,
		}
		if len(t.Args) > 0 {
			out.Args = make([]Type, len(t.Args))
			for i, a := range t.Args {
				out.Args[i] = CloneType(a)
			}
		}
		return out
	case *OptionalType:
		return &OptionalType{Inner: CloneType(t.Inner)}
	case *TupleType:
		out := &TupleType{}
		if len(t.Elems) > 0 {
			out.Elems = make([]Type, len(t.Elems))
			for i, e := range t.Elems {
				out.Elems[i] = CloneType(e)
			}
		}
		return out
	case *FnType:
		out := &FnType{Return: CloneType(t.Return)}
		if len(t.Params) > 0 {
			out.Params = make([]Type, len(t.Params))
			for i, p := range t.Params {
				out.Params[i] = CloneType(p)
			}
		}
		return out
	case *TypeVar:
		return &TypeVar{Name: t.Name, Owner: t.Owner}
	case *ErrType:
		// Shared singleton, same rationale as PrimType.
		return t
	}
	return t
}

// cloneTypes clones each element of a slice.
func cloneTypes(ts []Type) []Type {
	if len(ts) == 0 {
		return nil
	}
	out := make([]Type, len(ts))
	for i, t := range ts {
		out[i] = CloneType(t)
	}
	return out
}

// cloneNode dispatches on concrete node kind. Every case returns a fresh
// pointer; callers receive a disjoint subtree.
func cloneNode(n Node) Node {
	switch n := n.(type) {

	// ---- Module ----
	case *Module:
		return cloneModule(n)

	// ---- Decls ----
	case *FnDecl:
		return cloneFnDecl(n)
	case *StructDecl:
		return cloneStructDecl(n)
	case *EnumDecl:
		return cloneEnumDecl(n)
	case *LetDecl:
		return cloneLetDecl(n)
	case *InterfaceDecl:
		return cloneInterfaceDecl(n)
	case *TypeAliasDecl:
		return cloneTypeAliasDecl(n)
	case *UseDecl:
		return cloneUseDecl(n)

	// ---- Decl sub-nodes ----
	case *Param:
		return cloneParam(n)
	case *Field:
		return cloneField(n)
	case *TypeParam:
		return cloneTypeParam(n)
	case *Variant:
		return cloneVariant(n)

	// ---- Stmts ----
	case *Block:
		return cloneBlock(n)
	case *LetStmt:
		return cloneLetStmt(n)
	case *ExprStmt:
		return &ExprStmt{X: cloneExpr(n.X), SpanV: n.SpanV}
	case *AssignStmt:
		return cloneAssignStmt(n)
	case *ReturnStmt:
		return &ReturnStmt{Value: cloneExprOrNil(n.Value), SpanV: n.SpanV}
	case *BreakStmt:
		return &BreakStmt{Label: n.Label, SpanV: n.SpanV}
	case *ContinueStmt:
		return &ContinueStmt{Label: n.Label, SpanV: n.SpanV}
	case *IfStmt:
		return &IfStmt{
			Cond:  cloneExpr(n.Cond),
			Then:  cloneBlockOrNil(n.Then),
			Else:  cloneBlockOrNil(n.Else),
			SpanV: n.SpanV,
		}
	case *ForStmt:
		return cloneForStmt(n)
	case *DeferStmt:
		return &DeferStmt{Body: cloneBlockOrNil(n.Body), SpanV: n.SpanV}
	case *ChanSendStmt:
		return &ChanSendStmt{
			Channel: cloneExpr(n.Channel),
			Value:   cloneExpr(n.Value),
			SpanV:   n.SpanV,
		}
	case *MatchStmt:
		return cloneMatchStmt(n)
	case *ErrorStmt:
		return &ErrorStmt{Note: n.Note, SpanV: n.SpanV}
	}

	// Expressions and patterns dispatch through their own helpers.
	if e, ok := n.(Expr); ok {
		return cloneExpr(e)
	}
	if p, ok := n.(Pattern); ok {
		return clonePattern(p)
	}
	return n
}

// ==== Module, decls ====

func cloneModule(m *Module) *Module {
	if m == nil {
		return nil
	}
	out := &Module{Package: m.Package, SpanV: m.SpanV}
	if len(m.Decls) > 0 {
		out.Decls = make([]Decl, 0, len(m.Decls))
		for _, d := range m.Decls {
			out.Decls = append(out.Decls, cloneDecl(d))
		}
	}
	if len(m.Script) > 0 {
		out.Script = make([]Stmt, 0, len(m.Script))
		for _, s := range m.Script {
			out.Script = append(out.Script, cloneStmt(s))
		}
	}
	return out
}

func cloneDecl(d Decl) Decl {
	if d == nil {
		return nil
	}
	switch d := d.(type) {
	case *FnDecl:
		return cloneFnDecl(d)
	case *StructDecl:
		return cloneStructDecl(d)
	case *EnumDecl:
		return cloneEnumDecl(d)
	case *LetDecl:
		return cloneLetDecl(d)
	case *InterfaceDecl:
		return cloneInterfaceDecl(d)
	case *TypeAliasDecl:
		return cloneTypeAliasDecl(d)
	case *UseDecl:
		return cloneUseDecl(d)
	}
	return d
}

func cloneFnDecl(fn *FnDecl) *FnDecl {
	if fn == nil {
		return nil
	}
	out := &FnDecl{
		Name:               fn.Name,
		Return:             CloneType(fn.Return),
		Body:               cloneBlockOrNil(fn.Body),
		ReceiverMut:        fn.ReceiverMut,
		Exported:           fn.Exported,
		SpanV:              fn.SpanV,
		ExportSymbol:       fn.ExportSymbol,
		CABI:               fn.CABI,
		IsIntrinsic:        fn.IsIntrinsic,
		NoAlloc:            fn.NoAlloc,
		Vectorize:          fn.Vectorize,
		NoVectorize:        fn.NoVectorize,
		VectorizeWidth:     fn.VectorizeWidth,
		VectorizeScalable:  fn.VectorizeScalable,
		VectorizePredicate: fn.VectorizePredicate,
		Parallel:           fn.Parallel,
		Unroll:             fn.Unroll,
		UnrollCount:        fn.UnrollCount,
		InlineMode:         fn.InlineMode,
		Hot:                fn.Hot,
		Cold:               fn.Cold,
		TargetFeatures:     append([]string(nil), fn.TargetFeatures...),
		NoaliasAll:         fn.NoaliasAll,
		NoaliasParams:      append([]string(nil), fn.NoaliasParams...),
		Pure:               fn.Pure,
	}
	if len(fn.Params) > 0 {
		out.Params = make([]*Param, len(fn.Params))
		for i, p := range fn.Params {
			out.Params[i] = cloneParam(p)
		}
	}
	if len(fn.Generics) > 0 {
		out.Generics = make([]*TypeParam, len(fn.Generics))
		for i, g := range fn.Generics {
			out.Generics[i] = cloneTypeParam(g)
		}
	}
	return out
}

func cloneStructDecl(s *StructDecl) *StructDecl {
	out := &StructDecl{
		Name:             s.Name,
		Exported:         s.Exported,
		SpanV:            s.SpanV,
		Pod:              s.Pod,
		ReprC:            s.ReprC,
		BuilderDerivable: s.BuilderDerivable,
		BuiltinSource:    s.BuiltinSource,
	}
	if len(s.BuiltinSourceArgs) > 0 {
		out.BuiltinSourceArgs = make([]Type, len(s.BuiltinSourceArgs))
		for i, t := range s.BuiltinSourceArgs {
			out.BuiltinSourceArgs[i] = CloneType(t)
		}
	}
	if len(s.BuilderRequiredFields) > 0 {
		out.BuilderRequiredFields = append([]string(nil), s.BuilderRequiredFields...)
	}
	if len(s.Fields) > 0 {
		out.Fields = make([]*Field, len(s.Fields))
		for i, f := range s.Fields {
			out.Fields[i] = cloneField(f)
		}
	}
	if len(s.Methods) > 0 {
		out.Methods = make([]*FnDecl, len(s.Methods))
		for i, m := range s.Methods {
			out.Methods[i] = cloneFnDecl(m)
		}
	}
	if len(s.Generics) > 0 {
		out.Generics = make([]*TypeParam, len(s.Generics))
		for i, g := range s.Generics {
			out.Generics[i] = cloneTypeParam(g)
		}
	}
	return out
}

func cloneEnumDecl(e *EnumDecl) *EnumDecl {
	out := &EnumDecl{
		Name:          e.Name,
		Exported:      e.Exported,
		SpanV:         e.SpanV,
		BuiltinSource: e.BuiltinSource,
	}
	if len(e.BuiltinSourceArgs) > 0 {
		out.BuiltinSourceArgs = make([]Type, len(e.BuiltinSourceArgs))
		for i, t := range e.BuiltinSourceArgs {
			out.BuiltinSourceArgs[i] = CloneType(t)
		}
	}
	if len(e.Variants) > 0 {
		out.Variants = make([]*Variant, len(e.Variants))
		for i, v := range e.Variants {
			out.Variants[i] = cloneVariant(v)
		}
	}
	if len(e.Methods) > 0 {
		out.Methods = make([]*FnDecl, len(e.Methods))
		for i, m := range e.Methods {
			out.Methods[i] = cloneFnDecl(m)
		}
	}
	if len(e.Generics) > 0 {
		out.Generics = make([]*TypeParam, len(e.Generics))
		for i, g := range e.Generics {
			out.Generics[i] = cloneTypeParam(g)
		}
	}
	return out
}

func cloneLetDecl(l *LetDecl) *LetDecl {
	return &LetDecl{
		Name:     l.Name,
		Type:     CloneType(l.Type),
		Value:    cloneExprOrNil(l.Value),
		Mut:      l.Mut,
		Exported: l.Exported,
		SpanV:    l.SpanV,
	}
}

func cloneInterfaceDecl(i *InterfaceDecl) *InterfaceDecl {
	out := &InterfaceDecl{
		Name:     i.Name,
		Exported: i.Exported,
		SpanV:    i.SpanV,
	}
	if len(i.Methods) > 0 {
		out.Methods = make([]*FnDecl, len(i.Methods))
		for j, m := range i.Methods {
			out.Methods[j] = cloneFnDecl(m)
		}
	}
	if len(i.Extends) > 0 {
		out.Extends = cloneTypes(i.Extends)
	}
	if len(i.Generics) > 0 {
		out.Generics = make([]*TypeParam, len(i.Generics))
		for j, g := range i.Generics {
			out.Generics[j] = cloneTypeParam(g)
		}
	}
	return out
}

func cloneTypeAliasDecl(t *TypeAliasDecl) *TypeAliasDecl {
	out := &TypeAliasDecl{
		Name:     t.Name,
		Target:   CloneType(t.Target),
		Exported: t.Exported,
		SpanV:    t.SpanV,
	}
	if len(t.Generics) > 0 {
		out.Generics = make([]*TypeParam, len(t.Generics))
		for i, g := range t.Generics {
			out.Generics[i] = cloneTypeParam(g)
		}
	}
	return out
}

func cloneUseDecl(u *UseDecl) *UseDecl {
	out := &UseDecl{
		Path:         append([]string(nil), u.Path...),
		RawPath:      u.RawPath,
		Alias:        u.Alias,
		IsGoFFI:      u.IsGoFFI,
		IsRuntimeFFI: u.IsRuntimeFFI,
		GoPath:       u.GoPath,
		RuntimePath:  u.RuntimePath,
		SpanV:        u.SpanV,
	}
	if len(u.GoBody) > 0 {
		out.GoBody = make([]Decl, 0, len(u.GoBody))
		for _, d := range u.GoBody {
			out.GoBody = append(out.GoBody, cloneDecl(d))
		}
	}
	return out
}

func cloneParam(p *Param) *Param {
	if p == nil {
		return nil
	}
	return &Param{
		Name:    p.Name,
		Pattern: clonePattern(p.Pattern),
		Type:    CloneType(p.Type),
		Default: cloneExprOrNil(p.Default),
		SpanV:   p.SpanV,
	}
}

func cloneField(f *Field) *Field {
	return &Field{
		Name:         f.Name,
		Type:         CloneType(f.Type),
		Default:      cloneExprOrNil(f.Default),
		Exported:     f.Exported,
		SpanV:        f.SpanV,
		JSONKey:      f.JSONKey,
		JSONSkip:     f.JSONSkip,
		JSONOptional: f.JSONOptional,
	}
}

func cloneTypeParam(tp *TypeParam) *TypeParam {
	out := &TypeParam{Name: tp.Name, SpanV: tp.SpanV}
	if len(tp.Bounds) > 0 {
		out.Bounds = cloneTypes(tp.Bounds)
	}
	return out
}

func cloneVariant(v *Variant) *Variant {
	out := &Variant{
		Name:     v.Name,
		SpanV:    v.SpanV,
		JSONTag:  v.JSONTag,
		JSONSkip: v.JSONSkip,
	}
	if len(v.Payload) > 0 {
		out.Payload = cloneTypes(v.Payload)
	}
	return out
}

// ==== Statements ====

func cloneStmt(s Stmt) Stmt {
	if s == nil {
		return nil
	}
	switch s := s.(type) {
	case *Block:
		return cloneBlock(s)
	case *LetStmt:
		return cloneLetStmt(s)
	case *ExprStmt:
		return &ExprStmt{X: cloneExpr(s.X), SpanV: s.SpanV}
	case *AssignStmt:
		return cloneAssignStmt(s)
	case *ReturnStmt:
		return &ReturnStmt{Value: cloneExprOrNil(s.Value), SpanV: s.SpanV}
	case *BreakStmt:
		return &BreakStmt{Label: s.Label, SpanV: s.SpanV}
	case *ContinueStmt:
		return &ContinueStmt{Label: s.Label, SpanV: s.SpanV}
	case *IfStmt:
		return &IfStmt{
			Cond:  cloneExpr(s.Cond),
			Then:  cloneBlockOrNil(s.Then),
			Else:  cloneBlockOrNil(s.Else),
			SpanV: s.SpanV,
		}
	case *ForStmt:
		return cloneForStmt(s)
	case *DeferStmt:
		return &DeferStmt{Body: cloneBlockOrNil(s.Body), SpanV: s.SpanV}
	case *ChanSendStmt:
		return &ChanSendStmt{
			Channel: cloneExpr(s.Channel),
			Value:   cloneExpr(s.Value),
			SpanV:   s.SpanV,
		}
	case *MatchStmt:
		return cloneMatchStmt(s)
	case *ErrorStmt:
		return &ErrorStmt{Note: s.Note, SpanV: s.SpanV}
	}
	return s
}

func cloneBlock(b *Block) *Block {
	out := &Block{SpanV: b.SpanV}
	if len(b.Stmts) > 0 {
		out.Stmts = make([]Stmt, 0, len(b.Stmts))
		for _, s := range b.Stmts {
			out.Stmts = append(out.Stmts, cloneStmt(s))
		}
	}
	if b.Result != nil {
		out.Result = cloneExpr(b.Result)
	}
	return out
}

func cloneBlockOrNil(b *Block) *Block {
	if b == nil {
		return nil
	}
	return cloneBlock(b)
}

func cloneLetStmt(s *LetStmt) *LetStmt {
	return &LetStmt{
		Name:    s.Name,
		Pattern: clonePattern(s.Pattern),
		Type:    CloneType(s.Type),
		Value:   cloneExprOrNil(s.Value),
		Mut:     s.Mut,
		SpanV:   s.SpanV,
	}
}

func cloneAssignStmt(s *AssignStmt) *AssignStmt {
	out := &AssignStmt{
		Op:    s.Op,
		Value: cloneExpr(s.Value),
		SpanV: s.SpanV,
	}
	if len(s.Targets) > 0 {
		out.Targets = make([]Expr, len(s.Targets))
		for i, t := range s.Targets {
			out.Targets[i] = cloneExpr(t)
		}
	}
	return out
}

func cloneForStmt(s *ForStmt) *ForStmt {
	return &ForStmt{
		Kind:      s.Kind,
		Label:     s.Label,
		Var:       s.Var,
		Pattern:   clonePattern(s.Pattern),
		Cond:      cloneExprOrNil(s.Cond),
		Iter:      cloneExprOrNil(s.Iter),
		Start:     cloneExprOrNil(s.Start),
		End:       cloneExprOrNil(s.End),
		Inclusive: s.Inclusive,
		Body:      cloneBlockOrNil(s.Body),
		SpanV:     s.SpanV,
	}
}

func cloneMatchStmt(s *MatchStmt) *MatchStmt {
	out := &MatchStmt{
		Scrutinee: cloneExpr(s.Scrutinee),
		SpanV:     s.SpanV,
	}
	if len(s.Arms) > 0 {
		out.Arms = make([]*MatchArm, len(s.Arms))
		for i, a := range s.Arms {
			out.Arms[i] = cloneMatchArm(a)
		}
	}
	// Decision tree carries backend-specific bookkeeping; leaving it
	// nil here forces the monomorphised body to be recompiled on demand
	// by backends that rely on arm-by-arm fallback.
	return out
}

// ==== Expressions ====

func cloneExprOrNil(e Expr) Expr {
	if e == nil {
		return nil
	}
	return cloneExpr(e)
}

func cloneExpr(e Expr) Expr {
	if e == nil {
		return nil
	}
	switch e := e.(type) {
	case *IntLit:
		return &IntLit{Text: e.Text, T: CloneType(e.T), SpanV: e.SpanV}
	case *FloatLit:
		return &FloatLit{Text: e.Text, T: CloneType(e.T), SpanV: e.SpanV}
	case *BoolLit:
		return &BoolLit{Value: e.Value, SpanV: e.SpanV}
	case *CharLit:
		return &CharLit{Value: e.Value, SpanV: e.SpanV}
	case *ByteLit:
		return &ByteLit{Value: e.Value, SpanV: e.SpanV}
	case *StringLit:
		return cloneStringLit(e)
	case *UnitLit:
		return &UnitLit{SpanV: e.SpanV}
	case *Ident:
		return &Ident{
			Name:     e.Name,
			Kind:     e.Kind,
			TypeArgs: cloneTypes(e.TypeArgs),
			T:        CloneType(e.T),
			SpanV:    e.SpanV,
		}
	case *UnaryExpr:
		return &UnaryExpr{
			Op:    e.Op,
			X:     cloneExpr(e.X),
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *BinaryExpr:
		return &BinaryExpr{
			Op:    e.Op,
			Left:  cloneExpr(e.Left),
			Right: cloneExpr(e.Right),
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *CallExpr:
		return cloneCallExpr(e)
	case *IntrinsicCall:
		return cloneIntrinsicCall(e)
	case *MethodCall:
		return cloneMethodCall(e)
	case *ListLit:
		return cloneListLit(e)
	case *MapLit:
		return cloneMapLit(e)
	case *TupleLit:
		return cloneTupleLit(e)
	case *StructLit:
		return cloneStructLit(e)
	case *VariantLit:
		return cloneVariantLit(e)
	case *BlockExpr:
		return &BlockExpr{
			Block: cloneBlockOrNil(e.Block),
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *IfExpr:
		return &IfExpr{
			Cond:  cloneExpr(e.Cond),
			Then:  cloneBlockOrNil(e.Then),
			Else:  cloneBlockOrNil(e.Else),
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *IfLetExpr:
		return &IfLetExpr{
			Pattern:   clonePattern(e.Pattern),
			Scrutinee: cloneExpr(e.Scrutinee),
			Then:      cloneBlockOrNil(e.Then),
			Else:      cloneBlockOrNil(e.Else),
			T:         CloneType(e.T),
			SpanV:     e.SpanV,
		}
	case *MatchExpr:
		return cloneMatchExpr(e)
	case *FieldExpr:
		return &FieldExpr{
			X:        cloneExpr(e.X),
			Name:     e.Name,
			Optional: e.Optional,
			T:        CloneType(e.T),
			SpanV:    e.SpanV,
		}
	case *IndexExpr:
		return &IndexExpr{
			X:     cloneExpr(e.X),
			Index: cloneExpr(e.Index),
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *TupleAccess:
		return &TupleAccess{
			X:     cloneExpr(e.X),
			Index: e.Index,
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *RangeLit:
		return &RangeLit{
			Start:     cloneExprOrNil(e.Start),
			End:       cloneExprOrNil(e.End),
			Inclusive: e.Inclusive,
			T:         CloneType(e.T),
			SpanV:     e.SpanV,
		}
	case *QuestionExpr:
		return &QuestionExpr{
			X:     cloneExpr(e.X),
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *CoalesceExpr:
		return &CoalesceExpr{
			Left:  cloneExpr(e.Left),
			Right: cloneExpr(e.Right),
			T:     CloneType(e.T),
			SpanV: e.SpanV,
		}
	case *Closure:
		return cloneClosure(e)
	case *ErrorExpr:
		return &ErrorExpr{Note: e.Note, T: CloneType(e.T), SpanV: e.SpanV}
	}
	return e
}

func cloneStringLit(s *StringLit) *StringLit {
	out := &StringLit{IsRaw: s.IsRaw, IsTriple: s.IsTriple, SpanV: s.SpanV}
	if len(s.Parts) > 0 {
		out.Parts = make([]StringPart, len(s.Parts))
		for i, p := range s.Parts {
			out.Parts[i] = StringPart{
				IsLit: p.IsLit,
				Lit:   p.Lit,
			}
			if !p.IsLit && p.Expr != nil {
				out.Parts[i].Expr = cloneExpr(p.Expr)
			}
		}
	}
	return out
}

func cloneCallExpr(c *CallExpr) *CallExpr {
	out := &CallExpr{
		Callee:   cloneExpr(c.Callee),
		TypeArgs: cloneTypes(c.TypeArgs),
		T:        CloneType(c.T),
		SpanV:    c.SpanV,
	}
	out.Args = cloneArgs(c.Args)
	return out
}

func cloneIntrinsicCall(i *IntrinsicCall) *IntrinsicCall {
	return &IntrinsicCall{
		Kind:  i.Kind,
		Args:  cloneArgs(i.Args),
		SpanV: i.SpanV,
	}
}

func cloneMethodCall(m *MethodCall) *MethodCall {
	out := &MethodCall{
		Receiver: cloneExpr(m.Receiver),
		Name:     m.Name,
		TypeArgs: cloneTypes(m.TypeArgs),
		T:        CloneType(m.T),
		SpanV:    m.SpanV,
	}
	out.Args = cloneArgs(m.Args)
	return out
}

func cloneArgs(as []Arg) []Arg {
	if len(as) == 0 {
		return nil
	}
	out := make([]Arg, len(as))
	for i, a := range as {
		out[i] = Arg{
			Name:  a.Name,
			Value: cloneExpr(a.Value),
			SpanV: a.SpanV,
		}
	}
	return out
}

func cloneListLit(l *ListLit) *ListLit {
	out := &ListLit{Elem: CloneType(l.Elem), SpanV: l.SpanV}
	if len(l.Elems) > 0 {
		out.Elems = make([]Expr, len(l.Elems))
		for i, e := range l.Elems {
			out.Elems[i] = cloneExpr(e)
		}
	}
	return out
}

func cloneMapLit(m *MapLit) *MapLit {
	out := &MapLit{
		KeyT:  CloneType(m.KeyT),
		ValT:  CloneType(m.ValT),
		SpanV: m.SpanV,
	}
	if len(m.Entries) > 0 {
		out.Entries = make([]MapEntry, len(m.Entries))
		for i, en := range m.Entries {
			out.Entries[i] = MapEntry{
				Key:   cloneExpr(en.Key),
				Value: cloneExpr(en.Value),
				SpanV: en.SpanV,
			}
		}
	}
	return out
}

func cloneTupleLit(t *TupleLit) *TupleLit {
	out := &TupleLit{T: CloneType(t.T), SpanV: t.SpanV}
	if len(t.Elems) > 0 {
		out.Elems = make([]Expr, len(t.Elems))
		for i, e := range t.Elems {
			out.Elems[i] = cloneExpr(e)
		}
	}
	return out
}

func cloneStructLit(s *StructLit) *StructLit {
	out := &StructLit{
		TypeName: s.TypeName,
		Spread:   cloneExprOrNil(s.Spread),
		T:        CloneType(s.T),
		SpanV:    s.SpanV,
	}
	if len(s.Fields) > 0 {
		out.Fields = make([]StructLitField, len(s.Fields))
		for i, f := range s.Fields {
			out.Fields[i] = StructLitField{
				Name:  f.Name,
				Value: cloneExprOrNil(f.Value),
				SpanV: f.SpanV,
			}
		}
	}
	return out
}

func cloneVariantLit(v *VariantLit) *VariantLit {
	return &VariantLit{
		Enum:    v.Enum,
		Variant: v.Variant,
		Args:    cloneArgs(v.Args),
		T:       CloneType(v.T),
		SpanV:   v.SpanV,
	}
}

func cloneMatchExpr(m *MatchExpr) *MatchExpr {
	out := &MatchExpr{
		Scrutinee: cloneExpr(m.Scrutinee),
		T:         CloneType(m.T),
		SpanV:     m.SpanV,
	}
	if len(m.Arms) > 0 {
		out.Arms = make([]*MatchArm, len(m.Arms))
		for i, a := range m.Arms {
			out.Arms[i] = cloneMatchArm(a)
		}
	}
	return out
}

func cloneMatchArm(a *MatchArm) *MatchArm {
	if a == nil {
		return nil
	}
	return &MatchArm{
		Pattern: clonePattern(a.Pattern),
		Guard:   cloneExprOrNil(a.Guard),
		Body:    cloneBlockOrNil(a.Body),
		SpanV:   a.SpanV,
	}
}

func cloneClosure(c *Closure) *Closure {
	out := &Closure{
		Return: CloneType(c.Return),
		Body:   cloneBlockOrNil(c.Body),
		T:      CloneType(c.T),
		SpanV:  c.SpanV,
	}
	if len(c.Params) > 0 {
		out.Params = make([]*Param, len(c.Params))
		for i, p := range c.Params {
			out.Params[i] = cloneParam(p)
		}
	}
	if len(c.Captures) > 0 {
		out.Captures = make([]*Capture, len(c.Captures))
		for i, cap := range c.Captures {
			out.Captures[i] = &Capture{
				Name:  cap.Name,
				Kind:  cap.Kind,
				T:     CloneType(cap.T),
				Mut:   cap.Mut,
				SpanV: cap.SpanV,
			}
		}
	}
	return out
}

// ==== Patterns ====

func clonePattern(p Pattern) Pattern {
	if p == nil {
		return nil
	}
	switch p := p.(type) {
	case *WildPat:
		return &WildPat{SpanV: p.SpanV}
	case *IdentPat:
		return &IdentPat{Name: p.Name, Mut: p.Mut, SpanV: p.SpanV}
	case *LitPat:
		return &LitPat{Value: cloneExprOrNil(p.Value), SpanV: p.SpanV}
	case *TuplePat:
		out := &TuplePat{SpanV: p.SpanV}
		if len(p.Elems) > 0 {
			out.Elems = make([]Pattern, len(p.Elems))
			for i, e := range p.Elems {
				out.Elems[i] = clonePattern(e)
			}
		}
		return out
	case *StructPat:
		out := &StructPat{
			TypeName: p.TypeName,
			Rest:     p.Rest,
			SpanV:    p.SpanV,
		}
		if len(p.Fields) > 0 {
			out.Fields = make([]StructPatField, len(p.Fields))
			for i, f := range p.Fields {
				out.Fields[i] = StructPatField{
					Name:    f.Name,
					Pattern: clonePattern(f.Pattern),
					SpanV:   f.SpanV,
				}
			}
		}
		return out
	case *VariantPat:
		out := &VariantPat{
			Enum:    p.Enum,
			Variant: p.Variant,
			SpanV:   p.SpanV,
		}
		if len(p.Args) > 0 {
			out.Args = make([]Pattern, len(p.Args))
			for i, a := range p.Args {
				out.Args[i] = clonePattern(a)
			}
		}
		return out
	case *RangePat:
		return &RangePat{
			Low:       cloneExprOrNil(p.Low),
			High:      cloneExprOrNil(p.High),
			Inclusive: p.Inclusive,
			SpanV:     p.SpanV,
		}
	case *OrPat:
		out := &OrPat{SpanV: p.SpanV}
		if len(p.Alts) > 0 {
			out.Alts = make([]Pattern, len(p.Alts))
			for i, a := range p.Alts {
				out.Alts[i] = clonePattern(a)
			}
		}
		return out
	case *BindingPat:
		return &BindingPat{
			Name:    p.Name,
			Pattern: clonePattern(p.Pattern),
			SpanV:   p.SpanV,
		}
	case *ErrorPat:
		return &ErrorPat{Note: p.Note, SpanV: p.SpanV}
	}
	return p
}
