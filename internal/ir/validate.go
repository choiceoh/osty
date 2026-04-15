package ir

import "fmt"

// Validate performs a light structural sanity check over an IR Module.
// It does NOT re-run type checking — that's the front-end's job — but
// it does catch internal invariants that a buggy lowerer could
// violate, such as:
//
//   - Expr with nil Type()
//   - Empty Module with a nil Package
//   - FnDecl with a nil Body but not marked as an interface method
//   - MatchArm with nil Pattern or nil Body
//   - LetStmt with both Name == "" and Pattern == nil
//
// The returned slice is empty when the IR is well-formed. Backends
// may call Validate during development (or behind a flag) as cheap
// insurance against regressions in the lowerer.
func Validate(m *Module) []error {
	v := &validator{}
	v.validateModule(m)
	return v.errs
}

type validator struct {
	errs []error
	// methodCtx is true while validating a decl that's a method on a
	// struct/enum/interface (no Body required for interface stubs).
	inInterface bool
}

func (v *validator) addf(format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf(format, args...))
}

func (v *validator) validateModule(m *Module) {
	if m == nil {
		v.addf("nil module")
		return
	}
	if m.Package == "" {
		v.addf("module: empty Package name")
	}
	for i, d := range m.Decls {
		if d == nil {
			v.addf("decl[%d]: nil", i)
			continue
		}
		v.validateDecl(d)
	}
	for i, s := range m.Script {
		if s == nil {
			v.addf("script[%d]: nil", i)
			continue
		}
		v.validateStmt(s)
	}
}

func (v *validator) validateDecl(d Decl) {
	switch d := d.(type) {
	case *FnDecl:
		v.validateFnDecl(d, false)
	case *StructDecl:
		for _, f := range d.Fields {
			if f.Type == nil {
				v.addf("struct %s: field %s has nil Type", d.Name, f.Name)
			}
			if f.Default != nil {
				v.validateExpr(f.Default)
			}
		}
		for _, m := range d.Methods {
			v.validateFnDecl(m, false)
		}
	case *EnumDecl:
		seen := map[string]bool{}
		for _, vr := range d.Variants {
			if vr.Name == "" {
				v.addf("enum %s: variant with empty name", d.Name)
			}
			if seen[vr.Name] {
				v.addf("enum %s: duplicate variant %s", d.Name, vr.Name)
			}
			seen[vr.Name] = true
		}
		for _, m := range d.Methods {
			v.validateFnDecl(m, false)
		}
	case *InterfaceDecl:
		old := v.inInterface
		v.inInterface = true
		for _, m := range d.Methods {
			v.validateFnDecl(m, true)
		}
		v.inInterface = old
	case *TypeAliasDecl:
		if d.Target == nil {
			v.addf("alias %s: nil Target", d.Name)
		}
	case *LetDecl:
		if d.Name == "" {
			v.addf("top-level let: empty Name")
		}
		if d.Value == nil {
			v.addf("top-level let %s: nil Value", d.Name)
		} else {
			v.validateExpr(d.Value)
		}
	case *UseDecl:
		if d.Alias == "" && !d.IsGoFFI {
			v.addf("use: empty Alias")
		}
	}
}

func (v *validator) validateFnDecl(fn *FnDecl, allowNoBody bool) {
	if fn.Name == "" {
		v.addf("fn: empty Name")
	}
	if fn.Return == nil {
		v.addf("fn %s: nil Return", fn.Name)
	}
	for i, p := range fn.Params {
		if p == nil {
			v.addf("fn %s: param[%d] nil", fn.Name, i)
			continue
		}
		if p.Type == nil {
			v.addf("fn %s: param %q nil Type", fn.Name, p.Name)
		}
	}
	if fn.Body == nil {
		if !allowNoBody && !v.inInterface {
			v.addf("fn %s: nil Body", fn.Name)
		}
		return
	}
	v.validateBlock(fn.Body)
}

func (v *validator) validateBlock(b *Block) {
	if b == nil {
		v.addf("nil block")
		return
	}
	for i, s := range b.Stmts {
		if s == nil {
			v.addf("block stmt[%d]: nil", i)
			continue
		}
		v.validateStmt(s)
	}
	if b.Result != nil {
		v.validateExpr(b.Result)
	}
}

func (v *validator) validateStmt(s Stmt) {
	switch s := s.(type) {
	case *Block:
		v.validateBlock(s)
	case *LetStmt:
		if s.Name == "" && s.Pattern == nil {
			v.addf("let: both Name and Pattern empty")
		}
		if s.Value != nil {
			v.validateExpr(s.Value)
		}
	case *ExprStmt:
		if s.X == nil {
			v.addf("ExprStmt: nil X")
		} else {
			v.validateExpr(s.X)
		}
	case *AssignStmt:
		if len(s.Targets) == 0 {
			v.addf("AssignStmt: no targets")
		}
		for _, t := range s.Targets {
			v.validateExpr(t)
		}
		v.validateExpr(s.Value)
	case *ReturnStmt:
		if s.Value != nil {
			v.validateExpr(s.Value)
		}
	case *BreakStmt, *ContinueStmt:
		// leaves
	case *IfStmt:
		if s.Cond == nil {
			v.addf("IfStmt: nil Cond")
		} else {
			v.validateExpr(s.Cond)
		}
		if s.Then == nil {
			v.addf("IfStmt: nil Then")
		} else {
			v.validateBlock(s.Then)
		}
		if s.Else != nil {
			v.validateBlock(s.Else)
		}
	case *ForStmt:
		if s.Body == nil {
			v.addf("ForStmt: nil Body")
		} else {
			v.validateBlock(s.Body)
		}
		switch s.Kind {
		case ForWhile:
			if s.Cond == nil {
				v.addf("ForStmt While: nil Cond")
			} else {
				v.validateExpr(s.Cond)
			}
		case ForIn:
			if s.Iter == nil {
				v.addf("ForStmt In: nil Iter")
			} else {
				v.validateExpr(s.Iter)
			}
		case ForRange:
			if s.Start == nil {
				v.addf("ForStmt Range: nil Start")
			} else {
				v.validateExpr(s.Start)
			}
			if s.End == nil {
				v.addf("ForStmt Range: nil End")
			} else {
				v.validateExpr(s.End)
			}
		}
	case *DeferStmt:
		if s.Body == nil {
			v.addf("DeferStmt: nil Body")
		} else {
			v.validateBlock(s.Body)
		}
	case *ChanSendStmt:
		v.validateExpr(s.Channel)
		v.validateExpr(s.Value)
	case *MatchStmt:
		v.validateExpr(s.Scrutinee)
		for i, a := range s.Arms {
			if a == nil {
				v.addf("MatchStmt arm[%d]: nil", i)
				continue
			}
			v.validateMatchArm(a)
		}
	case *ErrorStmt:
		// allowed by design
	}
}

func (v *validator) validateExpr(e Expr) {
	if e == nil {
		v.addf("nil expression")
		return
	}
	if e.Type() == nil {
		v.addf("%T: nil Type()", e)
	}
	switch e := e.(type) {
	case *UnaryExpr:
		v.validateExpr(e.X)
	case *BinaryExpr:
		v.validateExpr(e.Left)
		v.validateExpr(e.Right)
	case *CallExpr:
		v.validateExpr(e.Callee)
		for _, a := range e.Args {
			v.validateExpr(a)
		}
	case *IntrinsicCall:
		for _, a := range e.Args {
			v.validateExpr(a)
		}
	case *MethodCall:
		v.validateExpr(e.Receiver)
		for _, a := range e.Args {
			v.validateExpr(a)
		}
	case *ListLit:
		for _, el := range e.Elems {
			v.validateExpr(el)
		}
	case *MapLit:
		for _, en := range e.Entries {
			v.validateExpr(en.Key)
			v.validateExpr(en.Value)
		}
	case *TupleLit:
		for _, el := range e.Elems {
			v.validateExpr(el)
		}
	case *StructLit:
		for _, f := range e.Fields {
			if f.Value != nil {
				v.validateExpr(f.Value)
			}
		}
		if e.Spread != nil {
			v.validateExpr(e.Spread)
		}
	case *RangeLit:
		if e.Start != nil {
			v.validateExpr(e.Start)
		}
		if e.End != nil {
			v.validateExpr(e.End)
		}
	case *QuestionExpr:
		v.validateExpr(e.X)
	case *CoalesceExpr:
		v.validateExpr(e.Left)
		v.validateExpr(e.Right)
	case *FieldExpr:
		v.validateExpr(e.X)
	case *TupleAccess:
		v.validateExpr(e.X)
	case *IndexExpr:
		v.validateExpr(e.X)
		v.validateExpr(e.Index)
	case *Closure:
		v.validateBlock(e.Body)
	case *VariantLit:
		for _, a := range e.Args {
			v.validateExpr(a)
		}
	case *BlockExpr:
		v.validateBlock(e.Block)
	case *IfExpr:
		v.validateExpr(e.Cond)
		v.validateBlock(e.Then)
		if e.Else != nil {
			v.validateBlock(e.Else)
		}
	case *IfLetExpr:
		if e.Pattern == nil {
			v.addf("IfLetExpr: nil Pattern")
		}
		v.validateExpr(e.Scrutinee)
		v.validateBlock(e.Then)
		if e.Else != nil {
			v.validateBlock(e.Else)
		}
	case *MatchExpr:
		v.validateExpr(e.Scrutinee)
		for i, a := range e.Arms {
			if a == nil {
				v.addf("MatchExpr arm[%d]: nil", i)
				continue
			}
			v.validateMatchArm(a)
		}
	case *StringLit:
		for _, p := range e.Parts {
			if !p.IsLit && p.Expr != nil {
				v.validateExpr(p.Expr)
			}
		}
	}
}

func (v *validator) validateMatchArm(a *MatchArm) {
	if a.Pattern == nil {
		v.addf("MatchArm: nil Pattern")
	}
	if a.Guard != nil {
		v.validateExpr(a.Guard)
	}
	if a.Body == nil {
		v.addf("MatchArm: nil Body")
	} else {
		v.validateBlock(a.Body)
	}
}
